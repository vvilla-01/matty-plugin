package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"layeh.com/gumble/gumble/MumbleProto"
)

// A phone session's own mute and deafen against a voice server that limits
// a user's own state messages as Murmur does (5 at once, then 1 a second,
// the rest dropped without a word) and answers nothing to a message that
// changes nothing, the connection's handshake counted against the limit as
// Murmur counts it. What the session goes by is the voice server's word, and
// four sequences that, sent as they come, leave a person self muted on the
// voice server while their phone shows them unmuted and talking, end heard.

// Murmur's limit as the fake keeps it: five at once, then one a second in
// whole messages, its clock started again at each drain.
func TestTheFakeVoiceServerLimitsAsMurmurDoes(t *testing.T) {
	at := time.Date(2026, 11, 23, 20, 0, 0, 0, time.UTC)
	b := relayBucket{since: at}

	for i := range 5 {
		require.True(t, b.take(at.Add(time.Duration(i)*10*time.Millisecond), 5, 1), i)
	}
	assert.False(t, b.take(at.Add(100*time.Millisecond), 5, 1), "the sixth at once is dropped")
	assert.False(t, b.take(at.Add(900*time.Millisecond), 5, 1), "and so is one before a whole second")
	assert.True(t, b.take(at.Add(1500*time.Millisecond), 5, 1), "a second later, one more")

	// The drain at 1.5 s started the clock again there: half a second
	// later nothing has drained.
	assert.False(t, b.take(at.Add(2000*time.Millisecond), 5, 1))
	assert.True(t, b.take(at.Add(2500*time.Millisecond), 5, 1))
}

// Sequence 22 as Murmur 1.5.939 took it: a connection made at 0, whose
// handshake counts one message, then the move at 27 ms, the join's state at
// 28 ms, and mute, unmute, mute and unmute 300 ms apart from 1033 ms. Murmur
// dropped the last, and so does the fake, from the allowance a connection to
// it starts with.
func TestTheFakeVoiceServerDropsWhatMurmurDropped(t *testing.T) {
	s := newTalkScene(t)
	alice := s.join(talkAlice, 0)
	require.Empty(t, s.server.receivedFrom(alice.answer.Session), "the join sent nothing of its own")
	user, ok := s.server.user(alice.answer.Session)
	require.True(t, ok)
	require.Equal(t, 1, user.bucket.filled, "the handshake counted")

	at := time.Date(2026, 11, 23, 16, 5, 41, 577_000_000, time.UTC)
	b := relayBucket{filled: user.bucket.filled, since: at}

	for i, ms := range []int{27, 28, 1033, 1336, 1639} {
		require.True(t, b.take(at.Add(time.Duration(ms)*time.Millisecond), 5, 1), "message %d", i+1)
	}
	assert.False(t, b.take(at.Add(1943*time.Millisecond), 5, 1), "the sixth message is dropped")
}

// The voice server's word on the session's own mute and deafen is read from
// its UserState, coupled as it couples them, and kept apart from an admin's.
func TestTheVoiceServersWordOnTheSessionsOwnStateIsRead(t *testing.T) {
	self := mergeVoiceSelf(voiceMumbleSelf{}, &MumbleProto.UserState{SelfDeaf: proto.Bool(true)})
	assert.Equal(t, voiceSelfPair{mute: true, deaf: true}, self.held(), "a self deafen self mutes")
	assert.False(t, self.muted(), "and is not an admin's mute")

	self = mergeVoiceSelf(self, &MumbleProto.UserState{SelfMute: proto.Bool(false)})
	assert.Equal(t, voiceSelfPair{}, self.held(), "a self unmute self undeafens")

	self = mergeVoiceSelf(self, &MumbleProto.UserState{SelfMute: proto.Bool(true), SelfDeaf: proto.Bool(false)})
	assert.Equal(t, voiceSelfPair{mute: true}, self.held())

	self = mergeVoiceSelf(self, &MumbleProto.UserState{Mute: proto.Bool(true)})
	assert.Equal(t, voiceSelfPair{mute: true}, self.held())
	assert.True(t, self.Mute)
}

// requirePaced holds a session's own messages to the plugin's pace: none
// dropped by the voice server, and, by the times the plugin sent them, at
// least voiceStateGap between two.
func requirePaced(t *testing.T, s *talkScene, session uint32) {
	t.Helper()

	var received []relayReceived
	require.Eventually(t, func() bool {
		received = s.server.receivedFrom(session)
		return len(received) == len(s.sendsOf(session))
	}, 5*time.Second, 5*time.Millisecond, "every message sent arrived")

	for i, message := range received {
		require.False(t, message.dropped, "message %d was dropped", i)
	}

	sends := s.sendsOf(session)
	for i := 1; i < len(sends); i++ {
		require.GreaterOrEqual(t, sends[i].at.Sub(sends[i-1].at), voiceStateGap, "messages %d and %d", i-1, i)
	}
}

// The pace has a floor: at least a second between two of a session's own
// messages, and at least two seconds of waiting for the voice server's word
// before a message is sent again.
func TestTheSessionsPaceIsNeverFaster(t *testing.T) {
	require.GreaterOrEqual(t, voiceStateGap, time.Second)
	require.GreaterOrEqual(t, voiceStateResend, 2*time.Second)
}

// moveSentAt is when the plugin sent the session's latest move.
func moveSentAt(t *testing.T, s *talkScene, session uint32) time.Time {
	t.Helper()

	sends := s.sendsOf(session)
	for i := len(sends) - 1; i >= 0; i-- {
		if sends[i].move {
			return sends[i].at
		}
	}
	require.Fail(t, "no move was sent")

	return time.Time{}
}

// requireHeard waits until the voice server holds the session neither self
// muted nor self deafened, and the session goes by that; then the phone
// speaks, the voice server passes it on, and the phone hears its own
// talking.
func requireHeard(t *testing.T, s *talkScene, userID string, peer *fakeVoicePeer, from uint32) {
	t.Helper()

	session := s.r.of(userID)
	require.NotNil(t, session)
	number := session.number()

	require.Eventually(t, func() bool {
		user, _ := s.server.user(number)
		session.lock.Lock()
		held := session.heldMuted || session.heldDeafened
		session.lock.Unlock()

		return !user.selfMute && !user.selfDeaf && !held
	}, 10*time.Second, 10*time.Millisecond, "the voice server holds the session unmuted")

	session.greet(peer)
	self, _ := peer.lastSelf()
	assert.False(t, self.SelfMuted, "the phone is told so")
	assert.False(t, self.SelfDeafened)

	before := len(s.server.relayedFrom(number))
	speak(s, session, peer, from, 5)
	require.Eventually(t, func() bool { return len(s.server.relayedFrom(number)) == before+5 }, 5*time.Second, 5*time.Millisecond, "heard")
	talking, _ := peer.talking()
	assert.Contains(t, talking, number)
}

// Sequence 15 as it was measured: eight changes about 50 ms apart, the last
// unmuted, from a second after the join's move. Sent as they came, the
// third change, a mute, would be the last the voice server takes, and the
// person would stay self muted there; now one goes in each turn, the newest
// wanted, and the session ends unmuted at the voice server.
func TestVoiceTalkRapidChangesEndHeard(t *testing.T) {
	s := newTalkScene(t)

	alice := s.join(talkAlice, 1)
	time.Sleep(time.Second)
	for range 4 {
		for _, muted := range []bool{true, false} {
			answeredMuted, _, err := s.r.state(talkAlice, muted, false)
			require.NoError(t, err)
			assert.Equal(t, muted, answeredMuted, "answered at once with what was asked")
			time.Sleep(50 * time.Millisecond)
		}
	}
	time.Sleep(1500 * time.Millisecond)

	requireHeard(t, s, talkAlice, alice.peer, 0)
	requirePaced(t, s, alice.answer.Session)
}

// Sequence 19: the app closes its connection and joins again, three times
// in a row about 1.3 s apart, as it does on a poor network. Each time the
// session waits for its phone self deafened, and each join takes it over.
func TestVoiceTalkReconnectsInARowEndHeard(t *testing.T) {
	s := newTalkScene(t)

	first := s.join(talkAlice, 1)
	session := s.r.of(talkAlice)
	peer := first.peer
	time.Sleep(time.Second)

	for range 3 {
		session.peerGone(peer)
		time.Sleep(300 * time.Millisecond)

		again := s.join(talkAlice, 1)
		assert.Equal(t, first.answer.Session, again.answer.Session, "taken over")
		peer = again.peer
		time.Sleep(time.Second)
	}

	requireHeard(t, s, talkAlice, peer, 0)
	requirePaced(t, s, first.answer.Session)
}

// Sequence 20: one reconnect, then mute, unmute, mute and unmute 250 ms
// apart.
func TestVoiceTalkAReconnectThenTapsEndHeard(t *testing.T) {
	s := newTalkScene(t)

	first := s.join(talkAlice, 1)
	session := s.r.of(talkAlice)
	time.Sleep(time.Second)

	session.peerGone(first.peer)
	time.Sleep(300 * time.Millisecond)
	again := s.join(talkAlice, 1)
	time.Sleep(time.Second)

	for range 2 {
		for _, muted := range []bool{true, false} {
			_, _, err := s.r.state(talkAlice, muted, false)
			require.NoError(t, err)
			time.Sleep(250 * time.Millisecond)
		}
	}

	requireHeard(t, s, talkAlice, again.peer, 0)
	requirePaced(t, s, first.answer.Session)
}

// Sequence 22: a join, then mute, unmute, mute and unmute 300 ms apart.
// Sent as they came, the last would be the sixth message in two seconds
// with the handshake counted, which Murmur drops (as the fake does); now
// each goes in its turn, the newest wanted, and the session ends unmuted at
// the voice server.
func TestVoiceTalkTapsAfterAJoinEndHeard(t *testing.T) {
	s := newTalkScene(t)

	alice := s.join(talkAlice, 1)
	time.Sleep(time.Second)

	firstTap := time.Now()
	for range 2 {
		for _, muted := range []bool{true, false} {
			_, _, err := s.r.state(talkAlice, muted, false)
			require.NoError(t, err)
			time.Sleep(300 * time.Millisecond)
		}
	}
	taps := time.Since(firstTap)

	requireHeard(t, s, talkAlice, alice.peer, 0)
	requirePaced(t, s, alice.answer.Session)

	// One a turn while the taps last, and the newest after them: two for
	// taps within a second, as they come on a machine that keeps up.
	most := int(taps/voiceStateGap) + 2
	assert.LessOrEqual(t, len(s.server.statesFrom(alice.answer.Session)), most, "at most one state a turn for four taps in %v", taps)
}

// A state the voice server drops is sent again once its word has not
// followed for two seconds, and until it does the voice stays shut: nothing
// goes up, the phone's own session is not talking, and the phone is told
// the voice server still holds it muted. The voice server drops the unmute
// twice, so that the voice is still shut for four seconds after it was
// first sent, whatever the machine's pace.
func TestVoiceTalkADroppedStateIsSentAgain(t *testing.T) {
	s := newTalkScene(t)

	peer, answer, err := s.tryJoin(talkAlice, 1, true, false)
	require.NoError(t, err)
	session := s.r.of(talkAlice)
	require.Eventually(t, func() bool { user, _ := s.server.user(answer.Session); return user.selfMute }, 5*time.Second, 5*time.Millisecond)

	s.server.set(func(f *relayMumble) { f.dropStates = 2 })
	muted, deafened, err := s.r.state(talkAlice, false, false)
	require.NoError(t, err)
	assert.False(t, muted, "answered at once with what was asked")
	assert.False(t, deafened)

	require.Eventually(t, func() bool {
		states := s.server.statesFrom(answer.Session)
		return len(states) == 2 && states[1].dropped
	}, 5*time.Second, 5*time.Millisecond, "the unmute went out and was dropped")

	next := speak(s, session, peer, 0, 5)
	time.Sleep(100 * time.Millisecond)
	assert.Empty(t, s.server.heardFrom(answer.Session), "nothing goes up while the voice server holds her muted")
	talking, _ := peer.talking()
	assert.NotContains(t, talking, answer.Session, "and she is not talking")
	session.greet(peer)
	self, _ := peer.lastSelf()
	assert.True(t, self.SelfMuted, "the phone is told the voice server's word")
	user, _ := s.server.user(answer.Session)
	assert.True(t, user.selfMute)

	require.Eventually(t, func() bool { return len(s.server.statesFrom(answer.Session)) == 4 }, 10*time.Second, 5*time.Millisecond, "sent again, twice")
	states := s.server.statesFrom(answer.Session)
	assert.True(t, states[2].dropped)
	assert.False(t, states[3].dropped)
	assert.False(t, *states[3].selfMute)

	// By the plugin's own clock: the move, the mute, then the unmute three
	// times, each again after two seconds without the voice server's word.
	sends := s.sendsOf(answer.Session)
	require.Len(t, sends, 5)
	assert.True(t, sends[0].move)
	for i := 3; i < 5; i++ {
		assert.GreaterOrEqual(t, sends[i].at.Sub(sends[i-1].at), voiceStateResend, "send %d", i)
	}

	requireHeard(t, s, talkAlice, peer, next)
	time.Sleep(2500 * time.Millisecond)
	assert.Len(t, s.server.statesFrom(answer.Session), 4, "nothing more once the word followed")
}

// Nothing is sent that would change nothing: a join to where the session
// is, unmuted as it is, sends nothing, and neither does a change undone
// before its turn. (A machine slow enough to undo it after its turn rightly
// sends both; the session still ends heard.)
func TestVoiceTalkSendsNothingThatChangesNothing(t *testing.T) {
	s := newTalkScene(t)

	alice := s.join(talkAlice, 0)
	for range 3 {
		_, _, err := s.r.state(talkAlice, false, false)
		require.NoError(t, err)
	}
	again := s.join(talkAlice, 0)
	time.Sleep(1500 * time.Millisecond)
	assert.Empty(t, s.server.receivedFrom(alice.answer.Session))

	channel, err := s.r.move(context.Background(), talkAlice, 1)
	require.NoError(t, err)
	assert.Equal(t, uint32(1), channel)
	_, _, err = s.r.state(talkAlice, true, false)
	require.NoError(t, err)
	_, _, err = s.r.state(talkAlice, false, false)
	require.NoError(t, err)
	undone := time.Now()
	time.Sleep(1500 * time.Millisecond)

	if undone.Sub(moveSentAt(t, s, alice.answer.Session)) >= voiceStateGap {
		requireHeard(t, s, talkAlice, again.peer, 0)
		return
	}
	received := s.server.receivedFrom(alice.answer.Session)
	require.Len(t, received, 1, "the move alone")
	assert.True(t, received[0].move)
}

// A phone that comes back to its waiting session costs one state message,
// not two: the join asks for its mute and deafen once, and attaching the
// phone sends nothing more.
func TestVoiceTalkATakeoverSendsItsStateOnce(t *testing.T) {
	s := newTalkScene(t)

	alice := s.join(talkAlice, 1)
	session := s.r.of(talkAlice)
	session.peerGone(alice.peer)
	require.Eventually(t, func() bool { user, _ := s.server.user(alice.answer.Session); return user.selfDeaf }, 5*time.Second, 5*time.Millisecond)

	again := s.join(talkAlice, 1)
	requireHeard(t, s, talkAlice, again.peer, 0)
	time.Sleep(2500 * time.Millisecond)

	states := s.server.statesFrom(alice.answer.Session)
	require.Len(t, states, 2, "the wait's self deafen, then the join's state")
	assert.True(t, *states[0].selfDeaf)
	assert.False(t, *states[1].selfMute)
	assert.False(t, *states[1].selfDeaf)
	requirePaced(t, s, alice.answer.Session)
}

// A move and the state go in messages of their own, the move first: a join
// muted into a channel sends the move, then the mute a second later.
func TestVoiceTalkAMoveGoesBeforeTheState(t *testing.T) {
	s := newTalkScene(t)

	_, answer, err := s.tryJoin(talkAlice, 2, true, false)
	require.NoError(t, err)
	assert.Equal(t, uint32(2), answer.Channel)
	require.Eventually(t, func() bool { user, _ := s.server.user(answer.Session); return user.selfMute }, 5*time.Second, 5*time.Millisecond)

	received := s.server.receivedFrom(answer.Session)
	require.Len(t, received, 2)
	assert.True(t, received[0].move)
	assert.Nil(t, received[0].selfMute, "the move alone")
	assert.False(t, received[1].move)
	assert.True(t, *received[1].selfMute)
	requirePaced(t, s, answer.Session)
}

// The voice server's word and a tap cross each other on two goroutines, each
// deciding whether voice may go up: whichever lands last, the uplink ends
// blocked exactly when the newest inputs say so.
func TestVoiceTalkTheNewestDecisionBlocksTheUplink(t *testing.T) {
	s := newTalkScene(t)
	s.join(talkAlice, 0)
	session := s.r.of(talkAlice)

	// Made in the order the race once took: the word's decision first, a
	// tap's after it, and the word's landing last.
	session.lock.Lock()
	session.heldMuted = false
	word := session.blockLocked()
	session.lock.Unlock()
	require.NoError(t, s.r.setState(session, true, false, false))
	session.up.setBlockedAs(word)
	session.up.lock.Lock()
	blocked := session.up.blocked
	session.up.lock.Unlock()
	assert.True(t, blocked, "an older decision does not undo a newer one")

	bad := 0
	for i := range 20000 {
		said := session.mumble.current()
		said.SelfMute, said.SelfDeaf = i%3 == 0, false

		done := make(chan struct{}, 2)
		go func() { session.mumbleSelf(said); done <- struct{}{} }()
		go func() { _ = s.r.setState(session, i%2 == 0, false, false); done <- struct{}{} }()
		<-done
		<-done

		session.lock.Lock()
		want := session.blockedLocked()
		session.lock.Unlock()
		session.up.lock.Lock()
		got := session.up.blocked
		session.up.lock.Unlock()
		if got != want {
			bad++
		}
	}
	assert.Zero(t, bad, "times the uplink disagreed with its inputs")
}

// A join that takes over a session and then fails leaves the session as it
// was: the person's own mute and deafen as before, and the uplink shut or
// open as before, with nothing new asked of the voice server. A session
// waiting for its phone stays self deafened there for the wait; one whose
// phone is still attached keeps its voice going up. Each with a join that
// asks for what the session has, and with one that asks to be muted and
// deafened.
func TestVoiceTalkAFailedTakeoverLeavesTheWaitAsItWas(t *testing.T) {
	uplinkBlocked := func(session *voiceSession) bool {
		session.up.lock.Lock()
		defer session.up.lock.Unlock()

		return session.up.blocked
	}

	for _, row := range []struct {
		name            string
		waiting         bool
		muted, deafened bool
	}{
		{"waiting, asking for the same", true, false, false},
		{"waiting, asking to be muted and deafened", true, true, true},
		{"attached, asking for the same", false, false, false},
		{"attached, asking to be muted and deafened", false, true, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			s := newTalkScene(t)

			alice := s.join(talkAlice, 1)
			session := s.r.of(talkAlice)
			states := 0
			if row.waiting {
				session.peerGone(alice.peer)
				require.Eventually(t, func() bool { user, _ := s.server.user(alice.answer.Session); return user.selfDeaf }, 5*time.Second, 5*time.Millisecond)
				states = 1
			}
			blocked := uplinkBlocked(session)
			assert.Equal(t, row.waiting, blocked, "shut for the wait alone")

			_, err := s.r.join(context.Background(), s.ask(talkAlice, 1, row.muted, row.deafened), s.dial, func(*voiceSession) (voicePeer, error) {
				return nil, errors.New("no answer for the phone")
			})
			require.Error(t, err)
			assert.Same(t, session, s.r.of(talkAlice), "the session stays")
			assert.Equal(t, voiceSelfPair{}, session.wanted(), "the person's own mute and deafen as before")
			assert.Equal(t, blocked, uplinkBlocked(session), "the uplink as before")

			time.Sleep(voiceStateResend + 500*time.Millisecond)
			user, _ := s.server.user(alice.answer.Session)
			assert.Equal(t, row.waiting, user.selfDeaf, "self deafened for the wait alone")
			assert.Len(t, s.server.statesFrom(alice.answer.Session), states, "nothing new asked")
			if !row.waiting {
				assert.False(t, alice.peer.isClosed(), "the attached phone stays")
				requireHeard(t, s, talkAlice, alice.peer, 0)
			}
		})
	}
}

// A move whose context ends gives up its turn and is never sent, and a leave
// after it is not held up.
func TestVoiceTalkAMoveWhoseContextEndsGivesUpItsTurn(t *testing.T) {
	s := newTalkScene(t)

	alice := s.join(talkAlice, 1)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)

	asked := time.Now()
	_, err := s.r.move(ctx, talkAlice, 2)
	require.Error(t, err)
	if time.Since(moveSentAt(t, s, alice.answer.Session)) >= voiceStateGap {
		t.Skip("the machine was too slow to ask within the turn after the join's move")
	}
	assert.Less(t, time.Since(asked), voiceStateGap, "it did not wait for its turn")

	left := time.Now()
	s.r.leave(talkAlice)
	assert.Less(t, time.Since(left), 500*time.Millisecond, "the leave is not held up")
	assert.Len(t, s.sendsOf(alice.answer.Session), 1, "the join's move alone")
}

// A person who taps again for what they already asked, while the voice
// server still holds otherwise, has it sent again as soon as the session's
// pace allows, not only after the two seconds of waiting for the voice
// server's word.
func TestVoiceTalkAskingAgainSendsAgainInTheNextTurn(t *testing.T) {
	s := newTalkScene(t)

	_, answer, err := s.tryJoin(talkAlice, 0, true, false)
	require.NoError(t, err)
	require.Eventually(t, func() bool { user, _ := s.server.user(answer.Session); return user.selfMute }, 5*time.Second, 5*time.Millisecond)

	s.server.set(func(f *relayMumble) { f.dropStates = 1 })
	_, _, err = s.r.state(talkAlice, false, false)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		states := s.server.statesFrom(answer.Session)
		return len(states) == 2 && states[1].dropped
	}, 5*time.Second, 5*time.Millisecond, "the unmute went out and was dropped")

	_, _, err = s.r.state(talkAlice, false, false)
	require.NoError(t, err)
	tapped := time.Now()

	require.Eventually(t, func() bool { user, _ := s.server.user(answer.Session); return !user.selfMute }, 5*time.Second, 5*time.Millisecond)
	sends := s.sendsOf(answer.Session)
	require.Len(t, sends, 3, "the mute, the unmute dropped, the unmute again")
	assert.GreaterOrEqual(t, sends[2].at.Sub(sends[1].at), voiceStateGap, "in its turn")
	if tapped.Sub(sends[1].at) < voiceStateGap {
		assert.Less(t, sends[2].at.Sub(sends[1].at), voiceStateResend, "in the next turn")
	}
}
