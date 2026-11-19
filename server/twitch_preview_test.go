package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A Twitch card's picture, fetched by the server and relayed
// (design/twitch-live.md §5.7).

// previewScene has Dave live on Twitch, in the published snapshot.
func previewScene(t *testing.T) (*twitchScene, apiStream) {
	t.Helper()

	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twDave: daveTwitch})
	s.fake.goLive(daveTwitch, "Live", "Chess", 4, s.now().Add(-time.Minute))
	s.step(t)

	streams := s.p.mergeStreams(nil, s.p.twitchStreams(s.p.getConfiguration(), s.now()), true)
	s.p.setSnapshot(&snapshot{Streams: streams})
	require.Len(t, streams, 1)
	require.NotEmpty(t, streams[0].PreviewVersion)

	return s, streams[0]
}

func (s *twitchScene) preview(handle, version string) *httptest.ResponseRecorder {
	return s.serve(http.MethodGet, twitchPreviewRoute+"?handle="+handle+"&v="+version, twErin, web(twErin), "", nil)
}

func previewFetches(s *twitchScene) int {
	return s.fake.requestsWhere(func(seen seenRequest) bool { return strings.HasPrefix(seen.path, "/previews-ttv/") })
}

func TestTwitchPreviewTemplatesAreChecked(t *testing.T) {
	newMediaFake(t)
	host := twitchImageHost

	for _, tried := range []struct {
		template string
		ok       bool
	}{
		{"https://" + host + "/previews-ttv/live_user_dave_tv-{width}x{height}.jpg", true},
		{"http://" + host + "/previews-ttv/live_user_dave_tv-{width}x{height}.jpg", false},
		{"https://elsewhere.example/previews-ttv/live_user_dave_tv-{width}x{height}.jpg", false},
		{"https://" + host + "/ttv-static/live_user_dave_tv-{width}x{height}.jpg", false},
		{"https://" + host + "/previews-ttv/live_user_dave_tv-640x360.jpg", false},
		{"https://" + host + "/previews-ttv/{width}x{height}-{width}x{height}.jpg", false},
		{"https://user@" + host + "/previews-ttv/a-{width}x{height}.jpg", false},
		{"https://" + host + "/previews-ttv/a-{width}x{height}.jpg?redirect=elsewhere", false},
		{"", false},
	} {
		require.Equal(t, tried.ok, twitchThumbOK(tried.template), tried.template)
	}
}

func TestATwitchPreviewIsRelayed(t *testing.T) {
	s, dave := previewScene(t)

	response := s.preview(dave.Handle, dave.PreviewVersion)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, jpegBytes, response.Body.Bytes())
	require.Equal(t, "image/jpeg", response.Header().Get("Content-Type"))
	require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "private, max-age=300", response.Header().Get("Cache-Control"), "the current version may be kept")

	stale := s.preview(dave.Handle, "0-0")
	require.Equal(t, http.StatusOK, stale.Code)
	require.Equal(t, "private, no-cache", stale.Header().Get("Cache-Control"), "an old version is asked about again")

	fetched := s.fake.requests()
	for _, seen := range fetched {
		if strings.HasPrefix(seen.path, "/previews-ttv/") {
			require.Equal(t, "/previews-ttv/live_user_dave_tv-640x360.jpg", seen.path)
		}
	}
	require.Equal(t, 1, previewFetches(s), "held, so fetched once")
}

func TestATwitchPreviewIsServedOnlyForAListedTwitchStream(t *testing.T) {
	s, dave := previewScene(t)

	bb := apiStream{StreamKey: "videokey-bob", Handle: s.p.streamHandle("videokey-bob"), Title: "Bob"}
	s.p.setSnapshot(&snapshot{Streams: []apiStream{dave, bb}})

	for _, handle := range []string{"nothing", strings.Repeat("a", 32), bb.Handle} {
		response := s.preview(handle, "")
		require.Equal(t, http.StatusNotFound, response.Code, handle)
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	}

	cfg := s.p.getConfiguration().Clone()
	cfg.LivePreviews = false
	s.p.setConfiguration(cfg)
	require.Equal(t, http.StatusNotFound, s.preview(dave.Handle, dave.PreviewVersion).Code, "Live preview pictures covers Twitch's")

	cfg = cfg.Clone()
	cfg.LivePreviews = true
	cfg.EnableTwitchLive = false
	s.p.setConfiguration(cfg)
	require.Equal(t, http.StatusNotFound, s.preview(dave.Handle, dave.PreviewVersion).Code)

	require.Zero(t, previewFetches(s))

	// Locked people see no pictures at all.
	locked := s.serve(http.MethodGet, twitchPreviewRoute+"?handle="+dave.Handle, twDan, web(twDan), "", nil)
	require.Equal(t, http.StatusForbidden, locked.Code)
}

func TestATwitchPreviewThatIsNotAPictureIsNoPicture(t *testing.T) {
	for _, tried := range []struct {
		name  string
		serve http.HandlerFunc
	}{
		{"an offline channel's placeholder", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/ttv-static/404_preview-640x360.jpg", http.StatusFound)
		}},
		{"too large", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(append(append([]byte(nil), jpegBytes...), bytes.Repeat([]byte("x"), maxTwitchPreviewBytes)...))
		}},
		{"not a picture", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("<html>surprise</html>"))
		}},
		{"a picture of a kind it does not relay", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("GIF89a" + strings.Repeat("x", 32)))
		}},
		{"an error", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}},
	} {
		t.Run(tried.name, func(t *testing.T) {
			s, dave := previewScene(t)
			s.fake.route("/previews-ttv/live_user_dave_tv-640x360.jpg", tried.serve)
			s.fake.route("/ttv-static/404_preview-640x360.jpg", func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(jpegBytes)
			})

			response := s.preview(dave.Handle, dave.PreviewVersion)
			require.Equal(t, http.StatusNotFound, response.Code)
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		})
	}
}

func TestATwitchPreviewIsHeldTwoMinutesAndDroppedWhenTheStreamEnds(t *testing.T) {
	s, dave := previewScene(t)

	require.Equal(t, http.StatusOK, s.preview(dave.Handle, dave.PreviewVersion).Code)
	s.clock.advance(twitchPreviewHold - time.Second)
	require.Equal(t, http.StatusOK, s.preview(dave.Handle, dave.PreviewVersion).Code)
	require.Equal(t, 1, previewFetches(s))

	s.clock.advance(time.Second)
	require.Equal(t, http.StatusOK, s.preview(dave.Handle, dave.PreviewVersion).Code)
	require.Equal(t, 2, previewFetches(s), "never older than two minutes")

	// The stream ends: two good reads without it.
	s.fake.goOffline(daveTwitch.ID)
	for i := 0; i < 2; i++ {
		s.clock.advance(time.Minute)
		s.step(t)
	}

	live := s.p.twitchLive()
	live.lock.Lock()
	require.Empty(t, live.pictures)
	live.lock.Unlock()
	require.Equal(t, http.StatusNotFound, s.preview(dave.Handle, dave.PreviewVersion).Code)
}

func TestTheTwitchPreviewLimiterCountsOnlyMisses(t *testing.T) {
	s, dave := previewScene(t)
	s.p.twitchLive().previewLimiter = newRateLimiter(2, time.Hour)

	require.Equal(t, http.StatusOK, s.preview(dave.Handle, dave.PreviewVersion).Code)
	for i := 0; i < 5; i++ {
		require.Equal(t, http.StatusOK, s.preview(dave.Handle, dave.PreviewVersion).Code, "a picture held costs nothing")
	}

	s.clock.advance(twitchPreviewHold)
	require.Equal(t, http.StatusOK, s.preview(dave.Handle, dave.PreviewVersion).Code)

	s.clock.advance(twitchPreviewHold)
	tooMany := s.preview(dave.Handle, dave.PreviewVersion)
	require.Equal(t, http.StatusTooManyRequests, tooMany.Code)
	require.Equal(t, 2, previewFetches(s))
}
