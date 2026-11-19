package main

import (
	"sort"
	"strings"
	"time"
)

// Hours on the 24/7 channels that have an achievement (design/3.2.0.md §5.5).
//
// 24/7 channels earn nobody airtime and count for no Snooper, because anybody
// could leave one open overnight. An admin may still give one an achievement,
// "Lo-fi radio: 10 hours", in ChannelAchievements, and only for those channels
// does the watch pass keep how long each linked viewer has watched,
// chanwatch_totals, keyed "<normalisedKey>:<viewerUserId>". The key never
// leaves the server: the achievement is known to browsers only by an id made
// from it (achievements.go).
//
// Counted in the same pass and by the same clock as the board, for the same
// people: linked, never let in. With faces off the panel records sessions for
// linked people whenever Top Snoopers is on, so channel watching is seen
// exactly when Top Snoopers is on, and only then. No daily cap (Victor's
// answer to Q11): a tile left open counts, and buys a badge, never a place on
// a board. It starts at zero on the day 3.2.0 is installed, since nobody's
// 24/7 watching was ever recorded before.
//
// Read, saved and started from zero by the fan ledger's rules (loadLedger):
// two thousand rows, a failed save losing only that pass, and bytes that will
// not decode deleted, logged, and told to admins.

const (
	kvChanwatchTotals = "chanwatch_totals"

	maxTrackedChannelRows = 2000
)

// chanwatchKey is a row's key: the normalised stream key, then the viewer.
func chanwatchKey(streamKey, viewerID string) string {
	return streamKey + ":" + viewerID
}

// splitChanwatchKey takes a row's key apart from the right: a Mattermost
// user id never holds a colon, so everything after the last one is the
// viewer, and a stream key with a colon in it still parses.
func splitChanwatchKey(key string) (string, string, bool) {
	at := strings.LastIndex(key, ":")
	if at <= 0 || at == len(key)-1 {
		return "", "", false
	}

	return key[:at], key[at+1:], true
}

// countedChannels is the normalised keys of the 24/7 channels whose watching
// the pass counts: those with a good line in 24/7 channel achievements, while
// achievements and Top Snoopers are both on. Nothing otherwise, so that with
// either off nothing is recorded about watching a channel.
func (p *Plugin) countedChannels(cfg *configuration) map[string]bool {
	keys := map[string]bool{}
	if !cfg.EnableAchievements || !cfg.TopWatchers {
		return keys
	}

	families, _ := p.channelAchievements(cfg)
	for _, family := range families {
		keys[family.key] = true
	}

	return keys
}

// creditChannelsLocked is the channel ledger's share of a watch pass (§4.3
// step 4), under statsWriteLock, by the fan ledger's rules.
func (p *Plugin) creditChannelsLocked(channels map[string]map[string]time.Time, from, now time.Time) {
	table, err := p.loadLedger(kvChanwatchTotals)
	if err != nil {
		p.API.LogWarn("Could not read the 24/7 channel hours; this pass's are lost", "error", err.Error())
		return
	}

	keys := []string{}
	starts := map[string]time.Time{}
	for streamKey, viewers := range channels {
		for viewerID, since := range viewers {
			key := chanwatchKey(streamKey, viewerID)
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

		if _, tracked := table[key]; !tracked && len(table) >= maxTrackedChannelRows {
			full = true
			continue
		}

		credit(table, key, "", seconds)
		changed = true
	}

	if full {
		p.API.LogWarn("Not counting some new 24/7 channel viewers: the table is full", "limit", maxTrackedChannelRows)
	}

	if changed {
		if err := p.saveStats(kvChanwatchTotals, table); err != nil {
			p.API.LogWarn("Could not save the 24/7 channel hours; this pass's are lost", "error", err.Error())
			return
		}
	}

	l := p.ledgers()
	l.lock.Lock()
	l.channels, l.channelsHeld = cloneTable(table), true
	l.lock.Unlock()
}

// channelHoursShown is chanwatch_totals as achievements are worked out from
// it: with faces on, as the last credit pass saved it (or read once, when
// this process has none yet); with faces off, as it stood at the first poll
// of the UTC hour, so the figures move once an hour, as the Snoopers board's
// do. False when it could not be read.
func (p *Plugin) channelHoursShown(cfg *configuration, now time.Time) (statTable, bool) {
	hour := now.UTC().Truncate(time.Hour).Unix()
	l := p.ledgers()

	l.lock.Lock()
	if !cfg.ShowWatchers && l.channelsShown != nil && l.channelsShownHour == hour {
		shown := l.channelsShown
		l.lock.Unlock()

		return shown, true
	}
	table, held := l.channels, l.channelsHeld
	l.lock.Unlock()

	if !held {
		loaded, err := p.loadLedgerUnderWriteLock(kvChanwatchTotals, nil)

		if err != nil {
			p.API.LogWarn("Could not read the 24/7 channel hours", "error", err.Error())
			return nil, false
		}
		table = loaded

		l.lock.Lock()
		if !l.channelsHeld {
			l.channels, l.channelsHeld = cloneTable(loaded), true
		}
		l.lock.Unlock()
	}

	shown := cloneTable(table)

	l.lock.Lock()
	l.channelsShown, l.channelsShownHour = shown, hour
	l.lock.Unlock()

	return shown, true
}
