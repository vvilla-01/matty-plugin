package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Who holds what, what goes beside a name and what a profile says, decided
// once (awards_decide.go). The rules are design/3.2.0.md 3.9 and 5.8's, and
// until 3.6.0 the web panel's own check held its copy of them to these same
// cases (build/check-awards.mjs): this is now the only copy.

var testKinds = []achievementKind{
	{ID: achStreamed, Name: "Streamed", Unit: unitHours, Tiers: []int64{10, 50, 100, 250, 500, 1000}},
	{ID: achSnooped, Name: "Snooped", Unit: unitHours, Tiers: []int64{10, 50, 100, 250, 500}},
	{ID: achPodium, Name: "Podium", Unit: unitMonths, Tiers: []int64{1, 3, 6, 12}},
	{ID: achThumbs, Name: "Thumbs up", Unit: unitThumbs, Tiers: []int64{10, 50, 100, 250}},
	{ID: "ch-1a2b3c4d5e6f", Name: "Alan Watts", Unit: unitHours, Tiers: []int64{1, 10, 50, 100, 250}},
}

// holding is a snapshot in which u-v holds whichever of the four old awards
// are asked for: a place of 0 is none.
func holding(allTime, month int, snooper, clipper bool) *snapshot {
	current := &snapshot{
		Streams:      []apiStream{},
		Configured:   true,
		AllTime:      []leaderEntry{{Rank: 1, Title: "y", Seconds: 9, UserID: "u-other"}},
		WatchAllTime: []leaderEntry{{Rank: 1, Title: "y", Seconds: 1, UserID: "u-other"}},
	}
	if allTime > 0 {
		current.AllTime = []leaderEntry{{Rank: allTime, Title: "x", Seconds: 1, UserID: "u-v"}}
	}
	if month > 0 {
		current.Month = []leaderEntry{{Rank: month, Title: "x", Seconds: 1, UserID: "u-v"}}
	}
	if snooper {
		current.WatchAllTime = []leaderEntry{{Rank: 1, Title: "x", Seconds: 1, UserID: "u-v"}}
	}
	if clipper {
		current.BestClipper = &bestClipperView{UserID: "u-v", Upvotes: 3, Clips: 1}
	}

	return current
}

func chose(current *snapshot, userID string, show ...string) *snapshot {
	if show == nil {
		show = []string{}
	}
	current.AwardChoices = append(current.AwardChoices, awardChoice{UserID: userID, Show: show})

	return current
}

func heldIDs(held []heldAward) []string {
	ids := []string{}
	for _, award := range held {
		ids = append(ids, award.ID)
	}

	return ids
}

func besideIDs(beside []besideAward) []string {
	ids := []string{}
	for _, award := range beside {
		ids = append(ids, award.ID)
	}

	return ids
}

// ---- Automatic ----

func TestAutomaticIsTheFourOldAwardsWheneverHeldInTheirOrder(t *testing.T) {
	for mask := range 16 {
		allTime, month := 0, 0
		if mask&1 != 0 {
			allTime = 2
		}
		if mask&2 != 0 {
			month = 1
		}

		expected := []string{}
		for index, id := range []string{awardAllTime, awardMonth, awardSnooper, awardClipper} {
			if mask&(1<<index) != 0 {
				expected = append(expected, id)
			}
		}

		facts := awardFactsOf(holding(allTime, month, mask&4 != 0, mask&8 != 0))
		require.Equal(t, expected, besideIDs(facts.beside("u-v")), "mask %d", mask)
	}
}

func TestAutomaticNeverIncludesAPastMonthAFanTitleOrAnAchievement(t *testing.T) {
	current := holding(1, 0, false, false)
	current.MonthMedals = []monthMedals{{UserID: "u-v", Gold: []string{"2026-08", "2026-09"}}}
	current.Fans = []fanView{{StreamerID: "u-m", Streamer: "Dave", FanID: "u-v", Fan: "Victor", Hours: 4}}
	current.FansOn = true
	current.AchievementKinds = testKinds
	current.Achievements = []achievementHolder{{UserID: "u-v", Have: []achievementStep{{ID: achStreamed, Tier: 4}}}}

	facts := awardFactsOf(current)
	require.Equal(t, []string{"alltime", "past", "fan:u-m", "ach:streamed"}, heldIDs(facts.held("u-v", nil)), "all held")
	require.Equal(t, []string{"alltime"}, besideIDs(facts.beside("u-v")))
}

// ---- A choice ----

func TestAChoiceKeepsItsOrderAndSkipsWhatIsNotHeldNow(t *testing.T) {
	held := awardFactsOf(holding(1, 2, true, true)).held("u-v", nil)
	require.Equal(t, []string{"clipper", "alltime", "snooper"}, heldIDs(shownAwards(held, []string{"clipper", "alltime", "snooper"}, true)),
		"whatever order the awards are held in")

	// Chosen and not held now: skipped, and kept. Held again, it is back
	// where it was put.
	choice := []string{"snooper", "month", "alltime"}
	without := awardFactsOf(holding(1, 0, true, false)).held("u-v", nil)
	require.Equal(t, []string{"snooper", "alltime"}, heldIDs(shownAwards(without, choice, true)))
	require.Equal(t, []string{"snooper", "month", "alltime"}, choice, "the choice itself is untouched")

	again := awardFactsOf(holding(1, 3, true, false)).held("u-v", nil)
	require.Equal(t, []string{"snooper", "month", "alltime"}, heldIDs(shownAwards(again, choice, true)))
}

func TestNeverMoreThanFourAndAnEmptyChoiceShowsNothing(t *testing.T) {
	current := holding(1, 1, true, true)
	current.MonthMedals = []monthMedals{{UserID: "u-v", Bronze: []string{"2026-07"}}}
	held := awardFactsOf(current).held("u-v", nil)

	require.Len(t, shownAwards(held, []string{"alltime", "month", "snooper", "clipper", "past"}, true), 4)
	require.Equal(t, []string{"month"}, heldIDs(shownAwards(held, []string{"nonsense", "month"}, true)), "an unknown id is ignored")
	require.Equal(t, []string{"month", "alltime"}, heldIDs(shownAwards(held, []string{"month", "month", "alltime"}, true)), "a repeat is drawn once")
	require.Empty(t, shownAwards(held, []string{}, true), "an empty choice is a choice: nothing")

	// The four are counted before what is not held is skipped: a fifth is
	// never promoted into a gap. The fifth, a past month, is held, so it
	// would show if it were.
	gap := holding(1, 0, true, true)
	gap.MonthMedals = []monthMedals{{UserID: "u-v", Bronze: []string{"2026-07"}}}
	without := awardFactsOf(gap).held("u-v", nil)
	require.Contains(t, heldIDs(without), "past")
	require.NotContains(t, heldIDs(without), "month")
	require.Equal(t, []string{"alltime", "snooper", "clipper"}, heldIDs(shownAwards(without, []string{"alltime", "month", "snooper", "clipper", "past"}, true)))

	// And as the snapshot carries it: by a name, the choice; nobody else's.
	facts := awardFactsOf(chose(chose(holding(1, 2, false, true), "u-v", "clipper", "month"), "u-b", "a", "b", "c", "d", "e"))
	require.Equal(t, []string{"clipper", "month"}, besideIDs(facts.beside("u-v")))
	require.Len(t, facts.choices["u-b"], 4)
	require.Empty(t, facts.beside("u-nobody"))
	require.Empty(t, facts.beside(""))
}

// ---- Past months ----

func TestAPastMonthIsHeldExactlyWhenAMedalIsAtTheBestPlaceHeld(t *testing.T) {
	one := func(medals monthMedals) *heldAward {
		medals.UserID = "u-v"
		current := holding(0, 0, false, false)
		current.MonthMedals = []monthMedals{medals}
		for _, award := range awardFactsOf(current).held("u-v", nil) {
			if award.ID == awardPast {
				return &award
			}
		}

		return nil
	}

	require.Nil(t, one(monthMedals{}), "no medal, no award")

	bronze := one(monthMedals{Bronze: []string{"2026-01"}})
	require.Equal(t, 3, bronze.Rank)
	require.Equal(t, "Monthly podium", bronze.Label)

	require.Equal(t, 2, one(monthMedals{Silver: []string{"2026-02"}, Bronze: []string{"2026-01"}}).Rank)

	champion := one(monthMedals{Gold: []string{"2026-03", "2026-05"}, Silver: []string{"2026-02"}})
	require.Equal(t, 1, champion.Rank)
	require.Equal(t, "Monthly champion, 2 months", champion.Label)
	require.Equal(t, "Monthly champion ×2", champion.Short)
	require.Equal(t, 2, champion.Wins)
	require.Equal(t, 3, champion.Months)

	require.Equal(t, "Monthly champion", one(monthMedals{Gold: []string{"2026-03"}}).Short, "×N only from two golds")
	require.Equal(t, 1, one(monthMedals{Gold: []string{"2026-03", "2026-03"}}).Wins, "a month counted once")
}

// ---- Fans and achievements ----

func TestAFanTitleIsHeldOnlyWhileBiggestFansAreOn(t *testing.T) {
	fans := []fanView{
		{StreamerID: "u-a", Streamer: "Bob", FanID: "u-v", Fan: "Victor", Hours: 2},
		{StreamerID: "u-m", Streamer: "Dave", FanID: "u-v", Fan: "Victor", Hours: 41},
		{StreamerID: "u-z", Streamer: "Walter", FanID: "u-other", Fan: "Other", Hours: 9},
	}

	on := holding(0, 0, false, false)
	on.Fans, on.FansOn = fans, true
	held := awardFactsOf(on).held("u-v", nil)
	require.Equal(t, []string{"fan:u-a", "fan:u-m"}, heldIDs(held), "one per streamer, as the list has them")
	require.Equal(t, "Biggest fan of Bob", held[0].Label)
	require.EqualValues(t, 41, held[1].Hours)

	off := holding(0, 0, false, false)
	off.Fans = fans
	require.Empty(t, awardFactsOf(off).held("u-v", nil), "fans without the switch: biggest fans are off, whatever else the snapshot carries")
	require.Empty(t, awardFactsOf(off).rows("u-v"))
}

func TestAnAchievementIsHeldAtItsHighestStepInCatalogueOrder(t *testing.T) {
	current := holding(0, 0, false, false)
	current.AchievementKinds = testKinds
	current.Achievements = []achievementHolder{{UserID: "u-v", Have: []achievementStep{{ID: "ch-1a2b3c4d5e6f", Tier: 1}, {ID: achStreamed, Tier: 3}}}}

	held := awardFactsOf(current).held("u-v", nil)
	require.Equal(t, []string{"ach:streamed", "ach:ch-1a2b3c4d5e6f"}, heldIDs(held))
	require.Equal(t, besideAward{ID: "ach:streamed", Kind: kindAch, Family: achStreamed, Tier: 3, Label: "Streamed 100 hours"}, held[0].besideAward)
	require.Equal(t, "Alan Watts: 1 hour", held[1].Label)

	// A family switched off is not held.
	current.AchievementKinds = testKinds[1:]
	require.Equal(t, []string{"ach:ch-1a2b3c4d5e6f"}, heldIDs(awardFactsOf(current).held("u-v", nil)))
}

func TestTheStepsNames(t *testing.T) {
	kind := func(id string) achievementKind {
		for _, entry := range testKinds {
			if entry.ID == id {
				return entry
			}
		}

		return achievementKind{}
	}

	require.Equal(t, "Streamed 10 hours", stepName(kind(achStreamed), 1))
	require.Equal(t, "Snooped 50 hours", stepName(kind(achSnooped), 2))
	require.Equal(t, "On the podium 1 month", stepName(kind(achPodium), 1))
	require.Equal(t, "On the podium 6 months", stepName(kind(achPodium), 3))
	require.Equal(t, "50 thumbs up", stepName(kind(achThumbs), 2))
	require.Equal(t, "Alan Watts: 1 hour", stepName(kind("ch-1a2b3c4d5e6f"), 1))
	require.Equal(t, "Alan Watts: 10 hours", stepName(kind("ch-1a2b3c4d5e6f"), 2))

	// A step past the last is the last, and one below the first is the first.
	require.Equal(t, "Streamed 1000 hours", stepName(kind(achStreamed), 9))
	require.Equal(t, "Streamed 10 hours", stepName(kind(achStreamed), 0))
	require.Equal(t, "Nothing: 0 hours", stepName(achievementKind{ID: "ch-x", Name: "Nothing"}, 1))
}

// ---- The Live now card, and beside "Clipped by" ----

func TestACardsOwnPlacesStandInForTheBoards(t *testing.T) {
	// On the boards u-v holds nothing; the card says second all time and
	// first this month, as the server works a card's places out.
	card := apiStream{Handle: "h", UserID: "u-v", AllTimeRank: 2, MonthRank: 1}

	automatic := awardFactsOf(holding(0, 0, true, true)).onCard(card)
	require.Equal(t, []string{"alltime", "month", "snooper"}, besideIDs(automatic), "3.1.0's card set: the card's places, the binoculars, and no clapper")
	require.Equal(t, besideAward{ID: awardAllTime, Kind: kindAllTime, Rank: 2, Label: "2nd for airtime, all time"}, automatic[0])
	require.Equal(t, "1st for airtime this month", automatic[1].Label)
	require.Equal(t, "Top Snooper, all time", automatic[2].Label)

	// A choice is the same on the card as by a post, with the card's places.
	chosen := awardFactsOf(chose(holding(0, 0, true, true), "u-v", "clipper", "month"))
	require.Equal(t, []string{"clipper", "month"}, besideIDs(chosen.onCard(card)))
	require.Equal(t, 1, chosen.onCard(card)[1].Rank)
	require.Equal(t, []string{"clipper"}, besideIDs(chosen.beside("u-v")), "by a post the boards' own place counts, and they hold none")

	// The Top Snooper's Twitch card has no binoculars on Automatic, as in
	// 3.1.0, and follows a choice like any other card.
	twitch := card
	twitch.Source = "twitch"
	require.Equal(t, []string{"alltime", "month"}, besideIDs(awardFactsOf(holding(0, 0, true, true)).onCard(twitch)))
	require.Equal(t, []string{"clipper", "month"}, besideIDs(chosen.onCard(twitch)))

	// A card with no person keeps its places and nothing else; a 24/7
	// channel has none, whoever it is attributed to.
	nobody := apiStream{Handle: "n", AllTimeRank: 3}
	require.Equal(t, []string{"alltime"}, besideIDs(awardFactsOf(holding(1, 1, true, true)).onCard(nobody)))
	radio := apiStream{Handle: "r", UserID: "u-v", AlwaysOn: true, AllTimeRank: 1}
	require.Empty(t, awardFactsOf(holding(1, 1, true, true)).onCard(radio))
	require.Empty(t, chosen.onCard(radio))
}

// A client draws the clapperboard beside "Clipped by" for whoever has it in
// the awards by their name: the Best Clipper, unless their choice leaves it
// out. Beside the name is beside the name.
func TestTheClapperIsBesideTheBestClippersNameUnlessTheirChoiceLeavesItOut(t *testing.T) {
	clapper := func(current *snapshot) bool {
		for _, award := range awardFactsOf(current).beside("u-v") {
			if award.Kind == kindClipper {
				return true
			}
		}

		return false
	}

	require.True(t, clapper(holding(0, 0, false, true)))
	require.Equal(t, []besideAward{{ID: awardClipper, Kind: kindClipper, Label: "Best Clipper, all time"}},
		awardFactsOf(chose(holding(0, 0, false, true), "u-v", "clipper")).beside("u-v"),
		"said beside a name as the panel says it; the picker's chip keeps its own words")
	require.Equal(t, "Best Clipper", awardFactsOf(holding(0, 0, false, true)).held("u-v", nil)[0].Label)
	require.False(t, clapper(chose(holding(0, 0, false, true), "u-v", "alltime")))
	require.True(t, clapper(chose(holding(0, 0, false, true), "u-v", "clipper")))
	require.False(t, clapper(holding(0, 0, false, false)), "nobody holds it")
}

// ---- A profile ----

func TestAProfileKeepsTheWholeRecordWhateverIsShownBesideTheName(t *testing.T) {
	current := chose(holding(2, 1, true, true), "u-v")
	current.AllTime[0].Seconds = 1483200
	current.Month[0].Seconds = 7260
	current.WatchAllTime[0].Seconds = 90000
	current.MonthLabel = "November 2026"
	current.BestClipper = &bestClipperView{UserID: "u-v", Upvotes: 142, Clips: 9}
	current.Fans = []fanView{
		{StreamerID: "u-a", Streamer: "bob", FanID: "u-v", Hours: 41},
		{StreamerID: "u-b", Streamer: "Trudy", FanID: "u-v", Hours: 41},
		{StreamerID: "u-m", Streamer: "Dave", FanID: "u-v", Hours: 60},
	}
	current.FansOn = true
	current.AchievementKinds = testKinds
	current.Achievements = []achievementHolder{{UserID: "u-v", Have: []achievementStep{{ID: achPodium, Tier: 2}, {ID: achStreamed, Tier: 4}}}}
	current.MonthMedals = []monthMedals{{UserID: "u-v", Gold: []string{"2026-03", "2026-08", "2026-03"}, Bronze: []string{"2026-01", "2026-02"}}}

	facts := awardFactsOf(current)
	require.Empty(t, facts.beside("u-v"), "they chose to show nothing")

	require.Equal(t, []awardRow{
		{Kind: rowBoard, ID: awardAllTime, Rank: 2, Title: "Runner-up", Board: "All time", Seconds: 1483200},
		{Kind: rowBoard, ID: awardMonth, Rank: 1, Title: "Champion", Board: "November 2026", Seconds: 7260},
		{Kind: rowBoard, ID: awardSnooper, Rank: 1, Title: "Top Snooper", Board: "All time", Seconds: 90000, Doing: "watching others"},
		{Kind: rowClipper, ID: awardClipper, Rank: 1, Title: "Best Clipper", Upvotes: 142, Clips: 9},

		// Most hours first, then by name whatever its case.
		{Kind: rowFan, ID: "fan:u-m", Rank: 1, Title: "Biggest fan of Dave", StreamerID: "u-m", Streamer: "Dave", Hours: 60},
		{Kind: rowFan, ID: "fan:u-a", Rank: 1, Title: "Biggest fan of bob", StreamerID: "u-a", Streamer: "bob", Hours: 41},
		{Kind: rowFan, ID: "fan:u-b", Rank: 1, Title: "Biggest fan of Trudy", StreamerID: "u-b", Streamer: "Trudy", Hours: 41},

		// One row however many families, in catalogue order.
		{Kind: rowAchievements, ID: "achievements", Title: "Achievements", Steps: []awardStep{
			{ID: achStreamed, Tier: 4, Name: "Streamed 250 hours"},
			{ID: achPodium, Tier: 2, Name: "On the podium 3 months"},
		}},

		// Last: what somebody holds now outranks what they won in the spring.
		{Kind: rowMonths, ID: awardPast, Rank: 1, Title: "Monthly champion", Gold: []string{"2026-08", "2026-03"}, Tallies: []awardTally{{Rank: 3, Months: []string{"2026-02", "2026-01"}}}},
	}, facts.rows("u-v"))

	// Nothing of it is formatted, and only a row's own facts are sent.
	encoded, err := json.Marshal(facts.rows("u-v")[0])
	require.NoError(t, err)
	require.JSONEq(t, `{"kind":"board","id":"alltime","rank":2,"title":"Runner-up","board":"All time","seconds":1483200}`, string(encoded))

	// A podium with no win is a podium, at its best place.
	current.MonthMedals = []monthMedals{{UserID: "u-v", Silver: []string{"2026-02"}, Bronze: []string{"2026-01"}}}
	rows := awardFactsOf(current).rows("u-v")
	require.Equal(t, awardRow{Kind: rowMonths, ID: awardPast, Rank: 2, Title: "Monthly podium", Tallies: []awardTally{{Rank: 2, Months: []string{"2026-02"}}, {Rank: 3, Months: []string{"2026-01"}}}}, rows[len(rows)-1])

	// With no label for the month, the row still names its board.
	current.MonthLabel = ""
	require.Equal(t, "This month", awardFactsOf(current).rows("u-v")[1].Board)

	// Somebody off the podium has no row for a board they are merely on.
	current.AllTime = append(current.AllTime, leaderEntry{Rank: 4, Title: "z", Seconds: 5, UserID: "u-fourth"})
	require.Empty(t, awardFactsOf(current).rows("u-fourth"))
	require.Empty(t, awardFactsOf(current).rows(""))
}

// Fan rows with the same hours are in the order of their names as a person
// reads them, blind to case and accents, as the panel and Matty ordered them
// before the server did: Óscar before Peggy, the_gamer before the9gamer.
// Then by who the streamer is, for two of the same name.
func TestFanRowsOfTheSameHoursAreInTheOrderOfTheirNames(t *testing.T) {
	current := holding(0, 0, false, false)
	current.FansOn = true
	current.Fans = []fanView{
		{StreamerID: "u-p", Streamer: "Peggy", FanID: "u-v", Hours: 2},
		{StreamerID: "u-o", Streamer: "Óscar", FanID: "u-v", Hours: 2},
		{StreamerID: "u-9", Streamer: "the9gamer", FanID: "u-v", Hours: 3},
		{StreamerID: "u-g", Streamer: "the_gamer", FanID: "u-v", Hours: 3},
		{StreamerID: "u-z", Streamer: "Wendy", FanID: "u-v", Hours: 1},
		{StreamerID: "u-e2", Streamer: "Ève", FanID: "u-v", Hours: 1},
		{StreamerID: "u-e1", Streamer: "eve", FanID: "u-v", Hours: 1},

		// A pictograph newer than the collator's table is still a symbol: after
		// punctuation, before a letter, as a browser and a phone put it.
		{StreamerID: "u-mallory", Streamer: "Mallory", FanID: "u-v", Hours: 4},
		{StreamerID: "u-fox", Streamer: "🦊Fox", FanID: "u-v", Hours: 4},
		{StreamerID: "u-und", Streamer: "_under", FanID: "u-v", Hours: 4},
	}

	var order []string
	for _, row := range awardFactsOf(current).rows("u-v") {
		if row.Kind == rowFan {
			order = append(order, row.StreamerID)
		}
	}
	require.Equal(t, []string{"u-und", "u-fox", "u-mallory", "u-g", "u-9", "u-o", "u-p", "u-e1", "u-e2", "u-z"}, order)
}

// ---- A snapshot being stored ----

func TestPublishDecidesTheAwardsAndTakesTheBoardsOutOfTheSnapshot(t *testing.T) {
	api := &plugintest.API{}
	api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return()

	p := &Plugin{}
	p.SetAPI(api)

	handed := []apiStream{
		{Handle: "h-v", UserID: "u-v", AllTimeRank: 2, MonthRank: 1},
		{Handle: "h-radio", UserID: "u-v", AlwaysOn: true},
	}
	current := chose(holding(2, 1, true, true), "u-other", "alltime")
	current.Streams = handed
	current.FetchedAt = 1000
	current.MonthLabel = "November 2026"
	current.Voice = &voiceView{Connected: true, People: []voicePerson{{Name: "V", UserID: "u-v"}}}
	p.publish(current)

	stored := p.getSnapshot()
	require.Equal(t, []string{"alltime", "month", "snooper"}, besideIDs(stored.Streams[0].Awards), "the card's badges")
	require.Empty(t, stored.Streams[1].Awards)
	require.Nil(t, handed[0].Awards, "written on a copy of the list: the one handed in may be one readers hold")

	// Beside a name, with what the index already said of them.
	require.Equal(t, voiceOpen, stored.People["u-v"].Voice)
	require.Equal(t, "h-v", stored.People["u-v"].Live)
	require.Equal(t, []string{"alltime", "month", "snooper", "clipper"}, besideIDs(stored.People["u-v"].Awards))
	require.NotContains(t, stored.People, "u-other", "u-other holds nothing in this one")

	// What is sent: the decisions, and one word about the boards.
	pushed, ok := api.Calls[0].Arguments[1].(map[string]any)
	require.True(t, ok)
	payload, ok := pushed["payload"].(string)
	require.True(t, ok)
	require.Contains(t, payload, `"boards":{"version":"`+stored.Boards.Version+`"}`)
	require.Contains(t, payload, `"awards":[{"id":"alltime","kind":"alltime","rank":2,"label":"2nd for airtime, all time"},`)
	for _, gone := range []string{"allTime", "month", "monthLabel", "watchAllTime", "bestClipper", "awardChoices", "allTimeRank", "monthRank", "seconds"} {
		require.NotContains(t, payload, `"`+gone+`":`, "the boards are not in the snapshot")
	}

	// And the boards, with everybody's record, where they are read.
	boards := p.boardsNow()
	require.Equal(t, stored.Boards.Version, boards.Version)
	require.Equal(t, "November 2026", boards.MonthLabel)
	require.Len(t, boards.AllTime, 1)
	require.NotNil(t, boards.BestClipper)
	require.Len(t, boards.People["u-v"].Rows, 4)
	require.NotContains(t, boards.People, "u-other", "u-other is on no podium in this one")

	// Somebody who holds awards, first for airtime and Top Snooper, but
	// chose only one they do not hold, has nothing beside their name, and is
	// not in the index for it.
	quiet := chose(holding(0, 0, false, false), "u-other", "month")
	quiet.FetchedAt = 2000
	require.NotEmpty(t, awardFactsOf(quiet).held("u-other", nil), "they hold something")
	p.publish(quiet)
	require.NotContains(t, p.getSnapshot().People, "u-other", "somebody whose choice shows nothing has nothing to say")
}

// A family comes with what each of its steps is called, so that a client
// showing the whole ladder, or the next step, words none of it.
func TestTheCatalogueComesWithItsStepsNames(t *testing.T) {
	current := holding(0, 0, false, false)
	current.AchievementKinds = testKinds[:1]

	require.JSONEq(t, `[{"id":"streamed","name":"Streamed","unit":"hours","tiers":[10,50,100,250,500,1000],
		"names":["Streamed 10 hours","Streamed 50 hours","Streamed 100 hours","Streamed 250 hours","Streamed 500 hours","Streamed 1000 hours"]}]`,
		jsonOf(t, decideAwards(current).AchievementKinds))
	require.Nil(t, decideAwards(holding(0, 0, false, false)).AchievementKinds, "and no catalogue is no field")
}

func TestTheBoardsVersionMovesWithEverythingButSeconds(t *testing.T) {
	scene := func() *snapshot {
		current := holding(1, 2, true, true)
		current.Fans = []fanView{{StreamerID: "u-m", Streamer: "Dave", FanID: "u-v", Hours: 41}}
		current.FansOn = true
		current.MonthMedals = []monthMedals{{UserID: "u-v", Gold: []string{"2026-03"}}}

		return current
	}
	versionOf := func(change func(current *snapshot)) string {
		current := scene()
		if change != nil {
			change(current)
		}

		return decideAwards(current).Version
	}

	base := versionOf(nil)
	require.Len(t, base, 16)
	require.Equal(t, base, versionOf(nil), "the same boards are the same word")

	require.Equal(t, base, versionOf(func(current *snapshot) {
		current.AllTime[0].Seconds += 3600
		current.AllTime[0].TwitchSeconds += 60
		current.Month[0].Seconds += 3600
		current.WatchAllTime[0].Seconds += 3600
	}), "seconds that tick, on a board or on a profile's row, are not a change")

	for name, change := range map[string]func(current *snapshot){
		"a place": func(current *snapshot) { current.Month[0].Rank = 1 },
		"a name":  func(current *snapshot) { current.AllTime[0].Title = "renamed" },
		"a new row": func(current *snapshot) {
			current.Month = append(current.Month, leaderEntry{Rank: 3, Title: "n", UserID: "u-n"})
		},
		"a fan's whole hours": func(current *snapshot) { current.Fans[0].Hours++ },
		"a medal":             func(current *snapshot) { current.MonthMedals[0].Gold = append(current.MonthMedals[0].Gold, "2026-04") },
		"a vote":              func(current *snapshot) { current.BestClipper.Upvotes++ },
		"the month":           func(current *snapshot) { current.MonthLabel = "December 2026" },
		"a board switched on": func(current *snapshot) { current.WatchBoards = true },
	} {
		require.NotEqual(t, base, versionOf(change), name)
	}

	// Somebody's choice changes what is beside their name, which the
	// snapshot carries itself; the boards are what they were.
	require.Equal(t, base, versionOf(func(current *snapshot) { chose(current, "u-v", "clipper") }))
}

// ---- The routes ----

func TestTheBoardsAreReadApartAndConditionally(t *testing.T) {
	s := newAccessScene(t)
	s.api.On("PublishWebSocketEvent", wsEventStreams, mock.Anything, mock.Anything).Return()

	// Before anything was published: an empty document, and nothing to hold
	// a copy against.
	early := s.serve(http.MethodGet, "/api/v1/boards", sceneLinked, "", nil)
	require.Equal(t, http.StatusOK, early.Code)
	require.JSONEq(t, `{"version":"","people":{}}`, early.Body.String())
	require.Empty(t, early.Header().Get("ETag"))

	// A reader who holds a copy from before a restart keeps it until there
	// is something to compare it with.
	kept := s.serve(http.MethodGet, "/api/v1/boards", sceneLinked, "", map[string]string{"If-None-Match": `"0123456789abcdef"`})
	require.Equal(t, http.StatusNotModified, kept.Code)
	require.Empty(t, kept.Body.String())
	require.Empty(t, kept.Header().Get("ETag"))

	current := holding(1, 2, true, true)
	current.AllTime[0].UserID, current.Month[0].UserID = sceneLinked, sceneLinked
	current.FetchedAt = 1000
	s.p.publish(current)

	first := s.serve(http.MethodGet, "/api/v1/boards", sceneLinked, "", nil)
	require.Equal(t, http.StatusOK, first.Code)
	require.Equal(t, "private, no-cache", first.Header().Get("Cache-Control"))
	require.Equal(t, "application/json", first.Header().Get("Content-Type"))

	var boards boardsView
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &boards))
	require.Equal(t, s.p.getSnapshot().Boards.Version, boards.Version)
	require.Len(t, boards.People[sceneLinked].Rows, 2)

	tag := first.Header().Get("ETag")
	require.NotEmpty(t, tag)
	still := s.serve(http.MethodGet, "/api/v1/boards", sceneLinked, "", map[string]string{"If-None-Match": tag})
	require.Equal(t, http.StatusNotModified, still.Code)
	require.Empty(t, still.Body.String())

	// A second moved: the snapshot's word stands, and the reader who asks is
	// given the boards again.
	ticked := holding(1, 2, true, true)
	ticked.AllTime[0].UserID, ticked.Month[0].UserID = sceneLinked, sceneLinked
	ticked.AllTime[0].Seconds++
	ticked.FetchedAt = 2000
	s.p.publish(ticked)
	require.Equal(t, boards.Version, s.p.getSnapshot().Boards.Version)

	moved := s.serve(http.MethodGet, "/api/v1/boards", sceneLinked, "", map[string]string{"If-None-Match": tag})
	require.Equal(t, http.StatusOK, moved.Code)
	require.NotEqual(t, tag, moved.Header().Get("ETag"))

	// Open, as the snapshot they rode until 3.6.0 is: a profile outside the
	// panel shows somebody's record to every reader.
	require.Equal(t, http.StatusOK, s.serve(http.MethodGet, "/api/v1/boards", sceneLocked, "", nil).Code)
	require.Equal(t, http.StatusOK, s.serve(http.MethodGet, "/api/v1/boards", sceneLetIn, "", nil).Code)
	require.Equal(t, http.StatusMethodNotAllowed, s.serve(http.MethodPost, "/api/v1/boards", sceneLinked, "", nil).Code)
}

func TestThePickerIsToldWhatItsReaderHolds(t *testing.T) {
	s := newAccessScene(t)
	s.api.On("PublishWebSocketEvent", wsEventStreams, mock.Anything, mock.Anything).Return()

	current := holding(2, 0, false, true)
	current.AllTime[0].UserID, current.BestClipper.UserID = sceneLinked, sceneLinked
	current.FetchedAt = 1000
	s.p.publish(current)

	recorder := s.serve(http.MethodGet, "/api/v1/awards/mine", sceneLinked, "", nil)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.JSONEq(t, `{"show":null,"readable":true,"held":[
		{"id":"alltime","kind":"alltime","rank":2,"label":"2nd for airtime, all time","short":"2nd, all time"},
		{"id":"clipper","kind":"clipper","label":"Best Clipper","short":"Best Clipper"}
	]}`, recorder.Body.String())
}

func jsonOf(t *testing.T, value any) string {
	t.Helper()

	encoded, err := json.Marshal(value)
	require.NoError(t, err)

	return string(encoded)
}
