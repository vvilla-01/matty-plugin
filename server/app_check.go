package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/plugin"

	"github.com/vvilla-01/matty-plugin/server/appcheck"
	"github.com/vvilla-01/matty-plugin/server/release"
)

// The app check: Matty proves to this server, on Android's word rather than
// its own, that it is the official build, signed with Matty's certificate,
// on a phone whose bootloader is locked and whose system is verified. It is
// Android's hardware key attestation (server/appcheck reads it), asked for
// with a challenge from here and bound to the session that asks:
//
//  1. POST /api/v1/app/challenge gives a challenge, kept for five minutes
//     for the caller's session.
//  2. The app makes a key in the phone's Keystore with, as its attestation
//     challenge, appcheck.DeriveChallenge of the origin it reaches this
//     server at, the user's id and the challenge, and sends the chain
//     Keystore gives it to POST /api/v1/app/attest.
//  3. The challenge is spent, the origin is held to the Site URL's, and the
//     chain is verified against a challenge derived here, from this server's
//     own origin and the session's user. The verdict is kept for the session.
//
// GET /api/v1/access and /me say, for the caller's session, what the setting
// is and what was found (appWord). With the setting at "require", a request
// that says it is Matty (its User-Agent) and whose session has not passed is
// refused on every route but the five a Matty needs to prove itself and to
// show why it cannot (passAppCheckGate). Whatever else asks, the web panel
// included, is never refused by it: nothing Mattermost gives a plugin tells
// the panel from a program that hides what it is.
//
// It contacts nothing: everything it checks against is built in.

const (
	appChallengePath = "/api/v1/app/challenge"
	appAttestPath    = "/api/v1/app/attest"
	adminAppCheck    = "/api/v1/admin/appcheck"

	// mattyPackage is the official app's package name.
	mattyPackage = "ceo.villa.matty"

	// mattyUserAgent begins every request Matty makes ("Matty/0.1.27
	// (Android 17; Mattermost client)").
	mattyUserAgent = "Matty/"

	// codeAppUnverified is the refusal of a Matty whose session has not
	// passed, while the setting is "require".
	codeAppUnverified = "app_unverified"

	// The most an attestation request may carry: a chain is a few KB.
	maxAttestBody = 64 << 10
)

// appCheckExempt are the routes a Matty that has not passed may still use:
// what its lock screen shows, whether this is a signed release, and the two
// it proves itself with.
var appCheckExempt = map[string]bool{
	"/api/v1/access":  true,
	"/api/v1/me":      true,
	"/api/v1/release": true,
	appChallengePath:  true,
	appAttestPath:     true,
}

// appCheckExemptRequest is whether a request is one a Matty that has not
// passed may still make: an exempt route, or one of the takebacks that always
// work, its own push registration (DELETE /me/push) and leaving voice (POST
// /voice/leave).
func appCheckExemptRequest(r *http.Request) bool {
	return appCheckExempt[r.URL.Path] ||
		(r.Method == http.MethodDelete && r.URL.Path == mePushPath) ||
		(r.Method == http.MethodPost && r.URL.Path == voiceLeavePath)
}

// appWord is what /access and /me say about the caller's session.
type appWord struct {
	// Check is the setting: "off", "report" or "require".
	Check string `json:"check"`

	// State is "verified", "unverified" (checked, and failed: Reason says
	// why) or "none" (not checked: never, or no longer, or the check is off).
	State  string `json:"state"`
	Reason string `json:"reason,omitempty"`

	// Development is set beside verified when only the development build
	// would pass it (an emulator, a debug build).
	Development bool `json:"development,omitempty"`

	// VersionCode is the version the verdict was for, as Android attested
	// it, so the app knows to prove itself again once it is updated.
	VersionCode int64 `json:"versionCode,omitempty"`

	// Minimum is the oldest Matty this server answers, while one is set,
	// whatever the check's setting (app_version.go).
	Minimum string `json:"minimum,omitempty"`
}

func sessionOf(c *plugin.Context) string {
	if c == nil {
		return ""
	}

	return c.SessionId
}

// appWordFor is the caller's appWord. A store that cannot be read is said as
// none, with why: /access never fails over this.
func (p *Plugin) appWordFor(sessionID, userID string, cfg *configuration) appWord {
	word := appWord{Check: cfg.appCheckMode(), State: "none", Minimum: cfg.appMinimum()}
	if word.Check == appCheckOff {
		return word
	}

	result, stale, err := p.resultFor(sessionID, userID, cfg)
	switch {
	case err != nil:
		word.Reason = "This server could not read its app check records just now."
	case stale:
		word.Reason = appCheckRulesChangedText
	case result == nil:
	case result.Verified:
		word.State = "verified"
		word.Development = result.Development
		word.VersionCode = result.VersionCode
	default:
		word.State = "unverified"
		word.Reason = result.Reason
		word.VersionCode = result.VersionCode
	}

	return word
}

// passAppCheckGate refuses a Matty whose session has not passed, while the
// setting is "require". True when the request may go on; otherwise it has
// answered. Every route but the exempt ones, the clips included, comes
// through here.
func (p *Plugin) passAppCheckGate(w http.ResponseWriter, r *http.Request, c *plugin.Context, userID string) bool {
	if appCheckExemptRequest(r) || !strings.HasPrefix(r.UserAgent(), mattyUserAgent) {
		return true
	}

	cfg := p.getConfiguration()
	if cfg.appCheckMode() != appCheckRequire {
		return true
	}

	result, stale, err := p.resultFor(sessionOf(c), userID, cfg)
	if err != nil {
		// Coded `store`, as any record that could not be read: worth asking
		// again, and never push being off.
		w.Header().Set("Cache-Control", "no-store")
		writeCodedError(w, http.StatusServiceUnavailable, "This server could not read its app check records. Try again in a moment.", accessCodeStore)

		return false
	}
	if result != nil && result.Verified {
		return true
	}

	message := p.brandName() + " answers only the official Matty app, and this copy has not been checked yet."
	switch {
	case stale:
		message = p.brandName() + " answers only the official Matty app, and this copy has to be checked again: this server's rules for the check have changed."
	case result != nil:
		message = p.brandName() + " answers only the official Matty app, and this copy did not pass the check. " + result.Reason
	}

	w.Header().Set("Cache-Control", "no-store")
	writeCodedError(w, http.StatusForbidden, message, codeAppUnverified)

	return false
}

// appCheckPolicy is what this build accepts, with the settings' additions.
func (p *Plugin) appCheckPolicy(cfg *configuration) (appcheck.Policy, error) {
	roots, err := appcheck.GoogleRoots()
	if err != nil {
		return appcheck.Policy{}, err
	}
	if p.appCheckRoots != nil {
		roots = p.appCheckRoots
	}
	revoked, err := appcheck.RevokedSerials()
	if err != nil {
		return appcheck.Policy{}, err
	}
	bootKeys, err := appcheck.GrapheneOSBootKeys()
	if err != nil {
		return appcheck.Policy{}, err
	}

	digests, _ := digestLines(cfg.AppCheckCertificates)
	digests[release.OfficialRSASHA256] = true

	added, _ := digestLines(cfg.AppCheckBootKeys)
	for key := range added {
		bootKeys[key] = true
	}

	return withDevelopmentAppCheck(appcheck.Policy{
		Roots:    roots,
		Packages: map[string]bool{mattyPackage: true},
		Digests:  digests,
		BootKeys: bootKeys,
		Revoked:  revoked,
	}), nil
}

// The app check's own refusals, which are not about the app.
const (
	appCheckOffText     = "This server does not check the app."
	appCheckSessionText = "This request carries no Mattermost session to check. Sign in again."
	appCheckStaleText   = "That challenge is not one this server gave this session, was used already, or is more than five minutes old. Ask for a new one."
	appCheckStoreText   = "This server could not keep the app check just now. Try again in a moment."
	appCheckTooManyText = "Too many app checks. Wait a minute and try again."

	// appCheckRulesChangedText is /access's and /me's reason beside "none"
	// for a verdict that stopped counting (stillCounts). The app draws it
	// with nothing before it, so it says on its own why nothing is held.
	appCheckRulesChangedText = "This server's rules for the app check have changed, and this Matty is to be checked under the new ones."
)

// handleAppChallenge answers POST /api/v1/app/challenge.
func (p *Plugin) handleAppChallenge(w http.ResponseWriter, c *plugin.Context, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if p.getConfiguration().appCheckMode() == appCheckOff {
		writeError(w, http.StatusForbidden, appCheckOffText)
		return
	}
	sessionID := sessionOf(c)
	if sessionID == "" {
		writeError(w, http.StatusUnauthorized, appCheckSessionText)
		return
	}

	s := p.appCheck()
	now := s.now()
	if !s.challengeLimiter.allowAt(userID, now) {
		writeTooMany(w, appCheckTooManyText, s.challengeLimiter, userID, now)
		return
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, appCheckStoreText)
		return
	}
	challenge := base64.StdEncoding.EncodeToString(raw)

	if err := p.keepChallenge(sessionID, hashHex(challenge), now); err != nil {
		writeError(w, http.StatusServiceUnavailable, appCheckStoreText)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"challenge": challenge,
		"expires":   now.Add(appCheckChallengeSeconds * time.Second).UnixMilli(),
	})
}

// attestRequest is POST /api/v1/app/attest's body.
type attestRequest struct {
	Challenge string          `json:"challenge"`
	Origin    string          `json:"origin"`
	Chain     []string        `json:"chain"`
	Claims    *appCheckClaims `json:"claims"`
}

// attestResponse is its answer: the verdict, as /access will say it.
type attestResponse struct {
	State       string `json:"state"`
	Reason      string `json:"reason,omitempty"`
	Development bool   `json:"development,omitempty"`
	VersionCode int64  `json:"versionCode,omitempty"`
	Expires     int64  `json:"expires"`
}

// handleAppAttest answers POST /api/v1/app/attest.
func (p *Plugin) handleAppAttest(w http.ResponseWriter, r *http.Request, c *plugin.Context, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	cfg := p.getConfiguration()
	if cfg.appCheckMode() == appCheckOff {
		writeError(w, http.StatusForbidden, appCheckOffText)
		return
	}
	sessionID := sessionOf(c)
	if sessionID == "" {
		writeError(w, http.StatusUnauthorized, appCheckSessionText)
		return
	}

	s := p.appCheck()
	now := s.now()
	if !s.attestLimiter.allowAt(userID, now) {
		writeTooMany(w, appCheckTooManyText, s.attestLimiter, userID, now)
		return
	}

	var request attestRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAttestBody)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "The app check's request could not be read.")
		return
	}
	chain, problem := decodeChain(request)
	if problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}

	// Spent first, whatever comes of it: a challenge answers once.
	switch err := p.spendChallenge(sessionID, hashHex(request.Challenge), now); {
	case errors.Is(err, errAppCheckStale):
		writeError(w, http.StatusConflict, appCheckStaleText)
		return
	case err != nil:
		writeError(w, http.StatusServiceUnavailable, appCheckStoreText)
		return
	}

	// The verdict lasts as long as the session, and 30 days at most.
	session, appErr := p.API.GetSession(sessionID)
	if appErr != nil || session == nil || session.UserId != userID {
		writeError(w, http.StatusUnauthorized, appCheckSessionText)
		return
	}
	expires := now.AddDate(0, 0, appCheckResultDays).UnixMilli()
	if session.ExpiresAt > 0 {
		expires = min(expires, session.ExpiresAt)
	}
	if expires <= now.UnixMilli() {
		writeError(w, http.StatusUnauthorized, appCheckSessionText)
		return
	}

	result := &appCheckResult{User: userID, At: now.UnixMilli(), Expires: expires, Claims: cleanClaims(request.Claims)}
	// The rules it is judged by, from the same settings as the judging. When
	// this build's lists do not read, judge says so, and none are kept.
	result.Rules, _ = p.appCheckRules(cfg)
	p.judge(result, request, chain, cfg, userID, now)

	if err := p.keepResult(sessionID, result, now); err != nil {
		writeError(w, http.StatusServiceUnavailable, appCheckStoreText)
		return
	}

	p.API.LogDebug("App check", "user_id", userID, "verified", result.Verified, "development", result.Development,
		"reason", result.Reason, "level", result.Level, "boot_state", result.BootState)

	word := appWord{State: "unverified", Reason: result.Reason}
	if result.Verified {
		word = appWord{State: "verified", Development: result.Development}
	}
	writeJSON(w, http.StatusOK, attestResponse{
		State: word.State, Reason: word.Reason, Development: word.Development,
		VersionCode: result.VersionCode, Expires: result.Expires,
	})
}

// judge fills in the verdict: the origin first, then the attestation.
func (p *Plugin) judge(result *appCheckResult, request attestRequest, chain [][]byte, cfg *configuration, userID string, now time.Time) {
	site, err := appcheck.Origin(p.siteURL())
	if err != nil {
		result.Reason = "This server has no usable Site URL to check the app's address against. An admin sets it in System Console > Environment > Web Server."
		return
	}

	origin, err := appcheck.Origin(request.Origin)
	if err != nil || origin != site {
		shown := strings.TrimSpace(request.Origin)
		if err == nil {
			shown = origin
		}
		result.Reason = "The app reached this server at " + cutRunes(printableText(shown), 200) + ", but its Site URL is " + site + "."
		return
	}

	policy, err := p.appCheckPolicy(cfg)
	if err != nil {
		p.API.LogError("The app check's built-in lists do not read", "error", err.Error())
		result.Reason = "This server's app check is broken: its built-in lists do not read. An admin will find why in the server log."
		return
	}

	verdict := appcheck.Verify(chain, appcheck.DeriveChallenge(site, userID, request.Challenge), policy, now)

	result.Verified = verdict.Verified
	result.Reason = verdict.Reason
	result.Development = verdict.Verified && verdict.Facts.Development
	if verdict.Verified {
		result.Package = verdict.Facts.Package
		result.Digest = verdict.Facts.Digest
		result.BootKey = verdict.Facts.BootKey
		result.RevocationList = appcheck.RevocationListDate
	}
	result.VersionCode = verdict.Facts.VersionCode
	if verdict.Facts.AttestationVersion != 0 {
		result.Level = verdict.Facts.Level.String()
	}
	if verdict.Facts.BootState != appcheck.BootUnknown {
		result.BootState = verdict.Facts.BootState.String()
	}
}

// decodeChain is the request's chain as DER, or why it cannot be read.
func decodeChain(request attestRequest) ([][]byte, string) {
	challenge, err := base64.StdEncoding.DecodeString(request.Challenge)
	if err != nil || len(challenge) != 32 {
		return nil, "The app check's challenge is not one this server gives."
	}
	if len(request.Chain) == 0 || len(request.Chain) > appcheck.MaxChain {
		return nil, "The app check's request carries no usable certificate chain."
	}

	chain := make([][]byte, len(request.Chain))
	for i, encoded := range request.Chain {
		der, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(der) == 0 {
			return nil, "The app check's request carries a certificate that is not base64."
		}
		chain[i] = der
	}

	return chain, ""
}

// cleanClaims keeps the app's claims to a sensible size and to printable
// text, since they are shown to admins as they are.
func cleanClaims(claims *appCheckClaims) *appCheckClaims {
	if claims == nil {
		return nil
	}

	cleaned := &appCheckClaims{
		Package:       cutRunes(printableText(claims.Package), 255),
		VersionCode:   claims.VersionCode,
		CurrentSigner: cutRunes(printableText(claims.CurrentSigner), 128),
		Hybrid:        claims.Hybrid,
	}
	for i, entry := range claims.History {
		if i == 8 {
			break
		}
		cleaned.History = append(cleaned.History, cutRunes(printableText(entry), 128))
	}

	return cleaned
}

// printableText is text with what is not printable taken out.
func printableText(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}

	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}

		return r
	}, s)
}

// appCheckSessionView is one session's verdict on the admins' list. Stale
// is a verdict that no longer counts under what this server accepts
// (stillCounts): one of verified whose certificate, boot key or revocation
// list is no longer accepted, or one of not verified judged under rules that
// have changed since. It is shown as not verified, with why, as the gate and
// /access treat it.
type appCheckSessionView struct {
	Session string `json:"session"`
	Stale   bool   `json:"stale,omitempty"`
	appCheckResult
}

// A stale verdict's reason on the admins' list. "Asked" is /access and /me
// saying "none": Matty proves itself again when it reads that.
const (
	staleAppCheckReason           = "This was verified for a certificate, a boot key or a revocation list this server no longer accepts. The app is asked to prove itself again."
	staleUnverifiedAppCheckReason = "This failed the check under rules this server has changed since. The app is asked to prove itself again."
)

type appCheckPersonView struct {
	UserID   string                `json:"userId"`
	Username string                `json:"username,omitempty"`
	Sessions []appCheckSessionView `json:"sessions"`
}

// appCheckAdminView is GET /api/v1/admin/appcheck: every person with a
// verdict that has not expired, the latest first, and each of their
// sessions'. "claims" in each is the app's own word. Minimum is the oldest
// Matty this server answers, while one is set; Problem says which lines of
// the two lists of digests were left out, and that Minimum Matty version
// counts as none when it is not a version.
type appCheckAdminView struct {
	Check   string               `json:"check"`
	Minimum string               `json:"minimum,omitempty"`
	Problem string               `json:"problem,omitempty"`
	People  []appCheckPersonView `json:"people"`
}

// handleAdminAppCheck answers GET /api/v1/admin/appcheck, for system admins.
func (p *Plugin) handleAdminAppCheck(w http.ResponseWriter, r *http.Request, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, "Only a system admin can see the app check's results.")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	cfg := p.getConfiguration()
	now := p.appCheck().now()
	problem := strings.TrimSpace(cfg.appCheckProblem() + " " + cfg.appMinimumProblem())
	view := appCheckAdminView{Check: cfg.appCheckMode(), Minimum: cfg.appMinimum(), Problem: problem, People: []appCheckPersonView{}}

	people, err := p.readIndex(kvAppCheckPeople, now)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "This server could not read its app check records. Try again in a moment.")
		return
	}

	for _, person := range people {
		sessions, err := p.readIndex(kvAppCheckPersonPrefix+person, now)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "This server could not read its app check records. Try again in a moment.")
			return
		}

		entry := appCheckPersonView{UserID: person, Sessions: []appCheckSessionView{}}
		for _, sessionID := range sessions {
			result, err := p.readResult(sessionID, now)
			if err != nil {
				writeError(w, http.StatusServiceUnavailable, "This server could not read its app check records. Try again in a moment.")
				return
			}
			if result != nil && result.User == person {
				session := appCheckSessionView{Session: sessionID, appCheckResult: *result}
				session.Rules = ""
				if !p.stillCounts(result, cfg) {
					session.Stale = true
					session.Reason = staleUnverifiedAppCheckReason
					if result.Verified {
						session.Reason = staleAppCheckReason
					}
					session.Verified, session.Development = false, false
				}
				entry.Sessions = append(entry.Sessions, session)
			}
		}
		if len(entry.Sessions) == 0 {
			continue
		}
		sort.SliceStable(entry.Sessions, func(a, b int) bool { return entry.Sessions[a].At > entry.Sessions[b].At })

		if user, appErr := p.API.GetUser(person); appErr == nil && user != nil {
			entry.Username = user.Username
		}
		view.People = append(view.People, entry)
	}

	writeJSON(w, http.StatusOK, view)
}
