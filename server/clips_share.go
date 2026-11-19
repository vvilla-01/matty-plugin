package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Sharing a clip (A§4). Share posts a clip into the channel the person has
// open, as them, as a post of VillaNET's own type, which every reader's
// webapp draws as a card. What the post shows in plain sight — its message,
// which is what phones, the Threads list, pushes, email and search see — is
// VillaNET's fixed text and a link, never a title or a name, so nothing
// anybody typed can notify, link or format there, and nothing of a deleted
// clip outlives it there. The title and the names ride in the post's props
// for the card, and are taken out again when the clip is deleted.
//
// Mattermost checks next to nothing about a post a plugin makes, and lets
// anybody make a post of any custom type, so there are two halves: the route
// here checks what Mattermost would have checked of the person themselves,
// and two hooks keep everybody else from making or editing a card. The hooks,
// and the one-time passes they redeem, live in share_cards.go, where they
// guard a shared plan's card (plan_share.go) by the same rules.

const (
	clipPostType = "custom_villanet_clip"

	// What a post of VillaNET's type becomes when this node did not make it:
	// a forged card, or Mattermost's own copy of a real one. No webapp has a
	// card for this type, so every client draws the post as the plain
	// message it is. The hook cannot simply take the type off: Mattermost
	// reads the hook's answer into the post it sent, field by field, and a
	// type of "" is never sent, so the post would keep the one it had. See
	// MessageWillBePosted.
	clipPostCopyType = "custom_villanet_clip_copy"

	// What a shared clip's post carries for its card. Only what a reader who
	// is not linked may see, since every member of the channel can read a
	// post's props, and every one of them treated by the card as a hint that
	// anybody could have forged.
	clipPropID         = "villanet_clip_id"
	clipPropTitle      = "villanet_clip_title"
	clipPropDurationMs = "villanet_clip_duration_ms"
	clipPropCreatedAt  = "villanet_clip_created_at"
	clipPropClipperID  = "villanet_clip_clipper_id"
	clipPropClipper    = "villanet_clip_clipper"
	clipPropStreamerID = "villanet_clip_streamer_id"
	clipPropStreamer   = "villanet_clip_streamer"

	// The game, movie or show the stream was tagged with (A§4.14): its title,
	// and "movie", "tv" or "game". Left out for a clip with no tag.
	clipPropTag     = "villanet_clip_tag"
	clipPropTagKind = "villanet_clip_tag_kind"

	// Written only by the server, once the clip has gone: clipMarkExpired
	// or clipMarkDeleted.
	clipPropGone = "villanet_clip_gone"

	// The one-time pass a share's post carries from CreatePost to the hook,
	// which blanks it before the post is saved: a pass is spent the moment
	// the hook sees it, so the "" left behind says nothing.
	clipPropNonce = "villanet_share_nonce"

	// Mattermost's own prop for a link preview its author has hidden, the
	// one thing about a shared post its author may still change.
	clipPropRemoveLinkPreview = "remove_link_preview"

	// Every prop of VillaNET's own starts with this, and the hooks guard all
	// of them, the ones a later version adds included.
	clipPropPrefix = "villanet_"

	clipMarkExpired = "expired"
	clipMarkDeleted = "deleted"

	// The most posts one clip may be shared in. Every one of them is tracked
	// so it can be told when the clip goes, and a bound is what makes that a
	// promise rather than a hope. A post deleted since no longer counts; see
	// goneShares.
	maxClipShares = 10

	// How often, at most, this node asks Mattermost which posts of a clip
	// that is in ten are still there, before refusing another share of it:
	// so that asking to share a full clip again and again costs ten reads a
	// minute, and no more.
	shareRecountEvery = time.Minute

	// How long the same person sharing the same clip into the same channel
	// is answered with the post they already made: a double click, a second
	// tab, a retry after an answer was lost on the way back.
	shareRepeatWindow = time.Minute

	// How long a pass is good for between CreatePost and the hook. The hook
	// runs inside that call, on this node, so a few milliseconds is the
	// usual wait; this is the bound on a slow one.
	sharePassTTL = 30 * time.Second

	// pendingMarks are removed by the marker that put them there; this only
	// keeps one a crashed marker left behind from staying for ever.
	pendingMarkTTL = 10 * time.Minute

	// How many times a mark is put on a post before the marker gives up on
	// it. Another node's update can land over a mark, because Mattermost's
	// post store does not check versions; see markShareGone.
	shareMarkAttempts = 3

	// The members a group message can have, and some margin.
	maxShareGroupMembers = 2 * model.ChannelGroupMaxUsers

	// What the link in a shared post opens: the webapp's route, which needs
	// no team in the address and so works for direct messages too.
	clipLinkPath = "/plug/" + manifestID + "/clip/"

	clipPostEditRefused = "Shared clip posts can't be edited."
	clipNoSiteURLText   = "Shared clips carry no link because Mattermost's Site URL is not set."
)

// clipScrubbedProps are what a deleted clip's posts lose: its title, who made
// it of whose stream, and what the stream was tagged with. What remains says
// only that a clip of some length was shared here, once, and is gone. An
// expired clip's posts keep all of it.
var clipScrubbedProps = []string{clipPropTitle, clipPropClipper, clipPropClipperID, clipPropStreamer, clipPropStreamerID, clipPropTag, clipPropTagKind}

// clipShares is this node's side of sharing: the passes for posts it is
// making, the marks waiting to land on posts of clips that have gone, and
// the shares made in the last minute. The hooks read the first two while the
// share route and the marker are in the middle of a call to Mattermost that
// is waiting on those very hooks, so both are sync.Maps that nobody holds a
// lock on across such a call.
type clipShares struct {
	// Pass value → sharePass.
	passes sync.Map

	// Post id → pendingMark.
	marks sync.Map

	// The shares of the last minute, and those still being made. Under lock,
	// which is never held across a call to Mattermost.
	lock   sync.Mutex
	recent map[shareKey]*recentShare

	// onWait is told, under no lock, when a share starts waiting for the
	// same share still being made. Nil but in a test, which uses it to know
	// the wait has begun. Under lock.
	onWait func()

	// Clip id → when this node last asked which of its posts are still
	// there. Under lock. See recountDue.
	recounted map[string]time.Time

	// Whether the missing Site URL has been logged since activation.
	noSiteURLLogged atomic.Bool
}

// pendingMark is a mark a post is about to be given. The hook applies it to
// any update of that post that lands while it is pending, whoever made the
// update, so an author's own edit landing first cannot take the mark off.
type pendingMark struct {
	clipID string
	value  string
	at     time.Time
}

// shareKey is one person sharing one clip into one channel.
type shareKey struct {
	userID    string
	clipID    string
	channelID string
}

// recentShare is a share of the last minute, or one still being made, which
// done is closed on once it has finished, either way.
type recentShare struct {
	done   chan struct{}
	postID string // "" until it has landed
	at     time.Time
}

type clipShareRequest struct {
	ChannelID string `json:"channelId"`
}

type clipShareResponse struct {
	PostID    string `json:"postId"`
	ChannelID string `json:"channelId"`
}

// POST /api/v1/clips/share?id= {"channelId"}: post a clip into a channel, as
// the caller (A§4.2), in this order:
//
//  0. Past the gate, the attempt is metered (tooManyClipAttempts).
//  1. The id, the body and the channel id are checked.
//  2. A bot is refused.
//  3. The clip must still be here, and must not already be in ten posts
//     that are still there.
//  4. The caller must be able to post in the channel, as Mattermost would
//     have checked had they posted it themselves.
//  5. The same share made in the last minute is answered with its post.
//  6. Only now is a token spent, the post made and its id remembered on the
//     clip; a post that cannot be remembered, or that Mattermost did not
//     take as a card, is taken down again.
//
// It works while Clips is switched off, as watching and voting do, and for
// somebody an admin has stopped making clips, who may vote too.
func (p *Plugin) handleClipShare(w http.ResponseWriter, r *http.Request, userID, _ string) {
	s := p.clips()
	now := s.now()

	if p.tooManyClipAttempts(w, userID, now) {
		return
	}

	clipID := r.URL.Query().Get("id")

	var request clipShareRequest
	if !validClipID(clipID) || !readClipJSON(r, &request) || !model.IsValidId(request.ChannelID) {
		p.writeClipError(w, http.StatusBadRequest, clipErrBadRequest)
		return
	}
	channelID := request.ChannelID

	if err := p.mayShareAs(userID, now); err != nil {
		p.writeClipRefusal(w, err)
		return
	}

	key := shareKey{userID: userID, clipID: clipID, channelID: channelID}
	card, gone, err := p.sharedClipCard(key, now)
	if err != nil {
		p.writeClipRefusal(w, err)
		return
	}

	mmConfig := p.API.GetConfig()
	if err := p.mayPostShareIn(userID, channelID, mmConfig); err != nil {
		p.writeClipRefusal(w, err)
		return
	}

	earlier, mine, err := s.shares.claim(r.Context(), key, now)
	if err != nil {
		p.writeClipError(w, http.StatusServiceUnavailable, clipErrStore)
		return
	}
	if mine == nil {
		writeJSON(w, http.StatusOK, clipShareResponse{PostID: earlier, ChannelID: channelID})
		return
	}

	postID := ""
	defer func() {
		s.shares.release(key, mine, postID, now)
	}()

	if !s.shareLimiter.allowAt(userID, now) {
		writeClipTooMany(w, s.shareLimiter, userID, now)
		return
	}

	created, err := p.postSharedClip(key, card, p.shareSiteURL(mmConfig))
	if err != nil {
		s.shareLimiter.refund(userID)
		p.writeClipRefusal(w, err)

		return
	}

	if err := p.trackShare(key, created, gone, now); err != nil {
		p.withdrawShare(created)
		s.shareLimiter.refund(userID)
		p.writeClipRefusal(w, err)

		return
	}

	postID = created
	p.API.LogInfo("A clip was shared", "user_id", userID, "clip_id", clipID, "channel_id", channelID, "post_id", created)

	writeJSON(w, http.StatusCreated, clipShareResponse{PostID: created, ChannelID: channelID})
}

// mayShareAs refuses a bot, which can never have linked a stream, and so in
// practice never gets this far; and somebody whose account cannot be read,
// since there is then no telling whether they are one.
func (p *Plugin) mayShareAs(userID string, now time.Time) error {
	s := p.clips()

	p.lookUpClipPeople([]string{userID}, now, false)
	facts := s.factsOf(userID)
	if !facts.userKnown {
		return errStore
	}
	if facts.bot || facts.gone {
		return refuseClip(http.StatusForbidden, clipErrForbidden)
	}

	return nil
}

// sharedClipCard is the clip as its card will show it, named as its people
// are named now, and which of the posts it is in have been deleted since; or
// why it cannot be shared: it has gone, or it is in as many posts as a clip
// may be, unless this is the same share again, which is answered with its
// post further on.
//
// The document is read afresh rather than from this node's copy, which can
// be a minute old: a clip deleted on another node in that minute would
// otherwise be posted, its title and names going out to every member of the
// channel, before the write that tracks the post found it gone and took the
// post down again. A halted document refuses here, before anything is
// posted, whether or not this node holds a copy: a share it cannot write
// down would only be posted to be taken down again.
//
// A clip in ten posts is asked about, at most once a minute on this node:
// Mattermost tells a plugin of no deleted post, so the posts deleted since
// are found by asking, and do not count.
func (p *Plugin) sharedClipCard(key shareKey, now time.Time) (clipCardView, []string, error) {
	s := p.clips()

	doc, err := p.readClipsFresh(now)
	if err != nil {
		return clipCardView{}, nil, err
	}

	nowMS := now.UnixMilli()
	kept := effectiveRetention(doc.Retention, p.getConfiguration().clipRetentionDays(), nowMS)
	entry := doc.liveClip(key.clipID, kept, nowMS)
	if entry == nil {
		return clipCardView{}, nil, refuseClip(http.StatusGone, clipErrClipGone)
	}

	var gone []string
	if len(entry.Shares) >= maxClipShares {
		if _, again := s.shares.repeatOf(key, now); !again {
			full := refuseClip(http.StatusConflict, clipErrShareCap)
			if !s.shares.recountDue(key.clipID, now) {
				return clipCardView{}, nil, full
			}

			gone = p.goneShares(entry.Shares)
			if len(entry.Shares)-len(gone) >= maxClipShares {
				return clipCardView{}, nil, full
			}
		}
	}

	return clipCardOf(entry, p.clipNamesNow(now)), gone, nil
}

// goneShares are the posts of these that Mattermost no longer has: deleted
// by their author or a moderator, or moved away with their thread. Only a
// 404 says so, since GetPost hands out no deleted post [v9
// S/app/plugin_api.go:683-689]; any other failure says nothing either way,
// and the post still counts.
func (p *Plugin) goneShares(postIDs []string) []string {
	var gone []string
	for _, postID := range postIDs {
		if _, appErr := p.API.GetPost(postID); appErr != nil && appErr.StatusCode == http.StatusNotFound {
			gone = append(gone, postID)
		}
	}

	return gone
}

// recountDue is whether a clip that is in ten posts is due a look at which of
// them are still there, and if so notes that it has had one. Once a minute at
// most, on this node, forgetting the clips whose minute is up.
func (c *clipShares) recountDue(clipID string, now time.Time) bool {
	c.lock.Lock()
	defer c.lock.Unlock()

	if at, seen := c.recounted[clipID]; seen && now.Sub(at) < shareRecountEvery {
		return false
	}

	if c.recounted == nil {
		c.recounted = map[string]time.Time{}
	}
	for id, at := range c.recounted {
		if now.Sub(at) >= shareRecountEvery {
			delete(c.recounted, id)
		}
	}
	c.recounted[clipID] = now

	return true
}

// mayPostShareIn is whether somebody may post in a channel, as far as
// Mattermost would have checked it had they posted there themselves. A post
// a plugin makes skips all of it: Mattermost asks only that the author
// exists, and posts into an archived channel, a channel they are not in, or
// one whose moderation forbids them to post, as readily as anywhere.
//
//   - The channel must exist, must not be archived, and must not be shared
//     with another workspace: a post there would carry the clip, its link
//     and its props to another server, none of whose people can ever be
//     linked here.
//   - They must be a member, and have create_post there, which honours the
//     channel's moderation (and which a system admin has everywhere).
//   - A direct message to somebody deactivated is refused, as the webapp
//     refuses it.
//   - Where messages between people are restricted to people in the same
//     team, the members must still share one, as newer Mattermost checks of
//     every post (A§4.2 step 10b).
//
// Any refusal is the same answer, so it says nothing about why.
func (p *Plugin) mayPostShareIn(userID, channelID string, mmConfig *model.Config) error {
	cannot := refuseClip(http.StatusForbidden, clipErrCannotPost)

	channel, appErr := p.API.GetChannel(channelID)
	if appErr != nil || channel == nil || channel.DeleteAt != 0 || channel.IsShared() {
		return cannot
	}

	if member, appErr := p.API.GetChannelMember(channelID, userID); appErr != nil || member == nil {
		return cannot
	}

	if !p.API.HasPermissionToChannel(userID, channelID, model.PermissionCreatePost) {
		return cannot
	}

	if channel.Type == model.ChannelTypeDirect {
		if other := channel.GetOtherUserIdForDM(userID); other != "" {
			user, appErr := p.API.GetUser(other)
			if appErr != nil || user == nil || user.DeleteAt != 0 {
				return cannot
			}
		}
	}

	if channel.IsGroupOrDirect() && directMessagesRestricted(mmConfig) {
		return p.mayShareAcrossTeams(channel, userID)
	}

	return nil
}

// directMessagesRestricted is whether Mattermost only lets people message
// each other when they share a team.
func directMessagesRestricted(mmConfig *model.Config) bool {
	restrict := mmConfig != nil && mmConfig.TeamSettings.RestrictDirectMessage != nil
	return restrict && *mmConfig.TeamSettings.RestrictDirectMessage == model.DirectMessageTeam
}

// mayShareAcrossTeams is the team rule for a direct or group message, counted
// the way Mattermost counts it [master S/app/channel.go:4555-4570,
// :4293-4326]: its members who are still active, bar the bots it lets message
// anybody, must have at least one team in common. A direct message's members
// are in its name, which is how Mattermost names them; a group message's are
// asked for. Not being able to find out is the store's trouble, not a no.
func (p *Plugin) mayShareAcrossTeams(channel *model.Channel, userID string) error {
	var members []string

	if channel.Type == model.ChannelTypeDirect {
		first, second := channel.GetBothUsersForDM()
		for _, member := range []string{first, second} {
			if member != "" {
				members = append(members, member)
			}
		}
	} else {
		list, appErr := p.API.GetChannelMembers(channel.Id, 0, maxShareGroupMembers)
		if appErr != nil {
			p.API.LogWarn("Could not read who is in a group message", "user_id", userID, "channel_id", channel.Id, "error", appErr.Error())
			return errStore
		}
		for _, member := range list {
			members = append(members, member.UserId)
		}
	}

	// The plugins installed here, read the first time a bot needs them.
	var plugins map[string]bool
	pluginOwned := func(ownerID string) (bool, error) {
		if plugins == nil {
			manifests, appErr := p.API.GetPlugins()
			if appErr != nil {
				p.API.LogWarn("Could not read which plugins are installed", "error", appErr.Error())
				return false, errStore
			}

			plugins = map[string]bool{}
			for _, manifest := range manifests {
				if manifest != nil {
					plugins[manifest.Id] = true
				}
			}
		}

		return plugins[ownerID], nil
	}

	counted := make([]string, 0, len(members))
	for _, member := range members {
		counts, err := p.countsForTeamRule(member, userID, pluginOwned)
		if err != nil {
			return err
		}
		if counts {
			counted = append(counted, member)
		}
	}

	if len(counted) == 0 {
		return refuseClip(http.StatusForbidden, clipErrCannotPost)
	}

	var common map[string]bool
	for _, member := range counted {
		teams, appErr := p.API.GetTeamsForUser(member)
		if appErr != nil {
			p.API.LogWarn("Could not read somebody's teams", "user_id", member, "error", appErr.Error())
			return errStore
		}

		theirs := map[string]bool{}
		for _, team := range teams {
			if team != nil && (common == nil || common[team.Id]) {
				theirs[team.Id] = true
			}
		}
		common = theirs

		if len(common) == 0 {
			return refuseClip(http.StatusForbidden, clipErrCannotPost)
		}
	}

	return nil
}

// countsForTeamRule is whether Mattermost's team rule counts a member of a
// direct or group message. Somebody deactivated is not counted, nor is an
// account that no longer exists, nor a bot Mattermost lets message anybody:
// one of its own, one owned by the person posting, or one owned by a plugin
// installed here [master S/app/bot.go:391-430].
func (p *Plugin) countsForTeamRule(memberID, sharerID string, pluginOwned func(ownerID string) (bool, error)) (bool, error) {
	user, appErr := p.API.GetUser(memberID)
	if appErr != nil {
		if appErr.StatusCode == http.StatusNotFound {
			return false, nil
		}

		p.API.LogWarn("Could not read somebody in a direct or group message", "user_id", memberID, "error", appErr.Error())
		return false, errStore
	}
	if user == nil || user.DeleteAt != 0 {
		return false, nil
	}
	if !user.IsBot {
		return true, nil
	}

	bot, appErr := p.API.GetBot(memberID, false)
	if appErr != nil || bot == nil {
		detail := "no bot came back"
		if appErr != nil {
			detail = appErr.Error()
		}
		p.API.LogWarn("Could not read a bot in a direct or group message", "user_id", memberID, "error", detail)

		return false, errStore
	}
	if bot.IsSystemOwned() || bot.OwnerId == sharerID {
		return false, nil
	}

	owned, err := pluginOwned(bot.OwnerId)
	if err != nil {
		return false, err
	}

	return !owned, nil
}

// repeatOf is the post of the same share, landed in the last minute.
func (c *clipShares) repeatOf(key shareKey, now time.Time) (string, bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	share := c.recent[key]
	if share == nil || share.postID == "" || now.Sub(share.at) >= shareRepeatWindow {
		return "", false
	}

	return share.postID, true
}

// claim is the last check before anything is posted: the same share made in
// the last minute is answered with its post, and otherwise this request takes
// the share for itself until release. A second request for the same share
// arriving while the first is still being made waits for it — the wait is
// on a channel, never a lock — and is then answered with the first one's
// post, or makes the share itself if the first one failed. So a double click
// posts once, whichever of the two gets here first.
//
// It answers the earlier post, or the claim to release, or the caller giving
// up while it waited.
func (c *clipShares) claim(ctx context.Context, key shareKey, now time.Time) (string, *recentShare, error) {
	for {
		c.lock.Lock()
		if c.recent == nil {
			c.recent = map[shareKey]*recentShare{}
		}

		share := c.recent[key]
		switch {
		case share != nil && share.postID != "" && now.Sub(share.at) < shareRepeatWindow:
			c.lock.Unlock()
			return share.postID, nil, nil

		case share != nil && share.postID == "":
			wait, onWait := share.done, c.onWait
			c.lock.Unlock()

			if onWait != nil {
				onWait()
			}

			select {
			case <-wait:
			case <-ctx.Done():
				return "", nil, ctx.Err()
			}

			continue
		}

		c.pruneRecent(now)
		mine := &recentShare{done: make(chan struct{})}
		c.recent[key] = mine
		c.lock.Unlock()

		return "", mine, nil
	}
}

// release ends a claim: with the post it made, kept for a minute, or with
// nothing, when it was refused or failed, so that asking again tries again.
// Either way, anybody waiting on it goes on.
func (c *clipShares) release(key shareKey, mine *recentShare, postID string, now time.Time) {
	c.lock.Lock()
	defer c.lock.Unlock()

	if postID != "" {
		mine.postID, mine.at = postID, now
	} else if c.recent[key] == mine {
		delete(c.recent, key)
	}

	close(mine.done)
}

// pruneRecent forgets shares more than a minute old. Under lock. Shares are
// metered, ten in ten minutes a person, so this stays small without a bound
// of its own.
func (c *clipShares) pruneRecent(now time.Time) {
	for key, share := range c.recent {
		if share.postID != "" && now.Sub(share.at) >= shareRepeatWindow {
			delete(c.recent, key)
		}
	}
}

// shareSiteURL is Mattermost's own address, which the link in a shared post
// starts with: never meResponse.SiteURL, which is Broadcast Box's. Without
// one a shared post carries no link at all, and an admin is told so once.
func (p *Plugin) shareSiteURL(mmConfig *model.Config) string {
	site := ""
	if mmConfig != nil && mmConfig.ServiceSettings.SiteURL != nil {
		site = strings.TrimSpace(*mmConfig.ServiceSettings.SiteURL)
	}

	if site == "" && p.clips().shares.noSiteURLLogged.CompareAndSwap(false, true) {
		p.API.LogWarn(clipNoSiteURLText)
	}

	return site
}

// clipShareMessage is a shared post's message, the same for every clip but
// for its length and its link, and nothing anybody typed:
//
//	🎬 Matty clip · 0:45
//	Watch it: https://chat.example.com/plug/ceo.villa.matty/clip/3f9a0c2e7b1d4a55
//
// name is the server's name for the plugin (brandName). The second line is
// left out when Mattermost's Site URL is not set.
func clipShareMessage(name, siteURL string, card clipCardView) string {
	message := "🎬 " + name + " clip · " + clipLength(card.DurationMs)

	if base := strings.TrimRight(strings.TrimSpace(siteURL), "/"); base != "" {
		message += "\nWatch it: " + base + clipLinkPath + card.ID
	}

	return message
}

// clipLength is how long a clip is, as m:ss, rounded to the nearest second as
// the card writes it, so the message and the card agree.
func clipLength(durationMs int64) string {
	seconds := (max(0, durationMs) + 500) / 1000

	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

// clipShareProps are a shared post's props: what its card shows a reader who
// is not linked, which is no more than the card shows anybody. Never the
// picture, the handle, the stream key or tag, the votes, or the stream's
// note. The stream's game, movie or show is here, for the card to show beside
// the title, and never in the message, which carries nothing anybody typed.
func clipShareProps(card clipCardView, pass string) model.StringInterface {
	props := model.StringInterface{
		clipPropID:         card.ID,
		clipPropTitle:      card.Title,
		clipPropDurationMs: card.DurationMs,
		clipPropCreatedAt:  card.CreatedAt,
		clipPropClipperID:  card.Clipper.UserID,
		clipPropClipper:    card.Clipper.Name,
		clipPropStreamer:   card.Streamer.Name,
		clipPropNonce:      pass,
	}
	if card.Streamer.UserID != "" {
		props[clipPropStreamerID] = card.Streamer.UserID
	}
	if card.Tag != nil {
		props[clipPropTag] = card.Tag.Title
		props[clipPropTagKind] = card.Tag.kind()
	}

	return props
}

// postSharedClip makes the post, as the person sharing, with a pass the hook
// will blank. No lock is held across CreatePost: the hook runs while it
// waits. The pass is good for half a minute from now, when it is issued, not
// from when the request came in: the checks before it, and a wait behind the
// same share, are none of its business.
//
// Refused by Mattermost for something about the request — the channel went
// in the meantime, say — is "you can't post there"; anything else is the
// server's trouble. Both are logged. So is a post that came back as anything
// but a card, which the hook makes of a post whose pass it did not take: the
// post is taken down again, since a plain message counted as a share would
// hold one of the clip's ten places for nothing.
func (p *Plugin) postSharedClip(key shareKey, card clipCardView, siteURL string) (string, error) {
	s := p.clips()

	pass := newID()
	if pass == "" {
		return "", errStore
	}

	post := &model.Post{
		UserId:    key.userID,
		ChannelId: key.channelID,
		Type:      clipPostType,
		Message:   clipShareMessage(p.brandName(), siteURL, card),
	}
	post.SetProps(clipShareProps(card, pass))

	issued := s.now()
	s.shares.issue(pass, sharePass{
		expires:   issued.Add(sharePassTTL),
		userID:    key.userID,
		channelID: key.channelID,
		clipID:    key.clipID,
	}, issued)

	created, appErr := p.API.CreatePost(post)

	// Whatever happened, the pass is spent.
	s.shares.passes.Delete(pass)

	if appErr != nil || created == nil || created.Id == "" {
		detail := "no post came back"
		if appErr != nil {
			detail = appErr.Error()
		}
		p.API.LogWarn("Could not post a shared clip", "user_id", key.userID, "clip_id", key.clipID, "channel_id", key.channelID, "error", detail)

		if appErr != nil && appErr.StatusCode >= 400 && appErr.StatusCode < 500 {
			return "", refuseClip(http.StatusForbidden, clipErrCannotPost)
		}

		return "", errStore
	}

	if created.Type != clipPostType {
		p.API.LogWarn("Could not post a shared clip", "user_id", key.userID, "clip_id", key.clipID, "channel_id", key.channelID, "error", "the post was not taken as a clip card")
		p.withdrawShare(created.Id)

		return "", errStore
	}

	return created.Id, nil
}

// trackShare writes a share's post down on its clip (A§4.2 step 14), or says
// why the post must come down again:
//
//   - the clip went while the post was being made;
//   - another share filled the clip's ten places meanwhile, once any of its
//     posts deleted since have been taken off;
//   - or the store would not take the write, which is logged, and which the
//     sharer is told as the store's trouble whatever the store said, since
//     nothing about it is theirs to put right.
func (p *Plugin) trackShare(key shareKey, postID string, gone []string, now time.Time) error {
	held, err := p.rememberShare(key.clipID, postID, gone, now)
	if refusalCode(err) == clipErrShareCap {
		if more := p.goneShares(held); len(more) > 0 {
			_, err = p.rememberShare(key.clipID, postID, more, now)
		}
	}

	switch refusalCode(err) {
	case clipErrClipGone, clipErrShareCap:
		return err
	}
	if err != nil {
		p.API.LogWarn("Could not remember a shared clip post", "user_id", key.userID, "clip_id", key.clipID, "channel_id", key.channelID, "post_id", postID, "error", err.Error())
		return errStore
	}

	return nil
}

// refusalCode is the code of a refusal, or "" for anything else.
func refusalCode(err error) clipErrorCode {
	var refusal *clipError
	if errors.As(err, &refusal) {
		return refusal.code
	}

	return ""
}

// rememberShare writes a share's post down on its clip, so that the post can
// be told when the clip goes, taking off the clip's posts that are known to
// be gone. A clip that went while the post was being made, or that other
// shares have filled up meanwhile, is refused, and the caller takes the post
// down: a share nobody can tell about the clip going must not stay up. A
// clip that is full answers with the posts it is in, so the caller can ask
// which of them are still there.
//
// Nothing a panel shows changes, so no panel is told.
func (p *Plugin) rememberShare(clipID, postID string, gone []string, now time.Time) ([]string, error) {
	var held []string

	_, err := p.clipsCAS(now, func(doc *clipsDoc, write *clipsWrite) (bool, error) {
		write.unseen = true
		held = nil

		entry := doc.liveClip(clipID, write.kept, write.now)
		if entry == nil {
			return false, refuseClip(http.StatusGone, clipErrClipGone)
		}
		if slices.Contains(entry.Shares, postID) {
			return false, nil
		}

		live := make([]string, 0, len(entry.Shares)+1)
		for _, shared := range entry.Shares {
			if !slices.Contains(gone, shared) {
				live = append(live, shared)
			}
		}
		if len(live) >= maxClipShares {
			held = live
			return false, refuseClip(http.StatusConflict, clipErrShareCap)
		}

		entry.Shares = append(live, postID)

		return true, nil
	})

	return held, err
}

// withdrawShare takes down a post a share made and could not write down.
func (p *Plugin) withdrawShare(postID string) {
	if appErr := p.API.DeletePost(postID); appErr != nil {
		p.API.LogWarn("Could not withdraw a shared clip post", "post_id", postID, "error", appErr.Error())
	}
}

// propText is a prop as a string, or "" when it is missing or is not one.
func propText(props model.StringInterface, key string) string {
	text, _ := props[key].(string)

	return text
}

// copyProps is a post's props to change. Never the map the post holds:
// Mattermost's own copies of a post share it, and a hook's changes must reach
// only the post it hands back.
func copyProps(props model.StringInterface) model.StringInterface {
	copied := make(model.StringInterface, len(props)+1)
	for key, value := range props {
		copied[key] = value
	}

	return copied
}

// applyGoneMark says on a post's props that its clip has gone, and for a
// deleted clip takes out its title and its people. The clip id, length and
// date stay, which say nothing a reader did not already know.
func applyGoneMark(props model.StringInterface, value string) {
	props[clipPropGone] = value

	if value == clipMarkDeleted {
		for _, key := range clipScrubbedProps {
			delete(props, key)
		}
	}
}

// markedGone is whether a post's props already say so.
func markedGone(props model.StringInterface, value string) bool {
	if propText(props, clipPropGone) != value {
		return false
	}

	if value == clipMarkDeleted {
		for _, key := range clipScrubbedProps {
			if _, present := props[key]; present {
				return false
			}
		}
	}

	return true
}

// markSharesGoneSoon starts telling the posts of clips a settle has just
// removed, without keeping whoever settled them waiting: a delete answers at
// once, and the sweep goes on to the files. While Clips is running the work
// is waited for by stopClips; outside that, which only a request racing a
// deactivation sees, it is done there and then.
func (p *Plugin) markSharesGoneSoon(settled []settledClip) {
	var items []settledClip
	for _, gone := range settled {
		if len(gone.Shares) > 0 {
			items = append(items, gone)
		}
	}
	if len(items) == 0 {
		return
	}

	s := p.clips()

	s.runLock.Lock()
	if !s.running {
		s.runLock.Unlock()
		p.markSharesGone(items)

		return
	}
	s.background.Add(1)
	s.runLock.Unlock()

	go func() {
		defer s.background.Done()
		p.safely("marking shared clips gone", func() { p.markSharesGone(items) })
	}()
}

// markSharesGone tells every post each clip was shared in that it has gone:
// "expired" for a clip that ran its course, which keeps its title and its
// people, so the card can still say what it was; "deleted" for one its
// clipper, its streamer or an admin took down, which takes them out, so a
// deleted clip's title is in none of its posts any more.
//
// The message is never touched, so no post is marked "(edited)", and open
// clients redraw the card from the edit Mattermost pushes.
func (p *Plugin) markSharesGone(items []settledClip) {
	for _, gone := range items {
		if len(gone.Shares) == 0 {
			continue
		}

		value := clipMarkDeleted
		if gone.Reason == clipGoneExpired {
			value = clipMarkExpired
		}

		marked := 0
		for _, postID := range gone.Shares {
			if p.markShareGone(postID, gone.ID, value) {
				marked++
			}
		}

		p.API.LogInfo("Shared clip posts marked", "clip_id", gone.ID, "posts", marked, "reason", gone.Reason)
	}
}

// markShareGone marks one post, and says whether it carries the mark now.
//
//  1. A post that cannot be read is skipped: Mattermost does not hand out
//     deleted ones.
//  2. So is anything that is not this clip's card, and one already marked.
//  3. The mark is made pending, so that an update of the post landing on
//     this node meanwhile — its author's, say — carries it too.
//  4. It is put on the post, and the post read back. An update from another
//     node, whose hook knows nothing of the mark, can land over it, since
//     Mattermost's post store does not check versions, so if it is not there
//     it is put on again, up to three times in all.
//
// A channel archived since refuses every update, and its post keeps what it
// had; the card's own expiry rule still covers an expired clip there. So
// does, from Mattermost 11, a direct or group message whose members no
// longer share a team while messages are kept within teams [v11.1.0
// S/app/post.go:710]. A post left unmarked after every attempt is logged at
// Info, so an admin can see which posts still carry what they carried.
func (p *Plugin) markShareGone(postID, clipID, value string) bool {
	s := p.clips()

	post, appErr := p.API.GetPost(postID)
	if appErr != nil || post == nil {
		p.logShareMark("Could not read a shared clip post to mark it", postID, clipID, appErr)
		return false
	}
	if post.Type != clipPostType || propText(post.GetProps(), clipPropID) != clipID {
		return false
	}
	if markedGone(post.GetProps(), value) {
		return true
	}

	mark := pendingMark{clipID: clipID, value: value, at: s.now()}
	s.shares.holdMark(postID, mark)
	defer s.shares.marks.CompareAndDelete(postID, mark)

	failure := ""
	for attempt := 0; attempt < shareMarkAttempts; attempt++ {
		props := copyProps(post.GetProps())
		applyGoneMark(props, value)
		post.SetProps(props)

		failure = "another update landed over the mark"
		if _, appErr := p.API.UpdatePost(post); appErr != nil {
			p.logShareMark("Could not mark a shared clip post", postID, clipID, appErr)
			failure = appErr.Error()
		}

		fresh, appErr := p.API.GetPost(postID)
		if appErr != nil || fresh == nil {
			p.logShareMark("Could not read a shared clip post back", postID, clipID, appErr)
			return false
		}
		if fresh.Type != clipPostType || propText(fresh.GetProps(), clipPropID) != clipID {
			return false
		}
		if markedGone(fresh.GetProps(), value) {
			return true
		}

		post = fresh
	}

	p.API.LogInfo("A shared clip post could not be marked", "post_id", postID, "clip_id", clipID, "mark", value, "error", failure)

	return false
}

// holdMark makes a mark pending, forgetting any a marker left behind long ago.
func (c *clipShares) holdMark(postID string, mark pendingMark) {
	c.marks.Range(func(key, stored any) bool {
		if old, ok := stored.(pendingMark); !ok || mark.at.Sub(old.at) >= pendingMarkTTL {
			c.marks.Delete(key)
		}

		return true
	})

	c.marks.Store(postID, mark)
}

// logShareMark logs what went wrong marking a post, at Debug: one attempt
// says little, and the Info lines after say how many were marked and which
// were not.
func (p *Plugin) logShareMark(message, postID, clipID string, appErr *model.AppError) {
	detail := "nothing came back"
	if appErr != nil {
		detail = appErr.Error()
	}

	p.API.LogDebug(message, "post_id", postID, "clip_id", clipID, "error", detail)
}
