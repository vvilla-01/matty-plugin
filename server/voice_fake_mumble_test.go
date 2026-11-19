package main

import (
	"crypto/tls"
	"net"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/stretchr/testify/require"
	"layeh.com/gumble/gumble"
	"layeh.com/gumble/gumble/MumbleProto"
	"layeh.com/gumble/gumble/varint"
)

// relayMumble is a voice server for tests that takes many connections and
// does what Murmur was measured doing (DESIGN's facts, from 1.5.939): it
// passes voice on to everybody else in the sender's channel with the
// sender's session, sequence and last-frame flag intact, never back to the
// sender, and not from or to the muted or deafened; it honours moves,
// refuses a full or forbidden channel with PermissionDenied, ignores a move
// to a channel that does not exist, takes self mute and deafen, kicks the
// first connection when a second one takes the same name in any case, and
// can kick, ban, mute, deafen and suppress, mute and deafen coupled as
// Murmur couples them. Made-up people without a connection can talk.
type relayMumble struct {
	t        *testing.T
	listener net.Listener

	lock sync.Mutex

	password   string
	full       bool
	badName    func(name string) bool
	registered func(name string) bool

	channels map[uint32]relayChannel
	users    map[uint32]*relayUser
	next     uint32

	// Every connection taken, let in or not.
	dials int

	// Channels refused to everybody: the reason, "" for a plain refusal
	// of permission.
	deny map[uint32]string

	// Moves are read and never answered.
	silentMoves bool

	// Voice the server was sent, by sender, and of it what it passed on
	// from the connections, which nothing from the muted is.
	heard   map[uint32][]relayAudio
	relayed map[uint32][]relayAudio
}

type relayChannel struct {
	id, parent uint32
	name       string
	max        uint32
}

type relayUser struct {
	session uint32
	name    string
	channel uint32

	mute, deaf, suppress bool
	selfMute, selfDeaf   bool

	// nil for a made-up person.
	conn *gumble.Conn
}

type relayAudio struct {
	sequence int64
	final    bool
	payload  []byte
}

// newRelayMumble is the rig's tree: Test (the root, 0), Lobby 1, Gaming 2,
// AFK 3, and Duo 4 under Gaming for two people.
func newRelayMumble(t *testing.T) *relayMumble {
	t.Helper()

	listener, err := tls.Listen("tcp", "127.0.0.1:0", selfSignedTLS(t))
	require.NoError(t, err)

	f := &relayMumble{
		t:        t,
		listener: listener,
		channels: map[uint32]relayChannel{
			0: {id: 0, name: "Test"},
			1: {id: 1, name: "Lobby"},
			2: {id: 2, name: "Gaming"},
			3: {id: 3, name: "AFK"},
			4: {id: 4, parent: 2, name: "Duo", max: 2},
		},
		users:   map[uint32]*relayUser{},
		next:    10,
		deny:    map[uint32]string{},
		heard:   map[uint32][]relayAudio{},
		relayed: map[uint32][]relayAudio{},
	}

	t.Cleanup(func() {
		_ = listener.Close()
		f.lock.Lock()
		for _, user := range f.users {
			if user.conn != nil {
				_ = user.conn.Close()
			}
		}
		f.lock.Unlock()
	})

	go func() {
		for {
			raw, err := listener.Accept()
			if err != nil {
				return
			}
			go f.serve(raw)
		}
	}()

	return f
}

func (f *relayMumble) address() string {
	return f.listener.Addr().String()
}

func (f *relayMumble) set(change func(f *relayMumble)) {
	f.lock.Lock()
	defer f.lock.Unlock()

	change(f)
}

// serve is one connection.
func (f *relayMumble) serve(raw net.Conn) {
	conn := gumble.NewConn(raw)
	conn.Timeout = time.Minute

	f.lock.Lock()
	f.dials++
	f.lock.Unlock()

	var auth MumbleProto.Authenticate
	for {
		kind, data, err := conn.ReadPacket()
		if err != nil {
			_ = raw.Close()
			return
		}
		if kind == 2 {
			if proto.Unmarshal(data, &auth) != nil {
				_ = raw.Close()
				return
			}
			break
		}
	}

	name := auth.GetUsername()

	f.lock.Lock()
	var reject *MumbleProto.Reject
	switch {
	case f.password != "" && auth.GetPassword() != f.password:
		reject = &MumbleProto.Reject{Type: MumbleProto.Reject_WrongServerPW.Enum(), Reason: proto.String("Wrong certificate or password for existing user")}
	case f.full:
		reject = &MumbleProto.Reject{Type: MumbleProto.Reject_ServerFull.Enum()}
	case f.badName != nil && f.badName(name):
		reject = &MumbleProto.Reject{Type: MumbleProto.Reject_InvalidUsername.Enum(), Reason: proto.String("Invalid username")}
	case f.registered != nil && f.registered(name):
		reject = &MumbleProto.Reject{Type: MumbleProto.Reject_WrongUserPW.Enum(), Reason: proto.String("Wrong certificate or password for existing user")}
	}
	if reject != nil {
		f.lock.Unlock()
		_ = conn.WriteProto(reject)
		_ = raw.Close()

		return
	}

	// The same name again, in any case: the first goes.
	for _, other := range f.users {
		if strings.EqualFold(other.name, name) {
			f.removeLocked(other.session, "You connected to the server from another device", false)
		}
	}

	f.next++
	me := &relayUser{session: f.next, name: name, conn: conn}
	f.users[me.session] = me

	var channels []relayChannel
	for _, channel := range f.channels {
		channels = append(channels, channel)
	}
	sort.Slice(channels, func(i, j int) bool { return channels[i].id < channels[j].id })
	for _, channel := range channels {
		state := &MumbleProto.ChannelState{ChannelId: proto.Uint32(channel.id), Name: proto.String(channel.name)}
		if channel.id != 0 {
			state.Parent = proto.Uint32(channel.parent)
		}
		if channel.max > 0 {
			state.MaxUsers = proto.Uint32(channel.max)
		}
		_ = conn.WriteProto(state)
	}
	for _, user := range f.users {
		_ = conn.WriteProto(user.stateLocked())
	}
	for _, user := range f.users {
		if user != me && user.conn != nil {
			_ = user.conn.WriteProto(me.stateLocked())
		}
	}
	f.lock.Unlock()

	_ = conn.WriteProto(&MumbleProto.ServerSync{Session: proto.Uint32(me.session), MaxBandwidth: proto.Uint32(72000)})

	for {
		kind, data, err := conn.ReadPacket()
		if err != nil {
			f.lock.Lock()
			if f.users[me.session] == me {
				f.removeLocked(me.session, "", false)
			}
			f.lock.Unlock()

			return
		}

		switch kind {
		case 3:
			var ping MumbleProto.Ping
			_ = proto.Unmarshal(data, &ping)
			_ = conn.WriteProto(&ping)
		case 9:
			var state MumbleProto.UserState
			if proto.Unmarshal(data, &state) == nil {
				f.userState(me, &state)
			}
		case 1:
			f.voice(me, data)
		}
	}
}

func (u *relayUser) stateLocked() *MumbleProto.UserState {
	return &MumbleProto.UserState{
		Session:   proto.Uint32(u.session),
		Name:      proto.String(u.name),
		ChannelId: proto.Uint32(u.channel),
		Mute:      proto.Bool(u.mute),
		Deaf:      proto.Bool(u.deaf),
		Suppress:  proto.Bool(u.suppress),
		SelfMute:  proto.Bool(u.selfMute),
		SelfDeaf:  proto.Bool(u.selfDeaf),
	}
}

// broadcastLocked sends a message to every connection.
func (f *relayMumble) broadcastLocked(message proto.Message) {
	for _, user := range f.users {
		if user.conn != nil {
			_ = user.conn.WriteProto(message)
		}
	}
}

// removeLocked takes somebody off, telling everybody (them included).
func (f *relayMumble) removeLocked(session uint32, reason string, ban bool) {
	user := f.users[session]
	if user == nil {
		return
	}

	remove := &MumbleProto.UserRemove{Session: proto.Uint32(session)}
	if reason != "" {
		remove.Reason = proto.String(reason)
	}
	if ban {
		remove.Ban = proto.Bool(true)
	}
	f.broadcastLocked(remove)

	delete(f.users, session)
	if user.conn != nil {
		_ = user.conn.Close()
	}
}

// userState is a client asking for a move or a change of its own state.
func (f *relayMumble) userState(me *relayUser, state *MumbleProto.UserState) {
	f.lock.Lock()
	defer f.lock.Unlock()

	if state.Session != nil && state.GetSession() != me.session {
		return
	}

	if state.ChannelId != nil && state.GetChannelId() != me.channel {
		target := state.GetChannelId()
		channel, known := f.channels[target]
		reason, denied := f.deny[target]

		in := 0
		for _, user := range f.users {
			if user.channel == target {
				in++
			}
		}

		switch {
		case f.silentMoves, !known:
		case denied && reason != "":
			_ = me.conn.WriteProto(&MumbleProto.PermissionDenied{Type: MumbleProto.PermissionDenied_Text.Enum(), Reason: proto.String(reason)})
		case denied:
			_ = me.conn.WriteProto(&MumbleProto.PermissionDenied{
				Type: MumbleProto.PermissionDenied_Permission.Enum(), Session: proto.Uint32(me.session),
				ChannelId: proto.Uint32(target), Permission: proto.Uint32(uint32(gumble.PermissionEnter)),
			})
		case channel.max > 0 && in >= int(channel.max):
			_ = me.conn.WriteProto(&MumbleProto.PermissionDenied{Type: MumbleProto.PermissionDenied_ChannelFull.Enum()})
		default:
			me.channel = target
			f.broadcastLocked(&MumbleProto.UserState{Session: proto.Uint32(me.session), Actor: proto.Uint32(me.session), ChannelId: proto.Uint32(target)})
		}
	}

	if state.SelfMute != nil || state.SelfDeaf != nil {
		if state.SelfDeaf != nil {
			me.selfDeaf = state.GetSelfDeaf()
			if me.selfDeaf {
				me.selfMute = true
			}
		}
		if state.SelfMute != nil {
			me.selfMute = state.GetSelfMute() || me.selfDeaf
		}
		f.broadcastLocked(&MumbleProto.UserState{Session: proto.Uint32(me.session), Actor: proto.Uint32(me.session), SelfMute: proto.Bool(me.selfMute), SelfDeaf: proto.Bool(me.selfDeaf)})
	}
}

// voice is a voice packet from a client: noted, and passed on.
func (f *relayMumble) voice(from *relayUser, data []byte) {
	if len(data) < 1 || data[0]>>5 != 4 {
		return
	}

	rest := data[1:]
	sequence, n := varint.Decode(rest)
	if n <= 0 {
		return
	}
	size, m := varint.Decode(rest[n:])
	if m <= 0 {
		return
	}
	length := int(size &^ 0x2000)
	if length > len(rest[n+m:]) {
		return
	}

	f.lock.Lock()
	defer f.lock.Unlock()

	audio := relayAudio{
		sequence: sequence,
		final:    size&0x2000 != 0,
		payload:  append([]byte(nil), rest[n+m:n+m+length]...),
	}
	f.heard[from.session] = append(f.heard[from.session], audio)

	if f.relayLocked(from, data[0], rest) {
		f.relayed[from.session] = append(f.relayed[from.session], audio)
	}
}

// relayLocked passes a packet on to the sender's channel: header byte,
// sender's session, then the rest as it came. It is false for a sender
// whose voice is passed on to nobody: muted or deafened, by themselves or by
// an admin, or suppressed.
func (f *relayMumble) relayLocked(from *relayUser, header byte, rest []byte) bool {
	if from.mute || from.deaf || from.suppress || from.selfMute || from.selfDeaf {
		return false
	}

	var session [varint.MaxVarintLen]byte
	n := varint.Encode(session[:], int64(from.session))

	packet := make([]byte, 0, 1+n+len(rest))
	packet = append(packet, header&^0x1F)
	packet = append(packet, session[:n]...)
	packet = append(packet, rest...)

	for _, user := range f.users {
		if user == from || user.conn == nil || user.channel != from.channel || user.deaf || user.selfDeaf {
			continue
		}
		_ = user.conn.WritePacket(1, packet)
	}

	return true
}

// add is a made-up person in a channel.
func (f *relayMumble) add(name string, channel uint32) uint32 {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.next++
	user := &relayUser{session: f.next, name: name, channel: channel}
	f.users[user.session] = user
	f.broadcastLocked(user.stateLocked())

	return user.session
}

// speak is a made-up person talking: one Opus packet into their channel.
func (f *relayMumble) speak(session uint32, sequence int64, payload []byte, final bool) {
	f.lock.Lock()
	defer f.lock.Unlock()

	from := f.users[session]
	if from == nil {
		return
	}

	var buffer [2 * varint.MaxVarintLen]byte
	n := varint.Encode(buffer[:], sequence)
	size := int64(len(payload))
	if final {
		size |= 0x2000
	}
	m := varint.Encode(buffer[n:], size)

	rest := append(append([]byte(nil), buffer[:n+m]...), payload...)
	_ = f.relayLocked(from, 4<<5, rest)
}

// kick takes a session off the server, banned or not, with a reason.
func (f *relayMumble) kick(session uint32, reason string, ban bool) {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.removeLocked(session, reason, ban)
}

// serverMute is an admin muting or suppressing somebody. Unmuting undeafens
// too, and says so.
func (f *relayMumble) serverMute(session uint32, mute, suppress bool) {
	f.lock.Lock()
	defer f.lock.Unlock()

	user := f.users[session]
	if user == nil {
		return
	}
	user.mute, user.suppress = mute, suppress
	state := &MumbleProto.UserState{Session: proto.Uint32(session), Mute: proto.Bool(mute), Suppress: proto.Bool(suppress)}
	if !mute {
		user.deaf = false
		state.Deaf = proto.Bool(false)
	}
	f.broadcastLocked(state)
}

// serverDeaf is an admin deafening somebody, which mutes them too and says
// so, or undeafening them, which leaves the mute and says nothing of it.
func (f *relayMumble) serverDeaf(session uint32, deaf bool) {
	f.lock.Lock()
	defer f.lock.Unlock()

	user := f.users[session]
	if user == nil {
		return
	}
	user.deaf = deaf
	state := &MumbleProto.UserState{Session: proto.Uint32(session), Deaf: proto.Bool(deaf)}
	if deaf {
		user.mute = true
		state.Mute = proto.Bool(true)
	}
	f.broadcastLocked(state)
}

// moveOther is an admin moving somebody.
func (f *relayMumble) moveOther(session, channel uint32) {
	f.lock.Lock()
	defer f.lock.Unlock()

	user := f.users[session]
	if user == nil {
		return
	}
	user.channel = channel
	f.broadcastLocked(&MumbleProto.UserState{Session: proto.Uint32(session), ChannelId: proto.Uint32(channel)})
}

// user is a copy of somebody's state, and whether they are there.
func (f *relayMumble) user(session uint32) (relayUser, bool) {
	f.lock.Lock()
	defer f.lock.Unlock()

	user := f.users[session]
	if user == nil {
		return relayUser{}, false
	}

	return *user, true
}

// named is the session with a name, or 0.
func (f *relayMumble) named(name string) uint32 {
	f.lock.Lock()
	defer f.lock.Unlock()

	for _, user := range f.users {
		if user.name == name {
			return user.session
		}
	}

	return 0
}

func (f *relayMumble) dialCount() int {
	f.lock.Lock()
	defer f.lock.Unlock()

	return f.dials
}

func (f *relayMumble) heardFrom(session uint32) []relayAudio {
	f.lock.Lock()
	defer f.lock.Unlock()

	return append([]relayAudio(nil), f.heard[session]...)
}

// relayedFrom is what the server passed on from a connection.
func (f *relayMumble) relayedFrom(session uint32) []relayAudio {
	f.lock.Lock()
	defer f.lock.Unlock()

	return append([]relayAudio(nil), f.relayed[session]...)
}
