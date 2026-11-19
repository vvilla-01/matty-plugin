package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The client contract, as files: ../contract/fixtures holds what this server
// really answers (and pushes) for a small cast, written by these tests from
// the same handlers and the same structs that serve a real client, and held
// to them byte for byte afterwards.
//
// They exist for the other side. Matty decodes these very files in its own
// unit tests (it keeps a copy, and a test there fails when the copy and this
// directory differ), so a field renamed, retyped or dropped here breaks a test
// in whichever repository did not follow, instead of a screen on somebody's
// phone. (The web panel is built and shipped with this server, and its types
// follow these structs by hand: nothing checks them against these files yet.)
//
// To change the contract on purpose, change the code, then write them afresh
// and read the difference like any other:
//
//	VILLANET_UPDATE_GOLDEN=1 go test ./server/ -run TestContract
//
// and bump contractRevision (plugin.go) when an old client would misread the
// new answer. See contract/README.md.

func contractFixture(t *testing.T, name string, raw []byte) {
	t.Helper()

	// Which build answered is not part of the contract: a release that
	// changes nothing a client reads must not change these files.
	raw = bytes.ReplaceAll(raw, []byte(`"version":"`+manifestVersion+`"`), []byte(`"version":"VERSION"`))

	// These files are copied into another repository, so they hold nobody's
	// secrets: no bearer token, no provider key, and a stream key only where
	// it is the caller's own answer (their /me).
	for _, secret := range []string{dansToken, davesToken, testSteamKey, testTwitchClientSecret} {
		require.NotContains(t, string(raw), secret, name)
	}
	if !strings.HasPrefix(name, "me-") {
		require.NotContains(t, string(raw), "videokey-", "%s: never a stream key", name)
	}

	// Steam and Hytale are for readers who may watch: nothing of them rides
	// the snapshot, the boards or any broadcast, which reach everybody.
	if strings.HasPrefix(name, "snapshot") || strings.HasPrefix(name, "boards") || strings.HasPrefix(name, "event-") {
		require.NotRegexp(t, `\\?"(steam|hytale)\\?"\s*:`, string(raw), name)
	}

	var pretty bytes.Buffer
	require.NoError(t, json.Indent(&pretty, bytes.TrimSpace(raw), "", "  "))
	pretty.WriteByte('\n')

	requireGolden(t, "../contract/fixtures/"+name, pretty.Bytes())
}

// The compatibility record: which contract this is and the first version
// that spoke it. A client keeps the same two figures and a test of its own
// holds them equal to this file, so "this app needs the Matty plugin 4.0.0 or
// newer" is never a sentence somebody typed.
func TestContractRecord(t *testing.T) {
	record, err := json.MarshalIndent(map[string]any{"revision": contractRevision, "since": contractSince}, "", "  ")
	require.NoError(t, err)

	requireGolden(t, "../contract/contract.json", append(record, '\n'))
}

// The one event about a reader's own state, "me": to them alone (or to
// everybody, for a change that is everybody's), and with nothing about anybody
// in it, whatever changed: only the server's name and which artwork it has,
// which every lock screen shows. A link, an unlink, a let-in and a connection
// made or taken away are all this one event, and none of them says which.
func TestContractMeEvent(t *testing.T) {
	s := newAccessScene(t)
	fake := newFakeBroadcastBox(t, map[string]string{dansToken: "videokey-dan"})
	s.pointedAt(fake)

	require.Equal(t, "me", wsEventMe)
	require.Equal(t, http.StatusOK, s.link(sceneLocked, dansToken).Code)
	s.p.tellTwitchChanged(sceneLocked)
	s.p.tellAccountChanged(sceneLocked)
	s.p.tellEverybodyMe()

	var to []string
	for _, call := range s.api.Calls {
		if call.Method != "PublishWebSocketEvent" || call.Arguments[0] == wsEventStreams || call.Arguments[0] == wsEventAccounts {
			continue
		}

		require.Equal(t, wsEventMe, call.Arguments[0], "every event about one reader's state is this one")
		require.Equal(t, map[string]any{
			"name":    "Matty",
			"artwork": map[string]any{"emblem": false, "ghost": false},
		}, call.Arguments[1], "and carries nothing about anybody")

		broadcast, ok := call.Arguments[2].(*model.WebsocketBroadcast)
		require.True(t, ok)
		require.Equal(t, model.WebsocketBroadcast{UserId: broadcast.UserId}, *broadcast, "to one person, or to every connection")
		to = append(to, broadcast.UserId)
	}
	require.Equal(t, []string{sceneLocked, sceneLocked, sceneLocked, ""}, to)
}

// The streams event exactly as Mattermost is handed it: the snapshot as a
// string under "payload", and under "tag" the bare tag that GET
// /api/v1/streams sends quoted as its ETag.
func TestContractStreamsEvent(t *testing.T) {
	api := &plugintest.API{}
	api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return()

	p := &Plugin{}
	p.SetAPI(api)
	p.publish(&snapshot{
		Configured: true,
		FetchedAt:  1790942400000,
		Streams:    []apiStream{{Handle: "h1", Title: "Dave", UserID: "u-dave", Viewers: 2}},
	})

	require.Len(t, api.Calls, 1)
	call := api.Calls[0]
	require.Equal(t, wsEventStreams, call.Arguments[0])
	require.Equal(t, &model.WebsocketBroadcast{}, call.Arguments[2], "to every connection")

	data, ok := call.Arguments[1].(map[string]any)
	require.True(t, ok)
	require.Len(t, data, 2)
	require.Equal(t, quoted(data["tag"].(string)), quoted(p.snapshotTag))

	encoded, err := json.Marshal(data)
	require.NoError(t, err)
	contractFixture(t, "event-streams.json", encoded)
}

// The snapshot, GET /api/v1/streams and the streams event's payload, and the
// boards, GET /api/v1/boards, as one publish makes them. The golden scene
// with the boards, the medals, the achievements and the fans on, and on top
// of it people in voice and wearing badges, so the people index has every
// kind of entry, and somebody who chose what shows beside their name, so the
// two ways of deciding that are both there.
func TestContractSnapshot(t *testing.T) {
	newMediaFake(t)
	g := newGoldenScene(t, &configuration{
		TwitchClientID: testTwitchClientID, TwitchClientSecret: testTwitchClientSecret,
		EnableAchievements: true, EnableBiggestFans: true,
	})

	// A plan, with somebody in on it.
	startsAt := nowMillis() + int64(time.Hour/time.Millisecond)
	require.NoError(t, g.p.addScheduleEntry("u-dave", "Arma co-op", "game", startsAt, nowMillis()))
	require.NoError(t, g.p.joinScheduleEntry("u-bob", onlyPlan(t, g.p).ID, true, nowMillis()))

	// Dave chose: the calendar before the crown, and a coin. Everybody else
	// is on Automatic.
	require.NoError(t, g.p.setAwardChoice("u-dave", []string{awardMonth, awardAllTime, awardAchPrefix + achStreamed}, nowMillis()))

	// Three people with a banner, a PNG and a JPEG, each kept as an upload
	// keeps it: small pictures kept for tests, so the versions are the same
	// on every machine. Alice chose the part of hers that shows, the middle
	// third of a square picture across its whole width; Bob and Dave chose
	// none.
	bannersAt := filepath.Join(t.TempDir(), bannersDir)
	g.p.banners().setDir(bannersAt, "")
	for userID, kept := range map[string]string{
		contractBannerDave: "testdata/banner-square.png",
		contractBannerBob:  "testdata/banner-square.jpg",
	} {
		picture, err := os.ReadFile(kept)
		require.NoError(t, err)
		kind := http.DetectContentType(picture)
		require.True(t, checkBanner(picture, kind), kept)
		_, _, err = g.p.banners().put(bannersAt, userID, kind, picture)
		require.NoError(t, err)
	}
	square, err := os.ReadFile("testdata/banner-square.png")
	require.NoError(t, err)
	shape, ok := bannerShape(square, bannerTypePNG)
	require.True(t, ok)
	aliceCrop := &bannerCrop{0, 3333, 10000, 3333}
	require.True(t, aliceCrop.fits(shape.Width, shape.Height), "three to one on the picture")
	_, _, err = g.p.banners().putCropped(bannersAt, contractBannerAlice, bannerTypePNG, square, aliceCrop)
	require.NoError(t, err)

	g.p.pollOnce(t.Context(), true)
	g.p.pollOnce(t.Context(), true)

	shown := *g.p.getSnapshot()
	shown.FetchedAt = 0
	if shown.MonthLabel != "" {
		shown.MonthLabel = "MONTH"
	}

	// The plan's id is random and its time is an hour from whenever this
	// runs: neither is the contract's business.
	require.Len(t, shown.Schedule, 1)
	shown.Schedule = append([]scheduleItem(nil), shown.Schedule...)
	shown.Schedule[0].ID, shown.Schedule[0].StartsAt = "00000000000000d4", 1790946000000

	// Voice: the rig's channel tree, read as the roster connection reads
	// it, and four people in it. Bob is deafened in Gaming; Dave is in the
	// Lobby twice, muted on a desktop and talking from Matty, as the account
	// that joined; a guest nobody could match sits in the root.
	shown.Voice = &voiceView{Connected: true, Channels: voiceChannelsOf(voiceTreeClient()), People: []voicePerson{
		{Session: 5, Name: "Bob", UserID: "u-bob", Channel: 2, Muted: true, Deaf: true},
		{Session: 3, Name: "Dave", UserID: "u-dave", Channel: 1, Muted: true},
		{Session: 12, Name: "dave (Matty)", UserID: "u-dave", Channel: 1, Phone: true},
		{Session: 7, Name: "Guest"},
	}}

	// On top of the scene's airtime boards, what its settings leave off: the
	// Snoopers boards, a biggest fan and a Best Clipper, so that every kind of
	// row a profile can have is in the file.
	bob := leaderEntry{Title: "Bob Turner", UserID: "u-bob", Username: "bob", DisplayName: "Bob Turner"}
	dave := leaderEntry{Title: "Dave", UserID: "u-dave", Username: "dave", DisplayName: "Dave"}
	bob.Rank, bob.Seconds, dave.Rank, dave.Seconds = 1, 90000, 2, 4000
	shown.WatchBoards = true
	shown.WatchAllTime, shown.WatchAllTimeTotal = []leaderEntry{bob, dave}, 2
	bob.Seconds = 1200
	shown.WatchMonth, shown.WatchMonthTotal = []leaderEntry{bob}, 1
	shown.Fans, shown.FansOn = []fanView{{StreamerID: "u-dave", Streamer: "Dave", FanID: "u-bob", Fan: "Bob Turner", FanUsername: "bob", Hours: 25}}, true
	shown.BestClipper = &bestClipperView{UserID: "u-bob", Username: "bob", DisplayName: "Bob Turner", Upvotes: 12, Clips: 3, Since: 1790000000000, BestClip: &bestClipRef{ID: "00000000000000a1"}}

	// As publish does it, in its order: the people, then the awards, which
	// puts each person's beside their name and lays the boards out.
	shown.People = peopleIndex(shown.Streams, shown.Voice, map[string]string{"u-dave": badgeLive, "u-bob": badgeBirthday})
	boards := decideAwards(&shown)
	require.Equal(t, boards.Version, shown.Boards.Version, "the snapshot carries the boards' own word for when they change")
	maskBoards(&shown, boards)

	encoded, err := json.Marshal(shown)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "videokey-dave", "never a stream key")
	for _, gone := range []string{"allTime", "month", "monthLabel", "monthMedals", "watchAllTime", "watchMonth", "bestClipper", "awardChoices", "fans", "achievementKinds", "achievements", "allTimeRank", "monthRank"} {
		require.NotContains(t, string(encoded), `"`+gone+`":`, "the boards are not in the snapshot")
	}

	contractFixture(t, "snapshot.json", encoded)

	encoded, err = json.Marshal(boards)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "videokey-dave", "never a stream key")
	require.NotContains(t, string(encoded), `"awardChoices"`, "nobody's choice leaves the server: only what it comes to")

	contractFixture(t, "boards.json", encoded)

	// GET /api/v1/banners, whose version the snapshot carries.
	recorder := httptest.NewRecorder()
	g.p.handleBanners(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/banners", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	var banners bannersView
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &banners))
	require.Len(t, banners.People, 3)
	require.Equal(t, aliceCrop, banners.People[contractBannerAlice].Crop)
	require.Nil(t, banners.People[contractBannerBob].Crop, "no crop is no field")
	require.Contains(t, recorder.Body.String(), `"crop":[0,0.3333,1,0.3333]`, "four places at most, as plain decimals")
	require.Equal(t, banners.Version, shown.Banners.Version, "the snapshot carries the list's own word")

	contractFixture(t, "banners.json", recorder.Body.Bytes())
}

// Who has a banner in the contract's scene: ids as Mattermost makes them,
// which is all a banner is kept under.
const (
	contractBannerDave  = "davebanner0000000000000000"
	contractBannerBob   = "bobbanner00000000000000000"
	contractBannerAlice = "alicebanner000000000000000"
)

// The same scene with every optional feature off: everything the server
// leaves out is left out. A client must make of an absent field exactly what
// the server means by leaving it out, and this is where a client's test finds
// out that it does. (A client's default of "on" for a field the server leaves
// out when off was a real bug.)
func TestContractQuietSnapshot(t *testing.T) {
	g := newGoldenScene(t, &configuration{})

	// The scene's own defaults leave previews on.
	cfg := g.p.getConfiguration().Clone()
	cfg.LivePreviews = false
	g.p.setConfiguration(cfg)

	g.p.pollOnce(t.Context(), true)
	g.p.pollOnce(t.Context(), true)

	shown := *g.p.getSnapshot()
	shown.FetchedAt = 0

	boards := g.p.boardsNow()
	maskBoards(&shown, boards)

	encoded, err := json.Marshal(shown)
	require.NoError(t, err)
	for _, absent := range []string{"livePreviews", "twitchOn", "twitchEmbed", "voice", "soundboard"} {
		require.NotContains(t, string(encoded), `"`+absent+`"`, "left out while off")
	}
	require.Contains(t, string(encoded), `"people":{`, "the index is always there")
	require.Contains(t, string(encoded), `"boards":{"version":"VERSION"}`, "and so is the boards' word")
	require.Contains(t, string(encoded), `"banners":{"version":"`+emptyBanners().Version+`"}`, "and the banners', nobody's")

	contractFixture(t, "snapshot-quiet.json", encoded)

	// The boards with the same features off: the airtime boards are all
	// there is.
	encoded, err = json.Marshal(boards)
	require.NoError(t, err)
	for _, absent := range []string{"watchBoards", "watchAllTime", "fansOn", "fans", "achievementKinds", "achievements", "bestClipper"} {
		require.NotContains(t, string(encoded), `"`+absent+`"`, "left out while off")
	}
	require.Contains(t, string(encoded), `"people":{`, "everybody's record is always there")

	contractFixture(t, "boards-quiet.json", encoded)
}

// GET /api/v1/me for somebody linked and somebody locked, and GET
// /api/v1/access, the one question a player asks while it plays.
func TestContractMeAndAccess(t *testing.T) {
	s := newAccessScene(t)

	linked := s.serve(http.MethodGet, "/api/v1/me", sceneLinked, "", nil)
	require.Equal(t, http.StatusOK, linked.Code)
	contractFixture(t, "me-linked.json", linked.Body.Bytes())

	locked := s.serve(http.MethodGet, "/api/v1/me", sceneLocked, "", nil)
	require.Equal(t, http.StatusOK, locked.Code)
	contractFixture(t, "me-locked.json", locked.Body.Bytes())

	access := s.serve(http.MethodGet, "/api/v1/access", sceneLetIn, "", nil)
	require.Equal(t, http.StatusOK, access.Code)
	contractFixture(t, "access.json", access.Body.Bytes())

	// From 4.3.0, with voice on: whether a Matty may talk, and why not.
	quiet := s.p.getConfiguration()
	voiceOn := quiet.Clone()
	voiceOn.EnableVoice, voiceOn.MumbleServer = true, "mumble.example.com"
	s.p.setConfiguration(voiceOn)
	voice := s.serve(http.MethodGet, "/api/v1/access", sceneLetIn, "", nil)
	require.Equal(t, http.StatusOK, voice.Code)
	contractFixture(t, "access-voice.json", voice.Body.Bytes())
	s.p.setConfiguration(quiet)

	// Whether this is a signed release: as the tests run, never, since they
	// run from no release folder (release_check_test.go has the other two).
	unsigned := s.serve(http.MethodGet, "/api/v1/release", sceneLocked, "", nil)
	require.Equal(t, http.StatusOK, unsigned.Code)
	contractFixture(t, "release-unsigned.json", unsigned.Body.Bytes())

	// Unlinking: the answer says the caller's access afterwards, as a link's
	// does (that one also carries the stream name, which no app keeps).
	unlinked := s.serve(http.MethodDelete, "/api/v1/me/link", sceneLetIn, "", nil)
	require.Equal(t, http.StatusOK, unlinked.Code)
	contractFixture(t, "unlink.json", unlinked.Body.Bytes())

	// A refusal: every one, on every route, has a sentence and a code.
	refused := s.serve(http.MethodGet, "/api/v1/accounts", sceneLocked, "", nil)
	require.Equal(t, http.StatusForbidden, refused.Code)
	contractFixture(t, "refusal-locked.json", refused.Body.Bytes())

	// A stream that is not there to play, which a player asks about again.
	notLive := s.serve(http.MethodPost, "/api/v1/whep", sceneLinked, viewerOffer, map[string]string{"X-Stream-Handle": "0123456789abcdef"})
	require.Equal(t, http.StatusNotFound, notLive.Code)
	contractFixture(t, "refusal-not-live.json", notLive.Body.Bytes())

	// And one that is about time says how long to wait: here the sixth link
	// in a quarter of an hour. How long is left moves with the clock, so the
	// file holds the whole of one token's wait.
	fake := newFakeBroadcastBox(t, map[string]string{})
	s.pointedAt(fake)
	for range 5 {
		require.Equal(t, http.StatusForbidden, s.link(sceneLocked, "nobodys-token").Code)
	}
	limited := s.link(sceneLocked, "nobodys-token")
	require.Equal(t, http.StatusTooManyRequests, limited.Code)

	var wait errorResponse
	require.NoError(t, json.Unmarshal(limited.Body.Bytes(), &wait))
	require.Positive(t, wait.RetryAfterMs)
	wait.RetryAfterMs = 180000
	encoded, err := json.Marshal(wait)
	require.NoError(t, err)
	contractFixture(t, "refusal-rate-limited.json", encoded)
}

// GET /api/v1/accounts: somebody in a game on Steam with a Hytale name, who is
// streaming that very game; somebody whose Steam profile is private, who reads
// as offline; somebody in a game with nothing on the air ("playing"); and
// somebody connected to nothing whose own stream names its game, who is there
// for the activity line alone; and somebody streaming a movie picked in What's
// on?, whose line is the movie.
func TestContractAccounts(t *testing.T) {
	s := newAccountsScene(t)
	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3})
	s.player(steamPlayer{SteamID: erinSteamID, Name: "Erin", State: 1, Visibility: 1})
	s.player(steamPlayer{SteamID: bossSteamID, Name: "Boss", State: 3, Visibility: 3})
	s.connectSteam(t, acDave, daveSteamID)
	s.connectSteam(t, acErin, erinSteamID)
	s.connectSteam(t, acBoss, bossSteamID)
	s.putLinks(t, hytaleProvider, &accountLinks{Users: map[string]accountLink{acDave: {ID: daveHytaleUUID, Name: "Steve_Hytale", At: 1}}})

	store := s.p.accounts()
	store.lock.Lock()
	store.steam[daveSteamID] = steamStatusOf(steamPlayer{SteamID: daveSteamID, State: 1, Visibility: 3, GameName: "ELDEN RING", GameID: json.RawMessage(`"1245620"`)})
	store.steam[erinSteamID] = steamStatusOf(steamPlayer{SteamID: erinSteamID, State: 1, Visibility: 1})
	store.steam[bossSteamID] = steamStatusOf(steamPlayer{SteamID: bossSteamID, State: 3, Visibility: 3, GameName: "Factorio", GameID: json.RawMessage(`427520`)})
	store.steamLastGood = s.now()
	store.lock.Unlock()

	// On the air: Dave, with the game he is in picked from Steam in What's
	// on?, Bob, connected to nothing, on Twitch, and Erin with a movie picked.
	store.noteOnAir(1, []apiStream{
		{Handle: "h-dave", UserID: acDave, LinkSource: linkSourceManual, NowPlaying: &apiNowPlaying{
			Category: "game", Title: "Elden Ring", Media: &scheduleMedia{Ref: "steam:1245620", Kind: "game", Title: "Elden Ring"},
		}},
		{Handle: "t-bob", UserID: acBob, Source: "twitch", LinkSource: linkSourceTwitch, TwitchCategory: "Hades"},
		{Handle: "h-erin", UserID: acErin, LinkSource: linkSourceManual, NowPlaying: &apiNowPlaying{
			Category: "movie", Title: "Quantum of Solace", Media: &scheduleMedia{Ref: "tmdb:movie:10764", Kind: "movie", Title: "Quantum of Solace"},
		}},
	})

	code, body := s.accountsAnswer(t, acLetIn)
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, string(body), "ELDEN RING")
	require.Contains(t, string(body), `"activity":{"kind":"streaming","title":"ELDEN RING","source":"steam"}`)
	require.Contains(t, string(body), `"activity":{"kind":"playing","title":"Factorio","source":"steam"}`)
	require.Contains(t, string(body), `"`+acBob+`":{"activity":{"kind":"streaming","title":"Hades","source":"twitch"}}`)
	require.Contains(t, string(body), `"activity":{"kind":"streaming","title":"Quantum of Solace","source":"panel"}`)

	contractFixture(t, "accounts.json", body)
}

// The clips a reader who may watch is given: the list's and the viewer's one
// clip (GET /api/v1/clips/clip), the cards of the clips shared into a channel
// (GET /api/v1/clips/cards), one still there and one gone, and what a clip
// that has gone answers with.
func TestContractClips(t *testing.T) {
	w, _ := startedClipsWorld(t, "uid-bob", "uid-dave", "uid-dan")
	clipsAdmins(w.api)

	votes := votesAt(clipsNow.Add(-time.Minute), "uid-dave", "uid-dan")
	publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), votes), true)

	clip := w.call(t, "uid-dan", clipCall{method: http.MethodGet, target: clipsPath + "/clip?id=" + clipA})
	require.Equal(t, http.StatusOK, clip.Code, clip.Body.String())
	contractFixture(t, "clip.json", clip.Body.Bytes())

	cards := w.call(t, "uid-dan", clipCall{method: http.MethodGet, target: clipsPath + "/cards?ids=" + clipA + "," + clipB})
	require.Equal(t, http.StatusOK, cards.Code, cards.Body.String())
	require.Contains(t, cards.Body.String(), `"`+clipB+`":null`, "a clip that has gone is null, not left out")
	contractFixture(t, "clip-cards.json", cards.Body.Bytes())

	gone := w.call(t, "uid-dan", clipCall{method: http.MethodGet, target: clipsPath + "/clip?id=" + clipB})
	require.Equal(t, http.StatusGone, gone.Code)
	contractFixture(t, "refusal-clip-gone.json", gone.Body.Bytes())
}
