package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
)

// The clip routes. Two families, each sent here whole by ServeHTTP before its
// own switch, so that no clip route can be added anywhere a gate does not run:
//
//   - /api/v1/clips… is the panel's, and every route in it is behind
//     requireClipsAccess, for admins exactly as for everybody else: looking
//     (clipWatchRoutes) for people who are linked or let in, everything else
//     for people who are linked.
//   - /api/v1/admin/clips… is the System Console's, and needs a system admin,
//     linked or not. The one exception to those rules, and a narrow one:
//     whoever runs the server must be able to look after what is kept on it,
//     while an admin who has not unlocked the panel still cannot make, watch
//     or vote for clips there.
//
// In both, a path that is not a route is 404 and a method a route does not
// take is 405 before anything is checked: those answers say nothing that the
// webapp's own source does not.

const (
	clipsPath      = "/api/v1/clips"
	adminClipsPath = "/api/v1/admin/clips"

	// The most a clip request's body may be: a vote, a user id. A larger one
	// is not something the panel sends.
	maxClipRequestBytes = 1 << 10

	// How many shared clips one request for cards may ask about: a channel's
	// worth on screen at once, batched by the webapp.
	maxClipCards = 25
)

// clipHandler serves a clip route to a caller the gate has let through, with
// the stream key the gate read, so no handler reads the link a second time
// and gets a different answer.
type clipHandler func(p *Plugin, w http.ResponseWriter, r *http.Request, userID, key string)

// adminClipHandler serves a System Console route to a system admin.
type adminClipHandler func(p *Plugin, w http.ResponseWriter, r *http.Request, userID string)

// clipRoutes is every route under /api/v1/clips, by path and then method. HEAD
// is taken wherever a video is, because a video is served with Range support
// and a player may ask what it is getting before it asks for it.
var clipRoutes = map[string]map[string]clipHandler{
	clipsPath: {
		http.MethodGet: (*Plugin).handleClipList,
	},
	clipsPath + "/clip": {
		http.MethodGet:    (*Plugin).handleClipGet,
		http.MethodDelete: (*Plugin).handleClipDelete,
	},
	clipsPath + "/cards": {
		http.MethodGet: (*Plugin).handleClipCards,
	},
	clipsPath + "/video": {
		http.MethodGet:  (*Plugin).handleClipVideo,
		http.MethodHead: (*Plugin).handleClipVideo,
	},
	clipsPath + "/thumb": {
		http.MethodGet: (*Plugin).handleClipThumb,
	},
	clipsPath + "/vote": {
		http.MethodPost: (*Plugin).handleClipVote,
	},
	clipsPath + "/report": {
		http.MethodPost: (*Plugin).handleClipReport,
	},
	clipsPath + "/share": {
		http.MethodPost: (*Plugin).handleClipShare,
	},
	clipsPath + "/drafts": {
		http.MethodGet:  (*Plugin).handleDraftList,
		http.MethodPost: (*Plugin).handleDraftCreate,
	},
	clipsPath + "/draft": {
		http.MethodDelete: (*Plugin).handleDraftDelete,
	},
	clipsPath + "/draft/video": {
		http.MethodGet:  (*Plugin).handleDraftVideo,
		http.MethodHead: (*Plugin).handleDraftVideo,
	},
	clipsPath + "/draft/thumb": {
		http.MethodPut: (*Plugin).handleDraftThumb,
	},
	clipsPath + "/draft/publish": {
		http.MethodPost: (*Plugin).handleDraftPublish,
	},
	clipsPath + "/streamer": {
		http.MethodPut: (*Plugin).handleClipStreamer,
	},
	clipsPath + "/wipe": {
		http.MethodPost: (*Plugin).handleClipWipe,
	},

	// 3.2.1's downloads (clips_download.go). Both for linked people only,
	// since neither is in clipWatchRoutes: the ticket, by a POST that
	// carries Mattermost's CSRF check, and the GET that spends it and then
	// serves the file.
	clipsPath + "/download-ticket": {
		http.MethodPost: (*Plugin).handleClipDownloadTicket,
	},
	clipsPath + "/download": {
		http.MethodGet: (*Plugin).handleClipDownload,
	},
}

// clipLinkSeenRoutes are the routes whose gate takes a yes as good for ten
// seconds (accessSeenOf): a playback is dozens of range requests, and a
// channel can show dozens of shared clips. Every other route asks afresh.
var clipLinkSeenRoutes = map[string]bool{
	clipsPath + "/video":       true,
	clipsPath + "/thumb":       true,
	clipsPath + "/draft/video": true,
	clipsPath + "/cards":       true,
}

// adminClipRoutes is every route under /api/v1/admin/clips, by path and then
// method.
var adminClipRoutes = map[string]map[string]adminClipHandler{
	adminClipsPath: {
		http.MethodGet: (*Plugin).handleAdminClips,
	},
	adminClipsPath + "/video": {
		http.MethodGet:  (*Plugin).handleAdminClipVideo,
		http.MethodHead: (*Plugin).handleAdminClipVideo,
	},
	adminClipsPath + "/thumb": {
		http.MethodGet:    (*Plugin).handleAdminClipThumb,
		http.MethodDelete: (*Plugin).handleAdminClipThumbRemove,
	},
	adminClipsPath + "/clip": {
		http.MethodDelete: (*Plugin).handleAdminClipDelete,
	},
	adminClipsPath + "/block": {
		http.MethodPost:   (*Plugin).handleAdminClipBlock,
		http.MethodDelete: (*Plugin).handleAdminClipUnblock,
	},
	adminClipsPath + "/reset-score": {
		http.MethodPost: (*Plugin).handleAdminClipResetScore,
	},
	adminClipsPath + "/reports": {
		http.MethodDelete: (*Plugin).handleAdminClipReportsClear,
	},
	adminClipsPath + "/wipe": {
		http.MethodPost: (*Plugin).handleAdminClipWipe,
	},
}

// underPath reports whether path is root or something beneath it, and not
// merely a path that starts with the same letters.
func underPath(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

// writeClipError answers with a code the panel words for itself and the fixed
// text beside it. Never cached: an answer about a clip, or about whether the
// caller may see one, is only true now.
func (p *Plugin) writeClipError(w http.ResponseWriter, status int, code clipErrorCode) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, clipErrorResponse{Error: clipErrorText(code, p.brandName()), Code: code})
}

// writeClipTooMany is a clips meter's refusal: "rate_limited" and its
// sentence, like any other, and how long until that meter would let key
// through again (errorResponse says what the three fields are).
func writeClipTooMany(w http.ResponseWriter, limiter *rateLimiter, key string, now time.Time) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusTooManyRequests, clipErrorResponse{
		Error:        clipErrorTexts[clipErrRateLimited],
		Code:         clipErrRateLimited,
		RetryAfterMs: retryAfterMs(limiter.wait(key, now)),
	})
}

// writeClipRefusal answers an error from the clips store: a refusal as
// itself, anything else as the store's trouble. See clipErrorFor.
func (p *Plugin) writeClipRefusal(w http.ResponseWriter, err error) {
	status, code := clipErrorFor(err)
	p.writeClipError(w, status, code)
}

// refundClipToken gives back the token a request spent when what turned it
// away was the server's trouble rather than the caller's: the store not
// answering, or halted. Asking again, as they are told to, must not cost them.
func refundClipToken(limiter *rateLimiter, userID string, err error) {
	if status, _ := clipErrorFor(err); status == http.StatusServiceUnavailable {
		limiter.refund(userID)
	}
}

// tooManyClipAttempts is the first thing a share, a draft and a publish do,
// and answers 429 when somebody has made too many of the three, refused or
// not, this minute (see attemptLimiter). True when it has answered.
func (p *Plugin) tooManyClipAttempts(w http.ResponseWriter, userID string, now time.Time) bool {
	if p.clips().attemptLimiter.allowAt(userID, now) {
		return false
	}

	writeClipTooMany(w, p.clips().attemptLimiter, userID, now)

	return true
}

// readClipJSON reads a small JSON body into into. One over the bound, or one
// that is not JSON, is not something the panel sends.
func readClipJSON(r *http.Request, into any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxClipRequestBytes+1))
	if err != nil || len(body) > maxClipRequestBytes {
		return false
	}

	return json.Unmarshal(body, into) == nil
}

// serveClips runs every panel route in the same order: the route, the method,
// then the gate, and only then the handler, which does its own reading and
// metering. Nothing the request carries is looked at before the gate. A
// click's moment is the one exception, and it is not something the request
// carries: it is taken as the click arrives, before even the gate, because
// the draft is the minutes that end then.
func (p *Plugin) serveClips(w http.ResponseWriter, r *http.Request, userID string) {
	// First, so that every answer from here on — the refusals included — is
	// one no cache keeps. Routes that serve files set their own.
	w.Header().Set("Cache-Control", "no-store")

	methods, known := clipRoutes[r.URL.Path]
	if !known {
		writeError(w, http.StatusNotFound, "Unknown endpoint.")
		return
	}

	handler, allowed := methods[r.Method]
	if !allowed {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	if r.Method == http.MethodPost && r.URL.Path == clipsPath+"/drafts" {
		r = p.withClick(r)
	}

	key, allowed := p.requireClipsAccess(w, userID, clipWatchRoutes[r.URL.Path][r.Method], clipLinkSeenRoutes[r.URL.Path])
	if !allowed {
		return
	}

	handler(p, w, r, userID, key)
}

// serveAdminClips runs every System Console route: the route, the method, then
// one question of Mattermost — is this a system admin — before anything the
// request carries is read. No link is asked for.
func (p *Plugin) serveAdminClips(w http.ResponseWriter, r *http.Request, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	methods, known := adminClipRoutes[r.URL.Path]
	if !known {
		writeError(w, http.StatusNotFound, "Unknown endpoint.")
		return
	}

	handler, allowed := methods[r.Method]
	if !allowed {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	if !p.isSystemAdmin(userID) {
		p.writeClipError(w, http.StatusForbidden, clipErrForbidden)
		return
	}

	handler(p, w, r, userID)
}

// liveClip is the clip with this id while it is still kept, or nil: a clip
// past its time is gone to every reader at once, whenever the sweep gets to
// it.
func (doc *clipsDoc) liveClip(clipID string, kept retentionRecord, now int64) *clipEntry {
	entry := doc.clip(clipID)
	if entry == nil || now >= clipExpiresAt(entry.CreatedAt, kept) {
		return nil
	}

	return entry
}

// clipReading is what a read route needs to show clips: the document, how
// long clips are kept as of now, who counts now, and names.
type clipReading struct {
	doc    *clipsDoc
	now    int64
	kept   retentionRecord
	counts func(string) bool
	names  clipNamer
}

// readClips is the document as the read routes see it, with what goes with
// it. Who counts comes from the cache as it is: a read never looks up voters,
// so an upvote count may leave out, for a moment, somebody nobody has looked
// up yet. Names come from names: clipNamesNow for the list and a single clip,
// clipNames for the cards.
func (p *Plugin) readClips(now time.Time, names clipNamer) (*clipReading, error) {
	doc, err := p.clipsDocument(now)
	if err != nil {
		return nil, err
	}

	nowMS := now.UnixMilli()

	return &clipReading{
		doc:    doc,
		now:    nowMS,
		kept:   effectiveRetention(doc.Retention, p.getConfiguration().clipRetentionDays(), nowMS),
		counts: p.clips().verdictsFor(clipPeopleOf(doc), doc.Blocked).eligible,
		names:  names,
	}, nil
}

// clipViewerOf is who a panel route is answering. Whether they are an admin
// is asked of Mattermost once for the whole request; whether they are a bot
// comes from the account cache. Behind the gate an empty key is somebody let
// in, and only ever on a route for looking (requireClipsAccess), so that is
// what makes the view watch-only.
func (p *Plugin) clipViewerOf(userID, key string, now time.Time) clipViewer {
	p.lookUpClipPeople([]string{userID}, now, false)

	return clipViewer{
		UserID:    userID,
		Tag:       p.streamTag(key),
		Admin:     p.isSystemAdmin(userID),
		Bot:       p.clips().factsOf(userID).bot,
		WatchOnly: key == "",
		Downloads: p.getConfiguration().AllowClipDownloads,
	}
}

// viewOf is one clip as viewer sees it, with whether they may download it,
// which needs this reading's document for its streamers' switches.
func (reading *clipReading) viewOf(entry *clipEntry, viewer clipViewer) clipView {
	view := clipViewOf(entry, viewer, reading.kept, reading.counts, reading.names)
	view.CanDownload = !viewer.Console && clipDownloadRefusal(viewer.Downloads, !viewer.WatchOnly, entry, reading.doc) == ""

	return view
}

// clipViewsOf is every clip still kept, as viewer sees them, newest first.
func (reading *clipReading) clipViewsOf(viewer clipViewer) []clipView {
	views := []clipView{}

	for i := range reading.doc.Clips {
		entry := &reading.doc.Clips[i]
		if reading.now >= clipExpiresAt(entry.CreatedAt, reading.kept) {
			continue
		}

		views = append(views, reading.viewOf(entry, viewer))
	}

	sort.SliceStable(views, func(i, j int) bool {
		if views[i].CreatedAt != views[j].CreatedAt {
			return views[i].CreatedAt > views[j].CreatedAt
		}

		return views[i].ID < views[j].ID
	})

	return views
}

type clipListResponse struct {
	Clips []clipView `json:"clips"`
}

type clipOKResponse struct {
	OK bool `json:"ok"`
}

// GET /api/v1/clips: every clip still kept, newest first, as the caller sees
// them.
func (p *Plugin) handleClipList(w http.ResponseWriter, _ *http.Request, userID, key string) {
	now := p.clips().now()

	reading, err := p.readClips(now, p.clipNamesNow(now))
	if err != nil {
		p.writeClipRefusal(w, err)
		return
	}

	writeJSON(w, http.StatusOK, clipListResponse{Clips: reading.clipViewsOf(p.clipViewerOf(userID, key, now))})
}

// GET /api/v1/clips/clip?id=: one clip, or 410 once it has gone.
func (p *Plugin) handleClipGet(w http.ResponseWriter, r *http.Request, userID, key string) {
	clipID := r.URL.Query().Get("id")
	if !validClipID(clipID) {
		p.writeClipError(w, http.StatusBadRequest, clipErrBadRequest)
		return
	}

	now := p.clips().now()
	reading, err := p.readClips(now, p.clipNamesNow(now))
	if err != nil {
		p.writeClipRefusal(w, err)
		return
	}

	entry := reading.doc.liveClip(clipID, reading.kept, reading.now)
	if entry == nil {
		p.writeClipError(w, http.StatusGone, clipErrClipGone)
		return
	}

	writeJSON(w, http.StatusOK, reading.viewOf(entry, p.clipViewerOf(userID, key, now)))
}

// clipCardIDs reads the ids a request for cards asks about: one to
// maxClipCards distinct clip ids, comma separated, and nothing else.
func clipCardIDs(value string) ([]string, bool) {
	if value == "" {
		return nil, false
	}

	ids := strings.Split(value, ",")
	if len(ids) > maxClipCards {
		return nil, false
	}

	seen := map[string]bool{}
	for _, id := range ids {
		if !validClipID(id) || seen[id] {
			return nil, false
		}
		seen[id] = true
	}

	return ids, true
}

// GET /api/v1/clips/cards?ids=: what each shared clip's card shows, or null
// for one that has gone, with its thumbs up (design/3.2.0.md §9.2): the count
// the same for every reader, and whether this reader voted and may vote. Per
// reader, so never cached; and still nothing asked of Mattermost about the
// reader but what the account cache already holds: whether they are an admin
// says nothing about voting, so it is not asked. From this node's copy of
// the document, never a read per clip.
func (p *Plugin) handleClipCards(w http.ResponseWriter, r *http.Request, userID, key string) {
	ids, ok := clipCardIDs(r.URL.Query().Get("ids"))
	if !ok {
		p.writeClipError(w, http.StatusBadRequest, clipErrBadRequest)
		return
	}

	// Names from the ten-minute cache, not asked for afresh: a channel can
	// mount dozens of cards at once (A§3.3).
	now := p.clips().now()
	reading, err := p.readClips(now, p.clipNames(now, true))
	if err != nil {
		p.writeClipRefusal(w, err)
		return
	}

	// Who is reading: a bot from the account cache, somebody let in by the
	// key the gate found (none), as clipViewerOf decides it, but without
	// asking Mattermost whether they are an admin.
	p.lookUpClipPeople([]string{userID}, now, false)
	viewer := clipViewer{UserID: userID, Bot: p.clips().factsOf(userID).bot, WatchOnly: key == ""}

	cards := make(map[string]*clipCardView, len(ids))
	for _, id := range ids {
		entry := reading.doc.liveClip(id, reading.kept, reading.now)
		if entry == nil {
			cards[id] = nil
			continue
		}

		card := clipCardOf(entry, reading.names)
		card.ExpiresAt = clipExpiresAt(entry.CreatedAt, reading.kept)
		card.Upvotes = displayedUpvotes(entry, reading.counts)
		_, card.Voted = entry.Voters[userID]
		card.CanVote = !viewer.WatchOnly && !viewer.Bot && entry.ClipperID != userID
		cards[id] = &card
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, clipCardsResponse{Cards: cards})
}

// GET|HEAD /api/v1/clips/video?id=: a clip's video, with ranges.
func (p *Plugin) handleClipVideo(w http.ResponseWriter, r *http.Request, _, _ string) {
	p.serveClipVideo(w, r)
}

// GET /api/v1/clips/thumb?id=&v=: a clip's picture.
func (p *Plugin) handleClipThumb(w http.ResponseWriter, r *http.Request, _, _ string) {
	p.serveClipThumb(w, r)
}

// serveClipVideo serves a clip still kept, for the panel and the console
// alike: 410 once it has gone, whatever is still on the disk, and 404 for a
// clip whose file is missing.
func (p *Plugin) serveClipVideo(w http.ResponseWriter, r *http.Request) {
	clipID := r.URL.Query().Get("id")
	if !validClipID(clipID) {
		p.writeClipError(w, http.StatusBadRequest, clipErrBadRequest)
		return
	}

	s := p.clips()
	now := s.now()

	doc, err := p.clipsDocument(now)
	if err != nil {
		p.writeClipRefusal(w, err)
		return
	}

	nowMS := now.UnixMilli()
	kept := effectiveRetention(doc.Retention, p.getConfiguration().clipRetentionDays(), nowMS)
	if doc.liveClip(clipID, kept, nowMS) == nil {
		p.writeClipError(w, http.StatusGone, clipErrClipGone)
		return
	}

	path := s.clipFile(clipID, clipVideoExt)
	if path == "" {
		p.writeClipError(w, http.StatusServiceUnavailable, clipErrStorageUnavailable)
		return
	}

	serveClipFile(w, r, path, "clip-"+clipID+clipVideoExt)
}

// serveClipThumb serves a clip's picture, for the panel and the console
// alike. Always revalidated, whatever version was asked for: whether the
// caller may see it is decided on every request, and a revalidation that
// matches is a cheap 304, after the gate.
func (p *Plugin) serveClipThumb(w http.ResponseWriter, r *http.Request) {
	clipID := r.URL.Query().Get("id")
	if !validClipID(clipID) {
		p.writeClipError(w, http.StatusBadRequest, clipErrBadRequest)
		return
	}

	s := p.clips()
	now := s.now()

	doc, err := p.clipsDocument(now)
	if err != nil {
		p.writeClipRefusal(w, err)
		return
	}

	nowMS := now.UnixMilli()
	kept := effectiveRetention(doc.Retention, p.getConfiguration().clipRetentionDays(), nowMS)
	entry := doc.liveClip(clipID, kept, nowMS)
	if entry == nil || entry.Thumb == "" {
		writeError(w, http.StatusNotFound, "Not found.")
		return
	}

	path := s.clipFile(clipID, clipThumbExt)
	if path == "" {
		writeError(w, http.StatusNotFound, "Not found.")
		return
	}

	data, err := readClipThumb(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "Not found.")
		return
	}

	writeImage(w, r, "image/jpeg", data, "private, no-cache")
}

type clipVoteRequest struct {
	Up *bool `json:"up"`
}

type clipVoteResponse struct {
	Upvotes int  `json:"upvotes"`
	Voted   bool `json:"voted"`
}

// POST /api/v1/clips/vote?id= {"up":bool}: the vote the caller wants on a
// clip, never a toggle; see voteClip.
func (p *Plugin) handleClipVote(w http.ResponseWriter, r *http.Request, userID, _ string) {
	clipID := r.URL.Query().Get("id")

	var request clipVoteRequest
	if !validClipID(clipID) || !readClipJSON(r, &request) || request.Up == nil {
		p.writeClipError(w, http.StatusBadRequest, clipErrBadRequest)
		return
	}

	s := p.clips()
	now := s.now()
	if !s.voteLimiter.allowAt(userID, now) {
		writeClipTooMany(w, s.voteLimiter, userID, now)
		return
	}

	upvotes, voted, err := p.voteClip(clipID, userID, *request.Up, now)
	if err != nil {
		refundClipToken(s.voteLimiter, userID, err)
		p.writeClipRefusal(w, err)

		return
	}

	writeJSON(w, http.StatusOK, clipVoteResponse{Upvotes: upvotes, Voted: voted})
}

// report marks a clip as reported by somebody, once: reporting it again
// changes nothing. Not by its own clipper, who can delete it instead.
func (doc *clipsDoc) report(clipID, userID string, kept retentionRecord, now int64) (bool, error) {
	entry := doc.liveClip(clipID, kept, now)
	if entry == nil {
		return false, refuseClip(http.StatusGone, clipErrClipGone)
	}
	if entry.ClipperID == userID {
		return false, refuseClip(http.StatusForbidden, clipErrForbidden)
	}
	if _, already := entry.Reports[userID]; already {
		return false, nil
	}

	if entry.Reports == nil {
		entry.Reports = map[string]int64{}
	}
	entry.Reports[userID] = now

	return true, nil
}

// reportClip reports a clip for an admin to look at. Bots are refused, as for
// votes, and so is somebody whose account cannot be read.
func (p *Plugin) reportClip(clipID, userID string, now time.Time) error {
	s := p.clips()

	p.lookUpClipPeople([]string{userID}, now, false)
	facts := s.factsOf(userID)
	if !facts.userKnown {
		return errStore
	}
	if facts.bot || facts.gone {
		return refuseClip(http.StatusForbidden, clipErrForbidden)
	}

	reported := false
	_, err := p.clipsCAS(now, func(doc *clipsDoc, write *clipsWrite) (bool, error) {
		changed, err := doc.report(clipID, userID, write.kept, write.now)
		reported = changed

		return changed, err
	})
	if err != nil {
		return err
	}

	if reported {
		p.API.LogInfo("A clip was reported", "user_id", userID, "clip_id", clipID)
	}

	return nil
}

// POST /api/v1/clips/report?id=: report a clip to the admins.
func (p *Plugin) handleClipReport(w http.ResponseWriter, r *http.Request, userID, _ string) {
	clipID := r.URL.Query().Get("id")
	if !validClipID(clipID) {
		p.writeClipError(w, http.StatusBadRequest, clipErrBadRequest)
		return
	}

	s := p.clips()
	now := s.now()
	if !s.writeLimiter.allowAt(userID, now) {
		writeClipTooMany(w, s.writeLimiter, userID, now)
		return
	}

	if err := p.reportClip(clipID, userID, now); err != nil {
		refundClipToken(s.writeLimiter, userID, err)
		p.writeClipRefusal(w, err)

		return
	}

	writeJSON(w, http.StatusOK, clipOKResponse{OK: true})
}

// removeClip settles one clip for the reason given, deletes its files, and
// says so in the log. A clip already gone is as good as removed. One left in
// place because somebody who voted for it cannot be looked up just now, and
// banking would lose their vote for good, is the store's trouble: the caller
// tries again.
func (p *Plugin) removeClip(clipID, userID, reason string, now time.Time) error {
	outcome, err := p.settleClips(map[string]string{clipID: reason}, now)
	if err != nil {
		return err
	}

	for _, deferred := range outcome.Deferred {
		if deferred == clipID {
			return errStore
		}
	}

	p.clipsSettled(outcome.Settled)
	for _, gone := range outcome.Settled {
		p.API.LogInfo("A clip was deleted", "user_id", userID, "clip_id", gone.ID, "reason", gone.Reason, "upvotes_banked", gone.Banked)
	}

	return nil
}

// DELETE /api/v1/clips/clip?id=: take a clip down, in exactly this order.
//
//  1. Past the gate, an id that is not one is 400, and only then is a token
//     spent.
//  2. A clip that is not there, or is past its time, is already gone: 200,
//     and one past its time is left for the sweep, which banks its upvotes.
//  3. Its clipper takes it down, and its upvotes go with it.
//  4. An admin who is not its clipper takes it down the same way.
//  5. The streamer of its stream — whoever has linked that stream now —
//     takes it down and its clipper keeps the upvotes.
//  6. Nobody else.
//
// The document is read afresh rather than from this node's copy, which could
// miss a clip published on another node a moment ago and answer that it had
// gone.
func (p *Plugin) handleClipDelete(w http.ResponseWriter, r *http.Request, userID, key string) {
	clipID := r.URL.Query().Get("id")
	if !validClipID(clipID) {
		p.writeClipError(w, http.StatusBadRequest, clipErrBadRequest)
		return
	}

	s := p.clips()
	now := s.now()
	if !s.writeLimiter.allowAt(userID, now) {
		writeClipTooMany(w, s.writeLimiter, userID, now)
		return
	}

	doc, err := p.readClipsFresh(now)
	if err != nil {
		refundClipToken(s.writeLimiter, userID, err)
		p.writeClipRefusal(w, err)

		return
	}

	nowMS := now.UnixMilli()
	kept := effectiveRetention(doc.Retention, p.getConfiguration().clipRetentionDays(), nowMS)
	entry := doc.liveClip(clipID, kept, nowMS)
	if entry == nil {
		writeJSON(w, http.StatusOK, clipOKResponse{OK: true})
		return
	}

	var reason string
	switch {
	case entry.ClipperID == userID:
		reason = clipGoneClipper
	case p.isSystemAdmin(userID):
		reason = clipGoneAdmin
	case p.streamOf(key, entry.StreamTag):
		reason = clipGoneStreamer
	default:
		p.writeClipError(w, http.StatusForbidden, clipErrNotAllowed)
		return
	}

	if err := p.removeClip(clipID, userID, reason, now); err != nil {
		refundClipToken(s.writeLimiter, userID, err)
		p.writeClipRefusal(w, err)

		return
	}

	writeJSON(w, http.StatusOK, clipOKResponse{OK: true})
}

// GET /api/v1/admin/clips: everything an admin needs to look after clips.
func (p *Plugin) handleAdminClips(w http.ResponseWriter, _ *http.Request, adminID string) {
	view, err := p.adminClipsView(adminID, p.clips().now())
	if err != nil {
		p.writeClipRefusal(w, err)
		return
	}

	writeJSON(w, http.StatusOK, view)
}

// clampToInt64 is a disk size as JSON carries it.
func clampToInt64(value uint64) int64 {
	if value > math.MaxInt64 {
		return math.MaxInt64
	}

	return int64(value)
}

// clipsKeptPending is how many clips are being kept only because the setting
// was lowered less than a day ago, and when that day is up. Nothing while no
// lowering is waiting.
func clipsKeptPending(doc *clipsDoc, kept retentionRecord, now int64) (int, int64) {
	if kept.ChangedAt == 0 || kept.PrevDays <= kept.Days {
		return 0, 0
	}

	end := kept.ChangedAt + retentionGraceMs
	if now >= end {
		return 0, 0
	}

	pending := 0
	for i := range doc.Clips {
		entry := &doc.Clips[i]
		expires := clipExpiresAt(entry.CreatedAt, kept)
		if now < expires && entry.CreatedAt+int64(kept.Days)*dayMs < expires {
			pending++
		}
	}
	if pending == 0 {
		return 0, 0
	}

	return pending, end
}

// adminClipsView is the System Console's view of clips on this server. A
// halted document is shown as that, with whatever this node last held,
// because that is when an admin most needs to see it.
func (p *Plugin) adminClipsView(adminID string, now time.Time) (*adminClipsResponse, error) {
	s := p.clips()
	cfg := p.getConfiguration()

	doc, err := p.clipsDocument(now)
	switch {
	case errors.Is(err, errClipsHalted):
		doc = &clipsDoc{V: clipsDocVersion}
	case err != nil:
		return nil, err
	}

	root, problem := s.storage()
	_, halted := s.halt.state()
	used, _ := s.usage("")
	limit := cfg.clipStorageLimitBytes()

	recorders := p.recorders()
	view := &adminClipsResponse{
		Problem:    problem,
		Halted:     halted,
		Root:       root,
		UsedBytes:  used,
		LimitBytes: limit,
		ShareBytes: clipShareBytes(limit),
		RingBytes:  recorders.ringBytes(),
		Recorders:  recorders.adminRows(now),
		Tails:      recorders.tailViews(now, cfg.ringWindow()),
		Drafts:     s.drafts.counted(),
		Clips:      []clipView{},
		People:     []adminClipperView{},
		Blocked:    []adminBlockedView{},
	}
	if root != "" {
		if free, total, ok := s.freeSpace(root); ok {
			view.FreeBytes, view.TotalBytes = clampToInt64(free), clampToInt64(total)
		}
	}

	nowMS := now.UnixMilli()
	kept := effectiveRetention(doc.Retention, cfg.clipRetentionDays(), nowMS)
	counts := s.verdictsFor(clipPeopleOf(doc), doc.Blocked).eligible
	names := p.clipNamesNow(now)
	reading := &clipReading{doc: doc, now: nowMS, kept: kept, counts: counts, names: names}

	// Reported clips first, the most reported first; then the newest.
	view.Clips = reading.clipViewsOf(clipViewer{UserID: adminID, Admin: true, Console: true})
	sort.SliceStable(view.Clips, func(i, j int) bool {
		return *view.Clips[i].Reports > *view.Clips[j].Reports
	})

	// Everybody with a score: clips still kept, or upvotes banked.
	live := map[string]int{}
	for i := range doc.Clips {
		if nowMS < clipExpiresAt(doc.Clips[i].CreatedAt, kept) {
			live[doc.Clips[i].ClipperID]++
		}
	}
	for userID, score := range clipperScores(doc, counts) {
		if live[userID] == 0 && doc.Banked[userID].Up == 0 && doc.Banked[userID].Clips == 0 {
			continue
		}

		person := names(userID)
		_, bytes := s.usage(userID)
		view.People = append(view.People, adminClipperView{
			UserID:      userID,
			Username:    person.Username,
			DisplayName: person.DisplayName,
			Clips:       live[userID],
			Bytes:       bytes,
			Upvotes:     score.up,
			Banked:      doc.Banked[userID].Up,
		})
	}
	sort.Slice(view.People, func(i, j int) bool {
		if view.People[i].Upvotes != view.People[j].Upvotes {
			return view.People[i].Upvotes > view.People[j].Upvotes
		}
		if view.People[i].Clips != view.People[j].Clips {
			return view.People[i].Clips > view.People[j].Clips
		}

		return view.People[i].UserID < view.People[j].UserID
	})

	for userID, mark := range doc.Blocked {
		person := names(userID)
		view.Blocked = append(view.Blocked, adminBlockedView{
			UserID:      userID,
			Username:    person.Username,
			DisplayName: person.DisplayName,
			BlockedAt:   mark.At,
			By:          mark.By,
		})
	}
	sort.Slice(view.Blocked, func(i, j int) bool {
		if view.Blocked[i].BlockedAt != view.Blocked[j].BlockedAt {
			return view.Blocked[i].BlockedAt > view.Blocked[j].BlockedAt
		}

		return view.Blocked[i].UserID < view.Blocked[j].UserID
	})

	pending, removalAt := clipsKeptPending(doc, kept, nowMS)
	view.Retention = adminRetentionView{Days: kept.Days, PendingRemovals: pending, RemovalAt: removalAt}

	// Shown here whether or not Clips is switched on: an admin looks after
	// the award either way.
	view.BestClipper = bestClipperViewOf(doc, true, kept, nowMS, counts, names)

	return view, nil
}

// GET|HEAD /api/v1/admin/clips/video?id=: a clip's video, for an admin to
// watch in the console.
func (p *Plugin) handleAdminClipVideo(w http.ResponseWriter, r *http.Request, _ string) {
	p.serveClipVideo(w, r)
}

// GET /api/v1/admin/clips/thumb?id=&v=: a clip's picture, for the console.
func (p *Plugin) handleAdminClipThumb(w http.ResponseWriter, r *http.Request, _ string) {
	p.serveClipThumb(w, r)
}

// adminClipWrite is the start every console write shares, once the caller is
// known to be an admin: the request's own checks, then a token. A false has
// answered already.
func (p *Plugin) adminClipWrite(w http.ResponseWriter, adminID string, valid bool, now time.Time) bool {
	if !valid {
		p.writeClipError(w, http.StatusBadRequest, clipErrBadRequest)
		return false
	}

	if !p.clips().writeLimiter.allowAt(adminID, now) {
		writeClipTooMany(w, p.clips().writeLimiter, adminID, now)
		return false
	}

	return true
}

// DELETE /api/v1/admin/clips/clip?id=: take a clip down from the console. Its
// upvotes go with it, unless it was already past its time.
func (p *Plugin) handleAdminClipDelete(w http.ResponseWriter, r *http.Request, adminID string) {
	clipID := r.URL.Query().Get("id")
	s := p.clips()
	now := s.now()
	if !p.adminClipWrite(w, adminID, validClipID(clipID), now) {
		return
	}

	if err := p.removeClip(clipID, adminID, clipGoneAdmin, now); err != nil {
		refundClipToken(s.writeLimiter, adminID, err)
		p.writeClipRefusal(w, err)

		return
	}

	writeJSON(w, http.StatusOK, clipOKResponse{OK: true})
}

// DELETE /api/v1/admin/clips/thumb?id=: take a clip's picture down. The
// clip's card falls back to its streamer's picture. The document first, then
// the file, so nothing ever points at a picture that is not there.
func (p *Plugin) handleAdminClipThumbRemove(w http.ResponseWriter, r *http.Request, adminID string) {
	clipID := r.URL.Query().Get("id")
	s := p.clips()
	now := s.now()
	if !p.adminClipWrite(w, adminID, validClipID(clipID), now) {
		return
	}

	removed, clipperID := false, ""
	_, err := p.clipsCAS(now, func(doc *clipsDoc, write *clipsWrite) (bool, error) {
		removed, clipperID = false, ""

		entry := doc.liveClip(clipID, write.kept, write.now)
		if entry == nil {
			return false, refuseClip(http.StatusGone, clipErrClipGone)
		}
		clipperID = entry.ClipperID
		if entry.Thumb == "" {
			return false, nil
		}

		entry.Thumb = ""
		removed = true

		return true, nil
	})
	if err != nil {
		refundClipToken(s.writeLimiter, adminID, err)
		p.writeClipRefusal(w, err)

		return
	}

	s.removeClipThumb(clipID, clipperID)

	if removed {
		p.API.LogInfo("A clip's picture was removed", "user_id", adminID, "clip_id", clipID)
	}

	writeJSON(w, http.StatusOK, clipOKResponse{OK: true})
}

type clipUserRequest struct {
	UserID string `json:"userId"`
}

// clipUserTarget is the person a console write is about, from the body: an id
// of a sensible length, or nothing.
func clipUserTarget(r *http.Request) string {
	var request clipUserRequest
	if !readClipJSON(r, &request) {
		return ""
	}

	return cleanClipUserID(request.UserID)
}

func cleanClipUserID(value string) string {
	userID := strings.TrimSpace(value)
	if len(userID) > 64 {
		return ""
	}

	return userID
}

// clipUserExists is whether somebody has an account, so that a mistyped id
// never sits in the document for ever, blocking or resetting nobody. A 404 is
// a request that made no sense; anything else, the user store not answering.
func (p *Plugin) clipUserExists(userID string) error {
	user, appErr := p.API.GetUser(userID)
	switch {
	case appErr != nil && appErr.StatusCode == http.StatusNotFound:
		return refuseClip(http.StatusBadRequest, clipErrBadRequest)
	case appErr != nil:
		return errStore
	case user == nil:
		return refuseClip(http.StatusBadRequest, clipErrBadRequest)
	}

	return nil
}

type clipBlockResponse struct {
	OK            bool `json:"ok"`
	DraftsRemoved int  `json:"draftsRemoved"`
}

// POST /api/v1/admin/clips/block {"userId"}: stop somebody making clips. They
// stop counting for Best Clipper in the same write, and their votes stop
// counting toward anybody's. Their drafts on this node are thrown away, those
// still being cut included, which their click is told; one already being
// published is left to finish, and thrown away only if that fails. Their
// published clips stay, for an admin to take down one by one. draftsRemoved
// counts the ready drafts and the ones being cut.
func (p *Plugin) handleAdminClipBlock(w http.ResponseWriter, r *http.Request, adminID string) {
	target := clipUserTarget(r)
	s := p.clips()
	now := s.now()
	if !p.adminClipWrite(w, adminID, target != "", now) {
		return
	}

	if err := p.clipUserExists(target); err != nil {
		refundClipToken(s.writeLimiter, adminID, err)
		p.writeClipRefusal(w, err)

		return
	}

	blocked := false
	_, err := p.clipsCAS(now, func(doc *clipsDoc, write *clipsWrite) (bool, error) {
		blocked = doc.block(target, clipMark{By: adminID, At: write.now})

		return blocked, nil
	})
	if err != nil {
		refundClipToken(s.writeLimiter, adminID, err)
		p.writeClipRefusal(w, err)

		return
	}

	removed := p.discardDrafts(func(draft *clipDraft) bool {
		return draft.ownerID == target
	}, draftBlocked, now)

	if blocked {
		p.API.LogInfo("An admin stopped somebody making clips", "admin_id", adminID, "user_id", target)
	}

	writeJSON(w, http.StatusOK, clipBlockResponse{OK: true, DraftsRemoved: removed})
}

// DELETE /api/v1/admin/clips/block?userId=: let somebody make clips again.
func (p *Plugin) handleAdminClipUnblock(w http.ResponseWriter, r *http.Request, adminID string) {
	target := cleanClipUserID(r.URL.Query().Get("userId"))
	s := p.clips()
	now := s.now()
	if !p.adminClipWrite(w, adminID, target != "", now) {
		return
	}

	unblocked := false
	_, err := p.clipsCAS(now, func(doc *clipsDoc, _ *clipsWrite) (bool, error) {
		unblocked = doc.unblock(target)

		return unblocked, nil
	})
	if err != nil {
		refundClipToken(s.writeLimiter, adminID, err)
		p.writeClipRefusal(w, err)

		return
	}

	if unblocked {
		p.API.LogInfo("An admin let somebody make clips again", "admin_id", adminID, "user_id", target)
	}

	writeJSON(w, http.StatusOK, clipOKResponse{OK: true})
}

// POST /api/v1/admin/clips/reset-score {"userId"}: start somebody's Best
// Clipper score again from nothing.
func (p *Plugin) handleAdminClipResetScore(w http.ResponseWriter, r *http.Request, adminID string) {
	target := clipUserTarget(r)
	s := p.clips()
	now := s.now()
	if !p.adminClipWrite(w, adminID, target != "", now) {
		return
	}

	if err := p.clipUserExists(target); err != nil {
		refundClipToken(s.writeLimiter, adminID, err)
		p.writeClipRefusal(w, err)

		return
	}

	_, err := p.clipsCAS(now, func(doc *clipsDoc, write *clipsWrite) (bool, error) {
		return doc.resetScore(target, clipMark{By: adminID, At: write.now}), nil
	}, target)
	if err != nil {
		refundClipToken(s.writeLimiter, adminID, err)
		p.writeClipRefusal(w, err)

		return
	}

	p.API.LogInfo("An admin reset somebody's Best Clipper score", "admin_id", adminID, "user_id", target)

	writeJSON(w, http.StatusOK, clipOKResponse{OK: true})
}

// DELETE /api/v1/admin/clips/reports?id=: an admin has looked at a reported
// clip and is keeping it.
func (p *Plugin) handleAdminClipReportsClear(w http.ResponseWriter, r *http.Request, adminID string) {
	clipID := r.URL.Query().Get("id")
	s := p.clips()
	now := s.now()
	if !p.adminClipWrite(w, adminID, validClipID(clipID), now) {
		return
	}

	cleared := false
	_, err := p.clipsCAS(now, func(doc *clipsDoc, write *clipsWrite) (bool, error) {
		cleared = false

		entry := doc.liveClip(clipID, write.kept, write.now)
		if entry == nil {
			return false, refuseClip(http.StatusGone, clipErrClipGone)
		}
		if len(entry.Reports) == 0 {
			return false, nil
		}

		entry.Reports = nil
		cleared = true

		return true, nil
	})
	if err != nil {
		refundClipToken(s.writeLimiter, adminID, err)
		p.writeClipRefusal(w, err)

		return
	}

	if cleared {
		p.API.LogInfo("An admin cleared the reports on a clip", "admin_id", adminID, "clip_id", clipID)
	}

	writeJSON(w, http.StatusOK, clipOKResponse{OK: true})
}

// clipsStreamerView is what /me tells somebody who has linked a stream about
// clipping it: whether it is kept for clipping, and, only while an admin
// allows downloads (3.2.1), whether people may download clips of it. Absent,
// downloads is what hides the streamer's second switch.
type clipsStreamerView struct {
	Allowed   bool  `json:"allowed"`
	Downloads *bool `json:"downloads,omitempty"`
}

// clipsStreamerFor is clipsStreamerView for the stream behind key, or nil when
// the document cannot be read: the panel then shows no switch, rather than a
// switch in a position nobody knows.
func (p *Plugin) clipsStreamerFor(key string) *clipsStreamerView {
	doc, err := p.clipsDocument(p.clips().now())
	if err != nil {
		return nil
	}

	tag := p.streamTag(key)
	_, off := doc.OptOut[tag]
	view := &clipsStreamerView{Allowed: tag == "" || !off}

	if p.getConfiguration().AllowClipDownloads {
		_, blocked := doc.NoDownloads[tag]
		downloads := tag == "" || !blocked
		view.Downloads = &downloads
	}

	return view
}

// clipsHaltedAlert is what an admin is told when the document will not
// decode, and what to do about it: nothing clears the halt but a restart.
const clipsHaltedAlert = "The stored clips could not be read, so nothing about them is being changed. Fix or delete the clips key, then disable and enable the plugin"

// clipsAlertFor is /me's word to an admin about clips, or "" when there is
// nothing to say: why clips cannot be kept, a halted document, recorders that
// have not reached Broadcast Box for five minutes, clips people have
// reported, and clips about to go because the setting was lowered. Counts and
// states only, never a clip.
func (p *Plugin) clipsAlertFor(cfg *configuration) string {
	s := p.clips()
	now := s.now()
	parts := []string{}

	if _, problem := s.storage(); problem != "" {
		parts = append(parts, strings.TrimSuffix(problem, "."))
	}
	if halted, _ := s.halt.state(); halted {
		parts = append(parts, clipsHaltedAlert)
	}
	if alert := p.recorders().healthAlert(now); alert != "" {
		parts = append(parts, alert)
	}

	if doc, err := p.clipsDocument(now); err == nil {
		nowMS := now.UnixMilli()
		kept := effectiveRetention(doc.Retention, cfg.clipRetentionDays(), nowMS)

		reported := 0
		for i := range doc.Clips {
			if len(doc.Clips[i].Reports) > 0 && doc.liveClip(doc.Clips[i].ID, kept, nowMS) != nil {
				reported++
			}
		}
		switch {
		case reported == 1:
			parts = append(parts, "1 clip has been reported")
		case reported > 1:
			parts = append(parts, fmt.Sprintf("%d clips have been reported", reported))
		}

		if pending, removalAt := clipsKeptPending(doc, kept, nowMS); pending > 0 {
			when := time.UnixMilli(removalAt).UTC().Format("2 Jan 2006 15:04 UTC")
			if pending == 1 {
				parts = append(parts, "Keep clips for was lowered: 1 clip will be removed by "+when)
			} else {
				parts = append(parts, fmt.Sprintf("Keep clips for was lowered: %d clips will be removed by %s", pending, when))
			}
		}
	}

	return strings.Join(parts, " · ")
}

// clipStreamerRequest is one of the streamer's two switches: allow, whether
// their stream is kept for clipping, or downloads (3.2.1), whether people may
// download clips of it. Exactly one of the two.
type clipStreamerRequest struct {
	Allow     *bool `json:"allow"`
	Downloads *bool `json:"downloads"`
}

type clipStreamerResponse struct {
	Allowed bool `json:"allowed"`
}

type clipDownloadsResponse struct {
	Downloads bool `json:"downloads"`
}

type clipWipeResponse struct {
	OK            bool `json:"ok"`
	DraftsRemoved int  `json:"draftsRemoved"`
}

// PUT /api/v1/clips/streamer {"allow":bool}: a streamer switching clipping of
// their stream off, or back on (spec §2.10.6, A§3.3, A§3.5). It acts on the
// stream the caller has linked, by its tag, and on no other: the switch is
// kept by stream, not by person, so unlinking does not quietly switch it
// back on, relinking to another stream does not carry it over, and whoever
// proves the key next finds it as it was left.
//
// Off takes effect at once, without waiting for a poll: the stream's
// recorder stops, what is held of it goes, and every draft cut from it, by
// anybody, goes as wiped — one being published cancelled, unless its clip is
// already written. On deletes the switch, and the next poll records the
// stream again.
//
// {"downloads":bool} is the other switch, on the same stream and the same
// terms: see setClipDownloads.
func (p *Plugin) handleClipStreamer(w http.ResponseWriter, r *http.Request, userID, key string) {
	var request clipStreamerRequest
	if !readClipJSON(r, &request) || (request.Allow == nil) == (request.Downloads == nil) {
		p.writeClipError(w, http.StatusBadRequest, clipErrBadRequest)
		return
	}

	// No tag means no secret to take one with, which is this server's
	// trouble and not the caller's.
	tag := p.streamTag(key)
	if tag == "" {
		p.writeClipError(w, http.StatusServiceUnavailable, clipErrStore)
		return
	}

	s := p.clips()
	now := s.now()
	if !s.writeLimiter.allowAt(userID, now) {
		writeClipTooMany(w, s.writeLimiter, userID, now)
		return
	}

	if request.Downloads != nil {
		p.setClipDownloads(w, userID, tag, *request.Downloads, now)
		return
	}
	allow := *request.Allow

	switched := false
	_, err := p.clipsCAS(now, func(doc *clipsDoc, write *clipsWrite) (bool, error) {
		switched = false

		_, off := doc.OptOut[tag]
		switch {
		case allow && off:
			delete(doc.OptOut, tag)
			if len(doc.OptOut) == 0 {
				doc.OptOut = nil
			}
			switched = true
		case !allow && !off:
			if doc.OptOut == nil {
				doc.OptOut = map[string]clipMark{}
			}
			doc.OptOut[tag] = clipMark{By: userID, At: write.now}
			switched = true
		}

		return switched, nil
	})
	if err != nil {
		refundClipToken(s.writeLimiter, userID, err)
		p.writeClipRefusal(w, err)

		return
	}

	// Again whether or not the switch moved: whatever is still held of a
	// stream switched off — another node's recording seen before its switch
	// arrived, say — goes now.
	removed := 0
	if !allow {
		p.recorders().optOut(tag, now)
		removed = p.wipeDrafts(func(draft *clipDraft) bool { return draft.capture.StreamTag == tag }, now)
	}

	switch {
	case switched && allow:
		p.API.LogInfo("A streamer switched clipping of their stream back on", "user_id", userID)
	case switched:
		p.API.LogInfo("A streamer switched clipping of their stream off", "user_id", userID, "drafts_removed", removed)
	}

	p.republishClips()

	writeJSON(w, http.StatusOK, clipStreamerResponse{Allowed: allow})
}

// POST /api/v1/clips/wipe?handle=: wipe what this server holds of a stream —
// its live recording, its held end and every draft cut from it — for the
// stream's streamer, by their link now, or a linked admin (spec §2.10.6,
// A§3.3). The stream is whichever this node knows behind the handle: on the
// air, recorded, held after it ended, or only in the drafts cut from it.
// Published clips are not touched; the streamer deletes those from Clips.
func (p *Plugin) handleClipWipe(w http.ResponseWriter, r *http.Request, userID, key string) {
	handle := r.URL.Query().Get("handle")
	if !validClipHandle(handle) {
		p.writeClipError(w, http.StatusBadRequest, clipErrBadRequest)
		return
	}

	s := p.clips()
	now := s.now()
	if !s.writeLimiter.allowAt(userID, now) {
		writeClipTooMany(w, s.writeLimiter, userID, now)
		return
	}

	// Read afresh before anything is decided, and refused when it cannot
	// be, as every wipe is (A§2.1).
	if _, err := p.readClipsFresh(now); err != nil {
		refundClipToken(s.writeLimiter, userID, err)
		p.writeClipRefusal(w, err)

		return
	}

	streamKey := p.streamKeyBehind(handle)
	if !p.streamOf(key, p.streamTag(streamKey)) && !p.isSystemAdmin(userID) {
		p.writeClipError(w, http.StatusForbidden, clipErrNotAllowed)
		return
	}

	removed := p.wipeStream(handle, now)
	p.API.LogInfo("A stream's held recording was wiped", "user_id", userID, "stream_handle", handle, "drafts_removed", removed)

	writeJSON(w, http.StatusOK, clipWipeResponse{OK: true, DraftsRemoved: removed})
}

// POST /api/v1/admin/clips/wipe?handle=: the same wipe, from the System
// Console, for any system admin, linked or not.
func (p *Plugin) handleAdminClipWipe(w http.ResponseWriter, r *http.Request, adminID string) {
	handle := r.URL.Query().Get("handle")
	s := p.clips()
	now := s.now()
	if !p.adminClipWrite(w, adminID, validClipHandle(handle), now) {
		return
	}

	if _, err := p.readClipsFresh(now); err != nil {
		refundClipToken(s.writeLimiter, adminID, err)
		p.writeClipRefusal(w, err)

		return
	}

	removed := p.wipeStream(handle, now)
	p.API.LogInfo("A stream's held recording was wiped", "user_id", adminID, "stream_handle", handle, "drafts_removed", removed)

	writeJSON(w, http.StatusOK, clipWipeResponse{OK: true, DraftsRemoved: removed})
}
