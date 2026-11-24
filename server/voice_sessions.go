package main

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pion/rtp"
	"layeh.com/gumble/gumble/MumbleProto"
)

// The voice sessions this node holds for people talking from Matty.
//
// One per Mattermost account: a Mumble connection of the person's own
// (voice_mumble.go), and while their phone is connected, its WebRTC peer
// (voicePeer), with the voice between them passed on (voice_talk.go).
//
// A phone that drops off (a tunnel, a lift, a network change) does not end
// the session at once. The Mumble connection stays for voiceTalkWait, self
// deafened, and the phone's next join takes it over without a new
// connection to the voice server. Every other ending is final: leaving, the
// voice server ending the connection, talk switched off, the voice server's
// settings changed, the person's access gone, or the Mattermost session that
// joined signed out.

const (
	// How long a session whose phone has gone stays for the phone to come
	// back.
	voiceTalkWait = 45 * time.Second

	// How many sessions a node holds at once.
	voiceTalkMost = 50

	// The plugin's own budget of connections to the voice server, below the
	// autoban of Murmur's defaults (10 connections from one address in 120
	// s), so that the plugin, every phone session of which comes from its
	// address, is never banned by the voice server.
	voiceBudgetDials  = 8
	voiceBudgetWindow = 120 * time.Second

	// Each person's share of the budget: new connections to the voice
	// server their joins may make, so that nobody can use up the node's
	// alone. A join that takes over the person's session makes none.
	voicePersonDials  = 2
	voicePersonWindow = 2 * time.Minute

	// How often the sessions are checked against access and sign-in.
	voiceTalkSweepEvery = time.Minute
)

// How a session ended, as the data channel's "end" says it.
const (
	voiceEndKicked   = "kicked"
	voiceEndBanned   = "banned"
	voiceEndMumble   = "mumble"
	voiceEndReplaced = "replaced"
	voiceEndOff      = "off"
	voiceEndSession  = "session"
)

// What a person reads.
const (
	// Endings.
	voiceKickedText          = "You were removed from the voice server."
	voiceBannedText          = "You were banned from the voice server."
	voiceLostText            = "The connection to the voice server was lost."
	voiceReplacedText        = "You joined voice again from another connection."
	voiceTalkOffText         = "Talking from Matty was switched off on this server."
	voiceAccessGoneText      = "You can no longer use voice on this server."
	voiceSignedOutText       = "Your Mattermost session has ended."
	voiceSettingsChangedText = "The voice server's settings changed."
	voiceRestartingText      = "Voice is restarting on this server."

	// Refusals of a join, a move or a change.
	voiceBudgetText              = "Too many people joined voice just now. Try again in a moment."
	voicePersonBudgetText        = "You have joined voice too often just now. Try again in a moment."
	voiceTalkFullText            = "Voice from Matty is full on this server. Try again later."
	voiceNotConnectedText        = "The voice server is not connected."
	voiceUnreachableText         = "The voice server could not be reached."
	voiceNoAnswerText            = "The voice server did not answer."
	voiceNoChannelText           = "That voice channel does not exist."
	voiceNotInVoiceText          = "You are not in a voice channel."
	voiceNoUsernameText          = "Your account has no username to join voice with."
	voiceDeniedText              = "The voice server did not let you into that channel."
	voiceChannelFullText         = "That channel is full."
	voiceRejectedText            = "The voice server refused the connection."
	voiceRejectedPasswordText    = "The voice server refused the password this server has for it."
	voiceRejectedFullText        = "The voice server is full."
	voiceRejectedVersionText     = "The voice server does not take connections from Matty's Mumble version."
	voiceRejectedCertificateText = "The voice server only lets in people with a certificate."
)

// voiceNameKeptText is a name the plugin will not take on the voice server.
func voiceNameKeptText(name string) string {
	return `The name "` + name + `" is kept for something else on the voice server.`
}

// voiceNameInvalidText is a name the voice server would not take.
func voiceNameInvalidText(name string) string {
	return `The voice server did not accept the name "` + name + `".`
}

// voiceNameInUseText is a name somebody else holds on the voice server.
func voiceNameInUseText(name string) string {
	return `The name "` + name + `" is already in use on the voice server.`
}

// voiceNameRegisteredText is a name somebody registered on the voice server.
func voiceNameRegisteredText(name string) string {
	return `The name "` + name + `" is registered to somebody else on the voice server.`
}

// voiceEnding is why a session ended. Code is the data channel's "end"; a
// session that ended with no code (the person left, or their phone never
// came back) tells its phone nothing.
type voiceEnding struct {
	Code   string
	Reason string
}

// The endings decided here, rather than by the voice server.
var (
	voiceEndingOff      = voiceEnding{Code: voiceEndOff, Reason: voiceTalkOffText}
	voiceEndingAccess   = voiceEnding{Code: voiceEndOff, Reason: voiceAccessGoneText}
	voiceEndingSession  = voiceEnding{Code: voiceEndSession, Reason: voiceSignedOutText}
	voiceEndingSettings = voiceEnding{Code: voiceEndMumble, Reason: voiceSettingsChangedText}
	voiceEndingReplaced = voiceEnding{Code: voiceEndReplaced, Reason: voiceReplacedText}

	// The plugin stopping: deactivated, restarted or upgraded. Coded as the
	// voice server lost, so that the phone joins again once it is back.
	voiceEndingRestart = voiceEnding{Code: voiceEndMumble, Reason: voiceRestartingText}
)

// voiceRefusal is a join, a move or a change refused, with what the route
// answers: its status, its code, the sentence and, when waiting helps, how
// long.
type voiceRefusal struct {
	Status     int
	Code       string
	Sentence   string
	RetryAfter time.Duration
}

func (r *voiceRefusal) Error() string { return r.Sentence }

// The refusals' codes of the voice routes' own.
const (
	codeVoiceDenied  = "voice_denied"
	codeVoiceRefused = "voice_refused"
)

// write answers the refusal with the plugin's usual body, never cached.
func (r *voiceRefusal) write(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, r.Status, errorResponse{Error: r.Sentence, Code: r.Code, RetryAfterMs: retryAfterMs(r.RetryAfter)})
}

func voiceUnavailableRefusal(sentence string, retry time.Duration) *voiceRefusal {
	return &voiceRefusal{Status: http.StatusServiceUnavailable, Code: codeUnavailable, Sentence: sentence, RetryAfter: retry}
}

func voiceDeniedRefusal(sentence string) *voiceRefusal {
	return &voiceRefusal{Status: http.StatusForbidden, Code: codeVoiceDenied, Sentence: sentence}
}

func voiceRefusedRefusal(sentence string) *voiceRefusal {
	return &voiceRefusal{Status: http.StatusConflict, Code: codeVoiceRefused, Sentence: sentence}
}

func voiceLostRefusal() *voiceRefusal {
	return voiceUnavailableRefusal(voiceLostText, 0)
}

// voiceRefusalOf is any error a join, move or change returned, as a
// refusal: its own, or the voice server not reached.
func voiceRefusalOf(err error) *voiceRefusal {
	var refusal *voiceRefusal
	if errors.As(err, &refusal) {
		return refusal
	}

	return voiceUnavailableRefusal(voiceUnreachableText, 0)
}

// voicePeer is a phone's WebRTC side as a session sees it. The listener
// (P2b's pion peer) makes it; everything here calls it from goroutines of
// its own, never from inside a pion callback.
type voicePeer interface {
	// slots is how many audio tracks the phone receives on (at most four
	// are used).
	slots() int

	// writeSlot writes one RTP packet on a track; only the payload type
	// and SSRC are the peer's to fill in.
	writeSlot(slot int, packet *rtp.Packet) error

	// signal sends one message on the data channel "voice" as JSON:
	// voiceTalkingSignal, voiceSelfSignal or voiceEndSignal. A message
	// sent before the channel is open may be dropped: greet sends the
	// latest ones again once it opens.
	signal(message any)

	// close ends the peer. After an end signal it waits until the data
	// channel has sent everything, or a second, before it closes; it is
	// always called on a goroutine of its own.
	close()

	// isGone is whether the phone's connection has already been found gone
	// (closed, failed, or its port closed under it), which may happen
	// before its session takes it.
	isGone() bool
}

// The data channel's messages.
type (
	voiceTalkingSignal struct {
		Talking []uint32 `json:"talking"`
	}

	voiceSelfSignal struct {
		Self voiceSelfState `json:"self"`
	}

	voiceSelfState struct {
		Channel    uint32 `json:"channel"`
		Muted      bool   `json:"muted"`
		Suppressed bool   `json:"suppressed"`

		// The reader's own mute and deafen as the voice server holds them,
		// which may lag what was asked for, and differs from it while the
		// voice server has not taken it yet.
		SelfMuted    bool `json:"selfMuted"`
		SelfDeafened bool `json:"selfDeafened"`
	}

	voiceEndSignal struct {
		End    string `json:"end"`
		Reason string `json:"reason,omitempty"`
	}
)

// ---------------------------------------------------------------- the budget

// voiceBudget counts the connections this node opens to the voice server:
// the roster connection's, every phone session's, and every name fallback's,
// whether they were let in or not.
type voiceBudget struct {
	lock sync.Mutex

	// The last voiceBudgetDials of them, oldest first.
	dials []time.Time
}

// note counts a connection that is never refused: the roster connection's.
func (b *voiceBudget) note(now time.Time) {
	b.lock.Lock()
	defer b.lock.Unlock()

	b.addLocked(now)
}

// take counts a phone session's connection, or says how long until one
// fits: one that would be the ninth in two minutes waits until the oldest of
// the last eight is two minutes old. lifted counts it without refusing.
func (b *voiceBudget) take(now time.Time, lifted bool) (time.Duration, bool) {
	b.lock.Lock()
	defer b.lock.Unlock()

	if !lifted && len(b.dials) >= voiceBudgetDials {
		if wait := b.dials[len(b.dials)-voiceBudgetDials].Add(voiceBudgetWindow).Sub(now); wait > 0 {
			return wait, false
		}
	}

	b.addLocked(now)

	return 0, true
}

func (b *voiceBudget) addLocked(now time.Time) {
	b.dials = append(b.dials, now)
	if len(b.dials) > voiceBudgetDials {
		b.dials = append(b.dials[:0], b.dials[len(b.dials)-voiceBudgetDials:]...)
	}
}

// ---------------------------------------------------------------- a session

// voiceSession is one person's voice session.
type voiceSession struct {
	owner *voiceSessions

	// The Mattermost account that joined and its username.
	userID   string
	username string

	// The name it has on the voice server.
	name string

	// The Mattermost session of the join that holds it, whether that join
	// said it came from Matty, and the version it said, for a change of the
	// minimum (endOutdated) and the sweep. A join that takes the session over
	// writes all three under lock.
	sessionID string
	matty     bool
	version   string

	up   *voiceUplink
	down *voiceDownlink

	lock sync.Mutex

	mumble *voiceMumble
	peer   voicePeer

	// As the person asked, deafened being muted too.
	muted    bool
	deafened bool

	// As the voice server says: muted or deafened by an admin, not allowed
	// to speak, and the session's own mute and deafen as it holds them
	// (held), which lags what was asked for until its word follows.
	serverMuted  bool
	suppressed   bool
	heldMuted    bool
	heldDeafened bool

	// Counts the decisions on whether voice may go up (blockLocked).
	blockedAt uint64

	// The wait for a phone that has gone, and its generation.
	wait   voiceTimer
	waitAt uint64

	// Sweeps in a row that could not read the Mattermost session.
	unread int

	ended   bool
	release func()

	// The talking set last sent to the phone.
	talkLock    sync.Mutex
	talkSent    bool
	talkLastSet []uint32
}

// number is the session's number on the voice server.
func (s *voiceSession) number() uint32 {
	s.lock.Lock()
	defer s.lock.Unlock()

	if s.mumble == nil {
		return 0
	}

	return s.mumble.session
}

// channel is the channel the session is in.
func (s *voiceSession) channel() uint32 {
	s.lock.Lock()
	m := s.mumble
	s.lock.Unlock()

	if m == nil {
		return 0
	}

	return m.current().Channel
}

// mumbleEnding is how the session's connection to the voice server ended,
// when it has: kicked, banned or lost.
func (s *voiceSession) mumbleEnding() voiceEnding {
	s.lock.Lock()
	m := s.mumble
	s.lock.Unlock()

	if m == nil {
		return voiceEnding{}
	}

	return m.endedWith()
}

// fromPhone takes one Opus packet from the phone's microphone track: its
// RTP timestamp, payload and audio level (voiceLevelUnknown without one). A
// packet from a peer that is no longer the session's is dropped.
func (s *voiceSession) fromPhone(peer voicePeer, timestamp uint32, payload []byte, level uint8) {
	s.lock.Lock()
	current := s.peer == peer && !s.ended
	s.lock.Unlock()

	if current {
		s.up.packet(timestamp, payload, level)
	}
}

// greet sends the phone the session's state and who is talking, for a data
// channel that has just opened.
func (s *voiceSession) greet(peer voicePeer) {
	s.lock.Lock()
	if s.peer != peer || s.ended {
		s.lock.Unlock()
		return
	}
	self := s.selfLocked()
	s.lock.Unlock()

	peer.signal(voiceSelfSignal{Self: self})

	s.talkLock.Lock()
	s.talkSent = false
	s.talkLock.Unlock()
	s.talkingChanged()
}

// peerGone is the phone's peer gone (closed, failed, or disconnected too
// long): the session waits voiceTalkWait for the phone to come back.
func (s *voiceSession) peerGone(peer voicePeer) {
	s.owner.peerGone(s, peer)
}

// selfLocked is the session's state as the data channel's "self" says it.
func (s *voiceSession) selfLocked() voiceSelfState {
	state := voiceSelfState{Muted: s.serverMuted, Suppressed: s.suppressed, SelfMuted: s.heldMuted, SelfDeafened: s.heldDeafened}
	if s.mumble != nil {
		state.Channel = s.mumble.current().Channel
	}

	return state
}

// blockedLocked is whether nothing may go up: no phone, muted or deafened
// as the person asked, which stops it at once, or anything the voice server
// says stops it, their own mute or deafen as it holds them included. Voice
// that went up while the voice server holds the session muted would be
// passed on to nobody, and the phone would show its own talking all the same.
func (s *voiceSession) blockedLocked() bool {
	return s.peer == nil || s.muted || s.deafened || s.serverMuted || s.suppressed || s.heldMuted || s.heldDeafened
}

// blockLocked is the decision on whether voice may go up, from the inputs as
// they stand under the lock, numbered: the uplink takes a decision only when
// it is newer than the last it took, so that two made at once on different
// goroutines (the voice server's word and a tap, say) end on the newer
// inputs whichever lands last.
func (s *voiceSession) blockLocked() voiceBlock {
	s.blockedAt++

	return voiceBlock{blocked: s.blockedLocked(), at: s.blockedAt}
}

// talkingChanged sends the phone who is talking, when it changed: the
// sessions heard on its slots, and its own while its voice goes up. It reads
// the state as it is now, so calls that cross each other still end on the
// right set.
func (s *voiceSession) talkingChanged() {
	s.talkLock.Lock()
	defer s.talkLock.Unlock()

	s.lock.Lock()
	peer, m, ended := s.peer, s.mumble, s.ended
	s.lock.Unlock()

	if peer == nil || ended {
		s.talkSent, s.talkLastSet = false, nil
		return
	}

	set := s.down.speakers()
	if s.up.isTalking() && m != nil {
		set = append(set, m.session)
		slices.Sort(set)
	}
	if set == nil {
		set = []uint32{}
	}

	if s.talkSent && slices.Equal(set, s.talkLastSet) {
		return
	}
	s.talkSent, s.talkLastSet = true, set

	peer.signal(voiceTalkingSignal{Talking: set})
}

// The session as its Mumble connection's handler.

func (s *voiceSession) mumbleAudio(sender uint32, sequence int64, payload []byte, final bool) {
	s.down.packet(sender, sequence, payload, final)
}

func (s *voiceSession) mumbleSelf(self voiceMumbleSelf) {
	s.lock.Lock()
	s.serverMuted, s.suppressed = self.muted(), self.Suppressed
	s.heldMuted, s.heldDeafened = self.SelfMute, self.SelfDeaf
	block := s.blockLocked()
	peer := s.peer
	state := s.selfLocked()
	state.Channel = self.Channel
	s.lock.Unlock()

	s.up.setBlockedAs(block)
	if peer != nil {
		peer.signal(voiceSelfSignal{Self: state})
	}
}

func (s *voiceSession) mumbleGone(session uint32) {
	s.down.gone(session)
}

func (s *voiceSession) mumbleEnded(ending voiceEnding) {
	s.owner.finish(s, ending, nil)
}

// ---------------------------------------------------------------- the registry

// voiceTalkDial is where phone sessions connect and the rules for their
// names, from the settings.
type voiceTalkDial struct {
	// The voice server, host and port, and its password.
	Address  string
	Password string

	// The roster connection's own name, and the hidden names (lowered),
	// which a phone session never takes.
	Own    string
	Hidden map[string]bool

	// The voice server's autoban is off: the budget counts, and refuses
	// nothing.
	NoBudget bool
}

// voiceTalkDialOf is voiceTalkDial from the settings.
func voiceTalkDialOf(cfg *configuration) voiceTalkDial {
	return voiceTalkDial{
		Address:  withDefaultPort(cfg.MumbleServer, "64738"),
		Password: cfg.MumblePassword,
		Own:      strings.TrimSpace(cfg.MumbleUsername),
		Hidden:   cfg.hiddenVoiceNames(),
		NoBudget: cfg.VoiceTalkNoBudget,
	}
}

// voiceJoinAsk is a join as the route hands it over.
type voiceJoinAsk struct {
	// The Mattermost account, its username, and the Mattermost session the
	// join came from.
	UserID    string
	Username  string
	SessionID string

	Channel  uint32
	Muted    bool
	Deafened bool

	// Whether the join said it came from Matty, and the version it said
	// ("" when none can be read), from its User-Agent (mattyOf).
	Matty   bool
	Version string
}

// voiceJoined is a join's answer, without the SDP the route adds.
type voiceJoined struct {
	Session uint32 `json:"session"`
	Channel uint32 `json:"channel"`
	Name    string `json:"name"`
}

// voiceSessionRead is what reading a session's Mattermost session found.
type voiceSessionRead int

const (
	voiceSessionStands voiceSessionRead = iota
	voiceSessionOver
	voiceSessionUnread
)

// voiceSessions is the registry.
type voiceSessions struct {
	clock  voiceClock
	safely func(where string, f func()) bool

	// The roster connection's knowledge of a channel: whether it is there,
	// and whether the roster connection is connected at all.
	channelKnown func(channel uint32) (known, connected bool)

	// What the roster is told about each Mumble session this node holds
	// (Plugin.putVoicePhone): what it returns takes the session out again.
	putPhone func(session uint32, userID string) func()

	// Whether a person may still use voice, and whether the store answered
	// that question; and whether the Mattermost session they joined from
	// stands. For the sweep.
	accessGone  func(userID string) (gone, answered bool)
	readSession func(userID, sessionID string) voiceSessionRead

	// The oldest Matty this server answers, as in force when it is called
	// ("" for none): a join checks it once its session is its own.
	minimum func() string

	// The sentence a Matty's session joined from a Mattermost session ends
	// with under a minimum, for the version its phone proved, or "": with
	// the app check at "require", a pass kept with an older version
	// (Plugin.voiceProvenBelow).
	provenBelow func(userID, sessionID, minimum string) string

	// How a phone session connects: dialVoiceMumble, or a test's.
	dial func(ctx context.Context, dial voiceMumbleDial, handler voiceMumbleHandler, safely func(string, func()) bool) (*voiceMumble, error)

	wait     time.Duration
	most     int
	moveWait time.Duration

	budget voiceBudget

	lock   sync.Mutex
	byUser map[string]*voiceSession
	users  map[string]*voiceUserLock

	// Counts endAll: a join that began before one ends what it made.
	era uint64

	// The last time the voice server refused a phone session (a connection
	// or a channel), for the admins' card.
	refused   string
	refusedAt time.Time

	// When each person's joins made a new connection to the voice server
	// lately, for their share of the budget.
	personal map[string][]time.Time

	// The voice server refused "<username> (Matty)" as an invalid name and
	// took "<username>.matty": the latter is asked for first from then on,
	// until the voice server's settings change.
	fallbackFirst bool
}

// voiceUserLock runs one person's joins, moves, changes and leaves one at a
// time.
type voiceUserLock struct {
	sync.Mutex
	holders int
}

// newVoiceSessions is an empty registry with the given time and safely,
// whose other parts the caller fills in.
func newVoiceSessions(clock voiceClock, safely func(string, func()) bool) *voiceSessions {
	return &voiceSessions{
		clock:        clock,
		safely:       safely,
		channelKnown: func(uint32) (bool, bool) { return false, false },
		putPhone:     func(uint32, string) func() { return func() {} },
		accessGone:   func(string) (bool, bool) { return false, true },
		readSession:  func(string, string) voiceSessionRead { return voiceSessionStands },
		minimum:      func() string { return "" },
		provenBelow:  func(string, string, string) string { return "" },
		dial:         dialVoiceMumble,
		wait:         voiceTalkWait,
		most:         voiceTalkMost,
		moveWait:     voiceMoveWait,
		byUser:       map[string]*voiceSession{},
		users:        map[string]*voiceUserLock{},
		personal:     map[string][]time.Time{},
	}
}

// lockUser takes one person's turn, and returns what gives it back.
func (r *voiceSessions) lockUser(userID string) func() {
	r.lock.Lock()
	held := r.users[userID]
	if held == nil {
		held = &voiceUserLock{}
		r.users[userID] = held
	}
	held.holders++
	r.lock.Unlock()

	held.Lock()

	return func() {
		held.Unlock()

		r.lock.Lock()
		held.holders--
		if held.holders == 0 {
			delete(r.users, userID)
		}
		r.lock.Unlock()
	}
}

// of is the person's session, or nil.
func (r *voiceSessions) of(userID string) *voiceSession {
	r.lock.Lock()
	defer r.lock.Unlock()

	return r.byUser[userID]
}

// count is how many sessions the node holds.
func (r *voiceSessions) count() int {
	r.lock.Lock()
	defer r.lock.Unlock()

	return len(r.byUser)
}

// all is every session, for endAll and the sweep.
func (r *voiceSessions) all() []*voiceSession {
	r.lock.Lock()
	defer r.lock.Unlock()

	sessions := make([]*voiceSession, 0, len(r.byUser))
	for _, s := range r.byUser {
		sessions = append(sessions, s)
	}

	return sessions
}

// noteRefusal keeps the voice server's latest refusal of a phone session.
func (r *voiceSessions) noteRefusal(sentence string) {
	r.lock.Lock()
	defer r.lock.Unlock()

	r.refused, r.refusedAt = sentence, r.clock.Now()
}

// lastRefusal is the voice server's latest refusal of a phone session and
// when it was, or "" for none since the plugin started.
func (r *voiceSessions) lastRefusal() (string, time.Time) {
	r.lock.Lock()
	defer r.lock.Unlock()

	return r.refused, r.refusedAt
}

// noteRosterDial counts a connection the roster connection opens.
func (r *voiceSessions) noteRosterDial() {
	r.budget.note(r.clock.Now())
}

// personalWait is how long until a person's join may make another new
// connection to the voice server, or 0 when it may now: their share of the
// budget, voicePersonDials in any voicePersonWindow.
func (r *voiceSessions) personalWait(userID string, now time.Time) time.Duration {
	r.lock.Lock()
	defer r.lock.Unlock()

	recent := r.recentLocked(userID, now)
	if len(recent) < voicePersonDials {
		return 0
	}

	return recent[len(recent)-voicePersonDials].Add(voicePersonWindow).Sub(now)
}

// notePersonalDial counts a new connection a person's join made, and lets
// go of every other person's that are old enough not to count.
func (r *voiceSessions) notePersonalDial(userID string, now time.Time) {
	r.lock.Lock()
	defer r.lock.Unlock()

	for other := range r.personal {
		if other != userID {
			r.recentLocked(other, now)
		}
	}
	r.personal[userID] = append(r.recentLocked(userID, now), now)
}

// recentLocked is a person's new connections that still count, the older
// ones let go of.
func (r *voiceSessions) recentLocked(userID string, now time.Time) []time.Time {
	recent := slices.DeleteFunc(r.personal[userID], func(at time.Time) bool { return now.Sub(at) >= voicePersonWindow })
	if len(recent) == 0 {
		delete(r.personal, userID)
		return nil
	}
	r.personal[userID] = recent

	return recent
}

// forgetNames forgets which name the voice server takes, for when its
// settings change.
func (r *voiceSessions) forgetNames() {
	r.lock.Lock()
	defer r.lock.Unlock()

	r.fallbackFirst = false
}

// checkChannel refuses a channel the roster connection does not know, which
// is also one Channels to show leaves out, unless the session already sits
// in it (here): a Mumble admin may have moved it there, and joining again
// where it is moves nothing.
func (r *voiceSessions) checkChannel(channel uint32, here bool) error {
	known, connected := r.channelKnown(channel)
	switch {
	case !connected:
		return voiceUnavailableRefusal(voiceNotConnectedText, 0)
	case !known && !here:
		return &voiceRefusal{Status: http.StatusNotFound, Code: codeNotFound, Sentence: voiceNoChannelText}
	default:
		return nil
	}
}

// join runs a join: the person's session as it is, taken over, or a new
// connection to the voice server within the budget; the move; mute and
// deafen as asked, which gate the voice at once; then attach, which makes
// the phone's peer for the session, and only then is the voice server asked
// for the mute and deafen. A join that fails leaves nothing it made behind:
// a session it took over keeps the mute and deafen it had, and the voice
// server is asked for nothing new. On success the session's previous peer,
// if it had one, is told it was replaced and closed. A Matty below the
// minimum in force by then, by its own word or by what its phone proved,
// has its session ended and is refused.
func (r *voiceSessions) join(ctx context.Context, ask voiceJoinAsk, dial voiceTalkDial, attach func(s *voiceSession) (voicePeer, error)) (voiceJoined, error) {
	unlock := r.lockUser(ask.UserID)
	defer unlock()

	s := r.of(ask.UserID)
	if err := r.checkChannel(ask.Channel, s != nil && s.channel() == ask.Channel); err != nil {
		return voiceJoined{}, err
	}

	made := false
	if s == nil {
		var err error
		if s, err = r.open(ctx, ask, dial); err != nil {
			return voiceJoined{}, err
		}
		made = true
	}
	before := s.wanted()

	fail := func(err error) (voiceJoined, error) {
		// A kick or a ban that landed meanwhile is the voice server's word
		// on this person, not a connection lost that is worth joining again
		// after.
		if ending := s.mumbleEnding(); ending.Code == voiceEndKicked || ending.Code == voiceEndBanned {
			err = voiceDeniedRefusal(ending.Reason)
		}
		if made {
			r.finish(s, voiceEnding{}, nil)
		} else {
			_, _ = r.holdState(s, before.mute, before.deaf)
		}

		return voiceJoined{}, voiceRefusalOf(err)
	}

	if err := r.moveSession(ctx, s, ask.Channel); err != nil {
		return fail(err)
	}
	m, err := r.holdState(s, ask.Muted, ask.Deafened)
	if err != nil {
		return fail(err)
	}

	peer, err := attach(s)
	if err != nil {
		return fail(err)
	}
	if peer == nil {
		return fail(errors.New("no peer"))
	}

	if !r.attach(s, peer) {
		r.closePeer(peer, voiceEnding{})
		return fail(voiceLostRefusal())
	}
	if m.setSelf(ask.Muted, ask.Deafened, false) != nil {
		return fail(voiceLostRefusal())
	}

	// The session is this join's now, and is what it said it is.
	s.lock.Lock()
	if s.sessionID != ask.SessionID {
		s.unread = 0
	}
	s.sessionID, s.matty, s.version = ask.SessionID, ask.Matty, ask.Version
	s.lock.Unlock()

	// A minimum that came into force while this join made its connection
	// found no session of this person to end: the join ends it here, as
	// endOutdated would, and is refused as the gate refuses.
	minimum := r.minimum()
	if sentence := appOutdatedText(minimum, ask.Version); ask.Matty && sentence != "" {
		r.finish(s, voiceEnding{Code: voiceEndOff, Reason: sentence}, nil)

		return voiceJoined{}, &voiceRefusal{Status: http.StatusForbidden, Code: codeAppOutdated, Sentence: sentence}
	}
	if sentence := r.provenBelowOf(ask.Matty, ask.UserID, ask.SessionID, minimum); sentence != "" {
		r.finish(s, voiceEnding{Code: voiceEndOff, Reason: sentence}, nil)

		return voiceJoined{}, &voiceRefusal{Status: http.StatusForbidden, Code: codeAppUnverified, Sentence: sentence}
	}

	// A phone whose connection went while it was answered (its port closed
	// under it, say) is gone from the start: the session waits for it as
	// for any other.
	if peer.isGone() {
		r.peerGoneLocked(s, peer)
	}

	return voiceJoined{Session: s.number(), Channel: s.channel(), Name: s.name}, nil
}

// open makes a new session: its name, the person's share of the budget and
// the node's, and its connection, with the other name when the voice server
// refuses the first as invalid or as a registered user's. Once the voice
// server has refused "<username> (Matty)" as invalid and taken
// "<username>.matty", the latter is asked for first.
func (r *voiceSessions) open(ctx context.Context, ask voiceJoinAsk, dial voiceTalkDial) (*voiceSession, error) {
	if strings.TrimSpace(ask.Username) == "" {
		return nil, &voiceRefusal{Status: http.StatusBadRequest, Code: codeBadRequest, Sentence: voiceNoUsernameText}
	}
	if r.count() >= r.most {
		return nil, voiceUnavailableRefusal(voiceTalkFullText, 0)
	}
	if wait := r.personalWait(ask.UserID, r.clock.Now()); wait > 0 {
		return nil, &voiceRefusal{Status: http.StatusTooManyRequests, Code: codeRateLimited, Sentence: voicePersonBudgetText, RetryAfter: wait}
	}

	r.lock.Lock()
	era, fallbackFirst := r.era, r.fallbackFirst
	r.lock.Unlock()

	first, fallback := voicePhoneNames(ask.Username)
	names := []string{first, fallback}
	if fallbackFirst {
		names = []string{fallback, first}
	}

	counted, invalid := false, false
	for i, name := range names {
		if refusal := r.nameRefusal(name, dial, ask.UserID); refusal != nil {
			return nil, refusal
		}

		if wait, ok := r.budget.take(r.clock.Now(), dial.NoBudget); !ok {
			return nil, voiceUnavailableRefusal(voiceBudgetText, wait)
		}
		if !counted {
			r.notePersonalDial(ask.UserID, r.clock.Now())
			counted = true
		}

		s := r.newSession(ask, name)
		m, err := r.dial(ctx, voiceMumbleDial{Address: dial.Address, Password: dial.Password, Name: name}, s, r.safely)
		if err != nil {
			var rejected *voiceMumbleRejected
			if errors.As(err, &rejected) {
				nameRefused := rejected.Type == MumbleProto.Reject_InvalidUsername || rejected.Type == MumbleProto.Reject_WrongUserPW
				if nameRefused && i+1 < len(names) {
					invalid = rejected.Type == MumbleProto.Reject_InvalidUsername
					continue
				}

				refusal := voiceRejectedRefusal(rejected, name)
				r.noteRefusal(refusal.Sentence)

				return nil, refusal
			}
			if errors.Is(err, errVoiceMumbleNoSync) {
				return nil, voiceUnavailableRefusal(voiceNoAnswerText, 0)
			}

			return nil, voiceUnavailableRefusal(voiceUnreachableText, 0)
		}

		if !r.insert(s, m, era) {
			m.close()
			return nil, voiceLostRefusal()
		}

		// The voice server's rule for names, learnt: which of the two it
		// took after refusing the other as invalid.
		if invalid {
			r.lock.Lock()
			r.fallbackFirst = name == fallback
			r.lock.Unlock()
		}

		return s, nil
	}

	// Not reached: the last name's refusal is returned above.
	return nil, voiceUnavailableRefusal(voiceUnreachableText, 0)
}

// nameRefusal refuses a name a phone session must never take: the roster
// connection's own, a hidden one, or another phone session's, without
// regard to case. The voice server would kick whoever had it first.
func (r *voiceSessions) nameRefusal(name string, dial voiceTalkDial, userID string) *voiceRefusal {
	if dial.Own != "" && strings.EqualFold(strings.TrimSpace(name), dial.Own) {
		return voiceRefusedRefusal(voiceNameKeptText(name))
	}
	if dial.Hidden[strings.ToLower(strings.TrimSpace(name))] {
		return voiceRefusedRefusal(voiceNameKeptText(name))
	}

	r.lock.Lock()
	defer r.lock.Unlock()

	for other, s := range r.byUser {
		if other != userID && strings.EqualFold(s.name, name) {
			return voiceRefusedRefusal(voiceNameKeptText(name))
		}
	}

	return nil
}

// newSession is a session not yet connected, ready to be its connection's
// handler.
func (r *voiceSessions) newSession(ask voiceJoinAsk, name string) *voiceSession {
	s := &voiceSession{owner: r, userID: ask.UserID, username: ask.Username, sessionID: ask.SessionID, name: name, matty: ask.Matty, version: ask.Version}

	s.up = &voiceUplink{
		clock:  r.clock,
		safely: r.safely,
		send: func(sequence int64, final bool, payload []byte) {
			s.lock.Lock()
			m := s.mumble
			s.lock.Unlock()

			if m != nil {
				_ = m.writeAudio(sequence, final, payload)
			}
		},
		changed: s.talkingChanged,
		blocked: true,
	}
	s.down = &voiceDownlink{clock: r.clock, safely: r.safely, changed: s.talkingChanged}

	return s
}

// insert puts a newly connected session in the registry, and in the roster's
// registry of phone sessions so that it shows as the account that joined.
// It is false when the session ended while it connected, or talk went off.
func (r *voiceSessions) insert(s *voiceSession, m *voiceMumble, era uint64) bool {
	s.lock.Lock()
	if s.ended {
		s.lock.Unlock()
		return false
	}
	s.mumble = m
	self := m.current()
	s.serverMuted, s.suppressed = self.muted(), self.Suppressed
	s.heldMuted, s.heldDeafened = self.SelfMute, self.SelfDeaf
	s.lock.Unlock()

	r.lock.Lock()
	if r.era != era {
		r.lock.Unlock()
		return false
	}
	r.byUser[s.userID] = s
	r.lock.Unlock()

	release := r.putPhone(m.session, s.userID)

	s.lock.Lock()
	if s.ended {
		s.lock.Unlock()
		release()

		return false
	}
	s.release = release
	s.lock.Unlock()

	return true
}

// moveSession moves a session to a channel the roster connection knows, or
// leaves it where it is. The move stops waiting when ctx ends.
func (r *voiceSessions) moveSession(ctx context.Context, s *voiceSession, channel uint32) error {
	if err := r.checkChannel(channel, s.channel() == channel); err != nil {
		return err
	}

	s.lock.Lock()
	m, ended := s.mumble, s.ended
	s.lock.Unlock()

	if m == nil || ended {
		return voiceLostRefusal()
	}

	err := m.move(ctx, channel, r.moveWait)

	var refused *voiceRefusal
	if errors.As(err, &refused) && refused.Code == codeVoiceDenied {
		r.noteRefusal(refused.Sentence)
	}

	return err
}

// setState sets a session's mute and deafen (holdState), then asks the voice
// server for them. When the new state stops the voice going up, the spell of
// talking is ended (its last frame sent) before the voice server is asked to
// self mute the session: it passes nothing on from somebody self muted, the
// last frame included. Letting the voice up again waits for the voice
// server's word that the session is no longer self muted (mumbleSelf). The
// session's connection sends what is asked when it differs from that word,
// in its turn (voiceMumble.reconcile), and nothing here waits for it. A
// person asking again for what they already asked (again) has it sent again
// as soon as the session's pace allows.
func (r *voiceSessions) setState(s *voiceSession, muted, deafened, again bool) error {
	m, err := r.holdState(s, muted, deafened)
	if err != nil {
		return err
	}
	if m.setSelf(muted, deafened, again) != nil {
		return voiceLostRefusal()
	}

	return nil
}

// holdState sets a session's mute and deafen as the person asked, deafened
// being muted too, and stops at once what they stop, without asking the
// voice server for anything. It answers the session's connection.
func (r *voiceSessions) holdState(s *voiceSession, muted, deafened bool) (*voiceMumble, error) {
	muted = muted || deafened

	s.lock.Lock()
	m, ended := s.mumble, s.ended
	if m == nil || ended {
		s.lock.Unlock()
		return nil, voiceLostRefusal()
	}
	s.muted, s.deafened = muted, deafened
	block := s.blockLocked()
	s.lock.Unlock()

	s.up.setBlockedAs(block)
	s.down.setDeaf(deafened)
	s.talkingChanged()

	return m, nil
}

// wanted is the session's mute and deafen as the person asked.
func (s *voiceSession) wanted() voiceSelfPair {
	s.lock.Lock()
	defer s.lock.Unlock()

	return voiceSelfPair{mute: s.muted, deaf: s.deafened}
}

// attach gives a session its new peer, in place of any it had (which is told
// it was replaced, and closed), and ends any wait. A session that had been
// waiting was self deafened on the voice server for it; the join has already
// asked for the mute and deafen it wants, and the session's connection sends
// them once, when the voice server's word differs. False when the session
// has ended.
func (r *voiceSessions) attach(s *voiceSession, peer voicePeer) bool {
	s.lock.Lock()
	if s.ended {
		s.lock.Unlock()
		return false
	}

	old := s.peer
	s.peer = peer
	s.waitAt++
	if s.wait != nil {
		s.wait.Stop()
		s.wait = nil
	}
	block := s.blockLocked()
	s.lock.Unlock()

	s.talkLock.Lock()
	s.talkSent, s.talkLastSet = false, nil
	s.talkLock.Unlock()

	s.up.restart()
	s.up.setBlockedAs(block)
	s.down.attach(peer)

	if old != nil && old != peer {
		r.closePeer(old, voiceEndingReplaced)
	}

	return true
}

// peerGone starts a session's wait for its phone: nothing goes up or down,
// the session asks to be self deafened on the voice server (sent by its
// connection as every state is), and it ends unless a join takes it over
// within the wait. It takes the person's turn, so that it never runs in the
// middle of a join: one that takes the session over first leaves this
// nothing to do, as the session's peer is no longer this one.
func (r *voiceSessions) peerGone(s *voiceSession, peer voicePeer) {
	unlock := r.lockUser(s.userID)
	defer unlock()

	r.peerGoneLocked(s, peer)
}

// peerGoneLocked is peerGone with the person's turn already taken.
func (r *voiceSessions) peerGoneLocked(s *voiceSession, peer voicePeer) {
	s.lock.Lock()
	if s.ended || s.peer != peer || peer == nil {
		s.lock.Unlock()
		return
	}

	s.peer = nil
	s.waitAt++
	at := s.waitAt
	if s.wait != nil {
		s.wait.Stop()
	}
	s.wait = r.clock.AfterFunc(r.wait, func() {
		r.safely("the end of a voice session whose phone went", func() {
			r.finish(s, voiceEnding{}, func(s *voiceSession) bool { return s.waitAt == at && s.peer == nil })
		})
	})
	m, muted := s.mumble, s.muted
	block := s.blockLocked()
	s.lock.Unlock()

	s.up.setBlockedAs(block)
	s.down.attach(nil)
	s.talkingChanged()

	if m != nil {
		_ = m.setSelf(muted, true, false)
	}

	r.closePeer(peer, voiceEnding{})
}

// finish ends a session once: out of both registries, its connection
// closed, and its peer, if it has one, told why (when the ending has a code)
// and closed. guard, when given, is checked under the session's lock first.
func (r *voiceSessions) finish(s *voiceSession, ending voiceEnding, guard func(s *voiceSession) bool) bool {
	s.lock.Lock()
	if s.ended || (guard != nil && !guard(s)) {
		s.lock.Unlock()
		return false
	}

	s.ended = true
	peer, m, release := s.peer, s.mumble, s.release
	s.peer = nil
	s.waitAt++
	if s.wait != nil {
		s.wait.Stop()
		s.wait = nil
	}
	block := s.blockLocked()
	s.lock.Unlock()

	r.lock.Lock()
	if r.byUser[s.userID] == s {
		delete(r.byUser, s.userID)
	}
	r.lock.Unlock()

	s.up.setBlockedAs(block)
	s.down.attach(nil)

	if m != nil {
		m.close()
	}
	if release != nil {
		release()
	}
	if peer != nil {
		r.closePeer(peer, ending)
	}

	return true
}

// closePeer tells a peer why its session is over, when there is something to
// tell, and closes it, on a goroutine of its own.
func (r *voiceSessions) closePeer(peer voicePeer, ending voiceEnding) {
	go r.safely("closing a phone's voice connection", func() {
		if ending.Code != "" {
			peer.signal(voiceEndSignal{End: ending.Code, Reason: ending.Reason})
		}
		peer.close()
	})
}

// move moves the person's session to a channel, holding the person's turn
// until the voice server answers, the move's wait runs out (its turn and
// moveWait, about four seconds at most) or the move gives up: a move whose
// context ends gives up its turn, and so does one whose session ends, as
// when the plugin stops. Under Mattermost a request's context never ends, so
// the person's next request, a leave among them, waits behind a move.
func (r *voiceSessions) move(ctx context.Context, userID string, channel uint32) (uint32, error) {
	unlock := r.lockUser(userID)
	defer unlock()

	s := r.of(userID)
	if s == nil {
		return 0, &voiceRefusal{Status: http.StatusConflict, Code: codeConflict, Sentence: voiceNotInVoiceText}
	}

	if err := r.moveSession(ctx, s, channel); err != nil {
		return 0, voiceRefusalOf(err)
	}

	return s.channel(), nil
}

// state sets the person's mute and deafen, and answers them as set.
func (r *voiceSessions) state(userID string, muted, deafened bool) (bool, bool, error) {
	unlock := r.lockUser(userID)
	defer unlock()

	s := r.of(userID)
	if s == nil {
		return false, false, &voiceRefusal{Status: http.StatusConflict, Code: codeConflict, Sentence: voiceNotInVoiceText}
	}

	if err := r.setState(s, muted, deafened, true); err != nil {
		return false, false, voiceRefusalOf(err)
	}

	return muted || deafened, deafened, nil
}

// leave ends the person's session, if they have one, telling the phone
// nothing: it asked.
func (r *voiceSessions) leave(userID string) {
	unlock := r.lockUser(userID)
	defer unlock()

	if s := r.of(userID); s != nil {
		r.finish(s, voiceEnding{}, nil)
	}
}

// endFor ends the person's session with an ending, if they have one.
func (r *voiceSessions) endFor(userID string, ending voiceEnding) {
	if s := r.of(userID); s != nil {
		r.finish(s, ending, nil)
	}
}

// endOutdated ends the session of every Matty that does not meet minimum,
// as its last join said it (an older version, or none that can be read),
// telling its phone the sentence a request of that Matty is refused with;
// and, with the app check at "require", the session of a Matty whose
// Mattermost session holds a pass kept with an older version, with the
// gate's sentence for it (provenBelow). A session joined from a client that
// does not say it is Matty stays, and no minimum ends nobody. One person at
// a time, so that a join taking a session over has said what it is first.
func (r *voiceSessions) endOutdated(minimum string) {
	if minimum == "" {
		return
	}

	for _, s := range r.all() {
		unlock := r.lockUser(s.userID)
		s.lock.Lock()
		sessionID, matty, version := s.sessionID, s.matty, s.version
		s.lock.Unlock()

		sentence := appOutdatedText(minimum, version)
		if sentence == "" {
			sentence = r.provenBelowOf(matty, s.userID, sessionID, minimum)
		}
		if matty && sentence != "" {
			r.finish(s, voiceEnding{Code: voiceEndOff, Reason: sentence}, nil)
		}
		unlock()
	}
}

// provenBelowOf is provenBelow for a session that said it is Matty, and ""
// for one that did not.
func (r *voiceSessions) provenBelowOf(matty bool, userID, sessionID, minimum string) string {
	if !matty || minimum == "" {
		return ""
	}

	return r.provenBelow(userID, sessionID, minimum)
}

// endAll ends every session with an ending: talk switched off, or the voice
// server's settings changed. A join still connecting ends what it made.
func (r *voiceSessions) endAll(ending voiceEnding) {
	r.lock.Lock()
	r.era++
	r.lock.Unlock()

	for _, s := range r.all() {
		r.finish(s, ending, nil)
	}
}

// sweep looks at every session once: one whose person no longer has access
// ends, and so does one whose Mattermost session is over. A session whose
// Mattermost session cannot be read ends only after two sweeps in a row, and
// only when the store answered in the same sweep, so that the database being
// away for a moment ends nobody's voice. Mattermost answers a signed-out
// session with an error that does not say the session is gone, so the
// store's answer is the access read made for every session, and a person
// alone in voice is ended as surely as one among others.
func (r *voiceSessions) sweep() {
	sessions := r.all()

	var unread []*voiceSession
	storeAnswered := false

	for _, s := range sessions {
		gone, answered := r.accessGone(s.userID)
		if answered {
			storeAnswered = true
		}
		if gone {
			r.finish(s, voiceEndingAccess, nil)
			continue
		}
		s.lock.Lock()
		sessionID := s.sessionID
		s.lock.Unlock()
		if sessionID == "" {
			continue
		}

		switch r.readSession(s.userID, sessionID) {
		case voiceSessionOver:
			// Unless a join from another session took it over meanwhile.
			r.finish(s, voiceEndingSession, func(s *voiceSession) bool { return s.sessionID == sessionID })
		case voiceSessionStands:
			storeAnswered = true
			s.lock.Lock()
			s.unread = 0
			s.lock.Unlock()
		case voiceSessionUnread:
			s.lock.Lock()
			s.unread++
			s.lock.Unlock()
			unread = append(unread, s)
		}
	}

	if !storeAnswered {
		return
	}
	for _, s := range unread {
		r.finish(s, voiceEndingSession, func(s *voiceSession) bool { return s.unread >= 2 })
	}
}

// run sweeps once a minute until ctx ends.
func (r *voiceSessions) run(ctx context.Context) {
	ticker := time.NewTicker(voiceTalkSweepEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.safely("looking over the voice sessions", r.sweep)
		}
	}
}

// ---------------------------------------------------------------- the plugin's

// voiceTalk is this node's registry of voice sessions, made on first use.
func (p *Plugin) voiceTalk() *voiceSessions {
	p.voiceTalkOnce.Do(func() {
		r := newVoiceSessions(realVoiceClock{}, p.safely)
		r.channelKnown = p.voiceChannelKnown
		r.putPhone = p.putVoicePhone
		r.accessGone = p.voiceAccessGone
		r.readSession = p.voiceReadSession
		r.minimum = p.appMinimumInForce
		r.provenBelow = p.voiceProvenBelow
		p.voiceTalkState = r
	})

	return p.voiceTalkState
}

// voiceChannelKnown is whether the roster connection knows a channel, and
// whether it is connected.
func (p *Plugin) voiceChannelKnown(channel uint32) (bool, bool) {
	state := p.getVoice()
	if !state.connected {
		return false, false
	}

	for _, known := range state.channels {
		if known.ID == channel {
			return true, true
		}
	}

	return false, true
}

// voiceAccessGone is whether somebody is neither linked nor let in any more,
// and whether the store answered: accessOf always reads it, so a read that
// worked says the store is there. A store that does not answer takes
// nobody's voice away.
func (p *Plugin) voiceAccessGone(userID string) (gone, answered bool) {
	level, _, err := p.accessOf(userID)

	return err == nil && level != accessLinked && level != accessLetIn, err == nil
}

// voiceReadSession reads the Mattermost session a join came from: over when
// it has expired or is somebody else's, or when Mattermost says there is no
// such session; unread when it could not be read for another reason.
func (p *Plugin) voiceReadSession(userID, sessionID string) voiceSessionRead {
	session, appErr := p.API.GetSession(sessionID)
	switch {
	case appErr != nil && appErr.StatusCode == http.StatusNotFound:
		return voiceSessionOver
	case appErr != nil || session == nil:
		return voiceSessionUnread
	case session.IsExpired() || session.UserId != userID:
		return voiceSessionOver
	default:
		return voiceSessionStands
	}
}
