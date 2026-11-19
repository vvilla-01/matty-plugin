package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// A system admin's way in and out of the airtime history.
//
// Export exists so the hours can be taken off the server before anything risky
// — a Mattermost upgrade, a database move, a plugin id change — and put back
// afterwards. Import exists so putting them back is a request rather than a
// hand-written SQL statement against PluginKeyValueStore.
//
// Import defaults to merging on the LARGER of the two figures per streamer.
// Restoring an old export can then never take hours away from someone who has
// streamed since it was taken, and running the same import twice does nothing
// the second time.
//
// NOTE, deliberately: the airtime tables are keyed by STREAM KEY, so this is
// the one response in the plugin that puts real stream keys in a browser. It is
// gated on manage_system, and a system admin already has them — they are what
// the "Stream names" setting is written in, and they are readable
// straight out of the KV store. The alternative, keying the export by the
// opaque handle, would make a backup useless: the handle is an HMAC under a
// per-installation secret, so it would not resolve after a rebuild, which is
// exactly the case a backup exists for. Nothing that reaches a non-admin —
// the snapshot, the leaderboard rows, the panel's airtime alert — carries a
// key.

const maxAirtimeImportBytes = 2 << 20 // 2 MiB

// An import's month tables each cost a KV read and a KV write while the
// poller's lock is held, and the body limit alone allows about 160,000 of them.
// Twenty years of history is a generous ceiling for a plugin that shipped this
// year.
const maxImportMonths = 240

// A real month, and a year this plugin could plausibly have recorded. The
// looser \d{4}-\d{2} accepted 0000-00 and 9999-99, which are a million KV keys
// that nothing will ever read again.
var monthSuffix = regexp.MustCompile(`^20[2-9]\d-(0[1-9]|1[0-2])$`)

type airtimeExport struct {
	Version       int                  `json:"version"`
	ExportedAt    int64                `json:"exportedAt"`
	PluginVersion string               `json:"pluginVersion"`
	Totals        statTable            `json:"totals"`
	Months        map[string]statTable `json:"months"`
	Backup        *statsBackup         `json:"backup,omitempty"`
	Halted        string               `json:"halted,omitempty"`

	// Incomplete names any monthly table that could not be read. An export is
	// taken before something risky, so quietly returning a short one with a
	// 200 would be the worst possible moment to be reassuring.
	Incomplete []string `json:"incomplete,omitempty"`
}

type airtimeImport struct {
	Version int                  `json:"version"`
	Mode    string               `json:"mode"`
	Totals  statTable            `json:"totals"`
	Months  map[string]statTable `json:"months"`
}

// airtimeStatus is the summary the System Console panel renders. It carries
// counts and timestamps only — deliberately NOT the tables themselves, which
// are keyed by stream key. The export is the one place those leave the server.
type airtimeStatus struct {
	PluginVersion   string   `json:"pluginVersion"`
	Recording       bool     `json:"recording"`
	Readable        bool     `json:"readable"`
	RecordedSeconds int64    `json:"recordedSeconds"`
	Streamers       int      `json:"streamers"`
	Months          int      `json:"months"`
	Halted          bool     `json:"halted"`
	HaltedReason    string   `json:"haltedReason,omitempty"`
	Quarantined     []string `json:"quarantined,omitempty"`

	HasBackup         bool   `json:"hasBackup"`
	BackupSavedAt     int64  `json:"backupSavedAt,omitempty"`
	BackupVersion     string `json:"backupVersion,omitempty"`
	BackupSeconds     int64  `json:"backupSeconds,omitempty"`
	BackupStreamers   int    `json:"backupStreamers,omitempty"`
	BackupAheadSecond int64  `json:"backupAheadSeconds"`

	// Twitch hours are a ledger of their own, never in these figures, the
	// export or the backup (design/twitch-live.md §7.12): said while Show
	// people live on Twitch is on, so an admin does not look for them here.
	TwitchSeparate bool `json:"twitchSeparate,omitempty"`

	// 3.2.0 (design/3.2.0.md §2.2): when the biggest-fan or 24/7 channel
	// hours would not decode and were started from zero, since the plugin
	// last started. Until 3.2.1 there was also fansOff, for a list of
	// biggest-fan opt-outs that could not be read; there is no opt-out now.
	FansResetAt     int64 `json:"fansResetAt,omitempty"`
	ChannelsResetAt int64 `json:"channelsResetAt,omitempty"`
}

type airtimeResult struct {
	Mode    string `json:"mode"`
	Changed int    `json:"changed"`
	Message string `json:"message"`
}

// airtimeAlert is the one piece of this that reaches the panel: it says the
// stored history is smaller than the backup taken when the plugin last
// started, which is the shape "an update lost my hours" actually has.
type airtimeAlert struct {
	MissingSeconds int64  `json:"missingSeconds"`
	BackupTaken    int64  `json:"backupTaken"`
	PluginVersion  string `json:"pluginVersion"`
	Halted         string `json:"halted,omitempty"`
}

func (p *Plugin) isSystemAdmin(userID string) bool {
	return p.API.HasPermissionTo(userID, model.PermissionManageSystem)
}

// airtimeStatus builds the panel's alert, or nil when nothing is wrong. Only
// ever called for system admins: a regular viewer can do nothing about it and
// does not need to know the plugin's internal state.
func (p *Plugin) airtimeStatus() *airtimeAlert {
	halted, reason := p.statsHalt()
	gap, backup := p.backupAhead()

	if gap <= 0 && !halted {
		return nil
	}

	alert := &airtimeAlert{MissingSeconds: gap}
	if halted {
		alert.Halted = reason
	}
	if backup != nil {
		alert.BackupTaken = backup.SavedAt
		alert.PluginVersion = backup.PluginVersion
	}

	return alert
}

// handleAirtimeStatus backs the System Console panel. Read-only, and cheap
// enough to poll after an action.
func (p *Plugin) handleAirtimeStatus(w http.ResponseWriter, r *http.Request, userID string) {
	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, "Only a system admin can see the airtime tools.")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	cfg := p.getConfiguration()
	status := airtimeStatus{
		PluginVersion:  manifestVersion,
		Recording:      cfg.EnableLeaderboard,
		TwitchSeparate: cfg.twitchLiveOn(),
	}

	status.Halted, status.HaltedReason = p.statsHalt()

	if totals, err := p.loadStats(kvStatsTotals); err == nil {
		status.Readable = true
		status.RecordedSeconds = totalSeconds(totals)
		status.Streamers = len(totals)
	}

	status.Months = len(p.kvKeysWithPrefix(kvStatsMonthPrefix))

	// Only airtime documents. The quarantine is shared with the schedule, and
	// telling an admin their irreplaceable streaming history is damaged because
	// somebody's plan to stream on Friday would not decode is the wrong alarm
	// at the wrong volume.
	for _, key := range p.kvKeysWithPrefix(kvQuarantinePrefix) {
		document := strings.TrimPrefix(key, kvQuarantinePrefix)
		if !strings.HasPrefix(document, "stats_") {
			continue
		}
		status.Quarantined = append(status.Quarantined, document)
	}
	sort.Strings(status.Quarantined)

	if backup := p.loadBackup(); backup != nil {
		status.HasBackup = true
		status.BackupSavedAt = backup.SavedAt
		status.BackupVersion = backup.PluginVersion
		status.BackupSeconds = backup.Seconds
		status.BackupStreamers = len(backup.Entries)
	}

	status.BackupAheadSecond, _ = p.backupAhead()

	if alert := p.ledgerAlertFor(); alert != nil {
		status.FansResetAt, status.ChannelsResetAt = alert.FansResetAt, alert.ChannelsResetAt
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, status)
}

func (p *Plugin) handleAirtime(w http.ResponseWriter, r *http.Request, userID string) {
	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, "Only a system admin can export or restore airtime.")
		return
	}

	switch r.Method {
	case http.MethodGet:
		p.exportAirtime(w)
	case http.MethodPost:
		p.importAirtime(w, r, userID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
	}
}

func (p *Plugin) exportAirtime(w http.ResponseWriter) {
	export := airtimeExport{
		Version:       statsSchemaVersion,
		ExportedAt:    nowMillis(),
		PluginVersion: manifestVersion,
		Months:        map[string]statTable{},
	}

	totals, err := p.loadStats(kvStatsTotals)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "The stored airtime could not be read. A copy has been kept in the plugin's KV store under a quarantine_ key.")
		return
	}
	export.Totals = totals

	for _, key := range p.kvKeysWithPrefix(kvStatsMonthPrefix) {
		suffix := strings.TrimPrefix(key, kvStatsMonthPrefix)

		month, monthErr := p.loadStats(key)
		if monthErr != nil {
			p.API.LogWarn("A monthly airtime table could not be exported", "month", suffix, "error", monthErr.Error())
			export.Incomplete = append(export.Incomplete, suffix)

			continue
		}
		export.Months[suffix] = month
	}

	sort.Strings(export.Incomplete)

	export.Backup = p.loadBackup()
	if halted, reason := p.statsHalt(); halted {
		export.Halted = reason
	}

	// Named so a browser following the link saves something recognisable
	// rather than "airtime".
	w.Header().Set("Content-Disposition", `attachment; filename="matty-airtime.json"`)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, export)
}

func (p *Plugin) importAirtime(w http.ResponseWriter, r *http.Request, userID string) {
	var request airtimeImport
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAirtimeImportBytes)).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "That is not a readable airtime export.")
		return
	}

	if request.Version > statsSchemaVersion {
		writeError(w, http.StatusBadRequest, "That export was written by a newer version of the plugin.")
		return
	}

	if len(request.Months) > maxImportMonths {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("That export carries %d monthly tables; %d is the most this will take at once.", len(request.Months), maxImportMonths))
		return
	}

	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	if mode == "" {
		mode = "merge"
	}

	if mode == "restore" {
		restored, err := p.restoreBackup()
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}

		p.API.LogWarn("A system admin restored the airtime backup", "user_id", userID, "streamers", restored)
		p.refreshSoon()
		writeJSON(w, http.StatusOK, airtimeResult{
			Mode:    mode,
			Changed: restored,
			Message: "Airtime restored from the backup.",
		})

		return
	}

	if mode != "merge" && mode != "replace" {
		writeError(w, http.StatusBadRequest, `Mode must be "merge", "replace" or "restore".`)
		return
	}

	// One writer at a time, or this races the poller's own read-modify-write.
	p.statsWriteLock.Lock()
	defer p.statsWriteLock.Unlock()

	changed := 0

	if request.Totals != nil {
		applied, err := p.applyTable(kvStatsTotals, request.Totals, mode)
		if err != nil {
			p.API.LogWarn("Could not apply an imported airtime table", "key", kvStatsTotals, "error", err.Error())
			writeError(w, http.StatusServiceUnavailable, clientSafeImportError(err))

			return
		}
		changed += applied
	}

	for suffix, table := range request.Months {
		if !monthSuffix.MatchString(suffix) {
			continue
		}
		applied, err := p.applyTable(kvStatsMonthPrefix+suffix, table, mode)
		if err != nil {
			p.API.LogWarn("Could not apply an imported airtime table", "key", suffix, "error", err.Error())
			writeError(w, http.StatusServiceUnavailable, clientSafeImportError(err))

			return
		}
		changed += applied
	}

	// An admin has now taken a deliberate decision about the data, so recording
	// can resume — unless what stopped it was a schema downgrade, which an
	// import cannot resolve and which exists to stop this build writing at all.
	p.clearDataHalt()

	p.API.LogWarn("A system admin imported airtime", "user_id", userID, "mode", mode, "entries", changed)
	p.refreshSoon()

	writeJSON(w, http.StatusOK, airtimeResult{
		Mode:    mode,
		Changed: changed,
		Message: "Airtime imported.",
	})
}

func clientSafeImportError(err error) string {
	if errors.Is(err, errStatsUnreadable) {
		return "The stored airtime could not be read just now, so nothing was changed. Try again in a moment."
	}

	return "Could not write the airtime."
}

// errStatsUnreadable is returned when a merge could not read what it is
// merging into. It is a refusal, not a failure to write.
var errStatsUnreadable = errors.New("the stored airtime could not be read")

// applyTable writes one imported table. In merge mode the larger figure wins
// per streamer, so an old export can never take hours off somebody who has
// streamed since; replace overwrites the document outright, which is the
// deliberate "put it back exactly as it was" case.
func (p *Plugin) applyTable(key string, incoming statTable, mode string) (int, error) {
	// Any month can be rewritten here, including one that has ended, and the
	// medals in people's profiles are read from a copy of those months' podiums.
	// Deferred so it also covers a write that reported failure: a KV write that
	// timed out may still have landed, and reading the months again once costs
	// far less than a profile that disagrees with the board.
	if strings.HasPrefix(key, kvStatsMonthPrefix) {
		defer p.forgetMonthPodiums()
	}

	target := statTable{}

	if mode == "merge" {
		existing, err := p.loadStats(key)
		switch {
		case err == nil:
			target = existing

		case errors.Is(err, errStatsQuarantined):
			// Genuinely undecodable, and a verbatim copy is already kept
			// aside. Merging into nothing is exactly what an import is for
			// here, and refusing would leave the admin no way back.
			target = statTable{}

		default:
			// It could not be READ, which is a different thing. Nothing was
			// preserved, so merging into an empty table would silently drop
			// every hour this document holds — which is the one guarantee
			// merge mode makes. Refuse and change nothing.
			return 0, errStatsUnreadable
		}
	}

	changed := 0
	for rawKey, entry := range incoming {
		streamKey := normalizeKey(rawKey)
		if streamKey == "" || !validStreamKey(streamKey) || entry.Seconds < 0 {
			continue
		}
		if len(target) >= maxTrackedStreamers {
			if _, tracked := target[streamKey]; !tracked {
				continue
			}
		}

		current := target[streamKey]
		if mode == "replace" || entry.Seconds > current.Seconds {
			current.Seconds = entry.Seconds
			changed++
		}
		if entry.Title != "" {
			current.Title = entry.Title
		}
		target[streamKey] = current
	}

	if err := p.saveStats(key, target); err != nil {
		return 0, err
	}

	return changed, nil
}
