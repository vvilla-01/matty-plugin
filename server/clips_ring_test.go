package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	mp4codecs "github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/pmp4"
	"github.com/stretchr/testify/require"
)

// The media tests run without WebRTC and without real time: every arrival is
// a number the test chooses, the writer is driven by hand (drain) except where
// a test is about the goroutines, and nothing sleeps.

// mediaFixture is testdata/clip-h264-opus.mp4 in memory: 10 s of 320x180 H.264
// at 15 fps with a keyframe every 2 s, and Opus in 20 ms packets.
type mediaFixture struct {
	sps, pps []byte
	video    []mediaFixtureFrame  // 150 frames, 90 kHz ticks
	audio    []mediaFixturePacket // the first 500 packets, exactly 10 s
}

type mediaFixtureFrame struct {
	ticks int64
	avcc  []byte
	key   bool
}

type mediaFixturePacket struct {
	ticks int64
	data  []byte
}

const mediaFixtureLoopTicks = 900000 // 10 s at 90 kHz

var (
	mediaFixtureOnce   sync.Once
	mediaFixtureLoaded *mediaFixture
	mediaFixtureErr    error
)

func loadMediaFixture(t *testing.T) *mediaFixture {
	t.Helper()
	mediaFixtureOnce.Do(func() {
		mediaFixtureLoaded, mediaFixtureErr = readMediaFixture(filepath.Join("testdata", "clip-h264-opus.mp4"))
	})
	require.NoError(t, mediaFixtureErr)
	return mediaFixtureLoaded
}

func readMediaFixture(path string) (*mediaFixture, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var pres pmp4.Presentation
	if err := pres.Unmarshal(f); err != nil {
		return nil, err
	}

	fix := &mediaFixture{}
	for _, track := range pres.Tracks {
		var cum int64
		for _, sample := range track.Samples {
			payload, err := sample.GetPayload()
			if err != nil {
				return nil, err
			}
			switch codec := track.Codec.(type) {
			case *mp4codecs.H264:
				fix.sps, fix.pps = codec.SPS, codec.PPS
				fix.video = append(fix.video, mediaFixtureFrame{
					ticks: cum * videoClockRate / int64(track.TimeScale),
					avcc:  payload,
					key:   !sample.IsNonSyncSample,
				})
			case *mp4codecs.Opus:
				if len(fix.audio) < 500 {
					fix.audio = append(fix.audio, mediaFixturePacket{ticks: cum, data: payload})
				}
			}
			cum += int64(sample.Duration)
		}
	}
	return fix, nil
}

// requireWallNear compares two wall times in integers: InDelta goes through
// float64, which cannot tell nanoseconds apart at 1.8e18.
func requireWallNear(t *testing.T, want, got int64, tolerance time.Duration, about ...any) {
	t.Helper()
	diff := got - want
	if diff < 0 {
		diff = -diff
	}
	require.LessOrEqual(t, diff, int64(tolerance), append([]any{"want %d, got %d", want, got}, about...)...)
}

// annexB joins NAL units with 4-byte start codes, as pion's depacketizer does.
func mediaAnnexB(nalus ...[]byte) []byte {
	var out []byte
	for _, nalu := range nalus {
		out = append(out, 0, 0, 0, 1)
		out = append(out, nalu...)
	}
	return out
}

// frameAnnexB is fixture frame j as it comes off the wire from OBS: Annex-B,
// with the parameter sets in front of every keyframe.
func (fix *mediaFixture) frameAnnexB(t *testing.T, j int) []byte {
	t.Helper()
	var nalus h264.AVCC
	require.NoError(t, nalus.Unmarshal(fix.video[j].avcc))
	if fix.video[j].key {
		return mediaAnnexB(append([][]byte{fix.sps, fix.pps}, nalus...)...)
	}
	return mediaAnnexB(nalus...)
}

// Frames that are only the right NAL types, for tests about timing.
func mediaKeyFrame(sps, pps []byte, size int) []byte {
	return mediaAnnexB(sps, pps, mediaSlice(0x65, size))
}

func mediaFrame(size int) []byte {
	return mediaAnnexB(mediaSlice(0x41, size))
}

func mediaSlice(header byte, size int) []byte {
	slice := bytes.Repeat([]byte{0xAA}, max(size, 2))
	slice[0] = header
	return slice
}

// mediaTestPipe is one recorder's pipeline in a temp directory, with its events
// written down. The test drives the writer by hand (drain) unless it starts
// the writer goroutine with startWriter.
type mediaTestPipe struct {
	*clipPipeline
	dir     string
	mu      sync.Mutex
	events  []pipelineEvent
	running bool
}

func (tp *mediaTestPipe) startWriter(ctx context.Context) {
	tp.running = true
	tp.writer.start(ctx)
}

func newMediaTestPipe(t *testing.T, window time.Duration, configure ...func(*pipelineConfig)) *mediaTestPipe {
	t.Helper()
	tp := &mediaTestPipe{dir: filepath.Join(t.TempDir(), "ring", "abcdef0123456789")}
	cfg := pipelineConfig{dir: tp.dir, handle: "abcdef0123456789", window: window, event: tp.record}
	for _, change := range configure {
		change(&cfg)
	}
	tp.clipPipeline = newClipPipeline(cfg)
	return tp
}

func (tp *mediaTestPipe) record(event pipelineEvent) {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	tp.events = append(tp.events, event)
}

func (tp *mediaTestPipe) eventsOf(kind pipelineEventKind) []pipelineEvent {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	var found []pipelineEvent
	for _, event := range tp.events {
		if event.kind == kind {
			found = append(found, event)
		}
	}
	return found
}

func (tp *mediaTestPipe) eventCount() int {
	tp.mu.Lock()
	defer tp.mu.Unlock()
	return len(tp.events)
}

func (tp *mediaTestPipe) live() *ring {
	return tp.writer.current()
}

// frame and packet hand one sample to a reader and, unless the writer
// goroutine runs, let the writer catch up.
func (tp *mediaTestPipe) frame(ts uint32, data []byte, arrival int64) error {
	err := tp.video.onSample(ts, data, arrival)
	if !tp.running {
		tp.writer.drain()
	}
	return err
}

func (tp *mediaTestPipe) packet(ts uint32, data []byte, arrival int64) {
	tp.audio.onSample(ts, data, arrival)
	if !tp.running {
		tp.writer.drain()
	}
}

// A segment file that keeps nothing, for tests that push gigabytes.
type mediaNullSegment struct{}

func (mediaNullSegment) WriteAt(p []byte, _ int64) (int, error) { return len(p), nil }
func (mediaNullSegment) Close() error                           { return nil }
func (mediaNullSegment) ReadAt(p []byte, _ int64) (int, error) {
	clear(p)
	return len(p), nil
}

func mediaNullSegments(cfg *pipelineConfig) {
	cfg.openSegment = func(string) (segmentFile, error) { return mediaNullSegment{}, nil }
}

// mediaOpsFeeder sends a steady synthetic stream straight to the writer, bypassing
// the readers: video at fps with a keyframe every gop frames, Opus packets
// every 20 ms, and every wall time exactly on its ticks.
type mediaOpsFeeder struct {
	tp        *mediaTestPipe
	start     int64
	fps       int64
	gop       int
	frameSize int
	payload   []byte // shared by every frame when set
	audio     bool
	// ticksPerFrame overrides 90000/fps, for 29.97 fps (3003).
	ticksPerFrame int64
	frames        int
	packets       int
	sps, pps      []byte
}

func newMediaOpsFeeder(t *testing.T, tp *mediaTestPipe, fps int64, gop, frameSize int) *mediaOpsFeeder {
	fix := loadMediaFixture(t)
	return &mediaOpsFeeder{
		tp:        tp,
		start:     time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC).UnixNano(),
		fps:       fps,
		gop:       gop,
		frameSize: frameSize,
		sps:       fix.sps,
		pps:       fix.pps,
	}
}

func (f *mediaOpsFeeder) frameTicks() int64 {
	if f.ticksPerFrame > 0 {
		return f.ticksPerFrame
	}
	return videoClockRate / f.fps
}

func (f *mediaOpsFeeder) frameWall(i int) int64 {
	return f.start + ticksToNs(int64(i)*f.frameTicks(), videoClockRate)
}

func (f *mediaOpsFeeder) packetWall(k int) int64 {
	return f.start + ticksToNs(int64(k)*opusFrameTicks, audioClockRate)
}

// framePayload is the frame's bytes: the shared payload, or one that starts
// with the frame's number so a test can tell frames apart on disk.
func (f *mediaOpsFeeder) framePayload(i int) []byte {
	if f.payload != nil {
		return f.payload
	}
	payload := make([]byte, max(f.frameSize, 8))
	binary.BigEndian.PutUint64(payload, uint64(i))
	return payload
}

// until sends everything whose wall time is before start+d, in wall order.
func (f *mediaOpsFeeder) until(d time.Duration) {
	end := f.start + int64(d)
	for {
		nextVideo, nextAudio := f.frameWall(f.frames), int64(1<<62)
		if f.audio {
			nextAudio = f.packetWall(f.packets)
		}
		if min(nextVideo, nextAudio) >= end {
			return
		}
		epoch := f.tp.epoch.Load()
		if nextVideo <= nextAudio {
			i := f.frames
			op := ringOp{track: ringTrackVideo, epoch: epoch, wall: nextVideo, ticks: int64(i) * f.frameTicks(), payload: f.framePayload(i)}
			if f.gop > 0 && i%f.gop == 0 {
				op.key, op.sps, op.pps = true, f.sps, f.pps
			}
			f.tp.writer.apply(op)
			f.frames++
		} else {
			op := ringOp{track: ringTrackAudio, epoch: epoch, wall: nextAudio, ticks: int64(f.packets) * opusFrameTicks, payload: f.packetPayload(f.packets)}
			f.tp.writer.apply(op)
			f.packets++
		}
	}
}

// packetPayload is a one-frame 20 ms Opus packet that carries its number.
func (f *mediaOpsFeeder) packetPayload(k int) []byte {
	return []byte{0xFC, byte(k >> 8), byte(k)}
}

func (f *mediaOpsFeeder) at(d time.Duration) time.Time {
	return time.Unix(0, f.start+int64(d))
}

func TestRingStartsSegmentsOnKeyframesAndKeepsOnlyTheWindow(t *testing.T) {
	tp := newMediaTestPipe(t, 30*time.Second)
	feed := newMediaOpsFeeder(t, tp, 30, 60, 1000) // a keyframe every 2 s
	feed.audio = true
	feed.until(120 * time.Second)

	r := tp.live()
	r.mu.Lock()
	defer r.mu.Unlock()

	require.GreaterOrEqual(t, len(r.segs), 6)
	for i, seg := range r.segs {
		first := -1
		for j, sample := range r.video {
			if sample.seg == seg.seq {
				first = j
				break
			}
		}
		require.NotEqual(t, -1, first, "segment %d holds video", seg.seq)
		require.True(t, r.video[first].key, "segment %d starts on a keyframe", seg.seq)
		require.Equal(t, r.video[first].wall, seg.start)
		if i > 0 {
			// Keyframes every 2 s and a 6 s target: every segment is 6 s.
			require.Equal(t, int64(ringSegmentTarget), seg.start-r.segs[i-1].start)
		}
	}

	// It holds the whole window and no segment that could not start a draft.
	cutoff := r.now - int64(30*time.Second)
	require.LessOrEqual(t, r.segs[0].start, cutoff)
	require.Greater(t, r.segs[1].start, cutoff)
	require.Equal(t, r.segs[0].seq, r.video[0].seg)
	require.Equal(t, r.segs[0].seq, r.audio[0].seg)

	// Evicted segments are gone from the disk too.
	files, err := os.ReadDir(r.dir)
	require.NoError(t, err)
	require.Len(t, files, len(r.segs))
	var onDisk int64
	for _, file := range files {
		info, err := file.Info()
		require.NoError(t, err)
		onDisk += info.Size()
	}
	require.Equal(t, r.bytes, onDisk)
}

func TestRingSizesToMeasuredBitrate(t *testing.T) {
	// 20 Mbit/s at L = 2:00. A fixed 12 Mbit/s cap would hold 140 s × 1.5 MB/s
	// = 210 MB, which is only 84 s of this stream; a cap from the measured
	// bitrate holds the whole window.
	tp := newMediaTestPipe(t, 120*time.Second, mediaNullSegments)
	feed := newMediaOpsFeeder(t, tp, 30, 60, 2_500_000/30)
	feed.payload = make([]byte, 2_500_000/30)
	feed.until(200 * time.Second)

	snap, err := tp.live().snapshotRange(feed.at(200*time.Second), 120*time.Second)
	require.NoError(t, err)
	defer snap.release()

	require.False(t, snap.truncated)
	require.False(t, snap.partial)
	held := snap.video[len(snap.video)-1].ticks + int64(snap.video[len(snap.video)-1].dur) - snap.video[0].ticks
	require.GreaterOrEqual(t, ticksToMs(held, videoClockRate), int64(118_000))

	status := tp.live().status()
	require.InEpsilon(t, 2_500_000, status.measuredBps, 0.02)
	require.InEpsilon(t, 437_500_000, float64(status.capBytes), 0.02, "(120 s + 20 s) x 1.25 x 2.5 MB/s")
	require.Zero(t, status.truncatedUntil)
	require.Empty(t, tp.eventsOf(pipeEventTruncated))
}

func TestByteCapEvictionIsTruncatedNotPartial(t *testing.T) {
	// 40 Mbit/s at L = 5:00 needs 1.6 GB; one ring holds 1 GiB, about 214 s.
	tp := newMediaTestPipe(t, 300*time.Second, mediaNullSegments)
	feed := newMediaOpsFeeder(t, tp, 10, 20, 500_000)
	feed.payload = make([]byte, 500_000)
	feed.until(330 * time.Second)

	snap, err := tp.live().snapshotRange(feed.at(330*time.Second), 300*time.Second)
	require.NoError(t, err)
	defer snap.release()

	require.True(t, snap.truncated)
	require.False(t, snap.partial, "the stream has been live longer than L; it is the cap that cut it short")
	held := ticksToMs(snap.video[len(snap.video)-1].ticks+int64(snap.video[len(snap.video)-1].dur)-snap.video[0].ticks, videoClockRate)
	require.Less(t, held, int64(300_000))
	require.Greater(t, held, int64(200_000))
	require.LessOrEqual(t, tp.live().size(), int64(ringMaxBytes))

	truncated := tp.eventsOf(pipeEventTruncated)
	require.Len(t, truncated, 1, "reported once, when it starts")
	require.Equal(t, "bitrate above 26 Mbit/s (clips shorter than 5:00)", truncated[0].reason)
}

func TestPinnedSegmentsSurviveEvictionUntilReleased(t *testing.T) {
	tp := newMediaTestPipe(t, 30*time.Second)
	feed := newMediaOpsFeeder(t, tp, 30, 60, 1000)
	feed.until(40 * time.Second)

	snap, err := tp.live().snapshotRange(feed.at(40*time.Second), 30*time.Second)
	require.NoError(t, err)
	first := snap.video[0]
	pinned := snap.files[first.seg]
	require.NotNil(t, pinned)

	// Eighty seconds later every one of those segments is far outside the
	// window, but an export is still reading them.
	feed.until(120 * time.Second)
	_, err = os.Stat(pinned.path)
	require.NoError(t, err, "a pinned segment stays on disk")
	payload := make([]byte, first.size)
	_, err = snap.file(first.seg).ReadAt(payload, int64(first.off))
	require.NoError(t, err)
	frameNumber := int(binary.BigEndian.Uint64(payload))
	require.Equal(t, first.wall, feed.frameWall(frameNumber))

	r := tp.live()
	r.mu.Lock()
	require.Equal(t, pinned, r.segs[0], "eviction stops at the pinned segment")
	r.mu.Unlock()

	snap.release()
	snap.release() // twice is harmless

	// Eviction catches up at the writer's next append.
	feed.until(121 * time.Second)
	_, err = os.Stat(pinned.path)
	require.True(t, os.IsNotExist(err), "released, it is evicted")
	r.mu.Lock()
	defer r.mu.Unlock()
	require.Greater(t, r.segs[1].start, r.now-int64(30*time.Second))
	for _, seg := range r.segs {
		require.Zero(t, seg.pins)
	}
}

func TestRingResetsEpochOnParameterSetChange(t *testing.T) {
	fix := loadMediaFixture(t)
	tp := newMediaTestPipe(t, 30*time.Second)
	start := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC).UnixNano()

	otherSPS := bytes.Clone(fix.sps)
	otherSPS[3] = 0x1f // another level: a new encoder setup
	_, _, ok := spsSize(otherSPS)
	require.True(t, ok)

	send := func(i int, sps []byte) {
		data := mediaFrame(200)
		if i%60 == 0 {
			data = mediaKeyFrame(sps, fix.pps, 400)
		}
		require.NoError(t, tp.frame(uint32(i*3000), data, start+ticksToNs(int64(i)*3000, videoClockRate)))
	}
	for i := range 240 { // 8 s
		send(i, fix.sps)
	}
	before := tp.live()
	require.Equal(t, fix.sps, before.sps)

	send(240, otherSPS)
	send(241, otherSPS)

	cuts := tp.eventsOf(pipeEventCut)
	require.Len(t, cuts, 1)
	require.Equal(t, cutParameterSets, cuts[0].reason)
	require.Same(t, before, cuts[0].tail, "the ended epoch is kept as a tail")
	tail := cuts[0].tail.status()
	require.True(t, tail.frozen)
	require.Equal(t, int64(8000), tail.heldMs)
	requireWallNear(t, start+int64(8*time.Second), tail.end, time.Microsecond)

	after := tp.live()
	require.NotSame(t, before, after)
	require.Equal(t, otherSPS, after.sps)
	require.Equal(t, start+ticksToNs(240*3000, videoClockRate), after.status().epochStart,
		"the new epoch starts at the IDR that changed them")
	require.Len(t, tp.eventsOf(pipeEventReady), 2)
}

func TestRingResetsWhenKeyframesAreTooRare(t *testing.T) {
	t.Run("one segment over the cap", func(t *testing.T) {
		// One GOP of 1 MB frames at 30 fps outgrows the 1 GiB any ring may
		// hold after about 36 s; nothing can be evicted to make room.
		tp := newMediaTestPipe(t, 30*time.Second, mediaNullSegments)
		feed := newMediaOpsFeeder(t, tp, 30, 0, 1_000_000)
		feed.payload = make([]byte, 1_000_000)
		feed.gop = 1 << 30 // only frame 0 is a keyframe
		first := tp.live()
		feed.until(30 * time.Second)
		require.Empty(t, tp.eventsOf(pipeEventRestarted), "a long GOP at a high bitrate is not too rare while it fits")
		feed.until(40 * time.Second)

		restarts := tp.eventsOf(pipeEventRestarted)
		require.Len(t, restarts, 1)
		require.Equal(t, restartTooRare, restarts[0].reason)
		require.Empty(t, tp.eventsOf(pipeEventCut), "no tail is kept")
		require.True(t, first.deleted)
		require.NotSame(t, first, tp.live())
		require.Zero(t, tp.live().status().epochStart, "starting until the next IDR")
	})

	t.Run("no IDR for a minute", func(t *testing.T) {
		fix := loadMediaFixture(t)
		tp := newMediaTestPipe(t, 30*time.Second)
		start := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC).UnixNano()
		first := tp.live()
		for i := range 30*61 + 1 {
			data := mediaFrame(50)
			if i == 0 {
				data = mediaKeyFrame(fix.sps, fix.pps, 50)
			}
			require.NoError(t, tp.frame(uint32(i*3000), data, start+ticksToNs(int64(i)*3000, videoClockRate)))
		}

		restarts := tp.eventsOf(pipeEventRestarted)
		require.Len(t, restarts, 1)
		require.Equal(t, restartTooRare, restarts[0].reason)
		require.Empty(t, tp.eventsOf(pipeEventCut))
		require.True(t, first.deleted)
		_, err := os.Stat(first.dir)
		require.True(t, os.IsNotExist(err), "the epoch is wiped from the disk")
	})
}

// mediaStuckSegment is a disk that does not answer until it is released.
type mediaStuckSegment struct {
	segmentFile
	release <-chan struct{}
}

func (s *mediaStuckSegment) WriteAt(p []byte, off int64) (int, error) {
	<-s.release
	return s.segmentFile.WriteAt(p, off)
}

func TestSlowDiskDoesNotStallTheReader(t *testing.T) {
	fix := loadMediaFixture(t)
	release := make(chan struct{})
	tp := newMediaTestPipe(t, 30*time.Second, func(cfg *pipelineConfig) {
		cfg.openSegment = func(path string) (segmentFile, error) {
			f, err := openSegmentFile(path)
			if err != nil {
				return nil, err
			}
			return &mediaStuckSegment{segmentFile: f, release: release}, nil
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tp.startWriter(ctx)

	start := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC).UnixNano()
	frameAt := func(i int) int64 { return start + ticksToNs(int64(i)*3000, videoClockRate) }
	packetAt := func(k int) int64 { return start + ticksToNs(int64(k)*opusFrameTicks, audioClockRate) }
	play := func(fromFrame, toFrame int) {
		k := int(nsToTicks(frameAt(fromFrame)-start, audioClockRate) / opusFrameTicks)
		for i := fromFrame; i < toFrame; i++ {
			data := mediaFrame(300)
			if i%60 == 0 {
				data = mediaKeyFrame(fix.sps, fix.pps, 600)
			}
			_ = tp.video.onSample(uint32(i*3000), data, frameAt(i))
			for ; packetAt(k) < frameAt(i+1); k++ {
				tp.audio.onSample(uint32(k*opusFrameTicks), fix.audio[k%len(fix.audio)].data, packetAt(k))
			}
		}
	}

	// The readers run on their own goroutine, as in the recorder. The disk is
	// stuck from the first write; three minutes of stream go by. The deadline
	// is only there to fail, rather than hang, if a reader ever blocks.
	fed := make(chan struct{})
	go func() {
		defer close(fed)
		play(0, 30*180)
	}()
	select {
	case <-fed:
	case <-time.After(time.Minute):
		t.Fatal("a reader blocked on the stuck writer")
	}

	require.Positive(t, tp.video.out.overflows+tp.audio.out.overflows, "the queue filled up")
	require.LessOrEqual(t, len(tp.queue), writerQueueSamples+writerQueueReserve)
	require.LessOrEqual(t, tp.queued.Load(), int64(writerQueueBytes))

	// The disk recovers. The writer catches up, applies the cut, and keeps
	// what it had as a tail.
	close(release)
	require.NoError(t, tp.sync(ctx))
	cuts := tp.eventsOf(pipeEventCut)
	require.Len(t, cuts, 1)
	require.Equal(t, cutDiskTooSlow, cuts[0].reason)
	require.NotNil(t, cuts[0].tail)

	// Recording resumes at the next keyframe in a new epoch, on the stream's
	// own timing: the delay the disk caused never reached the clocks.
	play(30*180, 30*190)
	require.NoError(t, tp.sync(ctx))
	resumed := tp.live().status()
	require.NotZero(t, resumed.epochStart)
	requireWallNear(t, frameAt(30*180), resumed.epochStart, time.Microsecond)
	require.Len(t, tp.eventsOf(pipeEventCut), 1)
}

func TestRingFinishKeepsTheEndedEpochAsATail(t *testing.T) {
	t.Run("long enough", func(t *testing.T) {
		tp := newMediaTestPipe(t, 30*time.Second)
		feed := newMediaOpsFeeder(t, tp, 30, 60, 500)
		feed.audio = true
		feed.until(20 * time.Second)

		tail := tp.writer.finish(context.Background(), true)
		require.NotNil(t, tail)
		status := tail.status()
		require.True(t, status.frozen)
		requireWallNear(t, feed.frameWall(feed.frames), status.end, time.Microsecond)

		// A frozen tail is not evicted, however far past its window.
		size := tail.size()
		tail.mu.Lock()
		tail.now += int64(time.Hour)
		tail.evictLocked()
		tail.mu.Unlock()
		require.Equal(t, size, tail.size())

		snap, err := tail.snapshotRange(tail.endTime(), 30*time.Second)
		require.NoError(t, err)
		last := snap.video[len(snap.video)-1]
		require.Equal(t, status.end, last.wall+ticksToNs(int64(last.dur), videoClockRate))
		snap.release()

		tail.remove()
		_, err = os.Stat(tail.dir)
		require.True(t, os.IsNotExist(err))
	})

	t.Run("too short", func(t *testing.T) {
		tp := newMediaTestPipe(t, 30*time.Second)
		feed := newMediaOpsFeeder(t, tp, 30, 60, 500)
		feed.until(3 * time.Second)
		dir := tp.live().dir

		require.Nil(t, tp.writer.finish(context.Background(), true))
		_, err := os.Stat(dir)
		require.True(t, os.IsNotExist(err))
	})

	t.Run("wiped while an export reads it", func(t *testing.T) {
		tp := newMediaTestPipe(t, 30*time.Second)
		feed := newMediaOpsFeeder(t, tp, 30, 60, 500)
		feed.until(12 * time.Second)
		r := tp.live()
		snap, err := r.snapshotRange(feed.at(12*time.Second), 30*time.Second)
		require.NoError(t, err)

		require.Nil(t, tp.writer.finish(context.Background(), false))
		_, err = os.Stat(snap.files[snap.video[0].seg].path)
		require.NoError(t, err, "still pinned")
		_, err = r.snapshotRange(feed.at(12*time.Second), 30*time.Second)
		require.ErrorIs(t, err, errWarmingUp)

		snap.release()
		_, err = os.Stat(r.dir)
		require.True(t, os.IsNotExist(err))
	})
}

func TestRingCutsAreDecidedOnce(t *testing.T) {
	// Both readers find the queue full at the same moment: one cut.
	tp := newMediaTestPipe(t, 30*time.Second)
	from := tp.epoch.Load()
	var wg sync.WaitGroup
	var won atomic.Int32
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if tp.cut(from, ringOp{reason: cutDiskTooSlow, keep: true}) {
				won.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), won.Load())
	require.NotEqual(t, from, tp.epoch.Load())
	require.Len(t, tp.cuts, 1)
	require.Equal(t, tp.epoch.Load(), tp.cuts[from].next)

	// One note to the writer, which then applies the recorded cut.
	require.Equal(t, 1, len(tp.queue))
	op := <-tp.queue
	require.Equal(t, ringOpCut, op.kind)
	require.Equal(t, from, op.epoch)
	require.Equal(t, tp.epoch.Load(), op.next)
	tp.writer.apply(op)
	require.Equal(t, tp.epoch.Load(), tp.live().epoch)
	require.Empty(t, tp.cuts)
	cuts := tp.eventsOf(pipeEventCut)
	require.Len(t, cuts, 1)
	require.Nil(t, cuts[0].tail, "an empty epoch is not kept")
}

func TestRingCapIsSizedFromTheBitrate(t *testing.T) {
	require.Equal(t, int64(437_500_000), ringCap(120*time.Second, 2_500_000), "(120 s + 20 s) x 1.25 x 2.5 MB/s")
	require.Equal(t, int64(75_000_000), ringCap(30*time.Second, 0), "the 12 Mbit/s floor")
	require.Equal(t, int64(75_000_000), ringCap(30*time.Second, 1_000_000), "1.25 MB/s is under the floor")
	require.Equal(t, int64(ringMaxBytes), ringCap(300*time.Second, 5_000_000))

	// Before it has measured ten seconds, a ring is sized from Broadcast
	// Box's reported bitrate.
	tp := newMediaTestPipe(t, 30*time.Second, mediaNullSegments, func(cfg *pipelineConfig) {
		cfg.reportedBps = 40_000_000
	})
	feed := newMediaOpsFeeder(t, tp, 30, 60, 1000)
	feed.until(8 * time.Second)
	status := tp.live().status()
	require.Equal(t, int64(312_500_000), status.capBytes, "(30 s + 20 s) x 1.25 x 5 MB/s")
	require.Zero(t, status.measuredBps)

	// After, from its own: 30 kB/s is under the floor.
	feed.until(20 * time.Second)
	status = tp.live().status()
	require.Equal(t, int64(75_000_000), status.capBytes)
	require.InEpsilon(t, 30_000, status.measuredBps, 0.05)
}

func TestRingCapEvictionOutsideTheWindowIsNotTruncation(t *testing.T) {
	// Segment 0 ends on a frame at 10 s that a stall left on screen; segment
	// 1 starts on the IDR at 13 s. The ring is over its cap.
	dir := t.TempDir()
	second := int64(time.Second)
	build := func(cutoff int64) *ring {
		r := &ring{dir: dir, window: 30 * time.Second, capBytes: 100, settings: &ringSettings{}, epochStart: 1}
		r.segs = []*ringSegment{
			{seq: 0, f: mediaNullSegment{}, path: filepath.Join(dir, "0.seg"), start: 1, bytes: 80},
			{seq: 1, f: mediaNullSegment{}, path: filepath.Join(dir, "1.seg"), start: 13 * second, bytes: 80},
		}
		r.video = []ringSample{
			{wall: 1, seg: 0, key: true, size: 40},
			{wall: 10 * second, seg: 0, size: 40},
			{wall: 13 * second, seg: 1, key: true, size: 80},
		}
		r.bytes = 160
		r.now = cutoff + int64(30*time.Second)
		return r
	}

	// The window opens at 12 s, inside the stall: nothing in segment 0 is
	// inside it.
	r := build(12 * second)
	_, truncated, dropped := r.evictLocked()
	require.Len(t, dropped, 1)
	require.False(t, truncated)
	require.Zero(t, r.truncatedUntil)

	// At 9 s, its last frame is.
	r = build(9 * second)
	_, truncated, dropped = r.evictLocked()
	require.Len(t, dropped, 1)
	require.True(t, truncated)
	require.Equal(t, r.now+int64(30*time.Second), r.truncatedUntil)
}

func TestPartialDraftIsNeverTruncated(t *testing.T) {
	// 40 Mbit/s at L = 5:00: the cap drops minutes from about 214 s on, while
	// the stream is still younger than L.
	tp := newMediaTestPipe(t, 300*time.Second, mediaNullSegments)
	feed := newMediaOpsFeeder(t, tp, 10, 20, 500_000)
	feed.payload = make([]byte, 500_000)
	feed.until(250 * time.Second)
	require.Len(t, tp.eventsOf(pipeEventTruncated), 1)

	snap, err := tp.live().snapshotRange(feed.at(250*time.Second), 300*time.Second)
	require.NoError(t, err)
	defer snap.release()
	require.True(t, snap.partial)
	require.False(t, snap.truncated, "a draft is partial or truncated, never both")
}

func TestTruncationThatStartsAtReleaseIsReported(t *testing.T) {
	// An export pins the front of a 40 Mbit/s ring for a long time, so the cap
	// cannot be kept until it lets go. The writer's next append evicts what
	// the pin held up, and says so.
	tp := newMediaTestPipe(t, 300*time.Second, mediaNullSegments)
	feed := newMediaOpsFeeder(t, tp, 10, 20, 500_000)
	feed.payload = make([]byte, 500_000)
	feed.until(100 * time.Second)
	snap, err := tp.live().snapshotRange(feed.at(100*time.Second), 300*time.Second)
	require.NoError(t, err)
	feed.until(330 * time.Second)
	require.Empty(t, tp.eventsOf(pipeEventTruncated), "nothing can be evicted past the pin")
	require.Greater(t, tp.live().size(), int64(ringMaxBytes))

	snap.release()
	feed.until(331 * time.Second)
	truncated := tp.eventsOf(pipeEventTruncated)
	require.Len(t, truncated, 1)
	require.Equal(t, "bitrate above 26 Mbit/s (clips shorter than 5:00)", truncated[0].reason)
	require.LessOrEqual(t, tp.live().size(), int64(ringMaxBytes))
}

func TestShortEpochIsNotKeptAsATail(t *testing.T) {
	for _, c := range []struct {
		frames int
		kept   bool
	}{{90, false}, {149, false}, {150, true}} {
		tp := newMediaTestPipe(t, 30*time.Second)
		frames := newMediaTimedFrames(t, tp)
		frames.send(c.frames)
		first := tp.live()
		require.True(t, tp.cut(tp.epoch.Load(), ringOp{reason: cutDiskTooSlow, keep: true}))
		tp.writer.drain()

		cuts := tp.eventsOf(pipeEventCut)
		require.Len(t, cuts, 1)
		if !c.kept {
			require.Nil(t, cuts[0].tail, "%d frames", c.frames)
			require.True(t, first.isDeleted())
			_, err := os.Stat(first.dir)
			require.True(t, os.IsNotExist(err))
			continue
		}
		require.Same(t, first, cuts[0].tail)
		require.Equal(t, int64(5000), first.heldMs())
	}
}

func TestWarmingUpWhenTheWindowsFirstKeyframeLeavesTooLittle(t *testing.T) {
	tp := newMediaTestPipe(t, 30*time.Second)
	feed := newMediaOpsFeeder(t, tp, 30, 60, 300) // a keyframe every 2 s
	feed.until(12 * time.Second)

	// The epoch is 8.9 s old, but the window opens at 3.4 s and its first
	// keyframe, at 4 s, is only 4.9 s before the click.
	_, err := tp.live().snapshotRange(feed.at(8900*time.Millisecond), 5500*time.Millisecond)
	require.ErrorIs(t, err, errWarmingUp)

	snap, err := tp.live().snapshotRange(feed.at(9*time.Second), 5500*time.Millisecond)
	require.NoError(t, err)
	defer snap.release()
	require.Equal(t, feed.frameWall(120), snap.video[0].wall)
}

func TestTailEndsOneFrameAfterItsLastFrameEvenAfterAStall(t *testing.T) {
	// The stream ends on the IDR that came after a 3 s stall. That IDR's
	// length was never seen; it is a frame's, not the stall's.
	tp := newMediaTestPipe(t, 30*time.Second)
	frames := newMediaTimedFrames(t, tp)
	frames.send(300)
	frames.ticks += 3 * videoClockRate
	frames.arrival += int64(3 * time.Second)
	idr := frames.arrival
	frames.one(true)

	tail := tp.writer.finish(context.Background(), true)
	require.NotNil(t, tail)
	requireWallNear(t, idr+ticksToNs(3000, videoClockRate), tail.status().end, time.Microsecond)
}

// mediaGate is a disk that answers until it is shut, then holds every write
// until it is opened again.
type mediaGate struct {
	shut    atomic.Bool
	fail    atomic.Bool   // the held writes fail when the gate opens
	waiting chan struct{} // closed when a write first waits
	open    chan struct{}
	once    sync.Once
	opened  sync.Once
}

func newMediaGate() *mediaGate {
	return &mediaGate{waiting: make(chan struct{}), open: make(chan struct{})}
}

func (g *mediaGate) segments(cfg *pipelineConfig) {
	cfg.openSegment = func(path string) (segmentFile, error) {
		f, err := openSegmentFile(path)
		if err != nil {
			return nil, err
		}
		return &mediaGateSegment{segmentFile: f, gate: g}, nil
	}
}

func (g *mediaGate) release() {
	g.opened.Do(func() { close(g.open) })
}

// await returns once a write is held at the gate. The deadline is only there
// to fail rather than hang.
func (g *mediaGate) await(t *testing.T) {
	t.Helper()
	select {
	case <-g.waiting:
	case <-time.After(time.Minute):
		t.Fatal("no write reached the shut disk")
	}
}

type mediaGateSegment struct {
	segmentFile
	gate *mediaGate
}

func (s *mediaGateSegment) WriteAt(p []byte, off int64) (int, error) {
	if s.gate.shut.Load() {
		s.gate.once.Do(func() { close(s.gate.waiting) })
		<-s.gate.open
		if s.gate.fail.Load() {
			return 0, errors.New("input/output error")
		}
	}
	return s.segmentFile.WriteAt(p, off)
}

// mediaWithin fails the test if do has not returned within a minute: what it
// does must not wait on the disk. do runs on its own goroutine, so it must not
// call t or require; it hands its results back for the test to check.
func mediaWithin(t *testing.T, what string, do func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		do()
	}()
	select {
	case <-done:
	case <-time.After(time.Minute):
		t.Fatalf("%s waited on the stuck disk", what)
	}
}

func TestStuckDiskBlocksOnlyTheWriter(t *testing.T) {
	gate := newMediaGate()
	defer gate.release()
	tp := newMediaTestPipe(t, 30*time.Second, gate.segments)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tp.startWriter(ctx)

	frames := newMediaTimedFrames(t, tp)
	frames.send(600)
	require.NoError(t, tp.sync(ctx))
	r := tp.live()
	at := time.Unix(0, frames.arrival-int64(time.Second))
	pinned, err := r.snapshotRange(at, 30*time.Second)
	require.NoError(t, err)

	// The disk sticks on the writer's next append.
	gate.shut.Store(true)
	frames.send(1)
	gate.await(t)

	var snapErr error
	var held, size int64
	mediaWithin(t, "reading the ring", func() {
		var snap *rangeSnap
		if snap, snapErr = r.snapshotRange(at, 30*time.Second); snapErr == nil {
			snap.release()
		}
		held, size = r.status().heldMs, r.size()
		r.endTime()
	})
	require.NoError(t, snapErr)
	require.Equal(t, int64(19966), held)
	require.Positive(t, size)
	mediaWithin(t, "releasing an export", pinned.release)
	mediaWithin(t, "wiping", tp.wipe)
	require.True(t, r.isDeleted())
	_, err = os.Stat(r.dir)
	require.True(t, os.IsNotExist(err), "wiped from the disk at once")

	// The disk answers. The write it was stuck on is thrown away, and the
	// stream carries on in the epoch after the wipe.
	gate.release()
	require.NoError(t, tp.sync(ctx))
	frames.send(60)
	require.NoError(t, tp.sync(ctx))
	restarts := tp.eventsOf(pipeEventRestarted)
	require.Len(t, restarts, 1)
	require.Equal(t, restartWiped, restarts[0].reason)
	require.Empty(t, tp.eventsOf(pipeEventCut))
	require.NotSame(t, r, tp.live())
	require.NotZero(t, tp.live().status().epochStart)
}

func TestFinishGivesUpOnAStuckWriter(t *testing.T) {
	for _, fails := range []bool{false, true} {
		t.Run(map[bool]string{false: "the disk answers", true: "the disk fails"}[fails], func(t *testing.T) {
			gate := newMediaGate()
			defer gate.release()
			tp := newMediaTestPipe(t, 30*time.Second, gate.segments)
			tp.startWriter(context.Background())
			frames := newMediaTimedFrames(t, tp)
			frames.send(600)
			require.NoError(t, tp.sync(context.Background()))
			gate.shut.Store(true)
			gate.fail.Store(fails)
			frames.send(1)
			gate.await(t)

			// The stop's time is up before the disk answers.
			stopped, stop := context.WithCancel(context.Background())
			stop()
			var tail *ring
			mediaWithin(t, "finish", func() { tail = tp.writer.finish(stopped, true) })
			require.NotNil(t, tail)
			status := tail.status()
			require.True(t, status.frozen)
			require.Equal(t, int64(20000), status.heldMs, "what was applied before the disk stuck")
			events := tp.eventCount()

			// When it answers, or fails, what the writer was doing is
			// discarded, quietly, and the tail finish handed over stays.
			gate.release()
			select {
			case <-tp.writer.done:
			case <-time.After(time.Minute):
				t.Fatal("the writer never returned")
			}
			require.False(t, tail.isDeleted())
			require.Equal(t, status, tail.status())
			require.Equal(t, events, tp.eventCount())
			snap, err := tail.snapshotRange(tail.endTime(), 30*time.Second)
			require.NoError(t, err)
			snap.release()
			tail.remove()
		})
	}
}

func TestWipeRacingATailsHandOverDeletesIt(t *testing.T) {
	// The wipe lands just as a cut's tail reaches the recorder: here, from
	// inside the hook. The tail must not outlive it.
	var tp *mediaTestPipe
	var handed *ring
	tp = newMediaTestPipe(t, 30*time.Second, func(cfg *pipelineConfig) {
		record := cfg.event
		cfg.event = func(event pipelineEvent) {
			record(event)
			if event.kind == pipeEventCut && event.tail != nil {
				handed = event.tail
				tp.wipe()
			}
		}
	})
	frames := newMediaTimedFrames(t, tp)
	frames.send(300)
	require.True(t, tp.cut(tp.epoch.Load(), ringOp{reason: cutDiskTooSlow, keep: true}))
	tp.writer.drain()

	require.NotNil(t, handed)
	require.True(t, handed.isDeleted())
	_, err := os.Stat(handed.dir)
	require.True(t, os.IsNotExist(err))
	frames.send(60)
	require.NotZero(t, tp.live().status().epochStart, "and recording goes on")
}

func TestFinishRightAfterTheReadersStop(t *testing.T) {
	// The recorder stops its readers and calls finish at once, with the
	// writer goroutine busy on what they queued. Every op is applied, once,
	// in order, on one goroutine at a time (run with -race).
	fix := loadMediaFixture(t)
	otherSPS := bytes.Clone(fix.sps)
	otherSPS[3] = 0x1f
	for round := range 20 {
		tp := newMediaTestPipe(t, 30*time.Second)
		tp.startWriter(context.Background())
		send := func(i int, sps []byte) {
			data := mediaFrame(100)
			if i%30 == 0 {
				data = mediaKeyFrame(sps, fix.pps, 200)
			}
			require.NoError(t, tp.frame(uint32(i*3000), data, mediaTestStart+ticksToNs(int64(i)*3000, videoClockRate)))
		}
		for i := range 300 {
			send(i, fix.sps)
		}
		for i := 300; i < 510; i++ {
			send(i, otherSPS) // a cut at 10 s
		}

		tail := tp.writer.finish(context.Background(), true)
		require.NotNil(t, tail, "round %d", round)
		cuts := tp.eventsOf(pipeEventCut)
		require.Len(t, cuts, 1, "round %d", round)
		require.Equal(t, int64(10000), cuts[0].tail.heldMs(), "round %d", round)
		require.Equal(t, int64(7000), tail.heldMs(), "round %d", round)
		require.Equal(t, codecString(otherSPS, false), tail.status().codecs, "round %d", round)
		cuts[0].tail.remove()
		tail.remove()
	}
}

func TestWipeCutsTheLiveRingWithNoTail(t *testing.T) {
	t.Run("live", func(t *testing.T) {
		tp := newMediaTestPipe(t, 30*time.Second)
		frames := newMediaTimedFrames(t, tp)
		frames.send(600)
		wiped := tp.live()

		tp.wipe()
		require.True(t, wiped.isDeleted(), "at once")
		_, err := os.Stat(wiped.dir)
		require.True(t, os.IsNotExist(err))
		_, err = exportDraft(context.Background(), tp.live(), time.Unix(0, frames.arrival), 30*time.Second, t.TempDir(), "d1")
		require.ErrorIs(t, err, errWarmingUp)

		wipedAt := frames.arrival
		frames.send(1200)
		live := tp.live()
		require.NotSame(t, wiped, live)
		require.Greater(t, live.status().epochStart, wipedAt-int64(time.Microsecond), "nothing from before the wipe")
		restarts := tp.eventsOf(pipeEventRestarted)
		require.Len(t, restarts, 1)
		require.Equal(t, restartWiped, restarts[0].reason)
		require.Empty(t, tp.eventsOf(pipeEventCut))
		require.Len(t, tp.eventsOf(pipeEventReady), 2)

		snap, err := live.snapshotRange(time.Unix(0, frames.arrival), 30*time.Second)
		require.NoError(t, err)
		snap.release()
	})

	t.Run("a cut not applied yet keeps no tail", func(t *testing.T) {
		tp := newMediaTestPipe(t, 30*time.Second)
		frames := newMediaTimedFrames(t, tp)
		frames.send(300)
		first := tp.live()
		require.True(t, tp.cut(tp.epoch.Load(), ringOp{reason: cutDiskTooSlow, keep: true}))

		tp.wipe()
		frames.send(60)
		require.True(t, first.isDeleted())
		require.Empty(t, tp.eventsOf(pipeEventCut))
		for _, restart := range tp.eventsOf(pipeEventRestarted) {
			require.Equal(t, restartWiped, restart.reason)
		}
		require.Equal(t, tp.epoch.Load(), tp.live().epoch)
		require.NotZero(t, tp.live().status().epochStart)
	})
}

func TestCutsReachTheNewestEpochWithoutTheirNotes(t *testing.T) {
	// Two cuts in a row, and the queue was too full for either note. The
	// writer still applies both, in order, when the newest epoch's first
	// sample reaches it.
	tp := newMediaTestPipe(t, 30*time.Second)
	frames := newMediaTimedFrames(t, tp)
	frames.send(300)
	first := tp.live()
	e1 := tp.epoch.Load()
	require.True(t, tp.cut(e1, ringOp{reason: cutDiskTooSlow, keep: true}))
	require.True(t, tp.cut(tp.epoch.Load(), ringOp{reason: cutParameterSets, keep: true}))
	for len(tp.queue) > 0 {
		<-tp.queue
	}

	resumed := frames.arrival
	frames.send(60)
	requireWallNear(t, resumed, tp.live().status().epochStart, time.Microsecond, "on the newest epoch's first IDR")
	cuts := tp.eventsOf(pipeEventCut)
	require.Len(t, cuts, 2)
	require.Equal(t, cutDiskTooSlow, cuts[0].reason)
	require.Same(t, first, cuts[0].tail)
	require.Equal(t, cutParameterSets, cuts[1].reason)
	require.Nil(t, cuts[1].tail, "the epoch between held nothing")
	require.Equal(t, tp.epoch.Load(), tp.live().epoch)
	require.NotZero(t, tp.live().status().epochStart)
	require.Empty(t, tp.cuts)

	// A straggler from the first epoch changes nothing.
	held := first.heldMs()
	tp.writer.apply(ringOp{track: ringTrackVideo, epoch: e1, wall: frames.arrival, ticks: 1 << 40})
	require.Equal(t, held, first.heldMs())
}

func TestRestartWinsOverACutItRacedWith(t *testing.T) {
	tp := newMediaTestPipe(t, 30*time.Second)
	frames := newMediaTimedFrames(t, tp)
	frames.send(300)
	first := tp.live()
	require.True(t, tp.cut(tp.epoch.Load(), ringOp{reason: cutDiskTooSlow, keep: true}))
	next := tp.epoch.Load()

	// The writer finds the disk failing before it reaches that cut.
	tp.writer.restart(restartWriteError + ": test")
	tp.writer.drain()
	restarts := tp.eventsOf(pipeEventRestarted)
	require.Len(t, restarts, 1)
	require.Equal(t, restartWriteError+": test", restarts[0].reason, "its own reason")
	require.Empty(t, tp.eventsOf(pipeEventCut), "and no tail")
	require.True(t, first.isDeleted())
	require.Equal(t, next, tp.live().epoch, "the epoch the readers are already in")
}

// ringWithin fails the test if do has not returned within ten seconds: what
// it does takes the ring's lock, which a panic must not have left held. do
// runs on its own goroutine, so it must not call t or require.
func ringWithin(t *testing.T, what string, do func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		do()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s waited on a lock a panic left held", what)
	}
}

// faultRing makes the ring's next commit or freeze panic part way through.
func faultRing(r *ring, span string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.testFault = func(at string) {
		if at == span {
			panic("a bug under the ring's lock")
		}
	}
}

// A panic part way through commit or freeze lets go of the ring's lock, and
// leaves nothing half committed for an export to cut: the ring reads as
// empty until it is removed. An export pinned before the panic still reads
// its files, and they go when it is released.
func TestAPanicUnderTheRingsLockLetsGoOfIt(t *testing.T) {
	for _, span := range []string{"commit", "freeze"} {
		t.Run(span, func(t *testing.T) {
			tp := newMediaTestPipe(t, 30*time.Second)
			feed := newMediaOpsFeeder(t, tp, 30, 60, 500)
			feed.audio = true
			feed.until(12 * time.Second)
			r := tp.live()
			pinned, err := r.snapshotRange(feed.at(12*time.Second), 30*time.Second)
			require.NoError(t, err)
			first := pinned.video[0]

			faultRing(r, span)
			require.PanicsWithValue(t, "a bug under the ring's lock", func() {
				if span == "commit" {
					feed.until(13 * time.Second)
				} else {
					r.freeze(0, false)
				}
			})

			var snapErr error
			var status ringStatus
			ringWithin(t, "cutting after the panic", func() {
				_, snapErr = r.snapshotRange(feed.at(12*time.Second), 30*time.Second)
				status = r.status()
			})
			require.ErrorIs(t, snapErr, errWarmingUp, "nothing half committed is cut")
			require.Zero(t, status.heldMs)
			require.False(t, status.frozen)

			got := make([]byte, 8)
			_, err = pinned.file(first.seg).ReadAt(got, int64(first.off))
			require.NoError(t, err)
			require.Equal(t, uint64(first.ticks/feed.frameTicks()), binary.BigEndian.Uint64(got), "the pinned export reads its frame")

			ringWithin(t, "removing the ring", r.remove)
			_, err = os.Stat(pinned.files[first.seg].path)
			require.NoError(t, err, "still pinned")
			ringWithin(t, "releasing the export", pinned.release)
			_, err = os.Stat(r.dir)
			require.True(t, os.IsNotExist(err))
		})
	}
}

// A writer that panics inside commit or inside freeze (applying a cut) starts
// again at the next keyframe, as after a write that failed: the old ring is
// removed, nothing of it is kept as a tail, the new ring can be cut, and
// finish returns. Before 3.7.1 the restart waited for good on the lock the
// panic left held, and the stream's recording stopped without a word.
func TestAWriterThatPanicsUnderTheRingsLockStartsAgain(t *testing.T) {
	for _, span := range []string{"commit", "freeze"} {
		t.Run(span, func(t *testing.T) {
			log := &recorderTestLog{}
			tp := newMediaTestPipe(t, 30*time.Second, func(cfg *pipelineConfig) { cfg.log = log })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tp.startWriter(ctx)
			synced := func(what string) {
				t.Helper()
				waited, stop := context.WithTimeout(ctx, 10*time.Second)
				defer stop()
				require.NoError(t, tp.sync(waited), what)
			}

			frames := newMediaTimedFrames(t, tp)
			frames.send(300)
			synced("the writer before the panic")
			old := tp.live()
			require.Positive(t, old.heldMs())

			faultRing(old, span)
			panicAt := frames.arrival
			if span == "commit" {
				frames.send(1)
			} else {
				require.True(t, tp.cut(tp.epoch.Load(), ringOp{reason: cutDiskTooSlow, keep: true}))
			}
			synced("the writer after the panic")

			restarts := tp.eventsOf(pipeEventRestarted)
			require.Len(t, restarts, 1)
			require.Equal(t, restartWriteError+": "+recorderPanicReason, restarts[0].reason)
			require.Empty(t, tp.eventsOf(pipeEventCut), "no tail of the ring that panicked")
			require.Equal(t, 1, log.count("error", panicLogged))
			require.True(t, old.isDeleted())
			_, err := os.Stat(old.dir)
			require.True(t, os.IsNotExist(err), "the old ring is removed")

			frames.send(240)
			synced("the writer in the new ring")
			live := tp.live()
			require.NotSame(t, old, live)
			status := live.status()
			require.GreaterOrEqual(t, status.epochStart, panicAt-int64(time.Millisecond), "started again at a keyframe from the panic on")
			require.Len(t, tp.eventsOf(pipeEventReady), 2)
			var snapErr error
			ringWithin(t, "cutting from the new ring", func() {
				var snap *rangeSnap
				if snap, snapErr = live.snapshotRange(time.Unix(0, frames.arrival), 30*time.Second); snapErr == nil {
					snap.release()
				}
			})
			require.NoError(t, snapErr)

			var tail *ring
			ringWithin(t, "finish", func() { tail = tp.writer.finish(ctx, true) })
			require.NotNil(t, tail)
			require.True(t, tail.status().frozen)
			tail.remove()
		})
	}
}

// A panic while finish freezes the epoch is taken as a write that failed:
// the epoch is wiped, nothing is kept, and finish returns rather than
// passing the panic on to a recorder's stop, which has nothing to recover it.
func TestAPanicWhileFinishFreezesWipesTheEpoch(t *testing.T) {
	log := &recorderTestLog{}
	tp := newMediaTestPipe(t, 30*time.Second, func(cfg *pipelineConfig) { cfg.log = log })
	frames := newMediaTimedFrames(t, tp)
	frames.send(600)
	r := tp.live()
	require.Positive(t, r.heldMs())

	faultRing(r, "freeze")
	tail := &ring{}
	ringWithin(t, "finish", func() { tail = tp.writer.finish(context.Background(), true) })
	require.Nil(t, tail)
	require.Equal(t, 1, log.count("error", panicLogged))
	require.True(t, r.isDeleted())
	_, err := os.Stat(r.dir)
	require.True(t, os.IsNotExist(err))
}

// A cut whose nudge found the queue full is applied by finish itself, after
// the drain; a panic while that cut freezes the epoch is taken as a write that
// failed too: the epoch is wiped, nothing is kept, and finish returns rather
// than passing the panic on to a recorder's stop, which has nothing to
// recover it.
func TestAPanicWhileFinishAppliesACutWipesTheEpoch(t *testing.T) {
	log := &recorderTestLog{}
	tp := newMediaTestPipe(t, 30*time.Second, func(cfg *pipelineConfig) { cfg.log = log })
	frames := newMediaTimedFrames(t, tp)
	frames.send(600)
	r := tp.live()
	require.Positive(t, r.heldMs())

	require.True(t, tp.cut(tp.epoch.Load(), ringOp{reason: cutDiskTooSlow, keep: true}))
	// The nudge is taken back out, as if the queue had been full: the cut is
	// recorded, and nothing queued reaches the next epoch.
	nudge := <-tp.queue
	require.Equal(t, ringOpCut, nudge.kind)
	require.Empty(t, tp.queue)

	faultRing(r, "freeze")
	tail := &ring{}
	ringWithin(t, "finish", func() { tail = tp.writer.finish(context.Background(), true) })
	require.Nil(t, tail)
	require.Equal(t, 1, log.count("error", panicLogged))
	require.Empty(t, tp.eventsOf(pipeEventCut), "no tail of the ring that panicked")
	require.True(t, r.isDeleted())
	_, err := os.Stat(r.dir)
	require.True(t, os.IsNotExist(err))
}

// A writer that finish has abandoned on a stuck disk, and that then panics
// when the disk answers, starts nothing again: the tail finish took stays as
// it was, and the recorder hears nothing more (applySafely).
func TestAnAbandonedWriterThatPanicsStartsNothingAgain(t *testing.T) {
	log := &recorderTestLog{}
	gate := newMediaGate()
	defer gate.release()
	tp := newMediaTestPipe(t, 30*time.Second, func(cfg *pipelineConfig) {
		cfg.log = log
		cfg.openSegment = func(path string) (segmentFile, error) {
			f, err := openSegmentFile(path)
			if err != nil {
				return nil, err
			}
			return &mediaPanicGateSegment{segmentFile: f, gate: gate}, nil
		}
	})
	tp.startWriter(context.Background())
	frames := newMediaTimedFrames(t, tp)
	frames.send(600)
	require.NoError(t, tp.sync(context.Background()))
	gate.shut.Store(true)
	frames.send(1)
	gate.await(t)

	stopped, stop := context.WithCancel(context.Background())
	stop()
	var tail *ring
	ringWithin(t, "finish", func() { tail = tp.writer.finish(stopped, true) })
	require.NotNil(t, tail)
	status := tail.status()
	events := tp.eventCount()

	gate.release()
	select {
	case <-tp.writer.done:
	case <-time.After(time.Minute):
		t.Fatal("the writer never returned")
	}
	require.Equal(t, 1, log.count("error", panicLogged))
	require.Same(t, tail, tp.live(), "no new ring")
	require.False(t, tail.isDeleted())
	require.Equal(t, status, tail.status())
	require.Equal(t, events, tp.eventCount())
	snap, err := tail.snapshotRange(tail.endTime(), 30*time.Second)
	require.NoError(t, err)
	snap.release()
	tail.remove()
}

// mediaPanicGateSegment is mediaGateSegment whose held writes panic when the
// gate opens.
type mediaPanicGateSegment struct {
	segmentFile
	gate *mediaGate
}

func (s *mediaPanicGateSegment) WriteAt(p []byte, off int64) (int, error) {
	if s.gate.shut.Load() {
		s.gate.once.Do(func() { close(s.gate.waiting) })
		<-s.gate.open
		panic("the disk answered with a bug")
	}
	return s.segmentFile.WriteAt(p, off)
}
