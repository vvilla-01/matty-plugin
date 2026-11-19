package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	someClipID  = "3f9a0c2e7b1d4a55"
	otherClipID = "0011223344556677"
	someHandle  = "0123456789abcdef0123456789abcdef"
	someChannel = "abcdefghijklmnopqrstuvwxyz"
)

// One request a browser could make, with whatever it carries.
type clipCall struct {
	method string
	target string
	body   string
	header map[string]string
}

// Every route and method under /api/v1/clips, as the panel would call it and as
// somebody probing it might: well-formed and malformed ids, a body that reads
// and one that does not, a Range, and a thumbnail the browser already holds.
var panelClipCalls = []clipCall{
	{method: http.MethodGet, target: "/api/v1/clips"},
	{method: http.MethodGet, target: "/api/v1/clips/clip?id=" + someClipID},
	{method: http.MethodGet, target: "/api/v1/clips/clip?id=not-an-id"},
	{method: http.MethodDelete, target: "/api/v1/clips/clip?id=" + someClipID},
	{method: http.MethodDelete, target: "/api/v1/clips/clip"},
	{method: http.MethodGet, target: "/api/v1/clips/cards?ids=" + someClipID + "," + otherClipID},
	{method: http.MethodGet, target: "/api/v1/clips/cards?ids=" + strings.Repeat(someClipID+",", 26)},
	{method: http.MethodGet, target: "/api/v1/clips/video?id=" + someClipID, header: map[string]string{"Range": "bytes=0-99"}},
	{method: http.MethodHead, target: "/api/v1/clips/video?id=" + someClipID},
	{method: http.MethodGet, target: "/api/v1/clips/thumb?id=" + someClipID + "&v=a1b2c3d4", header: map[string]string{"If-None-Match": `"a1b2c3d4"`}},
	{method: http.MethodPost, target: "/api/v1/clips/vote?id=" + someClipID, body: `{"up":true}`},
	{method: http.MethodPost, target: "/api/v1/clips/vote?id=../../x", body: `{"up":`},
	{method: http.MethodPost, target: "/api/v1/clips/report?id=" + someClipID},
	{method: http.MethodPost, target: "/api/v1/clips/share?id=" + someClipID, body: `{"channelId":"` + someChannel + `"}`},
	{method: http.MethodPost, target: "/api/v1/clips/share?id=" + someClipID, body: `{"channelId":"town-square"}`},
	{method: http.MethodGet, target: "/api/v1/clips/drafts"},
	{method: http.MethodPost, target: "/api/v1/clips/drafts", body: `{"handle":"` + someHandle + `","source":"live"}`},
	{method: http.MethodPost, target: "/api/v1/clips/drafts", body: `{"handle":"nope","source":"sideways"}`},
	{method: http.MethodDelete, target: "/api/v1/clips/draft?id=" + someClipID},
	{method: http.MethodGet, target: "/api/v1/clips/draft/video?id=" + someClipID, header: map[string]string{"Range": "bytes=100-199"}},
	{method: http.MethodHead, target: "/api/v1/clips/draft/video?id=" + someClipID},
	{method: http.MethodPut, target: "/api/v1/clips/draft/thumb?id=" + someClipID, body: "\xff\xd8\xff\xe0not really a picture", header: map[string]string{"Content-Type": "image/jpeg"}},
	{method: http.MethodPost, target: "/api/v1/clips/draft/publish?id=" + someClipID, body: `{"title":"that jump","inMs":0,"outMs":45000}`},
	{method: http.MethodPut, target: "/api/v1/clips/streamer", body: `{"allow":false}`},
	{method: http.MethodPost, target: "/api/v1/clips/wipe?handle=" + someHandle},
	{method: http.MethodPost, target: "/api/v1/clips/wipe?handle=zz"},
	{method: http.MethodPut, target: "/api/v1/clips/streamer", body: `{"downloads":false}`},
	{method: http.MethodPost, target: "/api/v1/clips/download-ticket", body: `{"id":"` + someClipID + `"}`},
	{method: http.MethodPost, target: "/api/v1/clips/download-ticket", body: `{"id":"../../x"}`},
	{method: http.MethodGet, target: "/api/v1/clips/download?t=" + strings.Repeat("ab", 32)},
	{method: http.MethodGet, target: "/api/v1/clips/download?d=" + someClipID + ".9999999999999." + strings.Repeat("0", 32), header: map[string]string{"Range": "bytes=0-99"}},
}

// Every route and method under /api/v1/admin/clips, the same way.
var consoleClipCalls = []clipCall{
	{method: http.MethodGet, target: "/api/v1/admin/clips"},
	{method: http.MethodGet, target: "/api/v1/admin/clips/video?id=" + someClipID, header: map[string]string{"Range": "bytes=0-99"}},
	{method: http.MethodHead, target: "/api/v1/admin/clips/video?id=" + someClipID},
	{method: http.MethodGet, target: "/api/v1/admin/clips/video?id=nonsense"},
	{method: http.MethodGet, target: "/api/v1/admin/clips/thumb?id=" + someClipID + "&v=a1b2c3d4", header: map[string]string{"If-None-Match": `"a1b2c3d4"`}},
	{method: http.MethodDelete, target: "/api/v1/admin/clips/thumb?id=" + someClipID},
	{method: http.MethodDelete, target: "/api/v1/admin/clips/clip?id=" + someClipID},
	{method: http.MethodDelete, target: "/api/v1/admin/clips/clip?id=nonsense"},
	{method: http.MethodPost, target: "/api/v1/admin/clips/block", body: `{"userId":"uid-linked"}`},
	{method: http.MethodDelete, target: "/api/v1/admin/clips/block?userId=uid-linked"},
	{method: http.MethodPost, target: "/api/v1/admin/clips/reset-score", body: `{"userId":"uid-linked"}`},
	{method: http.MethodPost, target: "/api/v1/admin/clips/reset-score", body: `not json`},
	{method: http.MethodDelete, target: "/api/v1/admin/clips/reports?id=" + someClipID},
	{method: http.MethodPost, target: "/api/v1/admin/clips/wipe?handle=" + someHandle},
	{method: http.MethodPost, target: "/api/v1/admin/clips/wipe?handle=zz"},
}

// A server with six people on it: somebody linked, somebody locked and
// somebody let in, and a system admin of each kind. Every KV key read is
// written down, so a test can see exactly what a request looked at before it
// was answered. The access gate's clock stands still, so what it remembers
// does not depend on how fast the test runs.
type clipsScene struct {
	p      *Plugin
	api    *plugintest.API
	store  map[string][]byte
	faults *kvFaults
	reads  []string
}

func newClipsScene(t *testing.T) *clipsScene {
	t.Helper()

	p, store, faults := kvBackedPluginWithFaults(map[string][]byte{})
	linkForTest(store, "uid-linked", "videokey-dave")
	linkForTest(store, "uid-admin-linked", "videokey-bob")
	store[kvLetIn] = []byte(`{"version":1,"people":{"uid-guest":{"at":1,"by":"uid-admin"},"uid-admin-guest":{"at":1,"by":"uid-admin"}}}`)

	sceneNow := time.Now()
	p.accessCache().now = func() time.Time { return sceneNow }

	// A clips document, so that a route reading one would find something,
	// and so that reading it would show up below.
	store[kvClips] = []byte(`{"v":1,"rev":1,"clips":[{"id":"` + someClipID + `","clipperId":"uid-unlinked","title":"SECRET-TITLE"}]}`)

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	people := map[string]*model.User{
		"uid-linked":       {Id: "uid-linked", Username: "dave"},
		"uid-unlinked":     {Id: "uid-unlinked", Username: "dan"},
		"uid-admin":        {Id: "uid-admin", Username: "boss"},
		"uid-admin-linked": {Id: "uid-admin-linked", Username: "bob"},
		"uid-guest":        {Id: "uid-guest", Username: "erin"},
		"uid-admin-guest":  {Id: "uid-admin-guest", Username: "chief"},
	}
	api.On("GetUser", mock.Anything).Return(func(userID string) (*model.User, *model.AppError) {
		if user, known := people[userID]; known {
			copied := *user

			return &copied, nil
		}

		return nil, model.NewAppError("GetUser", "app.user.missing.app_error", nil, "", http.StatusNotFound)
	})
	api.On("HasPermissionTo", "uid-admin", model.PermissionManageSystem).Return(true)
	api.On("HasPermissionTo", "uid-admin-linked", model.PermissionManageSystem).Return(true)
	api.On("HasPermissionTo", "uid-linked", model.PermissionManageSystem).Return(false)
	api.On("HasPermissionTo", "uid-unlinked", model.PermissionManageSystem).Return(false)
	api.On("HasPermissionTo", "uid-guest", model.PermissionManageSystem).Return(false)
	api.On("HasPermissionTo", "uid-admin-guest", model.PermissionManageSystem).Return(true)

	// No channel a clip could be shared into: a linked caller's share is
	// refused as a channel they cannot post in, and nothing is posted.
	api.On("GetChannel", mock.Anything).Return(nil, model.NewAppError("GetChannel", "app.channel.get.existing.app_error", nil, "", http.StatusNotFound))

	cfg := &configuration{EnableClips: true}
	cfg.normalize()
	p.setConfiguration(cfg)

	scene := &clipsScene{p: p, api: api, store: store, faults: faults}
	faults.onRead = func(key string) {
		scene.reads = append(scene.reads, key)
	}

	return scene
}

// start starts Clips on the scene, with its files in a fresh directory, and
// publishes one clip of its own there: someClipID, the unlinked person's, with
// a picture.
func (s *clipsScene) start(t *testing.T) *clipsHarness {
	t.Helper()

	h := startClipsIn(t, s.p, s.api, model.ImageDriverLocal)

	// The scene's own document holds a clip with no files; this one has
	// them, written over it as a later revision would be.
	entry := testClip(someClipID, "uid-unlinked", time.Now().Add(-time.Hour), nil)
	s.faults.lock.Lock()
	s.store[kvClips] = []byte(`{"v":1,"rev":10,"clips":[]}`)
	s.faults.lock.Unlock()
	publishFixtureClip(t, s.p, entry, true)

	return h
}

// call makes one request through ServeHTTP, as Mattermost would, and returns
// the answer and the KV keys it read.
func (s *clipsScene) call(t *testing.T, userID string, c clipCall) (*httptest.ResponseRecorder, []string) {
	t.Helper()

	s.reads = nil

	request := httptest.NewRequest(c.method, c.target, strings.NewReader(c.body))
	request.Header.Set("Mattermost-User-Id", userID)
	for name, value := range c.header {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()

	s.p.ServeHTTP(nil, recorder, request)

	return recorder, s.reads
}

// requireClipError checks a refusal: the status, the code, the fixed text, and
// that nothing may keep it.
func requireClipError(t *testing.T, response *httptest.ResponseRecorder, status int, code clipErrorCode, about string) {
	t.Helper()

	require.Equal(t, status, response.Code, "%s: %s", about, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"), about)

	var answer clipErrorResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer), about)
	require.Equal(t, code, answer.Code, about)
	require.Equal(t, clipErrorText(code, productName), answer.Error, about)
}

// routesOf is the path and method of every call, as a route table keys them.
func routesOf(t *testing.T, calls []clipCall) map[string]map[string]bool {
	t.Helper()

	routes := map[string]map[string]bool{}
	for _, c := range calls {
		parsed, err := url.Parse(c.target)
		require.NoError(t, err)

		if routes[parsed.Path] == nil {
			routes[parsed.Path] = map[string]bool{}
		}
		routes[parsed.Path][c.method] = true
	}

	return routes
}

func describe(c clipCall) string {
	return c.method + " " + c.target
}

// holdingThumbs is calls with every request that says it holds a picture
// already sending the ETag that picture is really served with, so that a
// route answering 304 before it checks who is asking would be caught.
func holdingThumbs(calls []clipCall, etag string) []clipCall {
	held := make([]clipCall, len(calls))
	for i, c := range calls {
		held[i] = c
		if _, holds := c.header["If-None-Match"]; holds {
			held[i].header = map[string]string{"If-None-Match": etag}
		}
	}

	return held
}

// Every route under /api/v1/clips has a class (T11, design/lock-screen.md
// §3.6). Somebody locked is refused on every route with the same answer
// whatever they asked for, having looked at nothing but who they are: not the
// clips, not the id, not the body, and not a limiter. Somebody let in passes
// the five routes for looking and is refused as watch-only on every other.
// A system admin is treated like anybody else; the panel's routes make no
// exception for them. Somebody linked gets past the gate on every route. A
// handler that acts is never handed an empty key. Publishing a draft already
// published is refused too, rather than handing back its clip.
func TestClipGateClasses(t *testing.T) {
	// The table above is every route the server has, and the server has no
	// route the table does not try.
	tried := routesOf(t, panelClipCalls)
	served := map[string]map[string]bool{}
	for path, methods := range clipRoutes {
		served[path] = map[string]bool{}
		for method := range methods {
			served[path][method] = true
		}
	}
	require.Equal(t, served, tried)

	// Every watch route is a route.
	for path, methods := range clipWatchRoutes {
		for method := range methods {
			require.NotNil(t, clipRoutes[path][method], "%s %s", method, path)
		}
	}

	// Every handler, wrapped, to see the key each one is handed.
	original := clipRoutes
	t.Cleanup(func() { clipRoutes = original })
	type handed struct{ path, method, key string }
	var keys []handed
	wrapped := map[string]map[string]clipHandler{}
	for path, methods := range original {
		wrapped[path] = map[string]clipHandler{}
		for method, handler := range methods {
			wrapped[path][method] = func(p *Plugin, w http.ResponseWriter, r *http.Request, userID, key string) {
				keys = append(keys, handed{path: path, method: method, key: key})
				handler(p, w, r, userID, key)
			}
		}
	}
	clipRoutes = wrapped

	scene := newClipsScene(t)
	scene.start(t)
	clips := scene.p.clips()

	// The thumbnail rows send the picture's real ETag, and it does match:
	// somebody linked who holds the picture is told it has not changed.
	calls := holdingThumbs(panelClipCalls, testJPEGETag(t))
	held := 0
	for _, c := range calls {
		if path, _, _ := strings.Cut(c.target, "?"); path != clipsPath+"/thumb" {
			continue
		}
		held++
		response, _ := scene.call(t, "uid-linked", c)
		require.Equal(t, http.StatusNotModified, response.Code, describe(c))
	}
	require.Positive(t, held, "a thumbnail row is tried")
	keys = nil

	for _, caller := range []string{"uid-unlinked", "uid-admin"} {
		var first []byte

		for _, c := range calls {
			response, reads := scene.call(t, caller, c)
			about := caller + " " + describe(c)

			requireClipError(t, response, http.StatusForbidden, clipErrLocked, about)
			for _, read := range reads {
				require.Contains(t, []string{kvLinkByUserPrefix + caller, kvLetIn}, read, "%s: only who they are is read", about)
			}

			// Byte for byte the same, so nothing about the clip, the id or
			// the route leaks into the refusal.
			if first == nil {
				first = response.Body.Bytes()
			}
			require.True(t, bytes.Equal(first, response.Body.Bytes()), "%s: %s", about, response.Body.String())
		}
	}

	for _, caller := range []string{"uid-guest", "uid-admin-guest"} {
		for _, c := range calls {
			path, _, _ := strings.Cut(c.target, "?")
			response, reads := scene.call(t, caller, c)
			about := caller + " " + describe(c)

			if clipWatchRoutes[path][c.method] {
				var answer clipErrorResponse
				_ = json.Unmarshal(response.Body.Bytes(), &answer)
				require.NotEqual(t, http.StatusForbidden, response.Code, "%s: %s", about, response.Body.String())
				require.NotEqual(t, clipErrStorageUnavailable, answer.Code, about)

				continue
			}

			requireClipError(t, response, http.StatusForbidden, clipErrWatchOnly, about)
			for _, read := range reads {
				require.Contains(t, []string{kvLinkByUserPrefix + caller, kvLetIn}, read, "%s: only who they are is read", about)
			}
		}
	}

	// Not one token spent, by anybody refused.
	for _, limiter := range []*rateLimiter{clips.captureLimiter, clips.publishLimiter, clips.voteLimiter, clips.writeLimiter, clips.shareLimiter, clips.attemptLimiter, clips.downloadLimiter} {
		limiter.lock.Lock()
		require.Empty(t, limiter.buckets)
		limiter.lock.Unlock()
	}

	// Nothing was changed, and the clip is still there to be had.
	require.Len(t, scene.p.clips().cached().Clips, 1)
	require.Empty(t, scene.p.clips().cached().Clips[0].Voters)
	require.Empty(t, scene.p.clips().cached().Clips[0].Reports)

	// The only handlers that ran were the ones for looking, with no key.
	for _, call := range keys {
		require.True(t, clipWatchRoutes[call.path][call.method], "%s %s ran for somebody refused", call.method, call.path)
		require.Empty(t, call.key)
	}

	// Nobody locked was asked whether they are an admin: being one changes
	// nothing here.
	for _, refused := range []string{"uid-unlinked", "uid-admin"} {
		scene.api.AssertNotCalled(t, "HasPermissionTo", refused, mock.Anything)
	}

	keys = nil
	for _, caller := range []string{"uid-linked", "uid-admin-linked"} {
		for _, c := range calls {
			response, _ := scene.call(t, caller, c)
			about := caller + " " + describe(c)

			var answer clipErrorResponse
			_ = json.Unmarshal(response.Body.Bytes(), &answer)
			require.NotEqual(t, clipErrLocked, answer.Code, about)
			require.NotEqual(t, clipErrWatchOnly, answer.Code, about)
			require.NotEqual(t, clipErrStorageUnavailable, answer.Code, "%s: every route has something behind it", about)
		}
	}
	require.NotEmpty(t, keys)
	for _, call := range keys {
		require.NotEmpty(t, call.key, "%s %s was handed no key for somebody linked", call.method, call.path)
	}

	// A draft its owner published before unlinking: asking to publish it
	// again is refused before the draft is looked at, never answered with
	// its clip, as it would be for somebody linked.
	clipRoutes = original
	d := newDraftsWorld(t)
	draft := d.makeDraft(t, "uid-bob")
	body := publishBody("that jump", 0, 10000)
	decodeClipView(t, d.publish(t, "uid-bob", draft.ID, body), http.StatusCreated)
	decodeClipView(t, d.publish(t, "uid-bob", draft.ID, body), http.StatusOK)

	d.unlink("uid-bob")
	requireClipError(t, d.publish(t, "uid-bob", draft.ID, body), http.StatusForbidden, clipErrLocked, "an already published draft")

	// Let in afterwards, they can look at it and do nothing else.
	_, err := d.p.letIn("uid-bob", "uid-boss", clipsNow)
	require.NoError(t, err)
	d.p.accessForget("uid-bob")
	requireClipError(t, d.publish(t, "uid-bob", draft.ID, body), http.StatusForbidden, clipErrWatchOnly, "let in")
}

// What somebody let in is shown about a clip says they may do nothing to it:
// not vote, report, delete or share, admin or not, and not for a clip they
// made while they were linked. A vote they cast then still shows as theirs.
func TestALetInViewerMayDoNothingToAClip(t *testing.T) {
	w, _ := startedClipsWorld(t, "uid-bob", "uid-dave", "uid-dan")
	clipsAdmins(w.api, "uid-bob")
	entry := testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), votesAt(clipsNow.Add(-time.Minute), "uid-dan"))
	entry.StreamTag = w.p.streamTag("videokey-bob")
	publishFixtureClip(t, w.p, entry, true)
	publishFixtureClip(t, w.p, testClip(clipB, "uid-dave", clipsNow.Add(-time.Hour), votesAt(clipsNow.Add(-time.Minute), "uid-bob")), true)

	viewsOf := func(userID string) map[string]clipView {
		response := w.call(t, userID, clipCall{method: http.MethodGet, target: clipsPath})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())

		views := map[string]clipView{}
		for _, view := range decodeClipList(t, response) {
			views[view.ID] = view
		}

		return views
	}

	// Linked, an admin and the streamer: everything.
	linked := viewsOf("uid-bob")
	require.True(t, linked[clipA].CanDelete)
	require.True(t, linked[clipA].CanShare)
	require.True(t, linked[clipB].CanVote)
	require.True(t, linked[clipB].CanReport)
	require.True(t, linked[clipB].Voted)

	// Unlinked, then let in.
	w.unlink("uid-bob")
	w.p.clips().background.Wait()
	_, err := w.p.letIn("uid-bob", "uid-boss", clipsNow)
	require.NoError(t, err)
	w.p.accessForget("uid-bob")

	watching := viewsOf("uid-bob")
	for _, id := range []string{clipA, clipB} {
		view := watching[id]
		require.False(t, view.CanVote, id)
		require.False(t, view.CanReport, id)
		require.False(t, view.CanDelete, id)
		require.False(t, view.CanShare, id)
		require.False(t, view.OfMyStream, id)
	}
	require.True(t, watching[clipA].Mine, "still theirs")
	require.True(t, watching[clipB].Voted, "a vote from when they were linked still shows")

	// Somebody linked who is not an admin can share, and nobody else's view
	// changed.
	require.True(t, viewsOf("uid-dan")[clipA].CanShare)
}

// Every route under /api/v1/admin/clips needs a system admin, and that is all
// it needs: somebody who is not one is refused before anything they sent is
// looked at, linked or not, and an admin is let through whether they have
// linked a stream or not, and can list, watch, see pictures, delete, block
// and wipe a stream's recording. Nobody's own link is read, and Mattermost is
// asked once per request.
func TestAdminRoutesNeedAnAdminNotALink(t *testing.T) {
	tried := routesOf(t, consoleClipCalls)
	served := map[string]map[string]bool{}
	for path, methods := range adminClipRoutes {
		served[path] = map[string]bool{}
		for method := range methods {
			served[path][method] = true
		}
	}
	require.Equal(t, served, tried)

	scene := newClipsScene(t)
	scene.start(t)

	// The picture's real ETag, which an admin holding it is answered 304 for
	// below: holding it gets somebody who is not an admin nowhere.
	etag := testJPEGETag(t)
	calls := holdingThumbs(consoleClipCalls, etag)

	for _, caller := range []string{"uid-unlinked", "uid-linked"} {
		for _, c := range calls {
			response, reads := scene.call(t, caller, c)
			about := caller + " " + describe(c)

			requireClipError(t, response, http.StatusForbidden, clipErrForbidden, about)
			require.Empty(t, reads, "%s: nothing is read", about)
		}
	}
	scene.api.AssertNumberOfCalls(t, "HasPermissionTo", 2*len(calls))

	fixture, err := os.ReadFile(clipFixture)
	require.NoError(t, err)

	for _, admin := range []string{"uid-admin", "uid-admin-linked"} {
		scene := newClipsScene(t)
		scene.start(t)
		own := kvLinkByUserPrefix + admin

		call := func(c clipCall) *httptest.ResponseRecorder {
			response, reads := scene.call(t, admin, c)
			require.NotContains(t, reads, own, "%s %s: no link of their own is asked for", admin, describe(c))

			return response
		}

		response := call(clipCall{method: http.MethodGet, target: adminClipsPath})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		view := decodeAdminClips(t, response)
		require.Len(t, view.Clips, 1)
		require.Equal(t, someClipID, view.Clips[0].ID)
		require.True(t, view.Clips[0].CanDelete)

		response = call(clipCall{method: http.MethodGet, target: adminClipsPath + "/video?id=" + someClipID, header: map[string]string{"Range": "bytes=0-99"}})
		require.Equal(t, http.StatusPartialContent, response.Code)
		require.Equal(t, fixture[:100], response.Body.Bytes())

		response = call(clipCall{method: http.MethodGet, target: adminClipsPath + "/thumb?id=" + someClipID + "&v=a1b2c3d4"})
		require.Equal(t, http.StatusOK, response.Code)
		require.Equal(t, "image/jpeg", response.Header().Get("Content-Type"))
		require.Equal(t, etag, response.Header().Get("ETag"))

		response = call(clipCall{method: http.MethodGet, target: adminClipsPath + "/thumb?id=" + someClipID + "&v=a1b2c3d4", header: map[string]string{"If-None-Match": etag}})
		require.Equal(t, http.StatusNotModified, response.Code)

		response = call(clipCall{method: http.MethodGet, target: adminClipsPath + "/video?id=nonsense"})
		requireClipError(t, response, http.StatusBadRequest, clipErrBadRequest, "malformed")

		response = call(clipCall{method: http.MethodPost, target: adminClipsPath + "/block", body: `{"userId":"uid-linked"}`})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Contains(t, scene.p.clips().cached().Blocked, "uid-linked")

		response = call(clipCall{method: http.MethodDelete, target: adminClipsPath + "/clip?id=" + someClipID})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.Empty(t, scene.p.clips().cached().Clips)

		response = call(clipCall{method: http.MethodPost, target: adminClipsPath + "/wipe?handle=" + someHandle})
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.JSONEq(t, `{"ok":true,"draftsRemoved":0}`, response.Body.String())

		response = call(clipCall{method: http.MethodPost, target: adminClipsPath + "/wipe?handle=zz"})
		requireClipError(t, response, http.StatusBadRequest, clipErrBadRequest, "a handle that is not one")
	}
}

// A path that is not a route, and a method a route does not take, are
// answered before anything is checked, in both families, and a path that only
// starts with the same letters is not a clip route at all.
func TestClipRoutesAnswerUnknownPathsAndMethodsFirst(t *testing.T) {
	scene := newClipsScene(t)

	for _, c := range []clipCall{
		{method: http.MethodGet, target: "/api/v1/clips/nope"},
		{method: http.MethodGet, target: "/api/v1/clips/"},
		{method: http.MethodGet, target: "/api/v1/clips/clip/" + someClipID},
		{method: http.MethodGet, target: "/api/v1/admin/clips/nope?id=" + someClipID},
	} {
		response, reads := scene.call(t, "uid-unlinked", c)

		require.Equal(t, http.StatusNotFound, response.Code, describe(c))
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"), describe(c))
		require.JSONEq(t, `{"error":"Unknown endpoint.","code":"not_found"}`, response.Body.String(), describe(c))
		require.Empty(t, reads, describe(c))
	}

	for _, c := range []clipCall{
		{method: http.MethodPatch, target: "/api/v1/clips"},
		{method: http.MethodPost, target: "/api/v1/clips/video?id=" + someClipID},
		{method: http.MethodGet, target: "/api/v1/clips/vote?id=" + someClipID},
		{method: http.MethodPut, target: "/api/v1/admin/clips"},
		{method: http.MethodGet, target: "/api/v1/admin/clips/clip?id=" + someClipID},
	} {
		response, reads := scene.call(t, "uid-unlinked", c)

		require.Equal(t, http.StatusMethodNotAllowed, response.Code, describe(c))
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"), describe(c))
		require.JSONEq(t, `{"error":"Method not allowed.","code":"method_not_allowed"}`, response.Body.String(), describe(c))
		require.Empty(t, reads, describe(c))
	}

	// Not a clip route at all: the System Console's paths answer 404 from
	// memory, and anything else is a path the front gate has no class for,
	// which is for linked people only, so somebody locked is refused there.
	response, reads := scene.call(t, "uid-unlinked", clipCall{method: http.MethodGet, target: "/api/v1/admin/clipsx"})
	require.Equal(t, http.StatusNotFound, response.Code)
	require.Empty(t, reads)

	response, _ = scene.call(t, "uid-unlinked", clipCall{method: http.MethodGet, target: "/api/v1/clipsx"})
	require.Equal(t, http.StatusForbidden, response.Code)
	require.JSONEq(t, `{"error":"`+accessLockedText(productName)+`","code":"locked"}`, response.Body.String())

	response, _ = scene.call(t, "uid-linked", clipCall{method: http.MethodGet, target: "/api/v1/clipsx"})
	require.Equal(t, http.StatusNotFound, response.Code)

	scene.api.AssertNotCalled(t, "HasPermissionTo", mock.Anything, mock.Anything)
}

// A link that cannot be read is a store that did not answer, not a person who
// is not linked: telling a linked person to link again would send them off to
// paste their token for nothing.
func TestLinkReadErrorIsStoreNotNotLinked(t *testing.T) {
	scene := newClipsScene(t)
	scene.faults.failRead(kvLinkByUserPrefix + "uid-linked")
	scene.faults.failRead(kvLinkByUserPrefix + "uid-unlinked")

	for _, caller := range []string{"uid-linked", "uid-unlinked"} {
		for _, c := range panelClipCalls {
			response, reads := scene.call(t, caller, c)
			about := caller + " " + describe(c)

			requireClipError(t, response, http.StatusServiceUnavailable, clipErrStore, about)
			require.Equal(t, []string{kvLinkByUserPrefix + caller}, reads, about)
		}
	}

	// Said so that it is true of a read as of a save: Matty shows it as it is.
	response, _ := scene.call(t, "uid-linked", panelClipCalls[0])
	require.Contains(t, response.Body.String(), `"error":"Clips could not be saved just now. Try again."`)
}

// A stream tag is never a stream's public handle, though both are an HMAC
// under the same secret: the handle of a lower-case key is broadcast to every
// browser, and the tag is the server's alone. It follows the link however the
// key was typed, and with nothing to take it of it is nothing.
func TestStreamTagIsNotTheHandle(t *testing.T) {
	p := &Plugin{handleSecret: testHandleSecret}

	for _, key := range []string{"videokey-dave", "videokey-bob", "abc", "dave_99"} {
		tag := p.streamTag(key)

		require.Len(t, tag, 32, key)
		_, err := hex.DecodeString(tag)
		require.NoError(t, err, key)
		require.NotEqual(t, p.streamHandle(key), tag, key)
	}

	require.Equal(t, p.streamTag("videokey-dave"), p.streamTag("VideoKey-Dave"))
	require.Equal(t, p.streamTag("videokey-dave"), p.streamTag(" Bearer videokey-dave "))
	require.NotEqual(t, p.streamTag("videokey-dave"), p.streamTag("videokey-bob"))

	other := &Plugin{handleSecret: []byte("fedcba9876543210fedcba9876543210")}
	require.NotEqual(t, p.streamTag("videokey-dave"), other.streamTag("videokey-dave"), "another installation, another tag")

	require.Empty(t, (&Plugin{}).streamTag("videokey-dave"), "no secret, no tag")
	require.Empty(t, p.streamTag(""), "no key, no tag")
	require.Empty(t, p.streamTag("  "), "no key, no tag")
}
