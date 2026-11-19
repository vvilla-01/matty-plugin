package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The schedule is the only document ordinary users write, and two people
// planning a stream in the same second is the normal case rather than the edge
// one. These tests run real goroutines against the mock's real compare-and-set,
// so a lost write fails the test rather than passing quietly.

func communityPlugin(t *testing.T) (*Plugin, map[string][]byte) {
	t.Helper()

	p, store, _ := communityPluginWithFaults(t, map[string][]byte{}, nil)

	return p, store
}

// communityPluginWithFaults is communityPlugin with the store's faults in hand,
// over a store and faults that may already exist (nil makes new ones).
func communityPluginWithFaults(t *testing.T, store map[string][]byte, faults *kvFaults) (*Plugin, map[string][]byte, *kvFaults) {
	t.Helper()

	if faults == nil {
		faults = &kvFaults{readFails: map[string]bool{}, writeFails: map[string]bool{}, writes: map[string]int{}}
	}
	p, store, faults := kvBackedPluginWithFaultsOn(store, faults)

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("GetUser", mock.Anything).Return(
		func(userID string) *model.User {
			return &model.User{Id: userID, Username: "u" + userID, Nickname: "Name " + userID}
		},
		func(_ string) *model.AppError { return nil },
	)

	return p, store, faults
}

func mustSchedule(t *testing.T, p *Plugin, nowMS int64) []scheduleItem {
	t.Helper()

	items, ok := p.scheduleItems(nowMS)
	require.True(t, ok, "the store should have answered")

	return items
}

// The schedule stores an instant, never a wall clock, so the same moment reads
// correctly in every viewer's zone.
func TestScheduleStoresAnInstantAndPrunesWhatHasPassed(t *testing.T) {
	p, _ := communityPlugin(t)
	now := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC).UnixMilli()

	soon := now + int64(2*time.Hour/time.Millisecond)
	require.NoError(t, p.addScheduleEntry("villa", "Arma co-op", "", soon, now))

	items := mustSchedule(t, p, now)
	require.Len(t, items, 1)
	require.Equal(t, soon, items[0].StartsAt, "the stored value is the instant, unchanged")
	require.Equal(t, "Arma co-op", items[0].Title)
	require.Equal(t, "Name villa", items[0].Who)

	// Still listed shortly after it starts: that is when it is on air.
	require.Len(t, mustSchedule(t, p, soon+int64(time.Hour/time.Millisecond)), 1)

	// Gone once the grace is up.
	require.Empty(t, mustSchedule(t, p, soon+scheduleGraceMS+1))
}

// The panel draws the row left to right in the order it is given, so "soonest
// first" is the store's promise and not something the browser re-derives.
func TestTheScheduleComesBackSoonestFirst(t *testing.T) {
	p, _ := communityPlugin(t)
	now := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC).UnixMilli()
	hour := int64(time.Hour / time.Millisecond)

	require.NoError(t, p.addScheduleEntry("villa", "Friday film", "", now+(48*hour), now))
	require.NoError(t, p.addScheduleEntry("dave", "Tonight", "", now+hour, now))
	require.NoError(t, p.addScheduleEntry("bob", "Tomorrow", "", now+(20*hour), now))

	items := mustSchedule(t, p, now)
	require.Len(t, items, 3)
	require.Equal(t, "Tonight", items[0].Title)
	require.Equal(t, "Tomorrow", items[1].Title)
	require.Equal(t, "Friday film", items[2].Title)

	for i := 1; i < len(items); i++ {
		require.LessOrEqual(t, items[i-1].StartsAt, items[i].StartsAt)
	}
}

func TestScheduleRejectsThePastAndTheFarFuture(t *testing.T) {
	p, _ := communityPlugin(t)
	now := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC).UnixMilli()

	require.Error(t, p.addScheduleEntry("villa", "Yesterday", "", now-1000, now))
	require.Error(t, p.addScheduleEntry("villa", "Next century", "", now+scheduleHorizonMS+1000, now))
	require.Error(t, p.addScheduleEntry("villa", "   ", "", now+100000, now))
	require.Empty(t, mustSchedule(t, p, now))
}

func TestScheduleIsPerPersonAndOnlyYoursToRemove(t *testing.T) {
	p, _ := communityPlugin(t)
	now := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC).UnixMilli()
	at := now + int64(time.Hour/time.Millisecond)

	for i := 0; i < maxSchedulePerUser+2; i++ {
		_ = p.addScheduleEntry("villa", "session "+strconv.Itoa(i), "", at+int64(i*1000), now)
	}
	require.Len(t, mustSchedule(t, p, now), maxSchedulePerUser)

	mine := mustSchedule(t, p, now)[0]

	require.NoError(t, p.removeScheduleEntry("someone-else", mine.ID, false, now))
	require.Len(t, mustSchedule(t, p, now), maxSchedulePerUser, "somebody else's plan is not yours to delete")

	require.NoError(t, p.removeScheduleEntry("villa", mine.ID, false, now))
	require.Len(t, mustSchedule(t, p, now), maxSchedulePerUser-1)

	// An admin can tidy up after anyone.
	other := mustSchedule(t, p, now)[0]
	require.NoError(t, p.removeScheduleEntry("an-admin", other.ID, true, now))
	require.Len(t, mustSchedule(t, p, now), maxSchedulePerUser-2)
}

// Read it, change it, write it back is not safe when several people do it at
// once. Every one of these is a separate person's plan, so every one has to
// survive — a lost write here is silent, and the person who lost it gets a 200.
func TestSimultaneousPlansAreAllKept(t *testing.T) {
	p, _ := communityPlugin(t)
	now := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC).UnixMilli()
	at := now + int64(time.Hour/time.Millisecond)

	const people = 8

	var wait sync.WaitGroup
	for i := 0; i < people; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			_ = p.addScheduleEntry("streamer"+strconv.Itoa(i), "session "+strconv.Itoa(i), "", at+int64(i*1000), now)
		}(i)
	}
	wait.Wait()

	require.Len(t, mustSchedule(t, p, now), people, "no plan was overwritten by another")
}

// Everything in the snapshot is built once and served to everybody, so nothing
// in it may depend on who is reading.
func TestTheScheduleViewIsTheSameForEveryone(t *testing.T) {
	p, _ := communityPlugin(t)
	now := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC).UnixMilli()

	require.NoError(t, p.addScheduleEntry("villa", "Arma", "", now+3600000, now))
	require.NoError(t, p.addScheduleEntry("dave", "Film", "", now+7200000, now))

	first, ok := p.scheduleItems(now)
	require.True(t, ok)
	second, ok := p.scheduleItems(now)
	require.True(t, ok)

	a, err := json.Marshal(first)
	require.NoError(t, err)
	b, err := json.Marshal(second)
	require.NoError(t, err)
	require.JSONEq(t, string(a), string(b))

	// And it carries the instant, not a rendering of it: no "Tonight", no
	// offset, nothing that would be right for one reader and wrong for the
	// rest.
	require.Contains(t, string(a), strconv.FormatInt(now+3600000, 10))
}

// What a streamer types here is shown to everybody, and they are exactly the
// person with a stream key on their clipboard.
func TestAPastedStreamKeyNeverReachesTheSchedule(t *testing.T) {
	p, store := communityPlugin(t)
	now := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC).UnixMilli()

	store[kvLinkByUserPrefix+"villa"] = []byte("videokey-villa")

	require.NoError(t, p.addScheduleEntry("villa", "watch videokey-villa at 8", "", now+3600000, now))

	items := mustSchedule(t, p, now)
	require.Len(t, items, 1)
	require.NotContains(t, items[0].Title, "videokey-villa")
	require.Contains(t, items[0].Title, "my stream")
}

// The display name lookup runs on every poll, so it must not scale with the
// number of entries somebody has planned.
func TestScheduleResolvesEachPersonOnce(t *testing.T) {
	p, _, _ := kvBackedPluginWithFaults(map[string][]byte{})

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	var lookups int32
	api.On("GetUser", mock.Anything).Return(
		func(userID string) *model.User {
			atomic.AddInt32(&lookups, 1)
			return &model.User{Id: userID, Username: "u" + userID}
		},
		func(_ string) *model.AppError { return nil },
	)

	now := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC).UnixMilli()
	for i := 0; i < maxSchedulePerUser; i++ {
		require.NoError(t, p.addScheduleEntry("villa", "session "+strconv.Itoa(i), "", now+int64(3600000+(i*1000)), now))
	}

	atomic.StoreInt32(&lookups, 0)
	require.Len(t, mustSchedule(t, p, now), maxSchedulePerUser)
	require.EqualValues(t, 1, atomic.LoadInt32(&lookups), "three entries, one person, one lookup")
}

// A store that will not answer is not the same as "nothing is planned", and
// publishing it as though it were empties the row on every screen.
func TestAStoreFailureDoesNotEmptyTheSchedule(t *testing.T) {
	p, _, faults := kvBackedPluginWithFaults(map[string][]byte{})

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("GetUser", mock.Anything).Return(
		func(userID string) *model.User { return &model.User{Id: userID, Username: "u" + userID} },
		func(_ string) *model.AppError { return nil },
	)

	// The real clock, not a fixed instant: addCommunity reads nowMillis(),
	// so an entry pinned to a date in the past falls outside the grace and
	// the test starts failing on its own overnight.
	now := time.Now().UTC().UnixMilli()
	require.NoError(t, p.addScheduleEntry("villa", "Arma", "", now+3600000, now))

	// The healthy snapshot is what a client should keep seeing.
	good := &snapshot{}
	p.addCommunity(good)
	p.setSnapshot(good)
	require.Len(t, good.Schedule, 1)

	faults.failRead(kvSchedule)

	degraded := &snapshot{}
	p.addCommunity(degraded)
	require.Len(t, degraded.Schedule, 1, "the last known schedule is carried forward")
	require.Equal(t, "Arma", degraded.Schedule[0].Title)
}

// A user store that will not answer is not the same as a room full of deleted
// accounts, and publishing it as though it were takes the row off every screen.
func TestAUserStoreFailureIsNotAnEmptySchedule(t *testing.T) {
	p, _, _ := kvBackedPluginWithFaults(map[string][]byte{})
	// The real clock, not a fixed instant: addCommunity reads nowMillis(),
	// so an entry pinned to a date in the past falls outside the grace and
	// the test starts failing on its own overnight.
	now := time.Now().UTC().UnixMilli()

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	var broken int32
	api.On("GetUser", mock.Anything).Return(
		func(userID string) *model.User {
			if atomic.LoadInt32(&broken) == 1 {
				return nil
			}

			return &model.User{Id: userID, Username: "u" + userID}
		},
		func(_ string) *model.AppError {
			if atomic.LoadInt32(&broken) == 1 {
				return &model.AppError{Message: "database is down", StatusCode: http.StatusInternalServerError}
			}

			return nil
		},
	)

	require.NoError(t, p.addScheduleEntry("villa", "Arma", "", now+3600000, now))
	require.Len(t, mustSchedule(t, p, now), 1)

	atomic.StoreInt32(&broken, 1)
	items, answered := p.scheduleItems(now)
	require.False(t, answered, "a failing user store is not an answer")
	require.Empty(t, items)

	// And the last known schedule is what keeps going out.
	good := &snapshot{Schedule: []scheduleItem{{ID: "a", Title: "Arma"}}}
	p.setSnapshot(good)
	degraded := &snapshot{}
	p.addCommunity(degraded)
	require.Len(t, degraded.Schedule, 1)
}

// A deleted account, though, IS an answer: that entry is dropped and the rest
// of the schedule goes out as normal.
func TestADeletedOwnerOnlyDropsTheirOwnEntry(t *testing.T) {
	p, _, _ := kvBackedPluginWithFaults(map[string][]byte{})
	now := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC).UnixMilli()

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("GetUser", mock.Anything).Return(
		func(userID string) *model.User {
			if userID == "ghost" {
				return nil
			}

			return &model.User{Id: userID, Username: "u" + userID}
		},
		func(userID string) *model.AppError {
			if userID == "ghost" {
				return &model.AppError{Message: "not found", StatusCode: http.StatusNotFound}
			}

			return nil
		},
	)

	require.NoError(t, p.addScheduleEntry("ghost", "Never happening", "", now+3600000, now))
	require.NoError(t, p.addScheduleEntry("villa", "Arma", "", now+7200000, now))

	items := mustSchedule(t, p, now)
	require.Len(t, items, 1)
	require.Equal(t, "Arma", items[0].Title)
}

// Redaction swaps a stream key for "my stream", and a key shorter than that
// makes the title longer than the clamp just allowed.
func TestRedactionCannotPushATitlePastItsLimit(t *testing.T) {
	p, store := communityPlugin(t)
	now := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC).UnixMilli()

	store[kvLinkByUserPrefix+"villa"] = []byte("ab")

	require.NoError(t, p.addScheduleEntry("villa", strings.Repeat("ab ", 40), "", now+3600000, now))

	items := mustSchedule(t, p, now)
	require.Len(t, items, 1)
	require.LessOrEqual(t, utf8.RuneCountInString(items[0].Title), maxScheduleTitle)
}

// An undecodable schedule is disposable in a way airtime never is: it is set
// aside and the feature keeps working, rather than halting until an admin looks.
//
// "Quarantine and carry on" without removing the document meant every poll
// re-read the same bad bytes and logged the same warning, for ever.
func TestAnUnreadableDocumentIsSetAsideOnce(t *testing.T) {
	p, store, _ := kvBackedPluginWithFaults(map[string][]byte{})

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("GetUser", mock.Anything).Return(
		func(userID string) *model.User { return &model.User{Id: userID, Username: "u" + userID} },
		func(_ string) *model.AppError { return nil },
	)

	now := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC).UnixMilli()
	store[kvSchedule] = []byte(`not json at all`)

	for i := 0; i < 10; i++ {
		items, ok := p.scheduleItems(now + int64(i*10000))
		require.True(t, ok)
		require.Empty(t, items)
	}

	require.NotContains(t, store, kvSchedule, "the bad document is gone, not re-read every poll")
	require.Equal(t, "not json at all", string(store[kvQuarantinePrefix+kvSchedule]), "the bytes are kept")

	// And the feature works again on its own.
	require.NoError(t, p.addScheduleEntry("villa", "Arma", "", now+3600000, now))
	require.Len(t, mustSchedule(t, p, now), 1)
}

// The quarantine is shared with the airtime tables, and the System Console
// panel exists to tell an admin their irreplaceable hours are in trouble. A
// schedule that would not decode must not set off that alarm.
func TestAQuarantinedScheduleIsNotReportedAsDamagedAirtime(t *testing.T) {
	p, store := communityPlugin(t)
	now := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC).UnixMilli()

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("HasPermissionTo", mock.Anything, mock.Anything).Return(true)
	api.On("KVList", mock.Anything, mock.Anything).Return(
		func(page, perPage int) []string {
			if page > 0 {
				return nil
			}
			keys := make([]string, 0, len(store))
			for key := range store {
				keys = append(keys, key)
			}
			return keys
		},
		func(_, _ int) *model.AppError { return nil },
	)

	store[kvSchedule] = []byte(`not json at all`)
	require.Empty(t, mustSchedule(t, p, now))
	require.Contains(t, store, kvQuarantinePrefix+kvSchedule, "the bytes are still kept")

	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/airtime/status", nil)
	request.Header.Set("Mattermost-User-Id", "admin")
	recorder := httptest.NewRecorder()
	p.ServeHTTP(nil, recorder, request)

	var status airtimeStatus
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &status))
	require.NotContains(t, status.Quarantined, kvSchedule, "a bad schedule is not damaged airtime")
	require.Empty(t, status.Quarantined)
}

// The store's own error text names tables and drivers; a sidebar gets a sentence.
func TestStoreFailuresDoNotLeakThroughTheEndpoints(t *testing.T) {
	p, _, faults := kvBackedPluginWithFaults(map[string][]byte{})
	now := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC).UnixMilli()

	faults.failRead(kvSchedule)

	err := p.addScheduleEntry("villa", "Arma", "", now+3600000, now)
	require.ErrorIs(t, err, errStore)

	recorder := httptest.NewRecorder()
	communityError(recorder, err)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "injected failure")
	require.Contains(t, recorder.Body.String(), "Try again")
}

// The vote's two documents are deleted once on activation rather than left in
// the store unread for ever.
func TestTheRetiredVoteDocumentsAreRemoved(t *testing.T) {
	p, store := communityPlugin(t)

	store["vote_active"] = []byte(`{"question":"Film?"}`)
	store["vote_last"] = []byte(`{"winner":"Dune"}`)
	store[kvSchedule] = []byte(`{"v":1,"entries":[]}`)

	p.removeRetiredDocuments()

	require.NotContains(t, store, "vote_active")
	require.NotContains(t, store, "vote_last")
	require.Contains(t, store, kvSchedule, "and nothing else is touched")
}

// A category is a hint for the eye, so an unknown one must not cost somebody
// their planned stream. Two values are kept, everything else is dropped, and
// the entry is saved either way.
func TestAScheduleCategoryIsKeptOrDroppedButNeverRefused(t *testing.T) {
	p, _ := communityPlugin(t)
	now := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC).UnixMilli()

	for index, tried := range []struct {
		given string
		want  string
	}{
		{"movie", "movie"},
		{"game", "game"},
		{"GAME", "game"},
		{"  Movie  ", "movie"},
		{"", ""},
		{"cooking", ""},
		{"<script>", ""},
	} {
		at := now + int64(3600000+(index*60000))
		require.NoError(t, p.addScheduleEntry("villa"+strconv.Itoa(index), "Session", tried.given, at, now),
			"a category of %q must not stop the stream being planned", tried.given)
	}

	items := mustSchedule(t, p, now)
	require.Len(t, items, 7)

	var got []string
	for _, item := range items {
		got = append(got, item.Category)
	}
	require.Equal(t, []string{"movie", "game", "game", "movie", "", "", ""}, got)
}

// An entry written before 2.0 has no category field at all. It must read back as
// an entry with no category rather than as an unreadable document — the whole
// schedule lives in one value, so one unreadable entry would lose the lot.
func TestAScheduleFromBeforeCategoriesStillReads(t *testing.T) {
	p, store := communityPlugin(t)
	now := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC).UnixMilli()

	store[kvSchedule] = []byte(`{"v":1,"entries":[` +
		`{"id":"a1","userId":"villa","title":"Friday film","startsAt":` +
		strconv.FormatInt(now+3600000, 10) + `,"addedAt":1}]}`)

	items := mustSchedule(t, p, now)
	require.Len(t, items, 1)
	require.Equal(t, "Friday film", items[0].Title)
	require.Equal(t, "", items[0].Category)
}

// A category stored by a later build, or edited into the document by hand, is
// not shown rather than shown as itself: the webapp switches on this value to
// pick an icon, and an unknown one must fall through to no icon.
func TestAnUnknownStoredCategoryIsNotHandedToTheWebapp(t *testing.T) {
	p, store := communityPlugin(t)
	now := time.Date(2026, 9, 23, 19, 0, 0, 0, time.UTC).UnixMilli()

	store[kvSchedule] = []byte(`{"v":1,"entries":[` +
		`{"id":"a1","userId":"villa","title":"Karaoke","category":"music","startsAt":` +
		strconv.FormatInt(now+3600000, 10) + `,"addedAt":1}]}`)

	items := mustSchedule(t, p, now)
	require.Len(t, items, 1)
	require.Equal(t, "", items[0].Category)
}
