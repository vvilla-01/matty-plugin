package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Achievements (design/3.2.0.md §5): worked out at every refresh from what
// VillaNET already keeps, backdated, going down when the figures do, and only
// ever for people linked now.

// achievementWorld is a plugin over a real in-memory store with these people,
// each "uid-x" linked to "videokey-x" unless they are in unlinked, and
// achievements on with every switch that feeds them.
func achievementWorld(t *testing.T, people []string, unlinked ...string) (*Plugin, map[string][]byte, *kvFaults) {
	t.Helper()

	p, store, faults := kvBackedPluginWithFaults(map[string][]byte{})
	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	skip := map[string]bool{}
	for _, userID := range unlinked {
		skip[userID] = true
	}

	users := map[string]*model.User{}
	for _, userID := range people {
		name := strings.TrimPrefix(userID, "uid-")
		users[userID] = &model.User{Id: userID, Username: name, Nickname: strings.ToUpper(name[:1]) + name[1:]}
		if !skip[userID] {
			linkForTest(store, userID, "videokey-"+name)
		}
	}

	api.On("GetUser", mock.Anything).Return(func(userID string) (*model.User, *model.AppError) {
		if user := users[userID]; user != nil {
			copied := *user
			return &copied, nil
		}

		return nil, model.NewAppError("GetUser", "app.user.missing", nil, "", http.StatusNotFound)
	})
	api.On("GetUserByUsername", mock.Anything).Return(nil, model.NewAppError("GetUserByUsername", "app.user.missing", nil, "", http.StatusNotFound)).Maybe()

	cfg := &configuration{
		EnableAchievements: true,
		EnableLeaderboard:  true,
		TopWatchers:        true,
		ShowWatchers:       true,
		EnableClips:        true,
		AlwaysOnStreamKeys: "videokey-watts, videokey-rain",
	}
	cfg.normalize()
	p.setConfiguration(cfg)

	return p, store, faults
}

func seedTable(t *testing.T, store map[string][]byte, key string, table statTable) {
	t.Helper()

	encoded, err := json.Marshal(table)
	require.NoError(t, err)
	store[key] = encoded
}

// refreshAchievements is a poll's part in achievements: the boards, then the
// figures, then the snapshot's fields.
func refreshAchievements(p *Plugin, now time.Time) *snapshot {
	cfg := p.getConfiguration()
	current := &snapshot{}
	people := p.newPollPeople()

	if cfg.EnableLeaderboard {
		p.addBoardsWith(current, cfg, now, people.resolve)
	}
	if cfg.TopWatchers {
		p.addWatchBoardsFor(current, cfg, now, people)
	}
	p.refreshAchievementFigures(current, cfg, now, people)
	p.addAchievements(current, cfg)

	return current
}

func tiersOf(current *snapshot) map[string]map[string]int {
	tiers := map[string]map[string]int{}
	for _, holder := range current.Achievements {
		tiers[holder.UserID] = map[string]int{}
		for _, step := range holder.Have {
			tiers[holder.UserID][step.ID] = step.Tier
		}
	}

	return tiers
}

const hour = int64(3600)

func TestStreamedMatchesBoardRows(t *testing.T) {
	s := newTwitchScene(t)
	cfg := s.p.getConfiguration().Clone()
	cfg.EnableAchievements = true
	s.p.setConfiguration(cfg)

	s.connected(t, map[string]twitchUser{twDave: daveTwitch})
	s.seedTable(t, kvStatsTotals, statTable{
		"videokey-dave": {Seconds: 30 * hour, Title: "Dave"},
		"videokey-bob":  {Seconds: 12 * hour, Title: "Bob"},
		"videokey-dan":  {Seconds: 99 * hour, Title: "Dan"},
	})
	s.seedTable(t, kvTwitchAirTotals, statTable{"1001": {Seconds: 25 * hour}})

	on := refreshAchievements(s.p, s.now())
	board := map[string]int64{}
	for _, row := range on.AllTime {
		if row.UserID != "" {
			board[row.UserID] = row.Seconds
		}
	}
	require.EqualValues(t, 55*hour, board[twDave], "the board counts a simulcaster's two sources once each, as one person")
	require.Equal(t, map[string]int{achStreamed: 2}, tiersOf(on)[twDave], "and so does Streamed: 55 hours")
	require.Equal(t, map[string]int{achStreamed: 1}, tiersOf(on)[twBob])
	for userID := range tiersOf(on) {
		require.NotEqual(t, twDan, userID)
	}
	encoded, err := json.Marshal(on.Achievements)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "videokey-dan", "an unlinked key is nobody, and its key goes nowhere")

	// Twitch live off takes Twitch hours off the boards, and off Streamed.
	off := cfg.Clone()
	off.EnableTwitchLive = false
	s.p.setConfiguration(off)
	require.Equal(t, map[string]int{achStreamed: 1}, tiersOf(refreshAchievements(s.p, s.now()))[twDave])
}

func TestAchievementsUncutByTheFiftyRowBoard(t *testing.T) {
	people := []string{}
	totals := statTable{}
	for i := range 60 {
		userID := fmt.Sprintf("uid-p%02d", i)
		people = append(people, userID)
		totals["videokey-"+strings.TrimPrefix(userID, "uid-")] = statEntry{Seconds: int64(100-i) * hour}
	}
	p, store, _ := achievementWorld(t, people)
	seedTable(t, store, kvStatsTotals, totals)

	current := refreshAchievements(p, time.Now())
	require.Len(t, current.AllTime, maxLeaderboardRows, "the board is cut to fifty")
	require.Equal(t, 1, tiersOf(current)["uid-p59"][achStreamed], "the sixtieth still has 41 hours streamed")
	require.Len(t, current.Achievements, 60)
}

func TestAchievementsOnlyForLinkedPeople(t *testing.T) {
	p, store, _ := achievementWorld(t, []string{"uid-bob", "uid-erin", "uid-alice"}, "uid-alice")
	seedTable(t, store, kvStatsTotals, statTable{"videokey-bob": {Seconds: 20 * hour}, "videokey-erin": {Seconds: 20 * hour}})
	seedTable(t, store, kvWatchTotals, statTable{"uid-bob": {Seconds: 20 * hour}, "uid-alice": {Seconds: 20 * hour}})

	// Erin's forward entry has gone, as a failed unlink would leave it: the
	// reverse entry alone is no link.
	delete(store, kvLinkByUserPrefix+"uid-erin")

	current := refreshAchievements(p, time.Now())
	require.Equal(t, map[string]map[string]int{"uid-bob": {achStreamed: 1, achSnooped: 1}}, tiersOf(current))
}

func TestSnoopedUsesTheHourlyCopyWithFacesOff(t *testing.T) {
	p, store, _ := achievementWorld(t, []string{"uid-bob"})
	cfg := p.getConfiguration().Clone()
	cfg.ShowWatchers = false
	p.setConfiguration(cfg)

	top := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)
	seedTable(t, store, kvWatchTotals, statTable{"uid-bob": {Seconds: 11 * hour}})
	require.Equal(t, 1, tiersOf(refreshAchievements(p, top.Add(time.Minute)))["uid-bob"][achSnooped])

	seedTable(t, store, kvWatchTotals, statTable{"uid-bob": {Seconds: 51 * hour}})
	require.Equal(t, 1, tiersOf(refreshAchievements(p, top.Add(59*time.Minute)))["uid-bob"][achSnooped],
		"with faces off the figures move once an hour")
	require.Equal(t, 2, tiersOf(refreshAchievements(p, top.Add(61*time.Minute)))["uid-bob"][achSnooped])
}

func TestPodiumCountsEndedMonths(t *testing.T) {
	p, store, _ := achievementWorld(t, []string{"uid-bob", "uid-dave", "uid-erin", "uid-caro"})
	now := time.Date(2026, 10, 12, 20, 0, 0, 0, time.UTC)

	for month, order := range map[string][]string{
		"2026-06": {"bob", "dave", "erin", "caro"},
		"2026-07": {"dave", "bob", "caro"},
		"2026-08": {"bob"},
		"2026-10": {"caro", "erin"}, // this month: not ended, not counted
	} {
		table := statTable{}
		for place, name := range order {
			table["videokey-"+name] = statEntry{Seconds: int64(1000 - place)}
		}
		seedTable(t, store, kvStatsMonthPrefix+month, table)
	}

	tiers := tiersOf(refreshAchievements(p, now))
	require.Equal(t, 2, tiers["uid-bob"][achPodium], "three months on a podium: past the first and third steps")
	require.Equal(t, 1, tiers["uid-dave"][achPodium])
	require.Equal(t, 1, tiers["uid-caro"][achPodium], "one month, July, counted; October has not ended")
	require.Equal(t, 1, tiers["uid-erin"][achPodium])

	var mine achievementsMineResponse
	recorder := httptest.NewRecorder()
	p.handleAchievementsMine(recorder, "uid-bob")
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &mine))
	require.Contains(t, mine.Progress, achievementProgress{ID: achPodium, Value: 3, Next: 6})
}

func TestThumbsIncludeBanked(t *testing.T) {
	w := newClipsWorld(t, "uid-bob", "uid-dave", "uid-dan", "uid-erin")
	cfg := w.p.getConfiguration().Clone()
	cfg.EnableAchievements = true
	w.p.setConfiguration(cfg)

	voters := []string{}
	for i := range 6 {
		voter := fmt.Sprintf("uid-voter%d", i)
		w.person(voter)
		w.link(voter, keyOf(voter))
		voters = append(voters, voter)
	}

	doc := &clipsDoc{V: clipsDocVersion, Clips: []clipEntry{
		testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), votesAt(clipsNow.Add(-time.Minute), voters...)),
	}, Banked: map[string]bankedScore{"uid-bob": {Up: 5, Clips: 2, LastUpAt: 1}}}
	w.seed(t, doc)
	w.recompute(t, clipsNow)

	require.EqualValues(t, 11, w.p.thumbsFigures()["uid-bob"], "six counted on a clip still here, five banked")

	current := &snapshot{}
	w.p.refreshAchievementFigures(current, cfg, clipsNow, nil)
	w.p.addAchievements(current, cfg)
	require.Equal(t, 1, tiersOf(current)["uid-bob"][achThumbs])
}

func TestFamiliesFollowTheirSwitches(t *testing.T) {
	p, store, _ := achievementWorld(t, []string{"uid-bob"})
	seedTable(t, store, kvStatsTotals, statTable{"videokey-bob": {Seconds: 20 * hour}})
	seedTable(t, store, kvWatchTotals, statTable{"uid-bob": {Seconds: 20 * hour}})
	cfg := p.getConfiguration().Clone()
	cfg.ChannelAchievements = "videokey-watts = Alan Watts = 1, 10"
	p.setConfiguration(cfg)

	ids := func() []string {
		var kinds []string
		for _, kind := range refreshAchievements(p, time.Now()).AchievementKinds {
			kinds = append(kinds, kind.ID)
		}

		return kinds
	}

	watts := p.channelAchievementID("videokey-watts")
	require.Equal(t, []string{achStreamed, achSnooped, achPodium, achThumbs, watts}, ids(), "the catalogue, in its order")

	for change, without := range map[string][]string{
		"leaderboard": {achSnooped, achThumbs, watts},
		"snoopers":    {achStreamed, achPodium, achThumbs},
		"clips":       {achStreamed, achSnooped, achPodium, watts},
	} {
		next := cfg.Clone()
		switch change {
		case "leaderboard":
			next.EnableLeaderboard = false
		case "snoopers":
			next.TopWatchers = false
		case "clips":
			next.EnableClips = false
		}
		p.setConfiguration(next)
		require.Equal(t, without, ids(), change)
	}

	p.setConfiguration(cfg)
	require.Equal(t, map[string]int{achStreamed: 1, achSnooped: 1}, tiersOf(refreshAchievements(p, time.Now()))["uid-bob"],
		"back on, nothing lost: nothing was stored")

	off := cfg.Clone()
	off.EnableAchievements = false
	p.setConfiguration(off)
	current := refreshAchievements(p, time.Now())
	require.Nil(t, current.AchievementKinds)
	require.Nil(t, current.Achievements)
}

func TestChannelAchievementSettingParses(t *testing.T) {
	p, _, _ := achievementWorld(t, nil)
	p.handleSecret = []byte("0123456789abcdef0123456789abcdef")
	alwaysOn := map[string]bool{"videokey-watts": true, "videokey-rain": true}

	families, faults := parseChannelAchievements(strings.Join([]string{
		"# the shipped line",
		"videokey-watts = Alan Watts = 1, 10, 50, 100, 250",
		"videokey-dave = Dave = 1, 10",
		"videokey-rain = Rain",
		"videokey-rain = " + strings.Repeat("R", 33) + " = 1",
		"videokey-rain = Rain = 10, 5",
		"videokey-rain = Rain = 0, 5",
		"videokey-rain = Rain = 1, 2, 3, 4, 5, 6, 7, 8, 9",
		"videokey-rain = Rain = 100001",
		"",
		"VIDEOKEY-RAIN = Rain = 2, 4",
		"videokey-watts = Again = 1",
	}, "\n"), alwaysOn, p.channelAchievementID)

	require.Equal(t, []string{
		"24/7 channel achievements, line 3: that key is not one of the 24/7 stream keys.",
		"24/7 channel achievements, line 4: needs a key, a name and the hours.",
		"24/7 channel achievements, line 5: the name is too long (32 at most).",
		"24/7 channel achievements, line 6: the hours must go up, from 1 to 100000, at most 8 of them.",
		"24/7 channel achievements, line 7: the hours must go up, from 1 to 100000, at most 8 of them.",
		"24/7 channel achievements, line 8: the hours must go up, from 1 to 100000, at most 8 of them.",
		"24/7 channel achievements, line 9: the hours must go up, from 1 to 100000, at most 8 of them.",
		"24/7 channel achievements, line 11: only 8 lines are read.",
		"24/7 channel achievements, line 12: only 8 lines are read.",
	}, faults)

	require.Len(t, families, 1)
	require.Equal(t, "videokey-watts", families[0].key)
	require.Equal(t, "Alan Watts", families[0].name)
	require.Equal(t, []int64{1, 10, 50, 100, 250}, families[0].tiers)

	id := families[0].id
	require.Regexp(t, `^ch-[0-9a-f]{12}$`, id)
	require.Equal(t, id, p.channelAchievementID("videokey-watts"), "stable")
	require.NotContains(t, p.streamHandle("videokey-watts"), strings.TrimPrefix(id, "ch-"), "never the stream's handle")
	require.NotContains(t, id, "watts", "and nothing of the key")

	// Two good lines, and a key written in another case is the same key.
	families, faults = parseChannelAchievements("videokey-watts = Alan Watts = 1\nVIDEOKEY-RAIN = Rain = 2, 4", alwaysOn, p.channelAchievementID)
	require.Empty(t, faults)
	require.Len(t, families, 2)
	require.Equal(t, "videokey-rain", families[1].key)

	// The alert says it to admins, while achievements are on.
	cfg := p.getConfiguration().Clone()
	cfg.ChannelAchievements = "videokey-dave = Dave = 1"
	require.Equal(t, "24/7 channel achievements, line 1: that key is not one of the 24/7 stream keys.", p.achievementsAlertFor(cfg))
	cfg.EnableAchievements = false
	require.Empty(t, p.achievementsAlertFor(cfg))
}

func TestChannelHoursCreditOnlyNamedChannels(t *testing.T) {
	p, store, _ := fansPlugin(t)
	cfg := p.getConfiguration().Clone()
	cfg.EnableAchievements = true
	cfg.AlwaysOnStreamKeys = "videokey-watts, videokey-rain"
	cfg.ChannelAchievements = "videokey-watts = Alan Watts = 1, 10"
	p.setConfiguration(cfg)

	start := watchEpoch()
	watts, rain := p.streamHandle("videokey-watts"), p.streamHandle("videokey-rain")
	raw := []bbStream{rawWatched("videokey-watts"), rawWatched("videokey-rain")}
	watchPass(p, raw, start)

	p.watchers().record("bob-watts", "uid-bob", watts, start)
	p.watchers().record("bob-rain", "uid-bob", rain, start)
	p.watchers().record("alice-watts", "uid-alice", watts, start)
	raw = []bbStream{rawWatched("videokey-watts", "bob-watts", "alice-watts"), rawWatched("videokey-rain", "bob-rain")}
	watchPass(p, raw, start.Add(45*time.Second))

	table := mustLoad(t, p, kvChanwatchTotals)
	require.Equal(t, statTable{chanwatchKey("videokey-watts", "uid-bob"): {Seconds: 45}}, table,
		"only the named channel, only the linked viewer")
	require.Zero(t, watched(p, kvWatchTotals, "uid-bob"), "24/7 still counts toward no Snooper")
	_, fans := store[kvFansTotals]
	require.False(t, fans, "and toward no fan")

	// Off, nothing at all.
	off := cfg.Clone()
	off.EnableAchievements = false
	p.setConfiguration(off)
	watchPass(p, raw, start.Add(90*time.Second))
	require.EqualValues(t, 45, mustLoad(t, p, kvChanwatchTotals)[chanwatchKey("videokey-watts", "uid-bob")].Seconds)
}

func TestChannelHoursSeenWithFacesOnAndOff(t *testing.T) {
	p, _, _ := fansPlugin(t)
	cfg := p.getConfiguration().Clone()
	cfg.EnableAchievements = true
	cfg.ChannelAchievements = "videokey-watts = Alan Watts = 1, 10"
	p.setConfiguration(cfg)

	start := watchEpoch()
	watts := p.streamHandle("videokey-watts")
	watchPass(p, []bbStream{rawWatched("videokey-watts")}, start)
	p.watchers().record("bob-1", "uid-bob", watts, start)
	watchPass(p, []bbStream{rawWatched("videokey-watts", "bob-1")}, start.Add(30*time.Second))
	require.EqualValues(t, 30, mustLoad(t, p, kvChanwatchTotals)[chanwatchKey("videokey-watts", "uid-bob")].Seconds, "faces on")

	off := cfg.Clone()
	off.ShowWatchers = false
	p.setConfiguration(off)
	p.watchers().record("erin-1", "uid-erin", watts, start)
	watchPass(p, []bbStream{rawWatched("videokey-watts", "bob-1", "erin-1")}, start.Add(60*time.Second))

	table := mustLoad(t, p, kvChanwatchTotals)
	require.EqualValues(t, 60, table[chanwatchKey("videokey-watts", "uid-bob")].Seconds, "faces off, as Top Snoopers sees it")
	require.EqualValues(t, 30, table[chanwatchKey("videokey-watts", "uid-erin")].Seconds)

	// And shown hourly while faces are off.
	top := start.Truncate(time.Hour)
	shown, ok := p.channelHoursShown(off, top.Add(time.Minute))
	require.True(t, ok)
	before := shown[chanwatchKey("videokey-watts", "uid-bob")].Seconds
	watchPass(p, []bbStream{rawWatched("videokey-watts", "bob-1")}, start.Add(90*time.Second))
	shown, _ = p.channelHoursShown(off, top.Add(2*time.Minute))
	require.Equal(t, before, shown[chanwatchKey("videokey-watts", "uid-bob")].Seconds, "the figures wait for the hour")
	shown, _ = p.channelHoursShown(off, top.Add(time.Hour))
	require.EqualValues(t, 90, shown[chanwatchKey("videokey-watts", "uid-bob")].Seconds)
}

func TestAnUnreadableChannelLedgerStartsFromZeroWithNoCopy(t *testing.T) {
	p, store, _ := fansPlugin(t)
	cfg := p.getConfiguration().Clone()
	cfg.EnableAchievements = true
	cfg.ChannelAchievements = "videokey-watts = Alan Watts = 1"
	p.setConfiguration(cfg)
	store[kvChanwatchTotals] = []byte(`[1,2,3`)

	start := watchEpoch()
	watts := p.streamHandle("videokey-watts")
	dave := p.streamHandle("videokey-dave")
	watchPass(p, []bbStream{rawWatched("videokey-watts"), rawWatched("videokey-dave")}, start)
	p.watchers().record("bob-1", "uid-bob", watts, start)
	p.watchers().record("erin-1", "uid-erin", dave, start)
	watchPass(p, []bbStream{rawWatched("videokey-watts", "bob-1"), rawWatched("videokey-dave", "erin-1")}, start.Add(30*time.Second))

	require.EqualValues(t, 30, mustLoad(t, p, kvChanwatchTotals)[chanwatchKey("videokey-watts", "uid-bob")].Seconds, "from zero")
	_, copied := store[kvQuarantinePrefix+kvChanwatchTotals]
	require.False(t, copied, "with no copy")
	require.EqualValues(t, 30, watched(p, kvWatchTotals, "uid-erin"), "the board carries on")
	require.EqualValues(t, 30, fanSeconds(t, p, "uid-dave", "uid-erin"), "and so do fans")
	require.Positive(t, p.ledgerAlertFor().ChannelsResetAt)
}

func TestAchievementsSnapshotStable(t *testing.T) {
	p, store, _ := achievementWorld(t, []string{"uid-walter", "uid-bob", "uid-dave"})
	seedTable(t, store, kvStatsTotals, statTable{"videokey-walter": {Seconds: 600 * hour}, "videokey-bob": {Seconds: 10 * hour}, "videokey-dave": {Seconds: 9 * hour}})
	seedTable(t, store, kvWatchTotals, statTable{"uid-bob": {Seconds: 500 * hour}, "uid-walter": {Seconds: 50 * hour}})

	var first string
	for range 5 {
		current := refreshAchievements(p, time.Now())
		encoded, err := json.Marshal(struct {
			Kinds   []achievementKind   `json:"kinds"`
			Holders []achievementHolder `json:"holders"`
		}{current.AchievementKinds, current.Achievements})
		require.NoError(t, err)

		if first == "" {
			first = string(encoded)
		}
		require.Equal(t, first, string(encoded))
	}

	current := refreshAchievements(p, time.Now())
	require.Equal(t, []achievementHolder{
		{UserID: "uid-bob", DisplayName: "Bob", Username: "bob", Have: []achievementStep{{ID: achStreamed, Tier: 1}, {ID: achSnooped, Tier: 5}}},
		{UserID: "uid-walter", DisplayName: "Walter", Username: "walter", Have: []achievementStep{{ID: achStreamed, Tier: 5}, {ID: achSnooped, Tier: 2}}},
	}, current.Achievements, "by user id, families in catalogue order, names beside them")
}

func TestMyProgressIsMineOnly(t *testing.T) {
	p, store, _ := achievementWorld(t, []string{"uid-bob", "uid-dave"})
	seedTable(t, store, kvStatsTotals, statTable{"videokey-bob": {Seconds: 62 * hour}, "videokey-dave": {Seconds: 1000 * hour}})
	refreshAchievements(p, time.Now())

	mine := func(userID string) achievementsMineResponse {
		recorder := httptest.NewRecorder()
		p.handleAchievementsMine(recorder, userID)
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))

		var answer achievementsMineResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer))

		return answer
	}

	require.Equal(t, achievementProgress{ID: achStreamed, Value: 62, Next: 100}, mine("uid-bob").Progress[0])
	require.Equal(t, achievementProgress{ID: achStreamed, Value: 1000, Next: 0}, mine("uid-dave").Progress[0], "all done")
	require.Equal(t, achievementProgress{ID: achStreamed, Value: 0, Next: 10}, mine("uid-nobody").Progress[0])

	cfg := p.getConfiguration().Clone()
	cfg.EnableLeaderboard = false
	p.setConfiguration(cfg)
	for _, progress := range mine("uid-bob").Progress {
		require.NotEqual(t, achStreamed, progress.ID, "a family that is off is left out")
	}
}
