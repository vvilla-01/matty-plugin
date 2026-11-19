package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/mattermost/mattermost/server/public/plugin"

	"github.com/vvilla-01/matty-plugin/server/pushsend"
	"github.com/vvilla-01/matty-plugin/server/webpush"
)

// The push routes (contract/README.md, "Push notifications"): a Matty
// registers its push address for its own session, confirms the hello the
// server sends it there, asks how it stands, takes it back and asks for a
// test; and the System Console's card. Every refusal is a sentence that
// quotes nothing the request carried.

const (
	mePushPath        = "/api/v1/me/push"
	mePushConfirmPath = "/api/v1/me/push/confirm"
	mePushTestPath    = "/api/v1/me/push/test"
	adminPushPath     = "/api/v1/admin/push"

	maxPushBody = 4 << 10

	// A PUT of what the session's pending side holds, less than this after
	// that side was made, is answered as it stands: no new hello, and not
	// counted against the limit. One later makes a new hello.
	pushSamePutWithin = 10 * time.Second

	// codePushKey is a registration made with a push key that is not this
	// server's (any more): ask /access for the current one and register
	// again with it.
	codePushKey = "push_key"
)

const (
	pushNoSessionText   = "This request carries no Mattermost session, and push notifications are registered for one."
	pushBodyText        = "The push registration could not be read."
	pushBodyTooLarge    = "The push registration is too large."
	pushKeyChangedText  = "This phone registered with a push key that is not this server's. Ask again for the current key and register with it."
	pushStoreText       = "This server could not read or write its push records. Try again in a moment."
	pushConflictText    = "This check message is not the latest one sent to this phone, or it has expired."
	pushNoDeviceText    = "This phone has no working push registration on this server. Set up push notifications again."
	pushTooManyText     = "Too many push registrations at once. Wait a few minutes and try again."
	pushTestTooManyText = "Too many test pushes. Wait a moment and try again."
	pushAdminOnlyText   = "Only a system admin can see push notifications' state."
)

// pushRegistration is PUT /me/push's body.
type pushRegistration struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
	Key      string `json:"key"`
}

// pushEndpointText is what is wrong with a push address, in a sentence that
// quotes none of it.
func pushEndpointText(err error) string {
	switch {
	case errors.Is(err, pushsend.ErrEndpointTooLong):
		return "The push address is longer than 1000 bytes."
	case errors.Is(err, pushsend.ErrEndpointCharacters):
		return "The push address has a character that is not printable ASCII."
	case errors.Is(err, pushsend.ErrEndpointScheme):
		return "The push address does not start with https://."
	case errors.Is(err, pushsend.ErrEndpointPlainHTTP):
		return "The push address uses http://, which only a push server on this server's list may."
	case errors.Is(err, pushsend.ErrEndpointUserinfo):
		return "The push address carries a user name."
	case errors.Is(err, pushsend.ErrEndpointFragment):
		return "The push address carries a fragment."
	case errors.Is(err, pushsend.ErrEndpointNotListed):
		return "This server sends pushes only to the push servers its admin listed, and this push address is at another."
	case errors.Is(err, pushsend.ErrEndpointAddress):
		return "The push address is an internet address this server does not send to."
	default:
		return "The push address is not an absolute web address with a host."
	}
}

// pushOn is whether the push routes that need it may be used, and if not,
// it has answered 503 with /access's sentence: coded `store` while push is
// switched on and the push key is not loaded yet (worth sending again), and
// `unavailable` while push is off (switched off, or Mattermost pushing to
// nobody).
func (p *Plugin) pushOn(w http.ResponseWriter, cfg *configuration) bool {
	word := p.pushWordNow(cfg)
	if word.State == "on" {
		return true
	}
	code := codeUnavailable
	if word.Reason == pushNotReadyText {
		code = accessCodeStore
	}
	writeCodedError(w, http.StatusServiceUnavailable, word.Reason, code)

	return false
}

// writePushStoreError answers a push record that could not be read or
// written: 503 with the contract's code for the store, which a client takes
// as worth trying again, never as push being off.
func writePushStoreError(w http.ResponseWriter) {
	writeCodedError(w, http.StatusServiceUnavailable, pushStoreText, accessCodeStore)
}

// readPushBody decodes a request body of at most maxPushBody, or answers.
func readPushBody(w http.ResponseWriter, r *http.Request, into any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPushBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, pushBodyTooLarge)
			return false
		}
		writeError(w, http.StatusBadRequest, pushBodyText)
		return false
	}
	if json.Unmarshal(raw, into) != nil {
		writeError(w, http.StatusBadRequest, pushBodyText)
		return false
	}

	return true
}

// handleMePush serves /me/push.
func (p *Plugin) handleMePush(w http.ResponseWriter, r *http.Request, c *plugin.Context, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	session := sessionOf(c)
	switch r.Method {
	case http.MethodPut:
		if !p.pushOn(w, p.getConfiguration()) {
			return
		}
		if session == "" {
			writeError(w, http.StatusBadRequest, pushNoSessionText)
			return
		}
		p.handlePushRegister(w, r, userID, session)
	case http.MethodGet:
		if session == "" {
			writeError(w, http.StatusBadRequest, pushNoSessionText)
			return
		}
		p.handlePushState(w, userID, session)
	case http.MethodDelete:
		// Never metered, and open while push is off: taking your own
		// registration back always works.
		if session == "" {
			writeError(w, http.StatusBadRequest, pushNoSessionText)
			return
		}
		p.handlePushRemove(w, userID, session)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
	}
}

// handlePushRegister is PUT /me/push: the same address, keys and push key
// as the confirmed device moves seen and nothing else; those of the pending
// side, made under pushSamePutWithin ago, answer as it stands; anything else
// becomes the device's pending side, and a hello goes to it.
func (p *Plugin) handlePushRegister(w http.ResponseWriter, r *http.Request, userID, session string) {
	var request pushRegistration
	if !readPushBody(w, r, &request) {
		return
	}

	pushes := p.pushes()
	rules := pushes.sendClient().Rules()
	endpoint, err := pushsend.ValidateEndpoint(request.Endpoint, rules)
	if err == nil && pushsend.LoopbackName(request.Endpoint, rules) {
		err = pushsend.ErrEndpointAddress
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, pushEndpointText(err))
		return
	}
	if _, err := webpush.DecodeSubscriberKey(request.P256dh); err != nil {
		writeError(w, http.StatusBadRequest, "The phone's push key is not a P-256 public key in base64url without padding.")
		return
	}
	if _, err := webpush.DecodeAuthSecret(request.Auth); err != nil {
		writeError(w, http.StatusBadRequest, "The phone's push secret is not 16 bytes in base64url without padding.")
		return
	}
	current := pushes.publicKey()
	if subtle.ConstantTimeCompare([]byte(request.Key), []byte(current)) != 1 {
		writeCodedError(w, http.StatusBadRequest, pushKeyChangedText, codePushKey)
		return
	}

	doc, err := p.readPushDoc(userID)
	if err != nil {
		writePushStoreError(w)
		return
	}
	if i := doc.find(session); i >= 0 && doc.Devices[i].State == pushStateConfirmed && sameRegistration(doc.Devices[i], request) {
		view := p.pushSeen(userID, session, true)
		if view.State == "" {
			writePushStoreError(w)
			return
		}
		writeJSON(w, http.StatusOK, view)
		return
	}
	if i := doc.find(session); i >= 0 && samePendingJustNow(doc.Devices[i], request, pushes.now()) {
		// The same registration again at once (a phone that registered
		// twice): answered as it stands, with no new hello.
		writeJSON(w, http.StatusOK, pushViewOf(doc, session, current, pushes.now()))
		return
	}

	if !pushes.registerLimiter.allowAt(userID, pushes.now()) {
		writeTooMany(w, pushTooManyText, pushes.registerLimiter, userID, pushes.now())
		return
	}

	nonce, err := newPushNonce()
	if err != nil {
		writeError(w, http.StatusInternalServerError, pushStoreText)
		return
	}

	var pending pushPending
	written, err := p.updatePushDoc(userID, func(doc *pushDoc, now time.Time) bool {
		pending = pushPending{
			Endpoint: request.Endpoint, P256dh: request.P256dh, Auth: request.Auth, Key: request.Key,
			Host: endpoint.Host, Nonce: nonce, At: now.UnixMilli(),
		}

		i := doc.find(session)
		if i < 0 {
			if len(doc.Devices) >= pushDevicesPerPerson {
				oldest := 0
				for j := range doc.Devices {
					if doc.Devices[j].Seen < doc.Devices[oldest].Seen {
						oldest = j
					}
				}
				doc.remove(oldest)
			}
			doc.Devices = append(doc.Devices, pushDevice{
				Session: session, Created: now.UnixMilli(), Seen: now.UnixMilli(),
				State: pushStatePending, Since: now.UnixMilli(),
			})
			i = len(doc.Devices) - 1
		}

		device := &doc.Devices[i]
		device.Pending = &pending
		if now.Sub(time.UnixMilli(device.Seen)) >= pushSeenEvery {
			device.Seen = now.UnixMilli()
		}

		return true
	})
	if err != nil {
		writePushStoreError(w)
		return
	}

	// In the index from the first registration, so that the sweep clears
	// one never confirmed and the card counts it as waiting.
	p.joinPushPeople(userID)
	p.queueHello(userID, session, pending)
	writeJSON(w, http.StatusOK, pushViewOf(written, session, current, pushes.now()))
}

// sameRegistration is whether a registration is what a device's main side
// already holds.
func sameRegistration(device pushDevice, request pushRegistration) bool {
	return device.Endpoint == request.Endpoint && device.P256dh == request.P256dh &&
		device.Auth == request.Auth && device.Key == request.Key
}

// samePendingJustNow is whether a registration is what a device's pending
// side holds, that side made less than pushSamePutWithin ago.
func samePendingJustNow(device pushDevice, request pushRegistration, now time.Time) bool {
	pending := device.Pending
	if pending == nil || !device.pendingLive(now) {
		return false
	}
	made := time.UnixMilli(pending.At)

	return pending.Endpoint == request.Endpoint && pending.P256dh == request.P256dh &&
		pending.Auth == request.Auth && pending.Key == request.Key &&
		!now.Before(made) && now.Sub(made) < pushSamePutWithin
}

// pushSeen answers for a session's device, moving its seen at most once a
// day; dropPending also lets go of a registration the app no longer makes. A
// person with a device who is missing from push_people is put back. A view
// with no state is a store that could not be read.
func (p *Plugin) pushSeen(userID, session string, dropPending bool) pushStateView {
	pushes := p.pushes()
	doc, err := p.updatePushDoc(userID, func(doc *pushDoc, now time.Time) bool {
		i := doc.find(session)
		if i < 0 {
			return false
		}
		device := &doc.Devices[i]
		changed := false
		if now.Sub(time.UnixMilli(device.Seen)) >= pushSeenEvery {
			device.Seen = now.UnixMilli()
			changed = true
		}
		if dropPending && device.Pending != nil {
			device.Pending = nil
			changed = true
		}
		return changed
	})
	if err != nil {
		// Seen is a convenience: say how things stand from a plain read.
		doc, err = p.readPushDoc(userID)
		if err != nil {
			return pushStateView{}
		}
	}
	p.keepInPushPeople(userID, doc)

	return pushViewOf(doc, session, pushes.publicKey(), pushes.now())
}

// handlePushState is GET /me/push.
func (p *Plugin) handlePushState(w http.ResponseWriter, userID, session string) {
	view := p.pushSeen(userID, session, false)
	if view.State == "" {
		writePushStoreError(w)
		return
	}

	writeJSON(w, http.StatusOK, view)
}

// handlePushRemove is DELETE /me/push.
func (p *Plugin) handlePushRemove(w http.ResponseWriter, userID, session string) {
	_, err := p.updatePushDoc(userID, func(doc *pushDoc, _ time.Time) bool {
		i := doc.find(session)
		if i < 0 {
			return false
		}
		doc.remove(i)
		return true
	})
	if err != nil {
		writePushStoreError(w)
		return
	}
	p.pushes().forget(session)

	writeJSON(w, http.StatusOK, pushStateView{State: pushStateNone})
}

// handlePushConfirm is POST /me/push/confirm: the hello's nonce makes the
// pending side the device.
func (p *Plugin) handlePushConfirm(w http.ResponseWriter, r *http.Request, c *plugin.Context, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.pushOn(w, p.getConfiguration()) {
		return
	}
	session := sessionOf(c)
	if session == "" {
		writeError(w, http.StatusBadRequest, pushNoSessionText)
		return
	}

	var request struct {
		Nonce string `json:"nonce"`
	}
	if !readPushBody(w, r, &request) {
		return
	}

	matched, confirmed := false, ""
	doc, err := p.updatePushDoc(userID, func(doc *pushDoc, now time.Time) bool {
		matched, confirmed = false, ""
		i := doc.find(session)
		if i < 0 {
			return false
		}
		device := &doc.Devices[i]
		if !device.pendingLive(now) || request.Nonce == "" ||
			subtle.ConstantTimeCompare([]byte(device.Pending.Nonce), []byte(request.Nonce)) != 1 {
			return false
		}

		pending := device.Pending
		device.Endpoint, device.P256dh, device.Auth, device.Key, device.Host = pending.Endpoint, pending.P256dh, pending.Auth, pending.Key, pending.Host
		device.State, device.Since = pushStateConfirmed, now.UnixMilli()
		device.Status, device.Kind, device.Trouble, device.Suspect = 0, "", 0, 0
		device.Pending = nil
		matched, confirmed = true, device.Endpoint

		return true
	})
	if err != nil {
		writePushStoreError(w)
		return
	}
	if !matched {
		writeError(w, http.StatusConflict, pushConflictText)
		return
	}

	pushes := p.pushes()
	pushes.forget(session)
	pushes.confirmedAs(session, confirmed)
	p.joinPushPeople(userID)

	writeJSON(w, http.StatusOK, pushViewOf(doc, session, pushes.publicKey(), pushes.now()))
}

// handlePushTest is POST /me/push/test: a test push to this session's
// confirmed device.
func (p *Plugin) handlePushTest(w http.ResponseWriter, c *plugin.Context, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.pushOn(w, p.getConfiguration()) {
		return
	}
	session := sessionOf(c)
	if session == "" {
		writeError(w, http.StatusBadRequest, pushNoSessionText)
		return
	}

	pushes := p.pushes()
	doc, err := p.readPushDoc(userID)
	if err != nil {
		writePushStoreError(w)
		return
	}
	i := doc.find(session)
	if i < 0 || doc.Devices[i].Endpoint == "" || doc.Devices[i].State != pushStateConfirmed || doc.Devices[i].Key != pushes.publicKey() {
		writeError(w, http.StatusConflict, pushNoDeviceText)
		return
	}

	if !pushes.testLimiter.allowAt(userID, pushes.now()) {
		writeTooMany(w, pushTestTooManyText, pushes.testLimiter, userID, pushes.now())
		return
	}

	p.queueTest(userID, doc.Devices[i])
	writeJSON(w, http.StatusAccepted, pushStateView{State: pushStateConfirmed, Host: doc.Devices[i].Host, Since: doc.Devices[i].Since})
}

// pushAdminView is GET /api/v1/admin/push: whether pushes can go out, and
// how many people and devices there are, by push server. Never anything per
// person: which push server somebody uses is theirs.
type pushAdminView struct {
	// On is the plugin's switch; Ready, whether the push key is loaded.
	On    bool `json:"on"`
	Ready bool `json:"ready"`

	// Mattermost is whether Mattermost decides to push at all, and
	// MattermostProblem why not, with the console path.
	Mattermost        bool   `json:"mattermost"`
	MattermostProblem string `json:"mattermostProblem,omitempty"`

	People  int           `json:"people"`
	Devices int           `json:"devices"`
	Hosts   []pushHostRow `json:"hosts"`

	// Full: more than the index keeps, so the counts are of the most
	// recent pushPeopleKept people.
	Full bool `json:"full,omitempty"`

	// Problem: lines of the list of push servers left out.
	Problem string `json:"problem,omitempty"`

	// Dropped is this node's count of pushes not sent since it started,
	// by why.
	Dropped map[string]int64 `json:"dropped,omitempty"`
}

// handleAdminPush answers GET /api/v1/admin/push, for system admins.
func (p *Plugin) handleAdminPush(w http.ResponseWriter, r *http.Request, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, pushAdminOnlyText)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	cfg := p.getConfiguration()
	pushes := p.pushes()
	view := pushAdminView{On: cfg.EnablePush, Ready: pushes.publicKey() != "", Hosts: []pushHostRow{}}
	view.Mattermost, view.MattermostProblem = p.mattermostCanPush()
	view.Problem = pushes.hostsProblemNow()
	if dropped := pushes.droppedNow(); len(dropped) > 0 {
		view.Dropped = dropped
	}

	people, _, err := p.readPushPeople()
	if err != nil {
		writePushStoreError(w)
		return
	}
	view.Full = len(people.People) >= pushPeopleKept

	now := pushes.now()
	rows := map[string]*pushHostRow{}
	for person := range people.People {
		doc, err := p.readPushDoc(person)
		if err != nil {
			writePushStoreError(w)
			return
		}
		if devices := countPushHosts(rows, doc, now); devices > 0 {
			view.People++
			view.Devices += devices
		}
	}
	view.Hosts = sortedPushHosts(rows)

	writeJSON(w, http.StatusOK, view)
}
