package main

import (
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
)

// VillaNET's own posts: a shared clip (clips_share.go) and, from 3.2.0, a
// shared plan (plan_share.go). Each is a post of a type of its own that every
// reader's webapp draws as a card, and Mattermost lets anybody make a post of
// any custom type, or ask through props.type for any post to be drawn as one.
// So the two hooks here keep everybody but VillaNET from making or editing a
// card of either kind, by one table rather than a copy of the rules per card.
//
// The rules are the clip card's, unchanged (design/clips-addendum.md §4.2,
// design/3.2.0.md §6.4): only a one-time pass this node issued for exactly
// that person, channel, kind and id, in the last half minute, makes a card;
// any other post of a card type becomes its copy type with every VillaNET prop
// blanked; and an edit can change neither the message nor the props.

// cardType is one kind of VillaNET post: what a post of the type becomes when
// VillaNET did not make it, the prop that names what it is about, and what
// an author who tries to edit one is told.
type cardType struct {
	copyType    string
	idProp      string
	editRefused string
}

// cardTypes is every kind of post VillaNET makes.
var cardTypes = map[string]cardType{
	clipPostType: {copyType: clipPostCopyType, idProp: clipPropID, editRefused: clipPostEditRefused},
	planPostType: {copyType: planPostCopyType, idProp: planPropID, editRefused: planPostEditRefused},
}

// sharePass is the one-time pass a share's post carries, and what it is good
// for: one post, by one person, into one channel, of one card of one kind.
// kind is the card's post type, and a pass that does not say is a clip card's,
// as every pass was before plans could be shared.
type sharePass struct {
	expires   time.Time
	userID    string
	channelID string
	clipID    string

	kind   string
	planID string
}

// postType is the type of post the pass is for.
func (pass sharePass) postType() string {
	if pass.kind == "" {
		return clipPostType
	}

	return pass.kind
}

// cardID is the clip or the plan the pass is for.
func (pass sharePass) cardID() string {
	if pass.postType() == planPostType {
		return pass.planID
	}

	return pass.clipID
}

// issue hands out a pass, forgetting any that have run out.
func (c *clipShares) issue(value string, pass sharePass, now time.Time) {
	c.passes.Range(func(key, stored any) bool {
		if old, ok := stored.(sharePass); !ok || !now.Before(old.expires) {
			c.passes.Delete(key)
		}

		return true
	})

	c.passes.Store(value, pass)
}

// redeem takes the pass a post carries, if this node issued it in the last
// half minute for exactly this post: this person, this channel, this kind of
// card and this clip or plan, at the root of the channel. Either way the pass
// is used up: a pass is good for one post, and only the one it was issued for.
func (c *clipShares) redeem(post *model.Post, now time.Time) bool {
	props := post.GetProps()

	value := propText(props, clipPropNonce)
	if value == "" {
		return false
	}

	stored, found := c.passes.LoadAndDelete(value)
	if !found {
		return false
	}
	pass, ok := stored.(sharePass)
	if !ok {
		return false
	}

	card, carded := cardTypes[post.Type]

	return carded && now.Before(pass.expires) &&
		pass.postType() == post.Type &&
		pass.userID == post.UserId &&
		pass.channelID == post.ChannelId &&
		pass.cardID() == propText(props, card.idProp) &&
		post.RootId == ""
}

// claimsCard is whether a post's props ask for it to be drawn as a card: a
// props.type that names one of VillaNET's card types, or one that is not a
// string at all. Mattermost's webapp up to 9.11 used props.type as a key
// without checking what it is, so ["custom_villanet_clip"] found the card as
// readily as the string did [v9.11.0 W/components/post_view/
// post_message_view/post_message_view.tsx:118]. 11.11's ignores one that is
// not a string [v11.11.1 W/.../post_message_view.tsx:140], but a post
// outlives the client that reads it, and Mattermost and integrations only
// ever put a string there, so taking off anything else still costs nobody
// anything.
func claimsCard(props model.StringInterface) bool {
	claim, present := props["type"]
	if !present || claim == nil {
		return false
	}

	text, isText := claim.(string)
	if !isText {
		return true
	}

	_, carded := cardTypes[text]

	return carded
}

// MessageWillBePosted runs for every post on the server, those this plugin
// makes included, so it does a string comparison and a map lookup and goes
// for everything that is not, or does not claim to be, a card.
//
//   - A post of a card type carrying a pass this node issued for exactly it,
//     in the last half minute, is a share: the pass is blanked and it is
//     posted.
//   - Any other post of a card type is posted as an ordinary message, of the
//     card's copy type, with every VillaNET prop blanked, rather than
//     refused. Mattermost's own copies of a post go through here too —
//     moving a thread re-posts its root, and a refusal would abandon the
//     whole move — so a forged card, or a copy of a real one, arrives as
//     what it says in plain text.
//   - Any other post that asks, through its props, to be drawn as a card has
//     that prop blanked.
//   - Before all of that, a post that is nothing but a bearer token is
//     refused (see tokenguard.go), which costs a trim and a length check for
//     every post that is not one.
//
// Every change is an overwrite with something that is not a zero value,
// never a deletion. Mattermost decodes the hook's answer into the very post
// it sent [v9 server/public/plugin/client_rpc.go:641; the same at master
// :1128], and gob sends no field that is zero and adds map entries to the map
// already there: a type set to "" would come back as the type the post had,
// and a prop deleted here would come back as it was. "" is what the webapp
// reads as absent.
func (p *Plugin) MessageWillBePosted(_ *plugin.Context, post *model.Post) (*model.Post, string) {
	if post == nil {
		return nil, ""
	}

	if refusal := p.tokenPostRefusal(post); refusal != "" {
		return nil, refusal
	}

	card, carded := cardTypes[post.Type]
	if !carded && !claimsCard(post.GetProps()) {
		return nil, ""
	}

	props := copyProps(post.GetProps())

	if !carded {
		props["type"] = ""
		post.SetProps(props)

		return post, ""
	}

	s := p.clips()
	if s.shares.redeem(post, s.now()) {
		props[clipPropNonce] = ""
		post.SetProps(props)

		return post, ""
	}

	post.Type = card.copyType
	for key := range props {
		if strings.HasPrefix(key, clipPropPrefix) || key == "type" {
			props[key] = ""
		}
	}
	post.SetProps(props)

	return post, ""
}

// MessageWillBeUpdated runs for every edit on the server, the marker's own
// and a pin's included. Handing back nil refuses the edit, so it does that
// for two things only: an edit that makes a message a bearer token (see
// tokenguard.go), and a change to a card's message, which is VillaNET's
// fixed text, and whose link an author could otherwise point anywhere for
// every reader on a phone, while the web showed a genuine card.
//
// Otherwise a card keeps the props it had, whatever the edit says, and for a
// clip a mark the marker is about to make is applied, whoever's edit this is.
// Nothing its author adds is kept: a prop that hangs a button or blocks off
// the card, styles it as a bot's, or passes it off as an integration's is as
// unwelcome as a change to VillaNET's own, and there is no list of such
// props that a later Mattermost could not outgrow. The one exception is
// hiding a link preview, which only ever takes something away. Files are
// put back as they were too — Mattermost lets an edit add them — and so is
// the edit time, so that an edit that changed nothing shows no "(edited)".
//
// Any other post that asks, through its props, to be drawn as a card has
// that prop taken off. Nothing here waits on anything the share routes or the
// marker hold while they call Mattermost.
//
// Unlike MessageWillBePosted's, this hook's answer is decoded into a post of
// its own [v11.11.1 server/public/plugin/client_rpc.go:935-947, as from
// v9.3.0], so a nil refuses and a prop left out is gone. Mattermost before
// 9.3 decoded it into the edited post, a nil was lost and the edit went
// through; plugin.json's min_server_version of 11.11.0 is well past that.
func (p *Plugin) MessageWillBeUpdated(_ *plugin.Context, newPost, oldPost *model.Post) (*model.Post, string) {
	if newPost == nil || oldPost == nil {
		return newPost, ""
	}

	// An edit that turns a message into a bearer token is refused as a post
	// would be. Only when the words changed: a pin or a reaction is an edit
	// too, and must not be refused over what was posted before the lock.
	if newPost.Message != oldPost.Message {
		if refusal := p.tokenPostRefusal(newPost); refusal != "" {
			return nil, refusal
		}
	}

	card, carded := cardTypes[oldPost.Type]
	if !carded {
		if claimsCard(newPost.GetProps()) {
			props := copyProps(newPost.GetProps())
			delete(props, "type")
			newPost.SetProps(props)
		}

		return newPost, ""
	}

	if newPost.Message != oldPost.Message {
		return nil, card.editRefused
	}

	props := copyProps(oldPost.GetProps())
	if propText(newPost.GetProps(), clipPropRemoveLinkPreview) == "true" {
		props[clipPropRemoveLinkPreview] = "true"
	}

	// Gone marks are a clip's alone: a plan's card reads whether its plan is
	// still on, and everything else about it, from the snapshot.
	if oldPost.Type == clipPostType {
		if stored, pending := p.clips().shares.marks.Load(newPost.Id); pending {
			if mark, ok := stored.(pendingMark); ok && mark.clipID == propText(props, clipPropID) {
				applyGoneMark(props, mark.value)
			}
		}
	}

	newPost.SetProps(props)
	newPost.FileIds = append(model.StringArray(nil), oldPost.FileIds...)
	newPost.EditAt = oldPost.EditAt

	return newPost, ""
}
