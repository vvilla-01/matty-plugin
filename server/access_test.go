package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The lock (design/lock-screen.md §3.12). Every test here is deterministic:
// the access gate's clock is the scene's, which only moves when a test moves
// it, and nothing sleeps.

// linkForTest is a link as setLink leaves it: both entries, so that the
// agreement rule (linkOf) finds the person linked.
func linkForTest(store map[string][]byte, userID, key string) {
	store[kvLinkByUserPrefix+userID] = []byte(key)
	store[kvLinkByKeyPrefix+normalizeKey(key)] = []byte(userID)
}

// The six kinds of caller every table here is about.
const (
	sceneLocked      = "uid-dan"   // neither linked nor let in
	sceneLetIn       = "uid-erin"  // let in by an admin
	sceneLinked      = "uid-dave"  // linked to videokey-dave
	sceneLockedAdmin = "uid-boss"  // a system admin with no token
	sceneLetInAdmin  = "uid-chief" // a system admin let in
	sceneLinkedAdmin = "uid-bob"   // a system admin linked to videokey-bob
)

var sceneCallers = []string{sceneLocked, sceneLetIn, sceneLinked, sceneLockedAdmin, sceneLetInAdmin, sceneLinkedAdmin}

// accessScene is a server with those six people on it, a real
// compare-and-set store that can be made to fail, accounts to look up, three
// live streams (Dave's, linked; Dan's, matched to her only by username; and a
// 24/7 channel), every meter made as OnActivate makes them, and a clock that
// stands still.
type accessScene struct {
	p      *Plugin
	api    *plugintest.API
	store  map[string][]byte
	faults *kvFaults

	lock      sync.Mutex
	now       time.Time
	reads     map[string]int
	users     map[string]*model.User
	userFails map[string]bool
}

func newAccessScene(t *testing.T) *accessScene {
	t.Helper()

	return newAccessSceneOn(t, map[string][]byte{}, newKVFaults())
}

func newKVFaults() *kvFaults {
	return &kvFaults{readFails: map[string]bool{}, writeFails: map[string]bool{}, writes: map[string]int{}}
}

// newAccessSceneOn is another node over the same store.
func newAccessSceneOn(t *testing.T, store map[string][]byte, faults *kvFaults) *accessScene {
	t.Helper()

	p, store, faults := kvBackedPluginWithFaultsOn(store, faults)
	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	s := &accessScene{
		p:         p,
		api:       api,
		store:     store,
		faults:    faults,
		now:       time.Date(2026, 10, 5, 20, 0, 0, 0, time.UTC),
		reads:     map[string]int{},
		userFails: map[string]bool{},
		users: map[string]*model.User{
			sceneLocked:      {Id: sceneLocked, Username: "dan", FirstName: "Dan"},
			sceneLetIn:       {Id: sceneLetIn, Username: "erin", FirstName: "Erin"},
			sceneLinked:      {Id: sceneLinked, Username: "dave", FirstName: "Dave"},
			sceneLockedAdmin: {Id: sceneLockedAdmin, Username: "boss", FirstName: "Victor"},
			sceneLetInAdmin:  {Id: sceneLetInAdmin, Username: "chief"},
			sceneLinkedAdmin: {Id: sceneLinkedAdmin, Username: "bob", FirstName: "Bob"},
			"uid-bot":        {Id: "uid-bot", Username: "robot", IsBot: true},
			"uid-old":        {Id: "uid-old", Username: "oldtimer", DeleteAt: 1},
		},
	}

	faults.lock.Lock()
	if _, seeded := store[kvLetIn]; !seeded {
		linkForTest(store, sceneLinked, "videokey-dave")
		linkForTest(store, sceneLinkedAdmin, "videokey-bob")
		store[kvLetIn] = []byte(`{"version":1,"people":{"` + sceneLetIn + `":{"at":1,"by":"` + sceneLockedAdmin + `"},"` +
			sceneLetInAdmin + `":{"at":2,"by":"` + sceneLockedAdmin + `"}}}`)
	}
	faults.lock.Unlock()

	faults.setOnRead(func(key string) {
		s.lock.Lock()
		defer s.lock.Unlock()

		s.reads[key]++
	})

	api.On("GetUser", mock.Anything).Return(func(userID string) (*model.User, *model.AppError) {
		s.lock.Lock()
		defer s.lock.Unlock()

		if s.userFails[userID] {
			return nil, model.NewAppError("GetUser", "app.user.get.app_error", nil, "injected failure", http.StatusInternalServerError)
		}
		user, known := s.users[userID]
		if !known {
			return nil, model.NewAppError("GetUser", "app.user.missing.app_error", nil, "", http.StatusNotFound)
		}
		copied := *user

		return &copied, nil
	})
	api.On("GetUserByUsername", mock.Anything).Return(func(username string) (*model.User, *model.AppError) {
		s.lock.Lock()
		defer s.lock.Unlock()

		if s.userFails["@"+username] {
			return nil, model.NewAppError("GetUserByUsername", "app.user.get_by_username.app_error", nil, "injected failure", http.StatusInternalServerError)
		}
		for _, user := range s.users {
			if user.Username == username {
				copied := *user

				return &copied, nil
			}
		}

		return nil, model.NewAppError("GetUserByUsername", "app.user.get_by_username.app_error", nil, "", http.StatusNotFound)
	})
	clipsAdmins(api, sceneLockedAdmin, sceneLetInAdmin, sceneLinkedAdmin)
	api.On("KVList", mock.Anything, mock.Anything).Return(func(page, perPage int) ([]string, *model.AppError) {
		faults.lock.Lock()
		defer faults.lock.Unlock()

		keys := make([]string, 0, len(store))
		for key := range store {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		from, to := min(page*perPage, len(keys)), min((page+1)*perPage, len(keys))

		return keys[from:to], nil
	}).Maybe()
	api.On("GetBot", mock.Anything, mock.Anything).Return(nil, model.NewAppError("GetBot", "app.bot.missing", nil, "", http.StatusNotFound)).Maybe()

	// Twitch connections' card shows the callback address, from the Site
	// URL, which this server has none of.
	api.On("GetConfig").Return(&model.Config{}).Maybe()

	cfg := &configuration{
		BroadcastBoxURL:    "http://192.0.2.10:8080",
		AlwaysOnStreamKeys: "videokey-watts",
		LivePreviews:       true,
		PublicSiteURL:      "https://tv.example.com",
		TopWatchers:        true,
		AutoLinkByUsername: true,
		EnableClips:        true,
	}
	cfg.normalize()
	p.setConfiguration(cfg)

	p.setSnapshot(&snapshot{Streams: []apiStream{
		{StreamKey: "videokey-dave", Handle: p.streamHandle("videokey-dave"), Title: "Dave", UserID: sceneLinked, LinkSource: linkSourceManual},
		{StreamKey: "videokey-dan", Handle: p.streamHandle("videokey-dan"), Title: "Dan", UserID: sceneLocked, LinkSource: linkSourceUsername},
		{StreamKey: "videokey-watts", Handle: p.streamHandle("videokey-watts"), Title: "Alan Watts Chillstep", AlwaysOn: true},
	}, Configured: true, LivePreviews: true})

	p.linkLimiter = newRateLimiter(5, 15*time.Minute)
	p.profileLimiter = newRateLimiter(10, time.Minute)
	p.playbackLimiter = newRateLimiter(60, time.Minute)
	p.communityLimiter = newRateLimiter(30, time.Minute)
	p.nowPlayingLimiter = newRateLimiter(10, time.Minute)

	p.accessCache().now = func() time.Time {
		s.lock.Lock()
		defer s.lock.Unlock()

		return s.now
	}

	return s
}

func (s *accessScene) advance(by time.Duration) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.now = s.now.Add(by)
}

func (s *accessScene) resetReads() {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.reads = map[string]int{}
}

func (s *accessScene) readsOf(key string) int {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.reads[key]
}

func (s *accessScene) totalReads() int {
	s.lock.Lock()
	defer s.lock.Unlock()

	total := 0
	for _, count := range s.reads {
		total += count
	}

	return total
}

func (s *accessScene) failUser(key string, failing bool) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.userFails[key] = failing
}

func (s *accessScene) kv(key string) []byte {
	s.faults.lock.Lock()
	defer s.faults.lock.Unlock()

	return append([]byte(nil), s.store[key]...)
}

func (s *accessScene) setKV(key string, value []byte) {
	s.faults.lock.Lock()
	defer s.faults.lock.Unlock()

	if value == nil {
		delete(s.store, key)
		return
	}
	s.store[key] = value
}

// serve makes one request through ServeHTTP, as Mattermost would.
func (s *accessScene) serve(method, target, userID, body string, header map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if userID != "" {
		request.Header.Set("Mattermost-User-Id", userID)
	}
	for name, value := range header {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()

	s.p.ServeHTTP(nil, recorder, request)

	return recorder
}

// gate asks layer 1 alone about a request.
func (s *accessScene) gate(method, target, userID string, header map[string]string) (bool, *httptest.ResponseRecorder) {
	request := httptest.NewRequest(method, target, nil)
	for name, value := range header {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()

	return s.p.passAccessGate(recorder, request, userID), recorder
}

// meEventOfAFreshServer is all a "me" event carries on a server with no Name
// set and no artwork uploaded.
var meEventOfAFreshServer = map[string]any{
	"name":    "Matty",
	"artwork": map[string]any{"emblem": false, "ghost": false},
}

// accessEvents is who has been sent the "me" event, in order, checking each
// carries nothing about anybody.
func (s *accessScene) accessEvents(t *testing.T) []string {
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
		require.Empty(t, broadcast.ChannelId)
		require.Empty(t, broadcast.TeamId)
		require.NotEmpty(t, broadcast.UserId, "to one person, never everybody")
		told = append(told, broadcast.UserId)
	}

	return told
}

// codedError reads a refusal.
func codedError(t *testing.T, recorder *httptest.ResponseRecorder) errorResponse {
	t.Helper()

	var answer errorResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer), recorder.Body.String())

	return answer
}

func requireRefused(t *testing.T, recorder *httptest.ResponseRecorder, status int, code, about string) {
	t.Helper()

	require.Equal(t, status, recorder.Code, "%s: %s", about, recorder.Body.String())
	require.Equal(t, code, codedError(t, recorder).Code, about)
}

// T1. Every quoted /api/v1/ path in a case of ServeHTTP's switch is classified
// or is a System Console path, every class names a route that is served, and
// nothing is dispatched above the gate but the static files, the two clip
// families and the six connect pages (Twitch's, Steam's and Hytale's connect
// windows and callbacks).
func TestEveryRouteHasAnAccessClass(t *testing.T) {
	source, err := os.ReadFile("api.go")
	require.NoError(t, err)

	text := string(source)
	start := strings.Index(text, "func (p *Plugin) ServeHTTP(")
	require.Positive(t, start)
	end := strings.Index(text[start:], "\n}\n")
	require.Positive(t, end)
	body := text[start : start+end]

	caseLine := regexp.MustCompile(`(?m)^\s*case\s+(.*):\s*$`)
	quoted := regexp.MustCompile(`"(/api/v1/[^"]*)"`)

	served := map[string]bool{}
	for _, line := range caseLine.FindAllStringSubmatch(body, -1) {
		for _, path := range quoted.FindAllStringSubmatch(line[1], -1) {
			served[path[1]] = true
		}
	}
	require.Contains(t, served, "/api/v1/schedule/join", "a case that lists several paths is read whole")

	for path := range served {
		if strings.HasPrefix(path, adminPathRoot+"/") {
			continue
		}
		require.Contains(t, routeClasses, path, "%s is served but has no access class: say who it is for in routeClasses", path)
	}
	for path := range routeClasses {
		require.True(t, served[path], "%s has an access class but is not served", path)
	}

	// Nothing may be dispatched above the gate without its own check.
	dispatch := regexp.MustCompile(`(?:underPath\(r\.URL\.Path,|strings\.HasPrefix\(r\.URL\.Path,)\s*([^)]+)\)`)
	allowed := map[string]bool{
		`"/static/"`: true, "clipsPath": true, "adminClipsPath": true,
		"twitchConnectPath": true, "twitchCallbackPath": true,
		"steamConnectPath": true, "steamCallbackPath": true,
		"hytaleConnectPath": true, "hytaleCallbackPath": true,
	}
	gateAt := strings.Index(body, "p.passAccessGate(")
	switchAt := strings.Index(body, "switch r.URL.Path")
	require.Positive(t, gateAt)
	require.Greater(t, switchAt, gateAt, "the gate comes before the switch")

	found := 0
	for _, match := range dispatch.FindAllStringSubmatchIndex(body, -1) {
		argument := strings.TrimSpace(body[match[2]:match[3]])
		require.True(t, allowed[argument], "ServeHTTP dispatches on %s with no access check of its own", argument)
		require.Less(t, match[0], gateAt)
		found++
	}
	require.Equal(t, 9, found)
}

// expectedAt is what the gate does with a caller on a route of a class: let
// them through, or refuse them with a code.
func expectedAt(class routeClass, caller string) (bool, string) {
	level := map[string]access{
		sceneLocked: accessLocked, sceneLockedAdmin: accessLocked,
		sceneLetIn: accessLetIn, sceneLetInAdmin: accessLetIn,
		sceneLinked: accessLinked, sceneLinkedAdmin: accessLinked,
	}[caller]
	admin := caller == sceneLockedAdmin || caller == sceneLetInAdmin || caller == sceneLinkedAdmin

	switch class {
	case routeOpen:
		return true, ""
	case routeWatch:
		if level == accessLocked {
			return false, accessCodeLocked
		}
		return true, ""
	case routeWatchOrAdmin:
		if level == accessLocked && !admin {
			return false, accessCodeLocked
		}
		return true, ""
	default:
		switch level {
		case accessLinked:
			return true, ""
		case accessLetIn:
			return false, accessCodeWatchOnly
		default:
			return false, accessCodeLocked
		}
	}
}

// T2. Every classified route, for each kind of caller; no meter spent on a
// refusal; a refusal remembered for two seconds, with no read; a linked
// person's made-up paths read once per ten seconds; and the pictures the gate
// guards are revalidated, so it hears of every re-display.
func TestRouteAccessTable(t *testing.T) {
	s := newAccessScene(t)

	for path, methods := range routeClasses {
		for method, class := range methods {
			for _, caller := range sceneCallers {
				pass, code := expectedAt(class, caller)
				passed, recorder := s.gate(method, path, caller, nil)
				about := fmt.Sprintf("%s %s as %s", method, path, caller)

				require.Equal(t, pass, passed, about)
				if !pass {
					requireRefused(t, recorder, http.StatusForbidden, code, about)
					require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"), about)
					text := map[string]string{accessCodeLocked: accessLockedText(productName), accessCodeWatchOnly: accessWatchOnlyText}[code]
					require.Equal(t, text, codedError(t, recorder).Error, about)
				}
			}
		}
	}

	// A peek exists only to take a still, which is publishing.
	peek := map[string]string{"X-Matty-Peek": "lease"}
	for _, caller := range sceneCallers {
		pass, code := expectedAt(routeLinked, caller)
		passed, recorder := s.gate(http.MethodPost, "/api/v1/whep", caller, peek)
		require.Equal(t, pass, passed, caller)
		if !pass {
			requireRefused(t, recorder, http.StatusForbidden, code, "peek as "+caller)
		}
	}

	// No meter is spent on a refusal: the gate comes before every one.
	s.advance(time.Minute)
	dave := s.p.streamHandle("videokey-dave")
	metered := []struct {
		method, target, body string
		header               map[string]string
		limiter              *rateLimiter
		watch                bool
	}{
		{http.MethodPost, "/api/v1/me/profile", `{"motd":"x"}`, nil, s.p.profileLimiter, false},
		{http.MethodPost, "/api/v1/nowplaying", `{"category":"game","title":"x"}`, nil, s.p.nowPlayingLimiter, false},
		{http.MethodPost, "/api/v1/schedule", `{"title":"x"}`, nil, s.p.communityLimiter, false},
		{http.MethodPost, "/api/v1/schedule/edit", `{"id":"x"}`, nil, s.p.communityLimiter, false},
		{http.MethodPost, "/api/v1/media/search", `{"kind":"game","q":"x"}`, nil, s.p.mediaStore().searchLimiter, false},
		{http.MethodPost, "/api/v1/stills/claim?handle=" + dave + "&mode=watching", "", nil, s.p.stills().claimLimiter, false},
		{http.MethodPost, "/api/v1/stills?handle=" + dave + "&lease=x", "x", nil, s.p.stills().uploadLimiter, false},
		{http.MethodPost, "/api/v1/whep", viewerOffer, map[string]string{"X-Stream-Handle": dave}, s.p.playbackLimiter, true},
		{http.MethodPost, "/api/v1/whep", viewerOffer, map[string]string{"X-Stream-Handle": dave, "X-Matty-Peek": "lease"}, s.p.playbackLimiter, false},
	}
	for _, caller := range []string{sceneLocked, sceneLetIn} {
		for _, call := range metered {
			if call.watch && caller == sceneLetIn {
				continue
			}

			response := s.serve(call.method, call.target, caller, call.body, call.header)
			require.Equal(t, http.StatusForbidden, response.Code, "%s %s as %s", call.method, call.target, caller)
			_, spent := tokensLeft(call.limiter, caller)
			require.False(t, spent, "%s %s as %s spent a meter", call.method, call.target, caller)
		}
	}

	// A refusal is remembered for two seconds: asking again reads nothing,
	// whatever the route, and once it is two seconds old the next request
	// reads the link once (the let-in list is this node's copy, still fresh).
	s.advance(time.Minute)
	_, _ = s.p.letInHas("anybody")
	s.resetReads()
	passed, _ := s.gate(http.MethodGet, "/api/v1/channel/picture", sceneLocked, nil)
	require.False(t, passed)
	require.Equal(t, 1, s.totalReads())

	s.resetReads()
	for _, target := range []string{"/api/v1/channel/picture", "/api/v1/me/profile", "/api/v1/nope", "/api/v1/stills/claim"} {
		passed, recorder := s.gate(http.MethodGet, target, sceneLocked, nil)
		require.False(t, passed, target)
		requireRefused(t, recorder, http.StatusForbidden, accessCodeLocked, target)
	}
	require.Zero(t, s.totalReads(), "refused from memory")

	// Somebody let in is refused a linked route from memory, and still let
	// through to watch, which reads.
	passed, _ = s.gate(http.MethodPost, "/api/v1/schedule", sceneLetIn, nil)
	require.False(t, passed)
	s.resetReads()
	passed, recorder := s.gate(http.MethodPost, "/api/v1/schedule", sceneLetIn, nil)
	require.False(t, passed)
	requireRefused(t, recorder, http.StatusForbidden, accessCodeWatchOnly, "from memory")
	require.Zero(t, s.totalReads())
	passed, _ = s.gate(http.MethodPost, "/api/v1/whep", sceneLetIn, nil)
	require.True(t, passed)

	// A locked admin refused on a watch route is asked about being an admin
	// on the one route that lets admins through.
	passed, _ = s.gate(http.MethodGet, "/api/v1/channel/picture", sceneLockedAdmin, nil)
	require.False(t, passed)
	passed, _ = s.gate(http.MethodGet, "/api/v1/stills", sceneLockedAdmin, nil)
	require.True(t, passed)

	// Somebody locked who was found not to be an admin there is not asked
	// again for two seconds.
	passed, _ = s.gate(http.MethodGet, "/api/v1/stills", sceneLocked, nil)
	require.False(t, passed)
	asked := len(s.api.Calls)
	s.resetReads()
	passed, _ = s.gate(http.MethodGet, "/api/v1/stills", sceneLocked, nil)
	require.False(t, passed)
	require.Zero(t, s.totalReads())
	for _, call := range s.api.Calls[asked:] {
		require.NotEqual(t, "HasPermissionTo", call.Method)
	}

	s.advance(accessNoTTL)
	s.resetReads()
	passed, _ = s.gate(http.MethodGet, "/api/v1/channel/picture", sceneLocked, nil)
	require.False(t, passed)
	require.Equal(t, 1, s.totalReads(), "two seconds on, exactly one read")

	// A linked person's made-up paths cost one read of the link per ten
	// seconds, and are answered 404 behind the gate.
	s.resetReads()
	for range 5 {
		require.Equal(t, http.StatusNotFound, s.serve(http.MethodGet, "/api/v1/made-up", sceneLinked, "", nil).Code)
	}
	require.Equal(t, 1, s.readsOf(kvLinkByUserPrefix+sceneLinked))
	s.advance(9 * time.Second)
	s.serve(http.MethodGet, "/api/v1/made-up", sceneLinked, "", nil)
	require.Equal(t, 1, s.readsOf(kvLinkByUserPrefix+sceneLinked))
	s.advance(time.Second)
	s.serve(http.MethodGet, "/api/v1/made-up", sceneLinked, "", nil)
	require.Equal(t, 2, s.readsOf(kvLinkByUserPrefix+sceneLinked))

	// The pictures the gate guards: a preview still, and a 24/7 channel's two.
	claim := s.p.stills().claim(dave, sceneLinked, stillModeWatching, time.Now())
	require.NotEmpty(t, claim.Lease)
	_, put := s.p.stills().put(dave, sceneLinked, claim.Lease, cardStill(t), time.Now())
	require.True(t, put)
	require.Equal(t, http.StatusOK, channelAdmin(t, s.p, http.MethodPost, "?key=videokey-watts", pngBytes, sceneLockedAdmin).Code)
	require.Equal(t, http.StatusOK, channelAdmin(t, s.p, http.MethodPost, "?key=videokey-watts&kind=thumb", pngBytes, sceneLockedAdmin).Code)
	watts := s.p.streamHandle("videokey-watts")

	for _, target := range []string{
		"/api/v1/stills?handle=" + dave + "&v=whatever",
		"/api/v1/channel/picture?handle=" + watts,
		"/api/v1/channel/thumbnail?handle=" + watts + "&v=" + pictureVersion(pngBytes),
	} {
		response := s.serve(http.MethodGet, target, sceneLetIn, "", nil)
		require.Equal(t, http.StatusOK, response.Code, target)
		require.Equal(t, "private, no-cache", response.Header().Get("Cache-Control"), target)
		etag := response.Header().Get("ETag")
		require.NotEmpty(t, etag, target)

		held := map[string]string{"If-None-Match": etag}
		require.Equal(t, http.StatusNotModified, s.serve(http.MethodGet, target, sceneLinked, "", held).Code, target)
		requireRefused(t, s.serve(http.MethodGet, target, sceneLocked, "", held), http.StatusForbidden, accessCodeLocked, target)
	}
}

// T3. A path nobody classified is for linked people only: refused to
// everybody else, and only then answered 404; a method a route does not take
// is the same, and only then 405.
func TestUnclassifiedRouteIsLinked(t *testing.T) {
	s := newAccessScene(t)

	requireRefused(t, s.serve(http.MethodGet, "/api/v1/nope", sceneLocked, "", nil), http.StatusForbidden, accessCodeLocked, "locked")
	requireRefused(t, s.serve(http.MethodGet, "/api/v1/nope", sceneLetIn, "", nil), http.StatusForbidden, accessCodeWatchOnly, "let in")
	require.Equal(t, http.StatusNotFound, s.serve(http.MethodGet, "/api/v1/nope", sceneLinked, "", nil).Code)

	requireRefused(t, s.serve(http.MethodPost, "/api/v1/streams", sceneLetIn, "", nil), http.StatusForbidden, accessCodeWatchOnly, "a method nobody listed")
	require.Equal(t, http.StatusMethodNotAllowed, s.serve(http.MethodPost, "/api/v1/streams", sceneLinked, "", nil).Code)
	require.Equal(t, http.StatusMethodNotAllowed, s.serve(http.MethodPut, "/api/v1/whep", sceneLinked, "", nil).Code)

	// No session at all is still 401, before anything.
	require.Equal(t, http.StatusUnauthorized, s.serve(http.MethodGet, "/api/v1/nope", "", "", nil).Code)
}

// adminRoutesInAPI is every System Console path ServeHTTP's switch serves.
func adminRoutesInAPI(t *testing.T) []string {
	t.Helper()

	source, err := os.ReadFile("api.go")
	require.NoError(t, err)

	paths := map[string]bool{}
	for _, match := range regexp.MustCompile(`case\s+"(/api/v1/admin/[^"]*)"`).FindAllStringSubmatch(string(source), -1) {
		paths[match[1]] = true
	}

	sorted := make([]string, 0, len(paths))
	for path := range paths {
		sorted = append(sorted, path)
	}
	sort.Strings(sorted)

	return sorted
}

// T4. Every System Console route answers a locked system admin exactly as it
// answers a linked one, without reading either one's link, and refuses
// somebody who is not an admin.
func TestAdminRoutesIgnoreTheLock(t *testing.T) {
	s := newAccessScene(t)

	// The bot is already made, so the bot's card asks Mattermost about it
	// rather than making it.
	s.p.bridgeLock.Lock()
	s.p.botUserID = "uid-bot"
	s.p.bridgeLock.Unlock()

	type adminCall struct{ method, target string }
	calls := []adminCall{}
	for _, path := range adminRoutesInAPI(t) {
		calls = append(calls, adminCall{http.MethodGet, path})
	}
	for path, methods := range adminClipRoutes {
		for method := range methods {
			calls = append(calls, adminCall{method, path + "?id=" + someClipID})
		}
	}
	require.Greater(t, len(calls), 15)

	// Sent as the System Console sends every request, from script: the
	// sounds card's Listen answers nothing else (design/3.2.0.md §2.2).
	console := map[string]string{"X-Requested-With": "XMLHttpRequest"}

	for _, call := range calls {
		s.resetReads()
		refused := s.serve(call.method, call.target, sceneLocked, "", console)
		require.Contains(t, []int{http.StatusForbidden, http.StatusMethodNotAllowed}, refused.Code, "%s %s", call.method, call.target)
		require.NotEqual(t, accessCodeLocked, codedErrorOrEmpty(refused), "never the lock's refusal")

		s.resetReads()
		locked := s.serve(call.method, call.target, sceneLockedAdmin, "", console)
		require.Zero(t, s.readsOf(kvLinkByUserPrefix+sceneLockedAdmin), "%s %s read the admin's link", call.method, call.target)

		s.resetReads()
		linked := s.serve(call.method, call.target, sceneLinkedAdmin, "", console)
		require.Zero(t, s.readsOf(kvLinkByUserPrefix+sceneLinkedAdmin), "%s %s read the admin's link", call.method, call.target)

		require.Equal(t, linked.Code, locked.Code, "%s %s", call.method, call.target)
		require.NotEqual(t, http.StatusForbidden, locked.Code, "%s %s: %s", call.method, call.target, locked.Body.String())
	}
}

// codedErrorOrEmpty is the code of an answer that has one.
func codedErrorOrEmpty(recorder *httptest.ResponseRecorder) string {
	var answer errorResponse
	_ = json.Unmarshal(recorder.Body.Bytes(), &answer)

	return answer.Code
}

// T5. The one panel route the System Console uses lets a locked admin through,
// and nobody else who is locked.
func TestStillsGetLetsLockedAdminsThrough(t *testing.T) {
	s := newAccessScene(t)
	dave := s.p.streamHandle("videokey-dave")
	claim := s.p.stills().claim(dave, sceneLinked, stillModeWatching, time.Now())
	_, put := s.p.stills().put(dave, sceneLinked, claim.Lease, cardStill(t), time.Now())
	require.True(t, put)

	target := "/api/v1/stills?handle=" + dave
	require.Equal(t, http.StatusOK, s.serve(http.MethodGet, target, sceneLockedAdmin, "", nil).Code)
	requireRefused(t, s.serve(http.MethodGet, target, sceneLocked, "", nil), http.StatusForbidden, accessCodeLocked, "locked")

	// And only to look: taking one is for linked people, admins included.
	requireRefused(t, s.serve(http.MethodPost, "/api/v1/stills/claim?handle="+dave+"&mode=watching", sceneLockedAdmin, "", nil),
		http.StatusForbidden, accessCodeLocked, "claim")
}

// T6. A store that does not answer is 503 "store", never "locked" and never
// "let in", and /me says "unknown". Linked people never depend on the let-in
// list, and an older copy of it never covers a failed read.
func TestAccessStoreErrorsAre503(t *testing.T) {
	s := newAccessScene(t)

	for _, key := range []string{kvLinkByUserPrefix + sceneLinked, kvLinkByKeyPrefix + "videokey-dave"} {
		s.faults.failRead(key)
		for _, target := range []string{"/api/v1/channel/picture", "/api/v1/me/profile"} {
			passed, recorder := s.gate(http.MethodGet, target, sceneLinked, nil)
			require.False(t, passed, key)
			requireRefused(t, recorder, http.StatusServiceUnavailable, accessCodeStore, key+" "+target)
			require.Equal(t, accessStoreText(productName), codedError(t, recorder).Error)
		}

		me := meOf(t, s.p, sceneLinked)
		require.Equal(t, accessUnknown, me["access"], key)
		require.Empty(t, me["streamKey"], key)
		s.faults.healRead(key)
	}

	// Not remembered: the next request, healed, passes at once.
	passed, _ := s.gate(http.MethodGet, "/api/v1/me/profile", sceneLinked, nil)
	require.True(t, passed)

	// The let-in list: a copy eleven seconds old is due, and a read that fails
	// is not covered by it.
	passed, _ = s.gate(http.MethodGet, "/api/v1/channel/picture", sceneLetIn, nil)
	require.True(t, passed)
	s.advance(11 * time.Second)
	s.faults.failRead(kvLetIn)

	passed, recorder := s.gate(http.MethodGet, "/api/v1/channel/picture", sceneLetIn, nil)
	require.False(t, passed)
	requireRefused(t, recorder, http.StatusServiceUnavailable, accessCodeStore, "let in, list unreadable")
	require.Equal(t, accessUnknown, meOf(t, s.p, sceneLetIn)["access"])

	passed, _ = s.gate(http.MethodGet, "/api/v1/channel/picture", sceneLocked, nil)
	require.False(t, passed, "nobody is let in on a guess")

	s.resetReads()
	passed, _ = s.gate(http.MethodGet, "/api/v1/me/profile", sceneLinked, nil)
	require.True(t, passed, "linked people never read the list")
	require.Zero(t, s.readsOf(kvLetIn))

	s.faults.healRead(kvLetIn)
	passed, _ = s.gate(http.MethodGet, "/api/v1/channel/picture", sceneLetIn, nil)
	require.True(t, passed)
}

// meOf is /me for somebody, decoded loosely.
func meOf(t *testing.T, p *Plugin, userID string) map[string]any {
	t.Helper()

	recorder := httptest.NewRecorder()
	p.handleMe(recorder, userID)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))

	var me map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &me))

	return me
}

// T7. Only a link of one's own unlocks: not a stream matched by username, not
// being a system admin, and not a forward entry the reverse entry does not
// agree with.
func TestOnlyALinkUnlocks(t *testing.T) {
	s := newAccessScene(t)

	level := func(userID string) access {
		t.Helper()

		got, _, err := s.p.accessOf(userID)
		require.NoError(t, err)

		return got
	}

	// Dan's stream is on the air and attributed to her by username.
	require.Equal(t, linkSourceUsername, s.p.getSnapshot().Streams[1].LinkSource)
	require.Equal(t, accessLocked, level(sceneLocked))
	require.Equal(t, accessLocked, level(sceneLockedAdmin), "being an admin is not a link")
	require.Equal(t, accessLinked, level(sceneLinked))

	// A forward entry whose reverse entry names somebody else.
	s.setKV(kvLinkByUserPrefix+sceneLocked, []byte("videokey-dave"))
	require.Equal(t, accessLocked, level(sceneLocked))
	require.Equal(t, accessLinked, level(sceneLinked), "and the person it does name is unaffected")

	// A forward entry with no reverse entry at all.
	s.setKV(kvLinkByUserPrefix+sceneLocked, []byte("videokey-dan"))
	require.Equal(t, accessLocked, level(sceneLocked))

	// Two links of one key at once: whoever wrote the reverse entry last.
	s.setKV(kvLinkByKeyPrefix+"videokey-dan", []byte(sceneLocked))
	require.Equal(t, accessLinked, level(sceneLocked))
	s.setKV(kvLinkByUserPrefix+sceneLetIn, []byte("videokey-dan"))
	require.Equal(t, accessLetIn, level(sceneLetIn), "the loser keeps what the let-in list gives her")
	s.setKV(kvLinkByKeyPrefix+"videokey-dan", []byte(sceneLetIn))
	require.Equal(t, accessLinked, level(sceneLetIn))
	require.Equal(t, accessLocked, level(sceneLocked))

	// And /me agrees.
	me := meOf(t, s.p, sceneLocked)
	require.Equal(t, "locked", me["access"])
	require.Empty(t, me["streamKey"])

	// Nor does her leftover forward entry let her make the server fetch
	// provider art for a title only people who can plan are shown, such as
	// their own What's on?: that spends the budget TMDB, Steam and IGDB
	// share between everybody.
	ref, ok := parseMediaRef("tmdb:movie:949")
	require.True(t, ok)
	require.False(t, s.p.refShownHere(ref))
	chosen := nowPlayingEntry{Category: "movie", Title: "Heat", Media: &scheduleMedia{Ref: ref.String()}, SetAt: nowMillis()}
	s.p.nowPlayingLock.Lock()
	s.p.nowPlayingLast = map[string]nowPlayingEntry{sceneLocked: chosen, sceneLinked: chosen}
	s.p.nowPlayingLock.Unlock()
	require.False(t, s.p.mayFetchPictureFor(sceneLocked, ref), "locked, with a leftover forward entry")
	require.True(t, s.p.mayFetchPictureFor(sceneLinked, ref), "linked")

	// (3.1.0) A Twitch connection is not a link, whatever Twitch said.
	s.setKV(kvLinkByUserPrefix+sceneLocked, nil)
	doc, err := json.Marshal(twitchLinks{V: twitchLinksVersion, Users: map[string]twitchLink{
		sceneLocked: {ID: "1001", Login: "dan_tv", Name: "Dan_TV", At: 1, Checked: 1},
	}})
	require.NoError(t, err)
	s.setKV(kvTwitchLinks, doc)
	require.Equal(t, accessLocked, level(sceneLocked), "connected to Twitch, and still locked")
}

// letInRequest posts to the Access card's route as somebody.
func (s *accessScene) letInPost(by, username string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"username": username})

	return s.serve(http.MethodPost, "/api/v1/admin/let-in", by, string(body), nil)
}

func (s *accessScene) letInList(t *testing.T) letInListResponse {
	t.Helper()

	response := s.serve(http.MethodGet, "/api/v1/admin/let-in", sceneLockedAdmin, "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

	var list letInListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))

	return list
}

func storedLetIn(t *testing.T, s *accessScene) letInDoc {
	t.Helper()

	var doc letInDoc
	require.NoError(t, json.Unmarshal(s.kv(kvLetIn), &doc))

	return doc
}

// T8. The let-in document and the Access card's routes.
func TestLetInDocument(t *testing.T) {
	s := newAccessScene(t)
	s.setKV(kvLetIn, nil)

	// Only a system admin, asked before anything is read, locked or not.
	requireRefused(t, s.letInPost(sceneLinked, "dan"), http.StatusForbidden, "forbidden", "not an admin")
	requireRefused(t, s.serve(http.MethodGet, "/api/v1/admin/let-in", sceneLetIn, "", nil), http.StatusForbidden, "forbidden", "not an admin")
	require.Empty(t, s.kv(kvLetIn))

	// Created on an empty store.
	require.Equal(t, letInListResponse{People: []letInRow{}, Max: maxLetIn}, s.letInList(t))
	response := s.letInPost(sceneLockedAdmin, " @Dan ")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"ok":true,"added":true,"linked":false}`, response.Body.String())
	doc := storedLetIn(t, s)
	require.Equal(t, 1, doc.Version)
	require.Equal(t, sceneLockedAdmin, doc.People[sceneLocked].By)
	require.Positive(t, doc.People[sceneLocked].At)

	// Again: nothing written.
	writes := s.faults.writesTo(kvLetIn)
	require.JSONEq(t, `{"ok":true,"added":false,"linked":false}`, s.letInPost(sceneLockedAdmin, "dan").Body.String())
	require.Equal(t, writes, s.faults.writesTo(kvLetIn))

	// Somebody linked, who it only matters for if they unlink.
	require.JSONEq(t, `{"ok":true,"added":true,"linked":true}`, s.letInPost(sceneLinkedAdmin, "dave").Body.String())

	// The list, named, sorted by username, with who let them in.
	s.users["uid-gone"] = &model.User{Id: "uid-gone", Username: "alice"}
	require.Equal(t, http.StatusOK, s.letInPost(sceneLockedAdmin, "alice").Code)
	delete(s.users, "uid-gone")
	s.users[sceneLocked].DeleteAt = 5
	s.p.clips().peopleLock.Lock()
	s.p.clips().people = map[string]clipFacts{}
	s.p.clips().peopleLock.Unlock()

	list := s.letInList(t)
	require.Equal(t, 3, list.Count)
	require.Equal(t, maxLetIn, list.Max)
	require.Empty(t, list.Problem)
	require.False(t, list.ReadOnly)
	require.Len(t, list.People, 3)
	require.Equal(t, "dan", list.People[0].Username)
	require.Equal(t, "Dan", list.People[0].Name)
	require.Equal(t, "deactivated", list.People[0].State)
	require.Equal(t, "boss", list.People[0].ByUsername)
	require.Equal(t, "dave", list.People[1].Username)
	require.Equal(t, "active", list.People[1].State)
	require.Equal(t, "bob", list.People[1].ByUsername)
	require.Equal(t, "uid-gone", list.People[2].UserID, "an account that has gone goes last")
	require.Equal(t, "gone", list.People[2].State)
	require.Empty(t, list.People[2].Username)
	s.users[sceneLocked].DeleteAt = 0

	// A lookup that fails shows the row by its id, never as gone.
	s.p.clips().peopleLock.Lock()
	s.p.clips().people = map[string]clipFacts{}
	s.p.clips().peopleLock.Unlock()
	s.failUser(sceneLinked, true)
	for _, row := range s.letInList(t).People {
		if row.UserID == sceneLinked {
			require.Equal(t, "active", row.State)
			require.Empty(t, row.Username)
		}
	}
	s.failUser(sceneLinked, false)

	// Who cannot be let in.
	requireRefused(t, s.letInPost(sceneLockedAdmin, "nobody-here"), http.StatusNotFound, "no_user", "no such user")
	requireRefused(t, s.letInPost(sceneLockedAdmin, "robot"), http.StatusBadRequest, "bot", "bot")
	requireRefused(t, s.letInPost(sceneLockedAdmin, "oldtimer"), http.StatusBadRequest, "deactivated", "deactivated")
	requireRefused(t, s.letInPost(sceneLockedAdmin, "  @ "), http.StatusBadRequest, "bad_request", "nobody named")
	requireRefused(t, s.serve(http.MethodPost, "/api/v1/admin/let-in", sceneLockedAdmin, "{nope", nil), http.StatusBadRequest, "bad_request", "not JSON")
	requireRefused(t, s.serve(http.MethodPost, "/api/v1/admin/let-in", sceneLockedAdmin, `{"username":"`+strings.Repeat("x", maxLetInRequestBytes)+`"}`, nil),
		http.StatusBadRequest, "bad_request", "too long")
	s.failUser("@erin", true)
	response = s.letInPost(sceneLockedAdmin, "erin")
	requireRefused(t, response, http.StatusServiceUnavailable, accessCodeStore, "user store down")
	require.Equal(t, letInAddUnsaved, codedError(t, response).Error)
	s.failUser("@erin", false)

	// Taken off by id, and taking off somebody absent is still fine.
	response = s.serve(http.MethodDelete, "/api/v1/admin/let-in?userId="+sceneLocked, sceneLockedAdmin, "", nil)
	require.JSONEq(t, `{"ok":true,"removed":true}`, response.Body.String())
	require.NotContains(t, storedLetIn(t, s).People, sceneLocked)
	writes = s.faults.writesTo(kvLetIn)
	response = s.serve(http.MethodDelete, "/api/v1/admin/let-in?userId="+sceneLocked, sceneLockedAdmin, "", nil)
	require.JSONEq(t, `{"ok":true,"removed":false}`, response.Body.String())
	require.Equal(t, writes, s.faults.writesTo(kvLetIn))
	requireRefused(t, s.serve(http.MethodDelete, "/api/v1/admin/let-in?userId=", sceneLockedAdmin, "", nil), http.StatusBadRequest, "bad_request", "nobody")
	require.Equal(t, http.StatusMethodNotAllowed, s.serve(http.MethodPut, "/api/v1/admin/let-in", sceneLockedAdmin, "", nil).Code)

	// Full at 500; somebody already on it does not count against it.
	full := letInDoc{Version: 1, People: map[string]letInEntry{}}
	for i := range maxLetIn {
		full.People[fmt.Sprintf("uid-%03d", i)] = letInEntry{At: 1, By: sceneLockedAdmin}
	}
	full.People[sceneLinked] = letInEntry{At: 1, By: sceneLockedAdmin}
	delete(full.People, "uid-000")
	raw, err := json.Marshal(full)
	require.NoError(t, err)
	s.setKV(kvLetIn, raw)
	requireRefused(t, s.letInPost(sceneLockedAdmin, "dan"), http.StatusConflict, "list_full", "full")
	require.JSONEq(t, `{"ok":true,"added":false,"linked":true}`, s.letInPost(sceneLockedAdmin, "dave").Body.String())
	s.setKV(kvLetIn, nil)

	// A compare-and-set that loses once, and then wins.
	lost := 0
	s.faults.setInterfere(func(key string, store map[string][]byte) {
		if key == kvLetIn && lost == 0 {
			lost++
			store[kvLetIn] = []byte(`{"version":1,"people":{"uid-erin":{"at":1,"by":"uid-boss"}}}`)
		}
	})
	require.JSONEq(t, `{"ok":true,"added":true,"linked":false}`, s.letInPost(sceneLockedAdmin, "dan").Body.String())
	require.Contains(t, storedLetIn(t, s).People, sceneLetIn, "the other node's change is kept")
	require.Contains(t, storedLetIn(t, s).People, sceneLocked)

	// And one that loses every time: busy, after eight tries.
	other := 0
	s.faults.setInterfere(func(key string, store map[string][]byte) {
		if key == kvLetIn {
			other++
			store[kvLetIn] = []byte(fmt.Sprintf(`{"version":1,"people":{"uid-x%d":{"at":1,"by":"uid-boss"}}}`, other))
		}
	})
	requireRefused(t, s.letInPost(sceneLockedAdmin, "chief"), http.StatusServiceUnavailable, "busy", "busy")
	require.Equal(t, casAttempts, other)
	s.faults.setInterfere(nil)

	// A store that will not save.
	s.setKV(kvLetIn, []byte(`{"version":1,"people":{"uid-dan":{"at":1,"by":"uid-boss"}}}`))
	s.faults.failWrite(kvLetIn)
	response = s.serve(http.MethodDelete, "/api/v1/admin/let-in?userId="+sceneLocked, sceneLockedAdmin, "", nil)
	requireRefused(t, response, http.StatusServiceUnavailable, accessCodeStore, "unsaved")
	require.Equal(t, letInDropUnsaved, codedError(t, response).Error)
	s.faults.healWrite(kvLetIn)

	// Garbage is set aside and reads as nobody, and says so to admins; a
	// change starts a fresh list.
	s.setKV(kvLetIn, []byte(`{"version":1,"people":`))
	s.advance(letInMaxAge)
	level, _, err := s.p.accessOf(sceneLocked)
	require.NoError(t, err)
	require.Equal(t, accessLocked, level)
	require.Empty(t, s.kv(kvLetIn), "removed, so nobody walks into it again")
	require.Equal(t, `{"version":1,"people":`, string(s.kv(kvQuarantinePrefix+kvLetIn)))
	list = s.letInList(t)
	require.Equal(t, letInSetAsideAlert, list.Problem)
	require.Equal(t, letInSetAsideAlert, meOf(t, s.p, sceneLockedAdmin)["letInAlert"])
	require.NotContains(t, meOf(t, s.p, sceneLinked), "letInAlert", "admins only")
	require.Equal(t, http.StatusOK, s.letInPost(sceneLockedAdmin, "dan").Code)
	require.Empty(t, s.letInList(t).Problem, "until people are added again")

	for _, garbage := range []string{`[]`, `{"version":0,"people":{}}`, `{"version":1}`, `{"version":1,"people":null}`} {
		_, state := decodeLetIn([]byte(garbage))
		require.Equal(t, letInGarbage, state, garbage)
	}

	// A newer version reads as nobody, is never written, and stays.
	newer := []byte(`{"version":2,"people":{"uid-dan":{"at":1,"by":"uid-boss"}},"groups":["x"]}`)
	s.setKV(kvLetIn, newer)
	s.advance(letInMaxAge)
	level, _, err = s.p.accessOf(sceneLocked)
	require.NoError(t, err)
	require.Equal(t, accessLocked, level)
	requireRefused(t, s.letInPost(sceneLockedAdmin, "erin"), http.StatusServiceUnavailable, "newer", "newer")
	requireRefused(t, s.serve(http.MethodDelete, "/api/v1/admin/let-in?userId="+sceneLocked, sceneLockedAdmin, "", nil), http.StatusServiceUnavailable, "newer", "newer")
	require.Equal(t, newer, s.kv(kvLetIn), "not deleted, not changed")
	list = s.letInList(t)
	require.True(t, list.ReadOnly)
	require.Equal(t, letInNewerAlert, list.Problem)
	require.Empty(t, list.People)
	require.Equal(t, letInNewerAlert, meOf(t, s.p, sceneLockedAdmin)["letInAlert"])
	s.setKV(kvLetIn, []byte(`{"version":1,"people":{"uid-erin":{"at":1,"by":"uid-boss"}}}`))

	// This node's copy is read again once it is ten seconds old, and not
	// before.
	s.advance(letInMaxAge)
	_, _ = s.p.letInHas(sceneLetIn)
	s.resetReads()
	for range 3 {
		in, err := s.p.letInHas(sceneLetIn)
		require.NoError(t, err)
		require.True(t, in)
	}
	s.advance(letInMaxAge - time.Millisecond)
	_, _ = s.p.letInHas(sceneLetIn)
	require.Zero(t, s.readsOf(kvLetIn))
	s.advance(time.Millisecond)
	_, _ = s.p.letInHas(sceneLetIn)
	require.Equal(t, 1, s.readsOf(kvLetIn))
}

// T8, continued. A refresh that began before a write on this node and
// finished after it is thrown away, so the writer keeps what it wrote; and a
// let-out reaches another node within its copy's age, and a store that fails
// there never lets anybody in on an older copy.
func TestLetInCopyOrderAndOtherNodes(t *testing.T) {
	s := newAccessScene(t)
	s.advance(letInMaxAge)

	// The copy is due. While its read is in flight, a write lands here; the
	// read still returns the list as it was before the write.
	c := s.p.letInList()
	written := false
	s.faults.setOnRead(func(key string) {
		if key != kvLetIn || written {
			return
		}
		written = true

		c.lock.Lock()
		c.install(map[string]bool{sceneLetIn: true, sceneLocked: true}, letInReadable, s.now)
		c.lock.Unlock()
	})
	in, err := s.p.letInHas(sceneLocked)
	require.NoError(t, err)
	require.True(t, written)
	require.True(t, in, "the write's list stands; the refresh that raced it is thrown away")
	s.faults.setOnRead(nil)

	// Two nodes over one store.
	other := newAccessSceneOn(t, s.store, s.faults)
	other.now = s.now
	in, err = other.p.letInHas(sceneLetIn)
	require.NoError(t, err)
	require.True(t, in)

	response := s.serve(http.MethodDelete, "/api/v1/admin/let-in?userId="+sceneLetIn, sceneLockedAdmin, "", nil)
	require.JSONEq(t, `{"ok":true,"removed":true}`, response.Body.String())
	level, _, err := s.p.accessOf(sceneLetIn)
	require.NoError(t, err)
	require.Equal(t, accessLocked, level, "at once on the node that changed it")

	level, _, err = other.p.accessOf(sceneLetIn)
	require.NoError(t, err)
	require.Equal(t, accessLetIn, level, "the other node's copy is still young")

	other.advance(letInMaxAge)
	s.faults.failRead(kvLetIn)
	_, _, err = other.p.accessOf(sceneLetIn)
	require.Error(t, err, "a copy that is due and cannot be read is not used")
	passed, recorder := other.gate(http.MethodGet, "/api/v1/channel/picture", sceneLetIn, nil)
	require.False(t, passed)
	requireRefused(t, recorder, http.StatusServiceUnavailable, accessCodeStore, "never let in on the old copy")
	s.faults.healRead(kvLetIn)

	level, _, err = other.p.accessOf(sceneLetIn)
	require.NoError(t, err)
	require.Equal(t, accessLocked, level)
}

// fakeBroadcastBox answers GET /api/whip/profile for the tokens it knows, as
// Broadcast Box does: the profile for a token it can resolve, 204 for one it
// cannot. It checks the bearer is base64 of the token, and counts requests.
type fakeBroadcastBox struct {
	server *httptest.Server

	lock     sync.Mutex
	requests int
	answer   func(token string) (int, string)
}

func newFakeBroadcastBox(t *testing.T, profiles map[string]string) *fakeBroadcastBox {
	t.Helper()

	fake := &fakeBroadcastBox{}
	fake.answer = func(token string) (int, string) {
		key, known := profiles[token]
		if !known {
			return http.StatusNoContent, ""
		}

		return http.StatusOK, `{"streamKey":"` + key + `","motd":"","isPublic":true}`
	}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.lock.Lock()
		fake.requests++
		answer := fake.answer
		fake.lock.Unlock()

		bearer, found := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		decoded, err := base64.StdEncoding.DecodeString(bearer)
		if r.URL.Path != "/api/whip/profile" || r.Method != http.MethodGet || !found || err != nil {
			http.Error(w, "no", http.StatusBadRequest)
			return
		}

		status, body := answer(string(decoded))
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(fake.server.Close)

	return fake
}

func (f *fakeBroadcastBox) count() int {
	f.lock.Lock()
	defer f.lock.Unlock()

	return f.requests
}

func (f *fakeBroadcastBox) answerWith(answer func(token string) (int, string)) {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.answer = answer
}

// pointedAt points the scene's Broadcast Box at a fake.
func (s *accessScene) pointedAt(fake *fakeBroadcastBox) {
	s.p.httpClient = fake.server.Client()
	cfg := s.p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = fake.server.URL
	s.p.setConfiguration(cfg)
}

func (s *accessScene) link(userID, token string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"token": token})

	return s.serve(http.MethodPost, "/api/v1/me/link", userID, string(body), nil)
}

func (s *accessScene) unlink(userID string) *httptest.ResponseRecorder {
	return s.serve(http.MethodDelete, "/api/v1/me/link", userID, "", nil)
}

const (
	dansToken  = "11111111-1111-4111-8111-111111111111"
	davesToken = "22222222-2222-4222-8222-222222222222"
)

// T9. Whoever's access changes is told, with an event to them alone that
// carries nothing; and setLink fails closed, telling nobody.
func TestAccessChangedTellsOnePerson(t *testing.T) {
	s := newAccessScene(t)
	fake := newFakeBroadcastBox(t, map[string]string{dansToken: "videokey-dan", davesToken: "videokey-dave"})
	s.pointedAt(fake)

	// A username match tells nobody anything, and unlocks nothing.
	streams := s.p.decorate([]bbStream{rawLive("dan")}, s.p.getConfiguration())
	require.Len(t, streams, 1)
	require.Equal(t, sceneLocked, streams[0].UserID)
	require.Equal(t, linkSourceUsername, streams[0].LinkSource)
	require.Empty(t, s.accessEvents(t))

	require.Equal(t, http.StatusOK, s.link(sceneLocked, dansToken).Code)
	require.Equal(t, []string{sceneLocked}, s.accessEvents(t), "link")

	require.Equal(t, http.StatusOK, s.unlink(sceneLocked).Code)
	require.Equal(t, []string{sceneLocked, sceneLocked}, s.accessEvents(t), "unlink")

	// Dan proves Dave's key: Dave is told, as well as Dan.
	response := s.link(sceneLocked, davesToken)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, []string{sceneLocked, sceneLocked, sceneLinked, sceneLocked}, s.accessEvents(t), "takeover")
	level, _, err := s.p.accessOf(sceneLinked)
	require.NoError(t, err)
	require.Equal(t, accessLocked, level)

	require.Equal(t, http.StatusOK, s.letInPost(sceneLockedAdmin, "dave").Code)
	require.Equal(t, sceneLinked, s.accessEvents(t)[4], "let in")
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/admin/let-in?userId="+sceneLinked, sceneLockedAdmin, "", nil).Code)
	require.Equal(t, sceneLinked, s.accessEvents(t)[5], "let out")
	require.Equal(t, http.StatusOK, s.serve(http.MethodPost, "/api/v1/admin/unlink", sceneLockedAdmin, `{"username":"dan"}`, nil).Code)
	require.Equal(t, sceneLocked, s.accessEvents(t)[6], "admin unlink")
	require.Len(t, s.accessEvents(t), 7)

	// Fail closed: a read inside setLink that fails stops it before any
	// write, and nobody is told anything.
	linkForTest(s.store, sceneLinked, "videokey-dave")
	for _, key := range []string{kvLinkByKeyPrefix + "videokey-dave", kvLinkByUserPrefix + sceneLocked} {
		before := map[string]string{}
		for _, k := range []string{kvLinkByUserPrefix + sceneLinked, kvLinkByKeyPrefix + "videokey-dave", kvLinkByUserPrefix + sceneLocked, kvLinkTokenPrefix + sceneLocked} {
			before[k] = string(s.kv(k))
		}
		told := len(s.accessEvents(t))

		s.faults.failRead(key)
		response := s.link(sceneLocked, davesToken)
		s.faults.healRead(key)

		requireRefused(t, response, http.StatusInternalServerError, accessCodeStore, key)
		require.Equal(t, linkNotSaved, codedError(t, response).Error)
		for k, value := range before {
			require.Equal(t, value, string(s.kv(k)), "%s: %s was written", key, k)
		}
		require.Len(t, s.accessEvents(t), told, key)
		level, _, err := s.p.accessOf(sceneLinked)
		require.NoError(t, err)
		require.Equal(t, accessLinked, level, "the previous owner is unchanged")
	}
}

// T10. Linking against Broadcast Box: every answer it can give has its code,
// and the token never reaches a log line.
func TestLinkAgainstBroadcastBox(t *testing.T) {
	s := newAccessScene(t)
	fake := newFakeBroadcastBox(t, map[string]string{dansToken: "videokey-dan"})
	s.pointedAt(fake)

	response := s.link(sceneLocked, dansToken)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	// The answer says what the caller's access is now, so whoever unlocked
	// has no need to ask.
	require.JSONEq(t, `{"streamKey":"videokey-dan","access":"linked","configured":true}`, response.Body.String())
	level, key, err := s.p.accessOf(sceneLocked)
	require.NoError(t, err)
	require.Equal(t, accessLinked, level)
	require.Equal(t, "videokey-dan", key)

	// And so does unlinking, which is a DELETE: locked again, for somebody
	// nobody let in. Posting no token is not a way to unlink, only a mistake.
	require.JSONEq(t, `{"streamKey":"","access":"locked","configured":true}`, s.unlink(sceneLocked).Body.String())
	requireRefused(t, s.link(sceneLocked, ""), http.StatusBadRequest, "bad_request", "no token")

	// With the prefix OBS's documentation shows, and spaces.
	require.Equal(t, http.StatusOK, s.link(sceneLocked, "  Bearer "+dansToken+" ").Code)

	requireRefused(t, s.link(sceneLetIn, "33333333-3333-4333-8333-333333333333"), http.StatusForbidden, "not_accepted", "204")

	for _, status := range []int{http.StatusUnauthorized, http.StatusInternalServerError} {
		fake.answerWith(func(string) (int, string) { return status, "" })
		requireRefused(t, s.link(sceneLetIn, dansToken), http.StatusBadGateway, "unreachable", fmt.Sprint(status))
	}

	fake.answerWith(func(string) (int, string) { return http.StatusOK, `{"streamKey":"../../etc/passwd"}` })
	requireRefused(t, s.link(sceneLetIn, dansToken), http.StatusBadGateway, "bad_name", "bad name")

	requests := fake.count()
	requireRefused(t, s.link(sceneLinked, strings.Repeat("x", 257)), http.StatusBadRequest, "too_long", "257")
	require.Equal(t, requests, fake.count(), "never sent")
	requireRefused(t, s.serve(http.MethodPost, "/api/v1/me/link", sceneLinked, "{nope", nil), http.StatusBadRequest, "bad_request", "not JSON")

	cfg := s.p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = ""
	s.p.setConfiguration(cfg)
	requireRefused(t, s.link(sceneLinkedAdmin, dansToken), http.StatusServiceUnavailable, "not_configured", "not set up")
	s.pointedAt(fake)

	// Five tries in a burst, then 429, and the sixth never reaches Broadcast
	// Box.
	fake.answerWith(func(string) (int, string) { return http.StatusNoContent, "" })
	requests = fake.count()
	for i := range 5 {
		require.Equal(t, http.StatusForbidden, s.link(sceneLockedAdmin, dansToken).Code, i)
	}
	requireRefused(t, s.link(sceneLockedAdmin, dansToken), http.StatusTooManyRequests, "rate_limited", "sixth")
	require.Equal(t, requests+5, fake.count())

	// Taking a link back is never metered, like every other takeback: after
	// all those tries, and as often as asked.
	for i := range 8 {
		require.Equal(t, http.StatusOK, s.unlink(sceneLockedAdmin).Code, i)
	}

	// The token is in no log line, in any form.
	for _, call := range s.api.Calls {
		if !strings.HasPrefix(call.Method, "Log") {
			continue
		}
		for _, argument := range call.Arguments {
			text := fmt.Sprint(argument)
			require.NotContains(t, text, dansToken, "%s %v", call.Method, call.Arguments)
			require.NotContains(t, text, base64.StdEncoding.EncodeToString([]byte(dansToken)))
		}
	}
}

// Taking a link back is behind no meter, so for somebody with nothing linked
// it must do nothing at all: no store written, no poll of Broadcast Box asked
// for, nobody told. Otherwise it is the one unmetered request anybody signed
// in could repeat to make the server work.
func TestAnUnlinkWithNothingLinkedCostsNothing(t *testing.T) {
	s := newAccessScene(t)
	s.p.refreshCh = make(chan struct{}, 1)

	count := func(method string) int {
		n := 0
		for _, call := range s.api.Calls {
			if call.Method == method {
				n++
			}
		}

		return n
	}
	deletes, sets, events := count("KVDelete"), count("KVSet"), len(s.accessEvents(t))

	for i := range 20 {
		response := s.unlink(sceneLocked)
		require.Equal(t, http.StatusOK, response.Code, i)
		require.JSONEq(t, `{"streamKey":"","access":"locked","configured":true}`, response.Body.String())
	}
	require.JSONEq(t, `{"streamKey":"","access":"letIn","configured":true}`, s.unlink(sceneLetIn).Body.String())

	require.Equal(t, deletes, count("KVDelete"), "nothing deleted")
	require.Equal(t, sets, count("KVSet"), "nothing written")
	require.Len(t, s.accessEvents(t), events, "nobody told")
	require.Empty(t, s.p.refreshCh, "no poll asked for")

	// Somebody who is linked is unlinked as ever: once, and then there is
	// nothing left to do.
	require.JSONEq(t, `{"streamKey":"","access":"locked","configured":true}`, s.unlink(sceneLinked).Body.String())
	require.Greater(t, count("KVDelete"), deletes)
	require.Len(t, s.accessEvents(t), events+1)
	require.Len(t, s.p.refreshCh, 1)

	after := count("KVDelete")
	require.Equal(t, http.StatusOK, s.unlink(sceneLinked).Code)
	require.Equal(t, after, count("KVDelete"))
	require.Len(t, s.accessEvents(t), events+1)
}

// The caller's stream profile is read from Broadcast Box afresh on every call,
// and nothing meters it. A picture in a post is loaded by every reader's
// browser with their session, so the read is answered only for a request the
// panel made itself, which says so with the header a picture cannot carry.
func TestAPictureInAPostCannotHaveAStreamProfileRead(t *testing.T) {
	s := newAccessScene(t)
	fake := newFakeBroadcastBox(t, map[string]string{dansToken: "videokey-dan"})
	s.pointedAt(fake)
	require.Equal(t, http.StatusOK, s.link(sceneLocked, dansToken).Code)
	asked := fake.count()

	for _, header := range []map[string]string{nil, {"Sec-Fetch-Dest": "image"}, {"X-Requested-With": "fetch"}} {
		response := s.serve(http.MethodGet, "/api/v1/me/profile", sceneLocked, "", header)
		require.Equal(t, http.StatusForbidden, response.Code, "%v", header)
	}
	require.Equal(t, asked, fake.count(), "Broadcast Box was not asked")

	response := s.serve(http.MethodGet, "/api/v1/me/profile", sceneLocked, "", map[string]string{"X-Requested-With": "XMLHttpRequest"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, asked+1, fake.count())
}

// T12. Best Clipper learns from the link only: somebody let in counts for
// nothing, so votes they cast while they were linked stop counting.
func TestBestClipperIgnoresLetIn(t *testing.T) {
	w, _ := startedClipsWorld(t, "uid-bob", "uid-dan")
	clipsAdmins(w.api)
	publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), votesAt(clipsNow.Add(-time.Minute), "uid-dan")), true)

	upvotes := func(userID string) int {
		response := w.call(t, userID, clipCall{method: http.MethodGet, target: clipsPath + "/clip?id=" + clipA})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())

		var view clipView
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &view))

		return view.Upvotes
	}

	w.p.lookUpClipPeople([]string{"uid-dan"}, clipsNow, true)
	require.Equal(t, 1, upvotes("uid-dan"), "Dan's vote counts while she is linked")

	w.unlink("uid-dan")
	w.p.clips().background.Wait()
	_, err := w.p.letIn("uid-dan", "uid-boss", clipsNow)
	require.NoError(t, err)

	require.Equal(t, 0, upvotes("uid-dan"), "the gate's fresh read says she is not linked")
	require.False(t, w.counts(t)("uid-dan"))
	facts := w.p.clips().factsOf("uid-dan")
	require.True(t, facts.linkKnown)
	require.False(t, facts.linked)
}

// T13. What /me says in each state.
func TestMeAccess(t *testing.T) {
	s := newAccessScene(t)
	linkForTest(s.store, sceneLetIn, "videokey-erin-was")
	s.setKV(kvLinkByKeyPrefix+"videokey-erin-was", nil)
	_, err := s.p.letIn(sceneLinked, sceneLockedAdmin, time.Now())
	require.NoError(t, err)

	want := map[string]string{
		sceneLocked: "locked", sceneLetIn: "letIn", sceneLinked: "linked",
		sceneLockedAdmin: "locked", sceneLetInAdmin: "letIn", sceneLinkedAdmin: "linked",
	}
	for _, caller := range sceneCallers {
		me := meOf(t, s.p, caller)
		linked := want[caller] == "linked"
		admin := caller == sceneLockedAdmin || caller == sceneLetInAdmin || caller == sceneLinkedAdmin

		require.Equal(t, want[caller], me["access"], caller)
		require.NotContains(t, me, "publicSiteUrl", caller)
		require.NotContains(t, me, "accessContact", "Who to ask went with the lock's line that said it (3.1.0): %s", caller)
		require.Equal(t, "never", me["openPanel"], "the auto-open setting goes to everybody, locked or not: %s", caller)
		require.Contains(t, me, "bannerText", caller)
		require.Equal(t, true, me["configured"], caller)
		require.Equal(t, admin, me["isAdmin"] == true, caller)

		if linked {
			require.NotEmpty(t, me["streamKey"], caller)
			require.Contains(t, me, "media", caller)
			require.Equal(t, true, me["popOut"], caller)
			require.Contains(t, me, "alsoLetIn", caller)
		} else {
			require.Empty(t, me["streamKey"], caller)
			require.NotContains(t, me, "media", caller)
			require.NotContains(t, me, "popOut", caller)
			require.NotContains(t, me, "alsoLetIn", caller)
			require.NotContains(t, me, "clipsStreamer", caller)
			require.Equal(t, false, me["linkedLive"], caller)
		}
	}

	require.Equal(t, true, meOf(t, s.p, sceneLinked)["alsoLetIn"])
	require.Equal(t, false, meOf(t, s.p, sceneLinkedAdmin)["alsoLetIn"])

	// No public watch address, no Pop out.
	off := s.p.getConfiguration().Clone()
	off.PublicSiteURL = ""
	s.p.setConfiguration(off)
	require.NotContains(t, meOf(t, s.p, sceneLinked), "popOut")

	// A locked admin is told what an admin needs to know.
	s.setKV(kvLetIn, []byte(`{"version":7,"people":{}}`))
	s.advance(letInMaxAge)
	me := meOf(t, s.p, sceneLockedAdmin)
	require.Equal(t, "locked", me["access"])
	require.Equal(t, letInNewerAlert, me["letInAlert"])
	require.NotContains(t, meOf(t, s.p, sceneLocked), "letInAlert")
}

// T14. Pop out: a redirect for linked people, carrying the key only in the
// address it sends them to, never in the snapshot.
func TestPopOut(t *testing.T) {
	s := newAccessScene(t)
	dave := s.p.streamHandle("videokey-dave")
	target := "/api/v1/watch/popout?handle=" + dave

	requireRefused(t, s.serve(http.MethodGet, target, sceneLocked, "", nil), http.StatusForbidden, accessCodeLocked, "locked")
	requireRefused(t, s.serve(http.MethodGet, target, sceneLetIn, "", nil), http.StatusForbidden, accessCodeWatchOnly, "let in")
	requireRefused(t, s.serve(http.MethodGet, target, sceneLetInAdmin, "", nil), http.StatusForbidden, accessCodeWatchOnly, "let-in admin")

	response := s.serve(http.MethodGet, target, sceneLinked, "", nil)
	require.Equal(t, http.StatusFound, response.Code)
	require.Equal(t, "https://tv.example.com/videokey-dave", response.Header().Get("Location"))
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Equal(t, "no-referrer", response.Header().Get("Referrer-Policy"))
	require.Empty(t, response.Body.String())

	for about, path := range map[string]string{
		"unknown handle": "/api/v1/watch/popout?handle=0123456789abcdef0123456789abcdef",
		"no handle":      "/api/v1/watch/popout",
		"a 24/7 channel": "/api/v1/watch/popout?handle=" + s.p.streamHandle("videokey-watts"),
	} {
		response := s.serve(http.MethodGet, path, sceneLinked, "", nil)
		require.Equal(t, http.StatusNotFound, response.Code, about)
		require.Equal(t, noWatchPage, codedError(t, response).Error, about)
	}
	require.Equal(t, http.StatusMethodNotAllowed, s.serve(http.MethodPost, target, sceneLinked, "", nil).Code)

	blank := s.p.getConfiguration().Clone()
	blank.PublicSiteURL = ""
	s.p.setConfiguration(blank)
	require.Equal(t, http.StatusNotFound, s.serve(http.MethodGet, target, sceneLinked, "", nil).Code)

	encoded, err := json.Marshal(s.p.getSnapshot())
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "watchUrl")
	require.NotContains(t, string(encoded), "videokey-dave")

	streams := s.serve(http.MethodGet, "/api/v1/streams", sceneLocked, "", nil)
	require.Equal(t, http.StatusOK, streams.Code, "the list stays open: the badges outside the panel need it")
	require.NotContains(t, streams.Body.String(), "watchUrl")
}

// T15. An admin's unlink: the link and What's on? go, what this node
// remembers goes, and they are told; it says whether they were linked and
// whether they can still watch.
func TestAdminUnlink(t *testing.T) {
	s := newAccessScene(t)
	_, err := s.p.letIn(sceneLinkedAdmin, sceneLockedAdmin, time.Now())
	require.NoError(t, err)
	s.setKV(kvNowPlaying, []byte(`{"v":1,"entries":{"uid-dave":{"category":"game","title":"Arma 3","setAt":1}}}`))

	// Remembered as linked, for ten seconds, by the routes that remember.
	level, _, err := s.p.accessSeenOf(sceneLinked)
	require.NoError(t, err)
	require.Equal(t, accessLinked, level)

	unlink := func(by, username string) *httptest.ResponseRecorder {
		return s.serve(http.MethodPost, "/api/v1/admin/unlink", by, `{"username":"`+username+`"}`, nil)
	}

	requireRefused(t, unlink(sceneLinked, "bob"), http.StatusForbidden, "forbidden", "not an admin")

	response := unlink(sceneLockedAdmin, "@Dave")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"ok":true,"wasLinked":true,"letIn":false}`, response.Body.String())
	require.Empty(t, s.kv(kvLinkByUserPrefix+sceneLinked))
	require.Empty(t, s.kv(kvLinkByKeyPrefix+"videokey-dave"))
	require.NotContains(t, string(s.kv(kvNowPlaying)), "uid-dave")
	require.Contains(t, s.accessEvents(t), sceneLinked)

	level, _, err = s.p.accessSeenOf(sceneLinked)
	require.NoError(t, err)
	require.Equal(t, accessLocked, level, "not remembered as linked any more")

	require.JSONEq(t, `{"ok":true,"wasLinked":false,"letIn":false}`, unlink(sceneLockedAdmin, "dave").Body.String())
	require.JSONEq(t, `{"ok":true,"wasLinked":true,"letIn":true}`, unlink(sceneLockedAdmin, "bob").Body.String())
	level, _, err = s.p.accessOf(sceneLinkedAdmin)
	require.NoError(t, err)
	require.Equal(t, accessLetIn, level, "still let in to watch")

	requireRefused(t, unlink(sceneLockedAdmin, "nobody-here"), http.StatusNotFound, "no_user", "no such user")
	requireRefused(t, unlink(sceneLockedAdmin, ""), http.StatusBadRequest, "bad_request", "nobody")
	require.Equal(t, http.StatusMethodNotAllowed, s.serve(http.MethodGet, "/api/v1/admin/unlink", sceneLockedAdmin, "", nil).Code)

	s.faults.failRead(kvLinkByUserPrefix + sceneLetIn)
	requireRefused(t, unlink(sceneLockedAdmin, "erin"), http.StatusServiceUnavailable, accessCodeStore, "store")
	s.faults.healRead(kvLinkByUserPrefix + sceneLetIn)

	// Logged with ids only, and only when something changed.
	require.Len(t, logged(s.api, "LogInfo", "A system admin unlinked somebody's stream"), 2)
}

// A let-in row's name is the display name, or "" when Mattermost's fallback
// would only repeat the username: the card shows the name and then
// "@username", so "chief @chief" would say the same thing twice (§3.3).
func TestLetInRowNameIsNeverJustTheUsername(t *testing.T) {
	s := newAccessScene(t)
	s.users[sceneLetIn].Nickname = "Erin the Cat"

	named := map[string]letInRow{}
	for _, row := range s.letInList(t).People {
		named[row.UserID] = row
	}

	require.Equal(t, "chief", named[sceneLetInAdmin].Username)
	require.Empty(t, named[sceneLetInAdmin].Name, "no nickname and no full name: no name")
	require.Equal(t, "erin", named[sceneLetIn].Username)
	require.Equal(t, "Erin the Cat", named[sceneLetIn].Name, "the nickname, as everywhere else")
}

// T16. Somebody let in watches, and is never counted on Top Snoopers.
func TestLetInNotCountedAsSnooper(t *testing.T) {
	s := newAccessScene(t)
	origin, issued := fakeWHEPOrigin(t)
	defer origin.Close()
	s.p.httpClient = origin.Client()
	cfg := s.p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = origin.URL
	cfg.ShowWatchers = false
	cfg.TopWatchers = true
	s.p.setConfiguration(cfg)

	dave := s.p.streamHandle("videokey-dave")
	whep := func(userID string) int {
		return s.serve(http.MethodPost, "/api/v1/whep", userID, viewerOffer, map[string]string{"X-Stream-Handle": dave}).Code
	}

	require.Equal(t, http.StatusOK, whep(sceneLetIn))
	require.Equal(t, http.StatusOK, whep(sceneLetInAdmin))
	require.Empty(t, rememberedSessions(s.p.watchers()), "nobody let in is remembered for the board")

	require.Equal(t, http.StatusOK, whep(sceneLinkedAdmin))
	require.Equal(t, map[string]string{issued()[2]: sceneLinkedAdmin}, rememberedSessions(s.p.watchers()))

	requireRefused(t, s.serve(http.MethodPost, "/api/v1/whep", sceneLocked, viewerOffer, map[string]string{"X-Stream-Handle": dave}),
		http.StatusForbidden, accessCodeLocked, "locked")
	require.Len(t, issued(), 3, "a locked caller never reaches Broadcast Box")

	// A let-in person with a forward link entry left behind (a race, a
	// failed cleanup, or setLink before 3.0.0) whose reverse entry names
	// somebody else. The gate lets her in to watch, and the board must agree
	// with the gate: linked means both entries.
	s.setKV(kvLinkByUserPrefix+sceneLetIn, []byte("videokey-bob"))
	level, _, err := s.p.accessOf(sceneLetIn)
	require.NoError(t, err)
	require.Equal(t, accessLetIn, level)

	require.Equal(t, http.StatusOK, whep(sceneLetIn))
	require.Equal(t, map[string]string{issued()[2]: sceneLinkedAdmin}, rememberedSessions(s.p.watchers()),
		"a leftover forward entry is not a link: her session is not remembered for the board")
	require.False(t, s.p.linkedNow()(sceneLetIn), "and no board names her")
	require.True(t, s.p.linkedNow()(sceneLinkedAdmin))

	// With faces on her session is remembered as a face, as everybody's is,
	// and a watch pass still pays her nothing.
	faces := s.p.getConfiguration().Clone()
	faces.ShowWatchers = true
	s.p.setConfiguration(faces)
	require.Equal(t, http.StatusOK, whep(sceneLetIn))
	session := issued()[4]
	require.Equal(t, sceneLetIn, rememberedSessions(s.p.watchers())[session])
	raw := []bbStream{rawWatched("videokey-dave", issued()[2], session)}
	paid := s.p.watchingNow(raw, s.p.getSnapshot().Streams, faces, s.now).snoopers
	require.NotContains(t, paid, sceneLetIn, "no pass pays a let-in person")
	require.Contains(t, paid, sceneLinkedAdmin, "while the linked admin beside her is paid")
}

// A post that is nothing but a bearer token is refused, on the server too.
func TestABearerTokenIsNotPosted(t *testing.T) {
	for message, shaped := range map[string]bool{
		dansToken:                              true,
		"  " + strings.ToUpper(dansToken):      true,
		"Bearer " + dansToken:                  true,
		"bearer   " + dansToken + "\n":         true,
		"my token is " + dansToken:             false,
		dansToken + " ok":                      false,
		"Bearer":                               false,
		"":                                     false,
		"11111111-1111-4111-8111-11111111111g": false,
		"11111111_1111-4111-8111-111111111111": false,
	} {
		_, got := looksLikeBearerToken(message)
		require.Equal(t, shaped, got, "%q", message)
	}

	s := newAccessScene(t)
	s.setKV(kvLinkTokenPrefix+sceneLinked, []byte(davesToken))
	post := func(userID, message string, props model.StringInterface) string {
		p := &model.Post{UserId: userID, ChannelId: "channel", Message: message}
		p.SetProps(props)
		_, refusal := s.p.MessageWillBePosted(nil, p)

		return refusal
	}

	require.Equal(t, bearerTokenPostRefused(productName), post(sceneLocked, dansToken, nil))
	require.Equal(t, bearerTokenPostRefused(productName), post(sceneLetIn, "Bearer "+dansToken, nil))
	require.Equal(t, bearerTokenPostRefused(productName), post(sceneLockedAdmin, dansToken, nil))
	require.Empty(t, post(sceneLocked, "here is "+dansToken, nil))
	require.Empty(t, post(sceneLocked, "hello", nil))

	// Linked: only their own token, since the server cannot see a bar open.
	require.Empty(t, post(sceneLinked, dansToken, nil))
	require.Equal(t, bearerTokenPostRefused(productName), post(sceneLinked, " "+strings.ToUpper(davesToken), nil))

	// Bots, webhooks and system messages hold no token.
	require.Empty(t, post(sceneLocked, dansToken, model.StringInterface{model.PostPropsFromBot: "true"}))
	require.Empty(t, post(sceneLocked, dansToken, model.StringInterface{model.PostPropsFromWebhook: "true"}))
	system := &model.Post{UserId: sceneLocked, Type: model.PostTypeJoinChannel, Message: dansToken}
	_, refusal := s.p.MessageWillBePosted(nil, system)
	require.Empty(t, refusal)

	// Kept back when access cannot be read.
	s.faults.failRead(kvLinkByUserPrefix + sceneLinked)
	require.Equal(t, bearerTokenPostRefused(productName), post(sceneLinked, dansToken, nil))
	s.faults.healRead(kvLinkByUserPrefix + sceneLinked)

	// An edit that makes a message a token is refused; one that leaves the
	// words alone is not.
	old := &model.Post{Id: "p1", UserId: sceneLocked, Message: "hello"}
	edited := &model.Post{Id: "p1", UserId: sceneLocked, Message: dansToken}
	updated, refusal := s.p.MessageWillBeUpdated(nil, edited, old)
	require.Nil(t, updated)
	require.Equal(t, bearerTokenPostRefused(productName), refusal)
	pinned := &model.Post{Id: "p2", UserId: sceneLocked, Message: dansToken, IsPinned: true}
	updated, refusal = s.p.MessageWillBeUpdated(nil, pinned, &model.Post{Id: "p2", UserId: sceneLocked, Message: dansToken})
	require.NotNil(t, updated)
	require.Empty(t, refusal)

	// Not set up: there is no bar to paste it into.
	cfg := s.p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = ""
	s.p.setConfiguration(cfg)
	require.Empty(t, post(sceneLocked, dansToken, nil))

	// Nothing about the message was logged.
	for _, call := range s.api.Calls {
		if strings.HasPrefix(call.Method, "Log") {
			require.NotContains(t, fmt.Sprint(call.Arguments...), dansToken)
		}
	}
}

// The settings backup never carries the let-in list, and says so.
func TestABackupLeavesTheLetInListOut(t *testing.T) {
	found := false
	for _, line := range settingsNotIncluded {
		if strings.Contains(line, "let in") {
			found = true
		}
	}
	require.True(t, found)
	require.NotContains(t, settingKeys(), "accesslist", "a component with no value")
	require.NotContains(t, settingKeys(), "accesscontact", "Who to ask is gone (3.1.0)")
}
