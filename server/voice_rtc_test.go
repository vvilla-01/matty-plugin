package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Talking from Matty end to end: a pion peer in the test plays the phone,
// against the plugin's own listener on a free port of 127.0.0.1 and a voice
// server that passes voice on (relayMumble), through ServeHTTP as Mattermost
// would call it.

// ---------------------------------------------------------------- the phone

// testVoicePhone is a phone as Matty makes its voice connection: its
// microphone sent with the audio level, some sections it receives on, and
// the data channel "voice".
type testVoicePhone struct {
	t         *testing.T
	pc        *webrtc.PeerConnection
	mic       *webrtc.TrackLocalStaticRTP
	micSender *webrtc.RTPSender

	connected     chan struct{}
	connectedOnce sync.Once

	// Closed once its data channel "voice" has closed.
	channelClosed chan struct{}

	lock      sync.Mutex
	messages  []map[string]json.RawMessage
	heard     map[string][]*rtp.Packet
	levelID   uint8
	sequence  uint16
	timestamp uint32
}

func newTestVoicePhone(t *testing.T, receive int) *testVoicePhone {
	t.Helper()

	media := &webrtc.MediaEngine{}
	require.NoError(t, media.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: voiceOpusCapability, PayloadType: voiceOpusPayloadType}, webrtc.RTPCodecTypeAudio))
	require.NoError(t, media.RegisterHeaderExtension(webrtc.RTPHeaderExtensionCapability{URI: sdp.AudioLevelURI}, webrtc.RTPCodecTypeAudio))
	registry := &interceptor.Registry{}
	require.NoError(t, webrtc.ConfigureRTCPReports(registry))

	settings := webrtc.SettingEngine{LoggerFactory: voiceSilentLogs}
	settings.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	settings.SetIncludeLoopbackCandidate(true)
	settings.SetIPFilter(func(ip net.IP) bool { return ip.IsLoopback() })
	api := webrtc.NewAPI(webrtc.WithMediaEngine(media), webrtc.WithInterceptorRegistry(registry), webrtc.WithSettingEngine(settings))

	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = pc.Close() })

	phone := &testVoicePhone{t: t, pc: pc, connected: make(chan struct{}), channelClosed: make(chan struct{}), heard: map[string][]*rtp.Packet{}, sequence: 100, timestamp: 5000}

	phone.mic, err = webrtc.NewTrackLocalStaticRTP(voiceOpusCapability, "microphone", "phone")
	require.NoError(t, err)
	transceiver, err := pc.AddTransceiverFromTrack(phone.mic, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly})
	require.NoError(t, err)
	phone.micSender = transceiver.Sender()
	go voiceDrainRTCP(phone.micSender)

	for range receive {
		_, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly})
		require.NoError(t, err)
	}

	channel, err := pc.CreateDataChannel(voiceDataChannelLabel, nil)
	require.NoError(t, err)
	channel.OnClose(func() { close(phone.channelClosed) })
	channel.OnMessage(func(message webrtc.DataChannelMessage) {
		var decoded map[string]json.RawMessage
		if json.Unmarshal(message.Data, &decoded) != nil {
			return
		}
		phone.lock.Lock()
		phone.messages = append(phone.messages, decoded)
		phone.lock.Unlock()
	})

	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		go func() {
			for {
				if _, _, err := receiver.ReadRTCP(); err != nil {
					return
				}
			}
		}()
		for {
			packet, _, err := track.ReadRTP()
			if err != nil {
				return
			}
			phone.lock.Lock()
			phone.heard[track.ID()] = append(phone.heard[track.ID()], packet)
			phone.lock.Unlock()
		}
	})

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateConnected {
			phone.connectedOnce.Do(func() { close(phone.connected) })
		}
	})

	return phone
}

// offer is the phone's offer, with every candidate.
func (p *testVoicePhone) offer() string {
	p.t.Helper()

	offer, err := p.pc.CreateOffer(nil)
	require.NoError(p.t, err)
	gathered := webrtc.GatheringCompletePromise(p.pc)
	require.NoError(p.t, p.pc.SetLocalDescription(offer))
	<-gathered

	return p.pc.LocalDescription().SDP
}

// accept takes the server's answer and waits until the phone is connected
// and greeted.
func (p *testVoicePhone) accept(answer string) {
	p.t.Helper()

	require.NoError(p.t, p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer}))
	for _, extension := range p.micSender.GetParameters().HeaderExtensions {
		if extension.URI == sdp.AudioLevelURI {
			p.levelID = uint8(extension.ID)
		}
	}
	require.NotZero(p.t, p.levelID, "the audio level is negotiated")

	select {
	case <-p.connected:
	case <-time.After(15 * time.Second):
		p.t.Fatal("the phone never connected")
	}

	require.Eventually(p.t, func() bool { _, ok := p.last("self"); return ok }, 10*time.Second, 10*time.Millisecond, "greeted with its state")
}

// speak sends n 20 ms Opus packets at a level, paced as a phone sends them.
func (p *testVoicePhone) speak(n int, level uint8) {
	p.t.Helper()

	for range n {
		extension, err := rtp.AudioLevelExtension{Level: level, Voice: level < 127}.Marshal()
		require.NoError(p.t, err)

		p.lock.Lock()
		packet := &rtp.Packet{
			Header:  rtp.Header{Version: 2, PayloadType: voiceOpusPayloadType, SequenceNumber: p.sequence, Timestamp: p.timestamp},
			Payload: []byte{31 << 3, 0x5a, 0x5a},
		}
		p.sequence++
		p.timestamp += 960
		p.lock.Unlock()

		require.NoError(p.t, packet.Header.SetExtension(p.levelID, extension))
		require.NoError(p.t, p.mic.WriteRTP(packet))
		time.Sleep(20 * time.Millisecond)
	}
}

// last is the latest message of a kind ("talking", "self", "end").
func (p *testVoicePhone) last(kind string) (json.RawMessage, bool) {
	p.lock.Lock()
	defer p.lock.Unlock()

	for i := len(p.messages) - 1; i >= 0; i-- {
		if raw, ok := p.messages[i][kind]; ok {
			return raw, true
		}
	}

	return nil, false
}

func (p *testVoicePhone) talking() []uint32 {
	raw, ok := p.last("talking")
	if !ok {
		return nil
	}
	var set []uint32
	_ = json.Unmarshal(raw, &set)

	return set
}

// sawTalking is whether any talking set the phone was sent passes a test.
func (p *testVoicePhone) sawTalking(test func([]uint32) bool) bool {
	p.lock.Lock()
	defer p.lock.Unlock()

	for _, message := range p.messages {
		if raw, ok := message["talking"]; ok {
			var set []uint32
			if json.Unmarshal(raw, &set) == nil && test(set) {
				return true
			}
		}
	}

	return false
}

func (p *testVoicePhone) self() voiceSelfState {
	raw, _ := p.last("self")
	var state voiceSelfState
	_ = json.Unmarshal(raw, &state)

	return state
}

func (p *testVoicePhone) ended() (voiceEndSignal, bool) {
	p.lock.Lock()
	defer p.lock.Unlock()

	for _, message := range p.messages {
		if raw, ok := message["end"]; ok {
			signal := voiceEndSignal{}
			_ = json.Unmarshal(raw, &signal.End)
			_ = json.Unmarshal(message["reason"], &signal.Reason)
			return signal, true
		}
	}

	return voiceEndSignal{}, false
}

func (p *testVoicePhone) heardOn(track string) []*rtp.Packet {
	p.lock.Lock()
	defer p.lock.Unlock()

	return append([]*rtp.Packet(nil), p.heard[track]...)
}

func (p *testVoicePhone) tracksHeard() map[string]int {
	p.lock.Lock()
	defer p.lock.Unlock()

	counts := map[string]int{}
	for track, packets := range p.heard {
		counts[track] = len(packets)
	}

	return counts
}

// ---------------------------------------------------------------- the scene

// voiceScene is the access scene with voice switched on, talk on, the
// listener up on a free port of 127.0.0.1, the roster connection standing
// for connected with the relay's channels, and the relay as the voice
// server.
type voiceScene struct {
	*accessScene
	t     *testing.T
	relay *relayMumble

	// The voice port the scene's settings give, free when the scene began.
	talkPort int
}

// The relay's tree, as the roster connection reads it.
func relayVoiceChannels() []voiceChannel {
	root, gaming := uint32(0), uint32(2)

	return []voiceChannel{
		{ID: 0, Name: "Test"},
		{ID: 1, Name: "Lobby", Parent: &root, Depth: 1},
		{ID: 2, Name: "Gaming", Parent: &root, Depth: 1},
		{ID: 4, Name: "Duo", Parent: &gaming, Depth: 2, Max: 2},
		{ID: 3, Name: "AFK", Parent: &root, Depth: 1},
	}
}

func newVoiceScene(t *testing.T) *voiceScene {
	t.Helper()

	s := &voiceScene{accessScene: newAccessScene(t), t: t, relay: newRelayMumble(t), talkPort: freeVoicePort(t)}
	s.p.voiceTalkLookup = func(context.Context, string) ([]net.IPAddr, error) {
		return nil, errors.New("no name server in tests")
	}
	s.p.setVoice(voiceState{connected: true, channels: relayVoiceChannels()})
	s.configure(nil)
	t.Cleanup(s.p.stopVoiceTalk)

	return s
}

// freeVoicePort is a UDP port nothing holds just now.
func freeVoicePort(t *testing.T) int {
	t.Helper()

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	require.NoError(t, err)
	port := conn.LocalAddr().(*net.UDPAddr).Port
	require.NoError(t, conn.Close())

	return port
}

// configure sets talk's settings, edited, and restarts talk as a save does,
// waiting until the listener has settled.
func (s *voiceScene) configure(edit func(cfg *configuration)) {
	s.t.Helper()

	cfg := s.p.getConfiguration().Clone()
	cfg.EnableVoice, cfg.EnableVoiceTalk = true, true
	cfg.MumbleServer = s.relay.address()
	cfg.VoiceTalkAddress = "127.0.0.1"
	cfg.VoiceTalkPort = s.talkPort
	if edit != nil {
		edit(cfg)
	}
	cfg.normalize()
	s.p.setConfiguration(cfg)

	passes := s.p.voiceTalkPasses.Load()
	s.p.restartVoiceTalk()

	if cfg.voiceTalkWanted() {
		require.Eventually(s.t, func() bool { return s.p.voiceTalkPasses.Load() > passes }, 10*time.Second, 5*time.Millisecond)
	}
}

func (s *voiceScene) port() int {
	return s.p.voiceListener().state().port
}

// call is a request as Matty makes it, with the header a script sends.
func (s *voiceScene) call(target, userID string, body any) *httptest.ResponseRecorder {
	s.t.Helper()

	text := ""
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(s.t, err)
		text = string(encoded)
	}

	return s.serve(http.MethodPost, target, userID, text, map[string]string{"X-Requested-With": "XMLHttpRequest"})
}

// join is a phone joining a channel, answered.
func (s *voiceScene) join(phone *testVoicePhone, userID string, channel uint32, muted bool) voiceJoinResponse {
	s.t.Helper()

	recorder := s.call("/api/v1/voice/join", userID, map[string]any{"channel": channel, "muted": muted, "deafened": false, "sdp": phone.offer()})
	require.Equal(s.t, http.StatusOK, recorder.Code, recorder.Body.String())

	var answer voiceJoinResponse
	require.NoError(s.t, json.Unmarshal(recorder.Body.Bytes(), &answer))
	phone.accept(answer.SDP)

	return answer
}

func (s *voiceScene) word() voiceWord {
	s.t.Helper()

	recorder := s.serve(http.MethodGet, "/api/v1/access", sceneLetIn, "", nil)
	require.Equal(s.t, http.StatusOK, recorder.Code)

	var access accessResponse
	require.NoError(s.t, json.Unmarshal(recorder.Body.Bytes(), &access))
	require.NotNil(s.t, access.Voice)

	return *access.Voice
}

// ---------------------------------------------------------------- the tests

func TestVoiceTalkFromAPhone(t *testing.T) {
	s := newVoiceScene(t)
	require.Equal(t, voiceWord{Talk: voiceTalkOn}, s.word())

	dave := newTestVoicePhone(t, 4)
	answer := s.join(dave, sceneLinked, 1, false)
	assert.Equal(t, "dave (Matty)", answer.Name)
	assert.EqualValues(t, 1, answer.Channel)
	assert.NotZero(t, answer.Session)
	assert.Equal(t, voiceSelfState{Channel: 1}, dave.self())

	// The answer's candidates are the address phones were given, on the
	// port.
	assert.Contains(t, answer.SDP, "127.0.0.1 "+itoa(s.port())+" typ host")

	// The roster is told the session is Dave's.
	who, ok := s.p.voicePhones.userOf(answer.Session)
	require.True(t, ok)
	assert.Equal(t, sceneLinked, who)

	// Up: quiet packets go nowhere; loud ones make Dave talk, with the two
	// before them, and every one reaches the voice server, counted in 10 ms
	// frames.
	dave.speak(3, 90)
	dave.speak(5, 30)
	require.Eventually(t, func() bool { return len(s.relay.heardFrom(answer.Session)) >= 7 }, 5*time.Second, 5*time.Millisecond)
	heard := s.relay.heardFrom(answer.Session)
	assert.Equal(t, []byte{31 << 3, 0x5a, 0x5a}, heard[0].payload)
	for i := 1; i < 7; i++ {
		assert.Equal(t, heard[i-1].sequence+2, heard[i].sequence, "20 ms apart")
	}
	require.Eventually(t, func() bool {
		return dave.sawTalking(func(talking []uint32) bool { return len(talking) == 1 && talking[0] == answer.Session })
	}, 5*time.Second, 5*time.Millisecond, "the phone hears that its own voice goes up")

	// The hangover ends it with an empty last frame.
	require.Eventually(t, func() bool {
		heard := s.relay.heardFrom(answer.Session)
		last := heard[len(heard)-1]
		return last.final && len(last.payload) == 0
	}, 5*time.Second, 5*time.Millisecond)
	require.Eventually(t, func() bool { return dave.talking() != nil && len(dave.talking()) == 0 }, 5*time.Second, 5*time.Millisecond)

	// Down: two people talking in the Lobby come on two tracks, each spell
	// marked, timestamps 480 a frame.
	carol := s.relay.add("carol", 1)
	bob := s.relay.add("bob", 1)
	for i := range 4 {
		s.relay.speak(carol, int64(40+i*2), spoken(1), false)
		s.relay.speak(bob, int64(10+i*2), spoken(2), false)
		time.Sleep(20 * time.Millisecond)
	}
	require.Eventually(t, func() bool {
		total := 0
		for _, n := range dave.tracksHeard() {
			total += n
		}
		return total == 8 && len(dave.tracksHeard()) == 2
	}, 10*time.Second, 5*time.Millisecond)
	for track := range dave.tracksHeard() {
		packets := dave.heardOn(track)
		require.Len(t, packets, 4, track)
		assert.True(t, packets[0].Marker, "a spell starts marked")
		speaker := packets[0].Payload[1]
		for i := 1; i < 4; i++ {
			assert.False(t, packets[i].Marker)
			assert.Equal(t, packets[i-1].SequenceNumber+1, packets[i].SequenceNumber)
			assert.Equal(t, packets[i-1].Timestamp+960, packets[i].Timestamp)
			assert.Equal(t, speaker, packets[i].Payload[1], "one person a track")
		}
		assert.True(t, strings.HasPrefix(track, "voice-"), track)
	}
	require.Eventually(t, func() bool {
		return dave.sawTalking(func(talking []uint32) bool { return len(talking) == 2 })
	}, 5*time.Second, 5*time.Millisecond, "both heard as talking")

	// Moving: answered with the channel, and the phone told.
	moved := s.call("/api/v1/voice/move", sceneLinked, map[string]any{"channel": 2})
	require.Equal(t, http.StatusOK, moved.Code, moved.Body.String())
	assert.JSONEq(t, `{"channel":2}`, moved.Body.String())
	require.Eventually(t, func() bool { return dave.self().Channel == 2 }, 5*time.Second, 5*time.Millisecond)

	// Muting: the voice server sees it, and nothing goes up.
	before := len(s.relay.heardFrom(answer.Session))
	muted := s.call("/api/v1/voice/state", sceneLinked, map[string]any{"muted": true, "deafened": false})
	require.Equal(t, http.StatusOK, muted.Code, muted.Body.String())
	assert.JSONEq(t, `{"muted":true,"deafened":false}`, muted.Body.String())
	require.Eventually(t, func() bool { user, _ := s.relay.user(answer.Session); return user.selfMute }, 5*time.Second, 5*time.Millisecond)
	dave.speak(5, 20)
	time.Sleep(100 * time.Millisecond)
	assert.Len(t, s.relay.heardFrom(answer.Session), before)

	// Deafened is muted too.
	deaf := s.call("/api/v1/voice/state", sceneLinked, map[string]any{"muted": false, "deafened": true})
	require.Equal(t, http.StatusOK, deaf.Code, deaf.Body.String())
	assert.JSONEq(t, `{"muted":true,"deafened":true}`, deaf.Body.String())

	// Leaving: the session and its Mumble connection go; a second leave
	// finds nothing.
	left := s.call("/api/v1/voice/leave", sceneLinked, nil)
	require.Equal(t, http.StatusOK, left.Code)
	assert.JSONEq(t, `{"left":true}`, left.Body.String())
	require.Eventually(t, func() bool { _, there := s.relay.user(answer.Session); return !there }, 5*time.Second, 5*time.Millisecond)
	assert.Zero(t, s.p.voiceTalk().count())
	_, told := dave.ended()
	assert.False(t, told, "a phone that left is told nothing")

	again := s.call("/api/v1/voice/leave", sceneLinked, nil)
	assert.JSONEq(t, `{"left":false}`, again.Body.String())
	require.Eventually(t, func() bool { return s.p.voiceListener().state().up && s.p.voiceListener().peerCount() == 0 }, 5*time.Second, 5*time.Millisecond)
}

func TestVoiceTalkAJoinReplacesAndAPhoneComesBack(t *testing.T) {
	s := newVoiceScene(t)

	first := newTestVoicePhone(t, 4)
	answer := s.join(first, sceneLinked, 1, false)
	dials := s.relay.dialCount()

	// Another join from another phone takes the session over: the first
	// phone is told, and no new connection is made to the voice server.
	second := newTestVoicePhone(t, 4)
	again := s.join(second, sceneLinked, 1, false)
	assert.Equal(t, answer.Session, again.Session)
	require.Eventually(t, func() bool { _, ok := first.ended(); return ok }, 5*time.Second, 5*time.Millisecond)
	ending, _ := first.ended()
	assert.Equal(t, voiceEndSignal{End: voiceEndReplaced, Reason: voiceReplacedText}, ending)
	assert.Equal(t, dials, s.relay.dialCount())

	// The second phone goes: the session waits for it, deafened on the
	// voice server.
	require.NoError(t, second.pc.Close())
	require.Eventually(t, func() bool { user, _ := s.relay.user(answer.Session); return user.selfDeaf }, 20*time.Second, 10*time.Millisecond)
	assert.Equal(t, 1, s.p.voiceTalk().count())

	// It comes back within the wait, muted this time, on the session it had.
	third := newTestVoicePhone(t, 2)
	back := s.join(third, sceneLinked, 2, true)
	assert.Equal(t, answer.Session, back.Session)
	assert.EqualValues(t, 2, back.Channel)
	assert.Equal(t, dials, s.relay.dialCount())
	require.Eventually(t, func() bool {
		user, _ := s.relay.user(answer.Session)
		return !user.selfDeaf && user.selfMute && user.channel == 2
	}, 5*time.Second, 5*time.Millisecond)
}

func TestVoiceTalkAnswersFourTracksAtMost(t *testing.T) {
	s := newVoiceScene(t)

	phone := newTestVoicePhone(t, 6)
	answer := s.join(phone, sceneLinked, 0, false)
	assert.EqualValues(t, 0, answer.Channel, "a join to the root is answered at once")

	var description sdp.SessionDescription
	require.NoError(t, description.UnmarshalString(answer.SDP))
	var directions []string
	for _, media := range description.MediaDescriptions {
		if media.MediaName.Media == "audio" {
			if media.MediaName.Port.Value == 0 {
				directions = append(directions, "rejected")
				continue
			}
			directions = append(directions, voiceSectionDirection(media))
		}
	}
	require.Len(t, directions, 7)
	assert.Equal(t, "recvonly", directions[0], "the microphone is received")
	assert.Equal(t, []string{"sendonly", "sendonly", "sendonly", "sendonly"}, directions[1:5])
	for _, direction := range directions[5:] {
		assert.Contains(t, []string{"inactive", "rejected"}, direction)
	}

	session := s.p.voiceTalk().of(sceneLinked)
	require.NotNil(t, session)
	session.lock.Lock()
	peer := session.peer
	session.lock.Unlock()
	assert.Equal(t, 4, peer.slots())
}

func TestVoiceTalkRefusals(t *testing.T) {
	s := newVoiceScene(t)
	offer := newTestVoicePhone(t, 4).offer()
	join := func(userID string, channel any) *httptest.ResponseRecorder {
		return s.call("/api/v1/voice/join", userID, map[string]any{"channel": channel, "sdp": offer})
	}

	// Not from a script, and not for somebody locked.
	plain := s.serve(http.MethodPost, "/api/v1/voice/join", sceneLinked, `{"channel":1}`, nil)
	requireRefused(t, plain, http.StatusForbidden, codeForbidden, "no X-Requested-With")
	requireRefused(t, join(sceneLocked, 1), http.StatusForbidden, accessCodeLocked, "locked")

	// Bad requests.
	requireRefused(t, s.call("/api/v1/voice/join", sceneLinked, map[string]any{"sdp": offer}), http.StatusBadRequest, codeBadRequest, "no channel")
	requireRefused(t, s.call("/api/v1/voice/join", sceneLinked, map[string]any{"channel": 1, "sdp": "v=0"}), http.StatusBadRequest, codeBadRequest, "not an offer")
	video := strings.Replace(offer, "m=audio", "m=video", 1)
	requireRefused(t, s.call("/api/v1/voice/join", sceneLinked, map[string]any{"channel": 1, "sdp": video}), http.StatusBadRequest, codeBadRequest, "video")
	requireRefused(t, s.serve(http.MethodPost, "/api/v1/voice/join", sceneLinked, "{", map[string]string{"X-Requested-With": "XMLHttpRequest"}), http.StatusBadRequest, codeBadRequest, "not JSON")
	big := s.serve(http.MethodPost, "/api/v1/voice/move", sceneLinked, `{"channel":1,"x":"`+strings.Repeat("a", maxVoiceBody)+`"}`, map[string]string{"X-Requested-With": "XMLHttpRequest"})
	requireRefused(t, big, http.StatusRequestEntityTooLarge, codeTooLarge, "too large")

	// No such channel.
	notFound := join(sceneLetIn, 9)
	requireRefused(t, notFound, http.StatusNotFound, codeNotFound, "no channel 9")
	contractFixture(t, "refusal-voice-not-found.json", notFound.Body.Bytes())

	// Moving and muting without a session.
	requireRefused(t, s.call("/api/v1/voice/move", sceneLetIn, map[string]any{"channel": 1}), http.StatusConflict, codeConflict, "not in voice")
	requireRefused(t, s.call("/api/v1/voice/state", sceneLetIn, map[string]any{"muted": true}), http.StatusConflict, codeConflict, "not in voice")

	// The voice server keeps somebody out of a channel, or it is full.
	s.relay.set(func(f *relayMumble) { f.deny[3] = "" })
	denied := join(sceneLetIn, 3)
	requireRefused(t, denied, http.StatusForbidden, codeVoiceDenied, "denied")
	assert.Equal(t, voiceDeniedText, codedError(t, denied).Error)
	contractFixture(t, "refusal-voice-denied.json", denied.Body.Bytes())
	refused, at := s.p.voiceTalk().lastRefusal()
	assert.Equal(t, voiceDeniedText, refused)
	assert.False(t, at.IsZero())

	s.relay.add("alice", 4)
	s.relay.add("carol", 4)
	full := join(sceneLetIn, 4)
	requireRefused(t, full, http.StatusForbidden, codeVoiceDenied, "full")
	assert.Equal(t, voiceChannelFullText, codedError(t, full).Error)
	assert.Zero(t, s.p.voiceTalk().count(), "a join that fails leaves nothing behind")

	// The voice server refuses the connection.
	s.relay.set(func(f *relayMumble) { f.password = "secret" })
	refusedJoin := join(sceneLinkedAdmin, 1)
	requireRefused(t, refusedJoin, http.StatusConflict, codeVoiceRefused, "password")
	assert.Equal(t, voiceRejectedPasswordText, codedError(t, refusedJoin).Error)
	contractFixture(t, "refusal-voice-refused.json", refusedJoin.Body.Bytes())
	s.relay.set(func(f *relayMumble) { f.password = "" })

	// The budget of connections to the voice server, used up.
	for range voiceBudgetDials {
		s.p.voiceTalk().noteRosterDial()
	}
	budget := join(sceneLetInAdmin, 1)
	requireRefused(t, budget, http.StatusServiceUnavailable, codeUnavailable, "budget")
	var wait errorResponse
	require.NoError(t, json.Unmarshal(budget.Body.Bytes(), &wait))
	assert.Equal(t, voiceBudgetText, wait.Error)
	assert.Positive(t, wait.RetryAfterMs)
	wait.RetryAfterMs = 120000
	encoded, err := json.Marshal(wait)
	require.NoError(t, err)
	contractFixture(t, "refusal-voice-budget.json", encoded)

	// Six joins a minute a person.
	for range 3 {
		join(sceneLetIn, 9)
	}
	requireRefused(t, join(sceneLetIn, 9), http.StatusTooManyRequests, codeRateLimited, "seventh join")

	// The voice server not connected.
	s.p.setVoice(voiceState{})
	notConnected := join(sceneLinked, 1)
	requireRefused(t, notConnected, http.StatusServiceUnavailable, codeUnavailable, "not connected")
	assert.Equal(t, voiceNotConnectedText, codedError(t, notConnected).Error)
	s.p.setVoice(voiceState{connected: true, channels: relayVoiceChannels()})

	// Talk switched off: refused, but leaving still answers.
	s.configure(func(cfg *configuration) { cfg.EnableVoiceTalk = false })
	off := join(sceneLinked, 1)
	requireRefused(t, off, http.StatusServiceUnavailable, codeUnavailable, "off")
	assert.Equal(t, voiceTalkSwitchedOffText, codedError(t, off).Error)
	contractFixture(t, "refusal-voice-off.json", off.Body.Bytes())
	requireRefused(t, s.call("/api/v1/voice/move", sceneLinked, map[string]any{"channel": 1}), http.StatusServiceUnavailable, codeUnavailable, "off")
	left := s.call("/api/v1/voice/leave", sceneLocked, nil)
	assert.Equal(t, http.StatusOK, left.Code, "leaving is open to everybody, whatever the switch says")
}

func TestVoiceTalkWord(t *testing.T) {
	s := newVoiceScene(t)
	assert.Equal(t, voiceWord{Talk: voiceTalkOn}, s.word())

	s.p.setVoice(voiceState{})
	assert.Equal(t, voiceWord{Talk: voiceTalkOff, Reason: voiceNotConnectedText}, s.word())
	s.p.setVoice(voiceState{connected: true, channels: relayVoiceChannels()})

	// An address that is IPv6: down, and the admins told why.
	s.configure(func(cfg *configuration) { cfg.VoiceTalkAddress = "2001:db8::7" })
	assert.Equal(t, voiceWord{Talk: voiceTalkOff, Reason: voiceTalkBrokenText}, s.word())
	assert.Equal(t, voiceTalkIPv6Text("2001:db8::7"), s.p.voiceTalkAlertFor(s.p.getConfiguration()))

	// A host name that leads nowhere.
	s.configure(func(cfg *configuration) { cfg.VoiceTalkAddress = "voice.example.invalid" })
	assert.Equal(t, voiceTalkUnresolvedText("voice.example.invalid"), s.p.voiceListener().state().problem)

	// No address and no Site URL.
	s.configure(func(cfg *configuration) { cfg.VoiceTalkAddress = "" })
	assert.Equal(t, voiceTalkNoAddressText, s.p.voiceListener().state().problem)

	// A port outside 1024 to 65535: kept as set, and talk off, saying why,
	// to the app, on the card and in the admins' alert.
	s.configure(func(cfg *configuration) { cfg.VoiceTalkPort = 443 })
	assert.Equal(t, 443, s.p.getConfiguration().VoiceTalkPort, "not replaced")
	assert.Equal(t, voiceWord{Talk: voiceTalkOff, Reason: "The voice port must be between 1024 and 65535."}, s.word())
	assert.Equal(t, "The voice port must be between 1024 and 65535.", s.p.voiceTalkAlertFor(s.p.getConfiguration()))
	card := s.p.voiceAdminNow()
	assert.Equal(t, 443, card.Port)
	assert.False(t, card.Open)
	assert.Equal(t, "The voice port must be between 1024 and 65535.", card.Problem)

	// A port taken by something else.
	taken, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 0})
	require.NoError(t, err)
	defer func() { _ = taken.Close() }()
	port := taken.LocalAddr().(*net.UDPAddr).Port
	s.configure(func(cfg *configuration) { cfg.VoiceTalkPort = port })
	assert.Contains(t, s.p.voiceListener().state().problem, "The voice port "+itoa(port)+" (UDP) could not be opened: ")

	// Switched off.
	s.configure(func(cfg *configuration) { cfg.EnableVoiceTalk = false })
	assert.Equal(t, voiceWord{Talk: voiceTalkOff, Reason: voiceTalkSwitchedOffText}, s.word())
	assert.Empty(t, s.p.voiceTalkAlertFor(s.p.getConfiguration()))

	// Back on.
	s.configure(nil)
	assert.Equal(t, voiceWord{Talk: voiceTalkOn}, s.word())
	on := s.serve(http.MethodGet, "/api/v1/access", sceneLetIn, "", nil)
	contractFixture(t, "access-voice-on.json", on.Body.Bytes())
}

func TestVoiceTalkInAClusterIsOff(t *testing.T) {
	s := newVoiceScene(t)

	calls := s.api.ExpectedCalls[:0]
	for _, call := range s.api.ExpectedCalls {
		if call.Method != "GetConfig" {
			calls = append(calls, call)
		}
	}
	s.api.ExpectedCalls = calls
	clustered := mattermostPushing(false, "")
	enabled := true
	clustered.ClusterSettings.Enable = &enabled
	s.api.On("GetConfig").Return(clustered).Maybe()

	s.configure(nil)
	assert.Equal(t, voiceWord{Talk: voiceTalkOff, Reason: voiceTalkClusterText}, s.word())
	assert.False(t, s.p.voiceListener().state().up)
}

func TestVoiceTalkCard(t *testing.T) {
	s := newVoiceScene(t)

	refused := s.serve(http.MethodGet, "/api/v1/admin/voice", sceneLinked, "", nil)
	requireRefused(t, refused, http.StatusForbidden, codeForbidden, "not an admin")

	phone := newTestVoicePhone(t, 4)
	s.join(phone, sceneLinked, 1, false)

	card := s.serve(http.MethodGet, "/api/v1/admin/voice", sceneLockedAdmin, "", nil)
	require.Equal(t, http.StatusOK, card.Code, card.Body.String())
	var view voiceAdminView
	require.NoError(t, json.Unmarshal(card.Body.Bytes(), &view))
	assert.Equal(t, voiceAdminView{On: true, Connected: true, Port: s.port(), Open: true, Addresses: []string{"127.0.0.1"}, Talking: 1}, view)

	// The fixture, with the port the contract's example has.
	view.Port, view.Refused, view.RefusedAt = voiceTalkDefaultPort, voiceDeniedText, 1795392000000
	view.Addresses = []string{"203.0.113.7"}
	encoded, err := json.Marshal(view)
	require.NoError(t, err)
	contractFixture(t, "admin-voice.json", encoded)

	// The admins' alert while the port is down.
	s.configure(func(cfg *configuration) { cfg.VoiceTalkAddress = "2001:db8::7" })
	me := s.serve(http.MethodGet, "/api/v1/me", sceneLockedAdmin, "", nil)
	var answer meResponse
	require.NoError(t, json.Unmarshal(me.Body.Bytes(), &answer))
	assert.Equal(t, voiceTalkIPv6Text("2001:db8::7"), answer.VoiceTalkAlert)
	other := s.serve(http.MethodGet, "/api/v1/me", sceneLinked, "", nil)
	assert.NotContains(t, other.Body.String(), "voiceTalkAlert")
}

func TestVoiceTalkSettingsRestartIt(t *testing.T) {
	s := newVoiceScene(t)

	phone := newTestVoicePhone(t, 4)
	answer := s.join(phone, sceneLinked, 1, false)
	firstPort := s.port()

	// Other addresses: new phones get them, the phone on the call stays.
	s.configure(func(cfg *configuration) { cfg.VoiceTalkAddress = "127.0.0.1\n127.0.0.2" })
	assert.Equal(t, []string{"127.0.0.1", "127.0.0.2"}, s.p.voiceListener().state().addresses)
	assert.Equal(t, firstPort, s.port())
	phone.speak(4, 20)
	require.Eventually(t, func() bool { return len(s.relay.heardFrom(answer.Session)) >= 4 }, 5*time.Second, 5*time.Millisecond)

	// Another port: the phone is dropped, and its session waits for it.
	taken, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 0})
	require.NoError(t, err)
	port := taken.LocalAddr().(*net.UDPAddr).Port
	require.NoError(t, taken.Close())
	s.configure(func(cfg *configuration) { cfg.VoiceTalkPort = port })
	assert.Equal(t, port, s.port())
	require.Eventually(t, func() bool { user, _ := s.relay.user(answer.Session); return user.selfDeaf }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, 1, s.p.voiceTalk().count())

	// It joins again on the new port and is let in.
	back := newTestVoicePhone(t, 4)
	again := s.join(back, sceneLinked, 1, false)
	assert.Equal(t, answer.Session, again.Session)
	assert.Contains(t, again.SDP, "127.0.0.1 "+itoa(port)+" typ host")

	// Another voice server: every session ends, its phone told why, and
	// which name it takes is not known.
	talk := s.p.voiceTalk()
	talk.lock.Lock()
	talk.fallbackFirst = true
	talk.lock.Unlock()
	s.configure(func(cfg *configuration) {
		cfg.VoiceTalkPort = port
		cfg.MumblePassword = "changed"
	})
	require.Eventually(t, func() bool { _, ok := back.ended(); return ok }, 5*time.Second, 5*time.Millisecond)
	ending, _ := back.ended()
	assert.Equal(t, voiceEndSignal{End: voiceEndMumble, Reason: voiceSettingsChangedText}, ending)
	assert.Zero(t, s.p.voiceTalk().count())
	talk.lock.Lock()
	assert.False(t, talk.fallbackFirst)
	talk.lock.Unlock()

	// Talk switched off: the phone is told, and the port closes.
	s.relay.set(func(f *relayMumble) { f.password = "changed" })
	last := newTestVoicePhone(t, 4)
	s.join(last, sceneLinked, 1, false)
	s.configure(func(cfg *configuration) {
		cfg.VoiceTalkPort = port
		cfg.MumblePassword = "changed"
		cfg.EnableVoiceTalk = false
	})
	require.Eventually(t, func() bool { _, ok := last.ended(); return ok }, 5*time.Second, 5*time.Millisecond)
	ending, _ = last.ended()
	assert.Equal(t, voiceEndSignal{End: voiceEndOff, Reason: voiceTalkOffText}, ending)
	assert.Zero(t, s.p.voiceTalk().count())
	assert.False(t, s.p.voiceListener().state().up)
	reopened, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
	require.NoError(t, err, "the port is free again")
	_ = reopened.Close()
}

func TestVoiceTalkContract(t *testing.T) {
	s := newVoiceScene(t)

	phone := newTestVoicePhone(t, 4)
	recorder := s.call("/api/v1/voice/join", sceneLinked, map[string]any{"channel": 1, "muted": false, "deafened": false, "sdp": phone.offer()})
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var answer voiceJoinResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer))
	phone.accept(answer.SDP)

	// The SDP is new every time: the fixture spells it.
	fixed := answer
	fixed.SDP, fixed.Session = "v=0 ...", 12
	encoded, err := json.Marshal(fixed)
	require.NoError(t, err)
	contractFixture(t, "voice-join.json", encoded)

	moved := s.call("/api/v1/voice/move", sceneLinked, map[string]any{"channel": 0})
	require.Equal(t, http.StatusOK, moved.Code)
	contractFixture(t, "voice-move.json", moved.Body.Bytes())

	state := s.call("/api/v1/voice/state", sceneLinked, map[string]any{"muted": true, "deafened": false})
	require.Equal(t, http.StatusOK, state.Code)
	contractFixture(t, "voice-state.json", state.Body.Bytes())

	left := s.call("/api/v1/voice/leave", sceneLinked, nil)
	require.Equal(t, http.StatusOK, left.Code)
	contractFixture(t, "voice-leave.json", left.Body.Bytes())

	// The data channel's three messages, as the server writes them.
	for name, message := range map[string]any{
		"voice-signal-talking.json": voiceTalkingSignal{Talking: []uint32{12, 15}},
		"voice-signal-self.json":    voiceSelfSignal{Self: voiceSelfState{Channel: 2}},
		"voice-signal-end.json":     voiceEndSignal{End: voiceEndKicked, Reason: withVoiceReason(voiceKickedText, "Spamming")},
	} {
		encoded, err := json.Marshal(message)
		require.NoError(t, err)
		contractFixture(t, name, encoded)
	}
}

func TestVoiceOfferShape(t *testing.T) {
	offer := newTestVoicePhone(t, 4).offer()
	receive, ok := voiceOfferShape(offer)
	require.True(t, ok)
	assert.Equal(t, 4, receive)

	many, ok := voiceOfferShape(newTestVoicePhone(t, 8).offer())
	require.True(t, ok)
	assert.Equal(t, 8, many)

	_, ok = voiceOfferShape(newTestVoicePhone(t, 9).offer())
	assert.False(t, ok, "nine sections to receive on")
	_, ok = voiceOfferShape(newTestVoicePhone(t, 0).offer())
	assert.False(t, ok, "none to receive on")

	// At most 16 candidates in any one section, however many in all: a
	// phone may repeat its candidates in every section.
	_, ok = voiceOfferShape(withCandidates(offer, 16))
	assert.True(t, ok, "16 candidates in one section")
	_, ok = voiceOfferShape(withCandidates(offer, 17))
	assert.False(t, ok, "17 candidates in one section")
	_, ok = voiceOfferShape(withCandidatesInEach(withCandidates(offer, 0), 4))
	assert.True(t, ok, "4 candidates in each of six sections")
	_, ok = voiceOfferShape(withCandidatesInEach(withCandidates(offer, 0), 16))
	assert.True(t, ok, "16 candidates in each of six sections")
	session := strings.Replace(withCandidates(offer, 0), "\r\nm=", "\r\n"+strings.Repeat("a=candidate:1 1 udp 2130706431 192.0.2.1 5000 typ host\r\n", 17)+"m=", 1)
	_, ok = voiceOfferShape(session)
	assert.False(t, ok, "17 candidates for the whole session")

	for about, broken := range map[string]string{
		"two microphones":  strings.Replace(offer, "a=recvonly", "a=sendonly", 1),
		"sendrecv":         strings.Replace(offer, "a=sendonly", "a=sendrecv", 1),
		"no Opus":          strings.ReplaceAll(offer, "opus/48000", "PCMU/8000"),
		"video":            strings.Replace(offer, "m=audio", "m=video", 1),
		"no data channel":  offer[:strings.Index(offer, "m=application")],
		"not SDP":          "hello",
		"empty":            "",
		"a section turned": strings.Replace(offer, "m=audio 9", "m=audio 0", 1),
	} {
		_, ok := voiceOfferShape(broken)
		assert.False(t, ok, about)
	}
}

func TestResolveVoiceAddresses(t *testing.T) {
	lookup := func(_ context.Context, host string) ([]net.IPAddr, error) {
		switch host {
		case "voice.example.com":
			return []net.IPAddr{{IP: net.ParseIP("2001:db8::1")}, {IP: net.ParseIP("203.0.113.7")}, {IP: net.ParseIP("203.0.113.8")}}, nil
		case "chat.example.com":
			return []net.IPAddr{{IP: net.ParseIP("198.51.100.4")}}, nil
		case "localhost":
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		case "six.example.com":
			return []net.IPAddr{{IP: net.ParseIP("2001:db8::2")}}, nil
		default:
			return nil, errors.New("no such host")
		}
	}
	ctx := context.Background()

	addresses, problem := resolveVoiceAddresses(ctx, []string{"192.0.2.1", "voice.example.com", "203.0.113.7"}, "", lookup)
	assert.Empty(t, problem)
	assert.Equal(t, []string{"192.0.2.1", "203.0.113.7", "203.0.113.8"}, addresses, "IPv4 only, each once")

	_, problem = resolveVoiceAddresses(ctx, []string{"192.0.2.1", "nowhere.example.com"}, "", lookup)
	assert.Equal(t, voiceTalkUnresolvedText("nowhere.example.com"), problem)
	_, problem = resolveVoiceAddresses(ctx, []string{"six.example.com"}, "", lookup)
	assert.Equal(t, voiceTalkUnresolvedText("six.example.com"), problem)
	_, problem = resolveVoiceAddresses(ctx, []string{"[2001:db8::3]"}, "", lookup)
	assert.Equal(t, voiceTalkIPv6Text("[2001:db8::3]"), problem)

	// Blank: the Site URL's host, which must not be this machine itself.
	addresses, problem = resolveVoiceAddresses(ctx, nil, "https://chat.example.com/mattermost", lookup)
	assert.Empty(t, problem)
	assert.Equal(t, []string{"198.51.100.4"}, addresses)
	for _, siteURL := range []string{"", "http://localhost:8065", "http://127.0.0.1:8065", "https://nowhere.example.com"} {
		_, problem = resolveVoiceAddresses(ctx, nil, siteURL, lookup)
		assert.Equal(t, voiceTalkNoAddressText, problem, siteURL)
	}

	// Typed by the admin, even this machine is taken: tests and a phone on
	// the same machine use it.
	addresses, problem = resolveVoiceAddresses(ctx, []string{"127.0.0.1"}, "", lookup)
	assert.Empty(t, problem)
	assert.Equal(t, []string{"127.0.0.1"}, addresses)
}

func TestVoiceTalkSettingsAreTidied(t *testing.T) {
	cfg := &configuration{VoiceTalkPort: 80, VoiceTalkAddress: " 203.0.113.7 \n\n voice.example.com\nVOICE.example.com\n"}
	cfg.normalize()
	assert.Equal(t, "203.0.113.7\nvoice.example.com", cfg.VoiceTalkAddress)

	// The port is kept as set, whatever it is: one outside 1024 to 65535
	// leaves talk off with a sentence instead.
	for _, port := range []int{0, 80, 1023, 1024, 50000, 65535, 65536} {
		cfg := &configuration{VoiceTalkPort: port}
		cfg.normalize()
		assert.Equal(t, port, cfg.VoiceTalkPort, port)
		assert.Equal(t, port >= 1024 && port <= 65535, voiceTalkPortAllowed(port), port)
	}

	base := &configuration{EnableVoice: true, EnableVoiceTalk: true, MumbleServer: "mumble.example.com", VoiceTalkPort: 8445}
	assert.True(t, base.voiceTalkWanted())
	assert.False(t, sameVoiceTalk(nil, base))
	assert.True(t, sameVoiceTalk(base, base.Clone()))
	for about, edit := range map[string]func(c *configuration){
		"switch":  func(c *configuration) { c.EnableVoiceTalk = false },
		"voice":   func(c *configuration) { c.EnableVoice = false },
		"port":    func(c *configuration) { c.VoiceTalkPort = 9000 },
		"address": func(c *configuration) { c.VoiceTalkAddress = "203.0.113.9" },
		"budget":  func(c *configuration) { c.VoiceTalkNoBudget = true },
	} {
		other := base.Clone()
		edit(other)
		assert.False(t, sameVoiceTalk(base, other), about)
	}
	noServer := base.Clone()
	noServer.MumbleServer = ""
	assert.False(t, noServer.voiceTalkWanted())
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

// withCandidates is an offer with its own candidates taken out and n made-up
// ones put in its first section.
func withCandidates(offer string, n int) string {
	var lines []string
	added := false
	for _, line := range strings.Split(strings.TrimRight(offer, "\r\n"), "\r\n") {
		if strings.HasPrefix(line, "a=candidate:") || line == "a=end-of-candidates" {
			continue
		}
		lines = append(lines, line)
		if !added && strings.HasPrefix(line, "m=") {
			for i := range n {
				lines = append(lines, "a=candidate:"+itoa(i+1)+" 1 udp 2130706431 192.0.2."+itoa(i+1)+" 5000 typ host")
			}
			added = true
		}
	}

	return strings.Join(lines, "\r\n") + "\r\n"
}

// withCandidatesInEach is an offer whose every section carries at least n
// candidates: its own, kept, and made-up ones after them.
func withCandidatesInEach(offer string, n int) string {
	var lines []string
	inSection, count, made := false, 0, 0
	fill := func() {
		for ; inSection && count < n; count++ {
			made++
			lines = append(lines, "a=candidate:"+itoa(100+made)+" 1 udp 2130706431 192.0.2."+itoa(made)+" 5000 typ host")
		}
	}
	for _, line := range strings.Split(strings.TrimRight(offer, "\r\n"), "\r\n") {
		if strings.HasPrefix(line, "m=") {
			fill()
			inSection, count = true, 0
		}
		if strings.HasPrefix(line, "a=candidate:") {
			count++
		}
		lines = append(lines, line)
	}
	fill()

	return strings.Join(lines, "\r\n") + "\r\n"
}

// voiceSectionCandidates is how many candidates each section of an offer
// carries.
func voiceSectionCandidates(t *testing.T, offer string) []int {
	t.Helper()

	var description sdp.SessionDescription
	require.NoError(t, description.UnmarshalString(offer))
	var counts []int
	for _, media := range description.MediaDescriptions {
		counts = append(counts, voiceCandidateCount(media.Attributes))
	}

	return counts
}

// A phone's WebRTC may give every section its own candidates, as Android's
// does without bundling: an offer whose six sections carry four each is
// answered and connects, and one section with seventeen is refused.
func TestVoiceTalkCandidatesInEverySection(t *testing.T) {
	s := newVoiceScene(t)

	dave := newTestVoicePhone(t, 4)
	offer := withCandidatesInEach(dave.offer(), 4)
	assert.Equal(t, []int{4, 4, 4, 4, 4, 4}, voiceSectionCandidates(t, offer))

	recorder := s.call("/api/v1/voice/join", sceneLinked, map[string]any{"channel": 1, "muted": false, "deafened": false, "sdp": offer})
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	var answer voiceJoinResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer))
	dave.accept(answer.SDP)
	assert.Equal(t, voiceSelfState{Channel: 1}, dave.self())

	erin := newTestVoicePhone(t, 4)
	tooMany := withCandidatesInEach(withCandidates(erin.offer(), 17), 1)
	assert.Equal(t, 17, slices.Max(voiceSectionCandidates(t, tooMany)))
	refused := s.call("/api/v1/voice/join", sceneLetIn, map[string]any{"channel": 1, "sdp": tooMany})
	requireRefused(t, refused, http.StatusBadRequest, codeBadRequest, "seventeen in one section")
}

// The phone's candidates this server never sends its checks to.
func TestVoiceRemoteAddressesAllowed(t *testing.T) {
	for address, allowed := range map[string]bool{
		"203.0.113.7":     true,
		"192.168.1.20":    true,
		"127.0.0.1":       true,
		"0.0.0.0":         false,
		"224.0.0.251":     false,
		"239.255.255.250": false,
		"255.255.255.255": false,
	} {
		assert.Equal(t, allowed, voiceRemoteAllowed(net.ParseIP(address)), address)
	}
}

// A phone opens the one data channel "voice": one more, or one by another
// name, ends its connection at once, and pion takes no third meanwhile.
func TestVoiceTalkAPhoneThatOpensMoreDataChannelsIsDropped(t *testing.T) {
	s := newVoiceScene(t)

	phone := newTestVoicePhone(t, 4)
	answer := s.join(phone, sceneLinked, 1, false)

	session := s.p.voiceTalk().of(sceneLinked)
	require.NotNil(t, session)
	session.lock.Lock()
	peer, _ := session.peer.(*voicePhonePeer)
	session.lock.Unlock()
	require.NotNil(t, peer)

	for _, label := range []string{voiceDataChannelLabel, "more"} {
		_, err := phone.pc.CreateDataChannel(label, nil)
		require.NoError(t, err)
	}

	select {
	case <-peer.closed:
	case <-time.After(10 * time.Second):
		t.Fatal("the phone's connection was not closed")
	}
	peer.lock.Lock()
	seen := peer.channels
	peer.lock.Unlock()
	assert.Equal(t, 2, seen, "the third channel was never taken")

	select {
	case <-phone.channelClosed:
	case <-time.After(10 * time.Second):
		t.Fatal("the phone's data channel stayed open")
	}

	// The session waits for the phone, as for any that goes.
	require.Eventually(t, func() bool { user, _ := s.relay.user(answer.Session); return user.selfDeaf }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, 1, s.p.voiceTalk().count())
	require.Eventually(t, func() bool { return s.p.voiceListener().peerCount() == 0 }, 5*time.Second, 5*time.Millisecond)
}

// A listener that is down is tried again after 30 seconds, then twice as
// long each time, up to the ten minutes between look-ups.
func TestVoiceTalkIsTriedAgainSoonerWhileDown(t *testing.T) {
	var waits []time.Duration
	wait := time.Duration(0)
	for range 7 {
		wait = voiceTalkRetry(wait)
		waits = append(waits, wait)
	}
	assert.Equal(t, []time.Duration{
		30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute, 10 * time.Minute,
	}, waits)
}

// Talk restarted for another setting, with the address unchanged and its
// look-up failing just then, keeps the addresses it had and the phones on
// them; another address that leads nowhere is a problem at once.
func TestVoiceTalkARestartKeepsTheAddressesALookUpCannotFind(t *testing.T) {
	s := newVoiceScene(t)

	var failing atomic.Bool
	s.p.voiceTalkLookup = func(_ context.Context, host string) ([]net.IPAddr, error) {
		if failing.Load() || host != "voice.example.test" {
			return nil, errors.New("no answer")
		}

		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}

	s.configure(func(cfg *configuration) { cfg.VoiceTalkAddress = "voice.example.test" })
	require.True(t, s.p.voiceListener().state().up)

	phone := newTestVoicePhone(t, 4)
	answer := s.join(phone, sceneLinked, 1, false)

	failing.Store(true)
	s.configure(func(cfg *configuration) {
		cfg.VoiceTalkAddress = "voice.example.test"
		cfg.VoiceTalkNoBudget = true
	})
	state := s.p.voiceListener().state()
	assert.True(t, state.up)
	assert.Equal(t, []string{"127.0.0.1"}, state.addresses)
	assert.Empty(t, state.problem)
	assert.Equal(t, voiceWord{Talk: voiceTalkOn}, s.word())

	phone.speak(4, 20)
	require.Eventually(t, func() bool { return len(s.relay.heardFrom(answer.Session)) >= 4 }, 5*time.Second, 5*time.Millisecond, "the phone is still on")

	s.configure(func(cfg *configuration) {
		cfg.VoiceTalkAddress = "elsewhere.example.test"
		cfg.VoiceTalkNoBudget = true
	})
	state = s.p.voiceListener().state()
	assert.False(t, state.up)
	assert.Equal(t, voiceTalkUnresolvedText("elsewhere.example.test"), state.problem)
}

// The plugin stopping, as a restart or an upgrade does, tells each phone
// voice is restarting, coded as the voice server lost, which the app joins
// again after by itself.
func TestVoiceTalkAPluginStopTellsPhonesItIsRestarting(t *testing.T) {
	s := newVoiceScene(t)

	phone := newTestVoicePhone(t, 4)
	s.join(phone, sceneLinked, 1, false)

	s.p.stopVoiceTalk()
	require.Eventually(t, func() bool { _, ok := phone.ended(); return ok }, 5*time.Second, 5*time.Millisecond)
	ending, _ := phone.ended()
	assert.Equal(t, voiceEndSignal{End: voiceEndMumble, Reason: "Voice is restarting on this server."}, ending)
	assert.Zero(t, s.p.voiceTalk().count())
	assert.False(t, s.p.voiceListener().state().up)
}

// Under the app check's `require`, leaving voice always works, as taking a
// push registration back does; joining, moving and muting stay behind the
// check.
func TestLeavingVoicePassesTheAppCheck(t *testing.T) {
	a := newAppCheckScene(t, appCheckRequire)

	for _, target := range []string{"/api/v1/voice/join", "/api/v1/voice/move", "/api/v1/voice/state"} {
		refused := a.call(http.MethodPost, target, sceneLinked, davesPhone, mattyAgent, map[string]any{"channel": 1})
		require.Equal(t, http.StatusForbidden, refused.Code, target)
		require.Equal(t, codeAppUnverified, codedErrorOrEmpty(refused), target)
	}

	left := a.call(http.MethodPost, "/api/v1/voice/leave", sceneLinked, davesPhone, mattyAgent, nil)
	require.Equal(t, http.StatusOK, left.Code, left.Body.String())
	assert.JSONEq(t, `{"left":false}`, left.Body.String())
}
