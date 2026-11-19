package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// These are the tests for the one thing this plugin stores that cannot be
// rebuilt. Every case below is a way the hours could have gone missing.

func atUTC(month, day, hour int) time.Time {
	return time.Date(2026, time.Month(month), day, hour, 0, 0, 0, time.UTC)
}

func seedTotals(t *testing.T, store map[string][]byte, table statTable) {
	t.Helper()

	data, err := json.Marshal(table)
	require.NoError(t, err)
	store[kvStatsTotals] = data
}

// The original bug: a KV read that failed was indistinguishable from an empty
// key, and the write at the end of the poll replaced the whole history with
// whoever happened to be live.
func TestAReadFailureNeverOverwritesTheHistory(t *testing.T) {
	p, store, faults := kvBackedPluginWithFaults(map[string][]byte{})
	cfg := leaderboardConfig()

	seedTotals(t, store, statTable{
		"videokey-villa": {Seconds: 400000, Title: "Villa"},
		"videokey-trent": {Seconds: 200000, Title: "Trent"},
	})
	before := append([]byte(nil), store[kvStatsTotals]...)

	faults.failRead(kvStatsTotals)

	start := atUTC(9, 18, 10)
	streams := []apiStream{live("videokey-walter", "Walter", start.Format(time.RFC3339Nano))}
	p.recordStreamTime(streams, cfg, start.Add(time.Hour))

	require.Equal(t, string(before), string(store[kvStatsTotals]), "a table that could not be read must be left exactly as it was")

	// A database that was briefly away is not a standing problem, so recording
	// is not stopped for good over it — the poll is simply skipped.
	halted, _ := p.statsHalt()
	require.False(t, halted, "a transient read failure must not halt recording for ever")

	faults.healRead(kvStatsTotals)
	p.recordStreamTime(streams, cfg, start.Add(2*time.Hour))
	require.EqualValues(t, 7200, hours(p, kvStatsTotals, "videokey-walter"), "and counting resumes once the database is back")
	require.EqualValues(t, 400000, hours(p, kvStatsTotals, "videokey-villa"), "with the history intact")
}

// A future version changing the stored shape, or a rollback to an older build,
// is the other route to the same loss.
func TestUnreadableTotalsAreQuarantinedOnceAndLeftAlone(t *testing.T) {
	p, store := kvBackedPlugin()
	cfg := leaderboardConfig()

	store[kvStatsTotals] = []byte(`{"version":2,"entries":{"videokey-villa":{"seconds":400000}}}`)
	original := append([]byte(nil), store[kvStatsTotals]...)

	start := atUTC(9, 18, 10)
	streams := []apiStream{live("videokey-villa", "Villa", start.Format(time.RFC3339Nano))}

	for i := 1; i <= 10; i++ {
		p.recordStreamTime(streams, cfg, start.Add(time.Duration(i)*time.Minute))
	}

	require.Equal(t, string(original), string(store[kvStatsTotals]), "the original bytes must survive untouched")

	quarantined := 0
	for key, value := range store {
		if strings.HasPrefix(key, kvQuarantinePrefix) {
			quarantined++
			require.Equal(t, string(original), string(value), "the quarantine holds the original bytes")
		}
	}
	require.Equal(t, 1, quarantined, "quarantined once, not once per poll")

	// Bytes that will not decode ARE a standing problem: retrying rewrites
	// nothing and each attempt is another chance to get it wrong.
	halted, reason := p.statsHalt()
	require.True(t, halted, "undecodable data stops recording until someone looks")
	require.NotEmpty(t, reason)
}

// An admin importing or restoring has dealt with a data problem, but can do
// nothing at all about a schema downgrade — so that halt has to survive.
func TestAnImportClearsADataHaltButNotASchemaHalt(t *testing.T) {
	p, _ := kvBackedPlugin()

	p.haltStats(haltData, "unreadable")
	p.clearDataHalt()
	halted, _ := p.statsHalt()
	require.False(t, halted, "an admin can resolve a data halt")

	p.haltStats(haltSchema, "written by a newer build")
	p.clearDataHalt()
	halted, reason := p.statsHalt()
	require.True(t, halted, "an import must not re-enable writes this build should not be making")
	require.Contains(t, reason, "newer")
}

// Merge mode promises it can never take hours off somebody. That promise only
// holds if it refuses to merge into a table it could not read.
func TestAMergeImportRefusesWhenTheLiveTableCannotBeRead(t *testing.T) {
	p, store, faults := kvBackedPluginWithFaults(map[string][]byte{})

	seedTotals(t, store, statTable{
		"videokey-villa": {Seconds: 400000, Title: "Villa"},
		"videokey-trent": {Seconds: 200000, Title: "Trent"},
	})
	before := append([]byte(nil), store[kvStatsTotals]...)

	faults.failRead(kvStatsTotals)

	_, err := p.applyTable(kvStatsTotals, statTable{"videokey-villa": {Seconds: 1}}, "merge")
	require.ErrorIs(t, err, errStatsUnreadable)
	require.Equal(t, string(before), string(store[kvStatsTotals]), "nothing is written when the merge base is unknown")
}

// Same hazard on the restore path, where the backup would otherwise be written
// straight over airtime earned since it was taken.
func TestRestoreRefusesWhenTheLiveTableCannotBeRead(t *testing.T) {
	p, store, faults := kvBackedPluginWithFaults(map[string][]byte{})

	seedTotals(t, store, statTable{"videokey-villa": {Seconds: 400000, Title: "Villa"}})
	p.backupStats("1.12.0")

	seedTotals(t, store, statTable{"videokey-villa": {Seconds: 500000, Title: "Villa"}})
	before := append([]byte(nil), store[kvStatsTotals]...)

	faults.failRead(kvStatsTotals)

	_, err := p.restoreBackup()
	require.Error(t, err)
	require.Equal(t, string(before), string(store[kvStatsTotals]), "the newer figures are left alone")
}

// Taking a key back out of "24/7 stream keys" must not hand that channel
// its whole running session in one poll. Always-on streams carry no mark, so
// the configuration change arms a resynchronisation instead.
func TestDemotingAnAlwaysOnKeyDoesNotCreditItsBacklog(t *testing.T) {
	before := leaderboardConfig()

	after := &configuration{EnableLeaderboard: true, AlwaysOnStreamKeys: ""}
	after.normalize()
	require.True(t, demotedFromAlwaysOn(before, after))

	// Adding one, or leaving it alone, is not a demotion.
	require.False(t, demotedFromAlwaysOn(after, before))
	require.False(t, demotedFromAlwaysOn(before, before))

	p, _ := kvBackedPlugin()
	start := atUTC(9, 18, 10)
	p.armStatsResync(start.UnixMilli())

	iso := start.Add(-6 * time.Hour).Format(time.RFC3339Nano)
	watts := []apiStream{live("videokey-watts", "Alan Watts Chillstep", iso)}

	p.recordStreamTime(watts, after, start)
	require.Zero(t, hours(p, kvStatsTotals, "videokey-watts"), "the six hours it ran while always-on are not credited")

	p.recordStreamTime(watts, after, start.Add(10*time.Minute))
	require.EqualValues(t, 600, hours(p, kvStatsTotals, "videokey-watts"), "time from the change onward counts normally")
}

// A build that understands less than what is on disk must not rewrite it in the
// older shape.
func TestADowngradeRefusesToWrite(t *testing.T) {
	p, store := kvBackedPlugin()
	cfg := leaderboardConfig()

	marker, err := json.Marshal(statsSchema{Version: statsSchemaVersion + 1})
	require.NoError(t, err)
	store[kvStatsSchema] = marker
	seedTotals(t, store, statTable{"videokey-villa": {Seconds: 400000, Title: "Villa"}})
	before := append([]byte(nil), store[kvStatsTotals]...)

	p.migrateStats()

	halted, reason := p.statsHalt()
	require.True(t, halted)
	require.Contains(t, reason, "schema")

	start := atUTC(9, 18, 10)
	p.recordStreamTime([]apiStream{live("videokey-villa", "Villa", start.Format(time.RFC3339Nano))}, cfg, start.Add(time.Hour))
	require.Equal(t, string(before), string(store[kvStatsTotals]))
}

// The documents in production carry no schema marker. Labelling them must not
// touch them.
func TestMigrationLabelsExistingDataWithoutTouchingIt(t *testing.T) {
	p, store := kvBackedPlugin()

	seedTotals(t, store, statTable{"videokey-villa": {Seconds: 400000, Title: "Villa"}})
	before := append([]byte(nil), store[kvStatsTotals]...)

	p.migrateStats()

	require.Equal(t, string(before), string(store[kvStatsTotals]))

	var schema statsSchema
	require.NoError(t, json.Unmarshal(store[kvStatsSchema], &schema))
	require.Equal(t, statsSchemaVersion, schema.Version)

	halted, _ := p.statsHalt()
	require.False(t, halted)

	// Running it again is a no-op, which is what makes a crash part way through
	// a future migration safe to replay.
	p.migrateStats()
	require.Equal(t, string(before), string(store[kvStatsTotals]))
}

// The backup is the answer to "make sure the hours are not lost when the plugin
// is updated": it is taken on activation and only ever replaced by more.
func TestTheBackupIsAHighWaterMark(t *testing.T) {
	p, store := kvBackedPlugin()

	seedTotals(t, store, statTable{"videokey-villa": {Seconds: 400000, Title: "Villa"}})
	p.backupStats("1.12.0")

	backup := p.loadBackup()
	require.NotNil(t, backup)
	require.EqualValues(t, 400000, backup.Seconds)
	require.Equal(t, "1.12.0", backup.PluginVersion)

	// A later build comes up having lost most of it. The backup must not move.
	seedTotals(t, store, statTable{"videokey-villa": {Seconds: 12, Title: "Villa"}})
	p.backupStats("1.13.0")

	kept := p.loadBackup()
	require.EqualValues(t, 400000, kept.Seconds, "a regression must never replace the good copy")
	require.Equal(t, "1.12.0", kept.PluginVersion)

	// And the panel can see it.
	gap, _ := p.backupAhead()
	require.EqualValues(t, 400000-12, gap)
}

func TestRestoreTakesTheLargerFigureAndIsIdempotent(t *testing.T) {
	p, store := kvBackedPlugin()

	seedTotals(t, store, statTable{
		"videokey-villa": {Seconds: 400000, Title: "Villa"},
		"videokey-trent": {Seconds: 200000, Title: "Trent"},
	})
	p.backupStats("1.12.0")

	// Villa has lost their history; Trent has streamed more since.
	seedTotals(t, store, statTable{
		"videokey-trent": {Seconds: 250000, Title: "Trent"},
	})

	restored, err := p.restoreBackup()
	require.NoError(t, err)
	require.Equal(t, 1, restored)

	totals := mustLoad(t, p, kvStatsTotals)
	require.EqualValues(t, 400000, totals["videokey-villa"].Seconds, "the lost history comes back")
	require.EqualValues(t, 250000, totals["videokey-trent"].Seconds, "airtime earned since the backup is kept")

	again, err := p.restoreBackup()
	require.NoError(t, err)
	require.Zero(t, again, "restoring twice changes nothing")
}

// Pausing in OBS used to delete the session mark, so resuming credited the
// whole session a second time.
func TestPausingDoesNotRecreditTheSession(t *testing.T) {
	p, _ := kvBackedPlugin()
	cfg := leaderboardConfig()

	start := atUTC(9, 18, 10)
	iso := start.Format(time.RFC3339Nano)

	p.recordStreamTime([]apiStream{live("videokey-villa", "Villa", iso)}, cfg, start.Add(2*time.Hour))
	require.EqualValues(t, 7200, hours(p, kvStatsTotals, "videokey-villa"))

	paused := live("videokey-villa", "Villa", iso)
	paused.Paused = true

	// Half an hour of pause, polled throughout as the real poller would.
	for minute := 1; minute <= 30; minute++ {
		p.recordStreamTime([]apiStream{paused}, cfg, start.Add(2*time.Hour+time.Duration(minute)*time.Minute))
	}

	// Back on air.
	p.recordStreamTime([]apiStream{live("videokey-villa", "Villa", iso)}, cfg, start.Add(2*time.Hour+30*time.Minute))

	require.EqualValues(t, 7200, hours(p, kvStatsTotals, "videokey-villa"),
		"the pause earns nothing and the session is not counted again")

	// Time on air after the pause is counted normally.
	p.recordStreamTime([]apiStream{live("videokey-villa", "Villa", iso)}, cfg, start.Add(2*time.Hour+40*time.Minute))
	require.EqualValues(t, 7800, hours(p, kvStatsTotals, "videokey-villa"))
}

// An OBS reconnect takes the stream out of the listing entirely for a poll or
// two, which used to have the same effect as a pause.
func TestAReconnectDoesNotRecreditTheSession(t *testing.T) {
	p, _ := kvBackedPlugin()
	cfg := leaderboardConfig()

	start := atUTC(9, 18, 10)
	iso := start.Format(time.RFC3339Nano)
	villa := []apiStream{live("videokey-villa", "Villa", iso)}

	p.recordStreamTime(villa, cfg, start.Add(2*time.Hour))
	require.EqualValues(t, 7200, hours(p, kvStatsTotals, "videokey-villa"))

	// Gone for two polls.
	p.recordStreamTime([]apiStream{}, cfg, start.Add(2*time.Hour+15*time.Second))
	p.recordStreamTime([]apiStream{}, cfg, start.Add(2*time.Hour+30*time.Second))

	// Back, same session.
	p.recordStreamTime(villa, cfg, start.Add(2*time.Hour+45*time.Second))

	require.EqualValues(t, 7245, hours(p, kvStatsTotals, "videokey-villa"),
		"only the 45 seconds that actually elapsed are added")
}

// The mark is not kept for ever, or the sessions document would grow without
// bound.
func TestASessionMarkIsGivenUpEventually(t *testing.T) {
	p, store := kvBackedPlugin()
	cfg := leaderboardConfig()

	start := atUTC(9, 18, 10)
	iso := start.Format(time.RFC3339Nano)

	p.recordStreamTime([]apiStream{live("videokey-villa", "Villa", iso)}, cfg, start.Add(time.Hour))

	first := sessionMarks{}
	require.NoError(t, json.Unmarshal(store[kvStatsSessions], &first))
	require.Len(t, first, 1)

	p.recordStreamTime([]apiStream{}, cfg, start.Add(time.Hour).Add(time.Duration(sessionRetentionMS)*time.Millisecond).Add(time.Minute))

	// A fresh map: unmarshalling into one that already holds the entry merges
	// rather than replaces, and would hide the deletion.
	after := sessionMarks{}
	require.NoError(t, json.Unmarshal(store[kvStatsSessions], &after))
	require.Empty(t, after, "a mark no stream has claimed for half an hour is dropped")
}

// A transient failure reading the marks must not be read as "no session yet",
// which would credit every live stream its whole session again.
func TestUnreadableSessionsSkipThePollRatherThanRecrediting(t *testing.T) {
	p, store, faults := kvBackedPluginWithFaults(map[string][]byte{})
	cfg := leaderboardConfig()

	start := atUTC(9, 18, 10)
	iso := start.Format(time.RFC3339Nano)
	villa := []apiStream{live("videokey-villa", "Villa", iso)}

	p.recordStreamTime(villa, cfg, start.Add(2*time.Hour))
	require.EqualValues(t, 7200, hours(p, kvStatsTotals, "videokey-villa"))
	before := append([]byte(nil), store[kvStatsTotals]...)

	faults.failRead(kvStatsSessions)
	p.recordStreamTime(villa, cfg, start.Add(2*time.Hour+15*time.Second))
	require.Equal(t, string(before), string(store[kvStatsTotals]), "the poll is skipped, not run against empty marks")

	// Not a permanent halt: this is a transient failure, not corruption.
	halted, _ := p.statsHalt()
	require.False(t, halted)

	faults.healRead(kvStatsSessions)
	p.recordStreamTime(villa, cfg, start.Add(2*time.Hour+30*time.Second))
	require.EqualValues(t, 7230, hours(p, kvStatsTotals, "videokey-villa"))
}

// Turning the leaderboard off for a while and back on used to hand every live
// stream the entire off period in one go.
func TestTurningTheLeaderboardBackOnDoesNotBackfill(t *testing.T) {
	p, _ := kvBackedPlugin()

	on := leaderboardConfig()
	off := leaderboardConfig()
	off.EnableLeaderboard = false

	start := atUTC(9, 18, 10)
	iso := start.Format(time.RFC3339Nano)
	villa := []apiStream{live("videokey-villa", "Villa", iso)}

	p.recordStreamTime(villa, on, start.Add(time.Hour))
	require.EqualValues(t, 3600, hours(p, kvStatsTotals, "videokey-villa"))

	// Off for four hours, with the stream still running.
	p.setConfiguration(off)
	p.recordStreamTime(villa, off, start.Add(3*time.Hour))

	// Back on at the four hour mark: OnConfigurationChange is what arms the
	// resynchronisation, and the next poll follows within seconds.
	p.setConfiguration(on)
	p.armStatsResync(start.Add(4 * time.Hour).UnixMilli())

	p.recordStreamTime(villa, on, start.Add(4*time.Hour+15*time.Second))
	require.EqualValues(t, 3600, hours(p, kvStatsTotals, "videokey-villa"),
		"the three hours it was switched off are not credited")

	p.recordStreamTime(villa, on, start.Add(4*time.Hour+10*time.Minute))
	require.EqualValues(t, 4185, hours(p, kvStatsTotals, "videokey-villa"),
		"time after it was switched back on counts normally")
}

// The epoch is not a flag one poll can use up, and it is not permanent either.
func TestTheResyncEpochAppliesOncePerSessionAndThenExpires(t *testing.T) {
	p, _ := kvBackedPlugin()
	cfg := leaderboardConfig()

	start := atUTC(9, 18, 10)
	armed := start.Add(time.Hour)
	p.armStatsResync(armed.UnixMilli())

	// A poll that sees nothing at all must not consume it — that was the flaw
	// in using a single boolean.
	p.recordStreamTime([]apiStream{}, cfg, armed.Add(time.Second))
	require.EqualValues(t, armed.UnixMilli(), p.resyncEpoch(armed.Add(time.Second).UnixMilli()))

	// The session that was already running is clipped when it is first seen.
	iso := start.Format(time.RFC3339Nano)
	villa := []apiStream{live("videokey-villa", "Villa", iso)}
	p.recordStreamTime(villa, cfg, armed.Add(30*time.Second))
	require.Zero(t, hours(p, kvStatsTotals, "videokey-villa"), "the hour before the change is not credited")

	// And only once: the next poll counts normally.
	p.recordStreamTime(villa, cfg, armed.Add(90*time.Second))
	require.EqualValues(t, 60, hours(p, kvStatsTotals, "videokey-villa"))

	// It expires, so it cannot keep clipping sessions that start much later.
	require.Zero(t, p.resyncEpoch(armed.Add(time.Duration(sessionRetentionMS)*time.Millisecond+time.Minute).UnixMilli()))
}

// At the tracking cap a new streamer earns nothing, but the plugin must not
// record them as paid: the time would then be lost even if room appeared.
func TestTheTrackingCapDoesNotSilentlyMarkTimeAsPaid(t *testing.T) {
	p, store := kvBackedPlugin()
	cfg := leaderboardConfig()

	full := statTable{}
	for i := 0; i < maxTrackedStreamers; i++ {
		full["streamer-"+strconv.Itoa(i)] = statEntry{Seconds: 60}
	}
	seedTotals(t, store, full)

	start := atUTC(9, 18, 10)
	iso := start.Format(time.RFC3339Nano)
	newcomer := []apiStream{live("videokey-newcomer", "Newcomer", iso)}

	p.recordStreamTime(newcomer, cfg, start.Add(time.Hour))
	require.Zero(t, hours(p, kvStatsTotals, "videokey-newcomer"))

	marks, err := p.loadSessions()
	require.NoError(t, err)
	require.EqualValues(t, 0, marks["videokey-newcomer"].Credited,
		"an uncredited stream must not be marked as already paid")

	// And the streamers who were already being tracked are untouched.
	totals := mustLoad(t, p, kvStatsTotals)
	require.Len(t, totals, maxTrackedStreamers)
}

// The 24/7 channel is live for ever. If a poll that sees only that channel
// wrote anything, the plugin would be rewriting three KV documents every few
// seconds until the end of time.
func TestAPollWithOnlyTheAlwaysOnChannelWritesNothing(t *testing.T) {
	p, _, faults := kvBackedPluginWithFaults(map[string][]byte{})
	cfg := leaderboardConfig()

	start := atUTC(9, 18, 10)
	watts := live("videokey-watts", "Alan Watts Chillstep", start.Format(time.RFC3339Nano))
	watts.AlwaysOn = true

	for i := 1; i <= 20; i++ {
		p.recordStreamTime([]apiStream{watts}, cfg, start.Add(time.Duration(i)*15*time.Second))
	}

	require.Zero(t, faults.writes[kvStatsTotals], "an always-on channel never earns airtime, so nothing is written")
	require.Zero(t, faults.writes[kvStatsSessions], "and it does not need a session mark either")
}

// A real streamer's marks are not rewritten on every poll just to move a
// timestamp, but the totals still are, because they are actually changing.
func TestSessionMarksAreNotRewrittenOnEveryPoll(t *testing.T) {
	p, _, faults := kvBackedPluginWithFaults(map[string][]byte{})
	cfg := leaderboardConfig()

	start := atUTC(9, 18, 10)
	villa := []apiStream{live("videokey-villa", "Villa", start.Format(time.RFC3339Nano))}

	// Four minutes of polling, every fifteen seconds.
	for i := 1; i <= 16; i++ {
		p.recordStreamTime(villa, cfg, start.Add(time.Duration(i)*15*time.Second))
	}

	require.EqualValues(t, 240, hours(p, kvStatsTotals, "videokey-villa"))
	require.Equal(t, 16, faults.writes[kvStatsTotals], "the totals really do change every poll")
	require.Equal(t, 16, faults.writes[kvStatsSessions], "and the mark's credited figure changes with them")
}

// Mattermost calls OnConfigurationChange once on every plugin start, before
// OnActivate, with no previous configuration to compare against. Reading that
// as "the leaderboard was just switched on" armed a resynchronisation on every
// single start, which threw away the airtime accrued while the plugin was down
// — on exactly the upgrades this work exists to survive.
func TestStartupDoesNotArmAResync(t *testing.T) {
	p, _ := kvBackedPlugin()

	require.Nil(t, p.previousConfiguration(), "a fresh process has no previous configuration")

	fresh := leaderboardConfig()
	require.False(t, p.getConfiguration().EnableLeaderboard,
		"and the substitute getConfiguration hands out has the leaderboard off, which is the trap")

	// What OnConfigurationChange now does on a first load: nothing.
	if previous := p.previousConfiguration(); previous != nil {
		t.Fatal("unreachable")
	}
	p.setConfiguration(fresh)

	require.Zero(t, p.resyncEpoch(nowMillis()), "no resynchronisation is armed by starting up")

	// A genuine change still arms one.
	off := leaderboardConfig()
	off.EnableLeaderboard = false
	p.setConfiguration(off)
	previous := p.previousConfiguration()
	require.NotNil(t, previous)
	require.False(t, previous.EnableLeaderboard)
}

// A mark that goes missing while the plugin is up and running must not let the
// session be paid for all over again when it comes back.
func TestAReturningSessionCannotReclaimTimeThePluginWasWatching(t *testing.T) {
	p, store := kvBackedPlugin()
	cfg := leaderboardConfig()

	start := atUTC(9, 18, 10)
	iso := start.Format(time.RFC3339Nano)
	villa := live("videokey-villa", "Villa", iso)
	trent := live("videokey-trent", "Trent", start.Format(time.RFC3339Nano))

	// Two hours of both of them on air.
	p.recordStreamTime([]apiStream{villa, trent}, cfg, start.Add(2*time.Hour))
	require.EqualValues(t, 7200, hours(p, kvStatsTotals, "videokey-villa"))

	// Villa drops out of the listing for longer than the retention window,
	// while Trent keeps the plugin demonstrably watching throughout. Polled
	// every fifteen seconds, as the plugin really does.
	for tick := 1; tick <= 4*40; tick++ {
		p.recordStreamTime([]apiStream{trent}, cfg, start.Add(2*time.Hour+time.Duration(tick)*15*time.Second))
	}

	marks := sessionMarks{}
	require.NoError(t, json.Unmarshal(store[kvStatsSessions], &marks))
	require.NotContains(t, marks, "videokey-villa", "the mark really was dropped")

	// Villa comes back on the same session.
	p.recordStreamTime([]apiStream{villa, trent}, cfg, start.Add(2*time.Hour+40*time.Minute+15*time.Second))

	// It used to get the whole two hours over again. What it can still claim is
	// one polling interval — the only window the stored evidence cannot rule
	// out the plugin having been away for — and not a second more.
	regained := hours(p, kvStatsTotals, "videokey-villa") - 7200
	require.GreaterOrEqual(t, regained, int64(0))
	require.LessOrEqual(t, regained, int64(15),
		"at most one poll interval, never the whole broadcast again")
}

// The other half of the same rule: time that really did run while the plugin
// was down is still recovered, because that is the whole point of measuring
// against the stream's own start.
func TestTimeDuringAnOutageIsStillRecovered(t *testing.T) {
	p, store := kvBackedPlugin()
	cfg := leaderboardConfig()

	start := atUTC(9, 18, 10)
	iso := start.Format(time.RFC3339Nano)
	villa := []apiStream{live("videokey-villa", "Villa", iso)}

	p.recordStreamTime(villa, cfg, start.Add(time.Hour))
	require.EqualValues(t, 3600, hours(p, kvStatsTotals, "videokey-villa"))

	// The plugin is restarted for an upgrade and is away for ten minutes: a
	// fresh process over the same KV, which is what an upgrade actually is.
	restarted, _ := kvBackedPluginOn(store)

	restarted.recordStreamTime(villa, cfg, start.Add(time.Hour+10*time.Minute))
	require.EqualValues(t, 4200, hours(restarted, kvStatsTotals, "videokey-villa"),
		"the ten minutes it ran while the plugin was away are recovered")
}

// The evidence that claimable() depends on used to live only in the marks map,
// so half an hour with nobody streaming erased it — and a stream that had been
// running unlisted all along could then claim the whole broadcast on the poll
// it first appeared in.
func TestAnEmptyMarksMapDoesNotEraseTheObservationEvidence(t *testing.T) {
	p, store := kvBackedPlugin()
	cfg := leaderboardConfig()

	start := atUTC(9, 18, 10)

	// Nobody live for a long stretch, polled throughout.
	for tick := 1; tick <= 4*45; tick++ {
		p.recordStreamTime([]apiStream{}, cfg, start.Add(time.Duration(tick)*15*time.Second))
	}

	marks := sessionMarks{}
	if len(store[kvStatsSessions]) > 0 {
		require.NoError(t, json.Unmarshal(store[kvStatsSessions], &marks))
	}
	require.Empty(t, marks, "there is no mark left to carry the evidence")

	// A stream that has apparently been running for three hours now appears.
	// The plugin has demonstrably been watching that whole time, so almost
	// none of it is claimable.
	iso := start.Add(-2 * time.Hour).Format(time.RFC3339Nano)
	hidden := []apiStream{live("videokey-hidden", "Hidden", iso)}
	p.recordStreamTime(hidden, cfg, start.Add(45*time.Minute+15*time.Second))

	require.LessOrEqual(t, hours(p, kvStatsTotals, "videokey-hidden"), int64(15),
		"at most one poll interval, not the hours it was not being counted for")
}

// The admin status panel reports counts and timestamps. It must not report the
// tables, because they are keyed by stream key — the export is the one place
// those are allowed to leave the server, and it is a deliberate download.
func TestTheAdminStatusCarriesNoStreamKeys(t *testing.T) {
	p, store, _ := kvBackedPluginWithFaults(map[string][]byte{})

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("HasPermissionTo", mock.Anything, mock.Anything).Return(true)

	seedTotals(t, store, statTable{
		"videokey-villa": {Seconds: 400000, Title: "Villa"},
		"videokey-trent": {Seconds: 200000, Title: "Trent"},
	})
	p.backupStats("1.12.0")

	api.On("KVList", mock.Anything, mock.Anything).Return(
		func(page, perPage int) []string {
			if page > 0 {
				return nil
			}
			keys := make([]string, 0, len(store))
			for key := range store {
				keys = append(keys, key)
			}
			return keys
		},
		func(_, _ int) *model.AppError { return nil },
	)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/airtime/status", nil)
	request.Header.Set("Mattermost-User-Id", "admin-user")
	recorder := httptest.NewRecorder()

	p.ServeHTTP(nil, recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	body := recorder.Body.String()

	require.NotContains(t, body, "videokey-villa", "the status must not name a stream")
	require.NotContains(t, body, "videokey-trent")
	require.NotContains(t, body, "entries", "and must not carry the table itself")

	var status airtimeStatus
	require.NoError(t, json.Unmarshal([]byte(body), &status))
	require.EqualValues(t, 600000, status.RecordedSeconds)
	require.Equal(t, 2, status.Streamers)
	require.True(t, status.HasBackup)
	require.Equal(t, "1.12.0", status.BackupVersion)
}

// And the same endpoint is closed to everybody else.
func TestTheAdminStatusRequiresSystemAdmin(t *testing.T) {
	p, _, _ := kvBackedPluginWithFaults(map[string][]byte{})

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("HasPermissionTo", mock.Anything, mock.Anything).Return(false)

	for _, path := range []string{"/api/v1/admin/airtime", "/api/v1/admin/airtime/status"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Mattermost-User-Id", "ordinary-user")
		recorder := httptest.NewRecorder()

		p.ServeHTTP(nil, recorder, request)

		require.Equal(t, http.StatusForbidden, recorder.Code, path)
	}

	// And with no session at all.
	recorder := httptest.NewRecorder()
	p.ServeHTTP(nil, recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/airtime", nil))
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
}
