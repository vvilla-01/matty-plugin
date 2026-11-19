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

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Changing a plan, and saying you are in on somebody else's. Both are writes
// to the one document everybody shares, so both go through the same
// compare-and-set as adding and cancelling, and both are checked for whose
// plan it is before anything is spent on them.

func postPlanChange(t *testing.T, p *Plugin, path, userID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()

	encoded, err := json.Marshal(body)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(encoded)))

	switch path {
	case "/api/v1/schedule":
		p.handleScheduleAdd(recorder, request, userID)
	case "/api/v1/schedule/remove":
		p.handleScheduleRemove(recorder, request, userID)
	case "/api/v1/schedule/edit":
		p.handleScheduleEdit(recorder, request, userID)
	case "/api/v1/schedule/join":
		p.handleScheduleJoin(recorder, request, userID)
	default:
		t.Fatalf("no such path in this test: %s", path)
	}

	return recorder
}

func inAnHour() int64 {
	return nowMillis() + int64(time.Hour/time.Millisecond)
}

// linkStream links somebody as setLink would, both entries, to a stream named
// after them.
func linkStream(store map[string][]byte, userID string) {
	linkForTest(store, userID, "videokey-"+userID)
}

func TestAPlanCanBeChangedByTheOnePersonWhoMadeIt(t *testing.T) {
	p, store := communityPlugin(t)
	require.NoError(t, p.addScheduleEntry("villa", "Arma co-op", "game", inAnHour(), nowMillis()))
	plan := onlyPlan(t, p)

	// Both linked, so the refusal below is about whose plan it is rather than
	// about dave having no stream.
	linkStream(store, "villa")
	linkStream(store, "dave")

	later := inAnHour() + int64(2*time.Hour/time.Millisecond)
	response := postPlanChange(t, p, "/api/v1/schedule/edit", "villa", map[string]any{
		"id": plan.ID, "title": "Arma: night ops", "category": "", "startsAt": later,
	})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	changed := onlyPlan(t, p)
	require.Equal(t, plan.ID, changed.ID, "the same plan, changed — not a new one")
	require.Equal(t, "Arma: night ops", changed.Title)
	require.Equal(t, later, changed.StartsAt)
	require.Empty(t, changed.Category)

	// Somebody else cannot, and neither can a plan that is not there.
	response = postPlanChange(t, p, "/api/v1/schedule/edit", "dave", map[string]any{
		"id": plan.ID, "title": "Mine now", "startsAt": later,
	})
	require.Equal(t, http.StatusForbidden, response.Code)

	response = postPlanChange(t, p, "/api/v1/schedule/edit", "villa", map[string]any{
		"id": "no-such-plan", "title": "Anything", "startsAt": later,
	})
	require.Equal(t, http.StatusNotFound, response.Code)

	// And the same rules as a new plan: words, and a time in the window.
	response = postPlanChange(t, p, "/api/v1/schedule/edit", "villa", map[string]any{
		"id": plan.ID, "title": "  ", "startsAt": later,
	})
	require.Equal(t, http.StatusBadRequest, response.Code)

	response = postPlanChange(t, p, "/api/v1/schedule/edit", "villa", map[string]any{
		"id": plan.ID, "title": "Too late", "startsAt": nowMillis() - 1000,
	})
	require.Equal(t, http.StatusBadRequest, response.Code)

	require.Equal(t, "Arma: night ops", onlyPlan(t, p).Title, "a refused change changes nothing")
}

// Saving a linked plan with the same title it had does not look the title up
// again — no call out, and nothing lost if lookups have been switched off
// since. A different title is looked up; none clears it.
func TestChangingAPlanOnlyLooksUpATitleThatChanged(t *testing.T) {
	fake := newMediaFake(t)
	fake.json("/3/movie/949", heatDetails())
	fake.json("/3/movie/603", map[string]any{"title": "The Matrix", "release_date": "1999-03-30", "runtime": 136})

	p, _ := mediaPlugin(t, movieConfig())
	require.Equal(t, http.StatusOK, planStream(t, p, map[string]any{"title": "Film night", "category": "movie", "media": "tmdb:movie:949"}).Code)
	plan := onlyPlan(t, p)
	require.Equal(t, 1, fake.requestsTo("/3/movie/949"))

	// Switched off since: the link survives an edit that does not touch it.
	off := movieConfig()
	off.MediaLookup = false
	off.normalize()
	p.setConfiguration(off)

	response := postPlanChange(t, p, "/api/v1/schedule/edit", "villa", map[string]any{
		"id": plan.ID, "title": "Film night: Heat", "category": "movie", "startsAt": plan.StartsAt, "media": "tmdb:movie:949",
	})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 1, fake.requestsTo("/3/movie/949"), "not looked up again")

	kept := onlyPlan(t, p)
	require.Equal(t, "Film night: Heat", kept.Title)
	require.NotNil(t, kept.Media)
	require.Equal(t, "Heat", kept.Media.Title)

	// A different title is looked up.
	p.setConfiguration(movieConfig())
	response = postPlanChange(t, p, "/api/v1/schedule/edit", "villa", map[string]any{
		"id": plan.ID, "title": "Film night", "category": "movie", "startsAt": plan.StartsAt, "media": "tmdb:movie:603",
	})
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, 1, fake.requestsTo("/3/movie/603"))
	require.Equal(t, "The Matrix", onlyPlan(t, p).Media.Title)

	// None clears it; so does becoming a game night.
	response = postPlanChange(t, p, "/api/v1/schedule/edit", "villa", map[string]any{
		"id": plan.ID, "title": "Film night", "category": "movie", "startsAt": plan.StartsAt,
	})
	require.Equal(t, http.StatusOK, response.Code)
	require.Nil(t, onlyPlan(t, p).Media)

	// A title under the wrong icon is refused, as for a new plan.
	response = postPlanChange(t, p, "/api/v1/schedule/edit", "villa", map[string]any{
		"id": plan.ID, "title": "Film night", "category": "game", "startsAt": plan.StartsAt, "media": "tmdb:movie:949",
	})
	require.Equal(t, http.StatusBadRequest, response.Code)
}

func TestJoiningIsForStreamersAndShowsWhoIsIn(t *testing.T) {
	p, store := communityPlugin(t)
	require.NoError(t, p.addScheduleEntry("villa", "Arma co-op", "game", inAnHour(), nowMillis()))
	plan := onlyPlan(t, p)

	// Not linked: not a streamer here, so not somebody who can join.
	response := postPlanChange(t, p, "/api/v1/schedule/join", "dave", map[string]any{"id": plan.ID, "join": true})
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Empty(t, onlyPlan(t, p).Joined)

	linkStream(store, "dave")
	linkStream(store, "bob")
	linkStream(store, "villa")

	require.Equal(t, http.StatusOK, postPlanChange(t, p, "/api/v1/schedule/join", "dave", map[string]any{"id": plan.ID, "join": true}).Code)
	require.Equal(t, http.StatusOK, postPlanChange(t, p, "/api/v1/schedule/join", "bob", map[string]any{"id": plan.ID, "join": true}).Code)

	// Joining twice is still once.
	require.Equal(t, http.StatusOK, postPlanChange(t, p, "/api/v1/schedule/join", "dave", map[string]any{"id": plan.ID, "join": true}).Code)

	joined := onlyPlan(t, p).Joined
	require.Equal(t, []scheduleJoiner{{UserID: "dave", Who: "Name dave"}, {UserID: "bob", Who: "Name bob"}}, joined, "by name, in the order they joined")

	// Your own plan is not one you join.
	response = postPlanChange(t, p, "/api/v1/schedule/join", "villa", map[string]any{"id": plan.ID, "join": true})
	require.Equal(t, http.StatusBadRequest, response.Code)

	// Leaving, and leaving what you were not in.
	require.Equal(t, http.StatusOK, postPlanChange(t, p, "/api/v1/schedule/join", "dave", map[string]any{"id": plan.ID, "join": false}).Code)
	require.Equal(t, http.StatusOK, postPlanChange(t, p, "/api/v1/schedule/join", "dave", map[string]any{"id": plan.ID, "join": false}).Code)
	require.Equal(t, []scheduleJoiner{{UserID: "bob", Who: "Name bob"}}, onlyPlan(t, p).Joined)

	// Changing the plan keeps who is in.
	response = postPlanChange(t, p, "/api/v1/schedule/edit", "villa", map[string]any{
		"id": plan.ID, "title": "Arma: night ops", "category": "game", "startsAt": plan.StartsAt,
	})
	require.Equal(t, http.StatusOK, response.Code)
	require.Len(t, onlyPlan(t, p).Joined, 1)

	require.Equal(t, http.StatusNotFound, postPlanChange(t, p, "/api/v1/schedule/join", "bob", map[string]any{"id": "gone", "join": true}).Code)
}

func TestAPlanHasRoomForADozen(t *testing.T) {
	p, store := communityPlugin(t)
	require.NoError(t, p.addScheduleEntry("villa", "Big night", "", inAnHour(), nowMillis()))
	plan := onlyPlan(t, p)

	for i := 0; i < maxScheduleJoined; i++ {
		require.False(t, onlyPlan(t, p).Full, "room for one more after %d", i)

		who := fmt.Sprintf("s%d", i)
		linkStream(store, who)
		require.Equal(t, http.StatusOK, postPlanChange(t, p, "/api/v1/schedule/join", who, map[string]any{"id": plan.ID, "join": true}).Code)
	}

	// Every client is told so, and the join it would have offered is the one refused.
	require.True(t, onlyPlan(t, p).Full)

	linkStream(store, "late")
	response := postPlanChange(t, p, "/api/v1/schedule/join", "late", map[string]any{"id": plan.ID, "join": true})
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Len(t, onlyPlan(t, p).Joined, maxScheduleJoined)

	// Somebody leaving makes room again.
	require.Equal(t, http.StatusOK, postPlanChange(t, p, "/api/v1/schedule/join", "s0", map[string]any{"id": plan.ID, "join": false}).Code)
	require.False(t, onlyPlan(t, p).Full)
	require.Equal(t, http.StatusOK, postPlanChange(t, p, "/api/v1/schedule/join", "late", map[string]any{"id": plan.ID, "join": true}).Code)
}

// A place is held by whoever took it, shown or not: somebody whose account has
// gone is left out of the names, and the plan is still full. A client that
// counted the names it was sent would offer a Join the server refuses, which
// is why the server says "full" itself.
func TestAPlanIsFullByThePlacesTakenNotTheNamesShown(t *testing.T) {
	p, store := communityPlugin(t)
	require.NoError(t, p.addScheduleEntry("villa", "Big night", "", inAnHour(), nowMillis()))
	plan := onlyPlan(t, p)

	for i := 0; i < maxScheduleJoined-1; i++ {
		who := fmt.Sprintf("s%d", i)
		linkStream(store, who)
		require.Equal(t, http.StatusOK, postPlanChange(t, p, "/api/v1/schedule/join", who, map[string]any{"id": plan.ID, "join": true}).Code)
	}

	// The last place goes to somebody whose account is then deactivated.
	require.NoError(t, p.joinScheduleEntry("gone", plan.ID, true, nowMillis()))

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	kept := api.ExpectedCalls[:0]
	for _, call := range api.ExpectedCalls {
		if call.Method != "GetUser" {
			kept = append(kept, call)
		}
	}
	api.ExpectedCalls = kept
	api.On("GetUser", mock.Anything).Return(
		func(userID string) *model.User {
			user := &model.User{Id: userID, Username: "u" + userID, Nickname: "Name " + userID}
			if userID == "gone" {
				user.DeleteAt = 1
			}

			return user
		},
		func(_ string) *model.AppError { return nil },
	)

	shown := onlyPlan(t, p)
	require.Len(t, shown.Joined, maxScheduleJoined-1, "the names that can be shown")
	require.True(t, shown.Full, "and no room, all the same")

	linkStream(store, "late")
	require.Equal(t, http.StatusBadRequest, postPlanChange(t, p, "/api/v1/schedule/join", "late", map[string]any{"id": plan.ID, "join": true}).Code)
}

// Somebody at their limit is told so before the server calls out on their
// behalf, not after an eight-second lookup for a plan that was never going to
// be saved.
func TestNoTitleIsLookedUpForAPlanThereIsNoRoomFor(t *testing.T) {
	fake := newMediaFake(t)
	fake.json("/3/movie/949", heatDetails())

	p, _ := mediaPlugin(t, movieConfig())
	for i := 0; i < maxSchedulePerUser; i++ {
		require.NoError(t, p.addScheduleEntry("villa", fmt.Sprintf("Plan %d", i), "", inAnHour()+int64(i), nowMillis()))
	}

	response := planStream(t, p, map[string]any{"title": "One more", "category": "movie", "media": "tmdb:movie:949"})
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, response.Body.String(), "three planned")
	require.Zero(t, fake.requestsTo("/3/movie/949"), "nothing asked of TMDB")
}

// The search leaves adult titles out, and so does a ref typed in by hand.
func TestAdultTitlesAreNeverCarriedOrShown(t *testing.T) {
	fake := newMediaFake(t)
	fake.json("/3/search/multi", map[string]any{"results": []map[string]any{
		{"media_type": "movie", "id": 949, "title": "Heat", "release_date": "1995-12-15"},
		{"media_type": "movie", "id": 777, "title": "Heat, but not that kind", "adult": true},
	}})
	adult := heatDetails()
	adult["adult"] = true
	adult["title"] = "Not for the panel"
	fake.json("/3/movie/777", adult)

	p, _ := mediaPlugin(t, movieConfig())

	found := mustSearch(t, p, "movie", "heat")
	require.Len(t, found.Results, 1)
	require.Equal(t, "tmdb:movie:949", found.Results[0].Ref)

	response := planStream(t, p, map[string]any{"title": "Film night", "category": "movie", "media": "tmdb:movie:777"})
	require.Equal(t, http.StatusOK, response.Code, "the plan is kept")
	require.Nil(t, onlyPlan(t, p).Media, "without the title")

	require.Equal(t, http.StatusNotFound, mediaPictureOf(t, p, "tmdb:movie:777", "backdrop").Code)
}

// Steam's store API refuses an address that asks too often with a 403. That
// is "slow down", not "no such game": nothing is remembered as missing.
func TestAProviderSayingSlowDownIsNotRememberedAsMissing(t *testing.T) {
	fake := newMediaFake(t)
	fake.status("/api/appdetails", http.StatusForbidden)

	p, _ := mediaPlugin(t, &configuration{MediaLookup: true})

	response := mediaPictureOf(t, p, "steam:1245620", "backdrop")
	require.Equal(t, http.StatusBadGateway, response.Code, "could not find out, said as such")

	// Steam answers again; the next panel to ask gets the picture.
	fake.json("/api/appdetails", eldenRingDetails(fake))
	fake.bytes("/steamcdn/apps/1245620/f0b19c231f86fa0e633ff88a1a63459443017728/header_alt_assets_3.jpg", jpegBytes)

	response = mediaPictureOf(t, p, "steam:1245620", "backdrop")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
}

// However many people ask, the server keeps to its own budget with each
// provider, and says so rather than asking anyway.
func TestTheServerKeepsToABudgetWithEachProvider(t *testing.T) {
	fake := newMediaFake(t)
	fake.json("/api/storesearch/", map[string]any{"total": 0, "items": []any{}})

	p, _ := mediaPlugin(t, &configuration{MediaLookup: true})

	allowed := 0
	for i := 0; i < steamCallsPerMinute+5; i++ {
		if err := p.steamGet(t.Context(), "/api/storesearch/", nil, &struct{}{}); err == nil {
			allowed++
		} else {
			require.ErrorIs(t, err, errMediaBusy)
		}
	}

	require.Equal(t, steamCallsPerMinute, allowed)
	require.Equal(t, steamCallsPerMinute, fake.requestsTo("/api/storesearch/"))
}

// A new plan appears on every open panel at once; they must not each ask for
// the same art in the same second.
func TestManyPanelsAskingForOnePictureMakeOneFetch(t *testing.T) {
	fake := newMediaFake(t)
	fake.json("/3/movie/949", heatDetails())

	var gate sync.WaitGroup
	gate.Add(1)
	var fetches atomic.Int32
	fake.route("/t/p/w780/heat-wide.jpg", func(w http.ResponseWriter, _ *http.Request) {
		fetches.Add(1)
		gate.Wait()
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(jpegBytes)
	})

	p, _ := mediaPlugin(t, movieConfig())

	var done sync.WaitGroup
	codes := make([]int, 12)
	for i := range codes {
		done.Add(1)
		go func(n int) {
			defer done.Done()
			codes[n] = mediaPictureOf(t, p, "tmdb:movie:949", "backdrop").Code
		}(i)
	}

	// Long enough for all twelve to be waiting on the one fetch.
	time.Sleep(200 * time.Millisecond)
	gate.Done()
	done.Wait()

	for _, code := range codes {
		require.Equal(t, http.StatusOK, code)
	}
	require.Equal(t, int32(1), fetches.Load())
	require.Equal(t, 1, fake.requestsTo("/3/movie/949"))
}

// A plan that has started is still listed for a while, and its owner can still
// fix its title without being made to move it into the future.
func TestAStartedPlanCanBeChangedWithoutMovingIt(t *testing.T) {
	p, store := communityPlugin(t)
	linkStream(store, "villa")
	now := nowMillis()
	started := now - int64(30*time.Minute/time.Millisecond)
	require.NoError(t, p.addScheduleEntry("villa", "Arma co-op", "game", started+int64(time.Hour/time.Millisecond), started))

	// Written as though it had been planned before it started.
	plan := mustSchedule(t, p, now)[0]
	require.NoError(t, p.editScheduleEntry("villa", plan.ID, "Arma co-op", "game", keepPlanMedia, nil, started, started-1000))

	response := postPlanChange(t, p, "/api/v1/schedule/edit", "villa", map[string]any{
		"id": plan.ID, "title": "Arma co-op, typo fixed", "category": "game", "startsAt": started,
	})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	changed := onlyPlan(t, p)
	require.Equal(t, "Arma co-op, typo fixed", changed.Title)
	require.Equal(t, started, changed.StartsAt, "left where it was")

	// Moving it still has to move it somewhere allowed.
	response = postPlanChange(t, p, "/api/v1/schedule/edit", "villa", map[string]any{
		"id": plan.ID, "title": "Arma co-op", "category": "game", "startsAt": started - 1000,
	})
	require.Equal(t, http.StatusBadRequest, response.Code)
}

// Joining needs a linked stream; leaving never does.
func TestSomebodyWhoUnlinkedCanStillLeave(t *testing.T) {
	p, store := communityPlugin(t)
	require.NoError(t, p.addScheduleEntry("villa", "Arma co-op", "game", inAnHour(), nowMillis()))
	plan := onlyPlan(t, p)

	linkStream(store, "dave")
	require.Equal(t, http.StatusOK, postPlanChange(t, p, "/api/v1/schedule/join", "dave", map[string]any{"id": plan.ID, "join": true}).Code)

	delete(store, kvLinkByUserPrefix+"dave")
	require.Equal(t, http.StatusForbidden, postPlanChange(t, p, "/api/v1/schedule/join", "dave", map[string]any{"id": plan.ID, "join": true}).Code)
	require.Equal(t, http.StatusOK, postPlanChange(t, p, "/api/v1/schedule/join", "dave", map[string]any{"id": plan.ID, "join": false}).Code)
	require.Empty(t, onlyPlan(t, p).Joined)
}

// Planning a stream and changing one are for somebody with a stream linked, as
// joining is. Cancelling your own plan never is: somebody who has unlinked can
// still take their plan down rather than leave it on every screen.
func TestOnlyALinkedStreamerCanPlanOrChangeAPlan(t *testing.T) {
	fake := newMediaFake(t)
	fake.json("/3/movie/949", heatDetails())

	// mediaPlugin links villa for the tests that are about titles; this one
	// starts from somebody who has not linked.
	p, store := mediaPlugin(t, movieConfig())
	delete(store, kvLinkByUserPrefix+"villa")

	plan := map[string]any{"title": "Film night", "category": "movie", "media": "tmdb:movie:949", "startsAt": inAnHour()}

	// Not linked: nothing planned, told why, and nothing asked of TMDB on
	// behalf of a plan that was never going to be saved.
	response := postPlanChange(t, p, "/api/v1/schedule", "villa", plan)
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Contains(t, response.Body.String(), "Link your stream first")
	require.Empty(t, mustSchedule(t, p, nowMillis()))
	require.Zero(t, fake.requestsTo("/3/movie/949"), "nothing looked up for somebody who cannot plan")

	// Linked: planned, and changed, exactly as before.
	linkStream(store, "villa")
	response = postPlanChange(t, p, "/api/v1/schedule", "villa", plan)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	planned := onlyPlan(t, p)
	require.NotNil(t, planned.Media)

	response = postPlanChange(t, p, "/api/v1/schedule/edit", "villa", map[string]any{
		"id": planned.ID, "title": "Film night: Heat", "category": "movie", "startsAt": planned.StartsAt, "media": "tmdb:movie:949",
	})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "Film night: Heat", onlyPlan(t, p).Title)

	// Unlinked since: the plan is still theirs, and still shown, but it can
	// no longer be changed — and the refusal says the × still works.
	delete(store, kvLinkByUserPrefix+"villa")
	require.Len(t, mustSchedule(t, p, nowMillis()), 1, "unlinking does not take a plan down by itself")

	response = postPlanChange(t, p, "/api/v1/schedule/edit", "villa", map[string]any{
		"id": planned.ID, "title": "Film night: Heat, again", "category": "movie", "startsAt": planned.StartsAt,
	})
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Contains(t, response.Body.String(), "Cancelling it never needs one")
	require.Equal(t, "Film night: Heat", onlyPlan(t, p).Title, "a refused change changes nothing")
	require.NotNil(t, onlyPlan(t, p).Media, "not even the linked title")

	// Nor can a new one be planned.
	response = postPlanChange(t, p, "/api/v1/schedule", "villa", map[string]any{"title": "Another", "startsAt": inAnHour()})
	require.Equal(t, http.StatusForbidden, response.Code)

	// But it can be cancelled.
	response = postPlanChange(t, p, "/api/v1/schedule/remove", "villa", map[string]any{"id": planned.ID})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Empty(t, mustSchedule(t, p, nowMillis()))
}

// An admin cancels anybody's plan, linked or not, as they always could.
func TestAnAdminCanCancelAnyPlanWithoutALinkedStream(t *testing.T) {
	p, store := communityPlugin(t)

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("HasPermissionTo", "an-admin", model.PermissionManageSystem).Return(true)
	api.On("HasPermissionTo", mock.Anything, model.PermissionManageSystem).Return(false)

	linkStream(store, "dave")
	require.Equal(t, http.StatusOK, postPlanChange(t, p, "/api/v1/schedule", "dave", map[string]any{"title": "Arma co-op", "startsAt": inAnHour()}).Code)
	plan := onlyPlan(t, p)

	// Somebody else, unlinked and not an admin, cannot.
	require.Equal(t, http.StatusOK, postPlanChange(t, p, "/api/v1/schedule/remove", "bob", map[string]any{"id": plan.ID}).Code)
	require.Len(t, mustSchedule(t, p, nowMillis()), 1, "not bob's to cancel")

	require.Equal(t, http.StatusOK, postPlanChange(t, p, "/api/v1/schedule/remove", "an-admin", map[string]any{"id": plan.ID}).Code)
	require.Empty(t, mustSchedule(t, p, nowMillis()))
}

// A link that cannot be read is the store failing, not the person being
// unlinked. Telling a linked streamer to link again because of a database blip
// would send them off to paste their token for nothing.
func TestAnUnreadableLinkIsNotTakenForNoLink(t *testing.T) {
	p, store, faults := kvBackedPluginWithFaults(map[string][]byte{})
	linkStream(store, "villa")
	faults.failRead(kvLinkByUserPrefix + "villa")

	for _, path := range []string{"/api/v1/schedule", "/api/v1/schedule/edit", "/api/v1/schedule/join"} {
		response := postPlanChange(t, p, path, "villa", map[string]any{"id": "any", "title": "Arma co-op", "startsAt": inAnHour(), "join": true})
		require.Equal(t, http.StatusServiceUnavailable, response.Code, path)
		require.NotContains(t, response.Body.String(), "Link your stream", path)
		require.NotContains(t, response.Body.String(), "injected failure", path)
	}
}
