package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"layeh.com/gumble/gumble/MumbleProto"
)

// Phone sessions against a voice server that passes voice on (relayMumble),
// with the registry's own timers on a clock the test moves.

const (
	talkAlice = "uid-alice"
	talkBob   = "uid-bob"
	talkCarol = "uid-carol"
)

var talkUsernames = map[string]string{talkAlice: "alice", talkBob: "bob", talkCarol: "carol"}

// talkScene is a registry on a fake voice server, with the roster's side
// stood in for: the channels it knows, and the phone sessions it was told
// about.
type talkScene struct {
	t      *testing.T
	server *relayMumble
	clock  *fakeVoiceClock
	r      *voiceSessions
	dial   voiceTalkDial

	lock      sync.Mutex
	connected bool
	phones    map[uint32]string
	gone      map[string]bool
	sessions  map[string]voiceSessionRead

	// The store does not answer access reads.
	storeDown bool

	// When each session's own messages went out, as the plugin sent them.
	sent map[uint32][]talkSend
}

// talkSend is one of a session's own messages as the plugin sent it: when it
// had gone out, and whether it was a move.
type talkSend struct {
	at   time.Time
	move bool
}

// sendsOf is what a session sent of its own, by the plugin's clock.
func (s *talkScene) sendsOf(session uint32) []talkSend {
	s.lock.Lock()
	defer s.lock.Unlock()

	return append([]talkSend(nil), s.sent[session]...)
}

func newTalkScene(t *testing.T) *talkScene {
	t.Helper()

	s := &talkScene{
		t:         t,
		server:    newRelayMumble(t),
		clock:     newFakeVoiceClock(),
		connected: true,
		phones:    map[uint32]string{},
		gone:      map[string]bool{},
		sessions:  map[string]voiceSessionRead{},
		sent:      map[uint32][]talkSend{},
	}

	s.r = newVoiceSessions(s.clock, voiceTestSafely(t))
	s.r.dial = func(ctx context.Context, dial voiceMumbleDial, handler voiceMumbleHandler, safely func(string, func()) bool) (*voiceMumble, error) {
		m, err := dialVoiceMumble(ctx, dial, handler, safely)
		if m != nil {
			// Nothing of its own goes out before the dial has returned.
			session := m.session
			m.lock.Lock()
			m.sends = func(at time.Time, move bool) {
				s.lock.Lock()
				s.sent[session] = append(s.sent[session], talkSend{at: at, move: move})
				s.lock.Unlock()
			}
			m.lock.Unlock()
		}

		return m, err
	}
	s.r.moveWait = 400 * time.Millisecond
	s.r.channelKnown = func(channel uint32) (bool, bool) {
		s.lock.Lock()
		defer s.lock.Unlock()

		return channel <= 4, s.connected
	}
	s.r.putPhone = func(session uint32, userID string) func() {
		s.lock.Lock()
		s.phones[session] = userID
		s.lock.Unlock()

		return func() {
			s.lock.Lock()
			delete(s.phones, session)
			s.lock.Unlock()
		}
	}
	s.r.accessGone = func(userID string) (bool, bool) {
		s.lock.Lock()
		defer s.lock.Unlock()

		if s.storeDown {
			return false, false
		}

		return s.gone[userID], true
	}
	s.r.readSession = func(_, sessionID string) voiceSessionRead {
		s.lock.Lock()
		defer s.lock.Unlock()

		return s.sessions[sessionID]
	}

	s.dial = voiceTalkDial{Address: s.server.address(), Own: "Matty", Hidden: map[string]bool{"mattermost": true}}

	t.Cleanup(func() { s.r.endAll(voiceEndingOff) })

	return s
}

// joined is a join that worked, and its peer.
type joined struct {
	peer   *fakeVoicePeer
	answer voiceJoined
}

// ask is a join by an account: Alice, Bob and Carol by their usernames,
// anybody else with their id for a username.
func (s *talkScene) ask(userID string, channel uint32, muted, deafened bool) voiceJoinAsk {
	username, ok := talkUsernames[userID]
	if !ok {
		username = userID
	}

	return voiceJoinAsk{
		UserID: userID, Username: username, SessionID: "session-" + userID,
		Channel: channel, Muted: muted, Deafened: deafened,
	}
}

func (s *talkScene) tryJoin(userID string, channel uint32, muted, deafened bool) (*fakeVoicePeer, voiceJoined, error) {
	peer := newFakeVoicePeer(4)
	answer, err := s.r.join(context.Background(), s.ask(userID, channel, muted, deafened), s.dial, func(*voiceSession) (voicePeer, error) {
		return peer, nil
	})

	return peer, answer, err
}

func (s *talkScene) join(userID string, channel uint32) joined {
	s.t.Helper()

	peer, answer, err := s.tryJoin(userID, channel, false, false)
	require.NoError(s.t, err)

	return joined{peer: peer, answer: answer}
}

func (s *talkScene) phoneOf(session uint32) (string, bool) {
	s.lock.Lock()
	defer s.lock.Unlock()

	userID, ok := s.phones[session]

	return userID, ok
}

func (s *talkScene) phoneCount() int {
	s.lock.Lock()
	defer s.lock.Unlock()

	return len(s.phones)
}

// refusal is an error as a refusal, failing when it is not one.
func refusal(t *testing.T, err error) *voiceRefusal {
	t.Helper()

	var refused *voiceRefusal
	require.True(t, errors.As(err, &refused), "%v", err)

	return refused
}

func TestVoicePhoneNames(t *testing.T) {
	first, fallback := voicePhoneNames(" dave ")
	assert.Equal(t, "dave (Matty)", first)
	assert.Equal(t, "dave.matty", fallback)
}

func TestVoiceTalkJoinsUnderTheAccountsName(t *testing.T) {
	s := newTalkScene(t)

	alice := s.join(talkAlice, 1)
	assert.Equal(t, "alice (Matty)", alice.answer.Name)
	assert.Equal(t, uint32(1), alice.answer.Channel)

	user, ok := s.server.user(alice.answer.Session)
	require.True(t, ok)
	assert.Equal(t, "alice (Matty)", user.name)
	assert.Equal(t, uint32(1), user.channel)

	userID, ok := s.phoneOf(alice.answer.Session)
	assert.True(t, ok, "the roster is told who the session is")
	assert.Equal(t, talkAlice, userID)
	assert.Equal(t, 1, s.r.count())
}

func TestVoiceTalkFallsBackToADottedName(t *testing.T) {
	s := newTalkScene(t)
	s.server.set(func(f *relayMumble) { f.badName = func(name string) bool { return strings.ContainsAny(name, " ()") } })

	alice := s.join(talkAlice, 0)
	assert.Equal(t, "alice.matty", alice.answer.Name)
	assert.Equal(t, 2, s.server.dialCount(), "both names were tried")

	// And when the voice server takes neither, the refusal of the name asked
	// for last: the first, now that the dotted one is asked for first.
	s.server.set(func(f *relayMumble) { f.badName = func(string) bool { return true } })
	_, _, err := s.tryJoin(talkBob, 0, false, false)
	refused := refusal(t, err)
	assert.Equal(t, http.StatusConflict, refused.Status)
	assert.Equal(t, codeVoiceRefused, refused.Code)
	assert.Equal(t, `The voice server did not accept the name "bob (Matty)".`, refused.Sentence)
}

func TestVoiceTalkNeverTakesAKeptName(t *testing.T) {
	s := newTalkScene(t)

	cases := map[string]voiceTalkDial{
		"the roster connection's own name": {Own: "ALICE (matty)"},
		"a hidden name":                    {Hidden: map[string]bool{"alice (matty)": true}},
	}
	for name, rules := range cases {
		dial := s.dial
		dial.Own, dial.Hidden = rules.Own, rules.Hidden

		_, err := s.r.join(context.Background(), s.ask(talkAlice, 0, false, false), dial, func(*voiceSession) (voicePeer, error) {
			return newFakeVoicePeer(4), nil
		})
		refused := refusal(t, err)
		assert.Equal(t, codeVoiceRefused, refused.Code, name)
		assert.Equal(t, `The name "alice (Matty)" is kept for something else on the voice server.`, refused.Sentence, name)
	}
	assert.Zero(t, s.server.dialCount(), "nothing was dialled for a kept name")

	// Another phone session's name, in any case.
	s.join(talkAlice, 0)
	talkUsernames["uid-alice-again"] = "ALICE"
	t.Cleanup(func() { delete(talkUsernames, "uid-alice-again") })
	_, _, err := s.tryJoin("uid-alice-again", 0, false, false)
	assert.Equal(t, `The name "ALICE (Matty)" is kept for something else on the voice server.`, refusal(t, err).Sentence)
	assert.Equal(t, 1, s.server.dialCount())
}

func TestVoiceTalkARefusedConnectionByType(t *testing.T) {
	cases := []struct {
		name     string
		server   func(f *relayMumble)
		sentence string
	}{
		{"a wrong password", func(f *relayMumble) { f.password = "the right one" }, "The voice server refused the password this server has for it."},
		{"both names registered", func(f *relayMumble) { f.registered = func(string) bool { return true } }, `The name "alice.matty" is registered to somebody else on the voice server.`},
		{"a full server", func(f *relayMumble) { f.full = true }, "The voice server is full."},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTalkScene(t)
			s.server.set(c.server)

			_, _, err := s.tryJoin(talkAlice, 1, false, false)
			refused := refusal(t, err)
			assert.Equal(t, http.StatusConflict, refused.Status)
			assert.Equal(t, codeVoiceRefused, refused.Code)
			assert.Equal(t, c.sentence, refused.Sentence)
			sentence, _ := s.r.lastRefusal()
			assert.Equal(t, c.sentence, sentence)
			assert.Zero(t, s.r.count())
			assert.Zero(t, s.phoneCount())
		})
	}

	t.Run("each type's sentence", func(t *testing.T) {
		for kind, sentence := range map[int32]string{
			1: "The voice server does not take connections from Matty's Mumble version.",
			3: `The name "dave (Matty)" is registered to somebody else on the voice server.`,
			4: "The voice server refused the password this server has for it.",
			5: `The name "dave (Matty)" is already in use on the voice server.`,
			7: "The voice server only lets in people with a certificate.",
			8: "The voice server refused the connection: no such account.",
		} {
			rejected := &voiceMumbleRejected{Type: MumbleProto.Reject_RejectType(kind), Reason: "no such account"}
			assert.Equal(t, sentence, voiceRejectedRefusal(rejected, "dave (Matty)").Sentence, kind)
		}
	})

	t.Run("nobody there", func(t *testing.T) {
		s := newTalkScene(t)
		s.dial.Address = "127.0.0.1:1"
		_, _, err := s.tryJoin(talkAlice, 1, false, false)
		refused := refusal(t, err)
		assert.Equal(t, http.StatusServiceUnavailable, refused.Status)
		assert.Equal(t, "The voice server could not be reached.", refused.Sentence)
	})
}

func TestVoiceTalkMoves(t *testing.T) {
	s := newTalkScene(t)

	// A fresh connection lands in the root: a join to channel 0 sends no
	// move, and is answered at once.
	started := time.Now()
	alice := s.join(talkAlice, 0)
	assert.Less(t, time.Since(started), s.r.moveWait, "answered at once")
	assert.Equal(t, uint32(0), alice.answer.Channel)

	channel, err := s.r.move(context.Background(), talkAlice, 2)
	require.NoError(t, err)
	assert.Equal(t, uint32(2), channel)
	user, _ := s.server.user(alice.answer.Session)
	assert.Equal(t, uint32(2), user.channel)

	t.Run("full", func(t *testing.T) {
		s.server.add("erin", 4)
		s.server.add("victor", 4)
		_, err := s.r.move(context.Background(), talkAlice, 4)
		refused := refusal(t, err)
		assert.Equal(t, http.StatusForbidden, refused.Status)
		assert.Equal(t, codeVoiceDenied, refused.Code)
		assert.Equal(t, "That channel is full.", refused.Sentence)
	})

	t.Run("denied", func(t *testing.T) {
		s.server.set(func(f *relayMumble) { f.deny[3] = "" })
		_, err := s.r.move(context.Background(), talkAlice, 3)
		assert.Equal(t, "The voice server did not let you into that channel.", refusal(t, err).Sentence)

		s.server.set(func(f *relayMumble) { f.deny[3] = "Ask an admin first" })
		_, err = s.r.move(context.Background(), talkAlice, 3)
		assert.Equal(t, "The voice server did not let you into that channel: Ask an admin first.", refusal(t, err).Sentence)
	})

	t.Run("unanswered", func(t *testing.T) {
		s.server.set(func(f *relayMumble) { f.silentMoves = true })
		defer s.server.set(func(f *relayMumble) { f.silentMoves = false })

		started := time.Now()
		_, err := s.r.move(context.Background(), talkAlice, 1)
		assert.GreaterOrEqual(t, time.Since(started), s.r.moveWait)
		refused := refusal(t, err)
		assert.Equal(t, codeVoiceDenied, refused.Code)
		assert.Equal(t, "The voice server did not let you into that channel.", refused.Sentence)
	})

	t.Run("a channel the roster does not know", func(t *testing.T) {
		_, err := s.r.move(context.Background(), talkAlice, 99)
		refused := refusal(t, err)
		assert.Equal(t, http.StatusNotFound, refused.Status)
		assert.Equal(t, codeNotFound, refused.Code)
		assert.Equal(t, "That voice channel does not exist.", refused.Sentence)
	})

	t.Run("the roster not connected", func(t *testing.T) {
		s.lock.Lock()
		s.connected = false
		s.lock.Unlock()
		defer func() {
			s.lock.Lock()
			s.connected = true
			s.lock.Unlock()
		}()

		_, err := s.r.move(context.Background(), talkAlice, 1)
		refused := refusal(t, err)
		assert.Equal(t, http.StatusServiceUnavailable, refused.Status)
		assert.Equal(t, "The voice server is not connected.", refused.Sentence)
	})

	t.Run("nobody in voice", func(t *testing.T) {
		_, err := s.r.move(context.Background(), talkBob, 1)
		refused := refusal(t, err)
		assert.Equal(t, http.StatusConflict, refused.Status)
		assert.Equal(t, "You are not in a voice channel.", refused.Sentence)
	})

	// Still where it was through all of that.
	assert.Equal(t, uint32(2), s.r.of(talkAlice).channel())

	// The admins' card shows the voice server's latest refusal.
	sentence, at := s.r.lastRefusal()
	assert.Equal(t, "The voice server did not let you into that channel.", sentence)
	assert.Equal(t, s.clock.Now(), at)
}

func TestVoiceTalkAFailedJoinLeavesNothingBehind(t *testing.T) {
	s := newTalkScene(t)
	s.server.add("erin", 4)
	s.server.add("victor", 4)

	_, _, err := s.tryJoin(talkAlice, 4, false, false)
	assert.Equal(t, "That channel is full.", refusal(t, err).Sentence)
	assert.Zero(t, s.r.count())
	assert.Zero(t, s.phoneCount())
	assert.Eventually(t, func() bool { return s.server.named("alice (Matty)") == 0 }, 5*time.Second, 10*time.Millisecond, "the connection went")

	// A peer that cannot be made, likewise.
	_, err = s.r.join(context.Background(), s.ask(talkAlice, 1, false, false), s.dial, func(*voiceSession) (voicePeer, error) {
		return nil, errors.New("no")
	})
	require.Error(t, err)
	assert.Zero(t, s.r.count())
	assert.Eventually(t, func() bool { return s.server.named("alice (Matty)") == 0 }, 5*time.Second, 10*time.Millisecond)
}

// speak is the phone sending n loud 20 ms packets, 20 ms apart on the clock.
func speak(s *talkScene, session *voiceSession, peer voicePeer, from uint32, n int) uint32 {
	for i := range n {
		session.fromPhone(peer, from+uint32(i*960), opus20ms, 30)
		s.clock.advance(20 * time.Millisecond)
	}

	return from + uint32(n*960)
}

func TestVoiceTalkPassesVoiceBothWays(t *testing.T) {
	s := newTalkScene(t)

	alice := s.join(talkAlice, 1)
	bob := s.join(talkBob, 1)
	carol := s.join(talkCarol, 2)
	aliceSession := s.r.of(talkAlice)

	speak(s, aliceSession, alice.peer, 0, 5)

	// Up: the voice server got Alice's voice, sequences counting 10 ms.
	require.Eventually(t, func() bool { return len(s.server.heardFrom(alice.answer.Session)) == 5 }, 5*time.Second, 5*time.Millisecond)
	var sequences []int64
	for _, audio := range s.server.heardFrom(alice.answer.Session) {
		sequences = append(sequences, audio.sequence)
		assert.Equal(t, opus20ms, audio.payload)
	}
	assert.Equal(t, []int64{0, 2, 4, 6, 8}, sequences)

	talking, _ := alice.peer.talking()
	assert.Equal(t, []uint32{alice.answer.Session}, talking, "her own session while her voice goes up")

	// Down: Bob's phone got it on a track, as RTP, and hears her talking;
	// Carol, elsewhere, does not.
	require.Eventually(t, func() bool { return len(bob.peer.on(0)) == 5 }, 5*time.Second, 5*time.Millisecond)
	assert.True(t, bob.peer.on(0)[0].Marker)
	require.Eventually(t, func() bool {
		talking, _ := bob.peer.talking()
		return len(talking) == 1 && talking[0] == alice.answer.Session
	}, 5*time.Second, 5*time.Millisecond)
	assert.Empty(t, carol.peer.on(0))

	// The hangover ends it: a last frame at the voice server, and Bob's
	// slot freed by it.
	s.clock.advance(400 * time.Millisecond)
	require.Eventually(t, func() bool {
		heard := s.server.heardFrom(alice.answer.Session)
		return len(heard) == 6 && heard[5].final && len(heard[5].payload) == 0 && heard[5].sequence == 10
	}, 5*time.Second, 5*time.Millisecond)
	require.Eventually(t, func() bool {
		talking, _ := bob.peer.talking()
		return talking != nil && len(talking) == 0
	}, 5*time.Second, 5*time.Millisecond)
	talking, _ = alice.peer.talking()
	assert.Empty(t, talking)
	assert.Len(t, bob.peer.on(0), 5, "the empty last frame is not written")

	// Two made-up people in Bob's channel talk at once: two tracks.
	erin := s.server.add("erin", 1)
	dave := s.server.add("dave", 1)
	for i := range 3 {
		s.server.speak(erin, int64(i*2), spoken(1), false)
		s.server.speak(dave, int64(i*2), spoken(2), false)
	}
	require.Eventually(t, func() bool { return len(bob.peer.on(0))+len(bob.peer.on(1))+len(bob.peer.on(2)) == 11 }, 5*time.Second, 5*time.Millisecond)
	require.Eventually(t, func() bool {
		talking, _ := bob.peer.talking()
		return len(talking) == 2
	}, 5*time.Second, 5*time.Millisecond)
}

func TestVoiceTalkSendsNothingUpWhileMutedOrSuppressed(t *testing.T) {
	s := newTalkScene(t)

	alice := s.join(talkAlice, 1)
	session := s.r.of(talkAlice)

	// Self muted, as asked.
	muted, deafened, err := s.r.state(talkAlice, true, false)
	require.NoError(t, err)
	assert.True(t, muted)
	assert.False(t, deafened)
	require.Eventually(t, func() bool { user, _ := s.server.user(alice.answer.Session); return user.selfMute }, 5*time.Second, 5*time.Millisecond)
	ts := speak(s, session, alice.peer, 0, 5)

	// Deafened is muted too.
	muted, deafened, err = s.r.state(talkAlice, false, true)
	require.NoError(t, err)
	assert.True(t, muted)
	assert.True(t, deafened)
	ts = speak(s, session, alice.peer, ts, 5)

	// Unmuted as asked: the voice goes up only once the voice server holds
	// the session unmuted, and the phone is told it does.
	_, _, err = s.r.state(talkAlice, false, false)
	require.NoError(t, err)
	require.Eventually(t, func() bool { self, _ := alice.peer.lastSelf(); return !self.SelfMuted && !self.SelfDeafened }, 5*time.Second, 5*time.Millisecond)
	user, _ := s.server.user(alice.answer.Session)
	assert.False(t, user.selfMute || user.selfDeaf)

	// Server muted, then suppressed: the phone is told, and nothing goes up.
	s.server.serverMute(alice.answer.Session, true, false)
	require.Eventually(t, func() bool { self, _ := alice.peer.lastSelf(); return self.Muted }, 5*time.Second, 5*time.Millisecond)
	ts = speak(s, session, alice.peer, ts, 5)

	s.server.serverMute(alice.answer.Session, false, true)
	require.Eventually(t, func() bool { self, _ := alice.peer.lastSelf(); return !self.Muted && self.Suppressed }, 5*time.Second, 5*time.Millisecond)
	ts = speak(s, session, alice.peer, ts, 5)

	time.Sleep(100 * time.Millisecond)
	assert.Empty(t, s.server.heardFrom(alice.answer.Session))

	// Free again.
	s.server.serverMute(alice.answer.Session, false, false)
	require.Eventually(t, func() bool { self, _ := alice.peer.lastSelf(); return !self.Muted && !self.Suppressed }, 5*time.Second, 5*time.Millisecond)
	speak(s, session, alice.peer, ts, 2)
	require.Eventually(t, func() bool { return len(s.server.heardFrom(alice.answer.Session)) == 2 }, 5*time.Second, 5*time.Millisecond)
}

func TestVoiceTalkTellsThePhoneWhereItIs(t *testing.T) {
	s := newTalkScene(t)

	alice := s.join(talkAlice, 1)
	session := s.r.of(talkAlice)

	session.greet(alice.peer)
	self, ok := alice.peer.lastSelf()
	require.True(t, ok)
	assert.Equal(t, voiceSelfState{Channel: 1}, self)
	talking, ok := alice.peer.talking()
	require.True(t, ok, "the talking set too, empty")
	assert.Equal(t, []uint32{}, talking)

	// An admin moves her.
	s.server.moveOther(alice.answer.Session, 3)
	require.Eventually(t, func() bool { self, _ := alice.peer.lastSelf(); return self.Channel == 3 }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, uint32(3), session.channel())

	// A peer that is not the session's is told nothing.
	other := newFakeVoicePeer(4)
	session.greet(other)
	assert.Empty(t, other.all())
}

func TestVoiceTalkEndings(t *testing.T) {
	t.Run("leave", func(t *testing.T) {
		s := newTalkScene(t)
		alice := s.join(talkAlice, 1)

		s.r.leave(talkAlice)
		assert.Zero(t, s.r.count())
		assert.Zero(t, s.phoneCount())
		require.Eventually(t, alice.peer.isClosed, 5*time.Second, 5*time.Millisecond)
		_, told := alice.peer.ended()
		assert.False(t, told, "the phone asked: nothing to tell it")
		require.Eventually(t, func() bool { _, there := s.server.user(alice.answer.Session); return !there }, 5*time.Second, 5*time.Millisecond)

		// Leaving with no session is fine.
		s.r.leave(talkAlice)
	})

	for _, c := range []struct {
		name, code, reason string
		ban                bool
		why                string
	}{
		{"kicked", voiceEndKicked, "You were removed from the voice server: spam.", false, "spam"},
		{"banned", voiceEndBanned, "You were banned from the voice server.", true, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newTalkScene(t)
			alice := s.join(talkAlice, 1)

			s.server.kick(alice.answer.Session, c.why, c.ban)
			require.Eventually(t, alice.peer.isClosed, 5*time.Second, 5*time.Millisecond)
			end, told := alice.peer.ended()
			require.True(t, told)
			assert.Equal(t, voiceEndSignal{End: c.code, Reason: c.reason}, end)
			assert.Zero(t, s.r.count())
			assert.Zero(t, s.phoneCount())
		})
	}

	t.Run("the voice connection lost", func(t *testing.T) {
		s := newTalkScene(t)
		alice := s.join(talkAlice, 1)

		s.server.set(func(f *relayMumble) { _ = f.users[alice.answer.Session].conn.Close() })
		require.Eventually(t, alice.peer.isClosed, 5*time.Second, 5*time.Millisecond)
		end, _ := alice.peer.ended()
		assert.Equal(t, voiceEndSignal{End: voiceEndMumble, Reason: "The connection to the voice server was lost."}, end)
		assert.Zero(t, s.r.count())
	})

	t.Run("talk switched off, or the settings changed", func(t *testing.T) {
		s := newTalkScene(t)
		alice := s.join(talkAlice, 1)
		bob := s.join(talkBob, 2)

		s.r.endAll(voiceEndingOff)
		require.Eventually(t, alice.peer.isClosed, 5*time.Second, 5*time.Millisecond)
		require.Eventually(t, bob.peer.isClosed, 5*time.Second, 5*time.Millisecond)
		end, _ := alice.peer.ended()
		assert.Equal(t, voiceEndSignal{End: voiceEndOff, Reason: "Talking from Matty was switched off on this server."}, end)
		assert.Zero(t, s.r.count())

		carol := s.join(talkCarol, 1)
		s.r.endAll(voiceEndingSettings)
		require.Eventually(t, carol.peer.isClosed, 5*time.Second, 5*time.Millisecond)
		end, _ = carol.peer.ended()
		assert.Equal(t, voiceEndSignal{End: voiceEndMumble, Reason: "The voice server's settings changed."}, end)
	})

	t.Run("access gone, or signed out", func(t *testing.T) {
		s := newTalkScene(t)
		alice := s.join(talkAlice, 1)
		bob := s.join(talkBob, 1)
		carol := s.join(talkCarol, 1)

		s.lock.Lock()
		s.gone[talkAlice] = true
		s.sessions["session-"+talkBob] = voiceSessionOver
		s.lock.Unlock()

		s.r.sweep()
		require.Eventually(t, alice.peer.isClosed, 5*time.Second, 5*time.Millisecond)
		require.Eventually(t, bob.peer.isClosed, 5*time.Second, 5*time.Millisecond)
		end, _ := alice.peer.ended()
		assert.Equal(t, voiceEndSignal{End: voiceEndOff, Reason: "You can no longer use voice on this server."}, end)
		end, _ = bob.peer.ended()
		assert.Equal(t, voiceEndSignal{End: voiceEndSession, Reason: "Your Mattermost session has ended."}, end)
		assert.False(t, carol.peer.isClosed())
		assert.Equal(t, 1, s.r.count())
	})

	t.Run("a session that cannot be read", func(t *testing.T) {
		s := newTalkScene(t)
		alice := s.join(talkAlice, 1)
		bob := s.join(talkBob, 1)

		s.lock.Lock()
		s.sessions["session-"+talkAlice] = voiceSessionUnread
		s.sessions["session-"+talkBob] = voiceSessionUnread
		s.storeDown = true
		s.lock.Unlock()

		// Nothing reads: the database is away, and nobody goes.
		s.r.sweep()
		s.r.sweep()
		assert.Equal(t, 2, s.r.count())

		// Bob's reads again: Alice's has been unread two sweeps in a row,
		// while others read, so it is over.
		s.lock.Lock()
		s.sessions["session-"+talkBob] = voiceSessionStands
		s.lock.Unlock()
		s.r.sweep()
		require.Eventually(t, alice.peer.isClosed, 5*time.Second, 5*time.Millisecond)
		assert.False(t, bob.peer.isClosed())
	})

	// Mattermost answers a signed-out session with an error that is not a
	// 404: somebody alone in voice still ends, at the second sweep, as the
	// store answers the access read made for them.
	t.Run("alone and signed out", func(t *testing.T) {
		s := newTalkScene(t)
		carol := s.join(talkCarol, 1)

		s.lock.Lock()
		s.sessions["session-"+talkCarol] = voiceSessionUnread
		s.lock.Unlock()

		s.r.sweep()
		assert.Equal(t, 1, s.r.count(), "one sweep unread ends nobody")
		assert.False(t, carol.peer.isClosed())

		s.r.sweep()
		require.Eventually(t, carol.peer.isClosed, 5*time.Second, 5*time.Millisecond)
		end, _ := carol.peer.ended()
		assert.Equal(t, voiceEndSignal{End: voiceEndSession, Reason: "Your Mattermost session has ended."}, end)
		assert.Zero(t, s.r.count())
	})

	// Alone, with neither the session nor the store answering: the database
	// is away, and nobody goes however long it lasts.
	t.Run("alone with the database away", func(t *testing.T) {
		s := newTalkScene(t)
		carol := s.join(talkCarol, 1)

		s.lock.Lock()
		s.sessions["session-"+talkCarol] = voiceSessionUnread
		s.storeDown = true
		s.lock.Unlock()

		for range 4 {
			s.r.sweep()
		}
		assert.Equal(t, 1, s.r.count())
		assert.False(t, carol.peer.isClosed())

		// A sweep that reads it standing again starts the count afresh.
		s.lock.Lock()
		s.sessions["session-"+talkCarol] = voiceSessionStands
		s.storeDown = false
		s.lock.Unlock()
		s.r.sweep()
		s.lock.Lock()
		s.sessions["session-"+talkCarol] = voiceSessionUnread
		s.lock.Unlock()
		s.r.sweep()
		assert.Equal(t, 1, s.r.count(), "unread once since it stood")
		assert.False(t, carol.peer.isClosed())
	})

	t.Run("at most so many at once", func(t *testing.T) {
		s := newTalkScene(t)
		s.r.most = 2
		s.join(talkAlice, 1)
		s.join(talkBob, 1)

		_, _, err := s.tryJoin(talkCarol, 1, false, false)
		refused := refusal(t, err)
		assert.Equal(t, http.StatusServiceUnavailable, refused.Status)
		assert.Equal(t, "Voice from Matty is full on this server. Try again later.", refused.Sentence)

		// Somebody already in may still join again.
		s.join(talkAlice, 2)
	})
}

func TestVoiceTalkAPhoneThatGoesIsWaitedFor(t *testing.T) {
	s := newTalkScene(t)

	alice := s.join(talkAlice, 1)
	session := s.r.of(talkAlice)
	dials := s.server.dialCount()

	session.peerGone(alice.peer)
	require.Eventually(t, alice.peer.isClosed, 5*time.Second, 5*time.Millisecond)
	require.Eventually(t, func() bool { user, _ := s.server.user(alice.answer.Session); return user.selfDeaf }, 5*time.Second, 5*time.Millisecond, "self deafened while it waits")

	// Voice from the old peer goes nowhere.
	speak(s, session, alice.peer, 0, 3)

	// Back within the wait, in the channel it is already in: taken over,
	// with no new connection, and answered at once.
	s.clock.advance(44 * time.Second)
	started := time.Now()
	again := s.join(talkAlice, 1)
	assert.Less(t, time.Since(started), s.r.moveWait)
	assert.Equal(t, alice.answer.Session, again.answer.Session)
	assert.Equal(t, dials, s.server.dialCount())
	require.Eventually(t, func() bool { user, _ := s.server.user(alice.answer.Session); return !user.selfDeaf && !user.selfMute }, 5*time.Second, 5*time.Millisecond)
	assert.Empty(t, s.server.heardFrom(alice.answer.Session))

	// The old wait does not end it.
	s.clock.advance(10 * time.Second)
	assert.Equal(t, 1, s.r.count())

	// Gone again, and not back: it ends after the wait, quietly.
	session.peerGone(again.peer)
	s.clock.advance(44 * time.Second)
	assert.Equal(t, 1, s.r.count())
	s.clock.advance(time.Second)
	assert.Zero(t, s.r.count())
	assert.Zero(t, s.phoneCount())
	require.Eventually(t, func() bool { _, there := s.server.user(alice.answer.Session); return !there }, 5*time.Second, 5*time.Millisecond)
	_, told := again.peer.ended()
	assert.False(t, told)
}

func TestVoiceTalkAJoinReplacesTheSessionsPeer(t *testing.T) {
	s := newTalkScene(t)

	first := s.join(talkAlice, 1)
	dials := s.server.dialCount()

	second := s.join(talkAlice, 2)
	assert.Equal(t, first.answer.Session, second.answer.Session, "the same Mumble session")
	assert.Equal(t, uint32(2), second.answer.Channel)
	assert.Equal(t, dials, s.server.dialCount(), "no new connection")

	require.Eventually(t, first.peer.isClosed, 5*time.Second, 5*time.Millisecond)
	end, _ := first.peer.ended()
	assert.Equal(t, voiceEndSignal{End: voiceEndReplaced, Reason: "You joined voice again from another connection."}, end)
	assert.False(t, second.peer.isClosed())

	// Voice comes from the new peer only.
	session := s.r.of(talkAlice)
	speak(s, session, first.peer, 0, 2)
	speak(s, session, second.peer, 0, 2)
	require.Eventually(t, func() bool { return len(s.server.heardFrom(first.answer.Session)) == 2 }, 5*time.Second, 5*time.Millisecond)

	// A join to a full channel by somebody already in leaves them where they
	// were, with their peer.
	s.server.add("erin", 4)
	s.server.add("victor", 4)
	_, _, err := s.tryJoin(talkAlice, 4, false, false)
	assert.Equal(t, "That channel is full.", refusal(t, err).Sentence)
	assert.False(t, second.peer.isClosed())
	assert.Equal(t, uint32(2), session.channel())
	assert.Equal(t, 1, s.r.count())
}

func TestVoiceBudget(t *testing.T) {
	at := time.Date(2026, 11, 19, 21, 0, 0, 0, time.UTC)

	var b voiceBudget
	b.note(at)
	for i := range 7 {
		_, ok := b.take(at.Add(time.Duration(i+1)*time.Second), false)
		require.True(t, ok, i)
	}

	// The ninth in two minutes waits until the oldest of the last eight is
	// two minutes old.
	wait, ok := b.take(at.Add(30*time.Second), false)
	assert.False(t, ok)
	assert.Equal(t, 90*time.Second, wait)

	// With the voice server's autoban off, counted and let through.
	_, ok = b.take(at.Add(30*time.Second), true)
	assert.True(t, ok)

	// The roster connection is never refused, and counts.
	b.note(at.Add(31 * time.Second))
	wait, ok = b.take(at.Add(32*time.Second), false)
	assert.False(t, ok)
	assert.Equal(t, 90*time.Second, wait, "the oldest of the last eight is now the one at 2 s")

	_, ok = b.take(at.Add(122*time.Second), false)
	assert.True(t, ok)
}

func TestVoiceTalkKeepsToTheBudget(t *testing.T) {
	s := newTalkScene(t)

	// The roster connection's own dial counts.
	s.r.noteRosterDial()

	// Seven people join and leave.
	for i := range 7 {
		person := "person-" + itoa(i)
		s.join(person, 0)
		s.r.leave(person)
		s.clock.advance(time.Second)
	}
	assert.Equal(t, 7, s.server.dialCount())

	_, _, err := s.tryJoin("person-7", 0, false, false)
	refused := refusal(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, refused.Status)
	assert.Equal(t, codeUnavailable, refused.Code)
	assert.Equal(t, "Too many people joined voice just now. Try again in a moment.", refused.Sentence)
	assert.Equal(t, 113*time.Second, refused.RetryAfter)
	assert.Equal(t, 7, s.server.dialCount(), "nothing was dialled")

	recorder := httptest.NewRecorder()
	refused.write(recorder)
	assert.JSONEq(t, `{"error":"Too many people joined voice just now. Try again in a moment.","code":"unavailable","retryAfterMs":113000}`, recorder.Body.String())
	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)

	// With the autoban off, through.
	s.dial.NoBudget = true
	s.join("person-7", 0)
	s.r.leave("person-7")

	// A name fallback costs two.
	s.dial.NoBudget = false
	s.clock.advance(119 * time.Second)
	s.server.set(func(f *relayMumble) { f.badName = func(name string) bool { return strings.Contains(name, " ") } })
	before := s.server.dialCount()
	s.join(talkAlice, 0)
	assert.Equal(t, before+2, s.server.dialCount())
}

// Each person has a share of the budget, two new connections in two
// minutes, so that one person joining and leaving over and over cannot keep
// everybody else out.
func TestVoiceTalkAPersonsShareOfTheBudget(t *testing.T) {
	s := newTalkScene(t)

	s.join(talkAlice, 1)
	s.r.leave(talkAlice)
	s.clock.advance(10 * time.Second)
	s.join(talkAlice, 1)

	// Taking her session over makes no new connection, and costs nothing.
	s.join(talkAlice, 2)
	s.r.leave(talkAlice)
	s.clock.advance(10 * time.Second)
	dials := s.server.dialCount()
	assert.Equal(t, 2, dials)

	_, _, err := s.tryJoin(talkAlice, 1, false, false)
	refused := refusal(t, err)
	assert.Equal(t, http.StatusTooManyRequests, refused.Status)
	assert.Equal(t, codeRateLimited, refused.Code)
	assert.Equal(t, "You have joined voice too often just now. Try again in a moment.", refused.Sentence)
	assert.Equal(t, 100*time.Second, refused.RetryAfter, "until the first of the two is two minutes old")
	assert.Equal(t, dials, s.server.dialCount(), "nothing was dialled")

	recorder := httptest.NewRecorder()
	refused.write(recorder)
	assert.JSONEq(t, `{"error":"You have joined voice too often just now. Try again in a moment.","code":"rate_limited","retryAfterMs":100000}`, recorder.Body.String())

	// Somebody else still joins.
	s.join(talkBob, 1)

	// And Alice once the first is two minutes old.
	s.clock.advance(100 * time.Second)
	s.join(talkAlice, 1)
}

func TestVoiceTalkCountsTheRosterConnectionsDial(t *testing.T) {
	s, _ := liveSoundScene(t)

	r := s.p.voiceTalk()
	r.budget.lock.Lock()
	dials := len(r.budget.dials)
	r.budget.lock.Unlock()
	assert.Equal(t, 1, dials)
}

// The plugin's own reads in the sweep: Dave, alone in voice, signs out, and
// Mattermost answers his session with a 400. His access read answers, so
// the second sweep ends him; with the store away as well, nothing does.
func TestVoiceTalkEndsSomebodyAloneWhoSignedOut(t *testing.T) {
	a := newAccessScene(t)
	a.api.On("GetSession", "session-"+sceneLinked).Return(nil, model.NewAppError("GetSession", "app.session.get.app_error", nil, "", http.StatusBadRequest))

	s := newTalkScene(t)
	s.r.accessGone = a.p.voiceAccessGone
	s.r.readSession = a.p.voiceReadSession

	dave := s.join(sceneLinked, 1)

	a.faults.lock.Lock()
	a.faults.readFails[kvLinkByUserPrefix+sceneLinked] = true
	a.faults.lock.Unlock()
	for range 3 {
		s.r.sweep()
	}
	assert.Equal(t, 1, s.r.count(), "the database away ends nobody")
	assert.False(t, dave.peer.isClosed())

	a.faults.lock.Lock()
	delete(a.faults.readFails, kvLinkByUserPrefix+sceneLinked)
	a.faults.lock.Unlock()
	s.r.sweep()
	require.Eventually(t, dave.peer.isClosed, 5*time.Second, 5*time.Millisecond)
	end, _ := dave.peer.ended()
	assert.Equal(t, voiceEndSignal{End: voiceEndSession, Reason: "Your Mattermost session has ended."}, end)
	assert.Zero(t, s.r.count())
}

func TestVoiceTalkReadsThePluginsState(t *testing.T) {
	s := newAccessScene(t)

	t.Run("channels", func(t *testing.T) {
		known, connected := s.p.voiceChannelKnown(1)
		assert.False(t, known)
		assert.False(t, connected)

		root := uint32(0)
		s.p.setVoice(voiceState{connected: true, channels: []voiceChannel{{ID: 0, Name: "Test"}, {ID: 1, Name: "Lobby", Parent: &root, Depth: 1}}})
		known, connected = s.p.voiceChannelKnown(1)
		assert.True(t, known)
		assert.True(t, connected)
		known, _ = s.p.voiceChannelKnown(4)
		assert.False(t, known)
	})

	t.Run("access", func(t *testing.T) {
		for userID, want := range map[string]bool{sceneLinked: false, sceneLetIn: false, sceneLocked: true} {
			gone, answered := s.p.voiceAccessGone(userID)
			assert.Equal(t, want, gone, userID)
			assert.True(t, answered, userID)
		}

		// A store that does not answer takes nobody's voice away, and says
		// it did not answer.
		s.faults.lock.Lock()
		s.faults.readFails[kvLinkByUserPrefix+sceneLocked] = true
		s.faults.lock.Unlock()
		gone, answered := s.p.voiceAccessGone(sceneLocked)
		assert.False(t, gone)
		assert.False(t, answered)
	})

	t.Run("sessions", func(t *testing.T) {
		s.api.On("GetSession", "standing").Return(&model.Session{Id: "standing", UserId: sceneLinked, ExpiresAt: model.GetMillis() + 60000}, nil)
		s.api.On("GetSession", "expired").Return(&model.Session{Id: "expired", UserId: sceneLinked, ExpiresAt: 1}, nil)
		s.api.On("GetSession", "missing").Return(nil, model.NewAppError("GetSession", "app.session.get.app_error", nil, "", http.StatusNotFound))
		// A signed-out session, as Mattermost answers it: not a 404, and
		// no different from the database failing, so unread; the sweep's
		// second unread look ends it.
		s.api.On("GetSession", "revoked").Return(nil, model.NewAppError("GetSession", "app.session.get.app_error", nil, "", http.StatusBadRequest))
		s.api.On("GetSession", "unreadable").Return(nil, model.NewAppError("GetSession", "app.session.get.app_error", nil, "", http.StatusInternalServerError))

		assert.Equal(t, voiceSessionStands, s.p.voiceReadSession(sceneLinked, "standing"))
		assert.Equal(t, voiceSessionOver, s.p.voiceReadSession(sceneLetIn, "standing"), "somebody else's")
		assert.Equal(t, voiceSessionOver, s.p.voiceReadSession(sceneLinked, "expired"))
		assert.Equal(t, voiceSessionOver, s.p.voiceReadSession(sceneLinked, "missing"))
		assert.Equal(t, voiceSessionUnread, s.p.voiceReadSession(sceneLinked, "revoked"))
		assert.Equal(t, voiceSessionUnread, s.p.voiceReadSession(sceneLinked, "unreadable"))
	})
}

// The voice server says "registered" (WrongUserPW) for a name somebody
// registered: the other name is tried, as for an invalid one.
func TestVoiceTalkARegisteredNameIsTheOtherNamesTurn(t *testing.T) {
	s := newTalkScene(t)
	s.server.set(func(f *relayMumble) { f.registered = func(name string) bool { return name == "alice (Matty)" } })

	alice := s.join(talkAlice, 1)
	assert.Equal(t, "alice.matty", alice.answer.Name)
	assert.Equal(t, 2, s.server.dialCount())

	// One registered name says nothing of the voice server's rule for
	// names: the next person is asked for under the first name.
	bob := s.join(talkBob, 1)
	assert.Equal(t, "bob (Matty)", bob.answer.Name)
	assert.Equal(t, 3, s.server.dialCount())
}

// Once the voice server has refused "<username> (Matty)" as invalid and taken
// "<username>.matty", the latter is asked for first, until the voice
// server's settings change.
func TestVoiceTalkAsksFirstForTheNameTheServerTakes(t *testing.T) {
	s := newTalkScene(t)
	s.server.set(func(f *relayMumble) { f.badName = func(name string) bool { return strings.ContainsAny(name, " ()") } })

	alice := s.join(talkAlice, 1)
	assert.Equal(t, "alice.matty", alice.answer.Name)
	assert.Equal(t, 2, s.server.dialCount())

	bob := s.join(talkBob, 1)
	assert.Equal(t, "bob.matty", bob.answer.Name)
	assert.Equal(t, 3, s.server.dialCount(), "one connection")

	s.r.forgetNames()
	carol := s.join(talkCarol, 1)
	assert.Equal(t, "carol.matty", carol.answer.Name)
	assert.Equal(t, 5, s.server.dialCount(), "both names again")

	// A voice server that takes the first name after all, and says no to
	// the other: the first is asked for first again.
	s.server.set(func(f *relayMumble) { f.badName = func(name string) bool { return strings.HasSuffix(name, ".matty") } })
	dave := s.join("dave", 1)
	assert.Equal(t, "dave (Matty)", dave.answer.Name)
	assert.Equal(t, 7, s.server.dialCount())
	erin := s.join("erin", 1)
	assert.Equal(t, "erin (Matty)", erin.answer.Name)
	assert.Equal(t, 8, s.server.dialCount())
}

// The app joins again on a new connection while the old one is found gone:
// whatever order the two run in, the new connection ends heard and hearing,
// neither muted nor deafened on the voice server.
func TestVoiceTalkAPhoneGoneDuringATakeoverLeavesTheNewOneHeard(t *testing.T) {
	s := newTalkScene(t)

	first := s.join(talkAlice, 1)
	session := s.r.of(talkAlice)

	second := newFakeVoicePeer(4)
	gone := make(chan struct{})
	_, err := s.r.join(context.Background(), s.ask(talkAlice, 1, false, false), s.dial, func(*voiceSession) (voicePeer, error) {
		// The old connection found gone after the mute and deafen asked for
		// went to the voice server, while the new one is being answered.
		go func() {
			session.peerGone(first.peer)
			close(gone)
		}()
		select {
		case <-gone:
		case <-time.After(200 * time.Millisecond):
		}

		return second, nil
	})
	require.NoError(t, err)

	select {
	case <-gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the old connection's going never finished")
	}

	require.Eventually(t, func() bool {
		user, _ := s.server.user(first.answer.Session)
		return !user.selfDeaf && !user.selfMute
	}, 5*time.Second, 5*time.Millisecond, "neither muted nor deafened on the voice server")
	require.Eventually(t, first.peer.isClosed, 5*time.Second, 5*time.Millisecond)

	// Up: passed on by the voice server.
	speak(s, session, second, 0, 3)
	require.Eventually(t, func() bool { return len(s.server.relayedFrom(first.answer.Session)) == 3 }, 5*time.Second, 5*time.Millisecond)

	// Down: somebody in her channel is heard on the new connection.
	erin := s.server.add("erin", 1)
	s.server.speak(erin, 0, spoken(1), false)
	require.Eventually(t, func() bool { return len(second.on(0)) == 1 }, 5*time.Second, 5*time.Millisecond)
}

// A connection already found gone when its join attaches it (its port closed
// under it while it was answered) is waited for as any other.
func TestVoiceTalkAPeerGoneBeforeItIsTakenIsWaitedFor(t *testing.T) {
	s := newTalkScene(t)

	peer := newFakeVoicePeer(4)
	peer.gone = true
	answer, err := s.r.join(context.Background(), s.ask(talkAlice, 1, false, false), s.dial, func(*voiceSession) (voicePeer, error) {
		return peer, nil
	})
	require.NoError(t, err)

	require.Eventually(t, peer.isClosed, 5*time.Second, 5*time.Millisecond)
	require.Eventually(t, func() bool { user, _ := s.server.user(answer.Session); return user.selfDeaf }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, 1, s.r.count())

	s.clock.advance(45 * time.Second)
	assert.Zero(t, s.r.count(), "ended after the wait")
}

// A Mumble admin who kicks or bans somebody while their join is under way
// has the join refused with what they said: not a connection lost, which
// the app would join again after.
func TestVoiceTalkAKickDuringAJoinIsAnsweredAsTheVoiceServersWord(t *testing.T) {
	for _, c := range []struct {
		name     string
		ban      bool
		sentence string
	}{
		{"kicked", false, "You were removed from the voice server: spam."},
		{"banned", true, "You were banned from the voice server: spam."},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newTalkScene(t)
			s.server.set(func(f *relayMumble) { f.silentMoves = true })

			// While her move waits for an answer.
			go func() {
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					if session := s.server.named("alice (Matty)"); session != 0 {
						time.Sleep(50 * time.Millisecond)
						s.server.kick(session, "spam", c.ban)

						return
					}
					time.Sleep(5 * time.Millisecond)
				}
			}()

			_, _, err := s.tryJoin(talkAlice, 2, false, false)
			refused := refusal(t, err)
			assert.Equal(t, http.StatusForbidden, refused.Status)
			assert.Equal(t, codeVoiceDenied, refused.Code)
			assert.Equal(t, c.sentence, refused.Sentence)
			assert.Zero(t, s.r.count())
		})
	}
}

// A Mumble admin deafens somebody, which mutes them too, then undeafens
// them alone: the voice server keeps them muted, and so does the phone's
// session, and nothing of their voice goes up.
func TestVoiceTalkUndeafenedByTheVoiceServerStaysMuted(t *testing.T) {
	s := newTalkScene(t)

	alice := s.join(talkAlice, 1)
	session := s.r.of(talkAlice)
	selves := func() int {
		n := 0
		for _, signal := range alice.peer.all() {
			if _, ok := signal.(voiceSelfSignal); ok {
				n++
			}
		}

		return n
	}

	s.server.serverDeaf(alice.answer.Session, true)
	require.Eventually(t, func() bool { self, _ := alice.peer.lastSelf(); return self.Muted }, 5*time.Second, 5*time.Millisecond)

	told := selves()
	s.server.serverDeaf(alice.answer.Session, false)
	require.Eventually(t, func() bool { return selves() > told }, 5*time.Second, 5*time.Millisecond, "the undeafen is read")
	self, _ := alice.peer.lastSelf()
	assert.True(t, self.Muted, "still muted by the voice server")

	speak(s, session, alice.peer, 0, 5)
	time.Sleep(100 * time.Millisecond)
	assert.Empty(t, s.server.heardFrom(alice.answer.Session), "nothing goes up")
	talking, _ := alice.peer.talking()
	assert.NotContains(t, talking, alice.answer.Session)

	// Unmuted by an admin: free again.
	s.server.serverMute(alice.answer.Session, false, false)
	require.Eventually(t, func() bool { self, _ := alice.peer.lastSelf(); return !self.Muted }, 5*time.Second, 5*time.Millisecond)
	speak(s, session, alice.peer, 10000, 2)
	require.Eventually(t, func() bool { return len(s.server.relayedFrom(alice.answer.Session)) == 2 }, 5*time.Second, 5*time.Millisecond)
}

// Muting while talking ends the spell before the mute reaches the voice
// server, which passes nothing on from somebody self muted: so the others
// get its last frame.
func TestVoiceTalkMutingEndsTheSpellBeforeTheMute(t *testing.T) {
	s := newTalkScene(t)

	alice := s.join(talkAlice, 1)
	session := s.r.of(talkAlice)

	speak(s, session, alice.peer, 0, 3)
	require.Eventually(t, func() bool { return len(s.server.relayedFrom(alice.answer.Session)) == 3 }, 5*time.Second, 5*time.Millisecond)

	_, _, err := s.r.state(talkAlice, true, false)
	require.NoError(t, err)
	require.Eventually(t, func() bool { user, _ := s.server.user(alice.answer.Session); return user.selfMute }, 5*time.Second, 5*time.Millisecond)

	relayed := s.server.relayedFrom(alice.answer.Session)
	require.Len(t, relayed, 4, "the last frame was passed on")
	assert.True(t, relayed[3].final)
	assert.Empty(t, relayed[3].payload)
	assert.Equal(t, int64(6), relayed[3].sequence)
}
