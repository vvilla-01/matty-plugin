package main

import (
	"sort"
	"sync"
	"time"
)

// Who is watching which stream, for the faces on its card.
//
// Broadcast Box knows every session on a stream but not whose it is: its
// /api/status lists a uuid and a bitrate for each. The plugin knows whose it
// is, because the panel's offer goes through handleWHEP as a signed-in user,
// and the answer comes back with the session's uuid in its Location. So the
// two are written down together when the offer is relayed, and each poll puts
// faces on whichever of those sessions Broadcast Box still lists.
//
// Only what the panel relayed has a face. Somebody watching on the public site
// or in a player of their own is in the viewer count as before and named
// nowhere, and a peek is neither. What is kept is a session id, a user id and a
// handle — never a stream key — in memory only, and none of it while the
// setting is off.
//
// The same record is what the Top Snoopers board counts hours from (see
// watchtime.go). With faces off and that board on, only the sessions of people
// who have linked their stream are kept, because they are the only people the
// board counts; nobody else's watching is remembered at all.

const (
	// How long a remembered session outlives its last appearance in the
	// listing, or its recording if it never appeared. Long enough for a poll
	// or two to go by and for Broadcast Box to list a session it has only just
	// opened; a session missing for longer than this has been closed.
	watcherSessionMemory = 60 * time.Second

	// Every session remembered at once. Far above any real audience; there so
	// that the map's size is a number somebody chose, since what goes into it
	// is decided by how often people press play.
	maxWatcherSessions = 4096

	// The most faces one card is sent. It shows three and a count for the
	// rest, and the snapshot goes to every client whenever it changes, so a
	// crowd is not worth sending by name.
	maxWatchersPerStream = 20

	// How long a watcher's name is believed. Without it, a poll every few
	// seconds would be a GetUser for every watcher every few seconds, for as
	// long as they watch, to learn a name that almost never changes.
	watcherNameTTL = 10 * time.Minute

	// And how long "could not be looked up" is. Short, because that is
	// usually the user store having a bad moment rather than an account that
	// has gone, and a face missing for a minute costs little.
	watcherMissTTL = time.Minute

	// Names held at once. Bounded like every other cache here, because who
	// goes into it is decided by who presses play.
	maxWatcherNames = 1024
)

// watcherSession is one Broadcast Box session the panel opened for somebody.
type watcherSession struct {
	userID string
	handle string

	// When this person started watching this stream, as far as the plugin
	// knows: when this session was recorded, or when an earlier one of theirs
	// on the same stream was, while that one is still remembered. Faces are
	// ordered by it, so a browser that renegotiates keeps its place.
	since time.Time

	// When the session was recorded, and when Broadcast Box last listed it,
	// which is zero until it has.
	recorded time.Time
	seen     time.Time
}

// heard is the last the plugin knew this session to be open: the last poll
// that listed it, or its recording when no poll has yet.
func (w *watcherSession) heard() time.Time {
	if w.seen.After(w.recorded) {
		return w.seen
	}

	return w.recorded
}

// watcherStore is the shared state behind the faces. Made on first use, so a
// Plugin built directly in a test has one without anybody setting it up.
type watcherStore struct {
	lock sync.Mutex

	// Keyed by Broadcast Box session id: the same uuid as sessions[].id in
	// /api/status, so a poll finds each one without a search.
	sessions map[string]*watcherSession

	// Display names by user id. "" is a lookup that failed, kept for
	// watcherMissTTL so a failing user store is not asked again every poll.
	names *memoryCache[string]
}

func newWatcherStore() *watcherStore {
	return &watcherStore{
		sessions: map[string]*watcherSession{},
		names:    newMemoryCache[string](maxWatcherNames, 0, nil),
	}
}

func (p *Plugin) watchers() *watcherStore {
	p.watchersOnce.Do(func() {
		p.watchersState = newWatcherStore()
	})

	return p.watchersState
}

// record remembers a session handleWHEP relayed for userID on handle. Nothing
// is remembered without an id to find the session by in the listing.
//
// When the store is full, the session heard of longest ago makes room. That is
// the one most likely to have closed already; a session that is still open is
// heard of on every poll.
func (s *watcherStore) record(sessionID, userID, handle string, now time.Time) {
	if sessionID == "" || userID == "" || handle == "" {
		return
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	since := now
	for _, session := range s.sessions {
		if session.userID == userID && session.handle == handle && session.since.Before(since) {
			since = session.since
		}
	}

	if _, known := s.sessions[sessionID]; !known && len(s.sessions) >= maxWatcherSessions {
		s.dropStalest()
	}

	s.sessions[sessionID] = &watcherSession{userID: userID, handle: handle, since: since, recorded: now}
}

// dropStalest forgets the session heard of longest ago. Called with lock held.
func (s *watcherStore) dropStalest() {
	stalest, stalestAt := "", time.Time{}
	for id, session := range s.sessions {
		heard := session.heard()
		if stalest == "" || heard.Before(stalestAt) || (heard.Equal(stalestAt) && id < stalest) {
			stalest, stalestAt = id, heard
		}
	}
	delete(s.sessions, stalest)
}

// observe notes every remembered session Broadcast Box lists, on any stream,
// on the air or not: a session that is still open is somebody still watching,
// even through an OBS reconnect that keeps the stream off the list for a poll.
// By id alone, because Broadcast Box makes a fresh uuid for every session, so
// one can only ever appear under the stream it was opened on.
//
// Called once per poll, before anything is listed or swept, so a session that
// is in this poll's listing is never forgotten by this poll's sweep.
func (s *watcherStore) observe(raw []bbStream, now time.Time) {
	s.lock.Lock()
	defer s.lock.Unlock()

	if len(s.sessions) == 0 {
		return
	}

	for _, source := range raw {
		for _, session := range source.Sessions {
			if remembered := s.sessions[session.ID]; remembered != nil {
				remembered.seen = now
			}
		}
	}
}

// list returns who is watching handle, by user id, earliest first: the
// distinct people with a remembered session on it among sessions, which the
// caller has already cleared of peeks. Somebody with two tabs open is listed
// once, from whichever they opened first.
//
// With them, how many of sessions have nobody the plugin knows of behind
// them. One each, because nothing says whether two of those are one person.
func (s *watcherStore) list(handle string, sessions []bbSession) ([]string, int) {
	s.lock.Lock()
	defer s.lock.Unlock()

	since, strangers := s.audienceLocked(handle, sessions)

	people := make([]string, 0, len(since))
	for userID := range since {
		people = append(people, userID)
	}

	// The user id breaks a tie, so two people who pressed play in the same
	// instant do not swap places from one poll to the next.
	sort.Slice(people, func(i, j int) bool {
		a, b := since[people[i]], since[people[j]]
		if !a.Equal(b) {
			return a.Before(b)
		}

		return people[i] < people[j]
	})

	return people, strangers
}

// audience is who is watching handle among sessions, by user id, each with
// when they started: the earliest since of any of their remembered sessions on
// it. The faces and the Top Snoopers board both start from this, so the two can
// never disagree about who was watching.
func (s *watcherStore) audience(handle string, sessions []bbSession) map[string]time.Time {
	s.lock.Lock()
	defer s.lock.Unlock()

	since, _ := s.audienceLocked(handle, sessions)

	return since
}

// audienceLocked is audience, with how many of sessions have nobody the plugin
// knows behind them. Called with lock held.
func (s *watcherStore) audienceLocked(handle string, sessions []bbSession) (map[string]time.Time, int) {
	strangers := 0
	since := map[string]time.Time{}
	for _, session := range sessions {
		remembered := s.sessions[session.ID]
		if remembered == nil || remembered.handle != handle {
			strangers++
			continue
		}

		if at, known := since[remembered.userID]; !known || remembered.since.Before(at) {
			since[remembered.userID] = remembered.since
		}
	}

	return since, strangers
}

// forget drops every session remembered for userID, on any stream.
func (s *watcherStore) forget(userID string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	for id, session := range s.sessions {
		if session.userID == userID {
			delete(s.sessions, id)
		}
	}
}

// sweep forgets every session that has not been heard of for
// watcherSessionMemory. With enabled false — faces off and the Top Snoopers
// board off — everything goes at once: nothing is kept about who watches what
// that nobody is going to be shown or count.
func (s *watcherStore) sweep(now time.Time, enabled bool) {
	// The names go too. Without the sessions they no longer say what anybody
	// watched, but they are still a list of who watched lately, which is just
	// as much nobody's business once the setting is off. The cache has a lock
	// of its own, so it is emptied before s.lock is taken rather than inside
	// it.
	if !enabled {
		s.names.clear()
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	for id, session := range s.sessions {
		if !enabled || now.Sub(session.heard()) >= watcherSessionMemory {
			delete(s.sessions, id)
		}
	}
}

// dropWatchersWhenOff is for a poll that publishes without calling decorate:
// one with nothing to reach Broadcast Box by, or one that cannot reach it and
// carries the last list forward. Those cards were made under the settings of
// the time, and the setting may have been switched off since — switching it
// off is itself what restarts the poller — so with it off their faces come
// off, with the count beside them, and the store is emptied as decorate would
// have emptied it.
//
// On a copy, because the carried-forward slice is the published snapshot's,
// which readers hold without a lock. With the setting on, the faces stay and
// nothing is swept: an unreachable origin lists no sessions, which is no
// evidence that anybody stopped watching, and the next poll that reaches it
// sorts out who has.
//
// With the Top Snoopers board on, the sessions stay and only the names go, for
// the same reason: the board counts hours from them, and somebody's watching
// carries on through an outage the plugin merely could not see. The next poll
// that reaches Broadcast Box drops anybody the board would not count; see
// recordWatchTime.
func (p *Plugin) dropWatchersWhenOff(current *snapshot, cfg *configuration) {
	if cfg.ShowWatchers {
		return
	}

	if cfg.TopWatchers {
		p.watchers().names.clear()
	} else {
		p.watchers().sweep(time.Now(), false)
	}

	if len(current.Streams) == 0 {
		return
	}

	bare := make([]apiStream, len(current.Streams))
	copy(bare, current.Streams)
	for i := range bare {
		bare[i].Watchers, bare[i].Others = nil, 0
	}
	current.Streams = bare
}

// watchersOf names the people watching a listed stream, earliest first, up to
// maxWatchersPerStream. sessions are the ones its viewer count is made of.
//
// Somebody whose account cannot be read, or has been deactivated, is left out
// rather than shown without a name, and the next one takes their place; the
// count still has them. The lookups happen with no lock held, so a slow user
// store holds up this poll and nothing else.
//
// With them, how many more people are watching than are named: every session
// with nobody the plugin knows behind it, and every person it knows of and
// did not name, past the cap or with no name to show. Counted in people
// rather than taken from the viewer count, which counts sessions: somebody
// named with the stream open in two tabs is two viewers and one face, and the
// difference between those is nobody. Zero when nobody is named, because then
// the card shows no faces for it to go beside.
func (p *Plugin) watchersOf(handle string, sessions []bbSession) ([]apiWatcher, int) {
	store := p.watchers()
	people, strangers := store.list(handle, sessions)

	var watchers []apiWatcher
	for _, userID := range people {
		if len(watchers) >= maxWatchersPerStream {
			break
		}

		if who := p.watcherName(store, userID); who != "" {
			watchers = append(watchers, apiWatcher{UserID: userID, Who: who})
		}
	}

	if len(watchers) == 0 {
		return nil, 0
	}

	// Everybody named is one of people, and only once, so this is never
	// below zero and never counts anybody who is already a face.
	return watchers, strangers + len(people) - len(watchers)
}

// watcherName is what a watcher is called on a card: the same name a streamer
// is given, through the cache. "" when there is no name to show.
func (p *Plugin) watcherName(store *watcherStore, userID string) string {
	if who, known := store.names.get(userID); known {
		return who
	}

	who := ""
	if user, appErr := p.API.GetUser(userID); appErr == nil && user != nil && user.DeleteAt == 0 {
		who = displayNameOf(user)
	}

	ttl := watcherNameTTL
	if who == "" {
		ttl = watcherMissTTL
	}
	store.names.put(userID, who, ttl)

	return who
}
