package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// settingsPlugin is an activated plugin with one admin, a configuration to back
// up, and a SavePluginConfig that records what it was handed instead of writing
// it anywhere.
// savedConfig is what SavePluginConfig was handed. Read through at(), which
// lowercases, because that is how Mattermost stores and addresses these keys —
// asserting on the struct's own casing is what let the casing bug through.
type savedConfig map[string]any

func (s savedConfig) at(name string) any {
	return s[strings.ToLower(name)]
}

func settingsPlugin(t *testing.T, cfg *configuration, stored map[string]any) (*Plugin, *savedConfig) {
	t.Helper()

	saved := &savedConfig{}

	api := &plugintest.API{}
	api.On("HasPermissionTo", "uid-admin", model.PermissionManageSystem).Return(true)
	api.On("HasPermissionTo", "uid-user", model.PermissionManageSystem).Return(false)
	api.On("GetPluginConfig").Return(stored)
	api.On("SavePluginConfig", mock.Anything).Return(
		func(config map[string]any) *model.AppError {
			*saved = config

			return nil
		},
	)
	for _, level := range []string{"LogWarn", "LogError", "LogInfo", "LogDebug"} {
		for pairs := 0; pairs <= 8; pairs++ {
			args := make([]any, 0, 1+(pairs*2))
			for i := 0; i < 1+(pairs*2); i++ {
				args = append(args, mock.Anything)
			}
			api.On(level, args...).Return()
		}
	}

	p := &Plugin{}
	p.SetAPI(api)
	p.setConfiguration(cfg)

	return p, saved
}

func fullConfig() *configuration {
	return &configuration{
		BroadcastBoxURL:     "http://192.168.1.20:8080",
		BasicAuthUsername:   "villa",
		BasicAuthPassword:   "the-basic-auth-password",
		PollIntervalSeconds: 10,
		EnableLiveBadge:     true,
		LiveEmoji:           "red_circle",
		LiveStatusText:      "Live now",
		MumbleServer:        "192.168.1.20",
		MumbleUsername:      "VillaNET",
		MumblePassword:      "the-mumble-password",
		EnableChatBridge:    true,
		BridgeChannel:       "whiskey-boys/b-andom",
		EnableBirthdays:     true,
		BirthdayHour:        9,
		Birthdays:           `[{"name":"Villa","month":3,"day":27}]`,
		BannerColor:         "#c8102e",
		OfflineGraceChecks:  2,
		MediaLookup:         true,
		TMDBToken:           "the-tmdb-token",
		TwitchClientID:      "the-twitch-client-id",
		TwitchClientSecret:  "the-twitch-client-secret",
		EnableTwitchLive:    true,
		TwitchEmbed:         true,
		EnableSteam:         true,
		SteamWebAPIKey:      "the-steam-web-api-key",
		EnableHytale:        true,
		HytaleClientID:      "the-hytale-client-id",
		HytaleClientSecret:  "the-hytale-client-secret",
		EnablePush:          true,
		PushAllowedHosts:    "ntfy.home.lan\n192.168.1.30:8080",
		PushOnlyListed:      true,
		PushContact:         "mailto:admin@example.com",
	}
}

func export(t *testing.T, p *Plugin, query string) settingsExport {
	t.Helper()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings"+query, nil)
	request.Header.Set("Mattermost-User-Id", "uid-admin")

	p.handleSettings(recorder, request, "uid-admin")
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var file settingsExport
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &file))

	return file
}

func importFile(t *testing.T, p *Plugin, file any) (*httptest.ResponseRecorder, settingsImportResult) {
	t.Helper()

	body, err := json.Marshal(file)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/settings", strings.NewReader(string(body)))
	request.Header.Set("Mattermost-User-Id", "uid-admin")

	p.handleSettings(recorder, request, "uid-admin")

	var result settingsImportResult
	_ = json.Unmarshal(recorder.Body.Bytes(), &result)

	return recorder, result
}

// The point of the whole feature: what comes out goes back in unchanged.
func TestASettingsBackupRestoresEverySetting(t *testing.T) {
	source, _ := settingsPlugin(t, fullConfig(), map[string]any{})
	file := export(t, source, "?secrets=1")

	// A fresh server with nothing set on it.
	target, saved := settingsPlugin(t, &configuration{}, map[string]any{})

	recorder, result := importFile(t, target, file)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.True(t, result.OK)
	require.Empty(t, result.Unknown)
	require.Empty(t, result.Warning, "a complete backup leaves nothing to warn about")

	restored, err := typedConfiguration(*saved)
	require.NoError(t, err)

	want := fullConfig()
	want.normalize()
	require.Equal(t, want, restored, "the restored configuration matches the one backed up")
}

// The file says what it leaves out, Clips among it: the settings for Clips
// travel, downloads' switch with them (design/3.2.1.md §1.1); the clips
// themselves, their votes, Best Clipper and the streamers' switches do not,
// and an admin restoring onto a new server must not expect them back.
func TestABackupSaysItCarriesNoClips(t *testing.T) {
	source, _ := settingsPlugin(t, fullConfig(), map[string]any{})
	file := export(t, source, "")

	clips := ""
	for _, line := range file.NotIncluded {
		if strings.HasPrefix(line, "Clips:") {
			clips = line
		}
	}
	require.NotEmpty(t, clips, "the file names Clips among what it leaves out")
	for _, named := range []string{"published clips", "drafts", "upvotes", "Best Clipper", "switched off for clipping or for downloads"} {
		require.Contains(t, clips, named)
	}
	require.Contains(t, file.Settings, "EnableClips", "the Clips settings themselves are in the file")
	require.Contains(t, file.Settings, "AllowClipDownloads", "downloads' switch is a setting like any other")
	require.NotContains(t, settingsSecrets, "AllowClipDownloads", "and no secret")
}

// A backup taken without passwords must be safe to restore onto a server that
// has them: silently blanking the origin's Basic Auth would take the plugin
// off the air.
func TestABackupWithoutPasswordsLeavesTheInstalledOnesAlone(t *testing.T) {
	source, _ := settingsPlugin(t, fullConfig(), map[string]any{})

	file := export(t, source, "")
	require.NotContains(t, file.Settings, "BasicAuthPassword")
	require.NotContains(t, file.Settings, "MumblePassword")
	require.NotContains(t, file.Settings, "TMDBToken")
	require.NotContains(t, file.Settings, "TwitchClientSecret")
	require.NotContains(t, file.Settings, "SteamWebAPIKey")
	require.NotContains(t, file.Settings, "HytaleClientSecret")
	require.Equal(t, "the-twitch-client-id", file.Settings["TwitchClientID"], "a Client ID is public, so it travels")
	require.Equal(t, "the-hytale-client-id", file.Settings["HytaleClientID"], "Hytale's Client ID is not a secret either")
	require.Equal(t, []string{"BasicAuthPassword", "HytaleClientSecret", "MumblePassword", "SteamWebAPIKey", "TMDBToken", "TwitchClientSecret"}, file.Omitted)

	// The same file, restored onto a server whose passwords differ.
	installed := fullConfig()
	installed.BasicAuthPassword = "the-one-already-here"
	installed.MumblePassword = "and-this-one"
	installed.TMDBToken = "and-the-tmdb-one"
	installed.TwitchClientSecret = "and-the-twitch-one"
	installed.SteamWebAPIKey = "and-the-steam-one"
	installed.HytaleClientSecret = "and-the-hytale-one"
	installed.BannerText = "changed"

	target, saved := settingsPlugin(t, installed, map[string]any{})

	_, result := importFile(t, target, file)
	require.True(t, result.OK)
	require.Equal(t, []string{"BasicAuthPassword", "HytaleClientSecret", "MumblePassword", "SteamWebAPIKey", "TMDBToken", "TwitchClientSecret"}, result.Kept)

	require.Equal(t, "the-one-already-here", saved.at("BasicAuthPassword"))
	require.Equal(t, "and-this-one", saved.at("MumblePassword"))
	require.Equal(t, "and-the-tmdb-one", saved.at("TMDBToken"))
	require.Equal(t, "and-the-twitch-one", saved.at("TwitchClientSecret"))
	require.Equal(t, "and-the-steam-one", saved.at("SteamWebAPIKey"))
	require.Equal(t, "and-the-hytale-one", saved.at("HytaleClientSecret"))
	require.Equal(t, "", saved.at("BannerText"), "everything the file does carry is applied")
}

// With the box ticked the passwords travel, because otherwise a migration to a
// new server is not a migration.
func TestABackupWithPasswordsCarriesThem(t *testing.T) {
	source, _ := settingsPlugin(t, fullConfig(), map[string]any{})

	file := export(t, source, "?secrets=1")
	require.Equal(t, "the-basic-auth-password", file.Settings["BasicAuthPassword"])
	require.Equal(t, "the-mumble-password", file.Settings["MumblePassword"])
	require.Equal(t, "the-tmdb-token", file.Settings["TMDBToken"])
	require.Equal(t, "the-twitch-client-secret", file.Settings["TwitchClientSecret"])
	require.Equal(t, "the-steam-web-api-key", file.Settings["SteamWebAPIKey"])
	require.Equal(t, "the-hytale-client-secret", file.Settings["HytaleClientSecret"])
	require.Empty(t, file.Omitted)
}

// GetPluginConfig returns the SANITIZED configuration: every secret setting
// comes back as a row of asterisks. Reading the backup from there instead of
// from the struct would set the passwords to asterisks on the next import.
func TestAnImportNeverWritesAsterisksOverAPassword(t *testing.T) {
	stored := map[string]any{
		"BroadcastBoxURL":   "http://192.168.1.20:8080",
		"BasicAuthPassword": redactedSetting,
		"SettingFromLater":  "kept",
	}

	target, saved := settingsPlugin(t, fullConfig(), stored)

	_, result := importFile(t, target, settingsExport{
		Kind:     settingsExportKind,
		Version:  settingsExportVersion,
		Settings: map[string]any{"BannerText": "Villa"},
	})
	require.True(t, result.OK)

	require.Equal(t, "the-basic-auth-password", saved.at("BasicAuthPassword"))
	require.Equal(t, "kept", saved.at("SettingFromLater"),
		"a stored setting this build has no field for survives the round trip")
	require.Equal(t, "Villa", saved.at("BannerText"))
}

// SavePluginConfig replaces the whole map, so a value this build does not know
// about would be deleted by a careless import. One that has been redacted on
// the way in must not be written back as asterisks either.
func TestARedactedUnknownSettingIsNotSavedAsAsterisks(t *testing.T) {
	stored := map[string]any{"SecretFromLater": redactedSetting}

	target, saved := settingsPlugin(t, fullConfig(), stored)

	_, result := importFile(t, target, settingsExport{
		Kind:     settingsExportKind,
		Version:  settingsExportVersion,
		Settings: map[string]any{"BannerText": "Villa"},
	})
	require.True(t, result.OK)
	require.NotContains(t, *saved, strings.ToLower("SecretFromLater"))
}

// A backup is a matty-settings file, named for what it holds.
func TestABackupIsAMattySettingsFile(t *testing.T) {
	source, _ := settingsPlugin(t, fullConfig(), map[string]any{})

	for query, name := range map[string]string{"": "matty-settings.json", "?secrets=1": "matty-settings-with-passwords.json"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings"+query, nil)
		source.handleSettings(recorder, request, "uid-admin")
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, `attachment; filename="`+name+`"`, recorder.Header().Get("Content-Disposition"))

		var file settingsExport
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &file))
		require.Equal(t, "matty-settings", file.Kind)
		require.Equal(t, manifestID, file.PluginID)
	}
}

// A backup taken before 4.0.0, when the plugin was ceo.villa.villanet, is
// restored as one of this plugin's own, with nothing to warn of.
func TestABackupFromBeforeTheRenameIsRestored(t *testing.T) {
	target, saved := settingsPlugin(t, fullConfig(), map[string]any{})

	recorder, result := importFile(t, target, settingsExport{
		Kind:          "villanet-tv-settings",
		Version:       settingsExportVersion,
		PluginID:      "ceo.villa.villanet",
		PluginVersion: "3.8.0",
		Settings:      map[string]any{"BannerText": "Members only"},
	})
	require.True(t, result.OK, recorder.Body.String())
	require.Equal(t, "Members only", saved.at("BannerText"))
	require.NotContains(t, result.Warning, "came from plugin")
}

func TestAnImportRefusesAFileThatIsNotASettingsBackup(t *testing.T) {
	target, saved := settingsPlugin(t, fullConfig(), map[string]any{})

	// An airtime export, which is the neighbouring button on the same page.
	recorder, _ := importFile(t, target, map[string]any{
		"version": 1,
		"totals":  map[string]any{},
	})

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "not a Matty settings backup")
	require.Empty(t, *saved, "nothing was saved")
}

func TestAnImportRefusesANewerFileFormat(t *testing.T) {
	target, saved := settingsPlugin(t, fullConfig(), map[string]any{})

	recorder, _ := importFile(t, target, settingsExport{
		Kind:     settingsExportKind,
		Version:  settingsExportVersion + 1,
		Settings: map[string]any{"BannerText": "Villa"},
	})

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "newer version")
	require.Empty(t, *saved)
}

func TestAnImportRefusesAnEmptyBackup(t *testing.T) {
	target, saved := settingsPlugin(t, fullConfig(), map[string]any{})

	recorder, _ := importFile(t, target, settingsExport{
		Kind:    settingsExportKind,
		Version: settingsExportVersion,
	})

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Empty(t, *saved)
}

// LoadPluginConfiguration logs an unmarshal failure and returns nil, so a
// hand-edited file with a string where a number belongs would not fail — every
// setting would silently become its zero value. It is refused here instead,
// and the offending setting is named.
func TestAnImportRefusesASettingOfTheWrongType(t *testing.T) {
	target, saved := settingsPlugin(t, fullConfig(), map[string]any{})

	recorder, _ := importFile(t, target, settingsExport{
		Kind:     settingsExportKind,
		Version:  settingsExportVersion,
		Settings: map[string]any{"PollIntervalSeconds": "ten"},
	})

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "PollIntervalSeconds")
	require.Contains(t, recorder.Body.String(), "Nothing was changed")
	require.Empty(t, *saved)
}

func TestAnImportNamesSettingsItDidNotRecognise(t *testing.T) {
	target, saved := settingsPlugin(t, fullConfig(), map[string]any{})

	_, result := importFile(t, target, settingsExport{
		Kind:    settingsExportKind,
		Version: settingsExportVersion,
		Settings: map[string]any{
			"BannerText":      "Villa",
			"VoteToWatchNext": true,
			"ZZZRemovedLater": "gone",
		},
	})

	require.True(t, result.OK)
	require.Equal(t, []string{"VoteToWatchNext", "ZZZRemovedLater"}, result.Unknown)
	require.NotContains(t, *saved, strings.ToLower("VoteToWatchNext"),
		"a setting this build does not have is reported, not resurrected")
}

// "Who to ask" (AccessContact) was a setting until 3.1.0, when the lock stopped
// telling people whom to ask to be let in. A backup written before then still
// carries it: it restores as any other does, the key is named as one this
// version does not have, and nothing of it is written back. A backup made now
// does not carry it at all.
func TestABackupWithWhoToAskInItStillRestores(t *testing.T) {
	target, saved := settingsPlugin(t, fullConfig(), map[string]any{})

	recorder, result := importFile(t, target, settingsExport{
		Kind:          settingsExportKind,
		Version:       settingsExportVersion,
		PluginVersion: "3.0.0",
		Settings: map[string]any{
			"BannerText":    "Villa",
			"AccessContact": "@victor",
		},
	})

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.True(t, result.OK)
	require.Equal(t, []string{"AccessContact"}, result.Unknown)
	require.Equal(t, "Villa", saved.at("BannerText"))
	require.NotContains(t, *saved, "accesscontact")

	require.NotContains(t, export(t, target, "?secrets=1").Settings, "AccessContact")
}

// A hand-edited file, or one written by a build that spelled the key
// differently, still lands: Mattermost matches stored settings to struct fields
// without regard to case, so the import must too.
func TestAnImportMatchesSettingNamesWithoutRegardToCase(t *testing.T) {
	target, saved := settingsPlugin(t, fullConfig(), map[string]any{})

	_, result := importFile(t, target, settingsExport{
		Kind:     settingsExportKind,
		Version:  settingsExportVersion,
		Settings: map[string]any{"broadcastboxurl": "http://10.0.0.9:8080"},
	})

	require.True(t, result.OK)
	require.Empty(t, result.Unknown)
	require.Equal(t, "http://10.0.0.9:8080", saved.at("BroadcastBoxURL"))
}

func TestOnlyASystemAdminCanBackUpOrRestore(t *testing.T) {
	p, saved := settingsPlugin(t, fullConfig(), map[string]any{})

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(method, "/api/v1/admin/settings", strings.NewReader("{}"))

		p.handleSettings(recorder, request, "uid-user")
		require.Equal(t, http.StatusForbidden, recorder.Code, method)
	}

	require.Empty(t, *saved)
}

func TestABackupFromAnotherPluginIDIsRestoredWithANote(t *testing.T) {
	target, saved := settingsPlugin(t, fullConfig(), map[string]any{})

	_, result := importFile(t, target, settingsExport{
		Kind:     settingsExportKind,
		Version:  settingsExportVersion,
		PluginID: "com.villa.villanettv",
		Settings: map[string]any{"BannerText": "Villa"},
	})

	require.True(t, result.OK)
	require.Contains(t, result.Warning, "com.villa.villanettv")
	require.Equal(t, "Villa", saved.at("BannerText"))
}

func TestARestoreWithNothingToWatchSaysSo(t *testing.T) {
	target, _ := settingsPlugin(t, fullConfig(), map[string]any{})

	_, result := importFile(t, target, settingsExport{
		Kind:     settingsExportKind,
		Version:  settingsExportVersion,
		PluginID: manifestID,
		Settings: map[string]any{"BroadcastBoxURL": ""},
	})

	require.True(t, result.OK)
	require.Contains(t, result.Warning, "nothing to watch")
}

// The backup is built from the configuration struct, and the settings page is
// built from plugin.json. If the two ever disagree, a setting is either not
// backed up or not editable, and both are silent failures.
func TestTheBackupAndTheSettingsPageAgreeOnEverySetting(t *testing.T) {
	raw, err := os.ReadFile("../plugin.json")
	require.NoError(t, err)

	var manifest struct {
		SettingsSchema struct {
			Settings []struct {
				Key    string `json:"key"`
				Type   string `json:"type"`
				Secret bool   `json:"secret"`
			} `json:"settings"`
			Sections []struct {
				Key      string `json:"key"`
				Settings []struct {
					Key    string `json:"key"`
					Type   string `json:"type"`
					Secret bool   `json:"secret"`
				} `json:"settings"`
			} `json:"sections"`
		} `json:"settings_schema"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))

	backed := settingKeys()

	// Custom settings render a component. Some of them edit a configuration
	// value (the birthday list); the rest are buttons that edit nothing, and
	// there is nothing for the backup to carry.
	buttons := map[string]bool{}
	onThePage := map[string]bool{}

	for _, section := range manifest.SettingsSchema.Sections {
		require.NotEmpty(t, section.Settings, "section %q has no settings", section.Key)

		for _, setting := range section.Settings {
			lowered := strings.ToLower(setting.Key)

			if _, known := backed[lowered]; !known {
				require.Equal(t, "custom", setting.Type,
					"setting %q is on the settings page but is not a configuration field, so a backup would not carry it", setting.Key)
				buttons[lowered] = true

				continue
			}

			require.False(t, onThePage[lowered], "setting %q appears twice on the settings page", setting.Key)
			onThePage[lowered] = true

			// Whatever the manifest calls a secret, the backup must treat as one.
			if setting.Secret {
				require.Contains(t, settingsSecrets, backed[lowered],
					"setting %q is marked secret in plugin.json but the backup would export it in the clear", setting.Key)
			}
		}
	}

	require.Empty(t, manifest.SettingsSchema.Settings,
		"settings live in sections; a stray top-level one would not be checked here")

	for lowered, name := range backed {
		require.True(t, onThePage[lowered],
			"%s is a configuration field with no setting on the page, so nobody can edit what the backup carries", name)
	}
}

// The other half of that agreement: every password the backup holds back must
// be a setting Mattermost itself treats as a secret, or the console shows it in
// the clear and the support packet carries it.
func TestEverySecretSettingIsMarkedSecret(t *testing.T) {
	raw, err := os.ReadFile("../plugin.json")
	require.NoError(t, err)

	var manifest struct {
		SettingsSchema struct {
			Sections []struct {
				Settings []struct {
					Key    string `json:"key"`
					Secret bool   `json:"secret"`
				} `json:"settings"`
			} `json:"sections"`
		} `json:"settings_schema"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))

	marked := map[string]bool{}
	for _, section := range manifest.SettingsSchema.Sections {
		for _, setting := range section.Settings {
			if setting.Secret {
				marked[strings.ToLower(setting.Key)] = true
			}
		}
	}

	for _, key := range settingsSecrets {
		require.True(t, marked[strings.ToLower(key)],
			"%s holds a credential but is not marked \"secret\": true in plugin.json", key)
	}
}

// Every configuration field has to survive the marshal the backup is built
// from. An unexported field, or one of a type JSON cannot carry, would be
// dropped without a word.
func TestEveryConfigurationFieldSurvivesTheBackup(t *testing.T) {
	values, err := settingsMap(fullConfig())
	require.NoError(t, err)

	shape := reflect.TypeOf(configuration{})
	for i := 0; i < shape.NumField(); i++ {
		name := shape.Field(i).Name
		require.Contains(t, values, name, "%s is not carried by a backup", name)
	}

	require.Len(t, values, shape.NumField(), "a backup carries nothing but settings")
}

// The overlay matches setting names without regard to case, because that is how
// Mattermost matches them. The list of passwords "left as they are" has to
// match the same way, or a hand-written file with a lower-cased key would
// change the password AND be reported as having left it alone — which is the
// one thing that list must be trusted about.
func TestAPasswordCarriedInAnyCaseIsNotReportedAsKept(t *testing.T) {
	installed := fullConfig()
	installed.MumblePassword = "the-one-already-here"

	target, saved := settingsPlugin(t, installed, map[string]any{})

	_, result := importFile(t, target, settingsExport{
		Kind:    settingsExportKind,
		Version: settingsExportVersion,
		Settings: map[string]any{
			"mumblepassword": "from-the-file",
		},
	})

	require.True(t, result.OK)
	require.Equal(t, "from-the-file", saved.at("MumblePassword"), "the file's value is applied")
	require.Equal(t, []string{"BasicAuthPassword", "HytaleClientSecret", "SteamWebAPIKey", "TMDBToken", "TwitchClientSecret"}, result.Kept,
		"only the passwords the file really did not carry are reported as kept")
}

// The System Console addresses every plugin setting at the LOWERCASED key:
// custom_plugin_settings builds the path as
// "PluginSettings.Plugins.<id>." + setting.key.toLowerCase(), and that one path
// is both where it reads a value from and where it writes it back.
//
// An import that saved the configuration struct's own casing therefore looked
// like it worked and was not. The plugin kept running — LoadPluginConfiguration
// lowercases on read — while the settings page came up with every box blank and
// every switch off, directly under a panel saying the settings were restored.
//
// This is the assertion that would have caught it. Every other test here went
// through typedConfiguration or a case-insensitive read, which is exactly why
// none of them did.
func TestEverySavedKeyIsSpeltTheWayTheSettingsPageReadsIt(t *testing.T) {
	// A server configured through the console, so the stored keys are already
	// lowercase, plus one setting a later build added.
	stored := map[string]any{
		"broadcastboxurl":  "http://192.168.1.20:8080",
		"bannertext":       "Controlled access",
		"enablelivebadge":  true,
		"SettingFromLater": "kept",
	}

	target, saved := settingsPlugin(t, fullConfig(), stored)

	_, result := importFile(t, target, settingsExport{
		Kind:     settingsExportKind,
		Version:  settingsExportVersion,
		Settings: map[string]any{"BannerText": "Villa"},
	})
	require.True(t, result.OK)

	for key := range *saved {
		require.Equal(t, strings.ToLower(key), key,
			"%q is not where the settings page would look for it", key)
	}

	// And the page would find every one of them, at the path it builds.
	raw, err := os.ReadFile("../plugin.json")
	require.NoError(t, err)

	var manifest struct {
		SettingsSchema struct {
			Sections []struct {
				Settings []struct {
					Key  string `json:"key"`
					Type string `json:"type"`
				} `json:"settings"`
			} `json:"sections"`
		} `json:"settings_schema"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))

	backed := settingKeys()
	for _, section := range manifest.SettingsSchema.Sections {
		for _, setting := range section.Settings {
			lowered := strings.ToLower(setting.Key)
			if _, known := backed[lowered]; !known {
				continue
			}

			require.Contains(t, *saved, lowered,
				"the settings page reads %q at %q and would find nothing", setting.Key, lowered)
		}
	}

	require.Equal(t, "Villa", (*saved)["bannertext"])
	require.Equal(t, "kept", (*saved)["settingfromlater"],
		"a setting this build does not know is kept, and spelt where its own page will look")

	// Nothing is stored twice under two spellings: LoadPluginConfiguration
	// lowercases both into one slot and picks a winner by map iteration order,
	// which is to say at random on every configuration reload.
	require.NotContains(t, *saved, "BroadcastBoxURL")
	require.NotContains(t, *saved, "SettingFromLater")
}

// The console writes the sanitized value back when an admin saves the page
// without touching the password field, so the stored password can BE the
// 32-asterisk placeholder. A backup carrying that would restore it onto a
// healthy server as the real thing and take the plugin off the air.
func TestAsterisksNeverTravelAsAPassword(t *testing.T) {
	sanitized := fullConfig()
	sanitized.BasicAuthPassword = redactedSetting

	source, _ := settingsPlugin(t, sanitized, map[string]any{})

	file := export(t, source, "?secrets=1")
	require.NotContains(t, file.Settings, "BasicAuthPassword", "not written into the file")
	require.Contains(t, file.Omitted, "BasicAuthPassword")
	require.Equal(t, "the-mumble-password", file.Settings["MumblePassword"], "the real one still travels")

	// And a file from an older build that does carry them is refused the same way.
	target, saved := settingsPlugin(t, fullConfig(), map[string]any{})

	_, result := importFile(t, target, settingsExport{
		Kind:    settingsExportKind,
		Version: settingsExportVersion,
		Settings: map[string]any{
			"BasicAuthPassword": redactedSetting,
			"MumblePassword":    "a-real-one",
		},
	})

	require.True(t, result.OK)
	require.Equal(t, "the-basic-auth-password", saved.at("BasicAuthPassword"), "kept, not overwritten with asterisks")
	require.Equal(t, "a-real-one", saved.at("MumblePassword"))
	require.Equal(t, []string{"BasicAuthPassword", "HytaleClientSecret", "SteamWebAPIKey", "TMDBToken", "TwitchClientSecret"}, result.Kept)
}

// The Twitch application's two halves are pasted from its console page. A
// space either side is a copying slip, and asterisks are what a hidden secret
// looks like, never the secret: sending them would only be refused by Twitch.
func TestTwitchCredentialsAreNormalised(t *testing.T) {
	for _, tried := range []struct {
		id, secret         string
		wantID, wantSecret string
		set                bool
	}{
		{"  abc123  ", "\tsecret456 ", "abc123", "secret456", true},
		{"abc123", redactedSetting, "abc123", "", false},
		{"abc123", "", "abc123", "", false},
		{"", "secret456", "", "secret456", false},
		{"   ", "secret456", "", "secret456", false},
	} {
		cfg := &configuration{TwitchClientID: tried.id, TwitchClientSecret: tried.secret}
		cfg.normalize()

		require.Equal(t, tried.wantID, cfg.TwitchClientID, "%q", tried.id)
		require.Equal(t, tried.wantSecret, cfg.TwitchClientSecret, "%q", tried.secret)

		creds, set := cfg.twitchCredentials()
		require.Equal(t, tried.set, set, "%q %q", tried.id, tried.secret)
		if set {
			require.Equal(t, twitchCredentials{clientID: tried.wantID, secret: tried.wantSecret}, creds)
		}
	}
}

// Who has connected which Twitch account, who is blocked from it and their
// Twitch hours are Twitch's data about people, which VillaNET must be able to
// delete when they disconnect: a settings file never carries them, and says
// so. The two Twitch live switches are settings like any other.
func TestABackupLeavesTwitchConnectionsOut(t *testing.T) {
	found := false
	for _, line := range settingsNotIncluded {
		found = found || strings.Contains(line, "Twitch account")
	}
	require.True(t, found)

	keys := settingKeys()
	require.Contains(t, keys, "enabletwitchlive")
	require.Contains(t, keys, "twitchembed")
	require.NotContains(t, keys, "twitchsetup", "a component with no value")
}

// Who has connected which Steam or Hytale account, and who is blocked from
// connecting them, are people's own connections, which they can delete with
// one press (design/3.4.0.md §2.11): a settings file never carries them,
// says so, and a restore leaves the store alone. The four settings travel;
// the two components have no value to carry.
func TestABackupLeavesSteamAndHytaleConnectionsOut(t *testing.T) {
	found := false
	for _, line := range settingsNotIncluded {
		found = found || strings.Contains(line, "Steam or Hytale account")
	}
	require.True(t, found)

	keys := settingKeys()
	for _, setting := range []string{"enablesteam", "steamwebapikey", "enablehytale", "hytaleclientid", "hytaleclientsecret"} {
		require.Contains(t, keys, setting)
	}
	for _, store := range []string{kvSteamLinks, kvHytaleLinks, "steamsetup", "hytalesetup"} {
		require.NotContains(t, keys, strings.ToLower(store))
	}

	source, _ := settingsPlugin(t, fullConfig(), map[string]any{})
	file := export(t, source, "?secrets=1")
	for name := range file.Settings {
		require.NotContains(t, strings.ToLower(name), "links", "%s is not a setting", name)
	}

	// A hand-made file naming the documents restores nothing into them: the
	// plugin here has no store at all, so any read or write of one would
	// fail the test.
	target, saved := settingsPlugin(t, fullConfig(), map[string]any{})
	_, result := importFile(t, target, settingsExport{
		Kind:    settingsExportKind,
		Version: settingsExportVersion,
		Settings: map[string]any{
			kvSteamLinks:  `{"v":1,"users":{"uid-x":{"id":"76561197960287930","name":"Dave","at":1}}}`,
			kvHytaleLinks: `{"v":1,"users":{}}`,
			"BannerText":  "Villa",
		},
	})
	require.True(t, result.OK)
	require.ElementsMatch(t, []string{kvSteamLinks, kvHytaleLinks}, result.Unknown)
	require.NotContains(t, *saved, kvSteamLinks)
	require.NotContains(t, *saved, kvHytaleLinks)
	require.Equal(t, "Villa", saved.at("BannerText"))
}

// Asterisks are what Mattermost hands back for a secret it has hidden, never
// the secret itself, so a Steam key or a Hytale secret saved as them reads as
// unset, and nothing would send them to Steam or Hytale (design/3.4.0.md
// §2.10). Hytale's Client ID is not a secret, so it is only trimmed.
func TestARedactedSecretIsNeverTakenAsTheSecret(t *testing.T) {
	cfg := &configuration{
		EnableSteam:        true,
		SteamWebAPIKey:     redactedSetting,
		EnableHytale:       true,
		HytaleClientID:     "  the-hytale-client-id ",
		HytaleClientSecret: redactedSetting,
	}
	cfg.normalize()

	require.Empty(t, cfg.SteamWebAPIKey)
	require.Empty(t, cfg.HytaleClientSecret)
	require.Equal(t, "the-hytale-client-id", cfg.HytaleClientID)
	require.False(t, cfg.steamOn(), "no key is no Steam")
	require.False(t, cfg.hytaleOn(), "no secret is no Hytale")

	cfg = &configuration{EnableSteam: true, SteamWebAPIKey: " \tthe-key\n", EnableHytale: true, HytaleClientID: "id", HytaleClientSecret: " secret "}
	cfg.normalize()
	require.Equal(t, "the-key", cfg.SteamWebAPIKey, "a stray space either side is a copying slip")
	require.Equal(t, "secret", cfg.HytaleClientSecret)
	require.True(t, cfg.steamOn())
	require.True(t, cfg.hytaleOn())
}

// 3.2.0's stores are people's own choices, who watched whom, and people's
// own sounds: none goes in a settings backup, and the file says so, by name
// (design/3.2.0.md §2.5). The new settings are settings, and none of them is
// a secret. 3.2.1 keeps no list of fan opt-outs, so the file names none.
func TestSettingsNotIncludedNamesEveryStore(t *testing.T) {
	named := map[string]string{
		kvAwardPrefs:      "Who chose which awards to show by their name",
		kvFansTotals:      "Biggest fan and 24/7 channel hours: who watched whom",
		kvChanwatchTotals: "Biggest fan and 24/7 channel hours: who watched whom",
		kvSounds:          "Soundboard sounds and who is blocked from the soundboard",
		kvSoundPrefix:     "Soundboard sounds and who is blocked from the soundboard",
		kvSteamLinks:      "Who has connected which Steam or Hytale account",
		kvHytaleLinks:     "Who has connected which Steam or Hytale account",
		kvPushUserPrefix:  "Which phones get push notifications, and the push key",
		kvPushPeople:      "Which phones get push notifications, and the push key",
		kvPushKey:         "Which phones get push notifications, and the push key",
	}

	for key, phrase := range named {
		found := false
		for _, line := range settingsNotIncluded {
			found = found || strings.Contains(line, phrase)
		}
		require.True(t, found, "%s is in no line of what a backup leaves out", key)
	}

	p, _ := settingsPlugin(t, fullConfig(), nil)
	file := export(t, p, "")
	for _, phrase := range named {
		found := false
		for _, line := range file.NotIncluded {
			found = found || strings.Contains(line, phrase)
		}
		require.True(t, found, "the file itself says it leaves out: %s", phrase)
	}

	for _, setting := range []string{"EnableAchievements", "ChannelAchievements", "EnableBiggestFans", "EnableSoundboard", "SoundCooldownSeconds", "SoundsRegisteredOnly"} {
		require.Contains(t, file.Settings, setting, "a setting like any other")
		require.NotContains(t, settingsSecrets, setting)
	}
}
