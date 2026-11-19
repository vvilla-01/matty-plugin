package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// The clip recorder (spec §2.4.1–§2.4.4, §2.4.8; A§2.1): one receive-only
// WHEP session per recorded stream, opened by this server on Broadcast Box,
// whose media goes through the media core (clips_media.go, clips_ring.go) into
// a ring on disk that drafts are cut from.
//
// One recorder records one handle, from start to stop, and lives through any
// number of SESSIONS: each attempt to connect is a session, with its own
// PeerConnection and its own pipeline (two readers and a writer), so a
// reconnect starts from clean readers and a fresh epoch. When a session ends
// for a reason the stream may be over or broken — a watchdog fired, or the
// recorder is being stopped — what its epoch holds is frozen as a TAIL, which
// can still be clipped for ten minutes, and handed to whoever drives the
// recorder: "dropped" for a cut the stream carried on after, "ended" for one
// it may not have.
//
// The recorder decides nothing about which streams to record. The manager
// (the recorder manager in this package) does that and drives each recorder
// through start, update, setWindow, wipe and stop, and hears what it does
// through onEvent. Everything the manager needs to show is read back through
// state, clipFrom, reason, bitrate, heldSeconds and live.
//
// A stream key goes only where it must: into the WHEP request's
// Authorization header (exchangeWHEP), into a tail's captured stream (for
// redacting clip titles; never sent or logged), and out of every error text
// before it is kept or logged. Logs name the stream by its handle.

// The recorder's own numbers, beside the spec's in clips.go.
const (
	// Connecting (§2.4.2): gathering our candidates, the WHEP exchange, and
	// the wait for the connection to open once Broadcast Box has answered.
	recorderGatherWait  = 5 * time.Second
	recorderWHEPWait    = 15 * time.Second
	recorderConnectWait = 10 * time.Second
	// Looking up Broadcast Box's address (§2.4.1).
	recorderResolveWait = 5 * time.Second

	// The watchdogs (§2.4.4). noVideoWatchdog is in clips.go.
	recorderDisconnectedFor = 5 * time.Second
	recorderAudioDiedAfter  = 2 * time.Second
	// Video counts as flowing while its packets come no further apart than
	// this. A frame's packets arrive together, and frames come at least five
	// times a second, so anything longer is a stall.
	recorderVideoFlowGap = 500 * time.Millisecond
	// How often the watchdogs look, besides whenever something happens.
	recorderWatchEvery = time.Second

	// Backoff (§2.4.4). recorderBackoff is the wait before each attempt after
	// the first, in turn, staying on its last; it starts again once a session
	// has been ready this long.
	recorderBackoffReset = 2 * time.Minute
	// A stream that cannot be recorded is tried again this long after.
	recorderUnsupportedRetry = 5 * time.Minute

	// The admin reason after an attempt that panicked (recovery.go); the
	// stack is in the server log.
	recorderPanicReason = "the recorder stopped on an internal error"

	// Attempts in a row that never reached ready before the state says
	// failed.
	recorderFailedAfter = 2

	// A closed session's id stays ours this long, so a poll that still lists
	// it does not count it as a viewer (§2.4.2).
	recorderSessionLinger = 60 * time.Second
	// The most a recording problem is logged at Warn for one stream.
	recorderProblemLogEvery = time.Minute
	// What closing a session may take when the recorder decides to: the same
	// five seconds that stopping every recorder may (§2.14).
	recorderCloseWait = 5 * time.Second
)

// errRecorderPanicked is a reader that panicked: the session ends as one
// whose stream cannot be recorded (readSafely).
var errRecorderPanicked = errors.New(recorderPanicReason)

var recorderBackoff = []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second, time.Minute, 5 * time.Minute}

// recorderBackoffAfter is the wait before the next attempt, step being how
// many waits have gone before it since the last reset.
func recorderBackoffAfter(step int) time.Duration {
	return recorderBackoff[min(max(step, 0), len(recorderBackoff)-1)]
}

// errNoIPv4 is the admin reason when Broadcast Box's address has never been
// found.
var errNoIPv4 = errors.New("Broadcast Box URL resolves to no IPv4 address")

// errBroadcastBoxLoopback is the admin reason when the Broadcast Box URL
// names this machine's loopback address (localhost, 127.0.0.1, or a name
// /etc/hosts maps to 127.0.1.1), which is natural when Broadcast Box runs
// beside Mattermost, and Broadcast Box offers no loopback address to receive
// media from, which by default it does not (pion leaves loopback out unless
// INCLUDE_LOOPBACK_CANDIDATE is set). Media may only come from the URL's
// addresses (§2.4.1), so such a session can never open.
var errBroadcastBoxLoopback = errors.New("the Broadcast Box URL is a loopback address, and Broadcast Box offers no loopback address to send media from: " +
	"set the Broadcast Box URL to its LAN address, the one its ICE candidates use")

// The payload types the recorder offers. H.264 is offered exactly as
// Broadcast Box v2.0.2 registers it (internal/webrtc/codecs/codecs.go, PT 96);
// Broadcast Box binds one payload type per codec family by MIME type, so one
// H.264 entry is enough. AV1, VP9 and H.265 are offered only so that a
// publisher using one of them is answered with it, and the recorder can say
// "unsupported" rather than receive nothing and blame the network.
const (
	recorderH264PT = 96
	recorderAV1PT  = 45
	recorderVP9PT  = 98
	recorderH265PT = 113
	recorderOpusPT = 111

	recorderH264Fmtp = "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"
	recorderOpusFmtp = "minptime=10;useinbandfec=1"
)

// newClipMediaEngine is the recorder's codecs: H.264 and Opus to record, and
// the three it can only recognise. NACK and NACK PLI are advertised because
// Broadcast Box's H.264 entry carries them; the recorder never sends a PLI.
func newClipMediaEngine() (*webrtc.MediaEngine, error) {
	m := &webrtc.MediaEngine{}
	feedback := []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}}

	video := []webrtc.RTPCodecParameters{
		{PayloadType: recorderH264PT, RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeH264, ClockRate: videoClockRate, SDPFmtpLine: recorderH264Fmtp, RTCPFeedback: feedback,
		}},
		{PayloadType: recorderAV1PT, RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeAV1, ClockRate: videoClockRate, RTCPFeedback: feedback,
		}},
		{PayloadType: recorderVP9PT, RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeVP9, ClockRate: videoClockRate, SDPFmtpLine: "profile-id=0", RTCPFeedback: feedback,
		}},
		{PayloadType: recorderH265PT, RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeH265, ClockRate: videoClockRate,
			SDPFmtpLine: "level-id=93;profile-id=1;tier-flag=0;tx-mode=SRST", RTCPFeedback: feedback,
		}},
	}
	for _, codec := range video {
		if err := m.RegisterCodec(codec, webrtc.RTPCodecTypeVideo); err != nil {
			return nil, err
		}
	}

	opus := webrtc.RTPCodecParameters{PayloadType: recorderOpusPT, RTPCodecCapability: webrtc.RTPCodecCapability{
		MimeType: webrtc.MimeTypeOpus, ClockRate: audioClockRate, Channels: 2, SDPFmtpLine: recorderOpusFmtp,
	}}
	if err := m.RegisterCodec(opus, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}

	return m, nil
}

// newClipWebRTCAPI builds the pion API every recorder session is made from,
// for one set of Broadcast Box's IPv4 addresses:
//
//   - interceptors for NACK and RTCP reports only: Broadcast Box answers NACKs
//     and wants receiver reports. No TWCC, and nothing that sends PLI or FIR:
//     OBS ignores them anyway, and Broadcast Box would forward every one to
//     the publisher with no rate limit;
//   - UDP over IPv4 only, with mDNS off;
//   - no remote candidate but Broadcast Box's own addresses, so media never
//     goes to its NAT_1_TO_1_IP public address or anywhere else, only to the
//     host the plugin already talks to. With no ICE servers, nothing else is
//     ever contacted.
//
// loopback lets a session use 127.0.0.1, which only the end-to-end tests
// need; nothing outside a test sets it.
func newClipWebRTCAPI(allowed []string, loopback bool) (*webrtc.API, error) {
	m, err := newClipMediaEngine()
	if err != nil {
		return nil, err
	}

	registry := &interceptor.Registry{}
	if err := webrtc.ConfigureNack(m, registry); err != nil {
		return nil, err
	}
	if err := webrtc.ConfigureRTCPReports(registry); err != nil {
		return nil, err
	}

	settings := webrtc.SettingEngine{}
	settings.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	settings.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	settings.SetRemoteIPFilter(remoteIPFilter(allowed))
	if loopback {
		settings.SetIncludeLoopbackCandidate(true)
	}

	return webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(registry), webrtc.WithSettingEngine(settings)), nil
}

// remoteIPFilter keeps a remote candidate only when its address is one of
// allowed, IPv4 written the usual way. An IPv4 address written as IPv6
// (::ffff:a.b.c.d) is the same address.
func remoteIPFilter(allowed []string) func(net.IP) bool {
	set := make(map[string]bool, len(allowed))
	for _, address := range allowed {
		set[address] = true
	}

	return func(ip net.IP) bool {
		v4 := ip.To4()
		return v4 != nil && set[v4.String()]
	}
}

// clipPeerAPI is the pion API the recorders' sessions are made from, built
// for Broadcast Box's IPv4 addresses (§2.4.1). One is shared by every
// recorder.
//
// The addresses are looked up at the start of every attempt, so a renumbered
// Broadcast Box is followed. A lookup that fails or finds nothing keeps the
// last good set for the same host: a resolver that hiccups must never stop a
// healthy stream from being recorded. A set that changes builds a new API for
// the attempts that follow; sessions already made keep the one they were made
// with, and are left alone.
type clipPeerAPI struct {
	mu      sync.Mutex
	host    string   // the host the API was built for
	allowed []string // its IPv4 addresses, sorted: the last good set
	api     *webrtc.API

	// lookup is net.DefaultResolver.LookupIP; build makes the API for a set.
	// Tests replace both: build to let loopback in, lookup to fail on cue.
	lookup func(ctx context.Context, network, host string) ([]net.IP, error)
	build  func(allowed []string) (*webrtc.API, error)
}

func newClipPeerAPI() *clipPeerAPI {
	return &clipPeerAPI{
		lookup: net.DefaultResolver.LookupIP,
		build:  func(allowed []string) (*webrtc.API, error) { return newClipWebRTCAPI(allowed, false) },
	}
}

// forAttempt is the API a new session is made with, for Broadcast Box at
// broadcastBoxURL, and the addresses it lets media come from.
func (a *clipPeerAPI) forAttempt(ctx context.Context, broadcastBoxURL string) (*webrtc.API, []string, error) {
	host := ""
	if parsed, err := url.Parse(broadcastBoxURL); err == nil {
		host = parsed.Hostname()
	}
	if host == "" {
		return nil, nil, errNoIPv4
	}

	found, lookupErr := a.resolve(ctx, host)

	a.mu.Lock()
	defer a.mu.Unlock()

	if len(found) == 0 {
		if host == a.host && a.api != nil {
			return a.api, a.allowed, nil
		}
		if lookupErr != nil {
			return nil, nil, fmt.Errorf("%w (%s)", errNoIPv4, lookupErr.Error())
		}
		return nil, nil, errNoIPv4
	}

	if host == a.host && a.api != nil && slices.Equal(found, a.allowed) {
		return a.api, a.allowed, nil
	}

	api, err := a.build(found)
	if err != nil {
		return nil, nil, fmt.Errorf("could not set up WebRTC: %w", err)
	}
	a.host, a.allowed, a.api = host, found, api

	return api, found, nil
}

// resolve is host's IPv4 addresses, sorted and without repeats. An address
// written as one is used as it is, and an IPv6 address has none.
func (a *clipPeerAPI) resolve(ctx context.Context, host string) ([]string, error) {
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return []string{v4.String()}, nil
		}
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(ctx, recorderResolveWait)
	defer cancel()

	ips, err := a.lookup(ctx, "ip4", host)
	var found []string
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			found = append(found, v4.String())
		}
	}
	slices.Sort(found)

	return slices.Compact(found), err
}

// clipOwnSessions are the playback sessions the recorders have opened on
// Broadcast Box, which its status lists beside everybody else's. The manager
// leaves them out of VillaNET's viewer counts and watchers (§2.4.7). One set
// is shared by every recorder, because a closed session must stay in it for
// a minute after its recorder may be gone.
type clipOwnSessions struct {
	mu  sync.Mutex
	ids map[string]time.Time // session id -> when it stops being ours; zero while open
}

// open adds a session the moment Broadcast Box names it, before media flows.
func (o *clipOwnSessions) open(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.ids == nil {
		o.ids = map[string]time.Time{}
	}
	o.ids[id] = time.Time{}
}

// closed keeps a closed session ours for recorderSessionLinger, and forgets
// the ones whose minute is up.
func (o *clipOwnSessions) closed(id string, now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.ids == nil {
		o.ids = map[string]time.Time{}
	}
	o.ids[id] = now.Add(recorderSessionLinger)
	o.pruneLocked(now)
}

// has says whether a session Broadcast Box lists is one of ours.
func (o *clipOwnSessions) has(id string, now time.Time) bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	until, ok := o.ids[id]
	return ok && (until.IsZero() || now.Before(until))
}

func (o *clipOwnSessions) prune(now time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.pruneLocked(now)
}

func (o *clipOwnSessions) pruneLocked(now time.Time) {
	for id, until := range o.ids {
		if !until.IsZero() && !now.Before(until) {
			delete(o.ids, id)
		}
	}
}

// recorderClock is the recorder's time: when things happened, and how it
// waits between attempts and between looks by the watchdogs. Tests put a
// clock behind it that moves only when they say.
type recorderClock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realRecorderClock struct{}

func (realRecorderClock) Now() time.Time                         { return time.Now() }
func (realRecorderClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// recorderLog is the part of the plugin API the recorder logs through.
type recorderLog interface {
	LogError(msg string, keyValuePairs ...any)
	LogInfo(msg string, keyValuePairs ...any)
	LogWarn(msg string, keyValuePairs ...any)
	LogDebug(msg string, keyValuePairs ...any)
}

// recorderPeer is a session's connection to Broadcast Box: a pion
// PeerConnection, or a test's stand-in.
type recorderPeer interface {
	close() error
}

type pionRecorderPeer struct {
	pc *webrtc.PeerConnection
}

func (p pionRecorderPeer) close() error { return p.pc.Close() }

// What the recorder tells the manager.
type recorderEventKind uint8

const (
	// The state or clipFrom changed.
	recorderChanged recorderEventKind = iota + 1
	// A tail was handed over (tail is set). It is the manager's from here:
	// to keep under its bounds and delete when it is done with it. A tail
	// handed over just as a wipe of the stream lands may already be deleted
	// (ring.isDeleted), and is then simply gone.
	recorderTailKept
)

type recorderEvent struct {
	kind     recorderEventKind
	handle   string
	state    string
	clipFrom time.Time // zero unless ready
	tail     *clipTail // recorderTailKept
}

// recorderConfig is what a recorder is made with.
type recorderConfig struct {
	handle string
	dir    string // <root>/ring/<handle>; each epoch is a directory in it

	peers *clipPeerAPI
	own   *clipOwnSessions

	// whep is exchangeWHEP: the offer to Broadcast Box, its answer and the
	// session id it opened.
	whep func(ctx context.Context, cfg *configuration, streamKey, offer string) (answer, sessionID string, err error)
	log  recorderLog

	// onEvent hears every change of state and every tail. It is called
	// from the recorder's goroutines, one call at a time and never under the
	// recorder's lock, and never again once stop has returned. It may read
	// the recorder (state, clipFrom, reason, live and the rest), but must
	// not call wipe or stop, which wait for those calls; and stop waits for
	// the goroutines that make them, so onEvent must not take a lock that
	// whoever calls stop holds while it does.
	onEvent func(recorderEvent)

	// Tests replace these: the clock, the whole of connecting (a session
	// with no PeerConnection), and the ring's files.
	clock       recorderClock
	connect     func(ctx context.Context, s *recorderSession) error
	openSegment func(path string) (segmentFile, error)
}

// recorderSession is one attempt to record: its connection, its pipeline,
// and what the watchdogs judge it by.
type recorderSession struct {
	pipe *clipPipeline
	wake chan struct{} // something the watchdogs should look at happened

	// Arrivals (Unix ns) of the newest video packet, and of the first
	// packet since video last stopped for recorderVideoFlowGap. Written
	// only by the video track's goroutine.
	lastVideo atomic.Int64
	flowSince atomic.Int64

	// Set when the recorder lets go of the session (takeSession), under
	// the recorder's mu: the wipes there had been then, so that a tail its
	// writer still hands over is deleted if a wipe has come since.
	taken      bool
	takenWipes uint64

	mu          sync.Mutex
	closed      bool // closing has begun: no more readers start
	peer        recorderPeer
	sessionID   string
	conn        webrtc.PeerConnectionState
	connAt      time.Time // when conn last changed
	connectedAt time.Time
	readyAt     time.Time // when this session's first epoch became ready
	unsupported error
	h264        map[uint8]bool // the payload types negotiated as H.264
	readers     sync.WaitGroup // track and RTCP goroutines
}

func (s *recorderSession) nudge() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// setPeer gives the session its connection, unless closing has already
// begun, when the connection is closed at once and the attempt should give
// up: nobody else would close it.
func (s *recorderSession) setPeer(peer recorderPeer) bool {
	s.mu.Lock()
	closed := s.closed
	if !closed {
		s.peer = peer
	}
	s.mu.Unlock()

	if closed {
		_ = peer.close()
	}
	return !closed
}

// addReader counts a goroutine in, unless closing has begun.
func (s *recorderSession) addReader() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return false
	}
	s.readers.Add(1)
	return true
}

// isH264 is the video reader's check of each packet's payload type. Before
// the track says which types are H.264 every one is taken to be.
func (s *recorderSession) isH264(pt uint8) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.h264) == 0 || s.h264[pt]
}

// noteVideoPacket is the video track's goroutine noting a packet's arrival.
func (s *recorderSession) noteVideoPacket(arrival int64) {
	if last := s.lastVideo.Load(); last == 0 || arrival-last > int64(recorderVideoFlowGap) {
		s.flowSince.Store(arrival)
	}
	s.lastVideo.Store(arrival)
}

func (s *recorderSession) connState() webrtc.PeerConnectionState {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.conn
}

// clipRecorder records one stream: see the top of this file.
type clipRecorder struct {
	handle      string
	dir         string
	peers       *clipPeerAPI
	own         *clipOwnSessions
	whep        func(ctx context.Context, cfg *configuration, streamKey, offer string) (string, string, error)
	log         recorderLog
	onEvent     func(recorderEvent)
	clock       recorderClock
	connect     func(ctx context.Context, s *recorderSession) error
	openSegment func(path string) (segmentFile, error)

	done chan struct{} // closed when run returns

	// eventMu is held while an event is worked out and delivered, so that
	// events arrive in order, and so that stop can promise none comes after
	// it returns. Taken before mu, never under it.
	eventMu  sync.Mutex
	silent   bool
	lastView recorderView

	mu sync.Mutex

	// The stream as the manager last said it is, its settings, and the
	// Clip length. The key in stream is never logged or sent.
	stream   apiStream
	cfg      *configuration
	window   time.Duration
	pausedAt time.Time // the last time the stream was seen paused

	cancel  context.CancelFunc
	stopped bool // stop has begun: no session starts from here on

	session *recorderSession // the one being attempted or recorded, if any
	wipes   uint64           // wipes so far: a tail made across one is not kept

	// What the panel and the admins are told.
	ready       bool      // the current session's epoch is in the ring
	clipFromAt  time.Time // its start, while ready
	failures    int       // attempts in a row that ended without ready
	step        int       // backoff steps taken since the last reset
	wait        time.Duration
	unsupported string // why the stream cannot be recorded; kept until ready
	reasonText  string // the latest admin reason

	warnedNoID     bool
	lastFailLogged string
	lastProblemLog time.Time
}

type recorderView struct {
	state    string
	clipFrom int64
}

func newClipRecorder(rc recorderConfig) *clipRecorder {
	r := &clipRecorder{
		handle:      rc.handle,
		dir:         rc.dir,
		peers:       rc.peers,
		own:         rc.own,
		whep:        rc.whep,
		log:         rc.log,
		onEvent:     rc.onEvent,
		clock:       rc.clock,
		connect:     rc.connect,
		openSegment: rc.openSegment,
		done:        make(chan struct{}),
		lastView:    recorderView{state: clipStateStarting},
	}
	if r.clock == nil {
		r.clock = realRecorderClock{}
	}
	if r.connect == nil {
		r.connect = r.connectWHEP
	}
	if r.own == nil {
		r.own = &clipOwnSessions{}
	}
	if r.peers == nil {
		r.peers = newClipPeerAPI()
	}

	return r
}

// newClipRecorder for this plugin: Broadcast Box through exchangeWHEP, and
// the plugin's log.
func (p *Plugin) newClipRecorder(handle, dir string, peers *clipPeerAPI, own *clipOwnSessions, onEvent func(recorderEvent)) *clipRecorder {
	return newClipRecorder(recorderConfig{
		handle:  handle,
		dir:     dir,
		peers:   peers,
		own:     own,
		whep:    p.exchangeWHEP,
		log:     p.API,
		onEvent: onEvent,
	})
}

// start begins recording stream, under ctx, with the settings in cfg. The
// first attempt is made at once. A recorder starts once; a second start does
// nothing.
func (r *clipRecorder) start(ctx context.Context, stream apiStream, cfg *configuration) {
	now := r.clock.Now()

	// The lock is let go by a defer, since what is done under it calls out:
	// a panic here must not leave it held for the stop that comes after
	// (recovery.go).
	ctx, started := func() (context.Context, bool) {
		r.mu.Lock()
		defer r.mu.Unlock()

		if r.cancel != nil || r.stopped {
			return nil, false
		}
		r.stream, r.cfg, r.window = stream, cfg, cfg.ringWindow()
		if stream.Paused {
			r.pausedAt = now
		}
		ctx, cancel := context.WithCancel(ctx)
		r.cancel = cancel
		return ctx, true
	}()
	if started {
		go r.run(ctx)
	}
}

// update is the stream as the latest poll has it: whether it is paused, how
// many audio tracks Broadcast Box says it has, its bitrate, and the name,
// note and tag a tail made from here on keeps.
func (r *clipRecorder) update(stream apiStream) {
	now := r.clock.Now()

	r.mu.Lock()
	key := r.stream.StreamKey
	r.stream = stream
	if r.stream.StreamKey == "" {
		r.stream.StreamKey = key
	}
	if stream.Paused {
		r.pausedAt = now
	}
	s := r.session
	r.mu.Unlock()

	if s != nil {
		s.pipe.writer.setReportedBitrate(stream.Bitrate)
	}
}

// setWindow changes the Clip length. The ring being recorded takes it at its
// next segment; sessions that follow start with it.
func (r *clipRecorder) setWindow(window time.Duration) {
	r.mu.Lock()
	r.window = window
	s := r.session
	r.mu.Unlock()

	if s != nil {
		s.pipe.writer.setWindow(window)
	}
}

// wipe deletes what is held of the live recording at once, for a streamer's
// or an admin's wipe (§2.10.6); recording carries on from the next IDR. A
// tail being made as it lands is deleted rather than handed over. Tails
// already handed over are the manager's to delete.
func (r *clipRecorder) wipe() {
	r.mu.Lock()
	r.wipes++
	s := r.session
	r.ready, r.clipFromAt = false, time.Time{}
	r.mu.Unlock()

	if s != nil {
		s.pipe.wipe()
	}
	r.changed()
}

// stop ends the recording: the run loop and the session are closed, waiting
// for their goroutines as long as ctx allows. With keep, what the last epoch
// holds is returned as a tail with reason "ended", when it holds at least
// tailMinHeld; without, it is deleted. No event is delivered once stop has
// returned.
func (r *clipRecorder) stop(ctx context.Context, keep bool) (clipTail, bool) {
	r.mu.Lock()
	r.stopped = true
	cancel := r.cancel
	r.mu.Unlock()

	if cancel != nil {
		cancel()
		select {
		case <-r.done:
		case <-ctx.Done():
		}
	}

	var tail clipTail
	held := false

	r.mu.Lock()
	s := r.session
	r.mu.Unlock()
	if wipes, ok := r.takeSession(s); ok {
		if frozen := r.closeSession(ctx, s, keep); frozen != nil {
			tail, held = r.tailOf(frozen, clipTailEnded, wipes)
		}
	}

	r.eventMu.Lock()
	r.silent = true
	r.eventMu.Unlock()

	// Why it stopped is the manager's to log; what it kept is said here.
	if held {
		r.log.LogInfo("A clip recording's end is held", "stream_handle", r.handle, "reason", clipTailEnded,
			"held_seconds", tail.ring.heldMs()/1000)
	}

	return tail, held
}

// state is what the panel is told: starting, ready, failed or unsupported.
// The manager's own states (queued, full, off) are its to add.
func (r *clipRecorder) state() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.stateLocked()
}

func (r *clipRecorder) stateLocked() string {
	switch {
	case r.unsupported != "":
		return clipStateUnsupported
	case r.failures >= recorderFailedAfter:
		return clipStateFailed
	case r.ready:
		return clipStateReady
	default:
		return clipStateStarting
	}
}

// clipFrom is when the epoch being recorded began, while ready: the
// snapshot's clipFrom. Zero otherwise.
func (r *clipRecorder) clipFrom() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.clipFromLocked()
}

func (r *clipRecorder) clipFromLocked() time.Time {
	if r.stateLocked() != clipStateReady {
		return time.Time{}
	}
	return r.clipFromAt
}

// reason is the admin reason for the state: why it cannot be recorded, why
// the last attempt failed, why the last epoch ended, or, while ready, why
// clips are shorter than the Clip length. Never a stream key.
func (r *clipRecorder) reason() string {
	r.mu.Lock()
	state := r.stateLocked()
	text, unsupported := r.reasonText, r.unsupported
	s := r.session
	r.mu.Unlock()

	switch state {
	case clipStateUnsupported:
		return unsupported
	case clipStateReady:
		if s == nil {
			return ""
		}
		status := s.pipe.writer.current().status()
		if status.truncation != "" && status.truncatedUntil > r.clock.Now().UnixNano() {
			return status.truncation
		}
		return ""
	}
	return text
}

// live is what clipRecordings.live answers for this stream: its state and,
// while ready, the ring to cut from.
func (r *clipRecorder) live() clipLive {
	r.mu.Lock()
	state := r.stateLocked()
	s := r.session
	r.mu.Unlock()

	live := clipLive{state: state, bytesPerSecond: r.bitrate()}
	if state == clipStateReady && s != nil {
		live.ring = s.pipe.writer.current()
	}
	return live
}

// bitrate is the stream's measured rate in bytes per second, or Broadcast
// Box's report of it until ten seconds have been measured.
func (r *clipRecorder) bitrate() float64 {
	r.mu.Lock()
	s := r.session
	reported := float64(max(r.stream.Bitrate, 0)) / 8
	r.mu.Unlock()

	if s != nil {
		if measured := s.pipe.writer.current().status().measuredBps; measured > 0 {
			return measured
		}
	}
	return reported
}

// heldSeconds is how much the ring holds, while ready.
func (r *clipRecorder) heldSeconds() int64 {
	r.mu.Lock()
	ready := r.stateLocked() == clipStateReady
	s := r.session
	r.mu.Unlock()

	if !ready || s == nil {
		return 0
	}
	return s.pipe.writer.current().heldMs() / 1000
}

// run makes attempts, watches each session that connects, and waits between
// them, until ctx ends. When stop ended it, the session it was in is left for
// stop to close, with or without a tail as stop was asked; when ctx ended
// some other way, it closes the session itself and keeps nothing.
func (r *clipRecorder) run(ctx context.Context) {
	defer close(r.done)
	defer func() {
		r.mu.Lock()
		stopping, s := r.stopped, r.session
		r.mu.Unlock()
		if stopping {
			return
		}
		if _, ok := r.takeSession(s); ok {
			closeCtx, cancel := context.WithTimeout(context.Background(), recorderCloseWait)
			defer cancel()
			// Outside the passes, so run safely of its own: a panic closing
			// the session is logged and ends nothing else, and done is still
			// closed for a stop waiting on it (recovery.go).
			safely(r.log, "a clip recorder, closing its session", func() { r.closeSession(closeCtx, s, false) })
		}
	}()

	// A pass that panics is logged, its session closed with nothing kept, and
	// the next attempt made after a pause, as after a failed one
	// (recovery.go). The recorder's locks are let go by a defer or held only
	// around plain assignments, so none is still held when the next pass
	// takes it.
	for {
		stop := false
		if safely(r.log, "a clip recorder", func() { stop = r.pass(ctx) }) {
			stop = r.afterPanic()
		}
		if stop {
			return
		}
	}
}

// pass is one turn of run: the wait, an attempt, and the session it opens
// watched to its end. It is whether run is to stop.
func (r *clipRecorder) pass(ctx context.Context) bool {
	r.mu.Lock()
	wait := r.wait
	r.mu.Unlock()
	if wait > 0 {
		select {
		case <-ctx.Done():
			return true
		case <-r.clock.After(wait):
		}
	}

	s := r.newSession()
	if s == nil || ctx.Err() != nil {
		return true
	}

	if err := r.connect(ctx, s); err != nil {
		// Nothing was recorded: media flows only once connected.
		if _, ok := r.takeSession(s); ok {
			closeCtx, cancel := context.WithTimeout(context.Background(), recorderCloseWait)
			r.closeSession(closeCtx, s, false)
			cancel()
		}
		if ctx.Err() != nil {
			return true
		}
		r.attemptFailed(err)
		return false
	}

	s.mu.Lock()
	if s.connectedAt.IsZero() {
		s.connectedAt = r.clock.Now()
	}
	s.mu.Unlock()

	end, stopped := r.watch(ctx, s)
	if stopped {
		return true
	}
	r.endSession(s, end)
	return false
}

// afterPanic is what follows a pass that panicked: the session it was in is
// closed with nothing kept, unless stop is ending the recorder, which closes
// it as it was asked to; and the next attempt waits loopPanicPause. It is
// whether run is to stop.
func (r *clipRecorder) afterPanic() bool {
	r.mu.Lock()
	stopping, s := r.stopped, r.session
	if !stopping {
		r.wait, r.reasonText = loopPanicPause, recorderPanicReason
	}
	r.mu.Unlock()
	if stopping {
		return true
	}

	if _, ok := r.takeSession(s); ok {
		closeCtx, cancel := context.WithTimeout(context.Background(), recorderCloseWait)
		r.closeSession(closeCtx, s, false)
		cancel()
	}
	r.changed()

	return false
}

// newSession makes the pipeline for an attempt, and makes it the current
// session; nil once stop has begun.
func (r *clipRecorder) newSession() *recorderSession {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.stopped {
		return nil
	}

	s := &recorderSession{wake: make(chan struct{}, 1)}
	s.pipe = newClipPipeline(pipelineConfig{
		dir:         r.dir,
		handle:      r.handle,
		window:      r.window,
		reportedBps: r.stream.Bitrate,
		event:       func(event pipelineEvent) { r.onPipeline(s, event) },
		isH264:      s.isH264,
		openSegment: r.openSegment,
		log:         r.log,
	})
	// The writer stops when the session is closed (finish), never because
	// the recorder is stopping: stop wants what is queued applied first.
	s.pipe.writer.start(context.Background())
	r.session = s

	return s
}

// takeSession makes s no longer the current session, if it still is, and
// says how many wipes there had been. Whoever takes a session closes it.
func (r *clipRecorder) takeSession(s *recorderSession) (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if s == nil || r.session != s {
		return 0, false
	}
	r.session = nil
	r.ready, r.clipFromAt = false, time.Time{}
	s.taken, s.takenWipes = true, r.wipes

	return r.wipes, true
}

// closeSession closes s's connection, waits for its goroutines and its
// writer as long as ctx allows, and returns what its epoch holds as a frozen
// ring, with keep, when it holds enough to be a tail.
func (r *clipRecorder) closeSession(ctx context.Context, s *recorderSession, keep bool) *ring {
	s.mu.Lock()
	s.closed = true
	peer, id := s.peer, s.sessionID
	s.mu.Unlock()

	// Closing ends Broadcast Box's session at once: it has no WHEP DELETE,
	// and closing ICE is what it notices. Closing also ends every ReadRTP,
	// which is what lets the readers return.
	waitFor(ctx, r.log, "closing a clip recorder's connection", func() {
		if peer == nil {
			return
		}
		if err := peer.close(); err != nil {
			r.log.LogDebug("A clip recorder's connection did not close cleanly", "stream_handle", r.handle, "error", r.scrub(err.Error()))
		}
	})
	if id != "" {
		r.own.closed(id, r.clock.Now())
	}
	waitFor(ctx, r.log, "waiting for a clip recorder's readers", s.readers.Wait)

	return s.pipe.writer.finish(ctx, keep)
}

// waitFor runs do and waits for it as long as ctx allows; what is left of it
// carries on alone. A panic in do is logged under where and ends do alone,
// and the defer lets the waiting go on as if do had returned (recovery.go).
func waitFor(ctx context.Context, log panicLog, where string, do func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		safely(log, where, do)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// tailOf makes a frozen ring into a tail, unless a wipe has come since the
// session was taken (wipes is the count then), in which case it is deleted.
func (r *clipRecorder) tailOf(frozen *ring, reason string, wipes uint64) (clipTail, bool) {
	r.mu.Lock()
	wiped := r.wipes != wipes
	stream := r.tailStreamLocked()
	r.mu.Unlock()

	if wiped || frozen.isDeleted() {
		frozen.remove()
		return clipTail{}, false
	}
	return clipTail{ring: frozen, until: frozen.endTime().Add(tailKeep), stream: stream, reason: reason}, true
}

// tailStreamLocked is the stream as a tail keeps it (§2.4.8, A§4.14): its
// key, for redacting clip titles only, and the name, attribution, note and
// tag it had.
func (r *clipRecorder) tailStreamLocked() apiStream {
	s := r.stream
	return apiStream{
		Handle:      r.handle,
		StreamKey:   s.StreamKey,
		Title:       s.Title,
		MOTD:        s.MOTD,
		UserID:      s.UserID,
		Username:    s.Username,
		DisplayName: s.DisplayName,
		LinkSource:  s.LinkSource,
		NowPlaying:  s.NowPlaying,
	}
}

// handOver gives the manager a tail, or deletes it when nobody will have it.
//
// A wipe can land between tailOf, which looks at the wipes once, and the
// delivery: it finds no session to cut, since this one is already taken, and
// no tail of the stream held by the manager yet. So the wipes are looked at
// again once the tail is delivered, and a tail made of what was recorded
// before a wipe is deleted, wherever it got to; the manager treats a deleted
// tail as gone. A wipe that comes after this look finds the tail held, and
// the manager's wipe deletes it there.
func (r *clipRecorder) handOver(frozen *ring, reason string, wipes uint64) {
	tail, ok := r.tailOf(frozen, reason, wipes)
	if !ok {
		return
	}

	held := frozen.heldMs() / 1000
	delivered := r.deliverTail(tail)

	r.mu.Lock()
	wiped := r.wipes != wipes
	r.mu.Unlock()

	if !delivered || wiped {
		frozen.remove()
		return
	}
	r.log.LogInfo("A clip recording's end is held", "stream_handle", r.handle, "reason", reason, "held_seconds", held)
}

func (r *clipRecorder) deliverTail(tail clipTail) bool {
	r.eventMu.Lock()
	defer r.eventMu.Unlock()

	if r.silent || r.onEvent == nil {
		return false
	}
	r.onEvent(recorderEvent{kind: recorderTailKept, handle: r.handle, tail: &tail})
	return true
}

// changed tells the manager the state or clipFrom moved, when they have.
func (r *clipRecorder) changed() {
	r.eventMu.Lock()
	defer r.eventMu.Unlock()

	r.mu.Lock()
	view := recorderView{state: r.stateLocked()}
	if from := r.clipFromLocked(); !from.IsZero() {
		view.clipFrom = from.UnixNano()
	}
	same := view == r.lastView
	r.lastView = view
	r.mu.Unlock()

	if same || r.silent || r.onEvent == nil {
		return
	}
	event := recorderEvent{kind: recorderChanged, handle: r.handle, state: view.state}
	if view.clipFrom != 0 {
		event.clipFrom = time.Unix(0, view.clipFrom)
	}
	r.onEvent(event)
}

// scrub takes the stream key out of a text that is about to be kept or
// logged: an error from Broadcast Box could echo it.
func (r *clipRecorder) scrub(text string) string {
	r.mu.Lock()
	key := r.stream.StreamKey
	r.mu.Unlock()

	return redactStreamKey(text, key, "the stream")
}

// attemptFailed is an attempt that never connected: the next waits its turn,
// and two in a row without ready are "failed". Logged once per change: the
// first failure, the one that makes it failed, and any whose reason differs.
func (r *clipRecorder) attemptFailed(err error) {
	reason := r.scrub(err.Error())

	r.mu.Lock()
	r.failures++
	r.wait = recorderBackoffAfter(r.step)
	r.step = min(r.step+1, len(recorderBackoff)-1)
	r.reasonText = reason
	failures, wait := r.failures, r.wait
	logIt := failures == 1 || failures == recorderFailedAfter || reason != r.lastFailLogged
	r.lastFailLogged = reason
	r.mu.Unlock()

	if logIt {
		r.log.LogWarn("A clip recorder could not connect to Broadcast Box", "stream_handle", r.handle,
			"attempts", failures, "retry_in", wait.String(), "error", reason)
	}
	r.changed()
}

// recorderEnd is why a connected session ends.
type recorderEnd struct {
	reason      string
	tail        string // what a tail of it is: clipTailDropped or clipTailEnded
	unsupported bool   // the stream cannot be recorded; try again much later
}

// watch looks after a connected session until a watchdog ends it, or ctx
// does (stopped).
func (r *clipRecorder) watch(ctx context.Context, s *recorderSession) (recorderEnd, bool) {
	// One tick is pending at a time, asked for only after a look that let
	// the session carry on.
	var tick <-chan time.Time
	for {
		if end, over := judgeRecorder(r.factsOf(s), r.clock.Now()); over {
			return end, false
		}
		if tick == nil {
			tick = r.clock.After(recorderWatchEvery)
		}

		select {
		case <-ctx.Done():
			return recorderEnd{}, true
		case <-s.wake:
		case <-tick:
			tick = nil
		}
	}
}

// recorderFacts are what the watchdogs judge a connected session by.
type recorderFacts struct {
	conn        webrtc.PeerConnectionState
	connAt      time.Time // when conn last changed
	connectedAt time.Time
	unsupported error

	lastVideo time.Time // zero: none yet
	flowSince time.Time // video has come without a stall since
	lastAudio time.Time // the newest audio sample; zero: none in this session

	paused      bool
	pausedAt    time.Time
	audioTracks int
}

func (r *clipRecorder) factsOf(s *recorderSession) recorderFacts {
	var f recorderFacts

	s.mu.Lock()
	f.conn, f.connAt, f.connectedAt, f.unsupported = s.conn, s.connAt, s.connectedAt, s.unsupported
	s.mu.Unlock()

	if ns := s.lastVideo.Load(); ns != 0 {
		f.lastVideo = time.Unix(0, ns)
		f.flowSince = time.Unix(0, s.flowSince.Load())
	}
	if ns := s.pipe.audio.lastSample.Load(); ns != 0 {
		f.lastAudio = time.Unix(0, ns)
	}

	r.mu.Lock()
	f.paused, f.pausedAt, f.audioTracks = r.stream.Paused, r.pausedAt, r.stream.AudioTracks
	r.mu.Unlock()

	return f
}

// judgeRecorder is the watchdogs of §2.4.4 on one connected session, in
// order:
//
//  1. The stream cannot be recorded (not H.264, or B-frames): close, and try
//     again in five minutes.
//  2. The connection failed or closed, which nobody asked of it while it is
//     watched: reconnect.
//  3. It has been disconnected for more than five seconds: reconnect.
//  4. No video for noVideoWatchdog while the stream is not paused, counted
//     from the newest packet or when it was last seen paused if that is
//     later: the publisher has probably gone, so what was recorded is its
//     END. While paused, wait. Before the session's first packet the wait is
//     maxKeyframeGap, counted from when it connected (or the pause): Broadcast
//     Box sends a new session no video at all until the publisher's next
//     keyframe (whep/packet_writer.go, IsWaitingForKeyframe), and publishers
//     such as OBS, which ignore the keyframe requests it sends for it, put
//     ten seconds or more between keyframes. A stream whose keyframes are
//     further apart than that cannot be recorded anyway (§2.4.4).
//  5. The sound died: audio flowed in this session, Broadcast Box says the
//     stream has audio, and none has come for two seconds while video has
//     come without a stall all that time. This is Broadcast Box reusing its
//     outgoing SSRC with a new publisher's sequence numbers, which pion's
//     SRTP replay check then drops, and also the backstop for a publisher
//     reconnect shorter than excessLimit. Reconnect. Video that stalled too
//     (a network hiccup, both tracks coming back together) is not it.
//
// The pipeline's own problems (rare keyframes, write errors, a full queue,
// timestamp breaks, wipes) end an epoch, not the session, and arrive as
// pipeline events instead.
func judgeRecorder(f recorderFacts, now time.Time) (recorderEnd, bool) {
	switch {
	case f.unsupported != nil:
		return recorderEnd{reason: f.unsupported.Error(), tail: clipTailDropped, unsupported: true}, true
	case f.conn == webrtc.PeerConnectionStateFailed || f.conn == webrtc.PeerConnectionStateClosed:
		return recorderEnd{reason: "the connection to Broadcast Box " + f.conn.String(), tail: clipTailDropped}, true
	case f.conn == webrtc.PeerConnectionStateDisconnected && now.Sub(f.connAt) > recorderDisconnectedFor:
		return recorderEnd{reason: "the connection to Broadcast Box was lost", tail: clipTailDropped}, true
	}

	if !f.paused {
		since := f.connectedAt
		for _, at := range []time.Time{f.lastVideo, f.pausedAt} {
			if at.After(since) {
				since = at
			}
		}
		switch {
		case since.IsZero():
		case f.lastVideo.IsZero() && now.Sub(since) >= maxKeyframeGap:
			return recorderEnd{reason: "no video came from Broadcast Box for " + maxKeyframeGap.String() +
				" after connecting (keyframes too rare?)", tail: clipTailEnded}, true
		case !f.lastVideo.IsZero() && now.Sub(since) >= noVideoWatchdog:
			return recorderEnd{reason: "no video came from Broadcast Box for " + noVideoWatchdog.String(), tail: clipTailEnded}, true
		}
	}

	audioDied := f.audioTracks > 0 && !f.lastAudio.IsZero() && now.Sub(f.lastAudio) >= recorderAudioDiedAfter &&
		!f.lastVideo.IsZero() && now.Sub(f.lastVideo) < recorderVideoFlowGap && now.Sub(f.flowSince) >= recorderAudioDiedAfter
	if audioDied {
		return recorderEnd{reason: "the sound stopped while the picture carried on", tail: clipTailDropped}, true
	}

	return recorderEnd{}, false
}

// endSession closes a session a watchdog ended: what its epoch holds becomes
// a tail, and the next attempt waits its turn. A session that never became
// ready counts towards "failed"; one that was ready for recorderBackoffReset
// starts the backoff again.
func (r *clipRecorder) endSession(s *recorderSession, end recorderEnd) {
	wipes, ok := r.takeSession(s)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), recorderCloseWait)
	frozen := r.closeSession(ctx, s, true)
	cancel()
	if frozen != nil {
		r.handOver(frozen, end.tail, wipes)
	}

	now := r.clock.Now()
	reason := r.scrub(end.reason)

	s.mu.Lock()
	readyAt := s.readyAt
	s.mu.Unlock()

	r.mu.Lock()
	newlyUnsupported := false
	switch {
	case end.unsupported:
		newlyUnsupported = r.unsupported != reason
		r.unsupported = reason
		r.wait = recorderUnsupportedRetry
	default:
		if readyAt.IsZero() {
			r.failures++
		} else if now.Sub(readyAt) >= recorderBackoffReset {
			r.step = 0
		}
		r.wait = recorderBackoffAfter(r.step)
		r.step = min(r.step+1, len(recorderBackoff)-1)
	}
	r.reasonText = reason
	wait := r.wait
	r.mu.Unlock()

	if end.unsupported {
		if newlyUnsupported {
			r.log.LogWarn("A stream cannot be clipped", "stream_handle", r.handle, "reason", reason,
				"retry_in", wait.String())
		}
	} else {
		r.logProblem("A clip recorder is reconnecting to Broadcast Box", reason, "retry_in", wait.String())
	}
	r.changed()
}

// logProblem logs a recording problem at Warn, at most once a minute for the
// stream.
func (r *clipRecorder) logProblem(msg, reason string, more ...any) {
	now := r.clock.Now()

	r.mu.Lock()
	if !r.lastProblemLog.IsZero() && now.Sub(r.lastProblemLog) < recorderProblemLogEvery {
		r.mu.Unlock()
		return
	}
	r.lastProblemLog = now
	r.mu.Unlock()

	r.log.LogWarn(msg, append([]any{"stream_handle", r.handle, "reason", reason}, more...)...)
}

// onPipeline is the writer's news of session s's epochs, on its goroutine
// (or on finish's, when closing drains it).
func (r *clipRecorder) onPipeline(s *recorderSession, event pipelineEvent) {
	switch event.kind {
	case pipeEventReady:
		now := r.clock.Now()
		status := s.pipe.writer.current().status()

		r.mu.Lock()
		current := r.session == s
		if current {
			r.ready, r.clipFromAt = true, event.at
			r.failures, r.unsupported, r.reasonText, r.lastFailLogged = 0, "", "", ""
		}
		r.mu.Unlock()
		if !current {
			return
		}

		s.mu.Lock()
		if s.readyAt.IsZero() {
			s.readyAt = now
		}
		s.mu.Unlock()

		r.log.LogInfo("A clip recorder is recording", "stream_handle", r.handle, "epoch", status.epoch,
			"size", fmt.Sprintf("%dx%d", status.width, status.height), "codec", status.codecs)

	case pipeEventCut, pipeEventRestarted:
		reason := r.scrub(event.reason)
		r.mu.Lock()
		current := r.session == s
		wipes := r.wipes
		if s.taken {
			wipes = s.takenWipes
		}
		if current {
			r.ready, r.clipFromAt = false, time.Time{}
			r.reasonText = reason
		}
		r.mu.Unlock()

		if event.kind == pipeEventCut && event.tail != nil {
			r.handOver(event.tail, clipTailDropped, wipes)
		}
		if event.reason != restartWiped {
			r.logProblem("A clip recording was cut", reason)
		}

	case pipeEventTruncated:
		r.logProblem("Clips of a stream are shorter than the clip length", r.scrub(event.reason))
	}

	r.changed()
}

// noteConn is the PeerConnection's news of its state.
func (r *clipRecorder) noteConn(s *recorderSession, state webrtc.PeerConnectionState) {
	now := r.clock.Now()

	s.mu.Lock()
	if s.conn != state {
		s.conn, s.connAt = state, now
	}
	if state == webrtc.PeerConnectionStateConnected && s.connectedAt.IsZero() {
		s.connectedAt = now
	}
	s.mu.Unlock()

	s.nudge()
}

// noteUnsupported ends s: the stream cannot be recorded.
func (r *clipRecorder) noteUnsupported(s *recorderSession, err error) {
	s.mu.Lock()
	if s.unsupported == nil {
		s.unsupported = err
	}
	s.mu.Unlock()

	s.nudge()
}

// noteSessionID keeps the id Broadcast Box gave the session as ours from the
// moment it is known, before any media flows, so that no poll counts it. A
// session already being closed is kept for its minute.
func (r *clipRecorder) noteSessionID(s *recorderSession, id string) {
	if id == "" {
		r.mu.Lock()
		warned := r.warnedNoID
		r.warnedNoID = true
		r.mu.Unlock()
		if !warned {
			r.log.LogWarn("Broadcast Box did not say which session the clip recorder opened; it will be counted as a viewer.",
				"stream_handle", r.handle)
		}
		return
	}

	r.own.open(id)

	s.mu.Lock()
	s.sessionID = id
	closed := s.closed
	s.mu.Unlock()

	if closed {
		r.own.closed(id, r.clock.Now())
	}
}

// connectWHEP is one attempt (§2.4.2): a PeerConnection with a receive-only
// video and audio transceiver, our candidates gathered in full (Broadcast Box
// does not trickle, and neither do we), the offer sent through exchangeWHEP,
// the session id kept as ours at once, and the answer applied. It returns
// once the connection is open; media then arrives through onTrack.
func (r *clipRecorder) connectWHEP(ctx context.Context, s *recorderSession) error {
	r.mu.Lock()
	cfg, key := r.cfg, r.stream.StreamKey
	r.mu.Unlock()
	if cfg == nil || key == "" {
		return errors.New("the recorder has no stream to record")
	}

	api, allowed, err := r.peers.forAttempt(ctx, cfg.BroadcastBoxURL)
	if err != nil {
		return err
	}

	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return fmt.Errorf("could not make a WebRTC connection: %w", err)
	}
	if !s.setPeer(pionRecorderPeer{pc: pc}) {
		return errors.New("the recorder was stopped")
	}

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) { r.noteConn(s, state) })
	pc.OnTrack(func(track *webrtc.TrackRemote, receiver *webrtc.RTPReceiver) {
		r.readSafely(s, "a clip recorder's new track", func() { r.onTrack(s, track, receiver) })
	})

	for _, kind := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio} {
		if _, err := pc.AddTransceiverFromKind(kind, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
			return fmt.Errorf("could not set up a %s receiver: %w", kind, err)
		}
	}

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("could not make an offer: %w", err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("could not use our own offer: %w", err)
	}

	// Gathering only host candidates finishes almost at once. Should it not,
	// the candidates found so far go: a session that cannot connect with
	// them fails like any other.
	gatherTimer := time.NewTimer(recorderGatherWait)
	select {
	case <-gathered:
	case <-gatherTimer.C:
	case <-ctx.Done():
		gatherTimer.Stop()
		return ctx.Err()
	}
	gatherTimer.Stop()

	whepCtx, cancel := context.WithTimeout(ctx, recorderWHEPWait)
	answer, sessionID, err := r.whep(whepCtx, cfg, key, pc.LocalDescription().SDP)
	cancel()
	if err != nil {
		return err
	}
	r.noteSessionID(s, sessionID)

	// Said at once, rather than after ten seconds of waiting and a reason
	// that blames UDP: closing the attempt ends Broadcast Box's session.
	if onlyLoopback(allowed) && !offersLoopback(answer) {
		return errBroadcastBoxLoopback
	}

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: answer}); err != nil {
		return fmt.Errorf("Broadcast Box's answer could not be used: %w", err)
	}

	return r.awaitConnected(ctx, s)
}

// onlyLoopback is whether every address media may come from is this
// machine's loopback.
func onlyLoopback(allowed []string) bool {
	for _, address := range allowed {
		if ip := net.ParseIP(address); ip == nil || !ip.IsLoopback() {
			return false
		}
	}

	return len(allowed) > 0
}

// offersLoopback is whether an answer offers a loopback address to receive
// media from.
func offersLoopback(sdp string) bool {
	for _, line := range candidateLines(sdp) {
		// a=candidate:<foundation> <component> <transport> <priority> <address> <port> typ ...
		fields := strings.Fields(line)
		if len(fields) > 4 {
			if ip := net.ParseIP(fields[4]); ip != nil && ip.IsLoopback() {
				return true
			}
		}
	}

	return false
}

// awaitConnected waits up to recorderConnectWait for the connection to open.
func (r *clipRecorder) awaitConnected(ctx context.Context, s *recorderSession) error {
	timer := time.NewTimer(recorderConnectWait)
	defer timer.Stop()

	for {
		state := s.connState()
		switch state {
		case webrtc.PeerConnectionStateConnected:
			return nil
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			return fmt.Errorf("the connection to Broadcast Box %s; check that this server can exchange UDP with it", state)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("the connection to Broadcast Box did not open within %s (it was %s); check that this server can exchange UDP with it",
				recorderConnectWait, state)
		case <-s.wake:
		}
	}
}

// rtpTrack is the part of a pion TrackRemote a reader uses.
type rtpTrack interface {
	ReadRTP() (*rtp.Packet, interceptor.Attributes, error)
}

// recorderTrack is the part of a pion TrackRemote onTrack uses, and
// recorderReceiver the part of its RTPReceiver: a test hands it stand-ins.
type recorderTrack interface {
	rtpTrack
	Kind() webrtc.RTPCodecType
	Codec() webrtc.RTPCodecParameters
}

type recorderReceiver interface {
	ReadRTCP() ([]rtcp.Packet, interceptor.Attributes, error)
	GetParameters() webrtc.RTPParameters
}

// readSafely runs one of a session's readers, on a goroutine of pion's or of
// its own, where a panic over what a stream sent would otherwise end the
// plugin. One that panics ends the session as a stream that cannot be
// recorded does, so it is tried again much later (recovery.go).
func (r *clipRecorder) readSafely(s *recorderSession, where string, read func()) {
	if safely(r.log, where, read) {
		r.noteUnsupported(s, errRecorderPanicked)
	}
}

// onTrack starts a track's reader. Video that is not H.264 cannot be
// recorded; audio that is not Opus is left unread, and the clips are silent.
// Every receiver's RTCP is read, so that the interceptors see Broadcast Box's
// reports.
func (r *clipRecorder) onTrack(s *recorderSession, track recorderTrack, receiver recorderReceiver) {
	if s.addReader() {
		go func() {
			defer s.readers.Done()
			r.readSafely(s, "a clip recorder's RTCP reader", func() {
				for {
					if _, _, err := receiver.ReadRTCP(); err != nil {
						return
					}
				}
			})
		}()
	}

	codec := track.Codec()
	switch track.Kind() {
	case webrtc.RTPCodecTypeVideo:
		if !strings.EqualFold(codec.MimeType, webrtc.MimeTypeH264) {
			r.noteUnsupported(s, fmt.Errorf("%w (it is %s)", errNotH264, strings.TrimPrefix(codec.MimeType, "video/")))
			return
		}

		h264 := map[uint8]bool{uint8(codec.PayloadType): true}
		for _, negotiated := range receiver.GetParameters().Codecs {
			if strings.EqualFold(negotiated.MimeType, webrtc.MimeTypeH264) {
				h264[uint8(negotiated.PayloadType)] = true
			}
		}
		s.mu.Lock()
		s.h264 = h264
		s.mu.Unlock()

		if s.addReader() {
			go func() {
				defer s.readers.Done()
				r.readSafely(s, "a clip recorder's video reader", func() { r.readVideo(s, track) })
			}()
		}

	case webrtc.RTPCodecTypeAudio:
		if !strings.EqualFold(codec.MimeType, webrtc.MimeTypeOpus) {
			return
		}
		if s.addReader() {
			go func() {
				defer s.readers.Done()
				r.readSafely(s, "a clip recorder's audio reader", func() { r.readAudio(s, track) })
			}()
		}
	}
}

// readVideo is the video track's goroutine (§2.4.3). It stamps each packet
// with the time it was read and hands it to the reader, which never touches
// the disk; it ends when the connection closes, or when the stream turns out
// not to be recordable.
func (r *clipRecorder) readVideo(s *recorderSession, track rtpTrack) {
	for {
		pkt, _, err := track.ReadRTP()
		if err != nil {
			return
		}
		arrival := time.Now().UnixNano()
		s.noteVideoPacket(arrival)
		if err := s.pipe.video.push(pkt, arrival); err != nil {
			r.noteUnsupported(s, err)
			return
		}
	}
}

func (r *clipRecorder) readAudio(s *recorderSession, track rtpTrack) {
	for {
		pkt, _, err := track.ReadRTP()
		if err != nil {
			return
		}
		s.pipe.audio.push(pkt, time.Now().UnixNano())
	}
}
