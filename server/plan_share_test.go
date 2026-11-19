package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// Sharing a plan into a channel (design/3.2.0.md §6): a card posted as the
// sharer, whose message is VillaNET's fixed text and link and whose props are
// only the plan's id and start time, guarded by the clip card's hooks.

// planShareWorld is the clip share's world with one plan on the schedule,
// Bob's, three hours from now, and its id.
func planShareWorld(t *testing.T) (*clipsWorld, *shareServer, string) {
	t.Helper()

	w, srv := newShareWorld(t)
	require.NoError(t, w.p.addScheduleEntry("uid-bob", "Arma night with @dave", "", nowMillis()+3*time.Hour.Milliseconds(), nowMillis()))

	doc, err := w.p.readSchedule()
	require.NoError(t, err)
	require.Len(t, doc.Entries, 1)

	return w, srv, doc.Entries[0].ID
}

func (w *clipsWorld) sharePlan(t *testing.T, userID, planID, channelID string) *httptest.ResponseRecorder {
	t.Helper()

	return w.call(t, userID, clipCall{
		method: http.MethodPost,
		target: "/api/v1/schedule/share",
		body:   `{"id":"` + planID + `","channelId":"` + channelID + `"}`,
	})
}

func decodePlanShare(t *testing.T, response *httptest.ResponseRecorder, status int) planShareResponse {
	t.Helper()

	require.Equal(t, status, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

	var answer planShareResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer))
	require.NotEmpty(t, answer.PostID)

	return answer
}

func TestPlanShareIsLinkedOnly(t *testing.T) {
	require.Equal(t, routeLinked, routeClasses["/api/v1/schedule/share"][http.MethodPost])

	w, srv, planID := planShareWorld(t)
	w.person("uid-unlinked")

	requireRefused(t, w.sharePlan(t, "uid-unlinked", planID, townSquare), http.StatusForbidden, accessCodeLocked, "the gate")

	// Layer 2, for a caller the gate's memory let through a moment before an
	// unlink.
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/schedule/share", strings.NewReader(`{"id":"`+planID+`","channelId":"`+townSquare+`"}`))
	w.p.handlePlanShare(recorder, request, "uid-unlinked")
	requireRefused(t, recorder, http.StatusForbidden, accessCodeLocked, "the handler")

	require.Zero(t, srv.createCount())
}

func TestPlanSharePostCarriesNoTitleOrNames(t *testing.T) {
	w, srv, planID := planShareWorld(t)

	shared := decodePlanShare(t, w.sharePlan(t, "uid-dan", planID, townSquare), http.StatusCreated)
	require.Equal(t, townSquare, shared.ChannelID)

	post := srv.post(shared.PostID)
	require.NotNil(t, post)
	require.Equal(t, planPostType, post.Type)
	require.Equal(t, "uid-dan", post.UserId, "as the sharer")
	require.Empty(t, post.RootId)
	require.Equal(t, "📅 Matty plan\nSee it: https://chat.example.com/plug/ceo.villa.matty/plan/"+planID, post.Message,
		"the fixed text and the link, never the title, the time or a name")

	doc, err := w.p.readSchedule()
	require.NoError(t, err)

	villanet := map[string]any{}
	for key, value := range post.GetProps() {
		if strings.HasPrefix(key, "villanet_") {
			villanet[key] = value
		}
	}
	require.Equal(t, map[string]any{
		planPropID:       planID,
		planPropStartsAt: doc.Entries[0].StartsAt,
		clipPropNonce:    "",
	}, villanet, "the id, the start time and the blanked pass, and nothing else")

	encoded, err := json.Marshal(post)
	require.NoError(t, err)
	for _, never := range []string{"Arma", "@dave", "uid-bob", "Bob", "videokey-bob"} {
		require.NotContains(t, string(encoded), never)
	}
}

func TestPlanShareRefusesAPlanJustCancelled(t *testing.T) {
	w, srv, planID := planShareWorld(t)

	// The snapshot still lists it; the schedule, read afresh, does not.
	w.p.addCommunity(w.p.getSnapshot())
	require.NoError(t, w.p.removeScheduleEntry("uid-bob", planID, false, nowMillis()))

	recorder := w.sharePlan(t, "uid-dan", planID, townSquare)
	requireRefused(t, recorder, http.StatusGone, "gone", "cancelled")
	require.Equal(t, planShareGone, codedError(t, recorder).Error)
	require.Zero(t, srv.createCount())

	requireRefused(t, w.sharePlan(t, "uid-dan", "0123456789abcdef", townSquare), http.StatusGone, "gone", "never was")
	requireRefused(t, w.sharePlan(t, "uid-dan", "NOT-A-PLAN", townSquare), http.StatusBadRequest, "bad_request", "not an id")
	requireRefused(t, w.sharePlan(t, "uid-dan", planID, "town-square"), http.StatusBadRequest, "bad_request", "not a channel id")
}

func TestPlanShareChecksTheChannel(t *testing.T) {
	w, srv, planID := planShareWorld(t)

	srv.addChannel(&model.Channel{Id: channelOf("archived"), Type: model.ChannelTypeOpen, DeleteAt: 1}, "uid-dan")
	srv.addChannel(&model.Channel{Id: channelOf("private"), Type: model.ChannelTypePrivate}, "uid-bob")
	srv.addChannel(&model.Channel{Id: channelOf("readonly"), Type: model.ChannelTypeOpen}, "uid-dan")
	srv.set(func(srv *shareServer) { srv.posters[channelOf("readonly")]["uid-dan"] = false })

	for about, channelID := range map[string]string{
		"no such channel": channelOf("nowhere"),
		"archived":        channelOf("archived"),
		"not a member":    channelOf("private"),
		"read-only":       channelOf("readonly"),
	} {
		recorder := w.sharePlan(t, "uid-dan", planID, channelID)
		requireRefused(t, recorder, http.StatusForbidden, "cannot_post", about)
		require.Equal(t, clipErrorTexts[clipErrCannotPost], codedError(t, recorder).Error, "the clip share's words")
	}
	require.Zero(t, srv.createCount())
}

func TestPlanShareRepeatWindow(t *testing.T) {
	w, srv, planID := planShareWorld(t)

	first := decodePlanShare(t, w.sharePlan(t, "uid-dan", planID, townSquare), http.StatusCreated)
	again := decodePlanShare(t, w.sharePlan(t, "uid-dan", planID, townSquare), http.StatusOK)
	require.Equal(t, first.PostID, again.PostID, "a double click posts once")
	require.Equal(t, 1, srv.createCount())

	// Another channel, or a minute later, is another share.
	decodePlanShare(t, w.sharePlan(t, "uid-dan", planID, offTopic), http.StatusCreated)
	setClipsClock(w.p, clipsNow.Add(61*time.Second))
	later := decodePlanShare(t, w.sharePlan(t, "uid-dan", planID, townSquare), http.StatusCreated)
	require.NotEqual(t, first.PostID, later.PostID)
	require.Equal(t, 3, srv.createCount())
}

func TestPlanShareMeter(t *testing.T) {
	w, srv, planID := planShareWorld(t)

	channels := []string{}
	for i := range 11 {
		channelID := channelOf(fmt.Sprintf("room%d", i))
		srv.addChannel(&model.Channel{Id: channelID, Type: model.ChannelTypeOpen}, "uid-dan")
		channels = append(channels, channelID)
	}

	// A post Mattermost refuses gives the token back.
	srv.set(func(srv *shareServer) {
		srv.createFail = model.NewAppError("CreatePost", "app.failed", nil, "", http.StatusInternalServerError)
	})
	requireRefused(t, w.sharePlan(t, "uid-dan", planID, channels[0]), http.StatusServiceUnavailable, accessCodeStore, "refused by Mattermost")
	srv.set(func(srv *shareServer) { srv.createFail = nil })

	for _, channelID := range channels[:10] {
		decodePlanShare(t, w.sharePlan(t, "uid-dan", planID, channelID), http.StatusCreated)
	}

	recorder := w.sharePlan(t, "uid-dan", planID, channels[10])
	requireRefused(t, recorder, http.StatusTooManyRequests, "rate_limited", "the eleventh in ten minutes")
	require.Equal(t, planShareTooMany, codedError(t, recorder).Error)
}

func TestForgedPlanCardBecomesACopy(t *testing.T) {
	w, _, planID := planShareWorld(t)
	s := w.p.clips()

	card := func(pass string) *model.Post {
		post := &model.Post{UserId: "uid-dan", ChannelId: townSquare, Type: planPostType, Message: "📅 Matty plan"}
		post.SetProps(model.StringInterface{planPropID: planID, planPropStartsAt: int64(5), "villanet_plan_title": "made up", "keep": "this"})
		if pass != "" {
			post.AddProp(clipPropNonce, pass)
		}

		return post
	}
	requireCopy := func(post *model.Post, reason, about string) {
		t.Helper()

		require.Empty(t, reason, about)
		require.Equal(t, planPostCopyType, post.Type, about)
		for key, value := range post.GetProps() {
			if strings.HasPrefix(key, "villanet_") || key == "type" {
				require.Equal(t, "", value, "%s: %s blanked", about, key)
			}
		}
		require.Equal(t, "this", post.GetProp("keep"), about)
	}

	post, reason := posted(t, w.p, card(""))
	requireCopy(post, reason, "no pass")

	good := sharePass{expires: clipsNow.Add(30 * time.Second), userID: "uid-dan", channelID: townSquare, kind: planPostType, planID: planID}
	for about, pass := range map[string]sharePass{
		"a clip's pass":   {expires: good.expires, userID: "uid-dan", channelID: townSquare, clipID: planID},
		"another plan":    {expires: good.expires, userID: "uid-dan", channelID: townSquare, kind: planPostType, planID: "fedcba9876543210"},
		"another person":  {expires: good.expires, userID: "uid-dave", channelID: townSquare, kind: planPostType, planID: planID},
		"another channel": {expires: good.expires, userID: "uid-dan", channelID: offTopic, kind: planPostType, planID: planID},
	} {
		s.shares.issue("cccccccccccccccc", pass, clipsNow)
		post, reason = posted(t, w.p, card("cccccccccccccccc"))
		requireCopy(post, reason, about)
	}

	s.shares.issue("aaaaaaaaaaaaaaaa", good, clipsNow)
	post, reason = posted(t, w.p, card("aaaaaaaaaaaaaaaa"))
	require.Empty(t, reason)
	require.Equal(t, planPostType, post.Type, "the one pass issued for exactly it")
	require.Equal(t, "", post.GetProp(clipPropNonce))

	post, reason = posted(t, w.p, card("aaaaaaaaaaaaaaaa"))
	requireCopy(post, reason, "a pass used twice")
}

func TestPlanCardClaimThroughPropsTypeIsBlanked(t *testing.T) {
	w, _, _ := planShareWorld(t)

	for about, claim := range map[string]any{
		"by name":   planPostType,
		"in a list": []any{planPostType},
	} {
		ordinary := &model.Post{UserId: "uid-dan", ChannelId: townSquare, Message: "hello"}
		ordinary.SetProps(model.StringInterface{"type": claim, planPropID: "0123456789abcdef", "keep": "this"})

		post, reason := posted(t, w.p, ordinary)
		require.Empty(t, reason, about)
		require.Equal(t, "", post.Type, about)
		require.Equal(t, "", post.GetProp("type"), "%s: blanked", about)
		require.Equal(t, "this", post.GetProp("keep"), about)
	}

	plain := &model.Post{Id: "plain", Message: "hello"}
	next := plain.Clone()
	next.SetProps(model.StringInterface{"type": planPostType, "keep": "this"})
	post, reason := updated(t, w.p, next, plain)
	require.Empty(t, reason)
	require.Equal(t, model.StringInterface{"keep": "this"}, post.GetProps(), "and on an edit, taken off")
}

func TestPlanPostEditRefusedAndPropsKept(t *testing.T) {
	w, srv, planID := planShareWorld(t)
	shared := decodePlanShare(t, w.sharePlan(t, "uid-dan", planID, townSquare), http.StatusCreated)
	original := srv.post(shared.PostID)

	edit := func(change func(post *model.Post)) (*model.Post, string) {
		next := original.Clone()
		next.SetProps(copyProps(original.GetProps()))
		change(next)

		return updated(t, w.p, next, original.Clone())
	}

	post, reason := edit(func(post *model.Post) { post.Message = "📅 Matty plan\nSee it: https://elsewhere.example/" })
	require.Nil(t, post)
	require.Equal(t, planPostEditRefused, reason)

	post, reason = edit(func(post *model.Post) {
		props := post.GetProps()
		props[planPropID] = "fedcba9876543210"
		props[planPropStartsAt] = int64(1)
		props["villanet_plan_title"] = "pwned"
		props["attachments"] = []any{map[string]any{"text": "@channel"}}
		post.SetProps(props)
		post.FileIds = model.StringArray{"fakefileid00000000000000000"}
		post.EditAt = 99
	})
	require.Empty(t, reason)
	require.Equal(t, original.GetProps(), post.GetProps(), "exactly the props it had")
	require.Empty(t, post.FileIds)
	require.Zero(t, post.EditAt)

	post, reason = edit(func(post *model.Post) { post.IsPinned = true })
	require.Empty(t, reason)
	require.True(t, post.IsPinned, "a pin passes")
}

// The hooks are one table for both cards now: a pass of one kind never makes
// a card of the other, and the clip's own rules, gone marks included, are
// unchanged (the clip share tests run against these same hooks).
func TestClipCardGuardsUnchanged(t *testing.T) {
	require.Equal(t, cardType{copyType: clipPostCopyType, idProp: clipPropID, editRefused: clipPostEditRefused}, cardTypes[clipPostType])
	require.Equal(t, cardType{copyType: planPostCopyType, idProp: planPropID, editRefused: planPostEditRefused}, cardTypes[planPostType])
	require.Len(t, cardTypes, 2)

	w, _, planID := planShareWorld(t)
	s := w.p.clips()

	// A plan's pass on a clip card.
	s.shares.issue("dddddddddddddddd", sharePass{expires: clipsNow.Add(30 * time.Second), userID: "uid-dan", channelID: townSquare, kind: planPostType, planID: clipA}, clipsNow)
	clip := &model.Post{UserId: "uid-dan", ChannelId: townSquare, Type: clipPostType, Message: "🎬 Matty clip · 0:10"}
	clip.SetProps(model.StringInterface{clipPropID: clipA, clipPropNonce: "dddddddddddddddd"})
	post, reason := posted(t, w.p, clip)
	require.Empty(t, reason)
	require.Equal(t, clipPostCopyType, post.Type)

	// A clip's real share still makes a clip card.
	shared := decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusCreated)
	require.NotEmpty(t, shared.PostID)
	require.NotEmpty(t, planID)

	require.True(t, claimsCard(model.StringInterface{"type": clipPostType}))
	require.True(t, claimsCard(model.StringInterface{"type": planPostType}))
	require.True(t, claimsCard(model.StringInterface{"type": int64(7)}))
	require.False(t, claimsCard(model.StringInterface{"type": "custom_somebody_else"}))
	require.False(t, claimsCard(model.StringInterface{}))
}

func TestScheduleAddAnswersTheID(t *testing.T) {
	s := newAccessScene(t)

	body := fmt.Sprintf(`{"title":"Arma","startsAt":%d}`, nowMillis()+time.Hour.Milliseconds())
	recorder := s.serve(http.MethodPost, "/api/v1/schedule", sceneLinked, body, nil)
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var answer scheduleAddResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer))
	require.True(t, answer.OK)
	require.Regexp(t, regexp.MustCompile(`^[0-9a-f]{16}$`), answer.ID)

	doc, err := s.p.readSchedule()
	require.NoError(t, err)
	require.Len(t, doc.Entries, 1)
	require.Equal(t, answer.ID, doc.Entries[0].ID, "the id answered is the one stored")
}

// A title is not a post, and Mattermost raises mentions only from a post's
// message: no share VillaNET makes carries a title, or a name, in its message
// (design/3.2.0.md §8.5).
func TestNoShareMessageCarriesATitle(t *testing.T) {
	w, srv, planID := planShareWorld(t)

	w.changeStored(t, func(doc *clipsDoc) {
		doc.Clips[0].Title = "Ace with @all and @dave #arma3"
	})
	clipPost := srv.post(decodeShare(t, w.share(t, "uid-dan", clipA, townSquare), http.StatusCreated).PostID)
	planPost := srv.post(decodePlanShare(t, w.sharePlan(t, "uid-dan", planID, offTopic), http.StatusCreated).PostID)

	for _, post := range []*model.Post{clipPost, planPost} {
		require.NotContains(t, post.Message, "@", "no mention can be in a share's message")
		require.NotContains(t, post.Message, "Ace")
		require.NotContains(t, post.Message, "Arma")
		require.NotContains(t, post.Message, "Bob")
		require.NotContains(t, post.Message, "Dave")
	}
}
