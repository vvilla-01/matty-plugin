package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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

// The golden scene: one server, with everything a snapshot carries that a
// stream list can carry, polled through pollOnce. It touches nothing Twitch
// live added, on purpose: the same file was run against the tree as it stood
// before Twitch live (the pre-twitch-3.1.0 checkpoint) to write
// testdata/twitch-off-snapshot.golden.json, and TestWithoutTwitchTheSnapshotIsByteIdentical
// runs it here, with Twitch credentials set and the switch off, and must get
// the same bytes.
//
// Nothing in it depends on the clock but the month the current board is for:
// the streams started in 2099, so nothing is credited, the airtime is what
// was seeded, and the label of the month is masked before comparing. The
// fixture has no person with two linked stream keys, because the boards by
// person (design/twitch-live.md §7.8) deliberately merge those.

// goldenScene is the scene's plugin, the store under it, every KV key read
// while it was polled, and the origin.
type goldenScene struct {
	p      *Plugin
	api    *plugintest.API
	store  map[string][]byte
	faults *kvFaults
	origin *httptest.Server

	lock  sync.Mutex
	reads []string
}

func (g *goldenScene) readKeys() []string {
	g.lock.Lock()
	defer g.lock.Unlock()

	return append([]string(nil), g.reads...)
}

func newGoldenScene(t *testing.T, cfg *configuration) *goldenScene {
	t.Helper()

	p, store, faults := kvBackedPluginWithFaults(map[string][]byte{})
	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	g := &goldenScene{p: p, api: api, store: store, faults: faults}

	paused := true
	streams := []bbStream{
		{StreamKey: "videokey-dave", MOTD: "Dave plays videokey-dave things", StreamStart: "2099-01-01T00:00:00Z", VideoTracks: []bbVideoTrack{{RID: "Video", Bitrate: 2500000}}, AudioTracks: []bbAudioTrack{{}}},
		{StreamKey: "videokey-bob", MOTD: "Back soon", StreamStart: "2099-01-01T00:00:00Z", VideoTracks: []bbVideoTrack{{RID: "Video"}}, Paused: &paused},
		{StreamKey: "videokey-dan", MOTD: "Welcome to videokey-dan!", StreamStart: "2099-01-01T00:00:00Z", VideoTracks: []bbVideoTrack{{RID: "Video", Bitrate: 900000}}},
		{StreamKey: "videokey-radio", StreamStart: "2099-01-01T00:00:00Z", VideoTracks: []bbVideoTrack{{RID: "Video"}}},
	}
	g.origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(streams)
	}))
	t.Cleanup(g.origin.Close)
	p.httpClient = g.origin.Client()

	seed := func(key string, value any) {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		store[key] = encoded
	}

	store[kvLinkByUserPrefix+"u-dave"] = []byte("videokey-dave")
	store[kvLinkByKeyPrefix+"videokey-dave"] = []byte("u-dave")
	store[kvLinkByUserPrefix+"u-bob"] = []byte("videokey-bob")
	store[kvLinkByKeyPrefix+"videokey-bob"] = []byte("u-bob")

	seed(kvStatsTotals, statTable{
		"videokey-dave": {Seconds: 7200, Title: "Dave"},
		"videokey-bob":  {Seconds: 3600, Title: "Bob"},
		"videokey-dan":  {Seconds: 1800, Title: "Dan"},
	})
	seed(monthBucket(time.Now()), statTable{
		"videokey-bob":  {Seconds: 600, Title: "Bob"},
		"videokey-dave": {Seconds: 300, Title: "Dave"},
		"videokey-dan":  {Seconds: 450, Title: "Dan"},
	})
	seed(kvStatsMonthPrefix+"2026-05", statTable{
		"videokey-dave": {Seconds: 900, Title: "Dave"},
		"videokey-bob":  {Seconds: 100, Title: "Bob"},
		"videokey-dan":  {Seconds: 50, Title: "Dan"},
	})
	seed(kvStatsMonthPrefix+"2026-06", statTable{
		"videokey-bob":  {Seconds: 500, Title: "Bob"},
		"videokey-dave": {Seconds: 400, Title: "Dave"},
	})
	// The 24/7 channel's picture, uploaded as an admin uploads one: nothing is
	// built in.
	radioPicture, err := os.ReadFile("testdata/banner-square.png")
	require.NoError(t, err)
	seed(channelPictureKey("videokey-radio"), storedBrand{ContentType: "image/png", Data: radioPicture})

	seed(kvNowPlaying, nowPlayingDoc{Version: nowPlayingVersion, Entries: map[string]nowPlayingEntry{
		"u-dave": {Category: "game", Title: "ELDEN RING", SetAt: 1, Aired: true},
	}})

	users := map[string]*model.User{
		"u-dave": {Id: "u-dave", Username: "dave", Nickname: "Dave"},
		"u-bob":  {Id: "u-bob", Username: "bob", FirstName: "Bob", LastName: "Turner"},
		"u-erin": {Id: "u-erin", Username: "erin", Nickname: "Erin"},
	}
	api.On("GetUser", mock.Anything).Return(func(userID string) (*model.User, *model.AppError) {
		if user := users[userID]; user != nil {
			copied := *user
			return &copied, nil
		}

		return nil, model.NewAppError("GetUser", "app.user.missing", nil, "", http.StatusNotFound)
	})
	api.On("GetUserByUsername", mock.Anything).Return(nil, model.NewAppError("GetUserByUsername", "app.user.missing", nil, "", http.StatusNotFound)).Maybe()
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

	cfg.BroadcastBoxURL = g.origin.URL
	cfg.EnableLeaderboard = true
	cfg.AlwaysOnStreamKeys = "videokey-radio"
	cfg.StreamNameOverrides = "videokey-dan = Dan's stream\nvideokey-radio = Lo-fi radio"
	cfg.StreamNotes = "videokey-radio = Music around the clock."
	cfg.LivePreviews = true
	cfg.OfflineGraceChecks = 2
	cfg.normalize()
	p.setConfiguration(cfg)

	faults.setOnRead(func(key string) {
		g.lock.Lock()
		defer g.lock.Unlock()

		g.reads = append(g.reads, key)
	})

	return g
}

// goldenJSON is what the last publish stored, as every client would receive
// it: the snapshot, and beside it the boards, which were part of it until
// 3.6.0 and are their own document since (boards.go). The poll's time is
// zeroed, and the label of the month and the boards' version, which is made
// from it among other things, are masked.
// requireGolden holds a snapshot to the one kept in a file, byte for byte.
//
// The files are the record of what a snapshot is with the newer features
// off, and the tests that use them are what stops a change from quietly
// altering what every client is sent. When a change is meant to (3.5.0 added
// the people index, which every snapshot with anybody live or in voice now
// carries), they are written afresh, on purpose and by name:
//
//	VILLANET_UPDATE_GOLDEN=1 go test ./server/ -run 'Snapshot|NobodyChose'
//
// and the difference is read in review like any other.
func requireGolden(t *testing.T, path string, got []byte) {
	t.Helper()

	if os.Getenv("VILLANET_UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.WriteFile(path, got, 0o644))

		return
	}

	golden, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(golden), string(got), "the snapshot is no longer what %s says it is", path)
}

func goldenJSON(t *testing.T, p *Plugin) []byte {
	t.Helper()

	shown := *p.getSnapshot()
	shown.FetchedAt = 0

	boards := p.boardsNow()
	maskBoards(&shown, boards)

	encoded, err := json.MarshalIndent(map[string]any{"snapshot": shown, "boards": boards}, "", "  ")
	require.NoError(t, err)
	require.False(t, strings.Contains(string(encoded), "videokey-dave"), "never a stream key")

	return append(encoded, '\n')
}

// maskBoards takes out of a snapshot and its boards what moves with the
// calendar and says nothing about the code: the name of the month in
// progress, wherever it is written, and the version, a fingerprint of the
// boards with that name in them.
func maskBoards(shown *snapshot, boards *boardsView) {
	if shown.Boards != nil {
		shown.Boards = &boardsRef{Version: "VERSION"}
	}
	boards.Version = "VERSION"

	if boards.MonthLabel != "" {
		boards.MonthLabel = "MONTH"
	}
	for _, person := range boards.People {
		for i := range person.Rows {
			if person.Rows[i].Kind == rowBoard && person.Rows[i].ID == awardMonth {
				person.Rows[i].Board = "MONTH"
			}
		}
	}
}

// boardsNow is the boards as the last publish stored them, decoded.
func (p *Plugin) boardsNow() *boardsView {
	p.snapshotLock.RLock()
	body := p.boardsJSON
	p.snapshotLock.RUnlock()

	view := &boardsView{People: map[string]boardsPerson{}}
	if body != nil {
		_ = json.Unmarshal(body, view)
	}

	return view
}
