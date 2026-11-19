package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/pmp4"
	"github.com/stretchr/testify/require"
)

// Export and trim tests play the fixture on a loop through the readers, with
// every sample arriving when its timestamp says, so a frame's wall time is
// start + its media time. At 15 fps a frame is 66.7 ms (6000 ticks), and a
// keyframe comes every 2 s.

type mediaFixtureStream struct {
	t     *testing.T
	tp    *mediaTestPipe
	fix   *mediaFixture
	start int64

	audioShift time.Duration // audio media time runs this far behind video's
	audioFrom  time.Duration // no audio before this media time
	audioUntil time.Duration // nor from this one on, when set
	gapFrom    time.Duration // no audio in [gapFrom, gapTo)
	gapTo      time.Duration
	stallFrom  time.Duration // no video in [stallFrom, stallTo): the publisher stalled
	stallTo    time.Duration

	// A burst at the start: samples of this track before burstUntil are held
	// up on the way and all arrive at burstUntil.
	burstTrack ringTrack
	burstUntil time.Duration

	nextVideo, nextAudio int
}

func (s *mediaFixtureStream) arrival(track ringTrack, media int64) int64 {
	if track == s.burstTrack && media < int64(s.burstUntil) {
		return s.start + int64(s.burstUntil)
	}
	return s.start + media
}

func newMediaFixtureStream(t *testing.T, tp *mediaTestPipe) *mediaFixtureStream {
	return &mediaFixtureStream{t: t, tp: tp, fix: loadMediaFixture(t), start: mediaTestStart, burstTrack: ringTrackVideo}
}

func (s *mediaFixtureStream) videoTicks(i int) int64 {
	return int64(i/len(s.fix.video))*mediaFixtureLoopTicks + s.fix.video[i%len(s.fix.video)].ticks
}

func (s *mediaFixtureStream) videoTime(i int) int64 {
	return ticksToNs(s.videoTicks(i), videoClockRate)
}

func (s *mediaFixtureStream) audioTime(k int) int64 {
	return ticksToNs(int64(k)*opusFrameTicks, audioClockRate) + int64(s.audioShift)
}

// play sends every sample whose media time is before until, in order.
func (s *mediaFixtureStream) play(until time.Duration) {
	s.t.Helper()
	for {
		videoAt, audioAt := s.videoTime(s.nextVideo), s.audioTime(s.nextAudio)
		if min(videoAt, audioAt) >= int64(until) {
			return
		}
		if videoAt <= audioAt {
			i := s.nextVideo
			s.nextVideo++
			if videoAt >= int64(s.stallFrom) && videoAt < int64(s.stallTo) {
				continue
			}
			ts := 0x80000000 + uint32(s.videoTicks(i))
			require.NoError(s.t, s.tp.frame(ts, s.fix.frameAnnexB(s.t, i%len(s.fix.video)), s.arrival(ringTrackVideo, videoAt)))
			continue
		}
		k := s.nextAudio
		s.nextAudio++
		if audioAt < int64(s.audioFrom) || s.audioUntil > 0 && audioAt >= int64(s.audioUntil) ||
			audioAt >= int64(s.gapFrom) && audioAt < int64(s.gapTo) {
			continue
		}
		s.tp.packet(0x1234+uint32(k*opusFrameTicks), s.fix.audio[k%len(s.fix.audio)].data, s.arrival(ringTrackAudio, audioAt))
	}
}

func (s *mediaFixtureStream) at(d time.Duration) time.Time {
	return time.Unix(0, s.start+int64(d))
}

func (s *mediaFixtureStream) ms(d time.Duration) int64 {
	return (s.start + int64(d)) / int64(time.Millisecond)
}

// mediaMP4 is an exported MP4 read back with pmp4.
type mediaMP4 struct {
	video, audio *pmp4.Track
}

func openMediaMP4(t *testing.T, path string) *mediaMP4 {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })

	var pres pmp4.Presentation
	require.NoError(t, pres.Unmarshal(f))
	draft := &mediaMP4{}
	for _, track := range pres.Tracks {
		require.Zero(t, track.TimeOffset)
		if track.Codec.IsVideo() {
			draft.video = track
		} else {
			draft.audio = track
		}
	}
	require.NotNil(t, draft.video)

	// pmp4 writes an edit list for every track; it must be the identity
	// edit, one entry from media time 0, so no player shifts anything.
	edits := mediaEditLists(t, path)
	require.Len(t, edits, len(pres.Tracks))
	for i, entries := range edits {
		require.Len(t, entries, 1, "track %d", i)
		require.Zero(t, entries[0], "track %d starts at media time 0", i)
	}
	return draft
}

// mediaEditLists reads each track's elst media times, walking moov > trak >
// edts > elst by hand.
func mediaEditLists(t *testing.T, path string) [][]int64 {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var lists [][]int64
	var walk func(boxes []byte, inside string)
	walk = func(boxes []byte, inside string) {
		for pos := 0; pos+8 <= len(boxes); {
			size := int(binary.BigEndian.Uint32(boxes[pos:]))
			kind := string(boxes[pos+4 : pos+8])
			require.GreaterOrEqual(t, size, 8)
			require.LessOrEqual(t, pos+size, len(boxes))
			body := boxes[pos+8 : pos+size]
			switch {
			case kind == "moov" || kind == "trak" || kind == "edts":
				walk(body, kind)
			case kind == "elst" && inside == "edts":
				version, count := body[0], int(binary.BigEndian.Uint32(body[4:]))
				var times []int64
				for i, at := 0, 8; i < count; i++ {
					if version == 1 {
						times = append(times, int64(binary.BigEndian.Uint64(body[at+8:])))
						at += 20
					} else {
						times = append(times, int64(int32(binary.BigEndian.Uint32(body[at+4:]))))
						at += 12
					}
				}
				lists = append(lists, times)
			}
			pos += size
		}
	}
	walk(data, "")
	return lists
}

func mediaTrackTicks(track *pmp4.Track) int64 {
	var total int64
	for _, sample := range track.Samples {
		total += int64(sample.Duration)
	}
	return total
}

func mediaTrackMs(track *pmp4.Track) int64 {
	return ticksToMs(mediaTrackTicks(track), track.TimeScale)
}

func mediaPayload(t *testing.T, sample *pmp4.Sample) []byte {
	t.Helper()
	payload, err := sample.GetPayload()
	require.NoError(t, err)
	return bytes.Clone(payload)
}

// mediaTopLevelBoxes lists an MP4's boxes in file order.
func mediaTopLevelBoxes(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var boxes []string
	for pos := 0; pos+8 <= len(data); {
		size := int(binary.BigEndian.Uint32(data[pos:]))
		boxes = append(boxes, string(data[pos+4:pos+8]))
		require.GreaterOrEqual(t, size, 8)
		pos += size
	}
	return boxes
}

func mediaMkdir(t *testing.T, parts ...string) string {
	t.Helper()
	dir := filepath.Join(append([]string{t.TempDir()}, parts...)...)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	return dir
}

func mediaDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestDraftStartsOnFirstKeyframeInsideTheWindow(t *testing.T) {
	tp := newMediaTestPipe(t, 30*time.Second)
	stream := newMediaFixtureStream(t, tp)
	stream.play(40 * time.Second)
	drafts := mediaMkdir(t, "drafts")

	// The window opens at 5.5 s; the keyframe at 4 s is outside it, so the
	// draft starts on the one at 6 s.
	result, err := exportDraft(context.Background(), tp.live(), stream.at(35500*time.Millisecond), 30*time.Second, drafts, "d1")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(drafts, "d1.mp4"), result.path)
	require.Equal(t, stream.ms(6*time.Second), result.startAt)
	require.Equal(t, int64(29500), result.durationMs, "from 6 s to the click at 35.5 s")
	require.Equal(t, int64(30000), result.requestedMs)
	require.False(t, result.partial)
	require.False(t, result.truncated)
	require.Equal(t, 320, result.width)
	require.Equal(t, 180, result.height)
	require.Equal(t, codecString(stream.fix.sps, true), result.codecs)

	draft := openMediaMP4(t, result.path)
	require.False(t, draft.video.Samples[0].IsNonSyncSample)
	require.Equal(t, result.durationMs, mediaTrackMs(draft.video))
	info, err := os.Stat(result.path)
	require.NoError(t, err)
	require.Equal(t, info.Size(), result.bytes)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestDraftIsNeverLongerThanTheClipLength(t *testing.T) {
	tp := newMediaTestPipe(t, 30*time.Second)
	stream := newMediaFixtureStream(t, tp)
	stream.play(45 * time.Second)
	drafts := mediaMkdir(t, "drafts")

	// 32 s puts the window's start exactly on the keyframe at 2 s.
	for i, at := range []time.Duration{32 * time.Second, 32010 * time.Millisecond, 33999 * time.Millisecond, 39970 * time.Millisecond, 44 * time.Second} {
		result, err := exportDraft(context.Background(), tp.live(), stream.at(at), 30*time.Second, drafts, fmt.Sprintf("d%d", i))
		require.NoError(t, err)
		require.LessOrEqual(t, result.durationMs, int64(30000), "at %s", at)
		require.LessOrEqual(t, result.startAt+result.durationMs, stream.ms(at), "it ends at the click, at %s", at)
		require.LessOrEqual(t, mediaTrackMs(openMediaMP4(t, result.path).video), int64(30000))
	}
}

func TestDraftAudioIsAlignedWithin10ms(t *testing.T) {
	for _, shift := range []time.Duration{0, 7 * time.Millisecond, 13 * time.Millisecond, -9 * time.Millisecond} {
		t.Run(fmt.Sprintf("audio %s behind", shift), func(t *testing.T) {
			tp := newMediaTestPipe(t, 30*time.Second)
			stream := newMediaFixtureStream(t, tp)
			stream.audioShift = shift
			stream.play(40 * time.Second)
			at := stream.at(37123 * time.Millisecond)

			snap, err := tp.live().snapshotRange(at, 30*time.Second)
			require.NoError(t, err)
			skew := time.Duration(snap.audio[0].wall - snap.video[0].wall)
			snap.release()
			require.LessOrEqual(t, skew.Abs(), audioAlignSlack)

			result, err := exportDraft(context.Background(), tp.live(), at, 30*time.Second, mediaMkdir(t, "drafts"), "d1")
			require.NoError(t, err)
			draft := openMediaMP4(t, result.path)
			require.NotEqual(t, opusSilence, mediaPayload(t, draft.audio.Samples[0]), "no silence needed in front")
			require.InDelta(t, mediaTrackMs(draft.video), mediaTrackMs(draft.audio), 20)
		})
	}

	t.Run("audio that starts late is preceded by silence", func(t *testing.T) {
		// The first packets of the epoch went missing: sound starts 65 ms
		// after the first frame. Three silence packets put it 5 ms early.
		tp := newMediaTestPipe(t, 30*time.Second)
		stream := newMediaFixtureStream(t, tp)
		stream.audioShift = 5 * time.Millisecond
		stream.audioFrom = 50 * time.Millisecond
		stream.play(20 * time.Second)

		at := stream.at(15 * time.Second)
		snap, err := tp.live().snapshotRange(at, 30*time.Second)
		require.NoError(t, err)
		late := time.Duration(snap.audio[0].wall - snap.video[0].wall)
		snap.release()
		require.Equal(t, 65*time.Millisecond, late)

		result, err := exportDraft(context.Background(), tp.live(), at, 30*time.Second, mediaMkdir(t, "drafts"), "d1")
		require.NoError(t, err)
		require.True(t, result.partial)
		draft := openMediaMP4(t, result.path)
		silent := 0
		for silent < len(draft.audio.Samples) && bytes.Equal(opusSilence, mediaPayload(t, draft.audio.Samples[silent])) {
			silent++
		}
		require.Equal(t, 3, silent)
		require.Equal(t, stream.fix.audio[3].data, mediaPayload(t, draft.audio.Samples[3]))
		var lead int64
		for _, sample := range draft.audio.Samples[:silent] {
			lead += int64(sample.Duration)
		}
		skew := late - time.Duration(ticksToNs(lead, audioClockRate))
		require.LessOrEqual(t, skew.Abs(), audioAlignSlack, "the first real packet plays where it was recorded")
	})
}

func TestDraftIsMoovFirstAndRoundTrips(t *testing.T) {
	tp := newMediaTestPipe(t, 30*time.Second)
	stream := newMediaFixtureStream(t, tp)
	stream.play(10500 * time.Millisecond)

	result, err := exportDraft(context.Background(), tp.live(), stream.at(10*time.Second), 30*time.Second, mediaMkdir(t, "drafts"), "d1")
	require.NoError(t, err)
	require.Equal(t, []string{"ftyp", "moov", "mdat"}, mediaTopLevelBoxes(t, result.path))
	require.Equal(t, int64(10000), result.durationMs)
	require.True(t, result.partial)

	// Every frame and packet of the fixture, byte for byte, in order.
	draft := openMediaMP4(t, result.path)
	require.Len(t, draft.video.Samples, len(stream.fix.video))
	for i, sample := range draft.video.Samples {
		require.Equal(t, stream.fix.video[i].avcc, mediaPayload(t, sample), "frame %d", i)
		require.Equal(t, !stream.fix.video[i].key, sample.IsNonSyncSample, "frame %d", i)
		require.Equal(t, uint32(6000), sample.Duration)
	}
	require.Len(t, draft.audio.Samples, len(stream.fix.audio))
	for k, sample := range draft.audio.Samples {
		require.Equal(t, stream.fix.audio[k].data, mediaPayload(t, sample), "packet %d", k)
		require.Equal(t, uint32(opusFrameTicks), sample.Duration)
	}
	require.Equal(t, uint32(videoClockRate), draft.video.TimeScale)
	require.Equal(t, uint32(audioClockRate), draft.audio.TimeScale)
}

func TestDraftPartialFlag(t *testing.T) {
	tp := newMediaTestPipe(t, 30*time.Second)
	stream := newMediaFixtureStream(t, tp)
	stream.play(20500 * time.Millisecond)
	drafts := mediaMkdir(t, "drafts")

	young, err := exportDraft(context.Background(), tp.live(), stream.at(20*time.Second), 30*time.Second, drafts, "young")
	require.NoError(t, err)
	require.True(t, young.partial, "the stream began 20 s ago, inside the 30 s window")
	require.False(t, young.truncated)
	require.Equal(t, int64(20000), young.durationMs)
	require.Equal(t, stream.ms(0), young.startAt)

	stream.play(40500 * time.Millisecond)
	full, err := exportDraft(context.Background(), tp.live(), stream.at(40*time.Second), 30*time.Second, drafts, "full")
	require.NoError(t, err)
	require.False(t, full.partial)
	require.Equal(t, int64(30000), full.durationMs)
}

func TestWarmingUpUnderFiveSeconds(t *testing.T) {
	tp := newMediaTestPipe(t, 30*time.Second)
	drafts := mediaMkdir(t, "drafts")
	stream := newMediaFixtureStream(t, tp)

	_, err := exportDraft(context.Background(), tp.live(), stream.at(time.Second), 30*time.Second, drafts, "none")
	require.ErrorIs(t, err, errWarmingUp, "nothing recorded yet")

	stream.play(4500 * time.Millisecond)
	_, err = exportDraft(context.Background(), tp.live(), stream.at(4500*time.Millisecond), 30*time.Second, drafts, "early")
	require.ErrorIs(t, err, errWarmingUp)

	stream.play(6 * time.Second)
	result, err := exportDraft(context.Background(), tp.live(), stream.at(6*time.Second), 30*time.Second, drafts, "ready")
	require.NoError(t, err)
	require.GreaterOrEqual(t, result.durationMs, int64(clipMinMS))
	require.Equal(t, []string{"ready.mp4"}, mediaDirNames(t, drafts), "a refusal writes nothing")
}

// mediaWaitPinned returns once an export has taken its range of r. It spins
// rather than sleeps; the deadline is only there to fail instead of hang.
func mediaWaitPinned(t *testing.T, r *ring) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		r.mu.Lock()
		pinned := false
		for _, seg := range r.segs {
			pinned = pinned || seg.pins > 0
		}
		r.mu.Unlock()
		if pinned {
			return
		}
		require.True(t, time.Now().Before(deadline), "the export never took its range")
		runtime.Gosched()
	}
}

func TestClickTimeIsFixedBeforeWaitingForASlot(t *testing.T) {
	tp := newMediaTestPipe(t, 30*time.Second)
	stream := newMediaFixtureStream(t, tp)
	stream.play(40 * time.Second)
	drafts := mediaMkdir(t, "drafts")

	// Both export slots are busy.
	releaseFirst, err := acquireExportSlot(context.Background())
	require.NoError(t, err)
	releaseSecond, err := acquireExportSlot(context.Background())
	require.NoError(t, err)
	defer releaseSecond()

	type exported struct {
		result exportResult
		err    error
	}
	done := make(chan exported, 1)
	live := tp.live()
	go func() {
		result, err := exportDraft(context.Background(), live, stream.at(39*time.Second), 30*time.Second, drafts, "d1")
		done <- exported{result, err}
	}()
	mediaWaitPinned(t, live)

	// Twenty seconds go by before a slot frees.
	stream.play(60 * time.Second)
	releaseFirst()

	var got exported
	select {
	case got = <-done:
	case <-time.After(time.Minute):
		t.Fatal("the export never finished")
	}
	require.NoError(t, got.err)
	require.Equal(t, stream.ms(10*time.Second), got.result.startAt, "the window of the click, not of the slot")
	require.Equal(t, stream.ms(39*time.Second), got.result.startAt+got.result.durationMs)

	// The range it read was pinned all along: every frame is the fixture's.
	draft := openMediaMP4(t, got.result.path)
	for i, sample := range draft.video.Samples {
		require.Equal(t, stream.fix.video[(150+i)%150].avcc, mediaPayload(t, sample), "frame %d", i)
	}
}

func TestDraftBusyWhenNoSlotFrees(t *testing.T) {
	tp := newMediaTestPipe(t, 30*time.Second)
	stream := newMediaFixtureStream(t, tp)
	stream.play(20 * time.Second)
	drafts := mediaMkdir(t, "drafts")

	for range exportSlots {
		release, err := acquireExportSlot(context.Background())
		require.NoError(t, err)
		defer release()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := exportDraft(ctx, tp.live(), stream.at(20*time.Second), 30*time.Second, drafts, "d1")
	require.ErrorIs(t, err, errBusy)
	require.Empty(t, mediaDirNames(t, drafts))

	r := tp.live()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, seg := range r.segs {
		require.Zero(t, seg.pins, "a refused export lets go of its range")
	}
}

func TestAudioGapsAreFilledWithSilence(t *testing.T) {
	// The sound drops out for 3 s while the picture goes on.
	tp := newMediaTestPipe(t, 30*time.Second)
	stream := newMediaFixtureStream(t, tp)
	stream.gapFrom, stream.gapTo = 8*time.Second, 11*time.Second
	stream.play(20500 * time.Millisecond)

	result, err := exportDraft(context.Background(), tp.live(), stream.at(20*time.Second), 30*time.Second, mediaMkdir(t, "drafts"), "d1")
	require.NoError(t, err)
	draft := openMediaMP4(t, result.path)

	// The audio is as long as the span it covers: 0 s to the end of the
	// packet at 19.98 s.
	require.Equal(t, int64(20*audioClockRate), mediaTrackTicks(draft.audio))
	silent := 0
	for _, sample := range draft.audio.Samples {
		payload := mediaPayload(t, sample)
		if bytes.Equal(payload, opusSilence) {
			silent++
			require.Equal(t, uint32(opusFrameTicks), sample.Duration)
			continue
		}
		require.LessOrEqual(t, sample.Duration, uint32(opusFrameTicks), "no packet stretched over the gap")
	}
	require.Equal(t, 150, silent, "3 s of 20 ms packets")
	require.Len(t, draft.audio.Samples, 1000)
	require.Equal(t, mediaTrackMs(draft.video), mediaTrackMs(draft.audio))
}

func TestTicksToMsIsSharedAt30And2997fps(t *testing.T) {
	for _, frameTicks := range []uint32{3000, 3003} {
		t.Run(fmt.Sprint(frameTicks), func(t *testing.T) {
			var samples []*pmp4.Sample
			for i := range 1800 {
				samples = append(samples, &pmp4.Sample{Duration: frameTicks, IsNonSyncSample: i%60 != 0})
			}
			var cum int64
			for k, sample := range samples {
				if !sample.IsNonSyncSample && k > 0 && cum < 50*videoClockRate {
					inMs := ticksToMs(cum, videoClockRate)
					plan, err := planTrim(samples, videoClockRate, inMs, inMs+6000)
					require.NoError(t, err)
					require.Equal(t, k, plan.kIn, "at %d ms", inMs)
					require.Zero(t, plan.leadMs)
				}
				cum += int64(sample.Duration)
			}

			// The same through a real draft: the keyframe times a draft
			// reports are where a trim lands with no lead.
			tp := newMediaTestPipe(t, 30*time.Second)
			feed := newMediaOpsFeeder(t, tp, 30, 60, 300)
			feed.ticksPerFrame = int64(frameTicks)
			feed.until(25 * time.Second)
			result, err := exportDraft(context.Background(), tp.live(), feed.at(25*time.Second), 30*time.Second, mediaMkdir(t, "drafts"), "d1")
			require.NoError(t, err)
			draft := openMediaMP4(t, result.path)
			require.Equal(t, result.durationMs, mediaTrackMs(draft.video))

			clips := mediaMkdir(t, "clips")
			cum = 0
			for k, sample := range draft.video.Samples {
				if !sample.IsNonSyncSample && k > 0 && ticksToMs(cum, videoClockRate)+clipMinMS <= result.durationMs {
					inMs := ticksToMs(cum, videoClockRate)
					trimmed, err := trimClip(context.Background(), result.path, clips, fmt.Sprintf("c%d", k), inMs, result.durationMs)
					require.NoError(t, err)
					require.Zero(t, trimmed.leadMs, "at %d ms", inMs)
					require.Equal(t, result.durationMs-inMs, trimmed.durationMs)
					require.Equal(t, trimmed.durationMs, mediaTrackMs(openMediaMP4(t, trimmed.path).video))
				}
				cum += int64(sample.Duration)
			}
		})
	}
}

// makeMediaDraft is a 30 s draft of the fixture on a loop: keyframes every 2 s,
// frames 66.7 ms, packets 20 ms, all starting at 0.
func makeMediaDraft(t *testing.T) (exportResult, string) {
	t.Helper()
	tp := newMediaTestPipe(t, 30*time.Second)
	stream := newMediaFixtureStream(t, tp)
	stream.play(31 * time.Second)
	result, err := exportDraft(context.Background(), tp.live(), stream.at(30*time.Second), 30*time.Second, mediaMkdir(t, "drafts"), "d1")
	require.NoError(t, err)
	require.Equal(t, int64(30000), result.durationMs)
	return result, mediaMkdir(t, "clips")
}

func TestTrimKeepsTheExactInAsLead(t *testing.T) {
	draft, clips := makeMediaDraft(t)

	result, err := trimClip(context.Background(), draft.path, clips, "c1", 5300, 15000)
	require.NoError(t, err)
	require.False(t, result.rename)
	require.Equal(t, filepath.Join(clips, "c1.mp4"), result.path)
	require.Equal(t, int64(5300), result.inMs)
	require.Equal(t, int64(1300), result.leadMs, "the file starts on the keyframe at 4 s")
	require.Equal(t, int64(9700), result.durationMs, "out minus in, the lead left out")

	clip := openMediaMP4(t, result.path)
	require.False(t, clip.video.Samples[0].IsNonSyncSample)
	require.Equal(t, result.leadMs+result.durationMs, mediaTrackMs(clip.video))
	require.InDelta(t, mediaTrackMs(clip.video), mediaTrackMs(clip.audio), 20)
	fix := loadMediaFixture(t)
	require.Equal(t, fix.video[60].avcc, mediaPayload(t, clip.video.Samples[0]), "frame 60 is the keyframe at 4 s")
	require.Equal(t, fix.audio[200].data, mediaPayload(t, clip.audio.Samples[0]), "and packet 200 the sound at 4 s")
	require.Equal(t, []string{"ftyp", "moov", "mdat"}, mediaTopLevelBoxes(t, result.path))
	info, err := os.Stat(result.path)
	require.NoError(t, err)
	require.Equal(t, info.Size(), result.bytes)
	require.Equal(t, []string{"c1.mp4"}, mediaDirNames(t, clips))
}

func TestTrimOutIsFrameExact(t *testing.T) {
	draft, clips := makeMediaDraft(t)

	// 12.03 s is inside the frame that starts at 12 s; the clip ends with
	// that frame, at 12.066 s.
	inside, err := trimClip(context.Background(), draft.path, clips, "inside", 2000, 12030)
	require.NoError(t, err)
	require.Equal(t, int64(10066), inside.durationMs)
	clip := openMediaMP4(t, inside.path)
	require.Len(t, clip.video.Samples, 151)
	require.Equal(t, int64(10066), mediaTrackMs(clip.video))

	// On a frame boundary it ends exactly there.
	boundary, err := trimClip(context.Background(), draft.path, clips, "boundary", 2000, 12000)
	require.NoError(t, err)
	require.Equal(t, int64(10000), boundary.durationMs)
	require.Len(t, openMediaMP4(t, boundary.path).video.Samples, 150)
}

func TestTrimRefusesUnderFiveSeconds(t *testing.T) {
	draft, clips := makeMediaDraft(t)

	_, err := trimClip(context.Background(), draft.path, clips, "short", 1000, 5900)
	require.ErrorIs(t, err, errTooShort, "4.93 s from the in-point")
	enough, err := trimClip(context.Background(), draft.path, clips, "enough", 1000, 6000)
	require.NoError(t, err)
	require.Equal(t, int64(clipMinMS), enough.durationMs)

	for _, bad := range [][2]int64{{-1, 10000}, {8000, 8000}, {9000, 8000}, {0, 30051}} {
		_, err := trimClip(context.Background(), draft.path, clips, "bad", bad[0], bad[1])
		require.ErrorIs(t, err, errBadRange, "%v", bad)
	}
	require.Equal(t, []string{"enough.mp4"}, mediaDirNames(t, clips))
}

func TestFullRangePublishRenamesWithoutRemux(t *testing.T) {
	draft, clips := makeMediaDraft(t)

	for _, keep := range []struct{ inMs, outMs, leadMs int64 }{{0, 30000, 0}, {1500, 30000, 1500}, {0, 30050, 0}} {
		result, err := trimClip(context.Background(), draft.path, clips, "c1", keep.inMs, keep.outMs)
		require.NoError(t, err)
		require.True(t, result.rename)
		require.Empty(t, result.path)
		require.Equal(t, keep.leadMs, result.leadMs)
		require.Equal(t, 30000-keep.inMs, result.durationMs)
		require.Equal(t, draft.bytes, result.bytes)
	}
	require.Empty(t, mediaDirNames(t, clips), "nothing is written; the caller renames the draft")
}

func TestFailedTrimLeavesNoTempFile(t *testing.T) {
	draft, clips := makeMediaDraft(t)

	t.Run("stopped mid-write", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := trimClip(ctx, draft.path, clips, "c1", 2000, 20000)
		require.ErrorIs(t, err, context.Canceled)
		require.Empty(t, mediaDirNames(t, clips))
	})

	t.Run("draft cut short", func(t *testing.T) {
		data, err := os.ReadFile(draft.path)
		require.NoError(t, err)
		// Half the media is gone: the frames from about 15 s on, which the
		// trim needs.
		short := filepath.Join(t.TempDir(), "short.mp4")
		require.NoError(t, os.WriteFile(short, data[:len(data)/2], 0o600))
		_, err = trimClip(context.Background(), short, clips, "c2", 2000, 20000)
		require.Error(t, err)
		require.Empty(t, mediaDirNames(t, clips))
	})

	t.Run("nowhere to write", func(t *testing.T) {
		_, err := trimClip(context.Background(), draft.path, filepath.Join(clips, "missing"), "c3", 2000, 20000)
		require.Error(t, err)
		require.Empty(t, mediaDirNames(t, clips))
	})

	t.Run("export stopped mid-write", func(t *testing.T) {
		tp := newMediaTestPipe(t, 30*time.Second)
		stream := newMediaFixtureStream(t, tp)
		stream.play(10 * time.Second)
		drafts := mediaMkdir(t, "drafts")

		// A context that ends as soon as the export has its slot.
		ctx, cancel := context.WithCancel(context.Background())
		snap, err := tp.live().snapshotRange(stream.at(10*time.Second), 30*time.Second)
		require.NoError(t, err)
		pres := snap.presentation(ctx)
		cancel()
		_, _, err = writeMP4(drafts, "d1", pres)
		snap.release()
		require.ErrorIs(t, err, context.Canceled)
		require.Empty(t, mediaDirNames(t, drafts))
	})
}

func TestDraftOfAnEpochsFirstFramesStaysInSyncWhileItsClockSettles(t *testing.T) {
	// One track's first 200 ms arrive in a burst; every sample after that is
	// on time. Its clock starts on the late first sample and settles 200 ms
	// earlier; the samples already in the ring must move with it, or a draft
	// that starts on the epoch's first frame is 200 ms out of sync.
	for _, track := range []ringTrack{ringTrackVideo, ringTrackAudio} {
		t.Run(map[ringTrack]string{ringTrackVideo: "video late", ringTrackAudio: "audio late"}[track], func(t *testing.T) {
			tp := newMediaTestPipe(t, 30*time.Second)
			stream := newMediaFixtureStream(t, tp)
			stream.burstTrack, stream.burstUntil = track, 200*time.Millisecond
			stream.play(20500 * time.Millisecond)
			at := stream.at(20 * time.Second)

			require.Equal(t, stream.start, tp.live().status().epochStart)
			snap, err := tp.live().snapshotRange(at, 30*time.Second)
			require.NoError(t, err)
			require.Equal(t, stream.start, snap.video[0].wall)
			require.Equal(t, stream.start, snap.audio[0].wall)
			snap.release()

			result, err := exportDraft(context.Background(), tp.live(), at, 30*time.Second, mediaMkdir(t, "drafts"), "d1")
			require.NoError(t, err)
			require.Equal(t, stream.ms(0), result.startAt)
			require.Equal(t, int64(20000), result.durationMs)
			draft := openMediaMP4(t, result.path)
			require.Equal(t, stream.fix.audio[0].data, mediaPayload(t, draft.audio.Samples[0]), "the sound of the first frame, no silence and none skipped")
			require.Equal(t, stream.fix.video[0].avcc, mediaPayload(t, draft.video.Samples[0]))
			require.InDelta(t, mediaTrackMs(draft.video), mediaTrackMs(draft.audio), 20)
		})
	}
}

func TestDraftClickedWhileWaitingForTheIDRAfterAStallEndsAtTheClick(t *testing.T) {
	// The publisher stalls from 12.3 s to 15.3 s and comes back mid-GOP: the
	// frame at 12.267 s stays on screen until the IDR at 16 s, and the sound
	// is back from 15.3 s. A click at 15.9 s gets all of that.
	tp := newMediaTestPipe(t, 30*time.Second)
	stream := newMediaFixtureStream(t, tp)
	stream.stallFrom, stream.stallTo = 12300*time.Millisecond, 15300*time.Millisecond
	stream.gapFrom, stream.gapTo = stream.stallFrom, stream.stallTo
	stream.play(15900 * time.Millisecond)

	result, err := exportDraft(context.Background(), tp.live(), stream.at(15900*time.Millisecond), 30*time.Second, mediaMkdir(t, "drafts"), "d1")
	require.NoError(t, err)
	require.Equal(t, int64(15900), result.durationMs)
	draft := openMediaMP4(t, result.path)
	require.Equal(t, int64(15900), mediaTrackMs(draft.video))
	frozen := draft.video.Samples[len(draft.video.Samples)-1]
	require.Equal(t, stream.fix.video[184%150].avcc, mediaPayload(t, frozen), "the frame before the stall")
	require.Equal(t, uint32(nsToTicksUp(int64(15900*time.Millisecond)-stream.videoTime(184), videoClockRate)), frozen.Duration)

	require.InDelta(t, mediaTrackMs(draft.video), mediaTrackMs(draft.audio), 20)
	last := draft.audio.Samples[len(draft.audio.Samples)-1]
	require.NotEqual(t, opusSilence, mediaPayload(t, last), "the sound that came back is in it")

	// Trimming that draft inside the frozen frame ends there too, and is a
	// new file, not the draft renamed.
	clips := mediaMkdir(t, "clips")
	trimmed, err := trimClip(context.Background(), result.path, clips, "c1", 0, 14000)
	require.NoError(t, err)
	require.False(t, trimmed.rename)
	require.Equal(t, int64(14000), trimmed.durationMs)
	require.Equal(t, int64(14000), mediaTrackMs(openMediaMP4(t, trimmed.path).video))
}

func TestTrimOutInsideAFreezeEndsAtTheOutPoint(t *testing.T) {
	tp := newMediaTestPipe(t, 30*time.Second)
	stream := newMediaFixtureStream(t, tp)
	stream.stallFrom, stream.stallTo = 12300*time.Millisecond, 15300*time.Millisecond
	stream.gapFrom, stream.gapTo = stream.stallFrom, stream.stallTo
	stream.play(20500 * time.Millisecond)
	draft, err := exportDraft(context.Background(), tp.live(), stream.at(20*time.Second), 30*time.Second, mediaMkdir(t, "drafts"), "d1")
	require.NoError(t, err)
	clips := mediaMkdir(t, "clips")

	// 13 s is inside the frame shown from 12.267 s to 16 s.
	result, err := trimClip(context.Background(), draft.path, clips, "c1", 1000, 13000)
	require.NoError(t, err)
	require.Equal(t, int64(1000), result.leadMs)
	require.Equal(t, int64(12000), result.durationMs, "out minus in, not to the end of the freeze")
	clip := openMediaMP4(t, result.path)
	require.Equal(t, result.leadMs+result.durationMs, mediaTrackMs(clip.video))
	require.LessOrEqual(t, mediaTrackMs(clip.audio), mediaTrackMs(clip.video)+20)

	// Past the freeze, an ordinary frame is still kept whole.
	whole, err := trimClip(context.Background(), draft.path, clips, "c2", 1000, 17030)
	require.NoError(t, err)
	require.Equal(t, int64(17066-1000), whole.durationMs)
}

func TestDraftAudioEndsWithTheVideoAfterAGap(t *testing.T) {
	t.Run("click just after the sound came back", func(t *testing.T) {
		// The packet before the gap steps 3 s to the next. The last packet
		// of the draft must not take that step as its length.
		tp := newMediaTestPipe(t, 30*time.Second)
		stream := newMediaFixtureStream(t, tp)
		stream.gapFrom, stream.gapTo = 17*time.Second, 20*time.Second
		stream.play(20500 * time.Millisecond)

		for _, click := range []time.Duration{20010 * time.Millisecond, 20040 * time.Millisecond} {
			result, err := exportDraft(context.Background(), tp.live(), stream.at(click), 30*time.Second, mediaMkdir(t, "drafts"), "d1")
			require.NoError(t, err)
			draft := openMediaMP4(t, result.path)
			require.LessOrEqual(t, mediaTrackMs(draft.audio), mediaTrackMs(draft.video)+20, "at %s", click)
			require.InDelta(t, result.durationMs, mediaTrackMs(draft.audio), 20, "at %s", click)
		}
	})

	t.Run("a tail whose sound stopped after a gap", func(t *testing.T) {
		// One packet comes after a 3 s gap, then no more sound; the stream
		// ends a second later. Frozen, that packet keeps its own 20 ms.
		tp := newMediaTestPipe(t, 30*time.Second)
		stream := newMediaFixtureStream(t, tp)
		stream.gapFrom, stream.gapTo = 10*time.Second, 13*time.Second
		stream.audioUntil = 13020 * time.Millisecond
		stream.play(14 * time.Second)

		tail := tp.writer.finish(context.Background(), true)
		require.NotNil(t, tail)
		result, err := exportDraft(context.Background(), tail, tail.endTime(), 30*time.Second, mediaMkdir(t, "drafts"), "d1")
		require.NoError(t, err)
		draft := openMediaMP4(t, result.path)
		require.Equal(t, int64(14000), mediaTrackMs(draft.video))
		require.Equal(t, int64(13020), mediaTrackMs(draft.audio))
	})
}

func TestTrimTakesTheSoundJustBeforeItsKeyframe(t *testing.T) {
	// At 29.97 fps the keyframe at frame 60 is at 2002 ms, between two 20 ms
	// packets. The clip's sound starts with the packet at 2000 ms, 2 ms early,
	// rather than the one at 2020 ms.
	tp := newMediaTestPipe(t, 30*time.Second)
	feed := newMediaOpsFeeder(t, tp, 30, 60, 300)
	feed.ticksPerFrame = 3003
	feed.audio = true
	feed.until(25 * time.Second)
	draft, err := exportDraft(context.Background(), tp.live(), feed.at(25*time.Second), 30*time.Second, mediaMkdir(t, "drafts"), "d1")
	require.NoError(t, err)

	result, err := trimClip(context.Background(), draft.path, mediaMkdir(t, "clips"), "c1", 2002, 12000)
	require.NoError(t, err)
	require.Zero(t, result.leadMs)
	clip := openMediaMP4(t, result.path)
	require.Equal(t, feed.packetPayload(100), mediaPayload(t, clip.audio.Samples[0]))
}

func TestCodecStringIsTheSPSProfileAndLevel(t *testing.T) {
	fix := loadMediaFixture(t)
	require.Equal(t, "avc1.640015,opus", codecString(fix.sps, true))
	require.Equal(t, "avc1.640015", codecString(fix.sps, false))
	require.Equal(t, "avc1.4d401f", codecString([]byte{0x67, 0x4D, 0x40, 0x1F, 0x00}, false), "lowercase hex")
	require.Empty(t, codecString([]byte{0x67, 0x64}, true))
}

func TestAudioSilenceFillFollowsTheSpec(t *testing.T) {
	read := func(seq, off, size uint32) func() ([]byte, error) {
		return func() ([]byte, error) { return []byte{0xFC, 0xFF, 0xFE}, nil }
	}
	// n = (dur - own) / 960 silence packets once dur is more than half a
	// packet over the packet's own 960; the packet keeps the remainder.
	for _, c := range []struct {
		dur    uint32
		first  uint32
		filled int
	}{
		{960, 960, 0},
		{1440, 1440, 0},
		{1441, 1441, 0},
		{1900, 1900, 0},
		{1920, 960, 1},
		{2400, 1440, 1},
		{144960, 960, 150},
	} {
		out := audioWithSilence([]ringSample{{wall: 1, dur: c.dur, pcm: 960, size: 3}}, 1, read)
		require.Equal(t, c.first, out[0].Duration, "dur %d", c.dur)
		require.Len(t, out, 1+c.filled, "dur %d", c.dur)
		var total uint32
		for i, sample := range out {
			total += sample.Duration
			if i > 0 {
				require.Equal(t, uint32(opusFrameTicks), sample.Duration)
				payload, err := sample.GetPayload()
				require.NoError(t, err)
				require.Equal(t, opusSilence, payload)
			}
		}
		require.Equal(t, c.dur, total, "the timeline keeps its length")
	}

	// Sound that starts after the first frame gets whole silence packets in
	// front, to the nearest: none under 10 ms.
	for _, c := range []struct {
		late    time.Duration
		leading int
	}{
		{9900 * time.Microsecond, 0},
		{10 * time.Millisecond, 1},
		{29 * time.Millisecond, 1},
		{30 * time.Millisecond, 2},
		{65 * time.Millisecond, 3},
	} {
		out := audioWithSilence([]ringSample{{wall: int64(c.late), dur: 960, pcm: 960, size: 3}}, 0, read)
		require.Len(t, out, c.leading+1, "%s late", c.late)
	}
}
