package main

import (
	"encoding/json"
	"errors"
	"sort"
	"time"
)

// Twitch airtime (design/twitch-live.md §7): time live on Twitch counts toward
// the boards and medals, merged with the same person's Broadcast Box time.
//
// It is kept in a ledger of its own, beside the Broadcast Box tables and in
// the same format, keyed by Twitch user id. Twitch hours are Twitch's data and
// must be deletable when somebody disconnects, and the Broadcast Box tables
// are built never to lose anything: their backup only ever grows and a
// restore keeps the larger figure, so a deleted Twitch row would come back.
// The two sources meet only when the boards are built.
//
// Broadcast Box is authoritative. Its time is credited in full, exactly as
// it always was, and only the Twitch ledger ever gives seconds back. A
// person live on both at once would otherwise be paid twice, so each person
// has a record of the stretches already paid, and by whom (paid spans): the
// Twitch pass pays only what nobody has, and the Broadcast Box pass takes
// back from the Twitch ledger whatever it paid first. A simulcast is counted
// once, as Broadcast Box time, which is in the export and the backup and
// survives a disconnect.

const (
	kvTwitchAirTotals      = "twitchair_totals"
	kvTwitchAirMonthPrefix = "twitchair_month_"
	kvTwitchAirSessions    = "twitchair_sessions"
	kvTwitchAirPaid        = "twitchair_paid"

	// Nothing can be credited further back than maxCreditPerPoll (30 h), so
	// older spans can never matter.
	twitchPaidKeep = 31 * time.Hour

	// Per person. A day of separate sessions, with payers alternating, fits.
	maxPaidSpans = 96
)

func twitchMonthBucket(now time.Time) string {
	return kvTwitchAirMonthPrefix + now.UTC().Format("2006-01")
}

// paidSpan is a stretch of wall-clock time, in Unix seconds, [From, To), that
// has already been credited to a person. Tw is the Twitch id that was paid
// for it, or "" when Broadcast Box paid for it.
type paidSpan struct {
	From int64  `json:"f"`
	To   int64  `json:"t"`
	Tw   string `json:"tw,omitempty"`
}

// paidSpans is twitchair_paid: by Mattermost user id.
type paidSpans map[string][]paidSpan

func sortedSpans(paid []paidSpan) []paidSpan {
	spans := append([]paidSpan(nil), paid...)
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].From != spans[j].From {
			return spans[i].From < spans[j].From
		}

		return spans[i].To < spans[j].To
	})

	return spans
}

// unpaidParts is [from, to) less everything in paid, in order. Pure.
func unpaidParts(from, to int64, paid []paidSpan) []paidSpan {
	if to <= from {
		return nil
	}

	var parts []paidSpan
	cursor := from
	for _, span := range sortedSpans(paid) {
		if span.To <= cursor || span.From >= span.To {
			continue
		}
		if span.From >= to {
			break
		}
		if span.From > cursor {
			parts = append(parts, paidSpan{From: cursor, To: span.From})
		}
		cursor = max(cursor, span.To)
		if cursor >= to {
			return parts
		}
	}

	return append(parts, paidSpan{From: cursor, To: to})
}

// twitchPaidParts is the parts of [from, to) that a Twitch id paid for, in
// order, each carrying its Tw. Pure.
func twitchPaidParts(from, to int64, paid []paidSpan) []paidSpan {
	var parts []paidSpan
	for _, span := range sortedSpans(paid) {
		if span.Tw == "" {
			continue
		}

		start, end := max(from, span.From), min(to, span.To)
		if start < end {
			parts = append(parts, paidSpan{From: start, To: end, Tw: span.Tw})
		}
	}

	return parts
}

// withPaid records [from, to) as paid by tw ("" for Broadcast Box). Whatever
// it overlaps is overwritten (only Broadcast Box ever overwrites: the Twitch
// pass adds only unpaid parts), and spans that touch with the same payer
// merge. Pure.
func withPaid(paid []paidSpan, from, to int64, tw string) []paidSpan {
	if to <= from {
		return append([]paidSpan(nil), paid...)
	}

	kept := make([]paidSpan, 0, len(paid)+2)
	for _, span := range paid {
		if span.To <= from || span.From >= to {
			kept = append(kept, span)
			continue
		}
		if span.From < from {
			kept = append(kept, paidSpan{From: span.From, To: from, Tw: span.Tw})
		}
		if span.To > to {
			kept = append(kept, paidSpan{From: to, To: span.To, Tw: span.Tw})
		}
	}
	kept = sortedSpans(append(kept, paidSpan{From: from, To: to, Tw: tw}))

	merged := make([]paidSpan, 0, len(kept))
	for _, span := range kept {
		if span.From >= span.To {
			continue
		}
		if last := len(merged) - 1; last >= 0 && merged[last].Tw == span.Tw && span.From <= merged[last].To {
			merged[last].To = max(merged[last].To, span.To)
			continue
		}
		merged = append(merged, span)
	}

	return merged
}

// prunePaid drops spans ending before cutoff and, beyond max, the oldest. Pure.
func prunePaid(paid []paidSpan, cutoff int64, limit int) []paidSpan {
	kept := make([]paidSpan, 0, len(paid))
	for _, span := range sortedSpans(paid) {
		if span.To > cutoff {
			kept = append(kept, span)
		}
	}
	if len(kept) > limit {
		kept = kept[len(kept)-limit:]
	}

	return kept
}

// debitSpan takes [from, to) back from key, split across the months it
// covers as creditSpan splits a credit, never below 0 in any table. A month
// that has no row for key is left alone.
func (m *monthTables) debitSpan(key string, from, to time.Time) error {
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
			if entry, has := table[key]; has {
				entry.Seconds = max(entry.Seconds-seconds, 0)
				table[key] = entry
			}
		}

		from = end
	}

	return nil
}

// loadTwitchPaid reads twitchair_paid. A store that does not answer is an
// error. Bytes that will not decode are set aside and read as nothing paid,
// which is what a restart looks like and what the passes already handle, as
// loadSessions does for marks.
func (p *Plugin) loadTwitchPaid() (paidSpans, error) {
	data, appErr := p.API.KVGet(kvTwitchAirPaid)
	if appErr != nil {
		return nil, errors.New(appErr.Error())
	}
	if len(data) == 0 {
		return paidSpans{}, nil
	}

	paid := paidSpans{}
	if err := json.Unmarshal(data, &paid); err != nil {
		p.quarantine(kvTwitchAirPaid, data)
		p.API.LogWarn("Twitch paid spans were unreadable; starting fresh", "error", err.Error())

		return paidSpans{}, nil
	}

	return paid, nil
}

// saveTwitchPaid writes twitchair_paid, pruned to what can still matter.
func (p *Plugin) saveTwitchPaid(paid paidSpans) error {
	data, err := json.Marshal(paid)
	if err != nil {
		return err
	}
	if appErr := p.API.KVSet(kvTwitchAirPaid, data); appErr != nil {
		return errors.New(appErr.Error())
	}

	return nil
}

func prunePaidSpans(paid paidSpans, now time.Time) {
	cutoff := now.Add(-twitchPaidKeep).Unix()
	for userID, spans := range paid {
		if kept := prunePaid(spans, cutoff, maxPaidSpans); len(kept) > 0 {
			paid[userID] = kept
		} else {
			delete(paid, userID)
		}
	}
}

// clear lets recording run again: only Delete all Twitch data does this to the
// Twitch ledger's halt, because it removes whatever was damaged.
func (h *recordingHalt) clear() {
	h.lock.Lock()
	defer h.lock.Unlock()

	h.halted = false
	h.reason = ""
}

// twitchHaltOrSkip is haltOrSkip for the Twitch ledger, with a stop switch of
// its own: a damaged Twitch table never stops Broadcast Box hours being
// recorded, and the reverse holds too.
func (p *Plugin) twitchHaltOrSkip(err error) {
	if errors.Is(err, errStatsQuarantined) {
		if p.twitchAirHalt.stop(err.Error()) {
			p.API.LogError(
				"Recording of Twitch hours has stopped so the stored history is not overwritten",
				"reason", err.Error(),
				"recover", "the unreadable document was copied to a "+kvQuarantinePrefix+" key in this plugin's KV store",
			)
		}

		return
	}

	p.API.LogWarn("Could not read the stored Twitch hours; skipping this pass", "error", err.Error())
}

// armTwitchResync records that Twitch sessions running right now should not
// be handed backdated airtime: Twitch has just been switched on.
func (p *Plugin) armTwitchResync(at int64) {
	p.statsLock.Lock()
	p.twitchResyncAt = at
	p.statsLock.Unlock()
}

// twitchResyncEpoch is the later of Twitch's own epoch and airtime's (the
// leaderboard switched back on), expiring as resyncEpoch does.
func (p *Plugin) twitchResyncEpoch(nowMS int64) int64 {
	stats := p.resyncEpoch(nowMS)

	p.statsLock.Lock()
	defer p.statsLock.Unlock()

	own := p.twitchResyncAt
	if own != 0 && nowMS-own > sessionRetentionMS {
		p.twitchResyncAt = 0
		own = 0
	}

	return max(stats, own)
}

// secondsFrom is how much of a stream that started at start came before the
// person connected at connectedMS: time that never counts. Rounded up, so not
// even part of a second before connecting is paid.
func secondsFrom(start time.Time, connectedMS int64) int64 {
	before := connectedMS - start.UnixMilli()
	if before <= 0 {
		return 0
	}

	return (before + 999) / 1000
}

// recordTwitchTime credits the streams one good read of Get Streams listed.
// Called by the Twitch loop after that read, never by the poller, so that
// neither source's evidence is advanced by the other's pass. It mirrors
// recordStreamTime, in the same order and with the same refusals.
func (p *Plugin) recordTwitchTime(listed []twitchLive, cfg *configuration, now time.Time) {
	if !cfg.EnableLeaderboard || !cfg.twitchLiveOn() {
		return
	}
	if halted, _ := p.twitchAirHalt.state(); halted {
		return
	}

	p.statsWriteLock.Lock()
	defer p.statsWriteLock.Unlock()

	s := p.twitchLive()

	totals, err := p.loadStats(kvTwitchAirTotals)
	if err != nil {
		p.twitchHaltOrSkip(err)
		return
	}

	marks, err := p.loadSessionMarks(kvTwitchAirSessions)
	if err != nil {
		p.API.LogWarn("Could not read the Twitch sessions; skipping this pass", "error", err.Error())
		return
	}

	nowMS := now.UnixMilli()

	// Twitch's own evidence of having been watching: the marks across a
	// restart, and the last pass in this process. Broadcast Box's lastPollAt
	// is not read or moved here.
	s.lock.Lock()
	observedAt := max(lastObserved(marks), s.creditAt)
	s.creditAt = nowMS
	links := s.links
	s.lock.Unlock()

	paid, err := p.loadTwitchPaid()
	if err != nil {
		p.API.LogWarn("Could not read the Twitch paid spans; skipping this pass", "error", err.Error())
		return
	}

	resyncAt := p.twitchResyncEpoch(nowMS)
	months := newMonthTables(p, kvTwitchAirMonthPrefix)
	present := map[string]bool{}
	credited, paidChanged, marksDirty := false, false, false
	titles := map[string]string{}

	for _, stream := range listed {
		key := stream.TwitchID
		if key == "" || links == nil {
			continue
		}
		owner := links.owner(key)
		if owner == "" {
			continue
		}
		link := links.Users[owner]

		start, parseErr := time.Parse(time.RFC3339Nano, stream.StartedAt)
		if parseErr != nil {
			continue
		}
		elapsed := int64(now.Sub(start).Seconds())
		if elapsed <= 0 {
			continue
		}

		present[key] = true

		mark, known := marks[key]
		if !known || mark.Start != stream.StartedAt {
			// Time from before the person connected never counts, even
			// after a restart, when claimable alone would allow the whole
			// session.
			mark = sessionMark{Start: stream.StartedAt, Credited: max(claimable(elapsed, nowMS, observedAt), min(secondsFrom(start, link.At), elapsed))}
			marksDirty = true
		}

		if nowMS-mark.LastSeen > lastSeenRefreshMS {
			marksDirty = true
		}
		mark.LastSeen = nowMS

		if resyncAt != 0 && mark.Synced != resyncAt {
			mark.Credited = elapsed
			mark.Synced = resyncAt
			marks[key] = mark
			marksDirty = true

			continue
		}

		// A channel an admin marked 24/7 earns nothing, but its mark is kept
		// current, as a paused stream's is, so clearing the flag never pays
		// back the hours it was 24/7.
		if link.AllDay {
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
			p.API.LogWarn("Ignoring an implausible Twitch stream duration", "seconds", delta)
			mark.Credited = elapsed
			marks[key] = mark
			marksDirty = true

			continue
		}

		if len(totals) >= maxTrackedStreamers {
			if _, tracked := totals[key]; !tracked {
				marks[key] = mark
				p.API.LogWarn("Not counting a new Twitch channel: the Twitch hours table is full", "limit", maxTrackedStreamers)

				continue
			}
		}

		title, named := titles[owner]
		if !named {
			if user, appErr := p.API.GetUser(owner); appErr == nil && user != nil {
				title = displayNameOf(user)
			}
			titles[owner] = title
		}

		// Only what nobody has paid for yet: Broadcast Box may have paid
		// some of it already, and a backlog may reach over what an earlier
		// pass of this one paid.
		base := start.Unix()
		for _, part := range unpaidParts(base+mark.Credited, base+elapsed, paid[owner]) {
			if err := months.creditSpan(key, title, time.Unix(part.From, 0), time.Unix(part.To, 0)); err != nil {
				p.twitchHaltOrSkip(err)
				return
			}
			credit(totals, key, title, part.To-part.From)
			paid[owner] = withPaid(paid[owner], part.From, part.To, key)
			credited, paidChanged = true, true
		}

		mark.Credited = elapsed
		marks[key] = mark
		marksDirty = true
	}

	for key, mark := range marks {
		if present[key] {
			continue
		}
		if mark.LastSeen == 0 {
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

	// Totals, the months, the paid spans, and the marks last, which is the
	// same "replay rather than lose" rule recordStreamTime keeps.
	if credited {
		if err := p.saveStats(kvTwitchAirTotals, totals); err != nil {
			p.API.LogWarn("Could not save the Twitch hours; they will be counted again on the next read", "error", err.Error())
			return
		}

		saveErr := months.save()
		if months.reachesBeforeMonth(now) {
			p.forgetMonthPodiums()
		}
		if saveErr != nil {
			p.API.LogWarn("Could not save this month's Twitch hours; they will be counted again on the next read", "error", saveErr.Error())
			return
		}
	}

	if paidChanged {
		prunePaidSpans(paid, now)
		if err := p.saveTwitchPaid(paid); err != nil {
			p.API.LogWarn("Could not save the Twitch paid spans; the next read will count again", "error", err.Error())
			return
		}
	}

	if marksDirty {
		if err := p.saveSessionMarks(kvTwitchAirSessions, marks); err != nil {
			p.API.LogWarn("Could not save the Twitch sessions", "error", err.Error())
		}
	}
}

// twitchPayback is the Broadcast Box pass's side of the paid spans: for the
// people with a Twitch connection, what it pays is recorded as paid by
// Broadcast Box, and whatever the Twitch ledger already holds of the same
// seconds is taken back from it. Everything is read lazily but before
// anything is written, so a store that fails still skips the whole pass.
//
// nil while the switch is off, while nobody is connected, or while the link
// document will not decode, and then the Broadcast Box pass does exactly what
// it did before Twitch, and reads no new key.
type twitchPayback struct {
	plugin *Plugin
	people map[string]bool
	halted bool

	paid      paidSpans
	paidDirty bool

	totals      statTable
	totalsDirty bool
	months      *monthTables
}

func (p *Plugin) newTwitchPayback(cfg *configuration) (*twitchPayback, error) {
	people, err := p.twitchLive().peopleIfOn(cfg)
	if errors.Is(err, errTwitchLinksUnreadable) {
		// A document that will not decode stays that way until an admin
		// deletes all Twitch data, and the Twitch loop credits nobody
		// meanwhile, so there is no Twitch second to pay twice. Broadcast
		// Box is paid in full, as with the switch off: skipping would stop
		// everybody's airtime, Twitch or not, for as long as it lasts.
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(people) == 0 {
		return nil, nil
	}

	halted, _ := p.twitchAirHalt.state()

	return &twitchPayback{plugin: p, people: people, halted: halted}, nil
}

// boardOwner is whose a Broadcast Box stream is, for the boards and the paid
// spans alike: the person who linked its key, and nobody for a stream matched
// only by username, which the boards keep as a row of its own.
func boardOwner(stream apiStream) string {
	if stream.LinkSource != linkSourceManual {
		return ""
	}

	return stream.UserID
}

// covers is whether the pass has anything to do for a stream's owner.
func (b *twitchPayback) covers(owner string) bool {
	return b != nil && owner != "" && b.people[owner]
}

// settle is told that Broadcast Box has just paid owner for [from, to).
func (b *twitchPayback) settle(owner string, from, to int64) error {
	p := b.plugin

	if b.paid == nil {
		paid, err := p.loadTwitchPaid()
		if err != nil {
			return err
		}
		b.paid = paid
	}

	// Seconds the Twitch ledger was paid for first, by a read that landed
	// before this poll or a backlog after a restart or an outage. Only those
	// of the Twitch id the person has connected now: a part of another id is
	// that id's to lose in its purge. Nothing is taken back while the Twitch
	// ledger is halted.
	parts := twitchPaidParts(from, to, b.paid[owner])
	if len(parts) > 0 && !b.halted {
		id, free := p.twitchLive().connectedID(owner)

		for _, part := range parts {
			if part.Tw != id || !free {
				continue
			}

			if b.totals == nil {
				totals, err := p.loadStats(kvTwitchAirTotals)
				if errors.Is(err, errStatsQuarantined) {
					p.twitchHaltOrSkip(err)
					b.halted = true

					break
				}
				if err != nil {
					return err
				}
				b.totals = totals
				b.months = newMonthTables(p, kvTwitchAirMonthPrefix)
			}

			if err := b.months.debitSpan(part.Tw, time.Unix(part.From, 0), time.Unix(part.To, 0)); err != nil {
				if errors.Is(err, errStatsQuarantined) {
					p.twitchHaltOrSkip(err)
					b.halted = true
					b.totals, b.months, b.totalsDirty = nil, nil, false

					break
				}

				return err
			}

			if entry, has := b.totals[part.Tw]; has {
				entry.Seconds = max(entry.Seconds-(part.To-part.From), 0)
				b.totals[part.Tw] = entry
			}
			b.totalsDirty = true
		}
	}

	b.paid[owner] = withPaid(b.paid[owner], from, to, "")
	b.paidDirty = true

	return nil
}

// save writes what settle changed, after the Broadcast Box tables and before
// the Broadcast Box marks: the Twitch totals, the Twitch months, then the
// paid spans. A failure stops the later writes.
func (b *twitchPayback) save(now time.Time) error {
	if b == nil {
		return nil
	}
	p := b.plugin

	if b.totalsDirty && b.totals != nil {
		if err := p.saveStats(kvTwitchAirTotals, b.totals); err != nil {
			return err
		}

		saveErr := b.months.save()
		if b.months.reachesBeforeMonth(now) {
			p.forgetMonthPodiums()
		}
		if saveErr != nil {
			return saveErr
		}
	}

	if b.paidDirty {
		prunePaidSpans(b.paid, now)
		if err := p.saveTwitchPaid(b.paid); err != nil {
			return err
		}
	}

	return nil
}
