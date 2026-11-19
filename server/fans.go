package main

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"
)

// Biggest fans (design/3.2.0.md §4): for each linked streamer, the linked
// viewer who has spent the most time watching their Broadcast Box streams in
// the panel, all time, once it is at least an hour. Victor's words: the
// streamer's "most watched snooper".
//
// Who counts is exactly who Top Snoopers counts (watchtime.go): linked
// viewers only, never their own stream, a paused one, a 24/7 channel, a peek
// or the recorder, never Twitch. On top of that, by Victor's answer of
// 2026-10-12, time is counted only while Show who is watching is on, when the
// cards already show everybody who watches whom: with faces off nothing is
// counted, so the figures stand still and two refreshes cannot be compared to
// see who is watching now.
//
// fans_totals is who watched whom, one number per pair, never when. 3.2.0 let
// anybody choose not to be named, which deleted what was counted for them;
// Victor took that away in 3.2.1 ("Don't let users opt out of biggest fan",
// design/3.2.1.md §2). Everybody linked is counted, while the faces are on,
// and a list of opt-outs left by 3.2.0 is deleted once at activation
// (removeFanOptouts). Still no copy of fans_totals is ever kept aside
// (loadLedger): it is who watched whom, and a copy nobody can read is a copy
// nobody can delete.

const (
	kvFansTotals = "fans_totals"

	// Pairs grow with the square of the linked people; two thousand is
	// ample for this server, and a bound somebody chose.
	maxTrackedFanPairs = 2000

	// An hour of watching before anybody is anybody's biggest fan.
	minFanSeconds = 3600
)

// fanView is one streamer's biggest fan, as the snapshot carries it: names as
// they are now, and whole hours, rounded down, so the figure moves at most
// once an hour.
type fanView struct {
	StreamerID  string `json:"streamerId"`
	Streamer    string `json:"streamer"`
	FanID       string `json:"fanId"`
	Fan         string `json:"fan"`
	FanUsername string `json:"fanUsername,omitempty"`
	Hours       int64  `json:"hours"`
}

// ledgerAlert is what /me tells system admins about the biggest-fan and 24/7
// channel ledgers (§2.3): a ledger that would not decode and was started from
// zero, since the plugin last started.
type ledgerAlert struct {
	FansResetAt     int64 `json:"fansResetAt,omitempty"`
	ChannelsResetAt int64 `json:"channelsResetAt,omitempty"`
}

// fanLedgers is this node's copy of fans_totals and chanwatch_totals as the
// last credit pass saved them (or as read once, when this process has none
// yet), the channel hours as shown this hour while faces are off, and when
// either ledger was last started from zero.
type fanLedgers struct {
	lock sync.Mutex

	fans     statTable
	fansHeld bool

	channels     statTable
	channelsHeld bool

	channelsShown     statTable
	channelsShownHour int64

	fansResetAt     int64
	channelsResetAt int64
}

func (p *Plugin) ledgers() *fanLedgers {
	p.ledgersOnce.Do(func() {
		p.ledgersState = &fanLedgers{}
	})

	return p.ledgersState
}

func cloneTable(table statTable) statTable {
	copied := make(statTable, len(table))
	for key, entry := range table {
		copied[key] = entry
	}

	return copied
}

// noteReset records that a ledger was started from zero, for the admin alert.
func (l *fanLedgers) noteReset(key string, at int64) {
	l.lock.Lock()
	defer l.lock.Unlock()

	switch key {
	case kvFansTotals:
		l.fansResetAt = at
		l.fans, l.fansHeld = statTable{}, true
	case kvChanwatchTotals:
		l.channelsResetAt = at
		l.channels, l.channelsHeld = statTable{}, true
		l.channelsShown = nil
	}
}

func (l *fanLedgers) keepFans(table statTable) {
	l.lock.Lock()
	defer l.lock.Unlock()

	l.fans, l.fansHeld = cloneTable(table), true
}

func (l *fanLedgers) fansCopy() (statTable, bool) {
	l.lock.Lock()
	defer l.lock.Unlock()

	if !l.fansHeld {
		return nil, false
	}

	return cloneTable(l.fans), true
}

// fanKey is a pair's key in fans_totals. Mattermost ids are 26 lower-case
// letters and digits, so the colon cannot occur in either.
func fanKey(streamerID, viewerID string) string {
	return streamerID + ":" + viewerID
}

func splitFanKey(key string) (string, string, bool) {
	streamer, viewer, found := strings.Cut(key, ":")

	return streamer, viewer, found && streamer != "" && viewer != ""
}

// dropViewers takes every row of these viewers out of a fans table, and says
// whether it took any: the deletions 3.2.0 still owed when it was replaced
// (removeFanOptouts).
func dropViewers(table statTable, viewers map[string]bool) bool {
	dropped := false

	for key := range table {
		if _, viewer, ok := splitFanKey(key); ok && viewers[viewer] {
			delete(table, key)
			dropped = true
		}
	}

	return dropped
}

// creditFansLocked is the fan ledger's share of a watch pass (§4.3 step 3),
// under statsWriteLock: every pair is credited from the later of the pass's
// start and when they started watching, in sorted key order, and the table is
// saved. A failed read or save loses this pass's fan time, at most one pass,
// and never makes Top Snoopers pay one twice.
func (p *Plugin) creditFansLocked(pairs map[string]map[string]time.Time, from, now time.Time) {
	table, err := p.loadLedger(kvFansTotals)
	if err != nil {
		p.API.LogWarn("Could not read the fan hours; this pass's are lost", "error", err.Error())
		return
	}

	keys := []string{}
	starts := map[string]time.Time{}
	for streamerID, viewers := range pairs {
		for viewerID, since := range viewers {
			key := fanKey(streamerID, viewerID)
			keys = append(keys, key)
			starts[key] = since
		}
	}
	sort.Strings(keys)

	changed, full := false, false
	for _, key := range keys {
		start := from
		if started := ceilSecond(starts[key].UTC()); started.After(start) {
			start = started
		}

		seconds := int64(now.Sub(start) / time.Second)
		if seconds <= 0 {
			continue
		}

		if _, tracked := table[key]; !tracked && len(table) >= maxTrackedFanPairs {
			full = true
			continue
		}

		credit(table, key, "", seconds)
		changed = true
	}

	if full {
		p.API.LogWarn("Not counting some new biggest-fan pairs: the table is full", "limit", maxTrackedFanPairs)
	}

	if changed {
		if err := p.saveStats(kvFansTotals, table); err != nil {
			p.API.LogWarn("Could not save the fan hours; this pass's are lost", "error", err.Error())
			return
		}
	}

	p.ledgers().keepFans(table)
}

// addFans puts each linked streamer's biggest fan on a snapshot (§4.4), from
// the table as the last credit pass saved it, or one read when this process
// has none yet. Rows whose streamer or viewer is not linked now, or either of
// whose accounts is deactivated, are left out. The top of each streamer's
// rows wins with at least an hour; a tie keeps whoever held it in the
// snapshot before, and with nobody holding it, the lower user id.
//
// Nothing at all while biggest fans or Top Snoopers are off. A table that
// cannot be read just now keeps the fans the last snapshot had, as a failed
// poll keeps the boards.
func (p *Plugin) addFans(current *snapshot, cfg *configuration, people *pollPeople) {
	current.Fans, current.FansOn = nil, false

	if !cfg.fansShown() {
		return
	}
	current.FansOn = true

	table, held := p.ledgers().fansCopy()
	if !held {
		loaded, err := p.loadLedgerUnderWriteLock(kvFansTotals, p.ledgers().keepFans)

		if err != nil {
			p.API.LogWarn("Could not read the fan hours; keeping the biggest fans shown", "error", err.Error())
			current.Fans = p.getSnapshot().Fans

			return
		}
		table = loaded
	}

	if people == nil {
		people = p.newPollPeople()
	}

	holders := map[string]string{}
	for _, fan := range p.getSnapshot().Fans {
		holders[fan.StreamerID] = fan.FanID
	}

	active := func(userID string) bool {
		user := people.resolve.person(userID)
		return user != nil && user.DeleteAt == 0 && people.linked(userID)
	}

	streamerSet := map[string]bool{}
	for key := range table {
		if streamer, _, ok := splitFanKey(key); ok {
			streamerSet[streamer] = true
		}
	}

	streamers := make([]string, 0, len(streamerSet))
	for streamer := range streamerSet {
		streamers = append(streamers, streamer)
	}
	sort.Strings(streamers)

	var fans []fanView
	for _, streamer := range streamers {
		if !active(streamer) {
			continue
		}

		// The rows level at the top, of people who still count. Worked out
		// on the whole group, so a top row of somebody who no longer counts
		// gives way to the next.
		winner, seconds := p.topFan(table, streamer, active, holders[streamer])
		if winner == "" || seconds < minFanSeconds {
			continue
		}

		fan := people.resolve.person(winner)
		fans = append(fans, fanView{
			StreamerID:  streamer,
			Streamer:    displayNameOf(people.resolve.person(streamer)),
			FanID:       winner,
			Fan:         displayNameOf(fan),
			FanUsername: fan.Username,
			Hours:       seconds / 3600,
		})
	}

	current.Fans = fans
}

// topFan is one streamer's biggest fan among the viewers who still count, and
// their seconds: the most, then the current holder on a tie, then the lower
// user id.
func (p *Plugin) topFan(table statTable, streamer string, active func(string) bool, holder string) (string, int64) {
	winner := ""
	var most int64

	viewers := []string{}
	for key := range table {
		if who, viewer, ok := splitFanKey(key); ok && who == streamer {
			viewers = append(viewers, viewer)
		}
	}
	sort.Strings(viewers)

	for _, viewer := range viewers {
		seconds := table[fanKey(streamer, viewer)].Seconds
		if seconds <= 0 || viewer == streamer || !active(viewer) {
			continue
		}

		switch {
		case winner == "" || seconds > most:
			winner, most = viewer, seconds
		case seconds == most && viewer == holder:
			winner = viewer
		}
	}

	return winner, most
}

// carryFans keeps the last snapshot's biggest fans, for a poll that could not
// reach Broadcast Box, as the watch boards are kept.
func (s *snapshot) carryFans(previous *snapshot) {
	s.Fans = previous.Fans
	s.FansOn = previous.FansOn
}

// ledgerAlertFor is /me's admin-only ledgerAlert, or nil when there is
// nothing to say.
func (p *Plugin) ledgerAlertFor() *ledgerAlert {
	alert := &ledgerAlert{}

	l := p.ledgers()
	l.lock.Lock()
	alert.FansResetAt = l.fansResetAt
	alert.ChannelsResetAt = l.channelsResetAt
	l.lock.Unlock()

	if *alert == (ledgerAlert{}) {
		return nil
	}

	return alert
}

// kvFanOptouts is where 3.2.0 kept who chose not to be named as a fan, and
// which of them still had hours to be taken out of fans_totals. 3.2.1 has no
// opt-out, so nothing reads or writes it; removeFanOptouts deletes what is
// left of it.
const kvFanOptouts = "fan_optouts"

// leftoverFanOptouts is as much of 3.2.0's fan_optouts as is needed to delete
// it properly: how many people it held, and the deletions still owed.
type leftoverFanOptouts struct {
	Out   map[string]json.RawMessage `json:"out"`
	Purge []string                   `json:"purge"`
}

// removeFanOptouts is activation's, and does something only on the first
// activation after 3.2.0: it deletes the list of people who had chosen not to
// be named as a biggest fan, and the copy of it 3.2.0 would have set aside if
// it had stopped reading, with a line in the log for each. Nobody's id goes
// into the log, only how many there were. They are counted again from now,
// while the faces are on; what was deleted for them stays deleted.
//
// Somebody whose deletion 3.2.0 still owed (its rewrite of fans_totals had
// not landed) has their rows taken out first, because that deletion was
// promised. If that cannot be saved, the list stays for the next activation,
// which tries again; nothing reads it meanwhile.
func (p *Plugin) removeFanOptouts() {
	raw, appErr := p.API.KVGet(kvFanOptouts)
	if appErr != nil {
		p.API.LogWarn("Could not read the old list of biggest-fan opt-outs; trying again at the next activation", "error", appErr.Error())
		return
	}

	if len(raw) > 0 {
		var leftover leftoverFanOptouts
		readable := json.Unmarshal(raw, &leftover) == nil

		if readable && len(leftover.Purge) > 0 && !p.finishOwedFanDeletions(leftover.Purge) {
			return
		}

		if appErr := p.API.KVDelete(kvFanOptouts); appErr != nil {
			p.API.LogWarn("Could not delete the old list of biggest-fan opt-outs; trying again at the next activation", "error", appErr.Error())
			return
		}

		people := 0
		if readable {
			people = len(leftover.Out)
		}
		p.API.LogInfo("Deleted the list of people who had chosen not to be named as a biggest fan: 3.2.1 has no opt-out, so they are counted again from now",
			"key", kvFanOptouts, "people", people, "readable", readable)
	}

	quarantined := kvQuarantinePrefix + kvFanOptouts
	copied, appErr := p.API.KVGet(quarantined)
	if appErr != nil || len(copied) == 0 {
		return
	}
	if appErr := p.API.KVDelete(quarantined); appErr != nil {
		p.API.LogWarn("Could not delete the set-aside copy of the old biggest-fan opt-outs", "key", quarantined, "error", appErr.Error())
		return
	}
	p.API.LogInfo("Deleted the set-aside copy of the old list of biggest-fan opt-outs", "key", quarantined)
}

// finishOwedFanDeletions takes these viewers' rows out of fans_totals, under
// statsWriteLock as every writer of it. True when there was nothing to take
// out or the table was saved without them.
func (p *Plugin) finishOwedFanDeletions(viewers []string) bool {
	p.statsWriteLock.Lock()
	defer p.statsWriteLock.Unlock()

	owed := make(map[string]bool, len(viewers))
	for _, viewer := range viewers {
		owed[viewer] = true
	}

	table, err := p.loadLedger(kvFansTotals)
	if err != nil {
		p.API.LogWarn("Could not read the fan hours to finish the deletions 3.2.0 still owed; keeping the old opt-outs until the next activation", "error", err.Error())
		return false
	}

	if dropViewers(table, owed) {
		if err := p.saveStats(kvFansTotals, table); err != nil {
			p.API.LogWarn("Could not save the fan hours to finish the deletions 3.2.0 still owed; keeping the old opt-outs until the next activation", "error", err.Error())
			return false
		}
	}
	p.ledgers().keepFans(table)

	return true
}
