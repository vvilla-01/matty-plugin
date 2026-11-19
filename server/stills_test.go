package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// A real JPEG, encoded here rather than checked in, so the size and the shape
// are whatever the test says they are.
func stillJPEG(t *testing.T, width, height int) []byte {
	t.Helper()

	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			picture.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 90, A: 255})
		}
	}

	var encoded bytes.Buffer
	require.NoError(t, jpeg.Encode(&encoded, picture, &jpeg.Options{Quality: 72}))

	return encoded.Bytes()
}

// The shape the panel sends.
func cardStill(t *testing.T) []byte {
	t.Helper()

	return stillJPEG(t, 480, 270)
}

// A plugin with live previews on, three people live and one 24/7 channel.
func stillsPlugin(t *testing.T) *Plugin {
	t.Helper()

	p, store := channelPicturePlugin(t)
	cfg := &configuration{AlwaysOnStreamKeys: "videokey-watts", LivePreviews: true}
	cfg.normalize()
	p.setConfiguration(cfg)

	streams := []apiStream{}
	for _, key := range []string{"videokey-dave", "videokey-bob", "videokey-erin"} {
		streams = append(streams, apiStream{StreamKey: key, Handle: p.streamHandle(key)})
	}
	streams = append(streams, apiStream{StreamKey: "videokey-watts", Handle: p.streamHandle("videokey-watts"), AlwaysOn: true})
	p.setSnapshot(&snapshot{Streams: streams, Configured: true, LivePreviews: true})

	// Everybody these tests name has unlocked the panel: who may take and
	// see stills is TestRouteAccessTable's business, not theirs.
	for _, userID := range []string{"uid-a", "uid-b", "uid-c", "uid-z", "uid-user"} {
		linkForTest(store, userID, "videokey-test-"+userID)
	}

	return p
}

// Through ServeHTTP, as Mattermost would call it, so the routes are tested too.
func stillRequest(t *testing.T, p *Plugin, method, target string, body []byte, userID string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	request.Header.Set("Mattermost-User-Id", userID)
	recorder := httptest.NewRecorder()

	p.ServeHTTP(nil, recorder, request)

	return recorder
}

func claimOverHTTP(t *testing.T, p *Plugin, handle, mode, userID string) (stillClaim, *httptest.ResponseRecorder) {
	t.Helper()

	response := stillRequest(t, p, http.MethodPost, "/api/v1/stills/claim?handle="+handle+"&mode="+mode, nil, userID)

	var claim stillClaim
	if response.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &claim))
	}

	return claim, response
}

func uploadOverHTTP(t *testing.T, p *Plugin, handle, lease string, body []byte, userID string) *httptest.ResponseRecorder {
	t.Helper()

	return stillRequest(t, p, http.MethodPost, "/api/v1/stills?handle="+handle+"&lease="+lease, body, userID)
}

func errorOf(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()

	var body errorResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body), response.Body.String())

	return body.Error
}

var stillEpoch = time.Date(2026, 9, 28, 20, 0, 0, 0, time.UTC)

func TestAStillIsClaimedOnceForEverybody(t *testing.T) {
	s := newStillStore()

	first := s.claim("h1", "uid-a", stillModeWatching, stillEpoch)
	require.NotEmpty(t, first.Lease)
	require.Equal(t, 20, first.TTL)
	require.Zero(t, first.RetryAfter)

	// Anybody else, either way, while it is held: not now, and when.
	for _, mode := range []string{stillModeWatching, stillModePeek} {
		other := s.claim("h1", "uid-b", mode, stillEpoch.Add(5*time.Second))
		require.Empty(t, other.Lease, mode)
		require.Equal(t, 15, other.RetryAfter, mode)
	}

	// The holder asking again is refused too. One capture at a time.
	require.Empty(t, s.claim("h1", "uid-a", stillModeWatching, stillEpoch.Add(time.Second)).Lease)

	// Once it runs out unused, the attempt holds the stream back as a still
	// would have, and then the next asker has it.
	require.Empty(t, s.claim("h1", "uid-b", stillModeWatching, stillEpoch.Add(stillLeaseTTL)).Lease)
	next := s.claim("h1", "uid-b", stillModeWatching, stillEpoch.Add(stillStaleAfter))
	require.NotEmpty(t, next.Lease)
	require.NotEqual(t, first.Lease, next.Lease)
}

func TestAWatchingBrowserHasAHeadStartOverAPeek(t *testing.T) {
	// The first still is taken by whoever asks, in either mode: there is
	// nothing yet to keep fresh.
	for _, mode := range []string{stillModeWatching, stillModePeek} {
		require.NotEmpty(t, newStillStore().claim("h1", "uid-a", mode, stillEpoch).Lease, mode)
	}

	s := newStillStore()
	lease := s.claim("h1", "uid-a", stillModeWatching, stillEpoch).Lease
	_, stored := s.put("h1", "uid-a", lease, []byte("still"), stillEpoch)
	require.True(t, stored)

	// Fresh: neither.
	early := stillEpoch.Add(30 * time.Second)
	require.Equal(t, 60, s.claim("h1", "uid-b", stillModeWatching, early).RetryAfter)
	require.Equal(t, 120, s.claim("h1", "uid-b", stillModePeek, early).RetryAfter)

	// Stale: a browser that is watching may retake it, one that would have to
	// peek is told to wait out the head start.
	stale := stillEpoch.Add(stillStaleAfter)
	require.Equal(t, 60, s.claim("h1", "uid-b", stillModePeek, stale).RetryAfter)
	watching := s.claim("h1", "uid-b", stillModeWatching, stale)
	require.NotEmpty(t, watching.Lease)

	// Nobody watching took it: after the head start, a peek may.
	s = newStillStore()
	lease = s.claim("h1", "uid-a", stillModeWatching, stillEpoch).Lease
	_, stored = s.put("h1", "uid-a", lease, []byte("still"), stillEpoch)
	require.True(t, stored)

	require.Empty(t, s.claim("h1", "uid-b", stillModePeek, stillEpoch.Add(stillStaleAfter+stillWatcherHeadStart-time.Second)).Lease)
	require.NotEmpty(t, s.claim("h1", "uid-b", stillModePeek, stillEpoch.Add(stillStaleAfter+stillWatcherHeadStart)).Lease)
}

// A capture that fails sends nothing back: its lease simply runs out. That has
// to cost the stream the same wait as a capture that worked, or a stream no
// browser can capture is handed a new peek, a session on the origin, every
// time a lease runs out.
func TestAnUnusedPeekLeaseHoldsTheStreamBackLikeAStill(t *testing.T) {
	s := newStillStore()
	require.NotEmpty(t, s.claim("h1", "uid-a", stillModePeek, stillEpoch).Lease)

	// Run out, unused, and no still to be stale against: nobody else peeks
	// until the usual interval has passed since it was given out.
	ranOut := stillEpoch.Add(stillLeaseTTL)
	refused := s.claim("h1", "uid-b", stillModePeek, ranOut)
	require.Empty(t, refused.Lease)
	require.Equal(t, 130, refused.RetryAfter, "asked back for when the interval is up, not for the next tick")

	// A poll in between forgets nothing.
	s.sweep(stillEpoch.Add(time.Minute), true)

	require.Empty(t, s.claim("h1", "uid-b", stillModePeek, stillEpoch.Add(stillStaleAfter+stillWatcherHeadStart-time.Second)).Lease)
	require.NotEmpty(t, s.claim("h1", "uid-b", stillModePeek, stillEpoch.Add(stillStaleAfter+stillWatcherHeadStart)).Lease)

	// A browser that is watching keeps its head start over that: the same
	// attempt holds it back for the plain interval only.
	s = newStillStore()
	s.claim("h1", "uid-a", stillModePeek, stillEpoch)
	require.Equal(t, 70, s.claim("h1", "uid-b", stillModeWatching, ranOut).RetryAfter)
	require.NotEmpty(t, s.claim("h1", "uid-b", stillModeWatching, stillEpoch.Add(stillStaleAfter)).Lease)
}

// And the other way round: a watching browser whose frame came to nothing
// holds back a peek just as its still would have.
func TestAnUnusedWatchingLeaseHoldsBackAPeekToo(t *testing.T) {
	s := newStillStore()
	require.NotEmpty(t, s.claim("h1", "uid-a", stillModeWatching, stillEpoch).Lease)

	for _, after := range []time.Duration{stillLeaseTTL, stillStaleAfter, stillStaleAfter + stillWatcherHeadStart - time.Second} {
		require.Empty(t, s.claim("h1", "uid-b", stillModePeek, stillEpoch.Add(after)).Lease, after)
	}

	require.NotEmpty(t, s.claim("h1", "uid-b", stillModePeek, stillEpoch.Add(stillStaleAfter+stillWatcherHeadStart)).Lease)
}

func TestAPeekWaitsLongerForEachCaptureInARowThatCameToNothing(t *testing.T) {
	require.Equal(t, 150*time.Second, stillWait(stillModePeek, 0))
	require.Equal(t, 150*time.Second, stillWait(stillModePeek, 1), "one that failed costs what a still would have")
	require.Equal(t, 300*time.Second, stillWait(stillModePeek, 2))
	require.Equal(t, 600*time.Second, stillWait(stillModePeek, 3))
	require.Equal(t, stillMissWaitMax, stillWait(stillModePeek, 4))
	require.Equal(t, stillMissWaitMax, stillWait(stillModePeek, 1<<20), "and no further however long it goes on")

	// A frame from a tile costs the origin nothing, so it is never made to
	// wait longer than usual.
	for _, misses := range []int{0, 1, 2, 10} {
		require.Equal(t, stillStaleAfter, stillWait(stillModeWatching, misses), misses)
	}

	// Through claim, with the polls that would come in between, none of
	// which may forget a miss: each peek lease runs out unused, and the next
	// is given out the moment it is allowed.
	s := newStillStore()
	at := stillEpoch
	for _, wait := range []time.Duration{150 * time.Second, 300 * time.Second, 600 * time.Second, 600 * time.Second} {
		require.NotEmpty(t, s.claim("h1", "uid-a", stillModePeek, at).Lease, wait)

		for poll := at; poll.Before(at.Add(wait)); poll = poll.Add(15 * time.Second) {
			s.seen("h1", poll)
			s.sweep(poll, true)
		}

		require.Empty(t, s.claim("h1", "uid-b", stillModePeek, at.Add(wait-time.Second)).Lease, wait)
		at = at.Add(wait)
	}

	// A browser that is watching still gets in at the usual interval...
	require.NotEmpty(t, s.claim("h1", "uid-a", stillModePeek, at).Lease)
	watching := s.claim("h1", "uid-c", stillModeWatching, at.Add(stillStaleAfter))
	require.NotEmpty(t, watching.Lease)

	// ...and once a still arrives, the stream is back where it started: the
	// next peek waits the usual interval from it.
	taken := at.Add(stillStaleAfter + time.Second)
	_, stored := s.put("h1", "uid-c", watching.Lease, []byte("a still at last"), taken)
	require.True(t, stored)

	next := taken.Add(stillStaleAfter + stillWatcherHeadStart)
	require.Empty(t, s.claim("h1", "uid-b", stillModePeek, next.Add(-time.Second)).Lease)
	require.NotEmpty(t, s.claim("h1", "uid-b", stillModePeek, next).Lease)

	// And if that one comes to nothing, the doubling starts again from the
	// bottom rather than from where it had got to.
	require.Empty(t, s.claim("h1", "uid-a", stillModePeek, next.Add(stillStaleAfter+stillWatcherHeadStart-time.Second)).Lease)
	require.NotEmpty(t, s.claim("h1", "uid-a", stillModePeek, next.Add(stillStaleAfter+stillWatcherHeadStart)).Lease)
}

func TestRetryAfterStaysInsideItsBounds(t *testing.T) {
	require.Equal(t, 5, retryAfter(-time.Minute))
	require.Equal(t, 5, retryAfter(0))
	require.Equal(t, 5, retryAfter(4200*time.Millisecond))
	require.Equal(t, 6, retryAfter(5100*time.Millisecond), "rounded up, so a panel never asks a moment early")
	require.Equal(t, 300, retryAfter(time.Hour))

	// And as a claim sees it: a lease a second from running out still says
	// five, not one.
	s := newStillStore()
	s.claim("h1", "uid-a", stillModeWatching, stillEpoch)
	require.Equal(t, 5, s.claim("h1", "uid-b", stillModeWatching, stillEpoch.Add(stillLeaseTTL-time.Second)).RetryAfter)
}

func TestALeaseRunsOut(t *testing.T) {
	s := newStillStore()
	lease := s.claim("h1", "uid-a", stillModePeek, stillEpoch).Lease
	require.True(t, s.takePeek("h1", "uid-a", lease, stillEpoch.Add(time.Second)))

	require.True(t, s.holds("h1", "uid-a", lease, stillEpoch.Add(stillLeaseTTL-time.Millisecond)))
	require.False(t, s.holds("h1", "uid-a", lease, stillEpoch.Add(stillLeaseTTL)))

	_, stored := s.put("h1", "uid-a", lease, []byte("late"), stillEpoch.Add(stillLeaseTTL))
	require.False(t, stored, "a still that arrives after its lease is not kept")
	require.Nil(t, s.current("h1", true, stillEpoch.Add(stillLeaseTTL)))
}

func TestOnePersonCannotHoldEveryStream(t *testing.T) {
	s := newStillStore()

	require.NotEmpty(t, s.claim("h1", "uid-a", stillModeWatching, stillEpoch).Lease)
	require.NotEmpty(t, s.claim("h2", "uid-a", stillModeWatching, stillEpoch).Lease)

	third := s.claim("h3", "uid-a", stillModeWatching, stillEpoch.Add(time.Second))
	require.Empty(t, third.Lease)
	require.Equal(t, 19, third.RetryAfter, "asked back for when their first lease is due to run out")

	require.NotEmpty(t, s.claim("h3", "uid-b", stillModeWatching, stillEpoch).Lease, "somebody else is not held up by it")
}

func TestOnlyTheLeaseHolderCanUploadAStill(t *testing.T) {
	p := stillsPlugin(t)
	dave, bob := p.streamHandle("videokey-dave"), p.streamHandle("videokey-bob")

	claim, response := claimOverHTTP(t, p, dave, stillModeWatching, "uid-a")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.NotEmpty(t, claim.Lease)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

	still := cardStill(t)

	// Somebody else, with the right lease.
	response = uploadOverHTTP(t, p, dave, claim.Lease, still, "uid-b")
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Equal(t, "That capture was not yours to make.", errorOf(t, response))

	// The right person, the right lease, the wrong stream.
	response = uploadOverHTTP(t, p, bob, claim.Lease, still, "uid-a")
	require.Equal(t, http.StatusForbidden, response.Code)

	// A lease nobody was given.
	response = uploadOverHTTP(t, p, dave, "made-up", still, "uid-a")
	require.Equal(t, http.StatusForbidden, response.Code)

	// No lease at all.
	response = uploadOverHTTP(t, p, dave, "", still, "uid-a")
	require.Equal(t, http.StatusForbidden, response.Code)

	require.Nil(t, p.stills().current(dave, true, time.Now()), "nothing refused is kept")

	// The holder.
	response = uploadOverHTTP(t, p, dave, claim.Lease, still, "uid-a")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var answer map[string]string
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer))
	require.Len(t, answer, 2, "the version and when, and nothing else")
	require.Equal(t, pictureVersion(still), answer["version"])
	taken, err := time.Parse(time.RFC3339, answer["at"])
	require.NoError(t, err)
	require.WithinDuration(t, time.Now(), taken, time.Minute)

	// And who took it, which is what makes a picture traceable to an account.
	require.Equal(t, "uid-a", p.stills().current(dave, true, time.Now()).by)

	// Spent: the same lease does not upload twice.
	response = uploadOverHTTP(t, p, dave, claim.Lease, stillJPEG(t, 640, 360), "uid-a")
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Equal(t, pictureVersion(still), p.stills().current(dave, true, time.Now()).version)
}

// A peek lease is for taking a still out of a peek, so nothing is uploaded
// under one until its peek has been asked for. A watching lease needs no such
// thing: that browser already has the stream playing.
func TestAStillUnderAPeekLeaseHasToFollowItsPeek(t *testing.T) {
	origin, _ := fakeWHEPOrigin(t)
	defer origin.Close()

	p := stillsPlugin(t)
	p.httpClient = origin.Client()
	cfg := &configuration{BroadcastBoxURL: origin.URL, AlwaysOnStreamKeys: "videokey-watts", LivePreviews: true}
	cfg.normalize()
	p.setConfiguration(cfg)

	dave := p.streamHandle("videokey-dave")
	claim, _ := claimOverHTTP(t, p, dave, stillModePeek, "uid-a")
	require.NotEmpty(t, claim.Lease)

	response := uploadOverHTTP(t, p, dave, claim.Lease, cardStill(t), "uid-a")
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Equal(t, "That capture was not yours to make.", errorOf(t, response))
	require.Nil(t, p.stills().current(dave, true, time.Now()))

	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, claim.Lease, "uid-a").Code)
	response = uploadOverHTTP(t, p, dave, claim.Lease, cardStill(t), "uid-a")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	// A watching lease uploads straight away, as it always has.
	bob := p.streamHandle("videokey-bob")
	watching, _ := claimOverHTTP(t, p, bob, stillModeWatching, "uid-a")
	require.NotEmpty(t, watching.Lease)
	response = uploadOverHTTP(t, p, bob, watching.Lease, cardStill(t), "uid-a")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
}

// Who put which picture on a card goes to Info, where the Server Logs page can
// find it, by handle and never by key. One line per stream per stale interval:
// an upload inside that, which only a picture dropped early allows, goes to
// Debug instead.
func TestASharedStillIsLoggedAtInfoWithWhoTookIt(t *testing.T) {
	p := stillsPlugin(t)
	dave := p.streamHandle("videokey-dave")
	store := p.stills()

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	shared := func(level string) [][]any {
		t.Helper()

		var lines [][]any
		for _, call := range api.Calls {
			if call.Method == level && len(call.Arguments) > 0 && call.Arguments[0] == "A preview still was shared" {
				lines = append(lines, []any(call.Arguments))
			}
		}

		return lines
	}

	upload := func(userID string, still []byte) {
		t.Helper()

		claim, _ := claimOverHTTP(t, p, dave, stillModeWatching, userID)
		require.NotEmpty(t, claim.Lease)
		response := uploadOverHTTP(t, p, dave, claim.Lease, still, userID)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}

	// Made stale by hand, as a replacement rather than an edit.
	stale := func() {
		t.Helper()

		store.lock.Lock()
		defer store.lock.Unlock()

		old := store.entries[dave].picture
		store.entries[dave].picture = &stillPicture{data: old.data, version: old.version, at: old.at.Add(-stillStaleAfter), by: old.by}
	}

	first := cardStill(t)
	upload("uid-a", first)
	require.Equal(t, [][]any{{"A preview still was shared", "user_id", "uid-a", "stream_handle", dave, "version", pictureVersion(first)}}, shared("LogInfo"))

	stale()
	second := stillJPEG(t, 640, 360)
	upload("uid-b", second)
	require.Len(t, shared("LogInfo"), 1, "not a second line inside the interval")
	require.Equal(t, [][]any{{"A preview still was shared", "user_id", "uid-b", "stream_handle", dave, "version", pictureVersion(second)}}, shared("LogDebug"))

	// Once the interval has passed, the next is at Info again.
	stale()
	store.lock.Lock()
	store.announced[dave] = time.Now()
	store.lock.Unlock()
	upload("uid-c", stillJPEG(t, 800, 450))
	require.Len(t, shared("LogInfo"), 2)

	for _, call := range api.Calls {
		if !strings.HasPrefix(call.Method, "Log") {
			continue
		}
		for _, argument := range call.Arguments {
			require.NotContains(t, fmt.Sprint(argument), "videokey-dave", call.Method)
		}
	}

	// And the marks go once they have passed.
	s := newStillStore()
	require.True(t, s.announce("h1", stillEpoch))
	require.False(t, s.announce("h1", stillEpoch.Add(stillStaleAfter-time.Second)))
	require.True(t, s.announce("h1", stillEpoch.Add(stillStaleAfter)))
	s.sweep(stillEpoch.Add(2*stillStaleAfter), true)
	require.Empty(t, s.announced)
}

func TestAnExpiredLeaseCannotUpload(t *testing.T) {
	p := stillsPlugin(t)
	dave := p.streamHandle("videokey-dave")

	claim, _ := claimOverHTTP(t, p, dave, stillModePeek, "uid-a")
	require.NotEmpty(t, claim.Lease)

	// Its peek taken, so running out is the only thing wrong with it.
	store := p.stills()
	require.True(t, store.takePeek(dave, "uid-a", claim.Lease, time.Now()))
	store.lock.Lock()
	store.entries[dave].lease.expires = time.Now().Add(-time.Second)
	store.lock.Unlock()

	response := uploadOverHTTP(t, p, dave, claim.Lease, cardStill(t), "uid-a")
	require.Equal(t, http.StatusForbidden, response.Code)
	require.Equal(t, "That capture was not yours to make.", errorOf(t, response))
	require.Nil(t, store.current(dave, true, time.Now()))
}

func TestOnlyAJPEGOfACardsShapeBecomesAStill(t *testing.T) {
	p := stillsPlugin(t)
	dave := p.streamHandle("videokey-dave")

	claim, _ := claimOverHTTP(t, p, dave, stillModeWatching, "uid-a")
	require.NotEmpty(t, claim.Lease)

	var pngStill bytes.Buffer
	require.NoError(t, png.Encode(&pngStill, image.NewRGBA(image.Rect(0, 0, 480, 270))))

	valid := cardStill(t)
	shapeProblem := "That picture is the wrong shape for a preview."

	for name, refused := range map[string]struct {
		body    []byte
		status  int
		problem string
	}{
		// Sent as image/jpeg like everything else here; the bytes decide.
		"a PNG":                  {pngStill.Bytes(), http.StatusBadRequest, "That is not a JPEG."},
		"html":                   {[]byte("<html><script>alert(1)</script></html>"), http.StatusBadRequest, "That is not a JPEG."},
		"a JPEG header and junk": {append([]byte("\xff\xd8\xff\xe0"), bytes.Repeat([]byte("x"), 400)...), http.StatusBadRequest, "That is not a JPEG."},
		"a square":               {stillJPEG(t, 300, 300), http.StatusBadRequest, shapeProblem},
		"a portrait":             {stillJPEG(t, 270, 480), http.StatusBadRequest, shapeProblem},
		"a strip, 3:1":           {stillJPEG(t, 1200, 400), http.StatusBadRequest, shapeProblem},
		"just under 6:5":         {stillJPEG(t, 239, 200), http.StatusBadRequest, shapeProblem},
		"just over 5:2":          {stillJPEG(t, 251, 100), http.StatusBadRequest, shapeProblem},
		"too small":              {stillJPEG(t, 128, 72), http.StatusBadRequest, shapeProblem},
		"too large":              {stillJPEG(t, 1600, 900), http.StatusBadRequest, shapeProblem},
		"too many bytes":         {append(append([]byte(nil), valid...), make([]byte, maxStillBytes)...), http.StatusRequestEntityTooLarge, "That still is too large; 200 KB is the limit."},
	} {
		response := uploadOverHTTP(t, p, dave, claim.Lease, refused.body, "uid-a")
		require.Equal(t, refused.status, response.Code, name)
		require.Equal(t, refused.problem, errorOf(t, response), name)
		require.Nil(t, p.stills().current(dave, true, time.Now()), name)
	}

	// Nothing refused spent the lease: the real one still goes up under it.
	require.Equal(t, http.StatusOK, uploadOverHTTP(t, p, dave, claim.Lease, valid, "uid-a").Code)

	// The largest size allowed is allowed, and so is the smallest; so are 4:3
	// and 21:9, and both ends of the shapes allowed, 6:5 and 5:2 exactly. The
	// last still is made stale in between, as a replacement rather than an
	// edit: a stored still is never changed in place.
	for _, size := range [][2]int{{1280, 720}, {160, 90}, {640, 480}, {1260, 540}, {240, 200}, {250, 100}} {
		store := p.stills()
		store.lock.Lock()
		old := store.entries[dave].picture
		store.entries[dave].picture = &stillPicture{data: old.data, version: old.version, at: old.at.Add(-stillMaxAge / 2)}
		store.lock.Unlock()

		again, _ := claimOverHTTP(t, p, dave, stillModeWatching, "uid-a")
		require.NotEmpty(t, again.Lease)
		response := uploadOverHTTP(t, p, dave, again.Lease, stillJPEG(t, size[0], size[1]), "uid-a")
		require.Equal(t, http.StatusOK, response.Code, "%v: %s", size, response.Body.String())
	}
}

func TestAlwaysOnAndOffAirStreamsAreNeverPreviewed(t *testing.T) {
	p := stillsPlugin(t)

	for _, mode := range []string{stillModeWatching, stillModePeek} {
		_, response := claimOverHTTP(t, p, p.streamHandle("videokey-watts"), mode, "uid-a")
		require.Equal(t, http.StatusNotFound, response.Code, "a 24/7 channel has a card picture instead")
		require.Equal(t, "That stream is not live right now.", errorOf(t, response))

		_, response = claimOverHTTP(t, p, p.streamHandle("videokey-gone"), mode, "uid-a")
		require.Equal(t, http.StatusNotFound, response.Code)
		require.Equal(t, "That stream is not live right now.", errorOf(t, response))
	}

	// An upload for either is refused before anything is read.
	response := uploadOverHTTP(t, p, p.streamHandle("videokey-watts"), "anything", cardStill(t), "uid-a")
	require.Equal(t, http.StatusNotFound, response.Code)

	_, response = claimOverHTTP(t, p, "", stillModeWatching, "uid-a")
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Equal(t, "Missing stream handle.", errorOf(t, response))

	_, response = claimOverHTTP(t, p, p.streamHandle("videokey-dave"), "record", "uid-a")
	require.Equal(t, http.StatusBadRequest, response.Code)

	response = stillRequest(t, p, http.MethodGet, "/api/v1/stills/claim?handle=x&mode=peek", nil, "uid-a")
	require.Equal(t, http.StatusMethodNotAllowed, response.Code)

	response = stillRequest(t, p, http.MethodDelete, "/api/v1/stills?handle=x", nil, "uid-a")
	require.Equal(t, http.StatusMethodNotAllowed, response.Code)

	// And nobody signed out gets anywhere.
	response = stillRequest(t, p, http.MethodPost, "/api/v1/stills/claim?handle=x&mode=peek", nil, "")
	require.Equal(t, http.StatusUnauthorized, response.Code)
}

func TestWithLivePreviewsOffThereAreNoStills(t *testing.T) {
	p := stillsPlugin(t)
	dave := p.streamHandle("videokey-dave")

	claim, _ := claimOverHTTP(t, p, dave, stillModeWatching, "uid-a")
	require.Equal(t, http.StatusOK, uploadOverHTTP(t, p, dave, claim.Lease, cardStill(t), "uid-a").Code)

	off := &configuration{AlwaysOnStreamKeys: "videokey-watts"}
	off.normalize()
	p.setConfiguration(off)

	_, response := claimOverHTTP(t, p, dave, stillModePeek, "uid-b")
	require.Equal(t, http.StatusNotFound, response.Code)
	require.Equal(t, "Live previews are off.", errorOf(t, response))

	response = uploadOverHTTP(t, p, dave, claim.Lease, cardStill(t), "uid-a")
	require.Equal(t, http.StatusNotFound, response.Code)
	require.Equal(t, "Live previews are off.", errorOf(t, response))

	response = stillRequest(t, p, http.MethodGet, "/api/v1/stills?handle="+dave, nil, "uid-b")
	require.Equal(t, http.StatusNotFound, response.Code, "what was taken while it was on is not served either")

	// The next poll forgets the lot.
	streams := p.decorate([]bbStream{rawLive("videokey-dave")}, off)
	require.Empty(t, streams[0].StillVersion)
	require.Nil(t, p.stills().current(dave, true, time.Now()))
}

// A still is revalidated every time, whatever version the address names: one
// kept for a day would be shown again without the access gate hearing of it.
// The ETag makes a re-display a 304, after the gate.
func TestAStillIsAlwaysRevalidated(t *testing.T) {
	p := stillsPlugin(t)
	dave := p.streamHandle("videokey-dave")

	response := stillRequest(t, p, http.MethodGet, "/api/v1/stills?handle="+dave, nil, "uid-b")
	require.Equal(t, http.StatusNotFound, response.Code, "nothing captured yet")

	still := cardStill(t)
	claim, _ := claimOverHTTP(t, p, dave, stillModeWatching, "uid-a")
	require.Equal(t, http.StatusOK, uploadOverHTTP(t, p, dave, claim.Lease, still, "uid-a").Code)

	response = stillRequest(t, p, http.MethodGet, "/api/v1/stills?handle="+dave+"&v="+pictureVersion(still), nil, "uid-b")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "image/jpeg", response.Header().Get("Content-Type"))
	require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "private, no-cache", response.Header().Get("Cache-Control"))
	require.Equal(t, still, response.Body.Bytes())

	// A panel a poll behind asks for the last version and gets this one.
	response = stillRequest(t, p, http.MethodGet, "/api/v1/stills?handle="+dave+"&v=000000000000", nil, "uid-b")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "private, no-cache", response.Header().Get("Cache-Control"))

	// Another stream's handle has nothing to show.
	response = stillRequest(t, p, http.MethodGet, "/api/v1/stills?handle="+p.streamHandle("videokey-bob"), nil, "uid-b")
	require.Equal(t, http.StatusNotFound, response.Code)
}

func TestStillsArePrunedAfterTheGraceAndTheirAge(t *testing.T) {
	put := func(s *stillStore, handle string, at time.Time) {
		t.Helper()

		lease := s.claim(handle, "uid-a", stillModeWatching, at).Lease
		require.NotEmpty(t, lease)
		_, stored := s.put(handle, "uid-a", lease, []byte("a still of "+handle), at)
		require.True(t, stored)
	}

	// Off the air: kept through the grace, gone after it.
	s := newStillStore()
	put(s, "h1", stillEpoch)
	s.sweep(stillEpoch.Add(stillOffAirGrace), true)
	require.NotNil(t, s.current("h1", false, stillEpoch.Add(stillOffAirGrace)), "a reconnect keeps its still")
	s.sweep(stillEpoch.Add(stillOffAirGrace+time.Second), true)
	require.Nil(t, s.current("h1", false, stillEpoch.Add(stillOffAirGrace+time.Second)))
	require.Empty(t, s.entries)
	require.Zero(t, s.bytes)

	// On the air but nobody has taken a new one: shown until it is too old,
	// then neither shown nor named in the snapshot.
	s = newStillStore()
	put(s, "h1", stillEpoch)
	for at := stillEpoch; at.Before(stillEpoch.Add(stillMaxAge)); at = at.Add(time.Minute) {
		require.NotNil(t, s.seen("h1", at))
		s.sweep(at, true)
	}
	late := stillEpoch.Add(stillMaxAge + time.Second)
	require.Nil(t, s.current("h1", true, late))
	require.Nil(t, s.seen("h1", late))
	s.sweep(late, true)
	require.Empty(t, s.entries)
	require.Zero(t, s.bytes)

	// A lease that ran out unused is remembered while the stream is on the
	// air, because that is what holds the next one back, and goes with the
	// off-air grace like everything else.
	s = newStillStore()
	s.claim("h1", "uid-a", stillModePeek, stillEpoch)
	s.sweep(stillEpoch.Add(stillLeaseTTL), true)
	require.Contains(t, s.entries, "h1")
	require.Empty(t, s.claim("h1", "uid-b", stillModePeek, stillEpoch.Add(stillLeaseTTL)).Lease)
	s.sweep(stillEpoch.Add(stillOffAirGrace+time.Second), true)
	require.Empty(t, s.entries)

	// Switched off: everything goes at once.
	s = newStillStore()
	put(s, "h1", stillEpoch)
	put(s, "h2", stillEpoch)
	s.sweep(stillEpoch.Add(time.Second), false)
	require.Empty(t, s.entries)
	require.Zero(t, s.bytes)
}

func TestTheStillStoreKeepsToItsSize(t *testing.T) {
	s := newStillStore()
	big := make([]byte, maxStillBytes)
	users := 0

	put := func(handle string, at time.Time) {
		users++
		userID := fmt.Sprintf("uid-%d", users)
		lease := s.claim(handle, userID, stillModeWatching, at).Lease
		require.NotEmpty(t, lease, handle)
		_, stored := s.put(handle, userID, lease, big, at)
		require.True(t, stored)
	}

	count := maxStillStoreBytes/maxStillBytes + 3
	for i := 0; i < count; i++ {
		put(fmt.Sprintf("h%03d", i), stillEpoch.Add(time.Duration(i)*time.Second))
	}

	require.LessOrEqual(t, s.bytes, maxStillStoreBytes)

	// The oldest went, the newest stayed.
	at := stillEpoch.Add(time.Duration(count) * time.Second)
	require.Nil(t, s.current("h000", true, at))
	require.NotNil(t, s.current(fmt.Sprintf("h%03d", count-1), true, at))
}

func TestTheSnapshotCarriesStillsOnlyWhenPreviewsAreOn(t *testing.T) {
	p := stillsPlugin(t)
	cfg := p.getConfiguration()
	dave := p.streamHandle("videokey-dave")

	raw := []bbStream{rawLive("videokey-dave"), rawLive("videokey-watts")}

	encode := func(value any) string {
		t.Helper()

		encoded, err := json.Marshal(value)
		require.NoError(t, err)

		return string(encoded)
	}

	// Nothing captured yet: the fields are absent, not empty.
	streams := p.decorate(raw, cfg)
	require.NotContains(t, encode(streams), "stillVersion")
	require.NotContains(t, encode(streams), "stillAt")

	still := cardStill(t)
	claim, _ := claimOverHTTP(t, p, dave, stillModeWatching, "uid-a")
	require.Equal(t, http.StatusOK, uploadOverHTTP(t, p, dave, claim.Lease, still, "uid-a").Code)

	streams = p.decorate(raw, cfg)
	byHandle := map[string]apiStream{}
	for _, stream := range streams {
		byHandle[stream.Handle] = stream
	}

	require.Equal(t, pictureVersion(still), byHandle[dave].StillVersion)
	taken, err := time.Parse(time.RFC3339, byHandle[dave].StillAt)
	require.NoError(t, err)
	require.WithinDuration(t, time.Now(), taken, time.Minute)

	channel := byHandle[p.streamHandle("videokey-watts")]
	require.Empty(t, channel.StillVersion, "a 24/7 channel is never previewed")
	require.Empty(t, channel.StillAt)

	// The flag itself: present when on, absent when off.
	require.Contains(t, encode(&snapshot{Streams: streams, LivePreviews: true}), `"livePreviews":true`)
	require.NotContains(t, encode(&snapshot{Streams: streams}), "livePreviews")

	// Off: gone from the streams as well.
	off := &configuration{AlwaysOnStreamKeys: "videokey-watts"}
	off.normalize()
	require.NotContains(t, encode(p.decorate(raw, off)), "still")
}

// The whole poll, against a Broadcast Box that answers /api/status: the flag
// goes out on the snapshot, the still rides on its stream, and a peek is not a
// viewer.
func TestAPollCarriesTheFlagTheStillsAndTheRealViewerCount(t *testing.T) {
	status := `[{"streamKey":"videokey-dave","videoTracks":[{"rid":"Video","bitrate":1000}],` +
		`"sessions":[{"id":"aaaaaaaa-0000-0000-0000-000000000001"},{"id":"aaaaaaaa-0000-0000-0000-000000000002"},{"id":"aaaaaaaa-0000-0000-0000-00000000beef"}]}]`

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, status)
	}))
	defer origin.Close()

	p := stillsPlugin(t)
	p.httpClient = origin.Client()

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("KVList", mock.Anything, mock.Anything).Return([]string{}, nil)
	api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return()

	for _, previews := range []bool{true, false} {
		cfg := &configuration{BroadcastBoxURL: origin.URL, LivePreviews: previews}
		cfg.normalize()
		p.setConfiguration(cfg)

		dave := p.streamHandle("videokey-dave")
		p.stills().rememberPeek(dave, "uid-z", "aaaaaaaa-0000-0000-0000-00000000beef", time.Now())
		if previews {
			claim := p.stills().claim(dave, "uid-a", stillModeWatching, time.Now())
			_, stored := p.stills().put(dave, "uid-a", claim.Lease, cardStill(t), time.Now())
			require.True(t, stored)
		}

		p.pollOnce(t.Context(), true)

		current := p.getSnapshot()
		require.Len(t, current.Streams, 1)
		require.Equal(t, 2, current.Streams[0].Viewers, "three sessions, one of them a peek")
		require.Equal(t, previews, current.LivePreviews)

		encoded, err := json.Marshal(current)
		require.NoError(t, err)
		if previews {
			require.Contains(t, string(encoded), `"livePreviews":true`)
			require.NotEmpty(t, current.Streams[0].StillVersion)
		} else {
			require.NotContains(t, string(encoded), "livePreviews")
			require.NotContains(t, string(encoded), "still")
		}
		require.NotContains(t, string(encoded), "videokey-dave")
	}
}

func TestPeeksAreNotCountedAsViewers(t *testing.T) {
	s := newStillStore()
	sessions := []bbSession{{ID: "peek-1"}, {ID: "viewer-1"}, {ID: "viewer-2"}}

	require.Equal(t, 3, s.viewers(sessions, stillEpoch))

	s.rememberPeek("h1", "uid-a", "peek-1", stillEpoch)
	require.Equal(t, 2, s.viewers(sessions, stillEpoch))
	require.Equal(t, 2, s.viewers(sessions, stillEpoch.Add(peekSessionMemory-time.Second)))

	// Not for ever: a peek lease cannot hide a real viewer past this.
	require.Equal(t, 3, s.viewers(sessions, stillEpoch.Add(peekSessionMemory)))
	s.sweep(stillEpoch.Add(peekSessionMemory), true)
	require.Empty(t, s.peeks)

	// And through decorate, which is where the count is made.
	p, _ := channelPicturePlugin(t)
	p.stills().rememberPeek(p.streamHandle("videokey-dave"), "uid-a", "cb74c661-4c6e-4f34-87dd-e6c03397d22d", time.Now())

	var raw []bbStream
	require.NoError(t, json.Unmarshal([]byte(liveStatusJSON), &raw))
	streams := p.decorate(raw, p.getConfiguration())
	require.Len(t, streams, 1)
	require.Equal(t, 1, streams[0].Viewers, "the capture is not an audience")
}

// A peek lease leaves its session out of the count, so used to watch, it hides
// a viewer. One person is therefore left out of one stream's count at most
// once per window: a panel never needs more than that, and somebody chaining
// peek leases is counted from the second one on.
func TestOnePersonIsLeftOutOfAStreamsCountOncePerWindow(t *testing.T) {
	s := newStillStore()
	sessions := []bbSession{{ID: "peek-1"}, {ID: "peek-2"}, {ID: "viewer"}}

	// A peek a moment into its lease, which then runs out with no still.
	first := s.claim("h1", "uid-a", stillModePeek, stillEpoch)
	peeked := stillEpoch.Add(15 * time.Second)
	require.True(t, s.takePeek("h1", "uid-a", first.Lease, peeked))
	s.rememberPeek("h1", "uid-a", "peek-1", peeked)
	require.Equal(t, 2, s.viewers(sessions, peeked))

	// Its session is left out for a minute, and not a moment longer.
	require.Equal(t, 2, s.viewers(sessions, peeked.Add(peekSessionMemory-time.Second)))
	require.Equal(t, 3, s.viewers(sessions, peeked.Add(peekSessionMemory)))

	// The next peek lease on that stream comes round, and the same person
	// takes it, inside the window: that session is counted. The lease is
	// spent on it all the same, so the still can still be uploaded under it.
	again := stillEpoch.Add(stillStaleAfter + stillWatcherHeadStart)
	second := s.claim("h1", "uid-a", stillModePeek, again)
	require.NotEmpty(t, second.Lease)
	require.False(t, s.takePeek("h1", "uid-a", second.Lease, again))
	require.False(t, s.takePeek("h1", "uid-a", second.Lease, again), "and it cannot be tried twice")
	require.True(t, s.holds("h1", "uid-a", second.Lease, again))
	_, stored := s.put("h1", "uid-a", second.Lease, []byte("a still"), again.Add(time.Second))
	require.True(t, stored)

	// Per stream: the same person's peek on another is left out as usual.
	other := s.claim("h2", "uid-a", stillModePeek, again)
	require.True(t, s.takePeek("h2", "uid-a", other.Lease, again))
	s.rememberPeek("h2", "uid-a", "peek-2", again)

	// The window passes, and the sweep forgets it, but not the other one.
	s.sweep(peeked.Add(peekRepeatWindow), true)
	require.NotContains(t, s.recentPeeks, peekerKey("uid-a", "h1"))
	require.Contains(t, s.recentPeeks, peekerKey("uid-a", "h2"))

	// And the next peek lease after that is left out again.
	third := again.Add(time.Second + stillStaleAfter + stillWatcherHeadStart)
	lease := s.claim("h1", "uid-a", stillModePeek, third)
	require.NotEmpty(t, lease.Lease)
	require.True(t, s.takePeek("h1", "uid-a", lease.Lease, third))
}

func TestThePeekSessionListIsBounded(t *testing.T) {
	s := newStillStore()

	for i := 0; i < maxPeekSessions+10; i++ {
		s.rememberPeek("h1", fmt.Sprintf("uid-%d", i), fmt.Sprintf("peek-%d", i), stillEpoch.Add(time.Duration(i)*time.Millisecond))
	}

	require.Len(t, s.peeks, maxPeekSessions)
	require.NotContains(t, s.peeks, "peek-0", "the one nearest to running out made room")
	require.Contains(t, s.peeks, fmt.Sprintf("peek-%d", maxPeekSessions+9))

	// And so is the list of who has just had one.
	require.Len(t, s.recentPeeks, maxPeekSessions)
	require.NotContains(t, s.recentPeeks, peekerKey("uid-0", "h1"))
	require.Contains(t, s.recentPeeks, peekerKey(fmt.Sprintf("uid-%d", maxPeekSessions+9), "h1"))
}

func TestWHEPSessionIDsAreReadFromTheLocation(t *testing.T) {
	require.Equal(t, "cb74c661-4c6e-4f34-87dd-e6c03397d22d", whepSessionID("/api/whep/cb74c661-4c6e-4f34-87dd-e6c03397d22d"))
	require.Equal(t, "cb74c661-4c6e-4f34-87dd-e6c03397d22d", whepSessionID("https://tv.example/bb/api/whep/cb74c661-4c6e-4f34-87dd-e6c03397d22d"))

	for _, junk := range []string{"", "/api/whep/", "/api/whep", "/api/whep/abc", "/api/whep/<script>alert(1)</script>", "/api/whep/" + strings.Repeat("a", 65), "%zz"} {
		require.Empty(t, whepSessionID(junk), junk)
	}
}

// A Broadcast Box that answers WHEP the way v2.0.2 does: 201, the answer, and
// the session in a Location header, plus the Link headers nothing should pass on.
func fakeWHEPOrigin(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()

	var lock sync.Mutex
	var issued []string

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/whep" || r.Header.Get("Authorization") != "Bearer videokey-dave" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}

		lock.Lock()
		id := fmt.Sprintf("bbbbbbbb-0000-0000-0000-%012d", len(issued)+1)
		issued = append(issued, id)
		lock.Unlock()

		w.Header().Add("Link", `</api/sse/`+id+`>; rel="urn:ietf:params:whep:ext:core:server-sent-events"; events="layers"`)
		w.Header().Add("Location", "/api/whep/"+id)
		w.Header().Set("Content-Type", "application/sdp")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "v=0\r\na=candidate:1 1 udp 2130706431 203.0.113.7 3478 typ host\r\n")
	}))

	return origin, func() []string {
		lock.Lock()
		defer lock.Unlock()

		return append([]string(nil), issued...)
	}
}

func whepRequest(t *testing.T, p *Plugin, handle, peek, userID string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/api/v1/whep", strings.NewReader(viewerOffer))
	request.Header.Set("X-Stream-Handle", handle)
	if peek != "" {
		request.Header.Set("X-Matty-Peek", peek)
	}
	recorder := httptest.NewRecorder()

	// Straight to the handler: the route's rate limiter is made in OnActivate.
	p.handleWHEP(recorder, request, userID)

	return recorder
}

func TestOnlyAValidPeekLeaseKeepsItsSessionOffTheViewerCount(t *testing.T) {
	origin, issued := fakeWHEPOrigin(t)
	defer origin.Close()

	p := stillsPlugin(t)
	p.httpClient = origin.Client()
	cfg := &configuration{BroadcastBoxURL: origin.URL, AlwaysOnStreamKeys: "videokey-watts", LivePreviews: true}
	cfg.normalize()
	p.setConfiguration(cfg)

	dave := p.streamHandle("videokey-dave")
	store := p.stills()

	counted := func() int {
		t.Helper()

		sessions := []bbSession{}
		for _, id := range issued() {
			sessions = append(sessions, bbSession{ID: id})
		}

		return store.viewers(sessions, time.Now())
	}

	// Each case below needs a fresh lease on the same stream, which the
	// last one's attempt would otherwise hold back for minutes.
	release := func() {
		t.Helper()

		store.lock.Lock()
		defer store.lock.Unlock()

		entry := store.entries[dave]
		entry.lease, entry.attempted, entry.misses = nil, time.Time{}, 0
	}

	// An ordinary viewer.
	response := whepRequest(t, p, dave, "", "uid-a")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, 1, counted())

	// A peek, under a peek lease of the asker's own: answered like any other,
	// and left out of the count.
	peek := store.claim(dave, "uid-b", stillModePeek, time.Now())
	require.NotEmpty(t, peek.Lease)

	response = whepRequest(t, p, dave, peek.Lease, "uid-b")
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), "a=candidate:")
	require.Equal(t, "application/sdp", response.Header().Get("Content-Type"))
	require.Empty(t, response.Header().Get("Location"), "Broadcast Box's session address stays on the server")
	require.Empty(t, response.Header().Values("Link"))
	require.Equal(t, 1, counted())

	// The same lease again: that is a second session, and it counts.
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, peek.Lease, "uid-b").Code)
	require.Equal(t, 2, counted())

	// A new peek lease for the same person on the same stream, inside the
	// window: counted this time, since somebody taking one peek after
	// another is watching. The lease is spent on it all the same, so the
	// still it was for can still be uploaded under it.
	release()
	again := store.claim(dave, "uid-b", stillModePeek, time.Now())
	require.NotEmpty(t, again.Lease)
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, again.Lease, "uid-b").Code)
	require.Equal(t, 3, counted())
	require.True(t, store.holds(dave, "uid-b", again.Lease, time.Now()))

	// Somebody else's peek lease.
	release()
	borrowed := store.claim(dave, "uid-b", stillModePeek, time.Now())
	require.NotEmpty(t, borrowed.Lease)
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, borrowed.Lease, "uid-a").Code)
	require.Equal(t, 4, counted())

	// A lease for a frame from a tile: that browser IS watching.
	release()
	watching := store.claim(dave, "uid-a", stillModeWatching, time.Now())
	require.NotEmpty(t, watching.Lease)
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, watching.Lease, "uid-a").Code)
	require.Equal(t, 5, counted())

	// A lease on another stream.
	bob := store.claim(p.streamHandle("videokey-bob"), "uid-c", stillModePeek, time.Now())
	require.NotEmpty(t, bob.Lease)
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, bob.Lease, "uid-c").Code)
	require.Equal(t, 6, counted())

	// A peek lease that has run out.
	release()
	expired := store.claim(dave, "uid-c", stillModePeek, time.Now())
	require.NotEmpty(t, expired.Lease)
	store.lock.Lock()
	store.entries[dave].lease.expires = time.Now().Add(-time.Second)
	store.lock.Unlock()
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, expired.Lease, "uid-c").Code)
	require.Equal(t, 7, counted())

	// Live previews off: no peeks at all.
	release()
	late := store.claim(dave, "uid-c", stillModePeek, time.Now())
	require.NotEmpty(t, late.Lease)
	off := cfg.Clone()
	off.LivePreviews = false
	p.setConfiguration(off)
	require.Equal(t, http.StatusOK, whepRequest(t, p, dave, late.Lease, "uid-c").Code)
	require.Equal(t, 8, counted())

	require.Len(t, issued(), 9)
}

func TestNoStreamKeyReachesAnyStillsAnswer(t *testing.T) {
	p := stillsPlugin(t)
	dave := p.streamHandle("videokey-dave")
	keys := []string{"videokey-dave", "videokey-bob", "videokey-erin", "videokey-watts"}

	var bodies []string
	record := func(response *httptest.ResponseRecorder) {
		bodies = append(bodies, response.Body.String())
	}

	granted, response := claimOverHTTP(t, p, dave, stillModeWatching, "uid-a")
	record(response)
	_, response = claimOverHTTP(t, p, dave, stillModeWatching, "uid-b")
	record(response)
	_, response = claimOverHTTP(t, p, p.streamHandle("videokey-watts"), stillModeWatching, "uid-b")
	record(response)
	record(uploadOverHTTP(t, p, dave, "wrong", cardStill(t), "uid-a"))
	record(uploadOverHTTP(t, p, dave, granted.Lease, []byte("nope"), "uid-a"))
	record(uploadOverHTTP(t, p, dave, granted.Lease, cardStill(t), "uid-a"))
	record(stillRequest(t, p, http.MethodGet, "/api/v1/stills?handle="+p.streamHandle("videokey-bob"), nil, "uid-b"))

	cfg := p.getConfiguration()
	current := &snapshot{Streams: p.decorate([]bbStream{rawLive("videokey-dave"), rawLive("videokey-watts")}, cfg), LivePreviews: true}
	require.NotEmpty(t, current.Streams[0].StillVersion)
	encoded, err := json.Marshal(current)
	require.NoError(t, err)
	bodies = append(bodies, string(encoded))

	for _, body := range bodies {
		for _, key := range keys {
			require.NotContains(t, body, key)
		}
	}
}

func TestAStillOffTheListIsNotServedPastTheGraceEvenWithoutASweep(t *testing.T) {
	s := newStillStore()
	lease := s.claim("h1", "uid-a", stillModeWatching, stillEpoch).Lease
	_, stored := s.put("h1", "uid-a", lease, []byte("a still"), stillEpoch)
	require.True(t, stored)

	// Nothing sweeps here, as nothing does while Broadcast Box cannot be
	// reached or the plugin is not configured.
	require.NotNil(t, s.current("h1", false, stillEpoch.Add(stillOffAirGrace)))
	require.Nil(t, s.current("h1", false, stillEpoch.Add(stillOffAirGrace+time.Second)))

	// Still on the list, though, it is shown to its age whatever the grace:
	// the poll interval can be longer than the grace, and a stream on every
	// card must not lose its picture between two polls.
	require.NotNil(t, s.current("h1", true, stillEpoch.Add(stillMaxAge)))
	require.Nil(t, s.current("h1", true, stillEpoch.Add(stillMaxAge+time.Second)))
}

func TestAStillIsServedThroughTheGraceAndNotAfter(t *testing.T) {
	p := stillsPlugin(t)
	dave := p.streamHandle("videokey-dave")
	still := cardStill(t)

	claim, _ := claimOverHTTP(t, p, dave, stillModeWatching, "uid-a")
	require.Equal(t, http.StatusOK, uploadOverHTTP(t, p, dave, claim.Lease, still, "uid-a").Code)

	serve := func() int {
		t.Helper()

		return stillRequest(t, p, http.MethodGet, "/api/v1/stills?handle="+dave+"&v="+pictureVersion(still), nil, "uid-b").Code
	}

	// Last seen on the air longer ago than the grace — a poll interval set
	// longer than it — but still on the list: served.
	backdate := func(by time.Duration) {
		t.Helper()

		store := p.stills()
		store.lock.Lock()
		store.entries[dave].lastLive = time.Now().Add(-by)
		store.lock.Unlock()
	}
	backdate(stillOffAirGrace + time.Minute)
	require.Equal(t, http.StatusOK, serve())

	// Off the list, as a failed origin or a stream that ended leaves it. A
	// new snapshot rather than an edit: the published one is shared.
	backdate(0)
	current := p.getSnapshot()
	next := *current
	next.Streams = nil
	for _, stream := range current.Streams {
		if stream.Handle != dave {
			next.Streams = append(next.Streams, stream)
		}
	}
	p.setSnapshot(&next)

	require.Equal(t, http.StatusOK, serve(), "a card fading out keeps its picture")

	// Past the grace: not served, with no sweep having run.
	backdate(stillOffAirGrace + time.Second)
	require.Equal(t, http.StatusNotFound, serve())

	// And the next sweep forgets it altogether.
	p.stills().sweep(time.Now(), true)
	p.stills().lock.Lock()
	require.Empty(t, p.stills().entries)
	p.stills().lock.Unlock()
}

func TestClaimsAndUploadsAreRateLimitedPerPerson(t *testing.T) {
	p := stillsPlugin(t)
	dave := p.streamHandle("videokey-dave")

	// Until refused, then by how many. A bucket refills as it is drained, so
	// a slow machine may be allowed one or two more than the burst; never
	// fewer, and never without end.
	untilRefused := func(send func() *httptest.ResponseRecorder, burst int, sentence string) {
		t.Helper()

		for allowed := 0; allowed <= 2*burst; allowed++ {
			response := send()
			if response.Code == http.StatusTooManyRequests {
				require.GreaterOrEqual(t, allowed, burst)
				require.Equal(t, sentence, errorOf(t, response))

				return
			}
		}

		t.Fatalf("never refused after %d requests", 2*burst)
	}

	untilRefused(func() *httptest.ResponseRecorder {
		_, response := claimOverHTTP(t, p, dave, stillModeWatching, "uid-a")

		return response
	}, stillClaimsPerMinute, "Too many preview requests. Wait a moment and try again.")

	untilRefused(func() *httptest.ResponseRecorder {
		return uploadOverHTTP(t, p, dave, "not-a-lease", nil, "uid-a")
	}, stillUploadsPerMinute, "Too many previews at once. Wait a moment and try again.")

	// Per person: somebody else is not held up by it.
	_, response := claimOverHTTP(t, p, dave, stillModeWatching, "uid-b")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, http.StatusForbidden, uploadOverHTTP(t, p, dave, "not-a-lease", nil, "uid-b").Code)
}

func TestAPeekThatOpensNoSessionHidesNothing(t *testing.T) {
	for name, origin := range map[string]http.HandlerFunc{
		// Broadcast Box refused the offer: there is no session to hide.
		"refused": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "bad offer", http.StatusBadRequest)
		},

		// Answered, but with no Location to read the session from: the
		// count goes uncorrected, and the peek still plays.
		"no location": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/sdp")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, "v=0\r\na=candidate:1 1 udp 2130706431 203.0.113.7 3478 typ host\r\n")
		},
	} {
		server := httptest.NewServer(origin)

		p := stillsPlugin(t)
		p.httpClient = server.Client()
		cfg := &configuration{BroadcastBoxURL: server.URL, AlwaysOnStreamKeys: "videokey-watts", LivePreviews: true}
		cfg.normalize()
		p.setConfiguration(cfg)

		dave := p.streamHandle("videokey-dave")
		store := p.stills()
		peek := store.claim(dave, "uid-b", stillModePeek, time.Now())
		require.NotEmpty(t, peek.Lease, name)

		response := whepRequest(t, p, dave, peek.Lease, "uid-b")
		if name == "refused" {
			require.Equal(t, http.StatusBadGateway, response.Code, name)
		} else {
			require.Equal(t, http.StatusOK, response.Code, name)
		}

		store.lock.Lock()
		require.Empty(t, store.peeks, name)
		require.Empty(t, store.recentPeeks, name, "nobody was left out, so nobody is held to the window")
		store.lock.Unlock()

		server.Close()
	}
}

func TestTheSDPDebugLogNeverNamesTheStreamKey(t *testing.T) {
	origin, _ := fakeWHEPOrigin(t)
	defer origin.Close()

	p := stillsPlugin(t)
	p.httpClient = origin.Client()
	cfg := &configuration{BroadcastBoxURL: origin.URL, AlwaysOnStreamKeys: "videokey-watts", LivePreviews: true, DebugLogSDP: true}
	cfg.normalize()
	p.setConfiguration(cfg)

	require.Equal(t, http.StatusOK, whepRequest(t, p, p.streamHandle("videokey-dave"), "", "uid-a").Code)

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	logged := false
	for _, call := range api.Calls {
		if !strings.HasPrefix(call.Method, "Log") {
			continue
		}
		if len(call.Arguments) > 0 && call.Arguments[0] == "WHEP exchange" {
			logged = true
		}
		for _, argument := range call.Arguments {
			require.NotContains(t, fmt.Sprint(argument), "videokey-dave", call.Method)
		}
	}
	require.True(t, logged, "the setting is on, so the exchange is logged")
}
