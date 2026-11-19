package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The people in these tests. Bob, Erin and Dave stream and have linked their
// streams; Caro has linked one that is not on the air; Alice has never linked
// anything, and so must never be counted, remembered or written down.
var watchTimeAccounts = map[string]*model.User{
	"uid-bob":   {Id: "uid-bob", Username: "bob", Nickname: "Bob"},
	"uid-erin":  {Id: "uid-erin", Username: "erin", FirstName: "Erin", LastName: "Cat"},
	"uid-dave":  {Id: "uid-dave", Username: "dave"},
	"uid-caro":  {Id: "uid-caro", Username: "caro", Nickname: "Caro"},
	"uid-alice": {Id: "uid-alice", Username: "alice"},
}

var watchTimeLinks = map[string]string{
	"uid-bob":  "videokey-bob",
	"uid-erin": "videokey-erin",
	"uid-dave": "videokey-dave",
	"uid-caro": "videokey-caro",
}

// A plugin with the Top Watchers board on and faces off, over a real
// in-memory KV store that a test can make misbehave, with everybody above
// linked as listed.
func watchTimePlugin(t *testing.T) (*Plugin, map[string][]byte, *kvFaults) {
	t.Helper()

	p, store, faults := kvBackedPluginWithFaults(map[string][]byte{})
	linkEverybody(t, p, store)

	cfg := &configuration{AlwaysOnStreamKeys: "videokey-watts", TopWatchers: true}
	cfg.normalize()
	p.setConfiguration(cfg)

	return p, store, faults
}

// linkEverybody writes the links as setLink leaves them, and gives the mock
// the accounts. Also how a restart is set up: a new process over the same KV.
func linkEverybody(t *testing.T, p *Plugin, store map[string][]byte) {
	t.Helper()

	for userID, key := range watchTimeLinks {
		store[kvLinkByUserPrefix+userID] = []byte(key)
		store[kvLinkByKeyPrefix+key] = []byte(userID)
	}

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	for userID, user := range watchTimeAccounts {
		api.On("GetUser", userID).Return(user, nil)
	}
	api.On("GetUser", mock.Anything).Return(nil, &model.AppError{Message: "not found", StatusCode: http.StatusNotFound})
}

// watchPass is the board's half of one poll at a chosen instant: the listing
// decorated, then credited, in pollOnce's order.
func watchPass(p *Plugin, raw []bbStream, at time.Time) {
	cfg := p.getConfiguration()
	p.recordWatchTime(raw, p.decorate(raw, cfg), cfg, at)
}

// watched is how many seconds a table holds for userID, or -1 when the table
// cannot be read.
func watched(p *Plugin, table, userID string) int64 {
	stored, err := p.loadStats(table)
	if err != nil {
		return -1
	}

	return stored[userID].Seconds
}

func pausedRaw(key string, sessionIDs ...string) bbStream {
	stream := rawWatched(key, sessionIDs...)
	paused := true
	stream.Paused = &paused

	return stream
}

// No watching table was ever written.
func requireNoWatchDocuments(t *testing.T, store map[string][]byte) {
	t.Helper()

	for key := range store {
		require.False(t, strings.HasPrefix(key, "watch_"), key)
	}
}

// A moment to measure from: whole seconds, in the present, so the watcher
// store's own clock and the passes' agree about what is recent.
func watchEpoch() time.Time {
	return time.Now().UTC().Truncate(time.Second)
}

func TestOnlyLinkedPeopleAreCountedAndNobodyElseIsRemembered(t *testing.T) {
	p, store, _ := watchTimePlugin(t)
	start := watchEpoch()
	dave := p.streamHandle("videokey-dave")

	// The plugin's first pass, with nobody watching yet. A first pass only
	// looks: there is no pass before it to measure from.
	watchPass(p, []bbStream{rawWatched("videokey-dave")}, start)

	p.watchers().record("bob-1", "uid-bob", dave, start.Add(4*time.Second))
	p.watchers().record("alice-1", "uid-alice", dave, start.Add(4*time.Second))
	raw := []bbStream{rawWatched("videokey-dave", "bob-1", "alice-1")}

	watchPass(p, raw, start.Add(10*time.Second))
	require.EqualValues(t, 6, watched(p, kvWatchTotals, "uid-bob"), "from when they pressed play")

	watchPass(p, raw, start.Add(20*time.Second))
	require.EqualValues(t, 16, watched(p, kvWatchTotals, "uid-bob"))
	require.EqualValues(t, 16, watched(p, watchMonthBucket(start), "uid-bob"))

	totals := mustLoad(t, p, kvWatchTotals)
	require.NotContains(t, totals, "uid-alice", "never linked, so never written down")

	// And with faces off, not even remembered: the first pass that looked
	// their link up let them go.
	require.Equal(t, map[string]string{"bob-1": "uid-bob"}, rememberedSessions(p.watchers()))
	require.NotContains(t, string(store[kvWatchTotals]), "alice")
}

func TestOnlyTimeWatchedWhileLinkedCounts(t *testing.T) {
	p, store, _ := watchTimePlugin(t)
	start := watchEpoch()
	dave := p.streamHandle("videokey-dave")

	cfg := p.getConfiguration().Clone()
	cfg.ShowWatchers = true
	p.setConfiguration(cfg)

	p.watchers().record("erin-1", "uid-erin", dave, start)
	raw := []bbStream{rawWatched("videokey-dave", "erin-1")}

	watchPass(p, raw, start)
	watchPass(p, raw, start.Add(10*time.Second))
	require.EqualValues(t, 10, watched(p, kvWatchTotals, "uid-erin"))

	// Unlinked: still watching, still a face, and paid nothing more.
	delete(store, kvLinkByUserPrefix+"uid-erin")
	watchPass(p, raw, start.Add(20*time.Second))
	watchPass(p, raw, start.Add(30*time.Second))
	require.EqualValues(t, 10, watched(p, kvWatchTotals, "uid-erin"))
	require.Contains(t, rememberedSessions(p.watchers()), "erin-1", "faces are on, so the session is still somebody's face")

	// Linked again: counted again from the next pass, and not for the time
	// in between.
	store[kvLinkByUserPrefix+"uid-erin"] = []byte("videokey-erin")
	watchPass(p, raw, start.Add(40*time.Second))
	require.EqualValues(t, 20, watched(p, kvWatchTotals, "uid-erin"))
}

// By the clock, per person: four tiles, or the same stream in two tabs, are
// one hour an hour.
func TestWatchingSeveralStreamsOrTabsCountsOnce(t *testing.T) {
	p, _, _ := watchTimePlugin(t)
	start := watchEpoch()
	store := p.watchers()

	watchPass(p, nil, start)

	// Caro opens Bob's stream, then Dave's in two tabs, then Erin's: from the
	// first of those, one clock.
	store.record("caro-bob", "uid-caro", p.streamHandle("videokey-bob"), start.Add(time.Second))
	store.record("caro-dave-tab-1", "uid-caro", p.streamHandle("videokey-dave"), start.Add(2*time.Second))
	store.record("caro-dave-tab-2", "uid-caro", p.streamHandle("videokey-dave"), start.Add(3*time.Second))
	store.record("caro-erin", "uid-caro", p.streamHandle("videokey-erin"), start.Add(4*time.Second))
	raw := []bbStream{
		rawWatched("videokey-dave", "caro-dave-tab-1", "caro-dave-tab-2"),
		rawWatched("videokey-bob", "caro-bob"),
		rawWatched("videokey-erin", "caro-erin"),
	}

	for at := 5; at <= 65; at += 10 {
		watchPass(p, raw, start.Add(time.Duration(at)*time.Second))
	}

	require.EqualValues(t, 64, watched(p, kvWatchTotals, "uid-caro"), "sixty-four seconds by the clock, not four times that")
	require.EqualValues(t, 64, watched(p, watchMonthBucket(start), "uid-caro"))
}

func TestWatchingYourOwnStreamDoesNotCount(t *testing.T) {
	p, _, _ := watchTimePlugin(t)
	start := watchEpoch()
	store := p.watchers()

	// Dave watching himself, and Bob watching Dave.
	store.record("dave-self", "uid-dave", p.streamHandle("videokey-dave"), start)
	store.record("bob-dave", "uid-bob", p.streamHandle("videokey-dave"), start)
	raw := []bbStream{rawWatched("videokey-dave", "dave-self", "bob-dave")}

	watchPass(p, raw, start)
	watchPass(p, raw, start.Add(10*time.Second))

	totals := mustLoad(t, p, kvWatchTotals)
	require.NotContains(t, totals, "uid-dave")
	require.EqualValues(t, 10, totals["uid-bob"].Seconds)

	// Own by the link alone, too: a stream the poll could not put a name on
	// is still the stream of whoever linked its key.
	p2, _, _ := watchTimePlugin(t)
	p2.watchers().record("dave-self", "uid-dave", p2.streamHandle("videokey-dave"), start)
	streams := p2.decorate(raw, p2.getConfiguration())
	for i := range streams {
		streams[i].UserID = ""
	}
	p2.recordWatchTime(raw, streams, p2.getConfiguration(), start)
	p2.recordWatchTime(raw, streams, p2.getConfiguration(), start.Add(10*time.Second))
	require.Zero(t, watched(p2, kvWatchTotals, "uid-dave"))
}

func TestTwentyFourSevenAndPausedStreamsDoNotCount(t *testing.T) {
	p, store, faults := watchTimePlugin(t)
	start := watchEpoch()
	watchers := p.watchers()

	watchers.record("bob-watts", "uid-bob", p.streamHandle("videokey-watts"), start)
	watchers.record("erin-dave", "uid-erin", p.streamHandle("videokey-dave"), start)
	raw := []bbStream{rawWatched("videokey-watts", "bob-watts"), pausedRaw("videokey-dave", "erin-dave")}

	watchPass(p, raw, start)
	watchPass(p, raw, start.Add(10*time.Second))
	watchPass(p, raw, start.Add(20*time.Second))

	requireNoWatchDocuments(t, store)
	require.Zero(t, faults.writes[kvWatchTotals], "a pass with nobody to pay writes nothing")

	// Unpaused: paid from the pass before, not for the pause.
	watchPass(p, []bbStream{rawWatched("videokey-watts", "bob-watts"), rawWatched("videokey-dave", "erin-dave")}, start.Add(30*time.Second))
	require.EqualValues(t, 10, watched(p, kvWatchTotals, "uid-erin"))
	require.Zero(t, watched(p, kvWatchTotals, "uid-bob"))
}

// The panel's hidden look-ins for preview stills are nobody's watching: they
// are never recorded as anybody's, and are left out of the sessions besides.
func TestPeeksDoNotCount(t *testing.T) {
	origin, issued := fakeWHEPOrigin(t)
	defer origin.Close()

	p, _, _ := watchTimePlugin(t)
	p.httpClient = origin.Client()
	cfg := p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = origin.URL
	cfg.LivePreviews = true
	p.setConfiguration(cfg)

	dave := p.streamHandle("videokey-dave")
	p.setSnapshot(&snapshot{Streams: []apiStream{{StreamKey: "videokey-dave", Handle: dave}}, Configured: true, LivePreviews: true})

	peek := p.stills().claim(dave, "uid-bob", stillModePeek, time.Now())
	require.NotEmpty(t, peek.Lease)
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, peek.Lease, "uid-bob").Code)
	require.Empty(t, rememberedSessions(p.watchers()), "a peek is not watching")

	// Even one that somehow was remembered as somebody's is not counted, as
	// it is not counted as a viewer.
	p.stills().rememberPeek(dave, "uid-erin", "erin-peek", time.Now())
	p.watchers().record("erin-peek", "uid-erin", dave, time.Now())

	start := watchEpoch()
	raw := []bbStream{rawWatched("videokey-dave", issued()[0], "erin-peek")}
	watchPass(p, raw, start)
	watchPass(p, raw, start.Add(10*time.Second))

	require.Empty(t, mustLoad(t, p, kvWatchTotals))
}

// With faces off and the board on, the relay remembers whose session it is
// only for somebody the board could count.
func TestWithFacesOffOnlyLinkedPeoplesSessionsAreRemembered(t *testing.T) {
	origin, issued := fakeWHEPOrigin(t)
	defer origin.Close()

	p, _, _ := watchTimePlugin(t)
	p.httpClient = origin.Client()
	cfg := p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = origin.URL
	p.setConfiguration(cfg)

	dave := p.streamHandle("videokey-dave")
	p.setSnapshot(&snapshot{Streams: []apiStream{{StreamKey: "videokey-dave", Handle: dave}}, Configured: true})

	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, "", "uid-bob").Code)
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, "", "uid-alice").Code)
	require.Len(t, issued(), 2)
	require.Equal(t, map[string]string{issued()[0]: "uid-bob"}, rememberedSessions(p.watchers()))

	// And with the board off as well, nobody's.
	off := cfg.Clone()
	off.TopWatchers = false
	p.setConfiguration(off)
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, "", "uid-erin").Code)
	require.Equal(t, map[string]string{issued()[0]: "uid-bob"}, rememberedSessions(p.watchers()))

	// A poll with both off forgets the one it had.
	p.decorate([]bbStream{rawWatched("videokey-dave", issued()...)}, off)
	require.Empty(t, rememberedSessions(p.watchers()))
}

// Faces off and the board on is the one combination that keeps some sessions
// and not others, and it has to hold through a poll that cannot reach
// Broadcast Box as well as through one that can.
func TestWithFacesOffTheBoardKeepsItsSessionsAndNoNames(t *testing.T) {
	p, _, _ := watchTimePlugin(t)
	dave := p.streamHandle("videokey-dave")
	store := p.watchers()

	store.record("bob-1", "uid-bob", dave, time.Now())
	store.names.put("uid-bob", "Bob", time.Minute)

	current := &snapshot{Streams: []apiStream{{Handle: dave, Watchers: []apiWatcher{{UserID: "uid-bob", Who: "Bob"}}}}}
	p.dropWatchersWhenOff(current, p.getConfiguration())

	require.Nil(t, current.Streams[0].Watchers, "no faces with faces off")
	require.Contains(t, rememberedSessions(store), "bob-1", "an outage is no evidence anybody stopped watching")
	_, named := store.names.get("uid-bob")
	require.False(t, named, "names are only ever for faces")

	streams := p.decorate([]bbStream{rawWatched("videokey-dave", "bob-1")}, p.getConfiguration())
	require.Nil(t, cardFor(t, streams, dave).Watchers)
	require.Equal(t, 1, cardFor(t, streams, dave).Viewers)
	require.Contains(t, rememberedSessions(store), "bob-1")
}

func TestWatchTimeIsSplitAcrossUTCMonths(t *testing.T) {
	p, _, _ := watchTimePlugin(t)
	dave := p.streamHandle("videokey-dave")

	endOfSeptember := time.Date(2026, time.September, 30, 23, 59, 0, 0, time.UTC)
	p.watchers().record("bob-1", "uid-bob", dave, endOfSeptember)
	raw := []bbStream{rawWatched("videokey-dave", "bob-1")}

	watchPass(p, raw, endOfSeptember.Add(50*time.Second))
	watchPass(p, raw, time.Date(2026, time.October, 1, 0, 0, 5, 0, time.UTC))

	require.EqualValues(t, 15, watched(p, kvWatchTotals, "uid-bob"))
	require.EqualValues(t, 10, watched(p, kvWatchMonthPrefix+"2026-09", "uid-bob"))
	require.EqualValues(t, 5, watched(p, kvWatchMonthPrefix+"2026-10", "uid-bob"))
}

// A fraction of a second dropped on every pass would be minutes a day nobody
// was ever paid.
func TestPassesAtUnevenInstantsLoseNothing(t *testing.T) {
	p, _, _ := watchTimePlugin(t)
	start := watchEpoch()
	p.watchers().record("bob-1", "uid-bob", p.streamHandle("videokey-dave"), start)
	raw := []bbStream{rawWatched("videokey-dave", "bob-1")}

	watchPass(p, raw, start)
	at := start
	for i := 0; i < 30; i++ {
		at = at.Add(9*time.Second + 700*time.Millisecond)
		watchPass(p, raw, at)
	}

	require.EqualValues(t, int64(at.Truncate(time.Second).Sub(start)/time.Second), watched(p, kvWatchTotals, "uid-bob"))
}

func TestWatchTimeIsNotPaidTwiceAfterARestartOrAGap(t *testing.T) {
	p, kv, _ := watchTimePlugin(t)
	start := watchEpoch()
	dave := p.streamHandle("videokey-dave")
	p.watchers().record("bob-1", "uid-bob", dave, start)
	raw := []bbStream{rawWatched("videokey-dave", "bob-1")}

	watchPass(p, raw, start)
	watchPass(p, raw, start.Add(10*time.Second))
	require.EqualValues(t, 10, watched(p, kvWatchTotals, "uid-bob"))

	// A gap: the passes in between never happened, and the session is still
	// open on the other side of it. Paid no more than the panel would have
	// gone on showing the stream through an outage, not the ten minutes.
	limit := watchCreditLimit(p.getConfiguration())
	watchPass(p, raw, start.Add(10*time.Second+10*time.Minute))
	require.EqualValues(t, 10+int64(limit/time.Second), watched(p, kvWatchTotals, "uid-bob"))
	require.Less(t, limit, 2*time.Minute)

	// A restart: a new process over the same KV. The session Broadcast Box
	// still lists is nobody's as far as it knows, so nothing is paid for it,
	// and nothing already written is touched.
	before := watched(p, kvWatchTotals, "uid-bob")
	restarted, _ := kvBackedPluginOn(kv)
	linkEverybody(t, restarted, kv)
	restarted.setConfiguration(p.getConfiguration())

	later := start.Add(20 * time.Minute)
	watchPass(restarted, raw, later)
	watchPass(restarted, raw, later.Add(10*time.Second))
	require.Equal(t, before, watched(restarted, kvWatchTotals, "uid-bob"))

	// Bob reopens the stream: counted from the first pass that sees it, and
	// only for the time since.
	restarted.watchers().record("bob-2", "uid-bob", dave, later.Add(12*time.Second))
	raw = []bbStream{rawWatched("videokey-dave", "bob-1", "bob-2")}
	watchPass(restarted, raw, later.Add(20*time.Second))
	require.Equal(t, before+8, watched(restarted, kvWatchTotals, "uid-bob"))
}

func TestAClockThatGoesBackwardsPaysNothingTwice(t *testing.T) {
	p, _, _ := watchTimePlugin(t)
	start := watchEpoch()
	p.watchers().record("bob-1", "uid-bob", p.streamHandle("videokey-dave"), start.Add(-time.Hour))
	raw := []bbStream{rawWatched("videokey-dave", "bob-1")}

	watchPass(p, raw, start)
	watchPass(p, raw, start.Add(10*time.Second))
	watchPass(p, raw, start.Add(-30*time.Second))
	watchPass(p, raw, start.Add(-20*time.Second))

	require.EqualValues(t, 20, watched(p, kvWatchTotals, "uid-bob"), "ten before the clock went back and ten after")
}

func TestTopWatchersOffRecordsNothing(t *testing.T) {
	p, store, faults := watchTimePlugin(t)
	start := watchEpoch()
	dave := p.streamHandle("videokey-dave")

	cfg := p.getConfiguration().Clone()
	cfg.TopWatchers = false
	cfg.ShowWatchers = true
	p.setConfiguration(cfg)

	// Faces on keep the session; the board off writes nothing about it.
	p.watchers().record("bob-1", "uid-bob", dave, start)
	raw := []bbStream{rawWatched("videokey-dave", "bob-1")}
	for at := 0; at <= 60; at += 10 {
		watchPass(p, raw, start.Add(time.Duration(at)*time.Second))
	}
	requireNoWatchDocuments(t, store)
	require.Zero(t, faults.writes[kvWatchTotals])

	// That a poll with the board off publishes nothing about it is checked
	// through pollOnce itself, by TestAPollCarriesTheWatchBoardsOnlyWhileTheyAreOn.
}

// On, off for a while with Bob watching all along, and on again, with every
// poll reaching Broadcast Box: the time it spent off is not paid. Faces on, so
// the session outlives the time off and there is somebody to overpay.
func TestSwitchingTheBoardBackOnPaysNothingForTheTimeItWasOff(t *testing.T) {
	p, _, _ := watchTimePlugin(t)
	start := watchEpoch()

	on := p.getConfiguration().Clone()
	on.ShowWatchers = true
	p.setConfiguration(on)

	p.watchers().record("bob-1", "uid-bob", p.streamHandle("videokey-dave"), start)
	raw := []bbStream{rawWatched("videokey-dave", "bob-1")}

	watchPass(p, raw, start)
	watchPass(p, raw, start.Add(10*time.Second))
	require.EqualValues(t, 10, watched(p, kvWatchTotals, "uid-bob"))

	off := on.Clone()
	off.TopWatchers = false
	p.setConfiguration(off)
	watchPass(p, raw, start.Add(20*time.Second))
	watchPass(p, raw, start.Add(30*time.Second))

	// Thirty seconds since the last pass that paid, well inside what one pass
	// may pay, so only the forgotten pass stands between Bob and all of it.
	p.setConfiguration(on)
	watchPass(p, raw, start.Add(40*time.Second))
	require.EqualValues(t, 10, watched(p, kvWatchTotals, "uid-bob"), "the first pass back only looks")

	watchPass(p, raw, start.Add(50*time.Second))
	require.EqualValues(t, 20, watched(p, kvWatchTotals, "uid-bob"))
}

// The same, switched off and on again while Broadcast Box cannot be reached,
// so that no pass runs with the board off to forget the last one: the
// settings change does it.
func TestSwitchingTheBoardOffAndOnDuringAnOutagePaysNothingForTheTimeOff(t *testing.T) {
	p, _, _ := watchTimePlugin(t)
	start := watchEpoch()

	saved := map[string]any{"alwaysonstreamkeys": "videokey-watts", "topwatchers": true, "showwatchers": true}
	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("GetUnsanitizedConfig").Return(&model.Config{PluginSettings: model.PluginSettings{
		Plugins: map[string]map[string]any{manifestID: saved},
	}})
	api.On("LoadPluginConfiguration", mock.Anything).Run(func(args mock.Arguments) {
		encoded, err := json.Marshal(saved)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(encoded, args.Get(0)))
	}).Return(nil)
	// The Site URL, which the app check's rules are read with on every load.
	api.On("GetConfig").Return(&model.Config{}).Maybe()
	require.NoError(t, p.OnConfigurationChange())
	require.True(t, p.getConfiguration().TopWatchers)
	require.True(t, p.getConfiguration().ShowWatchers)

	p.watchers().record("bob-1", "uid-bob", p.streamHandle("videokey-dave"), start)
	raw := []bbStream{rawWatched("videokey-dave", "bob-1")}

	watchPass(p, raw, start)
	watchPass(p, raw, start.Add(10*time.Second))
	require.EqualValues(t, 10, watched(p, kvWatchTotals, "uid-bob"))

	// Broadcast Box goes quiet, so no pass runs at all, and meanwhile the
	// board is switched off and back on.
	saved["topwatchers"] = false
	require.NoError(t, p.OnConfigurationChange())
	saved["topwatchers"] = true
	require.NoError(t, p.OnConfigurationChange())

	// Back fifty seconds after the last pass that paid, inside what one pass
	// may pay: without the settings change forgetting it, all fifty.
	watchPass(p, raw, start.Add(60*time.Second))
	require.EqualValues(t, 10, watched(p, kvWatchTotals, "uid-bob"), "the first pass back only looks")

	watchPass(p, raw, start.Add(70*time.Second))
	require.EqualValues(t, 20, watched(p, kvWatchTotals, "uid-bob"))
}

func TestAnUnreadableWatchTableIsNeverOverwritten(t *testing.T) {
	p, store, faults := watchTimePlugin(t)
	start := watchEpoch()
	p.watchers().record("bob-1", "uid-bob", p.streamHandle("videokey-dave"), start)
	raw := []bbStream{rawWatched("videokey-dave", "bob-1")}

	watchPass(p, raw, start)
	watchPass(p, raw, start.Add(10*time.Second))
	require.EqualValues(t, 10, watched(p, kvWatchTotals, "uid-bob"))

	// A database that does not answer for a moment: that pass is skipped,
	// nothing is written, and the next one pays for both. Not lost, and not
	// paid twice.
	faults.failRead(kvWatchTotals)
	watchPass(p, raw, start.Add(20*time.Second))
	faults.healRead(kvWatchTotals)
	require.EqualValues(t, 10, watched(p, kvWatchTotals, "uid-bob"))
	watchPass(p, raw, start.Add(30*time.Second))
	require.EqualValues(t, 30, watched(p, kvWatchTotals, "uid-bob"))

	// Bytes that will not decode: copied aside, left exactly as they are,
	// and recording stops.
	damaged := []byte(`{"uid-bob": "a format from the future"}`)
	store[kvWatchTotals] = damaged
	writes := faults.writes[kvWatchTotals]

	watchPass(p, raw, start.Add(40*time.Second))
	watchPass(p, raw, start.Add(50*time.Second))

	require.Equal(t, damaged, store[kvWatchTotals])
	require.Equal(t, damaged, store[kvQuarantinePrefix+kvWatchTotals])
	require.Equal(t, writes, faults.writes[kvWatchTotals])
	halted, reason := p.watchHalt.state()
	require.True(t, halted)
	require.Contains(t, reason, kvWatchTotals)

	// Only the board's own recording. Airtime is the history that cannot be
	// rebuilt, and a damaged watching table must not stop it.
	airtimeHalted, _ := p.statsHalt()
	require.False(t, airtimeHalted)

	// And the boards are left off rather than shown as if nobody had
	// watched.
	current := &snapshot{}
	p.addWatchBoards(current, p.getConfiguration(), start)
	require.False(t, current.WatchBoards)
	require.Empty(t, current.WatchAllTime)
}

func TestAnUnreadableMonthIsNeverOverwrittenEither(t *testing.T) {
	p, store, faults := watchTimePlugin(t)
	start := watchEpoch()
	p.watchers().record("bob-1", "uid-bob", p.streamHandle("videokey-dave"), start)
	raw := []bbStream{rawWatched("videokey-dave", "bob-1")}

	damaged := []byte(`[1, 2, 3]`)
	store[watchMonthBucket(start)] = damaged

	watchPass(p, raw, start)
	watchPass(p, raw, start.Add(10*time.Second))

	require.Equal(t, damaged, store[watchMonthBucket(start)])
	require.Zero(t, faults.writes[kvWatchTotals], "the totals are not paid for a month that could not be")
	halted, _ := p.watchHalt.state()
	require.True(t, halted)
}

// A write that fails is paid again on the next pass rather than lost.
func TestAFailedSaveIsPaidOnTheNextPass(t *testing.T) {
	p, _, faults := watchTimePlugin(t)
	start := watchEpoch()
	p.watchers().record("bob-1", "uid-bob", p.streamHandle("videokey-dave"), start)
	raw := []bbStream{rawWatched("videokey-dave", "bob-1")}

	watchPass(p, raw, start)
	faults.failWrite(kvWatchTotals)
	watchPass(p, raw, start.Add(10*time.Second))
	faults.healWrite(kvWatchTotals)
	require.Zero(t, watched(p, kvWatchTotals, "uid-bob"))

	watchPass(p, raw, start.Add(20*time.Second))
	require.EqualValues(t, 20, watched(p, kvWatchTotals, "uid-bob"))
	require.EqualValues(t, 20, watched(p, watchMonthBucket(start), "uid-bob"))
}

func seedWatchTable(t *testing.T, store map[string][]byte, key string, table statTable) {
	t.Helper()

	data, err := json.Marshal(table)
	require.NoError(t, err)
	store[key] = data
}

func TestTheWatchBoardsNamePeopleInAStableOrder(t *testing.T) {
	p, store, _ := watchTimePlugin(t)
	now := time.Date(2026, time.October, 12, 18, 0, 0, 0, time.UTC)

	// Caro and Bob tie on the month; Erin has only an all-time figure; one
	// account cannot be read any more.
	seedWatchTable(t, store, kvWatchTotals, statTable{
		"uid-bob":   {Seconds: 7200},
		"uid-caro":  {Seconds: 9000},
		"uid-erin":  {Seconds: 3600},
		"uid-gone":  {Seconds: 60},
		"uid-alice": {Seconds: 0},
	})
	seedWatchTable(t, store, watchMonthBucket(now), statTable{
		"uid-caro": {Seconds: 1800},
		"uid-bob":  {Seconds: 1800},
	})

	// Still linked, though the account behind the link cannot be read.
	linkForTest(store, "uid-gone", "videokey-gone")

	current := &snapshot{}
	p.addWatchBoards(current, p.getConfiguration(), now)

	require.True(t, current.WatchBoards)
	require.Equal(t, "October 2026", current.MonthLabel)
	require.Equal(t, []leaderEntry{
		{Rank: 1, Title: "Caro", Seconds: 9000, UserID: "uid-caro", Username: "caro", DisplayName: "Caro"},
		{Rank: 2, Title: "Bob", Seconds: 7200, UserID: "uid-bob", Username: "bob", DisplayName: "Bob"},
		{Rank: 3, Title: "Erin Cat", Seconds: 3600, UserID: "uid-erin", Username: "erin", DisplayName: "Erin Cat"},
		{Rank: 4, Title: unknownWatcher, Seconds: 60, UserID: "uid-gone"},
	}, current.WatchAllTime, "nobody with no time at all")
	require.Equal(t, 4, current.WatchAllTimeTotal)

	// A tie goes to the user id, which never changes, so the two keep their
	// places from one poll to the next.
	require.Equal(t, []leaderEntry{
		{Rank: 1, Title: "Bob", Seconds: 1800, UserID: "uid-bob", Username: "bob", DisplayName: "Bob"},
		{Rank: 2, Title: "Caro", Seconds: 1800, UserID: "uid-caro", Username: "caro", DisplayName: "Caro"},
	}, current.WatchMonth)
	require.Equal(t, 2, current.WatchMonthTotal)

	// The same every time, so the boards' version does not churn. (The
	// boards are not in the snapshot: they are what GET /boards sends.)
	first, err := json.Marshal(decideAwards(current))
	require.NoError(t, err)
	require.Contains(t, string(first), `"watchAllTime":[`)
	for i := 0; i < 20; i++ {
		again := &snapshot{}
		p.addWatchBoards(again, p.getConfiguration(), now)
		encoded, err := json.Marshal(decideAwards(again))
		require.NoError(t, err)
		require.Equal(t, string(first), string(encoded))
	}

	// Nothing that is a stream key, and nothing about whoever is looking,
	// in the boards or in the snapshot.
	snapshotted, err := json.Marshal(current)
	require.NoError(t, err)
	for _, key := range watchTimeLinks {
		require.NotContains(t, string(first), key)
		require.NotContains(t, string(snapshotted), key)
	}
}

func TestTheWatchBoardsAreOnWithNobodyOnThem(t *testing.T) {
	p, _, _ := watchTimePlugin(t)

	current := &snapshot{}
	p.addWatchBoards(current, p.getConfiguration(), watchEpoch())

	require.True(t, current.WatchBoards, "on, so the panel can say nobody has watched yet")

	// In the boards, which is where a client reads it; the snapshot itself
	// says nothing of any board.
	encoded, err := json.Marshal(decideAwards(current))
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"watchBoards":true`)
	require.NotContains(t, string(encoded), "watchMonth")
	require.NotContains(t, string(encoded), "watchAllTime")

	encoded, err = json.Marshal(current)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "watchBoards")
}

func TestTheWatchBoardsAreBoundedForTheWire(t *testing.T) {
	p, store, _ := watchTimePlugin(t)
	now := watchEpoch()

	table := statTable{}
	for i := 0; i < maxLeaderboardRows+15; i++ {
		userID := fmt.Sprintf("uid-%03d", i)
		table[userID] = statEntry{Seconds: int64(1000 + i)}
		linkForTest(store, userID, "videokey-"+userID)
	}
	seedWatchTable(t, store, kvWatchTotals, table)

	current := &snapshot{}
	p.addWatchBoards(current, p.getConfiguration(), now)

	require.Len(t, current.WatchAllTime, maxLeaderboardRows)
	require.Equal(t, maxLeaderboardRows+15, current.WatchAllTimeTotal)
	require.EqualValues(t, 1000+maxLeaderboardRows+14, current.WatchAllTime[0].Seconds)
}

// Somebody who unlinks leaves both boards at once, and the others close up
// behind them. Their hours are kept, and come back when they link again.
func TestSomebodyWhoUnlinksLeavesTheWatchBoards(t *testing.T) {
	p, store, faults := watchTimePlugin(t)
	now := time.Date(2026, time.October, 12, 18, 0, 0, 0, time.UTC)

	seedWatchTable(t, store, kvWatchTotals, statTable{"uid-bob": {Seconds: 7200}, "uid-caro": {Seconds: 3600}})
	seedWatchTable(t, store, watchMonthBucket(now), statTable{"uid-bob": {Seconds: 1800}, "uid-caro": {Seconds: 900}})

	delete(store, kvLinkByUserPrefix+"uid-bob")
	delete(store, kvLinkByKeyPrefix+"videokey-bob")

	current := &snapshot{}
	p.addWatchBoards(current, p.getConfiguration(), now)

	require.True(t, current.WatchBoards)
	require.Equal(t, []leaderEntry{
		{Rank: 1, Title: "Caro", Seconds: 3600, UserID: "uid-caro", Username: "caro", DisplayName: "Caro"},
	}, current.WatchAllTime)
	require.Equal(t, 1, current.WatchAllTimeTotal)
	require.Equal(t, []leaderEntry{
		{Rank: 1, Title: "Caro", Seconds: 900, UserID: "uid-caro", Username: "caro", DisplayName: "Caro"},
	}, current.WatchMonth)
	require.Equal(t, 1, current.WatchMonthTotal)

	encoded, err := json.Marshal(decideAwards(current))
	require.NoError(t, err)
	require.Contains(t, string(encoded), "uid-caro")
	require.NotContains(t, string(encoded), "bob")

	require.EqualValues(t, 7200, watched(p, kvWatchTotals, "uid-bob"), "kept, not wiped")

	// A link that cannot be read is not taken as a link either.
	linkForTest(store, "uid-bob", "videokey-bob")
	faults.failRead(kvLinkByUserPrefix + "uid-bob")
	current = &snapshot{}
	p.addWatchBoards(current, p.getConfiguration(), now)
	require.Len(t, current.WatchAllTime, 1)
	faults.healRead(kvLinkByUserPrefix + "uid-bob")

	// Linked again: back in their place, with everything they had.
	current = &snapshot{}
	p.addWatchBoards(current, p.getConfiguration(), now)
	require.Len(t, current.WatchAllTime, 2)
	require.Equal(t, "uid-bob", current.WatchAllTime[0].UserID)
	require.EqualValues(t, 7200, current.WatchAllTime[0].Seconds)
	require.Equal(t, 2, current.WatchAllTimeTotal)
}

// With faces off, the boards must not say who is watching right now by whose
// seconds went up since the last snapshot: the figures move once an hour, on
// the hour. Names and links do not wait.
func TestWithFacesOffTheWatchBoardsMoveOnlyOnTheHour(t *testing.T) {
	p, store, _ := watchTimePlugin(t)
	require.False(t, p.getConfiguration().ShowWatchers)
	at := time.Date(2026, time.October, 31, 23, 0, 5, 0, time.UTC)

	seed := func(seconds int64, month time.Time) {
		seedWatchTable(t, store, kvWatchTotals, statTable{"uid-bob": {Seconds: seconds}})
		seedWatchTable(t, store, watchMonthBucket(month), statTable{"uid-bob": {Seconds: seconds}})
	}
	board := func(when time.Time) *snapshot {
		current := &snapshot{}
		p.addWatchBoards(current, p.getConfiguration(), when)
		require.True(t, current.WatchBoards)

		return current
	}
	shown := func(current *snapshot) int64 {
		require.Len(t, current.WatchAllTime, 1)

		return current.WatchAllTime[0].Seconds
	}

	seed(600, at)
	first := board(at)
	require.EqualValues(t, 600, shown(first))

	// Bob goes on watching and the tables move with every pass, but the
	// boards do not, and nor does anything a snapshot is compared by.
	seed(610, at)
	require.Equal(t, first, board(at.Add(10*time.Second)))
	seed(3900, at)
	require.Equal(t, first, board(at.Add(59*time.Minute+50*time.Second)))

	// The hour turns, and with it the month here: a fresh read, of the new
	// month's table, which Bob is not in yet.
	november := at.Add(time.Hour)
	seedWatchTable(t, store, kvWatchTotals, statTable{"uid-bob": {Seconds: 3910}})
	next := board(november)
	require.EqualValues(t, 3910, shown(next))
	require.Empty(t, next.WatchMonth)
	require.Equal(t, "November 2026", next.MonthLabel)

	// Unlinking does not wait for the hour.
	delete(store, kvLinkByUserPrefix+"uid-bob")
	require.Empty(t, board(november.Add(10*time.Second)).WatchAllTime)
	store[kvLinkByUserPrefix+"uid-bob"] = []byte("videokey-bob")

	// With faces on there is nothing to hide: as they stand, every poll.
	faces := p.getConfiguration().Clone()
	faces.ShowWatchers = true
	p.setConfiguration(faces)
	seedWatchTable(t, store, kvWatchTotals, statTable{"uid-bob": {Seconds: 3920}})
	require.EqualValues(t, 3920, shown(board(november.Add(20*time.Second))))

	// And off again within the same hour: on from there, not back to the
	// figures of the hour's first poll.
	off := faces.Clone()
	off.ShowWatchers = false
	p.setConfiguration(off)
	seedWatchTable(t, store, kvWatchTotals, statTable{"uid-bob": {Seconds: 3930}})
	require.EqualValues(t, 3920, shown(board(november.Add(30*time.Second))))
}

// The whole round trip, against a Broadcast Box that answers both WHEP and
// /api/status: the board fills from real polls, rides out one that cannot
// reach the origin, and goes from the snapshot when it is switched off.
func TestAPollCarriesTheWatchBoardsOnlyWhileTheyAreOn(t *testing.T) {
	var lock sync.Mutex
	open := map[string][]string{}
	issued := 0
	down := false

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lock.Lock()
		defer lock.Unlock()

		if down {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}

		switch r.URL.Path {
		case "/api/whep":
			key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			issued++
			id := fmt.Sprintf("dddddddd-0000-0000-0000-%012d", issued)
			open[key] = append(open[key], id)

			w.Header().Set("Location", "/api/whep/"+id)
			w.Header().Set("Content-Type", "application/sdp")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, "v=0\r\na=candidate:1 1 udp 2130706431 203.0.113.7 3478 typ host\r\n")

		case "/api/status":
			status := []bbStream{}
			for _, key := range []string{"videokey-dave", "videokey-watts"} {
				stream := rawWatched(key, open[key]...)
				stream.StreamStart = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
				status = append(status, stream)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(status)

		default:
			http.NotFound(w, r)
		}
	}))
	defer origin.Close()

	p, _, _ := watchTimePlugin(t)
	p.httpClient = origin.Client()
	cfg := p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = origin.URL
	p.setConfiguration(cfg)

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("KVList", mock.Anything, mock.Anything).Return([]string{}, nil)
	api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return()

	firstPoll := time.Now()
	p.pollOnce(t.Context(), true)
	current := p.getSnapshot()
	require.True(t, current.WatchBoards)
	require.Empty(t, current.WatchAllTime, "nobody has watched yet")

	dave := p.streamHandle("videokey-dave")
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, "", "uid-bob").Code)

	// Real polls a moment apart. Time is paid in whole seconds, from the
	// first whole second after Bob pressed play, so two seconds is what it
	// takes to be sure of one.
	p.pollOnce(t.Context(), true)
	time.Sleep(2100 * time.Millisecond)
	p.pollOnce(t.Context(), true)

	// Recorded on every pass. With faces off, as here, the boards show it from
	// the next hour, not the next poll: see
	// TestWithFacesOffTheWatchBoardsMoveOnlyOnTheHour. Checked only when the
	// polls did not happen to straddle the top of an hour.
	require.Positive(t, watched(p, kvWatchTotals, "uid-bob"))
	if time.Now().UTC().Truncate(time.Hour).Equal(firstPoll.UTC().Truncate(time.Hour)) {
		require.Empty(t, p.getSnapshot().WatchAllTime)
	}

	// The next hour, as far as the boards can tell.
	p.watchShown.keep(0, nil, nil)
	p.pollOnce(t.Context(), true)

	current = p.getSnapshot()
	require.Len(t, current.WatchAllTime, 1)
	require.Equal(t, "uid-bob", current.WatchAllTime[0].UserID)
	require.Equal(t, "Bob", current.WatchAllTime[0].Title)
	require.Positive(t, current.WatchAllTime[0].Seconds)
	require.Equal(t, current.WatchAllTime, current.WatchMonth)
	require.NotEmpty(t, current.MonthLabel)

	// Neither in the snapshot nor in the boards, which carry the names.
	for _, sent := range []any{current, p.boardsNow()} {
		encoded, err := json.Marshal(sent)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "videokey-dave")
		require.NotContains(t, string(encoded), "dddddddd", "Broadcast Box's session ids stay on the server")
	}
	require.Contains(t, jsonOf(t, p.boardsNow()), "uid-bob")

	// Broadcast Box cannot be reached: the boards ride along with the cards.
	lock.Lock()
	down = true
	lock.Unlock()
	p.pollOnce(t.Context(), true)
	require.NotEmpty(t, p.getSnapshot().Error)
	require.Equal(t, current.WatchAllTime, p.getSnapshot().WatchAllTime)
	require.True(t, p.getSnapshot().WatchBoards)

	// Switched off, during the same outage: gone, not carried.
	off := cfg.Clone()
	off.TopWatchers = false
	p.setConfiguration(off)
	p.pollOnce(t.Context(), true)
	require.False(t, p.getSnapshot().WatchBoards)
	require.Nil(t, p.getSnapshot().WatchAllTime)
	require.NotContains(t, jsonOf(t, p.boardsNow()), `"watch`, "nor in what GET /boards sends")

	// And once it is back, still gone.
	lock.Lock()
	down = false
	lock.Unlock()
	p.pollOnce(t.Context(), true)
	require.Nil(t, p.getSnapshot().WatchAllTime)
	require.NotContains(t, jsonOf(t, p.boardsNow()), `"watch`)
}
