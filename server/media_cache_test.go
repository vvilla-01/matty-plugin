package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/require"
)

// The remembered titles are a cache that outlives a restart, so these tests
// restart a lot: a fresh Plugin, with nothing in memory, on the same KV store.
// Time is a clock the test moves; memory keeps real time, and no test here
// waits for memory to expire — a restart is how memory is emptied.

const day = 24 * time.Hour

// restartAt is the server restarted, with its clock set to the shared one.
func restartAt(t *testing.T, p *Plugin, clock *fakeClock) *Plugin {
	t.Helper()

	next := restart(t, p)
	useClock(next, clock)

	return next
}

func detailsOf(t *testing.T, p *Plugin, ref string) (*mediaDetails, error) {
	t.Helper()

	parsed, ok := parseMediaRef(ref)
	require.True(t, ok, ref)

	return p.mediaDetailsFor(t.Context(), p.getConfiguration(), parsed)
}

// keptEntry is one remembered entry as KV holds it, read under the store's
// lock.
func keptEntry(t *testing.T, faults *kvFaults, store map[string][]byte, entryKey string) (*mediaKept, bool) {
	t.Helper()

	faults.lock.Lock()
	raw := append([]byte(nil), store[mediaShardKey(entryKey)]...)
	faults.lock.Unlock()

	if len(raw) == 0 {
		return nil, false
	}

	var shard mediaShard
	require.NoError(t, json.Unmarshal(raw, &shard))
	kept, found := shard.Entries[entryKey]

	return kept, found
}

func heatSearch(fake *mediaFake) {
	fake.json("/3/search/multi", map[string]any{"results": []map[string]any{
		{"media_type": "movie", "id": 949, "title": "Heat", "release_date": "1995-12-15", "poster_path": "/heat.jpg"},
	}})
}

func TestARememberedSearchNeedsNoProviderAfterARestart(t *testing.T) {
	fake := newMediaFake(t)
	heatSearch(fake)
	fake.bytes("/t/p/w92/heat.jpg", jpegBytes)

	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	first := mustSearch(t, p, "movie", "heat")
	asked := len(fake.requests())

	q := restart(t, p)
	again := mustSearch(t, q, "movie", "heat")
	require.Equal(t, first, again)
	require.Len(t, fake.requests(), asked, "not asked again after a restart")

	// The remembered search carried its art, so the thumbnail needs only the
	// picture.
	require.Equal(t, http.StatusOK, mediaPictureOf(t, q, "tmdb:movie:949", "thumb").Code)
	require.Zero(t, fake.requestsTo("/3/movie/949"))
	require.Equal(t, 1, fake.requestsTo("/t/p/w92/heat.jpg"))
}

func TestARememberedTitleNeedsOnlyItsPicturesAfterARestart(t *testing.T) {
	fake := newMediaFake(t)
	fake.json("/api/appdetails", eldenRingDetails(fake))
	fake.bytes("/steamcdn/apps/1245620/f0b19c231f86fa0e633ff88a1a63459443017728/header_alt_assets_3.jpg", jpegBytes)

	p, _, _ := mediaPluginWithFaults(t, &configuration{MediaLookup: true})
	response := planStream(t, p, map[string]any{"title": "Elden Ring", "category": "game", "media": "steam:1245620"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 1, fake.requestsTo("/api/appdetails"))

	q := restart(t, p)
	before := len(fake.requests())

	response = mediaPictureOf(t, q, "steam:1245620", "backdrop")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 1, fake.requestsTo("/api/appdetails"), "the details were remembered")
	require.Len(t, fake.requests(), before+1, "one request: the picture")
}

func TestRememberedEntriesGoStale(t *testing.T) {
	type step struct {
		at    time.Duration
		asked int
	}

	for _, tried := range []struct {
		name  string
		setup func(fake *mediaFake)
		cfg   func() *configuration
		do    func(t *testing.T, p *Plugin)
		count func(fake *mediaFake) int
		steps []step
	}{
		{
			name:  "a movie search",
			setup: heatSearch,
			cfg:   movieConfig,
			do:    func(t *testing.T, p *Plugin) { mustSearch(t, p, "movie", "heat") },
			count: func(fake *mediaFake) int { return fake.requestsTo("/3/search/multi") },
			steps: []step{{6*day + 23*time.Hour, 1}, {7*day + time.Hour, 2}},
		},
		{
			name:  "a search with nothing found",
			setup: func(fake *mediaFake) { fake.json("/3/search/multi", map[string]any{"results": []any{}}) },
			cfg:   movieConfig,
			do:    func(t *testing.T, p *Plugin) { mustSearch(t, p, "movie", "zzzz") },
			count: func(fake *mediaFake) int { return fake.requestsTo("/3/search/multi") },
			steps: []step{{23 * time.Hour, 1}, {25 * time.Hour, 2}},
		},
		{
			name: "an IGDB search",
			setup: func(fake *mediaFake) {
				steamItems(fake, steamRow(42, "Hytale Modding Kit"))
				fake.igdbToken("tok", 5000000)
				fake.igdbGames(igdbCatalogue(hytaleRow()))
			},
			cfg:   twitchConfig,
			do:    func(t *testing.T, p *Plugin) { mustSearch(t, p, "game", "hytale") },
			count: gamesPosts,
			steps: []step{{2*day + 23*time.Hour, 1}, {3*day + time.Hour, 2}},
		},
		{
			name: "a settled movie",
			setup: func(fake *mediaFake) {
				fake.json("/3/movie/949", heatDetails())
			},
			cfg:   movieConfig,
			do:    func(t *testing.T, p *Plugin) { _, err := detailsOf(t, p, "tmdb:movie:949"); require.NoError(t, err) },
			count: func(fake *mediaFake) int { return fake.requestsTo("/3/movie/949") },
			steps: []step{{29 * day, 1}, {31 * day, 2}},
		},
		{
			name: "a movie not out yet",
			setup: func(fake *mediaFake) {
				fake.json("/3/movie/950", map[string]any{"title": "Heat 2", "status": "Post Production", "release_date": "2028-08-01"})
			},
			cfg:   movieConfig,
			do:    func(t *testing.T, p *Plugin) { _, err := detailsOf(t, p, "tmdb:movie:950"); require.NoError(t, err) },
			count: func(fake *mediaFake) int { return fake.requestsTo("/3/movie/950") },
			steps: []step{{2*day + 23*time.Hour, 1}, {3*day + time.Hour, 2}},
		},
		{
			name: "a show still running",
			setup: func(fake *mediaFake) {
				fake.json("/3/tv/1396", map[string]any{"name": "Breaking Bad", "status": "Returning Series", "first_air_date": "2008-01-20"})
			},
			cfg:   movieConfig,
			do:    func(t *testing.T, p *Plugin) { _, err := detailsOf(t, p, "tmdb:tv:1396"); require.NoError(t, err) },
			count: func(fake *mediaFake) int { return fake.requestsTo("/3/tv/1396") },
			steps: []step{{6*day + 23*time.Hour, 1}, {7*day + time.Hour, 2}},
		},
		{
			name: "a game in early access",
			setup: func(fake *mediaFake) {
				fake.igdbToken("tok", 5000000)
				fake.igdbGames(igdbCatalogue(vintageStoryRow()))
			},
			cfg:   twitchConfig,
			do:    func(t *testing.T, p *Plugin) { _, err := detailsOf(t, p, "igdb:69547"); require.NoError(t, err) },
			count: gamesPosts,
			steps: []step{{2*day + 23*time.Hour, 1}, {3*day + time.Hour, 2}},
		},
		{
			name: "an IGDB game out already",
			setup: func(fake *mediaFake) {
				fake.igdbToken("tok", 5000000)
				fake.igdbGames(igdbCatalogue(hytaleRow()))
			},
			cfg:   twitchConfig,
			do:    func(t *testing.T, p *Plugin) { _, err := detailsOf(t, p, "igdb:113360"); require.NoError(t, err) },
			count: gamesPosts,
			steps: []step{{6*day + 23*time.Hour, 1}, {7*day + time.Hour, 2}},
		},
		{
			name:  "no such title",
			setup: func(fake *mediaFake) { fake.status("/3/movie/404", http.StatusNotFound) },
			cfg:   movieConfig,
			do: func(t *testing.T, p *Plugin) {
				_, err := detailsOf(t, p, "tmdb:movie:404")
				require.ErrorIs(t, err, errMediaNotFound)
			},
			count: func(fake *mediaFake) int { return fake.requestsTo("/3/movie/404") },
			steps: []step{{23 * time.Hour, 1}, {25 * time.Hour, 2}},
		},
	} {
		t.Run(tried.name, func(t *testing.T) {
			fake := newMediaFake(t)
			tried.setup(fake)

			p, _, _ := mediaPluginWithFaults(t, tried.cfg())
			clock := newFakeClock()
			useClock(p, clock)
			start := clock.now()

			tried.do(t, p)
			require.Equal(t, 1, tried.count(fake))

			for _, at := range tried.steps {
				clock.advance(start.Add(at.at).Sub(clock.now()))
				p = restartAt(t, p, clock)

				tried.do(t, p)
				require.Equal(t, at.asked, tried.count(fake), "at +%s", at.at)
			}
		})
	}
}

func TestAStaleEntryIsServedWhenTheProviderFails(t *testing.T) {
	fake := newMediaFake(t)
	heatSearch(fake)
	fake.json("/3/movie/949", heatDetails())

	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	clock := newFakeClock()
	useClock(p, clock)

	first := mustSearch(t, p, "movie", "heat")
	_, err := detailsOf(t, p, "tmdb:movie:949")
	require.NoError(t, err)

	// Past its time, and TMDB down: what it said before, rather than nothing.
	fake.status("/3/search/multi", http.StatusInternalServerError)
	fake.status("/3/movie/949", http.StatusInternalServerError)
	clock.advance(31 * day)
	p = restartAt(t, p, clock)

	require.Equal(t, first, mustSearch(t, p, "movie", "heat"))
	details, err := detailsOf(t, p, "tmdb:movie:949")
	require.NoError(t, err)
	require.Equal(t, "Heat", details.info.Title)
	require.Equal(t, 2, fake.requestsTo("/3/search/multi"), "the provider was asked first")

	// But never past ninety days.
	clock.advance(60 * day)
	p = restartAt(t, p, clock)

	require.Equal(t, http.StatusBadGateway, mediaSearch(t, p, "movie", "heat").Code)
	_, err = detailsOf(t, p, "tmdb:movie:949")
	require.Error(t, err)
	require.NotErrorIs(t, err, errMediaNotFound)
}

// IGDB's data is kept 7 days for a title and 3 for a search, as Victor
// answered, and those are hard limits: past them an IGDB entry does not stand
// in for an IGDB that is down, as a TMDB or Steam entry does until ninety
// days. Within them it is served as usual.
func TestIGDBDataIsNeverServedPastItsDays(t *testing.T) {
	fake := newMediaFake(t)
	steamItems(fake, steamRow(42, "Hytale Modding Kit"))
	fake.igdbToken("tok", 5000000)
	fake.igdbGames(igdbCatalogue(hytaleRow()))

	p, store, faults := mediaPluginWithFaults(t, twitchConfig())
	clock := newFakeClock()
	useClock(p, clock)

	hasHytale := func(found mediaSearchResponse) bool {
		for _, result := range found.Results {
			if result.Ref == "igdb:113360" {
				return true
			}
		}

		return false
	}

	require.True(t, hasHytale(mustSearch(t, p, "game", "hytale")))
	_, err := detailsOf(t, p, "igdb:113360")
	require.NoError(t, err)
	asked := gamesPosts(fake)

	// IGDB goes down. Two days on, both are still believed, and served
	// without asking it.
	fake.status("/v4/games", http.StatusInternalServerError)
	clock.advance(2 * day)
	p = restartAt(t, p, clock)
	require.True(t, hasHytale(mustSearch(t, p, "game", "hytale")))
	_, err = detailsOf(t, p, "igdb:113360")
	require.NoError(t, err)
	require.Equal(t, asked, gamesPosts(fake), "believed, so IGDB is not asked")

	// Four days on, the search is past its three: IGDB is asked, fails, and
	// the old answer does not stand in. The title still has three days.
	clock.advance(2 * day)
	p = restartAt(t, p, clock)
	found := mustSearch(t, p, "game", "hytale")
	require.False(t, hasHytale(found), "an IGDB search past its three days is not served")
	require.Equal(t, "IGDB", found.Incomplete)
	_, err = detailsOf(t, p, "igdb:113360")
	require.NoError(t, err)

	// Eight days on, the title is past its seven: not served either, and
	// the answer is "could not find out", not "no such title".
	clock.advance(4 * day)
	p = restartAt(t, p, clock)
	_, err = detailsOf(t, p, "igdb:113360")
	require.Error(t, err)
	require.NotErrorIs(t, err, errMediaNotFound)
	require.NotEqual(t, http.StatusOK, mediaPictureOf(t, p, "igdb:113360", "cover").Code, "no picture from details nobody may serve")

	// The entries are still in their shards until a shard is next written,
	// and then they go: a write there drops an IGDB entry past its time
	// whatever else it keeps.
	hytale, parsed := parseMediaRef("igdb:113360")
	require.True(t, parsed)
	_, kept := keptEntry(t, faults, store, mediaTitleKey(hytale))
	require.True(t, kept, "only a write prunes")

	stale := &mediaShard{Version: mediaCacheVersion, Entries: map[string]*mediaKept{
		"t:igdb:1":     {Type: mediaKeptTitle, At: clock.now().Add(-8 * day).UnixMilli(), Until: clock.now().Add(-day).UnixMilli()},
		"t:igdb:2":     {Type: mediaKeptTitle, At: clock.now().Add(-6 * day).UnixMilli(), Until: clock.now().Add(day).UnixMilli()},
		"s:igdb:abcd":  {Type: mediaKeptSearch, At: clock.now().Add(-4 * day).UnixMilli(), Until: clock.now().Add(-day).UnixMilli()},
		"t:steam:1":    {Type: mediaKeptTitle, At: clock.now().Add(-40 * day).UnixMilli(), Until: clock.now().Add(-10 * day).UnixMilli()},
		"s:steam:abcd": {Type: mediaKeptSearch, At: clock.now().Add(-91 * day).UnixMilli(), Until: clock.now().Add(-84 * day).UnixMilli()},
	}}
	_, ok := pruneMediaShard(stale, clock.now())
	require.True(t, ok)
	left := []string{}
	for key := range stale.Entries {
		left = append(left, key)
	}
	require.ElementsMatch(t, []string{"t:igdb:2", "t:steam:1"}, left,
		"IGDB past its time goes; Steam stays as a stand-in until ninety days")
}

func TestATitleTheProviderNoLongerHasIsForgotten(t *testing.T) {
	t.Run("believed the second time, an hour on", func(t *testing.T) {
		fake := newMediaFake(t)
		fake.json("/3/movie/949", heatDetails())

		p, store, faults := mediaPluginWithFaults(t, movieConfig())
		clock := newFakeClock()
		useClock(p, clock)
		key := mediaTitleKey(mediaRef{provider: mediaProviderTMDB, kind: mediaKindMovie, id: "949"})

		_, err := detailsOf(t, p, "tmdb:movie:949")
		require.NoError(t, err)

		// Stale, and TMDB says there is no such film: still served, and
		// marked.
		fake.status("/3/movie/949", http.StatusNotFound)
		clock.advance(31 * day)
		firstGone := clock.now()
		p = restartAt(t, p, clock)

		details, err := detailsOf(t, p, "tmdb:movie:949")
		require.NoError(t, err)
		require.Equal(t, "Heat", details.info.Title)

		kept, found := keptEntry(t, faults, store, key)
		require.True(t, found)
		require.Equal(t, mediaKeptTitle, kept.Type)
		require.Equal(t, firstGone.UnixMilli(), kept.Gone)

		// Half an hour on: still served, and nothing written.
		writes := faults.writesTo(mediaShardKey(key))
		clock.advance(30 * time.Minute)
		p = restartAt(t, p, clock)

		_, err = detailsOf(t, p, "tmdb:movie:949")
		require.NoError(t, err)
		require.Equal(t, writes, faults.writesTo(mediaShardKey(key)))

		// Over an hour after the first: believed.
		clock.advance(35 * time.Minute)
		p = restartAt(t, p, clock)

		_, err = detailsOf(t, p, "tmdb:movie:949")
		require.ErrorIs(t, err, errMediaNotFound)
		kept, _ = keptEntry(t, faults, store, key)
		require.Equal(t, mediaKeptMissing, kept.Type)

		// For the rest of the day nobody is asked, and a plan saved
		// meanwhile carries no title.
		asked := fake.requestsTo("/3/movie/949")
		clock.advance(time.Hour)
		p = restartAt(t, p, clock)

		_, err = detailsOf(t, p, "tmdb:movie:949")
		require.ErrorIs(t, err, errMediaNotFound)
		response := planStream(t, p, map[string]any{"title": "Film night", "category": "movie", "media": "tmdb:movie:949"})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Nil(t, onlyPlan(t, p).Media)
		require.Equal(t, asked, fake.requestsTo("/3/movie/949"))
	})

	t.Run("an answer in between clears the mark", func(t *testing.T) {
		fake := newMediaFake(t)
		fake.json("/3/movie/949", heatDetails())

		p, store, faults := mediaPluginWithFaults(t, movieConfig())
		clock := newFakeClock()
		useClock(p, clock)
		key := mediaTitleKey(mediaRef{provider: mediaProviderTMDB, kind: mediaKindMovie, id: "949"})

		_, err := detailsOf(t, p, "tmdb:movie:949")
		require.NoError(t, err)

		fake.status("/3/movie/949", http.StatusNotFound)
		clock.advance(31 * day)
		p = restartAt(t, p, clock)
		_, err = detailsOf(t, p, "tmdb:movie:949")
		require.NoError(t, err)

		fake.json("/3/movie/949", heatDetails())
		clock.advance(30 * time.Minute)
		p = restartAt(t, p, clock)
		_, err = detailsOf(t, p, "tmdb:movie:949")
		require.NoError(t, err)

		kept, _ := keptEntry(t, faults, store, key)
		require.Equal(t, mediaKeptTitle, kept.Type)
		require.Zero(t, kept.Gone)
		require.Equal(t, clock.now().UnixMilli(), kept.At)
	})

	t.Run("never remembered", func(t *testing.T) {
		fake := newMediaFake(t)
		fake.status("/3/movie/950", http.StatusNotFound)

		p, store, faults := mediaPluginWithFaults(t, movieConfig())
		useClock(p, newFakeClock())

		_, err := detailsOf(t, p, "tmdb:movie:950")
		require.ErrorIs(t, err, errMediaNotFound)

		kept, found := keptEntry(t, faults, store, mediaTitleKey(mediaRef{provider: mediaProviderTMDB, kind: mediaKindMovie, id: "950"}))
		require.True(t, found)
		require.Equal(t, mediaKeptMissing, kept.Type)
	})
}

// A title the provider has not got is remembered in memory as nil, and a nil
// held there must read as "no such title", never as details.
func TestAMissingTitleIsNotAskedForTwice(t *testing.T) {
	fake := newMediaFake(t)
	fake.status("/3/movie/404", http.StatusNotFound)

	p, _, _ := mediaPluginWithFaults(t, movieConfig())

	for i := 0; i < 2; i++ {
		response := planStream(t, p, map[string]any{
			"title": fmt.Sprintf("Film night %d", i), "category": "movie", "media": "tmdb:movie:404",
			"startsAt": inAnHour() + int64(i)*60000,
		})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}

	items := mustSchedule(t, p, nowMillis())
	require.Len(t, items, 2)
	for _, item := range items {
		require.Nil(t, item.Media)
	}
	require.Equal(t, 1, fake.requestsTo("/3/movie/404"))

	require.Equal(t, http.StatusNotFound, mediaPictureOf(t, p, "tmdb:movie:404", "cover").Code)
	require.Equal(t, 1, fake.requestsTo("/3/movie/404"))
}

func TestOldArtAddressesAreRefreshed(t *testing.T) {
	t.Run("two days on, every address gone", func(t *testing.T) {
		fake := newMediaFake(t)
		fake.json("/3/movie/949", heatDetails())

		p, _, _ := mediaPluginWithFaults(t, movieConfig())
		clock := newFakeClock()
		useClock(p, clock)

		_, err := detailsOf(t, p, "tmdb:movie:949")
		require.NoError(t, err)

		// TMDB has new art, and the old address is gone.
		moved := heatDetails()
		moved["poster_path"] = "/heat-new.jpg"
		fake.json("/3/movie/949", moved)
		fake.bytes("/t/p/w342/heat-new.jpg", jpegBytes)

		clock.advance(2 * day)
		p = restartAt(t, p, clock)

		response := mediaPictureOf(t, p, "tmdb:movie:949", "cover")
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Equal(t, 2, fake.requestsTo("/3/movie/949"), "asked once more")
		require.Equal(t, 1, fake.requestsTo("/t/p/w342/heat.jpg"))
		require.Equal(t, 1, fake.requestsTo("/t/p/w342/heat-new.jpg"))
	})

	t.Run("an hour on", func(t *testing.T) {
		fake := newMediaFake(t)
		fake.json("/3/movie/949", heatDetails())

		p, _, _ := mediaPluginWithFaults(t, movieConfig())
		clock := newFakeClock()
		useClock(p, clock)

		_, err := detailsOf(t, p, "tmdb:movie:949")
		require.NoError(t, err)

		clock.advance(time.Hour)
		p = restartAt(t, p, clock)

		require.Equal(t, http.StatusNotFound, mediaPictureOf(t, p, "tmdb:movie:949", "cover").Code)
		require.Equal(t, 1, fake.requestsTo("/3/movie/949"), "details an hour old are not asked about again")
	})

	t.Run("a title with no such art", func(t *testing.T) {
		fake := newMediaFake(t)
		fake.json("/3/movie/77", map[string]any{"title": "No Art", "status": "Released", "release_date": "1990-01-01"})

		p, _, _ := mediaPluginWithFaults(t, movieConfig())
		clock := newFakeClock()
		useClock(p, clock)

		_, err := detailsOf(t, p, "tmdb:movie:77")
		require.NoError(t, err)

		clock.advance(2 * day)
		p = restartAt(t, p, clock)

		require.Equal(t, http.StatusNotFound, mediaPictureOf(t, p, "tmdb:movie:77", "backdrop").Code)
		require.Equal(t, 1, fake.requestsTo("/3/movie/77"), "nothing old to replace, so nothing asked")
	})
}

// keysInShardZero are n entry keys, each starting with prefix, that all land in
// the first shard.
func keysInShardZero(prefix string, n int) []string {
	keys := make([]string, 0, n)
	for i := 0; len(keys) < n; i++ {
		key := fmt.Sprintf("%s%d", prefix, i)
		if mediaShardKey(key) == kvMediaCachePrefix+"00" {
			keys = append(keys, key)
		}
	}

	return keys
}

func shardZero(t *testing.T, faults *kvFaults, store map[string][]byte) (mediaShard, int) {
	t.Helper()

	faults.lock.Lock()
	raw := append([]byte(nil), store[kvMediaCachePrefix+"00"]...)
	faults.lock.Unlock()

	var shard mediaShard
	require.NoError(t, json.Unmarshal(raw, &shard))

	return shard, len(raw)
}

// seedShardZero writes entries straight into the first shard, as an earlier
// run of the server would have left them.
func seedShardZero(t *testing.T, faults *kvFaults, store map[string][]byte, entries map[string]*mediaKept) {
	t.Helper()

	encoded, err := json.Marshal(mediaShard{Version: mediaCacheVersion, Entries: entries})
	require.NoError(t, err)

	faults.lock.Lock()
	store[kvMediaCachePrefix+"00"] = encoded
	faults.lock.Unlock()
}

func TestTheCacheStaysWithinItsBounds(t *testing.T) {
	t.Run("by count, searches first", func(t *testing.T) {
		p, store, faults := mediaPluginWithFaults(t, movieConfig())
		clock := newFakeClock()
		useClock(p, clock)
		start := clock.now()

		// Used a second apart, in the order written.
		entry := func(kind string, n int) *mediaKept {
			at := start.Add(time.Duration(n) * time.Second).UnixMilli()
			return &mediaKept{Type: kind, At: at, Until: at + day.Milliseconds(), Used: at}
		}
		keep := func(key, kind string) {
			clock.advance(time.Second)
			now := clock.now()
			p.keepMedia(key, &mediaKept{Type: kind, At: now.UnixMilli(), Until: now.Add(day).UnixMilli()})
		}

		titles := keysInShardZero("t:tmdb:movie:", 260)
		searches := keysInShardZero("s:tmdb:", 256)

		// Full: ten titles, then 246 searches.
		seeded := map[string]*mediaKept{}
		for i, key := range titles[:10] {
			seeded[key] = entry(mediaKeptTitle, i)
		}
		for i, key := range searches[:246] {
			seeded[key] = entry(mediaKeptSearch, 10+i)
		}
		seedShardZero(t, faults, store, seeded)
		clock.advance(time.Hour)

		// Ten more searches push out the ten least recently used searches,
		// and no title.
		for _, key := range searches[246:] {
			keep(key, mediaKeptSearch)
		}

		shard, _ := shardZero(t, faults, store)
		require.Len(t, shard.Entries, maxMediaShardEntries)
		for _, key := range titles[:10] {
			require.Contains(t, shard.Entries, key, "a search never pushes a title out")
		}
		for _, key := range searches[:10] {
			require.NotContains(t, shard.Entries, key, "the least recently used searches went")
		}
		for _, key := range searches[10:] {
			require.Contains(t, shard.Entries, key)
		}

		// Titles go only once there is no search left to drop, least
		// recently used first: 251 titles, four searches and a marker, then
		// six more titles, cost the searches, the marker and the oldest title.
		seeded = map[string]*mediaKept{}
		for i, key := range titles[:251] {
			seeded[key] = entry(mediaKeptTitle, i)
		}
		for i, key := range searches[:4] {
			seeded[key] = entry(mediaKeptSearch, 1000+i)
		}
		seeded[searches[4]] = &mediaKept{Type: mediaKeptMissing, At: start.UnixMilli(), Until: start.Add(day).UnixMilli(), Used: start.UnixMilli()}
		seedShardZero(t, faults, store, seeded)

		for _, key := range titles[251:257] {
			keep(key, mediaKeptTitle)
		}

		shard, _ = shardZero(t, faults, store)
		require.Len(t, shard.Entries, maxMediaShardEntries)
		for key, kept := range shard.Entries {
			require.Equal(t, mediaKeptTitle, kept.Type, key)
		}
		require.NotContains(t, shard.Entries, titles[0])
		require.Contains(t, shard.Entries, titles[1], "searches, then the marker, then the oldest title")
		require.Contains(t, shard.Entries, titles[256])
	})

	t.Run("by size", func(t *testing.T) {
		p, store, faults := mediaPluginWithFaults(t, movieConfig())
		clock := newFakeClock()
		useClock(p, clock)
		start := clock.now()

		bulky := func(i int) []mediaCachedResult {
			results := make([]mediaCachedResult, 0, maxMediaResults)
			for n := 0; n < maxMediaResults; n++ {
				results = append(results, mediaCachedResult{mediaResult: mediaResult{
					Ref: fmt.Sprintf("tmdb:movie:%d", i*10+n+1), Kind: "movie", Title: strings.Repeat("Long title ", 10), Label: "Movie",
				}, Art: mediaArt{Poster: "/" + strings.Repeat("p", 40) + ".jpg", Backdrop: "/" + strings.Repeat("b", 40) + ".jpg"}})
			}

			return results
		}

		keys := keysInShardZero("s:tmdb:", 70)

		// Nearly full by size, though far from full by count.
		seeded := map[string]*mediaKept{}
		for i, key := range keys[:60] {
			at := start.Add(time.Duration(i) * time.Second).UnixMilli()
			seeded[key] = &mediaKept{Type: mediaKeptSearch, At: at, Until: at + day.Milliseconds(), Used: at, Results: bulky(i)}
		}
		encoded, err := json.Marshal(mediaShard{Version: mediaCacheVersion, Entries: seeded})
		require.NoError(t, err)
		require.Greater(t, len(encoded), maxMediaShardBytes, "the seed alone is over, so the first write must prune")

		seedShardZero(t, faults, store, seeded)
		clock.advance(time.Hour)

		for i, key := range keys[60:] {
			clock.advance(time.Second)
			now := clock.now()
			p.keepMedia(key, &mediaKept{Type: mediaKeptSearch, At: now.UnixMilli(), Until: now.Add(day).UnixMilli(), Results: bulky(60 + i)})
		}

		shard, size := shardZero(t, faults, store)
		require.LessOrEqual(t, size, maxMediaShardBytes)
		require.Less(t, len(shard.Entries), len(keys), "some had to go")
		require.Contains(t, shard.Entries, keys[len(keys)-1], "the newest is kept")
		require.NotContains(t, shard.Entries, keys[0], "the oldest is not")

		// One entry larger than an entry may be is not kept at all.
		huge := keysInShardZero("s:steam:", 1)[0]
		now := clock.now()
		p.keepMedia(huge, &mediaKept{Type: mediaKeptSearch, At: now.UnixMilli(), Until: now.Add(day).UnixMilli(), Results: []mediaCachedResult{
			{mediaResult: mediaResult{Ref: "steam:1", Kind: "game", Title: "Huge"}, Art: mediaArt{Capsule: strings.Repeat("x", maxMediaKeptBytes)}},
		}})
		shard, _ = shardZero(t, faults, store)
		require.NotContains(t, shard.Entries, huge)
	})

	t.Run("short searches stay in memory", func(t *testing.T) {
		fake := newMediaFake(t)
		fake.json("/3/search/multi", map[string]any{"results": []map[string]any{{"media_type": "movie", "id": 1, "title": "Up"}}})

		p, _, faults := mediaPluginWithFaults(t, movieConfig())
		var written atomic.Int32
		faults.setOnWrite(func(key string) {
			if strings.HasPrefix(key, kvMediaCachePrefix) {
				written.Add(1)
			}
		})

		mustSearch(t, p, "movie", "up")
		require.Zero(t, written.Load(), "two letters are typing on the way to something")

		mustSearch(t, p, "movie", "upx")
		require.Positive(t, written.Load())
	})

	t.Run("ninety days is the most", func(t *testing.T) {
		p, store, faults := mediaPluginWithFaults(t, movieConfig())
		clock := newFakeClock()
		useClock(p, clock)

		keys := keysInShardZero("t:tmdb:movie:", 2)
		now := clock.now()
		p.keepMedia(keys[0], &mediaKept{Type: mediaKeptTitle, At: now.UnixMilli(), Until: now.Add(30 * day).UnixMilli()})

		clock.advance(91 * day)
		now = clock.now()
		p.keepMedia(keys[1], &mediaKept{Type: mediaKeptTitle, At: now.UnixMilli(), Until: now.Add(30 * day).UnixMilli()})

		shard, _ := shardZero(t, faults, store)
		require.NotContains(t, shard.Entries, keys[0], "dropped when its shard was next written")
		require.Contains(t, shard.Entries, keys[1])
	})
}

func TestATouchNeverBringsAnythingBack(t *testing.T) {
	// Planned at the start, and served from KV four days later, when it is
	// due to be marked as used. readsBeforeTouch is the reads of its shard up
	// to and including the one the touch makes.
	scene := func(t *testing.T) (*Plugin, map[string][]byte, *kvFaults, *fakeClock, string) {
		fake := newMediaFake(t)
		fake.json("/3/movie/949", heatDetails())

		p, store, faults := mediaPluginWithFaults(t, movieConfig())
		clock := newFakeClock()
		useClock(p, clock)

		_, err := detailsOf(t, p, "tmdb:movie:949")
		require.NoError(t, err)

		clock.advance(4 * day)
		p = restartAt(t, p, clock)

		return p, store, faults, clock, mediaTitleKey(mediaRef{provider: mediaProviderTMDB, kind: mediaKindMovie, id: "949"})
	}

	// onSecondRead runs change on the store just before the second read of
	// the title's shard answers: after the read that served the title, before
	// the touch's own read.
	onSecondRead := func(faults *kvFaults, shard string, change func()) {
		reads := 0
		faults.setOnRead(func(key string) {
			if key != shard {
				return
			}
			reads++
			if reads == 2 {
				change()
			}
		})
	}

	t.Run("forgotten meanwhile", func(t *testing.T) {
		p, store, faults, _, key := scene(t)
		shard := mediaShardKey(key)

		onSecondRead(faults, shard, func() { store[shard] = []byte(emptyMediaShard) })

		_, err := detailsOf(t, p, "tmdb:movie:949")
		require.NoError(t, err)
		faults.setOnRead(nil)

		faults.lock.Lock()
		defer faults.lock.Unlock()
		require.Equal(t, emptyMediaShard, string(store[shard]), "the touch did not put it back")
	})

	t.Run("refreshed by another node meanwhile", func(t *testing.T) {
		p, store, faults, _, key := scene(t)
		shard := mediaShardKey(key)

		var rewritten []byte
		onSecondRead(faults, shard, func() {
			var doc mediaShard
			require.NoError(t, json.Unmarshal(store[shard], &doc))
			doc.Entries[key].At++
			doc.Entries[key].Used = 1
			rewritten, _ = json.Marshal(doc)
			store[shard] = rewritten
		})

		_, err := detailsOf(t, p, "tmdb:movie:949")
		require.NoError(t, err)
		faults.setOnRead(nil)

		faults.lock.Lock()
		defer faults.lock.Unlock()
		require.Equal(t, string(rewritten), string(store[shard]), "the other node's entry is left exactly as it wrote it")
	})

	t.Run("unchanged", func(t *testing.T) {
		p, store, faults, clock, key := scene(t)

		before, found := keptEntry(t, faults, store, key)
		require.True(t, found)

		_, err := detailsOf(t, p, "tmdb:movie:949")
		require.NoError(t, err)

		after, found := keptEntry(t, faults, store, key)
		require.True(t, found)
		require.Equal(t, clock.now().UnixMilli(), after.Used)

		after.Used = before.Used
		require.Equal(t, before, after, "only Used changed")
	})
}

func TestNothingTypedIsStoredInTheClear(t *testing.T) {
	fake := newMediaFake(t)
	// Art paths that do not happen to spell the query, so any "heat" found
	// below came from what was typed.
	fake.json("/3/search/multi", map[string]any{"results": []map[string]any{
		{"media_type": "movie", "id": 949, "title": "Heat", "release_date": "1995-12-15", "poster_path": "/a1b2c3.jpg"},
	}})
	steamItems(fake)
	fake.igdbToken("tok", 5000000)
	fake.igdbGames(igdbCatalogue(vintageStoryRow()))

	cfg := twitchConfig()
	cfg.TMDBToken = testTMDBToken
	p, store, faults := mediaPluginWithFaults(t, cfg)

	var written []string
	var lock sync.Mutex
	faults.setOnWrite(func(key string) {
		lock.Lock()
		defer lock.Unlock()
		written = append(written, key)
	})

	mustSearch(t, p, "game", "vintage story")
	mustSearch(t, p, "movie", "heat")

	lock.Lock()
	require.NotEmpty(t, written)
	for _, key := range written {
		require.Regexp(t, `^mediacache_0[0-9a-f]$`, key)
	}
	lock.Unlock()

	faults.lock.Lock()
	defer faults.lock.Unlock()

	searches := 0
	for key, raw := range store {
		if !strings.HasPrefix(key, kvMediaCachePrefix) {
			continue
		}

		var shard mediaShard
		require.NoError(t, json.Unmarshal(raw, &shard))
		for entryKey, kept := range shard.Entries {
			for _, typed := range []string{"vintage", "heat"} {
				require.NotContains(t, strings.ToLower(entryKey), typed)
			}
			if kept.Type != mediaKeptSearch {
				continue
			}
			searches++

			// The results' titles are the providers' words, not anything
			// typed; everything else must not carry the query.
			for i := range kept.Results {
				kept.Results[i].Title = ""
			}
			encoded, err := json.Marshal(kept)
			require.NoError(t, err)
			for _, typed := range []string{"vintage", "heat"} {
				require.NotContains(t, strings.ToLower(string(encoded)), typed)
			}
		}
	}
	require.Equal(t, 3, searches, "Steam's and IGDB's for the game, TMDB's for the film")
}

func TestANewerOrUnreadableShardIsNotWrittenOver(t *testing.T) {
	fake := newMediaFake(t)
	heatSearch(fake)

	shard := mediaShardKey(mediaSearchKey(mediaProviderTMDB, "heat"))

	t.Run("from a newer build", func(t *testing.T) {
		p, store, faults := mediaPluginWithFaults(t, movieConfig())
		newer := `{"v":2,"entries":{"x":{"something":"else"}}}`

		faults.lock.Lock()
		store[shard] = []byte(newer)
		faults.lock.Unlock()

		require.Len(t, mustSearch(t, p, "movie", "heat").Results, 1)

		faults.lock.Lock()
		defer faults.lock.Unlock()
		require.Equal(t, newer, string(store[shard]), "left exactly as the newer build wrote it")
	})

	t.Run("unreadable", func(t *testing.T) {
		p, store, faults := mediaPluginWithFaults(t, movieConfig())
		garbage := func() {
			faults.lock.Lock()
			store[shard] = []byte("this is not a shard")
			faults.lock.Unlock()
		}

		garbage()
		_, found := p.recallMedia(mediaSearchKey(mediaProviderTMDB, "heat"))
		require.False(t, found)

		faults.lock.Lock()
		require.Equal(t, emptyMediaShard, string(store[shard]), "emptied, and still there")
		faults.lock.Unlock()

		garbage()
		require.Len(t, mustSearch(t, p, "movie", "heat").Results, 1, "the search is answered anyway")

		require.Equal(t, 1, warnings(p, "could not be read and was emptied"), "twice, told once")
	})
}

func TestTheCacheIsNeitherReadNorWrittenWhileLookupsAreOff(t *testing.T) {
	fake := newMediaFake(t)
	heatSearch(fake)
	fake.json("/3/movie/949", heatDetails())
	steamItems(fake)
	fake.igdbToken("tok", 5000000)
	fake.igdbGames(igdbCatalogue(hytaleRow()))

	on := twitchConfig()
	on.TMDBToken = testTMDBToken
	p, _, faults := mediaPluginWithFaults(t, on)

	var reads, writes atomic.Int32
	faults.setOnRead(func(key string) {
		if strings.HasPrefix(key, kvMediaCachePrefix) {
			reads.Add(1)
		}
	})
	faults.setOnWrite(func(key string) {
		if strings.HasPrefix(key, kvMediaCachePrefix) {
			writes.Add(1)
		}
	})

	// First, that this harness can see a write at all: otherwise the zero
	// below would prove nothing.
	mustSearch(t, p, "movie", "heat")
	mustSearch(t, p, "game", "hytale")
	_, err := detailsOf(t, p, "igdb:113360")
	require.NoError(t, err)
	require.Positive(t, writes.Load(), "the sanity check: lookups on write remembered titles")

	off := *on
	off.MediaLookup = false
	q := restart(t, p)
	q.setConfiguration(&off)
	reads.Store(0)
	writes.Store(0)

	for _, kind := range []string{"movie", "game"} {
		found := mustSearch(t, q, kind, "heat")
		require.NotEmpty(t, found.Unavailable)
	}
	require.Equal(t, http.StatusNotFound, mediaPictureOf(t, q, "tmdb:movie:949", "cover").Code)
	require.Equal(t, http.StatusNotFound, mediaPictureOf(t, q, "igdb:113360", "cover").Code)
	response := planStream(t, q, map[string]any{"title": "Hytale", "category": "game", "media": "igdb:113360"})
	require.Equal(t, http.StatusOK, response.Code)
	response = sayWhatIsOn(t, q, "villa", map[string]any{"category": "movie", "title": "Heat", "media": "tmdb:movie:949"})
	require.Equal(t, http.StatusOK, response.Code)

	require.Zero(t, reads.Load(), "nothing remembered was read")
	require.Zero(t, writes.Load(), "nor written")
}

func TestKVTroubleNeverFailsALookup(t *testing.T) {
	fake := newMediaFake(t)
	heatSearch(fake)
	fake.json("/3/movie/949", heatDetails())
	fake.bytes("/t/p/w342/heat.jpg", jpegBytes)

	p, _, faults := mediaPluginWithFaults(t, movieConfig())
	for _, shard := range mediaShardKeys() {
		faults.failRead(shard)
		faults.failWrite(shard)
	}

	require.Len(t, mustSearch(t, p, "movie", "heat").Results, 1)

	response := planStream(t, p, map[string]any{"title": "Film night", "category": "movie", "media": "tmdb:movie:949"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NotNil(t, onlyPlan(t, p).Media)

	require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "cover").Code)

	// Writes refused too, read fine: still answered.
	q := restart(t, p)
	for _, shard := range mediaShardKeys() {
		faults.healRead(shard)
	}
	require.Len(t, mustSearch(t, q, "movie", "heat").Results, 1)

	require.Equal(t, 1, warnings(p, "The remembered titles could not be read or written just now"), "told once")
	require.LessOrEqual(t, warnings(q, "The remembered titles could not be read or written just now"), 1)
}

func TestNormalisedQueriesShareOneEntry(t *testing.T) {
	fake := newMediaFake(t)
	steamItems(fake, steamRow(1608230, "Vintage Story"))

	p, _, _ := mediaPluginWithFaults(t, &configuration{MediaLookup: true})

	mustSearch(t, p, "game", "Vintage-Story!")
	mustSearch(t, p, "game", "vintage  story")
	require.Equal(t, 1, fake.requestsTo("/api/storesearch/"))
	require.Equal(t, "Vintage-Story!", fake.requests()[0].query.Get("term"), "what is sent is what was typed")

	q := restart(t, p)
	mustSearch(t, q, "game", "VINTAGE STORY")
	require.Equal(t, 1, fake.requestsTo("/api/storesearch/"), "remembered under its normal form")

	mustSearch(t, q, "game", "vintage s")
	require.Equal(t, 2, fake.requestsTo("/api/storesearch/"), "a different search is asked, never narrowed from a longer one")

	require.Equal(t, "assassins creed", mediaQueryKey("  Assassin’s CREED!! "))
	require.Equal(t, "c c", mediaQueryKey("C++: C#"))
}

func adminCache(t *testing.T, p *Plugin, method, userID string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, "/api/v1/admin/media/cache", nil)
	request.Header.Set("Mattermost-User-Id", userID)
	p.ServeHTTP(nil, recorder, request)

	return recorder
}

func TestRememberedTitlesAreForAdminsToSeeAndForget(t *testing.T) {
	fake := newMediaFake(t)
	heatSearch(fake)
	fake.json("/3/movie/949", heatDetails())
	fake.status("/3/movie/404", http.StatusNotFound)
	steamItems(fake, steamRow(1245620, "ELDEN RING"))
	fake.igdbToken("tok", 5000000)
	fake.igdbGames(igdbCatalogue(hytaleRow()))

	cfg := twitchConfig()
	cfg.TMDBToken = testTMDBToken
	p, store, faults := mediaPluginWithFaults(t, cfg, "an-admin")
	clock := newFakeClock()
	useClock(p, clock)

	remember := func() {
		mustSearch(t, p, "movie", "heat")
		mustSearch(t, p, "game", "elden ring")
		mustSearch(t, p, "game", "hytale")
		_, err := detailsOf(t, p, "tmdb:movie:949")
		require.NoError(t, err)
		_, err = detailsOf(t, p, "tmdb:movie:404")
		require.ErrorIs(t, err, errMediaNotFound)
	}
	remember()

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		response := adminCache(t, p, method, "villa")
		require.Equal(t, http.StatusForbidden, response.Code)
		require.Contains(t, response.Body.String(), "Only a system admin can see or forget remembered titles.")
	}
	require.Equal(t, http.StatusMethodNotAllowed, adminCache(t, p, http.MethodPost, "an-admin").Code)

	response := adminCache(t, p, http.MethodGet, "an-admin")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var summary mediaCacheSummary
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &summary))
	require.Equal(t, 1, summary.Titles)
	require.Equal(t, 5, summary.Searches, "TMDB's heat, and Steam's and IGDB's elden ring and hytale")
	require.Equal(t, 1, summary.Missing)
	require.Equal(t, map[string]int{"tmdb": 3, "steam": 2, "igdb": 2}, summary.ByProvider)
	require.Equal(t, 2097152, summary.LimitBytes)
	require.Positive(t, summary.Bytes)
	require.Equal(t, clock.now().UnixMilli(), summary.Oldest)
	require.Contains(t, response.Body.String(), `"limitBytes":2097152`)
	require.Contains(t, response.Body.String(), `"byProvider":{`)

	faults.lock.Lock()
	existed := map[string]bool{}
	for _, shard := range mediaShardKeys() {
		_, existed[shard] = store[shard]
	}
	faults.lock.Unlock()

	asked := fake.requestsTo("/3/search/multi")
	response = adminCache(t, p, http.MethodDelete, "an-admin")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"ok":true,"forgotten":7,"picturesForgotten":0}`, response.Body.String(), "no pictures on a disk this test never set up")

	faults.lock.Lock()
	for _, shard := range mediaShardKeys() {
		value, present := store[shard]
		require.Equal(t, existed[shard], present, "%s: forgetting neither deletes nor creates", shard)
		if present {
			require.Equal(t, emptyMediaShard, string(value), shard)
		}
	}
	faults.lock.Unlock()

	api, _ := p.API.(*plugintest.API)
	for _, call := range api.Calls {
		require.NotEqual(t, "KVDelete", call.Method)
		require.NotEqual(t, "KVDeleteAll", call.Method)
	}

	// This node's memory went too.
	mustSearch(t, p, "movie", "heat")
	require.Equal(t, asked+1, fake.requestsTo("/3/search/multi"))

	response = adminCache(t, p, http.MethodGet, "an-admin")
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &summary))
	require.Equal(t, 1, summary.Searches)

	// A shard that cannot be emptied is said to have failed.
	failing := mediaShardKey(mediaSearchKey(mediaProviderTMDB, "heat"))
	faults.failWrite(failing)
	response = adminCache(t, p, http.MethodDelete, "an-admin")
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Contains(t, response.Body.String(), "Some remembered titles could not be forgotten just now. Try again.")
}

func TestSearchesAndDetailsShareOneFetch(t *testing.T) {
	fake := newMediaFake(t)

	searching := newGate(t)
	fake.route("/3/search/multi", func(w http.ResponseWriter, _ *http.Request) {
		searching.Hold()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"media_type":"movie","id":949,"title":"Heat"}]}`))
	})
	looking := newGate(t)
	fake.route("/3/movie/949", func(w http.ResponseWriter, _ *http.Request) {
		looking.Hold()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"title":"Heat","status":"Released","release_date":"1995-12-15"}`))
	})

	p, _, _ := mediaPluginWithFaults(t, movieConfig())

	codes := make([]int, 2)
	var done sync.WaitGroup
	search := func(i int) {
		done.Add(1)
		go func() {
			defer done.Done()
			codes[i] = mediaSearch(t, p, "movie", "heat").Code
		}()
	}

	search(0)
	<-searching.entered
	search(1)
	searching.Release()
	done.Wait()

	require.Equal(t, []int{http.StatusOK, http.StatusOK}, codes)
	require.Equal(t, 1, fake.requestsTo("/3/search/multi"))

	errs := make([]error, 2)
	ask := func(i int) {
		done.Add(1)
		go func() {
			defer done.Done()
			ref, _ := parseMediaRef("tmdb:movie:949")
			_, errs[i] = p.mediaDetailsFor(t.Context(), p.getConfiguration(), ref)
		}()
	}

	ask(0)
	<-looking.entered
	ask(1)
	looking.Release()
	done.Wait()

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	require.Equal(t, 1, fake.requestsTo("/3/movie/949"))
}
