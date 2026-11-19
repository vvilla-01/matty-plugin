package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// Awards, decided once (contract/README.md, "Awards and the boards").
//
// Until 3.6.0 the snapshot carried the four boards, the medals, the fans, the
// achievements and everybody's choice, and every client worked out from them
// who holds what, which of it goes beside a name, and what a profile says:
// the web panel in two places (what is beside a name, and a profile's rows),
// and Matty in a port of the second made by hand. The rules are here now, the
// only copy, applied where every figure they need is in hand:
//
//   - beside a name (people[id].awards in the snapshot): up to four, the
//     person's own choice or Automatic;
//   - on a Live now card (streams[].awards): the same choice with the card's
//     own places, or 3.1.0's card set;
//   - on a profile (people[id].rows in GET /boards): the whole record, as
//     rows with their facts;
//   - in the picker (held in GET /awards/mine): everything the reader holds.
//
// A client maps an award to its artwork and writes figures the reader's way
// (a duration, a month). It decides nothing about who holds what.

// The kinds of award, which is also how a client picks the artwork.
const (
	kindAllTime = "alltime"
	kindMonth   = "month"
	kindSnooper = "snooper"
	kindClipper = "clipper"
	kindPast    = "past"
	kindFan     = "fan"
	kindAch     = "ach"
)

// besideAward is one award as it is drawn beside a name or on a card: which
// it is, what picks its artwork (the place, or the family and its step), and
// what it says to a screen reader and when pointed at.
type besideAward struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`

	// The crown's and the calendars' place, 1 to 3; a past month's best.
	Rank int `json:"rank,omitempty"`

	// An achievement: its family's id and how many steps.
	Family string `json:"family,omitempty"`
	Tier   int    `json:"tier,omitempty"`

	Label string `json:"label"`
}

// heldAward is one award somebody holds now, with what the picker says about
// it. The id says which award, never the place: a choice of "alltime" draws
// the crown at whatever place they hold, and nothing when they hold none.
type heldAward struct {
	besideAward

	// A past month's medal: the months won, and on the podium at all.
	Wins   int `json:"wins,omitempty"`
	Months int `json:"months,omitempty"`

	// A biggest fan: whose, and for how long.
	Streamer string `json:"streamer,omitempty"`
	Hours    int64  `json:"hours,omitempty"`

	// The words on its chip in the picker.
	Short string `json:"short"`
}

// The kinds of row on a profile.
const (
	rowBoard        = "board"
	rowClipper      = "clipper"
	rowFan          = "fan"
	rowAchievements = "achievements"
	rowMonths       = "months"

	rowIDAchievements = "achievements"
)

// awardRow is one row of somebody's record, as a profile shows it: what it is
// called and the facts under it. Only the facts of its own kind are there.
// Nothing in it is formatted: seconds are seconds and months are "YYYY-MM",
// for the reader's client to write their way.
type awardRow struct {
	Kind string `json:"kind"`

	// alltime, month, snooper, clipper, fan:<streamer id>, achievements or
	// past: the award the row is for, as its id beside a name.
	ID string `json:"id"`

	// The place, which picks the row's tint and its artwork: 1 to 3, and 0
	// for the achievements, which have none.
	Rank  int    `json:"rank"`
	Title string `json:"title"`

	// A place on a board: which board, how long, and what the time was spent
	// on when it was not streaming.
	Board   string `json:"board,omitempty"`
	Seconds int64  `json:"seconds,omitempty"`
	Doing   string `json:"doing,omitempty"`

	// The Best Clipper's score.
	Upvotes int `json:"upvotes,omitempty"`
	Clips   int `json:"clips,omitempty"`

	// A biggest fan: of whom, and for how long.
	StreamerID string `json:"streamerId,omitempty"`
	Streamer   string `json:"streamer,omitempty"`
	Hours      int64  `json:"hours,omitempty"`

	// The achievements, one step a family, in catalogue order.
	Steps []awardStep `json:"steps,omitempty"`

	// The months already over: the ones won, newest first, and second and
	// third places, whichever there are, in that order.
	Gold    []string     `json:"gold,omitempty"`
	Tallies []awardTally `json:"tallies,omitempty"`
}

type awardStep struct {
	ID   string `json:"id"`
	Tier int    `json:"tier"`
	Name string `json:"name"`
}

type awardTally struct {
	Rank   int      `json:"rank"`
	Months []string `json:"months"`
}

// What a place on a board is called. Only the podium earns one.
var podiumTitles = map[int]string{1: "Champion", 2: "Runner-up", 3: "Third place"}

const (
	snooperTitle = "Top Snooper"
	clipperTitle = "Best Clipper"

	boardAllTime = "All time"

	// What the Top Snooper's hours were spent on, said after them: a bare
	// figure reads as airtime, which is what every other row's hours are.
	doingWatching = "watching others"
)

// Automatic: exactly 3.1.0, the four old awards whenever held, in this order.
// Nothing new ever joins it by itself, past months included.
var automaticAwards = []string{awardAllTime, awardMonth, awardSnooper, awardClipper}

func ordinal(rank int) string {
	switch rank {
	case 1:
		return "1st"
	case 2:
		return "2nd"
	case 3:
		return "3rd"
	default:
		return fmt.Sprintf("%dth", rank)
	}
}

func counted(count int64, one, many string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, one)
	}

	return fmt.Sprintf("%d %s", count, many)
}

// stepName is a step's name, as everybody reads it: "Streamed 100 hours", "On
// the podium 1 month", "50 thumbs up", "Lo-fi radio: 10 hours".
func stepName(kind achievementKind, tier int) string {
	var step int64
	if index := max(0, min(len(kind.Tiers), tier)-1); index < len(kind.Tiers) {
		step = kind.Tiers[index]
	}

	switch kind.ID {
	case achStreamed:
		return "Streamed " + counted(step, "hour", "hours")
	case achSnooped:
		return "Snooped " + counted(step, "hour", "hours")
	case achPodium:
		return "On the podium " + counted(step, "month", "months")
	case achThumbs:
		return fmt.Sprintf("%d thumbs up", step)
	default:
		return kind.Name + ": " + counted(step, "hour", "hours")
	}
}

// forOrder is a name as the collator is asked about it. The collator's table
// is older than most of the pictographs people put in a name, and a symbol it
// does not know is weighed after every letter, where a browser's and a
// phone's own collation, which ordered these rows until the server did, put
// every symbol before the digits and the letters ("🦊Fox" before "Mallory"). So a
// pictograph from the planes where those live is asked about as a symbol the
// table has always known.
func forOrder(name string) string {
	return strings.Map(func(r rune) rune {
		if r >= 0x1F000 && (unicode.Is(unicode.So, r) || unicode.Is(unicode.Sk, r)) {
			return '☀'
		}

		return r
	}, name)
}

// awardFacts is a snapshot's boards and lists, laid out by person: what the
// rules below are asked of.
type awardFacts struct {
	// A place on the podium of each airtime board, by person.
	allTime map[string]leaderEntry
	month   map[string]leaderEntry

	// First place on the all-time Snoopers board: one holder, and nobody
	// while the board is off.
	snooper *leaderEntry

	clipper *bestClipperView
	medals  map[string]monthMedals

	// Who is whose biggest fan, by fan, in the order a profile lists them.
	// Empty while biggest fans are off, whatever the snapshot carries.
	fans map[string][]fanView

	kinds []achievementKind
	have  map[string][]achievementStep

	// Who chose what shows beside their name. No entry is Automatic.
	choices map[string][]string

	monthLabel string
}

func podiumOf(rows []leaderEntry) map[string]leaderEntry {
	places := map[string]leaderEntry{}
	seen := map[string]bool{}

	for _, row := range rows {
		// The first row of theirs is the one that counts, podium or not.
		if row.UserID == "" || seen[row.UserID] {
			continue
		}
		seen[row.UserID] = true

		if row.Rank >= 1 && row.Rank <= 3 {
			places[row.UserID] = row
		}
	}

	return places
}

func awardFactsOf(current *snapshot) *awardFacts {
	facts := &awardFacts{
		allTime:    podiumOf(current.AllTime),
		month:      podiumOf(current.Month),
		clipper:    current.BestClipper,
		medals:     map[string]monthMedals{},
		fans:       map[string][]fanView{},
		kinds:      current.AchievementKinds,
		have:       map[string][]achievementStep{},
		choices:    map[string][]string{},
		monthLabel: current.MonthLabel,
	}

	for i := range current.WatchAllTime {
		if current.WatchAllTime[i].Rank == 1 {
			facts.snooper = &current.WatchAllTime[i]
			break
		}
	}

	for _, medals := range current.MonthMedals {
		if _, seen := facts.medals[medals.UserID]; !seen {
			facts.medals[medals.UserID] = medals
		}
	}

	// Only while biggest fans are on: fans without the switch are not trusted.
	if current.FansOn {
		for _, fan := range current.Fans {
			facts.fans[fan.FanID] = append(facts.fans[fan.FanID], fan)
		}
		// Names are compared blind to case and accents, as the panel and
		// Matty ordered these rows before the server did (localeCompare at
		// base sensitivity, a Collator at primary strength): Óscar before
		// Peggy. One collator for this snapshot: it is not safe to share
		// between goroutines.
		names := collate.New(language.Und, collate.Loose)
		for fanID, titles := range facts.fans {
			// Most hours first, then by the streamer's name, then by who
			// they are, so a profile lists them the same way for everybody.
			sort.SliceStable(titles, func(i, j int) bool {
				if titles[i].Hours != titles[j].Hours {
					return titles[i].Hours > titles[j].Hours
				}
				if order := names.CompareString(forOrder(titles[i].Streamer), forOrder(titles[j].Streamer)); order != 0 {
					return order < 0
				}

				return titles[i].StreamerID < titles[j].StreamerID
			})
			facts.fans[fanID] = titles
		}
	}

	for _, holder := range current.Achievements {
		if _, seen := facts.have[holder.UserID]; !seen {
			facts.have[holder.UserID] = holder.Have
		}
	}

	for _, choice := range current.AwardChoices {
		show := choice.Show
		if len(show) > maxAwardsShown {
			show = show[:maxAwardsShown]
		}
		if show == nil {
			show = []string{}
		}
		facts.choices[choice.UserID] = show
	}

	return facts
}

// holders is everybody who holds anything at all, sorted.
func (f *awardFacts) holders() []string {
	seen := map[string]bool{}
	for userID := range f.allTime {
		seen[userID] = true
	}
	for userID := range f.month {
		seen[userID] = true
	}
	if f.snooper != nil {
		seen[f.snooper.UserID] = true
	}
	if f.clipper != nil {
		seen[f.clipper.UserID] = true
	}
	for userID := range f.medals {
		seen[userID] = true
	}
	for userID := range f.fans {
		seen[userID] = true
	}
	for userID := range f.have {
		seen[userID] = true
	}
	delete(seen, "")

	ids := make([]string, 0, len(seen))
	for userID := range seen {
		ids = append(ids, userID)
	}
	sort.Strings(ids)

	return ids
}

// cardPlaces is a Live now card's own places on the two airtime boards, which
// stand in for the boards' when the card's badges are decided: the server
// works them out per card from the same boards (rankByRow).
type cardPlaces struct {
	allTime, month int
}

func uniqueCount(months []string) int {
	seen := map[string]bool{}
	for _, month := range months {
		seen[month] = true
	}

	return len(seen)
}

// held is everything somebody holds now, in one order: the four old awards,
// the past months, the fan titles, then the achievements in catalogue order.
func (f *awardFacts) held(userID string, card *cardPlaces) []heldAward {
	var held []heldAward
	if userID == "" {
		return held
	}

	allTime, month := f.allTime[userID].Rank, f.month[userID].Rank
	if card != nil {
		allTime, month = card.allTime, card.month
	}

	if allTime > 0 {
		held = append(held, heldAward{
			besideAward: besideAward{ID: awardAllTime, Kind: kindAllTime, Rank: allTime, Label: ordinal(allTime) + " for airtime, all time"},
			Short:       ordinal(allTime) + ", all time",
		})
	}
	if month > 0 {
		held = append(held, heldAward{
			besideAward: besideAward{ID: awardMonth, Kind: kindMonth, Rank: month, Label: ordinal(month) + " for airtime this month"},
			Short:       ordinal(month) + " this month",
		})
	}

	if f.snooper != nil && f.snooper.UserID == userID {
		held = append(held, heldAward{
			besideAward: besideAward{ID: awardSnooper, Kind: kindSnooper, Label: snooperTitle + ", all time"},
			Short:       snooperTitle,
		})
	}

	if f.clipper != nil && f.clipper.UserID == userID {
		held = append(held, heldAward{
			besideAward: besideAward{ID: awardClipper, Kind: kindClipper, Label: clipperTitle},
			Short:       clipperTitle,
		})
	}

	if medals, has := f.medals[userID]; has {
		gold, silver, bronze := uniqueCount(medals.Gold), uniqueCount(medals.Silver), uniqueCount(medals.Bronze)
		if months := gold + silver + bronze; months > 0 {
			best, title := 3, "Monthly podium"
			switch {
			case gold > 0:
				best, title = 1, "Monthly champion"
			case silver > 0:
				best = 2
			}

			label, short := title, title
			if gold > 0 {
				label = title + ", " + counted(int64(gold), "month", "months")
			}
			if gold >= 2 {
				short = fmt.Sprintf("%s ×%d", title, gold)
			}

			held = append(held, heldAward{
				besideAward: besideAward{ID: awardPast, Kind: kindPast, Rank: best, Label: label},
				Wins:        gold,
				Months:      months,
				Short:       short,
			})
		}
	}

	for _, fan := range f.fansAsListed(userID) {
		name := "Biggest fan of " + fan.Streamer
		held = append(held, heldAward{
			besideAward: besideAward{ID: awardFanPrefix + fan.StreamerID, Kind: kindFan, Label: name},
			Streamer:    fan.Streamer,
			Hours:       fan.Hours,
			Short:       name,
		})
	}

	for _, kind := range f.kinds {
		tier := tierHeld(f.have[userID], kind.ID)
		if tier < 1 {
			continue
		}

		name := stepName(kind, tier)
		held = append(held, heldAward{
			besideAward: besideAward{ID: awardAchPrefix + kind.ID, Kind: kindAch, Family: kind.ID, Tier: tier, Label: name},
			Short:       name,
		})
	}

	return held
}

// fansAsListed is the streamers somebody is the biggest fan of, in the order
// the fans list has them (by streamer id), which is the order the picker
// always showed them in. A profile lists them by hours: see rows.
func (f *awardFacts) fansAsListed(userID string) []fanView {
	titles := slices.Clone(f.fans[userID])
	sort.SliceStable(titles, func(i, j int) bool { return titles[i].StreamerID < titles[j].StreamerID })

	return titles
}

func tierHeld(have []achievementStep, family string) int {
	for _, step := range have {
		if step.ID == family {
			return step.Tier
		}
	}

	return 0
}

// shownAwards is what is drawn beside a name. Automatic (chose false): the
// old four, whenever held, in their order. A choice: the ones chosen that are
// held now, in the chosen order; one not held now is skipped and kept, so it
// comes back where it was put. Never more than four. The headphones are not
// awards and are not here.
func shownAwards(held []heldAward, choice []string, chose bool) []heldAward {
	wanted := automaticAwards
	if chose {
		wanted = make([]string, 0, maxAwardsShown)
		for _, id := range choice {
			if !slices.Contains(wanted, id) {
				wanted = append(wanted, id)
			}
		}
		if len(wanted) > maxAwardsShown {
			wanted = wanted[:maxAwardsShown]
		}
	}

	var shown []heldAward
	for _, id := range wanted {
		for _, award := range held {
			if award.ID == id {
				shown = append(shown, award)
				break
			}
		}
	}

	return shown
}

func besideOf(shown []heldAward) []besideAward {
	if len(shown) == 0 {
		return nil
	}

	beside := make([]besideAward, len(shown))
	for i, award := range shown {
		beside[i] = award.besideAward

		// Beside a name, the clapper says to a screen reader what the panel
		// has always said there, "Best Clipper, all time", as the snooper
		// does. Its label in the picker (GET /awards/mine) stays "Best
		// Clipper", which is what the panel's picker has always read out.
		if award.Kind == kindClipper {
			beside[i].Label = clipperTitle + ", all time"
		}
	}

	return beside
}

// beside is what goes by somebody's name wherever it appears: a post, a
// member list, a profile's head, beside "Clipped by".
func (f *awardFacts) beside(userID string) []besideAward {
	choice, chose := f.choices[userID]

	return besideOf(shownAwards(f.held(userID, nil), choice, chose))
}

// onCard is a Live now card's set beside the streamer's name. A person who
// chose gets their choice, with the card's own places standing in for the
// boards'. Everybody else, and a card with no person, keep 3.1.0's card set:
// the crown and the calendar at the card's places, and the Top Snooper's
// binoculars on their own Broadcast Box card; never the clapper, which 3.1.0
// did not draw on a card. A 24/7 channel has no places and nobody to hold
// anything.
func (f *awardFacts) onCard(stream apiStream) []besideAward {
	if stream.AlwaysOn {
		return nil
	}

	places := &cardPlaces{allTime: stream.AllTimeRank, month: stream.MonthRank}

	if choice, chose := f.choices[stream.UserID]; chose && stream.UserID != "" {
		return besideOf(shownAwards(f.held(stream.UserID, places), choice, true))
	}

	var set []besideAward
	if places.allTime > 0 {
		set = append(set, besideAward{ID: awardAllTime, Kind: kindAllTime, Rank: places.allTime, Label: ordinal(places.allTime) + " for airtime, all time"})
	}
	if places.month > 0 {
		set = append(set, besideAward{ID: awardMonth, Kind: kindMonth, Rank: places.month, Label: ordinal(places.month) + " for airtime this month"})
	}
	if stream.UserID != "" && stream.Source == "" && f.snooper != nil && f.snooper.UserID == stream.UserID {
		set = append(set, besideAward{ID: awardSnooper, Kind: kindSnooper, Label: snooperTitle + ", all time"})
	}

	return set
}

func newestFirst(months []string) []string {
	unique := slices.Compact(slices.Sorted(slices.Values(months)))
	slices.Reverse(unique)

	return unique
}

// rows is somebody's whole record, as their profile shows it, whatever they
// chose to show beside their name: the places they hold on the boards, the
// titles with one holder, their achievements as one row, and last the months
// already over, since what somebody holds now outranks what they won in the
// spring.
func (f *awardFacts) rows(userID string) []awardRow {
	var rows []awardRow
	if userID == "" {
		return rows
	}

	if row, placed := f.allTime[userID]; placed {
		rows = append(rows, awardRow{Kind: rowBoard, ID: awardAllTime, Rank: row.Rank, Title: podiumTitles[row.Rank], Board: boardAllTime, Seconds: row.Seconds})
	}

	if row, placed := f.month[userID]; placed {
		// The month by name rather than "this month": a profile is read
		// weeks after the fact as often as on the day.
		board := f.monthLabel
		if board == "" {
			board = "This month"
		}
		rows = append(rows, awardRow{Kind: rowBoard, ID: awardMonth, Rank: row.Rank, Title: podiumTitles[row.Rank], Board: board, Seconds: row.Seconds})
	}

	// Gold, as first place is, though nobody else on that board wins
	// anything to be told apart from.
	if f.snooper != nil && f.snooper.UserID == userID {
		rows = append(rows, awardRow{Kind: rowBoard, ID: awardSnooper, Rank: 1, Title: snooperTitle, Board: boardAllTime, Seconds: f.snooper.Seconds, Doing: doingWatching})
	}

	if f.clipper != nil && f.clipper.UserID == userID {
		rows = append(rows, awardRow{Kind: rowClipper, ID: awardClipper, Rank: 1, Title: clipperTitle, Upvotes: f.clipper.Upvotes, Clips: f.clipper.Clips})
	}

	for _, fan := range f.fans[userID] {
		rows = append(rows, awardRow{
			Kind: rowFan, ID: awardFanPrefix + fan.StreamerID, Rank: 1, Title: "Biggest fan of " + fan.Streamer,
			StreamerID: fan.StreamerID, Streamer: fan.Streamer, Hours: fan.Hours,
		})
	}

	var steps []awardStep
	for _, kind := range f.kinds {
		if tier := tierHeld(f.have[userID], kind.ID); tier > 0 {
			steps = append(steps, awardStep{ID: kind.ID, Tier: tier, Name: stepName(kind, tier)})
		}
	}
	if len(steps) > 0 {
		rows = append(rows, awardRow{Kind: rowAchievements, ID: rowIDAchievements, Title: "Achievements", Steps: steps})
	}

	if medals, has := f.medals[userID]; has {
		gold := newestFirst(medals.Gold)

		var tallies []awardTally
		if silver := newestFirst(medals.Silver); len(silver) > 0 {
			tallies = append(tallies, awardTally{Rank: 2, Months: silver})
		}
		if bronze := newestFirst(medals.Bronze); len(bronze) > 0 {
			tallies = append(tallies, awardTally{Rank: 3, Months: bronze})
		}

		switch {
		case len(gold) > 0:
			rows = append(rows, awardRow{Kind: rowMonths, ID: awardPast, Rank: 1, Title: "Monthly champion", Gold: gold, Tallies: tallies})
		case len(tallies) > 0:
			rows = append(rows, awardRow{Kind: rowMonths, ID: awardPast, Rank: tallies[0].Rank, Title: "Monthly podium", Tallies: tallies})
		}
	}

	return rows
}
