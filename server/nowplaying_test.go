package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// What a streamer says is on, from the footer's What's on? control. Only for
// somebody with their stream linked; checked like a plan's words and title;
// shown on their card in place of a running plan's title; and gone by itself
// once their stream has been off the air past the grace, or twelve hours after
// it was set for a stream that never came.

// sayWhatIsOn posts to the endpoint as userID, straight to the handler.
func sayWhatIsOn(t *testing.T, p *Plugin, userID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()

	encoded, err := json.Marshal(body)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/nowplaying", strings.NewReader(string(encoded)))
	p.handleSetNowPlaying(recorder, request, userID)

	return recorder
}

func askWhatIsOn(t *testing.T, p *Plugin, userID string) (int, *apiNowPlaying) {
	t.Helper()

	recorder := httptest.NewRecorder()
	p.handleGetNowPlaying(recorder, userID)

	var answer nowPlayingResponse
	if recorder.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer))
	}

	return recorder.Code, answer.NowPlaying
}

// storedNowPlaying is the document as it is in the store.
func storedNowPlaying(t *testing.T, store map[string][]byte) map[string]nowPlayingEntry {
	t.Helper()

	doc, ok := decodeNowPlaying(store[kvNowPlaying])
	require.True(t, ok, "the document should decode")

	return doc.Entries
}

// card is a person's stream as decorate leaves it: attributed, by their link.
func card(userID, key string) apiStream {
	return apiStream{StreamKey: key, Handle: "h-" + userID, Title: "Name " + userID, UserID: userID, LinkSource: linkSourceManual}
}

// onNowAt is one poll's worth of what is on, at a chosen instant: settle says
// whether the status listing was read.
func onNowAt(p *Plugin, streams []apiStream, schedule []scheduleItem, at time.Time, settle bool) *snapshot {
	current := &snapshot{Streams: streams, Schedule: schedule}
	p.addNowPlaying(current, p.getConfiguration(), at, settle, true)

	return current
}

func graceConfig(grace int) *configuration {
	cfg := &configuration{OfflineGraceChecks: grace}
	cfg.normalize()

	return cfg
}

func TestOnlyALinkedStreamerCanSayWhatIsOn(t *testing.T) {
	p, store := communityPlugin(t)

	response := sayWhatIsOn(t, p, "alice", map[string]any{"category": "game", "title": "Elden Ring"})
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Contains(t, response.Body.String(), "Link your stream first")
	require.Empty(t, store[kvNowPlaying], "a refusal writes nothing")

	code, _ := askWhatIsOn(t, p, "alice")
	require.Equal(t, http.StatusForbidden, code)

	linkStream(store, "villa")
	response = sayWhatIsOn(t, p, "villa", map[string]any{"category": "game", "title": "Elden Ring"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	code, shown := askWhatIsOn(t, p, "villa")
	require.Equal(t, http.StatusOK, code)
	require.NotNil(t, shown)
	require.Equal(t, "game", shown.Category)
	require.Equal(t, "Elden Ring", shown.Title)
	require.Nil(t, shown.Media)

	// Stored by person, and nobody else's.
	entries := storedNowPlaying(t, store)
	require.Len(t, entries, 1)
	require.Contains(t, entries, "villa")
}

func TestWhatIsOnIsCheckedLikeAPlan(t *testing.T) {
	p, store := communityPlugin(t)
	linkStream(store, "villa")

	for _, refused := range []map[string]any{
		{"title": "No kind"},
		{"category": "book", "title": "A kind this build does not know"},
		{"category": "game", "title": "   "},
		{"category": "game", "title": "Elden Ring", "media": "imdb:tt0113277"},
		{"category": "game", "title": "Heat", "media": "tmdb:movie:949"},
		{"category": "movie", "title": "Elden Ring", "media": "steam:1245620"},
	} {
		response := sayWhatIsOn(t, p, "villa", refused)
		require.Equal(t, http.StatusBadRequest, response.Code, "%v", refused)
	}
	require.Empty(t, storedNowPlaying(t, store), "nothing refused was stored")

	// Bounded the way a plan's title is, and the streamer's own key never
	// stored, whatever they pasted.
	long := strings.Repeat("é", maxScheduleTitle+40)
	require.Equal(t, http.StatusOK, sayWhatIsOn(t, p, "villa", map[string]any{"category": "movie", "title": long}).Code)
	_, shown := askWhatIsOn(t, p, "villa")
	require.Equal(t, maxScheduleTitle, len([]rune(shown.Title)))

	require.Equal(t, http.StatusOK, sayWhatIsOn(t, p, "villa", map[string]any{"category": "game", "title": "Co-op on videokey-villa tonight"}).Code)
	_, shown = askWhatIsOn(t, p, "villa")
	require.Equal(t, "Co-op on my stream tonight", shown.Title)
	require.NotContains(t, string(rawStored(t, p, kvNowPlaying)), "videokey-villa")

	// Nor their publish token, which Relink asks to have pasted right beside
	// this box, and which would let anybody who read it go live as them. Not
	// stored, so never on the card either.
	token := "5d0c7e2a-41f6-4b8e-9a3c-7f1e2d9b6c40"
	store[kvLinkTokenPrefix+"villa"] = []byte(token)
	require.Equal(t, http.StatusOK, sayWhatIsOn(t, p, "villa", map[string]any{"category": "game", "title": strings.ToUpper(token)}).Code)
	_, shown = askWhatIsOn(t, p, "villa")
	require.Equal(t, "my stream", shown.Title)
	require.NotContains(t, strings.ToLower(string(rawStored(t, p, kvNowPlaying))), token)
	shownOnCard := onNowAt(p, []apiStream{card("villa", "videokey-villa")}, nil, time.Now(), true)
	encoded, err := json.Marshal(shownOnCard)
	require.NoError(t, err)
	require.NotContains(t, strings.ToLower(string(encoded)), token)

	// Pasted at the END of a long title: taken out whole before the title is
	// cut to length, so no half of it is left behind either.
	padded := strings.Repeat("words ", 11) + token
	require.Greater(t, len([]rune(padded)), maxScheduleTitle)
	require.Equal(t, http.StatusOK, sayWhatIsOn(t, p, "villa", map[string]any{"category": "game", "title": padded}).Code)
	_, shown = askWhatIsOn(t, p, "villa")
	require.NotContains(t, strings.ToLower(shown.Title), token[:8])
	require.True(t, strings.HasSuffix(shown.Title, "my stream"), shown.Title)

	// Lookups off: a picked title is still accepted, without its details —
	// the kind and the words are what is on.
	require.Equal(t, http.StatusOK, sayWhatIsOn(t, p, "villa", map[string]any{"category": "game", "title": "ELDEN RING", "media": "steam:1245620"}).Code)
	_, shown = askWhatIsOn(t, p, "villa")
	require.Equal(t, "ELDEN RING", shown.Title)
	require.Nil(t, shown.Media)
}

// rawStored is the bytes stored under key.
func rawStored(t *testing.T, p *Plugin, key string) []byte {
	t.Helper()

	raw, appErr := p.API.KVGet(key)
	require.Nil(t, appErr)

	return raw
}

// A picked title is looked up by the server, never taken from the browser,
// and the same title saved again is not looked up a second time.
func TestWhatIsOnLooksAPickedTitleUpOnce(t *testing.T) {
	fake := newMediaFake(t)
	fake.json("/3/movie/949", heatDetails())

	p, store := mediaPlugin(t, movieConfig())

	response := sayWhatIsOn(t, p, "villa", map[string]any{"category": "movie", "title": "Heat", "media": "tmdb:movie:949"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 1, fake.requestsTo("/3/movie/949"))

	var answer nowPlayingResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer))
	require.NotNil(t, answer.NowPlaying.Media, "the answer carries the details, so the footer can show the cover at once")
	require.Equal(t, "Heat", answer.NowPlaying.Media.Title)
	require.Equal(t, "1995", answer.NowPlaying.Media.Year)
	require.Equal(t, "tmdb:movie:949", answer.NowPlaying.Media.Ref)

	// Switched off since: saving the same title again keeps what was stored.
	off := movieConfig()
	off.MediaLookup = false
	off.normalize()
	p.setConfiguration(off)

	require.Equal(t, http.StatusOK, sayWhatIsOn(t, p, "villa", map[string]any{"category": "movie", "title": "Heat (director's cut)", "media": "tmdb:movie:949"}).Code)
	require.Equal(t, 1, fake.requestsTo("/3/movie/949"), "not looked up again")

	entry := storedNowPlaying(t, store)["villa"]
	require.Equal(t, "Heat (director's cut)", entry.Title)
	require.NotNil(t, entry.Media)
	require.Equal(t, "Heat", entry.Media.Title)
}

func TestTakingWhatIsOnDownNeedsNoLinkAndUnlinkingDoesIt(t *testing.T) {
	p, store := communityPlugin(t)
	linkStream(store, "villa")
	require.Equal(t, http.StatusOK, sayWhatIsOn(t, p, "villa", map[string]any{"category": "game", "title": "Arma 3"}).Code)

	// Unlinked since: taking it down still works, as cancelling a plan does.
	delete(store, kvLinkByUserPrefix+"villa")

	recorder := httptest.NewRecorder()
	p.handleClearNowPlaying(recorder, "villa")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Empty(t, storedNowPlaying(t, store))

	// Nothing there: still fine, and nothing written.
	recorder = httptest.NewRecorder()
	p.handleClearNowPlaying(recorder, "villa")
	require.Equal(t, http.StatusOK, recorder.Code)

	// And unlinking takes it down by itself.
	linkStream(store, "villa")
	require.Equal(t, http.StatusOK, sayWhatIsOn(t, p, "villa", map[string]any{"category": "game", "title": "Arma 3"}).Code)

	recorder = httptest.NewRecorder()
	p.handleUnlink(recorder, "villa")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Empty(t, storedNowPlaying(t, store), "unlinking takes what is on down with it")
}

// Saving and taking down are metered like the stream title beside them; the
// footer reading it back is not.
func TestSayingWhatIsOnIsRateLimited(t *testing.T) {
	p, store := communityPlugin(t)
	linkStream(store, "villa")
	p.nowPlayingLimiter = newRateLimiter(10, time.Minute)

	send := func(method, body string) int {
		request := httptest.NewRequest(method, "/api/v1/nowplaying", strings.NewReader(body))
		request.Header.Set("Mattermost-User-Id", "villa")
		recorder := httptest.NewRecorder()
		p.ServeHTTP(nil, recorder, request)

		return recorder.Code
	}

	for i := 0; i < 10; i++ {
		require.Equal(t, http.StatusOK, send(http.MethodPost, `{"category":"game","title":"Arma 3"}`), "change %d", i)
	}
	require.Equal(t, http.StatusTooManyRequests, send(http.MethodPost, `{"category":"game","title":"Arma 3"}`))
	require.Equal(t, http.StatusTooManyRequests, send(http.MethodDelete, ""))
	require.Equal(t, http.StatusOK, send(http.MethodGet, ""))
	require.Equal(t, http.StatusMethodNotAllowed, send(http.MethodPut, ""))
}

// The one rule for what is on, and everything it must never do.
func TestWhatIsOnBeatsTheTitleOfAPlanThatIsOn(t *testing.T) {
	p, store := communityPlugin(t)
	now := time.Date(2026, 10, 1, 21, 30, 0, 0, time.UTC)
	nowMS := now.UnixMilli()
	hour := int64(time.Hour / time.Millisecond)

	heat := &scheduleMedia{Ref: "tmdb:movie:949", Kind: "movie", Title: "Heat", Year: "1995", Link: "https://www.imdb.com/title/tt0113277/", Source: "TMDB"}
	elden := &scheduleMedia{Ref: "steam:1245620", Kind: "game", Title: "ELDEN RING", Year: "2022", Link: "https://store.steampowered.com/app/1245620/", Source: "Steam"}

	// A film at seven and a game at nine, both still listed at half past
	// nine: it is the game that is on.
	schedule := []scheduleItem{
		{ID: "a", UserID: "bob", Title: "Film night", StartsAt: nowMS - (2 * hour) - (30 * 60000), Category: "movie", Media: heat},
		{ID: "b", UserID: "bob", Title: "Souls night", StartsAt: nowMS - (30 * 60000), Category: "game", Media: elden},
		{ID: "c", UserID: "bob", Title: "Tomorrow", StartsAt: nowMS + (20 * hour), Category: "movie", Media: heat},
	}

	bob := card("bob", "videokey-bob")
	shown := onNowAt(p, []apiStream{bob}, schedule, now, true).Streams[0].NowPlaying
	require.NotNil(t, shown)
	require.Equal(t, "ELDEN RING", shown.Title)
	require.Equal(t, "game", shown.Category)
	require.Equal(t, "steam:1245620", shown.Media.Ref)

	// The latest plan has no title: an older one that has is not stood in.
	schedule[1].Media = nil
	require.Nil(t, onNowAt(p, []apiStream{bob}, schedule, now, true).Streams[0].NowPlaying)
	schedule[1].Media = elden

	// What they said beats the plan.
	store[kvNowPlaying] = mustJSON(t, nowPlayingDoc{Version: 1, Entries: map[string]nowPlayingEntry{
		"bob": {Category: "movie", Title: "Heat", Media: heat, SetAt: nowMS - 60000, Aired: true},
	}})
	shown = onNowAt(p, []apiStream{bob}, schedule, now, true).Streams[0].NowPlaying
	require.NotNil(t, shown)
	require.Equal(t, "Heat", shown.Title)
	require.Equal(t, "tmdb:movie:949", shown.Media.Ref)

	// Even without a title of its own: the kind and the words are what is
	// on, and the plan's game is not.
	store[kvNowPlaying] = mustJSON(t, nowPlayingDoc{Version: 1, Entries: map[string]nowPlayingEntry{
		"bob": {Category: "movie", Title: "Something obscure", SetAt: nowMS - 60000, Aired: true},
	}})
	shown = onNowAt(p, []apiStream{bob}, schedule, now, true).Streams[0].NowPlaying
	require.Equal(t, "Something obscure", shown.Title)
	require.Nil(t, shown.Media)

	// Only on the stream their link proves is theirs. Matched by username,
	// it is the plan's again.
	byName := bob
	byName.LinkSource = linkSourceUsername
	require.Equal(t, "ELDEN RING", onNowAt(p, []apiStream{byName}, schedule, now, false).Streams[0].NowPlaying.Title)

	// Somebody else's plan says nothing about Bob.
	dave := card("dave", "videokey-dave")
	require.Nil(t, onNowAt(p, []apiStream{dave}, schedule, now, false).Streams[0].NowPlaying)
}

func TestWhatIsOnIsNeverOnATwentyFourSevenChannel(t *testing.T) {
	p, store := communityPlugin(t)
	now := time.Date(2026, 10, 1, 21, 30, 0, 0, time.UTC)
	nowMS := now.UnixMilli()

	store[kvNowPlaying] = mustJSON(t, nowPlayingDoc{Version: 1, Entries: map[string]nowPlayingEntry{
		"villa": {Category: "game", Title: "Arma 3", SetAt: nowMS - 60000, Aired: true},
	}})
	heat := &scheduleMedia{Ref: "tmdb:movie:949", Kind: "movie", Title: "Heat", Link: "https://www.imdb.com/title/tt0113277/", Source: "TMDB"}
	schedule := []scheduleItem{{ID: "a", UserID: "villa", Title: "Film", StartsAt: nowMS - 60000, Media: heat}}

	// A channel attributed to the very person who said it, with a plan of
	// theirs on too.
	channel := card("villa", "videokey-watts")
	channel.AlwaysOn = true
	unnamed := apiStream{StreamKey: "videokey-other", Handle: "h-other", Title: "Stream abc"}

	current := onNowAt(p, []apiStream{channel, unnamed}, schedule, now, true)
	for _, stream := range current.Streams {
		require.Nil(t, stream.NowPlaying, stream.Handle)
	}

	encoded, err := json.Marshal(current)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "nowPlaying")
}

// It lasts while the stream does, rides out a reconnect, and goes once the
// stream has been off the air for the offline grace. Set here before the first
// poll that sees the stream, which is what marks it as aired; see the test
// after this one for a choice saved while the panel already shows it live.
func TestWhatIsOnClearsItselfOnceTheStreamHasBeenOffPastTheGrace(t *testing.T) {
	p, store := communityPlugin(t)
	p.setConfiguration(graceConfig(2))
	linkStream(store, "villa")

	require.Equal(t, http.StatusOK, sayWhatIsOn(t, p, "villa", map[string]any{"category": "game", "title": "Arma 3"}).Code)
	require.False(t, storedNowPlaying(t, store)["villa"].Aired, "no poll has seen the stream yet")

	villa := card("villa", "videokey-villa")
	at := time.Now()
	poll := func(streams ...apiStream) *snapshot {
		at = at.Add(10 * time.Second)

		return onNowAt(p, streams, nil, at, true)
	}

	require.Equal(t, "Arma 3", poll(villa).Streams[0].NowPlaying.Title)
	require.True(t, storedNowPlaying(t, store)["villa"].Aired, "seen on the air once, and marked")

	// Off for one poll — OBS reconnecting — and back.
	poll()
	require.Contains(t, storedNowPlaying(t, store), "villa")
	require.Equal(t, "Arma 3", poll(villa).Streams[0].NowPlaying.Title)

	// The count starts again after the reconnect: one poll off is not two.
	poll()
	require.Contains(t, storedNowPlaying(t, store), "villa")

	// A poll that could not read the listing is no evidence either way.
	onNowAt(p, nil, nil, at.Add(5*time.Second), false)
	require.Contains(t, storedNowPlaying(t, store), "villa")

	poll()
	require.NotContains(t, storedNowPlaying(t, store), "villa", "gone after two polls off the air")

	code, shown := askWhatIsOn(t, p, "villa")
	require.Equal(t, http.StatusOK, code)
	require.Nil(t, shown)

	// Back on the air later: nothing stale comes back with them.
	require.Nil(t, poll(villa).Streams[0].NowPlaying)
}

// Saved while the panel already shows the stream live, it counts as aired at
// once, before any poll. So if Broadcast Box cannot be reached for a while
// after that, and the stream is gone when it can, the choice goes after the
// offline grace rather than waiting out twelve hours as one set for a stream
// that has not started.
func TestWhatIsOnSavedWhileLiveCountsAsAiredAtOnce(t *testing.T) {
	p, store := communityPlugin(t)
	p.setConfiguration(graceConfig(2))
	linkStream(store, "villa")

	villa := card("villa", "videokey-villa")
	p.setSnapshot(&snapshot{Streams: []apiStream{villa}, Configured: true})

	require.Equal(t, http.StatusOK, sayWhatIsOn(t, p, "villa", map[string]any{"category": "game", "title": "Arma 3"}).Code)
	require.True(t, storedNowPlaying(t, store)["villa"].Aired, "straight after the save, before any poll")

	// Polls that cannot read the listing, and then two that can and no
	// longer list the stream: gone after the grace.
	at := time.Now()
	for i := 1; i <= 3; i++ {
		onNowAt(p, nil, nil, at.Add(time.Duration(i)*10*time.Second), false)
	}
	require.Contains(t, storedNowPlaying(t, store), "villa")

	onNowAt(p, nil, nil, at.Add(40*time.Second), true)
	require.Contains(t, storedNowPlaying(t, store), "villa")
	onNowAt(p, nil, nil, at.Add(50*time.Second), true)
	require.NotContains(t, storedNowPlaying(t, store), "villa", "gone after two polls off the air, not twelve hours")
}

// Set before going live, it waits for the stream, and then behaves as above.
// For a stream that never comes it lapses after twelve hours, whether or not
// the poller has taken it down yet.
func TestWhatIsOnThatNeverAirsLapsesAfterTwelveHours(t *testing.T) {
	p, store := communityPlugin(t)
	p.setConfiguration(graceConfig(2))
	linkStream(store, "villa")

	set := time.Now().Add(-11 * time.Hour)
	store[kvNowPlaying] = mustJSON(t, nowPlayingDoc{Version: 1, Entries: map[string]nowPlayingEntry{
		"villa": {Category: "movie", Title: "Heat", SetAt: set.UnixMilli()},
	}})

	// Not on the air for hours: the grace does not apply to a stream that
	// has not started.
	for i := 0; i < 5; i++ {
		onNowAt(p, nil, nil, set.Add(time.Duration(i+1)*time.Hour), true)
	}
	require.Contains(t, storedNowPlaying(t, store), "villa")

	code, shown := askWhatIsOn(t, p, "villa")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "Heat", shown.Title, "eleven hours in, it is still waiting")

	// Twelve hours: lapsed for the footer straight away...
	store[kvNowPlaying] = mustJSON(t, nowPlayingDoc{Version: 1, Entries: map[string]nowPlayingEntry{
		"villa": {Category: "movie", Title: "Heat", SetAt: time.Now().Add(-12*time.Hour - time.Second).UnixMilli()},
	}})
	_, shown = askWhatIsOn(t, p, "villa")
	require.Nil(t, shown)

	// ...and not shown on a stream that starts now, which the poller then
	// takes down rather than marking it as aired.
	villa := card("villa", "videokey-villa")
	require.Nil(t, onNowAt(p, []apiStream{villa}, nil, time.Now(), true).Streams[0].NowPlaying)
	require.NotContains(t, storedNowPlaying(t, store), "villa")

	// Whereas one that did air in time is kept for as long as the stream is.
	start := time.Now()
	store[kvNowPlaying] = mustJSON(t, nowPlayingDoc{Version: 1, Entries: map[string]nowPlayingEntry{
		"villa": {Category: "movie", Title: "Heat", SetAt: start.UnixMilli()},
	}})
	onNowAt(p, []apiStream{villa}, nil, start.Add(time.Hour), true)
	require.Equal(t, "Heat", onNowAt(p, []apiStream{villa}, nil, start.Add(14*time.Hour), true).Streams[0].NowPlaying.Title)
}

// The poller decides from what it read, and writes only if that is still
// what is stored: a fresh choice made in between is never what it takes down.
func TestAFreshChoiceIsNotTakenDownByAStaleDecision(t *testing.T) {
	p, store, faults := kvBackedPluginWithFaults(map[string][]byte{})
	p.setConfiguration(graceConfig(1))

	old := time.Now().Add(-time.Hour).UnixMilli()
	store[kvNowPlaying] = mustJSON(t, nowPlayingDoc{Version: 1, Entries: map[string]nowPlayingEntry{
		"villa": {Category: "game", Title: "Arma 3", SetAt: old, Aired: true},
	}})

	// The streamer picks something new between the poller's read and its
	// write.
	fresh := time.Now().UnixMilli()
	faults.interfere = func(key string, kv map[string][]byte) {
		if key != kvNowPlaying {
			return
		}

		faults.interfere = nil
		kv[key] = mustJSON(t, nowPlayingDoc{Version: 1, Entries: map[string]nowPlayingEntry{
			"villa": {Category: "movie", Title: "Heat", SetAt: fresh},
		}})
	}

	onNowAt(p, nil, nil, time.Now(), true)

	entries := storedNowPlaying(t, store)
	require.Contains(t, entries, "villa")
	require.Equal(t, "Heat", entries["villa"].Title)
	require.Equal(t, fresh, entries["villa"].SetAt)
}

// The snapshot is one object served to everybody, fingerprinted so an
// unchanged one sends nothing: what is on must come out the same every time
// from the same state, carry nothing viewer-specific, and never a stream key.
func TestWhatIsOnMakesTheSameSnapshotEveryTime(t *testing.T) {
	p, store := communityPlugin(t)
	now := time.Date(2026, 10, 1, 21, 30, 0, 0, time.UTC)
	nowMS := now.UnixMilli()

	heat := &scheduleMedia{Ref: "tmdb:movie:949", Kind: "movie", Title: "Heat", Year: "1995", Link: "https://www.imdb.com/title/tt0113277/", Source: "TMDB"}
	entries := map[string]nowPlayingEntry{}
	for _, userID := range []string{"bob", "dave", "erin", "caro", "villa", "alice"} {
		entries[userID] = nowPlayingEntry{Category: "movie", Title: "Heat with " + userID, Media: heat, SetAt: nowMS - 60000, Aired: true}
	}
	store[kvNowPlaying] = mustJSON(t, nowPlayingDoc{Version: 1, Entries: entries})

	streams := []apiStream{}
	for _, userID := range []string{"bob", "dave", "erin", "caro", "villa"} {
		streams = append(streams, card(userID, "videokey-"+userID))
	}

	first, err := json.Marshal(onNowAt(p, streams, nil, now, true))
	require.NoError(t, err)

	for i := 0; i < 20; i++ {
		again, err := json.Marshal(onNowAt(p, streams, nil, now, true))
		require.NoError(t, err)
		require.Equal(t, string(first), string(again))
	}

	require.NotContains(t, string(first), "videokey-")
	require.NotContains(t, string(first), "setAt", "when it was set is the server's business")
	require.NotContains(t, string(first), "aired")

	// The cards handed in are not written on: a failed poll hands in the
	// previous snapshot's own, which other requests may be reading.
	for _, stream := range streams {
		require.Nil(t, stream.NowPlaying)
	}
}

// A store that will not answer is not "nobody has said anything": the cards
// keep what was last known rather than all losing it at once.
func TestAStoreThatWillNotAnswerKeepsWhatIsOnAsItWas(t *testing.T) {
	p, store, faults := kvBackedPluginWithFaults(map[string][]byte{})
	p.setConfiguration(graceConfig(2))
	now := time.Now()

	store[kvNowPlaying] = mustJSON(t, nowPlayingDoc{Version: 1, Entries: map[string]nowPlayingEntry{
		"villa": {Category: "game", Title: "Arma 3", SetAt: now.UnixMilli() - 60000, Aired: true},
	}})
	villa := card("villa", "videokey-villa")
	require.Equal(t, "Arma 3", onNowAt(p, []apiStream{villa}, nil, now, true).Streams[0].NowPlaying.Title)

	faults.lock.Lock()
	faults.readFails[kvNowPlaying] = true
	faults.lock.Unlock()

	require.Equal(t, "Arma 3", onNowAt(p, []apiStream{villa}, nil, now.Add(10*time.Second), true).Streams[0].NowPlaying.Title)

	// And the footer is told the store did not answer, not that nothing is
	// set, which would invite them to set it again for nothing.
	linkStream(store, "villa")
	code, _ := askWhatIsOn(t, p, "villa")
	require.Equal(t, http.StatusServiceUnavailable, code)
}

// A document that will not decode is kept aside and replaced, as the
// schedule's is, rather than switching the feature off for good.
func TestAnUnreadableWhatIsOnIsSetAsideNotObeyed(t *testing.T) {
	p, store := communityPlugin(t)
	linkStream(store, "villa")
	store[kvNowPlaying] = []byte("{not json")

	require.Nil(t, onNowAt(p, []apiStream{card("villa", "videokey-villa")}, nil, time.Now(), true).Streams[0].NowPlaying)
	require.Equal(t, "{not json", string(store[kvQuarantinePrefix+kvNowPlaying]))

	require.Equal(t, http.StatusOK, sayWhatIsOn(t, p, "villa", map[string]any{"category": "game", "title": "Arma 3"}).Code)
	require.Contains(t, storedNowPlaying(t, store), "villa")
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()

	encoded, err := json.Marshal(value)
	require.NoError(t, err)

	return encoded
}

// End to end, through the poller itself: what is on reaches the card of the
// stream it was said for, and is taken down by the polls that see that
// stream gone, past the grace.
func TestAPollCarriesWhatIsOnAndTakesItDownAfterTheStream(t *testing.T) {
	var lock sync.Mutex
	onAir := true

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lock.Lock()
		defer lock.Unlock()

		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}

		status := []bbStream{}
		if onAir {
			stream := rawLive("videokey-bob")
			stream.StreamStart = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
			status = append(status, stream)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(status)
	}))
	defer origin.Close()

	p, store, _ := watchTimePlugin(t)
	p.httpClient = origin.Client()
	cfg := p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = origin.URL
	cfg.OfflineGraceChecks = 2
	p.setConfiguration(cfg)

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("KVList", mock.Anything, mock.Anything).Return([]string{}, nil)
	api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return()

	require.Equal(t, http.StatusOK, sayWhatIsOn(t, p, "uid-bob", map[string]any{"category": "game", "title": "ELDEN RING"}).Code)

	p.pollOnce(t.Context(), true)
	bob := cardFor(t, p.getSnapshot().Streams, p.streamHandle("videokey-bob"))
	require.NotNil(t, bob.NowPlaying)
	require.Equal(t, "ELDEN RING", bob.NowPlaying.Title)
	require.Equal(t, "game", bob.NowPlaying.Category)

	encoded, err := json.Marshal(p.getSnapshot())
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "videokey-bob", "never the stream key")

	lock.Lock()
	onAir = false
	lock.Unlock()

	p.pollOnce(t.Context(), true)
	require.Contains(t, storedNowPlaying(t, store), "uid-bob", "one poll off the air is inside the grace")

	p.pollOnce(t.Context(), true)
	require.NotContains(t, storedNowPlaying(t, store), "uid-bob", "two is past it")
}
