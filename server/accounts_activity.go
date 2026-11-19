package main

import (
	"encoding/json"
	"regexp"
	"strings"
)

// What somebody is doing, in one line under their name: "Playing ELDEN RING",
// or "Streaming ELDEN RING" when their own stream is of that very game, or
// "Streaming Quantum of Solace" when they are live and have said in What's on?
// that a movie or a show is on.
//
// Decided here, once, for both clients. It takes the game Steam reports, which
// only a reader who may watch is given, and what the card that stands for the
// person on the air says it is showing (What's on?, or Twitch's category),
// which is in the snapshot. Until 3.5.0 only Matty put the two together, by
// comparing names it had tidied itself, and the web panel could not have said
// "Streaming" without growing a second copy of that. The server holds both
// sides, and a better key than a name: Steam says which app it is, and a
// What's on? picked from Steam is that same number.
//
// It rides GET /api/v1/accounts and nothing else. Whatever is derived from
// Steam must never be in the snapshot, which reaches locked readers too, and a
// line that is sometimes Steam's cannot be split from the times it is not
// without saying which those are. So a locked reader is told nothing of what
// anybody is doing, as before, and the snapshot is the same for everybody.

// accountsViewActivity is the line. Title is "" only with kind "playing", for
// a game Steam does not name.
type accountsViewActivity struct {
	Kind   string `json:"kind"`   // "playing" | "streaming"
	Title  string `json:"title"`  // the game, or the movie or show What's on? names
	Source string `json:"source"` // who names it: "steam" | "panel" (What's on?) | "twitch"
}

const (
	activityPlaying   = "playing"
	activityStreaming = "streaming"

	activityFromSteam  = "steam"
	activityFromPanel  = "panel"
	activityFromTwitch = "twitch"
)

var (
	// What the same game's name differs by between Steam ("DARK SOULS™ III")
	// and Twitch ("Dark Souls III"), besides its capitals.
	gameNameMarks  = regexp.MustCompile(`[™®©℠]`)
	gameNameSpaces = regexp.MustCompile(`\s+`)
)

// steamAppIDOf is the app id in GetPlayerSummaries' gameid, which comes as a
// string or as a number: its digits, or "" for none or for anything else. An
// app id is what steamAppIDPattern (in igdb.go) takes, at most ten digits, the
// same rule a title's own "steam:<id>" is read by. A shortcut to a game that
// is not Steam's has an id past 2^63, which is no app id: it is "" here, so
// that such a game is told from a stream's by its name, never by an id that
// only looks like one.
func steamAppIDOf(raw json.RawMessage) string {
	id := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if id == "0" || !steamAppIDPattern.MatchString(id) {
		return ""
	}

	return id
}

// sameGameName is whether two names are one game's, as two services spell it.
// Never for no name.
func sameGameName(a, b string) bool {
	tidy := func(name string) string {
		return strings.TrimSpace(gameNameSpaces.ReplaceAllString(gameNameMarks.ReplaceAllString(name, ""), " "))
	}

	a, b = tidy(a), tidy(b)

	return a != "" && strings.EqualFold(a, b)
}

// onAirGame is what the card that stands for somebody on the air says is on:
// what its What's on? names, a game or not, and the Twitch categories it goes
// by.
type onAirGame struct {
	// tagged: the card carries a What's on? (or the title of the plan that
	// is on). game: it says a game, and not a movie or a show.
	tagged bool
	game   bool

	// What What's on? names, a game or a movie or a show: the title looked
	// up when one was picked, else the words as typed. And Steam's app id
	// for a game picked from Steam's own list ("steam:<appid>").
	said  string
	appID string

	// Twitch's category for the card, and for any Twitch entry the card
	// stands in for, in the order the snapshot lists them.
	categories []string
}

// onAirGames is that, for everybody on the air, from a snapshot's streams
// alone. Whose card is whose is the people index's rule (liveHandles), so
// this can never name a card the index does not.
func onAirGames(streams []apiStream) map[string]onAirGame {
	games := map[string]onAirGame{}

	for userID, handle := range liveHandles(streams) {
		var said onAirGame

		for i := range streams {
			stream := &streams[i]

			if stream.Handle == handle {
				if now := stream.NowPlaying; now != nil {
					said.tagged = true
					said.game = now.Category == "game"
					said.said = strings.TrimSpace(now.Title)
					if now.Media != nil {
						if title := strings.TrimSpace(now.Media.Title); title != "" {
							said.said = title
						}
						if id, fromSteam := strings.CutPrefix(now.Media.Ref, "steam:"); fromSteam && said.game && steamAppIDPattern.MatchString(id) {
							said.appID = id
						}
					}
				}
			}

			if stream.Handle == handle || stream.CoveredBy == handle {
				if category := strings.TrimSpace(stream.TwitchCategory); category != "" {
					said.categories = append(said.categories, category)
				}
			}
		}

		if said.tagged || len(said.categories) > 0 {
			games[userID] = said
		}
	}

	return games
}

// isOf is whether the card is of this very game, the one Steam reports.
//
// By Steam's app id where both sides have one: a What's on? picked from Steam
// against the app Steam says is running is the same number or it is not the
// same game, whatever the two are called. By name otherwise: a What's on?
// from IGDB or typed by hand, and Twitch's category, which has no id of
// Steam's.
func (g onAirGame) isOf(title, appID string) bool {
	if g.game {
		if g.appID != "" && appID != "" {
			if g.appID == appID {
				return true
			}
		} else if sameGameName(g.said, title) {
			return true
		}
	}

	for _, category := range g.categories {
		if sameGameName(category, title) {
			return true
		}
	}

	return false
}

// notAGame is the movie or show (or anything else that is not a game) the
// card's What's on? names, when it names one.
func (g onAirGame) notAGame() (title string, ok bool) {
	return g.said, g.tagged && !g.game && g.said != ""
}

// shown is the game the card itself names, and who names it: What's on?
// first, then Twitch's category. Nothing when What's on? says it is something
// else (a movie, which activityOf has already shown when it has a title): the
// category is then not asked, since the streamer has said what is on.
func (g onAirGame) shown() (title, source string, ok bool) {
	if g.tagged {
		if !g.game {
			return "", "", false
		}
		if g.said != "" {
			return g.said, activityFromPanel, true
		}
	}

	if len(g.categories) > 0 {
		return g.categories[0], activityFromTwitch, true
	}

	return "", "", false
}

// activityOf is the line for one person.
//
//   - Live, with a movie or a show named in What's on?: "Streaming" that,
//     whatever game Steam reports, since the streamer has said what is on.
//   - In a game Steam names: that game. "Streaming" only when their card is
//     of that very game, else "Playing".
//   - No game named by Steam, but their card says which game it is:
//     "Streaming" that.
//   - In a game Steam does not name: "Playing", with no title.
//
// inGame is whether Steam reports a game at all, for a profile that is public
// and a read that is fresh; the caller decides both.
func activityOf(inGame bool, title, appID string, onAir onAirGame, live bool) *accountsViewActivity {
	if live {
		if said, ok := onAir.notAGame(); ok {
			return &accountsViewActivity{Kind: activityStreaming, Title: said, Source: activityFromPanel}
		}
	}

	if inGame && title != "" {
		kind := activityPlaying
		if live && onAir.isOf(title, appID) {
			kind = activityStreaming
		}

		return &accountsViewActivity{Kind: kind, Title: title, Source: activityFromSteam}
	}

	if live {
		if said, source, ok := onAir.shown(); ok {
			return &accountsViewActivity{Kind: activityStreaming, Title: said, Source: source}
		}
	}

	if inGame {
		return &accountsViewActivity{Kind: activityPlaying, Title: "", Source: activityFromSteam}
	}

	return nil
}

// noteOnAir is told what every card says, each time a snapshot is stored
// (publish). When that differs from what the accounts answer was last built
// from, the answer is built again, and everybody is told to read it again if
// it came out different: a game or a movie picked in What's on?, or a stream
// starting, changes "Playing" to "Streaming" without Steam saying anything
// new.
//
// seq is the stored snapshot's number (publish). A snapshot older than the
// one last told is ignored: the newer one was stored after it, and is what
// the snapshot says now.
func (s *accountsStore) noteOnAir(seq uint64, streams []apiStream) {
	games := onAirGames(streams)
	key := onAirKey(games)

	s.lock.Lock()
	if seq < s.onAirSeq {
		s.lock.Unlock()

		return
	}
	s.onAirSeq = seq
	same := key == s.onAirKey

	// Somebody marked deactivated who has just come on the air may have been
	// let back in since: Mattermost tells nobody when an account is
	// reactivated, and with both services off no step will ever look again.
	returning := map[string]bool{}
	for userID := range games {
		if _, before := s.onAir[userID]; !before && s.inactive[userID] {
			returning[userID] = true
		}
	}

	s.onAir, s.onAirKey = games, key
	s.lock.Unlock()

	if len(returning) > 0 && s.lookUpReturning(returning) {
		same = false
	}
	if !same {
		s.rebuildView(s.plugin.getConfiguration(), s.now())
	}
}

// lookUpReturning asks Mattermost about people marked deactivated who have
// come on the air, and lifts the mark from each one active again: one lookup
// as a stream starts, never one for every snapshot. Whether any mark was
// lifted. When Mattermost does not answer, the marks stand.
func (s *accountsStore) lookUpReturning(userIDs map[string]bool) bool {
	users, appErr := s.plugin.API.GetUsersByIds(sortedIDs(userIDs))
	if appErr != nil {
		s.warnOnce("accounts-users-on-air", "Could not look up the Mattermost accounts of people who came on the air; somebody deactivated before stays left out of what people are doing until the next look.")
		return false
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	lifted := false
	for _, user := range users {
		if user != nil && user.DeleteAt == 0 && s.inactive[user.Id] {
			delete(s.inactive, user.Id)
			lifted = true
		}
	}

	return lifted
}

// onAirKey is what the cards say, as one string that is the same whenever
// they say the same.
func onAirKey(games map[string]onAirGame) string {
	var key strings.Builder
	for _, userID := range sortedIDs(games) {
		game := games[userID]
		key.WriteString(userID)
		key.WriteByte(0)
		if game.tagged {
			key.WriteByte('t')
		}
		if game.game {
			key.WriteByte('g')
		}
		key.WriteByte(0)
		key.WriteString(game.said)
		key.WriteByte(0)
		key.WriteString(game.appID)
		for _, category := range game.categories {
			key.WriteByte(0)
			key.WriteString(category)
		}
		key.WriteByte(1)
	}

	return key.String()
}

// addActivitiesLocked puts each person's line into the view: for everybody in
// it already (the people connected to Steam or Hytale), and for anybody on
// the air whose card names what is on, connected to anything or not. With lock
// held. steamGames is each person's game by Steam, for those in one.
func (s *accountsStore) addActivitiesLocked(view *accountsView, steamGames map[string]steamGame) {
	for userID, person := range view.People {
		game, inGame := steamGames[userID]
		onAir, live := s.onAir[userID]
		if person.Activity = activityOf(inGame, game.title, game.appID, onAir, live); person.Activity != nil {
			view.People[userID] = person
		}
	}

	for userID, onAir := range s.onAir {
		if _, there := view.People[userID]; there || s.inactive[userID] {
			continue
		}
		if activity := activityOf(false, "", "", onAir, true); activity != nil {
			view.People[userID] = accountsViewPerson{Activity: activity}
		}
	}
}

// steamGame is the game Steam reports somebody in: its name, which may be "",
// and Steam's id for it.
type steamGame struct {
	title string
	appID string
}
