package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/pmp4"
	"github.com/mattermost/mattermost/server/public/model"
)

// Drafts (spec §2.10.1–§2.10.2, as A§2.1, A§3 and A§4.14 amend them). A ✂
// cuts the last few minutes of a stream into a draft only the person who
// pressed it can see, kept on this node for two hours for them to trim, name
// and publish. Publishing turns the draft into a clip under the same id.
//
// A draft lives in this node's memory and in drafts/ beside it, and nowhere
// else: a restart loses every draft, and the README says so. Its state
// changes only under drafts.mu, and a publish is a state of its own, so a
// double click, a second tab and a retry after an answer was lost all meet
// the same draft and make one clip between them:
//
//	exporting → ready → publishing → published{clipId}
//	                  ↘ expired · wiped · discarded · blocked
//
// published and the four on the bottom row are tombstones, remembered for
// draftTombstone so that whoever asks next is told what became of it rather
// than that it never was.

// What a draft is doing.
const (
	// Its export is still running: a place under the caps is held for it,
	// and nothing else about it is shown or answered for.
	draftExporting  = "exporting"
	draftReady      = "ready"
	draftPublishing = "publishing"
	draftPublished  = "published"

	// Gone, each for its own reason, which is what it is answered with.
	draftExpired   = "expired"   // its two hours ran out, or Clips was switched off
	draftWiped     = "wiped"     // its streamer wiped what was held of the stream
	draftDiscarded = "discarded" // its owner threw it away
	draftBlocked   = "blocked"   // an admin stopped its owner making clips

	// A publish's body: a title and two numbers. A title is 80 runes once
	// cleaned, but what was typed may be longer, and a pasted token is
	// looked for in all of it before anything is cut.
	maxPublishRequestBytes = 8 << 10

	// How often the housekeeping runs, on each node.
	clipsHousekeepEvery = time.Minute

	draftVideoName = "draft-"
)

// clipRecordings is where drafts are cut from: the recorder manager
// (clips_manager.go), which the clips store is given when it is made.
// noClipRecordings says that nothing is being recorded. Tests hand the store
// recordings of their own, fed from the fixture through the media core's
// pipeline.
type clipRecordings interface {
	// live is what the recording of a live stream is doing, and, while it
	// is ready, the ring to cut from.
	live(handle string) clipLive

	// tail is the held end of a stream that has stopped, while it is held.
	tail(handle string, now time.Time) (clipTail, bool)
}

// clipLive is the recording of one live stream.
type clipLive struct {
	// One of the clipState values, or "" when nothing records the stream.
	state string

	// The epoch being recorded, while state is ready.
	ring *ring

	// The stream's measured bitrate, for how much room a draft will take.
	bytesPerSecond float64
}

// clipTail is the held end of a stream: its frozen ring, until when it is
// held, and the stream as it was when it ended — its key, which is never sent
// or logged, its name, whom it was attributed to, its note and its tag. reason
// is how it ended, clipTailEnded or clipTailDropped, as the recorder that
// handed it over says (clips_recorder.go).
type clipTail struct {
	ring   *ring
	until  time.Time
	stream apiStream
	reason string
}

// noClipRecordings is clipRecordings with no recorder behind it.
type noClipRecordings struct{}

func (noClipRecordings) live(string) clipLive { return clipLive{} }

func (noClipRecordings) tail(string, time.Time) (clipTail, bool) { return clipTail{}, false }

// clipCapture is what a draft keeps of the stream it was cut from, as it was
// at the click, and what the clip published from it is attributed to. The key
// is held for one thing only, taking it out of the clip's title, and is
// never logged or sent; the tag says whose stream it is to the server alone.
// TailReason is how the stream had stopped, for a draft cut from its held
// end: clipTailEnded or clipTailDropped.
type clipCapture struct {
	StreamKey string `json:"-"`
	StreamTag string `json:"-"`

	Handle       string
	StreamerID   string
	StreamerLink string
	StreamName   string
	StreamNote   string
	Tag          *clipTag
	TailReason   string
}

// captureOf is what a draft keeps of a stream.
func (p *Plugin) captureOf(stream apiStream) clipCapture {
	return clipCapture{
		StreamKey:    stream.StreamKey,
		StreamTag:    p.streamTag(stream.StreamKey),
		Handle:       stream.Handle,
		StreamerID:   stream.UserID,
		StreamerLink: stream.LinkSource,
		StreamName:   stream.Title,
		StreamNote:   stream.MOTD,
		Tag:          clipTagOf(stream.NowPlaying, stream.StreamKey),
	}
}

// clipDraft is one draft. Changed only under drafts.mu; read elsewhere only
// as a copy taken under it.
type clipDraft struct {
	id      string
	ownerID string
	source  string
	state   string
	clipID  string

	createdAt int64 // ms: the click
	expiresAt int64 // ms
	goneAt    time.Time

	capture clipCapture
	export  exportResult

	// Its picture: the first 8 hex of the JPEG's sha256, as a clip's is, and
	// its size, for the room it takes.
	thumb      string
	thumbBytes int64

	// What its export and its publish run under: a child of the store's run
	// context, so that stopping the plugin cancels them, and its own, so that
	// a wipe of its stream can.
	ctx    context.Context
	cancel context.CancelFunc

	// The tombstone it becomes if the publish under way fails, rather than
	// ready again: its owner was blocked, or Clips switched off, while it was
	// being published. A publish that succeeds is a clip all the same, since
	// both were checked when it began.
	discardTo string
}

// clipDrafts is every draft on this node.
type clipDrafts struct {
	mu   sync.Mutex
	byID map[string]*clipDraft
}

// holdsPlace is whether a draft counts under the caps and keeps its files.
func (d *clipDraft) holdsPlace() bool {
	return d.state == draftExporting || d.state == draftReady || d.state == draftPublishing
}

// gone makes a draft a tombstone. Under mu.
func (d *clipDraft) gone(state string, now time.Time) {
	d.state, d.goneAt = state, now
	if d.cancel != nil {
		d.cancel()
	}
}

// reserve holds a place under the caps for a draft whose export is about to
// run, so that two clicks at once cannot both take the last one. The caller
// either completes it or abandons it. A draft's id becomes its clip's, so it
// is never one a clip in the document already has.
func (ds *clipDrafts) reserve(ownerID, source string, capture clipCapture, taken func(id string) bool, parent context.Context) (*clipDraft, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	mine, total := 0, 0
	for _, draft := range ds.byID {
		if !draft.holdsPlace() {
			continue
		}
		total++
		if draft.ownerID == ownerID {
			mine++
		}
	}
	if mine >= maxDraftsPerUser || total >= maxDraftsTotal {
		return nil, refuseClip(http.StatusConflict, clipErrDraftCap)
	}

	id := ""
	for range 4 {
		candidate := newID()
		if _, held := ds.byID[candidate]; candidate != "" && !held && !taken(candidate) {
			id = candidate
			break
		}
	}
	if id == "" {
		return nil, errStore
	}

	ctx, cancel := context.WithCancel(parent)
	draft := &clipDraft{id: id, ownerID: ownerID, source: source, state: draftExporting, capture: capture, ctx: ctx, cancel: cancel}
	if ds.byID == nil {
		ds.byID = map[string]*clipDraft{}
	}
	ds.byID[id] = draft

	return draft, nil
}

// abandon gives up a reservation whose export did not make a draft.
func (ds *clipDrafts) abandon(id string) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	if draft := ds.byID[id]; draft != nil && draft.state == draftExporting {
		draft.cancel()
		delete(ds.byID, id)
	}
}

// complete makes a reservation a draft, from its export. False when it was
// taken away meanwhile — its owner blocked, Clips switched off, the plugin
// stopping — and the caller deletes the files and says why (takenAway).
func (ds *clipDrafts) complete(id string, result exportResult, clickAt time.Time) (clipDraft, bool) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	draft := ds.byID[id]
	if draft == nil || draft.state != draftExporting {
		return clipDraft{}, false
	}

	draft.state = draftReady
	draft.export = result
	draft.createdAt = clickAt.UnixMilli()
	draft.expiresAt = clickAt.Add(draftTTL).UnixMilli()

	return *draft, true
}

// takenAway is why a reservation is no longer its export's, or nil while it
// still is. Taking one away makes it a tombstone of the reason, except for
// the plugin stopping, which forgets it outright: the click is answered as
// the panel would want it, since the draft it asked for was never shown.
func (ds *clipDrafts) takenAway(id string) error {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	draft := ds.byID[id]
	switch {
	case draft == nil:
		return refuseClip(http.StatusServiceUnavailable, clipErrBusy)
	case draft.state == draftExporting:
		return nil
	case draft.state == draftExpired:
		// Only switching Clips off takes a reservation away as expired:
		// one still being cut has no two hours to run out.
		return refuseClip(http.StatusForbidden, clipErrClipsOff)
	}

	return draft.refusal()
}

// find is the draft with this id, if it is the owner's and is a draft at all
// yet: anybody else's, and one still being cut, are as good as unknown.
func (ds *clipDrafts) find(id, ownerID string) (clipDraft, bool) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	draft := ds.byID[id]
	if draft == nil || draft.ownerID != ownerID || draft.state == draftExporting {
		return clipDraft{}, false
	}

	return *draft, true
}

// ownedBy is somebody's drafts as the list shows them, newest first: those
// still to publish, those being published, and those published a moment ago,
// so that a second tab finds out. Never one that went any other way.
func (ds *clipDrafts) ownedBy(ownerID string) []clipDraft {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	var drafts []clipDraft
	for _, draft := range ds.byID {
		if draft.ownerID != ownerID {
			continue
		}
		switch draft.state {
		case draftReady, draftPublishing, draftPublished:
			drafts = append(drafts, *draft)
		}
	}

	sort.Slice(drafts, func(i, j int) bool {
		if drafts[i].createdAt != drafts[j].createdAt {
			return drafts[i].createdAt > drafts[j].createdAt
		}

		return drafts[i].id < drafts[j].id
	})

	return drafts
}

// counted is how many drafts this node holds, for the System Console.
func (ds *clipDrafts) counted() int {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	held := 0
	for _, draft := range ds.byID {
		if draft.state == draftReady || draft.state == draftPublishing {
			held++
		}
	}

	return held
}

// placeHolders is the id of every draft whose files are still its own.
func (ds *clipDrafts) placeHolders() map[string]bool {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	ids := map[string]bool{}
	for id, draft := range ds.byID {
		if draft.holdsPlace() {
			ids[id] = true
		}
	}

	return ids
}

// discardOwn throws away one of its owner's drafts, if it is ready, and
// answers it for its files to be deleted, all in one decision: a draft that
// is being published by now is busy, and one already gone is as good as
// thrown away. Anybody else's, and one never made, are not there.
func (ds *clipDrafts) discardOwn(id, ownerID string, now time.Time) (clipDraft, bool, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	draft := ds.byID[id]
	switch {
	case draft == nil || draft.ownerID != ownerID || draft.state == draftExporting:
		return clipDraft{}, false, draftGone()
	case draft.state == draftPublishing:
		return clipDraft{}, false, refuseClip(http.StatusConflict, clipErrPublishing)
	case draft.state != draftReady:
		return clipDraft{}, false, nil
	}

	draft.gone(draftDiscarded, now)

	return *draft, true, nil
}

// discard takes away every draft that match picks, whatever it is doing, as
// a tombstone of state: Clips switched off, or its owner blocked.
//
//   - A ready draft goes at once, and is answered for its files to be
//     deleted.
//   - One still being cut goes at once too: its export is cancelled, and the
//     click that is cutting it finds it gone, deletes whatever the export
//     wrote and says why. It is counted, but its files are the click's.
//   - One being published is left to finish, since the publish was allowed
//     when it began; if it fails, the draft becomes this tombstone rather
//     than ready again (endPublish). It is not counted: it may well become
//     a clip.
func (ds *clipDrafts) discard(match func(*clipDraft) bool, state string, now time.Time) (ready []clipDraft, cut int) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	for _, draft := range ds.byID {
		if !draft.holdsPlace() || !match(draft) {
			continue
		}

		switch draft.state {
		case draftReady:
			draft.gone(state, now)
			ready = append(ready, *draft)
		case draftExporting:
			draft.gone(state, now)
			cut++
		case draftPublishing:
			draft.discardTo = state
		}
	}

	return ready, cut
}

// wipe takes away every draft that match picks as wiped, whatever it is doing,
// for a streamer's or an admin's wipe of the stream it was cut from (spec
// §2.10.6 step 4). As discard does, and more: a draft being published has its
// publish cancelled too, since what it is made of was not to be kept, and it
// becomes wiped once the publish gives up. A publish whose clip is already
// written is a clip all the same, and stays: deleting it is the streamer's to
// do from Clips. Answered as discard is, those being published counted with
// the ones being cut.
func (ds *clipDrafts) wipe(match func(*clipDraft) bool, now time.Time) (ready []clipDraft, others int) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	for _, draft := range ds.byID {
		if !draft.holdsPlace() || !match(draft) {
			continue
		}

		switch draft.state {
		case draftReady:
			draft.gone(draftWiped, now)
			ready = append(ready, *draft)
		case draftExporting:
			draft.gone(draftWiped, now)
			others++
		case draftPublishing:
			draft.discardTo = draftWiped
			if draft.cancel != nil {
				draft.cancel()
			}
			others++
		}
	}

	return ready, others
}

// streamKeyOf is the key of the stream a draft still held was cut from by
// this handle, or "".
func (ds *clipDrafts) streamKeyOf(handle string) string {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	for _, draft := range ds.byID {
		if draft.holdsPlace() && draft.capture.Handle == handle && draft.capture.StreamKey != "" {
			return draft.capture.StreamKey
		}
	}

	return ""
}

// expire makes every ready draft whose two hours are up an expired tombstone,
// and answers which they were, for their files to be deleted. One being
// published is left to finish, and one still being cut has not started its
// two hours.
func (ds *clipDrafts) expire(now time.Time) []clipDraft {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	nowMS := now.UnixMilli()

	var expired []clipDraft
	for _, draft := range ds.byID {
		if draft.state != draftReady || nowMS < draft.expiresAt {
			continue
		}
		draft.gone(draftExpired, now)
		expired = append(expired, *draft)
	}

	return expired
}

// pruneTombstones forgets tombstones draftTombstone after they were made.
func (ds *clipDrafts) pruneTombstones(now time.Time) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	for id, draft := range ds.byID {
		if !draft.holdsPlace() && now.Sub(draft.goneAt) >= draftTombstone {
			delete(ds.byID, id)
		}
	}
}

// forgetAll drops every draft, their exports and publishes cancelled: the
// plugin is stopping, and drafts/ is about to be emptied.
func (ds *clipDrafts) forgetAll() {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	for _, draft := range ds.byID {
		if draft.cancel != nil {
			draft.cancel()
		}
	}
	ds.byID = nil
}

// refusal is how a route that works on a draft answers for one that is not
// ready: one being published is busy, and a tombstone says what became of
// it. Nil for a ready draft.
func (d *clipDraft) refusal() error {
	switch d.state {
	case draftReady:
		return nil
	case draftPublishing:
		return refuseClip(http.StatusConflict, clipErrPublishing)
	case draftWiped:
		return refuseClip(http.StatusGone, clipErrDraftWiped)
	case draftBlocked:
		return refuseClip(http.StatusForbidden, clipErrBlocked)
	default:
		return refuseClip(http.StatusGone, clipErrDraftGone)
	}
}

// draftGone is the answer for a draft that is unknown, or somebody else's:
// the same, so that nobody can tell the two apart.
func draftGone() error {
	return refuseClip(http.StatusNotFound, clipErrDraftGone)
}

// view is a draft as its owner sees it.
func (d *clipDraft) view(names clipNamer) draftView {
	view := draftView{
		ID:          d.id,
		Handle:      d.capture.Handle,
		Source:      d.source,
		State:       d.state,
		CreatedAt:   d.createdAt,
		ExpiresAt:   d.expiresAt,
		StartAt:     d.export.startAt,
		DurationMs:  d.export.durationMs,
		RequestedMs: d.export.requestedMs,
		Partial:     d.export.partial,
		Truncated:   d.export.truncated,
		Width:       d.export.width,
		Height:      d.export.height,
		Codecs:      d.export.codecs,
		Bytes:       d.export.bytes,
		HasThumb:    d.thumb != "",
		Streamer:    clipStreamerOf(&clipEntry{StreamerID: d.capture.StreamerID, StreamName: d.capture.StreamName}, names),
		StreamNote:  d.capture.StreamNote,
		Tag:         d.capture.Tag.shown(),
	}
	if d.state == draftPublished {
		view.ClipID = d.clipID
	}
	if d.source == clipSourceTail {
		view.TailReason = d.capture.TailReason
	}

	return view
}

// draftFile is where a draft's video or picture is, under root.
func draftFile(root, id, ext string) string {
	return filepath.Join(root, clipsDraftsDir, id+ext)
}

// removeDraftFiles deletes a draft's video and picture, a file already gone
// being as good as deleted, and gives back the room they took. Only the room
// of a file this call removed: one somebody else removed between the look and
// the removal was given back by them.
func (s *clipsStore) removeDraftFiles(id string) int64 {
	root := s.writableRoot()
	if root == "" || !validClipID(id) {
		return 0
	}

	freed := int64(0)
	for _, ext := range []string{clipVideoExt, clipThumbExt} {
		path := draftFile(root, id, ext)

		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if err := os.Remove(path); err == nil {
			freed += info.Size()
		}
	}

	s.addUsage("", -freed)

	return freed
}

// discardDrafts takes away the drafts that match picks, as tombstones of
// state, and deletes the files of those that were ready: Clips switched off,
// and an admin blocking somebody (see clipDrafts.discard for drafts being cut
// or published). It answers how many went.
func (p *Plugin) discardDrafts(match func(*clipDraft) bool, state string, now time.Time) int {
	s := p.clips()

	ready, cut := s.drafts.discard(match, state, now)
	for _, draft := range ready {
		s.removeDraftFiles(draft.id)
	}

	return len(ready) + cut
}

// wipeDrafts takes away the drafts that match picks as wiped, publishes under
// way included (clipDrafts.wipe), and deletes the files of those that were
// ready. It answers how many went.
func (p *Plugin) wipeDrafts(match func(*clipDraft) bool, now time.Time) int {
	s := p.clips()

	ready, others := s.drafts.wipe(match, now)
	for _, draft := range ready {
		s.removeDraftFiles(draft.id)
	}

	return len(ready) + others
}

// expireDrafts is the housekeeping's two hours: every ready draft past them
// goes, with its files.
func (p *Plugin) expireDrafts(now time.Time) {
	s := p.clips()

	for _, draft := range s.drafts.expire(now) {
		s.removeDraftFiles(draft.id)
	}
}

// runContext is what exports and trims run under: the store's from
// startClips to stopClips, and one that is already over at any other time.
func (s *clipsStore) runContext() context.Context {
	s.runLock.Lock()
	defer s.runLock.Unlock()

	if s.runCtx == nil {
		ended, cancel := context.WithCancel(context.Background())
		cancel()

		return ended
	}

	return s.runCtx
}

// withRequest is ctx, ended early too when the request is: a click whose
// asker has gone stops waiting for a slot.
func withRequest(ctx context.Context, r *http.Request) (context.Context, func()) {
	merged, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.Context(), cancel)

	return merged, func() {
		stop()
		cancel()
	}
}

// clickKey carries a POST /clips/drafts from before the gate to the handler:
// when it arrived, and the streams on the air then.
type clickKey struct{}

// clickMoment is a click as it arrived. The draft is the minutes that end at
// it, and the stream's name, attribution, note and tag are what the snapshot
// said then (A§4.14), however long anything after takes. The snapshot's list
// of streams is never changed once published, so holding it is safe.
type clickMoment struct {
	at      time.Time
	streams []apiStream
}

// withClick is serveClips's, for a click, before the gate.
func (p *Plugin) withClick(r *http.Request) *http.Request {
	click := clickMoment{at: p.clips().now(), streams: p.getSnapshot().Streams}

	return r.WithContext(context.WithValue(r.Context(), clickKey{}, click))
}

// clickOf is the click a request carries, or now and the snapshot as it is,
// for one that did not come through serveClips.
func (p *Plugin) clickOf(r *http.Request, now time.Time) clickMoment {
	if click, ok := r.Context().Value(clickKey{}).(clickMoment); ok {
		return click
	}

	return clickMoment{at: now, streams: p.getSnapshot().Streams}
}

// validClipHandle is the shape of a stream's public handle: 32 lower-case hex.
func validClipHandle(handle string) bool {
	return len(handle) == 32 && validClipID(handle[:16]) && validClipID(handle[16:])
}

type draftRequest struct {
	Handle string `json:"handle"`
	Source string `json:"source"`
}

type draftListResponse struct {
	Drafts []draftView `json:"drafts"`
}

// GET /api/v1/clips/drafts: the caller's own drafts, and nobody else's.
func (p *Plugin) handleDraftList(w http.ResponseWriter, _ *http.Request, userID, _ string) {
	s := p.clips()
	names := p.clipNames(s.now(), true)

	views := []draftView{}
	for _, draft := range s.drafts.ownedBy(userID) {
		views = append(views, draft.view(names))
	}

	writeJSON(w, http.StatusOK, draftListResponse{Drafts: views})
}

// draftSource is the stream a click asks to cut from, as the gate and the
// draft need it: where it is recorded, and what it was at the click.
type draftSource struct {
	target  clipTarget
	capture clipCapture
	ring    *ring
	at      time.Time
	state   string
	rate    float64
}

// draftSourceOf finds what a click is to be cut from, never by asking
// Broadcast Box:
//
//   - live: the stream must be in the snapshot as it is now, and not a 24/7
//     channel, or it is not live; what it is, is taken from there, at the
//     click;
//   - tail: the held end of it must still be held, and what it was is what
//     the tail kept.
func (p *Plugin) draftSourceOf(handle, source string, click clickMoment, now time.Time) (draftSource, error) {
	recordings := p.clips().recordings

	if source == clipSourceTail {
		tail, held := recordings.tail(handle, now)
		if !held || tail.ring == nil || !now.Before(tail.until) {
			return draftSource{}, refuseClip(http.StatusGone, clipErrTailGone)
		}

		end := tail.ring.endTime()
		tail.stream.Handle = handle
		capture := p.captureOf(tail.stream)
		capture.TailReason = tail.reason

		return draftSource{
			target:  clipTarget{handle: handle, source: source, streamKey: tail.stream.StreamKey, end: end},
			capture: capture,
			ring:    tail.ring,
			at:      end,
			state:   clipStateReady,
			rate:    tail.ring.status().measuredBps,
		}, nil
	}

	for _, stream := range click.streams {
		if stream.Handle != handle {
			continue
		}
		if stream.AlwaysOn || stream.StreamKey == "" || stream.Source != "" {
			break
		}

		recording := recordings.live(handle)

		return draftSource{
			target:  clipTarget{handle: handle, source: source, streamKey: stream.StreamKey},
			capture: p.captureOf(stream),
			ring:    recording.ring,
			at:      click.at,
			state:   recording.state,
			rate:    recording.bytesPerSecond,
		}, nil
	}

	return draftSource{}, refuseClip(http.StatusNotFound, clipErrNotLive)
}

// recordingRefusal is how a click on a live stream is answered for what its
// recording is doing, or nil when it is recording.
func recordingRefusal(state string) error {
	switch state {
	case clipStateReady:
		return nil
	case clipStateStarting:
		return refuseClip(http.StatusConflict, clipErrWarmingUp)
	case clipStateFailed, clipStateFull, clipStateQueued:
		return refuseClip(http.StatusServiceUnavailable, clipErrRecordingFailed)
	default:
		// unsupported, off, or nothing recording it at all.
		return refuseClip(http.StatusConflict, clipErrNotClippable)
	}
}

// POST /api/v1/clips/drafts {"handle","source"}: cut a draft at the click, in
// this order (spec §2.10.1, A§3.4):
//
//  0. The click's moment was taken before the gate, and the gate has run;
//     the attempt is metered (tooManyClipAttempts).
//  1. The request; Clips switched on; somewhere to keep it; the document
//     readable, read afresh, and the caller not blocked.
//  2. The stream: live now, or its end still held.
//  3. Its streamer has not switched clipping of it off.
//  4. The caller may clip it: its streamer, or watching it (mayClip).
//  5. For a live stream, it is being recorded and has been for long enough.
//  6. A place under the caps, and room on the disk.
//  7. Only now a token, refunded if the stream turns out to be warming up,
//     no export slot frees in time, or the export fails — none of which was
//     the caller's doing.
//  8. The export, of the window that ends at the click.
//
// Switching Clips off, or blocking the caller, while any of that runs takes
// the draft away: once its place is held, by taking the place (discard), and
// before then, because the place is not taken without looking again
// (clickStillStands). Either way the click is told why, and its token comes
// back.
func (p *Plugin) handleDraftCreate(w http.ResponseWriter, r *http.Request, userID, key string) {
	s := p.clips()
	now := s.now()
	click := p.clickOf(r, now)

	if p.tooManyClipAttempts(w, userID, now) {
		return
	}

	var request draftRequest
	valid := readClipJSON(r, &request) && validClipHandle(request.Handle) &&
		(request.Source == clipSourceLive || request.Source == clipSourceTail)
	if !valid {
		p.writeClipError(w, http.StatusBadRequest, clipErrBadRequest)
		return
	}

	cfg := p.getConfiguration()
	if err := p.mayMakeClips(cfg); err != nil {
		p.writeClipRefusal(w, err)
		return
	}

	doc, err := p.readClipsFresh(now)
	if err != nil {
		p.writeClipRefusal(w, err)
		return
	}
	if _, blocked := doc.Blocked[userID]; blocked {
		p.writeClipError(w, http.StatusForbidden, clipErrBlocked)
		return
	}

	source, err := p.draftSourceOf(request.Handle, request.Source, click, now)
	if err != nil {
		p.writeClipRefusal(w, err)
		return
	}

	if tag := source.capture.StreamTag; tag != "" {
		if _, off := doc.OptOut[tag]; off {
			p.writeClipError(w, http.StatusForbidden, clipErrStreamerOff)
			return
		}
	}

	if !p.mayClip(userID, key, source.target, now) {
		p.writeClipError(w, http.StatusForbidden, clipErrNotWatching)
		return
	}

	if err := recordingRefusal(source.state); err != nil {
		p.writeClipRefusal(w, err)
		return
	}
	if source.ring == nil {
		p.writeClipError(w, http.StatusConflict, clipErrNotClippable)
		return
	}

	taken := func(id string) bool { return doc.clip(id) != nil }
	draft, err := s.drafts.reserve(userID, request.Source, source.capture, taken, s.runContext())
	if err != nil {
		p.writeClipRefusal(w, err)
		return
	}
	made := false
	defer func() {
		if !made {
			s.drafts.abandon(draft.id)
		}
	}()

	if err := p.clickStillStands(userID); err != nil {
		p.writeClipRefusal(w, err)
		return
	}

	length := time.Duration(cfg.clipLengthSeconds()) * time.Second
	if err := s.roomFor("", int64(length.Seconds()*source.rate), cfg); err != nil {
		p.writeClipRefusal(w, err)
		return
	}

	if !s.captureLimiter.allowAt(userID, now) {
		writeClipTooMany(w, s.captureLimiter, userID, now)
		return
	}

	root := s.writableRoot()
	if root == "" {
		s.captureLimiter.refund(userID)
		p.writeClipError(w, http.StatusServiceUnavailable, clipErrStorageUnavailable)
		return
	}

	ctx, stop := withRequest(draft.ctx, r)
	result, err := exportDraft(ctx, source.ring, source.at, length, filepath.Join(root, clipsDraftsDir), draft.id)
	stop()
	if err != nil {
		s.captureLimiter.refund(userID)

		// An export cancelled because its draft was taken away failed for
		// that, and is answered for it.
		if refusal := s.drafts.takenAway(draft.id); refusal != nil {
			p.writeClipRefusal(w, refusal)
			return
		}

		switch {
		case errors.Is(err, errWarmingUp):
			p.writeClipError(w, http.StatusConflict, clipErrWarmingUp)
		case errors.Is(err, errBusy):
			p.writeClipError(w, http.StatusServiceUnavailable, clipErrBusy)
		default:
			p.API.LogWarn("Could not cut a clip draft", "user_id", userID, "draft_id", draft.id, "stream_handle", request.Handle, "error", err.Error())
			p.writeClipError(w, http.StatusServiceUnavailable, clipErrRecordingFailed)
		}

		return
	}

	s.addUsage("", result.bytes)

	ready, kept := s.drafts.complete(draft.id, result, click.at)
	if !kept {
		// Taken away while it was cut, after the export had finished: what
		// it wrote goes, and the click is told why, at no cost.
		s.removeDraftFiles(draft.id)
		s.captureLimiter.refund(userID)

		refusal := s.drafts.takenAway(draft.id)
		if refusal == nil {
			refusal = refuseClip(http.StatusServiceUnavailable, clipErrBusy)
		}
		p.writeClipRefusal(w, refusal)

		return
	}
	made = true

	p.API.LogInfo("A clip draft was made", "user_id", userID, "draft_id", draft.id, "stream_handle", request.Handle, "source", request.Source, "duration_ms", result.durationMs)

	writeJSON(w, http.StatusCreated, ready.view(p.clipNames(now, true)))
}

// mayMakeClips is what a draft and a publish both need before anything is
// looked at: Clips switched on, somewhere to keep clips, and a document that
// can be read. A store that cannot be written to is "not set up"; one halted
// is the store's trouble.
func (p *Plugin) mayMakeClips(cfg *configuration) error {
	if !cfg.EnableClips {
		return refuseClip(http.StatusForbidden, clipErrClipsOff)
	}

	s := p.clips()
	if s.writableRoot() == "" {
		return refuseClip(http.StatusServiceUnavailable, clipErrStorageUnavailable)
	}
	if halted, _ := s.halt.state(); halted {
		return errClipsHalted
	}

	return nil
}

// clickStillStands looks again, once a click's place under the caps is held,
// at the two things that take drafts away: Clips switched on, and the caller
// not blocked. Each takes away every place already held, but a click that
// looked just before either and held its place just after would otherwise be
// missed. OnConfigurationChange stores the setting before it takes drafts
// away, and a block writes this node's copy of the document before it does,
// so whichever of the two came first, the click is caught.
func (p *Plugin) clickStillStands(userID string) error {
	if !p.getConfiguration().EnableClips {
		return refuseClip(http.StatusForbidden, clipErrClipsOff)
	}

	if doc := p.clips().cached(); doc != nil {
		if _, blocked := doc.Blocked[userID]; blocked {
			return refuseClip(http.StatusForbidden, clipErrBlocked)
		}
	}

	return nil
}

// DELETE /api/v1/clips/draft?id=: throw a draft away. Doing it twice is doing
// it once; a draft being published cannot be, and somebody else's is not
// there at all.
func (p *Plugin) handleDraftDelete(w http.ResponseWriter, r *http.Request, userID, _ string) {
	draftID := r.URL.Query().Get("id")
	if !validClipID(draftID) {
		p.writeClipError(w, http.StatusBadRequest, clipErrBadRequest)
		return
	}

	s := p.clips()
	now := s.now()
	if !s.writeLimiter.allowAt(userID, now) {
		writeClipTooMany(w, s.writeLimiter, userID, now)
		return
	}

	draft, discarded, err := s.drafts.discardOwn(draftID, userID, now)
	if err != nil {
		p.writeClipRefusal(w, err)
		return
	}
	if discarded {
		s.removeDraftFiles(draft.id)
	}

	writeJSON(w, http.StatusOK, clipOKResponse{OK: true})
}

// GET|HEAD /api/v1/clips/draft/video?id=: a draft's video, with ranges, for
// its owner alone, the editor's included while the draft is being published.
// A video that cannot be found is answered with a code the panel knows: the
// draft is gone, or, while it is being published, busy.
func (p *Plugin) handleDraftVideo(w http.ResponseWriter, r *http.Request, userID, _ string) {
	draftID := r.URL.Query().Get("id")
	if !validClipID(draftID) {
		p.writeClipError(w, http.StatusBadRequest, clipErrBadRequest)
		return
	}

	s := p.clips()
	var missing error

	// Twice: a draft can be published, thrown away or taken away between the
	// look and the opening, and the second look says which.
	for range 2 {
		draft, found := s.drafts.find(draftID, userID)
		if !found {
			p.writeClipRefusal(w, draftGone())
			return
		}
		if draft.state != draftPublishing {
			if err := draft.refusal(); err != nil {
				p.writeClipRefusal(w, err)
				return
			}
		}

		root := s.readRoot()
		if root == "" {
			p.writeClipError(w, http.StatusServiceUnavailable, clipErrStorageUnavailable)
			return
		}

		file, info, opened := openDraftVideo(root, draftID, draft.state == draftPublishing)
		if opened {
			serveOpenClipFile(w, r, file, info, draftVideoName+draftID+clipVideoExt)
			_ = file.Close()

			return
		}

		missing = refuseClip(http.StatusNotFound, clipErrDraftGone)
		if draft.state == draftPublishing {
			missing = refuseClip(http.StatusConflict, clipErrPublishing)
		}
	}

	p.writeClipRefusal(w, missing)
}

// openDraftVideo opens a draft's video to serve it. While the draft is being
// published, a publish of the whole of it renames the video into clips/
// before it writes the clip, and back if the write fails, so it is looked
// for there, and then where it was again: the same bytes wherever they are.
// A trim leaves the draft's own video where it is until the publish is over
// (handleDraftPublish), so a clips/ file is only ever looked at when it is
// the draft's own. Once open, the file is served whatever is renamed
// meanwhile.
func openDraftVideo(root, id string, publishing bool) (*os.File, fs.FileInfo, bool) {
	paths := []string{draftFile(root, id, clipVideoExt)}
	if publishing {
		paths = append(paths, filepath.Join(root, clipsFilesDir, id+clipVideoExt), paths[0])
	}

	for _, path := range paths {
		if file, info, err := openClipFile(path); err == nil {
			return file, info, true
		}
	}

	return nil, nil, false
}

// PUT /api/v1/clips/draft/thumb?id=: a draft's picture, as the browser made
// it from a frame: a JPEG of a card's shape, of 200 KB at most. Kept beside
// the draft, and moved with it when it is published.
func (p *Plugin) handleDraftThumb(w http.ResponseWriter, r *http.Request, userID, _ string) {
	draftID := r.URL.Query().Get("id")
	if !validClipID(draftID) {
		p.writeClipError(w, http.StatusBadRequest, clipErrBadRequest)
		return
	}

	s := p.clips()
	now := s.now()
	draft, found := s.drafts.find(draftID, userID)
	if !found {
		p.writeClipRefusal(w, draftGone())
		return
	}
	if err := draft.refusal(); err != nil {
		p.writeClipRefusal(w, err)
		return
	}

	if !s.writeLimiter.allowAt(userID, now) {
		writeClipTooMany(w, s.writeLimiter, userID, now)
		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, maxClipThumbBytes+1))
	switch {
	case err != nil:
		p.writeClipError(w, http.StatusBadRequest, clipErrBadThumb)
		return
	case len(data) > maxClipThumbBytes:
		p.writeClipError(w, http.StatusRequestEntityTooLarge, clipErrBadThumb)
		return
	}
	if _, problem := checkStill(data); problem != "" {
		p.writeClipError(w, http.StatusBadRequest, clipErrBadThumb)
		return
	}

	if err := p.putDraftThumb(draftID, userID, data); err != nil {
		refundClipToken(s.writeLimiter, userID, err)
		p.writeClipRefusal(w, err)

		return
	}

	p.API.LogInfo("A clip picture was uploaded", "user_id", userID, "draft_id", draftID, "bytes", len(data))

	writeJSON(w, http.StatusOK, clipOKResponse{OK: true})
}

// putDraftThumb writes a draft's picture through a temporary file, and puts
// it in place under drafts.mu, so that a publish, which takes the picture
// with it, never finds half of one, and never misses one put in place just
// before it began.
func (p *Plugin) putDraftThumb(draftID, userID string, data []byte) error {
	s := p.clips()

	root := s.writableRoot()
	if root == "" {
		return refuseClip(http.StatusServiceUnavailable, clipErrStorageUnavailable)
	}

	file, temp, err := createClipTemp(filepath.Join(root, clipsDraftsDir), draftID, clipThumbExt)
	if err != nil {
		p.API.LogWarn("Could not keep a clip picture", "draft_id", draftID, "error", err.Error())
		return errStore
	}
	_, err = file.Write(data)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(temp)
		p.API.LogWarn("Could not keep a clip picture", "draft_id", draftID, "error", err.Error())

		return errStore
	}

	thumb := clipThumbVersion(data)

	s.drafts.mu.Lock()
	defer s.drafts.mu.Unlock()

	draft := s.drafts.byID[draftID]
	if draft == nil || draft.ownerID != userID || draft.state == draftExporting {
		_ = os.Remove(temp)
		return draftGone()
	}
	if err := draft.refusal(); err != nil {
		_ = os.Remove(temp)
		return err
	}

	if err := os.Rename(temp, draftFile(root, draftID, clipThumbExt)); err != nil {
		_ = os.Remove(temp)
		p.API.LogWarn("Could not keep a clip picture", "draft_id", draftID, "error", err.Error())

		return errStore
	}

	s.addUsage("", int64(len(data))-draft.thumbBytes)
	draft.thumb, draft.thumbBytes = thumb, int64(len(data))

	return nil
}

// clipThumbVersion is a picture's version, as a clip carries it and as the
// address of the picture asks for it: the first 8 hex of its sha256.
func clipThumbVersion(picture []byte) string {
	sum := sha256.Sum256(picture)

	return hex.EncodeToString(sum[:])[:8]
}

type publishRequest struct {
	Title string `json:"title"`
	InMs  *int64 `json:"inMs"`
	OutMs *int64 `json:"outMs"`
}

// readPublishRequest reads a publish's body: up to 8 KiB of JSON, with both
// ends of the range in it.
func readPublishRequest(r *http.Request) (publishRequest, bool) {
	var request publishRequest

	body, err := io.ReadAll(io.LimitReader(r.Body, maxPublishRequestBytes+1))
	if err != nil || len(body) > maxPublishRequestBytes || json.Unmarshal(body, &request) != nil {
		return publishRequest{}, false
	}

	return request, request.InMs != nil && request.OutMs != nil
}

// cleanClipTitle is a clip's title as it is kept (A§2.1), in an order that
// fails closed:
//
//  1. Unicode tidied and trimmed.
//  2. The caller's own publish token taken out, read strictly: a store that
//     does not answer refuses the publish rather than letting a pasted token
//     through.
//  3. Their own stream key, as the gate read it, and the clipped stream's key
//     taken out.
//  4. Only then cut to 80 runes, so a credential pasted near the end cannot
//     leave half of itself behind.
//
// Not withoutOwnKey, which reads the token and the key in a way that takes a
// failure to read for having none.
func (p *Plugin) cleanClipTitle(userID, key, streamKey, title string) (string, error) {
	cleaned := strings.TrimSpace(model.SanitizeUnicode(title))

	token, appErr := p.API.KVGet(kvLinkTokenPrefix + userID)
	if appErr != nil {
		p.API.LogWarn("Could not read a publish token to keep it out of a clip title", "user_id", userID, "error", appErr.Error())
		return "", errStore
	}

	cleaned = redactStreamKey(cleaned, string(token), "my stream")
	cleaned = redactStreamKey(cleaned, key, "my stream")
	cleaned = redactStreamKey(cleaned, streamKey, "the stream")

	return cleanText(cleaned, maxClipTitle), nil
}

// draftTrimPlan is where a trim of a draft would cut it, from the draft's own
// sample table, read without any of its media: whether the range is inside
// it, whether what it leaves is long enough, and where it starts.
func draftTrimPlan(path string, inMs, outMs int64) (trimPlan, error) {
	file, err := os.Open(path)
	if err != nil {
		return trimPlan{}, err
	}
	defer file.Close()

	var draft pmp4.Presentation
	if err := draft.Unmarshal(file); err != nil {
		return trimPlan{}, fmt.Errorf("reading the draft: %w", err)
	}

	for _, track := range draft.Tracks {
		if track.Codec.IsVideo() {
			return planTrim(track.Samples, track.TimeScale, inMs, outMs)
		}
	}

	return trimPlan{}, errors.New("the draft has no video")
}

// rangeRefusal is how a trim that cannot be made is answered: the caller's
// range, or nil for anything else.
func rangeRefusal(err error) error {
	switch {
	case errors.Is(err, errBadRange):
		return refuseClip(http.StatusBadRequest, clipErrBadRange)
	case errors.Is(err, errTooShort):
		return refuseClip(http.StatusBadRequest, clipErrTooShort)
	}

	return nil
}

// answerSettledDraft answers a publish of a draft that is not ready, as spec
// §2.10.2 step 2 says, and reports whether it did: one already published is
// answered with its clip, as the first publish was, or 410 once that has
// gone; one being published is busy; a tombstone says what became of it.
func (p *Plugin) answerSettledDraft(w http.ResponseWriter, draft clipDraft, userID, key string, now time.Time) bool {
	switch draft.state {
	case draftReady:
		return false
	case draftPublished:
		reading, err := p.readClips(now, p.clipNamesNow(now))
		if err != nil {
			p.writeClipRefusal(w, err)
			return true
		}

		entry := reading.doc.liveClip(draft.clipID, reading.kept, reading.now)
		if entry == nil {
			p.writeClipError(w, http.StatusGone, clipErrClipGone)
			return true
		}

		writeJSON(w, http.StatusOK, reading.viewOf(entry, p.clipViewerOf(userID, key, now)))

		return true
	}

	p.writeClipRefusal(w, draft.refusal())

	return true
}

// publishing is one publish's own doing, so that a failure undoes exactly
// that and nothing a concurrent request did.
type publishing struct {
	renamed    bool // the draft's video was renamed into clips/
	trimmed    bool // a new video was written into clips/
	thumbMoved bool // the draft's picture was moved into clips/
}

// undo puts the files back as they were before this publish touched them.
func (done publishing) undo(root, id string) {
	clipVideo := filepath.Join(root, clipsFilesDir, id+clipVideoExt)
	clipThumb := filepath.Join(root, clipsFilesDir, id+clipThumbExt)

	if done.thumbMoved {
		_ = os.Rename(clipThumb, draftFile(root, id, clipThumbExt))
	}
	switch {
	case done.renamed:
		_ = os.Rename(clipVideo, draftFile(root, id, clipVideoExt))
	case done.trimmed:
		_ = os.Remove(clipVideo)
	}
}

// beginPublish makes a ready draft the one being published: the moment two
// publishes of it part ways, since only one can find it ready. The other is
// answered as a publish of a draft in that state would be.
func (ds *clipDrafts) beginPublish(id, ownerID string) (clipDraft, error) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	draft := ds.byID[id]
	if draft == nil || draft.ownerID != ownerID || draft.state == draftExporting {
		return clipDraft{}, draftGone()
	}
	if draft.state != draftReady {
		return *draft, errDraftMoved
	}

	draft.state = draftPublishing

	return *draft, nil
}

// errDraftMoved is a draft that is no longer ready by the time a publish
// came to take it.
var errDraftMoved = errors.New("the draft is no longer ready")

// errDraftTakenAway is a publish given up because its draft's context ended
// under it: a wipe of its stream, or the plugin stopping. The handler answers
// with what the draft became (endPublish), or, when it became nothing, as a
// busy server, to be tried again.
var errDraftTakenAway = refuseClip(http.StatusServiceUnavailable, clipErrBusy)

// endPublish is the end of a publish: the draft is a published tombstone, or,
// when it failed, ready again to play and to publish. A draft taken away
// while its publish ran (discard) becomes that tombstone instead, and is
// answered with it, for its files to be deleted and the publish to be told.
func (ds *clipDrafts) endPublish(id string, clipID string, now time.Time) (clipDraft, bool) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	draft := ds.byID[id]
	if draft == nil || draft.state != draftPublishing {
		return clipDraft{}, false
	}

	if clipID != "" {
		draft.clipID = clipID
		draft.gone(draftPublished, now)

		return clipDraft{}, false
	}

	if draft.discardTo != "" {
		draft.gone(draft.discardTo, now)
		return *draft, true
	}

	draft.state = draftReady

	return clipDraft{}, false
}

// POST /api/v1/clips/draft/publish?id= {"title","inMs","outMs"}: make a draft
// a clip, in this order (spec §2.10.2, A§2.1, A§3.4):
//
//  0. Past the gate, the attempt is metered (tooManyClipAttempts).
//  1. The request, past the gate: an unlinked caller is never told whether
//     the draft is there, let alone handed its clip.
//  2. The draft: the caller's, and ready; one already published is
//     answered with its clip, so a retry after a lost answer finds what the
//     first try made.
//  3. Clips switched on; the document read afresh; the caller not blocked.
//  4. The title, cleaned failing closed (cleanClipTitle).
//  5. The range, from the draft's own sample table; then a duplicate of a
//     clip already theirs, their 30 and everybody's 500, and room on the
//     disk for it, within their share.
//  6. Only now a token, and the draft is taken to be published: a second
//     publish of it from here on is refused, or answered with the first's
//     clip.
//  7. An export slot; the trim, or for the whole of the draft a rename; its
//     picture moved; the clip written into the document with everything the
//     draft captured, its tag included.
//  8. Anything that fails in 7 undoes what this request did, and only that,
//     and the draft is ready to play and publish again.
func (p *Plugin) handleDraftPublish(w http.ResponseWriter, r *http.Request, userID, key string) {
	s := p.clips()
	now := s.now()

	if p.tooManyClipAttempts(w, userID, now) {
		return
	}

	draftID := r.URL.Query().Get("id")
	request, valid := readPublishRequest(r)
	if !validClipID(draftID) || !valid {
		p.writeClipError(w, http.StatusBadRequest, clipErrBadRequest)
		return
	}
	inMs, outMs := *request.InMs, *request.OutMs

	draft, found := s.drafts.find(draftID, userID)
	if !found {
		p.writeClipRefusal(w, draftGone())
		return
	}
	if p.answerSettledDraft(w, draft, userID, key, now) {
		return
	}

	cfg := p.getConfiguration()
	if err := p.mayMakeClips(cfg); err != nil {
		p.writeClipRefusal(w, err)
		return
	}

	doc, err := p.readClipsFresh(now)
	if err != nil {
		p.writeClipRefusal(w, err)
		return
	}
	if _, blocked := doc.Blocked[userID]; blocked {
		p.writeClipError(w, http.StatusForbidden, clipErrBlocked)
		return
	}

	title, err := p.cleanClipTitle(userID, key, draft.capture.StreamKey, request.Title)
	if err != nil {
		p.writeClipRefusal(w, err)
		return
	}

	// Looked at again, not taken from mayMakeClips: a second activation can
	// set the storage again in between, and no path is built from no root.
	root := s.writableRoot()
	if root == "" {
		p.writeClipError(w, http.StatusServiceUnavailable, clipErrStorageUnavailable)
		return
	}

	plan, err := draftTrimPlan(draftFile(root, draftID, clipVideoExt), inMs, outMs)
	if err != nil {
		if refusal := rangeRefusal(err); refusal != nil {
			p.writeClipRefusal(w, refusal)
			return
		}

		// The draft went while its range was being read, or it cannot be
		// read at all.
		again, still := s.drafts.find(draftID, userID)
		switch {
		case !still:
			p.writeClipRefusal(w, draftGone())
		case p.answerSettledDraft(w, again, userID, key, now):
		default:
			p.API.LogWarn("Could not read a clip draft to publish it", "user_id", userID, "draft_id", draftID, "error", err.Error())
			p.writeClipError(w, http.StatusServiceUnavailable, clipErrStore)
		}

		return
	}

	entry := clipEntry{
		ID:           draftID,
		ClipperID:    userID,
		StreamerID:   draft.capture.StreamerID,
		StreamerLink: draft.capture.StreamerLink,
		StreamName:   draft.capture.StreamName,
		StreamNote:   draft.capture.StreamNote,
		Tag:          draft.capture.Tag.shown(),
		Handle:       draft.capture.Handle,
		StreamTag:    draft.capture.StreamTag,
		Title:        title,
		StartAt:      draft.export.startAt + inMs,
		DurationMs:   plan.durationMs,
		LeadMs:       plan.leadMs,
		Width:        draft.export.width,
		Height:       draft.export.height,
		Codecs:       draft.export.codecs,
	}

	if doc.clip(draftID) == nil {
		if doc.duplicateOf(&entry) != nil {
			p.writeClipError(w, http.StatusConflict, clipErrDuplicate)
			return
		}

		mine := 0
		for i := range doc.Clips {
			if doc.Clips[i].ClipperID == userID {
				mine++
			}
		}
		if len(doc.Clips) >= maxClips || mine >= maxClipsPerUser {
			p.writeClipError(w, http.StatusConflict, clipErrClipCap)
			return
		}
	}

	estimate := int64(0)
	if draft.export.durationMs > 0 {
		estimate = draft.export.bytes * max(0, outMs-plan.startMs) / draft.export.durationMs
	}
	if err := s.roomFor(userID, estimate, cfg); err != nil {
		p.writeClipRefusal(w, err)
		return
	}

	if !s.publishLimiter.allowAt(userID, now) {
		writeClipTooMany(w, s.publishLimiter, userID, now)
		return
	}

	draft, err = s.drafts.beginPublish(draftID, userID)
	if err != nil {
		s.publishLimiter.refund(userID)
		if errors.Is(err, errDraftMoved) {
			p.answerSettledDraft(w, draft, userID, key, now)
		} else {
			p.writeClipRefusal(w, err)
		}

		return
	}

	published, err := p.publishDraft(r, draft, entry, inMs, outMs, now)
	if err != nil {
		refundClipToken(s.publishLimiter, userID, err)

		// Taken away while it was published, by a block, Clips going off or
		// a wipe of its stream (which also stopped the publish): it is that
		// tombstone now, its files go, and the answer is what a retry would
		// be told.
		if gone, discarded := s.drafts.endPublish(draftID, "", now); discarded {
			s.removeDraftFiles(draftID)
			err = gone.refusal()
		}
		p.writeClipRefusal(w, err)

		return
	}

	s.drafts.endPublish(draftID, draftID, now)

	// What is left of the draft — its video after a trim, and a picture that
	// was not moved — goes only now it is a published tombstone, so that
	// while it was being published its video played from where it always
	// had (openDraftVideo).
	s.removeDraftFiles(draftID)

	p.API.LogInfo("A clip was published", "user_id", userID, "clip_id", draftID, "stream_handle", entry.Handle, "duration_ms", published.entry.DurationMs, "bytes", published.entry.Bytes, "lead_ms", published.entry.LeadMs)

	nowMS := now.UnixMilli()
	reading := &clipReading{
		doc:    published.doc,
		now:    nowMS,
		kept:   effectiveRetention(published.doc.Retention, cfg.clipRetentionDays(), nowMS),
		counts: s.verdictsFor(clipPeopleOf(published.doc), published.doc.Blocked).eligible,
		names:  p.clipNamesNow(now),
	}
	writeJSON(w, http.StatusCreated, reading.viewOf(published.entry, p.clipViewerOf(userID, key, now)))
}

// publishedClip is what a publish made: the clip as written, and the document
// it was written into.
type publishedClip struct {
	entry *clipEntry
	doc   *clipsDoc
}

// publishDraft is the part of a publish that changes anything (steps 12–16):
// a slot, the clip's files and the write. Whatever fails undoes what this
// call did, and the caller puts the draft back to ready. What is left of the
// draft once it is a clip is the caller's to delete, after the draft is
// marked published.
func (p *Plugin) publishDraft(r *http.Request, draft clipDraft, entry clipEntry, inMs, outMs int64, now time.Time) (publishedClip, error) {
	s := p.clips()

	root := s.writableRoot()
	if root == "" {
		return publishedClip{}, refuseClip(http.StatusServiceUnavailable, clipErrStorageUnavailable)
	}

	waitCtx, stop := withRequest(draft.ctx, r)
	release, err := acquireExportSlot(waitCtx)
	stop()
	if err != nil {
		return publishedClip{}, refuseClip(http.StatusServiceUnavailable, clipErrBusy)
	}
	defer release()

	// Taken away while it waited for a slot: a wipe of its stream cancels its
	// context (clipDrafts.wipe), and the plugin stopping does too.
	if draft.ctx.Err() != nil {
		return publishedClip{}, errDraftTakenAway
	}

	var done publishing
	clipsDir := filepath.Join(root, clipsFilesDir)
	clipVideo := filepath.Join(clipsDir, draft.id+clipVideoExt)

	trim, err := trimClip(draft.ctx, draftFile(root, draft.id, clipVideoExt), clipsDir, draft.id, inMs, outMs)
	if err != nil {
		if refusal := rangeRefusal(err); refusal != nil {
			return publishedClip{}, refusal
		}
		if draft.ctx.Err() != nil {
			return publishedClip{}, errDraftTakenAway
		}
		p.API.LogWarn("Could not cut a clip from its draft", "user_id", draft.ownerID, "draft_id", draft.id, "error", err.Error())

		return publishedClip{}, errStore
	}

	videoBytes := trim.bytes
	if trim.rename {
		if err := os.Rename(draftFile(root, draft.id, clipVideoExt), clipVideo); err != nil {
			p.API.LogWarn("Could not move a clip draft into place", "user_id", draft.ownerID, "draft_id", draft.id, "error", err.Error())
			return publishedClip{}, errStore
		}
		done.renamed = true
	} else {
		done.trimmed = true
	}

	thumb, thumbBytes := "", int64(0)
	if draft.thumb != "" {
		if err := os.Rename(draftFile(root, draft.id, clipThumbExt), filepath.Join(clipsDir, draft.id+clipThumbExt)); err == nil {
			done.thumbMoved = true
			thumb, thumbBytes = draft.thumb, draft.thumbBytes
		} else if !errors.Is(err, fs.ErrNotExist) {
			done.undo(root, draft.id)
			p.API.LogWarn("Could not move a clip picture into place", "user_id", draft.ownerID, "draft_id", draft.id, "error", err.Error())

			return publishedClip{}, errStore
		}
	}

	entry.CreatedAt = now.UnixMilli()
	entry.LeadMs = trim.leadMs
	entry.DurationMs = trim.durationMs
	entry.StartAt = draft.export.startAt + trim.inMs
	entry.Bytes = videoBytes
	entry.Thumb = thumb

	// Looked at for the last time inside the write itself, after every
	// lookup the write makes first (a GetUser and a KVGet for each person it
	// has not seen, A§3.4), so that only the store's own compare-and-set is
	// left between a wipe and a clip of what it wiped: "if its CAS already
	// succeeded, it is a published clip and stays" (§2.10.6), and not
	// otherwise.
	doc, err := p.clipsCAS(now, func(doc *clipsDoc, _ *clipsWrite) (bool, error) {
		if draft.ctx.Err() != nil {
			return false, errDraftTakenAway
		}

		return doc.addClip(entry)
	}, entry.ClipperID)
	if err != nil {
		done.undo(root, draft.id)

		return publishedClip{}, err
	}

	written := doc.clip(draft.id)
	if written == nil {
		// Written and gone again in the same breath: only a document
		// replaced behind this node's back could do that.
		done.undo(root, draft.id)
		return publishedClip{}, errStore
	}

	// The room the clip takes is its clipper's now. What it took as a draft
	// stops counting under nobody: what moved into clips/ here, and the rest
	// when the caller deletes it.
	moved := thumbBytes
	if done.renamed {
		moved += videoBytes
	}
	s.addUsage("", -moved)
	s.addUsage(entry.ClipperID, videoBytes+thumbBytes)

	return publishedClip{entry: written, doc: doc}, nil
}

// clipsTurnedOff is what switching Clips off does at once, without waiting
// for a poll (spec §2.14): every recorder stops, and what they held goes,
// tails included; and every draft goes, since it could not be published now
// anyway — those still being cut too, their exports cancelled
// (forgetClipDrafts). Published clips stay, to be watched until they expire.
// Who is watching what is kept, as it is kept while Clips is off, so that
// switching it back on finds them watching (see the viewer registry).
//
// OnConfigurationChange calls this after storing the setting, which is what
// lets a click that slips past it be caught (clickStillStands).
func (p *Plugin) clipsTurnedOff(now time.Time) {
	p.recorders().stopAll(true, whyClipsOff, now)
}

// forgetClipDrafts is the drafts' part of Clips going off: every draft goes, a
// publish already under way excepted, which is left to finish and takes its
// draft with it if it fails (clipDrafts.discard). From memory, and the
// drafts' own files. The viewer registry stays: a panel already playing never
// tells the server again that it is, so forgetting it would refuse everybody
// watching once Clips is back on.
func (p *Plugin) forgetClipDrafts(now time.Time) {
	p.discardDrafts(func(*clipDraft) bool { return true }, draftExpired, now)
}

// housekeepClips is the minute's housekeeping on this node (spec §2.13): what
// only this node can do, because only it holds its drafts and its viewers.
//
//  1. Drafts whose two hours are up are expired, their files deleted; one
//     being published is left to finish.
//  2. Tombstones are forgotten draftTombstone after they were made.
//  3. drafts/ is swept of whatever no draft accounts for.
//  4. Sessions not heard of for a quarter of an hour are forgotten.
//  5. The recordings' own: tails past their time, the ring budget, free
//     space, the recorders' own sessions, and their health for the admins'
//     alert (clipRecorders.housekeep).
func (p *Plugin) housekeepClips(now time.Time) {
	s := p.clips()

	p.expireDrafts(now)
	s.drafts.pruneTombstones(now)
	p.sweepDraftsDir(now)
	s.viewers.prune(now)
	p.recorders().housekeep(now)
}

// sweepDraftsDir deletes from drafts/ what no draft accounts for: the video
// or picture of a draft that is no longer held, and a write that never
// finished, once each is draftOrphanAge old, so a file whose draft is being
// made as the sweep looks is never mistaken for one. Files it does not
// recognise are not its to delete.
func (p *Plugin) sweepDraftsDir(now time.Time) {
	s := p.clips()

	root := s.writableRoot()
	if root == "" {
		return
	}

	dir := filepath.Join(root, clipsDraftsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	held := s.drafts.placeHolders()
	removed, freed := 0, int64(0)
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}

		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) < draftOrphanAge {
			continue
		}

		name := entry.Name()
		ext := filepath.Ext(name)
		switch {
		case strings.HasPrefix(name, clipTempPrefix):
		case ext == clipVideoExt || ext == clipThumbExt:
			id := strings.TrimSuffix(name, ext)
			if !validClipID(id) || held[id] {
				continue
			}
		default:
			continue
		}

		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			continue
		}
		removed++
		freed += info.Size()
	}

	if removed > 0 {
		s.addUsage("", -freed)
		p.API.LogInfo("Stray clip draft files were removed", "files", removed, "bytes", freed)
	}
}

// everyClipsInterval runs run every interval until the job is closed, which
// waits for a run under way to finish.
func everyClipsInterval(interval time.Duration, run func()) clipsJob {
	job := &tickerJob{ticker: time.NewTicker(interval), stop: make(chan struct{}), done: make(chan struct{})}

	go func() {
		defer close(job.done)

		for {
			select {
			case <-job.ticker.C:
				run()
			case <-job.stop:
				return
			}
		}
	}()

	return job
}

type tickerJob struct {
	ticker *time.Ticker
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
}

func (j *tickerJob) Close() error {
	j.once.Do(func() {
		j.ticker.Stop()
		close(j.stop)
	})
	<-j.done

	return nil
}
