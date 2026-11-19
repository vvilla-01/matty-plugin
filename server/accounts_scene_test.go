package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Steam's and Hytale's tests (design/3.4.0.md §8.1) share this: a server with
// people on it, a real compare-and-set store that can be made to fail, the
// sessions Mattermost would hand the plugin, and Steam and Hytale themselves as
// local fakes on the media fake's server, which every one of steamOpenIDBase,
// steamAPIBase and hytaleBase is pointed at. Nothing leaves the machine and
// nothing sleeps: the clock is moved by hand.

// The people. Ids have Mattermost's shape.
const (
	acDave  = "acdave00000000000000000000" // linked
	acErin  = "acerin00000000000000000000" // linked
	acBob   = "acbobk00000000000000000000" // linked
	acBoss  = "acboss00000000000000000000" // a system admin, linked
	acDan   = "acdanm00000000000000000000" // neither linked nor let in
	acLetIn = "acletn00000000000000000000" // let in to watch
	acChief = "acchief0000000000000000000" // a system admin with no token
)

// Their Steam and Hytale accounts.
const (
	daveSteamID = "76561197960287930"
	erinSteamID = "76561197960287931"
	bobSteamID  = "76561197960287932"
	bossSteamID = "76561197960287933"

	daveHytaleUUID = "123e4567-e89b-42d3-a456-426614174000"
	erinHytaleUUID = "123e4567-e89b-42d3-a456-426614174001"
)

// The Steam Web API key and the Hytale application. Distinctive, so a test can
// look for them anywhere they must not be. The secret holds a +, a %, a : and
// a space would not survive normalize, so it holds a / too, which escaping
// must turn into %2F.
const (
	testSteamKey          = "STEAMKEY0123456789ABCDEF01234567"
	testHytaleClientID    = "villanet-hytale-client"
	testHytaleSecret      = "hy+tale%sec:ret/42"
	testHytaleSub         = "Kk9cYlqd1WOTjw4xI65u8mo5CHhcpiG3wu24dHul6kM"
	testHytaleAccessToken = "hytale-access-token-"
)

// steamFake is Steam: OpenID's check_authentication, and GetPlayerSummaries.
type steamFake struct {
	lock sync.Mutex

	// issued is every assertion "Steam" signed, by signature.
	issued map[string]url.Values

	checkStatus int    // when not zero, check_authentication answers this
	checkBody   string // when not "", check_authentication answers this
	checks      []string

	players        map[string]steamPlayer
	summaryStatus  int
	summaryBody    string
	summaryKeys    []string // the x-webapi-key header of each call
	summaryQueries []string // the raw query of each call

	// While summaryHold is set, a GetPlayerSummaries call is answered only
	// once it is closed, and says it has arrived on summaryArrived first.
	summaryHold    chan struct{}
	summaryArrived chan struct{}
}

// hytaleGrant is what the fake Hytale knows about one authorization code.
type hytaleGrant struct {
	challenge string // the PKCE challenge the authorize request carried
	redirect  string
	nonce     string
	claims    map[string]any // ID token claims to change or add; nil values delete
	idToken   string         // when not "", issued as it is
	info      map[string]any // the userinfo answer; nil is the usual one
	infoType  string         // userinfo's Content-Type, when not JSON
}

// hytaleFake is Hytale's issuer: token, userinfo and revoke.
type hytaleFake struct {
	lock sync.Mutex

	grants      map[string]hytaleGrant
	tokenStatus int
	tokenBody   string

	tokenAuth   []string // the Authorization header of each token request
	tokenBodies []string
	revokes     []string // the Authorization header of each revoke
	revokeForms []url.Values
	userinfos   int

	// While set, each /userinfo request says it came on infoArrived and
	// waits for infoHold to close (holdUserinfo).
	infoHold    chan struct{}
	infoArrived chan struct{}
}

type accountsScene struct {
	p      *Plugin
	api    *plugintest.API
	store  map[string][]byte
	faults *kvFaults
	fake   *mediaFake
	steam  *steamFake
	hytale *hytaleFake
	clock  *fakeClock

	lock     sync.Mutex
	site     string
	expiries map[string]int64
	users    map[string]*model.User

	// GetUsersByIds, as Mattermost answers it: deactivated accounts with
	// DeleteAt set, deleted ones left out, and an error while usersFail.
	usersFail  bool
	usersAsked [][]string
}

func accountsConfig() *configuration {
	return &configuration{
		BroadcastBoxURL:    "http://192.0.2.10:8080",
		EnableSteam:        true,
		SteamWebAPIKey:     testSteamKey,
		EnableHytale:       true,
		HytaleClientID:     testHytaleClientID,
		HytaleClientSecret: testHytaleSecret,
	}
}

func newAccountsScene(t *testing.T) *accountsScene {
	t.Helper()

	return newAccountsSceneWith(t, accountsConfig())
}

func newAccountsSceneWith(t *testing.T, cfg *configuration) *accountsScene {
	t.Helper()

	fake := newMediaFake(t)
	p, store, faults := kvBackedPluginWithFaults(map[string][]byte{})
	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	s := &accountsScene{
		p: p, api: api, store: store, faults: faults, fake: fake, clock: newFakeClock(),
		steam:    &steamFake{issued: map[string]url.Values{}, players: map[string]steamPlayer{}},
		hytale:   &hytaleFake{grants: map[string]hytaleGrant{}},
		site:     "https://chat.example.org",
		expiries: map[string]int64{},
		users: map[string]*model.User{
			acDave:  {Id: acDave, Username: "dave", FirstName: "Dave"},
			acErin:  {Id: acErin, Username: "erin", FirstName: "Erin"},
			acBob:   {Id: acBob, Username: "bob", FirstName: "Bob"},
			acBoss:  {Id: acBoss, Username: "boss", FirstName: "Boss"},
			acDan:   {Id: acDan, Username: "dan", FirstName: "Dan"},
			acLetIn: {Id: acLetIn, Username: "olivia", FirstName: "Olivia"},
			acChief: {Id: acChief, Username: "chief", FirstName: "Chief"},
		},
	}

	savedOpenID, savedAPI, savedHytale := steamOpenIDBase, steamAPIBase, hytaleBase
	steamOpenIDBase, steamAPIBase, hytaleBase = fake.server.URL, fake.server.URL, fake.server.URL
	t.Cleanup(func() { steamOpenIDBase, steamAPIBase, hytaleBase = savedOpenID, savedAPI, savedHytale })

	s.routeSteam()
	s.routeHytale()

	faults.lock.Lock()
	linkForTest(store, acDave, "videokey-dave")
	linkForTest(store, acErin, "videokey-erin")
	linkForTest(store, acBob, "videokey-bob")
	linkForTest(store, acBoss, "videokey-boss")
	store[kvLetIn] = []byte(`{"version":1,"people":{"` + acLetIn + `":{"at":1,"by":"` + acBoss + `"}}}`)
	faults.lock.Unlock()

	api.On("GetUser", mock.Anything).Return(func(userID string) (*model.User, *model.AppError) {
		s.lock.Lock()
		defer s.lock.Unlock()

		user, known := s.users[userID]
		if !known {
			return nil, model.NewAppError("GetUser", "app.user.missing", nil, "", http.StatusNotFound)
		}
		copied := *user

		return &copied, nil
	}).Maybe()
	api.On("GetUsersByIds", mock.Anything).Return(func(userIDs []string) ([]*model.User, *model.AppError) {
		s.lock.Lock()
		defer s.lock.Unlock()

		s.usersAsked = append(s.usersAsked, append([]string(nil), userIDs...))
		if s.usersFail {
			return nil, model.NewAppError("GetUsersByIds", "app.user.get.app_error", nil, "", http.StatusInternalServerError)
		}

		users := []*model.User{}
		for _, userID := range userIDs {
			if user, known := s.users[userID]; known {
				copied := *user
				users = append(users, &copied)
			}
		}

		return users, nil
	}).Maybe()
	api.On("GetUserByUsername", mock.Anything).Return(nil, model.NewAppError("GetUserByUsername", "app.user.missing", nil, "", http.StatusNotFound)).Maybe()
	clipsAdmins(api, acBoss, acChief)

	api.On("GetSession", mock.Anything).Return(func(sessionID string) (*model.Session, *model.AppError) {
		kind, userID, found := strings.Cut(sessionID, "-")
		if !found {
			return nil, model.NewAppError("GetSession", "app.session.missing", nil, "", http.StatusUnauthorized)
		}

		session := &model.Session{Id: sessionID, UserId: userID, Props: model.StringMap{}}
		switch kind {
		case "web", "tab", "matty": // interactive sessions: two browsers, and Matty's password sign-in
		case "pat":
			session.Props[model.SessionPropType] = model.SessionTypeUserAccessToken
		case "bot":
			session.Props[model.SessionPropIsBot] = model.SessionPropIsBotValue
		case "app":
			session.IsOAuth = true
		default:
			return nil, model.NewAppError("GetSession", "app.session.missing", nil, "", http.StatusUnauthorized)
		}

		return session, nil
	}).Maybe()

	api.On("GetConfig").Return(func() *model.Config {
		s.lock.Lock()
		defer s.lock.Unlock()

		site := s.site
		config := &model.Config{}
		config.ServiceSettings.SiteURL = &site

		return config
	}).Maybe()

	api.On("KVSetWithExpiry", mock.Anything, mock.Anything, mock.Anything).Return(func(key string, value []byte, seconds int64) *model.AppError {
		faults.lock.Lock()
		defer faults.lock.Unlock()

		faults.wrote(key)
		if faults.writeFails[key] {
			return model.NewAppError("KVSetWithExpiry", "kv.write", nil, "injected failure", http.StatusInternalServerError)
		}
		store[key] = value
		faults.writes[key]++

		s.lock.Lock()
		s.expiries[key] = seconds
		s.lock.Unlock()

		return nil
	}).Maybe()

	api.On("KVList", mock.Anything, mock.Anything).Return(func(page, perPage int) ([]string, *model.AppError) {
		faults.lock.Lock()
		defer faults.lock.Unlock()

		keys := make([]string, 0, len(store))
		for key := range store {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		return keys[min(page*perPage, len(keys)):min((page+1)*perPage, len(keys))], nil
	}).Maybe()

	api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	cfg.normalize()
	p.setConfiguration(cfg)
	p.linkLimiter = newRateLimiter(5, 15*time.Minute)

	accounts := p.accounts()
	accounts.now = s.clock.now
	accounts.after = never
	p.accessCache().now = s.clock.now

	t.Cleanup(p.stopAccountsLive)

	return s
}

// deactivate is an admin deactivating somebody's Mattermost account, and
// reactivate the reverse; forget is the account deleted for good.
func (s *accountsScene) deactivate(userID string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.users[userID].DeleteAt = 1
}

func (s *accountsScene) reactivate(userID string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.users[userID].DeleteAt = 0
}

func (s *accountsScene) forget(userID string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	delete(s.users, userID)
}

// person adds somebody to the server.
func (s *accountsScene) person(userID, name string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.users[userID] = &model.User{Id: userID, Username: strings.ToLower(name), FirstName: name}
}

func (s *accountsScene) failUsers(fail bool) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.usersFail = fail
}

func (s *accountsScene) usersLookups() int {
	s.lock.Lock()
	defer s.lock.Unlock()

	return len(s.usersAsked)
}

// step is one pass of the accounts loop, now.
func (s *accountsScene) step(t *testing.T) {
	t.Helper()

	s.p.accounts().step(t.Context(), s.p.getConfiguration(), s.now())
}

// accountsEvents is how many accounts events went out, checking each goes
// to everybody with nothing in it.
func (s *accountsScene) accountsEvents(t *testing.T) int {
	t.Helper()

	count := 0
	for _, call := range s.api.Calls {
		if call.Method != "PublishWebSocketEvent" || call.Arguments[0] != wsEventAccounts {
			continue
		}

		payload, ok := call.Arguments[1].(map[string]any)
		require.True(t, ok)
		require.Empty(t, payload, "the event carries nothing")

		broadcast, ok := call.Arguments[2].(*model.WebsocketBroadcast)
		require.True(t, ok)
		require.Equal(t, model.WebsocketBroadcast{}, *broadcast, "to every connection")
		count++
	}

	return count
}

func (s *accountsScene) now() time.Time { return s.clock.now() }

func (s *accountsScene) setSite(site string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.site = site
}

func (s *accountsScene) configure(change func(cfg *configuration)) {
	cfg := s.p.getConfiguration().Clone()
	change(cfg)
	cfg.normalize()
	s.p.setConfiguration(cfg)
}

func (s *accountsScene) expiryOf(key string) int64 {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.expiries[key]
}

func (s *accountsScene) kv(key string) []byte {
	s.faults.lock.Lock()
	defer s.faults.lock.Unlock()

	value, present := s.store[key]
	if !present {
		return nil
	}

	return append([]byte(nil), value...)
}

func (s *accountsScene) setKV(key string, value []byte) {
	s.faults.lock.Lock()
	defer s.faults.lock.Unlock()

	if value == nil {
		delete(s.store, key)
		return
	}
	s.store[key] = value
}

// keysWith is every stored key with this prefix.
func (s *accountsScene) keysWith(prefix string) []string {
	s.faults.lock.Lock()
	defer s.faults.lock.Unlock()

	var keys []string
	for key := range s.store {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	return keys
}

// links is a service's stored document.
func (s *accountsScene) links(t *testing.T, prov *accountProvider) *accountLinks {
	t.Helper()

	doc, err := decodeAccountLinks(s.kv(prov.linksKey))
	require.NoError(t, err)

	return doc
}

// putLinks stores a document as it would be after the people connected, and
// caches it as the loop's next read would.
func (s *accountsScene) putLinks(t *testing.T, prov *accountProvider, doc *accountLinks) {
	t.Helper()

	if doc.V == 0 {
		doc.V = accountLinksVersion
	}
	encoded, err := json.Marshal(doc)
	require.NoError(t, err)
	s.setKV(prov.linksKey, encoded)

	fresh, err := decodeAccountLinks(encoded)
	require.NoError(t, err)
	s.p.accounts().installLinks(prov, fresh)
}

// serve makes one request through ServeHTTP, in a session.
func (s *accountsScene) serve(method, target, userID, sessionID, body string, header map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if userID != "" {
		request.Header.Set("Mattermost-User-Id", userID)
	}
	for name, value := range header {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()

	s.p.ServeHTTP(&plugin.Context{SessionId: sessionID}, recorder, request)

	return recorder
}

func matty(userID string) string { return "matty-" + userID }

func ticketPath(prov *accountProvider) string { return "/api/v1/" + prov.id + "/connect-ticket" }
func appPath(prov *accountProvider, step string) string {
	return "/api/v1/" + prov.id + "/app-" + step
}

// ticket is the first press of Connect.
func (s *accountsScene) ticket(t *testing.T, prov *accountProvider, userID string) string {
	t.Helper()

	response := s.serve(http.MethodPost, ticketPath(prov), userID, web(userID), "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var answer struct {
		Ticket    string `json:"ticket"`
		ExpiresIn int    `json:"expiresIn"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer))
	require.Equal(t, 600, answer.ExpiresIn)

	return answer.Ticket
}

// connectWith is the second press, with a ticket in hand.
func (s *accountsScene) connectWith(prov *accountProvider, userID, sessionID, ticket string, header map[string]string) *httptest.ResponseRecorder {
	return s.serve(http.MethodGet, prov.connectPath+"?t="+url.QueryEscape(ticket), userID, sessionID, "", header)
}

// startConnect is both presses: the state, and where the browser was sent.
func (s *accountsScene) startConnect(t *testing.T, prov *accountProvider, userID string) (string, *url.URL) {
	t.Helper()

	response := s.connectWith(prov, userID, web(userID), s.ticket(t, prov, userID), nil)
	require.Equal(t, http.StatusFound, response.Code, response.Body.String())

	location, err := url.Parse(response.Header().Get("Location"))
	require.NoError(t, err)

	state := location.Query().Get("state")
	if prov.id == accountSteam {
		returnTo, err := url.Parse(location.Query().Get("openid.return_to"))
		require.NoError(t, err)
		state = returnTo.Query().Get("state")
	}
	require.Regexp(t, hex64, state)

	return state, location
}

// callback is the service sending the browser back.
func (s *accountsScene) callback(prov *accountProvider, userID, sessionID, query string) *httptest.ResponseRecorder {
	return s.serve(http.MethodGet, prov.callbackPath+"?"+query, userID, sessionID, "", nil)
}

// appConnect is Matty's first request.
func (s *accountsScene) appConnect(prov *accountProvider, userID, sessionID string) *httptest.ResponseRecorder {
	return s.serve(http.MethodPost, appPath(prov, "connect"), userID, sessionID, "{}", map[string]string{"X-Requested-With": "XMLHttpRequest"})
}

// appStart is app-connect, answered: where Matty's window goes, and the
// state in it.
func (s *accountsScene) appStart(t *testing.T, prov *accountProvider, userID string) (string, *url.URL) {
	t.Helper()

	response := s.appConnect(prov, userID, matty(userID))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var answer struct {
		URL      string `json:"url"`
		FinishAt string `json:"finishAt"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer))

	location, err := url.Parse(answer.URL)
	require.NoError(t, err)

	state := location.Query().Get("state")
	if prov.id == accountSteam {
		returnTo, err := url.Parse(location.Query().Get("openid.return_to"))
		require.NoError(t, err)
		state = returnTo.Query().Get("state")
	}

	return state, location
}

// appFinish is Matty's second request, with the raw query it caught.
func (s *accountsScene) appFinish(prov *accountProvider, userID, sessionID, query string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"query": query})

	return s.serve(http.MethodPost, appPath(prov, "finish"), userID, sessionID, string(body), map[string]string{"X-Requested-With": "XMLHttpRequest"})
}

// me is /me as somebody sees it.
func (s *accountsScene) me(t *testing.T, userID string) meResponse {
	t.Helper()

	response := s.serve(http.MethodGet, "/api/v1/me", userID, web(userID), "", nil)
	require.Equal(t, http.StatusOK, response.Code)

	var answer meResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer))

	return answer
}

// accountEvents is who has been sent the "me" event, in order, ""
// for everybody, checking each carries nothing about anybody.
func (s *accountsScene) accountEvents(t *testing.T) []string {
	t.Helper()

	var told []string
	for _, call := range s.api.Calls {
		if call.Method != "PublishWebSocketEvent" || call.Arguments[0] != wsEventMe {
			continue
		}

		payload, ok := call.Arguments[1].(map[string]any)
		require.True(t, ok)
		require.Equal(t, meEventOfAFreshServer, payload, "the event carries nothing about anybody")

		broadcast, ok := call.Arguments[2].(*model.WebsocketBroadcast)
		require.True(t, ok)
		told = append(told, broadcast.UserId)
	}

	return told
}

// logged is every argument of every log line, as one string each.
func (s *accountsScene) logged() []string {
	var lines []string
	for _, call := range s.api.Calls {
		if strings.HasPrefix(call.Method, "Log") {
			lines = append(lines, fmt.Sprint(call.Arguments...))
		}
	}

	return lines
}

// accountsAnswer is GET /api/v1/accounts as somebody sees it, raw.
func (s *accountsScene) accountsAnswer(t *testing.T, userID string) (int, []byte) {
	t.Helper()

	response := s.serve(http.MethodGet, "/api/v1/accounts", userID, web(userID), "", nil)

	return response.Code, response.Body.Bytes()
}

// Steam.

// steamNonce is a response nonce made at a time, unique per call.
var steamNonceCount struct {
	sync.Mutex
	n int
}

func steamNonceAt(at time.Time) string {
	steamNonceCount.Lock()
	defer steamNonceCount.Unlock()

	steamNonceCount.n++

	return at.UTC().Format("2006-01-02T15:04:05Z") + fmt.Sprintf("u%06dZ", steamNonceCount.n)
}

// steamAssertion is what Steam sends back for state when steamID signs in:
// the ten openid fields, signed by the fake.
func (s *accountsScene) steamAssertion(returnTo, steamID string) url.Values {
	identity := "https://steamcommunity.com/openid/id/" + steamID
	values := url.Values{}
	values.Set("openid.ns", steamOpenIDNamespace)
	values.Set("openid.mode", "id_res")
	values.Set("openid.op_endpoint", steamOpenIDBase+"/openid/login")
	values.Set("openid.claimed_id", identity)
	values.Set("openid.identity", identity)
	values.Set("openid.return_to", returnTo)
	values.Set("openid.response_nonce", steamNonceAt(s.now()))
	values.Set("openid.assoc_handle", "1234567890")
	values.Set("openid.signed", steamSignedFields)

	sum := sha256.Sum256([]byte(values.Encode()))
	values.Set("openid.sig", base64.StdEncoding.EncodeToString(sum[:]))

	s.steam.lock.Lock()
	s.steam.issued[values.Get("openid.sig")] = values
	s.steam.lock.Unlock()

	return values
}

// steamReturn is the query of the address Steam sends a browser back to: the
// return address's own state, then the assertion.
func steamReturn(state string, assertion url.Values) string {
	return "state=" + state + "&" + assertion.Encode()
}

// steamCallbackFor is the return address of a state on this scene's site.
func (s *accountsScene) steamCallbackFor(state string) string {
	s.lock.Lock()
	defer s.lock.Unlock()

	return strings.TrimRight(s.site, "/") + "/plugins/ceo.villa.matty" + steamCallbackPath + "?state=" + state
}

// connectSteam is the whole of connecting Steam in the browser, as steamID.
func (s *accountsScene) connectSteam(t *testing.T, userID, steamID string) *httptest.ResponseRecorder {
	t.Helper()

	state, _ := s.startConnect(t, steamProvider, userID)
	response := s.callback(steamProvider, userID, web(userID), steamReturn(state, s.steamAssertion(s.steamCallbackFor(state), steamID)))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	return response
}

// player is somebody as GetPlayerSummaries describes them.
func (s *accountsScene) player(player steamPlayer) {
	s.steam.lock.Lock()
	defer s.steam.lock.Unlock()

	s.steam.players[player.SteamID] = player
}

func (s *accountsScene) routeSteam() {
	f := s.steam

	s.fake.route("/openid/login", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		f.lock.Lock()
		f.checks = append(f.checks, string(body))
		status, override := f.checkStatus, f.checkBody
		f.lock.Unlock()

		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if status != 0 {
			w.WriteHeader(status)
			return
		}

		w.Header().Set("Content-Type", "text/plain;charset=utf-8")
		if override != "" {
			_, _ = io.WriteString(w, override)
			return
		}

		form, err := url.ParseQuery(string(body))
		valid := err == nil && form.Get("openid.mode") == "check_authentication" && len(form) == len(steamOpenIDFields)
		if valid {
			f.lock.Lock()
			issued, known := f.issued[form.Get("openid.sig")]
			f.lock.Unlock()

			valid = known
			for key := range issued {
				if key != "openid.mode" && issued.Get(key) != form.Get(key) {
					valid = false
				}
			}
		}

		_, _ = fmt.Fprintf(w, "ns:%s\nis_valid:%t\n", steamOpenIDNamespace, valid)
	})

	s.fake.route("/ISteamUser/GetPlayerSummaries/v2/", func(w http.ResponseWriter, r *http.Request) {
		f.lock.Lock()
		f.summaryKeys = append(f.summaryKeys, r.Header.Get("x-webapi-key"))
		f.summaryQueries = append(f.summaryQueries, r.URL.RawQuery)
		status, override, hold, arrived := f.summaryStatus, f.summaryBody, f.summaryHold, f.summaryArrived
		players := []steamPlayer{}
		for _, id := range strings.Split(r.URL.Query().Get("steamids"), ",") {
			if player, found := f.players[id]; found {
				players = append(players, player)
			}
		}
		f.lock.Unlock()

		if hold != nil {
			if arrived != nil {
				arrived <- struct{}{}
			}
			<-hold
		}
		if r.Header.Get("x-webapi-key") != testSteamKey {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, "<html>Forbidden: verify your x-webapi-key request header value.</html>")
			return
		}
		if status != 0 {
			w.WriteHeader(status)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if override != "" {
			_, _ = io.WriteString(w, override)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"players": players}})
	})
}

func (f *steamFake) checkBodies() []string {
	f.lock.Lock()
	defer f.lock.Unlock()

	return append([]string(nil), f.checks...)
}

func (f *steamFake) summaryCalls() ([]string, []string) {
	f.lock.Lock()
	defer f.lock.Unlock()

	return append([]string(nil), f.summaryKeys...), append([]string(nil), f.summaryQueries...)
}

func (f *steamFake) answerChecks(status int, body string) {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.checkStatus, f.checkBody = status, body
}

func (f *steamFake) answerSummaries(status int, body string) {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.summaryStatus, f.summaryBody = status, body
}

// holdSummaries keeps the next GetPlayerSummaries calls open until the
// returned release is called; arrived says when each one has come in.
func (f *steamFake) holdSummaries() (arrived <-chan struct{}, release func()) {
	hold, came := make(chan struct{}), make(chan struct{}, 4)

	f.lock.Lock()
	f.summaryHold, f.summaryArrived = hold, came
	f.lock.Unlock()

	var once sync.Once

	return came, func() {
		once.Do(func() {
			f.lock.Lock()
			f.summaryHold, f.summaryArrived = nil, nil
			f.lock.Unlock()
			close(hold)
		})
	}
}

// summaryCount is how many GetPlayerSummaries calls the fake has seen.
func (f *steamFake) summaryCount() int {
	f.lock.Lock()
	defer f.lock.Unlock()

	return len(f.summaryQueries)
}

// Hytale.

// hytaleIDTokenFor is the ID token the fake issues for a grant.
func hytaleIDTokenFor(grant hytaleGrant) string {
	if grant.idToken != "" {
		return grant.idToken
	}

	claims := map[string]any{
		"iss": hytaleIssuer, "aud": testHytaleClientID,
		"exp": time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC).Unix(), "iat": 1,
		"sub": testHytaleSub, "nonce": grant.nonce,
	}
	for name, value := range grant.claims {
		if value == nil {
			delete(claims, name)
			continue
		}
		claims[name] = value
	}

	return fakeIDToken(claims)
}

// grantHytale tells the fake Hytale that the person behind a sign-in Hytale
// was sent to by location approved it: code is the code it sends back.
func (s *accountsScene) grantHytale(t *testing.T, location *url.URL, code string, grant hytaleGrant) {
	t.Helper()

	query := location.Query()
	grant.challenge = query.Get("code_challenge")
	grant.redirect = query.Get("redirect_uri")
	if grant.nonce == "" {
		grant.nonce = query.Get("nonce")
	}

	s.hytale.lock.Lock()
	defer s.hytale.lock.Unlock()

	s.hytale.grants[code] = grant
}

// hytaleProfile is the usual userinfo answer.
func hytaleProfile(uuid, username string) map[string]any {
	return map[string]any{"sub": testHytaleSub, "profile": map[string]any{"uuid": uuid, "username": username}}
}

// connectHytale is the whole of connecting Hytale in the browser, picking a
// profile.
func (s *accountsScene) connectHytale(t *testing.T, userID, uuid, username string) *httptest.ResponseRecorder {
	t.Helper()

	state, location := s.startConnect(t, hytaleProvider, userID)
	code := "code-" + userID + "-" + uuid
	s.grantHytale(t, location, code, hytaleGrant{info: hytaleProfile(uuid, username)})

	response := s.callback(hytaleProvider, userID, web(userID), "code="+code+"&scope=openid+hytale%3Aprofile&state="+state)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	return response
}

// hytaleClientFrom reads an Authorization header as fosite does: Basic, then
// each half URL-unescaped.
func hytaleClientFrom(header string) (string, string, bool) {
	encoded, found := strings.CutPrefix(header, "Basic ")
	if !found {
		return "", "", false
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", false
	}
	escapedID, escapedSecret, found := strings.Cut(string(decoded), ":")
	if !found {
		return "", "", false
	}
	id, err1 := url.QueryUnescape(escapedID)
	secret, err2 := url.QueryUnescape(escapedSecret)

	return id, secret, err1 == nil && err2 == nil
}

func (s *accountsScene) routeHytale() {
	f := s.hytale

	s.fake.route("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		f.lock.Lock()
		f.tokenAuth = append(f.tokenAuth, r.Header.Get("Authorization"))
		f.tokenBodies = append(f.tokenBodies, string(body))
		status, override := f.tokenStatus, f.tokenBody
		f.lock.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if status != 0 {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, override)
			return
		}

		id, secret, ok := hytaleClientFrom(r.Header.Get("Authorization"))
		if !ok || id != testHytaleClientID || secret != testHytaleSecret {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"invalid_client"}`)
			return
		}

		form, err := url.ParseQuery(string(body))
		if err != nil || form.Get("client_secret") != "" || form.Get("grant_type") != "authorization_code" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_request"}`)
			return
		}

		f.lock.Lock()
		grant, known := f.grants[form.Get("code")]
		delete(f.grants, form.Get("code"))
		if known {
			f.grants["spent-"+form.Get("code")] = grant
		}
		f.lock.Unlock()

		// PKCE: the verifier must hash to the challenge the authorize
		// request carried, and the redirect must be the same string.
		sum := sha256.Sum256([]byte(form.Get("code_verifier")))
		if !known || base64.RawURLEncoding.EncodeToString(sum[:]) != grant.challenge || form.Get("redirect_uri") != grant.redirect {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": testHytaleAccessToken + form.Get("code"), "id_token": hytaleIDTokenFor(grant),
			"expires_in": 3600, "scope": "openid hytale:profile", "token_type": "bearer",
		})
	})

	s.fake.route("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		f.lock.Lock()
		f.userinfos++
		code := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "+testHytaleAccessToken)
		grant, known := f.grants["spent-"+code]
		hold, arrived := f.infoHold, f.infoArrived
		f.lock.Unlock()

		if hold != nil {
			arrived <- struct{}{}
			<-hold
		}

		if !known {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"request_unauthorized"}`)
			return
		}

		contentType := grant.infoType
		if contentType == "" {
			contentType = "application/json; charset=utf-8"
		}
		w.Header().Set("Content-Type", contentType)

		info := grant.info
		if info == nil {
			info = hytaleProfile(daveHytaleUUID, "Steve_Hytale")
		}
		_ = json.NewEncoder(w).Encode(info)
	})

	s.fake.route("/oauth2/revoke", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(body))

		f.lock.Lock()
		f.revokes = append(f.revokes, r.Header.Get("Authorization"))
		f.revokeForms = append(f.revokeForms, form)
		f.lock.Unlock()

		w.WriteHeader(http.StatusOK)
	})
}

// holdUserinfo keeps the next /userinfo requests open until the returned
// release is called; arrived says when each one has come in.
func (f *hytaleFake) holdUserinfo() (arrived <-chan struct{}, release func()) {
	hold, came := make(chan struct{}), make(chan struct{}, 4)

	f.lock.Lock()
	f.infoHold, f.infoArrived = hold, came
	f.lock.Unlock()

	var once sync.Once

	return came, func() {
		once.Do(func() {
			f.lock.Lock()
			f.infoHold, f.infoArrived = nil, nil
			f.lock.Unlock()
			close(hold)
		})
	}
}

func (f *hytaleFake) revoked() ([]string, []url.Values) {
	f.lock.Lock()
	defer f.lock.Unlock()

	return append([]string(nil), f.revokes...), append([]url.Values(nil), f.revokeForms...)
}

func (f *hytaleFake) tokenRequests() ([]string, []string) {
	f.lock.Lock()
	defer f.lock.Unlock()

	return append([]string(nil), f.tokenAuth...), append([]string(nil), f.tokenBodies...)
}

func (f *hytaleFake) answerTokens(status int, body string) {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.tokenStatus, f.tokenBody = status, body
}
