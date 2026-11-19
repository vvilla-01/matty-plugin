package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

// The write endpoints for the schedule.
//
// Both of them end the same way: change the document, then refreshSoon().
// Neither broadcasts anything itself. The poller's publish() already coalesces,
// fingerprints and fans out, so somebody adding and removing entries in a loop
// costs one push per poll cycle instead of one per click — and the panel has a
// single path for receiving state rather than two that can disagree.

type scheduleAddRequest struct {
	Title string `json:"title"`

	// Category is "movie", "game" or absent, and anything else is treated as
	// absent. An old client that does not send one is not an error.
	Category string `json:"category"`

	// An instant in epoch milliseconds, converted from the streamer's own
	// wall-clock pick by their own browser. The server never guesses a zone.
	StartsAt int64 `json:"startsAt"`

	// Media is the ref of the title picked in the planning form — as its
	// search returned it, "tmdb:movie:949", "steam:1245620" or "igdb:113360"
	// — or absent.
	// Only the ref: the details are looked up here rather than taken from the
	// browser, so what every panel shows about a title is what the provider
	// said, not what somebody's console said it said.
	Media string `json:"media"`
}

type scheduleRemoveRequest struct {
	ID string `json:"id"`
}

// scheduleEditRequest is a whole plan again, as the form has it now, for the
// plan with this id. The same fields as an add, because it is the same form.
type scheduleEditRequest struct {
	ID string `json:"id"`
	scheduleAddRequest
}

type scheduleJoinRequest struct {
	ID   string `json:"id"`
	Join bool   `json:"join"`
}

func decodeBody(r *http.Request, into any) bool {
	return json.NewDecoder(io.LimitReader(r.Body, maxCommunityBody)).Decode(into) == nil
}

// communityError maps the store's errors onto status codes. Their text is
// written to be read by a person in a sidebar, so it passes through.
// The codes of a plan that could not be made, changed or joined, beside the
// gate's and the store's: "gone" is plan_share.go's word for the same plan.
const (
	planCodeBusy     = "busy"
	planCodeGone     = "gone"
	planCodeNotYours = "not_yours"
	planCodeFull     = "full"
	planCodeOwnPlan  = "own_plan"
)

func communityError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errStore):
		// The store's own error text names tables and drivers. It is logged
		// where it was raised; a sidebar gets a sentence instead.
		writeCodedError(w, http.StatusServiceUnavailable, "Could not save that just now. Try again in a moment.", accessCodeStore)
	case errors.Is(err, errTooBusy):
		writeCodedError(w, http.StatusConflict, err.Error(), planCodeBusy)
	case errors.Is(err, errNoPlan):
		writeCodedError(w, http.StatusNotFound, err.Error(), planCodeGone)
	case errors.Is(err, errNotYourPlan):
		writeCodedError(w, http.StatusForbidden, err.Error(), planCodeNotYours)
	case errors.Is(err, errPlanFull):
		writeCodedError(w, http.StatusBadRequest, err.Error(), planCodeFull)
	case errors.Is(err, errOwnPlan):
		writeCodedError(w, http.StatusBadRequest, err.Error(), planCodeOwnPlan)
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

// The refusals for somebody with no stream linked, said the way the sidebar
// says everything else. Changing a plan says that cancelling it still works,
// because the person reading it may have planned it before they unlinked, and
// the × is the one thing left that they can do with it.
const (
	planNeedsLink   = "Link your stream first — planning is for streamers."
	changeNeedsLink = "Link your stream first — changing a plan is for streamers. Cancelling it never needs one."
	joinNeedsLink   = "Link your stream first — joining is for streamers."

	// The title search exists for the planning form, so it is for the same
	// people: anyone else asking would only be having the server call TMDB,
	// Steam or IGDB on their behalf.
	searchNeedsLink = "Link your stream first — looking up titles is for streamers."
)

// requireLinkedStream is the gate on the schedule writes that are for
// streamers: planning, changing a plan, and joining one. The link, proven with
// the person's Broadcast Box token, is what makes somebody a streamer here.
// Taking something back — cancelling your own plan, leaving one you joined —
// never comes through here, so unlinking cannot strand anybody's name on the
// schedule.
//
// Behind the access gate this is layer 2: it keeps the refusal's own words,
// and adds the code the gate would have given, "watch_only" for somebody an
// admin let in and "locked" for anybody else. The gate already refused them,
// so this answers only in a race, and on joining, which shares its route with
// leaving.
//
// A store that will not answer is reported as that, not as "you are not
// linked": telling a linked streamer to link again because of a database blip
// would send them off to paste their token for nothing.
func (p *Plugin) requireLinkedStream(w http.ResponseWriter, userID, refusal string) bool {
	level, _, err := p.accessOf(userID)
	if err != nil {
		p.API.LogWarn("Could not read a stream link", "error", err.Error())
		writeCodedError(w, http.StatusServiceUnavailable, "Could not save that just now. Try again in a moment.", accessCodeStore)

		return false
	}

	switch level {
	case accessLinked:
		return true
	case accessLetIn:
		writeCodedError(w, http.StatusForbidden, refusal, accessCodeWatchOnly)
	default:
		writeCodedError(w, http.StatusForbidden, refusal, accessCodeLocked)
	}

	return false
}

func (p *Plugin) handleScheduleAdd(w http.ResponseWriter, r *http.Request, userID string) {
	// Before the body is read or a title looked up: somebody who cannot plan
	// must not make the server call TMDB, Steam or IGDB on their behalf either.
	if !p.requireLinkedStream(w, userID, planNeedsLink) {
		return
	}

	var request scheduleAddRequest
	if !decodeBody(r, &request) {
		writeError(w, http.StatusBadRequest, "Could not read the request.")
		return
	}

	var media *scheduleMedia

	if picked := strings.TrimSpace(request.Media); picked != "" {
		ref, ok := parseMediaRef(picked)
		if !ok {
			writeError(w, http.StatusBadRequest, "That title is not one this server can look up.")
			return
		}

		// Refused rather than quietly dropped, unlike an unknown category: a
		// title picked under the wrong icon is a client that has lost track of
		// what it is showing, and a film's poster on a card with a gamepad on
		// it would be a small lie on every screen.
		if ref.category() != scheduleCategory(request.Category) {
			writeError(w, http.StatusBadRequest, "A movie or show goes with the film icon, and a game with the game icon.")
			return
		}

		if _, err := p.checkPlan(userID, request.Title, request.StartsAt, nowMillis()); err != nil {
			communityError(w, err)
			return
		}

		// Somebody at their limit is told so before the server calls out to
		// TMDB, Steam or IGDB on their behalf, not after.
		if err := p.checkRoom(userID, nowMillis()); err != nil {
			communityError(w, err)
			return
		}

		media = p.planMedia(r.Context(), ref)
	}

	// The clock is read again: the lookup above can take seconds, and a plan
	// for "in one minute" should be judged against the moment it is saved.
	id, err := p.addScheduleEntryWithMedia(userID, request.Title, request.Category, media, request.StartsAt, nowMillis())
	if err != nil {
		communityError(w, err)
		return
	}

	// The new plan's id, so the plan sheet can offer "Share in" at once.
	p.refreshSoon()
	writeJSON(w, http.StatusOK, scheduleAddResponse{OK: true, ID: id})
}

// scheduleAddResponse is what planning a stream answers: that it worked, and
// the new plan's id.
type scheduleAddResponse struct {
	OK bool   `json:"ok"`
	ID string `json:"id"`
}

// handleScheduleRemove cancels a plan. No link is asked for, unlike planning
// one: somebody who planned a stream and has since unlinked must still be able
// to take it down, rather than leave a promise on every screen that they now
// cannot change. An admin can cancel anybody's, as before.
func (p *Plugin) handleScheduleRemove(w http.ResponseWriter, r *http.Request, userID string) {
	var request scheduleRemoveRequest
	if !decodeBody(r, &request) {
		writeError(w, http.StatusBadRequest, "Could not read the request.")
		return
	}

	if err := p.removeScheduleEntry(userID, request.ID, p.isSystemAdmin(userID), nowMillis()); err != nil {
		communityError(w, err)
		return
	}

	p.refreshSoon()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleScheduleEdit changes one of the caller's own plans, and only while
// their stream is linked, the same as making one. Cancelling it does not need
// the link; see handleScheduleRemove.
//
// The linked title is only looked up again when it CHANGED. Saving a plan with
// the same title it had keeps the details stored when it was made — no call
// out, and nothing lost if lookups have been switched off since.
func (p *Plugin) handleScheduleEdit(w http.ResponseWriter, r *http.Request, userID string) {
	if !p.requireLinkedStream(w, userID, changeNeedsLink) {
		return
	}

	var request scheduleEditRequest
	if !decodeBody(r, &request) || strings.TrimSpace(request.ID) == "" {
		writeError(w, http.StatusBadRequest, "Could not read the request.")
		return
	}

	// Theirs, and still there, before anything else is spent on it.
	existing, err := p.ownPlan(userID, request.ID, nowMillis())
	if err != nil {
		communityError(w, err)
		return
	}

	change := clearPlanMedia
	var media *scheduleMedia

	if picked := strings.TrimSpace(request.Media); picked != "" {
		ref, ok := parseMediaRef(picked)
		if !ok {
			writeError(w, http.StatusBadRequest, "That title is not one this server can look up.")
			return
		}

		if ref.category() != scheduleCategory(request.Category) {
			writeError(w, http.StatusBadRequest, "A movie or show goes with the film icon, and a game with the game icon.")
			return
		}

		if existing.Media != nil && existing.Media.Ref == ref.String() {
			change = keepPlanMedia
		} else {
			if err := p.checkPlanChange(userID, request.Title, request.StartsAt, existing.StartsAt, nowMillis()); err != nil {
				communityError(w, err)
				return
			}

			change = setPlanMedia
			media = p.planMedia(r.Context(), ref)
		}
	}

	if err := p.editScheduleEntry(userID, request.ID, request.Title, request.Category, change, media, request.StartsAt, nowMillis()); err != nil {
		communityError(w, err)
		return
	}

	p.refreshSoon()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleScheduleJoin says the caller is in on somebody's plan, or takes it
// back. Only for somebody with a stream of their own linked: joining means
// streaming in it, and the link — proven with their Broadcast Box token — is
// what makes somebody a streamer here.
func (p *Plugin) handleScheduleJoin(w http.ResponseWriter, r *http.Request, userID string) {
	var request scheduleJoinRequest
	if !decodeBody(r, &request) || strings.TrimSpace(request.ID) == "" {
		writeError(w, http.StatusBadRequest, "Could not read the request.")
		return
	}

	// Joining needs a linked stream; LEAVING never does. Somebody who joined
	// and has since unlinked must still be able to take their name back off.
	if request.Join && !p.requireLinkedStream(w, userID, joinNeedsLink) {
		return
	}

	if err := p.joinScheduleEntry(userID, request.ID, request.Join, nowMillis()); err != nil {
		communityError(w, err)
		return
	}

	p.refreshSoon()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
