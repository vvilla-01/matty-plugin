package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The accounts these tests can look up. Anybody else, uid-ghost among them,
// cannot be found, as an account since deleted could not.
var stillSharers = map[string]model.User{
	"uid-admin": {Id: "uid-admin", Username: "victor", FirstName: "Victor", LastName: "Villa"},
	"uid-user":  {Id: "uid-user", Username: "viewer"},
	"uid-dave":  {Id: "uid-dave", Username: "dave", Nickname: "Dave"},
	"uid-bob":   {Id: "uid-bob", Username: "bob", FirstName: "Bob", LastName: "Turner"},
	"uid-troll": {Id: "uid-troll", Username: "troll"},
}

var stillsAdminKeys = []string{"videokey-dave", "videokey-bob", "videokey-erin", "videokey-watts"}

// As stillsPlugin, with live previews on, three people live and one 24/7
// channel, but over a KV store the test can break, with accounts to look up
// and the titles the poller would have given the streams. Built on store, so
// that a second one on the same store is the same server after a restart.
func stillsAdminPlugin(t *testing.T, store map[string][]byte) (*Plugin, *kvFaults) {
	t.Helper()

	p, _, faults := kvBackedPluginWithFaults(store)

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("HasPermissionTo", "uid-admin", model.PermissionManageSystem).Return(true)
	api.On("HasPermissionTo", "uid-user", model.PermissionManageSystem).Return(false)
	api.On("GetUser", mock.Anything).Return(
		func(userID string) *model.User {
			user, known := stillSharers[userID]
			if !known {
				return nil
			}

			return &user
		},
		func(userID string) *model.AppError {
			if _, known := stillSharers[userID]; !known {
				return model.NewAppError("GetUser", "app.user.missing.app_error", nil, "", http.StatusNotFound)
			}

			return nil
		},
	)

	cfg := &configuration{AlwaysOnStreamKeys: "videokey-watts", LivePreviews: true}
	cfg.normalize()
	p.setConfiguration(cfg)

	erin := p.streamHandle("videokey-erin")
	p.setSnapshot(&snapshot{Streams: []apiStream{
		{StreamKey: "videokey-bob", Handle: p.streamHandle("videokey-bob"), Title: "Bob Turner", UserID: "uid-bob"},
		{StreamKey: "videokey-dave", Handle: p.streamHandle("videokey-dave"), Title: "Dave", UserID: "uid-dave"},
		{StreamKey: "videokey-erin", Handle: erin, Title: unnamedTitle(erin)},
		{StreamKey: "videokey-watts", Handle: p.streamHandle("videokey-watts"), Title: "Alan Watts Chillstep", AlwaysOn: true},
	}, Configured: true, LivePreviews: true})

	// Everybody but the admin has unlocked the panel, so the routes they use
	// answer as these tests expect; the admin is locked, as an admin with no
	// token is, and uses the System Console. Who may reach which route is
	// TestRouteAccessTable's business.
	faults.lock.Lock()
	for _, userID := range []string{"uid-user", "uid-dave", "uid-bob", "uid-troll", "uid-ghost", "uid-departed", "uid-elsewhere", "uid-gone"} {
		linkForTest(store, userID, "videokey-test-"+userID)
	}
	faults.lock.Unlock()

	return p, faults
}

func adminStillsOf(t *testing.T, p *Plugin, userID string) (adminStillsResponse, *httptest.ResponseRecorder) {
	t.Helper()

	response := stillRequest(t, p, http.MethodGet, "/api/v1/admin/stills", nil, userID)

	var list adminStillsResponse
	if response.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))
	}

	return list, response
}

func removeOverHTTP(t *testing.T, p *Plugin, handle, userID string) *httptest.ResponseRecorder {
	t.Helper()

	return stillRequest(t, p, http.MethodDelete, "/api/v1/admin/stills?handle="+handle, nil, userID)
}

func blockOverHTTP(t *testing.T, p *Plugin, body, userID string) *httptest.ResponseRecorder {
	t.Helper()

	return stillRequest(t, p, http.MethodPost, "/api/v1/admin/stills/block", []byte(body), userID)
}

func unblockOverHTTP(t *testing.T, p *Plugin, target, userID string) *httptest.ResponseRecorder {
	t.Helper()

	return stillRequest(t, p, http.MethodDelete, "/api/v1/admin/stills/block?userId="+target, nil, userID)
}

func requireOK(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()

	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"ok":true}`, response.Body.String())
}

// shareStill does what the panel does: claims a lease on handle and uploads
// still under it.
func shareStill(t *testing.T, p *Plugin, handle, userID string, still []byte) {
	t.Helper()

	claim, response := claimOverHTTP(t, p, handle, stillModeWatching, userID)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NotEmpty(t, claim.Lease, "%s on %s", userID, handle)

	response = uploadOverHTTP(t, p, handle, claim.Lease, still, userID)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
}

// ageStill makes handle's still older by age, as a replacement rather than an
// edit: a stored still is never changed in place.
func ageStill(t *testing.T, p *Plugin, handle string, age time.Duration) {
	t.Helper()

	store := p.stills()
	store.lock.Lock()
	defer store.lock.Unlock()

	old := store.entries[handle].picture
	require.NotNil(t, old)
	store.entries[handle].picture = &stillPicture{data: old.data, version: old.version, at: old.at.Add(-age), by: old.by}
}

func storedBlocks(t *testing.T, store map[string][]byte) map[string]stillBlock {
	t.Helper()

	var blocks map[string]stillBlock
	require.NoError(t, json.Unmarshal(store[kvStillsBlocked], &blocks))

	return blocks
}

// blockListReads is how many times p has read the block list from the KV
// store.
func blockListReads(p *Plugin) int {
	api, _ := p.API.(*plugintest.API)

	reads := 0
	for _, call := range api.Calls {
		if call.Method == "KVGet" && len(call.Arguments) > 0 && call.Arguments[0] == kvStillsBlocked {
			reads++
		}
	}

	return reads
}

// atomicWriteTries is how many times p has tried to write key with
// KVSetWithOptions, whether or not the write landed.
func atomicWriteTries(p *Plugin, key string) int {
	api, _ := p.API.(*plugintest.API)

	tries := 0
	for _, call := range api.Calls {
		if call.Method == "KVSetWithOptions" && len(call.Arguments) > 0 && call.Arguments[0] == key {
			tries++
		}
	}

	return tries
}

// blockElsewhere is another node blocking userID: straight into the list as
// stored, with nothing on this node any the wiser. Called from inside the
// store's own lock too, by an interfere, so it touches store and nothing else.
func blockElsewhere(t *testing.T, store map[string][]byte, userID string) {
	t.Helper()

	blocks := map[string]stillBlock{}
	if raw := store[kvStillsBlocked]; len(raw) > 0 {
		require.NoError(t, json.Unmarshal(raw, &blocks))
	}

	blocks[userID] = stillBlock{BlockedAt: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC), BlockedBy: "uid-elsewhere"}

	data, err := json.Marshal(blocks)
	require.NoError(t, err)
	store[kvStillsBlocked] = data
}

// logLines is every call of level whose message is message, with its fields.
func logLines(p *Plugin, level, message string) [][]any {
	api, _ := p.API.(*plugintest.API)

	var lines [][]any
	for _, call := range api.Calls {
		if call.Method == level && len(call.Arguments) > 0 && call.Arguments[0] == message {
			lines = append(lines, []any(call.Arguments))
		}
	}

	return lines
}

func requireNoKeyLogged(t *testing.T, p *Plugin) {
	t.Helper()

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	for _, call := range api.Calls {
		if !strings.HasPrefix(call.Method, "Log") {
			continue
		}
		for _, argument := range call.Arguments {
			for _, key := range stillsAdminKeys {
				require.NotContains(t, fmt.Sprint(argument), key, call.Method)
			}
		}
	}
}

// The admin list has to show exactly what the cards are being served: for
// every stream, listed exactly when GET /api/v1/stills serves it, and with the
// picture it serves.
func requireListMatchesServed(t *testing.T, p *Plugin, list adminStillsResponse) {
	t.Helper()

	listed := map[string]string{}
	for _, still := range list.Stills {
		listed[still.Handle] = still.Version
	}

	for _, key := range stillsAdminKeys {
		handle := p.streamHandle(key)
		response := stillRequest(t, p, http.MethodGet, "/api/v1/stills?handle="+handle, nil, "uid-user")

		if version, shown := listed[handle]; shown {
			require.Equal(t, http.StatusOK, response.Code, key)
			require.Equal(t, version, pictureVersion(response.Body.Bytes()), key)
		} else {
			require.Equal(t, http.StatusNotFound, response.Code, key)
		}
	}
}

func TestOnlyASystemAdminCanManagePreviewPictures(t *testing.T) {
	store := map[string][]byte{}
	p, _ := stillsAdminPlugin(t, store)
	dave := p.streamHandle("videokey-dave")

	shareStill(t, p, dave, "uid-troll", cardStill(t))

	for _, request := range []struct{ method, target, body string }{
		{http.MethodGet, "/api/v1/admin/stills", ""},
		{http.MethodDelete, "/api/v1/admin/stills?handle=" + dave, ""},
		{http.MethodPost, "/api/v1/admin/stills/block", `{"userId":"uid-troll"}`},
		{http.MethodDelete, "/api/v1/admin/stills/block?userId=uid-troll", ""},
	} {
		response := stillRequest(t, p, request.method, request.target, []byte(request.body), "uid-user")
		require.Equal(t, http.StatusForbidden, response.Code, "%s %s", request.method, request.target)
		require.Equal(t, "Only a system admin can manage preview pictures.", errorOf(t, response), request.target)
		require.NotContains(t, response.Body.String(), "videokey-")

		response = stillRequest(t, p, request.method, request.target, []byte(request.body), "")
		require.Equal(t, http.StatusUnauthorized, response.Code, "%s %s", request.method, request.target)
	}

	// None of it happened: the picture is up, nobody is blocked, and nothing
	// was written.
	require.NotNil(t, p.stills().current(dave, true, time.Now()))
	require.NotContains(t, store, kvStillsBlocked)
	claim, _ := claimOverHTTP(t, p, p.streamHandle("videokey-bob"), stillModeWatching, "uid-troll")
	require.NotEmpty(t, claim.Lease)

	// And an admin asking either route for something it does not do.
	for _, target := range []string{"/api/v1/admin/stills", "/api/v1/admin/stills/block"} {
		response := stillRequest(t, p, http.MethodPut, target, nil, "uid-admin")
		require.Equal(t, http.StatusMethodNotAllowed, response.Code, target)
	}
}

func TestTheAdminListIsTwoEmptyListsWhenThereIsNothing(t *testing.T) {
	p, _ := stillsAdminPlugin(t, map[string][]byte{})

	_, response := adminStillsOf(t, p, "uid-admin")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"stills":[],"blocked":[]}`, response.Body.String(), "both present, and never null")
	require.Equal(t, "application/json", response.Header().Get("Content-Type"))
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
}

func TestTheAdminListShowsWhatIsBeingServedAndWhoTookIt(t *testing.T) {
	p, _ := stillsAdminPlugin(t, map[string][]byte{})
	dave, bob, erin := p.streamHandle("videokey-dave"), p.streamHandle("videokey-bob"), p.streamHandle("videokey-erin")

	daveStill, bobStill, erinStill := cardStill(t), stillJPEG(t, 640, 360), stillJPEG(t, 800, 450)
	shareStill(t, p, dave, "uid-bob", daveStill)
	shareStill(t, p, bob, "uid-troll", bobStill)
	shareStill(t, p, erin, "uid-ghost", erinStill)

	list, response := adminStillsOf(t, p, "uid-admin")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Empty(t, list.Blocked)

	// By title, as the cards show them, and each with who took it.
	require.Len(t, list.Stills, 3)

	require.Equal(t, bob, list.Stills[0].Handle)
	require.Equal(t, "Bob Turner", list.Stills[0].Title)
	require.Equal(t, pictureVersion(bobStill), list.Stills[0].Version)
	require.Equal(t, "uid-troll", list.Stills[0].ByUserID)
	require.Equal(t, "troll", list.Stills[0].ByUsername)
	require.Equal(t, "troll", list.Stills[0].ByDisplayName, "no nickname and no full name: the username, as on a card")
	taken, err := time.Parse(time.RFC3339, list.Stills[0].At)
	require.NoError(t, err)
	require.WithinDuration(t, time.Now(), taken, time.Minute)

	require.Equal(t, dave, list.Stills[1].Handle)
	require.Equal(t, "Dave", list.Stills[1].Title)
	require.Equal(t, pictureVersion(daveStill), list.Stills[1].Version)
	require.Equal(t, "uid-bob", list.Stills[1].ByUserID, "who took the picture, not whose stream it is")
	require.Equal(t, "bob", list.Stills[1].ByUsername)
	require.Equal(t, "Bob Turner", list.Stills[1].ByDisplayName)

	// Somebody who can no longer be looked up is listed by id alone, and
	// does not cost the list its other rows.
	require.Equal(t, erin, list.Stills[2].Handle)
	require.Equal(t, unnamedTitle(erin), list.Stills[2].Title)
	require.Equal(t, "uid-ghost", list.Stills[2].ByUserID)
	require.Empty(t, list.Stills[2].ByUsername)
	require.Empty(t, list.Stills[2].ByDisplayName)

	requireListMatchesServed(t, p, list)
	bodies := []string{response.Body.String()}

	// Bob's stream leaves the listing. Its picture is still served through
	// the off-air grace, so it is still listed, and named the way an unnamed
	// stream is: the listing was where its title came from, and the key is
	// never a title.
	current := p.getSnapshot()
	next := *current
	next.Streams = nil
	for _, stream := range current.Streams {
		if stream.Handle != bob {
			next.Streams = append(next.Streams, stream)
		}
	}
	p.setSnapshot(&next)

	list, response = adminStillsOf(t, p, "uid-admin")
	require.Len(t, list.Stills, 3)
	titles := map[string]string{}
	for _, still := range list.Stills {
		titles[still.Handle] = still.Title
	}
	require.Equal(t, "Stream "+bob[:6], titles[bob])
	requireListMatchesServed(t, p, list)
	bodies = append(bodies, response.Body.String())

	// Past the grace it is not served, and so not listed.
	store := p.stills()
	store.lock.Lock()
	store.entries[bob].lastLive = time.Now().Add(-stillOffAirGrace - time.Second)
	store.lock.Unlock()

	list, _ = adminStillsOf(t, p, "uid-admin")
	require.Len(t, list.Stills, 2)
	requireListMatchesServed(t, p, list)

	// Nor is a picture past its age, on the air or not.
	ageStill(t, p, dave, stillMaxAge+time.Second)
	list, response = adminStillsOf(t, p, "uid-admin")
	require.Len(t, list.Stills, 1)
	require.Equal(t, erin, list.Stills[0].Handle)
	requireListMatchesServed(t, p, list)
	bodies = append(bodies, response.Body.String())

	// With live previews off nothing is served, so nothing is listed.
	off := &configuration{AlwaysOnStreamKeys: "videokey-watts"}
	off.normalize()
	p.setConfiguration(off)

	_, response = adminStillsOf(t, p, "uid-admin")
	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `{"stills":[],"blocked":[]}`, response.Body.String())
	require.Equal(t, http.StatusNotFound, stillRequest(t, p, http.MethodGet, "/api/v1/stills?handle="+erin, nil, "uid-user").Code)

	for _, body := range bodies {
		for _, key := range stillsAdminKeys {
			require.NotContains(t, body, key)
		}
	}
}

func TestAnAdminCanTakeAStillDown(t *testing.T) {
	p, _ := stillsAdminPlugin(t, map[string][]byte{})
	dave, bob := p.streamHandle("videokey-dave"), p.streamHandle("videokey-bob")

	shareStill(t, p, dave, "uid-troll", cardStill(t))

	// Stale, and somebody is already retaking it: nobody else is given a
	// lease on it until they are done.
	ageStill(t, p, dave, stillStaleAfter)
	retake, _ := claimOverHTTP(t, p, dave, stillModeWatching, "uid-bob")
	require.NotEmpty(t, retake.Lease)
	refused, _ := claimOverHTTP(t, p, dave, stillModePeek, "uid-dave")
	require.Empty(t, refused.Lease)

	requireOK(t, removeOverHTTP(t, p, dave, "uid-admin"))

	// Not served and not listed.
	require.Equal(t, http.StatusNotFound, stillRequest(t, p, http.MethodGet, "/api/v1/stills?handle="+dave, nil, "uid-user").Code)
	list, _ := adminStillsOf(t, p, "uid-admin")
	require.Empty(t, list.Stills)

	// The lease on it went too, so nothing goes back up under it...
	response := uploadOverHTTP(t, p, dave, retake.Lease, stillJPEG(t, 640, 360), "uid-bob")
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Equal(t, "That capture was not yours to make.", errorOf(t, response))
	require.Nil(t, p.stills().current(dave, true, time.Now()))

	// ...and the next browser to ask is given one at once, even for a peek,
	// rather than after the wait the removed still would have held it to.
	again, response := claimOverHTTP(t, p, dave, stillModePeek, "uid-dave")
	require.Equal(t, http.StatusOK, response.Code)
	require.NotEmpty(t, again.Lease)

	// The next poll stops naming it, so the cards let it go.
	streams := p.decorate([]bbStream{rawLive("videokey-dave")}, p.getConfiguration())
	require.Len(t, streams, 1)
	require.Empty(t, streams[0].StillVersion)

	// Nothing to take down: gone already, never taken, or no such stream.
	for _, handle := range []string{dave, bob, "0123456789abcdef"} {
		response = removeOverHTTP(t, p, handle, "uid-admin")
		require.Equal(t, http.StatusNotFound, response.Code, handle)
		require.Equal(t, "There is no preview picture for that stream.", errorOf(t, response))
	}

	response = removeOverHTTP(t, p, "", "uid-admin")
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Equal(t, "Missing stream handle.", errorOf(t, response))

	// Who took it down, which stream, and who had put it up, at Info. By
	// handle, and no key anywhere in the log.
	require.Equal(t, [][]any{{"A system admin removed a preview still",
		"user_id", "uid-admin", "stream_handle", dave, "uploader_user_id", "uid-troll"}},
		logLines(p, "LogInfo", "A system admin removed a preview still"))
	requireNoKeyLogged(t, p)
}

func TestBlockingSomebodyTakesDownWhatTheyShared(t *testing.T) {
	store := map[string][]byte{}
	p, faults := stillsAdminPlugin(t, store)
	dave, bob, erin := p.streamHandle("videokey-dave"), p.streamHandle("videokey-bob"), p.streamHandle("videokey-erin")

	shareStill(t, p, dave, "uid-troll", cardStill(t))
	shareStill(t, p, erin, "uid-bob", stillJPEG(t, 640, 360))

	// And halfway through taking another.
	pending, _ := claimOverHTTP(t, p, bob, stillModeWatching, "uid-troll")
	require.NotEmpty(t, pending.Lease)

	before := time.Now()
	requireOK(t, blockOverHTTP(t, p, `{"userId":"uid-troll"}`, "uid-admin"))

	// Kept, with when and by whom.
	blocks := storedBlocks(t, store)
	require.Len(t, blocks, 1)
	require.Equal(t, "uid-admin", blocks["uid-troll"].BlockedBy)
	require.WithinDuration(t, before, blocks["uid-troll"].BlockedAt, time.Minute)

	// Their picture is down, and nobody else's is.
	require.Equal(t, http.StatusNotFound, stillRequest(t, p, http.MethodGet, "/api/v1/stills?handle="+dave, nil, "uid-user").Code)
	require.Equal(t, http.StatusOK, stillRequest(t, p, http.MethodGet, "/api/v1/stills?handle="+erin, nil, "uid-user").Code)

	// Their lease is gone, and both streams are anybody's at once.
	p.stills().lock.Lock()
	require.Nil(t, p.stills().entries[bob].lease)
	p.stills().lock.Unlock()

	for _, handle := range []string{dave, bob} {
		claim, _ := claimOverHTTP(t, p, handle, stillModePeek, "uid-dave")
		require.NotEmpty(t, claim.Lease, handle)
	}

	// Listed as blocked, by name.
	list, response := adminStillsOf(t, p, "uid-admin")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, []blockedSharer{{
		UserID: "uid-troll", Username: "troll", DisplayName: "troll",
		BlockedAt: blocks["uid-troll"].BlockedAt.UTC().Format(time.RFC3339),
	}}, list.Blocked)
	require.Len(t, list.Stills, 1)
	require.Equal(t, "uid-bob", list.Stills[0].ByUserID)

	// Blocking them again is the same answer, and keeps the block they had.
	stored := string(store[kvStillsBlocked])
	requireOK(t, blockOverHTTP(t, p, `{"userId":" uid-troll "}`, "uid-admin"))
	require.Equal(t, stored, string(store[kvStillsBlocked]))
	require.Equal(t, 1, faults.writes[kvStillsBlocked], "nothing new to write")

	// Who blocked whom, at Info.
	require.Equal(t, []any{"A system admin blocked somebody from sharing preview pictures",
		"user_id", "uid-admin", "blocked_user_id", "uid-troll", "stills_removed", 1},
		logLines(p, "LogInfo", "A system admin blocked somebody from sharing preview pictures")[0])
	requireNoKeyLogged(t, p)
}

func TestABlockedPersonCannotShareButStillWatches(t *testing.T) {
	origin, _ := fakeWHEPOrigin(t)
	defer origin.Close()

	p, _ := stillsAdminPlugin(t, map[string][]byte{})
	p.httpClient = origin.Client()
	cfg := &configuration{BroadcastBoxURL: origin.URL, AlwaysOnStreamKeys: "videokey-watts", LivePreviews: true}
	cfg.normalize()
	p.setConfiguration(cfg)

	dave, bob := p.streamHandle("videokey-dave"), p.streamHandle("videokey-bob")
	shareStill(t, p, bob, "uid-bob", cardStill(t))

	requireOK(t, blockOverHTTP(t, p, `{"userId":"uid-troll"}`, "uid-admin"))

	for _, mode := range []string{stillModeWatching, stillModePeek} {
		claim, response := claimOverHTTP(t, p, dave, mode, "uid-troll")
		require.Equal(t, http.StatusForbidden, response.Code, mode)
		require.Equal(t, "You can't share preview pictures.", errorOf(t, response), mode)
		require.Empty(t, claim.Lease)
	}

	response := uploadOverHTTP(t, p, dave, "a-lease-from-before", cardStill(t), "uid-troll")
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Equal(t, "You can't share preview pictures.", errorOf(t, response))
	require.Nil(t, p.stills().current(dave, true, time.Now()))

	// Everything else is as it was: they see the pictures and the streams,
	// and they watch.
	require.Equal(t, http.StatusOK, stillRequest(t, p, http.MethodGet, "/api/v1/stills?handle="+bob, nil, "uid-troll").Code)
	require.Equal(t, http.StatusOK, stillRequest(t, p, http.MethodGet, "/api/v1/streams", nil, "uid-troll").Code)
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, "", "uid-troll").Code)

	// Nobody else is held up by it.
	claim, _ := claimOverHTTP(t, p, dave, stillModeWatching, "uid-dave")
	require.NotEmpty(t, claim.Lease)

	// Unblocked, they share again.
	requireOK(t, unblockOverHTTP(t, p, "uid-troll", "uid-admin"))
	shareStill(t, p, p.streamHandle("videokey-erin"), "uid-troll", stillJPEG(t, 640, 360))
	list, _ := adminStillsOf(t, p, "uid-admin")
	require.Empty(t, list.Blocked)
	require.Len(t, logLines(p, "LogInfo", "A system admin let somebody share preview pictures again"), 1)

	// Twice is the same answer, and so is somebody who was never blocked.
	requireOK(t, unblockOverHTTP(t, p, "uid-troll", "uid-admin"))
	requireOK(t, unblockOverHTTP(t, p, "uid-bob", "uid-admin"))
	require.Len(t, logLines(p, "LogInfo", "A system admin let somebody share preview pictures again"), 1, "nothing changed, so nothing to log")

	// Nobody at all is not a request.
	response = unblockOverHTTP(t, p, "", "uid-admin")
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Equal(t, "Say who to unblock.", errorOf(t, response))
}

// A block can land after the claim handler has let somebody through and before
// their claim reaches the store. The block takes their leases away, so the
// store has to refuse them too, or they would be handed a new lease straight
// after; and a peek lease keeps a session off the viewer count whether or not
// a still ever comes back under it. Through the store rather than over HTTP,
// because over HTTP the handler's own check answers first.
func TestAClaimThatReachesTheStoreAfterABlockIsRefused(t *testing.T) {
	s := newStillStore()
	s.enforceBlocks(map[string]stillBlock{"uid-troll": {BlockedAt: stillEpoch, BlockedBy: "uid-admin"}}, "uid-troll")

	// Either way, no lease, and not told to come back soon.
	for _, mode := range []string{stillModeWatching, stillModePeek} {
		refused := s.claim("h1", "uid-troll", mode, stillEpoch)
		require.Empty(t, refused.Lease, mode)
		require.Equal(t, retryAfter(stillRetryMax), refused.RetryAfter, mode)
	}

	// And the refusal holds nobody else back: it left no attempt on the
	// stream, so the next person to ask has it at once.
	require.NotEmpty(t, s.claim("h1", "uid-dave", stillModeWatching, stillEpoch).Lease)
}

func TestABlockNamesSomebodyWhoExists(t *testing.T) {
	store := map[string][]byte{}
	p, _ := stillsAdminPlugin(t, store)

	for name, refused := range map[string]struct {
		body    string
		status  int
		problem string
	}{
		"no body":       {"", http.StatusBadRequest, "Say who to block."},
		"nobody named":  {`{}`, http.StatusBadRequest, "Say who to block."},
		"a blank name":  {`{"userId":"   "}`, http.StatusBadRequest, "Say who to block."},
		"not JSON":      {`{"userId":`, http.StatusBadRequest, "Could not read the request."},
		"too long":      {`{"userId":"` + strings.Repeat("a", maxBlockRequestBytes) + `"}`, http.StatusBadRequest, "Could not read the request."},
		"nobody at all": {`{"userId":"uid-ghost"}`, http.StatusNotFound, "There is no such user."},
	} {
		response := blockOverHTTP(t, p, refused.body, "uid-admin")
		require.Equal(t, refused.status, response.Code, name)
		require.Equal(t, refused.problem, errorOf(t, response), name)
	}

	require.NotContains(t, store, kvStillsBlocked)
	list, _ := adminStillsOf(t, p, "uid-admin")
	require.Empty(t, list.Blocked)
}

func TestABlockThatCannotBeSavedChangesNothing(t *testing.T) {
	store := map[string][]byte{}
	p, faults := stillsAdminPlugin(t, store)
	dave, bob := p.streamHandle("videokey-dave"), p.streamHandle("videokey-bob")

	shareStill(t, p, dave, "uid-troll", cardStill(t))

	faults.failWrite(kvStillsBlocked)
	response := blockOverHTTP(t, p, `{"userId":"uid-troll"}`, "uid-admin")
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Equal(t, "The block could not be saved, so nothing has changed. Try again in a moment.", errorOf(t, response))

	// Not kept, not in force, and their picture is still up.
	require.NotContains(t, store, kvStillsBlocked)
	require.NotNil(t, p.stills().current(dave, true, time.Now()))
	list, _ := adminStillsOf(t, p, "uid-admin")
	require.Empty(t, list.Blocked)
	require.Len(t, list.Stills, 1)
	claim, _ := claimOverHTTP(t, p, bob, stillModeWatching, "uid-troll")
	require.NotEmpty(t, claim.Lease)

	// Once the store takes it, the block goes through.
	faults.healWrite(kvStillsBlocked)
	requireOK(t, blockOverHTTP(t, p, `{"userId":"uid-troll"}`, "uid-admin"))
	require.Nil(t, p.stills().current(dave, true, time.Now()))

	// And the other way round: an unblock that cannot be saved leaves them
	// blocked, in the store and in force.
	faults.failWrite(kvStillsBlocked)
	response = unblockOverHTTP(t, p, "uid-troll", "uid-admin")
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Equal(t, "That could not be saved, so they are still blocked. Try again in a moment.", errorOf(t, response))
	require.Contains(t, storedBlocks(t, store), "uid-troll")

	_, response = claimOverHTTP(t, p, bob, stillModeWatching, "uid-troll")
	require.Equal(t, http.StatusForbidden, response.Code)
	list, _ = adminStillsOf(t, p, "uid-admin")
	require.Len(t, list.Blocked, 1)
}

func TestTheBlockListOutlivesTheProcess(t *testing.T) {
	store := map[string][]byte{}
	first, _ := stillsAdminPlugin(t, store)
	requireOK(t, blockOverHTTP(t, first, `{"userId":"uid-troll"}`, "uid-admin"))

	// A new process on the same store reads it on first use.
	second, _ := stillsAdminPlugin(t, store)
	_, response := claimOverHTTP(t, second, second.streamHandle("videokey-dave"), stillModeWatching, "uid-troll")
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Equal(t, "You can't share preview pictures.", errorOf(t, response))

	list, _ := adminStillsOf(t, second, "uid-admin")
	require.Len(t, list.Blocked, 1)
	require.Equal(t, "uid-troll", list.Blocked[0].UserID)

	// Listed by username, then id. Somebody whose account has gone since is
	// listed by id with no names, rather than costing the list its rows.
	store[kvStillsBlocked] = []byte(`{
		"uid-troll": {"blockedAt": "2026-09-01T10:00:00Z", "blockedBy": "uid-admin"},
		"uid-gone": {"blockedAt": "2026-09-02T10:00:00.5Z", "blockedBy": "uid-admin"},
		"uid-departed": {"blockedAt": "2026-09-03T10:00:00Z", "blockedBy": "uid-admin"},
		"uid-bob": {"blockedAt": "2026-09-04T10:00:00Z", "blockedBy": "uid-admin"}
	}`)

	third, _ := stillsAdminPlugin(t, store)
	list, _ = adminStillsOf(t, third, "uid-admin")
	require.Equal(t, []blockedSharer{
		{UserID: "uid-departed", BlockedAt: "2026-09-03T10:00:00Z"},
		{UserID: "uid-gone", BlockedAt: "2026-09-02T10:00:00Z"},
		{UserID: "uid-bob", Username: "bob", DisplayName: "Bob Turner", BlockedAt: "2026-09-04T10:00:00Z"},
		{UserID: "uid-troll", Username: "troll", DisplayName: "troll", BlockedAt: "2026-09-01T10:00:00Z"},
	}, list.Blocked)

	// Nothing stored at all is nobody blocked, and so is a stored null. Either
	// is read once and then held, like any other list: a null that left the
	// list looking unread would send every claim and every upload back to the
	// KV store, and sharing would still work, so only the count shows it.
	for _, stored := range [][]byte{nil, []byte("null")} {
		empty := map[string][]byte{}
		if stored != nil {
			empty[kvStillsBlocked] = stored
		}

		fresh, _ := stillsAdminPlugin(t, empty)
		shareStill(t, fresh, fresh.streamHandle("videokey-dave"), "uid-troll", cardStill(t))
		require.Equal(t, 1, blockListReads(fresh), "a claim and an upload, one read, with %q stored", stored)
	}
}

func TestABlockListThatCannotBeReadRefusesEverybodyUntilItCanBe(t *testing.T) {
	store := map[string][]byte{}
	first, _ := stillsAdminPlugin(t, store)
	requireOK(t, blockOverHTTP(t, first, `{"userId":"uid-troll"}`, "uid-admin"))

	p, faults := stillsAdminPlugin(t, store)
	dave := p.streamHandle("videokey-dave")
	faults.failRead(kvStillsBlocked)

	// Nobody shares while it is not known who may not.
	for _, userID := range []string{"uid-troll", "uid-dave"} {
		_, response := claimOverHTTP(t, p, dave, stillModeWatching, userID)
		require.Equal(t, http.StatusServiceUnavailable, response.Code, userID)
		require.Equal(t, "Preview pictures can't be shared right now. Try again in a moment.", errorOf(t, response))

		response = uploadOverHTTP(t, p, dave, "a-lease", cardStill(t), userID)
		require.Equal(t, http.StatusServiceUnavailable, response.Code, userID)
	}

	// One read for all of that, and one line in the log: a store that has
	// just failed is not asked again by every panel.
	require.Equal(t, 1, blockListReads(p))
	require.Len(t, logLines(p, "LogWarn", "Could not read who is blocked from sharing preview pictures"), 1)

	// And the admin is told, rather than shown an empty list or handed a
	// list with everybody else forgotten to write back. Each of theirs does
	// ask the store, wait or no wait: it is the list as stored that they are
	// asking about, and an admin is one person rather than every panel.
	unread := "The list of who can't share preview pictures could not be read. Try again in a moment."
	for _, response := range []*httptest.ResponseRecorder{
		stillRequest(t, p, http.MethodGet, "/api/v1/admin/stills", nil, "uid-admin"),
		blockOverHTTP(t, p, `{"userId":"uid-bob"}`, "uid-admin"),
		unblockOverHTTP(t, p, "uid-troll", "uid-admin"),
	} {
		require.Equal(t, http.StatusInternalServerError, response.Code)
		require.Equal(t, unread, errorOf(t, response))
	}
	require.Contains(t, storedBlocks(t, store), "uid-troll")
	require.NotContains(t, storedBlocks(t, store), "uid-bob")
	require.Equal(t, 4, blockListReads(p))

	// The store comes back. Inside the wait nothing has changed; after it,
	// the list is read and in force.
	faults.healRead(kvStillsBlocked)
	_, response := claimOverHTTP(t, p, dave, stillModeWatching, "uid-dave")
	require.Equal(t, http.StatusServiceUnavailable, response.Code)

	store2 := p.stills()
	store2.blockLock.Lock()
	store2.blocksFailedAt = time.Now().Add(-stillBlocksRetry)
	store2.blockLock.Unlock()

	_, response = claimOverHTTP(t, p, dave, stillModeWatching, "uid-troll")
	require.Equal(t, http.StatusForbidden, response.Code)
	shareStill(t, p, dave, "uid-dave", cardStill(t))

	// A document that will not parse is the same as one that cannot be
	// read, and is never written over.
	store[kvStillsBlocked] = []byte("{not json")
	broken, _ := stillsAdminPlugin(t, store)
	_, response = claimOverHTTP(t, broken, dave, stillModeWatching, "uid-dave")
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
	response = blockOverHTTP(t, broken, `{"userId":"uid-bob"}`, "uid-admin")
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Equal(t, "{not json", string(store[kvStillsBlocked]))
}

// Panels claim and upload while an admin blocks, lists, takes down and
// unblocks, all at once. Run under -race, this is what finds a map touched
// outside its lock; and whatever order it all landed in, a block made after
// it leaves nothing of theirs on the cards.
func TestBlockingAndSharingAtOnceIsSafe(t *testing.T) {
	p, _ := stillsAdminPlugin(t, map[string][]byte{})
	handles := []string{p.streamHandle("videokey-dave"), p.streamHandle("videokey-bob"), p.streamHandle("videokey-erin")}
	still := cardStill(t)

	var wg sync.WaitGroup

	// No require in here: it may only stop the test from the test's own
	// goroutine.
	for _, userID := range []string{"uid-dave", "uid-bob", "uid-troll"} {
		wg.Add(1)
		go func() {
			defer wg.Done()

			for i := range 15 {
				handle := handles[i%len(handles)]
				response := stillRequest(t, p, http.MethodPost, "/api/v1/stills/claim?handle="+handle+"&mode=watching", nil, userID)

				var claim stillClaim
				if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &claim) != nil || claim.Lease == "" {
					continue
				}

				stillRequest(t, p, http.MethodPost, "/api/v1/stills?handle="+handle+"&lease="+claim.Lease, still, userID)
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()

		for i := range 10 {
			blockOverHTTP(t, p, `{"userId":"uid-troll"}`, "uid-admin")
			stillRequest(t, p, http.MethodGet, "/api/v1/admin/stills", nil, "uid-admin")
			removeOverHTTP(t, p, handles[i%len(handles)], "uid-admin")
			unblockOverHTTP(t, p, "uid-troll", "uid-admin")
		}
	}()

	wg.Wait()

	requireOK(t, blockOverHTTP(t, p, `{"userId":"uid-troll"}`, "uid-admin"))

	list, _ := adminStillsOf(t, p, "uid-admin")
	for _, shown := range list.Stills {
		require.NotEqual(t, "uid-troll", shown.ByUserID)
	}

	store := p.stills()
	store.lock.Lock()
	defer store.lock.Unlock()

	for handle, entry := range store.entries {
		require.False(t, entry.lease != nil && entry.lease.userID == "uid-troll", handle)
		require.False(t, entry.picture != nil && entry.picture.by == "uid-troll", handle)
	}
}

// Admins blocking and unblocking different people at the same moment: two
// admins, or one with two tabs open. Each change reads the whole list, changes
// it and writes the whole list back, and a write made from a list read before
// somebody else's must never quietly undo theirs. Through one node the lock
// makes them take turns, so none of them should even lose a race to the store
// and have to try again. A lost change only shows when the writes interleave,
// so it is tried over and over.
func TestAdminsChangingTheBlockListAtOnceLoseNothing(t *testing.T) {
	targets := []string{"uid-admin", "uid-user", "uid-dave", "uid-bob", "uid-troll"}

	// Every change at once, each from its own goroutine, let go together. The
	// status codes are gathered and checked afterwards: require may only stop
	// the test from the test's own goroutine.
	allAtOnce := func(change func(target string) int) []int {
		codes := make([]int, len(targets))
		start := make(chan struct{})

		var wg sync.WaitGroup
		for i, target := range targets {
			wg.Add(1)
			go func() {
				defer wg.Done()

				<-start
				codes[i] = change(target)
			}()
		}

		close(start)
		wg.Wait()

		return codes
	}

	for round := range 20 {
		store := map[string][]byte{}
		p, _ := stillsAdminPlugin(t, store)

		// The list is read first, as it is once the admin page is open, so
		// that what the goroutines race over is the change and the write
		// rather than the first read.
		_, response := adminStillsOf(t, p, "uid-admin")
		require.Equal(t, http.StatusOK, response.Code)

		codes := allAtOnce(func(target string) int {
			return blockOverHTTP(t, p, `{"userId":"`+target+`"}`, "uid-admin").Code
		})
		for i, code := range codes {
			require.Equal(t, http.StatusOK, code, "round %d, blocking %s", round, targets[i])
		}

		// Every one of them kept, and every one in force, each written once.
		require.Len(t, storedBlocks(t, store), len(targets), "round %d", round)
		require.Equal(t, len(targets), atomicWriteTries(p, kvStillsBlocked), "round %d", round)
		list, _ := adminStillsOf(t, p, "uid-admin")
		require.Len(t, list.Blocked, len(targets), "round %d", round)

		codes = allAtOnce(func(target string) int {
			return unblockOverHTTP(t, p, target, "uid-admin").Code
		})
		for i, code := range codes {
			require.Equal(t, http.StatusOK, code, "round %d, unblocking %s", round, targets[i])
		}

		// And every one of them let go again, in the store and in force.
		require.Empty(t, storedBlocks(t, store), "round %d", round)
		require.Equal(t, 2*len(targets), atomicWriteTries(p, kvStillsBlocked), "round %d", round)
		list, _ = adminStillsOf(t, p, "uid-admin")
		require.Empty(t, list.Blocked, "round %d", round)
	}
}

// On a cluster every node holds a copy of the block list, and an admin's
// request lands on whichever node it lands on. The tests below are one node
// with the store changed underneath it, as another node would change it.

func TestABlockMadeThroughAnotherNodeSurvivesABlockMadeHere(t *testing.T) {
	store := map[string][]byte{}
	p, _ := stillsAdminPlugin(t, store)
	dave, bob := p.streamHandle("videokey-dave"), p.streamHandle("videokey-bob")

	// Bob shares through this node, which reads the list, empty, to let them.
	shareStill(t, p, dave, "uid-bob", cardStill(t))

	// Another node blocks Bob. Claims here go by the copy this node holds,
	// so until something makes it read the list again, Bob can still claim.
	blockElsewhere(t, store, "uid-bob")
	pending, response := claimOverHTTP(t, p, bob, stillModeWatching, "uid-bob")
	require.Equal(t, http.StatusOK, response.Code)
	require.NotEmpty(t, pending.Lease)
	elsewhere := string(store[kvStillsBlocked])

	// An admin blocks the troll through this node. The list written is the
	// one stored with the troll added, not this node's copy with the troll
	// added, so Bob's block is kept exactly as the other node made it.
	requireOK(t, blockOverHTTP(t, p, `{"userId":"uid-troll"}`, "uid-admin"))

	blocks := storedBlocks(t, store)
	require.Len(t, blocks, 2)
	require.Equal(t, "uid-elsewhere", blocks["uid-bob"].BlockedBy)
	require.Equal(t, time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC), blocks["uid-bob"].BlockedAt)
	require.Equal(t, "uid-admin", blocks["uid-troll"].BlockedBy)
	require.NotEqual(t, elsewhere, string(store[kvStillsBlocked]))

	// Both are in force here now: neither can claim, and what Bob put up
	// through this node, and the lease they were holding, are gone.
	for _, userID := range []string{"uid-bob", "uid-troll"} {
		_, response = claimOverHTTP(t, p, dave, stillModeWatching, userID)
		require.Equal(t, http.StatusForbidden, response.Code, userID)
	}
	require.Nil(t, p.stills().current(dave, true, time.Now()))

	response = uploadOverHTTP(t, p, bob, pending.Lease, stillJPEG(t, 640, 360), "uid-bob")
	require.Equal(t, http.StatusForbidden, response.Code)

	list, _ := adminStillsOf(t, p, "uid-admin")
	require.Len(t, list.Blocked, 2)
	require.Empty(t, list.Stills)
	requireNoKeyLogged(t, p)
}

func TestUnblockingSomebodyBlockedThroughAnotherNodeLetsThemGo(t *testing.T) {
	store := map[string][]byte{}
	p, _ := stillsAdminPlugin(t, store)
	dave := p.streamHandle("videokey-dave")

	// This node reads the list while it is empty.
	list, _ := adminStillsOf(t, p, "uid-admin")
	require.Empty(t, list.Blocked)

	// Another node blocks the troll. The admin list here shows it, because it
	// reads the list as stored. Nothing has been claimed here yet, so this
	// node has no copy of its own: the first claim reads the list, block and
	// all.
	blockElsewhere(t, store, "uid-troll")
	list, _ = adminStillsOf(t, p, "uid-admin")
	require.Len(t, list.Blocked, 1)
	require.Equal(t, "uid-troll", list.Blocked[0].UserID)

	_, response := claimOverHTTP(t, p, dave, stillModeWatching, "uid-troll")
	require.Equal(t, http.StatusForbidden, response.Code)

	// Unblocked through this node: off the list as stored, and able to share
	// here at once.
	requireOK(t, unblockOverHTTP(t, p, "uid-troll", "uid-admin"))
	require.Empty(t, storedBlocks(t, store))
	shareStill(t, p, dave, "uid-troll", cardStill(t))

	// And the same for a block this node had never heard of at all until the
	// admin asked for it to be lifted.
	blockElsewhere(t, store, "uid-bob")
	requireOK(t, unblockOverHTTP(t, p, "uid-bob", "uid-admin"))
	require.Empty(t, storedBlocks(t, store))
	require.Len(t, logLines(p, "LogInfo", "A system admin let somebody share preview pictures again"), 2)
}

func TestTheAdminListNeverUndoesABlockInForce(t *testing.T) {
	store := map[string][]byte{}
	p, _ := stillsAdminPlugin(t, store)
	dave := p.streamHandle("videokey-dave")

	requireOK(t, blockOverHTTP(t, p, `{"userId":"uid-troll"}`, "uid-admin"))
	blocked := string(store[kvStillsBlocked])

	// A read replica that has not caught up with the block yet: the admin
	// list reads the list without it, and says so, but the block made here
	// a moment ago stays in force.
	delete(store, kvStillsBlocked)
	list, _ := adminStillsOf(t, p, "uid-admin")
	require.Empty(t, list.Blocked)

	_, response := claimOverHTTP(t, p, dave, stillModeWatching, "uid-troll")
	require.Equal(t, http.StatusForbidden, response.Code)

	// Once it has caught up, the list shows the block again, and nothing
	// was written in between.
	store[kvStillsBlocked] = []byte(blocked)
	list, _ = adminStillsOf(t, p, "uid-admin")
	require.Len(t, list.Blocked, 1)
	require.Equal(t, 1, atomicWriteTries(p, kvStillsBlocked))
}

func TestABlockListChangeThatLosesARaceIsMadeAgain(t *testing.T) {
	store := map[string][]byte{}
	p, faults := stillsAdminPlugin(t, store)
	dave := p.streamHandle("videokey-dave")

	// Another node blocks Bob in the moment between this node reading the
	// list and writing it back: here, the first list anybody has stored, so
	// this node's write is the one that only creates it if nobody else has.
	raced := 0
	faults.setInterfere(func(key string, store map[string][]byte) {
		if key == kvStillsBlocked && raced == 0 {
			raced++
			blockElsewhere(t, store, "uid-bob")
		}
	})

	requireOK(t, blockOverHTTP(t, p, `{"userId":"uid-troll"}`, "uid-admin"))
	require.Equal(t, 1, raced)
	require.Equal(t, 2, atomicWriteTries(p, kvStillsBlocked), "lost once, then made again")
	require.Equal(t, 1, faults.writes[kvStillsBlocked])

	blocks := storedBlocks(t, store)
	require.Contains(t, blocks, "uid-bob", "the other node's block is kept")
	require.Contains(t, blocks, "uid-troll", "and this one is made on top of it")

	for _, userID := range []string{"uid-bob", "uid-troll"} {
		_, response := claimOverHTTP(t, p, dave, stillModeWatching, userID)
		require.Equal(t, http.StatusForbidden, response.Code, userID)
	}

	// An unblock the same way: the troll is let go, and the block the other
	// node made in the meantime stays, in the store and in force.
	raced = 0
	faults.setInterfere(func(key string, store map[string][]byte) {
		if key == kvStillsBlocked && raced == 0 {
			raced++
			blockElsewhere(t, store, "uid-dave")
		}
	})

	requireOK(t, unblockOverHTTP(t, p, "uid-troll", "uid-admin"))
	require.Equal(t, 1, raced)
	require.Equal(t, 4, atomicWriteTries(p, kvStillsBlocked))

	blocks = storedBlocks(t, store)
	require.Len(t, blocks, 2)
	require.Contains(t, blocks, "uid-bob")
	require.Contains(t, blocks, "uid-dave")

	_, response := claimOverHTTP(t, p, dave, stillModeWatching, "uid-dave")
	require.Equal(t, http.StatusForbidden, response.Code)
	shareStill(t, p, dave, "uid-troll", cardStill(t))

	// Somebody else writes the list every single time this node tries. The
	// change is given up as unsaved rather than written over theirs, and
	// nothing of it is in force.
	others := 0
	faults.setInterfere(func(key string, store map[string][]byte) {
		if key == kvStillsBlocked {
			others++
			blockElsewhere(t, store, fmt.Sprintf("uid-other-%d", others))
		}
	})

	response = blockOverHTTP(t, p, `{"userId":"uid-user"}`, "uid-admin")
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.Equal(t, "The block could not be saved, so nothing has changed. Try again in a moment.", errorOf(t, response))
	require.Equal(t, stillBlocksAttempts, others)
	require.NotContains(t, storedBlocks(t, store), "uid-user")
	require.Len(t, storedBlocks(t, store), 2+stillBlocksAttempts, "and everybody else's is there")

	faults.setInterfere(nil)
	claim, _ := claimOverHTTP(t, p, p.streamHandle("videokey-bob"), stillModeWatching, "uid-user")
	require.NotEmpty(t, claim.Lease)
	require.Len(t, logLines(p, "LogWarn", "Could not save who is blocked from sharing preview pictures, because it kept changing while it was being saved"), 1)
}

// A stored list that will not parse is never written over, not even by a node
// that has a good copy in force. That copy can be missing every block made
// through another node since it was read, and the document, damaged or not,
// is the only record of them.
func TestABlockListThatWillNotParseIsLeftAlone(t *testing.T) {
	store := map[string][]byte{}
	p, faults := stillsAdminPlugin(t, store)
	dave := p.streamHandle("videokey-dave")

	requireOK(t, blockOverHTTP(t, p, `{"userId":"uid-troll"}`, "uid-admin"))
	tries := atomicWriteTries(p, kvStillsBlocked)

	store[kvStillsBlocked] = []byte("{not json")

	unread := "The list of who can't share preview pictures could not be read. Try again in a moment."
	for _, response := range []*httptest.ResponseRecorder{
		blockOverHTTP(t, p, `{"userId":"uid-bob"}`, "uid-admin"),
		unblockOverHTTP(t, p, "uid-troll", "uid-admin"),
		stillRequest(t, p, http.MethodGet, "/api/v1/admin/stills", nil, "uid-admin"),
	} {
		require.Equal(t, http.StatusInternalServerError, response.Code)
		require.Equal(t, unread, errorOf(t, response))
	}

	require.Equal(t, "{not json", string(store[kvStillsBlocked]))
	require.Equal(t, tries, atomicWriteTries(p, kvStillsBlocked), "not so much as tried")
	require.Equal(t, 1, faults.writes[kvStillsBlocked])

	// What is in force here stays as it was: the troll blocked, and Bob, whom
	// the admin could not block, still sharing.
	_, response := claimOverHTTP(t, p, dave, stillModeWatching, "uid-troll")
	require.Equal(t, http.StatusForbidden, response.Code)
	shareStill(t, p, dave, "uid-bob", cardStill(t))
}
