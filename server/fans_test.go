package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/require"
)

// Biggest fans (design/3.2.0.md §4): the linked viewer who has watched a
// linked streamer most in the panel, all time, counted by Top Snoopers' rules
// and, by Victor's answer of 2026-10-12, only while faces are on.

// fansPlugin is the watch-time plugin with faces on and biggest fans on.
func fansPlugin(t *testing.T) (*Plugin, map[string][]byte, *kvFaults) {
	t.Helper()

	p, store, faults := watchTimePlugin(t)
	cfg := p.getConfiguration().Clone()
	cfg.ShowWatchers = true
	cfg.EnableBiggestFans = true
	p.setConfiguration(cfg)
	p.refreshAwardDocs(true)

	return p, store, faults
}

// fanSeconds is a pair's seconds as stored, 0 for none.
func fanSeconds(t *testing.T, p *Plugin, streamerID, viewerID string) int64 {
	t.Helper()

	return mustLoad(t, p, kvFansTotals)[fanKey(streamerID, viewerID)].Seconds
}

func seedFans(t *testing.T, store map[string][]byte, table statTable) {
	t.Helper()

	encoded, err := json.Marshal(table)
	require.NoError(t, err)
	store[kvFansTotals] = encoded
}

func TestFansCountOnlyLinkedOnBothSides(t *testing.T) {
	p, store, _ := fansPlugin(t)
	start := watchEpoch()
	dave, walter := p.streamHandle("videokey-dave"), p.streamHandle("videokey-walter")

	watchPass(p, []bbStream{rawWatched("videokey-dave"), rawWatched("videokey-walter")}, start)

	p.watchers().record("bob-1", "uid-bob", dave, start)
	p.watchers().record("alice-1", "uid-alice", dave, start)
	p.watchers().record("erin-1", "uid-erin", walter, start)
	raw := []bbStream{rawWatched("videokey-dave", "bob-1", "alice-1"), rawWatched("videokey-walter", "erin-1")}

	watchPass(p, raw, start.Add(30*time.Second))

	require.EqualValues(t, 30, fanSeconds(t, p, "uid-dave", "uid-bob"), "a linked viewer of a linked streamer")
	fans := mustLoad(t, p, kvFansTotals)
	require.Len(t, fans, 1, "never somebody never linked, never a stream nobody linked")
	require.NotContains(t, string(store[kvFansTotals]), "alice")
}

func TestFansLeaveOutOwnPausedTwentyFourSevenPeeksAndRecorders(t *testing.T) {
	p, store, _ := fansPlugin(t)
	start := watchEpoch()
	watchers := p.watchers()

	watchers.record("bob-own", "uid-bob", p.streamHandle("videokey-bob"), start)
	watchers.record("bob-watts", "uid-bob", p.streamHandle("videokey-watts"), start)
	watchers.record("erin-dave", "uid-erin", p.streamHandle("videokey-dave"), start)

	// A peek remembered as somebody's is still not counted.
	p.stills().rememberPeek(p.streamHandle("videokey-erin"), "uid-dave", "dave-peek", time.Now())
	watchers.record("dave-peek", "uid-dave", p.streamHandle("videokey-erin"), start)

	raw := []bbStream{
		rawWatched("videokey-bob", "bob-own"),
		rawWatched("videokey-watts", "bob-watts"),
		pausedRaw("videokey-dave", "erin-dave"),
		rawWatched("videokey-erin", "dave-peek", "recorder-session"),
	}

	watchPass(p, raw, start)
	watchPass(p, raw, start.Add(30*time.Second))
	watchPass(p, raw, start.Add(60*time.Second))

	_, written := store[kvFansTotals]
	require.False(t, written, "nobody was paid, so nothing was written")
}

func TestFansTwoStreamsCreditBoth(t *testing.T) {
	p, _, _ := fansPlugin(t)
	start := watchEpoch()

	watchPass(p, []bbStream{rawWatched("videokey-dave"), rawWatched("videokey-erin")}, start)

	p.watchers().record("bob-dave", "uid-bob", p.streamHandle("videokey-dave"), start)
	p.watchers().record("bob-erin", "uid-bob", p.streamHandle("videokey-erin"), start)
	raw := []bbStream{rawWatched("videokey-dave", "bob-dave"), rawWatched("videokey-erin", "bob-erin")}

	watchPass(p, raw, start.Add(40*time.Second))

	require.EqualValues(t, 40, fanSeconds(t, p, "uid-dave", "uid-bob"))
	require.EqualValues(t, 40, fanSeconds(t, p, "uid-erin", "uid-bob"), "each streamer their own clock time")
	require.EqualValues(t, 40, watched(p, kvWatchTotals, "uid-bob"), "and Top Snoopers pays it once")
}

func TestFansUseUsernameMatchedStreamsForNobody(t *testing.T) {
	p, store, _ := fansPlugin(t)
	cfg := p.getConfiguration().Clone()
	cfg.AutoLinkByUsername = true
	p.setConfiguration(cfg)

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("GetUserByUsername", "alice").Return(watchTimeAccounts["uid-alice"], nil)

	start := watchEpoch()
	watchPass(p, []bbStream{rawWatched("alice")}, start)
	p.watchers().record("bob-alice", "uid-bob", p.streamHandle("alice"), start)
	watchPass(p, []bbStream{rawWatched("alice", "bob-alice")}, start.Add(30*time.Second))

	require.EqualValues(t, 30, watched(p, kvWatchTotals, "uid-bob"), "the board still counts it")
	_, written := store[kvFansTotals]
	require.False(t, written, "but a stream matched only by username is nobody's to be a fan of")
}

func TestFansWriteNothingOnAQuietPass(t *testing.T) {
	p, _, faults := fansPlugin(t)
	start := watchEpoch()

	reads := 0
	faults.setOnRead(func(key string) {
		if key == kvFansTotals {
			reads++
		}
	})

	for i := range 5 {
		watchPass(p, []bbStream{rawWatched("videokey-dave")}, start.Add(time.Duration(i*15)*time.Second))
	}

	require.Zero(t, reads, "no read")
	require.Zero(t, faults.writesTo(kvFansTotals), "no write")
}

// Victor's answer of 2026-10-12 replaces §4.3's hourly freeze: with faces off
// nothing is counted for fans at all, so the figures stand still, and the
// Snoopers board goes on counting.
func TestFansStandStillWithFacesOff(t *testing.T) {
	p, store, faults := fansPlugin(t)
	seedFans(t, store, statTable{fanKey("uid-dave", "uid-bob"): {Seconds: 7200}})

	off := p.getConfiguration().Clone()
	off.ShowWatchers = false
	p.setConfiguration(off)

	start := watchEpoch()
	dave := p.streamHandle("videokey-dave")
	watchPass(p, []bbStream{rawWatched("videokey-dave")}, start)
	p.watchers().record("bob-1", "uid-bob", dave, start)
	raw := []bbStream{rawWatched("videokey-dave", "bob-1")}
	watchPass(p, raw, start.Add(time.Hour/2))
	watchPass(p, raw, start.Add(time.Hour))

	require.Zero(t, faults.writesTo(kvFansTotals), "nothing counted with faces off")
	require.EqualValues(t, 7200, fanSeconds(t, p, "uid-dave", "uid-bob"))
	require.Positive(t, watched(p, kvWatchTotals, "uid-bob"), "the board still counts")

	current := &snapshot{}
	p.addFans(current, off, nil)
	require.True(t, current.FansOn, "still shown, standing still")
	require.Len(t, current.Fans, 1)
	require.EqualValues(t, 2, current.Fans[0].Hours)
}

// There is no opt-out from 3.2.1 (design/3.2.1.md §2): somebody who opted
// out under 3.2.0 is counted again from now, while the faces are on, and named
// like anybody else. A list of opt-outs left in the store changes nothing.
func TestEverybodyIsCountedNowThereIsNoOptOut(t *testing.T) {
	p, store, _ := fansPlugin(t)
	store[kvFanOptouts] = []byte(`{"v":1,"out":{"uid-bob":{"at":1}}}`)
	p.refreshAwardDocs(true)

	start := watchEpoch()
	dave := p.streamHandle("videokey-dave")

	watchPass(p, []bbStream{rawWatched("videokey-dave")}, start)
	p.watchers().record("bob-1", "uid-bob", dave, start)
	p.watchers().record("erin-1", "uid-erin", dave, start)
	raw := []bbStream{rawWatched("videokey-dave", "bob-1", "erin-1")}
	watchPass(p, raw, start.Add(20*time.Second))
	watchPass(p, raw, start.Add(40*time.Second))

	require.EqualValues(t, 40, fanSeconds(t, p, "uid-dave", "uid-bob"), "counted like anybody")
	require.EqualValues(t, 40, fanSeconds(t, p, "uid-dave", "uid-erin"))

	seedFans(t, store, statTable{fanKey("uid-dave", "uid-erin"): {Seconds: 3600}, fanKey("uid-dave", "uid-bob"): {Seconds: 9000}})
	p.ledgers().forgetFansForTest()
	current := &snapshot{}
	p.addFans(current, p.getConfiguration(), nil)
	require.True(t, current.FansOn)
	require.Len(t, current.Fans, 1)
	require.Equal(t, "uid-bob", current.Fans[0].FanID, "and named like anybody")
}

// The opt-out's two routes are gone: being named, or a restart for a list that
// could not be read. Each is now a path like any other that is not a route,
// for every kind of caller, and nothing is written for asking.
func TestTheOptOutRoutesAreGone(t *testing.T) {
	s := newAccessScene(t)
	cfg := s.p.getConfiguration().Clone()
	cfg.EnableBiggestFans, cfg.ShowWatchers = true, true
	s.p.setConfiguration(cfg)
	s.setKV(kvFansTotals, []byte(`{"`+fanKey(sceneLinked, sceneLinkedAdmin)+`":{"seconds":4000,"title":""}}`))

	require.NotContains(t, routeClasses, "/api/v1/awards/fan")

	before := s.faults.writesTo(kvFansTotals)
	for _, caller := range []string{sceneLinked, sceneLinkedAdmin} {
		recorder := s.serve(http.MethodPost, "/api/v1/awards/fan", caller, `{"named":false}`, nil)
		require.Equal(t, http.StatusNotFound, recorder.Code, caller)
	}
	recorder := s.serve(http.MethodPost, "/api/v1/admin/fans/restart", sceneLockedAdmin, "", nil)
	require.Equal(t, http.StatusNotFound, recorder.Code)

	require.Equal(t, before, s.faults.writesTo(kvFansTotals))
	require.Zero(t, s.faults.writesTo(kvFanOptouts))
	require.Contains(t, string(s.kv(kvFansTotals)), sceneLinkedAdmin, "the hours are untouched")
}

func TestFanNeedsAnHour(t *testing.T) {
	p, store, _ := fansPlugin(t)

	seedFans(t, store, statTable{fanKey("uid-dave", "uid-bob"): {Seconds: 3599}})
	current := &snapshot{}
	p.addFans(current, p.getConfiguration(), nil)
	require.True(t, current.FansOn)
	require.Empty(t, current.Fans)

	seedFans(t, store, statTable{fanKey("uid-dave", "uid-bob"): {Seconds: 3600}})
	p.ledgers().forgetFansForTest()
	p.addFans(current, p.getConfiguration(), nil)
	require.Equal(t, []fanView{{StreamerID: "uid-dave", Streamer: "dave", FanID: "uid-bob", Fan: "Bob", FanUsername: "bob", Hours: 1}}, current.Fans)
}

func TestFanTieKeepsTheHolder(t *testing.T) {
	p, store, _ := fansPlugin(t)
	seedFans(t, store, statTable{
		fanKey("uid-dave", "uid-erin"): {Seconds: 7200},
		fanKey("uid-dave", "uid-bob"):  {Seconds: 7200},
	})

	current := &snapshot{}
	p.addFans(current, p.getConfiguration(), nil)
	require.Equal(t, "uid-bob", current.Fans[0].FanID, "with nobody holding it, the lower user id")

	p.setSnapshot(&snapshot{Fans: []fanView{{StreamerID: "uid-dave", FanID: "uid-erin"}}})
	p.addFans(current, p.getConfiguration(), nil)
	require.Equal(t, "uid-erin", current.Fans[0].FanID, "a tie keeps the holder")

	seedFans(t, store, statTable{
		fanKey("uid-dave", "uid-erin"): {Seconds: 7200},
		fanKey("uid-dave", "uid-bob"):  {Seconds: 7201},
	})
	p.ledgers().forgetFansForTest()
	p.addFans(current, p.getConfiguration(), nil)
	require.Equal(t, "uid-bob", current.Fans[0].FanID, "a second more takes it")
}

func TestAnUnreadableFanTableStartsFromZeroWithNoCopy(t *testing.T) {
	p, store, _ := fansPlugin(t)
	store[kvFansTotals] = []byte(`{"broken":`)

	start := watchEpoch()
	dave := p.streamHandle("videokey-dave")
	watchPass(p, []bbStream{rawWatched("videokey-dave")}, start)
	p.watchers().record("bob-1", "uid-bob", dave, start)
	watchPass(p, []bbStream{rawWatched("videokey-dave", "bob-1")}, start.Add(25*time.Second))

	require.EqualValues(t, 25, fanSeconds(t, p, "uid-dave", "uid-bob"), "started again from zero")
	_, copied := store[kvQuarantinePrefix+kvFansTotals]
	require.False(t, copied, "with no copy kept aside")
	require.EqualValues(t, 25, watched(p, kvWatchTotals, "uid-bob"), "Top Snoopers carries on")
	require.Positive(t, p.ledgerAlertFor().FansResetAt)
	require.Zero(t, p.ledgerAlertFor().ChannelsResetAt)
}

func TestAFailedFanSaveNeverRepaysSnoopers(t *testing.T) {
	p, _, faults := fansPlugin(t)
	start := watchEpoch()
	dave := p.streamHandle("videokey-dave")
	watchPass(p, []bbStream{rawWatched("videokey-dave")}, start)
	p.watchers().record("bob-1", "uid-bob", dave, start)
	raw := []bbStream{rawWatched("videokey-dave", "bob-1")}

	faults.failWrite(kvFansTotals)
	watchPass(p, raw, start.Add(15*time.Second))
	faults.healWrite(kvFansTotals)
	watchPass(p, raw, start.Add(30*time.Second))

	require.EqualValues(t, 30, watched(p, kvWatchTotals, "uid-bob"), "the board was paid each stretch once")
	require.EqualValues(t, 15, fanSeconds(t, p, "uid-dave", "uid-bob"), "the fan table lost one pass, no more")
}

func TestFansCap(t *testing.T) {
	p, store, _ := fansPlugin(t)

	full := statTable{fanKey("uid-dave", "uid-erin"): {Seconds: 10}}
	for i := len(full); i < maxTrackedFanPairs; i++ {
		full[fanKey(fmt.Sprintf("streamer%05d", i), "uid-caro")] = statEntry{Seconds: 1}
	}
	seedFans(t, store, full)

	start := watchEpoch()
	dave := p.streamHandle("videokey-dave")
	watchPass(p, []bbStream{rawWatched("videokey-dave")}, start)
	p.watchers().record("bob-1", "uid-bob", dave, start)
	p.watchers().record("erin-1", "uid-erin", dave, start)
	watchPass(p, []bbStream{rawWatched("videokey-dave", "bob-1", "erin-1")}, start.Add(20*time.Second))

	table := mustLoad(t, p, kvFansTotals)
	require.Len(t, table, maxTrackedFanPairs)
	require.NotContains(t, table, fanKey("uid-dave", "uid-bob"), "a new pair past the bound is not counted")
	require.EqualValues(t, 30, table[fanKey("uid-dave", "uid-erin")].Seconds, "a pair already there still is")
}

func TestFansSnapshotSortedAndStable(t *testing.T) {
	p, store, _ := fansPlugin(t)
	seedFans(t, store, statTable{
		fanKey("uid-erin", "uid-bob"):   {Seconds: 4 * 3600},
		fanKey("uid-dave", "uid-bob"):   {Seconds: 3*3600 + 59},
		fanKey("uid-dave", "uid-caro"):  {Seconds: 3600},
		fanKey("uid-bob", "uid-dave"):   {Seconds: 2 * 3600},
		fanKey("uid-bob", "uid-bob"):    {Seconds: 99 * 3600},
		fanKey("uid-alice", "uid-dave"): {Seconds: 99 * 3600},
		fanKey("uid-caro", "uid-alice"): {Seconds: 99 * 3600},
		fanKey("uid-erin", "uid-erin"):  {Seconds: 99 * 3600},
	})

	var first []byte
	for range 5 {
		current := &snapshot{}
		p.addFans(current, p.getConfiguration(), nil)

		encoded, err := json.Marshal(current.Fans)
		require.NoError(t, err)
		if first == nil {
			first = encoded
		}
		require.Equal(t, string(first), string(encoded), "the same every time")
	}

	var fans []fanView
	require.NoError(t, json.Unmarshal(first, &fans))
	require.Equal(t, []fanView{
		{StreamerID: "uid-bob", Streamer: "Bob", FanID: "uid-dave", Fan: "dave", FanUsername: "dave", Hours: 2},
		{StreamerID: "uid-dave", Streamer: "dave", FanID: "uid-bob", Fan: "Bob", FanUsername: "bob", Hours: 3},
		{StreamerID: "uid-erin", Streamer: "Erin Cat", FanID: "uid-bob", Fan: "Bob", FanUsername: "bob", Hours: 4},
	}, fans, "by streamer id; never yourself, a streamer or viewer who is not linked")
}

func TestFanRoutesAreOpenOrLinked(t *testing.T) {
	require.Equal(t, routeOpen, routeClasses["/api/v1/takebacks"][http.MethodGet])
	require.Equal(t, routeLinked, routeClasses["/api/v1/awards/display"][http.MethodPost])
	require.Equal(t, routeOpen, routeClasses["/api/v1/awards/display"][http.MethodDelete])
	require.Equal(t, routeLinked, routeClasses["/api/v1/awards/mine"][http.MethodGet])
	require.Equal(t, routeLinked, routeClasses["/api/v1/achievements/mine"][http.MethodGet])
}

func TestTakebacksAreTheCallersOwnAndReadNothingPerRequest(t *testing.T) {
	s := newAccessScene(t)
	cfg := s.p.getConfiguration().Clone()
	cfg.EnableBiggestFans = true
	s.p.setConfiguration(cfg)

	s.setKV(kvSounds, []byte(`{"v":1,"entries":[{"id":"aaaaaaaaaaaaaaaa","owner":"`+sceneLocked+`","name":"Airhorn","emoji":{"kind":"system","name":"loud_sound","unified":"1f50a"},"ms":2000,"bytes":1,"level":-21,"at":1},{"id":"bbbbbbbbbbbbbbbb","owner":"`+sceneLetIn+`","name":"Drum","emoji":{"kind":"system","name":"drum_with_drumsticks","unified":"1f941"},"ms":2000,"bytes":1,"level":-21,"at":1}]}`))
	s.p.refreshAwardDocs(true)

	takebacks := func(caller string) takebacksResponse {
		recorder := s.serve(http.MethodGet, "/api/v1/takebacks", caller, "", nil)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))

		var answer takebacksResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer))

		return answer
	}

	require.Equal(t, takebacksResponse{Sounds: 1}, takebacks(sceneLocked))

	s.resetReads()
	for range 5 {
		require.Equal(t, takebacksResponse{Sounds: 1}, takebacks(sceneLetIn))
		require.Equal(t, takebacksResponse{Sounds: 0}, takebacks(sceneLinked))
	}
	require.Zero(t, s.totalReads(), "answered from memory: not even the link")

	// Only the sounds: there is nothing about biggest fans to take back.
	recorder := s.serve(http.MethodGet, "/api/v1/takebacks", sceneLocked, "", nil)
	require.JSONEq(t, `{"sounds":1}`, recorder.Body.String())
}

// What 3.2.0 left of its opt-outs goes at the first activation of 3.2.1,
// with a line in the log that names nobody. A deletion 3.2.0 still owed is
// finished first, because it was promised; everything else in fans_totals
// stays, and the copy 3.2.0 would have set aside goes too. A second
// activation finds nothing and says nothing.
func TestALeftoverFanOptoutListIsDeletedOnceAtActivation(t *testing.T) {
	p, store, faults := fansPlugin(t)
	store[kvFanOptouts] = []byte(`{"v":1,"out":{"uid-bob":{"at":1},"uid-erin":{"at":2,"unlinked":true}},"purge":["uid-bob"]}`)
	store[kvQuarantinePrefix+kvFanOptouts] = []byte(`not json`)
	seedFans(t, store, statTable{
		fanKey("uid-dave", "uid-bob"):  {Seconds: 9000},
		fanKey("uid-erin", "uid-bob"):  {Seconds: 100},
		fanKey("uid-dave", "uid-caro"): {Seconds: 3600},
	})

	p.removeFanOptouts()

	_, left := store[kvFanOptouts]
	require.False(t, left, "the list is gone")
	_, copied := store[kvQuarantinePrefix+kvFanOptouts]
	require.False(t, copied, "and so is the copy set aside")
	table := mustLoad(t, p, kvFansTotals)
	require.Equal(t, statTable{fanKey("uid-dave", "uid-caro"): {Seconds: 3600}}, table, "the deletion owed to Bob is finished; nobody else's hours move")

	logged := infoLines(t, p)
	require.Len(t, logged, 2)
	require.Contains(t, logged[0], "counted again from now")
	require.Contains(t, logged[0], "people=2")
	for _, line := range logged {
		require.NotContains(t, line, "uid-", "nobody's id is logged")
	}

	// The next activation: nothing to read twice, nothing written, nothing said.
	writes := faults.writesTo(kvFansTotals)
	p.removeFanOptouts()
	require.Equal(t, writes, faults.writesTo(kvFansTotals))
	require.Len(t, infoLines(t, p), 2)
}

// A list that cannot be read is deleted all the same: nothing reads it any
// more, and nobody's deletion can be found in it to finish.
func TestAnUnreadableLeftoverFanOptoutListIsDeletedToo(t *testing.T) {
	p, store, _ := fansPlugin(t)
	store[kvFanOptouts] = []byte(`{"v":1,"out":`)
	seedFans(t, store, statTable{fanKey("uid-dave", "uid-bob"): {Seconds: 9000}})

	p.removeFanOptouts()

	_, left := store[kvFanOptouts]
	require.False(t, left)
	require.EqualValues(t, 9000, fanSeconds(t, p, "uid-dave", "uid-bob"))
	require.Contains(t, infoLines(t, p)[0], "readable=false")
}

// When the deletion 3.2.0 still owed cannot be saved, the list stays for the
// next activation, which finishes it; nothing reads the list meanwhile, and
// fans are counted as usual.
func TestALeftoverPurgeThatCannotBeSavedWaitsForTheNextActivation(t *testing.T) {
	p, store, faults := fansPlugin(t)
	leftover := []byte(`{"v":1,"out":{"uid-bob":{"at":1}},"purge":["uid-bob"]}`)
	store[kvFanOptouts] = leftover
	seedFans(t, store, statTable{fanKey("uid-dave", "uid-bob"): {Seconds: 9000}})

	faults.failWrite(kvFansTotals)
	p.removeFanOptouts()
	require.Equal(t, leftover, store[kvFanOptouts], "kept for the next try")
	require.Empty(t, infoLines(t, p))

	faults.healWrite(kvFansTotals)
	p.removeFanOptouts()
	_, left := store[kvFanOptouts]
	require.False(t, left)
	require.Zero(t, fanSeconds(t, p, "uid-dave", "uid-bob"))
}

// infoLines is every LogInfo the plugin wrote, each as its message and its
// pairs, "key=value", in order.
func infoLines(t *testing.T, p *Plugin) []string {
	t.Helper()

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	var lines []string
	for _, call := range api.Calls {
		if call.Method != "LogInfo" {
			continue
		}

		line := fmt.Sprint(call.Arguments[0])
		for i := 1; i+1 < len(call.Arguments); i += 2 {
			line += fmt.Sprintf(" %v=%v", call.Arguments[i], call.Arguments[i+1])
		}
		lines = append(lines, line)
	}

	return lines
}

// forgetFansForTest drops this node's copy of fans_totals, so the next
// display reads it, as a fresh process would.
func (l *fanLedgers) forgetFansForTest() {
	l.lock.Lock()
	defer l.lock.Unlock()

	l.fans, l.fansHeld = nil, false
}
