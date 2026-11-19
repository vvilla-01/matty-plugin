package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"
)

// The recorder manager with real recorders (spec §2.19 items 32 and 33 run
// through it), in real time, over loopback, against the stand-in for
// Broadcast Box of clips_recorder_e2e_test.go: the plugin is set up as it
// would be, Clips started on a local disk, and the manager driven as the
// poller drives it. Only with VILLANET_E2E=1 (make e2e).
func TestE2EManagerRecordsAStreamAndHoldsItsEnd(t *testing.T) {
	requireE2E(t)
	origin := newE2EOrigin(t)

	w := newClipsWorld(t, "uid-bob")
	w.person("uid-e2e")
	w.link("uid-e2e", e2eStreamKey)
	clipsAdmins(w.api)
	h := startClipsIn(t, w.p, w.api, model.ImageDriverLocal)
	w.p.clips().now = time.Now
	w.p.httpClient = origin.srv.Client()

	// "Log WHEP negotiation" on, so that every answer is logged in full and
	// the log can be looked at for the key.
	cfg := w.p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = origin.srv.URL
	cfg.OfflineGraceChecks = 1
	cfg.DebugLogSDP = true
	w.p.setConfiguration(cfg)

	// Loopback is let in here and nowhere else.
	m := w.p.recorders()
	m.peers.build = func(allowed []string) (*webrtc.API, error) {
		require.Equal(t, []string{"127.0.0.1"}, allowed)
		return newClipWebRTCAPI(allowed, true)
	}

	handle := w.p.streamHandle(e2eStreamKey)
	stream := apiStream{
		StreamKey: e2eStreamKey, Handle: handle, Title: "E2e", UserID: "uid-e2e", Username: "e2e", LinkSource: linkSourceManual,
		Viewers: 1, Bitrate: 100_000, AudioTracks: 1, VideoTracks: 1,
		NowPlaying: &apiNowPlaying{Category: categoryGame, Title: "Hytale"},
	}
	poll := func(streams ...apiStream) *snapshot {
		t.Helper()

		now := time.Now()
		m.reconcile(streams, w.p.getConfiguration(), now, true)
		current := &snapshot{Streams: append([]apiStream{}, streams...), Configured: true}
		w.p.addClips(current, w.p.getConfiguration(), now)
		w.p.setSnapshot(current)

		return current
	}

	began := time.Now()
	require.Equal(t, clipStateStarting, cardFor(t, poll(stream).Streams, handle).Clip)
	require.Eventually(t, func() bool {
		return m.streamViews(time.Now())[handle].state == clipStateReady
	}, e2eWithin(5*time.Second), 20*time.Millisecond, "the stream never became ready")
	t.Logf("ready after %s", time.Since(began))

	card := cardFor(t, poll(stream).Streams, handle)
	require.Equal(t, clipStateReady, card.Clip)
	require.WithinDuration(t, time.Now(), time.UnixMilli(card.ClipFrom), 5*time.Second)

	// The session the recorder opened is nobody watching.
	sessions := origin.sessions()
	require.Len(t, sessions, 1)
	raw := rawWatched(e2eStreamKey, sessions[0])
	require.Zero(t, w.p.decorate([]bbStream{raw}, w.p.getConfiguration())[0].Viewers)

	// Bob, watching, clips it once seven seconds are held.
	w.p.noteClipViewer("sess-bob", "uid-bob", handle, time.Now())
	require.Eventually(t, func() bool {
		rows := m.adminRows(time.Now())
		return len(rows) == 1 && rows[0].HeldSeconds >= 7
	}, e2eWithin(12*time.Second), 50*time.Millisecond, "the ring did not fill")
	cut := func(source string) *httptest.ResponseRecorder {
		return w.call(t, "uid-bob", clipCall{method: http.MethodPost, target: clipsPath + "/drafts", body: `{"handle":"` + handle + `","source":"` + source + `"}`})
	}
	live := decodeDraft(t, cut(clipSourceLive), http.StatusCreated)
	require.GreaterOrEqual(t, live.DurationMs, int64(7000))
	require.True(t, live.Partial)
	require.Equal(t, &clipTag{Category: categoryGame, Title: "Hytale"}, live.Tag)

	// The stream goes off: the recorder stops, keeping its end as a tail,
	// which Bob clips too.
	snap := poll()
	require.Len(t, snap.ClipTails, 1)
	require.Equal(t, clipTailEnded, snap.ClipTails[0].Reason)
	require.GreaterOrEqual(t, snap.ClipTails[0].HeldMs, int64(7000))
	tail := decodeDraft(t, cut(clipSourceTail), http.StatusCreated)
	require.Equal(t, clipTailEnded, tail.TailReason)
	require.Equal(t, &clipTag{Category: categoryGame, Title: "Hytale"}, tail.Tag)

	// Stopping the plugin closes everything, and leaves nothing in ring/.
	w.p.stopClips()
	require.Empty(t, namesIn(t, filepath.Join(h.root, clipsRingDir)))

	for _, call := range w.api.Calls {
		if strings.HasPrefix(call.Method, "Log") {
			require.NotContains(t, strings.ToLower(strings.Join(stringsOf(call.Arguments), " ")), strings.ToLower(e2eStreamKey))
		}
	}
}
