// Package standin holds what fakebb runs beside its Broadcast Box so that the
// whole of VillaNET can be developed on one machine: a stand-in for Steam (the
// sign-in and the Web API), a stand-in Mumble server, a stand-in UnifiedPush
// push server, and stand-in GitHub releases of the plugin itself.
//
// Each speaks what the real one speaks, so the plugin under test runs the code
// it runs for real. The Mumble server needs nothing from the plugin: where
// Mumble lives is a setting. Nor does the push server: a phone names it, here
// through the stand-in distributor, and the plugin calls it only once
// localhost:7788 is one of its listed push servers. Steam's two hosts are fixed in the plugin, and
// only a build made with the villanet_standin tag (server/standin.go) asks
// this machine instead; a release is never built with it.
//
// Never part of a release: nothing under build/ is in the plugin bundle.
package standin

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Steam stands in for the two things VillaNET asks of Steam:
//
//	GET  /openid/login                        Steam's sign-in page (steamcommunity.com)
//	POST /openid/login                        check_authentication: did Steam really sign this?
//	GET  /ISteamUser/GetPlayerSummaries/v2/   who is online and what they are playing (api.steampowered.com)
//
// The sign-in vouches for whichever account is picked: on its page, or with
// as=<name> added to the address, which is how a script signs somebody in.
// cancel=1 is somebody pressing Cancel. What it signs is checked for real
// afterwards: a signature it did not make, or one already asked about, is not
// valid, as with Steam.
//
// Accounts are steered while it runs:
//
//	curl -X POST 'localhost:7788/fake/steam?who=alice&game=Hades'           in a game
//	curl -X POST 'localhost:7788/fake/steam?who=alice&game='                not any more
//	curl -X POST 'localhost:7788/fake/steam?who=alice&state=away'           online | busy | away | snooze | offline
//	curl -X POST 'localhost:7788/fake/steam?who=alice&private=true'         a profile that is not public
//	curl -X POST 'localhost:7788/fake/steam?who=alice&name=Alice%20on%20Steam'
//	curl -X POST 'localhost:7788/fake/steam?api=429'                        the Web API answers 429 (or 403, 500) until api=200
//	curl 'localhost:7788/fake/steam'                                        everything it holds
//
// An account's SteamID is worked out from its name, so the same name is the
// same account after a restart, and a connection the plugin kept still finds
// it.
type Steam struct {
	// Now is the clock a response nonce is stamped with. time.Now unless a
	// test moves time by hand.
	Now func() time.Time

	mu       sync.Mutex
	secret   []byte
	accounts map[string]*SteamAccount // by SteamID
	used     map[string]time.Time     // response nonces already asked about
	api      int                      // the status the Web API answers with; 200 is working
	calls    int                      // GetPlayerSummaries calls so far
	mux      *http.ServeMux
}

// SteamAccount is one account as the stand-in holds it.
type SteamAccount struct {
	Who     string `json:"who"`     // the name it is steered by
	ID      string `json:"steamid"` // 17 digits, from Who
	Name    string `json:"name"`    // the Steam name
	State   string `json:"state"`   // online | busy | away | snooze | offline
	Private bool   `json:"private"` // the profile is not public
	Game    string `json:"game"`    // what Steam names the game; "" for none, or for one it does not name
	GameID  string `json:"gameid"`  // Steam's app id; in a game whenever this is set
}

const (
	openIDNamespace = "http://specs.openid.net/auth/2.0"
	openIDSelect    = "http://specs.openid.net/auth/2.0/identifier_select"
	openIDSigned    = "signed,op_endpoint,claimed_id,identity,return_to,response_nonce,assoc_handle"

	// How Steam names an account, whoever asks and wherever from. The plugin
	// takes nothing else for a Steam account, so the stand-in says exactly
	// this.
	steamIdentityPrefix = "https://steamcommunity.com/openid/id/"

	// A nonce is remembered this long. Steam's are good for a few minutes.
	nonceKept = time.Hour
)

var (
	steamIDShape = regexp.MustCompile(`^76561[0-9]{12}$`)
	whoShape     = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

	// Steam's own app ids for a few games, so that a game named here has the
	// id a stream tagged with the same game has.
	knownApps = map[string]string{
		"counter-strike 2": "730",
		"dota 2":           "570",
		"portal 2":         "620",
		"terraria":         "105600",
		"stardew valley":   "413150",
		"factorio":         "427520",
		"hades":            "1145360",
		"baldur's gate 3":  "1086940",
		"elden ring":       "1245620",
		"satisfactory":     "526870",
	}

	personaStates = map[string]int{"offline": 0, "online": 1, "busy": 2, "away": 3, "snooze": 4}
)

// NewSteam makes a Steam with these accounts: each entry is who, or who=game
// for somebody in a game. Everybody starts online and public.
func NewSteam(accounts ...string) *Steam {
	s := &Steam{
		Now:      time.Now,
		secret:   make([]byte, 32),
		accounts: map[string]*SteamAccount{},
		used:     map[string]time.Time{},
		api:      http.StatusOK,
		mux:      http.NewServeMux(),
	}
	_, _ = rand.Read(s.secret)

	for _, entry := range accounts {
		who, game, _ := strings.Cut(strings.TrimSpace(entry), "=")
		if account := s.accountFor(who); account != nil && game != "" {
			account.Game, account.GameID = game, AppID(game)
		}
	}

	s.Routes(s.mux)

	return s
}

// Routes adds Steam's addresses, and the stand-in's own, to mux.
func (s *Steam) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/openid/login", s.login)
	mux.HandleFunc("/ISteamUser/GetPlayerSummaries/v2/", s.summaries)
	mux.HandleFunc("/fake/steam", s.steer)
}

func (s *Steam) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// SteamID is the SteamID of the account steered by who: the same digits for
// the same name, every time. A SteamID is its own.
func SteamID(who string) string {
	who = strings.ToLower(strings.TrimSpace(who))
	if steamIDShape.MatchString(who) {
		return who
	}

	sum := sha256.Sum256([]byte("villanet stand-in steam account " + who))

	return fmt.Sprintf("76561198%09d", binary.BigEndian.Uint64(sum[:8])%1_000_000_000)
}

// AppID is Steam's id for a game: the real one for the few games known here,
// otherwise digits worked out from the name, past any id Steam has given out.
func AppID(game string) string {
	name := strings.ToLower(strings.TrimSpace(game))
	if name == "" {
		return ""
	}
	if id, known := knownApps[name]; known {
		return id
	}

	sum := sha256.Sum256([]byte("villanet stand-in steam app " + name))

	return fmt.Sprintf("9%09d", binary.BigEndian.Uint64(sum[:8])%1_000_000_000)
}

// accountFor finds the account steered by who, a name or a SteamID, and makes
// it when it is new: any name signs in, as any token unlocks the stand-in
// Broadcast Box. nil for a name that cannot be one. With mu held, or before
// anything is served.
func (s *Steam) accountFor(who string) *SteamAccount {
	who = strings.ToLower(strings.TrimSpace(who))
	if !whoShape.MatchString(who) {
		return nil
	}

	id := SteamID(who)
	if account, known := s.accounts[id]; known {
		return account
	}

	account := &SteamAccount{Who: who, ID: id, Name: who, State: "online"}
	s.accounts[id] = account

	return account
}

// Account is a copy of the account steered by who, for a test to read.
func (s *Steam) Account(who string) (SteamAccount, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	account, known := s.accounts[SteamID(who)]
	if !known {
		return SteamAccount{}, false
	}

	return *account, true
}

// Calls is how many GetPlayerSummaries calls there have been.
func (s *Steam) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.calls
}

// ---------------------------------------------------------------- the sign-in

func (s *Steam) login(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.signIn(w, r)
	case http.MethodPost:
		s.check(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// endpoint is this sign-in's own address, as whoever is asking reached it:
// what an assertion names as op_endpoint.
func endpoint(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host + "/openid/login"
}

func (s *Steam) signIn(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	if query.Get("openid.ns") != openIDNamespace || query.Get("openid.mode") != "checkid_setup" {
		http.Error(w, "The stand-in Steam only signs people in: openid.ns and openid.mode=checkid_setup are needed.", http.StatusBadRequest)
		return
	}
	if query.Get("openid.identity") != openIDSelect || query.Get("openid.claimed_id") != openIDSelect {
		http.Error(w, "The stand-in Steam lets the person pick their account: openid.identity and openid.claimed_id must be identifier_select.", http.StatusBadRequest)
		return
	}

	// Steam shows the realm and refuses a return address outside it. A plugin
	// that built either wrongly finds out here, not on the real thing.
	returnTo, realm := query.Get("openid.return_to"), query.Get("openid.realm")
	if !returnsWithin(returnTo, realm) {
		http.Error(w, "The stand-in Steam refuses this, as Steam would: openid.return_to is not inside openid.realm.", http.StatusBadRequest)
		return
	}

	if query.Get("cancel") != "" {
		http.Redirect(w, r, withQuery(returnTo, url.Values{"openid.mode": {"cancel"}, "openid.ns": {openIDNamespace}}), http.StatusFound)
		return
	}

	who := query.Get("as")
	if who == "" {
		s.page(w, r, realm)
		return
	}

	s.mu.Lock()
	account := s.accountFor(who)
	var assertion url.Values
	if account != nil {
		assertion = s.assert(endpoint(r), returnTo, account.ID)
	}
	s.mu.Unlock()

	if account == nil {
		http.Error(w, "Not a name the stand-in Steam can sign in: lower-case letters, digits, dots, dashes and underscores, or a SteamID.", http.StatusBadRequest)
		return
	}

	http.Redirect(w, r, withQuery(returnTo, assertion), http.StatusFound)
}

// returnsWithin is Steam's rule: the return address is the realm, or is under
// it.
func returnsWithin(returnTo, realm string) bool {
	back, err := url.Parse(returnTo)
	if err != nil || back.Host == "" || (back.Scheme != "https" && back.Scheme != "http") {
		return false
	}
	shown, err := url.Parse(realm)
	if err != nil || shown.Host == "" {
		return false
	}

	return back.Scheme == shown.Scheme && back.Host == shown.Host &&
		strings.HasPrefix(back.Path+"/", strings.TrimRight(shown.Path, "/")+"/")
}

// withQuery adds values to an address that may already have a query.
func withQuery(address string, values url.Values) string {
	separator := "?"
	if strings.Contains(address, "?") {
		separator = "&"
	}

	return address + separator + values.Encode()
}

// assert is a positive assertion for steamID, signed. With mu held.
func (s *Steam) assert(endpoint, returnTo, steamID string) url.Values {
	random := make([]byte, 9)
	_, _ = rand.Read(random)

	identity := steamIdentityPrefix + steamID
	values := url.Values{}
	values.Set("openid.ns", openIDNamespace)
	values.Set("openid.mode", "id_res")
	values.Set("openid.op_endpoint", endpoint)
	values.Set("openid.claimed_id", identity)
	values.Set("openid.identity", identity)
	values.Set("openid.return_to", returnTo)
	values.Set("openid.response_nonce", s.Now().UTC().Format("2006-01-02T15:04:05Z")+base64.RawURLEncoding.EncodeToString(random))
	values.Set("openid.assoc_handle", "1234567890")
	values.Set("openid.signed", openIDSigned)
	values.Set("openid.sig", s.sign(values))

	return values
}

// sign is the signature over the signed fields, in OpenID's own form: one
// name:value a line, in the order the signed list gives.
func (s *Steam) sign(values url.Values) string {
	mac := hmac.New(sha256.New, s.secret)
	for _, name := range strings.Split(values.Get("openid.signed"), ",") {
		_, _ = fmt.Fprintf(mac, "%s:%s\n", name, values.Get("openid."+name))
	}

	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// check answers check_authentication, in OpenID's key:value form. Valid once:
// the signature is this stand-in's own, over exactly what it signed, and
// nobody has asked about that nonce before.
func (s *Steam) check(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "unreadable", http.StatusBadRequest)
		return
	}
	form := r.PostForm

	s.mu.Lock()
	valid := form.Get("openid.mode") == "check_authentication" &&
		form.Get("openid.signed") == openIDSigned &&
		hmac.Equal([]byte(form.Get("openid.sig")), []byte(s.sign(form)))
	if valid {
		now := s.Now()
		for nonce, at := range s.used {
			if now.Sub(at) > nonceKept {
				delete(s.used, nonce)
			}
		}

		nonce := form.Get("openid.response_nonce")
		if _, asked := s.used[nonce]; asked {
			valid = false
		}
		s.used[nonce] = now
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprintf(w, "ns:%s\nis_valid:%t\n", openIDNamespace, valid)
}

var signInPage = template.Must(template.New("signin").Parse(`<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Stand-in Steam</title>
<style>
  body { font: 16px/1.5 system-ui, sans-serif; background: #171a21; color: #c7d5e0; max-width: 30rem; margin: 3rem auto; padding: 0 1rem; }
  h1 { font-size: 1.25rem; color: #fff; }
  a.account, button { display: block; width: 100%; box-sizing: border-box; margin: .5rem 0; padding: .75rem 1rem; border: 0; border-radius: .25rem; background: #2a475e; color: #fff; font: inherit; text-align: left; text-decoration: none; cursor: pointer; }
  a.account small { color: #8f98a0; }
  input[type=text] { width: 100%; box-sizing: border-box; padding: .75rem 1rem; border: 1px solid #2a475e; border-radius: .25rem; background: #0e1116; color: #fff; font: inherit; }
  .cancel { color: #8f98a0; }
  p.note { color: #8f98a0; font-size: .875rem; }
</style>
<h1>Stand-in Steam</h1>
<p>{{.Realm}} wants to know which Steam account is yours. This is not Steam: it is the stand-in on this machine, and it takes anybody's word.</p>
{{range .Accounts}}<a class="account" href="{{.Link}}">{{.Name}} <small>{{.Doing}}</small></a>
{{end}}
<form method="get" action="/openid/login">
  {{range .Fields}}<input type="hidden" name="{{.Name}}" value="{{.Value}}">
  {{end}}<input type="text" name="as" placeholder="Somebody new: a name" autocapitalize="none" autocomplete="off">
  <button type="submit">Sign in</button>
</form>
<p><a class="cancel" href="{{.Cancel}}">Cancel</a></p>
<p class="note">A script signs somebody in by adding <code>&amp;as=name</code> to this page's address.</p>
`))

// page is the sign-in page: the accounts there are, somebody new, or Cancel.
func (s *Steam) page(w http.ResponseWriter, r *http.Request, realm string) {
	type field struct{ Name, Value string }
	type row struct{ Name, Doing, Link string }

	// Everything the plugin asked with, and neither of the two answers.
	query := r.URL.Query()
	query.Del("as")
	query.Del("cancel")
	base := "/openid/login?" + query.Encode()

	var fields []field
	for name, values := range query {
		for _, value := range values {
			fields = append(fields, field{name, value})
		}
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })

	s.mu.Lock()
	var rows []row
	for _, account := range s.sorted() {
		doing := account.State
		switch {
		case account.Private:
			doing = "private profile"
		case account.Game != "":
			doing = "in " + account.Game
		case account.GameID != "":
			doing = "in a game"
		}
		rows = append(rows, row{account.Name, doing, base + "&as=" + url.QueryEscape(account.Who)})
	}
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = signInPage.Execute(w, map[string]any{"Realm": realm, "Accounts": rows, "Fields": fields, "Cancel": base + "&cancel=1"})
}

// sorted is every account, by the name it is steered by. With mu held.
func (s *Steam) sorted() []*SteamAccount {
	accounts := make([]*SteamAccount, 0, len(s.accounts))
	for _, account := range s.accounts {
		accounts = append(accounts, account)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Who < accounts[j].Who })

	return accounts
}

// ---------------------------------------------------------------- the Web API

// summaries answers GetPlayerSummaries as Steam does: only for the ids it
// knows, a private profile with nothing of what its owner is doing, and a 403
// for a call with no key.
func (s *Steam) summaries(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	if r.Header.Get("x-webapi-key") == "" && r.URL.Query().Get("key") == "" {
		http.Error(w, "Forbidden: no key", http.StatusForbidden)
		return
	}
	if s.api != http.StatusOK {
		http.Error(w, "The stand-in Steam was told to answer this.", s.api)
		return
	}

	players := []map[string]any{}
	for _, id := range strings.Split(r.URL.Query().Get("steamids"), ",") {
		account, known := s.accounts[id]
		if !known {
			continue
		}

		player := map[string]any{
			"steamid":                  account.ID,
			"communityvisibilitystate": 3,
			"profilestate":             1,
			"personaname":              account.Name,
			"profileurl":               "https://steamcommunity.com/profiles/" + account.ID + "/",
			"personastate":             personaStates[account.State],
		}
		switch {
		case account.Private:
			player["communityvisibilitystate"] = 1
			player["personastate"] = 0
		case account.GameID != "":
			player["gameid"] = account.GameID
			if account.Game != "" {
				player["gameextrainfo"] = account.Game
			}
		}
		players = append(players, player)
	}

	writeJSON(w, http.StatusOK, map[string]any{"response": map[string]any{"players": players}})
}

// ---------------------------------------------------------------- steering

// steer answers /fake/steam: a GET says everything held, a POST changes the
// account who names (making it when new) and says the same. Only what the
// address names is changed.
func (s *Steam) steer(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := r.URL.Query()

	if r.Method == http.MethodPost {
		if query.Has("api") {
			status := 0
			_, _ = fmt.Sscanf(query.Get("api"), "%d", &status)
			if status < 200 || status > 599 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "api is the status the Web API answers with: 200 to work, or 403, 429, 500"})
				return
			}
			s.api = status
		}

		if query.Has("who") {
			account := s.accountFor(query.Get("who"))
			if account == nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "who is a name (lower-case letters, digits, dots, dashes, underscores) or a SteamID"})
				return
			}

			if query.Has("state") {
				if _, known := personaStates[query.Get("state")]; !known {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "state is online, busy, away, snooze or offline"})
					return
				}
				account.State = query.Get("state")
			}
			if query.Has("name") {
				account.Name = query.Get("name")
			}
			if query.Has("private") {
				account.Private = query.Get("private") == "true"
			}
			if query.Has("game") {
				account.Game = query.Get("game")
				account.GameID = AppID(account.Game)
			}
			// After game, so that a game Steam gives an id but no name is
			// game=&gameid=123, and a game with an id of one's choosing is
			// both.
			if query.Has("gameid") {
				account.GameID = query.Get("gameid")
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"api": s.api, "calls": s.calls, "accounts": s.sorted()})
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
