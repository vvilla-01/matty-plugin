package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mattermost/mattermost/server/public/model"

	"github.com/vvilla-01/matty-plugin/server/appcheck"
)

// configuration mirrors the settings_schema in plugin.json. Field names must
// match the setting keys; Mattermost unmarshals the saved settings into this
// struct as JSON, case-insensitively.
type configuration struct {
	// Name is what people on this server see the plugin called: the panel's
	// title, the App Bar button and every sentence that names it. Cleaned and
	// cut to maxNameRunes by normalize (cleanName); blank means productName
	// (brandName).
	Name string

	BroadcastBoxURL     string
	BasicAuthUsername   string
	BasicAuthPassword   string
	PollIntervalSeconds int
	EnableLiveBadge     bool
	LiveEmoji           string
	LiveStatusText      string
	AutoLinkByUsername  bool
	StreamNameOverrides string
	StreamNotes         string
	EnableVoice         bool
	MumbleServer        string
	MumbleUsername      string
	MumblePassword      string
	VoiceHiddenNames    string
	VoiceNameMap        string
	VoiceMatchUsernames bool
	EnableChatBridge    bool
	BridgeChannel       string
	BridgeIgnore        string
	BotUsername         string
	BotDisplayName      string
	BotDescription      string
	EnableBirthdays     bool
	BirthdayChannel     string
	BirthdayZone        string
	BirthdayHour        int
	Birthdays           string
	BirthdayMessage     string
	BirthdayKidMessage  string
	BirthdayBadge       bool
	EnableTheme         bool
	Theme               string
	ThemeApply          string
	OpenPanel           string
	NoSignalGhost       string
	PublicSiteURL       string
	AlwaysOnStreamKeys  string
	LivePreviews        bool
	ShowWatchers        bool
	OfflineGraceChecks  int
	StyleCodeBlocks     bool
	MediaLookup         bool
	TMDBToken           string
	TwitchClientID      string
	TwitchClientSecret  string
	EnableTwitchLive    bool
	TwitchEmbed         bool
	DebugLogSDP         bool
	EnableLeaderboard   bool
	TopWatchers         bool

	// 3.2.0: achievements, the 24/7 channels that have them, and biggest
	// fans, all in the Airtime and awards section; and the soundboard,
	// with its own section after Mumble.
	EnableAchievements   bool
	ChannelAchievements  string
	EnableBiggestFans    bool
	EnableSoundboard     bool
	SoundCooldownSeconds int
	SoundsRegisteredOnly bool

	BannerText         string
	BannerColor        string
	BannerPlacement    string
	EnableClips        bool
	ClipLengthSeconds  string
	ClipRetentionDays  int
	ClipStorageLimitGB int

	// 3.2.1: whether linked people may download clips (design/3.2.1.md
	// §1). Off unless an admin switches it on; a streamer can still turn
	// downloads of their own stream's clips off (clips_download.go).
	AllowClipDownloads bool

	// 3.4.0: Steam and Hytale connections (design/3.4.0.md §2.10), each
	// off unless an admin switches it on. The Steam Web API key and the
	// Hytale client secret are secrets: masked on the settings page, never
	// sent to a browser, and left out of backups unless asked for.
	EnableSteam        bool
	SteamWebAPIKey     string
	EnableHytale       bool
	HytaleClientID     string
	HytaleClientSecret string

	// 4.0.0: the app check (app_check.go). Whether Matty is asked to prove
	// itself with Android's key attestation, and what is done with a Matty
	// that fails: "off", "report" or "require". Then more signing
	// certificates and more verified-boot keys to accept, one SHA-256 digest
	// per line.
	AppCheck             string
	AppCheckCertificates string
	AppCheckBootKeys     string

	// 4.1.0: push notifications through UnifiedPush (push.go). Whether
	// Matty may register a push address and this server sends there; the
	// push servers on the admin's own network, one host or host:port a
	// line (normalised, a line that is not one left out); whether only
	// those may be used; and the contact push servers are given.
	EnablePush       bool
	PushAllowedHosts string
	PushOnlyListed   bool
	PushContact      string

	// 4.3.0: talking from Matty (voice_rtc.go, voice_api.go). Whether
	// people may join the voice server's channels from Matty through this
	// server; the one UDP port phones reach it on; the addresses phones are
	// given, one a line, blank for the host of Mattermost's Site URL; and
	// whether the voice server's autoban is off, so that the plugin's own
	// budget of connections to it refuses nobody.
	EnableVoiceTalk   bool
	VoiceTalkPort     int
	VoiceTalkAddress  string
	VoiceTalkNoBudget bool

	// No AccessContact any more. "Who to ask" fed only the lock screen's
	// line telling people with no token whom to ask to be let in, and in
	// 3.1.0 the lock stopped saying that anybody can be (Victor: "Don't
	// advertise that"). A configuration saved before then still holds the
	// key; Mattermost's load ignores a key with no field, and the settings
	// restore lists it as a setting this version does not have.
}

// alwaysOnSet lists the stream keys that run unattended around the clock. They
// are shown like any other stream but are sorted last, tagged in the UI, and
// never move anyone's custom status.
func (c *configuration) alwaysOnSet() map[string]bool {
	keys := map[string]bool{}

	for _, field := range strings.FieldsFunc(c.AlwaysOnStreamKeys, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	}) {
		if key := normalizeKey(field); key != "" {
			keys[key] = true
		}
	}

	return keys
}

// demotedFromAlwaysOn reports whether any key has stopped being always-on.
// Such a stream has no session mark, so without a resynchronisation the next
// poll would treat its whole running session as time to be credited.
func demotedFromAlwaysOn(previous, current *configuration) bool {
	before := previous.alwaysOnSet()
	if len(before) == 0 {
		return false
	}

	after := current.alwaysOnSet()
	for key := range before {
		if !after[key] {
			return true
		}
	}

	return false
}

// keyedLines parses the "streamKey = something" blocks an admin can type, for
// the two settings that take that shape. Keys are lowercased so lookups match
// normalizeKey, and a # comments a line out.
func keyedLines(setting string) map[string]string {
	values := map[string]string{}

	for _, line := range strings.Split(setting, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		separator := strings.IndexAny(line, "=:")
		if separator <= 0 {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(line[:separator]))
		value := strings.TrimSpace(line[separator+1:])
		if key != "" && value != "" {
			values[key] = value
		}
	}

	return values
}

// nameOverrides labels always-on channels that no Mattermost account owns.
func (c *configuration) nameOverrides() map[string]string {
	return keyedLines(c.StreamNameOverrides)
}

// sameMumbleConnection reports whether two configurations would dial the same
// place, as the same person, for the same reasons.
//
// The connection is only rebuilt when one of these changes. Everything else an
// admin might save — a banner colour, a stream name, the birthday hour — used
// to drop it and dial again, which loses whatever was said in the meantime and
// makes the Save button take as long as a connect to a machine that is off.
// How often the No Signal screen catches a ghost of a picture. Anything not
// recognised is the default rather than off: a typo in a dropdown value should
// not quietly remove a feature nobody asked to remove.
const (
	ghostOff        = "off"
	ghostRare       = "rare"
	ghostOccasional = "occasional"
	ghostOften      = "often"
)

func (c *configuration) noSignalGhost() string {
	switch strings.ToLower(strings.TrimSpace(c.NoSignalGhost)) {
	case ghostOff:
		return ghostOff
	case ghostRare:
		return ghostRare
	case ghostOften:
		return ghostOften
	default:
		return ghostOccasional
	}
}

// sameBotIdentity reports whether two configurations would call the bot the
// same thing. Renaming it is a write to Mattermost, so it is only done when the
// name has actually changed rather than on every save.
func sameBotIdentity(a, b *configuration) bool {
	if a == nil || b == nil {
		return false
	}

	return a.BotUsername == b.BotUsername &&
		a.BotDisplayName == b.BotDisplayName &&
		a.BotDescription == b.BotDescription
}

// sameVoiceTalk reports whether talking from Matty would run the same way:
// switched on or off, on the same port, giving phones the same addresses,
// and under the same budget.
func sameVoiceTalk(a, b *configuration) bool {
	if a == nil || b == nil {
		return false
	}

	return a.EnableVoice == b.EnableVoice &&
		a.EnableVoiceTalk == b.EnableVoiceTalk &&
		a.VoiceTalkPort == b.VoiceTalkPort &&
		a.VoiceTalkAddress == b.VoiceTalkAddress &&
		a.VoiceTalkNoBudget == b.VoiceTalkNoBudget
}

func sameMumbleConnection(a, b *configuration) bool {
	if a == nil || b == nil {
		return false
	}

	return a.needsMumble() == b.needsMumble() &&
		a.MumbleServer == b.MumbleServer &&
		a.MumbleUsername == b.MumbleUsername &&
		a.MumblePassword == b.MumblePassword
}

// sameVoiceRoster reports whether two configurations, on the same connection,
// would show the same roster: whether it is shown at all, who is left out, and
// how names are matched to accounts. A roster is otherwise only rebuilt when
// somebody joins, leaves or changes state — and since the headphones put it on
// people's posts and profiles, a correction to a name match should show when it
// is saved, not whenever somebody next fidgets on Mumble.
func sameVoiceRoster(a, b *configuration) bool {
	if a == nil || b == nil {
		return false
	}

	return a.EnableVoice == b.EnableVoice &&
		a.VoiceHiddenNames == b.VoiceHiddenNames &&
		a.VoiceNameMap == b.VoiceNameMap &&
		a.VoiceMatchUsernames == b.VoiceMatchUsernames
}

// ignoredBridgeAccounts are Mattermost usernames whose posts are never sent to
// Mumble.
//
// For another bridge running alongside this one. Matterbridge posts as an
// ordinary account rather than a bot, so nothing about the post itself gives it
// away — only its username does.
func (c *configuration) ignoredBridgeAccounts() map[string]bool {
	ignored := map[string]bool{}

	for _, field := range strings.FieldsFunc(c.BridgeIgnore, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t'
	}) {
		if name := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(field), "@")); name != "" {
			ignored[name] = true
		}
	}

	return ignored
}

// bannerPlacement says where the panel shows its banner: "top", or "both"
// for the top and the bottom. The same two choices as Mattermost's own
// Classification Markings, so the panel can be set to match the bars around
// it. Anything unrecognised, a blank never-saved value included, is both:
// what the panel always did before there was a choice, and the safer of the
// two for a classification marking.
func (c *configuration) bannerPlacement() string {
	if strings.TrimSpace(c.BannerPlacement) == "top" {
		return "top"
	}

	return "both"
}

// openPanel says what the panel does when somebody loads the page: never,
// when somebody is live, or always.
//
// Normalised here rather than trusted, because an unrecognised value should
// leave people alone rather than do something surprising on every page load.
func (c *configuration) openPanel() string {
	switch strings.TrimSpace(c.OpenPanel) {
	case "live":
		return "live"
	case "always":
		return "always"
	default:
		return "never"
	}
}

// birthdayHour is the hour, in the configured zone, after which today's
// greetings go out. Out of range means the default rather than never.
func (c *configuration) birthdayHour() int {
	if c.BirthdayHour < 0 || c.BirthdayHour > 23 {
		return defaultBirthdayHour
	}

	return c.BirthdayHour
}

// clipLengthSeconds is how much one press of the ✂ keeps, in seconds: one of
// the lengths the settings page offers.
//
// Stored as text because the page offers it as a dropdown, and read through
// here rather than trusted, because a settings import or a hand-edited
// config.json can hold anything. A value that will not read as a number is the
// default rather than the shortest or the longest: a typo should not quietly
// make every clip half a minute, nor hold five minutes of every stream on disk.
// A number between two offered lengths takes the nearer one, and the shorter on
// a tie, since the length is also how much of everybody's stream is held.
func (c *configuration) clipLengthSeconds() int {
	seconds, err := strconv.Atoi(strings.TrimSpace(c.ClipLengthSeconds))
	if err != nil {
		return clipLengthDefault
	}

	shortest, longest := clipLengthOptions[0], clipLengthOptions[len(clipLengthOptions)-1]
	if seconds <= shortest {
		return shortest
	}
	if seconds >= longest {
		return longest
	}

	apart := func(option int) int {
		if seconds > option {
			return seconds - option
		}

		return option - seconds
	}

	// The options run shortest first, and only a strictly nearer one replaces
	// the one in hand, which is what gives a tie to the shorter.
	nearest := shortest
	for _, option := range clipLengthOptions {
		if apart(option) < apart(nearest) {
			nearest = option
		}
	}

	return nearest
}

// ringWindow is how much of each live stream is held for clipping: exactly one
// press, so no more of anybody's stream sits on this server's disk than one
// person could take. Its own value rather than clipLengthSeconds at every
// use, so that letting people extend a clip would change this one place.
func (c *configuration) ringWindow() time.Duration {
	return time.Duration(c.clipLengthSeconds()) * time.Second
}

// clipRetentionDays is how many days clips are kept, with normalize's rule
// applied to a configuration that has not been through it — the empty one
// getConfiguration hands out before Mattermost has said anything. Without
// it, 0 days would make every clip expire the moment it was published.
func (c *configuration) clipRetentionDays() int {
	switch {
	case c.ClipRetentionDays < 1:
		return clipRetentionDefaultDays
	case c.ClipRetentionDays > clipRetentionMaxDays:
		return clipRetentionMaxDays
	default:
		return c.ClipRetentionDays
	}
}

// clipStorageLimitBytes is the space for clips, in bytes, with normalize's
// rule applied as clipRetentionDays applies it: a limit of nothing would
// refuse every publish on a configuration nobody has saved yet.
func (c *configuration) clipStorageLimitBytes() int64 {
	limit := c.ClipStorageLimitGB
	switch {
	case limit < 1:
		limit = clipStorageDefaultGB
	case limit > clipStorageMaxGB:
		limit = clipStorageMaxGB
	}

	return int64(limit) << 30
}

// needsMumble reports whether anything wants the connection. The roster and the
// text bridge share one, because two would be two users in the channel list.
func (c *configuration) needsMumble() bool {
	return (c.EnableVoice || c.EnableChatBridge) && c.MumbleServer != ""
}

// voiceTalkWanted reports whether talking from Matty is switched on and has
// a voice server to talk on. Whether it works also takes the listener and
// the roster connection (voiceWordNow).
func (c *configuration) voiceTalkWanted() bool {
	return c.EnableVoice && c.EnableVoiceTalk && c.MumbleServer != ""
}

// voiceTalkAddresses are the lines of the address setting, trimmed, blank
// lines and repeats left out.
func (c *configuration) voiceTalkAddresses() []string {
	var lines []string
	seen := map[string]bool{}

	for _, line := range strings.Split(c.VoiceTalkAddress, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || seen[strings.ToLower(line)] {
			continue
		}
		seen[strings.ToLower(line)] = true
		lines = append(lines, line)
	}

	return lines
}

// hiddenVoiceNames are the Mumble names that are furniture rather than people:
// the chat bridge, and anything else parked in there.
//
// Load-bearing rather than tidying. Without it the voice row would permanently
// list the bridge and would never do the one thing asked of it, which is to
// disappear when nobody is in voice.
func (c *configuration) hiddenVoiceNames() map[string]bool {
	hidden := map[string]bool{}

	for _, field := range strings.FieldsFunc(c.VoiceHiddenNames, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	}) {
		if name := strings.ToLower(strings.TrimSpace(field)); name != "" {
			hidden[name] = true
		}
	}

	return hidden
}

// voiceNameMap pairs a Mumble name with a Mattermost username, for the people
// whose two names do not match: "mumbleName = mattermostUsername", one a line,
// a # commenting a line out.
//
// Keyed by the Mumble name folded ASCII-only, never strings.ToLower, and
// compared by voiceCandidate the same way (design/3.2.0.md §7.3). Go lowers
// a Turkish dotted İ to a plain i and the Kelvin sign to a k, which Murmur's
// own clash test does not, so a Unicode fold would let "vİctor" sit in Mumble
// beside "victor" and match the same account. Folded ASCII-only, every
// character that is not an ASCII letter has to be exactly the one the admin
// typed.
func (c *configuration) voiceNameMap() map[string]string {
	values := map[string]string{}

	for _, line := range strings.Split(c.VoiceNameMap, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		separator := strings.IndexAny(line, "=:")
		if separator <= 0 {
			continue
		}

		key := asciiFold(strings.TrimSpace(line[:separator]))
		value := asciiFold(strings.TrimPrefix(strings.TrimSpace(line[separator+1:]), "@"))
		if key != "" && value != "" {
			values[key] = value
		}
	}

	return values
}

// noteOverrides gives a stream a note when Broadcast Box holds none for it.
//
// Only ever a fallback. A streamer with a linked key writes their own note from
// the panel and that is the one that shows; this is for the channels that have
// nobody to write one — the 24/7 ones, which are exactly the streams a viewer
// is most likely to be looking at without knowing what they are.
func (c *configuration) noteOverrides() map[string]string {
	return keyedLines(c.StreamNotes)
}

// isHexColor accepts #rgb and #rrggbb, returning the six-digit form.
func isHexColor(value string) (string, bool) {
	if !strings.HasPrefix(value, "#") {
		return "", false
	}

	digits := value[1:]
	for _, r := range digits {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return "", false
		}
	}

	switch len(digits) {
	case 3:
		return "#" + string([]byte{digits[0], digits[0], digits[1], digits[1], digits[2], digits[2]}), true
	case 6:
		return "#" + digits, true
	default:
		return "", false
	}
}

// cutRunes trims a value to fit, with nothing added.
//
// Not clampRunes, which is for relayed chat and appends an ellipsis: that puts
// the result one rune OVER the limit that made it clamp, and Mattermost refuses
// the whole bot patch on a description of 1025 runes exactly as it does on
// 1225. A caught test, not a guessed one.
//
// A limit of zero cuts to nothing, which is what the name says. It used to mean
// "no limit", which is the sort of thing that reads fine until somebody passes
// a limit they computed.
func cutRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}

	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}

	return strings.TrimSpace(string(runes[:limit]))
}

// cleanName is the Name setting as people are shown it, or "" (which means
// productName). The System Console's box keeps to one line, but a restored
// backup, config.json or mmctl need not, and the name goes into posts made as
// a person, page titles and every refusal. So, as Mattermost cleans a display
// name and as cleanSoundName does: the characters that reorder or hide text
// (bidi controls, marks and isolates, zero-width spaces, the byte order mark)
// and control characters are dropped; each run of spaces, tabs and newlines
// becomes one space; a name with nothing visible left is blank; and the cut
// to maxNameRunes never leaves half an emoji (a joiner, or one of a flag's
// two letters) at the end. The zero-width joiner is kept: emoji are made of
// it. The tag characters (U+E0000 to U+E007F) are dropped, as Mattermost drops
// them from display names, so a subdivision flag such as Scotland's is left as
// the black flag it is built on.
func cleanName(value string) string {
	value = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsControl(r) && !unicode.IsSpace(r):
			return -1
		case r == '\u200b', r == '\u200e', r == '\u200f', r == '\u2060', r == '\u180e',
			r >= '\u2066' && r <= '\u2069':
			return -1
		}

		return r
	}, model.SanitizeUnicode(value))
	value = strings.Join(strings.FieldsFunc(value, unicode.IsSpace), " ")

	visible := false
	for _, r := range value {
		if unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S) {
			visible = true
			break
		}
	}
	if !visible {
		return ""
	}

	runes := []rune(cutRunes(value, maxNameRunes))
	for {
		end := len(runes)
		if end > 0 && runes[end-1] == '\u200d' {
			runes = runes[:end-1]
			continue
		}

		flags := 0
		for i := end - 1; i >= 0 && isRegionalIndicator(runes[i]); i-- {
			flags++
		}
		if flags%2 == 1 {
			runes = runes[:end-1]
			continue
		}

		break
	}

	return strings.TrimSpace(string(runes))
}

// isRegionalIndicator is one of the letters a flag emoji is made of, two at
// a time.
func isRegionalIndicator(r rune) bool {
	return r >= 0x1F1E6 && r <= 0x1F1FF
}

func (c *configuration) Clone() *configuration {
	clone := *c
	return &clone
}

// normalize applies defaults and trims operator typos (trailing slashes, colons
// around the emoji name) so the rest of the plugin can trust these values.
func (c *configuration) normalize() {
	c.Name = cleanName(c.Name)
	c.BroadcastBoxURL = strings.TrimRight(strings.TrimSpace(c.BroadcastBoxURL), "/")
	c.BasicAuthUsername = strings.TrimSpace(c.BasicAuthUsername)

	c.LiveEmoji = strings.Trim(strings.TrimSpace(c.LiveEmoji), ":")
	if c.LiveEmoji == "" {
		c.LiveEmoji = defaultLiveEmoji
	}

	c.LiveStatusText = strings.TrimSpace(c.LiveStatusText)
	if c.LiveStatusText == "" {
		c.LiveStatusText = defaultLiveText
	}

	c.BridgeChannel = strings.TrimSpace(c.BridgeChannel)

	// The bot's name is handed to Mattermost, which refuses anything that is
	// not a username — so an admin who types "Matty TV" here gets the
	// default back rather than a bot that silently stopped being renamed. The
	// @ is trimmed because that is how people write a username.
	c.BotUsername = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(c.BotUsername), "@"))
	if !model.IsValidUsername(c.BotUsername) {
		c.BotUsername = defaultBotUsername
	}

	c.BotDisplayName = strings.TrimSpace(c.BotDisplayName)
	if c.BotDisplayName == "" {
		c.BotDisplayName = defaultBotDisplayName
	}
	c.BotDisplayName = cutRunes(c.BotDisplayName, maxBotDisplayName)

	// An empty description is allowed — it is the one field of the three that
	// somebody might legitimately want blank — so it is not defaulted back.
	// Clamped, though: Mattermost refuses the whole patch over a long one, and
	// that would take the username and display name down with it.
	c.BotDescription = cutRunes(strings.TrimSpace(c.BotDescription), maxBotDescription)
	c.MumbleServer = strings.TrimSpace(c.MumbleServer)
	c.MumbleUsername = strings.TrimSpace(c.MumbleUsername)
	if c.MumbleUsername == "" {
		c.MumbleUsername = defaultMumbleUsername
	}

	// The public site URL ends up in an href, so anything that is not plain
	// http(s) is dropped rather than rendered.
	c.PublicSiteURL = strings.TrimRight(strings.TrimSpace(c.PublicSiteURL), "/")
	if c.PublicSiteURL != "" {
		parsed, err := url.Parse(c.PublicSiteURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			c.PublicSiteURL = ""
		}
	}

	// TMDB's settings page shows the token bare, but people copy it out of a
	// curl example as often as not, "Bearer " and all. Asterisks are what a
	// secret looks like once Mattermost has hidden it, never the secret, and
	// sending them would only earn a 401 on every search.
	c.TMDBToken = strings.TrimSpace(c.TMDBToken)
	if len(c.TMDBToken) > len("Bearer ") && strings.EqualFold(c.TMDBToken[:len("Bearer ")], "Bearer ") {
		c.TMDBToken = strings.TrimSpace(c.TMDBToken[len("Bearer "):])
	}
	if c.TMDBToken == redactedSetting {
		c.TMDBToken = ""
	}

	// The Twitch application's two halves are pasted from its console page,
	// often with a stray space. The secret gets the same guard as the TMDB
	// token: asterisks are a hidden secret, never the secret itself.
	c.TwitchClientID = strings.TrimSpace(c.TwitchClientID)
	c.TwitchClientSecret = strings.TrimSpace(c.TwitchClientSecret)
	if c.TwitchClientSecret == redactedSetting {
		c.TwitchClientSecret = ""
	}

	// Steam's key and the Hytale application's two halves are pasted from
	// their pages too, and the two secrets get the same guard. A value with
	// a space or a control character left inside is kept as it is: it can
	// never travel in a header, so it is treated as unset where it would be
	// sent, and the setup sentence names it (accountCredentialShaped).
	c.SteamWebAPIKey = strings.TrimSpace(c.SteamWebAPIKey)
	if c.SteamWebAPIKey == redactedSetting {
		c.SteamWebAPIKey = ""
	}
	c.HytaleClientID = strings.TrimSpace(c.HytaleClientID)
	c.HytaleClientSecret = strings.TrimSpace(c.HytaleClientSecret)
	if c.HytaleClientSecret == redactedSetting {
		c.HytaleClientSecret = ""
	}

	// An empty banner text hides the top bar. An empty colour means the
	// theme's own accent, which each client picks for itself; a colour that is
	// not one becomes empty too, rather than an invisible banner.
	c.BannerText = strings.TrimSpace(c.BannerText)
	if normalized, ok := isHexColor(strings.TrimSpace(c.BannerColor)); ok {
		c.BannerColor = normalized
	} else {
		c.BannerColor = ""
	}

	// Nothing below a day: a clip that expired as it was published would be a
	// way to lose people's work, and 0 is what an unsaved number field loads
	// as. A year at most, which is longer than anyone has asked to keep one.
	if c.ClipRetentionDays < 1 {
		c.ClipRetentionDays = clipRetentionDefaultDays
	}
	if c.ClipRetentionDays > clipRetentionMaxDays {
		c.ClipRetentionDays = clipRetentionMaxDays
	}

	// The same for the space clips may take. A limit of nothing would refuse
	// every publish without saying why anybody should look at this setting.
	if c.ClipStorageLimitGB < 1 {
		c.ClipStorageLimitGB = clipStorageDefaultGB
	}
	if c.ClipStorageLimitGB > clipStorageMaxGB {
		c.ClipStorageLimitGB = clipStorageMaxGB
	}

	c.SoundCooldownSeconds = c.soundCooldownSeconds()

	// The voice port is kept as the admin set it, even outside 1024 to
	// 65535: talk then stays off and says so (voiceTalkPortAllowed), rather
	// than open a port the firewall was never told about.
	c.VoiceTalkAddress = strings.Join(c.voiceTalkAddresses(), "\n")

	c.normalizePushSettings()

	if c.OfflineGraceChecks < 1 {
		c.OfflineGraceChecks = defaultOfflineGrace
	}
	if c.OfflineGraceChecks > 20 {
		c.OfflineGraceChecks = 20
	}

	switch {
	case c.PollIntervalSeconds < minPollSeconds:
		c.PollIntervalSeconds = defaultPollSeconds
	case c.PollIntervalSeconds > maxPollSeconds:
		c.PollIntervalSeconds = maxPollSeconds
	}
}

// validate reports whether the plugin has enough configuration to do anything
// useful. An unconfigured plugin stays activated but serves a friendly setup
// message instead of failing hard.
func (c *configuration) validate() error {
	if c.BroadcastBoxURL == "" {
		return errors.New("Broadcast Box URL is not set")
	}

	parsed, err := url.Parse(c.BroadcastBoxURL)
	if err != nil {
		return errors.New("Broadcast Box URL is not a valid URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("Broadcast Box URL must start with http:// or https://")
	}
	if parsed.Host == "" {
		return errors.New("Broadcast Box URL is missing a hostname")
	}
	if parsed.User != nil {
		return errors.New("put credentials in the Basic Auth settings, not in the URL")
	}

	return nil
}

// plainBroadcastBoxWarning is logged when Broadcast Box is reached over plain
// http on another machine. Every linked person's stream key goes to Broadcast
// Box in a header, on linking, on opening the panel and on saving a title, and
// so does the Basic Auth password on every poll. Only the host is logged
// beside it.
const plainBroadcastBoxWarning = "The Broadcast Box URL is plain http:// to another machine, so people's stream keys travel unencrypted to that host. Use https:// for it, or keep the path between the two servers on a network you trust."

// plainHTTPElsewhere is the host of an http:// address that is not this
// machine's, and whether it is one. Loopback is left alone: what never leaves
// the machine is not on any network to be read.
func plainHTTPElsewhere(address string) (string, bool) {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() == "" {
		return "", false
	}

	name := parsed.Hostname()
	if strings.EqualFold(name, "localhost") || strings.HasSuffix(strings.ToLower(name), ".localhost") {
		return "", false
	}
	if ip := net.ParseIP(name); ip != nil && ip.IsLoopback() {
		return "", false
	}

	return parsed.Host, true
}

func (c *configuration) hasBasicAuth() bool {
	return c.BasicAuthUsername != "" || c.BasicAuthPassword != ""
}

// previousConfiguration returns the configuration this process was last told
// about, or nil when it has not been told anything yet. getConfiguration
// deliberately substitutes an empty configuration for nil, which is right for
// every reader but wrong for anything comparing old against new.
func (p *Plugin) previousConfiguration() *configuration {
	p.configurationLock.RLock()
	defer p.configurationLock.RUnlock()

	return p.configuration
}

// brandName is what people on this server see the plugin called: the Name
// setting, or productName while it is blank and before the first
// configuration is loaded. Safe from any goroutine.
func (p *Plugin) brandName() string {
	if name := p.getConfiguration().Name; name != "" {
		return name
	}

	return productName
}

// getConfiguration returns the active configuration. It never returns nil.
func (p *Plugin) getConfiguration() *configuration {
	p.configurationLock.RLock()
	defer p.configurationLock.RUnlock()

	if p.configuration == nil {
		return &configuration{}
	}

	return p.configuration
}

func (p *Plugin) setConfiguration(configuration *configuration) {
	p.configurationLock.Lock()
	defer p.configurationLock.Unlock()

	p.configuration = configuration
}

// The soundboard's wait after somebody's sound ends (design/3.2.0.md §7.6):
// 10 seconds unless an admin says otherwise, and never under 3 or over 120,
// whatever a settings import or a hand-edited config.json holds.
const (
	soundCooldownDefault = 10
	soundCooldownMin     = 3
	soundCooldownMax     = 120
)

// The app check's three answers to "check the Matty app?".
const (
	appCheckOff     = "off"
	appCheckReport  = "report"
	appCheckRequire = "require"
)

// appCheckMode is the app check setting, read the way the settings page
// shows it: anything that is not one of its three values is the default,
// which checks and refuses nobody.
func (c *configuration) appCheckMode() string {
	switch strings.TrimSpace(c.AppCheck) {
	case appCheckOff:
		return appCheckOff
	case appCheckRequire:
		return appCheckRequire
	default:
		return appCheckReport
	}
}

// digestLines is a setting of SHA-256 digests, one per line, as a set of
// lower-case hex, and the numbers of the lines that are not one. Blank lines
// are nothing.
func digestLines(setting string) (map[string]bool, []int) {
	digests := map[string]bool{}
	var bad []int

	for i, line := range strings.Split(setting, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		digest, ok := appcheck.ParseDigest(line)
		if !ok {
			bad = append(bad, i+1)
			continue
		}
		digests[digest] = true
	}

	return digests, bad
}

// appCheckProblem is what is wrong with the two lists of digests, for the
// admins' page of results, or "".
func (c *configuration) appCheckProblem() string {
	var parts []string
	for _, setting := range []struct{ name, value string }{
		{"More signing certificates", c.AppCheckCertificates},
		{"More verified-boot keys", c.AppCheckBootKeys},
	} {
		if _, bad := digestLines(setting.value); len(bad) > 0 {
			lines := make([]string, len(bad))
			for i, n := range bad {
				lines[i] = strconv.Itoa(n)
			}
			parts = append(parts, fmt.Sprintf("line %s of %s", strings.Join(lines, ", "), setting.name))
		}
	}
	if len(parts) == 0 {
		return ""
	}

	return "Not a SHA-256 digest, so left out: " + strings.Join(parts, "; ") + "."
}

// soundCooldownSeconds is the wait with normalize's rule applied, for a
// configuration that has not been through it: the empty one getConfiguration
// hands out before Mattermost has said anything would otherwise mean no wait
// at all. Nothing set is the default, not the shortest.
func (c *configuration) soundCooldownSeconds() int {
	switch {
	case c.SoundCooldownSeconds < 1:
		return soundCooldownDefault
	case c.SoundCooldownSeconds < soundCooldownMin:
		return soundCooldownMin
	case c.SoundCooldownSeconds > soundCooldownMax:
		return soundCooldownMax
	default:
		return c.SoundCooldownSeconds
	}
}

// soundboardOn is whether the soundboard exists at all. It needs the roster
// as much as its own switch: a sound plays into the channel the roster finds
// its presser in, and the panel offers the board only to somebody the In
// voice row finds.
func (c *configuration) soundboardOn() bool {
	return c.EnableSoundboard && c.EnableVoice
}

// fansCounted is whether this pass may count biggest-fan time at all, by the
// settings alone: the feature and Top Snoopers both on, and, by Victor's
// answer of 2026-10-12, only while Show who is watching is on, when the cards
// already show who watches whom. With faces off nothing is counted, so the
// figures simply stand still; there is no hourly freeze to keep for fans.
func (c *configuration) fansCounted() bool {
	return c.EnableBiggestFans && c.TopWatchers && c.ShowWatchers
}

// fansShown is whether biggest fans are on the snapshot, by the settings
// alone: the feature and Top Snoopers on, whatever the faces are doing.
func (c *configuration) fansShown() bool {
	return c.EnableBiggestFans && c.TopWatchers
}
