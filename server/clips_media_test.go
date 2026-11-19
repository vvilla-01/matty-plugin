package main

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/stretchr/testify/require"
)

// Timing tests drive the video reader with frames at 30 fps (3000 ticks) and
// a keyframe every second, and with arrivals the test makes up. Unless a test
// says otherwise, every frame arrives exactly when its timestamp says.

var mediaTestStart = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC).UnixNano()

// mediaTimedFrames feeds video frames through the reader and the writer.
type mediaTimedFrames struct {
	t       *testing.T
	tp      *mediaTestPipe
	fix     *mediaFixture
	base    uint32 // the first RTP timestamp, chosen to wrap within the test
	sent    int    // frames sent so far
	ticks   int64  // the next frame's ticks since the first
	arrival int64  // the next frame's arrival
	keyDue  int    // the next frame is a keyframe every this many frames
}

func newMediaTimedFrames(t *testing.T, tp *mediaTestPipe) *mediaTimedFrames {
	return &mediaTimedFrames{
		t:       t,
		tp:      tp,
		fix:     loadMediaFixture(t),
		base:    0xFFFF0000, // the timestamp wraps two seconds in
		arrival: mediaTestStart,
		keyDue:  30,
	}
}

// send sends n frames 3000 ticks and 1/30 s apart.
func (f *mediaTimedFrames) send(n int) {
	f.t.Helper()
	for range n {
		f.one(f.sent%f.keyDue == 0)
		f.ticks += 3000
		f.arrival += ticksToNs(3000, videoClockRate)
	}
}

func (f *mediaTimedFrames) one(key bool) {
	f.t.Helper()
	data := mediaFrame(100)
	if key {
		data = mediaKeyFrame(f.fix.sps, f.fix.pps, 200)
	}
	require.NoError(f.t, f.tp.frame(f.base+uint32(f.ticks), data, f.arrival))
	f.sent++
}

// mediaLiveTicks lists the ticks of the video the live ring holds, pending frame
// included.
func mediaLiveTicks(r *ring) []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ticks []int64
	for _, sample := range r.video {
		ticks = append(ticks, sample.ticks)
	}
	if pending := r.pending[ringTrackVideo]; pending != nil {
		ticks = append(ticks, pending.ticks)
	}
	return ticks
}

func TestAccessUnitsDropAUDAndFillerAndDetectIDR(t *testing.T) {
	fix := loadMediaFixture(t)
	aud := []byte{0x09, 0xF0}
	sei := []byte{0x06, 0x05, 0x01, 0xAA, 0x80}
	idr := mediaSlice(0x65, 40)
	filler := []byte{0x0C, 0xFF, 0xFF, 0x80}

	key, err := splitAccessUnit(mediaAnnexB(aud, fix.sps, fix.pps, sei, idr, filler))
	require.NoError(t, err)
	require.True(t, key.key)
	require.Equal(t, fix.sps, key.sps)
	require.Equal(t, fix.pps, key.pps)

	var kept h264.AVCC
	require.NoError(t, kept.Unmarshal(key.payload))
	require.Equal(t, [][]byte{sei, idr}, [][]byte(kept), "AVCC, parameter sets out, AUD and filler dropped")
	require.Equal(t, uint32(len(sei)), binary.BigEndian.Uint32(key.payload))

	// Three-byte start codes, and a frame with no IDR.
	delta, err := splitAccessUnit(append(append([]byte{0, 0, 1}, aud...), append([]byte{0, 0, 1}, mediaSlice(0x41, 30)...)...))
	require.NoError(t, err)
	require.False(t, delta.key)
	require.Nil(t, delta.sps)
	require.NoError(t, kept.Unmarshal(delta.payload))
	require.Len(t, kept, 1)
	require.Equal(t, byte(0x41), kept[0][0])

	// Only delimiters: nothing to keep.
	empty, err := splitAccessUnit(mediaAnnexB(aud))
	require.NoError(t, err)
	require.Empty(t, empty.payload)

	_, err = splitAccessUnit([]byte{0x65, 0x01})
	require.Error(t, err)

	// The fixture's own keyframe round-trips to its AVCC bytes.
	round, err := splitAccessUnit(fix.frameAnnexB(t, 0))
	require.NoError(t, err)
	require.True(t, round.key)
	require.Equal(t, fix.video[0].avcc, round.payload)
	width, height, ok := spsSize(round.sps)
	require.True(t, ok)
	require.Equal(t, []int{320, 180}, []int{width, height})
}

func TestArrivalClockAnchorsFromTheFirstSample(t *testing.T) {
	for _, first := range []uint32{0, 0x7FFFFFFF, 0xFFFFF000, 123456789} {
		clock := arrivalClock{rate: videoClockRate}
		ext := clock.unwrap(first)
		clock.anchor(ext, mediaTestStart)
		require.Equal(t, mediaTestStart, clock.wall(ext), "the first sample is where it arrived")

		next := clock.unwrap(first + 3000)
		require.Equal(t, int64(3000), next)
		require.Equal(t, mediaTestStart+ticksToNs(3000, videoClockRate), clock.wall(next))
		require.Greater(t, time.Unix(0, clock.wall(next)).Year(), 2020, "never near 1970")
	}
}

func TestArrivalClockUnwrapsAndTakesTheEarliestArrival(t *testing.T) {
	clock := arrivalClock{rate: audioClockRate}
	base := uint32(0xFFFFFC40) // 960 ticks before the wrap

	require.Equal(t, int64(0), clock.unwrap(base))
	clock.anchor(0, mediaTestStart+int64(30*time.Millisecond)) // the first packet was 30 ms late
	require.Equal(t, int64(960), clock.unwrap(base+960))       // wraps to 0
	require.Equal(t, int64(1920), clock.unwrap(base+1920))
	require.Equal(t, int64(960), clock.unwrap(base+960), "a step back is a small negative step")
	require.Equal(t, int64(1920), clock.unwrap(base+1920))

	// A packet with less delay, inside the first three seconds, moves the
	// clock earlier.
	clock.anchor(1920, mediaTestStart+ticksToNs(1920, audioClockRate)+int64(5*time.Millisecond))
	require.Equal(t, mediaTestStart+int64(5*time.Millisecond), clock.wall(0))

	// A later one, or one after three seconds, does not.
	clock.anchor(1920, mediaTestStart+ticksToNs(1920, audioClockRate)+int64(20*time.Millisecond))
	require.Equal(t, mediaTestStart+int64(5*time.Millisecond), clock.wall(0))
	late := int64(48000 * 4)
	clock.anchor(late, mediaTestStart+ticksToNs(late, audioClockRate))
	require.Equal(t, mediaTestStart+int64(5*time.Millisecond), clock.wall(0))
}

func TestArrivalMapGivesEachSampleItsFirstPacket(t *testing.T) {
	var arrivals arrivalMap
	arrivals.note(9000, 300)
	arrivals.note(9000, 100) // a retransmission of an earlier packet can arrive out of order
	arrivals.note(9000, 200)
	got, ok := arrivals.take(9000)
	require.True(t, ok)
	require.Equal(t, int64(100), got)
	_, ok = arrivals.take(9000)
	require.False(t, ok, "taken once")

	for ts := range uint32(arrivalMapLimit) {
		arrivals.note(ts, 1)
	}
	arrivals.note(1<<31, 2)
	require.Len(t, arrivals.first, 1, "full of dropped frames' leftovers, it starts again")

	// Through the samplebuilder: each frame is many packets, the first one
	// least delayed. The epoch starts at the IDR's FIRST packet, and a later
	// frame whose first packet came with less delay pulls the clock earlier.
	fix := loadMediaFixture(t)
	tp := newMediaTestPipe(t, 30*time.Second)
	payloader := &codecs.H264Payloader{}
	seq := uint16(65530) // and the sequence numbers wrap
	send := func(ts uint32, frame []byte, firstArrival int64) {
		payloads := payloader.Payload(300, frame)
		require.Greater(t, len(payloads), 2)
		for i, payload := range payloads {
			pkt := &rtp.Packet{
				Header:  rtp.Header{Version: 2, PayloadType: 96, SequenceNumber: seq, Timestamp: ts, Marker: i == len(payloads)-1},
				Payload: payload,
			}
			seq++
			require.NoError(t, tp.video.push(pkt, firstArrival+int64(i)*int64(time.Millisecond)))
			tp.writer.drain()
		}
	}

	send(1000, mediaKeyFrame(fix.sps, fix.pps, 2000), mediaTestStart)
	send(4000, mediaFrame(2000), mediaTestStart+int64(33*time.Millisecond)) // 0.33 ms less delay
	send(7000, mediaFrame(2000), mediaTestStart+int64(67*time.Millisecond))
	send(10000, mediaFrame(2000), mediaTestStart+int64(100*time.Millisecond))

	// The IDR's first packet set the clock; its last came 6 ms later. The
	// second frame's first packet then came 0.33 ms less delayed, and the
	// whole epoch, the IDR included, moved onto that.
	r := tp.live()
	moved := mediaTestStart + int64(33*time.Millisecond) - ticksToNs(3000, videoClockRate)
	require.Equal(t, moved, r.status().epochStart)
	r.mu.Lock()
	defer r.mu.Unlock()
	require.Len(t, r.video, 2)
	require.Equal(t, moved, r.video[0].wall)
	require.Equal(t, mediaTestStart+int64(33*time.Millisecond), r.video[1].wall)
	require.Equal(t, mediaTestStart+int64(33*time.Millisecond)+ticksToNs(3000, videoClockRate), r.pending[ringTrackVideo].wall)
}

func TestTimestampRewriteIsACut(t *testing.T) {
	// Broadcast Box kept the session across an OBS reconnect: the next frame
	// is one frame on in RTP but 5.6 s on in arrival, and then the stream
	// runs at its normal pace.
	tp := newMediaTestPipe(t, 60*time.Second)
	frames := newMediaTimedFrames(t, tp)
	frames.send(300) // 10 s
	lastGood := frames.ticks - 3000
	first := tp.live()

	frames.arrival += int64(5600 * time.Millisecond)
	stretchStart := frames.arrival
	for len(tp.eventsOf(pipeEventCut)) == 0 {
		require.Less(t, frames.arrival-stretchStart, int64(5*time.Second), "the late stretch is cut within excessHold")
		frames.send(1)
	}
	require.GreaterOrEqual(t, frames.arrival-stretchStart, int64(excessHold), "not before excessHold of late arrivals")

	cuts := tp.eventsOf(pipeEventCut)
	require.Equal(t, cutRewritten, cuts[0].reason)
	require.Same(t, first, cuts[0].tail)
	tail := mediaLiveTicks(cuts[0].tail)
	require.Len(t, tail, 300, "the tail ends at the last frame before the late stretch")
	require.Equal(t, lastGood, tail[len(tail)-1])
	requireWallNear(t, mediaTestStart+int64(10*time.Second), cuts[0].tail.status().end, time.Microsecond)

	// The stream goes on in a new epoch from its next keyframe, on a clock
	// anchored afresh: its wall times are its arrivals again.
	frames.send(60)
	resumed := tp.live().status()
	require.NotZero(t, resumed.epochStart)
	require.NotEqual(t, first.epoch, resumed.epoch)
	require.Len(t, tp.eventsOf(pipeEventCut), 1)
	require.Greater(t, resumed.epochStart, stretchStart+int64(excessHold))
}

func TestLateBurstIsNotACut(t *testing.T) {
	// A 3 s network stall: the frames keep their pace in RTP, are held up on
	// the way, and arrive together when it clears, then the stream catches up.
	tp := newMediaTestPipe(t, 60*time.Second)
	frames := newMediaTimedFrames(t, tp)
	frames.send(300)

	stallEnds := frames.arrival + int64(3*time.Second)
	previous := int64(0)
	for range 390 {
		// Held up until the stall ends, then drained a millisecond apart
		// until the stream is back on time.
		onTime := frames.arrival
		frames.arrival = max(onTime, stallEnds, previous+int64(time.Millisecond))
		previous = frames.arrival
		frames.one(frames.sent%frames.keyDue == 0)
		frames.ticks += 3000
		frames.arrival = onTime + ticksToNs(3000, videoClockRate)
	}

	require.Empty(t, tp.eventsOf(pipeEventCut))
	require.Empty(t, tp.eventsOf(pipeEventRestarted))
	require.Len(t, tp.eventsOf(pipeEventReady), 1, "one epoch throughout")
	require.Len(t, mediaLiveTicks(tp.live()), 690, "and every frame in it")
}

func TestForwardJumpIsACut(t *testing.T) {
	tp := newMediaTestPipe(t, 60*time.Second)
	frames := newMediaTimedFrames(t, tp)
	frames.send(300)
	first := tp.live()

	// RTP leaps 10 s ahead while the frame arrives on time.
	frames.ticks += 10 * videoClockRate
	jumped := frames.arrival
	frames.send(1)

	cuts := tp.eventsOf(pipeEventCut)
	require.Len(t, cuts, 1, "at once, on the first such frame")
	require.Equal(t, cutJumpedForward, cuts[0].reason)
	require.Same(t, first, cuts[0].tail)
	require.Len(t, mediaLiveTicks(cuts[0].tail), 300, "nothing before the jump is lost")

	// The jumped frame was a keyframe; the new epoch starts on it, where it
	// arrived. (Arrivals here step by whole nanoseconds, a third of one short
	// of a frame, so the settled clock is a few nanoseconds earlier.)
	frames.send(30)
	requireWallNear(t, jumped, tp.live().status().epochStart, time.Microsecond)
}

func TestPauseWithAgreeingClocksKeepsTheEpoch(t *testing.T) {
	// The publisher stalls; RTP and arrival move on together.
	for _, gap := range []time.Duration{1200 * time.Millisecond, 3 * time.Second} {
		t.Run(gap.String(), func(t *testing.T) {
			tp := newMediaTestPipe(t, 60*time.Second)
			frames := newMediaTimedFrames(t, tp)
			frames.send(300)
			beforeGap := frames.ticks - 3000

			frames.ticks += nsToTicks(int64(gap), videoClockRate)
			frames.arrival += int64(gap)
			// Fifteen frames that reference pictures from before the stall,
			// then a keyframe.
			frames.keyDue = 1 << 30
			frames.send(15)
			keyTicks := frames.ticks
			frames.one(true)
			frames.ticks += 3000
			frames.arrival += ticksToNs(3000, videoClockRate)
			frames.keyDue = 30
			frames.send(29)

			require.Empty(t, tp.eventsOf(pipeEventCut))
			require.Empty(t, tp.eventsOf(pipeEventRestarted))
			require.Len(t, tp.eventsOf(pipeEventReady), 1)

			r := tp.live()
			r.mu.Lock()
			defer r.mu.Unlock()
			require.Len(t, r.video, 300+29, "the frames between the stall and the keyframe are dropped")
			frozen := r.video[299]
			require.Equal(t, beforeGap, frozen.ticks)
			require.Equal(t, uint32(keyTicks-beforeGap), frozen.dur, "the last frame before the stall is shown until the keyframe")
			require.True(t, r.video[300].key)
			require.Equal(t, keyTicks, r.video[300].ticks)
		})
	}

	t.Run("under a second is no stall", func(t *testing.T) {
		tp := newMediaTestPipe(t, 60*time.Second)
		frames := newMediaTimedFrames(t, tp)
		frames.send(300)
		frames.ticks += nsToTicks(int64(900*time.Millisecond), videoClockRate)
		frames.arrival += int64(900 * time.Millisecond)
		frames.keyDue = 1 << 30
		frames.send(15)
		require.Len(t, mediaLiveTicks(tp.live()), 315, "every frame after it is kept")
	})
}

func TestLongPauseEndingOnAnIDRKeepsTheEpoch(t *testing.T) {
	// A paused stream (the no-video watchdog waits while paused) comes back
	// after 61 s on an IDR. The IDR came as soon as it could; that is not
	// keyframes too rare.
	tp := newMediaTestPipe(t, 60*time.Second)
	frames := newMediaTimedFrames(t, tp)
	frames.send(300)
	frames.ticks += 61 * videoClockRate
	frames.arrival += int64(61 * time.Second)
	frames.sent = 0 // the next frame is a keyframe
	frames.send(30)

	require.Empty(t, tp.eventsOf(pipeEventRestarted))
	require.Empty(t, tp.eventsOf(pipeEventCut))
	require.Len(t, tp.eventsOf(pipeEventReady), 1)

	t.Run("but no IDR for a minute after a stall is", func(t *testing.T) {
		tp := newMediaTestPipe(t, 60*time.Second)
		frames := newMediaTimedFrames(t, tp)
		frames.send(300)
		frames.ticks += 3 * videoClockRate
		frames.arrival += int64(3 * time.Second)
		stallEnd := frames.arrival
		frames.keyDue = 1 << 30
		for len(tp.eventsOf(pipeEventRestarted)) == 0 {
			require.Less(t, frames.arrival-stallEnd, int64(maxKeyframeGap+time.Second))
			frames.send(1)
		}
		waited := time.Duration(frames.arrival - stallEnd)
		require.Greater(t, waited, maxKeyframeGap, "timed from the stall's end, not from the IDR before it")
		require.Equal(t, restartTooRare, tp.eventsOf(pipeEventRestarted)[0].reason)
	})
}

func TestExcessLagLooksBackAMinute(t *testing.T) {
	ms := int64(time.Millisecond)
	// lateStretch is 2.1 s of frames arriving 2.5 s later than the base.
	lateStretch := func(d *excessLag, from int64) lagVerdict {
		for at := from; at <= from+2100*ms; at += 33 * ms {
			if verdict, _ := d.observe(at, 2500*ms, 0); verdict != lagFine {
				return verdict
			}
		}
		return lagFine
	}

	// The least-delayed sample was 30 s ago: it is still the base.
	var d excessLag
	d.observe(0, 0, 0)
	require.Equal(t, lagRewritten, lateStretch(&d, 30_000*ms))

	// 61 s ago it is forgotten, and the late samples are the new normal.
	d = excessLag{}
	d.observe(0, 0, 0)
	require.Equal(t, lagFine, lateStretch(&d, 61_000*ms))
}

func TestForwardJumpWhileTheClockSettlesIsACut(t *testing.T) {
	// A second into the epoch, inside the clock's anchoring window, RTP
	// leaps 10 s ahead with the frame on time.
	tp := newMediaTestPipe(t, 60*time.Second)
	frames := newMediaTimedFrames(t, tp)
	frames.send(30)
	frames.ticks += 10 * videoClockRate
	frames.send(1)
	cuts := tp.eventsOf(pipeEventCut)
	require.Len(t, cuts, 1)
	require.Equal(t, cutJumpedForward, cuts[0].reason)
}

func TestBFramesAreReportedUnsupported(t *testing.T) {
	tp := newMediaTestPipe(t, 60*time.Second)
	fix := loadMediaFixture(t)

	// Decode order I0 P3 B1 B2: the timestamps go back a frame.
	require.NoError(t, tp.frame(0, mediaKeyFrame(fix.sps, fix.pps, 200), mediaTestStart))
	require.NoError(t, tp.frame(9000, mediaFrame(100), mediaTestStart+int64(10*time.Millisecond)))
	err := tp.frame(3000, mediaFrame(100), mediaTestStart+int64(20*time.Millisecond))
	require.ErrorIs(t, err, errBFrames)
	require.Contains(t, err.Error(), "set B-frames to 0")
	require.Empty(t, tp.eventsOf(pipeEventCut), "reported, not cut over and over")

	// Anything under a second back is decode order; a second or more is a
	// broken clock, and a cut.
	for _, back := range []int64{89999, 90000} {
		tp := newMediaTestPipe(t, 60*time.Second)
		require.NoError(t, tp.frame(0, mediaKeyFrame(fix.sps, fix.pps, 200), mediaTestStart))
		require.NoError(t, tp.frame(180000, mediaFrame(100), mediaTestStart+int64(2*time.Second)))
		err := tp.frame(uint32(180000-back), mediaFrame(100), mediaTestStart+int64(2033*time.Millisecond))
		if back < videoClockRate {
			require.ErrorIs(t, err, errBFrames, "%d back", back)
			continue
		}
		require.NoError(t, err)
		cuts := tp.eventsOf(pipeEventCut)
		require.Len(t, cuts, 1)
		require.Equal(t, cutWentBackwards, cuts[0].reason)
	}
}

func TestMediaTimestampsGoingBackOrStoppingAreCuts(t *testing.T) {
	t.Run("back a long way", func(t *testing.T) {
		tp := newMediaTestPipe(t, 60*time.Second)
		frames := newMediaTimedFrames(t, tp)
		frames.send(300)
		frames.ticks -= 20 * videoClockRate
		frames.send(1)
		cuts := tp.eventsOf(pipeEventCut)
		require.Len(t, cuts, 1)
		require.Equal(t, cutWentBackwards, cuts[0].reason)
	})

	t.Run("standing still", func(t *testing.T) {
		tp := newMediaTestPipe(t, 60*time.Second)
		frames := newMediaTimedFrames(t, tp)
		frames.send(300)
		frames.ticks -= 3000 // the next frames repeat the last one's timestamp
		for range repeatedTimestampLimit - 1 {
			frames.one(false)
			require.Empty(t, tp.eventsOf(pipeEventCut), "a repeat or two is only dropped")
		}
		frames.one(false)
		cuts := tp.eventsOf(pipeEventCut)
		require.Len(t, cuts, 1)
		require.Equal(t, cutStoppedMoving, cuts[0].reason)
		require.Len(t, mediaLiveTicks(cuts[0].tail), 300)
	})
}

func TestMediaPayloadTypeChanges(t *testing.T) {
	fix := loadMediaFixture(t)
	packet := func(pt uint8, seq uint16, ts uint32, payload []byte) *rtp.Packet {
		return &rtp.Packet{Header: rtp.Header{Version: 2, PayloadType: pt, SequenceNumber: seq, Timestamp: ts, Marker: true}, Payload: payload}
	}

	tp := newMediaTestPipe(t, 60*time.Second, func(cfg *pipelineConfig) {
		cfg.isH264 = func(pt uint8) bool { return pt == 96 || pt == 102 }
	})
	idr := mediaSlice(0x65, 50)
	require.NoError(t, tp.video.push(packet(96, 1, 0, fix.sps), mediaTestStart))
	require.NoError(t, tp.video.push(packet(96, 2, 0, idr), mediaTestStart))

	// Another H.264 payload type: a cut, recording carries on.
	require.NoError(t, tp.video.push(packet(102, 3, 3000, idr), mediaTestStart+int64(33*time.Millisecond)))
	tp.writer.drain()
	require.Len(t, tp.eventsOf(pipeEventCut), 1)
	require.Equal(t, cutCodecChanged, tp.eventsOf(pipeEventCut)[0].reason)

	// Anything else: unsupported.
	require.ErrorIs(t, tp.video.push(packet(45, 4, 6000, idr), mediaTestStart+int64(67*time.Millisecond)), errNotH264)

	// And the watchdog sees every packet's arrival.
	require.Equal(t, mediaTestStart+int64(67*time.Millisecond), tp.video.lastPacket.Load())
}

func TestMediaAudioReaderKeepsTimestampsMovingForward(t *testing.T) {
	fix := loadMediaFixture(t)
	tp := newMediaTestPipe(t, 60*time.Second)
	opus := fix.audio[0].data

	require.NoError(t, tp.frame(0, mediaKeyFrame(fix.sps, fix.pps, 100), mediaTestStart))
	for k, ts := range []uint32{0, 960, 1920, 960, 1920, 2880, 3840} {
		tp.packet(ts+5000, opus, mediaTestStart+int64(k)*int64(20*time.Millisecond))
	}
	require.NoError(t, tp.frame(3000, mediaFrame(100), mediaTestStart+int64(33*time.Millisecond)))

	r := tp.live()
	r.mu.Lock()
	defer r.mu.Unlock()
	var ticks []int64
	for _, sample := range r.audio {
		ticks = append(ticks, sample.ticks)
		require.Equal(t, uint16(960), sample.pcm)
	}
	require.Equal(t, []int64{0, 960, 1920, 2880}, ticks, "repeats and steps back are dropped; the newest is pending")
	require.Equal(t, int64(3840), r.pending[ringTrackAudio].ticks)
	require.Equal(t, mediaTestStart+int64(6)*int64(20*time.Millisecond), tp.audio.lastSample.Load())
}

// mediaAVStream plays 30 fps video and 20 ms Opus packets through both
// readers, every sample on time, with the audio's RTP timestamps moved by
// jump.
type mediaAVStream struct {
	tp     *mediaTestPipe
	frames *mediaTimedFrames
	opus   []byte
	k      int
	jump   uint32
}

func newMediaAVStream(t *testing.T, tp *mediaTestPipe) *mediaAVStream {
	return &mediaAVStream{tp: tp, frames: newMediaTimedFrames(t, tp), opus: loadMediaFixture(t).audio[0].data}
}

func (s *mediaAVStream) packetAt(k int) int64 {
	return mediaTestStart + ticksToNs(int64(k)*opusFrameTicks, audioClockRate)
}

func (s *mediaAVStream) packet() {
	s.tp.packet(0x1000+uint32(s.k*opusFrameTicks)+s.jump, s.opus, s.packetAt(s.k))
	s.k++
}

func (s *mediaAVStream) play(until time.Duration) {
	for s.frames.arrival < mediaTestStart+int64(until) {
		for s.packetAt(s.k) < s.frames.arrival {
			s.packet()
		}
		s.frames.send(1)
	}
}

func TestAudioLeapsAreRebasedNotDropped(t *testing.T) {
	t.Run("one packet far ahead", func(t *testing.T) {
		// One stray packet claims to be a minute ahead. It must neither move
		// the ring's clock (emptying the window) nor stop the sound after it.
		tp := newMediaTestPipe(t, 30*time.Second)
		stream := newMediaAVStream(t, tp)
		stream.play(60 * time.Second)
		stream.jump = 60 * audioClockRate
		stream.packet()
		stream.jump = 0
		stream.play(62 * time.Second)

		snap, err := tp.live().snapshotRange(time.Unix(0, mediaTestStart+int64(62*time.Second)), 30*time.Second)
		require.NoError(t, err, "the window is still held")
		defer snap.release()
		first, last := snap.video[0], snap.video[len(snap.video)-1]
		require.GreaterOrEqual(t, ticksToMs(last.ticks+int64(last.dur)-first.ticks, videoClockRate), int64(29_000))
		requireWallNear(t, snap.video[0].wall, snap.audio[0].wall, audioAlignSlack)
		requireWallNear(t, mediaTestStart+int64(62*time.Second), snap.audio[len(snap.audio)-1].wall, 60*time.Millisecond)
		for i := 1; i < len(snap.audio); i++ {
			step := snap.audio[i].wall - snap.audio[i-1].wall
			requireWallNear(t, int64(20*time.Millisecond), step, time.Millisecond, "no packet lost around the stray one")
		}
	})

	t.Run("timestamps start again further back", func(t *testing.T) {
		// A new timestamp base 30 s behind: the sound carries on where it is,
		// and the watchdog sees it.
		tp := newMediaTestPipe(t, 30*time.Second)
		stream := newMediaAVStream(t, tp)
		stream.play(20 * time.Second)
		back := int32(-30 * audioClockRate)
		stream.jump = uint32(back)
		stream.play(25 * time.Second)

		newest := stream.packetAt(stream.k - 1)
		require.Equal(t, newest, tp.audio.lastSample.Load())
		r := tp.live()
		r.mu.Lock()
		pending := r.pending[ringTrackAudio].wall
		indexed := len(r.audio)
		r.mu.Unlock()
		requireWallNear(t, newest, pending, time.Millisecond)
		require.Equal(t, stream.k-1, indexed, "no packet lost")
	})

	t.Run("a repeat is dropped and is no sign of sound", func(t *testing.T) {
		tp := newMediaTestPipe(t, 30*time.Second)
		stream := newMediaAVStream(t, tp)
		stream.play(5 * time.Second)
		heard := tp.audio.lastSample.Load()
		stream.k--
		tp.packet(0x1000+uint32(stream.k*opusFrameTicks), stream.opus, heard+int64(time.Second))
		require.Equal(t, heard, tp.audio.lastSample.Load())
	})
}

func TestMediaQueueShedsUntilHalfEmpty(t *testing.T) {
	tp := newMediaTestPipe(t, 30*time.Second)
	video, audio := &queueSender{pipe: tp.clipPipeline}, &queueSender{pipe: tp.clipPipeline}
	op := ringOp{track: ringTrackAudio, epoch: tp.epoch.Load(), payload: []byte{1}}
	for range writerQueueSamples {
		require.Equal(t, queueSent, audio.send(op))
	}
	require.Equal(t, queueOverflowed, audio.send(op), "the reader that finds it full cuts")
	require.Equal(t, queueDropped, video.send(op), "the other only sheds")
	require.Equal(t, queueDropped, audio.send(op))

	take := func(n int) {
		for range n {
			got := <-tp.queue
			tp.queued.Add(-int64(len(got.payload)))
		}
	}
	take(writerQueueSamples / 2)
	require.Equal(t, queueDropped, video.send(op), "still half full")
	take(1)
	require.Equal(t, queueSent, video.send(op))
	require.False(t, tp.shedding.Load())

	// Bytes count as well as samples.
	tp = newMediaTestPipe(t, 30*time.Second)
	big := ringOp{track: ringTrackVideo, epoch: tp.epoch.Load(), payload: make([]byte, 1<<20)}
	sender := &queueSender{pipe: tp.clipPipeline}
	for range writerQueueBytes >> 20 {
		require.Equal(t, queueSent, sender.send(big))
	}
	require.Equal(t, queueOverflowed, sender.send(big))
}
