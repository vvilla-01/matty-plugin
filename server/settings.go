package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strings"
)

// The settings page, out to a JSON file and back in again. Rebuilding a server,
// moving to another one, or undoing an afternoon of experiments should be one
// download and one upload, not forty fields retyped from a screenshot.
//
// What a settings backup carries, and what it deliberately does not:
//
//   - IN:  every setting on this page, including the birthday list and the
//          theme, which are settings like any other.
//   - OUT: passwords, unless the admin ticks the box and says so.
//   - OUT: recorded airtime. It has its own Export in the Airtime section,
//          because it is irreplaceable and a settings file is not the place to
//          keep the only copy of it.
//   - OUT: the bot's picture, which lives on the bot's Mattermost account
//          rather than in the configuration.
//   - OUT: which person has linked which stream key. Those are per-person
//          publish credentials; a file in somebody's Downloads folder is the
//          wrong home for them, and re-linking is one paste each.
//   - OUT: who is let in to watch without a token. It is per-person access,
//          like the links, not a setting; restoring an older file would
//          quietly let back in people an admin has since let out, which is
//          exactly the wrong failure for a lock; and it is short to re-enter.
//   - OUT: the remembered movie, show and game titles. They are a cache of
//          what the providers said, and looking them up again rebuilds it.
//   - OUT: who has connected which Twitch account, who is blocked from it,
//          and their Twitch hours. They are Twitch's data about people, which
//          VillaNET must be able to delete when somebody disconnects; a copy
//          in a file would outlive that.
//   - OUT: who has connected which Steam or Hytale account, and who is
//          blocked from connecting them. They are people's own connections,
//          which they can delete with one press; a copy in a file would
//          outlive that.
//   - OUT: who chose which awards to show by their name, the biggest-fan and
//          24/7 channel hours, and the soundboard's sounds and who is blocked
//          from it. They are people's own choices, who watched whom, and
//          people's own sounds, which they can take back; a copy in a file
//          would outlive that too.
//
// The file names its own omissions, so a reader can tell the difference between
// "this backup has no Mumble password" and "this backup does not carry
// passwords at all".

const (
	// settingsExportKind is checked on import. A settings file and an airtime
	// file are both JSON with a version in it, and putting one in the other's
	// box should say so rather than half-succeed.
	settingsExportKind = "matty-settings"

	// settingsExportKindBefore and settingsPluginIDBefore are what backups
	// said before 4.0.0, when the plugin was ceo.villa.villanet: read as this
	// plugin's own.
	settingsExportKindBefore = "villanet-tv-settings"
	settingsPluginIDBefore   = "ceo.villa.villanet"

	// settingsExportVersion is the file format, not the plugin version. A file
	// from a NEWER format is refused; one from an older format is read, which
	// is the whole point of writing the number down.
	settingsExportVersion = 1

	maxSettingsImportBytes = 512 << 10

	// redactedSetting is model.FakeSetting: what GetPluginConfig substitutes
	// for any setting the manifest marks secret. Nothing here ever reads a
	// password from that call, but a value carried across verbatim is checked
	// against it so a future secret setting cannot be saved as asterisks.
	redactedSetting = "********************************"
)

// settingsSecrets are the settings that hold a credential. Every one is marked
// secret in plugin.json, and TestEverySecretSettingIsMarkedSecret keeps the two
// lists honest with each other. The TMDB token is one: it is an account's key
// to an outside service, and a settings file in somebody's Downloads folder is
// no place to leave it lying about unasked. The Twitch application's client
// secret is another: it is the password to the admin's Twitch application, and
// Twitch's own terms say to keep it secret. Its Client ID is not, because
// Twitch calls Client IDs public. The Steam Web API key is one: Steam's terms
// say to keep it confidential and share it with nobody. So is Hytale's client
// secret, the password to the admin's Hytale application. Hytale's Client ID
// is not a secret.
var settingsSecrets = []string{"BasicAuthPassword", "MumblePassword", "TMDBToken", "TwitchClientSecret", "SteamWebAPIKey", "HytaleClientSecret"}

// settingsNotIncluded is written into the file itself. An admin reading a
// backup six months from now should not have to guess at its edges. Clips are
// named because an admin restoring onto a new server could otherwise expect
// them back: the file carries the Clips settings, never the clips.
var settingsNotIncluded = []string{
	"Recorded airtime — use Export in the Airtime section",
	"The bot's picture — it lives on the bot's Mattermost account",
	"Which person has linked which stream key",
	"Who is let in to watch without a token — add them again in the Access section",
	"The artwork, the 24/7 pictures and who is blocked from sharing preview pictures — they live in the plugin's store",
	"Remembered movie, show and game titles — a cache, rebuilt by looking them up again",
	"Who has connected which Twitch account, who is blocked from it, and their Twitch hours",
	"Who has connected which Steam or Hytale account, and who is blocked from connecting them — they live in the plugin's store",
	"Clips: published clips, drafts, upvotes, Best Clipper, who is blocked from clipping and which streams are switched off for clipping or for downloads — they live in the plugin's store and on the server's disk",
	"Who chose which awards to show by their name",
	"Biggest fan and 24/7 channel hours: who watched whom — they live in the plugin's store",
	"Soundboard sounds and who is blocked from the soundboard — they live in the plugin's store",
	"Which phones get push notifications, and the push key — each phone registers again by itself",
}

type settingsExport struct {
	Kind          string `json:"kind"`
	Version       int    `json:"version"`
	PluginID      string `json:"pluginId"`
	PluginVersion string `json:"pluginVersion"`
	ExportedAt    int64  `json:"exportedAt"`

	// Settings is keyed by the setting names in plugin.json. Anything absent is
	// left alone on import rather than cleared, which is what makes a
	// passwordless backup safe to restore onto a working server.
	Settings map[string]any `json:"settings"`

	Omitted     []string `json:"omitted,omitempty"`
	NotIncluded []string `json:"notIncluded,omitempty"`
}

type settingsImportResult struct {
	OK      bool     `json:"ok"`
	Changed int      `json:"changed"`
	Kept    []string `json:"kept,omitempty"`
	Unknown []string `json:"unknown,omitempty"`
	From    string   `json:"from,omitempty"`
	Warning string   `json:"warning,omitempty"`
}

// settingKeys maps a lowercased setting name to its canonical spelling, taken
// from the configuration struct rather than from a list kept by hand. A field
// added to configuration is therefore backed up without anybody remembering to
// add it here.
//
// Lowercased because that is how Mattermost matches a stored setting to a
// struct field, so a hand-edited file with "broadcastboxurl" in it must work.
func settingKeys() map[string]string {
	shape := reflect.TypeOf(configuration{})
	keys := make(map[string]string, shape.NumField())

	for i := 0; i < shape.NumField(); i++ {
		name := shape.Field(i).Name
		keys[strings.ToLower(name)] = name
	}

	return keys
}

// settingsMap turns a configuration into the shape it is stored in. Marshalling
// the struct rather than reading the stored map is deliberate: GetPluginConfig
// returns the SANITIZED configuration, so a password read back that way is a
// row of asterisks, and writing that back would set the password to asterisks.
func settingsMap(cfg *configuration) (map[string]any, error) {
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}

	var values map[string]any
	if err := json.Unmarshal(encoded, &values); err != nil {
		return nil, err
	}

	return values, nil
}

func (p *Plugin) handleSettings(w http.ResponseWriter, r *http.Request, userID string) {
	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, "Only a system admin can back up or restore the settings.")
		return
	}

	switch r.Method {
	case http.MethodGet:
		p.exportSettings(w, r)
	case http.MethodPost:
		p.importSettings(w, r, userID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
	}
}

// exportSettings writes the file the browser saves. Indented, because a backup
// nobody can read is a backup nobody checks.
func (p *Plugin) exportSettings(w http.ResponseWriter, r *http.Request) {
	withSecrets := r.URL.Query().Get("secrets") == "1"

	settings, err := settingsMap(p.getConfiguration())
	if err != nil {
		p.API.LogWarn("The settings could not be exported", "error", err.Error())
		writeError(w, http.StatusInternalServerError, "The settings could not be read.")

		return
	}

	export := settingsExport{
		Kind:          settingsExportKind,
		Version:       settingsExportVersion,
		PluginID:      manifestID,
		PluginVersion: manifestVersion,
		ExportedAt:    nowMillis(),
		Settings:      settings,
		NotIncluded:   settingsNotIncluded,
	}

	filename := "matty-settings.json"

	if !withSecrets {
		// Dropped whether or not they are set. A file that carries no passwords
		// says so once, and means the same thing on every server it is restored
		// onto: leave the passwords alone.
		for _, key := range settingsSecrets {
			delete(export.Settings, key)
		}
		export.Omitted = append([]string{}, settingsSecrets...)
		sort.Strings(export.Omitted)
	} else {
		filename = "matty-settings-with-passwords.json"

		// A password that is ALREADY the redaction placeholder is not a
		// password. The console can write the sanitized value back when an
		// admin saves the page without touching the field, and a backup
		// carrying 32 asterisks would restore them onto a healthy server as
		// the real thing. Left out and named instead.
		for _, key := range settingsSecrets {
			if text, isText := export.Settings[key].(string); isText && text == redactedSetting {
				delete(export.Settings, key)
				export.Omitted = append(export.Omitted, key)
			}
		}
		sort.Strings(export.Omitted)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
	w.Header().Set("Cache-Control", "no-store")

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "    ")

	if err := encoder.Encode(export); err != nil {
		p.API.LogWarn("The settings export could not be written", "error", err.Error())
	}
}

// importSettings puts a backup back. Everything the file does not mention keeps
// the value it has now, so restoring a passwordless backup onto a working
// server does not break the connection to the origin.
func (p *Plugin) importSettings(w http.ResponseWriter, r *http.Request, userID string) {
	var file settingsExport

	if err := json.NewDecoder(io.LimitReader(r.Body, maxSettingsImportBytes)).Decode(&file); err != nil {
		writeError(w, http.StatusBadRequest, "That is not a readable settings backup.")
		return
	}

	if file.Kind != settingsExportKind && file.Kind != settingsExportKindBefore {
		writeError(w, http.StatusBadRequest, "That file is not a "+productName+" settings backup. An airtime export goes in the Airtime section instead.")
		return
	}

	if file.Version > settingsExportVersion {
		writeError(w, http.StatusBadRequest, "That backup was written by a newer version of the plugin than this one.")
		return
	}

	if len(file.Settings) == 0 {
		writeError(w, http.StatusBadRequest, "That backup carries no settings.")
		return
	}

	canonical := settingKeys()

	before, err := settingsMap(p.getConfiguration())
	if err != nil {
		p.API.LogWarn("The current settings could not be read for an import", "error", err.Error())
		writeError(w, http.StatusInternalServerError, "The current settings could not be read, so nothing was changed.")

		return
	}

	merged := make(map[string]any, len(before))
	for key, value := range before {
		merged[key] = value
	}

	result := settingsImportResult{From: file.PluginVersion}

	// Which settings the file actually carried, by canonical name. Recorded
	// here rather than looked up in file.Settings afterwards: the overlay below
	// matches without regard to case, so a file written by hand with
	// "basicauthpassword" in it would be APPLIED and then reported as left
	// alone, which is the one thing an admin must be able to trust this list
	// about.
	carried := map[string]bool{}

	for key, value := range file.Settings {
		name, known := canonical[strings.ToLower(key)]
		if !known {
			result.Unknown = append(result.Unknown, key)
			continue
		}

		// And the same guard on the way in, for a file written before the one
		// above existed: asterisks are what a password looks like when it has
		// been hidden, never what it is.
		if text, isText := value.(string); isText && text == redactedSetting {
			result.Kept = append(result.Kept, name)
			carried[name] = true

			continue
		}

		merged[name] = value
		carried[name] = true
	}
	sort.Strings(result.Unknown)

	for _, key := range settingsSecrets {
		if !carried[key] {
			result.Kept = append(result.Kept, key)
		}
	}
	sort.Strings(result.Kept)
	result.Kept = slices.Compact(result.Kept)

	// Round-tripping through the typed struct IS the type check, and it has to
	// happen here rather than being left to the server. LoadPluginConfiguration
	// logs an unmarshal failure and returns nil, so a string where a number
	// belongs would not fail the import — it would silently zero every setting,
	// which is the worst possible way to find out.
	next, err := typedConfiguration(merged)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	next.normalize()

	typed, err := settingsMap(next)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "The imported settings could not be prepared, so nothing was changed.")
		return
	}

	// LOWERCASED, and this is load-bearing rather than tidy.
	//
	// settingsMap marshals the configuration struct, whose fields are named
	// BroadcastBoxURL and so on. The System Console addresses every plugin
	// setting at the LOWERCASED key — custom_plugin_settings builds the path as
	// "PluginSettings.Plugins.<id>." + setting.key.toLowerCase() — and that one
	// path is what it reads a value from and what it writes it back to.
	//
	// Saving the struct's own casing therefore looks like it worked and is not.
	// The plugin keeps running, because LoadPluginConfiguration lowercases on
	// read; the settings PAGE comes up with every box blank and every switch
	// off, directly after a panel saying the settings were restored. Which is
	// precisely how this hid: nothing fails, the wrong thing is merely invisible
	// to the only screen anybody looks at.
	saved := make(map[string]any, len(typed))
	for key, value := range typed {
		saved[strings.ToLower(key)] = value
	}

	// Anything in the stored configuration this build has no field for — a
	// setting from a newer version an admin has downgraded from, say — is
	// carried across untouched. SavePluginConfig replaces the whole map, so
	// leaving these out would quietly delete them. Lowercased for the same
	// reason, so a setting a later build adds is stored where its own console
	// page will look for it.
	for key, value := range p.API.GetPluginConfig() {
		lowered := strings.ToLower(key)
		if _, known := canonical[lowered]; known {
			continue
		}
		if text, isText := value.(string); isText && text == redactedSetting {
			continue
		}
		saved[lowered] = value
	}

	// Counted from the typed map rather than the saved one: both sides are then
	// in the same casing, and a recasing is not a change anybody made.
	for key, value := range typed {
		if _, known := canonical[strings.ToLower(key)]; !known {
			continue
		}
		if !reflect.DeepEqual(before[key], value) {
			result.Changed++
		}
	}

	if appErr := p.API.SavePluginConfig(saved); appErr != nil {
		p.API.LogWarn("The imported settings could not be saved", "user_id", userID, "error", appErr.Error())
		writeError(w, http.StatusInternalServerError, "Mattermost refused to save the imported settings. The server log has the reason.")

		return
	}

	result.OK = true
	result.Warning = importWarning(&file, next)

	p.API.LogWarn("A system admin restored the plugin settings from a backup",
		"user_id", userID,
		"written_by", file.PluginVersion,
		"changed", result.Changed,
		"unknown", len(result.Unknown))

	writeJSON(w, http.StatusOK, result)
}

// typedConfiguration validates the merged map by putting it through the struct
// the plugin actually reads, and names the offending setting when it will not
// fit.
func typedConfiguration(values map[string]any) (*configuration, error) {
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("the imported settings could not be prepared: %w", err)
	}

	var next configuration
	if err := json.Unmarshal(encoded, &next); err != nil {
		var typeError *json.UnmarshalTypeError
		if errors.As(err, &typeError) && typeError.Field != "" {
			return nil, fmt.Errorf("%s in that backup is a %s, which is not what this setting holds. Nothing was changed", typeError.Field, typeError.Value)
		}

		return nil, fmt.Errorf("that backup could not be read as settings, so nothing was changed")
	}

	return &next, nil
}

// importWarning is the note shown after a successful import: something the
// admin should know, but not a reason to refuse the file.
func importWarning(file *settingsExport, next *configuration) string {
	if file.PluginID != "" && file.PluginID != manifestID && file.PluginID != settingsPluginIDBefore {
		return fmt.Sprintf("That backup came from plugin %s, and this is %s. The settings were restored anyway.", file.PluginID, manifestID)
	}

	if err := next.validate(); err != nil {
		return fmt.Sprintf("Restored, but the plugin still has nothing to watch: %s.", err.Error())
	}

	return ""
}
