package main

import (
	"encoding/json"
	"strings"
)

// sectionDefaults are the defaults plugin.json declares, by setting key: every
// setting on the page that has one, and nothing else.
//
// They were added because Mattermost before 11.8 never applied them: its
// LoadPluginConfiguration seeded defaults from settings_schema.settings alone,
// and every setting on this page lives in settings_schema.sections, so a
// setting nobody had saved loaded as its zero value while the System Console
// showed the manifest's default ("Show live preview pictures" read as on and
// was off). Mattermost 11.8 seeds the sections too (mattermost/mattermost
// dc7241941e; LoadPluginConfiguration in server/channels/app/plugin_api.go at
// v11.11.1), and this plugin needs 11.11, so on every server it supports every
// key has a value already and applying these changes nothing.
//
// Kept all the same: it costs one read of the saved settings per change, and
// taking it out would change how settings load and which tests hold them,
// which moving the supported version is no occasion for.
//
// Kept by hand, and TestTheSectionDefaultsMatchTheManifest holds it to
// plugin.json: the server is built without the manifest beside it, and a file
// outside the package cannot be embedded.
var sectionDefaults = map[string]any{
	// Broadcast Box
	"BroadcastBoxURL":     "",
	"BasicAuthUsername":   "",
	"BasicAuthPassword":   "",
	"PollIntervalSeconds": 15,
	"OfflineGraceChecks":  2,
	"PublicSiteURL":       "",

	// Access has none: AccessList is a component with no value, and it is
	// the section's only setting since "Who to ask" went in 3.1.0.

	// App check
	"AppCheck":             "report",
	"AppCheckCertificates": "",
	"AppCheckBootKeys":     "",
	"MinimumAppVersion":    "",

	// Updates. UpdateStatus is a component with no value, so it has no
	// default.
	"EnableUpdates": false,

	// Push notifications. PushStatus is a component with no value, so it
	// has no default.
	"EnablePush":       false,
	"PushAllowedHosts": "",
	"PushOnlyListed":   false,
	"PushContact":      "",

	// Panel
	"OpenPanel":       "never",
	"BannerText":      "",
	"BannerColor":     "",
	"BannerPlacement": "both",
	"LivePreviews":    true,
	"ShowWatchers":    true,

	// Streams
	"AlwaysOnStreamKeys":  "",
	"StreamNameOverrides": "",
	"StreamNotes":         "",
	"AutoLinkByUsername":  false,

	// Name and artwork
	"Name":          "Matty",
	"NoSignalGhost": "occasional",

	// Live badge
	"EnableLiveBadge": true,
	"LiveEmoji":       "red_circle",
	"LiveStatusText":  "Live now",

	// Airtime and awards
	"EnableLeaderboard":   true,
	"TopWatchers":         true,
	"EnableAchievements":  true,
	"ChannelAchievements": "",
	"EnableBiggestFans":   true,

	// Clips. ClipsAdmin is a component with no value, so it has no default.
	"EnableClips":        true,
	"ClipLengthSeconds":  "120",
	"ClipRetentionDays":  30,
	"ClipStorageLimitGB": 20,
	"AllowClipDownloads": false,

	// Birthdays
	"EnableBirthdays": false,
	"Birthdays":       "",
	"BirthdayChannel": "",
	"BirthdayZone":    "",
	"BirthdayHour":    9,
	"BirthdayBadge":   true,
	"BirthdayMessage": `# 🎂 Happy birthday, {full_name}! 🎉
@{username} turns **{age}** today.`,
	"BirthdayKidMessage": `# 🎂 Happy birthday, {full_name}! 🎉
{parents}'s kid turns **{age}** today.`,

	// Theme
	"EnableTheme":     false,
	"ThemeApply":      "new",
	"Theme":           "",
	"StyleCodeBlocks": false,

	// Movies and games
	"MediaLookup": false,
	"TMDBToken":   "",

	// Twitch. TwitchSetup is a component with no value, so it has no default.
	"TwitchClientID":     "",
	"TwitchClientSecret": "",
	"EnableTwitchLive":   false,
	"TwitchEmbed":        false,

	// Steam and Hytale. The key, the Client ID and the Client Secret have no
	// default on the page, and SteamSetup and HytaleSetup are components
	// with no value.
	"EnableSteam":  false,
	"EnableHytale": false,

	// Mumble
	"EnableVoice":         false,
	"MumbleServer":        "",
	"MumbleUsername":      "Matty",
	"MumblePassword":      "",
	"VoiceHiddenNames":    "Mattermost",
	"VoiceMatchUsernames": true,
	"VoiceNameMap":        "",
	"VoiceChannelsShown":  "",

	// Talking from Matty. VoiceTalkStatus is a component with no value, so
	// it has no default.
	"EnableVoiceTalk":   false,
	"VoiceTalkPort":     8445,
	"VoiceTalkAddress":  "",
	"VoiceTalkNoBudget": false,

	// Soundboard. SoundsAdmin is a component with no value, so it has no
	// default.
	"EnableSoundboard":     false,
	"SoundCooldownSeconds": 10,
	"SoundsRegisteredOnly": false,

	// Mumble chat bridge
	"EnableChatBridge": false,
	"BridgeChannel":    "",
	"BridgeIgnore":     "",

	// Bot account
	"BotUsername":    "matty-bot",
	"BotDisplayName": "Matty",
	"BotDescription": "Relays chat between Mumble and Mattermost, and posts birthdays.",

	// Troubleshooting
	"DebugLogSDP": false,
}

// applySectionDefaults gives every setting that has never been saved the
// default plugin.json declares for it, which Mattermost 11.8 and later, and so
// every server this plugin supports, already do in LoadPluginConfiguration.
//
// stored is the plugin's saved settings map (see savedPluginSettings), and
// only its KEYS are read. Compared lowercased, because Mattermost stores and
// matches setting keys without regard to case.
//
// The defaults go through JSON into cfg, exactly as a saved value would, so a
// setting that is absent is filled in and nothing that is present is touched.
func applySectionDefaults(cfg *configuration, stored map[string]any) error {
	saved := make(map[string]bool, len(stored))
	for key := range stored {
		saved[strings.ToLower(key)] = true
	}

	unsaved := map[string]any{}
	for key, value := range sectionDefaults {
		if !saved[strings.ToLower(key)] {
			unsaved[key] = value
		}
	}

	if len(unsaved) == 0 {
		return nil
	}

	encoded, err := json.Marshal(unsaved)
	if err != nil {
		return err
	}

	return json.Unmarshal(encoded, cfg)
}

// savedPluginSettings is this plugin's stored settings map exactly as
// LoadPluginConfiguration reads it: from the unsanitized configuration.
//
// Not GetPluginConfig. That returns the SANITIZED configuration, and
// sanitizing scans the plugins folder on disk for every plugin's manifest; if
// the scan fails — or cannot read this plugin's plugin.json — it drops the
// settings of every plugin it has no manifest for and returns an empty map.
// Every saved setting would then look unsaved, and be overwritten with its
// default: the Broadcast Box address, the passwords, every switch. The
// unsanitized configuration depends on nothing but the configuration itself.
//
// false when there is no configuration to read at all, in which case nothing
// is given a default.
func (p *Plugin) savedPluginSettings() (map[string]any, bool) {
	config := p.API.GetUnsanitizedConfig()
	if config == nil {
		return nil, false
	}

	return config.PluginSettings.Plugins[manifestID], true
}
