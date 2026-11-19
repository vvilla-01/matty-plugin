package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/logging"
	"github.com/pion/rtp"
	"github.com/pion/sdp/v3"
	"github.com/pion/webrtc/v4"
)

// Phones' voice, over WebRTC, on one UDP port.
//
// A phone that joins voice opens one WebRTC connection to this server, audio
// only: one section it sends its microphone on, a few it receives on (one
// for each person it hears at once), and a data channel "voice" on which the
// server says who is talking and how the session stands. Every phone comes
// in on the same UDP port, which the admin opens in the firewall; the
// candidates this server gives a phone are the addresses the admin set (or
// the Site URL's host) on that port, and nothing else.
//
// Nothing is decoded here: the phone's Opus goes to its Mumble session
// (voice_talk.go) as it came, and the voice server's comes back as RTP.

const (
	// The port phones reach this server on, unless an admin says another.
	voiceTalkDefaultPort = 8445
	voiceTalkMinPort     = 1024
	voiceTalkMaxPort     = 65535

	// How often the addresses given to phones are looked up again.
	voiceTalkResolveEvery = 10 * time.Minute

	// How soon a listener that is down is first tried again.
	voiceTalkRetryFirst = 30 * time.Second

	// How long one look-up of an address may take.
	voiceTalkResolveWait = 5 * time.Second

	// How long this server's candidates may take to gather. With the one
	// port and no STUN server they are there at once.
	voiceTalkGatherWait = 5 * time.Second

	// A phone whose connection has been disconnected this long is gone.
	voicePhoneDisconnectedWait = 5 * time.Second

	// How long the data channel has to send its last message before the
	// connection closes.
	voicePhoneDrainWait = time.Second

	// How long stopping talk waits for the phones' connections to close
	// before it closes the port under them.
	voiceTalkCloseWait = 2 * time.Second

	// The data channel the phone opens.
	voiceDataChannelLabel = "voice"

	// How many receive-only sections an offer may have.
	voiceMostReceiveSections = 8

	// How many candidates any one section of an offer may carry: a phone has
	// a few addresses, and this server checks every one it is given. A phone
	// may repeat its candidates in every section, so the limit is a section's,
	// and the limit on sections bounds the whole.
	voiceMostCandidates = 16

	// How long a data channel the phone should not have opened holds up
	// pion's next one while its connection closes.
	voicePhoneRefuseWait = 3 * time.Second

	// Opus as the phone and the voice server speak it.
	voiceOpusPayloadType = 111
)

// What a person or an admin reads.
const (
	voiceTalkNoAddressText = "Set the address phones reach this server at."
	voiceTalkPortRangeText = "The voice port must be between 1024 and 65535."
	voiceTalkClusterText   = "Talking from Matty works on a single Mattermost server only."
	voiceTalkBrokenText    = "Talking from Matty is not working on this server just now."
	voiceTalkStartingText  = "Talking from Matty is starting on this server. Try again in a moment."
	voiceBadOfferText      = "That voice connection is not one this server can answer."
	voicePeerFailedText    = "This server could not set up the voice connection. Try again in a moment."
)

// voiceTalkPortAllowed is whether a voice port is one talk opens: 1024 to
// 65535. A port below 1024 needs privileges a Mattermost server does not run
// with, so one set there is not replaced but leaves talk off, saying why.
func voiceTalkPortAllowed(port int) bool {
	return port >= voiceTalkMinPort && port <= voiceTalkMaxPort
}

// voiceTalkPortText is the voice port not opened, with the system's reason.
func voiceTalkPortText(port int, err error) string {
	why := "it is in use or not allowed"
	var opError *net.OpError
	if errors.As(err, &opError) && opError.Err != nil {
		why = opError.Err.Error()
	}

	return fmt.Sprintf("The voice port %d (UDP) could not be opened: %s.", port, why)
}

// voiceTalkUnresolvedText is a host name for phones that leads nowhere.
func voiceTalkUnresolvedText(name string) string {
	return `The address for phones "` + name + `" leads to no IPv4 address.`
}

// voiceTalkIPv6Text is an IPv6 address for phones, which are reached on IPv4.
func voiceTalkIPv6Text(address string) string {
	return `The address for phones "` + address + `" is an IPv6 address, and phones reach this server on IPv4 only.`
}

// voiceOpusCapability is the one codec voice registers, as the phone offers
// it.
var voiceOpusCapability = webrtc.RTPCodecCapability{
	MimeType:    webrtc.MimeTypeOpus,
	ClockRate:   48000,
	Channels:    2,
	SDPFmtpLine: "minptime=10;useinbandfec=1",
}

// voiceSilentLogs keeps pion's own logging out of the server log.
var voiceSilentLogs = &logging.DefaultLoggerFactory{Writer: io.Discard, DefaultLogLevel: logging.LogLevelDisabled}

// ---------------------------------------------------------------- the offer

// voiceOfferShape checks an offer as the contract has it: exactly one audio
// section the phone sends on, with Opus; one to eight audio sections it only
// receives on; one data channel section; nothing else; and at most
// voiceMostCandidates candidates in any one section. It is how many
// receive-only sections there are, or false.
func voiceOfferShape(offer string) (int, bool) {
	var description sdp.SessionDescription
	if description.UnmarshalString(offer) != nil {
		return 0, false
	}

	if voiceCandidateCount(description.Attributes) > voiceMostCandidates {
		return 0, false
	}
	send, receive, application := 0, 0, 0
	for _, media := range description.MediaDescriptions {
		if media.MediaName.Port.Value == 0 {
			return 0, false
		}
		if voiceCandidateCount(media.Attributes) > voiceMostCandidates {
			return 0, false
		}

		switch media.MediaName.Media {
		case "audio":
			switch voiceSectionDirection(media) {
			case "sendonly":
				if !voiceSectionHasOpus(media) {
					return 0, false
				}
				send++
			case "recvonly":
				receive++
			default:
				return 0, false
			}
		case "application":
			application++
		default:
			return 0, false
		}
	}

	if send != 1 || receive < 1 || receive > voiceMostReceiveSections || application != 1 {
		return 0, false
	}

	return receive, true
}

// voiceCandidateCount is how many candidates a list of attributes carries.
func voiceCandidateCount(attributes []sdp.Attribute) int {
	count := 0
	for _, attribute := range attributes {
		if attribute.Key == "candidate" {
			count++
		}
	}

	return count
}

// voiceRemoteAllowed is whether this server sends its connection checks to
// an address a phone gave: never to an unspecified, multicast or broadcast
// one. Loopback stays allowed: a phone in an emulator on this machine comes
// from 127.0.0.1.
func voiceRemoteAllowed(ip net.IP) bool {
	return !ip.IsUnspecified() && !ip.IsMulticast() && !ip.Equal(net.IPv4bcast)
}

// voiceSectionDirection is a media section's direction, sendrecv when it
// says none.
func voiceSectionDirection(media *sdp.MediaDescription) string {
	for _, attribute := range media.Attributes {
		switch attribute.Key {
		case "sendonly", "recvonly", "sendrecv", "inactive":
			return attribute.Key
		}
	}

	return "sendrecv"
}

// voiceSectionHasOpus is whether a section offers Opus at 48 kHz.
func voiceSectionHasOpus(media *sdp.MediaDescription) bool {
	for _, attribute := range media.Attributes {
		if attribute.Key != "rtpmap" {
			continue
		}
		fields := strings.Fields(attribute.Value)
		if len(fields) == 2 && strings.HasPrefix(strings.ToLower(fields[1]), "opus/48000") {
			return true
		}
	}

	return false
}

// ---------------------------------------------------------------- addresses

// voiceLookup is how a host name is looked up: the system's resolver, or a
// test's.
type voiceLookup func(ctx context.Context, host string) ([]net.IPAddr, error)

// resolveVoiceAddresses works out the IPv4 addresses phones are given: each
// line of the setting, an address or a host name, or with none the host of
// the Site URL. A line that leads nowhere, or is IPv6, is a problem and
// leaves the listener down; so is a Site URL whose host is this machine
// itself, since a phone cannot reach that.
func resolveVoiceAddresses(ctx context.Context, lines []string, siteURL string, lookup voiceLookup) ([]string, string) {
	if len(lines) == 0 {
		host := ""
		if parsed, err := url.Parse(strings.TrimSpace(siteURL)); err == nil {
			host = parsed.Hostname()
		}
		if host == "" {
			return nil, voiceTalkNoAddressText
		}

		addresses, problem := resolveVoiceAddress(ctx, host, lookup)
		if problem != "" {
			return nil, voiceTalkNoAddressText
		}
		addresses = slices.DeleteFunc(addresses, func(address string) bool { return net.ParseIP(address).IsLoopback() })
		if len(addresses) == 0 {
			return nil, voiceTalkNoAddressText
		}

		return addresses, ""
	}

	var all []string
	for _, line := range lines {
		addresses, problem := resolveVoiceAddress(ctx, line, lookup)
		if problem != "" {
			return nil, problem
		}
		for _, address := range addresses {
			if !slices.Contains(all, address) {
				all = append(all, address)
			}
		}
	}

	return all, ""
}

// resolveVoiceAddress is one line's IPv4 addresses, or why it has none.
func resolveVoiceAddress(ctx context.Context, line string, lookup voiceLookup) ([]string, string) {
	host := strings.Trim(strings.TrimSpace(line), "[]")

	if ip := net.ParseIP(host); ip != nil {
		if ip.To4() == nil {
			return nil, voiceTalkIPv6Text(line)
		}

		return []string{ip.To4().String()}, ""
	}

	ctx, cancel := context.WithTimeout(ctx, voiceTalkResolveWait)
	defer cancel()

	found, err := lookup(ctx, host)
	if err != nil {
		return nil, voiceTalkUnresolvedText(line)
	}

	var addresses []string
	for _, address := range found {
		if v4 := address.IP.To4(); v4 != nil && !slices.Contains(addresses, v4.String()) {
			addresses = append(addresses, v4.String())
		}
	}
	if len(addresses) == 0 {
		return nil, voiceTalkUnresolvedText(line)
	}

	return addresses, ""
}

// ---------------------------------------------------------------- the listener

// voiceListener is the one UDP port and what answers phones on it.
type voiceListener struct {
	safely func(where string, f func()) bool

	lock sync.Mutex

	// The port asked for and the socket on it, with pion's mux over it.
	port int
	conn *net.UDPConn
	mux  ice.UDPMux

	// This start's DTLS certificate, made once and kept while the plugin
	// runs, and the API built on the mux for the addresses below.
	cert      *webrtc.Certificate
	api       *webrtc.API
	addresses []string

	// Why it is down, for the admins' card and alert: the port, the
	// addresses, or a cluster.
	problem string
	cluster bool

	// The phones' connections made on it, until each closes.
	peers map[*voicePhonePeer]struct{}
}

func newVoiceListener(safely func(string, func()) bool) *voiceListener {
	return &voiceListener{safely: safely, peers: map[*voicePhonePeer]struct{}{}}
}

// voiceListenerState is the listener as the card and the voice word see it.
type voiceListenerState struct {
	up        bool
	port      int
	addresses []string
	problem   string
	cluster   bool
}

func (l *voiceListener) state() voiceListenerState {
	l.lock.Lock()
	defer l.lock.Unlock()

	state := voiceListenerState{
		up:        l.api != nil,
		port:      l.port,
		addresses: slices.Clone(l.addresses),
		problem:   l.problem,
		cluster:   l.cluster,
	}
	if l.conn != nil {
		if address, ok := l.conn.LocalAddr().(*net.UDPAddr); ok {
			state.port = address.Port
		}
	}

	return state
}

// open makes sure the socket is on the port: kept when it already is, else
// opened on every interface, loopback included, IPv4 only. A new port
// closes the old one first, which sends every phone on it into its wait.
func (l *voiceListener) open(port int) error {
	l.lock.Lock()
	if l.conn != nil && l.port == port {
		l.lock.Unlock()
		return nil
	}
	l.lock.Unlock()

	l.closeSocket(true)

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: port})
	if err != nil {
		return err
	}

	l.lock.Lock()
	defer l.lock.Unlock()

	if l.cert == nil {
		key, keyErr := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if keyErr != nil {
			_ = conn.Close()
			return keyErr
		}
		cert, certErr := webrtc.GenerateCertificate(key)
		if certErr != nil {
			_ = conn.Close()
			return certErr
		}
		l.cert = cert
	}

	l.port = port
	l.conn = conn
	l.mux = webrtc.NewICEUDPMux(voiceSilentLogs.NewLogger("voice-udp"), conn)
	l.api = nil
	l.addresses = nil

	return nil
}

// serve builds the API for new phones on the open socket, giving them these
// addresses. Phones already connected keep theirs.
func (l *voiceListener) serve(addresses []string) error {
	l.lock.Lock()
	defer l.lock.Unlock()

	if l.mux == nil {
		return errors.New("the voice port is not open")
	}
	if l.api != nil && slices.Equal(l.addresses, addresses) {
		l.problem = ""
		return nil
	}

	api, err := newVoiceAPI(l.mux, addresses)
	if err != nil {
		return err
	}

	l.api, l.addresses, l.problem = api, slices.Clone(addresses), ""

	return nil
}

// down takes the listener down with a reason: the socket closed, every phone
// on it sent into its wait.
func (l *voiceListener) down(problem string, cluster bool) {
	l.closeSocket(true)

	l.lock.Lock()
	l.problem, l.cluster = problem, cluster
	l.lock.Unlock()
}

// shut closes the listener once the phones' connections have had a moment to
// close by themselves (their sessions ended, and told them why).
func (l *voiceListener) shut() {
	deadline := time.Now().Add(voiceTalkCloseWait)
	for l.peerCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	l.closeSocket(true)

	l.lock.Lock()
	l.problem, l.cluster = "", false
	l.lock.Unlock()
}

// closeSocket closes the socket and its mux. With gone, every phone still on
// it is gone first: its session waits for it to join again.
func (l *voiceListener) closeSocket(gone bool) {
	l.lock.Lock()
	conn, mux := l.conn, l.mux
	l.conn, l.mux, l.api, l.addresses = nil, nil, nil, nil
	peers := make([]*voicePhonePeer, 0, len(l.peers))
	for peer := range l.peers {
		peers = append(peers, peer)
	}
	l.lock.Unlock()

	if gone {
		for _, peer := range peers {
			peer.goneNow()
		}
	}

	if mux != nil {
		_ = mux.Close()
	}
	if conn != nil {
		_ = conn.Close()
	}
}

func (l *voiceListener) track(peer *voicePhonePeer) {
	l.lock.Lock()
	l.peers[peer] = struct{}{}
	l.lock.Unlock()
}

// peerCount is how many phones' connections are open on it.
func (l *voiceListener) peerCount() int {
	l.lock.Lock()
	defer l.lock.Unlock()

	return len(l.peers)
}

func (l *voiceListener) forget(peer *voicePhonePeer) {
	l.lock.Lock()
	delete(l.peers, peer)
	l.lock.Unlock()
}

// newVoiceAPI is pion as voice uses it: Opus alone, the audio level header
// extension, RTCP reports, on the one port, IPv4 only, no mDNS, loopback
// included, every candidate given as the admin's addresses, and none of the
// phone's taken that this server should not send to.
func newVoiceAPI(mux ice.UDPMux, addresses []string) (*webrtc.API, error) {
	media := &webrtc.MediaEngine{}
	if err := media.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: voiceOpusCapability, PayloadType: voiceOpusPayloadType}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}
	if err := media.RegisterHeaderExtension(webrtc.RTPHeaderExtensionCapability{URI: sdp.AudioLevelURI}, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}

	registry := &interceptor.Registry{}
	if err := webrtc.ConfigureRTCPReports(registry); err != nil {
		return nil, err
	}

	settings := webrtc.SettingEngine{LoggerFactory: voiceSilentLogs}
	settings.SetICEUDPMux(mux)
	settings.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	settings.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	settings.SetIncludeLoopbackCandidate(true)
	settings.SetRemoteIPFilter(voiceRemoteAllowed)
	if err := settings.SetICEAddressRewriteRules(webrtc.ICEAddressRewriteRule{
		External:        addresses,
		AsCandidateType: webrtc.ICECandidateTypeHost,
		Mode:            webrtc.ICEAddressRewriteReplace,
	}); err != nil {
		return nil, err
	}

	return webrtc.NewAPI(webrtc.WithMediaEngine(media), webrtc.WithInterceptorRegistry(registry), webrtc.WithSettingEngine(settings)), nil
}

// answer makes a phone's connection for a session from its offer, already
// checked by voiceOfferShape, and returns it with the answer, which carries
// every candidate. receive is how many receive-only sections the offer has.
func (l *voiceListener) answer(ctx context.Context, offer string, receive int, session *voiceSession) (*voicePhonePeer, string, error) {
	l.lock.Lock()
	api, cert := l.api, l.cert
	l.lock.Unlock()

	if api == nil || cert == nil {
		return nil, "", voiceUnavailableRefusal(voiceTalkBrokenText, 0)
	}

	pc, err := api.NewPeerConnection(webrtc.Configuration{Certificates: []webrtc.Certificate{*cert}})
	if err != nil {
		return nil, "", voiceUnavailableRefusal(voicePeerFailedText, 0)
	}

	peer := &voicePhonePeer{listener: l, pc: pc, safely: l.safely, session: session, closed: make(chan struct{})}
	l.track(peer)
	peer.wire()

	fail := func(refusal *voiceRefusal) (*voicePhonePeer, string, error) {
		peer.closeNow()
		return nil, "", refusal
	}

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer}); err != nil {
		return fail(&voiceRefusal{Status: http.StatusBadRequest, Code: codeBadRequest, Sentence: voiceBadOfferText})
	}

	// Each attaches to the next receive-only section, in the offer's order.
	for i := range min(voiceSlots, receive) {
		track, err := webrtc.NewTrackLocalStaticRTP(voiceOpusCapability, fmt.Sprintf("voice-%d", i), "voice")
		if err != nil {
			return fail(voiceUnavailableRefusal(voicePeerFailedText, 0))
		}
		sender, err := pc.AddTrack(track)
		if err != nil {
			return fail(voiceUnavailableRefusal(voicePeerFailedText, 0))
		}
		peer.tracks = append(peer.tracks, track)
		peer.senders = append(peer.senders, sender)
	}

	// The receive-only sections past the fourth are answered inactive.
	for _, transceiver := range pc.GetTransceivers() {
		if transceiver.Sender() == nil && transceiver.Direction() == webrtc.RTPTransceiverDirectionSendonly {
			if err := transceiver.Stop(); err != nil {
				return fail(voiceUnavailableRefusal(voicePeerFailedText, 0))
			}
		}
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return fail(&voiceRefusal{Status: http.StatusBadRequest, Code: codeBadRequest, Sentence: voiceBadOfferText})
	}

	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		return fail(voiceUnavailableRefusal(voicePeerFailedText, 0))
	}

	wait := time.NewTimer(voiceTalkGatherWait)
	defer wait.Stop()

	select {
	case <-gathered:
	case <-wait.C:
		return fail(voiceUnavailableRefusal(voicePeerFailedText, 0))
	case <-ctx.Done():
		return fail(voiceUnavailableRefusal(voicePeerFailedText, 0))
	}

	local := pc.LocalDescription()
	if local == nil {
		return fail(voiceUnavailableRefusal(voicePeerFailedText, 0))
	}

	for _, sender := range peer.senders {
		go l.safely("reading a phone's reports", func() { voiceDrainRTCP(sender) })
	}

	return peer, local.SDP, nil
}

// voiceDrainRTCP reads a sender's RTCP until it closes, so that its
// interceptors run.
func voiceDrainRTCP(sender *webrtc.RTPSender) {
	for {
		if _, _, err := sender.ReadRTCP(); err != nil {
			return
		}
	}
}

// ---------------------------------------------------------------- a phone

// voicePhonePeer is one phone's connection: its voice session's voicePeer.
type voicePhonePeer struct {
	listener *voiceListener
	pc       *webrtc.PeerConnection
	safely   func(where string, f func()) bool

	// Set while answering, before anything reads them.
	session *voiceSession
	tracks  []*webrtc.TrackLocalStaticRTP
	senders []*webrtc.RTPSender

	lock         sync.Mutex
	channel      *webrtc.DataChannel
	channels     int
	open         bool
	gone         bool
	disconnected *time.Timer

	closeOnce sync.Once

	// Closed once the connection is.
	closed chan struct{}
}

// wire sets the connection's callbacks, each in safely. None of them closes
// the connection: that is always done on a goroutine of its own.
func (p *voicePhonePeer) wire() {
	p.pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		p.safely("a phone's microphone", func() { p.readMicrophone(track, receiver) })
	})

	p.pc.OnDataChannel(func(channel *webrtc.DataChannel) {
		p.safely("a phone's data channel", func() {
			p.lock.Lock()
			p.channels++
			taken := p.channel == nil && channel.Label() == voiceDataChannelLabel
			if taken {
				p.channel = channel
			}
			p.lock.Unlock()

			if !taken {
				p.refuseChannel()
				return
			}

			channel.OnOpen(func() {
				p.safely("a phone's data channel opening", func() {
					p.lock.Lock()
					p.open = p.channel == channel
					p.lock.Unlock()

					go p.safely("greeting a phone", func() { p.session.greet(p) })
				})
			})
			// A phone closes the channel only by closing the connection
			// (it sends nothing on it), so a channel that closes is the
			// phone gone, at once rather than once its connection times
			// out.
			channel.OnClose(func() {
				p.safely("a phone's data channel closing", func() {
					p.lock.Lock()
					current := p.channel == channel
					if current {
						p.open = false
					}
					p.lock.Unlock()

					if current {
						p.goneNow()
					}
				})
			})
		})
	})

	p.pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		p.safely("a phone's connection state", func() {
			switch state {
			case webrtc.ICEConnectionStateDisconnected:
				p.lock.Lock()
				if p.disconnected == nil && !p.gone {
					p.disconnected = time.AfterFunc(voicePhoneDisconnectedWait, func() {
						p.safely("a phone gone quiet", p.goneNow)
					})
				}
				p.lock.Unlock()
			case webrtc.ICEConnectionStateConnected, webrtc.ICEConnectionStateCompleted, webrtc.ICEConnectionStateChecking:
				p.lock.Lock()
				if p.disconnected != nil {
					p.disconnected.Stop()
					p.disconnected = nil
				}
				p.lock.Unlock()
			case webrtc.ICEConnectionStateFailed, webrtc.ICEConnectionStateClosed:
				p.goneNow()
			}
		})
	})

	p.pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		p.safely("a phone's connection state", func() {
			if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
				p.goneNow()
			}
		})
	})
}

// refuseChannel ends a connection whose phone opened a data channel it should
// not have: a second one, or one not named "voice". Each costs pion a reader
// and its buffer, and a phone has no use for more than the one. It returns
// once the connection has closed, or after voicePhoneRefuseWait, and pion
// takes no further channel from the phone until it returns, so that no more
// than two are ever made.
func (p *voicePhonePeer) refuseChannel() {
	p.goneNow()
	go p.safely("closing a phone's voice connection", p.closeNow)

	wait := time.NewTimer(voicePhoneRefuseWait)
	defer wait.Stop()

	select {
	case <-p.closed:
	case <-wait.C:
	}
}

// readMicrophone reads the phone's microphone track, with each packet's
// audio level, and hands it to the session.
func (p *voicePhonePeer) readMicrophone(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
	if track.Kind() != webrtc.RTPCodecTypeAudio {
		return
	}

	go p.safely("reading a phone's reports", func() {
		for {
			if _, _, err := receiver.ReadRTCP(); err != nil {
				return
			}
		}
	})

	levelID := uint8(0)
	for _, extension := range receiver.GetParameters().HeaderExtensions {
		if extension.URI == sdp.AudioLevelURI && extension.ID > 0 && extension.ID < 256 {
			levelID = uint8(extension.ID)
		}
	}

	for {
		packet, _, err := track.ReadRTP()
		if err != nil {
			return
		}

		level := uint8(voiceLevelUnknown)
		if levelID != 0 {
			if raw := packet.GetExtension(levelID); raw != nil {
				var audio rtp.AudioLevelExtension
				if audio.Unmarshal(raw) == nil {
					level = audio.Level
				}
			}
		}

		p.session.fromPhone(p, packet.Timestamp, packet.Payload, level)
	}
}

// goneNow is the phone gone: its session starts waiting for it, once.
func (p *voicePhonePeer) goneNow() {
	p.lock.Lock()
	if p.gone {
		p.lock.Unlock()
		return
	}
	p.gone = true
	if p.disconnected != nil {
		p.disconnected.Stop()
		p.disconnected = nil
	}
	p.lock.Unlock()

	go p.safely("a phone's voice connection gone", func() { p.session.peerGone(p) })
}

func (p *voicePhonePeer) slots() int { return len(p.tracks) }

func (p *voicePhonePeer) isGone() bool {
	p.lock.Lock()
	defer p.lock.Unlock()

	return p.gone
}

func (p *voicePhonePeer) writeSlot(slot int, packet *rtp.Packet) error {
	if slot < 0 || slot >= len(p.tracks) {
		return errors.New("no such voice track")
	}

	return p.tracks[slot].WriteRTP(packet)
}

// signal sends one message on the data channel, or drops it while the
// channel is not open: the session greets the phone with its state again
// when it opens.
func (p *voicePhonePeer) signal(message any) {
	encoded, err := json.Marshal(message)
	if err != nil {
		return
	}

	p.lock.Lock()
	channel, open := p.channel, p.open
	p.lock.Unlock()

	if channel != nil && open {
		_ = channel.SendText(string(encoded))
	}
}

// close waits for the data channel to send what it holds, a second at most,
// then closes the connection. It is called on a goroutine of its own.
func (p *voicePhonePeer) close() {
	p.closeOnce.Do(func() {
		p.lock.Lock()
		channel, open := p.channel, p.open
		p.gone = true
		p.lock.Unlock()

		if channel != nil && open {
			deadline := time.Now().Add(voicePhoneDrainWait)
			for channel.BufferedAmount() > 0 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
		}

		p.shutdown()
	})
}

// closeNow closes a connection at once, with nothing to send first: one that
// never reached its session, or one its phone misused.
func (p *voicePhonePeer) closeNow() {
	p.closeOnce.Do(func() {
		p.lock.Lock()
		p.gone = true
		p.lock.Unlock()

		p.shutdown()
	})
}

func (p *voicePhonePeer) shutdown() {
	p.lock.Lock()
	if p.disconnected != nil {
		p.disconnected.Stop()
		p.disconnected = nil
	}
	p.lock.Unlock()

	_ = p.pc.Close()
	p.listener.forget(p)
	close(p.closed)
}
