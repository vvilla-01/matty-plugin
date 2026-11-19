package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Twitch airtime (design/twitch-live.md §7, §11.1): its own ledger, paid
// spans so a simulcast is counted once and as Broadcast Box time, and boards
// and medals by person.

// bbOf is somebody's Broadcast Box stream, linked by hand, as decorate makes it.
func bbOf(key, userID, title string, start time.Time) apiStream {
	return apiStream{StreamKey: key, Title: title, StreamStart: start.UTC().Format(time.RFC3339Nano), UserID: userID, LinkSource: linkSourceManual}
}

// twOf is a Twitch stream as a good read lists it.
func twOf(who twitchUser, start time.Time) twitchLive {
	return twitchLive{TwitchID: who.ID, Login: who.Login, Name: who.DisplayName, StartedAt: start.UTC().Format(time.RFC3339)}
}

func (s *twitchScene) bb(at time.Time, streams ...apiStream) {
	s.p.recordStreamTime(streams, s.p.getConfiguration(), at)
}

func (s *twitchScene) tw(at time.Time, listed ...twitchLive) {
	s.p.recordTwitchTime(listed, s.p.getConfiguration(), at)
}

func (s *twitchScene) seconds(t *testing.T, table, key string) int64 {
	t.Helper()

	return s.table(t, table)[key].Seconds
}

func TestPaidSpanArithmetic(t *testing.T) {
	bb := func(from, to int64) paidSpan { return paidSpan{From: from, To: to} }
	tw := func(from, to int64, id string) paidSpan { return paidSpan{From: from, To: to, Tw: id} }

	for _, tried := range []struct {
		name     string
		from, to int64
		paid     []paidSpan
		unpaid   []paidSpan
		twitch   []paidSpan
	}{
		{"nothing paid", 0, 10, nil, []paidSpan{bb(0, 10)}, nil},
		{"empty interval", 5, 5, []paidSpan{bb(0, 10)}, nil, nil},
		{"all paid, by containment", 2, 8, []paidSpan{bb(0, 10)}, nil, nil},
		{"overlap at the start", 0, 10, []paidSpan{bb(-5, 3)}, []paidSpan{bb(3, 10)}, nil},
		{"overlap at the end", 0, 10, []paidSpan{tw(7, 20, "1")}, []paidSpan{bb(0, 7)}, []paidSpan{tw(7, 10, "1")}},
		{"a hole in the middle", 0, 10, []paidSpan{tw(3, 5, "1")}, []paidSpan{bb(0, 3), bb(5, 10)}, []paidSpan{tw(3, 5, "1")}},
		{"touching edges", 10, 20, []paidSpan{bb(0, 10), tw(20, 30, "1")}, []paidSpan{bb(10, 20)}, nil},
		{"out of order and overlapping", 0, 30, []paidSpan{tw(20, 25, "2"), bb(5, 12), tw(10, 15, "1")}, []paidSpan{bb(0, 5), bb(15, 20), bb(25, 30)}, []paidSpan{tw(10, 15, "1"), tw(20, 25, "2")}},
	} {
		require.Equal(t, tried.unpaid, unpaidParts(tried.from, tried.to, tried.paid), tried.name)
		require.Equal(t, tried.twitch, twitchPaidParts(tried.from, tried.to, tried.paid), tried.name)
	}

	// withPaid: what it overlaps is overwritten, and touching spans of one
	// payer merge.
	require.Equal(t, []paidSpan{bb(0, 10)}, withPaid(nil, 0, 10, ""))
	require.Equal(t, []paidSpan{bb(0, 20)}, withPaid([]paidSpan{bb(0, 10)}, 10, 20, ""), "touching, same payer")
	require.Equal(t, []paidSpan{bb(0, 10), tw(10, 20, "1")}, withPaid([]paidSpan{bb(0, 10)}, 10, 20, "1"), "touching, another payer")
	require.Equal(t, []paidSpan{tw(0, 5, "1"), bb(5, 15), tw(15, 20, "1")}, withPaid([]paidSpan{tw(0, 20, "1")}, 5, 15, ""), "Broadcast Box takes the middle")
	require.Equal(t, []paidSpan{bb(0, 30)}, withPaid([]paidSpan{bb(0, 10), tw(10, 20, "1"), bb(20, 30)}, 10, 20, ""), "and the three become one")
	require.Equal(t, []paidSpan{tw(0, 10, "1")}, withPaid([]paidSpan{tw(0, 10, "1")}, 4, 4, ""), "an empty span changes nothing")
	original := []paidSpan{tw(0, 20, "1")}
	_ = withPaid(original, 5, 15, "")
	require.Equal(t, []paidSpan{tw(0, 20, "1")}, original, "pure")

	// prunePaid: ended before the cutoff go, and beyond the limit the oldest.
	require.Equal(t, []paidSpan{bb(20, 30), bb(40, 50)}, prunePaid([]paidSpan{bb(40, 50), bb(0, 10), bb(20, 30)}, 10, 5))
	require.Equal(t, []paidSpan{bb(20, 30), bb(40, 50)}, prunePaid([]paidSpan{bb(0, 15), bb(20, 30), bb(40, 50)}, 0, 2))
	require.Empty(t, prunePaid(nil, 0, 2))
}

func TestTwitchTimeIsCreditedFromGoodReadsOnly(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})
	start := s.now()
	s.fake.goLive(daveTwitch, "Live", "Chess", 1, start)

	s.clock.advance(time.Minute)
	s.step(t)
	require.EqualValues(t, 60, s.seconds(t, kvTwitchAirTotals, "1001"))

	// Missed once: still on the card, but this read did not list it.
	s.fake.goOffline(daveTwitch.ID)
	s.clock.advance(time.Minute)
	s.step(t)
	require.Len(t, s.p.twitchLive().liveNow(s.now()), 1)
	require.EqualValues(t, 60, s.seconds(t, kvTwitchAirTotals, "1001"), "a stream kept by grace earns nothing")

	// A failed read earns nothing either.
	s.fake.goLive(daveTwitch, "Live", "Chess", 1, start)
	s.fake.setStreamsStatus(500)
	s.clock.advance(time.Minute)
	s.step(t)
	require.EqualValues(t, 60, s.seconds(t, kvTwitchAirTotals, "1001"))

	// Back on a good read, the session is paid up to now from its own start.
	s.fake.setStreamsStatus(0)
	s.clock.advance(2 * time.Minute)
	s.step(t)
	require.EqualValues(t, 300, s.seconds(t, kvTwitchAirTotals, "1001"))
	require.EqualValues(t, 300, s.seconds(t, twitchMonthBucket(s.now()), "1001"))
	require.Equal(t, "Dave", s.table(t, kvTwitchAirTotals)["1001"].Title, "named as the person, as Broadcast Box rows are")
}

func TestASimulcastIsCountedOnce(t *testing.T) {
	type pass struct {
		twitch bool
		at     time.Duration
	}
	bbAt := func(at time.Duration) pass { return pass{at: at} }
	twAt := func(at time.Duration) pass { return pass{twitch: true, at: at} }
	second := time.Second
	minute := time.Minute

	for _, tried := range []struct {
		name       string
		bbStart    time.Duration // relative to the scene's start
		twStart    time.Duration
		passes     []pass
		wantBB, tw int64
	}{
		{name: "the same poll, Twitch first", passes: []pass{twAt(60 * second), bbAt(60 * second)}, wantBB: 60},
		{name: "the same poll, Broadcast Box first", passes: []pass{bbAt(60 * second), twAt(60 * second)}, wantBB: 60},
		{
			name:   "Twitch reads landing between Broadcast Box polls",
			passes: []pass{bbAt(15 * second), bbAt(30 * second), bbAt(45 * second), twAt(60 * second), bbAt(60 * second), bbAt(75 * second), twAt(120 * second), bbAt(120 * second)},
			wantBB: 120,
		},
		{name: "after a restart with different starts, Twitch first", bbStart: 0, twStart: -20 * minute, passes: []pass{twAt(60 * minute), bbAt(60 * minute)}, wantBB: 3600, tw: 1200},
		{name: "after a restart with different starts, Broadcast Box first", bbStart: 0, twStart: -20 * minute, passes: []pass{bbAt(60 * minute), twAt(60 * minute)}, wantBB: 3600, tw: 1200},
		{
			name:   "a Broadcast Box backlog after an outage",
			passes: []pass{bbAt(15 * second), bbAt(60 * second), twAt(60 * second), twAt(120 * second), twAt(180 * second), twAt(240 * second), bbAt(240 * second)},
			wantBB: 240,
		},
	} {
		t.Run(tried.name, func(t *testing.T) {
			s := newTwitchScene(t)
			origin := s.now()
			s.connected(t, map[string]twitchUser{twDave: daveTwitch})

			dave := bbOf("videokey-dave", twDave, "Dave", origin.Add(tried.bbStart))
			onTwitch := twOf(daveTwitch, origin.Add(tried.twStart))

			for _, next := range tried.passes {
				if next.twitch {
					s.tw(origin.Add(next.at), onTwitch)
				} else {
					s.bb(origin.Add(next.at), dave)
				}
			}

			month := origin
			require.Equal(t, tried.wantBB, s.seconds(t, kvStatsTotals, "videokey-dave"), "Broadcast Box totals")
			require.Equal(t, tried.wantBB, s.seconds(t, monthBucket(month), "videokey-dave"), "Broadcast Box month")
			require.Equal(t, tried.tw, s.seconds(t, kvTwitchAirTotals, "1001"), "Twitch totals hold only Twitch-only seconds")
			require.Equal(t, tried.tw, s.seconds(t, twitchMonthBucket(month), "1001"), "and so does Twitch's month")

			rows := s.p.boardRows(s.table(t, kvStatsTotals), s.table(t, kvTwitchAirTotals), s.p.getConfiguration(), nil, map[string]string{"1001": twDave})
			require.Len(t, rows, 1)
			require.Equal(t, tried.wantBB+tried.tw, rows[0].seconds, "every second once")
		})
	}
}

func TestASimulcastSurvivesDisconnecting(t *testing.T) {
	s := newTwitchScene(t)
	s.p.twitchLive().purgeOff = true
	origin := s.now()
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})
	dave := bbOf("videokey-dave", twDave, "Dave", origin)
	onTwitch := twOf(daveTwitch, origin)

	// Ten minutes on both, Twitch's reads landing between the polls.
	for at := 15 * time.Second; at <= 10*time.Minute; at += 15 * time.Second {
		s.bb(origin.Add(at), dave)
		if at%time.Minute == 45*time.Second {
			s.tw(origin.Add(at+7*time.Second), onTwitch)
		}
	}
	s.tw(origin.Add(10*time.Minute), onTwitch)
	s.bb(origin.Add(10*time.Minute), dave)

	require.NoError(t, s.p.disconnectTwitch(twDave, false, s.now().UnixMilli()))
	for {
		more, err := s.p.purgeStep(t.Context(), s.now())
		require.NoError(t, err)
		if !more {
			break
		}
	}

	require.EqualValues(t, 600, s.seconds(t, kvStatsTotals, "videokey-dave"), "all ten minutes, on Broadcast Box")
	require.Empty(t, s.table(t, kvTwitchAirTotals))

	rows := s.p.boardRows(s.table(t, kvStatsTotals), s.table(t, kvTwitchAirTotals), s.p.getConfiguration(), nil, nil)
	require.EqualValues(t, 600, rows[0].seconds)

	// And they are in what an admin exports, which is Broadcast Box's alone.
	response := s.serve("GET", "/api/v1/admin/airtime", twBoss, web(twBoss), "", nil)
	require.Equal(t, 200, response.Code, response.Body.String())

	var export airtimeExport
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &export))
	require.EqualValues(t, 600, export.Totals["videokey-dave"].Seconds)
	require.Len(t, export.Totals, 1, "no Twitch row in it")
}

func TestTheFirstPollAfterARestartDoesNotDoubleCount(t *testing.T) {
	for _, twitchFirst := range []bool{true, false} {
		name := "Broadcast Box first"
		if twitchFirst {
			name = "Twitch first"
		}

		t.Run(name, func(t *testing.T) {
			s := newTwitchScene(t)
			origin := s.now()
			connected := origin.Add(-2 * time.Hour).UnixMilli()
			s.putLinks(t, &twitchLinks{Users: map[string]twitchLink{twDave: {ID: "1001", Login: "dave_tv", At: connected, Checked: origin.UnixMilli()}}})

			// A fresh process: nothing cached, nothing observed.
			s.p.twitchLive().clearLive(true)
			s.fake.goLive(daveTwitch, "Live", "Chess", 1, origin.Add(-80*time.Minute))
			dave := bbOf("videokey-dave", twDave, "Dave", origin.Add(-60*time.Minute))

			if twitchFirst {
				s.step(t)
				s.bb(origin, dave)
			} else {
				s.bb(origin, dave)
				s.step(t)
			}

			require.EqualValues(t, 3600, s.seconds(t, kvStatsTotals, "videokey-dave"))
			require.EqualValues(t, 1200, s.seconds(t, kvTwitchAirTotals, "1001"))
		})
	}

	t.Run("the links cannot be read", func(t *testing.T) {
		s := newTwitchScene(t)
		s.connected(t, map[string]twitchUser{twDave: daveTwitch})
		s.p.twitchLive().clearLive(true)
		s.faults.failRead(kvTwitchLinks)

		previous := &snapshot{
			Streams: []apiStream{{Handle: "h", AllTimeRank: 1}},
			AllTime: []leaderEntry{{Rank: 1, Title: "Dave", Seconds: 7}},
		}
		s.p.setSnapshot(previous)

		s.bb(s.now(), bbOf("videokey-dave", twDave, "Dave", s.now().Add(-time.Hour)))
		require.Nil(t, s.kv(kvStatsTotals), "the pass is skipped, never guessed")
		require.Nil(t, s.kv(kvStatsSessions), "and its marks are not advanced")

		current := &snapshot{Streams: []apiStream{{Handle: "h"}}}
		s.p.addBoards(current, s.p.getConfiguration(), s.now())
		require.Equal(t, previous.AllTime, current.AllTime, "the boards are carried")
		require.Equal(t, 1, current.Streams[0].AllTimeRank)
	})
}

func TestPaidSpansFollowTheBoardsOwnership(t *testing.T) {
	s := newTwitchScene(t)
	origin := s.now()
	s.connected(t, map[string]twitchUser{twDave: daveTwitch, twErin: erinTwitch})

	// Erin's Twitch read lands first; her Broadcast Box stream is matched
	// to her only by username, so the boards keep it as a row of its own,
	// and the paid spans leave it alone.
	byName := bbOf("erin", twErin, "Erin", origin)
	byName.LinkSource = linkSourceUsername

	// Dave's, linked by hand, is settled.
	dave := bbOf("videokey-dave", twDave, "Dave", origin)

	s.tw(origin.Add(time.Minute), twOf(erinTwitch, origin), twOf(daveTwitch, origin))
	s.bb(origin.Add(time.Minute), byName, dave)

	require.EqualValues(t, 60, s.seconds(t, kvTwitchAirTotals, "1002"), "nothing taken back")
	require.Zero(t, s.seconds(t, kvTwitchAirTotals, "1001"))
	require.EqualValues(t, 60, s.seconds(t, kvStatsTotals, "erin"), "the username match is still paid as it always was")

	paid, err := s.p.loadTwitchPaid()
	require.NoError(t, err)
	require.Equal(t, []paidSpan{{From: origin.Unix(), To: origin.Unix() + 60, Tw: "1002"}}, paid[twErin], "no span recorded for it")
	require.Equal(t, []paidSpan{{From: origin.Unix(), To: origin.Unix() + 60}}, paid[twDave])
}

func TestTwitchTimeInAnEndedMonthForgetsThePodiums(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})
	midnight := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	start := midnight.Add(-10 * time.Minute)

	generation := func() uint64 {
		s.p.endedPodiums.lock.Lock()
		defer s.p.endedPodiums.lock.Unlock()

		return s.p.endedPodiums.generation
	}

	before := generation()
	s.tw(midnight.Add(10*time.Minute), twOf(daveTwitch, start))
	require.EqualValues(t, 600, s.seconds(t, kvTwitchAirMonthPrefix+"2026-10", "1001"))
	require.EqualValues(t, 600, s.seconds(t, kvTwitchAirMonthPrefix+"2026-11", "1001"))
	require.Greater(t, generation(), before, "Twitch time across midnight on the 1st")

	// Broadcast Box taking October's minutes back from Twitch.
	before = generation()
	s.p.lastPollAt = 0
	s.bb(midnight.Add(10*time.Minute), bbOf("videokey-dave", twDave, "Dave", start))
	require.Zero(t, s.seconds(t, kvTwitchAirMonthPrefix+"2026-10", "1001"))
	require.Greater(t, generation(), before, "a debit into an ended month")

	months := newMonthTables(s.p, kvTwitchAirMonthPrefix)
	_, err := months.table(kvTwitchAirMonthPrefix + "2026-10")
	require.NoError(t, err)
	require.True(t, months.reachesBeforeMonth(midnight))
	require.False(t, months.reachesBeforeMonth(midnight.Add(-time.Minute)))
}

func TestTimeBeforeConnectingNeverCounts(t *testing.T) {
	s := newTwitchScene(t)
	start := s.now()
	connectedAt := start.Add(30 * time.Minute)

	s.putLinks(t, &twitchLinks{Users: map[string]twitchLink{twDave: {ID: "1001", Login: "dave_tv", At: connectedAt.UnixMilli(), Checked: connectedAt.UnixMilli()}}})

	// A fresh process (no marks, no evidence), reading forty minutes in.
	s.tw(start.Add(40*time.Minute), twOf(daveTwitch, start))
	require.EqualValues(t, 600, s.seconds(t, kvTwitchAirTotals, "1001"), "only the ten minutes since connecting")

	s.tw(start.Add(41*time.Minute), twOf(daveTwitch, start))
	require.EqualValues(t, 660, s.seconds(t, kvTwitchAirTotals, "1001"))
}

func TestSwitchingTwitchOnResyncs(t *testing.T) {
	s := newTwitchScene(t)
	start := s.now()
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})

	switchedOn := start.Add(30 * time.Minute)
	s.p.armTwitchResync(switchedOn.UnixMilli())

	s.tw(start.Add(31*time.Minute), twOf(daveTwitch, start))
	require.Zero(t, s.seconds(t, kvTwitchAirTotals, "1001"), "nothing for the time before")

	s.tw(start.Add(32*time.Minute), twOf(daveTwitch, start))
	require.EqualValues(t, 60, s.seconds(t, kvTwitchAirTotals, "1001"), "once, then counted as usual")

	// Switching the leaderboard back on resyncs Twitch sessions too.
	s.p.armStatsResync(start.Add(40 * time.Minute).UnixMilli())
	s.tw(start.Add(41*time.Minute), twOf(daveTwitch, start))
	require.EqualValues(t, 60, s.seconds(t, kvTwitchAirTotals, "1001"))
	require.Equal(t, start.Add(40*time.Minute).UnixMilli(), s.p.twitchResyncEpoch(start.Add(41*time.Minute).UnixMilli()), "the later of the two")
}

func TestBroadcastBoxRecordingIsUnchangedWithTwitchOff(t *testing.T) {
	for _, tried := range []struct {
		name string
		cfg  func() *configuration
	}{
		{"switched off", func() *configuration {
			cfg := leaderboardConfig()
			cfg.TwitchClientID, cfg.TwitchClientSecret = testTwitchClientID, testTwitchClientSecret
			return cfg
		}},
		{"on, with nobody connected", func() *configuration {
			cfg := leaderboardConfig()
			cfg.TwitchClientID, cfg.TwitchClientSecret, cfg.EnableTwitchLive = testTwitchClientID, testTwitchClientSecret, true
			return cfg
		}},
	} {
		t.Run(tried.name, func(t *testing.T) {
			p, _, faults := kvBackedPluginWithFaults(map[string][]byte{})
			faults.setOnRead(func(key string) {
				require.False(t, strings.HasPrefix(key, "twitchair_"), "%s was read", key)
			})
			cfg := tried.cfg()

			start := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
			streams := []apiStream{bbOf("videokey-villa", "u-villa", "Villa", start)}

			p.recordStreamTime(streams, cfg, start.Add(30*time.Second))
			p.recordStreamTime(streams, cfg, start.Add(90*time.Second))
			require.EqualValues(t, 90, hours(p, kvStatsTotals, "videokey-villa"))
			require.EqualValues(t, 90, hours(p, monthBucket(start), "videokey-villa"))
		})
	}
}

func TestADamagedTwitchLedgerHaltsOnlyTwitch(t *testing.T) {
	s := newTwitchScene(t)
	origin := s.now()
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})
	s.setKV(kvTwitchAirTotals, []byte("{damaged"))

	s.tw(origin.Add(time.Minute), twOf(daveTwitch, origin))
	halted, reason := s.p.twitchAirHalt.state()
	require.True(t, halted)
	require.Contains(t, reason, kvTwitchAirTotals)
	require.Equal(t, []byte("{damaged"), s.kv(kvQuarantinePrefix+kvTwitchAirTotals))
	require.Equal(t, []byte("{damaged"), s.kv(kvTwitchAirTotals), "never overwritten")

	statsHalted, _ := s.p.statsHalt()
	require.False(t, statsHalted)
	s.bb(origin.Add(time.Minute), bbOf("videokey-dave", twDave, "Dave", origin))
	require.EqualValues(t, 60, s.seconds(t, kvStatsTotals, "videokey-dave"), "Broadcast Box still records")

	// The admin is told.
	require.Contains(t, s.me(t, twBoss).Twitch.Problem, "Twitch hours are not being recorded")
	require.NotEmpty(t, s.p.adminTwitchStatus().Hours.Halted)
}

// A link document that will not decode (one a later build wrote, say) is set
// aside until an admin deletes all Twitch data. That is a standing problem,
// not a passing one, so it never stops Broadcast Box hours for anybody:
// Broadcast Box is paid in full and ranked on its own, as with the switch off.
func TestAnUnreadableLinkDocumentNeverStopsBroadcastBoxHours(t *testing.T) {
	later := []byte(`{"v":2,"users":{}}`)

	for _, tried := range []struct {
		name      string
		loopFirst bool
	}{
		{"after the loop has set it aside", true},
		{"before the loop has looked", false},
	} {
		t.Run(tried.name, func(t *testing.T) {
			s := newTwitchScene(t)
			origin := s.now()
			s.connected(t, map[string]twitchUser{twDave: daveTwitch})
			s.seedTable(t, kvTwitchAirTotals, statTable{"1001": {Seconds: 600, Title: "Dave"}})
			s.setKV(kvTwitchLinks, later)

			// A fresh process: nothing cached.
			s.p.twitchLive().clearLive(true)
			if tried.loopFirst {
				s.step(t)
				require.Equal(t, later, s.kv(kvQuarantinePrefix+kvTwitchLinks), "set aside")
			}

			previous := &snapshot{
				Streams: []apiStream{{Handle: "h", AllTimeRank: 3}},
				AllTime: []leaderEntry{{Rank: 1, Title: "Stale", Seconds: 7}},
			}
			s.p.setSnapshot(previous)

			bob := bbOf("videokey-bob", twBob, "Bob", origin.Add(30*time.Second))
			dave := bbOf("videokey-dave", twDave, "Dave", origin)
			s.bb(origin.Add(30*time.Second), bob, dave)
			s.bb(origin.Add(90*time.Second), bob, dave)

			require.EqualValues(t, 60, s.seconds(t, kvStatsTotals, "videokey-bob"), "somebody with no Twitch at all")
			require.EqualValues(t, 90, s.seconds(t, kvStatsTotals, "videokey-dave"), "and somebody who had connected")
			require.EqualValues(t, 90, s.seconds(t, monthBucket(origin), "videokey-dave"))
			require.Nil(t, s.kv(kvTwitchAirPaid), "no paid spans: nobody is known to be connected")
			require.Equal(t, later, s.kv(kvTwitchLinks), "and the document is never overwritten")

			dave.Handle = "h"
			current := &snapshot{Streams: []apiStream{dave}}
			s.p.addBoards(current, s.p.getConfiguration(), origin.Add(90*time.Second))
			require.Equal(t, []leaderEntry{
				{Rank: 1, Title: "Dave", Seconds: 90, UserID: twDave, Username: "dave", DisplayName: "Dave"},
				{Rank: 2, Title: "Bob", Seconds: 60, UserID: twBob, Username: "bob", DisplayName: "Bob"},
			}, current.AllTime, "built afresh, with the Twitch row nobody can be said to own left out")
			require.Equal(t, 1, current.Streams[0].AllTimeRank, "and the card's place is this poll's, not carried")
		})
	}

	t.Run("a document that decodes again resyncs Twitch, so a simulcast is not paid twice", func(t *testing.T) {
		s := newTwitchScene(t)
		origin := s.now()
		s.connected(t, map[string]twitchUser{twDave: daveTwitch})
		s.tw(origin.Add(time.Minute), twOf(daveTwitch, origin))
		require.EqualValues(t, 60, s.seconds(t, kvTwitchAirTotals, "1001"))

		good := s.kv(kvTwitchLinks)
		s.setKV(kvTwitchLinks, later)
		s.step(t)

		// Half an hour live on both, which only Broadcast Box can pay while
		// nobody is known to be connected.
		dave := bbOf("videokey-dave", twDave, "Dave", origin)
		s.bb(origin.Add(2*time.Minute), dave)
		s.bb(origin.Add(30*time.Minute), dave)
		require.EqualValues(t, 1800, s.seconds(t, kvStatsTotals, "videokey-dave"))

		s.setKV(kvTwitchLinks, good)
		s.clock.advance(30 * time.Minute)
		s.fake.goLive(daveTwitch, "Live", "Chess", 1, origin)
		s.step(t)
		require.EqualValues(t, 60, s.seconds(t, kvTwitchAirTotals, "1001"), "nothing for the stretch Broadcast Box paid")

		s.clock.advance(time.Minute)
		s.step(t)
		require.EqualValues(t, 120, s.seconds(t, kvTwitchAirTotals, "1001"), "then counted as usual")
	})
}

func TestBoardsAreByPerson(t *testing.T) {
	s := newTwitchScene(t)
	cfg := s.p.getConfiguration().Clone()
	cfg.StreamNameOverrides = "videokey-dan = Dan's stream"
	cfg.normalize()

	bb := statTable{
		"videokey-dave": {Seconds: 100, Title: "Dave"},
		"videokey-dan":  {Seconds: 130, Title: "videokey-dan"},
		"videokey-bob":  {Seconds: 50, Title: "Bob"},
	}
	tw := statTable{
		"1001": {Seconds: 30, Title: "Dave"},
		"1002": {Seconds: 50, Title: "Erin"},
		"9999": {Seconds: 999, Title: "Nobody"},
	}
	owners := map[string]string{"1001": twDave, "1002": twErin}

	rows := s.p.boardRows(bb, tw, cfg, nil, owners)
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.id)
	}
	require.Equal(t, []string{"u:" + twDave, "k:videokey-dan", "u:" + twBob, "u:" + twErin}, ids,
		"one row a person, an unlinked stream keeps its own, an orphan Twitch row is left out, and a tie is settled by name")

	board, total := s.p.leaderboard("all time", rows, maxLeaderboardRows)
	require.Equal(t, 4, total)
	require.Equal(t, leaderEntry{Rank: 1, Title: "Dave", Seconds: 130, UserID: twDave, Username: "dave", DisplayName: "Dave", TwitchSeconds: 30}, board[0])
	require.Equal(t, leaderEntry{Rank: 2, Title: "Dan's stream", Seconds: 130}, board[1])
	require.Equal(t, leaderEntry{Rank: 4, Title: "Erin", Seconds: 50, UserID: twErin, Username: "erin", DisplayName: "Erin", TwitchSeconds: 50}, board[3])

	encoded, err := json.Marshal(board)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "videokey-dan")
	require.NotContains(t, string(encoded), "1001")

	for i := 0; i < 10; i++ {
		require.Equal(t, rows, s.p.boardRows(bb, tw, cfg, nil, owners), "a total order")
	}
}

func TestTwitchHoursHideWhileTheSwitchIsOff(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twErin: erinTwitch})
	s.seedTable(t, kvTwitchAirTotals, statTable{"1002": {Seconds: 900, Title: "Erin"}})
	s.seedTable(t, twitchMonthBucket(s.now()), statTable{"1002": {Seconds: 900, Title: "Erin"}})

	on := &snapshot{}
	s.p.addBoards(on, s.p.getConfiguration(), s.now())
	require.Len(t, on.AllTime, 1)
	require.Len(t, on.Month, 1)

	cfg := s.p.getConfiguration().Clone()
	cfg.EnableTwitchLive = false
	s.p.setConfiguration(cfg)

	off := &snapshot{}
	s.p.addBoards(off, cfg, s.now())
	require.Empty(t, off.AllTime)
	require.Empty(t, off.Month)
	require.EqualValues(t, 900, s.seconds(t, kvTwitchAirTotals, "1002"), "kept, to show again when it is switched back on")
}

func TestCardMedalsAreByPerson(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twDave: daveTwitch, twErin: erinTwitch})
	s.seedTable(t, kvStatsTotals, statTable{"videokey-bob": {Seconds: 500, Title: "Bob"}, "videokey-dave": {Seconds: 300, Title: "Dave"}})
	s.seedTable(t, kvTwitchAirTotals, statTable{"1001": {Seconds: 300}, "1002": {Seconds: 50}})

	streams := []apiStream{
		{StreamKey: "videokey-dave", Handle: "bb-dave", Title: "Dave", UserID: twDave, LinkSource: linkSourceManual},
		{Source: sourceTwitch, Handle: "tw-dave", Title: "Dave", UserID: twDave, TwitchID: "1001"},
		{Source: sourceTwitch, Handle: "tw-erin", Title: "Erin", UserID: twErin, TwitchID: "1002"},
		{StreamKey: "videokey-bob", Handle: "bb-bob", Title: "Bob", UserID: twBob, LinkSource: linkSourceManual},
	}
	current := &snapshot{Streams: streams}
	s.p.addBoards(current, s.p.getConfiguration(), s.now())

	ranks := map[string]int{}
	for _, stream := range current.Streams {
		ranks[stream.Handle] = stream.AllTimeRank
	}
	require.Equal(t, map[string]int{"bb-dave": 1, "tw-dave": 1, "tw-erin": 3, "bb-bob": 2}, ranks,
		"Dave's two sources are one person in first place, on both his cards")
	require.Zero(t, streams[0].AllTimeRank, "written on a copy")
}

func TestPastMonthMedalsMergeBeforeTheCut(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twErin: erinTwitch})
	s.seedTable(t, kvStatsMonthPrefix+"2026-08", statTable{
		"videokey-dave": {Seconds: 400, Title: "Dave"},
		"videokey-bob":  {Seconds: 300, Title: "Bob"},
		"videokey-erin": {Seconds: 200, Title: "Erin"},
		"videokey-boss": {Seconds: 100, Title: "Victor"},
	})
	s.seedTable(t, kvTwitchAirMonthPrefix+"2026-08", statTable{
		"1002": {Seconds: 250, Title: "Erin"},
	})

	owners, err := s.p.twitchLive().owners(s.p.getConfiguration())
	require.NoError(t, err)

	tally := s.p.monthMedalTally(s.now(), newIdentityResolver(s.p), owners, true)
	byUser := map[string]monthMedals{}
	for _, medals := range tally {
		byUser[medals.UserID] = medals
	}
	require.Equal(t, []string{"2026-08"}, byUser[twErin].Gold, "third on Broadcast Box, and with Twitch the month's winner")
	require.Equal(t, []string{"2026-08"}, byUser[twDave].Silver)
	require.Equal(t, []string{"2026-08"}, byUser[twBob].Bronze)
	require.NotContains(t, byUser, twBoss)
}

func TestTheMonthsAreReadAgainWhenTwitchIsSwitchedOn(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twErin: erinTwitch})
	s.seedTable(t, kvStatsMonthPrefix+"2026-08", statTable{"videokey-dave": {Seconds: 400}, "videokey-erin": {Seconds: 300}})
	s.seedTable(t, kvTwitchAirMonthPrefix+"2026-08", statTable{"1002": {Seconds: 250}})

	reads := 0
	s.faults.setOnRead(func(key string) {
		if strings.HasPrefix(key, kvTwitchAirMonthPrefix) {
			reads++
		}
	})

	off := s.p.monthMedalTally(s.now(), newIdentityResolver(s.p), nil, false)
	require.Zero(t, reads, "Twitch's months are not read while it is off")
	require.Len(t, off, 2)

	owners, err := s.p.twitchLive().owners(s.p.getConfiguration())
	require.NoError(t, err)
	on := s.p.monthMedalTally(s.now(), newIdentityResolver(s.p), owners, true)
	require.Positive(t, reads, "a reading without Twitch is stale once it is on")
	for _, medals := range on {
		if medals.UserID == twErin {
			require.Equal(t, []string{"2026-08"}, medals.Gold)
		}
	}

	again := reads
	s.p.monthMedalTally(s.now(), newIdentityResolver(s.p), owners, true)
	require.Equal(t, again, reads, "and then kept, as before")
}
