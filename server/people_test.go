package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The people index: one answer per person, with the rules applied once, for
// every client to look up (people.go).

// Who is live is the web panel's rule, in the web panel's words: one card
// each, the Broadcast Box one before the Twitch one, and never a 24/7
// channel, a paused stream or an entry another card covers.
func TestLiveHandlesFollowThePanelsRule(t *testing.T) {
	streams := []apiStream{
		{Handle: "dave-twitch", UserID: "dave", Source: "twitch"},
		{Handle: "dave-bb", UserID: "dave"},
		{Handle: "dave-twitch-again", UserID: "dave", Source: "twitch"},
		{Handle: "bob-twitch", UserID: "bob", Source: "twitch"},
		{Handle: "dan-paused", UserID: "dan", Paused: true},
		{Handle: "radio", UserID: "bot", AlwaysOn: true},
		{Handle: "erin-covered", UserID: "erin", Source: "twitch", CoveredBy: "erin-bb"},
		{Handle: "nobody"},
	}

	require.Equal(t, map[string]string{"dave": "dave-bb", "bob": "bob-twitch"}, liveHandles(streams))
	require.Empty(t, liveHandles(nil))

	// The people the panel's own check of this rule used (build/check-twitch.mjs,
	// before 3.5.0 moved the rule here): live on Twitch with Broadcast Box
	// paused is live, on the Twitch card.
	require.Equal(t, map[string]string{"u-victor": "v-bb", "u-erin": "n-tw", "u-dave": "m-tw"}, liveHandles([]apiStream{
		{Handle: "v-tw", UserID: "u-victor", Source: "twitch"},
		{Handle: "v-bb", UserID: "u-victor"},
		{Handle: "n-tw", UserID: "u-erin", Source: "twitch"},
		{Handle: "m-bb", UserID: "u-dave", Paused: true},
		{Handle: "m-tw", UserID: "u-dave", Source: "twitch"},
		{Handle: "a-tw", UserID: "u-bob", Source: "twitch", CoveredBy: "a-bb"},
		{Handle: "w", UserID: "u-watts", AlwaysOn: true},
	}))
}

// Somebody in voice twice is muted only when both of them are, and a name the
// roster could not match to an account is nobody's.
func TestInVoiceIsMutedOnlyWhenEverySessionIs(t *testing.T) {
	voice := &voiceView{Connected: true, People: []voicePerson{
		{Name: "Dave", UserID: "dave", Muted: true},
		{Name: "Dave (phone)", UserID: "dave"},
		{Name: "Bob", UserID: "bob", Muted: true},
		{Name: "Bob (phone)", UserID: "bob", Muted: true},
		{Name: "Dan", UserID: "dan"},
		{Name: "Guest", Muted: true},
	}}

	require.Equal(t, map[string]string{"dave": voiceOpen, "bob": voiceMuted, "dan": voiceOpen}, voiceByUser(voice))

	// The bridge down: nobody is known to be in voice, whoever was.
	voice.Connected = false
	require.Empty(t, voiceByUser(voice))
	require.Empty(t, voiceByUser(nil))
}

func TestThePeopleIndexIsOneEntryEachAndEmptyWhenQuiet(t *testing.T) {
	people := peopleIndex(
		[]apiStream{{Handle: "h-dave", UserID: "dave"}},
		&voiceView{Connected: true, People: []voicePerson{{Name: "Dave", UserID: "dave", Muted: true}, {Name: "Bob", UserID: "bob"}}},
		map[string]string{"dave": badgeLive, "dan": badgeBirthday},
	)

	require.Equal(t, map[string]personView{
		"dave": {Live: "h-dave", Voice: voiceMuted, Badge: badgeLive},
		"bob":  {Voice: voiceOpen},
		"dan":  {Badge: badgeBirthday},
	}, people)

	encoded, err := json.Marshal(people["bob"])
	require.NoError(t, err)
	require.JSONEq(t, `{"voice":"open"}`, string(encoded), "what has nothing to say is not sent")

	// Nobody is {}, never absent: "nobody" and "no index" must not look alike.
	quiet, err := json.Marshal(snapshot{Streams: []apiStream{}, People: peopleIndex(nil, nil, nil)})
	require.NoError(t, err)
	require.Contains(t, string(quiet), `"people":{}`)
}

// The index says whose custom status is the plugin's, so that no client ever
// has to match the words of the badge: while it is the plugin's, through the
// grace after the stream, and never once the person has taken the slot back.
func TestTheIndexSaysWhoseStatusIsThePlugins(t *testing.T) {
	p, store := badgePlugin(t)
	cfg := badgeConfig()

	p.reconcileBadges([]apiStream{liveStream("villa"), liveStream("dave")}, cfg, badgeClock, true)
	require.Equal(t, map[string]string{"villa": badgeLive, "dave": badgeLive}, p.wornBadges())

	// Dave sets a status of his own mid-broadcast: the slot is his again.
	store.set("dave", &model.CustomStatus{Emoji: "headphones", Text: "Ranked, no DMs"})
	p.reconcileBadges([]apiStream{liveStream("villa"), liveStream("dave")}, cfg, badgeClock, true)
	require.Equal(t, map[string]string{"villa": badgeLive}, p.wornBadges(), "a status somebody chose is never called the plugin's")

	// The streams stop. The badge stays through the grace, and so does the entry.
	p.reconcileBadges(nil, cfg, badgeClock, true)
	require.Equal(t, map[string]string{"villa": badgeLive}, p.wornBadges(), "still wearing it through the grace")
	// A poll off the clock never counts toward taking one off, and takes no entry away either.
	p.reconcileBadges(nil, cfg, badgeClock, false)
	require.Equal(t, map[string]string{"villa": badgeLive}, p.wornBadges())

	p.reconcileBadges(nil, cfg, badgeClock, true)
	require.Empty(t, p.wornBadges(), "handed back, and said so")
	require.Equal(t, "", store.emoji("villa"))
}

// Through the grace after a broadcast the badge is still on, and the index
// still says it is the plugin's. Not once its wearer has written a status of
// their own: those are their words from that poll on, on the clock or off it,
// and no client may be told to leave them out.
func TestAStatusTakenBackDuringTheGraceIsNotCalledThePlugins(t *testing.T) {
	p, store := badgePlugin(t)
	cfg := badgeConfig()
	cfg.OfflineGraceChecks = 4

	p.reconcileBadges([]apiStream{liveStream("villa")}, cfg, badgeClock, true)
	require.Equal(t, map[string]string{"villa": badgeLive}, p.wornBadges())

	// The stream ends; the grace begins.
	p.reconcileBadges(nil, cfg, badgeClock, true)
	require.Equal(t, map[string]string{"villa": badgeLive}, p.wornBadges())

	store.set("villa", &model.CustomStatus{Emoji: "pizza", Text: "Dinner"})
	p.reconcileBadges(nil, cfg, badgeClock, false)
	require.Empty(t, p.wornBadges(), "hers again, on a poll off the clock too")
	p.reconcileBadges(nil, cfg, badgeClock, true)
	require.Empty(t, p.wornBadges())
	require.Equal(t, "pizza", store.emoji("villa"), "and nothing of hers was touched")
}

// From the very first answer, before any poll has published: an index of
// nobody, never the silence of a server with no index.
func TestTheSnapshotBeforeTheFirstPollStillHasItsIndex(t *testing.T) {
	p := &Plugin{}

	encoded, err := json.Marshal(p.getSnapshot())
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"people":{}`)

	recorder := httptest.NewRecorder()
	p.handleStreams(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/streams", nil))
	require.Contains(t, recorder.Body.String(), `"people":{}`)
}

func TestTheIndexTellsACakeFromALiveBadge(t *testing.T) {
	p, store := badgePlugin(t)
	cfg := cakeConfig("villa")

	p.reconcileBadges([]apiStream{liveStream("villa"), liveStream("dave")}, cfg, badgeClock, true)
	require.Equal(t, birthdayEmoji, store.emoji("villa"))
	require.Equal(t, map[string]string{"villa": badgeBirthday, "dave": badgeLive}, p.wornBadges(), "the cake wins, and is said to be a cake")

	// Both switches off: every slot goes back, and nobody is said to wear anything.
	off := badgeConfig()
	off.EnableLiveBadge = false
	p.reconcileBadges([]apiStream{liveStream("dave")}, off, badgeClock, true)
	require.Empty(t, p.wornBadges())
}

// A status that could not be set is not said to be worn.
func TestABadgeThatCouldNotBeSetIsNotInTheIndex(t *testing.T) {
	p, store := badgePlugin(t)
	cfg := badgeConfig()

	store.failNextUpdate()
	p.reconcileBadges([]apiStream{liveStream("villa")}, cfg, badgeClock, true)
	require.Empty(t, p.wornBadges())

	p.reconcileBadges([]apiStream{liveStream("villa")}, cfg, badgeClock, true)
	require.Equal(t, map[string]string{"villa": badgeLive}, p.wornBadges(), "the next poll tries again")
}

// Every snapshot that goes out carries the index as of that moment, and the
// push carries the tag a reader can ask about later.
func TestEveryPublishedSnapshotHasItsPeopleAndItsTag(t *testing.T) {
	api := &plugintest.API{}
	api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return()

	p := &Plugin{}
	p.SetAPI(api)
	p.setWornBadges(map[string]string{"dave": badgeLive})

	p.publish(&snapshot{
		Configured: true, FetchedAt: 1,
		Streams: []apiStream{{Handle: "h-dave", UserID: "dave"}},
		Voice:   &voiceView{Connected: true, People: []voicePerson{{Name: "Bob", UserID: "bob", Muted: true}}},
	})

	require.Equal(t, map[string]personView{
		"dave": {Live: "h-dave", Badge: badgeLive},
		"bob":  {Voice: voiceMuted},
	}, p.getSnapshot().People)

	require.Len(t, api.Calls, 1)
	data, ok := api.Calls[0].Arguments.Get(1).(map[string]any)
	require.True(t, ok)
	tag, _ := data["tag"].(string)
	require.Len(t, tag, 16, "the push says which snapshot it is")

	var pushed snapshot
	require.NoError(t, json.Unmarshal([]byte(data["payload"].(string)), &pushed))
	require.Equal(t, "h-dave", pushed.People["dave"].Live)

	// Somebody leaving voice is a change, and a new tag.
	p.publish(&snapshot{Configured: true, FetchedAt: 2, Streams: []apiStream{{Handle: "h-dave", UserID: "dave"}}, Voice: &voiceView{Connected: true}})
	require.Len(t, api.Calls, 2)
	next, _ := api.Calls[1].Arguments.Get(1).(map[string]any)["tag"].(string)
	require.NotEqual(t, tag, next)
	require.NotContains(t, p.getSnapshot().People, "bob")
}

// A reader who still holds the snapshot it was last given is told so in a
// few bytes; one who does not is sent it, with the tag for next time.
func TestStreamsTellsAReaderWhoHasItThatNothingChanged(t *testing.T) {
	s := newAccessScene(t)
	s.api.On("PublishWebSocketEvent", wsEventStreams, mock.Anything, mock.Anything).Return().Maybe()

	s.p.publish(&snapshot{Configured: true, FetchedAt: 10, Streams: []apiStream{{Handle: "h1", Title: "Dave"}}})

	first := s.serve(http.MethodGet, "/api/v1/streams", sceneLocked, "", nil)
	require.Equal(t, http.StatusOK, first.Code)
	tag := first.Header().Get("ETag")
	require.Regexp(t, `^"[0-9a-f]{16}"$`, tag)
	require.Equal(t, "private, no-cache", first.Header().Get("Cache-Control"))
	require.Contains(t, first.Body.String(), `"h1"`)

	again := s.serve(http.MethodGet, "/api/v1/streams", sceneLocked, "", map[string]string{"If-None-Match": tag})
	require.Equal(t, http.StatusNotModified, again.Code)
	require.Empty(t, again.Body.String())
	require.Equal(t, tag, again.Header().Get("ETag"))

	// The same thing a poll later (only the time and a bitrate moved) is still unchanged.
	s.p.publish(&snapshot{Configured: true, FetchedAt: 20, Streams: []apiStream{{Handle: "h1", Title: "Dave", Bitrate: 5}}})
	require.Equal(t, http.StatusNotModified, s.serve(http.MethodGet, "/api/v1/streams", sceneLocked, "", map[string]string{"If-None-Match": tag}).Code)

	// A change anybody can see is sent, under a new tag. Another reader's old tag, or junk, gets the document.
	s.p.publish(&snapshot{Configured: true, FetchedAt: 30, Streams: []apiStream{{Handle: "h1", Title: "Dave"}, {Handle: "h2", Title: "Bob"}}})
	changed := s.serve(http.MethodGet, "/api/v1/streams", sceneLocked, "", map[string]string{"If-None-Match": tag})
	require.Equal(t, http.StatusOK, changed.Code)
	require.NotEqual(t, tag, changed.Header().Get("ETag"))
	require.Contains(t, changed.Body.String(), `"h2"`)
	require.Equal(t, http.StatusOK, s.serve(http.MethodGet, "/api/v1/streams", sceneLocked, "", map[string]string{"If-None-Match": `"nonsense"`}).Code)
}

// The one question a player asks while it plays, answered with nothing else:
// not the stream key, not the settings, not the connected accounts.
func TestAccessSaysOnlyWhetherYouMayWatch(t *testing.T) {
	s := newAccessScene(t)

	for userID, want := range map[string]string{sceneLocked: "locked", sceneLetIn: "letIn", sceneLinked: "linked"} {
		response := s.serve(http.MethodGet, "/api/v1/access", userID, "", nil)
		require.Equal(t, http.StatusOK, response.Code, userID)
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

		var body map[string]any
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		require.Equal(t, want, body["access"], userID)
		require.Contains(t, body, "configured")
		require.EqualValues(t, contractRevision, body["contract"])
		require.Equal(t, manifestVersion, body["version"])
		require.Equal(t, "Matty", body["name"])
		require.Equal(t, map[string]any{"emblem": false, "ghost": false}, body["artwork"])
		require.Equal(t, map[string]any{"state": "unsigned", "version": manifestVersion}, body["release"])
		require.Equal(t, map[string]any{"check": "report", "state": "none"}, body["app"])
		require.Equal(t, map[string]any{"state": "off", "reason": pushOffText}, body["push"])
		require.Len(t, body, 9, "the access, whether it is set up, which version this is and whether it is a signed release, its name and artwork, what the app check found, whether push is on, and nothing more")
	}

	require.Equal(t, http.StatusMethodNotAllowed, s.serve(http.MethodPost, "/api/v1/access", sceneLinked, "", map[string]string{"X-Requested-With": "XMLHttpRequest"}).Code)
}

// /me says which contract this server speaks and which VillaNET it is.
func TestMeSaysWhichContractItSpeaks(t *testing.T) {
	s := newAccessScene(t)

	response := s.serve(http.MethodGet, "/api/v1/me", sceneLocked, "", nil)
	require.Equal(t, http.StatusOK, response.Code)

	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.EqualValues(t, contractRevision, body["contract"])
	require.Equal(t, manifestVersion, body["version"])
}

// The same for everybody's Steam and Hytale: a reader who still holds the
// last answer is told nothing has changed, a locked one is still refused
// before anything is said, and a connection made since is a new answer.
func TestAccountsTellsAReaderWhoHasItThatNothingChanged(t *testing.T) {
	s := newAccountsScene(t)
	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3})

	// Steam was last read two minutes ago.
	store := s.p.accounts()
	store.lock.Lock()
	store.steamLastGood = s.now().Add(-2 * time.Minute)
	store.lock.Unlock()

	first := s.serve(http.MethodGet, "/api/v1/accounts", acLetIn, web(acLetIn), "", nil)
	require.Equal(t, http.StatusOK, first.Code)
	tag := first.Header().Get("ETag")
	require.Regexp(t, `^"[0-9a-f]{16}"$`, tag)
	require.Equal(t, "no-store", first.Header().Get("Cache-Control"), "still nobody's to keep but the reader's own")

	again := s.serve(http.MethodGet, "/api/v1/accounts", acLetIn, web(acLetIn), "", map[string]string{"If-None-Match": tag})
	require.Equal(t, http.StatusNotModified, again.Code)
	require.Empty(t, again.Body.String())

	// Steam is read again every minute, and a read that found nothing new
	// is not news: the answer must not move by itself, or nobody would ever
	// be told "unchanged".
	store.lock.Lock()
	store.steamLastGood = s.now().Add(-time.Minute)
	store.lock.Unlock()
	later := s.serve(http.MethodGet, "/api/v1/accounts", acLetIn, web(acLetIn), "", map[string]string{"If-None-Match": tag})
	require.Equal(t, http.StatusNotModified, later.Code, "a later read of Steam with nothing new in it")
	require.NotContains(t, first.Body.String(), `"at"`)

	// The gate comes first: a tag is no way in.
	locked := s.serve(http.MethodGet, "/api/v1/accounts", acDan, web(acDan), "", map[string]string{"If-None-Match": tag})
	require.Equal(t, http.StatusForbidden, locked.Code)

	s.connectSteam(t, acDave, daveSteamID)
	changed := s.serve(http.MethodGet, "/api/v1/accounts", acLetIn, web(acLetIn), "", map[string]string{"If-None-Match": tag})
	require.Equal(t, http.StatusOK, changed.Code)
	require.NotEqual(t, tag, changed.Header().Get("ETag"))
}
