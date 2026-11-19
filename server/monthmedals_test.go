package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The medal tally in people's profiles: every month that has ended keeps its
// podium, read from the monthly tables that were already being kept.

// medalScene is the in-memory KV store with what a medal needs besides the
// airtime: accounts for linked streams to resolve to, and a listing of the
// store paged the way the server pages it, which the tally must not rely on.
type medalScene struct {
	p      *Plugin
	api    *plugintest.API
	store  map[string][]byte
	faults *kvFaults

	// users are the accounts GetUser answers with, by id. A test can replace
	// one between polls, the way somebody changing their name would.
	users map[string]*model.User

	// duringList runs inside every listing, before the page asked for is cut,
	// so a test can change the store between one page and the next.
	duringList func(page int)
}

func newMedalScene(t *testing.T) *medalScene {
	t.Helper()

	p, store, faults := kvBackedPluginWithFaults(map[string][]byte{})
	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	scene := &medalScene{p: p, api: api, store: store, faults: faults}

	api.On("KVList", mock.Anything, mock.Anything).Return(
		func(page, perPage int) []string {
			if scene.duringList != nil {
				scene.duringList(page)
			}

			faults.lock.Lock()
			defer faults.lock.Unlock()

			// Sorted and cut by offset, the way the server pages it (ORDER BY
			// PKey, then LIMIT and OFFSET), so a key removed between two pages
			// does to this listing what it does to a real one.
			keys := make([]string, 0, len(store))
			for key := range store {
				keys = append(keys, key)
			}
			sort.Strings(keys)

			start := page * perPage
			if start >= len(keys) {
				return nil
			}

			return keys[start:min(start+perPage, len(keys))]
		},
		func(_, _ int) *model.AppError { return nil },
	)

	scene.users = map[string]*model.User{
		"u-dave":  {Id: "u-dave", Username: "dave", Nickname: "Dave"},
		"u-bob":   {Id: "u-bob", Username: "bob", Nickname: "Bob"},
		"u-erin":  {Id: "u-erin", Username: "erin", Nickname: "Erin"},
		"u-peggy": {Id: "u-peggy", Username: "peggy", Nickname: "Peggy"},
	}
	api.On("GetUser", mock.Anything).Return(
		func(userID string) *model.User { return scene.users[userID] },
		func(userID string) *model.AppError {
			if scene.users[userID] == nil {
				return model.NewAppError("GetUser", "app.user.missing", nil, "", http.StatusNotFound)
			}

			return nil
		},
	)

	return scene
}

func (s *medalScene) seedMonth(t *testing.T, month string, table statTable) {
	t.Helper()

	data, err := json.Marshal(table)
	require.NoError(t, err)
	s.store[kvStatsMonthPrefix+month] = data
}

// link attributes a stream key to an account the way setLink leaves it, which
// is the only half of a link the boards read.
func (s *medalScene) link(streamKey, userID string) {
	s.store[kvLinkByKeyPrefix+normalizeKey(streamKey)] = []byte(userID)
}

// tally is what one poll would put on the snapshot at now: a fresh resolver,
// as every poll makes.
func (s *medalScene) tally(now time.Time) []monthMedals {
	return s.p.monthMedalTally(now, newIdentityResolver(s.p), nil, false)
}

// pollAt is the airtime half of pollOnce at a chosen instant: credit, then the
// boards, in that order.
func (s *medalScene) pollAt(cfg *configuration, streams []apiStream, now time.Time) *snapshot {
	s.p.recordStreamTime(streams, cfg, now)

	current := &snapshot{Streams: streams, Configured: true}
	s.p.addBoards(current, cfg, now)

	return current
}

// calls counts the calls made to method, and when arg is not empty only those
// whose first argument is arg.
func (s *medalScene) calls(method, arg string) int {
	count := 0
	for _, call := range s.api.Calls {
		if call.Method != method {
			continue
		}
		if arg != "" && (len(call.Arguments) == 0 || fmt.Sprint(call.Arguments[0]) != arg) {
			continue
		}
		count++
	}

	return count
}

// listings is how many pages of the whole KV namespace have been asked for.
func (s *medalScene) listings() int {
	return s.calls("KVList", "")
}

// readings is how many times the months have been read. Every reading starts
// at the first month there could be, whatever is in the store.
func (s *medalScene) readings() int {
	return s.readsOf(firstPossibleMonth.Format("2006-01"))
}

func (s *medalScene) readsOf(month string) int {
	return s.calls("KVGet", kvStatsMonthPrefix+month)
}

const podiumReadWarning = "Could not read past months for the monthly medals; keeping what was known and trying again later"

// Late September 2026, so June to August have ended and September has not.
var inSeptember = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

// Every month that has ended counts, however long ago, with nothing new ever
// having been stored for it. The month in progress does not: its places are
// not settled, and the profile shows them on their own already.
func TestMonthMedalsCountEveryMonthThatHasEnded(t *testing.T) {
	scene := newMedalScene(t)

	for key, userID := range map[string]string{
		"videokey-dave": "u-dave", "videokey-bob": "u-bob", "videokey-erin": "u-erin", "videokey-peggy": "u-peggy",
	} {
		scene.link(key, userID)
	}

	scene.seedMonth(t, "2026-06", statTable{
		"videokey-dave":  {Seconds: 9000, Title: "Dave"},
		"videokey-bob":   {Seconds: 7000, Title: "Bob"},
		"videokey-erin":  {Seconds: 5000, Title: "Erin"},
		"videokey-peggy": {Seconds: 3000, Title: "Peggy"},
	})
	scene.seedMonth(t, "2026-07", statTable{
		"videokey-dave":  {Seconds: 4000, Title: "Dave"},
		"videokey-erin":  {Seconds: 2000, Title: "Erin"},
		"videokey-peggy": {Seconds: 0, Title: "Peggy"},
	})
	scene.seedMonth(t, "2026-08", statTable{
		"videokey-bob":  {Seconds: 8000, Title: "Bob"},
		"videokey-dave": {Seconds: 6000, Title: "Dave"},
		"videokey-erin": {Seconds: 1000, Title: "Erin"},
	})
	scene.seedMonth(t, "2026-09", statTable{
		"videokey-peggy": {Seconds: 99000, Title: "Peggy"},
	})

	// Keys no real month has, which an import would refuse and so must this.
	scene.seedMonth(t, "1999-01", statTable{"videokey-peggy": {Seconds: 1, Title: "Peggy"}})
	scene.seedMonth(t, "2026-13", statTable{"videokey-peggy": {Seconds: 1, Title: "Peggy"}})

	tally := scene.tally(inSeptember)

	require.Equal(t, []monthMedals{
		{UserID: "u-bob", Username: "bob", DisplayName: "Bob", Gold: []string{"2026-08"}, Silver: []string{"2026-06"}},
		{UserID: "u-dave", Username: "dave", DisplayName: "Dave", Gold: []string{"2026-06", "2026-07"}, Silver: []string{"2026-08"}},
		{UserID: "u-erin", Username: "erin", DisplayName: "Erin", Silver: []string{"2026-07"}, Bronze: []string{"2026-06", "2026-08"}},
	}, tally, "fourth place and zero seconds win nothing, and September is not over")

	encoded, err := json.Marshal(tally)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "videokey-", "a medal names a person, never a stream key")
	require.NotContains(t, string(encoded), "2026-09")
	require.Equal(t, 0, scene.readsOf("2026-09"), "the month in progress is not even read")
}

// The months are found by name, not in a listing of the KV store. A listing is
// paged by offset, so a key ahead of the months that is removed between two
// pages — somebody unlinking their stream while it runs — moves every later
// key back one place, and the one on the boundary is never returned, with no
// error to say so. Remembered until the next rollover, that would be a month
// somebody won missing from their profile for weeks.
func TestAMonthIsNotLostToAListingThatShiftsUnderIt(t *testing.T) {
	scene := newMedalScene(t)

	// Exactly a page of links ahead of the months, so July is the first key
	// of the second page.
	for i := 0; i < 199; i++ {
		scene.link(fmt.Sprintf("videokey-guest-%03d", i), fmt.Sprintf("u-guest-%03d", i))
	}
	scene.link("videokey-dave", "u-dave")
	scene.seedMonth(t, "2026-07", statTable{"videokey-dave": {Seconds: 900, Title: "Dave"}})
	scene.seedMonth(t, "2026-08", statTable{"videokey-dave": {Seconds: 900, Title: "Dave"}})

	// Somebody unlinks between the first page and the second.
	scene.duringList = func(page int) {
		if page == 1 {
			delete(scene.store, kvLinkByKeyPrefix+"videokey-guest-000")
		}
	}

	require.Equal(t, []monthMedals{{UserID: "u-dave", Username: "dave", DisplayName: "Dave", Gold: []string{"2026-07", "2026-08"}}}, scene.tally(inSeptember))
	require.Zero(t, scene.listings(), "read by name, there is no listing to shift")
}

// Reading by name starts at firstPossibleMonth, so it has to be the first month
// monthSuffix lets in: any later and the earliest real months would never be
// read, any earlier and every reading would pay for months that cannot count.
func TestTheFirstPossibleMonthIsTheFirstRealOne(t *testing.T) {
	require.True(t, monthSuffix.MatchString(firstPossibleMonth.Format("2006-01")))
	require.False(t, monthSuffix.MatchString(firstPossibleMonth.AddDate(0, -1, 0).Format("2006-01")))

	scene := newMedalScene(t)
	scene.link("videokey-dave", "u-dave")
	scene.seedMonth(t, firstPossibleMonth.Format("2006-01"), statTable{"videokey-dave": {Seconds: 900, Title: "Dave"}})

	require.Equal(t, []monthMedals{{UserID: "u-dave", Username: "dave", DisplayName: "Dave", Gold: []string{"2020-01"}}}, scene.tally(inSeptember))
}

// A podium is the board's own top three, so a tie is settled exactly as the
// board settles it. The board is by person (design/twitch-live.md §7.8):
// seconds, then the person's name folded, then the row; a stream nobody has
// linked sorts by its stored title, as rankRows always did.
func TestMonthMedalsSettleTiesTheWayTheBoardDid(t *testing.T) {
	scene := newMedalScene(t)

	june := statTable{
		"videokey-b": {Seconds: 100, Title: "Same"},
		"videokey-a": {Seconds: 100, Title: "same"},
		"videokey-c": {Seconds: 100, Title: "SAME"},
		"videokey-d": {Seconds: 100, Title: "Alpha"},
	}
	scene.seedMonth(t, "2026-06", june)
	scene.link("videokey-a", "u-bob")
	scene.link("videokey-b", "u-erin")
	scene.link("videokey-c", "u-peggy")
	scene.link("videokey-d", "u-dave")

	board := scene.p.boardRows(june, nil, leaderboardConfig(), nil, nil)
	require.Equal(t, []string{"u:u-bob", "u:u-dave", "u:u-erin", "u:u-peggy"},
		[]string{board[0].id, board[1].id, board[2].id, board[3].id})

	require.Equal(t, []monthMedals{
		{UserID: "u-bob", Username: "bob", DisplayName: "Bob", Gold: []string{"2026-06"}},
		{UserID: "u-dave", Username: "dave", DisplayName: "Dave", Silver: []string{"2026-06"}},
		{UserID: "u-erin", Username: "erin", DisplayName: "Erin", Bronze: []string{"2026-06"}},
	}, scene.tally(inSeptember), "u-peggy was fourth on the board, so wins nothing")
}

// A stream nobody has linked has no profile to show a medal in. Its places are
// not thrown away, though: linking it later credits the months it already won,
// without the months having to be read again.
func TestMonthMedalsFollowAStreamLinkedLater(t *testing.T) {
	scene := newMedalScene(t)

	scene.seedMonth(t, "2026-07", statTable{
		"videokey-dave":  {Seconds: 900, Title: "Dave"},
		"videokey-ghost": {Seconds: 800, Title: "Ghost"},
	})
	scene.seedMonth(t, "2026-08", statTable{
		"videokey-ghost": {Seconds: 700, Title: "Ghost"},
	})
	scene.link("videokey-dave", "u-dave")

	require.Equal(t, []monthMedals{{UserID: "u-dave", Username: "dave", DisplayName: "Dave", Gold: []string{"2026-07"}}}, scene.tally(inSeptember))
	require.Equal(t, 1, scene.readings())

	scene.link("videokey-ghost", "u-bob")

	require.Equal(t, []monthMedals{
		{UserID: "u-bob", Username: "bob", DisplayName: "Bob", Gold: []string{"2026-08"}, Silver: []string{"2026-07"}},
		{UserID: "u-dave", Username: "dave", DisplayName: "Dave", Gold: []string{"2026-07"}},
	}, scene.tally(inSeptember.Add(time.Minute)))
	require.Equal(t, 1, scene.readings(), "who owns a key is looked up per poll; the podiums are not read again for it")
}

// Every entry names its person the way the boards name a row: the username,
// and the display name displayNameOf picks. The trophy menu lists the tally
// beside the boards with no lookup of its own, so the two must never disagree
// about what somebody is called.
func TestMonthMedalsNameThePersonAsTheBoardsDo(t *testing.T) {
	scene := newMedalScene(t)
	cfg := leaderboardConfig()

	// One of each way a display name comes out: a nickname, a full name, and
	// neither, which falls back to the username.
	scene.users["u-bob"] = &model.User{Id: "u-bob", Username: "bob", FirstName: "Bobby", LastName: "Quinn"}
	scene.users["u-erin"] = &model.User{Id: "u-erin", Username: "erin"}

	scene.link("videokey-dave", "u-dave")
	scene.link("videokey-bob", "u-bob")
	scene.link("videokey-erin", "u-erin")

	august := statTable{
		"videokey-dave": {Seconds: 900, Title: "Dave"},
		"videokey-bob":  {Seconds: 800, Title: "Bob"},
		"videokey-erin": {Seconds: 700, Title: "Erin"},
	}
	scene.seedMonth(t, "2026-08", august)
	seedTotals(t, scene.store, august)

	current := scene.pollAt(cfg, nil, inSeptember)
	require.Equal(t, []monthMedals{
		{UserID: "u-bob", Username: "bob", DisplayName: "Bobby Quinn", Silver: []string{"2026-08"}},
		{UserID: "u-dave", Username: "dave", DisplayName: "Dave", Gold: []string{"2026-08"}},
		{UserID: "u-erin", Username: "erin", DisplayName: "erin", Bronze: []string{"2026-08"}},
	}, current.MonthMedals)

	require.Len(t, current.AllTime, 3)
	for _, row := range current.AllTime {
		found := false
		for _, medals := range current.MonthMedals {
			if medals.UserID != row.UserID {
				continue
			}
			found = true
			require.Equal(t, row.Username, medals.Username)
			require.Equal(t, row.DisplayName, medals.DisplayName)
		}
		require.True(t, found, row.UserID)
	}
}

// Names are looked up afresh every poll, the same as the boards', so somebody
// who changes theirs is called the new name on the very next one. Nothing else
// moves: the same places, and the months are not read again for it.
func TestARenameShowsOnTheNextPoll(t *testing.T) {
	scene := newMedalScene(t)
	cfg := leaderboardConfig()

	scene.link("videokey-dave", "u-dave")
	scene.link("videokey-bob", "u-bob")
	scene.seedMonth(t, "2026-07", statTable{
		"videokey-dave": {Seconds: 900, Title: "Dave"},
		"videokey-bob":  {Seconds: 800, Title: "Bob"},
	})
	scene.seedMonth(t, "2026-08", statTable{"videokey-bob": {Seconds: 900, Title: "Bob"}})
	seedTotals(t, scene.store, statTable{
		"videokey-dave": {Seconds: 900, Title: "Dave"},
		"videokey-bob":  {Seconds: 1700, Title: "Bob"},
	})

	before := scene.pollAt(cfg, nil, inSeptember)
	require.Equal(t, []monthMedals{
		{UserID: "u-bob", Username: "bob", DisplayName: "Bob", Gold: []string{"2026-08"}, Silver: []string{"2026-07"}},
		{UserID: "u-dave", Username: "dave", DisplayName: "Dave", Gold: []string{"2026-07"}},
	}, before.MonthMedals)

	scene.users["u-dave"] = &model.User{Id: "u-dave", Username: "dave.r", Nickname: "Dave R."}

	after := scene.pollAt(cfg, nil, inSeptember.Add(10*time.Second))
	require.Equal(t, []monthMedals{
		{UserID: "u-bob", Username: "bob", DisplayName: "Bob", Gold: []string{"2026-08"}, Silver: []string{"2026-07"}},
		{UserID: "u-dave", Username: "dave.r", DisplayName: "Dave R.", Gold: []string{"2026-07"}},
	}, after.MonthMedals, "the new name, and the same places")
	require.Equal(t, 1, scene.readings(), "a rename is no reason to read the months again")
}

// Somebody with two stream keys is one person with one profile, and one row
// on each month's board: the keys are added up before the top three is cut
// (design/twitch-live.md §7.8), so in July Dave's two keys make one Gold
// (900 + 500 against Bob's 100), and Bob is second, not third behind a second
// Dave.
func TestMonthMedalsMergeTwoKeysOfOnePerson(t *testing.T) {
	scene := newMedalScene(t)

	scene.link("videokey-dave", "u-dave")
	scene.link("videokey-dave-late", "u-dave")
	scene.link("videokey-bob", "u-bob")

	scene.seedMonth(t, "2026-05", statTable{
		"videokey-dave": {Seconds: 900, Title: "Dave"},
		"videokey-bob":  {Seconds: 100, Title: "Bob"},
	})
	scene.seedMonth(t, "2026-06", statTable{
		"videokey-dave-late": {Seconds: 900, Title: "Dave"},
		"videokey-bob":       {Seconds: 100, Title: "Bob"},
	})
	scene.seedMonth(t, "2026-07", statTable{
		"videokey-dave-late": {Seconds: 900, Title: "Dave"},
		"videokey-dave":      {Seconds: 500, Title: "Dave"},
		"videokey-bob":       {Seconds: 100, Title: "Bob"},
	})

	require.Equal(t, []monthMedals{
		{UserID: "u-bob", Username: "bob", DisplayName: "Bob", Silver: []string{"2026-05", "2026-06", "2026-07"}},
		{UserID: "u-dave", Username: "dave", DisplayName: "Dave", Gold: []string{"2026-05", "2026-06", "2026-07"}},
	}, scene.tally(inSeptember), "one entry for the person, one place a month, each month in order")
}

// The whole point of the cache: a poll every few seconds must not list the
// whole KV store, or read every month there has ever been, to learn nothing.
func TestTheMonthsAreNotReadOnEveryPoll(t *testing.T) {
	scene := newMedalScene(t)
	cfg := leaderboardConfig()

	scene.link("videokey-dave", "u-dave")
	scene.seedMonth(t, "2026-07", statTable{"videokey-dave": {Seconds: 900, Title: "Dave"}})
	scene.seedMonth(t, "2026-08", statTable{"videokey-dave": {Seconds: 900, Title: "Dave"}})

	// On this month's board and the all-time one as well, which is the usual
	// case for somebody with medals.
	scene.seedMonth(t, "2026-09", statTable{"videokey-dave": {Seconds: 900, Title: "Dave"}})
	seedTotals(t, scene.store, statTable{"videokey-dave": {Seconds: 2700, Title: "Dave"}})

	for poll := 0; poll < 20; poll++ {
		current := scene.pollAt(cfg, nil, inSeptember.Add(time.Duration(poll)*10*time.Second))
		require.Equal(t, []monthMedals{{UserID: "u-dave", Username: "dave", DisplayName: "Dave", Gold: []string{"2026-07", "2026-08"}}}, current.MonthMedals)
		require.Equal(t, poll+1, scene.calls("KVGet", kvLinkByKeyPrefix+"videokey-dave"),
			"one lookup of who owns the stream per poll, shared by both boards and the tally")
	}

	require.Equal(t, 1, scene.readings(), "read once, not once a poll")
	require.Equal(t, 1, scene.readsOf("2026-07"))
	require.Equal(t, 1, scene.readsOf("2026-08"))
}

// Crossing midnight on the 1st: the month that has just ended starts to count,
// and it counts with the seconds the first poll of the new month paid it —
// which here are what decide who won it.
func TestTheMonthThatHasJustEndedCountsFromTheFirstPollAfterIt(t *testing.T) {
	scene := newMedalScene(t)
	cfg := leaderboardConfig()

	scene.link("videokey-dave", "u-dave")
	scene.link("videokey-bob", "u-bob")
	scene.seedMonth(t, "2026-09", statTable{"videokey-bob": {Seconds: 3000, Title: "Bob"}})

	start := time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC)
	streams := []apiStream{live("videokey-dave", "Dave", start.Format(time.RFC3339Nano))}

	// Twenty minutes before the end of the month, Bob is ahead.
	before := scene.pollAt(cfg, streams, start.Add(40*time.Minute))
	require.Empty(t, before.MonthMedals, "September has not ended")
	require.Equal(t, "Bob", before.Month[0].Title)
	require.EqualValues(t, 2400, hours(scene.p, "stats_month_2026-09", "videokey-dave"))

	// The first poll of October pays September its last twenty minutes, which
	// puts Dave ahead. The podium is read after that, not before.
	after := scene.pollAt(cfg, streams, time.Date(2026, 10, 1, 0, 0, 10, 0, time.UTC))
	require.EqualValues(t, 3600, hours(scene.p, "stats_month_2026-09", "videokey-dave"))
	require.EqualValues(t, 10, hours(scene.p, "stats_month_2026-10", "videokey-dave"))
	require.Equal(t, "October 2026", after.MonthLabel)
	require.Equal(t, []monthMedals{
		{UserID: "u-bob", Username: "bob", DisplayName: "Bob", Silver: []string{"2026-09"}},
		{UserID: "u-dave", Username: "dave", DisplayName: "Dave", Gold: []string{"2026-09"}},
	}, after.MonthMedals)
	require.Equal(t, 2, scene.readings(), "read once in September and once for the rollover")
}

// The final seconds of a month are not always paid on the first poll of the
// next one. When they arrive later they still reach the podium, and a poll
// that only credits the month in progress reads nothing.
func TestAFinalCreditPaidLateStillReachesTheEndedMonthsPodium(t *testing.T) {
	scene := newMedalScene(t)
	cfg := leaderboardConfig()

	scene.link("videokey-dave", "u-dave")
	scene.link("videokey-bob", "u-bob")
	scene.seedMonth(t, "2026-09", statTable{"videokey-bob": {Seconds: 3000, Title: "Bob"}})

	start := time.Date(2026, 9, 30, 23, 0, 0, 0, time.UTC)
	streams := []apiStream{live("videokey-dave", "Dave", start.Format(time.RFC3339Nano))}
	scene.pollAt(cfg, streams, start.Add(40*time.Minute))

	// The first poll of October cannot read the session marks, so it credits
	// nothing at all. It is still the poll that reads September's podium, and
	// on what has been paid so far Bob won it.
	scene.faults.failRead(kvStatsSessions)
	first := scene.pollAt(cfg, streams, time.Date(2026, 10, 1, 0, 0, 10, 0, time.UTC))
	require.Equal(t, []monthMedals{
		{UserID: "u-bob", Username: "bob", DisplayName: "Bob", Gold: []string{"2026-09"}},
		{UserID: "u-dave", Username: "dave", DisplayName: "Dave", Silver: []string{"2026-09"}},
	}, first.MonthMedals)
	read := scene.readings()

	// The next poll pays September what it was owed, and that changes the
	// podium, so it is read again.
	scene.faults.healRead(kvStatsSessions)
	second := scene.pollAt(cfg, streams, time.Date(2026, 10, 1, 0, 0, 20, 0, time.UTC))
	require.EqualValues(t, 3600, hours(scene.p, "stats_month_2026-09", "videokey-dave"))
	require.Equal(t, []monthMedals{
		{UserID: "u-bob", Username: "bob", DisplayName: "Bob", Silver: []string{"2026-09"}},
		{UserID: "u-dave", Username: "dave", DisplayName: "Dave", Gold: []string{"2026-09"}},
	}, second.MonthMedals)
	require.Equal(t, read+1, scene.readings())

	// From here on every poll credits October only, which has not ended.
	for poll := 1; poll <= 5; poll++ {
		scene.pollAt(cfg, streams, time.Date(2026, 10, 1, 0, 0, 20+poll*10, 0, time.UTC))
	}
	require.Equal(t, read+1, scene.readings(), "crediting the month in progress changes no podium")
}

// An admin import can rewrite a month that has ended, and the next poll shows
// its new podium. An import that only touches the totals rewrites no podium
// and reads nothing.
func TestAnAirtimeImportMovesAnEndedMonthsPodium(t *testing.T) {
	scene := newMedalScene(t)
	scene.api.On("HasPermissionTo", mock.Anything, mock.Anything).Return(true)

	scene.link("videokey-dave", "u-dave")
	scene.link("videokey-bob", "u-bob")
	scene.seedMonth(t, "2026-06", statTable{
		"videokey-dave": {Seconds: 900, Title: "Dave"},
		"videokey-bob":  {Seconds: 800, Title: "Bob"},
	})

	require.Equal(t, []monthMedals{
		{UserID: "u-bob", Username: "bob", DisplayName: "Bob", Silver: []string{"2026-06"}},
		{UserID: "u-dave", Username: "dave", DisplayName: "Dave", Gold: []string{"2026-06"}},
	}, scene.tally(inSeptember))

	importing := func(body string) {
		t.Helper()

		request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/airtime", bytes.NewReader([]byte(body)))
		request.Header.Set("Mattermost-User-Id", "admin-user")
		recorder := httptest.NewRecorder()
		scene.p.ServeHTTP(nil, recorder, request)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	}

	importing(`{"version":1,"mode":"merge","totals":{"videokey-bob":{"seconds":5000,"title":"Bob"}}}`)
	scene.tally(inSeptember.Add(time.Minute))
	require.Equal(t, 1, scene.readings(), "the totals are not a monthly podium")

	importing(`{"version":1,"mode":"merge","months":{"2026-06":{"videokey-bob":{"seconds":5000,"title":"Bob"}}}}`)
	require.Equal(t, []monthMedals{
		{UserID: "u-bob", Username: "bob", DisplayName: "Bob", Gold: []string{"2026-06"}},
		{UserID: "u-dave", Username: "dave", DisplayName: "Dave", Silver: []string{"2026-06"}},
	}, scene.tally(inSeptember.Add(2*time.Minute)))
	require.Equal(t, 2, scene.readings())
}

// An import that lands while the months are being read must not be lost to
// that read: what it read may be from before the import.
func TestAForgetDuringAReadLeavesThePodiumsStale(t *testing.T) {
	scene := newMedalScene(t)

	scene.link("videokey-dave", "u-dave")
	scene.seedMonth(t, "2026-06", statTable{"videokey-dave": {Seconds: 900, Title: "Dave"}})

	// Part way through the first reading, once June is in hand.
	forgotten := false
	scene.faults.onRead = func(key string) {
		if key == kvStatsMonthPrefix+"2026-06" && !forgotten {
			forgotten = true
			scene.p.forgetMonthPodiums()
		}
	}

	require.NotEmpty(t, scene.tally(inSeptember))
	scene.tally(inSeptember.Add(time.Minute))
	require.Equal(t, 2, scene.readings(), "read again, because it was forgotten while being read")

	scene.tally(inSeptember.Add(2 * time.Minute))
	require.Equal(t, 2, scene.readings(), "and then kept")
}

// A month the database could not hand over is not a month nobody won.
// Remembering the gap would take medals out of people's profiles until the
// next rollover, so nothing is remembered — and nothing is tried again for a
// few minutes, rather than on every poll against a struggling database.
func TestAMonthThatCouldNotBeReadIsNotRememberedAsEmpty(t *testing.T) {
	scene := newMedalScene(t)

	scene.link("videokey-dave", "u-dave")
	scene.seedMonth(t, "2026-06", statTable{"videokey-dave": {Seconds: 900, Title: "Dave"}})
	scene.seedMonth(t, "2026-07", statTable{"videokey-dave": {Seconds: 900, Title: "Dave"}})

	scene.faults.failRead(kvStatsMonthPrefix + "2026-07")

	require.Empty(t, scene.tally(inSeptember), "nothing known yet, and a partial reading is not shown")
	require.Equal(t, 1, scene.calls("LogWarn", podiumReadWarning))

	// Polls keep coming; none of them tries again inside the wait.
	for poll := 1; poll <= 10; poll++ {
		require.Empty(t, scene.tally(inSeptember.Add(time.Duration(poll)*10*time.Second)))
	}
	require.Equal(t, 1, scene.readings())
	require.Equal(t, 1, scene.readsOf("2026-07"))
	require.Equal(t, 1, scene.calls("LogWarn", podiumReadWarning), "one warning per attempt, not per poll")

	// The database recovers, but the wait is still running.
	scene.faults.healRead(kvStatsMonthPrefix + "2026-07")
	require.Empty(t, scene.tally(inSeptember.Add(2*time.Minute)))
	require.Equal(t, 1, scene.readings())

	// Once it is over, the whole reading is taken.
	later := inSeptember.Add(monthPodiumRetry + time.Second)
	want := []monthMedals{{UserID: "u-dave", Username: "dave", DisplayName: "Dave", Gold: []string{"2026-06", "2026-07"}}}
	require.Equal(t, want, scene.tally(later))
	require.Equal(t, 2, scene.readings())

	// With something known, a failed re-read keeps what was known rather than
	// emptying it.
	scene.faults.failRead(kvStatsMonthPrefix + "2026-06")
	scene.p.forgetMonthPodiums()
	require.Equal(t, want, scene.tally(later.Add(time.Minute)), "the last complete reading stays")
	require.Equal(t, 2, scene.calls("LogWarn", podiumReadWarning))

	// And the same holds for a month with no table at all. A database that
	// could not say there is nothing there has not said there is nothing
	// there, and August could be a month somebody won.
	scene.faults.healRead(kvStatsMonthPrefix + "2026-06")
	scene.faults.failRead(kvStatsMonthPrefix + "2026-08")
	later = later.Add(monthPodiumRetry + 2*time.Minute)
	require.Equal(t, want, scene.tally(later))
	require.Equal(t, 3, scene.calls("LogWarn", podiumReadWarning))

	scene.faults.healRead(kvStatsMonthPrefix + "2026-08")
	require.Equal(t, want, scene.tally(later.Add(monthPodiumRetry+time.Second)))
	require.Equal(t, 3, scene.calls("LogWarn", podiumReadWarning), "and a good read logs nothing")
}

// Bytes that will not decode will not decode next time either. That month is
// kept aside, counts for nothing, and does not stop the other months counting
// or cost a read on every poll. It does not stop recording either: nothing
// writes to a month that has ended.
func TestAnUnreadableMonthCountsForNothing(t *testing.T) {
	scene := newMedalScene(t)

	scene.link("videokey-dave", "u-dave")
	scene.link("videokey-bob", "u-bob")
	scene.seedMonth(t, "2026-06", statTable{"videokey-dave": {Seconds: 900, Title: "Dave"}})
	scene.store[kvStatsMonthPrefix+"2026-07"] = []byte(`{"videokey-bob":{"seconds":"lots"`)
	scene.seedMonth(t, "2026-08", statTable{"videokey-bob": {Seconds: 900, Title: "Bob"}})
	original := append([]byte(nil), scene.store[kvStatsMonthPrefix+"2026-07"]...)

	want := []monthMedals{
		{UserID: "u-bob", Username: "bob", DisplayName: "Bob", Gold: []string{"2026-08"}},
		{UserID: "u-dave", Username: "dave", DisplayName: "Dave", Gold: []string{"2026-06"}},
	}
	require.Equal(t, want, scene.tally(inSeptember))

	for poll := 1; poll <= 5; poll++ {
		require.Equal(t, want, scene.tally(inSeptember.Add(time.Duration(poll)*time.Minute)))
	}
	require.Equal(t, 1, scene.readings(), "remembered: retrying would change nothing")
	require.Equal(t, 1, scene.readsOf("2026-07"))
	require.Zero(t, scene.calls("LogWarn", podiumReadWarning), "not the transient kind")

	require.Equal(t, string(original), string(scene.store[kvQuarantinePrefix+kvStatsMonthPrefix+"2026-07"]), "a verbatim copy is kept aside")
	require.Equal(t, string(original), string(scene.store[kvStatsMonthPrefix+"2026-07"]), "and the month itself is left alone")

	halted, _ := scene.p.statsHalt()
	require.False(t, halted)
}

// The tally rides with the boards: when they could not be built there is no
// tally either, rather than one beside boards that are missing.
func TestNoTallyWithoutTheBoards(t *testing.T) {
	scene := newMedalScene(t)
	cfg := leaderboardConfig()

	scene.link("videokey-dave", "u-dave")
	scene.seedMonth(t, "2026-06", statTable{"videokey-dave": {Seconds: 900, Title: "Dave"}})
	seedTotals(t, scene.store, statTable{"videokey-dave": {Seconds: 900, Title: "Dave"}})

	scene.faults.failRead(kvStatsTotals)
	current := scene.pollAt(cfg, nil, inSeptember)
	require.Empty(t, current.AllTime)
	require.Nil(t, current.MonthMedals)
	require.Zero(t, scene.readings())

	scene.faults.healRead(kvStatsTotals)
	current = scene.pollAt(cfg, nil, inSeptember.Add(time.Minute))
	require.NotEmpty(t, current.AllTime)
	require.NotEmpty(t, current.MonthMedals)
}

// The same people and the same months must come out in the same order every
// time, whatever order the maps inside gave them in, or the snapshot's
// fingerprint would move and push a message to every client about nothing.
func TestTheTallyIsTheSameEveryTime(t *testing.T) {
	scene := newMedalScene(t)

	for i, userID := range []string{"u-dave", "u-bob", "u-erin", "u-peggy"} {
		scene.link(fmt.Sprintf("videokey-%d", i), userID)
	}
	for month := 1; month <= 8; month++ {
		table := statTable{}
		for i := 0; i < 4; i++ {
			table[fmt.Sprintf("videokey-%d", i)] = statEntry{Seconds: int64(100 + ((i+month)%4)*10), Title: "Someone"}
		}
		scene.seedMonth(t, fmt.Sprintf("2026-%02d", month), table)
	}

	first, err := json.Marshal(scene.tally(inSeptember))
	require.NoError(t, err)

	for attempt := 0; attempt < 20; attempt++ {
		scene.p.forgetMonthPodiums()

		again, marshalErr := json.Marshal(scene.tally(inSeptember))
		require.NoError(t, marshalErr)
		require.Equal(t, string(first), string(again))
	}
}

// The whole poll, against a Broadcast Box that answers: the tally goes out on
// the snapshot, consecutive polls publish nothing new, the months are read
// once, and with the leaderboard switched off there is no tally at all.
func TestAPollCarriesTheTallyAndOnlyWhenTheLeaderboardIsOn(t *testing.T) {
	var down atomic.Bool
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		if down.Load() {
			http.Error(w, "busy", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[]`)
	}))
	defer origin.Close()

	scene := newMedalScene(t)
	scene.api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return()
	scene.p.httpClient = origin.Client()

	// Real time, because pollOnce reads the clock itself: the two months
	// before this one, whenever this runs.
	thisMonth := time.Now().UTC()
	thisMonth = time.Date(thisMonth.Year(), thisMonth.Month(), 1, 0, 0, 0, 0, time.UTC)
	lastMonth := thisMonth.AddDate(0, -1, 0).Format("2006-01")
	monthBefore := thisMonth.AddDate(0, -2, 0).Format("2006-01")

	scene.link("videokey-dave", "u-dave")
	scene.link("videokey-bob", "u-bob")
	scene.seedMonth(t, monthBefore, statTable{"videokey-dave": {Seconds: 900, Title: "Dave"}, "videokey-bob": {Seconds: 800, Title: "Bob"}})
	scene.seedMonth(t, lastMonth, statTable{"videokey-dave": {Seconds: 900, Title: "Dave"}})

	cfg := &configuration{BroadcastBoxURL: origin.URL, EnableLeaderboard: true}
	cfg.normalize()
	scene.p.setConfiguration(cfg)

	scene.p.pollOnce(t.Context(), true)
	published := scene.p.lastPublished

	current := scene.p.getSnapshot()
	require.Equal(t, []monthMedals{
		{UserID: "u-bob", Username: "bob", DisplayName: "Bob", Silver: []string{monthBefore}},
		{UserID: "u-dave", Username: "dave", DisplayName: "Dave", Gold: []string{monthBefore, lastMonth}},
	}, current.MonthMedals)

	// Sent with the boards, where the Trophies card reads the tally, and as
	// each person's own row on their profile; the snapshot has neither.
	encoded, err := json.Marshal(scene.p.boardsNow())
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"monthMedals":[{"userId":"u-bob","username":"bob","displayName":"Bob","silver":["`+monthBefore+`"]}`)
	require.Contains(t, string(encoded), `{"kind":"months","id":"past","rank":1,"title":"Monthly champion","gold":["`+lastMonth+`","`+monthBefore+`"]}`)
	require.NotContains(t, string(encoded), "videokey-")

	encoded, err = json.Marshal(current)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "monthMedals")
	require.NotContains(t, string(encoded), "videokey-")

	for poll := 0; poll < 3; poll++ {
		scene.p.pollOnce(t.Context(), true)
		require.Equal(t, published, scene.p.lastPublished, "an unchanged tally is an unchanged snapshot")
	}
	require.Equal(t, 1, scene.calls("PublishWebSocketEvent", wsEventStreams), "so nothing is pushed after the first poll")
	require.Equal(t, 1, scene.readsOf(monthBefore))
	require.Equal(t, 1, scene.readsOf(lastMonth))

	// A hiccup at the origin keeps the last boards on screen, and the tally
	// stays with them rather than vanishing from every profile until the next
	// good poll.
	down.Store(true)
	scene.p.pollOnce(t.Context(), true)
	current = scene.p.getSnapshot()
	require.NotEmpty(t, current.Error)
	require.Len(t, current.MonthMedals, 2)
	down.Store(false)

	off := cfg.Clone()
	off.EnableLeaderboard = false
	scene.p.setConfiguration(off)
	scene.p.pollOnce(t.Context(), true)

	current = scene.p.getSnapshot()
	require.Nil(t, current.MonthMedals)
	require.NotContains(t, jsonOf(t, scene.p.boardsNow()), "monthMedals", "nor in what GET /boards sends")
}
