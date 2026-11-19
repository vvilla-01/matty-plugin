package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
)

// The one thing in this panel that people write rather than watch: a note of
// when somebody plans to stream.
//
// These are the only documents ORDINARY USERS write. Everything else the
// plugin stores is written by the poller, one goroutine, serialised by
// statsWriteLock — so "read it, change it, write it back" was safe by
// construction. It is not safe here: two people can plan a stream in the same
// second, and a plain KVSet would hand the last one the write and silently
// drop the other, with a 200 OK for its trouble.
//
// So every mutation below goes through casUpdate, which writes only if the
// stored bytes are still the ones it read, and retries when they are not. Two
// rules make that correct rather than merely careful:
//
//  1. A mutation must be safe to REPLAY, because a retry re-applies it. An
//     entry therefore carries its own id and is appended by identity, never by
//     a count that a retry could advance twice.
//  2. The compare is against the exact bytes read, not a re-marshal of the
//     decoded struct: Go map ordering makes a re-marshal a different byte
//     string for the same data, and the compare would never match.
//
// There used to be a vote here too — "what are we watching next" — removed in
// 1.17 because a plain Mattermost poll in the channel does the same job without
// a second place to look. Its two documents are deleted on activation by
// removeRetiredDocuments so they do not sit in the store for ever.

// The two kinds of thing anybody here plans, and a third state for everything
// else. A category is a hint for the eye — one icon on the card — so an entry
// without one is ordinary rather than incomplete, and every entry planned
// before 2.0 is exactly that.
const (
	categoryMovie = "movie"
	categoryGame  = "game"
)

// scheduleCategory drops anything it does not recognise rather than refusing
// the request. A client that posts a category this build has never heard of
// still gets its stream planned, which is the right failure: the icon is the
// least important thing on the card.
func scheduleCategory(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case categoryMovie:
		return categoryMovie
	case categoryGame:
		return categoryGame
	default:
		return ""
	}
}

const (
	kvSchedule = "schedule"

	// Versioned inside each document rather than through statsSchemaVersion.
	// That marker governs the airtime tables, and a downgrade seeing a bumped
	// one halts airtime recording — the schedule changing shape must not be
	// able to stop the hours being counted.
	communityVersion = 1

	maxCommunityBody   = 8 << 10
	maxScheduleTitle   = 80
	maxScheduleEntries = 40
	maxSchedulePerUser = 3

	// Streamers who can say they are in on one plan. A group stream on an
	// eight-person server is four or five POVs; this is room to spare, and a
	// bound on a list every panel receives.
	maxScheduleJoined = 12

	// How long a planned stream stays on the list after its time passes, and
	// how far ahead one may be planned.
	scheduleGraceMS   = int64(3 * 60 * 60 * 1000)
	scheduleHorizonMS = int64(90 * 24 * 60 * 60 * 1000)

	// Enough retries to lose a few races on an eight-person server and still
	// land; few enough that a hot loop cannot hold a request open.
	casAttempts = 8
)

var (
	errTooBusy      = errors.New("too many people are changing this at once; try again")
	errTooManyItems = errors.New("there is no room for another one")
	errMinePerUser  = errors.New("you already have three planned; remove one first")

	// errNoPlan is a plan that is not there any more — cancelled, or past —
	// and errNotYourPlan one that is somebody else's to change.
	errNoPlan      = errors.New("that plan is not there any more")
	errNotYourPlan = errors.New("only the person who planned it can change it")

	// A join that is refused for what the plan is, not for who is asking.
	errPlanFull = errors.New("that one is full")
	errOwnPlan  = errors.New("it is your own plan")

	// errStore is any failure of the key-value store itself. Its text is
	// whatever the database said, which is not something to put in a sidebar.
	errStore = errors.New("could not reach the store")
)

// retiredDocuments are keys written by a feature that no longer exists. Deleted
// once per activation, which is idempotent — KVDelete on a key that is not
// there does nothing — rather than left to sit in the store unread for ever.
var retiredDocuments = []string{"vote_active", "vote_last"}

func (p *Plugin) removeRetiredDocuments() {
	for _, key := range retiredDocuments {
		if appErr := p.API.KVDelete(key); appErr != nil {
			p.API.LogWarn("Could not remove a retired document", "key", key, "error", appErr.Error())
		}
	}
}

type scheduleEntry struct {
	ID     string `json:"id"`
	UserID string `json:"userId"`
	Title  string `json:"title"`

	// StartsAt is an INSTANT, in epoch milliseconds UTC. Never a wall-clock
	// string and never a zone name: the whole point is that every viewer reads
	// the same moment in their own zone, and a stored "Friday 8pm" would be
	// wrong for everyone but its author — and wrong for its author too, twice
	// a year, when their offset changes between planning and streaming.
	StartsAt int64 `json:"startsAt"`

	// Category is "movie", "game", or absent. Omitted when empty so a document
	// written before 2.0 and one written after it are byte-identical for an
	// entry with no category, which keeps casUpdate's compare-on-bytes honest.
	Category string `json:"category,omitempty"`

	// Media is the movie, show or game picked for it, when one was, as it was
	// looked up at the moment of saving. Omitted when absent for the same
	// reason as Category: an entry without one is byte-identical to an entry
	// written before titles could be picked at all.
	Media *scheduleMedia `json:"media,omitempty"`

	// Joined is the user ids of other streamers who have said they are in, in
	// the order they joined. Ids only: names are resolved when the schedule is
	// read, so a rename shows everywhere at once. Omitted when empty, like
	// the fields above.
	Joined []string `json:"joined,omitempty"`

	AddedAt int64 `json:"addedAt"`
}

type scheduleDoc struct {
	Version int             `json:"v"`
	Entries []scheduleEntry `json:"entries"`
}

type scheduleItem struct {
	ID       string           `json:"id"`
	Title    string           `json:"title"`
	StartsAt int64            `json:"startsAt"`
	Category string           `json:"category,omitempty"`
	Media    *scheduleMedia   `json:"media,omitempty"`
	UserID   string           `json:"userId,omitempty"`
	Who      string           `json:"who"`
	Joined   []scheduleJoiner `json:"joined,omitempty"`

	// Full is whether a join would be refused for want of room, by the very
	// count joinScheduleEntry refuses on. Not the length of Joined: that
	// leaves out anybody whose account has gone, who still holds a place, so
	// a client counting the names it was sent could offer a Join the server
	// then refuses. Always sent, so neither client keeps the cap or the rule.
	Full bool `json:"full"`
}

// scheduleJoiner is somebody who is in on a plan, as the panel shows them.
type scheduleJoiner struct {
	UserID string `json:"userId"`
	Who    string `json:"who"`
}

func newID() string {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return ""
	}

	return hex.EncodeToString(raw)
}

// withoutOwnKey removes the author's own stream key, and their publish token,
// from anything they typed.
//
// The people writing here are exactly the people with a publish key on their
// clipboard, and what they type is shown to every signed-in user. Applied at
// WRITE time so a mis-paste is never stored, let alone broadcast.
//
// The token matters more than the key: the key is on every card anyway, but
// whoever holds the token can go live as them. And it is what Relink asks to
// have pasted, right beside the What's on? box. It goes first, so a key that
// happens to be part of the token cannot leave the rest of it behind.
func (p *Plugin) withoutOwnKey(userID, value string) string {
	if token := p.getLinkedToken(userID); token != "" {
		value = redactStreamKey(value, token, "my stream")
	}

	if key := p.getLinkedKey(userID); key != "" {
		value = redactStreamKey(value, key, "my stream")
	}

	return value
}

// cleanTyped is cleanText for something a person typed that everybody will
// see: their own publish token and stream key come out FIRST, while the whole
// of what they typed is there to find them in, and only then is it cut to
// length. Cut first, and a token pasted near the end would be left half in —
// half a credential, on every screen.
func (p *Plugin) cleanTyped(userID, value string, maxRunes int) string {
	return cleanText(p.withoutOwnKey(userID, strings.TrimSpace(model.SanitizeUnicode(value))), maxRunes)
}

// cleanText bounds anything a person typed before it is stored or shown.
func cleanText(value string, maxRunes int) string {
	trimmed := strings.TrimSpace(model.SanitizeUnicode(value))
	if utf8.RuneCountInString(trimmed) > maxRunes {
		trimmed = string([]rune(trimmed)[:maxRunes])
	}

	return strings.TrimSpace(trimmed)
}

// casUpdate is the only way these documents are written.
//
// apply receives the bytes exactly as stored and returns what should replace
// them, or nil to leave them alone. It may be called more than once, so it must
// not accumulate anything outside its own return value.
func (p *Plugin) casUpdate(key string, apply func(raw []byte) ([]byte, error)) error {
	for attempt := 0; attempt < casAttempts; attempt++ {
		raw, appErr := p.API.KVGet(key)
		if appErr != nil {
			p.API.LogWarn("Could not read before writing", "key", key, "error", appErr.Error())

			return errStore
		}
		if len(raw) == 0 {
			// Nil, not an empty slice: KVCompareAndSet reads a nil oldValue as
			// "insert only if absent", which is what creates the document
			// safely when two people write it at the same moment.
			raw = nil
		}

		next, err := apply(raw)
		if err != nil {
			return err
		}
		if next == nil {
			return nil
		}

		written, appErr := p.API.KVCompareAndSet(key, raw, next)
		if appErr != nil {
			p.API.LogWarn("Could not write", "key", key, "error", appErr.Error())

			return errStore
		}
		if written {
			return nil
		}
	}

	return errTooBusy
}

func (p *Plugin) readSchedule() (*scheduleDoc, error) {
	raw, appErr := p.API.KVGet(kvSchedule)
	if appErr != nil {
		p.API.LogWarn("Could not read the schedule", "error", appErr.Error())

		return nil, errStore
	}
	if len(raw) == 0 {
		return &scheduleDoc{Version: communityVersion}, nil
	}

	var doc scheduleDoc
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Version > communityVersion {
		p.setAside(kvSchedule, raw, "The schedule could not be read and has been set aside")

		return &scheduleDoc{Version: communityVersion}, nil
	}

	return &doc, nil
}

// scheduleItems returns the upcoming entries, soonest first, with the streamer
// resolved. An entry whose owner no longer resolves is dropped rather than
// shown as an anonymous promise.
//
// This is the whole of what the panel receives. It goes in the snapshot, which
// is ONE object served identically to everybody, so nothing here may be
// specific to a viewer and nothing here is formatted: the server sends one
// instant and each browser turns it into words in its own zone.
//
// A false is "the store would not answer", which is NOT the same as "nothing is
// planned" and must not be published as though it were.
func (p *Plugin) scheduleItems(nowMS int64) ([]scheduleItem, bool) {
	doc, err := p.readSchedule()
	if err != nil {
		return nil, false
	}

	live := prune(doc.Entries, nowMS)
	items := make([]scheduleItem, 0, len(live))

	// Resolved once per PERSON, not once per entry: this runs on every poll,
	// and somebody with three planned sessions should not cost three lookups
	// every fifteen seconds for as long as they are planned.
	people := map[string]*model.User{}

	// A 404 is "this account has gone", which is an answer. Anything else is
	// the user store failing to answer, and the two must not be confused:
	// treating a database blip as "everybody who planned something has been
	// deleted" would publish an empty schedule to every panel and take the
	// whole row off every screen — the exact confident lie the halt above
	// exists to prevent.
	resolve := func(userID string) (*model.User, bool) {
		if user, known := people[userID]; known {
			return user, true
		}

		found, getErr := p.API.GetUser(userID)
		if getErr != nil && getErr.StatusCode != http.StatusNotFound {
			p.API.LogWarn("Could not resolve a planned stream's owner", "error", getErr.Error())

			return nil, false
		}

		var user *model.User
		if getErr == nil {
			user = found
		}
		people[userID] = user

		return user, true
	}

	for _, entry := range live {
		user, ok := resolve(entry.UserID)
		if !ok {
			return nil, false
		}

		// An entry whose owner has gone, or been deactivated, is dropped
		// rather than shown as an anonymous promise nobody will keep.
		if user == nil || user.DeleteAt != 0 {
			continue
		}

		// Everybody who is in, by name. Somebody whose account has gone is
		// left out, like an owner who has; a lookup that FAILS halts the whole
		// read for the same reason as above.
		var joined []scheduleJoiner
		for _, joinerID := range entry.Joined {
			joiner, ok := resolve(joinerID)
			if !ok {
				return nil, false
			}
			if joiner == nil || joiner.DeleteAt != 0 || joinerID == entry.UserID {
				continue
			}

			joined = append(joined, scheduleJoiner{UserID: joinerID, Who: displayNameOf(joiner)})
		}

		items = append(items, scheduleItem{
			ID:       entry.ID,
			Title:    entry.Title,
			StartsAt: entry.StartsAt,
			Category: scheduleCategory(entry.Category),
			Media:    entry.Media.checked(),
			UserID:   entry.UserID,
			Who:      displayNameOf(user),
			Joined:   joined,
			Full:     scheduleFull(&entry),
		})
	}

	return items, true
}

// scheduleFull is whether a plan has no room for anybody else: the one place
// the cap is applied, for the join that is refused and for the flag every
// client reads (scheduleItem.Full).
func scheduleFull(entry *scheduleEntry) bool {
	return len(entry.Joined) >= maxScheduleJoined
}

// prune drops entries whose time has been and gone, soonest first. The grace
// keeps something that started an hour ago on the list: it is probably on air
// right now, which is exactly when people look.
func prune(entries []scheduleEntry, nowMS int64) []scheduleEntry {
	kept := make([]scheduleEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.StartsAt+scheduleGraceMS > nowMS {
			kept = append(kept, entry)
		}
	}

	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].StartsAt != kept[j].StartsAt {
			return kept[i].StartsAt < kept[j].StartsAt
		}

		return kept[i].ID < kept[j].ID
	})

	return kept
}

// setAside keeps a copy of a document that will not decode and then REMOVES it,
// so the reader stops walking into it.
//
// "Quarantine and carry on" without the removal meant every poll re-read the
// same bad bytes, attempted the same insert-only write and logged the same
// warning — thousands of pointless round trips and log lines a day, for as long
// as it stood. Compare-and-delete, so a writer who has already replaced it in
// the meantime is not undone.
//
// Deliberately different from the airtime rule, which halts rather than
// removes: those hours exist nowhere else, whereas a plan to stream on Friday
// is disposable, and the feature working again matters more than the broken
// document.
func (p *Plugin) setAside(key string, raw []byte, reason string) {
	p.quarantine(key, raw)

	if _, appErr := p.API.KVCompareAndDelete(key, raw); appErr != nil {
		p.API.LogWarn("Could not remove an unreadable document", "key", key, "error", appErr.Error())
		return
	}

	p.API.LogWarn(reason, "key", key)
}

func (p *Plugin) addScheduleEntry(userID, title, category string, startsAt, nowMS int64) error {
	_, err := p.addScheduleEntryWithMedia(userID, title, category, nil, startsAt, nowMS)

	return err
}

// checkPlan is the title as it will be stored, or why the plan cannot be. Its
// own function so the endpoint can ask it BEFORE spending up to eight seconds
// looking a title up for a plan that was never going to be saved.
func (p *Plugin) checkPlan(userID, title string, startsAt, nowMS int64) (string, error) {
	// Clamped again AFTER the redaction, not only before it. Redacting swaps a
	// stream key for the words "my stream", and a key shorter than that makes
	// the title longer than the clamp just allowed.
	title = p.cleanTyped(userID, title, maxScheduleTitle)
	if title == "" {
		return "", errors.New("say what you are streaming")
	}
	if startsAt <= nowMS {
		return "", errors.New("pick a time in the future")
	}
	if startsAt > nowMS+scheduleHorizonMS {
		return "", errors.New("that is too far ahead")
	}

	return title, nil
}

// addScheduleEntryWithMedia plans a stream, and answers the new plan's id, so
// the plan sheet can offer to share it at once (design/3.2.0.md §6.3). media
// has already been looked up, or is nil: nothing here reaches outside the
// store, because apply may run several times and a lookup inside it would be
// repeated with it.
//
// The id is made once, before the write, so that a retry appends the same
// entry it would have, and the id answered is the one stored.
func (p *Plugin) addScheduleEntryWithMedia(userID, title, category string, media *scheduleMedia, startsAt, nowMS int64) (string, error) {
	title, err := p.checkPlan(userID, title, startsAt, nowMS)
	if err != nil {
		return "", err
	}

	id := newID()
	if id == "" {
		return "", errStore
	}

	return id, p.casUpdate(kvSchedule, func(raw []byte) ([]byte, error) {
		doc := &scheduleDoc{Version: communityVersion}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, doc); err != nil {
				doc = &scheduleDoc{Version: communityVersion}
			}
		}

		doc.Version = communityVersion
		doc.Entries = prune(doc.Entries, nowMS)

		mine := 0
		for _, entry := range doc.Entries {
			if entry.UserID == userID {
				mine++
			}
		}
		if mine >= maxSchedulePerUser {
			return nil, errMinePerUser
		}
		if len(doc.Entries) >= maxScheduleEntries {
			return nil, errTooManyItems
		}

		doc.Entries = append(doc.Entries, scheduleEntry{
			ID:       id,
			UserID:   userID,
			Title:    title,
			StartsAt: startsAt,
			Category: scheduleCategory(category),
			Media:    media,
			AddedAt:  nowMS,
		})

		return json.Marshal(doc)
	})
}

func (p *Plugin) removeScheduleEntry(userID, entryID string, isAdmin bool, nowMS int64) error {
	return p.casUpdate(kvSchedule, func(raw []byte) ([]byte, error) {
		if len(raw) == 0 {
			return nil, nil
		}

		doc := &scheduleDoc{Version: communityVersion}
		if err := json.Unmarshal(raw, doc); err != nil {
			return nil, nil
		}

		kept := make([]scheduleEntry, 0, len(doc.Entries))
		removed := false
		for _, entry := range doc.Entries {
			if entry.ID == entryID && (entry.UserID == userID || isAdmin) {
				removed = true
				continue
			}
			kept = append(kept, entry)
		}

		if !removed {
			return nil, nil
		}

		doc.Version = communityVersion
		doc.Entries = prune(kept, nowMS)

		return json.Marshal(doc)
	})
}

// checkRoom reports whether there is room for another plan from this person,
// read without writing. The endpoint asks it before looking a title up, so
// somebody already at their limit does not make the server call TMDB, Steam or
// IGDB for a plan that is going to be refused. The write itself checks again:
// this is an early answer, not the rule.
func (p *Plugin) checkRoom(userID string, nowMS int64) error {
	doc, err := p.readSchedule()
	if err != nil {
		return err
	}

	live := prune(doc.Entries, nowMS)

	mine := 0
	for _, entry := range live {
		if entry.UserID == userID {
			mine++
		}
	}
	if mine >= maxSchedulePerUser {
		return errMinePerUser
	}
	if len(live) >= maxScheduleEntries {
		return errTooManyItems
	}

	return nil
}

// ownPlan returns one of this person's plans as it stands, or why it cannot be
// changed by them. Read without writing, like checkRoom, so the edit endpoint
// can decide whether a title needs looking up at all before it does.
func (p *Plugin) ownPlan(userID, entryID string, nowMS int64) (*scheduleEntry, error) {
	doc, err := p.readSchedule()
	if err != nil {
		return nil, err
	}

	for _, entry := range prune(doc.Entries, nowMS) {
		if entry.ID != entryID {
			continue
		}
		if entry.UserID != userID {
			return nil, errNotYourPlan
		}

		found := entry

		return &found, nil
	}

	return nil, errNoPlan
}

// planMediaChange is what an edit does to a plan's linked title.
type planMediaChange int

const (
	// The title was not changed: whatever was stored stays, looked up or not.
	keepPlanMedia planMediaChange = iota

	// A different title, freshly looked up (or nil when the lookup failed).
	setPlanMedia

	// No title any more.
	clearPlanMedia
)

// checkPlanChange is checkPlan for an edit. The time is only checked if it
// CHANGED: a plan that started half an hour ago is still listed, and fixing a
// typo in its title must not demand a start time in the future — which would
// quietly move it, and take it off "Live now".
func (p *Plugin) checkPlanChange(userID, title string, startsAt, wasStartsAt, nowMS int64) error {
	if startsAt == wasStartsAt {
		if p.cleanTyped(userID, title, maxScheduleTitle) == "" {
			return errors.New("say what you are streaming")
		}

		return nil
	}

	_, err := p.checkPlan(userID, title, startsAt, nowMS)

	return err
}

// editScheduleEntry changes one of this person's own plans: the words, the
// time, the kind and the linked title. Nobody else's — an admin can cancel a
// plan, but what somebody said they would stream is theirs to change. Who is
// in on it is kept: they joined the plan, not a particular spelling of it.
func (p *Plugin) editScheduleEntry(userID, entryID, title, category string, change planMediaChange, media *scheduleMedia, startsAt, nowMS int64) error {
	title = p.cleanTyped(userID, title, maxScheduleTitle)
	if title == "" {
		return errors.New("say what you are streaming")
	}

	return p.casUpdate(kvSchedule, func(raw []byte) ([]byte, error) {
		if len(raw) == 0 {
			return nil, errNoPlan
		}

		doc := &scheduleDoc{Version: communityVersion}
		if err := json.Unmarshal(raw, doc); err != nil {
			return nil, errNoPlan
		}

		doc.Version = communityVersion
		doc.Entries = prune(doc.Entries, nowMS)

		for i := range doc.Entries {
			entry := &doc.Entries[i]
			if entry.ID != entryID {
				continue
			}
			if entry.UserID != userID {
				return nil, errNotYourPlan
			}

			// Judged against the plan as stored, inside the write: a new time
			// must be in the window, an unchanged one may already have passed.
			if err := p.checkPlanChange(userID, title, startsAt, entry.StartsAt, nowMS); err != nil {
				return nil, err
			}

			entry.Title = title
			entry.StartsAt = startsAt
			entry.Category = scheduleCategory(category)

			switch change {
			case setPlanMedia:
				entry.Media = media
			case clearPlanMedia:
				entry.Media = nil
			case keepPlanMedia:
				// A title that no longer goes with the kind of plan goes.
				if entry.Media != nil {
					if ref, ok := parseMediaRef(entry.Media.Ref); !ok || ref.category() != entry.Category {
						entry.Media = nil
					}
				}
			}

			doc.Entries = prune(doc.Entries, nowMS)

			return json.Marshal(doc)
		}

		return nil, errNoPlan
	})
}

// joinScheduleEntry says this person is in on somebody else's plan, or takes
// it back. Idempotent both ways: joining twice, or leaving what you were not
// in, writes nothing.
func (p *Plugin) joinScheduleEntry(userID, entryID string, join bool, nowMS int64) error {
	return p.casUpdate(kvSchedule, func(raw []byte) ([]byte, error) {
		if len(raw) == 0 {
			return nil, errNoPlan
		}

		doc := &scheduleDoc{Version: communityVersion}
		if err := json.Unmarshal(raw, doc); err != nil {
			return nil, errNoPlan
		}

		doc.Version = communityVersion
		doc.Entries = prune(doc.Entries, nowMS)

		for i := range doc.Entries {
			entry := &doc.Entries[i]
			if entry.ID != entryID {
				continue
			}
			if entry.UserID == userID {
				return nil, errOwnPlan
			}

			at := -1
			for n, joined := range entry.Joined {
				if joined == userID {
					at = n
					break
				}
			}

			switch {
			case join && at >= 0, !join && at < 0:
				return nil, nil
			case join:
				if scheduleFull(entry) {
					return nil, errPlanFull
				}
				entry.Joined = append(entry.Joined, userID)
			default:
				entry.Joined = append(entry.Joined[:at:at], entry.Joined[at+1:]...)
				if len(entry.Joined) == 0 {
					entry.Joined = nil
				}
			}

			return json.Marshal(doc)
		}

		return nil, errNoPlan
	})
}
