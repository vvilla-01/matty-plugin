package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Achievements (design/3.2.0.md §5): badges for milestones, in five families.
// Four are built in and worked out at every refresh from figures VillaNET
// already has in hand while it builds the snapshot: hours streamed, by person,
// as the airtime boards count them; hours watching other people's streams, as
// the Snoopers board shows them; months ended on a podium, from the medal
// tally; and thumbs up on clips, as Best Clipper counts them, banked ones
// included. Nothing is stored for those four, so they are backdated — the
// first refresh after 3.2.0 gives everybody every step their hours already
// reach — and they go back down when the figure does, as the boards do, which
// is also what keeps "Twitch hours are deletable" true. The fifth family is
// the admin's: hours on a 24/7 channel (chanwatch.go), counted from 3.2.0 on.
//
// Only people linked now appear, as on every board. Nothing is posted or sent
// to anybody: the earner's own panel says so with a quiet toast, and the rest
// see the badges where they look for them.

const (
	achStreamed = "streamed"
	achSnooped  = "snooped"
	achPodium   = "podium"
	achThumbs   = "thumbs"

	channelAchPrefix = "ch-"

	unitHours  = "hours"
	unitMonths = "months"
	unitThumbs = "thumbs"

	// The 24/7 channel setting's bounds (§5.5).
	maxChannelAchievementLines = 8
	maxChannelAchievementName  = 32
	maxChannelAchievementSteps = 8
	maxChannelAchievementHours = 100000

	// A bound on the snapshot, which every connection receives.
	maxAchievementHolders = 500
)

// achievementKind is one family as the snapshot carries it: its id, its name
// (a built-in family's own, or the channel's as the admin wrote it), what it
// counts, and its steps, lowest first.
type achievementKind struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Unit  string  `json:"unit"`
	Tiers []int64 `json:"tiers"`
}

// achievementStep is how many steps of a family somebody has reached.
type achievementStep struct {
	ID   string `json:"id"`
	Tier int    `json:"tier"`
}

// achievementHolder is one person's achievements, with their names as the
// boards name them, which the Achievements tab needs (an approved departure
// from §5.3, made with Victor's P4 pick).
type achievementHolder struct {
	UserID      string            `json:"userId"`
	DisplayName string            `json:"displayName,omitempty"`
	Username    string            `json:"username,omitempty"`
	Have        []achievementStep `json:"have"`
}

// builtInAchievements is the catalogue's fixed part, in its order.
var builtInAchievements = []achievementKind{
	{ID: achStreamed, Name: "Streamed", Unit: unitHours, Tiers: []int64{10, 50, 100, 250, 500, 1000}},
	{ID: achSnooped, Name: "Snooped", Unit: unitHours, Tiers: []int64{10, 50, 100, 250, 500}},
	{ID: achPodium, Name: "Podium", Unit: unitMonths, Tiers: []int64{1, 3, 6, 12}},
	{ID: achThumbs, Name: "Thumbs up", Unit: unitThumbs, Tiers: []int64{10, 50, 100, 250}},
}

// channelAchievement is one good line of 24/7 channel achievements. key is
// the normalised stream key, which never leaves the server.
type channelAchievement struct {
	key   string
	id    string
	name  string
	tiers []int64
}

// parseChannelAchievements reads the 24/7 channel achievements setting: one
// line per channel, "key = Name = 1, 10, 50", at most eight. A line that is
// wrong is left out and said in a sentence of its own, by its line number as
// the admin sees it; a # comments a line out. idOf makes a family's id from
// its key.
func parseChannelAchievements(setting string, alwaysOn map[string]bool, idOf func(key string) string) ([]channelAchievement, []string) {
	var families []channelAchievement
	var faults []string
	seen := map[string]bool{}
	counted := 0

	fault := func(line int, why string) {
		faults = append(faults, fmt.Sprintf("24/7 channel achievements, line %d: %s", line, why))
	}

	for index, line := range strings.Split(setting, "\n") {
		number := index + 1
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		counted++
		if counted > maxChannelAchievementLines {
			fault(number, "only 8 lines are read.")
			continue
		}

		parts := strings.Split(line, "=")
		if len(parts) != 3 {
			fault(number, "needs a key, a name and the hours.")
			continue
		}

		key := normalizeKey(parts[0])
		name := strings.TrimSpace(parts[1])
		if key == "" || name == "" || strings.TrimSpace(parts[2]) == "" {
			fault(number, "needs a key, a name and the hours.")
			continue
		}

		if !alwaysOn[key] {
			fault(number, "that key is not one of the 24/7 stream keys.")
			continue
		}

		if utf8.RuneCountInString(name) > maxChannelAchievementName {
			fault(number, "the name is too long (32 at most).")
			continue
		}

		tiers, ok := channelAchievementSteps(parts[2])
		if !ok {
			fault(number, "the hours must go up, from 1 to 100000, at most 8 of them.")
			continue
		}

		if seen[key] {
			fault(number, "that key already has a line.")
			continue
		}
		seen[key] = true

		families = append(families, channelAchievement{key: key, id: idOf(key), name: name, tiers: tiers})
	}

	return families, faults
}

// channelAchievementSteps reads "1, 10, 50": whole hours, going up, each from
// 1 to 100000, one to eight of them.
func channelAchievementSteps(text string) ([]int64, bool) {
	var tiers []int64

	for _, field := range strings.Split(text, ",") {
		hours, err := strconv.ParseInt(strings.TrimSpace(field), 10, 64)
		if err != nil || hours < 1 || hours > maxChannelAchievementHours {
			return nil, false
		}
		if len(tiers) > 0 && hours <= tiers[len(tiers)-1] {
			return nil, false
		}

		tiers = append(tiers, hours)
	}

	if len(tiers) == 0 || len(tiers) > maxChannelAchievementSteps {
		return nil, false
	}

	return tiers, true
}

// channelAchievementID is a channel family's id: "ch-" and the first twelve
// hex of an HMAC of the key under the handle secret, behind a prefix no stream
// key can have, so it is stable, never equals a stream handle, and says
// nothing about the key.
func (p *Plugin) channelAchievementID(key string) string {
	mac := hmac.New(sha256.New, p.handleSecret)
	mac.Write([]byte("achievement\x00" + key))

	return channelAchPrefix + hex.EncodeToString(mac.Sum(nil))[:12]
}

// channelAchievements is the setting as read now, and its faults.
func (p *Plugin) channelAchievements(cfg *configuration) ([]channelAchievement, []string) {
	return parseChannelAchievements(cfg.ChannelAchievements, cfg.alwaysOnSet(), p.channelAchievementID)
}

// achievementCatalogue is the families that are on, in the catalogue's order:
// the built-in ones whose own switch is on, then the channels, while
// achievements are on at all.
func (p *Plugin) achievementCatalogue(cfg *configuration) []achievementKind {
	if !cfg.EnableAchievements {
		return nil
	}

	on := map[string]bool{
		achStreamed: cfg.EnableLeaderboard,
		achSnooped:  cfg.TopWatchers,
		achPodium:   cfg.EnableLeaderboard,
		achThumbs:   cfg.EnableClips,
	}

	var kinds []achievementKind
	for _, kind := range builtInAchievements {
		if on[kind.ID] {
			kinds = append(kinds, kind)
		}
	}

	if cfg.TopWatchers {
		families, _ := p.channelAchievements(cfg)
		for _, family := range families {
			kinds = append(kinds, achievementKind{ID: family.id, Name: family.name, Unit: unitHours, Tiers: family.tiers})
		}
	}

	return kinds
}

// achievementFamilyIDs is every family an award choice may name: the four
// built in, whatever their switches say, and every channel with a good line
// now. A choice may be made ahead; one not held is skipped until it is.
func (p *Plugin) achievementFamilyIDs(cfg *configuration) map[string]bool {
	ids := map[string]bool{}
	for _, kind := range builtInAchievements {
		ids[kind.ID] = true
	}

	families, _ := p.channelAchievements(cfg)
	for _, family := range families {
		ids[family.id] = true
	}

	return ids
}

// achievementsAlertFor is /me's admin-only line about the 24/7 channel
// setting, or "" when every line is good or achievements are off.
func (p *Plugin) achievementsAlertFor(cfg *configuration) string {
	if !cfg.EnableAchievements {
		return ""
	}

	_, faults := p.channelAchievements(cfg)

	return strings.Join(faults, " ")
}

// achievementFigures is what achievements are worked out from, kept in memory
// only and swapped whole at each refresh: seconds streamed and watched, months
// on a podium and hours on each channel family, by user id; who of the people
// holding a step is linked now; and their names.
type achievementFigures struct {
	streamed map[string]int64
	snooped  map[string]int64
	podium   map[string]int64
	channels map[string]map[string]int64
	thumbs   map[string]int64
	linked   map[string]bool
	names    map[string]achievementName
}

type achievementName struct {
	display  string
	username string
}

// noteStreamedFigures keeps the all-time board's rows by person, before the
// fifty-row cut, for the Streamed family: exactly the hours the board counts,
// Twitch's included while Twitch live is on.
func (p *Plugin) noteStreamedFigures(rows []boardRow) {
	streamed := map[string]int64{}
	for _, row := range rows {
		if row.user != nil && row.user.Id != "" {
			streamed[row.user.Id] += row.seconds
		}
	}

	p.achievementsLock.Lock()
	p.achievementFacts.streamed = streamed
	p.achievementsLock.Unlock()
}

// noteSnoopedFigures keeps the watching table as the Snoopers board was just
// built from it: hourly with faces off.
func (p *Plugin) noteSnoopedFigures(totals statTable) {
	snooped := make(map[string]int64, len(totals))
	for userID, entry := range totals {
		if entry.Seconds > 0 {
			snooped[userID] = entry.Seconds
		}
	}

	p.achievementsLock.Lock()
	p.achievementFacts.snooped = snooped
	p.achievementsLock.Unlock()
}

// thumbsFigures is everybody's Best Clipper standing in counted upvotes,
// banked ones included, from this node's copy of the clips document: no read.
func (p *Plugin) thumbsFigures() map[string]int64 {
	s := p.clips()
	doc := s.cached()
	if doc == nil {
		return nil
	}

	counts := s.verdictsFor(clipPeopleOf(doc), doc.Blocked).eligible
	thumbs := map[string]int64{}
	for userID, score := range clipperScores(doc, counts) {
		if score.up > 0 {
			thumbs[userID] = int64(score.up)
		}
	}

	return thumbs
}

// valueOf is somebody's figure for a family, in its own unit, rounded down.
func (f *achievementFigures) valueOf(kind achievementKind, userID string) int64 {
	switch kind.ID {
	case achStreamed:
		return f.streamed[userID] / 3600
	case achSnooped:
		return f.snooped[userID] / 3600
	case achPodium:
		return f.podium[userID]
	case achThumbs:
		return f.thumbs[userID]
	default:
		return f.channels[kind.ID][userID] / 3600
	}
}

// tierOf is how many of a family's steps a figure reaches.
func tierOf(kind achievementKind, value int64) int {
	tier := 0
	for _, step := range kind.Tiers {
		if value >= step {
			tier++
		}
	}

	return tier
}

// refreshAchievementFigures gathers this poll's figures: the months on a
// podium from the medal tally just built, the channel hours as shown, the
// thumbs; and for everybody who reaches a step of anything, whether they are
// linked now and what they are called, asked once per person per poll with
// the boards' own answers. Nothing is gathered while achievements are off.
func (p *Plugin) refreshAchievementFigures(current *snapshot, cfg *configuration, now time.Time, people *pollPeople) {
	p.achievementsLock.Lock()
	facts := p.achievementFacts
	p.achievementsLock.Unlock()

	if !cfg.EnableAchievements {
		p.achievementsLock.Lock()
		p.achievementFacts = achievementFigures{}
		p.achievementsLock.Unlock()

		return
	}

	if !cfg.EnableLeaderboard {
		facts.streamed = nil
	}
	if !cfg.TopWatchers {
		facts.snooped = nil
	}

	facts.podium = map[string]int64{}
	if cfg.EnableLeaderboard {
		for _, medals := range current.MonthMedals {
			facts.podium[medals.UserID] = int64(len(medals.Gold) + len(medals.Silver) + len(medals.Bronze))
		}
	}

	facts.channels = map[string]map[string]int64{}
	if cfg.TopWatchers {
		families, _ := p.channelAchievements(cfg)
		if len(families) > 0 {
			if table, ok := p.channelHoursShown(cfg, now); ok {
				byKey := map[string]string{}
				for _, family := range families {
					byKey[family.key] = family.id
				}

				for key, entry := range table {
					streamKey, viewer, ok := splitChanwatchKey(key)
					id := byKey[streamKey]
					if !ok || id == "" || entry.Seconds <= 0 {
						continue
					}
					if facts.channels[id] == nil {
						facts.channels[id] = map[string]int64{}
					}
					facts.channels[id][viewer] += entry.Seconds
				}
			} else {
				// Not readable just now: the channel hours shown last time
				// stand, rather than everybody's badges going.
				p.achievementsLock.Lock()
				facts.channels = p.achievementFacts.channels
				p.achievementsLock.Unlock()
			}
		}
	}

	if cfg.EnableClips {
		facts.thumbs = p.thumbsFigures()
	} else {
		facts.thumbs = nil
	}

	if people == nil {
		people = p.newPollPeople()
	}

	kinds := p.achievementCatalogue(cfg)
	candidates := map[string]bool{}
	for _, values := range []map[string]int64{facts.streamed, facts.snooped, facts.podium, facts.thumbs} {
		for userID := range values {
			candidates[userID] = true
		}
	}
	for _, values := range facts.channels {
		for userID := range values {
			candidates[userID] = true
		}
	}

	facts.linked = map[string]bool{}
	facts.names = map[string]achievementName{}
	for userID := range candidates {
		holds := false
		for _, kind := range kinds {
			if tierOf(kind, facts.valueOf(kind, userID)) > 0 {
				holds = true
				break
			}
		}
		if !holds || !people.linked(userID) {
			continue
		}

		user := people.resolve.person(userID)
		if user == nil || user.DeleteAt != 0 {
			continue
		}

		facts.linked[userID] = true
		facts.names[userID] = achievementName{display: displayNameOf(user), username: user.Username}
	}

	p.achievementsLock.Lock()
	p.achievementFacts = facts
	p.achievementsLock.Unlock()
}

// addAchievements puts the catalogue and everybody's steps on a snapshot,
// from the figures the last poll gathered and the thumbs as this node's clips
// document has them now, so a vote shows without a poll. Every publish path
// calls it, and it reads nothing from the store.
func (p *Plugin) addAchievements(current *snapshot, cfg *configuration) {
	current.AchievementKinds, current.Achievements = nil, nil

	kinds := p.achievementCatalogue(cfg)
	if len(kinds) == 0 {
		return
	}
	current.AchievementKinds = kinds

	p.achievementsLock.Lock()
	facts := p.achievementFacts
	p.achievementsLock.Unlock()

	if cfg.EnableClips {
		facts.thumbs = p.thumbsFigures()
	}

	holders := make([]achievementHolder, 0, len(facts.linked))
	for userID := range facts.linked {
		var have []achievementStep
		for _, kind := range kinds {
			if tier := tierOf(kind, facts.valueOf(kind, userID)); tier > 0 {
				have = append(have, achievementStep{ID: kind.ID, Tier: tier})
			}
		}
		if len(have) == 0 {
			continue
		}

		name := facts.names[userID]
		holders = append(holders, achievementHolder{UserID: userID, DisplayName: name.display, Username: name.username, Have: have})
	}

	sort.Slice(holders, func(i, j int) bool { return holders[i].UserID < holders[j].UserID })
	if len(holders) > maxAchievementHolders {
		holders = holders[:maxAchievementHolders]
	}

	if len(holders) > 0 {
		current.Achievements = holders
	}
}

// achievementProgress is one family as GET /achievements/mine tells it: the
// reader's figure in its own unit, rounded down, and the next step, or 0 once
// every step is reached.
type achievementProgress struct {
	ID    string `json:"id"`
	Value int64  `json:"value"`
	Next  int64  `json:"next"`
}

type achievementsMineResponse struct {
	Progress []achievementProgress `json:"progress"`
}

// handleAchievementsMine is GET /api/v1/achievements/mine, for linked people:
// their own progress along every family that is on, from memory. Per reader,
// so never cached.
func (p *Plugin) handleAchievementsMine(w http.ResponseWriter, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	cfg := p.getConfiguration()
	kinds := p.achievementCatalogue(cfg)

	p.achievementsLock.Lock()
	facts := p.achievementFacts
	p.achievementsLock.Unlock()

	if cfg.EnableClips {
		facts.thumbs = p.thumbsFigures()
	}

	progress := make([]achievementProgress, 0, len(kinds))
	for _, kind := range kinds {
		value := facts.valueOf(kind, userID)

		next := int64(0)
		for _, step := range kind.Tiers {
			if value < step {
				next = step
				break
			}
		}

		progress = append(progress, achievementProgress{ID: kind.ID, Value: value, Next: next})
	}

	writeJSON(w, http.StatusOK, achievementsMineResponse{Progress: progress})
}
