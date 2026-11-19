package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Who is live on Twitch, and how it reaches the stream list
// (design/twitch-live.md §5, §8, §11.1).

// origin is a Broadcast Box for the scene's poller, answering with whatever
// answer says.
func (s *twitchScene) origin(t *testing.T, answer func() (int, []bbStream)) *atomic.Int32 {
	t.Helper()

	var asked atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}

		status, streams := answer()
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(streams)
	}))
	t.Cleanup(server.Close)

	cfg := s.p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = server.URL
	s.p.setConfiguration(cfg)
	s.p.httpClient = server.Client()

	return &asked
}

func (s *twitchScene) poll(t *testing.T) *snapshot {
	t.Helper()

	s.p.pollOnce(t.Context(), true)

	return s.p.getSnapshot()
}

func liveRaw(key string, started time.Time) bbStream {
	return bbStream{StreamKey: key, StreamStart: started.UTC().Format(time.RFC3339), VideoTracks: []bbVideoTrack{{RID: "Video"}}}
}

func TestNothingIsAskedWhileOffOrNobodyIsConnected(t *testing.T) {
	t.Run("switched off", func(t *testing.T) {
		s := newTwitchScene(t)
		s.connected(t, map[string]twitchUser{twDave: daveTwitch})
		s.fake.goLive(daveTwitch, "Live", "Chess", 1, s.now())

		cfg := s.p.getConfiguration().Clone()
		cfg.EnableTwitchLive = false
		s.p.setConfiguration(cfg)

		s.step(t)
		require.Empty(t, s.fake.requests())
		require.Nil(t, s.p.twitchStreams(cfg, s.now()))
	})

	t.Run("nobody connected", func(t *testing.T) {
		s := newTwitchScene(t)
		for i := 0; i < 3; i++ {
			s.step(t)
			s.clock.advance(time.Minute)
		}
		require.Empty(t, s.fake.requests(), "not even a token")
	})

	t.Run("a poll with the switch off reads no Twitch key", func(t *testing.T) {
		newMediaFake(t)
		g := newGoldenScene(t, &configuration{TwitchClientID: testTwitchClientID, TwitchClientSecret: testTwitchClientSecret})
		g.p.pollOnce(t.Context(), true)

		for _, key := range g.readKeys() {
			require.False(t, strings.Contains(key, "twitch"), "%s was read", key)
		}
		for _, call := range g.api.Calls {
			require.NotContains(t, []string{"GetConfig", "GetSession", "KVSetWithExpiry"}, call.Method)
		}
	})
}

func TestGetStreamsIsBatchedByAHundred(t *testing.T) {
	s := newTwitchScene(t)

	doc := &twitchLinks{Users: map[string]twitchLink{}}
	var ids []string
	for i := 0; i < 205; i++ {
		id := fmt.Sprint(100000 + i)
		ids = append(ids, id)
		doc.Users[fmt.Sprintf("someone%019d", i)] = twitchLink{ID: id, Login: "someone" + id}
	}
	s.putLinks(t, doc)

	s.step(t)

	var calls [][]string
	for _, seen := range s.fake.requests() {
		if seen.path != "/helix/streams" {
			continue
		}
		require.Equal(t, "live", seen.query.Get("type"))
		require.Equal(t, "100", seen.query.Get("first"))
		require.LessOrEqual(t, len(seen.query["user_id"]), 100)
		calls = append(calls, seen.query["user_id"])
	}
	require.Len(t, calls, 3)

	var asked []string
	for _, call := range calls {
		asked = append(asked, call...)
	}
	sort.Strings(ids)
	require.Equal(t, ids, asked, "everybody, once, in order")
}

func TestAFailedBatchFailsTheWholePass(t *testing.T) {
	s := newTwitchScene(t)

	doc := &twitchLinks{Users: map[string]twitchLink{}}
	for i := 0; i < 150; i++ {
		id := fmt.Sprint(100000 + i)
		doc.Users[fmt.Sprintf("someone%019d", i)] = twitchLink{ID: id, Login: "someone" + id}
	}
	s.putLinks(t, doc)

	first := twitchUser{ID: "100000", Login: "someone100000", DisplayName: "First"}
	second := twitchUser{ID: "100120", Login: "someone100120", DisplayName: "Second"}
	s.fake.goLive(first, "a", "b", 1, s.now())
	s.fake.goLive(second, "a", "b", 1, s.now())

	s.step(t)
	require.Len(t, s.p.twitchLive().liveNow(s.now()), 2)

	// The first batch now says First has stopped; the second batch fails.
	s.fake.goOffline(first.ID)
	var batch atomic.Int32
	s.fake.route("/helix/streams", func(w http.ResponseWriter, r *http.Request) {
		if batch.Add(1)%2 == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	})

	s.clock.advance(time.Minute)
	s.step(t)

	live := s.p.twitchLive().liveNow(s.now())
	require.Len(t, live, 2, "nobody vanishes because a batch failed, and nobody because another one answered")
	for _, stream := range live {
		require.Zero(t, stream.Missed)
	}
}

func TestAStreamMissedOnceIsKept(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})
	s.fake.goLive(daveTwitch, "Live", "Chess", 1, s.now())

	s.step(t)
	s.fake.goOffline(daveTwitch.ID)
	s.clock.advance(time.Minute)
	s.step(t)

	live := s.p.twitchLive().liveNow(s.now())
	require.Len(t, live, 1, "Get Streams can leave out a stream that is live")
	require.Equal(t, 1, live[0].Missed)
}

func TestAStreamMissedTwiceIsDropped(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})
	s.fake.goLive(daveTwitch, "Live", "Chess", 1, s.now())

	s.step(t)
	s.fake.goOffline(daveTwitch.ID)
	for i := 0; i < 2; i++ {
		s.clock.advance(time.Minute)
		s.step(t)
	}

	require.Empty(t, s.p.twitchLive().liveNow(s.now()))
}

func TestFailedReadsKeepTheListForFiveMinutes(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})
	s.fake.goLive(daveTwitch, "Live", "Chess", 1, s.now())
	s.step(t)

	s.fake.setStreamsStatus(http.StatusInternalServerError)
	for minute := 0; minute < 4; minute++ {
		s.clock.advance(time.Minute)
		s.step(t)
	}
	s.clock.advance(59 * time.Second)
	s.step(t)
	require.Len(t, s.p.twitchLive().liveNow(s.now()), 1, "kept through failed reads")
	require.Positive(t, s.requestsTo("/helix/streams"))

	s.clock.advance(2 * time.Second)
	s.step(t)
	require.Empty(t, s.p.twitchLive().liveNow(s.now()), "a card nobody can confirm is worse than none")
	require.Empty(t, s.p.twitchStreams(s.p.getConfiguration(), s.now()))
}

func TestBackoffDoubles(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})
	s.fake.setStreamsStatus(http.StatusInternalServerError)
	live := s.p.twitchLive()

	var waits []time.Duration
	for i := 0; i < 6; i++ {
		if i > 0 {
			s.clock.advance(waits[i-1])
		}
		s.step(t)

		live.lock.Lock()
		wait := live.nextAt.Sub(s.now())
		problem := live.problem
		live.lock.Unlock()

		waits = append(waits, wait)
		if i < 2 {
			require.Empty(t, problem, "not a problem worth an admin's time yet")
		} else {
			require.Contains(t, problem, "Twitch could not be reached")
		}
	}
	require.Equal(t, []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute, 10 * time.Minute}, waits)

	// A kick does not cut a backoff short.
	live.kick()
	live.lock.Lock()
	require.Equal(t, 10*time.Minute, live.nextAt.Sub(s.now()))
	live.lock.Unlock()

	// A good read ends it.
	s.fake.setStreamsStatus(0)
	s.clock.advance(10 * time.Minute)
	s.step(t)
	live.lock.Lock()
	require.Equal(t, time.Minute, live.nextAt.Sub(s.now()))
	require.Empty(t, live.problem)
	live.lock.Unlock()
}

func TestRateLimitWaitsForReset(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})
	live := s.p.twitchLive()

	reset := s.now().Add(3 * time.Minute).Truncate(time.Second)
	s.fake.lock.Lock()
	s.fake.rate = http.Header{"Ratelimit-Limit": {"800"}, "Ratelimit-Remaining": {"2"}, "Ratelimit-Reset": {fmt.Sprint(reset.Unix())}}
	s.fake.lock.Unlock()

	s.step(t)
	live.lock.Lock()
	require.True(t, live.nextAt.Equal(reset), "nearly out of points: wait for the bucket")
	live.lock.Unlock()

	// A 429 waits for the reset, at least a minute and at most ten.
	s.clock.advance(3 * time.Minute)
	far := s.now().Add(time.Hour)
	s.fake.lock.Lock()
	s.fake.rate = http.Header{"Ratelimit-Reset": {fmt.Sprint(far.Unix())}}
	s.fake.streamsStatus = http.StatusTooManyRequests
	s.fake.lock.Unlock()

	s.step(t)
	live.lock.Lock()
	require.Equal(t, 10*time.Minute, live.nextAt.Sub(s.now()))
	require.Contains(t, live.problem, "Twitch is asking Matty to slow down; checking again at")
	live.lock.Unlock()

	live.kick()
	live.lock.Lock()
	require.Equal(t, 10*time.Minute, live.nextAt.Sub(s.now()), "a kick does not jump the queue")
	live.lock.Unlock()
}

func TestTwitchStreamsCarryNoStreamKey(t *testing.T) {
	s := newTwitchScene(t)
	s.setKV(kvLinkTokenPrefix+twDave, []byte("the-publish-token"))
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})
	s.fake.goLive(daveTwitch, "Live", "Chess", 7, s.now())
	s.step(t)

	twitch := s.p.twitchStreams(s.p.getConfiguration(), s.now())
	require.Len(t, twitch, 1)
	require.Empty(t, twitch[0].StreamKey)
	require.Equal(t, sourceTwitch, twitch[0].Source)

	bb := apiStream{StreamKey: "videokey-dave", Handle: s.p.streamHandle("videokey-dave"), Title: "Dave", UserID: twDave, LinkSource: linkSourceManual}
	encoded, err := json.Marshal(snapshot{Streams: s.p.mergeStreams([]apiStream{bb}, twitch, true)})
	require.NoError(t, err)

	text := string(encoded)
	require.NotContains(t, text, "videokey-dave")
	require.NotContains(t, text, `"1001"`, "the Twitch id never leaves the server")
	require.NotContains(t, text, "the-publish-token")
	require.NotContains(t, text, "app-token")
	require.Contains(t, text, `"twitchLogin":"dave_tv"`)

	handle := s.p.twitchHandle("1001")
	require.Equal(t, handle, twitch[0].Handle)
	require.Equal(t, handle, s.p.twitchHandle("1001"), "stable")
	require.NotEqual(t, s.p.streamHandle("1001"), handle, "never a stream key's handle")
	require.Regexp(t, `^[0-9a-f]{32}$`, handle)

	s.p.handleSecret = nil
	require.Empty(t, s.p.twitchHandle("1001"))
}

func TestTwitchEntriesAppearOnAllThreePaths(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twErin: erinTwitch})
	s.fake.goLive(erinTwitch, "Live", "Art", 3, s.now().Add(-time.Minute))
	s.step(t)
	erin := s.p.twitchHandle(erinTwitch.ID)

	var lock sync.Mutex
	status := http.StatusOK
	s.origin(t, func() (int, []bbStream) {
		lock.Lock()
		defer lock.Unlock()

		return status, []bbStream{liveRaw("videokey-dave", time.Now().Add(-time.Hour))}
	})
	setStatus := func(code int) {
		lock.Lock()
		status = code
		lock.Unlock()
	}

	// Success.
	current := s.poll(t)
	require.True(t, hasCard(current.Streams, erin))
	require.True(t, hasCard(current.Streams, s.p.streamHandle("videokey-dave")))
	require.True(t, current.TwitchOn)

	// Broadcast Box failing: the first failure carries the last list, the
	// fourth no longer does, and the Twitch card is there throughout.
	setStatus(http.StatusInternalServerError)
	for failure := 1; failure <= 4; failure++ {
		current = s.poll(t)
		require.True(t, hasCard(current.Streams, erin), "failure %d", failure)
		require.Equal(t, failure <= pollFailuresBeforeEmpty, hasCard(current.Streams, s.p.streamHandle("videokey-dave")), "failure %d", failure)
	}

	// Broadcast Box not set up at all.
	cfg := s.p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = ""
	s.p.setConfiguration(cfg)
	current = s.poll(t)
	require.False(t, current.Configured)
	require.Equal(t, []string{erin}, []string{current.Streams[0].Handle})

	// Erin wore the badge from the first poll and never lost it.
	put, removed := 0, 0
	for _, call := range s.api.Calls {
		switch call.Method {
		case "UpdateUserCustomStatus":
			if call.Arguments[0] == twErin {
				put++
			}
		case "RemoveUserCustomStatus":
			if call.Arguments[0] == twErin {
				removed++
			}
		}
	}
	require.Positive(t, put)
	require.Zero(t, removed)
}

func TestMergeNeverWritesIntoThePublishedSnapshot(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})
	s.fake.goLive(daveTwitch, "Live", "Chess", 1, s.now())
	s.step(t)

	published := []apiStream{
		{StreamKey: "videokey-dave", Handle: s.p.streamHandle("videokey-dave"), Title: "Dave", UserID: twDave, LinkSource: linkSourceManual},
		{StreamKey: "videokey-bob", Handle: s.p.streamHandle("videokey-bob"), Title: "Bob", UserID: twBob, LinkSource: linkSourceManual},
	}
	s.p.setSnapshot(&snapshot{Streams: published})
	twitch := s.p.twitchStreams(s.p.getConfiguration(), s.now())

	stop := make(chan struct{})
	readers := sync.WaitGroup{}
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, stream := range s.p.getSnapshot().Streams {
					_ = stream.AlsoTwitch + stream.CoveredBy + stream.Title
				}
			}
		}()
	}

	for i := 0; i < 50; i++ {
		previous := s.p.getSnapshot().Streams
		require.NotEmpty(t, s.p.mergeStreams(broadcastBoxOnly(previous), twitch, true))
		require.NotEmpty(t, s.p.mergeStreams(previous, twitch, true))
	}
	close(stop)
	readers.Wait()

	for _, stream := range published {
		require.Empty(t, stream.AlsoTwitch, "the published list is never written")
	}
}

// The golden file was regenerated once for 3.2.0 (design/3.2.0.md §11.1),
// with achievements and biggest fans switched on: its only differences from
// 3.1.0's, kept as testdata/snapshot-310.golden.json, are the new fields,
// here the achievements the seeded months on the podium give. Twitch off
// still changes nothing.
func TestWithoutTwitchTheSnapshotIsByteIdentical(t *testing.T) {
	fake := newMediaFake(t)
	g := newGoldenScene(t, &configuration{
		TwitchClientID: testTwitchClientID, TwitchClientSecret: testTwitchClientSecret,
		EnableAchievements: true, EnableBiggestFans: true,
	})

	g.p.pollOnce(t.Context(), true)
	g.p.pollOnce(t.Context(), true)

	requireGolden(t, "testdata/twitch-off-snapshot.golden.json", goldenJSON(t, g.p))
	require.Empty(t, fake.requests())
}

func TestLiveOnBothShowsOneCardAndAlsoTwitch(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})
	s.fake.goLive(daveTwitch, "Live", "Chess", 1, s.now())
	s.step(t)

	twitch := s.p.twitchStreams(s.p.getConfiguration(), s.now())
	twitchHandle := twitch[0].Handle
	bbHandle := s.p.streamHandle("videokey-dave")
	card := apiStream{StreamKey: "videokey-dave", Handle: bbHandle, Title: "Dave", UserID: twDave, LinkSource: linkSourceManual}
	paused := card
	paused.Paused = true

	merge := func(bb ...apiStream) (apiStream, apiStream, bool) {
		merged := s.p.mergeStreams(bb, twitch, true)
		entry := cardOf(t, merged, twitchHandle)
		if !hasCard(merged, bbHandle) {
			return entry, apiStream{}, false
		}

		return entry, cardOf(t, merged, bbHandle), true
	}

	// Live on Twitch first: one card, the Twitch one.
	entry, _, _ := merge()
	require.Empty(t, entry.CoveredBy)

	// Then on Broadcast Box too: both cards for the grace, then one.
	entry, bb, _ := merge(card)
	require.Empty(t, entry.CoveredBy, "first poll of the simulcast")
	require.Empty(t, bb.AlsoTwitch)

	entry, bb, _ = merge(card)
	require.Equal(t, bbHandle, entry.CoveredBy, "after OfflineGraceChecks polls")
	require.Equal(t, "dave_tv", bb.AlsoTwitch)

	// A one-poll gap on Broadcast Box never uncovers it.
	entry, _, present := merge()
	require.False(t, present)
	require.Empty(t, entry.CoveredBy, "with no card to stand for it this poll, the Twitch card shows")
	entry, bb, _ = merge(card)
	require.Equal(t, bbHandle, entry.CoveredBy)
	require.Equal(t, "dave_tv", bb.AlsoTwitch)

	// Paused: still covered, until the grace has run out.
	entry, bb, _ = merge(paused)
	require.Equal(t, bbHandle, entry.CoveredBy)
	require.Equal(t, "dave_tv", bb.AlsoTwitch)
	entry, bb, _ = merge(paused)
	require.Empty(t, entry.CoveredBy)
	require.Empty(t, bb.AlsoTwitch)

	// The entry is always in the list, covered or not.
	require.Len(t, s.p.mergeStreams([]apiStream{card}, twitch, true), 2)
}

func TestTwitchBoardsAreRebuiltWhileBroadcastBoxIsDown(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twErin: erinTwitch})
	s.fake.goLive(erinTwitch, "Live", "Art", 3, s.now().Add(-time.Minute))
	s.step(t)

	s.seedTable(t, kvStatsTotals, statTable{"videokey-dave": {Seconds: 100, Title: "Dave"}})
	s.origin(t, func() (int, []bbStream) { return http.StatusInternalServerError, nil })

	// Twitch hours earned during the outage: the board must explain the crown.
	s.seedTable(t, kvTwitchAirTotals, statTable{erinTwitch.ID: {Seconds: 5000, Title: "Erin"}})
	var current *snapshot
	for failure := 1; failure <= 4; failure++ {
		current = s.poll(t)
	}
	require.NotEmpty(t, current.AllTime)
	require.Equal(t, twErin, current.AllTime[0].UserID)
	require.EqualValues(t, 5000, current.AllTime[0].TwitchSeconds)
	require.Equal(t, 1, cardOf(t, current.Streams, s.p.twitchHandle(erinTwitch.ID)).AllTimeRank)

	// With Twitch off, a failed poll is exactly what it always was: the
	// boards carried while the list is, and gone with it.
	off := newTwitchScene(t)
	cfg := off.p.getConfiguration().Clone()
	cfg.EnableTwitchLive = false
	off.p.setConfiguration(cfg)
	off.seedTable(t, kvStatsTotals, statTable{"videokey-dave": {Seconds: 100, Title: "Dave"}})
	var lock sync.Mutex
	status := http.StatusOK
	off.origin(t, func() (int, []bbStream) {
		lock.Lock()
		defer lock.Unlock()

		return status, []bbStream{liveRaw("videokey-dave", time.Now().Add(-time.Hour))}
	})

	good := off.poll(t)
	require.NotEmpty(t, good.AllTime)
	lock.Lock()
	status = http.StatusInternalServerError
	lock.Unlock()

	carried := off.poll(t)
	require.Equal(t, good.AllTime, carried.AllTime)
	require.Equal(t, good.Streams, carried.Streams)
	for failure := 2; failure <= 4; failure++ {
		carried = off.poll(t)
	}
	require.Empty(t, carried.Streams)
	require.Empty(t, carried.AllTime)
	require.False(t, carried.TwitchOn)
}

func TestSwitchingOffDropsTheCardsAtOnce(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twErin: erinTwitch})
	s.fake.goLive(erinTwitch, "Live", "Art", 3, s.now().Add(-time.Minute))
	s.step(t)
	s.origin(t, func() (int, []bbStream) { return http.StatusOK, nil })

	require.True(t, hasCard(s.poll(t).Streams, s.p.twitchHandle(erinTwitch.ID)))

	cfg := s.p.getConfiguration().Clone()
	cfg.EnableTwitchLive = false
	s.p.setConfiguration(cfg)
	s.p.restartTwitchLive()

	current := s.poll(t)
	require.Empty(t, current.Streams, "gone on the next poll")
	require.False(t, current.TwitchOn)

	removed := func() int {
		count := 0
		for _, call := range s.api.Calls {
			if call.Method == "RemoveUserCustomStatus" && call.Arguments[0] == twErin {
				count++
			}
		}

		return count
	}
	require.Zero(t, removed(), "the badge waits out the grace")
	s.poll(t)
	require.Equal(t, 1, removed(), "and then comes off")
}

func TestTheTwitchTitleIsCleanedAndRedacted(t *testing.T) {
	s := newTwitchScene(t)
	s.setKV(kvLinkTokenPrefix+twDave, []byte("d2b1e5c4-0000-4000-8000-000000000001"))
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})

	title := "  Join me ‮here‬: token d2b1e5c4-0000-4000-8000-000000000001 key VIDEOKEY-DAVE " + strings.Repeat("x", 300)
	s.fake.goLive(daveTwitch, title, "  Just Chatting  ", 1, s.now())
	s.step(t)

	streams := s.p.twitchStreams(s.p.getConfiguration(), s.now())
	require.Len(t, streams, 1)
	motd := streams[0].MOTD
	require.NotContains(t, motd, "d2b1e5c4")
	require.NotContains(t, strings.ToLower(motd), "videokey-dave")
	require.NotContains(t, motd, "‮")
	require.True(t, strings.HasPrefix(motd, "Join me here: token Dave key Dave "), motd)
	require.LessOrEqual(t, len([]rune(motd)), maxMOTDLength)
	require.Equal(t, "Just Chatting", streams[0].TwitchCategory)
	require.Equal(t, "Dave", streams[0].Title, "named as every person's card is")
}

func TestWHEPRefusesATwitchHandle(t *testing.T) {
	s := newTwitchScene(t)
	asked := s.origin(t, func() (int, []bbStream) { return http.StatusOK, nil })
	s.connected(t, map[string]twitchUser{twErin: erinTwitch})
	s.fake.goLive(erinTwitch, "Live", "Art", 3, s.now())
	s.step(t)
	s.p.setSnapshot(&snapshot{Streams: s.p.mergeStreams(nil, s.p.twitchStreams(s.p.getConfiguration(), s.now()), true)})
	s.p.playbackLimiter = newRateLimiter(60, time.Minute)

	before := asked.Load()
	response := s.serve(http.MethodPost, "/api/v1/whep", twDave, web(twDave), viewerOffer, map[string]string{"X-Stream-Handle": s.p.twitchHandle(erinTwitch.ID)})
	require.Equal(t, http.StatusNotFound, response.Code)
	require.Contains(t, response.Body.String(), "That stream is not live right now.")
	require.Equal(t, before, asked.Load(), "Broadcast Box is never asked about a Twitch stream")
	require.Empty(t, s.p.streamKeyForHandle(s.p.twitchHandle(erinTwitch.ID)))
}

func TestStillsRefuseATwitchHandle(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twErin: erinTwitch})
	s.fake.goLive(erinTwitch, "Live", "Art", 3, s.now())
	s.step(t)
	s.p.setSnapshot(&snapshot{Streams: s.p.mergeStreams(nil, s.p.twitchStreams(s.p.getConfiguration(), s.now()), true)})

	handle := s.p.twitchHandle(erinTwitch.ID)
	require.False(t, s.p.previewable(handle))

	claim := s.serve(http.MethodPost, "/api/v1/stills/claim?handle="+handle+"&mode=watching", twDave, web(twDave), "", nil)
	require.Equal(t, http.StatusNotFound, claim.Code)
	upload := s.serve(http.MethodPost, "/api/v1/stills?handle="+handle+"&lease=x", twDave, web(twDave), string(jpegBytes), nil)
	require.Equal(t, http.StatusNotFound, upload.Code)
}

func TestLinkedKeyIsLiveIgnoresTwitchAndEmptyKeys(t *testing.T) {
	s := newTwitchScene(t)
	twitchEntry := apiStream{Handle: s.p.twitchHandle("1001"), Source: sourceTwitch, UserID: twDave, TwitchID: "1001"}
	s.p.setSnapshot(&snapshot{Streams: []apiStream{twitchEntry}})

	require.False(t, s.p.linkedKeyIsLive(""), "a link that could not be read is never live")
	require.False(t, s.p.linkedKeyIsLive("videokey-dave"))

	bb := apiStream{StreamKey: "videokey-dave", Handle: s.p.streamHandle("videokey-dave")}
	s.p.setSnapshot(&snapshot{Streams: []apiStream{twitchEntry, bb}})
	require.True(t, s.p.linkedKeyIsLive("VideoKey-Dave"))
	require.False(t, s.p.linkedKeyIsLive(""))
}

func TestWhatsOnShowsOnATwitchCardAndSettles(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})
	s.fake.goLive(daveTwitch, "Live", "Chess", 1, s.now())
	s.step(t)
	s.p.nowPlayingLimiter = newRateLimiter(10, time.Minute)

	// Live only on Twitch, so saying what is on counts as aired at once.
	said := s.serve(http.MethodPost, "/api/v1/nowplaying", twDave, web(twDave), `{"category":"game","title":"Chess"}`, nil)
	require.Equal(t, http.StatusOK, said.Code, said.Body.String())
	doc, ok := s.p.readNowPlaying()
	require.True(t, ok)
	require.True(t, doc.Entries[twDave].Aired)

	cfg := s.p.getConfiguration()
	current := &snapshot{Streams: s.p.mergeStreams(nil, s.p.twitchStreams(cfg, s.now()), true)}
	s.p.addNowPlaying(current, cfg, time.Now(), true, true)
	require.Equal(t, "Chess", current.Streams[0].NowPlaying.Title, "on the Twitch card")

	// Off the air for the grace: taken down.
	for poll := 0; poll < cfg.OfflineGraceChecks; poll++ {
		s.p.addNowPlaying(&snapshot{Streams: []apiStream{}}, cfg, time.Now(), true, true)
	}
	doc, ok = s.p.readNowPlaying()
	require.True(t, ok)
	require.NotContains(t, doc.Entries, twDave)
}
