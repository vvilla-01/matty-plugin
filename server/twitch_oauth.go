package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/plugin"
)

// Connect Twitch (design/twitch-live.md §4): a person proves a Twitch channel
// is theirs by signing in on Twitch and approving VillaNET, the OAuth
// authorization code flow, asking for nothing but who they are (openid).
//
// Three requests make a connection, and each one guards the next:
//
//  1. POST connect-ticket, from the panel, on the first press of Connect
//     Twitch. A POST, so a cookie caller must carry Mattermost's CSRF token.
//     It hands back a single-use ticket bound to the caller's session.
//  2. GET connect?t=<ticket>, opened by the panel in a window of its own on
//     the second press. It spends the ticket, makes a state and a nonce, and
//     sends the browser to Twitch. Without a ticket it writes nothing, counts
//     nothing and redirects nowhere, so a Markdown image, another site or a
//     frame in the desktop app cannot start a connection by fetching it.
//  3. GET callback, where Twitch sends the browser back. It spends the state,
//     insists on the same person's own interactive session, trades the code
//     for a token on the server, checks the ID token carries the state's
//     nonce, asks Twitch whose the token is, writes the link, and revokes
//     the token. VillaNET keeps no Twitch token of anybody's.
//
// connect and callback answer in HTML and sit above ServeHTTP's user check,
// each with its own session and access checks. Connecting is for linked
// people only (the lock screen's rule): a Twitch connection makes somebody a
// streamer everybody sees, and it never unlocks the panel or lets anybody in.

const (
	twitchConnectPath       = "/api/v1/twitch/connect"
	twitchCallbackPath      = "/api/v1/twitch/callback"
	twitchConnectTicketPath = "/api/v1/twitch/connect-ticket"
	twitchLinkPath          = "/api/v1/twitch/link"
	twitchNoticePath        = "/api/v1/twitch/notice"
	twitchPreviewRoute      = "/api/v1/twitch/preview"

	adminTwitchPath       = "/api/v1/admin/twitch"
	adminTwitchBlockPath  = "/api/v1/admin/twitch/block"
	adminTwitchAllDayPath = "/api/v1/admin/twitch/allday"

	// A ticket and a state each last ten minutes.
	twitchTicketSeconds = 600
	twitchStateMS       = int64(600 * 1000)

	// The whole of a callback's conversation with Twitch, and the revoke
	// after it, which runs on its own budget so a slow page still revokes.
	twitchCallbackBudget = 15 * time.Second
	twitchRevokeBudget   = 8 * time.Second

	maxTwitchCodeBytes = 512

	twitchSwitchedOff = "Connecting Twitch accounts is switched off on this server."
	twitchNeedsSetup  = "An admin needs to finish setting up Twitch."
	twitchBlockedText = "An admin has turned off Connect Twitch for your account."
)

// twitchInteractiveOnly is the refusal for a connect asked with a personal
// access token, with the server's name for the plugin (brandName).
func twitchInteractiveOnly(name string) string {
	return "Connect Twitch from the " + name + " panel while signed in to Mattermost."
}

var (
	twitchHexToken = regexp.MustCompile(`^[0-9a-f]{64}$`)
	twitchErrorID  = regexp.MustCompile(`^[a-z_]{1,40}$`)
)

// twitchCallbackURL is the redirect_uri, exactly as the admin must register it.
// problem is a sentence for the admin when there cannot be one.
func twitchCallbackURL(siteURL string) (address, problem string) {
	siteURL = strings.TrimSpace(siteURL)
	if siteURL == "" {
		return "", "Mattermost's Site URL is not set (System Console > Environment > Web Server > Site URL), so Twitch has nowhere to send people back to."
	}

	parsed, err := url.Parse(siteURL)
	if err != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.User != nil || parsed.Host == "" {
		return "", "The Site URL could not be read as an address."
	}

	switch {
	case parsed.Scheme == "https":
	case parsed.Scheme == "http" && isLocalTwitchHost(parsed.Hostname()):
	default:
		return "", "Twitch only accepts an https:// callback address, and the Site URL is " + parsed.Scheme + "://…"
	}

	return strings.TrimRight(siteURL, "/") + "/plugins/" + manifestID + twitchCallbackPath, ""
}

func isLocalTwitchHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// twitchEmbedProblem says why Twitch's player cannot run on pages served at
// siteURL, or "" when it can. Twitch names the page's host in the player's
// frame-ancestors, and accepts no port, no scheme and no IP address there,
// assuming 443 for anything but localhost.
func twitchEmbedProblem(siteURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(siteURL))
	if err != nil || parsed.Host == "" {
		return "Mattermost's Site URL is not set, so it is not known where Twitch's player would run."
	}

	host := parsed.Hostname()
	switch {
	case host == "localhost" || host == "127.0.0.1":
		return ""
	case parsed.Scheme != "https":
		return "Twitch's player only runs on https:// pages."
	case parsed.Port() != "" && parsed.Port() != "443":
		return "Twitch's player only runs on the standard https port (443); this server uses :" + parsed.Port() + "."
	case net.ParseIP(strings.Trim(host, "[]")) != nil:
		return "Twitch's player does not run on a site reached by IP address."
	default:
		return ""
	}
}

// siteURL is Mattermost's Site URL, unsanitized, or "".
func (p *Plugin) siteURL() string {
	config := p.API.GetConfig()
	if config == nil || config.ServiceSettings.SiteURL == nil {
		return ""
	}

	return *config.ServiceSettings.SiteURL
}

// interactiveSession is whether a request comes from somebody's own web or
// desktop session, and not a bot, a personal access token or an OAuth app,
// which Mattermost also lets into plugin routes. Only an interactive session
// may add a Twitch connection: an integration acting for somebody is not
// them approving it.
func (p *Plugin) interactiveSession(c *plugin.Context, userID string) bool {
	if c == nil || c.SessionId == "" || userID == "" {
		return false
	}

	session, appErr := p.API.GetSession(c.SessionId)
	if appErr != nil || session == nil || session.UserId != userID {
		return false
	}

	return !session.IsIntegration()
}

// randomHex is n random bytes, hex-encoded.
func randomHex(n int) (string, error) {
	buffer := make([]byte, n)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}

	return hex.EncodeToString(buffer), nil
}

func hashHex(value string) string {
	sum := sha256.Sum256([]byte(value))

	return hex.EncodeToString(sum[:])
}

// twitchTicket is a connect ticket as stored: its hash, never itself.
type twitchTicket struct {
	Hash    string `json:"h"`
	Session string `json:"s"`
	At      int64  `json:"at"`
}

// twitchState is a connection in flight, kept for ten minutes. The nonce is
// the one secret VillaNET keeps about it, and only here.
type twitchState struct {
	User     string `json:"u"`
	At       int64  `json:"at"`
	Redirect string `json:"r"`
	Nonce    string `json:"n"`
}

// connectTwitchLinks is the link document for a step of connecting. Bytes
// that will not decode are set aside by whoever finds them first, here as in
// the loop, and are never overwritten: nobody can connect until an admin
// deletes all Twitch data.
func (p *Plugin) connectTwitchLinks() (*twitchLinks, error) {
	doc, err := p.readTwitchLinks()
	if errors.Is(err, errTwitchLinksUnreadable) {
		s := p.twitchLive()
		s.linksUnreadable(s.now())
	}

	return doc, err
}

// handleTwitchTicket answers POST /api/v1/twitch/connect-ticket. The gate has
// already found the caller linked.
func (p *Plugin) handleTwitchTicket(c *plugin.Context, w http.ResponseWriter, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.interactiveSession(c, userID) {
		writeError(w, http.StatusForbidden, twitchInteractiveOnly(p.brandName()))
		return
	}

	cfg := p.getConfiguration()
	if !cfg.twitchLiveOn() {
		writeError(w, http.StatusNotFound, twitchSwitchedOff)
		return
	}

	if _, problem := twitchCallbackURL(p.siteURL()); problem != "" {
		message := twitchNeedsSetup
		if p.isSystemAdmin(userID) {
			message = problem
		}
		writeError(w, http.StatusServiceUnavailable, message)

		return
	}

	doc, err := p.connectTwitchLinks()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, p.brandName()+" could not check Twitch just now. Try again in a moment.")
		return
	}
	if _, blocked := doc.Blocked[userID]; blocked {
		writeError(w, http.StatusForbidden, twitchBlockedText)
		return
	}

	s := p.twitchLive()
	now := s.now()

	// The only place connecting is counted: a ticket is what a connection
	// cannot start without.
	if !s.connectLimiter.allowAt(userID, now) {
		writeTooMany(w, "Too many tries. Wait a few minutes and press Connect Twitch again.", s.connectLimiter, userID, now)
		return
	}

	ticket, err := randomHex(32)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, p.brandName()+" could not start connecting Twitch just now. Try again in a moment.")
		return
	}

	record, err := json.Marshal(twitchTicket{Hash: hashHex(ticket), Session: c.SessionId, At: now.UnixMilli()})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, p.brandName()+" could not start connecting Twitch just now. Try again in a moment.")
		return
	}

	// One ticket per person: a new one replaces the old.
	if appErr := p.API.KVSetWithExpiry(kvTwitchTicketPrefix+userID, record, twitchTicketSeconds); appErr != nil {
		writeError(w, http.StatusServiceUnavailable, p.brandName()+" could not start connecting Twitch just now. Try again in a moment.")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ticket": ticket, "expiresIn": twitchTicketSeconds})
}

// serveTwitchConnect answers GET /api/v1/twitch/connect?t=<ticket>. Until the
// ticket is spent it writes nothing, counts nothing and redirects nowhere.
func (p *Plugin) serveTwitchConnect(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	twitchPageHeaders(w)

	if r.URL.Path != twitchConnectPath || r.Method != http.MethodGet {
		p.writeTwitchPage(w, http.StatusNotFound, twitchPageStale, twitchPageData{})
		return
	}

	// A cheap extra layer. A browser that says what it is fetching, and
	// says it is not a page being opened by this site or by the desktop
	// app's own window (Sec-Fetch-Site: none), is not somebody pressing
	// Continue on Twitch. Browsers that send neither rely on the ticket.
	if dest := r.Header.Get("Sec-Fetch-Dest"); dest != "" && dest != "document" {
		p.writeTwitchPage(w, http.StatusBadRequest, twitchPageStale, twitchPageData{})
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		p.writeTwitchPage(w, http.StatusBadRequest, twitchPageStale, twitchPageData{})
		return
	}

	userID := r.Header.Get("Mattermost-User-Id")
	if userID == "" || !p.interactiveSession(c, userID) {
		p.writeTwitchPage(w, http.StatusUnauthorized, twitchPageSignIn, twitchPageData{})
		return
	}

	// Connecting is for linked people, which this route, above the gate,
	// asks for itself.
	if page, status, allowed := p.twitchConnectAllowed(userID); !allowed {
		p.writeTwitchPage(w, status, page, twitchPageData{})
		return
	}

	ticket := r.URL.Query().Get("t")
	if !twitchHexToken.MatchString(ticket) {
		p.writeTwitchPage(w, http.StatusBadRequest, twitchPageStale, twitchPageData{})
		return
	}

	key := kvTwitchTicketPrefix + userID
	raw, appErr := p.API.KVGet(key)
	if appErr != nil {
		p.writeTwitchPage(w, http.StatusServiceUnavailable, twitchPageStore, twitchPageData{})
		return
	}
	if len(raw) == 0 {
		p.writeTwitchPage(w, http.StatusBadRequest, twitchPageStale, twitchPageData{})
		return
	}

	// Whoever sent a wrong ticket cannot spend the real one: it is left
	// exactly where it is.
	var stored twitchTicket
	if json.Unmarshal(raw, &stored) != nil ||
		subtle.ConstantTimeCompare([]byte(hashHex(ticket)), []byte(stored.Hash)) != 1 ||
		subtle.ConstantTimeCompare([]byte(stored.Session), []byte(c.SessionId)) != 1 {
		p.writeTwitchPage(w, http.StatusBadRequest, twitchPageStale, twitchPageData{})
		return
	}

	// Spent: the first write, and only the caller's own valid ticket reaches it.
	spent, appErr := p.API.KVCompareAndDelete(key, raw)
	if appErr != nil || !spent {
		p.writeTwitchPage(w, http.StatusBadRequest, twitchPageStale, twitchPageData{})
		return
	}

	cfg := p.getConfiguration()
	if !cfg.twitchLiveOn() {
		p.writeTwitchPage(w, http.StatusNotFound, twitchPageOff, twitchPageData{})
		return
	}

	redirectURI, problem := twitchCallbackURL(p.siteURL())
	if problem != "" {
		data := twitchPageData{}
		if p.isSystemAdmin(userID) {
			data.Problem = problem
		}
		p.writeTwitchPage(w, http.StatusServiceUnavailable, twitchPageSetup, data)

		return
	}

	doc, err := p.connectTwitchLinks()
	if err != nil {
		p.writeTwitchPage(w, http.StatusServiceUnavailable, twitchPageStore, twitchPageData{})
		return
	}
	if _, blocked := doc.Blocked[userID]; blocked {
		p.writeTwitchPage(w, http.StatusForbidden, twitchPageBlocked, twitchPageData{})
		return
	}
	if _, connected := doc.Users[userID]; !connected && len(doc.Users) >= maxTwitchLinks {
		p.writeTwitchPage(w, http.StatusConflict, twitchPageFull, twitchPageData{})
		return
	}

	state, stateErr := randomHex(32)
	nonce, nonceErr := randomHex(32)
	if stateErr != nil || nonceErr != nil {
		p.writeTwitchPage(w, http.StatusServiceUnavailable, twitchPageStore, twitchPageData{})
		return
	}

	record, err := json.Marshal(twitchState{User: userID, At: p.twitchLive().now().UnixMilli(), Redirect: redirectURI, Nonce: nonce})
	if err != nil {
		p.writeTwitchPage(w, http.StatusServiceUnavailable, twitchPageStore, twitchPageData{})
		return
	}
	if appErr := p.API.KVSetWithExpiry(kvTwitchStatePrefix+state, record, twitchTicketSeconds); appErr != nil {
		p.writeTwitchPage(w, http.StatusServiceUnavailable, twitchPageStore, twitchPageData{})
		return
	}

	w.Header().Set("Location", twitchAuthorizeURL(cfg.TwitchClientID, redirectURI, state, nonce))
	w.WriteHeader(http.StatusFound)
}

// twitchAuthorizeURL is where Continue on Twitch goes, its parameters in a
// fixed order so the address is always the same string for the same values.
// force_verify makes Twitch ask, every time, rather than wave a browser
// already signed in to Twitch straight through.
func twitchAuthorizeURL(clientID, redirectURI, state, nonce string) string {
	var query strings.Builder
	for i, pair := range [][2]string{
		{"response_type", "code"},
		{"client_id", clientID},
		{"redirect_uri", redirectURI},
		{"scope", "openid"},
		{"state", state},
		{"nonce", nonce},
		{"force_verify", "true"},
	} {
		if i > 0 {
			query.WriteByte('&')
		}
		query.WriteString(url.QueryEscape(pair[0]))
		query.WriteByte('=')
		query.WriteString(url.QueryEscape(pair[1]))
	}

	return twitchIDBase + "/oauth2/authorize?" + query.String()
}

// twitchConnectAllowed is the lock's rule for adding a connection: linked
// people only. A store that does not answer is the store page, never a no.
func (p *Plugin) twitchConnectAllowed(userID string) (page string, status int, allowed bool) {
	level, _, err := p.accessOf(userID)
	switch {
	case err != nil:
		return twitchPageStore, http.StatusServiceUnavailable, false
	case level != accessLinked:
		return twitchPageLocked, http.StatusForbidden, false
	default:
		return "", 0, true
	}
}

// serveTwitchCallback answers GET /api/v1/twitch/callback, where Twitch sends
// the person's browser back. Nothing here asks Twitch anything until a live
// state of the caller's own has been spent.
func (p *Plugin) serveTwitchCallback(c *plugin.Context, w http.ResponseWriter, r *http.Request) {
	twitchPageHeaders(w)

	if r.URL.Path != twitchCallbackPath || r.Method != http.MethodGet {
		p.writeTwitchPage(w, http.StatusNotFound, twitchPageExpired, twitchPageData{})
		return
	}

	query := r.URL.Query()
	s := p.twitchLive()
	now := s.now()
	nowMS := now.UnixMilli()

	state := query.Get("state")
	if !twitchHexToken.MatchString(state) {
		p.writeTwitchPage(w, http.StatusBadRequest, twitchPageExpired, twitchPageData{})
		return
	}

	key := kvTwitchStatePrefix + state
	raw, appErr := p.API.KVGet(key)
	if appErr != nil {
		p.writeTwitchPage(w, http.StatusServiceUnavailable, twitchPageStore, twitchPageData{})
		return
	}
	if len(raw) == 0 {
		p.writeTwitchPage(w, http.StatusBadRequest, twitchPageExpired, twitchPageData{})
		return
	}

	// Checked again here, because a compare-and-delete does not look at
	// whether a key has expired.
	var record twitchState
	if json.Unmarshal(raw, &record) != nil || record.User == "" || nowMS-record.At > twitchStateMS || record.At > nowMS+twitchStateMS {
		_, _ = p.API.KVCompareAndDelete(key, raw)
		p.writeTwitchPage(w, http.StatusBadRequest, twitchPageExpired, twitchPageData{})

		return
	}

	// Used up, whatever happens next.
	spent, appErr := p.API.KVCompareAndDelete(key, raw)
	if appErr != nil {
		p.writeTwitchPage(w, http.StatusServiceUnavailable, twitchPageStore, twitchPageData{})
		return
	}
	if !spent {
		p.writeTwitchPage(w, http.StatusBadRequest, twitchPageExpired, twitchPageData{})
		return
	}

	// The account-linking defence: the person who started this, in their
	// own interactive session, is the only one who may finish it. Otherwise
	// somebody could start a connection as themselves and trick somebody
	// else into approving it on Twitch, attaching the victim's Twitch
	// account to the attacker's Mattermost one.
	userID := r.Header.Get("Mattermost-User-Id")
	if userID == "" || !p.interactiveSession(c, userID) {
		p.writeTwitchPage(w, http.StatusUnauthorized, twitchPageSignIn, twitchPageData{})
		return
	}
	if userID != record.User {
		p.writeTwitchPage(w, http.StatusForbidden, twitchPageOtherAccount, twitchPageData{})
		return
	}

	// A state stays good for ten minutes, and a let-out or an unlink inside
	// them must still stop the connection.
	if page, status, allowed := p.twitchConnectAllowed(userID); !allowed {
		p.writeTwitchPage(w, status, page, twitchPageData{})
		return
	}

	if twitchError := query.Get("error"); twitchError != "" {
		if twitchError == "access_denied" {
			p.writeTwitchPage(w, http.StatusOK, twitchPageCancelled, twitchPageData{Close: true})
			return
		}

		// Whoever completes a callback chooses this, so it reaches the log
		// only when it is one of Twitch's plain code words.
		logged := "other"
		if twitchErrorID.MatchString(twitchError) {
			logged = twitchError
		}
		p.twitchApp().warnOnce("twitch-callback", "Twitch sent somebody back without connecting their account.", "error", logged)
		p.writeTwitchPage(w, http.StatusBadGateway, twitchPageTwitchError, twitchPageData{})

		return
	}

	code := query.Get("code")
	if code == "" || len(code) > maxTwitchCodeBytes {
		p.writeTwitchPage(w, http.StatusBadRequest, twitchPageExpired, twitchPageData{})
		return
	}

	cfg := p.getConfiguration()
	if !cfg.twitchLiveOn() {
		p.writeTwitchPage(w, http.StatusNotFound, twitchPageOff, twitchPageData{})
		return
	}
	creds, _ := cfg.twitchCredentials()

	doc, err := p.connectTwitchLinks()
	if err != nil {
		p.writeTwitchPage(w, http.StatusServiceUnavailable, twitchPageStore, twitchPageData{})
		return
	}
	if _, blocked := doc.Blocked[userID]; blocked {
		p.writeTwitchPage(w, http.StatusForbidden, twitchPageBlocked, twitchPageData{})
		return
	}

	who, err := p.proveTwitchAccount(r.Context(), creds, code, record, now)
	if err != nil {
		p.writeTwitchPage(w, http.StatusBadGateway, twitchPageTwitchError, twitchPageData{})
		return
	}

	doc, previous, purging, err := p.setTwitchLink(userID, who, nowMS)
	switch {
	case errors.Is(err, errTwitchFull):
		p.writeTwitchPage(w, http.StatusConflict, twitchPageFull, twitchPageData{})
		return
	case errors.Is(err, errTwitchPurging):
		p.writeTwitchPage(w, http.StatusConflict, twitchPagePurging, twitchPageData{})
		return
	case errors.Is(err, errTwitchBlocked):
		p.writeTwitchPage(w, http.StatusForbidden, twitchPageBlocked, twitchPageData{})
		return
	case err != nil:
		p.writeTwitchPage(w, http.StatusServiceUnavailable, twitchPageStore, twitchPageData{})
		return
	}

	s.noteLinks(doc)
	s.kick()
	if purging {
		p.kickTwitchPurges()
	}
	p.refreshSoon()
	p.tellTwitchChanged(userID)
	if previous != "" {
		s.forgetLive(who.ID)
		p.tellTwitchChanged(previous)
	}

	p.API.LogInfo("Somebody connected their Twitch account", "user_id", userID)

	p.writeTwitchPage(w, http.StatusOK, twitchPageConnected, twitchPageData{
		Close:     true,
		Name:      who.DisplayName,
		Login:     who.Login,
		TakenOver: previous != "",
	})
}

// proveTwitchAccount trades the code, checks the ID token carries the
// state's nonce, and asks Twitch whose the token is, which must agree. The
// token is revoked whatever happens once it exists, on a budget of its own,
// and is never kept, logged or shown.
func (p *Plugin) proveTwitchAccount(ctx context.Context, creds twitchCredentials, code string, record twitchState, now time.Time) (twitchUser, error) {
	app := p.twitchApp()

	ctx, cancel := context.WithTimeout(ctx, twitchCallbackBudget)
	defer cancel()

	token, idToken, err := app.exchangeCode(ctx, creds, code, record.Redirect)
	if err != nil {
		return twitchUser{}, err
	}
	defer func() {
		revokeCtx, revokeCancel := context.WithTimeout(context.Background(), twitchRevokeBudget)
		defer revokeCancel()

		app.revokeUserToken(revokeCtx, creds, token)
	}()

	// This binds the code to the state it was issued for: a code from
	// somebody else's connection carries somebody else's nonce.
	sub, err := checkIDToken(idToken, creds.clientID, record.Nonce, now)
	if err != nil {
		var refused *twitchIDTokenError
		reason := "shape"
		if errors.As(err, &refused) {
			reason = refused.code
		}
		app.warnOnce("twitch-idtoken", "Twitch's confirmation of a new connection did not check out, so nothing was connected.", "reason", reason)

		return twitchUser{}, err
	}

	who, err := app.whoseToken(ctx, creds, token)
	if err != nil {
		return twitchUser{}, err
	}
	if who.ID != sub {
		app.warnOnce("twitch-idtoken", "Twitch's two answers about a new connection disagreed, so nothing was connected.", "reason", "sub")
		return twitchUser{}, errTwitchBadAnswer
	}

	return who, nil
}

// handleTwitchUnlink answers DELETE /api/v1/twitch/link: the caller's own
// Disconnect. Open to everybody, the lock screen included, and working
// whatever the switch says, because Twitch's agreement says to delete a
// person's data when they ask.
func (p *Plugin) handleTwitchUnlink(w http.ResponseWriter, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if err := p.disconnectTwitch(userID, false, p.twitchLive().now().UnixMilli()); err != nil {
		p.API.LogWarn("Could not disconnect somebody's Twitch account", "user_id", userID, "error", err.Error())
		writeError(w, http.StatusServiceUnavailable, p.brandName()+" could not disconnect Twitch just now. Try again in a moment.")

		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleTwitchNotice answers POST /api/v1/twitch/notice: the caller has read
// that their Twitch account was connected to somebody else.
func (p *Plugin) handleTwitchNotice(w http.ResponseWriter, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if err := p.dismissTwitchNotice(userID, p.twitchLive().now().UnixMilli()); err != nil {
		writeError(w, http.StatusServiceUnavailable, p.brandName()+" could not save that just now. Try again in a moment.")
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// meTwitch is what /me says about Twitch.
type meTwitch struct {
	Available   bool        `json:"available"`             // the switch, credentials and a callback address
	Login       string      `json:"login,omitempty"`       // Twitch's login, while connected
	DisplayName string      `json:"displayName,omitempty"` // Twitch's display name
	Live        bool        `json:"live,omitempty"`        // in the store's live list right now
	Blocked     bool        `json:"blocked,omitempty"`     // an admin blocked this person
	Lost        *twitchLost `json:"lost,omitempty"`        // their account moved to someone else
	Problem     string      `json:"problem,omitempty"`     // system admins only
}

// meTwitchFor is /me's twitch: present whenever the switch is on, and
// whenever the caller has a connection or a takeover notice even while it is
// off, so Disconnect can always be reached. Read from the store directly, not
// the cache, so a connection shows at once: with the switch off, the only
// thing /me does for Twitch.
func (p *Plugin) meTwitchFor(userID string, cfg *configuration, admin bool) *meTwitch {
	on := cfg.twitchLiveOn()
	s := p.twitchLive()
	nowMS := s.now().UnixMilli()

	// Asked of Mattermost only while the switch is on: off, nothing is
	// available whatever the address.
	callbackProblem := ""
	if on {
		_, callbackProblem = twitchCallbackURL(p.siteURL())
	}

	doc, err := p.readTwitchLinks()
	if err != nil {
		if !on {
			return nil
		}

		answer := &meTwitch{Available: callbackProblem == ""}
		if admin {
			answer.Problem = twitchLinksProblem
			if !errors.Is(err, errTwitchLinksUnreadable) {
				answer.Problem = "The list of Twitch connections could not be read just now."
			}
		}

		return answer
	}

	link, connected := doc.Users[userID]
	lost := doc.lostFor(userID, nowMS)
	if !on && !connected && lost == nil {
		return nil
	}

	answer := &meTwitch{Available: on && callbackProblem == "", Lost: lost}
	if _, blocked := doc.Blocked[userID]; blocked {
		answer.Blocked = true
	}
	if connected {
		answer.Login = link.Login
		answer.DisplayName = link.Name
		answer.Live = s.twitchLiveFor(userID)
	}

	if admin && on {
		switch {
		case callbackProblem != "":
			answer.Problem = callbackProblem
		case s.status().problem != "":
			answer.Problem = s.status().problem
		default:
			if halted, reason := p.twitchAirHalt.state(); halted {
				answer.Problem = "Twitch hours are not being recorded: " + reason
			}
		}
	}

	return answer
}

// The pages connect and callback answer with. One template, so every value is
// escaped; no picture, no outside font, and the only script closes the window
// when the browser allows it.

const (
	twitchPageConnected    = "connected"
	twitchPageCancelled    = "cancelled"
	twitchPageExpired      = "expired"
	twitchPageStale        = "stale"
	twitchPageSignIn       = "signin"
	twitchPageOtherAccount = "otheraccount"
	twitchPageBlocked      = "blocked"
	twitchPageLocked       = "locked"
	twitchPageOff          = "off"
	twitchPageSetup        = "setup"
	twitchPageFull         = "full"
	twitchPagePurging      = "purging"
	twitchPageTwitchError  = "twitcherror"
	twitchPageStore        = "store"

	// The page's one script. Its hash goes in the page's CSP, computed from
	// this very text, so a change to it can never leave the CSP stale.
	twitchPageScript = "setTimeout(function(){window.close()},1500)"
)

var twitchPageScriptHash = func() string {
	sum := sha256.Sum256([]byte(twitchPageScript))

	return base64.StdEncoding.EncodeToString(sum[:])
}()

// twitchPageWords is each page's heading and body, with name, the server's
// name for the plugin (brandName), where a page says it.
func twitchPageWords(name string) map[string][2]string {
	return map[string][2]string{
		twitchPageConnected:    {"Twitch connected", ""},
		twitchPageCancelled:    {"Not connected", "You cancelled on Twitch, so nothing was connected. You can close this window."},
		twitchPageExpired:      {"This link has run out", "This Twitch connection was already used, or took longer than 10 minutes. Close this window and press Connect Twitch again. Do not share this page's address."},
		twitchPageStale:        {"Start from " + name, "This window was not opened by Connect Twitch in the " + name + " panel, or it was opened twice. Close it and press Connect Twitch in the panel."},
		twitchPageSignIn:       {"Sign in first", "Sign in to Mattermost in this browser, then press Connect Twitch in the " + name + " panel again. Do not share this page's address."},
		twitchPageOtherAccount: {"Started by another account", "This connection was started while signed in to Mattermost as someone else. Close this window and press Connect Twitch again while signed in as yourself. Do not share this page's address."},
		twitchPageBlocked:      {"Not allowed", twitchBlockedText},
		twitchPageLocked:       {"Not allowed", "Connect Twitch is for people who have unlocked " + name + " with their bearer token. Unlock it in the " + name + " panel, then press Connect Twitch again."},
		twitchPageOff:          {"Twitch is switched off", twitchSwitchedOff},
		twitchPageSetup:        {"Not set up yet", twitchNeedsSetup},
		twitchPageFull:         {"No room", "This server already has 500 Twitch accounts connected, which is as many as it keeps."},
		twitchPagePurging:      {"Not just yet", "That Twitch channel's hours are still being removed after it was disconnected. Try again in a minute."},
		twitchPageTwitchError:  {"Twitch did not confirm it", "Twitch did not confirm the account just now. Close this window and try again in a minute."},
		twitchPageStore:        {"Could not save", name + " could not save the connection just now. Try again in a minute."},
	}
}

type twitchPageData struct {
	Brand         string // the server's name for the plugin (brandName)
	Heading, Body string
	Problem       string // for system admins, on the setup page
	Name, Login   string // on the connected page
	TakenOver     bool
	Connected     bool
	Close         bool
}

var twitchPageTemplate = template.Must(template.New("twitch").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>{{.Brand}} · {{.Heading}}</title>
<style>
body { margin: 0; min-height: 100vh; display: flex; align-items: center; justify-content: center; background: #0b0b0b; color: #e6e6e6; font: 15px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif; }
main { max-width: 26rem; margin: 1.5rem; padding: 1.5rem 1.75rem; background: #161616; border-top: 3px solid #3a3a3a; border-radius: 6px; }
h1 { margin: 0 0 0.75rem; font-size: 1.15rem; font-weight: 600; }
p { margin: 0.5rem 0 0; }
.problem { color: #ff8a8a; }
</style>
</head>
<body>
<main>
<h1>{{.Heading}}</h1>
{{if .Connected}}<p>Connected as <strong>{{.Name}}</strong> (twitch.tv/{{.Login}}). You can close this window and go back to {{.Brand}}.</p>
{{if .TakenOver}}<p>It was connected to another Mattermost account before. It is connected to yours now.</p>
{{end}}{{else}}<p>{{.Body}}</p>
{{end}}{{if .Problem}}<p class="problem">{{.Problem}}</p>
{{end}}</main>
{{if .Close}}<script>` + twitchPageScript + `</script>
{{end}}</body>
</html>
`))

// twitchPageHeaders goes on every page, before anything else is decided.
func twitchPageHeaders(w http.ResponseWriter) {
	header := w.Header()
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'sha256-"+twitchPageScriptHash+
		"'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
}

func (p *Plugin) writeTwitchPage(w http.ResponseWriter, status int, page string, data twitchPageData) {
	data.Brand = p.brandName()
	words := twitchPageWords(data.Brand)[page]
	data.Heading, data.Body = words[0], words[1]
	data.Connected = page == twitchPageConnected

	var body bytes.Buffer
	if err := twitchPageTemplate.Execute(&body, data); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}
