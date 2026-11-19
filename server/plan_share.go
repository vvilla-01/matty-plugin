package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// Sharing a plan into a channel (design/3.2.0.md §6). Built on the clip
// share's pattern: a post as the sharer, of VillaNET's own type, which every
// reader's webapp draws as a card, made only with a one-time pass the hooks in
// share_cards.go redeem.
//
// What is different is how little the post carries. A clip's card is drawn
// from its props, which the marker scrubs when the clip is deleted. A plan's
// card is drawn from the snapshot every reader already has: the post holds
// only the plan's id and its start time. So a plan that is edited or
// cancelled leaves nothing of its title, its host or its picked title in any
// post, though the post belongs to the sharer, who may not be the host; and
// nothing here keeps track of which posts a plan was shared in, because there
// is nothing to take back from them.
//
// The message is VillaNET's fixed text and a link, never a title or a name,
// as a clip's is: it is what phones, the Threads list, pushes, email and
// search see, and a mention in it would notify people.

const (
	planPostType     = "custom_villanet_plan"
	planPostCopyType = "custom_villanet_plan_copy"

	// Everything a shared plan's post carries for its card, besides the
	// pass the hook blanks.
	planPropID       = "villanet_plan_id"
	planPropStartsAt = "villanet_plan_starts_at"

	planLinkPath = "/plug/" + manifestID + "/plan/"

	planPostEditRefused = "Shared plan posts can't be edited."

	maxPlanShareBody = 1 << 10

	planShareGone    = "That plan isn't there any more."
	planShareTooMany = "You've shared a lot just now. Try again in a few minutes."
	planShareNeeds   = "Link your stream first — sharing plans is for streamers."
)

// planShareStore is the refusal when a share could not be stored, with the
// server's name for the plugin (brandName).
func planShareStore(name string) string {
	return name + " couldn't share that just now. Try again in a moment."
}

type planShareRequest struct {
	ID        string `json:"id"`
	ChannelID string `json:"channelId"`
}

type planShareResponse struct {
	PostID    string `json:"postId"`
	ChannelID string `json:"channelId"`
}

// validPlanID is a plan id as newID makes them: 16 lower-case hex digits.
func validPlanID(id string) bool {
	if len(id) != 16 {
		return false
	}

	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}

	return true
}

// validChannelID is a channel id as Mattermost makes them: 26 letters and
// digits.
func validChannelID(id string) bool {
	if len(id) != 26 {
		return false
	}

	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}

	return true
}

// writePlanShareRefusal answers a refusal from the checks a share borrows
// from clips (mayShareAs, mayPostShareIn), in the plan share's own words.
func (p *Plugin) writePlanShareRefusal(w http.ResponseWriter, err error) {
	status, code := clipErrorFor(err)

	switch code {
	case clipErrCannotPost, clipErrForbidden:
		writeCodedError(w, status, clipErrorTexts[code], string(code))
	default:
		writeCodedError(w, http.StatusServiceUnavailable, planShareStore(p.brandName()), accessCodeStore)
	}
}

// handlePlanShare is POST /api/v1/schedule/share {"id","channelId"}, in this
// order (§6.3):
//
//  1. The caller must be linked (layer 2 behind the gate).
//  2. The body is checked.
//  3. A bot or an account that has gone is refused.
//  4. The plan must be on the schedule as read just now, never the snapshot,
//     so a plan cancelled a moment ago is not posted.
//  5. The caller must be able to post in the channel, as Mattermost would
//     have checked had they posted it themselves.
//  6. The same share made in the last minute is answered with its post.
//  7. Only now is a token spent, and given back if the post is not made.
//  8. The post is made, with a pass the hook blanks.
func (p *Plugin) handlePlanShare(w http.ResponseWriter, r *http.Request, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.requireLinkedStream(w, userID, planShareNeeds) {
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxPlanShareBody+1))
	if err != nil || len(body) > maxPlanShareBody {
		writeCodedError(w, http.StatusBadRequest, "Could not read the request.", "bad_request")
		return
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()

	var request planShareRequest
	if decoder.Decode(&request) != nil || !validPlanID(request.ID) || !validChannelID(request.ChannelID) {
		writeCodedError(w, http.StatusBadRequest, "Could not read the request.", "bad_request")
		return
	}

	s := p.clips()
	now := s.now()

	if err := p.mayShareAs(userID, now); err != nil {
		p.writePlanShareRefusal(w, err)
		return
	}

	doc, err := p.readSchedule()
	if err != nil {
		writeCodedError(w, http.StatusServiceUnavailable, planShareStore(p.brandName()), accessCodeStore)
		return
	}

	var plan *scheduleEntry
	for _, entry := range prune(doc.Entries, nowMillis()) {
		if entry.ID == request.ID {
			found := entry
			plan = &found

			break
		}
	}
	if plan == nil {
		writeCodedError(w, http.StatusGone, planShareGone, "gone")
		return
	}

	mmConfig := p.API.GetConfig()
	if err := p.mayPostShareIn(userID, request.ChannelID, mmConfig); err != nil {
		p.writePlanShareRefusal(w, err)
		return
	}

	// The clip share's repeat window, under a key no clip id can have.
	key := shareKey{userID: userID, clipID: "plan:" + plan.ID, channelID: request.ChannelID}
	earlier, mine, err := s.shares.claim(r.Context(), key, now)
	if err != nil {
		writeCodedError(w, http.StatusServiceUnavailable, planShareStore(p.brandName()), accessCodeStore)
		return
	}
	if mine == nil {
		writeJSON(w, http.StatusOK, planShareResponse{PostID: earlier, ChannelID: request.ChannelID})
		return
	}

	postID := ""
	defer func() {
		s.shares.release(key, mine, postID, now)
	}()

	limiter := p.limiters().planShare
	if !limiter.allowAt(userID, now) {
		writeTooMany(w, planShareTooMany, limiter, userID, now)
		return
	}

	created, err := p.postSharedPlan(userID, request.ChannelID, plan, p.shareSiteURL(mmConfig))
	if err != nil {
		limiter.refund(userID)
		p.writePlanShareRefusal(w, err)

		return
	}

	postID = created

	// At Debug, and never the title: a plan's words are its host's.
	p.API.LogDebug("A plan was shared", "user_id", userID, "plan_id", plan.ID, "channel_id", request.ChannelID, "post_id", created)

	writeJSON(w, http.StatusCreated, planShareResponse{PostID: created, ChannelID: request.ChannelID})
}

// planShareMessage is a shared plan's message, the same for every plan but
// for its link, and nothing anybody typed:
//
//	📅 Matty plan
//	See it: https://chat.example.com/plug/ceo.villa.matty/plan/3f9a0c2e7b1d4a55
//
// name is the server's name for the plugin (brandName). The second line is
// left out when Mattermost's Site URL is not set.
func planShareMessage(name, siteURL, planID string) string {
	message := "📅 " + name + " plan"

	if base := strings.TrimRight(strings.TrimSpace(siteURL), "/"); base != "" {
		message += "\nSee it: " + base + planLinkPath + planID
	}

	return message
}

// planShareProps are a shared plan's props: its id and its start time, which
// say nothing a reader could not see on the schedule, and the pass. Never the
// title, the host, the category, the picked title, who joined, an art id, a
// stream key or anything about a reader: the card draws all of that from the
// snapshot while the plan is listed, and nothing at all once it is not.
func planShareProps(plan *scheduleEntry, pass string) model.StringInterface {
	return model.StringInterface{
		planPropID:       plan.ID,
		planPropStartsAt: plan.StartsAt,
		clipPropNonce:    pass,
	}
}

// postSharedPlan makes the post, as the person sharing, with a pass the hook
// will blank. No lock is held across CreatePost: the hook runs while it waits.
// Refused by Mattermost for something about the request is "you can't post
// there"; anything else is the server's trouble. A post that came back as
// anything but a card is taken down again.
func (p *Plugin) postSharedPlan(userID, channelID string, plan *scheduleEntry, siteURL string) (string, error) {
	s := p.clips()

	pass := newID()
	if pass == "" {
		return "", errStore
	}

	post := &model.Post{
		UserId:    userID,
		ChannelId: channelID,
		Type:      planPostType,
		Message:   planShareMessage(p.brandName(), siteURL, plan.ID),
	}
	post.SetProps(planShareProps(plan, pass))

	issued := s.now()
	s.shares.issue(pass, sharePass{
		expires:   issued.Add(sharePassTTL),
		userID:    userID,
		channelID: channelID,
		kind:      planPostType,
		planID:    plan.ID,
	}, issued)

	created, appErr := p.API.CreatePost(post)

	// Whatever happened, the pass is spent.
	s.shares.passes.Delete(pass)

	if appErr != nil || created == nil || created.Id == "" {
		detail := "no post came back"
		if appErr != nil {
			detail = appErr.Error()
		}
		p.API.LogWarn("Could not post a shared plan", "user_id", userID, "plan_id", plan.ID, "channel_id", channelID, "error", detail)

		if appErr != nil && appErr.StatusCode >= 400 && appErr.StatusCode < 500 {
			return "", refuseClip(http.StatusForbidden, clipErrCannotPost)
		}

		return "", errStore
	}

	if created.Type != planPostType {
		p.API.LogWarn("Could not post a shared plan", "user_id", userID, "plan_id", plan.ID, "channel_id", channelID, "error", "the post was not taken as a plan card")
		p.withdrawShare(created.Id)

		return "", errStore
	}

	return created.Id, nil
}
