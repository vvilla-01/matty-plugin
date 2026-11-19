package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
)

// Sounds on the server, in the System Console (design/3.2.0.md §7.11, the
// JSON in §2.2): every sound on the board with Listen and Delete, who is
// blocked from the soundboard, whether VillaNET's Mumble account can play,
// and Start the board again when the board cannot be read.
//
// Under /api/v1/admin, so the access gate lets them through and each handler
// asks whether the caller is a system admin before anything else: that is
// what keeps the console working for an admin with no token. The audio route
// and the list answer only a request the console made (madeByScript), before
// anything is read: the list reads every sound's audio to check it, and a
// Markdown image in a post is fetched with each reader's session.

const soundsAdminOnly = "Only a system admin can see the sounds on the server."

type adminSoundView struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Emoji   soundEmoji `json:"emoji"`
	OwnerID string     `json:"ownerId"`
	Owner   string     `json:"owner"`
	Ms      int64      `json:"ms"`
	Bytes   int        `json:"bytes"`
	Level   float64    `json:"level"`
	At      int64      `json:"at"`

	// False when the sound's audio is missing or fails its checksum.
	OK bool `json:"ok"`
}

type adminSoundBlocked struct {
	UserID   string `json:"userId"`
	Username string `json:"username"`
	Name     string `json:"name"`
	By       string `json:"by"`
	At       int64  `json:"at"`
}

type adminSoundTotals struct {
	Sounds int `json:"sounds"`
	Bytes  int `json:"bytes"`
	People int `json:"people"`
}

type adminSoundBot struct {
	OK   bool   `json:"ok"`
	Line string `json:"line,omitempty"`
}

type adminSoundsResponse struct {
	On       bool                `json:"on"`
	Readable bool                `json:"readable"`
	Bot      adminSoundBot       `json:"bot"`
	Totals   adminSoundTotals    `json:"totals"`
	Sounds   []adminSoundView    `json:"sounds"`
	Blocked  []adminSoundBlocked `json:"blocked"`
}

// adminOnly answers 403 to anybody but a system admin, and says whether it
// did not.
func (p *Plugin) adminOnly(w http.ResponseWriter, userID string) bool {
	w.Header().Set("Cache-Control", "no-store")

	if p.isSystemAdmin(userID) {
		return true
	}

	writeCodedError(w, http.StatusForbidden, soundsAdminOnly, "forbidden")

	return false
}

// handleAdminSounds is GET /api/v1/admin/sounds: the card's whole view, read
// afresh, each sound's audio checked. That is up to 120 reads of the store, so
// like the audio route it answers only the console itself (design/3.2.0.md
// §2.2): an image in a post that an admin reads must cost nothing.
func (p *Plugin) handleAdminSounds(w http.ResponseWriter, r *http.Request, adminID string) {
	if !p.adminOnly(w, adminID) {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	if !madeByScript(r) {
		writeError(w, http.StatusForbidden, "Ask from the panel.")
		return
	}

	cfg := p.getConfiguration()
	if err := p.readSounds(true); err != nil {
		writeCodedError(w, http.StatusServiceUnavailable, soundText(soundCodeStore, productName), soundCodeStore)
		return
	}
	doc, readable, _ := p.board()

	answer := adminSoundsResponse{On: cfg.soundboardOn(), Readable: readable, Sounds: []adminSoundView{}, Blocked: []adminSoundBlocked{}}

	if cfg.soundboardOn() {
		answer.Bot.OK, answer.Bot.Line = p.checkSoundBot()
	} else {
		answer.Bot.OK = p.getVoice().connected
	}

	names := map[string]string{}
	nameOf := func(userID string) (string, string) {
		user, appErr := p.API.GetUser(userID)
		if appErr != nil || user == nil {
			return "Unknown", ""
		}

		return displayNameOf(user), user.Username
	}
	displayOf := func(userID string) string {
		if name, known := names[userID]; known {
			return name
		}
		name, _ := nameOf(userID)
		names[userID] = name

		return name
	}

	for _, entry := range doc.Entries {
		ok := false
		if raw, appErr := p.API.KVGet(kvSoundPrefix + entry.ID); appErr == nil && len(raw) > 0 {
			_, ok = readSoundBlob(raw)
		}

		answer.Sounds = append(answer.Sounds, adminSoundView{
			ID: entry.ID, Name: entry.Name, Emoji: entry.Emoji, OwnerID: entry.Owner, Owner: displayOf(entry.Owner),
			Ms: entry.Ms, Bytes: entry.Bytes, Level: entry.Level, At: entry.At, OK: ok,
		})
		answer.Totals.Bytes += entry.Bytes
	}
	answer.Totals.Sounds = len(doc.Entries)
	answer.Totals.People = len(sortedSoundOwners(doc.Entries))

	for userID, block := range doc.Blocked {
		name, username := nameOf(userID)
		answer.Blocked = append(answer.Blocked, adminSoundBlocked{UserID: userID, Username: username, Name: name, By: displayOf(block.By), At: block.At})
	}
	sort.Slice(answer.Blocked, func(i, j int) bool {
		if answer.Blocked[i].At != answer.Blocked[j].At {
			return answer.Blocked[i].At < answer.Blocked[j].At
		}

		return answer.Blocked[i].UserID < answer.Blocked[j].UserID
	})

	writeJSON(w, http.StatusOK, answer)
}

// handleAdminSoundAudio is GET /api/v1/admin/sounds/audio?id=: any sound's
// packets, for the card's Listen, with the panel route's file headers.
func (p *Plugin) handleAdminSoundAudio(w http.ResponseWriter, r *http.Request, adminID string) {
	if !p.adminOnly(w, adminID) {
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	if !madeByScript(r) {
		writeError(w, http.StatusForbidden, "Ask from the panel.")
		return
	}

	soundID := r.URL.Query().Get("id")
	if !validPlanID(soundID) {
		writeSoundRefusal(w, http.StatusBadRequest, soundCodeBadRequest, 0, productName)
		return
	}

	doc, _, err := p.board()
	if err != nil {
		writeSoundRefusal(w, http.StatusServiceUnavailable, soundCodeStore, 0, productName)
		return
	}

	onBoard := false
	for _, entry := range doc.Entries {
		onBoard = onBoard || entry.ID == soundID
	}
	if !onBoard {
		writeSoundRefusal(w, http.StatusGone, soundCodeGone, 0, productName)
		return
	}

	p.serveSoundAudio(w, r, soundID)
}

// handleAdminSoundRemove is POST /api/v1/admin/sounds/remove?id=: any sound,
// off the board and out of the store.
func (p *Plugin) handleAdminSoundRemove(w http.ResponseWriter, r *http.Request, adminID string) {
	if !p.adminOnly(w, adminID) {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	soundID := r.URL.Query().Get("id")
	if !validPlanID(soundID) {
		writeSoundRefusal(w, http.StatusBadRequest, soundCodeBadRequest, 0, productName)
		return
	}

	removed, err := p.removeSounds(func(entry soundEntry) (bool, error) {
		return entry.ID != soundID, nil
	})
	if err == nil && len(removed) == 0 {
		err = errSoundGone
	}
	if err != nil {
		p.writeSoundWriteError(w, err)
		return
	}

	p.API.LogInfo("A system admin deleted a sound", "user_id", adminID, "sound_id", soundID)
	p.refreshSoon()

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type soundBlockRequest struct {
	Username string `json:"username"`
	Blocked  *bool  `json:"blocked"`
}

// handleAdminSoundBlock is POST /api/v1/admin/sounds/block {"username",
// "blocked"}: stops somebody adding and playing sounds, or lets them again.
// Their sounds stay until somebody deletes them.
func (p *Plugin) handleAdminSoundBlock(w http.ResponseWriter, r *http.Request, adminID string) {
	if !p.adminOnly(w, adminID) {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	var request soundBlockRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, maxAwardBody+1))
	if err != nil || len(body) > maxAwardBody || json.Unmarshal(body, &request) != nil || request.Blocked == nil {
		writeCodedError(w, http.StatusBadRequest, "Could not read the request.", "bad_request")
		return
	}

	username := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(request.Username), "@")))
	if username == "" {
		writeCodedError(w, http.StatusBadRequest, "Say who.", "bad_request")
		return
	}

	user, appErr := p.API.GetUserByUsername(username)
	switch {
	case appErr != nil && appErr.StatusCode == http.StatusNotFound, appErr == nil && user == nil:
		writeCodedError(w, http.StatusNotFound, "There is no such user.", "no_such_user")
		return
	case appErr != nil:
		writeCodedError(w, http.StatusServiceUnavailable, soundText(soundCodeStore, productName), soundCodeStore)
		return
	}

	blocked := *request.Blocked
	at := p.sounds().now().UnixMilli()

	err = p.changeSounds(func(doc *soundsDoc) (bool, error) {
		_, already := doc.Blocked[user.Id]
		if blocked == already {
			return false, nil
		}

		if !blocked {
			delete(doc.Blocked, user.Id)
			return true, nil
		}

		if len(doc.Blocked) >= maxSoundsBlocked {
			return false, errSoundBoardFull
		}
		if doc.Blocked == nil {
			doc.Blocked = map[string]soundBlock{}
		}
		doc.Blocked[user.Id] = soundBlock{By: adminID, At: at}

		return true, nil
	})
	switch {
	case errors.Is(err, errSoundBoardFull):
		writeCodedError(w, http.StatusConflict, "Fifty people are blocked already. Unblock somebody first.", "full")
		return
	case err != nil:
		writeCodedError(w, http.StatusServiceUnavailable, soundText(soundCodeStore, productName), soundCodeStore)
		return
	}

	if blocked {
		p.API.LogInfo("A system admin blocked somebody from the soundboard", "user_id", adminID, "blocked_user_id", user.Id)
	} else {
		p.API.LogInfo("A system admin let somebody use the soundboard again", "user_id", adminID, "unblocked_user_id", user.Id)
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "userId": user.Id})
}

// handleAdminSoundCheck is POST /api/v1/admin/sounds/check: a fresh look at
// VillaNET's own Mumble session.
func (p *Plugin) handleAdminSoundCheck(w http.ResponseWriter, r *http.Request, adminID string) {
	if !p.adminOnly(w, adminID) {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	ok, line := p.checkSoundBot()
	writeJSON(w, http.StatusOK, adminSoundBot{OK: ok, Line: line})
}

// handleAdminSoundsRestart is POST /api/v1/admin/sounds/restart: Start the
// board again, while it cannot be read, and only then. It writes an empty
// board; the old sounds' audio is deleted by the sweep at the plugin's next
// start, which leaves time to put the list back by hand from
// quarantine_sounds.
func (p *Plugin) handleAdminSoundsRestart(w http.ResponseWriter, r *http.Request, adminID string) {
	if !p.adminOnly(w, adminID) {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	// One conversation with the store about the board at a time, as for
	// every read and write of it (soundStore.io).
	st := p.sounds()
	st.io.Lock()
	defer st.io.Unlock()

	raw, appErr := p.API.KVGet(kvSounds)
	if appErr != nil {
		writeCodedError(w, http.StatusServiceUnavailable, soundText(soundCodeStore, productName), soundCodeStore)
		return
	}
	if _, ok := decodeSounds(raw); ok {
		p.installSounds(raw, p.sounds().now())
		writeCodedError(w, http.StatusConflict, "The list of sounds can be read, so there is nothing to start again.", "readable")

		return
	}

	p.quarantine(kvSounds, raw)

	fresh, err := json.Marshal(soundsDoc{Version: soundsVersion, Entries: []soundEntry{}})
	if err != nil {
		writeCodedError(w, http.StatusInternalServerError, soundText(soundCodeStore, productName), soundCodeStore)
		return
	}
	if written, appErr := p.API.KVCompareAndSet(kvSounds, raw, fresh); appErr != nil || !written {
		writeCodedError(w, http.StatusServiceUnavailable, "The board could not be started again just now. Try again in a moment.", soundCodeStore)
		return
	}
	p.installSounds(fresh, p.sounds().now())

	p.API.LogWarn("A system admin started the soundboard again; the unreadable list is kept aside", "user_id", adminID, "quarantine_key", kvQuarantinePrefix+kvSounds)
	p.refreshSoon()

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
