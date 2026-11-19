package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/pion/sdp/v3"
)

// errorResponse is every refusal the plugin's own routes give, whichever
// route and whichever client (contract/README.md, "A refusal"):
//
//   - error: a sentence a person can read. It is the wording: a client shows
//     it as it is, and keeps words of its own only for no answer at all.
//   - code: a stable word for what was refused, for a client that does
//     something about it (ask for the reader's access again, offer to try
//     later) and for a test. Always there: a refusal whose handler names none
//     is given its status's (refusalCodeOf).
//   - retryAfterMs: with a refusal that is about time (a meter, a cooldown),
//     how long until asking again can work. Left out otherwise.
//
// The clips and the soundboard keep refusal types of their own, with the same
// three fields under the same names.
type errorResponse struct {
	Error        string `json:"error"`
	Code         string `json:"code"`
	RetryAfterMs int64  `json:"retryAfterMs,omitempty"`
}

// The codes of the refusals that are not one family's own. A handler that
// names none gets its status's, so no refusal ever goes out without a code.
const (
	codeBadRequest       = "bad_request"
	codeSignedOut        = "signed_out"
	codeForbidden        = "forbidden"
	codeNotFound         = "not_found"
	codeMethodNotAllowed = "method_not_allowed"
	codeConflict         = "conflict"
	codeGone             = "gone"
	codeTooLarge         = "too_large"
	codeRateLimited      = "rate_limited"
	codeFailed           = "failed"
	codeUnreachable      = "unreachable"
	codeUnavailable      = "unavailable"
	codeNotConfigured    = "not_configured"
)

// refusalCodeOf is the code of a refusal whose handler named none: what its
// status says, in the words the named codes already use.
func refusalCodeOf(status int) string {
	switch status {
	case http.StatusBadRequest:
		return codeBadRequest
	case http.StatusUnauthorized:
		return codeSignedOut
	case http.StatusForbidden:
		return codeForbidden
	case http.StatusNotFound:
		return codeNotFound
	case http.StatusMethodNotAllowed:
		return codeMethodNotAllowed
	case http.StatusConflict:
		return codeConflict
	case http.StatusGone:
		return codeGone
	case http.StatusRequestEntityTooLarge:
		return codeTooLarge
	case http.StatusTooManyRequests:
		return codeRateLimited
	case http.StatusBadGateway:
		return codeUnreachable
	case http.StatusServiceUnavailable:
		return codeUnavailable
	default:
		return codeFailed
	}
}

// retryAfterMs is a wait as a refusal says it: whole milliseconds, never a
// wait rounded down to none, and nothing at all for no wait.
func retryAfterMs(wait time.Duration) int64 {
	if wait <= 0 {
		return 0
	}

	return max(wait.Milliseconds(), 1)
}

// meResponse is what /me tells the person asking. It never carries the
// Broadcast Box URL: that is the origin's address on the LAN, which only an
// admin is told (see clientSafeError), the panel has no use for it, and a URL
// saved with a user name and password in it would otherwise reach everybody
// until somebody noticed.
//
// Nor the public watch address: it is only ever used with a stream key beside
// it, and Pop out now goes through /api/v1/watch/popout, for linked people.
type meResponse struct {
	// Which client contract this server speaks, and which version it is: so a
	// client can tell a plugin older or newer than the one it was written for
	// from one it understands, and say so, instead of misreading it. See
	// contractRevision.
	Contract int    `json:"contract"`
	Version  string `json:"version"`

	// What this server calls the plugin: the Name setting, "Matty" while it
	// is blank (brandName). Never empty.
	Name string `json:"name"`

	// Which artwork this server has beyond the icon, which always exists.
	Artwork brandArtwork `json:"artwork"`

	// Whether this plugin's files are a signed release, as on /access.
	Release releaseWord `json:"release"`

	// What the app check found for the caller's session, as on /access.
	App appWord `json:"app"`

	UserID    string `json:"userId"`
	Username  string `json:"username"`
	StreamKey string `json:"streamKey"`

	// What the panel may be for this person: "linked", "letIn", "locked", or
	// "unknown" when the store did not answer. The panel decides from this,
	// never from streamKey or clipsStreamer, and everything below that is
	// for linked people only comes from the same one reading.
	Access string `json:"access"`

	// Whether Pop out is offered: linked people only, and only while an
	// admin has set a public watch address.
	PopOut bool `json:"popOut,omitempty"`

	// For linked people only: whether an admin has also let them in, so the
	// Unlink confirmation can say they will still be able to watch.
	AlsoLetIn *bool `json:"alsoLetIn,omitempty"`

	BadgeOn     bool   `json:"badgeEnabled"`
	AutoLink    bool   `json:"autoLinkByUsername"`
	LinkedLive  bool   `json:"linkedLive"`
	StyleCode   bool   `json:"styleCodeBlocks"`
	OpenPanel   string `json:"openPanel"`
	NoSignal    string `json:"noSignalGhost"`
	BannerText  string `json:"bannerText"`
	BannerColor string `json:"bannerColor"`
	BannerPlace string `json:"bannerPlacement"`
	Configured  bool   `json:"configured"`

	// Admin-only. A viewer can do nothing about the airtime store and does not
	// need to be told about its internals.
	IsAdmin      bool          `json:"isAdmin"`
	AirtimeAlert *airtimeAlert `json:"airtimeAlert,omitempty"`

	// Why the voice roster is missing, when it is. Admin-only for the same
	// reason: everybody else would see a red line about a server they cannot
	// reach and cannot fix, every time they opened the panel.
	VoiceAlert string `json:"voiceAlert,omitempty"`

	// What is wrong with the birthday list, if anything. A list that will not
	// parse looks exactly like a list with no birthdays in it today, which is
	// the kind of silence nobody notices until a birthday has gone past.
	BirthdayAlert string `json:"birthdayAlert,omitempty"`

	// Which kinds of title the planning form can look up, so it offers a
	// search only where one will work rather than one that always comes back
	// empty. For linked people only, who are the only ones who can plan.
	Media *mediaFlags `json:"media,omitempty"`

	// What an admin should know about clips on this server, linked or not:
	// counts and states, never a clip. See clipsAlertFor.
	ClipsAlert string `json:"clipsAlert,omitempty"`

	// For somebody who has linked a stream: whether it is kept for clipping,
	// which they can switch. Absent for everybody else, and also absent for
	// somebody linked while the clips document cannot be read (halted with no
	// copy on this node, or the store not answering): the switch's position
	// is then not known, and no switch is better than one that may be wrong.
	// So its absence never says whether somebody is linked; StreamKey does.
	ClipsStreamer *clipsStreamerView `json:"clipsStreamer,omitempty"`

	// What is wrong with the let-in list, if anything. Admin-only, in any
	// access state, so the panel has it the moment it unlocks. The lock
	// itself never shows it: an admin's lock is everybody's, and the System
	// Console is where a locked admin learns of it (design/lock-screen.md,
	// the amendment of 2026-10-06).
	LetInAlert string `json:"letInAlert,omitempty"`

	// Twitch: present whenever Twitch live is on, and whenever the caller
	// has a connection or a takeover notice even while it is off, so
	// Disconnect can always be reached, the lock screen included. In any
	// access state: a Twitch connection is not a link and unlocks nothing.
	// Problem is for system admins only. See meTwitchFor.
	Twitch *meTwitch `json:"twitch,omitempty"`

	// Steam and Hytale (design/3.4.0.md §2.9): the caller's own connection,
	// on twitch's rule, in any access state, because a connection is not a
	// link either and unlocks nothing. Everybody else's is not here: it is
	// GET /api/v1/accounts, for people who may watch. See meAccountFor.
	Steam  *meAccount `json:"steam,omitempty"`
	Hytale *meAccount `json:"hytale,omitempty"`

	// 3.2.0, admin-only like voiceAlert. Why the soundboard cannot play
	// (sounds_play.go); a line of 24/7 channel achievements that could not
	// be read (achievements.go); and the biggest-fan and channel ledgers'
	// troubles (fans.go).
	SoundAlert        string       `json:"soundAlert,omitempty"`
	AchievementsAlert string       `json:"achievementsAlert,omitempty"`
	LedgerAlert       *ledgerAlert `json:"ledgerAlert,omitempty"`

	// Admin-only: the plugin's files do not match their signed release, or
	// its folder holds files nobody signed. See release_check.go.
	ReleaseAlert *releaseAlert `json:"releaseAlert,omitempty"`

	// Admin-only, from 4.1.0: push notifications are switched on here and
	// Mattermost decides to push to nobody, and why. See push.go.
	PushAlert string `json:"pushAlert,omitempty"`

	// Admin-only, from 4.3.0: talking from Matty is switched on and its
	// voice port is down, and why. See voice_api.go.
	VoiceTalkAlert string `json:"voiceTalkAlert,omitempty"`
}

type profileResponse struct {
	Available bool   `json:"available"`
	MOTD      string `json:"motd"`
	IsPublic  bool   `json:"isPublic"`
	Message   string `json:"message,omitempty"`
}

type saveProfileRequest struct {
	MOTD     string `json:"motd"`
	IsPublic bool   `json:"isPublic"`
}

type linkRequest struct {
	Token string `json:"token"`
}

// linkResponse answers a link and an unlink. Besides the stream name, it says
// what the caller's access is now, as GET /api/v1/access would: whoever made
// the change knows the outcome from the answer itself and has no need to ask
// again (the access event still goes to their other windows and devices).
type linkResponse struct {
	StreamKey  string `json:"streamKey"`
	Access     string `json:"access"`
	Configured bool   `json:"configured"`
}

// linked is the answer to a link (streamKey set) or an unlink (empty) that
// has just been written.
func (p *Plugin) linked(userID, streamKey string) linkResponse {
	response := linkResponse{StreamKey: streamKey, Access: accessUnknown, Configured: p.getConfiguration().validate() == nil}

	level, _, err := p.accessOf(userID)
	if err != nil {
		p.API.LogWarn("Could not read somebody's access to Matty", "error", err.Error())
	} else {
		response.Access = level.String()
	}

	return response
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError is a refusal that needs no code of its own: it goes out with its
// status's (refusalCodeOf), so every refusal has one.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message, Code: refusalCodeOf(status)})
}

// writeTooMany is a meter's refusal: its sentence, "rate_limited", and how
// long until that meter would let key through again, read without taking
// anything from it. Never cached: it is only true now.
func writeTooMany(w http.ResponseWriter, message string, limiter *rateLimiter, key string, now time.Time) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusTooManyRequests, errorResponse{
		Error:        message,
		Code:         codeRateLimited,
		RetryAfterMs: retryAfterMs(limiter.wait(key, now)),
	})
}

// clientSafeError turns an upstream failure into something safe to hand a
// non-admin. A transport error wraps *url.Error and therefore carries the
// origin's internal LAN address, and an upstream body can carry anything at
// all, so only known, non-sensitive conditions pass through verbatim. The
// detail is always logged instead.
func clientSafeError(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errStatusDisabled):
		return "The Broadcast Box status API is turned off on the server."
	default:
		return "Broadcast Box is not responding. Ask an admin to check the server."
	}
}

// ServeHTTP answers every request to the plugin.
//
// Nothing may be dispatched above the access gate (passAccessGate) without an
// access check of its own. The only dispatches above it are the static files,
// which need no session, the two clip families, each of which runs its own
// gate before anything else, and the six pages of Connect Twitch, Connect
// Steam and Connect Hytale (each service's connect window and its callback),
// each of which runs its own session and access checks;
// TestEveryRouteHasAnAccessClass fails on any other.
func (p *Plugin) ServeHTTP(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	// First, so that it covers every route: see recovery.go.
	defer p.recoverRequest(w, r)

	// A session Mattermost hands a request with is alive: push remembers
	// the last few, in memory (pushSessionGone).
	if c != nil {
		p.pushes().noteAlive(c.SessionId)
	}

	if strings.HasPrefix(r.URL.Path, "/static/") {
		p.serveStatic(w, r)
		return
	}

	// Connect Twitch's window, and Twitch sending the browser back. Both
	// answer in HTML rather than JSON, so they come before the check below,
	// and each checks the session and the caller's access itself.
	if underPath(r.URL.Path, twitchConnectPath) {
		p.serveTwitchConnect(c, w, r)
		return
	}
	if underPath(r.URL.Path, twitchCallbackPath) {
		p.serveTwitchCallback(c, w, r)
		return
	}

	// Connect Steam's and Connect Hytale's windows, and each service sending
	// the browser back: HTML, so before the user check, and each checks the
	// session and the caller's access itself (accounts.go). Four plain lines
	// rather than a loop over the services, so that the test above can see
	// every one of them.
	if underPath(r.URL.Path, steamConnectPath) {
		p.serveAccountConnect(c, w, r, steamProvider)
		return
	}
	if underPath(r.URL.Path, steamCallbackPath) {
		p.serveAccountCallback(c, w, r, steamProvider)
		return
	}
	if underPath(r.URL.Path, hytaleConnectPath) {
		p.serveAccountConnect(c, w, r, hytaleProvider)
		return
	}
	if underPath(r.URL.Path, hytaleCallbackPath) {
		p.serveAccountCallback(c, w, r, hytaleProvider)
		return
	}

	// Mattermost sets this header for authenticated sessions and strips any
	// client-supplied copy, so its presence is the authentication check.
	userID := r.Header.Get("Mattermost-User-Id")
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "You must be signed in to Mattermost.")
		return
	}

	// The app check's gate, while it is set to refuse a Matty that has not
	// passed: before everything below, the clips included (app_check.go).
	if !p.passAppCheckGate(w, r, c, userID) {
		return
	}

	// Every clip route, whole, before the switch: each family runs its own
	// gate ahead of anything else, and a route added to it later cannot be
	// added anywhere the gate does not run. See clips_api.go.
	if underPath(r.URL.Path, clipsPath) {
		p.serveClips(w, r, userID)
		return
	}
	if underPath(r.URL.Path, adminClipsPath) {
		p.serveAdminClips(w, r, userID)
		return
	}

	// Layer 1: whoever the route's class does not admit is refused here,
	// before any meter is spent or anything the request carries is read. The
	// System Console's routes pass, and ask for a system admin themselves.
	if !p.passAccessGate(w, r, userID) {
		return
	}

	switch r.URL.Path {
	case "/api/v1/streams":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleStreams(w, r)

	// The boards and everybody's record: see boards.go.
	case "/api/v1/boards":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleBoards(w, r)

	// Profile banners: see banners.go.
	case "/api/v1/banners":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleBanners(w, r)

	case "/api/v1/banner":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleBanner(w, r)

	case "/api/v1/me/banner":
		p.handleMyBanner(w, r, userID)

	case "/api/v1/me/banner/crop":
		p.handleMyBannerCrop(w, r, userID)

	case "/api/v1/me":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleMeIn(w, userID, sessionOf(c))

	case "/api/v1/access":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleAccess(w, userID, sessionOf(c))

	// Whether this plugin is a signed release, and the release's manifest and
	// signatures for a client to verify: see release_check.go.
	case "/api/v1/release":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleRelease(w)

	// The app check: a challenge, and the attestation that answers it. See
	// app_check.go.
	case "/api/v1/app/challenge":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleAppChallenge(w, c, userID)

	case "/api/v1/app/attest":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleAppAttest(w, r, c, userID)

	case "/api/v1/admin/appcheck":
		p.handleAdminAppCheck(w, r, userID)

	// Push notifications through UnifiedPush: a Matty's own registration
	// for its session, its confirmation and a test, each metered in its
	// handler; and the System Console's card. See push_api.go.
	case "/api/v1/me/push":
		p.handleMePush(w, r, c, userID)

	case "/api/v1/me/push/confirm":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handlePushConfirm(w, r, c, userID)

	case "/api/v1/me/push/test":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handlePushTest(w, c, userID)

	case "/api/v1/admin/push":
		p.handleAdminPush(w, r, userID)

	case "/api/v1/me/link":
		switch r.Method {
		case http.MethodPost:
			if !p.linkLimiter.allow(userID) {
				writeTooMany(w, linkTooMany, p.linkLimiter, userID, time.Now())
				return
			}
			p.handleLink(w, r, userID)
		case http.MethodDelete:
			// Taking a link back is never metered, like the disconnects:
			// somebody who mistyped their token five times can still unlink.
			p.handleUnlink(w, userID)
		default:
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		}

	case "/api/v1/me/profile":
		switch r.Method {
		case http.MethodGet:
			// Every call asks Broadcast Box, with the caller's own token and
			// behind no meter. So it is answered only for the panel's own
			// request (madeByScript): a picture in a post, loaded by every
			// linked reader's browser, asks nothing.
			if !madeByScript(r) {
				writeError(w, http.StatusForbidden, "Ask from the panel.")
				return
			}
			p.handleGetProfile(w, r, userID)
		case http.MethodPost:
			if !p.profileLimiter.allow(userID) {
				writeTooMany(w, "Too many changes. Wait a moment and try again.", p.profileLimiter, userID, time.Now())
				return
			}
			p.handleSaveProfile(w, r, userID)
		default:
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		}

	case "/api/v1/nowplaying":
		switch r.Method {
		case http.MethodGet:
			p.handleGetNowPlaying(w, userID)
		case http.MethodPost, http.MethodDelete:
			if !p.nowPlayingLimiter.allow(userID) {
				writeTooMany(w, "Too many changes. Wait a moment and try again.", p.nowPlayingLimiter, userID, time.Now())
				return
			}
			if r.Method == http.MethodPost {
				p.handleSetNowPlaying(w, r, userID)
			} else {
				p.handleClearNowPlaying(w, userID)
			}
		default:
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		}

	case "/api/v1/schedule", "/api/v1/schedule/remove", "/api/v1/schedule/edit", "/api/v1/schedule/join":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		if !p.communityLimiter.allow(userID) {
			writeTooMany(w, "Too many changes at once. Wait a moment and try again.", p.communityLimiter, userID, time.Now())
			return
		}

		switch r.URL.Path {
		case "/api/v1/schedule":
			p.handleScheduleAdd(w, r, userID)
		case "/api/v1/schedule/edit":
			p.handleScheduleEdit(w, r, userID)
		case "/api/v1/schedule/join":
			p.handleScheduleJoin(w, r, userID)
		default:
			p.handleScheduleRemove(w, r, userID)
		}

	// Sharing a plan into a channel (plan_share.go), metered on its own:
	// each one is a post as the sharer.
	case "/api/v1/schedule/share":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handlePlanShare(w, r, userID)

	// Awards by your name: see awards.go.
	case "/api/v1/awards/mine":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleAwardsMine(w, userID)

	case "/api/v1/awards/display":
		if r.Method != http.MethodPost && r.Method != http.MethodDelete {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleAwardDisplay(w, r, userID)

	case "/api/v1/takebacks":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleTakebacks(w, userID)

	case "/api/v1/achievements/mine":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleAchievementsMine(w, userID)

	// The soundboard: see sounds.go and sounds_play.go. Each route is
	// metered in its handler, after the checks a refusal should not spend a
	// press on.
	case "/api/v1/sounds":
		switch r.Method {
		case http.MethodGet:
			p.handleSoundsList(w, userID)
		case http.MethodPost:
			p.handleSoundAdd(w, r, userID)
		default:
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		}

	case "/api/v1/sounds/edit":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleSoundEdit(w, r, userID)

	case "/api/v1/sounds/remove":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleSoundRemove(w, r, userID)

	case "/api/v1/sounds/remove-mine":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleSoundRemoveMine(w, userID)

	case "/api/v1/sounds/play":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleSoundPlay(w, r, userID)

	case "/api/v1/sounds/audio":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleSoundAudio(w, r, userID)

	// Talking from Matty: see voice_api.go. Joining is metered in its
	// handler, after the checks a refusal should not spend a try on; leaving
	// is never metered or refused.
	case "/api/v1/voice/join":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleVoiceJoin(c, w, r, userID)

	case "/api/v1/voice/move":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleVoiceMove(w, r, userID)

	case "/api/v1/voice/state":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleVoiceState(w, r, userID)

	case "/api/v1/voice/leave":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleVoiceLeave(w, r, userID)

	// How talking from Matty stands, in the System Console.
	case "/api/v1/admin/voice":
		p.handleAdminVoice(w, r, userID)

	// Sounds on the server, in the System Console: see sounds_admin.go.
	case "/api/v1/admin/sounds":
		p.handleAdminSounds(w, r, userID)

	case "/api/v1/admin/sounds/audio":
		p.handleAdminSoundAudio(w, r, userID)

	case "/api/v1/admin/sounds/remove":
		p.handleAdminSoundRemove(w, r, userID)

	case "/api/v1/admin/sounds/block":
		p.handleAdminSoundBlock(w, r, userID)

	case "/api/v1/admin/sounds/check":
		p.handleAdminSoundCheck(w, r, userID)

	case "/api/v1/admin/sounds/restart":
		p.handleAdminSoundsRestart(w, r, userID)

	case "/api/v1/media/search":
		// A POST, with what to look for in its body: a search asks TMDB,
		// Steam or IGDB in the caller's name, and nothing that merely loads
		// an address (a picture in a post, a link preview) can make one. See
		// handleMediaSearch.
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		// Only for somebody who can plan, and checked before the meter, so a
		// refusal never spends one of their searches.
		if !p.requireLinkedStream(w, userID, searchNeedsLink) {
			return
		}
		// Metered like the other endpoints that reach out: the planning form
		// searches as somebody types, and every miss is a request to TMDB,
		// Steam or IGDB made in this server's name.
		if !p.mediaStore().searchLimiter.allow(userID) {
			writeTooMany(w, "Too many searches at once. Wait a moment and try again.", p.mediaStore().searchLimiter, userID, time.Now())
			return
		}
		p.handleMediaSearch(w, r)

	case "/api/v1/media/image":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleMediaImage(w, r, userID)

	// Remembered titles
	case "/api/v1/admin/media/cache":
		p.handleMediaCache(w, r, userID)

	case "/api/v1/admin/airtime":
		p.handleAirtime(w, r, userID)

	case "/api/v1/admin/airtime/status":
		p.handleAirtimeStatus(w, r, userID)

	case "/api/v1/admin/avatar":
		p.handleBotAvatar(w, r, userID)

	case "/api/v1/admin/bot/adopt":
		p.handleBotAdopt(w, r, userID)

	case "/api/v1/admin/theme":
		p.handleTheme(w, r, userID)

	case "/api/v1/admin/birthdays/seed":
		p.handleBirthdaySeed(w, r, userID)

	case "/api/v1/admin/settings":
		p.handleSettings(w, r, userID)

	case "/api/v1/admin/brand":
		p.handleBrand(w, r, userID)

	case "/api/v1/admin/channel-pictures":
		p.handleChannelPictures(w, r, userID)

	case "/api/v1/admin/stills":
		p.handleAdminStills(w, r, userID)

	case "/api/v1/admin/stills/block":
		p.handleAdminStillBlock(w, r, userID)

	// Who is let in to watch without a token, and unlinking somebody: the
	// System Console's Access card. See letin.go.
	case "/api/v1/admin/let-in":
		p.handleAdminLetIn(w, r, userID)

	case "/api/v1/admin/unlink":
		p.handleAdminUnlink(w, r, userID)

	// Taking anybody's profile banner away: see banners.go.
	case "/api/v1/admin/banner":
		p.handleAdminBanner(w, r, userID)

	case "/api/v1/channel/picture":
		p.handleChannelPicture(w, r)

	case "/api/v1/channel/thumbnail":
		p.handleChannelThumbnail(w, r)

	case "/api/v1/stills/claim":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		if !p.stills().claimLimiter.allow(userID) {
			writeTooMany(w, "Too many preview requests. Wait a moment and try again.", p.stills().claimLimiter, userID, time.Now())
			return
		}
		p.handleStillClaim(w, r, userID)

	case "/api/v1/stills":
		switch r.Method {
		case http.MethodGet:
			p.handleStill(w, r)
		case http.MethodPost:
			if !p.stills().uploadLimiter.allow(userID) {
				writeTooMany(w, "Too many previews at once. Wait a moment and try again.", p.stills().uploadLimiter, userID, time.Now())
				return
			}
			p.handleStillUpload(w, r, userID)
		default:
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		}

	case "/api/v1/whep":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		if !p.playbackLimiter.allow(userID) {
			writeTooMany(w, "Too many playback requests. Wait a moment and try again.", p.playbackLimiter, userID, time.Now())
			return
		}
		p.handleWHEP(w, r, userID)

	case "/api/v1/watch/popout":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleWatchPopOut(w, r)

	// Twitch live: see twitch_oauth.go and twitch_preview.go. Connecting is
	// metered in its handler, after the checks a refusal should not spend a
	// try on; disconnecting and putting the notice away are not metered at
	// all, like unlinking. (Leaving or cancelling a plan is not like them: it
	// shares the schedule's meter.)
	case "/api/v1/twitch/connect-ticket":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleTwitchTicket(c, w, userID)

	case "/api/v1/twitch/link":
		if r.Method != http.MethodDelete {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleTwitchUnlink(w, userID)

	case "/api/v1/twitch/notice":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleTwitchNotice(w, userID)

	case "/api/v1/twitch/preview":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleTwitchPreview(w, r, userID)

	// Twitch connections in the System Console: see twitch_admin.go.
	case "/api/v1/admin/twitch":
		p.handleAdminTwitch(w, r, userID)

	case "/api/v1/admin/twitch/block":
		p.handleAdminTwitchBlock(w, r, userID)

	case "/api/v1/admin/twitch/allday":
		p.handleAdminTwitchAllDay(w, r, userID)

	// Steam connections and Hytale connections in the System Console
	// (accounts_admin.go), whatever the switches say.
	case "/api/v1/admin/steam", "/api/v1/admin/hytale":
		p.handleAdminAccount(w, r, adminAccountProviderOf(r.URL.Path), userID)

	case "/api/v1/admin/steam/block", "/api/v1/admin/hytale/block":
		p.handleAdminAccountBlock(w, r, adminAccountProviderOf(r.URL.Path), userID)

	// Steam and Hytale: see accounts.go, steam.go and hytale.go. Everybody's
	// Steam status and Hytale names come from memory. Connecting is metered
	// in its handler, after the checks a refusal should not spend a try on;
	// taking things back is not metered at all, as for Twitch.
	case "/api/v1/accounts":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleAccounts(w, r)

	case "/api/v1/steam/connect-ticket", "/api/v1/hytale/connect-ticket":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleAccountTicket(c, w, accountProviderOf(r.URL.Path), userID)

	// Matty's own sign-in window (design/3.4.0.md §6.5).
	case "/api/v1/steam/app-connect", "/api/v1/hytale/app-connect":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleAccountAppConnect(c, w, accountProviderOf(r.URL.Path), userID)

	case "/api/v1/steam/app-finish", "/api/v1/hytale/app-finish":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleAccountAppFinish(c, w, r, accountProviderOf(r.URL.Path), userID)

	case "/api/v1/steam/link", "/api/v1/hytale/link":
		if r.Method != http.MethodDelete {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleAccountUnlink(w, accountProviderOf(r.URL.Path), userID)

	case "/api/v1/steam/notice", "/api/v1/hytale/notice":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		p.handleAccountNotice(w, accountProviderOf(r.URL.Path), userID)

	default:
		writeError(w, http.StatusNotFound, "Unknown endpoint.")
	}
}

// handleStreams answers GET /api/v1/streams with the snapshot as last
// published, or, to a reader who still holds that very snapshot and says so
// (If-None-Match with the tag it came with, from this route's ETag or from the
// streams event), with 304 and nothing else. A client asks again after every
// reconnect and every return to the foreground, and nearly always nothing has
// changed: this is what makes asking cost a few bytes instead of the whole
// document, encoded here and decoded there, to learn that.
func (p *Plugin) handleStreams(w http.ResponseWriter, r *http.Request) {
	p.snapshotLock.RLock()
	current, tag := p.snapshot, p.snapshotTag
	p.snapshotLock.RUnlock()

	// Kept by the reader, checked every time: it is the same for everybody
	// who may ask, but never something a proxy in between should hand out.
	w.Header().Set("Cache-Control", "private, no-cache")

	if unchangedSince(w, r, tag) {
		return
	}

	if current == nil {
		current = &snapshot{Streams: []apiStream{}, People: map[string]personView{}, Banners: p.banners().word()}
	}
	writeJSON(w, http.StatusOK, current)
}

// accessResponse is the least a client needs to decide whether it may go on
// playing: see handleAccess.
type accessResponse struct {
	Access     string `json:"access"`
	Configured bool   `json:"configured"`

	// Which contract this server speaks and which version it is, as on /me:
	// this is the one answer every client reads, so it is where a client
	// learns whether the two are a pair.
	Contract int    `json:"contract"`
	Version  string `json:"version"`

	// What this server calls the plugin (brandName), and which artwork it
	// has, as on /me: what the lock screen draws before anything else.
	Name    string       `json:"name"`
	Artwork brandArtwork `json:"artwork"`

	// Whether this plugin's files are a signed release (release_check.go),
	// so a client knows whether to look at GET /api/v1/release.
	Release releaseWord `json:"release"`

	// What the app check is set to, and what it found for the caller's
	// session (app_check.go), so Matty knows whether to prove itself.
	App appWord `json:"app"`

	// Whether a Matty may register for push notifications here, with the
	// key to register with, or why not (push.go). From 4.1.0. From memory:
	// no store read.
	Push pushWord `json:"push"`

	// Whether a Matty may talk in the voice server's channels from here, or
	// why not (voice.go). From 4.3.0, and only while voice is switched on.
	// From memory: no store read.
	Voice *voiceWord `json:"voice,omitempty"`
}

// handleAccess answers GET /api/v1/access: the caller's access, and which
// plugin is answering, and nothing else. Nothing can end a playing stream from outside (the relay only carries
// its start), so the rule is that a player asks, at least once a minute while
// anything plays, whether it may still watch, and stops when the answer is
// locked. Asking /me for that reads every connected-account document and
// builds a page of settings each time; this is the same answer to the one
// question, from the one read it needs.
func (p *Plugin) handleAccess(w http.ResponseWriter, userID, sessionID string) {
	cfg := p.getConfiguration()

	w.Header().Set("Cache-Control", "no-store")

	response := accessResponse{
		Access:     accessUnknown,
		Configured: cfg.validate() == nil,
		Contract:   contractRevision,
		Version:    manifestVersion,
		Name:       p.brandName(),
		Artwork:    p.artwork(),
		Release:    p.releaseWordNow(),
		App:        p.appWordFor(sessionID, userID, cfg),
		Push:       p.pushWordNow(cfg),
		Voice:      p.voiceWordNow(cfg),
	}

	// A store that did not answer is "unknown", never a guess, as on /me: a
	// player keeps playing and asks again.
	level, _, err := p.accessOf(userID)
	if err != nil {
		p.API.LogWarn("Could not read somebody's access to Matty", "error", err.Error())
	} else {
		response.Access = level.String()
	}

	writeJSON(w, http.StatusOK, response)
}

// handleMe is /me for a caller with no session to speak of, as tests ask.
func (p *Plugin) handleMe(w http.ResponseWriter, userID string) {
	p.handleMeIn(w, userID, "")
}

// handleMeIn answers GET /api/v1/me for the caller's session.
func (p *Plugin) handleMeIn(w http.ResponseWriter, userID, sessionID string) {
	cfg := p.getConfiguration()

	w.Header().Set("Cache-Control", "no-store")

	response := meResponse{
		UserID:      userID,
		Contract:    contractRevision,
		Version:     manifestVersion,
		Name:        p.brandName(),
		Artwork:     p.artwork(),
		Release:     p.releaseWordNow(),
		App:         p.appWordFor(sessionID, userID, cfg),
		Access:      accessUnknown,
		BadgeOn:     cfg.EnableLiveBadge,
		AutoLink:    cfg.AutoLinkByUsername,
		StyleCode:   cfg.StyleCodeBlocks,
		OpenPanel:   cfg.openPanel(),
		NoSignal:    cfg.noSignalGhost(),
		BannerText:  cfg.BannerText,
		BannerColor: cfg.BannerColor,
		BannerPlace: cfg.bannerPlacement(),
		Configured:  cfg.validate() == nil,
	}

	if user, err := p.API.GetUser(userID); err == nil && user != nil {
		response.Username = user.Username
	}

	// Read once, fresh, and every field that depends on it follows this one
	// answer. A store that did not answer is "unknown", never a guess: the
	// webapp keeps what it last knew.
	level, key, err := p.accessOf(userID)
	if err != nil {
		p.API.LogWarn("Could not read somebody's access to Matty", "error", err.Error())
	} else {
		response.Access = level.String()
	}

	if err == nil && level == accessLinked {
		response.StreamKey = key
		response.LinkedLive = p.linkedKeyIsLive(key)
		response.ClipsStreamer = p.clipsStreamerFor(key)
		response.PopOut = cfg.PublicSiteURL != ""

		media := cfg.mediaFlags()
		response.Media = &media

		// This node's copy, and no when it cannot be read: it only words a
		// confirmation.
		alsoLetIn, letInErr := p.letInHas(userID)
		alsoLetIn = alsoLetIn && letInErr == nil
		response.AlsoLetIn = &alsoLetIn
	}

	admin := p.isSystemAdmin(userID)
	response.Twitch = p.meTwitchFor(userID, cfg, admin)
	response.Steam = p.meAccountFor(steamProvider, userID, cfg, admin)
	response.Hytale = p.meAccountFor(hytaleProvider, userID, cfg, admin)

	if admin {
		response.IsAdmin = true
		response.AirtimeAlert = p.airtimeStatus()

		if cfg.EnableVoice {
			if state := p.getVoice(); !state.connected && state.problem != "" {
				response.VoiceAlert = state.problem
			}
		}

		if cfg.EnableBirthdays {
			response.BirthdayAlert = p.birthdayAlert()
		}

		response.ClipsAlert = p.clipsAlertFor(cfg)
		response.LetInAlert, _ = p.letInProblem()

		response.SoundAlert = p.soundAlertFor(cfg)
		response.AchievementsAlert = p.achievementsAlertFor(cfg)
		response.LedgerAlert = p.ledgerAlertFor()
		response.ReleaseAlert = p.releaseAlertNow()
		response.PushAlert = p.pushAlertFor(cfg)
		response.VoiceTalkAlert = p.voiceTalkAlertFor(cfg)
	}

	writeJSON(w, http.StatusOK, response)
}

// handleBotAvatar puts an uploaded image on the plugin's bot.
//
// Admin-gated the same way the airtime tools are: this changes something every
// member sees beside every relayed message.
func (p *Plugin) handleBotAvatar(w http.ResponseWriter, r *http.Request, userID string) {
	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, "Only a system admin can change the bot avatar.")
		return
	}

	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	if r.Method == http.MethodGet {
		// Who the bot is, so the settings page can show the picture it is
		// wearing and the name it is actually going by.
		//
		// Read from the account rather than from the setting: a rename that
		// Mattermost refused leaves the two disagreeing, and the one worth
		// showing is the one on the account.
		cfg := p.getConfiguration()

		identity := map[string]any{
			"userId":      p.getBotUserID(),
			"username":    cfg.BotUsername,
			"displayName": cfg.BotDisplayName,
			"description": cfg.BotDescription,
		}

		if botID := p.getBotUserID(); botID != "" {
			if user, appErr := p.API.GetUser(botID); appErr == nil && user != nil {
				identity["username"] = user.Username
				identity["displayName"] = user.GetDisplayName(model.ShowFullName)
			}

			// The description lives on the bot record rather than the user, so
			// it takes a second call. Worth it for the same reason as the other
			// two: what is shown here is what the ACCOUNT says, which is not
			// always what the boxes above it say.
			if bot, appErr := p.API.GetBot(botID, false); appErr == nil && bot != nil {
				identity["description"] = bot.Description
			}
		}

		if problem := p.botProblemNow(); problem != "" {
			identity["error"] = problem
		}

		// The account in the way of the configured name, when the plugin may
		// take it over — or why it may not. Only worked out when the name the
		// admin asked for is not the one in use, which is the only time there
		// is anything in the way.
		if identity["username"] != cfg.BotUsername {
			candidate, adoptErr := p.adoptionCandidate(cfg.BotUsername)
			switch {
			case adoptErr != nil:
				identity["adoptBlocked"] = adoptErr.Error()
			case candidate != nil:
				identity["adoptable"] = candidate
			}
		}

		writeJSON(w, http.StatusOK, identity)

		return
	}

	image, err := io.ReadAll(io.LimitReader(r.Body, maxAvatarBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Could not read the image.")
		return
	}

	if len(image) > maxAvatarBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "That image is too large; 2 MB is the limit.")
		return
	}

	if err := p.setBotAvatar(image); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleTheme reports what is wrong with the configured theme, and applies it
// to everybody when an admin asks.
func (p *Plugin) handleTheme(w http.ResponseWriter, r *http.Request, userID string) {
	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, "Only a system admin can change everybody's theme.")
		return
	}

	theme, whitened, err := parseTheme(p.getConfiguration().Theme)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{
			"theme":    theme,
			"whitened": whitened,
		})

		return
	}

	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	applied, err := p.applyThemeToEveryone(theme)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"applied": applied})
}

// handleLink is the lock screen's door, and the footer's Relink and Unlink:
// a bearer token that Broadcast Box accepts links the caller to its stream,
// and an empty one unlinks. Every refusal carries a code the lock screen words
// for itself; the footer shows the text. The token itself is never logged,
// never echoed and never put anywhere but the link_token_ entry.
// handleUnlink answers DELETE /api/v1/me/link: the caller's stream is no
// longer linked to their account. The answer says what their access is now
// (let in, or locked), as a link's does.
func (p *Plugin) handleUnlink(w http.ResponseWriter, userID string) {
	// Nothing of theirs to take back: say what their access is, and do
	// nothing. No meter stands in front of this route, so a request that
	// changes nothing must cost nothing either: no store written, no poll of
	// Broadcast Box asked for, nobody told. (A read that fails falls through,
	// and the unlink below reports it.)
	if held, err := p.holdsLinkEntries(userID); err == nil && !held {
		writeJSON(w, http.StatusOK, p.linked(userID, ""))
		return
	}

	if err := p.clearLink(userID); err != nil {
		p.API.LogWarn("Could not unlink a stream", "user_id", userID, "error", err.Error())
		writeCodedError(w, http.StatusInternalServerError, "Could not unlink your stream.", accessCodeStore)
		return
	}

	// Saying what is on is for linked streamers, so unlinking takes it
	// down too. Not a reason to refuse the unlink if it cannot: the cards
	// only show it on a stream a link proves is theirs, and the poller
	// takes it down once that stream has been gone for the grace.
	if err := p.clearNowPlaying(userID); err != nil {
		p.API.LogWarn("Could not take down what is on after an unlink", "user_id", userID, "error", err.Error())
	}

	p.refreshSoon()
	p.clipsLinkChanged(userID)
	p.accessChanged(userID)
	writeJSON(w, http.StatusOK, p.linked(userID, ""))
}

// What a link that is refused says. These are the wording, for the lock
// screen and for Matty alike: each shows the sentence it is sent, and keeps
// words of its own only for no answer at all. (Both used to keep this very
// table, word for word, over sentences of the server's that nobody saw.) What
// went wrong behind a sentence is in the log, never in it: which setting is
// missing, or how Broadcast Box failed, is an admin's to read.
const (
	linkUnreadable  = "That did not get through. Try again."
	linkTooLong     = "That is too long to be a bearer token."
	linkUnreachable = "Broadcast Box is not answering, so your token can’t be checked. Try again later."
	linkNotAccepted = "That token was not accepted. Check it and try again."
	linkBadName     = "Your token was accepted, but its stream name can’t be used here. Ask an admin to look at it."
	linkNotSaved    = "Your token was accepted but could not be saved. Try again in a moment."
	linkTooMany     = "Too many tries. Wait a few minutes, then try again."
)

// linkNotSetUp is the refusal while Broadcast Box is not set up, with the
// server's name for the plugin (brandName).
func linkNotSetUp(name string) string {
	return name + " is not set up yet, so it can’t be unlocked."
}

func (p *Plugin) handleLink(w http.ResponseWriter, r *http.Request, userID string) {
	cfg := p.getConfiguration()

	var request linkRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&request); err != nil {
		writeCodedError(w, http.StatusBadRequest, linkUnreadable, codeBadRequest)
		return
	}

	token := cleanStreamKey(request.Token)

	// Nothing to check. Both clients catch an empty bar before sending
	// anything, so a token that is empty only once the server has cleaned it
	// did not get through as it was meant to: said as the lock said it
	// before the server's sentence was shown. (Unlinking is DELETE on this
	// path, which no meter stands in front of: handleUnlink.)
	if token == "" {
		writeCodedError(w, http.StatusBadRequest, linkUnreadable, codeBadRequest)
		return
	}

	if utf8.RuneCountInString(token) > 256 {
		writeCodedError(w, http.StatusBadRequest, linkTooLong, "too_long")
		return
	}

	if err := cfg.validate(); err != nil {
		writeCodedError(w, http.StatusServiceUnavailable, linkNotSetUp(p.brandName()), codeNotConfigured)
		return
	}

	// Holding the token is the proof of ownership. Broadcast Box tells us which
	// stream it belongs to, so nobody can claim a stream by typing its name.
	profile, err := p.fetchProfileByToken(r.Context(), cfg, token)
	if err != nil {
		p.API.LogWarn("Could not verify a publish token", "user_id", userID, "error", err.Error())
		writeCodedError(w, http.StatusBadGateway, linkUnreachable, codeUnreachable)
		return
	}
	if profile == nil || profile.StreamKey == "" {
		p.API.LogWarn("Publish token was not recognised", "user_id", userID)
		writeCodedError(w, http.StatusForbidden, linkNotAccepted, "not_accepted")
		return
	}

	if !validStreamKey(profile.StreamKey) {
		p.API.LogWarn("Broadcast Box returned an unusable stream key", "user_id", userID)
		writeCodedError(w, http.StatusBadGateway, linkBadName, "bad_name")
		return
	}

	if err := p.setLink(userID, profile.StreamKey, token); err != nil {
		p.API.LogWarn("Could not save a stream link", "user_id", userID, "error", err.Error())
		writeCodedError(w, http.StatusInternalServerError, linkNotSaved, accessCodeStore)
		return
	}

	p.refreshSoon()
	p.clipsLinkChanged(userID)
	p.accessChanged(userID)
	writeJSON(w, http.StatusOK, p.linked(userID, profile.StreamKey))
}

// Editing a stream profile writes to Broadcast Box, so it requires an explicit
// link rather than the username auto-match. Mattermost usernames are
// self-service, and treating one as proof of ownership would let anyone rename
// themselves into control of somebody else's stream.
func (p *Plugin) claimedToken(userID string) string {
	return p.getLinkedToken(userID)
}

func (p *Plugin) handleGetProfile(w http.ResponseWriter, r *http.Request, userID string) {
	cfg := p.getConfiguration()

	if err := cfg.validate(); err != nil {
		writeJSON(w, http.StatusOK, profileResponse{Message: err.Error()})
		return
	}

	token := p.claimedToken(userID)
	if token == "" {
		writeJSON(w, http.StatusOK, profileResponse{Message: "Link your stream first."})
		return
	}

	profile, err := p.fetchProfileByToken(r.Context(), cfg, token)
	if err != nil {
		p.API.LogWarn("Could not read the stream profile", "user_id", userID, "error", err.Error())
		writeJSON(w, http.StatusOK, profileResponse{Message: clientSafeError(err)})
		return
	}
	if profile == nil {
		writeJSON(w, http.StatusOK, profileResponse{Message: "Broadcast Box has no reserved profile for your stream key."})
		return
	}

	motd := profile.MOTD
	if isDefaultMOTD(motd, profile.StreamKey) {
		motd = ""
	}

	writeJSON(w, http.StatusOK, profileResponse{Available: true, MOTD: motd, IsPublic: profile.IsPublic})
}

func (p *Plugin) handleSaveProfile(w http.ResponseWriter, r *http.Request, userID string) {
	cfg := p.getConfiguration()

	if err := cfg.validate(); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}

	token := p.claimedToken(userID)
	if token == "" {
		writeError(w, http.StatusForbidden, "Link your stream before editing your stream info.")
		return
	}

	var request saveProfileRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "Could not read the request.")
		return
	}

	// The title box sits right beside Relink, which asks for the publish
	// token to be pasted, and what is typed here is on every card: their own
	// token and stream key never reach Broadcast Box, let alone the cards.
	motd := strings.TrimSpace(p.withoutOwnKey(userID, request.MOTD))
	if utf8.RuneCountInString(motd) > maxMOTDLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("Keep the stream note to %d characters or fewer.", maxMOTDLength))
		return
	}

	// isPublic is deliberately not taken from the request. Unlisting removes a
	// stream from /api/status entirely, which would hide the streamer from this
	// panel and silently drop their live badge.
	if err := p.saveProfile(r.Context(), cfg, token, motd, true); err != nil {
		p.API.LogWarn("Could not save the stream profile", "user_id", userID, "error", err.Error())
		writeError(w, http.StatusBadGateway, clientSafeError(err))
		return
	}

	// The list carries the note, so refresh instead of waiting for the tick.
	p.refreshSoon()

	writeJSON(w, http.StatusOK, profileResponse{Available: true, MOTD: motd, IsPublic: true})
}

// handleWHEP proxies the SDP exchange so the browser never has to reach
// Broadcast Box directly. Only keys that are currently live are forwarded, so
// the endpoint cannot be used as an open relay.
//
// Only the answer goes back. Broadcast Box's other headers — the Location
// naming the session, the Link headers for its layer and event endpoints — are
// addresses on an origin the browser never talks to, and the session id in them
// is enough on its own to renegotiate somebody's session there.
// The codes of a playback that is refused, besides the gate's own and
// "rate_limited": what a player does next differs by which it was. "not_live"
// is worth asking again in a moment, "bad_offer" never is.
const (
	whepCodeNotLive  = "not_live"
	whepCodeBadOffer = "bad_offer"

	whepBadOffer = "This player asked for the stream in a way Broadcast Box can't serve: it needs to ask for the sound and the picture together."

	// A real offer is under 10 KB. Anything much longer is refused before it
	// is parsed, so that a body of a megabyte costs a read and nothing more.
	maxWHEPOfferBytes = 64 << 10 // 64 KiB
)

func (p *Plugin) handleWHEP(w http.ResponseWriter, r *http.Request, userID string) {
	cfg := p.getConfiguration()

	if err := cfg.validate(); err != nil {
		writeCodedError(w, http.StatusServiceUnavailable, err.Error(), codeNotConfigured)
		return
	}

	handle := strings.TrimSpace(r.Header.Get("X-Stream-Handle"))
	if handle == "" {
		writeCodedError(w, http.StatusBadRequest, "Missing stream handle.", codeBadRequest)
		return
	}

	// One byte over the limit is read so that an offer that is too long is
	// told from one that only just fits.
	offer, err := io.ReadAll(io.LimitReader(r.Body, maxWHEPOfferBytes+1))
	if err != nil || len(offer) == 0 {
		writeCodedError(w, http.StatusBadRequest, "Missing the WebRTC offer.", codeBadRequest)
		return
	}

	// Before anything is asked of Broadcast Box: an offer it cannot serve is
	// not passed on (whepOfferServable says why that matters). One far longer
	// than any player writes is not even read.
	if len(offer) > maxWHEPOfferBytes || !whepOfferServable(string(offer)) {
		p.API.LogWarn("A playback offer was not passed on to Broadcast Box: it did not ask for one audio and one video stream to receive", "user_id", userID)
		writeCodedError(w, http.StatusBadRequest, whepBadOffer, whepCodeBadOffer)
		return
	}

	streamKey := p.streamKeyForHandle(handle)
	if streamKey == "" {
		// No upstream work for an unknown handle: polling the origin here was an
		// unmetered way for any signed-in user to drive load on it. Queue a
		// refresh instead, which is coalesced, and let the panel retry.
		p.refreshSoon()
		writeCodedError(w, http.StatusNotFound, "That stream is not live right now.", whepCodeNotLive)
		return
	}

	// A session opened only to take a preview still says so, with the lease
	// it was given for it. Checked, and used up, BEFORE the exchange: the
	// exchange can take seconds, and a lease that was good when the offer
	// arrived should not lose its peek to a lease running out meanwhile. A
	// header that does not check out is not an error — the session is simply
	// counted as a viewer, which is what it would have been anyway. Using the
	// lease up is also what lets the still be uploaded under it afterwards.
	peek := false
	if token := strings.TrimSpace(r.Header.Get(peekHeader)); token != "" && cfg.LivePreviews {
		peek = p.stills().takePeek(handle, userID, token, time.Now())
	}

	answer, sessionID, err := p.exchangeWHEP(r.Context(), cfg, streamKey, string(offer))
	if err != nil {
		p.API.LogWarn("WHEP exchange failed", "stream_handle", handle, "error", err.Error())
		writeCodedError(w, http.StatusBadGateway, clientSafeError(err), codeUnreachable)
		return
	}

	// Whose session this is, which Broadcast Box will never know: a peek so
	// it can be left out of the count, anybody else so their face can go on
	// the card. Only after an exchange that worked, since a refused offer
	// opened no session to put a face on.
	//
	// With faces off, the Top Snoopers board still needs to know whose session
	// it is, but only for somebody it could count: a person who has linked
	// their stream, both link entries agreeing as the gate requires. Nobody
	// else's watching is remembered at all, a let-in person's included.
	if peek {
		p.stills().rememberPeek(handle, userID, sessionID, time.Now())
	} else if cfg.ShowWatchers || (cfg.TopWatchers && p.hasLink(userID)) {
		p.watchers().record(sessionID, userID, handle, time.Now())
	}

	// Apart from the faces and the boards, and whatever they are set to:
	// clipping a stream is for somebody watching it (A§3.4). Everybody's
	// session, linked or not, since whether they are linked is asked when they
	// clip; never a peek, which is nobody watching. Whether Clips is on or
	// not, too: a panel that is already playing never offers again, so a
	// session noted only while Clips was on would leave everybody watching
	// when an admin switched it on told to tune in to a stream they can see
	// playing.
	if !peek {
		p.noteClipViewer(sessionID, userID, handle, p.clips().now())
	}

	w.Header().Set("Content-Type", "application/sdp")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, answer)
}

// whepOfferServable is whether a viewer's offer is one Broadcast Box can be
// handed: exactly one audio and one video section, both offered and both
// saying, themselves, that they receive.
//
// Broadcast Box adds an audio and a video track to every viewer's session
// whatever the offer says, and from then on hands that session every packet
// of the stream. For an offer with no video section, or one marked inactive,
// its answer leaves the video track out, the track is never started, and
// Broadcast Box 2.0.2 then writes to it at the stream's next key frame: a nil
// pointer in the publisher's own goroutine, with no recover anywhere, which
// ends the whole process and every stream and viewer with it. Later versions
// check, and instead ask the publisher for a key frame hundreds of times a
// second for as long as such a viewer listens. Neither client ever sends such
// an offer (both ask for audio then video, recvonly), but this route relays
// whatever a signed-in person who may watch posts to it, so one request could
// take Broadcast Box down for everybody. Found by reading Broadcast Box's
// source; nobody has tried it on a real one.
//
// So the offer is read as Broadcast Box's own pion reads it, by pion's SDP
// parser, and the rules are stricter than pion's wherever pion versions
// differ:
//
//   - An offer that does not parse is refused, and so is one with a carriage
//     return that does not end a line (pion's parser skips one, so the line
//     after it would read differently here and there).
//   - Every audio and every video section counts, a turned-down one too:
//     there must be exactly one of each. Each must be offered: a port other
//     than 0, or port 0 riding another's transport (a=bundle-only).
//   - Each one's own first direction attribute decides, as pion's
//     getPeerDirection reads it (the attribute's key, before any colon, and
//     the first one wins). It must be recvonly or sendrecv. A section that
//     says no direction of its own is refused, whatever the session says:
//     pion 4.2.6 skips such a section and leaves its track unbound, later
//     versions take the session's.
//
// An offer's own section for something else (a data channel) is nobody's
// business here.
func whepOfferServable(offer string) bool {
	if strings.Contains(strings.ReplaceAll(offer, "\r\n", ""), "\r") {
		return false
	}

	var description sdp.SessionDescription
	if err := description.UnmarshalString(offer); err != nil {
		return false
	}

	audio, video := 0, 0
	for _, section := range description.MediaDescriptions {
		kind := section.MediaName.Media
		if kind != "audio" && kind != "video" {
			continue
		}
		if kind == "audio" {
			audio++
		} else {
			video++
		}

		_, bundleOnly := section.Attribute("bundle-only")
		if section.MediaName.Port.Value == 0 && !bundleOnly {
			return false
		}

		direction := ""
		for _, attribute := range section.Attributes {
			if key := attribute.Key; key == "sendrecv" || key == "sendonly" || key == "recvonly" || key == "inactive" {
				direction = key
				break
			}
		}
		if direction != "recvonly" && direction != "sendrecv" {
			return false
		}
	}

	return audio == 1 && video == 1
}

// linkedKeyIsLive reports whether the stream a user linked is live right now.
// Broadcast Box streams only, and never for no key: a Twitch entry has an
// empty key, which a link read that failed (getLinkedKey makes that "") would
// otherwise match.
func (p *Plugin) linkedKeyIsLive(linked string) bool {
	wanted := normalizeKey(linked)
	if wanted == "" {
		return false
	}

	for _, stream := range p.getSnapshot().Streams {
		if stream.Source != "" {
			continue
		}
		if normalizeKey(stream.StreamKey) == wanted {
			return true
		}
	}

	return false
}
