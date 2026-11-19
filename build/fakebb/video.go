package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media/h264reader"
	"github.com/pion/webrtc/v4/pkg/media/ivfreader"
)

// The codecs a stream can be sent in. Each stream has one, as on a real
// Broadcast Box, where it is whatever its streamer's encoder sends. H.264 is
// what OBS sends and the only video the plugin's clip recorder records. VP8 is
// the stand-in's own: Broadcast Box v2.0.2 does not register VP8 at all
// (design/clips-research.json, from its internal/webrtc/codecs/codecs.go), so
// no real stream is ever in it, but it is what the app's playback checks have
// always used, since an emulator without a working H.264 decoder plays it.
const (
	codecVP8  = "vp8"
	codecH264 = "h264"
)

// h264Rate is the H.264 clip's frame rate. A raw H.264 file says nothing about
// time, so it is the rate the Makefile's fakebb-media makes it at, the same as
// the VP8 clip's, so that the Opus loop still fits under it.
const h264Rate = 30

// rtpPayloadMTU is the most media one RTP packet carries, as pion's own
// tracks do (1200 bytes a packet, less the 12 of the RTP header).
const rtpPayloadMTU = 1200 - 12

// frameSource is a clip's frames, one at a time, each as long as frame().
type frameSource interface {
	next() ([]byte, error)
	frame() time.Duration
	close()
}

type ivfSource struct {
	file   *os.File
	ivf    *ivfreader.IVFReader
	length time.Duration
}

func openIVF(path string) (frameSource, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	ivf, header, err := ivfreader.NewWith(file)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	length := time.Duration(float64(time.Second) * float64(header.TimebaseNumerator) / float64(header.TimebaseDenominator))
	if length <= 0 {
		length = time.Second / 30
	}

	return &ivfSource{file: file, ivf: ivf, length: length}, nil
}

func (s *ivfSource) next() ([]byte, error) {
	data, _, err := s.ivf.ParseNextFrame()
	return data, err
}

func (s *ivfSource) frame() time.Duration { return s.length }
func (s *ivfSource) close()               { _ = s.file.Close() }

// h264Frames cuts a raw (Annex B) H.264 stream into access units: one
// picture each, with the SPS and PPS that come before a keyframe in the same
// unit as it, so that a viewer joining at a keyframe can decode it. A unit
// is sent as one sample, so all of a picture's packets carry one timestamp.
type h264Frames struct {
	nals    *h264reader.H264Reader
	pending *h264reader.NAL
}

func newH264Frames(in io.Reader) (*h264Frames, error) {
	nals, err := h264reader.NewReader(in)
	if err != nil {
		return nil, err
	}

	return &h264Frames{nals: nals}, nil
}

// vcl is whether a NAL unit is part of a picture (a coded slice).
func vcl(nal *h264reader.NAL) bool {
	return nal.UnitType >= h264reader.NalUnitTypeCodedSliceNonIdr && nal.UnitType <= h264reader.NalUnitTypeCodedSliceIdr
}

// startsPicture is whether a slice is a picture's first: its first_mb_in_slice
// is 0, which in Exp-Golomb is a single 1 bit right after the NAL header.
func startsPicture(nal *h264reader.NAL) bool {
	return len(nal.Data) > 1 && nal.Data[1]&0x80 != 0
}

// next is the next access unit in Annex B (each NAL unit after a start code),
// or io.EOF after the last. A unit ends where something that can only start
// the next one begins: a NAL unit that is not a slice (an access unit
// delimiter, SPS, PPS or SEI), or a slice that is a picture's first.
func (f *h264Frames) next() ([]byte, error) {
	var unit []byte
	sawSlice := false
	for {
		nal := f.pending
		f.pending = nil
		if nal == nil {
			var err error
			nal, err = f.nals.NextNAL()
			if errors.Is(err, io.EOF) || (err == nil && nal == nil) {
				if len(unit) == 0 {
					return nil, io.EOF
				}

				return unit, nil
			}
			if err != nil {
				return nil, err
			}
		}
		if sawSlice && (!vcl(nal) || startsPicture(nal)) {
			f.pending = nal
			return unit, nil
		}
		if vcl(nal) {
			sawSlice = true
		}
		unit = append(unit, 0, 0, 0, 1)
		unit = append(unit, nal.Data...)
	}
}

type h264Source struct {
	file   *os.File
	frames *h264Frames
}

func openH264(path string) (frameSource, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	// h264reader reads a byte at a time.
	frames, err := newH264Frames(bufio.NewReader(file))
	if err != nil {
		_ = file.Close()
		return nil, err
	}

	return &h264Source{file: file, frames: frames}, nil
}

func (s *h264Source) next() ([]byte, error) { return s.frames.next() }
func (s *h264Source) frame() time.Duration  { return time.Second / h264Rate }
func (s *h264Source) close()                { _ = s.file.Close() }

// videoTrack is one viewer's picture, sent in whichever codec its stream is in
// at the moment, the way Broadcast Box's own track does it (TrackMultiCodec in
// its internal/webrtc): when the session starts it takes one payload type for
// each codec the viewer offered, and then sends each packet with the payload
// type of the codec it is in. A stream whose codec changes while it runs (a
// streamer who changed encoder) carries on in the new one on the same
// connection.
//
// A viewer who did not offer the stream's codec is answered all the same and
// keeps its session and its sound, but is sent no picture, and the stand-in
// logs it once per session and codec. That is what design/clips-research.json
// read in Broadcast Box v2.0.2's source (track_multi_codec.go: payload types
// are taken only for codecs the viewer agreed to, and packets of any other
// codec go out with payload type 0, which the player drops); it has not been
// tried against a running Broadcast Box. This stand-in sends nothing rather
// than packets of payload type 0.
type videoTrack struct {
	key string
	// offered is the video codecs the viewer's offer had, for the log.
	offered string

	mu    sync.Mutex
	bound bool
	out   webrtc.TrackLocalWriter
	ssrc  webrtc.SSRC
	types map[string]webrtc.PayloadType
	seq   uint16
	stamp uint32
	vp8   codecs.VP8Payloader
	h264  codecs.H264Payloader
}

func newVideoTrack(key string) *videoTrack {
	return &videoTrack{key: key, types: map[string]webrtc.PayloadType{}}
}

func (t *videoTrack) ID() string                { return "video" }
func (t *videoTrack) RID() string               { return "" }
func (t *videoTrack) StreamID() string          { return t.key }
func (t *videoTrack) Kind() webrtc.RTPCodecType { return webrtc.RTPCodecTypeVideo }

// codecOf is which of the stand-in's codecs a negotiated one is, or "" for
// one it never sends. It goes by the codec's family, as Broadcast Box binds
// its payload types (design/clips-spec.md §2.4.1: one per codec family, by
// MIME type): H.264 in any profile or mode is H.264. pion, which Broadcast Box
// answers with too, puts a viewer's H.264 in the answer even when no profile
// matches one registered (codecParametersFuzzySearch falls back to the MIME
// type), so a viewer offering H.264 only as 42001f or 640c1f is sent the
// stream's constrained baseline under its own payload type, which its
// decoder can play.
func codecOf(codec webrtc.RTPCodecParameters) string {
	switch {
	case strings.EqualFold(codec.MimeType, webrtc.MimeTypeVP8):
		return codecVP8
	case strings.EqualFold(codec.MimeType, webrtc.MimeTypeH264):
		return codecH264
	}

	return ""
}

// broadcastBoxsOwn is whether a negotiated H.264 entry is Broadcast Box's
// documented one (constrained baseline, packetization-mode 1), which Bind
// takes over any other H.264 the viewer listed first. Which of several
// Broadcast Box itself takes is not recorded here; this is the one the
// stream's pictures are made for.
func broadcastBoxsOwn(codec webrtc.RTPCodecParameters) bool {
	return fmtpValue(codec.SDPFmtpLine, "packetization-mode") == "1" &&
		strings.HasPrefix(strings.ToLower(fmtpValue(codec.SDPFmtpLine, "profile-level-id")), "42e0")
}

// fmtpValue is one parameter of an fmtp line, or "".
func fmtpValue(fmtp, name string) string {
	for _, part := range strings.Split(fmtp, ";") {
		if key, value, ok := strings.Cut(strings.TrimSpace(part), "="); ok && strings.EqualFold(key, name) {
			return value
		}
	}

	return ""
}

// Broadcast Box v2.0.2's codecs, as far as this repository records them
// (design/clips-research.json, design/clips-spec.md §2.4.1 and the payload
// types in server/clips_recorder.go): H.264 at payload type 96 as
// constrained baseline, packetization-mode 1; AV1 45; VP9 98 (profile 0) and
// 100 (profile 2); H.265 113; Opus 111. Broadcast Box registers six more
// H.264 payload types (102, 103, 104, 106, 108, 39) whose profiles nobody has
// written down here, so they are left out; pion answers a viewer's H.264 in
// any profile all the same (see codecOf). VP8 is the stand-in's own addition
// (see codecVP8).
const broadcastBoxH264Fmtp = "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"

func registerBroadcastBoxCodecs(engine *webrtc.MediaEngine) error {
	feedback := []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}}
	video := []webrtc.RTPCodecParameters{
		{PayloadType: 96, RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264, ClockRate: 90000, SDPFmtpLine: broadcastBoxH264Fmtp, RTCPFeedback: feedback}},
		{PayloadType: 45, RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeAV1, ClockRate: 90000, RTCPFeedback: feedback}},
		{PayloadType: 98, RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP9, ClockRate: 90000, SDPFmtpLine: "profile-id=0", RTCPFeedback: feedback}},
		{PayloadType: 100, RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP9, ClockRate: 90000, SDPFmtpLine: "profile-id=2", RTCPFeedback: feedback}},
		{PayloadType: 113, RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH265, ClockRate: 90000, SDPFmtpLine: "level-id=93;profile-id=1;tier-flag=0;tx-mode=SRST", RTCPFeedback: feedback}},
		{PayloadType: 120, RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000, RTCPFeedback: feedback}},
	}
	for _, codec := range video {
		if err := engine.RegisterCodec(codec, webrtc.RTPCodecTypeVideo); err != nil {
			return err
		}
	}

	return engine.RegisterCodec(webrtc.RTPCodecParameters{PayloadType: 111, RTPCodecCapability: webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2, SDPFmtpLine: "minptime=10;useinbandfec=1",
	}}, webrtc.RTPCodecTypeAudio)
}

// offerVideoCodecs is the video codecs an offer has, in its own order, H.264
// with its profile and packetization mode: for a log line, not a decision.
func offerVideoCodecs(offer string) string {
	var names []string
	seen := map[string]bool{}
	inVideo := false
	rtpmap := map[string]string{}
	var order []string
	for _, line := range strings.Split(offer, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "m=") {
			inVideo = strings.HasPrefix(line, "m=video")
			continue
		}
		if !inVideo {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "a=rtpmap:"); ok {
			pt, name, _ := strings.Cut(rest, " ")
			name, _, _ = strings.Cut(name, "/")
			rtpmap[pt] = name
			order = append(order, pt)
		}
		if rest, ok := strings.CutPrefix(line, "a=fmtp:"); ok {
			pt, fmtp, _ := strings.Cut(rest, " ")
			if strings.EqualFold(rtpmap[pt], "H264") {
				rtpmap[pt] = "H264 " + fmtpValue(fmtp, "profile-level-id") + " mode " + fmtpValue(fmtp, "packetization-mode")
			}
		}
	}
	for _, pt := range order {
		name := rtpmap[pt]
		if strings.EqualFold(name, "rtx") || strings.EqualFold(name, "red") || strings.EqualFold(name, "ulpfec") || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) == 0 {
		return "no video"
	}

	return strings.Join(names, ", ")
}

// Bind takes a payload type for each codec the viewer agreed to: the first
// of each, except that for H.264 Broadcast Box's own entry wins. What
// it answers pion with only says how this sender is described to pion's own
// interceptors; the packets carry their own payload types.
func (t *videoTrack) Bind(ctx webrtc.TrackLocalContext) (webrtc.RTPCodecParameters, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	negotiated := ctx.CodecParameters()
	if len(negotiated) == 0 {
		return webrtc.RTPCodecParameters{}, webrtc.ErrUnsupportedCodec
	}
	own := false
	for _, codec := range negotiated {
		name := codecOf(codec)
		if name == "" {
			continue
		}
		if _, taken := t.types[name]; !taken || (name == codecH264 && !own && broadcastBoxsOwn(codec)) {
			t.types[name] = codec.PayloadType
			own = own || (name == codecH264 && broadcastBoxsOwn(codec))
		}
	}
	t.out, t.ssrc, t.bound = ctx.WriteStream(), ctx.SSRC(), true

	return negotiated[0], nil
}

func (t *videoTrack) Unbind(webrtc.TrackLocalContext) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.out = nil

	return nil
}

// isBound is whether the session has started sending the picture: a session
// whose offer had no picture never is.
func (t *videoTrack) isBound() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.bound
}

// payloadType is the payload type a codec is sent with to this viewer, if it
// offered the codec.
func (t *videoTrack) payloadType(codec string) (webrtc.PayloadType, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	pt, ok := t.types[codec]
	return pt, ok
}

// write sends one frame in the given codec. The clock goes on whether or not
// it is sent, so the timestamps stay one clock across a change of codec, as
// they do across Broadcast Box's.
func (t *videoTrack) write(codec string, frame []byte, length time.Duration) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Rounded: a thirtieth of a second is 3000 ticks, not the 2999 that
	// truncating its nanoseconds gives, which would make the clock run slow.
	stamp := t.stamp
	t.stamp += uint32((length*90000 + time.Second/2) / time.Second)
	pt, offered := t.types[codec]
	if t.out == nil || !offered {
		return nil
	}

	var payloads [][]byte
	switch codec {
	case codecVP8:
		payloads = t.vp8.Payload(rtpPayloadMTU, frame)
	case codecH264:
		payloads = t.h264.Payload(rtpPayloadMTU, frame)
	default:
		return fmt.Errorf("no such codec as %q", codec)
	}
	for i, payload := range payloads {
		header := rtp.Header{
			Version:        2,
			Marker:         i == len(payloads)-1,
			PayloadType:    uint8(pt),
			SequenceNumber: t.seq,
			Timestamp:      stamp,
			SSRC:           uint32(t.ssrc),
		}
		t.seq++
		if _, err := t.out.WriteRTP(&header, payload); err != nil {
			return err
		}
	}

	return nil
}
