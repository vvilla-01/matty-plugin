package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Connect Steam (design/3.4.0.md §3): Steam's sign-in is OpenID 2.0, which has
// no token, no secret and no PKCE. The proof is Steam's own signature over
// what it sent back, which only Steam can check, so the server asks it to
// (check_authentication), after every check it can make itself.
//
// The endpoint is hard-coded and discovery is never run, as the one strict
// validator in real use advises: a generic OpenID library leaks the server's
// address through discovery and has been spoofed through it, and a callback
// built from the Host header can be pointed anywhere. Every check below runs
// before any outside request, so nobody can make this server call Steam
// without a live state of their own (accounts.go spends it first):
//
//   - S1 the mode: a cancel is a cancel, an error is Steam saying no, and
//     anything but a positive assertion is nothing at all.
//   - S2 exactly the ten fields, each once. A repeated key would let what
//     VillaNET checks and what Steam checks be two different values.
//   - S3-S4 the namespace and the endpoint, so the assertion is OpenID 2.0
//     from Steam's own server.
//   - S5 claimed_id equal to identity, and both Steam's, by a pattern whose
//     host is literal and whose dots are escaped: one library in use checks
//     an unescaped dot, which "steamcommunityXcom" passes.
//   - S6 the exact signed list, so Steam's signature covers return_to, and
//     with it the state: an assertion cannot be moved to anybody else's
//     state without check_authentication failing.
//   - S7 return_to byte for byte the address this state was started with.
//   - S8-S9 the response nonce's time, and the nonce itself recorded before
//     Steam is asked, with an insert that fails if it is already there, so
//     two callbacks carrying one assertion cannot both pass.
//   - S10 check_authentication: exactly those ten fields back to the same
//     hard-coded endpoint, which must answer is_valid:true.
//
// Statuses come from Steam's Web API (GetPlayerSummaries), once per
// connection for the name and once a minute in the accounts loop
// (accounts_live.go). The key goes in the x-webapi-key header, never in an
// address: Go's *url.Error spells out the whole address it was given, and a
// reverse proxy logs it, so a key in a query string would end up in both.

var (
	// Where Steam's OpenID sign-in lives, and Steam's Web API. Variables only
	// so tests can point them at a fake: nothing a test runs ever leaves the
	// machine.
	steamOpenIDBase = "https://steamcommunity.com"
	steamAPIBase    = "https://api.steampowered.com"
)

const (
	steamOpenIDNamespace  = "http://specs.openid.net/auth/2.0"
	steamIdentifierSelect = "http://specs.openid.net/auth/2.0/identifier_select"
	steamSignedFields     = "signed,op_endpoint,claimed_id,identity,return_to,response_nonce,assoc_handle"

	// A profile link is built here from the SteamID64, never taken from
	// Steam's profileurl.
	steamProfilePrefix = "https://steamcommunity.com/profiles/"

	// A response nonce's time may be this far from now either way.
	steamNonceSkew = 300 * time.Second

	// How long a response nonce is remembered: longer than the skew either
	// way, so a replay is refused for as long as its time would pass.
	steamNonceSeconds = 600

	// check_authentication's share of the callback's 15 seconds, and the
	// name's after it.
	steamCheckBudget = 10 * time.Second
	steamNameBudget  = 5 * time.Second

	// The accounts loop's one GetPlayerSummaries call a pass.
	steamRequestBudget = 10 * time.Second

	maxSteamValueBytes   = 512
	maxSteamReturnBytes  = 1024
	maxSteamQueryBytes   = 8 << 10
	maxSteamNonceLength  = 255
	maxSteamCheckBytes   = 4 << 10
	maxSteamSummaryBytes = 1 << 20
	maxSteamNameRunes    = 64
	maxSteamGameRunes    = 80

	// One GetPlayerSummaries call takes at most this many SteamIDs, which is
	// why at most this many people can connect Steam.
	maxSteamIDsPerCall = 100
)

var (
	// xPaw's pattern: https only, the host literal with its dots escaped, and
	// 17 digits kept as a string. They fit a uint64, never a float or a
	// JavaScript number.
	steamIdentityPattern = regexp.MustCompile(`^https://steamcommunity\.com/openid/id/(76561[0-9]{12})/?$`)

	// A SteamID64 as kept: the same 17 digits.
	steamIDPattern = regexp.MustCompile(`^76561[0-9]{12}$`)

	// A response nonce starts with an RFC 3339 UTC time.
	steamNonceTime = regexp.MustCompile(`^([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z)`)

	// The ten fields of a positive assertion, in the order check_authentication
	// sends them back.
	steamOpenIDFields = []string{"ns", "mode", "op_endpoint", "claimed_id", "identity", "return_to", "response_nonce", "assoc_handle", "signed", "sig"}
)

// steamOn: the switch is on and a key is set. Everything that contacts Steam,
// shows Steam or offers Connect Steam asks this first; taking things away
// does not.
func (c *configuration) steamOn() bool {
	return c.EnableSteam && c.SteamWebAPIKey != ""
}

// steamKey is the Web API key, when it can travel in a header.
func (c *configuration) steamKey() (string, bool) {
	if !accountCredentialShaped(c.SteamWebAPIKey) {
		return "", false
	}

	return c.SteamWebAPIKey, true
}

// steamCallbackURL is where Steam sends people back to, without the state. It
// is not registered anywhere: OpenID 2.0 sends it with each sign-in.
func steamCallbackURL(siteURL string) (address, problem string) {
	return accountCallbackURL(siteURL, steamCallbackPath, "Steam",
		"Steam's sign-in needs an https:// address to send people back to, and the Site URL is ")
}

// steamRealm is what Steam's page names this server as: the Site URL's scheme
// and host, with any port that is not the scheme's own, no path and no
// trailing slash, which is what Steam fills in itself when given none. A Site
// URL with a path still works: the origin covers every path under it.
func steamRealm(siteURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(siteURL))
	if err != nil || parsed.Host == "" {
		return ""
	}

	host := parsed.Host
	if port := parsed.Port(); (parsed.Scheme == "https" && port == "443") || (parsed.Scheme == "http" && port == "80") {
		host = strings.TrimSuffix(host, ":"+port)
	}

	return parsed.Scheme + "://" + host
}

// steamSetup is "" when people can connect Steam, otherwise the sentence an
// admin is shown.
func steamSetup(p *Plugin, cfg *configuration) string {
	if _, problem := steamCallbackURL(p.siteURL()); problem != "" {
		return problem
	}
	if cfg.SteamWebAPIKey == "" {
		return "Fill in the Steam Web API key first."
	}
	if _, ok := cfg.steamKey(); !ok {
		return "The Steam Web API key has characters a key cannot have."
	}

	return ""
}

// steamProfileURL is a person's Steam profile, from their SteamID64.
func steamProfileURL(steamID string) string {
	return steamProfilePrefix + steamID
}

// steamStart is where Continue on Steam goes (§3.3): Steam's sign-in, asked to
// let the person pick their own account and send them back to this state.
// Its parameters are in a fixed order, so the address is always the same
// string for the same values. The server asks Steam nothing here.
func steamStart(p *Plugin, _ *configuration, state string, st *accountState) (string, error) {
	// A build that asks a stand-in for Steam (standin.go) checked the Site
	// URL as it started; it may have changed since. Never in a release.
	if problem := p.standInRefusal(); problem != "" {
		return "", errors.New(problem)
	}

	site := p.siteURL()
	address, problem := steamCallbackURL(site)
	if problem != "" {
		return "", errors.New("no address for Steam to send people back to")
	}

	st.Return = address + "?state=" + state

	var query strings.Builder
	for i, pair := range [][2]string{
		{"openid.ns", steamOpenIDNamespace},
		{"openid.mode", "checkid_setup"},
		{"openid.return_to", st.Return},
		{"openid.realm", steamRealm(site)},
		{"openid.identity", steamIdentifierSelect},
		{"openid.claimed_id", steamIdentifierSelect},
	} {
		if i > 0 {
			query.WriteByte('&')
		}
		query.WriteString(url.QueryEscape(pair[0]))
		query.WriteByte('=')
		query.WriteString(url.QueryEscape(pair[1]))
	}

	return steamOpenIDBase + "/openid/login?" + query.String(), nil
}

// steamDeclined is S1, the "came back without an account" check: the person
// cancelled on Steam, or Steam said no. Steam's openid.error is never read.
func steamDeclined(query url.Values) *accountRefusal {
	switch query.Get("openid.mode") {
	case "id_res":
		return nil
	case "cancel":
		return accountRefused(accountPageCancelled, "")
	case "error":
		return accountRefused(accountPageProviderError, "steam_error")
	default:
		return accountRefused(accountPageExpired, "mode")
	}
}

// steamFinish proves the account Steam sent back (§3.4, S1-S12).
func steamFinish(ctx context.Context, p *Plugin, cfg *configuration, _ string, st accountState, query url.Values, now time.Time) (accountProof, *accountRefusal) {
	// Where a stand-in's word would be taken for whose account this is: only
	// while the Site URL is still this machine (standin.go). Never refused
	// in a release.
	if p.standInRefusal() != "" {
		return accountProof{}, accountRefused(accountPageProviderError, "standin")
	}

	if refusal := steamDeclined(query); refusal != nil {
		return accountProof{}, refusal
	}

	if refusal := steamFieldsRefusal(query); refusal != nil {
		return accountProof{}, refusal
	}

	field := func(name string) string { return query.Get("openid." + name) }

	if field("ns") != steamOpenIDNamespace {
		return accountProof{}, accountRefused(accountPageProviderError, "ns")
	}
	if field("op_endpoint") != steamOpenIDBase+"/openid/login" {
		return accountProof{}, accountRefused(accountPageProviderError, "op_endpoint")
	}

	identity := field("identity")
	matched := steamIdentityPattern.FindStringSubmatch(identity)
	if field("claimed_id") != identity || matched == nil {
		return accountProof{}, accountRefused(accountPageProviderError, "identity")
	}
	steamID := matched[1]

	if field("signed") != steamSignedFields {
		return accountProof{}, accountRefused(accountPageProviderError, "signed")
	}
	if field("return_to") != st.Return || st.Return == "" {
		return accountProof{}, accountRefused(accountPageProviderError, "return_to")
	}

	nonce := field("response_nonce")
	if !steamNonceInTime(nonce, now) {
		return accountProof{}, accountRefused(accountPageProviderError, "nonce_time")
	}

	// Never twice. Written before Steam is asked, so two callbacks carrying
	// one assertion cannot both get as far as asking.
	record, _ := json.Marshal(map[string]int64{"at": now.UnixMilli()})
	fresh, appErr := p.API.KVSetWithOptions(kvSteamNoncePrefix+hashHex(nonce), record, model.PluginKVSetOptions{
		Atomic:          true,
		OldValue:        nil,
		ExpireInSeconds: steamNonceSeconds,
	})
	if appErr != nil {
		return accountProof{}, accountRefused(accountPageStore, "")
	}
	if !fresh {
		return accountProof{}, accountRefused(accountPageProviderError, "nonce_reuse")
	}

	if refusal := p.steamCheckAuthentication(ctx, query); refusal != nil {
		return accountProof{}, refusal
	}

	// The name, which Steam's sign-in does not say. A failure here is not a
	// refusal: the link is written with no name, and the accounts loop's
	// next read fills it in.
	proof := accountProof{ID: steamID}
	if key, ok := cfg.steamKey(); ok {
		nameCtx, cancel := context.WithTimeout(ctx, steamNameBudget)
		players, err := p.askSteamSummaries(nameCtx, key, []string{steamID})
		cancel()

		if player, found := players[steamID]; err == nil && found {
			status := steamStatusOf(player)
			proof.Name, proof.Hidden = status.Name, status.Hidden
		} else {
			p.accounts().warnOnce("steam-name", "Steam did not say a new connection's Steam name just now; Matty fills it in at its next check.",
				"status", steamReadStatus(err))
		}
	}

	return proof, nil
}

// steamFieldsRefusal is S2: the size of what came back, then exactly the ten
// openid fields, each once. Keys that do not start with openid. are not
// Steam's and are left alone; state is checked before this.
func steamFieldsRefusal(query url.Values) *accountRefusal {
	if len(query.Encode()) > maxSteamQueryBytes {
		return accountRefused(accountPageExpired, "size")
	}

	known := make(map[string]bool, len(steamOpenIDFields))
	for _, name := range steamOpenIDFields {
		known[name] = true
	}

	seen := 0
	for key, values := range query {
		name, isOpenID := strings.CutPrefix(key, "openid.")
		if !isOpenID {
			continue
		}
		if !known[name] || len(values) != 1 {
			return accountRefused(accountPageProviderError, "fields")
		}

		limit := maxSteamValueBytes
		if name == "return_to" {
			limit = maxSteamReturnBytes
		}
		if len(values[0]) > limit {
			return accountRefused(accountPageExpired, "size")
		}
		seen++
	}

	if seen != len(steamOpenIDFields) {
		return accountRefused(accountPageProviderError, "fields")
	}

	return nil
}

// steamNonceInTime is S8: at most 255 characters, starting with a UTC time
// within five minutes of now either way.
func steamNonceInTime(nonce string, now time.Time) bool {
	if nonce == "" || len(nonce) > maxSteamNonceLength {
		return false
	}

	matched := steamNonceTime.FindStringSubmatch(nonce)
	if matched == nil {
		return false
	}

	at, err := time.Parse(time.RFC3339, matched[1])
	if err != nil {
		return false
	}

	gap := now.Sub(at)

	return gap <= steamNonceSkew && gap >= -steamNonceSkew
}

// steamCheckAuthentication is S10: Steam is asked whether it really signed
// what came back. The body is exactly the ten fields, with the mode changed
// and nothing added (not the state), sent to the same hard-coded endpoint.
func (p *Plugin) steamCheckAuthentication(ctx context.Context, query url.Values) *accountRefusal {
	var body strings.Builder
	for i, name := range steamOpenIDFields {
		value := query.Get("openid." + name)
		if name == "mode" {
			value = "check_authentication"
		}
		if i > 0 {
			body.WriteByte('&')
		}
		body.WriteString(url.QueryEscape("openid." + name))
		body.WriteByte('=')
		body.WriteString(url.QueryEscape(value))
	}

	ctx, cancel := context.WithTimeout(ctx, steamCheckBudget)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, steamOpenIDBase+"/openid/login", strings.NewReader(body.String()))
	if err != nil {
		return accountRefused(accountPageProviderError, "unreachable")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := sendMedia(request)
	if err != nil {
		return accountRefused(accountPageProviderError, "unreachable")
	}
	defer response.Body.Close()

	switch {
	case response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusTooManyRequests:
		// Valve rate-limits this endpoint.
		return accountRefused(accountPageBusy, "slow", "status", response.StatusCode)
	case response.StatusCode != http.StatusOK:
		return accountRefused(accountPageProviderError, "status", "status", response.StatusCode)
	}

	answer, err := io.ReadAll(io.LimitReader(response.Body, maxSteamCheckBytes+1))
	if err != nil || len(answer) > maxSteamCheckBytes {
		return accountRefused(accountPageProviderError, "is_valid")
	}

	values, ok := steamKeyValues(answer)
	if !ok || values["ns"] != steamOpenIDNamespace || values["is_valid"] != "true" {
		return accountRefused(accountPageProviderError, "is_valid")
	}

	return nil
}

// steamKeyValues reads OpenID's Key-Value form: one key:value a line. A line
// without a colon, or a key said twice, is not an answer.
func steamKeyValues(body []byte) (map[string]string, bool) {
	values := map[string]string{}

	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}

		key, value, found := strings.Cut(line, ":")
		if !found {
			return nil, false
		}
		if _, twice := values[key]; twice {
			return nil, false
		}
		values[key] = value
	}

	return values, true
}

// steamPlayer is one player of GetPlayerSummaries' answer: only the fields
// used. Nothing else Steam returns (avatars, real name, country, server
// address, profileurl) is decoded at all.
type steamPlayer struct {
	SteamID    string          `json:"steamid"`
	Name       string          `json:"personaname"`
	State      int             `json:"personastate"`
	Visibility int             `json:"communityvisibilitystate"`
	GameName   string          `json:"gameextrainfo"`
	GameID     json.RawMessage `json:"gameid"` // only whether it is there; string or number, never parsed as a number
}

type steamSummaries struct {
	Response struct {
		Players []steamPlayer `json:"players"`
	} `json:"response"`
}

// steamReadError is why a GetPlayerSummaries call did not answer: Steam's
// status, or 0 when Steam could not be reached or its answer would not
// decode. Fixed words only, so it can go into a log line.
type steamReadError struct {
	status int
	reason string // "key" | "slow" | "status" | "unreachable" | "answer"
}

func (e *steamReadError) Error() string { return "Steam's Web API did not answer: " + e.reason }

// steamReadStatus is the status behind a failed read, for a log line.
func steamReadStatus(err error) int {
	var failed *steamReadError
	if errors.As(err, &failed) {
		return failed.status
	}

	return 0
}

// askSteamSummaries is one GetPlayerSummaries call for up to a hundred
// SteamIDs, with the key in the x-webapi-key header. It answers only for the
// ids it was asked about. Every call it makes is counted for the admin card.
// A 403 is Steam refusing the key, a 429 Steam asking VillaNET to slow down.
func (p *Plugin) askSteamSummaries(ctx context.Context, key string, ids []string) (map[string]steamPlayer, error) {
	// Only SteamIDs as kept go into the address, so nothing else can ride
	// along in it.
	asked := make(map[string]bool, len(ids))
	var sent []string
	for _, id := range ids {
		if steamIDPattern.MatchString(id) && !asked[id] {
			asked[id] = true
			sent = append(sent, id)
		}
	}
	if len(sent) == 0 || len(sent) > maxSteamIDsPerCall {
		return nil, &steamReadError{reason: "answer"}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		steamAPIBase+"/ISteamUser/GetPlayerSummaries/v2/?steamids="+strings.Join(sent, ","), nil)
	if err != nil {
		return nil, &steamReadError{reason: "unreachable"}
	}
	request.Header.Set("x-webapi-key", key)
	request.Header.Set("Accept", "application/json")

	s := p.accounts()
	s.countSteamCall(s.now())

	response, err := sendMedia(request)
	if err != nil {
		return nil, &steamReadError{reason: "unreachable"}
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden:
		return nil, &steamReadError{status: response.StatusCode, reason: "key"}
	case http.StatusTooManyRequests:
		return nil, &steamReadError{status: response.StatusCode, reason: "slow"}
	default:
		return nil, &steamReadError{status: response.StatusCode, reason: "status"}
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, maxSteamSummaryBytes+1))
	var answer steamSummaries
	if err != nil || len(body) > maxSteamSummaryBytes || json.Unmarshal(body, &answer) != nil {
		return nil, &steamReadError{status: response.StatusCode, reason: "answer"}
	}

	players := make(map[string]steamPlayer, len(answer.Response.Players))
	for _, player := range answer.Response.Players {
		if asked[player.SteamID] {
			players[player.SteamID] = player
		}
	}

	return players, nil
}

// steamStatus is what VillaNET makes of one player, in memory only.
type steamStatus struct {
	State  string // "online" | "busy" | "away" | "offline"
	InGame bool
	Game   string // cleaned; "" with InGame for a game Steam did not name
	GameID string // Steam's app id for it, digits; "" when Steam gave none that reads as one
	Hidden bool   // the profile is not public
	Name   string // the Steam name, cleaned

	// at is when Steam said it, so the accounts loop forgets it
	// steamKeepGood later; zero until the loop or a connect stamps it.
	at time.Time
}

// steamStatusOf is §3.7's table. Only a profile Steam reports as public shows
// anything but offline, so a private profile never shows a game, whatever
// personastate says (Steam's own bug reports 5 and 6 for some private ones).
// Invisible (7) is never published by Steam, and any value Steam does not
// document reads offline.
func steamStatusOf(player steamPlayer) steamStatus {
	status := steamStatus{State: "offline", Name: cleanText(player.Name, maxSteamNameRunes)}
	if player.Visibility != 3 {
		status.Hidden = true
		return status
	}

	switch player.State {
	case 1, 5, 6:
		status.State = "online"
	case 2:
		status.State = "busy"
	case 3, 4:
		status.State = "away"
	}

	game := cleanText(player.GameName, maxSteamGameRunes)
	if game != "" || steamGameIDPresent(player.GameID) {
		status.InGame, status.Game, status.GameID = true, game, steamAppIDOf(player.GameID)
	}

	return status
}

// steamGameIDPresent is whether a gameid came at all. It is never parsed as a
// number: a non-Steam shortcut's id passes 2^63. Such a shortcut is still a
// game somebody is in. (steamAppIDOf, in accounts_activity.go, keeps only an
// app id's digits, for telling whether a stream is of the same game; a
// shortcut's id is none.)
func steamGameIDPresent(raw json.RawMessage) bool {
	switch strings.TrimSpace(string(raw)) {
	case "", "null", `""`, "0", `"0"`:
		return false
	default:
		return true
	}
}
