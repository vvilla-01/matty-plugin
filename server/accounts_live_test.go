package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/require"
)

// The accounts loop (design/3.4.0.md §2.13, §3.5, §8.1): the one Steam read a
// minute, who is asked about, how long a status is kept, how failures back
// off, the accounts event, and starting and stopping with the switches.
// Every test steps the loop by hand on a fake clock.

// steamView is GET /api/v1/accounts as somebody sees it, decoded.
func (s *accountsScene) steamView(t *testing.T, userID string) accountsView {
	t.Helper()

	code, body := s.accountsAnswer(t, userID)
	require.Equal(t, http.StatusOK, code, string(body))

	var view accountsView
	require.NoError(t, json.Unmarshal(body, &view))

	return view
}

// steamStates is each person's Steam state in the view, by Mattermost user.
func (s *accountsScene) steamStates(t *testing.T) map[string]string {
	t.Helper()

	states := map[string]string{}
	for userID, person := range s.steamView(t, acBob).People {
		if person.Steam != nil {
			states[userID] = person.Steam.State
		}
	}

	return states
}

// statusesHeld is the SteamIDs the loop holds a status for.
func (s *accountsScene) statusesHeld() []string {
	store := s.p.accounts()
	store.lock.Lock()
	defer store.lock.Unlock()

	return sortedIDs(store.steam)
}

// steamQueries is the address of every GetPlayerSummaries call so far.
func (s *accountsScene) steamQueries() []string {
	_, queries := s.steam.summaryCalls()

	return queries
}

// davesAndErins is Dave and Erin connected to Steam, both public.
func (s *accountsScene) davesAndErins(t *testing.T) {
	t.Helper()

	s.putLinks(t, steamProvider, &accountLinks{Users: map[string]accountLink{
		acDave: {ID: daveSteamID, Name: "Dave", At: 1},
		acErin: {ID: erinSteamID, Name: "Erin", At: 1},
	}})
	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3, GameName: "ELDEN RING"})
	s.player(steamPlayer{SteamID: erinSteamID, Name: "Erin", State: 3, Visibility: 3})
}

func TestSteamKeepsTheLastGoodFiveMinutes(t *testing.T) {
	s := newAccountsScene(t)
	s.davesAndErins(t)

	s.step(t)
	require.Equal(t, map[string]string{acDave: "online", acErin: "away"}, s.steamStates(t))
	view := s.steamView(t, acBob)
	require.True(t, view.Steam.Fresh)
	require.Equal(t, "ELDEN RING", *view.People[acDave].Steam.Game)
	goodAt := s.now()

	// A failed read changes nothing.
	s.clock.advance(time.Minute)
	s.steam.answerSummaries(http.StatusInternalServerError, "")
	s.step(t)
	require.Len(t, s.steamQueries(), 2)
	require.Equal(t, map[string]string{acDave: "online", acErin: "away"}, s.steamStates(t))

	// Until five minutes after the last good one.
	s.clock.advance(4 * time.Minute)
	require.True(t, s.steamView(t, acBob).Steam.Fresh)
	require.Equal(t, map[string]string{acDave: "online", acErin: "away"}, s.steamStates(t))

	// Then names, not statuses: nothing guessed.
	s.clock.advance(time.Second)
	view = s.steamView(t, acBob)
	require.False(t, view.Steam.Fresh)
	require.Equal(t, goodAt, s.p.accounts().steamLastGood, "when the last good read was")
	require.Equal(t, map[string]string{acDave: "", acErin: ""}, s.steamStates(t))
	require.Equal(t, "Dave", view.People[acDave].Steam.Name)
	require.False(t, view.People[acDave].Steam.InGame)
	require.Nil(t, view.People[acDave].Steam.Game)

	// And the next step forgets them altogether: statuses live in memory a
	// few minutes at most.
	s.step(t)
	require.Empty(t, s.statusesHeld())

	// A good answer again, without Erin: she shows "", never her last status.
	s.clock.advance(3 * time.Minute)
	s.steam.answerSummaries(0, "")
	s.steam.lock.Lock()
	delete(s.steam.players, erinSteamID)
	s.steam.lock.Unlock()
	s.step(t)
	require.Equal(t, map[string]string{acDave: "online", acErin: ""}, s.steamStates(t))
	require.Equal(t, []string{daveSteamID}, s.statusesHeld())
}

func TestADeactivatedPersonIsNotAskedAbout(t *testing.T) {
	s := newAccountsScene(t)
	s.putLinks(t, steamProvider, &accountLinks{Users: map[string]accountLink{
		acDave: {ID: daveSteamID, Name: "Dave", At: 1},
		acErin: {ID: erinSteamID, Name: "Erin", At: 1},
		acBob:  {ID: bobSteamID, Name: "Bob", At: 1},
	}})
	s.putLinks(t, hytaleProvider, &accountLinks{Users: map[string]accountLink{
		acDave: {ID: daveHytaleUUID, Name: "Steve_Hytale", At: 1},
		acErin: {ID: erinHytaleUUID, Name: "Erin_Builds", At: 1},
	}})
	for _, id := range []string{daveSteamID, erinSteamID, bobSteamID} {
		s.player(steamPlayer{SteamID: id, Name: "Someone", State: 1, Visibility: 3})
	}

	// Dave's account is deactivated; Erin's is deleted for good.
	s.deactivate(acDave)
	s.forget(acErin)
	s.step(t)

	require.Equal(t, []string{"steamids=" + bobSteamID}, s.steamQueries(), "nobody else is asked about")
	require.Equal(t, 1, s.usersLookups(), "one lookup for everybody")
	view := s.steamView(t, acBob)
	require.Equal(t, []string{acBob}, sortedIDs(view.People), "not shown either, on any service")
	require.Equal(t, []string{bobSteamID}, s.statusesHeld())

	// The admin card still lists them, marked.
	var card struct {
		People []adminSteamPerson `json:"people"`
	}
	response := s.serve(http.MethodGet, "/api/v1/admin/steam", acBoss, web(acBoss), "", nil)
	require.Equal(t, http.StatusOK, response.Code)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &card))
	active := map[string]bool{}
	for _, person := range card.People {
		active[person.UserID] = person.Active
	}
	require.Equal(t, map[string]bool{acDave: false, acErin: false, acBob: true}, active)

	// Reactivated, Dave is asked about again from the next step.
	s.reactivate(acDave)
	s.clock.advance(time.Minute)
	s.step(t)
	require.Equal(t, "steamids="+daveSteamID+","+bobSteamID, s.steamQueries()[1])
	require.Contains(t, s.steamView(t, acBob).People, acDave)

	// Mattermost's own hook takes Bob out at once, before any step.
	s.p.UserHasBeenDeactivated(nil, &model.User{Id: acBob, DeleteAt: 1})
	require.NotContains(t, s.steamView(t, acDave).People, acBob)
	require.NotContains(t, s.statusesHeld(), bobSteamID)
	s.deactivate(acBob)

	// A lookup that fails asks Steam nothing, and the last view stands.
	before := s.steamView(t, acDave)
	s.failUsers(true)
	s.clock.advance(time.Minute)
	s.step(t)
	require.Len(t, s.steamQueries(), 2)
	require.Equal(t, before.People, s.steamView(t, acDave).People)

	// Nothing connected is no lookup at all.
	empty := newAccountsScene(t)
	empty.step(t)
	require.Zero(t, empty.usersLookups())
	require.Empty(t, empty.steamQueries())
}

func TestADisconnectDuringASteamReadStaysDisconnected(t *testing.T) {
	for _, how := range []string{"disconnects", "is blocked"} {
		t.Run(how, func(t *testing.T) {
			s := newAccountsScene(t)
			s.davesAndErins(t)
			s.step(t)
			require.Contains(t, s.steamStates(t), acDave)

			// Steam's next answer carries a new name for Dave; it is held open
			// while he goes.
			s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave_Renamed", State: 1, Visibility: 3, GameName: "Hades II"})
			arrived, release := s.steam.holdSummaries()
			t.Cleanup(release)

			s.clock.advance(time.Minute)
			stepped := make(chan struct{})
			go func() {
				defer close(stepped)
				s.p.accounts().step(t.Context(), s.p.getConfiguration(), s.now())
			}()
			<-arrived

			if how == "disconnects" {
				response := s.serve(http.MethodDelete, "/api/v1/steam/link", acDave, web(acDave), "", nil)
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			} else {
				response := s.serve(http.MethodPost, "/api/v1/admin/steam/block", acBoss, web(acBoss), `{"userId":"`+acDave+`","blocked":true}`, nil)
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			}
			release()
			<-stepped

			// Not in anybody's view, not in the document, not in memory.
			_, body := s.accountsAnswer(t, acErin)
			for _, gone := range []string{acDave, daveSteamID, "Dave_Renamed", "Hades II"} {
				require.NotContains(t, string(body), gone)
			}
			require.NotContains(t, s.links(t, steamProvider).Users, acDave)
			require.NotContains(t, string(s.kv(kvSteamLinks)), "Dave_Renamed", "a rename creates no entry")
			require.Equal(t, []string{erinSteamID}, s.statusesHeld())

			// Nor in the view an accounts event would send panels to read.
			store := s.p.accounts()
			store.lock.Lock()
			announced := string(store.view)
			store.lock.Unlock()
			require.NotContains(t, announced, acDave)
			require.Contains(t, announced, acErin, "Erin's read went through")

			// The next step agrees.
			s.clock.advance(time.Minute)
			s.step(t)
			require.Equal(t, map[string]string{acErin: "away"}, s.steamStates(t))
		})
	}
}

func TestSteamFailuresBackOff(t *testing.T) {
	s := newAccountsScene(t)
	s.davesAndErins(t)
	store := s.p.accounts()
	problem := func() string { return s.me(t, acBoss).Steam.Problem }

	// Anything else: a minute, doubling, to ten. Admins are told on the
	// third failed pass in a row. A kick never cuts a wait short.
	s.steam.answerSummaries(http.StatusInternalServerError, "")
	s.step(t)
	require.Len(t, s.steamQueries(), 1)
	require.Empty(t, problem(), "one blip is not worth a sentence")

	for i, wait := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute, 10 * time.Minute} {
		asked := len(s.steamQueries())

		store.kick()
		s.clock.advance(steamKickGap)
		s.step(t)
		s.clock.advance(wait - steamKickGap - time.Second)
		s.step(t)
		require.Len(t, s.steamQueries(), asked, "still waiting after failure %d", i+1)

		s.clock.advance(time.Second)
		s.step(t)
		require.Len(t, s.steamQueries(), asked+1, "asked once the wait of %s is over", wait)

		if i+2 < steamProblemAfter {
			require.Empty(t, problem())
		} else {
			require.Equal(t, "Steam could not be reached; there has been no good check yet.", problem())
		}
	}

	// A good read clears it; three failures after it say how long ago it was.
	s.steam.answerSummaries(0, "")
	s.clock.advance(steamBackoffMax)
	s.step(t)
	require.Empty(t, problem())
	s.steam.answerSummaries(http.StatusBadGateway, "")
	for _, wait := range []time.Duration{0, time.Minute, 2 * time.Minute} {
		s.clock.advance(wait)
		s.step(t)
	}
	require.Equal(t, "Steam could not be reached; the last good check was 3 minutes ago.", problem())

	// A 429: two minutes, doubling, to ten, and admins are told at once,
	// with when.
	slow := newAccountsScene(t)
	slow.davesAndErins(t)
	slow.steam.answerSummaries(http.StatusTooManyRequests, "")
	slow.step(t)
	for _, wait := range []time.Duration{2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute} {
		next := slow.now().Add(wait)
		require.Equal(t, "Steam is asking Matty to slow down; checking again at "+next.UTC().Format("15:04")+" UTC.", slow.me(t, acBoss).Steam.Problem)

		asked := len(slow.steamQueries())
		slow.p.accounts().kick()
		slow.clock.advance(wait - time.Second)
		slow.step(t)
		require.Len(t, slow.steamQueries(), asked)
		slow.clock.advance(time.Second)
		slow.step(t)
		require.Len(t, slow.steamQueries(), asked+1)
	}

	// The loop wakes for the end of a wait shorter than its minute.
	slowStore := slow.p.accounts()
	slowStore.lock.Lock()
	slowStore.steamWaitUntil = slow.now().Add(30 * time.Second)
	slowStore.lock.Unlock()
	slowStore.waitForSteam(slow.now())
	slowStore.lock.Lock()
	require.Equal(t, slow.now().Add(30*time.Second), slowStore.nextAt)
	slowStore.lock.Unlock()

	// A refused key: statuses dropped, admins told at once, and nothing asked
	// however long it is, until the settings change (see
	// TestTheAccountsLoopRunsOnlyWhileASwitchIsOn).
	refused := newAccountsScene(t)
	refused.davesAndErins(t)
	refused.step(t)
	require.Equal(t, map[string]string{acDave: "online", acErin: "away"}, refused.steamStates(t))
	refused.steam.answerSummaries(http.StatusForbidden, "")
	refused.clock.advance(time.Minute)
	refused.step(t)
	require.Equal(t, steamKeyRefused, refused.me(t, acBoss).Steam.Problem)
	require.Empty(t, refused.statusesHeld())
	require.False(t, refused.steamView(t, acBob).Steam.Fresh)
	require.Equal(t, map[string]string{acDave: "", acErin: ""}, refused.steamStates(t))
	for _, later := range []time.Duration{steamKickGap, time.Minute, time.Hour, 24 * time.Hour} {
		refused.p.accounts().kick()
		refused.clock.advance(later)
		refused.step(t)
	}
	require.Len(t, refused.steamQueries(), 2)
	require.Empty(t, refused.me(t, acDave).Steam.Problem, "for admins only")

	// Connecting still works: the name is left for later.
	refused.player(steamPlayer{SteamID: bobSteamID, Name: "Bob", State: 1, Visibility: 3})
	refused.connectSteam(t, acBob, bobSteamID)
	require.True(t, refused.me(t, acBob).Steam.Connected)
}

func TestSteamWaitIsCapped(t *testing.T) {
	require.Equal(t, time.Minute, steamWait(steamBackoff, 1))
	require.Equal(t, 8*time.Minute, steamWait(steamBackoff, 4))
	require.Equal(t, steamBackoffMax, steamWait(steamBackoff, 5))
	require.Equal(t, steamBackoffMax, steamWait(steamSlowDown, 64), "never overflows")
	require.Equal(t, steamSlowDown, steamWait(steamSlowDown, 0))
}

func TestNothingIsAskedWhileOff(t *testing.T) {
	s := newAccountsSceneWith(t, &configuration{
		BroadcastBoxURL: "http://192.0.2.10:8080",
		SteamWebAPIKey:  testSteamKey, HytaleClientID: testHytaleClientID, HytaleClientSecret: testHytaleSecret,
	})
	s.putLinks(t, steamProvider, &accountLinks{Users: map[string]accountLink{acDave: {ID: daveSteamID, Name: "Dave", At: 1}}})
	s.putLinks(t, hytaleProvider, &accountLinks{Users: map[string]accountLink{acDave: {ID: daveHytaleUUID, Name: "Steve_Hytale", At: 1}}})
	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3})

	reads := map[string]int{}
	s.faults.setOnRead(func(key string) { reads[key]++ })
	linkReads := func() map[string]int {
		s.faults.lock.Lock()
		defer s.faults.lock.Unlock()

		return map[string]int{kvSteamLinks: reads[kvSteamLinks], kvHytaleLinks: reads[kvHytaleLinks]}
	}

	// Activation starts no loop, and reads nothing.
	s.p.restartAccountsLive(nil)
	store := s.p.accounts()
	store.runLock.Lock()
	require.Nil(t, store.cancel, "no loop")
	store.runLock.Unlock()

	// A step anyway asks nobody and reads nothing.
	s.step(t)
	require.Empty(t, s.fake.requests())
	require.Equal(t, map[string]int{kvSteamLinks: 0, kvHytaleLinks: 0}, linkReads())
	require.Zero(t, s.usersLookups())

	// Off twice, nobody, on the raw JSON.
	code, body := s.accountsAnswer(t, acErin)
	require.Equal(t, http.StatusOK, code)
	require.JSONEq(t, `{"v":"`+accountsPeopleV(map[string]accountsViewPerson{})+`","steam":{"on":false,"fresh":false},"hytale":{"on":false},"people":{}}`, string(body))

	// /me reads each document once, and nothing else of them.
	s.me(t, acDave)
	require.Equal(t, map[string]int{kvSteamLinks: 1, kvHytaleLinks: 1}, linkReads())

	require.Empty(t, s.fake.requests())
	require.Zero(t, s.faults.writesTo(kvSteamLinks))
	require.Zero(t, s.faults.writesTo(kvHytaleLinks))
	require.Zero(t, s.accountsEvents(t))
}

func TestHytaleAloneStillReadsItsListEveryMinute(t *testing.T) {
	s := newAccountsSceneWith(t, &configuration{
		BroadcastBoxURL: "http://192.0.2.10:8080",
		EnableHytale:    true, HytaleClientID: testHytaleClientID, HytaleClientSecret: testHytaleSecret,
	})

	// Somebody connected on another node: this node's memory knows nothing
	// of it until its next step reads the document.
	encoded, err := json.Marshal(&accountLinks{V: accountLinksVersion, Users: map[string]accountLink{acDave: {ID: daveHytaleUUID, Name: "Steve_Hytale", At: 1}}})
	require.NoError(t, err)
	s.setKV(kvHytaleLinks, encoded)
	require.Empty(t, s.steamView(t, acErin).People)

	s.step(t)
	view := s.steamView(t, acErin)
	require.Equal(t, "Steve_Hytale", view.People[acDave].Hytale.Name)
	require.Nil(t, view.People[acDave].Steam)
	require.Empty(t, s.fake.requests(), "Hytale is never asked anything by the loop")

	store := s.p.accounts()
	store.lock.Lock()
	require.Equal(t, s.now().Add(steamReadEvery), store.nextAt)
	store.lock.Unlock()
}

func TestTheLoopWritesBackSteamNamesThatChanged(t *testing.T) {
	s := newAccountsScene(t)

	// Steam's name was late at the connect, so the link has none.
	s.steam.answerSummaries(http.StatusServiceUnavailable, "")
	s.connectSteam(t, acDave, daveSteamID)
	require.Empty(t, s.links(t, steamProvider).Users[acDave].Name)

	s.steam.answerSummaries(0, "")
	s.player(steamPlayer{SteamID: daveSteamID, Name: " Da\u202eve ", State: 1, Visibility: 3})
	s.clock.advance(steamKickGap)
	s.step(t)
	require.Equal(t, "Dave", s.links(t, steamProvider).Users[acDave].Name, "cleaned and written back")
	require.Equal(t, "Dave", s.me(t, acDave).Steam.Name)
	require.Equal(t, "Dave", s.steamView(t, acErin).People[acDave].Steam.Name)

	// The same name again is no write.
	writes := s.faults.writesTo(kvSteamLinks)
	s.clock.advance(time.Minute)
	s.step(t)
	require.Equal(t, writes, s.faults.writesTo(kvSteamLinks))
}

func TestAccountsEventIsDebouncedAndEmpty(t *testing.T) {
	s := newAccountsScene(t)
	store := s.p.accounts()

	var lock sync.Mutex
	var waits []time.Duration
	fire := make(chan time.Time)
	store.after = func(wait time.Duration) <-chan time.Time {
		lock.Lock()
		defer lock.Unlock()

		waits = append(waits, wait)

		return fire
	}
	waited := func() []time.Duration {
		lock.Lock()
		defer lock.Unlock()

		return append([]time.Duration(nil), waits...)
	}

	// Ten changes in a second are two events: the first change at once, so
	// that a game somebody just started shows straight away, and one more for
	// everything after it, ten seconds on.
	for i := 0; i < 10; i++ {
		s.putLinks(t, hytaleProvider, &accountLinks{Users: map[string]accountLink{acDave: {ID: daveHytaleUUID, Name: fmt.Sprintf("Dave_%d", i), At: 1}}})
		s.p.accountsChanged()
		if i == 0 {
			require.Equal(t, 1, s.accountsEvents(t), "the first change of a quiet spell is told at once")
			require.Empty(t, waited())
		}
		s.clock.advance(100 * time.Millisecond)
	}
	require.Equal(t, 1, s.accountsEvents(t), "nothing more until the burst is over")
	require.Equal(t, []time.Duration{accountsEventGap}, waited())

	// The wait is over, well after the first event: so what follows is held
	// to the time of THIS event, not the first one's.
	s.clock.advance(accountsEventGap)
	fire <- time.Time{}
	store.eventWait.Wait()
	require.Equal(t, 2, s.accountsEvents(t))

	// Nothing that moves the view is no event.
	s.p.accountsChanged()
	s.step(t)
	require.Len(t, waited(), 1)
	require.Equal(t, 2, s.accountsEvents(t))

	// A change right after an event waits ten seconds of its own: never two
	// events inside ten seconds.
	s.putLinks(t, hytaleProvider, &accountLinks{Users: map[string]accountLink{}})
	s.p.accountsChanged()
	require.Equal(t, []time.Duration{accountsEventGap, accountsEventGap}, waited())
	require.Equal(t, 2, s.accountsEvents(t))

	// A stop drops a waiting event: nothing goes out afterwards.
	s.p.stopAccountsLive()
	require.Equal(t, 2, s.accountsEvents(t))
	store.eventLock.Lock()
	require.Nil(t, store.eventCancel)
	store.eventLock.Unlock()

	// "me" goes to the one person whose connection changed, empty.
	s.connectHytale(t, acErin, erinHytaleUUID, "Erin_Builds")
	before := len(s.accountEvents(t))
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/hytale/link", acErin, web(acErin), "", nil).Code)
	require.Equal(t, []string{acErin}, s.accountEvents(t)[before:])
}

func TestTheAccountsLoopRunsOnlyWhileASwitchIsOn(t *testing.T) {
	s := newAccountsScene(t)
	store := s.p.accounts()
	s.davesAndErins(t)

	// The loop's waits say when a step is over; an event that waits (every
	// one but the first: the scene's clock stands still) waits for ever.
	waiting := make(chan time.Duration, 16)
	store.after = func(wait time.Duration) <-chan time.Time {
		if wait != accountsEventGap {
			waiting <- wait
		}

		return nil
	}
	stepped := func() {
		t.Helper()

		select {
		case <-waiting:
		case <-time.After(30 * time.Second):
			require.FailNow(t, "the loop did not step")
		}
	}
	running := func() bool {
		store.runLock.Lock()
		defer store.runLock.Unlock()

		return store.cancel != nil
	}
	restart := func(change func(cfg *configuration)) {
		t.Helper()

		previous := s.p.getConfiguration().Clone()
		s.configure(change)
		if !sameAccountsLive(previous, s.p.getConfiguration()) {
			s.p.restartAccountsLive(previous)
		}
	}

	// Activation with both on: a step at once, which asks Steam.
	s.p.restartAccountsLive(nil)
	stepped()
	require.True(t, running())
	require.Len(t, s.steamQueries(), 1)
	require.Equal(t, "online", s.steamStates(t)[acDave])

	// A save that changes nothing that matters restarts nothing.
	restart(func(cfg *configuration) { cfg.BroadcastBoxURL = "http://192.0.2.11:8080" })
	require.Len(t, s.steamQueries(), 1)

	// Steam refuses the key. A Hytale change restarts the loop, but the
	// refusal holds: the key is the same.
	s.steam.answerSummaries(http.StatusForbidden, "")
	restart(func(cfg *configuration) { cfg.HytaleClientSecret = testHytaleSecret + "2" })
	stepped()
	require.Len(t, s.steamQueries(), 2)
	require.Equal(t, steamKeyRefused, s.me(t, acBoss).Steam.Problem)
	restart(func(cfg *configuration) { cfg.HytaleClientSecret = testHytaleSecret })
	stepped()
	require.Len(t, s.steamQueries(), 2, "held")

	// A new key is asked with at once.
	restart(func(cfg *configuration) { cfg.SteamWebAPIKey = testSteamKey + "NEW" })
	stepped()
	require.Len(t, s.steamQueries(), 3)
	keys, _ := s.steam.summaryCalls()
	require.Equal(t, testSteamKey+"NEW", keys[2])

	// Steam switched off: its rows go at once, and the loop runs on for
	// Hytale, asking Steam nothing.
	s.steam.answerSummaries(0, "")
	restart(func(cfg *configuration) { cfg.SteamWebAPIKey = testSteamKey })
	stepped()
	require.Equal(t, "online", s.steamStates(t)[acDave])
	restart(func(cfg *configuration) { cfg.EnableSteam = false })
	stepped()
	require.True(t, running())
	view := s.steamView(t, acErin)
	require.False(t, view.Steam.On)
	require.Empty(t, view.People)
	require.Empty(t, s.statusesHeld())
	asked := len(s.steamQueries())

	// Both off: the loop stops, and nothing is left running.
	restart(func(cfg *configuration) { cfg.EnableHytale = false })
	require.False(t, running())
	require.Len(t, s.steamQueries(), asked)

	// Back on: Steam is asked again at once, as if new.
	restart(func(cfg *configuration) { cfg.EnableSteam = true })
	stepped()
	require.Len(t, s.steamQueries(), asked+1)
	require.Equal(t, "online", s.steamStates(t)[acDave])

	// Deactivating waits for the loop, and drops the event waiting to go.
	s.p.stopAccountsLive()
	require.False(t, running())
	store.eventLock.Lock()
	require.Nil(t, store.eventCancel)
	store.eventLock.Unlock()
	require.Equal(t, 1, s.accountsEvents(t), "the first change was told at once; every one after it waited for ever, and none of those went")

	// A kick with no loop is harmless.
	store.kick()
	require.False(t, running())
}

func TestALinkWriteThatChangesNothingDoesNothing(t *testing.T) {
	s := newAccountsScene(t)
	s.davesAndErins(t)
	store := s.p.accounts()
	s.step(t)

	gap := func() time.Duration {
		store.lock.Lock()
		defer store.lock.Unlock()

		return store.nextAt.Sub(store.lastStep)
	}
	require.Equal(t, steamReadEvery, gap())
	writes := store.linkWrites(steamProvider)
	told := len(s.accountEvents(t))

	// Dan is locked, has nothing connected, and asks from a personal access
	// token, again and again: Disconnect and the notice's OK are open to
	// every session and not metered. An admin unblocks somebody who is not
	// blocked, clears an empty block list and disconnects somebody with
	// nothing connected.
	for i := 0; i < 3; i++ {
		for _, prov := range accountProviders {
			for _, method := range []string{http.MethodDelete, http.MethodPost} {
				target := "/api/v1/" + prov.id + "/link"
				if method == http.MethodPost {
					target = "/api/v1/" + prov.id + "/notice"
				}
				response := s.serve(method, target, acDan, "pat-"+acDan, "", nil)
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.JSONEq(t, `{"ok":true}`, response.Body.String())
			}
		}
	}
	s.adminDo(t, http.MethodPost, "/api/v1/admin/steam/block", acBoss, `{"userId":"`+acDan+`","blocked":false}`)
	s.adminDo(t, http.MethodPost, "/api/v1/admin/steam/block", acBoss, `{"all":true,"blocked":false}`)
	s.adminDo(t, http.MethodDelete, "/api/v1/admin/steam?user="+acDan, acBoss, "")

	// Steam's next call stays a minute away, nothing is cached over the
	// loop's read, and nobody is told anything changed.
	require.Equal(t, steamReadEvery, gap(), "no kick")
	require.Equal(t, writes, store.linkWrites(steamProvider), "nothing installed")
	require.Len(t, s.accountEvents(t), told, "no me event")

	// A Disconnect that takes something still does all of it.
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/steam/link", acDave, web(acDave), "", nil).Code)
	require.Equal(t, steamKickGap, gap())
	require.Equal(t, writes+1, store.linkWrites(steamProvider))
	require.Equal(t, []string{acDave}, s.accountEvents(t)[told:])
}

// writeHook runs afterWrite once, right after the next compare-and-set of key
// has been saved, before the writer goes on to anything else.
type writeHook struct {
	plugin.API
	key        string
	afterWrite func()
	fired      atomic.Bool
}

func (h *writeHook) KVCompareAndSet(key string, oldValue, newValue []byte) (bool, *model.AppError) {
	written, appErr := h.API.KVCompareAndSet(key, oldValue, newValue)
	if written && appErr == nil && key == h.key && h.fired.CompareAndSwap(false, true) {
		h.afterWrite()
	}

	return written, appErr
}

func hookWrite(t *testing.T, p *Plugin, key string, afterWrite func()) {
	t.Helper()

	original := p.API
	p.API = &writeHook{API: original, key: key, afterWrite: afterWrite}
	t.Cleanup(func() { p.API = original })
}

func TestLinkWritesAreCachedInTheOrderTheyWereSaved(t *testing.T) {
	gone := func(t *testing.T, s *accountsScene, userIDs ...string) {
		t.Helper()

		cached := s.p.accounts().cachedLinks(steamProvider)
		_, body := s.accountsAnswer(t, acBob)
		for _, userID := range userIDs {
			require.NotContains(t, s.links(t, steamProvider).Users, userID)
			require.NotContains(t, cached.Users, userID, "the cache never goes back to an older document")
			require.NotContains(t, string(body), userID)
		}
	}

	t.Run("two Disconnects at once", func(t *testing.T) {
		// Erin presses Disconnect just after Dave's has been saved, before it
		// has been cached. Hers waits for his to be cached, so his older
		// document, which still has her, is never put back over hers.
		s := newAccountsScene(t)
		s.davesAndErins(t)

		var erins chan error
		hookWrite(t, s.p, kvSteamLinks, func() {
			erins = meanwhile(func() error { return s.p.disconnectAccount(steamProvider, acErin, false, s.now().UnixMilli()) })
		})
		require.NoError(t, s.p.disconnectAccount(steamProvider, acDave, false, s.now().UnixMilli()))
		require.NoError(t, <-erins)

		gone(t, s, acDave, acErin)
	})

	t.Run("a Disconnect that changes nothing", func(t *testing.T) {
		// Dan's Disconnect, with nothing to take, reads the document while it
		// still has Dave, and Dave's own Disconnect lands before Dan's goes
		// on. Dan's caches nothing, so Dave stays gone.
		s := newAccountsScene(t)
		s.davesAndErins(t)

		var daves chan error
		hookRead(t, s.p, kvSteamLinks, func() {
			daves = meanwhile(func() error { return s.p.disconnectAccount(steamProvider, acDave, false, s.now().UnixMilli()) })
		})
		require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/steam/link", acDan, web(acDan), "", nil).Code)
		require.NoError(t, <-daves)

		gone(t, s, acDave)
		require.Contains(t, s.p.accounts().cachedLinks(steamProvider).Users, acErin)
	})
}

func TestAFixedHytaleApplicationIsNotStillSaidToBeWrong(t *testing.T) {
	adminWords := "Hytale refused the application's Client ID or Client Secret. Check them in the Hytale section."
	cardWords := "Hytale refused the Client ID or Client Secret."

	problems := func(t *testing.T, s *accountsScene) (string, string) {
		t.Helper()

		var card adminHytaleStatus
		require.NoError(t, json.Unmarshal(s.adminCard(t, hytaleProvider, acBoss), &card))

		return s.me(t, acBoss).Hytale.Problem, card.Problem
	}
	save := func(s *accountsScene, change func(cfg *configuration)) {
		previous := s.p.getConfiguration().Clone()
		s.configure(change)
		if !sameAccountsLive(previous, s.p.getConfiguration()) {
			s.p.restartAccountsLive(previous)
		}
	}

	for _, fix := range []struct {
		name   string
		change func(cfg *configuration)
	}{
		{"the right secret", func(cfg *configuration) { cfg.HytaleClientSecret = testHytaleSecret }},
		{"another application", func(cfg *configuration) {
			cfg.HytaleClientID, cfg.HytaleClientSecret = "another-client", testHytaleSecret
		}},
		{"Hytale switched off and on", func(cfg *configuration) { cfg.EnableHytale = false }},
	} {
		t.Run(fix.name, func(t *testing.T) {
			s := newAccountsScene(t)
			s.configure(func(cfg *configuration) { cfg.HytaleClientSecret = "the-wrong-secret" })

			// The exchange is refused: invalid_client, and admins are told.
			state, location := s.startConnect(t, hytaleProvider, acDave)
			s.grantHytale(t, location, "code-dave", hytaleGrant{})
			refused := s.callback(hytaleProvider, acDave, web(acDave), "code=code-dave&scope=openid+hytale%3Aprofile&state="+state)
			require.NotEqual(t, http.StatusOK, refused.Code, refused.Body.String())
			me, card := problems(t, s)
			require.Equal(t, adminWords, me)
			require.Equal(t, cardWords, card)

			// A save of something else says it still.
			save(s, func(cfg *configuration) { cfg.SteamWebAPIKey = testSteamKey + "-new" })
			me, card = problems(t, s)
			require.Equal(t, adminWords, me)
			require.Equal(t, cardWords, card)

			// The admin fixes it: nothing more is said until a sign-in says so.
			save(s, fix.change)
			if !s.p.getConfiguration().EnableHytale {
				save(s, func(cfg *configuration) { cfg.EnableHytale = true })
			}
			me, card = problems(t, s)
			require.Empty(t, me, "the alert over the admin's panel goes")
			require.Empty(t, card)
		})
	}
}
