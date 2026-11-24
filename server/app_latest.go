package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
)

// The latest release of Matty as the minimum: Minimum Matty version
// (MinimumAppVersion) set to "latest". While it is, and only then, this
// server asks GitHub for the newest release of the app: one GET of
// <api>/repos/vvilla-01/matty/releases/latest, through the same client, with
// the same headers, time and size limits as the look for the plugin's own
// release (update.go), of which only the tag, whether it is a draft or a
// prerelease, and the names of its files are read. The newest Matty is the
// tag vN.N.N of a release that is no draft and no prerelease and holds the
// file matty-N.N.N.apk.
//
// It looks once a day (its own cluster job, app_latest, which exists only
// while the setting is latest), once when a save makes the setting latest,
// once at a start with it latest while no version is known, and when an
// admin presses Check now on the app check's card. The daily look, the one
// at a start and the one at a save take one claim for the whole server
// (app_latest_claim), and Check now its own (app_latest_check_claim), so
// that several nodes make one look; none of them is the plugin's own job or
// claims, so neither look delays or skips the other. Look for updates has
// nothing to do with it.
//
// What a look found is kept for the whole server (app_latest_seen): the
// version, when the last look ended, and the last trouble with its time. A
// look that fails keeps the version seen before; a look that finds an older
// version takes it. A 404, or a draft or a prerelease, is no release: while
// no version was ever known nothing is known, and once one was, it is
// trouble and the version stays.
//
// Each node keeps a copy, which a reader of the minimum takes from the store
// again once it is five seconds old, and which the node also reads once a
// minute, so that every node answers a new minimum within seconds and ends
// the voice of a Matty below it without anybody asking. The node whose look
// changed the version tells every open client to read /me again, once.

const (
	// appLatestRepo is where Matty is released.
	appLatestRepo = "vvilla-01/matty"

	// appLatestJob names the daily look's cluster job.
	appLatestJob   = "app_latest"
	appLatestEvery = 24 * time.Hour

	kvAppLatestSeen       = "app_latest_seen"
	kvAppLatestClaim      = "app_latest_claim"
	kvAppLatestCheckClaim = "app_latest_check_claim"

	// appLatestFresh is how old a node's copy is before a reader takes it
	// from the store again; appLatestTickEvery, how often a node reads it
	// by itself.
	appLatestFresh     = 5 * time.Second
	appLatestTickEvery = time.Minute

	appLatestNotLatestText = "Minimum Matty version is not the latest release. Choose it above and save first."
	appLatestStoreText     = "This server could not read how the latest Matty stands just now. Try again in a moment."
	appLatestNoAppText     = "The newest release of Matty holds no app to install yet."
	appLatestNoneText      = "GitHub says Matty has no release."
	appLatestTagText       = "The newest release of Matty has a tag that is not a version such as v0.1.33."
)

// appLatestAddress is where a node asks for the newest Matty: GitHub's API
// in every release build, the stand-in beside it in the stand-in build.
func appLatestAddress(r *updateRuntime) string {
	return r.apiBase + "/repos/" + appLatestRepo + "/releases/latest"
}

// appLatestAssetName is the app a release of Matty holds.
func appLatestAssetName(version string) string {
	return "matty-" + version + ".apk"
}

// appLatestSeen is the last look, shared by every node (kvAppLatestSeen),
// and the admin card's "latest".
type appLatestSeen struct {
	Version   string `json:"version,omitempty"`
	LookedAt  int64  `json:"lookedAt,omitempty"`
	Trouble   string `json:"trouble,omitempty"`
	TroubleAt int64  `json:"troubleAt,omitempty"`
}

// ---------------------------------------------------------------- the runtime

type appLatestRuntime struct {
	mu sync.Mutex

	// This node's copy, and when it was last taken from the store or
	// written (or a read of it was tried).
	seen   appLatestSeen
	readAt time.Time

	// What tests replace.
	fresh     time.Duration
	tickEvery time.Duration
	schedule  func(api cluster.JobPluginAPI, run func()) (clipsJob, error)

	// The life of this node's part.
	started bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// While the setting is latest: the daily job and the minute's read,
	// which onCancel ends. onLock keeps one change of them at a time.
	onLock   sync.Mutex
	on       bool
	onCancel context.CancelFunc
	job      clipsJob

	// One look at a time on this node; whatever replaces the copy takes it.
	lookLock sync.Mutex
}

func newAppLatestRuntime() *appLatestRuntime {
	return &appLatestRuntime{
		fresh:     appLatestFresh,
		tickEvery: appLatestTickEvery,
		schedule: func(api cluster.JobPluginAPI, run func()) (clipsJob, error) {
			job, err := cluster.Schedule(api, appLatestJob, cluster.MakeWaitForInterval(appLatestEvery), run)
			if err != nil {
				return nil, err
			}

			return job, nil
		},
	}
}

func (p *Plugin) appLatest() *appLatestRuntime {
	p.appLatestOnce.Do(func() { p.appLatestState = newAppLatestRuntime() })

	return p.appLatestState
}

func (r *appLatestRuntime) copyNow() appLatestSeen {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.seen
}

// take makes seen this node's copy, read at at, and is whether its version
// changed.
func (r *appLatestRuntime) take(seen appLatestSeen, at time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	moved := r.seen.Version != seen.Version
	r.seen, r.readAt = seen, at

	return moved
}

func (r *appLatestRuntime) onNow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.on
}

// spawn runs f on a goroutine stopAppLatest waits for, unless this node's
// part has stopped.
func (r *appLatestRuntime) spawn(f func(ctx context.Context)) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started {
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

// ---------------------------------------------------------------- the copy

// readAppLatest is the last look as the store has it, an empty one when
// nothing was ever kept, and false when the store did not answer or holds
// something that cannot be read.
func (p *Plugin) readAppLatest() (appLatestSeen, bool) {
	raw, appErr := p.API.KVGet(kvAppLatestSeen)
	if appErr != nil {
		return appLatestSeen{}, false
	}

	var seen appLatestSeen
	if len(raw) == 0 {
		return seen, true
	}
	if json.Unmarshal(raw, &seen) != nil {
		return appLatestSeen{}, false
	}
	if !updateVersionRE.MatchString(seen.Version) {
		seen.Version = ""
	}

	return seen, true
}

// refreshAppLatestLocked takes the copy from the store, with lookLock held,
// and is whether its version changed. A store that cannot be read leaves
// the copy as it is, to be tried again five seconds on.
func (p *Plugin) refreshAppLatestLocked() bool {
	r := p.appLatest()
	now := p.updates().now()

	seen, ok := p.readAppLatest()
	if !ok {
		r.mu.Lock()
		r.readAt = now
		r.mu.Unlock()
		return false
	}

	return r.take(seen, now)
}

// appLatestNow is this node's copy, taken from the store again first when
// it is five seconds old. A reader never waits for a look under way on
// this node: it takes the copy as it is, which the look then replaces.
func (p *Plugin) appLatestNow() appLatestSeen {
	r := p.appLatest()
	now := p.updates().now()

	r.mu.Lock()
	due := r.readAt.IsZero() || now.Sub(r.readAt) >= r.fresh || now.Before(r.readAt)
	r.mu.Unlock()

	if due && r.lookLock.TryLock() {
		moved := p.refreshAppLatestLocked()
		r.lookLock.Unlock()
		if moved {
			p.appLatestMoved()
		}
	}

	return r.copyNow()
}

// appLatestMoved is this node's part when the latest it knows changed
// without a save: the voice of every Matty here that no longer meets the
// minimum in force ends, on its own goroutine, with that minimum as it then
// is.
func (p *Plugin) appLatestMoved() {
	if !p.getConfiguration().appMinimumLatest() {
		return
	}

	p.goSafely("ending the voice of a Matty below the latest release", func() {
		if minimum := p.appMinimumInForce(); minimum != "" {
			p.voiceTalk().endOutdated(minimum)
		}
	})
}

// appLatestTick is the minute's work on every node while the setting is
// latest: the copy read from the store, so that a new minimum ends voice
// here without anybody asking.
func (p *Plugin) appLatestTick() {
	if !p.getConfiguration().appMinimumLatest() {
		return
	}

	r := p.appLatest()
	r.lookLock.Lock()
	moved := p.refreshAppLatestLocked()
	r.lookLock.Unlock()
	if moved {
		p.appLatestMoved()
	}
}

// ---------------------------------------------------------------- the life

// startAppLatest is the usual start's: while the setting is latest, the
// copy as kept, the daily job and the minute's read, and a look when no
// version is known yet. Otherwise nothing is asked and the store is not
// read.
func (p *Plugin) startAppLatest() {
	p.stopAppLatest()

	r := p.appLatest()
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	r.started, r.ctx, r.cancel = true, ctx, cancel
	r.mu.Unlock()

	if !p.getConfiguration().appMinimumLatest() {
		return
	}

	r.lookLock.Lock()
	p.refreshAppLatestLocked()
	r.lookLock.Unlock()

	p.setAppLatestOn(true)
	if r.copyNow().Version == "" {
		r.spawn(func(ctx context.Context) {
			p.safely("looking for the newest Matty", func() { p.lookForAppLatestClaimed(ctx) })
		})
	}
}

// stopAppLatest is OnDeactivate's: the job and the minute's read stop, and
// a look under way is waited for five seconds at most.
func (p *Plugin) stopAppLatest() {
	r := p.appLatest()

	r.onLock.Lock()
	r.mu.Lock()
	if !r.started {
		r.mu.Unlock()
		r.onLock.Unlock()
		return
	}
	r.started = false
	r.cancel()
	job := r.job
	r.on, r.job, r.onCancel = false, nil, nil
	r.mu.Unlock()
	r.onLock.Unlock()

	if job != nil {
		_ = job.Close()
	}

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		p.API.LogWarn("Stopped waiting for the look for the newest Matty to stop")
	}
}

// setAppLatestOn starts the daily job and the minute's read when on, and
// stops them when off. Nothing of either exists while the setting is not
// latest.
func (p *Plugin) setAppLatestOn(on bool) {
	r := p.appLatest()
	r.onLock.Lock()
	defer r.onLock.Unlock()

	r.mu.Lock()
	if !r.started || r.on == on {
		r.mu.Unlock()
		return
	}
	r.on = on
	if !on {
		job, stop := r.job, r.onCancel
		r.job, r.onCancel = nil, nil
		r.mu.Unlock()

		if stop != nil {
			stop()
		}
		if job != nil {
			_ = job.Close()
		}
		return
	}
	onCtx, onCancel := context.WithCancel(r.ctx)
	r.onCancel = onCancel
	r.mu.Unlock()

	r.spawn(func(context.Context) { p.appLatestTicks(onCtx) })

	// The cluster job holds its lock while its callback runs, so the look
	// runs on a goroutine of its own and the callback returns at once. A
	// job that never ran runs at once: it shares one claim with the look at
	// a start and at a save, so only one of them looks.
	job, err := r.schedule(p.API, func() {
		r.spawn(func(context.Context) {
			p.safely("the daily look for the newest Matty", func() { p.lookForAppLatestClaimed(onCtx) })
		})
	})
	if err != nil {
		p.API.LogWarn("Could not schedule the daily look for the newest Matty", "error", err.Error())
	}
	r.mu.Lock()
	if r.on && r.onCancel != nil {
		r.job = job
		job = nil
	}
	r.mu.Unlock()
	if job != nil {
		_ = job.Close()
	}
}

func (p *Plugin) appLatestTicks(ctx context.Context) {
	r := p.appLatest()
	// A tick holds onLock, so that switching the setting off or a stop
	// returns only once no tick is under way.
	tick := func() {
		r.onLock.Lock()
		defer r.onLock.Unlock()
		if ctx.Err() == nil {
			p.appLatestTick()
		}
	}
	for {
		timer := time.NewTimer(r.tickEvery)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		p.safely("the minute's look at the newest Matty", tick)
	}
}

// appLatestChanged is OnConfigurationChange's, once the plugin is up: the
// job and the minute's read follow the setting, and when a save makes it
// latest, one node looks at once.
func (p *Plugin) appLatestChanged(previous, cfg *configuration) {
	if previous == nil {
		return
	}
	p.setAppLatestOn(cfg.appMinimumLatest())
	if previous.appMinimumLatest() || !cfg.appMinimumLatest() {
		return
	}

	p.appLatest().spawn(func(ctx context.Context) {
		p.safely("looking for the newest Matty", func() { p.lookForAppLatestClaimed(ctx) })
	})
}

// ---------------------------------------------------------------- looking

// lookForAppLatestClaimed is the daily look, the look at a start and the
// look at a save: by the node that claims it, at most once a minute for the
// whole server. Only a claim another node holds skips it: when the store
// does not answer, the node says so in the log and looks anyway.
func (p *Plugin) lookForAppLatestClaimed(ctx context.Context) {
	if !p.getConfiguration().appMinimumLatest() {
		return
	}

	claimed, _, err := p.claimUpdateWindow(kvAppLatestClaim)
	if err != nil {
		p.API.LogWarn("Could not claim the look for the newest Matty", "kind", "store")
		claimed = true
	}
	if claimed {
		p.lookForAppLatest(ctx)
	}
}

// lookForAppLatest is one look, while the setting is latest.
func (p *Plugin) lookForAppLatest(ctx context.Context) {
	if !p.getConfiguration().appMinimumLatest() {
		return
	}

	p.appLatestLook(ctx)
}

// appLatestLook asks, and keeps what it found for every node. It reads the
// kept look from the store under the look's lock just before it writes, and
// a change is a change of the version against that: then, once the write
// is done, every open client is told. The voice of a Matty below it ends
// when this node's copy changed. A look that was stopped is no look, and
// neither is one whose write the store refused.
func (p *Plugin) appLatestLook(ctx context.Context) {
	r := p.appLatest()
	now := p.updates().now()

	r.lookLock.Lock()
	answer, done := p.askAppLatest(ctx)
	if !done {
		r.lookLock.Unlock()
		return
	}
	stored, ok := p.readAppLatest()
	if !ok {
		stored = r.copyNow()
	}
	next := answer.applied(stored, now)
	raw, err := json.Marshal(next)
	written := err == nil
	if written {
		if appErr := p.API.KVSet(kvAppLatestSeen, raw); appErr != nil {
			p.API.LogWarn("Could not keep what the look for the newest Matty found", "error", appErr.Error())
			written = false
		}
	}
	moved := written && r.take(next, now)
	r.lookLock.Unlock()

	if moved {
		p.appLatestMoved()
	}
	if written && stored.Version != next.Version {
		p.tellEverybodyMe()
	}
}

// appLatestAnswer is what one look found: a version, no release, or what
// went wrong.
type appLatestAnswer struct {
	version string
	none    bool
	trouble string
}

// applied is the kept look once this answer is taken into it.
func (a appLatestAnswer) applied(kept appLatestSeen, now time.Time) appLatestSeen {
	next := kept
	next.LookedAt = now.UnixMilli()

	switch {
	case a.version != "":
		next.Version, next.Trouble, next.TroubleAt = a.version, "", 0
	case a.none && kept.Version == "":
		next.Trouble, next.TroubleAt = "", 0
	case a.none:
		next.Trouble, next.TroubleAt = appLatestNoneText, now.UnixMilli()
	default:
		next.Trouble, next.TroubleAt = a.trouble, now.UnixMilli()
	}

	return next
}

// askAppLatest is the request and its answer. Only the failure's kind goes
// to the log; the trouble's words go on the card. done is false for a look
// that was stopped (not timed out).
func (p *Plugin) askAppLatest(parent context.Context) (appLatestAnswer, bool) {
	u := p.updates()
	trouble := func(words string) (appLatestAnswer, bool) { return appLatestAnswer{trouble: words}, true }

	ctx, cancel := context.WithTimeout(parent, u.lookTimeout)
	defer cancel()
	stopped := func() bool { return errors.Is(ctx.Err(), context.Canceled) }

	response, err := u.get(ctx, u.lookClient(), appLatestAddress(u), "application/vnd.github+json")
	if err != nil {
		if stopped() {
			return appLatestAnswer{}, false
		}
		kind := requestFailureKind(err)
		p.API.LogWarn("Could not reach GitHub to look for the newest Matty", "kind", kind)
		if kind == "timeout" {
			return trouble("GitHub did not answer within 15 seconds.")
		}
		return trouble("GitHub could not be reached.")
	}
	defer response.Body.Close()

	switch {
	case response.StatusCode == http.StatusNotFound:
		return appLatestAnswer{none: true}, true
	case response.StatusCode != http.StatusOK:
		return trouble(fmt.Sprintf("GitHub answered %d.", response.StatusCode))
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, updateLookMaxBytes+1))
	if err != nil {
		if stopped() {
			return appLatestAnswer{}, false
		}
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			p.API.LogWarn("Could not reach GitHub to look for the newest Matty", "kind", "timeout")
			return trouble("GitHub did not answer within 15 seconds.")
		}
		return trouble("GitHub's answer could not be read.")
	}
	if len(body) > updateLookMaxBytes {
		return trouble("GitHub's answer was larger than 256 KB.")
	}

	var answer struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return trouble("GitHub's answer could not be read.")
	}
	if answer.Draft || answer.Prerelease {
		return appLatestAnswer{none: true}, true
	}
	match := updateTagRE.FindStringSubmatch(answer.TagName)
	if match == nil {
		return trouble(appLatestTagText)
	}
	for _, file := range answer.Assets {
		if file.Name == appLatestAssetName(match[1]) {
			return appLatestAnswer{version: match[1]}, true
		}
	}

	return trouble(appLatestNoAppText)
}

// ---------------------------------------------------------------- Check now

// handleAdminAppLatestCheck answers POST /api/v1/admin/appcheck/latest, the
// app check card's Check now: a look now, at most once a minute for the
// whole server, and only while the setting is latest. The look runs on this
// node's own context with its own 15 seconds, so a browser that leaves does
// not cut it short. It answers with the card as it then stands.
func (p *Plugin) handleAdminAppLatestCheck(w http.ResponseWriter, r *http.Request, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, appCheckAdminOnlyText)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	if !p.getConfiguration().appMinimumLatest() {
		writeCodedError(w, http.StatusConflict, appLatestNotLatestText, codeConflict)
		return
	}
	claimed, left, err := p.claimUpdateWindow(kvAppLatestCheckClaim)
	if err != nil {
		writeCodedError(w, http.StatusServiceUnavailable, appLatestStoreText, accessCodeStore)
		return
	}
	if !claimed {
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: updateTooSoonText, Code: codeRateLimited, RetryAfterMs: max(left.Milliseconds(), 1000)})
		return
	}

	latest := p.appLatest()
	latest.mu.Lock()
	ctx := latest.ctx
	latest.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	p.appLatestLook(ctx)

	p.writeAppCheckAdmin(w)
}
