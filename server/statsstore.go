package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Airtime is the only thing this plugin accumulates that cannot be rebuilt from
// anywhere else. Broadcast Box does not remember how long anyone streamed, so
// once these documents are gone the hours are gone.
//
// They live in the Mattermost plugin KV store, which survives both an upgrade
// and an uninstall: neither RemovePlugin nor removePluginLocally
// (server/channels/app/plugin_install.go) touches the PluginKeyValueStore
// table, and the expiry sweep only deletes rows with a non-zero ExpireAt, which
// KVSet never sets. So the database is not the hazard.
//
// The hazard is this plugin. Every rule below exists to stop a future version
// of it from destroying the history it inherited:
//
//  1. A document that cannot be decoded is copied aside and then LEFT ALONE.
//     Reading it as "empty" and carrying on would have the next poll write an
//     empty table over the original, which is how a format change turns into
//     permanent loss.
//  2. Recording halts entirely on such a failure rather than continuing in a
//     degraded state, because a partial write is worse than no write.
//  3. A schema marker records which format is on disk, so a future change is a
//     migration rather than a silent misread — and a downgrade refuses to write
//     in the older shape.
//  4. The all-time totals are backed up on every activation, and the backup is
//     only ever replaced by a LARGER number. If a build ever comes up with less
//     airtime than the one before it, the good copy is still there and the panel
//     offers an admin a one-click restore.

// errStatsQuarantined marks the ONE failure a caller may respond to by writing:
// the stored bytes would not decode, a verbatim copy is now kept aside, and
// there is nothing left to preserve. Every other failure — a database blip, a
// connection pool exhausted, an HA failover — preserved nothing, so treating it
// the same way turns a transient error into real loss.
var errStatsQuarantined = errors.New("stored airtime was unreadable and has been quarantined")

// statsSchema is the marker document that says which format the airtime
// documents are written in. It is stored separately from the data so that the
// data itself keeps its original, unambiguous shape: a bare
// map[streamKey]statEntry, exactly as versions before 1.12 wrote it.
type statsSchema struct {
	Version int `json:"version"`
}

// statsBackup is the safety copy taken at activation.
type statsBackup struct {
	SavedAt       int64     `json:"savedAt"`
	PluginVersion string    `json:"pluginVersion"`
	Seconds       int64     `json:"seconds"`
	Entries       statTable `json:"entries"`
}

// Why recording stopped. The distinction matters because an admin importing or
// restoring data has dealt with haltData, but cannot do anything at all about
// haltSchema — that one needs the newer build back, and clearing it would let
// this build start rewriting newer documents in an older shape, which is the
// exact thing the check exists to prevent.
const (
	haltData   = "data"
	haltSchema = "schema"
)

// haltStats stops recording and says why, once. The poll loop runs every few
// seconds, so the reason is logged on the first failure only.
func (p *Plugin) haltStats(cause, reason string) {
	p.statsLock.Lock()
	first := !p.statsHalted
	p.statsHalted = true
	p.statsCause = cause
	p.statsReason = reason
	p.statsLock.Unlock()

	if first {
		p.API.LogError(
			"Airtime recording has stopped so the stored history is not overwritten",
			"reason", reason,
			"recover", "the unreadable document was copied to a "+kvQuarantinePrefix+" key in this plugin's KV store",
		)
	}
}

func (p *Plugin) statsHalt() (bool, string) {
	p.statsLock.Lock()
	defer p.statsLock.Unlock()

	return p.statsHalted, p.statsReason
}

// clearDataHalt lets recording resume after an admin has imported or restored.
// A schema halt is left exactly where it is.
func (p *Plugin) clearDataHalt() {
	p.statsLock.Lock()
	defer p.statsLock.Unlock()

	if p.statsCause == haltSchema {
		return
	}

	p.statsHalted = false
	p.statsCause = ""
	p.statsReason = ""
}

// quarantine keeps a verbatim copy of a document that could not be decoded, so
// a botched format change is recoverable by hand instead of gone.
//
// Only the first copy is kept. A second failure must not overwrite it: the
// whole point is to hold the last good bytes, and the second attempt is at
// least as damaged as the first.
func (p *Plugin) quarantine(key string, data []byte) {
	target := kvQuarantinePrefix + key

	stored, appErr := p.API.KVSetWithOptions(target, data, model.PluginKVSetOptions{
		Atomic:   true,
		OldValue: nil,
	})
	if appErr != nil {
		p.API.LogError("Could not quarantine an unreadable document", "key", key, "error", appErr.Error())
		return
	}

	if stored {
		p.API.LogError("Quarantined an unreadable document", "key", key, "quarantine_key", target, "bytes", len(data))
		return
	}

	p.API.LogWarn("An unreadable document was already quarantined; keeping the first copy", "key", key, "quarantine_key", target)
}

// loadStats reads one airtime table.
//
// It returns an error rather than an empty table when the stored bytes cannot
// be decoded. Callers must not treat that as "no history yet": the next write
// would replace real data with nothing.
func (p *Plugin) loadStats(key string) (statTable, error) {
	data, appErr := p.API.KVGet(key)
	if appErr != nil {
		// Unwrapped deliberately: a *model.AppError handed back through an
		// error interface compares non-nil even when it is nil.
		return nil, errors.New(appErr.Error())
	}
	if len(data) == 0 {
		return statTable{}, nil
	}

	table := statTable{}
	if err := json.Unmarshal(data, &table); err != nil {
		p.quarantine(key, data)

		return nil, fmt.Errorf("%w: %s (%s)", errStatsQuarantined, key, err)
	}

	return table, nil
}

// loadLedger reads one of the two 3.2.0 ledgers, fans_totals and
// chanwatch_totals (fans.go, chanwatch.go), which break loadStats's rule on
// purpose.
//
// A store that does not answer is an error, as ever, and the pass skips the
// table. But bytes that will not decode are DELETED and the table starts
// again from zero: no quarantine copy, no halt, no button. Why:
//
//   - fans_totals is who watched whom, and quarantine keeps its first copy
//     for ever: a copy nobody can read is a copy nobody can ever delete from.
//     That was first said for 3.2.0's opt-out, which deleted what was counted
//     for somebody; there is no opt-out from 3.2.1, and the rule stands for
//     the same kind of data. 3.1.0's Delete all Twitch data deletes its
//     quarantine copies for the same reason.
//   - Losing either costs titles and badges that start counting again; no
//     board, medal or airtime depends on them, and a halt and a restart
//     button would be machinery for very little.
//   - loadSessions already treats stats_sessions this way.
//
// The bytes are deleted only if they are still the ones read, so a pass that
// saved good bytes in the meantime is never undone. The reset is logged at
// Error, with the key and the size, and admins are told until the plugin next
// restarts (ledgerAlert).
func (p *Plugin) loadLedger(key string) (statTable, error) {
	data, appErr := p.API.KVGet(key)
	if appErr != nil {
		return nil, errors.New(appErr.Error())
	}
	if len(data) == 0 {
		return statTable{}, nil
	}

	table := statTable{}
	if err := json.Unmarshal(data, &table); err != nil {
		deleted, appErr := p.API.KVCompareAndDelete(key, data)
		if appErr != nil {
			return nil, errors.New(appErr.Error())
		}
		if !deleted {
			return nil, fmt.Errorf("%s changed while it was being read", key)
		}

		p.API.LogError("A ledger could not be read, so it has been deleted and started again from zero",
			"key", key, "bytes", len(data), "error", err.Error())
		p.ledgers().noteReset(key, nowMillis())

		return statTable{}, nil
	}
	if table == nil {
		table = statTable{}
	}

	return table, nil
}

// loadLedgerUnderWriteLock is loadLedger for a reader outside a pass, with
// statsWriteLock held and let go by a defer, and keep, when there is one, given
// what was read before it is let go: a store that answers with a panic must
// not leave every stats write waiting for good.
func (p *Plugin) loadLedgerUnderWriteLock(key string, keep func(statTable)) (statTable, error) {
	p.statsWriteLock.Lock()
	defer p.statsWriteLock.Unlock()

	loaded, err := p.loadLedger(key)
	if err == nil && keep != nil {
		keep(loaded)
	}

	return loaded, err
}

// saveStats writes a whole table with a plain KVSet rather than a compare-and-
// set, and that is a decision rather than an oversight.
//
// Within one server the read-modify-write is already serialised: statsWriteLock
// covers the poller and the admin import/restore, and restartPoller waits for
// the previous poller to finish before starting the next. Across a Mattermost
// HA CLUSTER it is not — every node runs its own poller against the same
// document, and two nodes writing in the same second would drop one node's
// credit. A cluster would need KVSetWithOptions{Atomic: true, OldValue: <as
// read>} with a retry around the whole credit pass, which is why the handle
// secret in handles.go does use the atomic form: there, a lost race breaks
// playback outright rather than costing a few seconds of airtime.
//
// This installation is a single server, and the cost of the race is bounded by
// one poll interval of one streamer's time.
func (p *Plugin) saveStats(key string, table statTable) error {
	data, err := json.Marshal(table)
	if err != nil {
		return err
	}

	if appErr := p.API.KVSet(key, data); appErr != nil {
		return errors.New(appErr.Error())
	}

	return nil
}

// loadSessions reads the in-flight session marks.
//
// The two failure modes are treated differently on purpose. A KV error is
// transient and is reported, because carrying on with no marks would credit
// every live stream its whole session a second time. A document that will not
// decode is kept aside and replaced with empty marks: that is indistinguishable
// from the plugin having just restarted, which the accounting already handles.
func (p *Plugin) loadSessions() (sessionMarks, error) {
	return p.loadSessionMarks(kvStatsSessions)
}

// loadSessionMarks is loadSessions for any set of marks: Broadcast Box's, or
// the Twitch ledger's (twitch_air.go), which follow the same rules.
func (p *Plugin) loadSessionMarks(key string) (sessionMarks, error) {
	data, appErr := p.API.KVGet(key)
	if appErr != nil {
		return nil, errors.New(appErr.Error())
	}
	if len(data) == 0 {
		return sessionMarks{}, nil
	}

	marks := sessionMarks{}
	if err := json.Unmarshal(data, &marks); err != nil {
		p.quarantine(key, data)
		p.API.LogWarn("Stream sessions were unreadable; starting fresh marks", "key", key, "error", err.Error())

		return sessionMarks{}, nil
	}

	return marks, nil
}

func (p *Plugin) saveSessions(marks sessionMarks) {
	if err := p.saveSessionMarks(kvStatsSessions, marks); err != nil {
		p.API.LogWarn("Could not save stream sessions", "error", err.Error())
	}
}

func (p *Plugin) saveSessionMarks(key string, marks sessionMarks) error {
	data, err := json.Marshal(marks)
	if err != nil {
		return err
	}
	if appErr := p.API.KVSet(key, data); appErr != nil {
		return errors.New(appErr.Error())
	}

	return nil
}

// migrateStats labels the stored format, converting it first when the format
// has moved on. It runs once per activation, before the poller starts.
func (p *Plugin) migrateStats() {
	data, appErr := p.API.KVGet(kvStatsSchema)
	if appErr != nil {
		// Transient. Not knowing the marker is survivable, because loadStats
		// refuses to write over anything it cannot decode anyway — the marker
		// is the second line of defence, not the only one. Halting for ever
		// over a database still waking up would be worse.
		p.API.LogWarn("Could not read the airtime schema marker; leaving it alone this time", "error", appErr.Error())
		return
	}

	version := 0
	if len(data) > 0 {
		var schema statsSchema
		if err := json.Unmarshal(data, &schema); err != nil {
			p.quarantine(kvStatsSchema, data)
			p.haltStats(haltData, "the airtime schema marker is unreadable")

			return
		}
		version = schema.Version
	}

	switch {
	case version == statsSchemaVersion:
		return

	case version > statsSchemaVersion:
		// A newer build wrote this. Rewriting its documents in an older shape
		// is exactly the kind of downgrade that loses data, so this build
		// reads nothing and writes nothing until the newer one is back.
		p.haltStats(haltSchema, fmt.Sprintf("stored airtime is schema v%d and this build understands v%d; upgrade the plugin again", version, statsSchemaVersion))

		return
	}

	// version < statsSchemaVersion.
	//
	// v0 is the original unversioned format. It is byte-identical to v1, which
	// only introduced this marker, so there is nothing to convert and the
	// documents are left untouched.
	//
	// A future v2 adds its conversion HERE, before the marker is written, and
	// must be safe to run twice: the marker is written last, so a crash part
	// way through replays the conversion on the next activation. One that
	// rewrites the monthly tables calls forgetMonthPodiums as well, like every
	// other writer of them; see monthmedals.go.

	marker, err := json.Marshal(statsSchema{Version: statsSchemaVersion})
	if err != nil {
		return
	}
	if appErr := p.API.KVSet(kvStatsSchema, marker); appErr != nil {
		p.API.LogWarn("Could not record the airtime schema version", "error", appErr.Error())
		return
	}

	p.API.LogInfo("Airtime storage is at schema version", "from", version, "to", statsSchemaVersion)
}

func totalSeconds(table statTable) int64 {
	var sum int64
	for _, entry := range table {
		if entry.Seconds > 0 {
			sum += entry.Seconds
		}
	}

	return sum
}

func (p *Plugin) loadBackup() *statsBackup {
	data, appErr := p.API.KVGet(kvStatsBackup)
	if appErr != nil || len(data) == 0 {
		return nil
	}

	var backup statsBackup
	if err := json.Unmarshal(data, &backup); err != nil {
		p.quarantine(kvStatsBackup, data)
		return nil
	}

	return &backup
}

// backupStats refreshes the safety copy of the all-time totals. Called on
// activation, which is the moment a plugin upgrade lands.
//
// The copy is only ever replaced by MORE airtime than it already holds, so a
// build that comes up having lost hours cannot quietly overwrite the good
// figures with its own emptier ones — the whole point of keeping it.
func (p *Plugin) backupStats(version string) {
	live, err := p.loadStats(kvStatsTotals)
	if err != nil {
		if errors.Is(err, errStatsQuarantined) {
			p.haltStats(haltData, err.Error())
		} else {
			p.API.LogWarn("Could not read the airtime totals to back them up", "error", err.Error())
		}

		return
	}
	if len(live) == 0 {
		return
	}

	liveSeconds := totalSeconds(live)
	previous := p.loadBackup()

	if previous != nil && previous.Seconds > liveSeconds {
		p.API.LogError(
			"Stored airtime is lower than the last backup, so the backup has been kept",
			"stored_seconds", liveSeconds,
			"backup_seconds", previous.Seconds,
			"backup_taken", time.UnixMilli(previous.SavedAt).UTC().Format(time.RFC3339),
			"restore", "a system admin can restore it in System Console > Plugins > "+productName+" > Airtime and awards",
		)

		return
	}

	data, marshalErr := json.Marshal(statsBackup{
		SavedAt:       nowMillis(),
		PluginVersion: version,
		Seconds:       liveSeconds,
		Entries:       live,
	})
	if marshalErr != nil {
		return
	}

	if appErr := p.API.KVSet(kvStatsBackup, data); appErr != nil {
		p.API.LogWarn("Could not back up the airtime totals", "error", appErr.Error())
		return
	}

	p.API.LogInfo("Backed up airtime totals", "streamers", len(live), "seconds", liveSeconds)
}

// restoreBackup merges the safety copy back into the live totals, keeping
// whichever figure is larger for each streamer. Merging rather than replacing
// means a restore can never undo airtime earned since the backup was taken.
func (p *Plugin) restoreBackup() (int, error) {
	// Serialised against the poller, which is doing its own read-modify-write
	// of this very document every few seconds.
	p.statsWriteLock.Lock()
	defer p.statsWriteLock.Unlock()

	backup := p.loadBackup()
	if backup == nil || len(backup.Entries) == 0 {
		return 0, errors.New("there is no airtime backup to restore")
	}

	live, err := p.loadStats(kvStatsTotals)
	if err != nil {
		if !errors.Is(err, errStatsQuarantined) {
			// The document could not be READ, which is not the same as it
			// being unreadable. Nothing was copied aside, so merging into an
			// empty table here would throw away every hour earned since the
			// backup was taken. Refuse and let the admin try again.
			return 0, errors.New("the stored airtime could not be read just now; try again in a moment")
		}

		// Genuinely undecodable, and a verbatim copy is already quarantined.
		// This is precisely the case a restore exists for.
		live = statTable{}
	}

	restored := 0
	for key, entry := range backup.Entries {
		current := live[key]
		if entry.Seconds > current.Seconds {
			current.Seconds = entry.Seconds
			restored++
		}
		if current.Title == "" {
			current.Title = entry.Title
		}
		live[key] = current
	}

	if err := p.saveStats(kvStatsTotals, live); err != nil {
		return 0, err
	}

	p.clearDataHalt()

	p.API.LogInfo("Restored airtime from the backup", "streamers_restored", restored)

	return restored, nil
}

// backupAhead reports how far the safety copy is ahead of what is stored now.
// Anything above zero means airtime went missing, which is what the panel
// surfaces to system admins.
func (p *Plugin) backupAhead() (int64, *statsBackup) {
	backup := p.loadBackup()
	if backup == nil {
		return 0, nil
	}

	live, err := p.loadStats(kvStatsTotals)
	if err != nil {
		// A transient read failure is not evidence that anything is missing,
		// and reporting the whole backup as a gap would raise a false alarm on
		// every database hiccup. A real problem shows up as the halt instead.
		return 0, backup
	}

	if gap := backup.Seconds - totalSeconds(live); gap > 0 {
		return gap, backup
	}

	return 0, backup
}

// armStatsResync records that sessions running right now should not be handed
// backdated airtime.
func (p *Plugin) armStatsResync(at int64) {
	p.statsLock.Lock()
	p.statsResyncAt = at
	p.statsLock.Unlock()
}

// resyncEpoch returns the moment sessions must be brought up to date against,
// or zero. It expires itself: after the retention window every session that was
// running at the change has long since been seen, and leaving it armed would
// keep clipping a few seconds off each new session for ever.
func (p *Plugin) resyncEpoch(nowMS int64) int64 {
	p.statsLock.Lock()
	defer p.statsLock.Unlock()

	if p.statsResyncAt == 0 {
		return 0
	}
	if nowMS-p.statsResyncAt > sessionRetentionMS {
		p.statsResyncAt = 0
		return 0
	}

	return p.statsResyncAt
}

// haltOrSkip stops recording for good when the stored bytes are the problem,
// and merely gives up on this poll when the database was.
func (p *Plugin) haltOrSkip(err error) {
	if errors.Is(err, errStatsQuarantined) {
		p.haltStats(haltData, err.Error())
		return
	}

	p.API.LogWarn("Could not read the stored airtime; skipping this poll", "error", err.Error())
}
