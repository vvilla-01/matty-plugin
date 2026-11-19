package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// The let-in list: people a system admin lets use the panel without a token,
// to watch only (design/lock-screen.md §3.2). One small document, written
// only by compare-and-set, and a copy on each node that is read again after
// ten seconds. Every failure fails closed: a list that cannot be read lets
// nobody in, and linked people never depend on it at all, because accessOf
// asks about it only for somebody not linked.

const (
	kvLetIn              = "let_in"
	letInVersion         = 1
	maxLetIn             = 500              // a bound somebody chose; far above this server's people
	letInMaxAge          = 10 * time.Second // a node's copy is read again after this
	maxLetInRequestBytes = 4096

	letInSetAsideAlert = "The let-in list could not be read and was set aside. Nobody is let in until people are added again."
	letInNewerAlert    = "The let-in list was saved by a newer " + productName + ". Nobody is let in until that version is back."
)

type letInDoc struct {
	Version int                   `json:"version"`
	People  map[string]letInEntry `json:"people"`
}

type letInEntry struct {
	At int64  `json:"at"` // milliseconds since the epoch
	By string `json:"by"` // the system admin who let them in
}

// letInState is what reading the stored list found.
type letInState int

const (
	letInReadable letInState = iota // nothing stored, or a list this version reads
	letInGarbage                    // will not parse, or is not a list: set aside
	letInNewer                      // written by a newer version: read as nobody, never written
)

// decodeLetIn reads the stored bytes. Anything but a list this version wrote
// reads as nobody: an unreadable list is no reason to let anybody in.
func decodeLetIn(raw []byte) (*letInDoc, letInState) {
	empty := &letInDoc{Version: letInVersion, People: map[string]letInEntry{}}
	if len(raw) == 0 {
		return empty, letInReadable
	}

	var doc letInDoc
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Version < 1 || doc.People == nil {
		return empty, letInGarbage
	}

	// Not set aside: a downgrade followed by an upgrade keeps the list.
	if doc.Version > letInVersion {
		return empty, letInNewer
	}

	return &doc, letInReadable
}

// letInCopy is this node's copy of the list.
type letInCopy struct {
	lock   sync.Mutex
	loaded bool
	people map[string]bool
	state  letInState
	readAt time.Time

	// setAside is this node's own memory of having set an unreadable list
	// aside, for the admins' alert, until a change to the list lands here.
	// The store forgets it the moment the copy is moved out of the way.
	setAside bool

	// gen orders refreshes and writes. A refresh notes it before it reads
	// and installs what it read only if it has not moved meanwhile; a write
	// installs what it wrote at once and moves it. So a refresh that began
	// before a write and finishes after it is thrown away, rather than putting
	// the old list back on the writing node for ten seconds.
	gen uint64

	// The clock, which a test replaces; the access caches' by default.
	now func() time.Time
}

func (p *Plugin) letInList() *letInCopy {
	p.letInOnce.Do(func() {
		p.letInState = &letInCopy{
			people: map[string]bool{},
			now:    func() time.Time { return p.accessCache().now() },
		}
	})

	return p.letInState
}

// freshAt is whether the copy may be used without reading the store. Under
// lock.
func (c *letInCopy) freshAt(now time.Time) bool {
	return c.loaded && !now.Before(c.readAt) && now.Sub(c.readAt) < letInMaxAge
}

// install puts a list in place as of now and moves the generation. Under lock.
func (c *letInCopy) install(people map[string]bool, state letInState, now time.Time) {
	c.people = people
	c.state = state
	if state == letInGarbage {
		c.setAside = true
	}
	c.readAt = now
	c.loaded = true
	c.gen++
}

func peopleOf(doc *letInDoc) map[string]bool {
	people := make(map[string]bool, len(doc.People))
	for userID := range doc.People {
		people[userID] = true
	}

	return people
}

// readLetIn reads the stored list, setting an unreadable one aside.
func (p *Plugin) readLetIn() (*letInDoc, letInState, error) {
	raw, appErr := p.API.KVGet(kvLetIn)
	if appErr != nil {
		return nil, letInReadable, errors.New(appErr.Error())
	}

	doc, state := decodeLetIn(raw)
	if state == letInGarbage {
		p.setAside(kvLetIn, raw, "The let-in list could not be read and has been set aside")
	}

	return doc, state, nil
}

// refreshLetIn reads the list into this node's copy, unless a write landed
// meanwhile. An error leaves the copy as it was, and the caller does not use
// it: a list that cannot be read lets nobody in, however recently it could.
func (p *Plugin) refreshLetIn(now time.Time) error {
	c := p.letInList()

	c.lock.Lock()
	gen := c.gen
	c.lock.Unlock()

	doc, state, err := p.readLetIn()
	if err != nil {
		return err
	}

	c.lock.Lock()
	defer c.lock.Unlock()

	if c.gen == gen {
		c.install(peopleOf(doc), state, now)
	}

	return nil
}

// letInHas is whether an admin has let somebody in, from this node's copy,
// read again once it is letInMaxAge old. An error means the store did not
// answer when the copy was due, and is never covered by the older copy.
func (p *Plugin) letInHas(userID string) (bool, error) {
	c := p.letInList()
	now := c.now()

	c.lock.Lock()
	if c.freshAt(now) {
		in := c.people[userID]
		c.lock.Unlock()

		return in, nil
	}
	c.lock.Unlock()

	if err := p.refreshLetIn(now); err != nil {
		return false, err
	}

	c.lock.Lock()
	defer c.lock.Unlock()

	return c.people[userID], nil
}

// letInProblem is the admins' alert about the list, or "", and whether the
// list may be changed here, as of this node's copy (read again when due). A
// store that does not answer is no alert: it is not something wrong with the
// list.
func (p *Plugin) letInProblem() (string, bool) {
	c := p.letInList()
	now := c.now()

	c.lock.Lock()
	fresh := c.freshAt(now)
	c.lock.Unlock()

	if !fresh {
		_ = p.refreshLetIn(now)
	}

	c.lock.Lock()
	defer c.lock.Unlock()

	return letInAlertOf(c.state, c.setAside), c.state == letInNewer
}

func letInAlertOf(state letInState, setAside bool) string {
	switch {
	case state == letInNewer:
		return letInNewerAlert
	case state == letInGarbage || setAside:
		return letInSetAsideAlert
	default:
		return ""
	}
}

var (
	errLetInNewer = errors.New("the let-in list was saved by a newer version")
	errLetInFull  = errors.New("the let-in list is full")
)

// changeLetIn applies change to the stored list by compare-and-set, and puts
// what it found or wrote in this node's copy at once. change answers whether
// it changed anything; it may run more than once, so it re-checks everything
// it depends on each time. An unreadable list is replaced by a fresh one, a
// copy of it kept; a newer one is never written.
func (p *Plugin) changeLetIn(change func(doc *letInDoc) (bool, error)) (bool, error) {
	var result *letInDoc
	changed := false

	err := p.casUpdate(kvLetIn, func(raw []byte) ([]byte, error) {
		result, changed = nil, false

		doc, state := decodeLetIn(raw)
		switch state {
		case letInNewer:
			return nil, errLetInNewer
		case letInGarbage:
			p.quarantine(kvLetIn, raw)
		}

		did, err := change(doc)
		if err != nil {
			return nil, err
		}

		doc.Version = letInVersion
		result = doc
		if !did {
			return nil, nil
		}

		encoded, err := json.Marshal(doc)
		if err != nil {
			return nil, err
		}
		changed = true

		return encoded, nil
	})
	if err != nil {
		return false, err
	}

	c := p.letInList()
	c.lock.Lock()
	defer c.lock.Unlock()

	c.install(peopleOf(result), letInReadable, c.now())
	if changed {
		c.setAside = false
	}

	return changed, nil
}

// letIn adds somebody, by user id. Adding somebody already there changes
// nothing and does not count against the bound.
func (p *Plugin) letIn(userID, adminID string, now time.Time) (bool, error) {
	return p.changeLetIn(func(doc *letInDoc) (bool, error) {
		if _, already := doc.People[userID]; already {
			return false, nil
		}
		if len(doc.People) >= maxLetIn {
			return false, errLetInFull
		}

		doc.People[userID] = letInEntry{At: now.UnixMilli(), By: adminID}

		return true, nil
	})
}

// letOut takes somebody off the list, by user id. Somebody not on it is not an
// error.
func (p *Plugin) letOut(userID string) (bool, error) {
	return p.changeLetIn(func(doc *letInDoc) (bool, error) {
		if _, there := doc.People[userID]; !there {
			return false, nil
		}

		delete(doc.People, userID)

		return true, nil
	})
}

// The System Console's Access card.

type letInRequest struct {
	Username string `json:"username"`
}

type letInRow struct {
	UserID     string `json:"userId"`
	Username   string `json:"username"`
	Name       string `json:"name"` // the display name, or "" when it would only repeat the username
	At         int64  `json:"at"`
	ByUsername string `json:"byUsername"`
	State      string `json:"state"` // "active", "deactivated" or "gone"
}

type letInListResponse struct {
	People   []letInRow `json:"people"`
	Count    int        `json:"count"`
	Max      int        `json:"max"`
	Problem  string     `json:"problem"`
	ReadOnly bool       `json:"readOnly"`
}

const (
	letInForbidden   = "That is not allowed."
	letInUnreadable  = "The let-in list could not be read. Try again in a moment."
	letInBadRequest  = "Could not read the request."
	letInNewerText   = "This list was saved by a newer " + productName + ", so it cannot be changed here."
	letInBusyText    = "Too many changes at once. Try again in a moment."
	letInNoUserText  = "There is no such user."
	letInAddUnsaved  = "The list could not be saved, so nothing has changed. Try again in a moment."
	letInDropUnsaved = "That could not be saved, so they are still let in. Try again in a moment."
	unlinkUnsaved    = "Could not unlink that stream. Try again in a moment."
)

// readUsername reads {"username":…} from an admin's request: trimmed, one
// leading @ taken off, lower-cased as Mattermost stores usernames. An empty
// body is an empty name, not a broken request.
func readUsername(r *http.Request) (string, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxLetInRequestBytes+1))
	if err != nil || len(body) > maxLetInRequestBytes {
		return "", false
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return "", true
	}

	var request letInRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return "", false
	}

	name := strings.TrimSpace(request.Username)
	name = strings.TrimPrefix(name, "@")

	return strings.ToLower(strings.TrimSpace(name)), true
}

// handleAdminLetIn answers /api/v1/admin/let-in: GET lists who is let in,
// POST {"username":…} lets somebody in, DELETE ?userId=… takes them off.
// Whether the caller is a system admin is asked before anything in the
// request is read; no token is asked for, and no meter is spent.
func (p *Plugin) handleAdminLetIn(w http.ResponseWriter, r *http.Request, adminID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.isSystemAdmin(adminID) {
		writeCodedError(w, http.StatusForbidden, letInForbidden, "forbidden")
		return
	}

	switch r.Method {
	case http.MethodGet:
		p.listLetIn(w)
	case http.MethodPost:
		p.addLetIn(w, r, adminID)
	case http.MethodDelete:
		p.dropLetIn(w, r, adminID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
	}
}

func (p *Plugin) listLetIn(w http.ResponseWriter) {
	c := p.letInList()
	c.lock.Lock()
	gen := c.gen
	c.lock.Unlock()

	doc, state, err := p.readLetIn()
	if err != nil {
		p.API.LogWarn("Could not read the let-in list", "error", err.Error())
		writeCodedError(w, http.StatusServiceUnavailable, letInUnreadable, accessCodeStore)

		return
	}

	// What was just read is the freshest copy there is, unless a change
	// landed on this node meanwhile.
	c.lock.Lock()
	if c.gen == gen {
		c.install(peopleOf(doc), state, c.now())
	}
	problem := letInAlertOf(state, c.setAside)
	c.lock.Unlock()

	// Named from the clip store's people cache: one account lookup per person
	// per ten minutes, and a failed lookup shows the row with its id rather
	// than as an account that has gone.
	now := p.clips().now()
	people := make([]string, 0, 2*len(doc.People))
	for userID, entry := range doc.People {
		people = append(people, userID, entry.By)
	}
	p.lookUpClipPeople(people, now, false)

	s := p.clips()
	rows := make([]letInRow, 0, len(doc.People))
	for userID, entry := range doc.People {
		row := letInRow{UserID: userID, At: entry.At, State: "active"}

		facts := s.factsOf(userID)
		if facts.userKnown {
			switch {
			case facts.gone:
				row.State = "gone"
			case facts.deactivated:
				row.State = "deactivated"
			}
			if !facts.gone {
				row.Username = facts.username
				// The display name falls back to the username for somebody
				// with no nickname or full name. Sent as that, the card would
				// show the username twice ("dan @dan"); §3.3 sends "" instead,
				// and the card shows the username alone.
				if facts.displayName != facts.username {
					row.Name = facts.displayName
				}
			}
		}

		if by := s.factsOf(entry.By); by.userKnown && !by.gone {
			row.ByUsername = by.username
		}

		rows = append(rows, row)
	}

	sort.SliceStable(rows, func(i, j int) bool {
		iGone, jGone := rows[i].State == "gone", rows[j].State == "gone"
		if iGone != jGone {
			return jGone
		}

		iName, jName := strings.ToLower(rows[i].Username), strings.ToLower(rows[j].Username)
		if iName != jName {
			return iName < jName
		}

		return rows[i].UserID < rows[j].UserID
	})

	writeJSON(w, http.StatusOK, letInListResponse{
		People:   rows,
		Count:    len(rows),
		Max:      maxLetIn,
		Problem:  problem,
		ReadOnly: state == letInNewer,
	})
}

// letInWriteError answers a failed change to the list. unsaved is the store's
// text, which says what did not happen.
func letInWriteError(w http.ResponseWriter, err error, unsaved string) {
	switch {
	case errors.Is(err, errLetInNewer):
		writeCodedError(w, http.StatusServiceUnavailable, letInNewerText, "newer")
	case errors.Is(err, errTooBusy):
		writeCodedError(w, http.StatusServiceUnavailable, letInBusyText, "busy")
	case errors.Is(err, errLetInFull):
		writeCodedError(w, http.StatusConflict, "The list is full (500 people). Take somebody off it first.", "list_full")
	default:
		writeCodedError(w, http.StatusServiceUnavailable, unsaved, accessCodeStore)
	}
}

func (p *Plugin) addLetIn(w http.ResponseWriter, r *http.Request, adminID string) {
	username, ok := readUsername(r)
	if !ok {
		writeCodedError(w, http.StatusBadRequest, letInBadRequest, "bad_request")
		return
	}
	if username == "" {
		writeCodedError(w, http.StatusBadRequest, "Say who to let in.", "bad_request")
		return
	}

	user, appErr := p.API.GetUserByUsername(username)
	switch {
	case appErr != nil && appErr.StatusCode == http.StatusNotFound, appErr == nil && user == nil:
		writeCodedError(w, http.StatusNotFound, letInNoUserText, "no_user")
		return
	case appErr != nil:
		p.API.LogWarn("Could not look somebody up to let them in", "error", appErr.Error())
		writeCodedError(w, http.StatusServiceUnavailable, letInAddUnsaved, accessCodeStore)

		return
	case user.IsBot:
		writeCodedError(w, http.StatusBadRequest, "Bots cannot be let in.", "bot")
		return
	case user.DeleteAt != 0:
		writeCodedError(w, http.StatusBadRequest, "That account is deactivated.", "deactivated")
		return
	}

	added, err := p.letIn(user.Id, adminID, time.Now())
	if err != nil {
		if !errors.Is(err, errLetInFull) {
			p.API.LogWarn("Could not let somebody in", "error", err.Error())
		}
		letInWriteError(w, err, letInAddUnsaved)

		return
	}

	if added {
		p.API.LogInfo("A system admin let somebody in to watch", "user_id", adminID, "let_in_user_id", user.Id)
	}
	p.accessChanged(user.Id)

	// Read once, so the card can say it only matters if they unlink.
	key, linkErr := p.linkOf(user.Id)

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "added": added, "linked": linkErr == nil && key != ""})
}

func (p *Plugin) dropLetIn(w http.ResponseWriter, r *http.Request, adminID string) {
	// By id, not by name: the row may be a renamed, deactivated or deleted
	// account.
	target := cleanClipUserID(r.URL.Query().Get("userId"))
	if target == "" {
		writeCodedError(w, http.StatusBadRequest, "Say who to take off the list.", "bad_request")
		return
	}

	removed, err := p.letOut(target)
	if err != nil {
		p.API.LogWarn("Could not take somebody off the let-in list", "error", err.Error())
		letInWriteError(w, err, letInDropUnsaved)

		return
	}

	if removed {
		p.API.LogInfo("A system admin took somebody off the let-in list", "user_id", adminID, "let_out_user_id", target)
	}
	p.accessChanged(target)

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "removed": removed})
}

// handleAdminUnlink answers POST /api/v1/admin/unlink {"username":…}: a
// system admin unlinks somebody's stream, as they could themselves. Revoking a
// token in Broadcast Box unlinks nobody here, because a stored link is never
// checked again, so without this nobody linked could ever be locked out.
// They can link again with their bearer token; the card says so.
func (p *Plugin) handleAdminUnlink(w http.ResponseWriter, r *http.Request, adminID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.isSystemAdmin(adminID) {
		writeCodedError(w, http.StatusForbidden, letInForbidden, "forbidden")
		return
	}

	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	username, ok := readUsername(r)
	if !ok {
		writeCodedError(w, http.StatusBadRequest, letInBadRequest, "bad_request")
		return
	}
	if username == "" {
		writeCodedError(w, http.StatusBadRequest, "Say whose stream to unlink.", "bad_request")
		return
	}

	user, appErr := p.API.GetUserByUsername(username)
	switch {
	case appErr != nil && appErr.StatusCode == http.StatusNotFound, appErr == nil && user == nil:
		writeCodedError(w, http.StatusNotFound, letInNoUserText, "no_user")
		return
	case appErr != nil:
		p.API.LogWarn("Could not look somebody up to unlink them", "error", appErr.Error())
		writeCodedError(w, http.StatusServiceUnavailable, unlinkUnsaved, accessCodeStore)

		return
	}

	key, err := p.linkOf(user.Id)
	if err != nil {
		p.API.LogWarn("Could not read a stream link", "error", err.Error())
		writeCodedError(w, http.StatusServiceUnavailable, unlinkUnsaved, accessCodeStore)

		return
	}

	// The unlink path of handleLink, for them.
	if err := p.clearLink(user.Id); err != nil {
		p.API.LogWarn("Could not unlink a stream", "error", err.Error())
		writeCodedError(w, http.StatusServiceUnavailable, unlinkUnsaved, accessCodeStore)

		return
	}
	if err := p.clearNowPlaying(user.Id); err != nil {
		p.API.LogWarn("Could not take down what is on after an unlink", "user_id", user.Id, "error", err.Error())
	}
	p.refreshSoon()
	p.clipsLinkChanged(user.Id)
	p.accessChanged(user.Id)

	if key != "" {
		p.API.LogInfo("A system admin unlinked somebody's stream", "user_id", adminID, "unlinked_user_id", user.Id)
	}

	// Whether they can still watch, so the card does not promise a lock.
	letIn, letInErr := p.letInHas(user.Id)

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true, "wasLinked": key != "", "letIn": letInErr == nil && letIn})
}
