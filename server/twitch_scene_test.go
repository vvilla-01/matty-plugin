package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
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

// Twitch live's tests (design/twitch-live.md §11.1) share this: a server with
// people on it, a real compare-and-set store that can be made to fail, the
// sessions Mattermost would hand the plugin, and Twitch itself as a local fake
// (the media fake, which stands in for id.twitch.tv, Helix and Twitch's image
// host). Nothing leaves the machine, and nothing sleeps: the clock is moved by
// hand and the loop's steps are run directly.

// The people. Ids have Mattermost's shape, because the admin routes check it.
const (
	twDave  = "dave0000000000000000000000" // linked to videokey-dave
	twErin  = "erin0000000000000000000000" // linked to videokey-erin
	twBob   = "ashk0000000000000000000000" // linked to videokey-bob
	twBoss  = "boss0000000000000000000000" // a system admin, linked to videokey-boss
	twDan   = "danm0000000000000000000000" // neither linked nor let in
	twLetIn = "letn0000000000000000000000" // let in to watch
	twGone  = "gone0000000000000000000000" // a deactivated account
)

// Their Twitch accounts.
var (
	daveTwitch  = twitchUser{ID: "1001", Login: "dave_tv", DisplayName: "Dave_TV"}
	erinTwitch  = twitchUser{ID: "1002", Login: "erin_streams", DisplayName: "Erin_Streams"}
	bobTwitch   = twitchUser{ID: "1003", Login: "ashk", DisplayName: "BobK"}
	otherTwitch = twitchUser{ID: "1009", Login: "somebody_else", DisplayName: "Somebody_Else"}
)

// fakeGrant is what the fake Twitch knows about one authorization code: the
// ID token it will issue for it, and whose /helix/users says its token is.
type fakeGrant struct {
	sub         string
	nonce       string
	aud         any
	who         twitchUser
	usersStatus int // when not zero, /helix/users answers this for the code's token
}

// twitchFake is Twitch: tokens, ID tokens, Get Users, Get Streams, revoke and
// preview pictures.
type twitchFake struct {
	*mediaFake

	lock          sync.Mutex
	grants        map[string]fakeGrant
	live          map[string]helixStream
	accounts      map[string]twitchUser
	streamsStatus int
	rate          http.Header
}

func newTwitchFake(t *testing.T) *twitchFake {
	t.Helper()

	f := &twitchFake{
		mediaFake: newMediaFake(t),
		grants:    map[string]fakeGrant{},
		live:      map[string]helixStream{},
		accounts:  map[string]twitchUser{},
	}

	f.route("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()

		switch r.PostForm.Get("grant_type") {
		case "client_credentials":
			writeTwitchToken(w, "app-token", 5000000)

		case "authorization_code":
			code := r.PostForm.Get("code")

			f.lock.Lock()
			grant, known := f.grants[code]
			f.lock.Unlock()

			if !known || r.PostForm.Get("client_secret") != testTwitchClientSecret {
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			aud := grant.aud
			if aud == nil {
				aud = testTwitchClientID
			}
			idToken := fakeIDToken(map[string]any{
				"iss": twitchIssuer, "aud": aud, "azp": testTwitchClientID,
				"exp": time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC).Unix(), "iat": 1,
				"sub": grant.sub, "nonce": grant.nonce,
			})

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "utok-" + code, "refresh_token": "rtok-" + code, "id_token": idToken,
				"expires_in": 14400, "scope": []string{"openid"}, "token_type": "bearer",
			})

		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	})

	f.route("/oauth2/revoke", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	f.route("/helix/users", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Client-Id") != testTwitchClientID {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		f.lock.Lock()
		defer f.lock.Unlock()

		auth := r.Header.Get("Authorization")
		var data []map[string]string
		switch {
		case auth == "Bearer app-token":
			for _, id := range r.URL.Query()["id"] {
				if account, found := f.accounts[id]; found {
					data = append(data, map[string]string{"id": account.ID, "login": account.Login, "display_name": account.DisplayName})
				}
			}

		case strings.HasPrefix(auth, "Bearer utok-"):
			grant, known := f.grants[strings.TrimPrefix(auth, "Bearer utok-")]
			if !known {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if grant.usersStatus != 0 {
				w.WriteHeader(grant.usersStatus)
				return
			}
			data = append(data, map[string]string{"id": grant.who.ID, "login": grant.who.Login, "display_name": grant.who.DisplayName})

		default:
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	})

	f.route("/helix/streams", func(w http.ResponseWriter, r *http.Request) {
		f.lock.Lock()
		defer f.lock.Unlock()

		for name, values := range f.rate {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		if f.streamsStatus != 0 {
			w.WriteHeader(f.streamsStatus)
			return
		}

		data := []helixStream{}
		for _, id := range r.URL.Query()["user_id"] {
			if stream, live := f.live[id]; live {
				data = append(data, stream)
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	})

	return f
}

// fakeIDToken is an ID token with these claims, signed by nobody: VillaNET
// checks the claims of a token it got from Twitch over TLS, not a signature.
func fakeIDToken(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	body, _ := json.Marshal(claims)

	return header + "." + base64.RawURLEncoding.EncodeToString(body) + ".c2lnbmF0dXJl"
}

func (f *twitchFake) grant(code string, grant fakeGrant) {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.grants[code] = grant
}

// goLive puts somebody on the air on Twitch, with a picture.
func (f *twitchFake) goLive(who twitchUser, title, game string, viewers int, started time.Time) {
	f.lock.Lock()
	f.live[who.ID] = helixStream{
		UserID: who.ID, UserLogin: who.Login, UserName: who.DisplayName,
		GameName: game, Type: "live", Title: title, ViewerCount: viewers,
		StartedAt:    started.UTC().Format(time.RFC3339),
		ThumbnailURL: "https://" + f.host() + "/previews-ttv/live_user_" + who.Login + "-{width}x{height}.jpg",
	}
	f.lock.Unlock()

	f.route("/previews-ttv/live_user_"+who.Login+"-640x360.jpg", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(jpegBytes)
	})
}

func (f *twitchFake) goOffline(id string) {
	f.lock.Lock()
	defer f.lock.Unlock()

	delete(f.live, id)
}

func (f *twitchFake) setStreamsStatus(status int) {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.streamsStatus = status
}

func (f *twitchFake) setAccount(who twitchUser) {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.accounts[who.ID] = who
}

func (f *twitchFake) dropAccount(id string) {
	f.lock.Lock()
	defer f.lock.Unlock()

	delete(f.accounts, id)
}

// twitchScene is the server.
type twitchScene struct {
	p      *Plugin
	api    *plugintest.API
	store  map[string][]byte
	faults *kvFaults
	fake   *twitchFake
	clock  *fakeClock

	lock     sync.Mutex
	site     string
	expiries map[string]int64
	users    map[string]*model.User
}

// immediately is an after that has already happened.
func immediately(time.Duration) <-chan time.Time {
	ready := make(chan time.Time, 1)
	ready <- time.Time{}

	return ready
}

// never is an after that never comes, for the waits a test does not want to
// happen on their own: the purge worker's minute after a store error.
func never(time.Duration) <-chan time.Time {
	return nil
}

func twitchLiveConfig() *configuration {
	return &configuration{
		BroadcastBoxURL:    "http://192.0.2.10:8080",
		TwitchClientID:     testTwitchClientID,
		TwitchClientSecret: testTwitchClientSecret,
		EnableTwitchLive:   true,
		EnableLeaderboard:  true,
		EnableLiveBadge:    true,
		LivePreviews:       true,
		AlwaysOnStreamKeys: "videokey-watts",
		OfflineGraceChecks: 2,
	}
}

func newTwitchScene(t *testing.T) *twitchScene {
	t.Helper()

	return newTwitchSceneWith(t, twitchLiveConfig())
}

func newTwitchSceneWith(t *testing.T, cfg *configuration) *twitchScene {
	t.Helper()

	fake := newTwitchFake(t)
	p, store, faults := kvBackedPluginWithFaults(map[string][]byte{})
	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	s := &twitchScene{
		p: p, api: api, store: store, faults: faults, fake: fake, clock: newFakeClock(),
		site:     "https://chat.example.com/mm",
		expiries: map[string]int64{},
		users: map[string]*model.User{
			twDave:  {Id: twDave, Username: "dave", Nickname: "Dave"},
			twErin:  {Id: twErin, Username: "erin", Nickname: "Erin"},
			twBob:   {Id: twBob, Username: "bob", Nickname: "Bob"},
			twBoss:  {Id: twBoss, Username: "boss", Nickname: "Victor"},
			twDan:   {Id: twDan, Username: "dan", Nickname: "Dan"},
			twLetIn: {Id: twLetIn, Username: "olivia", Nickname: "Olivia"},
			twGone:  {Id: twGone, Username: "gone", Nickname: "Gone", DeleteAt: 1},
		},
	}

	faults.lock.Lock()
	linkForTest(store, twDave, "videokey-dave")
	linkForTest(store, twErin, "videokey-erin")
	linkForTest(store, twBob, "videokey-bob")
	linkForTest(store, twBoss, "videokey-boss")
	store[kvLetIn] = []byte(`{"version":1,"people":{"` + twLetIn + `":{"at":1,"by":"` + twBoss + `"}}}`)
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
	})
	api.On("GetUserByUsername", mock.Anything).Return(nil, model.NewAppError("GetUserByUsername", "app.user.missing", nil, "", http.StatusNotFound)).Maybe()
	clipsAdmins(api, twBoss)

	api.On("GetSession", mock.Anything).Return(func(sessionID string) (*model.Session, *model.AppError) {
		kind, userID, found := strings.Cut(sessionID, "-")
		if !found {
			return nil, model.NewAppError("GetSession", "app.session.missing", nil, "", http.StatusUnauthorized)
		}

		session := &model.Session{Id: sessionID, UserId: userID, Props: model.StringMap{}}
		switch kind {
		case "web", "tab": // two interactive sessions, as two browsers would be
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
	})

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
	})

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

	// The badge: a custom status, and what was there before it.
	api.On("UpdateUserCustomStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
	api.On("RemoveUserCustomStatus", mock.Anything).Return(nil).Maybe()

	cfg.normalize()
	p.setConfiguration(cfg)
	p.refreshCh = make(chan struct{}, 1)
	p.offlineStreak = map[string]int{}

	live := p.twitchLive()
	live.now = s.clock.now
	live.after = never
	app := p.twitchApp()
	app.now = s.clock.now
	app.after = immediately

	t.Cleanup(func() {
		p.stopTwitchLive()
		p.stopTwitchPurges()
	})

	return s
}

func (s *twitchScene) now() time.Time { return s.clock.now() }

func (s *twitchScene) setSite(site string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.site = site
}

func (s *twitchScene) expiryOf(key string) int64 {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.expiries[key]
}

func (s *twitchScene) kv(key string) []byte {
	s.faults.lock.Lock()
	defer s.faults.lock.Unlock()

	value, present := s.store[key]
	if !present {
		return nil
	}

	return append([]byte(nil), value...)
}

func (s *twitchScene) setKV(key string, value []byte) {
	s.faults.lock.Lock()
	defer s.faults.lock.Unlock()

	if value == nil {
		delete(s.store, key)
		return
	}
	s.store[key] = value
}

// keysWith is every stored key with this prefix.
func (s *twitchScene) keysWith(prefix string) []string {
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

// links is the stored link document.
func (s *twitchScene) links(t *testing.T) *twitchLinks {
	t.Helper()

	doc, err := decodeTwitchLinks(s.kv(kvTwitchLinks))
	require.NoError(t, err)

	return doc
}

// putLinks stores a link document as it would be after the people connected.
func (s *twitchScene) putLinks(t *testing.T, doc *twitchLinks) {
	t.Helper()

	if doc.V == 0 {
		doc.V = twitchLinksVersion
	}
	encoded, err := json.Marshal(doc)
	require.NoError(t, err)
	s.setKV(kvTwitchLinks, encoded)

	// The cache, as the loop's next read would make it.
	fresh, err := decodeTwitchLinks(encoded)
	require.NoError(t, err)
	s.p.twitchLive().noteLinks(fresh)
}

// connected is a link document with these people connected an hour before
// the scene's clock started.
func (s *twitchScene) connected(t *testing.T, people map[string]twitchUser) {
	t.Helper()

	at := s.now().Add(-time.Hour).UnixMilli()
	doc := &twitchLinks{V: twitchLinksVersion, Users: map[string]twitchLink{}}
	for userID, who := range people {
		doc.Users[userID] = twitchLink{ID: who.ID, Login: who.Login, Name: who.DisplayName, At: at, Checked: at}
	}
	s.putLinks(t, doc)
}

// serve makes one request through ServeHTTP, in a session.
func (s *twitchScene) serve(method, target, userID, sessionID, body string, header map[string]string) *httptest.ResponseRecorder {
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

func web(userID string) string { return "web-" + userID }

// ticket is the first press of Connect Twitch.
func (s *twitchScene) ticket(t *testing.T, userID string) string {
	t.Helper()

	response := s.serve(http.MethodPost, twitchConnectTicketPath, userID, web(userID), "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var answer struct {
		Ticket    string `json:"ticket"`
		ExpiresIn int    `json:"expiresIn"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer))

	return answer.Ticket
}

// connectWith is the second press, with a ticket in hand.
func (s *twitchScene) connectWith(userID, sessionID, ticket string, header map[string]string) *httptest.ResponseRecorder {
	return s.serve(http.MethodGet, twitchConnectPath+"?t="+url.QueryEscape(ticket), userID, sessionID, "", header)
}

// startConnect is both presses: the state and the nonce Twitch was sent.
func (s *twitchScene) startConnect(t *testing.T, userID string) (state, nonce string) {
	t.Helper()

	response := s.connectWith(userID, web(userID), s.ticket(t, userID), nil)
	require.Equal(t, http.StatusFound, response.Code, response.Body.String())

	location, err := url.Parse(response.Header().Get("Location"))
	require.NoError(t, err)

	return location.Query().Get("state"), location.Query().Get("nonce")
}

// callback is Twitch sending the browser back.
func (s *twitchScene) callback(userID, sessionID, query string) *httptest.ResponseRecorder {
	return s.serve(http.MethodGet, twitchCallbackPath+"?"+query, userID, sessionID, "", nil)
}

// connectAs is the whole of connecting, approved on Twitch as who.
func (s *twitchScene) connectAs(t *testing.T, userID string, who twitchUser) *httptest.ResponseRecorder {
	t.Helper()

	state, nonce := s.startConnect(t, userID)
	code := "code-" + userID + "-" + who.ID
	s.fake.grant(code, fakeGrant{sub: who.ID, nonce: nonce, who: who})

	response := s.callback(userID, web(userID), "code="+code+"&scope=openid&state="+state)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	return response
}

// step is one pass of the loop, now.
func (s *twitchScene) step(t *testing.T) {
	t.Helper()

	s.p.twitchLive().step(t.Context(), s.p.getConfiguration(), s.now())
}

// twitchEvents is who has been sent the "me" event, in order.
func (s *twitchScene) twitchEvents(t *testing.T) []string {
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
func (s *twitchScene) logged() []string {
	var lines []string
	for _, call := range s.api.Calls {
		if strings.HasPrefix(call.Method, "Log") {
			lines = append(lines, fmt.Sprint(call.Arguments...))
		}
	}

	return lines
}

// me is /me as somebody sees it.
func (s *twitchScene) me(t *testing.T, userID string) meResponse {
	t.Helper()

	response := s.serve(http.MethodGet, "/api/v1/me", userID, web(userID), "", nil)
	require.Equal(t, http.StatusOK, response.Code)

	var answer meResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer))

	return answer
}

// waitForPurges waits for the purge worker, if one is running, to finish.
func (s *twitchScene) waitForPurges() {
	live := s.p.twitchLive()

	live.purgeLock.Lock()
	done := live.purgeDone
	live.purgeLock.Unlock()

	if done != nil {
		<-done
	}
}

// requestsTo is how many requests the fake Twitch had for a path.
func (s *twitchScene) requestsTo(path string) int {
	return s.fake.requestsTo(path)
}

// seedTable stores an airtime table.
func (s *twitchScene) seedTable(t *testing.T, key string, table statTable) {
	t.Helper()

	encoded, err := json.Marshal(table)
	require.NoError(t, err)
	s.setKV(key, encoded)
}

func (s *twitchScene) table(t *testing.T, key string) statTable {
	t.Helper()

	table, err := s.p.loadStats(key)
	require.NoError(t, err)

	return table
}

// cardOf is the entry with this handle in the published snapshot.
func cardOf(t *testing.T, streams []apiStream, handle string) apiStream {
	t.Helper()

	for _, stream := range streams {
		if stream.Handle == handle {
			return stream
		}
	}
	require.Failf(t, "no such card", "handle %s", handle)

	return apiStream{}
}

func hasCard(streams []apiStream, handle string) bool {
	for _, stream := range streams {
		if stream.Handle == handle {
			return true
		}
	}

	return false
}
