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

// Steam and Hytale connections in the System Console (design/3.4.0.md §3.8,
// §4.7): where each service sends people back to, whether it is set up, how
// the last Steam check went, who has connected, who is blocked, and Delete
// all.
//
// Every route works whatever the switch says, so connections can always be
// removed, and each asks for a system admin before it reads anything in the
// request, as every admin route does. They sit under /api/v1/admin, which the
// access gate leaves to them, so a locked admin can use them too. That is why
// nothing here says anybody's Steam status, not even "private": locked
// people see none (Victor's answer). The Steam card says how many people
// Steam answered for, never how many are online, which with one or two people
// connected would be their status.

const maxAdminAccountRequestBytes = 4096

type adminSteamStatus struct {
	Enabled         bool   `json:"enabled"`
	KeySet          bool   `json:"keySet"`
	KeyProblem      string `json:"keyProblem"`
	CallbackURL     string `json:"callbackUrl"`
	Realm           string `json:"realm"`
	CallbackProblem string `json:"callbackProblem"`
	LastTry         int64  `json:"lastTry"`
	LastGood        int64  `json:"lastGood"`
	Problem         string `json:"problem"`
	CallsToday      int    `json:"callsToday"`

	// The last good read: active people asked about, and answered for.
	Asked    int `json:"asked"`
	Answered int `json:"answered"`

	// Always lists, [] when empty, never omitempty: the card draws from them.
	People      []adminSteamPerson    `json:"people"`
	Blocked     []adminAccountBlocked `json:"blocked"`
	Quarantined bool                  `json:"quarantined"`
}

// adminSteamPerson has no status field, by design.
type adminSteamPerson struct {
	UserID    string `json:"userId"`
	Name      string `json:"name"`
	SteamName string `json:"steamName"`
	URL       string `json:"url"`
	Since     int64  `json:"since"`

	// False for a deactivated or deleted Mattermost account, which Steam is
	// not asked about and nobody is shown.
	Active bool `json:"active"`

	// True only when Mattermost answered and no longer has the account at
	// all, which is then nameless. Never guessed from a missing name: when
	// Mattermost does not answer, nobody has a name.
	Deleted bool `json:"deleted"`
}

type adminHytaleStatus struct {
	Enabled         bool   `json:"enabled"`
	Credentials     bool   `json:"credentials"`
	CallbackURL     string `json:"callbackUrl"`
	CallbackProblem string `json:"callbackProblem"`
	Problem         string `json:"problem"`

	People      []adminHytalePerson   `json:"people"`
	Blocked     []adminAccountBlocked `json:"blocked"`
	Quarantined bool                  `json:"quarantined"`
}

type adminHytalePerson struct {
	UserID     string `json:"userId"`
	Name       string `json:"name"`
	HytaleName string `json:"hytaleName"`
	Since      int64  `json:"since"`
	Active     bool   `json:"active"`
	Deleted    bool   `json:"deleted"` // as adminSteamPerson's
}

type adminAccountBlocked struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
	Since  int64  `json:"since"`
}

// adminAccountProviderOf is the service an admin route under
// /api/v1/admin/steam or /api/v1/admin/hytale is for.
func adminAccountProviderOf(path string) *accountProvider {
	rest := strings.TrimPrefix(path, adminPathRoot+"/")
	for _, prov := range accountProviders {
		if rest == prov.id || strings.HasPrefix(rest, prov.id+"/") {
			return prov
		}
	}

	return nil
}

// handleAdminAccount answers GET and DELETE /api/v1/admin/{steam,hytale}: the
// card, an admin's Disconnect (?user=, and with block=1 Disconnect and
// block), and Delete all (?all=1).
func (p *Plugin) handleAdminAccount(w http.ResponseWriter, r *http.Request, prov *accountProvider, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, "Only a system admin can manage "+prov.name+" connections.")
		return
	}

	switch r.Method {
	case http.MethodGet:
		if prov.id == accountSteam {
			writeJSON(w, http.StatusOK, p.adminSteamStatus())
			return
		}
		writeJSON(w, http.StatusOK, p.adminHytaleStatus())

	case http.MethodDelete:
		query := r.URL.Query()
		now := p.accounts().now()

		if query.Get("all") == "1" {
			if err := p.deleteAllAccountData(prov, now); err != nil {
				p.API.LogWarn("Could not delete all "+prov.name+" data", "error", err.Error())
				writeError(w, http.StatusServiceUnavailable, "Not everything could be deleted just now. Press Delete all "+prov.name+" data again.")

				return
			}

			p.API.LogInfo("A system admin deleted all "+prov.name+" data", "user_id", userID)
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

			return
		}

		target := strings.TrimSpace(query.Get("user"))
		if !model.IsValidId(target) {
			writeError(w, http.StatusBadRequest, "Say whose "+prov.name+" account to disconnect.")
			return
		}

		// Nobody connected is not an error: with block=1 they are still
		// blocked, and pressing twice is harmless.
		block := query.Get("block") == "1"
		if err := p.disconnectAccount(prov, target, block, now.UnixMilli()); err != nil {
			adminAccountError(w, prov, err, true)
			return
		}

		if block {
			p.API.LogInfo("A system admin disconnected and blocked somebody's "+prov.name+" account", "user_id", userID, "blocked_user_id", target)
		} else {
			p.API.LogInfo("A system admin disconnected somebody's "+prov.name+" account", "user_id", userID, "disconnected_user_id", target)
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
	}
}

// handleAdminAccountBlock answers POST /api/v1/admin/{steam,hytale}/block:
// block one person (which disconnects them), unblock one, or clear the list
// ({"all": true, "blocked": false}).
func (p *Plugin) handleAdminAccountBlock(w http.ResponseWriter, r *http.Request, prov *accountProvider, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, "Only a system admin can manage "+prov.name+" connections.")
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
	if json.NewDecoder(io.LimitReader(r.Body, maxAdminAccountRequestBytes)).Decode(&request) != nil {
		writeError(w, http.StatusBadRequest, "Could not read the request.")
		return
	}

	nowMS := p.accounts().now().UnixMilli()

	if request.All {
		if request.Blocked {
			writeError(w, http.StatusBadRequest, "Block people one at a time.")
			return
		}
		if err := p.clearAccountBlocks(prov, nowMS); err != nil {
			adminAccountError(w, prov, err, false)
			return
		}

		p.API.LogInfo("A system admin cleared the "+prov.name+" block list", "user_id", userID)
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
		err = p.disconnectAccount(prov, target, true, nowMS)
	} else {
		err = p.unblockAccount(prov, target, nowMS)
	}
	if err != nil {
		adminAccountError(w, prov, err, request.Blocked)
		return
	}

	if request.Blocked {
		p.API.LogInfo("A system admin blocked somebody from connecting "+prov.name, "user_id", userID, "blocked_user_id", target)
	} else {
		p.API.LogInfo("A system admin unblocked somebody from connecting "+prov.name, "user_id", userID, "unblocked_user_id", target)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// adminAccountError words a write that did not happen. A disconnect that
// could not be saved says so in Disconnect's own words, with its code.
func adminAccountError(w http.ResponseWriter, prov *accountProvider, err error, disconnecting bool) {
	switch {
	case errors.Is(err, errAccountBlockedFull):
		writeError(w, http.StatusConflict, "The block list is full (500 people). Unblock somebody first.")
	case errors.Is(err, errTooBusy):
		writeError(w, http.StatusServiceUnavailable, "Too many changes at once. Try again in a moment.")
	case errors.Is(err, errAccountLinksUnreadable):
		writeError(w, http.StatusServiceUnavailable, accountLinksProblem(prov))
	case disconnecting:
		writeCodedError(w, http.StatusServiceUnavailable, productName+" could not disconnect "+prov.name+" just now. Try again in a moment.", accountCodeStore)
	default:
		writeError(w, http.StatusServiceUnavailable, "That could not be saved just now, so nothing has changed. Try again in a moment.")
	}
}

// adminAccountPeople is who is named on a card, with the Mattermost name of
// each, whether their account is still active, and whether it is gone for
// good, from one lookup. An account Mattermost no longer has comes back with
// no name, and deleted. When Mattermost does not answer, names are left out,
// nobody is said to be deleted, and the loop's last word on who is active
// stands.
func (p *Plugin) adminAccountPeople(doc *accountLinks) (names map[string]string, active, deleted map[string]bool) {
	everyone := map[string]bool{}
	for userID := range doc.Users {
		everyone[userID] = true
	}
	for userID := range doc.Blocked {
		everyone[userID] = true
	}

	names, active, deleted = map[string]string{}, map[string]bool{}, map[string]bool{}
	if len(everyone) == 0 {
		return names, active, deleted
	}

	users, appErr := p.API.GetUsersByIds(sortedIDs(everyone))
	if appErr != nil {
		s := p.accounts()
		for userID := range everyone {
			active[userID] = !s.inactiveNow(userID)
		}

		return names, active, deleted
	}

	for userID := range everyone {
		deleted[userID] = true
	}
	for _, user := range users {
		if user == nil || !everyone[user.Id] {
			continue
		}
		names[user.Id] = displayNameOf(user)
		active[user.Id] = user.DeleteAt == 0
		deleted[user.Id] = false
	}

	return names, active, deleted
}

// adminAccountDocument is the card's fresh read of a service's document, or
// the sentence that says why there is none.
func (p *Plugin) adminAccountDocument(prov *accountProvider) (*accountLinks, string) {
	doc, _, err := p.readAccountLinks(prov)
	switch {
	case errors.Is(err, errAccountLinksUnreadable):
		return nil, accountLinksProblem(prov)
	case err != nil:
		return nil, "The list of " + prov.name + " connections could not be read just now."
	default:
		return doc, ""
	}
}

// adminAccountBlockedList is a card's blocked people, by name.
func adminAccountBlockedList(doc *accountLinks, names map[string]string) []adminAccountBlocked {
	blocked := []adminAccountBlocked{}
	for userID, at := range doc.Blocked {
		blocked = append(blocked, adminAccountBlocked{UserID: userID, Name: names[userID], Since: at})
	}
	sort.Slice(blocked, func(i, j int) bool {
		return byNameThenID(blocked[i].Name, blocked[i].UserID, blocked[j].Name, blocked[j].UserID)
	})

	return blocked
}

// adminSteamStatus is the Steam connections card.
func (p *Plugin) adminSteamStatus() adminSteamStatus {
	cfg := p.getConfiguration()
	s := p.accounts()
	site := p.siteURL()
	card := s.steamCardState()

	answer := adminSteamStatus{
		Enabled:     cfg.EnableSteam,
		KeySet:      cfg.SteamWebAPIKey != "",
		Problem:     card.problem,
		CallsToday:  s.steamCallsToday(s.now()),
		Asked:       card.asked,
		Answered:    card.answered,
		People:      []adminSteamPerson{},
		Blocked:     []adminAccountBlocked{},
		Quarantined: p.accountQuarantined(steamProvider),
	}

	// Nothing to register: the card shows these for reference only.
	answer.CallbackURL, answer.CallbackProblem = steamCallbackURL(site)
	if answer.CallbackProblem == "" {
		answer.Realm = steamRealm(site)
	}

	switch {
	case card.keyRefused:
		answer.KeyProblem = steamKeyRefused
	case answer.KeySet:
		if _, ok := cfg.steamKey(); !ok {
			answer.KeyProblem = "The Steam Web API key has characters a key cannot have."
		}
	}

	if !card.lastTry.IsZero() {
		answer.LastTry = card.lastTry.UnixMilli()
	}
	if !card.lastGood.IsZero() {
		answer.LastGood = card.lastGood.UnixMilli()
	}

	doc, problem := p.adminAccountDocument(steamProvider)
	if doc == nil {
		if answer.Problem == "" {
			answer.Problem = problem
		}

		return answer
	}

	names, active, deleted := p.adminAccountPeople(doc)
	for userID, link := range doc.Users {
		answer.People = append(answer.People, adminSteamPerson{
			UserID:    userID,
			Name:      names[userID],
			SteamName: link.Name,
			URL:       steamProfileURL(link.ID),
			Since:     link.At,
			Active:    active[userID],
			Deleted:   deleted[userID],
		})
	}
	sort.Slice(answer.People, func(i, j int) bool {
		return byNameThenID(answer.People[i].Name, answer.People[i].UserID, answer.People[j].Name, answer.People[j].UserID)
	})
	answer.Blocked = adminAccountBlockedList(doc, names)

	return answer
}

// adminHytaleStatus is the Hytale connections card.
func (p *Plugin) adminHytaleStatus() adminHytaleStatus {
	cfg := p.getConfiguration()
	s := p.accounts()

	answer := adminHytaleStatus{
		Enabled:     cfg.EnableHytale,
		Credentials: cfg.HytaleClientID != "" && cfg.HytaleClientSecret != "",
		People:      []adminHytalePerson{},
		Blocked:     []adminAccountBlocked{},
		Quarantined: p.accountQuarantined(hytaleProvider),
	}
	answer.CallbackURL, answer.CallbackProblem = hytaleCallbackURL(p.siteURL())

	// The application's own problem, from the setting or from what the last
	// sign-in said.
	_, _, shaped := cfg.hytaleCredentials()
	switch {
	case answer.Credentials && !shaped:
		answer.Problem = "The Hytale Client ID or Client Secret has characters they cannot have."
	case s.signInProblem(hytaleProvider) == hytaleProblemCredentials:
		answer.Problem = "Hytale refused the Client ID or Client Secret."
	case s.signInProblem(hytaleProvider) == hytaleProblemScope:
		answer.Problem = "Hytale has not granted this application the scope hytale:profile yet."
	}

	doc, problem := p.adminAccountDocument(hytaleProvider)
	if doc == nil {
		if answer.Problem == "" {
			answer.Problem = problem
		}

		return answer
	}

	names, active, deleted := p.adminAccountPeople(doc)
	for userID, link := range doc.Users {
		answer.People = append(answer.People, adminHytalePerson{
			UserID:     userID,
			Name:       names[userID],
			HytaleName: link.Name,
			Since:      link.At,
			Active:     active[userID],
			Deleted:    deleted[userID],
		})
	}
	sort.Slice(answer.People, func(i, j int) bool {
		return byNameThenID(answer.People[i].Name, answer.People[i].UserID, answer.People[j].Name, answer.People[j].UserID)
	})
	answer.Blocked = adminAccountBlockedList(doc, names)

	return answer
}
