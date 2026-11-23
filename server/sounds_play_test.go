package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"layeh.com/gumble/gumble"
	"layeh.com/gumble/gumble/MumbleProto"
	"layeh.com/gumble/gumble/varint"
)

// Playing a sound into Mumble (design/3.2.0.md §7.5, §7.10). Who may press is
// read from gumble's live state, at the press and again before every packet;
// one sound at a time, admitted under one lock; and on the wire, Mumble's own
// legacy tunnelled Opus, 20 ms apart, the sequence counting 10 ms units.

// ----------------------------------------------------------- the read alone

// pressClient is a Mumble connection's state built by hand: VillaNET parked
// in the root, people in Lounge. Nothing else touches it, so no lock is needed.
func pressClient(people ...*gumble.User) (*gumble.Client, *gumble.Channel) {
	root := &gumble.Channel{ID: 0, Name: "Root"}
	lounge := &gumble.Channel{ID: 7, Name: "Lounge"}
	bot := &gumble.User{Session: 1, Name: "VillaNET", Channel: root}

	users := gumble.Users{1: bot}
	for i, person := range people {
		person.Session = uint32(i + 2)
		if person.Channel == nil {
			person.Channel = lounge
		}
		users[person.Session] = person
	}

	return &gumble.Client{Self: bot, Users: users}, lounge
}

func pressConfig() *configuration {
	cfg := voiceConfig()
	cfg.EnableSoundboard = true

	return cfg
}

func TestEveryPressOutcomeIsReadFromMumble(t *testing.T) {
	cfg := pressConfig()
	perms := newFakePermissions()

	for about, row := range map[string]struct {
		users []*gumble.User
		tweak func(cfg *configuration, client *gumble.Client)
		code  string
		in    bool

		// The first person is a phone session of this node's, the
		// account's that joined from Matty.
		phoneOf string

		// The channels the roster shows (Channels to show), nil for every
		// one.
		shown map[uint32]bool
	}{
		"in, and free to speak": {users: []*gumble.User{{Name: "Craig"}}, code: "", in: true},
		"not in Mumble":         {users: []*gumble.User{{Name: "Bob"}}, code: soundCodeNotInVoice},
		"a lookalike":           {users: []*gumble.User{{Name: "Craİg"}}, code: soundCodeNotInVoice},
		"muted":                 {users: []*gumble.User{{Name: "craig", Muted: true}}, code: soundCodeMuted, in: true},
		"self-muted":            {users: []*gumble.User{{Name: "craig", SelfMuted: true}}, code: soundCodeMuted, in: true},
		"deafened":              {users: []*gumble.User{{Name: "craig", Deafened: true, Muted: true}}, code: soundCodeDeafened, in: true},
		"self-deafened":         {users: []*gumble.User{{Name: "craig", SelfDeafened: true, SelfMuted: true}}, code: soundCodeDeafened, in: true},
		"suppressed":            {users: []*gumble.User{{Name: "craig", Suppressed: true}}, code: soundCodeSuppressed, in: true},
		"twice, by Names that differ": {
			users: []*gumble.User{{Name: "craig"}, {Name: "Craig_laptop"}},
			tweak: func(cfg *configuration, _ *gumble.Client) { cfg.VoiceNameMap = "Craig_laptop = craig" },
			code:  soundCodeTwoSessions, in: true,
		},
		"unregistered, with registered names only": {
			users: []*gumble.User{{Name: "craig"}},
			tweak: func(cfg *configuration, _ *gumble.Client) { cfg.SoundsRegisteredOnly = true },
			code:  soundCodeNotRegistered, in: true,
		},
		"registered, with registered names only": {
			users: []*gumble.User{{Name: "craig", UserID: 12}},
			tweak: func(cfg *configuration, _ *gumble.Client) { cfg.SoundsRegisteredOnly = true },
			code:  "", in: true,
		},
		"VillaNET unregistered, UserID 0, plays": {
			users: []*gumble.User{{Name: "craig"}},
			tweak: func(_ *configuration, client *gumble.Client) { client.Self.UserID = 0 },
			code:  "", in: true,
		},
		"VillaNET logged in as SuperUser, also UserID 0": {
			users: []*gumble.User{{Name: "craig"}},
			tweak: func(cfg *configuration, client *gumble.Client) {
				client.Self.Name = "SuperUser"
				cfg.MumbleUsername = "SuperUser"
			},
			code: soundCodeBotCannot, in: true,
		},
		"VillaNET muted": {
			users: []*gumble.User{{Name: "craig"}},
			tweak: func(_ *configuration, client *gumble.Client) { client.Self.Muted = true },
			code:  soundCodeBotCannot, in: true,
		},
		"VillaNET suppressed where it sits": {
			users: []*gumble.User{{Name: "craig"}},
			tweak: func(_ *configuration, client *gumble.Client) { client.Self.Suppressed = true },
			code:  soundCodeBotCannot, in: true,
		},
		"in only from Matty, under the phone's name": {
			users: []*gumble.User{{Name: "craig (Matty)"}}, phoneOf: "uid-craig",
			code: "", in: true,
		},
		"from Matty, with registered names only, as the voice server reports the session": {
			users:   []*gumble.User{{Name: "craig (Matty)"}},
			phoneOf: "uid-craig",
			tweak:   func(cfg *configuration, _ *gumble.Client) { cfg.SoundsRegisteredOnly = true },
			code:    soundCodeNotRegistered, in: true,
		},
		"somebody else's phone session, whatever its name": {
			users: []*gumble.User{{Name: "craig"}}, phoneOf: "uid-dave",
			code: soundCodeNotInVoice,
		},
		"in from Matty and from Mumble too": {
			users: []*gumble.User{{Name: "craig (Matty)"}, {Name: "craig"}}, phoneOf: "uid-craig",
			code: soundCodeTwoSessions, in: true,
		},
		"in a channel the roster does not show": {
			users: []*gumble.User{{Name: "craig"}}, shown: map[uint32]bool{0: true},
			code: soundCodeNotInVoice,
		},
		"in a channel the roster shows": {
			users: []*gumble.User{{Name: "craig"}}, shown: map[uint32]bool{7: true},
			code: "", in: true,
		},
		"twice, once in a channel the roster does not show": {
			users: []*gumble.User{{Name: "craig"}, {Name: "Craig_laptop"}},
			tweak: func(cfg *configuration, client *gumble.Client) {
				cfg.VoiceNameMap = "Craig_laptop = craig"
				client.Users[3].Channel = client.Self.Channel
			},
			shown: map[uint32]bool{7: true},
			code:  "", in: true,
		},
		"VillaNET deafened": {
			users: []*gumble.User{{Name: "craig"}},
			tweak: func(_ *configuration, client *gumble.Client) { client.Self.SelfDeafened = true },
			code:  soundCodeBotCannot, in: true,
		},
	} {
		next := *cfg
		client, _ := pressClient(row.users...)
		if row.tweak != nil {
			row.tweak(&next, client)
		}

		presser := voicePresser{userID: "uid-craig", username: "craig", phones: &voicePhoneRegistry{}, shown: row.shown}
		if row.phoneOf != "" {
			presser.phones.put(row.users[0].Session, row.phoneOf)
		}

		read := readVoice(client, presser, &next, perms)
		require.Equal(t, row.code, read.code, about)
		require.Equal(t, row.in, read.in, about)
		if row.code == "" {
			require.Equal(t, "Lounge", read.channelName, about)
			require.EqualValues(t, 7, read.channelID, about)
		}
	}

	// Whisper in the presser's channel, when it is known.
	client, lounge := pressClient(&gumble.User{Name: "craig"})
	perms.set(func(f *fakePermissions) { f.whisper[lounge.ID] = false })
	read := readVoice(client, voicePresser{username: "craig"}, cfg, perms)
	require.Equal(t, soundCodeBotCannot, read.code, "no Whisper there")
	require.True(t, read.whisperKnown)

	perms.set(func(f *fakePermissions) { delete(f.whisper, lounge.ID); f.unknown[lounge.ID] = true })
	read = readVoice(client, voicePresser{username: "craig"}, cfg, perms)
	require.Empty(t, read.code, "not known yet: the press asks, and waits")
	require.False(t, read.whisperKnown)

	// The admins' lines name the trouble, and SuperUser by its name.
	_, line := botCanSpeak(&gumble.User{Name: "superuser"})
	require.Contains(t, line, "SuperUser")
	ok, _ := botCanSpeak(&gumble.User{Name: "VillaNET", UserID: 0})
	require.True(t, ok, "an unregistered account is not SuperUser")
}

// pressesLeft is how many presses somebody has left this minute.
func pressesLeft(limiter *rateLimiter, key string) float64 {
	limiter.lock.Lock()
	defer limiter.lock.Unlock()

	if bucket := limiter.buckets[key]; bucket != nil {
		return bucket.tokens
	}

	return limiter.burst
}

func TestTheWaitsAnswerWithRetryAfterAndTheTokenIsKeptOrRefunded(t *testing.T) {
	s := newSoundScene(t)
	id := s.seedSounds(t, sceneLinkedAdmin)[0]
	press := func() *httptest.ResponseRecorder {
		return s.serve(http.MethodPost, "/api/v1/sounds/play?id="+id, sceneLinked, "", nil)
	}
	refusal := func(recorder *httptest.ResponseRecorder) soundRefusal {
		var answer soundRefusal
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer))

		return answer
	}
	presses := p320Limiters(s.p).soundPress
	st := s.p.sounds()
	now := time.Now()

	// Muted: refused, the token kept.
	s.user(2).SelfMuted = true
	before := pressesLeft(presses, sceneLinked)
	requireRefused(t, press(), http.StatusForbidden, soundCodeMuted, "muted")
	require.InDelta(t, before-1, pressesLeft(presses, sceneLinked), 0.01, "kept: the panel greys it out")
	s.user(2).SelfMuted = false

	// Mumble not connected: refunded.
	s.p.setVoiceClient(nil)
	before = pressesLeft(presses, sceneLinked)
	requireRefused(t, press(), http.StatusServiceUnavailable, soundCodeBotOffline, "offline")
	require.InDelta(t, before, pressesLeft(presses, sceneLinked), 0.05, "refunded: nothing the presser could see coming")
	s.p.setVoiceClient(s.client)

	// Somebody else's sound in flight: busy, with how long to wait.
	other, code, _ := st.gate.admit(sceneLinkedAdmin, now, 10*time.Second)
	require.Empty(t, code)
	before = pressesLeft(presses, sceneLinked)
	recorder := press()
	requireRefused(t, recorder, http.StatusConflict, soundCodeBusy, "busy")
	require.Positive(t, refusal(recorder).RetryAfterMs)
	require.InDelta(t, before-1, pressesLeft(presses, sceneLinked), 0.01, "kept")
	st.gate.ended(other, time.Now().Add(-time.Second), 10*time.Second)

	// Their own wait after their sound: cooldown, with how long.
	mine, code, _ := st.gate.admit(sceneLinked, time.Now(), 10*time.Second)
	require.Empty(t, code)
	st.gate.ended(mine, time.Now(), 10*time.Second)
	recorder = press()
	requireRefused(t, recorder, http.StatusTooManyRequests, soundCodeCooldown, "cooldown")
	require.InDelta(t, 10000, refusal(recorder).RetryAfterMs, 500)

	// Eight sounds a minute in all.
	st.gate.lock.Lock()
	delete(st.gate.cooldownUntil, sceneLinked)
	st.gate.busyUntil = time.Time{}
	for st.gate.global.allowAt("", time.Now()) {
	}
	st.gate.lock.Unlock()
	recorder = press()
	requireRefused(t, recorder, http.StatusTooManyRequests, soundCodeTooMany, "eight a minute")
	require.Positive(t, refusal(recorder).RetryAfterMs)

	// Thirty presses a minute each, refused presses included.
	for pressesLeft(presses, sceneLinked) >= 1 {
		press()
	}
	recorder = press()
	requireRefused(t, recorder, http.StatusTooManyRequests, soundCodeRateLimited, "thirty a minute")
	require.Positive(t, refusal(recorder).RetryAfterMs)
}

// p320Limiters is the plugin's 3.2.0 meters.
func p320Limiters(p *Plugin) *featureLimiters {
	return p.limiters()
}

// ------------------------------------------------- a Mumble server, faked

// mumbleFrame is one packet VillaNET sent the server, as it arrived.
type mumbleFrame struct {
	at   time.Time
	kind uint16
	data []byte
}

// audioPacket is a tunnelled audio frame taken apart.
type audioPacket struct {
	at       time.Time
	header   byte
	sequence int64
	final    bool
	data     []byte
}

// fakeMumble is just enough of a Mumble server for a gumble client to connect
// to, sync with and send audio to: TLS, the channels and users sent at
// connect, and everything the client sends kept in order.
type fakeMumble struct {
	t        *testing.T
	listener net.Listener

	lock   sync.Mutex
	conn   *gumble.Conn
	frames []mumbleFrame

	// Called on the server's read goroutine for each packet that arrives,
	// before it is kept, so a test can answer at a chosen moment.
	onFrame func(frame mumbleFrame, audio int)

	// What the server answers a PermissionQuery with, by channel: nothing
	// until a test says so.
	onPermissionQuery func(channelID uint32)
}

func u32(v uint32) *uint32 { return &v }
func str(v string) *string { return &v }
func yes(v bool) *bool     { return &v }

func selfSignedTLS(t *testing.T) *tls.Config {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "mumble.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)

	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
}

// newFakeMumble starts a server with Root, Lounge (7) and Games (8), and
// these users after VillaNET, whose session is 1.
func newFakeMumble(t *testing.T, users ...*MumbleProto.UserState) *fakeMumble {
	t.Helper()

	listener, err := tls.Listen("tcp", "127.0.0.1:0", selfSignedTLS(t))
	require.NoError(t, err)

	f := &fakeMumble{t: t, listener: listener}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		raw, err := listener.Accept()
		if err != nil {
			return
		}

		conn := gumble.NewConn(raw)
		conn.Timeout = time.Minute
		f.lock.Lock()
		f.conn = conn
		f.lock.Unlock()

		// The client's Version and Authenticate.
		for range 2 {
			if _, _, err := conn.ReadPacket(); err != nil {
				return
			}
		}

		f.send(&MumbleProto.ChannelState{ChannelId: u32(0), Name: str("Root")})
		f.send(&MumbleProto.ChannelState{ChannelId: u32(7), Parent: u32(0), Name: str("Lounge")})
		f.send(&MumbleProto.ChannelState{ChannelId: u32(8), Parent: u32(0), Name: str("Games")})
		f.send(&MumbleProto.UserState{Session: u32(1), Name: str("VillaNET"), ChannelId: u32(0)})
		for _, user := range users {
			f.send(user)
		}
		f.send(&MumbleProto.ServerSync{Session: u32(1), MaxBandwidth: u32(558000)})

		audio := 0
		for {
			kind, data, err := conn.ReadPacket()
			if err != nil {
				return
			}

			frame := mumbleFrame{at: time.Now(), kind: kind, data: append([]byte(nil), data...)}

			f.lock.Lock()
			onFrame, onQuery := f.onFrame, f.onPermissionQuery
			f.lock.Unlock()

			if kind == 20 && onQuery != nil {
				var query MumbleProto.PermissionQuery
				if query.XXX_Unmarshal(frame.data) == nil && query.ChannelId != nil {
					onQuery(*query.ChannelId)
				}
			}
			if onFrame != nil {
				onFrame(frame, audio)
			}
			if kind == 1 {
				audio++
			}

			f.lock.Lock()
			f.frames = append(f.frames, frame)
			f.lock.Unlock()
		}
	}()

	return f
}

type protoMessage interface {
	Reset()
	String() string
	ProtoMessage()
}

// send writes a message to the client, as the server.
func (f *fakeMumble) send(message protoMessage) {
	f.lock.Lock()
	conn := f.conn
	f.lock.Unlock()

	if err := conn.WriteProto(message); err != nil {
		f.t.Logf("fake Mumble could not send: %v", err)
	}
}

// grantWhisper answers the client's PermissionQuery for a channel with
// Whisper and Speak, as Murmur's defaults would.
func (f *fakeMumble) grantWhisper(channelID uint32) {
	f.send(&MumbleProto.PermissionQuery{ChannelId: u32(channelID), Permissions: u32(uint32(gumble.PermissionWhisper | gumble.PermissionSpeak | gumble.PermissionEnter | gumble.PermissionTraverse))})
}

func (f *fakeMumble) set(change func(f *fakeMumble)) {
	f.lock.Lock()
	defer f.lock.Unlock()

	change(f)
}

func (f *fakeMumble) audio() []audioPacket {
	f.lock.Lock()
	defer f.lock.Unlock()

	var packets []audioPacket
	for _, frame := range f.frames {
		if frame.kind != 1 {
			continue
		}

		packet := audioPacket{at: frame.at, header: frame.data[0]}
		rest := frame.data[1:]
		sequence, n := varint.Decode(rest)
		rest = rest[n:]
		length, n := varint.Decode(rest)
		rest = rest[n:]

		packet.sequence = sequence
		packet.final = length&0x2000 != 0
		packet.data = rest[:length&^0x2000]
		packets = append(packets, packet)
	}

	return packets
}

func (f *fakeMumble) targets() []*MumbleProto.VoiceTarget {
	f.lock.Lock()
	defer f.lock.Unlock()

	var targets []*MumbleProto.VoiceTarget
	for _, frame := range f.frames {
		if frame.kind != 19 {
			continue
		}

		var target MumbleProto.VoiceTarget
		require.NoError(f.t, target.XXX_Unmarshal(frame.data))
		targets = append(targets, &target)
	}

	return targets
}

// liveSoundScene is the sound scene on a real gumble connection to the fake
// server, held by VillaNET's own connection code, with Dave and Bob in Lounge.
func liveSoundScene(t *testing.T, users ...*MumbleProto.UserState) (*soundScene, *fakeMumble) {
	t.Helper()

	if len(users) == 0 {
		users = []*MumbleProto.UserState{
			{Session: u32(2), Name: str("dave"), ChannelId: u32(7)},
			{Session: u32(3), Name: str("bob"), ChannelId: u32(7)},
		}
	}
	server := newFakeMumble(t, users...)

	s := newSoundScene(t)
	s.p.setVoiceClient(nil)
	s.api.On("PublishWebSocketEvent", wsEventSound, mock.Anything, mock.Anything).Return()

	st := s.p.sounds()
	st.lock.Lock()
	st.perms = nil
	st.whisperWait = 2 * time.Second
	st.lock.Unlock()

	cfg := s.p.getConfiguration().Clone()
	cfg.MumbleServer = server.listener.Addr().String()
	cfg.SoundCooldownSeconds = 3
	s.p.setConfiguration(cfg)

	// Murmur says what VillaNET may do in a channel when asked.
	server.set(func(f *fakeMumble) { f.onPermissionQuery = f.grantWhisper })

	s.p.restartVoice()
	t.Cleanup(s.p.stopVoice)

	require.Eventually(t, func() bool {
		client, player := s.p.currentVoiceClient()
		return client != nil && player != nil
	}, 10*time.Second, 10*time.Millisecond, "connected")

	return s, server
}

// played is a press's answer.
func played(t *testing.T, recorder *httptest.ResponseRecorder) soundPlayResponse {
	t.Helper()

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var answer soundPlayResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer))

	return answer
}

// waitForAudio waits until the server has n audio packets, and a moment more
// to be sure no more come.
func waitForAudio(t *testing.T, server *fakeMumble, n int) []audioPacket {
	t.Helper()

	require.Eventually(t, func() bool { return len(server.audio()) >= n }, 15*time.Second, 5*time.Millisecond)
	time.Sleep(100 * time.Millisecond)

	return server.audio()
}

// waitForGate waits until the gate is free for anybody.
func waitForGate(t *testing.T, s *soundScene) {
	t.Helper()

	require.Eventually(t, func() bool {
		_, busy := s.p.sounds().gate.state("", time.Now())
		return busy.IsZero()
	}, 15*time.Second, 5*time.Millisecond)
}

func (s *soundScene) press(id, caller string) *httptest.ResponseRecorder {
	return s.serve(http.MethodPost, "/api/v1/sounds/play?id="+id, caller, "", nil)
}

func TestTheWireCarriesThePacketsAsMumbleExpects(t *testing.T) {
	s, server := liveSoundScene(t)
	tone := s.addedFrom(t, sceneLinked, "tone1s")
	blip := s.addedFrom(t, sceneLinkedAdmin, "blip")

	answer := played(t, s.press(tone, sceneLinked))
	require.Equal(t, "Lounge", answer.Channel)
	require.EqualValues(t, 51*20, answer.EndsAt-answer.StartedAt)
	require.EqualValues(t, answer.EndsAt+3000, answer.CooldownUntil)

	packets := waitForAudio(t, server, 51)
	require.Len(t, packets, 51)

	sent := soundFixtures(t, "chromium")["tone1s"]
	for i, packet := range packets {
		require.Equal(t, byte(4<<5|1), packet.header, "Opus, to voice target 1")
		require.EqualValues(t, 2*i, packet.sequence, "from 0, +2 a packet: 10 ms units")
		require.Equal(t, i == 50, packet.final, "the terminator bit on the last only")
		require.Equal(t, sent[i], packet.data, "the browser's packet, unchanged")
	}

	spacing := packets[50].at.Sub(packets[0].at) / 50
	require.InDelta(t, float64(20*time.Millisecond), float64(spacing), float64(5*time.Millisecond), "20 ms apart on average: %v", spacing)

	targets := server.targets()
	require.Len(t, targets, 1, "the voice target, sent once")
	require.EqualValues(t, 1, targets[0].GetId())
	require.Len(t, targets[0].Targets, 1)
	require.EqualValues(t, 7, targets[0].Targets[0].GetChannelId(), "a shout into the presser's own channel")
	require.False(t, targets[0].Targets[0].GetChildren())
	require.False(t, targets[0].Targets[0].GetLinks())

	// Another person a moment later: the sequence carries on through the
	// silence, and the same channel needs no new target.
	waitForGate(t, s)
	gapStart := packets[50].at
	played(t, s.press(blip, sceneLinkedAdmin))
	all := waitForAudio(t, server, 64)
	second := all[51]
	gap := second.at.Sub(gapStart)
	// Measured here by when the packets arrived, which can each be a few
	// milliseconds late; a count that restarted, or went on by 2, would be
	// out by twenty-five or more.
	require.InDelta(t, 100+float64(gap/(10*time.Millisecond)), float64(second.sequence), 8, "the count went on through %v of silence", gap)
	require.Len(t, server.targets(), 1)

	// Five seconds of silence later, in another channel: from 0, with the
	// target sent again.
	server.send(&MumbleProto.UserState{Session: u32(2), ChannelId: u32(8)})
	require.Eventually(t, func() bool {
		var channel uint32
		client, _ := s.p.currentVoiceClient()
		client.Do(func() { channel = client.Users[2].Channel.ID })
		return channel == 8
	}, 5*time.Second, 5*time.Millisecond)
	time.Sleep(soundSequenceReset + 100*time.Millisecond)

	answer = played(t, s.press(tone, sceneLinked))
	require.Equal(t, "Games", answer.Channel)
	all = waitForAudio(t, server, 115)
	require.EqualValues(t, 0, all[64].sequence, "a new count after five seconds of silence")
	targets = server.targets()
	require.Len(t, targets, 2, "sent again for the new channel")
	require.EqualValues(t, 8, targets[1].Targets[0].GetChannelId())
}

// addedFrom adds a sound made from a fixture, as caller.
func (s *soundScene) addedFrom(t *testing.T, caller, fixture string) string {
	t.Helper()

	recorder := s.serve(http.MethodPost, "/api/v1/sounds", caller, soundBody(t, strings.ToUpper(fixture[:1])+fixture[1:4], "bell", soundFixtures(t, "chromium")[fixture]), nil)
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())

	var answer map[string]string
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer))

	return answer["id"]
}

func TestPressesSentTogetherGiveOneSound(t *testing.T) {
	users := []*MumbleProto.UserState{
		{Session: u32(2), Name: str("dave"), ChannelId: u32(7)},
		{Session: u32(3), Name: str("bob"), ChannelId: u32(7)},
	}
	extra := []string{"wendy", "frank", "ivan"}
	for i, name := range extra {
		users = append(users, &MumbleProto.UserState{Session: u32(uint32(4 + i)), Name: str(name), ChannelId: u32(7)})
	}

	s, server := liveSoundScene(t, users...)
	people := []string{sceneLinked, sceneLinkedAdmin}
	for _, name := range extra {
		userID := "uid-" + name
		s.lock.Lock()
		s.users[userID] = &model.User{Id: userID, Username: name}
		s.lock.Unlock()
		s.faults.lock.Lock()
		linkForTest(s.store, userID, "videokey-"+name)
		s.faults.lock.Unlock()
		people = append(people, userID)
	}

	blip := s.addedFrom(t, sceneLinked, "blip")

	pressAll := func(callers []string) map[int][]string {
		var lock sync.Mutex
		answers := map[int][]string{}

		var wait sync.WaitGroup
		start := make(chan struct{})
		for _, caller := range callers {
			wait.Add(1)
			go func() {
				defer wait.Done()
				<-start

				recorder := s.press(blip, caller)
				code := codedErrorOrEmpty(recorder)

				lock.Lock()
				answers[recorder.Code] = append(answers[recorder.Code], code)
				lock.Unlock()
			}()
		}
		close(start)
		wait.Wait()

		return answers
	}

	// Thirty at once from one person.
	callers := make([]string, 30)
	for i := range callers {
		callers[i] = sceneLinked
	}
	answers := pressAll(callers)
	require.Len(t, answers[http.StatusOK], 1, "exactly one sound: %v", answers)
	for status, codes := range answers {
		if status == http.StatusOK {
			continue
		}
		for _, code := range codes {
			require.Contains(t, []string{soundCodeCooldown, soundCodeBusy}, code)
		}
	}
	require.Len(t, waitForAudio(t, server, 13), 13, "one sound's packets on the wire")

	// At once after it ends: anybody is told to wait for the quarter second
	// of quiet, and the presser for their own wait.
	requireRefused(t, s.press(blip, sceneLinkedAdmin), http.StatusConflict, soundCodeBusy, "within 250 ms of the end")
	waitForGate(t, s)

	// Dave spent all thirty of this minute's presses above; the meter is
	// another rule, so it is filled again to see the wait.
	s.p.limiters().soundPress = newRateLimiter(30, time.Minute)
	requireRefused(t, s.press(blip, sceneLinked), http.StatusTooManyRequests, soundCodeCooldown, "within the presser's own wait")
	s.p.limiters().soundPress = newRateLimiter(30, time.Minute)

	// Thirty at once from five people, Dave still waiting.
	callers = callers[:0]
	for i := range 30 {
		callers = append(callers, people[i%len(people)])
	}
	answers = pressAll(callers)
	require.Len(t, answers[http.StatusOK], 1, "exactly one sound: %v", answers)
	for status, codes := range answers {
		if status == http.StatusOK {
			continue
		}
		for _, code := range codes {
			require.Contains(t, []string{soundCodeCooldown, soundCodeBusy}, code)
		}
	}
	require.Len(t, waitForAudio(t, server, 26), 26, "and one more sound's")
}

func TestAPresserMutedBeforePacketZeroPutsNothingOnTheWire(t *testing.T) {
	s, server := liveSoundScene(t)
	tone := s.addedFrom(t, sceneLinked, "tone1s")

	// Mumble has not said whether VillaNET may whisper in Lounge, so the
	// press asks, after its admission; while it waits, Dave mutes.
	server.set(func(f *fakeMumble) {
		f.onPermissionQuery = func(channelID uint32) {
			f.send(&MumbleProto.UserState{Session: u32(2), SelfMute: yes(true)})
			f.grantWhisper(channelID)
		}
	})

	requireRefused(t, s.press(tone, sceneLinked), http.StatusForbidden, soundCodeMuted, "muted between the press and packet 0")
	time.Sleep(100 * time.Millisecond)
	require.Empty(t, server.audio(), "zero packets on the wire")

	_, busy := s.p.sounds().gate.state(sceneLinked, time.Now())
	require.True(t, busy.IsZero(), "and the admission undone")
}

func TestAPresserWhoMovedBeforePacketZeroIsRefused(t *testing.T) {
	s, server := liveSoundScene(t)
	tone := s.addedFrom(t, sceneLinked, "tone1s")

	server.set(func(f *fakeMumble) {
		f.onPermissionQuery = func(channelID uint32) {
			f.send(&MumbleProto.UserState{Session: u32(2), ChannelId: u32(8)})
			f.grantWhisper(channelID)
		}
	})

	requireRefused(t, s.press(tone, sceneLinked), http.StatusConflict, soundCodeMoved, "moved between the press and packet 0")
	time.Sleep(100 * time.Millisecond)
	require.Empty(t, server.audio(), "nothing played into the channel they left, nor the one they joined")
	require.Empty(t, server.targets())
}

func TestAPresserWhoMutesMidSoundGetsTheNextPacketFinal(t *testing.T) {
	s, server := liveSoundScene(t)
	tone := s.addedFrom(t, sceneLinked, "tone1s")

	server.set(func(f *fakeMumble) {
		f.onFrame = func(frame mumbleFrame, audio int) {
			if frame.kind == 1 && audio == 30 {
				f.send(&MumbleProto.UserState{Session: u32(2), SelfMute: yes(true)})
			}
		}
	})

	played(t, s.press(tone, sceneLinked))
	waitForGate(t, s)
	packets := server.audio()

	// The mute arrives after packet 30. Its successor goes out final, and
	// nothing after it; one more is allowed for gumble's own read goroutine
	// to have been slow to hear of it.
	last := packets[len(packets)-1]
	require.True(t, last.final)
	require.Contains(t, []int{32, 33}, len(packets), "packet 31 final, or at worst 32")
	for _, packet := range packets[:len(packets)-1] {
		require.False(t, packet.final)
	}
}

func TestAConnectionEndingStopsThePlayer(t *testing.T) {
	s, server := liveSoundScene(t)
	full := s.addedFrom(t, sceneLinked, "full6s")

	played(t, s.press(full, sceneLinked))
	require.Eventually(t, func() bool { return len(server.audio()) >= 5 }, 5*time.Second, 5*time.Millisecond)

	s.p.stopVoice()
	stoppedAt := len(server.audio())
	time.Sleep(200 * time.Millisecond)

	require.LessOrEqual(t, len(server.audio()), stoppedAt+1, "nothing more after the connection went")
	require.Less(t, len(server.audio()), 301)

	client, player := s.p.currentVoiceClient()
	require.Nil(t, client)
	require.Nil(t, player)

	s.p.sounds().gate.lock.Lock()
	inFlight := s.p.sounds().gate.inFlight
	s.p.sounds().gate.lock.Unlock()
	require.Empty(t, inFlight, "the gate is freed")

	waitForGate(t, s)
	requireRefused(t, s.press(full, sceneLinkedAdmin), http.StatusServiceUnavailable, soundCodeBotOffline, "and nothing more plays")
}

// Channels to show reaches the soundboard through the presser the press
// route builds: a press from a channel it leaves out is refused as one from
// somebody not in voice.
func TestAPressFromAChannelNotShownIsRefused(t *testing.T) {
	s, _ := liveSoundScene(t)
	tone := s.addedFrom(t, sceneLinked, "tone1s")

	cfg := s.p.getConfiguration().Clone()
	cfg.VoiceChannelsShown = "Games"
	s.p.setConfiguration(cfg)
	s.p.republishVoice()
	require.Eventually(t, func() bool { return len(s.p.getVoice().channels) == 1 }, 5*time.Second, 5*time.Millisecond)

	recorder := s.press(tone, sceneLinked)
	requireRefused(t, recorder, http.StatusForbidden, soundCodeNotInVoice, "Dave is in Lounge, which is not shown")
}

// A Save that leaves out the presser's channel while a sound plays there
// ends the sound, with its last packet marked as the last.
func TestASoundEndsWhenItsChannelStopsBeingShown(t *testing.T) {
	s, server := liveSoundScene(t)
	tone := s.addedFrom(t, sceneLinked, "tone1s")

	server.set(func(f *fakeMumble) {
		f.onFrame = func(frame mumbleFrame, audio int) {
			if frame.kind == 1 && audio == 30 {
				go func() {
					cfg := s.p.getConfiguration().Clone()
					cfg.VoiceChannelsShown = "Games"
					s.p.setConfiguration(cfg)
					s.p.republishVoice()
				}()
			}
		}
	})

	played(t, s.press(tone, sceneLinked))
	waitForGate(t, s)
	packets := server.audio()
	require.NotEmpty(t, packets)
	require.Less(t, len(packets), 51, "cut short")
	require.True(t, packets[len(packets)-1].final)
}
