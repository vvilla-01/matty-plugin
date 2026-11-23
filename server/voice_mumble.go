package main

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/golang/protobuf/proto"
	"layeh.com/gumble/gumble"
	"layeh.com/gumble/gumble/MumbleProto"
	"layeh.com/gumble/gumble/varint"
)

// A phone's own Mumble connection, for somebody talking from Matty.
//
// Each person who joins a voice channel from Matty gets a connection of their
// own to the voice server, made by this node and named after their
// Mattermost account, so that the voice server and everybody on it see them
// as a person of their own: in a channel, muted or not, talking or not.
//
// It is NOT a gumble.Client, as the roster connection is (voice.go). gumble's
// client decodes what it hears, registers its codecs for every client in the
// process at once, and hands audio over on a channel per person that stalls
// the whole connection when it is read slowly; and it never gives the Opus
// packet as it came, with its sequence number and last-frame flag, which is
// what is passed on to the phone. So this speaks Mumble's control protocol
// directly over gumble's framing (gumble.Conn), and never decodes anything:
// Opus goes from the phone to the voice server and back as it is.
//
// Voice is sent the way Mumble 1.3 clients send it, inside the TLS
// connection (a "UDP tunnel"), so one TCP connection carries everything and
// no UDP port is needed towards the voice server.

const (
	// How long a dial may take, as for the roster connection.
	voiceMumbleDialTimeout = 10 * time.Second

	// How long the voice server may take to let a new connection in.
	voiceMumbleSyncTimeout = 10 * time.Second

	// How often the connection pings, which is also what keeps the voice
	// server from dropping it (Murmur drops a client silent for 30 s).
	voiceMumblePingEvery = 15 * time.Second

	// Every write has this long to go out, and one that cannot ends the
	// session: a voice server that stops reading must not hold up the phone.
	voiceMumbleWriteTimeout = 5 * time.Second

	// A read waits this long, which two pings and their answers fit in.
	voiceMumbleReadTimeout = 40 * time.Second

	// How long a move waits for the voice server to confirm it or refuse it,
	// from when it goes out.
	voiceMoveWait = 3 * time.Second

	// The voice server takes a user's own state messages (moves, self mute
	// and self deafen) only so fast: Murmur's messageburst (5) at once, then
	// messagelimit (1) a second, and it drops the rest without a word. So a
	// session's own messages go at least this far apart...
	voiceStateGap = time.Second

	// ...and a self mute or deafen that the voice server's word has not
	// followed within this long is sent again: only its word proves that a
	// message arrived.
	voiceStateResend = 2 * time.Second

	// Mumble's version announced, 1.3.0: the voice server then speaks to this
	// connection in the legacy audio format, the one framed here.
	voiceMumbleVersion = 1<<16 | 3<<8

	// Mumble's number for Opus in the legacy audio format, and the frame
	// type of a voice packet inside the TLS connection.
	voiceOpusFormat     = 4
	voiceMumbleUDPFrame = 1

	// The size field's last-frame flag, and what is left of it for the size.
	voiceLastFrameFlag = 0x2000
	voiceSizeMask      = 0x1FFF
)

// The control messages read here, by Mumble's numbers.
const (
	voiceMsgReject           = 4
	voiceMsgServerSync       = 5
	voiceMsgUserRemove       = 8
	voiceMsgUserState        = 9
	voiceMsgPermissionDenied = 12
)

// Names on the voice server.
const (
	// "<username> (Matty)": who it is, and that they are here from Matty.
	voicePhoneNameSuffix = " (Matty)"

	// "<username>.matty", for a voice server whose rule for names does not
	// take spaces or brackets.
	voicePhoneFallbackSuffix = ".matty"
)

// voicePhoneNames is the name a phone session asks for, and the one it asks
// for when the voice server refuses that one as invalid.
func voicePhoneNames(username string) (string, string) {
	username = strings.TrimSpace(username)

	return username + voicePhoneNameSuffix, username + voicePhoneFallbackSuffix
}

// voiceMumbleDial is where and as whom a phone session connects.
type voiceMumbleDial struct {
	// The voice server, host and port.
	Address string

	// The voice server's own password, the same one the roster connection
	// gives (blank for none).
	Password string

	// The name to connect under.
	Name string
}

// voiceMumbleSelf is what the voice server says about a phone session.
type voiceMumbleSelf struct {
	// The channel it is in.
	Channel uint32

	// Muted and deafened by the voice server (an admin), as opposed to by
	// the person, each kept as the voice server keeps it: deafening mutes
	// too, unmuting undeafens too, and undeafening alone leaves the mute.
	Mute bool
	Deaf bool

	// Not allowed to speak in its channel.
	Suppressed bool

	// The session's own mute and deafen as the voice server holds them,
	// which is what it goes by, whatever was asked of it. Coupled as the
	// voice server couples them: a self deafen self mutes too, and a self
	// unmute self undeafens too.
	SelfMute bool
	SelfDeaf bool
}

// muted is whether the voice server lets nothing of the session be heard:
// muted or deafened by it.
func (s voiceMumbleSelf) muted() bool {
	return s.Mute || s.Deaf
}

// voiceSelfPair is a session's own mute and deafen, deafened being muted
// too.
type voiceSelfPair struct {
	mute, deaf bool
}

// held is the session's own mute and deafen as the voice server holds them.
func (s voiceMumbleSelf) held() voiceSelfPair {
	return voiceSelfPair{mute: s.SelfMute, deaf: s.SelfDeaf}
}

// voiceMumbleHandler is what a phone session's connection tells as it reads.
// Every call is made inside the plugin's safely, on the connection's read
// goroutine (mumbleEnded may also come from a writer or the pinger), and the
// payload of mumbleAudio is a copy the handler may keep.
type voiceMumbleHandler interface {
	// A voice packet from somebody else: their session, the sequence
	// number they sent it with, the Opus payload and the last-frame flag.
	mumbleAudio(sender uint32, sequence int64, payload []byte, final bool)

	// Its channel, server mute, suppression, or self mute or deafen as the
	// voice server holds them changed.
	mumbleSelf(self voiceMumbleSelf)

	// Somebody else left the voice server.
	mumbleGone(session uint32)

	// The connection ended without being closed from here: kicked, banned
	// or lost.
	mumbleEnded(ending voiceEnding)
}

// voiceMumbleRejected is the voice server refusing a connection: Mumble's
// reason type and its own words.
type voiceMumbleRejected struct {
	Type   MumbleProto.Reject_RejectType
	Reason string
}

func (r *voiceMumbleRejected) Error() string {
	if r.Reason == "" {
		return "the voice server refused the connection: " + r.Type.String()
	}

	return "the voice server refused the connection: " + r.Type.String() + ": " + r.Reason
}

// errVoiceMumbleClosed is a write to a connection that has ended.
var errVoiceMumbleClosed = errors.New("the voice connection has ended")

// voiceMumble is one phone session's connection to the voice server.
type voiceMumble struct {
	conn    *gumble.Conn
	raw     net.Conn
	handler voiceMumbleHandler
	safely  func(where string, f func()) bool

	// The session's number on the voice server and the name it connected
	// under. Set before dialVoiceMumble returns and never changed.
	session uint32
	name    string

	// One move at a time.
	moveLock sync.Mutex

	lock   sync.Mutex
	self   voiceMumbleSelf
	moving *voiceMovePending

	// The session's own mute and deafen as they should be on the voice
	// server, once a join or a change has said (wantedSet); the last state
	// message sent, when, and whether the voice server's word has yet to
	// follow it (sentOpen); and when the last of the session's own messages,
	// a move or its state, went out. reconcile sends from these, and nothing
	// else sends a move or a state.
	wanted    voiceSelfPair
	wantedSet bool
	sent      voiceSelfPair
	sentAt    time.Time
	sentOpen  bool
	lastAt    time.Time

	// Told when each of the session's own messages has gone out, and whether
	// it was a move, when a test sets it. Read under the lock, called
	// outside it.
	sends func(at time.Time, move bool)

	// Wakes reconcile: something it sends from changed.
	wake chan struct{}

	// How the connection ended: kicked, banned or lost, or no code for one
	// closed from here. Set before ended closes.
	ending voiceEnding

	endOnce sync.Once
	ended   chan struct{}
}

// voiceMovePending is a move asked for and not yet answered: out is closed
// when it goes out (sent), which waits for its turn.
type voiceMovePending struct {
	channel uint32
	answer  chan error
	out     chan struct{}
	sent    bool
}

// voiceWriteDeadline is the connection under gumble.Conn, which gives every
// write its own deadline: gumble's writes have none.
type voiceWriteDeadline struct {
	net.Conn
	timeout time.Duration
}

func (c voiceWriteDeadline) Write(b []byte) (int, error) {
	if err := c.Conn.SetWriteDeadline(time.Now().Add(c.timeout)); err != nil {
		return 0, err
	}

	return c.Conn.Write(b)
}

// dialVoiceMumble connects a phone session and waits until the voice server
// has let it in, at most voiceMumbleSyncTimeout. A refusal is a
// *voiceMumbleRejected; anything else is the voice server not reached or not
// answering. The handler is called from the moment the connection is up, and
// never after close.
func dialVoiceMumble(ctx context.Context, dial voiceMumbleDial, handler voiceMumbleHandler, safely func(string, func()) bool) (*voiceMumble, error) {
	// The voice server's certificate is not checked, for the reason the
	// roster connection gives (voice.go): Mumble servers sign their own.
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: voiceMumbleDialTimeout},
		Config:    &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // see voice.go
	}

	raw, err := dialer.DialContext(ctx, "tcp", dial.Address)
	if err != nil {
		return nil, err
	}

	m := &voiceMumble{
		raw:     raw,
		handler: handler,
		safely:  safely,
		name:    dial.Name,
		wake:    make(chan struct{}, 1),
		ended:   make(chan struct{}),
	}
	m.conn = gumble.NewConn(voiceWriteDeadline{Conn: raw, timeout: voiceMumbleWriteTimeout})
	m.conn.Timeout = voiceMumbleReadTimeout

	name, password := dial.Name, dial.Password
	if err := m.conn.WriteProto(&MumbleProto.Version{
		Version:   proto.Uint32(voiceMumbleVersion),
		Release:   proto.String("Matty"),
		Os:        proto.String(runtime.GOOS),
		OsVersion: proto.String(runtime.GOARCH),
	}); err != nil {
		_ = raw.Close()
		return nil, err
	}
	if err := m.conn.WriteProto(&MumbleProto.Authenticate{Username: &name, Password: &password, Opus: proto.Bool(true)}); err != nil {
		_ = raw.Close()
		return nil, err
	}

	synced := make(chan error, 1)
	go func() {
		if safely("a voice session's connection", func() { m.read(synced) }) {
			m.finish(voiceEnding{Code: voiceEndMumble, Reason: voiceLostText}, true)
		}
	}()

	timer := time.NewTimer(voiceMumbleSyncTimeout)
	defer timer.Stop()

	select {
	case err := <-synced:
		if err != nil {
			m.close()
			return nil, err
		}
	case <-timer.C:
		m.close()
		return nil, errVoiceMumbleNoSync
	case <-ctx.Done():
		m.close()
		return nil, ctx.Err()
	}

	go safely("a voice session's pings", m.ping)
	go func() {
		if safely("a voice session's own state", m.reconcile) {
			m.finish(voiceEnding{Code: voiceEndMumble, Reason: voiceLostText}, true)
		}
	}()

	return m, nil
}

// errVoiceMumbleNoSync is a voice server that took the connection and never
// let it in.
var errVoiceMumbleNoSync = errors.New("the voice server did not let the connection in in time")

// read is the connection's read goroutine. Until the voice server has let
// the session in it only collects what it says about every session, since
// its own number is not known yet; after that it reads voice, its own state
// and refusals.
func (m *voiceMumble) read(synced chan<- error) {
	early := map[uint32]voiceMumbleSelf{}
	in := false

	for {
		kind, data, err := m.conn.ReadPacket()
		if err != nil {
			if !in {
				synced <- err
				return
			}
			m.finish(voiceEnding{Code: voiceEndMumble, Reason: voiceLostText}, true)

			return
		}

		switch kind {
		case voiceMsgReject:
			var reject MumbleProto.Reject
			if proto.Unmarshal(data, &reject) != nil {
				reject.Reset()
			}
			if !in {
				synced <- &voiceMumbleRejected{Type: reject.GetType(), Reason: reject.GetReason()}
				return
			}

		case voiceMsgServerSync:
			var sync MumbleProto.ServerSync
			if proto.Unmarshal(data, &sync) != nil || in {
				continue
			}
			m.lock.Lock()
			m.session = sync.GetSession()
			m.self = early[m.session]
			m.lock.Unlock()
			in = true
			synced <- nil

		case voiceMsgUserState:
			var state MumbleProto.UserState
			if proto.Unmarshal(data, &state) != nil {
				continue
			}
			if !in {
				early[state.GetSession()] = mergeVoiceSelf(early[state.GetSession()], &state)
				continue
			}
			m.userState(&state)

		case voiceMsgUserRemove:
			var remove MumbleProto.UserRemove
			if proto.Unmarshal(data, &remove) != nil || !in {
				continue
			}
			if remove.GetSession() == m.session {
				m.finish(voiceRemovedEnding(remove.GetBan(), remove.GetReason()), true)
				return
			}
			gone := remove.GetSession()
			m.safely("a voice session's departure", func() { m.handler.mumbleGone(gone) })

		case voiceMsgPermissionDenied:
			var denied MumbleProto.PermissionDenied
			if proto.Unmarshal(data, &denied) != nil || !in {
				continue
			}
			m.denied(&denied)

		case voiceMumbleUDPFrame:
			if in {
				m.audio(data)
			}
		}
	}
}

// mergeVoiceSelf is a session's state with what one UserState says about it,
// mute and deafen coupled in the order the voice server applies them: a
// deafen mutes, then an unmute undeafens. An undeafen alone leaves the mute,
// as on the voice server, which says nothing of the mute then. The admin's
// mute and deafen and the session's own are coupled each on their own.
func mergeVoiceSelf(self voiceMumbleSelf, state *MumbleProto.UserState) voiceMumbleSelf {
	if state.SelfDeaf != nil {
		self.SelfDeaf = state.GetSelfDeaf()
		if self.SelfDeaf {
			self.SelfMute = true
		}
	}
	if state.SelfMute != nil {
		self.SelfMute = state.GetSelfMute()
		if !self.SelfMute {
			self.SelfDeaf = false
		}
	}
	if state.ChannelId != nil {
		self.Channel = state.GetChannelId()
	}
	if state.Deaf != nil {
		self.Deaf = state.GetDeaf()
		if self.Deaf {
			self.Mute = true
		}
	}
	if state.Mute != nil {
		self.Mute = state.GetMute()
		if !self.Mute {
			self.Deaf = false
		}
	}
	if state.Suppress != nil {
		self.Suppressed = state.GetSuppress()
	}

	return self
}

// userState is a UserState after the session is in: its own, which may
// answer a move or a state sent, or somebody else's, which is not this
// connection's business (the roster connection reads everybody).
func (m *voiceMumble) userState(state *MumbleProto.UserState) {
	m.lock.Lock()
	if state.GetSession() != m.session {
		m.lock.Unlock()
		return
	}

	before := m.self
	m.self = mergeVoiceSelf(m.self, state)
	after := m.self

	if m.moving != nil && after.Channel == m.moving.channel {
		m.moving.answer <- nil
		m.moving = nil
	}
	if m.sentOpen && after.held() == m.wanted {
		m.sentOpen = false
	}
	m.lock.Unlock()

	// The voice server's word on the session's own state changed: whether
	// it still differs from what is wanted is reconcile's to look at.
	if after.held() != before.held() {
		m.kick()
	}

	if after != before {
		m.safely("a voice session's state", func() { m.handler.mumbleSelf(after) })
	}
}

// denied is a PermissionDenied: while a move waits, its refusal. Any other
// refusal (a text message, say) is not this connection's to act on.
func (m *voiceMumble) denied(denied *MumbleProto.PermissionDenied) {
	m.lock.Lock()
	defer m.lock.Unlock()

	if m.moving == nil || !m.moving.sent {
		return
	}
	if denied.ChannelId != nil && denied.GetChannelId() != m.moving.channel {
		return
	}

	m.moving.answer <- voiceMoveRefusal(denied.GetType(), denied.GetReason())
	m.moving = nil
}

// audio reads one voice packet: Mumble's legacy format, which is a header
// byte (format and target), then from the voice server the sender's session,
// the sequence number, the size with its last-frame flag, and the payload.
// Anything that is not Opus, or does not read, is dropped.
func (m *voiceMumble) audio(data []byte) {
	if len(data) < 1 || data[0]>>5 != voiceOpusFormat {
		return
	}

	rest := data[1:]
	sender, n := varint.Decode(rest)
	if n <= 0 {
		return
	}
	rest = rest[n:]

	sequence, n := varint.Decode(rest)
	if n <= 0 {
		return
	}
	rest = rest[n:]

	size, n := varint.Decode(rest)
	if n <= 0 || size < 0 {
		return
	}
	rest = rest[n:]

	length := int(size & voiceSizeMask)
	if length > len(rest) {
		return
	}

	// A copy: gumble.Conn reads every packet into the same buffer.
	payload := append([]byte(nil), rest[:length]...)
	final := size&voiceLastFrameFlag != 0
	from := uint32(sender)

	m.safely("a voice packet from the voice server", func() { m.handler.mumbleAudio(from, sequence, payload, final) })
}

// ping keeps the connection alive until it ends.
func (m *voiceMumble) ping() {
	ticker := time.NewTicker(voiceMumblePingEvery)
	defer ticker.Stop()

	for {
		select {
		case <-m.ended:
			return
		case <-ticker.C:
			_ = m.write(&MumbleProto.Ping{Timestamp: proto.Uint64(uint64(time.Now().Unix()))})
		}
	}
}

// write sends one control message. One that cannot go out ends the session.
func (m *voiceMumble) write(message proto.Message) error {
	select {
	case <-m.ended:
		return errVoiceMumbleClosed
	default:
	}

	if err := m.conn.WriteProto(message); err != nil {
		m.finish(voiceEnding{Code: voiceEndMumble, Reason: voiceLostText}, true)
		return err
	}

	return nil
}

// writeAudio sends one voice packet to the session's channel (target 0). A
// zero-length payload with final set ends a spell of talking.
func (m *voiceMumble) writeAudio(sequence int64, final bool, payload []byte) error {
	select {
	case <-m.ended:
		return errVoiceMumbleClosed
	default:
	}

	if err := m.conn.WriteAudio(voiceOpusFormat, 0, sequence, final, payload, nil, nil, nil); err != nil {
		m.finish(voiceEnding{Code: voiceEndMumble, Reason: voiceLostText}, true)
		return err
	}

	return nil
}

// current is what the voice server last said about the session.
func (m *voiceMumble) current() voiceMumbleSelf {
	m.lock.Lock()
	defer m.lock.Unlock()

	return m.self
}

// move takes the session to a channel and waits, at most wait from when the
// move goes out, for the voice server to confirm it. A session already there
// is not moved: the voice server answers nothing to a move to the channel a
// session is in. No answer is taken as a refusal, and a refused move is not
// tried again. The move goes in a UserState of its own, sent by reconcile in
// its turn, before any state. When ctx ends the move stops waiting, and one
// that has not gone out yet never does; so does it when the session ends,
// as when the plugin stops. Under Mattermost the context of a route's
// request never ends, so a move a phone asked for waits its turn and the
// answer.
func (m *voiceMumble) move(ctx context.Context, channel uint32, wait time.Duration) error {
	m.moveLock.Lock()
	defer m.moveLock.Unlock()

	m.lock.Lock()
	if m.self.Channel == channel {
		m.lock.Unlock()
		return nil
	}
	pending := &voiceMovePending{channel: channel, answer: make(chan error, 1), out: make(chan struct{})}
	m.moving = pending
	m.lock.Unlock()
	m.kick()

	forget := func() {
		m.lock.Lock()
		if m.moving == pending {
			m.moving = nil
		}
		m.lock.Unlock()
	}

	// Its turn: at most voiceStateGap after the session's last message. An
	// admin who moves the session there meanwhile answers it.
	select {
	case <-pending.out:
	case err := <-pending.answer:
		return err
	case <-m.ended:
		forget()
		return voiceLostRefusal()
	case <-ctx.Done():
		forget()
		return ctx.Err()
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case err := <-pending.answer:
		return err
	case <-m.ended:
		forget()
		return voiceLostRefusal()
	case <-ctx.Done():
		forget()
		return ctx.Err()
	case <-timer.C:
		forget()

		// An answer that came in as the wait ran out still counts.
		select {
		case err := <-pending.answer:
			return err
		default:
		}

		return voiceDeniedRefusal(voiceDeniedText)
	}
}

// setSelf asks for the session's own mute and deafen on the voice server,
// deafened being muted too, as in Mumble. It does not wait: reconcile sends
// it while the voice server's word differs from it, and again until that
// word follows. A person who asks again (again) for what is already wanted
// while the voice server's word still differs has it sent again as soon as
// the session's pace allows, without the wait for that word. It fails only
// on a connection that has ended.
func (m *voiceMumble) setSelf(muted, deafened, again bool) error {
	select {
	case <-m.ended:
		return errVoiceMumbleClosed
	default:
	}

	wanted := voiceSelfPair{mute: muted || deafened, deaf: deafened}

	m.lock.Lock()
	if again && m.wantedSet && m.wanted == wanted && wanted != m.self.held() {
		m.sentOpen = false
	}
	m.wanted, m.wantedSet = wanted, true
	m.lock.Unlock()
	m.kick()

	return nil
}

// kick wakes reconcile.
func (m *voiceMumble) kick() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// reconcile is the one sender of the session's own messages, moves and its
// self mute and deafen, until the connection ends: whatever nextLocked says
// is due, when it says.
func (m *voiceMumble) reconcile() {
	var timer *time.Timer
	var due <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	for {
		select {
		case <-m.ended:
			return
		case <-m.wake:
		case <-due:
		}

		for {
			message, wait := m.next(time.Now())
			if message == nil {
				if timer != nil {
					timer.Stop()
					timer, due = nil, nil
				}
				if wait > 0 {
					timer = time.NewTimer(wait)
					due = timer.C
				}

				break
			}

			// One that cannot go out has ended the connection.
			if m.write(message) != nil {
				return
			}
			m.wrote(message)
		}
	}
}

// wrote notes that one of the session's own messages has gone out: the next
// one waits voiceStateGap from here, and the voice server's word on a state
// is waited for from here, so that the pace holds between the times the
// messages actually went out, however long a write took.
func (m *voiceMumble) wrote(message proto.Message) {
	move := false
	if state, ok := message.(*MumbleProto.UserState); ok {
		move = state.ChannelId != nil
	}
	now := time.Now()

	m.lock.Lock()
	m.lastAt = now
	if !move {
		m.sentAt = now
	}
	sends := m.sends
	m.lock.Unlock()

	if sends != nil {
		sends(now, move)
	}
}

// next is nextLocked under the lock.
func (m *voiceMumble) next(now time.Time) (proto.Message, time.Duration) {
	m.lock.Lock()
	defer m.lock.Unlock()

	return m.nextLocked(now)
}

// nextLocked is the message the session sends now, noted as sent, or nil
// and how long until one may be due (0: nothing is). A move asked for goes
// first. The state goes only while the voice server's word differs from
// what is wanted, so that nothing is ever sent that would change nothing:
// the newest wanted at once, and the same one again only once the voice
// server's word has not followed it for voiceStateResend. Neither goes
// sooner than voiceStateGap after the session's last message.
func (m *voiceMumble) nextLocked(now time.Time) (proto.Message, time.Duration) {
	move := m.moving != nil && !m.moving.sent

	at := now
	switch {
	case move:
	case m.wantedSet && m.wanted != m.self.held():
		if m.sentOpen && m.sent == m.wanted {
			at = m.sentAt.Add(voiceStateResend)
		}
	default:
		return nil, 0
	}
	if !m.lastAt.IsZero() {
		if next := m.lastAt.Add(voiceStateGap); next.After(at) {
			at = next
		}
	}
	if at.After(now) {
		return nil, at.Sub(now)
	}

	m.lastAt = now
	if move {
		m.moving.sent = true
		close(m.moving.out)

		return &MumbleProto.UserState{Session: proto.Uint32(m.session), ChannelId: proto.Uint32(m.moving.channel)}, 0
	}

	m.sent, m.sentAt, m.sentOpen = m.wanted, now, true

	return &MumbleProto.UserState{Session: proto.Uint32(m.session), SelfMute: proto.Bool(m.wanted.mute), SelfDeaf: proto.Bool(m.wanted.deaf)}, 0
}

// endedWith is how the connection ended: kicked, banned or lost, with the
// sentence; no code while it stands or when it was closed from here.
func (m *voiceMumble) endedWith() voiceEnding {
	m.lock.Lock()
	defer m.lock.Unlock()

	return m.ending
}

// close ends the connection from here, without telling the handler.
func (m *voiceMumble) close() {
	m.finish(voiceEnding{}, false)
}

// finish ends the connection once, telling the handler when it ended by
// itself (tell).
func (m *voiceMumble) finish(ending voiceEnding, tell bool) {
	m.endOnce.Do(func() {
		m.lock.Lock()
		m.ending = ending
		m.lock.Unlock()

		close(m.ended)
		_ = m.raw.Close()

		m.lock.Lock()
		if m.moving != nil {
			m.moving.answer <- voiceLostRefusal()
			m.moving = nil
		}
		m.lock.Unlock()

		// On a goroutine of its own: a write that failed may have been made
		// with the writer's locks held, and the handler ends the session,
		// which takes them.
		if tell {
			go m.safely("the end of a voice session's connection", func() { m.handler.mumbleEnded(ending) })
		}
	})
}

// voiceRemovedEnding is the voice server taking the session off: kicked, or
// banned, with the reason it gave.
func voiceRemovedEnding(ban bool, reason string) voiceEnding {
	if ban {
		return voiceEnding{Code: voiceEndBanned, Reason: withVoiceReason(voiceBannedText, reason)}
	}

	return voiceEnding{Code: voiceEndKicked, Reason: withVoiceReason(voiceKickedText, reason)}
}

// withVoiceReason is a sentence with the voice server's own reason after
// it, when it gave one: "You were removed from the voice server: spam."
func withVoiceReason(sentence, reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return sentence
	}

	sentence = strings.TrimSuffix(sentence, ".") + ": " + reason
	if !strings.HasSuffix(sentence, ".") && !strings.HasSuffix(sentence, "!") && !strings.HasSuffix(sentence, "?") {
		sentence += "."
	}

	return sentence
}

// voiceMoveRefusal is a PermissionDenied answering a move, as the refusal a
// person reads.
func voiceMoveRefusal(kind MumbleProto.PermissionDenied_DenyType, reason string) error {
	switch {
	case kind == MumbleProto.PermissionDenied_ChannelFull:
		return voiceDeniedRefusal(voiceChannelFullText)
	case kind == MumbleProto.PermissionDenied_Text && strings.TrimSpace(reason) != "":
		return voiceDeniedRefusal(withVoiceReason(voiceDeniedText, reason))
	default:
		return voiceDeniedRefusal(voiceDeniedText)
	}
}

// voiceRejectedRefusal is the voice server refusing a connection, as the
// refusal a person reads.
func voiceRejectedRefusal(rejected *voiceMumbleRejected, name string) *voiceRefusal {
	switch rejected.Type {
	case MumbleProto.Reject_WrongServerPW:
		return voiceRefusedRefusal(voiceRejectedPasswordText)
	case MumbleProto.Reject_WrongUserPW:
		// The name is a registered user's, and this connection has neither
		// that user's certificate nor password.
		return voiceRefusedRefusal(voiceNameRegisteredText(name))
	case MumbleProto.Reject_ServerFull:
		return voiceRefusedRefusal(voiceRejectedFullText)
	case MumbleProto.Reject_InvalidUsername:
		return voiceRefusedRefusal(voiceNameInvalidText(name))
	case MumbleProto.Reject_UsernameInUse:
		return voiceRefusedRefusal(voiceNameInUseText(name))
	case MumbleProto.Reject_WrongVersion:
		return voiceRefusedRefusal(voiceRejectedVersionText)
	case MumbleProto.Reject_NoCertificate:
		return voiceRefusedRefusal(voiceRejectedCertificateText)
	default:
		return voiceRefusedRefusal(withVoiceReason(voiceRejectedText, rejected.Reason))
	}
}
