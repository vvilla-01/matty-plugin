package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
)

func nowMillis() int64 {
	return time.Now().UnixMilli()
}

func (p *Plugin) restartPoller() {
	cfg := p.getConfiguration()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	// Swap under the lock, so concurrent restarts cannot both read the same old
	// poller and drop one of the new ones on the floor.
	p.pollerLock.Lock()
	oldCancel, oldDone := p.pollerCancel, p.pollerDone
	p.pollerCancel = cancel
	p.pollerDone = done
	p.pollerLock.Unlock()

	// Wait for the previous poller outside the critical section.
	if oldCancel != nil {
		oldCancel()
		<-oldDone
	}

	// Who is covered in Live now is a grace counted in ticks, and a new
	// poller counts afresh.
	p.twitchCoverLock.Lock()
	p.twitchCovers = nil
	p.twitchCoverLock.Unlock()

	go p.runPoller(ctx, done, time.Duration(cfg.PollIntervalSeconds)*time.Second)
}

func (p *Plugin) stopPoller() {
	p.pollerLock.Lock()
	cancel := p.pollerCancel
	done := p.pollerDone
	p.pollerCancel = nil
	p.pollerDone = nil
	p.pollerLock.Unlock()

	if cancel == nil {
		return
	}

	cancel()
	<-done
}

func (p *Plugin) runPoller(ctx context.Context, done chan struct{}, interval time.Duration) {
	defer close(done)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// The first poll is a tick when the plugin has just started, or when a
	// whole interval has gone by since the last one. Otherwise this poller
	// is replacing one that a settings save stopped (Mattermost calls
	// OnConfigurationChange for a change to any of the server's settings),
	// and its first poll belongs to the save, not to the clock. Its own ticks
	// start an interval from now, so none comes sooner than an interval
	// after the last.
	//
	// Every pass is run safely (recovery.go), and the ticker paces the next
	// one after a pass that panicked. The locks a poll takes are let go by a
	// defer or held only around plain assignments to maps and fields.
	if last := p.pollerTickedAt.Load(); last == 0 || time.Since(time.UnixMilli(last)) >= interval {
		p.safely("the poller", func() { p.tick(ctx) })
	} else {
		p.safely("the poller", func() { p.pollOnce(ctx, false) })
	}

	// A roster change waiting for its turn, or nil: see publishRosterSoon.
	var rosterDue <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.safely("the poller", func() { p.tick(ctx) })
		case <-p.refreshCh:
			// Somebody's click asked for this one. It shows them the change
			// at once, but no time has passed: see pollOnce.
			p.safely("the poller", func() { p.pollOnce(ctx, false) })
		case <-p.rosterCh:
			if rosterDue == nil {
				now := time.Now()
				rosterDue = time.After(republishEvery - now.Sub(now.Truncate(republishEvery)))
			}
		case <-rosterDue:
			rosterDue = nil
			p.safely("the poller", p.publishRosterNow)
		}
	}
}

// tick is a poll on the poller's own clock, one interval after the last. It
// notes when, for the first poll of the next poller (see runPoller).
func (p *Plugin) tick(ctx context.Context) {
	p.pollerTickedAt.Store(time.Now().UnixMilli())
	p.pollOnce(ctx, true)
}

// publishRosterSoon puts a change to who is in voice in front of every panel,
// without a poll. It never blocks.
//
// The roster is one field of the snapshot, and it used to cost a whole poll:
// Broadcast Box asked, every store read a poll makes, and while anybody was
// live the airtime tables written again — for every join, leave and, since a
// muted person is shown dimmed, every mute. Somebody using mute as
// push-to-talk was a poll a press. Now it is the snapshot as last published
// with the new roster on it, as a clip write is (republishClips).
//
// Coalesced the same way, at most once every republishEvery: the first change
// arms the next boundary and every change until then rides on it, since each
// is a push to every connected client. And done by the poller, so it can never
// interleave with a poll, and stops with it: a poll that carried the roster
// already leaves it nothing to push, and once the poller has stopped,
// nothing is published at all.
func (p *Plugin) publishRosterSoon() {
	if p.rosterCh == nil {
		return
	}

	select {
	case p.rosterCh <- struct{}{}:
	default:
		// A change is already on its way; it will read the roster as it is
		// by then.
	}
}

// publishRosterNow is the armed roster change going out. Broadcast Box is not
// asked anything: the snapshot as last published is copied and given the
// roster, and this node's clips, as they are now. The clips too, because a
// clip republish can be copying the same snapshot at the same moment, and
// whichever publishes second must not take the other's change back.
func (p *Plugin) publishRosterNow() {
	p.snapshotLock.RLock()
	published := p.snapshot
	p.snapshotLock.RUnlock()

	// Nothing published yet: the first poll will carry the roster itself, and
	// a snapshot made from nothing would tell every panel nothing is live.
	if published == nil {
		return
	}

	current := *published
	cfg := p.getConfiguration()
	p.addFromMemory(&current, cfg, time.Now())
	p.publish(&current)
}

// refreshSoon asks the poller to run again. It never blocks and never starts a
// goroutine, so a user hammering an endpoint that triggers a refresh cannot
// multiply load on the origin, the KV store or every client's WebSocket.
func (p *Plugin) refreshSoon() {
	if p.refreshCh == nil {
		return
	}

	select {
	case p.refreshCh <- struct{}{}:
	default:
		// A refresh is already queued; nothing to add.
	}
}

// pollOnce reads Broadcast Box and publishes what it found.
//
// ticked is whether the poll is on the poller's clock. Every grace counted in
// polls means that many intervals: OfflineGraceChecks for a badge, What's on?,
// a Clips recording and the swap between a Twitch card and a Broadcast Box
// one, and pollFailuresBeforeEmpty for an unreachable Broadcast Box. A poll
// somebody's click asks for (refreshSoon), or a settings save, can come a
// moment after a tick, and counted it would have one OBS reconnect take a
// badge off and delete what was on. So only a tick counts a miss or a
// failure. Any poll publishes what it read, and any poll that sees a stream
// again starts its grace afresh: that is evidence, whatever the clock says.
func (p *Plugin) pollOnce(ctx context.Context, ticked bool) {
	cfg := p.getConfiguration()

	if err := cfg.validate(); err != nil {
		now := time.Now().UTC()
		unconfigured := &snapshot{
			Streams:      nonNilStreams(p.mergeStreams(nil, p.twitchStreams(cfg, now), ticked)),
			Configured:   false,
			Error:        err.Error(),
			FetchedAt:    nowMillis(),
			LivePreviews: cfg.LivePreviews,
			TwitchOn:     cfg.twitchLiveOn(),
			TwitchEmbed:  cfg.twitchEmbedOn(),
		}
		p.addCommunity(unconfigured)

		// A server that streams only through Twitch still has hours and
		// boards. With Twitch off this path is what it always was.
		if cfg.twitchLiveOn() {
			p.addNowPlaying(unconfigured, cfg, now, false, ticked)
			if cfg.EnableLeaderboard {
				p.addBoards(unconfigured, cfg, now)
			}
			if cfg.TopWatchers {
				p.addWatchBoards(unconfigured, cfg, now)
			}
		}

		// Biggest fans and achievements, from the figures this node holds:
		// nothing is counted without Broadcast Box, and nothing is lost.
		p.refreshAwardDocs(false)
		p.loadSoundsForPoll(cfg)
		people := p.newPollPeople()
		p.addFans(unconfigured, cfg, people)
		p.refreshAchievementFigures(unconfigured, cfg, now, people)

		p.dropWatchersWhenOff(unconfigured, cfg)

		// Nothing can be recorded from a Broadcast Box nobody named, and
		// what was recorded came from the one before. Nothing at all to do
		// on the polls after the first (A§2.1).
		p.recorders().stopAll(false, whyUnconfigured, time.Now())

		// Last before every publish: see addFromMemory.
		p.addFromMemory(unconfigured, cfg, time.Now())
		p.publish(unconfigured)

		// Badges are still reconciled. A birthday has nothing to do with
		// whether the television is reachable, and a cake that went on at
		// midnight would otherwise stay on until somebody fixed an unrelated
		// setting — with its owner's real status buried underneath it.
		p.reconcileBadges(p.getSnapshot().Streams, cfg, time.Now(), ticked)

		return
	}

	reqCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	raw, err := p.fetchStatus(reqCtx, cfg)
	if err != nil {
		// A cancelled context means the plugin is shutting down or being
		// reconfigured, which is not worth reporting to every client.
		if ctx.Err() != nil {
			return
		}
		p.API.LogWarn("Could not read the Broadcast Box status", "error", err.Error())

		// One hiccup must not empty every viewer's panel. A request that
		// times out because the origin is briefly busy used to publish an
		// empty list, which tore down whatever was playing for everyone at
		// once. Keep showing what was live, with the error alongside, and
		// only give up after several failed ticks in a row: a failed poll
		// somebody's click asked for is no longer an outage.
		now := time.Now().UTC()
		current := &snapshot{
			Streams:      []apiStream{},
			Configured:   true,
			Error:        clientSafeError(err),
			FetchedAt:    nowMillis(),
			LivePreviews: cfg.LivePreviews,
			TwitchOn:     cfg.twitchLiveOn(),
			TwitchEmbed:  cfg.twitchEmbedOn(),
		}
		p.addCommunity(current)

		carried := p.countPollFailure(ticked) <= pollFailuresBeforeEmpty
		if carried {
			previous := p.getSnapshot()

			// The last Broadcast Box cards, in a list of their own: the
			// Twitch ones are merged afresh below, from Twitch's own reads,
			// which Broadcast Box being down says nothing about.
			current.Streams = broadcastBoxOnly(previous.Streams)
			current.AllTime, current.AllTimeTotal = previous.AllTime, previous.AllTimeTotal
			current.Month, current.MonthTotal = previous.Month, previous.MonthTotal
			current.MonthLabel = previous.MonthLabel

			// With the boards they go with. An origin that cannot be reached
			// says nothing about who won a month, and without this every
			// profile's tally would vanish with each hiccup and come back
			// with the next good poll.
			current.MonthMedals = previous.MonthMedals

			// The watchers' boards for the same reason, but only while
			// they are on: switching them off restarts the poller, and the
			// poll that follows may well be this one. Biggest fans with
			// them, while they are on.
			if cfg.TopWatchers {
				current.carryWatchBoards(previous)
			}
			if cfg.fansShown() {
				current.carryFans(previous)
			}
		}

		current.Streams = p.mergeStreams(current.Streams, p.twitchStreams(cfg, now), ticked)

		// What is on, put on the last known cards afresh, so a choice made or
		// taken down during the outage still shows. Nothing is taken down
		// for going quiet here, though: an origin that cannot be reached
		// says nothing about who stopped streaming.
		p.addNowPlaying(current, cfg, now, false, ticked)

		// Twitch time keeps being credited while Broadcast Box is
		// unreachable, so the boards are rebuilt rather than carried:
		// otherwise a Twitch card would keep a crown that no board explains
		// once the carry-over ends. With Twitch off, nothing here changes.
		if cfg.twitchLiveOn() {
			people := p.newPollPeople()
			if cfg.EnableLeaderboard {
				p.addBoardsWith(current, cfg, now, people.resolve)
			}
			if cfg.TopWatchers && !carried {
				p.addWatchBoardsFor(current, cfg, now, people)
			}
			p.refreshAchievementFigures(current, cfg, now, people)
		}

		// The last known cards, but not the last known settings: see
		// dropWatchersWhenOff.
		p.dropWatchersWhenOff(current, cfg)

		// Last, after the last known streams are in: see addFromMemory.
		p.addFromMemory(current, cfg, now)
		p.publish(current)

		// The last known list rather than an empty one: the origin being
		// unreachable is not evidence that anybody stopped streaming, and the
		// offline grace exists for exactly this. What this call is really for
		// is the cake, which must still go on and come off while the
		// television is down; and Twitch's badges, which do not depend on it.
		p.reconcileBadges(current.Streams, cfg, now, ticked)

		return
	}

	p.resetPollFailures()

	streams := p.decorate(raw, cfg)

	now := time.Now().UTC()
	p.recordStreamTime(streams, cfg, now)
	p.recordWatchTime(raw, streams, cfg, now)

	// Which panel sessions Broadcast Box still lists, for the watch gate
	// (A§2.1). From memory alone, so a poll never waits on it.
	p.noteClipSessions(raw, now)

	// The Twitch streams, from memory, after everything above that reads
	// Broadcast Box's own: airtime and watching are credited from each
	// source's own evidence, and nothing above is ever handed a Twitch entry.
	streams = p.mergeStreams(streams, p.twitchStreams(cfg, now), ticked)

	current := &snapshot{
		Streams:      streams,
		Configured:   true,
		FetchedAt:    nowMillis(),
		LivePreviews: cfg.LivePreviews,
		TwitchOn:     cfg.twitchLiveOn(),
		TwitchEmbed:  cfg.twitchEmbedOn(),
	}
	p.addCommunity(current)

	// Who chose which awards to show, when this node's copy is more than a
	// minute old (awards.go); and the soundboard's board, once, when it has
	// just been switched on.
	p.refreshAwardDocs(false)
	p.loadSoundsForPoll(cfg)

	// After the schedule, which a plan's title is read from, and before the
	// boards, which write their medals onto the cards this replaces.
	p.addNowPlaying(current, cfg, now, true, ticked)

	// Which streams are recorded for clipping (spec §2.4.5), on this poll's
	// listing, as only a successful poll can say a stream has gone. After
	// what is on, so a recorder hands over the end of a stream with the
	// game, movie or show it was tagged with (A§4.14). It only changes the
	// manager; addClips tells the panels. Never handed a Twitch stream:
	// Twitch's video is only ever shown through Twitch's own player.
	p.recorders().reconcile(broadcastBoxOnly(current.Streams), cfg, now, ticked)

	// One poll's answers about people for the boards, biggest fans and
	// achievements together, so nobody is looked up twice.
	people := p.newPollPeople()
	if cfg.EnableLeaderboard {
		p.addBoardsWith(current, cfg, now, people.resolve)
	}
	if cfg.TopWatchers {
		p.addWatchBoardsFor(current, cfg, now, people)
	}
	p.addFans(current, cfg, people)
	p.refreshAchievementFigures(current, cfg, now, people)

	// The same instant the airtime was credited against, rather than a second
	// reading of the clock: "is it their birthday" and "how long have they been
	// on" should never disagree about what time it is.
	p.checkBirthdays(cfg, now)
	p.reconcileBadges(streams, cfg, now, ticked)

	p.addFromMemory(current, cfg, now)
	p.publish(current)
}

// addFromMemory puts on a snapshot everything that comes from this node's own
// memory rather than a poll, last before it is published, on every path:
// who is in voice and the clips, as always, and from 3.2.0 the award
// choices, the achievements (whose thumbs follow the clips) and the
// soundboard's fingerprint. A roster change or a clip write publishes a copy
// of the last snapshot through here too, so nothing here may read the store:
// a change of one must not take another's back, and a run of them must stay
// as cheap as it was.
func (p *Plugin) addFromMemory(current *snapshot, cfg *configuration, now time.Time) {
	p.addVoice(current, cfg)
	p.addClips(current, cfg, now)
	p.addAwardChoices(current)
	p.addAchievements(current, cfg)
	p.addSoundboard(current, cfg)
}

// addBoards puts the leaderboards on a snapshot, the places they give its live
// cards, and the medals of months that have ended. now is the instant the
// airtime was just credited against, so the first poll after midnight on the
// 1st has already paid the old month its final seconds before anything reads
// that month's podium.
//
// The boards are by person: a person's Broadcast Box rows and, while Twitch
// live is on, their Twitch hours, are one row (boardRows).
func (p *Plugin) addBoards(current *snapshot, cfg *configuration, now time.Time) {
	p.addBoardsWith(current, cfg, now, newIdentityResolver(p))
}

// addBoardsWith is addBoards with a poll's own resolver, shared with the
// watch boards, biggest fans and achievements.
func (p *Plugin) addBoardsWith(current *snapshot, cfg *configuration, now time.Time, resolve *identityResolver) {
	// Unreadable tables are reported by recordStreamTime, which has
	// already halted recording; the boards simply stay empty rather than
	// showing a partial ranking that looks like people lost their place.
	totals, totalsErr := p.loadStats(kvStatsTotals)
	month, monthErr := p.loadStats(monthBucket(now))
	if totalsErr != nil || monthErr != nil {
		return
	}

	twitchOn := cfg.twitchLiveOn()
	var owners map[string]string
	var twitchTotals, twitchMonth statTable
	if twitchOn {
		var err error
		owners, twitchTotals, twitchMonth, err = p.twitchBoardTables(cfg, now)
		if err != nil {
			// Who has connected Twitch, or the Twitch hours, could not be
			// read just now. Never guessed: the boards stay as the last
			// poll had them, and the next poll tries again.
			p.API.LogWarn("Could not read the Twitch hours for the boards; keeping the last ones", "error", err.Error())
			p.carryBoards(current)

			return
		}
	}

	// One resolver for both boards: the same streamer is usually on
	// each of them, and this halves the identity lookups per poll.
	allTime := p.boardRows(totals, twitchTotals, cfg, resolve, owners)
	thisMonth := p.boardRows(month, twitchMonth, cfg, resolve, owners)

	// The figures the Streamed achievements are worked out from: the
	// all-time rows by person, before the board is cut to fifty.
	p.noteStreamedFigures(allTime)

	current.AllTime, current.AllTimeTotal = p.leaderboard("all time", allTime, maxLeaderboardRows)
	current.Month, current.MonthTotal = p.leaderboard("this month", thisMonth, maxLeaderboardRows)
	current.MonthLabel = monthLabel(now)

	// And for the medals of months gone by, so the tally in somebody's
	// profile and the boards beside it always agree about who they are.
	current.MonthMedals = p.monthMedalTally(now, resolve, owners, twitchOn)

	// Medals on the cards themselves, so a streamer sees their place
	// without opening anything. Written on a copy: on a failed poll the
	// list may share its array with the snapshot readers hold.
	allTimeRanks := rankByRow(allTime)
	monthRanks := rankByRow(thisMonth)

	streams := make([]apiStream, len(current.Streams))
	copy(streams, current.Streams)
	for i := range streams {
		streams[i].AllTimeRank, streams[i].MonthRank = 0, 0
		if streams[i].AlwaysOn {
			continue
		}

		id := cardRowID(streams[i], resolve)
		streams[i].AllTimeRank = allTimeRanks[id]
		streams[i].MonthRank = monthRanks[id]
	}
	current.Streams = streams
}

// twitchBoardTables is what the boards need from Twitch: who has connected
// which Twitch id, and the Twitch ledger's all-time and monthly tables. A
// Twitch table whose bytes will not decode halts the Twitch ledger and leaves
// Twitch hours off the boards, and so does a link document that will not
// decode: with nobody known to own a Twitch row, the boards are Broadcast
// Box's alone until an admin starts again. A store that does not answer is
// an error.
func (p *Plugin) twitchBoardTables(cfg *configuration, now time.Time) (map[string]string, statTable, statTable, error) {
	owners, err := p.twitchLive().owners(cfg)
	if errors.Is(err, errTwitchLinksUnreadable) {
		return nil, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}

	totals, err := p.loadStats(kvTwitchAirTotals)
	if errors.Is(err, errStatsQuarantined) {
		p.twitchHaltOrSkip(err)
		return owners, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}

	month, err := p.loadStats(twitchMonthBucket(now))
	if errors.Is(err, errStatsQuarantined) {
		p.twitchHaltOrSkip(err)
		return owners, nil, nil, nil
	}
	if err != nil {
		return nil, nil, nil, err
	}

	return owners, totals, month, nil
}

// carryBoards puts the last snapshot's boards on this one, with each card's
// places copied by handle: what a failed poll does, for a poll whose Twitch
// half could not be read.
func (p *Plugin) carryBoards(current *snapshot) {
	previous := p.getSnapshot()

	current.AllTime, current.AllTimeTotal = previous.AllTime, previous.AllTimeTotal
	current.Month, current.MonthTotal = previous.Month, previous.MonthTotal
	current.MonthLabel = previous.MonthLabel
	current.MonthMedals = previous.MonthMedals

	ranks := make(map[string][2]int, len(previous.Streams))
	for _, stream := range previous.Streams {
		ranks[stream.Handle] = [2]int{stream.AllTimeRank, stream.MonthRank}
	}

	streams := make([]apiStream, len(current.Streams))
	copy(streams, current.Streams)
	for i := range streams {
		place := ranks[streams[i].Handle]
		streams[i].AllTimeRank, streams[i].MonthRank = place[0], place[1]
	}
	current.Streams = streams
}

// twitchCover is whether one person's Twitch entry is covered by their
// Broadcast Box card in Live now, and how many ticks in a row the evidence
// has said otherwise.
type twitchCover struct {
	covered bool
	streak  int
}

// mergeStreams adds this poll's Twitch entries to the Broadcast Box ones.
// With no Twitch entries it returns bb untouched (same slice, same order), so
// a server without Twitch publishes exactly what it did before.
//
// It never writes into bb or twitch: on a failed poll they share arrays with
// the published snapshot, which readers hold without a lock.
//
// Somebody live on both is one card in Live now, the Broadcast Box one, with
// "Also on Twitch". Their Twitch entry stays in the list, marked coveredBy,
// because the watch list works by handle, and a Twitch tile somebody is
// watching must keep playing when its streamer also starts on Broadcast Box.
// Which card stands for them changes only after OfflineGraceChecks ticks of
// evidence in a row, so an OBS reconnect or a short pause never swaps it.
// ticked is pollOnce's: a poll off the poller's clock can stop a swap, never
// bring one closer.
func (p *Plugin) mergeStreams(bb, twitch []apiStream, ticked bool) []apiStream {
	if len(twitch) == 0 {
		p.twitchCoverLock.Lock()
		p.twitchCovers = nil
		p.twitchCoverLock.Unlock()

		return bb
	}

	grace := max(p.getConfiguration().OfflineGraceChecks, 1)

	merged := make([]apiStream, 0, len(bb)+len(twitch))
	merged = append(merged, bb...)

	handles := make(map[string]bool, len(bb))
	for _, stream := range bb {
		handles[stream.Handle] = true
	}

	// Each person's Broadcast Box card, preferring one on the air: the one a
	// covered Twitch entry points at.
	cardOf := map[string]int{}
	for i, stream := range merged {
		if stream.UserID == "" || stream.AlwaysOn || stream.Source != "" {
			continue
		}
		if at, seen := cardOf[stream.UserID]; !seen || (merged[at].Paused && !stream.Paused) {
			cardOf[stream.UserID] = i
		}
	}

	p.twitchCoverLock.Lock()
	defer p.twitchCoverLock.Unlock()

	covers := make(map[string]twitchCover, len(twitch))
	for _, entry := range twitch {
		// Only a hostile origin could make a Broadcast Box handle collide
		// with a domain-separated Twitch one.
		if handles[entry.Handle] {
			p.twitchApp().warnOnce("twitch-handle", "Ignoring a Twitch stream whose handle a Broadcast Box stream already has")
			continue
		}
		handles[entry.Handle] = true

		if entry.AlwaysOn {
			merged = append(merged, entry)
			continue
		}

		at, hasCard := cardOf[entry.UserID]
		want := hasCard && !merged[at].Paused

		cover, known := p.twitchCovers[entry.UserID]
		switch {
		case !known:
			cover = twitchCover{covered: want}
		case cover.covered == want:
			cover.streak = 0
		case ticked:
			cover.streak++
			if cover.streak >= grace {
				cover = twitchCover{covered: want}
			}
		}
		covers[entry.UserID] = cover

		// Covered, and the card is in this list to stand for them. In a poll
		// where it is not, the Twitch card shows rather than nobody.
		if cover.covered && hasCard {
			entry.CoveredBy = merged[at].Handle
			merged[at].AlsoTwitch = entry.TwitchLogin
		}

		merged = append(merged, entry)
	}
	p.twitchCovers = covers

	sort.SliceStable(merged, func(i, j int) bool {
		return streamBefore(merged[i], merged[j])
	})

	return merged
}

// streamGroup is where a stream sorts: people on Broadcast Box, then people
// on Twitch, then streams nobody is named for, then 24/7 channels, as
// decorate groups them.
func streamGroup(stream apiStream) int {
	switch {
	case stream.AlwaysOn:
		return 3
	case stream.Source != "":
		return 1
	case stream.UserID != "":
		return 0
	default:
		return 2
	}
}

// streamBefore is the merged list's order: a total one, by group, then name,
// then handle.
func streamBefore(a, b apiStream) bool {
	if ga, gb := streamGroup(a), streamGroup(b); ga != gb {
		return ga < gb
	}
	if ta, tb := strings.ToLower(a.Title), strings.ToLower(b.Title); ta != tb {
		return ta < tb
	}

	return a.Handle < b.Handle
}

// broadcastBoxOnly is a new slice of the Broadcast Box entries of a list, with
// AlsoTwitch cleared: for a poll that carries the last list forward, and for
// the clip recorders, which are never handed a Twitch stream.
func broadcastBoxOnly(streams []apiStream) []apiStream {
	only := make([]apiStream, 0, len(streams))
	for _, stream := range streams {
		if stream.Source != "" {
			continue
		}
		stream.AlsoTwitch = ""
		only = append(only, stream)
	}

	return only
}

func nonNilStreams(streams []apiStream) []apiStream {
	if streams == nil {
		return []apiStream{}
	}

	return streams
}

// decorate turns the raw Broadcast Box status into the list the webapp renders:
// only live streams, attributed to Mattermost accounts where possible, and
// ordered so that people appear above always-on channels.
func (p *Plugin) decorate(raw []bbStream, cfg *configuration) []apiStream {
	overrides := cfg.nameOverrides()
	notes := cfg.noteOverrides()
	alwaysOn := cfg.alwaysOnSet()
	channelHandles := map[string]string{}
	seen := make(map[string]bool, len(raw))
	streams := make([]apiStream, 0, len(raw))
	stills := p.stills()
	watchers := p.watchers()
	now := time.Now()

	// Before any stream is listed: see observe. For the Top Snoopers board as
	// much as for the faces, since it counts hours from the same record.
	if cfg.ShowWatchers || cfg.TopWatchers {
		watchers.observe(raw, now)
	}

	for _, source := range raw {
		if len(streams) >= maxStreams {
			p.API.LogWarn("Broadcast Box reported more streams than the plugin will show", "limit", maxStreams, "reported", len(raw))
			break
		}

		if !source.live() {
			continue
		}
		streamKey := cleanStreamKey(source.StreamKey)

		// The sessions that make the audience, peeks left out. The faces
		// are drawn from the same ones, so a card can never name somebody
		// its count has left out.
		counted := stills.counted(source.Sessions, now)

		// And the clip recorders' own sessions, which are nobody watching
		// and nobody to count as a stranger beside the faces (§2.4.7).
		// Never in the watcher store, so the Top Snoopers board never
		// credits them either.
		counted = p.recorders().withoutOwn(counted, now)

		stream := apiStream{
			StreamKey:   streamKey,
			Handle:      p.streamHandle(streamKey),
			AlwaysOn:    alwaysOn[normalizeKey(streamKey)],
			MOTD:        source.MOTD,
			StreamStart: source.StreamStart,
			Paused:      source.isPaused(),
			Viewers:     len(counted),
			Bitrate:     source.bitrate(),
			VideoTracks: len(source.VideoTracks),
			AudioTracks: len(source.AudioTracks),
		}

		if userID, linkSource := p.resolveUser(streamKey, cfg); userID != "" {
			if user, err := p.API.GetUser(userID); err == nil && user != nil {
				stream.UserID = user.Id
				stream.Username = user.Username
				stream.DisplayName = displayNameOf(user)
				stream.LinkSource = linkSource
			}
		}

		switch {
		case stream.DisplayName != "":
			stream.Title = stream.DisplayName
		case overrides[normalizeKey(stream.StreamKey)] != "":
			stream.Title = overrides[normalizeKey(stream.StreamKey)]
		default:
			// Never the stream key: on this build it is close enough to a
			// credential, and the whole point of handles is that it does not
			// reach the browser. Admins give it a real name with
			// StreamNameOverrides.
			stream.Title = unnamedTitle(stream.Handle)
		}

		if stream.AlwaysOn && stream.Handle != "" {
			channelHandles[stream.Handle] = streamKey
		}

		// A 24/7 channel has nobody's profile picture to wear, so it wears
		// its own when it has one. Its card picture is not about a face, so
		// it goes on whoever the channel is attributed to.
		if stream.AlwaysOn {
			if stream.UserID == "" {
				stream.PictureVersion = p.channelImageVersion(channelPictureKind, streamKey, now)
			}
			stream.ThumbnailVersion = p.channelImageVersion(channelThumbKind, streamKey, now)
		}

		if isDefaultMOTD(stream.MOTD, stream.StreamKey) {
			stream.MOTD = ""
		} else {
			// The owner's publish token too, and first, as withoutOwnKey does:
			// a note written in Broadcast Box itself never passed through the
			// panel's save, which takes it out there.
			if stream.UserID != "" {
				if token := p.getLinkedToken(stream.UserID); token != "" {
					stream.MOTD = redactStreamKey(stream.MOTD, token, stream.Title)
				}
			}
			stream.MOTD = redactStreamKey(stream.MOTD, stream.StreamKey, stream.Title)
		}

		// Only when the stream has none of its own, so a streamer who writes a
		// note from the panel is never overruled by a setting.
		if stream.MOTD == "" {
			stream.MOTD = notes[normalizeKey(stream.StreamKey)]
		}

		if utf8.RuneCountInString(stream.MOTD) > maxMOTDLength {
			stream.MOTD = string([]rune(stream.MOTD)[:maxMOTDLength])
		}

		// A stream with no handle cannot be addressed by the panel at all, and
		// two streams sharing one would let the relay serve the wrong video.
		if stream.Handle == "" {
			continue
		}
		if seen[stream.Handle] {
			p.API.LogWarn("Ignoring a duplicate stream handle", "stream", keyPrefix(stream.StreamKey))
			continue
		}
		seen[stream.Handle] = true

		// Only once the stream is certain to be listed, because this is also
		// what tells the still store the stream is still on the air.
		if cfg.LivePreviews && !stream.AlwaysOn {
			if still := stills.seen(stream.Handle, now); still != nil {
				stream.StillVersion = still.version
				stream.StillAt = still.at.UTC().Format(time.RFC3339)
			}
		}

		// People's streams and 24/7 channels alike: whoever is watching a
		// channel is as much its audience as anybody's.
		if cfg.ShowWatchers {
			stream.Watchers, stream.Others = p.watchersOf(stream.Handle, counted)
		}

		streams = append(streams, stream)
	}

	// After every stream in this poll has been marked as seen, so the off-air
	// grace is measured from this poll and not from the one before it.
	stills.sweep(now, cfg.LivePreviews)

	// Likewise after observe, so a session in this poll's listing is never
	// forgotten by it. One recorded while this poll was running, too late to
	// be in its listing, was heard of moments ago, and is kept.
	watchers.sweep(now, cfg.ShowWatchers || cfg.TopWatchers)

	// Names are only ever for faces. With faces off and the board on, the
	// sessions stay for the board and the list of who was looked up goes, just
	// as sweep empties it when both are off.
	if !cfg.ShowWatchers {
		watchers.names.clear()
	}

	sort.SliceStable(streams, func(i, j int) bool {
		// People first, then always-on channels; alphabetical within each group.
		if streams[i].AlwaysOn != streams[j].AlwaysOn {
			return streams[j].AlwaysOn
		}
		iLinked, jLinked := streams[i].UserID != "", streams[j].UserID != ""
		if iLinked != jLinked {
			return iLinked
		}
		return strings.ToLower(streams[i].Title) < strings.ToLower(streams[j].Title)
	})

	p.rememberChannelHandles(channelHandles, alwaysOn)

	return streams
}

// resolveUser maps a stream key onto a Mattermost account, preferring an
// explicit link over the username match so a manual link always wins.
func (p *Plugin) resolveUser(streamKey string, cfg *configuration) (string, string) {
	if userID := p.getLinkedUser(streamKey); userID != "" {
		return userID, linkSourceManual
	}

	if cfg.AutoLinkByUsername {
		if user, err := p.API.GetUserByUsername(normalizeKey(streamKey)); err == nil && user != nil {
			// A username match must not override someone's explicit link to a
			// different key.
			if linked := p.getLinkedKey(user.Id); linked == "" || normalizeKey(linked) == normalizeKey(streamKey) {
				return user.Id, linkSourceUsername
			}
		}
	}

	return "", ""
}

// isDefaultMOTD reports whether a note is Broadcast Box's auto-generated
// placeholder, "Welcome to <streamKey>!". It carries no information and is a
// place the key would otherwise be put on screen, so it is treated as unset.
func isDefaultMOTD(motd, streamKey string) bool {
	return strings.EqualFold(strings.TrimSpace(motd), "Welcome to "+streamKey+"!")
}

// asciiFold lowercases ASCII letters and nothing else. Unicode-aware lowering
// can change a string's byte length — U+0130 lowercases to two runes — and the
// offsets this produces are used to slice the ORIGINAL string, so a fold that
// shifts bytes turns a crafted MOTD into an out-of-range slice and a panic.
func asciiFold(value string) string {
	folded := []byte(value)
	for i, c := range folded {
		if c >= 'A' && c <= 'Z' {
			folded[i] = c + ('a' - 'A')
		}
	}

	return string(folded)
}

// redactStreamKey keeps the stream key out of the free text the panel shows.
// Broadcast Box's default MOTD is "Welcome to <streamKey>!", and the key is
// never meant to reach the browser.
func redactStreamKey(motd, streamKey, title string) string {
	if motd == "" || streamKey == "" || title == "" || strings.EqualFold(title, streamKey) {
		return motd
	}

	// Folded once, outside the loop: byte-for-byte aligned with the originals.
	foldedMOTD := asciiFold(motd)
	foldedKey := asciiFold(streamKey)

	var out strings.Builder

	for start := 0; ; {
		offset := strings.Index(foldedMOTD[start:], foldedKey)
		if offset < 0 {
			out.WriteString(motd[start:])
			return out.String()
		}

		at := start + offset
		out.WriteString(motd[start:at])
		out.WriteString(title)
		start = at + len(streamKey)
	}
}

// unnamedTitle labels a stream nobody has named, without disclosing its key.
func unnamedTitle(handle string) string {
	if len(handle) >= 6 {
		return "Stream " + handle[:6]
	}

	return "Unnamed stream"
}

func displayNameOf(user *model.User) string {
	if nickname := strings.TrimSpace(user.Nickname); nickname != "" {
		return nickname
	}

	full := strings.TrimSpace(strings.TrimSpace(user.FirstName) + " " + strings.TrimSpace(user.LastName))
	if full != "" {
		return full
	}

	return user.Username
}

// reconcileBadges brings the set of badged users in line with who should be
// wearing one: a cake on their birthday, a red dot while they are broadcasting.
//
// ONE function decides this, for one reason. There is a single custom status per
// person, and the plugin remembers what it put there so it can tell its own
// badge from one the person set themselves. Two independent writers — one for
// live, one for birthdays — would each see the other's emoji as somebody taking
// their slot back, and a person who was both live and celebrating would have
// their status rewritten twice every poll, for ever. So the two are a priority
// order inside one decision, not two features that happen to share a slot.
//
// The cake wins. Somebody streaming on their birthday is having a birthday.
//
// Going live shows immediately, but a badge is only taken away after several
// consecutive ticks with no sign of the stream: OBS reconnects and playlist
// restarts would otherwise make people's status flicker. ticked is pollOnce's:
// a poll off the poller's clock puts badges on and keeps them, but never
// counts toward taking one off.
func (p *Plugin) reconcileBadges(streams []apiStream, cfg *configuration, now time.Time, ticked bool) {
	celebrating := p.celebratingToday(cfg, now)

	// Nothing to badge anybody for. Both switches off, or the badge switch off
	// on a day that is nobody's birthday.
	if !cfg.EnableLiveBadge && len(celebrating) == 0 {
		p.clearAllBadges()
		p.setWornBadges(map[string]string{})

		p.offlineLock.Lock()
		p.offlineStreak = map[string]int{}
		p.offlineLock.Unlock()

		return
	}

	// Who is live is one rule, the people index's (people.go liveHandles):
	// the badge beside a name and the index every client reads can then never
	// say different things about the same person. A person stops being live
	// by that rule a little before the old one here would have had it (their
	// own card paused while the Twitch entry it covers is still listed), which
	// costs nothing: a badge only comes off after the grace below.
	live := make(map[string]bool, len(streams))
	if cfg.EnableLiveBadge {
		for userID := range liveHandles(streams) {
			live[userID] = true
		}
	}

	// Listed outside the lock: this pages the whole KV namespace.
	badged := p.kvKeysWithPrefix(kvPrevStatusPrefix)

	// Everybody the plugin might have an opinion about: who is celebrating, who
	// is live, and who it is already holding a slot for.
	wanted := map[string]bool{}
	for userID := range celebrating {
		wanted[userID] = true
	}
	for userID := range live {
		wanted[userID] = true
	}
	for _, key := range badged {
		wanted[strings.TrimPrefix(key, kvPrevStatusPrefix)] = true
	}

	// An empty emoji means take the badge off.
	type change struct {
		userID string
		emoji  string
		text   string
		kind   string
	}

	changes := make([]change, 0, len(wanted))

	// Who is wearing what once this is done, for the people index. People
	// kept through the grace below keep what they had, unless they have taken
	// their status back meanwhile (checked after the lock, like every other
	// read here).
	before := p.wornBadges()
	worn := make(map[string]string, len(wanted))
	var graced []string

	p.offlineLock.Lock()
	for userID := range wanted {
		switch {
		case celebrating[userID]:
			delete(p.offlineStreak, userID)
			changes = append(changes, change{userID, birthdayEmoji, birthdayBadgeText, badgeBirthday})

		case live[userID]:
			delete(p.offlineStreak, userID)
			changes = append(changes, change{userID, cfg.LiveEmoji, cfg.LiveStatusText, badgeLive})

		default:
			// Nothing to wear. The grace has to run out before the badge comes
			// off, so a reconnect does not flap. A birthday ending does not
			// flap, and waiting half a minute to take the cake off costs
			// nothing, so it goes through the same path. Only a tick is a
			// check: a poll somebody's click asked for, a moment after one,
			// is no more time off the air.
			if !ticked {
				graced = append(graced, userID)
				continue
			}
			p.offlineStreak[userID]++
			if p.offlineStreak[userID] < cfg.OfflineGraceChecks {
				graced = append(graced, userID)
				continue
			}
			delete(p.offlineStreak, userID)

			changes = append(changes, change{userID, "", "", ""})
		}
	}
	p.offlineLock.Unlock()

	// Through the grace the badge is still on, and still said to be the
	// plugin's: but only while it is. Somebody whose stream has ended and who
	// has since written a status of their own must not have those words
	// called the plugin's (and left out by a client) until the grace runs out.
	for _, userID := range graced {
		kind, wearing := before[userID]
		if !wearing {
			continue
		}
		if record, holding := p.readLive(userID); holding && !p.reclaimed(userID, record) {
			worn[userID] = kind
		}
	}

	// KV writes and status RPCs happen with no lock held, so a slow Mattermost
	// API cannot stall the next poll or make OnDeactivate wait on it.
	for _, c := range changes {
		if c.emoji == "" {
			p.clearLiveBadge(c.userID)
			continue
		}

		if p.setLiveBadge(c.userID, c.emoji, c.text) {
			worn[c.userID] = c.kind
		}
	}

	p.setWornBadges(worn)
}

// publish stores the snapshot and pushes it to every connected client, but only
// when something a user would notice actually changed. The poll timestamp alone
// is not a change, and nor is a stream's bitrate or a board's seconds ticking
// inside the minute: see noticeable.
func (p *Plugin) publish(current *snapshot) {
	// Here, so that whatever built the snapshot (a poll, a roster change, a
	// clip), the people in it are indexed from exactly what it carries.
	current.People = peopleIndex(current.Streams, current.Voice, p.wornBadges())

	// And the banners' word, as this node last read the list: a change made
	// here republishes (setMyBanner), and one made on another node is seen
	// within bannerListTTL.
	current.Banners = p.banners().word()

	// And the awards are decided from exactly the boards it carries: each
	// card's badges, what goes beside each name, and the boards themselves
	// as the document they are read from (boards.go).
	boards, err := json.Marshal(decideAwards(current))
	if err != nil {
		p.API.LogWarn("Could not encode the boards", "error", err.Error())
		return
	}

	key, err := json.Marshal(noticeable(current))
	if err != nil {
		p.API.LogWarn("Could not encode the stream snapshot", "error", err.Error())
		return
	}

	p.snapshotLock.Lock()
	if p.snapshot != nil && p.snapshot.FetchedAt > current.FetchedAt {
		// A slower poll finished after a newer one; discard it.
		p.snapshotLock.Unlock()
		return
	}

	fingerprint := sha256.Sum256(key)
	changed := p.lastPublished != string(fingerprint[:])
	if changed {
		p.lastPublished = string(fingerprint[:])
		p.snapshotTag = hex.EncodeToString(fingerprint[:8])
	}
	tag := p.snapshotTag
	p.snapshot = current
	p.snapshotSeq++
	seq := p.snapshotSeq

	// The boards of this very snapshot, stored with it. Their tag moves with
	// every second on them: it is only ever compared by a reader asking
	// whether the copy in hand is still what there is.
	p.boardsJSON, p.boardsTag = boards, shortSum(boards)
	p.snapshotLock.Unlock()

	// What each card says about a game is half of the line that says what
	// somebody is doing, which rides GET /accounts (accounts_activity.go):
	// told on every snapshot stored, pushed or not, and it does nothing
	// unless that has changed. Told with the snapshot's number, since two
	// publishes can get here in the other order from the one they stored in.
	p.accounts().noteOnAir(seq, current.Streams)

	if !changed {
		return
	}

	payload, err := json.Marshal(current)
	if err != nil {
		return
	}

	// The tag rides along, so a client that took this push can ask later
	// (after a reconnect, on coming back to the foreground) whether anything
	// has changed since, and be told no in a few bytes.
	p.API.PublishWebSocketEvent(wsEventStreams, map[string]any{
		"payload": string(payload),
		"tag":     tag,
	}, &model.WebsocketBroadcast{})
}

// noticeable is the snapshot as far as deciding whether to push it goes: a
// copy without what moves on every poll and that no panel shows.
//
//   - FetchedAt, the poll's own time.
//
//   - Each stream's Bitrate. It is what Broadcast Box measured on that read,
//     so it is different on every poll of a live stream, and the panel never
//     shows it. Counted, it pushed the whole snapshot to every connection on
//     every poll — around the clock, while a 24/7 channel is on the air — and
//     every push re-renders the panel and the badges on every visible post.
//
// The boards' seconds were the third, and the reason for a push a minute for
// as long as anybody was on the air or being watched: they tick on every
// poll. They are not in the snapshot at all now (GET /api/v1/boards, read by
// whoever is looking), and what it does say of the boards, boards.version and
// the awards beside each name, moves only when a place, a medal or a step
// does.
//
// Only the comparison leaves these out. The snapshot stored and pushed keeps
// them as read: the clips recorders size their rings from the bitrate.
func noticeable(current *snapshot) snapshot {
	comparable := *current
	comparable.FetchedAt = 0

	// A copy of the list, never the list itself: readers hold the stored
	// snapshot's streams without a lock.
	if len(current.Streams) > 0 {
		comparable.Streams = make([]apiStream, len(current.Streams))
		copy(comparable.Streams, current.Streams)
		for i := range comparable.Streams {
			comparable.Streams[i].Bitrate = 0
		}
	}

	return comparable
}

// countPollFailure records a failed status read and returns how many have
// happened in a row. Only a tick's is counted (ticked is pollOnce's): the
// failures a panel rides out are meant as that many intervals, and a poll
// somebody's click asked for during an outage would otherwise empty every
// panel sooner. Any other failure is told the count as it stands.
func (p *Plugin) countPollFailure(ticked bool) int {
	p.pollFailLock.Lock()
	defer p.pollFailLock.Unlock()

	if ticked {
		p.pollFails++
	}

	return p.pollFails
}

func (p *Plugin) resetPollFailures() {
	p.pollFailLock.Lock()
	p.pollFails = 0
	p.pollFailLock.Unlock()
}

// addCommunity attaches the schedule to a snapshot.
//
// Called on EVERY publish path, including the ones where Broadcast Box could
// not be reached. Saying when you will stream has nothing to do with the
// television server being up — and a row that emptied itself whenever the
// origin hiccupped would be a poor way to learn that. Standing in voice is
// the same, and is put on by addVoice.
func (p *Plugin) addCommunity(current *snapshot) {
	schedule, ok := p.scheduleItems(nowMillis())
	if !ok {
		// The store would not answer. Publishing an empty list would tell
		// every panel that nothing was planned, which is a confident lie
		// rather than a missing answer. Keep what was last known, exactly as a
		// failed status read keeps the last known stream list.
		current.Schedule = p.getSnapshot().Schedule

		return
	}

	current.Schedule = schedule
}

// addVoice puts who is in voice on a snapshot, on every publish path: an
// origin that cannot be reached says nothing about who is standing in voice.
//
// From the plugin's own memory, so it neither fails nor blocks, and as late as
// it can be, just before the snapshot is published, as addClips is. A roster
// change goes out without a poll (publishRosterSoon), and a poll that read the
// roster at its start, a Broadcast Box read and a pass over the store earlier,
// would be holding an older one than the panels have by then.
func (p *Plugin) addVoice(current *snapshot, cfg *configuration) {
	current.Voice = p.voiceOf(cfg)
}
