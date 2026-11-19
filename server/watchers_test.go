package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// A plugin with live previews and faces on, three people live and one 24/7
// channel, and an account behind everybody who watches in these tests: one who
// goes by a nickname, one by their full name, one by their username alone, one
// whose account cannot be read and one who has been deactivated.
func watchersPlugin(t *testing.T) *Plugin {
	t.Helper()

	p := stillsPlugin(t)
	cfg := &configuration{AlwaysOnStreamKeys: "videokey-watts", LivePreviews: true, ShowWatchers: true}
	cfg.normalize()
	p.setConfiguration(cfg)

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("GetUser", "uid-a").Return(&model.User{Id: "uid-a", Username: "bob", Nickname: "Bob"}, nil)
	api.On("GetUser", "uid-b").Return(&model.User{Id: "uid-b", Username: "erin", FirstName: "Erin", LastName: "Cat"}, nil)
	api.On("GetUser", "uid-c").Return(&model.User{Id: "uid-c", Username: "dave"}, nil)
	api.On("GetUser", "uid-gone").Return(nil, &model.AppError{Message: "not found", StatusCode: http.StatusNotFound})
	api.On("GetUser", "uid-left").Return(&model.User{Id: "uid-left", Username: "left", DeleteAt: 1}, nil)

	return p
}

// A live stream as Broadcast Box lists it, with these sessions open on it.
func rawWatched(key string, sessionIDs ...string) bbStream {
	stream := rawLive(key)
	for _, id := range sessionIDs {
		stream.Sessions = append(stream.Sessions, bbSession{ID: id})
	}

	return stream
}

// The card a poll made for handle.
func cardFor(t *testing.T, streams []apiStream, handle string) apiStream {
	t.Helper()

	for _, stream := range streams {
		if stream.Handle == handle {
			return stream
		}
	}

	require.Fail(t, "the stream is not listed")

	return apiStream{}
}

// Just the names on a card, in the order it shows them.
func namesOn(card apiStream) []string {
	names := []string{}
	for _, watcher := range card.Watchers {
		names = append(names, watcher.Who)
	}

	return names
}

// How many times the user store has been asked about userID.
func getUserCalls(t *testing.T, p *Plugin, userID string) int {
	t.Helper()

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	count := 0
	for _, call := range api.Calls {
		if call.Method == "GetUser" && call.Arguments.String(0) == userID {
			count++
		}
	}

	return count
}

// Who the store remembers, by session.
func rememberedSessions(s *watcherStore) map[string]string {
	s.lock.Lock()
	defer s.lock.Unlock()

	remembered := map[string]string{}
	for id, session := range s.sessions {
		remembered[id] = session.userID
	}

	return remembered
}

func TestOnlyASuccessfulExchangeThatIsNotAPeekIsRecorded(t *testing.T) {
	origin, issued := fakeWHEPOrigin(t)
	defer origin.Close()

	p := watchersPlugin(t)
	p.httpClient = origin.Client()
	cfg := p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = origin.URL
	p.setConfiguration(cfg)

	dave := p.streamHandle("videokey-dave")
	store := p.watchers()

	// An ordinary viewer: remembered, under the session Broadcast Box opened.
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, "", "uid-a").Code)
	require.Equal(t, map[string]string{issued()[0]: "uid-a"}, rememberedSessions(store))

	// A peek left out of the count is nobody's audience, so nobody's face.
	peek := p.stills().claim(dave, "uid-b", stillModePeek, time.Now())
	require.NotEmpty(t, peek.Lease)
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, peek.Lease, "uid-b").Code)
	require.Len(t, issued(), 2)
	require.Equal(t, map[string]string{issued()[0]: "uid-a"}, rememberedSessions(store))

	// The same lease again opens a session that IS counted, so it is a
	// watcher like any other.
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, peek.Lease, "uid-b").Code)
	require.Equal(t, map[string]string{issued()[0]: "uid-a", issued()[2]: "uid-b"}, rememberedSessions(store))

	// Broadcast Box refused the offer, since this origin only plays Dave's
	// stream: no session, so nothing to remember.
	require.Equal(t, http.StatusBadGateway, whepRequest(t, p, p.streamHandle("videokey-bob"), "", "uid-c").Code)
	require.Len(t, rememberedSessions(store), 2)

	// Nor for a stream that is not live, which never reaches the origin.
	require.Equal(t, http.StatusNotFound, whepRequest(t, p, "not-a-handle", "", "uid-c").Code)
	require.Len(t, rememberedSessions(store), 2)

	// With the setting off, nothing is kept about who watches what.
	off := cfg.Clone()
	off.ShowWatchers = false
	p.setConfiguration(off)
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, "", "uid-c").Code)
	require.Len(t, issued(), 4)
	require.Len(t, rememberedSessions(store), 2)

	// Answered, but with no Location to read the session from: it plays, and
	// there is no id to find it by in the listing, so nothing is kept.
	bare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/sdp")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "v=0\r\na=candidate:1 1 udp 2130706431 203.0.113.7 3478 typ host\r\n")
	}))
	defer bare.Close()

	p.httpClient = bare.Client()
	elsewhere := cfg.Clone()
	elsewhere.BroadcastBoxURL = bare.URL
	p.setConfiguration(elsewhere)
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, "", "uid-c").Code)
	require.Len(t, rememberedSessions(store), 2)
}

func TestAWatcherIsListedOnlyWhileBroadcastBoxListsTheSession(t *testing.T) {
	p := watchersPlugin(t)
	cfg := p.getConfiguration()
	dave := p.streamHandle("videokey-dave")

	p.watchers().record("session-a", "uid-a", dave, time.Now())

	// Relayed, but not listed yet: no face. Remembered all the same, since
	// Broadcast Box may simply not have got round to listing it.
	card := cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave")}, cfg), dave)
	require.Empty(t, card.Watchers)
	require.Zero(t, card.Viewers)
	require.Contains(t, rememberedSessions(p.watchers()), "session-a")

	// Listed: a face, and a count that agrees with it.
	card = cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave", "session-a")}, cfg), dave)
	require.Equal(t, []apiWatcher{{UserID: "uid-a", Who: "Bob"}}, card.Watchers)
	require.Equal(t, 1, card.Viewers)

	// Closed: gone from the card with the count.
	card = cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave")}, cfg), dave)
	require.Empty(t, card.Watchers)
	require.Zero(t, card.Viewers)

	// Somebody watching some other way is counted and has no face.
	card = cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave", "session-a", "not-relayed")}, cfg), dave)
	require.Equal(t, []string{"Bob"}, namesOn(card))
	require.Equal(t, 2, card.Viewers)

	// And a face goes on the stream it was opened for, and no other.
	streams := p.decorate([]bbStream{rawWatched("videokey-dave"), rawWatched("videokey-bob", "session-a")}, cfg)
	require.Empty(t, cardFor(t, streams, p.streamHandle("videokey-bob")).Watchers)
}

func TestSomebodyWithTwoSessionsIsListedOnce(t *testing.T) {
	p := watchersPlugin(t)
	dave := p.streamHandle("videokey-dave")
	store := p.watchers()

	started := time.Now().Add(-time.Minute)
	store.record("a-first", "uid-a", dave, started)
	store.record("b-only", "uid-b", dave, started.Add(time.Second))
	store.record("a-second", "uid-a", dave, started.Add(2*time.Second))

	card := cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave", "a-second", "b-only", "a-first")}, p.getConfiguration()), dave)
	require.Equal(t, []apiWatcher{{UserID: "uid-a", Who: "Bob"}, {UserID: "uid-b", Who: "Erin Cat"}}, card.Watchers)
	require.Equal(t, 3, card.Viewers, "every session is still an audience")
	require.Zero(t, card.Others, "but not another person beside the faces")
}

// The count beside the faces is of people. Broadcast Box counts sessions, and
// the panel opens one for every tile, so the viewer count less the faces made
// somebody with a stream open twice into a stranger.
func TestTheCountBesideTheFacesIsPeopleNotSessions(t *testing.T) {
	p := watchersPlugin(t)
	cfg := p.getConfiguration()
	dave := p.streamHandle("videokey-dave")
	store := p.watchers()

	// Recent enough that a session left out of one poll's listing is not
	// forgotten by it before the next.
	started := time.Now().Add(-10 * time.Second)
	store.record("a-tab-1", "uid-a", dave, started)
	store.record("a-tab-2", "uid-a", dave, started.Add(time.Second))

	// Bob, twice over: two viewers, one face, and nobody else.
	card := cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave", "a-tab-1", "a-tab-2")}, cfg), dave)
	require.Equal(t, []string{"Bob"}, namesOn(card))
	require.Equal(t, 2, card.Viewers)
	require.Zero(t, card.Others, "a second tab is not a second person")

	// Somebody on the public site: a session nobody can be named for.
	card = cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave", "a-tab-1", "a-tab-2", "not-relayed")}, cfg), dave)
	require.Equal(t, 3, card.Viewers)
	require.Equal(t, 1, card.Others)

	// People the plugin knows are watching and has no name to show for: one
	// whose account cannot be read, in two tabs, and one deactivated. Each is
	// one more, however many sessions they have.
	store.record("gone-1", "uid-gone", dave, started.Add(2*time.Second))
	store.record("gone-2", "uid-gone", dave, started.Add(3*time.Second))
	store.record("left", "uid-left", dave, started.Add(4*time.Second))

	card = cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave", "a-tab-1", "a-tab-2", "gone-1", "gone-2", "left", "not-relayed")}, cfg), dave)
	require.Equal(t, []string{"Bob"}, namesOn(card))
	require.Equal(t, 6, card.Viewers)
	require.Equal(t, 3, card.Others, "two people with no name to show, and one session with nobody behind it")

	encoded, err := json.Marshal(card)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"others":3`)

	// With nobody named there are no faces for a count to go beside, so
	// there is none, however many are watching: the viewer count says it.
	card = cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave", "gone-1", "left", "not-relayed")}, cfg), dave)
	require.Nil(t, card.Watchers)
	require.Zero(t, card.Others)
	require.Equal(t, 3, card.Viewers)

	encoded, err = json.Marshal(card)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "others", "absent, not zero")

	// Whatever mix is watching, the count is exactly the sessions nobody is
	// behind and the people nobody named, never below zero, and never makes
	// the faces and the count together more than the sessions they are drawn
	// from.
	store.record("b-1", "uid-b", dave, started.Add(5*time.Second))
	pool := []string{"a-tab-1", "a-tab-2", "b-1", "gone-1", "left", "not-relayed", "also-not-relayed"}
	for mask := 0; mask < 1<<len(pool); mask++ {
		listed := []string{}
		has := map[string]bool{}
		for i, id := range pool {
			if mask&(1<<i) != 0 {
				listed = append(listed, id)
				has[id] = true
			}
		}

		named := 0
		if has["a-tab-1"] || has["a-tab-2"] {
			named++
		}
		if has["b-1"] {
			named++
		}

		want := 0
		if named > 0 {
			for _, unnamed := range []string{"gone-1", "left", "not-relayed", "also-not-relayed"} {
				if has[unnamed] {
					want++
				}
			}
		}

		card := cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave", listed...)}, cfg), dave)
		require.Len(t, card.Watchers, named, listed)
		require.Equal(t, want, card.Others, listed)
		require.GreaterOrEqual(t, card.Others, 0, listed)
		require.LessOrEqual(t, len(card.Watchers)+card.Others, card.Viewers, listed)
	}

	// Switched off, there is no count either, on a fresh card or on one
	// carried forward from before.
	carried := &snapshot{Streams: []apiStream{cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave", "a-tab-1", "not-relayed")}, cfg), dave)}}
	require.Equal(t, 1, carried.Streams[0].Others)

	off := cfg.Clone()
	off.ShowWatchers = false
	card = cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave", "a-tab-1", "not-relayed")}, off), dave)
	require.Nil(t, card.Watchers)
	require.Zero(t, card.Others)

	p.dropWatchersWhenOff(carried, off)
	require.Nil(t, carried.Streams[0].Watchers)
	require.Zero(t, carried.Streams[0].Others)
}

func TestWatchersKeepTheirPlaceFromPollToPoll(t *testing.T) {
	p := watchersPlugin(t)
	cfg := p.getConfiguration()
	dave := p.streamHandle("videokey-dave")
	store := p.watchers()

	started := time.Now().Add(-10 * time.Second)
	store.record("c-1", "uid-c", dave, started)
	store.record("a-1", "uid-a", dave, started.Add(time.Second))
	store.record("b-1", "uid-b", dave, started.Add(2*time.Second))

	// Earliest first, whatever order Broadcast Box lists the sessions in.
	for _, order := range [][]string{{"b-1", "a-1", "c-1"}, {"a-1", "c-1", "b-1"}, {"c-1", "b-1", "a-1"}} {
		card := cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave", order...)}, cfg), dave)
		require.Equal(t, []string{"dave", "Bob", "Erin Cat"}, namesOn(card), order)
	}

	// Dave's browser renegotiates: a new session, and the old one closes. He
	// has been watching all along, so he keeps his place.
	store.record("c-2", "uid-c", dave, started.Add(3*time.Second))
	card := cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave", "b-1", "a-1", "c-2")}, cfg), dave)
	require.Equal(t, []string{"dave", "Bob", "Erin Cat"}, namesOn(card))

	// Even once the old session has been forgotten.
	store.lock.Lock()
	delete(store.sessions, "c-1")
	store.lock.Unlock()
	card = cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave", "b-1", "a-1", "c-2")}, cfg), dave)
	require.Equal(t, []string{"dave", "Bob", "Erin Cat"}, namesOn(card))

	// Somebody who comes back after they were forgotten starts again at the
	// back.
	store.lock.Lock()
	delete(store.sessions, "a-1")
	store.lock.Unlock()
	store.record("a-2", "uid-a", dave, time.Now())
	card = cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave", "a-2", "b-1", "c-2")}, cfg), dave)
	require.Equal(t, []string{"dave", "Erin Cat", "Bob"}, namesOn(card))
}

func TestACardNamesTwentyWatchersAtMost(t *testing.T) {
	p := watchersPlugin(t)
	dave := p.streamHandle("videokey-dave")

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("GetUser", mock.Anything).Return(func(userID string) *model.User {
		return &model.User{Id: userID, Username: userID}
	}, nil)

	started := time.Now().Add(-time.Minute)
	sessions := []string{}
	for i := 0; i < maxWatchersPerStream+5; i++ {
		id := fmt.Sprintf("session-%02d", i)
		p.watchers().record(id, fmt.Sprintf("uid-%02d", i), dave, started.Add(time.Duration(i)*time.Second))
		sessions = append(sessions, id)
	}

	card := cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave", sessions...)}, p.getConfiguration()), dave)
	require.Len(t, card.Watchers, maxWatchersPerStream)
	require.Equal(t, "uid-00", card.Watchers[0].UserID, "the earliest are the ones named")
	require.Equal(t, maxWatchersPerStream+5, card.Viewers, "the rest are counted")
	require.Equal(t, 5, card.Others, "and are the count beside the faces")

	// Somebody past the cap with a second tab open is still one of them.
	p.watchers().record("session-24-again", "uid-24", dave, time.Now())
	card = cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave", append(sessions, "session-24-again")...)}, p.getConfiguration()), dave)
	require.Len(t, card.Watchers, maxWatchersPerStream)
	require.Equal(t, maxWatchersPerStream+6, card.Viewers)
	require.Equal(t, 5, card.Others)
}

func TestWithShowWatchersOffACardNamesNobody(t *testing.T) {
	p := watchersPlugin(t)
	dave := p.streamHandle("videokey-dave")
	raw := []bbStream{rawWatched("videokey-dave", "session-a"), rawWatched("videokey-watts", "session-b")}

	p.watchers().record("session-a", "uid-a", dave, time.Now())
	p.watchers().record("session-b", "uid-b", p.streamHandle("videokey-watts"), time.Now())

	on, err := json.Marshal(p.decorate(raw, p.getConfiguration()))
	require.NoError(t, err)
	require.Contains(t, string(on), `"watchers":[{"userId":"uid-a","who":"Bob"}]`)
	require.Contains(t, string(on), `"watchers":[{"userId":"uid-b","who":"Erin Cat"}]`, "a 24/7 channel has an audience too")
	_, named := p.watchers().names.get("uid-a")
	require.True(t, named)

	off := p.getConfiguration().Clone()
	off.ShowWatchers = false

	streams := p.decorate(raw, off)
	encoded, err := json.Marshal(streams)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "watchers", "absent, not empty")
	require.Equal(t, 1, cardFor(t, streams, dave).Viewers, "only how many")

	// And nothing is kept about who was watching what, nor about who was
	// looked up: that alone is still a list of who watched lately.
	require.Empty(t, rememberedSessions(p.watchers()))
	for _, userID := range []string{"uid-a", "uid-b"} {
		_, named = p.watchers().names.get(userID)
		require.False(t, named, userID)
	}

	// On, with nobody the plugin can name: absent as well.
	card := cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave", "not-relayed")}, p.getConfiguration()), dave)
	require.Nil(t, card.Watchers)
	require.Equal(t, 1, card.Viewers)
}

// Switching the setting off restarts the poller, and the poll that follows can
// be one that cannot reach Broadcast Box and so carries the last cards forward.
// Those were made with the setting on, and their faces must not ride along.
func TestShowWatchersOffHoldsWhileBroadcastBoxIsDown(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	defer down.Close()

	p := watchersPlugin(t)
	p.httpClient = down.Client()
	cfg := p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = down.URL
	p.setConfiguration(cfg)

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("KVList", mock.Anything, mock.Anything).Return([]string{}, nil)
	api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return()

	dave := p.streamHandle("videokey-dave")
	store := p.watchers()
	store.record("session-a", "uid-a", dave, time.Now())
	last := p.decorate([]bbStream{rawWatched("videokey-dave", "session-a")}, cfg)
	p.setSnapshot(&snapshot{Streams: last, Configured: true})

	// With the setting still on, an outage keeps the faces with the rest of
	// the card and forgets nobody: no listing is no evidence of anything.
	p.pollOnce(t.Context(), true)
	current := p.getSnapshot()
	require.NotEmpty(t, current.Error)
	require.Equal(t, []string{"Bob"}, namesOn(cardFor(t, current.Streams, dave)))
	require.Contains(t, rememberedSessions(store), "session-a")

	// Switched off: the same cards, with nobody on them, and nothing kept.
	off := cfg.Clone()
	off.ShowWatchers = false
	p.setConfiguration(off)
	p.pollOnce(t.Context(), true)

	current = p.getSnapshot()
	require.NotEmpty(t, current.Error)
	require.Equal(t, 1, cardFor(t, current.Streams, dave).Viewers, "only how many")
	encoded, err := json.Marshal(current)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "watchers")
	require.Empty(t, rememberedSessions(store))
	_, named := store.names.get("uid-a")
	require.False(t, named)

	// On a copy: the cards published before are not changed under whoever is
	// still reading them.
	require.Equal(t, []string{"Bob"}, namesOn(cardFor(t, last, dave)))

	// And with nothing to reach Broadcast Box by, the same.
	store.record("session-b", "uid-b", dave, time.Now())
	unset := off.Clone()
	unset.BroadcastBoxURL = ""
	p.setConfiguration(unset)
	p.pollOnce(t.Context(), true)
	require.False(t, p.getSnapshot().Configured)
	require.Empty(t, rememberedSessions(store))
}

func TestWatcherNamesAreLookedUpOnceAndKept(t *testing.T) {
	p := watchersPlugin(t)
	cfg := p.getConfiguration()
	dave := p.streamHandle("videokey-dave")
	bob := p.streamHandle("videokey-bob")
	store := p.watchers()

	started := time.Now().Add(-time.Minute)
	store.record("a-dave", "uid-a", dave, started)
	store.record("b-dave", "uid-b", dave, started.Add(time.Second))
	store.record("c-dave", "uid-c", dave, started.Add(2*time.Second))
	store.record("a-bob", "uid-a", bob, started)

	raw := []bbStream{rawWatched("videokey-dave", "a-dave", "b-dave", "c-dave"), rawWatched("videokey-bob", "a-bob")}

	for poll := 0; poll < 5; poll++ {
		streams := p.decorate(raw, cfg)

		// The same name a streamer is given: a nickname, else the full
		// name, else the username.
		require.Equal(t, []string{"Bob", "Erin Cat", "dave"}, namesOn(cardFor(t, streams, dave)))
		require.Equal(t, []string{"Bob"}, namesOn(cardFor(t, streams, bob)))
	}

	// Five polls, two streams: one lookup per person.
	require.Equal(t, 1, getUserCalls(t, p, "uid-a"))
	require.Equal(t, 1, getUserCalls(t, p, "uid-b"))
	require.Equal(t, 1, getUserCalls(t, p, "uid-c"))
}

func TestAWatcherWhoCannotBeLookedUpIsLeftOut(t *testing.T) {
	p := watchersPlugin(t)
	cfg := p.getConfiguration()
	dave := p.streamHandle("videokey-dave")
	store := p.watchers()

	started := time.Now().Add(-time.Minute)
	store.record("gone", "uid-gone", dave, started)
	store.record("left", "uid-left", dave, started.Add(time.Second))
	store.record("bob", "uid-a", dave, started.Add(2*time.Second))

	raw := []bbStream{rawWatched("videokey-dave", "gone", "left", "bob")}

	for poll := 0; poll < 3; poll++ {
		card := cardFor(t, p.decorate(raw, cfg), dave)
		require.Equal(t, []apiWatcher{{UserID: "uid-a", Who: "Bob"}}, card.Watchers)
		require.Equal(t, 3, card.Viewers, "still counted, just not named")
		require.Equal(t, 2, card.Others, "and counted beside the face that is shown")
	}

	// A failed lookup is believed for a while too, so a user store having a
	// bad moment is not asked again on every poll.
	require.Equal(t, 1, getUserCalls(t, p, "uid-gone"))
	require.Equal(t, 1, getUserCalls(t, p, "uid-left"))
}

func TestARememberedSessionIsForgottenAMinuteAfterItWasLastHeardOf(t *testing.T) {
	s := newWatcherStore()
	s.record("never-listed", "uid-a", "h1", stillEpoch)
	s.record("listed", "uid-b", "h1", stillEpoch)

	// Broadcast Box lists one of them half a minute in.
	s.observe([]bbStream{{Sessions: []bbSession{{ID: "listed"}}}}, stillEpoch.Add(30*time.Second))

	s.sweep(stillEpoch.Add(watcherSessionMemory-time.Second), true)
	require.Len(t, rememberedSessions(s), 2)

	// Never listed within a minute of being recorded: forgotten.
	s.sweep(stillEpoch.Add(watcherSessionMemory), true)
	require.Equal(t, map[string]string{"listed": "uid-b"}, rememberedSessions(s))

	// And a minute after it was last listed, so is the other.
	s.sweep(stillEpoch.Add(30*time.Second+watcherSessionMemory-time.Second), true)
	require.Len(t, rememberedSessions(s), 1)
	s.sweep(stillEpoch.Add(30*time.Second+watcherSessionMemory), true)
	require.Empty(t, rememberedSessions(s))

	// Through a poll: the sessions are looked for before anything is swept,
	// so one that is still open is kept however long ago it was opened, and
	// one that has closed is not.
	p := watchersPlugin(t)
	dave := p.streamHandle("videokey-dave")
	long := time.Now().Add(-2 * watcherSessionMemory)
	p.watchers().record("still-open", "uid-a", dave, long)
	p.watchers().record("closed", "uid-b", dave, long)

	card := cardFor(t, p.decorate([]bbStream{rawWatched("videokey-dave", "still-open")}, p.getConfiguration()), dave)
	require.Equal(t, []string{"Bob"}, namesOn(card))
	require.Equal(t, map[string]string{"still-open": "uid-a"}, rememberedSessions(p.watchers()))

	// A session recorded a moment ago, and not listed yet, is not swept.
	p.watchers().record("just-opened", "uid-c", dave, time.Now())
	p.decorate([]bbStream{rawWatched("videokey-dave", "still-open")}, p.getConfiguration())
	require.Contains(t, rememberedSessions(p.watchers()), "just-opened")

	// Nor is one on a stream that is off the air for a moment: it is still
	// in the listing, with no video.
	p.watchers().record("reconnecting", "uid-c", p.streamHandle("videokey-bob"), long)
	p.decorate([]bbStream{
		rawWatched("videokey-dave", "still-open"),
		{StreamKey: "videokey-bob", Sessions: []bbSession{{ID: "reconnecting"}}},
	}, p.getConfiguration())
	require.Contains(t, rememberedSessions(p.watchers()), "reconnecting")
}

func TestTheWatcherSessionListIsBounded(t *testing.T) {
	s := newWatcherStore()

	for i := 0; i < maxWatcherSessions; i++ {
		s.record(fmt.Sprintf("s-%d", i), fmt.Sprintf("uid-%d", i), "h1", stillEpoch.Add(time.Duration(i)*time.Millisecond))
	}
	require.Len(t, rememberedSessions(s), maxWatcherSessions)

	// The earliest session is still being listed, so the next one along is
	// the one heard of longest ago, and it makes room.
	s.observe([]bbStream{{Sessions: []bbSession{{ID: "s-0"}}}}, stillEpoch.Add(time.Hour))

	for i := maxWatcherSessions; i < maxWatcherSessions+10; i++ {
		s.record(fmt.Sprintf("s-%d", i), fmt.Sprintf("uid-%d", i), "h1", stillEpoch.Add(time.Duration(i)*time.Millisecond))
	}

	remembered := rememberedSessions(s)
	require.Len(t, remembered, maxWatcherSessions)
	require.Contains(t, remembered, "s-0")
	for i := 1; i <= 10; i++ {
		require.NotContains(t, remembered, fmt.Sprintf("s-%d", i))
	}
	require.Contains(t, remembered, "s-11")
	require.Contains(t, remembered, fmt.Sprintf("s-%d", maxWatcherSessions+9))
}

// The whole round trip, against a Broadcast Box that answers both WHEP and
// /api/status: faces on the cards, a peek on neither, and nothing in the
// snapshot that is a stream key or a Broadcast Box session.
func TestAPollNamesWhoIsWatchingAndGivesAwayNothingElse(t *testing.T) {
	var lock sync.Mutex
	open := map[string][]string{}
	issued := 0

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lock.Lock()
		defer lock.Unlock()

		switch r.URL.Path {
		case "/api/whep":
			key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			issued++
			id := fmt.Sprintf("cccccccc-0000-0000-0000-%012d", issued)
			open[key] = append(open[key], id)

			w.Header().Set("Location", "/api/whep/"+id)
			w.Header().Set("Content-Type", "application/sdp")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, "v=0\r\na=candidate:1 1 udp 2130706431 203.0.113.7 3478 typ host\r\n")

		case "/api/status":
			status := []bbStream{}
			for _, key := range []string{"videokey-dave", "videokey-watts"} {
				status = append(status, rawWatched(key, open[key]...))
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(status)

		default:
			http.NotFound(w, r)
		}
	}))
	defer origin.Close()

	p := watchersPlugin(t)
	p.httpClient = origin.Client()
	cfg := p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = origin.URL
	p.setConfiguration(cfg)

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("KVList", mock.Anything, mock.Anything).Return([]string{}, nil)
	api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return()

	dave := p.streamHandle("videokey-dave")
	watts := p.streamHandle("videokey-watts")

	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, "", "uid-b").Code)
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, "", "uid-a").Code)
	require.Equal(t, http.StatusOK, whepRequest(t, p, watts, "", "uid-c").Code)

	peek := p.stills().claim(dave, "uid-c", stillModePeek, time.Now())
	require.NotEmpty(t, peek.Lease)
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, peek.Lease, "uid-c").Code)

	p.pollOnce(t.Context(), true)

	current := p.getSnapshot()
	require.Equal(t, []string{"Erin Cat", "Bob"}, namesOn(cardFor(t, current.Streams, dave)))
	require.Equal(t, 2, cardFor(t, current.Streams, dave).Viewers, "three sessions, one of them a peek")
	require.Zero(t, cardFor(t, current.Streams, dave).Others, "and the peek is nobody beside the faces either")
	require.Equal(t, []string{"dave"}, namesOn(cardFor(t, current.Streams, watts)))

	encoded, err := json.Marshal(current)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "videokey-dave")
	require.NotContains(t, string(encoded), "videokey-watts")
	require.NotContains(t, string(encoded), "cccccccc", "Broadcast Box's session ids stay on the server")

	// Erin closes her tab: gone from the card at the next poll.
	lock.Lock()
	open["videokey-dave"] = open["videokey-dave"][1:]
	lock.Unlock()

	p.pollOnce(t.Context(), true)
	require.Equal(t, []string{"Bob"}, namesOn(cardFor(t, p.getSnapshot().Streams, dave)))
}

// The poller lists and sweeps while playback requests record, from as many
// goroutines as there are viewers pressing play. Run under -race.
func TestWatchersCanBeRecordedWhileAPollListsThem(t *testing.T) {
	p := watchersPlugin(t)
	cfg := p.getConfiguration()
	dave := p.streamHandle("videokey-dave")
	raw := []bbStream{rawWatched("videokey-dave", "s-0-0", "s-1-0")}

	var group sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()

			for i := 0; i < 200; i++ {
				p.watchers().record(fmt.Sprintf("s-%d-%d", worker, i), "uid-a", dave, time.Now())
			}
		}(worker)
	}

	for poll := 0; poll < 50; poll++ {
		p.decorate(raw, cfg)
	}
	group.Wait()

	require.Equal(t, []apiWatcher{{UserID: "uid-a", Who: "Bob"}}, cardFor(t, p.decorate(raw, cfg), dave).Watchers)
}
