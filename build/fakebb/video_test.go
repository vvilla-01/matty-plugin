package main

import (
	"bytes"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media/oggreader"
	"github.com/pion/webrtc/v4/pkg/media/oggwriter"
)

// annexB is NAL units each after a four-byte start code.
func annexB(nals ...[]byte) []byte {
	var out []byte
	for _, nal := range nals {
		out = append(out, 0, 0, 0, 1)
		out = append(out, nal...)
	}

	return out
}

var (
	testSPS     = []byte{0x67, 0x42, 0xc0, 0x1f, 0xda}
	testPPS     = []byte{0x68, 0xce, 0x3c, 0x80}
	testSEI     = []byte{0x06, 0x05, 0x01, 0xaa, 0x80}
	testAUD     = []byte{0x09, 0xf0}
	testIDR     = []byte{0x65, 0x88, 0x84, 0x21, 0x00, 0x03, 0x01, 0x7f} // first_mb_in_slice 0; 00 00 03 is kept as it is
	testIDRRest = []byte{0x65, 0x4c, 0x11, 0x22}                         // a second slice of the same picture
	testP1      = []byte{0x41, 0x9a, 0x01, 0x02}
	testP2      = []byte{0x41, 0x9a, 0x03, 0x04}
)

// A raw H.264 stream is cut into pictures: the SPS and PPS go with the
// keyframe after them, a picture of two slices is one unit, an access unit
// delimiter begins one, and the bytes are sent exactly as they are in the
// file (emulation prevention included).
func TestTheH264ClipIsCutIntoPictures(t *testing.T) {
	file := annexB(testSPS, testPPS, testSEI, testIDR, testP1, testP2, testAUD, testSPS, testPPS, testIDR, testIDRRest, testP1)
	frames, err := newH264Frames(bytes.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}

	want := [][]byte{
		annexB(testSPS, testPPS, testIDR), // pion's reader leaves SEI out
		annexB(testP1),
		annexB(testP2),
		annexB(testAUD, testSPS, testPPS, testIDR, testIDRRest),
		annexB(testP1),
	}
	for i, unit := range want {
		got, nextErr := frames.next()
		if nextErr != nil {
			t.Fatalf("unit %d: %v", i, nextErr)
		}
		if !bytes.Equal(got, unit) {
			t.Fatalf("unit %d is % x, not % x", i, got, unit)
		}
	}
	if got, nextErr := frames.next(); !errors.Is(nextErr, io.EOF) {
		t.Fatalf("after the last unit came % x, %v", got, nextErr)
	}
}

// aSecondOfPictures is what the Makefile's clip is, in small: seconds of
// pictures at h264Rate a second, the first of each second a keyframe with its
// SPS and PPS before it, the rest one slice each.
func aSecondOfPictures(seconds int) []byte {
	var out []byte
	for i := range seconds * h264Rate {
		if i%h264Rate == 0 {
			out = append(out, annexB(testSPS, testPPS, testIDR)...)
		} else {
			out = append(out, annexB([]byte{0x41, 0x9a, byte(i), 0x01})...)
		}
	}

	return out
}

// checkAKeyframeASecond reads a clip through and checks it is single pictures
// at h264Rate a second, a keyframe each second with its SPS and PPS in front
// of it (what OBS sends and the recorder needs), and as long as it should be.
func checkAKeyframeASecond(t *testing.T, source frameSource, seconds int) {
	t.Helper()
	var length time.Duration
	units := 0
	for ; ; units++ {
		unit, nextErr := source.next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		length += source.frame()

		var types []byte
		pictures := 0
		for _, nal := range bytes.Split(unit, []byte{0, 0, 0, 1})[1:] {
			types = append(types, nal[0]&0x1f)
			if nal[0]&0x1f == 1 || nal[0]&0x1f == 5 {
				if nal[1]&0x80 != 0 {
					pictures++
				}
			}
		}
		if pictures != 1 {
			t.Fatalf("unit %d holds %d pictures (NAL types %v)", units, pictures, types)
		}
		if units%h264Rate == 0 {
			if len(types) < 3 || !bytes.Equal(types[len(types)-3:len(types)-1], []byte{7, 8}) || types[len(types)-1] != 5 {
				t.Fatalf("unit %d, a second in, is NAL types %v, not SPS, PPS and a keyframe", units, types)
			}
		} else if !bytes.Equal(types, []byte{1}) {
			t.Fatalf("unit %d is NAL types %v, not one slice of a picture that is not a keyframe", units, types)
		}
	}
	want := time.Duration(seconds) * time.Second
	if units != seconds*h264Rate || length < want-time.Millisecond || length > want+time.Millisecond {
		t.Fatalf("the clip is %d pictures, %v long", units, length)
	}
}

// Three seconds of pictures made here are cut into pictures and timed as the
// stand-in sends them; so is the Makefile's own clip where it has been made.
func TestAnH264ClipIsAKeyframeASecond(t *testing.T) {
	frames, err := newH264Frames(bytes.NewReader(aSecondOfPictures(3)))
	if err != nil {
		t.Fatal(err)
	}
	checkAKeyframeASecond(t, &h264Source{frames: frames}, 3)

	const path = "media/stream.h264"
	if _, statErr := os.Stat(path); statErr != nil {
		t.Log("no " + path + " (make fakebb-media makes it): checked on the clip made here only")
		return
	}
	source, err := openH264(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.close()
	checkAKeyframeASecond(t, source, 12)
}

// writes is a viewer's connection, as far as a track sees it.
type writes struct {
	mu      sync.Mutex
	packets []rtp.Packet
}

func (w *writes) WriteRTP(header *rtp.Header, payload []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.packets = append(w.packets, rtp.Packet{Header: *header, Payload: append([]byte(nil), payload...)})
	return len(payload), nil
}

func (w *writes) sent() []rtp.Packet {
	w.mu.Lock()
	defer w.mu.Unlock()

	return append([]rtp.Packet(nil), w.packets...)
}

func (w *writes) Write(b []byte) (int, error) { return len(b), nil }

type bound struct {
	codecs []webrtc.RTPCodecParameters
	out    *writes
}

func (b bound) CodecParameters() []webrtc.RTPCodecParameters           { return b.codecs }
func (b bound) HeaderExtensions() []webrtc.RTPHeaderExtensionParameter { return nil }
func (b bound) SSRC() webrtc.SSRC                                      { return 1234 }
func (b bound) SSRCRetransmission() webrtc.SSRC                        { return 0 }
func (b bound) SSRCForwardErrorCorrection() webrtc.SSRC                { return 0 }
func (b bound) WriteStream() webrtc.TrackLocalWriter                   { return b.out }
func (b bound) ID() string                                             { return "sender" }
func (b bound) RTCPReader() interceptor.RTCPReader                     { return nil }

func codec(mime string, pt webrtc.PayloadType, fmtp string) webrtc.RTPCodecParameters {
	return webrtc.RTPCodecParameters{PayloadType: pt, RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: mime, ClockRate: 90000, SDPFmtpLine: fmtp}}
}

// A viewer is sent each picture in the stream's codec, with the payload type
// it agreed to for that codec, on one clock that carries on across a change of
// codec; a codec it did not offer is not sent to it at all.
func TestAViewerIsSentTheStreamsCodecWithItsOwnPayloadType(t *testing.T) {
	big := annexB(testSPS, testPPS, append([]byte{0x65, 0x88}, bytes.Repeat([]byte{0x11}, 3000)...))
	frame := time.Second / h264Rate

	// The app offers both; pion's packetization-mode 0 entry is not one an H.264 picture can be sent in.
	app := bound{out: &writes{}, codecs: []webrtc.RTPCodecParameters{
		codec(webrtc.MimeTypeVP8, 96, ""),
		codec(webrtc.MimeTypeH264, 127, "level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42e01f"),
		codec(webrtc.MimeTypeH264, 102, "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"),
		codec(webrtc.MimeTypeH264, 106, "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f"),
	}}
	track := newVideoTrack("victor")
	if _, err := track.Bind(app); err != nil {
		t.Fatal(err)
	}
	if err := track.write(codecH264, big, frame); err != nil {
		t.Fatal(err)
	}
	if err := track.write(codecVP8, []byte{0x10, 0x02, 0x00, 0x9d, 0x01, 0x2a}, frame); err != nil {
		t.Fatal(err)
	}
	h264, vp8 := app.out.packets[:len(app.out.packets)-1], app.out.packets[len(app.out.packets)-1]
	if len(h264) < 3 {
		t.Fatalf("a 3 kB picture went in %d packets", len(h264))
	}
	for i, packet := range append(h264, vp8) {
		if packet.SequenceNumber != uint16(i) || packet.SSRC != 1234 {
			t.Fatalf("packet %d has sequence %d and SSRC %d", i, packet.SequenceNumber, packet.SSRC)
		}
	}
	for i, packet := range h264 {
		if packet.PayloadType != 102 || packet.Timestamp != 0 || packet.Marker != (i == len(h264)-1) {
			t.Fatalf("H.264 packet %d: payload type %d, timestamp %d, marker %v", i, packet.PayloadType, packet.Timestamp, packet.Marker)
		}
	}
	if vp8.PayloadType != 96 || vp8.Timestamp != 3000 || !vp8.Marker {
		t.Fatalf("the VP8 picture after it: payload type %d, timestamp %d, marker %v", vp8.PayloadType, vp8.Timestamp, vp8.Marker)
	}

	// The recorder offers H.264 and no VP8: a stream in VP8 sends it nothing,
	// and its H.264 when the stream changes is on the same clock.
	recorder := bound{out: &writes{}, codecs: []webrtc.RTPCodecParameters{
		codec(webrtc.MimeTypeH264, 96, "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"),
		codec(webrtc.MimeTypeAV1, 45, ""),
	}}
	track = newVideoTrack("alice")
	if _, err := track.Bind(recorder); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := track.write(codecVP8, []byte{0x10, 0x02}, frame); err != nil {
			t.Fatal(err)
		}
	}
	if len(recorder.out.packets) != 0 {
		t.Fatalf("a viewer with no VP8 was sent %d VP8 packets", len(recorder.out.packets))
	}
	if err := track.write(codecH264, annexB(testP1), frame); err != nil {
		t.Fatal(err)
	}
	if len(recorder.out.packets) != 1 || recorder.out.packets[0].PayloadType != 96 || recorder.out.packets[0].Timestamp != 9000 {
		t.Fatalf("after the change to H.264 the recorder was sent %+v", recorder.out.packets)
	}

	// H.264 goes by family, as Broadcast Box binds it: a viewer with H.264 in
	// other profiles only is sent it under its first one; one that also has
	// Broadcast Box's own entry, under that, wherever it is in the list.
	for _, c := range []struct {
		codecs []webrtc.RTPCodecParameters
		want   webrtc.PayloadType
	}{
		{[]webrtc.RTPCodecParameters{
			codec(webrtc.MimeTypeH264, 102, "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f"),
			codec(webrtc.MimeTypeH264, 112, "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=640c1f"),
		}, 102},
		{[]webrtc.RTPCodecParameters{
			codec(webrtc.MimeTypeH264, 112, "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=640c1f"),
			codec(webrtc.MimeTypeH264, 127, "level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42e01f"),
			codec(webrtc.MimeTypeH264, 106, broadcastBoxH264Fmtp),
		}, 106},
	} {
		other := bound{out: &writes{}, codecs: c.codecs}
		track = newVideoTrack("carol")
		if _, err := track.Bind(other); err != nil {
			t.Fatal(err)
		}
		if err := track.write(codecH264, big, frame); err != nil {
			t.Fatal(err)
		}
		if pt, _ := track.payloadType(codecH264); pt != c.want || len(other.out.packets) == 0 || other.out.packets[0].PayloadType != uint8(c.want) {
			t.Fatalf("H.264 was taken as %d (not %d) and sent %d packets", pt, c.want, len(other.out.packets))
		}
	}

	// A session with no picture in it never binds the track: nothing is sent, and nothing breaks.
	if err := newVideoTrack("bob").write(codecH264, big, frame); err != nil {
		t.Fatal(err)
	}
}

func testBox(t *testing.T) *box {
	t.Helper()
	api, err := newBoxAPI()
	if err != nil {
		t.Fatal(err)
	}
	b := &box{streams: map[string]*stream{}, sessions: map[string]*session{}, api: api, inH264: map[string]bool{"victor": true}}
	t.Cleanup(func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		for _, s := range b.sessions {
			_ = s.pc.Close()
		}
	})

	return b
}

func post(handler http.HandlerFunc, target string, body string, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	answer := httptest.NewRecorder()
	handler(answer, req)

	return answer
}

// A stream starts in the codec -codecs gives it, and /fake/codec changes only
// its codec: whether it is on the air, paused or silent stays as it was, and
// /fake/live leaves the codec alone.
func TestTheCodecControlChangesOnlyTheCodec(t *testing.T) {
	b := testBox(t)
	post(b.setLive, "/fake/live?key=victor", "")
	post(b.setLive, "/fake/live?key=alice&paused=true", "")
	if b.codecOfStream("victor") != codecH264 || b.codecOfStream("alice") != codecVP8 {
		t.Fatalf("victor starts in %s and alice in %s", b.codecOfStream("victor"), b.codecOfStream("alice"))
	}

	if answer := post(b.setCodec, "/fake/codec?key=alice&codec=h264", ""); answer.Code != http.StatusOK {
		t.Fatalf("the change was answered %d: %s", answer.Code, answer.Body)
	}
	alice := b.streams["alice"]
	if alice.Codec != codecH264 || !alice.Live || !alice.Paused || alice.Silent {
		t.Fatalf("after the change alice is %+v", *alice)
	}
	post(b.setLive, "/fake/live?key=alice", "")
	if b.codecOfStream("alice") != codecH264 {
		t.Fatal("coming back from a pause changed the codec")
	}
	post(b.setCodec, "/fake/codec?key=alice&codec=vp8", "")
	if b.codecOfStream("alice") != codecVP8 {
		t.Fatal("alice could not be put back to VP8")
	}

	for _, target := range []string{"/fake/codec?key=alice&codec=av1", "/fake/codec?codec=h264", "/fake/codec?key=alice"} {
		if answer := post(b.setCodec, target, ""); answer.Code != http.StatusBadRequest {
			t.Errorf("%s was answered %d", target, answer.Code)
		}
	}
	if b.codecOfStream("alice") != codecVP8 {
		t.Fatal("a refused change changed the codec")
	}
	get := httptest.NewRecorder()
	b.setCodec(get, httptest.NewRequest(http.MethodGet, "/fake/codec?key=alice&codec=h264", nil))
	if get.Code != http.StatusMethodNotAllowed || b.codecOfStream("alice") != codecVP8 {
		t.Fatalf("a GET was answered %d and alice is in %s", get.Code, b.codecOfStream("alice"))
	}
}

// offerFrom is the offer a player with these video codecs makes: sound, then
// picture, both received only.
func offerFrom(t *testing.T, video ...webrtc.RTPCodecParameters) string {
	t.Helper()
	engine := &webrtc.MediaEngine{}
	for _, c := range video {
		if err := engine.RegisterCodec(c, webrtc.RTPCodecTypeVideo); err != nil {
			t.Fatal(err)
		}
	}
	opus := webrtc.RTPCodecParameters{PayloadType: 111, RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}}
	if err := engine.RegisterCodec(opus, webrtc.RTPCodecTypeAudio); err != nil {
		t.Fatal(err)
	}
	pc, err := webrtc.NewAPI(webrtc.WithMediaEngine(engine)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	for _, kind := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeAudio, webrtc.RTPCodecTypeVideo} {
		if _, err = pc.AddTransceiverFromKind(kind, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
			t.Fatal(err)
		}
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}

	return offer.SDP
}

// Every offer Broadcast Box can read is answered with a session, whatever its
// stream's codec: the recorder's (H.264 and no VP8), the app's (both) and one
// with VP8 alone. What decides whether the picture can be sent is the payload
// types the session took for each codec.
func TestEveryKindOfOfferIsAnsweredAndTakesTheCodecsItOffered(t *testing.T) {
	recorderH264 := codec(webrtc.MimeTypeH264, 96, "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f")
	vp8 := codec(webrtc.MimeTypeVP8, 120, "")
	offers := []struct {
		name       string
		video      []webrtc.RTPCodecParameters
		vp8, h264  bool
		h264Answer bool
	}{
		{"the recorder's", []webrtc.RTPCodecParameters{recorderH264, codec(webrtc.MimeTypeAV1, 45, "")}, false, true, true},
		{"the app's", []webrtc.RTPCodecParameters{vp8, recorderH264}, true, true, true},
		{"one with VP8 alone", []webrtc.RTPCodecParameters{vp8}, true, false, false},
		{"one with H.264 as 42001f only", []webrtc.RTPCodecParameters{codec(webrtc.MimeTypeH264, 102, "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f")}, false, true, true},
		{"one with H.264 as 640c1f only", []webrtc.RTPCodecParameters{codec(webrtc.MimeTypeH264, 112, "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=640c1f")}, false, true, true},
		{"one with AV1 alone", []webrtc.RTPCodecParameters{codec(webrtc.MimeTypeAV1, 45, "")}, false, false, false},
	}
	for _, key := range []string{"victor", "alice"} {
		for _, offer := range offers {
			b := testBox(t)
			post(b.setLive, "/fake/live?key="+key, "")
			answer := post(b.whep, "/api/whep", offerFrom(t, offer.video...), "Authorization", "Bearer "+key)
			if answer.Code != http.StatusCreated {
				t.Fatalf("%s offer for %s was answered %d: %s", offer.name, key, answer.Code, answer.Body)
			}
			if strings.Contains(answer.Body.String(), "H264") != offer.h264Answer {
				t.Fatalf("%s offer for %s: H.264 in the answer is %v:\n%s", offer.name, key, !offer.h264Answer, answer.Body)
			}
			b.mu.Lock()
			if len(b.sessions) != 1 {
				t.Fatalf("%s offer for %s made %d sessions", offer.name, key, len(b.sessions))
			}
			for _, s := range b.sessions {
				_, hasVP8 := s.video.payloadType(codecVP8)
				h264PT, hasH264 := s.video.payloadType(codecH264)
				var offeredH264 webrtc.PayloadType
				for _, c := range offer.video {
					if c.MimeType == webrtc.MimeTypeH264 {
						offeredH264 = c.PayloadType
					}
				}
				if hasVP8 != offer.vp8 || hasH264 != offer.h264 || (hasH264 && h264PT != offeredH264) {
					t.Fatalf("%s offer for %s took VP8 %v, H.264 %v (payload type %d)", offer.name, key, hasVP8, hasH264, h264PT)
				}
			}
			b.mu.Unlock()
		}
	}
}

// opusOgg is an Ogg Opus file made here, the way ffmpeg makes the
// stand-in's: OpusHead, OpusTags, then packets of sound.
func opusOgg(t *testing.T, packets int) []byte {
	t.Helper()
	var file bytes.Buffer
	writer, err := oggwriter.NewWith(&file, 48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	for i := range packets {
		packet := &rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: uint16(i), Timestamp: uint32(i * 960)}, Payload: []byte{0xfc, byte(i), 0x02}}
		if err = writer.WriteRTP(packet); err != nil {
			t.Fatal(err)
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}

	return file.Bytes()
}

// The sound is sent without Opus's headers, each page of it 20 ms long: the
// reading playAudio does, of a file with the headers in it.
func TestTheSoundLoopSendsNoOpusHeaders(t *testing.T) {
	ogg, _, err := oggreader.NewWith(bytes.NewReader(opusOgg(t, 5)))
	if err != nil {
		t.Fatal(err)
	}
	var last uint64
	for i := range 5 {
		data, length, nextErr := nextSound(ogg, &last, 20*time.Millisecond)
		if nextErr != nil {
			t.Fatalf("page %d: %v", i, nextErr)
		}
		if !bytes.Equal(data, []byte{0xfc, byte(i), 0x02}) || length != 20*time.Millisecond {
			t.Fatalf("page %d is % x, %v long", i, data, length)
		}
	}
	if _, _, nextErr := nextSound(ogg, &last, 20*time.Millisecond); !errors.Is(nextErr, io.EOF) {
		t.Fatalf("after the sound came %v", nextErr)
	}
}

// fakeClip is a codec's clip for playVideo: its frames, each two
// milliseconds, or a read that fails.
type fakeClip struct {
	codec  string
	frames [][]byte
	at     int
	broken bool
}

func (c *fakeClip) next() ([]byte, error) {
	if c.broken {
		return nil, errors.New("data is not a H264 bitstream")
	}
	if c.at == len(c.frames) {
		return nil, io.EOF
	}
	c.at++

	return c.frames[c.at-1], nil
}

func (c *fakeClip) frame() time.Duration { return 2 * time.Millisecond }
func (c *fakeClip) close()               {}

// lines is the log, as a test reads it.
type lines struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lines) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lines) count(text string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Count(l.buf.String(), text)
}

func logTo(t *testing.T) *lines {
	t.Helper()
	out := &lines{}
	log.SetOutput(out)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	return out
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if done() {
			return
		}
	}
	t.Fatalf("waited 5 s for %s", what)
}

// playVideo sends the stream's codec, and when /fake/codec changes it, the
// other codec's clip from its first frame (its keyframe), on the same
// connection; at the end of a clip it starts again.
func TestPlayVideoFollowsTheStreamsCodec(t *testing.T) {
	logTo(t)
	b := testBox(t)
	b.open = func(codec string) (frameSource, error) {
		mark := byte(1)
		if codec == codecVP8 {
			mark = 2
		}
		frames := [][]byte{annexB(testSPS, testPPS, testIDR), annexB(testP1), annexB(testP2)}
		if codec == codecVP8 {
			// Long, so that a change back to H.264 has to cut it short.
			frames = [][]byte{{0x10, mark, 0}}
			for i := range 1000 {
				frames = append(frames, []byte{0x11, mark, byte(i)})
			}
		}
		return &fakeClip{codec: codec, frames: frames}, nil
	}
	post(b.setLive, "/fake/live?key=victor", "")
	viewer := bound{out: &writes{}, codecs: []webrtc.RTPCodecParameters{
		codec(webrtc.MimeTypeH264, 96, broadcastBoxH264Fmtp),
		codec(webrtc.MimeTypeVP8, 120, ""),
	}}
	track := newVideoTrack("victor")
	if _, err := track.Bind(viewer); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	ended := make(chan struct{})
	go func() { b.playVideo("victor", "v1", track, done); close(ended) }()
	defer func() { close(done); <-ended }()

	// More than one clip's worth: it went round.
	waitFor(t, "H.264 pictures", func() bool { return len(viewer.out.sent()) >= 10 })
	post(b.setCodec, "/fake/codec?key=victor&codec=vp8", "")
	waitFor(t, "VP8 pictures", func() bool { p := viewer.out.sent(); return p[len(p)-1].PayloadType == 120 })

	sent := viewer.out.sent()
	first := -1
	for i, packet := range sent {
		if packet.PayloadType == 120 && first < 0 {
			first = i
		}
		if first >= 0 && packet.PayloadType != 120 {
			t.Fatalf("packet %d went back to payload type %d", i, packet.PayloadType)
		}
		if first < 0 && packet.PayloadType != 96 {
			t.Fatalf("packet %d of the H.264 stream had payload type %d", i, packet.PayloadType)
		}
	}
	if keyframes := 0; true {
		for _, packet := range sent[:first] {
			if packet.Payload[0]&0x1f == 24 { // STAP-A: SPS and PPS with the keyframe
				keyframes++
			}
		}
		if keyframes < 3 {
			t.Fatalf("%d keyframes in %d H.264 packets: the clip did not start again from the top", keyframes, first)
		}
	}
	if got := sent[first].Payload[1:]; !bytes.Equal(got, []byte{0x10, 2, 0}) {
		t.Fatalf("the first VP8 picture after the change is % x, not the clip's first", got)
	}

	// And back, in the middle of the VP8 clip: H.264 again from its keyframe.
	vp8Sent := len(viewer.out.sent()) - first
	post(b.setCodec, "/fake/codec?key=victor&codec=h264", "")
	waitFor(t, "H.264 again", func() bool { p := viewer.out.sent(); return p[len(p)-1].PayloadType == 96 })
	sent = viewer.out.sent()
	back := len(sent) - 1
	for sent[back-1].PayloadType == 96 {
		back--
	}
	if back-first > vp8Sent+200 || sent[back].Payload[0]&0x1f != 24 {
		t.Fatalf("H.264 came back after %d VP8 pictures (%d when asked), starting with NAL type %d", back-first, vp8Sent, sent[back].Payload[0]&0x1f)
	}
}

// A viewer whose offer lacks the stream's codec is told about once in the
// log, and a clip that cannot be read is said once, not on every tick.
func TestPlayVideoSaysOnceWhatItCannotSend(t *testing.T) {
	out := logTo(t)
	b := testBox(t)
	opens := 0
	var mu sync.Mutex
	b.open = func(codec string) (frameSource, error) {
		mu.Lock()
		defer mu.Unlock()
		opens++
		return &fakeClip{codec: codec, broken: codec == codecH264, frames: [][]byte{{0x10, 0, 0}}}, nil
	}
	post(b.setLive, "/fake/live?key=victor", "")
	viewer := bound{out: &writes{}, codecs: []webrtc.RTPCodecParameters{codec(webrtc.MimeTypeVP8, 120, "")}}
	track := newVideoTrack("victor")
	track.offered = "VP8"
	if _, err := track.Bind(viewer); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	ended := make(chan struct{})
	go func() { b.playVideo("victor", "v1", track, done); close(ended) }()
	// Long enough for the clip to be tried again twice.
	time.Sleep(2300 * time.Millisecond)
	close(done)
	<-ended

	if n := out.count(`no picture for viewer v1 of "victor": the stream is in h264 and the offer had VP8`); n != 1 {
		t.Fatalf("the viewer with no H.264 was logged %d times:\n%s", n, out.buf.String())
	}
	if n := out.count("data is not a H264 bitstream"); n != 1 {
		t.Fatalf("the unreadable clip was logged %d times", n)
	}
	if opens > 4 {
		t.Fatalf("the unreadable clip was opened %d times in 2.3 s", opens)
	}
	if len(viewer.out.sent()) != 0 {
		t.Fatal("something was sent")
	}
}

// The log line says what an offer had for video, H.264 with its profile.
func TestTheOfferIsDescribedForTheLog(t *testing.T) {
	offer := offerFrom(t, codec(webrtc.MimeTypeVP8, 120, ""), codec(webrtc.MimeTypeH264, 102, "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f"))
	if got := offerVideoCodecs(offer); got != "VP8, H264 42001f mode 1" {
		t.Fatalf("the offer is described as %q", got)
	}
}
