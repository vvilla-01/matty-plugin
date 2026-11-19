package main

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"
)

// Every month that has ended leaves a podium behind: whoever the monthly board
// had in its top three when the month ran out. Those places are kept for good,
// as a tally in each person's profile, because the monthly board starts again
// from nothing on the 1st and a champion would otherwise stop being one the
// moment their month was over.
//
// Nothing new is stored to make this work. Every monthly table is still in the
// KV store, and a podium is just the board of one of them cut to podiumSize,
// so the tally is exactly what that month's board showed on its last day, ties
// and all. The month in progress is left out: its places are not settled yet,
// and the profile already shows them on their own.
//
// What costs something is finding them. That means reading every month there
// could be a table for, one KV read each since January 2020, which is not
// something to do every few seconds for an answer that changes a handful of
// times a year. So the podiums are read once and kept in memory, and read
// again only when one of them could have changed:
//
//   - the month rolls over, and the one that has just ended starts to count;
//   - a poll credits time to a month that has already ended, which is the tail
//     of a broadcast running across midnight on the 1st or a stretch recovered
//     after an outage (see recordStreamTime);
//   - an admin imports airtime, which can rewrite any month (see applyTable).
//
// Those are all the writers of a monthly table. restoreBackup only touches the
// totals and migrateStats only the schema marker; anything that writes the
// months in future has to call forgetMonthPodiums the same way.
//
// What is kept is each ended month's whole tables, Broadcast Box's and, while
// Twitch live is on, the Twitch ledger's, not its podium: a board is by person
// (boardRows), and two sources can only be merged into one person's row
// before the top three is cut. Who a stream key or a Twitch id belongs to is
// decided on every poll, by the same resolver the boards use, so somebody who
// links their stream today is credited with the months they won before they
// did — and the tally never disagrees with the boards about who anyone is.
//
// The copy is per process, like every other cache here. A Mattermost HA
// cluster would need the forget to reach every node; see saveStats for why
// this plugin is written for a single server.

// monthMedals is one person's places on the boards of months that have ended,
// each list as "YYYY-MM", earliest first. It names the person the way a
// leaderEntry does, so the trophy menu can list everybody's tally without a
// lookup of its own, and never carries a stream key.
type monthMedals struct {
	UserID      string   `json:"userId"`
	Username    string   `json:"username,omitempty"`
	DisplayName string   `json:"displayName,omitempty"`
	Gold        []string `json:"gold,omitempty"`
	Silver      []string `json:"silver,omitempty"`
	Bronze      []string `json:"bronze,omitempty"`
}

// endedMonth is one ended month's tables, as read. It never leaves the
// server. tw is nil when Twitch was not read.
type endedMonth struct {
	month string
	bb    statTable
	tw    statTable
}

// monthPodiumRetry is how long the months are left alone after one of them
// could not be read. The poller runs every few seconds, and a database that is
// already struggling does not need a read of every month there has ever been
// added to every one of those.
const monthPodiumRetry = 5 * time.Minute

// firstPossibleMonth is the earliest month monthSuffix accepts, and so the
// earliest that can have a podium. An import refuses a table dated before it,
// and the recorder only writes the month the server's clock is in, so an
// earlier one would take a clock set years wrong, which is no month anybody
// really won. Every reading starts here.
var firstPossibleMonth = time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC)

// podiumCache is the tables of every month that has ended, as last read.
type podiumCache struct {
	lock sync.Mutex

	// read is whether months holds a complete reading at all. An empty one is
	// a real answer — nobody has finished a month yet — and is kept as such.
	read   bool
	months []endedMonth

	// withTwitch is whether that reading took in the Twitch ledger's months.
	// One that did not is stale once Twitch live is switched on.
	withTwitch bool

	// month is the one that was in progress when they were read, as
	// "YYYY-MM". Once it is not the current month any more it has ended, and
	// its podium is missing.
	month string

	// generation moves on with every forget, and readAt is the generation the
	// podiums were read under. A forget that lands while a read is in flight
	// — an import during a poll — then leaves them stale for the next poll,
	// instead of being lost to a read that may have begun before the import.
	generation uint64
	readAt     uint64

	// retryAt holds off another read after one failed.
	retryAt time.Time
}

// forgetMonthPodiums marks the copy stale, so the ended months are read again
// on the next poll — or, straight after a read that failed, once its wait is
// over. Cheap, and safe to call from any writer, under any lock it holds.
func (p *Plugin) forgetMonthPodiums() {
	p.endedPodiums.lock.Lock()
	p.endedPodiums.generation++
	p.endedPodiums.lock.Unlock()
}

// monthMedalTally is what the snapshot carries: everybody with at least one
// place on the board of a month that has ended, in user id order. resolve is
// the resolver the boards were built with in this poll, and owners the Twitch
// connections they were built with; with twitchOn false the Twitch ledger is
// left out, as it is from the boards.
//
// Stable from poll to poll, because an unchanged snapshot must publish
// nothing: the months come out in order, and the people are sorted rather
// than left in the order a map happens to give them.
func (p *Plugin) monthMedalTally(now time.Time, resolve *identityResolver, owners map[string]string, twitchOn bool) []monthMedals {
	months := p.endedMonths(now, twitchOn)
	if len(months) == 0 {
		return nil
	}
	if resolve == nil {
		resolve = newIdentityResolver(p)
	}
	cfg := p.getConfiguration()

	byUser := map[string]*monthMedals{}
	for _, ended := range months {
		var tw statTable
		if twitchOn {
			tw = ended.tw
		}

		rows := p.boardRows(ended.bb, tw, cfg, resolve, owners)
		if len(rows) > podiumSize {
			rows = rows[:podiumSize]
		}

		for place, row := range rows {
			// A stream nobody has linked has no profile to put a medal in.
			// It is not forgotten: the month's table is kept whole, so the
			// medal appears on the first poll after its owner links it.
			user := row.user
			if user == nil || user.Id == "" {
				continue
			}

			// Named exactly as leaderboard names a row, from the same
			// resolver, so the menu and the boards above it cannot call one
			// person two things. The resolver is made afresh every poll, so a
			// rename shows on the next one.
			medals := byUser[user.Id]
			if medals == nil {
				medals = &monthMedals{UserID: user.Id, Username: user.Username, DisplayName: displayNameOf(user)}
				byUser[user.Id] = medals
			}

			switch place {
			case 0:
				medals.Gold = append(medals.Gold, ended.month)
			case 1:
				medals.Silver = append(medals.Silver, ended.month)
			case 2:
				medals.Bronze = append(medals.Bronze, ended.month)
			}
		}
	}

	tally := make([]monthMedals, 0, len(byUser))
	for _, medals := range byUser {
		// The months arrive earliest first, which keeps every list in order;
		// a person is one row on each month's board, and Compact is what
		// guarantees no month is ever counted twice in one list all the same,
		// rather than leaving that to how the months happened to be read.
		medals.Gold = slices.Compact(medals.Gold)
		medals.Silver = slices.Compact(medals.Silver)
		medals.Bronze = slices.Compact(medals.Bronze)

		tally = append(tally, *medals)
	}

	sort.Slice(tally, func(i, j int) bool {
		return tally[i].UserID < tally[j].UserID
	})

	return tally
}

// endedMonths returns the tables of every month before the one now falls in,
// earliest first, reading them only when the copy in memory is missing or
// could be out of date: the month has rolled over, a writer forgot them, or
// Twitch live has been switched on since a reading that left Twitch out.
//
// A read that fails part way through changes nothing: the last complete
// reading stays, or none at all, and nothing is tried again for a few minutes.
// Caching the gap would tell every profile that months somebody won never
// happened, and it would stay that way until the next rollover.
func (p *Plugin) endedMonths(now time.Time, twitchOn bool) []endedMonth {
	current := now.UTC().Format("2006-01")
	cache := &p.endedPodiums

	cache.lock.Lock()
	upToDate := cache.read && cache.month == current && cache.readAt == cache.generation && (cache.withTwitch || !twitchOn)
	waiting := now.Before(cache.retryAt)
	months, generation := cache.months, cache.generation
	cache.lock.Unlock()

	if upToDate || waiting {
		return months
	}

	// Read with no lock held, so an import that forgets the podiums is never
	// kept waiting on a read of every month there has been.
	fresh, err := p.readEndedMonths(current, twitchOn)

	cache.lock.Lock()
	if err != nil {
		cache.retryAt = now.Add(monthPodiumRetry)
		months = cache.months
	} else {
		cache.read = true
		cache.months = fresh
		cache.withTwitch = twitchOn
		cache.month = current
		cache.readAt = generation
		cache.retryAt = time.Time{}
		months = fresh
	}
	cache.lock.Unlock()

	if err != nil {
		// Once per attempt, and attempts are minutes apart, so this is not a
		// line in the log every poll.
		p.API.LogWarn("Could not read past months for the monthly medals; keeping what was known and trying again later",
			"retry_in", monthPodiumRetry.String(), "error", err.Error())
	}

	return months
}

// readEndedMonths reads the tables of every month that ended before current,
// and with twitch the Twitch ledger's months beside them.
//
// The months are read by name, every one from firstPossibleMonth on, rather
// than found in a listing of the KV store. A listing is paged by offset, so a
// key that sorts ahead of the months and is deleted between two pages — a
// link removed by somebody unlinking, say — moves every later key back one
// place, and the key on the boundary is never returned, with no error to say
// so. kvKeysWithPrefix's callers ask again on the next poll; this answer is
// kept until the month rolls over, so a month missed here would be missing
// from people's profiles for weeks. A read by name cannot miss, and a few
// dozen of them, most for months with no table, cost less than paging through
// the whole namespace.
//
// The two ways a month can fail to read are treated differently, as they are
// everywhere else. A month whose bytes will not decode has been quarantined by
// loadStats and will not decode next time either, so it is skipped and counts
// for nothing — retrying it every few minutes would change nothing. A month
// that could not be READ is a database that was briefly unavailable, and fails
// the whole reading so that none of it is remembered.
func (p *Plugin) readEndedMonths(current string, twitch bool) ([]endedMonth, error) {
	read := func(key, month string) (statTable, error) {
		table, err := p.loadStats(key)
		if err == nil {
			return table, nil
		}
		if !errors.Is(err, errStatsQuarantined) {
			return nil, fmt.Errorf("%s: %w", month, err)
		}

		p.API.LogWarn("A past month's airtime is unreadable, so it counts for no monthly medals",
			"month", month, "error", err.Error())

		return nil, nil
	}

	// Earliest first, so every person's months come out in order.
	var months []endedMonth
	for at := firstPossibleMonth; ; at = at.AddDate(0, 1, 0) {
		month := at.Format("2006-01")

		// Only months that are over, and only real ones, the same rule an
		// import is held to. "YYYY-MM" sorts by date, and monthSuffix runs out
		// at the end of 2099.
		if month >= current || !monthSuffix.MatchString(month) {
			break
		}

		// A month with no table reads as an empty one, which has no podium.
		bb, err := read(kvStatsMonthPrefix+month, month)
		if err != nil {
			return nil, err
		}

		var tw statTable
		if twitch {
			if tw, err = read(kvTwitchAirMonthPrefix+month, month); err != nil {
				return nil, err
			}
		}

		if !hasSeconds(bb) && !hasSeconds(tw) {
			continue
		}

		months = append(months, endedMonth{month: month, bb: bb, tw: tw})
	}

	return months, nil
}

func hasSeconds(table statTable) bool {
	for _, entry := range table {
		if entry.Seconds > 0 {
			return true
		}
	}

	return false
}
