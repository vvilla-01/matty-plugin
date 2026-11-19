package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Every grace counted in polls — the live badge, What's on?, a Clips
// recording, which card stands for somebody also live on Twitch, the failed
// reads a panel rides out — is meant as that many intervals. The poller also
// polls whenever somebody's click asks it to, and whenever a settings save
// restarts it, and those polls are no time at all. They used to count: a
// stream that missed one tick and a refresh a moment later lost its badge,
// its What's on? and its recording while OBS was still reconnecting.

// graceWorld is Dave streaming on a server with everything an OBS reconnect
// could take from him switched on: the live badge, What's on? and Clips. It
// is polled through the poller, against a Broadcast Box that lists his stream
// while it is on the air and answers nothing while it is down.
type graceWorld struct {
	*recordingWorld

	mu       sync.Mutex
	onAir    bool
	down     bool
	statuses map[string]string // the emoji each person is wearing
}

func newGraceWorld(t *testing.T) *graceWorld {
	t.Helper()

	g := &graceWorld{onAir: true, statuses: map[string]string{}}

	started := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		onAir, down := g.onAir, g.down
		g.mu.Unlock()

		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		if down {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}

		listing := []bbStream{}
		if onAir {
			dave := rawLive("videokey-dave")
			dave.StreamStart = started
			listing = append(listing, dave)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(listing)
	}))
	t.Cleanup(origin.Close)

	g.recordingWorld = newRecordingWorld(t, "uid-dave")
	g.setNow(time.Time{})
	g.p.httpClient = origin.Client()
	g.p.offlineStreak = map[string]int{}

	cfg := g.p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = origin.URL
	cfg.OfflineGraceChecks = 2
	cfg.EnableLiveBadge = true
	g.p.setConfiguration(cfg)

	// What's on Dave's stream, said before it is on the air.
	g.kvSet(kvNowPlaying, mustJSON(t, nowPlayingDoc{Version: nowPlayingVersion, Entries: map[string]nowPlayingEntry{
		"uid-dave": {Category: categoryGame, Title: "Hytale", SetAt: time.Now().UnixMilli()},
	}}))

	// The whole store, so the badge's own records are found again.
	g.api.On("KVList", mock.Anything, mock.Anything).Return(func(page, perPage int) ([]string, *model.AppError) {
		g.faults.lock.Lock()
		defer g.faults.lock.Unlock()

		keys := make([]string, 0, len(g.store))
		for key := range g.store {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		return keys[min(page*perPage, len(keys)):min((page+1)*perPage, len(keys))], nil
	}).Maybe()
	g.api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	g.api.On("UpdateUserCustomStatus", mock.Anything, mock.Anything).Return(func(userID string, status *model.CustomStatus) *model.AppError {
		g.mu.Lock()
		defer g.mu.Unlock()

		g.statuses[userID] = status.Emoji

		return nil
	}).Maybe()
	g.api.On("RemoveUserCustomStatus", mock.Anything).Return(func(userID string) *model.AppError {
		g.mu.Lock()
		defer g.mu.Unlock()

		delete(g.statuses, userID)

		return nil
	}).Maybe()

	// Something published, so that polled can tell when the next poll has.
	g.p.setSnapshot(&snapshot{Streams: []apiStream{}})

	return g
}

// tick is a poll on the poller's clock; refresh is one a click asked for.
func (g *graceWorld) tick(t *testing.T)    { g.p.pollOnce(t.Context(), true) }
func (g *graceWorld) refresh(t *testing.T) { g.p.pollOnce(t.Context(), false) }

// polled has the running poller poll, by trigger, and waits until that poll
// has published, which every successful poll does last.
func (g *graceWorld) polled(t *testing.T, trigger func()) {
	t.Helper()

	before := g.p.getSnapshot()
	trigger()
	require.Eventually(t, func() bool { return g.p.getSnapshot() != before }, 10*time.Second, 2*time.Millisecond)
}

func (g *graceWorld) set(onAir, down bool) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.onAir, g.down = onAir, down
}

func (g *graceWorld) wearing(userID string) string {
	g.mu.Lock()
	defer g.mu.Unlock()

	return g.statuses[userID]
}

func (g *graceWorld) whatsOn(t *testing.T) map[string]nowPlayingEntry {
	t.Helper()

	doc, ok := decodeNowPlaying(g.kvGet(kvNowPlaying))
	require.True(t, ok)

	return doc.Entries
}

// requireOnAir is Dave as the first poll of his stream leaves him: wearing
// the badge, his What's on? aired, and recorded.
func (g *graceWorld) requireOnAir(t *testing.T) {
	t.Helper()

	require.Equal(t, "red_circle", g.wearing("uid-dave"))
	require.True(t, g.whatsOn(t)["uid-dave"].Aired)
	require.Equal(t, 1, g.recorder(t, "videokey-dave").look().starts)
}

// requireKept is everything an OBS reconnect must not take from him.
func (g *graceWorld) requireKept(t *testing.T, why string) {
	t.Helper()

	require.Equal(t, "red_circle", g.wearing("uid-dave"), "the badge: %s", why)
	require.Contains(t, g.whatsOn(t), "uid-dave", "What's on?: %s", why)
	require.Zero(t, g.recorder(t, "videokey-dave").look().stops, "the recording: %s", why)
}

// requireGone is the stream over, as the grace has always ended it.
func (g *graceWorld) requireGone(t *testing.T) {
	t.Helper()

	require.Empty(t, g.wearing("uid-dave"), "the badge comes off")
	require.NotContains(t, g.whatsOn(t), "uid-dave", "What's on? is taken down")
	look := g.recorder(t, "videokey-dave").look()
	require.Equal(t, 1, look.stops, "the recording stops")
	require.True(t, look.keptEnd, "with its end kept")
}

// A stream that misses one tick is inside the grace, and the refreshes that
// come in the meantime — somebody saving What's on?, a plan, a profile — are
// no more time off the air: the badge, What's on? and the recording all stay.
// The tick after that is the second check, and everything goes as it always
// has.
func TestOneMissedTickAndAnyNumberOfRefreshesTakeNothingDown(t *testing.T) {
	g := newGraceWorld(t)

	g.tick(t)
	g.requireOnAir(t)

	g.set(false, false)
	g.tick(t)
	g.requireKept(t, "one missed tick is inside the grace")

	for range 4 {
		g.refresh(t)
	}
	g.requireKept(t, "a refresh is not a check")

	g.tick(t)
	g.requireGone(t)
}

// The auditor's case, through the poller itself: a stream leaves the listing,
// and before the next tick clicks ask for polls (refreshSoon) and settings
// saves restart the poller, which Mattermost does for a change to any of the
// server's settings. None of them is a check.
func TestRefreshesAndSettingsSavesAreNotChecks(t *testing.T) {
	g := newGraceWorld(t)
	g.p.refreshCh = make(chan struct{}, 1)

	// No tick of its own while the test runs, but the first poll.
	cfg := g.p.getConfiguration().Clone()
	cfg.PollIntervalSeconds = 3600
	g.p.setConfiguration(cfg)
	t.Cleanup(g.p.stopPoller)

	// The plugin starting: its first poll is a tick.
	g.polled(t, g.p.restartPoller)
	g.requireOnAir(t)

	g.set(false, false)
	for range 3 {
		g.polled(t, g.p.refreshSoon)
	}
	g.requireKept(t, "refreshes are not checks")

	for range 2 {
		g.polled(t, g.p.restartPoller)
	}
	g.requireKept(t, "nor are settings saves")
}

// A poller a settings save started does count its first poll when a whole
// interval has gone by since the last tick, as when the plugin starts: the
// clock owes a check by then, and the new poller's own first tick is an
// interval away.
func TestTheFirstPollAfterAQuietIntervalIsATick(t *testing.T) {
	g := newGraceWorld(t)

	cfg := g.p.getConfiguration().Clone()
	cfg.PollIntervalSeconds = 3600
	g.p.setConfiguration(cfg)
	t.Cleanup(g.p.stopPoller)

	g.polled(t, g.p.restartPoller)
	g.requireOnAir(t)

	g.set(false, false)
	g.tick(t)
	g.requireKept(t, "one missed tick is inside the grace")

	g.p.pollerTickedAt.Store(time.Now().Add(-time.Hour).UnixMilli())
	g.polled(t, g.p.restartPoller)
	g.requireGone(t)
}

// Broadcast Box out of reach keeps the last list on every panel for
// pollFailuresBeforeEmpty failed ticks. A refresh that fails in the meantime
// shows the error like any other, but is not one of them, so it never
// empties everybody's panel sooner.
func TestRefreshesDuringAnOutageDoNotEmptyThePanelSooner(t *testing.T) {
	g := newGraceWorld(t)
	dave := g.handle("videokey-dave")

	g.tick(t)
	require.True(t, hasCard(g.p.getSnapshot().Streams, dave))

	g.set(true, true)
	g.tick(t)
	for refresh := 1; refresh <= 2*pollFailuresBeforeEmpty; refresh++ {
		g.refresh(t)

		shown := g.p.getSnapshot()
		require.NotEmpty(t, shown.Error)
		require.True(t, hasCard(shown.Streams, dave), "refresh %d is not a failed check", refresh)
	}

	for failure := 2; failure <= pollFailuresBeforeEmpty; failure++ {
		g.tick(t)
		require.True(t, hasCard(g.p.getSnapshot().Streams, dave), "failed tick %d", failure)
	}

	g.tick(t)
	require.False(t, hasCard(g.p.getSnapshot().Streams, dave), "emptied on the failed tick after the last one ridden out")
}

// A failed poll carries the airtime boards with how many people each one
// has, so the boards read the same across a hiccup and boards.version does
// not move for it.
func TestAFailedPollCarriesTheBoardsWithTheirTotals(t *testing.T) {
	g := newGraceWorld(t)
	g.tick(t)

	held := *g.p.getSnapshot()
	held.AllTime = []leaderEntry{{Rank: 1, Title: "Dave", Seconds: 7200, UserID: "uid-dave"}}
	held.Month = []leaderEntry{{Rank: 1, Title: "Dave", Seconds: 3600, UserID: "uid-dave"}}
	held.AllTimeTotal, held.MonthTotal, held.MonthLabel = 12, 5, "November 2026"
	g.p.publish(&held)
	before := g.p.getSnapshot().Boards
	require.NotNil(t, before)

	g.set(true, true)
	g.tick(t)

	shown := g.p.getSnapshot()
	require.NotEmpty(t, shown.Error)
	require.Equal(t, held.AllTime, shown.AllTime)
	require.Equal(t, 12, shown.AllTimeTotal)
	require.Equal(t, 5, shown.MonthTotal)
	require.Equal(t, before, shown.Boards, "the boards' version does not move for a hiccup")
}

// A refresh may not count a miss, but it is evidence all the same: one that
// lists the stream again starts its grace afresh, so the next missed tick is
// the first again, not the second.
func TestARefreshThatSeesTheStreamAgainStartsTheGraceAfresh(t *testing.T) {
	g := newGraceWorld(t)

	g.tick(t)
	g.requireOnAir(t)

	g.set(false, false)
	g.tick(t)
	g.refresh(t)
	g.requireKept(t, "one missed tick")

	// Back, and seen by a refresh.
	g.set(true, false)
	g.refresh(t)

	g.set(false, false)
	g.tick(t)
	g.requireKept(t, "one missed tick since the refresh that saw it")

	g.tick(t)
	g.requireGone(t)
}

// Somebody live on Twitch and Broadcast Box at once is one card after
// OfflineGraceChecks ticks of evidence, and two again after as many the other
// way. Refreshes in between bring neither swap closer.
func TestRefreshesNeverSwapATwitchCard(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})
	s.fake.goLive(daveTwitch, "Live", "Chess", 1, s.now())
	s.step(t)

	twitch := s.p.twitchStreams(s.p.getConfiguration(), s.now())
	twitchHandle := twitch[0].Handle
	bbHandle := s.p.streamHandle("videokey-dave")
	card := apiStream{StreamKey: "videokey-dave", Handle: bbHandle, Title: "Dave", UserID: twDave, LinkSource: linkSourceManual}
	paused := card
	paused.Paused = true

	coveredBy := func(ticked bool, bb ...apiStream) string {
		return cardOf(t, s.p.mergeStreams(bb, twitch, ticked), twitchHandle).CoveredBy
	}

	// Live on Twitch first, then on Broadcast Box too.
	require.Empty(t, coveredBy(true))
	require.Empty(t, coveredBy(true, card), "the first tick of the simulcast")
	for range 3 {
		require.Empty(t, coveredBy(false, card), "a refresh is not a tick")
	}
	require.Equal(t, bbHandle, coveredBy(true, card), "the second tick")

	// Paused on Broadcast Box: still one card until the grace has run out.
	require.Equal(t, bbHandle, coveredBy(true, paused), "the first tick paused")
	for range 3 {
		require.Equal(t, bbHandle, coveredBy(false, paused), "a refresh is not a tick")
	}
	require.Empty(t, coveredBy(true, paused), "the second tick paused")
}
