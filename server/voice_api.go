package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/mattermost/mattermost/server/public/plugin"
)

// Talking from Matty: the routes a phone joins, moves, mutes and leaves
// with, the admins' card, and the listener's life (voice_rtc.go).
//
// Talk is on while the switch is, the port is open with addresses to give
// phones, the roster connection is connected, and Mattermost is not a
// cluster. Not in a cluster, because a phone's voice reaches one node, on
// the address its admin set for that node, while its join, moves and leave
// are requests any node may be handed; and each node holds the Mumble
// sessions it made. So it is refused there, with a sentence that says why.

const (
	// Leaving voice, which the app check never refuses (appCheckExemptRequest).
	voiceLeavePath = "/api/v1/voice/leave"

	// The largest body a voice route reads: an offer with eight sections and
	// its candidates is a few kilobytes.
	maxVoiceBody = 64 << 10

	voiceBodyText      = "That is not a voice request this server can read."
	voiceBodyLargeText = "That voice request is too large."
	voiceChannelText   = "Say which voice channel."
	voiceJoinsText     = "Too many joins at once. Wait a moment and try again."
	voiceChangesText   = "Too many voice changes at once. Wait a moment and try again."
	voiceAccountText   = "Your account could not be read just now. Try again in a moment."
	voiceAdminOnlyText = "Only a system admin can see how talking from Matty stands."
	voiceAskText       = "Ask from Matty."
)

// The requests and answers of the voice routes.
type (
	voiceJoinRequest struct {
		Channel  *uint32 `json:"channel"`
		Muted    bool    `json:"muted"`
		Deafened bool    `json:"deafened"`
		SDP      string  `json:"sdp"`
	}

	voiceJoinResponse struct {
		SDP string `json:"sdp"`
		voiceJoined
	}

	voiceMoveRequest struct {
		Channel *uint32 `json:"channel"`
	}

	voiceMoveResponse struct {
		Channel uint32 `json:"channel"`
	}

	voiceStateRequest struct {
		Muted    bool `json:"muted"`
		Deafened bool `json:"deafened"`
	}

	voiceStateResponse struct {
		Muted    bool `json:"muted"`
		Deafened bool `json:"deafened"`
	}

	voiceLeaveResponse struct {
		Left bool `json:"left"`
	}
)

// voiceListener is this node's listener, made on first use.
func (p *Plugin) voiceListener() *voiceListener {
	p.voiceRTCOnce.Do(func() {
		p.voiceRTCState = newVoiceListener(p.safely)
	})

	return p.voiceRTCState
}

// voiceLookupNow is how the addresses for phones are looked up.
func (p *Plugin) voiceLookupNow() voiceLookup {
	if p.voiceTalkLookup != nil {
		return p.voiceTalkLookup
	}

	return net.DefaultResolver.LookupIPAddr
}

// voiceTalkUnavailable is why talk is off, or "" while it is on.
func (p *Plugin) voiceTalkUnavailable(cfg *configuration) string {
	if !cfg.voiceTalkWanted() {
		return voiceTalkSwitchedOffText
	}

	state := p.voiceListener().state()
	switch {
	case state.cluster:
		return voiceTalkClusterText
	case !voiceTalkPortAllowed(cfg.VoiceTalkPort):
		return voiceTalkPortRangeText
	case !p.getVoice().connected:
		return voiceNotConnectedText
	case !state.up && state.problem != "":
		return voiceTalkBrokenText
	case !state.up:
		return voiceTalkStartingText
	default:
		return ""
	}
}

// voiceTalkAlertFor is /me's voiceTalkAlert for system admins: talk is
// switched on and the listener is down, and why.
func (p *Plugin) voiceTalkAlertFor(cfg *configuration) string {
	if !cfg.voiceTalkWanted() {
		return ""
	}

	return p.voiceListener().state().problem
}

// ---------------------------------------------------------------- the life

// restartVoiceTalk brings talk to what the settings say: off, with every
// session ended and the port closed; or on, the port open and the addresses
// looked up, again every ten minutes. Sessions end too when the voice
// server's own settings changed under them. Run one at a time, and never
// from inside OnConfigurationChange itself, since it may wait for phones.
func (p *Plugin) restartVoiceTalk() {
	p.voiceTalkRunLock.Lock()
	defer p.voiceTalkRunLock.Unlock()

	p.stopVoiceTalkWorkerLocked()

	cfg := p.getConfiguration()
	previous := p.voiceTalkRanWith
	p.voiceTalkRanWith = cfg

	if !cfg.voiceTalkWanted() {
		p.voiceTalk().endAll(voiceEndingOff)
		p.voiceListener().shut()

		return
	}

	if previous != nil && previous.voiceTalkWanted() && !sameMumbleConnection(previous, cfg) {
		p.voiceTalk().endAll(voiceEndingSettings)
	}

	// Which name another voice server takes is not known.
	if previous == nil || !sameMumbleConnection(previous, cfg) {
		p.voiceTalk().forgetNames()
	}

	// Up already, for the same addresses: a look-up that fails on the way
	// keeps the ones it has.
	up := previous != nil && previous.VoiceTalkAddress == cfg.VoiceTalkAddress && p.voiceListener().state().up

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	p.voiceTalkCancel, p.voiceTalkDone = cancel, done

	go p.runVoiceTalk(ctx, cfg, up, done)
}

// stopVoiceTalk ends every session, telling each phone voice is restarting
// here (which it joins again after by itself), and closes the port: for
// deactivation, which a restart and an upgrade are too.
func (p *Plugin) stopVoiceTalk() {
	p.voiceTalkRunLock.Lock()
	defer p.voiceTalkRunLock.Unlock()

	p.stopVoiceTalkWorkerLocked()
	p.voiceTalkRanWith = nil

	p.voiceTalk().endAll(voiceEndingRestart)
	p.voiceListener().shut()
}

func (p *Plugin) stopVoiceTalkWorkerLocked() {
	if p.voiceTalkCancel == nil {
		return
	}

	p.voiceTalkCancel()
	<-p.voiceTalkDone
	p.voiceTalkCancel, p.voiceTalkDone = nil, nil
}

// runVoiceTalk keeps the listener up, looking the addresses up again every
// ten minutes, and trying again sooner while it is down (voiceTalkRetry),
// and sweeps the sessions once a minute (voice_sessions.go), until talk is
// restarted or stopped. up is whether the listener is up already for the
// same addresses, so that a restart whose look-up fails keeps the ones it had
// and the phones on them.
func (p *Plugin) runVoiceTalk(ctx context.Context, cfg *configuration, up bool, done chan struct{}) {
	defer close(done)

	swept := make(chan struct{})
	go func() {
		defer close(swept)
		p.voiceTalk().run(ctx)
	}()
	defer func() { <-swept }()

	told, retry := "", time.Duration(0)
	for {
		p.safely("starting talk from Matty", func() {
			var problem string
			up, problem = p.bringVoiceTalkUp(ctx, cfg, up)
			if problem != "" && problem != told {
				p.API.LogWarn("Talking from Matty is down", "reason", problem)
			}
			told = problem
		})
		p.voiceTalkPasses.Add(1)

		wait := voiceTalkResolveEvery
		if up {
			retry = 0
		} else {
			retry = voiceTalkRetry(retry)
			wait = retry
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// voiceTalkRetry is how long a listener that is down waits before it is
// tried again, after it waited previous (0 the first time): 30 seconds,
// then twice as long each time, up to the ten minutes of a look-up.
func voiceTalkRetry(previous time.Duration) time.Duration {
	if previous <= 0 {
		return voiceTalkRetryFirst
	}

	return min(2*previous, voiceTalkResolveEvery)
}

// bringVoiceTalkUp is one pass: the port opened if it is not, the addresses
// looked up, and the API for new phones built when they changed. While it
// was up, a look-up that fails keeps the addresses it had: a name server
// away for a moment takes nobody's voice. It is whether the listener is up,
// and why not.
func (p *Plugin) bringVoiceTalkUp(ctx context.Context, cfg *configuration, wasUp bool) (bool, string) {
	listener := p.voiceListener()

	siteURL := ""
	if mattermost := p.API.GetConfig(); mattermost != nil {
		if enabled := mattermost.ClusterSettings.Enable; enabled != nil && *enabled {
			listener.down(voiceTalkClusterText, true)
			return false, voiceTalkClusterText
		}
		if mattermost.ServiceSettings.SiteURL != nil {
			siteURL = *mattermost.ServiceSettings.SiteURL
		}
	}

	if !voiceTalkPortAllowed(cfg.VoiceTalkPort) {
		listener.down(voiceTalkPortRangeText, false)
		return false, voiceTalkPortRangeText
	}

	if err := listener.open(cfg.VoiceTalkPort); err != nil {
		problem := voiceTalkPortText(cfg.VoiceTalkPort, err)
		listener.down(problem, false)

		return false, problem
	}

	addresses, problem := resolveVoiceAddresses(ctx, cfg.voiceTalkAddresses(), siteURL, p.voiceLookupNow())
	if problem != "" {
		if wasUp && listener.state().up {
			return true, ""
		}
		listener.down(problem, false)

		return false, problem
	}

	if err := listener.serve(addresses); err != nil {
		listener.down(voicePeerFailedText, false)
		return false, voicePeerFailedText
	}

	return true, ""
}

// ---------------------------------------------------------------- the routes

// voiceTalkOn is whether a join, move or change may go ahead, and if not, it
// has answered 503 with /access's reason.
func (p *Plugin) voiceTalkOn(w http.ResponseWriter, cfg *configuration) bool {
	if reason := p.voiceTalkUnavailable(cfg); reason != "" {
		voiceUnavailableRefusal(reason, 0).write(w)
		return false
	}

	return true
}

// voiceAsked is the checks every voice route starts with: asked by a
// script, never cached.
func voiceAsked(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	if !madeByScript(r) {
		writeError(w, http.StatusForbidden, voiceAskText)
		return false
	}

	return true
}

// readVoiceBody decodes a voice route's body of at most maxVoiceBody, or
// answers.
func readVoiceBody(w http.ResponseWriter, r *http.Request, into any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxVoiceBody))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, voiceBodyLargeText)
			return false
		}
		writeError(w, http.StatusBadRequest, voiceBodyText)

		return false
	}
	if json.Unmarshal(raw, into) != nil {
		writeError(w, http.StatusBadRequest, voiceBodyText)
		return false
	}

	return true
}

// handleVoiceJoin answers POST /api/v1/voice/join: the person's voice
// session, made or taken over, in the channel asked for, muted and deafened
// as asked, with the phone's connection answered.
func (p *Plugin) handleVoiceJoin(c *plugin.Context, w http.ResponseWriter, r *http.Request, userID string) {
	if !voiceAsked(w, r) {
		return
	}

	cfg := p.getConfiguration()
	if !p.voiceTalkOn(w, cfg) {
		return
	}

	limiter := p.limiters().voiceJoin
	if !limiter.allow(userID) {
		writeTooMany(w, voiceJoinsText, limiter, userID, time.Now())
		return
	}

	var ask voiceJoinRequest
	if !readVoiceBody(w, r, &ask) {
		return
	}
	if ask.Channel == nil {
		writeError(w, http.StatusBadRequest, voiceChannelText)
		return
	}
	receive, ok := voiceOfferShape(ask.SDP)
	if !ok {
		writeError(w, http.StatusBadRequest, voiceBadOfferText)
		return
	}

	user, appErr := p.API.GetUser(userID)
	if appErr != nil || user == nil {
		writeCodedError(w, http.StatusServiceUnavailable, voiceAccountText, accessCodeStore)
		return
	}

	sessionID := ""
	if c != nil {
		sessionID = c.SessionId
	}

	dial := voiceTalkDialOf(cfg)
	matty, version := mattyOf(r.UserAgent())

	var answer string
	joined, err := p.voiceTalk().join(r.Context(), voiceJoinAsk{
		UserID:    userID,
		Username:  user.Username,
		SessionID: sessionID,
		Channel:   *ask.Channel,
		Muted:     ask.Muted,
		Deafened:  ask.Deafened,
		Matty:     matty,
		Version:   version,
	}, dial, func(s *voiceSession) (voicePeer, error) {
		peer, sdp, err := p.voiceListener().answer(r.Context(), ask.SDP, receive, s)
		if err != nil {
			return nil, err
		}
		answer = sdp

		return peer, nil
	})
	if err != nil {
		voiceRefusalOf(err).write(w)
		return
	}

	writeJSON(w, http.StatusOK, voiceJoinResponse{SDP: answer, voiceJoined: joined})
}

// handleVoiceMove answers POST /api/v1/voice/move.
func (p *Plugin) handleVoiceMove(w http.ResponseWriter, r *http.Request, userID string) {
	if !voiceAsked(w, r) {
		return
	}

	cfg := p.getConfiguration()
	if !p.voiceTalkOn(w, cfg) {
		return
	}

	limiter := p.limiters().voiceChange
	if !limiter.allow(userID) {
		writeTooMany(w, voiceChangesText, limiter, userID, time.Now())
		return
	}

	var ask voiceMoveRequest
	if !readVoiceBody(w, r, &ask) {
		return
	}
	if ask.Channel == nil {
		writeError(w, http.StatusBadRequest, voiceChannelText)
		return
	}

	channel, err := p.voiceTalk().move(r.Context(), userID, *ask.Channel)
	if err != nil {
		voiceRefusalOf(err).write(w)
		return
	}

	writeJSON(w, http.StatusOK, voiceMoveResponse{Channel: channel})
}

// handleVoiceState answers POST /api/v1/voice/state: mute and deafen,
// deafened being muted too.
func (p *Plugin) handleVoiceState(w http.ResponseWriter, r *http.Request, userID string) {
	if !voiceAsked(w, r) {
		return
	}

	cfg := p.getConfiguration()
	if !p.voiceTalkOn(w, cfg) {
		return
	}

	limiter := p.limiters().voiceChange
	if !limiter.allow(userID) {
		writeTooMany(w, voiceChangesText, limiter, userID, time.Now())
		return
	}

	var ask voiceStateRequest
	if !readVoiceBody(w, r, &ask) {
		return
	}

	muted, deafened, err := p.voiceTalk().state(userID, ask.Muted, ask.Deafened)
	if err != nil {
		voiceRefusalOf(err).write(w)
		return
	}

	writeJSON(w, http.StatusOK, voiceStateResponse{Muted: muted, Deafened: deafened})
}

// handleVoiceLeave answers POST /api/v1/voice/leave, whether or not there
// was a session, whatever talk's switch says and whatever the app check
// found: leaving is never refused.
func (p *Plugin) handleVoiceLeave(w http.ResponseWriter, r *http.Request, userID string) {
	if !voiceAsked(w, r) {
		return
	}

	talk := p.voiceTalk()
	had := talk.of(userID) != nil
	talk.leave(userID)

	writeJSON(w, http.StatusOK, voiceLeaveResponse{Left: had})
}

// voiceAdminView is GET /api/v1/admin/voice: how talking from Matty stands
// on the node that answers, and the lines of Channels to show that name no
// channel or more than one. Never who is talking.
type voiceAdminView struct {
	// The switch, and whether the voice server is set and connected.
	On        bool `json:"on"`
	Connected bool `json:"connected"`

	// The port, whether it is open and answering, the addresses phones are
	// given, and why it is down.
	Port      int      `json:"port"`
	Open      bool     `json:"open"`
	Addresses []string `json:"addresses"`
	Problem   string   `json:"problem,omitempty"`

	// How many people are talking from Matty through this node now.
	Talking int `json:"talking"`

	// Whether the plugin's own budget of connections to the voice server
	// is lifted.
	NoBudget bool `json:"noBudget"`

	// The voice server's last refusal of somebody joining from Matty, and
	// when, in milliseconds.
	Refused   string `json:"refused,omitempty"`
	RefusedAt int64  `json:"refusedAt,omitempty"`

	// The lines of Channels to show that name no channel on the voice
	// server, as the admin wrote them, and the ones that name more than one,
	// with the channels they name, while it is connected.
	Unmatched []string       `json:"unmatched,omitempty"`
	Several   []voiceSeveral `json:"several,omitempty"`

	// The lines of Channels to show that are not used, connected or not:
	// the ones longer than 256 characters, each as its first 40 characters
	// and "…", and how many lines come after the first 200.
	TooLong []string `json:"tooLong,omitempty"`
	Dropped int      `json:"dropped,omitempty"`
}

// handleAdminVoice answers GET /api/v1/admin/voice, for system admins.
func (p *Plugin) handleAdminVoice(w http.ResponseWriter, r *http.Request, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, voiceAdminOnlyText)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	writeJSON(w, http.StatusOK, p.voiceAdminNow())
}

func (p *Plugin) voiceAdminNow() voiceAdminView {
	cfg := p.getConfiguration()
	state := p.voiceListener().state()
	talk := p.voiceTalk()
	voice := p.getVoice()

	view := voiceAdminView{
		On:        cfg.voiceTalkWanted(),
		Connected: cfg.EnableVoice && voice.connected,
		Port:      cfg.VoiceTalkPort,
		Addresses: []string{},
		Talking:   talk.count(),
		NoBudget:  cfg.VoiceTalkNoBudget,
	}
	if view.On {
		view.Open = state.up
		view.Problem = state.problem
		if state.up && state.port != 0 {
			view.Port = state.port
		}
		if state.addresses != nil {
			view.Addresses = state.addresses
		}
	}
	if refused, at := talk.lastRefusal(); refused != "" {
		view.Refused, view.RefusedAt = refused, at.UnixMilli()
	}
	if view.Connected {
		view.Unmatched = voice.unmatched
		view.Several = voice.several
	}
	setting := cfg.voiceShownSetting()
	view.TooLong, view.Dropped = setting.tooLong, setting.dropped

	return view
}
