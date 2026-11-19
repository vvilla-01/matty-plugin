package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image/jpeg"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Preview stills for the Live Now cards: a recent picture of each person's live
// stream, shared with everybody who has the panel open.
//
// Broadcast Box has no thumbnails, and this server cannot decode video, so a
// still can only come from a browser that is already receiving the stream. The
// plugin's part is to decide WHICH browser takes it and to hand the result to
// everybody else, so that a stream is captured about once every minute and a
// half for the whole server rather than once per open panel.
//
// That decision is a LEASE. A browser asks for one on a stream; it gets one only
// when that stream's still has gone stale and nobody else is holding a lease on
// it, and only the browser that holds the lease may upload. When the answer is
// "not now" it says when to ask again, so an idle panel asks rarely rather than
// on every tick.
//
// An attempt counts as much as a picture. A capture that fails — a black
// frame, a codec the browser cannot decode, a browser closed mid-peek — sends
// nothing back, and the lease simply runs out. If that left no trace, a stream
// no browser can capture would be handed a new lease every time the last one
// ran out, which for a peek is a session on the origin every twenty seconds for
// as long as the picture stays black. So a lease holds the stream back just as
// a new still would, and each further one in a row that comes to nothing makes
// the next peek wait twice as long, up to ten minutes.
//
// Two ways to take a still, and the cheap one is preferred:
//
//   - watching: a frame from a tile the viewer is already playing, which costs
//     nothing at all.
//   - peek: a short hidden WHEP session opened only for one picture. That is a
//     real session on the origin, so a peeking browser only gets a lease once a
//     still is a minute past stale — a head start for anybody who could take it
//     for free — and the session is left out of the stream's viewer count.
//
// Everything lives in memory on purpose. A still is a few dozen kilobytes, it is
// out of date within two minutes, and it is worthless after a restart: storing
// it in the KV store would be a database write per stream per minute and a half
// for something nobody would miss. The cost is on a cluster, where each node
// keeps its own: a lease is only good on the node that gave it, and a panel
// sees the stills taken through the node serving it. Nothing breaks; a card
// simply goes without a picture for longer.
//
// 24/7 channels are never previewed. They have a card picture an admin chose
// instead (see channelpics.go), and a round-the-clock channel would otherwise
// cost a peek session every two and a half minutes, for ever.

const (
	// A still is retaken once it is this old. Matches the lab's mock in
	// webapp/lab/mocks/client.ts, which is how the panel is looked at.
	stillStaleAfter = 90 * time.Second

	// How much longer a peek has to wait than a frame from a playing tile. A
	// browser that is watching the stream can refresh the still for nothing,
	// so it is given the first minute to do so before anybody opens a session
	// on the origin for the purpose.
	stillWatcherHeadStart = 60 * time.Second

	// How long a lease lasts. Long enough for a peek — negotiation, up to twelve
	// seconds for the first picture, and the upload — and short enough that a
	// browser that went away mid-capture blocks nobody for long.
	stillLeaseTTL = 20 * time.Second

	// Bounds on "ask again in". Never sooner than five seconds, so a refusal
	// cannot turn into a busy loop, and never later than five minutes, so a
	// bad answer cannot silence a panel for good.
	stillRetryMin = 5 * time.Second
	stillRetryMax = 300 * time.Second

	// The longest a peek is held back on a stream whose captures keep coming
	// to nothing. The wait doubles with each lease in a row that ran out
	// unused, from the usual two and a half minutes, up to this; a still that
	// does arrive puts it straight back. Ten minutes is the oldest still worth
	// showing, so waiting any longer would only mean a card with no picture
	// for longer after the stream came good.
	stillMissWaitMax = 10 * time.Minute

	// How long a still outlives its stream going off the air. Enough to ride
	// out an OBS reconnect, which drops the stream from the listing for a poll
	// or two, without keeping a picture of something that has ended.
	stillOffAirGrace = 2 * time.Minute

	// The oldest still that is shown at all. With nobody's panel open nobody
	// takes a new one, and a card showing a picture from an hour ago is worse
	// than a card showing none.
	stillMaxAge = 10 * time.Minute

	// How long a peek's Broadcast Box session is left out of the viewer count.
	// A peek lasts at most twelve seconds and is closed by the browser; this
	// outlasts that, and outlasts ICE giving up on a browser that vanished
	// without closing — about half a minute for pion to call it disconnected
	// and then failed — with room for the poll that reads the count. It is
	// also a limit: a peek lease cannot be used to hide a real viewer from the
	// count for longer than this.
	peekSessionMemory = 60 * time.Second

	// And how often one person may be left out of one stream's count. A panel
	// is given a peek on a stream at most once per stale still, so it never
	// needs two inside this; somebody who keeps taking peek leases to watch
	// is counted from the second one on, and a peek lease hides nobody for
	// more than a minute in every two and a half.
	peekRepeatWindow = stillStaleAfter + stillWatcherHeadStart

	// The still the panel sends is 480 × 270 at JPEG quality 0.72, which comes
	// to twenty or thirty kilobytes. This leaves room for a busy picture and
	// none for anything else.
	maxStillBytes = 200 << 10

	// Everything held at once. Far above any real roster; there so that the
	// store's size is a number somebody chose.
	maxStillStoreBytes = 32 << 20
	maxStillEntries    = maxStreams
	maxPeekSessions    = 1024

	// Leases one person may hold at once. The panel takes one still at a time,
	// so this allows a second tab. Without it one browser could keep every
	// stream leased, and nobody else would ever take a still.
	maxLeasesPerUser = 2

	// The panel asks about one stream per tick, every few seconds, and uploads
	// at most once per stream per minute and a half. These allow a few tabs.
	stillClaimsPerMinute  = 60
	stillUploadsPerMinute = 20

	// What a still may look like: no smaller than is worth showing and no
	// larger than a card could ever use.
	minStillWidth  = 160
	minStillHeight = 90
	maxStillWidth  = 1280
	maxStillHeight = 720

	// And its shape, as width over height: from 6:5 to 5:2, kept as the two
	// sides of each fraction so the check is exact in whole numbers. The panel
	// sends 16:9, cropped from the middle the way the card crops it, but the
	// card covers whatever it is given, so a panel that kept a camera's 4:3 or
	// an ultrawide 21:9 is not refused for it. What is refused is a picture no
	// card crop could make sense of: a square, a portrait, a strip.
	minStillAspectWide, minStillAspectHigh = 6, 5
	maxStillAspectWide, maxStillAspectHigh = 5, 2

	stillModeWatching = "watching"
	stillModePeek     = "peek"
)

// stillPicture is one captured still. Never changed once made: a newer still
// replaces the whole value, so the bytes can be handed to a response without a
// copy while the lock is not held.
type stillPicture struct {
	data    []byte
	version string
	at      time.Time

	// Who took it. The server cannot tell a frame of the stream from any other
	// JPEG, so the lease is the only check on what goes on somebody's card;
	// this is what makes a picture that should not have been there traceable
	// to an account.
	by string
}

// stillLease is one browser's permission to capture one stream, once.
type stillLease struct {
	token   string
	userID  string
	mode    string
	expires time.Time

	// A peek lease may hide ONE session from the viewer count. Set when that
	// session is asked for, so the same lease cannot be replayed to hide more.
	// It is also what lets a still be uploaded under a peek lease at all: see
	// leaseCanUpload.
	peekUsed bool
}

// stillEntry is everything held about one stream, keyed by its handle. The
// handle rather than the key: nothing here needs the key, and a map of stream
// keys is one more place a credential could leak from.
type stillEntry struct {
	picture *stillPicture
	lease   *stillLease

	// When the stream was last seen on the air, for the off-air grace.
	lastLive time.Time

	// When the last lease was given out that has not brought a still back,
	// and how many leases in a row have run out without one. Both go back to
	// nothing when a still arrives. Between them they are what holds the next
	// lease back after a capture that failed; see the note at the top.
	attempted time.Time
	misses    int
}

// stillStore is the shared state behind the stills. Made on first use, so a
// Plugin built directly in a test has one without anybody setting it up.
type stillStore struct {
	lock    sync.Mutex
	entries map[string]*stillEntry
	bytes   int

	// Broadcast Box session ids that are peeks, and when each stops being one.
	peeks map[string]time.Time

	// Who has recently had a peek left out of which stream's count, keyed by
	// peekerKey, and when they may have another. See peekRepeatWindow.
	recentPeeks map[string]time.Time

	// Streams whose last still was logged at Info, by handle, and when the
	// next one may be. See handleStillUpload.
	announced map[string]time.Time

	claimLimiter  *rateLimiter
	uploadLimiter *rateLimiter

	// Who an admin has blocked from sharing stills, by user id: the list in
	// the KV store, read on first use and again whenever an admin lists or
	// changes it through this node, kept here in between, and nil until it
	// has been read. Under lock with the leases, so that a block takes
	// effect on claims in the same moment its stills come down. See
	// stills_admin.go.
	blocked map[string]stillBlock

	// Held from reading the block list to putting it in force, across any
	// change and write in between, so that the list in force here is the one
	// this node read or wrote last. Two changes through this node take turns;
	// two through different nodes are kept apart by the compare-and-set in
	// changeStillBlocks, which this lock cannot reach. Taken before lock and
	// never while holding it.
	blockLock sync.Mutex

	// When reading the block list last failed, under blockLock. See
	// stillBlocksRetry.
	blocksFailedAt time.Time
}

// stillClaim is the answer to a request for a lease: a lease and how long it
// lasts, or no lease and when to ask again. The lease is always present, empty
// when refused, so the panel has one field to look at.
type stillClaim struct {
	Lease      string `json:"lease"`
	TTL        int    `json:"ttl,omitempty"`
	RetryAfter int    `json:"retryAfter,omitempty"`
}

func newStillStore() *stillStore {
	return &stillStore{
		entries:       map[string]*stillEntry{},
		peeks:         map[string]time.Time{},
		recentPeeks:   map[string]time.Time{},
		announced:     map[string]time.Time{},
		claimLimiter:  newRateLimiter(stillClaimsPerMinute, time.Minute),
		uploadLimiter: newRateLimiter(stillUploadsPerMinute, time.Minute),
	}
}

func (p *Plugin) stills() *stillStore {
	p.stillsOnce.Do(func() {
		p.stillsState = newStillStore()
	})

	return p.stillsState
}

// retryAfter turns a wait into whole seconds for the panel, rounded up — a
// panel that asks a moment early is only refused again — and held inside the
// bounds.
func retryAfter(wait time.Duration) int {
	if wait < stillRetryMin {
		wait = stillRetryMin
	}
	if wait > stillRetryMax {
		wait = stillRetryMax
	}

	return int((wait + time.Second - 1) / time.Second)
}

func notNow(wait time.Duration) stillClaim {
	return stillClaim{RetryAfter: retryAfter(wait)}
}

// newLeaseToken is sixteen bytes from crypto/rand, in hex. Unguessable is the
// whole requirement: it is all that stands between a signed-in user and the
// picture on somebody else's card. The error is not checked because there is
// none to check: since Go 1.24 crypto/rand stops the process rather than hand
// back fewer random bytes than were asked for.
func newLeaseToken() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)

	return hex.EncodeToString(raw)
}

// leaseMatches reports whether token is userID's unexpired lease. Compared in
// constant time: the token is the only thing standing between anybody signed in
// and replacing the picture on somebody else's card.
func leaseMatches(lease *stillLease, userID, token string, now time.Time) bool {
	return lease != nil &&
		token != "" &&
		now.Before(lease.expires) &&
		lease.userID == userID &&
		hmac.Equal([]byte(lease.token), []byte(token))
}

// leaseCanUpload reports whether token is userID's unexpired lease and a still
// may be uploaded under it. A peek lease needs its peek first. The still is
// meant to come out of that session, so an upload under a peek lease that never
// opened one is a picture from somewhere else, and refusing it ties every
// peeked still to a WHEP exchange made by the same account.
func leaseCanUpload(lease *stillLease, userID, token string, now time.Time) bool {
	return leaseMatches(lease, userID, token, now) && (lease.mode != stillModePeek || lease.peekUsed)
}

// expireLease forgets entry's lease once it has run out, and counts it as a
// miss. A lease that ran out is always one no still came back under, because
// put spends a lease the moment one does.
func (e *stillEntry) expireLease(now time.Time) {
	if e.lease != nil && !now.Before(e.lease.expires) {
		e.lease = nil
		e.misses++
	}
}

// stillWait is how long after the last still, or the last attempt that brought
// none back, a lease in mode may be given out.
//
// A peek waits out the head start as well, and twice as long again for each
// further lease in a row that ran out unused, because any of those may have been
// a session on the origin for no picture. A watching browser keeps the plain
// interval whatever has failed before: its frame costs nothing, and when a
// black stream comes good, the first browser playing it should be the one to
// put it on the card.
func stillWait(mode string, misses int) time.Duration {
	if mode != stillModePeek {
		return stillStaleAfter
	}

	wait := stillStaleAfter + stillWatcherHeadStart
	for failed := 1; failed < misses && wait < stillMissWaitMax; failed++ {
		wait *= 2
	}

	return min(wait, stillMissWaitMax)
}

// claim decides whether userID's browser should capture handle now, by mode.
// The caller has already established that the stream is live and may be
// previewed.
func (s *stillStore) claim(handle, userID, mode string, now time.Time) stillClaim {
	s.lock.Lock()
	defer s.lock.Unlock()

	// The handler has already answered a blocked person with a 403. This is
	// for a block that lands between that check and this lock: the block has
	// just taken their leases away, and must not be handed a new one after.
	if _, blocked := s.blocked[userID]; blocked {
		return notNow(stillRetryMax)
	}

	entry := s.entries[handle]
	if entry != nil {
		entry.expireLease(now)
	}

	// One capture of a stream at a time, whoever asks. This is the whole
	// point: every open panel asks, and exactly one of them is told yes.
	if entry != nil && entry.lease != nil {
		return notNow(entry.lease.expires.Sub(now))
	}

	// Stale from whichever is later, the last still or the last attempt that
	// brought none back, so a capture that failed costs the stream the same
	// wait as one that worked. A stream with neither has nothing to be stale
	// against, so the first still is taken by whoever asks first, in either
	// mode. The head start is for keeping a picture fresh, not for delaying
	// the first.
	if entry != nil {
		last := entry.attempted
		if entry.picture != nil && entry.picture.at.After(last) {
			last = entry.picture.at
		}

		if !last.IsZero() {
			wait := stillWait(mode, entry.misses)
			if age := now.Sub(last); age < wait {
				return notNow(wait - age)
			}
		}
	}

	held, soonest := 0, time.Time{}
	for _, other := range s.entries {
		if other.lease != nil && other.lease.userID == userID && now.Before(other.lease.expires) {
			held++
			if soonest.IsZero() || other.lease.expires.Before(soonest) {
				soonest = other.lease.expires
			}
		}
	}
	if held >= maxLeasesPerUser {
		return notNow(soonest.Sub(now))
	}

	if entry == nil {
		if len(s.entries) >= maxStillEntries {
			return notNow(stillStaleAfter)
		}

		entry = &stillEntry{}
		s.entries[handle] = entry
	}

	token := newLeaseToken()
	entry.lease = &stillLease{token: token, userID: userID, mode: mode, expires: now.Add(stillLeaseTTL)}
	entry.attempted = now
	entry.lastLive = now

	return stillClaim{Lease: token, TTL: int(stillLeaseTTL / time.Second)}
}

// holds reports whether token is userID's live lease on handle, and one a still
// may be uploaded under.
func (s *stillStore) holds(handle, userID, token string, now time.Time) bool {
	s.lock.Lock()
	defer s.lock.Unlock()

	entry := s.entries[handle]

	return entry != nil && leaseCanUpload(entry.lease, userID, token, now)
}

// put keeps a still taken under a lease, and spends the lease. The lease is
// checked again here rather than trusted from before the upload was read: it
// can run out while a slow upload arrives, and by then somebody else may hold
// the next one.
func (s *stillStore) put(handle, userID, token string, data []byte, now time.Time) (*stillPicture, bool) {
	s.lock.Lock()
	defer s.lock.Unlock()

	entry := s.entries[handle]
	if entry == nil || !leaseCanUpload(entry.lease, userID, token, now) {
		return nil, false
	}

	if entry.picture != nil {
		s.bytes -= len(entry.picture.data)
	}

	picture := &stillPicture{data: data, version: pictureVersion(data), at: now, by: userID}
	entry.picture = picture
	entry.lease = nil
	entry.lastLive = now
	s.bytes += len(data)

	// The capture worked, so nothing is held against the stream any more:
	// the next lease waits on this still alone, and a peek goes back to the
	// usual interval.
	entry.attempted = time.Time{}
	entry.misses = 0

	// Over the limit: the oldest pictures go first, and never the one that
	// has just arrived.
	for s.bytes > maxStillStoreBytes {
		oldest := ""
		for other, candidate := range s.entries {
			if other == handle || candidate.picture == nil {
				continue
			}
			if oldest == "" || candidate.picture.at.Before(s.entries[oldest].picture.at) {
				oldest = other
			}
		}
		if oldest == "" {
			break
		}

		s.bytes -= len(s.entries[oldest].picture.data)
		s.entries[oldest].picture = nil
	}

	return picture, true
}

// current is the still to serve for handle, if there is one worth showing.
// live says whether the stream is in the listing the panels have right now.
//
// The off-air grace is enforced here as well as in sweep, because sweep only
// runs after a poll that reached Broadcast Box. While the origin is down, or
// the plugin is not configured, nothing sweeps, and a still would otherwise be
// served for its whole age after its stream had left every panel.
//
// But only for a stream that HAS left the listing. lastLive is refreshed once
// a poll, and the poll interval may be set longer than the grace: measured
// against lastLive alone, a stream that is on every card would lose its
// picture between polls. A listed stream is bounded by the age alone.
func (s *stillStore) current(handle string, live bool, now time.Time) *stillPicture {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.entries[handle].showing(live, now)
}

// showing is the still to serve from e, by the rules in current, or nil. One
// place for them, because the admin list has to show exactly what is being
// served. Nil-safe, for a stream with no entry; the caller holds the lock.
func (e *stillEntry) showing(live bool, now time.Time) *stillPicture {
	if e == nil || e.picture == nil || now.Sub(e.picture.at) > stillMaxAge {
		return nil
	}

	if !live && now.Sub(e.lastLive) > stillOffAirGrace {
		return nil
	}

	return e.picture
}

// seen notes that handle is on the air, as of this poll, and returns its still
// for the snapshot. A still past its age is dropped here rather than in sweep,
// so the snapshot can never name a version that the next request for it would
// not find.
func (s *stillStore) seen(handle string, now time.Time) *stillPicture {
	s.lock.Lock()
	defer s.lock.Unlock()

	entry := s.entries[handle]
	if entry == nil {
		return nil
	}

	entry.lastLive = now

	if entry.picture != nil && now.Sub(entry.picture.at) > stillMaxAge {
		s.bytes -= len(entry.picture.data)
		entry.picture = nil
	}

	return entry.picture
}

// sweep forgets what is no longer wanted: pictures of streams that have been
// off the air past the grace or are too old to show, leases that have run out,
// peeks that have long since closed, and the marks that space out peeks and
// log lines once they have passed. With the setting off, every picture and
// lease goes at once.
func (s *stillStore) sweep(now time.Time, enabled bool) {
	s.lock.Lock()
	defer s.lock.Unlock()

	for handle, entry := range s.entries {
		entry.expireLease(now)

		if entry.picture != nil && (!enabled || now.Sub(entry.picture.at) > stillMaxAge) {
			s.bytes -= len(entry.picture.data)
			entry.picture = nil
		}

		// An entry with nothing in it is forgotten, but a stream whose last
		// capture came to nothing is not nothing: that is what holds its next
		// lease back, and forgetting it would hand one out at once and start
		// the doubling again from the bottom. It is kept while the stream is
		// on the air, and goes with the off-air grace like everything else.
		empty := entry.picture == nil && entry.lease == nil && entry.misses == 0

		if !enabled || now.Sub(entry.lastLive) > stillOffAirGrace || empty {
			if entry.picture != nil {
				s.bytes -= len(entry.picture.data)
			}
			delete(s.entries, handle)
		}
	}

	forgetPassed(s.peeks, now)
	forgetPassed(s.recentPeeks, now)
	forgetPassed(s.announced, now)
}

// forgetPassed deletes every deadline in deadlines that has passed.
func forgetPassed(deadlines map[string]time.Time, now time.Time) {
	for key, until := range deadlines {
		if !now.Before(until) {
			delete(deadlines, key)
		}
	}
}

// makeRoom keeps deadlines under limit before one more is added: whatever has
// passed goes first, and if that was not enough, the one nearest to passing.
func makeRoom(deadlines map[string]time.Time, limit int, now time.Time) {
	if len(deadlines) < limit {
		return
	}

	forgetPassed(deadlines, now)
	if len(deadlines) < limit {
		return
	}

	soonest, soonestAt := "", time.Time{}
	for key, until := range deadlines {
		if soonest == "" || until.Before(soonestAt) {
			soonest, soonestAt = key, until
		}
	}
	delete(deadlines, soonest)
}

// peekerKey is what recentPeeks is keyed by: one person on one stream. User ids
// and handles are both letters and digits, so with a NUL between them no two
// pairs can come out as the same key.
func peekerKey(userID, handle string) string {
	return userID + "\x00" + handle
}

// takePeek reports whether the session about to be opened under token should
// be left out of the viewer count, and uses the lease's peek up either way.
//
// Only an unused peek lease of userID's own on handle counts: a browser that
// is WATCHING the stream is a viewer, and a lease for a frame from its tile
// must not take it off the count. Nor does one for somebody who has just had a
// peek left out on the same stream (see peekRepeatWindow); their session is
// counted, but the lease is still spent on it, so the still they came for can
// be uploaded under it as usual.
func (s *stillStore) takePeek(handle, userID, token string, now time.Time) bool {
	s.lock.Lock()
	defer s.lock.Unlock()

	entry := s.entries[handle]
	if entry == nil || !leaseMatches(entry.lease, userID, token, now) {
		return false
	}
	if entry.lease.mode != stillModePeek || entry.lease.peekUsed {
		return false
	}

	entry.lease.peekUsed = true

	if until, recent := s.recentPeeks[peekerKey(userID, handle)]; recent && now.Before(until) {
		return false
	}

	return true
}

// rememberPeek records a Broadcast Box session as userID's peek on handle, so
// the viewer count leaves it out, and so that person's next peek on that stream
// inside peekRepeatWindow does not.
//
// Both lists are bounded. When one is still full of marks that have not
// passed, the one nearest to passing makes room: counting one peek as a viewer
// for a moment is the whole cost of that, which is better than growing
// without bound.
func (s *stillStore) rememberPeek(handle, userID, sessionID string, now time.Time) {
	if sessionID == "" {
		return
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	makeRoom(s.peeks, maxPeekSessions, now)
	s.peeks[sessionID] = now.Add(peekSessionMemory)

	makeRoom(s.recentPeeks, maxPeekSessions, now)
	s.recentPeeks[peekerKey(userID, handle)] = now.Add(peekRepeatWindow)
}

// announce reports whether a still just kept for handle should be logged at
// Info, and if so notes that it has been.
func (s *stillStore) announce(handle string, now time.Time) bool {
	s.lock.Lock()
	defer s.lock.Unlock()

	if until, recent := s.announced[handle]; recent && now.Before(until) {
		return false
	}

	makeRoom(s.announced, maxStillEntries, now)
	s.announced[handle] = now.Add(stillStaleAfter)

	return true
}

// viewers counts the people watching: every Broadcast Box session except the
// peeks. Without this, every still taken by a peek would show one more viewer
// for a moment and then one fewer, which is two pushes of the snapshot to every
// connected client, and a count that says somebody is watching who is not.
func (s *stillStore) viewers(sessions []bbSession, now time.Time) int {
	return len(s.counted(sessions, now))
}

// counted is the sessions viewers counts: every one that is not a peek. The
// poller takes the list rather than the number, so that the faces on a card
// are drawn from exactly the sessions its count is made of.
func (s *stillStore) counted(sessions []bbSession, now time.Time) []bbSession {
	s.lock.Lock()
	defer s.lock.Unlock()

	counted := make([]bbSession, 0, len(sessions))
	for _, session := range sessions {
		if until, peek := s.peeks[session.ID]; peek && now.Before(until) {
			continue
		}
		counted = append(counted, session)
	}

	return counted
}

// previewable reports whether handle is a live stream that may have a still:
// on the air right now, on Broadcast Box, and not a 24/7 channel. A Twitch
// stream has its own picture, relayed from Twitch (twitch_preview.go), and
// nobody's browser may share a frame of one.
func (p *Plugin) previewable(handle string) bool {
	if handle == "" {
		return false
	}

	for _, stream := range p.getSnapshot().Streams {
		if hmac.Equal([]byte(stream.Handle), []byte(handle)) {
			return !stream.AlwaysOn && stream.Source == ""
		}
	}

	return false
}

// handleStillClaim answers POST /api/v1/stills/claim?handle=…&mode=….
//
// Switched off is a 404, as it is for every other stills route: with the
// setting off there is nothing here to claim, rather than something the
// caller is not allowed.
func (p *Plugin) handleStillClaim(w http.ResponseWriter, r *http.Request, userID string) {
	if !p.getConfiguration().LivePreviews {
		writeError(w, http.StatusNotFound, "Live previews are off.")
		return
	}

	handle := strings.TrimSpace(r.URL.Query().Get("handle"))
	if handle == "" {
		writeError(w, http.StatusBadRequest, "Missing stream handle.")
		return
	}

	mode := strings.TrimSpace(r.URL.Query().Get("mode"))
	if mode != stillModeWatching && mode != stillModePeek {
		writeError(w, http.StatusBadRequest, "Say whether the still comes from a stream being watched or from a peek.")
		return
	}

	if !p.previewable(handle) {
		writeError(w, http.StatusNotFound, "That stream is not live right now.")
		return
	}

	if !p.mayShareStills(w, userID) {
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, p.stills().claim(handle, userID, mode, time.Now()))
}

// stillUploaded is the answer to a still that was kept: its version and when
// it was taken, the same two things the next snapshot names it by.
type stillUploaded struct {
	Version string `json:"version"`
	At      string `json:"at"`
}

// handleStillUpload answers POST /api/v1/stills?handle=…&lease=…, whose body is
// the still as raw JPEG bytes.
func (p *Plugin) handleStillUpload(w http.ResponseWriter, r *http.Request, userID string) {
	if !p.getConfiguration().LivePreviews {
		writeError(w, http.StatusNotFound, "Live previews are off.")
		return
	}

	handle := strings.TrimSpace(r.URL.Query().Get("handle"))
	token := strings.TrimSpace(r.URL.Query().Get("lease"))

	if handle == "" {
		writeError(w, http.StatusBadRequest, "Missing stream handle.")
		return
	}

	if !p.previewable(handle) {
		writeError(w, http.StatusNotFound, "That stream is not live right now.")
		return
	}

	// Before the lease is looked at. Blocking somebody takes their leases
	// away as well, so an upload already on its way when the block landed is
	// refused by put all the same.
	if !p.mayShareStills(w, userID) {
		return
	}

	store := p.stills()

	// Checked before the body is read, so an upload nobody asked for costs
	// nothing, and again when it is stored.
	if !store.holds(handle, userID, token, time.Now()) {
		writeError(w, http.StatusForbidden, "That capture was not yours to make.")
		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, maxStillBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Could not read the still.")
		return
	}

	if len(data) > maxStillBytes {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("That still is too large; %d KB is the limit.", maxStillBytes>>10))

		return
	}

	if status, problem := checkStill(data); problem != "" {
		writeError(w, status, problem)
		return
	}

	still, stored := store.put(handle, userID, token, data, time.Now())
	if !stored {
		writeError(w, http.StatusForbidden, "That capture was not yours to make.")
		return
	}

	// Who put which picture on whose card, so one that should not be there
	// can be traced to an account. At Info, because by default Mattermost's
	// log file keeps nothing below it, and that file is what the System
	// Console's Server Logs page shows. A stream is recaptured once a minute
	// and a half, so that is one line per stream per minute and a half; an
	// upload inside that, which only happens when the stream's picture was
	// dropped early, goes to Debug rather than adding to it. By handle: never
	// the key.
	log := p.API.LogDebug
	if store.announce(handle, time.Now()) {
		log = p.API.LogInfo
	}
	log("A preview still was shared", "user_id", userID, "stream_handle", handle, "version", still.version)

	// No refreshSoon: every panel learns the new version from the next
	// scheduled poll, a few seconds away. Asking for one here would hand any
	// lease holder a way to make the server poll the origin and push the
	// snapshot to every connected client, for a picture nobody is waiting on.
	writeJSON(w, http.StatusOK, stillUploaded{Version: still.version, At: still.at.UTC().Format(time.RFC3339)})
}

// checkStill decides whether an upload is a still, from its bytes: a JPEG — not
// whatever the request's Content-Type claimed — of a size and shape a card can
// use. It says what is wrong, or nothing.
//
// Only the header is read, never the picture. The header is what gives the
// size and shape, and decoding the rest would cost every upload megabytes of
// buffers for nothing the card needs. What that lets through is a JPEG header
// followed by something else, which the server stores as bytes and serves only
// as image/jpeg, with nosniff and a sandbox policy (see writeImage): at worst a
// card shows a broken picture, and never runs anything. The lease already says
// who may send one, and the log says who did.
func checkStill(data []byte) (int, string) {
	if http.DetectContentType(data) != "image/jpeg" {
		return http.StatusBadRequest, "That is not a JPEG."
	}

	shape, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return http.StatusBadRequest, "That is not a JPEG."
	}

	width, height := shape.Width, shape.Height
	tooNarrow := width*minStillAspectHigh < height*minStillAspectWide
	tooWide := width*maxStillAspectHigh > height*maxStillAspectWide

	if width < minStillWidth || height < minStillHeight || width > maxStillWidth || height > maxStillHeight ||
		tooNarrow || tooWide {
		return http.StatusBadRequest, "That picture is the wrong shape for a preview."
	}

	return 0, ""
}

// handleStill answers GET /api/v1/stills?handle=…&v=…, behind the access gate:
// for people who can watch, and for system admins, whose preview pictures card
// in the System Console shows these without a token.
//
// The stream need not be live: a card that is fading out of the list keeps its
// picture through the off-air grace. See current.
func (p *Plugin) handleStill(w http.ResponseWriter, r *http.Request) {
	if !p.getConfiguration().LivePreviews {
		writeError(w, http.StatusNotFound, "Not found.")
		return
	}

	handle := strings.TrimSpace(r.URL.Query().Get("handle"))
	still := p.stills().current(handle, p.previewable(handle), time.Now())
	if still == nil {
		writeError(w, http.StatusNotFound, "Not found.")
		return
	}

	// Revalidated every time, whatever version was asked for. A picture kept
	// for a day would be shown again from the browser's cache without the
	// gate ever hearing of it: after a let-out, an unlink, or somebody else
	// signing in on the same browser. The ETag makes a re-display a cheap
	// 304 for whoever may still see it, and the version stays in the address,
	// so a new still still gets a new one.
	writeImage(w, r, "image/jpeg", still.data, "private, no-cache")
}
