package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Looking titles up is the one feature that reaches outside the building, so
// these tests are mostly about where it is and is not allowed to reach. None of
// them leaves the machine: TMDB, Steam, IGDB, Twitch's token service and their
// image hosts are all one local TLS server, and a second one stands for
// "somewhere else" — any request that arrives there is a request the relay
// should have refused.

// Enough of a JPEG for the sniffer, which goes by the signature.
var jpegBytes = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00" + strings.Repeat("x", 64))

// A TMDB v4 read access token has the shape of a JWT; a v3 API key is 32 hex.
const (
	testTMDBToken  = "eyJhbGciOiJIUzI1NiJ9.eyJhdWQiOiJ0ZXN0In0.c2lnbmF0dXJlLW5vdC1yZWFs"
	testTMDBAPIKey = "0123456789abcdef0123456789abcdef"
)

type seenRequest struct {
	method      string
	path        string
	query       url.Values
	auth        string
	agent       string
	clientID    string // the Client-ID header, as IGDB wants it
	contentType string
	body        string
}

type mediaFake struct {
	server    *httptest.Server
	elsewhere *httptest.Server

	lock          sync.Mutex
	routes        map[string]http.HandlerFunc
	seen          []seenRequest
	elsewhereHits int
}

// newMediaFake points every provider address, the image host list and the
// shared client at a local fake, and puts them all back afterwards: Twitch's
// token service, Helix and Twitch's preview host included.
func newMediaFake(t *testing.T) *mediaFake {
	t.Helper()

	fake := &mediaFake{routes: map[string]http.HandlerFunc{}}

	fake.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read in full and put back, so the record has it and the route can
		// still read it.
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))

		fake.lock.Lock()
		fake.seen = append(fake.seen, seenRequest{
			method:      r.Method,
			path:        r.URL.Path,
			query:       r.URL.Query(),
			auth:        r.Header.Get("Authorization"),
			agent:       r.Header.Get("User-Agent"),
			clientID:    r.Header.Get("Client-ID"),
			contentType: r.Header.Get("Content-Type"),
			body:        string(body),
		})
		route := fake.routes[r.URL.Path]
		fake.lock.Unlock()

		if route == nil {
			http.NotFound(w, r)
			return
		}
		route(w, r)
	}))
	// Quiet: a test that asks Steam and IGDB at once can leave a connection
	// still being set up when the server closes, which is not worth a line.
	fake.server.Config.ErrorLog = log.New(io.Discard, "", 0)
	fake.server.StartTLS()

	fake.elsewhere = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fake.lock.Lock()
		fake.elsewhereHits++
		fake.lock.Unlock()

		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(jpegBytes)
	}))

	savedAPI, savedImages, savedStore, savedLibrary := tmdbAPIBase, tmdbImageBase, steamStoreBase, steamLibraryBase
	savedIGDB, savedIGDBImages, savedTwitch := igdbAPIBase, igdbImageBase, twitchIDBase
	savedHelix, savedTwitchImages := twitchAPIBase, twitchImageHost
	savedHosts, savedClient := mediaImageHosts, mediaHTTPClient

	tmdbAPIBase = fake.server.URL + "/3"
	tmdbImageBase = fake.server.URL + "/t/p"
	steamStoreBase = fake.server.URL
	steamLibraryBase = fake.server.URL + "/steamcdn/apps"
	igdbAPIBase = fake.server.URL + "/v4"
	igdbImageBase = fake.server.URL + "/igdb/image/upload"
	twitchIDBase = fake.server.URL
	twitchAPIBase = fake.server.URL
	twitchImageHost = fake.host()
	mediaImageHosts = map[string]bool{fake.host(): true}

	// The fake's own transport trusts its certificate — and, because every
	// httptest TLS server shares one, the elsewhere server's too. So nothing
	// but the plugin's own checks stands between a refused address and a
	// request that would succeed.
	mediaHTTPClient = newMediaClient(fake.server.Client().Transport)

	t.Cleanup(func() {
		tmdbAPIBase, tmdbImageBase, steamStoreBase, steamLibraryBase = savedAPI, savedImages, savedStore, savedLibrary
		igdbAPIBase, igdbImageBase, twitchIDBase = savedIGDB, savedIGDBImages, savedTwitch
		twitchAPIBase, twitchImageHost = savedHelix, savedTwitchImages
		mediaImageHosts, mediaHTTPClient = savedHosts, savedClient
		fake.server.Close()
		fake.elsewhere.Close()
	})

	return fake
}

func (f *mediaFake) host() string {
	return strings.TrimPrefix(f.server.URL, "https://")
}

func (f *mediaFake) route(path string, handler http.HandlerFunc) {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.routes[path] = handler
}

func (f *mediaFake) json(path string, body any) {
	encoded, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}

	f.route(path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encoded)
	})
}

func (f *mediaFake) bytes(path string, body []byte) {
	f.route(path, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	})
}

func (f *mediaFake) status(path string, code int) {
	f.route(path, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
	})
}

func (f *mediaFake) requests() []seenRequest {
	f.lock.Lock()
	defer f.lock.Unlock()

	return append([]seenRequest(nil), f.seen...)
}

func (f *mediaFake) requestsTo(path string) int {
	count := 0
	for _, seen := range f.requests() {
		if seen.path == path {
			count++
		}
	}

	return count
}

func (f *mediaFake) hitsElsewhere() int {
	f.lock.Lock()
	defer f.lock.Unlock()

	return f.elsewhereHits
}

func mediaPlugin(t *testing.T, cfg *configuration) (*Plugin, map[string][]byte) {
	t.Helper()

	p, store, _ := mediaPluginWithFaults(t, cfg)

	return p, store
}

// mediaHarness is what a media test plugin was built on, so that restart can
// build another on the same store, faults and admins.
type mediaHarness struct {
	store  map[string][]byte
	faults *kvFaults
	admins []string
}

var mediaHarnesses sync.Map // *Plugin -> *mediaHarness

// mediaPluginWithFaults is mediaPlugin with the store's faults in hand. The
// admins are registered before the catch-all "not an admin": testify answers
// with the FIRST expectation that matches, so a more specific one added later
// would never be used.
func mediaPluginWithFaults(t *testing.T, cfg *configuration, admins ...string) (*Plugin, map[string][]byte, *kvFaults) {
	t.Helper()

	return mediaPluginOn(t, map[string][]byte{}, nil, cfg, admins)
}

func mediaPluginOn(t *testing.T, store map[string][]byte, faults *kvFaults, cfg *configuration, admins []string) (*Plugin, map[string][]byte, *kvFaults) {
	t.Helper()

	p, store, faults := communityPluginWithFaults(t, store, faults)

	// planStream plans as villa, and planning is for somebody with a stream
	// linked; these tests are about the title, not about who may plan. The
	// same goes for uid-user, whom mediaPictureOf asks as: a picture nobody
	// holds is fetched only for a title the panel shows, and somebody linked
	// is shown a few more (TestOnlyATitleShownHereFetchesAPicture).
	faults.lock.Lock()
	linkStream(store, "villa")
	linkStream(store, "uid-user")
	faults.lock.Unlock()

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	for _, admin := range admins {
		api.On("HasPermissionTo", admin, model.PermissionManageSystem).Return(true)
	}
	api.On("HasPermissionTo", mock.Anything, model.PermissionManageSystem).Return(false)

	cfg.normalize()
	p.setConfiguration(cfg)

	mediaHarnesses.Store(p, &mediaHarness{store: store, faults: faults, admins: admins})

	return p, store, faults
}

// restart is the same server after a restart: a fresh Plugin, with nothing in
// memory, on the same KV store and faults, with the same settings.
func restart(t *testing.T, p *Plugin) *Plugin {
	t.Helper()

	found, ok := mediaHarnesses.Load(p)
	require.True(t, ok, "restart is for plugins made by mediaPluginWithFaults")
	harness, _ := found.(*mediaHarness)

	cfg := *p.getConfiguration()
	next, _, _ := mediaPluginOn(t, harness.store, harness.faults, &cfg, harness.admins)

	return next
}

// fakeClock is a clock a test moves by hand, so nothing waits on real time.
type fakeClock struct {
	lock sync.Mutex
	at   time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{at: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.at
}

func (c *fakeClock) advance(by time.Duration) {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.at = c.at.Add(by)
}

// useClock drives both the media store and the Twitch token component from
// clock.
func useClock(p *Plugin, clock *fakeClock) {
	p.mediaStore().now = clock.now
	p.twitchApp().now = clock.now
}

// Test credentials for a Twitch application. The secret is distinctive so that
// a test can be sure it never turns up anywhere it should not.
const (
	testTwitchClientID     = "testclientid0123456789abcdefgh"
	testTwitchClientSecret = "TOP-SECRET-twitch-client-secret-0042"
)

func twitchConfig() *configuration {
	return &configuration{MediaLookup: true, TwitchClientID: testTwitchClientID, TwitchClientSecret: testTwitchClientSecret}
}

// igdbToken answers the token route the way Twitch does.
func (f *mediaFake) igdbToken(token string, expiresIn int) {
	f.json("/oauth2/token", map[string]any{"access_token": token, "expires_in": expiresIn, "token_type": "bearer"})
}

// igdbGames answers /v4/games with whatever answer says for the query it was
// sent, so one route can tell a search from a lookup by id.
func (f *mediaFake) igdbGames(answer func(query string) (int, any)) {
	f.route("/v4/games", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		status, reply := answer(string(body))

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if reply != nil {
			encoded, err := json.Marshal(reply)
			if err != nil {
				panic(err)
			}
			_, _ = w.Write(encoded)
		}
	})
}

// requestsWhere counts the requests that keep says yes to.
func (f *mediaFake) requestsWhere(keep func(seenRequest) bool) int {
	count := 0
	for _, seen := range f.requests() {
		if keep(seen) {
			count++
		}
	}

	return count
}

// searchBody is the body of a title search, as the panel posts it.
func searchBody(kind, text string) string {
	body, _ := json.Marshal(mediaSearchRequest{Kind: kind, Q: text})

	return string(body)
}

func mediaSearch(t *testing.T, p *Plugin, kind, text string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/media/search", strings.NewReader(searchBody(kind, text)))

	p.handleMediaSearch(recorder, request)

	return recorder
}

func mustSearch(t *testing.T, p *Plugin, kind, text string) mediaSearchResponse {
	t.Helper()

	recorder := mediaSearch(t, p, kind, text)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var found mediaSearchResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &found))

	return found
}

// mediaPictureOf asks for a picture as uid-user, who is linked, about a title
// the panel is showing them: their own What's on?, chosen and not yet aired.
// The tests that use it are about fetching a picture, not about who may have
// one fetched, which TestOnlyATitleShownHereFetchesAPicture is about. In
// memory only, as the poller keeps it, so nothing is read or written for it.
func mediaPictureOf(t *testing.T, p *Plugin, ref, size string) *httptest.ResponseRecorder {
	t.Helper()

	p.nowPlayingLock.Lock()
	chosen := map[string]nowPlayingEntry{}
	for userID, entry := range p.nowPlayingLast {
		chosen[userID] = entry
	}
	chosen["uid-user"] = nowPlayingEntry{Category: "game", Title: ref, Media: &scheduleMedia{Ref: ref}, SetAt: nowMillis()}
	p.nowPlayingLast = chosen
	p.nowPlayingLock.Unlock()

	return mediaPictureAs(t, p, "uid-user", ref, size)
}

func mediaPictureAs(t *testing.T, p *Plugin, userID, ref, size string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet,
		"/api/v1/media/image?ref="+url.QueryEscape(ref)+"&size="+url.QueryEscape(size), nil)

	p.handleMediaImage(recorder, request, userID)

	return recorder
}

func planStream(t *testing.T, p *Plugin, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()

	if _, given := body["startsAt"]; !given {
		body["startsAt"] = nowMillis() + int64(time.Hour/time.Millisecond)
	}

	encoded, err := json.Marshal(body)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/schedule", strings.NewReader(string(encoded)))

	p.handleScheduleAdd(recorder, request, "villa")

	return recorder
}

func onlyPlan(t *testing.T, p *Plugin) scheduleItem {
	t.Helper()

	items := mustSchedule(t, p, nowMillis())
	require.Len(t, items, 1)

	return items[0]
}

func movieConfig() *configuration {
	return &configuration{MediaLookup: true, TMDBToken: testTMDBToken}
}

func heatDetails() map[string]any {
	return map[string]any{
		"title":         "Heat",
		"status":        "Released",
		"release_date":  "1995-12-15",
		"runtime":       170,
		"tagline":       "A Los Angeles crime saga",
		"overview":      "A group of high-end professional thieves start to feel the heat from the LAPD.",
		"imdb_id":       "tt0113277",
		"genres":        []map[string]any{{"name": "Action"}, {"name": "Crime"}, {"name": "Drama"}, {"name": "Thriller"}},
		"poster_path":   "/heat.jpg",
		"backdrop_path": "/heat-wide.jpg",
	}
}

// Steam's appdetails answer for Elden Ring, as it really came back: keyed by
// the CURRENT edition's id, 2855530, although 1245620 was asked for.
func eldenRingDetails(fake *mediaFake) map[string]any {
	cdn := fake.server.URL + "/steamcdn/apps/1245620/f0b19c231f86fa0e633ff88a1a63459443017728"

	return map[string]any{
		"2855530": map[string]any{
			"success": true,
			"data": map[string]any{
				"type":              "game",
				"name":              "ELDEN RING",
				"steam_appid":       1245620,
				"short_description": "THE CRITICALLY ACCLAIMED FANTASY ACTION RPG. Rise, Tarnished, and be guided by <b>grace</b> &amp; brandish the power of the Elden Ring.",
				"header_image":      cdn + "/header_alt_assets_3.jpg?t=1790290043",
				"capsule_image":     cdn + "/capsule_231x87_alt_assets_3.jpg?t=1790290043",
				"genres":            []map[string]any{{"id": "1", "description": "Action"}, {"id": "3", "description": "RPG"}},
				"developers":        []string{"FromSoftware, Inc."},
				"release_date":      map[string]any{"coming_soon": false, "date": "Feb 24, 2022"},
			},
		},
	}
}

func TestAMovieSearchListsFilmsAndShowsButNotPeople(t *testing.T) {
	fake := newMediaFake(t)

	results := []map[string]any{
		{"media_type": "person", "id": 1158, "name": "Al Pacino"},
		{"media_type": "movie", "id": 949, "title": "Heat", "release_date": "1995-12-15", "poster_path": "/heat.jpg"},
		{"media_type": "tv", "id": 1396, "name": "Breaking Bad", "first_air_date": "2008-01-20"},
		{"media_type": "collection", "id": 5, "name": "Heat Collection"},
	}
	for i := 0; i < 8; i++ {
		results = append(results, map[string]any{"media_type": "movie", "id": 5000 + i, "title": "Heat " + strconv.Itoa(i+2)})
	}
	fake.json("/3/search/multi", map[string]any{"page": 1, "results": results})

	p, _ := mediaPlugin(t, movieConfig())
	found := mustSearch(t, p, "movie", "heat")

	require.Empty(t, found.Unavailable)
	require.Len(t, found.Results, maxMediaResults, "at most eight, however many TMDB offers")
	require.Equal(t, mediaResult{Ref: "tmdb:movie:949", Kind: "movie", Title: "Heat", Year: "1995", Label: "Movie"}, found.Results[0])
	require.Equal(t, mediaResult{Ref: "tmdb:tv:1396", Kind: "tv", Title: "Breaking Bad", Year: "2008", Label: "TV series"}, found.Results[1])
	require.Equal(t, "", found.Results[2].Year, "an unknown year is empty rather than guessed")

	for _, result := range found.Results {
		require.NotEqual(t, "Al Pacino", result.Title, "people are not something to plan a stream of")
		require.NotEqual(t, "Heat Collection", result.Title)
	}

	asked := fake.requests()
	require.Len(t, asked, 1)
	require.Equal(t, "/3/search/multi", asked[0].path)
	require.Equal(t, "heat", asked[0].query.Get("query"))
	require.Equal(t, "false", asked[0].query.Get("include_adult"))
	require.Equal(t, "en-US", asked[0].query.Get("language"))
	require.Equal(t, "1", asked[0].query.Get("page"))
	require.Equal(t, "MattyPlugin/"+manifestVersion+" (Mattermost plugin; +https://github.com/vvilla-01/matty-plugin)", asked[0].agent,
		"the plugin says who it is, and never which community it serves")
}

func TestAGameSearchListsOnlyGamesAndHasNoYear(t *testing.T) {
	fake := newMediaFake(t)

	items := []map[string]any{
		{"type": "app", "name": "ELDEN RING", "id": 1245620, "tiny_image": fake.server.URL + "/steamcdn/apps/1245620/65b8/capsule_231x87.jpg?t=1"},
		{"type": "sub", "name": "ELDEN RING Deluxe Edition", "id": 99},
		{"type": "bundle", "name": "ELDEN RING Bundle", "id": 98},
	}
	for i := 0; i < 8; i++ {
		items = append(items, map[string]any{"type": "app", "name": "ELDEN RING DLC " + strconv.Itoa(i), "id": 3000000 + i})
	}
	fake.json("/api/storesearch/", map[string]any{"total": len(items), "items": items})

	p, _ := mediaPlugin(t, &configuration{MediaLookup: true})
	found := mustSearch(t, p, "game", "elden ring")

	require.Len(t, found.Results, maxMediaResults)
	require.Equal(t, mediaResult{Ref: "steam:1245620", Kind: "game", Title: "ELDEN RING", Year: "", Label: "Game"}, found.Results[0])
	for _, result := range found.Results {
		require.NotContains(t, result.Title, "Edition", "a package is a way to buy a game, not a game")
		require.NotContains(t, result.Title, "Bundle")
	}

	asked := fake.requests()
	require.Len(t, asked, 1, "no details are fetched per result to find a year")
	require.Equal(t, "elden ring", asked[0].query.Get("term"))
	require.Equal(t, "english", asked[0].query.Get("l"))
	require.Equal(t, "US", asked[0].query.Get("cc"))
}

// Switched off means nothing leaves: not a search, not a picture, not a
// lookup while saving, not a Twitch token — and the planning form is told in
// a sentence. With every credential set, so it is the switch that stops them.
func TestNothingIsLookedUpWhenTheFeatureIsOff(t *testing.T) {
	fake := newMediaFake(t)
	fake.json("/api/storesearch/", map[string]any{"items": []any{}})
	fake.igdbToken("tok-1", 5000000)

	off := twitchConfig()
	off.MediaLookup = false
	off.TMDBToken = testTMDBToken
	p, _ := mediaPlugin(t, off)

	for _, kind := range []string{"movie", "game"} {
		recorder := mediaSearch(t, p, kind, "heat")
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Contains(t, recorder.Body.String(), `"results":[]`, "an empty list, not null")

		var found mediaSearchResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &found))
		require.NotEmpty(t, found.Unavailable)
	}

	require.Equal(t, http.StatusNotFound, mediaPictureOf(t, p, "tmdb:movie:949", "cover").Code)
	require.Equal(t, http.StatusNotFound, mediaPictureOf(t, p, "steam:1245620", "cover").Code)
	require.Equal(t, http.StatusNotFound, mediaPictureOf(t, p, "igdb:113360", "cover").Code)

	response := planStream(t, p, map[string]any{"title": "Heat", "category": "movie", "media": "tmdb:movie:949"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Nil(t, onlyPlan(t, p).Media, "saved, without a title it was not allowed to look up")

	response = planStream(t, p, map[string]any{"title": "Hytale", "category": "game", "media": "igdb:113360", "startsAt": inAnHour() + 60000})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	for _, item := range mustSchedule(t, p, nowMillis()) {
		require.Nil(t, item.Media)
	}

	require.Empty(t, fake.requests(), "not one request left while the feature was off")

	// On, but with no token: games work and movies say why they do not.
	q, _ := mediaPlugin(t, &configuration{MediaLookup: true})

	found := mustSearch(t, q, "movie", "heat")
	require.Empty(t, found.Results)
	require.NotEmpty(t, found.Unavailable)

	found = mustSearch(t, q, "game", "elden")
	require.Empty(t, found.Unavailable)

	for _, seen := range fake.requests() {
		require.False(t, strings.HasPrefix(seen.path, "/3/"), "TMDB is never asked without a token")
	}
}

func TestTheSearchTextIsChecked(t *testing.T) {
	fake := newMediaFake(t)
	fake.json("/3/search/multi", map[string]any{"results": []any{}})

	p, _ := mediaPlugin(t, movieConfig())

	for _, refused := range []struct{ kind, text string }{
		{"movie", ""},
		{"movie", "a"},
		{"movie", "   a   "},
		{"movie", strings.Repeat("é", maxMediaQuery+1)},
		{"book", "heat"},
		{"", "heat"},
		{"tv", "heat"},
	} {
		recorder := mediaSearch(t, p, refused.kind, refused.text)
		require.Equal(t, http.StatusBadRequest, recorder.Code, "%q %q", refused.kind, refused.text)
	}
	require.Empty(t, fake.requests(), "nothing refused is sent anywhere")

	require.Equal(t, http.StatusOK, mediaSearch(t, p, "movie", "  up  ").Code, "trimmed, two is enough")
	require.Equal(t, http.StatusOK, mediaSearch(t, p, "MOVIE", strings.Repeat("é", maxMediaQuery)).Code)
}

// A v4 token is a Bearer header; a v3 key only works as a query parameter.
// Admins paste either, and sometimes paste "Bearer " along with it.
func TestTMDBIsAuthenticatedTheWayTheTokenAsks(t *testing.T) {
	for _, tried := range []struct {
		pasted     string
		wantHeader string
		wantKey    string
	}{
		{testTMDBToken, "Bearer " + testTMDBToken, ""},
		{"  Bearer " + testTMDBToken + "  ", "Bearer " + testTMDBToken, ""},
		{testTMDBAPIKey, "", testTMDBAPIKey},
	} {
		fake := newMediaFake(t)
		fake.json("/3/search/multi", map[string]any{"results": []any{}})

		p, _ := mediaPlugin(t, &configuration{MediaLookup: true, TMDBToken: tried.pasted})
		mustSearch(t, p, "movie", "heat")

		asked := fake.requests()
		require.Len(t, asked, 1)
		require.Equal(t, tried.wantHeader, asked[0].auth, "pasted %q", tried.pasted)
		require.Equal(t, tried.wantKey, asked[0].query.Get("api_key"), "pasted %q", tried.pasted)
	}
}

func TestSearchesAreRememberedForAWhile(t *testing.T) {
	fake := newMediaFake(t)
	fake.json("/3/search/multi", map[string]any{"results": []map[string]any{{"media_type": "movie", "id": 949, "title": "Heat"}}})

	p, _ := mediaPlugin(t, movieConfig())

	first := mustSearch(t, p, "movie", "Heat")
	second := mustSearch(t, p, "movie", "  heat ")
	require.Equal(t, first, second)
	require.Len(t, fake.requests(), 1, "the same search, typed differently, is asked once")

	// A different kind is a different search.
	fake.json("/api/storesearch/", map[string]any{"items": []any{}})
	mustSearch(t, p, "game", "heat")
	require.Len(t, fake.requests(), 2)
}

// The person searching is told it failed. The admin is told why, in the log —
// and neither of them is ever shown the credential.
func TestAFailedSearchSaysSoWithoutGivingAnythingAway(t *testing.T) {
	fake := newMediaFake(t)
	fake.status("/3/search/multi", http.StatusUnauthorized)

	p, _ := mediaPlugin(t, &configuration{MediaLookup: true, TMDBToken: testTMDBAPIKey})

	recorder := mediaSearch(t, p, "movie", "heat")
	require.Equal(t, http.StatusBadGateway, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"error"`)
	require.NotContains(t, recorder.Body.String(), testTMDBAPIKey)
	require.NotContains(t, recorder.Body.String(), "401", "the user is not handed the admin's problem")

	// Asked twice, told once: this is logged for the admin, not per search.
	mediaSearch(t, p, "movie", "heat again")

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	told := 0
	for _, call := range api.Calls {
		if !strings.HasPrefix(call.Method, "Log") {
			continue
		}

		line := fmt.Sprint(call.Arguments...)
		require.NotContains(t, line, testTMDBAPIKey, "a log line carried the key")
		if strings.Contains(line, "TMDB refused") {
			told++
		}
	}
	require.Equal(t, 1, told, "the admin is told plainly that the token is wrong, once")

	// A provider that cannot be reached at all is the same 502 — and the
	// transport's error, which quotes the whole address, key and all, does not
	// make it into the log either.
	tmdbAPIBase = "https://127.0.0.1:1/3"
	recorder = mediaSearch(t, p, "movie", "unreachable")
	require.Equal(t, http.StatusBadGateway, recorder.Code)

	for _, call := range api.Calls {
		require.NotContains(t, fmt.Sprint(call.Arguments...), testTMDBAPIKey)
	}
}

func TestTitleRefsAreCheckedStrictly(t *testing.T) {
	for given, want := range map[string]string{
		"tmdb:movie:949":        "tmdb:movie:949",
		"tmdb:tv:1396":          "tmdb:tv:1396",
		"steam:1245620":         "steam:1245620",
		"steam:0001245620":      "steam:1245620",
		"tmdb:movie:9999999999": "tmdb:movie:9999999999",
		"igdb:113360":           "igdb:113360",
		"igdb:0069547":          "igdb:69547",
	} {
		ref, ok := parseMediaRef(given)
		require.True(t, ok, given)
		require.Equal(t, want, ref.String())
	}

	for _, refused := range []string{
		"",
		"tmdb:person:1158",
		"tmdb:movie:",
		"tmdb:movie:12345678901",
		"tmdb:movie:949/../../account",
		"tmdb:movie:-1",
		"steam:12a",
		"steam:1245620\n",
		" steam:1245620",
		"STEAM:1245620",
		"steam:1245620?x=1",
		"https://evil.example/steam:1",
		"tmdb:game:1",
		"steam:tv:1",
		"igdb:",
		"igdb:x",
		"igdb:movie:1",
		"IGDB:1",
		"igdb:12345678901",
	} {
		_, ok := parseMediaRef(refused)
		require.False(t, ok, "%q", refused)
	}

	fake := newMediaFake(t)
	p, _ := mediaPlugin(t, movieConfig())

	require.Equal(t, http.StatusBadRequest, mediaPictureOf(t, p, "tmdb:person:1158", "cover").Code)
	require.Equal(t, http.StatusBadRequest, mediaPictureOf(t, p, "tmdb:movie:949", "huge").Code)
	require.Equal(t, http.StatusBadRequest, mediaPictureOf(t, p, "tmdb:movie:949", "").Code)
	require.Empty(t, fake.requests())
}

func TestPicturesAreRelayedFromTheProviders(t *testing.T) {
	fake := newMediaFake(t)
	fake.json("/3/search/multi", map[string]any{"results": []map[string]any{
		{"media_type": "movie", "id": 949, "title": "Heat", "poster_path": "/heat.jpg", "backdrop_path": nil},
	}})
	fake.bytes("/t/p/w92/heat.jpg", jpegBytes)
	fake.bytes("/t/p/w780/heat.jpg", pngBytes)

	p, _ := mediaPlugin(t, movieConfig())
	mustSearch(t, p, "movie", "heat")

	// The planning form's thumbnail: straight from what the search described.
	response := mediaPictureOf(t, p, "tmdb:movie:949", "thumb")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "image/jpeg", response.Header().Get("Content-Type"))
	require.Equal(t, "private, max-age=86400", response.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	require.Equal(t, jpegBytes, response.Body.Bytes())

	// No backdrop: the poster, at backdrop size, rather than nothing.
	response = mediaPictureOf(t, p, "tmdb:movie:949", "backdrop")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "image/png", response.Header().Get("Content-Type"))

	require.Zero(t, fake.requestsTo("/3/movie/949"), "the search already said where the pictures are")

	// A second panel asking is served from memory.
	before := len(fake.requests())
	require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "thumb").Code)
	require.Len(t, fake.requests(), before)

	// Steam's cover is its portrait box art, and its header when a game has
	// none — which takes the details to find, since that address is Steam's.
	fake.status("/steamcdn/apps/1245620/library_600x900.jpg", http.StatusNotFound)
	fake.json("/api/appdetails", eldenRingDetails(fake))
	fake.bytes("/steamcdn/apps/1245620/f0b19c231f86fa0e633ff88a1a63459443017728/header_alt_assets_3.jpg", jpegBytes)

	response = mediaPictureOf(t, p, "steam:1245620", "cover")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 1, fake.requestsTo("/steamcdn/apps/1245620/library_600x900.jpg"))

	// And a title with no such picture at all is a 404, remembered.
	fake.json("/3/movie/77", map[string]any{"title": "No Art", "poster_path": nil, "backdrop_path": nil})
	require.Equal(t, http.StatusNotFound, mediaPictureOf(t, p, "tmdb:movie:77", "cover").Code)
	require.Equal(t, http.StatusNotFound, mediaPictureOf(t, p, "tmdb:movie:77", "cover").Code)
	require.Equal(t, 1, fake.requestsTo("/3/movie/77"))
}

// A picture this server does not hold is fetched only for a title the panel
// can be showing anybody — a plan, what is on a live stream, a title a search
// has just listed — or one it shows somebody linked: a clip's tag, or their
// own What's on? before they go on the air. Anybody asking about another
// title is told 404 at once: no provider is asked, nothing is remembered or
// written to the disk, and none of their pictures is spent.
//
// Somebody linked included. A Markdown image in a post is fetched by every
// reader's browser with their session, so letting a linked reader have any
// title at all let anybody who could post walk the providers' ids on their
// say-so.
func TestOnlyATitleShownHereFetchesAPicture(t *testing.T) {
	fake := newMediaFake(t)
	fake.json("/3/movie/949", heatDetails())
	fake.bytes("/t/p/w342/heat.jpg", jpegBytes)
	fake.json("/3/movie/950", map[string]any{"title": "Heat 2", "poster_path": "/heat2.jpg"})
	fake.bytes("/t/p/w342/heat2.jpg", jpegBytes)
	fake.bytes("/steamcdn/apps/1245620/library_600x900.jpg", jpegBytes)

	p, store, faults := mediaPluginWithFaults(t, movieConfig())

	// Dave has not linked a stream, and nothing he asks about is shown.
	for range mediaFetchesPerMinute + 2 {
		require.Equal(t, http.StatusNotFound, mediaPictureAs(t, p, "dave", "tmdb:movie:949", "cover").Code)
		require.Equal(t, http.StatusNotFound, mediaPictureAs(t, p, "dave", "steam:1245620", "cover").Code)
	}
	require.Empty(t, fake.requests(), "no provider was asked")
	_, metered := tokensLeft(p.mediaStore().fetchLimiter, "dave")
	require.False(t, metered, "refused before the meter")
	faults.lock.Lock()
	for key := range store {
		require.False(t, strings.HasPrefix(key, kvMediaCachePrefix), "nothing remembered: %s", key)
	}
	faults.lock.Unlock()

	// Planned, so on everybody's panel: fetched for him too.
	p.setSnapshot(&snapshot{Streams: []apiStream{}, Schedule: []scheduleItem{{
		ID: "plan-1", Title: "Heat night", Category: "movie", Who: "Villa",
		Media: &scheduleMedia{Ref: "tmdb:movie:0949", Kind: "movie", Title: "Heat"},
	}}})
	require.Equal(t, http.StatusOK, mediaPictureAs(t, p, "dave", "tmdb:movie:949", "cover").Code)

	// What is on a live stream, the same.
	p.setSnapshot(&snapshot{Streams: []apiStream{{
		NowPlaying: &apiNowPlaying{Category: "game", Title: "Elden Ring", Media: &scheduleMedia{Ref: "steam:1245620", Kind: "game", Title: "ELDEN RING"}},
	}}})
	require.Equal(t, http.StatusOK, mediaPictureAs(t, p, "dave", "steam:1245620", "cover").Code)

	// Held now, so served to anybody, shown or not.
	p.setSnapshot(&snapshot{Streams: []apiStream{}})
	require.Equal(t, http.StatusOK, mediaPictureAs(t, p, "dave", "tmdb:movie:949", "cover").Code)
	require.Equal(t, 1, fake.requestsTo("/t/p/w342/heat.jpg"))

	// Still nothing new for him.
	require.Equal(t, http.StatusNotFound, mediaPictureAs(t, p, "dave", "tmdb:movie:950", "cover").Code)
	require.Zero(t, fake.requestsTo("/3/movie/950"))

	// Nor with a forward link entry left behind for him that names Villa's
	// key, whose reverse entry names Villa: the gate locks him, so he cannot
	// plan, and nothing is fetched on his say.
	faults.lock.Lock()
	store[kvLinkByUserPrefix+"dave"] = []byte("videokey-villa")
	faults.lock.Unlock()
	require.Equal(t, http.StatusNotFound, mediaPictureAs(t, p, "dave", "tmdb:movie:950", "cover").Code)
	require.Zero(t, fake.requestsTo("/3/movie/950"))
	faults.lock.Lock()
	delete(store, kvLinkByUserPrefix+"dave")
	faults.lock.Unlock()

	// Somebody linked may not have any title either: this is what an image
	// in a post would ask for on a linked reader's behalf.
	require.Equal(t, http.StatusNotFound, mediaPictureAs(t, p, "villa", "tmdb:movie:950", "cover").Code)
	require.Zero(t, fake.requestsTo("/3/movie/950"))
	_, metered = tokensLeft(p.mediaStore().fetchLimiter, "villa")
	require.False(t, metered, "refused before the meter")

	// But a clip's tag is shown to somebody linked, and to nobody else. Put
	// in this node's copy of the clips as a clip written here would be.
	clips := &clipsDoc{V: clipsDocVersion, Rev: 1, Clips: []clipEntry{{
		ID:  "clip-heat-2",
		Tag: &clipTag{Category: "movie", Title: "Heat 2", Media: &clipTagMedia{Ref: "tmdb:movie:950", Kind: "movie", Title: "Heat 2", Source: "tmdb"}},
	}}}
	tagged, err := json.Marshal(clips)
	require.NoError(t, err)
	faults.lock.Lock()
	store[kvClips] = tagged
	faults.lock.Unlock()
	p.clips().offer(clips, time.Now())
	require.Equal(t, http.StatusNotFound, mediaPictureAs(t, p, "dave", "tmdb:movie:950", "cover").Code)
	require.Zero(t, fake.requestsTo("/3/movie/950"))
	require.Equal(t, http.StatusOK, mediaPictureAs(t, p, "villa", "tmdb:movie:950", "cover").Code)
	require.Equal(t, 1, fake.requestsTo("/3/movie/950"))

	// And so is their own What's on?, which the footer shows them before
	// their card does — only them.
	fake.json("/3/movie/952", map[string]any{"title": "Heat 4", "poster_path": "/heat4.jpg"})
	fake.bytes("/t/p/w342/heat4.jpg", jpegBytes)
	p.nowPlayingLock.Lock()
	p.nowPlayingLast = map[string]nowPlayingEntry{"villa": {Category: "movie", Title: "Heat 4", Media: &scheduleMedia{Ref: "tmdb:movie:952", Kind: "movie", Title: "Heat 4"}, SetAt: nowMillis()}}
	p.nowPlayingLock.Unlock()
	require.Equal(t, http.StatusNotFound, mediaPictureAs(t, p, "uid-user", "tmdb:movie:952", "cover").Code)
	require.Equal(t, http.StatusOK, mediaPictureAs(t, p, "villa", "tmdb:movie:952", "cover").Code)
	require.Equal(t, 1, fake.requestsTo("/3/movie/952"))

	// A title a search has just listed is shown in the planning form, to
	// whoever searched; its thumbnail is fetched for anybody who asks.
	fake.json("/3/search/multi", map[string]any{"results": []map[string]any{
		{"media_type": "movie", "id": 951, "title": "Heat 3", "poster_path": "/heat3.jpg"},
	}})
	fake.bytes("/t/p/w92/heat3.jpg", jpegBytes)
	mustSearch(t, p, "movie", "heat 3")
	require.Equal(t, http.StatusOK, mediaPictureAs(t, p, "dave", "tmdb:movie:951", "thumb").Code)
}

func TestOnlyTheListedImageHostsAreEverAllowed(t *testing.T) {
	for address, allowed := range map[string]bool{
		"https://image.tmdb.org/t/p/w92/heat.jpg":                                                  true,
		"https://shared.akamai.steamstatic.com/store_item_assets/steam/apps/1/library_600x900.jpg": true,
		"https://cdn.fastly.steamstatic.com/steam/apps/1/header.jpg":                               true,
		"http://image.tmdb.org/t/p/w92/heat.jpg":                                                   false,
		"https://image.tmdb.org:8443/t/p/w92/heat.jpg":                                             false,
		"https://someone@image.tmdb.org/t/p/w92/heat.jpg":                                          false,
		"https://image.tmdb.org.evil.example/heat.jpg":                                             false,
		"https://evil.example/heat.jpg":                                                            false,
		"https://api.themoviedb.org/3/movie/949":                                                   false,
	} {
		parsed, err := url.Parse(address)
		require.NoError(t, err)
		require.Equal(t, allowed, mediaImageURLAllowed(parsed), address)
	}
}

// A provider's answer decides WHICH picture, never WHERE from: an address on
// any other host is refused, and so is a redirect off the list.
func TestThePictureRelayOnlyFetchesFromTheAllowedHosts(t *testing.T) {
	fake := newMediaFake(t)

	fake.json("/api/storesearch/", map[string]any{"items": []map[string]any{
		{"type": "app", "name": "Elsewhere", "id": 10, "tiny_image": fake.elsewhere.URL + "/capsule.jpg"},
		{"type": "app", "name": "Plain HTTP", "id": 11, "tiny_image": "http://" + fake.host() + "/steamcdn/plain.jpg"},
		{"type": "app", "name": "Redirected", "id": 12, "tiny_image": fake.server.URL + "/steamcdn/redirect.jpg"},
	}})
	fake.bytes("/steamcdn/plain.jpg", jpegBytes)
	fake.route("/steamcdn/redirect.jpg", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, fake.elsewhere.URL+"/capsule.jpg", http.StatusFound)
	})

	p, _ := mediaPlugin(t, &configuration{MediaLookup: true})
	mustSearch(t, p, "game", "elsewhere")

	for _, ref := range []string{"steam:10", "steam:11", "steam:12"} {
		require.Equal(t, http.StatusNotFound, mediaPictureOf(t, p, ref, "thumb").Code, ref)
	}

	require.Zero(t, fake.hitsElsewhere(), "nothing was fetched from a host off the list")
	require.Zero(t, fake.requestsTo("/steamcdn/plain.jpg"), "nor over plain HTTP from one on it")
}

func TestOnlyRealPicturesOfASensibleSizeAreRelayed(t *testing.T) {
	fake := newMediaFake(t)

	oversize := append(append([]byte(nil), jpegBytes...), make([]byte, maxMediaImageBytes)...)
	fake.json("/3/search/multi", map[string]any{"results": []map[string]any{
		{"media_type": "movie", "id": 1, "title": "Streamed Too Big", "poster_path": "/streamed.jpg"},
		{"media_type": "movie", "id": 2, "title": "Declared Too Big", "poster_path": "/declared.jpg"},
		{"media_type": "movie", "id": 3, "title": "A Web Page", "poster_path": "/page.jpg"},
		{"media_type": "movie", "id": 4, "title": "A Drawing", "poster_path": "/drawing.jpg"},
		{"media_type": "movie", "id": 5, "title": "Just Right", "poster_path": "/right.jpg"},
	}})
	fake.bytes("/t/p/w342/streamed.jpg", oversize)
	fake.route("/t/p/w342/declared.jpg", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(maxMediaImageBytes+1))
		_, _ = w.Write(jpegBytes)
	})
	fake.bytes("/t/p/w342/page.jpg", []byte("<!doctype html><html><script>alert(1)</script></html>"))
	fake.bytes("/t/p/w342/drawing.jpg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	fake.bytes("/t/p/w342/right.jpg", append(append([]byte(nil), jpegBytes...), make([]byte, maxMediaImageBytes-len(jpegBytes))...))

	p, _ := mediaPlugin(t, movieConfig())
	mustSearch(t, p, "movie", "pictures")

	for _, ref := range []string{"tmdb:movie:1", "tmdb:movie:2", "tmdb:movie:3", "tmdb:movie:4"} {
		response := mediaPictureOf(t, p, ref, "cover")
		require.Equal(t, http.StatusNotFound, response.Code, ref)
		require.NotContains(t, response.Body.String(), "alert")
	}

	response := mediaPictureOf(t, p, "tmdb:movie:5", "cover")
	require.Equal(t, http.StatusOK, response.Code, "exactly at the cap is fine")
	require.Len(t, response.Body.Bytes(), maxMediaImageBytes)
}

// The appdetails answer is keyed by whichever edition Steam considers current,
// which is not always the id that was asked for.
func TestSteamDetailsKeyedByAnotherEditionStillWork(t *testing.T) {
	fake := newMediaFake(t)
	fake.json("/api/appdetails", eldenRingDetails(fake))

	p, _ := mediaPlugin(t, &configuration{MediaLookup: true})

	response := planStream(t, p, map[string]any{"title": "Elden Ring blind run", "category": "game", "media": "steam:1245620"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	media := onlyPlan(t, p).Media
	require.NotNil(t, media)
	require.Equal(t, "steam:1245620", media.Ref)
	require.Equal(t, "game", media.Kind)
	require.Equal(t, "ELDEN RING", media.Title)
	require.Equal(t, "2022", media.Year)
	require.Equal(t, "Action · RPG · FromSoftware, Inc.", media.Facts)
	require.Equal(t, "https://store.steampowered.com/app/1245620/", media.Link)
	require.Equal(t, "Steam", media.Source)
	require.NotContains(t, media.Overview, "<b>", "markup is taken out")
	require.NotContains(t, media.Overview, "&amp;", "entities are turned back into text")
	require.Contains(t, media.Overview, "guided by grace & brandish")

	asked := fake.requests()
	require.Len(t, asked, 1)
	require.Equal(t, "1245620", asked[0].query.Get("appids"))

	// A game Steam has never heard of is a plan without a title, not a failure.
	fake.json("/api/appdetails", map[string]any{"42": map[string]any{"success": false}})
	response = planStream(t, p, map[string]any{"title": "Mystery game", "category": "game", "media": "steam:42"})
	require.Equal(t, http.StatusOK, response.Code)
}

func TestAPlanCarriesTheTitleItWasPlannedWith(t *testing.T) {
	fake := newMediaFake(t)

	details := heatDetails()
	words := make([]string, 0, 80)
	for i := 0; i < 80; i++ {
		words = append(words, "word"+strconv.Itoa(i))
	}
	longOverview := strings.Join(words, " ")
	details["overview"] = longOverview
	fake.json("/3/movie/949", details)

	p, store := mediaPlugin(t, movieConfig())

	response := planStream(t, p, map[string]any{"title": "Heat, with commentary", "category": "movie", "media": "tmdb:movie:949"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	item := onlyPlan(t, p)
	require.Equal(t, "Heat, with commentary", item.Title, "the plan keeps its own title")
	require.Equal(t, &scheduleMedia{
		Ref:      "tmdb:movie:949",
		Kind:     "movie",
		Title:    "Heat",
		Year:     "1995",
		Overview: item.Media.Overview,
		Facts:    "Action · Crime · 2h 50m",
		Link:     "https://www.imdb.com/title/tt0113277/",
		Source:   "TMDB",
	}, item.Media)

	// Trimmed at a word, with the ellipsis inside the limit.
	overview := item.Media.Overview
	require.LessOrEqual(t, utf8.RuneCountInString(overview), maxMediaOverview)
	require.True(t, strings.HasSuffix(overview, "…"))
	kept := strings.TrimSuffix(overview, "…")
	require.True(t, strings.HasPrefix(longOverview, kept+" "), "cut between two words, not in one: %q", kept)

	// Stored with the entry, so it rides the snapshot to every panel.
	require.Contains(t, string(store[kvSchedule]), `"media":{"ref":"tmdb:movie:949"`)

	encoded, err := json.Marshal(snapshot{Schedule: []scheduleItem{item}})
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"media":{"ref":"tmdb:movie:949","kind":"movie","title":"Heat","year":"1995"`)

	asked := fake.requests()
	require.Len(t, asked, 1)
	require.Equal(t, "en-US", asked[0].query.Get("language"))
}

func TestAShowLinksToTMDBWhenItHasNoIMDbPage(t *testing.T) {
	fake := newMediaFake(t)
	fake.json("/3/tv/1396", map[string]any{
		"name":              "Breaking Bad",
		"first_air_date":    "2008-01-20",
		"number_of_seasons": 5,
		"genres":            []map[string]any{{"name": "Drama"}},
		"overview":          "A chemistry teacher diagnosed with cancer.",
		"external_ids":      map[string]any{"imdb_id": "javascript:alert(1)"},
	})

	p, _ := mediaPlugin(t, movieConfig())

	response := planStream(t, p, map[string]any{"title": "Breaking Bad marathon", "category": "movie", "media": "tmdb:tv:1396"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	media := onlyPlan(t, p).Media
	require.NotNil(t, media)
	require.Equal(t, "tv", media.Kind)
	require.Equal(t, "Drama · 5 seasons", media.Facts)
	require.Equal(t, "2008", media.Year)
	require.Equal(t, "https://www.themoviedb.org/tv/1396", media.Link, "an IMDb id that is not one is not used")
	require.Equal(t, "A chemistry teacher diagnosed with cancer.", media.Overview)

	asked := fake.requests()
	require.Len(t, asked, 1)
	require.Equal(t, "external_ids", asked[0].query.Get("append_to_response"))
}

func TestATitleMustMatchThePlansIcon(t *testing.T) {
	fake := newMediaFake(t)
	p, store := mediaPlugin(t, &configuration{MediaLookup: true, TMDBToken: testTMDBToken})

	for _, refused := range []map[string]any{
		{"title": "Heat", "category": "game", "media": "tmdb:movie:949"},
		{"title": "Breaking Bad", "category": "game", "media": "tmdb:tv:1396"},
		{"title": "Elden Ring", "category": "movie", "media": "steam:1245620"},
		{"title": "Heat", "category": "", "media": "tmdb:movie:949"},
		{"title": "Heat", "category": "cooking", "media": "tmdb:movie:949"},
		{"title": "Heat", "category": "movie", "media": "tmdb:person:1158"},
		{"title": "Heat", "category": "movie", "media": "https://www.imdb.com/title/tt0113277/"},
	} {
		response := planStream(t, p, refused)
		require.Equal(t, http.StatusBadRequest, response.Code, "%v", refused)
		require.Contains(t, response.Body.String(), `"error"`)
	}

	require.NotContains(t, store, kvSchedule, "nothing was saved")
	require.Empty(t, fake.requests(), "and nothing was looked up")

	// A plan that would be refused anyway is refused before the lookup, not
	// after eight seconds of it.
	response := planStream(t, p, map[string]any{"title": "Heat", "category": "movie", "media": "tmdb:movie:949", "startsAt": nowMillis() - 1000})
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Empty(t, fake.requests())
}

func TestAPlanIsKeptWhenItsTitleCannotBeLookedUp(t *testing.T) {
	fake := newMediaFake(t)
	fake.status("/3/movie/949", http.StatusInternalServerError)

	p, _ := mediaPlugin(t, movieConfig())

	response := planStream(t, p, map[string]any{"title": "Heat", "category": "movie", "media": "tmdb:movie:949"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	item := onlyPlan(t, p)
	require.Equal(t, "Heat", item.Title)
	require.Equal(t, "movie", item.Category)
	require.Nil(t, item.Media)
	require.Equal(t, 1, fake.requestsTo("/3/movie/949"))
}

func TestMeSaysWhichTitlesCanBeLookedUp(t *testing.T) {
	for _, tried := range []struct {
		cfg  *configuration
		want mediaFlags
	}{
		{&configuration{}, mediaFlags{}},
		{&configuration{TMDBToken: testTMDBToken}, mediaFlags{}},
		{&configuration{MediaLookup: true}, mediaFlags{Games: true}},
		{&configuration{MediaLookup: true, TMDBToken: "   "}, mediaFlags{Games: true}},
		{&configuration{MediaLookup: true, TMDBToken: redactedSetting}, mediaFlags{Games: true}},
		{&configuration{MediaLookup: true, TMDBToken: testTMDBToken}, mediaFlags{Movies: true, Games: true}},
		{&configuration{MediaLookup: true, TwitchClientID: testTwitchClientID, TwitchClientSecret: testTwitchClientSecret}, mediaFlags{Games: true, IGDB: true}},
		{&configuration{MediaLookup: false, TwitchClientID: testTwitchClientID, TwitchClientSecret: testTwitchClientSecret}, mediaFlags{}},
		{&configuration{MediaLookup: true, TwitchClientID: testTwitchClientID}, mediaFlags{Games: true}},
		{&configuration{MediaLookup: true, TwitchClientID: testTwitchClientID, TwitchClientSecret: redactedSetting}, mediaFlags{Games: true}},
		{&configuration{MediaLookup: true, TMDBToken: testTMDBToken, TwitchClientID: testTwitchClientID, TwitchClientSecret: testTwitchClientSecret}, mediaFlags{Movies: true, Games: true, IGDB: true}},
	} {
		p, _ := mediaPlugin(t, tried.cfg)

		recorder := httptest.NewRecorder()
		p.handleMe(recorder, "villa")
		require.Equal(t, http.StatusOK, recorder.Code)

		var me struct {
			Media *mediaFlags `json:"media"`
		}
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &me))
		require.NotNil(t, me.Media, "always present, so the webapp need not guess")
		require.Equal(t, tried.want, *me.Media)
		require.NotContains(t, recorder.Body.String(), testTMDBToken, "the token never reaches a browser")
		require.NotContains(t, recorder.Body.String(), testTwitchClientSecret, "nor does the Twitch secret")
		require.Contains(t, recorder.Body.String(), `"igdb":`, "always present, as the other two are")
	}
}

// A document from a later build, or edited by hand, cannot put anything into
// the panel's hands that the lookup itself would not have.
func TestStoredTitlesAreCheckedOnTheWayOut(t *testing.T) {
	p, store := mediaPlugin(t, movieConfig())
	at := strconv.FormatInt(nowMillis()+3600000, 10)

	entry := func(id, media string) string {
		if media != "" {
			media = `,"media":` + media
		}

		return `{"id":"` + id + `","userId":"villa","title":"Plan ` + id + `","category":"movie","startsAt":` + at + media + `,"addedAt":1}`
	}
	good := `{"ref":"tmdb:movie:949","kind":"movie","title":"Heat","year":"1995","overview":"Thieves.","facts":"Crime","link":"https://www.imdb.com/title/tt0113277/","source":"TMDB"}`
	igdbGood := `{"ref":"igdb:113360","kind":"game","title":"Hytale","year":"2026","overview":"Blocks.","facts":"RPG","link":"https://www.igdb.com/games/hytale","source":"IGDB"}`

	store[kvSchedule] = []byte(`{"v":1,"entries":[` + strings.Join([]string{
		entry("a", good),
		entry("b", strings.Replace(good, "https://www.imdb.com/title/tt0113277/", "javascript:alert(1)", 1)),
		entry("c", strings.Replace(good, `"kind":"movie"`, `"kind":"game"`, 1)),
		entry("d", strings.Replace(good, `"source":"TMDB"`, `"source":"Steam"`, 1)),
		entry("e", strings.Replace(good, "tmdb:movie:949", "tmdb:movie:../949", 1)),
		entry("f", ""),
		entry("g", igdbGood),
		entry("h", strings.Replace(igdbGood, `"source":"IGDB"`, `"source":"Steam"`, 1)),
		entry("i", strings.Replace(igdbGood, "https://www.igdb.com/games/hytale", "https://www.igdb.example/games/hytale", 1)),
	}, ",") + `]}`)

	items := mustSchedule(t, p, nowMillis())
	require.Len(t, items, 9, "every plan is still shown")

	media := map[string]*scheduleMedia{}
	for _, item := range items {
		media[item.ID] = item.Media
	}

	require.NotNil(t, media["a"])
	require.Equal(t, "Heat", media["a"].Title)
	for _, dropped := range []string{"b", "c", "d", "e"} {
		require.Nil(t, media[dropped], dropped)
	}
	require.Nil(t, media["f"], "a plan from before titles could be picked reads as one without")

	require.NotNil(t, media["g"], "an IGDB title with IGDB's link and name is kept")
	require.Equal(t, "igdb:113360", media["g"].Ref)
	require.Nil(t, media["h"], "an IGDB ref claiming to be Steam's is not")
	require.Nil(t, media["i"], "nor one leading anywhere else")
}

func TestFactsAndOverviewsAreKeptShort(t *testing.T) {
	require.Equal(t, "Action · RPG", factLine("Action", "RPG", strings.Repeat("Developer ", 20)),
		"a fact that does not fit is left off whole rather than cut")

	lone := factLine(strings.Repeat("x", 300))
	require.LessOrEqual(t, utf8.RuneCountInString(lone), maxMediaFacts)
	require.True(t, strings.HasSuffix(lone, "…"))

	require.Equal(t, "Crime · Thriller · 2h 50m", factLine("Crime", "Thriller", runtimeText(170)))
	require.Equal(t, "Drama", factLine("", "Drama", "", seasonsText(0)))

	require.Equal(t, "2h", runtimeText(120))
	require.Equal(t, "45m", runtimeText(45))
	require.Equal(t, "", runtimeText(0))
	require.Equal(t, "1 season", seasonsText(1))
	require.Equal(t, "5 seasons", seasonsText(5))

	require.Equal(t, "Short and whole.", overviewOf("  Short   and\nwhole.  ", "the tagline"))
	require.Equal(t, "the tagline", overviewOf("", "the tagline"))

	giant := overviewOf(strings.Repeat("a", 1000), "")
	require.Equal(t, maxMediaOverview, utf8.RuneCountInString(giant), "one enormous word is cut, not deleted")

	require.Equal(t, "2022", yearOf("Feb 24, 2022"))
	require.Equal(t, "1995", yearOf("1995-12-15"))
	require.Equal(t, "", yearOf("Coming soon"))
	require.Equal(t, "", yearOf(""))

	require.Equal(t, "Tom Clancy's Rainbow Six", plainText("Tom Clancy&#39;s <b>Rainbow</b>   Six"))
}

func TestTheCachesStayWithinTheirBounds(t *testing.T) {
	counted := newMemoryCache[string](2, 0, nil)
	counted.put("a", "A", time.Hour)
	counted.put("b", "B", time.Hour)
	time.Sleep(time.Millisecond)
	_, _ = counted.get("a")
	counted.put("c", "C", time.Hour)

	_, kept := counted.get("a")
	require.True(t, kept, "used recently, so kept")
	_, kept = counted.get("b")
	require.False(t, kept, "least recently used, so gone")
	_, kept = counted.get("c")
	require.True(t, kept)

	sized := newMemoryCache(10, 100, func(value []byte) int { return len(value) })
	sized.put("first", make([]byte, 60), time.Hour)
	sized.put("second", make([]byte, 60), time.Hour)
	require.LessOrEqual(t, sized.bytes, 100)
	_, kept = sized.get("first")
	require.False(t, kept, "made room for by bytes, not only by count")

	sized.put("huge", make([]byte, 101), time.Hour)
	_, kept = sized.get("huge")
	require.False(t, kept, "bigger than the whole cache is not kept")
	_, kept = sized.get("second")
	require.True(t, kept, "and does not empty the cache trying")

	sized.put("stale", []byte("x"), -time.Second)
	_, kept = sized.get("stale")
	require.False(t, kept, "expired is gone")
}

func TestTheMediaRoutesAreForSignedInPeopleAndMetered(t *testing.T) {
	fake := newMediaFake(t)
	p, store := mediaPlugin(t, &configuration{})

	send := func(method, path, userID string) int {
		recorder := httptest.NewRecorder()
		var body io.Reader
		if method == http.MethodPost {
			body = strings.NewReader(searchBody("game", "elden"))
		}
		request := httptest.NewRequest(method, path, body)
		if userID != "" {
			request.Header.Set("Mattermost-User-Id", userID)
		}
		p.ServeHTTP(nil, recorder, request)

		return recorder.Code
	}

	require.Equal(t, http.StatusUnauthorized, send(http.MethodPost, "/api/v1/media/search", ""))
	require.Equal(t, http.StatusUnauthorized, send(http.MethodGet, "/api/v1/media/image?ref=steam:1&size=thumb", ""))
	require.Equal(t, http.StatusMethodNotAllowed, send(http.MethodGet, "/api/v1/media/search?kind=game&q=elden", "villa"))
	require.Equal(t, http.StatusMethodNotAllowed, send(http.MethodPost, "/api/v1/media/image?ref=steam:1&size=thumb", "villa"))

	for i := 0; i < mediaSearchesPerMinute; i++ {
		require.Equal(t, http.StatusOK, send(http.MethodPost, "/api/v1/media/search", "villa"))
	}
	require.Equal(t, http.StatusTooManyRequests, send(http.MethodPost, "/api/v1/media/search", "villa"))
	linkStream(store, "someone-else")
	require.Equal(t, http.StatusOK, send(http.MethodPost, "/api/v1/media/search", "someone-else"),
		"metered per person")

	require.Empty(t, fake.requests())
}

// A Markdown image in a post is fetched by every reader's browser with their
// session, and by every other client that shows the post, as a GET. A search
// costs something — a provider asked in the reader's name, one of their
// searches, the answer kept, and every title it lists made shown here for
// anybody — so no GET is a search, whatever headers come with it: not a
// browser's, and not those of a native app whose picture loader sends the
// header the panel's requests carry. A search is a POST, which nothing that
// loads an address makes.
func TestAnImageInAPostCannotSearchForTitles(t *testing.T) {
	fake := newMediaFake(t)
	fake.json("/3/search/multi", map[string]any{"results": []map[string]any{
		{"media_type": "movie", "id": 949, "title": "Heat", "poster_path": "/heat.jpg"},
	}})
	p, _ := mediaPlugin(t, movieConfig())

	search := func(method string, header map[string]string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/media/search?kind=movie&q=heat", nil)
		if method == http.MethodPost {
			request = httptest.NewRequest(http.MethodPost, "/api/v1/media/search", strings.NewReader(searchBody("movie", "heat")))
		}
		request.Header.Set("Mattermost-User-Id", "villa")
		for name, value := range header {
			request.Header.Set(name, value)
		}
		p.ServeHTTP(nil, recorder, request)

		return recorder
	}

	ref, ok := parseMediaRef("tmdb:movie:949")
	require.True(t, ok)

	// villa is linked, and something of hers is showing somebody's post.
	for _, header := range []map[string]string{nil, {"X-Requested-With": "fetch"}, {"Sec-Fetch-Dest": "image"}, {"X-Requested-With": "XMLHttpRequest"}} {
		response := search(http.MethodGet, header)
		require.Equal(t, http.StatusMethodNotAllowed, response.Code, "%v", header)
	}
	require.Empty(t, fake.requests(), "no provider was asked")
	_, metered := tokensLeft(p.mediaStore().searchLimiter, "villa")
	require.False(t, metered, "refused before the meter")
	require.False(t, p.refShownHere(ref), "and nothing it would have listed is shown here")

	// The panel's own search is answered, and needs no header to say so.
	response := search(http.MethodPost, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 1, fake.requestsTo("/3/search/multi"))
	require.True(t, p.refShownHere(ref))
}

// What to look for is read strictly: a body that is not the two fields, or is
// larger than any search could be, is refused before anything is asked.
func TestASearchReadsItsBodyStrictly(t *testing.T) {
	fake := newMediaFake(t)
	fake.json("/3/search/multi", map[string]any{"results": []any{}})
	p, _ := mediaPlugin(t, movieConfig())

	post := func(body string) int {
		recorder := httptest.NewRecorder()
		p.handleMediaSearch(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/media/search", strings.NewReader(body)))

		return recorder.Code
	}

	// The last is a search like any other but for its size: only the limit
	// on the body refuses it.
	for _, body := range []string{"", "{nope", `{"kind":"movie","q":"heat","also":1}`, `{"kind":"movie",` + strings.Repeat(" ", 2000) + `"q":"heat"}`} {
		require.Equal(t, http.StatusBadRequest, post(body), "%.40s", body)
	}
	require.Equal(t, http.StatusBadRequest, post(`{"kind":"book","q":"heat"}`))
	require.Equal(t, http.StatusBadRequest, post(`{"kind":"movie","q":"h"}`))
	require.Empty(t, fake.requests(), "nothing was asked for any of those")

	require.Equal(t, http.StatusOK, post(`{"kind":"movie",`+strings.Repeat(" ", 200)+`"q":"heat"}`), "the same search, small enough")
	require.Equal(t, 1, fake.requestsTo("/3/search/multi"))
}

func TestOnlySomebodyWhoCanPlanCanSearchForTitles(t *testing.T) {
	fake := newMediaFake(t)
	p, store := mediaPlugin(t, &configuration{})

	search := func(userID string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/media/search", strings.NewReader(searchBody("movie", "heat")))
		request.Header.Set("Mattermost-User-Id", userID)
		p.ServeHTTP(nil, recorder, request)

		return recorder
	}

	// Nobody linked: refused by the access gate before anything is looked
	// up or metered.
	for i := 0; i < mediaSearchesPerMinute+2; i++ {
		response := search("dave")
		require.Equal(t, http.StatusForbidden, response.Code)
		require.Contains(t, response.Body.String(), `"code":"locked"`)
	}
	require.Empty(t, fake.requests())

	// Behind the gate, the handler's own check keeps its words, with the
	// code the gate would have given.
	recorder := httptest.NewRecorder()
	require.False(t, p.requireLinkedStream(recorder, "dave", searchNeedsLink))
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), searchNeedsLink)
	require.Contains(t, recorder.Body.String(), `"code":"locked"`)

	// Linking lets them search at once, with every search of the minute still
	// theirs: the refusals above spent none. Linking forgets the refusals
	// the gate remembered, as handleLink does here.
	linkStream(store, "dave")
	p.accessForget("dave")
	for i := 0; i < mediaSearchesPerMinute; i++ {
		require.NotEqual(t, http.StatusTooManyRequests, search("dave").Code)
	}
}
