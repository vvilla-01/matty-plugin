package standin

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/golang/protobuf/proto"
	"layeh.com/gumble/gumble"
	"layeh.com/gumble/gumble/MumbleProto"
)

// Mumble stands in for a Mumble server, as far as VillaNET uses one: a client
// that connects is told who is on the server, is told again whenever that
// changes, and has its pings answered. It speaks Mumble's own protocol over
// TLS with a certificate it signs itself, as Mumble servers do, so the plugin
// connects to it exactly as it connects to the real thing, and nothing in the
// plugin knows the difference.
//
// The people in voice are made up, and steered while it runs:
//
//	curl -X POST 'localhost:7788/fake/voice?name=bob'               joins, or stays as they are
//	curl -X POST 'localhost:7788/fake/voice?name=bob&muted=true'    mutes themselves (deafened=true: deafened)
//	curl -X POST 'localhost:7788/fake/voice?name=bob&on=false'      leaves
//	curl -X POST 'localhost:7788/fake/voice?name=bob&say=hello'     says it in the text chat
//	curl 'localhost:7788/fake/voice'                                who is in voice, who is connected for real, and what those sent
//
// A name that is somebody's Mattermost username is that person, when the
// plugin's "Match names to usernames" is on; any other name is a guest.
//
// It keeps count of what a real connection sends: the voice packets (a sound
// played from the soundboard) and the text messages (the chat bridge).
type Mumble struct {
	tls *tls.Config

	// mu is held for every change and for every write that tells a client
	// about one, so each client hears the changes in the order they happened.
	mu      sync.Mutex
	people  map[string]*VoicePerson
	clients map[uint32]*mumbleClient
	next    uint32
	heard   int
	said    []string
}

// VoicePerson is somebody made up, in voice.
type VoicePerson struct {
	Name     string `json:"name"`
	Muted    bool   `json:"muted"`
	Deafened bool   `json:"deafened"`

	session uint32
}

// mumbleClient is a real connection: VillaNET's own.
type mumbleClient struct {
	name string
	conn *gumble.Conn
}

// Mumble's message numbers, for the ones read here.
const (
	mumbleUDPTunnel       = 1
	mumbleAuthenticate    = 2
	mumblePing            = 3
	mumbleTextMessage     = 11
	mumblePermissionQuery = 20
)

const (
	// The two channels: where a client lands, and where the people are. With
	// one voice channel under the root, VillaNET's own connection stays
	// parked in the root, as on a real server.
	mumbleRoot  = 0
	mumbleVoice = 1

	// What an ordinary user may do in a channel: traverse, enter, speak,
	// whisper and send text.
	mumbleUserPermissions = 0x2 | 0x4 | 0x8 | 0x100 | 0x200

	// The most a server lets a client send, bits a second: Murmur's default.
	mumbleBandwidth = 72000

	mumbleWriteBudget = 5 * time.Second
)

// NewMumble makes a server with these people in voice: each entry is a name,
// or name=muted, or name=deafened.
func NewMumble(people ...string) (*Mumble, error) {
	certificate, err := selfSigned()
	if err != nil {
		return nil, err
	}

	m := &Mumble{
		tls:     &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12},
		people:  map[string]*VoicePerson{},
		clients: map[uint32]*mumbleClient{},
	}
	for _, entry := range people {
		name, how, _ := strings.Cut(strings.TrimSpace(entry), "=")
		if name == "" {
			continue
		}
		m.next++
		m.people[name] = &VoicePerson{Name: name, Muted: how == "muted", Deafened: how == "deafened", session: m.next}
	}

	return m, nil
}

// selfSigned is a certificate nobody vouches for, which is what a Mumble
// server has: its clients trust the first one they see.
func selfSigned() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "VillaNET stand-in Mumble"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}

	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}

// Serve answers every connection to listener until it is closed.
func (m *Mumble) Serve(listener net.Listener) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}

		go m.serve(tls.Server(conn, m.tls))
	}
}

// serve is one client, from its first packet to its last.
func (m *Mumble) serve(raw net.Conn) {
	defer raw.Close()

	conn := gumble.NewConn(raw)

	// A client says its version, then who it is. Nothing is checked: any
	// name and any password get in.
	name := ""
	for name == "" {
		kind, data, err := conn.ReadPacket()
		if err != nil {
			return
		}
		if kind != mumbleAuthenticate {
			continue
		}

		var hello MumbleProto.Authenticate
		if proto.Unmarshal(data, &hello) != nil || hello.GetUsername() == "" {
			return
		}
		name = hello.GetUsername()
	}

	session := m.welcome(name, conn)
	defer m.goodbye(session)

	for {
		kind, data, err := conn.ReadPacket()
		if err != nil {
			return
		}

		switch kind {
		case mumblePing:
			var ping MumbleProto.Ping
			if proto.Unmarshal(data, &ping) == nil {
				send(conn, &MumbleProto.Ping{Timestamp: ping.Timestamp})
			}

		case mumbleUDPTunnel:
			m.mu.Lock()
			m.heard++
			m.mu.Unlock()

		case mumbleTextMessage:
			var message MumbleProto.TextMessage
			if proto.Unmarshal(data, &message) == nil {
				m.mu.Lock()
				m.said = append(m.said, message.GetMessage())
				m.mu.Unlock()
			}

		case mumblePermissionQuery:
			var query MumbleProto.PermissionQuery
			if proto.Unmarshal(data, &query) == nil && query.ChannelId != nil && query.GetChannelId() <= mumbleVoice {
				send(conn, &MumbleProto.PermissionQuery{ChannelId: query.ChannelId, Permissions: proto.Uint32(mumbleUserPermissions)})
			}
		}
	}
}

// welcome tells a new client everything, in the order Murmur does: the
// channels, everybody on the server, itself, and then that it is in step. The
// client's own entry has to come before that last message, which names it.
func (m *Mumble) welcome(name string, conn *gumble.Conn) uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.next++
	session := m.next

	key, nonce := make([]byte, 16), make([]byte, 32)
	_, _ = rand.Read(key)
	_, _ = rand.Read(nonce)

	send(conn, &MumbleProto.Version{Version: proto.Uint32(1<<16 | 3<<8), Release: proto.String("VillaNET stand-in")})
	send(conn, &MumbleProto.CryptSetup{Key: key, ClientNonce: nonce[:16], ServerNonce: nonce[16:]})
	send(conn, &MumbleProto.CodecVersion{Alpha: proto.Int32(-2147483637), Beta: proto.Int32(0), PreferAlpha: proto.Bool(true), Opus: proto.Bool(true)})
	send(conn, &MumbleProto.ChannelState{ChannelId: proto.Uint32(mumbleRoot), Name: proto.String("Root")})
	send(conn, &MumbleProto.ChannelState{ChannelId: proto.Uint32(mumbleVoice), Parent: proto.Uint32(mumbleRoot), Name: proto.String("Voice")})
	for _, person := range m.sorted() {
		send(conn, person.state())
	}
	for other, client := range m.clients {
		send(conn, &MumbleProto.UserState{Session: proto.Uint32(other), Name: proto.String(client.name), ChannelId: proto.Uint32(mumbleRoot)})
	}

	own := &MumbleProto.UserState{Session: proto.Uint32(session), Name: proto.String(name), ChannelId: proto.Uint32(mumbleRoot)}
	send(conn, own)
	send(conn, &MumbleProto.ServerSync{
		Session:      proto.Uint32(session),
		MaxBandwidth: proto.Uint32(mumbleBandwidth),
		WelcomeText:  proto.String("The VillaNET stand-in for Mumble."),
		Permissions:  proto.Uint64(mumbleUserPermissions),
	})
	send(conn, &MumbleProto.ServerConfig{MaxBandwidth: proto.Uint32(mumbleBandwidth), AllowHtml: proto.Bool(true), MessageLength: proto.Uint32(5000)})

	m.tell(own)
	m.clients[session] = &mumbleClient{name: name, conn: conn}

	return session
}

func (m *Mumble) goodbye(session uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.clients, session)
	m.tell(&MumbleProto.UserRemove{Session: proto.Uint32(session)})
}

// state is everything a client is told about somebody made up.
func (p *VoicePerson) state() *MumbleProto.UserState {
	return &MumbleProto.UserState{
		Session:   proto.Uint32(p.session),
		Name:      proto.String(p.Name),
		ChannelId: proto.Uint32(mumbleVoice),
		SelfMute:  proto.Bool(p.Muted || p.Deafened),
		SelfDeaf:  proto.Bool(p.Deafened),
	}
}

// send writes one message, giving up on a client that has stopped reading.
func send(conn *gumble.Conn, message proto.Message) {
	_ = conn.SetWriteDeadline(time.Now().Add(mumbleWriteBudget))
	_ = conn.WriteProto(message)
}

// tell sends every connected client one message. With mu held.
func (m *Mumble) tell(message proto.Message) {
	for _, client := range m.clients {
		send(client.conn, message)
	}
}

// sorted is the people made up, by name. With mu held.
func (m *Mumble) sorted() []*VoicePerson {
	people := make([]*VoicePerson, 0, len(m.people))
	for _, person := range m.people {
		people = append(people, person)
	}
	sort.Slice(people, func(i, j int) bool { return people[i].Name < people[j].Name })

	return people
}

// ---------------------------------------------------------------- steering

// Join puts somebody in voice, or changes how they are in it: muted and
// deafened are left as they were when nil.
func (m *Mumble) Join(name string, muted, deafened *bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	person, in := m.people[name]
	if !in {
		m.next++
		person = &VoicePerson{Name: name, session: m.next}
		m.people[name] = person
	}
	if muted != nil {
		person.Muted = *muted
	}
	if deafened != nil {
		person.Deafened = *deafened
	}

	m.tell(person.state())
}

// Leave takes somebody out of voice.
func (m *Mumble) Leave(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	person, in := m.people[name]
	if !in {
		return
	}

	delete(m.people, name)
	m.tell(&MumbleProto.UserRemove{Session: proto.Uint32(person.session)})
}

// Say has somebody in voice write in the text chat of the channel where a
// client lands, which is where VillaNET's own connection listens.
func (m *Mumble) Say(name, text string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	person, in := m.people[name]
	if !in {
		return false
	}

	m.tell(&MumbleProto.TextMessage{Actor: proto.Uint32(person.session), ChannelId: []uint32{mumbleRoot}, Message: proto.String(text)})

	return true
}

// People is who is in voice, by name.
func (m *Mumble) People() []VoicePerson {
	m.mu.Lock()
	defer m.mu.Unlock()

	people := []VoicePerson{}
	for _, person := range m.sorted() {
		people = append(people, *person)
	}

	return people
}

// Connected is the names of the real connections, in order.
func (m *Mumble) Connected() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := []string{}
	for _, client := range m.clients {
		names = append(names, client.name)
	}
	sort.Strings(names)

	return names
}

// Heard is how many voice packets real connections have sent, and Said the
// text messages they sent, oldest first.
func (m *Mumble) Heard() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.heard
}

func (m *Mumble) Said() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	return append([]string{}, m.said...)
}

// Routes adds the stand-in's own address to mux.
func (m *Mumble) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/fake/voice", m.steer)
}

// steer answers /fake/voice: a GET says who is in voice, a POST changes the
// person name names and says the same.
func (m *Mumble) steer(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	if r.Method == http.MethodPost {
		name := strings.TrimSpace(query.Get("name"))
		if name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is the Mumble name of whoever this is about"})
			return
		}

		if query.Get("on") == "false" {
			m.Leave(name)
		} else {
			var muted, deafened *bool
			if query.Has("muted") {
				muted = proto.Bool(query.Get("muted") == "true")
			}
			if query.Has("deafened") {
				deafened = proto.Bool(query.Get("deafened") == "true")
			}
			m.Join(name, muted, deafened)

			if query.Has("say") {
				m.Say(name, query.Get("say"))
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"people": m.People(), "connected": m.Connected(), "heard": m.Heard(), "said": m.Said()})
}
