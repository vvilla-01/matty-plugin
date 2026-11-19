package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// Withdrawing, purging, and keeping names right (design/twitch-live.md §4.6,
// §4.7, §11.1).

func TestDisconnectIsIdempotent(t *testing.T) {
	s := newTwitchScene(t)
	s.p.twitchLive().purgeOff = true
	s.connectAs(t, twDave, daveTwitch)

	first := s.serve(http.MethodDelete, twitchLinkPath, twDave, web(twDave), "", nil)
	require.Equal(t, http.StatusOK, first.Code)
	require.JSONEq(t, `{"ok":true}`, first.Body.String())
	require.Equal(t, "no-store", first.Header().Get("Cache-Control"))

	doc := s.links(t)
	require.NotContains(t, doc.Users, twDave)
	require.Equal(t, twitchPurgeEntry{User: twDave, At: s.now().UnixMilli(), Login: "dave_tv"}, doc.Purge["1001"])

	writes := s.faults.writesTo(kvTwitchLinks)
	again := s.serve(http.MethodDelete, twitchLinkPath, twDave, web(twDave), "", nil)
	require.Equal(t, http.StatusOK, again.Code)
	require.Equal(t, writes, s.faults.writesTo(kvTwitchLinks), "nothing to take, nothing written")

	// Open to everybody, the locked included, whether or not they have
	// anything connected.
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, twitchLinkPath, twDan, web(twDan), "", nil).Code)

	// The admin's Disconnect is the same.
	for i := 0; i < 2; i++ {
		response := s.serve(http.MethodDelete, adminTwitchPath+"?user="+twErin, twBoss, web(twBoss), "", nil)
		require.Equal(t, http.StatusOK, response.Code)
	}
	require.Equal(t, writes, s.faults.writesTo(kvTwitchLinks))
}

func TestDisconnectDropsTheCardOnTheNextPoll(t *testing.T) {
	s := newTwitchScene(t)
	s.p.twitchLive().purgeOff = true
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})
	s.fake.goLive(daveTwitch, "Live", "Chess", 4, s.now().Add(-time.Minute))
	s.step(t)

	cfg := s.p.getConfiguration()
	require.Len(t, s.p.twitchStreams(cfg, s.now()), 1)

	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, twitchLinkPath, twDave, web(twDave), "", nil).Code)
	require.Empty(t, s.p.twitchStreams(cfg, s.now()), "gone before Twitch is asked again")
	require.Equal(t, []string{twDave}, s.twitchEvents(t))
}

func TestPurgeIsRetriedAfterAKVError(t *testing.T) {
	s := newTwitchScene(t)
	s.seedTable(t, kvTwitchAirTotals, statTable{"1001": {Seconds: 600}, "1002": {Seconds: 60}})
	s.seedTable(t, twitchMonthBucket(s.now()), statTable{"1001": {Seconds: 600}})
	s.putLinks(t, &twitchLinks{Users: map[string]twitchLink{}, Purge: map[string]twitchPurgeEntry{"1001": {User: twDave, At: 1}}})

	month := twitchMonthBucket(s.now())
	s.faults.failRead(month)

	waits := make(chan time.Duration, 1)
	retry := make(chan time.Time)
	s.p.twitchLive().after = func(d time.Duration) <-chan time.Time {
		waits <- d
		return retry
	}

	s.p.startTwitchPurges()
	require.Equal(t, twitchPurgeRetry, <-waits, "a store error waits a minute")
	require.Contains(t, s.links(t).Purge, "1001", "still queued")
	require.Contains(t, s.table(t, kvTwitchAirTotals), "1001", "and nothing half done that matters")

	s.faults.healRead(month)
	retry <- time.Time{}
	s.waitForPurges()

	require.Empty(t, s.links(t).Purge)
	require.NotContains(t, s.table(t, kvTwitchAirTotals), "1001")
	require.Contains(t, s.table(t, kvTwitchAirTotals), "1002", "nobody else's")
	require.NotContains(t, s.table(t, month), "1001")
}

func TestPurgeWaitsWhileTheLedgerIsHalted(t *testing.T) {
	s := newTwitchScene(t)
	s.seedTable(t, kvTwitchAirTotals, statTable{"1001": {Seconds: 600}})
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})
	s.p.twitchAirHalt.stop("a Twitch table could not be read")

	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, twitchLinkPath, twDave, web(twDave), "", nil).Code)
	s.waitForPurges()

	require.NotContains(t, s.links(t).Users, twDave, "the link goes at once")
	require.Contains(t, s.links(t).Purge, "1001", "the purge waits")
	require.Contains(t, s.table(t, kvTwitchAirTotals), "1001")

	// A restart clears the halt, and its activation purges.
	s.p.twitchAirHalt.clear()
	s.p.startTwitchPurges()
	s.waitForPurges()
	require.Empty(t, s.links(t).Purge)
	require.NotContains(t, s.table(t, kvTwitchAirTotals), "1001")
}

func TestNamesAreRefreshedDaily(t *testing.T) {
	s := newTwitchScene(t)

	stale := s.now().Add(-25 * time.Hour).UnixMilli()
	fresh := s.now().Add(-time.Hour).UnixMilli()
	s.putLinks(t, &twitchLinks{Users: map[string]twitchLink{
		twDave: {ID: "1001", Login: "dave_tv", Name: "Dave_TV", At: stale, Checked: stale},
		twErin: {ID: "1002", Login: "erin_streams", Name: "Erin_Streams", At: fresh, Checked: fresh},
	}})
	s.fake.setAccount(twitchUser{ID: "1001", Login: "dave_renamed", DisplayName: "Dave_Renamed"})
	s.fake.setAccount(erinTwitch)

	s.step(t)

	users := s.fake.requests()
	asked := 0
	for _, seen := range users {
		if seen.path == "/helix/users" {
			asked++
			require.Equal(t, []string{"1001"}, seen.query["id"], "only the stale one")
			require.Equal(t, "Bearer app-token", seen.auth)
		}
	}
	require.Equal(t, 1, asked)

	dave := s.links(t).Users[twDave]
	require.Equal(t, "dave_renamed", dave.Login)
	require.Equal(t, "Dave_Renamed", dave.Name)
	require.Equal(t, s.now().UnixMilli(), dave.Checked)

	// Not again for a day.
	s.clock.advance(2 * time.Minute)
	s.step(t)
	require.Equal(t, 1, s.requestsTo("/helix/users"))
}

func TestAnAccountGoneTwiceIsDisconnected(t *testing.T) {
	s := newTwitchScene(t)
	s.p.twitchLive().purgeOff = true

	stale := s.now().Add(-25 * time.Hour).UnixMilli()
	s.putLinks(t, &twitchLinks{Users: map[string]twitchLink{twDave: {ID: "1001", Login: "dave_tv", Name: "Dave_TV", At: stale, Checked: stale}}})

	s.step(t)
	dave := s.links(t).Users[twDave]
	require.Equal(t, 1, dave.Gone, "one miss is not enough: Twitch may only be hiding a suspended account")
	require.Equal(t, s.now().UnixMilli(), dave.Checked)

	s.clock.advance(25 * time.Hour)
	s.step(t)
	doc := s.links(t)
	require.NotContains(t, doc.Users, twDave)
	require.Contains(t, doc.Purge, "1001")
	require.Equal(t, []string{twDave}, s.twitchEvents(t))
	require.Contains(t, strings.Join(s.logged(), "\n"), "no longer exists on Twitch")
}

func TestDeleteAllTwitchData(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})
	doc := s.links(t)
	doc.Blocked[twDan] = 5
	doc.Purge["1002"] = twitchPurgeEntry{User: twErin, At: 1}
	doc.Lost[twBob] = twitchLost{Login: "x", At: s.now().UnixMilli()}
	s.putLinks(t, doc)
	s.p.twitchLive().purgeOff = true

	s.seedTable(t, kvTwitchAirTotals, statTable{"1001": {Seconds: 5}})
	s.seedTable(t, kvTwitchAirMonthPrefix+"2026-05", statTable{"1001": {Seconds: 5}})
	s.seedTable(t, twitchMonthBucket(s.now()), statTable{"1001": {Seconds: 5}})
	s.setKV(kvTwitchAirSessions, []byte(`{}`))
	s.setKV(kvTwitchAirPaid, []byte(`{}`))
	for _, key := range []string{kvTwitchLinks, kvTwitchAirTotals, kvTwitchAirSessions, kvTwitchAirPaid, kvTwitchAirMonthPrefix + "2026-05"} {
		s.setKV(kvQuarantinePrefix+key, []byte("damaged"))
	}
	s.setKV(kvTwitchStatePrefix+strings.Repeat("a", 64), []byte("{}"))
	s.setKV(kvTwitchTicketPrefix+twErin, []byte("{}"))
	s.seedTable(t, kvStatsTotals, statTable{"videokey-dave": {Seconds: 999}})
	s.seedTable(t, monthBucket(s.now()), statTable{"videokey-dave": {Seconds: 99}})

	require.True(t, s.p.adminTwitchStatus().Hours.Quarantined)

	response := s.serve(http.MethodDelete, adminTwitchPath+"?all=1", twBoss, web(twBoss), "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	remaining := s.keysWith("twitch")
	require.Equal(t, []string{kvTwitchLinks}, remaining, "every Twitch key but the fresh document")
	require.Empty(t, s.keysWith(kvQuarantinePrefix+"twitch"), "and every set-aside copy")

	after := s.links(t)
	require.Empty(t, after.Users)
	require.Empty(t, after.Purge)
	require.Empty(t, after.Lost)
	require.Equal(t, map[string]int64{twDan: 5}, after.Blocked, "the block list holds only Mattermost ids, and stays")

	require.EqualValues(t, 999, s.table(t, kvStatsTotals)["videokey-dave"].Seconds, "Broadcast Box hours untouched")
	require.EqualValues(t, 99, s.table(t, monthBucket(s.now()))["videokey-dave"].Seconds)

	require.Equal(t, []string{""}, s.twitchEvents(t), "everybody's panels told")
	require.False(t, s.p.adminTwitchStatus().Hours.Quarantined)
}

// Blocking is the admin's only way to keep a channel out of Live now, so
// Delete all must never lose the block list quietly: not when the store will
// not read it, and not when another admin blocks somebody at the same moment.
func TestDeleteAllNeverLosesTheBlockList(t *testing.T) {
	t.Run("a read the store refuses writes nothing, and the second press finishes", func(t *testing.T) {
		s := newTwitchScene(t)
		s.p.twitchLive().purgeOff = true
		s.connected(t, map[string]twitchUser{twDave: daveTwitch})
		doc := s.links(t)
		doc.Blocked[twDan] = 5
		s.putLinks(t, doc)

		s.faults.failRead(kvTwitchLinks)
		first := s.serve(http.MethodDelete, adminTwitchPath+"?all=1", twBoss, web(twBoss), "", nil)
		require.Equal(t, http.StatusServiceUnavailable, first.Code)
		require.Contains(t, first.Body.String(), "Press Delete all Twitch data again.")
		s.faults.healRead(kvTwitchLinks)

		kept := s.links(t)
		require.Equal(t, map[string]int64{twDan: 5}, kept.Blocked, "the document was left exactly as it was")
		require.Contains(t, kept.Users, twDave)

		again := s.serve(http.MethodDelete, adminTwitchPath+"?all=1", twBoss, web(twBoss), "", nil)
		require.Equal(t, http.StatusOK, again.Code, again.Body.String())

		after := s.links(t)
		require.Empty(t, after.Users)
		require.Equal(t, map[string]int64{twDan: 5}, after.Blocked)
	})

	t.Run("a block saved while Delete all runs is kept", func(t *testing.T) {
		s := newTwitchScene(t)
		s.p.twitchLive().purgeOff = true
		s.connected(t, map[string]twitchUser{twDave: daveTwitch})
		doc := s.links(t)
		doc.Blocked[twDan] = 5
		s.putLinks(t, doc)

		// Another admin's block lands between Delete all reading the
		// document and writing it back.
		landed := false
		s.faults.setInterfere(func(key string, store map[string][]byte) {
			if key != kvTwitchLinks || landed {
				return
			}
			landed = true

			current, err := decodeTwitchLinks(store[key])
			require.NoError(t, err)
			current.Blocked[twBob] = 7
			encoded, err := json.Marshal(current)
			require.NoError(t, err)
			store[key] = encoded
		})

		response := s.serve(http.MethodDelete, adminTwitchPath+"?all=1", twBoss, web(twBoss), "", nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.True(t, landed)

		after := s.links(t)
		require.Empty(t, after.Users)
		require.Equal(t, map[string]int64{twDan: 5, twBob: 7}, after.Blocked, "both blocks, the one that raced it included")
	})
}

func TestWithdrawalWorksWithTheSwitchOff(t *testing.T) {
	for _, off := range []struct {
		name   string
		change func(cfg *configuration)
	}{
		{"switched off", func(cfg *configuration) { cfg.EnableTwitchLive = false }},
		{"credentials cleared", func(cfg *configuration) { cfg.TwitchClientID, cfg.TwitchClientSecret = "", "" }},
	} {
		t.Run(off.name, func(t *testing.T) {
			s := newTwitchScene(t)
			s.p.twitchLive().purgeOff = true
			s.seedTable(t, kvTwitchAirTotals, statTable{"1001": {Seconds: 600}, "1002": {Seconds: 60}})
			s.connected(t, map[string]twitchUser{twDave: daveTwitch, twErin: erinTwitch})

			cfg := s.p.getConfiguration().Clone()
			off.change(cfg)
			s.p.setConfiguration(cfg)

			me := s.me(t, twDave)
			require.NotNil(t, me.Twitch)
			require.Equal(t, meTwitch{Available: false, Login: "dave_tv", DisplayName: "Dave_TV"}, *me.Twitch)
			require.Nil(t, s.me(t, twBob).Twitch, "nothing for somebody with nothing to withdraw")

			require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, twitchLinkPath, twDave, web(twDave), "", nil).Code)
			require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, adminTwitchPath+"?user="+twErin, twBoss, web(twBoss), "", nil).Code)
			require.Empty(t, s.links(t).Users)

			// What OnActivate does for Twitch, whatever the switch says:
			// the loop, which stays stopped, and the purges, which run.
			s.p.restartTwitchLive()
			s.p.startTwitchPurges()
			s.waitForPurges()

			require.Empty(t, s.links(t).Purge)
			require.Empty(t, s.table(t, kvTwitchAirTotals))
			require.Empty(t, s.fake.requests(), "not one request to Twitch")
		})
	}
}

func TestAPurgeKeepsOtherPayersSpans(t *testing.T) {
	s := newTwitchScene(t)
	s.putLinks(t, &twitchLinks{
		Users: map[string]twitchLink{twDave: {ID: "1009", Login: "somebody_else"}},
		Purge: map[string]twitchPurgeEntry{"1001": {User: twDave, At: 1}},
	})

	paid := paidSpans{
		twDave: {{From: 0, To: 100}, {From: 100, To: 200, Tw: "1001"}, {From: 200, To: 300, Tw: "1009"}},
		twErin: {{From: 0, To: 50, Tw: "1002"}},
	}
	require.NoError(t, s.p.saveTwitchPaid(paid))

	more, err := s.p.purgeStep(t.Context(), s.now())
	require.NoError(t, err)
	require.True(t, more)

	after, err := s.p.loadTwitchPaid()
	require.NoError(t, err)
	require.Equal(t, paidSpans{
		twDave: {{From: 0, To: 100}, {From: 200, To: 300, Tw: "1009"}},
		twErin: {{From: 0, To: 50, Tw: "1002"}},
	}, after, "Broadcast Box's span and the newer account's stay")
}

func TestARenameWhoseWriteFailsNeverMisdirectsThePlayer(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twDave: daveTwitch, twErin: erinTwitch})

	renamed := twitchUser{ID: "1001", Login: "dave_new", DisplayName: "Dave_New"}
	s.fake.goLive(renamed, "Same channel, new name", "Chess", 2, s.now().Add(-time.Minute))
	s.fake.goLive(twitchUser{ID: "1002", Login: "Erin Streams!", DisplayName: "x"}, "Unchecked login", "Art", 2, s.now().Add(-time.Minute))
	s.faults.failWrite(kvTwitchLinks)

	s.step(t)

	streams := s.p.twitchStreams(s.p.getConfiguration(), s.now())
	require.Len(t, streams, 1, "a login that fails its check leaves the stream out of the read")
	require.Equal(t, "dave_new", streams[0].TwitchLogin, "from the read, not the document")

	doc := s.links(t)
	require.Equal(t, "dave_tv", doc.Users[twDave].Login, "the stored one is unchanged")
	require.Equal(t, "erin_streams", doc.Users[twErin].Login, "and nothing unchecked is ever written")

	s.faults.healWrite(kvTwitchLinks)
	s.clock.advance(time.Minute)
	s.step(t)
	require.Equal(t, "dave_new", s.links(t).Users[twDave].Login, "written once the store takes it")
	require.Equal(t, "erin_streams", s.links(t).Users[twErin].Login)
}

// The worker and the loop can each be stopped while they are inside a store
// read, by Delete all and by a settings save: each finishes the read it is in,
// notices it was cancelled, and goes, and whoever stopped it returns.
func TestStoppingDuringAPurgeDoesNotDeadlock(t *testing.T) {
	t.Run("Delete all, with the purge worker mid-purge", func(t *testing.T) {
		s := newTwitchScene(t)
		s.putLinks(t, &twitchLinks{Users: map[string]twitchLink{}, Purge: map[string]twitchPurgeEntry{"1001": {User: twDave, At: 1}}})
		live := s.p.twitchLive()

		held := newGate(t)
		month := kvTwitchAirMonthPrefix + "2023-01"
		s.faults.setOnRead(func(key string) {
			if key == month {
				held.Hold()
			}
		})

		s.p.startTwitchPurges()
		<-held.entered

		cancelled := make(chan struct{})
		live.purgeLock.Lock()
		cancel := live.purgeCancel
		live.purgeCancel = func() { cancel(); close(cancelled) }
		live.purgeLock.Unlock()

		finished := make(chan error, 1)
		go func() { finished <- s.p.deleteAllTwitchData(s.now()) }()

		// The hook holds the store's lock while it waits, so it is
		// released before anything else touches the store.
		<-cancelled
		held.Release()
		require.NoError(t, <-finished)
		s.faults.setOnRead(nil)
		require.Empty(t, s.links(t).Purge)
	})

	t.Run("a settings save, with the loop inside recordTwitchTime", func(t *testing.T) {
		s := newTwitchScene(t)
		s.connected(t, map[string]twitchUser{twDave: daveTwitch})
		s.fake.goLive(daveTwitch, "Live", "Chess", 1, s.now().Add(-time.Minute))
		live := s.p.twitchLive()

		held := newGate(t)
		s.faults.setOnRead(func(key string) {
			if key == kvTwitchAirTotals {
				held.Hold()
			}
		})

		s.p.restartTwitchLive()
		<-held.entered

		cancelled := make(chan struct{})
		live.runLock.Lock()
		cancel := live.cancel
		live.cancel = func() { cancel(); close(cancelled) }
		live.runLock.Unlock()

		cfg := s.p.getConfiguration().Clone()
		cfg.EnableTwitchLive = false
		s.p.setConfiguration(cfg)

		finished := make(chan struct{})
		go func() {
			s.p.restartTwitchLive()
			close(finished)
		}()

		<-cancelled
		held.Release()
		<-finished
		s.faults.setOnRead(nil)

		live.runLock.Lock()
		require.Nil(t, live.cancel, "the loop is gone and nothing replaced it")
		live.runLock.Unlock()
	})
}

// A Twitch connection never unlocks anything (the lock's T7).
func TestATwitchConnectionUnlocksNothing(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twDan: daveTwitch})

	level, _, err := s.p.accessOf(twDan)
	require.NoError(t, err)
	require.Equal(t, accessLocked, level)

	me := s.me(t, twDan)
	require.Equal(t, "locked", me.Access)
	require.Equal(t, "dave_tv", me.Twitch.Login, "and they can still see, and withdraw, their connection")

	response := s.serve(http.MethodGet, "/api/v1/nowplaying", twDan, web(twDan), "", nil)
	require.Equal(t, http.StatusForbidden, response.Code)
}

// The panel learns of every change through one empty event.
func TestTheTwitchEventIsEmptyAndPersonal(t *testing.T) {
	s := newTwitchScene(t)
	s.connectAs(t, twDave, daveTwitch)

	for _, call := range s.api.Calls {
		if call.Method != "PublishWebSocketEvent" || call.Arguments[0] != wsEventMe {
			continue
		}
		broadcast, ok := call.Arguments[2].(*model.WebsocketBroadcast)
		require.True(t, ok)
		require.Equal(t, twDave, broadcast.UserId)

		encoded, err := json.Marshal(call.Arguments[1])
		require.NoError(t, err)
		require.Equal(t, `{"artwork":{"emblem":false,"ghost":false},"name":"Matty"}`, string(encoded), "nothing about anybody")
	}
}
