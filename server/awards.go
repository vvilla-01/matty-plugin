package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Awards by your name (design/3.2.0.md §3).
//
// Which awards somebody holds, and which of them are drawn beside their name,
// is decided on the server since 3.6.0 (awards_decide.go); until then every
// browser worked it out from the snapshot. This file is the choice itself:
// up to four award ids, in their order, or nothing chosen at all, which is
// Automatic and exactly what 3.1.0 drew. What the choice comes to is public,
// like the awards it picks from, and rides the snapshot as each person's
// awards; the choice itself no longer leaves the server except to its owner.
// It cannot live in Mattermost's preferences, which are private to their
// owner and which the server would have to read for everybody.
//
// award_prefs holds the choices and nothing else, so a damaged one can only
// put badges back to Automatic. Until 3.2.1 a second document, fan_optouts,
// held who chose not to be named as a fan; there is no opt-out any more
// (design/3.2.1.md §2), and activation deletes a leftover one (fans.go).
//
// It is read at activation, after this node writes it, and by the poller
// when its copy is more than a minute old, so another node's write shows
// within a minute. Single server is the realistic case, as for airtime.

const (
	kvAwardPrefs = "award_prefs"

	awardDocsVersion = 1

	// The most awards beside a name: four is the most 3.1.0 ever drew
	// (crown, calendar, binoculars, clapper), so with the headphones no post
	// header gets any wider than it was.
	maxAwardsShown = 4

	// Only linked people write a choice, and clearing one removes it, so
	// this is reached only by five hundred linked people.
	maxAwardPrefs = 500

	maxAwardBody = 2 << 10

	// How old this node's copy of the choices may get before the poller
	// reads them again.
	awardDocsMaxAge = time.Minute

	awardFull      = "There is no room for another choice just now."
	awardBadChoice = "Choose up to four of your awards, each once."
	awardNeedsLink = "Link your stream first — choosing your awards is for streamers."
)

// The award ids that are not about anybody in particular (§3.1). Fans and
// achievements carry what they are about after a colon.
const (
	awardAllTime = "alltime"
	awardMonth   = "month"
	awardSnooper = "snooper"
	awardClipper = "clipper"
	awardPast    = "past"

	awardFanPrefix = "fan:"
	awardAchPrefix = "ach:"
)

var errAwardFull = errors.New("no room for another choice")

// awardPrefsDoc is award_prefs: one entry per person who chose, keyed by user
// id. An entry always has show, possibly empty; somebody on Automatic has no
// entry at all.
type awardPrefsDoc struct {
	Version int                  `json:"v"`
	Entries map[string]awardPref `json:"entries"`
}

type awardPref struct {
	Show []string `json:"show"`
	At   int64    `json:"at"`
}

// awardChoice is one person's choice as a snapshot being built carries it to
// where the awards are decided: only the ids, in their order, never when it
// was made.
type awardChoice struct {
	UserID string   `json:"userId"`
	Show   []string `json:"show"`
}

// awardDocs is this node's copy of award_prefs.
type awardDocs struct {
	lock sync.Mutex

	// One conversation with the store about the document at a time on this
	// node: a read and the install of what it read, or a write and the
	// install of what it wrote. Without it a re-read that fetched the bytes
	// from just before a write could be installed after the write's own, and
	// put a choice back the way it was for up to a minute. Taken before
	// lock, never the other way.
	io sync.Mutex

	loaded bool
	readAt time.Time

	choices       map[string][]string
	prefsReadable bool

	// The fingerprint of the bad bytes last copied aside, so the copy and
	// its log line happen once per damage, not once a minute for as long as
	// it stands.
	badSeen map[string][32]byte

	// The clock, which a test replaces.
	now func() time.Time
}

func (p *Plugin) awards() *awardDocs {
	p.awardsOnce.Do(func() {
		p.awardsState = &awardDocs{
			choices:       map[string][]string{},
			prefsReadable: true,
			badSeen:       map[string][32]byte{},
			now:           time.Now,
		}
	})

	return p.awardsState
}

// decodeAwardPrefs reads award_prefs. False is bytes that will not decode, or
// that a later build wrote; nothing at all is an empty document.
func decodeAwardPrefs(raw []byte) (*awardPrefsDoc, bool) {
	doc := &awardPrefsDoc{Version: awardDocsVersion, Entries: map[string]awardPref{}}
	if len(raw) == 0 {
		return doc, true
	}

	if err := json.Unmarshal(raw, doc); err != nil || doc.Version > awardDocsVersion {
		return &awardPrefsDoc{Version: awardDocsVersion, Entries: map[string]awardPref{}}, false
	}
	if doc.Entries == nil {
		doc.Entries = map[string]awardPref{}
	}

	return doc, true
}

// refreshAwardDocs reads the choices when this node's copy is more than a
// minute old, or has never been read, or force says so. A read the store did
// not answer keeps the copy there was, and is tried again on the next poll.
func (p *Plugin) refreshAwardDocs(force bool) {
	a := p.awards()
	if !force && !a.due(a.now()) {
		return
	}

	a.io.Lock()
	defer a.io.Unlock()

	// Asked again: the read this one waited behind may have just done it.
	now := a.now()
	if !force && !a.due(now) {
		return
	}

	p.readAwardDocsLocked(now)
}

// due is whether this node's copy of the choices is old enough to read again.
func (a *awardDocs) due(now time.Time) bool {
	a.lock.Lock()
	defer a.lock.Unlock()

	return !a.loaded || now.Sub(a.readAt) >= awardDocsMaxAge || now.Before(a.readAt)
}

// readAwardDocsLocked reads the choices and installs them, with io held.
func (p *Plugin) readAwardDocsLocked(now time.Time) {
	a := p.awards()

	prefsRaw, prefsErr := p.API.KVGet(kvAwardPrefs)
	if prefsErr != nil {
		p.API.LogWarn("Could not read who chose which awards to show", "error", prefsErr.Error())
		return
	}

	p.installAwardPrefs(prefsRaw)

	a.lock.Lock()
	a.loaded = true
	a.readAt = now
	a.lock.Unlock()
}

// loadAwardDocsOnce reads the choices if this node has never managed to,
// which only a request arriving before activation has finished sees. After
// that the routes answer from memory and the poller keeps it fresh.
func (p *Plugin) loadAwardDocsOnce() {
	a := p.awards()
	if a.isLoaded() {
		return
	}

	a.io.Lock()
	defer a.io.Unlock()

	if !a.isLoaded() {
		p.readAwardDocsLocked(a.now())
	}
}

func (a *awardDocs) isLoaded() bool {
	a.lock.Lock()
	defer a.lock.Unlock()

	return a.loaded
}

// noteBadDocument copies a document that will not decode aside, once per
// damage, and says whether this is the first time this node has seen these
// bytes.
func (p *Plugin) noteBadDocument(key string, raw []byte) bool {
	a := p.awards()
	sum := sha256.Sum256(raw)

	a.lock.Lock()
	seen, known := a.badSeen[key]
	first := !known || seen != sum
	a.badSeen[key] = sum
	a.lock.Unlock()

	if first {
		p.quarantine(key, raw)
	}

	return first
}

// installAwardPrefs makes these stored bytes this node's copy. Bytes that will
// not decode are copied aside once and read as nobody chose: badges go back to
// Automatic, and nothing else depends on them. The caller holds io, so what it
// installs is what the store last said to this node.
func (p *Plugin) installAwardPrefs(raw []byte) {
	doc, ok := decodeAwardPrefs(raw)
	if !ok && p.noteBadDocument(kvAwardPrefs, raw) {
		p.API.LogError("Who chose which awards to show could not be read, so everybody's badges are Automatic until somebody chooses again",
			"key", kvAwardPrefs, "bytes", len(raw), "quarantine_key", kvQuarantinePrefix+kvAwardPrefs)
	}

	choices := make(map[string][]string, len(doc.Entries))
	for userID, entry := range doc.Entries {
		if entry.Show == nil {
			continue
		}
		choices[userID] = append([]string{}, entry.Show...)
	}

	a := p.awards()
	a.lock.Lock()
	a.choices = choices
	a.prefsReadable = ok
	a.lock.Unlock()
}

// choiceOf is somebody's choice as this node holds it, and false for
// Automatic.
func (p *Plugin) choiceOf(userID string) ([]string, bool) {
	a := p.awards()
	a.lock.Lock()
	defer a.lock.Unlock()

	show, chosen := a.choices[userID]
	if !chosen {
		return nil, false
	}

	return append([]string{}, show...), true
}

// addAwardChoices puts everybody's choice on a snapshot, from this node's
// copy: one row per person who chose, sorted by user id, and nothing else.
// Not when it was made.
func (p *Plugin) addAwardChoices(current *snapshot) {
	a := p.awards()
	a.lock.Lock()
	defer a.lock.Unlock()

	if len(a.choices) == 0 {
		current.AwardChoices = nil
		return
	}

	choices := make([]awardChoice, 0, len(a.choices))
	for userID, show := range a.choices {
		choices = append(choices, awardChoice{UserID: userID, Show: append([]string{}, show...)})
	}
	sort.Slice(choices, func(i, j int) bool { return choices[i].UserID < choices[j].UserID })

	current.AwardChoices = choices
}

// validAwardChoice is the §3.4 rule for one list: at most four ids, none
// twice, each of a known form. Whether the person holds an award is not asked:
// they may choose ahead, and an award not held is skipped until it is.
func validAwardChoice(show []string, families map[string]bool) bool {
	if len(show) > maxAwardsShown {
		return false
	}

	seen := map[string]bool{}
	for _, id := range show {
		if seen[id] || !validAwardID(id, families) {
			return false
		}
		seen[id] = true
	}

	return true
}

// withoutRetiredFamilies takes out of a list the achievement families that
// were in the person's stored choice and have since left the catalogue: a
// 24/7 channel whose line was removed, or no longer reads. Nobody can hold
// one again, and the catalogue is what an ach: id is checked against (§3.4),
// so it is not stored again; but the panel sends the whole list with every
// change, its dimmed chip included, and refusing the list would refuse every
// change they made until they happened to press that chip. A family that was
// never in their choice is still refused, as before.
func withoutRetiredFamilies(show []string, families map[string]bool, stored []string) []string {
	kept := make([]string, 0, len(show))
	for _, id := range show {
		if rest, ach := strings.CutPrefix(id, awardAchPrefix); ach && !families[rest] && slices.Contains(stored, id) {
			continue
		}
		kept = append(kept, id)
	}

	return kept
}

// validAwardID is one id of the §3.1 forms. A fan: id names a Mattermost user
// id, 26 lower-case letters and digits; an ach: id names a family in the
// catalogue as it stands now.
func validAwardID(id string, families map[string]bool) bool {
	switch id {
	case awardAllTime, awardMonth, awardSnooper, awardClipper, awardPast:
		return true
	}

	if rest, fan := strings.CutPrefix(id, awardFanPrefix); fan {
		return isUserID(rest)
	}

	if rest, ach := strings.CutPrefix(id, awardAchPrefix); ach {
		return families[rest]
	}

	return false
}

// isUserID is a Mattermost id as Mattermost makes them: 26 lower-case letters
// and digits.
func isUserID(value string) bool {
	if len(value) != 26 {
		return false
	}

	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}

	return true
}

// changeAwardPrefs is the only way award_prefs is written. change edits the
// entries as stored and says whether it changed anything. A document that will
// not decode is started afresh, which is the only thing a write could do with
// it: the bad copy is already kept aside, and it held nothing but choices.
func (p *Plugin) changeAwardPrefs(change func(entries map[string]awardPref) (bool, error)) error {
	a := p.awards()
	a.io.Lock()
	defer a.io.Unlock()

	var written []byte

	err := p.casUpdate(kvAwardPrefs, func(raw []byte) ([]byte, error) {
		written = nil

		doc, ok := decodeAwardPrefs(raw)
		if !ok {
			if p.noteBadDocument(kvAwardPrefs, raw) {
				p.API.LogError("Who chose which awards to show could not be read",
					"key", kvAwardPrefs, "bytes", len(raw), "quarantine_key", kvQuarantinePrefix+kvAwardPrefs)
			}
			p.API.LogWarn("Starting a fresh list of who chose which awards to show; the unreadable one is kept aside",
				"quarantine_key", kvQuarantinePrefix+kvAwardPrefs)
		}

		changed, err := change(doc.Entries)
		if err != nil {
			return nil, err
		}
		if !changed && ok {
			return nil, nil
		}

		doc.Version = awardDocsVersion
		next, err := json.Marshal(doc)
		written = next

		return next, err
	})
	if err != nil {
		return err
	}

	if written != nil {
		p.installAwardPrefs(written)
	}

	return nil
}

// setAwardChoice stores somebody's choice, or with show nil puts them back on
// Automatic.
func (p *Plugin) setAwardChoice(userID string, show []string, nowMS int64) error {
	return p.changeAwardPrefs(func(entries map[string]awardPref) (bool, error) {
		if show == nil {
			if _, chose := entries[userID]; !chose {
				return false, nil
			}
			delete(entries, userID)

			return true, nil
		}

		if previous, chose := entries[userID]; chose && sameIDs(previous.Show, show) {
			return false, nil
		}

		if _, chose := entries[userID]; !chose && len(entries) >= maxAwardPrefs {
			return false, errAwardFull
		}

		entries[userID] = awardPref{Show: append([]string{}, show...), At: nowMS}

		return true, nil
	})
}

func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

// awardsMineResponse is GET /api/v1/awards/mine: the caller's own choice, nil
// for Automatic, whether the choices could be read, and everything they hold
// now, in the order the picker lists it, which is what a choice picks from.
// Until 3.2.1 it also said whether they were named as a fan; there is no
// opt-out any more.
type awardsMineResponse struct {
	Show     []string `json:"show"`
	Readable bool     `json:"readable"`

	// Always there, [] for somebody who holds nothing.
	Held []heldAward `json:"held"`
}

func (p *Plugin) awardsMineOf(userID string) awardsMineResponse {
	p.loadAwardDocsOnce()

	// From the boards as the snapshot in hand has them, so the picker and
	// the badges everybody sees are decided from the same figures.
	held := awardFactsOf(p.getSnapshot()).held(userID, nil)
	if held == nil {
		held = []heldAward{}
	}

	a := p.awards()
	a.lock.Lock()
	defer a.lock.Unlock()

	answer := awardsMineResponse{Readable: a.prefsReadable, Held: held}
	if show, chose := a.choices[userID]; chose {
		answer.Show = append([]string{}, show...)
	}

	return answer
}

type awardDisplayRequest struct {
	Show *[]string `json:"show"`
}

type awardDisplayResponse struct {
	OK   bool     `json:"ok"`
	Show []string `json:"show"`
}

// handleAwardsMine is GET /api/v1/awards/mine, linked, from memory.
func (p *Plugin) handleAwardsMine(w http.ResponseWriter, userID string) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, p.awardsMineOf(userID))
}

// handleAwardDisplay is POST /api/v1/awards/display, a choice, for linked
// people; and DELETE, back to Automatic, for anybody. The caller is always
// Mattermost's header, never anything in the body, so a choice can only ever
// be one's own.
func (p *Plugin) handleAwardDisplay(w http.ResponseWriter, r *http.Request, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if r.Method == http.MethodPost && !p.requireLinkedStream(w, userID, awardNeedsLink) {
		return
	}

	var show []string

	if r.Method == http.MethodPost {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxAwardBody+1))
		if err != nil || len(body) > maxAwardBody {
			writeCodedError(w, http.StatusBadRequest, "Could not read the request.", "bad_request")
			return
		}

		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()

		var request awardDisplayRequest
		if err := decoder.Decode(&request); err != nil {
			writeCodedError(w, http.StatusBadRequest, "Could not read the request.", "bad_request")
			return
		}

		if request.Show != nil {
			families := p.achievementFamilyIDs(p.getConfiguration())
			p.loadAwardDocsOnce()
			stored, _ := p.choiceOf(userID)
			show = withoutRetiredFamilies(*request.Show, families, stored)
			if !validAwardChoice(show, families) {
				writeCodedError(w, http.StatusBadRequest, awardBadChoice, "bad_choice")
				return
			}
		}
	} else {
		// Back to Automatic writes nothing over a document that cannot be
		// read: everybody already shows Automatic, and this route is open.
		p.loadAwardDocsOnce()
		a := p.awards()
		a.lock.Lock()
		readable := a.prefsReadable
		a.lock.Unlock()

		if !readable {
			writeJSON(w, http.StatusOK, awardDisplayResponse{OK: true})
			return
		}
	}

	// Metered once the request is known to be one that writes: a choice
	// refused for its shape spends nothing.
	if !p.limiters().award.allow(userID) {
		writeTooMany(w, "Too many changes. Wait a moment and try again.", p.limiters().award, userID, time.Now())
		return
	}

	if err := p.setAwardChoice(userID, show, nowMillis()); err != nil {
		switch {
		case errors.Is(err, errAwardFull):
			p.API.LogWarn("Somebody's choice of awards was refused: the list is full", "limit", maxAwardPrefs)
			writeCodedError(w, http.StatusServiceUnavailable, awardFull, "full")
		case errors.Is(err, errTooBusy):
			writeCodedError(w, http.StatusConflict, err.Error(), "busy")
		default:
			writeCodedError(w, http.StatusServiceUnavailable, "Could not save that just now. Try again in a moment.", accessCodeStore)
		}

		return
	}

	p.refreshSoon()

	shown, _ := p.choiceOf(userID)
	writeJSON(w, http.StatusOK, awardDisplayResponse{OK: true, Show: shown})
}

// takebacksResponse is GET /api/v1/takebacks: what the caller's own profile
// popover may offer them to take back. Since 3.2.1 that is only their sounds:
// there is no biggest-fan opt-out any more (design/3.2.1.md §2).
type takebacksResponse struct {
	Sounds int `json:"sounds"`
}

// handleTakebacks answers from what this node holds in memory: no read per
// request and no meter, so a Markdown image pointing at it costs nothing. The
// sounds are as held for the soundboard, or, with it off, read at most once
// every 30 seconds for the whole server.
func (p *Plugin) handleTakebacks(w http.ResponseWriter, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	writeJSON(w, http.StatusOK, takebacksResponse{Sounds: p.soundsOwnedBy(userID)})
}
