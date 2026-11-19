package standin

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Push stands in for a UnifiedPush push server: the server a phone's
// UnifiedPush app (its distributor) keeps a connection to, which the plugin
// sends Web Push messages to (RFC 8030, encrypted as RFC 8291 says, signed as
// RFC 8292 says).
//
//	POST /push/<id>                    a Web Push message for the endpoint <id>
//	GET  /fake/push/<id>/next?wait=25  the next message, for the stand-in distributor (a long poll)
//	PUT  /fake/push/<id>?vapid=<key>   the distributor says which VAPID key the app registered
//	DELETE /fake/push/<id>             the distributor dropped the endpoint: its messages are refused with 404
//	GET  /fake/push/<id>               what it holds for one endpoint
//	GET  /fake/push/log                every message it was sent, without what it said
//
// A message must say Content-Encoding: aes128gcm and carry a TTL, and its
// Authorization must be a VAPID token that verifies with its own k, names
// this server's origin as its audience and expires within 24 hours. Where the
// distributor registered a key for the endpoint, a message without
// Authorization, or signed with another key, is answered 401 (as autopush
// does), and one whose Authorization is not a valid VAPID token is answered
// 403 (as NextPush does); where none was registered, Authorization may be left
// out, and an invalid one is still answered 403. A message bigger than 4096
// bytes is answered 413. What it accepts is answered 201 and kept, for its TTL,
// for the distributor's long poll, which takes it (in base64) and leaves the
// next one; a TTL of 0 is kept only when a long poll is waiting.
//
// It is steered while it runs, for one endpoint (id=) or, without id, for every
// endpoint there is and every one to come, each counting its own times:
//
//	curl -X POST 'localhost:7788/fake/push?id=abc&status=410'             the next message is answered 410, and not kept
//	curl -X POST 'localhost:7788/fake/push?status=429&times=3&retry=30'   the next three, with Retry-After: 30
//	curl -X POST 'localhost:7788/fake/push?status=0'                      answered as usual again
//	curl -X POST 'localhost:7788/fake/push?stall=60'                      a tarpit: every answer waits a minute
//	curl -X POST 'localhost:7788/fake/push?hold=900'                      messages are handed over a quarter of an hour late
//	curl -X POST 'localhost:7788/fake/push?mode=ntfy'                     as ntfy: see below; mode=rfc is the usual
//	curl 'localhost:7788/fake/push'                                       everything it holds
//
// In ntfy mode it does what ntfy does with a UnifiedPush message: answers 200,
// reads no TTL, keeps a message 12 hours whatever its TTL, drops (still
// answering 200) what is sent to an endpoint before its first long poll, cuts
// a message bigger than 4096 bytes to 4096 without a word, and reads no
// Authorization (the log still says what it would have found).
//
// The log keeps each message's size, its headers and, of its Authorization,
// only the scheme, whether k is the registered key, whether the signature
// verifies, the audience and how far off the expiry is: never what a message
// says, a token or a key.
type Push struct {
	// Now is the clock messages, expiries and tokens are judged by. time.Now
	// unless a test moves time by hand.
	Now func() time.Time

	mu       sync.Mutex
	ids      map[string]*pushEndpoint
	defaults PushRules
	log      []PushLogEntry
	kept     int // messages kept so far, which numbers them
	mux      *http.ServeMux
}

// PushRules is how the stand-in answers for an endpoint, as steered.
type PushRules struct {
	Mode   string `json:"mode"`                    // rfc | ntfy
	Hold   int    `json:"hold_s,omitempty"`        // a message is handed over this many seconds after it came
	Status int    `json:"status,omitempty"`        // the next Times messages are answered with this, and not kept
	Times  int    `json:"times,omitempty"`         // how many more are answered with Status
	Retry  int    `json:"retry_after_s,omitempty"` // Retry-After on what Status answers
	Stall  int    `json:"stall_s,omitempty"`       // every answer waits this many seconds first
}

// PushState is what the stand-in holds for one endpoint. The registered key
// itself is never shown.
type PushState struct {
	ID      string    `json:"id"`
	VAPID   bool      `json:"vapid"`  // a key was registered for it
	Gone    bool      `json:"gone"`   // the distributor dropped it
	Polled  bool      `json:"polled"` // a long poll has asked for it
	Waiting int       `json:"waiting"`
	Queued  int       `json:"queued"`
	Rules   PushRules `json:"rules"`
}

// PushLogEntry is one message the stand-in was sent.
type PushLogEntry struct {
	At      time.Time         `json:"at"`
	ID      string            `json:"id"`
	Status  int               `json:"status"` // 0: the sender gave up before it was answered
	Problem string            `json:"problem,omitempty"`
	Size    int               `json:"size"`
	Kept    bool              `json:"kept"`
	Headers map[string]string `json:"headers"` // every header but Authorization
	// Authorization is null when the message had none.
	Authorization *PushAuth `json:"authorization"`
}

// PushAuth is what the log says of a message's Authorization.
type PushAuth struct {
	Scheme    string `json:"scheme"`
	KMatches  *bool  `json:"k_matches"` // null when no key was registered
	Signature string `json:"signature"` // ok | bad | malformed
	Aud       string `json:"aud"`
	ExpInS    *int64 `json:"exp_in_s"`
}

type pushEndpoint struct {
	id      string
	vapid   string // the key the distributor registered, as it gave it; "" for none
	gone    bool
	polled  bool
	waiting int
	rules   PushRules
	queue   []pushMessage
	wake    chan struct{} // closed, and replaced, whenever something changes
}

type pushMessage struct {
	n       int
	body    []byte
	ready   time.Time
	expires time.Time
}

const (
	pushModeRFC  = "rfc"
	pushModeNtfy = "ntfy"

	// The most a Web Push message may be (RFC 8030 section 7.2, and what
	// every UnifiedPush distributor takes).
	maxPushBody = 4096

	// RFC 8291's message header: salt (16), rs (4), idlen (1), keyid.
	pushHeaderKeyID = 21

	// ntfy keeps a message this long (its DefaultCacheDuration).
	ntfyKept = 12 * time.Hour

	maxPushEndpoints = 1000
	maxPushQueued    = 100
	maxPushLog       = 500
	maxPushWait      = 60
	maxVAPIDAhead    = 24 * time.Hour
)

var pushIDShape = regexp.MustCompile(`^[A-Za-z0-9_-]{1,100}$`)

// NewPush makes a push server that holds nothing yet and answers as RFC 8030
// says.
func NewPush() *Push {
	p := &Push{
		Now:      time.Now,
		ids:      map[string]*pushEndpoint{},
		defaults: PushRules{Mode: pushModeRFC},
		mux:      http.NewServeMux(),
	}
	p.Routes(p.mux)

	return p
}

// Routes adds the push server's addresses, and the stand-in's own, to mux.
func (p *Push) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/push/", p.push)
	mux.HandleFunc("/fake/push", p.steer)
	mux.HandleFunc("/fake/push/", p.distributor)
}

func (p *Push) ServeHTTP(w http.ResponseWriter, r *http.Request) { p.mux.ServeHTTP(w, r) }

// Log is a copy of the log, for a test to read.
func (p *Push) Log() []PushLogEntry {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]PushLogEntry(nil), p.log...)
}

// endpointFor is the endpoint id, made when it is new; nil when there are as
// many as the stand-in keeps. With mu held.
func (p *Push) endpointFor(id string) *pushEndpoint {
	if e, known := p.ids[id]; known {
		return e
	}
	if len(p.ids) >= maxPushEndpoints {
		return nil
	}
	e := &pushEndpoint{id: id, rules: p.defaults, wake: make(chan struct{})}
	p.ids[id] = e

	return e
}

// changed wakes every long poll on e. With mu held.
func (e *pushEndpoint) changed() {
	close(e.wake)
	e.wake = make(chan struct{})
}

func (e *pushEndpoint) state() PushState {
	return PushState{ID: e.id, VAPID: e.vapid != "", Gone: e.gone, Polled: e.polled, Waiting: e.waiting, Queued: len(e.queue), Rules: e.rules}
}

// ---------------------------------------------------------------- the push server

func (p *Push) push(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "A push message is a POST.", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/push/")
	if !pushIDShape.MatchString(id) {
		http.Error(w, "No such endpoint.", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxPushBody+1))
	if err != nil {
		http.Error(w, "The message could not be read.", http.StatusBadRequest)
		return
	}

	// A tarpit waits before it judges anything, so a sender that gives up
	// has had nothing kept.
	p.mu.Lock()
	stall := 0
	if e := p.endpointFor(id); e != nil {
		stall = e.rules.Stall
	}
	p.mu.Unlock()
	if stall > 0 {
		timer := time.NewTimer(time.Duration(stall) * time.Second)
		select {
		case <-timer.C:
		case <-r.Context().Done():
			timer.Stop()
			p.mu.Lock()
			p.record(PushLogEntry{At: p.Now(), ID: id, Problem: "the sender gave up while it stalled", Size: len(body), Headers: loggedHeaders(r.Header),
				Authorization: inspectVAPID(r.Header.Get("Authorization"), requestOrigin(r), p.Now(), "", body).auth})
			p.mu.Unlock()
			return
		}
	}

	p.mu.Lock()
	answer := p.judge(r, id, body)
	p.mu.Unlock()

	for name, value := range answer.headers {
		w.Header().Set(name, value)
	}
	if answer.json != nil {
		writeJSON(w, answer.status, answer.json)
		return
	}
	if answer.problem != "" && answer.status >= 300 {
		http.Error(w, answer.problem, answer.status)
		return
	}
	w.WriteHeader(answer.status)
}

type pushAnswer struct {
	status  int
	problem string
	headers map[string]string
	json    any
}

// judge decides what a message is answered with, keeps it when it is
// accepted and logs it. With mu held.
func (p *Push) judge(r *http.Request, id string, body []byte) pushAnswer {
	now := p.Now()
	entry := PushLogEntry{At: now, ID: id, Size: len(body), Headers: loggedHeaders(r.Header)}
	answer := func(status int, problem string) pushAnswer {
		entry.Status, entry.Problem = status, problem
		p.record(entry)

		return pushAnswer{status: status, problem: problem}
	}

	e := p.endpointFor(id)
	if e == nil {
		return answer(http.StatusServiceUnavailable, "The stand-in holds as many endpoints as it keeps.")
	}
	ntfy := e.rules.Mode == pushModeNtfy

	vapid := inspectVAPID(r.Header.Get("Authorization"), requestOrigin(r), now, e.vapid, body)
	entry.Authorization = vapid.auth

	if e.gone {
		return answer(http.StatusNotFound, "This endpoint was dropped by its distributor.")
	}
	if len(body) > maxPushBody {
		if !ntfy {
			return answer(http.StatusRequestEntityTooLarge, "A push message is at most 4096 bytes.")
		}
		body = body[:maxPushBody]
	}
	if encodings := r.Header.Values("Content-Encoding"); len(encodings) != 1 || !strings.EqualFold(strings.TrimSpace(encodings[0]), "aes128gcm") {
		return answer(http.StatusBadRequest, "Content-Encoding must be exactly aes128gcm.")
	}
	ttl := 0
	if !ntfy {
		raw := strings.TrimSpace(r.Header.Get("TTL"))
		parsed, parseErr := strconv.Atoi(raw)
		if raw == "" || parseErr != nil || parsed < 0 || strings.Trim(raw, "0123456789") != "" {
			return answer(http.StatusBadRequest, "A push message needs a TTL: a number of seconds.")
		}
		ttl = parsed
	}
	if !ntfy {
		switch vapid.verdict {
		case vapidAbsent:
			if e.vapid != "" {
				return answer(http.StatusUnauthorized, "This endpoint takes only messages signed with the key it was registered with: there is no Authorization.")
			}
		case vapidInvalid:
			return answer(http.StatusForbidden, vapid.problem)
		case vapidSigningKeyIsTheMessageKey:
			return answer(http.StatusBadRequest, vapid.problem)
		case vapidKeyDiffers:
			return answer(http.StatusUnauthorized, vapid.problem)
		case vapidValid:
		}
	}

	if e.rules.Status != 0 && e.rules.Times > 0 {
		status, retry := e.rules.Status, e.rules.Retry
		if e.rules.Times--; e.rules.Times == 0 {
			e.rules.Status, e.rules.Retry = 0, 0
		}
		out := answer(status, "Steered to answer this.")
		if retry > 0 {
			out.headers = map[string]string{"Retry-After": strconv.Itoa(retry)}
		}

		return out
	}

	ready := now.Add(time.Duration(e.rules.Hold) * time.Second)
	switch {
	case ntfy && !e.polled:
		entry.Problem = "dropped: nobody had asked for this endpoint yet, as ntfy does"
	case ntfy:
		p.keep(e, body, ready, now.Add(ntfyKept))
		entry.Kept = true
	case ttl == 0 && (e.waiting == 0 || e.rules.Hold > 0):
		entry.Problem = "dropped: a TTL of 0 and nobody waiting for it"
	default:
		// A TTL of 0 is kept for the long poll waiting now, and no longer.
		expires := ready.Add(time.Duration(ttl) * time.Second)
		if ttl == 0 {
			expires = ready.Add(time.Second)
		}
		p.keep(e, body, ready, expires)
		entry.Kept = true
	}

	if ntfy {
		entry.Status = http.StatusOK
		p.record(entry)

		return pushAnswer{status: http.StatusOK, json: map[string]any{"id": fmt.Sprintf("m%d", p.kept), "time": now.Unix(), "event": "message", "topic": id}}
	}
	entry.Status = http.StatusCreated
	p.record(entry)

	return pushAnswer{status: http.StatusCreated, headers: map[string]string{
		"Location": "/push/" + id + "/m" + strconv.Itoa(p.kept),
		"TTL":      strconv.Itoa(ttl),
	}}
}

// keep queues a message for the long poll. With mu held.
func (p *Push) keep(e *pushEndpoint, body []byte, ready, expires time.Time) {
	p.kept++
	e.queue = append(e.queue, pushMessage{n: p.kept, body: append([]byte(nil), body...), ready: ready, expires: expires})
	if len(e.queue) > maxPushQueued {
		e.queue = e.queue[len(e.queue)-maxPushQueued:]
	}
	e.changed()
}

// record adds an entry to the log. With mu held.
func (p *Push) record(entry PushLogEntry) {
	p.log = append(p.log, entry)
	if len(p.log) > maxPushLog {
		p.log = p.log[len(p.log)-maxPushLog:]
	}
}

// loggedHeaders is every header the log may show: all but the ones that
// carry a credential.
func loggedHeaders(header http.Header) map[string]string {
	out := map[string]string{}
	for name, values := range header {
		switch http.CanonicalHeaderKey(name) {
		case "Authorization", "Proxy-Authorization", "Cookie":
			continue
		}
		out[name] = strings.Join(values, ", ")
	}

	return out
}

// requestOrigin is this server's origin as the sender reached it, the
// audience a VAPID token for it must name.
func requestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return originOf(scheme, r.Host)
}

// originOf is an origin as RFC 6454 serialises it: lower case, and without
// the scheme's default port.
func originOf(scheme, host string) string {
	scheme, host = strings.ToLower(scheme), strings.ToLower(host)
	if (scheme == "http" && strings.HasSuffix(host, ":80")) || (scheme == "https" && strings.HasSuffix(host, ":443")) {
		host = host[:strings.LastIndex(host, ":")]
	}

	return scheme + "://" + host
}

// ---------------------------------------------------------------- VAPID (RFC 8292)

type vapidVerdict int

const (
	vapidAbsent vapidVerdict = iota
	vapidValid
	vapidInvalid
	vapidKeyDiffers
	vapidSigningKeyIsTheMessageKey
)

type vapidResult struct {
	verdict vapidVerdict
	problem string // a sentence that names no value
	auth    *PushAuth
}

// inspectVAPID judges a message's Authorization as RFC 8292 says a push
// server does: the scheme vapid with t (a JWT) and k (the key that signed
// it, an uncompressed P-256 point in base64url); a JWS header naming ES256; a
// signature of 64 bytes, R then S, that verifies with k over the first two
// parts; an aud that is this server's origin; an exp in the future and no
// more than 24 hours off; a sub, when there is one, that is a mailto: or
// https: address. registered is the key the endpoint was registered with, ""
// for none; body is the message, whose own key must not be k (section 3.2).
func inspectVAPID(header, origin string, now time.Time, registered string, body []byte) vapidResult {
	if strings.TrimSpace(header) == "" {
		return vapidResult{verdict: vapidAbsent, problem: "There is no Authorization."}
	}
	auth := &PushAuth{Signature: "malformed"}
	invalid := func(problem string) vapidResult {
		return vapidResult{verdict: vapidInvalid, problem: problem, auth: auth}
	}

	scheme, rest, _ := strings.Cut(strings.TrimSpace(header), " ")
	auth.Scheme = clip(strings.ToLower(scheme), 20)
	if auth.Scheme != "vapid" {
		return invalid("The Authorization scheme is not vapid.")
	}
	var token, key string
	for _, param := range strings.Split(rest, ",") {
		name, value, found := strings.Cut(strings.TrimSpace(param), "=")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && strings.HasPrefix(value, `"`) && strings.HasSuffix(value, `"`) {
			value = value[1 : len(value)-1]
		}
		// Unknown parameters are ignored, as section 3 says.
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "t":
			token = value
		case "k":
			key = value
		}
	}
	if token == "" || key == "" {
		return invalid("The vapid Authorization needs both t and k.")
	}
	if registered != "" {
		matches := key == registered
		auth.KMatches = &matches
	}

	rawKey, err := base64.RawURLEncoding.Strict().DecodeString(key)
	if err != nil {
		return invalid("k is not base64url without padding.")
	}
	public, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), rawKey)
	if err != nil {
		return invalid("k is not an uncompressed P-256 public key.")
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return invalid("t is not a JWT in three parts.")
	}
	var jose struct {
		Typ string `json:"typ"`
		Alg string `json:"alg"`
	}
	if !decodePart(parts[0], &jose) {
		return invalid("The JWT's header is not base64url JSON.")
	}
	if jose.Alg != "ES256" || (jose.Typ != "" && !strings.EqualFold(jose.Typ, "JWT")) {
		return invalid("The JWT is not signed with ES256.")
	}
	var claims struct {
		Aud *string      `json:"aud"`
		Exp *json.Number `json:"exp"`
		Sub *string      `json:"sub"`
	}
	if !decodePart(parts[1], &claims) {
		return invalid("The JWT's claims are not base64url JSON.")
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil || len(signature) != 64 {
		return invalid("The JWT's signature is not 64 bytes, R then S, in base64url.")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if ecdsa.Verify(public, digest[:], new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
		auth.Signature = "ok"
	} else {
		auth.Signature = "bad"
	}

	if claims.Aud != nil {
		auth.Aud = clip(*claims.Aud, 200)
	}
	var exp int64
	if claims.Exp != nil {
		if exp, err = claims.Exp.Int64(); err == nil {
			ahead := exp - now.Unix()
			auth.ExpInS = &ahead
		}
	}

	switch {
	case auth.Signature != "ok":
		return invalid("The JWT's signature does not verify with k.")
	case claims.Aud == nil || !sameOrigin(*claims.Aud, origin):
		return invalid("The JWT's aud is not this push server's origin.")
	case claims.Exp == nil || auth.ExpInS == nil:
		return invalid("The JWT has no exp in whole seconds.")
	case exp <= now.Unix():
		return invalid("The JWT has expired.")
	case time.Duration(exp-now.Unix())*time.Second > maxVAPIDAhead:
		return invalid("The JWT expires more than 24 hours from now.")
	case claims.Sub != nil && !strings.HasPrefix(*claims.Sub, "mailto:") && !strings.HasPrefix(*claims.Sub, "https:"):
		return invalid("The JWT's sub is neither a mailto: nor an https: address.")
	}

	if len(body) >= pushHeaderKeyID+65 && body[pushHeaderKeyID-1] == 65 && bytes.Equal(body[pushHeaderKeyID:pushHeaderKeyID+65], rawKey) {
		return vapidResult{verdict: vapidSigningKeyIsTheMessageKey, problem: "The message is encrypted with the key that signs it.", auth: auth}
	}
	if registered != "" && key != registered {
		return vapidResult{verdict: vapidKeyDiffers, problem: "The message is signed with another key than the one this endpoint was registered with.", auth: auth}
	}

	return vapidResult{verdict: vapidValid, auth: auth}
}

// decodePart decodes one base64url part of a JWT as JSON into v.
func decodePart(part string, v any) bool {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(part)
	if err != nil {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	return decoder.Decode(v) == nil
}

// sameOrigin is whether aud is the origin given: a scheme and a host, with
// nothing after them.
func sameOrigin(aud, origin string) bool {
	parsed, err := url.Parse(aud)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}

	return originOf(parsed.Scheme, parsed.Host) == origin
}

func clip(text string, most int) string {
	if len(text) > most {
		return text[:most]
	}

	return text
}

// ---------------------------------------------------------------- the distributor's side

// distributor answers /fake/push/...: the log, and the stand-in
// distributor's own calls for one endpoint.
func (p *Push) distributor(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/fake/push/")
	if rest == "log" {
		p.showLog(w, r)
		return
	}
	id, next, _ := strings.Cut(rest, "/")
	if !pushIDShape.MatchString(id) || (next != "" && next != "next") {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "/fake/push/<id>, /fake/push/<id>/next or /fake/push/log"})
		return
	}
	if next == "next" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		p.next(w, r, id)
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	switch r.Method {
	case http.MethodPut:
		vapid := r.URL.Query().Get("vapid")
		if vapid != "" {
			raw, err := base64.RawURLEncoding.Strict().DecodeString(vapid)
			if err == nil {
				_, err = ecdsa.ParseUncompressedPublicKey(elliptic.P256(), raw)
			}
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "vapid is an uncompressed P-256 public key in base64url without padding (87 characters), or empty for none"})
				return
			}
		}
		e := p.endpointFor(id)
		if e == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the stand-in holds as many endpoints as it keeps"})
			return
		}
		if e.gone {
			e.gone, e.polled, e.queue = false, false, nil
		}
		e.vapid = vapid
		e.changed()
		writeJSON(w, http.StatusOK, e.state())
	case http.MethodDelete:
		e := p.endpointFor(id)
		if e == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the stand-in holds as many endpoints as it keeps"})
			return
		}
		e.gone, e.vapid, e.queue = true, "", nil
		e.changed()
		writeJSON(w, http.StatusOK, e.state())
	case http.MethodGet:
		e, known := p.ids[id]
		if !known {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "nothing has been sent to or registered for this endpoint"})
			return
		}
		writeJSON(w, http.StatusOK, e.state())
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// next is the long poll: the next message ready for id, taken from the
// queue, as {"id","body"} with the body in base64 (standard, padded); 204
// when none came within wait seconds; 404 once the endpoint is dropped.
func (p *Push) next(w http.ResponseWriter, r *http.Request, id string) {
	wait := 25
	if raw := r.URL.Query().Get("wait"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 || parsed > maxPushWait {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "wait is 0 to 60 seconds"})
			return
		}
		wait = parsed
	}
	deadline := time.NewTimer(time.Duration(wait) * time.Second)
	defer deadline.Stop()

	p.mu.Lock()
	e := p.endpointFor(id)
	if e == nil {
		p.mu.Unlock()
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the stand-in holds as many endpoints as it keeps"})
		return
	}
	e.polled = true
	e.waiting++
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		e.waiting--
		p.mu.Unlock()
	}()

	for {
		p.mu.Lock()
		now := p.Now()
		gone := e.gone
		message, readyAt := e.take(now)
		wake := e.wake
		p.mu.Unlock()

		switch {
		case gone:
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "this endpoint was dropped"})
			return
		case message != nil:
			writeJSON(w, http.StatusOK, map[string]string{"id": "m" + strconv.Itoa(message.n), "body": base64.StdEncoding.EncodeToString(message.body)})
			return
		}

		var held <-chan time.Time
		var timer *time.Timer
		if !readyAt.IsZero() {
			timer = time.NewTimer(readyAt.Sub(now))
			held = timer.C
		}
		select {
		case <-wake:
		case <-held:
		case <-deadline.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-r.Context().Done():
			return
		}
		if timer != nil {
			timer.Stop()
		}
	}
}

// take removes what has expired and the first message that is ready, and
// returns that; otherwise nil and when the next held one will be ready (zero
// for none). With mu held.
func (e *pushEndpoint) take(now time.Time) (*pushMessage, time.Time) {
	var readyAt time.Time
	kept := e.queue[:0]
	var taken *pushMessage
	for i := range e.queue {
		message := e.queue[i]
		switch {
		case !now.Before(message.expires):
			continue
		case taken == nil && !now.Before(message.ready):
			taken = &message
			continue
		case now.Before(message.ready) && (readyAt.IsZero() || message.ready.Before(readyAt)):
			readyAt = message.ready
		}
		kept = append(kept, message)
	}
	e.queue = kept
	if taken != nil {
		return taken, time.Time{}
	}

	return nil, readyAt
}

// ---------------------------------------------------------------- steering

func (p *Push) showLog(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()

	id := r.URL.Query().Get("id")
	entries := []PushLogEntry{}
	for _, entry := range p.log {
		if id == "" || entry.ID == id {
			entries = append(entries, entry)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// steer answers /fake/push: a GET says everything held, a POST changes how
// one endpoint (id=) or every endpoint is answered, and says the same. Only
// what the address names is changed.
func (p *Push) steer(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if r.Method == http.MethodPost {
		query := r.URL.Query()
		number := func(name string, low, high int) (int, bool) {
			value, err := strconv.Atoi(query.Get(name))
			return value, err == nil && value >= low && value <= high
		}
		var change []func(*PushRules)
		bad := func(message string) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": message})
		}

		if query.Has("status") {
			status, ok := number("status", 0, 599)
			if query.Get("status") == "" {
				status, ok = 0, true
			}
			if !ok || (status != 0 && status < 200) {
				bad("status is what the next messages are answered with, 200 to 599, or 0 to answer as usual")
				return
			}
			times := 1
			if query.Has("times") {
				if times, ok = number("times", 1, 1000); !ok {
					bad("times is how many messages are answered with status, 1 to 1000")
					return
				}
			}
			retry := 0
			if query.Has("retry") {
				if retry, ok = number("retry", 0, 3600); !ok {
					bad("retry is the Retry-After given with status, 0 to 3600 seconds")
					return
				}
			}
			change = append(change, func(rules *PushRules) {
				if status == 0 {
					rules.Status, rules.Times, rules.Retry = 0, 0, 0
				} else {
					rules.Status, rules.Times, rules.Retry = status, times, retry
				}
			})
		}
		if query.Has("mode") {
			mode := query.Get("mode")
			if mode != pushModeRFC && mode != pushModeNtfy {
				bad("mode is rfc or ntfy")
				return
			}
			change = append(change, func(rules *PushRules) { rules.Mode = mode })
		}
		if query.Has("hold") {
			hold, ok := number("hold", 0, 86400)
			if !ok {
				bad("hold is how many seconds late a message is handed over, 0 to 86400")
				return
			}
			change = append(change, func(rules *PushRules) { rules.Hold = hold })
		}
		if query.Has("stall") {
			stall, ok := number("stall", 0, 600)
			if !ok {
				bad("stall is how many seconds every answer waits, 0 to 600")
				return
			}
			change = append(change, func(rules *PushRules) { rules.Stall = stall })
		}

		var targets []*pushEndpoint
		if query.Has("id") {
			id := query.Get("id")
			if !pushIDShape.MatchString(id) {
				bad("id is an endpoint's last part: letters, digits, - and _")
				return
			}
			e := p.endpointFor(id)
			if e == nil {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "the stand-in holds as many endpoints as it keeps"})
				return
			}
			targets = []*pushEndpoint{e}
		} else {
			for _, apply := range change {
				apply(&p.defaults)
			}
			for _, e := range p.ids {
				targets = append(targets, e)
			}
		}
		for _, e := range targets {
			for _, apply := range change {
				apply(&e.rules)
			}
			e.changed()
		}
	}

	states := []PushState{}
	for _, e := range p.ids {
		states = append(states, e.state())
	}
	sort.Slice(states, func(i, j int) bool { return states[i].ID < states[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{"defaults": p.defaults, "endpoints": states})
}
