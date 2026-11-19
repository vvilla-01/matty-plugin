package standin

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// vapidKey is an application server's signing key, as a test makes one.
type vapidKey struct {
	private *ecdsa.PrivateKey
	public  string // uncompressed, base64url without padding: what k and a registration carry
}

func newVAPIDKey(t *testing.T) vapidKey {
	t.Helper()

	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	public, err := private.PublicKey.Bytes()
	require.NoError(t, err)

	return vapidKey{private: private, public: base64.RawURLEncoding.EncodeToString(public)}
}

func part(t *testing.T, v any) string {
	t.Helper()

	raw, err := json.Marshal(v)
	require.NoError(t, err)

	return base64.RawURLEncoding.EncodeToString(raw)
}

// token is a JWT with this JOSE header and these claims, signed as RFC 8292
// says (R then S, 32 bytes each), or in ASN.1 DER when der is set.
func (k vapidKey) token(t *testing.T, jose, claims map[string]any, der bool) string {
	t.Helper()

	signed := part(t, jose) + "." + part(t, claims)
	digest := sha256.Sum256([]byte(signed))
	r, s, err := ecdsa.Sign(rand.Reader, k.private, digest[:])
	require.NoError(t, err)

	var signature []byte
	if der {
		signature, err = asn1.Marshal(struct{ R, S *big.Int }{r, s})
		require.NoError(t, err)
	} else {
		signature = make([]byte, 64)
		r.FillBytes(signature[:32])
		s.FillBytes(signature[32:])
	}

	return signed + "." + base64.RawURLEncoding.EncodeToString(signature)
}

var es256 = map[string]any{"typ": "JWT", "alg": "ES256"}

// authorization is a good Authorization for a message to origin, signed by k.
func (k vapidKey) authorization(t *testing.T, origin string, now time.Time) string {
	t.Helper()

	claims := map[string]any{"aud": origin, "exp": now.Add(12 * time.Hour).Unix(), "sub": "mailto:admin@example.com"}

	return "vapid t=" + k.token(t, es256, claims, false) + ", k=" + k.public
}

func pushServer(t *testing.T) (*Push, *httptest.Server) {
	t.Helper()

	push := NewPush()
	server := httptest.NewServer(push)
	t.Cleanup(server.Close)

	return push, server
}

// message is something shaped like an RFC 8291 message: a header naming the
// sender's message key (made here, or the one given), and some bytes after it.
func pushBody(t *testing.T, keyID []byte) []byte {
	t.Helper()

	if keyID == nil {
		keyID = mustDecode(t, newVAPIDKey(t).public)
	}
	body := make([]byte, 16, 16+4+1+65+40)
	_, _ = rand.Read(body)
	body = append(body, 0, 0, 16, 0, 65)
	body = append(body, keyID...)
	tail := make([]byte, 40)
	_, _ = rand.Read(tail)

	return append(body, tail...)
}

func mustDecode(t *testing.T, text string) []byte {
	t.Helper()

	raw, err := base64.RawURLEncoding.DecodeString(text)
	require.NoError(t, err)

	return raw
}

// send posts a message to the endpoint id with these headers (an empty value
// leaves the header out) and returns the answer, its body read.
func pushSend(t *testing.T, server *httptest.Server, id string, body []byte, headers map[string]string) (*http.Response, string) {
	t.Helper()

	request, err := http.NewRequest(http.MethodPost, server.URL+"/push/"+id, bytes.NewReader(body))
	require.NoError(t, err)
	for name, value := range headers {
		if value != "" {
			request.Header.Set(name, value)
		}
	}
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	text, err := io.ReadAll(response.Body)
	require.NoError(t, err)

	return response, string(text)
}

// sendAs sends a message as the plugin sends one, signed by k.
func pushSendAs(t *testing.T, server *httptest.Server, k vapidKey, id string, body []byte) *http.Response {
	t.Helper()

	response, _ := pushSend(t, server, id, body, map[string]string{
		"Content-Encoding": "aes128gcm",
		"Content-Type":     "application/octet-stream",
		"TTL":              "86400",
		"Urgency":          "high",
		"Authorization":    k.authorization(t, server.URL, time.Now()),
	})

	return response
}

// next long-polls id for wait seconds: the status, and the message's body
// when there was one.
func pushNext(t *testing.T, server *httptest.Server, id string, wait int) (int, []byte) {
	t.Helper()

	response, err := http.Get(server.URL + "/fake/push/" + id + "/next?wait=" + strconv.Itoa(wait))
	require.NoError(t, err)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return response.StatusCode, nil
	}
	var got struct{ ID, Body string }
	require.NoError(t, json.NewDecoder(response.Body).Decode(&got))
	require.NotEmpty(t, got.ID)
	body, err := base64.StdEncoding.DecodeString(got.Body)
	require.NoError(t, err)

	return http.StatusOK, body
}

func pushCall(t *testing.T, method, address string) (int, map[string]any) {
	t.Helper()

	request, err := http.NewRequest(method, address, nil)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	var got map[string]any
	_ = json.NewDecoder(response.Body).Decode(&got)

	return response.StatusCode, got
}

func pushRegister(t *testing.T, server *httptest.Server, id, vapid string) {
	t.Helper()

	status, _ := pushCall(t, http.MethodPut, server.URL+"/fake/push/"+id+"?vapid="+vapid)
	require.Equal(t, http.StatusOK, status)
}

// A message signed as RFC 8292 says, to an endpoint registered with its key,
// is answered 201 and handed to the distributor's long poll once, byte for
// byte.
func TestPushHandsOverASignedMessage(t *testing.T) {
	_, server := pushServer(t)
	k := newVAPIDKey(t)
	pushRegister(t, server, "abc", k.public)

	body := pushBody(t, nil)
	response := pushSendAs(t, server, k, "abc", body)
	require.Equal(t, http.StatusCreated, response.StatusCode)
	require.True(t, strings.HasPrefix(response.Header.Get("Location"), "/push/abc/"))
	require.Equal(t, "86400", response.Header.Get("TTL"))

	status, got := pushNext(t, server, "abc", 0)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, body, got)

	status, _ = pushNext(t, server, "abc", 0)
	require.Equal(t, http.StatusNoContent, status, "a message is handed over once")

	// An endpoint nobody registered a key for takes a signed message too.
	require.Equal(t, http.StatusCreated, pushSendAs(t, server, k, "other", body).StatusCode)
}

// The check is RFC 8292's own: its example token verifies with its example
// key while it was good, and not once it expired or with a byte changed.
func TestPushVerifiesRFC8292sExample(t *testing.T) {
	const (
		token = "eyJ0eXAiOiJKV1QiLCJhbGciOiJFUzI1NiJ9." +
			"eyJhdWQiOiJodHRwczovL3B1c2guZXhhbXBsZS5uZXQiLCJleHAiOjE0NTM1MjM3NjgsInN1YiI6Im1haWx0bzpwdXNoQGV4YW1wbGUuY29tIn0." +
			"i3CYb7t4xfxCDquptFOepC9GAu_HLGkMlMuCGSK2rpiUfnK9ojFwDXb1JrErtmysazNjjvW2L9OkSSHzvoD1oA"
		key    = "BA1Hxzyi1RUM1b5wjxsn7nGxAszw2u61m164i3MrAIxHF6YK5h4SDYic-dRuU_RCPCfA5aq9ojSwk5Y2EmClBPs"
		origin = "https://push.example.net"
	)
	good := time.Unix(1453523768, 0).Add(-time.Hour)

	result := inspectVAPID("vapid t="+token+", k="+key, origin, good, key, nil)
	require.Equal(t, vapidValid, result.verdict, result.problem)
	require.Equal(t, "ok", result.auth.Signature)
	require.Equal(t, origin, result.auth.Aud)
	require.Equal(t, int64(3600), *result.auth.ExpInS)
	require.True(t, *result.auth.KMatches)

	// The parameters in another order, quoted, and with one nobody knows.
	result = inspectVAPID(`vapid k="`+key+`", realm=x, t=`+token, origin, good, "", nil)
	require.Equal(t, vapidValid, result.verdict, result.problem)

	result = inspectVAPID("vapid t="+token+", k="+key, origin, time.Unix(1453523768, 0), "", nil)
	require.Equal(t, vapidInvalid, result.verdict)
	require.Contains(t, result.problem, "expired")

	result = inspectVAPID("vapid t="+token+", k="+key, "https://push.example.org", good, "", nil)
	require.Equal(t, vapidInvalid, result.verdict)
	require.Contains(t, result.problem, "aud")

	changed := []byte(token)
	changed[len(changed)-3] ^= 'A' ^ 'B'
	result = inspectVAPID("vapid t="+string(changed)+", k="+key, origin, good, "", nil)
	require.Equal(t, vapidInvalid, result.verdict)
	require.Equal(t, "bad", result.auth.Signature)
}

// What a real push server refuses is refused, with the status the design
// gives, and nothing refused is kept.
func TestPushRefusesWhatAPushServerRefuses(t *testing.T) {
	_, server := pushServer(t)
	k, other := newVAPIDKey(t), newVAPIDKey(t)
	pushRegister(t, server, "kept", k.public)

	now := time.Now()
	claims := func(change func(map[string]any)) map[string]any {
		c := map[string]any{"aud": server.URL, "exp": now.Add(time.Hour).Unix(), "sub": "mailto:admin@example.com"}
		if change != nil {
			change(c)
		}
		return c
	}
	signed := func(jose, c map[string]any) string {
		return "vapid t=" + k.token(t, jose, c, false) + ", k=" + k.public
	}
	good := signed(es256, claims(nil))
	tampered := []byte(good)
	tampered[strings.Index(good, ",")-2] ^= 'A' ^ 'B'

	for _, test := range []struct {
		name, encoding, ttl, authorization string
		body                               []byte
		status                             int
	}{
		{"no Content-Encoding", "", "60", good, nil, http.StatusBadRequest},
		{"the draft's aesgcm", "aesgcm", "60", good, nil, http.StatusBadRequest},
		{"no TTL", "aes128gcm", "", good, nil, http.StatusBadRequest},
		{"a TTL that is no number", "aes128gcm", "soon", good, nil, http.StatusBadRequest},
		{"a negative TTL", "aes128gcm", "-1", good, nil, http.StatusBadRequest},
		{"more than 4096 bytes", "aes128gcm", "60", good, make([]byte, 4097), http.StatusRequestEntityTooLarge},
		{"no Authorization", "aes128gcm", "60", "", nil, http.StatusUnauthorized},
		{"signed with another key", "aes128gcm", "60", other.authorization(t, server.URL, now), nil, http.StatusUnauthorized},
		{"a signature that does not verify", "aes128gcm", "60", string(tampered), nil, http.StatusForbidden},
		{"a signature in DER", "aes128gcm", "60", "vapid t=" + k.token(t, es256, claims(nil), true) + ", k=" + k.public, nil, http.StatusForbidden},
		{"another algorithm", "aes128gcm", "60", signed(map[string]any{"typ": "JWT", "alg": "HS256"}, claims(nil)), nil, http.StatusForbidden},
		{"another origin", "aes128gcm", "60", signed(es256, claims(func(c map[string]any) { c["aud"] = "https://push.example.net" })), nil, http.StatusForbidden},
		{"an origin with a path", "aes128gcm", "60", signed(es256, claims(func(c map[string]any) { c["aud"] = server.URL + "/push/kept" })), nil, http.StatusForbidden},
		{"no aud", "aes128gcm", "60", signed(es256, claims(func(c map[string]any) { delete(c, "aud") })), nil, http.StatusForbidden},
		{"expired", "aes128gcm", "60", signed(es256, claims(func(c map[string]any) { c["exp"] = now.Add(-time.Minute).Unix() })), nil, http.StatusForbidden},
		{"more than 24 hours off", "aes128gcm", "60", signed(es256, claims(func(c map[string]any) { c["exp"] = now.Add(25 * time.Hour).Unix() })), nil, http.StatusForbidden},
		{"no exp", "aes128gcm", "60", signed(es256, claims(func(c map[string]any) { delete(c, "exp") })), nil, http.StatusForbidden},
		{"a sub that is no address", "aes128gcm", "60", signed(es256, claims(func(c map[string]any) { c["sub"] = "admin@example.com" })), nil, http.StatusForbidden},
		{"k with padding", "aes128gcm", "60", "vapid t=" + k.token(t, es256, claims(nil), false) + ", k=" + k.public + "=", nil, http.StatusForbidden},
		{"no k", "aes128gcm", "60", "vapid t=" + k.token(t, es256, claims(nil), false), nil, http.StatusForbidden},
		{"another scheme", "aes128gcm", "60", "WebPush " + k.token(t, es256, claims(nil), false), nil, http.StatusForbidden},
		{"a bearer token", "aes128gcm", "60", "Bearer abc", nil, http.StatusForbidden},
		{"encrypted with the signing key", "aes128gcm", "60", good, pushBody(t, mustDecode(t, k.public)), http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := test.body
			if body == nil {
				body = pushBody(t, nil)
			}
			response, text := pushSend(t, server, "kept", body, map[string]string{"Content-Encoding": test.encoding, "TTL": test.ttl, "Authorization": test.authorization})
			require.Equal(t, test.status, response.StatusCode, text)
			for _, secret := range []string{k.public, other.public, "t="} {
				require.NotContains(t, text, secret, "a refusal names no value")
			}
			status, _ := pushNext(t, server, "kept", 0)
			require.Equal(t, http.StatusNoContent, status, "nothing refused is kept")
		})
	}

	// Where no key was registered, Authorization may be left out, but one that
	// does not verify is still refused.
	response, _ := pushSend(t, server, "open", pushBody(t, nil), map[string]string{"Content-Encoding": "aes128gcm", "TTL": "60"})
	require.Equal(t, http.StatusCreated, response.StatusCode)
	response, _ = pushSend(t, server, "open", pushBody(t, nil), map[string]string{"Content-Encoding": "aes128gcm", "TTL": "60", "Authorization": string(tampered)})
	require.Equal(t, http.StatusForbidden, response.StatusCode)

	// The key the distributor registers is a key.
	status, _ := pushCall(t, http.MethodPut, server.URL+"/fake/push/kept?vapid=not-a-key")
	require.Equal(t, http.StatusBadRequest, status)
}

// Steered answers: a status for so many messages, with Retry-After, for one
// endpoint or for all of them, and then the usual answer again.
func TestPushAnswersAsSteered(t *testing.T) {
	_, server := pushServer(t)
	k := newVAPIDKey(t)

	status, _ := pushCall(t, http.MethodPost, server.URL+"/fake/push?id=abc&status=429&times=2&retry=7")
	require.Equal(t, http.StatusOK, status)
	for range 2 {
		response := pushSendAs(t, server, k, "abc", pushBody(t, nil))
		require.Equal(t, http.StatusTooManyRequests, response.StatusCode)
		require.Equal(t, "7", response.Header.Get("Retry-After"))
	}
	require.Equal(t, http.StatusCreated, pushSendAs(t, server, k, "abc", pushBody(t, nil)).StatusCode)
	require.Equal(t, http.StatusCreated, pushSendAs(t, server, k, "elsewhere", pushBody(t, nil)).StatusCode, "id= steers that endpoint only")

	// Without id: every endpoint there is and every one to come.
	pushCall(t, http.MethodPost, server.URL+"/fake/push?status=410&times=1")
	require.Equal(t, http.StatusGone, pushSendAs(t, server, k, "abc", pushBody(t, nil)).StatusCode)
	require.Equal(t, http.StatusGone, pushSendAs(t, server, k, "new", pushBody(t, nil)).StatusCode)
	require.Equal(t, http.StatusCreated, pushSendAs(t, server, k, "abc", pushBody(t, nil)).StatusCode)

	pushCall(t, http.MethodPost, server.URL+"/fake/push?status=500&times=5")
	pushCall(t, http.MethodPost, server.URL+"/fake/push?status=0")
	require.Equal(t, http.StatusCreated, pushSendAs(t, server, k, "abc", pushBody(t, nil)).StatusCode, "status=0 answers as usual again")

	for _, bad := range []string{"status=99", "status=600", "status=500&times=0", "mode=fcm", "hold=-1", "stall=x", "id=a/b&status=500"} {
		status, _ := pushCall(t, http.MethodPost, server.URL+"/fake/push?"+bad)
		require.Equal(t, http.StatusBadRequest, status, bad)
	}
}

// A tarpit: the answer waits, a sender that gives up first has nothing kept,
// and the log says it gave up.
func TestPushStallsLikeATarpit(t *testing.T) {
	push, server := pushServer(t)
	k := newVAPIDKey(t)
	pushCall(t, http.MethodPost, server.URL+"/fake/push?id=slow&stall=30")

	request, err := http.NewRequest(http.MethodPost, server.URL+"/push/slow", bytes.NewReader(pushBody(t, nil)))
	require.NoError(t, err)
	request.Header.Set("Content-Encoding", "aes128gcm")
	request.Header.Set("TTL", "60")
	request.Header.Set("Authorization", k.authorization(t, server.URL, time.Now()))
	started := time.Now()
	_, err = (&http.Client{Timeout: 300 * time.Millisecond}).Do(request)
	require.Error(t, err)
	require.Less(t, time.Since(started), 5*time.Second)

	require.Eventually(t, func() bool {
		for _, entry := range push.Log() {
			if entry.ID == "slow" && entry.Status == 0 && strings.Contains(entry.Problem, "gave up") {
				return true
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond)
	status, _ := pushNext(t, server, "slow", 0)
	require.Equal(t, http.StatusNoContent, status)

	pushCall(t, http.MethodPost, server.URL+"/fake/push?id=slow&stall=1")
	started = time.Now()
	require.Equal(t, http.StatusCreated, pushSendAs(t, server, k, "slow", pushBody(t, nil)).StatusCode)
	require.GreaterOrEqual(t, time.Since(started), time.Second)
}

// The long poll waits for the next message, and a held message is handed
// over late.
func TestPushLongPollWaitsAndHolds(t *testing.T) {
	push, server := pushServer(t)
	k := newVAPIDKey(t)

	var waited sync.WaitGroup
	var status int
	var got []byte
	waited.Add(1)
	go func() {
		defer waited.Done()
		status, got = pushNext(t, server, "abc", 10)
	}()
	require.Eventually(t, func() bool {
		push.mu.Lock()
		defer push.mu.Unlock()
		e, known := push.ids["abc"]
		return known && e.waiting == 1
	}, 5*time.Second, 10*time.Millisecond)

	body := pushBody(t, nil)
	started := time.Now()
	require.Equal(t, http.StatusCreated, pushSendAs(t, server, k, "abc", body).StatusCode)
	waited.Wait()
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, body, got)
	require.Less(t, time.Since(started), 5*time.Second)

	// hold=1: not there at once, there a second later, to a poll already
	// waiting.
	pushCall(t, http.MethodPost, server.URL+"/fake/push?id=abc&hold=1")
	body = pushBody(t, nil)
	sent := time.Now()
	require.Equal(t, http.StatusCreated, pushSendAs(t, server, k, "abc", body).StatusCode)
	status, _ = pushNext(t, server, "abc", 0)
	require.Equal(t, http.StatusNoContent, status)
	status, got = pushNext(t, server, "abc", 10)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, body, got)
	require.GreaterOrEqual(t, time.Since(sent), time.Second)
}

// A message is kept for its TTL, and one with a TTL of 0 only for a poll
// already waiting.
func TestPushKeepsAMessageForItsTTL(t *testing.T) {
	push, server := pushServer(t)
	k := newVAPIDKey(t)
	now := time.Now()
	push.mu.Lock()
	push.Now = func() time.Time { return now }
	push.mu.Unlock()
	headers := func(ttl string) map[string]string {
		return map[string]string{"Content-Encoding": "aes128gcm", "TTL": ttl, "Authorization": k.authorization(t, server.URL, time.Now())}
	}

	response, _ := pushSend(t, server, "abc", pushBody(t, nil), headers("0"))
	require.Equal(t, http.StatusCreated, response.StatusCode)
	status, _ := pushNext(t, server, "abc", 0)
	require.Equal(t, http.StatusNoContent, status, "a TTL of 0 with nobody waiting is not kept")

	response, _ = pushSend(t, server, "abc", pushBody(t, nil), headers("60"))
	require.Equal(t, http.StatusCreated, response.StatusCode)
	push.mu.Lock()
	now = now.Add(61 * time.Second)
	push.mu.Unlock()
	status, _ = pushNext(t, server, "abc", 0)
	require.Equal(t, http.StatusNoContent, status, "a message past its TTL is gone")
}

// ntfy mode answers as ntfy does: 200, no TTL needed, nothing kept before
// the first long poll, kept 12 hours, cut to 4096 bytes, Authorization read
// for the log only.
func TestPushInNtfyMode(t *testing.T) {
	push, server := pushServer(t)
	k := newVAPIDKey(t)
	pushRegister(t, server, "up1", k.public)
	pushCall(t, http.MethodPost, server.URL+"/fake/push?mode=ntfy")
	now := time.Now()
	push.mu.Lock()
	push.Now = func() time.Time { return now }
	push.mu.Unlock()

	plain := map[string]string{"Content-Encoding": "aes128gcm"}
	response, text := pushSend(t, server, "up1", pushBody(t, nil), plain)
	require.Equal(t, http.StatusOK, response.StatusCode, text)
	require.Contains(t, text, `"event":"message"`)
	status, _ := pushNext(t, server, "up1", 0)
	require.Equal(t, http.StatusNoContent, status, "what came before the first long poll is dropped")

	body := pushBody(t, nil)
	response, _ = pushSend(t, server, "up1", body, plain)
	require.Equal(t, http.StatusOK, response.StatusCode, "no Authorization, though a key was registered")
	status, got := pushNext(t, server, "up1", 0)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, body, got)

	big := append(pushBody(t, nil), make([]byte, 5000)...)
	response, _ = pushSend(t, server, "up1", big, map[string]string{"Content-Encoding": "aes128gcm", "Authorization": "vapid t=a.b.c, k=" + k.public})
	require.Equal(t, http.StatusOK, response.StatusCode)
	status, got = pushNext(t, server, "up1", 0)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, big[:4096], got)
	entries := push.Log()
	require.Equal(t, "malformed", entries[len(entries)-1].Authorization.Signature)

	require.Equal(t, http.StatusOK, pushSendAs(t, server, k, "up1", pushBody(t, nil)).StatusCode)
	push.mu.Lock()
	now = now.Add(11 * time.Hour)
	push.mu.Unlock()
	require.Equal(t, http.StatusOK, pushSendAs(t, server, k, "up1", pushBody(t, nil)).StatusCode)
	push.mu.Lock()
	now = now.Add(2 * time.Hour)
	push.mu.Unlock()
	status, _ = pushNext(t, server, "up1", 0)
	require.Equal(t, http.StatusOK, status, "the newer message is still kept")
	status, _ = pushNext(t, server, "up1", 0)
	require.Equal(t, http.StatusNoContent, status, "the one past 12 hours is gone")
}

// An endpoint its distributor dropped answers 404, to the sender and to the
// long poll, until it is registered again.
func TestPushDroppedEndpointIsNotFound(t *testing.T) {
	_, server := pushServer(t)
	k := newVAPIDKey(t)
	pushRegister(t, server, "abc", k.public)
	require.Equal(t, http.StatusCreated, pushSendAs(t, server, k, "abc", pushBody(t, nil)).StatusCode)

	status, _ := pushCall(t, http.MethodDelete, server.URL+"/fake/push/abc")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, http.StatusNotFound, pushSendAs(t, server, k, "abc", pushBody(t, nil)).StatusCode)
	status, _ = pushNext(t, server, "abc", 0)
	require.Equal(t, http.StatusNotFound, status)

	pushRegister(t, server, "abc", k.public)
	status, _ = pushNext(t, server, "abc", 0)
	require.Equal(t, http.StatusNoContent, status, "what it held went with it")
	require.Equal(t, http.StatusCreated, pushSendAs(t, server, k, "abc", pushBody(t, nil)).StatusCode)
}

// The log says what came and how it was answered, and of Authorization only
// the summary: never a token, a key or what a message says.
func TestPushLogNeverShowsATokenAKeyOrABody(t *testing.T) {
	_, server := pushServer(t)
	k, other := newVAPIDKey(t), newVAPIDKey(t)
	pushRegister(t, server, "abc", k.public)

	body := pushBody(t, nil)
	good := k.authorization(t, server.URL, time.Now())
	require.Equal(t, http.StatusCreated, pushSendAs(t, server, k, "abc", body).StatusCode)
	wrong := other.authorization(t, server.URL, time.Now())
	response, _ := pushSend(t, server, "abc", body, map[string]string{"Content-Encoding": "aes128gcm", "TTL": "60", "Authorization": wrong})
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)

	response, err := http.Get(server.URL + "/fake/push/log?id=abc")
	require.NoError(t, err)
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	text := string(raw)
	for _, secret := range []string{
		k.public, other.public,
		strings.TrimPrefix(strings.Split(good, ",")[0], "vapid t="),
		strings.TrimPrefix(strings.Split(wrong, ",")[0], "vapid t="),
		strings.Split(strings.TrimPrefix(good, "vapid t="), ".")[2],
		base64.StdEncoding.EncodeToString(body), base64.RawURLEncoding.EncodeToString(body),
	} {
		require.NotContains(t, text, secret)
	}

	var log struct{ Entries []PushLogEntry }
	require.NoError(t, json.Unmarshal(raw, &log))
	require.Len(t, log.Entries, 2)

	first := log.Entries[0]
	require.Equal(t, http.StatusCreated, first.Status)
	require.True(t, first.Kept)
	require.Equal(t, len(body), first.Size)
	require.Equal(t, "aes128gcm", first.Headers["Content-Encoding"])
	require.Equal(t, "86400", first.Headers["Ttl"])
	require.Equal(t, "high", first.Headers["Urgency"])
	require.NotContains(t, first.Headers, "Authorization")
	require.Equal(t, "vapid", first.Authorization.Scheme)
	require.True(t, *first.Authorization.KMatches)
	require.Equal(t, "ok", first.Authorization.Signature)
	require.Equal(t, server.URL, first.Authorization.Aud)
	require.InDelta(t, 12*3600, *first.Authorization.ExpInS, 5)

	second := log.Entries[1]
	require.Equal(t, http.StatusUnauthorized, second.Status)
	require.False(t, second.Kept)
	require.False(t, *second.Authorization.KMatches)
	require.Equal(t, "ok", second.Authorization.Signature)

	// The state shows that a key is registered, never the key.
	status, state := pushCall(t, http.MethodGet, server.URL+"/fake/push/abc")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, true, state["vapid"])
	everything, err := http.Get(server.URL + "/fake/push")
	require.NoError(t, err)
	defer everything.Body.Close()
	all, err := io.ReadAll(everything.Body)
	require.NoError(t, err)
	require.NotContains(t, string(all), k.public)
}

// What it keeps is bounded: an endpoint's oldest messages, and the log's
// oldest entries, give way to new ones.
func TestPushKeepsWhatItHoldsBounded(t *testing.T) {
	push, server := pushServer(t)
	k := newVAPIDKey(t)

	var bodies [][]byte
	for range maxPushLog + 1 {
		body := pushBody(t, nil)
		bodies = append(bodies, body)
		require.Equal(t, http.StatusCreated, pushSendAs(t, server, k, "abc", body).StatusCode)
	}
	require.Len(t, push.Log(), maxPushLog)

	status, got := pushNext(t, server, "abc", 0)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, bodies[len(bodies)-maxPushQueued], got, "the oldest messages gave way")
}
