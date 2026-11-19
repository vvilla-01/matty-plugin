package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// The accounts loop's memory (design/3.4.0.md §2.13): one store for Steam and
// Hytale, made on first use and kept for the life of the plugin, so a restart
// of the loop loses nothing. Nothing in it is ever written to KV. It holds
// each service's link document as last read or written, with a counter of
// link writes; the Steam statuses, which live here and nowhere else, for at
// most five minutes after Steam said them; the failure state an admin is
// shown; the day's Steam call count, for the admin card only; the built
// GET /api/v1/accounts answer; and the two connect limiters.
//
// The loop steps once a minute while either service is on, and sooner when a
// connection changes. Each step reads every link document fresh (one KV read
// each), looks up everybody connected in Mattermost once, so that nobody
// whose account is deactivated or gone is asked about or shown, and asks
// Steam about everybody else in one GetPlayerSummaries call. While Hytale
// alone is on it still steps once a minute: the view is each node's own
// memory, and on a cluster that read is how a connection made on another
// node shows here.
//
// Four rules keep a Disconnect from coming back while a step of the loop is
// out:
//
//  1. Every link write installs its own compare-and-set result here and bumps
//     the counter, under lock, before the view is rebuilt (installLinks).
//  2. The view is always built from these cached documents, joined to the
//     statuses by SteamID; a status no cached entry holds is never shown
//     (accounts_view.go), and is forgotten at the end of the step.
//  3. A step installs its fresh read only when the counter is still what it
//     was when the read began (linkWrites).
//  4. A rename changes an existing entry with the same id and never creates
//     one (renameAccountLinks).
//
// Rule 1 holds in order too: each service's link writes on this node take
// its write lock from the read before the compare-and-set to the install of
// the result (updateAccountLinks), so two writes landing at once are cached
// in the order they were saved, and a write that changed nothing caches
// nothing.
//
// Lock order: runLock (starting and stopping the loop), then a service's
// write lock (its link writes), then lock (the data). No stats lock is ever
// taken, and eventLock (the accounts event) and warnLock are only ever taken
// alone. Stopping waits with no lock held, and every request a step makes
// uses the loop's context, so a stop ends within one request.

const (
	// Statuses change by the minute; one call a minute is 1,440 a day, 1.4 %
	// of what Steam's terms allow.
	steamReadEvery = 60 * time.Second

	// A connect or a disconnect asks for a step this soon after the last one,
	// and no sooner, so it shows within seconds without letting kicks flood
	// Steam: even a kick every 10 s all day would be 8,640 calls.
	steamKickGap = 10 * time.Second

	// After this, statuses are not guessed: the view says fresh: false and
	// every state is "", and the statuses themselves are forgotten.
	steamKeepGood = 5 * time.Minute

	// A failed read waits a minute, then two, four and so on; a read Steam
	// answered with "slow down" waits two minutes, then four. Never longer
	// than ten.
	steamBackoff    = 60 * time.Second
	steamSlowDown   = 2 * time.Minute
	steamBackoffMax = 10 * time.Minute

	// Admins are told Steam is out of reach only after this many failed
	// passes in a row, as for Twitch: one blip is not worth a sentence.
	steamProblemAfter = 3

	// The accounts event goes out at most this often, after the last change
	// of a burst.
	accountsEventGap = 10 * time.Second

	// The accounts loop's own warning throttle.
	accountsWarnEvery = 15 * time.Minute
)

// steamKeyRefused is what admins are told while Steam refuses the key.
const steamKeyRefused = "Steam refused the Web API key. Check it in the Steam section."

// accountService is what the store holds about one service.
type accountService struct {
	// write is held by each link write on this node, from its read to the
	// install of its result (rule 1, in order); never taken while lock is
	// held.
	write sync.Mutex

	links  *accountLinks // the cached document, nil before the first read or write
	writes uint64        // link writes installed so far (rule 1)
	bad    bool          // the stored document will not decode, and was set aside

	// problem is the loop's sentence for admins ("" when fine): for Steam,
	// the key refused, Steam asking VillaNET to slow down, or Steam out of
	// reach.
	problem string

	// signIn is what the last sign-in said was wrong with the application
	// itself (Hytale): hytaleProblemCredentials, hytaleProblemScope or "".
	signIn string
}

// accountsStore is Steam and Hytale's memory, and the loop's.
type accountsStore struct {
	plugin *Plugin

	now   func() time.Time                     // time.Now; tests replace it
	after func(time.Duration) <-chan time.Time // time.After; tests replace it

	lock     sync.Mutex
	services map[string]*accountService // by provider id

	// steam is each SteamID's status as Steam last gave it, or as a connect
	// seeded it. Only SteamIDs a cached entry holds are ever shown.
	steam map[string]steamStatus

	// inactive are the Mattermost users the loop last found deactivated or
	// gone: never asked about, never in the view, until a step finds them
	// active again.
	inactive map[string]bool

	// onAir is what the card of everybody on the air says is on, as
	// of the snapshot last stored (noteOnAir), and onAirKey the same as one
	// string, to tell when it has changed. The one thing in the view that is
	// not Steam's or Hytale's: accounts_activity.go. onAirSeq is that
	// snapshot's number, so that an older one told late changes nothing.
	onAir    map[string]onAirGame
	onAirKey string
	onAirSeq uint64

	// The day's Steam calls, by UTC day, for the admin card.
	steamDay   string
	steamCalls int

	// The GET /api/v1/accounts answer as last built, and its v.
	view  []byte
	viewV string

	connectLimiters map[string]*rateLimiter

	warnLock sync.Mutex
	warnedAt map[string]time.Time

	runLock sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	kickCh  chan struct{} // capacity 1, coalesced like refreshCh

	// The loop's timing, under lock. A step is due at nextAt; a kick brings
	// it forward, but never to sooner than steamKickGap after lastStep.
	lastStep time.Time
	nextAt   time.Time

	// Steam's reads, under lock: when Steam was last asked, and the last
	// good read, with how many active people it asked about and answered
	// for, for the admin card.
	lastTry       time.Time
	steamLastGood time.Time
	steamAsked    int
	steamAnswered int

	// Steam's failures, under lock: failed passes in a row; while holding,
	// Steam is not asked again before steamWaitUntil, whatever a kick says,
	// and with keyRefused not at all until the settings change.
	steamFails     int
	holding        bool
	steamWaitUntil time.Time
	keyRefused     bool

	// The accounts event: at most one waiting at a time (announce).
	eventLock   sync.Mutex
	eventCancel context.CancelFunc
	eventWait   sync.WaitGroup
	eventLast   time.Time // when the last one went out
}

func (p *Plugin) accounts() *accountsStore {
	p.accountsOnce.Do(func() {
		p.accountsState = &accountsStore{
			plugin:   p,
			now:      time.Now,
			after:    time.After,
			services: map[string]*accountService{},
			steam:    map[string]steamStatus{},
			inactive: map[string]bool{},
			warnedAt: map[string]time.Time{},
			kickCh:   make(chan struct{}, 1),

			// Nobody yet, which is no change: the first view with nobody in
			// it is not worth telling every panel about.
			viewV: accountsPeopleV(map[string]accountsViewPerson{}),

			// Five connects a quarter of an hour per person, per service:
			// each is a state written and a trip to the service's sign-in.
			connectLimiters: map[string]*rateLimiter{
				accountSteam:  newRateLimiter(5, 15*time.Minute),
				accountHytale: newRateLimiter(5, 15*time.Minute),
			},
		}
	})

	return p.accountsState
}

// serviceLocked is a service's entry, made on first use. With lock held.
func (s *accountsStore) serviceLocked(prov *accountProvider) *accountService {
	return s.serviceByIDLocked(prov.id)
}

func (s *accountsStore) serviceByIDLocked(id string) *accountService {
	service, found := s.services[id]
	if !found {
		service = &accountService{}
		s.services[id] = service
	}

	return service
}

// writeLock is a service's link-write lock. Taken before lock, never with it.
func (s *accountsStore) writeLock(prov *accountProvider) *sync.Mutex {
	s.lock.Lock()
	defer s.lock.Unlock()

	return &s.serviceLocked(prov).write
}

func (s *accountsStore) connectLimiter(prov *accountProvider) *rateLimiter {
	return s.connectLimiters[prov.id]
}

// installLinks is rule 1: a link write's own result becomes the cached
// document, and the counter moves, so a step whose read began before it
// keeps this one instead of its own. A document read or written cleanly
// means the stored bytes decode again.
func (s *accountsStore) installLinks(prov *accountProvider, doc *accountLinks) {
	if doc == nil {
		return
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	service := s.serviceLocked(prov)
	service.links = doc
	service.writes++
	service.bad = false
}

// linkWrites is the counter, for a step to note before its read (rule 3).
func (s *accountsStore) linkWrites(prov *accountProvider) uint64 {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.serviceLocked(prov).writes
}

// installRead is rule 3: a step's fresh read becomes the cached document
// only when no link write was installed since the step noted the counter.
func (s *accountsStore) installRead(prov *accountProvider, doc *accountLinks, noted uint64) bool {
	s.lock.Lock()
	defer s.lock.Unlock()

	service := s.serviceLocked(prov)
	if doc == nil || service.writes != noted {
		return false
	}
	service.links = doc
	service.bad = false

	return true
}

// cachedLinks is the cached document, nil before the first read or while the
// stored one will not decode.
func (s *accountsStore) cachedLinks(prov *accountProvider) *accountLinks {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.serviceLocked(prov).links
}

// markUnreadable is a document whose bytes will not decode: nobody can
// connect, nothing of the service shows, the admin is told, and the bytes are
// set aside once, by whoever finds them first. quarantine keeps only the
// first copy.
func (s *accountsStore) markUnreadable(prov *accountProvider, raw []byte) {
	s.lock.Lock()
	service := s.serviceLocked(prov)
	first := !service.bad
	service.bad = true
	service.links = nil
	if prov.id == accountSteam {
		s.steam = map[string]steamStatus{}
	}
	s.lock.Unlock()

	if first && len(raw) > 0 {
		s.plugin.quarantine(prov.linksKey, raw)
	}
}

// clearService forgets everything held about a service: for Delete all, and
// for a switch turned off. For Steam that is the statuses, the last reads and
// the failure state too, so a Steam switched back on starts by asking.
func (s *accountsStore) clearService(prov *accountProvider) {
	s.lock.Lock()
	defer s.lock.Unlock()

	service := s.serviceLocked(prov)
	service.links = nil
	service.bad = false
	service.problem = ""
	service.signIn = ""
	if prov.id == accountSteam {
		s.steam = map[string]steamStatus{}
		s.lastTry, s.steamLastGood = time.Time{}, time.Time{}
		s.steamAsked, s.steamAnswered = 0, 0
		s.liftSteamHoldLocked()
	}
}

// liftSteamHold forgets Steam's failures: the Steam settings changed, so a
// refused key or a wait is no reason not to ask at the next step.
func (s *accountsStore) liftSteamHold() {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.liftSteamHoldLocked()
}

func (s *accountsStore) liftSteamHoldLocked() {
	s.steamFails = 0
	s.holding, s.keyRefused = false, false
	s.steamWaitUntil = time.Time{}
	s.serviceLocked(steamProvider).problem = ""
}

// forgetSteam drops one SteamID's status now, so nothing shows it after a
// disconnect, even before the view is next built.
func (s *accountsStore) forgetSteam(steamID string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	delete(s.steam, steamID)
}

// seedSteam is a new connection's first status, from what Steam's sign-in and
// its one GetPlayerSummaries said: offline, and private as it said, until the
// loop's next read, which the connect kicks.
func (s *accountsStore) seedSteam(proof accountProof) {
	at := s.now()

	s.lock.Lock()
	defer s.lock.Unlock()

	s.steam[proof.ID] = steamStatus{State: "offline", Hidden: proof.Hidden, Name: proof.Name, at: at}
}

// steamHidden is whether the last read found a profile not public, for its
// owner's own /me.
func (s *accountsStore) steamHidden(steamID string) bool {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.steam[steamID].Hidden
}

// countSteamCall counts one Web API call against today, in UTC, for the
// admin card. The count starts again at midnight UTC, Steam's day.
func (s *accountsStore) countSteamCall(now time.Time) {
	day := now.UTC().Format("2006-01-02")

	s.lock.Lock()
	defer s.lock.Unlock()

	if s.steamDay != day {
		s.steamDay, s.steamCalls = day, 0
	}
	s.steamCalls++
}

// steamCallsToday is today's count, in UTC.
func (s *accountsStore) steamCallsToday(now time.Time) int {
	s.lock.Lock()
	defer s.lock.Unlock()

	if s.steamDay != now.UTC().Format("2006-01-02") {
		return 0
	}

	return s.steamCalls
}

// noteSignInProblem remembers what the last sign-in said was wrong with the
// application itself, or "" when it worked. By the service's id, because a
// provider's own finish calls it.
func (s *accountsStore) noteSignInProblem(id, kind string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.serviceByIDLocked(id).signIn = kind
}

// signInProblem is what noteSignInProblem last remembered.
func (s *accountsStore) signInProblem(prov *accountProvider) string {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.serviceLocked(prov).signIn
}

// adminProblem is /me's problem for a system admin while the switch is on and
// the service is set up: the first that applies of the loop's, the last
// sign-in's, and a document set aside.
func (s *accountsStore) adminProblem(prov *accountProvider) string {
	s.lock.Lock()
	defer s.lock.Unlock()

	service := s.serviceLocked(prov)
	switch {
	case service.problem != "":
		return service.problem
	case service.signIn == hytaleProblemCredentials:
		return "Hytale refused the application's Client ID or Client Secret. Check them in the Hytale section."
	case service.signIn == hytaleProblemScope:
		return "Hytale has not granted the application the scope hytale:profile."
	case service.bad:
		return accountLinksProblem(prov)
	default:
		return ""
	}
}

// steamCard is what the admin card is told about Steam's reads. Never a
// person's status: only when Steam was asked, how many people it answered
// for, and what is wrong.
type steamCard struct {
	lastTry, lastGood time.Time
	asked, answered   int
	problem           string
	keyRefused        bool
}

func (s *accountsStore) steamCardState() steamCard {
	s.lock.Lock()
	defer s.lock.Unlock()

	return steamCard{
		lastTry:    s.lastTry,
		lastGood:   s.steamLastGood,
		asked:      s.steamAsked,
		answered:   s.steamAnswered,
		problem:    s.serviceLocked(steamProvider).problem,
		keyRefused: s.keyRefused,
	}
}

// inactiveNow is whether the loop last found somebody's Mattermost account
// deactivated or gone.
func (s *accountsStore) inactiveNow(userID string) bool {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.inactive[userID]
}

// warnOnce logs something the admin has to know, once in a while rather than
// once per request. Keys are fixed strings, so the map cannot grow.
func (s *accountsStore) warnOnce(key, message string, pairs ...any) {
	now := s.now()

	s.warnLock.Lock()
	last, warned := s.warnedAt[key]
	if warned && now.Sub(last) < accountsWarnEvery && !now.Before(last) {
		s.warnLock.Unlock()
		return
	}
	s.warnedAt[key] = now
	s.warnLock.Unlock()

	s.plugin.API.LogWarn(message, pairs...)
}

// accountsChanged is told after every link write, once the write's result is
// installed (rule 1): the view is built again from the cached documents, and
// the loop is kicked, so a new Steam connection's status is read within
// seconds.
func (p *Plugin) accountsChanged() {
	s := p.accounts()
	s.rebuildView(p.getConfiguration(), s.now())
	s.kick()
}

// UserHasBeenDeactivated takes a deactivated person out of everybody's view
// at once and forgets their Steam status, so nobody here sees it and Steam is
// not asked about them; the loop's next step, which this kicks, agrees.
// Nothing is deleted: their connections stay until they or an admin remove
// them, and a step that finds the account active again shows them again.
func (p *Plugin) UserHasBeenDeactivated(_ *plugin.Context, user *model.User) {
	if user == nil || user.Id == "" {
		return
	}

	s := p.accounts()

	s.lock.Lock()
	s.inactive[user.Id] = true
	if links := s.serviceLocked(steamProvider).links; links != nil {
		if link, connected := links.Users[user.Id]; connected {
			delete(s.steam, link.ID)
		}
	}
	s.lock.Unlock()

	s.rebuildView(p.getConfiguration(), s.now())
	s.kick()
}

// restartAccountsLive stops the loop, if it is running, and waits for it.
// Changed Steam settings lift any hold on asking Steam: a key Steam refused is
// not asked with again until then. Changed Hytale settings forget what the
// last sign-in said was wrong with the application, which was about the
// Client ID and Secret now replaced: an admin who has just fixed them is not
// told to fix them again until a sign-in says so. A service switched off lets
// go of everything held about it at once, and the view is built again, so its
// rows go with the next event. The loop starts again while either service is
// on; with both off nothing is read, asked or started. previous is nil at
// activation.
func (p *Plugin) restartAccountsLive(previous *configuration) {
	cfg := p.getConfiguration()
	s := p.accounts()

	s.runLock.Lock()
	defer s.runLock.Unlock()

	s.stopLoopLocked()

	if previous == nil || previous.EnableSteam != cfg.EnableSteam || previous.SteamWebAPIKey != cfg.SteamWebAPIKey {
		s.liftSteamHold()
	}
	if previous == nil || previous.EnableHytale != cfg.EnableHytale ||
		previous.HytaleClientID != cfg.HytaleClientID || previous.HytaleClientSecret != cfg.HytaleClientSecret {
		s.noteSignInProblem(accountHytale, "")
	}
	for _, prov := range accountProviders {
		if !prov.on(cfg) {
			s.clearService(prov)
		}
	}
	s.rebuildView(cfg, s.now())

	if cfg.steamOn() || cfg.hytaleOn() {
		s.startLoopLocked()
	}
}

// stopAccountsLive stops the loop and waits for it, and drops an accounts
// event still waiting to go out: the plugin is stopping.
func (p *Plugin) stopAccountsLive() {
	s := p.accounts()

	s.runLock.Lock()
	s.stopLoopLocked()
	s.runLock.Unlock()

	s.stopEvents()
}

// kick asks for a step no sooner than steamKickGap after the last one. It
// never blocks, and is a no-op while the loop is not running. It never cuts
// short a backoff or a wait Steam asked for: the step it brings forward reads
// the link documents and builds the view, and asks Steam only once the wait
// is over.
func (s *accountsStore) kick() {
	s.lock.Lock()
	if soonest := s.lastStep.Add(steamKickGap); soonest.Before(s.nextAt) {
		s.nextAt = soonest
	}
	s.lock.Unlock()

	select {
	case s.kickCh <- struct{}{}:
	default:
	}
}

// stopLoopLocked cancels the loop and waits for it, with runLock held and no
// other lock: a step ends within one request of being cancelled.
func (s *accountsStore) stopLoopLocked() {
	if s.cancel == nil {
		return
	}

	s.cancel()
	<-s.done
	s.cancel, s.done = nil, nil
}

func (s *accountsStore) startLoopLocked() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel, s.done = cancel, done

	go s.run(ctx, done)
}

// run is the loop: a step now, then a step whenever one is due or a kick asks
// for one sooner. A step that panics is followed by a pause, then the loop
// (recovery.go): step says when the next is due before anything else, and
// the locks it takes are let go by a defer or held only around assignments.
func (s *accountsStore) run(ctx context.Context, done chan struct{}) {
	defer close(done)

	for {
		passSafely(ctx, s.plugin.API, "the accounts loop", loopPanicPause, func() {
			s.step(ctx, s.plugin.getConfiguration(), s.now())
		})

		for {
			if ctx.Err() != nil {
				return
			}

			s.lock.Lock()
			wait := s.nextAt.Sub(s.now())
			s.lock.Unlock()

			if wait <= 0 {
				break
			}

			select {
			case <-ctx.Done():
				return
			case <-s.kickCh:
				// nextAt may have moved closer; look again.
			case <-s.after(wait):
			}
		}
	}
}

// accountsRead is one service's part of a step: the counter as it stood
// before the read, the fresh document (nil when there is none to install),
// and who is connected, from the fresh document or, when the store did not
// answer, the cached one.
type accountsRead struct {
	prov  *accountProvider
	noted uint64
	doc   *accountLinks
	users map[string]accountLink
}

// step is one pass of the loop (§2.13), in order:
//
//  1. For each service that is on, the link-write counter is noted, then the
//     document read fresh. Bytes that will not decode are set aside, and the
//     service contributes nothing.
//  2. One GetUsersByIds for everybody connected to a service that is on, and
//     everybody on the air.
//     Nobody it does not return, or returns deactivated, is asked about or
//     shown. When it fails, the step ends there: Steam is asked nothing, and
//     the last view stands.
//  3. While Steam is on and not waiting: one GetPlayerSummaries for the
//     active people's SteamIDs.
//  4. Steam names that changed are written back, to existing entries only.
//  5. Each fresh read is installed, unless a link write was installed since
//     its counter was noted, which is newer.
//  6. Statuses no cached entry holds, or older than steamKeepGood, are
//     forgotten, and the view is built again from the cached documents.
//
// The goroutine calls it on a timer and on a kick; tests call it directly
// with a fake clock and fake hosts.
func (s *accountsStore) step(ctx context.Context, cfg *configuration, now time.Time) {
	p := s.plugin

	s.lock.Lock()
	s.lastStep = now
	s.nextAt = now.Add(steamReadEvery)
	s.lock.Unlock()
	defer s.waitForSteam(now)

	// The loop is restarted whenever a switch moves; a step that sees one
	// off first only lets go of what is held about it.
	for _, prov := range accountProviders {
		if !prov.on(cfg) {
			s.clearService(prov)
		}
	}

	var reads []accountsRead
	for _, prov := range accountProviders {
		if !prov.on(cfg) {
			continue
		}

		read := accountsRead{prov: prov, noted: s.linkWrites(prov)}
		doc, raw, err := p.readAccountLinks(prov)
		switch {
		case errors.Is(err, errAccountLinksUnreadable):
			s.markUnreadable(prov, raw)
		case err != nil:
			s.warnOnce(prov.id+"-links-read", "Could not read the list of "+prov.name+" connections; using the last one read.")
			if cached := s.cachedLinks(prov); cached != nil {
				read.users = cached.Users
			}
		default:
			read.doc, read.users = doc, doc.Users
		}
		reads = append(reads, read)
	}
	if ctx.Err() != nil {
		return
	}

	inactive, ok := s.lookUpPeople(reads)
	if !ok {
		return
	}

	var renames map[string]accountLink
	for _, read := range reads {
		if read.prov.id == accountSteam {
			renames = s.readSteam(ctx, cfg, read.users, inactive, now)
		}
	}
	if ctx.Err() != nil {
		return
	}

	if len(renames) > 0 {
		if _, err := p.renameAccountLinks(steamProvider, renames, now.UnixMilli()); err != nil {
			s.warnOnce("steam-rename", "Could not save Steam names that changed; Matty tries again at its next check.")
		}
	}

	for _, read := range reads {
		s.installRead(read.prov, read.doc, read.noted)
	}

	s.lock.Lock()
	s.pruneSteamLocked(now)
	s.lock.Unlock()

	s.rebuildView(cfg, now)
}

// lookUpPeople is step 2: who among everybody connected is deactivated or
// gone, and among everybody whose card is on the air, since the line that
// says what somebody is doing is given for them too, connected or not.
// Mattermost hands back deactivated accounts too, with DeleteAt set, and
// leaves out an account that was deleted. false when Mattermost did not
// answer, which ends the step.
func (s *accountsStore) lookUpPeople(reads []accountsRead) (map[string]bool, bool) {
	connected := map[string]bool{}
	for _, read := range reads {
		for userID := range read.users {
			connected[userID] = true
		}
	}
	s.lock.Lock()
	for userID := range s.onAir {
		connected[userID] = true
	}
	s.lock.Unlock()

	inactive := map[string]bool{}
	if len(connected) > 0 {
		users, appErr := s.plugin.API.GetUsersByIds(sortedIDs(connected))
		if appErr != nil {
			s.warnOnce("accounts-users", "Could not look up the Mattermost accounts of the people connected to Steam or Hytale, so Steam was not asked this time.")
			return nil, false
		}

		active := map[string]bool{}
		for _, user := range users {
			if user != nil && user.DeleteAt == 0 {
				active[user.Id] = true
			}
		}
		for userID := range connected {
			if !active[userID] {
				inactive[userID] = true
			}
		}
	}

	s.lock.Lock()
	s.inactive = inactive
	s.lock.Unlock()

	return inactive, true
}

// readSteam is step 3: one GetPlayerSummaries call for the SteamIDs of the
// active people connected, at most a hundred, never one more (one call's
// limit, and the cap on connections). A good answer replaces the statuses,
// so somebody Steam leaves out has none and shows "" rather than a guess. A
// failed one changes nothing, and the last good statuses stand until they
// are steamKeepGood old. It returns the names that changed, by Mattermost
// user, with the SteamID they were read for.
func (s *accountsStore) readSteam(ctx context.Context, cfg *configuration, users map[string]accountLink, inactive map[string]bool, now time.Time) map[string]accountLink {
	// A key that cannot travel in a header is never sent; the setup
	// sentence says why.
	key, ok := cfg.steamKey()
	if !ok {
		return nil
	}

	owners := map[string]string{}
	for userID, link := range users {
		if !inactive[userID] && steamIDPattern.MatchString(link.ID) {
			owners[link.ID] = userID
		}
	}
	ids := sortedIDs(owners)
	if len(ids) > maxSteamIDsPerCall {
		ids = ids[:maxSteamIDsPerCall]
	}

	s.lock.Lock()
	if len(ids) == 0 {
		// Nobody to ask about, and so nothing asked.
		s.steam = map[string]steamStatus{}
		s.lock.Unlock()

		return nil
	}
	if s.holding && (s.keyRefused || now.Before(s.steamWaitUntil)) {
		s.lock.Unlock()
		return nil
	}
	s.lastTry = now
	s.lock.Unlock()

	readCtx, cancel := context.WithTimeout(ctx, steamRequestBudget)
	players, err := s.plugin.askSteamSummaries(readCtx, key, ids)
	cancel()
	if err != nil {
		// Stopped is nobody's failure.
		if ctx.Err() == nil {
			s.steamFailed(err, now)
		}

		return nil
	}

	statuses := make(map[string]steamStatus, len(players))
	renames := map[string]accountLink{}
	for steamID, player := range players {
		status := steamStatusOf(player)
		status.at = now
		statuses[steamID] = status

		userID := owners[steamID]
		if status.Name != "" && status.Name != users[userID].Name {
			renames[userID] = accountLink{ID: steamID, Name: status.Name}
		}
	}

	s.lock.Lock()
	s.steam = statuses
	s.steamLastGood = now
	s.steamAsked, s.steamAnswered = len(ids), len(players)
	s.liftSteamHoldLocked()
	s.lock.Unlock()

	return renames
}

// steamFailed records a read that went wrong, and how long to wait before
// the next. A refused key is held until the settings change, with the
// statuses dropped, because Steam says retrying will not help; a "slow down"
// waits two minutes, doubling; anything else a minute, doubling, and admins
// are told only after three failed passes in a row.
func (s *accountsStore) steamFailed(err error, now time.Time) {
	reason, status := "unreachable", 0
	var failed *steamReadError
	if errors.As(err, &failed) {
		reason, status = failed.reason, failed.status
	}

	s.lock.Lock()
	service := s.serviceLocked(steamProvider)
	s.steamFails++
	s.holding = true

	switch reason {
	case "key":
		s.keyRefused = true
		s.steam = map[string]steamStatus{}
		service.problem = steamKeyRefused

	case "slow":
		s.steamWaitUntil = now.Add(steamWait(steamSlowDown, s.steamFails))
		service.problem = fmt.Sprintf("Steam is asking "+productName+" to slow down; checking again at %s UTC.", s.steamWaitUntil.UTC().Format("15:04"))

	default:
		s.steamWaitUntil = now.Add(steamWait(steamBackoff, s.steamFails))

		switch {
		case s.steamFails < steamProblemAfter:
			// Not yet worth a sentence; nor is a wait already over.
			service.problem = ""
		case s.steamLastGood.IsZero():
			service.problem = "Steam could not be reached; there has been no good check yet."
		default:
			service.problem = fmt.Sprintf("Steam could not be reached; the last good check was %d minutes ago.", int(now.Sub(s.steamLastGood)/time.Minute))
		}
	}
	s.lock.Unlock()

	if reason == "key" {
		s.warnOnce("steam-read-key", "Steam refused the Web API key, so Matty asks Steam nothing until the key is changed.", "status", status)
		return
	}
	s.warnOnce("steam-read-"+reason, "Could not read Steam statuses just now; Matty tries again later.", "reason", reason, "status", status)
}

// steamWait is a backoff: base after the first failure, doubling with each
// one after, never longer than steamBackoffMax.
func steamWait(base time.Duration, fails int) time.Duration {
	if fails < 1 {
		fails = 1
	}
	if fails > 10 {
		return steamBackoffMax
	}

	return min(base<<(fails-1), steamBackoffMax)
}

// waitForSteam ends a step: the next one is due a minute after this one
// began, or sooner if a kick asked while it ran, or when a wait Steam is held
// to ends, if that comes first.
func (s *accountsStore) waitForSteam(now time.Time) {
	s.lock.Lock()
	defer s.lock.Unlock()

	if s.holding && !s.keyRefused && s.steamWaitUntil.After(now) && s.steamWaitUntil.Before(s.nextAt) {
		s.nextAt = s.steamWaitUntil
	}
}

// pruneSteamLocked forgets every status no cached entry holds (somebody who
// disconnected, or was blocked, while a read was out) and every one older
// than steamKeepGood: statuses live in memory a few minutes at most.
func (s *accountsStore) pruneSteamLocked(now time.Time) {
	held := map[string]bool{}
	if service := s.serviceLocked(steamProvider); service.links != nil && !service.bad {
		for _, link := range service.links.Users {
			held[link.ID] = true
		}
	}

	for steamID, status := range s.steam {
		if !held[steamID] || now.Sub(status.at) > steamKeepGood {
			delete(s.steam, steamID)
		}
	}
}

// sortedIDs is a set's members in order, so a request carries the same ids
// in the same order every time.
func sortedIDs[V any](set map[string]V) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}
