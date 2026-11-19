package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// call makes one request through ServeHTTP, as Mattermost would.
func (w *clipsWorld) call(t *testing.T, userID string, c clipCall) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(c.method, c.target, strings.NewReader(c.body))
	request.Header.Set("Mattermost-User-Id", userID)
	for name, value := range c.header {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()

	w.p.ServeHTTP(nil, recorder, request)

	return recorder
}

// linkReads counts the reads of somebody's link from here on.
func (w *clipsWorld) linkReads(userID string) func() int {
	var lock sync.Mutex
	count := 0

	w.faults.lock.Lock()
	previous := w.faults.onRead
	w.faults.onRead = func(key string) {
		if previous != nil {
			previous(key)
		}
		if key == kvLinkByUserPrefix+userID {
			lock.Lock()
			count++
			lock.Unlock()
		}
	}
	w.faults.lock.Unlock()

	return func() int {
		lock.Lock()
		defer lock.Unlock()

		return count
	}
}

func decodeAdminClips(t *testing.T, response *httptest.ResponseRecorder) adminClipsResponse {
	t.Helper()

	var view adminClipsResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &view), response.Body.String())

	return view
}

func decodeClipList(t *testing.T, response *httptest.ResponseRecorder) []clipView {
	t.Helper()

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

	var list clipListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))

	return list.Clips
}

// A linked reader gets every clip still kept, newest first, each saying what
// they may do with it: their own, one of their stream, any at all for a
// linked admin. A clip past its time is gone, before the sweep has been.
func TestClipListAndGetRoutes(t *testing.T) {
	w, _ := startedClipsWorld(t, "uid-bob", "uid-dave", "uid-dan", "uid-boss")
	clipsAdmins(w.api, "uid-boss")

	older := testClip(clipA, "uid-bob", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-time.Hour), "uid-dan"))
	older.StreamTag = w.p.streamTag("videokey-dave")
	publishFixtureClip(t, w.p, older, true)
	newer := testClip(clipB, "uid-dan", clipsNow.Add(-time.Hour), nil)
	newer.Handle = strings.Repeat("e", 32)
	publishFixtureClip(t, w.p, newer, false)
	publishFixtureClip(t, w.p, testClip(clipC, "uid-bob", clipsNow.Add(-31*24*time.Hour), nil), false)

	clips := decodeClipList(t, w.call(t, "uid-bob", clipCall{method: http.MethodGet, target: clipsPath}))
	require.Len(t, clips, 2, "the expired one is not listed")
	require.Equal(t, clipB, clips[0].ID)
	require.Equal(t, clipA, clips[1].ID)
	require.True(t, clips[1].Mine)
	require.True(t, clips[1].CanDelete)
	require.False(t, clips[1].CanVote)
	require.Equal(t, 1, clips[1].Upvotes)
	require.False(t, clips[0].CanDelete)
	require.True(t, clips[0].CanVote)
	require.Nil(t, clips[0].Reports)

	clips = decodeClipList(t, w.call(t, "uid-dave", clipCall{method: http.MethodGet, target: clipsPath}))
	require.True(t, clips[1].OfMyStream, "Dave's stream, by the link he has now")
	require.True(t, clips[1].CanDelete)
	require.False(t, clips[0].CanDelete)

	clips = decodeClipList(t, w.call(t, "uid-boss", clipCall{method: http.MethodGet, target: clipsPath}))
	require.True(t, clips[0].CanDelete, "a linked admin")
	require.True(t, clips[1].CanDelete)

	response := w.call(t, "uid-dan", clipCall{method: http.MethodGet, target: clipsPath + "/clip?id=" + clipA})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var view clipView
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &view))
	require.Equal(t, clipA, view.ID)
	require.True(t, view.Voted)
	require.Equal(t, "Bob", view.Clipper.Name)
	require.NotEmpty(t, view.Thumb)

	for _, gone := range []string{clipC, clipD} {
		response = w.call(t, "uid-dan", clipCall{method: http.MethodGet, target: clipsPath + "/clip?id=" + gone})
		requireClipError(t, response, http.StatusGone, clipErrClipGone, gone)
	}
	response = w.call(t, "uid-dan", clipCall{method: http.MethodGet, target: clipsPath + "/clip?id=A00000000000000A"})
	requireClipError(t, response, http.StatusBadRequest, clipErrBadRequest, "upper case is not an id")

	// One question of Mattermost per request, however many clips.
	w.api.AssertNumberOfCalls(t, "HasPermissionTo", 4)

	for _, listed := range []string{"tag-of-daves-stream", older.StreamTag, "videokey-"} {
		require.NotContains(t, w.call(t, "uid-bob", clipCall{method: http.MethodGet, target: clipsPath}).Body.String(), listed)
	}

	// Halted with nothing cached on a fresh node: the store's trouble.
	w.kvSet(kvClips, []byte(`{"v":1,`))
	cold := clipsWorldOn(t, w.store, "uid-bob")
	setClipsClock(cold.p, clipsNow)
	response = cold.call(t, "uid-bob", clipCall{method: http.MethodGet, target: clipsPath})
	requireClipError(t, response, http.StatusServiceUnavailable, clipErrStore, "halted")
}

// Votes through the route: the state asked for, answered with the count the
// voter now sees; never on their own clip, nor one that has gone; a body that
// makes no sense costs nothing; and they are metered.
func TestVoteRouteIsDesiredStateAndMetered(t *testing.T) {
	w, _ := startedClipsWorld(t, "uid-bob", "uid-dave")
	publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil), false)
	publishFixtureClip(t, w.p, testClip(clipB, "uid-bob", clipsNow.Add(-31*24*time.Hour), nil), false)

	vote := func(userID, id, body string) *httptest.ResponseRecorder {
		return w.call(t, userID, clipCall{method: http.MethodPost, target: clipsPath + "/vote?id=" + id, body: body})
	}
	answer := func(response *httptest.ResponseRecorder) clipVoteResponse {
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

		var got clipVoteResponse
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &got))

		return got
	}

	require.Equal(t, clipVoteResponse{Upvotes: 1, Voted: true}, answer(vote("uid-dave", clipA, `{"up":true}`)))
	require.Equal(t, clipVoteResponse{Upvotes: 1, Voted: true}, answer(vote("uid-dave", clipA, `{"up":true}`)))
	require.Equal(t, clipVoteResponse{Upvotes: 0, Voted: false}, answer(vote("uid-dave", clipA, `{"up":false}`)))

	requireClipError(t, vote("uid-bob", clipA, `{"up":true}`), http.StatusForbidden, clipErrOwnClip, "own clip")
	requireClipError(t, vote("uid-dave", clipB, `{"up":true}`), http.StatusGone, clipErrClipGone, "expired")
	requireClipError(t, vote("uid-dave", clipC, `{"up":true}`), http.StatusGone, clipErrClipGone, "never was")

	// Five tokens spent so far by Dave, and none for nonsense.
	for _, nonsense := range []string{``, `{}`, `{"up":"yes"}`, `{"up":true} trailing`, `{"up":` + strings.Repeat(" ", maxClipRequestBytes) + `true}`} {
		requireClipError(t, vote("uid-dave", clipA, nonsense), http.StatusBadRequest, clipErrBadRequest, nonsense)
	}
	requireClipError(t, vote("uid-dave", "nope", `{"up":true}`), http.StatusBadRequest, clipErrBadRequest, "bad id")

	// A store that did not answer is not the voter's fault, and costs no token.
	w.faults.failRead(kvClips)
	for range 3 {
		requireClipError(t, vote("uid-dave", clipA, `{"up":true}`), http.StatusServiceUnavailable, clipErrStore, "store")
	}
	w.faults.healRead(kvClips)

	for range 15 {
		answer(vote("uid-dave", clipA, `{"up":true}`))
	}
	requireClipError(t, vote("uid-dave", clipA, `{"up":true}`), http.StatusTooManyRequests, clipErrRateLimited, "the 21st in a minute")
}

// Every clip write reaches the panels without a poll: the snapshot as last
// published, given the clips again and published, at most once every two
// seconds however many writes there were. Broadcast Box is never asked.
func TestVotesRepublishWithoutPolling(t *testing.T) {
	w, h := startedClipsWorld(t, "uid-bob", "uid-v1", "uid-v2", "uid-v3")
	publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil), false)

	var asked []string
	var lock sync.Mutex
	w.p.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		lock.Lock()
		asked = append(asked, r.URL.String())
		lock.Unlock()

		return nil, http.ErrHandlerTimeout
	})}

	var published []string
	w.api.On("PublishWebSocketEvent", wsEventStreams, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		payload, ok := args.Get(1).(map[string]any)
		require.True(t, ok)
		published = append(published, payload["payload"].(string))
	}).Return()

	// What was last published: a poll's, with a stream on it.
	streams := []apiStream{{Handle: someHandle, Title: "Dave", StreamKey: "videokey-dave"}}
	w.p.setSnapshot(&snapshot{Streams: streams, Configured: true, FetchedAt: 1234})

	// The publish armed one already; this world's first republish goes off
	// with it.
	require.Equal(t, 1, h.timers.count())
	h.timers.fire(t)
	require.Len(t, published, 1)

	for _, voter := range []string{"uid-v1", "uid-v2", "uid-v3"} {
		response := w.call(t, voter, clipCall{method: http.MethodPost, target: clipsPath + "/vote?id=" + clipA, body: `{"up":true}`})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}

	require.Equal(t, 2, h.timers.count(), "three votes, one republish")
	wait := h.timers.armed[1].wait
	require.Positive(t, wait)
	require.LessOrEqual(t, wait, republishEvery)
	require.Len(t, published, 1, "nothing until it goes off")

	before := w.p.getSnapshot().Clips.Version
	h.timers.fire(t)
	require.Len(t, published, 2)

	current := w.p.getSnapshot()
	require.EqualValues(t, 1234, current.FetchedAt, "the poll's snapshot, not a new poll")
	require.Equal(t, streams, current.Streams)
	require.NotEqual(t, before, current.Clips.Version)
	require.Contains(t, published[1], current.Clips.Version)

	// A vote that changes nothing writes nothing and arms nothing; the next
	// one that does arms the next republish.
	response := w.call(t, "uid-v1", clipCall{method: http.MethodPost, target: clipsPath + "/vote?id=" + clipA, body: `{"up":true}`})
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, 2, h.timers.count())

	response = w.call(t, "uid-v1", clipCall{method: http.MethodPost, target: clipsPath + "/vote?id=" + clipA, body: `{"up":false}`})
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, 3, h.timers.count())
	h.timers.fire(t)
	require.Len(t, published, 3)

	lock.Lock()
	require.Empty(t, asked, "Broadcast Box was never asked")
	lock.Unlock()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// Linked people report clips, once each and never their own; admins see the
// reported ones first and are told how many there are, clear the reports, and
// take a picture down, after which the card falls back to the streamer's.
func TestReportsAndPictureRemoval(t *testing.T) {
	w, h := startedClipsWorld(t, "uid-bob", "uid-dave", "uid-dan", "uid-boss")
	clipsAdmins(w.api, "uid-boss")
	publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-2*time.Hour), nil), true)
	publishFixtureClip(t, w.p, testClip(clipB, "uid-dave", clipsNow.Add(-time.Hour), nil), false)
	publishFixtureClip(t, w.p, testClip(clipC, "uid-dave", clipsNow.Add(-31*24*time.Hour), nil), true)

	report := func(userID, id string) *httptest.ResponseRecorder {
		return w.call(t, userID, clipCall{method: http.MethodPost, target: clipsPath + "/report?id=" + id})
	}

	response := report("uid-dave", clipA)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"ok":true}`, response.Body.String())
	require.Equal(t, map[string]int64{"uid-dave": clipsNow.UnixMilli()}, w.stored(t).clip(clipA).Reports)
	w.api.AssertCalled(t, "LogInfo", "A clip was reported", "user_id", "uid-dave", "clip_id", clipA)

	before := w.kvGet(kvClips)
	require.Equal(t, http.StatusOK, report("uid-dave", clipA).Code)
	require.Equal(t, before, w.kvGet(kvClips), "reporting again changes nothing")

	require.Equal(t, http.StatusOK, report("uid-dan", clipA).Code)
	requireClipError(t, report("uid-bob", clipA), http.StatusForbidden, clipErrForbidden, "own clip")
	requireClipError(t, report("uid-dan", clipC), http.StatusGone, clipErrClipGone, "expired")
	requireClipError(t, report("uid-dan", "x"), http.StatusBadRequest, clipErrBadRequest, "bad id")

	clips := decodeClipList(t, w.call(t, "uid-dave", clipCall{method: http.MethodGet, target: clipsPath}))
	require.True(t, clips[1].Reported)
	require.Nil(t, clips[1].Reports, "how often is for admins")

	view := decodeAdminClips(t, w.call(t, "uid-boss", clipCall{method: http.MethodGet, target: adminClipsPath}))
	require.Equal(t, clipA, view.Clips[0].ID, "reported first, though older")
	require.Equal(t, 2, *view.Clips[0].Reports)
	require.Equal(t, 0, *view.Clips[1].Reports)
	require.Len(t, view.Clips, 2)
	require.Contains(t, w.p.clipsAlertFor(w.p.getConfiguration()), "1 clip has been reported")

	// Clearing them.
	response = w.call(t, "uid-boss", clipCall{method: http.MethodDelete, target: adminClipsPath + "/reports?id=" + clipA})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Empty(t, w.stored(t).clip(clipA).Reports)
	require.NotContains(t, w.p.clipsAlertFor(w.p.getConfiguration()), "reported")
	w.api.AssertCalled(t, "LogInfo", "An admin cleared the reports on a clip", "admin_id", "uid-boss", "clip_id", clipA)
	before = w.kvGet(kvClips)
	response = w.call(t, "uid-boss", clipCall{method: http.MethodDelete, target: adminClipsPath + "/reports?id=" + clipA})
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, before, w.kvGet(kvClips), "nothing to clear, nothing written")
	response = w.call(t, "uid-boss", clipCall{method: http.MethodDelete, target: adminClipsPath + "/reports?id=" + clipC})
	requireClipError(t, response, http.StatusGone, clipErrClipGone, "expired")

	// The picture.
	thumb := w.stored(t).clip(clipA).Thumb
	response = w.call(t, "uid-dave", clipCall{method: http.MethodGet, target: clipsPath + "/thumb?id=" + clipA + "&v=" + thumb})
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "image/jpeg", response.Header().Get("Content-Type"))
	require.Equal(t, "private, no-cache", response.Header().Get("Cache-Control"), "whatever version was asked for")
	require.Equal(t, testJPEG(t), response.Body.Bytes())

	etag := response.Header().Get("ETag")
	response = w.call(t, "uid-dave", clipCall{method: http.MethodGet, target: clipsPath + "/thumb?id=" + clipA, header: map[string]string{"If-None-Match": etag}})
	require.Equal(t, http.StatusNotModified, response.Code)

	version := w.p.clipsDocumentView(t)
	response = w.call(t, "uid-boss", clipCall{method: http.MethodDelete, target: adminClipsPath + "/thumb?id=" + clipA})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Empty(t, w.stored(t).clip(clipA).Thumb)
	require.False(t, exists(filepath.Join(h.root, clipsFilesDir, clipA+clipThumbExt)))
	require.NotEqual(t, version, w.p.clipsDocumentView(t), "the list shows it")
	w.api.AssertCalled(t, "LogInfo", "A clip's picture was removed", "user_id", "uid-boss", "clip_id", clipA)

	response = w.call(t, "uid-dave", clipCall{method: http.MethodGet, target: clipsPath + "/thumb?id=" + clipA})
	require.Equal(t, http.StatusNotFound, response.Code)
	response = w.call(t, "uid-boss", clipCall{method: http.MethodGet, target: adminClipsPath + "/thumb?id=" + clipA})
	require.Equal(t, http.StatusNotFound, response.Code)

	before = w.kvGet(kvClips)
	response = w.call(t, "uid-boss", clipCall{method: http.MethodDelete, target: adminClipsPath + "/thumb?id=" + clipA})
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, before, w.kvGet(kvClips))
	response = w.call(t, "uid-boss", clipCall{method: http.MethodDelete, target: adminClipsPath + "/thumb?id=" + clipC})
	requireClipError(t, response, http.StatusGone, clipErrClipGone, "expired")
	require.True(t, exists(filepath.Join(h.root, clipsFilesDir, clipC+clipThumbExt)), "an expired clip's files are the sweep's")
}

// clipsDocumentView is the snapshot's version of the clips, as a poll would
// put it now.
func (p *Plugin) clipsDocumentView(t *testing.T) string {
	t.Helper()

	snap := &snapshot{}
	p.addClips(snap, p.getConfiguration(), p.clips().now())

	return snap.Clips.Version
}

// Taking a clip down from the panel, in exactly the order A§3.3 gives: past
// the gate, a malformed id; then a token; then a clip already gone, which is
// left to the sweep when it is only past its time; then its clipper, an
// admin, and its stream's streamer by the link they have now; nobody else.
func TestDeleteRouteFollowsItsOrder(t *testing.T) {
	w, h := startedClipsWorld(t, "uid-bob", "uid-dave", "uid-dan", "uid-boss", "uid-v1")
	clipsAdmins(w.api, "uid-boss")
	tag := w.p.streamTag("videokey-dave")

	of := func(id, clipper string, publishedAt time.Time) clipEntry {
		entry := testClip(id, clipper, publishedAt, votesAt(clipsNow.Add(-time.Minute), "uid-v1"))
		entry.StreamTag = tag

		return publishFixtureClip(t, w.p, entry, true)
	}
	remove := func(userID, id string) *httptest.ResponseRecorder {
		return w.call(t, userID, clipCall{method: http.MethodDelete, target: clipsPath + "/clip?id=" + id})
	}

	of(clipA, "uid-bob", clipsNow.Add(-4*time.Hour))
	of(clipB, "uid-bob", clipsNow.Add(-3*time.Hour))
	of(clipC, "uid-bob", clipsNow.Add(-2*time.Hour))
	expired := of(clipD, "uid-bob", clipsNow.Add(-31*24*time.Hour))

	requireClipError(t, remove("uid-dan", "zz"), http.StatusBadRequest, clipErrBadRequest, "malformed")
	requireClipError(t, remove("uid-dan", clipA), http.StatusForbidden, clipErrNotAllowed, "somebody else")

	response := remove("uid-dan", "00000000000000ff")
	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `{"ok":true}`, response.Body.String())

	// Past its time: answered as gone, and left for the sweep to bank.
	rev := w.stored(t).Rev
	require.Equal(t, http.StatusOK, remove("uid-bob", expired.ID).Code)
	require.Equal(t, rev, w.stored(t).Rev)
	require.NotNil(t, w.stored(t).clip(expired.ID))

	// The clipper: the upvotes go with it.
	require.Equal(t, http.StatusOK, remove("uid-bob", clipA).Code)
	require.Nil(t, w.stored(t).clip(clipA))
	require.Zero(t, w.stored(t).Banked["uid-bob"].Up)
	require.False(t, exists(filepath.Join(h.root, clipsFilesDir, clipA+clipVideoExt)))
	require.False(t, exists(filepath.Join(h.root, clipsFilesDir, clipA+clipThumbExt)))
	w.api.AssertCalled(t, "LogInfo", "A clip was deleted", "user_id", "uid-bob", "clip_id", clipA, "reason", clipGoneClipper, "upvotes_banked", 0)

	// The streamer, by the key he has linked now, whatever its case: the
	// clipper keeps them.
	w.link("uid-dave", "VideoKey-Dave")
	w.p.clipsLinkChanged("uid-dave")
	w.p.clips().background.Wait()
	require.Equal(t, http.StatusOK, remove("uid-dave", clipB).Code)
	require.Equal(t, 1, w.stored(t).Banked["uid-bob"].Up)
	w.api.AssertCalled(t, "LogInfo", "A clip was deleted", "user_id", "uid-dave", "clip_id", clipB, "reason", clipGoneStreamer, "upvotes_banked", 1)

	// Once Dan proves Dave's key, it is Dan's stream and not Dave's: Dave is
	// not linked at all, and once he links another stream, that one is his.
	require.NoError(t, w.p.setLink("uid-dan", "videokey-dave", "token"))
	w.p.clips().background.Wait()
	requireClipError(t, remove("uid-dave", clipC), http.StatusForbidden, clipErrLocked, "unlinked by Dan")
	w.link("uid-dave", "videokey-dave-two")
	requireClipError(t, remove("uid-dave", clipC), http.StatusForbidden, clipErrNotAllowed, "the old streamer")

	// A banking delete waits while a voter cannot be looked up.
	w.failUser("uid-v1", true)
	w.p.clips().forgetLink("uid-v1")
	w.p.clips().peopleLock.Lock()
	delete(w.p.clips().people, "uid-v1")
	w.p.clips().peopleLock.Unlock()
	requireClipError(t, remove("uid-dan", clipC), http.StatusServiceUnavailable, clipErrStore, "an unknown voter")
	require.NotNil(t, w.stored(t).clip(clipC))
	w.failUser("uid-v1", false)
	setClipsClock(w.p, clipsNow.Add(2*time.Minute))
	require.Equal(t, http.StatusOK, remove("uid-dan", clipC).Code)
	require.Equal(t, 2, w.stored(t).Banked["uid-bob"].Up)

	// A linked admin who is not the clipper: void.
	of(clipB, "uid-dave", clipsNow.Add(-time.Hour))
	require.Equal(t, http.StatusOK, remove("uid-boss", clipB).Code)
	require.Zero(t, w.stored(t).Banked["uid-dave"].Up)

	// Thirty deletes a minute.
	for range 30 {
		remove("uid-v1", "00000000000000ff")
	}
	requireClipError(t, remove("uid-v1", "00000000000000ff"), http.StatusTooManyRequests, clipErrRateLimited, "metered")
}

// The admin console's own delete, block, unblock and score reset.
func TestAdminClipWrites(t *testing.T) {
	w, _ := startedClipsWorld(t, "uid-bob", "uid-dave", "uid-dan", "uid-boss")
	clipsAdmins(w.api, "uid-boss")
	publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), votesAt(clipsNow.Add(-time.Minute), "uid-dave", "uid-dan")), false)
	publishFixtureClip(t, w.p, testClip(clipB, "uid-dave", clipsNow.Add(-time.Hour), votesAt(clipsNow.Add(-time.Minute), "uid-dan")), false)
	require.Equal(t, "uid-bob", w.holder(t))

	admin := func(method, target, body string) *httptest.ResponseRecorder {
		return w.call(t, "uid-boss", clipCall{method: method, target: target, body: body})
	}

	response := admin(http.MethodPost, adminClipsPath+"/block", `{"userId":"uid-bob"}`)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"ok":true,"draftsRemoved":0}`, response.Body.String())
	doc := w.stored(t)
	require.Equal(t, clipMark{By: "uid-boss", At: clipsNow.UnixMilli()}, doc.Blocked["uid-bob"])
	require.Equal(t, "uid-dave", doc.Holder.UserID, "in the same write")
	w.api.AssertCalled(t, "LogInfo", "An admin stopped somebody making clips", "admin_id", "uid-boss", "user_id", "uid-bob")

	view := decodeAdminClips(t, admin(http.MethodGet, adminClipsPath, ""))
	require.Len(t, view.Blocked, 1)
	require.Equal(t, adminBlockedView{UserID: "uid-bob", Username: "bob", DisplayName: "Bob", BlockedAt: clipsNow.UnixMilli(), By: "uid-boss"}, view.Blocked[0])
	require.Equal(t, "uid-dave", view.BestClipper.UserID)

	requireClipError(t, admin(http.MethodPost, adminClipsPath+"/block", `{"userId":"uid-nobody"}`), http.StatusBadRequest, clipErrBadRequest, "nobody")
	requireClipError(t, admin(http.MethodPost, adminClipsPath+"/block", `{}`), http.StatusBadRequest, clipErrBadRequest, "no id")

	response = admin(http.MethodDelete, adminClipsPath+"/block?userId=uid-bob", "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Empty(t, w.stored(t).Blocked)
	require.Equal(t, "uid-bob", w.holder(t), "ahead again")
	w.api.AssertCalled(t, "LogInfo", "An admin let somebody make clips again", "admin_id", "uid-boss", "user_id", "uid-bob")

	response = admin(http.MethodPost, adminClipsPath+"/reset-score", `{"userId":"uid-bob"}`)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, clipMark{By: "uid-boss", At: clipsNow.UnixMilli()}, w.stored(t).Resets["uid-bob"])
	require.Equal(t, "uid-dave", w.holder(t), "Bob's votes so far no longer count toward it")
	requireClipError(t, admin(http.MethodPost, adminClipsPath+"/reset-score", `not json`), http.StatusBadRequest, clipErrBadRequest, "not json")

	response = admin(http.MethodDelete, adminClipsPath+"/clip?id="+clipB, "")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Nil(t, w.stored(t).clip(clipB))
	require.Zero(t, w.stored(t).Banked["uid-dave"].Up, "an admin's delete voids the upvotes")
	require.Empty(t, w.holder(t), "nobody has any that count")
	require.Equal(t, http.StatusOK, admin(http.MethodDelete, adminClipsPath+"/clip?id="+clipB, "").Code, "already gone")
	requireClipError(t, admin(http.MethodDelete, adminClipsPath+"/clip?id=B2", ""), http.StatusBadRequest, clipErrBadRequest, "malformed")

	// Thirty writes a minute, an admin's as much as anybody's.
	limited := 0
	for range 30 {
		if admin(http.MethodDelete, adminClipsPath+"/clip?id="+clipB, "").Code == http.StatusTooManyRequests {
			limited++
		}
	}
	require.Positive(t, limited)
	requireClipError(t, admin(http.MethodDelete, adminClipsPath+"/clip?id="+clipB, ""), http.StatusTooManyRequests, clipErrRateLimited, "metered")
}

// A locked caller learns nothing about a clip by asking: one that exists,
// one past its time, one that never did and an id that is nonsense all get
// the same answer, byte for byte, on every route that takes an id — the
// clip's own clipper included, who is not linked.
func TestLockedComesBeforeLookup(t *testing.T) {
	w, _ := startedClipsWorld(t, "uid-bob", "uid-dave")
	publishFixtureClip(t, w.p, testClip(clipA, "uid-dan", clipsNow.Add(-time.Hour), nil), true)
	publishFixtureClip(t, w.p, testClip(clipB, "uid-dan", clipsNow.Add(-31*24*time.Hour), nil), true)
	w.person("uid-dan")

	// The pictures' real ETag, which does match: somebody linked who holds
	// the picture is told it has not changed.
	etag := testJPEGETag(t)
	held := w.call(t, "uid-bob", clipCall{method: http.MethodGet, target: clipsPath + "/thumb?id=" + clipA, header: map[string]string{"If-None-Match": etag}})
	require.Equal(t, http.StatusNotModified, held.Code)

	reads := w.linkReads("uid-dan")
	var first []byte

	for _, id := range []string{clipA, clipB, clipC, "../../etc/passwd", ""} {
		for _, c := range []clipCall{
			{method: http.MethodGet, target: clipsPath + "/clip?id=" + id},
			{method: http.MethodDelete, target: clipsPath + "/clip?id=" + id},
			{method: http.MethodGet, target: clipsPath + "/video?id=" + id, header: map[string]string{"Range": "bytes=0-9"}},
			{method: http.MethodHead, target: clipsPath + "/video?id=" + id},
			{method: http.MethodGet, target: clipsPath + "/thumb?id=" + id, header: map[string]string{"If-None-Match": etag}},
			{method: http.MethodPost, target: clipsPath + "/vote?id=" + id, body: `{"up":true}`},
			{method: http.MethodPost, target: clipsPath + "/report?id=" + id},
			{method: http.MethodPost, target: clipsPath + "/share?id=" + id, body: `{"channelId":"` + someChannel + `"}`},
			{method: http.MethodGet, target: clipsPath + "/cards?ids=" + id},
		} {
			response := w.call(t, "uid-dan", c)
			requireClipError(t, response, http.StatusForbidden, clipErrLocked, describe(c))
			if first == nil {
				first = response.Body.Bytes()
			}
			require.True(t, bytes.Equal(first, response.Body.Bytes()), describe(c))
		}
	}
	require.Equal(t, 1, reads(), "the link once: the refusal is remembered for two seconds, on a clock that has not moved")

	doc := w.stored(t)
	require.NotNil(t, doc.clip(clipA), "nothing was deleted")
	require.Empty(t, doc.clip(clipA).Voters)
	require.Empty(t, doc.clip(clipA).Reports)
}

// The cards of shared clips: up to 25 at once, with the count of thumbs up
// the same for every reader and, from 3.2.0, whether this reader voted and
// may vote, but never who voted. A clip that has gone is null. Mattermost is
// not asked whether anybody is an admin.
func TestCardsRoute(t *testing.T) {
	w, _ := startedClipsWorld(t, "uid-bob", "uid-dave", "uid-dan")
	clipsAdmins(w.api)
	publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), votesAt(clipsNow.Add(-time.Minute), "uid-dan")), true)
	publishFixtureClip(t, w.p, testClip(clipB, "uid-bob", clipsNow.Add(-31*24*time.Hour), nil), false)
	reported := w.stored(t).clip(clipA)
	require.NotNil(t, reported)

	cards := func(userID, ids string) *httptest.ResponseRecorder {
		return w.call(t, userID, clipCall{method: http.MethodGet, target: clipsPath + "/cards?ids=" + ids})
	}

	response := cards("uid-dave", clipA+","+clipB+","+clipC)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

	var answer clipCardsResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer))
	require.Len(t, answer.Cards, 3)
	require.Nil(t, answer.Cards[clipB], "expired")
	require.Nil(t, answer.Cards[clipC], "never was")
	require.Equal(t, &clipCardView{
		ID: clipA, Title: reported.Title, CreatedAt: reported.CreatedAt, DurationMs: reported.DurationMs, Thumb: reported.Thumb,
		// Thirty days, the retention in force: the clip's own expiresAt.
		ExpiresAt: reported.CreatedAt + int64(30*24*time.Hour/time.Millisecond),
		Clipper:   clipPerson{UserID: "uid-bob", Username: "bob", DisplayName: "Bob", Name: "Bob"},
		Streamer:  clipPerson{UserID: "uid-dave", Username: "dave", DisplayName: "Dave", Name: "Dave"},
		Upvotes:   1, CanVote: true,
	}, answer.Cards[clipA])

	var raw map[string]map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &raw))
	for _, field := range []string{"voted", "mine", "canDelete", "voters", "reports", "streamTag", "shares", "handle"} {
		require.NotContains(t, raw["cards"][clipA], field)
	}
	require.NotContains(t, response.Body.String(), "uid-dan", "nobody who voted")

	// The count is the same for somebody else; that they voted is theirs.
	var dans clipCardsResponse
	require.NoError(t, json.Unmarshal(cards("uid-dan", clipA+","+clipB+","+clipC).Body.Bytes(), &dans))
	require.Equal(t, 1, dans.Cards[clipA].Upvotes)
	require.True(t, dans.Cards[clipA].Voted)

	// Twenty-five, as the webapp batches them; not the constant, so that a
	// change to it shows up here.
	ids := []string{}
	for i := range 25 {
		ids = append(ids, clipIDFor(i))
	}
	require.Equal(t, http.StatusOK, cards("uid-dave", strings.Join(ids, ",")).Code)

	for about, value := range map[string]string{
		"26 ids":     strings.Join(append(ids, clipIDFor(99)), ","),
		"none":       "",
		"twice":      clipA + "," + clipA,
		"malformed":  clipA + ",nope",
		"a trailing": clipA + ",",
		"spaces":     clipA + ",%20" + clipB,
	} {
		requireClipError(t, cards("uid-dave", value), http.StatusBadRequest, clipErrBadRequest, about)
	}

	w.api.AssertNotCalled(t, "HasPermissionTo", mock.Anything, mock.Anything)
}

// The media routes and the cards take a yes to "is this person linked" as
// good for ten seconds, and nothing else does: a no and a failure are asked
// again every time, and a link changing is seen at once.
func TestMediaLinkCacheExpires(t *testing.T) {
	w, _ := startedClipsWorld(t, "uid-bob", "uid-dave")
	clipsAdmins(w.api)
	publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil), true)
	w.person("uid-dan")

	current := clipsNow
	w.p.clips().now = func() time.Time { return current }

	media := []clipCall{
		{method: http.MethodGet, target: clipsPath + "/video?id=" + clipA, header: map[string]string{"Range": "bytes=0-9"}},
		{method: http.MethodHead, target: clipsPath + "/video?id=" + clipA},
		{method: http.MethodGet, target: clipsPath + "/thumb?id=" + clipA},
		{method: http.MethodGet, target: clipsPath + "/cards?ids=" + clipA},
		{method: http.MethodGet, target: clipsPath + "/draft/video?id=" + clipA},
	}

	dave := w.linkReads("uid-dave")
	for _, c := range media {
		response := w.call(t, "uid-dave", c)
		require.NotEqual(t, http.StatusForbidden, response.Code, describe(c))
	}
	require.Equal(t, 1, dave(), "one read for all of them")

	current = clipsNow.Add(9 * time.Second)
	for _, c := range media {
		w.call(t, "uid-dave", c)
	}
	require.Equal(t, 1, dave())

	// Every other route asks every time.
	for range 3 {
		w.call(t, "uid-dave", clipCall{method: http.MethodGet, target: clipsPath})
	}
	require.Equal(t, 4, dave())

	current = clipsNow.Add(10 * time.Second)
	w.call(t, "uid-dave", media[0])
	require.Equal(t, 5, dave(), "ten seconds")

	// Unlinking is seen at once.
	w.unlink("uid-dave")
	w.p.clips().background.Wait()
	for _, c := range media {
		requireClipError(t, w.call(t, "uid-dave", c), http.StatusForbidden, clipErrLocked, describe(c))
	}
	require.Equal(t, 6, dave(), "and a no is remembered by the gate, not this cache: for two seconds")

	dan := w.linkReads("uid-dan")
	for range 3 {
		requireClipError(t, w.call(t, "uid-dan", media[3]), http.StatusForbidden, clipErrLocked, "dan")
	}
	require.Equal(t, 1, dan())

	current = current.Add(accessNoTTL)
	requireClipError(t, w.call(t, "uid-dan", media[3]), http.StatusForbidden, clipErrLocked, "dan, two seconds on")
	require.Equal(t, 2, dan(), "asked again once the refusal is two seconds old")
}

// The snapshot carries counts and the award, never a clip: no title, no
// picture, no voter, no shared post, no stream key and no stream tag, for a
// live stream being recorded and the held end of another, both with a
// lower-case key, as much as anything (A§3.11).
func TestSnapshotCarriesNoClipContent(t *testing.T) {
	w, _ := startedClipsWorld(t, "uid-bob", "uid-dave", "uid-dan")
	cfg := w.p.getConfiguration()

	entry := testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), votesAt(clipsNow.Add(-time.Minute), "uid-dan"))
	entry.Title = "SECRET-TITLE"
	entry.StreamTag = w.p.streamTag("videokey-dave")
	entry.Shares = []string{"post-of-a-share-xyz"}
	entry.Reports = map[string]int64{"uid-dan": 1}
	entry.Tag = &clipTag{Category: categoryGame, Title: "SECRET-CLIP-TAG"}
	entry = publishFixtureClip(t, w.p, entry, true)
	require.NotEmpty(t, entry.Thumb)

	streams := w.p.decorate([]bbStream{rawLive("videokey-dave")}, cfg)
	require.Len(t, streams, 1)
	require.Equal(t, "uid-dave", streams[0].UserID)

	// Dave's stream is being recorded, and Bob's ended a moment ago and its
	// end is held.
	recorders := map[string]*fakeRecorder{}
	w.p.recorders().newRecorder = func(handle, dir string, onEvent func(recorderEvent)) clipRecorderHandle {
		recorders[handle] = &fakeRecorder{handle: handle, dir: dir, onEvent: onEvent}

		return recorders[handle]
	}
	withConfig := cfg.Clone()
	withConfig.BroadcastBoxURL = "http://192.168.1.20:8080"
	w.p.setConfiguration(withConfig)
	bob := w.p.decorate([]bbStream{rawLive("videokey-bob")}, cfg)
	w.p.recorders().reconcile(append(append([]apiStream{}, streams...), bob...), withConfig, clipsNow, true)
	bobRecorder := recorders[w.p.streamHandle("videokey-bob")]
	bobRecorder.holds(heldRing(t, bobRecorder.dir, clipsNow, 20*time.Second))
	w.p.recorders().reconcile(streams, withConfig, clipsNow.Add(time.Second), true)
	w.p.recorders().reconcile(streams, withConfig, clipsNow.Add(2*time.Second), true)
	dave := recorders[streams[0].Handle]
	dave.recording(liveRing(t, filepath.Join(dave.dir, "live"), clipsNow, 30*time.Second))

	snapshotNow := func() *snapshot {
		snap := &snapshot{Streams: append([]apiStream(nil), streams...), Configured: true, FetchedAt: 1}
		w.p.addClips(snap, cfg, clipsNow)

		return snap
	}
	before := snapshotNow()

	// A draft of the same stream, cut by Dan while it was tagged, kept on
	// this node: nothing of it is anybody else's business.
	drafts := &w.p.clips().drafts
	tagged := streams[0]
	tagged.NowPlaying = &apiNowPlaying{Category: categoryGame, Title: "SECRET-DRAFT-TAG"}
	draft, err := drafts.reserve("uid-dan", clipSourceLive, w.p.captureOf(tagged), func(string) bool { return false }, context.Background())
	require.NoError(t, err)
	_, kept := drafts.complete(draft.id, exportResult{durationMs: 30000, bytes: 1000, width: 320, height: 180}, clipsNow)
	require.True(t, kept)
	require.Equal(t, entry.StreamTag, draft.capture.StreamTag)

	snap := snapshotNow()
	require.Equal(t, clipStateReady, snap.Streams[0].Clip)
	require.NotZero(t, snap.Streams[0].ClipFrom)
	require.Len(t, snap.ClipTails, 1)
	require.Equal(t, w.p.streamHandle("videokey-bob"), snap.ClipTails[0].Handle)
	require.Equal(t, 1, snap.Clips.Count)
	require.Equal(t, "uid-bob", snap.BestClipper.UserID)
	require.Equal(t, clipA, snap.BestClipper.BestClip.ID)
	require.Equal(t, before.Clips.Version, snap.Clips.Version, "a draft changes nothing the snapshot says")

	encoded, err := json.Marshal(snap)
	require.NoError(t, err)
	for _, secret := range []string{"SECRET-TITLE", entry.Thumb, "uid-dan", "post-of-a-share-xyz", "videokey-dave", "videokey-bob", entry.StreamTag, w.p.streamTag("videokey-bob"), "streamTag", "shares", "SECRET-CLIP-TAG", draft.id, "SECRET-DRAFT-TAG"} {
		require.NotContains(t, strings.ToLower(string(encoded)), strings.ToLower(secret))
	}

	// Nor can the title be had back from the version. Everything else that
	// goes into it is in the snapshot, or is 8 hex or nothing, so an unkeyed
	// hash would let anybody check a guess at the title.
	require.NotEmpty(t, snap.Clips.Version)
	for _, thumb := range []string{entry.Thumb, ""} {
		line := fmt.Sprintf("%s|%s|%d|%s\n", snap.BestClipper.BestClip.ID, "SECRET-TITLE", snap.BestClipper.Upvotes, thumb)
		guess := sha256.Sum256([]byte(line))
		require.NotEqual(t, hex.EncodeToString(guess[:])[:16], snap.Clips.Version)
	}
}

// The snapshot is one object for everybody: building it twice, for nobody in
// particular, gives the same bytes, however the document's maps fall, with
// clips, drafts, recorders and tails all present (spec §2.19 item 19), and
// none of them is a stream key.
func TestSnapshotCarriesNoStreamKeyAndNothingPerViewer(t *testing.T) {
	w, _ := startedClipsWorld(t, "uid-bob", "uid-dave", "uid-dan", "uid-v1", "uid-v2")
	cfg := w.p.getConfiguration()
	publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), votesAt(clipsNow.Add(-time.Minute), "uid-dan", "uid-v1", "uid-v2")), true)
	publishFixtureClip(t, w.p, testClip(clipB, "uid-dave", clipsNow.Add(-2*time.Hour), votesAt(clipsNow.Add(-time.Minute), "uid-v1")), false)

	// Both streams recorded, Dave's ready, and the end of an earlier stretch
	// of Bob's held after it dropped; a draft of Dave's on this node.
	recorders := map[string]*fakeRecorder{}
	w.p.recorders().newRecorder = func(handle, dir string, onEvent func(recorderEvent)) clipRecorderHandle {
		recorders[handle] = &fakeRecorder{handle: handle, dir: dir, onEvent: onEvent}

		return recorders[handle]
	}
	withURL := cfg.Clone()
	withURL.BroadcastBoxURL = "http://192.168.1.20:8080"
	w.p.setConfiguration(withURL)
	onAir := w.p.decorate([]bbStream{rawLive("videokey-dave"), rawLive("videokey-bob")}, cfg)
	w.p.recorders().reconcile(onAir, withURL, clipsNow, true)
	dave := recorders[w.p.streamHandle("videokey-dave")]
	dave.recording(liveRing(t, filepath.Join(dave.dir, "live"), clipsNow, 30*time.Second))
	bob := recorders[w.p.streamHandle("videokey-bob")]
	bob.handsOver(heldRing(t, filepath.Join(bob.dir, "dropped"), clipsNow, 20*time.Second), clipTailDropped)
	draft, err := w.p.clips().drafts.reserve("uid-dan", clipSourceLive, w.p.captureOf(onAir[0]), func(string) bool { return false }, context.Background())
	require.NoError(t, err)
	_, kept := w.p.clips().drafts.complete(draft.id, exportResult{durationMs: 30000}, clipsNow)
	require.True(t, kept)

	build := func() []byte {
		// Decoded afresh each time, maps in whatever order they fall.
		w.p.clips().lock.Lock()
		w.p.clips().doc = w.stored(t)
		w.p.clips().lock.Unlock()

		snap := &snapshot{Streams: w.p.decorate([]bbStream{rawLive("videokey-dave"), rawLive("videokey-bob")}, cfg), FetchedAt: 1}
		w.p.addClips(snap, cfg, clipsNow)

		encoded, err := json.Marshal(snap)
		require.NoError(t, err)

		return encoded
	}

	first := build()
	for range 20 {
		require.Equal(t, string(first), string(build()))
	}
	require.Contains(t, string(first), `"clip":"ready"`)
	require.Contains(t, string(first), `"clipTails":[`)
	for _, key := range []string{"videokey-dave", "videokey-bob", "VILLANET"} {
		require.NotContains(t, strings.ToLower(string(first)), strings.ToLower(key))
	}
	for _, perViewer := range []string{"voted", "mine", "canDelete", "ofMyStream"} {
		require.NotContains(t, string(first), perViewer)
	}
}

// /me tells somebody who has linked a stream whether it is kept for clipping,
// and an admin what needs their attention, linked or not; and tells nobody
// else anything about clips.
func TestMeSaysWhatThereIsOfClips(t *testing.T) {
	w, _ := startedClipsWorld(t, "uid-bob", "uid-dave")
	w.person("uid-boss")
	w.person("uid-dan")
	clipsAdmins(w.api, "uid-boss")
	publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-2*24*time.Hour), nil), false)

	me := func(userID string) meResponse {
		recorder := httptest.NewRecorder()
		w.p.handleMe(recorder, userID)
		require.Equal(t, http.StatusOK, recorder.Code)

		var answer meResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer))

		return answer
	}

	require.Equal(t, &clipsStreamerView{Allowed: true}, me("uid-dave").ClipsStreamer)
	require.Empty(t, me("uid-dave").ClipsAlert)
	require.Nil(t, me("uid-dan").ClipsStreamer)
	require.Empty(t, me("uid-boss").ClipsAlert, "nothing to say")

	// Dave's stream switched off, by its tag.
	_, err := w.p.clipsCAS(clipsNow, func(doc *clipsDoc, write *clipsWrite) (bool, error) {
		doc.OptOut = map[string]clipMark{w.p.streamTag("VideoKey-Dave"): {By: "uid-dave", At: write.now}}

		return true, nil
	})
	require.NoError(t, err)
	require.Equal(t, &clipsStreamerView{Allowed: false}, me("uid-dave").ClipsStreamer)
	require.Equal(t, &clipsStreamerView{Allowed: true}, me("uid-bob").ClipsStreamer)

	// Reported, and the setting lowered from 30 days to 1.
	_, err = w.p.clipsCAS(clipsNow, func(doc *clipsDoc, write *clipsWrite) (bool, error) {
		doc.Retention = write.kept
		doc.clip(clipA).Reports = map[string]int64{"uid-dave": write.now}

		return true, nil
	})
	require.NoError(t, err)
	cfg := w.p.getConfiguration().Clone()
	cfg.ClipRetentionDays = 1
	w.p.setConfiguration(cfg)

	alert := me("uid-boss").ClipsAlert
	require.Equal(t, "1 clip has been reported · Keep clips for was lowered: 1 clip will be removed by 3 Oct 2026 21:00 UTC", alert)
	require.Empty(t, me("uid-dave").ClipsAlert)

	view := decodeAdminClips(t, w.call(t, "uid-boss", clipCall{method: http.MethodGet, target: adminClipsPath}))
	require.Equal(t, adminRetentionView{Days: 1, PendingRemovals: 1, RemovalAt: clipsNow.Add(24 * time.Hour).UnixMilli()}, view.Retention)

	// A halt says what to do about it.
	w.kvSet(kvClips, []byte(`{"v":1,"rev":`))
	_, err = w.p.readClipsFresh(clipsNow)
	require.Error(t, err)
	require.Contains(t, me("uid-boss").ClipsAlert, "disable and enable the plugin")

	view = decodeAdminClips(t, w.call(t, "uid-boss", clipCall{method: http.MethodGet, target: adminClipsPath}))
	require.Equal(t, "the clips document could not be read", view.Halted)
	require.Len(t, view.Clips, 1, "what this node last held")

	// A node with no copy of a document it cannot read does not know whether
	// a linked stream is switched off, so it shows no switch, rather than one
	// that says it is on. The link itself is still told, by streamKey.
	for _, c := range []struct {
		about  string
		breaks func(fresh *clipsWorld)
	}{
		{"the store does not answer", func(fresh *clipsWorld) { fresh.faults.failRead(kvClips) }},
		{"the document is broken", func(fresh *clipsWorld) { fresh.kvSet(kvClips, []byte(`{"v":1,"rev":`)) }},
	} {
		fresh := newClipsWorld(t, "uid-dave")
		clipsAdmins(fresh.api)
		c.breaks(fresh)

		recorder := httptest.NewRecorder()
		fresh.p.handleMe(recorder, "uid-dave")
		require.Equal(t, http.StatusOK, recorder.Code, c.about)

		var answer map[string]any
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer), c.about)
		require.Equal(t, keyOf("uid-dave"), answer["streamKey"], c.about)
		require.NotContains(t, answer, "clipsStreamer", c.about)
	}
}

// The sweep job runs on the store's schedule, at the moment it runs, and can
// be told to stop part way.
func TestSweepRunsFromItsSchedule(t *testing.T) {
	w, h := startedClipsWorld(t, "uid-bob")
	publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", time.Now().Add(-40*24*time.Hour), nil), false)

	h.job.lock.Lock()
	run := h.job.run
	h.job.lock.Unlock()
	require.NotNil(t, run, "scheduled by startClips")

	run()
	require.Empty(t, w.stored(t).Clips)
	require.Empty(t, namesIn(t, filepath.Join(h.root, clipsFilesDir)))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.False(t, w.p.sweepClipsAt(ctx, clipsNow).Ran)
}

// A rename shows the next time the list, a clip or the console is fetched:
// those name people as their accounts are now, asking about each person once
// per request however many clips name them. The cards of shared clips keep
// the ten-minute cache, since a channel can hold dozens. A user store that
// does not answer leaves the name last known, not "Unknown user".
func TestRenamesShowOnTheNextList(t *testing.T) {
	w, _ := startedClipsWorld(t, "uid-bob", "uid-dave", "uid-dan")
	w.person("uid-boss")
	clipsAdmins(w.api, "uid-boss")
	publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil), false)
	publishFixtureClip(t, w.p, testClip(clipB, "uid-bob", clipsNow.Add(-2*time.Hour), nil), false)

	rename := func(name string) {
		w.changeUser("uid-bob", func(user *model.User) { user.FirstName = name })
	}
	card := func() string {
		response := w.call(t, "uid-dan", clipCall{method: http.MethodGet, target: clipsPath + "/cards?ids=" + clipA})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())

		var answer clipCardsResponse
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer))

		return answer.Cards[clipA].Clipper.Name
	}
	list := func() []string {
		names := []string{}
		for _, view := range decodeClipList(t, w.call(t, "uid-dan", clipCall{method: http.MethodGet, target: clipsPath})) {
			names = append(names, view.Clipper.Name)
		}

		return names
	}
	one := func() string {
		response := w.call(t, "uid-dan", clipCall{method: http.MethodGet, target: clipsPath + "/clip?id=" + clipA})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())

		var view clipView
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &view))

		return view.Clipper.Name
	}

	require.Equal(t, "Bob", card())

	// The cards take the cache's word for ten minutes.
	rename("Bobby")
	require.Equal(t, "Bob", card())

	// The list asks, once for both of Bob's clips, and what it hears is kept.
	before := w.lookupsOf("uid-bob")
	require.Equal(t, []string{"Bobby", "Bobby"}, list())
	require.Equal(t, before+1, w.lookupsOf("uid-bob"))
	require.Equal(t, "Bobby", card())

	rename("Bobbi")
	require.Equal(t, "Bobbi", one())

	rename("Bobbie")
	view := decodeAdminClips(t, w.call(t, "uid-boss", clipCall{method: http.MethodGet, target: adminClipsPath}))
	require.Len(t, view.Clips, 2)
	for _, clip := range view.Clips {
		require.Equal(t, "Bobbie", clip.Clipper.Name)
	}
	require.Len(t, view.People, 1)
	require.Equal(t, "Bobbie", view.People[0].DisplayName)

	// The user store stops answering: the last name known stands, and the
	// store is not asked again straight away.
	w.failUser("uid-bob", true)
	rename("Robert")
	require.Equal(t, []string{"Bobbie", "Bobbie"}, list())
	failed := w.lookupsOf("uid-bob")
	require.Equal(t, "Bobbie", one())
	require.Equal(t, failed, w.lookupsOf("uid-bob"))
}
