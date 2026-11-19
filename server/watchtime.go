package main

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// The Top Snoopers board: who spends the most time watching other people's
// streams in the panel, this month and all time.
//
// What counts is time by the clock, per person. Somebody with four tiles open,
// or the same stream in two tabs, is watching for one hour an hour, not four,
// so each pass credits each person once, whatever they have open.
//
// Who counts is narrower than who watches, on purpose:
//
//   - only people who have linked their stream, and only while they are
//     linked. Nobody else's watching is recorded at all, in memory or in the
//     KV store: they never opted into anything;
//   - not their own stream, or going live would be a way up the board;
//   - not a 24/7 channel, which anybody could leave open overnight;
//   - not a paused stream, which has nothing on it to watch, just as it earns
//     its streamer nothing;
//   - not the panel's hidden look-ins for preview stills, which handleWHEP
//     never records as anybody's watching, and which stillStore.counted leaves
//     out of the sessions besides.
//
// Only a session handleWHEP relayed for a signed-in person can count, because
// only those are in the watcher store. A session the server opens for itself —
// a peek's today, a recorder's one day — is never put there, so it is left out
// by the same rule that keeps it off the cards, with no list of exceptions to
// keep up to date.
//
// How the time is measured follows airtime's rules wherever they apply (see
// stats.go). The difference is the evidence. A stream's start comes from
// Broadcast Box and survives a restart of the plugin; who is watching is known
// only from the sessions this process relayed. So there is nothing to recover
// after a restart, and nothing that could be paid twice across one: each pass
// pays the time since the pass before it, from when each person started if
// that is later, and never more than a stretch the plugin could plausibly have
// missed (see watchCreditLimit). The time of that last pass is kept in memory
// for the reason lastPollAt is: a process that has just started really has
// not been watching.
//
// Stored as airtime is, in a totals table and one table per UTC month, split
// at the month boundary by the same creditSpan — but keyed by Mattermost user
// id, so no stream key is involved. They are read and written by loadStats and
// saveStats, with the same refusal to treat an unreadable table as an empty
// one.
//
// From 3.2.0 the same pass, on the same clock, keeps two more ledgers
// (design/3.2.0.md §4.3, §5.5), each all time and keyed by user id:
//
//   - fans_totals, how long each linked viewer has watched each linked
//     streamer, for biggest fans (fans.go). Who counts is exactly who the
//     board counts, and, by Victor's answer of 2026-10-12, only while Show
//     who is watching is on, when the cards already show who watches whom.
//   - chanwatch_totals, how long each linked viewer has watched each 24/7
//     channel an admin has given an achievement (chanwatch.go). 24/7
//     channels count toward that and nothing else: never the board, never a
//     fan.
//
// Neither can stop the board, and the board halting no longer stops them.

const (
	// Like maxTrackedStreamers: far above any real roster, so that a table
	// written on every poll has a size somebody chose.
	maxTrackedWatchers = 1000

	// What a row says when the account behind it cannot be read.
	unknownWatcher = "Unknown viewer"
)

// recordingHalt is a stop switch like airtime's (see haltStats): once a
// table's stored bytes will not decode, recording stops rather than carrying on
// in a degraded state. In memory, so a restart tries again, and the first
// poll that reaches the damaged document stops it again.
type recordingHalt struct {
	lock   sync.Mutex
	halted bool
	reason string
}

// stop halts recording, and reports whether it was running until now, so the
// reason is logged once rather than on every poll.
func (h *recordingHalt) stop(reason string) bool {
	h.lock.Lock()
	defer h.lock.Unlock()

	first := !h.halted
	h.halted = true
	h.reason = reason

	return first
}

func (h *recordingHalt) state() (bool, string) {
	h.lock.Lock()
	defer h.lock.Unlock()

	return h.halted, h.reason
}

// watchHaltOrSkip is haltOrSkip for the watching tables: recording stops for
// good when the stored bytes are the problem, and merely skips this pass when
// the database was.
func (p *Plugin) watchHaltOrSkip(err error) {
	if errors.Is(err, errStatsQuarantined) {
		if p.watchHalt.stop(err.Error()) {
			p.API.LogError(
				"Recording of watched hours has stopped so the stored history is not overwritten",
				"reason", err.Error(),
				"recover", "the unreadable document was copied to a "+kvQuarantinePrefix+" key in this plugin's KV store",
			)
		}

		return
	}

	p.API.LogWarn("Could not read the stored watching hours; skipping this poll", "error", err.Error())
}

func watchMonthBucket(now time.Time) string {
	return kvWatchMonthPrefix + now.UTC().Format("2006-01")
}

// watchCreditLimit is the most one pass pays anybody.
//
// A pass normally pays one polling interval, or the part of one since a
// refresh's pass: what is paid is the clock's time since the last pass, so a
// refresh in between splits a stretch and never adds to it. A longer stretch
// means the passes in between did not happen — Broadcast Box could not be
// reached, or a poll took its time — and a session still open on both sides
// of the gap says the person most likely went on watching, but not for how
// much of it. So no more is paid than the panel would itself have gone on
// showing their stream through the same outage: the failed ticks it rides
// out (refreshes are not counted; see countPollFailure), the one that ends
// it, and a slow answer. Clamped rather than refused, unlike an implausible
// airtime delta: there a long one means a wrong clock, and here only a gap.
func watchCreditLimit(cfg *configuration) time.Duration {
	interval := time.Duration(cfg.PollIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = defaultPollSeconds * time.Second
	}

	return interval*(pollFailuresBeforeEmpty+1) + httpTimeout
}

// ceilSecond rounds up to a whole second, so that somebody who pressed play
// part way through one is not paid for the part before they did.
func ceilSecond(t time.Time) time.Time {
	whole := t.Truncate(time.Second)
	if whole.Before(t) {
		return whole.Add(time.Second)
	}

	return whole
}

// watchSets is who this pass would pay, in each ledger, each person with when
// they started watching.
type watchSets struct {
	// The Top Snoopers board: somebody on several streams at once is here
	// once, from the earliest.
	snoopers map[string]time.Time

	// Biggest fans, by streamer then viewer: two streams at once credit both
	// streamers their own clock time.
	pairs map[string]map[string]time.Time

	// 24/7 channels with an achievement, by normalised key then viewer.
	channels map[string]map[string]time.Time
}

func (w watchSets) empty() bool {
	return len(w.snoopers) == 0 && len(w.pairs) == 0 && len(w.channels) == 0
}

// earliestIn notes that viewer has been watching since since under group in
// a two-level set, keeping the earliest.
func earliestIn(set map[string]map[string]time.Time, group, viewer string, since time.Time) {
	members := set[group]
	if members == nil {
		members = map[string]time.Time{}
		set[group] = members
	}

	if at, known := members[viewer]; !known || since.Before(at) {
		members[viewer] = since
	}
}

// watchingNow is who this pass would pay, each with when they started
// watching: everybody with a session the panel relayed on a stream this poll
// lists, live and not paused, that is neither a 24/7 channel nor their own,
// who has a linked stream right now. Somebody on several streams at once is
// in snoopers once, from the earliest.
//
// The same audiences, read in the same loop, give the other two ledgers: a
// pair for each linked streamer such a viewer watches (only while fans are
// counted, and only for a stream its owner linked by hand, never one matched
// by username), and each linked viewer of a 24/7 channel with an achievement.
//
// With faces off, it is also where the watcher store lets go of anybody the
// board would not count — remembered while faces were on, or unlinked since —
// because this is where their link is looked up anyway. A link that could not
// be read neither counts nor forgets anybody this poll.
func (p *Plugin) watchingNow(raw []bbStream, streams []apiStream, cfg *configuration, now time.Time) watchSets {
	byHandle := make(map[string]*apiStream, len(streams))
	for i := range streams {
		byHandle[streams[i].Handle] = &streams[i]
	}

	store := p.watchers()
	stills := p.stills()

	// The 24/7 channels whose watching is counted, by normalised key, and
	// whether biggest fans are counted at all this pass.
	channelKeys := p.countedChannels(cfg)
	countPairs := cfg.fansCounted()

	// One lookup per person per pass, however many streams they have open.
	type link struct {
		key  string
		read bool
	}

	// Linked as the gate means it, both link entries agreeing (linkOf): a
	// forward entry left behind by a race or a failed cleanup belongs to
	// somebody locked or let in, and let-in watching never counts (lock-screen
	// Q5). Such a person is forgotten like anybody unlinked.
	links := map[string]link{}
	linkFor := func(userID string) link {
		if known, looked := links[userID]; looked {
			return known
		}

		key, err := p.linkOf(userID)
		found := link{key: normalizeKey(key), read: err == nil}
		links[userID] = found

		return found
	}

	sets := watchSets{
		snoopers: map[string]time.Time{},
		pairs:    map[string]map[string]time.Time{},
		channels: map[string]map[string]time.Time{},
	}
	visited := map[string]bool{}

	for _, source := range raw {
		if !source.live() {
			continue
		}

		// Only the streams this poll lists, and each once: decorate leaves
		// out a stream whose handle another has already taken.
		handle := p.streamHandle(cleanStreamKey(source.StreamKey))
		stream := byHandle[handle]
		if stream == nil || visited[handle] {
			continue
		}
		visited[handle] = true

		countable := !stream.AlwaysOn && !stream.Paused
		streamKey := normalizeKey(stream.StreamKey)
		channel := stream.AlwaysOn && !stream.Paused && channelKeys[streamKey]

		// With faces on, the store is everybody's anyway, and a stream that
		// counts for nothing is not worth a lookup per person watching it.
		if !countable && !channel && cfg.ShowWatchers {
			continue
		}

		owner := ""
		if countPairs {
			owner = boardOwner(*stream)
		}

		for userID, since := range store.audience(handle, stills.counted(source.Sessions, now)) {
			linked := linkFor(userID)
			if !linked.read {
				continue
			}

			if linked.key == "" {
				if !cfg.ShowWatchers {
					store.forget(userID)
				}

				continue
			}

			if userID == stream.UserID || linked.key == streamKey {
				continue
			}

			if channel {
				earliestIn(sets.channels, streamKey, userID, since)
			}

			if !countable {
				continue
			}

			if at, known := sets.snoopers[userID]; !known || since.Before(at) {
				sets.snoopers[userID] = since
			}

			if owner != "" && owner != userID {
				earliestIn(sets.pairs, owner, userID, since)
			}
		}
	}

	return sets
}

// forgetWatchPass makes the next pass only look, as the first pass of a
// process does, so that switching the board back on pays nothing for the time
// it spent off.
//
// Called on every pass the board is off, and by OnConfigurationChange whenever
// the setting changes. A pass only runs on a poll that reached Broadcast Box,
// and the board can be switched off and on again while none does; then
// nothing but the settings change itself is there to forget the old pass.
func (p *Plugin) forgetWatchPass() {
	p.statsWriteLock.Lock()
	p.watchPollAt = 0
	p.statsWriteLock.Unlock()
}

// recordWatchTime credits everybody watching with the time since the last
// pass, in each ledger. raw is this poll's listing and streams what decorate
// made of it.
//
// One clock for all three (from, watchCreditLimit, the first-pass rule,
// watchPollAt), and the board first, exactly as it always ran: a failed read
// or save of the board returns without marking the pass done, so nothing else
// is paid this pass either, and the next one pays the stretch to all three.
// The fan and channel ledgers cannot do that to the board: a failed load or
// save of either loses that ledger one pass, and the pass carries on.
func (p *Plugin) recordWatchTime(raw []bbStream, streams []apiStream, cfg *configuration, now time.Time) {
	if !cfg.TopWatchers {
		p.forgetWatchPass()
		return
	}

	// Whole seconds throughout. A pass is a few seconds long, and a fraction
	// of one dropped on every pass would add up to minutes a day that nobody
	// was ever paid.
	now = now.UTC().Truncate(time.Second)

	// Before the halt, so that a halted board still forgets the people it
	// would not count.
	sets := p.watchingNow(raw, streams, cfg, now)

	// One writer at a time, as for airtime.
	p.statsWriteLock.Lock()
	defer p.statsWriteLock.Unlock()

	nowMS := now.UnixMilli()
	previous := p.watchPollAt

	// The first pass of this process, or the first since the board was
	// switched on, only looks: there is no pass before it to measure from,
	// and before it nothing was being counted. A clock that has gone
	// backwards starts again from here too, rather than paying nobody until
	// it has caught up with itself.
	if previous == 0 || nowMS < previous {
		p.watchPollAt = nowMS
		return
	}

	// Nobody to pay is a quiet pass: no reads and no writes.
	if sets.empty() {
		p.watchPollAt = nowMS
		return
	}

	from := time.UnixMilli(previous).UTC()
	if earliest := now.Add(-watchCreditLimit(cfg)); from.Before(earliest) {
		from = earliest
	}

	if halted, _ := p.watchHalt.state(); !halted && len(sets.snoopers) > 0 {
		if !p.creditSnoopersLocked(sets.snoopers, from, now) {
			return
		}
	}

	if len(sets.pairs) > 0 {
		p.creditFansLocked(sets.pairs, from, now)
	}

	if len(sets.channels) > 0 {
		p.creditChannelsLocked(sets.channels, from, now)
	}

	p.watchPollAt = nowMS
}

// creditSnoopersLocked is the Top Snoopers board's share of a pass, exactly
// as 3.1.0 ran it, under statsWriteLock. False when a table could not be read
// or saved: the pass is then not marked done, so a transient failure is paid
// on the next one.
func (p *Plugin) creditSnoopersLocked(watching map[string]time.Time, from, now time.Time) bool {
	// An unreadable table is never treated as an empty one, or the write at
	// the end would replace the whole history with this pass.
	totals, err := p.loadStats(kvWatchTotals)
	if err != nil {
		p.watchHaltOrSkip(err)
		return false
	}

	// In a fixed order, so that a full table always turns away the same
	// people rather than whoever a map happened to hand over first.
	people := make([]string, 0, len(watching))
	for userID := range watching {
		people = append(people, userID)
	}
	sort.Strings(people)

	months := newMonthTables(p, kvWatchMonthPrefix)
	credited := false

	for _, userID := range people {
		start := from
		if started := ceilSecond(watching[userID].UTC()); started.After(start) {
			start = started
		}

		seconds := int64(now.Sub(start) / time.Second)
		if seconds <= 0 {
			continue
		}

		if len(totals) >= maxTrackedWatchers {
			if _, tracked := totals[userID]; !tracked {
				p.API.LogWarn("Not counting a new watcher: the watching table is full", "limit", maxTrackedWatchers)
				continue
			}
		}

		// Across every UTC month the stretch really covers, as airtime is.
		if err := months.creditSpan(userID, "", start, now); err != nil {
			p.watchHaltOrSkip(err)
			return false
		}

		credit(totals, userID, "", seconds)
		credited = true
	}

	// Totals first, then the months, and the pass is only marked done once
	// both have landed. Paying a failed pass again on the next one can at
	// worst count one pass twice in whichever table did save, which is the
	// cheaper mistake, exactly as for airtime.
	if credited {
		if err := p.saveStats(kvWatchTotals, totals); err != nil {
			p.API.LogWarn("Could not save the watching hours; they will be counted again on the next poll", "error", err.Error())
			return false
		}

		if err := months.save(); err != nil {
			p.API.LogWarn("Could not save this month's watching hours; they will be counted again on the next poll", "error", err.Error())
			return false
		}
	}

	return true
}

// addWatchBoards puts the Top Snoopers boards on a snapshot. As with the
// airtime boards, a table that cannot be read leaves both off rather than
// showing a ranking that looks like people lost their place, and WatchBoards
// stays false with them, so the panel does not claim nobody has watched.
func (p *Plugin) addWatchBoards(current *snapshot, cfg *configuration, now time.Time) {
	p.addWatchBoardsFor(current, cfg, now, p.newPollPeople())
}

// addWatchBoardsFor is addWatchBoards with a poll's own answers about people,
// shared with the airtime boards, biggest fans and achievements, so nobody is
// looked up twice in one poll.
func (p *Plugin) addWatchBoardsFor(current *snapshot, cfg *configuration, now time.Time, people *pollPeople) {
	totals, month, ok := p.watchTablesShown(cfg, now)
	if !ok {
		return
	}

	// The figures the Snooped achievements are worked out from: the table
	// exactly as the board is about to show it.
	p.noteSnoopedFigures(totals)

	// One resolver and one set of link lookups for both boards: the same
	// people are usually on each.
	resolve, linked := people.resolve, people.linked

	current.WatchBoards = true
	current.WatchAllTime, current.WatchAllTimeTotal = p.watchBoard(totals, maxLeaderboardRows, resolve, linked)
	current.WatchMonth, current.WatchMonthTotal = p.watchBoard(month, maxLeaderboardRows, resolve, linked)

	// The same month the airtime board is for, and set here too, so it is
	// there when only this board is on.
	current.MonthLabel = monthLabel(now)
}

// watchTables is the two watching tables as the boards were last built from
// them, and the hour on the clock they were read in. See watchTablesShown.
type watchTables struct {
	lock   sync.Mutex
	hour   int64
	totals statTable
	month  statTable
}

// at is the tables read in hour, if those are the ones kept.
func (w *watchTables) at(hour int64) (statTable, statTable, bool) {
	w.lock.Lock()
	defer w.lock.Unlock()

	if w.totals == nil || w.hour != hour {
		return nil, nil, false
	}

	return w.totals, w.month, true
}

func (w *watchTables) keep(hour int64, totals, month statTable) {
	w.lock.Lock()
	defer w.lock.Unlock()

	w.hour = hour
	w.totals = totals
	w.month = month
}

// watchTablesShown reads the watching tables for the boards.
//
// With faces on, as they stand: everybody can see who is watching what on
// the cards anyway. With faces off, as they stood at the first poll of the
// hour, and the boards are built from that copy until the next hour begins.
// Otherwise anybody could compare two snapshots a poll apart and see whose
// seconds went up — who is watching right now, and with one stream on the
// air, what — which is exactly what switching faces off is for hiding. Only
// the figures wait: names and links are looked up afresh on every poll, so a
// rename shows at once and somebody who unlinks leaves the boards at once.
//
// By the hour on the clock rather than an hour from whenever, so a new month
// starts from a fresh read: months begin on the hour too. And kept on every
// fresh read, faces on or off, so switching faces off carries on from the
// figures last shown rather than going back to older ones.
func (p *Plugin) watchTablesShown(cfg *configuration, now time.Time) (statTable, statTable, bool) {
	hour := now.UTC().Truncate(time.Hour).Unix()

	if !cfg.ShowWatchers {
		if totals, month, kept := p.watchShown.at(hour); kept {
			return totals, month, true
		}
	}

	totals, totalsErr := p.loadStats(kvWatchTotals)
	month, monthErr := p.loadStats(watchMonthBucket(now))
	if totalsErr != nil || monthErr != nil {
		return nil, nil, false
	}

	p.watchShown.keep(hour, totals, month)

	return totals, month, true
}

// pollPeople is one poll's answers about people: who a user id is, and
// whether they are linked now, each asked at most once a poll however many
// boards, fans and achievements they are on.
type pollPeople struct {
	resolve *identityResolver
	linked  func(userID string) bool
}

func (p *Plugin) newPollPeople() *pollPeople {
	return &pollPeople{resolve: newIdentityResolver(p), linked: p.linkedNow()}
}

// linkedNow answers whether somebody has a linked stream right now, both
// link entries agreeing (hasLink), asking the store once per person however
// many boards they are on. A link that cannot be read counts as none: a row
// left off for one poll is a cheaper mistake than a name shown that should
// not be.
func (p *Plugin) linkedNow() func(userID string) bool {
	known := map[string]bool{}

	return func(userID string) bool {
		if linked, asked := known[userID]; asked {
			return linked
		}

		linked := p.hasLink(userID)
		known[userID] = linked

		return linked
	}
}

// watchBoard ranks a watching table, most hours first, by rankRows: the same
// total order as the airtime boards, ending on the key, which here is the user
// id. So two people on identical seconds keep their places from one poll to
// the next and the snapshot does not churn.
//
// Only people linked now are on it, ranked and counted among themselves.
// Their hours stay stored when they unlink, and come back with them when they
// link again; the board just never names somebody who has stopped taking
// part, the way the airtime boards only medal a linked stream.
//
// Names are resolved now rather than stored, like the airtime boards', so a
// rename shows on the next poll. Everybody with hours is listed up to limit,
// with the true count beside them.
func (p *Plugin) watchBoard(table statTable, limit int, resolve *identityResolver, linked func(userID string) bool) ([]leaderEntry, int) {
	rows := make([]rankedRow, 0, len(table))
	for _, row := range rankRows(table) {
		if linked(row.key) {
			rows = append(rows, row)
		}
	}

	total := len(rows)

	if limit > 0 && total > limit {
		rows = rows[:limit]
	}

	board := make([]leaderEntry, 0, len(rows))
	for i, row := range rows {
		entry := leaderEntry{Rank: i + 1, Seconds: row.seconds, UserID: row.key, Title: unknownWatcher}

		if user := resolve.person(row.key); user != nil {
			entry.Username = user.Username
			entry.DisplayName = displayNameOf(user)
			entry.Title = entry.DisplayName
		}

		board = append(board, entry)
	}

	return board, total
}

// carryWatchBoards keeps the watchers' boards of the snapshot before, for a
// poll that could not reach Broadcast Box.
func (s *snapshot) carryWatchBoards(previous *snapshot) {
	s.WatchBoards = previous.WatchBoards
	s.WatchMonth = previous.WatchMonth
	s.WatchAllTime = previous.WatchAllTime
	s.WatchMonthTotal = previous.WatchMonthTotal
	s.WatchAllTimeTotal = previous.WatchAllTimeTotal
}
