package main

import (
	"sort"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Streamed hours are accumulated here, because /api/status only ever describes
// the present moment.
//
// Time is credited as a DELTA against the session's own start, not by adding
// the poll interval: streamStart comes from the origin and is authoritative, so
// a stream that ran while the plugin was restarting still gets its time when
// the plugin comes back. Each session records how many seconds of it have
// already been credited, so nothing is counted twice.
//
// Three KV documents rather than one row per streamer: the whole table is small
// and this keeps a poll to a fixed number of reads and writes regardless of how
// many people stream. Reading and writing them — and the rules that keep them
// from being destroyed by a future version of this plugin — live in
// statsstore.go.

type statEntry struct {
	Seconds int64  `json:"seconds"`
	Title   string `json:"title"`
}

type statTable map[string]statEntry

// sessionMark remembers how much of the current session has been credited.
type sessionMark struct {
	Start    string `json:"start"`
	Credited int64  `json:"credited"`

	// LastSeen is when this stream was last in the status listing, in
	// milliseconds. A mark outlives a brief absence, because forgetting it is
	// exactly what makes a reconnect credit the whole session a second time.
	// Absent in documents written before 1.12, which is read as "never", and
	// handled below rather than expiring every mark on the first poll.
	LastSeen int64 `json:"lastSeen,omitempty"`

	// Synced is the resynchronisation epoch this session has already been
	// brought up to date against, so a settings change clips each session once
	// rather than every poll or, worse, none of them.
	Synced int64 `json:"synced,omitempty"`
}

type sessionMarks map[string]sessionMark

// leaderEntry is one row of a leaderboard, as handed to the panel. It never
// carries a stream key.
type leaderEntry struct {
	Rank        int    `json:"rank"`
	Title       string `json:"title"`
	Seconds     int64  `json:"seconds"`
	UserID      string `json:"userId,omitempty"`
	Username    string `json:"username,omitempty"`
	DisplayName string `json:"displayName,omitempty"`

	// Of Seconds, how many were live on Twitch (twitch_air.go). Absent for
	// everybody without Twitch hours, so a board without Twitch is what it
	// always was.
	TwitchSeconds int64 `json:"twitchSeconds,omitempty"`
}

// Calendar boundaries are UTC, never the server's local time. Which month a
// broadcast counts toward should not depend on where the host happens to be
// configured, and moving the server — or a timezone database update — must not
// silently reshuffle history. Both helpers normalise internally so no caller
// can reintroduce a local-time boundary.
func monthBucket(now time.Time) string {
	return kvStatsMonthPrefix + now.UTC().Format("2006-01")
}

func monthLabel(now time.Time) string {
	return now.UTC().Format("January 2006")
}

// lastObserved is the most recent moment any session was seen, which is the
// best evidence available that the plugin was running and watching. Zero when
// there is nothing to go on.
func lastObserved(marks sessionMarks) int64 {
	var latest int64
	for _, mark := range marks {
		if mark.LastSeen > latest {
			latest = mark.LastSeen
		}
	}

	return latest
}

// claimable decides how much of a session a BRAND NEW mark is allowed to reach
// back and claim, expressed as the seconds it must treat as already credited.
//
// A new mark used to start at zero, which meant the whole session so far. That
// is right after an outage — a broadcast that ran while the plugin was
// restarting should still get its time, and it is why airtime is measured
// against the stream's own start in the first place. It is wrong when the mark
// merely went missing while the plugin was up: an unlisted stream that comes
// back after half an hour, or a mark dropped by the retention sweep, would be
// paid for the whole broadcast a second time.
//
// The two are told apart by evidence rather than by guessing: a session may
// only claim the window in which the plugin can be shown to have been away.
func claimable(elapsed, nowMS, observedAt int64) int64 {
	if observedAt <= 0 {
		// Nothing was being watched, so there is nothing to contradict the
		// session's own account of itself. maxCreditPerPoll still bounds it.
		return 0
	}

	unobserved := (nowMS - observedAt) / 1000
	if unobserved < 0 {
		unobserved = 0
	}
	if elapsed <= unobserved {
		return 0
	}

	return elapsed - unobserved
}

// monthTables loads the monthly documents a poll actually touches, and no
// others. Almost every poll touches exactly one.
//
// prefix says whose months they are: the airtime tables, or the hours spent
// watching (see watchtime.go). Both are split across UTC months by exactly the
// same rules, so they share this rather than each keeping a copy of it.
type monthTables struct {
	plugin *Plugin
	prefix string
	loaded map[string]statTable
}

func newMonthTables(p *Plugin, prefix string) *monthTables {
	return &monthTables{plugin: p, prefix: prefix, loaded: map[string]statTable{}}
}

func (m *monthTables) table(bucket string) (statTable, error) {
	if table, ok := m.loaded[bucket]; ok {
		return table, nil
	}

	table, err := m.plugin.loadStats(bucket)
	if err != nil {
		return nil, err
	}
	m.loaded[bucket] = table

	return table, nil
}

// creditSpan credits the interval [from, to) to each UTC month it covers,
// giving every month only the seconds that actually fall inside it.
func (m *monthTables) creditSpan(key, title string, from, to time.Time) error {
	from = from.UTC()
	to = to.UTC()

	for from.Before(to) {
		end := startOfNextMonth(from)
		if to.Before(end) {
			end = to
		}

		seconds := int64(end.Sub(from).Seconds())
		if seconds > 0 {
			table, err := m.table(m.prefix + from.Format("2006-01"))
			if err != nil {
				return err
			}
			credit(table, key, title, seconds)
		}

		from = end
	}

	return nil
}

func (m *monthTables) save() error {
	for bucket, table := range m.loaded {
		if err := m.plugin.saveStats(bucket, table); err != nil {
			return err
		}
	}

	return nil
}

// reachesBeforeMonth reports whether this poll touched the table of any month
// earlier than the one now falls in. Compared under the tables' own prefix,
// because the keys are compared as strings and every twitchair_month_ key
// sorts after every stats_month_ one.
func (m *monthTables) reachesBeforeMonth(now time.Time) bool {
	bucket := m.prefix + now.UTC().Format("2006-01")
	for loaded := range m.loaded {
		if loaded < bucket {
			return true
		}
	}

	return false
}

func startOfNextMonth(t time.Time) time.Time {
	utc := t.UTC()

	return time.Date(utc.Year(), utc.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, 1, 0)
}

func credit(table statTable, key, title string, seconds int64) {
	entry := table[key]
	entry.Seconds += seconds
	if title != "" {
		entry.Title = title
	}
	table[key] = entry
}

// recordStreamTime credits every live, attended stream with the time that has
// elapsed since it was last seen.
func (p *Plugin) recordStreamTime(streams []apiStream, cfg *configuration, now time.Time) {
	if !cfg.EnableLeaderboard {
		return
	}
	if halted, _ := p.statsHalt(); halted {
		return
	}

	// One writer at a time. The poller is the usual one, but an admin
	// restoring a backup or importing a file is another, and a read-modify-
	// write race between them would silently drop whichever landed first.
	p.statsWriteLock.Lock()
	defer p.statsWriteLock.Unlock()

	// Who has a Twitch connection, so that a second paid here is never paid
	// again by the Twitch ledger (twitch_air.go). Nil while Twitch is off or
	// nobody is connected, and then nothing below reads a Twitch key. Not
	// knowing is not guessed at: the pass is skipped, and its marks are not
	// advanced, so nothing is lost.
	payback, err := p.newTwitchPayback(cfg)
	if err != nil {
		p.API.LogWarn("Could not read who has connected Twitch; skipping this poll", "error", err.Error())
		return
	}

	// A table that could not be read is never treated as an empty one: the
	// write at the end of this function would replace the whole history with
	// nothing.
	//
	// How long that lasts depends on WHY. Bytes that will not decode are a
	// standing problem — retrying rewrites nothing and each attempt is another
	// chance to get it wrong — so recording stops until an admin deals with it.
	// A database that was briefly unavailable just means skipping this poll.
	totals, err := p.loadStats(kvStatsTotals)
	if err != nil {
		p.haltOrSkip(err)
		return
	}

	marks, err := p.loadSessions()
	if err != nil {
		// Transient, not corruption. Carrying on with no marks would credit
		// every live stream its whole session again.
		p.API.LogWarn("Could not read stream sessions; skipping this poll", "error", err.Error())
		return
	}

	nowMS := now.UnixMilli()

	// Sessions running when a settings change made backdated airtime unwanted
	// are brought up to date against this, once each.
	resyncAt := p.resyncEpoch(nowMS)

	// When the plugin was last demonstrably watching, from two sources.
	//
	// The marks carry it across a restart, which is what keeps the recovery
	// after an upgrade honest. They are not enough on their own: when nobody
	// has streamed for half an hour the map is empty and the evidence goes
	// with it, and a stream that was running unlisted all along could then
	// claim the whole broadcast on the poll it first appears in. The
	// in-process timestamp covers exactly that gap, and is deliberately not
	// persisted — a process that has just started really has not been watching.
	observedAt := lastObserved(marks)
	if p.lastPollAt > observedAt {
		observedAt = p.lastPollAt
	}
	p.lastPollAt = nowMS

	present := make(map[string]bool, len(streams))
	months := newMonthTables(p, kvStatsMonthPrefix)

	// Two separate reasons to write, because they have very different costs.
	// credited means the totals actually moved; marksDirty means only the
	// session bookkeeping did. Conflating them had the 24/7 channel writing
	// three KV documents every fifteen seconds for ever.
	credited := false
	marksDirty := false

	for _, stream := range streams {
		// An unattended channel would win every leaderboard by simply never
		// stopping. It is skipped outright, marks and all: it can never be
		// credited, so there is nothing worth writing about it, and keeping a
		// mark current for it would rewrite the sessions document every few
		// seconds for ever.
		//
		// The cost of having no mark is that a key taken back OUT of
		// "24/7 stream keys" would look like a brand new session that
		// started hours ago. OnConfigurationChange arms a resynchronisation
		// when that happens, which is the branch below.
		if stream.AlwaysOn {
			continue
		}

		if stream.StreamStart == "" {
			continue
		}

		key := normalizeKey(stream.StreamKey)
		if key == "" {
			continue
		}

		start, parseErr := time.Parse(time.RFC3339Nano, stream.StreamStart)
		if parseErr != nil {
			continue
		}

		elapsed := int64(now.Sub(start).Seconds())
		if elapsed <= 0 {
			// A start in the future means the clocks disagree; wait it out.
			continue
		}

		present[key] = true

		mark, known := marks[key]
		if !known || mark.Start != stream.StreamStart {
			mark = sessionMark{Start: stream.StreamStart, Credited: claimable(elapsed, nowMS, observedAt)}
			marksDirty = true
		}

		// Always brought up to date in memory, but only worth a write of its
		// own once it has drifted. Whenever the document is written for some
		// other reason the timestamp goes with it, so it is exact for as long
		// as anything is being credited — which matters, because how far back
		// a new mark may reach is measured from it.
		if nowMS-mark.LastSeen > lastSeenRefreshMS {
			marksDirty = true
		}
		mark.LastSeen = nowMS

		// The first time this session is seen after a settings change that
		// made backdated airtime unwanted, it is brought up to date and earns
		// nothing for the period before the change. Recorded on the mark, so
		// it happens once per session however many polls it takes to see them
		// all.
		if resyncAt != 0 && mark.Synced != resyncAt {
			mark.Credited = elapsed
			mark.Synced = resyncAt
			marks[key] = mark
			marksDirty = true

			continue
		}

		// A paused stream is not on air, so it earns nothing — but its mark is
		// kept current, or the minutes it spent paused would all be handed
		// over the moment it came back.
		if stream.Paused {
			if mark.Credited != elapsed {
				mark.Credited = elapsed
				marksDirty = true
			}
			marks[key] = mark

			continue
		}

		delta := elapsed - mark.Credited
		if delta <= 0 {
			marks[key] = mark
			continue
		}

		if delta > maxCreditPerPoll {
			// Not clamped. A delta this size means streamStart is wrong, and
			// writing thirty hours of invented airtime that nothing can ever
			// take back is worse than writing none. Resynchronise instead.
			p.API.LogWarn("Ignoring an implausible stream duration", "stream", keyPrefix(stream.StreamKey), "seconds", delta)
			mark.Credited = elapsed
			marks[key] = mark
			marksDirty = true

			continue
		}

		if len(totals) >= maxTrackedStreamers {
			if _, tracked := totals[key]; !tracked {
				// The mark is deliberately NOT advanced: this stream was not
				// paid, and recording it as paid would lose the time for good
				// if room appears later.
				marks[key] = mark
				p.API.LogWarn("Not counting a new streamer: the airtime table is full", "limit", maxTrackedStreamers)

				continue
			}
		}

		// Credited across every month the interval really covers, rather than
		// billed wholesale to the month the poll happens to run in. A delta is
		// normally one poll interval and lands in one bucket; after a gap it
		// can be hours, and a gap across midnight on the 1st would otherwise
		// hand the whole stretch to the wrong month.
		from := start.Add(time.Duration(mark.Credited) * time.Second)
		to := start.Add(time.Duration(elapsed) * time.Second)
		if err := months.creditSpan(key, stream.Title, from, to); err != nil {
			p.haltOrSkip(err)
			return
		}

		// Paid in full, as always; and for somebody with a Twitch
		// connection, recorded as paid by Broadcast Box, with whatever the
		// Twitch ledger already held of it taken back.
		if owner := boardOwner(stream); payback.covers(owner) {
			base := start.Unix()
			if err := payback.settle(owner, base+mark.Credited, base+elapsed); err != nil {
				p.API.LogWarn("Could not read the Twitch hours; skipping this poll", "error", err.Error())
				return
			}
		}

		mark.Credited = elapsed
		marks[key] = mark

		credit(totals, key, stream.Title, delta)
		credited = true
		marksDirty = true
	}

	// Marks outlive a brief absence. Dropping one the moment a stream flickers
	// is what made an OBS reconnect — or a pause, which leaves the listing
	// entirely on some builds — credit the whole session over again.
	for key, mark := range marks {
		if present[key] {
			continue
		}

		if mark.LastSeen == 0 {
			// Written before marks carried a timestamp. Give it a full grace
			// window from now instead of expiring every one of them on the
			// first poll after an upgrade.
			mark.LastSeen = nowMS
			marks[key] = mark
			marksDirty = true

			continue
		}

		if nowMS-mark.LastSeen < sessionRetentionMS {
			continue
		}

		delete(marks, key)
		marksDirty = true
	}

	// Totals first, then the month, and the session marks only once both have
	// landed. The marks are what stops time being counted twice, so advancing
	// them over a failed write would throw the delta away for good. Replaying
	// it instead can at worst double-count one poll interval in the window
	// where one table saved and the other did not, which is the cheaper
	// mistake of the two.
	if credited {
		if err := p.saveStats(kvStatsTotals, totals); err != nil {
			p.API.LogWarn("Could not save all-time airtime; it will be counted again on the next poll", "error", err.Error())
			return
		}

		saveErr := months.save()

		// Time that fell in a month that has already ended can change who
		// finished on its podium, and the medals in people's profiles are read
		// from a copy of those. It is the tail of a broadcast running across
		// midnight on the 1st, or a stretch recovered after an outage, and it
		// is not always paid on the first poll of the new month: a skipped
		// poll or a reconnect can carry it to a later one. Checked before the
		// error, because a save that failed part way may still have written
		// some of the months.
		if months.reachesBeforeMonth(now) {
			p.forgetMonthPodiums()
		}

		if saveErr != nil {
			p.API.LogWarn("Could not save monthly airtime; it will be counted again on the next poll", "error", saveErr.Error())
			return
		}
	}

	// The Twitch side of what was just paid, after the Broadcast Box tables
	// and before the marks. A failure leaves the marks where they were, so
	// the worst case is the old one: one interval counted again (or taken
	// back from Twitch again, which stops at nothing).
	if err := payback.save(now); err != nil {
		p.API.LogWarn("Could not save the Twitch side of a simulcast; it will be settled again on the next poll", "error", err.Error())
		return
	}

	if marksDirty {
		p.saveSessions(marks)
	}
}

// identityResolver answers "who owns this stream key" once per poll instead of
// once per row per board. Each row used to cost a KVGet and a GetUser, and the
// same streamer appears on both the monthly and the all-time board — with the
// boards cut to three that was twelve round trips a poll, and with every
// streamer listed it grows with the roster.
type identityResolver struct {
	plugin *Plugin
	users  map[string]*model.User

	// By user id, for the boards that are kept by person rather than by
	// stream key: see watchtime.go.
	people map[string]*model.User
}

func newIdentityResolver(p *Plugin) *identityResolver {
	return &identityResolver{plugin: p, users: map[string]*model.User{}, people: map[string]*model.User{}}
}

func (r *identityResolver) user(streamKey string) *model.User {
	if user, known := r.users[streamKey]; known {
		return user
	}

	var user *model.User
	if userID := r.plugin.getLinkedUser(streamKey); userID != "" {
		if found, err := r.plugin.API.GetUser(userID); err == nil {
			user = found
		}
	}
	r.users[streamKey] = user

	return user
}

// person answers "who is this user id" once per poll, for the same reason
// user does. nil when the account cannot be read.
func (r *identityResolver) person(userID string) *model.User {
	if user, known := r.people[userID]; known {
		return user
	}

	var user *model.User
	if found, err := r.plugin.API.GetUser(userID); err == nil {
		user = found
	}
	r.people[userID] = user

	return user
}

// boardRow is one line on a board: a person, or a stream nobody has linked.
type boardRow struct {
	id      string      // "u:" + user id, or "k:" + normalised stream key
	user    *model.User // nil for a stream nobody has linked
	seconds int64
	twitch  int64  // of seconds, how many came from Twitch
	sortBy  string // display name for a person; the stored title for a stream (as rankRows)
	title   string // what the board shows
}

// boardRows merges one Broadcast Box table and, when tw is not nil, one Twitch
// table into rows by person. owners maps a Twitch id to its Mattermost user
// (the link document as last read).
//
// Identity is resolved now rather than trusted from the stored title, which
// may predate a name override or a user linking their stream. A stream key
// resolves to a person only through its link (resolve.user), as it always
// did; a Twitch id only through its connection, and a Twitch row with neither
// (a purge still pending, an account gone) is left out. Two rows of one
// person, two linked keys or a key and a Twitch channel, are one row.
//
// The order is total, as rankRows's is: seconds, then the name, then the id.
// A snapshot that changes order between polls pushes a message to every
// client for nothing.
func (p *Plugin) boardRows(bb, tw statTable, cfg *configuration, resolve *identityResolver, owners map[string]string) []boardRow {
	overrides := cfg.nameOverrides()
	if resolve == nil {
		resolve = newIdentityResolver(p)
	}

	byID := map[string]*boardRow{}
	person := func(user *model.User) *boardRow {
		id := "u:" + user.Id
		row := byID[id]
		if row == nil {
			name := displayNameOf(user)
			row = &boardRow{id: id, user: user, sortBy: name, title: name}
			byID[id] = row
		}

		return row
	}

	for key, entry := range bb {
		if entry.Seconds <= 0 {
			continue
		}

		if user := resolve.user(key); user != nil {
			person(user).seconds += entry.Seconds
			continue
		}

		// A title stored by a build that predates opaque handles could be the
		// stream key itself. Nothing may put one of those in a browser.
		title := entry.Title
		if strings.EqualFold(title, key) {
			title = ""
		}
		if name := overrides[key]; name != "" {
			title = name
		}
		if title == "" {
			title = "Unnamed stream"
		}

		id := "k:" + key
		row := byID[id]
		if row == nil {
			row = &boardRow{id: id, sortBy: entry.Title, title: title}
			byID[id] = row
		}
		row.seconds += entry.Seconds
	}

	for id, entry := range tw {
		if entry.Seconds <= 0 {
			continue
		}

		owner := owners[id]
		if owner == "" {
			continue
		}
		user := resolve.person(owner)
		if user == nil {
			continue
		}

		row := person(user)
		row.seconds += entry.Seconds
		row.twitch += entry.Seconds
	}

	rows := make([]boardRow, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, *row)
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].seconds != rows[j].seconds {
			return rows[i].seconds > rows[j].seconds
		}
		if folded := strings.Compare(strings.ToLower(rows[i].sortBy), strings.ToLower(rows[j].sortBy)); folded != 0 {
			return folded < 0
		}

		return rows[i].id < rows[j].id
	})

	return rows
}

// leaderboard turns rows into a board, highest first.
//
// Everyone who has streamed appears, not just the podium — the hours were
// always being recorded for everybody, only the display was cut to three. The
// limit is a bound on the WebSocket message, not an editorial decision, so the
// true count comes back alongside the rows and the panel says when it has cut.
// The count is of rows, which is people.
//
// board names which board this is, for the log: the cut is said once for
// each count a board reaches, not on every poll for as long as it lasts.
func (p *Plugin) leaderboard(board string, rows []boardRow, limit int) ([]leaderEntry, int) {
	total := len(rows)

	if limit > 0 && total > limit {
		if said, known := p.boardCutsLogged.Swap(board, total); !known || said != total {
			p.API.LogWarn("More streamers have airtime than the board will carry", "board", board, "limit", limit, "total", total)
		}
		rows = rows[:limit]
	}

	leaders := make([]leaderEntry, 0, len(rows))
	for i, row := range rows {
		entry := leaderEntry{Rank: i + 1, Title: row.title, Seconds: row.seconds, TwitchSeconds: row.twitch}

		if row.user != nil {
			entry.UserID = row.user.Id
			entry.Username = row.user.Username
			entry.DisplayName = displayNameOf(row.user)
			entry.Title = entry.DisplayName
		}

		leaders = append(leaders, entry)
	}

	return leaders, total
}

type rankedRow struct {
	key     string
	seconds int64
	title   string
}

// rankRows orders a table highest first. The stream key is the final
// comparison, and it is load-bearing: sort.Slice is not stable and the rows
// come out of a map in a random order, so seconds-then-title alone is not a
// total order. Two streamers on identical seconds with identical titles would
// swap places between polls, which changes the snapshot fingerprint and pushes
// a pointless WebSocket message to every client, for ever.
func rankRows(table statTable) []rankedRow {
	rows := make([]rankedRow, 0, len(table))
	for key, entry := range table {
		if entry.Seconds <= 0 {
			continue
		}
		rows = append(rows, rankedRow{key: key, seconds: entry.Seconds, title: entry.Title})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].seconds != rows[j].seconds {
			return rows[i].seconds > rows[j].seconds
		}
		if folded := strings.Compare(strings.ToLower(rows[i].title), strings.ToLower(rows[j].title)); folded != 0 {
			return folded < 0
		}

		return rows[i].key < rows[j].key
	})

	return rows
}

// rankByRow maps a row's id to its place, for decorating cards.
func rankByRow(rows []boardRow) map[string]int {
	ranks := map[string]int{}

	// Only the podium: the badges are an award, and an award everybody has is
	// not one. The boards list everyone regardless.
	for i, row := range rows {
		if i >= podiumSize {
			break
		}
		ranks[row.id] = i + 1
	}

	return ranks
}

// cardRowID is the board row a live card belongs to, by the same resolver
// the boards were built with in this poll, so a card and the board can never
// disagree about whose place it is.
func cardRowID(stream apiStream, resolve *identityResolver) string {
	if stream.Source == sourceTwitch {
		return "u:" + stream.UserID
	}

	key := normalizeKey(stream.StreamKey)
	if user := resolve.user(key); user != nil {
		return "u:" + user.Id
	}

	return "k:" + key
}
