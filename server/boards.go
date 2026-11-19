package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
)

// The boards (contract/README.md, "Awards and the boards").
//
// Until 3.6.0 the four boards, the medals, the fans and the achievements rode
// the snapshot, which goes to every connection whenever anything in it
// changes. A board's seconds tick for as long as anybody is on the air or
// being watched, so for all of that time the whole snapshot went out once a
// minute: every card, the plans and the voice roster, to every connection,
// to say that a figure nobody was looking at had grown.
//
// They are a document of their own now, GET /api/v1/boards, which a client
// reads when somebody looks at a board or a profile, and again a minute later
// if they are still looking. What everybody needs at once without looking,
// which award goes beside whose name, is decided here (awards_decide.go) and
// stays in the snapshot, where it changes only when a place does.
//
// The snapshot says in one word, boards.version, when anything in this
// document has changed other than a board's seconds: a place, a medal, a fan,
// a step, the Best Clipper's score. A client holding a copy reads again when
// that word moves, so nothing but the seconds is ever behind, and those are
// read fresh whenever somebody looks.

// boardsView is GET /api/v1/boards. The boards and lists are as the snapshot
// carried them, under the names it gave them; people is new.
type boardsView struct {
	// The same word the snapshot carries in boards.version.
	Version string `json:"version"`

	AllTime    []leaderEntry `json:"allTime,omitempty"`
	Month      []leaderEntry `json:"month,omitempty"`
	MonthLabel string        `json:"monthLabel,omitempty"`

	// How many streamers the boards actually have, so a client can say when
	// the row cap has hidden some rather than looking like it lost them.
	AllTimeTotal int `json:"allTimeTotal,omitempty"`
	MonthTotal   int `json:"monthTotal,omitempty"`

	// Who finished on the podium of a month that has ended, and which
	// months: see monthmedals.go.
	MonthMedals []monthMedals `json:"monthMedals,omitempty"`

	// The Top Snoopers boards, and whether they are on and could be read, so
	// a client can tell "nobody has watched yet" from "not shown": see
	// watchtime.go.
	WatchBoards       bool          `json:"watchBoards,omitempty"`
	WatchMonth        []leaderEntry `json:"watchMonth,omitempty"`
	WatchAllTime      []leaderEntry `json:"watchAllTime,omitempty"`
	WatchMonthTotal   int           `json:"watchMonthTotal,omitempty"`
	WatchAllTimeTotal int           `json:"watchAllTimeTotal,omitempty"`

	// Who holds Best Clipper, while Clips is on and somebody does.
	BestClipper *bestClipperView `json:"bestClipper,omitempty"`

	// Each linked streamer's biggest fan, sorted by streamer id, and whether
	// biggest fans are on: see fans.go.
	Fans   []fanView `json:"fans,omitempty"`
	FansOn bool      `json:"fansOn,omitempty"`

	// The achievement families that are on, in catalogue order, and who
	// holds which step of each, sorted by user id: see achievements.go.
	AchievementKinds []boardsKind        `json:"achievementKinds,omitempty"`
	Achievements     []achievementHolder `json:"achievements,omitempty"`

	// Everybody's record as their profile shows it, by user id, for
	// everybody who holds anything. Always there, {} when nobody does.
	People map[string]boardsPerson `json:"people"`
}

type boardsPerson struct {
	Rows []awardRow `json:"rows"`
}

// boardsKind is an achievement family as the boards carry it: the
// catalogue's entry, and what each of its steps is called, lowest first, so
// that a client showing the whole ladder words none of it itself.
type boardsKind struct {
	achievementKind

	Names []string `json:"names"`
}

func boardsKindsOf(kinds []achievementKind) []boardsKind {
	if len(kinds) == 0 {
		return nil
	}

	named := make([]boardsKind, len(kinds))
	for i, kind := range kinds {
		names := make([]string, len(kind.Tiers))
		for step := range kind.Tiers {
			names[step] = stepName(kind, step+1)
		}
		named[i] = boardsKind{achievementKind: kind, Names: names}
	}

	return named
}

// boardsRef is what the snapshot says of the boards: only when to read them
// again.
type boardsRef struct {
	Version string `json:"version"`
}

// decideAwards applies the rules to a snapshot that is about to be stored:
// each card's badges, what goes beside each person's name, and the boards as
// their own document with everybody's record. The snapshot is told the
// document's version. Called by publish alone, after the people are indexed,
// so every snapshot that is stored has all of it as of that moment.
func decideAwards(current *snapshot) *boardsView {
	facts := awardFactsOf(current)

	// On a copy of the list: the one handed in may share its array with a
	// snapshot readers hold.
	if len(current.Streams) > 0 {
		streams := make([]apiStream, len(current.Streams))
		copy(streams, current.Streams)
		for i := range streams {
			streams[i].Awards = facts.onCard(streams[i])
		}
		current.Streams = streams
	}

	view := &boardsView{
		AllTime:           current.AllTime,
		Month:             current.Month,
		MonthLabel:        current.MonthLabel,
		AllTimeTotal:      current.AllTimeTotal,
		MonthTotal:        current.MonthTotal,
		MonthMedals:       current.MonthMedals,
		WatchBoards:       current.WatchBoards,
		WatchMonth:        current.WatchMonth,
		WatchAllTime:      current.WatchAllTime,
		WatchMonthTotal:   current.WatchMonthTotal,
		WatchAllTimeTotal: current.WatchAllTimeTotal,
		BestClipper:       current.BestClipper,
		Fans:              current.Fans,
		FansOn:            current.FansOn,
		AchievementKinds:  boardsKindsOf(current.AchievementKinds),
		Achievements:      current.Achievements,
		People:            map[string]boardsPerson{},
	}

	if current.People == nil {
		current.People = map[string]personView{}
	}
	for _, userID := range facts.holders() {
		if beside := facts.beside(userID); len(beside) > 0 {
			person := current.People[userID]
			person.Awards = beside
			current.People[userID] = person
		}
		if rows := facts.rows(userID); len(rows) > 0 {
			view.People[userID] = boardsPerson{Rows: rows}
		}
	}

	view.Version = boardsVersion(view)
	current.Boards = &boardsRef{Version: view.Version}

	return view
}

// boardsVersion is the word that moves when anything in the boards does but
// a figure that only ticks: the seconds on the four boards, and on the rows
// made from them. A row that appears, leaves, changes place or changes name
// is a different board; so is a medal, a fan and their whole hours, a step,
// or a vote on the Best Clipper's clips.
func boardsVersion(view *boardsView) string {
	still := *view
	still.Version = ""
	still.AllTime = withoutSeconds(view.AllTime)
	still.Month = withoutSeconds(view.Month)
	still.WatchAllTime = withoutSeconds(view.WatchAllTime)
	still.WatchMonth = withoutSeconds(view.WatchMonth)

	still.People = make(map[string]boardsPerson, len(view.People))
	for userID, person := range view.People {
		rows := slices.Clone(person.Rows)
		for i := range rows {
			rows[i].Seconds = 0
		}
		still.People[userID] = boardsPerson{Rows: rows}
	}

	raw, err := json.Marshal(still)
	if err != nil {
		return ""
	}

	return shortSum(raw)
}

func withoutSeconds(rows []leaderEntry) []leaderEntry {
	still := slices.Clone(rows)
	for i := range still {
		still[i].Seconds, still[i].TwitchSeconds = 0, 0
	}

	return still
}

// shortSum is a fingerprint short enough to be a tag.
func shortSum(raw []byte) string {
	sum := sha256.Sum256(raw)

	return hex.EncodeToString(sum[:8])
}

// handleBoards answers GET /api/v1/boards from what the last publish stored:
// no read per request. Conditional, like the snapshot: a reader who sends the
// tag of the copy in hand is told "that one still" whenever not even a second
// has moved, which is whenever nobody is on the air or being watched.
func (p *Plugin) handleBoards(w http.ResponseWriter, r *http.Request) {
	p.snapshotLock.RLock()
	body, tag := p.boardsJSON, p.boardsTag
	p.snapshotLock.RUnlock()

	// The same for everybody who may ask, and kept by the reader only to be
	// checked each time, as the snapshot is.
	w.Header().Set("Cache-Control", "private, no-cache")

	if unchangedSince(w, r, tag) {
		return
	}

	if body == nil {
		// Nothing published yet, as after a restart: a reader who holds a
		// copy keeps it, since an empty document would only be replaced by
		// the first publish's, which is very likely that same copy.
		if r.Header.Get("If-None-Match") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		writeJSON(w, http.StatusOK, &boardsView{People: map[string]boardsPerson{}})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
