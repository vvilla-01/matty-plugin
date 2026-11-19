package main

import (
	"bytes"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4/pkg/media/samplebuilder"
)

// The clip recorder's media handling: everything between an RTP packet read
// from Broadcast Box and an op handed to the ring writer (clips_ring.go), and
// nothing that needs a PeerConnection. The recorder (clips_recorder.go) owns
// the session and runs one goroutine per track that does only this:
//
//	for {
//	    pkt, _, err := track.ReadRTP()
//	    if err != nil { return }
//	    if err := reader.push(pkt, time.Now().UnixNano()); err != nil { ...unsupported... }
//	}
//
// A reader never touches the disk or a ring's lock. Broadcast Box sends a
// 6 Mbit/s stream into a socket buffer that holds about 280 ms, so a reader
// that waited for a slow write would lose packets, and would also stamp the
// ones it did read late, which looks exactly like the timestamp discontinuity
// the excess-lag rule below hunts for. So a reader only computes, and hands
// each sample to the writer through a bounded queue it never blocks on.
//
// Timing is by arrival. Each track keeps an arrivalClock that maps its RTP
// timestamps onto this server's wall clock, anchored on the least-delayed
// arrival of the epoch's first three seconds. Audio and video are lined up by
// those wall times alone; RTCP sender reports would only map Broadcast Box's
// clock onto ours, and the measured error of arrival anchoring was a few
// milliseconds, constant, with no drift.

// errBFrames and errNotH264 mean the stream cannot be recorded. The recorder
// reports "unsupported" with the error as the admin reason, closes, and tries
// again in five minutes. B-frames cannot be stored because the MP4 is written
// with no composition offsets (see clips_mux.go).
var (
	errBFrames = errors.New("B-frames (set B-frames to 0; do not use the NVENC UHQ tune)")
	errNotH264 = errors.New("the video is not H.264")
)

// opusSilence is a 20 ms CELT frame that decodes to digital silence. Gaps in a
// stream's audio are filled with it at export, so that the audio timeline
// keeps its length: letting a gap stretch the previous packet's duration
// instead made a 10 s test clip decode to only 7 s of audio, out of sync.
// Shared and never written to.
var opusSilence = []byte{0xF8, 0xFF, 0xFE}

// ticksToMs is the one conversion from a track's ticks to milliseconds. The
// ring, the draft's numbers, export and trim all use it on cumulative ticks,
// never on per-sample milliseconds summed, so that a trim at exactly the
// millisecond a draft reported for a keyframe lands on that keyframe.
func ticksToMs(ticks int64, timescale uint32) int64 {
	return ticks * 1000 / int64(timescale)
}

// ticksToNs converts ticks at rate per second into nanoseconds without the
// overflow ticks*1e9 would hit after a day of 90 kHz video.
func ticksToNs(ticks, rate int64) int64 {
	return ticks/rate*int64(time.Second) + ticks%rate*int64(time.Second)/rate
}

// nsToTicks is the reverse, rounding down.
func nsToTicks(ns, rate int64) int64 {
	return ns/int64(time.Second)*rate + ns%int64(time.Second)*rate/int64(time.Second)
}

// nsToTicksUp rounds up instead: the ticks it takes to cover ns.
func nsToTicksUp(ns, rate int64) int64 {
	return ns/int64(time.Second)*rate + (ns%int64(time.Second)*rate+int64(time.Second)-1)/int64(time.Second)
}

// arrivalMap remembers when each RTP timestamp's first packet arrived. pion's
// samplebuilder hands back a sample with no arrival time, and only once the
// packet after it has come, so the reader notes every packet's arrival here
// and takes the frame's back when the sample pops out. The first packet is
// the least delayed, which is the arrival the clock wants.
type arrivalMap struct {
	first map[uint32]int64
}

func (m *arrivalMap) note(ts uint32, arrival int64) {
	if m.first == nil {
		m.first = make(map[uint32]int64)
	}
	if known, ok := m.first[ts]; ok && known <= arrival {
		return
	}
	// Entries leave as their samples pop. What stays behind belongs to frames
	// the samplebuilder dropped, so a full map is cleared rather than grown;
	// the few samples that then miss fall back to the time they popped, which
	// is later than the truth and therefore safe.
	if len(m.first) >= arrivalMapLimit {
		clear(m.first)
	}
	m.first[ts] = arrival
}

func (m *arrivalMap) take(ts uint32) (int64, bool) {
	arrival, ok := m.first[ts]
	if ok {
		delete(m.first, ts)
	}
	return arrival, ok
}

// arrivalClock maps one track's RTP timestamps onto wall time, for one epoch.
// The first sample sets the offset, so a wall time is never near 1970; during
// the first arrivalAnchorWindow the offset may only move earlier, to the
// least-delayed arrival seen, and after that it is fixed. Every op carries the
// offset it was mapped with, and the ring moves the walls it already holds
// when the offset moves (ring.reanchorLocked), so that the whole epoch ends up
// on the one offset the spec asks for.
type arrivalClock struct {
	rate        int64 // 90000 or 48000
	started     bool
	last        uint32
	ext         int64 // unwrapped ticks since the epoch's first sample
	anchored    bool
	offsetNs    int64 // wall(ext) = offsetNs + ext in ns
	anchorUntil int64 // arrival ns; the offset may move earlier only before this
}

// unwrap turns a 32-bit RTP timestamp into ticks since the epoch's first
// sample. Steps are read as signed, so a timestamp just behind the last one is
// a small negative step rather than four billion ticks forward.
func (c *arrivalClock) unwrap(ts uint32) int64 {
	if !c.started {
		c.started, c.last, c.ext = true, ts, 0
		return 0
	}
	c.ext += int64(int32(ts - c.last))
	c.last = ts
	return c.ext
}

func (c *arrivalClock) anchor(ext, arrivalNs int64) {
	candidate := arrivalNs - ticksToNs(ext, c.rate)
	if !c.anchored {
		c.offsetNs, c.anchored, c.anchorUntil = candidate, true, arrivalNs+int64(arrivalAnchorWindow)
		return
	}
	if arrivalNs < c.anchorUntil && candidate < c.offsetNs {
		c.offsetNs = candidate
	}
}

func (c *arrivalClock) wall(ext int64) int64 {
	return c.offsetNs + ticksToNs(ext, c.rate)
}

// rebase puts the sample with timestamp ts where it arrived, keeping the
// offset, and carries on unwrapping from there: for audio whose timestamps
// leapt, so that its ticks continue from where its sound really is. floor is
// the least the sample's ticks may be, to keep them moving forward.
func (c *arrivalClock) rebase(ts uint32, arrivalNs, floor int64) int64 {
	c.ext = max(floor, nsToTicks(arrivalNs-c.offsetNs, c.rate))
	c.last = ts
	return c.ext
}

// excessLag tells a timestamp rewrite from a late burst. A sample's lag is how
// far it arrived behind its RTP time, measured as arrival minus the ticks
// since the epoch's first sample: the clock's offset is left out, so moving
// it while the clock settles changes no verdict. base is the smallest lag of
// the last excessBaseWindow, kept in a monotonic queue, and excess is lag
// minus base.
//
//   - Excess below -excessLimit on any one sample: the timestamps leapt ahead
//     with no matching wait. Cut now.
//   - Excess above excessLimit on every sample across excessHold of arrivals:
//     Broadcast Box kept the session through an OBS reconnect and rewrote the
//     timestamps (measured: RTP +0 s against the wall +5.6 s). The stream
//     then runs at its normal pace, so the excess never drains. Cut, at the
//     last sample before the late stretch began.
//   - A burst delivered late after a network stall drains within about a
//     second, so it never lasts excessHold, and is not a discontinuity.
//   - A publisher pause moves RTP and arrival together; its excess is about
//     zero and the epoch is kept (see the gap rule in videoReader).
type excessLag struct {
	lows      []lagPoint // lags increase from head to tail; lows[head] is the base
	head      int
	late      bool
	lateSince int64 // arrival of the first sample of the current late stretch
	lastGood  int64 // ticks of the sample just before that stretch
}

type lagPoint struct {
	arrival, lag int64
}

type lagVerdict int

const (
	lagFine lagVerdict = iota
	lagJumpedForward
	lagRewritten
)

// observe judges one video sample against the samples before it, then adds
// it. prevTicks is the previous sample's ticks, the cut point should this
// sample begin a late stretch that lasts.
func (d *excessLag) observe(arrival, lag, prevTicks int64) (lagVerdict, int64) {
	for d.head < len(d.lows) && d.lows[d.head].arrival < arrival-int64(excessBaseWindow) {
		d.head++
	}

	if d.head < len(d.lows) {
		excess := lag - d.lows[d.head].lag
		switch {
		case excess < -int64(excessLimit):
			return lagJumpedForward, 0
		case excess > int64(excessLimit):
			if !d.late {
				d.late, d.lateSince, d.lastGood = true, arrival, prevTicks
			} else if arrival-d.lateSince >= int64(excessHold) {
				return lagRewritten, d.lastGood
			}
		default:
			d.late = false
		}
	}

	for len(d.lows) > d.head && d.lows[len(d.lows)-1].lag >= lag {
		d.lows = d.lows[:len(d.lows)-1]
	}
	d.lows = append(d.lows, lagPoint{arrival: arrival, lag: lag})
	if d.head > 64 && d.head*2 > len(d.lows) {
		d.lows = append(d.lows[:0], d.lows[d.head:]...)
		d.head = 0
	}
	return lagFine, 0
}

// h264AccessUnit is one H.264 frame as the ring keeps it: AVCC (4-byte
// lengths), with the parameter sets taken out, because they belong only in the
// MP4's avcC box, and access unit delimiters and filler dropped, because they
// are waste.
type h264AccessUnit struct {
	payload  []byte
	key      bool   // holds an IDR slice (NAL type 5)
	sps, pps []byte // the parameter sets this frame carried in-band, if any
}

// splitAccessUnit reads one Annex-B frame, as pion's H.264 depacketizer
// rebuilds it.
func splitAccessUnit(annexB []byte) (h264AccessUnit, error) {
	var nalus h264.AnnexB
	if err := nalus.Unmarshal(annexB); err != nil {
		return h264AccessUnit{}, fmt.Errorf("the frame is not Annex-B H.264: %w", err)
	}

	var au h264AccessUnit
	kept := make(h264.AVCC, 0, len(nalus))
	for _, nalu := range nalus {
		if len(nalu) == 0 {
			continue
		}
		switch h264.NALUType(nalu[0] & 0x1f) {
		case h264.NALUTypeSPS:
			au.sps = bytes.Clone(nalu)
		case h264.NALUTypePPS:
			au.pps = bytes.Clone(nalu)
		case h264.NALUTypeAccessUnitDelimiter, h264.NALUTypeFillerData:
		case h264.NALUTypeIDR:
			au.key = true
			kept = append(kept, nalu)
		default:
			kept = append(kept, nalu)
		}
	}

	if len(kept) > 0 {
		payload, err := kept.Marshal()
		if err != nil {
			return h264AccessUnit{}, fmt.Errorf("the frame could not be rewritten as AVCC: %w", err)
		}
		au.payload = payload
	}
	return au, nil
}

// spsSize reads the picture size from a sequence parameter set, and says
// whether it can be read at all: a frame that decodes with an unreadable one
// could not be put in an MP4's avcC.
func spsSize(sps []byte) (width, height int, ok bool) {
	var parsed h264.SPS
	if len(sps) < 4 || parsed.Unmarshal(sps) != nil {
		return 0, 0, false
	}
	return parsed.Width(), parsed.Height(), true
}

// queueSender is a reader's side of the writer queue. A send never blocks:
// when the queue is full the sample is dropped, the reader cuts the epoch
// ("disk too slow"), and then both readers drop everything until the queue is
// below half full. Shedding belongs to the pipeline, not to one reader, so
// that the other reader, finding the same full queue a moment later, does not
// cut the epoch that has only just begun.
type queueSender struct {
	pipe      *clipPipeline
	overflows int64 // times this reader began an overflow; for tests and the debug log
}

type queueSendResult int

const (
	queueSent queueSendResult = iota
	queueDropped
	queueOverflowed
)

func (s *queueSender) send(op ringOp) queueSendResult {
	queue := s.pipe.queue
	if s.pipe.shedding.Load() {
		if len(queue) >= writerQueueSamples/2 || s.pipe.queued.Load() >= writerQueueBytes/2 {
			return queueDropped
		}
		s.pipe.shedding.Store(false)
	}

	size := int64(len(op.payload))
	if len(queue) >= writerQueueSamples || s.pipe.queued.Load()+size > writerQueueBytes {
		return s.overflow()
	}
	s.pipe.queued.Add(size)
	select {
	case queue <- op:
		return queueSent
	default:
		s.pipe.queued.Add(-size)
		return s.overflow()
	}
}

// overflow starts shedding. Only the reader that starts it cuts the epoch.
func (s *queueSender) overflow() queueSendResult {
	if !s.pipe.shedding.CompareAndSwap(false, true) {
		return queueDropped
	}
	s.overflows++
	return queueOverflowed
}

// videoReader turns one video track's RTP packets into ring ops, applying the
// checks of §2.6 on the way: B-frames, broken timestamps, the excess-lag rule,
// publisher stalls, rare keyframes and parameter-set changes. Only the track's
// own goroutine may call push or onSample.
type videoReader struct {
	pipe     *clipPipeline
	out      queueSender
	sb       *samplebuilder.SampleBuilder
	arrivals arrivalMap
	isH264   func(pt uint8) bool

	// lastPacket is the arrival of the newest RTP packet, for the recorder's
	// no-video watchdog.
	lastPacket atomic.Int64

	pt     uint8
	havePT bool

	// The parameter sets most recently seen in-band. They outlive an epoch:
	// a publisher that sends them only now and then still starts the next
	// epoch at its next IDR.
	sps, pps []byte

	// Per epoch; reset by startEpoch.
	epoch       uint64
	clock       arrivalClock
	lag         excessLag
	havePrev    bool
	prevExt     int64
	repeats     int
	waitKey     bool
	afterGap    bool
	keyed       bool
	lastKeyExt  int64 // ticks of the newest IDR
	keyWaitFrom int64 // ticks of the first frame after the newest stall
	epochSPS    []byte
	epochPPS    []byte
}

func newVideoReader(pipe *clipPipeline, isH264 func(pt uint8) bool) *videoReader {
	return &videoReader{
		pipe:   pipe,
		out:    queueSender{pipe: pipe},
		sb:     samplebuilder.New(1024, &codecs.H264Packet{}, videoClockRate, samplebuilder.WithMaxTimeDelay(time.Second)),
		isH264: isH264,
	}
}

// push takes one RTP packet and the time it was read, in Unix ns. The packet
// is kept by the samplebuilder, so the caller must not reuse its buffer; pion's
// ReadRTP allocates a new one each time. A non-nil error means the stream
// cannot be recorded (errBFrames, errNotH264) and the recorder should stop.
func (r *videoReader) push(pkt *rtp.Packet, arrival int64) error {
	r.lastPacket.Store(arrival)

	if !r.havePT || pkt.PayloadType != r.pt {
		if r.isH264 != nil && !r.isH264(pkt.PayloadType) {
			return errNotH264
		}
		if r.havePT {
			r.cut(cutCodecChanged, true, 0, false)
		}
		r.pt, r.havePT = pkt.PayloadType, true
	}

	r.arrivals.note(pkt.Timestamp, arrival)
	r.sb.Push(pkt)
	for sample := r.sb.Pop(); sample != nil; sample = r.sb.Pop() {
		// A frame whose first packet went unrecorded is stamped with now,
		// which is later than the truth and so never pulls the clock early.
		at, ok := r.arrivals.take(sample.PacketTimestamp)
		if !ok {
			at = arrival
		}
		if err := r.onSample(sample.PacketTimestamp, sample.Data, at); err != nil {
			return err
		}
	}
	return nil
}

// onSample handles one complete frame: its RTP timestamp, its Annex-B bytes
// and the arrival of its first packet.
func (r *videoReader) onSample(ts uint32, data []byte, arrival int64) error {
	if r.pipe.epoch.Load() != r.epoch {
		r.startEpoch()
	}

	ext := r.clock.unwrap(ts)
	lag := arrival - ticksToNs(ext, videoClockRate)
	if !r.havePrev {
		r.clock.anchor(ext, arrival)
		r.havePrev, r.prevExt = true, ext
		r.lag.observe(arrival, lag, ext)
		return r.forward(ts, ext, data, arrival)
	}

	step := ext - r.prevExt
	switch {
	case step < 0 && step > -videoClockRate:
		// Decode order is not presentation order.
		return errBFrames
	case step <= -videoClockRate:
		r.cut(cutWentBackwards, true, 0, false)
		return r.onSample(ts, data, arrival)
	case step == 0:
		// Two frames with one timestamp cannot both be shown; the second is
		// dropped, and a run of them means the timestamps have broken.
		r.repeats++
		if r.repeats >= repeatedTimestampLimit {
			r.cut(cutStoppedMoving, true, 0, false)
			return r.onSample(ts, data, arrival)
		}
		return nil
	}
	r.repeats = 0

	verdict, lastGood := r.lag.observe(arrival, lag, r.prevExt)
	switch verdict {
	case lagJumpedForward:
		r.cut(cutJumpedForward, true, 0, false)
		return r.onSample(ts, data, arrival)
	case lagRewritten:
		r.cut(cutRewritten, true, lastGood, true)
		return r.onSample(ts, data, arrival)
	}
	r.clock.anchor(ext, arrival)
	r.prevExt = ext

	if step > nsToTicks(int64(gapMin), videoClockRate) {
		// The publisher stalled and the clocks agree. Keep the epoch: the
		// frame before the gap is shown until the next IDR, because the
		// frames between would reference pictures that never came. The wait
		// for that IDR is timed from here, not from the IDR before the stall.
		r.afterGap, r.waitKey, r.keyWaitFrom = true, true, ext
	}
	return r.forward(ts, ext, data, arrival)
}

// forward turns a frame that passed the timing checks into a ring op, or
// drops it while the epoch waits for an IDR.
func (r *videoReader) forward(ts uint32, ext int64, data []byte, arrival int64) error {
	au, err := splitAccessUnit(data)
	if err != nil {
		// A frame the depacketizer could not rebuild is left out, like a
		// frame lost on the wire; the decoder conceals it.
		return nil
	}
	if au.sps != nil {
		if _, _, ok := spsSize(au.sps); ok {
			r.sps = au.sps
		}
	}
	if au.pps != nil {
		r.pps = au.pps
	}
	if len(au.payload) == 0 {
		return nil
	}

	if au.key {
		if r.sps == nil || r.pps == nil {
			return nil
		}
		if r.epochSPS != nil && (!bytes.Equal(r.sps, r.epochSPS) || !bytes.Equal(r.pps, r.epochPPS)) {
			// A new picture size or encoder setup: the new epoch starts at
			// this IDR, with the new parameter sets.
			r.cut(cutParameterSets, true, 0, false)
			return r.onSample(ts, data, arrival)
		}
		if r.epochSPS == nil {
			r.epochSPS, r.epochPPS = r.sps, r.pps
		}
		r.waitKey, r.keyed, r.lastKeyExt = false, true, ext
	} else {
		// An IDR that arrives is enough, however long it took; only a frame
		// that is not one, maxKeyframeGap after the newest IDR (or the end of
		// the newest stall), means the keyframes are too rare to evict by.
		if r.keyed && ext-max(r.lastKeyExt, r.keyWaitFrom) > nsToTicks(int64(maxKeyframeGap), videoClockRate) {
			r.cut(restartTooRare, false, 0, false)
			return r.onSample(ts, data, arrival)
		}
		if r.waitKey {
			return nil
		}
	}

	op := ringOp{
		track:    ringTrackVideo,
		epoch:    r.epoch,
		wall:     r.clock.wall(ext),
		offset:   r.clock.offsetNs,
		ticks:    ext,
		key:      au.key,
		afterGap: r.afterGap,
		payload:  au.payload,
	}
	if au.key {
		op.sps, op.pps = r.epochSPS, r.epochPPS
	}

	switch r.out.send(op) {
	case queueSent:
		r.afterGap = false
	case queueDropped:
		// The frames after it would reference a picture the ring never got.
		r.waitKey = true
	case queueOverflowed:
		r.cut(cutDiskTooSlow, true, 0, false)
	}
	return nil
}

// cut ends the current epoch. Whoever moves the pipeline's epoch on first
// records the cut; a reader that finds it already moved (the other reader
// cut, a wipe, or the writer restarted) only follows.
func (r *videoReader) cut(reason string, keep bool, upTo int64, hasUpTo bool) {
	r.pipe.cut(r.epoch, ringOp{reason: reason, keep: keep, upTo: upTo, hasUpTo: hasUpTo})
	r.startEpoch()
}

func (r *videoReader) startEpoch() {
	r.epoch = r.pipe.epoch.Load()
	r.clock = arrivalClock{rate: videoClockRate}
	r.lag = excessLag{}
	r.havePrev, r.prevExt, r.repeats = false, 0, 0
	r.waitKey, r.afterGap, r.keyed = true, false, false
	r.lastKeyExt, r.keyWaitFrom = 0, 0
	r.epochSPS, r.epochPPS = nil, nil
}

// audioReader turns the Opus track's RTP packets into ring ops. One Opus
// packet is one sample, its duration comes from the RTP step to the next, and
// every decision about epochs is the video reader's.
//
// Its only checks keep the audio's ticks moving forward with its sound. A
// repeat or a small step back is dropped, so a duration is never zero or
// negative. A packet that claims to be more than excessLimit ahead of its
// arrival, or a step back further than that, means the timestamps leapt (a
// stray packet, or a publisher's new timestamp base): the packet is put where
// it arrived and the track carries on from there. Dropping instead would lose
// every packet until the old timeline caught up, hours for a large leap, with
// the audio watchdog fooled into thinking sound still flowed.
type audioReader struct {
	pipe     *clipPipeline
	out      queueSender
	sb       *samplebuilder.SampleBuilder
	arrivals arrivalMap

	// lastSample is the arrival of the newest audio sample that went on to
	// the ring, for the recorder's audio-died watchdog.
	lastSample atomic.Int64

	epoch    uint64
	clock    arrivalClock
	havePrev bool
	prevExt  int64
}

func newAudioReader(pipe *clipPipeline) *audioReader {
	return &audioReader{
		pipe: pipe,
		out:  queueSender{pipe: pipe},
		sb:   samplebuilder.New(64, &codecs.OpusPacket{}, audioClockRate, samplebuilder.WithMaxTimeDelay(500*time.Millisecond)),
	}
}

func (r *audioReader) push(pkt *rtp.Packet, arrival int64) {
	r.arrivals.note(pkt.Timestamp, arrival)
	r.sb.Push(pkt)
	for sample := r.sb.Pop(); sample != nil; sample = r.sb.Pop() {
		at, ok := r.arrivals.take(sample.PacketTimestamp)
		if !ok {
			at = arrival
		}
		r.onSample(sample.PacketTimestamp, sample.Data, at)
	}
}

func (r *audioReader) onSample(ts uint32, data []byte, arrival int64) {
	if len(data) == 0 {
		return
	}
	if r.pipe.epoch.Load() != r.epoch {
		r.epoch = r.pipe.epoch.Load()
		r.clock = arrivalClock{rate: audioClockRate}
		r.havePrev = false
	}

	ext := r.clock.unwrap(ts)
	if r.havePrev {
		leapt := r.clock.wall(ext)-arrival > int64(excessLimit) ||
			r.prevExt-ext > nsToTicks(int64(excessLimit), audioClockRate)
		switch {
		case leapt:
			ext = r.clock.rebase(ts, arrival, r.prevExt+opusFrameTicks)
		case ext <= r.prevExt:
			return
		}
	}
	r.clock.anchor(ext, arrival)
	r.havePrev, r.prevExt = true, ext
	r.lastSample.Store(arrival)

	op := ringOp{
		track:   ringTrackAudio,
		epoch:   r.epoch,
		wall:    r.clock.wall(ext),
		offset:  r.clock.offsetNs,
		ticks:   ext,
		payload: data,
	}
	if r.out.send(op) == queueOverflowed {
		r.pipe.cut(r.epoch, ringOp{reason: cutDiskTooSlow, keep: true})
	}
}
