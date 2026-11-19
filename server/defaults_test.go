package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The defaults plugin.json declares, read from the file itself: every section
// setting that has one.
func manifestSectionDefaults(t *testing.T) map[string]any {
	t.Helper()

	raw, err := os.ReadFile("../plugin.json")
	require.NoError(t, err)

	var manifest struct {
		SettingsSchema struct {
			Sections []struct {
				Settings []struct {
					Key     string `json:"key"`
					Default any    `json:"default"`
				} `json:"settings"`
			} `json:"sections"`
		} `json:"settings_schema"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))

	defaults := map[string]any{}
	for _, section := range manifest.SettingsSchema.Sections {
		for _, setting := range section.Settings {
			if setting.Default != nil {
				defaults[setting.Key] = setting.Default
			}
		}
	}

	return defaults
}

func TestTheSectionDefaultsMatchTheManifest(t *testing.T) {
	// Through JSON, so a number in the table and one read from plugin.json
	// compare as the same kind of number.
	encoded, err := json.Marshal(sectionDefaults)
	require.NoError(t, err)

	var table map[string]any
	require.NoError(t, json.Unmarshal(encoded, &table))

	require.Equal(t, manifestSectionDefaults(t), table)

	// The Clips group is there, as values its fields can hold. ClipsAdmin is
	// not: it is a custom setting, a component on the page with no value and
	// no field, so a default for it would land nowhere.
	require.Equal(t, true, table["EnableClips"])
	require.Equal(t, "120", table["ClipLengthSeconds"])
	require.EqualValues(t, 30, table["ClipRetentionDays"])
	require.EqualValues(t, 20, table["ClipStorageLimitGB"])
	require.NotContains(t, table, "ClipsAdmin")

	// And every one of them lands on a configuration field, as a value that
	// field can hold. One that did not would be dropped without a word, and
	// the setting would go on loading as its zero value.
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()

	var cfg configuration
	require.NoError(t, decoder.Decode(&cfg))
}

// A plugin on a Mattermost that applies no section defaults itself, as every
// release before 11.8 did (every one VillaNET supports does, which makes
// applySectionDefaults a no-op there; these tests pin that it still holds
// alone): LoadPluginConfiguration fills in what was saved and nothing else, the
// unsanitized configuration holds exactly that, and GetPluginConfig hands back
// the same keys with the secrets hidden. Nothing is configured yet, as on a
// plugin start.
func olderServerPlugin(t *testing.T, saved map[string]any) (*Plugin, *savedConfig) {
	t.Helper()

	return olderServerPluginSanitized(t, saved, nil)
}

// The same, with the sanitized copy given outright rather than made from what
// was saved: nil makes it from what was saved.
func olderServerPluginSanitized(t *testing.T, saved map[string]any, given map[string]any) (*Plugin, *savedConfig) {
	t.Helper()

	sanitized := map[string]any{}
	for key, value := range saved {
		sanitized[key] = value
		for _, secret := range settingsSecrets {
			if strings.EqualFold(key, secret) {
				sanitized[key] = redactedSetting
			}
		}
	}

	if given != nil {
		sanitized = given
	}

	p, written := settingsPlugin(t, nil, sanitized)

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("GetUnsanitizedConfig").Return(&model.Config{PluginSettings: model.PluginSettings{
		Plugins: map[string]map[string]any{manifestID: saved},
	}})
	api.On("LoadPluginConfiguration", mock.Anything).Run(func(args mock.Arguments) {
		encoded, err := json.Marshal(saved)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(encoded, args.Get(0)))
	}).Return(nil)
	// The Site URL, which the app check's rules are read with on every load.
	api.On("GetConfig").Return(&model.Config{}).Maybe()

	return p, written
}

// Saved under 2.6.3, which had no LivePreviews, on a page where nobody ever
// touched the live badge, the leaderboard or the Mumble name matching. The
// System Console shows every one of those as on, so that is what runs.
func TestASettingNeverSavedTakesItsManifestDefault(t *testing.T) {
	p, _ := olderServerPlugin(t, map[string]any{
		"broadcastboxurl":     "http://bb.lan:8080",
		"pollintervalseconds": 10,
		"mumblepassword":      "the-mumble-password",
	})
	require.NoError(t, p.OnConfigurationChange())

	cfg := p.getConfiguration()
	require.True(t, cfg.LivePreviews)
	require.True(t, cfg.ShowWatchers)
	require.True(t, cfg.EnableLiveBadge)
	require.True(t, cfg.EnableLeaderboard)

	// New in 2.10.0, so never saved anywhere yet: on, as the page shows it.
	require.True(t, cfg.TopWatchers)
	require.True(t, cfg.VoiceMatchUsernames)
	require.True(t, cfg.BirthdayBadge)
	require.Equal(t, 9, cfg.BirthdayHour)
	require.Equal(t, "Matty", cfg.Name)
	require.Equal(t, "occasional", cfg.NoSignalGhost)

	// What was saved is what runs, and the password is the real one, not the
	// asterisks GetPluginConfig shows in its place.
	require.Equal(t, "http://bb.lan:8080", cfg.BroadcastBoxURL)
	require.Equal(t, 10, cfg.PollIntervalSeconds)
	require.Equal(t, "the-mumble-password", cfg.MumblePassword)
}

// A default fills in a setting that was never saved, and nothing else: not a
// switch saved off, not a text saved blank on purpose, whatever the casing it
// was saved under.
func TestASavedSettingIsNeverOverwrittenByItsDefault(t *testing.T) {
	p, _ := olderServerPlugin(t, map[string]any{
		"livepreviews":       false,
		"EnableLeaderboard":  false,
		"bannertext":         "",
		"livestatustext":     "On the telly",
		"alwaysonstreamkeys": "",
		"birthdayhour":       0,
		"name":               "",
	})
	require.NoError(t, p.OnConfigurationChange())

	cfg := p.getConfiguration()
	require.False(t, cfg.LivePreviews)
	require.False(t, cfg.EnableLeaderboard, "saved in the struct's own casing, as an old import wrote it")
	require.Empty(t, cfg.BannerText, "blank is how the banner is hidden")
	require.Equal(t, "On the telly", cfg.LiveStatusText)
	require.Empty(t, cfg.AlwaysOnStreamKeys)
	require.Zero(t, cfg.BirthdayHour)
	require.Empty(t, cfg.Name, "a Name saved blank stays blank")
	require.Equal(t, productName, p.brandName(), "and the server is then called Matty")

	// Everything left unsaved still takes its default.
	require.True(t, cfg.EnableLiveBadge)
	require.Equal(t, "matty-bot", cfg.BotUsername)
}

// The settings backup reads the configuration the plugin runs on, so it
// carries the defaults that apply. A backup from before a setting existed,
// restored, then stores that setting's default rather than pinning it off.
func TestTheSettingsBackupCarriesTheDefaultsThatApply(t *testing.T) {
	p, written := olderServerPlugin(t, map[string]any{"broadcastboxurl": "http://bb.lan:8080"})
	require.NoError(t, p.OnConfigurationChange())

	file := export(t, p, "")
	require.Equal(t, true, file.Settings["LivePreviews"])
	require.Equal(t, "http://bb.lan:8080", file.Settings["BroadcastBoxURL"])

	delete(file.Settings, "LivePreviews")
	file.PluginVersion = "2.6.3"

	recorder, result := importFile(t, p, file)
	require.True(t, result.OK, recorder.Body.String())
	require.Equal(t, true, written.at("LivePreviews"))
	require.Equal(t, "http://bb.lan:8080", written.at("BroadcastBoxURL"))
}

// GetPluginConfig comes back EMPTY when Mattermost cannot find the plugins'
// manifests on disk while sanitizing. Were it the list of what has been saved,
// every saved setting would look unsaved and be overwritten with its default.
// It is not: which settings are saved is read from the unsanitized
// configuration, the same map the settings themselves are loaded from.
func TestAnEmptySanitizedConfigurationOverwritesNothing(t *testing.T) {
	p, _ := olderServerPluginSanitized(t, map[string]any{
		"broadcastboxurl":    "http://bb.lan:8080",
		"mumblepassword":     "the-mumble-password",
		"enablevoice":        true,
		"alwaysonstreamkeys": "lofi-radio",
		"livepreviews":       false,
	}, map[string]any{})
	require.NoError(t, p.OnConfigurationChange())

	cfg := p.getConfiguration()
	require.Equal(t, "http://bb.lan:8080", cfg.BroadcastBoxURL)
	require.Equal(t, "the-mumble-password", cfg.MumblePassword)
	require.True(t, cfg.EnableVoice)
	require.Equal(t, "lofi-radio", cfg.AlwaysOnStreamKeys)
	require.False(t, cfg.LivePreviews)

	// Only what really was never saved takes its default.
	require.True(t, cfg.EnableLiveBadge)
}

// A configuration saved before 3.1.0 still holds "Who to ask", which went with
// the lock's line that said it. It loads as before, the key ignored as
// Mattermost's own load ignores any key with no field (LoadPluginConfiguration
// in server/channels/app/plugin_api.go at v11.11.1 is a plain json.Unmarshal),
// and nobody is sent it.
func TestAConfigurationSavedWithWhoToAskStillLoads(t *testing.T) {
	p, _ := olderServerPlugin(t, map[string]any{
		"broadcastboxurl": "http://bb.lan:8080",
		"accesscontact":   "@victor",
	})
	require.NoError(t, p.OnConfigurationChange())

	cfg := p.getConfiguration()
	require.Equal(t, "http://bb.lan:8080", cfg.BroadcastBoxURL)
	require.True(t, cfg.LivePreviews, "everything never saved still takes its default")
	require.NotContains(t, sectionDefaults, "AccessContact")
	require.NotContains(t, export(t, p, "").Settings, "AccessContact")
}

// No configuration to read at all: nothing is given a default, and the
// settings run exactly as loaded.
func TestNoConfigurationMeansNoDefaults(t *testing.T) {
	p, _ := settingsPlugin(t, nil, map[string]any{})

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("GetUnsanitizedConfig").Return(nil)
	api.On("LoadPluginConfiguration", mock.Anything).Run(func(args mock.Arguments) {
		encoded, err := json.Marshal(map[string]any{"broadcastboxurl": "http://bb.lan:8080"})
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(encoded, args.Get(0)))
	}).Return(nil)
	// The Site URL, which the app check's rules are read with on every load.
	api.On("GetConfig").Return(&model.Config{}).Maybe()

	require.NoError(t, p.OnConfigurationChange())

	cfg := p.getConfiguration()
	require.Equal(t, "http://bb.lan:8080", cfg.BroadcastBoxURL)
	require.False(t, cfg.LivePreviews)
	require.False(t, cfg.EnableLiveBadge)
}

// Broadcast Box over plain http on another machine is warned of in the log,
// once a change of the address and with nothing but its host, and nothing
// else about it changes: the address is still the one run on.
func TestPlainHTTPToBroadcastBoxIsWarnedOfOnce(t *testing.T) {
	saved := map[string]any{"broadcastboxurl": "http://bb.lan:8080"}
	p, _ := olderServerPlugin(t, saved)
	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	require.NoError(t, p.OnConfigurationChange())
	require.Equal(t, "http://bb.lan:8080", p.getConfiguration().BroadcastBoxURL)
	warned := logged(api, "LogWarn", plainBroadcastBoxWarning)
	require.Len(t, warned, 1)
	require.Equal(t, mock.Arguments{plainBroadcastBoxWarning, "host", "bb.lan:8080"}, warned[0])

	// Saving anything else says nothing again.
	saved["pollintervalseconds"] = 20
	require.NoError(t, p.OnConfigurationChange())
	require.Len(t, logged(api, "LogWarn", plainBroadcastBoxWarning), 1)

	// Neither does an address that is safe, and another plain one is told.
	for _, address := range []string{"https://bb.example.com", "http://127.0.0.1:7788", "http://localhost:8080", "http://[::1]:8080", "http://bb.example.com"} {
		saved["broadcastboxurl"] = address
		require.NoError(t, p.OnConfigurationChange())
	}
	warned = logged(api, "LogWarn", plainBroadcastBoxWarning)
	require.Len(t, warned, 2)
	require.Equal(t, "bb.example.com", warned[1][2])
}

func TestPlainHTTPElsewhere(t *testing.T) {
	for address, want := range map[string]string{
		"http://192.168.1.20:8080": "192.168.1.20:8080",
		"http://bb.lan":            "bb.lan",
		"http://10.0.0.2:8080/bb":  "10.0.0.2:8080",
		"https://192.168.1.20":     "",
		"http://127.0.0.1:7788":    "",
		"http://127.8.0.1":         "",
		"http://LOCALHOST:8080":    "",
		"http://bb.localhost":      "",
		"http://[::1]:8080":        "",
		"":                         "",
		"not a url":                "",
	} {
		host, plain := plainHTTPElsewhere(address)
		require.Equal(t, want, host, address)
		require.Equal(t, want != "", plain, address)
	}
}

// A fresh server ships nobody's particulars: no Broadcast Box address, no
// banner, no 24/7 channel, no theme. What a community calls itself, its
// address and its channels are its admin's to type in.
func TestAFreshServerShipsNobodysParticulars(t *testing.T) {
	for _, key := range []string{
		"BroadcastBoxURL", "PublicSiteURL", "BannerText", "BannerColor", "AlwaysOnStreamKeys",
		"StreamNameOverrides", "StreamNotes", "ChannelAchievements", "Theme",
	} {
		require.Equal(t, "", sectionDefaults[key], key)
	}

	require.Equal(t, "Matty", sectionDefaults["Name"])
	require.Equal(t, "Matty", sectionDefaults["MumbleUsername"])
	require.Equal(t, "matty-bot", sectionDefaults["BotUsername"])
	require.Equal(t, "Matty", sectionDefaults["BotDisplayName"])
	require.Equal(t, defaultBotUsername, sectionDefaults["BotUsername"])
	require.Equal(t, defaultBotDisplayName, sectionDefaults["BotDisplayName"])
	require.Equal(t, defaultMumbleUsername, sectionDefaults["MumbleUsername"])

	raw, err := os.ReadFile("../plugin.json")
	require.NoError(t, err)
	for _, owned := range []string{"villanet", "192.168.0.", "villa.ceo", "chicago", "c8102e", "watts", "controlled access"} {
		require.NotContains(t, strings.ToLower(string(raw)), owned)
	}
}

// A first start with nothing saved is quiet: no Broadcast Box to ask, so
// nothing about one is warned of, and nothing at all is logged as a notice.
func TestAFreshServerStartsQuietly(t *testing.T) {
	p, _ := olderServerPlugin(t, map[string]any{})
	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	require.NoError(t, p.OnConfigurationChange())

	cfg := p.getConfiguration()
	require.Empty(t, cfg.BroadcastBoxURL)
	require.Error(t, cfg.validate(), "not set up, so the poller asks nobody")
	require.Empty(t, logged(api, "LogWarn", plainBroadcastBoxWarning))
	for _, call := range api.Calls {
		require.NotContains(t, []string{"LogInfo", "LogWarn", "LogError"}, call.Method, "nothing to say on a fresh start")
	}
	require.Equal(t, "Matty", p.brandName())
	require.Empty(t, cfg.BannerColor, "the theme's own accent")
}

// The Name setting: trimmed, at most 32 characters, and blank means Matty.
func TestTheNameIsTrimmedCutAndBlankMeansMatty(t *testing.T) {
	for given, want := range map[string]string{
		"  Alice TV  ":                       "Alice TV",
		"":                                   "",
		"   ":                                "",
		strings.Repeat("é", 40):              strings.Repeat("é", 32),
		"Carol's " + strings.Repeat("x", 40): "Carol's " + strings.Repeat("x", 24),

		// Nothing that hides or reorders text, and one line.
		"\u200b":                    "",
		"\u200e\ufeff \u2066\u200d": "",
		"Acme\u202eTV":              "AcmeTV",
		"Acme\u200bTV\u200f":        "AcmeTV",
		"Acme\nTV":                  "Acme TV",
		"Acme\t\r\n  TV\x00":        "Acme TV",

		// Emoji are kept whole, and the cut never leaves half of one.
		"Erin's 👩\u200d💻 TV":                 "Erin's 👩\u200d💻 TV",
		"🏳\ufe0f\u200d🌈 Radio":               "🏳\ufe0f\u200d🌈 Radio",
		strings.Repeat("x", 31) + "👩\u200d💻": strings.Repeat("x", 31) + "👩",
		strings.Repeat("x", 30) + "👩\u200d💻": strings.Repeat("x", 30) + "👩",
		strings.Repeat("x", 30) + "🇬🇧":       strings.Repeat("x", 30) + "🇬🇧",
		strings.Repeat("x", 31) + "🇬🇧":       strings.Repeat("x", 31),
		strings.Repeat("x", 29) + "🇫🇷🇬🇧":     strings.Repeat("x", 29) + "🇫🇷",

		// Tag characters are dropped, as in a Mattermost display name: a
		// subdivision flag is left as the black flag it is built on.
		"\U0001F3F4\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F Radio": "\U0001F3F4 Radio",
	} {
		cfg := &configuration{Name: given}
		cfg.normalize()
		require.Equal(t, want, cfg.Name, given)

		p := &Plugin{}
		p.setConfiguration(cfg)
		if want == "" {
			require.Equal(t, "Matty", p.brandName())
		} else {
			require.Equal(t, want, p.brandName())
		}
	}

	// Before the first configuration is loaded.
	require.Equal(t, "Matty", (&Plugin{}).brandName())
}

// An empty banner colour means each client's theme accent; a colour that is
// not one is emptied too, rather than drawn as nothing.
func TestAnEmptyBannerColourStaysEmpty(t *testing.T) {
	for given, want := range map[string]string{
		"":         "",
		"  ":       "",
		"red":      "",
		"#12345":   "",
		"#1C58D9":  "#1C58D9",
		" #1c58d9": "#1c58d9",
	} {
		cfg := &configuration{BannerColor: given}
		cfg.normalize()
		require.Equal(t, want, cfg.BannerColor, given)
	}
}

// The example in an empty Names that differ box is made up, as the lab's
// people are. A real Mumble name or username put back fails here.
func TestTheNamesThatDifferExampleIsMadeUp(t *testing.T) {
	raw, err := os.ReadFile("../plugin.json")
	require.NoError(t, err)

	var manifest struct {
		SettingsSchema struct {
			Sections []struct {
				Settings []struct {
					Key         string `json:"key"`
					Placeholder string `json:"placeholder"`
					Default     any    `json:"default"`
				} `json:"settings"`
			} `json:"sections"`
		} `json:"settings_schema"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))

	madeUp := map[string]bool{"alice": true, "bob": true, "carol": true, "dave": true, "erin": true}

	found := false
	for _, section := range manifest.SettingsSchema.Sections {
		for _, setting := range section.Settings {
			if setting.Key != "VoiceNameMap" {
				continue
			}
			found = true

			require.Equal(t, "", setting.Default, "Names that differ ships empty")

			cfg := &configuration{VoiceNameMap: setting.Placeholder}
			pairs := cfg.voiceNameMap()
			require.Len(t, pairs, 1, "the example is one line in the setting's own shape")
			for mumble, username := range pairs {
				require.True(t, madeUp[strings.Trim(mumble, "_.-0123456789")], "the example's Mumble name is not one of the made-up people")
				require.True(t, madeUp[username], "the example's username is not one of the made-up people")
				require.NotEqual(t, mumble, username, "the example shows two names that differ")
			}
		}
	}
	require.True(t, found)
}
