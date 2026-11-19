package main

import "net/http"

// The starting list offered on System Console → Plugins → Matty as "Start
// from the old bot's list" while the birthday list is empty.
//
// Empty since 3.7.2. It held the list the old cron script had, real people's
// names and dates of birth, and those do not ship in the plugin: a server that
// wants a list types it in, or restores it from its own settings backup. The
// route stays, answering an empty list in the same shape, so the System
// Console page and what it asks for do not change.
//
// Served from here, not from the webapp, as the list was: Mattermost serves a
// plugin's webapp bundle to every signed-in user, and the System Console
// promises the birthday list is for system admins only.
type birthdaySeedEntry struct {
	Username       string   `json:"username,omitempty"`
	Name           string   `json:"name"`
	Birthday       string   `json:"birthday"`
	BirthYear      int      `json:"birth_year"`
	ParentUsername []string `json:"parent_username,omitempty"`
}

// Not nil: it is answered as [] rather than null, which the System Console
// reads as a list.
var birthdaySeed = []birthdaySeedEntry{}

// handleBirthdaySeed hands the starting list to a system admin. Nothing is
// written: the System Console puts it in the list's box, and it is saved, or
// not, with the page.
func (p *Plugin) handleBirthdaySeed(w http.ResponseWriter, r *http.Request, userID string) {
	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, "Only a system admin can see the birthday list.")
		return
	}

	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	// Dates of birth, when there were any: not kept in the browser's cache,
	// as the settings export that carries the list is not.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, birthdaySeed)
}
