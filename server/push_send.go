package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"

	"github.com/vvilla-01/matty-plugin/server/pushsend"
	"github.com/vvilla-01/matty-plugin/server/webpush"
)

// Sending: the hook offers a job and returns at once; a worker reads the
// person's devices and puts a job in each device's lane; lanes are sent from
// under limits, and nothing ever sleeps or blocks holding a slot: a retry is
// a timer that offers the job back to its lane, and a lane whose host or
// person is at its limit waits for the next pass. Everything runs under the
// stop context, and what was queued is lost on a stop.

const (
	pushKindMessage = "message"
	pushKindClear   = "clear"
	pushKindHello   = "hello"
	pushKindTest    = "test"
	pushKindEnded   = "ended"

	pushPayloadVersion = 1

	// pushSweepJob names the daily sweep's cluster job (its KV keys are
	// cron_push_sweep and mutex_cron_push_sweep).
	pushSweepJob   = "push_sweep"
	pushSweepEvery = 24 * time.Hour

	// The sweep checks at most this many sessions a second.
	pushSweepPerSecond = 20

	// Every node reads push_people this often.
	pushPeopleEvery = 30 * time.Second

	pushIntakeKept  = 1000
	pushLaneKept    = 32
	pushParkedKept  = 2000
	pushPerPerson   = 2
	pushPerHost     = 4
	pushInFlight    = 8
	pushBreakerRun  = 3
	pushBreakerRest = 60 * time.Second

	// A send that took this long or longer, whatever came of it, makes its
	// person slow for pushSlowFor from then; of the pushInFlight sends at
	// once, at most half go to slow people.
	pushSlowAfter = 2 * time.Second
	pushSlowFor   = 10 * time.Minute

	// A message or a clear is not tried later than this after it was made.
	pushMessageRetryAge = 5 * time.Minute

	// Of the pushParkedKept jobs waiting to be tried again on a node, at
	// most this many are one person's, so that pushes to one person that
	// keep failing leave room for everybody else's.
	pushParkedPerPerson = 256

	// A lane answered with a Retry-After is remembered as limited until a
	// push from it is delivered, or this long after the last such answer
	// (firstRetryAfter).
	pushLimitedKept = time.Hour

	// A hello answered this (ntfy's answer while the phone's ntfy has not
	// subscribed to the address yet) waits for the phone: it is tried again
	// after pushNoSubscriberWaits, then every pushNoSubscriberEvery, for as
	// long as its pending side is waited for.
	pushStatusNoSubscriber = 507
	pushNoSubscriberEvery  = time.Minute

	// A device's address confirmed in place of another is remembered this
	// long, so that nothing made before for the other one is sent (confirmedAs).
	pushReplacedFor = 5 * time.Minute

	// A device is suspended after this many failed sends in a row, or an
	// hour of nothing but failures with at least pushFailuresForTheHour of
	// them on this node.
	pushFailuresToSuspend  = 10
	pushFailingToSuspend   = time.Hour
	pushFailuresForTheHour = 3

	// A suspended (failing) device is tried with one message at most this
	// often on a node, and a refused one at most this often; a try that goes
	// through puts it back to work.
	pushFailingRetryEvery = 10 * time.Minute
	pushRefusedRetryEvery = time.Hour

	// Jobs waiting in all the lanes of a node together.
	pushQueuedKept = 20000

	// A session that could not be read is looked at again this long after
	// it was first marked, so that its device is dropped (or cleared) without
	// waiting for the next push or the sweep; at most so many such looks wait
	// on a node.
	pushSuspectLookAgain  = pushSuspectGrace + 5*time.Second
	pushSuspectLooksKept  = 2000
	pushRememberedDevices = 20000

	// A node remembers the last pushAliveKept sessions it saw alive. Before a
	// device whose session cannot be read is dropped, up to pushAliveTried of
	// them are read, newest first: only when one reads (so that the store
	// answers) and the device's still does not is the device dropped. When
	// none reads, a warn line says so, at most once in pushUnreadWarnEvery.
	pushAliveKept       = 16
	pushAliveTried      = 3
	pushUnreadWarnEvery = 10 * time.Minute

	// The sweep writes a row again when its last write is this old and one
	// of its sessions stands, so that a registration lives as long as its
	// session.
	pushRowRenewAfter = 30 * 24 * time.Hour

	// The per-device bucket: 30 at once, one more every 2 seconds.
	pushBucketBurst = 30
	pushBucketPer   = 60 * time.Second

	pushHelloDelay = 3 * time.Second
	pushStopWait   = 2 * time.Second
)

// pushRetryWaits are the waits before the three more tries an ended push
// that may go through later gets; a hello and a test push get only the
// first.
var pushRetryWaits = []time.Duration{2 * time.Second, 10 * time.Second, 60 * time.Second}

// pushMessageRetryWaits are a message's or a clear's: after 2, 10 and 60
// seconds, then once a minute, each only when it comes no later than
// pushMessageRetryAge after the job was made. So with nothing in the way
// the tries are at 0, 2, 12, 72, 132, 192 and 252 seconds; one more minute
// would be past the five.
var pushMessageRetryWaits = []time.Duration{
	2 * time.Second, 10 * time.Second, 60 * time.Second, 60 * time.Second, 60 * time.Second, 60 * time.Second,
}

// pushNoSubscriberWaits are the waits before the first tries of a hello
// answered pushStatusNoSubscriber.
var pushNoSubscriberWaits = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second}

// pushJob is one push to go.
type pushJob struct {
	kind    string
	user    string
	session string

	// conv is the conversation a message or a clear is about: the channel,
	// and the thread under collapsed threads.
	conv string

	payload []byte
	made    time.Time
	tries   int

	// noSubscriber is how many times a hello was answered
	// pushStatusNoSubscriber and parked for its next try.
	noSubscriber int

	target pushTarget

	// What the device's row said when the job was made.
	suspect bool
	trouble int64

	// revive: the one try of a device suspended as failing, or refused,
	// sent once with no retries of its own.
	revive bool
}

// pushTarget is where a job goes: a device's main side, or (for a hello) its
// pending side.
type pushTarget struct {
	endpoint string
	p256dh   string
	auth     string
	host     string
	pending  bool
	nonce    string

	// lapses is when a hello's pending side stops being waited for.
	lapses time.Time
}

// pushLane is one device's jobs to one of its sides (laneKey).
type pushLane struct {
	session string
	user    string
	jobs    []*pushJob
	busy    bool
}

// pushRuntime is this node's push memory: the key, the client, whom to
// offer pushes for, the lanes and their limits.
type pushRuntime struct {
	mu sync.Mutex

	now func() time.Time

	key    *webpush.Key
	signer *webpush.Signer
	client *pushsend.Client

	canPushKnown  bool
	canPushOK     bool
	canPushReason string
	canPushAt     time.Time

	// What is wrong with the list of push servers as last saved.
	hostsProblem string

	people      map[string]bool
	peopleKnown bool
	peopleFull  bool

	running bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	timers  map[*time.Timer]bool
	sweep   clipsJob

	intake []*pushJob
	signal chan struct{}

	// What bounds each: lanes, the devices' sides with jobs waiting or
	// sending (laneKey);
	// queued, pushQueuedKept; hostRun and retryRun, the hosts with a lane or
	// a send (the tick lets go of the rest, and of runs of retryRun whose
	// rests are over); hostRest, the rests of the last minute (let go of once
	// over); laneRest, pushRememberedDevices, the lanes' rests of the last
	// minute (let go of once over); limited, pushRememberedDevices, the lanes
	// answered with a Retry-After and nothing delivered since (let go of
	// after pushLimitedKept, and with their device); parkedBy, the people
	// with a job parked; slow, the people with a slow send in the last
	// pushSlowFor, pushRememberedDevices (let go of once over); failRun and
	// revived, pushRememberedDevices, and they go with their device (revived
	// also once older than the longest wait between tries); replaced,
	// pushRememberedDevices (let go of after pushReplacedFor); suspects,
	// pushSuspectLooksKept, each going when its look runs. slowBusy is how
	// many of the sends in flight were to slow people when they started.
	lanes      map[string]*pushLane
	queued     int
	personBusy map[string]int
	hostBusy   map[string]int
	busy       int
	slowBusy   int
	slow       map[string]time.Time
	hostRun    map[string]pushHostRun
	hostRest   map[string]time.Time
	retryRun   map[string]pushRetryRun
	laneRest   map[string]time.Time
	limited    map[string]time.Time
	failRun    map[string]int
	revived    map[string]time.Time
	replaced   map[string]pushReplaced
	suspects   map[string]bool
	parked     int
	parkedBy   map[string]int
	dropped    map[string]int64
	bucket     *rateLimiter

	// alive is the sessions lately seen alive, distinct, newest first, at
	// most pushAliveKept; unreadWarned, when the line that sessions could not
	// be read was last logged.
	alive        []string
	unreadWarned time.Time

	registerLimiter *rateLimiter
	testLimiter     *rateLimiter

	// What tests replace.
	retryWaits        []time.Duration
	messageWaits      []time.Duration
	noSubscriberWaits []time.Duration
	noSubscriberEvery time.Duration
	slowAfter         time.Duration
	breakerRest       time.Duration
	helloDelay        time.Duration
	sweepPace         time.Duration
	peopleEvery       time.Duration
	suspectWait       time.Duration
	schedule          func(api cluster.JobPluginAPI, run func()) (clipsJob, error)
	contactOf         func() string
}

func newPushRuntime() *pushRuntime {
	return &pushRuntime{
		now:               time.Now,
		client:            pushsend.NewClient(pushsend.Options{}),
		people:            map[string]bool{},
		timers:            map[*time.Timer]bool{},
		signal:            make(chan struct{}, 1),
		lanes:             map[string]*pushLane{},
		personBusy:        map[string]int{},
		hostBusy:          map[string]int{},
		slow:              map[string]time.Time{},
		hostRun:           map[string]pushHostRun{},
		hostRest:          map[string]time.Time{},
		retryRun:          map[string]pushRetryRun{},
		laneRest:          map[string]time.Time{},
		limited:           map[string]time.Time{},
		parkedBy:          map[string]int{},
		failRun:           map[string]int{},
		revived:           map[string]time.Time{},
		replaced:          map[string]pushReplaced{},
		suspects:          map[string]bool{},
		dropped:           map[string]int64{},
		bucket:            newRateLimiter(pushBucketBurst, pushBucketPer),
		registerLimiter:   newRateLimiter(6, 10*time.Minute),
		testLimiter:       newRateLimiter(3, time.Minute),
		retryWaits:        pushRetryWaits,
		messageWaits:      pushMessageRetryWaits,
		noSubscriberWaits: pushNoSubscriberWaits,
		noSubscriberEvery: pushNoSubscriberEvery,
		slowAfter:         pushSlowAfter,
		breakerRest:       pushBreakerRest,
		helloDelay:        pushHelloDelay,
		sweepPace:         time.Second / pushSweepPerSecond,
		peopleEvery:       pushPeopleEvery,
		suspectWait:       pushSuspectLookAgain,
		schedule: func(api cluster.JobPluginAPI, run func()) (clipsJob, error) {
			job, err := cluster.Schedule(api, pushSweepJob, cluster.MakeWaitForInterval(pushSweepEvery), run)
			if err != nil {
				return nil, err
			}

			return job, nil
		},
	}
}

// pushes is this node's push memory, made on first use.
func (p *Plugin) pushes() *pushRuntime {
	p.pushOnce.Do(func() {
		r := newPushRuntime()
		r.client.SetRules(p.getConfiguration().pushRules())
		r.contactOf = func() string { return p.getConfiguration().PushContact }
		p.pushState = r
	})

	return p.pushState
}

func (r *pushRuntime) setKey(key *webpush.Key) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.key = key
	r.signer = webpush.NewSigner(key, func() time.Time { return r.now() })
}

// publicKey is the push key's public half, or "" before it is loaded.
// sendClient is the client pushes go out with.
func (r *pushRuntime) sendClient() *pushsend.Client {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.client
}

func (r *pushRuntime) publicKey() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.key == nil {
		return ""
	}

	return r.key.Public()
}

func (r *pushRuntime) canPushNow() (bool, string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.canPushKnown || r.now().Sub(r.canPushAt) >= pushCanPushEvery || r.now().Before(r.canPushAt) {
		return false, "", false
	}

	return r.canPushOK, r.canPushReason, true
}

func (r *pushRuntime) keepCanPush(ok bool, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.canPushKnown, r.canPushOK, r.canPushReason, r.canPushAt = true, ok, reason, r.now()
}

func (r *pushRuntime) forgetCanPush() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.canPushKnown = false
}

func (r *pushRuntime) setHostsProblem(problem string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.hostsProblem = problem
}

func (r *pushRuntime) hostsProblemNow() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.hostsProblem
}

func (r *pushRuntime) notePerson(userID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.people[userID] = true
}

// offered is whether this node offers pushes for a person: everybody while
// the index has not been read or is full, as then it cannot be trusted.
func (r *pushRuntime) offered(userID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return !r.peopleKnown || r.peopleFull || r.people[userID]
}

func (r *pushRuntime) count(what string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.dropped[what]++
}

// droppedNow is this node's counts of pushes not sent, by why.
func (r *pushRuntime) droppedNow() map[string]int64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	counts := make(map[string]int64, len(r.dropped))
	for why, n := range r.dropped {
		counts[why] = n
	}

	return counts
}

// later runs f after wait, unless push stops first.
func (r *pushRuntime) later(wait time.Duration, f func()) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return false
	}

	var timer *time.Timer
	timer = time.AfterFunc(wait, func() {
		r.mu.Lock()
		_, live := r.timers[timer]
		delete(r.timers, timer)
		r.mu.Unlock()
		if live {
			f()
		}
	})
	r.timers[timer] = true

	return true
}

// spawn runs f on a goroutine that stopPush waits for, under the stop
// context, unless push has stopped.
func (r *pushRuntime) spawn(f func(ctx context.Context)) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return false
	}

	ctx := r.ctx
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		f(ctx)
	}()

	return true
}

// startPush starts this node's sending: whom to offer pushes for, the
// worker that fans jobs out to devices, and the daily sweep.
func (p *Plugin) startPush() {
	p.stopPush()

	r := p.pushes()
	r.sendClient().SetRules(p.getConfiguration().pushRules())
	p.refreshPushPeople()

	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.running, r.ctx, r.cancel = true, ctx, cancel
	r.wg.Add(2)
	r.mu.Unlock()

	go func() {
		defer r.wg.Done()
		p.fanOutPushes(ctx)
	}()
	go func() {
		defer r.wg.Done()
		p.followPushPeople(ctx)
	}()

	// The cluster job holds its lock while its callback runs, so the sweep
	// runs on a goroutine of its own, which a stop ends, and the callback
	// returns at once.
	job, err := r.schedule(p.API, func() { r.spawn(p.sweepPushes) })
	if err != nil {
		p.API.LogWarn("Could not schedule the daily sweep of push registrations", "error", err.Error())
	}
	r.mu.Lock()
	r.sweep = job
	r.mu.Unlock()
}

// stopPush stops it and waits, about a second at most, for what is sending.
// What was queued is lost.
func (p *Plugin) stopPush() {
	r := p.pushes()

	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	r.running = false
	r.cancel()
	for timer := range r.timers {
		timer.Stop()
	}
	r.timers = map[*time.Timer]bool{}
	r.intake = nil
	r.lanes = map[string]*pushLane{}
	r.queued = 0
	r.suspects = map[string]bool{}
	r.parked = 0
	r.parkedBy = map[string]int{}
	sweep := r.sweep
	r.sweep = nil
	r.mu.Unlock()

	if sweep != nil {
		_ = sweep.Close()
	}

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(pushStopWait):
		p.API.LogWarn("Push notifications were still sending when the plugin stopped")
	}
	r.sendClient().Close()
}

// refreshPushPeople reads push_people into this node's set. A failed read
// keeps the set it had.
func (p *Plugin) refreshPushPeople() {
	doc, _, err := p.readPushPeople()
	if err != nil {
		return
	}

	r := p.pushes()
	r.mu.Lock()
	defer r.mu.Unlock()

	r.people = make(map[string]bool, len(doc.People))
	for id := range doc.People {
		r.people[id] = true
	}
	r.peopleKnown = true
	r.peopleFull = len(doc.People) >= pushPeopleKept
}

func (p *Plugin) followPushPeople(ctx context.Context) {
	r := p.pushes()
	ticker := time.NewTicker(r.peopleEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			passSafely(ctx, p.API, "reading who has push notifications", loopPanicPause, p.refreshPushPeople)
			passSafely(ctx, p.API, "loading the push key", loopPanicPause, p.retryPushKey)
			passSafely(ctx, p.API, "tidying what push remembers", loopPanicPause, r.trim)
		}
	}
}

// trim lets go of what the senders remember and no longer need: rests and
// slow people that are over, runs of hosts with no lane or send, runs of
// Retry-After answers whose rests are over, lanes last answered with a
// Retry-After pushLimitedKept ago, tries of suspended or refused devices
// older than the longest wait between them, and confirmed addresses older
// than pushReplacedFor.
func (r *pushRuntime) trim() {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	for host, until := range r.hostRest {
		if !now.Before(until) {
			delete(r.hostRest, host)
		}
	}
	for key, until := range r.laneRest {
		if !now.Before(until) {
			delete(r.laneRest, key)
		}
	}
	for user, until := range r.slow {
		if !now.Before(until) {
			delete(r.slow, user)
		}
	}

	hosts := map[string]bool{}
	for _, lane := range r.lanes {
		for _, job := range lane.jobs {
			hosts[job.target.host] = true
		}
	}
	for host := range r.hostRun {
		if !hosts[host] && r.hostBusy[host] == 0 {
			delete(r.hostRun, host)
		}
	}
	// A run lasts as long as its rests, whether or not a job waits for the
	// host meanwhile (the one answered so is parked, not in a lane).
	for host, run := range r.retryRun {
		if !run.running(now) {
			delete(r.retryRun, host)
		}
	}
	for key, at := range r.limited {
		if now.Sub(at) >= pushLimitedKept || now.Before(at) {
			delete(r.limited, key)
		}
	}

	for session, at := range r.revived {
		if now.Sub(at) >= pushRefusedRetryEvery || now.Before(at) {
			delete(r.revived, session)
		}
	}
	for session, replaced := range r.replaced {
		if now.Sub(replaced.at) >= pushReplacedFor || now.Before(replaced.at) {
			delete(r.replaced, session)
		}
	}
}

// forget lets go of what this node remembers of a device's sends, once the
// device is confirmed again, registers again or is gone.
func (r *pushRuntime) forget(session string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.failRun, session)
	delete(r.revived, session)
	delete(r.laneRest, session)
	delete(r.limited, session)
}

// pushReplaced is the address a device's main side was confirmed as, and
// when.
type pushReplaced struct {
	endpoint string
	at       time.Time
}

// confirmedAs takes out of a device's lane what waits for an address other
// than the one just confirmed for it, and remembers that address, so that
// such a push made before then is not put in the lane again either (a retry
// coming back, enqueue): the device's next pushes wait for nothing sent to
// the address it had before.
func (r *pushRuntime) confirmedAs(session, endpoint string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, known := r.replaced[session]; known || len(r.replaced) < pushRememberedDevices {
		r.replaced[session] = pushReplaced{endpoint: endpoint, at: r.now()}
	}
	lane := r.lanes[session]
	if lane == nil {
		return
	}
	kept := lane.jobs[:0]
	for _, job := range lane.jobs {
		if job.target.endpoint != endpoint {
			r.queued--
			continue
		}
		kept = append(kept, job)
	}
	lane.jobs = kept
}

// reviveDue is whether a device suspended as failing, or refused, may be
// tried now: never before, or longer ago than its state's wait.
func (r *pushRuntime) reviveDue(session, state string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	every := pushFailingRetryEvery
	if state == pushStateRefused {
		every = pushRefusedRetryEvery
	}
	if at, tried := r.revived[session]; tried {
		return now.Sub(at) >= every
	}

	return len(r.revived) < pushRememberedDevices
}

// revivedAt notes a try of a suspended or refused device, or the moment it
// became so, from which the wait to its next try runs.
func (r *pushRuntime) revivedAt(session string, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, known := r.revived[session]; known || len(r.revived) < pushRememberedDevices {
		r.revived[session] = at
	}
}

// suspected is whether this node marked a session's device suspect and
// looks at it again soon.
func (r *pushRuntime) suspected(session string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.suspects[session]
}

// noteAlive notes a session seen alive (a request Mattermost handed the
// plugin with it, a post made with it, a read of it that stood) as the
// newest of those remembered.
func (r *pushRuntime) noteAlive(session string) {
	if session == "" {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	i := slices.Index(r.alive, session)
	if i < 0 {
		if len(r.alive) < pushAliveKept {
			r.alive = append(r.alive, "")
		}
		i = len(r.alive) - 1
	}
	copy(r.alive[1:i+1], r.alive[:i])
	r.alive[0] = session
}

// aliveBut is the newest n sessions remembered alive, without one.
func (r *pushRuntime) aliveBut(session string, n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	picked := make([]string, 0, n)
	for _, alive := range r.alive {
		if len(picked) == n {
			break
		}
		if alive != session {
			picked = append(picked, alive)
		}
	}

	return picked
}

// forgetAlive lets go of a remembered session that could not be read.
func (r *pushRuntime) forgetAlive(session string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.alive = slices.DeleteFunc(r.alive, func(alive string) bool { return alive == session })
}

// warnUnread is whether the line that sessions could not be read is due:
// at most once in pushUnreadWarnEvery on a node.
func (r *pushRuntime) warnUnread() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	if !r.unreadWarned.IsZero() && now.Sub(r.unreadWarned) < pushUnreadWarnEvery && !now.Before(r.unreadWarned) {
		return false
	}
	r.unreadWarned = now

	return true
}

// NotificationWillBePushed is Mattermost deciding to push to somebody. It
// never changes or stops Mattermost's own push, and asks nothing of the
// store or the network: it copies what it needs and offers a job.
func (p *Plugin) NotificationWillBePushed(notification *model.PushNotification, userID string) (*model.PushNotification, string) {
	p.safely("offering a push", func() { p.offerPush(notification, userID) })

	return nil, ""
}

func (p *Plugin) offerPush(notification *model.PushNotification, userID string) {
	if notification == nil || userID == "" || !p.getConfiguration().EnablePush {
		return
	}
	if notification.Type != model.PushTypeMessage && notification.Type != model.PushTypeClear {
		return
	}

	r := p.pushes()
	if notification.SubType != "" {
		// Pushes other plugins make (Calls and others) are not sent.
		r.count("other plugin")
		return
	}
	if !r.offered(userID) {
		return
	}

	now := r.now()
	job := &pushJob{kind: notification.Type, user: userID, made: now}
	if notification.Type == model.PushTypeMessage {
		job.conv = pushConversation(notification.ChannelId, notification.RootId, notification.IsCRTEnabled)
		job.payload = messagePayload(notification, userID, now)
	} else {
		job.conv = notification.ChannelId + "|" + notification.RootId
		job.payload = pushJSON(pushClearPayload{
			V: pushPayloadVersion, Type: pushKindClear, User: userID,
			Channel: notification.ChannelId, Root: notification.RootId, CRT: notification.IsCRTEnabled, At: now.UnixMilli(),
		})
	}

	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return
	}
	if job.kind == pushKindClear {
		kept := r.intake[:0]
		for _, waiting := range r.intake {
			if waiting.kind == pushKindMessage && waiting.user == userID && waiting.conv == job.conv {
				continue
			}
			kept = append(kept, waiting)
		}
		r.intake = kept
	}
	if len(r.intake) >= pushIntakeKept {
		r.intake = r.intake[1:]
		r.dropped["queue full"]++
	}
	r.intake = append(r.intake, job)
	r.mu.Unlock()

	select {
	case r.signal <- struct{}{}:
	default:
	}
}

// pushConversation is what a message is about, as a clear names it: the
// channel, and the thread under collapsed threads.
func pushConversation(channel, root string, crt bool) string {
	if !crt {
		root = ""
	}

	return channel + "|" + root
}

// The plaintexts (contract/README.md, "Push notifications").
type pushMessagePayload struct {
	V           int    `json:"v"`
	Type        string `json:"type"`
	User        string `json:"user"`
	Post        string `json:"post"`
	Channel     string `json:"channel"`
	Root        string `json:"root"`
	Team        string `json:"team"`
	Sender      string `json:"sender"`
	SenderName  string `json:"sender_name"`
	ChannelName string `json:"channel_name"`
	ChannelType string `json:"channel_type"`
	Text        string `json:"text"`
	CRT         bool   `json:"crt"`
	IDLoaded    bool   `json:"id_loaded"`
	FromWebhook bool   `json:"from_webhook"`
	At          int64  `json:"at"`
}

type pushClearPayload struct {
	V       int    `json:"v"`
	Type    string `json:"type"`
	User    string `json:"user"`
	Channel string `json:"channel"`
	Root    string `json:"root"`
	CRT     bool   `json:"crt"`
	At      int64  `json:"at"`
}

type pushHelloPayload struct {
	V     int    `json:"v"`
	Type  string `json:"type"`
	User  string `json:"user"`
	Nonce string `json:"nonce"`
	At    int64  `json:"at"`
}

type pushBarePayload struct {
	V    int    `json:"v"`
	Type string `json:"type"`
	User string `json:"user"`
	At   int64  `json:"at"`
}

func pushJSON(value any) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}

	return encoded
}

// messagePayload is what Mattermost put in its own push, for the server's
// contents setting, cut to fit: the text loses its end, on a character
// boundary, with "…". In id-loaded mode Mattermost's sender field holds the
// recipient, so sender is empty there.
func messagePayload(n *model.PushNotification, userID string, now time.Time) []byte {
	payload := pushMessagePayload{
		V: pushPayloadVersion, Type: pushKindMessage, User: userID,
		Post: n.PostId, Channel: n.ChannelId, Root: n.RootId, Team: n.TeamId,
		Sender: n.SenderId, SenderName: n.SenderName, ChannelName: n.ChannelName, ChannelType: string(n.ChannelType),
		Text: n.Message, CRT: n.IsCRTEnabled, IDLoaded: n.IsIdLoaded, FromWebhook: n.FromWebhook == "true",
		At: now.UnixMilli(),
	}
	if n.IsIdLoaded {
		payload.Sender = ""
	}
	if !utf8.ValidString(payload.Text) {
		payload.Text = strings.ToValidUTF8(payload.Text, "\uFFFD")
	}

	encoded := pushJSON(payload)
	if len(encoded) <= webpush.MaxPlaintext {
		return encoded
	}

	// The longest start of the text that fits with the ellipsis.
	text := []rune(payload.Text)
	low, high := 0, len(text)
	for low < high {
		mid := (low + high + 1) / 2
		payload.Text = string(text[:mid]) + "…"
		if len(pushJSON(payload)) <= webpush.MaxPlaintext {
			low = mid
		} else {
			high = mid - 1
		}
	}
	payload.Text = string(text[:low]) + "…"
	encoded = pushJSON(payload)
	if len(encoded) > webpush.MaxPlaintext {
		// Names alone too long to fit: they are cut as well.
		payload.Text, payload.SenderName, payload.ChannelName = "…", cutRunes(payload.SenderName, 64), cutRunes(payload.ChannelName, 64)
		encoded = pushJSON(payload)
	}

	return encoded
}

// fanOutPushes takes the hook's jobs and puts one in the lane of each of the
// person's confirmed devices.
func (p *Plugin) fanOutPushes(ctx context.Context) {
	r := p.pushes()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.signal:
		}

		for {
			r.mu.Lock()
			if !r.running || len(r.intake) == 0 {
				r.mu.Unlock()
				break
			}
			job := r.intake[0]
			r.intake = r.intake[1:]
			r.mu.Unlock()

			p.safely("handing a push to its devices", func() { p.fanOut(job) })
		}
	}
}

func (p *Plugin) fanOut(job *pushJob) {
	r := p.pushes()

	doc, err := p.readPushDoc(job.user)
	if err != nil {
		r.count("store")
		return
	}

	current := r.publicKey()
	now := r.now()
	for _, device := range doc.Devices {
		if device.Endpoint == "" || device.Key != current {
			continue
		}
		revive := false
		switch device.State {
		case pushStateConfirmed:
		case pushStateFailing, pushStateRefused:
			// Tried with one message now and then, so that it works again
			// by itself once its push server takes the pushes.
			if job.kind != pushKindMessage || !r.reviveDue(device.Session, device.State, now) {
				continue
			}
			revive = true
		default:
			continue
		}
		// Clears come only from the person's own reading and are not
		// metered: one left out would leave a notification showing.
		if job.kind != pushKindClear && !r.bucket.allowAt(device.Session, now) {
			r.count("too many for one phone")
			continue
		}
		if revive {
			r.revivedAt(device.Session, now)
		}

		copied := *job
		copied.session = device.Session
		copied.target = pushTarget{endpoint: device.Endpoint, p256dh: device.P256dh, auth: device.Auth, host: device.Host}
		copied.suspect = device.Suspect != 0
		copied.trouble = device.Trouble
		copied.revive = revive
		r.enqueue(&copied, false)
	}
	r.pump(p)
}

// laneKey is the lane a job waits in: its device's, or for a job to the
// device's pending side (a hello) a lane of that device's own beside it, so
// that a hello waits for nothing sent to the address it is to replace. No
// session id holds a NUL, so neither key is ever another session's.
func laneKey(job *pushJob) string {
	if job.target.pending {
		return job.session + "\x00pending"
	}

	return job.session
}

// enqueue puts a job in its lane: a clear first takes out the messages
// waiting there for the same conversation made before it; a full lane loses
// its oldest job; with pushQueuedKept waiting on the node, the job is
// dropped; a retry goes to the front. A job made before its device's main
// side was confirmed as another address, for the address before, is dropped
// (confirmedAs).
func (r *pushRuntime) enqueue(job *pushJob, front bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return
	}
	if replaced, known := r.replaced[job.session]; known && !job.target.pending &&
		job.target.endpoint != replaced.endpoint && !job.made.After(replaced.at) {
		return
	}

	key := laneKey(job)
	lane := r.lanes[key]
	if lane == nil {
		lane = &pushLane{session: job.session, user: job.user}
		r.lanes[key] = lane
	}

	if job.kind == pushKindClear {
		kept := lane.jobs[:0]
		for _, waiting := range lane.jobs {
			if waiting.kind == pushKindMessage && waiting.conv == job.conv && !waiting.made.After(job.made) {
				r.queued--
				continue
			}
			kept = append(kept, waiting)
		}
		lane.jobs = kept
	}

	switch {
	case len(lane.jobs) >= pushLaneKept:
		lane.jobs = lane.jobs[1:]
		r.queued--
		r.dropped["queue full"]++
	case r.queued >= pushQueuedKept:
		r.dropped["queue full"]++
		if len(lane.jobs) == 0 && !lane.busy {
			delete(r.lanes, key)
		}
		return
	}
	r.queued++
	if front {
		lane.jobs = append([]*pushJob{job}, lane.jobs...)
	} else {
		lane.jobs = append(lane.jobs, job)
	}
}

// pump starts sending whatever may go now. A lane of a slow person starts
// only while fewer than half the sends in flight are to slow people, so
// that slow push servers, however many people have them, hold up at most
// half the sending; everybody else's sends may use every slot. A host's
// pushPerHost at a time are whoever's they are, so on a push server many
// people use the half holds for the node's pushInFlight, not within that
// host. A lane waits while it rests or its host rests; one whose next job is
// a hello is held back by no rest, so that the app hears of a hello that
// cannot be delivered while it waits for it.
func (r *pushRuntime) pump(p *Plugin) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return
	}

	now := r.now()
	for key, lane := range r.lanes {
		if r.busy >= pushInFlight {
			return
		}
		if lane.busy {
			continue
		}
		if len(lane.jobs) == 0 {
			delete(r.lanes, key)
			continue
		}

		job := lane.jobs[0]
		host := job.target.host
		if r.personBusy[lane.user] >= pushPerPerson || r.hostBusy[host] >= pushPerHost {
			continue
		}
		if job.kind != pushKindHello && (resting(r.laneRest, key, now) || resting(r.hostRest, host, now)) {
			continue
		}
		slow := resting(r.slow, lane.user, now)
		if slow && r.slowBusy >= pushInFlight/2 {
			continue
		}

		lane.jobs = lane.jobs[1:]
		r.queued--
		lane.busy = true
		r.personBusy[lane.user]++
		r.hostBusy[host]++
		r.busy++
		if slow {
			r.slowBusy++
		}
		ctx := r.ctx

		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			p.safely("sending a push", func() { p.sendPush(ctx, lane, job) })
			r.release(lane, host, slow)
			r.pump(p)
		}()
	}
}

// release gives back a send's slots; slow is whether it counted as a send to
// a slow person when it started.
func (r *pushRuntime) release(lane *pushLane, host string, slow bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if slow {
		r.slowBusy--
	}
	lane.busy = false
	r.personBusy[lane.user]--
	if r.personBusy[lane.user] <= 0 {
		delete(r.personBusy, lane.user)
	}
	r.hostBusy[host]--
	if r.hostBusy[host] <= 0 {
		delete(r.hostBusy, host)
	}
	r.busy--
}

// resting is whether a host rests, or a person is slow, at a moment: whether
// the moment is before the one kept for it; one that is over is let go of.
// The caller holds r.mu.
func resting(rests map[string]time.Time, key string, now time.Time) bool {
	until, known := rests[key]
	if !known {
		return false
	}
	if now.Before(until) {
		return true
	}
	delete(rests, key)

	return false
}

// restHost keeps a host from being sent anything but hellos until a moment,
// unless it already rests as long, and says whether it did; the caller holds
// r.mu, and passes the lanes again then (passLater).
func (r *pushRuntime) restHost(host string, until time.Time) bool {
	if current, resting := r.hostRest[host]; resting && !until.After(current) {
		return false
	}
	r.hostRest[host] = until

	return true
}

// restLane keeps a lane from being sent anything but hellos until a moment,
// unless it already rests as long or pushRememberedDevices lanes rest, and
// says whether it did; the caller holds r.mu, and passes the lanes again
// then (passLater).
func (r *pushRuntime) restLane(key string, until time.Time) bool {
	current, known := r.laneRest[key]
	if known && !until.After(current) || !known && len(r.laneRest) >= pushRememberedDevices {
		return false
	}
	r.laneRest[key] = until

	return true
}

// pushRetryRun is a host's run of Retry-After answers with nothing delivered
// there in between: of the rests they asked for, the latest-ending two of
// different people.
type pushRetryRun struct {
	rests [2]pushPersonRest
}

type pushPersonRest struct {
	user  string
	until time.Time
}

// running is whether a rest of the run is still running at a moment.
func (run pushRetryRun) running(now time.Time) bool {
	for _, rest := range run.rests {
		if rest.user != "" && now.Before(rest.until) {
			return true
		}
	}

	return false
}

// firstRetryAfter notes a Retry-After answered for a lane, and says whether
// it is the lane's first since a push from it was delivered. Only that one
// counts toward its host's run (retryBreaker): a phone that goes on being
// answered so is at a limit of its own, as with ntfy's limits by subscriber,
// which is no reason to rest the host for everybody else. With
// pushRememberedDevices lanes remembered, another one's counts every time.
// The caller holds r.mu.
func (r *pushRuntime) firstRetryAfter(key string, now time.Time) bool {
	_, limited := r.limited[key]
	if limited || len(r.limited) < pushRememberedDevices {
		r.limited[key] = now
	}

	return !limited
}

// retryBreaker counts a Retry-After answered to a person's push toward its
// host's run, and says until when the run rests the host: only when another
// person's Retry-After rest there is still running, until the later of the
// two, so that one person's own pushes never rest a host others use. Any
// push delivered there ends the run (afterPush). The caller holds r.mu, and
// calls it only for a lane's first Retry-After (firstRetryAfter).
func (r *pushRuntime) retryBreaker(host, user string, until, now time.Time) (time.Time, bool) {
	run := r.retryRun[host]
	other := time.Time{}
	for _, rest := range run.rests {
		if rest.user != "" && rest.user != user && now.Before(rest.until) && rest.until.After(other) {
			other = rest.until
		}
	}

	// The person's rest takes the place of an earlier one of theirs, else of
	// one that is over, else of the one that ends first when it ends later.
	slot := -1
	for i, rest := range run.rests {
		if rest.user == user {
			slot = i
			break
		}
	}
	switch {
	case slot >= 0:
		if until.After(run.rests[slot].until) {
			run.rests[slot].until = until
		}
	default:
		slot = 0
		for i, rest := range run.rests {
			if rest.user == "" || !now.Before(rest.until) {
				slot = i
				break
			}
			if rest.until.Before(run.rests[slot].until) {
				slot = i
			}
		}
		if free := run.rests[slot]; free.user == "" || !now.Before(free.until) || until.After(free.until) {
			run.rests[slot] = pushPersonRest{user: user, until: until}
		}
	}
	r.retryRun[host] = run

	if other.IsZero() {
		return time.Time{}, false
	}
	if until.After(other) {
		return until, true
	}

	return other, true
}

// passLater passes the lanes again at a moment.
func (r *pushRuntime) passLater(p *Plugin, at time.Time) {
	r.later(at.Sub(r.now()), func() { r.pump(p) })
}

// pushHostRun is a host's run of sends that got no answer and were slow:
// how many, the person of the first, and whether another person's came.
type pushHostRun struct {
	sends   int
	person  string
	several bool
}

// hostBreaker counts an outcome toward its host's run, and says when the
// run rests the host: pushBreakerRun sends in a row that got no answer and
// were slow, from at least two people, so that one person's own push
// servers never rest a host others use. A refused connection, a failed
// lookup that came back at once and a dial the address rules refused are
// not such sends; any outcome that is not one ends the run. The caller
// holds r.mu.
func (r *pushRuntime) hostBreaker(host, user string, outcome pushsend.Outcome, slow bool) bool {
	if outcome.Kind != pushsend.KindUnreachable || outcome.Reason == pushsend.ReasonRules || !slow {
		delete(r.hostRun, host)
		return false
	}

	run := r.hostRun[host]
	if run.sends == 0 {
		run.person = user
	} else if user != run.person {
		run.several = true
	}
	run.sends++
	if run.sends < pushBreakerRun || !run.several {
		r.hostRun[host] = run
		return false
	}
	delete(r.hostRun, host)

	return true
}

// noteSlow makes a person slow for pushSlowFor from now. The caller holds
// r.mu.
func (r *pushRuntime) noteSlow(user string, now time.Time) {
	if _, known := r.slow[user]; known || len(r.slow) < pushRememberedDevices {
		r.slow[user] = now.Add(pushSlowFor)
	}
}

// sendPush sends one job and deals with what came of it.
func (p *Plugin) sendPush(ctx context.Context, lane *pushLane, job *pushJob) {
	r := p.pushes()
	if ctx.Err() != nil {
		return
	}

	// Switched off, nothing goes out: not what was waiting or parked, not a
	// hello or a test, not an ended push.
	if !p.getConfiguration().EnablePush {
		r.count("switched off")
		return
	}

	// A hello tried again goes only while its pending side is still the
	// device's and still waited for: one confirmed, replaced, removed or
	// lapsed since ends here, with nothing written or logged.
	if job.kind == pushKindHello && (job.tries > 0 || job.noSubscriber > 0) && !p.helloStillWaited(job) {
		return
	}

	if job.kind != pushKindEnded && !p.pushSessionStands(job) {
		return
	}

	r.mu.Lock()
	signer := r.signer
	r.mu.Unlock()
	if signer == nil {
		return
	}

	started := time.Now()
	outcome := p.deliverPush(ctx, signer, job)
	took := time.Since(started)
	if ctx.Err() != nil {
		return
	}
	p.afterPush(lane, job, outcome, took)
}

// helloStillWaited is whether a hello's pending side is still its device's
// (the same nonce) and still waited for. A store that cannot be read says
// nothing against it: the hello goes.
func (p *Plugin) helloStillWaited(job *pushJob) bool {
	doc, err := p.readPushDoc(job.user)
	if err != nil {
		return true
	}
	i := doc.find(job.session)
	if i < 0 {
		return false
	}
	device := doc.Devices[i]

	return device.pendingLive(p.pushes().now()) && device.Pending.Nonce == job.target.nonce
}

// deliverPush encrypts and sends.
func (p *Plugin) deliverPush(ctx context.Context, signer *webpush.Signer, job *pushJob) pushsend.Outcome {
	r := p.pushes()

	refused := pushsend.Outcome{Result: pushsend.Permanent, Kind: pushsend.KindAddress, Host: job.target.host, Reason: pushsend.ReasonRules}
	publicKey, err := webpush.DecodeSubscriberKey(job.target.p256dh)
	if err != nil {
		return refused
	}
	secret, err := webpush.DecodeAuthSecret(job.target.auth)
	if err != nil {
		return refused
	}
	body, err := webpush.Encrypt(job.payload, publicKey, secret)
	if err != nil {
		return pushsend.Outcome{Result: pushsend.Permanent, Kind: pushsend.KindAnswer, Host: job.target.host, Reason: pushsend.ReasonBody}
	}
	authorization, err := signer.Authorization(job.target.endpoint, r.contactOf())
	if err != nil {
		return refused
	}

	return r.sendClient().Send(ctx, job.target.endpoint, authorization, body)
}

// pushSessionStands checks the device's session before a push goes to it
// (checkPushSession). A suspect mark is cleared when the job's row had one
// or this node made one since.
func (p *Plugin) pushSessionStands(job *pushJob) bool {
	return p.checkPushSession(job.user, job.session, job.suspect || p.pushes().suspected(job.session))
}

// checkPushSession reads a device's session, as every push, the second look
// and the sweep do: one that cannot be read is pushSessionUnread's, and one
// that reads is pushSessionRead's. It says whether the session stands.
func (p *Plugin) checkPushSession(userID, sessionID string, maybeSuspect bool) bool {
	session, appErr := p.API.GetSession(sessionID)
	if appErr != nil || session == nil {
		return p.pushSessionUnread(userID, sessionID)
	}

	return p.pushSessionRead(userID, sessionID, session, maybeSuspect)
}

// pushSessionRead is a device's session as read: one that is over (expired,
// or somebody else's) drops the device at once; one that stands is noted
// alive and clears the device's suspect mark when it may have one. It says
// whether the session stands.
func (p *Plugin) pushSessionRead(userID, sessionID string, session *model.Session, maybeSuspect bool) bool {
	if session.IsExpired() || session.UserId != userID {
		p.dropPushDevice(userID, sessionID, "its session is over")
		return false
	}
	p.pushes().noteAlive(sessionID)

	if maybeSuspect {
		_, _ = p.updatePushDoc(userID, func(doc *pushDoc, _ time.Time) bool {
			i := doc.find(sessionID)
			if i < 0 || doc.Devices[i].Suspect == 0 {
				return false
			}
			doc.Devices[i].Suspect = 0
			return true
		})
	}

	return true
}

// pushSessionUnread is a session that could not be read: suspect the first
// time, and looked at again a little over a minute later; once it has been
// suspect for a minute, pushSessionGone's. A mark older than two such
// looks' wait (left from a time no session could be read) is made anew and
// looked at again as a first one, so that the two failed reads that drop a
// device are always about a minute apart. A store that is down deletes
// nothing, since nothing can be written to it. It says whether the session
// stands after all (pushSessionGone read it).
func (p *Plugin) pushSessionUnread(userID, session string) bool {
	drop, again := false, false
	_, err := p.updatePushDoc(userID, func(doc *pushDoc, now time.Time) bool {
		drop, again = false, false
		i := doc.find(session)
		if i < 0 {
			return false
		}
		device := &doc.Devices[i]
		age := now.Sub(time.UnixMilli(device.Suspect))
		if device.Suspect == 0 || age >= 2*pushSuspectLookAgain {
			device.Suspect = now.UnixMilli()
			again = true
			return true
		}
		if age >= pushSuspectGrace {
			drop = true
		} else {
			again = true
		}
		return false
	})
	switch {
	case err != nil:
	case drop:
		return p.pushSessionGone(userID, session)
	case again:
		p.lookAgainLater(userID, session)
	}

	return false
}

// pushSessionGone is a session suspect for a minute and unread again.
// Mattermost answers a session that is no more and a store that fails alike,
// so it is taken as over only on evidence: up to pushAliveTried sessions
// this node saw alive are read, newest first (one that cannot be read is
// forgotten), and when one reads, the session is read once more; only if it
// still cannot be read is its device dropped. When none reads, nothing is
// dropped or written, and no look is scheduled: the device stays suspect,
// and the next push or the sweep looks again. It says whether the session
// stands (it read this time, and is the person's and not over).
func (p *Plugin) pushSessionGone(userID, session string) bool {
	r := p.pushes()

	tried, answered := r.aliveBut(session, pushAliveTried), false
	for _, other := range tried {
		if read, appErr := p.API.GetSession(other); appErr == nil && read != nil {
			answered = true
			break
		}
		r.forgetAlive(other)
	}
	if !answered {
		if len(tried) > 0 && r.warnUnread() {
			p.API.LogWarn("Sessions could not be read just now, so no phone was taken as signed out")
		}
		return false
	}

	read, appErr := p.API.GetSession(session)
	if appErr != nil || read == nil {
		p.dropPushDevice(userID, session, "its session could not be read while others could")
		return false
	}

	return p.pushSessionRead(userID, session, read, true)
}

// lookAgainLater reads a suspect device's session once more after
// suspectWait, and does with the answer what the sweep does: at most one
// such look a session, and pushSuspectLooksKept on a node (beyond them the
// next push or the sweep looks).
func (p *Plugin) lookAgainLater(userID, session string) {
	r := p.pushes()

	r.mu.Lock()
	if !r.running || r.suspects[session] || len(r.suspects) >= pushSuspectLooksKept {
		r.mu.Unlock()
		return
	}
	r.suspects[session] = true
	wait := r.suspectWait
	r.mu.Unlock()

	scheduled := r.later(wait, func() {
		r.mu.Lock()
		delete(r.suspects, session)
		r.mu.Unlock()

		r.spawn(func(ctx context.Context) {
			if ctx.Err() == nil {
				p.safely("looking again at a push session", func() { p.checkPushSession(userID, session, true) })
			}
		})
	})
	if !scheduled {
		r.mu.Lock()
		delete(r.suspects, session)
		r.mu.Unlock()
	}
}

// dropPushDevice drops a device whose session is over, and sends the ended
// push, from memory, to its main side when it has one: an address never
// confirmed is sent nothing else.
func (p *Plugin) dropPushDevice(userID, session, why string) {
	var dropped *pushDevice
	_, err := p.updatePushDoc(userID, func(doc *pushDoc, _ time.Time) bool {
		dropped = nil
		i := doc.find(session)
		if i < 0 {
			return false
		}
		device := doc.Devices[i]
		dropped = &device
		doc.remove(i)
		return true
	})
	if err != nil || dropped == nil {
		return
	}

	r := p.pushes()
	r.forget(session)
	host := dropped.Host
	if dropped.Endpoint == "" && dropped.Pending != nil {
		host = dropped.Pending.Host
	}
	p.API.LogInfo("A phone's push registration was dropped", "why", why, "host", host)
	if dropped.Endpoint == "" {
		return
	}

	target := pushTarget{endpoint: dropped.Endpoint, p256dh: dropped.P256dh, auth: dropped.Auth, host: dropped.Host}
	now := r.now()
	r.enqueue(&pushJob{
		kind: pushKindEnded, user: userID, session: session, made: now, target: target,
		payload: pushJSON(pushBarePayload{V: pushPayloadVersion, Type: pushKindEnded, User: userID, At: now.UnixMilli()}),
	}, false)
	r.pump(p)
}

// afterPush is what an outcome leads to; took is how long the send took.
func (p *Plugin) afterPush(lane *pushLane, job *pushJob, outcome pushsend.Outcome, took time.Duration) {
	r := p.pushes()
	host := job.target.host
	now := r.now()

	// A slow send makes its person slow (pump keeps them to half the
	// sending); the host's breaker rests a host that does not answer people
	// for a minute. A Retry-After rests the lane the push came from as long
	// as it says (with ntfy it is often that one phone's own limit). It
	// rests the host only when it is the lane's first since a push from it
	// was delivered (firstRetryAfter) and another person's such rest there
	// still runs, with nothing delivered there in between (retryBreaker). A
	// hello's rests nothing and counts for nothing: nothing in its lane
	// waits for a rest (pump). A rest is written in the same step as the
	// decision, so that no send to the lane or the host starts in between.
	r.mu.Lock()
	slow := took >= r.slowAfter
	if slow {
		r.noteSlow(job.user, now)
	}
	breakerUntil, retryUntil := now.Add(r.breakerRest), now.Add(outcome.RetryAfter)
	hostBreaks := r.hostBreaker(host, job.user, outcome, slow) && r.restHost(host, breakerUntil)
	laneRests, hostRests, hostUntil := false, false, time.Time{}
	if outcome.Result == pushsend.Delivered {
		delete(r.retryRun, host)
		if !job.target.pending {
			delete(r.limited, laneKey(job))
		}
	}
	if outcome.RetryAfter > 0 && !job.target.pending {
		key := laneKey(job)
		laneRests = r.restLane(key, retryUntil)
		if r.firstRetryAfter(key, now) {
			if until, run := r.retryBreaker(host, job.user, retryUntil, now); run {
				hostRests, hostUntil = r.restHost(host, until), until
			}
		}
	}
	r.mu.Unlock()
	if hostBreaks {
		p.API.LogWarn("A push server did not answer three times in a row, so nothing but check messages is sent to it for a minute", "host", host)
		r.passLater(p, breakerUntil)
	}
	if laneRests {
		r.passLater(p, retryUntil)
	}
	if hostRests {
		r.passLater(p, hostUntil)
	}

	if job.kind == pushKindEnded {
		// Tried again like any other push, and never written as failed:
		// its row is gone.
		if outcome.Result == pushsend.Retry {
			p.retryLater(job, now)
		}
		return
	}
	if job.revive {
		p.afterRevive(job, outcome)
		return
	}

	switch outcome.Result {
	case pushsend.Delivered:
		r.mu.Lock()
		_, failed := r.failRun[job.session]
		_, suspended := r.revived[job.session]
		delete(r.failRun, job.session)
		r.mu.Unlock()
		switch {
		case job.target.pending:
		case suspended && p.pushRevived(job):
			// A push that was waiting when this node suspended the device,
			// or marked it refused, went through: it is back to work.
		case job.trouble != 0 || failed:
			p.pushTroubleOver(job)
		}

	case pushsend.Gone:
		if job.target.pending {
			// A hello to an address that is no more: its pending side
			// reads as failing, with the status, as for any hello that
			// cannot be delivered.
			p.pushFailed(job, outcome)
			return
		}
		p.API.LogInfo("A push server says a phone's push address is no more, so it is dropped", "host", host, "status", outcome.Status)
		p.pushGone(job)

	case pushsend.Refused:
		p.API.LogWarn("A push server refused this server's pushes", "host", host, "status", outcome.Status)
		p.pushRefused(job, outcome)

	case pushsend.Retry:
		if job.target.pending && outcome.Status == pushStatusNoSubscriber {
			p.helloWaitsForThePhone(job, now)
			return
		}
		if !p.retryLater(job, now) {
			p.pushFailed(job, outcome)
		}

	default:
		p.pushFailed(job, outcome)
	}
}

// retryLater parks a job for its next try, when it has one left and there
// is room: a hello and a test push have one (none for a hello answered
// pushStatusNoSubscriber before: it has had its tries); a message or a
// clear has messageWaits, each only when it comes no later than
// pushMessageRetryAge after the job was made; an ended push has
// retryWaits. It says whether the job is taken care of; false is a send
// that failed for good.
func (p *Plugin) retryLater(job *pushJob, now time.Time) bool {
	r := p.pushes()

	r.mu.Lock()
	waits := r.retryWaits
	if job.kind == pushKindMessage || job.kind == pushKindClear {
		waits = r.messageWaits
	}
	r.mu.Unlock()
	switch job.kind {
	case pushKindHello, pushKindTest:
		// So that the app hears of a hello that cannot be delivered while it
		// still waits for it.
		waits = waits[:min(1, len(waits))]
		if job.noSubscriber > 0 {
			return false
		}
	}
	if job.tries >= len(waits) {
		return false
	}
	wait := waits[job.tries]
	if (job.kind == pushKindMessage || job.kind == pushKindClear) && now.Add(wait).After(job.made.Add(pushMessageRetryAge)) {
		return false
	}

	return p.park(job, wait, &job.tries)
}

// helloWaitsForThePhone is a hello answered pushStatusNoSubscriber: the push
// server has no subscriber for the address yet, as ntfy says until the
// phone's ntfy has connected. Nothing is written, so that the registration
// still reads pending; it is tried again after noSubscriberWaits, then every
// noSubscriberEvery, while its pending side lives (and, before each try,
// is still the device's: sendPush). One whose next try would come after
// its pending side lapsed ends there, with nothing written or logged. The
// first such answer is logged.
func (p *Plugin) helloWaitsForThePhone(job *pushJob, now time.Time) {
	r := p.pushes()

	r.mu.Lock()
	wait := r.noSubscriberEvery
	if job.noSubscriber < len(r.noSubscriberWaits) {
		wait = r.noSubscriberWaits[job.noSubscriber]
	}
	r.mu.Unlock()
	if !now.Add(wait).Before(job.target.lapses) {
		return
	}

	// A hello always has room to wait (park).
	first := job.noSubscriber == 0
	p.park(job, wait, &job.noSubscriber)
	if first {
		p.API.LogInfo("A push server has no subscriber for a phone's push address yet, so its check message is tried again until the phone's UnifiedPush app connects, for ten minutes at most", "host", job.target.host)
	}
}

// park offers a job back to its lane after wait, counting the try in tries,
// when there is room: pushParkedKept jobs wait so on a node, and
// pushParkedPerPerson of them are one person's. A hello always has room and
// is not counted as its person's: there are no more of them than pending
// registrations, which the register limit bounds, and one that could not
// wait would read as failing. It says whether it did. Back in its lane the
// job waits for a rest of its lane or its host too, unless it is a hello
// (pump).
func (p *Plugin) park(job *pushJob, wait time.Duration, tries *int) bool {
	r := p.pushes()

	hello := job.kind == pushKindHello
	r.mu.Lock()
	room := hello || (r.parked < pushParkedKept && r.parkedBy[job.user] < pushParkedPerPerson)
	if room {
		r.parked++
		if !hello {
			r.parkedBy[job.user]++
		}
	}
	r.mu.Unlock()
	if !room {
		r.count("too many waiting to try again")
		return false
	}
	// Not below nothing: a stop empties both counts while a timer may
	// already be running.
	unpark := func() {
		r.mu.Lock()
		if r.parked > 0 {
			r.parked--
		}
		if !hello {
			if r.parkedBy[job.user] > 1 {
				r.parkedBy[job.user]--
			} else {
				delete(r.parkedBy, job.user)
			}
		}
		r.mu.Unlock()
	}

	*tries++
	parked := r.later(wait, func() {
		unpark()
		r.enqueue(job, true)
		r.pump(p)
	})
	if !parked {
		// Stopped: what was queued is lost.
		unpark()
	}

	return true
}

// afterRevive is what the one try of a failing or refused device leads to:
// delivered, it is confirmed again; gone and refused, as for any push;
// anything else leaves it as it was, with nothing written, until its next
// try.
func (p *Plugin) afterRevive(job *pushJob, outcome pushsend.Outcome) {
	switch outcome.Result {
	case pushsend.Delivered:
		p.pushRevived(job)
	case pushsend.Gone:
		p.API.LogInfo("A push server says a phone's push address is no more, so it is dropped", "host", job.target.host, "status", outcome.Status)
		p.pushGone(job)
	case pushsend.Refused:
		p.pushRefused(job, outcome)
	}
}

// pushRevived puts a failing or refused device back to work, and says
// whether it did.
func (p *Plugin) pushRevived(job *pushJob) bool {
	r := p.pushes()

	revived := false
	_, err := p.updatePushDoc(job.user, func(doc *pushDoc, now time.Time) bool {
		revived = false
		i := doc.find(job.session)
		if i < 0 || doc.Devices[i].Endpoint != job.target.endpoint {
			return false
		}
		device := &doc.Devices[i]
		if device.State != pushStateFailing && device.State != pushStateRefused {
			return false
		}
		device.State, device.Since = pushStateConfirmed, now.UnixMilli()
		device.Trouble, device.Kind, device.Status = 0, "", 0
		revived = true
		return true
	})
	r.forget(job.session)
	if err != nil || !revived {
		return false
	}
	p.API.LogInfo("A push server takes a phone's pushes again, so it is sent them as before", "host", job.target.host)

	return true
}

// pushTroubleOver clears a run of failures once a push goes through.
func (p *Plugin) pushTroubleOver(job *pushJob) {
	_, _ = p.updatePushDoc(job.user, func(doc *pushDoc, _ time.Time) bool {
		i := doc.find(job.session)
		if i < 0 || doc.Devices[i].Endpoint != job.target.endpoint || doc.Devices[i].Trouble == 0 {
			return false
		}
		doc.Devices[i].Trouble, doc.Devices[i].Kind, doc.Devices[i].Status = 0, "", 0
		return true
	})
}

// pushGone drops the main side a push server says is no more (a hello's
// pending side is pushFailed's). No ended push: the address is gone.
func (p *Plugin) pushGone(job *pushJob) {
	p.pushes().forget(job.session)

	_, _ = p.updatePushDoc(job.user, func(doc *pushDoc, now time.Time) bool {
		i := doc.find(job.session)
		if i < 0 {
			return false
		}
		device := &doc.Devices[i]
		if device.Endpoint != job.target.endpoint {
			return false
		}
		if device.Pending != nil {
			// A registration waiting for its hello stays; the address
			// that is gone does not.
			device.Endpoint, device.P256dh, device.Auth, device.Key, device.Host = "", "", "", "", ""
			device.State, device.Since = pushStatePending, now.UnixMilli()
			return true
		}
		doc.remove(i)
		return true
	})
}

// pushRefused marks the side refused, with the push server's status. A
// refused device is then tried with one message at most every
// pushRefusedRetryEvery, from now, until one goes through or the app
// registers again.
func (p *Plugin) pushRefused(job *pushJob, outcome pushsend.Outcome) {
	if !job.target.pending {
		r := p.pushes()
		r.mu.Lock()
		delete(r.failRun, job.session)
		r.mu.Unlock()
		r.revivedAt(job.session, r.now())
	}

	_, _ = p.updatePushDoc(job.user, func(doc *pushDoc, now time.Time) bool {
		i := doc.find(job.session)
		if i < 0 {
			return false
		}
		device := &doc.Devices[i]
		if job.target.pending {
			if device.Pending == nil || device.Pending.Nonce != job.target.nonce {
				return false
			}
			device.Pending.State, device.Pending.Status, device.Pending.Kind, device.Pending.Since = pushStateRefused, outcome.Status, string(outcome.Kind), now.UnixMilli()
			return true
		}
		if device.Endpoint != job.target.endpoint || device.State == pushStateRefused {
			return false
		}
		device.State, device.Status, device.Kind, device.Since, device.Trouble = pushStateRefused, outcome.Status, string(outcome.Kind), now.UnixMilli(), 0
		return true
	})
}

// pushFailed is a send that did not go through and will not be tried
// again. The first of a run is written; the device is suspended after
// pushFailuresToSuspend in a row on this node, or once the run is an hour
// old with at least pushFailuresForTheHour of them on this node. A
// suspended device is then tried with one message at most every
// pushFailingRetryEvery, from now, until one goes through or the app
// registers again.
func (p *Plugin) pushFailed(job *pushJob, outcome pushsend.Outcome) {
	r := p.pushes()
	now := r.now()

	if job.target.pending {
		// Logged only when the pending side was this hello's: not for one
		// that a later registration took the place of.
		written := false
		_, err := p.updatePushDoc(job.user, func(doc *pushDoc, now time.Time) bool {
			written = false
			i := doc.find(job.session)
			if i < 0 || doc.Devices[i].Pending == nil || doc.Devices[i].Pending.Nonce != job.target.nonce {
				return false
			}
			pending := doc.Devices[i].Pending
			pending.State, pending.Status, pending.Kind, pending.Since = pushStateFailing, outcome.Status, string(outcome.Kind), now.UnixMilli()
			written = true
			return true
		})
		if err == nil && written {
			p.API.LogInfo("A phone's first push could not be delivered", "host", job.target.host, "kind", string(outcome.Kind), "status", outcome.Status, "reason", outcome.Reason)
		}
		return
	}

	// Only a failure of the device's own address counts toward its run, not
	// one of the address it had before it registered again.
	current := true
	if doc, err := p.readPushDoc(job.user); err == nil {
		i := doc.find(job.session)
		current = i >= 0 && doc.Devices[i].Endpoint == job.target.endpoint
	}
	r.mu.Lock()
	if _, known := r.failRun[job.session]; current && (known || len(r.failRun) < pushRememberedDevices) {
		r.failRun[job.session]++
	}
	run := r.failRun[job.session]
	r.mu.Unlock()

	anHour := func(trouble int64, now time.Time) bool {
		return trouble != 0 && run >= pushFailuresForTheHour && now.Sub(time.UnixMilli(trouble)) >= pushFailingToSuspend
	}
	suspend := run >= pushFailuresToSuspend || anHour(job.trouble, now)
	if job.trouble != 0 && !suspend {
		return
	}

	suspended := false
	_, _ = p.updatePushDoc(job.user, func(doc *pushDoc, now time.Time) bool {
		suspended = false
		i := doc.find(job.session)
		if i < 0 || doc.Devices[i].Endpoint != job.target.endpoint {
			return false
		}
		device := &doc.Devices[i]
		if device.State != pushStateConfirmed {
			return false
		}
		if device.Trouble == 0 {
			device.Trouble = now.UnixMilli()
		}
		device.Kind, device.Status = string(outcome.Kind), outcome.Status
		if suspend || anHour(device.Trouble, now) {
			device.State, device.Since, device.Trouble = pushStateFailing, device.Trouble, 0
			suspended = true
		}
		return true
	})

	if suspended {
		r.mu.Lock()
		delete(r.failRun, job.session)
		r.mu.Unlock()
		r.revivedAt(job.session, now)
		p.API.LogWarn("Pushes to a phone kept failing, so it is tried with one of its pushes at most every ten minutes until one goes through", "host", job.target.host, "kind", string(outcome.Kind), "status", outcome.Status)
	} else {
		p.API.LogInfo("A push could not be delivered", "host", job.target.host, "kind", string(outcome.Kind), "status", outcome.Status, "reason", outcome.Reason)
	}
}

// queueHello sends a pending side its hello, a few seconds on, so that the
// app has finished registering when it comes.
func (p *Plugin) queueHello(userID, session string, pending pushPending) {
	r := p.pushes()
	at := r.now().Add(r.helloDelay)
	job := &pushJob{
		kind: pushKindHello, user: userID, session: session, made: at,
		target: pushTarget{
			endpoint: pending.Endpoint, p256dh: pending.P256dh, auth: pending.Auth, host: pending.Host, pending: true, nonce: pending.Nonce,
			lapses: time.UnixMilli(pending.At).Add(pushPendingLife),
		},
		payload: pushJSON(pushHelloPayload{V: pushPayloadVersion, Type: pushKindHello, User: userID, Nonce: pending.Nonce, At: at.UnixMilli()}),
	}
	r.later(r.helloDelay, func() {
		r.enqueue(job, false)
		r.pump(p)
	})
}

// queueTest sends a confirmed device the test push.
func (p *Plugin) queueTest(userID string, device pushDevice) {
	r := p.pushes()
	now := r.now()
	r.enqueue(&pushJob{
		kind: pushKindTest, user: userID, session: device.Session, made: now,
		target:  pushTarget{endpoint: device.Endpoint, p256dh: device.P256dh, auth: device.Auth, host: device.Host},
		suspect: device.Suspect != 0, trouble: device.Trouble,
		payload: pushJSON(pushBarePayload{V: pushPayloadVersion, Type: pushKindTest, User: userID, At: now.UnixMilli()}),
	}, false)
	r.pump(p)
}

// newPushNonce is a fresh 16-byte nonce, base64url.
func newPushNonce() (string, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(nonce), nil
}

// sweepPushes is the daily sweep, on one node: every person with a row
// (those in push_people, and those whose row the store holds without them
// being in it, who are put back while it has room), each device's session
// checked as the send path checks it, at most pushSweepPerSecond a second.
// What is over is dropped (with its ended push), what was never confirmed is
// cleared, a suspect mark whose session reads again is cleared, and a row
// last written pushRowRenewAfter ago with a session that stands is written
// again, so that it lives as long as its session.
func (p *Plugin) sweepPushes(ctx context.Context) {
	p.safely("the sweep of push registrations", func() {
		r := p.pushes()

		people, _, err := p.readPushPeople()
		if err != nil {
			p.API.LogWarn("The sweep of push registrations could not read who has one")
			return
		}

		everyone := make(map[string]bool, len(people.People))
		for userID := range people.People {
			everyone[userID] = true
		}
		for _, key := range p.kvKeysWithPrefix(kvPushUserPrefix) {
			everyone[strings.TrimPrefix(key, kvPushUserPrefix)] = true
		}

		pace := time.NewTicker(r.sweepPace)
		defer pace.Stop()

		for userID := range everyone {
			if ctx.Err() != nil {
				return
			}

			doc, err := p.updatePushDoc(userID, func(*pushDoc, time.Time) bool { return false })
			if err != nil {
				continue
			}
			if len(doc.Devices) == 0 {
				p.leavePushPeople(userID)
				continue
			}

			stands := false
			for _, device := range doc.Devices {
				select {
				case <-ctx.Done():
					return
				case <-pace.C:
				}

				if p.checkPushSession(userID, device.Session, device.Suspect != 0) {
					stands = true
				}
			}

			after, err := p.updatePushDoc(userID, func(doc *pushDoc, now time.Time) bool {
				return stands && now.Sub(time.UnixMilli(doc.Written)) >= pushRowRenewAfter
			})
			if err != nil || len(after.Devices) == 0 {
				continue
			}
			if _, in := people.People[userID]; !in && len(people.People) < pushPeopleKept {
				p.joinPushPeople(userID)
				people.People[userID] = r.now().UnixMilli()
			}
		}
	})
}
