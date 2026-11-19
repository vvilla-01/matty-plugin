package main

import (
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// A bearer token is refused before it can be posted (design/lock-screen.md
// §4.5). Every person must now paste their token at least once, and a paste
// that misses the lock screen's bar lands in Mattermost's message box, where
// the next Enter posts it to the channel. The webapp refuses such a post in
// its own MessageWillBePosted hook while the page is not linked or any token
// bar is open; this is the same rule kept on the server, so a phone, another
// tab or anything else that posts gets it too.
//
// Its limits, as the design states them:
//
//   - It is a belt, not the defence. With synced drafts on, which is
//     Mattermost's default, text that lands in the message box is saved on
//     the server as a draft before anybody presses Enter, and no plugin hook
//     sees a draft. The webapp's key rule, which puts a paste in the bar
//     instead, is what keeps a token out of the box.
//   - It knows a token only by its shape: Broadcast Box's are UUIDs, so a
//     message that is nothing but a UUID, with or without one "Bearer " in
//     front, is what it looks for. A UUID inside a sentence is not refused.
//   - The server cannot see whether a token bar is open. So a linked person's
//     UUID-shaped message is refused only when it is the token they linked
//     with; anybody not linked has every UUID-shaped message refused, while
//     the plugin is set up.
//   - Bots, webhooks and Mattermost's own system messages are left alone:
//     none of them holds a bearer token, and refusing an integration's post
//     would break it. Mattermost sets from_bot before this hook runs [MM
//     v9.3.0 server/channels/app/post.go:238-240, hook at :305; master :269,
//     :410].
//   - Moving a thread re-posts its root through this hook, so a thread whose
//     root is nothing but a UUID, posted by somebody not linked, cannot be
//     moved. Accepted: such a root is exactly what this exists to stop.

// bearerTokenPostRefused is what somebody who posts their bearer token is
// told, with the server's name for the plugin (brandName).
func bearerTokenPostRefused(name string) string {
	return "That looks like your bearer token. Paste it into the bar in the " + name + " panel instead."
}

// looksLikeBearerToken is the message as a token, trimmed and with one leading
// "Bearer " taken off as handleLink does, when that is all it is: a UUID.
func looksLikeBearerToken(message string) (string, bool) {
	// Cheap first: this runs for every post on the server, and almost none is
	// 36 characters, or starts with "Bearer ".
	trimmed := strings.TrimSpace(message)
	if len(trimmed) < 36 || len(trimmed) > 36 && !strings.EqualFold(trimmed[:7], "bearer ") {
		return "", false
	}

	candidate := cleanStreamKey(trimmed)
	if len(candidate) != 36 {
		return "", false
	}

	for i := 0; i < len(candidate); i++ {
		c := candidate[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return "", false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return "", false
			}
		}
	}

	return candidate, true
}

// tokenPostRefusal is why a post or an edit must not go out, or "". Nothing
// about the message is ever logged.
func (p *Plugin) tokenPostRefusal(post *model.Post) string {
	if post == nil {
		return ""
	}

	candidate, shaped := looksLikeBearerToken(post.Message)
	if !shaped {
		return ""
	}

	if post.UserId == "" || strings.HasPrefix(post.Type, model.PostSystemMessagePrefix) {
		return ""
	}
	props := post.GetProps()
	if propText(props, model.PostPropsFromBot) == "true" || propText(props, model.PostPropsFromWebhook) == "true" {
		return ""
	}

	// Not set up, there is no bar to paste it into, and nothing to unlock.
	if p.getConfiguration().validate() != nil {
		return ""
	}

	level, _, err := p.accessOf(post.UserId)
	switch {
	case err != nil:
		// Something shaped like a credential, from somebody whose access
		// cannot be read just now: kept back rather than posted on a guess.
		return bearerTokenPostRefused(p.brandName())
	case level != accessLinked:
		return bearerTokenPostRefused(p.brandName())
	}

	if stored := cleanStreamKey(p.getLinkedToken(post.UserId)); stored != "" && strings.EqualFold(stored, candidate) {
		return bearerTokenPostRefused(p.brandName())
	}

	return ""
}
