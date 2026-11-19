package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The custom status emoji is the only thing a plugin can get into the members
// list, and there is one of it per person. It is borrowed for a broadcast and
// handed straight back; these are the rules for that.

type statusStore struct {
	lock     sync.Mutex
	statuses map[string]*model.CustomStatus
	kv       map[string][]byte
	sets     map[string]int
	failOnce bool
}

// failNextUpdate makes the next status write fail, the way a row lock or a
// moment of database trouble would.
func (s *statusStore) failNextUpdate() {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.failOnce = true
}

// A fixed instant for the badge tests: an afternoon in March that is nobody's
// birthday in the list any of them configure. Fixed rather than time.Now() so a
// test cannot start passing or failing because of the date it is run on.
var badgeClock = time.Date(2026, 3, 11, 14, 0, 0, 0, time.UTC)

func badgePlugin(t *testing.T) (*Plugin, *statusStore) {
	t.Helper()

	store := &statusStore{
		statuses: map[string]*model.CustomStatus{},
		kv:       map[string][]byte{},
		sets:     map[string]int{},
	}

	api := &plugintest.API{}
	api.On("KVGet", mock.Anything).Return(
		func(key string) []byte {
			store.lock.Lock()
			defer store.lock.Unlock()

			return append([]byte(nil), store.kv[key]...)
		},
		func(_ string) *model.AppError { return nil },
	)
	api.On("KVSet", mock.Anything, mock.Anything).Return(
		func(key string, value []byte) *model.AppError {
			store.lock.Lock()
			defer store.lock.Unlock()
			store.kv[key] = value

			return nil
		},
	)
	api.On("KVDelete", mock.Anything).Return(
		func(key string) *model.AppError {
			store.lock.Lock()
			defer store.lock.Unlock()
			delete(store.kv, key)

			return nil
		},
	)
	api.On("KVList", mock.Anything, mock.Anything).Return(
		func(page, perPage int) []string {
			store.lock.Lock()
			defer store.lock.Unlock()

			if page > 0 {
				return nil
			}
			keys := make([]string, 0, len(store.kv))
			for key := range store.kv {
				keys = append(keys, key)
			}

			return keys
		},
		func(_, _ int) *model.AppError { return nil },
	)
	api.On("GetUser", mock.Anything).Return(
		func(userID string) *model.User {
			store.lock.Lock()
			defer store.lock.Unlock()

			user := &model.User{Id: userID, Username: userID}
			if status := store.statuses[userID]; status != nil {
				props, err := json.Marshal(status)
				require.NoError(t, err)
				user.Props = model.StringMap{model.UserPropsKeyCustomStatus: string(props)}
			}

			return user
		},
		func(_ string) *model.AppError { return nil },
	)
	api.On("UpdateUserCustomStatus", mock.Anything, mock.Anything).Return(
		func(userID string, status *model.CustomStatus) *model.AppError {
			store.lock.Lock()
			defer store.lock.Unlock()

			if store.failOnce {
				store.failOnce = false

				return &model.AppError{Message: "briefly unhappy"}
			}

			store.statuses[userID] = status
			store.sets[userID]++

			return nil
		},
	)
	api.On("RemoveUserCustomStatus", mock.Anything).Return(
		func(userID string) *model.AppError {
			store.lock.Lock()
			defer store.lock.Unlock()
			delete(store.statuses, userID)

			return nil
		},
	)
	for _, level := range []string{"LogWarn", "LogError", "LogInfo", "LogDebug"} {
		for pairs := 0; pairs <= 8; pairs++ {
			args := make([]any, 0, 1+(pairs*2))
			for i := 0; i < 1+(pairs*2); i++ {
				args = append(args, mock.Anything)
			}
			api.On(level, args...).Return()
		}
	}

	// Resolving a birthday to an account. "ghost" is in the list but has no
	// Mattermost account, which is the ordinary case for the children in it.
	api.On("GetUserByUsername", mock.Anything).Return(
		func(username string) *model.User {
			if username == "ghost" {
				return nil
			}

			return &model.User{Id: username, Username: username}
		},
		func(username string) *model.AppError {
			if username == "ghost" {
				// 404 is Mattermost's answer for a name nobody has, which is a
				// settled answer rather than a failure to retry.
				return &model.AppError{Message: "no such user", StatusCode: http.StatusNotFound}
			}

			return nil
		},
	)

	p := &Plugin{offlineStreak: map[string]int{}}
	p.SetAPI(api)

	return p, store
}

// cakeConfig badges whoever is named, on badgeClock's date.
func cakeConfig(usernames ...string) *configuration {
	entries := make([]string, 0, len(usernames))
	for _, username := range usernames {
		entries = append(entries,
			`{"username":"`+username+`","name":"`+username+`","birthday":"03-11","birth_year":1990}`)
	}

	return &configuration{
		EnableLiveBadge:    true,
		LiveEmoji:          "red_circle",
		LiveStatusText:     "Live now",
		OfflineGraceChecks: 2,
		EnableBirthdays:    true,
		BirthdayBadge:      true,
		BirthdayZone:       "UTC",
		Birthdays:          "[" + strings.Join(entries, ",") + "]",
	}
}

func (s *statusStore) emoji(userID string) string {
	s.lock.Lock()
	defer s.lock.Unlock()

	if status := s.statuses[userID]; status != nil {
		return status.Emoji
	}

	return ""
}

func (s *statusStore) writes(userID string) int {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.sets[userID]
}

func (s *statusStore) set(userID string, status *model.CustomStatus) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.statuses[userID] = status
}

func badgeConfig() *configuration {
	cfg := &configuration{
		EnableLiveBadge:    true,
		OfflineGraceChecks: 2,
	}
	cfg.normalize()

	return cfg
}

func liveStream(userID string) apiStream {
	return apiStream{StreamKey: "videokey-" + userID, UserID: userID}
}

// The whole point: going live shows beside the name, stopping takes it away.
func TestGoingLiveTakesTheSlotAndGivesItBack(t *testing.T) {
	p, store := badgePlugin(t)
	cfg := badgeConfig()

	p.reconcileBadges([]apiStream{liveStream("villa")}, cfg, badgeClock, true)
	require.Equal(t, "red_circle", store.emoji("villa"))
	require.Empty(t, store.emoji("dave"))

	// The grace has to run out before anything moves back, so a reconnect does
	// not make somebody's status flicker.
	p.reconcileBadges(nil, cfg, badgeClock, true)
	require.Equal(t, "red_circle", store.emoji("villa"), "one missed poll is not the end of a stream")

	p.reconcileBadges(nil, cfg, badgeClock, true)
	require.Empty(t, store.emoji("villa"))
}

// Whatever somebody was wearing before is what they get back.
func TestTheirOwnStatusComesBack(t *testing.T) {
	p, store := badgePlugin(t)
	cfg := badgeConfig()

	store.set("villa", &model.CustomStatus{Emoji: "palm_tree", Text: "On holiday"})

	p.reconcileBadges([]apiStream{liveStream("villa")}, cfg, badgeClock, true)
	require.Equal(t, "red_circle", store.emoji("villa"))

	p.reconcileBadges(nil, cfg, badgeClock, true)
	p.reconcileBadges(nil, cfg, badgeClock, true)
	require.Equal(t, "palm_tree", store.emoji("villa"), "and it is theirs again, exactly as it was")
}

// Somebody who sets their own status while the plugin holds the slot has taken
// it back, and the plugin must not fight them for it.
func TestChangingYourOwnStatusTakesTheSlotBack(t *testing.T) {
	p, store := badgePlugin(t)
	cfg := badgeConfig()

	p.reconcileBadges([]apiStream{liveStream("villa")}, cfg, badgeClock, true)
	require.Equal(t, "red_circle", store.emoji("villa"))

	store.set("villa", &model.CustomStatus{Emoji: "coffee", Text: "brb"})

	for i := 0; i < 5; i++ {
		p.reconcileBadges([]apiStream{liveStream("villa")}, cfg, badgeClock, true)
	}
	require.Equal(t, "coffee", store.emoji("villa"), "the plugin stops touching a slot somebody reclaimed")

	p.reconcileBadges(nil, cfg, badgeClock, true)
	p.reconcileBadges(nil, cfg, badgeClock, true)
	require.Equal(t, "coffee", store.emoji("villa"), "and it does not take it away afterwards either")
}

// Changing the emoji in the settings has to reach somebody who is live now,
// and must not be mistaken for them reclaiming the slot.
func TestChangingTheBadgeEmojiUpdatesSomebodyAlreadyLive(t *testing.T) {
	p, store := badgePlugin(t)
	cfg := badgeConfig()

	store.set("villa", &model.CustomStatus{Emoji: "palm_tree", Text: "On holiday"})
	p.reconcileBadges([]apiStream{liveStream("villa")}, cfg, badgeClock, true)
	require.Equal(t, "red_circle", store.emoji("villa"))

	changed := badgeConfig()
	changed.LiveEmoji = "tv"
	p.reconcileBadges([]apiStream{liveStream("villa")}, changed, badgeClock, true)
	require.Equal(t, "tv", store.emoji("villa"))

	p.reconcileBadges(nil, changed, badgeClock, true)
	p.reconcileBadges(nil, changed, badgeClock, true)
	require.Equal(t, "palm_tree", store.emoji("villa"), "and the original status is still what comes back")
}

// Wearing the badge costs one status write, not one per poll.
func TestAStandingBadgeIsNotRewrittenEveryPoll(t *testing.T) {
	p, store := badgePlugin(t)
	cfg := badgeConfig()

	for i := 0; i < 6; i++ {
		p.reconcileBadges([]apiStream{liveStream("villa")}, cfg, badgeClock, true)
	}

	require.Equal(t, "red_circle", store.emoji("villa"))
	require.Equal(t, 1, store.writes("villa"), "set once, then left alone")
}

// Switching the badge off has to hand every slot back.
func TestTurningTheBadgeOffReleasesEverySlot(t *testing.T) {
	p, store := badgePlugin(t)
	cfg := badgeConfig()

	store.set("dave", &model.CustomStatus{Emoji: "coffee", Text: "brb"})

	p.reconcileBadges([]apiStream{liveStream("villa")}, cfg, badgeClock, true)
	require.Equal(t, "red_circle", store.emoji("villa"))

	off := badgeConfig()
	off.EnableLiveBadge = false
	p.reconcileBadges([]apiStream{liveStream("villa")}, off, badgeClock, true)

	require.Empty(t, store.emoji("villa"))
	require.Equal(t, "coffee", store.emoji("dave"), "somebody the plugin never touched is untouched")
}

// Records written before this version are a bare custom status; records written
// by 1.16 wrapped it beside a "kind". Both still restore what they remembered,
// so an upgrade never strands somebody wearing a red dot.
func TestOlderRecordsStillRestoreWhatTheyRemembered(t *testing.T) {
	for name, document := range map[string]string{
		"pre-1.16": `{"emoji":"palm_tree","text":"On holiday"}`,
		"1.16":     `{"kind":"live","set":"red_circle","prev":{"emoji":"palm_tree","text":"On holiday"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			p, store := badgePlugin(t)

			store.kv[kvPrevStatusPrefix+"villa"] = []byte(document)
			store.set("villa", &model.CustomStatus{Emoji: "red_circle", Text: "Live now"})

			record, holding := p.readLive("villa")
			require.True(t, holding)
			require.NotNil(t, record.Previous)
			require.Equal(t, "palm_tree", record.Previous.Emoji)

			p.clearAllBadges()
			require.Equal(t, "palm_tree", store.emoji("villa"))
			require.NotContains(t, store.kv, kvPrevStatusPrefix+"villa")
		})
	}
}

// An always-on channel is furniture, not a person, and never moves a status.
func TestAlwaysOnAndPausedStreamsAreNotBadged(t *testing.T) {
	p, store := badgePlugin(t)
	cfg := badgeConfig()

	always := liveStream("watts")
	always.AlwaysOn = true
	paused := liveStream("dave")
	paused.Paused = true

	p.reconcileBadges([]apiStream{always, paused}, cfg, badgeClock, true)

	require.Empty(t, store.emoji("watts"))
	require.Empty(t, store.emoji("dave"))
}

// The cake and the red dot share one slot, so the rule about which wins has to
// live in one place. Two writers would each read the other's emoji as the
// person taking their own status back.

func TestTheCakeBeatsTheRedDot(t *testing.T) {
	p, store := badgePlugin(t)
	cfg := cakeConfig("villa")

	// Live AND celebrating: one badge, and it is the cake.
	p.reconcileBadges([]apiStream{liveStream("villa")}, cfg, badgeClock, true)
	require.Equal(t, birthdayEmoji, store.emoji("villa"))

	// Poll after poll after poll. This is the flapping test: two independent
	// writers would trade the slot back and forth every fifteen seconds.
	for i := 0; i < 5; i++ {
		p.reconcileBadges([]apiStream{liveStream("villa")}, cfg, badgeClock, true)
	}
	require.Equal(t, birthdayEmoji, store.emoji("villa"))
	require.Equal(t, 1, store.writes("villa"), "the status is written once, not once per poll")
}

func TestTheRedDotComesBackWhenTheBirthdayEnds(t *testing.T) {
	p, store := badgePlugin(t)
	cfg := cakeConfig("villa")

	p.reconcileBadges([]apiStream{liveStream("villa")}, cfg, badgeClock, true)
	require.Equal(t, birthdayEmoji, store.emoji("villa"))

	// Midnight has passed and they are still streaming.
	tomorrow := badgeClock.AddDate(0, 0, 1)
	p.reconcileBadges([]apiStream{liveStream("villa")}, cfg, tomorrow, true)
	require.Equal(t, "red_circle", store.emoji("villa"), "still live, so back to the live badge")
}

func TestACakeIsTakenOffWhenTheDayEnds(t *testing.T) {
	p, store := badgePlugin(t)
	cfg := cakeConfig("villa")

	p.reconcileBadges(nil, cfg, badgeClock, true)
	require.Equal(t, birthdayEmoji, store.emoji("villa"), "not streaming, still a birthday")

	// The grace has to run out, exactly as it does for a stream that stopped.
	tomorrow := badgeClock.AddDate(0, 0, 1)
	p.reconcileBadges(nil, cfg, tomorrow, true)
	require.Equal(t, birthdayEmoji, store.emoji("villa"))

	p.reconcileBadges(nil, cfg, tomorrow, true)
	require.Equal(t, "", store.emoji("villa"))
}

// Somebody whose own status was there first gets it back, whichever badge was
// borrowed over the top of it.
func TestACakeHandsBackTheStatusItBorrowed(t *testing.T) {
	p, store := badgePlugin(t)
	cfg := cakeConfig("villa")

	store.set("villa", &model.CustomStatus{Emoji: "palm_tree", Text: "On holiday"})

	p.reconcileBadges([]apiStream{liveStream("villa")}, cfg, badgeClock, true)
	require.Equal(t, birthdayEmoji, store.emoji("villa"))

	// The stream stops and the day ends together.
	tomorrow := badgeClock.AddDate(0, 0, 1)
	p.reconcileBadges(nil, cfg, tomorrow, true)
	p.reconcileBadges(nil, cfg, tomorrow, true)

	require.Equal(t, "palm_tree", store.emoji("villa"), "their own status, not the one under the cake")
}

// A birthday badge must not depend on the live badge being on: they are
// separate features that happen to share the one slot Mattermost offers.
func TestTheCakeWorksWithTheLiveBadgeSwitchedOff(t *testing.T) {
	p, store := badgePlugin(t)
	cfg := cakeConfig("villa")
	cfg.EnableLiveBadge = false

	p.reconcileBadges([]apiStream{liveStream("dave")}, cfg, badgeClock, true)

	require.Equal(t, birthdayEmoji, store.emoji("villa"))
	require.Equal(t, "", store.emoji("dave"), "the live badge is off, so a live streamer gets nothing")
}

func TestNobodyIsBadgedWhenTheCakeIsSwitchedOff(t *testing.T) {
	p, store := badgePlugin(t)
	cfg := cakeConfig("villa")
	cfg.BirthdayBadge = false

	p.reconcileBadges([]apiStream{liveStream("villa")}, cfg, badgeClock, true)
	require.Equal(t, "red_circle", store.emoji("villa"), "live, and it is not the plugin's business that it is their birthday")
}

// The children in the list have no account. There is no status to put a cake
// on, and their parents are not the ones having a birthday.
func TestSomebodyWithNoAccountIsNotBadged(t *testing.T) {
	p, store := badgePlugin(t)
	cfg := cakeConfig("ghost")

	p.reconcileBadges(nil, cfg, badgeClock, true)
	require.Equal(t, "", store.emoji("ghost"))
}

// Resolving a username costs a lookup, and the poller runs every fifteen
// seconds. Asking Mattermost five and a half thousand times who "villa" is,
// for one birthday, is not a thing to do.
func TestBirthdaysAreResolvedOncePerDayNotOncePerPoll(t *testing.T) {
	p, _ := badgePlugin(t)
	cfg := cakeConfig("villa")

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	for i := 0; i < 20; i++ {
		p.reconcileBadges(nil, cfg, badgeClock, true)
	}
	api.AssertNumberOfCalls(t, "GetUserByUsername", 1)

	// Editing the list takes effect without waiting for tomorrow.
	cfg.Birthdays = strings.Replace(cfg.Birthdays, "villa", "dave", -1)
	p.reconcileBadges(nil, cfg, badgeClock, true)
	api.AssertNumberOfCalls(t, "GetUserByUsername", 2)

	// The day turning drops the cache too. Next year, same date, so there is
	// somebody to look up again rather than an empty answer that would prove
	// nothing about whether the cache was dropped.
	p.reconcileBadges(nil, cfg, badgeClock.AddDate(1, 0, 0), true)
	api.AssertNumberOfCalls(t, "GetUserByUsername", 3)

	// Tomorrow is nobody's birthday, so it costs no lookup at all.
	p.reconcileBadges(nil, cfg, badgeClock.AddDate(1, 0, 1), true)
	api.AssertNumberOfCalls(t, "GetUserByUsername", 3)
}

// A name nobody has is a settled answer, worth remembering for the day. A
// database that was briefly unhappy is not: caching that would cost somebody
// their cake until tomorrow because of one bad moment.
func TestATransientLookupFailureIsNotCachedForTheDay(t *testing.T) {
	store := &statusStore{
		statuses: map[string]*model.CustomStatus{},
		kv:       map[string][]byte{},
		sets:     map[string]int{},
	}
	_ = store

	api := &plugintest.API{}
	failures := 0
	api.On("GetUserByUsername", "villa").Return(
		func(_ string) *model.User {
			failures++
			if failures <= 2 {
				return nil
			}

			return &model.User{Id: "uid-villa", Username: "villa"}
		},
		func(_ string) *model.AppError {
			if failures <= 2 {
				return &model.AppError{Message: "database is down", StatusCode: http.StatusInternalServerError}
			}

			return nil
		},
	)
	for _, level := range []string{"LogWarn", "LogError", "LogInfo", "LogDebug"} {
		for pairs := 0; pairs <= 8; pairs++ {
			args := make([]any, 0, 1+(pairs*2))
			for i := 0; i < 1+(pairs*2); i++ {
				args = append(args, mock.Anything)
			}
			api.On(level, args...).Return()
		}
	}

	p := &Plugin{offlineStreak: map[string]int{}}
	p.SetAPI(api)

	cfg := cakeConfig("villa")

	require.Empty(t, p.celebratingToday(cfg, badgeClock), "the lookup failed, so nobody is celebrating yet")
	require.Empty(t, p.celebratingToday(cfg, badgeClock), "and it is tried again rather than remembered")

	third := p.celebratingToday(cfg, badgeClock)
	require.True(t, third["uid-villa"], "once the database answers, the cake is on")
	require.Equal(t, 3, failures)

	// And now it IS remembered: a settled answer costs one lookup a day.
	p.celebratingToday(cfg, badgeClock)
	require.Equal(t, 3, failures)
}

// The other half: a username nobody has is settled, and must not be asked for
// five and a half thousand times a day.
func TestAMissingAccountIsRememberedForTheDay(t *testing.T) {
	p, _ := badgePlugin(t)
	cfg := cakeConfig("ghost")

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	for i := 0; i < 5; i++ {
		require.Empty(t, p.celebratingToday(cfg, badgeClock))
	}
	api.AssertNumberOfCalls(t, "GetUserByUsername", 1)
}

// A birthday has nothing to do with whether the television is reachable. When
// the origin goes down, the poll used to return before reconcileBadges — so a
// cake that went on at midnight stayed on until somebody fixed an unrelated
// setting, with its owner's real status buried underneath it.
func TestAFailedHandBackIsRetriedRatherThanForgotten(t *testing.T) {
	p, store := badgePlugin(t)
	cfg := cakeConfig("villa")

	store.set("villa", &model.CustomStatus{Emoji: "palm_tree", Text: "On holiday"})

	p.reconcileBadges(nil, cfg, badgeClock, true)
	require.Equal(t, birthdayEmoji, store.emoji("villa"))

	// The day ends, and the hand-back fails on the one poll that attempts it.
	store.failNextUpdate()

	tomorrow := badgeClock.AddDate(0, 0, 1)
	p.reconcileBadges(nil, cfg, tomorrow, true)
	p.reconcileBadges(nil, cfg, tomorrow, true)
	require.Equal(t, birthdayEmoji, store.emoji("villa"), "the restore failed, so the cake is still on")

	// The record must survive, or nothing will ever look at this person again.
	_, holding := p.readLive("villa")
	require.True(t, holding, "the plugin still remembers it borrowed the slot")

	// The grace window runs again before the next attempt, which is the same
	// patience every other removal here gets. Half a minute to recover from a
	// failed write, rather than never.
	p.reconcileBadges(nil, cfg, tomorrow, true)
	p.reconcileBadges(nil, cfg, tomorrow, true)
	require.Equal(t, "palm_tree", store.emoji("villa"), "and it is handed back on the retry")

	_, holding = p.readLive("villa")
	require.False(t, holding, "only then is the record forgotten")
}
