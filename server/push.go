package main

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/vvilla-01/matty-plugin/server/pushsend"
	"github.com/vvilla-01/matty-plugin/server/webpush"
)

// Push notifications through UnifiedPush (docs/push.md, contract/README.md
// "Push notifications"): when Mattermost decides to push to somebody, the
// plugin sends an end-to-end encrypted Web Push (RFC 8030, RFC 8291, VAPID
// RFC 8292) to the address each of that person's Matty sessions registered,
// and Matty shows the notification without connecting to this server.
//
// What is kept, all in the plugin's store and never in a settings backup:
//
//   - push_vapid: the server's push key, a P-256 private key, made on the
//     first start and never shown, logged or sent;
//   - push_user_<user id>: one document per person, their devices, one per
//     Mattermost session and at most pushDevicesPerPerson, written only by
//     compare-and-set, saying when it was last written, and expiring
//     pushRowDays after that unless written again: the daily sweep writes
//     again a row whose session stands (pushRowRenewAfter), so that it lives
//     as long as its session;
//   - push_people: who has a device, at most pushPeopleKept, which the
//     daily sweep walks and every node reads to know whom to offer pushes
//     for. It never expires: the joins, the leaves and the sweep keep it,
//     and a person missing from it is put back by their next GET /me/push
//     or by the sweep, which also lists the push_user_ keys.
//
// No key carries a push address or a session id, and no log line, refusal or
// error carries a push address, a key, a message or a nonce.

const (
	kvPushKey        = "push_vapid"
	kvPushUserPrefix = "push_user_"
	kvPushPeople     = "push_people"

	pushDocVersion = 1

	// pushRowDays is how long a person's devices are kept after their last
	// write; the sweep writes a row whose session stands well before that.
	pushRowDays    = 90
	pushRowSeconds = int64(pushRowDays * 24 * 60 * 60)

	pushDevicesPerPerson = 8
	pushPeopleKept       = 5000

	// A device's pending side not confirmed within this is dropped.
	pushPendingLife = 10 * time.Minute

	// seen is written at most this often.
	pushSeenEvery = 24 * time.Hour

	// A session that could not be read is dropped only when a later check,
	// at least this long after the first, cannot read it either.
	pushSuspectGrace = 60 * time.Second

	pushStatePending   = "pending"
	pushStateConfirmed = "confirmed"
	pushStateRefused   = "refused"
	pushStateFailing   = "failing"

	// What GET /me/push may say besides those.
	pushStateNone    = "none"
	pushStateRefresh = "refresh"

	// How often the answer to "can Mattermost push?" is worked out again.
	pushCanPushEvery = 60 * time.Second
)

// The sentences /access gives with "off", and the refusals of the routes
// that need push to be on.
const (
	pushOffText      = "Push notifications are switched off on this server."
	pushNotReadyText = "Push notifications are not ready on this server yet. Try again in a moment."

	// Mattermost's own words for where its push notifications are switched
	// on (System Console > Environment > Push Notification Server, "Enable
	// Push Notifications").
	pushMattermostOffText = "Mattermost's own push notifications are switched off on this server, and Matty's go out only when Mattermost decides to push. " +
		"An admin switches them on in System Console > Environment > Push Notification Server, where Enable Push Notifications must not be \"Do not send push notifications\"."
	pushMattermostLicenceText = "Mattermost's push notification server here needs a licence this server does not have, so Mattermost decides to push to nobody. " +
		"An admin chooses another in System Console > Environment > Push Notification Server."
	pushMattermostUnknownText = "This server could not read Mattermost's own push notification settings just now."
)

// errPushStore is a push document that could not be read or written.
var errPushStore = errors.New("the push records could not be read or written")

// pushDoc is push_user_<user id>.
type pushDoc struct {
	V       int          `json:"v"`
	Devices []pushDevice `json:"devices"`

	// Written is when the document was last written, Unix ms.
	Written int64 `json:"written,omitempty"`
}

// pushDevice is one Matty session's registration. The main side (Endpoint
// and the keys) is what pushes go to once confirmed; Pending is a
// registration the server has sent a hello to and waits to hear back about,
// while the main side, if confirmed, stays in use.
type pushDevice struct {
	Session string `json:"session"`

	Endpoint string `json:"endpoint,omitempty"`
	P256dh   string `json:"p256dh,omitempty"`
	Auth     string `json:"auth,omitempty"`
	Key      string `json:"key,omitempty"`
	Host     string `json:"host,omitempty"`

	Created int64 `json:"created"`
	Seen    int64 `json:"seen"`

	// State is pending (no main side yet), confirmed, refused (the push
	// server answered 401 or 403: Status says which) or failing (suspended
	// after sends that kept failing: Kind says how). Since is when it
	// became so; for failing, when the failures began.
	State  string `json:"state"`
	Since  int64  `json:"since"`
	Status int    `json:"status,omitempty"`
	Kind   string `json:"kind,omitempty"`

	// Trouble is when a run of failed sends began, while it lasts and the
	// device is not yet suspended; Suspect, when its session could first
	// not be read.
	Trouble int64 `json:"trouble,omitempty"`
	Suspect int64 `json:"suspect,omitempty"`

	Pending *pushPending `json:"pending,omitempty"`
}

// pushPending is a registration waiting for its hello to be confirmed.
type pushPending struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
	Key      string `json:"key"`
	Host     string `json:"host,omitempty"`
	Nonce    string `json:"nonce"`
	At       int64  `json:"at"`

	// What became of its hello, when it did not go: refused or failing.
	State  string `json:"state,omitempty"`
	Status int    `json:"status,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Since  int64  `json:"since,omitempty"`
}

// pushPeopleDoc is push_people: who has a device, with when they last got
// one, so that the oldest go first when it is full.
type pushPeopleDoc struct {
	V      int              `json:"v"`
	People map[string]int64 `json:"people"`
}

func pushUserKey(userID string) string {
	return kvPushUserPrefix + userID
}

// pendingLive is whether a device's pending side is still waited for.
func (d *pushDevice) pendingLive(now time.Time) bool {
	return d.Pending != nil && now.Sub(time.UnixMilli(d.Pending.At)) < pushPendingLife
}

// tidy drops what has expired from a document: pending sides past their
// life, and devices left with neither side. It says whether anything went.
func (doc *pushDoc) tidy(now time.Time) bool {
	changed := false
	kept := doc.Devices[:0]

	for _, device := range doc.Devices {
		if device.Pending != nil && !device.pendingLive(now) {
			device.Pending = nil
			changed = true
		}
		if device.Endpoint == "" && device.Pending == nil {
			changed = true
			continue
		}
		kept = append(kept, device)
	}
	doc.Devices = kept

	return changed
}

func (doc *pushDoc) find(session string) int {
	for i := range doc.Devices {
		if doc.Devices[i].Session == session {
			return i
		}
	}

	return -1
}

func (doc *pushDoc) remove(i int) {
	doc.Devices = append(doc.Devices[:i], doc.Devices[i+1:]...)
}

// decodePushDoc reads a stored document. One that does not read is taken as
// empty, so that its person can register again; it is replaced by
// compare-and-set like any other.
func decodePushDoc(raw []byte) *pushDoc {
	doc := &pushDoc{V: pushDocVersion}
	if len(raw) == 0 {
		return doc
	}

	var stored pushDoc
	if err := json.Unmarshal(raw, &stored); err != nil || stored.V != pushDocVersion {
		return doc
	}
	doc.Devices = stored.Devices
	doc.Written = stored.Written

	return doc
}

// readPushDoc is a person's devices, as stored.
func (p *Plugin) readPushDoc(userID string) (*pushDoc, error) {
	raw, appErr := p.API.KVGet(pushUserKey(userID))
	if appErr != nil {
		return nil, errPushStore
	}

	return decodePushDoc(raw), nil
}

// updatePushDoc changes a person's devices by compare-and-set. apply is
// given a fresh copy each try and says whether to write it; it may run more
// than once, so it keeps nothing outside what it is given but what it sets
// afresh each time. Every write sets Written. A document left with no device
// is deleted, and its person leaves push_people. It returns the document as
// written (or as read, when apply wrote nothing).
func (p *Plugin) updatePushDoc(userID string, apply func(doc *pushDoc, now time.Time) bool) (*pushDoc, error) {
	key := pushUserKey(userID)

	for attempt := 0; attempt < casAttempts; attempt++ {
		raw, appErr := p.API.KVGet(key)
		if appErr != nil {
			return nil, errPushStore
		}
		if len(raw) == 0 {
			raw = nil
		}

		now := p.pushes().now()
		doc := decodePushDoc(raw)
		tidied := doc.tidy(now)
		if !apply(doc, now) && !tidied {
			return doc, nil
		}
		doc.tidy(now)

		if len(doc.Devices) == 0 {
			if raw == nil {
				return doc, nil
			}
			deleted, appErr := p.API.KVCompareAndDelete(key, raw)
			if appErr != nil {
				return nil, errPushStore
			}
			if !deleted {
				continue
			}
			p.leavePushPeople(userID)

			return doc, nil
		}

		doc.Written = now.UnixMilli()
		encoded, err := json.Marshal(doc)
		if err != nil {
			return nil, err
		}
		written, appErr := p.API.KVSetWithOptions(key, encoded, model.PluginKVSetOptions{
			Atomic:          true,
			OldValue:        raw,
			ExpireInSeconds: pushRowSeconds,
		})
		if appErr != nil {
			return nil, errPushStore
		}
		if written {
			return doc, nil
		}
	}

	return nil, errTooBusy
}

// readPushPeople is push_people.
func (p *Plugin) readPushPeople() (*pushPeopleDoc, []byte, error) {
	raw, appErr := p.API.KVGet(kvPushPeople)
	if appErr != nil {
		return nil, nil, errPushStore
	}
	if len(raw) == 0 {
		return &pushPeopleDoc{V: pushDocVersion, People: map[string]int64{}}, nil, nil
	}

	var doc pushPeopleDoc
	if err := json.Unmarshal(raw, &doc); err != nil || doc.People == nil {
		return &pushPeopleDoc{V: pushDocVersion, People: map[string]int64{}}, raw, nil
	}

	return &doc, raw, nil
}

// changePushPeople changes push_people by compare-and-set. It is written with
// no expiry: an index that expired would leave every node offering pushes for
// nobody.
func (p *Plugin) changePushPeople(apply func(doc *pushPeopleDoc) bool) error {
	for attempt := 0; attempt < casAttempts; attempt++ {
		doc, raw, err := p.readPushPeople()
		if err != nil {
			return err
		}
		if !apply(doc) {
			return nil
		}

		encoded, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		written, appErr := p.API.KVSetWithOptions(kvPushPeople, encoded, model.PluginKVSetOptions{
			Atomic:   true,
			OldValue: raw,
		})
		if appErr != nil {
			return errPushStore
		}
		if written {
			return nil
		}
	}

	return errTooBusy
}

// joinPushPeople puts a person in push_people, the oldest entry making room
// when it is full, and in this node's set at once.
func (p *Plugin) joinPushPeople(userID string) {
	now := p.pushes().now().UnixMilli()
	p.pushes().notePerson(userID)

	err := p.changePushPeople(func(doc *pushPeopleDoc) bool {
		if _, in := doc.People[userID]; in {
			return false
		}
		for len(doc.People) >= pushPeopleKept {
			oldest, at := "", int64(0)
			for id, when := range doc.People {
				if oldest == "" || when < at || (when == at && id < oldest) {
					oldest, at = id, when
				}
			}
			delete(doc.People, oldest)
		}
		doc.People[userID] = now

		return true
	})
	if err != nil {
		p.API.LogWarn("Could not add somebody to the list of people with push notifications", "error", err.Error())
	}
}

// keepInPushPeople puts a person who has a device back in push_people when
// this node's set, read from it and trusted, does not have them (a join that
// failed, or the oldest let go while it was full): one look in memory, and a
// write only when they are missing.
func (p *Plugin) keepInPushPeople(userID string, doc *pushDoc) {
	if len(doc.Devices) == 0 || !p.pushes().missingPerson(userID) {
		return
	}
	p.joinPushPeople(userID)
}

// missingPerson is whether this node's set, read from push_people and not
// full, lacks a person.
func (r *pushRuntime) missingPerson(userID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.peopleKnown && !r.peopleFull && !r.people[userID]
}

// leavePushPeople takes a person out of push_people.
func (p *Plugin) leavePushPeople(userID string) {
	err := p.changePushPeople(func(doc *pushPeopleDoc) bool {
		if _, in := doc.People[userID]; !in {
			return false
		}
		delete(doc.People, userID)

		return true
	})
	if err != nil {
		p.API.LogWarn("Could not take somebody off the list of people with push notifications", "error", err.Error())
	}
}

// loadPushKey reads the server's push key, or makes it: the first node to
// write it wins, and every other adopts it, as with the handle secret. One
// that could not be loaded is tried again by retryPushKey.
func (p *Plugin) loadPushKey() {
	if problem, err := p.readOrMakePushKey(); problem != "" {
		details := []any{}
		if err != nil {
			details = append(details, "error", err.Error())
		}
		p.API.LogWarn(problem+": push notifications wait for it, and it is tried again every 30 seconds", details...)
	}
}

// retryPushKey loads the push key while there is none, quietly but for when
// it succeeds. It reads nothing once there is one.
func (p *Plugin) retryPushKey() {
	if p.pushes().publicKey() != "" {
		return
	}
	if problem, _ := p.readOrMakePushKey(); problem == "" {
		p.API.LogInfo("The push key is loaded, so push notifications can be registered for")
	}
}

// readOrMakePushKey is loadPushKey's work. It says what went wrong when it
// left the server with no key, and the error if there is one, or "".
func (p *Plugin) readOrMakePushKey() (string, error) {
	raw, appErr := p.API.KVGet(kvPushKey)
	if appErr != nil {
		return "Could not read the push key", appErr
	}
	if len(raw) > 0 {
		if key, err := webpush.ParseKey(raw); err == nil {
			p.pushes().setKey(key)
			return "", nil
		}
		p.API.LogError("The stored push key could not be read, so a new one is made and every phone registers again")
	} else {
		raw = nil
	}

	key, err := webpush.GenerateKey()
	if err != nil {
		return "Could not make a push key", err
	}

	written, appErr := p.API.KVSetWithOptions(kvPushKey, key.Bytes(), model.PluginKVSetOptions{Atomic: true, OldValue: raw})
	if appErr != nil {
		return "Could not keep the push key", appErr
	}
	if !written {
		// Another node won: its key is everybody's.
		stored, appErr := p.API.KVGet(kvPushKey)
		if appErr != nil || len(stored) == 0 {
			return "Could not read the push key another node made", nil
		}
		adopted, err := webpush.ParseKey(stored)
		if err != nil {
			return "The push key another node made could not be read", err
		}
		key = adopted
	}

	p.pushes().setKey(key)

	return "", nil
}

// pushRules are the address rules the settings give.
func (c *configuration) pushRules() pushsend.Rules {
	list, _ := pushsend.ParseHostList(c.PushAllowedHosts)

	return pushsend.Rules{Listed: list, OnlyListed: c.PushOnlyListed}
}

// normalizePushSettings is normalize's part for push: the list of push
// servers in its canonical form, a line that is not one left out (the
// settings load logs which, pushHostsProblem), and a contact that may not go
// in a token blanked.
func (c *configuration) normalizePushSettings() {
	list, _ := pushsend.ParseHostList(c.PushAllowedHosts)
	c.PushAllowedHosts = list.String()

	c.PushContact = strings.TrimSpace(c.PushContact)
	if c.PushContact != "" && !webpush.ContactOK(c.PushContact) {
		c.PushContact = ""
	}
}

// pushHostsProblem is the lines of the list of push servers that are not a
// host or host:port, and so are left out, as a sentence, or "".
func pushHostsProblem(setting string) string {
	_, bad := pushsend.ParseHostList(setting)
	if len(bad) == 0 {
		return ""
	}

	lines := make([]string, len(bad))
	for i, n := range bad {
		lines[i] = strconv.Itoa(n)
	}
	word := "Line "
	if len(bad) > 1 {
		word = "Lines "
	}

	return word + strings.Join(lines, ", ") + " of Push servers on your own network " + map[bool]string{true: "are", false: "is"}[len(bad) > 1] +
		" not a host or host:port, or past the first " + strconv.Itoa(pushsend.MaxListedHosts) + " entries, and left out."
}

// pushConfigured follows a settings load: the client sends under the rules
// as they are now, and whether Mattermost can push is worked out afresh.
func (p *Plugin) pushConfigured(cfg *configuration) {
	r := p.pushes()
	r.sendClient().SetRules(cfg.pushRules())
	r.forgetCanPush()
}

// mattermostCanPush is whether Mattermost decides to push at all (its own
// canSendPushNotifications: its switch, and a licence for a hosted push
// server), and why not, worked out at most once a minute and afresh after a
// settings load.
func (p *Plugin) mattermostCanPush() (bool, string) {
	r := p.pushes()
	if ok, reason, known := r.canPushNow(); known {
		return ok, reason
	}

	ok, reason := true, ""
	config := p.API.GetConfig()
	switch {
	case config == nil:
		ok, reason = false, pushMattermostUnknownText
	case config.EmailSettings.SendPushNotifications == nil || !*config.EmailSettings.SendPushNotifications:
		ok, reason = false, pushMattermostOffText
	case config.EmailSettings.PushNotificationServer != nil && model.IsMHPNSEndpoint(*config.EmailSettings.PushNotificationServer) && !p.API.GetLicense().HasMHPNS():
		ok, reason = false, pushMattermostLicenceText
	}

	r.keepCanPush(ok, reason)

	return ok, reason
}

// pushWord is what /access says about push.
type pushWord struct {
	// State is "on" (a Matty may register) or "off".
	State string `json:"state"`

	// Key is the server's push key, the VAPID public key a Matty registers
	// with, while on.
	Key string `json:"key,omitempty"`

	// Reason is why not, while off.
	Reason string `json:"reason,omitempty"`
}

// pushWordNow is /access's push word. It reads nothing from the store.
func (p *Plugin) pushWordNow(cfg *configuration) pushWord {
	if !cfg.EnablePush {
		return pushWord{State: "off", Reason: pushOffText}
	}

	key := p.pushes().publicKey()
	if key == "" {
		return pushWord{State: "off", Reason: pushNotReadyText}
	}

	if ok, reason := p.mattermostCanPush(); !ok {
		return pushWord{State: "off", Reason: reason}
	}

	return pushWord{State: "on", Key: key}
}

// pushAlertFor is /me's pushAlert for system admins: while push is on here,
// why Mattermost pushes to nobody, or "".
func (p *Plugin) pushAlertFor(cfg *configuration) string {
	if !cfg.EnablePush {
		return ""
	}
	if ok, reason := p.mattermostCanPush(); !ok {
		return reason
	}

	return ""
}

// pushViewOf is what GET /me/push says of a session's device. The side it
// speaks of is the pending one while that is waited for, else the main one;
// it says refresh when that side was registered with another key than the
// server's, and never while the server has no key loaded.
func pushViewOf(doc *pushDoc, session, currentKey string, now time.Time) pushStateView {
	i := doc.find(session)
	if i < 0 {
		return pushStateView{State: pushStateNone}
	}
	device := doc.Devices[i]

	key := device.Key
	if device.Pending != nil && (device.pendingLive(now) || device.Endpoint == "") {
		key = device.Pending.Key
	}
	if currentKey != "" && key != currentKey {
		return pushStateView{State: pushStateRefresh}
	}

	if device.pendingLive(now) {
		pending := device.Pending
		switch pending.State {
		case pushStateRefused:
			return pushStateView{State: pushStateRefused, Host: pending.Host, Status: pending.Status, Since: pending.Since}
		case pushStateFailing:
			return pushStateView{State: pushStateFailing, Host: pending.Host, Kind: pending.Kind, Since: pending.Since}
		}

		return pushStateView{State: pushStatePending, Host: pending.Host, Since: pending.At}
	}

	switch device.State {
	case pushStateRefused:
		return pushStateView{State: pushStateRefused, Host: device.Host, Status: device.Status, Since: device.Since}
	case pushStateFailing:
		return pushStateView{State: pushStateFailing, Host: device.Host, Kind: device.Kind, Since: device.Since}
	case pushStateConfirmed:
		return pushStateView{State: pushStateConfirmed, Host: device.Host, Since: device.Since}
	}

	return pushStateView{State: pushStateNone}
}

// pushStateView is the answer of the /me/push routes.
type pushStateView struct {
	State  string `json:"state"`
	Host   string `json:"host,omitempty"`
	Status int    `json:"status,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Since  int64  `json:"since,omitempty"`
}

// pushHostRow is one push server's line on the admins' card.
type pushHostRow struct {
	Host    string `json:"host"`
	Devices int    `json:"devices"`
	Working int    `json:"working"`
	Waiting int    `json:"waiting"`
	Failing int    `json:"failing"`
	Refused int    `json:"refused"`

	// FailingSince is the earliest moment a device here began failing,
	// Unix ms, or absent.
	FailingSince int64 `json:"failingSince,omitempty"`
}

// countPushHosts adds a person's devices to the rows by host.
func countPushHosts(rows map[string]*pushHostRow, doc *pushDoc, now time.Time) int {
	devices := 0
	for _, device := range doc.Devices {
		host, waiting := device.Host, false
		if device.Endpoint == "" {
			if !device.pendingLive(now) {
				continue
			}
			host, waiting = device.Pending.Host, true
		}
		devices++

		row := rows[host]
		if row == nil {
			row = &pushHostRow{Host: host}
			rows[host] = row
		}
		row.Devices++

		failingFrom := int64(0)
		switch {
		case waiting:
			row.Waiting++
		case device.State == pushStateRefused:
			row.Refused++
		case device.State == pushStateFailing:
			row.Failing++
			failingFrom = device.Since
		default:
			// Confirmed, a run of failed sends or not: GET /me/push says
			// confirmed, and pushes go to it.
			row.Working++
		}
		if failingFrom != 0 && (row.FailingSince == 0 || failingFrom < row.FailingSince) {
			row.FailingSince = failingFrom
		}
	}

	return devices
}

// sortedPushHosts is the rows, most devices first, then by host.
func sortedPushHosts(rows map[string]*pushHostRow) []pushHostRow {
	sorted := make([]pushHostRow, 0, len(rows))
	for _, row := range rows {
		sorted = append(sorted, *row)
	}
	sort.Slice(sorted, func(a, b int) bool {
		if sorted[a].Devices != sorted[b].Devices {
			return sorted[a].Devices > sorted[b].Devices
		}
		return sorted[a].Host < sorted[b].Host
	})

	return sorted
}
