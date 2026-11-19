package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// "Playing" or "Streaming", decided on the server (accounts_activity.go): the
// rule itself first, then where it is sent and to whom.

// liveCard is somebody's live Broadcast Box card, saying what What's on? says.
func liveCard(userID, handle string, now *apiNowPlaying) apiStream {
	return apiStream{Handle: handle, UserID: userID, LinkSource: linkSourceManual, NowPlaying: now}
}

func saysGame(title, ref string) *apiNowPlaying {
	now := &apiNowPlaying{Category: "game", Title: title}
	if ref != "" {
		now.Media = &scheduleMedia{Ref: ref, Kind: "game", Title: title}
	}

	return now
}

func TestTheActivityLine(t *testing.T) {
	const eldenRing = "1245620"

	live := func(streams ...apiStream) onAirGame { return onAirGames(streams)["u-dave"] }

	for _, tried := range []struct {
		name   string
		inGame bool
		title  string
		appID  string
		onAir  onAirGame
		live   bool
		want   *accountsViewActivity
	}{
		{name: "nothing known"},
		{name: "in a game on Steam, off the air", inGame: true, title: "ELDEN RING", appID: eldenRing,
			want: &accountsViewActivity{Kind: "playing", Title: "ELDEN RING", Source: "steam"}},
		{name: "in a game Steam does not name", inGame: true,
			want: &accountsViewActivity{Kind: "playing", Title: "", Source: "steam"}},

		// By Steam's id: the same app is the same game, whatever each side calls it.
		{name: "live, What's on? picked from Steam: the same app", inGame: true, title: "ELDEN RING", appID: eldenRing, live: true,
			onAir: live(liveCard("u-dave", "h1", saysGame("Elden Ring: Game of the Year", "steam:"+eldenRing))),
			want:  &accountsViewActivity{Kind: "streaming", Title: "ELDEN RING", Source: "steam"}},
		{name: "live, What's on? picked from Steam: another app of the same name", inGame: true, title: "ELDEN RING", appID: eldenRing, live: true,
			onAir: live(liveCard("u-dave", "h1", saysGame("ELDEN RING", "steam:999"))),
			want:  &accountsViewActivity{Kind: "playing", Title: "ELDEN RING", Source: "steam"}},

		// By name where there is no id on both sides: tidied the way the two
		// services differ.
		{name: "live, What's on? from IGDB, the same name", inGame: true, title: "DARK SOULS™ III", appID: "374320", live: true,
			onAir: live(liveCard("u-dave", "h1", saysGame("Dark  Souls III", "igdb:11133"))),
			want:  &accountsViewActivity{Kind: "streaming", Title: "DARK SOULS™ III", Source: "steam"}},
		{name: "live, What's on? typed by hand, the same name", inGame: true, title: "Hades", appID: "1145360", live: true,
			onAir: live(liveCard("u-dave", "h1", saysGame("hades", ""))),
			want:  &accountsViewActivity{Kind: "streaming", Title: "Hades", Source: "steam"}},
		{name: "live, Steam gave no id, What's on? from Steam, the same name", inGame: true, title: "Hades", live: true,
			onAir: live(liveCard("u-dave", "h1", saysGame("Hades", "steam:1145360"))),
			want:  &accountsViewActivity{Kind: "streaming", Title: "Hades", Source: "steam"}},
		{name: "live, in a game through a Steam shortcut, What's on? picked from Steam, the same name", inGame: true, title: "Hades",
			appID: steamAppIDOf(json.RawMessage(`"15832351425395220480"`)), live: true,
			onAir: live(liveCard("u-dave", "h1", saysGame("Hades", "steam:1145360"))),
			want:  &accountsViewActivity{Kind: "streaming", Title: "Hades", Source: "steam"}},
		{name: "live, another game on", inGame: true, title: "Hades", appID: "1145360", live: true,
			onAir: live(liveCard("u-dave", "h1", saysGame("Factorio", "steam:427520"))),
			want:  &accountsViewActivity{Kind: "playing", Title: "Hades", Source: "steam"}},

		// A movie or a show on: "Streaming" it, whatever Steam says, since
		// the streamer has said what is on.
		{name: "live with a movie on, no Steam game", live: true,
			onAir: live(liveCard("u-dave", "h1", &apiNowPlaying{Category: "movie", Title: " Quantum of Solace "})),
			want:  &accountsViewActivity{Kind: "streaming", Title: "Quantum of Solace", Source: "panel"}},
		{name: "live with a movie on, in a game on Steam", inGame: true, title: "Hades", appID: "1145360", live: true,
			onAir: live(liveCard("u-dave", "h1", &apiNowPlaying{Category: "movie", Title: "Heat"})),
			want:  &accountsViewActivity{Kind: "streaming", Title: "Heat", Source: "panel"}},
		{name: "live with a movie on, in a game Steam does not name", inGame: true, live: true,
			onAir: live(liveCard("u-dave", "h1", &apiNowPlaying{Category: "movie", Title: "Heat"})),
			want:  &accountsViewActivity{Kind: "streaming", Title: "Heat", Source: "panel"}},
		{name: "live with a movie picked, its looked-up title", live: true,
			onAir: live(liveCard("u-dave", "h1", &apiNowPlaying{Category: "movie", Title: "quantum solace",
				Media: &scheduleMedia{Ref: "tmdb:movie:10764", Kind: "movie", Title: "Quantum of Solace"}})),
			want: &accountsViewActivity{Kind: "streaming", Title: "Quantum of Solace", Source: "panel"}},
		{name: "live with a show picked", inGame: true, title: "Hades", appID: "1145360", live: true,
			onAir: live(liveCard("u-dave", "h1", &apiNowPlaying{Category: "movie", Title: "severance",
				Media: &scheduleMedia{Ref: "tmdb:tv:95396", Kind: "tv", Title: "Severance"}})),
			want: &accountsViewActivity{Kind: "streaming", Title: "Severance", Source: "panel"}},
		{name: "live with a title of no category", live: true,
			onAir: live(liveCard("u-dave", "h1", &apiNowPlaying{Title: "Movie night"})),
			want:  &accountsViewActivity{Kind: "streaming", Title: "Movie night", Source: "panel"}},
		{name: "off the air with a movie on, in a game on Steam", inGame: true, title: "Hades", appID: "1145360",
			onAir: live(liveCard("u-dave", "h1", &apiNowPlaying{Category: "movie", Title: "Heat"})),
			want:  &accountsViewActivity{Kind: "playing", Title: "Hades", Source: "steam"}},
		{name: "off the air with a movie on", onAir: live(liveCard("u-dave", "h1", &apiNowPlaying{Category: "movie", Title: "Heat"}))},

		// A movie with no title says nothing of its own: "Playing" what
		// Steam says, and nothing without Steam.
		{name: "live with an untitled movie on, in a game on Steam", inGame: true, title: "Hades", appID: "1145360", live: true,
			onAir: live(liveCard("u-dave", "h1", &apiNowPlaying{Category: "movie", Title: "  "})),
			want:  &accountsViewActivity{Kind: "playing", Title: "Hades", Source: "steam"}},
		{name: "live with an untitled movie on, no Steam game", live: true,
			onAir: live(liveCard("u-dave", "h1", &apiNowPlaying{Category: "movie"}))},

		// No game named by Steam: what the card says.
		{name: "live, the card names the game", live: true,
			onAir: live(liveCard("u-dave", "h1", saysGame("Factorio", "steam:427520"))),
			want:  &accountsViewActivity{Kind: "streaming", Title: "Factorio", Source: "panel"}},
		{name: "live, in a game Steam does not name, the card names one", inGame: true, live: true,
			onAir: live(liveCard("u-dave", "h1", saysGame("Minecraft", ""))),
			want:  &accountsViewActivity{Kind: "streaming", Title: "Minecraft", Source: "panel"}},
		{name: "live, nothing said", live: true, onAir: live(liveCard("u-dave", "h1", nil))},
	} {
		require.Equal(t, tried.want, activityOf(tried.inGame, tried.title, tried.appID, tried.onAir, tried.live), tried.name)
	}
}

// Twitch's category counts for the card it is on, and for the Broadcast Box
// card that stands in for a Twitch simulcast.
func TestTheActivityLineReadsTwitchsCategory(t *testing.T) {
	onTwitch := apiStream{Handle: "tw1", UserID: "u-dave", Source: "twitch", LinkSource: linkSourceTwitch, TwitchCategory: "Dark Souls III"}

	// Live on Twitch alone: the Twitch entry is their card.
	alone := onAirGames([]apiStream{onTwitch})["u-dave"]
	require.Equal(t, &accountsViewActivity{Kind: "streaming", Title: "Dark Souls III", Source: "twitch"},
		activityOf(false, "", "", alone, true))
	require.Equal(t, &accountsViewActivity{Kind: "streaming", Title: "DARK SOULS™ III", Source: "steam"},
		activityOf(true, "DARK SOULS™ III", "374320", alone, true), "the same game by name")

	// A simulcast: the Broadcast Box card stands for them, and the Twitch
	// entry it covers still says the category.
	covered := onTwitch
	covered.CoveredBy = "h1"
	both := onAirGames([]apiStream{covered, liveCard("u-dave", "h1", nil)})["u-dave"]
	require.Equal(t, &accountsViewActivity{Kind: "streaming", Title: "Dark Souls III", Source: "twitch"},
		activityOf(false, "", "", both, true))

	// What's on? comes first, and a movie on is what they stream, whatever
	// the category or Steam says.
	tagged := onAirGames([]apiStream{covered, liveCard("u-dave", "h1", saysGame("Hades", "steam:1145360"))})["u-dave"]
	require.Equal(t, &accountsViewActivity{Kind: "streaming", Title: "Hades", Source: "panel"},
		activityOf(false, "", "", tagged, true))
	movie := onAirGames([]apiStream{covered, liveCard("u-dave", "h1", &apiNowPlaying{Category: "movie", Title: "Heat"})})["u-dave"]
	require.Equal(t, &accountsViewActivity{Kind: "streaming", Title: "Heat", Source: "panel"},
		activityOf(false, "", "", movie, true))
	require.Equal(t, &accountsViewActivity{Kind: "streaming", Title: "Heat", Source: "panel"},
		activityOf(true, "DARK SOULS™ III", "374320", movie, true))

	// A movie with no title means the category is not asked, but the
	// category still makes it "Streaming" the game Steam names.
	untitled := onAirGames([]apiStream{covered, liveCard("u-dave", "h1", &apiNowPlaying{Category: "movie"})})["u-dave"]
	require.Nil(t, activityOf(false, "", "", untitled, true))
	require.Equal(t, &accountsViewActivity{Kind: "streaming", Title: "DARK SOULS™ III", Source: "steam"},
		activityOf(true, "DARK SOULS™ III", "374320", untitled, true))
}

// Whose card is whose is the people index's rule: never a 24/7 channel, never
// a paused stream, and nobody the stream is not attributed to.
func TestOnlyTheCardThatStandsForSomebodySaysWhatTheyStream(t *testing.T) {
	paused := liveCard("u-dave", "h1", saysGame("Hades", ""))
	paused.Paused = true
	channel := liveCard("u-dave", "h2", saysGame("Hades", ""))
	channel.AlwaysOn = true
	nobodys := liveCard("", "h3", saysGame("Hades", ""))

	require.Empty(t, onAirGames([]apiStream{paused, channel, nobodys}))
	require.Empty(t, onAirGames([]apiStream{liveCard("u-dave", "h1", nil)}), "a card that says nothing is nothing to keep")

	games := onAirGames([]apiStream{paused, liveCard("u-bob", "h4", saysGame("Factorio", "steam:427520"))})
	require.Len(t, games, 1)
	require.Equal(t, onAirGame{tagged: true, game: true, said: "Factorio", appID: "427520"}, games["u-bob"])
}

func TestTheSameGameByTwoNames(t *testing.T) {
	for _, pair := range [][2]string{
		{"DARK SOULS™ III", "Dark Souls III"},
		{"Tom Clancy's Rainbow Six® Siege", "tom clancy's rainbow six siege"},
		{"  Hades ", "Hades"},
		{"A  Game\tOf   Spaces", "a game of spaces"},
	} {
		require.True(t, sameGameName(pair[0], pair[1]), pair[0])
	}
	for _, pair := range [][2]string{
		{"Hades", "Hades II"},
		{"", ""},
		{"™", "®"},
		{"Hades", ""},
	} {
		require.False(t, sameGameName(pair[0], pair[1]), "%q and %q", pair[0], pair[1])
	}

	for raw, want := range map[string]string{
		`"1245620"`: "1245620", `1245620`: "1245620", ` "730" `: "730",
		`"0"`: "", `0`: "", `null`: "", ``: "", `""`: "", `"12ab"`: "", `"-5"`: "", `"123456789012345678901"`: "",
		// A shortcut to a game that is not Steam's: past 2^63, no app id.
		`"15832351425395220480"`: "", `15832351425395220480`: "", `"9223372036854775808"`: "", `"12345678901"`: "",
		`"4294967295"`: "4294967295",
	} {
		require.Equal(t, want, steamAppIDOf(json.RawMessage(raw)), raw)
	}
}

// activityIn is somebody's line in GET /accounts as reader is given it.
func activityIn(t *testing.T, s *accountsScene, reader, userID string) (*accountsViewActivity, accountsViewPerson) {
	t.Helper()

	code, body := s.accountsAnswer(t, reader)
	require.Equal(t, http.StatusOK, code, string(body))

	var view accountsView
	require.NoError(t, json.Unmarshal(body, &view))

	return view.People[userID].Activity, view.People[userID]
}

// The line rides GET /accounts and nothing else: for readers who may watch,
// changing when the stream does as well as when Steam does, and telling
// everybody to read again when it has.
func TestTheActivityLineRidesTheAccountsAnswer(t *testing.T) {
	s := newAccountsScene(t)
	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3, GameName: "ELDEN RING", GameID: json.RawMessage(`"1245620"`)})
	s.connectSteam(t, acDave, daveSteamID)
	s.step(t)

	line, _ := activityIn(t, s, acErin, acDave)
	require.Equal(t, &accountsViewActivity{Kind: "playing", Title: "ELDEN RING", Source: "steam"}, line)

	// A quiet spell: no event still waiting to go out, and none in the last
	// ten seconds, so the next change is told at once and can be counted.
	quiet := func() {
		s.p.accounts().stopEvents()
		s.clock.advance(time.Minute)
	}
	quiet()
	events := s.accountsEvents(t)

	// Dave goes live with that very game picked in What's on?: the snapshot
	// is stored, the line changes, and everybody is told to read again.
	// Nothing was asked of Steam for it.
	asked := s.steam.summaryCount()
	s.p.publish(&snapshot{FetchedAt: 1, Streams: []apiStream{liveCard(acDave, "h1", saysGame("ELDEN RING", "steam:1245620"))}})

	line, _ = activityIn(t, s, acErin, acDave)
	require.Equal(t, &accountsViewActivity{Kind: "streaming", Title: "ELDEN RING", Source: "steam"}, line)
	require.Equal(t, events+1, s.accountsEvents(t))
	require.Equal(t, asked, s.steam.summaryCount())

	// The same cards again say nothing new, and nobody is told anything.
	quiet()
	s.p.publish(&snapshot{FetchedAt: 2, Streams: []apiStream{liveCard(acDave, "h1", saysGame("ELDEN RING", "steam:1245620"))}})
	require.Equal(t, events+1, s.accountsEvents(t))

	// Somebody connected to nothing, on the air with a game on their card,
	// is in people for the line alone.
	quiet()
	s.p.publish(&snapshot{FetchedAt: 3, Streams: []apiStream{
		liveCard(acDave, "h1", saysGame("ELDEN RING", "steam:1245620")),
		liveCard(acBob, "h2", saysGame("Factorio", "steam:427520")),
	}})
	line, bob := activityIn(t, s, acErin, acBob)
	require.Equal(t, &accountsViewActivity{Kind: "streaming", Title: "Factorio", Source: "panel"}, line)
	require.Nil(t, bob.Steam)
	require.Nil(t, bob.Hytale)
	require.Equal(t, events+2, s.accountsEvents(t))

	// None of it is in the snapshot, which everybody is sent, locked or
	// not: looked at while both are on the air and in its people. (A card
	// may carry a "steam:" reference of What's on?: that is the card's.)
	pushed, err := json.Marshal(s.p.getSnapshot())
	require.NoError(t, err)
	require.NotContains(t, string(pushed), `"activity"`)
	var sent struct {
		People map[string]map[string]json.RawMessage `json:"people"`
	}
	require.NoError(t, json.Unmarshal(pushed, &sent))
	require.Contains(t, sent.People, acDave)
	require.Contains(t, sent.People, acBob)
	for userID, person := range sent.People {
		for _, field := range []string{"activity", "steam", "hytale", "game"} {
			require.NotContains(t, person, field, "%s's %s in the snapshot", userID, field)
		}
	}

	// Off the air: "Playing" again for Dave, and Bob is gone from people.
	quiet()
	s.p.publish(&snapshot{FetchedAt: 4, Streams: []apiStream{}})
	line, _ = activityIn(t, s, acErin, acDave)
	require.Equal(t, &accountsViewActivity{Kind: "playing", Title: "ELDEN RING", Source: "steam"}, line)
	code, body := s.accountsAnswer(t, acErin)
	require.Equal(t, http.StatusOK, code)
	require.NotContains(t, string(body), acBob)

	// An older snapshot's cards, told after the newer one's (two publishes
	// let go of the snapshot's lock in the other order from the one they
	// stored in), change nothing: the store keeps what the snapshot says now.
	s.p.accounts().noteOnAir(s.p.snapshotSeq-1, []apiStream{liveCard(acBob, "h2", saysGame("Factorio", "steam:427520"))})
	code, body = s.accountsAnswer(t, acErin)
	require.Equal(t, http.StatusOK, code)
	require.NotContains(t, string(body), acBob)

	// Nobody locked is told any of it, whoever's stream says it.
	code, _ = s.accountsAnswer(t, acDan)
	require.Equal(t, http.StatusForbidden, code)
}

// A movie picked, changed or taken down mid-stream changes the line, and each
// change tells everybody to read again, Steam game or not; nobody locked is
// told any of it.
func TestAMovieOnTheAirIsWhatTheyStream(t *testing.T) {
	s := newAccountsScene(t)
	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3, GameName: "ELDEN RING", GameID: json.RawMessage(`"1245620"`)})
	s.connectSteam(t, acDave, daveSteamID)
	s.step(t)

	quiet := func() {
		s.p.accounts().stopEvents()
		s.clock.advance(time.Minute)
	}
	onAir := func(at int64, dave, bob *apiNowPlaying) {
		s.p.publish(&snapshot{FetchedAt: at, Streams: []apiStream{liveCard(acDave, "h1", dave), liveCard(acBob, "h2", bob)}})
	}
	movie := func(title string) *apiNowPlaying { return &apiNowPlaying{Category: "movie", Title: title} }

	// Both live, nothing said: Dave plays what Steam says, Bob has no line.
	quiet()
	onAir(1, nil, nil)
	line, _ := activityIn(t, s, acErin, acDave)
	require.Equal(t, &accountsViewActivity{Kind: "playing", Title: "ELDEN RING", Source: "steam"}, line)
	line, _ = activityIn(t, s, acErin, acBob)
	require.Nil(t, line)

	// A movie picked by each, mid-stream: one event, and the movie wins
	// over Dave's Steam game.
	quiet()
	events := s.accountsEvents(t)
	onAir(2, movie("Heat"), movie("Quantum of Solace"))
	line, _ = activityIn(t, s, acErin, acDave)
	require.Equal(t, &accountsViewActivity{Kind: "streaming", Title: "Heat", Source: "panel"}, line)
	line, bob := activityIn(t, s, acErin, acBob)
	require.Equal(t, &accountsViewActivity{Kind: "streaming", Title: "Quantum of Solace", Source: "panel"}, line)
	require.Nil(t, bob.Steam)
	require.Equal(t, events+1, s.accountsEvents(t))

	// Another movie: the title alone changed, and that is told too.
	quiet()
	onAir(3, movie("Heat"), movie("Skyfall"))
	line, _ = activityIn(t, s, acErin, acBob)
	require.Equal(t, &accountsViewActivity{Kind: "streaming", Title: "Skyfall", Source: "panel"}, line)
	require.Equal(t, events+2, s.accountsEvents(t))

	// Still nothing of it in the snapshot.
	pushed, err := json.Marshal(s.p.getSnapshot())
	require.NoError(t, err)
	require.NotContains(t, string(pushed), `"activity"`)

	// Taken down: Dave plays again, Bob is gone from people.
	quiet()
	onAir(4, nil, nil)
	line, _ = activityIn(t, s, acErin, acDave)
	require.Equal(t, &accountsViewActivity{Kind: "playing", Title: "ELDEN RING", Source: "steam"}, line)
	code, body := s.accountsAnswer(t, acErin)
	require.Equal(t, http.StatusOK, code)
	require.NotContains(t, string(body), acBob)
	require.Equal(t, events+3, s.accountsEvents(t))

	code, body = s.accountsAnswer(t, acDan)
	require.Equal(t, http.StatusForbidden, code)
	require.NotContains(t, string(body), "Heat")
	require.NotContains(t, string(body), "activity")
}

// Steam's half is shown only while Steam's read is fresh and the profile is
// public; the stream's half does not wait on Steam at all, and is there with
// Steam switched off.
func TestTheActivityLineKeepsSteamsRules(t *testing.T) {
	s := newAccountsScene(t)
	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3, GameName: "ELDEN RING", GameID: json.RawMessage(`"1245620"`)})
	s.connectSteam(t, acDave, daveSteamID)
	s.step(t)
	s.p.publish(&snapshot{FetchedAt: 1, Streams: []apiStream{liveCard(acDave, "h1", saysGame("Factorio", "steam:427520"))}})

	line, _ := activityIn(t, s, acErin, acDave)
	require.Equal(t, &accountsViewActivity{Kind: "playing", Title: "ELDEN RING", Source: "steam"}, line, "live with another game on")

	// A profile that went private: Steam's game is not said, so the line is
	// what the stream says.
	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 0, Visibility: 1})
	s.clock.advance(time.Minute)
	s.step(t)
	line, _ = activityIn(t, s, acErin, acDave)
	require.Equal(t, &accountsViewActivity{Kind: "streaming", Title: "Factorio", Source: "panel"}, line)

	// Steam not answered for six minutes: the same, never a game that was.
	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3, GameName: "ELDEN RING", GameID: json.RawMessage(`"1245620"`)})
	s.clock.advance(time.Minute)
	s.step(t)
	line, _ = activityIn(t, s, acErin, acDave)
	require.Equal(t, "ELDEN RING", line.Title)

	s.steam.answerSummaries(http.StatusInternalServerError, "")
	s.clock.advance(6 * time.Minute)
	s.step(t)
	line, _ = activityIn(t, s, acErin, acDave)
	require.Equal(t, &accountsViewActivity{Kind: "streaming", Title: "Factorio", Source: "panel"}, line)

	// Steam switched off altogether: the stream's own line is still there
	// for a reader who may watch.
	s.configure(func(cfg *configuration) { cfg.EnableSteam = false })
	s.p.restartAccountsLive(nil)
	line, dave := activityIn(t, s, acErin, acDave)
	require.Equal(t, &accountsViewActivity{Kind: "streaming", Title: "Factorio", Source: "panel"}, line)
	require.Nil(t, dave.Steam)
}

// Somebody deactivated is left out of what people are doing, connected to a
// service or not; and let back in, they are given their line again as soon as
// they are on the air, with both services off as with one on.
func TestADeactivatedPersonOnTheAirIsLeftOutUntilLetBackIn(t *testing.T) {
	bob := func(s *accountsScene) *accountsViewActivity {
		line, _ := activityIn(t, s, acErin, acBob)
		return line
	}
	factorio := &accountsViewActivity{Kind: "streaming", Title: "Factorio", Source: "panel"}
	onAir := func(s *accountsScene, at int64) {
		s.p.publish(&snapshot{FetchedAt: at, Streams: []apiStream{liveCard(acBob, "h2", saysGame("Factorio", "steam:427520"))}})
	}

	// Steam on: Bob, connected to nothing, is deactivated while on the air.
	// The step the hook kicks agrees with the hook, and does not bring the
	// line back.
	s := newAccountsScene(t)
	s.step(t)
	onAir(s, 1)
	require.Equal(t, factorio, bob(s))

	s.deactivate(acBob)
	s.p.UserHasBeenDeactivated(nil, &model.User{Id: acBob, DeleteAt: 1})
	require.Nil(t, bob(s))
	s.clock.advance(time.Minute)
	s.step(t)
	require.Nil(t, bob(s), "the next step agrees")

	// Reactivated: the next step finds them active again.
	s.reactivate(acBob)
	s.clock.advance(time.Minute)
	s.step(t)
	require.Equal(t, factorio, bob(s))

	// Both services off, so no step ever runs. Deactivated, then let back
	// in while off the air: going live again is when they are looked up,
	// and given their line.
	cfg := accountsConfig()
	cfg.EnableSteam, cfg.EnableHytale = false, false
	off := newAccountsSceneWith(t, cfg)
	onAir(off, 1)
	require.Equal(t, factorio, bob(off))

	off.deactivate(acBob)
	off.p.UserHasBeenDeactivated(nil, &model.User{Id: acBob, DeleteAt: 1})
	require.Nil(t, bob(off))
	off.p.publish(&snapshot{FetchedAt: 2, Streams: []apiStream{}})

	looked := off.usersLookups()
	onAir(off, 3)
	require.Nil(t, bob(off), "still deactivated")
	require.Equal(t, looked+1, off.usersLookups(), "looked up as the stream started")
	onAir(off, 4)
	require.Equal(t, looked+1, off.usersLookups(), "and not again while it goes on")

	off.p.publish(&snapshot{FetchedAt: 5, Streams: []apiStream{}})
	off.reactivate(acBob)
	onAir(off, 6)
	require.Equal(t, factorio, bob(off))
}
