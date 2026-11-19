package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	mp4codecs "github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/pmp4"
)

// Cutting MP4s: a draft from a ring (exportDraft) and a published clip from a
// draft (trimClip). Nothing is re-encoded. Both write with mediacommon's pmp4,
// which puts the moov box before the media in one pass and reads each sample's
// bytes only as it writes them, so a five-minute clip streams from disk to disk
// through one buffer and is never whole in memory.
//
// Every track starts at zero: pmp4's TimeOffset is never set, because a
// negative one writes a broken edit list and a positive one an empty edit
// that Firefox ignores. pmp4 still writes an edts box for every track, but
// with TimeOffset 0 it is the identity edit (one entry, media_time 0, the
// track's whole length), which every player applies as no shift at all. So
// the file always starts on a keyframe, and the exact in-point the person
// chose is carried beside it as leadMs, where VillaNET's players start
// playing. Audio is lined up with the first frame by choosing its first
// packet, never by an edit.
//
// A file is written to a temp name in the same directory, opened with O_EXCL,
// and renamed into place when it is complete, so nothing ever serves half a
// file and a failure leaves nothing behind.

var (
	errBusy     = errors.New("every export slot is taken")
	errBadRange = errors.New("the trim range is not inside the draft")
	errTooShort = errors.New("the clip would be shorter than five seconds")
)

// clipExportSlots bounds exports and trims together: they are disk-bound, and
// two at once keep one person's publish from waiting behind a queue of
// others' drafts.
var clipExportSlots = make(chan struct{}, exportSlots)

// acquireExportSlot waits up to exportWait for a slot. It answers errBusy when
// none frees in time or ctx ends first. The publish route takes a slot this way
// before calling trimClip; exportDraft takes its own.
func acquireExportSlot(ctx context.Context) (release func(), err error) {
	if ctx.Err() != nil {
		return nil, errBusy
	}
	timer := time.NewTimer(exportWait)
	defer timer.Stop()

	select {
	case clipExportSlots <- struct{}{}:
		return func() { <-clipExportSlots }, nil
	case <-timer.C:
		return nil, errBusy
	case <-ctx.Done():
		return nil, errBusy
	}
}

// exportResult is what a draft is made of, for the API layer's draft record.
type exportResult struct {
	path        string // <dir>/<draftID>.mp4
	durationMs  int64  // ticksToMs of the video's total ticks
	startAt     int64  // ms since the epoch: the wall time of the first frame
	requestedMs int64  // L
	partial     bool   // the stream's epoch began inside the window
	truncated   bool   // the ring's byte cap had dropped part of the window
	bytes       int64
	width       int
	height      int
	codecs      string // "avc1.64001f,opus"
}

// exportDraft cuts the draft for a click at `at` from src: the moment of the
// click for a live ring, the tail's end (src.endTime()) for a tail. The range
// is pinned before waiting for an export slot, so a click that queues still
// gets the moment it asked for. errWarmingUp: less than clipMinMS is held.
// errBusy: no slot within exportWait, or ctx ended.
func exportDraft(ctx context.Context, src *ring, at time.Time, L time.Duration, dir, draftID string) (exportResult, error) {
	if src == nil {
		return exportResult{}, errWarmingUp
	}
	snap, err := src.snapshotRange(at, L)
	if err != nil {
		return exportResult{}, err
	}
	defer snap.release()

	release, err := acquireExportSlot(ctx)
	if err != nil {
		return exportResult{}, err
	}
	defer release()

	path, size, err := writeMP4(dir, draftID, snap.presentation(ctx))
	if err != nil {
		return exportResult{}, fmt.Errorf("writing draft %s: %w", draftID, err)
	}

	var videoTicks int64
	for _, sample := range snap.video {
		videoTicks += int64(sample.dur)
	}
	return exportResult{
		path:        path,
		durationMs:  ticksToMs(videoTicks, videoClockRate),
		startAt:     snap.video[0].wall / int64(time.Millisecond),
		requestedMs: L.Milliseconds(),
		partial:     snap.partial,
		truncated:   snap.truncated,
		bytes:       size,
		width:       snap.width,
		height:      snap.height,
		codecs:      snap.codecs,
	}, nil
}

// presentation lays the snapshot out as pmp4 tracks whose payloads are read
// from the pinned segments as they are written out. pmp4 asks for one payload
// at a time and writes it before asking for the next, so one buffer serves
// them all.
func (s *rangeSnap) presentation(ctx context.Context) *pmp4.Presentation {
	var buf []byte
	read := func(seq, off, size uint32) func() ([]byte, error) {
		f := s.file(seq)
		return func() ([]byte, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if cap(buf) < int(size) {
				buf = make([]byte, size)
			}
			payload := buf[:size]
			n, err := f.ReadAt(payload, int64(off))
			if n == len(payload) {
				return payload, nil
			}
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			return nil, fmt.Errorf("reading the recording: %w", err)
		}
	}

	video := &pmp4.Track{ID: 1, TimeScale: videoClockRate, Codec: &mp4codecs.H264{SPS: s.sps, PPS: s.pps}}
	for _, sample := range s.video {
		video.Samples = append(video.Samples, &pmp4.Sample{
			Duration:        sample.dur,
			IsNonSyncSample: !sample.key,
			PayloadSize:     sample.size,
			GetPayload:      read(sample.seg, sample.off, sample.size),
		})
	}
	pres := &pmp4.Presentation{Tracks: []*pmp4.Track{video}}

	if len(s.audio) > 0 {
		audio := &pmp4.Track{ID: 2, TimeScale: audioClockRate, Codec: &mp4codecs.Opus{ChannelCount: 2}}
		audio.Samples = audioWithSilence(s.audio, s.video[0].wall, read)
		pres.Tracks = append(pres.Tracks, audio)
	}
	return pres
}

// audioWithSilence lays out the Opus packets with every gap filled with
// silence packets, so the audio timeline keeps its true length: a packet
// whose RTP step to the next is longer than its own length, by more than half
// a packet, keeps its own length plus the remainder, and the rest becomes 20
// ms silence packets. When the first packet starts after the first frame (it
// was lost, or evicted with the segment before), silence comes first, to the
// nearest whole packet.
func audioWithSilence(audio []ringSample, videoStart int64, read func(seq, off, size uint32) func() ([]byte, error)) []*pmp4.Sample {
	frame := ticksToNs(opusFrameTicks, audioClockRate)
	out := make([]*pmp4.Sample, 0, len(audio))
	if lead := audio[0].wall - videoStart; lead >= int64(audioAlignSlack) {
		out = appendSilence(out, (lead+frame/2)/frame)
	}

	for _, sample := range audio {
		own := int64(sample.pcm)
		if own == 0 {
			own = opusFrameTicks
		}
		duration, fill := int64(sample.dur), int64(0)
		if duration > own+opusFrameTicks/2 {
			fill = (duration - own) / opusFrameTicks
			duration -= fill * opusFrameTicks
		}
		out = append(out, &pmp4.Sample{
			Duration:    uint32(duration),
			PayloadSize: sample.size,
			GetPayload:  read(sample.seg, sample.off, sample.size),
		})
		out = appendSilence(out, fill)
	}
	return out
}

func appendSilence(samples []*pmp4.Sample, n int64) []*pmp4.Sample {
	for range n {
		samples = append(samples, &pmp4.Sample{
			Duration:    opusFrameTicks,
			PayloadSize: uint32(len(opusSilence)),
			GetPayload:  func() ([]byte, error) { return opusSilence, nil },
		})
	}
	return samples
}

// trimResult is what publishing a draft makes of it.
type trimResult struct {
	rename     bool   // the range is the whole draft: the caller renames it; nothing was written
	path       string // the clip written, <clipsDir>/<id>.mp4, unless rename
	inMs       int64  // the exact in-point asked for, in the draft's time
	leadMs     int64  // how far the file starts before inMs, on the keyframe at or before it
	durationMs int64  // from inMs to the end of the last frame
	bytes      int64  // the clip's size; the draft's, with rename
}

// trimClip cuts the published clip from a draft: from the keyframe at or
// before inMs, with the difference as leadMs, to the end of the frame that
// contains outMs, which is frame-exact because B-frame streams are never
// recorded. The one frame not kept whole is a publisher stall's frozen frame:
// the clip ends at outMs inside it. When that is the whole draft it writes
// nothing and says rename.
// errBadRange: the range is not inside the draft. errTooShort: under
// clipMinMS from inMs. The caller holds an export slot.
func trimClip(ctx context.Context, draftPath, clipsDir, id string, inMs, outMs int64) (trimResult, error) {
	f, err := os.Open(draftPath)
	if err != nil {
		return trimResult{}, fmt.Errorf("opening the draft: %w", err)
	}
	// The samples read their bytes from f as the clip is written, so it stays
	// open until then.
	defer f.Close()

	var draft pmp4.Presentation
	if err := draft.Unmarshal(f); err != nil {
		return trimResult{}, fmt.Errorf("reading the draft %s: %w", filepath.Base(draftPath), err)
	}
	var video, audio *pmp4.Track
	for _, track := range draft.Tracks {
		switch {
		case track.Codec.IsVideo() && video == nil:
			video = track
		case !track.Codec.IsVideo() && audio == nil:
			audio = track
		}
	}
	if video == nil {
		return trimResult{}, fmt.Errorf("the draft %s has no video", filepath.Base(draftPath))
	}

	plan, err := planTrim(video.Samples, video.TimeScale, inMs, outMs)
	if err != nil {
		return trimResult{}, err
	}
	result := trimResult{inMs: inMs, leadMs: plan.leadMs, durationMs: plan.durationMs}

	if plan.kIn == 0 && plan.kOut == len(video.Samples)-1 && plan.lastDur == video.Samples[plan.kOut].Duration {
		info, err := f.Stat()
		if err != nil {
			return trimResult{}, fmt.Errorf("reading the draft's size: %w", err)
		}
		result.rename, result.bytes = true, info.Size()
		return result, nil
	}

	frames := append([]*pmp4.Sample(nil), video.Samples[plan.kIn:plan.kOut+1]...)
	if last := frames[len(frames)-1]; last.Duration != plan.lastDur {
		cut := *last
		cut.Duration = plan.lastDur
		frames[len(frames)-1] = &cut
	}
	clip := &pmp4.Presentation{Tracks: []*pmp4.Track{{
		ID:        video.ID,
		TimeScale: video.TimeScale,
		Codec:     video.Codec,
		Samples:   frames,
	}}}
	if audio != nil {
		if samples := audioBetween(audio.Samples, audio.TimeScale, plan.startMs-audioAlignSlack.Milliseconds(), plan.endMs); len(samples) > 0 {
			clip.Tracks = append(clip.Tracks, &pmp4.Track{
				ID:        audio.ID,
				TimeScale: audio.TimeScale,
				Codec:     audio.Codec,
				Samples:   samples,
			})
		}
	}
	for _, track := range clip.Tracks {
		for _, sample := range track.Samples {
			sample.GetPayload = stopWith(ctx, sample.GetPayload)
		}
	}

	path, size, err := writeMP4(clipsDir, id, clip)
	if err != nil {
		return trimResult{}, fmt.Errorf("writing clip %s: %w", id, err)
	}
	result.path, result.bytes = path, size
	return result, nil
}

// stopWith makes a payload read fail once ctx has ended, which abandons the
// write: the plugin is stopping.
func stopWith(ctx context.Context, read func() ([]byte, error)) func() ([]byte, error) {
	return func() ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return read()
	}
}

// trimPlan is where a trim cuts the draft's video.
type trimPlan struct {
	kIn, kOut  int
	lastDur    uint32 // frame kOut's duration in the clip
	startMs    int64  // where the clip's file starts: frame kIn
	endMs      int64  // where it ends: the end of frame kOut
	leadMs     int64
	durationMs int64
}

// planTrim works on cumulative ticks through ticksToMs, the same arithmetic
// that gave the draft its numbers, so an in-point at exactly a keyframe's
// reported millisecond lands on that keyframe with no lead.
//
// A frame longer than gapMin is not a frame of the stream's rate but the one a
// stalled publisher left on screen until the next IDR (the ring's gap freeze).
// An out-point inside it ends the clip there: keeping it whole would publish
// seconds of frozen picture and silence the person trimmed off. Exports cut a
// last frame at the click for the same reason.
func planTrim(samples []*pmp4.Sample, timescale uint32, inMs, outMs int64) (trimPlan, error) {
	cum := make([]int64, len(samples)+1)
	for i, sample := range samples {
		cum[i+1] = cum[i] + int64(sample.Duration)
	}
	startOf := func(i int) int64 { return ticksToMs(cum[i], timescale) }

	if len(samples) == 0 || inMs < 0 || inMs >= outMs || outMs > startOf(len(samples))+trimEndSlackMS {
		return trimPlan{}, errBadRange
	}

	kIn := -1
	for i := 0; i < len(samples) && startOf(i) <= inMs; i++ {
		if !samples[i].IsNonSyncSample {
			kIn = i
		}
	}
	if kIn < 0 {
		return trimPlan{}, errBadRange
	}
	kOut := kIn
	for i := kIn; i < len(samples) && startOf(i) < outMs; i++ {
		kOut = i
	}

	plan := trimPlan{
		kIn:     kIn,
		kOut:    kOut,
		lastDur: samples[kOut].Duration,
		startMs: startOf(kIn),
	}
	if dur := int64(plan.lastDur); dur > nsToTicks(int64(gapMin), int64(timescale)) {
		outTicks := (outMs*int64(timescale) + 999) / 1000
		if cut := outTicks - cum[kOut]; cut >= 1 && cut < dur {
			plan.lastDur = uint32(cut)
		}
	}
	plan.endMs = ticksToMs(cum[kOut]+int64(plan.lastDur), timescale)
	plan.leadMs = inMs - plan.startMs
	plan.durationMs = plan.endMs - inMs
	if plan.durationMs < clipMinMS {
		return trimPlan{}, errTooShort
	}
	return plan, nil
}

// audioBetween is the draft's audio that starts in [fromMs, toMs). The
// draft's gaps are already filled, so the first of them starts within half a
// packet of fromMs + audioAlignSlack.
func audioBetween(samples []*pmp4.Sample, timescale uint32, fromMs, toMs int64) []*pmp4.Sample {
	var kept []*pmp4.Sample
	var cum int64
	for _, sample := range samples {
		start := ticksToMs(cum, timescale)
		cum += int64(sample.Duration)
		if start >= toMs {
			break
		}
		if start >= fromMs {
			kept = append(kept, sample)
		}
	}
	return kept
}

// writeMP4 writes pres to <dir>/<id>.mp4 through a temp file in dir, and
// returns the path and size. On any error the temp file is removed and
// nothing is left at the final name.
func writeMP4(dir, id string, pres *pmp4.Presentation) (string, int64, error) {
	f, tmp, err := createClipTemp(dir, id, ".mp4")
	if err != nil {
		return "", 0, err
	}

	counted := &mp4CountingWriter{w: f}
	buffered := bufio.NewWriterSize(counted, exportBufferBytes)
	err = pres.Marshal(buffered)
	if err == nil {
		err = buffered.Flush()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	final := filepath.Join(dir, id+".mp4")
	if err == nil {
		err = os.Rename(tmp, final)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", 0, err
	}
	return final, counted.n, nil
}

type mp4CountingWriter struct {
	w io.Writer
	n int64
}

func (c *mp4CountingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
