package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The recorder manager's tests drive it with stand-in recorders that connect
// to nothing and move only when a test says: a stream goes ready, drops, or
// is stopped when the test calls for it, and the end a stand-in hands back is
// a real ring played from the fixture, under the clips root, so that drafts
// are cut from it and its files can be seen to go. The manager is driven the
// way the poller drives it — reconcile with a poll's listing at a moment the
// test picks — and the panels are read the way they are told, through
// addClips. Nothing here waits on real time.

// fakeRecorder stands in for a recorder (clips_recorder.go).
type fakeRecorder struct {
	handle  string
	dir     string
	onEvent func(recorderEvent)

	mu       sync.Mutex
	starts   int
	ctx      context.Context
	cfg      *configuration // the settings it was started with
	stream   apiStream
	updates  int
	windows  []time.Duration
	wipes    int
	stops    int
	keptEnd  bool
	st       string
	from     time.Time
	why      string
	rate     float64
	heldSecs int64
	ring     *ring // the live ring, while ready
	end      *ring // what a stop that keeps the end hands back
	onStop   func()
	blockFor time.Duration // a stop waits this long, or until its context ends
}

func (f *fakeRecorder) start(ctx context.Context, stream apiStream, cfg *configuration) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.starts++
	f.ctx, f.stream, f.cfg = ctx, stream, cfg
	if f.st == "" {
		f.st = clipStateStarting
	}
}

func (f *fakeRecorder) update(stream apiStream) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.updates++
	f.stream = stream
}

func (f *fakeRecorder) setWindow(window time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.windows = append(f.windows, window)
}

// wipe cuts the live ring with nothing kept, as a recorder's does: nothing is
// held until the next keyframe.
func (f *fakeRecorder) wipe() {
	f.mu.Lock()
	f.wipes++
	live := f.ring
	f.ring, f.st, f.from = nil, clipStateStarting, time.Time{}
	f.mu.Unlock()

	if live != nil {
		live.remove()
	}
	f.onEvent(recorderEvent{kind: recorderChanged, handle: f.handle, state: clipStateStarting})
}

func (f *fakeRecorder) stop(ctx context.Context, keep bool) (clipTail, bool) {
	f.mu.Lock()
	f.stops++
	f.keptEnd = keep
	end, live, hook, block := f.end, f.ring, f.onStop, f.blockFor
	f.end, f.ring, f.onStop = nil, nil, nil
	stream := f.tailStreamLocked()
	f.mu.Unlock()

	if block > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(block):
		}
	}
	if hook != nil {
		hook()
	}
	if live != nil && live != end {
		live.remove()
	}
	if end == nil {
		return clipTail{}, false
	}
	if !keep {
		end.remove()
		return clipTail{}, false
	}

	return clipTail{ring: end, until: end.endTime().Add(tailKeep), stream: stream, reason: clipTailEnded}, true
}

// tailStreamLocked is the stream as a tail keeps it, as the recorder's
// tailStreamLocked has it.
func (f *fakeRecorder) tailStreamLocked() apiStream {
	s := f.stream

	return apiStream{
		Handle: f.handle, StreamKey: s.StreamKey, Title: s.Title, MOTD: s.MOTD, UserID: s.UserID,
		Username: s.Username, DisplayName: s.DisplayName, LinkSource: s.LinkSource, NowPlaying: s.NowPlaying,
	}
}

func (f *fakeRecorder) state() string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.st
}

func (f *fakeRecorder) clipFrom() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.st != clipStateReady {
		return time.Time{}
	}

	return f.from
}

func (f *fakeRecorder) reason() string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.why
}

func (f *fakeRecorder) bitrate() float64 {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.rate
}

func (f *fakeRecorder) heldSeconds() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.heldSecs
}

func (f *fakeRecorder) live() clipLive {
	f.mu.Lock()
	defer f.mu.Unlock()

	live := clipLive{state: f.st, bytesPerSecond: f.rate}
	if f.st == clipStateReady {
		live.ring = f.ring
	}

	return live
}

// became moves the recorder to a state, and tells the manager, as a recorder
// does.
func (f *fakeRecorder) became(state string, from time.Time, why string) {
	f.mu.Lock()
	f.st, f.from, f.why = state, from, why
	f.mu.Unlock()

	f.onEvent(recorderEvent{kind: recorderChanged, handle: f.handle, state: state, clipFrom: from})
}

// recording makes the recorder ready, recording into a live ring.
func (f *fakeRecorder) recording(live *ring) {
	f.mu.Lock()
	f.ring = live
	f.heldSecs = live.heldMs() / 1000
	f.rate = 12000
	f.mu.Unlock()

	f.became(clipStateReady, time.Unix(0, live.status().epochStart), "")
}

// handsOver hands the manager a tail, as a recorder does when its stream
// drops and carries on.
func (f *fakeRecorder) handsOver(r *ring, reason string) {
	f.mu.Lock()
	stream := f.tailStreamLocked()
	f.mu.Unlock()

	f.onEvent(recorderEvent{kind: recorderTailKept, handle: f.handle, tail: &clipTail{ring: r, until: r.endTime().Add(tailKeep), stream: stream, reason: reason}})
}

// holds gives the recorder an end to hand back when it is stopped.
func (f *fakeRecorder) holds(end *ring) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.end = end
}

// whenStopped runs hook part way through the next stop, as a recorder's last
// events arrive while it stops.
func (f *fakeRecorder) whenStopped(hook func()) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.onStop = hook
}

type fakeRecorderLook struct {
	starts, updates, wipes, stops int
	keptEnd                       bool
	stream                        apiStream
	windows                       []time.Duration
	ctx                           context.Context
	cfg                           *configuration
}

func (f *fakeRecorder) look() fakeRecorderLook {
	f.mu.Lock()
	defer f.mu.Unlock()

	return fakeRecorderLook{
		starts: f.starts, updates: f.updates, wipes: f.wipes, stops: f.stops, keptEnd: f.keptEnd,
		stream: f.stream, windows: append([]time.Duration(nil), f.windows...), ctx: f.ctx, cfg: f.cfg,
	}
}

// heldRing is a tail as a recorder hands one over: held of the fixture, its
// last frame ending at end, frozen, its segments in dir.
func heldRing(t *testing.T, dir string, end time.Time, held time.Duration) *ring {
	t.Helper()

	tp := newMediaTestPipe(t, 2*time.Minute, func(cfg *pipelineConfig) { cfg.dir = dir })
	stream := newMediaFixtureStream(t, tp)
	stream.start = end.Add(-held).UnixNano()
	stream.play(held)

	tail := tp.writer.finish(context.Background(), true)
	require.NotNil(t, tail)

	return tail
}

// liveRing is a recorder's live ring: held of the fixture, up to at, still
// being recorded, its segments in dir.
func liveRing(t *testing.T, dir string, at time.Time, held time.Duration) *ring {
	t.Helper()

	tp := newMediaTestPipe(t, 2*time.Minute, func(cfg *pipelineConfig) { cfg.dir = dir })
	stream := newMediaFixtureStream(t, tp)
	stream.start = at.Add(-held).UnixNano()
	stream.play(held)

	return tp.live()
}

// recordingWorld is a server where Clips has started on local storage, the
// people given have each linked a stream of their own ("uid-dave" streams as
// "videokey-dave"), Boss and Chief are system admins, and the recorder manager
// makes stand-ins rather than recorders.
type recordingWorld struct {
	*clipsWorld
	h *clipsHarness
	m *clipRecorders

	lock sync.Mutex
	made map[string][]*fakeRecorder
	dirs map[string]string

	// The handlers' clock: a moment the test set, or the real clock when it
	// set none. Guarded, because a link change works Best Clipper out again
	// on a goroutine of its own, which reads it.
	clockLock sync.Mutex
	clock     time.Time
}

func newRecordingWorld(t *testing.T, people ...string) *recordingWorld {
	t.Helper()

	w, h := startedClipsWorld(t, people...)
	clipsAdmins(w.api, "uid-boss", "uid-chief")

	cfg := w.p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = "http://192.168.1.20:8080"
	w.p.setConfiguration(cfg)

	rw := &recordingWorld{clipsWorld: w, h: h, m: w.p.recorders(), made: map[string][]*fakeRecorder{}, dirs: map[string]string{}, clock: clipsNow}
	w.p.clips().now = func() time.Time {
		rw.clockLock.Lock()
		defer rw.clockLock.Unlock()

		if rw.clock.IsZero() {
			return time.Now()
		}

		return rw.clock
	}
	rw.m.newRecorder = func(handle, dir string, onEvent func(recorderEvent)) clipRecorderHandle {
		f := &fakeRecorder{handle: handle, dir: dir, onEvent: onEvent}

		rw.lock.Lock()
		defer rw.lock.Unlock()

		rw.made[handle] = append(rw.made[handle], f)
		rw.dirs[handle] = dir

		return f
	}

	return rw
}

// setNow sets the handlers' clock to a moment, or to the real clock for the
// zero time.
func (rw *recordingWorld) setNow(at time.Time) {
	rw.clockLock.Lock()
	defer rw.clockLock.Unlock()

	rw.clock = at
}

func (rw *recordingWorld) handle(key string) string {
	return rw.p.streamHandle(key)
}

// onAir is a stream as a poll lists it once decorated, attributed to the
// person who linked its key when there is one: "videokey-dave" is Dave's.
func (rw *recordingWorld) onAir(key string, viewers int, started time.Time) apiStream {
	name := strings.TrimPrefix(key, "videokey-")
	stream := apiStream{
		StreamKey:   key,
		Handle:      rw.handle(key),
		Title:       strings.ToUpper(name[:1]) + name[1:],
		Viewers:     viewers,
		Bitrate:     6_000_000,
		AudioTracks: 1,
		VideoTracks: 1,
	}
	if !started.IsZero() {
		stream.StreamStart = started.UTC().Format(time.RFC3339Nano)
	}

	userID := "uid-" + name
	if linked := rw.kvGet(kvLinkByUserPrefix + userID); string(linked) == key {
		stream.UserID, stream.Username, stream.DisplayName, stream.LinkSource = userID, name, stream.Title, linkSourceManual
	}

	return stream
}

// poll is a successful poll at a moment, listing these streams: the manager
// reconciles with them, and the snapshot is what addClips leaves for every
// panel, published so that the routes find the streams on the air.
func (rw *recordingWorld) poll(t *testing.T, at time.Time, streams ...apiStream) *snapshot {
	t.Helper()

	rw.setNow(at)
	cfg := rw.p.getConfiguration()
	rw.m.reconcile(streams, cfg, at, true)

	// No FetchedAt: a real poll after it, on the real clock, must not be
	// taken for an older one and discarded.
	current := &snapshot{Streams: append([]apiStream{}, streams...), Configured: true}
	rw.p.addClips(current, cfg, at)
	rw.p.setSnapshot(current)

	return current
}

// recorder is the latest stand-in made for a stream's key.
func (rw *recordingWorld) recorder(t *testing.T, key string) *fakeRecorder {
	t.Helper()

	rw.lock.Lock()
	defer rw.lock.Unlock()

	made := rw.made[rw.handle(key)]
	require.NotEmpty(t, made, "a recorder was made for %s", key)

	return made[len(made)-1]
}

// madeFor is how many recorders have been made for a stream's key.
func (rw *recordingWorld) madeFor(key string) int {
	rw.lock.Lock()
	defer rw.lock.Unlock()

	return len(rw.made[rw.handle(key)])
}

// ringDir is where a stream's recordings are kept.
func (rw *recordingWorld) ringDir(key string) string {
	return filepath.Join(rw.h.root, clipsRingDir, rw.handle(key))
}

// watch opens a panel session on a stream at a moment, which a poll lists.
func (rw *recordingWorld) watch(sessionID, userID, key string, at time.Time) {
	rw.p.noteClipViewer(sessionID, userID, rw.handle(key), at)
	rw.p.noteClipSessions([]bbStream{rawWatched(key, sessionID)}, at)
}

// cut asks for a draft of a stream, live or from its held end.
func (rw *recordingWorld) cut(t *testing.T, userID, key, source string) *httptest.ResponseRecorder {
	t.Helper()

	return rw.call(t, userID, clipCall{
		method: http.MethodPost,
		target: clipsPath + "/drafts",
		body:   `{"handle":"` + rw.handle(key) + `","source":"` + source + `"}`,
	})
}

func (rw *recordingWorld) switchClipping(t *testing.T, userID string, allow bool) *httptest.ResponseRecorder {
	t.Helper()

	body := `{"allow":false}`
	if allow {
		body = `{"allow":true}`
	}

	return rw.call(t, userID, clipCall{method: http.MethodPut, target: clipsPath + "/streamer", body: body})
}

func (rw *recordingWorld) wipe(t *testing.T, userID, key string) *httptest.ResponseRecorder {
	t.Helper()

	return rw.call(t, userID, clipCall{method: http.MethodPost, target: clipsPath + "/wipe?handle=" + rw.handle(key)})
}

// logged is every line logged with this message, as its key/value pairs.
func (rw *recordingWorld) logged(level, msg string) [][]string {
	var lines [][]string
	for _, call := range rw.api.Calls {
		if call.Method != level || len(call.Arguments) == 0 || call.Arguments[0] != msg {
			continue
		}
		lines = append(lines, stringsOf(call.Arguments[1:]))
	}

	return lines
}

// requireNoKeyLogged checks that no line logged carries a stream key, in any
// case.
func (rw *recordingWorld) requireNoKeyLogged(t *testing.T, keys ...string) {
	t.Helper()

	for _, call := range rw.api.Calls {
		if !strings.HasPrefix(call.Method, "Log") {
			continue
		}
		line := strings.ToLower(strings.Join(stringsOf(call.Arguments), " "))
		for _, key := range keys {
			require.NotContains(t, line, strings.ToLower(key), call.Method)
		}
	}
}

// Recorders keep their slots for as long as their streams stay on the air,
// however the viewers move; a free slot goes to the most watched stream
// waiting, then the longest on the air, a start that cannot be read last,
// then by handle; a 24/7 channel, and a stream with no handle, are never
// recorded; and a stream there is no slot for is queued. A stream that leaves
// the listing is let go only once it has missed the offline grace, and its
// slot then goes to the next in line (spec §2.19 item 22).
func TestSlotsAreStickyAndGoToTheMostWatched(t *testing.T) {
	rw := newRecordingWorld(t, "uid-alice", "uid-heidi", "uid-carol", "uid-judy", "uid-eve")
	t0 := clipsNow

	alice := rw.onAir("videokey-alice", 1, t0)
	heidi := rw.onAir("videokey-heidi", 5, t0.Add(time.Minute))
	carol := rw.onAir("videokey-carol", 5, t0)
	judy := rw.onAir("videokey-judy", 3, time.Time{})
	judy.StreamStart = "not a time"
	eve := rw.onAir("videokey-eve", 3, t0.Add(2*time.Minute))
	watts := rw.onAir("videokey-watts", 9, t0)
	watts.AlwaysOn = true
	nameless := apiStream{StreamKey: "videokey-nameless", Viewers: 9}

	snap := rw.poll(t, t0.Add(5*time.Minute), alice, heidi, carol, judy, eve, watts, nameless)

	for _, key := range []string{"videokey-carol", "videokey-heidi", "videokey-eve", "videokey-judy"} {
		require.Equal(t, 1, rw.madeFor(key), key)
		look := rw.recorder(t, key).look()
		require.Equal(t, 1, look.starts, key)
		require.Equal(t, key, look.stream.StreamKey, key)
		require.Equal(t, rw.ringDir(key), rw.dirs[rw.handle(key)], "%s records into ring/<handle>", key)
		require.Equal(t, clipStateStarting, cardFor(t, snap.Streams, rw.handle(key)).Clip, key)
	}
	require.Zero(t, rw.madeFor("videokey-alice"))
	require.Equal(t, clipStateQueued, cardFor(t, snap.Streams, alice.Handle).Clip, "the least watched waits")
	require.Zero(t, rw.madeFor("videokey-watts"))
	require.Empty(t, cardFor(t, snap.Streams, watts.Handle).Clip, "a 24/7 channel is never recorded, and says nothing")
	require.Len(t, rw.made, 4)

	// Ready, its card says since when.
	live := liveRing(t, filepath.Join(rw.ringDir("videokey-carol"), "live"), t0.Add(5*time.Minute), 30*time.Second)
	rw.recorder(t, "videokey-carol").recording(live)
	snap = rw.poll(t, t0.Add(6*time.Minute), alice, heidi, carol, judy, eve, watts)
	card := cardFor(t, snap.Streams, carol.Handle)
	require.Equal(t, clipStateReady, card.Clip)
	require.Equal(t, time.Unix(0, live.status().epochStart).UnixMilli(), card.ClipFrom)
	require.Zero(t, cardFor(t, snap.Streams, heidi.Handle).ClipFrom, "since when only while ready")

	// Alice's audience grows past everybody's: the running recorders keep their
	// slots all the same, and each hears of its stream as the poll has it.
	alice.Viewers = 50
	heidi.Viewers, carol.Viewers = 0, 0
	snap = rw.poll(t, t0.Add(7*time.Minute), alice, heidi, carol, judy, eve)
	require.Zero(t, rw.madeFor("videokey-alice"))
	require.Equal(t, clipStateQueued, cardFor(t, snap.Streams, alice.Handle).Clip)
	for _, key := range []string{"videokey-carol", "videokey-heidi", "videokey-eve", "videokey-judy"} {
		look := rw.recorder(t, key).look()
		require.Zero(t, look.stops, key)
		require.Equal(t, 2, look.updates, key)
	}
	require.Equal(t, 0, rw.recorder(t, "videokey-heidi").look().stream.Viewers, "the stream as the poll has it")

	// Heidi leaves the listing. One poll is inside the grace; the second lets
	// her recorder go, keeping her end, and her slot goes to Alice in the same
	// poll.
	snap = rw.poll(t, t0.Add(8*time.Minute), alice, carol, judy, eve)
	require.Zero(t, rw.recorder(t, "videokey-heidi").look().stops, "one missed poll is inside the grace")
	require.Equal(t, clipStateQueued, cardFor(t, snap.Streams, alice.Handle).Clip)

	// Back within the grace, the count starts again.
	rw.poll(t, t0.Add(9*time.Minute), alice, heidi, carol, judy, eve)
	rw.poll(t, t0.Add(10*time.Minute), alice, carol, judy, eve)
	require.Zero(t, rw.recorder(t, "videokey-heidi").look().stops, "missed polls are counted in a row")

	snap = rw.poll(t, t0.Add(11*time.Minute), alice, carol, judy, eve)
	look := rw.recorder(t, "videokey-heidi").look()
	require.Equal(t, 1, look.stops)
	require.True(t, look.keptEnd, "a stream that went off keeps its end")
	require.Equal(t, 1, rw.madeFor("videokey-alice"))
	require.Equal(t, clipStateStarting, cardFor(t, snap.Streams, alice.Handle).Clip)
	require.Len(t, rw.logged("LogInfo", "A clip recorder stopped"), 1)
	require.Equal(t, []string{"stream_handle", heidi.Handle, "reason", whyStreamEnded}, rw.logged("LogInfo", "A clip recorder stopped")[0])

	// A stream that turns into a 24/7 channel stops at once, nothing kept.
	eve.AlwaysOn = true
	rw.poll(t, t0.Add(12*time.Minute), alice, carol, judy, eve)
	look = rw.recorder(t, "videokey-eve").look()
	require.Equal(t, 1, look.stops)
	require.False(t, look.keptEnd)

	// Two streams alike in viewers and start go by handle.
	same := []apiStream{
		{Handle: "ffff", Viewers: 2, StreamStart: "2026-10-02T20:00:00Z"},
		{Handle: "aaaa", Viewers: 2, StreamStart: "2026-10-02T20:00:00.000000000Z"},
		{Handle: "bbbb", Viewers: 2, StreamStart: ""},
		{Handle: "cccc", Viewers: 2, StreamStart: "2026-10-02T19:59:59Z"},
	}
	sortCandidates(same)
	require.Equal(t, []string{"cccc", "aaaa", "ffff", "bbbb"}, []string{same[0].Handle, same[1].Handle, same[2].Handle, same[3].Handle})

	rw.requireNoKeyLogged(t, "videokey-")
}

// polledRecordingWorld is a recordingWorld polled through the poller itself,
// against a Broadcast Box whose status listing is origin's, on the real
// clock, as pollOnce reads it.
func polledRecordingWorld(t *testing.T, origin *httptest.Server, people ...string) *recordingWorld {
	t.Helper()

	rw := newRecordingWorld(t, people...)
	rw.setNow(time.Time{})
	rw.p.httpClient = origin.Client()

	cfg := rw.p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = origin.URL
	cfg.OfflineGraceChecks = 2
	rw.p.setConfiguration(cfg)

	rw.api.On("KVList", mock.Anything, mock.Anything).Return([]string{}, nil).Maybe()
	rw.api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	return rw
}

// statusOrigin is a Broadcast Box whose status lists what listing says at the
// moment it is asked.
func statusOrigin(t *testing.T, listing func() []bbStream) *httptest.Server {
	t.Helper()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(listing())
	}))
	t.Cleanup(origin.Close)

	return origin
}

// A stream that goes off the air for good, through the poller itself. Its
// recorder is handed the stream with what is on it, as addNowPlaying put it
// there (A§4.14); once the stream has missed the offline grace the recorder
// stops and its end is held as a tail, which every snapshot lists by handle
// and which somebody who was watching when it ended may clip, as may its
// streamer, and nobody else. A draft cut from it says how the stream stopped
// and what was on it. Ten minutes after its end it goes, files and all
// (spec §2.19 item 23).
func TestEndedStreamCanBeClippedFromItsTail(t *testing.T) {
	var lock sync.Mutex
	onAir := true
	started := time.Now().Add(-time.Hour)
	origin := statusOrigin(t, func() []bbStream {
		lock.Lock()
		defer lock.Unlock()

		if !onAir {
			return []bbStream{}
		}
		dave := rawWatched("videokey-dave", "sess-bob")
		dave.StreamStart = started.UTC().Format(time.RFC3339Nano)

		return []bbStream{dave}
	})

	rw := polledRecordingWorld(t, origin, "uid-bob", "uid-dave", "uid-dan")
	dave := rw.handle("videokey-dave")

	// What is on Dave's stream, said with What's on?.
	rw.kvSet(kvNowPlaying, mustJSON(t, nowPlayingDoc{Version: 1, Entries: map[string]nowPlayingEntry{
		"uid-dave": {Category: categoryGame, Title: "Hytale", SetAt: time.Now().UnixMilli()},
	}}))

	rw.p.noteClipViewer("sess-bob", "uid-bob", dave, time.Now())
	rw.p.pollOnce(t.Context(), true)

	recorder := rw.recorder(t, "videokey-dave")
	look := recorder.look()
	require.Equal(t, 1, look.starts)
	require.Equal(t, "uid-dave", look.stream.UserID)
	require.Equal(t, &apiNowPlaying{Category: categoryGame, Title: "Hytale"}, look.stream.NowPlaying, "what is on, as the poll put it on the card")
	require.Equal(t, clipStateStarting, cardFor(t, rw.p.getSnapshot().Streams, dave).Clip)

	// Forty seconds are held when the publisher goes.
	end := time.Now()
	recorder.holds(heldRing(t, recorder.dir, end, 40*time.Second))

	lock.Lock()
	onAir = false
	lock.Unlock()

	rw.p.pollOnce(t.Context(), true)
	require.Zero(t, recorder.look().stops, "one poll off the air is inside the grace")
	require.Empty(t, rw.p.getSnapshot().ClipTails)

	rw.p.pollOnce(t.Context(), true)
	look = recorder.look()
	require.Equal(t, 1, look.stops)
	require.True(t, look.keptEnd)

	snap := rw.p.getSnapshot()
	require.Len(t, snap.ClipTails, 1)
	tail := snap.ClipTails[0]
	require.Equal(t, dave, tail.Handle)
	require.Equal(t, "Dave", tail.Title)
	require.Equal(t, "uid-dave", tail.UserID)
	require.Equal(t, clipTailEnded, tail.Reason)
	require.InDelta(t, end.UnixMilli(), tail.EndedAt, 100)
	require.Equal(t, tailKeep.Milliseconds(), tail.Until-tail.EndedAt)
	require.InDelta(t, 40000, tail.HeldMs, 100)

	encoded, err := json.Marshal(snap)
	require.NoError(t, err)
	require.NotContains(t, strings.ToLower(string(encoded)), "videokey-dave")
	require.NotContains(t, string(encoded), rw.p.streamTag("videokey-dave"))

	// Bob, who was watching, clips its end: the clip length before it, or
	// all of it, and what was on it.
	draft := decodeDraft(t, rw.cut(t, "uid-bob", "videokey-dave", clipSourceTail), http.StatusCreated)
	require.Equal(t, clipSourceTail, draft.Source)
	require.Equal(t, clipTailEnded, draft.TailReason)
	require.Equal(t, &clipTag{Category: categoryGame, Title: "Hytale"}, draft.Tag)
	require.Equal(t, "Dave", draft.Streamer.Name)
	require.InDelta(t, tail.EndedAt, draft.StartAt+draft.DurationMs, 70)

	// Dan was not watching. Dave, its streamer, needs no session.
	requireClipError(t, rw.cut(t, "uid-dan", "videokey-dave", clipSourceTail), http.StatusForbidden, clipErrNotWatching, "not watching")
	decodeDraft(t, rw.cut(t, "uid-dave", "videokey-dave", clipSourceTail), http.StatusCreated)

	// Its time up, it goes at the next housekeeping, files and all.
	later := end.Add(tailKeep)
	rw.setNow(later)
	rw.p.housekeepClips(later)
	requireClipError(t, rw.cut(t, "uid-bob", "videokey-dave", clipSourceTail), http.StatusGone, clipErrTailGone, "gone")
	current := &snapshot{Streams: []apiStream{}}
	rw.p.addClips(current, rw.p.getConfiguration(), later)
	require.Empty(t, current.ClipTails)
	require.Empty(t, namesIn(t, recorder.dir), "its segments went with it")

	rw.requireNoKeyLogged(t, "videokey-dave")
}

// A recorder hands over the end of a stream that dropped and carried on: it
// is held for the stream's card, which says until when, and listed as
// dropped, and a draft cut from it says so. A newer one replaces it, the older
// deleted at once. One too short to clip, one a wipe deleted as it was handed
// over, and one from a recorder being let go of without keeping anything are
// not held (spec §2.19 item 23, §2.4.8). One too short to clip never reaches
// the manager: the writer deletes it (TestShortEpochIsNotKeptAsATail).
func TestDroppedEpochBecomesATail(t *testing.T) {
	rw := newRecordingWorld(t, "uid-bob", "uid-dave")
	at := clipsNow
	dave := rw.onAir("videokey-dave", 1, at.Add(-time.Hour))
	rw.poll(t, at, dave)
	recorder := rw.recorder(t, "videokey-dave")
	rw.watch("sess-bob", "uid-bob", "videokey-dave", at.Add(-30*time.Second))

	first := heldRing(t, filepath.Join(recorder.dir, "first"), at.Add(-time.Minute), 30*time.Second)
	recorder.handsOver(first, clipTailDropped)

	second := heldRing(t, filepath.Join(recorder.dir, "second"), at, 40*time.Second)
	recorder.handsOver(second, clipTailDropped)
	require.True(t, first.isDeleted(), "a newer end replaces the older")
	require.Empty(t, namesIn(t, filepath.Join(recorder.dir, "first")))

	snap := rw.poll(t, at.Add(time.Second), dave)
	card := cardFor(t, snap.Streams, dave.Handle)
	require.Equal(t, clipStateStarting, card.Clip, "recording again from the next keyframe")
	require.Equal(t, second.endTime().Add(tailKeep).UnixMilli(), card.ClipTail)
	require.Len(t, snap.ClipTails, 1)
	require.Equal(t, clipTailDropped, snap.ClipTails[0].Reason)

	draft := decodeDraft(t, rw.cut(t, "uid-bob", "videokey-dave", clipSourceTail), http.StatusCreated)
	require.Equal(t, clipTailDropped, draft.TailReason)
	require.Equal(t, "uid-dave", draft.Streamer.UserID)

	// A live draft says nothing of how a stream stopped.
	live := liveRing(t, filepath.Join(recorder.dir, "live"), at.Add(time.Second), 30*time.Second)
	recorder.recording(live)
	fromLive := decodeDraft(t, rw.cut(t, "uid-bob", "videokey-dave", clipSourceLive), http.StatusCreated)
	require.Empty(t, fromLive.TailReason)

	// Deleted by a wipe as it was handed over: gone, the older one staying.
	wiped := heldRing(t, filepath.Join(recorder.dir, "wiped"), at.Add(2*time.Second), 20*time.Second)
	wiped.remove()
	recorder.handsOver(wiped, clipTailDropped)
	held, ok := rw.m.tail(dave.Handle, at.Add(3*time.Second))
	require.True(t, ok)
	require.Same(t, second, held.ring)

	// Its streamer switches clipping off while it stops: whatever it hands
	// over on the way out goes too.
	lastWords := heldRing(t, filepath.Join(recorder.dir, "last"), at.Add(3*time.Second), 20*time.Second)
	recorder.whenStopped(func() { recorder.handsOver(lastWords, clipTailDropped) })
	rw.setNow(at.Add(4 * time.Second))
	require.Equal(t, http.StatusOK, rw.switchClipping(t, "uid-dave", false).Code)
	require.True(t, lastWords.isDeleted())
	_, ok = rw.m.tail(dave.Handle, at.Add(4*time.Second))
	require.False(t, ok)
}

// At most four tails are held, the oldest going to make room; each goes ten
// minutes after its end; and while rings and tails together hold more than
// their budget, tails go oldest first (spec §2.19 item 23, §2.4.8, §2.13).
func TestTailsExpireAndAreBounded(t *testing.T) {
	keys := []string{"videokey-alice", "videokey-heidi", "videokey-carol", "videokey-judy", "videokey-eve"}
	rw := newRecordingWorld(t, "uid-alice", "uid-heidi", "uid-carol", "uid-judy", "uid-eve")
	at := clipsNow

	// A second apart, so Eve, the last to start, is the one left waiting.
	var streams []apiStream
	for i, key := range keys {
		streams = append(streams, rw.onAir(key, 1, at.Add(-time.Hour+time.Duration(i)*time.Second)))
	}
	rw.poll(t, at, streams...)
	require.Zero(t, rw.madeFor("videokey-eve"), "queued behind four")

	// Four drop, a minute apart: four held.
	ends := map[string]*ring{}
	for i, key := range keys[:4] {
		recorder := rw.recorder(t, key)
		ends[key] = heldRing(t, filepath.Join(recorder.dir, "dropped"), at.Add(time.Duration(i)*time.Minute), 20*time.Second)
		recorder.handsOver(ends[key], clipTailDropped)
	}
	require.Len(t, rw.m.tailViews(at.Add(4*time.Minute), time.Minute), 4)

	// Alice's stream goes off, keeping nothing more, and Eve's is recorded in
	// her place. Alice's end stays held: tails outlive their recorders.
	rw.poll(t, at.Add(4*time.Minute), streams[1:]...)
	rw.poll(t, at.Add(4*time.Minute+time.Second), streams[1:]...)
	require.Equal(t, 1, rw.recorder(t, "videokey-alice").look().stops)
	require.Len(t, rw.m.tailViews(at.Add(4*time.Minute), time.Minute), 4)

	// A fifth: Alice's, the oldest, goes.
	eve := rw.recorder(t, "videokey-eve")
	ends["videokey-eve"] = heldRing(t, filepath.Join(eve.dir, "dropped"), at.Add(4*time.Minute), 20*time.Second)
	eve.handsOver(ends["videokey-eve"], clipTailDropped)

	views := rw.m.tailViews(at.Add(4*time.Minute), time.Minute)
	require.Len(t, views, 4)
	require.True(t, ends["videokey-alice"].isDeleted())
	require.Equal(t, rw.handle("videokey-eve"), views[0].Handle, "newest first")
	require.Equal(t, rw.handle("videokey-heidi"), views[3].Handle)
	require.InDelta(t, 20000, views[0].HeldMs, 70)
	require.Equal(t, int64(15000), rw.m.tailViews(at.Add(4*time.Minute), 15*time.Second)[0].HeldMs, "no more than one press takes")

	// Ten minutes after Heidi's end, hers goes at the housekeeping; the rest
	// stay.
	rw.p.housekeepClips(ends["videokey-heidi"].endTime().Add(tailKeep))
	require.True(t, ends["videokey-heidi"].isDeleted())
	require.Len(t, rw.m.tailViews(ends["videokey-heidi"].endTime().Add(tailKeep), time.Minute), 3)

	// Over the budget, the oldest go until what is held fits.
	size := ends["videokey-carol"].size()
	require.Positive(t, size)
	rw.m.mu.Lock()
	rw.m.budget = 2*size + size/2
	rw.m.mu.Unlock()
	rw.p.housekeepClips(ends["videokey-heidi"].endTime().Add(tailKeep))
	require.True(t, ends["videokey-carol"].isDeleted(), "the oldest left")
	require.False(t, ends["videokey-judy"].isDeleted())
	require.False(t, ends["videokey-eve"].isDeleted())

	// And a tail handed over past the budget makes room the same way.
	carol := rw.recorder(t, "videokey-carol")
	again := heldRing(t, filepath.Join(carol.dir, "again"), at.Add(6*time.Minute), 20*time.Second)
	carol.handsOver(again, clipTailDropped)
	require.True(t, ends["videokey-judy"].isDeleted())
	require.False(t, again.isDeleted())
	require.Len(t, rw.m.tailViews(at.Add(7*time.Minute), time.Minute), 2)
}

// me is /me as somebody's panel asks for it.
func (rw *recordingWorld) me(t *testing.T, userID string) meResponse {
	t.Helper()

	recorder := httptest.NewRecorder()
	rw.p.handleMe(recorder, userID)
	require.Equal(t, http.StatusOK, recorder.Code)

	var answer meResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer))

	return answer
}

// draftOf is a draft as this node holds it, whatever state it is in.
func (rw *recordingWorld) draftOf(id string) clipDraft {
	drafts := &rw.p.clips().drafts
	drafts.mu.Lock()
	defer drafts.mu.Unlock()

	draft := drafts.byID[id]
	if draft == nil {
		return clipDraft{}
	}

	return *draft
}

// publishing puts a draft in the middle of being published, as a publish
// waiting for a slot leaves it.
func (rw *recordingWorld) publishing(id string) {
	drafts := &rw.p.clips().drafts
	drafts.mu.Lock()
	defer drafts.mu.Unlock()

	drafts.byID[id].state = draftPublishing
}

// cutting is a draft whose export is still running: its place held, as a
// click waiting for a slot holds one.
func (rw *recordingWorld) cutting(t *testing.T, userID string, stream apiStream) string {
	t.Helper()

	draft, err := rw.p.clips().drafts.reserve(userID, clipSourceLive, rw.p.captureOf(stream), func(string) bool { return false }, rw.p.clips().runContext())
	require.NoError(t, err)

	return draft.id
}

func (rw *recordingWorld) draftVideoOf(t *testing.T, userID, id string) *httptest.ResponseRecorder {
	t.Helper()

	return rw.call(t, userID, clipCall{method: http.MethodGet, target: clipsPath + "/draft/video?id=" + id})
}

// A streamer switching clipping of their stream off stops it at once,
// without waiting for a poll: its recorder stops with nothing kept, what is
// held of it goes, its end included, and every draft cut from it, by
// anybody, goes as wiped — one being cut and one being published cancelled.
// Another stream is untouched. The switch is kept by the stream's tag; while
// it is off nothing records the stream, and nobody clips it; switched on, the
// next poll records it again (spec §2.19 item 24, §2.10.6, A§3.3, A§3.5).
func TestStreamerOptOutStopsRecordingAndWipes(t *testing.T) {
	rw := newRecordingWorld(t, "uid-bob", "uid-dave", "uid-dan")
	at := clipsNow
	dave := rw.onAir("videokey-dave", 2, at.Add(-time.Hour))
	dan := rw.onAir("videokey-dan", 1, at.Add(-time.Hour))
	rw.poll(t, at, dave, dan)

	daveRecorder, danRecorder := rw.recorder(t, "videokey-dave"), rw.recorder(t, "videokey-dan")
	daveLive := liveRing(t, filepath.Join(daveRecorder.dir, "live"), at, time.Minute)
	daveRecorder.recording(daveLive)
	danRecorder.recording(liveRing(t, filepath.Join(danRecorder.dir, "live"), at, time.Minute))
	dropped := heldRing(t, filepath.Join(daveRecorder.dir, "dropped"), at.Add(-90*time.Second), 20*time.Second)
	daveRecorder.handsOver(dropped, clipTailDropped)

	// Bob has been watching Dave since before his stream dropped.
	rw.p.noteClipViewer("sess-bob-dave", "uid-bob", dave.Handle, at.Add(-2*time.Minute))
	rw.p.noteClipSessions([]bbStream{rawWatched("videokey-dave", "sess-bob-dave"), rawWatched("videokey-dan")}, at)

	ready := decodeDraft(t, rw.cut(t, "uid-bob", "videokey-dave", clipSourceLive), http.StatusCreated)
	fromEnd := decodeDraft(t, rw.cut(t, "uid-bob", "videokey-dave", clipSourceTail), http.StatusCreated)
	published := decodeDraft(t, rw.cut(t, "uid-bob", "videokey-dave", clipSourceLive), http.StatusCreated)
	rw.publishing(published.ID)
	beingCut := rw.cutting(t, "uid-dan", dave)
	dans := decodeDraft(t, rw.cut(t, "uid-dan", "videokey-dan", clipSourceLive), http.StatusCreated)

	response := rw.switchClipping(t, "uid-dave", false)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.JSONEq(t, `{"allowed":false}`, response.Body.String())

	// Kept by the stream's tag, never by person or handle.
	require.Equal(t, map[string]clipMark{rw.p.streamTag("videokey-dave"): {By: "uid-dave", At: at.UnixMilli()}}, rw.stored(t).OptOut)

	// Stopped, nothing kept, his end gone with it; Dan's carries on.
	look := daveRecorder.look()
	require.Equal(t, 1, look.stops)
	require.False(t, look.keptEnd)
	require.True(t, daveLive.isDeleted())
	require.True(t, dropped.isDeleted())
	_, held := rw.m.tail(dave.Handle, at)
	require.False(t, held)
	require.Zero(t, danRecorder.look().stops)

	// Every draft cut from it, whoever cut it, as wiped.
	for _, id := range []string{ready.ID, fromEnd.ID} {
		requireClipError(t, rw.draftVideoOf(t, "uid-bob", id), http.StatusGone, clipErrDraftWiped, "a draft of the stream")
		require.False(t, exists(filepath.Join(rw.h.root, clipsDraftsDir, id+clipVideoExt)))
	}
	cut := rw.draftOf(beingCut)
	require.Equal(t, draftWiped, cut.state, "an export under way is cancelled")
	require.Error(t, cut.ctx.Err())
	publishing := rw.draftOf(published.ID)
	require.Equal(t, draftPublishing, publishing.state, "a publish under way finds out when it next looks")
	require.Equal(t, draftWiped, publishing.discardTo)
	require.Error(t, publishing.ctx.Err(), "and is cancelled")
	gone, discarded := rw.p.clips().drafts.endPublish(published.ID, "", at)
	require.True(t, discarded)
	require.Equal(t, draftWiped, gone.state)

	require.Equal(t, http.StatusOK, rw.draftVideoOf(t, "uid-dan", dans.ID).Code, "another stream's draft stays")

	require.Equal(t, [][]string{{"user_id", "uid-dave", "drafts_removed", "4"}}, loggedPairs(rw, "LogInfo", "A streamer switched clipping of their stream off"))

	// Every panel hears at once that the stream is switched off.
	current := &snapshot{Streams: []apiStream{dave, dan}}
	rw.p.addClips(current, rw.p.getConfiguration(), at)
	require.Equal(t, clipStateOff, cardFor(t, current.Streams, dave.Handle).Clip)
	require.Zero(t, cardFor(t, current.Streams, dave.Handle).ClipTail)
	require.Empty(t, current.ClipTails)
	require.Equal(t, clipStateReady, cardFor(t, current.Streams, dan.Handle).Clip)
	require.Equal(t, &clipsStreamerView{Allowed: false}, rw.me(t, "uid-dave").ClipsStreamer)

	// Polls go on finding it off: nothing records it, and nobody clips it.
	snap := rw.poll(t, at.Add(15*time.Second), dave, dan)
	require.Equal(t, 1, rw.madeFor("videokey-dave"))
	require.Equal(t, clipStateOff, cardFor(t, snap.Streams, dave.Handle).Clip)
	requireClipError(t, rw.cut(t, "uid-bob", "videokey-dave", clipSourceLive), http.StatusForbidden, clipErrStreamerOff, "switched off")

	// Again: nothing changes, nothing is written.
	writes := rw.faults.writesTo(kvClips)
	require.Equal(t, http.StatusOK, rw.switchClipping(t, "uid-dave", false).Code)
	require.Equal(t, writes, rw.faults.writesTo(kvClips))

	// Back on: the switch goes, and the next poll records it again.
	response = rw.switchClipping(t, "uid-dave", true)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"allowed":true}`, response.Body.String())
	require.Empty(t, rw.stored(t).OptOut)
	require.Len(t, loggedPairs(rw, "LogInfo", "A streamer switched clipping of their stream back on"), 1)

	snap = rw.poll(t, at.Add(30*time.Second), dave, dan)
	require.Equal(t, 2, rw.madeFor("videokey-dave"))
	require.Equal(t, clipStateStarting, cardFor(t, snap.Streams, dave.Handle).Clip)
	require.Equal(t, &clipsStreamerView{Allowed: true}, rw.me(t, "uid-dave").ClipsStreamer)

	rw.requireNoKeyLogged(t, "videokey-dave", "videokey-dan")
}

// loggedPairs is every line logged with this message, as its key/value
// pairs, numbers written out.
func loggedPairs(rw *recordingWorld, level, msg string) [][]string {
	var lines [][]string
	for _, call := range rw.api.Calls {
		if call.Method != level || len(call.Arguments) == 0 || call.Arguments[0] != msg {
			continue
		}

		var pairs []string
		for _, value := range call.Arguments[1:] {
			pairs = append(pairs, strings.TrimSpace(strings.Trim(fmt.Sprint(value), "[]")))
		}
		lines = append(lines, pairs)
	}

	return lines
}

// A wipe deletes what is held of one stream and nothing else: its live ring
// is cut with nothing kept, recording carrying on from the next keyframe; its
// held end goes; and every draft cut from it, by anybody, goes as wiped, one
// being published cancelled. Its published clips stay, and so does every
// other stream's recording and drafts. Its streamer can wipe its held end
// once it has gone off the air, and an admin can wipe any stream from the
// System Console (spec §2.19 item 24, §2.10.6).
func TestWipeDiscardsDraftsOfThatStream(t *testing.T) {
	rw := newRecordingWorld(t, "uid-bob", "uid-dave", "uid-dan")
	at := clipsNow
	dave := rw.onAir("videokey-dave", 2, at.Add(-time.Hour))
	dan := rw.onAir("videokey-dan", 1, at.Add(-time.Hour))
	rw.poll(t, at, dave, dan)

	daveRecorder, danRecorder := rw.recorder(t, "videokey-dave"), rw.recorder(t, "videokey-dan")
	daveLive := liveRing(t, filepath.Join(daveRecorder.dir, "live"), at, time.Minute)
	daveRecorder.recording(daveLive)
	danRecorder.recording(liveRing(t, filepath.Join(danRecorder.dir, "live"), at, time.Minute))
	dropped := heldRing(t, filepath.Join(daveRecorder.dir, "dropped"), at.Add(-90*time.Second), 20*time.Second)
	daveRecorder.handsOver(dropped, clipTailDropped)
	rw.p.noteClipViewer("sess-bob-dave", "uid-bob", dave.Handle, at.Add(-2*time.Minute))
	rw.p.noteClipSessions([]bbStream{rawWatched("videokey-dave", "sess-bob-dave"), rawWatched("videokey-dan")}, at)

	kept := decodeDraft(t, rw.cut(t, "uid-bob", "videokey-dave", clipSourceLive), http.StatusCreated)
	clip := decodeClipView(t, rw.call(t, "uid-bob", clipCall{method: http.MethodPost, target: clipsPath + "/draft/publish?id=" + kept.ID, body: publishBody("kept", 0, 10000)}), http.StatusCreated)
	ready := decodeDraft(t, rw.cut(t, "uid-bob", "videokey-dave", clipSourceTail), http.StatusCreated)
	published := decodeDraft(t, rw.cut(t, "uid-bob", "videokey-dave", clipSourceLive), http.StatusCreated)
	rw.publishing(published.ID)
	dans := decodeDraft(t, rw.cut(t, "uid-dan", "videokey-dan", clipSourceLive), http.StatusCreated)

	response := rw.wipe(t, "uid-dave", "videokey-dave")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.JSONEq(t, `{"ok":true,"draftsRemoved":2}`, response.Body.String())

	// The live ring is cut and recording carries on; the held end goes.
	look := daveRecorder.look()
	require.Equal(t, 1, look.wipes)
	require.Zero(t, look.stops, "recording carries on")
	require.True(t, daveLive.isDeleted())
	require.True(t, dropped.isDeleted())
	_, held := rw.m.tail(dave.Handle, at)
	require.False(t, held)
	requireClipError(t, rw.cut(t, "uid-bob", "videokey-dave", clipSourceLive), http.StatusConflict, clipErrWarmingUp, "nothing held until the next keyframe")

	// Its drafts, as wiped; the one being published cancelled.
	requireClipError(t, rw.draftVideoOf(t, "uid-bob", ready.ID), http.StatusGone, clipErrDraftWiped, "wiped")
	require.Equal(t, draftWiped, rw.draftOf(published.ID).discardTo)
	require.Error(t, rw.draftOf(published.ID).ctx.Err())

	// Its published clip stays; Dan's stream and draft are untouched.
	require.Equal(t, http.StatusOK, rw.call(t, "uid-dan", clipCall{method: http.MethodGet, target: clipsPath + "/clip?id=" + clip.ID}).Code)
	require.Zero(t, danRecorder.look().wipes)
	require.Equal(t, http.StatusOK, rw.draftVideoOf(t, "uid-dan", dans.ID).Code)

	require.Equal(t, [][]string{{"user_id", "uid-dave", "stream_handle", dave.Handle, "drafts_removed", "2"}}, loggedPairs(rw, "LogInfo", "A stream's held recording was wiped"))

	// Off the air, only its held end is left, and its streamer wipes that.
	end := heldRing(t, filepath.Join(daveRecorder.dir, "end"), at.Add(30*time.Second), 20*time.Second)
	daveRecorder.holds(end)
	rw.poll(t, at.Add(45*time.Second), dan)
	rw.poll(t, at.Add(60*time.Second), dan)
	_, held = rw.m.tail(dave.Handle, at.Add(time.Minute))
	require.True(t, held)
	response = rw.wipe(t, "uid-dave", "videokey-dave")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.True(t, end.isDeleted())

	// An admin wipes Dan's from the console, linked or not.
	response = rw.call(t, "uid-chief", clipCall{method: http.MethodPost, target: adminClipsPath + "/wipe?handle=" + dan.Handle})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"ok":true,"draftsRemoved":1}`, response.Body.String())
	require.Equal(t, 1, danRecorder.look().wipes)
	requireClipError(t, rw.draftVideoOf(t, "uid-dan", dans.ID), http.StatusGone, clipErrDraftWiped, "wiped by an admin")

	rw.requireNoKeyLogged(t, "videokey-dave", "videokey-dan")
}

// Only the stream's streamer — whoever's link is to its key now — may wipe
// it, or a linked admin from the panel, or any admin from the System Console.
// Somebody watching it, somebody it is attributed to by username, and an
// unlinked admin in the panel may not. The switch acts on the caller's own
// linked stream and no other: there is nothing to say whose (spec §2.19 item
// 24, renamed from TestOnlyManualStreamersMayOptOutOrWipe; A§3.3).
func TestOnlyTheLinkedStreamerMayOptOutOrWipe(t *testing.T) {
	rw := newRecordingWorld(t, "uid-bob", "uid-dave", "uid-dan", "uid-boss")
	rw.person("uid-nobody")
	rw.person("uid-chief")
	at := clipsNow

	dave := rw.onAir("videokey-dave", 2, at.Add(-time.Hour))

	// A stream with no link, attributed to Dan by her username; Dan has
	// linked a stream of her own.
	danByName := rw.onAir("dan", 1, at.Add(-time.Hour))
	danByName.UserID, danByName.Username, danByName.LinkSource = "uid-dan", "dan", linkSourceUsername
	rw.poll(t, at, dave, danByName)

	recorder := rw.recorder(t, "videokey-dave")
	dropped := heldRing(t, filepath.Join(recorder.dir, "dropped"), at.Add(-time.Minute), 20*time.Second)
	recorder.handsOver(dropped, clipTailDropped)
	rw.watch("sess-bob", "uid-bob", "videokey-dave", at)

	untouched := func(about string) {
		t.Helper()

		require.Zero(t, recorder.look().wipes, about)
		require.Zero(t, recorder.look().stops, about)
		require.False(t, dropped.isDeleted(), about)
	}

	// Not linked: refused before anything, an admin as much as anybody.
	for _, userID := range []string{"uid-nobody", "uid-chief"} {
		requireClipError(t, rw.wipe(t, userID, "videokey-dave"), http.StatusForbidden, clipErrLocked, userID)
		requireClipError(t, rw.switchClipping(t, userID, false), http.StatusForbidden, clipErrLocked, userID)
	}
	untouched("not linked")

	// Watching it is not being its streamer.
	requireClipError(t, rw.wipe(t, "uid-bob", "videokey-dave"), http.StatusForbidden, clipErrNotAllowed, "watching")
	untouched("watching")

	// Nor is being named on it by username.
	requireClipError(t, rw.wipe(t, "uid-dan", "dan"), http.StatusForbidden, clipErrNotAllowed, "attributed by username")
	require.Zero(t, rw.recorder(t, "dan").look().wipes)

	// A handle that is not one, a stream nobody knows, and a switch that does
	// not say which way.
	requireClipError(t, rw.call(t, "uid-dave", clipCall{method: http.MethodPost, target: clipsPath + "/wipe?handle=zz"}), http.StatusBadRequest, clipErrBadRequest, "no handle")
	requireClipError(t, rw.wipe(t, "uid-dave", "videokey-nobody-knows"), http.StatusForbidden, clipErrNotAllowed, "nobody's stream")
	for _, body := range []string{`{}`, `{"allow":"no"}`, `not json`} {
		response := rw.call(t, "uid-dave", clipCall{method: http.MethodPut, target: clipsPath + "/streamer", body: body})
		requireClipError(t, response, http.StatusBadRequest, clipErrBadRequest, body)
	}
	untouched("refused")

	// Bob's switch is for Bob's stream: Dave's carries on.
	require.Equal(t, http.StatusOK, rw.switchClipping(t, "uid-bob", false).Code)
	require.Equal(t, []string{rw.p.streamTag("videokey-bob")}, mapKeys(rw.stored(t).OptOut))
	untouched("somebody else switched their own stream off")

	// A linked admin, from the panel.
	response := rw.wipe(t, "uid-boss", "videokey-dave")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 1, recorder.look().wipes)
	require.True(t, dropped.isDeleted())

	// Its streamer.
	response = rw.wipe(t, "uid-dave", "videokey-dave")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 2, recorder.look().wipes)

	// Any admin, from the System Console.
	response = rw.call(t, "uid-chief", clipCall{method: http.MethodPost, target: adminClipsPath + "/wipe?handle=" + dave.Handle})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 3, recorder.look().wipes)
}

func mapKeys[V any](values map[string]V) []string {
	keys := []string{}
	for key := range values {
		keys = append(keys, key)
	}

	return keys
}

// Streamer rights follow the link as it is now, never who a clip or a stream
// was attributed to: once somebody else proves Dave's key — typed in another
// case — Dave can neither delete a clip of his old stream, nor wipe it, nor
// switch it off, unlinked or linked to another stream, and the one who proved
// it can do all three (A§3.1, A§3.11).
func TestStreamerRightsFollowTheCurrentLink(t *testing.T) {
	rw := newRecordingWorld(t, "uid-bob", "uid-dave", "uid-v3")
	at := clipsNow
	dave := rw.onAir("videokey-dave", 2, at.Add(-time.Hour))

	entry := testClip(clipA, "uid-bob", at.Add(-time.Hour), nil)
	entry.StreamTag = rw.p.streamTag("videokey-dave")
	entry.Handle = dave.Handle
	publishFixtureClip(t, rw.p, entry, false)

	rw.poll(t, at, dave)
	recorder := rw.recorder(t, "videokey-dave")
	dropped := heldRing(t, filepath.Join(recorder.dir, "dropped"), at.Add(-time.Minute), 20*time.Second)
	recorder.handsOver(dropped, clipTailDropped)

	deleteClip := func(userID string) *httptest.ResponseRecorder {
		return rw.call(t, userID, clipCall{method: http.MethodDelete, target: clipsPath + "/clip?id=" + clipA})
	}

	require.NoError(t, rw.p.setLink("uid-v3", "VideoKey-Dave", "publish-token-v3"))

	// Dave, linked to nothing now.
	requireClipError(t, deleteClip("uid-dave"), http.StatusForbidden, clipErrLocked, "delete")
	requireClipError(t, rw.wipe(t, "uid-dave", "videokey-dave"), http.StatusForbidden, clipErrLocked, "wipe")
	requireClipError(t, rw.switchClipping(t, "uid-dave", false), http.StatusForbidden, clipErrLocked, "switch")

	// Dave, linked to another stream of his: not this one's streamer.
	rw.link("uid-dave", "videokey-dave-two")
	rw.p.clipsLinkChanged("uid-dave")
	requireClipError(t, deleteClip("uid-dave"), http.StatusForbidden, clipErrNotAllowed, "delete")
	requireClipError(t, rw.wipe(t, "uid-dave", "videokey-dave"), http.StatusForbidden, clipErrNotAllowed, "wipe")
	require.Equal(t, http.StatusOK, rw.switchClipping(t, "uid-dave", false).Code)
	require.Equal(t, []string{rw.p.streamTag("videokey-dave-two")}, mapKeys(rw.stored(t).OptOut), "his switch is for the stream he has linked")
	require.Zero(t, recorder.look().wipes)
	require.Zero(t, recorder.look().stops)
	require.False(t, dropped.isDeleted())

	// V3, whose link is to Dave's key however it was typed, may do all three.
	response := rw.wipe(t, "uid-v3", "videokey-dave")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 1, recorder.look().wipes)
	require.True(t, dropped.isDeleted())

	require.Equal(t, http.StatusOK, rw.switchClipping(t, "uid-v3", false).Code)
	require.Contains(t, rw.stored(t).OptOut, rw.p.streamTag("videokey-dave"))
	require.Equal(t, 1, recorder.look().stops)

	response = deleteClip("uid-v3")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Nil(t, rw.stored(t).clip(clipA))
	require.Equal(t, 1, rw.stored(t).Banked["uid-bob"].Clips, "a streamer's deletion banks the clip for its clipper")
}

// The switch is kept by the stream and not the person (A§3.5): unlinking
// leaves the stream switched off; relinking to another stream does not carry
// it over, and the new stream starts allowed; and whoever proves the key next
// finds it off, and may switch it back on.
func TestOptOutIsKeyedByStreamAndSurvivesUnlink(t *testing.T) {
	rw := newRecordingWorld(t, "uid-dave", "uid-dan")
	at := clipsNow
	dave := rw.onAir("videokey-dave", 2, at.Add(-time.Hour))
	rw.poll(t, at, dave)

	require.Equal(t, http.StatusOK, rw.switchClipping(t, "uid-dave", false).Code)
	off := rw.stored(t).OptOut
	require.Equal(t, []string{rw.p.streamTag("videokey-dave")}, mapKeys(off))
	require.NotContains(t, off, "uid-dave")
	require.NotContains(t, off, dave.Handle)

	// Dave unlinks: his stream stays switched off, and is not recorded.
	rw.unlink("uid-dave")
	dave.UserID, dave.LinkSource = "", ""
	snap := rw.poll(t, at.Add(time.Minute), dave)
	require.Equal(t, clipStateOff, cardFor(t, snap.Streams, dave.Handle).Clip)
	require.Equal(t, 1, rw.madeFor("videokey-dave"))
	require.Nil(t, rw.me(t, "uid-dave").ClipsStreamer, "not linked: no switch")

	// He links another stream: it starts allowed, and his old one stays off.
	rw.link("uid-dave", "videokey-dave-two")
	rw.p.clipsLinkChanged("uid-dave")
	require.Equal(t, &clipsStreamerView{Allowed: true}, rw.me(t, "uid-dave").ClipsStreamer)
	snap = rw.poll(t, at.Add(2*time.Minute), dave)
	require.Equal(t, clipStateOff, cardFor(t, snap.Streams, dave.Handle).Clip)

	// Dan proves his old key: she finds it off, and switches it on.
	require.NoError(t, rw.p.setLink("uid-dan", "VIDEOKEY-DAVE", "publish-token-dan"))
	require.Equal(t, &clipsStreamerView{Allowed: false}, rw.me(t, "uid-dan").ClipsStreamer)
	require.Equal(t, http.StatusOK, rw.switchClipping(t, "uid-dan", true).Code)
	require.Empty(t, rw.stored(t).OptOut)

	snap = rw.poll(t, at.Add(3*time.Minute), dave)
	require.Equal(t, clipStateStarting, cardFor(t, snap.Streams, dave.Handle).Clip)
	require.Equal(t, 2, rw.madeFor("videokey-dave"))
}

// With no root — Clips never started, a problem with the storage, or a root
// that is not an absolute path — stopping, reconciling, housekeeping and
// annotating touch no file and ask Mattermost nothing: not ring/ or drafts/
// in whatever directory Mattermost runs in, and not under the root either
// (A§2.1). The bare API below answers nothing, so any call fails the test.
func TestStopAllTouchesNoFilesWithoutARoot(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	for _, path := range []string{"ring/abc/1/0.seg", "drafts/0123456789abcdef.mp4", "relative/ceo.villa.matty/ring/x/1/0.seg"} {
		writeAged(t, filepath.Join(cwd, path), []byte("keep me"), clipsNow)
	}
	before := treeOf(t, cwd)

	absolute := filepath.Join(t.TempDir(), "ceo.villa.matty")
	writeAged(t, filepath.Join(absolute, "ring", "abc", "1", "0.seg"), []byte("keep me"), clipsNow)

	for _, storage := range []struct {
		about         string
		root, problem string
	}{
		{about: "never started"},
		{about: "a problem", root: absolute, problem: clipsProblemNotLocal},
		{about: "a relative root", root: "relative/ceo.villa.matty"},
	} {
		p := &Plugin{handleSecret: testHandleSecret}
		p.SetAPI(&plugintest.API{})
		p.clips().setStorage(storage.root, storage.problem)
		m := p.recorders()
		m.newRecorder = func(string, string, func(recorderEvent)) clipRecorderHandle {
			t.Fatalf("%s: a recorder was made", storage.about)
			return nil
		}

		stream := apiStream{StreamKey: "videokey-dave", Handle: p.streamHandle("videokey-dave"), Title: "Dave"}
		for _, enabled := range []bool{true, false} {
			cfg := &configuration{EnableClips: enabled, BroadcastBoxURL: "http://192.168.1.20:8080"}
			cfg.normalize()
			p.setConfiguration(cfg)

			m.reconcile([]apiStream{stream}, cfg, clipsNow, true)
			m.stopAll(true, whyClipsOff, clipsNow)
			m.stopAll(false, whyUnconfigured, clipsNow)
			m.housekeep(clipsNow)
			p.housekeepClips(clipsNow)
			p.clipsTurnedOff(clipsNow)

			current := &snapshot{Streams: []apiStream{stream}}
			p.addClips(current, cfg, clipsNow)
			require.Empty(t, current.Streams[0].Clip, storage.about)
			require.Empty(t, current.ClipTails, storage.about)
		}

		sessions := []bbSession{{ID: "a"}, {ID: "b"}}
		require.Equal(t, sessions, m.withoutOwn(sessions, clipsNow), storage.about)

		require.Equal(t, before, treeOf(t, cwd), storage.about)
		require.True(t, exists(filepath.Join(absolute, "ring", "abc", "1", "0.seg")), storage.about)
	}
}

// treeOf is every file under dir, by its path from dir.
func treeOf(t *testing.T, dir string) []string {
	t.Helper()

	var files []string
	require.NoError(t, filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			relative, err := filepath.Rel(dir, path)
			require.NoError(t, err)
			files = append(files, relative)
		}

		return nil
	}))

	return files
}

// The recorders' own playback sessions are nobody watching: Broadcast Box
// lists them beside everybody else's, and VillaNET leaves them out of the
// count and out of the strangers beside the faces, from the moment Broadcast
// Box names them until a minute after they close (spec §2.19 item 18, §2.4.7,
// A§2.1).
func TestRecorderSessionsAreNeitherViewersNorStrangers(t *testing.T) {
	rw := newRecordingWorld(t, "uid-bob", "uid-dave")
	cfg := rw.p.getConfiguration().Clone()
	cfg.ShowWatchers = true
	rw.p.setConfiguration(cfg)

	dave := rw.handle("videokey-dave")
	now := time.Now()
	rw.p.watchers().record("sess-bob", "uid-bob", dave, now)
	raw := []bbStream{rawWatched("videokey-dave", "sess-bob", "sess-recorder")}

	card := func() apiStream {
		t.Helper()

		streams := rw.p.decorate(raw, rw.p.getConfiguration())
		require.Len(t, streams, 1)

		return streams[0]
	}

	// Before the recorder says which session is its own, it is a stranger.
	before := card()
	require.Equal(t, 2, before.Viewers)
	require.Equal(t, 1, before.Others)

	rw.m.own.open("sess-recorder")
	stream := card()
	require.Equal(t, 1, stream.Viewers, "the recorder is not watching")
	require.Zero(t, stream.Others, "nor is it a stranger")
	require.Len(t, stream.Watchers, 1)
	require.Equal(t, "uid-bob", stream.Watchers[0].UserID)

	// Closed, it is still the recorder's for a minute, should a poll still
	// list it; then it is nobody's.
	rw.m.own.closed("sess-recorder", time.Now())
	require.Equal(t, 1, card().Viewers)
	rw.m.own.closed("sess-recorder", time.Now().Add(-recorderSessionLinger-time.Second))
	require.Equal(t, 2, card().Viewers)
	rw.m.housekeep(time.Now())
	rw.m.own.mu.Lock()
	require.Empty(t, rw.m.own.ids, "forgotten by the housekeeping")
	rw.m.own.mu.Unlock()

	// None of it reaches the snapshot.
	rw.m.own.open("sess-recorder")
	current := &snapshot{Streams: rw.p.decorate(raw, rw.p.getConfiguration())}
	rw.p.addClips(current, rw.p.getConfiguration(), time.Now())
	encoded, err := json.Marshal(current)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "sess-recorder")
}

// Stopping the plugin stops every recorder within the five seconds it may
// take all together, keeping nothing, and their run context ends with it; the
// tails go, and ring/ is emptied. Until Clips starts again, nothing records
// (spec §2.19 item 31, §2.14).
func TestDeactivateStopsRecordersAndWipesRing(t *testing.T) {
	rw := newRecordingWorld(t, "uid-dave", "uid-dan")
	at := clipsNow
	dave, dan := rw.onAir("videokey-dave", 1, at.Add(-time.Hour)), rw.onAir("videokey-dan", 1, at.Add(-time.Hour))
	rw.poll(t, at, dave, dan)

	daveRecorder, danRecorder := rw.recorder(t, "videokey-dave"), rw.recorder(t, "videokey-dan")
	daveRecorder.holds(heldRing(t, filepath.Join(daveRecorder.dir, "end"), at, 20*time.Second))
	dropped := heldRing(t, filepath.Join(danRecorder.dir, "dropped"), at, 20*time.Second)
	danRecorder.handsOver(dropped, clipTailDropped)
	writeAged(t, filepath.Join(rw.h.root, clipsRingDir, "stray", "0.seg"), []byte("left behind"), at)

	// One of them will not stop in time: the stop gives up on it when the
	// time all of them may take is up, and its context ends anyway.
	rw.m.stopWait = 50 * time.Millisecond
	danRecorder.mu.Lock()
	danRecorder.blockFor = time.Hour
	danRecorder.mu.Unlock()

	rw.p.stopClips()

	for _, recorder := range []*fakeRecorder{daveRecorder, danRecorder} {
		look := recorder.look()
		require.Equal(t, 1, look.stops)
		require.False(t, look.keptEnd, "nothing is kept")
		require.Error(t, look.ctx.Err(), "its context ended")
	}
	require.True(t, dropped.isDeleted())
	require.Empty(t, namesIn(t, filepath.Join(rw.h.root, clipsRingDir)))
	require.Len(t, rw.logged("LogInfo", "A clip recorder stopped"), 2)

	// Nothing records while stopped.
	rw.poll(t, at.Add(time.Minute), dave, dan)
	require.Equal(t, 1, rw.madeFor("videokey-dave"))

	// Started again, the next poll records them afresh.
	rw.p.startClips()
	snap := rw.poll(t, at.Add(2*time.Minute), dave, dan)
	require.Equal(t, 2, rw.madeFor("videokey-dave"))
	require.Equal(t, clipStateStarting, cardFor(t, snap.Streams, dave.Handle).Clip)
}

// blockedOrigin is a Broadcast Box that never answers a status request until
// the request is given up, so that a poller restarted by a settings save
// never gets as far as the recorders: whatever happens to them is the save's
// own doing.
func blockedOrigin(t *testing.T) *httptest.Server {
	t.Helper()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(origin.Close)

	return origin
}

// Saving the console stops recorders there and then, without a poll: Clips
// switched off stops every one and takes every tail and draft with them; a
// new Broadcast Box URL stops every one too, since what they held came from
// the old one, but keeps the drafts; a new clip length goes to the running
// recorders and stops nothing (spec §2.19 item 31, §2.14, A§2.1).
func TestTurningClipsOffStopsRecordersWithoutAPoll(t *testing.T) {
	rw := newRecordingWorld(t, "uid-bob", "uid-dave")
	at := clipsNow
	first, second := blockedOrigin(t), blockedOrigin(t)
	rw.p.httpClient = first.Client()

	saved := map[string]any{"enableclips": true, "cliplengthseconds": "120", "broadcastboxurl": first.URL}
	rw.api.On("GetUnsanitizedConfig").Return(&model.Config{PluginSettings: model.PluginSettings{
		Plugins: map[string]map[string]any{manifestID: saved},
	}})
	rw.api.On("LoadPluginConfiguration", mock.Anything).Run(func(args mock.Arguments) {
		encoded, err := json.Marshal(saved)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(encoded, args.Get(0)))
	}).Return(nil)
	// The Site URL, which the app check's rules are read with on every load.
	rw.api.On("GetConfig").Return(&model.Config{}).Maybe()

	save := func(change func()) {
		t.Helper()

		change()
		require.NoError(t, rw.p.OnConfigurationChange())
	}

	// Loaded once before activation, as Mattermost does.
	save(func() {})
	rw.p.activatedLock.Lock()
	rw.p.activated = true
	rw.p.activatedLock.Unlock()
	t.Cleanup(rw.p.stopPoller)
	save(func() {})

	dave := rw.onAir("videokey-dave", 1, at.Add(-time.Hour))
	record := func(when time.Time) *fakeRecorder {
		t.Helper()

		rw.poll(t, when, dave)
		recorder := rw.recorder(t, "videokey-dave")
		recorder.handsOver(heldRing(t, filepath.Join(recorder.dir, "dropped"), when, 20*time.Second), clipTailDropped)
		_, held := rw.m.tail(dave.Handle, when)
		require.True(t, held)

		return recorder
	}
	recorder := record(at)

	draft, err := rw.p.clips().drafts.reserve("uid-bob", clipSourceLive, rw.p.captureOf(dave), func(string) bool { return false }, rw.p.clips().runContext())
	require.NoError(t, err)
	_, kept := rw.p.clips().drafts.complete(draft.id, exportResult{durationMs: 30000}, at)
	require.True(t, kept)
	rw.p.noteClipViewer("sess-bob", "uid-bob", dave.Handle, at)

	// A new clip length: running recorders take it, nothing stops.
	save(func() { saved["cliplengthseconds"] = "60" })
	require.Equal(t, []time.Duration{time.Minute}, recorder.look().windows)
	require.Zero(t, recorder.look().stops)

	// Another Broadcast Box: everything recorded goes, the drafts stay.
	save(func() { saved["broadcastboxurl"] = second.URL })
	look := recorder.look()
	require.Equal(t, 1, look.stops)
	require.False(t, look.keptEnd)
	_, held := rw.m.tail(dave.Handle, at)
	require.False(t, held)
	require.Equal(t, 1, rw.p.clips().drafts.counted())
	require.True(t, rw.p.clips().viewers.watching("uid-bob", dave.Handle, at))

	// Recording again under it, then Clips switched off: everything goes,
	// the drafts included. Who is watching what stays, for when Clips is
	// back on: a panel already playing never says so again.
	recorder = record(at.Add(time.Minute))
	save(func() { saved["enableclips"] = false })
	require.Equal(t, 1, recorder.look().stops)
	_, held = rw.m.tail(dave.Handle, at.Add(time.Minute))
	require.False(t, held)
	require.Zero(t, rw.p.clips().drafts.counted())
	require.True(t, rw.p.clips().viewers.watching("uid-bob", dave.Handle, at))
	require.Empty(t, namesIn(t, filepath.Join(rw.h.root, clipsRingDir)))

	current := &snapshot{Streams: []apiStream{dave}}
	rw.p.addClips(current, rw.p.getConfiguration(), at.Add(time.Minute))
	require.Empty(t, current.Streams[0].Clip, "no scissors while Clips is off")
	require.Empty(t, current.ClipTails)
}

// A Broadcast Box URL taken away stops every recorder on the next poll,
// which cannot reach anything, and every tail goes; the polls after it find
// nothing left to stop, and say nothing (spec §2.19 item 31, A§2.1).
func TestUnconfiguredStopsRecorders(t *testing.T) {
	rw := newRecordingWorld(t, "uid-dave")
	rw.api.On("KVList", mock.Anything, mock.Anything).Return([]string{}, nil).Maybe()
	rw.api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	at := clipsNow

	dave := rw.onAir("videokey-dave", 1, at.Add(-time.Hour))
	rw.poll(t, at, dave)
	recorder := rw.recorder(t, "videokey-dave")
	dropped := heldRing(t, filepath.Join(recorder.dir, "dropped"), at, 20*time.Second)
	recorder.handsOver(dropped, clipTailDropped)

	unset := rw.p.getConfiguration().Clone()
	unset.BroadcastBoxURL = ""
	rw.p.setConfiguration(unset)

	rw.p.pollOnce(t.Context(), true)
	look := recorder.look()
	require.Equal(t, 1, look.stops)
	require.False(t, look.keptEnd)
	require.True(t, dropped.isDeleted())
	require.Empty(t, namesIn(t, filepath.Join(rw.h.root, clipsRingDir)))
	require.False(t, rw.p.getSnapshot().Configured)
	require.Empty(t, rw.p.getSnapshot().ClipTails)

	lines := len(rw.logged("LogInfo", "A clip recorder stopped"))
	for range 3 {
		rw.p.pollOnce(t.Context(), true)
	}
	require.Equal(t, 1, recorder.look().stops)
	require.Len(t, rw.logged("LogInfo", "A clip recorder stopped"), lines)
}

// failingDNS is an HTTP client whose every request fails as a lookup of
// Broadcast Box's name that found nothing.
func failingDNS() *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, &net.DNSError{Err: "no such host", Name: "bb.lan", IsNotFound: true}
		},
	}}
}

// A poll that cannot reach Broadcast Box — here because its name would not
// resolve — says nothing about who stopped streaming: no recorder counts a
// missed poll, none stops, and the panels go on being told what is recorded
// and what is held. Only a poll that reached it counts. A recorder's own
// attempts keep their last good set of Broadcast Box's addresses through a
// failed lookup (TestRecorderResolvesBroadcastBoxPerAttempt), and the manager
// gives every recorder the one shared set (spec §2.19 item 22, §2.4.1,
// §2.4.5).
func TestResolverFailureKeepsRecorders(t *testing.T) {
	var lock sync.Mutex
	listing := []bbStream{rawLive("videokey-dave"), rawLive("videokey-dan")}
	origin := statusOrigin(t, func() []bbStream {
		lock.Lock()
		defer lock.Unlock()

		return listing
	})
	rw := polledRecordingWorld(t, origin, "uid-dave", "uid-dan")

	rw.p.pollOnce(t.Context(), true)
	daveRecorder, danRecorder := rw.recorder(t, "videokey-dave"), rw.recorder(t, "videokey-dan")
	live := liveRing(t, filepath.Join(daveRecorder.dir, "live"), time.Now(), 30*time.Second)
	daveRecorder.recording(live)
	dropped := heldRing(t, filepath.Join(danRecorder.dir, "dropped"), time.Now(), 20*time.Second)
	danRecorder.handsOver(dropped, clipTailDropped)

	rw.p.httpClient = failingDNS()
	for poll := 1; poll <= pollFailuresBeforeEmpty; poll++ {
		rw.p.pollOnce(t.Context(), true)

		snap := rw.p.getSnapshot()
		require.NotEmpty(t, snap.Error)
		require.Equal(t, clipStateReady, cardFor(t, snap.Streams, rw.handle("videokey-dave")).Clip, "poll %d", poll)
		require.Equal(t, time.Unix(0, live.status().epochStart).UnixMilli(), cardFor(t, snap.Streams, rw.handle("videokey-dave")).ClipFrom)
		require.Equal(t, dropped.endTime().Add(tailKeep).UnixMilli(), cardFor(t, snap.Streams, rw.handle("videokey-dan")).ClipTail)
		require.Len(t, snap.ClipTails, 1)
	}
	for _, recorder := range []*fakeRecorder{daveRecorder, danRecorder} {
		require.Zero(t, recorder.look().stops)
	}

	// Reached again, with Dan gone: only now is her absence counted.
	rw.p.httpClient = origin.Client()
	lock.Lock()
	listing = []bbStream{rawLive("videokey-dave")}
	lock.Unlock()
	rw.p.pollOnce(t.Context(), true)
	require.Zero(t, danRecorder.look().stops, "one poll off the air is inside the grace")
	rw.p.pollOnce(t.Context(), true)
	require.Equal(t, 1, danRecorder.look().stops)
	require.Zero(t, daveRecorder.look().stops)
	require.Equal(t, 1, rw.madeFor("videokey-dave"), "never started again")

	// Every recorder the manager makes for real shares its one set of
	// addresses.
	m := newClipRecorders(rw.p)
	made := m.newRecorder(rw.handle("videokey-dave"), rw.ringDir("videokey-dave"), func(recorderEvent) {})
	real, ok := made.(*clipRecorder)
	require.True(t, ok)
	require.Same(t, m.peers, real.peers)
	require.Same(t, m.own, real.own)
}

// Short of room, recording gives way. Below recorderStopFree everything stops
// with nothing kept and every tail goes, each stream saying it is full, which
// is said once in the log; with room again, recording starts afresh. Above
// that but under the floor a new recorder needs, running recorders carry on
// and a new one waits as full; and a new one waits as full too when the
// recordings have used their budget, once every tail has made what room it
// could (spec §2.4.5 step 4, §2.13).
func TestRecordersStopWhenTheDiskIsNearlyFull(t *testing.T) {
	rw := newRecordingWorld(t, "uid-dave", "uid-dan", "uid-bob")
	at := clipsNow
	dave, dan := rw.onAir("videokey-dave", 2, at.Add(-time.Hour)), rw.onAir("videokey-dan", 1, at.Add(-time.Hour))

	var lock sync.Mutex
	free, total := uint64(500<<30), uint64(1000<<30)
	rw.p.clips().freeSpace = func(string) (uint64, uint64, bool) {
		lock.Lock()
		defer lock.Unlock()

		return free, total, true
	}
	room := func(bytes uint64) {
		lock.Lock()
		defer lock.Unlock()

		free = bytes
	}

	rw.poll(t, at, dave)
	recorder := rw.recorder(t, "videokey-dave")
	dropped := heldRing(t, filepath.Join(recorder.dir, "dropped"), at, 20*time.Second)
	recorder.handsOver(dropped, clipTailDropped)

	// Under the floor a new recorder needs, five per cent of the volume:
	// Dave's carries on, Dan's waits as full. Dave's held end stays: all of
	// it could not make the ten gigabytes Dan's would need.
	room(40 << 30)
	snap := rw.poll(t, at.Add(time.Minute), dave, dan)
	require.Zero(t, recorder.look().stops)
	require.Zero(t, rw.madeFor("videokey-dan"))
	require.Equal(t, clipStateFull, cardFor(t, snap.Streams, dan.Handle).Clip)
	require.False(t, dropped.isDeleted(), "a tail goes only if the tails could make the room")

	// Below the stopping point: everything stops, nothing kept, and the
	// held end goes too.
	room(recorderStopFree - 1)
	snap = rw.poll(t, at.Add(2*time.Minute), dave, dan)
	look := recorder.look()
	require.Equal(t, 1, look.stops)
	require.False(t, look.keptEnd)
	require.True(t, dropped.isDeleted())
	_, held := rw.m.tail(dave.Handle, at.Add(2*time.Minute))
	require.False(t, held)
	require.Equal(t, clipStateFull, cardFor(t, snap.Streams, dave.Handle).Clip)
	require.Equal(t, clipStateFull, cardFor(t, snap.Streams, dan.Handle).Clip)
	requireClipError(t, rw.cut(t, "uid-dave", "videokey-dave", clipSourceLive), http.StatusServiceUnavailable, clipErrRecordingFailed, "no room")

	rw.poll(t, at.Add(3*time.Minute), dave, dan)
	require.Len(t, rw.logged("LogWarn", "Clips stopped recording because the disk is nearly full"), 1, "said once")

	// The housekeeping finds it too, between polls.
	room(500 << 30)
	rw.poll(t, at.Add(4*time.Minute), dave, dan)
	require.Equal(t, 2, rw.madeFor("videokey-dave"))
	require.Equal(t, 1, rw.madeFor("videokey-dan"))
	require.Len(t, rw.logged("LogInfo", "Clips can record again: the disk has room"), 1)
	room(recorderStopFree - 1)
	rw.p.housekeepClips(at.Add(5 * time.Minute))
	require.Equal(t, 1, rw.recorder(t, "videokey-dave").look().stops)
	require.Equal(t, 1, rw.recorder(t, "videokey-dan").look().stops)
	current := &snapshot{Streams: []apiStream{dave, dan}}
	rw.p.addClips(current, rw.p.getConfiguration(), at.Add(5*time.Minute))
	require.Equal(t, clipStateFull, cardFor(t, current.Streams, dave.Handle).Clip)
	require.Equal(t, clipStateFull, cardFor(t, current.Streams, dan.Handle).Clip)

	// The budget: room on the disk, but the recordings have used theirs.
	room(500 << 30)
	rw.m.mu.Lock()
	rw.m.budget = ringCap(time.Duration(rw.p.getConfiguration().clipLengthSeconds())*time.Second, 6_000_000/8) + 1
	rw.m.mu.Unlock()
	snap = rw.poll(t, at.Add(6*time.Minute), dave, dan)
	states := []string{cardFor(t, snap.Streams, dave.Handle).Clip, cardFor(t, snap.Streams, dan.Handle).Clip}
	require.ElementsMatch(t, []string{clipStateStarting, clipStateFull}, states, "room for one")

	view := decodeAdminClips(t, rw.call(t, "uid-boss", clipCall{method: http.MethodGet, target: adminClipsPath}))
	reasons := map[string]string{}
	for _, row := range view.Recorders {
		reasons[row.State] = row.Reason
	}
	require.Equal(t, fullBudgetReason, reasons[clipStateFull])
}

// The System Console lists every stream recorded or waiting to be, with how
// much is held, at what bitrate, and why one is not recording; every tail
// held; and what the recordings hold on the disk. Never a stream key
// (spec §3.14, §2.10).
func TestAdminSeesRecordingsAndTails(t *testing.T) {
	rw := newRecordingWorld(t, "uid-alice", "uid-heidi", "uid-carol", "uid-judy", "uid-eve", "uid-faythe")
	at := clipsNow

	var streams []apiStream
	for i, key := range []string{"videokey-alice", "videokey-heidi", "videokey-carol", "videokey-judy", "videokey-eve", "videokey-faythe"} {
		streams = append(streams, rw.onAir(key, 10-i, at.Add(-time.Hour)))
	}
	rw.poll(t, at, streams...)
	require.Equal(t, http.StatusOK, rw.switchClipping(t, "uid-faythe", false).Code)
	rw.poll(t, at.Add(time.Second), streams...)

	alice := rw.recorder(t, "videokey-alice")
	live := liveRing(t, filepath.Join(alice.dir, "live"), at, time.Minute)
	alice.recording(live)
	rw.recorder(t, "videokey-heidi").became(clipStateFailed, time.Time{}, "the connection to Broadcast Box did not open within 10s")
	carol := rw.recorder(t, "videokey-carol")
	dropped := heldRing(t, filepath.Join(carol.dir, "dropped"), at, 20*time.Second)
	carol.handsOver(dropped, clipTailDropped)

	response := rw.call(t, "uid-chief", clipCall{method: http.MethodGet, target: adminClipsPath})
	view := decodeAdminClips(t, response)
	require.NotContains(t, strings.ToLower(response.Body.String()), "videokey-")

	rows := map[string]adminRecorderView{}
	for _, row := range view.Recorders {
		rows[row.Title] = row
	}
	require.Len(t, rows, 6)
	require.Equal(t, adminRecorderView{Handle: streams[0].Handle, Title: "Alice", State: clipStateReady, HeldSeconds: int(live.heldMs() / 1000), Bitrate: 96000}, rows["Alice"])
	require.Equal(t, clipStateFailed, rows["Heidi"].State)
	require.Equal(t, "the connection to Broadcast Box did not open within 10s", rows["Heidi"].Reason)
	require.Equal(t, clipStateStarting, rows["Carol"].State)
	require.Equal(t, clipStateQueued, rows["Eve"].State)
	require.Equal(t, clipStateOff, rows["Faythe"].State)

	require.Len(t, view.Tails, 1)
	require.Equal(t, streams[2].Handle, view.Tails[0].Handle)
	require.Equal(t, clipTailDropped, view.Tails[0].Reason)
	require.Equal(t, live.size()+dropped.size(), view.RingBytes)

	// Alice's ring has had to drop the start of the window for want of room:
	// her clips are shorter than the clip length while that lasts.
	truncatedRow := func() adminRecorderView {
		t.Helper()

		for _, row := range decodeAdminClips(t, rw.call(t, "uid-chief", clipCall{method: http.MethodGet, target: adminClipsPath})).Recorders {
			if row.Title == "Alice" {
				return row
			}
		}
		t.Fatal("no row for Alice")

		return adminRecorderView{}
	}
	live.mu.Lock()
	live.truncatedUntil = at.Add(time.Minute).UnixNano()
	live.truncation = truncationReason(live.capBytes, time.Minute)
	live.mu.Unlock()
	require.True(t, truncatedRow().Truncated)
	live.mu.Lock()
	live.truncatedUntil = at.UnixNano()
	live.mu.Unlock()
	require.False(t, truncatedRow().Truncated, "over once the window has passed")
}

// Every recorder failing for five minutes — almost always UDP between this
// server and Broadcast Box's media port — is something only an admin can put
// right, and /me tells every admin, linked or not, and nobody else. One
// recorder that works says it is not that (spec §2.12).
func TestMeAlertsWhenNoRecorderCanReachBroadcastBox(t *testing.T) {
	rw := newRecordingWorld(t, "uid-dave", "uid-dan")
	rw.person("uid-chief")
	at := clipsNow
	dave, dan := rw.onAir("videokey-dave", 1, at.Add(-time.Hour)), rw.onAir("videokey-dan", 1, at.Add(-time.Hour))

	// Nothing on the air for six minutes, so nothing recorded: nothing is
	// failing either.
	rw.poll(t, at.Add(-10*time.Minute))
	rw.p.housekeepClips(at.Add(-4 * time.Minute))
	rw.setNow(at.Add(-4 * time.Minute))
	require.Empty(t, rw.me(t, "uid-chief").ClipsAlert, "no recorder is not a failing one")

	rw.poll(t, at, dave, dan)

	why := "the connection to Broadcast Box did not open within 10s (it was checking); check that this server can exchange UDP with it"
	for _, key := range []string{"videokey-dave", "videokey-dan"} {
		rw.recorder(t, key).became(clipStateFailed, time.Time{}, why)
	}

	rw.poll(t, at.Add(time.Minute), dave, dan)
	rw.setNow(at.Add(5*time.Minute + 59*time.Second))
	require.Empty(t, rw.me(t, "uid-chief").ClipsAlert, "not yet five minutes")

	rw.p.housekeepClips(at.Add(6 * time.Minute))
	rw.setNow(at.Add(6 * time.Minute))
	want := "Clips cannot reach Broadcast Box's media port from this server: " + why
	require.Equal(t, want, rw.me(t, "uid-chief").ClipsAlert)
	require.Equal(t, want, rw.me(t, "uid-boss").ClipsAlert)
	require.Empty(t, rw.me(t, "uid-dave").ClipsAlert, "admins only")

	// One recording again: it is not the media port.
	rw.recorder(t, "videokey-dan").became(clipStateReady, at, "")
	rw.poll(t, at.Add(7*time.Minute), dave, dan)
	require.Empty(t, rw.me(t, "uid-chief").ClipsAlert)
}

// A poll reads the settings once, as it starts, and a save can land while it
// is out asking Broadcast Box. The save stops what it has to there and then
// (OnConfigurationChange); the poll, coming back with the settings it began
// with, must not start recording again — from the Broadcast Box that was
// replaced, or after Clips went off — and the poller the save restarts
// decides. A clip length saved while a poll is starting a recorder reaches
// that recorder. And a recorder of an old Broadcast Box never outlives it,
// whatever order things happen in (spec §2.14, A§2.1).
func TestAPollFromBeforeASaveLeavesTheRecordersAlone(t *testing.T) {
	at := clipsNow

	t.Run("a new Broadcast Box URL", func(t *testing.T) {
		rw := newRecordingWorld(t, "uid-dave")
		dave := rw.onAir("videokey-dave", 1, at.Add(-time.Hour))
		rw.poll(t, at, dave)
		first := rw.recorder(t, "videokey-dave")

		// The poll starts, reading the settings; the save lands while it
		// is out.
		stale := rw.p.getConfiguration()
		fresh := stale.Clone()
		fresh.BroadcastBoxURL = "http://10.9.9.9:8080"
		rw.p.setConfiguration(fresh)
		rw.m.stopAll(false, whyURLChanged, at)
		require.Equal(t, 1, first.look().stops)

		// It comes back with what it read.
		rw.m.reconcile([]apiStream{dave}, stale, at.Add(time.Second), true)
		require.Equal(t, 1, rw.madeFor("videokey-dave"), "nothing is recorded from the Broadcast Box that was replaced")

		// The restarted poller's first poll records from the new one.
		rw.poll(t, at.Add(time.Minute), dave)
		require.Equal(t, 2, rw.madeFor("videokey-dave"))
		require.Equal(t, fresh.BroadcastBoxURL, rw.recorder(t, "videokey-dave").look().cfg.BroadcastBoxURL)
	})

	t.Run("Clips switched off", func(t *testing.T) {
		rw := newRecordingWorld(t, "uid-dave")
		dave := rw.onAir("videokey-dave", 1, at.Add(-time.Hour))
		rw.poll(t, at, dave)

		stale := rw.p.getConfiguration()
		off := stale.Clone()
		off.EnableClips = false
		rw.p.setConfiguration(off)
		rw.p.clipsTurnedOff(at)
		require.Equal(t, 1, rw.recorder(t, "videokey-dave").look().stops)

		rw.m.reconcile([]apiStream{dave}, stale, at.Add(time.Second), true)
		require.Equal(t, 1, rw.madeFor("videokey-dave"), "nothing is recorded after Clips went off")
		rw.m.mu.Lock()
		running := len(rw.m.slots)
		rw.m.mu.Unlock()
		require.Zero(t, running)
	})

	t.Run("a new clip length", func(t *testing.T) {
		rw := newRecordingWorld(t, "uid-dave")
		dave := rw.onAir("videokey-dave", 1, at.Add(-time.Hour))

		before := rw.p.getConfiguration()
		shorter := before.Clone()
		shorter.ClipLengthSeconds = "60"
		require.NotEqual(t, before.ringWindow(), shorter.ringWindow())

		// The save lands as the poll is starting the recorder with the
		// length it read: OnConfigurationChange stores the settings, then
		// tells the recorders. It is given every chance to be done before
		// the recorder is counted as running; it must wait for that, and
		// the test comes out the same however long it is given.
		saved := make(chan struct{})
		inner := rw.m.newRecorder
		rw.m.newRecorder = func(handle, dir string, onEvent func(recorderEvent)) clipRecorderHandle {
			rw.p.setConfiguration(shorter)
			go func() {
				defer close(saved)
				rw.m.setWindow(shorter.ringWindow())
			}()
			select {
			case <-saved:
			case <-time.After(100 * time.Millisecond):
			}

			return inner(handle, dir, onEvent)
		}
		rw.m.reconcile([]apiStream{dave}, before, at, true)
		<-saved
		rw.m.newRecorder = inner
		rw.poll(t, at.Add(time.Minute), dave)

		look := rw.recorder(t, "videokey-dave").look()
		require.Equal(t, before.ringWindow(), look.cfg.ringWindow(), "started with the length its poll read")
		require.Equal(t, []time.Duration{shorter.ringWindow()}, look.windows, "and told the new one")
	})

	t.Run("a recorder of the old Broadcast Box", func(t *testing.T) {
		rw := newRecordingWorld(t, "uid-dave")
		dave := rw.onAir("videokey-dave", 1, at.Add(-time.Hour))
		rw.poll(t, at, dave)
		first := rw.recorder(t, "videokey-dave")
		dropped := heldRing(t, filepath.Join(first.dir, "dropped"), at, 20*time.Second)
		first.handsOver(dropped, clipTailDropped)

		// The settings change, and nothing stops for it.
		moved := rw.p.getConfiguration().Clone()
		moved.BroadcastBoxURL = "http://10.9.9.9:8080"
		rw.p.setConfiguration(moved)
		rw.poll(t, at.Add(time.Second), dave)

		look := first.look()
		require.Equal(t, 1, look.stops)
		require.False(t, look.keptEnd)
		require.True(t, dropped.isDeleted(), "what it held came from the old one")
		require.Equal(t, 2, rw.madeFor("videokey-dave"))
		require.Equal(t, moved.BroadcastBoxURL, rw.recorder(t, "videokey-dave").look().cfg.BroadcastBoxURL)
		require.Equal(t, [][]string{{"stream_handle", dave.Handle, "reason", whyURLChanged}}, rw.logged("LogInfo", "A clip recorder stopped"))
	})
}

// A stream waiting for room takes held tails only when they could make the
// room between them. The disk's floor is a share of the whole volume, which a
// few minutes of video never frees, and the people who were watching a stream
// that dropped would lose its end at every poll for nothing. When the tails
// can make the room the budget needs, the oldest go until there is, and the
// stream is recorded (spec §2.4.5 step 4, §2.4.8).
func TestOnlyTailsThatMakeRoomGoForANewRecorder(t *testing.T) {
	rw := newRecordingWorld(t, "uid-dave", "uid-dan")
	at := clipsNow
	dave, dan := rw.onAir("videokey-dave", 2, at.Add(-time.Hour)), rw.onAir("videokey-dan", 1, at.Add(-time.Hour))

	var lock sync.Mutex
	free := uint64(500 << 30)
	rw.p.clips().freeSpace = func(string) (uint64, uint64, bool) {
		lock.Lock()
		defer lock.Unlock()

		return free, 1000 << 30, true
	}
	room := func(bytes uint64) {
		lock.Lock()
		defer lock.Unlock()

		free = bytes
	}
	danWaits := func(when time.Time, reason string) {
		t.Helper()

		snap := rw.poll(t, when, dave, dan)
		require.Equal(t, clipStateFull, cardFor(t, snap.Streams, dan.Handle).Clip)
		for _, row := range rw.m.adminRows(when) {
			if row.Handle == dan.Handle {
				require.Equal(t, reason, row.Reason)
			}
		}
	}

	rw.poll(t, at, dave)
	recorder := rw.recorder(t, "videokey-dave")
	dropped := heldRing(t, filepath.Join(recorder.dir, "dropped"), at.Add(time.Second), 20*time.Second)
	recorder.handsOver(dropped, clipTailDropped)

	// Ten gigabytes under the disk's floor, poll after poll: Dan waits, and
	// Dave's end stays to be clipped.
	room(40 << 30)
	for i := 1; i <= 3; i++ {
		danWaits(at.Add(time.Duration(i)*5*time.Second), fullDiskReason)
	}
	require.False(t, dropped.isDeleted())
	_, held := rw.m.tail(dave.Handle, at.Add(15*time.Second))
	require.True(t, held)

	// Room on the disk, and the budget short by more than the tail holds:
	// the same. What Dave's recorder may grow to counts, and so does the
	// tail.
	room(500 << 30)
	each := ringCap(rw.p.getConfiguration().ringWindow(), float64(dave.Bitrate)/8)
	rw.m.mu.Lock()
	rw.m.budget = 2*each - 1
	rw.m.mu.Unlock()
	danWaits(at.Add(20*time.Second), fullBudgetReason)
	require.False(t, dropped.isDeleted())
	require.Zero(t, rw.madeFor("videokey-dan"))

	// Short by less than the tail holds: it goes, and Dan is recorded.
	rw.m.mu.Lock()
	rw.m.budget = 2*each + dropped.size()/2
	rw.m.mu.Unlock()
	snap := rw.poll(t, at.Add(25*time.Second), dave, dan)
	require.Equal(t, clipStateStarting, cardFor(t, snap.Streams, dan.Handle).Clip)
	require.Equal(t, 1, rw.madeFor("videokey-dan"))
	require.True(t, dropped.isDeleted())
}

// While rings and tails together hold more than their budget, tails go, the
// oldest first: what a stream is recording now counts, though it is never
// deleted for it (spec §2.4.8, §2.13).
func TestTheBudgetCountsWhatIsBeingRecorded(t *testing.T) {
	rw := newRecordingWorld(t, "uid-dave", "uid-dan")
	at := clipsNow
	dave, dan := rw.onAir("videokey-dave", 2, at.Add(-time.Hour)), rw.onAir("videokey-dan", 1, at.Add(-time.Hour))
	rw.poll(t, at, dave, dan)

	daveRecorder, danRecorder := rw.recorder(t, "videokey-dave"), rw.recorder(t, "videokey-dan")
	live := liveRing(t, filepath.Join(daveRecorder.dir, "live"), at, time.Minute)
	daveRecorder.recording(live)
	dropped := heldRing(t, filepath.Join(danRecorder.dir, "dropped"), at, 20*time.Second)
	danRecorder.handsOver(dropped, clipTailDropped)

	// The tail alone fits; with the live ring, they do not.
	rw.m.mu.Lock()
	rw.m.budget = dropped.size() + live.size()/2
	rw.m.mu.Unlock()
	rw.p.housekeepClips(at.Add(time.Second))
	require.True(t, dropped.isDeleted())
	require.False(t, live.isDeleted())
	require.Zero(t, daveRecorder.look().stops)
}

// Until this node has read the document once, it does not know which streams
// their streamers have switched off, so nothing new is recorded, and the
// streams say nothing about clipping; once it is read, they are recorded (the
// manager's deviation 6, failing closed).
func TestNothingIsRecordedBeforeTheSwitchesAreKnown(t *testing.T) {
	rw := newRecordingWorld(t, "uid-dave")
	at := clipsNow
	dave := rw.onAir("videokey-dave", 1, at.Add(-time.Hour))

	s := rw.p.clips()
	s.lock.Lock()
	s.doc, s.loaded = nil, false
	s.lock.Unlock()
	rw.faults.failRead(kvClips)

	snap := rw.poll(t, at, dave)
	require.Zero(t, rw.madeFor("videokey-dave"))
	require.Empty(t, cardFor(t, snap.Streams, dave.Handle).Clip)

	rw.faults.healRead(kvClips)
	snap = rw.poll(t, at.Add(time.Second), dave)
	require.Equal(t, 1, rw.madeFor("videokey-dave"))
	require.Equal(t, clipStateStarting, cardFor(t, snap.Streams, dave.Handle).Clip)
}

// A switch pressed on another node reaches this one through the document,
// read again at most a minute later, and the poll that finds it does what the
// switch does where it was pressed: the stream's recorder stops with nothing
// kept, its held end goes, and every draft cut from it goes as wiped. A
// stream gone from the listing stops at once, rather than counting misses and
// keeping its end (spec §2.4.5 step 2, A§3.5).
func TestOptOutFromTheDocumentIsAppliedAtReconcile(t *testing.T) {
	rw := newRecordingWorld(t, "uid-bob", "uid-dave", "uid-dan")
	at := clipsNow
	dave, dan := rw.onAir("videokey-dave", 2, at.Add(-time.Hour)), rw.onAir("videokey-dan", 1, at.Add(-time.Hour))
	rw.poll(t, at, dave, dan)

	daveRecorder, danRecorder := rw.recorder(t, "videokey-dave"), rw.recorder(t, "videokey-dan")
	daveLive := liveRing(t, filepath.Join(daveRecorder.dir, "live"), at, time.Minute)
	daveRecorder.recording(daveLive)
	dropped := heldRing(t, filepath.Join(daveRecorder.dir, "dropped"), at.Add(-90*time.Second), 20*time.Second)
	daveRecorder.handsOver(dropped, clipTailDropped)
	danEnd := heldRing(t, filepath.Join(danRecorder.dir, "end"), at, 20*time.Second)
	danRecorder.holds(danEnd)

	rw.p.noteClipViewer("sess-bob-dave", "uid-bob", dave.Handle, at.Add(-2*time.Minute))
	rw.p.noteClipSessions([]bbStream{rawWatched("videokey-dave", "sess-bob-dave"), rawWatched("videokey-dan")}, at)
	ready := decodeDraft(t, rw.cut(t, "uid-bob", "videokey-dave", clipSourceLive), http.StatusCreated)
	fromEnd := decodeDraft(t, rw.cut(t, "uid-bob", "videokey-dave", clipSourceTail), http.StatusCreated)
	beingCut := rw.cutting(t, "uid-dan", dave)

	// Another node writes both switches.
	doc := rw.stored(t)
	doc.Rev++
	doc.OptOut = map[string]clipMark{
		rw.p.streamTag("videokey-dave"): {By: "uid-dave", At: at.UnixMilli()},
		rw.p.streamTag("videokey-dan"):  {By: "uid-dan", At: at.UnixMilli()},
	}
	rw.kvSet(kvClips, mustJSON(t, doc))

	// This node reads it again a minute on, when Dave is still on the air
	// and Dan has gone off it.
	later := at.Add(clipsCacheMaxAge + time.Second)
	snap := rw.poll(t, later, dave)

	look := daveRecorder.look()
	require.Equal(t, 1, look.stops)
	require.False(t, look.keptEnd)
	require.True(t, daveLive.isDeleted())
	require.True(t, dropped.isDeleted(), "its held end goes")
	_, held := rw.m.tail(dave.Handle, later)
	require.False(t, held)
	require.Equal(t, clipStateOff, cardFor(t, snap.Streams, dave.Handle).Clip)

	for _, id := range []string{ready.ID, fromEnd.ID} {
		requireClipError(t, rw.draftVideoOf(t, "uid-bob", id), http.StatusGone, clipErrDraftWiped, "a draft of the stream")
		require.False(t, exists(filepath.Join(rw.h.root, clipsDraftsDir, id+clipVideoExt)))
	}
	require.Equal(t, draftWiped, rw.draftOf(beingCut).state)

	look = danRecorder.look()
	require.Equal(t, 1, look.stops, "off the air and switched off: stopped at once")
	require.False(t, look.keptEnd)
	require.True(t, danEnd.isDeleted())
	_, held = rw.m.tail(dan.Handle, later)
	require.False(t, held)
	require.Empty(t, snap.ClipTails)

	// And nothing records either while they stay switched off.
	rw.poll(t, later.Add(5*time.Second), dave, dan)
	require.Equal(t, 1, rw.madeFor("videokey-dave"))
	require.Equal(t, 1, rw.madeFor("videokey-dan"))
}

// Once a stream is off the air, its streamer can still wipe what is left of
// it: its held end when nothing else knows its key, and, once that has gone
// too, the drafts cut from it (the manager's deviation 2, §2.10.6).
func TestStreamerWipesWhatIsLeftOfAStreamOffTheAir(t *testing.T) {
	rw := newRecordingWorld(t, "uid-dave", "uid-dan")
	at := clipsNow
	dave, dan := rw.onAir("videokey-dave", 1, at.Add(-time.Hour)), rw.onAir("videokey-dan", 1, at.Add(-time.Hour))
	rw.poll(t, at, dave, dan)

	// Dave's stream ends, and only its held end is left: no draft of it.
	daveRecorder := rw.recorder(t, "videokey-dave")
	end := heldRing(t, filepath.Join(daveRecorder.dir, "end"), at.Add(10*time.Second), 20*time.Second)
	daveRecorder.holds(end)
	rw.poll(t, at.Add(15*time.Second), dan)
	rw.poll(t, at.Add(20*time.Second), dan)
	_, held := rw.m.tail(dave.Handle, at.Add(20*time.Second))
	require.True(t, held)
	require.Empty(t, rw.p.clips().drafts.streamKeyOf(dave.Handle))

	response := rw.wipe(t, "uid-dave", "videokey-dave")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"ok":true,"draftsRemoved":0}`, response.Body.String())
	require.True(t, end.isDeleted())

	// Dan's ends, she cuts a draft of its end, and the end runs out: only
	// the draft knows her stream now.
	danRecorder := rw.recorder(t, "videokey-dan")
	danEnd := heldRing(t, filepath.Join(danRecorder.dir, "end"), at.Add(25*time.Second), 20*time.Second)
	danRecorder.holds(danEnd)
	rw.poll(t, at.Add(30*time.Second))
	rw.poll(t, at.Add(35*time.Second))
	draft := decodeDraft(t, rw.cut(t, "uid-dan", "videokey-dan", clipSourceTail), http.StatusCreated)
	gone := danEnd.endTime().Add(tailKeep)
	rw.p.housekeepClips(gone)
	rw.setNow(gone)
	require.True(t, danEnd.isDeleted())
	require.Empty(t, rw.m.streamKeyOf(dan.Handle))

	response = rw.wipe(t, "uid-dan", "videokey-dan")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"ok":true,"draftsRemoved":1}`, response.Body.String())
	requireClipError(t, rw.draftVideoOf(t, "uid-dan", draft.ID), http.StatusGone, clipErrDraftWiped, "wiped")

	// Nobody else may, all the same.
	requireClipError(t, rw.wipe(t, "uid-dave", "videokey-dan"), http.StatusForbidden, clipErrNotAllowed, "not hers")
}

// The streamer's controls answer for the store as every clip write does. A
// wipe reads the document afresh before anything is decided: while it cannot
// be read, or has been halted, nothing is wiped and the caller is told to try
// again, the token they spent given back, so that asking again as told costs
// nothing. A switch that cannot be written changes nothing, its token given
// back too. Each is metered: spent, 429. And a switch that changes nothing
// still has every panel told what is left (spec §2.10.6 "Then
// republishClips()", §2.10.7, A§2.1, A§3.3).
func TestStreamerControlsAnswerForTheStore(t *testing.T) {
	rw := newRecordingWorld(t, "uid-dave")
	rw.person("uid-chief")
	at := clipsNow
	dave := rw.onAir("videokey-dave", 1, at.Add(-time.Hour))
	rw.poll(t, at, dave)
	recorder := rw.recorder(t, "videokey-dave")
	dropped := heldRing(t, filepath.Join(recorder.dir, "dropped"), at, 20*time.Second)
	recorder.handsOver(dropped, clipTailDropped)

	adminWipe := func() *httptest.ResponseRecorder {
		return rw.call(t, "uid-chief", clipCall{method: http.MethodPost, target: adminClipsPath + "/wipe?handle=" + dave.Handle})
	}
	untouched := func(about string) {
		t.Helper()

		require.Zero(t, recorder.look().wipes, about)
		require.Zero(t, recorder.look().stops, about)
		require.False(t, dropped.isDeleted(), about)
	}

	// Unreadable: more times than the limiter allows in a minute.
	rw.faults.failRead(kvClips)
	for range 31 {
		requireClipError(t, rw.wipe(t, "uid-dave", "videokey-dave"), http.StatusServiceUnavailable, clipErrStore, "unreadable")
		requireClipError(t, adminWipe(), http.StatusServiceUnavailable, clipErrStore, "unreadable, from the console")
	}
	rw.faults.healRead(kvClips)
	untouched("unreadable")

	// Unwritable.
	rw.faults.failWrite(kvClips)
	for range 31 {
		requireClipError(t, rw.switchClipping(t, "uid-dave", false), http.StatusServiceUnavailable, clipErrStore, "unwritable")
	}
	rw.faults.healWrite(kvClips)
	untouched("unwritable")
	require.Empty(t, rw.stored(t).OptOut)

	// Switched off, and then again: the second writes nothing, and still
	// tells every panel.
	require.Equal(t, http.StatusOK, rw.switchClipping(t, "uid-dave", false).Code)
	require.Equal(t, 1, recorder.look().stops)
	s := rw.p.clips()
	s.runLock.Lock()
	s.republishArmed = false
	s.runLock.Unlock()
	armed := rw.h.timers.count()
	writes := rw.faults.writesTo(kvClips)
	require.Equal(t, http.StatusOK, rw.switchClipping(t, "uid-dave", false).Code)
	require.Equal(t, writes, rw.faults.writesTo(kvClips))
	require.Equal(t, armed+1, rw.h.timers.count(), "a republish is armed")

	// Spent: 429 on all three.
	s.writeLimiter = newRateLimiter(1, time.Hour)
	require.True(t, s.writeLimiter.allowAt("uid-dave", at))
	require.True(t, s.writeLimiter.allowAt("uid-chief", at))
	requireClipError(t, rw.wipe(t, "uid-dave", "videokey-dave"), http.StatusTooManyRequests, clipErrRateLimited, "wipe")
	requireClipError(t, rw.switchClipping(t, "uid-dave", true), http.StatusTooManyRequests, clipErrRateLimited, "switch")
	requireClipError(t, adminWipe(), http.StatusTooManyRequests, clipErrRateLimited, "console wipe")
	require.Contains(t, rw.stored(t).OptOut, rw.p.streamTag("videokey-dave"), "still off")

	// Halted: the wipe that finds the document unreadable halts it, and
	// every wipe after it is refused the same way.
	s.writeLimiter = newRateLimiter(30, time.Minute)
	rw.kvSet(kvClips, []byte(`{"v":1,"rev":`))
	requireClipError(t, rw.wipe(t, "uid-dave", "videokey-dave"), http.StatusServiceUnavailable, clipErrStore, "halted")
	requireClipError(t, adminWipe(), http.StatusServiceUnavailable, clipErrStore, "halted, from the console")
	requireClipError(t, rw.switchClipping(t, "uid-dave", true), http.StatusServiceUnavailable, clipErrStore, "halted switch")
	halted, _ := s.halt.state()
	require.True(t, halted)
	require.Zero(t, recorder.look().wipes)
}

// The recordings go onto a copy of the streams, never into the slice the
// snapshot was handed, which readers of an earlier snapshot hold without a
// lock (A§2.1: otherwise -race fails).
func TestRecordingsGoOntoACopyOfTheStreams(t *testing.T) {
	rw := newRecordingWorld(t, "uid-dave")
	at := clipsNow
	dave := rw.onAir("videokey-dave", 1, at.Add(-time.Hour))
	rw.poll(t, at, dave)
	recorder := rw.recorder(t, "videokey-dave")
	recorder.recording(liveRing(t, filepath.Join(recorder.dir, "live"), at, 30*time.Second))

	current := &snapshot{Streams: []apiStream{dave}, Configured: true}
	held := current.Streams
	rw.p.addClips(current, rw.p.getConfiguration(), at)
	require.Equal(t, clipStateReady, current.Streams[0].Clip)
	require.Empty(t, held[0].Clip, "what a reader holds is never written")
	require.Zero(t, held[0].ClipFrom)

	// And the other way: a card carried over that loses what it said.
	off := rw.p.getConfiguration().Clone()
	off.EnableClips = false
	carried := current.Streams
	rw.p.addClips(current, off, at)
	require.Empty(t, current.Streams[0].Clip)
	require.Equal(t, clipStateReady, carried[0].Clip)
}

// Every poll while Clips is off or Broadcast Box is not set up, and every
// save, stops everything: once nothing is left that must do nothing at all —
// no line logged, no panel told, and no file touched, a file under ring/
// included (A§2.1).
func TestStopAllIsQuietOnceNothingIsLeft(t *testing.T) {
	rw := newRecordingWorld(t, "uid-dave")
	at := clipsNow
	dave := rw.onAir("videokey-dave", 1, at.Add(-time.Hour))
	rw.poll(t, at, dave)
	recorder := rw.recorder(t, "videokey-dave")
	recorder.handsOver(heldRing(t, filepath.Join(recorder.dir, "dropped"), at, 20*time.Second), clipTailDropped)

	off := rw.p.getConfiguration().Clone()
	off.EnableClips = false
	rw.p.setConfiguration(off)
	rw.poll(t, at.Add(time.Second), dave)
	require.Equal(t, 1, recorder.look().stops)

	planted := filepath.Join(rw.h.root, clipsRingDir, "planted", "0.seg")
	writeAged(t, planted, []byte("nothing of the manager's"), at)
	s := rw.p.clips()
	s.runLock.Lock()
	s.republishArmed = false
	s.runLock.Unlock()
	armed := rw.h.timers.count()
	calls := len(rw.api.Calls)

	for i := range 3 {
		rw.poll(t, at.Add(time.Duration(i+2)*time.Second), dave)
		rw.m.stopAll(false, whyUnconfigured, at)
		rw.p.clipsTurnedOff(at)
	}

	require.True(t, exists(planted))
	require.Equal(t, armed, rw.h.timers.count())
	s.runLock.Lock()
	require.False(t, s.republishArmed)
	s.runLock.Unlock()
	for _, call := range rw.api.Calls[calls:] {
		require.False(t, strings.HasPrefix(call.Method, "Log"), "%s %v", call.Method, call.Arguments)
	}
}

// A wipe, or a switch to off, that lands while a draft of the stream is being
// published stops the publish, unless its clip is already being written.
// Whether the whole draft was being published or a part cut out of it,
// nothing is written, the draft goes as wiped, its files with it, and its
// clipper is told so. Only the store's own write is left between the two: a
// wipe that lands while it is being made finds a clip, which stays (spec
// §2.10.6 step 4).
func TestWipeStopsAPublishUnderWay(t *testing.T) {
	type landing struct {
		about string
		inMs  int64
		// lands is the wipe or the switch, as it lands; it answers how many
		// drafts went.
		lands func(rw *recordingWorld, dave apiStream) int
		// whileWriting lands it as the clip is being written, rather than as
		// the document is read for the write.
		whileWriting bool
	}
	wipe := func(rw *recordingWorld, dave apiStream) int {
		return rw.p.wipeStream(dave.Handle, clipsNow)
	}
	switchOff := func(rw *recordingWorld, dave apiStream) int {
		// What PUT /clips/streamer does once its switch is written.
		tag := rw.p.streamTag(dave.StreamKey)
		rw.m.optOut(tag, clipsNow)

		return rw.p.wipeDrafts(func(draft *clipDraft) bool { return draft.capture.StreamTag == tag }, clipsNow)
	}

	for _, c := range []landing{
		{about: "the whole draft, wiped", lands: wipe},
		{about: "a part of it, wiped", inMs: 2000, lands: wipe},
		{about: "the whole draft, switched off", lands: switchOff},
		{about: "the clip already being written", lands: wipe, whileWriting: true},
	} {
		t.Run(c.about, func(t *testing.T) {
			rw := newRecordingWorld(t, "uid-bob", "uid-dave")
			at := clipsNow
			dave := rw.onAir("videokey-dave", 2, at.Add(-time.Hour))
			rw.poll(t, at, dave)
			recorder := rw.recorder(t, "videokey-dave")
			recorder.recording(liveRing(t, filepath.Join(recorder.dir, "live"), at, time.Minute))
			rw.watch("sess-bob", "uid-bob", "videokey-dave", at)
			draft := decodeDraft(t, rw.cut(t, "uid-bob", "videokey-dave", clipSourceLive), http.StatusCreated)

			// The document is read for the write, or the write is made, once
			// the draft is being published: the wipe lands there. Inside the
			// store's lock, so nothing here may go through the store.
			landed, removed := false, 0
			hook := func(key string) {
				if key != kvClips || landed || rw.draftOf(draft.ID).state != draftPublishing {
					return
				}
				landed = true
				removed = c.lands(rw, dave)
			}
			if c.whileWriting {
				rw.faults.setOnWrite(hook)
			} else {
				rw.faults.setOnRead(hook)
			}
			response := rw.call(t, "uid-bob", clipCall{method: http.MethodPost, target: clipsPath + "/draft/publish?id=" + draft.ID, body: publishBody("after the wipe", c.inMs, draft.DurationMs)})
			rw.faults.setOnRead(nil)
			rw.faults.setOnWrite(nil)

			require.True(t, landed)
			require.Equal(t, 1, removed, "counted as it went")
			clipVideo := filepath.Join(rw.h.root, clipsFilesDir, draft.ID+clipVideoExt)
			if c.whileWriting {
				clip := decodeClipView(t, response, http.StatusCreated)
				require.Equal(t, draft.ID, clip.ID)
				require.NotNil(t, rw.stored(t).clip(draft.ID))
				require.True(t, exists(clipVideo))
				require.Equal(t, draftPublished, rw.draftOf(draft.ID).state)

				return
			}

			requireClipError(t, response, http.StatusGone, clipErrDraftWiped, c.about)
			require.Nil(t, rw.stored(t).clip(draft.ID), "nothing written")
			require.False(t, exists(clipVideo))
			require.False(t, exists(filepath.Join(rw.h.root, clipsDraftsDir, draft.ID+clipVideoExt)))
			require.Equal(t, draftWiped, rw.draftOf(draft.ID).state)
			require.Empty(t, rw.logged("LogWarn", "Could not cut a clip from its draft"))
		})
	}
}

// Twitch's video may only ever be shown through Twitch's own player
// (design/twitch-live.md §9): a Twitch stream in the list never takes a slot,
// never has a recorder made for it, never opens a session, and its card says
// nothing about clipping.
func TestTheRecorderNeverTakesATwitchStream(t *testing.T) {
	rw := newRecordingWorld(t, "uid-dave")
	t0 := clipsNow

	dave := rw.onAir("videokey-dave", 1, t0)
	onTwitch := apiStream{
		Handle: rw.p.twitchHandle("1001"), Source: sourceTwitch, Title: "Dave", UserID: "uid-dave",
		LinkSource: linkSourceTwitch, TwitchLogin: "dave_tv", TwitchID: "1001", Viewers: 500,
		StreamStart: t0.UTC().Format(time.RFC3339Nano),
	}

	snap := rw.poll(t, t0.Add(time.Minute), dave, onTwitch)

	rw.lock.Lock()
	require.Empty(t, rw.made[onTwitch.Handle], "no recorder, so no session at Broadcast Box or anywhere")
	require.Len(t, rw.made[dave.Handle], 1, "the Broadcast Box stream beside it is recorded as usual")
	rw.lock.Unlock()

	card := cardFor(t, snap.Streams, onTwitch.Handle)
	require.Empty(t, card.Clip)
	require.Zero(t, card.ClipFrom)
	require.Zero(t, card.ClipTail)

	// And a click on it is refused before anything is cut.
	source, err := rw.p.draftSourceOf(onTwitch.Handle, clipSourceLive, clickMoment{at: t0, streams: snap.Streams}, t0)
	require.Error(t, err)
	require.Empty(t, source.target.streamKey)
}

// A recorder whose stop panics is logged once, with its stack, and costs
// nothing else: the recorder beside it still stops, the stop waits on neither
// for good, and deactivating returns, all in the one test binary that would
// otherwise have ended with the panic (recovery.go).
func TestARecorderStopThatPanicsStopsNothingElse(t *testing.T) {
	rw := newRecordingWorld(t, "uid-dave", "uid-dan")
	at := clipsNow
	dave, dan := rw.onAir("videokey-dave", 1, at.Add(-time.Hour)), rw.onAir("videokey-dan", 1, at.Add(-time.Hour))
	rw.poll(t, at, dave, dan)

	daveRecorder, danRecorder := rw.recorder(t, "videokey-dave"), rw.recorder(t, "videokey-dan")
	danRecorder.whenStopped(func() { panic("dan's recorder fell over while it stopped") })
	// Deactivating goes on to take every badge off, and finds none.
	rw.api.On("KVList", mock.Anything, mock.Anything).Return([]string{}, nil).Maybe()

	deactivated := make(chan error, 1)
	go func() { deactivated <- rw.p.OnDeactivate() }()
	select {
	case err := <-deactivated:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("deactivating waited on the recorder whose stop panicked")
	}

	require.Equal(t, 1, daveRecorder.look().stops)
	require.Equal(t, 1, danRecorder.look().stops)
	require.Error(t, daveRecorder.look().ctx.Err(), "its context ended")
	panics := rw.logged("LogError", panicLogged)
	require.Len(t, panics, 1)
	require.Contains(t, panics[0], "stopping a clip recorder")
	require.Contains(t, panics[0], "dan's recorder fell over while it stopped")
	require.Len(t, rw.logged("LogInfo", "A clip recorder stopped"), 2)
}
