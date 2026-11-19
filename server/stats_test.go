package main

import (
	"bytes"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// kvBackedPlugin gives the plugin a real in-memory KV store, so a test can drive
// several polls and observe what accumulates.
func kvBackedPlugin() (*Plugin, map[string][]byte) {
	p, store, _ := kvBackedPluginWithFaults(map[string][]byte{})

	return p, store
}

// kvBackedPluginOn builds a fresh Plugin over an existing store, which is how a
// restart is simulated: new process, same KV.
func kvBackedPluginOn(store map[string][]byte) (*Plugin, map[string][]byte) {
	p, store, _ := kvBackedPluginWithFaults(store)

	return p, store
}

// kvFaults lets a test make the store misbehave. Without this the harness could
// never fail — KVGet always returned a nil error — and every rule about not
// overwriting data you could not read was untestable, which is exactly how the
// original bug survived.
type kvFaults struct {
	// The store the mock wraps is a plain map, so every accessor takes this.
	// Without it a concurrency test would trip the race detector on the mock
	// rather than on the thing under test.
	lock sync.Mutex

	readFails map[string]bool

	// writeFails makes KVSet and KVSetWithOptions refuse a key, so a test can
	// see what a write that never happened leaves behind.
	writeFails map[string]bool

	// writes counts how many times each key was written, so a test can assert
	// that a quiet poll is actually quiet.
	writes map[string]int

	// interfere, when set, runs inside every atomic write just before the
	// compare, with the store in hand. It is another node writing the same
	// key in the moment between this node reading it and writing it back, so
	// a test can make a race be lost and see what the loser does next. It
	// runs with lock held, so it changes store directly.
	interfere func(key string, store map[string][]byte)

	// onRead, when set, runs inside every KVGet before it answers. It is
	// something else happening while a read is in flight — an import landing
	// part way through a reading — so a test can see that the reader does not
	// lose it. It runs with lock held, so it must not touch the store through
	// the API.
	onRead func(key string)

	// onWrite, when set, runs inside every call that could change a key —
	// KVSet, KVSetWithOptions, KVCompareAndSet, KVCompareAndDelete and
	// KVDelete — whether or not it then succeeds, so a test can count every
	// attempt to write, not only the ones that landed. It runs with lock held,
	// like onRead.
	onWrite func(key string)
}

func (f *kvFaults) setOnRead(onRead func(key string)) {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.onRead = onRead
}

func (f *kvFaults) setOnWrite(onWrite func(key string)) {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.onWrite = onWrite
}

// writesTo is how many writes landed on key, read under the lock so a test can
// ask while something else may still be writing.
func (f *kvFaults) writesTo(key string) int {
	f.lock.Lock()
	defer f.lock.Unlock()

	return f.writes[key]
}

// wrote calls onWrite, if set. Called with lock held.
func (f *kvFaults) wrote(key string) {
	if f.onWrite != nil {
		f.onWrite(key)
	}
}

func (f *kvFaults) setInterfere(interfere func(key string, store map[string][]byte)) {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.interfere = interfere
}

func (f *kvFaults) failRead(key string) {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.readFails[key] = true
}

func (f *kvFaults) healRead(key string) {
	f.lock.Lock()
	defer f.lock.Unlock()

	delete(f.readFails, key)
}

func (f *kvFaults) failWrite(key string) {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.writeFails[key] = true
}

func (f *kvFaults) healWrite(key string) {
	f.lock.Lock()
	defer f.lock.Unlock()

	delete(f.writeFails, key)
}

// readHook is the plugin's API with one thing added: after a KVGet of a key
// has its answer, and before the caller sees it, afterRead runs, once, outside
// every lock. It is another request arriving while a read is in flight, so a
// test can put the two in the one order that matters and see who wins. Unlike
// onRead it may use the API.
type readHook struct {
	plugin.API

	key       string
	afterRead func()

	// Not a sync.Once, whose Do would hold every other read of the key
	// until afterRead returned, which is the very wait under test.
	fired atomic.Bool
}

func (h *readHook) KVGet(key string) ([]byte, *model.AppError) {
	raw, appErr := h.API.KVGet(key)
	if key == h.key && h.fired.CompareAndSwap(false, true) {
		h.afterRead()
	}

	return raw, appErr
}

// hookRead runs afterRead once, right after the next read of key has been
// answered, and puts the plugin's API back when the test ends.
func hookRead(t *testing.T, p *Plugin, key string, afterRead func()) {
	t.Helper()

	original := p.API
	p.API = &readHook{API: original, key: key, afterRead: afterRead}
	t.Cleanup(func() { p.API = original })
}

// meanwhile runs work on its own goroutine and gives it a moment to finish,
// for a hook standing in for another request: work that is made to wait for
// the read in flight cannot finish meanwhile, whatever the timing. The
// returned channel says how it ended.
func meanwhile(work func() error) chan error {
	done := make(chan error, 1)
	go func() { done <- work() }()

	select {
	case err := <-done:
		done <- err
	case <-time.After(300 * time.Millisecond):
	}

	return done
}

func kvBackedPluginWithFaults(store map[string][]byte) (*Plugin, map[string][]byte, *kvFaults) {
	faults := &kvFaults{readFails: map[string]bool{}, writeFails: map[string]bool{}, writes: map[string]int{}}

	return kvBackedPluginWithFaultsOn(store, faults)
}

// kvBackedPluginWithFaultsOn builds a plugin over a store and faults that
// already exist, which is how a restart is simulated when the test is also
// watching the store misbehave.
func kvBackedPluginWithFaultsOn(store map[string][]byte, faults *kvFaults) (*Plugin, map[string][]byte, *kvFaults) {
	api := &plugintest.API{}
	api.On("KVGet", mock.Anything).Return(
		func(key string) []byte {
			faults.lock.Lock()
			defer faults.lock.Unlock()

			if faults.onRead != nil {
				faults.onRead(key)
			}

			if faults.readFails[key] {
				return nil
			}

			// A copy: the caller compares these bytes against the store later,
			// and handing out the live slice would make every compare succeed.
			return append([]byte(nil), store[key]...)
		},
		func(key string) *model.AppError {
			faults.lock.Lock()
			defer faults.lock.Unlock()

			if faults.readFails[key] {
				return model.NewAppError("KVGet", "kv.read", nil, "injected failure", 500)
			}

			return nil
		},
	)
	api.On("KVSet", mock.Anything, mock.Anything).Return(
		func(key string, value []byte) *model.AppError {
			faults.lock.Lock()
			defer faults.lock.Unlock()

			faults.wrote(key)
			if faults.writeFails[key] {
				return model.NewAppError("KVSet", "kv.write", nil, "injected failure", 500)
			}

			store[key] = value
			faults.writes[key]++

			return nil
		},
	)
	// Refused and counted like KVSet, so a document written atomically can be
	// failed and watched the same way as one written plainly.
	api.On("KVSetWithOptions", mock.Anything, mock.Anything, mock.Anything).Return(
		func(key string, value []byte, opt model.PluginKVSetOptions) bool {
			faults.lock.Lock()
			defer faults.lock.Unlock()

			faults.wrote(key)
			if faults.writeFails[key] {
				return false
			}

			if opt.Atomic {
				if faults.interfere != nil {
					faults.interfere(key, store)
				}

				current, present := store[key]
				if opt.OldValue == nil {
					if present {
						return false
					}
				} else if !present || !bytes.Equal(current, opt.OldValue) {
					return false
				}
			}

			store[key] = value
			faults.writes[key]++

			return true
		},
		func(key string, _ []byte, _ model.PluginKVSetOptions) *model.AppError {
			faults.lock.Lock()
			defer faults.lock.Unlock()

			if faults.writeFails[key] {
				return model.NewAppError("KVSetWithOptions", "kv.write", nil, "injected failure", 500)
			}

			return nil
		},
	)
	// Real compare-and-set semantics, guarded by the same lock the rest of the
	// mock uses, so a test can run genuine goroutines under -race and find out
	// whether a write was actually lost. Refused and counted like KVSet, so a
	// document written by compare-and-set can be failed and watched too.
	api.On("KVCompareAndSet", mock.Anything, mock.Anything, mock.Anything).Return(
		func(key string, oldValue, newValue []byte) bool {
			faults.lock.Lock()
			defer faults.lock.Unlock()

			faults.wrote(key)
			if faults.writeFails[key] {
				return false
			}

			if faults.interfere != nil {
				faults.interfere(key, store)
			}

			current, present := store[key]
			if oldValue == nil {
				if present {
					return false
				}
			} else if !present || !bytes.Equal(current, oldValue) {
				return false
			}

			store[key] = newValue
			faults.writes[key]++

			return true
		},
		func(key string, _, _ []byte) *model.AppError {
			faults.lock.Lock()
			defer faults.lock.Unlock()

			if faults.writeFails[key] {
				return model.NewAppError("KVCompareAndSet", "kv.write", nil, "injected failure", 500)
			}

			return nil
		},
	)
	api.On("KVCompareAndDelete", mock.Anything, mock.Anything).Return(
		func(key string, oldValue []byte) bool {
			faults.lock.Lock()
			defer faults.lock.Unlock()

			faults.wrote(key)
			if faults.writeFails[key] {
				return false
			}

			current, present := store[key]
			if !present || !bytes.Equal(current, oldValue) {
				return false
			}
			delete(store, key)
			faults.writes[key]++

			return true
		},
		func(key string, _ []byte) *model.AppError {
			faults.lock.Lock()
			defer faults.lock.Unlock()

			if faults.writeFails[key] {
				return model.NewAppError("KVCompareAndDelete", "kv.write", nil, "injected failure", 500)
			}

			return nil
		},
	)
	api.On("KVDelete", mock.Anything).Return(
		func(key string) *model.AppError {
			faults.lock.Lock()
			defer faults.lock.Unlock()

			faults.wrote(key)
			delete(store, key)

			return nil
		},
	)
	// Whoever's access changes is told, with an event of their own and
	// nothing in it (access.go). Allowed everywhere, so that a test that
	// links or unlinks somebody need not say so; a test about the event
	// reads api.Calls.
	api.On("PublishWebSocketEvent", wsEventMe, mock.Anything, mock.Anything).Return().Maybe()

	// The log methods are variadic and testify matches on the exact argument
	// count, so every arity the plugin actually uses has to be declared.
	for _, level := range []string{"LogWarn", "LogError", "LogInfo", "LogDebug"} {
		for pairs := 0; pairs <= 8; pairs++ {
			args := make([]any, 0, 1+(pairs*2))
			for i := 0; i < 1+(pairs*2); i++ {
				args = append(args, mock.Anything)
			}
			api.On(level, args...).Return()
		}
	}

	p := &Plugin{handleSecret: testHandleSecret}
	p.SetAPI(api)

	return p, store, faults
}

func leaderboardConfig() *configuration {
	cfg := &configuration{EnableLeaderboard: true, AlwaysOnStreamKeys: "videokey-watts"}
	cfg.normalize()

	return cfg
}

func live(key, title, start string) apiStream {
	return apiStream{StreamKey: key, Title: title, StreamStart: start}
}

// boardOf is the common case: every row of one Broadcast Box table, no
// identity cache and no Twitch.
func boardOf(p *Plugin, table statTable, cfg *configuration) []leaderEntry {
	rows, _ := p.leaderboard("all time", p.boardRows(table, nil, cfg, nil, nil), maxLeaderboardRows)

	return rows
}

func mustLoad(t *testing.T, p *Plugin, key string) statTable {
	t.Helper()

	table, err := p.loadStats(key)
	require.NoError(t, err)

	return table
}

func hours(p *Plugin, table string, key string) int64 {
	stored, err := p.loadStats(table)
	if err != nil {
		return -1
	}

	return stored[key].Seconds
}

// Time is credited from the session's own start, so consecutive polls add only
// what actually elapsed rather than a fixed interval.
func TestCreditsElapsedTimeNotPollInterval(t *testing.T) {
	p, _ := kvBackedPlugin()
	cfg := leaderboardConfig()

	start := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	streams := []apiStream{live("videokey-villa", "Villa", start.Format(time.RFC3339Nano))}

	p.recordStreamTime(streams, cfg, start.Add(30*time.Second))
	require.EqualValues(t, 30, hours(p, kvStatsTotals, "videokey-villa"))

	p.recordStreamTime(streams, cfg, start.Add(90*time.Second))
	require.EqualValues(t, 90, hours(p, kvStatsTotals, "videokey-villa"), "total tracks the session, not the number of polls")

	// A poll that arrives with no time passed must not double-count.
	p.recordStreamTime(streams, cfg, start.Add(90*time.Second))
	require.EqualValues(t, 90, hours(p, kvStatsTotals, "videokey-villa"))
}

// The plugin can be down for part of a session; streamStart is authoritative,
// so the missed time is recovered on the next poll.
func TestRecoversTimeMissedWhilePluginWasDown(t *testing.T) {
	p, store := kvBackedPlugin()
	cfg := leaderboardConfig()

	start := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	streams := []apiStream{live("videokey-villa", "Villa", start.Format(time.RFC3339Nano))}

	p.recordStreamTime(streams, cfg, start.Add(time.Minute))
	require.EqualValues(t, 60, hours(p, kvStatsTotals, "videokey-villa"))

	// New process over the same KV: the plugin was down for 19 minutes.
	restarted, _ := kvBackedPluginOn(store)

	restarted.recordStreamTime(streams, cfg, start.Add(20*time.Minute))
	require.EqualValues(t, 1200, hours(restarted, kvStatsTotals, "videokey-villa"),
		"the session is credited to 20 minutes total, not 60s + 20min")
}

// A restarted broadcast is a new session and starts from zero again.
func TestNewSessionRestartsCrediting(t *testing.T) {
	p, _ := kvBackedPlugin()
	cfg := leaderboardConfig()

	first := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	p.recordStreamTime([]apiStream{live("videokey-villa", "Villa", first.Format(time.RFC3339Nano))}, cfg, first.Add(10*time.Minute))
	require.EqualValues(t, 600, hours(p, kvStatsTotals, "videokey-villa"))

	second := first.Add(time.Hour)
	p.recordStreamTime([]apiStream{live("videokey-villa", "Villa", second.Format(time.RFC3339Nano))}, cfg, second.Add(5*time.Minute))
	require.EqualValues(t, 900, hours(p, kvStatsTotals, "videokey-villa"), "600 from the first session plus 300 from the second")
}

// The 24/7 channel would win every board by never stopping.
func TestAlwaysOnAndPausedStreamsAreNotCounted(t *testing.T) {
	p, _ := kvBackedPlugin()
	cfg := leaderboardConfig()

	start := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	watts := live("videokey-watts", "Alan Watts Chillstep", start.Format(time.RFC3339Nano))
	watts.AlwaysOn = true
	paused := live("videokey-dave", "Dave", start.Format(time.RFC3339Nano))
	paused.Paused = true

	p.recordStreamTime([]apiStream{watts, paused}, cfg, start.Add(time.Hour))

	require.Zero(t, hours(p, kvStatsTotals, "videokey-watts"), "the always-on channel must never place")
	require.Zero(t, hours(p, kvStatsTotals, "videokey-dave"), "a paused stream is not airtime")
	require.Empty(t, boardOf(p, mustLoad(t, p, kvStatsTotals), cfg))
}

func TestClockSkewAndImplausibleDurations(t *testing.T) {
	p, _ := kvBackedPlugin()
	cfg := leaderboardConfig()

	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)

	// A start in the future credits nothing rather than a negative total.
	future := live("videokey-villa", "Villa", now.Add(time.Hour).Format(time.RFC3339Nano))
	p.recordStreamTime([]apiStream{future}, cfg, now)
	require.Zero(t, hours(p, kvStatsTotals, "videokey-villa"))

	// A start years ago credits nothing at all. It used to be clamped to
	// maxCreditPerPoll, which wrote thirty hours of invented airtime off one
	// bad timestamp — and nothing in the plugin can ever take that back.
	ancient := live("videokey-peggy", "Peggy", now.AddDate(-3, 0, 0).Format(time.RFC3339Nano))
	p.recordStreamTime([]apiStream{ancient}, cfg, now)
	require.Zero(t, hours(p, kvStatsTotals, "videokey-peggy"), "an implausible duration is refused, not clamped")

	// The session is resynchronised rather than abandoned, so the time that
	// really does elapse afterwards is counted normally.
	p.recordStreamTime([]apiStream{ancient}, cfg, now.Add(5*time.Minute))
	require.EqualValues(t, 300, hours(p, kvStatsTotals, "videokey-peggy"))
}

// Time lands in the month it is credited in, so a session spanning midnight on
// the 1st does not dump the whole session into the new month.
func TestMonthlyBucketsAreSeparate(t *testing.T) {
	p, _ := kvBackedPlugin()
	cfg := leaderboardConfig()

	start := time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC)
	streams := []apiStream{live("videokey-villa", "Villa", start.Format(time.RFC3339Nano))}

	september := start.Add(30 * time.Minute)
	p.recordStreamTime(streams, cfg, september)

	october := start.Add(90 * time.Minute)
	p.recordStreamTime(streams, cfg, october)

	// The broadcast ran 23:00-24:00 on the 30th and 00:00-00:30 on the 1st, so
	// that is how it is split — by where the time actually fell, not by which
	// month the poll that noticed it happened to run in. The second poll covers
	// 23:30 to 00:30 and is divided at midnight.
	require.EqualValues(t, 3600, hours(p, monthBucket(september), "videokey-villa"), "the full hour it was on air in September")
	require.EqualValues(t, 1800, hours(p, monthBucket(october), "videokey-villa"), "and the half hour in October")
	require.EqualValues(t, 5400, hours(p, kvStatsTotals, "videokey-villa"), "all time is the sum")
	require.NotEqual(t, monthBucket(september), monthBucket(october))
}

// The same split has to hold when the gap is an outage rather than a poll
// interval: a plugin that was down across midnight on the 1st used to hand the
// whole recovered stretch to the new month.
func TestAnOutageAcrossAMonthBoundaryIsSplitCorrectly(t *testing.T) {
	p, _ := kvBackedPlugin()
	cfg := leaderboardConfig()

	start := time.Date(2026, 9, 30, 18, 0, 0, 0, time.UTC)
	streams := []apiStream{live("videokey-villa", "Villa", start.Format(time.RFC3339Nano))}

	// Two hours in, then the plugin is away until well into October.
	p.recordStreamTime(streams, cfg, start.Add(2*time.Hour))

	back := time.Date(2026, 10, 1, 4, 0, 0, 0, time.UTC)
	p.recordStreamTime(streams, cfg, back)

	september := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	october := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

	require.EqualValues(t, 6*3600, hours(p, monthBucket(september), "videokey-villa"), "18:00 to midnight is September's")
	require.EqualValues(t, 4*3600, hours(p, monthBucket(october), "videokey-villa"), "midnight to 04:00 is October's")
	require.EqualValues(t, 10*3600, hours(p, kvStatsTotals, "videokey-villa"))
}

// The boards list everyone who has streamed. The AWARD is still the top three:
// the hours were always recorded for everybody, only the display was cut.
func TestTheBoardListsEveryoneButOnlyThreePlace(t *testing.T) {
	p, _ := kvBackedPlugin()
	cfg := leaderboardConfig()

	table := statTable{
		"a": {Seconds: 100, Title: "Alpha"},
		"b": {Seconds: 500, Title: "Bravo"},
		"c": {Seconds: 300, Title: "Charlie"},
		"d": {Seconds: 900, Title: "Delta"},
		"e": {Seconds: 0, Title: "Echo"},
	}

	leaders := boardOf(p, table, cfg)

	require.Len(t, leaders, 4, "everyone with airtime, not just the podium")
	require.Equal(t,
		[]string{"Delta", "Bravo", "Charlie", "Alpha"},
		[]string{leaders[0].Title, leaders[1].Title, leaders[2].Title, leaders[3].Title})
	require.Equal(t, []int{1, 2, 3, 4}, []int{leaders[0].Rank, leaders[1].Rank, leaders[2].Rank, leaders[3].Rank})

	ranks := rankByRow(p.boardRows(table, nil, cfg, nil, nil))
	require.Equal(t, 1, ranks["k:d"])
	require.Equal(t, 3, ranks["k:c"])
	require.Zero(t, ranks["k:a"], "fourth place is listed, but wears no badge")
	require.Zero(t, ranks["k:e"], "zero airtime never places")
}

// The limit is a bound on the WebSocket message, not an editorial decision.
func TestTheBoardIsBoundedForTheWire(t *testing.T) {
	p, _ := kvBackedPlugin()
	cfg := leaderboardConfig()

	table := statTable{}
	for i := 0; i < maxLeaderboardRows+25; i++ {
		table["streamer-"+strconv.Itoa(i)] = statEntry{Seconds: int64(i + 1), Title: "S" + strconv.Itoa(i)}
	}

	rows, total := p.leaderboard("all time", p.boardRows(table, nil, cfg, nil, nil), maxLeaderboardRows)
	require.Len(t, rows, maxLeaderboardRows)
	require.Equal(t, len(table), total, "the true count comes back even when the rows are cut")

	all, _ := p.leaderboard("all time", p.boardRows(table, nil, cfg, nil, nil), 0)
	require.Len(t, all, len(table), "no limit means no cut")

	// The cut is logged once for each count a board reaches, not on every
	// poll; each board for itself.
	cuts := func() int {
		said := 0
		for _, call := range p.API.(*plugintest.API).Calls {
			if call.Method == "LogWarn" && call.Arguments.String(0) == "More streamers have airtime than the board will carry" {
				said++
			}
		}

		return said
	}
	for range 3 {
		p.leaderboard("all time", p.boardRows(table, nil, cfg, nil, nil), maxLeaderboardRows)
	}
	require.Equal(t, 1, cuts(), "said once, however many polls")
	p.leaderboard("this month", p.boardRows(table, nil, cfg, nil, nil), maxLeaderboardRows)
	require.Equal(t, 2, cuts(), "the other board says so for itself")
	table["streamer-new"] = statEntry{Seconds: 1, Title: "New"}
	p.leaderboard("all time", p.boardRows(table, nil, cfg, nil, nil), maxLeaderboardRows)
	require.Equal(t, 3, cuts(), "and again when the count moves")
}

// Equal totals must not make the podium flip order between polls.
func TestTiesAreBrokenStably(t *testing.T) {
	p, _ := kvBackedPlugin()
	cfg := leaderboardConfig()

	table := statTable{
		"x": {Seconds: 100, Title: "Zeta"},
		"y": {Seconds: 100, Title: "Alpha"},
	}

	first := boardOf(p, table, cfg)
	for i := 0; i < 5; i++ {
		require.Equal(t, first, boardOf(p, table, cfg))
	}
	require.Equal(t, "Alpha", first[0].Title)
}

// A leaderboard row must never carry a stream key.
func TestLeaderboardDoesNotDiscloseStreamKeys(t *testing.T) {
	p, _ := kvBackedPlugin()
	cfg := leaderboardConfig()

	start := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	p.recordStreamTime([]apiStream{live("videokey-secretkey", "", start.Format(time.RFC3339Nano))}, cfg, start.Add(time.Hour))

	leaders := boardOf(p, mustLoad(t, p, kvStatsTotals), cfg)
	require.Len(t, leaders, 1)
	require.NotContains(t, leaders[0].Title, "videokey-secretkey")
	require.Equal(t, "Unnamed stream", leaders[0].Title)
}

func TestLeaderboardDisabledRecordsNothing(t *testing.T) {
	p, _ := kvBackedPlugin()
	cfg := &configuration{EnableLeaderboard: false}
	cfg.normalize()

	start := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	p.recordStreamTime([]apiStream{live("videokey-villa", "Villa", start.Format(time.RFC3339Nano))}, cfg, start.Add(time.Hour))

	require.Zero(t, hours(p, kvStatsTotals, "videokey-villa"))
}

// The month a broadcast counts toward must not depend on the server's timezone.
func TestMonthBoundariesAreUTC(t *testing.T) {
	// 01:30 on 1 October in +0300 is still 22:30 on 30 September in UTC.
	plusThree := time.FixedZone("EEST", 3*60*60)
	local := time.Date(2026, 10, 1, 1, 30, 0, 0, plusThree)

	require.Equal(t, "stats_month_2026-09", monthBucket(local), "the UTC month wins, not the local one")
	require.Equal(t, "September 2026", monthLabel(local))

	// The same instant expressed in UTC must land in the same bucket.
	require.Equal(t, monthBucket(local), monthBucket(local.UTC()))
	require.Equal(t, monthLabel(local), monthLabel(local.UTC()))

	// And a zone the other side of UTC.
	minusEight := time.FixedZone("PST", -8*60*60)
	westward := time.Date(2026, 9, 30, 20, 0, 0, 0, minusEight) // 1 Oct 04:00 UTC
	require.Equal(t, "stats_month_2026-10", monthBucket(westward))
	require.Equal(t, "October 2026", monthLabel(westward))
}

// Recording must bucket by UTC too, not just the helpers.
func TestRecordingUsesUTCMonthBuckets(t *testing.T) {
	p, _ := kvBackedPlugin()
	cfg := leaderboardConfig()

	plusThree := time.FixedZone("EEST", 3*60*60)
	start := time.Date(2026, 10, 1, 0, 30, 0, 0, plusThree) // 30 Sep 21:30 UTC
	streams := []apiStream{live("videokey-villa", "Villa", start.Format(time.RFC3339Nano))}

	p.recordStreamTime(streams, cfg, start.Add(30*time.Minute))

	require.EqualValues(t, 1800, hours(p, "stats_month_2026-09", "videokey-villa"),
		"still September in UTC even though the server clock says October")
	require.Zero(t, hours(p, "stats_month_2026-10", "videokey-villa"))
}

// Equal seconds AND equal titles used to leave the order to Go's map iteration,
// which changes every time — so the snapshot fingerprint moved on every poll and
// pushed a WebSocket message to every client saying nothing had happened.
func TestIdenticalRowsStillHaveAStableOrder(t *testing.T) {
	table := statTable{
		"videokey-a": {Seconds: 100, Title: "Same"},
		"videokey-b": {Seconds: 100, Title: "same"},
		"videokey-c": {Seconds: 100, Title: "SAME"},
	}

	first := rankRows(table)
	for i := 0; i < 20; i++ {
		require.Equal(t, first, rankRows(table), "the ordering must not depend on map iteration")
	}
	require.Equal(t, []string{"videokey-a", "videokey-b", "videokey-c"},
		[]string{first[0].key, first[1].key, first[2].key})
}

// A title stored by a build that predates opaque handles could be the stream
// key itself, and the boards now show far more rows than the three that were
// always named after a linked account.
func TestAStoredTitleThatIsTheStreamKeyNeverReachesTheBoard(t *testing.T) {
	p, _ := kvBackedPlugin()
	cfg := &configuration{EnableLeaderboard: true}
	cfg.normalize()

	table := statTable{"videokey-ghost": {Seconds: 500, Title: "videokey-ghost"}}

	rows, _ := p.leaderboard("all time", p.boardRows(table, nil, cfg, nil, nil), maxLeaderboardRows)
	require.Len(t, rows, 1)
	require.Equal(t, "Unnamed stream", rows[0].Title)
	require.NotContains(t, rows[0].Title, "villanet")
}
