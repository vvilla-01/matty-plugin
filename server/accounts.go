package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/plugin"
)

// Connect Steam and Connect Hytale (design/3.4.0.md §2): a linked person proves
// a Steam or Hytale account is theirs by signing in on that service's own page,
// and everybody who may watch the panel then sees their Steam status or their
// Hytale username. Twitch's connecting (twitch_oauth.go) is the template, and
// is not touched: this file serves Steam and Hytale only, each through one
// accountProvider, so that a ticket, a state or a notice of one service can
// never be spent, read or shown as another's.
//
// Three requests make a connection in the panel, and each one guards the next:
//
//  1. POST connect-ticket, on the first press of Connect. A POST, so a cookie
//     caller must carry Mattermost's CSRF token, and it hands back a
//     single-use ticket bound to the caller's session. That is what a ticket
//     is for: the next request is a GET, which a Markdown image, another site
//     or a frame could make with the reader's cookie, and without a ticket it
//     writes nothing, counts nothing and redirects nowhere.
//  2. GET connect?t=<ticket>, opened by the panel in a window of its own. It
//     spends the ticket, writes a state for ten minutes and sends the browser
//     to the service: Steam's OpenID sign-in (steam.go) or Hytale's OIDC
//     sign-in with PKCE (hytale.go).
//  3. GET callback, where the service sends the browser back. It spends the
//     state, insists on the same person's own interactive session, has the
//     service prove the account on the server, and writes the link.
//
// The web callback keeps requiring the starter's own session. Without it,
// somebody could start a connection as themselves, send the sign-in address
// to somebody else, and attach the victim's Steam or Hytale account to their
// own Mattermost account (§6.6): a state bound to a user, used once and short
// lived describes exactly the attacker's state, so none of that is a defence.
//
// Matty, Victor's Android app, has no cookie to carry and no way back from a
// browser, so it finishes inside its own sign-in window (§6.5): app-connect
// makes a state marked "app" for the caller's Bearer session, Matty catches the
// service's return in its window without loading it, and app-finish hands the
// query over with the same session. The HTML callback spends an "app" state
// and links nothing (the "app" page), so a sign-in address Matty asked for and
// somebody passed on attaches nobody's account to anybody.
//
// Disconnecting is open to everybody, the lock screen included, works whatever
// the switch says, and needs no purge worker: neither service has derived data.
// A connection's status lives only in the accounts loop's memory
// (accounts_live.go), so one compare-and-set write of the link document
// deletes everything VillaNET holds about it, the takeover notices that name
// the account included (dropNotices).
//
// Nothing a service sends back, and no ticket, state, nonce, verifier, code,
// token, SteamID, uuid or username, ever reaches a log line, an error string or
// a page other than the person's own connected page.

const (
	// The two services' ids: their KV prefixes and paths are built from them.
	accountSteam  = "steam"
	accountHytale = "hytale"

	kvSteamLinks       = "steam_links"
	kvHytaleLinks      = "hytale_links"
	kvSteamNoncePrefix = "steam_nonce_"

	// The four HTML routes, dispatched above ServeHTTP's user check.
	steamConnectPath   = "/api/v1/steam/connect"
	steamCallbackPath  = "/api/v1/steam/callback"
	hytaleConnectPath  = "/api/v1/hytale/connect"
	hytaleCallbackPath = "/api/v1/hytale/callback"

	accountLinksVersion = 1

	// One GetPlayerSummaries call takes at most 100 SteamIDs, so a Steam pass
	// is always one call; Hytale keeps the same cap, which is plenty here.
	maxAccountLinks   = 100
	maxAccountBlocked = 500

	// A takeover notice is forgotten after a month, read or not.
	accountLostKeepMS = int64(30 * 24 * time.Hour / time.Millisecond)

	// A ticket and a state each last ten minutes.
	accountTicketSeconds = 600
	accountStateMS       = int64(600 * 1000)

	// The whole of a callback's conversation with the service.
	accountCallbackBudget = 15 * time.Second

	// app-finish's body: the raw query of an address, and a little JSON.
	maxAccountAppFinishBytes = 8 << 10

	accountModeWeb = "web"
	accountModeApp = "app"

	// wsEventAccounts reaches every client as
	// custom_ceo.villa.matty_accounts, with nothing in it: "read
	// /api/v1/accounts again, if you may watch". (A change to somebody's own
	// connection is told to them with wsEventMe, in access.go.)
	wsEventAccounts = "accounts"
)

var (
	accountHexToken  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	accountErrorWord = regexp.MustCompile(`^[a-z_]{1,40}$`)

	// errAccountLinksUnreadable is a link document whose bytes will not
	// decode. It is set aside and never overwritten, and it is errStore to
	// everybody who only needs to know that the store could not help.
	errAccountLinksUnreadable = fmt.Errorf("%w: a list of connections could not be read", errStore)

	errAccountFull        = errors.New("as many accounts are connected as this server keeps")
	errAccountBlocked     = errors.New("an admin has turned off connecting for this person")
	errAccountBlockedFull = errors.New("the block list is full")

	// errAccountCleared is a connection that started before an admin's Delete
	// all and finished after it: it is not written.
	errAccountCleared = errors.New("the connection started before everything was deleted")
)

// accountProvider is one service a person can connect besides Twitch. Each
// one's paths, KV keys, words and checks come from here, so a ticket, a state
// or a notice of one service can never be spent, read or shown as another's.
type accountProvider struct {
	id       string // "steam" | "hytale": KV prefixes and paths are built from it
	name     string // "Steam" | "Hytale", in words
	nameKind string // what the connected page calls the name: "Steam" | "Hytale username"
	linksKey string // "steam_links" | "hytale_links"
	maxLinks int

	connectPath, callbackPath string

	on func(cfg *configuration) bool

	// setup is "" when people can connect, otherwise the sentence an admin
	// is shown.
	setup func(p *Plugin, cfg *configuration) string

	// callbackURL is where the service sends people back to, without any
	// query, or the sentence that says why there is none.
	callbackURL func(siteURL string) (address, problem string)

	// start writes what the provider needs into the state and returns where
	// the browser (or Matty's sign-in window) is sent. It asks the service
	// nothing.
	start func(p *Plugin, cfg *configuration, state string, st *accountState) (location string, err error)

	// declined is the "came back without an account" check (K8): a person
	// who cancelled, or the service saying no. It asks the service nothing.
	declined func(query url.Values) *accountRefusal

	// finish turns what came back into a proof, or a refusal with a code
	// word.
	finish func(ctx context.Context, p *Plugin, cfg *configuration, state string, st accountState, query url.Values, now time.Time) (accountProof, *accountRefusal)
}

// accountProof is what a service proved about the account that signed in.
type accountProof struct {
	ID     string // Steam: SteamID64, decimal; Hytale: profile uuid, lower case
	Name   string // Steam: the Steam name ("" if Steam's answer was late); Hytale: the username
	Hidden bool   // Steam only: the profile is not public
}

// accountRefusal is a page id and a code word: the page for the browser, the
// code for Matty's app-finish, the reason (fixed words only) for the log.
type accountRefusal struct {
	page, code, reason string
	status             int

	// problem is shown to system admins only, under the page's words.
	problem string

	// logged are more fixed values for the log line: a status, a provider's
	// error word that passed accountErrorWord. Never anything else.
	logged []any

	// signIn is what the refusal says is wrong with the admin's application
	// itself, to be remembered for the admin (hytaleProblemCredentials,
	// hytaleProblemScope), or "".
	signIn string
}

// The pages connect and callback answer with (§2.8).
const (
	accountPageConnected     = "connected"
	accountPageCancelled     = "cancelled"
	accountPageExpired       = "expired"
	accountPageStale         = "stale"
	accountPageSignIn        = "signin"
	accountPageOtherAccount  = "otheraccount"
	accountPageApp           = "app"
	accountPageLocked        = "locked"
	accountPageBlocked       = "blocked"
	accountPageOff           = "off"
	accountPageSetup         = "setup"
	accountPageFull          = "full"
	accountPageProviderError = "providererror"
	accountPageBusy          = "busy"
	accountPageNoProfile     = "noprofile"
	accountPageScope         = "scope"
	accountPageStore         = "store"
)

// The codes every JSON refusal of these routes carries (§6.3), so Matty can
// word them itself. locked and watch_only are the gate's own.
const (
	accountCodeInteractiveOnly = "interactive_only"
	accountCodeOff             = "off"
	accountCodeSetup           = "setup"
	accountCodeBlocked         = "blocked"
	accountCodeFull            = "full"
	accountCodeRateLimited     = "rate_limited"
	accountCodeBadRequest      = "bad_request"
	accountCodeExpired         = "expired"
	accountCodeOtherSession    = "other_session"
	accountCodeCancelled       = "cancelled"
	accountCodeProviderError   = "provider_error"
	accountCodeBusy            = "busy"
	accountCodeScope           = "scope"
	accountCodeNoProfile       = "no_profile"
	accountCodeStore           = "store"
)

// accountPageStatus is each page's status when no step says otherwise.
var accountPageStatus = map[string]int{
	accountPageConnected:     http.StatusOK,
	accountPageCancelled:     http.StatusOK,
	accountPageExpired:       http.StatusBadRequest,
	accountPageStale:         http.StatusBadRequest,
	accountPageSignIn:        http.StatusUnauthorized,
	accountPageOtherAccount:  http.StatusForbidden,
	accountPageApp:           http.StatusBadRequest,
	accountPageLocked:        http.StatusForbidden,
	accountPageBlocked:       http.StatusForbidden,
	accountPageOff:           http.StatusNotFound,
	accountPageSetup:         http.StatusServiceUnavailable,
	accountPageFull:          http.StatusConflict,
	accountPageProviderError: http.StatusBadGateway,
	accountPageBusy:          http.StatusServiceUnavailable,
	accountPageNoProfile:     http.StatusConflict,
	accountPageScope:         http.StatusBadGateway,
	accountPageStore:         http.StatusServiceUnavailable,
}

// accountPageCodes is the code word Matty is given where the browser would
// have been given a page.
var accountPageCodes = map[string]string{
	accountPageCancelled:     accountCodeCancelled,
	accountPageExpired:       accountCodeExpired,
	accountPageStale:         accountCodeExpired,
	accountPageSignIn:        accountCodeInteractiveOnly,
	accountPageOtherAccount:  accountCodeOtherSession,
	accountPageApp:           accountCodeOtherSession,
	accountPageLocked:        accessCodeLocked,
	accountPageBlocked:       accountCodeBlocked,
	accountPageOff:           accountCodeOff,
	accountPageSetup:         accountCodeSetup,
	accountPageFull:          accountCodeFull,
	accountPageProviderError: accountCodeProviderError,
	accountPageBusy:          accountCodeBusy,
	accountPageNoProfile:     accountCodeNoProfile,
	accountPageScope:         accountCodeScope,
	accountPageStore:         accountCodeStore,
}

// accountCodeStatus is each code's status in JSON (§6.3).
var accountCodeStatus = map[string]int{
	accessCodeLocked:           http.StatusForbidden,
	accessCodeWatchOnly:        http.StatusForbidden,
	accountCodeInteractiveOnly: http.StatusForbidden,
	accountCodeOff:             http.StatusNotFound,
	accountCodeSetup:           http.StatusServiceUnavailable,
	accountCodeBlocked:         http.StatusForbidden,
	accountCodeFull:            http.StatusConflict,
	accountCodeRateLimited:     http.StatusTooManyRequests,
	accountCodeBadRequest:      http.StatusBadRequest,
	accountCodeExpired:         http.StatusBadRequest,
	accountCodeOtherSession:    http.StatusForbidden,
	accountCodeCancelled:       http.StatusBadRequest,
	accountCodeProviderError:   http.StatusBadGateway,
	accountCodeBusy:            http.StatusServiceUnavailable,
	accountCodeScope:           http.StatusBadGateway,
	accountCodeNoProfile:       http.StatusConflict,
	accountCodeStore:           http.StatusServiceUnavailable,
}

// accountRefused is a refusal with its page's own status and code.
func accountRefused(page, reason string, logged ...any) *accountRefusal {
	return &accountRefusal{page: page, code: accountPageCodes[page], reason: reason, status: accountPageStatus[page], logged: logged}
}

// withStatus is the same refusal answered with another status, where a step
// says so.
func (r *accountRefusal) withStatus(status int) *accountRefusal {
	r.status = status
	return r
}

// accountCodeText is the sentence a JSON refusal carries for a code, in
// plain text: Matty may show it as it is. name is the server's name for the
// plugin (brandName).
func accountCodeText(prov *accountProvider, code, name string) string {
	switch code {
	case accessCodeLocked:
		return accessLockedText(name)
	case accessCodeWatchOnly:
		return accessWatchOnlyText
	case accountCodeInteractiveOnly:
		return "Connect " + prov.name + " while signed in with your password or single sign-on, not a personal access token."
	case accountCodeOff:
		return "Connecting " + prov.name + " accounts is switched off on this server."
	case accountCodeSetup:
		return "An admin needs to finish setting up " + prov.name + "."
	case accountCodeBlocked:
		return "An admin has turned off Connect " + prov.name + " for your account."
	case accountCodeFull:
		return fmt.Sprintf("This server already has %d %s accounts connected, which is as many as it keeps.", prov.maxLinks, prov.name)
	case accountCodeRateLimited:
		return "Too many tries. Wait a few minutes and press Connect " + prov.name + " again."
	case accountCodeBadRequest:
		return "Could not read the request."
	case accountCodeExpired:
		return "This " + prov.name + " sign-in was already used, or took longer than 10 minutes. Start again."
	case accountCodeOtherSession:
		return "This sign-in was started somewhere else. Start again from here."
	case accountCodeCancelled:
		return "You cancelled on " + prov.name + ", so nothing was connected."
	case accountCodeProviderError:
		return prov.name + " did not confirm the account just now. Try again in a minute."
	case accountCodeBusy:
		return prov.name + " asked " + name + " to wait. Try again in a couple of minutes."
	case accountCodeScope:
		return "Hytale has not approved this server's application to read game profiles yet."
	case accountCodeNoProfile:
		return "That Hytale account has no game profile to show."
	default:
		return name + " could not save that just now. Try again in a moment."
	}
}

// writeAccountCode answers a JSON route with a code and its sentence. A
// system admin is told the problem behind a setup refusal; everybody else is
// told an admin needs to finish setting up.
func (p *Plugin) writeAccountCode(w http.ResponseWriter, prov *accountProvider, code, problem, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	message := accountCodeText(prov, code, p.brandName())
	if problem != "" && p.isSystemAdmin(userID) {
		message = problem
	}

	status, known := accountCodeStatus[code]
	if !known {
		status = http.StatusServiceUnavailable
	}

	writeCodedError(w, status, message, code)
}

// accountTicket is a connect ticket as stored: its hash, never itself.
type accountTicket struct {
	Hash    string `json:"h"`
	Session string `json:"s"`
	At      int64  `json:"at"`
}

// accountState is a connection in flight, kept ten minutes. The nonce and the
// PKCE verifier are the secrets VillaNET keeps about it, and only here.
type accountState struct {
	Provider string `json:"p"`           // "steam" | "hytale"
	User     string `json:"u"`           // who started it
	Session  string `json:"s"`           // their c.SessionId then
	At       int64  `json:"at"`          // ms
	Mode     string `json:"m"`           // "web" (the panel's window) | "app" (Matty, §6.5)
	Return   string `json:"r"`           // Steam: the exact return_to; Hytale: the exact redirect_uri
	Nonce    string `json:"n,omitempty"` // Hytale: the OIDC nonce
	Verifier string `json:"v,omitempty"` // Hytale: the PKCE code_verifier
}

func (prov *accountProvider) ticketKey(userID string) string { return prov.id + "_ticket_" + userID }
func (prov *accountProvider) stateKey(state string) string   { return prov.id + "_state_" + state }

// steamProvider and hytaleProvider are the two services. Their hosts are
// package variables of their own files (steamOpenIDBase, steamAPIBase,
// hytaleBase), which the tests point at a fake.
var (
	steamProvider = &accountProvider{
		id: accountSteam, name: "Steam", nameKind: "Steam", linksKey: kvSteamLinks, maxLinks: maxAccountLinks,
		connectPath: steamConnectPath, callbackPath: steamCallbackPath,
		on:          func(cfg *configuration) bool { return cfg.steamOn() },
		setup:       steamSetup,
		callbackURL: steamCallbackURL,
		start:       steamStart,
		declined:    steamDeclined,
		finish:      steamFinish,
	}

	hytaleProvider = &accountProvider{
		id: accountHytale, name: "Hytale", nameKind: "Hytale username", linksKey: kvHytaleLinks, maxLinks: maxAccountLinks,
		connectPath: hytaleConnectPath, callbackPath: hytaleCallbackPath,
		on:          func(cfg *configuration) bool { return cfg.hytaleOn() },
		setup:       hytaleSetup,
		callbackURL: hytaleCallbackURL,
		start:       hytaleStart,
		declined:    hytaleDeclined,
		finish:      hytaleFinish,
	}

	accountProviders = []*accountProvider{steamProvider, hytaleProvider}
)

// accountProviderOf is the service a JSON route under /api/v1/steam/ or
// /api/v1/hytale/ is for.
func accountProviderOf(path string) *accountProvider {
	for _, prov := range accountProviders {
		if strings.HasPrefix(path, "/api/v1/"+prov.id+"/") {
			return prov
		}
	}

	return nil
}

// sameAccountsLive: nothing that changes what the accounts loop does has
// changed (design/3.4.0.md §2.13), so a settings save need not restart it.
func sameAccountsLive(a, b *configuration) bool {
	if a == nil || b == nil {
		return false
	}

	return a.EnableSteam == b.EnableSteam &&
		a.SteamWebAPIKey == b.SteamWebAPIKey &&
		a.EnableHytale == b.EnableHytale &&
		a.HytaleClientID == b.HytaleClientID &&
		a.HytaleClientSecret == b.HytaleClientSecret
}

// accountCredentialShaped is whether a key, a Client ID or a secret can travel
// in a header: printable ASCII with no space, at most 256 bytes. Anything
// else is treated as unset where it would be sent, and the setup sentence
// names it.
func accountCredentialShaped(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}

	for i := 0; i < len(value); i++ {
		if value[i] <= ' ' || value[i] > '~' {
			return false
		}
	}

	return true
}

// accountCallbackURL is the address a service sends people back to:
// Mattermost's Site URL, which must be https unless it is this machine, and
// the plugin's path. Each service words the http case its own way.
func accountCallbackURL(siteURL, path, service, needsHTTPS string) (address, problem string) {
	siteURL = strings.TrimSpace(siteURL)
	if siteURL == "" {
		return "", "Mattermost's Site URL is not set (System Console > Environment > Web Server > Site URL), so " + service + " has nowhere to send people back to."
	}

	parsed, err := url.Parse(siteURL)
	if err != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.User != nil || parsed.Host == "" {
		return "", "The Site URL could not be read as an address."
	}

	switch {
	case parsed.Scheme == "https":
	case parsed.Scheme == "http" && isLocalAccountHost(parsed.Hostname()):
	default:
		return "", needsHTTPS + parsed.Scheme + "://…"
	}

	return strings.TrimRight(siteURL, "/") + "/plugins/" + manifestID + path, ""
}

// isLocalAccountHost is a Site URL on this machine, the one place a sign-in
// may come back over plain http.
func isLocalAccountHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// The link documents (§2.6): one per service, always written by
// compare-and-set. The accounts loop needs every connected id on every pass,
// which rules out a key per person and a KV listing.

type accountLinks struct {
	V       int                    `json:"v"`
	Users   map[string]accountLink `json:"users"`
	Blocked map[string]int64       `json:"blocked,omitempty"` // Mattermost user id -> when, ms
	Lost    map[string]accountLost `json:"lost,omitempty"`    // takeover notices, by previous owner

	// Cleared is when an admin last deleted all of the service's data, ms. A
	// connection whose state was made no later than this is refused when it
	// finishes, however long its conversation with the service took, so
	// nothing started before Delete all is written after it. It sits in the
	// document itself, so the refusal is part of the same compare-and-set as
	// the write it refuses, on every node of a cluster.
	Cleared int64 `json:"cleared,omitempty"`
}

type accountLink struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	At   int64  `json:"at"`
}

// accountLost is a takeover notice. ID is the account that moved, kept only so
// that removing that account removes the notice too (V31); it stays on the
// server and is never in /me, which gets a meLost.
type accountLost struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	At   int64  `json:"at"`
}

type meLost struct {
	Name string `json:"name"`
	At   int64  `json:"at"`
}

// decodeAccountLinks reads the stored bytes; nothing stored is an empty
// document. Bytes that will not decode, or a version this build does not
// write, are errAccountLinksUnreadable.
func decodeAccountLinks(raw []byte) (*accountLinks, error) {
	doc := &accountLinks{V: accountLinksVersion}
	if len(raw) > 0 {
		doc = &accountLinks{}
		if err := json.Unmarshal(raw, doc); err != nil || doc.V != accountLinksVersion {
			return nil, errAccountLinksUnreadable
		}
	}

	if doc.Users == nil {
		doc.Users = map[string]accountLink{}
	}
	if doc.Blocked == nil {
		doc.Blocked = map[string]int64{}
	}
	if doc.Lost == nil {
		doc.Lost = map[string]accountLost{}
	}

	return doc, nil
}

// readAccountLinks reads a service's document fresh, one KV read. errStore
// when the store did not answer, errAccountLinksUnreadable when it answered
// with bytes that will not decode. It never sets them aside: the connecting
// steps, the writers and the loop do.
func (p *Plugin) readAccountLinks(prov *accountProvider) (*accountLinks, []byte, error) {
	raw, appErr := p.API.KVGet(prov.linksKey)
	if appErr != nil {
		return nil, nil, errStore
	}

	doc, err := decodeAccountLinks(raw)

	return doc, raw, err
}

// connectAccountLinks is the link document for a step of connecting. Bytes
// that will not decode are set aside by whoever finds them first, here as in
// the loop, and are never overwritten: nobody can connect that service until
// an admin deletes all of its data.
func (p *Plugin) connectAccountLinks(prov *accountProvider) (*accountLinks, error) {
	doc, raw, err := p.readAccountLinks(prov)
	if errors.Is(err, errAccountLinksUnreadable) {
		p.accounts().markUnreadable(prov, raw)
	}

	return doc, err
}

// owner is the Mattermost user an account is connected to, or "".
func (d *accountLinks) owner(id string) string {
	for userID, link := range d.Users {
		if link.ID == id {
			return userID
		}
	}

	return ""
}

// lostFor is somebody's takeover notice while it is still worth showing,
// without the account's id, which never leaves the server.
func (d *accountLinks) lostFor(userID string, nowMS int64) *meLost {
	lost, found := d.Lost[userID]
	if !found || nowMS-lost.At > accountLostKeepMS {
		return nil
	}

	return &meLost{Name: lost.Name, At: lost.At}
}

// dropNotices deletes every takeover notice that names the account id, and
// says whose they were. A notice holds the name of an account that moved, and
// once that account is disconnected nobody keeps its name: Twitch's purge
// step 5 without a purge worker. It runs inside the same write as every
// removal of an account, so "Disconnect deletes all of it" stays true. The
// person who had the notice loses it.
func (d *accountLinks) dropNotices(id string) []string {
	var dropped []string
	for userID, lost := range d.Lost {
		if id != "" && lost.ID == id {
			delete(d.Lost, userID)
			dropped = append(dropped, userID)
		}
	}

	return dropped
}

// updateAccountLinks is the only way a link document is written: apply works
// on a freshly decoded copy, and says whether it changed anything. It may run
// more than once. Bytes that will not decode are copied aside and left exactly
// where they are, and every caller is told the store could not help. It
// returns the document as it now stands.
//
// A document it wrote becomes the loop's cached one before it returns (§2.13
// rule 1), and the read, the write and that install are one step for this
// node: the service's write lock is held throughout, so two writes landing at
// once are cached in the order they were saved, and the older can never be
// put back over the newer. One that changed nothing installs nothing: what it
// read may already be older than what another write has cached.
func (p *Plugin) updateAccountLinks(prov *accountProvider, nowMS int64, apply func(doc *accountLinks) (bool, error)) (*accountLinks, error) {
	s := p.accounts()
	writing := s.writeLock(prov)
	writing.Lock()
	defer writing.Unlock()

	var result *accountLinks
	var unreadable []byte
	written := false

	err := p.casUpdate(prov.linksKey, func(raw []byte) ([]byte, error) {
		result, unreadable, written = nil, nil, false

		doc, err := decodeAccountLinks(raw)
		if err != nil {
			unreadable = raw
			return nil, err
		}

		changed, err := apply(doc)
		if err != nil {
			return nil, err
		}

		// Old takeover notices go on every write.
		for userID, lost := range doc.Lost {
			if nowMS-lost.At > accountLostKeepMS {
				delete(doc.Lost, userID)
				changed = true
			}
		}

		result = doc
		if !changed {
			return nil, nil
		}

		written = true

		return json.Marshal(doc)
	})
	if errors.Is(err, errAccountLinksUnreadable) {
		s.markUnreadable(prov, unreadable)
	}
	if err != nil {
		return nil, err
	}

	if written {
		s.installLinks(prov, result)
	}

	return result, nil
}

// setAccountLink connects the proved account to userID. startedMS is when the
// connection's state was made: one made no later than the last Delete all is
// errAccountCleared. previous is whoever had that account before, when it was
// taken from them, and told the people whose takeover notices went with the
// person's own earlier account.
func (p *Plugin) setAccountLink(prov *accountProvider, userID string, proof accountProof, startedMS, nowMS int64) (doc *accountLinks, previous string, told []string, err error) {
	doc, err = p.updateAccountLinks(prov, nowMS, func(doc *accountLinks) (bool, error) {
		previous, told = "", nil

		if startedMS <= doc.Cleared {
			return false, errAccountCleared
		}

		if _, blocked := doc.Blocked[userID]; blocked {
			return false, errAccountBlocked
		}

		name := proof.Name

		// A verified proof wins, as for Twitch and stream keys, and the
		// person who lost the account is told.
		if owner := doc.owner(proof.ID); owner != "" && owner != userID {
			if name == "" {
				name = doc.Users[owner].Name
			}
			delete(doc.Users, owner)
			doc.Lost[owner] = accountLost{ID: proof.ID, Name: name, At: nowMS}
			previous = owner
		}

		// Connecting again answers any notice of one's own.
		delete(doc.Lost, userID)

		old, had := doc.Users[userID]
		if !had && len(doc.Users) >= prov.maxLinks {
			return false, errAccountFull
		}

		switch {
		case had && old.ID != proof.ID:
			// Replacing one's own account with another: the old one is
			// connected to nobody now, so nobody keeps a notice naming it.
			told = doc.dropNotices(old.ID)
		case had && name == "":
			// The same account again, with Steam's name late: keep the one
			// already known rather than show none.
			name = old.Name
		}

		doc.Users[userID] = accountLink{ID: proof.ID, Name: name, At: nowMS}

		return true, nil
	})

	return doc, previous, told, err
}

// disconnectAccount takes somebody's connection away, and with block keeps
// them from connecting again. It changes nothing for somebody with nothing to
// take, so pressing Disconnect twice is harmless. Whatever the switch says.
//
// Nothing to take is nothing at all: no install, no new view, no kick and no
// event. The route is open to every session and not metered, and a kick
// brings Steam's next call forward, so a Disconnect that changes nothing must
// not be a way to have Steam asked every ten seconds instead of every minute.
func (p *Plugin) disconnectAccount(prov *accountProvider, userID string, block bool, nowMS int64) error {
	removed := ""
	changed := false
	var told []string

	_, err := p.updateAccountLinks(prov, nowMS, func(doc *accountLinks) (bool, error) {
		removed, told, changed = "", nil, false

		if link, connected := doc.Users[userID]; connected {
			delete(doc.Users, userID)
			told = doc.dropNotices(link.ID)
			removed = link.ID
			changed = true
		}

		if block {
			if _, already := doc.Blocked[userID]; !already {
				if len(doc.Blocked) >= maxAccountBlocked {
					return false, errAccountBlockedFull
				}
				doc.Blocked[userID] = nowMS
				changed = true
			}
		}

		return changed, nil
	})
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}

	if removed != "" && prov.id == accountSteam {
		p.accounts().forgetSteam(removed)
	}
	p.accountsChanged()

	p.tellAccountChanged(userID)
	for _, other := range told {
		p.tellAccountChanged(other)
	}

	return nil
}

// unblockAccount lets somebody connect again. Somebody not blocked is told
// nothing.
func (p *Plugin) unblockAccount(prov *accountProvider, userID string, nowMS int64) error {
	unblocked := false

	_, err := p.updateAccountLinks(prov, nowMS, func(doc *accountLinks) (bool, error) {
		_, unblocked = doc.Blocked[userID]
		delete(doc.Blocked, userID)

		return unblocked, nil
	})
	if err != nil || !unblocked {
		return err
	}

	p.accountsChanged()
	p.tellAccountChanged(userID)

	return nil
}

// clearAccountBlocks empties a service's block list, and tells each person
// who was on it.
func (p *Plugin) clearAccountBlocks(prov *accountProvider, nowMS int64) error {
	var unblocked []string

	_, err := p.updateAccountLinks(prov, nowMS, func(doc *accountLinks) (bool, error) {
		unblocked = nil
		if len(doc.Blocked) == 0 {
			return false, nil
		}
		for userID := range doc.Blocked {
			unblocked = append(unblocked, userID)
		}
		doc.Blocked = map[string]int64{}

		return true, nil
	})
	if err != nil || len(unblocked) == 0 {
		return err
	}

	p.accountsChanged()
	for _, userID := range unblocked {
		p.tellAccountChanged(userID)
	}

	return nil
}

// dismissAccountNotice forgets somebody's takeover notice once they have read
// it. Somebody with no notice is told nothing.
func (p *Plugin) dismissAccountNotice(prov *accountProvider, userID string, nowMS int64) error {
	dismissed := false

	_, err := p.updateAccountLinks(prov, nowMS, func(doc *accountLinks) (bool, error) {
		_, dismissed = doc.Lost[userID]
		delete(doc.Lost, userID)

		return dismissed, nil
	})
	if err != nil || !dismissed {
		return err
	}

	p.tellAccountChanged(userID)

	return nil
}

// renameAccountLinks writes back names the accounts loop read (Steam only:
// Hytale names change only by connecting again). renames is keyed by
// Mattermost user, each with the account id the read was about and the name
// it said. A name changes only where that person is still connected to that
// same account in the fresh document, and an entry is never created, so a
// rename can never bring back a connection deleted while the read was out
// (§2.6, §2.13 rule 4). Names must already have passed cleanText.
func (p *Plugin) renameAccountLinks(prov *accountProvider, renames map[string]accountLink, nowMS int64) (*accountLinks, error) {
	if len(renames) == 0 {
		return nil, nil
	}

	doc, err := p.updateAccountLinks(prov, nowMS, func(doc *accountLinks) (bool, error) {
		changed := false
		for userID, renamed := range renames {
			link, connected := doc.Users[userID]
			if !connected || link.ID != renamed.ID || link.Name == renamed.Name || renamed.Name == "" {
				continue
			}
			link.Name = renamed.Name
			doc.Users[userID] = link
			changed = true
		}

		return changed, nil
	})
	if err != nil {
		return nil, err
	}

	return doc, nil
}

// deleteAllAccountData is Delete all Steam data or Delete all Hytale data
// (§2.7), whatever the switch says. The loop is stopped and waited for first,
// under runLock with nothing else held, so no read in flight can install
// anything afterwards, and started again only if it was running: it runs
// whenever a switch is on and the plugin is active, and never because an
// admin deleted something. The block list survives, in a document that holds
// nothing else, written by compare-and-set like every other write, so a block
// saved at the same moment is kept. A read the store refuses writes nothing at
// all, and the admin presses again: writing a fresh document then would empty
// the block list without a word. A document that will not decode cannot say
// who was blocked, and goes.
//
// The fresh document also says when it was written (Cleared), and is written
// even where nothing was stored: a connection already past its state, still
// talking to the service, would otherwise write its account back the moment
// after the admin was told everything was gone, the first one included.
// setAccountLink refuses every connection started no later than that.
//
// Both locks are let go by a defer, so that a panic on the way, which
// ServeHTTP turns into one refused request, does not leave the loop and the
// service's link writes stopped until the plugin is restarted.
func (p *Plugin) deleteAllAccountData(prov *accountProvider, now time.Time) error {
	s := p.accounts()

	keysErr, err := func() (error, error) {
		s.runLock.Lock()
		defer s.runLock.Unlock()

		wasRunning := s.cancel != nil
		s.stopLoopLocked()

		err := func() error {
			// Rule 1 as for every other link write: the fresh document is
			// saved and cached as one step, so no write landing meanwhile is
			// cached over it.
			writing := s.writeLock(prov)
			writing.Lock()
			defer writing.Unlock()

			fresh := &accountLinks{V: accountLinksVersion, Users: map[string]accountLink{}, Blocked: map[string]int64{}, Lost: map[string]accountLost{}, Cleared: now.UnixMilli()}
			err := p.casUpdate(prov.linksKey, func(raw []byte) ([]byte, error) {
				fresh.Blocked = map[string]int64{}
				if doc, err := decodeAccountLinks(raw); err == nil {
					fresh.Blocked = doc.Blocked
				}

				return json.Marshal(fresh)
			})
			if err != nil {
				return err
			}

			// The connections are gone from here on, whatever happens to the
			// keys below: memory lets go of them, and every panel is told,
			// even when a ticket, a state or the set-aside copy has to wait
			// for the next press.
			s.clearService(prov)
			s.installLinks(prov, fresh)

			return nil
		}()

		var keysErr error
		if err == nil {
			keysErr = p.deleteAccountKeys(prov)
		}

		if cfg := p.getConfiguration(); wasRunning && (cfg.steamOn() || cfg.hytaleOn()) {
			s.startLoopLocked()
		}

		return keysErr, err
	}()
	if err != nil {
		return err
	}

	p.accountsChanged()

	// Everybody's open panels read /me again.
	p.tellEverybodyMe()

	return keysErr
}

// deleteAccountKeys deletes a service's set-aside copy, and every ticket,
// state and Steam nonce record. Tickets and states expire on their own within
// ten minutes; they go now all the same, so a connection that has not reached
// its callback yet cannot reach it. One already past it holds its state in
// memory, and is stopped by the document's Cleared instead (setAccountLink).
func (p *Plugin) deleteAccountKeys(prov *accountProvider) error {
	var first error

	if appErr := p.API.KVDelete(kvQuarantinePrefix + prov.linksKey); appErr != nil {
		first = errStore
	}

	prefixes := []string{prov.id + "_ticket_", prov.id + "_state_"}
	if prov.id == accountSteam {
		prefixes = append(prefixes, kvSteamNoncePrefix)
	}

	for _, key := range p.kvKeysWithPrefix(prov.id + "_") {
		for _, prefix := range prefixes {
			if strings.HasPrefix(key, prefix) {
				if appErr := p.API.KVDelete(key); appErr != nil && first == nil {
					first = errStore
				}
			}
		}
	}

	return first
}

// accountQuarantined is whether a set-aside copy of a service's connections
// exists: one read by name, so a listing cannot miss it.
func (p *Plugin) accountQuarantined(prov *accountProvider) bool {
	raw, appErr := p.API.KVGet(kvQuarantinePrefix + prov.linksKey)

	return appErr == nil && len(raw) > 0
}

// accountLinksProblem is the admin's sentence for a document set aside.
func accountLinksProblem(prov *accountProvider) string {
	return "The list of " + prov.name + " connections could not be read and was set aside (" + kvQuarantinePrefix + prov.linksKey +
		"). Delete all " + prov.name + " data to start again."
}

// tellAccountChanged tells somebody's open panels to read /me again. Nothing
// is in the event, so it can never be the thing that changes anything.
func (p *Plugin) tellAccountChanged(userID string) {
	if userID == "" {
		return
	}

	p.tellMe(userID)
}

// accountOffer is whether Connect can be offered to userID right now: the
// switch, the setup, the link document, the block and, with room, the cap.
// It asks the service nothing.
func (p *Plugin) accountOffer(prov *accountProvider, cfg *configuration, userID string, room bool) *accountRefusal {
	if !prov.on(cfg) {
		return accountRefused(accountPageOff, "")
	}

	if problem := prov.setup(p, cfg); problem != "" {
		refusal := accountRefused(accountPageSetup, "")
		refusal.problem = problem

		return refusal
	}

	doc, err := p.connectAccountLinks(prov)
	if err != nil {
		return accountRefused(accountPageStore, "")
	}
	if _, blocked := doc.Blocked[userID]; blocked {
		return accountRefused(accountPageBlocked, "")
	}
	if _, connected := doc.Users[userID]; room && !connected && len(doc.Users) >= prov.maxLinks {
		return accountRefused(accountPageFull, "")
	}

	return nil
}

// accountAccessRefusal is the lock's rule for adding a connection: linked
// people only. A store that does not answer is the store page, never a no.
func (p *Plugin) accountAccessRefusal(userID string) (*accountRefusal, access) {
	level, _, err := p.accessOf(userID)
	switch {
	case err != nil:
		return accountRefused(accountPageStore, ""), accessLocked
	case level != accessLinked:
		return accountRefused(accountPageLocked, ""), level
	default:
		return nil, level
	}
}

// handleAccountTicket answers POST /api/v1/{steam,hytale}/connect-ticket (T1-T7).
// The gate has already found the caller linked.
func (p *Plugin) handleAccountTicket(c *plugin.Context, w http.ResponseWriter, prov *accountProvider, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.interactiveSession(c, userID) {
		p.writeAccountCode(w, prov, accountCodeInteractiveOnly, "", userID)
		return
	}

	cfg := p.getConfiguration()
	if refusal := p.accountOffer(prov, cfg, userID, false); refusal != nil {
		p.writeAccountCode(w, prov, refusal.code, refusal.problem, userID)
		return
	}

	s := p.accounts()
	now := s.now()

	// The only place a web attempt is counted: a ticket is what a
	// connection cannot start without.
	if !s.connectLimiter(prov).allowAt(userID, now) {
		writeTooMany(w, accountCodeText(prov, accountCodeRateLimited, p.brandName()), s.connectLimiter(prov), userID, now)
		return
	}

	ticket, err := randomHex(32)
	if err != nil {
		p.writeAccountCode(w, prov, accountCodeStore, "", userID)
		return
	}

	record, err := json.Marshal(accountTicket{Hash: hashHex(ticket), Session: c.SessionId, At: now.UnixMilli()})
	if err != nil {
		p.writeAccountCode(w, prov, accountCodeStore, "", userID)
		return
	}

	// One per person per service: a new one replaces the old.
	if appErr := p.API.KVSetWithExpiry(prov.ticketKey(userID), record, accountTicketSeconds); appErr != nil {
		p.writeAccountCode(w, prov, accountCodeStore, "", userID)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ticket": ticket, "expiresIn": accountTicketSeconds})
}

// serveAccountConnect answers GET /api/v1/{steam,hytale}/connect?t=<ticket>
// (C1-C10). Until the ticket is spent it writes nothing, counts nothing and
// redirects nowhere.
func (p *Plugin) serveAccountConnect(c *plugin.Context, w http.ResponseWriter, r *http.Request, prov *accountProvider) {
	twitchPageHeaders(w)

	if r.URL.Path != prov.connectPath || r.Method != http.MethodGet {
		p.writeAccountPage(w, prov, accountRefused(accountPageStale, "").withStatus(http.StatusNotFound), "")
		return
	}

	// A cheap extra layer, as for Twitch: a browser that says what it is
	// fetching, and says it is not a page opened by this site or by the
	// desktop app's own window, is not somebody pressing Continue.
	if dest := r.Header.Get("Sec-Fetch-Dest"); dest != "" && dest != "document" {
		p.writeAccountPage(w, prov, accountRefused(accountPageStale, ""), "")
		return
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		p.writeAccountPage(w, prov, accountRefused(accountPageStale, ""), "")
		return
	}

	userID := r.Header.Get("Mattermost-User-Id")
	if userID == "" || !p.interactiveSession(c, userID) {
		p.writeAccountPage(w, prov, accountRefused(accountPageSignIn, ""), "")
		return
	}

	// Connecting is for linked people, which this route, above the gate,
	// asks for itself.
	if refusal, _ := p.accountAccessRefusal(userID); refusal != nil {
		p.writeAccountPage(w, prov, refusal, userID)
		return
	}

	ticket := r.URL.Query().Get("t")
	if !accountHexToken.MatchString(ticket) {
		p.writeAccountPage(w, prov, accountRefused(accountPageStale, ""), userID)
		return
	}

	key := prov.ticketKey(userID)
	raw, appErr := p.API.KVGet(key)
	if appErr != nil {
		p.writeAccountPage(w, prov, accountRefused(accountPageStore, ""), userID)
		return
	}
	if len(raw) == 0 {
		p.writeAccountPage(w, prov, accountRefused(accountPageStale, ""), userID)
		return
	}

	// Whoever sent a wrong ticket cannot spend the real one: it is left
	// exactly where it is.
	var stored accountTicket
	if json.Unmarshal(raw, &stored) != nil ||
		subtle.ConstantTimeCompare([]byte(hashHex(ticket)), []byte(stored.Hash)) != 1 ||
		subtle.ConstantTimeCompare([]byte(stored.Session), []byte(c.SessionId)) != 1 {
		p.writeAccountPage(w, prov, accountRefused(accountPageStale, ""), userID)
		return
	}

	// Spent: the first write, and only the caller's own valid ticket reaches it.
	spent, appErr := p.API.KVCompareAndDelete(key, raw)
	if appErr != nil {
		p.writeAccountPage(w, prov, accountRefused(accountPageStore, ""), userID)
		return
	}
	if !spent {
		p.writeAccountPage(w, prov, accountRefused(accountPageStale, ""), userID)
		return
	}

	cfg := p.getConfiguration()
	if refusal := p.accountOffer(prov, cfg, userID, true); refusal != nil {
		p.writeAccountPage(w, prov, refusal, userID)
		return
	}

	location, refusal := p.startAccountState(prov, cfg, userID, c.SessionId, accountModeWeb)
	if refusal != nil {
		p.writeAccountPage(w, prov, refusal, userID)
		return
	}

	w.Header().Set("Location", location)
	w.WriteHeader(http.StatusFound)
}

// startAccountState makes a state for a connection starting now (C8-C9,
// AC5-AC6) and says where the browser or Matty's window goes next.
func (p *Plugin) startAccountState(prov *accountProvider, cfg *configuration, userID, sessionID, mode string) (string, *accountRefusal) {
	state, err := randomHex(32)
	if err != nil {
		return "", accountRefused(accountPageStore, "")
	}

	st := accountState{Provider: prov.id, User: userID, Session: sessionID, At: p.accounts().now().UnixMilli(), Mode: mode}

	location, err := prov.start(p, cfg, state, &st)
	if err != nil {
		refusal := accountRefused(accountPageSetup, "")
		refusal.problem = prov.setup(p, cfg)

		return "", refusal
	}

	record, err := json.Marshal(st)
	if err != nil {
		return "", accountRefused(accountPageStore, "")
	}
	if appErr := p.API.KVSetWithExpiry(prov.stateKey(state), record, accountTicketSeconds); appErr != nil {
		return "", accountRefused(accountPageStore, "")
	}

	return location, nil
}

// spendAccountState is K2-K4 (and AF2-AF4): the state's shape, its record,
// its service and its age, and then it is spent, whatever happens next.
// Nothing here asks the service anything, so nobody can make the server call
// Steam or Hytale without a live state of their own.
func (p *Plugin) spendAccountState(prov *accountProvider, query url.Values, nowMS int64) (string, accountState, *accountRefusal) {
	// The only state in the query: two would let what VillaNET spends and
	// what the service signed be different values.
	states := query["state"]
	if len(states) != 1 || !accountHexToken.MatchString(states[0]) {
		return "", accountState{}, accountRefused(accountPageExpired, "")
	}
	state := states[0]

	key := prov.stateKey(state)
	raw, appErr := p.API.KVGet(key)
	if appErr != nil {
		return "", accountState{}, accountRefused(accountPageStore, "")
	}
	if len(raw) == 0 {
		return "", accountState{}, accountRefused(accountPageExpired, "")
	}

	// The age is checked here, because a compare-and-delete does not look at
	// whether a key has expired.
	var st accountState
	if json.Unmarshal(raw, &st) != nil || st.Provider != prov.id || st.User == "" ||
		nowMS-st.At > accountStateMS || st.At > nowMS+accountStateMS {
		_, _ = p.API.KVCompareAndDelete(key, raw)
		return "", accountState{}, accountRefused(accountPageExpired, "")
	}

	spent, appErr := p.API.KVCompareAndDelete(key, raw)
	if appErr != nil {
		return "", accountState{}, accountRefused(accountPageStore, "")
	}
	if !spent {
		return "", accountState{}, accountRefused(accountPageExpired, "")
	}

	return state, st, nil
}

// serveAccountCallback answers GET /api/v1/{steam,hytale}/callback (K1-K13),
// where the service sends the person's browser back.
func (p *Plugin) serveAccountCallback(c *plugin.Context, w http.ResponseWriter, r *http.Request, prov *accountProvider) {
	twitchPageHeaders(w)

	if r.URL.Path != prov.callbackPath || r.Method != http.MethodGet {
		p.writeAccountPage(w, prov, accountRefused(accountPageExpired, "").withStatus(http.StatusNotFound), "")
		return
	}

	query := r.URL.Query()
	s := p.accounts()
	now := s.now()

	state, st, refusal := p.spendAccountState(prov, query, now.UnixMilli())
	if refusal != nil {
		p.writeAccountPage(w, prov, refusal, "")
		return
	}

	// A sign-in started by Matty can only be finished by Matty (§6.5). A
	// browser that arrives here with such a state, by accident or because
	// somebody sent it a link, links nothing, and the state is gone.
	if st.Mode == accountModeApp {
		p.writeAccountPage(w, prov, accountRefused(accountPageApp, ""), "")
		return
	}

	// The account-linking defence: the person who started this, in their
	// own interactive session, is the only one who may finish it.
	userID := r.Header.Get("Mattermost-User-Id")
	if userID == "" || !p.interactiveSession(c, userID) {
		p.writeAccountPage(w, prov, accountRefused(accountPageSignIn, ""), "")
		return
	}
	if userID != st.User {
		p.writeAccountPage(w, prov, accountRefused(accountPageOtherAccount, ""), "")
		return
	}

	// A state stays good for ten minutes, and a let-out or an unlink inside
	// them must still stop the connection.
	if refusal, _ := p.accountAccessRefusal(userID); refusal != nil {
		p.writeAccountPage(w, prov, refusal, userID)
		return
	}

	proof, previous, refusal := p.finishAccount(r.Context(), prov, userID, state, st, query, now)
	if refusal != nil {
		p.writeAccountPage(w, prov, refusal, userID)
		return
	}

	p.writeAccountPageData(w, prov, http.StatusOK, accountPageConnected, accountPageData{
		Name:      proof.Name,
		TakenOver: previous != "",
		Hidden:    prov.id == accountSteam && proof.Hidden,
	})
}

// finishAccount is K8-K12 (AF7-AF11), shared by the browser's callback and
// Matty's app-finish once each has spent the state and checked who is asking:
// the service's own "came back without an account", the switch and the
// block, the proof, the link, and telling everybody who needs to know.
func (p *Plugin) finishAccount(ctx context.Context, prov *accountProvider, userID, state string, st accountState, query url.Values, now time.Time) (accountProof, string, *accountRefusal) {
	cfg := p.getConfiguration()

	if refusal := prov.declined(query); refusal != nil {
		p.noteAccountRefusal(prov, userID, refusal)
		return accountProof{}, "", refusal
	}

	if !prov.on(cfg) {
		return accountProof{}, "", accountRefused(accountPageOff, "")
	}

	doc, err := p.connectAccountLinks(prov)
	if err != nil {
		return accountProof{}, "", accountRefused(accountPageStore, "")
	}
	if _, blocked := doc.Blocked[userID]; blocked {
		return accountProof{}, "", accountRefused(accountPageBlocked, "")
	}

	finishCtx, cancel := context.WithTimeout(ctx, accountCallbackBudget)
	defer cancel()

	proof, refusal := prov.finish(finishCtx, p, cfg, state, st, query, now)
	if refusal != nil {
		p.noteAccountRefusal(prov, userID, refusal)
		return accountProof{}, "", refusal
	}

	// The service proved an account, so the application itself works.
	s := p.accounts()
	s.noteSignInProblem(prov.id, "")

	// The conversation with the service can take its whole budget, and the
	// switch can go off meanwhile: nothing is connected while it is off.
	if !prov.on(p.getConfiguration()) {
		return accountProof{}, "", accountRefused(accountPageOff, "")
	}

	// The write caches its own result (§2.13 rule 1), and a connection that
	// started before an admin deleted everything is refused inside it.
	_, previous, told, err := p.setAccountLink(prov, userID, proof, st.At, now.UnixMilli())
	switch {
	case errors.Is(err, errAccountFull):
		return accountProof{}, "", accountRefused(accountPageFull, "")
	case errors.Is(err, errAccountBlocked):
		return accountProof{}, "", accountRefused(accountPageBlocked, "")
	case errors.Is(err, errAccountCleared):
		return accountProof{}, "", accountRefused(accountPageExpired, "")
	case err != nil:
		return accountProof{}, "", accountRefused(accountPageStore, "")
	}

	// Then the view, built from the cache. A new Steam connection shows as
	// Steam's answer said until the loop's next read, which the kick brings
	// within seconds.
	if prov.id == accountSteam {
		s.seedSteam(proof)
	}
	p.accountsChanged()

	p.tellAccountChanged(userID)
	if previous != "" {
		p.tellAccountChanged(previous)
	}
	for _, other := range told {
		p.tellAccountChanged(other)
	}

	p.API.LogInfo("Somebody connected their "+prov.name+" account", "user_id", userID)

	return proof, previous, nil
}

// noteAccountRefusal logs why a connection was refused: once per kind, with
// the user id, a fixed reason word and fixed values only. Nothing the service
// sent is ever in it. A refusal that says the admin's application is at fault
// is remembered for the admin's /me.
func (p *Plugin) noteAccountRefusal(prov *accountProvider, userID string, refusal *accountRefusal) {
	if refusal.signIn != "" {
		p.accounts().noteSignInProblem(prov.id, refusal.signIn)
	}
	if refusal.reason == "" {
		return
	}

	pairs := append([]any{"reason", refusal.reason, "user_id", userID}, refusal.logged...)
	p.accounts().warnOnce(prov.id+"-refused-"+refusal.reason,
		prov.name+" did not confirm a new connection, so nothing was connected.", pairs...)
}

// handleAccountAppConnect answers POST /api/v1/{steam,hytale}/app-connect
// (AC1-AC7), Matty's start of a connection in its own sign-in window. No
// ticket: a ticket protects a GET that a page could fire with somebody's
// cookie, and this state is made by a Bearer POST only the session's holder
// can send.
func (p *Plugin) handleAccountAppConnect(c *plugin.Context, w http.ResponseWriter, prov *accountProvider, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.interactiveSession(c, userID) {
		p.writeAccountCode(w, prov, accountCodeInteractiveOnly, "", userID)
		return
	}

	cfg := p.getConfiguration()
	if refusal := p.accountOffer(prov, cfg, userID, true); refusal != nil {
		p.writeAccountCode(w, prov, refusal.code, refusal.problem, userID)
		return
	}

	s := p.accounts()
	if now := s.now(); !s.connectLimiter(prov).allowAt(userID, now) {
		writeTooMany(w, accountCodeText(prov, accountCodeRateLimited, p.brandName()), s.connectLimiter(prov), userID, now)
		return
	}

	finishAt, problem := prov.callbackURL(p.siteURL())
	if problem != "" {
		p.writeAccountCode(w, prov, accountCodeSetup, problem, userID)
		return
	}

	location, refusal := p.startAccountState(prov, cfg, userID, c.SessionId, accountModeApp)
	if refusal != nil {
		p.writeAccountCode(w, prov, refusal.code, refusal.problem, userID)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"url": location, "finishAt": finishAt, "expiresIn": accountTicketSeconds})
}

// handleAccountAppFinish answers POST /api/v1/{steam,hytale}/app-finish
// (AF1-AF11): Matty hands over the raw query of the address its window caught
// and did not load.
func (p *Plugin) handleAccountAppFinish(c *plugin.Context, w http.ResponseWriter, r *http.Request, prov *accountProvider, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.interactiveSession(c, userID) {
		p.writeAccountCode(w, prov, accountCodeInteractiveOnly, "", userID)
		return
	}

	var body struct {
		Query string `json:"query"`
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxAccountAppFinishBytes+1))
	if err != nil || len(raw) > maxAccountAppFinishBytes || json.Unmarshal(raw, &body) != nil {
		p.writeAccountCode(w, prov, accountCodeBadRequest, "", userID)
		return
	}
	query, err := url.ParseQuery(body.Query)
	if err != nil {
		p.writeAccountCode(w, prov, accountCodeBadRequest, "", userID)
		return
	}

	s := p.accounts()
	now := s.now()

	state, st, refusal := p.spendAccountState(prov, query, now.UnixMilli())
	if refusal != nil {
		p.writeAccountCode(w, prov, refusal.code, "", userID)
		return
	}

	// A state made by the web flow can never be finished here, and a state
	// made by one session can never be finished by another.
	if st.Mode != accountModeApp || st.User != userID ||
		subtle.ConstantTimeCompare([]byte(st.Session), []byte(c.SessionId)) != 1 {
		p.writeAccountCode(w, prov, accountCodeOtherSession, "", userID)
		return
	}

	if refusal, level := p.accountAccessRefusal(userID); refusal != nil {
		code := refusal.code
		if level == accessLetIn {
			code = accessCodeWatchOnly
		}
		p.writeAccountCode(w, prov, code, "", userID)

		return
	}

	if _, _, refusal := p.finishAccount(r.Context(), prov, userID, state, st, query, now); refusal != nil {
		p.writeAccountCode(w, prov, refusal.code, refusal.problem, userID)
		return
	}

	cfg := p.getConfiguration()
	account := p.meAccountFor(prov, userID, cfg, p.isSystemAdmin(userID))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "account": account})
}

// handleAccountUnlink answers DELETE /api/v1/{steam,hytale}/link: the caller's
// own Disconnect. Open to everybody, the lock screen included, and working
// whatever the switch says, because a person can always take their own
// account back, and Steam's terms say to delete on ending.
func (p *Plugin) handleAccountUnlink(w http.ResponseWriter, prov *accountProvider, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if err := p.disconnectAccount(prov, userID, false, p.accounts().now().UnixMilli()); err != nil {
		p.API.LogWarn("Could not disconnect somebody's "+prov.name+" account", "user_id", userID, "error", err.Error())
		writeCodedError(w, http.StatusServiceUnavailable, p.brandName()+" could not disconnect "+prov.name+" just now. Try again in a moment.", accountCodeStore)

		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAccountNotice answers POST /api/v1/{steam,hytale}/notice: the caller
// has read that their account was connected to somebody else.
func (p *Plugin) handleAccountNotice(w http.ResponseWriter, prov *accountProvider, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if err := p.dismissAccountNotice(prov, userID, p.accounts().now().UnixMilli()); err != nil {
		writeCodedError(w, http.StatusServiceUnavailable, accountCodeText(prov, accountCodeStore, p.brandName()), accountCodeStore)
		return
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// meAccount is what /me says about the caller's own Steam or Hytale account:
// present whenever that switch is on, and whenever the caller has a
// connection or a takeover notice even while it is off, so Disconnect can
// always be reached. Problem is for system admins only.
type meAccount struct {
	Available bool    `json:"available"` // the switch on and set up: Connect can be offered
	Connected bool    `json:"connected,omitempty"`
	Name      string  `json:"name,omitempty"`    // Steam name or Hytale username, while connected
	URL       string  `json:"url,omitempty"`     // Steam only: https://steamcommunity.com/profiles/<id>
	Hidden    bool    `json:"hidden,omitempty"`  // Steam only: the profile is not public (from the loop's last read)
	Blocked   bool    `json:"blocked,omitempty"` // an admin blocked this person
	Lost      *meLost `json:"lost,omitempty"`    // {name, at} only: their account moved to someone else
	Problem   string  `json:"problem,omitempty"` // system admins only
}

// meAccountFor is /me's steam or hytale, built like meTwitchFor: one fresh KV
// read of that service's document per /me, whatever the switch says, so a
// connection shows at once. With the switch off, that read is the only thing
// /me does for the service.
func (p *Plugin) meAccountFor(prov *accountProvider, userID string, cfg *configuration, admin bool) *meAccount {
	on := prov.on(cfg)
	s := p.accounts()
	nowMS := s.now().UnixMilli()

	// Asked only while the switch is on: off, nothing is available whatever
	// the setup.
	setup := ""
	if on {
		setup = prov.setup(p, cfg)
	}

	doc, _, err := p.readAccountLinks(prov)
	if err != nil {
		if !on {
			return nil
		}

		answer := &meAccount{Available: setup == ""}
		if admin {
			switch {
			case setup != "":
				answer.Problem = setup
			case errors.Is(err, errAccountLinksUnreadable):
				answer.Problem = accountLinksProblem(prov)
			default:
				answer.Problem = "The list of " + prov.name + " connections could not be read just now."
			}
		}

		return answer
	}

	link, connected := doc.Users[userID]
	lost := doc.lostFor(userID, nowMS)
	if !on && !connected && lost == nil {
		return nil
	}

	answer := &meAccount{Available: on && setup == "", Lost: lost}
	if _, blocked := doc.Blocked[userID]; blocked {
		answer.Blocked = true
	}
	if connected {
		answer.Connected = true
		answer.Name = link.Name
		if prov.id == accountSteam {
			answer.URL = steamProfileURL(link.ID)
			answer.Hidden = s.steamHidden(link.ID)
		}
	}

	if admin && on {
		answer.Problem = setup
		if answer.Problem == "" {
			answer.Problem = s.adminProblem(prov)
		}
	}

	return answer
}

// The pages connect and callback answer with (§2.8). The same headers, CSP,
// styling and close script as Twitch's: twitchPageHeaders and
// twitchPageScript are Twitch's own, so the two sets of pages can never
// drift apart, and the script's hash in the CSP is the same one. Each page is
// one template, so every value is escaped and the service's name is filled in
// by the template, never by putting strings together into HTML.

type accountPageData struct {
	Brand     string // the server's name for the plugin (brandName)
	Service   string // "Steam" | "Hytale"
	NameKind  string // "Steam" | "Hytale username"
	Name      string // on the connected page
	TakenOver bool
	Hidden    bool   // Steam only: the profile is not public
	Problem   string // for system admins only
	Close     bool
}

// accountPageWords is each page's heading and body, as template text.
var accountPageWords = map[string][2]string{
	accountPageConnected: {"{{.Service}} connected",
		`<p>{{if .Name}}Connected as <strong>{{.Name}}</strong> ({{.NameKind}}).{{else}}Your {{.Service}} account is connected.{{end}} You can close this window and go back to {{.Brand}}.</p>
{{if .TakenOver}}<p>It was connected to another Mattermost account before. It is connected to yours now.</p>
{{end}}{{if .Hidden}}<p>Your Steam profile is not public, so people here will see you as offline. To share your status and game, set your profile and its game details to Public in Steam's privacy settings.</p>
{{end}}`},
	accountPageCancelled: {"Not connected",
		`<p>You cancelled on {{.Service}}, so nothing was connected. You can close this window.</p>
`},
	accountPageExpired: {"This link has run out",
		`<p>This {{.Service}} connection was already used, or took longer than 10 minutes. Close this window and press Connect {{.Service}} again. Do not share this page's address.</p>
`},
	accountPageStale: {"Start from {{.Brand}}",
		`<p>This window was not opened by Connect {{.Service}} in the {{.Brand}} panel, or it was opened twice. Close it and press Connect {{.Service}} in the panel.</p>
`},
	accountPageSignIn: {"Sign in first",
		`<p>Sign in to Mattermost in this browser, then press Connect {{.Service}} in the {{.Brand}} panel again. Do not share this page's address.</p>
`},
	accountPageOtherAccount: {"Started by another account",
		`<p>This connection was started while signed in to Mattermost as someone else. Close this window and press Connect {{.Service}} again while signed in as yourself. Do not share this page's address.</p>
`},
	accountPageApp: {"Finish in Matty",
		`<p>This sign-in was started in the Matty app, so it can only be finished there. Close this window and press Connect {{.Service}} in Matty again. Do not share this page's address.</p>
`},
	accountPageLocked: {"Not allowed",
		`<p>Connect {{.Service}} is for people who have unlocked {{.Brand}} with their bearer token. Unlock it in the {{.Brand}} panel, then press Connect {{.Service}} again.</p>
`},
	accountPageBlocked: {"Not allowed",
		`<p>An admin has turned off Connect {{.Service}} for your account.</p>
`},
	accountPageOff: {"{{.Service}} is switched off",
		`<p>Connecting {{.Service}} accounts is switched off on this server.</p>
`},
	accountPageSetup: {"Not set up yet",
		`<p>An admin needs to finish setting up {{.Service}}.</p>
`},
	accountPageFull: {"No room",
		`<p>This server already has 100 {{.Service}} accounts connected, which is as many as it keeps.</p>
`},
	accountPageProviderError: {"{{.Service}} did not confirm it",
		`<p>{{.Service}} did not confirm the account just now. Close this window and try again in a minute.</p>
`},
	accountPageBusy: {"Steam is busy",
		`<p>Steam asked {{.Brand}} to wait before checking another sign-in. Close this window and try again in a couple of minutes.</p>
`},
	accountPageNoProfile: {"No game profile",
		`<p>That Hytale account has no game profile to show. Sign in with the account you play with, and pick a profile when Hytale asks.</p>
`},
	accountPageScope: {"Not approved yet",
		`<p>Hytale has not approved this server's application to read game profiles yet, so nothing was connected.</p>
`},
	accountPageStore: {"Could not save",
		`<p>{{.Brand}} could not save the connection just now. Try again in a minute.</p>
`},
}

// accountPageTemplate is the page every one of them shares. The style is
// Twitch's, a copy of twitchPageTemplate's, and the one script is Twitch's.
var accountPageTemplate = template.Must(template.New("account").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer">
<title>{{.Brand}} · {{template "heading" .}}</title>
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
<h1>{{template "heading" .}}</h1>
{{template "body" .}}{{if .Problem}}<p class="problem">{{.Problem}}</p>
{{end}}</main>
{{if .Close}}<script>` + twitchPageScript + `</script>
{{end}}</body>
</html>
`))

// accountPages is each page's own template: the shared one with its heading
// and body defined, made once at start-up.
var accountPages = func() map[string]*template.Template {
	pages := make(map[string]*template.Template, len(accountPageWords))
	for page, words := range accountPageWords {
		clone := template.Must(accountPageTemplate.Clone())
		pages[page] = template.Must(clone.Parse(`{{define "heading"}}` + words[0] + `{{end}}{{define "body"}}` + words[1] + `{{end}}`))
	}

	return pages
}()

// accountScopeProblem is what an admin is told on the scope page.
const accountScopeProblem = "Ask Hytale to grant the scope hytale:profile to the application whose Client ID is in the Hytale section."

// writeAccountPage answers with a refusal's page. A system admin is told the
// problem behind it, where there is one; userID is "" before the caller is
// known.
func (p *Plugin) writeAccountPage(w http.ResponseWriter, prov *accountProvider, refusal *accountRefusal, userID string) {
	data := accountPageData{}
	if refusal.problem != "" && userID != "" && p.isSystemAdmin(userID) {
		data.Problem = refusal.problem
	}

	p.writeAccountPageData(w, prov, refusal.status, refusal.page, data)
}

func (p *Plugin) writeAccountPageData(w http.ResponseWriter, prov *accountProvider, status int, page string, data accountPageData) {
	tmpl, known := accountPages[page]
	if !known {
		tmpl = accountPages[accountPageStore]
	}

	data.Brand, data.Service, data.NameKind = p.brandName(), prov.name, prov.nameKind

	// The close script only where the person is done: connected, or
	// cancelled on the service's own page.
	data.Close = page == accountPageConnected || page == accountPageCancelled

	var body bytes.Buffer
	if err := tmpl.Execute(&body, data); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(status)
	_, _ = w.Write(body.Bytes())
}
