package main

import (
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// A greeting that arrives twice is worse than one that arrives late, and there
// are two ways for it to double: a poll every fifteen seconds, and Mattermost
// running this plugin on every node of a cluster.

// The list an admin pastes in is the file the cron script read, unchanged.
const theirList = `[
  {"username": "itzdave", "name": "Dave Jones", "birthday": "04-12", "birth_year": 1983},
  {"username": "alice", "name": "Alice Nguyen", "birthday": "03-14", "birth_year": 1990},
  {"name": "Ivan Fisher", "birthday": "09-03", "birth_year": 2017,
   "parent_username": ["trent", "mamacarol"]}
]`

func birthdayConfig() *configuration {
	cfg := &configuration{
		EnableBirthdays: true,
		BirthdayChannel: "myteam/town-square",
		BirthdayHour:    9,
		// Pinned: blank means the machine's own zone, and these times are UTC.
		BirthdayZone:    "UTC",
		Birthdays:       theirList,
		BirthdayMessage: "{full_name} (@{username}) turns **{age}** today! Wish {first_name} well.",
		BirthdayKidMessage: "{full_name} ({parents}'s kiddo) turns **{age}** today! " +
			"Wish {first_name} well.",
	}
	cfg.normalize()

	return cfg
}

func birthdayPlugin(t *testing.T) (*Plugin, map[string][]byte, *[]*model.Post) {
	t.Helper()

	p, store, _ := kvBackedPluginWithFaults(map[string][]byte{})
	posted := &[]*model.Post{}

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("GetChannelByNameForTeamName", "myteam", "town-square", false).Return(
		&model.Channel{Id: "chan-town"}, nil)
	// Not a member of anything to begin with, which is the case that used to
	// leave the bot outside the channel and its @all inert.
	api.On("GetTeamMember", mock.Anything, mock.Anything).Return(
		nil, &model.AppError{Message: "not a team member"})
	api.On("CreateTeamMember", mock.Anything, mock.Anything).Return(&model.TeamMember{}, nil)
	api.On("GetChannelMember", mock.Anything, mock.Anything).Return(
		nil, &model.AppError{Message: "not a channel member"})
	api.On("AddChannelMember", mock.Anything, mock.Anything).Return(&model.ChannelMember{}, nil)
	api.On("CreatePost", mock.Anything).Return(
		func(post *model.Post) *model.Post {
			*posted = append(*posted, post)

			return post
		},
		func(_ *model.Post) *model.AppError { return nil },
	)

	p.botUserID = "uid-bot"

	return p, store, posted
}

// Their file, read without being rewritten.
func TestTheExistingBirthdayFileIsUnderstood(t *testing.T) {
	entries, err := parseBirthdays(theirList)
	require.NoError(t, err)
	require.Len(t, entries, 3)

	require.Equal(t, "Dave Jones", entries[0].Name)
	require.Equal(t, "itzdave", entries[0].Username)
	require.Equal(t, "Dave", entries[0].firstName())

	// A child with two parents, given as a list.
	require.Empty(t, entries[2].Username)
	require.Equal(t, []string{"trent", "mamacarol"}, entries[2].parents())
}

// The script accepted a single parent as a bare string as well as a list.
func TestOneParentMayBeAStringOrAList(t *testing.T) {
	entries, err := parseBirthdays(
		`[{"name": "A Child", "birthday": "01-02", "birth_year": 2020, "parent_username": "trent"}]`)
	require.NoError(t, err)
	require.Equal(t, []string{"trent"}, entries[0].parents())
}

// A list that is wrong says so, rather than looking like a list with no
// birthdays in it today.
func TestABadListIsReportedRatherThanIgnored(t *testing.T) {
	for name, source := range map[string]string{
		"not json":      `[{"name": "Alice",}]`,
		"no name":       `[{"username": "alice", "birthday": "03-14", "birth_year": 1990}]`,
		"no date":       `[{"name": "Alice", "username": "alice", "birth_year": 1990}]`,
		"a made-up day": `[{"name": "Alice", "username": "alice", "birthday": "02-30", "birth_year": 1990}]`,
		"month 13":      `[{"name": "Alice", "username": "alice", "birthday": "13-01", "birth_year": 1990}]`,
		"nobody to tag": `[{"name": "A Child", "birthday": "01-02", "birth_year": 2020}]`,
	} {
		_, err := parseBirthdays(source)
		require.Error(t, err, name)
	}

	// And 29 February is a real birthday.
	_, err := parseBirthdays(`[{"name": "Leap", "username": "leap", "birthday": "02-29", "birth_year": 1996}]`)
	require.NoError(t, err)

	entries, err := parseBirthdays("   ")
	require.NoError(t, err, "an empty list is not an error")
	require.Empty(t, entries)
}

func TestTheRightGreetingIsChosen(t *testing.T) {
	cfg := birthdayConfig()
	entries, err := parseBirthdays(theirList)
	require.NoError(t, err)

	grown := birthdayMessage(cfg, entries[1], 2026)
	require.Equal(t, "Alice Nguyen (@alice) turns **36** today! Wish Alice well.", grown)

	child := birthdayMessage(cfg, entries[2], 2026)
	require.Equal(t, "Ivan Fisher (@trent & @mamacarol's kiddo) turns **9** today! Wish Ivan well.", child)
}

// Once a day, whatever happens.
func TestABirthdayIsPostedExactlyOnce(t *testing.T) {
	p, store, posted := birthdayPlugin(t)
	cfg := birthdayConfig()
	p.setConfiguration(cfg)

	// Alice's birthday, at ten in the morning.
	noon := time.Date(2026, 3, 14, 10, 0, 0, 0, time.UTC)

	// The poller asks over and over.
	for i := 0; i < 200; i++ {
		p.checkBirthdays(cfg, noon.Add(time.Duration(i)*15*time.Second))
	}

	require.Len(t, *posted, 1)
	require.Equal(t, "chan-town", (*posted)[0].ChannelId)
	require.Equal(t, "uid-bot", (*posted)[0].UserId)
	require.Contains(t, (*posted)[0].Message, "Alice Nguyen")
	require.Equal(t, "2026-03-14", string(store[kvBirthdayPosted]))
}

// A second node, with its own memory, must not greet anybody a second time.
func TestASecondNodeDoesNotGreetAgain(t *testing.T) {
	p, store, posted := birthdayPlugin(t)
	cfg := birthdayConfig()
	p.setConfiguration(cfg)

	morning := time.Date(2026, 3, 14, 9, 30, 0, 0, time.UTC)
	p.checkBirthdays(cfg, morning)
	require.Len(t, *posted, 1)

	// A different node: the same database, none of the memory.
	second, secondStore, alsoPosted := birthdayPlugin(t)
	for key, value := range store {
		secondStore[key] = value
	}
	second.setConfiguration(cfg)
	second.checkBirthdays(cfg, morning)

	require.Empty(t, *alsoPosted, "the date was already claimed")
}

// Nothing goes out before the configured hour, and nothing goes out on a day
// that is nobody's birthday.
func TestNothingIsPostedEarlyOrOnAQuietDay(t *testing.T) {
	p, _, posted := birthdayPlugin(t)
	cfg := birthdayConfig()
	p.setConfiguration(cfg)

	p.checkBirthdays(cfg, time.Date(2026, 3, 14, 8, 59, 0, 0, time.UTC))
	require.Empty(t, *posted, "before the hour")

	p.checkBirthdays(cfg, time.Date(2026, 3, 15, 11, 0, 0, 0, time.UTC))
	require.Empty(t, *posted, "nobody's birthday")

	off := birthdayConfig()
	off.EnableBirthdays = false
	p.checkBirthdays(off, time.Date(2026, 3, 14, 11, 0, 0, 0, time.UTC))
	require.Empty(t, *posted, "switched off")
}

// Which day it is depends on where you are standing.
func TestTodayIsMeasuredInTheConfiguredZone(t *testing.T) {
	p, _, posted := birthdayPlugin(t)

	cfg := birthdayConfig()
	cfg.BirthdayZone = "Pacific/Auckland"
	p.setConfiguration(cfg)

	// 13 March, 21:00 UTC is already 14 March in Auckland, and past nine o'clock.
	p.checkBirthdays(cfg, time.Date(2026, 3, 13, 21, 0, 0, 0, time.UTC))
	require.Len(t, *posted, 1, "it is already their birthday there")

	// An unknown zone falls back rather than refusing to work at all.
	odd := birthdayConfig()
	odd.BirthdayZone = "Middle/Earth"
	require.NotNil(t, odd.birthdayZone())
}

func TestTheHourIsBounded(t *testing.T) {
	cfg := birthdayConfig()

	cfg.BirthdayHour = 0
	require.Equal(t, 0, cfg.birthdayHour(), "midnight is a choice")

	cfg.BirthdayHour = 23
	require.Equal(t, 23, cfg.birthdayHour())

	cfg.BirthdayHour = -1
	require.Equal(t, defaultBirthdayHour, cfg.birthdayHour())

	cfg.BirthdayHour = 99
	require.Equal(t, defaultBirthdayHour, cfg.birthdayHour())
}
