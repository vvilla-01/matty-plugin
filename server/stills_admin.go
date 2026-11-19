package main

import (
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Admin controls for the preview stills: what is on the cards right now and
// who put it there, taking a picture down, and stopping somebody from sharing
// any more.
//
// A still is whatever the browser holding the lease sent (see stills.go). The
// server checks that it is a JPEG of a card's shape, and cannot tell a frame of
// the stream from anything else of that shape. The lease says who may send one
// and the log says who did; this is what an admin does about it when the
// answer is somebody who should not have.
//
// The block list is the one thing here that is kept. The stills themselves
// live in memory and are gone within minutes, but a person blocked on Tuesday
// has to stay blocked through Wednesday's upgrade. It is one KV document, and
// it only changes when an admin changes it, which goes through here.
//
// Every change is made to the document as it is stored at that moment, never
// to this node's copy of it: read afresh, changed, and written back only if
// nobody has written it in between. On a cluster that is what stops a block
// made through one node being wiped by a change made through another, and an
// unblock finding nobody to unblock because the block was made elsewhere. The
// node a change goes through puts it in force at once.
//
// Claims and uploads, which are checked against it on every request, go by a
// copy held in memory: read the first time anybody claims or uploads, and read
// again whenever an admin changes the blocks through that node. So on a
// cluster a block made through one node is in force on another only once the
// other next starts, or next has an admin's change land on it. That is the
// price of not reading the KV store on every claim, and it is paid only on a
// cluster, where the stills are per node already. The admin list reads the
// document as stored but leaves the copy in force alone: see
// currentStillBlocks.

const (
	kvStillsBlocked = "stills_blocked"

	// How long a block list that could not be read is left before it is
	// tried again. Until it has been read, every claim and upload is
	// refused, and each would otherwise be another read of a store that has
	// just failed and another line in the log saying so.
	stillBlocksRetry = 30 * time.Second

	// How many times a change to the block list is tried when the stored
	// list was written by somebody else between reading it and writing it
	// back. Each try reads the list afresh, so losing every one of them takes
	// that many other changes landing in the same moment; and one that does
	// is refused as unsaved, which changes nothing, rather than written over
	// somebody else's.
	stillBlocksAttempts = 5

	// The pause before the second try, and a step longer before each one
	// after it: 50, 100, 150 and 200 ms, a half-second at most, all while
	// nobody else is changing the list here.
	stillBlocksBackoff = 50 * time.Millisecond

	// A block request is one user id in a JSON object.
	maxBlockRequestBytes = 4096

	stillsAdminOnly = "Only a system admin can manage preview pictures."
	stillsUnread    = "The list of who can't share preview pictures could not be read. Try again in a moment."
)

var (
	errStillBlocksUnread  = errors.New("the preview block list could not be read")
	errStillBlocksUnsaved = errors.New("the preview block list could not be saved")
)

// stillBlock is one person an admin has blocked from sharing stills, as the
// list is stored: keyed by their user id, with when and by whom.
type stillBlock struct {
	BlockedAt time.Time `json:"blockedAt"`
	BlockedBy string    `json:"blockedBy"`
}

// adminStill is one still being shown right now, for the admin list. By
// handle and title, never by key: the admin list is a page in the browser
// like any other.
type adminStill struct {
	Handle        string `json:"handle"`
	Title         string `json:"title"`
	Version       string `json:"version"`
	At            string `json:"at"`
	ByUserID      string `json:"byUserId"`
	ByUsername    string `json:"byUsername"`
	ByDisplayName string `json:"byDisplayName"`
}

// blockedSharer is one person on the block list, for the admin list.
type blockedSharer struct {
	UserID      string `json:"userId"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	BlockedAt   string `json:"blockedAt"`
}

// Both lists are always present, empty rather than null, so the page has
// nothing to guard against.
type adminStillsResponse struct {
	Stills  []adminStill    `json:"stills"`
	Blocked []blockedSharer `json:"blocked"`
}

type blockRequest struct {
	UserID string `json:"userId"`
}

// shownStill is one still being served right now, and whose stream it is.
type shownStill struct {
	handle  string
	picture *stillPicture
}

// showingAll is every still current would serve right now. live says which
// handles are on the listing and may be previewed, as previewable does.
func (s *stillStore) showingAll(live map[string]bool, now time.Time) []shownStill {
	s.lock.Lock()
	defer s.lock.Unlock()

	shown := make([]shownStill, 0, len(s.entries))
	for handle, entry := range s.entries {
		if picture := entry.showing(live[handle], now); picture != nil {
			shown = append(shown, shownStill{handle: handle, picture: picture})
		}
	}

	return shown
}

// remove takes handle's still down, and reports who took it and whether there
// was one to take down.
//
// Whether or not it is still being served: a picture that has aged past
// showing is one nobody needs, and one kept past the off-air grace could be
// shown again if its stream came back before a sweep.
func (s *stillStore) remove(handle string) (string, bool) {
	s.lock.Lock()
	defer s.lock.Unlock()

	entry := s.entries[handle]
	if entry == nil || entry.picture == nil {
		return "", false
	}

	by := entry.picture.by
	s.bytes -= len(entry.picture.data)
	entry.picture = nil

	// The lease goes with it. Whoever holds it may be the person who put the
	// picture there, about to put up another; and whoever it is, the card
	// should have a fresh picture as soon as somebody can take one, not after
	// the wait the removed still, or a capture that came to nothing, would
	// have held the next lease back for.
	entry.lease = nil
	entry.attempted, entry.misses = time.Time{}, 0

	return by, true
}

// enforceBlocks puts blocks in force as the list of who may not share, and
// takes down everything on the cards of userID and of anybody else it blocks
// who was not blocked here already: every still they took and every lease
// they hold. It reports how many stills came down. userID is empty when the
// list has only been read, or has lost somebody.
//
// Anybody new to the list and not only userID, because on a cluster a list
// read afresh can bring a block made through another node, and that node
// could not take down what the person put up through this one.
//
// One lock for both, so that a claim sees the block and the stills come down
// in the same moment. Taking the leases is what refuses an upload that was
// already on its way: put checks the lease again, under this lock.
func (s *stillStore) enforceBlocks(blocks map[string]stillBlock, userID string) int {
	s.lock.Lock()
	defer s.lock.Unlock()

	down := map[string]bool{}
	if userID != "" {
		down[userID] = true
	}
	for blocked := range blocks {
		if _, already := s.blocked[blocked]; !already {
			down[blocked] = true
		}
	}

	s.blocked = blocks
	if len(down) == 0 {
		return 0
	}

	removed := 0
	for _, entry := range s.entries {
		cleared := false

		if entry.picture != nil && down[entry.picture.by] {
			s.bytes -= len(entry.picture.data)
			entry.picture = nil
			removed++
			cleared = true
		}

		if entry.lease != nil && down[entry.lease.userID] {
			entry.lease = nil
			cleared = true
		}

		// As for remove, nothing of theirs holds the stream back. Unless
		// somebody else is capturing it this moment, in which case the
		// attempt is theirs and stands.
		if cleared && entry.lease == nil {
			entry.attempted, entry.misses = time.Time{}, 0
		}
	}

	return removed
}

// sharerBlocked reports whether userID is on the block list. A list that has
// not been read blocks nobody, so the caller makes sure of it first.
func (s *stillStore) sharerBlocked(userID string) bool {
	s.lock.Lock()
	defer s.lock.Unlock()

	_, blocked := s.blocked[userID]

	return blocked
}

// blocksLoaded reports whether the block list has been read.
func (s *stillStore) blocksLoaded() bool {
	s.lock.Lock()
	defer s.lock.Unlock()

	return s.blocked != nil
}

// loadStillBlocks reads the block list from the KV store, unless it has been
// read already or a read has failed too recently to try again. The caller
// holds blockLock. This is the way in for claims and uploads, which is why it
// does not read again once it has a list: see the top of this file.
func (p *Plugin) loadStillBlocks(s *stillStore, now time.Time) error {
	if s.blocksLoaded() {
		return nil
	}

	if !s.blocksFailedAt.IsZero() && now.Sub(s.blocksFailedAt) < stillBlocksRetry {
		return errStillBlocksUnread
	}

	_, _, err := p.reloadStillBlocks(s, now)

	return err
}

// reloadStillBlocks reads the block list from the KV store whether or not this
// node has read it before, and puts what it finds in force here. It hands back
// the list, for the caller to change, and the bytes it was read from, for the
// change to be written over exactly those.
//
// Everything an admin does starts here rather than from the copy in force,
// because on a cluster that copy can be missing a change made through another
// node. The caller holds blockLock, which is what keeps the list in force the
// one this node read or wrote last: without it, a read that finished late
// could put an older list back over a newer one.
//
// A read that fails leaves the list in force as it was, since it is no
// evidence that anything has changed, and fails the request rather than
// showing the admin, or writing back, a list that may be out of date.
func (p *Plugin) reloadStillBlocks(s *stillStore, now time.Time) (map[string]stillBlock, []byte, error) {
	blocks, raw, err := p.readStillBlocks()
	if err != nil {
		// Only while there is no list at all, because that is what claims
		// and uploads are waiting on. See stillBlocksRetry.
		if !s.blocksLoaded() {
			s.blocksFailedAt = now
		}
		p.API.LogWarn("Could not read who is blocked from sharing preview pictures", "error", err.Error())

		return nil, nil, errStillBlocksUnread
	}

	s.blocksFailedAt = time.Time{}

	// A copy goes in force, because the list handed back is the caller's to
	// change and the one in force is never changed in place, only replaced.
	// Whatever a block learned this way takes down comes off the cards now
	// rather than at the next scheduled poll, as for a block made here.
	if s.enforceBlocks(maps.Clone(blocks), "") > 0 {
		p.refreshSoon()
	}

	return blocks, raw, nil
}

// readStillBlocks is the block list as stored, with the bytes it was read
// from. Nothing stored is an empty list and nil bytes: as the old value of an
// atomic write, nil means "only if nobody has created it since", which is what
// was found. A document that will not parse is an error, and is never written
// over with a list that has forgotten everybody in it.
func (p *Plugin) readStillBlocks() (map[string]stillBlock, []byte, error) {
	raw, appErr := p.API.KVGet(kvStillsBlocked)
	if appErr != nil {
		return nil, nil, appErr
	}

	blocks := map[string]stillBlock{}
	if len(raw) == 0 {
		return blocks, nil, nil
	}

	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, nil, err
	}

	// A stored null leaves the map nil, which would read as never loaded.
	if blocks == nil {
		blocks = map[string]stillBlock{}
	}

	return blocks, raw, nil
}

// changeStillBlocks makes one change to the block list as stored, and hands
// back the list as it then stands, for the caller to put in force, whether or
// not change changed anything. The caller holds blockLock.
//
// change is handed the list as stored at that moment and reports whether it
// changed it. It can be called more than once, each time with the list read
// afresh, so it must make its change from what it is handed alone. The write
// only lands if the stored list is still the one that was read. If somebody
// wrote it in between, which on a cluster is another node that this node's
// lock does not hold back, the change is made again on top of theirs rather
// than over it.
//
// A change that is not saved leaves the list in force as last read, which is
// the list as stored.
func (p *Plugin) changeStillBlocks(s *stillStore, now time.Time, change func(map[string]stillBlock) bool) (map[string]stillBlock, error) {
	for attempt := 0; attempt < stillBlocksAttempts; attempt++ {
		// A moment's pause, longer each time, before trying again after a
		// lost race: on a database with read replicas the list is read from a
		// replica, which can take a moment to catch up with the write that won,
		// and trying again at once would read the same stale list every time.
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * stillBlocksBackoff)
		}

		blocks, raw, err := p.reloadStillBlocks(s, now)
		if err != nil {
			return nil, err
		}

		if !change(blocks) {
			return blocks, nil
		}

		data, err := json.Marshal(blocks)
		if err != nil {
			return nil, errStillBlocksUnsaved
		}

		written, appErr := p.API.KVSetWithOptions(kvStillsBlocked, data, model.PluginKVSetOptions{Atomic: true, OldValue: raw})
		if appErr != nil {
			p.API.LogWarn("Could not save who is blocked from sharing preview pictures", "error", appErr.Error())

			return nil, errStillBlocksUnsaved
		}

		if written {
			return blocks, nil
		}
	}

	p.API.LogWarn("Could not save who is blocked from sharing preview pictures, because it kept changing while it was being saved",
		"attempts", stillBlocksAttempts)

	return nil, errStillBlocksUnsaved
}

// ensureStillBlocks makes sure the block list has been read.
func (p *Plugin) ensureStillBlocks() error {
	s := p.stills()
	if s.blocksLoaded() {
		return nil
	}

	s.blockLock.Lock()
	defer s.blockLock.Unlock()

	return p.loadStillBlocks(s, time.Now())
}

// currentStillBlocks is the block list as stored right now, for the admin
// list, which is asking about every block — on a cluster including those made
// through other nodes.
//
// Only read, never put in force here. On a database with read replicas the
// read can come from a replica that has not caught up yet, and putting what it
// says in force would undo a block — or an unblock — made on this node a
// moment ago. What is in force changes only through a change made here, which
// is checked against the stored list as it is written, or a restart.
func (p *Plugin) currentStillBlocks() (map[string]stillBlock, error) {
	blocks, _, err := p.readStillBlocks()
	if err != nil {
		p.API.LogWarn("Could not read who is blocked from sharing preview pictures", "error", err.Error())

		return nil, errStillBlocksUnread
	}

	return blocks, nil
}

// mayShareStills reports whether userID may claim or upload a still, and
// answers the request when they may not.
//
// A block list that cannot be read refuses everybody rather than letting
// everybody through. A card without a new picture for a minute costs nobody
// anything; a blocked person's picture back on every card is what the list is
// there to stop.
func (p *Plugin) mayShareStills(w http.ResponseWriter, userID string) bool {
	if err := p.ensureStillBlocks(); err != nil {
		writeError(w, http.StatusServiceUnavailable, "Preview pictures can't be shared right now. Try again in a moment.")
		return false
	}

	if p.stills().sharerBlocked(userID) {
		writeError(w, http.StatusForbidden, "You can't share preview pictures.")
		return false
	}

	return true
}

// blockStillSharer puts userID on the block list, stores it, and takes down
// whatever of theirs is on the cards. It reports how many stills came down.
// Blocking somebody already blocked keeps the block they have, and still
// clears the cards, which costs nothing.
func (p *Plugin) blockStillSharer(userID, adminID string, now time.Time) (int, error) {
	s := p.stills()

	// Held from the read until the list is in force: see reloadStillBlocks.
	// Two admins changing the list through this node at once take turns,
	// rather than one of them losing the race to the store for nothing.
	s.blockLock.Lock()
	defer s.blockLock.Unlock()

	blocks, err := p.changeStillBlocks(s, now, func(blocks map[string]stillBlock) bool {
		if _, already := blocks[userID]; already {
			return false
		}

		blocks[userID] = stillBlock{BlockedAt: now.UTC(), BlockedBy: adminID}

		return true
	})
	if err != nil {
		return 0, err
	}

	return s.enforceBlocks(blocks, userID), nil
}

// unblockStillSharer takes userID off the block list, and reports whether they
// were on it: on the list as stored, so on a cluster a block made through
// another node, which this one may not have heard of, is lifted all the same.
func (p *Plugin) unblockStillSharer(userID string, now time.Time) (bool, error) {
	s := p.stills()

	s.blockLock.Lock()
	defer s.blockLock.Unlock()

	unblocked := false
	blocks, err := p.changeStillBlocks(s, now, func(blocks map[string]stillBlock) bool {
		_, unblocked = blocks[userID]
		delete(blocks, userID)

		return unblocked
	})
	if err != nil {
		return false, err
	}

	s.enforceBlocks(blocks, "")

	return unblocked, nil
}

// sharerNames looks people up for the admin list, each of them once however
// many stills they took. Somebody who cannot be looked up, an account since
// deleted or a lookup that failed, comes back as two empty names rather than
// failing the whole list: the id is still there, and the picture still needs
// taking down.
func (p *Plugin) sharerNames() func(string) (string, string) {
	users := map[string]*model.User{}

	return func(userID string) (string, string) {
		user, seen := users[userID]
		if !seen {
			if userID != "" {
				if found, appErr := p.API.GetUser(userID); appErr == nil {
					user = found
				}
			}
			users[userID] = user
		}

		if user == nil {
			return "", ""
		}

		return user.Username, displayNameOf(user)
	}
}

// handleAdminStills answers /api/v1/admin/stills: GET lists every still being
// shown and who took it, with who is blocked from sharing; DELETE ?handle=…
// takes one still down.
func (p *Plugin) handleAdminStills(w http.ResponseWriter, r *http.Request, userID string) {
	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, stillsAdminOnly)
		return
	}

	switch r.Method {
	case http.MethodGet:
		p.listAdminStills(w)
	case http.MethodDelete:
		p.removeAdminStill(w, r, userID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
	}
}

func (p *Plugin) listAdminStills(w http.ResponseWriter) {
	// Read afresh rather than taken from the copy in force: see
	// currentStillBlocks.
	blocks, err := p.currentStillBlocks()
	if err != nil {
		writeError(w, http.StatusInternalServerError, stillsUnread)
		return
	}

	names := p.sharerNames()
	now := time.Now()

	answer := adminStillsResponse{
		Stills:  []adminStill{},
		Blocked: make([]blockedSharer, 0, len(blocks)),
	}

	// Exactly what GET /api/v1/stills would serve right now, and nothing it
	// would not: with the setting off that is nothing at all, whatever is
	// still in memory until the next poll clears it.
	if p.getConfiguration().LivePreviews {
		titles, live := map[string]string{}, map[string]bool{}
		for _, stream := range p.getSnapshot().Streams {
			titles[stream.Handle] = stream.Title
			live[stream.Handle] = !stream.AlwaysOn && stream.Source == ""
		}

		for _, shown := range p.stills().showingAll(live, now) {
			// A stream that has just left the listing keeps its picture
			// through the off-air grace, but the listing is where its title
			// came from. It is named the way the poller names a stream
			// nobody has named: never by its key.
			title := titles[shown.handle]
			if strings.TrimSpace(title) == "" {
				title = unnamedTitle(shown.handle)
			}

			username, displayName := names(shown.picture.by)
			answer.Stills = append(answer.Stills, adminStill{
				Handle:        shown.handle,
				Title:         title,
				Version:       shown.picture.version,
				At:            shown.picture.at.UTC().Format(time.RFC3339),
				ByUserID:      shown.picture.by,
				ByUsername:    username,
				ByDisplayName: displayName,
			})
		}
	}

	sort.Slice(answer.Stills, func(i, j int) bool {
		a, b := answer.Stills[i], answer.Stills[j]
		if folded := strings.ToLower(a.Title); folded != strings.ToLower(b.Title) {
			return folded < strings.ToLower(b.Title)
		}

		return a.Handle < b.Handle
	})

	for blockedID, block := range blocks {
		username, displayName := names(blockedID)
		answer.Blocked = append(answer.Blocked, blockedSharer{
			UserID:      blockedID,
			Username:    username,
			DisplayName: displayName,
			BlockedAt:   block.BlockedAt.UTC().Format(time.RFC3339),
		})
	}

	sort.Slice(answer.Blocked, func(i, j int) bool {
		a, b := answer.Blocked[i], answer.Blocked[j]
		if a.Username != b.Username {
			return a.Username < b.Username
		}

		return a.UserID < b.UserID
	})

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, answer)
}

func (p *Plugin) removeAdminStill(w http.ResponseWriter, r *http.Request, adminID string) {
	handle := strings.TrimSpace(r.URL.Query().Get("handle"))
	if handle == "" {
		writeError(w, http.StatusBadRequest, "Missing stream handle.")
		return
	}

	by, removed := p.stills().remove(handle)
	if !removed {
		writeError(w, http.StatusNotFound, "There is no preview picture for that stream.")
		return
	}

	// Who took which picture down, beside the line that says who put it up.
	// By handle, never the key; the handle can only be one the store knew.
	p.API.LogInfo("A system admin removed a preview still",
		"user_id", adminID, "stream_handle", handle, "uploader_user_id", by)

	// A panel that already has the picture keeps showing it until the
	// snapshot stops naming it, which the next poll does. Asked for now
	// rather than up to a whole interval away: the picture came down because
	// it should not be on anybody's screen. Only an admin can ask, so this is
	// no way to make the server poll the origin at will.
	p.refreshSoon()

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAdminStillBlock answers /api/v1/admin/stills/block: POST {"userId":…}
// blocks somebody from sharing preview stills, and DELETE ?userId=… lets them
// share again.
func (p *Plugin) handleAdminStillBlock(w http.ResponseWriter, r *http.Request, userID string) {
	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, stillsAdminOnly)
		return
	}

	switch r.Method {
	case http.MethodPost:
		p.handleBlockSharer(w, r, userID)
	case http.MethodDelete:
		p.handleUnblockSharer(w, r, userID)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
	}
}

func (p *Plugin) handleBlockSharer(w http.ResponseWriter, r *http.Request, adminID string) {
	// An empty body says no more than an empty id does, and gets the same
	// answer.
	var request blockRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBlockRequestBytes)).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "Could not read the request.")
		return
	}

	target := strings.TrimSpace(request.UserID)
	if target == "" {
		writeError(w, http.StatusBadRequest, "Say who to block.")
		return
	}

	// Only somebody who exists. Otherwise a mistyped id would sit on the
	// list for ever, listed with no name, blocking nobody.
	if user, appErr := p.API.GetUser(target); appErr != nil || user == nil {
		writeError(w, http.StatusNotFound, "There is no such user.")
		return
	}

	removed, err := p.blockStillSharer(target, adminID, time.Now())
	switch {
	case errors.Is(err, errStillBlocksUnsaved):
		writeError(w, http.StatusInternalServerError, "The block could not be saved, so nothing has changed. Try again in a moment.")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, stillsUnread)
		return
	}

	p.API.LogInfo("A system admin blocked somebody from sharing preview pictures",
		"user_id", adminID, "blocked_user_id", target, "stills_removed", removed)

	// As for a picture taken down: the cards stop showing theirs now, not at
	// the next scheduled poll.
	if removed > 0 {
		p.refreshSoon()
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (p *Plugin) handleUnblockSharer(w http.ResponseWriter, r *http.Request, adminID string) {
	target := strings.TrimSpace(r.URL.Query().Get("userId"))
	if target == "" {
		writeError(w, http.StatusBadRequest, "Say who to unblock.")
		return
	}

	unblocked, err := p.unblockStillSharer(target, time.Now())
	switch {
	case errors.Is(err, errStillBlocksUnsaved):
		writeError(w, http.StatusInternalServerError, "That could not be saved, so they are still blocked. Try again in a moment.")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, stillsUnread)
		return
	}

	if unblocked {
		p.API.LogInfo("A system admin let somebody share preview pictures again",
			"user_id", adminID, "unblocked_user_id", target)
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
