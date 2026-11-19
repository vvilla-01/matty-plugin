package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Who may use Clips. Looking — the list, a clip, its video and picture, the
// cards of shared clips — is for people who have linked their own stream with
// their bearer token, and for people a system admin has let in to watch
// (design/lock-screen.md §3.6). Everything else — clipping, publishing,
// upvoting, reporting, sharing, deleting, the streamer's switch — is for
// linked people only. A username that happens to match a stream key is not a
// link. The panel hides what somebody may not do, but that is a courtesy: the
// rule is kept here, on every route, so a browser console gets no further
// than the panel does.

// clipsTagDomain keeps a stream tag from ever equalling a stream's public
// handle. Both are an HMAC under the same secret, and the handle is taken over
// the key exactly as Broadcast Box relays it, which for the usual lower-case
// key is the same bytes normalizeKey gives. Without something in front, the
// tag of most streams would be broadcast to every browser in every snapshot.
const clipsTagDomain = "villanet-clips-tag\x00"

// clipWatchRoutes are the clip routes a person a system admin let in may use:
// looking, never acting. Every other clip route needs a link.
var clipWatchRoutes = map[string]map[string]bool{
	clipsPath:            {http.MethodGet: true},
	clipsPath + "/clip":  {http.MethodGet: true},
	clipsPath + "/cards": {http.MethodGet: true},
	clipsPath + "/video": {http.MethodGet: true, http.MethodHead: true},
	clipsPath + "/thumb": {http.MethodGet: true},
}

// requireClipsAccess is the gate in front of every clip route a person uses,
// and it returns the caller's stream key for the handler behind it: their
// key when they are linked, and "" only for somebody let in, on a route
// watchOK says they may use. So a handler that acts never sees an empty key.
// With seen, a yes is good for ten seconds (accessSeenOf), for the routes one
// playback or one channel of cards calls many times.
//
// It runs before anything else the request asks for is looked at: before the
// body is read, an id checked, a clip looked up or a limiter spent. So
// somebody refused gets the same answer for a clip that exists, one that has
// gone, one that never did and an id that is nonsense, and finds out nothing
// by asking; and a refusal never costs them a request they might have made
// once unlocked. A refusal is remembered for two seconds, as the front gate's
// are, so that asking again and again does not make the store work either.
//
// A store that will not answer is reported as that, never as "locked":
// telling a linked person to unlock again over a database blip would send them
// off to paste their token for nothing.
//
// What a fresh read found about the link is also the freshest word there is on
// whether the caller counts for Best Clipper, so the eligibility cache takes
// it: the link, never the access, so somebody let in counts for nothing, as
// somebody unlinked never did.
func (p *Plugin) requireClipsAccess(w http.ResponseWriter, userID string, watchOK, seen bool) (string, bool) {
	c := p.accessCache()
	s := p.clips()
	now := c.now()

	class := routeLinked
	if watchOK {
		class = routeWatch
	}

	if refusal, recent := c.refusedRecently(userID, now); recent {
		if level, refused := refusedFromMemory(class, refusal); refused {
			p.writeClipError(w, http.StatusForbidden, clipRefusalCode(level))
			return "", false
		}
	}

	epoch := c.currentEpoch()
	linkEpoch := s.currentLinkEpoch()

	level, key, fresh, err := p.accessRead(userID, seen)
	if err != nil {
		p.API.LogWarn("Could not read somebody's access to Matty", "error", err.Error())
		p.writeClipError(w, http.StatusServiceUnavailable, clipErrStore)

		return "", false
	}

	if fresh {
		s.noteLinked(userID, level == accessLinked, s.now(), linkEpoch)
	}

	switch {
	case level == accessLinked:
		return key, true
	case level == accessLetIn && watchOK:
		return "", true
	}

	c.noteRefusal(userID, level, false, now, epoch)
	p.writeClipError(w, http.StatusForbidden, clipRefusalCode(level))

	return "", false
}

// clipRefusalCode is the clip family's word for a refusal at this level.
func clipRefusalCode(level access) clipErrorCode {
	if level == accessLetIn {
		return clipErrWatchOnly
	}

	return clipErrLocked
}

// streamTag names a stream for the server's own bookkeeping — whose stream a
// clip was cut from, which streams their streamers switched off — without
// keeping the key. Unlike the handle it is taken over the normalised key, so
// it follows the link however the key's case was typed, and it is never sent
// to a browser.
//
// Empty when there is no secret to take it with, or no key to take it of, and
// an empty tag must match nothing: comparing two of them would make everybody
// the streamer of every stream nobody could name.
func (p *Plugin) streamTag(streamKey string) string {
	normalized := normalizeKey(streamKey)
	if len(p.handleSecret) == 0 || normalized == "" {
		return ""
	}

	mac := hmac.New(sha256.New, p.handleSecret)
	mac.Write([]byte(clipsTagDomain + normalized))

	return hex.EncodeToString(mac.Sum(nil))[:32]
}

const (
	// How long a lookup of somebody — their account, and whether they are
	// linked — is taken as the answer before it is asked again.
	clipPeopleTTL = 10 * time.Minute

	// How long somebody whose lookup failed is left before they are asked
	// again, so that a user store that is down is not asked about every
	// clipper and voter on every vote.
	clipLookUpRetry = time.Minute

	// Bounds on the caches of people, here and in access.go. Far above the people a server like this has;
	// they exist so that a bound is something somebody chose.
	maxClipPeople = 5000
)

// clipFacts is what the eligibility cache knows of one person: what their
// account said and whether they were linked, each as of the last lookup that
// answered. A lookup that fails changes neither, so what was last known
// stands until the store answers again.
type clipFacts struct {
	userKnown   bool
	gone        bool // the account no longer exists: Mattermost said 404
	deactivated bool
	bot         bool
	username    string
	displayName string
	userAt      time.Time

	linkKnown bool
	linked    bool
	linkAt    time.Time

	failedAt time.Time
}

// verdict is whether somebody counts for Best Clipper — their votes, and
// their own score — so far as their account and their link say, and whether
// that is known at all. Either half can settle it alone: a deactivated
// account counts for nothing whatever its link, and neither does somebody who
// is not linked. Somebody never looked up does not count, and is not known
// not to.
func (f clipFacts) verdict() (eligible, known bool) {
	if f.userKnown && (f.gone || f.deactivated || f.bot) {
		return false, true
	}
	if f.linkKnown && !f.linked {
		return false, true
	}
	if f.userKnown && f.linkKnown {
		return true, true
	}

	return false, false
}

type clipVerdict struct {
	eligible bool
	known    bool
}

// clipVerdicts is who counts, for one look at one document: the cache's
// verdicts taken all at once, so that a write sees one answer for each person
// however long it takes, and the document's own blocks, which settle it
// without any lookup.
type clipVerdicts struct {
	people  map[string]clipVerdict
	blocked map[string]clipMark
}

func (v clipVerdicts) eligible(userID string) bool {
	if _, blocked := v.blocked[userID]; blocked {
		return false
	}

	return v.people[userID].eligible
}

func (v clipVerdicts) known(userID string) bool {
	if _, blocked := v.blocked[userID]; blocked {
		return true
	}

	return v.people[userID].known
}

// verdictsFor takes the cache's verdicts on these people, with the blocks
// the document they are in holds.
func (s *clipsStore) verdictsFor(people []string, blocked map[string]clipMark) clipVerdicts {
	s.peopleLock.Lock()
	defer s.peopleLock.Unlock()

	verdicts := make(map[string]clipVerdict, len(people))
	for _, userID := range people {
		eligible, known := s.people[userID].verdict()
		verdicts[userID] = clipVerdict{eligible: eligible, known: known}
	}

	return clipVerdicts{people: verdicts, blocked: blocked}
}

// factsOf is what the cache knows of somebody, which is nothing for somebody
// never looked up.
func (s *clipsStore) factsOf(userID string) clipFacts {
	s.peopleLock.Lock()
	defer s.peopleLock.Unlock()

	return s.people[userID]
}

// lookUpDue says which halves of somebody's facts need asking for: those not
// known, or older than clipPeopleTTL. Neither, while a failed lookup of them
// is still recent. And the link epoch, which a link answer must still match
// to be kept.
func (s *clipsStore) lookUpDue(userID string, now time.Time, withLink bool) (bool, bool, uint64) {
	s.peopleLock.Lock()
	defer s.peopleLock.Unlock()

	facts := s.people[userID]
	if !facts.failedAt.IsZero() && now.Sub(facts.failedAt) < clipLookUpRetry {
		return false, false, s.linkEpoch
	}

	needUser := !facts.userKnown || now.Sub(facts.userAt) >= clipPeopleTTL
	needLink := withLink && (!facts.linkKnown || now.Sub(facts.linkAt) >= clipPeopleTTL)

	return needUser, needLink, s.linkEpoch
}

// lookUpWanted is whether a lookup of somebody would ask anything now.
func (s *clipsStore) lookUpWanted(userID string, now time.Time) bool {
	needUser, needLink, _ := s.lookUpDue(userID, now, true)

	return needUser || needLink
}

func (s *clipsStore) currentLinkEpoch() uint64 {
	s.peopleLock.Lock()
	defer s.peopleLock.Unlock()

	return s.linkEpoch
}

// clipLookUp is what one lookup of somebody came back with.
type clipLookUp struct {
	user     *model.User
	gone     bool
	userSeen bool

	linked   bool
	linkSeen bool

	failed bool
}

// remember keeps what a lookup found. A link answer read before the latest
// link change is dropped: it may be the link as it was.
func (s *clipsStore) remember(userID string, found clipLookUp, now time.Time, epoch uint64) {
	s.peopleLock.Lock()
	defer s.peopleLock.Unlock()

	facts := s.people[userID]

	if found.userSeen {
		facts.userKnown = true
		facts.gone = found.gone
		facts.deactivated, facts.bot, facts.username, facts.displayName = false, false, "", ""
		if found.user != nil {
			facts.deactivated = found.user.DeleteAt != 0
			facts.bot = found.user.IsBot
			facts.username = found.user.Username
			facts.displayName = displayNameOf(found.user)
		}
		facts.userAt = now
	}

	if found.linkSeen && epoch == s.linkEpoch {
		facts.linkKnown = true
		facts.linked = found.linked
		facts.linkAt = now
	}

	if found.failed {
		facts.failedAt = now
	}

	s.people[userID] = facts
	s.prunePeople(now)
}

// noteLinked is the gate's answer about the person asking, which is as fresh
// as any lookup, kept unless a link changed while it was being read.
func (s *clipsStore) noteLinked(userID string, linked bool, now time.Time, epoch uint64) {
	s.remember(userID, clipLookUp{linked: linked, linkSeen: true}, now, epoch)
}

// prunePeople keeps the cache inside its bound by forgetting whoever it would
// ask about again anyway. Under peopleLock.
func (s *clipsStore) prunePeople(now time.Time) {
	if len(s.people) <= maxClipPeople {
		return
	}

	for userID, facts := range s.people {
		if now.Sub(facts.userAt) >= clipPeopleTTL && now.Sub(facts.linkAt) >= clipPeopleTTL {
			delete(s.people, userID)
		}
	}
}

// lookUpClipPeople makes sure the cache has a recent answer for each of these
// people: their account, and with withLink whether they are linked. Before a
// write, never during one. Somebody already known recently is not asked
// about, and nor is somebody whose last lookup failed a moment ago.
//
// A 404 is an answer — the account has gone, and it counts for nothing —
// where any other failure is the user store not answering, and keeps what
// was known before. Confusing the two would freeze Best Clipper for good on
// an account somebody deleted, or count everybody out over a database blip.
func (p *Plugin) lookUpClipPeople(people []string, now time.Time, withLink bool) {
	s := p.clips()
	asked := map[string]bool{}
	failures := 0
	firstError := ""

	fail := func(err string) {
		failures++
		if firstError == "" {
			firstError = err
		}
	}

	for _, userID := range people {
		if userID == "" || asked[userID] {
			continue
		}
		asked[userID] = true

		needUser, needLink, epoch := s.lookUpDue(userID, now, withLink)
		if !needUser && !needLink {
			continue
		}

		found, errs := p.askClipPerson(userID, needUser, needLink)
		for _, err := range errs {
			fail(err)
		}

		s.remember(userID, found, now, epoch)
	}

	if failures > 0 {
		p.API.LogWarn("Could not look up some of the people behind clips and votes", "people", failures, "error", firstError)
	}
}

// askClipPerson asks Mattermost about somebody: with needUser, their account,
// and with needLink, whether they are linked. It answers what came back and
// what went wrong, for the caller to keep and to log.
func (p *Plugin) askClipPerson(userID string, needUser, needLink bool) (clipLookUp, []string) {
	found := clipLookUp{}
	inactive := false
	var errs []string

	if needUser {
		user, appErr := p.API.GetUser(userID)
		switch {
		case appErr == nil && user != nil:
			found.user, found.userSeen = user, true
			inactive = user.DeleteAt != 0 || user.IsBot
		case appErr != nil && appErr.StatusCode == http.StatusNotFound:
			found.gone, found.userSeen = true, true
			inactive = true
		case appErr != nil:
			found.failed = true
			errs = append(errs, appErr.Error())
		default:
			found.failed = true
			errs = append(errs, "no account came back")
		}
	}

	// Whether somebody who counts for nothing anyway is linked changes
	// nothing, so nobody is asked. Linked as the gate means it: both entries
	// agree (linkOf).
	if needLink && !inactive {
		key, err := p.linkOf(userID)
		if err != nil {
			found.failed = true
			errs = append(errs, err.Error())
		} else {
			found.linked, found.linkSeen = key != "", true
		}
	}

	return found, errs
}

// mayAsk is whether somebody may be asked about now: not while a lookup of
// them that failed is still recent.
func (s *clipsStore) mayAsk(userID string, now time.Time) bool {
	s.peopleLock.Lock()
	defer s.peopleLock.Unlock()

	failedAt := s.people[userID].failedAt

	return failedAt.IsZero() || now.Sub(failedAt) >= clipLookUpRetry
}

// clipNames names people for views from the eligibility cache, so naming
// costs one account lookup per person per ten minutes rather than one per
// clip. For the cards of shared clips, which a channel can hold dozens of
// (A§3.3), and for the snapshot. With lookUp, somebody the cache has nothing
// recent on is looked up; without it, for the snapshot, they are named from
// whatever it has, which may be nothing.
func (p *Plugin) clipNames(now time.Time, lookUp bool) clipNamer {
	s := p.clips()

	return func(userID string) clipPerson {
		if userID != "" && lookUp {
			p.lookUpClipPeople([]string{userID}, now, false)
		}

		return s.nameOf(userID)
	}
}

// clipNamesNow names people as their accounts are at the moment of the
// request, for the panel's list and single clip and for the System Console:
// each person named is asked about once per request, however many clips name
// them, so a rename shows the next time a list is fetched (spec §2.10.3).
// What comes back goes into the cache too.
//
// Somebody whose lookup failed a moment ago is not asked again, and somebody
// whose lookup fails now is named from what was last known, so a user store
// that is down neither turns every name into "Unknown user" nor is asked
// about everybody on every list.
//
// For one request: it is not safe to share between goroutines.
func (p *Plugin) clipNamesNow(now time.Time) clipNamer {
	s := p.clips()
	asked := map[string]bool{}

	return func(userID string) clipPerson {
		if userID != "" && !asked[userID] {
			asked[userID] = true

			if s.mayAsk(userID, now) {
				epoch := s.currentLinkEpoch()
				found, errs := p.askClipPerson(userID, true, false)
				s.remember(userID, found, now, epoch)
				if len(errs) > 0 {
					p.API.LogWarn("Could not look up somebody named on a clip", "user_id", userID, "error", errs[0])
				}
			}
		}

		return s.nameOf(userID)
	}
}

// nameOf is somebody as the cache knows them: "Unknown user" for nobody, for
// somebody never looked up, and for an account that has gone.
func (s *clipsStore) nameOf(userID string) clipPerson {
	if userID == "" {
		return clipPerson{Name: unknownClipPerson}
	}

	facts := s.factsOf(userID)
	if !facts.userKnown || facts.gone || facts.username == "" {
		return clipPerson{UserID: userID, Name: unknownClipPerson}
	}

	name := facts.displayName
	if name == "" {
		name = facts.username
	}

	return clipPerson{UserID: userID, Username: facts.username, DisplayName: facts.displayName, Name: name}
}

// forgetLink drops everything the cache holds on somebody's link, and moves
// the epoch, so that an answer still on its way from before the change is not
// kept either. Their account facts stay: linking changes nothing about those.
// What the access gate remembers is dropped by clipsLinkChanged.
func (s *clipsStore) forgetLink(userID string) {
	s.peopleLock.Lock()
	defer s.peopleLock.Unlock()

	s.linkEpoch++

	if facts, known := s.people[userID]; known {
		facts.linkKnown, facts.linked, facts.linkAt = false, false, time.Time{}
		facts.failedAt = time.Time{}
		s.people[userID] = facts
	}
}

// streamOf is whether a stream tag is the stream behind this key: the test
// for a streamer when their key is already in hand, as it is behind the gate.
// An empty tag is nobody's stream.
func (p *Plugin) streamOf(key, tag string) bool {
	if tag == "" {
		return false
	}

	return hmac.Equal([]byte(p.streamTag(key)), []byte(tag))
}

// streamerOf is whether somebody is, right now, the streamer of the stream a
// tag names: linked, by their own publish token, to that stream's key.
// Exactly one person at a time can be, because proving a key takes it from
// whoever proved it before; and a streamer who unlinks, or whose key somebody
// else proves, stops being one at once. Who the stream was attributed to when
// it was clipped is only ever shown, never trusted.
func (p *Plugin) streamerOf(userID, tag string) (bool, error) {
	key, err := p.linkOf(userID)
	if err != nil {
		return false, errors.New(err.Error())
	}
	if key == "" {
		return false, nil
	}

	return p.streamOf(key, tag), nil
}

// clipsLinkChanged is told whenever somebody's link changes: they linked,
// unlinked, or somebody else proved their key. What the caches knew of their
// link goes at once, the access gate's memory of them included. Then, while Clips is running, Best Clipper is worked out
// again in the background — their votes now count, or no longer do, and they
// may have won or lost it — without keeping the link request waiting. Other
// nodes catch up within clipPeopleTTL.
//
// Before startClips, and after stopClips, it touches nothing but memory.
// Changes that arrive while a recompute is already waiting to start share it.
func (p *Plugin) clipsLinkChanged(userID string) {
	s := p.clips()
	s.forgetLink(userID)
	p.accessForget(userID)

	s.runLock.Lock()
	defer s.runLock.Unlock()

	if !s.running || s.rearmed {
		return
	}
	s.rearmed = true

	s.background.Add(1)
	go func() {
		defer s.background.Done()

		// Cleared before anything is looked up, so a change that lands from
		// here on starts a recompute of its own rather than missing this one.
		s.runLock.Lock()
		s.rearmed = false
		s.runLock.Unlock()

		p.safely("working out Best Clipper again", func() {
			if err := p.recomputeBestClipper(s.now()); err != nil && !errors.Is(err, errClipsHalted) {
				p.API.LogWarn("Could not work out Best Clipper again after a link changed", "error", err.Error())
			}
		})
	}()
}

// The watch gate (spec §2.4.9, A§3.4). Clipping a stream is for somebody
// watching it in VillaNET, or for its streamer: without the rule, back to
// back drafts would let somebody follow a stream minute by minute without
// ever being counted as watching it.
//
// The viewer registry is for that alone. It is kept in memory, apart from the
// watcher store, never shown, logged or returned, and forgotten fifteen
// minutes after a session was last seen, and when the plugin stops. It holds
// every panel session, linked or not: whether somebody is linked is asked
// when they clip, so somebody who links half way through a stream can clip it
// at once rather than being told to tune in again. And whether Clips is on or
// not: a panel that is already playing does not offer again, so a registry
// kept only while Clips is on would refuse everybody watching when an admin
// switches it on, or back on, until they tuned in again. Nothing in it is
// used while Clips is off.

const (
	// The most sessions the registry holds, as the watcher store does. When
	// it is full, the one heard of longest ago makes room.
	maxClipViewerSessions = 4096

	// What a session Broadcast Box gave no id is filed under, beside the
	// person and the stream: only the first minute's rule can ever find it.
	clipViewerNoID = "\x00no-id\x00"
)

// clipViewerSession is one playback session the panel opened.
type clipViewerSession struct {
	userID   string
	handle   string
	openedAt time.Time

	// When a successful poll last found Broadcast Box listing it.
	lastSeen time.Time
}

// heard is when the session was last known to be open.
func (s *clipViewerSession) heard() time.Time {
	if s.lastSeen.After(s.openedAt) {
		return s.lastSeen
	}

	return s.openedAt
}

// clipViewers is the registry. lastPollAt is when the latest successful poll
// was, so that "listed in the latest poll" is a session seen then.
type clipViewers struct {
	lock       sync.Mutex
	sessions   map[string]*clipViewerSession
	lastPollAt time.Time

	// Whether a session with no id has been logged since the start.
	noIDLogged atomic.Bool
}

// note remembers a session handleWHEP has just opened.
func (v *clipViewers) note(sessionID, userID, handle string, now time.Time) {
	if userID == "" || handle == "" {
		return
	}
	if sessionID == "" {
		sessionID = clipViewerNoID + userID + "\x00" + handle
	}

	v.lock.Lock()
	defer v.lock.Unlock()

	if v.sessions == nil {
		v.sessions = map[string]*clipViewerSession{}
	}
	if _, known := v.sessions[sessionID]; !known && len(v.sessions) >= maxClipViewerSessions {
		v.dropStalest()
	}

	v.sessions[sessionID] = &clipViewerSession{userID: userID, handle: handle, openedAt: now}
}

// dropStalest forgets the session heard of longest ago. Under lock.
func (v *clipViewers) dropStalest() {
	stalest, stalestAt := "", time.Time{}
	for id, session := range v.sessions {
		heard := session.heard()
		if stalest == "" || heard.Before(stalestAt) || (heard.Equal(stalestAt) && id < stalest) {
			stalest, stalestAt = id, heard
		}
	}
	delete(v.sessions, stalest)
}

// seen is a successful poll: every remembered session Broadcast Box still
// lists under the stream it was opened on is seen now, and this is the latest
// poll. handleOf is the handle of a listed stream, asked only for a stream
// that lists a session the registry knows.
func (v *clipViewers) seen(raw []bbStream, handleOf func(streamKey string) string, now time.Time) {
	v.lock.Lock()
	defer v.lock.Unlock()

	v.lastPollAt = now
	if len(v.sessions) == 0 {
		return
	}

	for _, source := range raw {
		handle := ""
		for _, listed := range source.Sessions {
			session := v.sessions[listed.ID]
			if session == nil {
				continue
			}
			if handle == "" {
				handle = handleOf(source.StreamKey)
			}
			if session.handle == handle {
				session.lastSeen = now
			}
		}
	}

	v.pruneLocked(now)
}

// watching is the live rule: somebody has a session on the stream that the
// latest successful poll listed, or that they opened in the last minute,
// before Broadcast Box could list it.
func (v *clipViewers) watching(userID, handle string, now time.Time) bool {
	v.lock.Lock()
	defer v.lock.Unlock()

	for _, session := range v.sessions {
		if session.userID != userID || session.handle != handle {
			continue
		}
		if !v.lastPollAt.IsZero() && session.lastSeen.Equal(v.lastPollAt) {
			return true
		}
		if now.Sub(session.openedAt) <= viewerGrace {
			return true
		}
	}

	return false
}

// watchingAt is the tail rule: somebody had a session on the stream that was
// open by the time it ended, at end, and still known to be open at since, a
// little before that. A session opened after the end is somebody tuning in
// to whatever came next on that handle — the stream back with the same key,
// say — who was not watching when it ended; somebody who watched through it
// has the session they had before as well.
func (v *clipViewers) watchingAt(userID, handle string, since, end time.Time) bool {
	v.lock.Lock()
	defer v.lock.Unlock()

	for _, session := range v.sessions {
		if session.userID != userID || session.handle != handle {
			continue
		}
		if !session.openedAt.After(end) && !session.heard().Before(since) {
			return true
		}
	}

	return false
}

// prune forgets every session not heard of for viewerMemory.
func (v *clipViewers) prune(now time.Time) {
	v.lock.Lock()
	defer v.lock.Unlock()

	v.pruneLocked(now)
}

func (v *clipViewers) pruneLocked(now time.Time) {
	for id, session := range v.sessions {
		if now.Sub(session.heard()) >= viewerMemory {
			delete(v.sessions, id)
		}
	}
}

// clear forgets everything: the plugin is stopping.
func (v *clipViewers) clear() {
	v.lock.Lock()
	defer v.lock.Unlock()

	v.sessions = nil
	v.lastPollAt = time.Time{}
}

// noteClipViewer is handleWHEP's, for a session that is not a peek, whatever
// Clips is set to. Broadcast Box names every session it opens; one it did not
// name can never be found in a listing, so only the first minute's rule
// covers it, which an admin is told once.
func (p *Plugin) noteClipViewer(sessionID, userID, handle string, now time.Time) {
	viewers := &p.clips().viewers
	if sessionID == "" && viewers.noIDLogged.CompareAndSwap(false, true) {
		p.API.LogWarn("Broadcast Box gave a playback session no id, so viewers can clip only in their first minute of watching")
	}

	viewers.note(sessionID, userID, handle, now)
}

// noteClipSessions is the poller's, on every successful poll. From memory
// alone: no call to Mattermost and no log line, for a store never started as
// much as for one running.
func (p *Plugin) noteClipSessions(raw []bbStream, now time.Time) {
	p.clips().viewers.seen(raw, func(streamKey string) string {
		return p.streamHandle(cleanStreamKey(streamKey))
	}, now)
}

// clipTarget is what a draft is to be cut from, as far as the gate goes: the
// stream, live or the held end of one, its key, and for a tail, when it
// ended.
type clipTarget struct {
	handle    string
	source    string
	streamKey string
	end       time.Time
}

// mayClip is whether somebody linked — the gate has already said so, and
// key is their stream key as it read it — may cut a draft from target
// (A§3.4):
//
//  1. They are the stream's streamer: the person whose link, now, is to the
//     stream's key — the live stream's, or the one the tail kept.
//  2. Live: they have a session on it that the latest poll listed, or that
//     they opened in the last minute.
//  3. Tail: they had a session on it, opened by its end, that was still open
//     within the larger of a minute and two polls of its end: they were
//     watching when it ended.
func (p *Plugin) mayClip(userID, key string, target clipTarget, now time.Time) bool {
	if p.streamOf(key, p.streamTag(target.streamKey)) {
		return true
	}

	viewers := &p.clips().viewers
	switch target.source {
	case clipSourceLive:
		return viewers.watching(userID, target.handle, now)
	case clipSourceTail:
		poll := time.Duration(p.getConfiguration().PollIntervalSeconds) * time.Second
		return viewers.watchingAt(userID, target.handle, target.end.Add(-max(time.Minute, 2*poll)), target.end)
	}

	return false
}
