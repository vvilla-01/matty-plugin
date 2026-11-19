package main

import (
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/require"
)

// IGDB fills in the games Steam does not have. These tests hold it to that:
// Steam first, IGDB after, nothing twice; never asked without the admin's
// Twitch application or with lookups off; kept to its own limits; and nothing
// a person types can reach its query language. IGDB, Twitch and their image
// host are the media fake.

// The two games Victor named, as igdb.com describes them. The game type's text
// is a stand-in (IGDB documents only the ids), so the id-only shape is tested
// too.
func hytaleRow() map[string]any {
	return map[string]any{
		"id": 113360, "name": "Hytale", "slug": "hytale", "url": "https://www.igdb.com/games/hytale",
		"first_release_date": 1768262400,
		"summary":            "A block game of exploration and creation.",
		"cover":              map[string]any{"id": 1, "image_id": "cobc4t"},
		"game_type":          map[string]any{"id": 0, "type": "Main Game"},
		"genres":             []map[string]any{{"id": 12, "name": "Role-playing (RPG)"}, {"id": 13, "name": "Simulator"}},
		"involved_companies": []map[string]any{{"id": 9, "developer": true, "company": map[string]any{"id": 8, "name": "Hypixel Studios"}}},
		"websites":           []map[string]any{{"id": 1, "type": 1, "url": "https://hytale.com"}},
	}
}

func vintageStoryRow() map[string]any {
	return map[string]any{
		"id": 69547, "name": "Vintage Story", "slug": "vintage-story", "url": "https://www.igdb.com/games/vintage-story",
		"first_release_date": 1534377600,
		"summary":            "An uncompromising wilderness survival sandbox game.",
		"cover":              map[string]any{"id": 2, "image_id": "co2j6g"},
		"genres":             []map[string]any{{"id": 32, "name": "Indie"}},
		"game_status":        map[string]any{"id": 4, "status": "Early Access"},
		"involved_companies": []map[string]any{{"id": 3, "developer": true, "company": map[string]any{"id": 4, "name": "Anego Studios"}}},
		"websites":           []map[string]any{{"id": 5, "type": 15, "url": "https://tyronx.itch.io/vintage-story"}},
	}
}

var (
	igdbSearchedPattern = regexp.MustCompile(`^search "([^"]*)";`)
	igdbIDPattern       = regexp.MustCompile(`where id = ([0-9]+);`)
)

// igdbSearchedFor is the text an IGDB query searched for, or "".
func igdbSearchedFor(query string) string {
	if match := igdbSearchedPattern.FindStringSubmatch(query); match != nil {
		return match[1]
	}

	return ""
}

// igdbIDOf is the id an IGDB query asked for, or 0.
func igdbIDOf(query string) int {
	if match := igdbIDPattern.FindStringSubmatch(query); match != nil {
		id, _ := strconv.Atoi(match[1])
		return id
	}

	return 0
}

// igdbCatalogue answers searches by name and lookups by id from rows.
func igdbCatalogue(rows ...map[string]any) func(string) (int, any) {
	return func(query string) (int, any) {
		if id := igdbIDOf(query); id != 0 {
			for _, row := range rows {
				if row["id"] == id {
					return http.StatusOK, []map[string]any{row}
				}
			}

			return http.StatusOK, []any{}
		}

		searched := mediaQueryKey(igdbSearchedFor(query))
		found := []map[string]any{}
		for _, row := range rows {
			name, _ := row["name"].(string)
			if searched != "" && strings.Contains(mediaQueryKey(name), searched) {
				found = append(found, row)
			}
		}

		return http.StatusOK, found
	}
}

func steamItems(fake *mediaFake, items ...map[string]any) {
	if items == nil {
		items = []map[string]any{}
	}
	fake.json("/api/storesearch/", map[string]any{"total": len(items), "items": items})
}

func steamRow(id int, name string) map[string]any {
	return map[string]any{"type": "app", "name": name, "id": id}
}

func gamesPosts(fake *mediaFake) int {
	return fake.requestsTo("/v4/games")
}

func partialWarnings(p *Plugin, naming string) int {
	api, _ := p.API.(*plugintest.API)

	count := 0
	for _, call := range api.Calls {
		line := fmt.Sprint(call.Arguments...)
		if call.Method == "LogWarn" && strings.Contains(line, "answered without one of its sources") && strings.Contains(line, naming) {
			count++
		}
	}

	return count
}

func cached(ref, title, year string) mediaCachedResult {
	return mediaCachedResult{mediaResult: mediaResult{Ref: ref, Kind: "game", Title: title, Year: year, Label: "Game"}}
}

func refsOf(results []mediaResult) []string {
	refs := make([]string, 0, len(results))
	for _, result := range results {
		refs = append(refs, result.Ref)
	}

	return refs
}

func TestHytaleAndVintageStoryComeFromIGDB(t *testing.T) {
	fake := newMediaFake(t)
	steamItems(fake)
	fake.igdbToken("tok-igdb", 5000000)
	fake.igdbGames(igdbCatalogue(hytaleRow(), vintageStoryRow()))

	p, _ := mediaPlugin(t, twitchConfig())

	found := mustSearch(t, p, "game", "hytale")
	require.Equal(t, []mediaResult{{Ref: "igdb:113360", Kind: "game", Title: "Hytale", Year: "2026", Label: "Game"}}, found.Results)

	require.Equal(t, 1, fake.requestsTo("/oauth2/token"))
	require.Equal(t, 1, gamesPosts(fake))

	var games seenRequest
	for _, seen := range fake.requests() {
		if seen.path == "/v4/games" {
			games = seen
		}
	}
	require.Equal(t, http.MethodPost, games.method)
	require.Equal(t, testTwitchClientID, games.clientID)
	require.Equal(t, "Bearer tok-igdb", games.auth)
	require.Equal(t, "text/plain", games.contentType)
	require.Equal(t, "search \"hytale\";\n"+
		"fields name,first_release_date,cover.image_id,game_type.type,parent_game,version_parent,external_games.external_game_source,external_games.uid,external_games.url,websites.type,websites.url;\n"+
		"where version_parent = null & themes != (42) & (game_type = (0,4,8,9,10,11) | game_type = null);\n"+
		"limit 16;", games.body)

	found = mustSearch(t, p, "game", "Vintage Story")
	require.Equal(t, []mediaResult{{Ref: "igdb:69547", Kind: "game", Title: "Vintage Story", Year: "2018", Label: "Game"}}, found.Results)
	require.Equal(t, 1, fake.requestsTo("/oauth2/token"), "the token is kept")
}

func TestGamesComeFromSteamFirstAndIGDBFillsIn(t *testing.T) {
	steamGames := func(n int) []mediaCachedResult {
		games := make([]mediaCachedResult, 0, n)
		for i := 0; i < n; i++ {
			games = append(games, cached(fmt.Sprintf("steam:%d", 100+i), fmt.Sprintf("Steam Game %d", i), ""))
		}

		return games
	}
	igdbGames := func(n int) []mediaCachedResult {
		games := make([]mediaCachedResult, 0, n)
		for i := 0; i < n; i++ {
			games = append(games, cached(fmt.Sprintf("igdb:%d", 500+i), fmt.Sprintf("IGDB Game %d", i), "2020"))
		}

		return games
	}
	count := func(results []mediaCachedResult, prefix string) int {
		n := 0
		for _, result := range results {
			if strings.HasPrefix(result.Ref, prefix) {
				n++
			}
		}

		return n
	}

	for _, tried := range []struct {
		name            string
		steam, igdb     []mediaCachedResult
		query           string
		wantS, wantI    int
		wantFirst       string
		wantSteamBefore bool
	}{
		{"both full", steamGames(8), igdbGames(8), "game", 5, 3, "steam:100", true},
		{"eight from Steam and three from IGDB", steamGames(8), igdbGames(3), "game", 5, 3, "steam:100", true},
		{"two from Steam and eight from IGDB", steamGames(2), igdbGames(8), "game", 2, 6, "steam:100", true},
		{"eight from Steam and none from IGDB", steamGames(8), nil, "game", 8, 0, "steam:100", true},
		{"none from Steam", nil, igdbGames(5), "game", 0, 5, "igdb:500", false},
		{"hytale, from IGDB alone", nil, []mediaCachedResult{cached("igdb:113360", "Hytale", "2026")}, "hytale", 0, 1, "igdb:113360", false},
	} {
		t.Run(tried.name, func(t *testing.T) {
			merged := mergeGames(tried.steam, tried.igdb, tried.query)

			require.LessOrEqual(t, len(merged), maxMediaResults)
			require.Equal(t, tried.wantS, count(merged, "steam:"))
			require.Equal(t, tried.wantI, count(merged, "igdb:"))
			require.Equal(t, tried.wantFirst, merged[0].Ref)

			if tried.wantSteamBefore {
				// Steam's games, then IGDB's, each in its own order.
				seenIGDB := false
				for _, result := range merged {
					if strings.HasPrefix(result.Ref, "igdb:") {
						seenIGDB = true
					} else {
						require.False(t, seenIGDB, "a Steam game after an IGDB one")
					}
				}
			}
		})
	}

	// A game typed in full comes first wherever it came from: Steam's exact
	// matches, then IGDB's, then the rest.
	steam := append(steamGames(6), cached("steam:900", "Terraria", ""))
	igdb := append(igdbGames(4), cached("igdb:77", "Terraria: Otherworld", "2026"), cached("igdb:78", "TERRARIA", "2011"))
	merged := mergeGames(steam, igdb, "terraria")
	require.Equal(t, "steam:900", merged[0].Ref, "Steam's exact match first")
	for _, result := range merged {
		require.NotEqual(t, "igdb:78", result.Ref, "the same name as a game Steam listed is Steam's")
	}

	igdb = append(igdbGames(4), cached("igdb:77", "Terraria", "2011"))
	merged = mergeGames(steamGames(8), igdb, "terraria")
	require.Equal(t, "igdb:77", merged[0].Ref, "IGDB's exact match first when Steam has none")
	require.Len(t, merged, maxMediaResults)
	require.Equal(t, 4, count(merged, "steam:"), "the exact match takes one of the eight places")
	require.Equal(t, 4, count(merged, "igdb:"), "and IGDB still has its three among the rest")
}

func TestIGDBGamesSteamListedAreLeftToSteam(t *testing.T) {
	// Each of the three shapes a Steam link takes in IGDB.
	for _, tried := range []struct {
		name string
		row  map[string]any
		want string
	}{
		{"a website", map[string]any{"websites": []map[string]any{{"type": 13, "url": "https://store.steampowered.com/app/1245620/ELDEN_RING/"}}}, "1245620"},
		{"an external game's address", map[string]any{"external_games": []map[string]any{{"external_game_source": 1, "url": "https://store.steampowered.com/app/1245620"}}}, "1245620"},
		{"an external game's uid", map[string]any{"external_games": []map[string]any{{"external_game_source": 1, "uid": "01245620"}}}, "1245620"},
		{"another store's uid", map[string]any{"external_games": []map[string]any{{"external_game_source": 5, "uid": "1245620"}}}, ""},
		{"a look-alike host", map[string]any{"websites": []map[string]any{{"url": "https://store.steampowered.com.evil.example/app/1/"}}}, ""},
		{"a bare reference", map[string]any{"external_games": []any{12, 13}}, ""},
	} {
		t.Run(tried.name, func(t *testing.T) {
			row := map[string]any{"id": 1245, "name": "ELDEN RING"}
			for key, value := range tried.row {
				row[key] = value
			}

			results := igdbResults(decodeIGDB(t, row))
			require.Len(t, results, 1)
			require.Equal(t, tried.want, results[0].SteamApp)
		})
	}

	// Left to Steam: linked to a game in Steam's answer, or named like one.
	steam := []mediaCachedResult{cached("steam:1245620", "ELDEN RING", ""), cached("steam:42", "Hollow Knight", "")}
	linked := cached("igdb:1", "Elden Ring", "2022")
	linked.SteamApp = "1245620"
	named := cached("igdb:2", "Hollow Knight", "2017")
	other := cached("igdb:3", "Hollow Knight: Silksong", "2025")

	merged := mergeGames(steam, []mediaCachedResult{linked, named, other}, "hollow knight")
	require.Equal(t, []string{"steam:42", "steam:1245620", "igdb:3"}, refsOfCached(merged))
}

func refsOfCached(results []mediaCachedResult) []string {
	refs := make([]string, 0, len(results))
	for _, result := range results {
		refs = append(refs, result.Ref)
	}

	return refs
}

// decodeIGDB turns rows into what IGDB's answer decodes to.
func decodeIGDB(t *testing.T, rows ...map[string]any) []igdbGame {
	t.Helper()

	fake := newMediaFake(t)
	fake.igdbToken("tok", 5000000)
	fake.igdbGames(func(string) (int, any) { return http.StatusOK, rows })

	p, _ := mediaPlugin(t, twitchConfig())

	var games []igdbGame
	require.NoError(t, p.igdbPost(t.Context(), p.getConfiguration(), "games", "fields name;", &games))

	return games
}

// Steam's search does not list the games Steam no longer sells, so a Steam
// link in IGDB is not a reason to leave a game out.
func TestIGDBKeepsGamesSteamNoLongerSells(t *testing.T) {
	fake := newMediaFake(t)

	// As OBSERVED: Steam answers "forza horizon 4" with add-ons for 5.
	steamItems(fake,
		steamRow(2000001, "Forza Horizon 5: Hot Wheels"),
		steamRow(2000002, "Forza Horizon 5: Rally Adventure"))
	fake.igdbToken("tok", 5000000)
	fake.igdbGames(igdbCatalogue(map[string]any{
		"id": 104967, "name": "Forza Horizon 4", "first_release_date": 1538697600,
		"game_type": map[string]any{"id": 0, "type": "Main Game"},
		"websites":  []map[string]any{{"type": 13, "url": "https://store.steampowered.com/app/1293830/"}},
	}))

	p, _, _ := mediaPluginWithFaults(t, twitchConfig())

	found := mustSearch(t, p, "game", "forza horizon 4")
	require.Equal(t, []string{"igdb:104967", "steam:2000001", "steam:2000002"}, refsOf(found.Results),
		"typed in full, so first; from IGDB, because Steam did not list it")

	// After a restart, the remembered IGDB search still carries its Steam
	// link, and the same answer comes back without asking anybody.
	before := len(fake.requests())
	q := restart(t, p)
	again := mustSearch(t, q, "game", "forza horizon 4")
	require.Equal(t, found.Results, again.Results)
	require.Len(t, fake.requests(), before)

	recalled, ok := q.recallMedia(mediaSearchKey(mediaProviderIGDB, "forza horizon 4"))
	require.True(t, ok)
	require.Equal(t, "1293830", recalled.Results[0].SteamApp)
}

func TestEditionsDLCAndBundlesAreLeftOut(t *testing.T) {
	row := func(id int, name string, extra map[string]any) map[string]any {
		r := map[string]any{"id": id, "name": name}
		for key, value := range extra {
			r[key] = value
		}

		return r
	}
	typed := func(id int, text string) map[string]any {
		return map[string]any{"game_type": map[string]any{"id": id, "type": text}}
	}
	typedByID := func(id int) map[string]any { return map[string]any{"game_type": id} }
	withParent := func(extra map[string]any) map[string]any {
		extra["parent_game"] = 99
		return extra
	}

	rows := []map[string]any{
		row(1, "Main", typed(0, "Main Game")),
		row(2, "A DLC", typed(1, "DLC Addon")),
		row(3, "An expansion", typed(2, "Expansion")),
		row(4, "A bundle", typed(3, "Bundle")),
		row(5, "Standalone", typed(4, "Standalone Expansion")),
		row(6, "A mod", typed(5, "Mod")),
		row(7, "A remake", withParent(typed(8, "Remake"))),
		row(8, "A remaster", withParent(typed(9, "Remaster"))),
		row(9, "Expanded", typed(10, "Expanded Game")),
		row(10, "A port", withParent(typed(11, "Port"))),
		row(11, "A pack", typed(13, "Pack")),
		row(12, "Untyped", nil),
		row(13, "Untyped add-on", map[string]any{"parent_game": map[string]any{"id": 99}}),
		row(14, "An edition", map[string]any{"version_parent": 7}),
		row(15, "By id, main", typedByID(0)),
		row(16, "By id, bundle", typedByID(3)),
		row(17, "By id, remake", withParent(typedByID(8))),
		row(18, "Erotic", map[string]any{"themes": []int{42}}),
	}

	results := igdbResults(decodeIGDB(t, rows...))

	kept := map[string]bool{}
	for _, result := range results {
		kept[result.Title] = true
	}
	require.Len(t, results, maxMediaResults, "eight at most, whatever IGDB offers")

	for _, name := range []string{"Main", "Standalone", "A remake", "A remaster", "Expanded", "A port", "Untyped", "By id, main"} {
		require.True(t, kept[name], name)
	}
	for _, name := range []string{"A DLC", "An expansion", "A bundle", "A mod", "A pack", "Untyped add-on", "An edition", "By id, bundle", "Erotic"} {
		require.False(t, kept[name], name)
	}

	// The rest of what is kept, without the cap of eight.
	rest := igdbResults(decodeIGDB(t, rows[14:]...))
	require.Equal(t, []string{"By id, main", "By id, remake"}, titlesOf(rest))

	// The where clause asks for the same, by IGDB's documented ids.
	require.Contains(t, igdbSearchBody("x"),
		"where version_parent = null & themes != (42) & (game_type = (0,4,8,9,10,11) | game_type = null);")

	// One line for a game ported to several machines under one name.
	same := igdbResults(decodeIGDB(t,
		row(30, "Stardew Valley", map[string]any{"first_release_date": 1456444800}),
		row(31, "Stardew Valley", map[string]any{"first_release_date": 1456444800, "game_type": map[string]any{"id": 11, "type": "Port"}}),
		row(32, "Stardew Valley", map[string]any{"first_release_date": 1600000000})))
	require.Equal(t, []string{"igdb:30", "igdb:32"}, refsOfCached(same), "the same name and year once; another year is another game")
}

func titlesOf(results []mediaCachedResult) []string {
	titles := make([]string, 0, len(results))
	for _, result := range results {
		titles = append(titles, result.Title)
	}

	return titles
}

func TestWhatSomebodyTypesCannotBreakOutOfTheIGDBQuery(t *testing.T) {
	for typed, sent := range map[string]string{
		`he said "x"; fields *;\`: "he said x fields",
		`“zelda”`:                 "zelda",
		`‘zelda’`:                 "zelda",
		`;fields *;`:              "fields",
		`Baldur's Gate 3`:         "Baldurs Gate 3",
		`Baldur’s Gate 3`:         "Baldurs Gate 3",
		`C++: Origins!`:           "C++: Origins!",
		"tab\there\nnewline":      "tab here newline",
		`""""`:                    "",
		`;*`:                      "",
		`Pokémon Ōkami 2`:         "Pokémon Ōkami 2",
	} {
		require.Equal(t, sent, igdbSearchText(typed), "%q", typed)
	}

	fake := newMediaFake(t)
	steamItems(fake)
	fake.igdbToken("tok", 5000000)
	fake.igdbGames(func(string) (int, any) { return http.StatusOK, []any{} })

	p, store := mediaPlugin(t, twitchConfig())

	mustSearch(t, p, "game", `he said "x"; fields *;\`)
	var body string
	for _, seen := range fake.requests() {
		if seen.path == "/v4/games" {
			body = seen.body
		}
	}
	require.True(t, strings.HasPrefix(body, "search \"he said x fields\";\nfields name,"), body)
	require.Equal(t, 1, strings.Count(body, ";\nfields "), "one fields clause, the plugin's own")

	// Nothing left to search for: IGDB is not asked, and nothing remembered.
	remembered := func() map[string]string {
		kept := map[string]string{}
		for key, value := range store {
			if strings.HasPrefix(key, kvMediaCachePrefix) {
				kept[key] = string(value)
			}
		}

		return kept
	}

	before, posts := remembered(), gamesPosts(fake)
	mustSearch(t, p, "game", `""""`)
	mustSearch(t, p, "game", `;*`)
	require.Equal(t, posts, gamesPosts(fake))
	require.Equal(t, before, remembered())
}

func TestWithoutTwitchCredentialsGamesComeFromSteamOnly(t *testing.T) {
	fake := newMediaFake(t)
	steamItems(fake, steamRow(1245620, "ELDEN RING"))
	fake.igdbToken("tok", 5000000)
	fake.igdbGames(igdbCatalogue(hytaleRow()))

	for _, cfg := range []*configuration{
		{MediaLookup: true},
		{MediaLookup: true, TwitchClientID: testTwitchClientID},
		{MediaLookup: true, TwitchClientSecret: testTwitchClientSecret},
	} {
		p, _ := mediaPlugin(t, cfg)

		found := mustSearch(t, p, "game", "elden ring")
		require.Equal(t, []string{"steam:1245620"}, refsOf(found.Results))
		require.Empty(t, found.Incomplete, "IGDB is off, not missing")
		require.False(t, p.getConfiguration().mediaFlags().IGDB)

		// A stored IGDB plan keeps its words, but its art is not relayed.
		require.Equal(t, http.StatusNotFound, mediaPictureOf(t, p, "igdb:113360", "cover").Code)
	}

	for _, seen := range fake.requests() {
		require.False(t, strings.HasPrefix(seen.path, "/oauth2/"), seen.path)
		require.False(t, strings.HasPrefix(seen.path, "/v4/"), seen.path)
		require.False(t, strings.HasPrefix(seen.path, "/igdb/"), seen.path)
	}
}

func TestOneProviderFailingStillAnswersWithTheOther(t *testing.T) {
	t.Run("Steam failing", func(t *testing.T) {
		fake := newMediaFake(t)
		fake.status("/api/storesearch/", http.StatusInternalServerError)
		fake.igdbToken("tok", 5000000)
		fake.igdbGames(igdbCatalogue(hytaleRow(), vintageStoryRow()))

		p, _ := mediaPlugin(t, twitchConfig())

		found := mustSearch(t, p, "game", "hytale")
		require.Equal(t, []string{"igdb:113360"}, refsOf(found.Results))
		require.Equal(t, "Steam", found.Incomplete)
		require.Equal(t, 1, partialWarnings(p, "Steam"))

		// Asked twice, told once.
		mustSearch(t, p, "game", "vintage story")
		require.Equal(t, 1, partialWarnings(p, "Steam"))
	})

	t.Run("IGDB failing", func(t *testing.T) {
		fake := newMediaFake(t)
		steamItems(fake, steamRow(1245620, "ELDEN RING"))
		fake.igdbToken("tok", 5000000)
		fake.igdbGames(func(string) (int, any) { return http.StatusBadGateway, nil })

		p, _ := mediaPlugin(t, twitchConfig())
		clock := newFakeClock()
		useClock(p, clock)

		found := mustSearch(t, p, "game", "elden ring")
		require.Equal(t, []string{"steam:1245620"}, refsOf(found.Results))
		require.Equal(t, "IGDB", found.Incomplete)
		require.Equal(t, 1, partialWarnings(p, "IGDB"))

		// Past its pause, failing again: still told once.
		clock.advance(time.Minute)
		mustSearch(t, p, "game", "elden")
		require.Equal(t, 2, gamesPosts(fake))
		require.Equal(t, 1, partialWarnings(p, "IGDB"))
	})

	t.Run("both failing", func(t *testing.T) {
		fake := newMediaFake(t)
		fake.status("/api/storesearch/", http.StatusInternalServerError)
		fake.igdbToken("tok", 5000000)
		fake.igdbGames(func(string) (int, any) { return http.StatusInternalServerError, nil })

		p, _ := mediaPlugin(t, twitchConfig())

		recorder := mediaSearch(t, p, "game", "hytale")
		require.Equal(t, http.StatusBadGateway, recorder.Code)
		require.Contains(t, recorder.Body.String(), "Could not search for games just now")
	})

	// Nothing on Steam and IGDB failing is not "nothing on either": the
	// answer says IGDB is missing, so the form does not claim Hytale is not
	// a game.
	t.Run("IGDB failing, nothing on Steam", func(t *testing.T) {
		fake := newMediaFake(t)
		steamItems(fake)
		fake.igdbToken("tok", 5000000)
		fake.igdbGames(func(string) (int, any) { return http.StatusServiceUnavailable, nil })

		p, _ := mediaPlugin(t, twitchConfig())

		found := mustSearch(t, p, "game", "hytale")
		require.Empty(t, found.Results)
		require.Equal(t, "IGDB", found.Incomplete)
	})

	// Refused, resting or over budget is reported elsewhere or expected, and
	// is not a partial failure worth a word.
	t.Run("IGDB refused", func(t *testing.T) {
		fake := newMediaFake(t)
		steamItems(fake, steamRow(1245620, "ELDEN RING"))
		fake.status("/oauth2/token", http.StatusForbidden)

		p, _ := mediaPlugin(t, twitchConfig())

		found := mustSearch(t, p, "game", "elden ring")
		require.Equal(t, []string{"steam:1245620"}, refsOf(found.Results))
		require.Equal(t, "IGDB", found.Incomplete)
		mustSearch(t, p, "game", "elden")
		require.Zero(t, partialWarnings(p, "IGDB"))
		require.Equal(t, 1, warnings(p, "Twitch refused"), "said once, by the Twitch application's own warning")
	})

	t.Run("IGDB busy", func(t *testing.T) {
		fake := newMediaFake(t)
		steamItems(fake, steamRow(1245620, "ELDEN RING"))
		fake.igdbToken("tok", 5000000)
		fake.igdbGames(igdbCatalogue())

		p, _ := mediaPlugin(t, twitchConfig())
		clock := newFakeClock()
		useClock(p, clock)

		for i := 0; i < igdbCallsPerSecond; i++ {
			mustSearch(t, p, "game", fmt.Sprintf("elden %d", i))
		}
		found := mustSearch(t, p, "game", "elden ring")
		require.Equal(t, []string{"steam:1245620"}, refsOf(found.Results))
		require.Equal(t, "IGDB", found.Incomplete)
		require.Equal(t, igdbCallsPerSecond, gamesPosts(fake))
		require.Zero(t, partialWarnings(p, "IGDB"))
	})
}

func TestASlowIGDBDoesNotHoldSteamsAnswer(t *testing.T) {
	fake := newMediaFake(t)
	steamItems(fake, steamRow(42, "Hytale Modding Kit"))
	fake.igdbToken("tok", 5000000)

	held := newGate(t)
	answer := igdbCatalogue(hytaleRow())
	fake.igdbGames(func(query string) (int, any) {
		held.Hold()
		return answer(query)
	})

	// The handler cannot answer until released, so however slow the machine
	// the grace runs out first.
	saved := igdbGrace
	igdbGrace = 20 * time.Millisecond
	t.Cleanup(func() { igdbGrace = saved })

	p, _ := mediaPlugin(t, twitchConfig())

	found := mustSearch(t, p, "game", "hytale")
	require.Equal(t, []string{"steam:42"}, refsOf(found.Results), "Steam's answer, without waiting for IGDB")
	require.Equal(t, "IGDB", found.Incomplete, "and it says IGDB is missing from it, so the form asks again")
	require.Zero(t, partialWarnings(p, "IGDB"), "a late IGDB is not a failure")

	// IGDB's search went on by itself; once it answers, it is there for the
	// next keystroke, and IGDB was asked once.
	held.Release()
	results, err := p.providerSearch(t.Context(), p.getConfiguration(), mediaProviderIGDB, "hytale", "hytale")
	require.NoError(t, err)
	require.Equal(t, []string{"igdb:113360"}, refsOfCached(results))
	require.Equal(t, 1, gamesPosts(fake))

	found = mustSearch(t, p, "game", "hytale")
	require.Equal(t, []string{"igdb:113360", "steam:42"}, refsOf(found.Results))
	require.Empty(t, found.Incomplete)
	require.Equal(t, 1, gamesPosts(fake))
}

// The games IGDB is here for are the ones Steam has nothing for. With nothing
// of Steam's to show, the search does not stop at the grace: it waits for
// IGDB, within the search's budget, rather than answer "nothing" while IGDB's
// Hytale is on its way.
func TestIGDBIsWaitedForWhenSteamHasNothing(t *testing.T) {
	fake := newMediaFake(t)
	steamItems(fake)
	fake.igdbToken("tok", 5000000)

	held := newGate(t)
	answer := igdbCatalogue(hytaleRow())
	fake.igdbGames(func(query string) (int, any) {
		held.Hold()
		return answer(query)
	})

	// No grace at all. IGDB cannot answer before the test lets it, and that
	// is only once its request has arrived, long after Steam's empty answer;
	// waiting by the grace would have answered "nothing" by then.
	saved := igdbGrace
	igdbGrace = time.Nanosecond
	t.Cleanup(func() { igdbGrace = saved })

	p, _ := mediaPlugin(t, twitchConfig())

	go func() {
		<-held.entered
		held.Release()
	}()

	found := mustSearch(t, p, "game", "hytale")
	require.Equal(t, []string{"igdb:113360"}, refsOf(found.Results))
	require.Empty(t, found.Incomplete)
}

func TestAFailingIGDBIsLeftAloneForAMoment(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			fake := newMediaFake(t)
			steamItems(fake, steamRow(1245620, "ELDEN RING"))
			fake.igdbToken("tok", 5000000)
			fake.igdbGames(func(string) (int, any) { return status, nil })

			p, _ := mediaPlugin(t, twitchConfig())
			clock := newFakeClock()
			useClock(p, clock)

			found := mustSearch(t, p, "game", "elden ring")
			require.Equal(t, []string{"steam:1245620"}, refsOf(found.Results))
			require.Equal(t, 1, gamesPosts(fake))

			clock.advance(10 * time.Second)
			found = mustSearch(t, p, "game", "elden ring")
			require.Equal(t, []string{"steam:1245620"}, refsOf(found.Results))
			require.Equal(t, 1, gamesPosts(fake), "left alone for thirty seconds")

			clock.advance(21 * time.Second)
			mustSearch(t, p, "game", "elden ring")
			require.Equal(t, 2, gamesPosts(fake), "and asked again after")
		})
	}
}

func TestAnIGDBTitleIsPlannedWithItsDetails(t *testing.T) {
	fake := newMediaFake(t)
	fake.igdbToken("tok", 5000000)
	fake.igdbGames(igdbCatalogue(vintageStoryRow()))

	p, store := mediaPlugin(t, twitchConfig())

	// A second apart, so IGDB's four a second never refuses one of these.
	clock := newFakeClock()
	useClock(p, clock)

	response := planStream(t, p, map[string]any{"title": "Vintage Story, day one", "category": "game", "media": "igdb:0069547"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	require.Equal(t, &scheduleMedia{
		Ref:      "igdb:69547",
		Kind:     "game",
		Title:    "Vintage Story",
		Year:     "2018",
		Overview: "An uncompromising wilderness survival sandbox game.",
		Facts:    "Indie · Anego Studios",
		Link:     "https://www.igdb.com/games/vintage-story",
		Source:   "IGDB",
	}, onlyPlan(t, p).Media)

	var body string
	for _, seen := range fake.requests() {
		if seen.path == "/v4/games" {
			body = seen.body
		}
	}
	require.Equal(t, "fields name,slug,url,summary,storyline,first_release_date,cover.image_id,artworks.image_id,"+
		"screenshots.image_id,genres.name,themes,game_type.type,game_status.status,involved_companies.developer,"+
		"involved_companies.company.name;\nwhere id = 69547;\nlimit 1;", body)

	// Stored with the plan as any other title is, so an older build reading
	// it shows the plan without its title rather than failing.
	require.Contains(t, string(store[kvSchedule]), `"media":{"ref":"igdb:69547"`)

	// A link that is not IGDB's falls back to one built from the slug, and
	// an answer with neither is not trusted.
	odd := vintageStoryRow()
	odd["id"] = 70000
	odd["url"] = "javascript:alert(1)"
	broken := vintageStoryRow()
	broken["id"] = 70001
	broken["url"] = "https://evil.example/"
	broken["slug"] = "../../x"
	fake.igdbGames(igdbCatalogue(odd, broken))

	clock.advance(time.Second)
	details, err := p.igdbGame(t.Context(), p.getConfiguration(), mediaRef{provider: mediaProviderIGDB, kind: mediaKindGame, id: "70000"})
	require.NoError(t, err)
	require.Equal(t, "https://www.igdb.com/games/vintage-story", details.info.Link)
	require.True(t, details.changing, "early access is still settling")

	clock.advance(time.Second)
	_, err = p.igdbGame(t.Context(), p.getConfiguration(), mediaRef{provider: mediaProviderIGDB, kind: mediaKindGame, id: "70001"})
	require.Error(t, err)
	require.NotErrorIs(t, err, errMediaNotFound, "a broken answer is not remembered as no such game")

	// No such game, and a game IGDB files as erotic, are both none.
	fake.igdbGames(igdbCatalogue(map[string]any{"id": 5, "name": "Not for the panel", "slug": "nope", "themes": []int{1, 42}}))
	clock.advance(time.Second)
	_, err = p.igdbGame(t.Context(), p.getConfiguration(), mediaRef{provider: mediaProviderIGDB, kind: mediaKindGame, id: "5"})
	require.ErrorIs(t, err, errMediaNotFound)
	clock.advance(time.Second)
	_, err = p.igdbGame(t.Context(), p.getConfiguration(), mediaRef{provider: mediaProviderIGDB, kind: mediaKindGame, id: "6"})
	require.ErrorIs(t, err, errMediaNotFound)
}

func TestIGDBGenreNamesAreShortened(t *testing.T) {
	for name, shown := range map[string]string{
		"Role-playing (RPG)":         "RPG",
		"Real Time Strategy (RTS)":   "RTS",
		"Turn-based strategy (TBS)":  "TBS",
		"Hack and slash/Beat 'em up": "Hack and slash/Beat 'em up",
		"Card & Board Game":          "Card & Board Game",
	} {
		require.Equal(t, shown, igdbGenreName(name), name)
	}

	fake := newMediaFake(t)
	fake.igdbToken("tok", 5000000)
	fake.igdbGames(igdbCatalogue(hytaleRow()))
	p, _ := mediaPlugin(t, twitchConfig())

	details, err := p.igdbGame(t.Context(), p.getConfiguration(), mediaRef{provider: mediaProviderIGDB, kind: mediaKindGame, id: "113360"})
	require.NoError(t, err)
	require.Equal(t, "RPG · Simulator · Hypixel Studios", details.info.Facts)
}

func TestIGDBPicturesComeFromItsImageHost(t *testing.T) {
	fake := newMediaFake(t)
	steamItems(fake)
	fake.igdbToken("tok", 5000000)
	fake.igdbGames(igdbCatalogue(vintageStoryRow()))
	fake.bytes("/igdb/image/upload/t_cover_small/co2j6g.jpg", jpegBytes)
	fake.bytes("/igdb/image/upload/t_cover_big/co2j6g.jpg", pngBytes)
	fake.bytes("/igdb/image/upload/t_720p/co2j6g.jpg", jpegBytes)

	p, _ := mediaPlugin(t, twitchConfig())
	mustSearch(t, p, "game", "vintage story")
	searches := gamesPosts(fake)

	response := mediaPictureOf(t, p, "igdb:69547", "thumb")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, jpegBytes, response.Body.Bytes())

	response = mediaPictureOf(t, p, "igdb:69547", "cover")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "image/png", response.Header().Get("Content-Type"))
	require.Equal(t, searches, gamesPosts(fake), "the search already said where the thumb and cover are")

	// A search does not describe a game's wide art, so the backdrop asks for
	// the details. This one has no artwork and no screenshot: the cover,
	// larger, behind the card.
	response = mediaPictureOf(t, p, "igdb:69547", "backdrop")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, searches+1, gamesPosts(fake))
	require.Equal(t, 1, fake.requestsTo("/igdb/image/upload/t_720p/co2j6g.jpg"))

	// A wide picture, when the game has one, even straight after a search
	// that knew only its cover.
	fake.bytes("/igdb/image/upload/t_screenshot_big/ar1x2y.jpg", pngBytes)
	fake.bytes("/igdb/image/upload/t_720p/cobc4t.jpg", jpegBytes)
	withArt := hytaleRow()
	withArt["artworks"] = []map[string]any{{"id": 3, "image_id": "ar1x2y"}}
	fake.igdbGames(igdbCatalogue(withArt))

	require.Equal(t, []string{"igdb:113360"}, refsOf(mustSearch(t, p, "game", "hytale").Results))
	response = mediaPictureOf(t, p, "igdb:113360", "backdrop")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "image/png", response.Header().Get("Content-Type"))
	require.Equal(t, 1, fake.requestsTo("/igdb/image/upload/t_screenshot_big/ar1x2y.jpg"))
	require.Zero(t, fake.requestsTo("/igdb/image/upload/t_720p/cobc4t.jpg"), "not the cover standing in")

	for _, address := range []string{
		igdbImageURL("cover_big", "../../etc/passwd"),
		igdbImageURL("cover_big", ""),
		igdbImageURL("cover_big", "a/b"),
	} {
		require.Empty(t, address)
	}
}

func TestAnIGDB401FetchesANewTokenOnceAndRetries(t *testing.T) {
	fake := newMediaFake(t)
	steamItems(fake)

	var issued atomic.Int32
	fake.route("/oauth2/token", func(w http.ResponseWriter, _ *http.Request) {
		writeTwitchToken(w, fmt.Sprintf("tok-%d", issued.Add(1)), 5000000)
	})

	answer := igdbCatalogue(hytaleRow())
	var asked atomic.Int32
	fake.igdbGames(func(query string) (int, any) {
		if asked.Add(1) == 1 {
			return http.StatusUnauthorized, nil
		}

		return answer(query)
	})

	p, _ := mediaPlugin(t, twitchConfig())

	found := mustSearch(t, p, "game", "hytale")
	require.Equal(t, []string{"igdb:113360"}, refsOf(found.Results))
	require.Equal(t, 2, fake.requestsTo("/oauth2/token"))
	require.Equal(t, 2, gamesPosts(fake))

	var auths []string
	for _, seen := range fake.requests() {
		if seen.path == "/v4/games" {
			auths = append(auths, seen.auth)
		}
	}
	require.Equal(t, []string{"Bearer tok-1", "Bearer tok-2"}, auths)
}

// IGDB refusing every token must not turn into a new token per search: an
// application has 25, and going over cancels other nodes' and features'.
func TestIGDBRefusingEveryTokenDoesNotChurnTokens(t *testing.T) {
	fake := newMediaFake(t)
	steamItems(fake)

	var issued atomic.Int32
	fake.route("/oauth2/token", func(w http.ResponseWriter, _ *http.Request) {
		writeTwitchToken(w, fmt.Sprintf("tok-%d", issued.Add(1)), 5000000)
	})
	fake.igdbGames(func(string) (int, any) { return http.StatusUnauthorized, nil })

	p, _ := mediaPlugin(t, twitchConfig())
	clock := newFakeClock()
	useClock(p, clock)

	_, err := p.searchIGDB(t.Context(), p.getConfiguration(), "hytale")
	require.ErrorIs(t, err, errTwitchRefused)
	require.Equal(t, 2, fake.requestsTo("/oauth2/token"))
	require.Equal(t, 2, gamesPosts(fake))
	require.Equal(t, 1, warnings(p, "IGDB refused Matty's Twitch access token"))

	// Searching on: nothing asked of anybody.
	mustSearch(t, p, "game", "hytale")
	require.Equal(t, 2, fake.requestsTo("/oauth2/token"))
	require.Equal(t, 2, gamesPosts(fake))

	// After the pause: IGDB asked with the token it already refused, and no
	// new token minted for it.
	clock.advance(6 * time.Minute)
	_, err = p.searchIGDB(t.Context(), p.getConfiguration(), "hytale")
	require.ErrorIs(t, err, errTwitchRefused)
	require.Equal(t, 2, fake.requestsTo("/oauth2/token"))
	require.Equal(t, 3, gamesPosts(fake))

	// New credentials end the hold-back at once.
	fixed := twitchConfig()
	fixed.TwitchClientSecret = "a-new-secret"
	fixed.normalize()
	p.setConfiguration(fixed)
	_, err = p.searchIGDB(t.Context(), fixed, "hytale")
	require.ErrorIs(t, err, errTwitchRefused)
	require.Equal(t, 4, fake.requestsTo("/oauth2/token"), "a token for the new credentials, and one more after the first 401")
	require.Equal(t, 5, gamesPosts(fake))

	// A 403 holds back with no token minted after it.
	other := newMediaFake(t)
	other.igdbToken("tok", 5000000)
	other.igdbGames(func(string) (int, any) { return http.StatusForbidden, nil })
	q, _ := mediaPlugin(t, twitchConfig())

	_, err = q.searchIGDB(t.Context(), q.getConfiguration(), "hytale")
	require.ErrorIs(t, err, errTwitchRefused)
	_, err = q.searchIGDB(t.Context(), q.getConfiguration(), "hytale")
	require.ErrorIs(t, err, errTwitchRefused)
	require.Equal(t, 1, other.requestsTo("/oauth2/token"))
	require.Equal(t, 1, other.requestsTo("/v4/games"))
}

// A token IGDB once refused and then took again is an ordinary token: when
// Twitch later cancels it (25 an application, the oldest going first), the 401
// earns a new one at once, not five minutes of "IGDB refused" per search.
func TestIGDBTakingATokenAgainEndsItsRefusal(t *testing.T) {
	fake := newMediaFake(t)
	steamItems(fake)

	var issued atomic.Int32
	fake.route("/oauth2/token", func(w http.ResponseWriter, _ *http.Request) {
		writeTwitchToken(w, fmt.Sprintf("tok-%d", issued.Add(1)), 5000000)
	})

	// The one token IGDB takes just now, or none.
	var takes atomic.Value
	takes.Store("")
	fake.route("/v4/games", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+takes.Load().(string) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	})

	p, _ := mediaPlugin(t, twitchConfig())
	clock := newFakeClock()
	useClock(p, clock)

	// A hiccup at IGDB: both tokens refused, and IGDB held back.
	_, err := p.searchIGDB(t.Context(), p.getConfiguration(), "hytale")
	require.ErrorIs(t, err, errTwitchRefused)
	require.Equal(t, 2, fake.requestsTo("/oauth2/token"))

	// Over: the second token works.
	takes.Store("tok-2")
	clock.advance(6 * time.Minute)
	_, err = p.searchIGDB(t.Context(), p.getConfiguration(), "hytale")
	require.NoError(t, err)

	// Twitch cancels it: one new token, and the search goes through.
	takes.Store("tok-3")
	clock.advance(time.Second)
	_, err = p.searchIGDB(t.Context(), p.getConfiguration(), "hytale")
	require.NoError(t, err)
	require.Equal(t, 3, fake.requestsTo("/oauth2/token"))
}

// IGDB says "no such game" with an empty list. A 404 or 410 from it is IGDB
// not being where it should be, and a game asked about meanwhile must not be
// remembered as one that does not exist.
func TestAnIGDB404IsNotNoSuchGame(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			fake := newMediaFake(t)
			steamItems(fake)
			fake.igdbToken("tok", 5000000)
			fake.igdbGames(func(string) (int, any) { return status, nil })

			p, _, _ := mediaPluginWithFaults(t, twitchConfig())
			clock := newFakeClock()
			useClock(p, clock)

			ref, ok := parseMediaRef("igdb:113360")
			require.True(t, ok)

			_, err := p.mediaDetailsFor(t.Context(), p.getConfiguration(), ref)
			require.Error(t, err)
			require.NotErrorIs(t, err, errMediaNotFound)
			_, remembered := p.recallMedia(mediaTitleKey(ref))
			require.False(t, remembered, "nothing remembered about the game, missing or otherwise")

			// Left alone for a while, like any failing IGDB, and said so.
			found := mustSearch(t, p, "game", "hytale")
			require.Empty(t, found.Results)
			require.Equal(t, "IGDB", found.Incomplete)
			require.Equal(t, 1, gamesPosts(fake))

			// Back, and the game is there.
			fake.igdbGames(igdbCatalogue(hytaleRow()))
			clock.advance(igdbFailurePause + time.Second)
			details, err := p.mediaDetailsFor(t.Context(), p.getConfiguration(), ref)
			require.NoError(t, err)
			require.Equal(t, "Hytale", details.info.Title)
		})
	}
}

func TestIGDBIsKeptToItsBudget(t *testing.T) {
	ask := func(p *Plugin) error {
		var games []igdbGame
		return p.igdbPost(t.Context(), p.getConfiguration(), "games", "fields name;", &games)
	}

	t.Run("four in any second", func(t *testing.T) {
		fake := newMediaFake(t)
		fake.igdbToken("tok", 5000000)
		fake.igdbGames(func(string) (int, any) { return http.StatusOK, []any{} })

		p, _ := mediaPlugin(t, twitchConfig())
		clock := newFakeClock()
		useClock(p, clock)

		for i := 0; i < igdbCallsPerSecond; i++ {
			require.NoError(t, ask(p))
		}

		clock.advance(999 * time.Millisecond)
		require.ErrorIs(t, ask(p), errMediaBusy)
		require.Equal(t, igdbCallsPerSecond, gamesPosts(fake), "refused without a request")

		clock.advance(time.Millisecond)
		require.NoError(t, ask(p))
		require.Equal(t, igdbCallsPerSecond+1, gamesPosts(fake))
	})

	// A place is taken as a request goes out, not when it was first asked
	// for. Requests asked for in two different seconds that all waited for
	// one token fetch go out together, so only four of them may.
	t.Run("counted when sent", func(t *testing.T) {
		fake := newMediaFake(t)
		held := newGate(t)
		fake.route("/oauth2/token", func(w http.ResponseWriter, _ *http.Request) {
			held.Hold()
			writeTwitchToken(w, "tok", 5000000)
		})
		fake.igdbGames(func(string) (int, any) { return http.StatusOK, []any{} })

		p, _ := mediaPlugin(t, twitchConfig())
		clock := newFakeClock()
		useClock(p, clock)

		results := make(chan error, 2*igdbCallsPerSecond)
		var done sync.WaitGroup
		start := func() {
			done.Add(1)
			go func() {
				defer done.Done()
				results <- ask(p)
			}()
		}

		// Four asked for at t0, waiting on the token...
		start()
		<-held.entered
		for i := 1; i < igdbCallsPerSecond; i++ {
			start()
		}

		// ...and four more a second later.
		clock.advance(time.Second)
		for i := 0; i < igdbCallsPerSecond; i++ {
			start()
		}

		held.Release()
		done.Wait()
		close(results)

		busy := 0
		for err := range results {
			if err != nil {
				require.ErrorIs(t, err, errMediaBusy)
				busy++
			}
		}
		require.Equal(t, igdbCallsPerSecond, gamesPosts(fake), "four went out together, not eight")
		require.Equal(t, igdbCallsPerSecond, busy)
	})

	t.Run("a 401's retry fits", func(t *testing.T) {
		fake := newMediaFake(t)
		fake.igdbToken("tok", 5000000)
		var asked atomic.Int32
		fake.igdbGames(func(string) (int, any) {
			if asked.Add(1) == 3 {
				return http.StatusUnauthorized, nil
			}

			return http.StatusOK, []any{}
		})

		p, _ := mediaPlugin(t, twitchConfig())
		useClock(p, newFakeClock())

		require.NoError(t, ask(p))
		require.NoError(t, ask(p))
		require.NoError(t, ask(p), "refused once, then retried within the same second")
		require.Equal(t, 4, gamesPosts(fake))
		require.ErrorIs(t, ask(p), errMediaBusy)
	})

	t.Run("a hundred and twenty a minute", func(t *testing.T) {
		fake := newMediaFake(t)
		fake.igdbToken("tok", 5000000)
		fake.igdbGames(func(string) (int, any) { return http.StatusOK, []any{} })

		p, _ := mediaPlugin(t, twitchConfig())
		clock := newFakeClock()
		useClock(p, clock)
		start := clock.now()

		// A quarter of a second apart, so the per-second window never binds
		// and the bucket (120 at once, refilling two a second) decides.
		refused := 0
		for call := 1; call <= 240; call++ {
			if call > 1 {
				clock.advance(250 * time.Millisecond)
			}

			err := ask(p)
			if call < 240 {
				require.NoError(t, err, "call %d", call)
				continue
			}

			require.ErrorIs(t, err, errMediaBusy, "call %d", call)
			require.Equal(t, 59750*time.Millisecond, clock.now().Sub(start))
			refused++
		}

		require.Equal(t, 1, refused)
		require.Equal(t, 239, gamesPosts(fake))
	})
}

func TestWhatsOnCanPickAnIGDBGame(t *testing.T) {
	fake := newMediaFake(t)
	fake.igdbToken("tok", 5000000)
	fake.igdbGames(igdbCatalogue(hytaleRow()))

	p, store := mediaPlugin(t, twitchConfig())

	response := sayWhatIsOn(t, p, "villa", map[string]any{"category": "game", "title": "Hytale", "media": "igdb:113360"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	entry := storedNowPlaying(t, store)["villa"]
	require.NotNil(t, entry.Media)
	require.Equal(t, "igdb:113360", entry.Media.Ref)
	require.Equal(t, "IGDB", entry.Media.Source)
	require.Equal(t, "https://www.igdb.com/games/hytale", entry.Media.Link)

	require.Equal(t, http.StatusOK, sayWhatIsOn(t, p, "villa", map[string]any{"category": "game", "title": "Hytale, still", "media": "igdb:113360"}).Code)
	require.Equal(t, 1, gamesPosts(fake), "looked up once")

	// Not under the film icon.
	response = sayWhatIsOn(t, p, "villa", map[string]any{"category": "movie", "title": "Hytale", "media": "igdb:113360"})
	require.Equal(t, http.StatusBadRequest, response.Code)
}
