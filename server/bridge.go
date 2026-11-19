package main

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/shared/markdown"
	"golang.org/x/net/html"
	"layeh.com/gumble/gumble"
)

// The text bridge: Mumble chat in one direction, Mattermost in the other, over
// the same connection the voice roster already holds.
//
// ---------------------------------------------------------------------------
// Everything relayed carries its author's name IN THE TEXT, and that is not a
// stylistic choice. A plugin cannot post as somebody else. Mattermost's
// SanitizeProps strips override_username, override_icon_url and friends from
// every locally-originated post, and re-injects them only for posts flagged as
// coming from an INCOMING WEBHOOK — explicitly not for bots, OAuth apps or
// plugins (app/post.go: "there's no legitimate per-post override use case for
// them"). The plugin API exposes no webhook functions at all. So relayed
// messages arrive from one bot account with the sender's name written into the
// message, which is exactly what Matterbridge itself does when configured with
// a token rather than a webhook URL.
// ---------------------------------------------------------------------------
//
// Two rules keep this from becoming a hall of mirrors, and both are load
// bearing rather than defensive:
//
//  1. Nothing the plugin's own bot posts is ever sent back to Mumble.
//     MessageHasBeenPosted's own documentation warns that it fires for posts
//     created by plugins, INCLUDING the plugin that created the post.
//  2. Nothing a bot, a webhook or another plugin posts is sent either, and no
//     Mumble text from a hidden name is relayed.
//
//     That is NOT enough on its own, and the honest version matters here.
//     Matterbridge's recommended Mattermost setup is an ordinary account with
//     a password or a token — not a bot — so Mattermost marks its posts with
//     nothing at all and rule 2 cannot see it. Running both without naming its
//     account in "Mattermost accounts to ignore" is an endless loop, not a
//     doubling. mayRelay is the backstop for the day somebody forgets.

var (
	errNoBot      = errors.New("the plugin's bot account is not ready yet")
	errEmptyImage = errors.New("that file was empty")
	errBadImage   = errors.New("Mattermost would not accept that image; PNG or JPEG, please")
)

const (
	// What a person reads before it stops being a chat message. Mattermost's own
	// post limit is far larger, so this is about restraint rather than safety.
	maxBridgedRunes = 1500

	// What murmur will accept, in bytes, with room to spare under its 5,000
	// byte default. This one IS safety: over the limit the server rejects the
	// message rather than truncating it.
	maxMumbleBytes = 4000

	// How long a channel that would not resolve is left alone before trying
	// again. Long enough to stop a per-post query, short enough that fixing
	// the setting works without a restart.
	channelMissTTL = time.Minute
)

// channelIDFor turns the team/channel an admin typed into a channel id, once.
//
// Looked up rather than configured as an id because an admin types names, and
// cached against the exact setting that produced it: change the setting and it
// resolves again, change anything else and it does not. A failed lookup is not
// cached, so fixing a typo — or creating the channel — takes effect without a
// restart.
func (p *Plugin) channelIDFor(setting string) string {
	want := strings.TrimSpace(setting)
	if want == "" {
		return ""
	}

	p.bridgeLock.RLock()
	id, known := p.channelIDs[want]
	failedAt, failed := p.channelMisses[want]
	p.bridgeLock.RUnlock()

	if known {
		// Resolved before, but the bot may still not be IN it: joinBot can fail
		// on a transient error, and the result used to be discarded. Caching
		// the id therefore also cached the failure, and the birthday greeting
		// went out as a plain-text @all every day until somebody restarted the
		// server. Retried here instead, until it works once.
		p.bridgeLock.RLock()
		joined := p.channelJoined[id]
		p.bridgeLock.RUnlock()

		if !joined {
			p.joinBot(p.teamIDs[id], id)
		}

		return id
	}

	// A failure is remembered too, briefly. MessageHasBeenPosted runs for every
	// post on the whole server, so a channel that does not resolve — a typo, a
	// team renamed, a channel not created yet — would otherwise be a database
	// query per post for everybody, for ever. Remembered for a minute rather
	// than for good, so fixing it takes effect without a restart.
	if failed && time.Since(failedAt) < channelMissTTL {
		return ""
	}

	team, channel, found := strings.Cut(want, "/")
	if !found {
		p.API.LogWarn("A channel setting needs to be team/channel", "value", want)
		p.rememberChannelMiss(want)

		return ""
	}

	resolved, appErr := p.API.GetChannelByNameForTeamName(
		strings.TrimSpace(team), strings.TrimSpace(channel), false)
	if appErr != nil || resolved == nil {
		p.API.LogWarn("Could not find a configured channel", "value", want)
		p.rememberChannelMiss(want)

		return ""
	}

	p.bridgeLock.Lock()
	if p.channelIDs == nil {
		p.channelIDs = map[string]string{}
	}
	if p.teamIDs == nil {
		p.teamIDs = map[string]string{}
	}
	p.channelIDs[want] = resolved.Id
	p.teamIDs[resolved.Id] = resolved.TeamId
	delete(p.channelMisses, want)
	p.bridgeLock.Unlock()

	// Put the bot in the channel it posts to. Its posts arrive either way — a
	// plugin does not go through the membership check a client does — but a bot
	// outside the channel holds none of the roles that grant
	// use_channel_mentions, so its @all would reach nobody. See joinBot.
	p.joinBot(resolved.TeamId, resolved.Id)

	return resolved.Id
}

// joinBot puts the bot in a channel the plugin posts to.
//
// This is NOT cosmetic, which is what it was mistaken for. The permission that
// allows @all, @channel and @here is use_channel_mentions, and the built-in
// roles that grant it are channel_user, channel_guest and team_user —
// system_user, which is all a bot has by default, does not. Mattermost checks
// it against the POST'S AUTHOR, so a bot that is not in the channel can post
// perfectly well and its @all reaches nobody: it is delivered as ordinary text,
// highlighted for no one, notifying no one. That is exactly how the birthday
// greeting went out.
//
// And channel membership needs team membership first. AddChannelMember does
// not add it, so on a server where the bot had never been put on a team by
// hand, every call here failed — at Debug level, where nobody was looking.
func (p *Plugin) joinBot(teamID, channelID string) {
	botID := p.getBotUserID()
	if botID == "" {
		return
	}

	if teamID != "" {
		// DeleteAt matters. Removing somebody from a team leaves the row behind
		// with a deletion time on it, and GetTeamMember hands that row back
		// with no error — so a bot that HAS been removed from the team looks
		// like a member here, CreateTeamMember is skipped, and every
		// AddChannelMember after it fails for as long as the plugin runs.
		member, appErr := p.API.GetTeamMember(teamID, botID)
		if appErr != nil || member == nil || member.DeleteAt != 0 {
			if _, appErr := p.API.CreateTeamMember(teamID, botID); appErr != nil {
				p.API.LogWarn("Could not put the bot on the team, so @all in its posts will notify nobody",
					"team_id", teamID, "error", appErr.Error())

				return
			}
		}
	}

	// No DeleteAt to check here, unlike a team membership: channel membership
	// is a hard delete, so the row existing is the answer.
	member, appErr := p.API.GetChannelMember(channelID, botID)
	if appErr == nil && member != nil {
		// Already a member, which is the ordinary case: this runs whenever a
		// channel setting is resolved.
		p.rememberJoined(channelID)

		return
	}

	if _, appErr := p.API.AddChannelMember(channelID, botID); appErr != nil {
		p.API.LogWarn("Could not add the bot to a channel it posts to, so @all in its posts there will notify nobody",
			"channel_id", channelID, "error", appErr.Error())

		return
	}

	p.rememberJoined(channelID)
}

func (p *Plugin) rememberJoined(channelID string) {
	p.bridgeLock.Lock()
	if p.channelJoined == nil {
		p.channelJoined = map[string]bool{}
	}
	p.channelJoined[channelID] = true
	p.bridgeLock.Unlock()
}

func (p *Plugin) rememberChannelMiss(setting string) {
	p.bridgeLock.Lock()
	defer p.bridgeLock.Unlock()

	if p.channelMisses == nil {
		p.channelMisses = map[string]time.Time{}
	}
	p.channelMisses[setting] = time.Now()
}

// ------------------------------------------------------- Mumble to Mattermost

func (p *Plugin) relayFromMumble(event *gumble.TextMessageEvent, cfg *configuration) {
	if !cfg.EnableChatBridge || event == nil || event.Sender == nil {
		return
	}

	// Addressed to a channel or a tree, never to the plugin alone. A Mumble
	// user can send a private message straight to this connection, and
	// reposting that into a public channel would publish something somebody
	// deliberately said in private.
	if len(event.Channels) == 0 && len(event.Trees) == 0 {
		return
	}

	sender := strings.TrimSpace(event.Sender.Name)
	if sender == "" {
		return
	}

	// The bridge that is being replaced, the plugin itself, and anything else
	// an admin has listed as furniture.
	if cfg.hiddenVoiceNames()[strings.ToLower(sender)] ||
		strings.EqualFold(sender, cfg.MumbleUsername) {
		return
	}

	text := clampRunes(oneLine(defuseLinks(defuseMentions(htmlToText(event.Message)))), maxBridgedRunes)
	if text == "" {
		return
	}

	channelID := p.channelIDFor(cfg.BridgeChannel)
	botID := p.getBotUserID()
	if channelID == "" || botID == "" || !p.mayRelay() {
		return
	}

	if _, appErr := p.API.CreatePost(&model.Post{
		UserId:    botID,
		ChannelId: channelID,
		Message:   "**" + escapeMarkdown(defuseName(sender)) + "**: " + text,
	}); appErr != nil {
		p.API.LogWarn("Could not relay a Mumble message", "error", appErr.Error())
	}
}

// ------------------------------------------------------- Mattermost to Mumble

// MessageHasBeenPosted relays what people type in the bridged channel.
//
// This runs for every post on the whole server, so the cheap comparisons come
// first and the user lookup comes last.
func (p *Plugin) MessageHasBeenPosted(c *plugin.Context, post *model.Post) {
	// A post made with a session says that session is alive: push
	// remembers the last few, in memory (pushSessionGone).
	if c != nil {
		p.pushes().noteAlive(c.SessionId)
	}

	author := p.relayableAuthor(post, p.getConfiguration())
	if author == nil {
		return
	}

	p.sendToMumble(displayNameOf(author) + ": " + post.Message)
}

// relayableAuthor returns the person whose message should go to Mumble, or nil
// when this post is not one.
//
// Separate from the hook so the rules can be tested as rules. Everything that
// has ever gone wrong with a chat bridge lives in this function.
func (p *Plugin) relayableAuthor(post *model.Post, cfg *configuration) *model.User {
	if post == nil || post.Message == "" || !cfg.EnableChatBridge {
		return nil
	}

	// Joins, leaves, header changes: Mattermost's own housekeeping, not
	// somebody talking.
	if strings.HasPrefix(post.Type, "system_") {
		return nil
	}

	// Rule 1: never our own echo. MessageHasBeenPosted fires for posts created
	// by plugins, including the plugin that created the post — its own
	// documentation says so.
	if post.UserId == p.getBotUserID() {
		return nil
	}

	// Rule 2: no integration. Mattermost sets these two props itself —
	// from_bot for a bot account, from_webhook for an incoming webhook.
	if post.GetProp("from_webhook") == "true" ||
		post.GetProp("from_bot") == "true" ||
		post.GetProp("from_plugin") == "true" {
		return nil
	}

	// Last, because it is the only one that costs a lookup.
	if post.ChannelId != p.channelIDFor(cfg.BridgeChannel) {
		return nil
	}

	user, appErr := p.API.GetUser(post.UserId)
	if appErr != nil || user == nil || user.IsBot {
		return nil
	}

	// Rule 3, and the one that matters most in practice. Matterbridge does NOT
	// post as a bot: its recommended Mattermost setup is a login and password,
	// or a personal access token, on an ORDINARY account — and Mattermost sets
	// neither prop for those, so nothing above can tell it from a person.
	//
	// Left unguarded, that is a genuine infinite loop rather than a doubling.
	// Somebody speaks in Mumble; Matterbridge posts it to Mattermost as a
	// person; this relays that back to Mumble; Matterbridge sees it there and
	// posts it again, for ever, as fast as two servers can manage.
	if cfg.ignoredBridgeAccounts()[strings.ToLower(user.Username)] {
		return nil
	}

	return user
}

// relayBudget is a circuit breaker for the loop above.
//
// Every guard in relayableAuthor depends on somebody having configured
// something correctly, and the failure mode if they have not is two servers
// shouting at each other until somebody notices. A bridge between eight people
// does not legitimately carry a message a second, so this stops rather than
// discovering how fast Mumble can go.
const (
	relayBurst  = 30
	relayWindow = time.Minute
)

// mayRelay reports whether this message is inside the budget, and complains
// exactly once when it is not.
func (p *Plugin) mayRelay() bool {
	now := time.Now()

	p.bridgeLock.Lock()
	defer p.bridgeLock.Unlock()

	if now.Sub(p.relayWindowStart) > relayWindow {
		p.relayWindowStart = now
		p.relayCount = 0
		p.relayStopped = false
	}

	p.relayCount++
	if p.relayCount <= relayBurst {
		return true
	}

	if !p.relayStopped {
		p.relayStopped = true
		p.API.LogWarn(
			"Stopped relaying to Mumble for a minute: more than " +
				strconv.Itoa(relayBurst) + " messages in one. If another bridge is still " +
				"running, add its Mattermost account to the ignore list.")
	}

	return false
}

// sendToMumble writes one line to the whole server.
//
// Addressed to the root channel as a TREE rather than to a channel, so it
// reaches everybody wherever they are sitting. The plugin never joins the voice
// channel, and a message addressed to a channel it is not in would reach nobody.
func (p *Plugin) sendToMumble(text string) {
	text = clampRunes(strings.TrimSpace(text), maxBridgedRunes)
	if text == "" || !p.mayRelay() {
		return
	}

	p.voiceLock.RLock()
	client := p.voiceClient
	p.voiceLock.RUnlock()

	if client == nil {
		return
	}

	var root *gumble.Channel
	client.Do(func() {
		root = client.Channels[0]
	})

	if root == nil {
		return
	}

	// gumble serialises writes on the connection itself, so this is safe from
	// the hook's goroutine.
	// Escaped first, then bounded, because escaping is what makes it grow.
	client.Send(&gumble.TextMessage{
		Trees:   []*gumble.Channel{root},
		Message: clampBytes(html.EscapeString(text), maxMumbleBytes),
	})
}

// ------------------------------------------------------------------ plumbing

// htmlToText flattens a Mumble message.
//
// Mumble chat is HTML — the desktop client sends rich text, and a pasted image
// arrives as a data: URI inside an <img> that can be megabytes long. Parsed
// rather than pattern-matched, because this is untrusted input from anybody who
// can connect to the voice server, and a regular expression over hostile HTML
// is a bug waiting for the right message.
func htmlToText(source string) string {
	node, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return ""
	}

	var out strings.Builder

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch {
		case n.Type == html.TextNode:
			out.WriteString(n.Data)
		case n.Type == html.ElementNode && n.Data == "br":
			out.WriteString("\n")
		case n.Type == html.ElementNode && n.Data == "img":
			// Never the source, which is the whole image.
			out.WriteString("[image]")
			return
		case n.Type == html.ElementNode && (n.Data == "p" || n.Data == "div"):
			out.WriteString("\n")
		}

		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)

	// Collapse the blank lines the block elements above leave behind.
	lines := strings.Split(out.String(), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}

	return strings.Join(kept, "\n")
}

// zeroWidthSpace is what defuses a mention: put after the @, it leaves the
// text reading exactly as it was typed, and Mattermost splits a word on it,
// so what is left is an @ on its own and a plain word after it.
const zeroWidthSpace = "\u200b"

// defuseLinks stops relayed text becoming a picture or a link that reads as
// one address and goes to another, posted by the bot for somebody with no
// Mattermost account. Mumble's own HTML is flattened to its text, but text
// typed as Markdown, ![ ](https://tracker.example/p.png) or
// [https://chat.example.com](https://elsewhere.example), would otherwise be
// drawn by every reader's client, which fetches the picture straight from
// wherever it is. An inline link or image needs "](" with nothing between, so
// a zero-width space there leaves the text reading as typed and inert. A
// reference-style link cannot be made at all: its definition has to start a
// line, and a relayed message is one line that starts with the sender's
// name. A bare address still becomes a link, and that shows where it goes.
func defuseLinks(value string) string {
	return strings.ReplaceAll(value, "](", "]"+zeroWidthSpace+"(")
}

// defuseMentions stops relayed text ringing everybody's phone.
//
// Anybody who can connect to the voice server can type @all, and without this
// it becomes a real Mattermost mention — somebody in Mumble notifying every
// member of the team, from outside Mattermost, with no account and no
// permission to do it. The bot can do it, because it is a member of the
// channel it posts to (joinBot), so its @all is honoured.
//
// In any case at all. Mattermost lower-cases a word before comparing it with
// @all, @channel and @here, so @aLL rings every phone exactly as @all does.
// This used to be a list of the spellings somebody had thought of — all, All
// and ALL — and anything else went straight through.
//
// And wherever Mattermost thinks the word ends, which is not where a regular
// expression's \b would say. It takes a trailing "_", ".", "-" or ":" off a
// word before comparing, so @all_ is @all too. So the word is taken to have
// ended unless a letter or a digit follows it: @allison is somebody's name,
// and @all_ is not.
//
// And as Mattermost reads it, which is after it has decoded the post's
// Markdown: see defuseReferences, which runs first.
//
// Only the three broadcast words. @victor is a person talking about a person
// and should keep working.
func defuseMentions(value string) string {
	value = defuseReferences(value)

	var out strings.Builder
	out.Grow(len(value))

	for {
		at := strings.IndexByte(value, '@')
		if at < 0 {
			break
		}

		out.WriteString(value[:at+1])
		value = value[at+1:]

		if startsBroadcastWord(value) {
			out.WriteString(zeroWidthSpace)
		}
	}
	out.WriteString(value)

	return out.String()
}

// startsBroadcastWord reports whether value, the text just after an @, starts
// with all, channel or here, in any case, as a whole word.
//
// Only an ASCII letter or digit carries the word on. Mattermost's server
// would count an accented letter too, but the webapp's own pattern would not,
// and ending a word too soon only ever defuses something that was harmless.
func startsBroadcastWord(value string) bool {
	for _, word := range []string{"all", "channel", "here"} {
		if len(value) < len(word) || !strings.EqualFold(value[:len(word)], word) {
			continue
		}

		rest := value[len(word):]

		return rest == "" || !isASCIILetterOrDigit(rest[0])
	}

	return false
}

func isASCIILetterOrDigit(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// defuseReferences writes the & of every character reference in value as
// &amp;, so that Mattermost shows the reference as it was typed rather than
// decoding it.
//
// A post is Markdown, and Mattermost looks for mentions in the text its
// Markdown parser makes of it, where &#64; is an @ and &#108; an l. So
// "@al&#108;" is @all by the time Mattermost looks, with no "all" anywhere for
// defuseMentions to find, and "&#64;all" has no @. Every one of them is
// written so that it decodes to a plain & followed by the rest as typed.
//
// Nothing a person sees is lost by it. Mumble's chat is HTML, and htmlToText
// has already decoded that, so a reference still here is one somebody typed
// as text, and it now reads exactly as they typed it. The one place it shows
// is inside `code`, where Markdown decodes nothing and the &amp; is left as
// it is.
//
// Which & starts a reference is Mattermost's own parser's answer
// (markdown.CharacterReference, up to the first ";" after it, as its
// parseCharacterReference reads one), not a list of the ones somebody thought
// of.
func defuseReferences(value string) string {
	var out strings.Builder
	out.Grow(len(value))

	for {
		at := strings.IndexByte(value, '&')
		if at < 0 {
			break
		}

		out.WriteString(value[:at])
		value = value[at+1:]

		if end := strings.IndexByte(value, ';'); end >= 0 && markdown.CharacterReference(value[:end]) != "" {
			out.WriteString("&amp;")
		} else {
			out.WriteString("&")
		}
	}
	out.WriteString(value)

	return out.String()
}

// defuseName keeps a Mumble name from mentioning anybody.
//
// The name goes into the post as the bold part of "**name**: message", and
// Mattermost reads a mention straight through the bold: it splits words on
// the asterisks, so "**@all**: hello" holds the word @all, and somebody who
// calls themselves @all in Mumble makes the bot post a real @all with every
// message they send. Murmur's default rule for names allows an @, and a voice
// server somebody else runs can call a person anything.
//
// Every @ rather than the three words. A name says who is talking, so it never
// needs to mention anybody, @victor included. And its references first, as
// defuseMentions does, so that &#64; cannot stand in for one.
func defuseName(name string) string {
	return strings.ReplaceAll(defuseReferences(name), "@", "@"+zeroWidthSpace)
}

// oneLine keeps a relayed message from impersonating anybody.
//
// htmlToText emits newlines for block elements, so a Mumble message can carry
// them — and a newline plus another bold name is a post that looks exactly like
// two relayed messages, the second from somebody who never said it. A relayed
// line is one line.
func oneLine(value string) string {
	return strings.Join(strings.FieldsFunc(value, func(r rune) bool {
		return r == '\n' || r == '\r'
	}), " \u23ce ")
}

// escapeMarkdown keeps a Mumble name from becoming formatting. Only the name is
// escaped: a message is somebody talking, and people write asterisks.
func escapeMarkdown(value string) string {
	replacer := strings.NewReplacer(
		"\\", "\\\\", "*", "\\*", "_", "\\_", "`", "\\`",
		"~", "\\~", "|", "\\|", "[", "\\[", "]", "\\]",
	)

	return replacer.Replace(value)
}

func clampRunes(value string, limit int) string {
	value = strings.TrimSpace(value)
	if utf8.RuneCountInString(value) <= limit {
		return value
	}

	return strings.TrimSpace(string([]rune(value)[:limit])) + "…"
}

// clampBytes is the one that matters going the other way.
//
// Mumble's message limit is measured in BYTES, and the text is HTML-escaped
// before it is sent — so a line of emoji, or one full of angle brackets, can be
// several times its own length by the time murmur counts it. Over the limit the
// server does not truncate, it rejects the message and may drop the connection.
//
// Cut on a rune boundary, so the result is never invalid UTF-8.
func clampBytes(value string, limit int) string {
	if len(value) <= limit {
		return value
	}

	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}

	return strings.TrimSpace(value[:cut])
}

// getBotUserID returns the bot's id, creating the account if an earlier attempt
// failed.
//
// Retried rather than settled at activation. EnsureBotUser can fail for reasons
// that pass — the database being briefly unavailable while Mattermost starts —
// and giving up permanently means a bridge that is switched on, configured, and
// silent until somebody restarts the server, with nothing on screen to say why.
func (p *Plugin) getBotUserID() string {
	p.bridgeLock.RLock()
	botID := p.botUserID
	since := time.Since(p.botTriedAt)
	p.bridgeLock.RUnlock()

	if botID != "" {
		return botID
	}

	// Not on every call. MessageHasBeenPosted runs for every post on the whole
	// server, so a bot that cannot be created — a name permanently taken —
	// would otherwise mean an EnsureBotUser round trip, ending in a failed
	// PatchBot, per message anybody sends anywhere, for as long as the plugin
	// runs. The retry is still there, because the ordinary reason for this is
	// the database being briefly unavailable during startup; it is just paced.
	if since < botRetryInterval {
		return ""
	}

	p.bridgeLock.Lock()
	p.botTriedAt = time.Now()
	p.bridgeLock.Unlock()

	p.ensureBot()

	p.bridgeLock.RLock()
	defer p.bridgeLock.RUnlock()

	return p.botUserID
}

// maxAvatarBytes bounds what an admin can upload. Mattermost resizes profile
// images server-side, so this only has to stop something absurd reaching it.
const maxAvatarBytes = 2 << 20

// setBotAvatar puts an uploaded image on the plugin's bot.
//
// The image goes to Mattermost's own user-image store rather than into the
// plugin's configuration: a few hundred kilobytes of base64 in a config value
// would be written to disk, held in memory and shipped to every node on every
// configuration change, for a picture.
func (p *Plugin) setBotAvatar(image []byte) error {
	botID := p.getBotUserID()
	if botID == "" {
		return errNoBot
	}

	if len(image) == 0 {
		return errEmptyImage
	}

	if appErr := p.API.SetProfileImage(botID, image); appErr != nil {
		p.API.LogWarn("Could not set the bot avatar", "error", appErr.Error())

		return errBadImage
	}

	return nil
}

// ensureBot creates the account the plugin posts as, and renames it when an
// admin has changed what it is called.
//
// EnsureBotUser is not only a create. It remembers the bot's id in the plugin's
// own KV store, and when that id still resolves it PATCHES the account with the
// username, display name and description passed in. So handing it a different
// name renames the existing bot rather than leaving a second one behind — which
// is what makes renaming safe, and why nothing here looks the old name up.
//
// A username already taken by somebody else is refused by Mattermost. The
// account keeps the name it had, and the reason is put where an admin will see
// it rather than only in the log.
//
// One bot, owned by the plugin, rather than a personal access token an admin
// has to make and keep — which is the whole point of moving the bridge in here.
func (p *Plugin) ensureBot() {
	cfg := p.getConfiguration()

	username := cfg.BotUsername
	if username == "" {
		username = defaultBotUsername
	}

	displayName := cfg.BotDisplayName
	if displayName == "" {
		displayName = defaultBotDisplayName
	}

	botID, err := p.ensureBotNamed(username, displayName, cfg.BotDescription)
	if err == nil {
		p.setBotProblem("")
		p.rememberBot(botID)

		return
	}

	p.API.LogWarn("Could not create or rename the plugin's bot", "username", username, "error", err.Error())
	refusal := err.Error()

	// A name Mattermost will not take must not cost the plugin its bot.
	//
	// EnsureBotUser patches the account it already has, so a username somebody
	// else owns makes the whole call fail — and it fails BEFORE handing back
	// the id of the perfectly good bot it just looked up. Taking that as the
	// answer leaves botUserID empty, which is not "the rename did not happen";
	// it is no bot at all. Birthdays stop, the bridge goes silent, and nothing
	// on screen connects that to the name typed days earlier.
	//
	// So it is asked again under a name that cannot collide. First choice: the
	// name the account ALREADY has, which it holds by definition. The default
	// name used to be the only fallback, and that stops being safe the moment
	// some other account is sitting on it — which is exactly what happens after
	// the plugin takes over an older bot and retires its own: the retired one
	// keeps "villanet-tv", and a refused rename would then have found both
	// names taken and left the plugin with no bot.
	//
	// The default name stays as the second choice, for a fresh install where
	// there is no remembered account to ask about.
	//
	// The description and display name are kept either way: it was the
	// USERNAME Mattermost refused, and there is no reason to throw away the
	// rest of what an admin typed along with it.
	for _, fallbackName := range []string{p.storedBotUsername(), defaultBotUsername} {
		if fallbackName == "" || fallbackName == username {
			continue
		}

		if fallback, retryErr := p.ensureBotNamed(fallbackName, displayName, cfg.BotDescription); retryErr == nil {
			p.setBotProblem(refusal + ` — the bot has been left as "` + fallbackName + `".`)
			p.rememberBot(fallback)

			return
		}
	}

	p.setBotProblem(refusal)
}

func (p *Plugin) ensureBotNamed(username, displayName, description string) (string, error) {
	return p.API.EnsureBotUser(&model.Bot{
		Username:    username,
		DisplayName: displayName,
		Description: description,
	})
}

func (p *Plugin) rememberBot(botID string) {
	p.bridgeLock.Lock()
	p.botUserID = botID
	p.bridgeLock.Unlock()
}

// setBotProblem records why the bot could not be created or renamed, for the
// settings page. Renaming fails for exactly one interesting reason — the name
// is taken — and an admin who has just typed a new name and seen nothing happen
// deserves to be told which.
func (p *Plugin) setBotProblem(reason string) {
	p.bridgeLock.Lock()
	p.botProblem = reason
	p.bridgeLock.Unlock()
}

func (p *Plugin) botProblemNow() string {
	p.bridgeLock.RLock()
	defer p.bridgeLock.RUnlock()

	return p.botProblem
}
