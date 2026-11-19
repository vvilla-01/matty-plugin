package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Who may make and see drafts: their owner alone, within the caps and the
// limiters, never somebody blocked, never on a document that cannot be read,
// and only of a stream they are watching or is theirs.

// A draft is its owner's alone. Anybody else — a linked admin included — is
// told it is not there, exactly as for a draft that never was, whatever they
// ask of it, and nothing they ask changes it (spec §2.19 item 20).
func TestDraftsArePrivate(t *testing.T) {
	d := newDraftsWorld(t)
	draft := d.makeDraft(t, "uid-bob")
	require.Equal(t, http.StatusOK, d.putThumb(t, "uid-bob", draft.ID, draftJPEG(t, 60)).Code)
	picture := draftJPEG(t, 200)

	for _, caller := range []string{"uid-dan", "uid-boss"} {
		require.Empty(t, d.drafts(t, caller), caller)

		var first []byte
		for _, id := range []string{draft.ID, "00000000000000ab"} {
			for _, c := range []clipCall{
				{method: http.MethodGet, target: clipsPath + "/draft/video?id=" + id, header: map[string]string{"Range": "bytes=0-99"}},
				{method: http.MethodHead, target: clipsPath + "/draft/video?id=" + id},
				{method: http.MethodPut, target: clipsPath + "/draft/thumb?id=" + id, body: string(picture)},
				{method: http.MethodPost, target: clipsPath + "/draft/publish?id=" + id, body: publishBody("mine now", 0, 10000)},
				{method: http.MethodDelete, target: clipsPath + "/draft?id=" + id},
			} {
				about := caller + " " + describe(c)
				response := d.call(t, caller, c)
				requireClipError(t, response, http.StatusNotFound, clipErrDraftGone, about)

				if first == nil {
					first = response.Body.Bytes()
				}
				require.True(t, bytes.Equal(first, response.Body.Bytes()), about)
			}
		}
	}

	listed := d.drafts(t, "uid-bob")
	require.Len(t, listed, 1)
	require.Equal(t, draftReady, listed[0].State)
	require.True(t, listed[0].HasThumb)
	require.True(t, exists(d.draftPath(draft.ID, clipVideoExt)))
	thumb, err := readClipThumb(d.draftPath(draft.ID, clipThumbExt))
	require.NoError(t, err)
	require.Equal(t, draftJPEG(t, 60), thumb, "nobody else's picture went on it")
	require.Empty(t, d.stored(t).Clips)

	response := d.draftVideo(t, "uid-bob", draft.ID)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "video/mp4", response.Header().Get("Content-Type"))
	require.Equal(t, "private, no-cache", response.Header().Get("Cache-Control"))
	require.Equal(t, draft.Bytes, int64(response.Body.Len()))
}

// Three drafts each and twenty in all, held from the moment an export starts
// and given back when one is thrown away; five clicks in ten minutes, and ten
// publishes an hour. A cap is checked before a token is spent (spec §2.19
// item 20).
func TestDraftCapsAndRateLimits(t *testing.T) {
	d := newDraftsWorld(t)
	capture := d.p.clips().captureLimiter

	first := d.makeDraft(t, "uid-bob")
	second := d.makeDraft(t, "uid-bob")
	third := d.makeDraft(t, "uid-bob")
	requireClipError(t, d.cut(t, "uid-bob", d.dave, clipSourceLive), http.StatusConflict, clipErrDraftCap, "a fourth")
	tokens, _ := tokensLeft(capture, "uid-bob")
	require.Equal(t, float64(2), tokens, "the cap is checked before a token is spent")

	// Throwing one away makes room; doing it again changes nothing.
	for range 2 {
		response := d.discard(t, "uid-bob", first.ID)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.JSONEq(t, `{"ok":true}`, response.Body.String())
	}
	require.False(t, exists(d.draftPath(first.ID, clipVideoExt)))
	requireClipError(t, d.draftVideo(t, "uid-bob", first.ID), http.StatusGone, clipErrDraftGone, "thrown away")
	fourth := d.makeDraft(t, "uid-bob")

	require.Equal(t, http.StatusOK, d.discard(t, "uid-bob", second.ID).Code)
	d.makeDraft(t, "uid-bob")

	// Five clicks are all ten minutes give: a place is free, a token is not.
	require.Equal(t, http.StatusOK, d.discard(t, "uid-bob", third.ID).Code)
	requireClipError(t, d.cut(t, "uid-bob", d.dave, clipSourceLive), http.StatusTooManyRequests, clipErrRateLimited, "a sixth click")
	require.Len(t, d.drafts(t, "uid-bob"), 2, "the place it held is given back")

	// Two minutes give one back.
	d.at(t, 180*time.Second)
	d.watch("sess-bob-2", "uid-bob", d.dave)
	d.makeDraft(t, "uid-bob")

	// Twenty in all, across everybody: Bob's three, and seventeen more.
	for i, userID := range []string{"uid-v1", "uid-v2", "uid-v3", "uid-v4", "uid-v5", "uid-v6"} {
		d.person(userID)
		d.link(userID, keyOf(userID))
		d.watch("sess-"+userID, userID, d.dave)

		for n := range 3 {
			if i*3+n == 17 {
				break
			}
			d.makeDraft(t, userID)
		}
	}
	require.Equal(t, maxDraftsTotal, d.p.clips().drafts.counted())
	requireClipError(t, d.cut(t, "uid-v6", d.dave, clipSourceLive), http.StatusConflict, clipErrDraftCap, "the twenty-first")
	d.watch("sess-dan", "uid-dan", d.dave)
	requireClipError(t, d.cut(t, "uid-dan", d.dave, clipSourceLive), http.StatusConflict, clipErrDraftCap, "nobody's first, past twenty")

	// Ten publishes an hour: the eleventh is refused, and the draft stays.
	publishes := d.p.clips().publishLimiter
	for range 10 {
		require.True(t, publishes.allowAt("uid-bob", d.now()))
	}
	requireClipError(t, d.publish(t, "uid-bob", fourth.ID, publishBody("", 0, 10000)), http.StatusTooManyRequests, clipErrRateLimited, "an eleventh publish")
	require.Contains(t, draftIDs(d.drafts(t, "uid-bob")), fourth.ID)
	require.Empty(t, d.stored(t).Clips)
}

// unreadableSegment keeps nothing: a recording whose disk has gone, so that an
// export of it fails part way.
type unreadableSegment struct{}

func (unreadableSegment) WriteAt(p []byte, _ int64) (int, error) { return len(p), nil }
func (unreadableSegment) ReadAt([]byte, int64) (int, error) {
	return 0, errors.New("the disk went away")
}
func (unreadableSegment) Close() error { return nil }

// A click turned away for something that was not the caller's doing — the
// stream only just started, no export slot freed, the recording could not be
// read — costs them nothing: the token comes back, and so does the place the
// export held. A publish turned away because no slot freed puts the draft
// back and gives the token back too (spec §2.19 item 20).
func TestLimiterRefundedOnWarmingUpAndBusy(t *testing.T) {
	d := newDraftsWorld(t)
	capture := d.p.clips().captureLimiter
	d.goLive(apiStream{StreamKey: "videokey-dan", Title: "Dan", UserID: "uid-dan", Username: "dan", LinkSource: "manual"})
	dan := d.p.streamHandle("videokey-dan")

	// A recording still waiting for its first keyframe: told so, before any
	// token is spent.
	d.rec.setLive(dan, fakeLive{state: clipStateStarting})
	requireClipError(t, d.cut(t, "uid-dan", dan, clipSourceLive), http.StatusConflict, clipErrWarmingUp, "starting")
	_, spent := tokensLeft(capture, "uid-dan")
	require.False(t, spent)

	for state, code := range map[string]clipErrorCode{
		clipStateFailed:      clipErrRecordingFailed,
		clipStateFull:        clipErrRecordingFailed,
		clipStateQueued:      clipErrRecordingFailed,
		clipStateUnsupported: clipErrNotClippable,
		clipStateOff:         clipErrNotClippable,
		"":                   clipErrNotClippable,
	} {
		d.rec.setLive(dan, fakeLive{state: state})
		response := d.cut(t, "uid-dan", dan, clipSourceLive)
		require.Equal(t, code, clipErrorCode(decodeClipErrorCode(t, response)), state)
	}
	_, spent = tokensLeft(capture, "uid-dan")
	require.False(t, spent)

	// Recording, but for under five seconds: the export says so, and the
	// token comes back.
	young := newMediaTestPipe(t, draftsClipLength)
	youngStream := newMediaFixtureStream(t, young)
	youngStream.start = d.stream.start + int64(56*time.Second)
	youngStream.play(4 * time.Second)
	d.rec.setLive(dan, fakeLive{state: clipStateReady, tp: young})
	requireClipError(t, d.cut(t, "uid-dan", dan, clipSourceLive), http.StatusConflict, clipErrWarmingUp, "four seconds in")
	tokens, spent := tokensLeft(capture, "uid-dan")
	require.True(t, spent)
	require.Equal(t, float64(5), tokens)
	require.Empty(t, d.drafts(t, "uid-dan"))

	// Every slot busy until the asker gives up: busy, and refunded.
	releaseFirst, err := acquireExportSlot(context.Background())
	require.NoError(t, err)
	releaseSecond, err := acquireExportSlot(context.Background())
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- d.callWith(ctx, "uid-bob", clipCall{method: http.MethodPost, target: clipsPath + "/drafts", body: `{"handle":"` + d.dave + `","source":"live"}`})
	}()
	mediaWaitPinned(t, d.tp.live())
	cancel()
	requireClipError(t, <-done, http.StatusServiceUnavailable, clipErrBusy, "no slot")
	tokens, _ = tokensLeft(capture, "uid-bob")
	require.Equal(t, float64(5), tokens)
	releaseFirst()
	releaseSecond()

	// A recording that cannot be read back: the export fails, nothing is
	// left behind, and the token comes back.
	gone := newMediaTestPipe(t, draftsClipLength, func(cfg *pipelineConfig) {
		cfg.openSegment = func(string) (segmentFile, error) { return unreadableSegment{}, nil }
	})
	goneStream := newMediaFixtureStream(t, gone)
	goneStream.play(60 * time.Second)
	d.rec.setLive(dan, fakeLive{state: clipStateReady, tp: gone})
	requireClipError(t, d.cut(t, "uid-dan", dan, clipSourceLive), http.StatusServiceUnavailable, clipErrRecordingFailed, "unreadable")
	tokens, _ = tokensLeft(capture, "uid-dan")
	require.Equal(t, float64(5), tokens)
	require.Empty(t, namesIn(t, filepath.Join(d.h.root, clipsDraftsDir)))
	require.Len(t, logged(d.api, "LogWarn", "Could not cut a clip draft"), 1)

	// Every place held by those is free again: Bob can make three.
	draft := d.makeDraft(t, "uid-bob")
	d.makeDraft(t, "uid-bob")
	d.makeDraft(t, "uid-bob")

	// A publish that no slot frees for before its asker gives up.
	releaseFirst, err = acquireExportSlot(context.Background())
	require.NoError(t, err)
	releaseSecond, err = acquireExportSlot(context.Background())
	require.NoError(t, err)

	gaveUp, stop := context.WithCancel(context.Background())
	stop()
	response := d.callWith(gaveUp, "uid-bob", clipCall{method: http.MethodPost, target: clipsPath + "/draft/publish?id=" + draft.ID, body: publishBody("", 0, 10000)})
	requireClipError(t, response, http.StatusServiceUnavailable, clipErrBusy, "no slot for the publish")
	tokens, _ = tokensLeft(d.p.clips().publishLimiter, "uid-bob")
	require.Equal(t, float64(10), tokens)
	for _, listed := range d.drafts(t, "uid-bob") {
		require.Equal(t, draftReady, listed.State, "it is ready again")
	}
	require.False(t, exists(d.clipPath(draft.ID, clipVideoExt)))
	releaseFirst()
	releaseSecond()

	decodeClipView(t, d.publish(t, "uid-bob", draft.ID, publishBody("", 0, 10000)), http.StatusCreated)
}

// decodeClipErrorCode is the code of any clip refusal.
func decodeClipErrorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()

	var answer clipErrorResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer), response.Body.String())

	return string(answer.Code)
}

// Somebody blocked can neither cut nor publish: blocking them throws away
// their drafts on this node, and a block made on another node is found by the
// fresh read every draft and publish makes. Unblocked, they can again (spec
// §2.19 item 20, §2.10.4).
func TestBlockedUsersCannotDraftOrPublish(t *testing.T) {
	d := newDraftsWorld(t)
	draft := d.makeDraft(t, "uid-bob")
	require.Equal(t, http.StatusOK, d.putThumb(t, "uid-bob", draft.ID, draftJPEG(t, 10)).Code)
	d.watch("sess-dan", "uid-dan", d.dave)
	dans := d.makeDraft(t, "uid-dan")

	response := d.call(t, "uid-boss", clipCall{method: http.MethodPost, target: adminClipsPath + "/block", body: `{"userId":"uid-bob"}`})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"ok":true,"draftsRemoved":1}`, response.Body.String())
	require.False(t, exists(d.draftPath(draft.ID, clipVideoExt)))
	require.False(t, exists(d.draftPath(draft.ID, clipThumbExt)))
	used, _ := d.p.clips().usage("")
	require.Equal(t, dans.Bytes, used)

	tokens, _ := tokensLeft(d.p.clips().captureLimiter, "uid-bob")
	requireClipError(t, d.publish(t, "uid-bob", draft.ID, publishBody("", 0, 10000)), http.StatusForbidden, clipErrBlocked, "publish")
	requireClipError(t, d.draftVideo(t, "uid-bob", draft.ID), http.StatusForbidden, clipErrBlocked, "video")
	requireClipError(t, d.cut(t, "uid-bob", d.dave, clipSourceLive), http.StatusForbidden, clipErrBlocked, "a new draft")
	left, _ := tokensLeft(d.p.clips().captureLimiter, "uid-bob")
	require.Equal(t, tokens, left)
	require.Empty(t, d.drafts(t, "uid-bob"))
	require.Equal(t, http.StatusOK, d.discard(t, "uid-bob", draft.ID).Code, "it is gone already")

	// Blocked on another node, behind this one's back.
	d.changeStored(t, func(doc *clipsDoc) {
		doc.Blocked["uid-dan"] = clipMark{By: "uid-boss", At: d.now().UnixMilli()}
	})
	requireClipError(t, d.publish(t, "uid-dan", dans.ID, publishBody("", 0, 10000)), http.StatusForbidden, clipErrBlocked, "Dan's publish")
	requireClipError(t, d.cut(t, "uid-dan", d.dave, clipSourceLive), http.StatusForbidden, clipErrBlocked, "Dan's new draft")
	_, spent := tokensLeft(d.p.clips().publishLimiter, "uid-dan")
	require.False(t, spent)
	require.Equal(t, []string{dans.ID}, draftIDs(d.drafts(t, "uid-dan")), "this node threw nothing away")

	response = d.call(t, "uid-boss", clipCall{method: http.MethodDelete, target: adminClipsPath + "/block?userId=uid-dan"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	decodeClipView(t, d.publish(t, "uid-dan", dans.ID, publishBody("", 0, 10000)), http.StatusCreated)

	block := clipCall{method: http.MethodPost, target: adminClipsPath + "/block", body: `{"userId":"uid-bob"}`}

	// A click whose export is waiting for a slot when its clipper is
	// blocked: its place is taken and counted, its export cancelled, and it
	// is told why, at no cost.
	d = newDraftsWorld(t)
	release := holdSlots(t)
	waiting := d.clickWaiting(t, "uid-bob")
	response = d.call(t, "uid-boss", block)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"ok":true,"draftsRemoved":1}`, response.Body.String())
	requireClipError(t, answerOf(t, waiting), http.StatusForbidden, clipErrBlocked, "cut as its clipper was blocked")
	release()

	tokens, _ = tokensLeft(d.p.clips().captureLimiter, "uid-bob")
	require.Equal(t, float64(5), tokens, "refunded")
	require.Zero(t, d.p.clips().drafts.counted())
	require.Empty(t, namesIn(t, filepath.Join(d.h.root, clipsDraftsDir)))
	used, _ = d.p.clips().usage("")
	require.Zero(t, used)

	// A click that read the document just before the block, and holds its
	// place just after.
	d = newDraftsWorld(t)
	d.rec.beforeNextLive(func() {
		response := d.call(t, "uid-boss", block)
		require.JSONEq(t, `{"ok":true,"draftsRemoved":0}`, response.Body.String())
	})
	requireClipError(t, d.cut(t, "uid-bob", d.dave, clipSourceLive), http.StatusForbidden, clipErrBlocked, "a click that slipped past the block")
	_, spent = tokensLeft(d.p.clips().captureLimiter, "uid-bob")
	require.False(t, spent)
	require.Zero(t, d.p.clips().drafts.counted())
	require.Empty(t, namesIn(t, filepath.Join(d.h.root, clipsDraftsDir)))

	// Two publishes waiting for a slot when their clipper is blocked: the
	// block was checked when they began, so the one that gets its slot is a
	// clip; the one whose asker gives up takes its draft with it.
	d = newDraftsWorld(t)
	lands := d.makeDraft(t, "uid-bob")
	fails := d.makeDraft(t, "uid-bob")
	release = holdSlots(t)
	landing := d.publishWaiting(t, context.Background(), "uid-bob", lands.ID, publishBody("", 0, 10000))
	gaveUp, giveUp := context.WithCancel(context.Background())
	failing := d.publishWaiting(t, gaveUp, "uid-bob", fails.ID, publishBody("", 0, 10000))

	response = d.call(t, "uid-boss", block)
	require.JSONEq(t, `{"ok":true,"draftsRemoved":0}`, response.Body.String(), "neither is gone yet")
	giveUp()
	requireClipError(t, answerOf(t, failing), http.StatusForbidden, clipErrBlocked, "a publish that failed after the block")
	release()
	decodeClipView(t, answerOf(t, landing), http.StatusCreated)

	require.False(t, exists(d.draftPath(fails.ID, clipVideoExt)))
	require.False(t, exists(d.clipPath(fails.ID, clipVideoExt)))
	requireClipError(t, d.draftVideo(t, "uid-bob", fails.ID), http.StatusForbidden, clipErrBlocked, "gone with the block")
	require.Equal(t, []string{lands.ID}, draftIDs(d.drafts(t, "uid-bob")))
	used, mine := d.p.clips().usage("uid-bob")
	require.Equal(t, mine, used, "nothing of either draft is counted any more")
}

// Who is blocked and which streams are switched off are read afresh before
// every draft and publish, and a document that cannot be read refuses both:
// a store that does not answer, and one that will not decode, which halts
// clips and is never written over (spec §2.19 item 20, A§2.1).
func TestUnreadableDocFailsClosed(t *testing.T) {
	d := newDraftsWorld(t)
	draft := d.makeDraft(t, "uid-bob")
	tokens, _ := tokensLeft(d.p.clips().captureLimiter, "uid-bob")

	d.faults.failRead(kvClips)
	requireClipError(t, d.cut(t, "uid-bob", d.dave, clipSourceLive), http.StatusServiceUnavailable, clipErrStore, "a draft, unread")
	requireClipError(t, d.publish(t, "uid-bob", draft.ID, publishBody("", 0, 10000)), http.StatusServiceUnavailable, clipErrStore, "a publish, unread")
	d.faults.healRead(kvClips)

	left, _ := tokensLeft(d.p.clips().captureLimiter, "uid-bob")
	require.Equal(t, tokens, left)
	_, spent := tokensLeft(d.p.clips().publishLimiter, "uid-bob")
	require.False(t, spent)
	require.Equal(t, []string{draft.ID + clipVideoExt}, namesIn(t, filepath.Join(d.h.root, clipsDraftsDir)))
	require.Empty(t, namesIn(t, filepath.Join(d.h.root, clipsFilesDir)))

	broken := []byte(`{"v":1,"rev":`)
	d.kvSet(kvClips, broken)
	requireClipError(t, d.cut(t, "uid-bob", d.dave, clipSourceLive), http.StatusServiceUnavailable, clipErrStore, "a draft, halted")
	requireClipError(t, d.publish(t, "uid-bob", draft.ID, publishBody("", 0, 10000)), http.StatusServiceUnavailable, clipErrStore, "a publish, halted")
	require.Equal(t, broken, d.kvGet(kvClips))
	require.Equal(t, draftReady, d.drafts(t, "uid-bob")[0].State)
	require.Empty(t, namesIn(t, filepath.Join(d.h.root, clipsFilesDir)))
}

// The watch gate (spec §2.4.9, A§3.4). Somebody linked may cut a draft of a
// stream they are watching in VillaNET: with a session the latest poll
// listed, or one they opened in the last minute. A peek is nobody watching.
// The stream's streamer needs no session, and is whoever's link is to its key
// now, whatever case the key was typed in. Somebody not linked is refused
// first, watching or not. The held end of a stream may be clipped by whoever
// was watching when it ended, and by its streamer (spec §2.19 item 21).
func TestWatchGate(t *testing.T) {
	d := newDraftsWorld(t)
	for _, userID := range []string{"uid-v1", "uid-v2", "uid-v3"} {
		d.person(userID)
		d.link(userID, keyOf(userID))
	}

	// Not watching.
	requireClipError(t, d.cut(t, "uid-dan", d.dave, clipSourceLive), http.StatusForbidden, clipErrNotWatching, "not watching")
	_, spent := tokensLeft(d.p.clips().captureLimiter, "uid-dan")
	require.False(t, spent)

	// Opened half a minute ago, before any poll could list it.
	d.p.noteClipViewer("sess-dan", "uid-dan", d.dave, d.now().Add(-30*time.Second))
	d.makeDraft(t, "uid-dan")

	// Opened a minute and a second ago, and listed by no poll.
	d.p.noteClipViewer("sess-v1", "uid-v1", d.dave, d.now().Add(-61*time.Second))
	requireClipError(t, d.cut(t, "uid-v1", d.dave, clipSourceLive), http.StatusForbidden, clipErrNotWatching, "unlisted after a minute")

	// The latest poll lists it.
	d.p.noteClipSessions(d.listing(map[string][]string{d.dave: {"sess-v1", "sess-bob"}}), d.now())
	d.makeDraft(t, "uid-v1")

	// A session on another stream is not one on this.
	d.goLive(apiStream{StreamKey: "videokey-erin", Title: "Erin"})
	erin := d.p.streamHandle("videokey-erin")
	d.p.noteClipViewer("sess-v2", "uid-v2", erin, d.now())
	requireClipError(t, d.cut(t, "uid-v2", d.dave, clipSourceLive), http.StatusForbidden, clipErrNotWatching, "watching another stream")

	// The streamer, by their link now, without a session; however the key's
	// case was typed when they linked.
	d.makeDraft(t, "uid-dave")
	d.link("uid-dave", "VideoKey-Dave")
	d.makeDraft(t, "uid-dave")

	// Attribution by username is not a link: V3 is named on the stream, and
	// has linked a stream of their own.
	d.goLive(apiStream{StreamKey: "videokey-dave", Title: "V3", UserID: "uid-v3", Username: "v3", LinkSource: "username"})
	requireClipError(t, d.cut(t, "uid-v3", d.dave, clipSourceLive), http.StatusForbidden, clipErrNotWatching, "named by username")

	// Somebody else proves Dave's key: Dave is linked to nothing now, and
	// the one who proved it is the streamer.
	require.NoError(t, d.p.setLink("uid-v3", "videokey-dave", "publish-token-v3"))
	requireClipError(t, d.cut(t, "uid-dave", d.dave, clipSourceLive), http.StatusForbidden, clipErrLocked, "superseded")
	d.makeDraft(t, "uid-v3")

	// Not linked: refused before anything, watching or not.
	d.person("uid-v4")
	d.watch("sess-v4", "uid-v4", d.dave)
	requireClipError(t, d.cut(t, "uid-v4", d.dave, clipSourceLive), http.StatusForbidden, clipErrLocked, "not linked")

	// A poll a few minutes on no longer lists V1's session: they stopped.
	d.at(t, 4*time.Minute)
	d.p.noteClipSessions(d.listing(map[string][]string{d.dave: {"sess-bob"}}), d.now())
	requireClipError(t, d.cut(t, "uid-v1", d.dave, clipSourceLive), http.StatusForbidden, clipErrNotWatching, "no longer listed")
	d.makeDraft(t, "uid-bob")

	// The held end of Bob's stream, which ended ten seconds ago after forty.
	ended := newMediaTestPipe(t, draftsClipLength)
	endedStream := newMediaFixtureStream(t, ended)
	endedStream.start = d.now().Add(-50 * time.Second).UnixNano()
	endedStream.play(40 * time.Second)
	tailRing := ended.writer.finish(context.Background(), true)
	require.NotNil(t, tailRing)
	end := tailRing.endTime()
	bob := d.p.streamHandle("videokey-bob")
	d.rec.setTail(bob, clipTail{
		ring:   tailRing,
		until:  d.now().Add(10 * time.Minute),
		stream: apiStream{StreamKey: "videokey-bob", Title: "Bob", UserID: "uid-bob", Username: "bob", LinkSource: "manual", MOTD: "bye"},
	})

	// Watching ten seconds before it ended: within the minute.
	d.p.noteClipViewer("sess-v2-bob", "uid-v2", bob, end.Add(-50*time.Second))
	tail := decodeDraft(t, d.cut(t, "uid-v2", bob, clipSourceTail), http.StatusCreated)
	require.Equal(t, clipSourceTail, tail.Source)
	require.Equal(t, bob, tail.Handle)
	require.Equal(t, endedStream.ms(10*time.Second), tail.StartAt, "the clip length before its end")
	require.InDelta(t, end.UnixMilli(), tail.StartAt+tail.DurationMs, 1, "ends at its end, to the millisecond")
	require.Equal(t, "bye", tail.StreamNote)
	require.Equal(t, "Bob", tail.Streamer.Name)

	// Last heard of seventy seconds before it ended: not watching then.
	d.p.noteClipViewer("sess-dan-bob", "uid-dan", bob, end.Add(-70*time.Second))
	requireClipError(t, d.cut(t, "uid-dan", bob, clipSourceTail), http.StatusForbidden, clipErrNotWatching, "gone before the end")

	// Tuned in five seconds after it ended — to whatever came next on that
	// handle — and so not watching when it ended.
	d.p.noteClipViewer("sess-v1-bob", "uid-v1", bob, end.Add(5*time.Second))
	requireClipError(t, d.cut(t, "uid-v1", bob, clipSourceTail), http.StatusForbidden, clipErrNotWatching, "tuned in after the end")

	// Polled a minute apart, two polls are the larger: last heard of ninety
	// seconds before the end is within them, and a hundred and thirty is not.
	slow := d.p.getConfiguration().Clone()
	slow.PollIntervalSeconds = 60
	d.p.setConfiguration(slow)
	d.p.noteClipViewer("sess-v3-bob", "uid-v3", bob, end.Add(-90*time.Second))
	decodeDraft(t, d.cut(t, "uid-v3", bob, clipSourceTail), http.StatusCreated)
	d.p.noteClipViewer("sess-v1-bob-early", "uid-v1", bob, end.Add(-130*time.Second))
	requireClipError(t, d.cut(t, "uid-v1", bob, clipSourceTail), http.StatusForbidden, clipErrNotWatching, "two slow polls before the end")

	// Its streamer, by link, without a session.
	decodeDraft(t, d.cut(t, "uid-bob", bob, clipSourceTail), http.StatusCreated)

	// A tail no longer held, and one never held, are gone.
	d.rec.setTail(bob, clipTail{ring: tailRing, until: d.now(), stream: apiStream{StreamKey: "videokey-bob"}})
	requireClipError(t, d.cut(t, "uid-v2", bob, clipSourceTail), http.StatusGone, clipErrTailGone, "no longer held")
	requireClipError(t, d.cut(t, "uid-v2", d.dave, clipSourceTail), http.StatusGone, clipErrTailGone, "never held")

	// A live stream that is not on the air, and a 24/7 channel, are not live.
	requireClipError(t, d.cut(t, "uid-bob", bob, clipSourceLive), http.StatusNotFound, clipErrNotLive, "off the air")
	d.goLive(apiStream{StreamKey: "videokey-watts", Title: "Watts", AlwaysOn: true})
	requireClipError(t, d.cut(t, "uid-v2", d.p.streamHandle("videokey-watts"), clipSourceLive), http.StatusNotFound, clipErrNotLive, "24/7")

	// Without a recorder behind them, there is nothing to cut from: a live
	// stream cannot be clipped, and no end of one is held.
	d.p.clips().recordings = noClipRecordings{}
	requireClipError(t, d.cut(t, "uid-bob", d.dave, clipSourceLive), http.StatusConflict, clipErrNotClippable, "no recorder")
	requireClipError(t, d.cut(t, "uid-bob", bob, clipSourceTail), http.StatusGone, clipErrTailGone, "no tails")

	// A peek is nobody watching: through handleWHEP, as the panel opens one.
	d.p.clips().recordings = d.rec
	origin, issued := fakeWHEPOrigin(t)
	defer origin.Close()
	d.p.httpClient = origin.Client()
	cfg := d.p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = origin.URL
	cfg.LivePreviews = true
	d.p.setConfiguration(cfg)
	d.goLive(apiStream{StreamKey: "videokey-dave", Title: "Dave", UserID: "uid-dave", Username: "dave", LinkSource: "manual"})

	d.person("uid-v5")
	d.link("uid-v5", keyOf("uid-v5"))
	peek := d.p.stills().claim(d.dave, "uid-v5", stillModePeek, time.Now())
	require.NotEmpty(t, peek.Lease)
	require.Equal(t, http.StatusOK, whepRequest(t, d.p, d.dave, peek.Lease, "uid-v5").Code)
	require.Len(t, issued(), 1)
	requireClipError(t, d.cut(t, "uid-v5", d.dave, clipSourceLive), http.StatusForbidden, clipErrNotWatching, "a peek")

	require.Equal(t, http.StatusOK, whepRequest(t, d.p, d.dave, "", "uid-v5").Code)
	d.makeDraft(t, "uid-v5")

	// The gate keeps nobody's session in a log line.
	for _, call := range d.api.Calls {
		if strings.HasPrefix(call.Method, "Log") {
			for _, session := range append(issued(), "sess-dan", "sess-v1") {
				require.NotContains(t, strings.Join(stringsOf(call.Arguments), " "), session)
			}
		}
	}
}

func stringsOf(values []any) []string {
	texts := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			texts = append(texts, text)
		}
	}

	return texts
}

// A clip's title keeps no credential: the publisher's own publish token and
// stream key, in whatever case, and the clipped stream's key all come out
// before it is cut to 80 runes, so a token pasted near the end leaves none of
// itself behind (spec §2.19 item 27, A§2.1).
func TestTitlesAreCleanedAndKeysRedacted(t *testing.T) {
	d := newDraftsWorld(t)
	token := "0f0e0d0c-1111-2222-3333-444455556666"
	d.kvSet(kvLinkTokenPrefix+"uid-bob", []byte(token))

	for _, c := range []struct{ title, want string }{
		{"  that jump  ", "that jump"},
		{"my key is VIDEOKEY-BOB, oops", "my key is my stream, oops"},
		{"look: videokey-dave is the key", "look: the stream is the key"},
		{"token " + token + " pasted", "token my stream pasted"},
		{strings.Repeat("é", 100), strings.Repeat("é", 80)},
		{"", ""},
	} {
		cleaned, err := d.p.cleanClipTitle("uid-bob", "videokey-bob", "videokey-dave", c.title)
		require.NoError(t, err)
		require.Equal(t, c.want, cleaned, c.title)
	}

	// A credential with a character in it that Mattermost strips: stripped
	// before anything is looked for, so it is found, rather than put back
	// together by the stripping that comes with the cut.
	disguised := []struct{ title, want string }{
		{"see video\u202ekey-dave now", "see the stream now"},
		{"VIDEO\u2028KEY-BOB is mine", "my stream is mine"},
		{"token " + token[:8] + "\u202a" + token[8:], "token my stream"},
	}
	for _, c := range disguised {
		cleaned, err := d.p.cleanClipTitle("uid-bob", "videokey-bob", "videokey-dave", c.title)
		require.NoError(t, err)
		require.Equal(t, c.want, cleaned, c.title)
	}

	nearTheEnd := strings.Repeat("a", 75) + " " + token
	cleaned, err := d.p.cleanClipTitle("uid-bob", "videokey-bob", "videokey-dave", nearTheEnd)
	require.NoError(t, err)
	require.LessOrEqual(t, len([]rune(cleaned)), maxClipTitle)
	require.NotContains(t, cleaned, token[:4], "not even the start of it")

	// Through the route: what is kept, and what is answered.
	title := "Dave is videokey-dave, I am VideoKey-Bob, token " + token
	draft := d.makeDraft(t, "uid-bob")
	view := decodeClipView(t, d.publish(t, "uid-bob", draft.ID, publishBody(title, 0, 8000)), http.StatusCreated)
	want := "Dave is the stream, I am my stream, token my stream"
	require.Equal(t, want, view.Title)
	require.Equal(t, want, d.stored(t).clip(draft.ID).Title)

	d.at(t, 100*time.Second)
	cut := d.makeDraft(t, "uid-bob")
	view = decodeClipView(t, d.publish(t, "uid-bob", cut.ID, publishBody(nearTheEnd, 0, 8000)), http.StatusCreated)
	require.LessOrEqual(t, len([]rune(view.Title)), maxClipTitle)

	for i, c := range disguised {
		d.at(t, time.Duration(135+35*i)*time.Second)
		cut := d.makeDraft(t, "uid-bob")
		view := decodeClipView(t, d.publish(t, "uid-bob", cut.ID, publishBody(c.title, 0, 8000)), http.StatusCreated)
		require.Equal(t, c.want, view.Title, c.title)
		require.Equal(t, c.want, d.stored(t).clip(cut.ID).Title, c.title)
	}

	stored := string(d.kvGet(kvClips))
	for _, secret := range []string{token[:4], "videokey-dave", "videokey-bob", "VideoKey-Bob"} {
		require.NotContains(t, stored, secret)
	}
}

// Somebody who unlinks keeps their drafts for their two hours, but can reach
// none of them, not even to throw one away; relinking, to any stream, gives
// them back, and a draft cut before still publishes as the stream it was cut
// from (A§3.9, A§3.11).
func TestDraftsSurviveUnlinkButAreUnreachable(t *testing.T) {
	d := newDraftsWorld(t)
	draft := d.makeDraft(t, "uid-bob")
	require.Equal(t, http.StatusOK, d.putThumb(t, "uid-bob", draft.ID, draftJPEG(t, 30)).Code)

	d.unlink("uid-bob")
	for _, c := range []clipCall{
		{method: http.MethodGet, target: clipsPath + "/drafts"},
		{method: http.MethodPost, target: clipsPath + "/drafts", body: `{"handle":"` + d.dave + `","source":"live"}`},
		{method: http.MethodGet, target: clipsPath + "/draft/video?id=" + draft.ID},
		{method: http.MethodHead, target: clipsPath + "/draft/video?id=" + draft.ID},
		{method: http.MethodPut, target: clipsPath + "/draft/thumb?id=" + draft.ID, body: string(draftJPEG(t, 31))},
		{method: http.MethodPost, target: clipsPath + "/draft/publish?id=" + draft.ID, body: publishBody("", 0, 10000)},
		{method: http.MethodDelete, target: clipsPath + "/draft?id=" + draft.ID},
	} {
		requireClipError(t, d.call(t, "uid-bob", c), http.StatusForbidden, clipErrLocked, describe(c))
	}

	// Still there, as it was.
	kept, found := d.p.clips().drafts.find(draft.ID, "uid-bob")
	require.True(t, found)
	require.Equal(t, draftReady, kept.state)
	require.True(t, exists(d.draftPath(draft.ID, clipVideoExt)))
	require.True(t, exists(d.draftPath(draft.ID, clipThumbExt)))

	// Relinked, to another stream.
	d.link("uid-bob", "videokey-bob-two")
	d.p.clipsLinkChanged("uid-bob")
	require.Equal(t, []string{draft.ID}, draftIDs(d.drafts(t, "uid-bob")))
	require.Equal(t, http.StatusOK, d.draftVideo(t, "uid-bob", draft.ID).Code)

	view := decodeClipView(t, d.publish(t, "uid-bob", draft.ID, publishBody("", 0, 10000)), http.StatusCreated)
	require.Equal(t, "Dave", view.Streamer.Name)
	require.False(t, view.OfMyStream)
	require.Equal(t, d.p.streamTag("videokey-dave"), d.stored(t).clip(draft.ID).StreamTag)

	// Two hours after it was cut, an unlinked owner's draft goes like any
	// other.
	d.at(t, 100*time.Second)
	later := d.makeDraft(t, "uid-bob")
	d.unlink("uid-bob")
	expiry := time.UnixMilli(later.ExpiresAt)
	d.p.housekeepClips(expiry)
	require.False(t, exists(d.draftPath(later.ID, clipVideoExt)))
}

// The publisher's own token is read strictly, to take it out of the title: a
// store that does not answer refuses the publish, and nothing is published,
// moved or spent (A§2.1).
func TestPublishRefusesWhenTokenUnreadable(t *testing.T) {
	d := newDraftsWorld(t)
	draft := d.makeDraft(t, "uid-bob")
	body := publishBody("my token might be in here", 0, 10000)

	d.faults.failRead(kvLinkTokenPrefix + "uid-bob")
	requireClipError(t, d.publish(t, "uid-bob", draft.ID, body), http.StatusServiceUnavailable, clipErrStore, "token unreadable")
	require.Len(t, logged(d.api, "LogWarn", "Could not read a publish token to keep it out of a clip title"), 1)

	require.Empty(t, d.stored(t).Clips)
	require.Empty(t, namesIn(t, filepath.Join(d.h.root, clipsFilesDir)))
	require.True(t, exists(d.draftPath(draft.ID, clipVideoExt)))
	require.Equal(t, draftReady, d.drafts(t, "uid-bob")[0].State)
	_, spent := tokensLeft(d.p.clips().publishLimiter, "uid-bob")
	require.False(t, spent)

	d.faults.healRead(kvLinkTokenPrefix + "uid-bob")
	decodeClipView(t, d.publish(t, "uid-bob", draft.ID, body), http.StatusCreated)
}

// Every panel session is kept, linked or not, so somebody who links half way
// through a stream can clip it at once, without tuning in again; and with
// Clips off too, so that switching it on finds everybody already watching
// (TestWatchingBeforeClipsIsOnStillCounts). A session Broadcast Box named no
// id counts for its first minute, which an admin is told once (A§3.4).
func TestNoteViewerRecordsUnlinkedSessions(t *testing.T) {
	d := newDraftsWorld(t)
	origin, issued := fakeWHEPOrigin(t)
	defer origin.Close()
	d.p.httpClient = origin.Client()
	cfg := d.p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = origin.URL
	d.p.setConfiguration(cfg)
	viewers := &d.p.clips().viewers

	d.person("uid-niaj")
	require.Equal(t, http.StatusOK, whepRequest(t, d.p, d.dave, "", "uid-niaj").Code)
	require.True(t, viewers.watching("uid-niaj", d.dave, d.now()))
	requireClipError(t, d.cut(t, "uid-niaj", d.dave, clipSourceLive), http.StatusForbidden, clipErrLocked, "not linked yet")

	// The poll lists it, and four minutes on Nova links.
	d.p.noteClipSessions(d.listing(map[string][]string{d.dave: issued()}), d.now())
	d.at(t, 4*time.Minute)
	d.p.noteClipSessions(d.listing(map[string][]string{d.dave: issued()}), d.now())
	d.link("uid-niaj", keyOf("uid-niaj"))
	d.p.clipsLinkChanged("uid-niaj")
	d.makeDraft(t, "uid-niaj")

	// With Clips off, a session is still noted, for when it is back on.
	off := cfg.Clone()
	off.EnableClips = false
	d.p.setConfiguration(off)
	d.person("uid-v1")
	require.Equal(t, http.StatusOK, whepRequest(t, d.p, d.dave, "", "uid-v1").Code)
	require.True(t, viewers.watching("uid-v1", d.dave, d.now()))
	d.p.setConfiguration(cfg)

	// A Broadcast Box that names no session: it counts for its first minute
	// only, and an admin is told once.
	bare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/sdp")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("v=0\r\n"))
	}))
	defer bare.Close()
	d.p.httpClient = bare.Client()
	elsewhere := cfg.Clone()
	elsewhere.BroadcastBoxURL = bare.URL
	d.p.setConfiguration(elsewhere)

	d.person("uid-v2")
	d.link("uid-v2", keyOf("uid-v2"))
	for range 2 {
		require.Equal(t, http.StatusOK, whepRequest(t, d.p, d.dave, "", "uid-v2").Code)
	}
	require.Len(t, logged(d.api, "LogWarn", "Broadcast Box gave a playback session no id, so viewers can clip only in their first minute of watching"), 1)
	d.makeDraft(t, "uid-v2")
	d.move(d.now().Add(61 * time.Second))
	d.p.noteClipSessions(d.listing(nil), d.now())
	requireClipError(t, d.cut(t, "uid-v2", d.dave, clipSourceLive), http.StatusForbidden, clipErrNotWatching, "unnamed, past its minute")
}

// Somebody who pressed play before Clips was switched on can clip as soon as
// it is: their panel never offers again, so the session it opened while Clips
// was off is the only one the server will ever hear of. The same when Clips is
// switched off and on again while they watch (Victor's rule: linked and
// watching the stream now).
func TestWatchingBeforeClipsIsOnStillCounts(t *testing.T) {
	d := newDraftsWorld(t)
	origin, issued := fakeWHEPOrigin(t)
	defer origin.Close()
	d.p.httpClient = origin.Client()
	on := d.p.getConfiguration().Clone()
	on.BroadcastBoxURL = origin.URL
	d.p.setConfiguration(on)
	d.switchClipsOff()

	// Dan, linked, starts watching Dave while Clips is off.
	require.Equal(t, http.StatusOK, whepRequest(t, d.p, d.dave, "", "uid-dan").Code)
	d.p.noteClipSessions(d.listing(map[string][]string{d.dave: issued()}), d.now())

	// Four minutes on, long past a new session's first minute, an admin
	// switches Clips on. Dan's panel says nothing new; the polls go on
	// listing her session.
	d.at(t, 4*time.Minute)
	d.p.noteClipSessions(d.listing(map[string][]string{d.dave: issued()}), d.now())
	d.p.setConfiguration(on)
	d.makeDraft(t, "uid-dan")

	// Off and on again while she watches.
	d.switchClipsOff()
	d.at(t, 5*time.Minute)
	d.p.noteClipSessions(d.listing(map[string][]string{d.dave: issued()}), d.now())
	d.p.setConfiguration(on)
	d.makeDraft(t, "uid-dan")

	// Somebody whose session Broadcast Box no longer lists is still told to
	// tune in.
	requireClipError(t, d.cut(t, "uid-bob", d.dave, clipSourceLive), http.StatusForbidden, clipErrNotWatching, "Bob's session is long closed")
}

// Switching Clips off throws away every draft, at once: OnConfigurationChange
// calls this from the settings save itself, without waiting for a poll (spec
// §2.14, A§2.1). Who is watching what is kept, for when it is back on. A draft still being
// cut goes too, its export cancelled and its click told why, at no cost; so
// does a click that read the settings just before the save and held its
// place just after. A draft being published is left to finish, and goes if
// the publish fails rather than coming back.
func TestTurningClipsOffThrowsAwayTheDrafts(t *testing.T) {
	d := newDraftsWorld(t)
	draft := d.makeDraft(t, "uid-bob")
	busy := d.makeDraft(t, "uid-bob")

	d.p.clips().drafts.mu.Lock()
	d.p.clips().drafts.byID[busy.ID].state = draftPublishing
	d.p.clips().drafts.mu.Unlock()

	d.p.clipsTurnedOff(d.now())

	require.False(t, exists(d.draftPath(draft.ID, clipVideoExt)))
	requireClipError(t, d.draftVideo(t, "uid-bob", draft.ID), http.StatusGone, clipErrDraftGone, "thrown away with Clips")
	require.True(t, exists(d.draftPath(busy.ID, clipVideoExt)), "a publish under way finishes")
	require.True(t, d.p.clips().viewers.watching("uid-bob", d.dave, d.now()), "who is watching is kept")
	used, _ := d.p.clips().usage("")
	require.Equal(t, busy.Bytes, used)

	// A click whose export is waiting for a slot as Clips goes off.
	d = newDraftsWorld(t)
	release := holdSlots(t)
	waiting := d.clickWaiting(t, "uid-bob")
	d.switchClipsOff()
	requireClipError(t, answerOf(t, waiting), http.StatusForbidden, clipErrClipsOff, "cut as Clips went off")
	release()

	tokens, _ := tokensLeft(d.p.clips().captureLimiter, "uid-bob")
	require.Equal(t, float64(5), tokens, "refunded")
	require.Empty(t, d.drafts(t, "uid-bob"))
	require.Zero(t, d.p.clips().drafts.counted())
	require.Empty(t, namesIn(t, filepath.Join(d.h.root, clipsDraftsDir)))
	used, _ = d.p.clips().usage("")
	require.Zero(t, used)

	// A click that looked at the settings just before they were saved, and
	// holds its place just after: the streamer's own, who needs no session,
	// since the save forgets every one.
	d = newDraftsWorld(t)
	d.rec.beforeNextLive(d.switchClipsOff)
	requireClipError(t, d.cut(t, "uid-dave", d.dave, clipSourceLive), http.StatusForbidden, clipErrClipsOff, "a click that slipped past the save")
	_, spent := tokensLeft(d.p.clips().captureLimiter, "uid-dave")
	require.False(t, spent)
	require.Zero(t, d.p.clips().drafts.counted())
	require.Empty(t, namesIn(t, filepath.Join(d.h.root, clipsDraftsDir)))

	// Two publishes waiting for a slot as Clips goes off: the one that gets
	// its slot is a clip; the one whose asker gives up takes its draft with
	// it, and is told what a retry would be.
	d = newDraftsWorld(t)
	lands := d.makeDraft(t, "uid-bob")
	fails := d.makeDraft(t, "uid-bob")
	release = holdSlots(t)
	landing := d.publishWaiting(t, context.Background(), "uid-bob", lands.ID, publishBody("", 0, 10000))
	gaveUp, giveUp := context.WithCancel(context.Background())
	failing := d.publishWaiting(t, gaveUp, "uid-bob", fails.ID, publishBody("", 0, 10000))

	d.switchClipsOff()
	giveUp()
	requireClipError(t, answerOf(t, failing), http.StatusGone, clipErrDraftGone, "a publish that failed after Clips went off")
	release()
	decodeClipView(t, answerOf(t, landing), http.StatusCreated)

	require.False(t, exists(d.draftPath(fails.ID, clipVideoExt)))
	require.False(t, exists(d.clipPath(fails.ID, clipVideoExt)))
	requireClipError(t, d.draftVideo(t, "uid-bob", fails.ID), http.StatusGone, clipErrDraftGone, "gone with Clips")
	require.Equal(t, []string{lands.ID}, draftIDs(d.drafts(t, "uid-bob")), "the clip that landed, and nothing else")
	require.Empty(t, namesIn(t, filepath.Join(d.h.root, clipsDraftsDir)))
	used, mine := d.p.clips().usage("uid-bob")
	require.Equal(t, mine, used, "nothing of either draft is counted any more")
	tokens, _ = tokensLeft(d.p.clips().publishLimiter, "uid-bob")
	require.Equal(t, float64(9), tokens, "the publish that failed cost nothing")
}

// Stopping the plugin cancels whatever is being cut, forgets every draft and
// who was watching what, and stops the minute's housekeeping; starting again
// begins with none of them (spec §2.14).
func TestStoppingClipsForgetsDraftsAndViewers(t *testing.T) {
	d := newDraftsWorld(t)
	draft := d.makeDraft(t, "uid-bob")

	closed := 0
	d.p.clips().runLock.Lock()
	d.p.clips().housekeeping = closingJob{closed: &closed}
	d.p.clips().runLock.Unlock()

	d.p.stopClips()
	require.Equal(t, 1, closed)
	require.Nil(t, d.p.clips().runCtx)
	require.Zero(t, d.p.clips().drafts.counted())
	require.False(t, exists(d.draftPath(draft.ID, clipVideoExt)))
	require.False(t, d.p.clips().viewers.watching("uid-bob", d.dave, d.now()))

	// Stopped, nobody is remembered watching anything.
	requireClipError(t, d.cut(t, "uid-bob", d.dave, clipSourceLive), http.StatusForbidden, clipErrNotWatching, "nobody is watching")

	ran := 0
	d.p.clips().every = func(interval time.Duration, run func()) clipsJob {
		require.Equal(t, time.Minute, interval)
		ran++
		run()

		return &fakeClipsJob{}
	}
	d.p.startClips()
	require.Equal(t, 1, ran, "the housekeeping is started with the store")
	require.NotNil(t, d.p.clips().runCtx)
	require.Empty(t, d.drafts(t, "uid-bob"))
	require.Equal(t, http.StatusNotFound, d.draftVideo(t, "uid-bob", draft.ID).Code)

	// Activated again with no deactivation between, as Mattermost can: the
	// drafts this process held went with the files, so they are forgotten
	// too, not listed with nothing behind them.
	d.watch("sess-bob-again", "uid-bob", d.dave)
	again := d.makeDraft(t, "uid-bob")
	d.p.startClips()
	require.Empty(t, d.drafts(t, "uid-bob"))
	require.Zero(t, d.p.clips().drafts.counted())
	requireClipError(t, d.draftVideo(t, "uid-bob", again.ID), http.StatusNotFound, clipErrDraftGone, "forgotten on activation")
	require.Empty(t, namesIn(t, filepath.Join(d.h.root, clipsDraftsDir)))
}

// Saving the console with Clips switched off takes the drafts away there and
// then, from OnConfigurationChange, before the poller is restarted; saving
// with it still on takes nothing (spec §2.14, A§2.1).
func TestSavingClipsOffInTheConsoleTakesTheDraftsAway(t *testing.T) {
	d := newDraftsWorld(t)
	draft := d.makeDraft(t, "uid-bob")

	saved := map[string]any{"enableclips": true, "cliplengthseconds": "30", "broadcastboxurl": ""}
	d.api.On("GetUnsanitizedConfig").Return(&model.Config{PluginSettings: model.PluginSettings{
		Plugins: map[string]map[string]any{manifestID: saved},
	}})
	d.api.On("LoadPluginConfiguration", mock.Anything).Run(func(args mock.Arguments) {
		encoded, err := json.Marshal(saved)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(encoded, args.Get(0)))
	}).Return(nil)
	// The Site URL, which the app check's rules are read with on every load.
	d.api.On("GetConfig").Return(&model.Config{}).Maybe()

	// The poller the save restarts finds no Broadcast Box set, and says so.
	d.api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	d.api.On("KVList", mock.Anything, mock.Anything).Return([]string{}, nil).Maybe()

	// Loaded once before activation, as Mattermost does, so that the saves
	// below differ from what is running in nothing but what they change.
	require.NoError(t, d.p.OnConfigurationChange())
	d.p.activatedLock.Lock()
	d.p.activated = true
	d.p.activatedLock.Unlock()
	t.Cleanup(d.p.stopPoller)

	require.NoError(t, d.p.OnConfigurationChange())
	require.Equal(t, []string{draft.ID}, draftIDs(d.drafts(t, "uid-bob")), "saved with Clips on")

	saved["enableclips"] = false
	require.NoError(t, d.p.OnConfigurationChange())
	require.False(t, exists(d.draftPath(draft.ID, clipVideoExt)))
	require.Zero(t, d.p.clips().drafts.counted())
	requireClipError(t, d.draftVideo(t, "uid-bob", draft.ID), http.StatusGone, clipErrDraftGone, "taken when Clips went off")
	require.True(t, d.p.clips().viewers.watching("uid-bob", d.dave, d.now()), "who is watching is kept for when Clips is back on")
}

type closingJob struct {
	closed *int
}

func (j closingJob) Close() error {
	*j.closed++

	return nil
}

// The registry is fed where A§2.1 puts it: handleWHEP notes a session the
// panel opens, and every successful poll notes which of them Broadcast Box
// still lists. A session the latest poll listed counts
// however long ago it opened; one it no longer lists counts no more once its
// first minute is up. Nothing of it reaches the snapshot.
func TestPollNotesWhichSessionsAreStillOpen(t *testing.T) {
	var lock sync.Mutex
	open := []string{}
	issued := 0

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lock.Lock()
		defer lock.Unlock()

		switch r.URL.Path {
		case "/api/whep":
			issued++
			id := fmt.Sprintf("dddddddd-0000-0000-0000-%012d", issued)
			open = append(open, id)

			w.Header().Set("Location", "/api/whep/"+id)
			w.Header().Set("Content-Type", "application/sdp")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, "v=0\r\n")

		case "/api/status":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]bbStream{rawWatched("videokey-dave", open...)})

		default:
			http.NotFound(w, r)
		}
	}))
	defer origin.Close()

	p := watchersPlugin(t)
	p.httpClient = origin.Client()
	cfg := p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = origin.URL
	cfg.EnableClips = true
	cfg.ShowWatchers = false
	p.setConfiguration(cfg)

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("KVList", mock.Anything, mock.Anything).Return([]string{}, nil)
	api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return()

	dave := p.streamHandle("videokey-dave")
	viewers := &p.clips().viewers

	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, "", "uid-a").Code)
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, "", "uid-b").Code)

	p.pollOnce(t.Context(), true)
	later := time.Now().Add(5 * time.Minute)
	require.True(t, viewers.watching("uid-a", dave, later), "listed by the latest poll")
	require.True(t, viewers.watching("uid-b", dave, later))

	// B closes the panel: the next poll no longer lists it.
	lock.Lock()
	open = open[:1]
	lock.Unlock()
	p.pollOnce(t.Context(), true)
	require.True(t, viewers.watching("uid-a", dave, later))
	require.False(t, viewers.watching("uid-b", dave, later), "no longer listed, and its first minute is long gone")

	encoded, err := json.Marshal(p.getSnapshot())
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "dddddddd")
}

// A stream its streamer has switched clipping off for is clipped by nobody,
// its streamer included, live or from its held end: refused before the watch
// gate, and before a token is spent. The switch is kept by stream, under the
// tag of its key in whatever case that was typed; one kept under the handle,
// under somebody's id, or under another stream's tag, is not it (A§3.4,
// A§3.5, spec §2.10.1 step 6).
func TestDraftRefusedWhileTheStreamIsOptedOut(t *testing.T) {
	d := newDraftsWorld(t)
	capture := d.p.clips().captureLimiter
	off := clipMark{By: "uid-dave", At: d.now().UnixMilli()}

	d.changeStored(t, func(doc *clipsDoc) {
		doc.OptOut = map[string]clipMark{d.dave: off, "uid-dave": off, d.p.streamTag("videokey-bob"): off}
	})
	first := d.makeDraft(t, "uid-bob")

	// The held end of Dave's stream too, as when it dropped a moment ago.
	ended := newMediaTestPipe(t, draftsClipLength)
	endedStream := newMediaFixtureStream(t, ended)
	endedStream.start = d.now().Add(-50 * time.Second).UnixNano()
	endedStream.play(40 * time.Second)
	tailRing := ended.writer.finish(context.Background(), true)
	require.NotNil(t, tailRing)
	d.rec.setTail(d.dave, clipTail{
		ring:   tailRing,
		until:  d.now().Add(10 * time.Minute),
		stream: apiStream{StreamKey: "videokey-dave", Title: "Dave", UserID: "uid-dave", Username: "dave", LinkSource: "manual"},
	})

	// Switched off by Dave, who linked his key as "VideoKey-Dave".
	d.changeStored(t, func(doc *clipsDoc) {
		doc.OptOut = map[string]clipMark{d.p.streamTag("VideoKey-Dave"): off}
	})

	before := map[string]float64{}
	for _, userID := range []string{"uid-bob", "uid-dave", "uid-dan"} {
		before[userID], _ = tokensLeft(capture, userID)
	}
	for _, c := range []struct{ userID, source, about string }{
		{"uid-bob", clipSourceLive, "somebody watching"},
		{"uid-dave", clipSourceLive, "its streamer"},
		{"uid-dan", clipSourceLive, "somebody not even watching"},
		{"uid-bob", clipSourceTail, "its held end"},
		{"uid-dave", clipSourceTail, "its held end, by its streamer"},
	} {
		requireClipError(t, d.cut(t, c.userID, d.dave, c.source), http.StatusForbidden, clipErrStreamerOff, c.about)
	}
	for userID, tokens := range before {
		left, _ := tokensLeft(capture, userID)
		require.Equal(t, tokens, left, userID)
	}
	require.Equal(t, []string{first.ID + clipVideoExt}, namesIn(t, filepath.Join(d.h.root, clipsDraftsDir)))
	require.Equal(t, 1, d.p.clips().drafts.counted())

	// Switched on again, it is clipped as before.
	d.changeStored(t, func(doc *clipsDoc) { doc.OptOut = nil })
	d.at(t, 95*time.Second)
	d.makeDraft(t, "uid-dave")
	decodeDraft(t, d.cut(t, "uid-dave", d.dave, clipSourceTail), http.StatusCreated)
}

// Switched off, Clips cuts no draft and publishes none, and says so before a
// token is spent. A draft already published is still answered with its clip:
// a retry after a lost answer is told what the first try made before
// anything else is looked at (spec §2.10.1 step 2, §2.10.2 steps 2–3).
func TestClipsOffRefusesDraftsAndPublishes(t *testing.T) {
	d := newDraftsWorld(t)
	published := d.makeDraft(t, "uid-bob")
	clip := decodeClipView(t, d.publish(t, "uid-bob", published.ID, publishBody("", 0, 10000)), http.StatusCreated)
	d.at(t, 95*time.Second)
	draft := d.makeDraft(t, "uid-bob")

	captures, _ := tokensLeft(d.p.clips().captureLimiter, "uid-bob")
	publishes, _ := tokensLeft(d.p.clips().publishLimiter, "uid-bob")

	off := d.p.getConfiguration().Clone()
	off.EnableClips = false
	d.p.setConfiguration(off)

	requireClipError(t, d.cut(t, "uid-bob", d.dave, clipSourceLive), http.StatusForbidden, clipErrClipsOff, "a click")
	requireClipError(t, d.cut(t, "uid-dave", d.dave, clipSourceLive), http.StatusForbidden, clipErrClipsOff, "the streamer's click")
	requireClipError(t, d.publish(t, "uid-bob", draft.ID, publishBody("", 0, 10000)), http.StatusForbidden, clipErrClipsOff, "a publish")

	again := decodeClipView(t, d.publish(t, "uid-bob", published.ID, publishBody("", 0, 10000)), http.StatusOK)
	require.Equal(t, clip.ID, again.ID)

	left, _ := tokensLeft(d.p.clips().captureLimiter, "uid-bob")
	require.Equal(t, captures, left)
	left, _ = tokensLeft(d.p.clips().publishLimiter, "uid-bob")
	require.Equal(t, publishes, left)
	require.True(t, exists(d.draftPath(draft.ID, clipVideoExt)))
	require.False(t, exists(d.clipPath(draft.ID, clipVideoExt)))
	require.Equal(t, 1, d.p.clips().drafts.counted())
}

// A draft needs room on the disk for itself, at the stream's measured rate,
// above the volume's floor; a publish needs room within its clipper's share,
// within the space for clips and above the floor, for what the trim keeps.
// Each is refused before a token is spent, and nothing is cut or moved.
func TestDraftsAndPublishesNeedRoom(t *testing.T) {
	d := newDraftsWorld(t)
	s := d.p.clips()
	const total = uint64(1000 << 30)
	plenty := s.freeSpace
	room := func(free uint64) {
		s.freeSpace = func(string) (uint64, uint64, bool) { return free, total, true }
	}

	// Above the floor, but not by the clip length at Dave's 12 kB/s.
	room(clipFreeFloor(total) + 30*12000/2)
	requireClipError(t, d.cut(t, "uid-bob", d.dave, clipSourceLive), http.StatusInsufficientStorage, clipErrStorageFull, "no room for a draft")
	_, spent := tokensLeft(s.captureLimiter, "uid-bob")
	require.False(t, spent)
	require.Empty(t, d.drafts(t, "uid-bob"))
	require.Empty(t, namesIn(t, filepath.Join(d.h.root, clipsDraftsDir)))

	s.freeSpace = plenty
	draft := d.makeDraft(t, "uid-bob")

	// A thousand bytes short of Bob's share: a third of the draft is more.
	share := clipShareBytes(d.p.getConfiguration().clipStorageLimitBytes())
	s.addUsage("uid-bob", share-1000)
	requireClipError(t, d.publish(t, "uid-bob", draft.ID, publishBody("", 0, 10000)), http.StatusInsufficientStorage, clipErrUserStorageFull, "past their share")
	s.addUsage("uid-bob", -(share - 1000))

	// A thousand bytes above the floor: the same third is more.
	room(clipFreeFloor(total) + 1000)
	requireClipError(t, d.publish(t, "uid-bob", draft.ID, publishBody("", 0, 10000)), http.StatusInsufficientStorage, clipErrStorageFull, "below the floor")

	_, spent = tokensLeft(s.publishLimiter, "uid-bob")
	require.False(t, spent)
	require.Empty(t, namesIn(t, filepath.Join(d.h.root, clipsFilesDir)))
	require.Equal(t, draftReady, d.drafts(t, "uid-bob")[0].State)

	s.freeSpace = plenty
	decodeClipView(t, d.publish(t, "uid-bob", draft.ID, publishBody("", 0, 10000)), http.StatusCreated)
}

// A clip most of which its clipper has already published from the same
// stream is a duplicate, and their thirtieth clip, or everybody's five
// hundredth, is the last: each refused before a token is spent and before
// anything is cut or moved, with the draft left ready (spec §2.10.2 steps
// 7–8, before step 10).
func TestDuplicatesAndCapsAreRefusedBeforeATokenIsSpent(t *testing.T) {
	d := newDraftsWorld(t)
	publishes := d.p.clips().publishLimiter
	first := d.makeDraft(t, "uid-bob")
	second := d.makeDraft(t, "uid-bob")
	decodeClipView(t, d.publish(t, "uid-bob", first.ID, publishBody("", 0, 20000)), http.StatusCreated)
	tokens, _ := tokensLeft(publishes, "uid-bob")

	requireClipError(t, d.publish(t, "uid-bob", second.ID, publishBody("", 2000, 20000)), http.StatusConflict, clipErrDuplicate, "the same moment again")
	left, _ := tokensLeft(publishes, "uid-bob")
	require.Equal(t, tokens, left)
	require.False(t, exists(d.clipPath(second.ID, clipVideoExt)))
	require.True(t, exists(d.draftPath(second.ID, clipVideoExt)))
	require.Empty(t, tempsIn(t, filepath.Join(d.h.root, clipsFilesDir)))

	// Somebody else's clip of the same moment is no duplicate of theirs.
	d.watch("sess-dan", "uid-dan", d.dave)
	dans := d.makeDraft(t, "uid-dan")
	decodeClipView(t, d.publish(t, "uid-dan", dans.ID, publishBody("", 0, 20000)), http.StatusCreated)

	// Thirty of their own, the one just published among them.
	d.changeStored(t, func(doc *clipsDoc) {
		for i := range maxClipsPerUser - 1 {
			doc.Clips = append(doc.Clips, testClip(clipIDFor(i+1), "uid-bob", d.now().Add(-time.Hour), nil))
		}
	})
	d.at(t, 95*time.Second)
	third := d.makeDraft(t, "uid-bob")
	requireClipError(t, d.publish(t, "uid-bob", third.ID, publishBody("", 0, 10000)), http.StatusConflict, clipErrClipCap, "their thirty-first")

	// Five hundred in all, nearly all of them other people's.
	d.changeStored(t, func(doc *clipsDoc) {
		doc.Clips = doc.Clips[:2]
		for i := len(doc.Clips); i < maxClips; i++ {
			doc.Clips = append(doc.Clips, testClip(clipIDFor(i+1), "uid-"+clipIDFor(i+1), d.now().Add(-time.Hour), nil))
		}
	})
	requireClipError(t, d.publish(t, "uid-bob", third.ID, publishBody("", 0, 10000)), http.StatusConflict, clipErrClipCap, "everybody's five hundred and first")

	left, _ = tokensLeft(publishes, "uid-bob")
	require.Equal(t, tokens, left)
	require.False(t, exists(d.clipPath(third.ID, clipVideoExt)))
	require.ElementsMatch(t, []string{second.ID, third.ID}, draftIDsIn(d.drafts(t, "uid-bob"), draftReady))
}

// draftIDsIn is the ids of the drafts listed in a state.
func draftIDsIn(drafts []draftView, state string) []string {
	ids := []string{}
	for _, draft := range drafts {
		if draft.State == state {
			ids = append(ids, draft.ID)
		}
	}

	return ids
}

// Throwing a draft away and giving it a picture are writes, thirty a minute
// between them like every other write; the thirty-first is refused and
// changes nothing (spec §2.10 limiters).
func TestDraftWritesAreMetered(t *testing.T) {
	d := newDraftsWorld(t)
	draft := d.makeDraft(t, "uid-bob")

	writes := d.p.clips().writeLimiter
	for range 30 {
		require.True(t, writes.allowAt("uid-bob", d.now()))
	}

	requireClipError(t, d.putThumb(t, "uid-bob", draft.ID, draftJPEG(t, 80)), http.StatusTooManyRequests, clipErrRateLimited, "a picture")
	requireClipError(t, d.discard(t, "uid-bob", draft.ID), http.StatusTooManyRequests, clipErrRateLimited, "a discard")

	listed := d.drafts(t, "uid-bob")
	require.Len(t, listed, 1)
	require.Equal(t, draftReady, listed[0].State)
	require.False(t, listed[0].HasThumb)
	require.True(t, exists(d.draftPath(draft.ID, clipVideoExt)))
}

// A publish that spent its token and then found the draft already taken —
// another tab's publish began between its look and its taking it — gives the
// token back and is answered as that tab's would be (spec §2.10.2 step 11).
func TestPublishLosingTheRaceCostsNothing(t *testing.T) {
	d := newDraftsWorld(t)
	draft := d.makeDraft(t, "uid-bob")

	armed := true
	d.faults.lock.Lock()
	d.faults.onRead = func(key string) {
		if armed && key == kvLinkTokenPrefix+"uid-bob" {
			armed = false
			d.setDraftState(draft.ID, draftPublishing)
		}
	}
	d.faults.lock.Unlock()

	requireClipError(t, d.publish(t, "uid-bob", draft.ID, publishBody("", 0, 10000)), http.StatusConflict, clipErrPublishing, "beaten to it")
	tokens, spent := tokensLeft(d.p.clips().publishLimiter, "uid-bob")
	require.True(t, spent, "it was spent")
	require.Equal(t, float64(10), tokens, "and given back")
	require.Empty(t, namesIn(t, filepath.Join(d.h.root, clipsFilesDir)))

	d.setDraftState(draft.ID, draftReady)
	decodeClipView(t, d.publish(t, "uid-bob", draft.ID, publishBody("", 0, 10000)), http.StatusCreated)
}

// A click that is still being cut holds its place under the caps: while it
// waits for a slot, another click past three of theirs, or past twenty in
// all, is refused before it spends a token (spec §2.10.1 step 9).
func TestAClickBeingCutHoldsItsPlace(t *testing.T) {
	d := newDraftsWorld(t)
	capture := d.p.clips().captureLimiter
	d.makeDraft(t, "uid-bob")
	d.makeDraft(t, "uid-bob")

	release := holdSlots(t)
	waiting := d.clickWaiting(t, "uid-bob")
	tokens, _ := tokensLeft(capture, "uid-bob")
	requireClipError(t, d.cut(t, "uid-bob", d.dave, clipSourceLive), http.StatusConflict, clipErrDraftCap, "a fourth while the third is cut")
	left, _ := tokensLeft(capture, "uid-bob")
	require.Equal(t, tokens, left)
	release()
	decodeDraft(t, answerOf(t, waiting), http.StatusCreated)
	require.Len(t, d.drafts(t, "uid-bob"), 3)

	// Nineteen in all, and the twentieth being cut.
	for i, userID := range []string{"uid-v1", "uid-v2", "uid-v3", "uid-v4", "uid-v5", "uid-v6"} {
		d.person(userID)
		d.link(userID, keyOf(userID))
		d.watch("sess-"+userID, userID, d.dave)
		for n := range 3 {
			if 3+i*3+n == maxDraftsTotal-1 {
				break
			}
			d.makeDraft(t, userID)
		}
	}
	require.Equal(t, maxDraftsTotal-1, d.p.clips().drafts.counted())

	release = holdSlots(t)
	waiting = d.clickWaiting(t, "uid-v6")
	d.watch("sess-dan", "uid-dan", d.dave)
	requireClipError(t, d.cut(t, "uid-dan", d.dave, clipSourceLive), http.StatusConflict, clipErrDraftCap, "the twenty-first while the twentieth is cut")
	_, spent := tokensLeft(capture, "uid-dan")
	require.False(t, spent)
	release()
	decodeDraft(t, answerOf(t, waiting), http.StatusCreated)
	require.Equal(t, maxDraftsTotal, d.p.clips().drafts.counted())
}

// A publish looks at where clips are kept again before it builds a path from
// it: a second activation can set the storage again between the publish's
// first look and its reading the draft, and a path is never built from no
// root (A§2.1).
func TestPublishRefusedWhenTheStorageGoesAway(t *testing.T) {
	d := newDraftsWorld(t)
	draft := d.makeDraft(t, "uid-bob")
	root := d.h.root

	armed := true
	d.faults.lock.Lock()
	d.faults.onRead = func(key string) {
		if armed && key == kvClips {
			armed = false
			d.p.clips().setStorage(root, "Clips cannot write to "+root+".")
		}
	}
	d.faults.lock.Unlock()

	requireClipError(t, d.publish(t, "uid-bob", draft.ID, publishBody("", 0, 10000)), http.StatusServiceUnavailable, clipErrStorageUnavailable, "the storage went")
	_, spent := tokensLeft(d.p.clips().publishLimiter, "uid-bob")
	require.False(t, spent)
	require.Empty(t, logged(d.api, "LogWarn", "Could not read a clip draft to publish it"), "no path was looked at")

	d.p.clips().setStorage(root, "")
	require.Equal(t, draftReady, d.drafts(t, "uid-bob")[0].State)
	decodeClipView(t, d.publish(t, "uid-bob", draft.ID, publishBody("", 0, 10000)), http.StatusCreated)
}

// The registry's bounds: a session is forgotten a quarter of an hour after it
// was last heard of, to the second; it holds 4096 sessions, and when full the
// one heard of longest ago makes room; and a session Broadcast Box gave no id
// is filed under the person and the stream, so two people watching through
// such a Broadcast Box are each remembered (spec §2.4.9).
func TestViewerRegistryKeepsWithinItsBounds(t *testing.T) {
	var viewers clipViewers
	const handle = "0123456789abcdef0123456789abcdef"

	viewers.note("sess-a", "uid-a", handle, clipsNow)
	viewers.prune(clipsNow.Add(viewerMemory - time.Second))
	require.True(t, viewers.watchingAt("uid-a", handle, clipsNow, clipsNow))
	viewers.prune(clipsNow.Add(viewerMemory))
	require.False(t, viewers.watchingAt("uid-a", handle, clipsNow, clipsNow), "forgotten at a quarter of an hour")

	// Last heard of by a poll, the quarter of an hour runs from then.
	viewers.note("sess-b", "uid-b", handle, clipsNow)
	viewers.seen([]bbStream{{StreamKey: "k", Sessions: []bbSession{{ID: "sess-b"}}}}, func(string) string { return handle }, clipsNow.Add(10*time.Minute))
	viewers.prune(clipsNow.Add(10*time.Minute + viewerMemory - time.Second))
	require.True(t, viewers.watching("uid-b", handle, clipsNow.Add(10*time.Minute+viewerMemory-time.Second)))
	viewers.clear()

	// Two people, no session ids: each has their own first minute.
	viewers.note("", "uid-a", handle, clipsNow)
	viewers.note("", "uid-b", handle, clipsNow)
	require.True(t, viewers.watching("uid-a", handle, clipsNow.Add(30*time.Second)))
	require.True(t, viewers.watching("uid-b", handle, clipsNow.Add(30*time.Second)))
	viewers.clear()

	// Full: the stalest goes, and only it.
	for i := range maxClipViewerSessions {
		viewers.note(fmt.Sprintf("sess-%d", i), fmt.Sprintf("uid-%d", i), handle, clipsNow.Add(time.Duration(i)*time.Millisecond))
	}
	viewers.note("sess-new", "uid-new", handle, clipsNow.Add(time.Hour))
	viewers.lock.Lock()
	held := len(viewers.sessions)
	_, stalest := viewers.sessions["sess-0"]
	_, next := viewers.sessions["sess-1"]
	_, newest := viewers.sessions["sess-new"]
	viewers.lock.Unlock()
	require.Equal(t, maxClipViewerSessions, held)
	require.False(t, stalest)
	require.True(t, next)
	require.True(t, newest)
}
