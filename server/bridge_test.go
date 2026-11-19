package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/shared/markdown"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"layeh.com/gumble/gumble"
)

// A bridge that relays its own output is a bridge that fills a channel in about
// four seconds, so most of what follows is about what must NOT be relayed.

func bridgePlugin(t *testing.T) (*Plugin, *plugintest.API, *[]*model.Post) {
	t.Helper()

	posted := &[]*model.Post{}

	api := &plugintest.API{}
	api.On("GetChannelByNameForTeamName", "villanet", "town-square", false).Return(
		&model.Channel{Id: "chan-bridge", TeamId: "team-villanet"}, nil)
	api.On("GetChannelByNameForTeamName", mock.Anything, mock.Anything, mock.Anything).Return(
		nil, &model.AppError{Message: "no such channel"})
	// Not a member of anything to begin with, which is the case that used to
	// leave the bot outside the channel and its @all inert.
	api.On("GetTeamMember", mock.Anything, mock.Anything).Return(
		nil, &model.AppError{Message: "not a team member"})
	api.On("CreateTeamMember", mock.Anything, mock.Anything).Return(&model.TeamMember{}, nil)
	api.On("GetChannelMember", mock.Anything, mock.Anything).Return(
		nil, &model.AppError{Message: "not a channel member"})
	api.On("AddChannelMember", mock.Anything, mock.Anything).Return(&model.ChannelMember{}, nil)
	api.On("CreatePost", mock.Anything).Return(
		func(post *model.Post) *model.Post {
			*posted = append(*posted, post)

			return post
		},
		func(_ *model.Post) *model.AppError { return nil },
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
	p.botUserID = "uid-bot"

	return p, api, posted
}

func bridgeConfig() *configuration {
	cfg := &configuration{
		EnableChatBridge: true,
		EnableVoice:      true,
		MumbleServer:     "192.168.1.20",
		MumbleUsername:   "VillaNET",
		VoiceHiddenNames: "Mattermost",
		BridgeChannel:    "villanet/town-square",
	}
	cfg.normalize()

	return cfg
}

// The connection is shared: either feature alone is reason enough to hold it.
func TestTheMumbleConnectionServesBothFeatures(t *testing.T) {
	cfg := bridgeConfig()
	require.True(t, cfg.needsMumble())

	voiceOnly := bridgeConfig()
	voiceOnly.EnableChatBridge = false
	require.True(t, voiceOnly.needsMumble())

	bridgeOnly := bridgeConfig()
	bridgeOnly.EnableVoice = false
	require.True(t, bridgeOnly.needsMumble())

	neither := bridgeConfig()
	neither.EnableVoice = false
	neither.EnableChatBridge = false
	require.False(t, neither.needsMumble())

	noServer := bridgeConfig()
	noServer.MumbleServer = ""
	require.False(t, noServer.needsMumble(), "nothing to connect to")
}

// Rule 1: the plugin must never relay its own echo. MessageHasBeenPosted fires
// for posts the plugin itself created, and its own documentation says so.
func TestThePluginNeverRelaysItsOwnPosts(t *testing.T) {
	p, _, _ := bridgePlugin(t)
	p.setConfiguration(bridgeConfig())

	author := p.relayableAuthor(&model.Post{
		UserId:    "uid-bot",
		ChannelId: "chan-bridge",
		Message:   "**Victor**: relayed a moment ago",
	}, bridgeConfig())

	require.Nil(t, author, "its own post is not somebody talking")
}

// Rule 2: nothing any other integration posts is relayed either. This is what
// makes it safe to leave Matterbridge running during the swap — messages double
// briefly rather than echoing between the two of them for ever.
func TestNoIntegrationPostIsRelayed(t *testing.T) {
	p, api, _ := bridgePlugin(t)
	p.setConfiguration(bridgeConfig())

	api.On("GetUser", "uid-human").Return(&model.User{Id: "uid-human", Username: "victor"}, nil)
	api.On("GetUser", "uid-otherbot").Return(&model.User{Id: "uid-otherbot", Username: "matterbridge", IsBot: true}, nil)

	// A webhook post carries the prop rather than a bot account.
	webhook := &model.Post{UserId: "uid-human", ChannelId: "chan-bridge", Message: "via webhook"}
	webhook.AddProp("from_webhook", "true")

	cfg := bridgeConfig()

	for name, post := range map[string]*model.Post{
		"a webhook":        webhook,
		"another bridge":   {UserId: "uid-otherbot", ChannelId: "chan-bridge", Message: "from another bridge"},
		"a system message": {UserId: "uid-human", ChannelId: "chan-bridge", Message: "joined", Type: "system_join_channel"},
		"another channel":  {UserId: "uid-human", ChannelId: "some-other-channel", Message: "elsewhere"},
		"an empty message": {UserId: "uid-human", ChannelId: "chan-bridge", Message: ""},
	} {
		require.Nil(t, p.relayableAuthor(post, cfg), name)
	}

	// And a person in the bridged channel IS relayed, or the test above would
	// pass with a bridge that relays nothing at all.
	author := p.relayableAuthor(&model.Post{
		UserId:    "uid-human",
		ChannelId: "chan-bridge",
		Message:   "evening all",
	}, cfg)
	require.NotNil(t, author)
	require.Equal(t, "victor", author.Username)
}

// Switched off, the hook does no work at all — it runs for every post on the
// whole server.
func TestTheHookDoesNothingWhenTheBridgeIsOff(t *testing.T) {
	p, api, _ := bridgePlugin(t)

	off := bridgeConfig()
	off.EnableChatBridge = false
	p.setConfiguration(off)

	require.Nil(t, p.relayableAuthor(&model.Post{
		UserId:    "uid-human",
		ChannelId: "chan-bridge",
		Message:   "hello",
	}, off))

	api.AssertNotCalled(t, "GetUser", mock.Anything)
	api.AssertNotCalled(t, "GetChannelByNameForTeamName", mock.Anything, mock.Anything, mock.Anything)
}

// The other direction, and the same two rules.
func TestMumbleMessagesAreRelayedExceptTheFurniture(t *testing.T) {
	p, _, posted := bridgePlugin(t)
	cfg := bridgeConfig()
	p.setConfiguration(cfg)

	relay := func(name, message string) {
		p.relayFromMumble(gumbleText(name, message), cfg)
	}

	relay("Victor", "evening all")
	relay("Mattermost", "Victor: this came from the old bridge")
	relay("VillaNET", "this is the plugin's own connection")

	require.Len(t, *posted, 1, "only the person")
	require.Equal(t, "chan-bridge", (*posted)[0].ChannelId)
	require.Equal(t, "uid-bot", (*posted)[0].UserId)
	require.Equal(t, "**Victor**: evening all", (*posted)[0].Message)
}

// A private message in Mumble is private. Reposting one into a public channel
// would publish something somebody deliberately said to one listener.
func TestAPrivateMumbleMessageIsNotPublished(t *testing.T) {
	p, _, posted := bridgePlugin(t)
	cfg := bridgeConfig()
	p.setConfiguration(cfg)

	p.relayFromMumble(gumbleWhisper("Victor", "just between us"), cfg)
	require.Empty(t, *posted, "a whisper is not for the channel")

	// And the same words to the channel are relayed, so the test above is not
	// passing on a bridge that relays nothing.
	p.relayFromMumble(gumbleText("Victor", "just between us"), cfg)
	require.Len(t, *posted, 1)
}

// A name is not formatting.
func TestAMumbleNameCannotSmuggleMarkdown(t *testing.T) {
	p, _, posted := bridgePlugin(t)
	cfg := bridgeConfig()
	p.setConfiguration(cfg)

	p.relayFromMumble(gumbleText("**Victor**", "hello"), cfg)

	require.Len(t, *posted, 1)
	require.Equal(t, `**\*\*Victor\*\***: hello`, (*posted)[0].Message)
}

// Markdown typed in Mumble cannot make the bot post a picture every reader
// fetches, or a link that reads as one address and goes to another; brackets
// that are only brackets, and everything else, read as typed.
func TestAMumbleMessageCannotSmuggleALinkOrAPicture(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"![ ](https://x/p.png)", "![ ]\u200b(https://x/p.png)"},
		{"[a](https://b)", "[a]\u200b(https://b)"},
		{"<p>[https://chat.example.com/login](https://evil.example/login)</p>", "[https://chat.example.com/login]\u200b(https://evil.example/login)"},
		{"[image]", "[image]"},
		{"a[0] and (b)", "a[0] and (b)"},
		{"**bold** and _italic_ https://example.com", "**bold** and _italic_ https://example.com"},
	} {
		p, _, posted := bridgePlugin(t)
		cfg := bridgeConfig()
		p.setConfiguration(cfg)

		p.relayFromMumble(gumbleText("Victor", tc.in), cfg)

		require.Len(t, *posted, 1, tc.in)
		require.Equal(t, "**Victor**: "+tc.want, (*posted)[0].Message, tc.in)
	}
}

// Mumble chat is HTML, and a pasted image arrives as a data: URI that can run
// to megabytes.
func TestMumbleHTMLIsFlattened(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain text", "plain text"},
		{"<b>bold</b> and <i>italic</i>", "bold and italic"},
		{"one<br/>two", "one\ntwo"},
		{"<p>one</p><p>two</p>", "one\ntwo"},
		{"&lt;not a tag&gt; &amp; an entity", "<not a tag> & an entity"},
		{`<a href="https://example.com">the link</a>`, "the link"},
		{`before<img src="data:image/png;base64,AAAAAAAA"/>after`, "before[image]after"},
		{"<script>alert(1)</script>", "alert(1)"},
		{"", ""},
	} {
		require.Equal(t, tc.want, htmlToText(tc.in), tc.in)
	}
}

// Neither side gets an unbounded message.
func TestBridgedMessagesAreBounded(t *testing.T) {
	long := strings.Repeat("é", maxBridgedRunes*2)
	clamped := clampRunes(long, maxBridgedRunes)

	require.LessOrEqual(t, len([]rune(clamped)), maxBridgedRunes+1, "the ellipsis is the one extra")
	require.True(t, strings.HasSuffix(clamped, "…"))
	require.Equal(t, "short", clampRunes("  short  ", maxBridgedRunes))
}

// Matterbridge posts as an ORDINARY account, not a bot, so nothing about the
// post distinguishes it from a person. Left unguarded that is an infinite loop
// between the two bridges rather than a doubling.
func TestAnotherBridgesAccountIsNotRelayed(t *testing.T) {
	p, api, _ := bridgePlugin(t)

	api.On("GetUser", "uid-matterbridge").Return(
		&model.User{Id: "uid-matterbridge", Username: "matterbridge"}, nil)
	api.On("GetUser", "uid-human").Return(
		&model.User{Id: "uid-human", Username: "victor"}, nil)

	cfg := bridgeConfig()
	cfg.BridgeIgnore = "@matterbridge"

	post := func(userID string) *model.Post {
		return &model.Post{UserId: userID, ChannelId: "chan-bridge", Message: "hello"}
	}

	require.Nil(t, p.relayableAuthor(post("uid-matterbridge"), cfg), "an ordinary account, still a bridge")
	require.NotNil(t, p.relayableAuthor(post("uid-human"), cfg), "and a person is still a person")

	// Without the setting it is indistinguishable from a person, which is
	// exactly why the breaker below exists.
	require.NotNil(t, p.relayableAuthor(post("uid-matterbridge"), bridgeConfig()))
}

// The guards all depend on somebody having configured something. The breaker
// does not.
func TestTheRelayStopsRatherThanRunningAway(t *testing.T) {
	p, _, _ := bridgePlugin(t)

	allowed := 0
	for i := 0; i < relayBurst*3; i++ {
		if p.mayRelay() {
			allowed++
		}
	}

	require.Equal(t, relayBurst, allowed, "it stops rather than discovering how fast Mumble can go")

	// And it opens again once the minute has passed.
	p.bridgeLock.Lock()
	p.relayWindowStart = time.Now().Add(-2 * relayWindow)
	p.bridgeLock.Unlock()

	require.True(t, p.mayRelay())
}

// The channel is looked up from the team/channel an admin typed, once.
func TestTheBridgeChannelIsResolvedAndRemembered(t *testing.T) {
	p, api, _ := bridgePlugin(t)
	cfg := bridgeConfig()

	require.Equal(t, "chan-bridge", p.channelIDFor(cfg.BridgeChannel))
	require.Equal(t, "chan-bridge", p.channelIDFor(cfg.BridgeChannel))
	api.AssertNumberOfCalls(t, "GetChannelByNameForTeamName", 1)

	// A different setting is a different lookup.
	moved := bridgeConfig()
	moved.BridgeChannel = "villanet/somewhere-else"
	require.Empty(t, p.channelIDFor(moved.BridgeChannel))

	// And a failure is remembered too. MessageHasBeenPosted runs for every post
	// on the server, so a channel that does not resolve must not become a
	// database query per post for everybody.
	for i := 0; i < 50; i++ {
		require.Empty(t, p.channelIDFor(moved.BridgeChannel))
	}
	api.AssertNumberOfCalls(t, "GetChannelByNameForTeamName", 2)

	// Remembered for a minute, not for good: creating the channel or fixing
	// the typo takes effect without a restart.
	p.bridgeLock.Lock()
	p.channelMisses[moved.BridgeChannel] = time.Now().Add(-2 * channelMissTTL)
	p.bridgeLock.Unlock()
	require.Empty(t, p.channelIDFor(moved.BridgeChannel))
	api.AssertNumberOfCalls(t, "GetChannelByNameForTeamName", 3)

	// And something that is not team/channel at all is refused rather than
	// guessed at.
	malformed := bridgeConfig()
	malformed.BridgeChannel = "town-square"
	require.Empty(t, p.channelIDFor(malformed.BridgeChannel))
}

// gumbleText is a message sent to a CHANNEL, which is the ordinary case.
func gumbleText(sender, message string) *gumble.TextMessageEvent {
	event := &gumble.TextMessageEvent{}
	event.Sender = &gumble.User{Name: sender}
	event.Message = message
	event.Channels = []*gumble.Channel{{ID: 0, Name: "Root"}}

	return event
}

// gumbleWhisper is a message sent straight to this connection and nobody else.
func gumbleWhisper(sender, message string) *gumble.TextMessageEvent {
	event := &gumble.TextMessageEvent{}
	event.Sender = &gumble.User{Name: sender}
	event.Message = message
	event.Users = []*gumble.User{{Name: "VillaNET"}}

	return event
}

// Anybody who can connect to the voice server can type @all. Without this, that
// is somebody outside Mattermost, with no account, ringing every phone on the
// team.
func TestRelayedTextCannotMentionEverybody(t *testing.T) {
	p, _, posted := bridgePlugin(t)
	cfg := bridgeConfig()
	p.setConfiguration(cfg)

	p.relayFromMumble(gumbleText("Victor", "@all @channel @here @HERE look at this"), cfg)

	require.Len(t, *posted, 1)
	message := (*posted)[0].Message

	for _, broadcast := range []string{"@all ", "@channel ", "@here ", "@HERE "} {
		require.NotContains(t, message, broadcast, "a broadcast mention survived")
	}
	require.False(t, mattermostWouldBroadcast(message), message)

	// It still READS as what they typed, and a person is still a person.
	require.Contains(t, message, "all")
	require.Contains(t, message, "look at this")

	p.relayFromMumble(gumbleText("Victor", "ask @dave about it"), cfg)
	require.Contains(t, (*posted)[1].Message, "@dave", "mentioning a person still works")

	// Mattermost lower-cases the word before comparing it, so a spelling
	// nobody thought to list rings every phone just the same.
	p.relayFromMumble(gumbleText("Victor", "@aLL @Here @cHaNnEl stream is up"), cfg)
	require.False(t, mattermostWouldBroadcast((*posted)[2].Message), (*posted)[2].Message)
	require.Contains(t, (*posted)[2].Message, "stream is up")
}

// Every way of writing the three words that Mattermost would take for one of
// them, judged by Mattermost's own rules (mattermostWouldBroadcast) rather than
// by a list of spellings, which is how the gap got in.
func TestEverySpellingOfABroadcastMentionIsDefused(t *testing.T) {
	for _, typed := range []string{
		"@all", "@All", "@ALL", "@aLL", "@alL",
		"@channel", "@CHANNEL", "@cHaNnEl",
		"@here", "@HERE", "@hERE",
		// Where the word ends, as Mattermost reads it: a trailing "_", ".",
		// "-" or ":" comes off before it compares.
		"@all_", "@all.", "@all:", "@all-", "@all__", "@all_.",
		"(@all)", "@all!", "@all,", "**@all**:", "x.@all", "x:@all-stars", `\@all`,
		// A post is Markdown, and Mattermost decodes a character reference
		// before it looks, so these have no "all", or no @, until it does.
		"@al&#108;", "@AL&#x4C;", "@channe&#108;", "@her&#101;.", "&#64;all", "&commat;channel", "@&#97;ll",
	} {
		// Its parser keeps a decoded reference out of the text after it, so
		// the last three ring nobody in this version of it, but one that
		// joined them would find @all: it is defused for that one too.
		require.True(t, mattermostWouldBroadcast(typed+" look") || broadcastIn(mattermostText(typed+" look")),
			"%q is a broadcast to begin with", typed)

		defused := defuseMentions(typed + " look")
		require.False(t, mattermostWouldBroadcast(defused), "%q went through as %q", typed, defused)
		require.False(t, broadcastIn(mattermostText(defused)), "%q went through as %q", typed, defused)

		// And it reads as it always did, but that a reference reads as it
		// was typed rather than decoded: as it would with every & literal.
		literal := mattermostText(strings.ReplaceAll(typed, "&", "&amp;") + " look")
		require.Equal(t, literal, strings.ReplaceAll(mattermostText(defused), zeroWidthSpace, ""), "%q reads as typed", typed)
	}

	// A word that only starts the same way is somebody's name.
	for _, person := range []string{"@allison", "@here4u", "@channels", "@victor", "@all2"} {
		require.Equal(t, "ask "+person+" about it", defuseMentions("ask "+person+" about it"))
	}
}

// A name is written into the post, and Mattermost reads a mention straight
// through the bold: it splits words on the asterisks, so "**@all**: hello"
// holds the word @all. So somebody who called themselves @all in Mumble made
// the bot ring every phone with every message they sent.
func TestAMumbleNameCannotMentionAnybody(t *testing.T) {
	p, _, posted := bridgePlugin(t)
	cfg := bridgeConfig()
	p.setConfiguration(cfg)

	names := []struct{ name, mention string }{
		{"@all", "@all"}, {"@aLL", "@aLL"}, {"@channel", "@channel"}, {"@HERE", "@HERE"},
		{"@al&#108;", "@all"}, {"&#64;all", "@all"}, {"&commat;here", "@here"},
		{"@victor", "@victor"},
	}
	for _, c := range names {
		p.relayFromMumble(gumbleText(c.name, "hello"), cfg)
	}

	require.Len(t, *posted, len(names))
	for i, post := range *posted {
		read := mattermostText(post.Message)
		require.False(t, mattermostWouldBroadcast(post.Message), post.Message)
		require.False(t, broadcastIn(read), post.Message)
		require.NotContains(t, read, names[i].mention, "not even a person: a name says who is talking")
		require.Equal(t, "**"+names[i].name+"**: hello", strings.ReplaceAll(read, zeroWidthSpace, ""), "and it reads as their name")
	}
}

// mattermostWouldBroadcast reads a post the way Mattermost's server does when
// it works out who the post notifies, and reports whether that includes @all,
// @channel or @here.
//
// As getExplicitMentions (server/channels/app/notification.go, v11.11.1)
// does: through Mattermost's own Markdown parser, so what it reads is the text
// with character references decoded and escapes taken out, and code left out
// altogether, a run of text at a time (mattermostTexts). Each run is then cut
// into words as ProcessText and checkForMention
// (mention_parser_standard.go) cut it, which set the broadcast flag for any
// word they are handed that lower-cases to one of the three, whether or not
// it is anybody's keyword. Only that part of them: the cutting, the trimming
// and the lower-casing.
func mattermostWouldBroadcast(message string) bool {
	for _, text := range mattermostTexts(message) {
		if broadcastIn(text) {
			return true
		}
	}

	return false
}

// mattermostText is what Mattermost reads of a post, all its runs of text
// together: for a test that wants what a reader sees, as opposed to the
// Markdown that was posted.
func mattermostText(message string) string {
	return strings.Join(mattermostTexts(message), "")
}

// mattermostTexts is a post's runs of text as getExplicitMentions collects
// them: adjacent Text nodes joined, and anything else ending a run.
func mattermostTexts(message string) []string {
	var runs []string
	run := ""

	markdown.Inspect(message, func(node any) bool {
		text, ok := node.(*markdown.Text)
		if !ok {
			if run != "" {
				runs = append(runs, run)
			}
			run = ""

			return true
		}

		run += text.Text

		return false
	})
	if run != "" {
		runs = append(runs, run)
	}

	return runs
}

// broadcastIn is ProcessText's part of mattermostWouldBroadcast, for one run.
func broadcastIn(text string) bool {
	broadcast := func(word string) bool {
		switch strings.ToLower(word) {
		case "@all", "@channel", "@here":
			return true
		}

		return false
	}

	words := strings.FieldsFunc(text, func(c rune) bool {
		return !(c == ':' || c == '.' || c == '-' || c == '_' || c == '@' || unicode.IsLetter(c) || unicode.IsNumber(c))
	})
	for _, word := range words {
		// ":word:" is an emoji.
		if word[0] == ':' && word[len(word)-1] == ':' {
			continue
		}

		word = strings.TrimLeft(word, ":.-_")
		if broadcast(word) {
			return true
		}

		for trimmed := word; trimmed != "" && strings.LastIndexAny(trimmed, ".-:_") == len(trimmed)-1; {
			trimmed = trimmed[:len(trimmed)-1]
			if broadcast(trimmed) {
				return true
			}
		}

		// A word that does not start with @ is split again on ".", "-" and
		// ":", which is how "x.@all" is @all.
		if !strings.HasPrefix(word, "@") && strings.ContainsAny(word, ".-:") {
			for _, part := range strings.FieldsFunc(word, func(c rune) bool { return c == '.' || c == '-' || c == ':' }) {
				if broadcast(part) {
					return true
				}
			}
		}
	}

	return false
}

// A newline plus another bold name is a post that looks like two relayed
// messages, the second from somebody who never said it.
func TestARelayedMessageCannotImpersonateTheNextSpeaker(t *testing.T) {
	p, _, posted := bridgePlugin(t)
	cfg := bridgeConfig()
	p.setConfiguration(cfg)

	p.relayFromMumble(gumbleText("Victor", "morning<br/><b>Dave</b>: I quit"), cfg)

	require.Len(t, *posted, 1)
	require.NotContains(t, (*posted)[0].Message, "\n", "a relayed line is one line")
	require.Contains(t, (*posted)[0].Message, "I quit", "and none of it is lost")
}

// Murmur measures its limit in bytes, and escaping is what makes text grow.
func TestWhatGoesToMumbleIsBoundedInBytes(t *testing.T) {
	// Every rune here becomes "&lt;" — one byte in, four bytes out.
	grown := clampBytes(strings.Repeat("&lt;", 4000), maxMumbleBytes)
	require.LessOrEqual(t, len(grown), maxMumbleBytes)

	// Cut on a rune boundary, never through one.
	emoji := clampBytes(strings.Repeat("\U0001f382", 4000), maxMumbleBytes)
	require.LessOrEqual(t, len(emoji), maxMumbleBytes)
	require.True(t, utf8.ValidString(emoji), "never invalid UTF-8")

	require.Equal(t, "short", clampBytes("short", maxMumbleBytes))
}

// The connection is long-lived and is deliberately NOT rebuilt for settings
// that do not change where it dials. So anything reading configuration on that
// connection has to read the CURRENT one — a copy captured when Mumble was last
// connected to could be days old, and switching the bridge on would do nothing
// at all until something unrelated forced a re-dial.
func TestSwitchingTheBridgeOnTakesEffectWithoutReconnecting(t *testing.T) {
	p, _, posted := bridgePlugin(t)

	// The connection is up, dialled while the bridge was switched off.
	dialledWith := bridgeConfig()
	dialledWith.EnableChatBridge = false
	p.setConfiguration(dialledWith)

	p.relayFromMumble(gumbleText("Victor", "before"), p.getConfiguration())
	require.Empty(t, *posted, "nothing to relay yet")

	// An admin switches the bridge on. Nothing about WHERE it dials changed,
	// so the connection is rightly left alone.
	now := bridgeConfig()
	require.True(t, sameMumbleConnection(dialledWith, now), "no reconnect is warranted")
	p.setConfiguration(now)

	p.relayFromMumble(gumbleText("Victor", "after"), p.getConfiguration())
	require.Len(t, *posted, 1, "the running connection picked up the new setting")
	require.Contains(t, (*posted)[0].Message, "after")
}

// The permission that allows @all, @channel and @here is use_channel_mentions,
// and the built-in roles that grant it are channel_user, channel_guest and
// team_user. system_user — all a bot has by default — does not. Mattermost
// checks it against the post's AUTHOR, so a bot outside the channel posts an
// @all that notifies nobody and highlights for nobody: exactly how the birthday
// greeting went out.
//
// So resolving a channel must leave the bot a member of it, and of the team,
// because AddChannelMember does not add the team for you.
func TestResolvingAChannelPutsTheBotOnTheTeamAndInTheChannel(t *testing.T) {
	p, api, _ := bridgePlugin(t)

	require.Equal(t, "chan-bridge", p.channelIDFor("villanet/town-square"))

	api.AssertCalled(t, "CreateTeamMember", "team-villanet", "uid-bot")
	api.AssertCalled(t, "AddChannelMember", "chan-bridge", "uid-bot")
}

// It runs on every channel resolution, so asking to join something it is
// already in must cost a read rather than a write.
func TestTheBotIsNotAddedToAChannelItIsAlreadyIn(t *testing.T) {
	posted := &[]*model.Post{}

	api := &plugintest.API{}
	api.On("GetTeamMember", mock.Anything, mock.Anything).Return(&model.TeamMember{}, nil)
	api.On("GetChannelMember", mock.Anything, mock.Anything).Return(&model.ChannelMember{}, nil)
	api.On("CreateTeamMember", mock.Anything, mock.Anything).Return(&model.TeamMember{}, nil)
	api.On("AddChannelMember", mock.Anything, mock.Anything).Return(&model.ChannelMember{}, nil)
	api.On("GetChannelByNameForTeamName", mock.Anything, mock.Anything, mock.Anything).Return(
		&model.Channel{Id: "chan-bridge", TeamId: "team-villanet"}, nil)
	api.On("CreatePost", mock.Anything).Return(
		func(post *model.Post) *model.Post {
			*posted = append(*posted, post)

			return post
		},
		func(_ *model.Post) *model.AppError { return nil },
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
	p.botUserID = "uid-bot"

	require.Equal(t, "chan-bridge", p.channelIDFor("villanet/town-square"))

	api.AssertNotCalled(t, "CreateTeamMember", mock.Anything, mock.Anything)
	api.AssertNotCalled(t, "AddChannelMember", mock.Anything, mock.Anything)
}

// EnsureBotUser remembers the bot's id and PATCHES the existing account with
// whatever name it is handed, so renaming is a matter of handing it a different
// one. Nothing here looks the old name up, and nothing creates a second bot.
func TestRenamingTheBotPatchesTheAccountItAlreadyHas(t *testing.T) {
	api := &plugintest.API{}

	var asked []*model.Bot
	// No remembered bot, so a refused rename falls back to the default name
	// exactly as it did before the fallback learned to try the current one.
	api.On("KVGet", ensureBotKey).Return([]byte(nil), (*model.AppError)(nil))
	api.On("EnsureBotUser", mock.Anything).Return(
		func(bot *model.Bot) string {
			asked = append(asked, bot)

			return "uid-bot"
		},
		func(_ *model.Bot) error { return nil },
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
	p.setConfiguration(&configuration{BotUsername: "tv-bot", BotDisplayName: "The Telly"})

	p.ensureBot()

	require.Len(t, asked, 1)
	require.Equal(t, "tv-bot", asked[0].Username)
	require.Equal(t, "The Telly", asked[0].DisplayName)
	require.Equal(t, "uid-bot", p.getBotUserID())
	require.Equal(t, "", p.botProblemNow())
}

// A name Mattermost will not take leaves the account alone. An admin who typed
// it and saw nothing happen has to be told which name is the problem.
func TestARefusedBotNameIsReported(t *testing.T) {
	api := &plugintest.API{}
	// No remembered bot, so a refused rename falls back to the default name
	// exactly as it did before the fallback learned to try the current one.
	api.On("KVGet", ensureBotKey).Return([]byte(nil), (*model.AppError)(nil))
	api.On("EnsureBotUser", mock.Anything).Return("", errors.New(`username "villa" is already taken by a non-bot user`))
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
	p.setConfiguration(&configuration{BotUsername: "villa", BotDisplayName: "VillaNET"})

	p.ensureBot()

	require.Contains(t, p.botProblemNow(), "already taken")
}

// Mattermost refuses a username with a space or a capital in it, and the
// refusal would arrive as a bot that quietly stopped being renamed. Caught
// here instead, where the default can be put back.
func TestABotNameThatIsNotAUsernameFallsBack(t *testing.T) {
	// "VillaNET TV" is here for the SPACE in it, not because of what it says.
	// A rename of the product swept through this file once and turned it into
	// "VillaNET", which lowercases to a perfectly valid username — so the case
	// stopped testing anything and the suite said so.
	for _, typed := range []string{"VillaNET TV", "villa net", "", "@", "all", "channel", "tv!"} {
		cfg := &configuration{BotUsername: typed}
		cfg.normalize()

		require.Equal(t, defaultBotUsername, cfg.BotUsername, "typed %q", typed)
	}

	// And the ones it will take, including an @ somebody pasted with it.
	for typed, want := range map[string]string{
		"tv-bot":      "tv-bot",
		"@tv-bot":     "tv-bot",
		"  TV_Bot ":   "tv_bot",
		"villanet.tv": "villanet.tv",
	} {
		cfg := &configuration{BotUsername: typed}
		cfg.normalize()

		require.Equal(t, want, cfg.BotUsername, "typed %q", typed)
	}
}

// Renaming is a write to Mattermost, so it must not happen on every save of an
// unrelated setting.
func TestTheBotIsOnlyRenamedWhenItsNameChanges(t *testing.T) {
	base := &configuration{BotUsername: "tv-bot", BotDisplayName: "The Telly"}

	same := base.Clone()
	same.BannerText = "changed"
	require.True(t, sameBotIdentity(base, same))

	renamed := base.Clone()
	renamed.BotUsername = "telly"
	require.False(t, sameBotIdentity(base, renamed))

	relabelled := base.Clone()
	relabelled.BotDisplayName = "Telly"
	require.False(t, sameBotIdentity(base, relabelled))

	require.False(t, sameBotIdentity(nil, base), "no previous configuration is not a match")
}

// The birthday channel is otherwise only resolved on the morning of somebody's
// birthday, which is a poor time to discover the bot is not in it. Resolving
// both configured channels at activation is what makes the membership — and so
// the @all — visible to an admin who has just upgraded.
func TestBothConfiguredChannelsAreJoined(t *testing.T) {
	api := &plugintest.API{}

	// Registered before any catch-all: testify takes the first expectation that
	// matches, so a broad one registered earlier would swallow these.
	api.On("GetChannelByNameForTeamName", "villanet", "town-square", false).Return(
		&model.Channel{Id: "chan-bridge", TeamId: "team-villanet"}, nil)
	api.On("GetChannelByNameForTeamName", "villanet", "birthdays", false).Return(
		&model.Channel{Id: "chan-birthdays", TeamId: "team-villanet"}, nil)
	api.On("GetTeamMember", mock.Anything, mock.Anything).Return(
		nil, &model.AppError{Message: "not a team member"})
	api.On("CreateTeamMember", mock.Anything, mock.Anything).Return(&model.TeamMember{}, nil)
	api.On("GetChannelMember", mock.Anything, mock.Anything).Return(
		nil, &model.AppError{Message: "not a channel member"})
	api.On("AddChannelMember", mock.Anything, mock.Anything).Return(&model.ChannelMember{}, nil)
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
	p.botUserID = "uid-bot"
	p.setConfiguration(&configuration{
		BridgeChannel:   "villanet/town-square",
		BirthdayChannel: "villanet/birthdays",
	})

	p.joinConfiguredChannels()

	api.AssertCalled(t, "AddChannelMember", "chan-bridge", "uid-bot")
	api.AssertCalled(t, "AddChannelMember", "chan-birthdays", "uid-bot")
}

func TestNoChannelIsResolvedWhenNoneIsConfigured(t *testing.T) {
	p, api, _ := bridgePlugin(t)
	p.setConfiguration(&configuration{})

	p.joinConfiguredChannels()

	api.AssertNotCalled(t, "GetChannelByNameForTeamName", mock.Anything, mock.Anything, mock.Anything)
}

// A name Mattermost will not take must not cost the plugin its bot.
//
// EnsureBotUser patches the account it already has, so a username somebody else
// owns fails the whole call — BEFORE handing back the id of the perfectly good
// bot it just looked up. Taking that as the answer is not "the rename did not
// happen", it is no bot at all: birthdays stop and the bridge goes silent.
func TestARefusedRenameFallsBackToTheDefaultNameRatherThanNoBot(t *testing.T) {
	api := &plugintest.API{}

	var asked []string
	// No remembered bot, so a refused rename falls back to the default name
	// exactly as it did before the fallback learned to try the current one.
	api.On("KVGet", ensureBotKey).Return([]byte(nil), (*model.AppError)(nil))
	api.On("EnsureBotUser", mock.Anything).Return(
		func(bot *model.Bot) string {
			asked = append(asked, bot.Username)
			if bot.Username == "villa" {
				return ""
			}

			return "uid-bot"
		},
		func(bot *model.Bot) error {
			if bot.Username == "villa" {
				return errors.New(`username "villa" is already taken by a non-bot user`)
			}

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
	p.setConfiguration(&configuration{BotUsername: "villa", BotDisplayName: "VillaNET"})

	p.ensureBot()

	require.Equal(t, []string{"villa", defaultBotUsername}, asked,
		"the refused name is tried, then the default")
	require.Equal(t, "uid-bot", p.getBotUserID(), "there is still a bot")
	require.Contains(t, p.botProblemNow(), "already taken")
	require.Contains(t, p.botProblemNow(), defaultBotUsername,
		"and the admin is told what it is called instead")
}

// The retry runs from MessageHasBeenPosted, which fires for every post on the
// whole server. Unpaced, a permanently unavailable bot means an EnsureBotUser
// round trip per message, for as long as the plugin runs.
func TestTheBotIsNotRetriedOnEveryPost(t *testing.T) {
	api := &plugintest.API{}
	// No remembered bot, so a refused rename falls back to the default name
	// exactly as it did before the fallback learned to try the current one.
	api.On("KVGet", ensureBotKey).Return([]byte(nil), (*model.AppError)(nil))
	api.On("EnsureBotUser", mock.Anything).Return("", errors.New("no bot for you"))
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
	p.setConfiguration(&configuration{})

	for i := 0; i < 50; i++ {
		require.Equal(t, "", p.getBotUserID())
	}

	// One attempt for the default name and one for the fallback path is the
	// most a single try costs; fifty posts must not multiply it.
	api.AssertNumberOfCalls(t, "EnsureBotUser", 1)

	// And the retry is still a retry: it happens, just not fifty times a second.
	p.bridgeLock.Lock()
	p.botTriedAt = time.Now().Add(-botRetryInterval - time.Second)
	p.bridgeLock.Unlock()

	require.Equal(t, "", p.getBotUserID())
	api.AssertNumberOfCalls(t, "EnsureBotUser", 2)
}

// Removing somebody from a team leaves the row behind with a deletion time on
// it, and GetTeamMember hands that row back with NO error. A bot that has been
// removed therefore looks like a member, the re-join is skipped, and every
// AddChannelMember after it fails for as long as the plugin runs.
func TestASoftDeletedTeamMembershipIsNotAMembership(t *testing.T) {
	api := &plugintest.API{}
	api.On("GetTeamMember", mock.Anything, mock.Anything).Return(
		&model.TeamMember{TeamId: "team-villanet", UserId: "uid-bot", DeleteAt: 1700000000000}, nil)
	api.On("CreateTeamMember", mock.Anything, mock.Anything).Return(&model.TeamMember{}, nil)
	api.On("GetChannelMember", mock.Anything, mock.Anything).Return(
		nil, &model.AppError{Message: "not a member"})
	api.On("AddChannelMember", mock.Anything, mock.Anything).Return(&model.ChannelMember{}, nil)
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
	p.botUserID = "uid-bot"

	p.joinBot("team-villanet", "chan-bridge")

	api.AssertCalled(t, "CreateTeamMember", "team-villanet", "uid-bot")
}

// The channel id is cached the first time it resolves. Without this, so was a
// failed attempt to join it — and the birthday greeting went out as a
// plain-text @all every day until somebody restarted the server.
func TestAFailedJoinIsRetriedOnTheNextResolve(t *testing.T) {
	api := &plugintest.API{}
	api.On("GetChannelByNameForTeamName", mock.Anything, mock.Anything, mock.Anything).Return(
		&model.Channel{Id: "chan-bridge", TeamId: "team-villanet"}, nil)
	api.On("GetTeamMember", mock.Anything, mock.Anything).Return(&model.TeamMember{}, nil)
	api.On("GetChannelMember", mock.Anything, mock.Anything).Return(
		nil, &model.AppError{Message: "not a member"})

	attempts := 0
	api.On("AddChannelMember", mock.Anything, mock.Anything).Return(
		func(_, _ string) *model.ChannelMember {
			attempts++
			if attempts == 1 {
				return nil
			}

			return &model.ChannelMember{}
		},
		func(_, _ string) *model.AppError {
			if attempts == 1 {
				return &model.AppError{Message: "briefly unhappy"}
			}

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
	p.botUserID = "uid-bot"

	require.Equal(t, "chan-bridge", p.channelIDFor("villanet/town-square"))
	require.Equal(t, 1, attempts, "the first attempt failed")

	require.Equal(t, "chan-bridge", p.channelIDFor("villanet/town-square"))
	require.Equal(t, 2, attempts, "and is tried again rather than cached away")

	require.Equal(t, "chan-bridge", p.channelIDFor("villanet/town-square"))
	require.Equal(t, 2, attempts, "once it works, it is not asked again")
}

// The description is a setting like the other two, and it goes through the same
// patch. An empty one is allowed — it is the one field of the three somebody
// might legitimately want blank — so it is not defaulted back.
func TestTheBotDescriptionIsWhateverWasTyped(t *testing.T) {
	api := &plugintest.API{}

	var asked []*model.Bot
	// No remembered bot, so a refused rename falls back to the default name
	// exactly as it did before the fallback learned to try the current one.
	api.On("KVGet", ensureBotKey).Return([]byte(nil), (*model.AppError)(nil))
	api.On("EnsureBotUser", mock.Anything).Return(
		func(bot *model.Bot) string {
			asked = append(asked, bot)

			return "uid-bot"
		},
		func(_ *model.Bot) error { return nil },
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

	cfg := &configuration{
		BotUsername:    "tv-bot",
		BotDisplayName: "The Telly",
		BotDescription: "Says when the telly is on.",
	}
	cfg.normalize()
	p.setConfiguration(cfg)
	p.ensureBot()

	require.Len(t, asked, 1)
	require.Equal(t, "Says when the telly is on.", asked[0].Description)

	blank := cfg.Clone()
	blank.BotDescription = "   "
	blank.normalize()
	p.setConfiguration(blank)
	p.ensureBot()

	require.Len(t, asked, 2)
	require.Equal(t, "", asked[1].Description, "a blank description stays blank")
}

// Mattermost refuses the WHOLE patch over a description longer than 1024 runes,
// which would take the username and display name down with it. Clamped here so
// a long one costs its own tail rather than the rename.
func TestATooLongBotDescriptionIsClampedRatherThanRefused(t *testing.T) {
	cfg := &configuration{BotDescription: strings.Repeat("é", maxBotDescription+200)}
	cfg.normalize()

	require.Equal(t, maxBotDescription, len([]rune(cfg.BotDescription)))

	long := &configuration{BotDisplayName: strings.Repeat("x", maxBotDisplayName+50)}
	long.normalize()
	require.Equal(t, maxBotDisplayName, len([]rune(long.BotDisplayName)))
}

// Changing only the description must still reach Mattermost: it is part of the
// same patch, and sameBotIdentity is what decides whether that patch happens.
func TestChangingOnlyTheDescriptionRenamesTheBot(t *testing.T) {
	base := &configuration{
		BotUsername:    "tv-bot",
		BotDisplayName: "The Telly",
		BotDescription: "Says when the telly is on.",
	}

	reworded := base.Clone()
	reworded.BotDescription = "Bridges Mumble, and remembers birthdays."
	require.False(t, sameBotIdentity(base, reworded))

	unchanged := base.Clone()
	unchanged.BannerText = "something else"
	require.True(t, sameBotIdentity(base, unchanged))
}

// A refused USERNAME must not throw away the rest of what an admin typed.
func TestTheFallbackKeepsTheTypedDisplayNameAndDescription(t *testing.T) {
	api := &plugintest.API{}

	var asked []*model.Bot
	// No remembered bot, so a refused rename falls back to the default name
	// exactly as it did before the fallback learned to try the current one.
	api.On("KVGet", ensureBotKey).Return([]byte(nil), (*model.AppError)(nil))
	api.On("EnsureBotUser", mock.Anything).Return(
		func(bot *model.Bot) string {
			asked = append(asked, bot)
			if bot.Username == "villa" {
				return ""
			}

			return "uid-bot"
		},
		func(bot *model.Bot) error {
			if bot.Username == "villa" {
				return errors.New(`username "villa" is already taken by a non-bot user`)
			}

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
	p.setConfiguration(&configuration{
		BotUsername:    "villa",
		BotDisplayName: "The Telly",
		BotDescription: "Says when the telly is on.",
	})

	p.ensureBot()

	require.Len(t, asked, 2)
	require.Equal(t, defaultBotUsername, asked[1].Username)
	require.Equal(t, "The Telly", asked[1].DisplayName, "only the username was refused")
	require.Equal(t, "Says when the telly is on.", asked[1].Description)
}

// plugin.json ships a default description, and the constant here is what that
// default is meant to be. Kept in step, so the placeholder an admin sees and
// the words the bot actually starts with cannot drift apart.
func TestTheShippedBotDescriptionMatchesTheManifest(t *testing.T) {
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
			defaults[setting.Key] = setting.Default
		}
	}

	require.Equal(t, defaultBotUsername, defaults["BotUsername"])
	require.Equal(t, defaultBotDisplayName, defaults["BotDisplayName"])
	require.Equal(t, defaultBotDescription, defaults["BotDescription"])
}

// The chat clamp appends an ellipsis, which is right for a relayed message and
// wrong for a field with a hard limit: it lands one rune OVER, and Mattermost
// refuses a 1025-rune description exactly as it refuses a 1225-rune one.
func TestTheBotClampAddsNothingToWhatItCuts(t *testing.T) {
	require.Equal(t, "abc", cutRunes("abc", 10))
	require.Equal(t, "ab", cutRunes("abcdef", 2))
	require.Equal(t, "", cutRunes("abcdef", 0))
	require.Equal(t, "éé", cutRunes("ééééé", 2), "counted in runes, not bytes")
	require.NotContains(t, cutRunes(strings.Repeat("a", 50), 10), "…")

	// And the chat clamp still behaves as the bridge needs it to.
	require.Equal(t, 11, len([]rune(clampRunes(strings.Repeat("a", 50), 10))),
		"clampRunes still appends its ellipsis, which is why it is not used for this")
}
