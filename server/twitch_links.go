package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Who has connected which Twitch account (design/twitch-live.md §4.5–§4.7):
// one KV document, always written by compare-and-set. The poller needs every
// connected id on every read, which rules out a key per person and a KV
// listing, for the same reason nowplaying.go gives.
//
// What is kept about a person is the least that works: their Twitch user id,
// login and display name, and when they connected. Never a token, a code or
// an e-mail. Blocking is by Mattermost user only: a list of blocked Twitch ids
// would be Twitch's data about people who had withdrawn, which Twitch's
// agreement does not allow VillaNET to keep.
//
// Withdrawing never depends on the switch. Twitch's agreement says to delete a
// person's data when they withdraw or ask, so Disconnect, an admin's
// Disconnect and Delete all Twitch data work whatever an admin has set, and
// the purge of a disconnected channel's hours runs in a worker of its own that
// only ever touches KV, so it needs nothing from Twitch.

const (
	kvTwitchLinks        = "twitch_links"
	kvTwitchTicketPrefix = "twitch_ticket_"
	kvTwitchStatePrefix  = "twitch_state_"

	twitchLinksVersion = 1

	// A takeover notice is forgotten after this, whether or not anybody read
	// it: the person has had a month to notice.
	twitchLostKeepMS = int64(30 * 24 * time.Hour / time.Millisecond)

	// At most this many people blocked from connecting.
	maxTwitchBlocked = 500

	// A purge that failed on a store error is tried again after this.
	twitchPurgeRetry = 60 * time.Second

	twitchLinksProblem = "The list of Twitch connections could not be read and was set aside (quarantine_twitch_links). Delete all Twitch data to start again."
)

var (
	// errTwitchLinksUnreadable is a link document whose bytes will not
	// decode. It is quarantined and never overwritten, and it is errStore to
	// everybody who only needs to know that the store could not help.
	errTwitchLinksUnreadable = fmt.Errorf("%w: the list of Twitch connections could not be read", errStore)

	// errPurgePanicked is a purge step that panicked, which is tried again
	// as one that failed: see runTwitchPurges.
	errPurgePanicked = errors.New("removing the hours stopped on an internal error")

	errTwitchFull        = errors.New("as many Twitch accounts are connected as this server keeps")
	errTwitchPurging     = errors.New("that Twitch channel's hours are still being removed")
	errTwitchBlocked     = errors.New("an admin has turned off Connect Twitch for this person")
	errTwitchBlockedFull = errors.New("the block list is full")
	errTwitchNotLinked   = errors.New("that person has not connected Twitch")
	errTwitchHalted      = errors.New("Twitch hours are not being recorded or removed until the plugin restarts")
)

type twitchLinks struct {
	V       int                         `json:"v"`
	Users   map[string]twitchLink       `json:"users"`
	Purge   map[string]twitchPurgeEntry `json:"purge,omitempty"`
	Blocked map[string]int64            `json:"blocked,omitempty"` // Mattermost user id -> when, ms
	Lost    map[string]twitchLost       `json:"lost,omitempty"`    // takeover notices, by previous owner
}

type twitchLink struct {
	ID      string `json:"id"`
	Login   string `json:"login"`
	Name    string `json:"name"`
	At      int64  `json:"at"`               // connected, ms
	Checked int64  `json:"checked"`          // names last confirmed with Get Users, ms
	Gone    int    `json:"gone,omitempty"`   // daily checks in a row that did not find the account
	AllDay  bool   `json:"allDay,omitempty"` // an admin marked this channel 24/7
}

type twitchPurgeEntry struct {
	User  string `json:"user"`
	At    int64  `json:"at"`
	Login string `json:"login,omitempty"`
}

// twitchLost tells a person that the Twitch account they had connected was
// proved by someone else and moved to them, so a hijack cannot go unnoticed.
type twitchLost struct {
	Login string `json:"login"`
	At    int64  `json:"at"`
}

// decodeTwitchLinks reads the stored bytes; nothing stored is an empty
// document. Bytes that will not decode, or a version this build does not
// write, are errTwitchLinksUnreadable.
func decodeTwitchLinks(raw []byte) (*twitchLinks, error) {
	doc := &twitchLinks{V: twitchLinksVersion}
	if len(raw) > 0 {
		doc = &twitchLinks{}
		if err := json.Unmarshal(raw, doc); err != nil || doc.V != twitchLinksVersion {
			return nil, errTwitchLinksUnreadable
		}
	}

	if doc.Users == nil {
		doc.Users = map[string]twitchLink{}
	}
	if doc.Purge == nil {
		doc.Purge = map[string]twitchPurgeEntry{}
	}
	if doc.Blocked == nil {
		doc.Blocked = map[string]int64{}
	}
	if doc.Lost == nil {
		doc.Lost = map[string]twitchLost{}
	}

	return doc, nil
}

// readTwitchLinks reads the document fresh. errStore when the store did not
// answer, errTwitchLinksUnreadable when it answered with bytes that will not
// decode. It never quarantines: the loop and the writers do.
func (p *Plugin) readTwitchLinks() (*twitchLinks, error) {
	raw, appErr := p.API.KVGet(kvTwitchLinks)
	if appErr != nil {
		return nil, errStore
	}

	return decodeTwitchLinks(raw)
}

// twitchIDs is every connected Twitch id, sorted. A nil document has none.
func (d *twitchLinks) twitchIDs() []string {
	if d == nil {
		return nil
	}

	ids := make([]string, 0, len(d.Users))
	for _, link := range d.Users {
		ids = append(ids, link.ID)
	}
	sort.Strings(ids)

	return ids
}

// owner is the Mattermost user a Twitch id is connected to, or "".
func (d *twitchLinks) owner(twitchID string) string {
	for userID, link := range d.Users {
		if link.ID == twitchID {
			return userID
		}
	}

	return ""
}

// lostFor is somebody's takeover notice while it is still worth showing.
func (d *twitchLinks) lostFor(userID string, nowMS int64) *twitchLost {
	lost, found := d.Lost[userID]
	if !found || nowMS-lost.At > twitchLostKeepMS {
		return nil
	}

	return &lost
}

// updateTwitchLinks is the only way the document is written: apply works on a
// freshly decoded copy, and says whether it changed anything. It may run more
// than once. Bytes that will not decode are copied aside and left exactly
// where they are, and every caller is told the store could not help: dropping
// them would orphan Twitch hours that could then never be found to purge.
// It returns the document as it now stands.
func (p *Plugin) updateTwitchLinks(nowMS int64, apply func(doc *twitchLinks) (bool, error)) (*twitchLinks, error) {
	var result *twitchLinks

	err := p.casUpdate(kvTwitchLinks, func(raw []byte) ([]byte, error) {
		result = nil

		doc, err := decodeTwitchLinks(raw)
		if err != nil {
			p.quarantine(kvTwitchLinks, raw)
			return nil, err
		}

		changed, err := apply(doc)
		if err != nil {
			return nil, err
		}

		// Old takeover notices go on every write.
		for userID, lost := range doc.Lost {
			if nowMS-lost.At > twitchLostKeepMS {
				delete(doc.Lost, userID)
				changed = true
			}
		}

		result = doc
		if !changed {
			return nil, nil
		}

		return json.Marshal(doc)
	})
	if err != nil {
		return nil, err
	}

	return result, nil
}

// setTwitchLink connects who to userID, once Twitch has proved it. previous
// is whoever had that Twitch account before, when it was taken from them, and
// purging says the person's own earlier account was queued to be purged.
func (p *Plugin) setTwitchLink(userID string, who twitchUser, nowMS int64) (doc *twitchLinks, previous string, purging bool, err error) {
	doc, err = p.updateTwitchLinks(nowMS, func(doc *twitchLinks) (bool, error) {
		previous, purging = "", false

		if _, blocked := doc.Blocked[userID]; blocked {
			return false, errTwitchBlocked
		}
		if _, waiting := doc.Purge[who.ID]; waiting {
			return false, errTwitchPurging
		}

		// A verified proof wins, as a publish token does for a stream key.
		// The channel's Twitch hours stay with the channel, keyed by its
		// Twitch id, and the person who lost it is told.
		if owner := doc.owner(who.ID); owner != "" && owner != userID {
			delete(doc.Users, owner)
			doc.Lost[owner] = twitchLost{Login: who.Login, At: nowMS}
			previous = owner
		}

		// Connecting again answers any notice of one's own.
		delete(doc.Lost, userID)

		// Replacing one account with another: the old one is connected to
		// nobody now, so its data goes.
		old, had := doc.Users[userID]
		if had && old.ID != who.ID {
			doc.Purge[old.ID] = twitchPurgeEntry{User: userID, At: nowMS, Login: old.Login}
			purging = true
		}

		if !had && len(doc.Users) >= maxTwitchLinks {
			return false, errTwitchFull
		}

		doc.Users[userID] = twitchLink{ID: who.ID, Login: who.Login, Name: who.DisplayName, At: nowMS, Checked: nowMS}

		return true, nil
	})

	return doc, previous, purging, err
}

// tellTwitchChanged tells somebody's open panels to read /me again. Nothing
// is in the event, so it can never be the thing that changes anything.
func (p *Plugin) tellTwitchChanged(userID string) {
	p.tellMe(userID)
}

// disconnectTwitch takes somebody's Twitch connection away and queues its
// hours to be purged, and with block keeps them from connecting again. It
// changes nothing for somebody with nothing to take, so pressing Disconnect
// twice is harmless. Whatever the switch says.
func (p *Plugin) disconnectTwitch(userID string, block bool, nowMS int64) error {
	removed := ""

	doc, err := p.updateTwitchLinks(nowMS, func(doc *twitchLinks) (bool, error) {
		removed = ""
		changed := false

		if link, connected := doc.Users[userID]; connected {
			delete(doc.Users, userID)
			doc.Purge[link.ID] = twitchPurgeEntry{User: userID, At: nowMS, Login: link.Login}
			removed = link.ID
			changed = true
		}

		if block {
			if _, already := doc.Blocked[userID]; !already {
				if len(doc.Blocked) >= maxTwitchBlocked {
					return false, errTwitchBlockedFull
				}
				doc.Blocked[userID] = nowMS
				changed = true
			}
		}

		return changed, nil
	})
	if err != nil {
		return err
	}

	s := p.twitchLive()
	s.noteLinks(doc)

	if removed != "" {
		s.forgetLive(removed)
		p.kickTwitchPurges()
		s.kick()
		p.refreshSoon()
	}

	p.tellTwitchChanged(userID)

	return nil
}

// unblockTwitch lets somebody connect again.
func (p *Plugin) unblockTwitch(userID string, nowMS int64) error {
	doc, err := p.updateTwitchLinks(nowMS, func(doc *twitchLinks) (bool, error) {
		if _, blocked := doc.Blocked[userID]; !blocked {
			return false, nil
		}
		delete(doc.Blocked, userID)

		return true, nil
	})
	if err != nil {
		return err
	}

	p.twitchLive().noteLinks(doc)
	p.tellTwitchChanged(userID)

	return nil
}

// clearTwitchBlocks empties the block list.
func (p *Plugin) clearTwitchBlocks(nowMS int64) error {
	doc, err := p.updateTwitchLinks(nowMS, func(doc *twitchLinks) (bool, error) {
		if len(doc.Blocked) == 0 {
			return false, nil
		}
		doc.Blocked = map[string]int64{}

		return true, nil
	})
	if err != nil {
		return err
	}

	p.twitchLive().noteLinks(doc)

	return nil
}

// setTwitchAllDay marks a connected channel 24/7, or not: tagged 24/7, no
// live badge, no medals and no airtime, like an always-on stream key.
func (p *Plugin) setTwitchAllDay(userID string, allDay bool, nowMS int64) error {
	doc, err := p.updateTwitchLinks(nowMS, func(doc *twitchLinks) (bool, error) {
		link, connected := doc.Users[userID]
		if !connected {
			return false, errTwitchNotLinked
		}
		if link.AllDay == allDay {
			return false, nil
		}
		link.AllDay = allDay
		doc.Users[userID] = link

		return true, nil
	})
	if err != nil {
		return err
	}

	p.twitchLive().noteLinks(doc)
	p.refreshSoon()

	return nil
}

// dismissTwitchNotice forgets somebody's takeover notice once they have read it.
func (p *Plugin) dismissTwitchNotice(userID string, nowMS int64) error {
	doc, err := p.updateTwitchLinks(nowMS, func(doc *twitchLinks) (bool, error) {
		if _, noticed := doc.Lost[userID]; !noticed {
			return false, nil
		}
		delete(doc.Lost, userID)

		return true, nil
	})
	if err != nil {
		return err
	}

	p.twitchLive().noteLinks(doc)

	return nil
}

// renameTwitchLinks keeps the stored names in step with Get Streams, which
// carries them on every read. Rare, so it costs a write only when a name
// really changed. What it writes passed cleanTwitchUser when the read did. If
// the write fails, the stored names stay as they were, which only ever shows:
// the player and the links take the login from the read itself.
func (p *Plugin) renameTwitchLinks(listed []twitchLive, now time.Time) {
	s := p.twitchLive()

	s.lock.Lock()
	links := s.links
	s.lock.Unlock()
	if links == nil {
		return
	}

	renamed := map[string]twitchLive{}
	for _, stream := range listed {
		owner := links.owner(stream.TwitchID)
		if owner == "" {
			continue
		}
		if link := links.Users[owner]; link.Login != stream.Login || link.Name != stream.Name {
			renamed[stream.TwitchID] = stream
		}
	}
	if len(renamed) == 0 {
		return
	}

	doc, err := p.updateTwitchLinks(now.UnixMilli(), func(doc *twitchLinks) (bool, error) {
		changed := false
		for userID, link := range doc.Users {
			stream, found := renamed[link.ID]
			if !found || (link.Login == stream.Login && link.Name == stream.Name) {
				continue
			}
			link.Login, link.Name = stream.Login, stream.Name
			doc.Users[userID] = link
			changed = true
		}

		return changed, nil
	})
	if err != nil {
		p.API.LogWarn("Could not update a connected Twitch account's name; trying again on the next read", "error", err.Error())
		return
	}

	s.noteLinks(doc)
}

// checkTwitchNames is the daily check (§4.7): Get Users for up to a hundred
// connections whose names were last confirmed more than a day ago, the
// oldest first. A name that came back is brought up to date. An account that
// did not come back two days running no longer exists on Twitch, or is
// suspended (which Twitch does not document either way, hence two), and is
// disconnected and purged, which honours Twitch reporting an account deleted.
func (p *Plugin) checkTwitchNames(ctx context.Context, creds twitchCredentials, now time.Time) {
	s := p.twitchLive()

	s.lock.Lock()
	links := s.links
	waiting := now.Before(s.namesRetryAt)
	s.lock.Unlock()
	if links == nil || waiting {
		return
	}

	type due struct {
		userID, id string
		checked    int64
	}

	var dues []due
	cutoff := now.Add(-twitchNamesEvery).UnixMilli()
	for userID, link := range links.Users {
		if link.Checked < cutoff {
			dues = append(dues, due{userID: userID, id: link.ID, checked: link.Checked})
		}
	}
	if len(dues) == 0 {
		return
	}

	sort.Slice(dues, func(i, j int) bool {
		if dues[i].checked != dues[j].checked {
			return dues[i].checked < dues[j].checked
		}

		return dues[i].userID < dues[j].userID
	})
	if len(dues) > twitchBatch {
		dues = dues[:twitchBatch]
	}

	query := url.Values{}
	for _, d := range dues {
		query.Add("id", d.id)
	}

	body, _, err := p.twitchApp().helixGet(ctx, creds, "/helix/users", query)

	var answer struct {
		Data []struct {
			ID          string `json:"id"`
			Login       string `json:"login"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err == nil && json.Unmarshal(body, &answer) != nil {
		err = errTwitchBadAnswer
	}
	if err != nil {
		s.lock.Lock()
		s.namesRetryAt = now.Add(twitchNamesRetry)
		s.lock.Unlock()

		return
	}

	// Found is found, whether or not what came back passes the check; only
	// a name that passes is ever written.
	found := map[string]bool{}
	names := map[string]twitchUser{}
	for _, user := range answer.Data {
		found[user.ID] = true
		if who, ok := cleanTwitchUser(user.ID, user.Login, user.DisplayName); ok {
			names[user.ID] = who
		}
	}

	nowMS := now.UnixMilli()
	gone := map[string]string{} // Mattermost user id -> Twitch id

	doc, err := p.updateTwitchLinks(nowMS, func(doc *twitchLinks) (bool, error) {
		gone = map[string]string{}
		changed := false

		for _, d := range dues {
			link, connected := doc.Users[d.userID]
			if !connected || link.ID != d.id {
				continue
			}

			changed = true
			link.Checked = nowMS

			if found[d.id] {
				link.Gone = 0
				if who, ok := names[d.id]; ok {
					link.Login, link.Name = who.Login, who.DisplayName
				}
				doc.Users[d.userID] = link

				continue
			}

			link.Gone++
			if link.Gone < twitchGoneChecks {
				doc.Users[d.userID] = link
				continue
			}

			delete(doc.Users, d.userID)
			doc.Purge[link.ID] = twitchPurgeEntry{User: d.userID, At: nowMS, Login: link.Login}
			gone[d.userID] = link.ID
		}

		return changed, nil
	})
	if err != nil {
		p.API.LogWarn("Could not record the daily check of connected Twitch accounts; trying again shortly", "error", err.Error())

		s.lock.Lock()
		s.namesRetryAt = now.Add(twitchNamesRetry)
		s.lock.Unlock()

		return
	}

	s.noteLinks(doc)

	for userID, id := range gone {
		p.API.LogInfo("A connected Twitch account no longer exists on Twitch; disconnecting it", "user", userID)
		s.forgetLive(id)
		p.tellTwitchChanged(userID)
	}
	if len(gone) > 0 {
		p.kickTwitchPurges()
		p.refreshSoon()
	}
}

// The purge worker.
//
// At most one, with its own context, started by a kick when none is running:
// at activation (whatever the switch says), after a disconnect, a takeover or
// a replaced account, the daily check, and Delete all. It does not run inside
// the Twitch loop, so it works with the loop stopped, and it only ever touches
// KV. It purges the oldest queued channel, then the next, and stops when the
// queue is empty, when the Twitch ledger is halted (whose restart kicks it
// again), or when the document will not decode.

// startTwitchPurges is OnActivate's: the worker may run again, and runs if
// there is anything to purge. One read of twitch_links whatever the switch.
func (p *Plugin) startTwitchPurges() {
	s := p.twitchLive()

	s.purgeLock.Lock()
	s.purgeOff = false
	s.purgeLock.Unlock()

	p.kickTwitchPurges()
}

// kickTwitchPurges starts the worker, or, when it is running, makes it look
// again before it stops, so a purge queued while it was finishing is never
// left waiting for the next activation.
func (p *Plugin) kickTwitchPurges() {
	s := p.twitchLive()

	s.purgeLock.Lock()
	defer s.purgeLock.Unlock()

	if s.purgeOff {
		return
	}
	if s.purgeDone != nil {
		s.purgeAgain = true
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.purgeCancel, s.purgeDone, s.purgeAgain = cancel, done, false

	go p.runTwitchPurges(ctx, done)
}

// stopTwitchPurges is OnDeactivate's: the worker stops, and no kick starts
// another until the next activation.
func (p *Plugin) stopTwitchPurges() {
	p.haltTwitchPurges(true)
}

// haltTwitchPurges cancels the worker and waits for it, with no stats lock
// held: a step ends within one KV read of being cancelled.
func (p *Plugin) haltTwitchPurges(off bool) {
	s := p.twitchLive()

	s.purgeLock.Lock()
	cancel, done := s.purgeCancel, s.purgeDone
	s.purgeCancel, s.purgeDone, s.purgeAgain = nil, nil, false
	if off {
		s.purgeOff = true
	}
	s.purgeLock.Unlock()

	if cancel != nil {
		cancel()
		<-done
	}
}

func (p *Plugin) runTwitchPurges(ctx context.Context, done chan struct{}) {
	defer close(done)

	s := p.twitchLive()

	for {
		// A step that panics is tried again as one that failed: every step of
		// a purge is safe to repeat (recovery.go).
		var more bool
		var err error
		if p.safely("the Twitch purges", func() { more, err = p.purgeStep(ctx, s.now()) }) {
			err = errPurgePanicked
		}
		if ctx.Err() != nil {
			return
		}

		if err != nil {
			p.API.LogWarn("Could not remove a disconnected Twitch channel's hours yet; trying again in a minute", "error", err.Error())

			select {
			case <-ctx.Done():
				return
			case <-s.after(twitchPurgeRetry):
			}

			continue
		}
		if more {
			continue
		}

		s.purgeLock.Lock()
		if s.purgeAgain {
			s.purgeAgain = false
			s.purgeLock.Unlock()

			continue
		}
		if s.purgeDone == done {
			s.purgeCancel, s.purgeDone = nil, nil
		}
		s.purgeLock.Unlock()

		return
	}
}

// purgeStep purges the oldest queued channel. more is whether there may be
// another; an error is a store that did not answer, tried again later.
func (p *Plugin) purgeStep(ctx context.Context, now time.Time) (more bool, err error) {
	if halted, _ := p.twitchAirHalt.state(); halted {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	doc, err := p.readTwitchLinks()
	if errors.Is(err, errTwitchLinksUnreadable) {
		return false, nil
	}
	if err != nil {
		return true, err
	}

	id, entry, found := oldestPurge(doc)
	if !found {
		return false, nil
	}

	if err := p.purgeTwitchHours(ctx, id, now); err != nil {
		if errors.Is(err, errTwitchHalted) {
			return false, nil
		}

		return true, err
	}

	// Last, so that a purge interrupted part way is simply done again: every
	// step of it is safe to repeat.
	nowMS := now.UnixMilli()
	updated, err := p.updateTwitchLinks(nowMS, func(doc *twitchLinks) (bool, error) {
		if _, queued := doc.Purge[id]; !queued {
			return false, nil
		}
		delete(doc.Purge, id)

		// A notice naming this channel names Twitch's data about it, which
		// goes with everything else.
		for userID, lost := range doc.Lost {
			if entry.Login != "" && lost.Login == entry.Login {
				delete(doc.Lost, userID)
			}
		}

		return true, nil
	})
	if err != nil {
		return true, err
	}

	p.twitchLive().noteLinks(updated)
	p.forgetMonthPodiums()
	p.API.LogInfo("Removed a disconnected Twitch channel's hours", "user", entry.User)

	return true, nil
}

// oldestPurge is the queued purge that has waited longest, by when it was
// queued and then by id, so the order never depends on a map.
func oldestPurge(doc *twitchLinks) (string, twitchPurgeEntry, bool) {
	found := false
	var oldestID string
	var oldest twitchPurgeEntry

	for id, entry := range doc.Purge {
		if !found || entry.At < oldest.At || (entry.At == oldest.At && id < oldestID) {
			found, oldestID, oldest = true, id, entry
		}
	}

	return oldestID, oldest, found
}

// purgeTwitchHours removes one Twitch id from every table of the Twitch
// ledger, under statsWriteLock: every month by name, from the first one there
// can be, as readEndedPodiums reads them, because a listing can miss a key;
// the totals; the session marks; and every paid span that id paid for.
// Spans Broadcast Box paid, and spans of an account the person has connected
// since, stay.
func (p *Plugin) purgeTwitchHours(ctx context.Context, id string, now time.Time) error {
	p.statsWriteLock.Lock()
	defer p.statsWriteLock.Unlock()

	changed := map[string]statTable{}

	load := func(key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		table, err := p.loadStats(key)
		if err != nil {
			if errors.Is(err, errStatsQuarantined) {
				p.twitchHaltOrSkip(err)
				return errTwitchHalted
			}

			return err
		}

		if _, has := table[id]; has {
			delete(table, id)
			changed[key] = table
		}

		return nil
	}

	current := now.UTC().Format("2006-01")
	for at := firstPossibleMonth; ; at = at.AddDate(0, 1, 0) {
		month := at.Format("2006-01")
		if month > current {
			break
		}
		if err := load(kvTwitchAirMonthPrefix + month); err != nil {
			return err
		}
	}
	if err := load(kvTwitchAirTotals); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	marks, err := p.loadSessionMarks(kvTwitchAirSessions)
	if err != nil {
		return err
	}
	_, marked := marks[id]
	delete(marks, id)

	if err := ctx.Err(); err != nil {
		return err
	}
	paid, err := p.loadTwitchPaid()
	if err != nil {
		return err
	}
	paidChanged := false
	for userID, spans := range paid {
		kept := spans[:0:0]
		for _, span := range spans {
			if span.Tw == id {
				paidChanged = true
				continue
			}
			kept = append(kept, span)
		}
		if len(kept) == 0 {
			delete(paid, userID)
		} else {
			paid[userID] = kept
		}
	}

	keys := make([]string, 0, len(changed))
	for key := range changed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := p.saveStats(key, changed[key]); err != nil {
			return err
		}
	}

	if paidChanged {
		if err := p.saveTwitchPaid(paid); err != nil {
			return err
		}
	}
	if marked {
		if err := p.saveSessionMarks(kvTwitchAirSessions, marks); err != nil {
			return err
		}
	}

	return nil
}

// deleteAllTwitchData is Delete all Twitch data: everybody disconnected, and
// every Twitch hour, name and id VillaNET holds deleted, set-aside copies
// included. Broadcast Box hours are not touched, and the block list is kept:
// it holds only Mattermost ids. Whatever the switch says.
//
// In §2.2's lock order: the loop and the purge worker are stopped and waited
// for under runLock with no stats lock held, then the keys go under
// statsWriteLock. runLock is let go by a defer, so that a panic on the way
// does not leave the loop stopped for good.
func (p *Plugin) deleteAllTwitchData(now time.Time) error {
	s := p.twitchLive()

	err := func() error {
		s.runLock.Lock()
		defer s.runLock.Unlock()

		wasRunning := s.cancel != nil
		s.stopLoopLocked()
		p.haltTwitchPurges(false)

		err := p.deleteTwitchKeys(now)

		s.clearLive(true)
		p.twitchAirHalt.clear()

		if wasRunning && p.getConfiguration().twitchLiveOn() {
			s.startLoopLocked()
		}

		return err
	}()

	p.forgetMonthPodiums()
	p.refreshSoon()

	// Everybody's open panels read /me again.
	p.tellEverybodyMe()

	return err
}

// twitchLedgerKeys is every key the Twitch ledger can have, months by name.
func twitchLedgerKeys(now time.Time) []string {
	keys := []string{kvTwitchAirTotals, kvTwitchAirSessions, kvTwitchAirPaid}

	current := now.UTC().Format("2006-01")
	for at := firstPossibleMonth; ; at = at.AddDate(0, 1, 0) {
		month := at.Format("2006-01")
		if month > current {
			break
		}
		keys = append(keys, kvTwitchAirMonthPrefix+month)
	}

	return keys
}

func (p *Plugin) deleteTwitchKeys(now time.Time) error {
	p.statsWriteLock.Lock()
	defer p.statsWriteLock.Unlock()

	var first error
	keep := func(err error) {
		if err != nil && first == nil {
			first = err
		}
	}

	// The block list survives, in a document that holds nothing else. It is
	// written by compare-and-set like every other write to it, so a block an
	// admin saves at this same moment is read again and kept rather than
	// written over. A read the store refuses writes nothing: the document
	// stays as it is, block list and all, and the admin is told to press
	// again. Writing a fresh one then would empty the block list without a
	// word, and let the people on it connect again. A document that will not
	// decode cannot say who was blocked, and goes.
	keep(p.casUpdate(kvTwitchLinks, func(raw []byte) ([]byte, error) {
		fresh := &twitchLinks{V: twitchLinksVersion, Users: map[string]twitchLink{}}
		if doc, err := decodeTwitchLinks(raw); err == nil && len(doc.Blocked) > 0 {
			fresh.Blocked = doc.Blocked
		}

		return json.Marshal(fresh)
	}))

	for _, key := range append(twitchLedgerKeys(now), kvTwitchLinks) {
		if key != kvTwitchLinks {
			if appErr := p.API.KVDelete(key); appErr != nil {
				keep(errStore)
			}
		}
		if appErr := p.API.KVDelete(kvQuarantinePrefix + key); appErr != nil {
			keep(errStore)
		}
	}

	// Tickets and states expire on their own within ten minutes; they go now
	// all the same, so nothing started before this can finish after it.
	for _, key := range p.kvKeysWithPrefix("twitch_") {
		if strings.HasPrefix(key, kvTwitchTicketPrefix) || strings.HasPrefix(key, kvTwitchStatePrefix) {
			if appErr := p.API.KVDelete(key); appErr != nil {
				keep(errStore)
			}
		}
	}

	return first
}

// twitchQuarantined is whether any set-aside copy of Twitch data exists: one
// read per name, so it cannot be missed by a listing.
func (p *Plugin) twitchQuarantined(now time.Time) bool {
	for _, key := range append(twitchLedgerKeys(now), kvTwitchLinks) {
		if raw, appErr := p.API.KVGet(kvQuarantinePrefix + key); appErr == nil && len(raw) > 0 {
			return true
		}
	}

	return false
}
