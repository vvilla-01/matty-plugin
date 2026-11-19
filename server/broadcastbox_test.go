package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/require"
)

// bbLikeAnswer is an answer to one of the recorder's offers as Broadcast Box
// v2.0.2 makes one: a pion answerer with its H.264 and Opus, whose tracks
// are named as it names them, by the stream key (codecs.GetDefaultTracks).
// Nothing is sent anywhere: no candidates are gathered, and no answer is
// applied.
func bbLikeAnswer(t *testing.T, streamKey string) (answer string, recorder *webrtc.PeerConnection) {
	t.Helper()

	bb, err := e2eAPI(t).NewPeerConnection(webrtc.Configuration{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = bb.Close() })
	audio, err := webrtc.NewTrackLocalStaticRTP(e2eAudioCapability(), "audio", streamKey)
	require.NoError(t, err)
	video, err := webrtc.NewTrackLocalStaticRTP(e2eVideoCapability(), "video", streamKey)
	require.NoError(t, err)
	for _, track := range []webrtc.TrackLocal{audio, video} {
		_, err := bb.AddTrack(track)
		require.NoError(t, err)
	}

	api, err := newClipWebRTCAPI([]string{"192.168.1.20"}, false)
	require.NoError(t, err)
	recorder, err = api.NewPeerConnection(webrtc.Configuration{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = recorder.Close() })
	for _, kind := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio} {
		_, err := recorder.AddTransceiverFromKind(kind, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly})
		require.NoError(t, err)
	}
	offer, err := recorder.CreateOffer(nil)
	require.NoError(t, err)
	require.NoError(t, recorder.SetLocalDescription(offer))
	require.NoError(t, bb.SetRemoteDescription(offer))
	made, err := bb.CreateAnswer(nil)
	require.NoError(t, err)

	return made.SDP, recorder
}

// Broadcast Box names every playback session's media stream after the stream
// key, and pion writes that into the answer four ways. What VillaNET keeps of
// an answer names it only as VillaNET does, the same for picture and sound,
// and nothing else in it changes: it still answers the offer it was made for.
func TestWHEPAnswersNameNoStreamKey(t *testing.T) {
	const key = "videokey-dave-SECRETKEY"
	answer, recorder := bbLikeAnswer(t, key)
	require.Contains(t, answer, "a=msid:"+key+" video", "Broadcast Box's answer carries the key")
	require.Contains(t, answer, "cname:"+key)

	kept := withoutStreamIDs(answer)
	require.NotContains(t, strings.ToLower(kept), strings.ToLower(key))
	require.Equal(t, strings.Count(answer, "\r\n"), strings.Count(kept, "\r\n"), "line for line, endings kept")
	before, after := strings.Split(answer, "\r\n"), strings.Split(kept, "\r\n")
	for i := range before {
		if before[i] != after[i] {
			require.True(t, strings.HasPrefix(before[i], "a=msid:") || strings.HasPrefix(before[i], "a=ssrc:"), before[i])
			require.Equal(t, before[i], strings.ReplaceAll(after[i], answerStreamID, key))
		}
	}

	parsed, err := (&webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: kept}).Unmarshal()
	require.NoError(t, err)
	require.Len(t, parsed.MediaDescriptions, 2)
	for _, media := range parsed.MediaDescriptions {
		msid, ok := media.Attribute("msid")
		require.True(t, ok)
		require.Equal(t, answerStreamID+" "+media.MediaName.Media, msid, "one stream for both, so they stay in sync")
	}
	require.NoError(t, recorder.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: kept}))
}

func TestOnlyStreamIDsAreTakenOutOfAnAnswer(t *testing.T) {
	for _, c := range []struct{ line, kept string }{
		{"a=msid:KEY video", "a=msid:stream video"},
		{"a=msid:KEY", "a=msid:stream"},
		{"a=ssrc:1680383782 cname:KEY", "a=ssrc:1680383782 cname:stream"},
		{"a=ssrc:1680383782 msid:KEY audio", "a=ssrc:1680383782 msid:stream audio"},
		{"a=ssrc:1680383782 mslabel:KEY", "a=ssrc:1680383782 mslabel:stream"},
		// The track's own label, no stream at all, and everything else stay.
		{"a=ssrc:1680383782 label:video", "a=ssrc:1680383782 label:video"},
		{"a=msid:- video", "a=msid:- video"},
		{"a=msid-semantic:WMS *", "a=msid-semantic:WMS *"},
		{"a=ssrc-group:FID 1 2", "a=ssrc-group:FID 1 2"},
		{"a=ssrc:1680383782", "a=ssrc:1680383782"},
		{"a=candidate:1 1 udp 2130706431 192.168.1.20 8443 typ host", "a=candidate:1 1 udp 2130706431 192.168.1.20 8443 typ host"},
		{"a=mid:0", "a=mid:0"},
	} {
		require.Equal(t, c.kept, withoutStreamIDs(c.line), c.line)
		require.Equal(t, c.kept+"\r\n", withoutStreamIDs(c.line+"\r\n"), c.line)
		require.Equal(t, c.kept+"\n", withoutStreamIDs(c.line+"\n"), c.line)
	}
	require.Empty(t, withoutStreamIDs(""))
}

// The answer a viewer's browser is handed, and the one logged with "Log WHEP
// negotiation" on, never carry the stream key, though Broadcast Box's does:
// handles exist so that keys never reach a browser.
func TestTheWHEPAnswerABrowserGetsNamesNoStreamKey(t *testing.T) {
	answer, _ := bbLikeAnswer(t, "videokey-dave")
	require.Contains(t, answer, "videokey-dave")

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/whep" || r.Header.Get("Authorization") != "Bearer videokey-dave" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		w.Header().Add("Location", "/api/whep/bbbbbbbb-0000-0000-0000-000000000001")
		w.Header().Set("Content-Type", "application/sdp")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, answer)
	}))
	defer origin.Close()

	p := stillsPlugin(t)
	p.httpClient = origin.Client()
	cfg := &configuration{BroadcastBoxURL: origin.URL, AlwaysOnStreamKeys: "videokey-watts", LivePreviews: true, DebugLogSDP: true}
	cfg.normalize()
	p.setConfiguration(cfg)

	response := whepRequest(t, p, p.streamHandle("videokey-dave"), "", "uid-a")
	require.Equal(t, http.StatusOK, response.Code)
	body := response.Body.String()
	require.NotContains(t, strings.ToLower(body), "videokey-dave")
	require.Contains(t, body, "a=msid:"+answerStreamID+" video")
	require.Equal(t, strings.Count(answer, "\n"), strings.Count(body, "\n"))

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
		require.NotContains(t, strings.ToLower(fmt.Sprint(call.Arguments...)), "videokey-dave", call.Method)
	}
	require.True(t, logged, "the setting is on, so the exchange is logged")
}

// A refusal from Broadcast Box can name the stream it refused, and the panel's
// playback relay logs the refusal as it came. The key is taken out where the
// error is made, so every caller logs it without: the relay as well as the
// clips recorder, which used to be the only one that took it out.
func TestARefusedPlaybackRequestLogsNoStreamKey(t *testing.T) {
	refusing := func(refusal string) *httptest.Server {
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, refusal, http.StatusNotFound)
		}))
		t.Cleanup(origin.Close)

		return origin
	}
	origin := refusing("no stream VIDEOKEY-DAVE is being published (videokey-dave)")

	p := stillsPlugin(t)
	p.httpClient = origin.Client()
	cfg := &configuration{BroadcastBoxURL: origin.URL, AlwaysOnStreamKeys: "videokey-watts", LivePreviews: true}
	cfg.normalize()
	p.setConfiguration(cfg)

	response := whepRequest(t, p, p.streamHandle("videokey-dave"), "", "uid-a")
	require.Equal(t, http.StatusBadGateway, response.Code)
	require.NotContains(t, strings.ToLower(response.Body.String()), "videokey-dave")

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	logged := ""
	for _, call := range api.Calls {
		if !strings.HasPrefix(call.Method, "Log") {
			continue
		}
		line := fmt.Sprint(call.Arguments...)
		require.NotContains(t, strings.ToLower(line), "videokey-dave", call.Method)
		if len(call.Arguments) > 0 && call.Arguments[0] == "WHEP exchange failed" {
			logged = line
		}
	}
	require.Contains(t, logged, "no stream the stream is being published", "the refusal is still logged, without the key")

	// Taken out before the detail is cut to length, so a key that runs over
	// the cut does not leave its first half behind.
	origin = refusing(strings.Repeat("x", 190) + "videokey-dave")
	p.httpClient = origin.Client()
	cfg = &configuration{BroadcastBoxURL: origin.URL}
	cfg.normalize()
	_, _, err := p.exchangeWHEP(t.Context(), cfg, "videokey-dave", "v=0\r\n")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "videokey-m")
}
