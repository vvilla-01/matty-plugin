package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/opus"
)

// The disk ring: the last ringWindow() of one live stream, kept as raw sample
// payloads in segment files with the index in memory, so that a click can be
// cut into an MP4 without re-encoding anything (clips_mux.go).
//
// One ring is one EPOCH: a stretch of the stream with one clock, one set of
// parameter sets and one picture size, that starts on an IDR. A clip never
// spans two. When an epoch ends early (the readers cut it, §2.4.4) the writer
// freezes its ring, read-only, and hands it to the recorder manager as a tail
// that can still be clipped for ten minutes; the stream carries on in a new
// ring in a new directory.
//
// Segments are what eviction works in. A segment starts on a keyframe once the
// one before it is ringSegmentTarget old, so dropping the oldest segment never
// leaves a draft starting on a frame that cannot be decoded. A segment is kept
// while any part of it could still start a draft (its successor starts inside
// the window), and the ring as a whole is held to a byte cap sized from the
// stream's measured bitrate. Losing minutes to that cap is reported as
// truncation, never as the stream being young.
//
// Nothing that touches the disk runs under the ring's lock. The writer writes
// each payload before it takes the lock to publish it, and segments are
// closed and deleted after the lock is let go, so a stuck disk stalls the
// writer alone: never an export, a status read or a wipe. Exports never hold
// the lock while they read either: snapshotRange copies the slice of the index
// a draft needs and PINS its segments, and eviction and deletion leave a
// pinned segment alone until the export releases it.
//
// Nor can a panic leave the lock held. A panic over a sample is recovered and
// the ring started again (applySafely), and that restart, a wipe, a cut and
// finish all take the lock to remove the old ring; a lock still held would
// stop the stream's recording for good, without a word. So every span under
// the lock is let go by a defer: plan, snapshotRange and the short reads
// (status and the like) directly, and commit, freeze, remove and release
// through update or locked, which do the disk work after (ringCleanup).

// segmentFile is the part of an *os.File a segment uses. Every write names its
// offset, so nothing depends on a file position. Tests put a slow or
// bottomless file behind it.
type segmentFile interface {
	io.WriterAt
	io.ReaderAt
	io.Closer
}

func openSegmentFile(path string) (segmentFile, error) {
	return os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
}

type ringTrack uint8

const (
	ringTrackVideo ringTrack = iota
	ringTrackAudio
)

// ringSample is one frame or one Opus packet in the index: 40 bytes, about
// 80 of them a second.
type ringSample struct {
	wall  int64  // unix ns: arrivalClock mapping of this sample's RTP time
	ticks int64  // unwrapped RTP ts relative to the epoch base, track clock (90000 / 48000)
	dur   uint32 // track ticks, = next.ticks - ticks
	seg   uint32 // segment seq
	off   uint32 // payload offset in segment file
	size  uint32
	key   bool   // video only: contains NAL type 5
	pcm   uint16 // audio only: the packet's own length in 48 kHz ticks, 0 if unreadable
}

type ringSegment struct {
	seq   uint32
	f     segmentFile // O_RDWR|O_CREATE|O_TRUNC, 0600; payloads only, no headers
	path  string
	start int64 // wall ns of its first (key) video sample
	bytes int64 // payload bytes published into it
	pins  int
}

// pendingSample is the newest sample of a track. Its payload is already on
// disk, but its duration is known only when the next sample arrives, so it
// stays out of the index until then.
type pendingSample struct {
	ringSample
}

// heldAudio is audio that came before the epoch's first IDR, kept in memory
// until that IDR opens the first segment.
type heldAudio struct {
	wall, ticks int64
	payload     []byte
}

// ringSettings are what the recorder may change while a ring runs: the window
// (Clip length) and Broadcast Box's reported bitrate, used to size a ring
// before it has measured ten seconds of its own.
type ringSettings struct {
	window   atomic.Int64 // ns
	reported atomic.Int64 // bytes per second
}

type ring struct {
	mu sync.Mutex

	// Set when the ring is made and never changed: the writer reads them
	// without the lock.
	dir    string // <root>/ring/<handle>/<epoch>
	handle string
	epoch  uint64
	open   func(path string) (segmentFile, error)

	epochStart int64 // wall ns of the epoch's first IDR
	sps, pps   []byte
	width      int
	height     int

	video, audio []ringSample
	pending      [2]*pendingSample // on disk; duration known only when the next sample arrives
	frameDur     uint32            // the latest video duration that was not a freeze
	offset       [2]int64          // the clock offset each track's walls are mapped with
	anchored     [2]bool
	preroll      []heldAudio // audio that came before the epoch's first IDR
	segs         []*ringSegment
	nextSeq      uint32
	bytes        int64

	window         time.Duration // ringWindow(), refreshed on each rotation
	capBytes       int64
	measuredBps    float64 // the writer's 10 s average, bytes per second
	meter          bitrateMeter
	truncatedUntil int64 // wall ns; see evict
	truncation     string
	now            int64 // the newest video wall time appended
	end            int64 // a tail: the wall time at the end of its last video sample

	frozen   bool // a tail: no appends, no eviction
	hasAudio bool
	deleted  bool

	settings *ringSettings

	// testFault, when a test sets it under mu, is called part way through
	// commit's and freeze's changes with "commit" or "freeze", for the test
	// to panic there as a bug would. It is nil outside tests.
	testFault func(span string)
}

// ringStatus is what the recorder manager and the admin view read of a ring.
type ringStatus struct {
	epoch          uint64
	epochStart     int64 // wall ns; 0 until the first IDR
	heldMs         int64
	bytes          int64
	capBytes       int64
	measuredBps    float64
	truncatedUntil int64
	truncation     string // the admin reason while truncatedUntil is ahead
	width, height  int
	codecs         string
	frozen         bool
	end            int64
}

func (r *ring) status() ringStatus {
	r.mu.Lock()
	defer r.mu.Unlock()

	return ringStatus{
		epoch:          r.epoch,
		epochStart:     r.epochStart,
		heldMs:         r.heldMsLocked(),
		bytes:          r.bytes,
		capBytes:       r.capBytes,
		measuredBps:    r.measuredBps,
		truncatedUntil: r.truncatedUntil,
		truncation:     r.truncation,
		width:          r.width,
		height:         r.height,
		codecs:         codecString(r.sps, r.hasAudio),
		frozen:         r.frozen,
		end:            r.end,
	}
}

// size is the ring's bytes on disk, for the manager's ringBudgetBytes.
func (r *ring) size() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bytes
}

// endTime is when a tail's last frame ends; a draft from a tail is cut from
// [end - L, end].
func (r *ring) endTime() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return time.Unix(0, r.end)
}

// heldMs is how much video the ring holds.
func (r *ring) heldMs() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.heldMsLocked()
}

func (r *ring) heldMsLocked() int64 {
	if len(r.video) == 0 {
		return 0
	}
	first, last := r.video[0], r.video[len(r.video)-1]
	return ticksToMs(last.ticks+int64(last.dur)-first.ticks, videoClockRate)
}

func (r *ring) isDeleted() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.deleted
}

// codecString is the draft's and the clip's codecs field.
func codecString(sps []byte, hasAudio bool) string {
	if len(sps) < 4 {
		return ""
	}
	codecs := fmt.Sprintf("avc1.%02x%02x%02x", sps[1], sps[2], sps[3])
	if hasAudio {
		codecs += ",opus"
	}
	return codecs
}

// ringCap is the most a ring may hold: the window plus a segment and a GOP of
// margin, at the stream's bitrate with headroom, never below 12 Mbit/s and
// never above ringMaxBytes.
func ringCap(window time.Duration, bytesPerSecond float64) int64 {
	rate := max(float64(ringFloorBytesPerSecond), ringBitrateHeadroom*bytesPerSecond)
	return min(int64(ringMaxBytes), int64((window+ringSlack).Seconds()*rate))
}

// bitrateMeter adds up the bytes appended over the last bitrateWindow of wall
// time, a second at a time.
type bitrateMeter struct {
	seconds []meterSecond
	total   int64
}

type meterSecond struct {
	second int64
	bytes  int64
}

func (m *bitrateMeter) add(wall int64, size int64) {
	second := wall / int64(time.Second)
	if n := len(m.seconds); n > 0 && m.seconds[n-1].second >= second {
		m.seconds[n-1].bytes += size
	} else {
		m.seconds = append(m.seconds, meterSecond{second: second, bytes: size})
	}
	m.total += size

	horizon := second - int64(bitrateWindow/time.Second)
	drop := 0
	for drop < len(m.seconds) && m.seconds[drop].second <= horizon {
		m.total -= m.seconds[drop].bytes
		drop++
	}
	if drop > 0 {
		m.seconds = append(m.seconds[:0], m.seconds[drop:]...)
	}
}

// perSecond divides by the span the kept seconds actually cover, up to now,
// so the newest second, still filling, does not read as a slower stream.
func (m *bitrateMeter) perSecond(now int64) float64 {
	if len(m.seconds) == 0 {
		return 0
	}
	span := max(now-m.seconds[0].second*int64(time.Second), int64(time.Second))
	return float64(m.total) / (float64(span) / float64(time.Second))
}

// rateLocked is the bitrate the cap is sized for: the ring's own once it has
// measured a full window, Broadcast Box's report before that.
func (r *ring) rateLocked() float64 {
	if r.epochStart != 0 && r.now-r.epochStart >= int64(bitrateWindow) {
		r.measuredBps = r.meter.perSecond(r.now)
		return r.measuredBps
	}
	return float64(r.settings.reported.Load())
}

// opusPCM is an Opus packet's own length in 48 kHz ticks, 0 if it cannot be
// read.
func opusPCM(payload []byte) uint16 {
	return uint16(min(max(opus.PacketDuration2(payload), 0), 1<<16-1))
}

// ownAudioTicks is how long an audio sample plays by itself, for a last packet
// whose step to the next one is unknown or spans a gap.
func ownAudioTicks(sample ringSample) uint32 {
	if sample.pcm > 0 {
		return uint32(sample.pcm)
	}
	return opusFrameTicks
}

// errRingGone means the ring was deleted while the writer was appending to
// it: the live ring was wiped.
var errRingGone = errors.New("the ring was deleted")

// ringAppend is what one append did, for the writer's events.
type ringAppend struct {
	started   bool  // this sample is the epoch's first IDR
	startedAt int64 // its wall time
	tooRare   bool  // the newest segment alone has outgrown the cap
	truncated bool  // this append began a stretch of truncation
}

// ringWrite is one append in flight: plan decides it under the ring's lock,
// put writes it to disk without the lock, and commit publishes it.
type ringWrite struct {
	op     ringOp
	sample ringSample   // where its payload goes; its duration is not known yet
	seg    *ringSegment // the segment it goes in: the newest, or newSeg
	newSeg *ringSegment // a segment put opens for it, when it starts one
	mkdir  bool
	first  bool // the epoch's first IDR
	width  int
	height int
	held   []heldAudio  // with the first IDR: the audio held back for it
	heldAt []ringSample // where put wrote that audio
	err    error
}

// add appends one sample. Only the writer goroutine calls it, so nothing else
// ever appends to a segment or changes what plan read; what can happen while
// put writes without the lock is a wipe (remove) or a finish that gave up
// waiting (freeze), and commit checks for both.
func (r *ring) add(op ringOp) (ringAppend, error) {
	w, err := r.plan(op)
	if w == nil || err != nil {
		return ringAppend{}, err
	}
	w.put(r)
	return r.commit(w)
}

func (r *ring) plan(op ringOp) (*ringWrite, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.deleted {
		return nil, errRingGone
	}
	if r.frozen {
		return nil, nil
	}
	r.reanchorLocked(op.track, op.offset)

	w := &ringWrite{op: op, sample: ringSample{wall: op.wall, ticks: op.ticks, size: uint32(len(op.payload))}}
	if op.track == ringTrackAudio {
		if r.epochStart == 0 {
			// There is no segment before the first IDR. The newest
			// audioPrerollSamples are held for it, so that a draft starting on
			// that IDR has sound from its first frame.
			if len(r.preroll) >= audioPrerollSamples {
				r.preroll = append(r.preroll[:0], r.preroll[1:]...)
			}
			r.preroll = append(r.preroll, heldAudio{wall: op.wall, ticks: op.ticks, payload: op.payload})
			return nil, nil
		}
		if prev := r.pending[ringTrackAudio]; prev != nil {
			if op.ticks <= prev.ticks {
				return nil, nil
			}
			w.sample.wall = max(op.wall, prev.wall+1)
		}
		w.sample.pcm = opusPCM(op.payload)
		w.appendTo(r.segs[len(r.segs)-1])
		return w, nil
	}

	if r.epochStart == 0 {
		if !op.key || len(op.sps) == 0 || len(op.pps) == 0 {
			return nil, nil
		}
		width, height, ok := spsSize(op.sps)
		if !ok {
			return nil, nil
		}
		w.first, w.width, w.height = true, width, height
		w.sample.key = true
		w.held, r.preroll = r.preroll, nil
		w.mkdir = len(r.segs) == 0
		w.startSegment(r)
		return w, nil
	}

	w.sample.key = op.key
	if prev := r.pending[ringTrackVideo]; prev != nil {
		if op.ticks <= prev.ticks {
			return nil, nil
		}
		w.sample.wall = max(op.wall, prev.wall+1)
	}
	if last := r.segs[len(r.segs)-1]; op.key && w.sample.wall-last.start >= int64(ringSegmentTarget) {
		w.startSegment(r)
	} else {
		w.appendTo(last)
	}
	return w, nil
}

func (w *ringWrite) appendTo(seg *ringSegment) {
	w.seg = seg
	w.sample.seg, w.sample.off = seg.seq, uint32(seg.bytes)
}

// startSegment gives the sample a new segment of its own, which put opens.
// Its number is taken now, so a write that is discarded never reuses one.
func (w *ringWrite) startSegment(r *ring) {
	seq := r.nextSeq
	r.nextSeq++
	w.newSeg = &ringSegment{
		seq:   seq,
		path:  filepath.Join(r.dir, strconv.FormatUint(uint64(seq), 10)+".seg"),
		start: w.sample.wall,
	}
	w.seg = w.newSeg
	w.sample.seg, w.sample.off = seq, 0
}

// put writes the sample, and any audio held for it, without the ring's lock.
// It touches only the segment the writer alone appends to, past the bytes
// already published.
func (w *ringWrite) put(r *ring) {
	if w.newSeg != nil {
		if w.mkdir {
			if err := os.MkdirAll(r.dir, 0o750); err != nil {
				w.err = fmt.Errorf("making %s: %w", r.dir, err)
				return
			}
		}
		f, err := r.open(w.newSeg.path)
		if err != nil {
			w.err = fmt.Errorf("opening %s: %w", w.newSeg.path, err)
			return
		}
		w.newSeg.f = f
	}

	off := int64(w.sample.off)
	if w.err = writeSegment(w.seg, w.op.payload, off); w.err != nil {
		return
	}
	off += int64(len(w.op.payload))
	for _, held := range w.held {
		if w.err = writeSegment(w.seg, held.payload, off); w.err != nil {
			return
		}
		w.heldAt = append(w.heldAt, ringSample{
			wall:  held.wall,
			ticks: held.ticks,
			seg:   w.seg.seq,
			off:   uint32(off),
			size:  uint32(len(held.payload)),
			pcm:   opusPCM(held.payload),
		})
		off += int64(len(held.payload))
	}
}

func writeSegment(seg *ringSegment, payload []byte, off int64) error {
	n, err := seg.f.WriteAt(payload, off)
	if err == nil && n != len(payload) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return fmt.Errorf("writing to %s: %w", seg.path, err)
	}
	return nil
}

func (r *ring) commit(w *ringWrite) (ringAppend, error) {
	var result ringAppend
	var err error
	var after ringCleanup
	r.update(func() { result, after, err = r.commitLocked(w) })
	after.run(r)
	return result, err
}

// commitLocked is commit under the ring's lock. What it leaves on the disk to
// close or delete is returned for after the lock.
func (r *ring) commitLocked(w *ringWrite) (ringAppend, ringCleanup, error) {
	if r.deleted || r.frozen || w.err != nil {
		// Wiped, or frozen by a finish that stopped waiting, while the disk
		// was written; or the write failed. A segment opened for this sample
		// is in no index, so it goes now.
		gone, err := r.deleted, w.err
		if r.frozen && !gone {
			err = nil // finish has taken the ring as it stood
		}
		var after ringCleanup
		if w.newSeg != nil && w.newSeg.f != nil {
			after.segs = append(after.segs, w.newSeg)
		}
		after.emptyDir = gone && len(r.segs) == 0
		if gone {
			return ringAppend{}, after, errRingGone
		}
		return ringAppend{}, after, err
	}

	var result ringAppend
	if w.newSeg != nil {
		r.segs = append(r.segs, w.newSeg)
	}
	r.publishLocked(w.seg, w.sample)
	r.faultLocked("commit")

	if w.op.track == ringTrackAudio {
		r.appendAudioLocked(w.sample)
	} else {
		if w.first {
			r.epochStart = w.sample.wall
			r.sps, r.pps, r.width, r.height = w.op.sps, w.op.pps, w.width, w.height
			result.started, result.startedAt = true, w.sample.wall
		} else if prev := r.pending[ringTrackVideo]; prev != nil {
			r.indexVideoLocked(prev, uint32(w.sample.ticks-prev.ticks))
		}
		r.pending[ringTrackVideo] = &pendingSample{w.sample}
		r.now = max(r.now, w.sample.wall)

		for _, held := range w.heldAt {
			r.publishLocked(w.seg, held)
			r.appendAudioLocked(held)
		}
		if w.newSeg != nil {
			// A rotation sizes the ring again: the window may have changed,
			// and so may the bitrate.
			r.window = time.Duration(r.settings.window.Load())
			r.capBytes = ringCap(r.window, r.rateLocked())
		}
	}

	var after ringCleanup
	result.tooRare, result.truncated, after.segs = r.evictLocked()
	return result, after, nil
}

// publishLocked counts a written payload into its segment and the ring.
func (r *ring) publishLocked(seg *ringSegment, sample ringSample) {
	size := int64(sample.size)
	seg.bytes += size
	r.bytes += size
	r.meter.add(sample.wall, size)
}

// indexVideoLocked puts the pending frame into the index now that its
// duration is known. A duration over gapMin is a freeze, not a frame length,
// and is never taken as the length of a last frame.
func (r *ring) indexVideoLocked(prev *pendingSample, dur uint32) {
	entry := prev.ringSample
	entry.dur = dur
	r.video = append(r.video, entry)
	if int64(dur) <= nsToTicks(int64(gapMin), videoClockRate) {
		r.frameDur = dur
	}
}

// appendAudioLocked makes sample the pending audio, indexing the one before
// it. Audio before the epoch's start is kept: snapshotRange picks a draft's
// sound by wall time, and the epoch's start may still move earlier while its
// clock settles (reanchorLocked).
func (r *ring) appendAudioLocked(sample ringSample) {
	if prev := r.pending[ringTrackAudio]; prev != nil {
		if sample.ticks <= prev.ticks {
			return
		}
		sample.wall = max(sample.wall, prev.wall+1)
		entry := prev.ringSample
		entry.dur = uint32(sample.ticks - prev.ticks)
		r.audio = append(r.audio, entry)
	}
	r.pending[ringTrackAudio] = &pendingSample{sample}
	r.hasAudio = true
}

// reanchorLocked moves a track's walls earlier when its reader's clock has
// found a less-delayed arrival. The first frames of an epoch were mapped
// before the clock settled, so without this a draft that starts on them would
// carry the difference as A/V skew for its whole length. It happens only in
// the clock's first arrivalAnchorWindow, so it touches at most that much of
// the index.
func (r *ring) reanchorLocked(track ringTrack, offset int64) {
	if !r.anchored[track] {
		r.offset[track], r.anchored[track] = offset, true
		return
	}
	shift := r.offset[track] - offset
	if shift <= 0 {
		return
	}
	r.offset[track] = offset

	if pending := r.pending[track]; pending != nil {
		pending.wall -= shift
	}
	if track == ringTrackAudio {
		shiftWalls(r.audio, shift)
		for i := range r.preroll {
			r.preroll[i].wall -= shift
		}
		return
	}
	shiftWalls(r.video, shift)
	for _, seg := range r.segs {
		seg.start -= shift
	}
	if r.epochStart != 0 {
		r.epochStart -= shift
	}
	if r.now != 0 {
		r.now -= shift
	}
}

func shiftWalls(samples []ringSample, shift int64) {
	for i := range samples {
		samples[i].wall -= shift
	}
}

// evictLocked drops whole segments from the front: those that can no longer
// start a draft, then those over the byte cap. It never drops a pinned
// segment, nor anything from a tail. It reports whether the newest segment
// alone is over the cap (keyframes too rare), whether this call began a
// stretch of truncation, and the segments the caller must close once it has
// let go of the lock.
//
// The keyframe test sizes the cap from the bitrate measured so far as well as
// the one it was set with: a first segment is sized before anything has been
// measured, and a fast stream with long GOPs, whose bitrate Broadcast Box did
// not report, would otherwise be wiped as "too rare" before the cap could
// ever catch up with it.
func (r *ring) evictLocked() (tooRare, truncated bool, dropped []*ringSegment) {
	if r.frozen || r.deleted || len(r.segs) == 0 {
		return false, false, nil
	}

	cutoff := r.now - int64(r.window)
	for len(r.segs) >= 2 && r.segs[0].pins == 0 && r.segs[1].start <= cutoff {
		dropped = append(dropped, r.dropFirstLocked())
	}
	for r.bytes > r.capBytes && len(r.segs) >= 2 && r.segs[0].pins == 0 {
		if r.lastWallLocked(r.segs[0]) > cutoff {
			if r.truncatedUntil <= r.now {
				truncated = true
			}
			r.truncatedUntil = r.now + int64(r.window)
			r.truncation = truncationReason(r.capBytes, r.window)
		}
		dropped = append(dropped, r.dropFirstLocked())
	}
	limit := max(r.capBytes, ringCap(r.window, r.meter.perSecond(r.now)))
	return r.segs[len(r.segs)-1].bytes > limit, truncated, dropped
}

// lastWallLocked is the wall time of the newest indexed sample in seg. The
// index is in segment order, so it is the one before the first sample of a
// later segment.
func (r *ring) lastWallLocked(seg *ringSegment) int64 {
	last := seg.start
	for _, samples := range [][]ringSample{r.video, r.audio} {
		i := sort.Search(len(samples), func(i int) bool { return samples[i].seg > seg.seq })
		if i > 0 && samples[i-1].seg == seg.seq {
			last = max(last, samples[i-1].wall)
		}
	}
	return last
}

// truncationReason is the admin's explanation of clips coming out short: the
// bitrate above which the cap cannot hold the window and its margin. At the
// 1 GiB cap and 5:00 that is about 26 Mbit/s.
func truncationReason(capBytes int64, window time.Duration) string {
	mbits := float64(capBytes) * 8 / (window + ringSlack).Seconds() / 1e6
	seconds := int(window / time.Second)
	return fmt.Sprintf("bitrate above %d Mbit/s (clips shorter than %d:%02d)", int(mbits), seconds/60, seconds%60)
}

// dropFirstLocked takes the oldest segment out of the ring and its samples out
// of the index. A pending audio packet written into it goes with it; the
// pending frame is always in the newest segment, which is never dropped.
func (r *ring) dropFirstLocked() *ringSegment {
	seg := r.segs[0]
	r.video = dropSegmentPrefix(r.video, seg.seq)
	r.audio = dropSegmentPrefix(r.audio, seg.seq)
	for track, pending := range r.pending {
		if pending != nil && pending.seg == seg.seq {
			r.pending[track] = nil
		}
	}
	r.segs = r.segs[1:]
	r.bytes -= seg.bytes
	return seg
}

func dropSegmentPrefix(samples []ringSample, seq uint32) []ringSample {
	n := 0
	for n < len(samples) && samples[n].seg == seq {
		n++
	}
	return samples[n:]
}

// closeSegments closes and deletes segments the index no longer points at.
// Callers run it after letting go of the ring's lock, because an unlink can
// wait on a stuck disk.
func closeSegments(segs []*ringSegment) {
	for _, seg := range segs {
		if seg.f != nil {
			_ = seg.f.Close()
		}
		_ = os.Remove(seg.path)
	}
}

// ringCleanup is the disk work a change under the ring's lock leaves for
// after it: segments the index no longer points at, and the ring's directory
// once nothing is left in it.
type ringCleanup struct {
	segs     []*ringSegment
	emptyDir bool
}

func (c ringCleanup) run(r *ring) {
	closeSegments(c.segs)
	if c.emptyDir {
		_ = os.Remove(r.dir)
	}
}

// locked runs f under the ring's lock and lets go of it however f ends, a
// panic included, then does the disk work f returns.
func (r *ring) locked(f func() ringCleanup) {
	var after ringCleanup
	func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		after = f()
	}()
	after.run(r)
}

// update runs f under the ring's lock for one of the writer's changes to a
// ring that is not a tail yet (commit, freeze), and lets go of the lock
// however f ends. A panic part way through can leave the index half changed:
// a frame both indexed and pending, say. So the ring drops its index before
// the lock goes, and reads as empty to every export (errWarmingUp) until it
// is removed, which is what comes next: the writer starting again
// (applySafely) or finish wiping it. Snapshots already taken keep their
// copies and their pins. A tail is never changed here, so a tail keeps its
// index. The panic itself goes on, with its stack, to whoever recovers it.
func (r *ring) update(f func()) {
	r.mu.Lock()
	finished := false
	defer func() {
		if !finished && !r.frozen {
			r.video, r.audio, r.preroll = nil, nil, nil
			r.pending = [2]*pendingSample{}
		}
		r.mu.Unlock()
	}()
	f()
	finished = true
}

// faultLocked calls the test's fault, if there is one (testFault).
func (r *ring) faultLocked(span string) {
	if r.testFault != nil {
		r.testFault(span)
	}
}

// freeze ends the epoch and makes the ring a read-only tail. The pending
// samples join the index: the frame with the latest frame length (never a
// freeze's), the audio packet with its own length (never a gap's). If the cut
// names a video sample (hasUpTo), everything after it goes, and the audio
// with it: those are the late stretch of a timestamp rewrite, which belongs
// to the publisher that reconnected.
func (r *ring) freeze(upTo int64, hasUpTo bool) {
	var after ringCleanup
	r.update(func() { after = r.freezeLocked(upTo, hasUpTo) })
	after.run(r)
}

func (r *ring) freezeLocked(upTo int64, hasUpTo bool) ringCleanup {
	if r.frozen || r.deleted {
		return ringCleanup{}
	}

	r.preroll = nil
	if pending := r.pending[ringTrackVideo]; pending != nil {
		dur := r.frameDur
		if dur == 0 {
			dur = defaultVideoTicks
		}
		r.indexVideoLocked(pending, dur)
	}
	if pending := r.pending[ringTrackAudio]; pending != nil {
		entry := pending.ringSample
		entry.dur = ownAudioTicks(entry)
		r.audio = append(r.audio, entry)
	}
	r.pending = [2]*pendingSample{}
	r.faultLocked("freeze")

	var dropped []*ringSegment
	if hasUpTo {
		keep := sort.Search(len(r.video), func(i int) bool { return r.video[i].ticks > upTo })
		r.video = r.video[:keep]
		endWall := int64(0)
		if keep > 0 {
			last := r.video[keep-1]
			endWall = last.wall + ticksToNs(int64(last.dur), videoClockRate)
		}
		r.audio = r.audio[:sort.Search(len(r.audio), func(i int) bool { return r.audio[i].wall >= endWall })]

		// Segments that now hold nothing the index points at.
		lastSeq := int64(-1)
		if n := len(r.video); n > 0 {
			lastSeq = int64(r.video[n-1].seg)
		}
		if n := len(r.audio); n > 0 {
			lastSeq = max(lastSeq, int64(r.audio[n-1].seg))
		}
		for len(r.segs) > 0 && int64(r.segs[len(r.segs)-1].seq) > lastSeq && r.segs[len(r.segs)-1].pins == 0 {
			seg := r.segs[len(r.segs)-1]
			r.segs = r.segs[:len(r.segs)-1]
			r.bytes -= seg.bytes
			dropped = append(dropped, seg)
		}
	}

	if n := len(r.video); n > 0 {
		last := r.video[n-1]
		r.end = last.wall + ticksToNs(int64(last.dur), videoClockRate)
	}
	r.frozen = true
	return ringCleanup{segs: dropped}
}

// remove deletes the ring or tail: every segment that no export is reading at
// once, the rest when their pins are released. It is for tails and for the
// rings of stopped recorders; a running recorder's live ring is wiped through
// clipPipeline.wipe, which also starts the next epoch.
func (r *ring) remove() {
	r.locked(func() ringCleanup {
		if r.deleted {
			return ringCleanup{}
		}
		r.deleted = true
		r.video, r.audio, r.preroll = nil, nil, nil
		r.pending = [2]*pendingSample{}
		dropped := r.dropUnpinnedLocked()
		return ringCleanup{segs: dropped, emptyDir: len(r.segs) == 0}
	})
}

func (r *ring) dropUnpinnedLocked() []*ringSegment {
	var dropped []*ringSegment
	pinned := r.segs[:0]
	for _, seg := range r.segs {
		if seg.pins > 0 {
			pinned = append(pinned, seg)
			continue
		}
		r.bytes -= seg.bytes
		dropped = append(dropped, seg)
	}
	r.segs = pinned
	return dropped
}

// errWarmingUp means there is not yet clipMinMS of the stream to cut a draft
// from: the epoch is young, or has only just been wiped.
var errWarmingUp = errors.New("not enough of the stream is recorded yet")

// rangeSnap is the part of a ring a draft is cut from: copies of the index and
// the stream's parameters, and pins on the segments the samples live in. The
// caller must release it.
type rangeSnap struct {
	r          *ring
	handle     string
	epoch      uint64
	epochStart int64
	sps, pps   []byte
	width      int
	height     int
	codecs     string
	video      []ringSample
	audio      []ringSample
	files      map[uint32]*ringSegment
	partial    bool // the epoch began inside the window
	truncated  bool // the byte cap dropped part of the window
	released   bool
}

// snapshotRange picks the draft for a click at `at` (a tail's end, for a
// tail) with clip length L, and pins it. It holds the ring's lock only while
// it copies the index, a few milliseconds at five minutes, and never waits on
// the disk.
func (r *ring) snapshotRange(at time.Time, L time.Duration) (*rangeSnap, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.deleted || r.epochStart == 0 {
		return nil, errWarmingUp
	}
	atNs := at.UnixNano()
	from := atNs - int64(L)
	if atNs-r.epochStart < clipMinMS*int64(time.Millisecond) {
		return nil, errWarmingUp
	}

	// The first keyframe inside the window, and the last frame before the
	// click.
	first := sort.Search(len(r.video), func(i int) bool { return r.video[i].wall >= from })
	for first < len(r.video) && r.video[first].wall <= atNs && !r.video[first].key {
		first++
	}
	if first == len(r.video) || r.video[first].wall > atNs {
		return nil, errWarmingUp
	}
	last := sort.Search(len(r.video), func(i int) bool { return r.video[i].wall >= atNs }) - 1
	if last < first {
		return nil, errWarmingUp
	}
	video := make([]ringSample, 0, last-first+2)
	video = append(video, r.video[first:last+1]...)
	if pending := r.pending[ringTrackVideo]; last == len(r.video)-1 && pending != nil && pending.wall < atNs {
		// The newest frame is on disk with its length still open: the frame
		// a stalled publisher left on screen, say, while the reader waits for
		// the next IDR. It is shown until the click, so the moment clicked,
		// and the sound that has already come back, are in the draft.
		video = append(video, pending.ringSample)
	}

	// The draft ends at the click: a last frame that is shown past it (the
	// frozen frame of a stall, say) is cut short there, and never so that the
	// draft is longer than L.
	end := &video[len(video)-1]
	if reach := nsToTicksUp(atNs-end.wall, videoClockRate); end.dur == 0 || int64(end.dur) > reach {
		end.dur = uint32(reach)
	}
	span := end.ticks + int64(end.dur) - video[0].ticks
	if over := span - nsToTicks(int64(L), videoClockRate); over > 0 && int64(end.dur) > over {
		end.dur -= uint32(over)
		span -= over
	}
	if ticksToMs(span, videoClockRate) < clipMinMS {
		return nil, errWarmingUp
	}

	// Sound from audioAlignSlack before the first frame to the end of the
	// last. The last packet's step to the next one may span a gap that runs
	// past the draft, so it keeps only its own length.
	startWall := video[0].wall - int64(audioAlignSlack)
	endWall := end.wall + ticksToNs(int64(end.dur), videoClockRate)
	a0 := sort.Search(len(r.audio), func(i int) bool { return r.audio[i].wall >= startWall })
	a1 := sort.Search(len(r.audio), func(i int) bool { return r.audio[i].wall >= endWall })
	var audio []ringSample
	if a1 > a0 {
		audio = append([]ringSample(nil), r.audio[a0:a1]...)
		lastAudio := &audio[len(audio)-1]
		lastAudio.dur = min(lastAudio.dur, ownAudioTicks(*lastAudio))
	}

	snap := &rangeSnap{
		r:          r,
		handle:     r.handle,
		epoch:      r.epoch,
		epochStart: r.epochStart,
		sps:        r.sps,
		pps:        r.pps,
		width:      r.width,
		height:     r.height,
		codecs:     codecString(r.sps, len(audio) > 0),
		video:      video,
		audio:      audio,
		files:      make(map[uint32]*ringSegment),
		partial:    r.epochStart > from,
	}
	snap.truncated = !snap.partial && r.truncatedUntil > atNs

	for _, seg := range r.segs {
		if seg.seq >= video[0].seg && seg.seq <= end.seg ||
			len(audio) > 0 && seg.seq >= audio[0].seg && seg.seq <= audio[len(audio)-1].seg {
			seg.pins++
			snap.files[seg.seq] = seg
		}
	}
	return snap, nil
}

// file is the segment a sample of the snapshot lives in.
func (s *rangeSnap) file(seq uint32) segmentFile {
	return s.files[seq].f
}

// release unpins the snapshot's segments. Those of a deleted ring that no
// other export holds are deleted now. A live ring's eviction, held up by the
// pins, catches up at the writer's next append, which is also where any
// truncation it causes is reported.
func (s *rangeSnap) release() {
	r := s.r
	r.locked(func() ringCleanup {
		if s.released {
			return ringCleanup{}
		}
		s.released = true
		for _, seg := range s.files {
			seg.pins--
		}
		if !r.deleted {
			return ringCleanup{}
		}
		dropped := r.dropUnpinnedLocked()
		return ringCleanup{segs: dropped, emptyDir: len(r.segs) == 0}
	})
}

// ringOp is one instruction from a reader to the writer: a sample, a note
// that an epoch was cut, or a sync marker that tells whoever sent it the
// writer has caught up.
type ringOp struct {
	kind     ringOpKind
	track    ringTrack
	epoch    uint64 // the epoch the reader made this op in
	wall     int64
	offset   int64 // the reader's clock offset when it mapped wall (arrivalClock.offsetNs)
	ticks    int64
	key      bool
	afterGap bool // the first frame after a publisher stall; the frame before it is shown until now
	payload  []byte
	sps, pps []byte // video key samples: the epoch's parameter sets

	// Cuts, as clipPipeline.cut records them.
	next    uint64 // the epoch that starts
	reason  string
	keep    bool  // freeze the ended epoch as a tail, when it is long enough
	upTo    int64 // video ticks of the last sample to keep, with hasUpTo
	hasUpTo bool

	done chan struct{} // syncs
}

type ringOpKind uint8

const (
	ringOpSample ringOpKind = iota
	ringOpCut
	ringOpSync
)

// epochIDs numbers epochs for the whole process, so that a reconnected
// recorder's new rings never share a directory with its old tails. Along one
// pipeline every new epoch has a larger number than the one it follows.
var epochIDs atomic.Uint64

func newEpochID() uint64 {
	return epochIDs.Add(1)
}

// pipelineEvent is what the writer tells the recorder.
type pipelineEvent struct {
	kind   pipelineEventKind
	reason string    // an admin reason: why the epoch ended, or why clips are short
	at     time.Time // pipeEventReady: the epoch's start, the snapshot's clipFrom
	tail   *ring     // pipeEventCut: the frozen epoch, or nil when it was too short to keep (see pipeEventCut)
}

type pipelineEventKind uint8

const (
	// The epoch's first IDR is in the ring: state "ready". While the
	// epoch's clocks settle in its first three seconds, its start may still
	// move earlier by the first frame's extra delay, a few milliseconds.
	pipeEventReady pipelineEventKind = iota + 1
	// An epoch ended early and the stream carries on in a new one. Logged at
	// Warn, at most once a minute; a non-nil tail is kept with reason
	// "dropped". A wipe that races with the hand-over deletes that tail just
	// after; a deleted tail is gone (isDeleted, and its snapshots say
	// errWarmingUp).
	pipeEventCut
	// The epoch was wiped with no tail (keyframes too rare, the disk failed,
	// or a wipe). State "starting" until the next IDR.
	pipeEventRestarted
	// The byte cap is dropping minutes the window should hold.
	pipeEventTruncated
)

// pipelineConfig sets up one recorder's readers, queue and writer.
type pipelineConfig struct {
	dir         string // <root>/ring/<handle>
	handle      string
	window      time.Duration // ringWindow()
	reportedBps int64         // Broadcast Box's bitrate for the stream, bits per second, 0 if unknown

	// event receives the writer's events, on the writer's goroutine (or
	// finish's), never under a lock. It must not block for long, and must
	// not call finish, which waits for the writer.
	event func(pipelineEvent)

	// isH264 says whether a video payload type was negotiated as H.264;
	// nil treats every one as H.264. openSegment replaces real files in
	// tests.
	isH264      func(pt uint8) bool
	openSegment func(path string) (segmentFile, error)

	// log is where a panic in the writer is written (recovery.go); nil
	// writes it nowhere.
	log panicLog
}

// clipPipeline joins a recorder's two readers to its writer: the bounded
// queue, the bytes waiting in it, and the epoch every new sample belongs to.
//
// Epochs are decided in one place. Whoever ends one (a reader with a cut, a
// wipe, or the writer finding keyframes too rare) moves pipeline.epoch on
// under mu, and a cut is recorded in cuts rather than queued: a full queue can
// then neither lose it nor let the next epoch's samples overtake it. Every op
// carries the epoch it was made in. The writer applies an op only to the ring
// of that epoch; when it first meets a later epoch it applies the recorded
// cuts that lead there, in order. The readers notice the change on their next
// sample and start over with a fresh clock, waiting for an IDR.
type clipPipeline struct {
	queue    chan ringOp
	queued   atomic.Int64 // payload bytes in the queue
	shedding atomic.Bool  // the queue overflowed: readers drop until it is below half full
	epoch    atomic.Uint64

	mu    sync.Mutex        // held for every change of epoch
	cuts  map[uint64]ringOp // epoch -> the cut that ended it, until the writer applies it
	wipes uint64            // wipes so far; a tail handed over across one is deleted

	video  *videoReader
	audio  *audioReader
	writer *ringWriter
}

func newClipPipeline(cfg pipelineConfig) *clipPipeline {
	p := &clipPipeline{
		queue: make(chan ringOp, writerQueueSamples+writerQueueReserve),
		cuts:  make(map[uint64]ringOp),
	}
	first := newEpochID()
	p.epoch.Store(first)

	open := cfg.openSegment
	if open == nil {
		open = openSegmentFile
	}
	event := cfg.event
	if event == nil {
		event = func(pipelineEvent) {}
	}
	w := &ringWriter{pipe: p, dir: cfg.dir, handle: cfg.handle, open: open, event: event, log: cfg.log, done: make(chan struct{})}
	w.settings.window.Store(int64(cfg.window))
	w.setReportedBitrate(cfg.reportedBps)
	w.live = w.newRing(first)

	p.writer = w
	p.video = newVideoReader(p, cfg.isH264)
	p.audio = newAudioReader(p)
	return p
}

// cut ends epoch `from` with op's reason and what to keep, if nobody has yet,
// and reports whether this call did. The writer is nudged through the queue's
// reserve when there is room; when there is not, it finds the cut anyway the
// first time it meets the next epoch.
func (p *clipPipeline) cut(from uint64, op ringOp) bool {
	p.mu.Lock()
	if p.epoch.Load() != from {
		p.mu.Unlock()
		return false
	}
	next := newEpochID()
	op.kind, op.epoch, op.next, op.payload = ringOpCut, from, next, nil
	p.cuts[from] = op
	p.epoch.Store(next)
	p.mu.Unlock()

	p.nudge(from, next)
	return true
}

func (p *clipPipeline) nudge(from, next uint64) {
	select {
	case p.queue <- ringOp{kind: ringOpCut, epoch: from, next: next}:
	default:
	}
}

// wipe ends the live epoch with no tail and deletes what it holds at once, for
// a streamer's or an admin's wipe (§2.10.6 step 1). Cuts the writer has not
// applied yet lose their tails too, so nothing held before the wipe outlives
// it except a tail already handed to the recorder, which the caller deletes
// next (step 2). Recording carries on at the next IDR in a new epoch, and the
// writer reports pipeEventRestarted with restartWiped when it gets there.
func (p *clipPipeline) wipe() {
	p.mu.Lock()
	p.wipes++
	from := p.epoch.Load()
	next := newEpochID()
	for epoch, cut := range p.cuts {
		cut.keep, cut.reason = false, restartWiped
		p.cuts[epoch] = cut
	}
	p.cuts[from] = ringOp{kind: ringOpCut, epoch: from, next: next, reason: restartWiped}
	p.epoch.Store(next)
	p.mu.Unlock()

	// Now, not when the writer gets to it, which a stuck disk can put off. A
	// ring the writer has already started for the new epoch holds nothing
	// from before the wipe. A cut the writer is applying at this moment
	// sees the wipe count move and deletes its tail itself (applyCut).
	if live := p.writer.current(); live.epoch < next {
		live.remove()
	}
	p.nudge(from, next)
}

// wipedSince reports whether a wipe came after the count was gen.
func (p *clipPipeline) wipedSince(gen uint64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.wipes != gen
}

// sync waits until the writer has applied everything queued before it. For
// tests, and for a recorder that wants its ring current.
func (p *clipPipeline) sync(ctx context.Context) error {
	done := make(chan struct{})
	select {
	case p.queue <- ringOp{kind: ringOpSync, done: done}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ringWriter is the recorder's one writer goroutine. It applies ops in order,
// starts a new ring for each epoch, and reports what happened through the
// event hook.
type ringWriter struct {
	pipe     *clipPipeline
	dir      string
	handle   string
	open     func(path string) (segmentFile, error)
	event    func(pipelineEvent)
	log      panicLog
	settings ringSettings

	mu   sync.Mutex // guards live: the API reads it to cut drafts
	live *ring

	runMu     sync.Mutex
	cancel    context.CancelFunc // stops run
	started   atomic.Bool
	done      chan struct{} // closed when run returns
	abandoned atomic.Bool   // finish stopped waiting for a writer stuck on the disk
}

// current is the live ring, for exportDraft. It may change at any moment; a
// ring that was frozen or wiped meanwhile still snapshots, or says
// errWarmingUp.
func (w *ringWriter) current() *ring {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.live
}

// setWindow changes the Clip length; rings take it at their next rotation.
func (w *ringWriter) setWindow(window time.Duration) {
	w.settings.window.Store(int64(window))
}

// setReportedBitrate takes Broadcast Box's bitrate for the stream, in bits
// per second, for sizing a ring before it has measured its own.
func (w *ringWriter) setReportedBitrate(bitsPerSecond int64) {
	w.settings.reported.Store(max(bitsPerSecond, 0) / 8)
}

func (w *ringWriter) newRing(epoch uint64) *ring {
	return &ring{
		dir:      filepath.Join(w.dir, strconv.FormatUint(epoch, 10)),
		handle:   w.handle,
		epoch:    epoch,
		window:   time.Duration(w.settings.window.Load()),
		settings: &w.settings,
		open:     w.open,
	}
}

func (w *ringWriter) setLive(r *ring) {
	w.mu.Lock()
	w.live = r
	w.mu.Unlock()
}

// start runs the writer goroutine until ctx ends or finish stops it. The
// recorder calls it once, before its readers start.
func (w *ringWriter) start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	w.runMu.Lock()
	w.cancel = cancel
	w.runMu.Unlock()
	w.started.Store(true)
	go w.run(ctx)
}

func (w *ringWriter) run(ctx context.Context) {
	defer close(w.done)
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return
		case op := <-w.pipe.queue:
			w.applySafely(op)
		}
	}
}

// applySafely is apply, where a panic over a sample is taken as a write that
// failed: the ring starts again at the next keyframe, and the recorder hears
// of it as of any restart (recovery.go). The writer's locks are let go by a
// defer or held only around plain assignments. A writer finish has abandoned
// starts nothing again, as in apply: its ring has been taken as it stood, and
// a restart would remove the files of a tail the manager now holds.
func (w *ringWriter) applySafely(op ringOp) {
	if safely(w.log, "a clip ring's writer", func() { w.apply(op) }) && !w.abandoned.Load() {
		safely(w.log, "a clip ring's writer, starting again", func() {
			w.restart(restartWriteError + ": " + recorderPanicReason)
		})
	}
}

// drain applies whatever is queued now, without waiting for more. Only the
// goroutine acting as the writer may call it: tests, or finish once run has
// returned.
func (w *ringWriter) drain() {
	for {
		select {
		case op := <-w.pipe.queue:
			w.applySafely(op)
		default:
			return
		}
	}
}

func (w *ringWriter) apply(op ringOp) {
	defer w.pipe.queued.Add(-int64(len(op.payload)))

	switch op.kind {
	case ringOpSync:
		close(op.done)
		return
	case ringOpCut:
		w.catchUp(op.next)
		return
	}

	if op.epoch > w.live.epoch {
		w.catchUp(op.epoch)
	}
	r := w.live
	if op.epoch != r.epoch {
		// Made for an epoch that has already ended.
		return
	}

	result, err := r.add(op)
	if w.abandoned.Load() {
		return // finish has taken the ring as it stood; nothing here is reported
	}
	switch {
	case errors.Is(err, errRingGone):
		w.restart(restartWiped)
		return
	case err != nil:
		w.restart(restartWriteError + ": " + err.Error())
		return
	case result.tooRare:
		w.restart(restartTooRare)
		return
	}
	if result.started {
		w.emit(pipelineEvent{kind: pipeEventReady, at: time.Unix(0, result.startedAt)})
	}
	if result.truncated {
		w.emit(pipelineEvent{kind: pipeEventTruncated, reason: r.status().truncation})
	}
}

// catchUp applies the recorded cuts from the live epoch on, until the live
// epoch is target or later.
func (w *ringWriter) catchUp(target uint64) {
	for w.live.epoch < target {
		w.pipe.mu.Lock()
		cut, ok := w.pipe.cuts[w.live.epoch]
		delete(w.pipe.cuts, w.live.epoch)
		wipes := w.pipe.wipes
		w.pipe.mu.Unlock()
		if !ok {
			return
		}
		w.applyCut(cut, wipes)
	}
}

// applyCut ends the live epoch as its recorded cut says: a tail when it is
// worth keeping, otherwise wiped. wipes is the wipe count when the cut was
// read: a wipe after that reaches this tail too, whether before the recorder
// has it or just after, so nothing held before a wipe outlives it.
func (w *ringWriter) applyCut(cut ringOp, wipes uint64) {
	old := w.live
	var tail *ring
	if cut.keep {
		old.freeze(cut.upTo, cut.hasUpTo)
		if old.heldMs() >= tailMinHeld.Milliseconds() {
			tail = old
		}
	}
	w.setLive(w.newRing(cut.next))
	if tail != nil && (tail.isDeleted() || w.pipe.wipedSince(wipes)) {
		tail = nil
	}

	delivered := false
	if cut.keep {
		delivered = w.emit(pipelineEvent{kind: pipeEventCut, reason: cut.reason, tail: tail})
	} else {
		w.emit(pipelineEvent{kind: pipeEventRestarted, reason: cut.reason})
	}
	if tail == nil || !delivered || w.pipe.wipedSince(wipes) {
		old.remove()
	}
}

// restart wipes the live epoch with no tail, from the writer's side. If the
// epoch already has a cut recorded (a reader ended it at the same moment, or a
// wipe did), that cut's next epoch is the one the readers are in, and the
// restart decides what becomes of this one: nothing is kept.
func (w *ringWriter) restart(reason string) {
	old := w.live
	p := w.pipe
	p.mu.Lock()
	var next uint64
	if cut, ok := p.cuts[old.epoch]; ok {
		delete(p.cuts, old.epoch)
		next = cut.next
	} else {
		next = newEpochID()
		p.epoch.Store(next)
	}
	p.mu.Unlock()

	w.setLive(w.newRing(next))
	w.emit(pipelineEvent{kind: pipeEventRestarted, reason: reason})
	old.remove()
}

// emit hands an event to the recorder, unless finish has abandoned this
// writer; it reports whether it did.
func (w *ringWriter) emit(event pipelineEvent) bool {
	if w.abandoned.Load() {
		return false
	}
	w.event(event)
	return true
}

// finish ends the recording when the recorder stops, after its readers have
// returned. It stops the writer goroutine and waits for it for as long as ctx
// allows (§2.14: five seconds for the whole stop). Then, with keep, it applies
// what is still queued and freezes the epoch as a tail (reason "ended") if it
// holds at least tailMinHeld; otherwise, or without keep, the epoch is wiped
// and finish returns nil. Events for what the drain applies are delivered on
// the caller's goroutine.
//
// A writer still stuck on the disk when ctx ends is abandoned: what is queued
// is not applied, the epoch is frozen as it stands, and whatever the writer
// does when the disk answers is discarded, its events included.
func (w *ringWriter) finish(ctx context.Context, keep bool) *ring {
	w.runMu.Lock()
	cancel := w.cancel
	w.runMu.Unlock()
	if cancel != nil {
		cancel()
	}

	if !w.waitRun(ctx) {
		w.abandoned.Store(true)
	} else if keep {
		w.drain()
		// A cut whose nudge found the queue full is applied here, outside
		// applySafely, so a panic while it freezes is caught here too and
		// taken as a write that failed: the epoch is wiped, nothing is kept.
		if safely(w.log, "a clip ring's writer, ending", func() { w.catchUp(w.pipe.epoch.Load()) }) {
			keep = false
		}
	}

	r := w.current()
	if !keep {
		r.remove()
		return nil
	}
	// A panic while freezing is taken as a write that failed, as in
	// applySafely: the epoch is wiped, nothing is kept, and finish still
	// returns, to a recorder's stop that has nothing to recover it.
	if safely(w.log, "a clip ring's writer, ending", func() { r.freeze(0, false) }) ||
		r.heldMs() < tailMinHeld.Milliseconds() {
		r.remove()
		return nil
	}
	return r
}

// waitRun reports whether run has returned (or never started) before ctx
// ended.
func (w *ringWriter) waitRun(ctx context.Context) bool {
	if !w.started.Load() {
		return true
	}
	select {
	case <-w.done:
		return true
	default:
	}
	select {
	case <-w.done:
		return true
	case <-ctx.Done():
		return false
	}
}
