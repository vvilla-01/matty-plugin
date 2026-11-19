package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"
)

// The recorder's tests run with no WebRTC and no real time, except the one
// that looks at the offer, which makes a real PeerConnection and sends
// nothing. A fake clock moves only when a test says, and every wait the
// recorder asks it for is handed to the test, which checks how long it is and
// then lets it pass; a fake connect hands each attempt to the test, which says
// how it went. The fixture is played into a session's pipeline with arrival
// times on the fake clock, and its writer runs as it would for real.

const recorderTestHandle = "abcdef0123456789"

// A key with a part that reads like a word, so that a test can look for it
// in logs and reasons.
const recorderTestKey = "videokey-bob-SECRETKEY"

// fakeRecorderClock is a clock whose waits are the test's to release.
type fakeRecorderClock struct {
	mu    sync.Mutex
	now   time.Time
	waits chan fakeRecorderWait
}

type fakeRecorderWait struct {
	d    time.Duration
	fire chan time.Time
}

func newFakeRecorderClock() *fakeRecorderClock {
	return &fakeRecorderClock{now: time.Unix(0, mediaTestStart), waits: make(chan fakeRecorderWait, 64)}
}

func (c *fakeRecorderClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeRecorderClock) After(d time.Duration) <-chan time.Time {
	fire := make(chan time.Time, 1)
	c.waits <- fakeRecorderWait{d: d, fire: fire}
	return fire
}

func (c *fakeRecorderClock) set(at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = at
}

func (c *fakeRecorderClock) advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

// recorderTestLog keeps every line logged.
type recorderTestLog struct {
	mu    sync.Mutex
	lines []recorderLogLine
}

type recorderLogLine struct {
	level, msg string
	pairs      []any
}

func (l *recorderTestLog) add(level, msg string, pairs []any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, recorderLogLine{level: level, msg: msg, pairs: pairs})
}

func (l *recorderTestLog) LogError(msg string, pairs ...any) { l.add("error", msg, pairs) }
func (l *recorderTestLog) LogInfo(msg string, pairs ...any)  { l.add("info", msg, pairs) }
func (l *recorderTestLog) LogWarn(msg string, pairs ...any)  { l.add("warn", msg, pairs) }
func (l *recorderTestLog) LogDebug(msg string, pairs ...any) { l.add("debug", msg, pairs) }

func (l *recorderTestLog) count(level, msg string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, line := range l.lines {
		if line.level == level && strings.Contains(line.msg, msg) {
			n++
		}
	}
	return n
}

// text is every line as one string, for looking for a key in.
func (l *recorderTestLog) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out strings.Builder
	for _, line := range l.lines {
		fmt.Fprintf(&out, "%s %s %v\n", line.level, line.msg, line.pairs)
	}
	return out.String()
}

// requireNoKey checks that no line carries the key, in any case.
func (l *recorderTestLog) requireNoKey(t *testing.T, key string) {
	t.Helper()
	require.NotContains(t, strings.ToLower(l.text()), strings.ToLower(key))
}

type fakeRecorderPeer struct {
	closes atomic.Int32
	panics bool // close panics, after counting itself
}

func (p *fakeRecorderPeer) close() error {
	p.closes.Add(1)
	if p.panics {
		panic("the connection fell over while it closed")
	}
	return nil
}

type fakeRecorderAttempt struct {
	s      *recorderSession
	result chan error
}

// recorderWorld is one recorder with everything around it faked.
type recorderWorld struct {
	t        *testing.T
	clock    *fakeRecorderClock
	log      *recorderTestLog
	own      *clipOwnSessions
	rec      *clipRecorder
	attempts chan *fakeRecorderAttempt
	dir      string
	stream   apiStream

	mu     sync.Mutex
	events []recorderEvent

	feeds map[*recorderSession]*mediaFixtureStream
}

func newRecorderWorld(t *testing.T) *recorderWorld {
	t.Helper()
	w := &recorderWorld{
		t:        t,
		clock:    newFakeRecorderClock(),
		log:      &recorderTestLog{},
		own:      &clipOwnSessions{},
		attempts: make(chan *fakeRecorderAttempt),
		dir:      filepath.Join(t.TempDir(), "ring", recorderTestHandle),
		feeds:    map[*recorderSession]*mediaFixtureStream{},
		stream: apiStream{
			Handle:      recorderTestHandle,
			StreamKey:   recorderTestKey,
			Title:       "Bob plays Hytale",
			MOTD:        "come in",
			UserID:      "bob-user-id",
			Username:    "bob",
			LinkSource:  "manual",
			AudioTracks: 1,
			VideoTracks: 1,
			Bitrate:     6_000_000,
			NowPlaying:  &apiNowPlaying{Category: "game", Title: "Hytale"},
		},
	}
	w.rec = newClipRecorder(recorderConfig{
		handle:  recorderTestHandle,
		dir:     w.dir,
		own:     w.own,
		log:     w.log,
		onEvent: w.record,
		clock:   w.clock,
		connect: w.connect,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if tail, held := w.rec.stop(ctx, false); held {
			tail.ring.remove()
		}
		w.log.requireNoKey(t, recorderTestKey)
	})
	return w
}

func (w *recorderWorld) record(event recorderEvent) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, event)
}

func (w *recorderWorld) eventCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.events)
}

// states are the states the recorder has announced, in order.
func (w *recorderWorld) states() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	var states []string
	for _, event := range w.events {
		if event.kind == recorderChanged {
			states = append(states, event.state)
		}
	}
	return states
}

func (w *recorderWorld) tails() []clipTail {
	w.mu.Lock()
	defer w.mu.Unlock()
	var tails []clipTail
	for _, event := range w.events {
		if event.kind == recorderTailKept {
			tails = append(tails, *event.tail)
		}
	}
	return tails
}

func (w *recorderWorld) connect(ctx context.Context, s *recorderSession) error {
	attempt := &fakeRecorderAttempt{s: s, result: make(chan error, 1)}
	select {
	case w.attempts <- attempt:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-attempt.result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *recorderWorld) start() {
	cfg := &configuration{BroadcastBoxURL: "http://192.168.1.20:8080", ClipLengthSeconds: "120"}
	w.rec.start(context.Background(), w.stream, cfg)
}

// The waits for the recorder to get somewhere are bounded in real time only
// so that a broken recorder fails the test rather than hanging it.
const recorderTestPatience = 10 * time.Second

// attempt is the recorder's next attempt to connect.
func (w *recorderWorld) attempt() *fakeRecorderAttempt {
	w.t.Helper()
	select {
	case attempt := <-w.attempts:
		return attempt
	case <-time.After(recorderTestPatience):
		w.t.Fatal("the recorder made no attempt")
		return nil
	}
}

// wait is the recorder's next wait, which must be d long.
func (w *recorderWorld) wait(d time.Duration) fakeRecorderWait {
	w.t.Helper()
	select {
	case wait := <-w.clock.waits:
		require.Equal(w.t, d, wait.d, "the recorder waited for the wrong time")
		return wait
	case <-time.After(recorderTestPatience):
		w.t.Fatalf("the recorder did not wait %s", d)
		return fakeRecorderWait{}
	}
}

// pass lets a wait end, the clock having moved by its length.
func (w *recorderWorld) pass(wait fakeRecorderWait) {
	wait.fire <- w.clock.advance(wait.d)
}

// fail makes an attempt fail.
func (w *recorderWorld) fail(attempt *fakeRecorderAttempt, err error) {
	attempt.result <- err
}

// connected makes an attempt succeed, and returns the session's peer and
// the first tick of its watchdogs.
func (w *recorderWorld) connected(attempt *fakeRecorderAttempt, sessionID string) (*fakeRecorderPeer, fakeRecorderWait) {
	w.t.Helper()
	peer := &fakeRecorderPeer{}
	require.True(w.t, attempt.s.setPeer(peer))
	if sessionID != "" {
		w.rec.noteSessionID(attempt.s, sessionID)
	}
	w.rec.noteConn(attempt.s, webrtc.PeerConnectionStateConnected)
	attempt.result <- nil
	return peer, w.wait(recorderWatchEvery)
}

// feed plays the fixture into a session up to `until` of media time, from
// the moment the first feed of the session began, and moves the clock there.
// shape changes the stream before it plays: no audio from some point, say.
func (w *recorderWorld) feed(s *recorderSession, until time.Duration, shape ...func(*mediaFixtureStream)) {
	w.t.Helper()
	stream := w.feeds[s]
	if stream == nil {
		stream = newMediaFixtureStream(w.t, &mediaTestPipe{clipPipeline: s.pipe, running: true})
		stream.start = w.clock.Now().UnixNano()
		s.noteVideoPacket(stream.start)
		w.feeds[s] = stream
	}
	for _, change := range shape {
		change(stream)
	}
	stream.play(until)
	if stream.nextVideo > 0 {
		s.lastVideo.Store(stream.start + stream.videoTime(stream.nextVideo-1))
	}

	ctx, cancel := context.WithTimeout(context.Background(), recorderTestPatience)
	defer cancel()
	require.NoError(w.t, s.pipe.sync(ctx))
	w.clock.set(time.Unix(0, stream.start+int64(until)))
}

// readySession is a connected session with six seconds of the fixture in its
// ring, and its watchdogs' pending tick.
func (w *recorderWorld) readySession(sessionID string) (*recorderSession, *fakeRecorderPeer, fakeRecorderWait) {
	w.t.Helper()
	attempt := w.attempt()
	peer, tick := w.connected(attempt, sessionID)
	w.feed(attempt.s, 6*time.Second)
	require.Equal(w.t, clipStateReady, w.rec.state())
	return attempt.s, peer, tick
}

func TestRecorderBackoffSteps(t *testing.T) {
	want := []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second, time.Minute, 5 * time.Minute, 5 * time.Minute, 5 * time.Minute}
	for step, d := range want {
		require.Equal(t, d, recorderBackoffAfter(step), "step %d", step)
	}
	require.Equal(t, 2*time.Second, recorderBackoffAfter(-1))
}

func TestRecorderRemoteIPFilterKeepsOnlyBroadcastBox(t *testing.T) {
	keep := remoteIPFilter([]string{"192.168.1.20", "10.0.0.7"})

	require.True(t, keep(net.ParseIP("192.168.1.20")))
	require.True(t, keep(net.ParseIP("10.0.0.7")))
	require.True(t, keep(net.ParseIP("::ffff:192.168.1.20")), "the same address written as IPv6")
	require.False(t, keep(net.ParseIP("192.168.1.21")))
	require.False(t, keep(net.ParseIP("203.0.113.9")), "Broadcast Box's NAT_1_TO_1_IP")
	require.False(t, keep(net.ParseIP("127.0.0.1")))
	require.False(t, keep(net.ParseIP("fd00::106")))
	require.False(t, keep(nil))
}

func TestRecorderResolvesBroadcastBoxPerAttempt(t *testing.T) {
	type lookup struct {
		ips []net.IP
		err error
	}
	var (
		mu      sync.Mutex
		answers []lookup
		asked   []string
		builds  [][]string
	)
	a := &clipPeerAPI{
		lookup: func(ctx context.Context, network, host string) ([]net.IP, error) {
			deadline, ok := ctx.Deadline()
			require.True(t, ok, "a lookup has a deadline")
			require.LessOrEqual(t, time.Until(deadline), recorderResolveWait)
			require.Equal(t, "ip4", network)

			mu.Lock()
			defer mu.Unlock()
			asked = append(asked, host)
			next := answers[0]
			answers = answers[1:]
			return next.ips, next.err
		},
		build: func(allowed []string) (*webrtc.API, error) {
			builds = append(builds, allowed)
			return webrtc.NewAPI(), nil
		},
	}
	answer := func(next ...lookup) {
		mu.Lock()
		defer mu.Unlock()
		answers = append(answers, next...)
	}
	ctx := context.Background()

	// An address is used as it is, and the API is built once for it.
	first, allowed, err := a.forAttempt(ctx, "http://192.168.1.20:8080")
	require.NoError(t, err)
	require.Equal(t, []string{"192.168.1.20"}, allowed)
	again, _, err := a.forAttempt(ctx, "http://192.168.1.20:8080/")
	require.NoError(t, err)
	require.Same(t, first, again)
	require.Empty(t, asked)
	require.Equal(t, [][]string{{"192.168.1.20"}}, builds)
	require.False(t, onlyLoopback(allowed))

	// A name is looked up for IPv4 at every attempt; the set is sorted and
	// has no repeats, and a new set builds a new API.
	answer(lookup{ips: []net.IP{net.ParseIP("10.0.0.5"), net.ParseIP("10.0.0.4"), net.ParseIP("10.0.0.5")}})
	byName, allowed, err := a.forAttempt(ctx, "http://bb.lan:8080")
	require.NoError(t, err)
	require.NotSame(t, first, byName)
	require.Equal(t, []string{"10.0.0.4", "10.0.0.5"}, builds[1])
	require.Equal(t, []string{"10.0.0.4", "10.0.0.5"}, allowed)

	// A failed or empty lookup keeps the last good set: no rebuild.
	answer(lookup{err: errors.New("no such host")}, lookup{}, lookup{ips: []net.IP{net.ParseIP("10.0.0.5"), net.ParseIP("10.0.0.4")}})
	for range 3 {
		kept, allowed, err := a.forAttempt(ctx, "http://bb.lan:8080")
		require.NoError(t, err)
		require.Same(t, byName, kept)
		require.Equal(t, []string{"10.0.0.4", "10.0.0.5"}, allowed)
	}
	require.Len(t, builds, 2)

	// A different set rebuilds for the attempts that follow. Sessions made
	// earlier keep the API they were made with, which nothing changes.
	answer(lookup{ips: []net.IP{net.ParseIP("10.0.0.9")}})
	moved, _, err := a.forAttempt(ctx, "http://bb.lan:8080")
	require.NoError(t, err)
	require.NotSame(t, byName, moved)
	require.Equal(t, []string{"10.0.0.9"}, builds[2])

	// Another host has no good set until it resolves: the last host's set is
	// not used for it.
	answer(lookup{err: errors.New("no such host")})
	_, _, err = a.forAttempt(ctx, "http://elsewhere.lan:8080")
	require.ErrorIs(t, err, errNoIPv4)

	// An IPv6 address, or no host, has no IPv4 address at all.
	_, _, err = a.forAttempt(ctx, "http://[fd00::106]:8080")
	require.ErrorIs(t, err, errNoIPv4)
	_, _, err = a.forAttempt(ctx, "")
	require.ErrorIs(t, err, errNoIPv4)

	// This machine's loopback, written as an address or as a name that
	// resolves to it (localhost; a Debian host name is 127.0.1.1), is a set
	// with nothing but loopback in it: an attempt with it can open only if
	// Broadcast Box offers a loopback address (TestRecorderOffersOnlyWhatItCanRecord).
	_, allowed, err = a.forAttempt(ctx, "http://127.0.0.1:8080")
	require.NoError(t, err)
	require.Equal(t, []string{"127.0.0.1"}, allowed)
	require.True(t, onlyLoopback(allowed))
	answer(lookup{ips: []net.IP{net.ParseIP("127.0.0.1")}}, lookup{ips: []net.IP{net.ParseIP("127.0.1.1")}})
	for _, url := range []string{"http://localhost:8080", "http://mattermost-host:8080"} {
		_, allowed, err = a.forAttempt(ctx, url)
		require.NoError(t, err)
		require.True(t, onlyLoopback(allowed), url)
	}
	require.False(t, onlyLoopback(nil))
	require.False(t, onlyLoopback([]string{"127.0.0.1", "192.168.1.20"}))

	// A name that has never resolved fails every attempt with the reason.
	fresh := &clipPeerAPI{
		lookup: func(context.Context, string, string) ([]net.IP, error) { return nil, nil },
		build:  func([]string) (*webrtc.API, error) { t.Fatal("nothing to build for"); return nil, nil },
	}
	_, _, err = fresh.forAttempt(ctx, "http://bb.lan:8080")
	require.ErrorIs(t, err, errNoIPv4)
	require.Equal(t, "Broadcast Box URL resolves to no IPv4 address", err.Error())
}

func TestRecorderWatchdogs(t *testing.T) {
	now := time.Unix(0, mediaTestStart).Add(time.Hour)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	healthy := func() recorderFacts {
		return recorderFacts{
			conn:        webrtc.PeerConnectionStateConnected,
			connAt:      ago(time.Minute),
			connectedAt: ago(time.Minute),
			lastVideo:   ago(30 * time.Millisecond),
			flowSince:   ago(time.Minute),
			lastAudio:   ago(20 * time.Millisecond),
			audioTracks: 1,
		}
	}

	cases := []struct {
		name   string
		change func(*recorderFacts)
		over   bool
		tail   string
		reason string
	}{
		{name: "a healthy session carries on", change: func(*recorderFacts) {}},
		{name: "disconnected for 5 s is still waited out", change: func(f *recorderFacts) {
			f.conn, f.connAt = webrtc.PeerConnectionStateDisconnected, ago(5*time.Second)
		}},
		{name: "disconnected for more than 5 s reconnects", change: func(f *recorderFacts) {
			f.conn, f.connAt = webrtc.PeerConnectionStateDisconnected, ago(5*time.Second+time.Millisecond)
		}, over: true, tail: clipTailDropped, reason: "lost"},
		{name: "failed reconnects at once", change: func(f *recorderFacts) {
			f.conn, f.connAt = webrtc.PeerConnectionStateFailed, now
		}, over: true, tail: clipTailDropped, reason: "failed"},
		{name: "closed when nobody asked reconnects", change: func(f *recorderFacts) {
			f.conn, f.connAt = webrtc.PeerConnectionStateClosed, now
		}, over: true, tail: clipTailDropped, reason: "closed"},
		{name: "no video for just under 10 s is waited out", change: func(f *recorderFacts) {
			f.lastVideo, f.flowSince = ago(noVideoWatchdog-time.Millisecond), ago(time.Minute)
		}},
		{name: "no video for 10 s is the stream's end", change: func(f *recorderFacts) {
			f.lastVideo = ago(noVideoWatchdog)
		}, over: true, tail: clipTailEnded, reason: "no video"},
		// Broadcast Box sends a new session nothing of the picture until the
		// publisher's next keyframe, which OBS puts ten seconds or more apart.
		{name: "no video yet 10 s after connecting is waited for", change: func(f *recorderFacts) {
			f.lastVideo, f.flowSince = time.Time{}, time.Time{}
			f.connectedAt = ago(noVideoWatchdog)
		}},
		{name: "no video yet just under a minute after connecting is waited for", change: func(f *recorderFacts) {
			f.lastVideo, f.flowSince, f.lastAudio = time.Time{}, time.Time{}, time.Time{}
			f.connectedAt = ago(maxKeyframeGap - time.Millisecond)
		}},
		{name: "no video at all a minute after connecting", change: func(f *recorderFacts) {
			f.lastVideo, f.flowSince, f.lastAudio = time.Time{}, time.Time{}, time.Time{}
			f.connectedAt = ago(maxKeyframeGap)
		}, over: true, tail: clipTailEnded, reason: "keyframes too rare"},
		{name: "no video yet, a minute after connecting but 10 s after a pause", change: func(f *recorderFacts) {
			f.lastVideo, f.flowSince = time.Time{}, time.Time{}
			f.connectedAt, f.pausedAt = ago(maxKeyframeGap), ago(noVideoWatchdog)
		}},
		{name: "no video while paused is waited for", change: func(f *recorderFacts) {
			f.lastVideo, f.paused, f.pausedAt = ago(time.Hour), true, ago(time.Hour)
		}},
		{name: "no video since a pause ended 5 s ago is waited for", change: func(f *recorderFacts) {
			f.lastVideo, f.pausedAt = ago(time.Hour), ago(5*time.Second)
		}},
		{name: "sound gone 2 s while the picture flows reconnects", change: func(f *recorderFacts) {
			f.lastAudio = ago(recorderAudioDiedAfter)
		}, over: true, tail: clipTailDropped, reason: "sound"},
		{name: "sound gone just under 2 s is waited out", change: func(f *recorderFacts) {
			f.lastAudio = ago(recorderAudioDiedAfter - time.Millisecond)
		}},
		{name: "no sound when Broadcast Box lists no audio track", change: func(f *recorderFacts) {
			f.lastAudio, f.audioTracks = ago(time.Minute), 0
		}},
		{name: "no sound ever in this session", change: func(f *recorderFacts) {
			f.lastAudio = time.Time{}
		}},
		{name: "both stalled and the picture just came back", change: func(f *recorderFacts) {
			f.lastAudio, f.flowSince = ago(5*time.Second), ago(300*time.Millisecond)
		}},
		{name: "the picture has stalled too", change: func(f *recorderFacts) {
			f.lastAudio, f.lastVideo = ago(5*time.Second), ago(time.Second)
		}},
		{name: "the picture came back 2 s ago and the sound did not", change: func(f *recorderFacts) {
			f.lastAudio, f.flowSince = ago(5*time.Second), ago(recorderAudioDiedAfter)
		}, over: true, tail: clipTailDropped, reason: "sound"},
		{name: "a stream that cannot be recorded ends first", change: func(f *recorderFacts) {
			f.unsupported = errBFrames
			f.conn, f.connAt = webrtc.PeerConnectionStateDisconnected, ago(time.Minute)
		}, over: true, tail: clipTailDropped, reason: "B-frames"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			facts := healthy()
			tc.change(&facts)
			end, over := judgeRecorder(facts, now)
			require.Equal(t, tc.over, over)
			if !tc.over {
				return
			}
			require.Equal(t, tc.tail, end.tail)
			require.Contains(t, end.reason, tc.reason)
			require.Equal(t, facts.unsupported != nil, end.unsupported)
		})
	}
}

func TestRecorderBacksOffAndSaysFailedAfterTwoAttempts(t *testing.T) {
	w := newRecorderWorld(t)
	w.start()

	// The first attempt is made at once. Broadcast Box's refusal echoes
	// the key; neither the reason nor the log may.
	w.fail(w.attempt(), fmt.Errorf("Broadcast Box refused the playback request (HTTP 404): no stream %s", strings.ToUpper(recorderTestKey)))
	retry := w.wait(2 * time.Second)
	require.Equal(t, clipStateStarting, w.rec.state())
	require.Contains(t, w.rec.reason(), "HTTP 404")
	require.NotContains(t, strings.ToLower(w.rec.reason()), strings.ToLower(recorderTestKey))
	require.Equal(t, 1, w.log.count("warn", "could not connect"))

	// The second in a row without ready is failed.
	w.pass(retry)
	w.fail(w.attempt(), errors.New("the connection to Broadcast Box did not open within 10s"))
	retry = w.wait(5 * time.Second)
	require.Equal(t, clipStateFailed, w.rec.state())
	require.Equal(t, []string{clipStateFailed}, w.states())
	require.Equal(t, 2, w.log.count("warn", "could not connect"))

	// The same failure again is not logged again.
	w.pass(retry)
	w.fail(w.attempt(), errors.New("the connection to Broadcast Box did not open within 10s"))
	retry = w.wait(15 * time.Second)
	require.Equal(t, 2, w.log.count("warn", "could not connect"))

	// A session that connects stays failed until its epoch is ready.
	w.pass(retry)
	attempt := w.attempt()
	_, tick := w.connected(attempt, "")
	require.Equal(t, clipStateFailed, w.rec.state())
	require.True(t, w.rec.clipFrom().IsZero())

	start := w.clock.Now()
	w.feed(attempt.s, 6*time.Second)
	require.Equal(t, clipStateReady, w.rec.state())
	require.Equal(t, start, w.rec.clipFrom(), "clipFrom is the epoch's first IDR")
	require.Empty(t, w.rec.reason())
	require.Equal(t, []string{clipStateFailed, clipStateReady}, w.states())
	require.EqualValues(t, 5, w.rec.heldSeconds(), "the newest frame is held once the next one says how long it is")
	live := w.rec.live()
	require.Equal(t, clipStateReady, live.state)
	require.Same(t, attempt.s.pipe.writer.current(), live.ring)

	// Dropped after less than two minutes of ready: the backoff carries on
	// where it was.
	w.rec.noteConn(attempt.s, webrtc.PeerConnectionStateDisconnected)
	tick.fire <- w.clock.advance(6 * time.Second)
	retry = w.wait(time.Minute)
	require.Equal(t, clipStateStarting, w.rec.state())
	require.True(t, w.rec.clipFrom().IsZero())
	require.Contains(t, w.rec.reason(), "lost")

	// Ready for two minutes, then dropped: the backoff starts again.
	w.pass(retry)
	attempt = w.attempt()
	w.connected(attempt, "")
	w.feed(attempt.s, 6*time.Second)
	w.clock.advance(recorderBackoffReset)
	w.rec.noteConn(attempt.s, webrtc.PeerConnectionStateFailed)
	retry = w.wait(2 * time.Second)

	// A session that connects but never gets a picture counts as an attempt
	// that failed, and two of them make it failed again. Its first picture
	// is waited for longer than ten seconds: Broadcast Box sends a new
	// session none until the publisher's next keyframe. A minute without one
	// is a failure.
	w.pass(retry)
	attempt = w.attempt()
	_, tick = w.connected(attempt, "")
	tails := len(w.tails())
	tick.fire <- w.clock.advance(noVideoWatchdog)
	tick = w.wait(recorderWatchEvery)
	require.Equal(t, clipStateStarting, w.rec.state())
	require.Len(t, w.tails(), tails)
	tick.fire <- w.clock.advance(maxKeyframeGap - noVideoWatchdog)
	retry = w.wait(5 * time.Second)
	require.Equal(t, clipStateStarting, w.rec.state())
	w.pass(retry)
	attempt = w.attempt()
	_, tick = w.connected(attempt, "")
	tick.fire <- w.clock.advance(maxKeyframeGap)
	w.wait(15 * time.Second)
	require.Equal(t, clipStateFailed, w.rec.state())
	require.Contains(t, w.rec.reason(), "no video")
	require.Contains(t, w.rec.reason(), "keyframes too rare")
}

// Broadcast Box holds a new session's picture until the publisher's next
// keyframe, and its sound not at all: a session whose first keyframe comes
// twelve seconds after it connects, as OBS's default keyframe interval at 24
// frames a second gives, is recorded, without a reconnect.
func TestRecorderWaitsForTheFirstKeyframe(t *testing.T) {
	w := newRecorderWorld(t)
	w.start()

	attempt := w.attempt()
	_, tick := w.connected(attempt, "aaaa-1111-2222")
	for range 12 {
		tick.fire <- w.clock.advance(time.Second)
		tick = w.wait(recorderWatchEvery)
	}
	require.Equal(t, clipStateStarting, w.rec.state())

	w.feed(attempt.s, 6*time.Second)
	require.Equal(t, clipStateReady, w.rec.state())
	tick.fire <- w.clock.Now()
	w.wait(recorderWatchEvery)
	require.Equal(t, clipStateReady, w.rec.state())
	require.Zero(t, w.log.count("warn", "reconnecting"))
	require.Empty(t, w.tails())
}

func TestRecorderSaysUnsupportedAndTriesAgainInFiveMinutes(t *testing.T) {
	w := newRecorderWorld(t)
	w.start()

	attempt := w.attempt()
	peer, _ := w.connected(attempt, "")
	w.rec.noteUnsupported(attempt.s, errBFrames)
	retry := w.wait(recorderUnsupportedRetry)
	require.Equal(t, clipStateUnsupported, w.rec.state())
	require.Equal(t, errBFrames.Error(), w.rec.reason())
	require.EqualValues(t, 1, peer.closes.Load())
	require.Equal(t, 1, w.log.count("warn", "cannot be clipped"))

	// Still unsupported while it tries again, whatever the attempt does.
	w.pass(retry)
	w.fail(w.attempt(), errors.New("could not reach Broadcast Box"))
	retry = w.wait(2 * time.Second)
	require.Equal(t, clipStateUnsupported, w.rec.state())
	w.pass(retry)
	w.fail(w.attempt(), errors.New("could not reach Broadcast Box"))
	retry = w.wait(5 * time.Second)
	require.Equal(t, clipStateUnsupported, w.rec.state())

	// The same reason again is not logged again.
	w.pass(retry)
	attempt = w.attempt()
	w.connected(attempt, "")
	w.rec.noteUnsupported(attempt.s, errBFrames)
	retry = w.wait(recorderUnsupportedRetry)
	require.Equal(t, 1, w.log.count("warn", "cannot be clipped"))

	// The publisher switched B-frames off: ready again.
	w.pass(retry)
	attempt = w.attempt()
	w.connected(attempt, "")
	w.feed(attempt.s, 6*time.Second)
	require.Equal(t, clipStateReady, w.rec.state())
	require.Empty(t, w.rec.reason())
	require.Equal(t, []string{clipStateUnsupported, clipStateReady}, w.states())
}

func TestRecorderTailsWhenTheSoundDiesOrThePictureStops(t *testing.T) {
	w := newRecorderWorld(t)
	w.start()

	// The sound stops at 6 s while the picture carries on: a dropped tail.
	attempt := w.attempt()
	_, tick := w.connected(attempt, "")
	w.feed(attempt.s, 6*time.Second)
	firstFrom := w.rec.clipFrom()
	w.rec.setWindow(90 * time.Second)
	require.Equal(t, int64(90*time.Second), attempt.s.pipe.writer.settings.window.Load())

	w.feed(attempt.s, 8500*time.Millisecond, func(s *mediaFixtureStream) { s.audioUntil = 6 * time.Second })
	tick.fire <- w.clock.Now()
	retry := w.wait(2 * time.Second)

	tails := w.tails()
	require.Len(t, tails, 1)
	tail := tails[0]
	require.Equal(t, clipTailDropped, tail.reason)
	require.True(t, tail.ring.status().frozen)
	require.GreaterOrEqual(t, tail.ring.heldMs(), int64(8000))
	require.Equal(t, tail.ring.endTime().Add(tailKeep), tail.until)
	require.Equal(t, apiStream{
		Handle: recorderTestHandle, StreamKey: recorderTestKey, Title: "Bob plays Hytale", MOTD: "come in",
		UserID: "bob-user-id", Username: "bob", LinkSource: "manual", NowPlaying: w.stream.NowPlaying,
	}, tail.stream)
	require.Contains(t, w.rec.reason(), "sound")
	require.Equal(t, 1, w.log.count("warn", "reconnecting"))
	require.Equal(t, 1, w.log.count("info", "end is held"))

	// The next session picks the stream up again in a new epoch.
	w.pass(retry)
	attempt = w.attempt()
	_, tick = w.connected(attempt, "")
	w.feed(attempt.s, 6*time.Second)
	require.True(t, w.rec.clipFrom().After(firstFrom))
	require.NotEqual(t, tail.ring.status().epoch, w.rec.live().ring.status().epoch)
	require.Equal(t, int64(90*time.Second), attempt.s.pipe.writer.settings.window.Load(), "a new session takes the window set")

	// Now the picture stops: after ten seconds, the stream's end.
	tick.fire <- w.clock.advance(noVideoWatchdog)
	w.wait(5 * time.Second)
	tails = w.tails()
	require.Len(t, tails, 2)
	require.Equal(t, clipTailEnded, tails[1].reason)
	require.GreaterOrEqual(t, tails[1].ring.heldMs(), int64(5000))
}

func TestRecorderShortEpochsAreNotTails(t *testing.T) {
	w := newRecorderWorld(t)
	w.start()

	attempt := w.attempt()
	w.connected(attempt, "")
	w.feed(attempt.s, 3*time.Second)
	require.Equal(t, clipStateReady, w.rec.state())
	epochDir := attempt.s.pipe.writer.current().dir

	w.rec.noteConn(attempt.s, webrtc.PeerConnectionStateFailed)
	w.wait(2 * time.Second)
	require.Empty(t, w.tails())
	require.NoDirExists(t, epochDir, "an epoch too short for a tail is deleted")
}

func TestRecorderHandsOverTheTailOfACut(t *testing.T) {
	w := newRecorderWorld(t)
	w.start()

	attempt := w.attempt()
	peer, _ := w.connected(attempt, "")
	w.feed(attempt.s, 6*time.Second)
	firstFrom := w.rec.clipFrom()

	// A reader cuts the epoch (Broadcast Box rewrote the timestamps, say):
	// the session carries on and the epoch that ended is a dropped tail.
	pipe := attempt.s.pipe
	require.True(t, pipe.cut(pipe.epoch.Load(), ringOp{reason: cutRewritten, keep: true}))
	ctx, cancel := context.WithTimeout(context.Background(), recorderTestPatience)
	defer cancel()
	require.NoError(t, pipe.sync(ctx))

	tails := w.tails()
	require.Len(t, tails, 1)
	require.Equal(t, clipTailDropped, tails[0].reason)
	require.Equal(t, clipStateStarting, w.rec.state())
	require.Equal(t, cutRewritten, w.rec.reason())
	require.Equal(t, 1, w.log.count("warn", "was cut"))

	// The next IDR starts the new epoch.
	w.feed(attempt.s, 10*time.Second)
	require.Equal(t, clipStateReady, w.rec.state())
	require.True(t, w.rec.clipFrom().After(firstFrom))
	require.Zero(t, peer.closes.Load(), "a cut is not a reconnect")

	// Another cut within the minute is not logged again.
	require.True(t, pipe.cut(pipe.epoch.Load(), ringOp{reason: cutDiskTooSlow, keep: true}))
	require.NoError(t, pipe.sync(ctx))
	require.Equal(t, 1, w.log.count("warn", "was cut"))
}

func TestRecorderStopKeepsTheEndAsATail(t *testing.T) {
	w := newRecorderWorld(t)
	w.start()

	_, peer, _ := w.readySession("0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0")
	require.True(t, w.own.has("0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", w.clock.Now()))

	ctx, cancel := context.WithTimeout(context.Background(), recorderTestPatience)
	defer cancel()
	tail, held := w.rec.stop(ctx, true)
	require.True(t, held)
	require.Equal(t, clipTailEnded, tail.reason)
	require.GreaterOrEqual(t, tail.ring.heldMs(), int64(5000))
	require.Equal(t, recorderTestKey, tail.stream.StreamKey)
	require.EqualValues(t, 1, peer.closes.Load())
	require.Nil(t, w.rec.live().ring)
	require.Equal(t, 1, w.log.count("info", "end is held"))

	// The session id stays ours for a minute after it closed.
	closedAt := w.clock.Now()
	require.True(t, w.own.has("0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", closedAt.Add(recorderSessionLinger-time.Millisecond)))
	require.False(t, w.own.has("0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", closedAt.Add(recorderSessionLinger)))

	// Nothing is heard from a stopped recorder.
	events := w.eventCount()
	w.rec.wipe()
	w.rec.changed()
	require.Equal(t, events, w.eventCount())

	tail.ring.remove()
}

func TestRecorderStopWithoutKeepDeletesTheRing(t *testing.T) {
	w := newRecorderWorld(t)
	w.start()

	s, _, _ := w.readySession("")
	epochDir := s.pipe.writer.current().dir
	require.DirExists(t, epochDir)

	ctx, cancel := context.WithTimeout(context.Background(), recorderTestPatience)
	defer cancel()
	_, held := w.rec.stop(ctx, false)
	require.False(t, held)
	require.NoDirExists(t, epochDir)
	require.Empty(t, w.tails())
}

func TestRecorderStopBetweenAttempts(t *testing.T) {
	w := newRecorderWorld(t)
	w.start()

	w.fail(w.attempt(), errors.New("could not reach Broadcast Box"))
	w.wait(2 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), recorderTestPatience)
	defer cancel()
	_, held := w.rec.stop(ctx, true)
	require.False(t, held)

	// A recorder starts once: a stopped one stays stopped.
	w.start()
	select {
	case <-w.attempts:
		t.Fatal("a stopped recorder made an attempt")
	default:
	}
}

func TestRecorderWipeDeletesWhatIsHeldAndCarriesOn(t *testing.T) {
	w := newRecorderWorld(t)
	w.start()

	s, _, _ := w.readySession("")
	wiped := s.pipe.writer.current()
	firstFrom := w.rec.clipFrom()

	w.rec.wipe()
	require.Equal(t, clipStateStarting, w.rec.state(), "nothing is held to cut from")
	require.True(t, wiped.isDeleted())
	require.NoDirExists(t, wiped.dir)

	ctx, cancel := context.WithTimeout(context.Background(), recorderTestPatience)
	defer cancel()
	require.NoError(t, s.pipe.sync(ctx))
	require.Equal(t, restartWiped, w.rec.reason())
	require.Zero(t, w.log.count("warn", "was cut"), "a wipe is not a problem")
	require.Empty(t, w.tails())

	// Recording carries on at the next IDR.
	w.feed(s, 10*time.Second)
	require.Equal(t, clipStateReady, w.rec.state())
	require.True(t, w.rec.clipFrom().After(firstFrom))
}

func TestRecorderTailMadeAcrossAWipeIsDeleted(t *testing.T) {
	w := newRecorderWorld(t)
	w.start()

	s, _, _ := w.readySession("")

	// The session is let go of (a watchdog fired), then a wipe lands before
	// its epoch is frozen: the tail must not outlive the wipe.
	wipes, ok := w.rec.takeSession(s)
	require.True(t, ok)
	w.rec.wipe()

	ctx, cancel := context.WithTimeout(context.Background(), recorderTestPatience)
	defer cancel()
	frozen := w.rec.closeSession(ctx, s, true)
	require.NotNil(t, frozen)
	w.rec.handOver(frozen, clipTailDropped, wipes)
	require.Empty(t, w.tails())
	require.True(t, frozen.isDeleted())
}

func TestRecorderUpdateFollowsThePoll(t *testing.T) {
	w := newRecorderWorld(t)
	w.start()

	s, _, tick := w.readySession("")

	// Paused: no picture for a long time is waited out.
	paused := w.stream
	paused.Paused = true
	paused.Title = "Bob, back soon"
	paused.Bitrate = 16_000_000
	paused.StreamKey = ""
	w.rec.update(paused)
	require.Equal(t, int64(2_000_000), s.pipe.writer.settings.reported.Load())

	tick.fire <- w.clock.advance(time.Minute)
	tick = w.wait(recorderWatchEvery)
	require.Equal(t, clipStateReady, w.rec.state())

	// Back from the pause, a minute on: the poll before said paused, and the
	// picture has ten seconds from then to come back.
	w.rec.update(paused)
	resumed := paused
	resumed.Paused = false
	w.rec.update(resumed)
	tick.fire <- w.clock.advance(noVideoWatchdog - time.Second)
	tick = w.wait(recorderWatchEvery)
	tick.fire <- w.clock.advance(time.Second)
	w.wait(2 * time.Second)

	tails := w.tails()
	require.Len(t, tails, 1)
	require.Equal(t, clipTailEnded, tails[0].reason)
	require.Equal(t, "Bob, back soon", tails[0].stream.Title, "a tail keeps the stream as the last poll had it")
	require.Equal(t, recorderTestKey, tails[0].stream.StreamKey, "an update without the key keeps it")
}

func TestRecorderKnowsItsSessions(t *testing.T) {
	own := &clipOwnSessions{}
	now := time.Unix(0, mediaTestStart)

	require.False(t, own.has("aaaa-1111", now))
	own.open("aaaa-1111")
	require.True(t, own.has("aaaa-1111", now.Add(24*time.Hour)), "an open session is ours for as long as it is open")
	own.closed("aaaa-1111", now)
	require.True(t, own.has("aaaa-1111", now.Add(59*time.Second)))
	require.False(t, own.has("aaaa-1111", now.Add(recorderSessionLinger)))
	own.prune(now.Add(recorderSessionLinger))
	require.Empty(t, own.ids)

	// A session closed while Broadcast Box was still answering is kept for
	// its minute, not forever.
	w := newRecorderWorld(t)
	s := &recorderSession{wake: make(chan struct{}, 1)}
	s.closed = true
	w.rec.noteSessionID(s, "bbbb-2222")
	require.True(t, w.own.has("bbbb-2222", w.clock.Now()))
	require.False(t, w.own.has("bbbb-2222", w.clock.Now().Add(recorderSessionLinger)))

	// No id: warned about once per recorder.
	w.rec.noteSessionID(s, "")
	w.rec.noteSessionID(s, "")
	require.Equal(t, 1, w.log.count("warn", "did not say which session"))
}

// The one test with a real PeerConnection: what is offered to Broadcast Box,
// and that the session id is ours the moment it is known. Nothing is sent
// anywhere: the exchange is a fake, and nothing answers.
func TestRecorderOffersOnlyWhatItCanRecord(t *testing.T) {
	w := newRecorderWorld(t)

	var offer string
	answerWith := func(answer, sessionID string, err error) {
		w.rec.whep = func(ctx context.Context, cfg *configuration, streamKey, sdp string) (string, string, error) {
			_, hasDeadline := ctx.Deadline()
			require.True(t, hasDeadline)
			require.Equal(t, recorderTestKey, streamKey)
			require.Equal(t, "http://127.0.0.1:9", cfg.BroadcastBoxURL)
			offer = sdp
			return answer, sessionID, err
		}
	}
	w.rec.mu.Lock()
	w.rec.stream, w.rec.cfg = w.stream, &configuration{BroadcastBoxURL: "http://127.0.0.1:9", ClipLengthSeconds: "120"}
	w.rec.mu.Unlock()

	attempt := func() error {
		s := w.rec.newSession()
		err := w.rec.connectWHEP(context.Background(), s)
		_, ok := w.rec.takeSession(s)
		require.True(t, ok)
		ctx, cancel := context.WithTimeout(context.Background(), recorderTestPatience)
		defer cancel()
		require.Nil(t, w.rec.closeSession(ctx, s, false))
		return err
	}

	answerWith("", "", errors.New("could not reach Broadcast Box"))
	require.EqualError(t, attempt(), "could not reach Broadcast Box")

	var video, audio []string
	section := ""
	for _, line := range strings.Split(offer, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "m=video"):
			section = "video"
			video = append(video, line)
		case strings.HasPrefix(line, "m=audio"):
			section = "audio"
			audio = append(audio, line)
		case section == "video":
			video = append(video, line)
		case section == "audio":
			audio = append(audio, line)
		}
		if strings.HasPrefix(line, "a=candidate:") {
			fields := strings.Fields(line)
			require.GreaterOrEqual(t, len(fields), 8, line)
			require.Equal(t, "udp", strings.ToLower(fields[2]), "UDP only: %s", line)
			require.NotNil(t, net.ParseIP(fields[4]).To4(), "IPv4 only, and no mDNS names: %s", line)
		}
	}
	for _, line := range []string{
		"m=video 9 UDP/TLS/RTP/SAVPF 96 45 98 113",
		"a=recvonly",
		"a=rtpmap:96 H264/90000",
		"a=fmtp:96 " + recorderH264Fmtp,
		"a=rtcp-fb:96 nack",
		"a=rtcp-fb:96 nack pli",
		"a=rtpmap:45 AV1/90000",
		"a=rtpmap:98 VP9/90000",
		"a=fmtp:98 profile-id=0",
		"a=rtpmap:113 H265/90000",
		"a=fmtp:113 level-id=93;profile-id=1;tier-flag=0;tx-mode=SRST",
	} {
		require.Contains(t, video, line)
	}
	for _, line := range []string{
		"m=audio 9 UDP/TLS/RTP/SAVPF 111",
		"a=recvonly",
		"a=rtpmap:111 opus/48000/2",
		"a=fmtp:111 " + recorderOpusFmtp,
	} {
		require.Contains(t, audio, line)
	}
	for _, never := range []string{"transport-cc", "goog-remb", "ccm fir", "transport-wide-cc", "a=sendrecv"} {
		require.NotContains(t, offer, never)
	}

	// The id Broadcast Box names is ours before the answer is even used,
	// and for a minute after the session closes.
	answerWith("v=0\r\nnot an answer\r\n", "0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", nil)
	require.Error(t, attempt())
	require.True(t, w.own.has("0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", w.clock.Now().Add(recorderSessionLinger-time.Second)))
	require.False(t, w.own.has("0f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", w.clock.Now().Add(recorderSessionLinger)))

	// Broadcast Box beside Mattermost, named by its loopback address: an
	// answer with no loopback address to send media from is refused at once,
	// saying why, rather than waited on for ten seconds and blamed on UDP.
	// The session it opened is closed with the attempt, and was ours all
	// along.
	lanOnly := "v=0\r\na=candidate:1 1 udp 2130706431 192.168.1.94 8443 typ host\r\n"
	answerWith(lanOnly, "1f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", nil)
	err := attempt()
	require.ErrorIs(t, err, errBroadcastBoxLoopback)
	require.Contains(t, err.Error(), "LAN address")
	require.True(t, w.own.has("1f1e2d3c-4b5a-6978-8796-a5b4c3d2e1f0", w.clock.Now()))

	// One that does offer one (INCLUDE_LOOPBACK_CANDIDATE) is tried.
	answerWith(lanOnly+"a=candidate:2 1 udp 2130706431 127.0.0.1 8443 typ host\r\n", "", nil)
	err = attempt()
	require.Error(t, err)
	require.NotErrorIs(t, err, errBroadcastBoxLoopback)
	require.True(t, offersLoopback(lanOnly+"a=candidate:2 1 udp 2130706431 127.0.0.1 8443 typ host\r\n"))
	require.False(t, offersLoopback(lanOnly))

	// An address that is not IPv4 never gets as far as an offer.
	w.rec.mu.Lock()
	w.rec.cfg = &configuration{BroadcastBoxURL: "http://[fd00::106]:8080"}
	w.rec.mu.Unlock()
	require.ErrorIs(t, attempt(), errNoIPv4)
}

func TestRecorderSessionGoroutinesStopWithIt(t *testing.T) {
	w := newRecorderWorld(t)
	w.start()

	s, _, _ := w.readySession("")

	// Readers still running when the session closes are waited for.
	release := make(chan struct{})
	require.True(t, s.addReader())
	go func() {
		defer s.readers.Done()
		<-release
	}()

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ctx, cancel := context.WithTimeout(context.Background(), recorderTestPatience)
		defer cancel()
		if tail, held := w.rec.stop(ctx, true); held {
			tail.ring.remove()
		}
	}()

	// Once closing has begun no reader may start.
	require.Eventually(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.closed
	}, recorderTestPatience, time.Millisecond)
	require.False(t, s.addReader())
	select {
	case <-stopped:
		t.Fatal("stop did not wait for the session's readers")
	default:
	}
	close(release)
	<-stopped

	// A connection made after closing began is closed at once.
	late := &fakeRecorderPeer{}
	require.False(t, s.setPeer(late))
	require.EqualValues(t, 1, late.closes.Load())
}

func TestRecorderFilesStayInItsDirectory(t *testing.T) {
	w := newRecorderWorld(t)
	w.start()

	s, _, _ := w.readySession("")
	dir := s.pipe.writer.current().dir
	require.Equal(t, w.dir, filepath.Dir(dir))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)
	for _, entry := range entries {
		require.True(t, strings.HasSuffix(entry.Name(), ".seg"), entry.Name())
	}
}

func TestRecorderClosesItsSessionWhenItsContextEnds(t *testing.T) {
	w := newRecorderWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	w.rec.start(ctx, w.stream, &configuration{BroadcastBoxURL: "http://192.168.1.20:8080", ClipLengthSeconds: "120"})

	s, peer, _ := w.readySession("aaaa-bbbb-cccc")
	epochDir := s.pipe.writer.current().dir

	// Not stopped, but whatever it ran under has ended: nobody else will
	// close the session, so the recorder does, and keeps nothing.
	cancel()
	select {
	case <-w.rec.done:
	case <-time.After(recorderTestPatience):
		t.Fatal("the recorder did not end with its context")
	}
	require.EqualValues(t, 1, peer.closes.Load())
	require.NoDirExists(t, epochDir)
	require.False(t, w.own.has("aaaa-bbbb-cccc", w.clock.Now().Add(recorderSessionLinger)))
	require.Empty(t, w.tails())
}

// A wipe can land while a recorder is handing over the end of a session:
// after the recorder looked at the wipes, before the manager holds the tail.
// It then finds no session to cut, since this one is already let go of, and
// no tail of the stream held yet. What was recorded before it must still go,
// so the recorder looks again once the tail is delivered (§2.10.6).
func TestTailHandedOverAsAWipeLandsIsDeleted(t *testing.T) {
	w := newRecorderWorld(t)
	rw := newRecordingWorld(t, "uid-dave")

	var rec *clipRecorder
	var handed *ring
	rw.m.newRecorder = func(handle, dir string, onEvent func(recorderEvent)) clipRecorderHandle {
		rec = newClipRecorder(recorderConfig{
			handle: handle, dir: dir, own: rw.m.own, log: w.log, clock: w.clock, connect: w.connect,
			onEvent: func(event recorderEvent) {
				if event.kind == recorderTailKept && handed == nil {
					handed = event.tail.ring

					// The streamer's wipe lands now, as it would between the
					// recorder's look and this delivery: the recorder counts
					// it, and the manager deletes the stream's tail, of
					// which it holds none yet. (The wipe itself would wait
					// here for this delivery to finish.)
					rec.mu.Lock()
					rec.wipes++
					rec.mu.Unlock()
					rw.m.mu.Lock()
					delete(rw.m.tails, handle)
					rw.m.mu.Unlock()
				}
				onEvent(event)
			},
		})
		w.rec = rec

		return rec
	}

	dave := rw.onAir("videokey-dave", 1, clipsNow.Add(-time.Hour))
	rw.poll(t, clipsNow, dave)
	attempt := w.attempt()
	w.connected(attempt, "")
	w.feed(attempt.s, 12*time.Second)
	require.Equal(t, clipStateReady, rec.state())

	// The connection fails: the session's twelve seconds are handed over as
	// a dropped tail, as the wipe lands.
	rec.noteConn(attempt.s, webrtc.PeerConnectionStateFailed)
	w.wait(2 * time.Second)

	require.NotNil(t, handed)
	require.True(t, handed.isDeleted(), "recorded before the wipe")
	_, held := rw.m.tail(dave.Handle, w.clock.Now())
	require.False(t, held)
	require.Empty(t, rw.m.tailViews(w.clock.Now(), time.Minute))
	require.Zero(t, w.log.count("info", "end is held"))
}

// While ready, the reason an admin is given is why clips come out shorter
// than the clip length, for as long as that lasts: the ring has had to drop
// the start of the window for want of room.
func TestRecorderSaysWhyClipsAreShortWhileReady(t *testing.T) {
	w := newRecorderWorld(t)
	w.start()

	s, _, _ := w.readySession("")
	require.Empty(t, w.rec.reason())

	live := s.pipe.writer.current()
	why := truncationReason(ringMaxBytes, 5*time.Minute)
	live.mu.Lock()
	live.truncatedUntil = w.clock.Now().Add(time.Minute).UnixNano()
	live.truncation = why
	live.mu.Unlock()
	require.Equal(t, clipStateReady, w.rec.state())
	require.Equal(t, why, w.rec.reason())

	w.clock.advance(time.Minute)
	require.Empty(t, w.rec.reason(), "over once the window has passed")
}

// fakeTrack and fakeReceiver stand in for what pion hands onTrack.
type fakeTrack struct {
	kind  webrtc.RTPCodecType
	codec webrtc.RTPCodecParameters
}

func (f fakeTrack) Kind() webrtc.RTPCodecType        { return f.kind }
func (f fakeTrack) Codec() webrtc.RTPCodecParameters { return f.codec }
func (f fakeTrack) ReadRTP() (*rtp.Packet, interceptor.Attributes, error) {
	return nil, nil, io.EOF
}

type fakeReceiver struct{ codecs []webrtc.RTPCodecParameters }

func (f fakeReceiver) ReadRTCP() ([]rtcp.Packet, interceptor.Attributes, error) {
	return nil, nil, io.EOF
}

func (f fakeReceiver) GetParameters() webrtc.RTPParameters {
	return webrtc.RTPParameters{Codecs: f.codecs}
}

// A publisher sending AV1, VP9 or H.265 is answered with it, since the
// recorder offers them only to recognise them: the track that arrives says
// which, the stream cannot be recorded, and the recorder tries again in five
// minutes (spec §2.4.2 step 4).
func TestRecorderSaysUnsupportedForAVideoCodecItCannotRecord(t *testing.T) {
	for _, codec := range []struct {
		mime, named string
		pt          webrtc.PayloadType
	}{
		{mime: webrtc.MimeTypeAV1, named: "AV1", pt: recorderAV1PT},
		{mime: webrtc.MimeTypeVP9, named: "VP9", pt: recorderVP9PT},
		{mime: webrtc.MimeTypeH265, named: "H265", pt: recorderH265PT},
	} {
		t.Run(codec.named, func(t *testing.T) {
			w := newRecorderWorld(t)
			w.start()

			attempt := w.attempt()
			peer, _ := w.connected(attempt, "")
			track := fakeTrack{kind: webrtc.RTPCodecTypeVideo, codec: webrtc.RTPCodecParameters{
				PayloadType: codec.pt, RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: codec.mime, ClockRate: videoClockRate},
			}}
			w.rec.onTrack(attempt.s, track, fakeReceiver{codecs: []webrtc.RTPCodecParameters{track.codec}})

			w.wait(recorderUnsupportedRetry)
			require.Equal(t, clipStateUnsupported, w.rec.state())
			require.Contains(t, w.rec.reason(), errNotH264.Error())
			require.Contains(t, w.rec.reason(), "(it is "+codec.named+")")
			require.EqualValues(t, 1, peer.closes.Load())
			require.Equal(t, 1, w.log.count("warn", "cannot be clipped"))
		})
	}

	// H.264, and sound in Opus, are read; sound in anything else is left
	// alone, and the clips are silent.
	w := newRecorderWorld(t)
	w.start()
	attempt := w.attempt()
	w.connected(attempt, "")
	h264 := fakeTrack{kind: webrtc.RTPCodecTypeVideo, codec: webrtc.RTPCodecParameters{
		PayloadType: recorderH264PT, RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: videoClockRate},
	}}
	w.rec.onTrack(attempt.s, h264, fakeReceiver{codecs: []webrtc.RTPCodecParameters{h264.codec, {
		PayloadType: 102, RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: videoClockRate},
	}}})
	w.rec.onTrack(attempt.s, fakeTrack{kind: webrtc.RTPCodecTypeAudio, codec: webrtc.RTPCodecParameters{
		PayloadType: 0, RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypePCMU, ClockRate: 8000},
	}}, fakeReceiver{})
	require.True(t, attempt.s.isH264(recorderH264PT))
	require.True(t, attempt.s.isH264(102))
	require.False(t, attempt.s.isH264(recorderAV1PT))
	attempt.s.mu.Lock()
	require.Nil(t, attempt.s.unsupported)
	attempt.s.mu.Unlock()
	require.NotEqual(t, clipStateUnsupported, w.rec.state())
}

// A recorder whose start panics (here over a missing configuration) lets go
// of its lock, so the stop that follows returns rather than waiting on it
// for good.
func TestARecorderStartThatPanicsLeavesItsLockFree(t *testing.T) {
	rec := newClipRecorder(recorderConfig{handle: "abcdef0123456789", dir: t.TempDir(), log: &recorderTestLog{}})
	require.Panics(t, func() { rec.start(context.Background(), apiStream{Handle: "abcdef0123456789"}, nil) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		rec.stop(context.Background(), false)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("stop waited on the lock the panic left held")
	}
}

// A connection whose close panics, in what closeSession waits for, is logged
// once and ends nothing else: its stop returns, and a recorder stopping
// beside it closes its own connection as ever, in the one test binary that
// would otherwise have ended with the panic (recovery.go).
func TestARecorderCloseThatPanicsStopsNothingElse(t *testing.T) {
	broken, sound := newRecorderWorld(t), newRecorderWorld(t)
	broken.start()
	sound.start()
	_, brokenPeer, _ := broken.readySession("11111111-2222-3333-4444-555555555555")
	_, soundPeer, _ := sound.readySession("66666666-7777-8888-9999-000000000000")
	brokenPeer.panics = true

	var stopping sync.WaitGroup
	for _, w := range []*recorderWorld{broken, sound} {
		stopping.Add(1)
		go func() {
			defer stopping.Done()
			ctx, cancel := context.WithTimeout(context.Background(), recorderTestPatience)
			defer cancel()
			if tail, held := w.rec.stop(ctx, true); held {
				tail.ring.remove()
			}
		}()
	}
	stopped := make(chan struct{})
	go func() {
		stopping.Wait()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(recorderTestPatience):
		t.Fatal("a stop waited on the connection whose close panicked")
	}

	require.EqualValues(t, 1, brokenPeer.closes.Load())
	require.EqualValues(t, 1, soundPeer.closes.Load())
	require.Equal(t, 1, broken.log.count("error", panicLogged))
	require.Contains(t, broken.log.text(), "closing a clip recorder's connection")
	require.Zero(t, sound.log.count("error", panicLogged))
	// What was closed is still let go of as the recorder's own.
	require.False(t, broken.own.has("11111111-2222-3333-4444-555555555555", broken.clock.Now().Add(recorderSessionLinger)))
}

// A recorder whose context ends closes its session from run's last defer,
// outside the passes it runs safely. A panic there (here, with the record of
// its own sessions gone) is logged once, and run still ends, so a stop
// waiting on it is not kept waiting for good.
func TestARecorderClosingOnItsOwnThatPanicsStillEnds(t *testing.T) {
	w := newRecorderWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	w.rec.start(ctx, w.stream, &configuration{BroadcastBoxURL: "http://192.168.1.20:8080", ClipLengthSeconds: "120"})
	w.readySession("aaaa-bbbb-cccc")

	// Read by run only after the cancel below, which orders the two.
	w.rec.own = nil
	cancel()
	select {
	case <-w.rec.done:
	case <-time.After(recorderTestPatience):
		t.Fatal("the recorder did not end after the panic closing its session")
	}
	require.Equal(t, 1, w.log.count("error", panicLogged))
	require.Contains(t, w.log.text(), "a clip recorder, closing its session")
}
