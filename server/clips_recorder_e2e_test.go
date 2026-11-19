package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/pmp4"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
	"github.com/stretchr/testify/require"
)

// The recorder for real (spec §2.19 items 32 and 33), in real time, over
// loopback. A pion publisher sends the fixture's H.264 and Opus, paced as
// they were recorded and looped, to a minimal stand-in for Broadcast Box: an
// httptest server whose /api/whep answers each offer with a PeerConnection
// that forwards the publisher's tracks, as Broadcast Box does, and says which
// session it opened in Location: /api/whep/<uuid>. Like Broadcast Box v2.0.2
// it names each session's tracks' stream after the stream key
// (codecs.GetDefaultTracks), and sends a new session no picture until the
// publisher's next keyframe (whep.CreateNewWHEP sets IsWaitingForKeyframe;
// packet_writer.go drops video until one), its sound from the start. The
// recorder is the real one, with exchangeWHEP, and with loopback let in,
// which only tests do.
//
// Real-time WebRTC is flaky rather than wrong under the race detector, so
// these run only with VILLANET_E2E=1 (make e2e), and allow longer under
// -race. Set VILLANET_E2E_KEEP to a directory to keep a draft there.

const e2eStreamKey = "videokey-e2e-SECRETKEY"

func requireE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("VILLANET_E2E") != "1" {
		t.Skip("set VILLANET_E2E=1 or run make e2e")
	}
}

// e2eWithin is how long something real-time may take: d, or three times as
// long under -race.
func e2eWithin(d time.Duration) time.Duration {
	if raceEnabled {
		return 3 * d
	}
	return d
}

// e2eAPI is the stand-in's and the publisher's pion API: H.264 and Opus as
// Broadcast Box registers them, over loopback only.
func e2eAPI(t *testing.T) *webrtc.API {
	t.Helper()
	m := &webrtc.MediaEngine{}
	require.NoError(t, m.RegisterCodec(webrtc.RTPCodecParameters{PayloadType: recorderH264PT, RTPCodecCapability: e2eVideoCapability()}, webrtc.RTPCodecTypeVideo))
	require.NoError(t, m.RegisterCodec(webrtc.RTPCodecParameters{PayloadType: recorderOpusPT, RTPCodecCapability: e2eAudioCapability()}, webrtc.RTPCodecTypeAudio))
	registry := &interceptor.Registry{}
	require.NoError(t, webrtc.RegisterDefaultInterceptors(m, registry))

	settings := webrtc.SettingEngine{}
	settings.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	settings.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	settings.SetIncludeLoopbackCandidate(true)
	settings.SetIPFilter(func(ip net.IP) bool { return ip.IsLoopback() })
	return webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(registry), webrtc.WithSettingEngine(settings))
}

func e2eVideoCapability() webrtc.RTPCodecCapability {
	return webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeH264, ClockRate: videoClockRate, SDPFmtpLine: recorderH264Fmtp,
		RTCPFeedback: []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}},
	}
}

func e2eAudioCapability() webrtc.RTPCodecCapability {
	return webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: audioClockRate, Channels: 2, SDPFmtpLine: recorderOpusFmtp}
}

// e2eOrigin is the stand-in for Broadcast Box, with its publisher.
type e2eOrigin struct {
	t   *testing.T
	api *webrtc.API
	srv *httptest.Server

	// keyHold is how long after a session is answered the publisher's next
	// keyframe comes, at the soonest: a publisher whose keyframes are that
	// far apart. The fixture's own come every two seconds.
	keyHold time.Duration

	mu   sync.Mutex
	subs []*e2eSubscriber
	ids  []string
	pcs  []*webrtc.PeerConnection
}

// e2eSubscriber is one WHEP session: its own tracks, so that one session's
// sound can be stopped while the picture flows (Broadcast Box reusing its
// outgoing SSRC, which the recorder's SRTP replay check then drops).
type e2eSubscriber struct {
	video, audio *webrtc.TrackLocalStaticRTP
	muted        atomic.Bool

	// Broadcast Box's IsWaitingForKeyframe: no picture before the first
	// packet that starts a keyframe, and none of those before keyFrom.
	keyFrom time.Time
	keyed   atomic.Bool
}

// takesVideo is whether the session is sent this packet of the picture.
func (sub *e2eSubscriber) takesVideo(payload []byte) bool {
	if sub.keyed.Load() {
		return true
	}
	if time.Now().Before(sub.keyFrom) || !e2eStartsKeyframe(payload) {
		return false
	}
	sub.keyed.Store(true)
	return true
}

// e2eStartsKeyframe is whether an H.264 RTP payload starts a keyframe: an
// SPS or an IDR slice, alone, in a STAP-A, or as the first fragment of a
// FU-A.
func e2eStartsKeyframe(payload []byte) bool {
	if len(payload) == 0 {
		return false
	}
	key := func(nal byte) bool { return nal&0x1f == 5 || nal&0x1f == 7 }
	switch payload[0] & 0x1f {
	case 24:
		for i := 1; i+2 < len(payload); {
			size := int(payload[i])<<8 | int(payload[i+1])
			i += 2
			if i < len(payload) && key(payload[i]) {
				return true
			}
			i += size
		}
		return false
	case 28:
		return len(payload) > 1 && payload[1]&0x80 != 0 && payload[1]&0x1f == 5
	default:
		return key(payload[0])
	}
}

func newE2EOrigin(t *testing.T) *e2eOrigin {
	t.Helper()
	return newE2EOriginHolding(t, 0)
}

// newE2EOriginHolding is the stand-in for a publisher whose next keyframe
// comes keyHold after each session is answered.
func newE2EOriginHolding(t *testing.T, keyHold time.Duration) *e2eOrigin {
	t.Helper()
	o := &e2eOrigin{t: t, api: e2eAPI(t), keyHold: keyHold}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/whep", o.serveWHEP)
	o.srv = httptest.NewServer(mux)
	t.Cleanup(func() {
		o.srv.Close()
		o.mu.Lock()
		pcs := slices.Clone(o.pcs)
		o.mu.Unlock()
		for _, pc := range pcs {
			_ = pc.Close()
		}
	})

	o.publish(t)
	return o
}

func (o *e2eOrigin) keep(pc *webrtc.PeerConnection) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.pcs = append(o.pcs, pc)
}

func (o *e2eOrigin) sessions() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.ids)
}

func (o *e2eOrigin) mute(session int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.subs[session].muted.Store(true)
}

func e2eUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func (o *e2eOrigin) serveWHEP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+e2eStreamKey {
		http.Error(w, "unknown stream", http.StatusUnauthorized)
		return
	}
	offer, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	pc, err := o.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	o.keep(pc)

	// Named after the stream key, as Broadcast Box names them: exchangeWHEP
	// must take it out of the answer.
	sub := &e2eSubscriber{keyFrom: time.Now().Add(o.keyHold)}
	sub.video, _ = webrtc.NewTrackLocalStaticRTP(e2eVideoCapability(), "video", e2eStreamKey)
	sub.audio, _ = webrtc.NewTrackLocalStaticRTP(e2eAudioCapability(), "audio", e2eStreamKey)
	for _, track := range []webrtc.TrackLocal{sub.video, sub.audio} {
		sender, err := pc.AddTrack(track)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		go func() {
			buf := make([]byte, 1500)
			for {
				if _, _, err := sender.Read(buf); err != nil {
					return
				}
			}
		}()
	}

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: string(offer)}); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	<-gathered

	id := e2eUUID()
	o.mu.Lock()
	o.subs = append(o.subs, sub)
	o.ids = append(o.ids, id)
	o.mu.Unlock()

	w.Header().Set("Location", "/api/whep/"+id)
	w.Header().Set("Content-Type", "application/sdp")
	w.WriteHeader(http.StatusCreated)
	_, _ = io.WriteString(w, pc.LocalDescription().SDP)
}

// publish connects a publisher to the origin and starts sending the fixture,
// looped, in real time, until the test ends. The origin forwards each packet
// to every session, the picture only from its first keyframe on, and the
// sound only to sessions not muted.
func (o *e2eOrigin) publish(t *testing.T) {
	t.Helper()
	fix := loadMediaFixture(t)

	frames := make([][]byte, len(fix.video))
	for j := range fix.video {
		frames[j] = fix.frameAnnexB(t, j)
	}

	ingest, err := o.api.NewPeerConnection(webrtc.Configuration{})
	require.NoError(t, err)
	o.keep(ingest)
	ingest.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		video := track.Kind() == webrtc.RTPCodecTypeVideo
		for {
			pkt, _, err := track.ReadRTP()
			if err != nil {
				return
			}
			o.mu.Lock()
			subs := slices.Clone(o.subs)
			o.mu.Unlock()
			for _, sub := range subs {
				switch {
				case video:
					if sub.takesVideo(pkt.Payload) {
						_ = sub.video.WriteRTP(pkt)
					}
				case !sub.muted.Load():
					_ = sub.audio.WriteRTP(pkt)
				}
			}
		}
	})

	publisher, err := o.api.NewPeerConnection(webrtc.Configuration{})
	require.NoError(t, err)
	o.keep(publisher)
	videoTrack, err := webrtc.NewTrackLocalStaticSample(e2eVideoCapability(), "video", "obs")
	require.NoError(t, err)
	audioTrack, err := webrtc.NewTrackLocalStaticSample(e2eAudioCapability(), "audio", "obs")
	require.NoError(t, err)
	for _, track := range []webrtc.TrackLocal{videoTrack, audioTrack} {
		_, err := publisher.AddTransceiverFromTrack(track, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
		require.NoError(t, err)
	}

	connected := make(chan struct{})
	var once sync.Once
	publisher.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			once.Do(func() { close(connected) })
		}
	})

	offer, err := publisher.CreateOffer(nil)
	require.NoError(t, err)
	gathered := webrtc.GatheringCompletePromise(publisher)
	require.NoError(t, publisher.SetLocalDescription(offer))
	<-gathered
	require.NoError(t, ingest.SetRemoteDescription(*publisher.LocalDescription()))
	answer, err := ingest.CreateAnswer(nil)
	require.NoError(t, err)
	gathered = webrtc.GatheringCompletePromise(ingest)
	require.NoError(t, ingest.SetLocalDescription(answer))
	<-gathered
	require.NoError(t, publisher.SetRemoteDescription(*ingest.LocalDescription()))

	select {
	case <-connected:
	case <-time.After(e2eWithin(10 * time.Second)):
		t.Fatal("the publisher did not connect to the stand-in")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		<-done
	})

	go func() {
		defer close(done)
		frameNs := func(j int) int64 {
			next := int64(mediaFixtureLoopTicks)
			if j+1 < len(fix.video) {
				next = fix.video[j+1].ticks
			}
			return ticksToNs(next-fix.video[j].ticks, videoClockRate)
		}
		start := time.Now()
		videoAt := func(i int) int64 {
			loops, j := i/len(fix.video), i%len(fix.video)
			return int64(loops)*ticksToNs(mediaFixtureLoopTicks, videoClockRate) + ticksToNs(fix.video[j].ticks, videoClockRate)
		}
		audioAt := func(k int) int64 { return ticksToNs(int64(k)*opusFrameTicks, audioClockRate) }

		for vi, ai := 0, 0; ; {
			next := min(videoAt(vi), audioAt(ai))
			if wait := time.Until(start.Add(time.Duration(next))); wait > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
			} else if ctx.Err() != nil {
				return
			}

			if videoAt(vi) <= audioAt(ai) {
				j := vi % len(fix.video)
				_ = videoTrack.WriteSample(media.Sample{Data: frames[j], Duration: time.Duration(frameNs(j))})
				vi++
				continue
			}
			k := ai % len(fix.audio)
			_ = audioTrack.WriteSample(media.Sample{Data: fix.audio[k].data, Duration: 20 * time.Millisecond})
			ai++
		}
	}()
}

// e2eRecorder is a real recorder pointed at the stand-in.
type e2eRecorder struct {
	p      *Plugin
	rec    *clipRecorder
	own    *clipOwnSessions
	log    *recorderTestLog
	dir    string
	cfg    *configuration
	stream apiStream
	events chan recorderEvent
}

func newE2ERecorder(t *testing.T, origin *e2eOrigin) *e2eRecorder {
	t.Helper()
	p, _, _ := kvBackedPluginWithFaults(map[string][]byte{})
	p.httpClient = origin.srv.Client()

	// With "Log WHEP negotiation" on, so that every exchange is logged in
	// full, and the log can be looked at for the key.
	e := &e2eRecorder{
		p:      p,
		own:    &clipOwnSessions{},
		log:    &recorderTestLog{},
		dir:    filepath.Join(t.TempDir(), "ring", "e2e0123456789abc"),
		cfg:    &configuration{BroadcastBoxURL: origin.srv.URL, ClipLengthSeconds: "120", DebugLogSDP: true},
		events: make(chan recorderEvent, 1024),
		stream: apiStream{
			Handle: "e2e0123456789abc", StreamKey: e2eStreamKey, Title: "Bob plays Hytale", UserID: "bob-user-id",
			Username: "bob", LinkSource: "manual", AudioTracks: 1, VideoTracks: 1, Bitrate: 100_000,
			NowPlaying: &apiNowPlaying{Category: "game", Title: "Hytale"},
		},
	}
	// Loopback is let in here and nowhere else.
	peers := newClipPeerAPI()
	peers.build = func(allowed []string) (*webrtc.API, error) {
		require.Equal(t, []string{"127.0.0.1"}, allowed)
		return newClipWebRTCAPI(allowed, true)
	}
	e.rec = p.newClipRecorder(e.stream.Handle, e.dir, peers, e.own, func(event recorderEvent) {
		select {
		case e.events <- event:
		default:
		}
	})
	// The plugin's own log, kept to be read.
	e.rec.log = e.log
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), recorderCloseWait)
		defer cancel()
		if tail, held := e.rec.stop(ctx, false); held {
			tail.ring.remove()
		}
		e.log.requireNoKey(t, e2eStreamKey)
		e.requireNoKeyInPluginLog(t)
	})
	return e
}

// requireNoKeyInPluginLog checks what exchangeWHEP logged through the plugin
// API: every exchange, answers included, and never the key.
func (e *e2eRecorder) requireNoKeyInPluginLog(t *testing.T) {
	t.Helper()
	api, ok := e.p.API.(*plugintest.API)
	require.True(t, ok)
	exchanges := 0
	for _, call := range api.Calls {
		if !strings.HasPrefix(call.Method, "Log") {
			continue
		}
		if len(call.Arguments) > 0 && call.Arguments[0] == "WHEP exchange" {
			exchanges++
		}
		line := strings.ToLower(fmt.Sprint(call.Arguments...))
		require.NotContains(t, line, strings.ToLower(e2eStreamKey), call.Method)
	}
	require.Positive(t, exchanges, "every exchange is logged with the setting on")
}

// remoteSDP is the answer the recorder's session is using now.
func (e *e2eRecorder) remoteSDP(t *testing.T) string {
	t.Helper()
	e.rec.mu.Lock()
	s := e.rec.session
	e.rec.mu.Unlock()
	require.NotNil(t, s)
	s.mu.Lock()
	peer, ok := s.peer.(pionRecorderPeer)
	s.mu.Unlock()
	require.True(t, ok)
	answer := peer.pc.RemoteDescription()
	require.NotNil(t, answer)
	return answer.SDP
}

// next is the next event of kind (and, for changes, state), within d.
func (e *e2eRecorder) next(t *testing.T, kind recorderEventKind, state string, d time.Duration) recorderEvent {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case event := <-e.events:
			if event.kind == kind && (state == "" || event.state == state) {
				return event
			}
		case <-deadline:
			t.Fatalf("no %v %q event within %s; reason %q", kind, state, d, e.rec.reason())
			return recorderEvent{}
		}
	}
}

// waitHeld waits until the ring holds seconds of the stream.
func (e *e2eRecorder) waitHeld(t *testing.T, seconds int64) {
	t.Helper()
	require.Eventually(t, func() bool { return e.rec.heldSeconds() >= seconds },
		e2eWithin(time.Duration(seconds+5)*time.Second), 50*time.Millisecond, "the ring did not fill; reason %q", e.rec.reason())
}

// e2eFixtureIndex is where each payload of the fixture is, for payloads that
// appear once in it; -1 for any that repeat.
func e2eFixtureIndex[T any](items []T, payload func(T) []byte) map[string]int {
	index := make(map[string]int, len(items))
	for i, item := range items {
		key := string(payload(item))
		if _, seen := index[key]; seen {
			index[key] = -1
			continue
		}
		index[key] = i
	}
	return index
}

func e2eReadSample(t *testing.T, snap *rangeSnap, sample ringSample) []byte {
	t.Helper()
	buf := make([]byte, sample.size)
	_, err := snap.file(sample.seg).ReadAt(buf, int64(sample.off))
	require.NoError(t, err)
	return buf
}

// requireInSync checks a snapshot's start: the first audio sample is within
// 40 ms of the first frame, and the sound there is the sound that was sent
// with that picture, within 40 ms. Which frame and which packet were sent is
// read from their bytes, so nothing here trusts the recorder's clocks.
func requireInSync(t *testing.T, snap *rangeSnap) {
	t.Helper()
	fix := loadMediaFixture(t)
	require.NotEmpty(t, snap.video)
	require.NotEmpty(t, snap.audio)

	start := snap.video[0].wall
	skew := time.Duration(snap.audio[0].wall - start)
	require.LessOrEqual(t, skew.Abs(), 40*time.Millisecond, "the draft's audio starts %s from its picture", skew)

	frames := e2eFixtureIndex(fix.video, func(f mediaFixtureFrame) []byte { return f.avcc })
	packets := e2eFixtureIndex(fix.audio, func(p mediaFixturePacket) []byte { return p.data })

	j := frames[string(e2eReadSample(t, snap, snap.video[0]))]
	require.GreaterOrEqual(t, j, 0, "the first frame is a frame of the fixture")
	for _, sample := range snap.audio[:min(len(snap.audio), 100)] {
		k, ok := packets[string(e2eReadSample(t, snap, sample))]
		if !ok || k < 0 {
			continue
		}
		loop := ticksToNs(mediaFixtureLoopTicks, videoClockRate)
		sent := ticksToNs(int64(k)*opusFrameTicks, audioClockRate) - ticksToNs(fix.video[j].ticks, videoClockRate)
		got := sample.wall - start
		loops := math.Round(float64(got-sent) / float64(loop))
		off := time.Duration(got - sent - int64(loops)*loop)
		require.LessOrEqual(t, off.Abs(), 40*time.Millisecond, "the sound is %s out from the picture", off)
		t.Logf("start skew %s; the sound is %s from the picture it was sent with", skew, off)
		return
	}
	t.Fatal("no audio packet of the fixture in the first two seconds")
}

func TestE2ERecorderOverLoopback(t *testing.T) {
	requireE2E(t)
	origin := newE2EOrigin(t)
	e := newE2ERecorder(t, origin)

	began := time.Now()
	e.rec.start(context.Background(), e.stream, e.cfg)
	ready := e.next(t, recorderChanged, clipStateReady, e2eWithin(5*time.Second))
	t.Logf("ready after %s", time.Since(began))
	require.False(t, ready.clipFrom.IsZero())
	require.WithinDuration(t, time.Now(), ready.clipFrom, 5*time.Second)

	// The session Broadcast Box named is ours, so no poll counts it.
	sessions := origin.sessions()
	require.Len(t, sessions, 1)
	require.True(t, e.own.has(sessions[0], time.Now()))

	// Broadcast Box named the stream after its key; the answer the session
	// was opened with names it only as VillaNET does.
	answer := e.remoteSDP(t)
	require.NotContains(t, strings.ToLower(answer), strings.ToLower(e2eStreamKey))
	require.Contains(t, answer, "a=msid:"+answerStreamID+" ")

	e.waitHeld(t, 7)
	ring := e.rec.live().ring
	require.NotNil(t, ring)
	status := ring.status()
	require.Equal(t, 320, status.width)
	require.Equal(t, 180, status.height)
	require.Equal(t, codecString(loadMediaFixture(t).sps, true), status.codecs)

	at := time.Now()
	snap, err := ring.snapshotRange(at, e.cfg.ringWindow())
	require.NoError(t, err)
	requireInSync(t, snap)
	snap.release()

	draftDir := t.TempDir()
	result, err := exportDraft(context.Background(), ring, at, e.cfg.ringWindow(), draftDir, "e2e0000000000001")
	require.NoError(t, err)
	require.GreaterOrEqual(t, result.durationMs, int64(clipMinMS))
	require.True(t, result.partial, "the epoch began inside the window")
	t.Logf("draft: %d ms, %d bytes, %s", result.durationMs, result.bytes, result.codecs)

	f, err := os.Open(result.path)
	require.NoError(t, err)
	var pres pmp4.Presentation
	require.NoError(t, pres.Unmarshal(f))
	require.Len(t, pres.Tracks, 2)
	require.IsType(t, &codecs.H264{}, pres.Tracks[0].Codec)
	require.IsType(t, &codecs.Opus{}, pres.Tracks[1].Codec)
	require.False(t, pres.Tracks[0].Samples[0].IsNonSyncSample, "a draft starts on a keyframe")
	require.Greater(t, len(pres.Tracks[1].Samples), 200)
	require.NoError(t, f.Close())

	if ffmpeg, err := exec.LookPath("ffmpeg"); err == nil {
		out, err := exec.Command(ffmpeg, "-v", "error", "-xerror", "-i", result.path, "-f", "null", "-").CombinedOutput()
		require.NoError(t, err, string(out))
		require.Empty(t, strings.TrimSpace(string(out)))
	} else {
		t.Log("ffmpeg is not installed; the draft was checked with pmp4 only")
	}
	if keep := os.Getenv("VILLANET_E2E_KEEP"); keep != "" {
		data, err := os.ReadFile(result.path)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(keep, "e2e-draft.mp4"), data, 0o600))
	}

	// Stopping keeps the stream's end, and the session stays ours for its
	// minute.
	ctx, cancel := context.WithTimeout(context.Background(), recorderCloseWait)
	defer cancel()
	tail, held := e.rec.stop(ctx, true)
	require.True(t, held)
	defer tail.ring.remove()
	require.Equal(t, clipTailEnded, tail.reason)
	require.GreaterOrEqual(t, tail.ring.heldMs(), int64(7000))
	require.Equal(t, e2eStreamKey, tail.stream.StreamKey)
	stoppedAt := time.Now()
	require.True(t, e.own.has(sessions[0], stoppedAt.Add(recorderSessionLinger-5*time.Second)))
	require.False(t, e.own.has(sessions[0], stoppedAt.Add(recorderSessionLinger+5*time.Second)))
	require.Equal(t, 1, e.log.count("info", "is recording"))
}

func TestE2ERecorderReconnectsWhenAudioStops(t *testing.T) {
	requireE2E(t)
	origin := newE2EOrigin(t)
	e := newE2ERecorder(t, origin)

	e.rec.start(context.Background(), e.stream, e.cfg)
	first := e.next(t, recorderChanged, clipStateReady, e2eWithin(5*time.Second))
	e.waitHeld(t, 6)
	firstEpoch := e.rec.live().ring.status().epoch

	// Broadcast Box stops the sound of this session while the picture flows.
	origin.mute(0)
	muted := time.Now()

	kept := e.next(t, recorderTailKept, "", e2eWithin(8*time.Second))
	t.Logf("the session was dropped %s after the sound stopped", time.Since(muted))
	tail := kept.tail
	require.NotNil(t, tail)
	defer tail.ring.remove()
	require.Equal(t, clipTailDropped, tail.reason)
	require.Equal(t, firstEpoch, tail.ring.status().epoch)
	require.True(t, tail.ring.status().frozen)
	require.GreaterOrEqual(t, tail.ring.heldMs(), int64(tailMinHeld/time.Millisecond))
	require.Equal(t, "Bob plays Hytale", tail.stream.Title)
	require.GreaterOrEqual(t, time.Since(muted), recorderAudioDiedAfter)

	// The recorder comes back in a new session, with a new epoch whose
	// clipFrom is later.
	var again recorderEvent
	deadline := time.Now().Add(e2eWithin(12 * time.Second))
	for {
		again = e.next(t, recorderChanged, clipStateReady, time.Until(deadline))
		if again.clipFrom.After(first.clipFrom) {
			break
		}
	}
	require.True(t, again.clipFrom.After(muted))
	live := e.rec.live().ring
	require.NotNil(t, live)
	require.NotEqual(t, firstEpoch, live.status().epoch)
	require.Len(t, origin.sessions(), 2)
	require.Equal(t, 1, e.log.count("warn", "reconnecting"))
	require.Contains(t, e.log.text(), "the sound stopped")
	require.Equal(t, 1, e.log.count("info", "end is held"))
}

// Broadcast Box sends a new session no picture until the publisher's next
// keyframe, and OBS's default keyframe interval puts that ten seconds or more
// after the session opens: here twelve. The recorder waits for it, in the one
// session, and records from it.
func TestE2ERecorderWaitsForALateFirstKeyframe(t *testing.T) {
	requireE2E(t)
	const hold = 12 * time.Second
	origin := newE2EOriginHolding(t, hold)
	e := newE2ERecorder(t, origin)

	began := time.Now()
	e.rec.start(context.Background(), e.stream, e.cfg)
	ready := e.next(t, recorderChanged, clipStateReady, e2eWithin(hold+5*time.Second))
	t.Logf("ready after %s", time.Since(began))
	require.GreaterOrEqual(t, time.Since(began), hold)
	require.False(t, ready.clipFrom.Before(began.Add(hold)), "the epoch starts at the first keyframe sent")
	require.Len(t, origin.sessions(), 1, "never reconnected")
	require.Zero(t, e.log.count("warn", "reconnecting"))
	require.Zero(t, e.log.count("warn", "could not connect"))
}
