package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// Twitch connections in the System Console (design/twitch-live.md §2.4,
// §4.10): the callback address to register, whether Twitch's player can run
// here, who has connected, who is blocked, and Delete all Twitch data.
//
// Every route works whatever the switch says, so connections can always be
// removed, and each asks for a system admin before it reads anything in the
// request, as every admin route does. No Twitch id is ever sent: the people
// are named by their Mattermost account and their public Twitch login.

const maxAdminTwitchRequestBytes = 4096

type adminTwitchStatus struct {
	Enabled         bool                 `json:"enabled"`
	Embed           bool                 `json:"embed"`
	Credentials     bool                 `json:"credentials"`
	CallbackURL     string               `json:"callbackUrl"`
	CallbackProblem string               `json:"callbackProblem"`
	EmbedProblem    string               `json:"embedProblem"`
	LastTry         int64                `json:"lastTry"`
	LastGood        int64                `json:"lastGood"`
	Problem         string               `json:"problem"`
	People          []adminTwitchPerson  `json:"people"`
	Blocked         []adminTwitchBlocked `json:"blocked"`
	Hours           adminTwitchHours     `json:"hours"`
}

type adminTwitchPerson struct {
	UserID     string `json:"userId"`
	Name       string `json:"name"`
	Login      string `json:"login"`
	TwitchName string `json:"twitchName"`
	Since      int64  `json:"since"`
	Live       bool   `json:"live"`
	AllDay     bool   `json:"allDay"`
}

type adminTwitchBlocked struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Since  int64  `json:"since"`
}

type adminTwitchHours struct {
	Channels    int    `json:"channels"`
	Seconds     int64  `json:"seconds"`
	Halted      string `json:"halted"`
	Purging     int    `json:"purging"`
	Quarantined bool   `json:"quarantined"`
}

// handleAdminTwitch answers GET and DELETE /api/v1/admin/twitch.
func (p *Plugin) handleAdminTwitch(w http.ResponseWriter, r *http.Request, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, "Only a system admin can manage Twitch connections.")
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, p.adminTwitchStatus())

	case http.MethodDelete:
		query := r.URL.Query()
		now := p.twitchLive().now()

		if query.Get("all") == "1" {
			if err := p.deleteAllTwitchData(now); err != nil {
				p.API.LogWarn("Could not delete all Twitch data", "error", err.Error())
				writeError(w, http.StatusServiceUnavailable, "Not everything could be deleted just now. Press Delete all Twitch data again.")

				return
			}

			p.API.LogInfo("A system admin deleted all Twitch data", "user_id", userID)
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

			return
		}

		target := strings.TrimSpace(query.Get("user"))
		if !model.IsValidId(target) {
			writeError(w, http.StatusBadRequest, "Say whose Twitch account to disconnect.")
			return
		}

		block := query.Get("block") == "1"
		if err := p.disconnectTwitch(target, block, now.UnixMilli()); err != nil {
			adminTwitchError(w, err)
			return
		}

		if block {
			p.API.LogInfo("A system admin disconnected and blocked somebody's Twitch account", "user_id", userID, "blocked_user_id", target)
		} else {
			p.API.LogInfo("A system admin disconnected somebody's Twitch account", "user_id", userID, "disconnected_user_id", target)
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
	}
}

// handleAdminTwitchBlock answers POST /api/v1/admin/twitch/block: block one
// person (which disconnects them), unblock one, or clear the list.
func (p *Plugin) handleAdminTwitchBlock(w http.ResponseWriter, r *http.Request, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, "Only a system admin can manage Twitch connections.")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	var request struct {
		UserID  string `json:"userId"`
		Blocked bool   `json:"blocked"`
		All     bool   `json:"all"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, maxAdminTwitchRequestBytes)).Decode(&request) != nil {
		writeError(w, http.StatusBadRequest, "Could not read the request.")
		return
	}

	nowMS := p.twitchLive().now().UnixMilli()

	if request.All {
		if request.Blocked {
			writeError(w, http.StatusBadRequest, "Block people one at a time.")
			return
		}
		if err := p.clearTwitchBlocks(nowMS); err != nil {
			adminTwitchError(w, err)
			return
		}

		p.API.LogInfo("A system admin cleared the Twitch block list", "user_id", userID)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

		return
	}

	target := strings.TrimSpace(request.UserID)
	if !model.IsValidId(target) {
		writeError(w, http.StatusBadRequest, "Say who to block or unblock.")
		return
	}

	var err error
	if request.Blocked {
		err = p.disconnectTwitch(target, true, nowMS)
	} else {
		err = p.unblockTwitch(target, nowMS)
	}
	if err != nil {
		adminTwitchError(w, err)
		return
	}

	if request.Blocked {
		p.API.LogInfo("A system admin blocked somebody from connecting Twitch", "user_id", userID, "blocked_user_id", target)
	} else {
		p.API.LogInfo("A system admin unblocked somebody from connecting Twitch", "user_id", userID, "unblocked_user_id", target)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAdminTwitchAllDay answers POST /api/v1/admin/twitch/allday.
func (p *Plugin) handleAdminTwitchAllDay(w http.ResponseWriter, r *http.Request, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, "Only a system admin can manage Twitch connections.")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	var request struct {
		UserID string `json:"userId"`
		AllDay bool   `json:"allDay"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, maxAdminTwitchRequestBytes)).Decode(&request) != nil {
		writeError(w, http.StatusBadRequest, "Could not read the request.")
		return
	}

	target := strings.TrimSpace(request.UserID)
	if !model.IsValidId(target) {
		writeError(w, http.StatusBadRequest, "Say whose channel to change.")
		return
	}

	if err := p.setTwitchAllDay(target, request.AllDay, p.twitchLive().now().UnixMilli()); err != nil {
		adminTwitchError(w, err)
		return
	}

	p.API.LogInfo("A system admin changed whether a Twitch channel is 24/7", "user_id", userID, "channel_user_id", target, "all_day", request.AllDay)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func adminTwitchError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errTwitchNotLinked):
		writeError(w, http.StatusNotFound, "That person has not connected Twitch.")
	case errors.Is(err, errTwitchBlockedFull):
		writeError(w, http.StatusConflict, "The block list is full (500 people). Unblock somebody first.")
	case errors.Is(err, errTooBusy):
		writeError(w, http.StatusServiceUnavailable, "Too many changes at once. Try again in a moment.")
	default:
		writeError(w, http.StatusServiceUnavailable, "That could not be saved just now, so nothing has changed. Try again in a moment.")
	}
}

// adminTwitchStatus is the System Console's picture of Twitch live.
func (p *Plugin) adminTwitchStatus() adminTwitchStatus {
	cfg := p.getConfiguration()
	s := p.twitchLive()
	now := s.now()
	site := p.siteURL()

	_, credentials := cfg.twitchCredentials()
	callbackURL, callbackProblem := twitchCallbackURL(site)
	state := s.status()

	answer := adminTwitchStatus{
		Enabled:         cfg.EnableTwitchLive,
		Embed:           cfg.TwitchEmbed,
		Credentials:     credentials,
		CallbackURL:     callbackURL,
		CallbackProblem: callbackProblem,
		EmbedProblem:    twitchEmbedProblem(site),
		Problem:         state.problem,
		People:          []adminTwitchPerson{},
		Blocked:         []adminTwitchBlocked{},
	}
	if !state.lastTry.IsZero() {
		answer.LastTry = state.lastTry.UnixMilli()
	}
	if !state.lastGood.IsZero() {
		answer.LastGood = state.lastGood.UnixMilli()
	}

	nameOf := func(userID string) string {
		if user, appErr := p.API.GetUser(userID); appErr == nil && user != nil {
			return displayNameOf(user)
		}

		return ""
	}

	doc, err := p.readTwitchLinks()
	switch {
	case errors.Is(err, errTwitchLinksUnreadable):
		answer.Problem = twitchLinksProblem
	case err != nil:
		answer.Problem = "The list of Twitch connections could not be read just now."
	default:
		on := cfg.twitchLiveOn()
		for userID, link := range doc.Users {
			_, live := s.liveEntry(link.ID, now)
			answer.People = append(answer.People, adminTwitchPerson{
				UserID:     userID,
				Name:       nameOf(userID),
				Login:      link.Login,
				TwitchName: link.Name,
				Since:      link.At,
				Live:       on && live,
				AllDay:     link.AllDay,
			})
		}
		for userID, at := range doc.Blocked {
			answer.Blocked = append(answer.Blocked, adminTwitchBlocked{UserID: userID, Name: nameOf(userID), Since: at})
		}
		answer.Hours.Purging = len(doc.Purge)
	}

	sort.Slice(answer.People, func(i, j int) bool {
		return byNameThenID(answer.People[i].Name, answer.People[i].UserID, answer.People[j].Name, answer.People[j].UserID)
	})
	sort.Slice(answer.Blocked, func(i, j int) bool {
		return byNameThenID(answer.Blocked[i].Name, answer.Blocked[i].UserID, answer.Blocked[j].Name, answer.Blocked[j].UserID)
	})

	if totals, err := p.loadStats(kvTwitchAirTotals); err == nil {
		for _, entry := range totals {
			if entry.Seconds > 0 {
				answer.Hours.Channels++
				answer.Hours.Seconds += entry.Seconds
			}
		}
	}
	if halted, reason := p.twitchAirHalt.state(); halted {
		answer.Hours.Halted = reason
	}
	answer.Hours.Quarantined = p.twitchQuarantined(now)

	return answer
}

func byNameThenID(nameA, idA, nameB, idB string) bool {
	if a, b := strings.ToLower(nameA), strings.ToLower(nameB); a != b {
		return a < b
	}

	return idA < idB
}
