package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Captured verbatim from tv.example.com GET /api/status.
var testHandleSecret = []byte("0123456789abcdef0123456789abcdef")

const liveStatusJSON = `[{"streamKey":"videokey-watts","isPublic":true,"motd":"Welcome to videokey-watts!","streamStart":"2026-09-01T18:46:53.421749032Z","audioTracks":[{"rid":"Audio","packetsReceived":1731052,"packetsDropped":0}],"videoTracks":[{"rid":"Video","bitrate":39420,"packetsReceived":1621353,"packetsDropped":0,"lastKeyframe":"2026-09-06T12:50:53.891351236Z"}],"sessions":[{"id":"cb74c661-4c6e-4f34-87dd-e6c03397d22d","videoBitrate":56702},{"id":"d1d35aa0-6382-47ed-846c-8fa17c18d1f0","videoBitrate":56703}]}]`

func TestParsesRealStatusPayload(t *testing.T) {
	var streams []bbStream
	require.NoError(t, json.Unmarshal([]byte(liveStatusJSON), &streams))
	require.Len(t, streams, 1)

	stream := streams[0]
	require.Equal(t, "videokey-watts", stream.StreamKey)
	require.Equal(t, "Welcome to videokey-watts!", stream.MOTD)
	require.Equal(t, "2026-09-01T18:46:53.421749032Z", stream.StreamStart)
	require.True(t, stream.live(), "one video track means live")
	require.False(t, stream.isPaused())
	require.Len(t, stream.Sessions, 2, "sessions are the viewer count")
	require.Equal(t, int64(39420), stream.bitrate())
}

func TestOfflineWhenNoVideoTracks(t *testing.T) {
	// A viewer hitting an idle key creates an entry with no video tracks.
	var streams []bbStream
	require.NoError(t, json.Unmarshal([]byte(`[{"streamKey":"videokey-dave","videoTracks":[],"sessions":[{"id":"x"}]}]`), &streams))
	require.False(t, streams[0].live())
}

func TestCleanStreamKeyStripsBearer(t *testing.T) {
	require.Equal(t, "videokey-villa", cleanStreamKey("Bearer videokey-villa"))
	require.Equal(t, "videokey-villa", cleanStreamKey("bearer  videokey-villa "))
	require.Equal(t, "videokey-villa", cleanStreamKey("videokey-villa"))
	require.Equal(t, "videokey-villa", normalizeKey("Bearer VideoKey-Villa"))
}

func TestNameOverridesAndAlwaysOn(t *testing.T) {
	cfg := &configuration{
		StreamNameOverrides: "videokey-watts = Alan Watts Chillstep\nvideokey-villa: Villa\n# a comment\n\nbroken-line",
		AlwaysOnStreamKeys:  "videokey-watts, videokey-radio",
	}

	overrides := cfg.nameOverrides()
	require.Equal(t, "Alan Watts Chillstep", overrides["videokey-watts"])
	require.Equal(t, "Villa", overrides["videokey-villa"])
	require.Len(t, overrides, 2, "comments and malformed lines are skipped")

	alwaysOn := cfg.alwaysOnSet()
	require.True(t, alwaysOn["videokey-watts"])
	require.True(t, alwaysOn["videokey-radio"])
	require.False(t, alwaysOn["videokey-villa"])
}

func TestNormalizeAppliesDefaults(t *testing.T) {
	cfg := &configuration{BroadcastBoxURL: "http://192.168.1.20:8080/", LiveEmoji: " :red_circle: "}
	cfg.normalize()

	require.Equal(t, "http://192.168.1.20:8080", cfg.BroadcastBoxURL, "trailing slash trimmed")
	require.Equal(t, "red_circle", cfg.LiveEmoji, "colons trimmed")
	require.Equal(t, defaultLiveText, cfg.LiveStatusText)
	require.Equal(t, defaultPollSeconds, cfg.PollIntervalSeconds)
	require.Equal(t, defaultOfflineGrace, cfg.OfflineGraceChecks)
	require.NoError(t, cfg.validate())
}

func TestValidateRejectsBadURLs(t *testing.T) {
	for _, raw := range []string{"", "tv.example.com", "ftp://tv.example.com"} {
		cfg := &configuration{BroadcastBoxURL: raw}
		cfg.normalize()
		require.Error(t, cfg.validate(), "expected %q to be rejected", raw)
	}
}

// decorate is where attribution, always-on tagging and ordering come together.
func TestDecorateAttributesAndOrders(t *testing.T) {
	api := &plugintest.API{}

	// No manual links stored.
	api.On("KVGet", mock.Anything).Return(nil, nil)

	villa := &model.User{Id: "uid-villa", Username: "videokey-villa", Nickname: "Villa"}
	api.On("GetUserByUsername", "videokey-villa").Return(villa, nil)
	api.On("GetUserByUsername", mock.Anything).Return(nil, &model.AppError{Message: "not found"})
	api.On("GetUser", "uid-villa").Return(villa, nil)

	p := &Plugin{handleSecret: testHandleSecret}
	p.SetAPI(api)

	cfg := &configuration{
		AutoLinkByUsername:  true,
		StreamNameOverrides: "videokey-watts = Alan Watts Chillstep",
		AlwaysOnStreamKeys:  "videokey-watts",
	}
	cfg.normalize()

	raw := []bbStream{
		{StreamKey: "videokey-watts", VideoTracks: []bbVideoTrack{{RID: "Video"}}, Sessions: []bbSession{{ID: "a"}}},
		{StreamKey: "Bearer videokey-villa", VideoTracks: []bbVideoTrack{{RID: "Video"}}},
		{StreamKey: "videokey-idle", VideoTracks: nil},
	}

	streams := p.decorate(raw, cfg)

	require.Len(t, streams, 2, "the idle key is dropped")

	require.Equal(t, "videokey-villa", streams[0].StreamKey, "the Bearer prefix is stripped")
	require.Equal(t, "Villa", streams[0].Title, "nickname wins as the title")
	require.Equal(t, "uid-villa", streams[0].UserID)
	require.Equal(t, linkSourceUsername, streams[0].LinkSource)
	require.False(t, streams[0].AlwaysOn)

	require.Equal(t, "videokey-watts", streams[1].StreamKey, "always-on channels sort last")
	require.Equal(t, "Alan Watts Chillstep", streams[1].Title, "override supplies the name")
	require.True(t, streams[1].AlwaysOn)
	require.Equal(t, 1, streams[1].Viewers)
	require.Empty(t, streams[1].UserID, "an unowned channel is attributed to nobody")
}

// An always-on channel must never move anyone's custom status.
func TestReconcileIgnoresAlwaysOnStreams(t *testing.T) {
	api := &plugintest.API{}
	api.On("KVList", mock.Anything, mock.Anything).Return([]string{}, nil)

	p := &Plugin{offlineStreak: map[string]int{}}
	p.SetAPI(api)

	cfg := &configuration{EnableLiveBadge: true, OfflineGraceChecks: 2}
	p.reconcileBadges([]apiStream{{StreamKey: "videokey-watts", UserID: "uid-bot", AlwaysOn: true}}, cfg, badgeClock, true)

	api.AssertNotCalled(t, "UpdateUserCustomStatus", mock.Anything, mock.Anything)
}

// A *model.AppError returned through an `error` result is a non-nil interface
// holding a nil pointer, so callers see a failure and then panic dereferencing
// it. That is what turned a successful re-link into a 500.
func TestSetLinkReturnsPlainNilOnSuccess(t *testing.T) {
	api := &plugintest.API{}
	api.On("KVGet", mock.Anything).Return(nil, nil)
	api.On("KVSet", mock.Anything, mock.Anything).Return(nil)
	api.On("KVDelete", mock.Anything).Return(nil)

	p := &Plugin{}
	p.SetAPI(api)

	err := p.setLink("uid-villa", "videokey-villa", "5f3c1b2a-token")
	require.NoError(t, err, "a successful link must not report an error")
	require.Nil(t, err)

	require.NoError(t, p.clearLink("uid-villa"))
	require.Nil(t, p.clearLink("uid-villa"))
}

func TestLinkedKeyIsLive(t *testing.T) {
	p := &Plugin{}
	p.setSnapshot(&snapshot{Streams: []apiStream{{StreamKey: "videokey-villa"}}})

	require.True(t, p.linkedKeyIsLive("videokey-villa"))
	require.True(t, p.linkedKeyIsLive("VideoKey-Villa"))
	require.False(t, p.linkedKeyIsLive("videokey-someone-else"))
}

func TestPublishTokenIsBase64OfTheToken(t *testing.T) {
	// Broadcast Box's ResolveBearerToken base64-decodes the bearer when it can.
	require.Equal(t, "NWYzYzFiMmEtdG9rZW4=", publishToken("5f3c1b2a-token"))
}

// Broadcast Box's default MOTD embeds the stream key, which is also the publish
// credential on this build, so it must not reach the panel verbatim.
func TestRedactStreamKeyFromMOTD(t *testing.T) {
	require.Equal(t, "Welcome to Alan Watts Chillstep!",
		redactStreamKey("Welcome to videokey-watts!", "videokey-watts", "Alan Watts Chillstep"))

	require.Equal(t, "Alan Watts Chillstep and Alan Watts Chillstep",
		redactStreamKey("VideoKey-Watts and videokey-watts", "videokey-watts", "Alan Watts Chillstep"),
		"every occurrence goes, whatever the casing")

	require.Equal(t, "Welcome to videokey-watts!",
		redactStreamKey("Welcome to videokey-watts!", "videokey-watts", "videokey-watts"),
		"with no friendly title the key is already the visible title")

	require.Equal(t, "Just chatting", redactStreamKey("Just chatting", "videokey-villa", "Villa"))
	require.Equal(t, "", redactStreamKey("", "videokey-villa", "Villa"))
}

func TestDefaultMOTDIsTreatedAsUnset(t *testing.T) {
	require.True(t, isDefaultMOTD("Welcome to videokey-villa!", "videokey-villa"))
	require.True(t, isDefaultMOTD("  welcome to VideoKey-Villa!  ", "videokey-villa"))
	require.False(t, isDefaultMOTD("Chill coding, come say hi", "videokey-villa"))
	require.False(t, isDefaultMOTD("", "videokey-villa"))
}

// The placeholder note must not reach the panel, since it embeds the key.
func TestDecorateBlanksPlaceholderMOTD(t *testing.T) {
	api := &plugintest.API{}
	api.On("KVGet", mock.Anything).Return(nil, nil)

	p := &Plugin{handleSecret: testHandleSecret}
	p.SetAPI(api)

	cfg := &configuration{StreamNameOverrides: "videokey-watts = Alan Watts Chillstep"}
	cfg.normalize()

	streams := p.decorate([]bbStream{
		{StreamKey: "videokey-watts", MOTD: "Welcome to videokey-watts!", VideoTracks: []bbVideoTrack{{RID: "Video"}}},
	}, cfg)

	require.Len(t, streams, 1)
	require.Empty(t, streams[0].MOTD, "the placeholder is dropped rather than shown")
	require.Equal(t, "Alan Watts Chillstep", streams[0].Title)
}

// A publish token pasted into a note written in Broadcast Box itself never
// passed through the panel's save, so the cards take it out as well — the
// owner's token, not only their stream key.
func TestDecorateTakesTheOwnersPublishTokenOutOfTheNote(t *testing.T) {
	token := "5d0c7e2a-41f6-4b8e-9a3c-7f1e2d9b6c40"

	api := &plugintest.API{}
	api.On("KVGet", kvLinkByKeyPrefix+"videokey-villa").Return([]byte("uid-villa"), nil)
	api.On("KVGet", kvLinkTokenPrefix+"uid-villa").Return([]byte(token), nil)
	api.On("KVGet", mock.Anything).Return(nil, nil)
	api.On("GetUser", "uid-villa").Return(&model.User{Id: "uid-villa", Username: "villa", Nickname: "Villa"}, nil)
	api.On("GetConfig").Return(&model.Config{}).Maybe()

	p := &Plugin{handleSecret: testHandleSecret}
	p.SetAPI(api)

	cfg := &configuration{}
	cfg.normalize()

	streams := p.decorate([]bbStream{
		{StreamKey: "videokey-villa", MOTD: "Arma tonight, relink with " + strings.ToUpper(token) + " lol", VideoTracks: []bbVideoTrack{{RID: "Video"}}},
	}, cfg)

	require.Len(t, streams, 1)
	require.Equal(t, "uid-villa", streams[0].UserID)
	require.NotContains(t, strings.ToLower(streams[0].MOTD), token)
	require.NotContains(t, strings.ToLower(streams[0].MOTD), token[:8], "not even part of it")
	require.Contains(t, streams[0].MOTD, "Arma tonight")
}

// A 24/7 channel has nobody to write it a note, so an admin can supply one —
// but only as a fallback, never over the top of a real one.
func TestStreamNotesFillOnlyAnEmptyNote(t *testing.T) {
	api := &plugintest.API{}
	api.On("KVGet", mock.Anything).Return(nil, nil)

	p := &Plugin{handleSecret: testHandleSecret}
	p.SetAPI(api)

	cfg := &configuration{
		StreamNameOverrides: "videokey-watts = Alan Watts Chillstep",
		StreamNotes:         "videokey-watts = A 24/7 Alan Watts chillstep stream.\nvideokey-villa = should not win",
	}
	cfg.normalize()

	streams := p.decorate([]bbStream{
		// No note at all.
		{StreamKey: "videokey-watts", VideoTracks: []bbVideoTrack{{RID: "Video"}}},
		// Broadcast Box's placeholder, which is blanked before the fallback runs.
		{StreamKey: "videokey-nobody", MOTD: "Welcome to videokey-nobody!", VideoTracks: []bbVideoTrack{{RID: "Video"}}},
		// A real one, which must be left alone.
		{StreamKey: "videokey-villa", MOTD: "Arma tonight", VideoTracks: []bbVideoTrack{{RID: "Video"}}},
	}, cfg)

	notes := map[string]string{}
	for _, stream := range streams {
		notes[stream.StreamKey] = stream.MOTD
	}

	require.Equal(t, "A 24/7 Alan Watts chillstep stream.", notes["videokey-watts"])
	require.Empty(t, notes["videokey-nobody"], "no fallback configured for it")
	require.Equal(t, "Arma tonight", notes["videokey-villa"], "a real note is never overruled")
}

// Stream keys must not reach the browser, so the panel addresses streams by an
// opaque handle that only the server can map back.
func TestStreamHandlesAreOpaqueAndStable(t *testing.T) {
	p := &Plugin{handleSecret: testHandleSecret}

	handle := p.streamHandle("videokey-villa")

	require.Len(t, handle, 32)
	require.NotContains(t, handle, "villa", "the handle must not embed the key")
	require.Equal(t, handle, p.streamHandle("videokey-villa"), "stable across polls")
	// Deliberately case-SENSITIVE: folding the key before hashing made distinct
	// live streams share a handle, and the relay serves the first match.
	require.NotEqual(t, handle, p.streamHandle("VideoKey-Villa"))
	require.NotEqual(t, handle, p.streamHandle("videokey-walter"))

	// A different installation must not produce the same handles.
	other := &Plugin{handleSecret: []byte("fedcba9876543210fedcba9876543210")}
	require.NotEqual(t, handle, other.streamHandle("videokey-villa"))
}

func TestHandleResolvesOnlyLiveStreams(t *testing.T) {
	p := &Plugin{handleSecret: testHandleSecret}

	live := p.streamHandle("videokey-villa")
	offline := p.streamHandle("videokey-walter")

	p.setSnapshot(&snapshot{Streams: []apiStream{
		{StreamKey: "videokey-villa", Handle: live},
	}})

	require.Equal(t, "videokey-villa", p.streamKeyForHandle(live))
	require.Empty(t, p.streamKeyForHandle(offline), "a stream that is not live must not resolve")
	require.Empty(t, p.streamKeyForHandle(""))
	require.Empty(t, p.streamKeyForHandle("deadbeef"))
}

// Without a secret the plugin must not emit a guessable handle.
func TestNoHandleWithoutSecret(t *testing.T) {
	p := &Plugin{}
	require.Empty(t, p.streamHandle("videokey-villa"))
}

// The snapshot goes to everybody, so it carries no address with a stream key
// in it, whatever the public watch setting says: Pop out goes through
// /api/v1/watch/popout instead, for linked people (see TestPopOut).
func TestTheSnapshotCarriesNoWatchAddress(t *testing.T) {
	api := &plugintest.API{}
	api.On("KVGet", mock.Anything).Return(nil, nil)

	p := &Plugin{handleSecret: testHandleSecret}
	p.SetAPI(api)

	raw := []bbStream{{StreamKey: "videokey-villa", MOTD: "Welcome to videokey-villa!", VideoTracks: []bbVideoTrack{{RID: "Video"}}}}

	for _, site := range []string{"", "https://tv.example.com"} {
		cfg := &configuration{PublicSiteURL: site}
		cfg.normalize()

		streams := p.decorate(raw, cfg)
		require.Len(t, streams, 1)
		require.Empty(t, streams[0].MOTD, "the placeholder is still dropped")

		encoded, err := json.Marshal(&snapshot{Streams: streams})
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "watchUrl", site)
		require.NotContains(t, string(encoded), "tv.example.com", site)
		require.NotContains(t, string(encoded), "videokey-villa", site)
	}
}

// The origin's DEBUG_PRINT_ANSWER is off, so the plugin is the only place the
// negotiation can be inspected. Candidates are the part that matters.
func TestCandidateLines(t *testing.T) {
	answer := "v=0\r\n" +
		"a=setup:active\r\n" +
		"a=candidate:1 1 udp 2130706431 192.168.1.20 3478 typ host\r\n" +
		"a=candidate:2 1 udp 2130706431 94.76.11.138 3478 typ host\r\n" +
		"a=ice-ufrag:abcd\r\n"

	candidates := candidateLines(answer)

	require.Len(t, candidates, 2)
	require.Contains(t, candidates[0], "192.168.1.20")
	require.Contains(t, candidates[1], "94.76.11.138")

	require.Empty(t, candidateLines("v=0\r\na=setup:active\r\n"), "an answer with no candidates yields none")
	require.Empty(t, candidateLines(""))
}

// A MOTD is origin-controlled. strings.ToLower("İ") is one byte longer than the
// original, so offsets taken from a lowered copy and applied to the original ran
// off the end and panicked — killing the plugin process, which makes every
// /plugins/ route 404 for every client.
func TestRedactStreamKeySurvivesLengthChangingUnicode(t *testing.T) {
	key := "videokey-villa"

	for _, motd := range []string{
		"İ" + key,           // capital I with dot above
		"İİİ " + key + " !", // several, before and after
		"ẞ" + key,           // capital sharp s
		strings.Repeat("İ", 40) + key,
		key + "İ",
		"İ" + key + "İ" + key,
	} {
		require.NotPanics(t, func() {
			out := redactStreamKey(motd, key, "Villa")
			require.NotContains(t, out, key, "the key must still be redacted: %q", out)
			require.Contains(t, out, "Villa")
		}, "input %q", motd)
	}
}

func TestAsciiFoldPreservesByteLength(t *testing.T) {
	for _, value := range []string{"ABC", "İ", "ẞ", "Ünïcødé", "", "MiXeD-123"} {
		require.Len(t, asciiFold(value), len(value), "fold must not shift byte offsets for %q", value)
	}
	require.Equal(t, "abc", asciiFold("ABC"))
	require.Equal(t, "İ", asciiFold("İ"), "non-ASCII is left alone")
}

// The stream key must never be used as a display title.
func TestUnnamedStreamDoesNotDiscloseTheKey(t *testing.T) {
	api := &plugintest.API{}
	api.On("KVGet", mock.Anything).Return(nil, nil)

	p := &Plugin{handleSecret: testHandleSecret}
	p.SetAPI(api)

	cfg := &configuration{}
	cfg.normalize()

	streams := p.decorate([]bbStream{
		{StreamKey: "videokey-newcomer", MOTD: "Welcome to videokey-newcomer!", VideoTracks: []bbVideoTrack{{RID: "Video"}}},
	}, cfg)

	require.Len(t, streams, 1)
	require.NotContains(t, streams[0].Title, "videokey-newcomer", "title must not be the key")
	require.NotContains(t, streams[0].MOTD, "videokey-newcomer", "motd must not carry the key either")

	encoded, err := json.Marshal(streams)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "videokey-newcomer", "the key must not appear anywhere on the wire")
}

// The public site URL is rendered into an href, so a non-http scheme must be
// dropped rather than passed to the browser.
func TestPublicSiteURLSchemeIsValidated(t *testing.T) {
	for _, bad := range []string{"javascript:alert(1)", "data:text/html,x", "file:///etc/passwd", "notaurl", "://x"} {
		cfg := &configuration{BroadcastBoxURL: "http://192.168.1.20:8080", PublicSiteURL: bad}
		cfg.normalize()
		require.Empty(t, cfg.PublicSiteURL, "expected %q to be rejected", bad)
	}

	cfg := &configuration{BroadcastBoxURL: "http://192.168.1.20:8080", PublicSiteURL: "https://tv.example.com/"}
	cfg.normalize()
	require.Equal(t, "https://tv.example.com", cfg.PublicSiteURL)
}

// Distinct stream keys must never share a handle: the lookup returns the first
// match, so a collision serves one streamer's video under another's name.
func TestHandlesDoNotCollideOnCaseOrPrefix(t *testing.T) {
	p := &Plugin{handleSecret: testHandleSecret}

	distinct := []string{"videokey-villa", "VideoKey-Villa", "Bearer videokey-villa", "videokey-villa "}
	seen := map[string]string{}

	for _, key := range distinct {
		h := p.streamHandle(key)
		if prior, clash := seen[h]; clash {
			t.Fatalf("handle collision between %q and %q", prior, key)
		}
		seen[h] = key
	}
}

func TestRateLimiterBurstAndRefill(t *testing.T) {
	limiter := newRateLimiter(5, 15*time.Minute)
	start := time.Unix(1788700000, 0)

	for i := 0; i < 5; i++ {
		require.True(t, limiter.allowAt("uid", start), "burst request %d must pass", i+1)
	}
	require.False(t, limiter.allowAt("uid", start), "the sixth in the same instant must be refused")

	// A different user has their own bucket.
	require.True(t, limiter.allowAt("other", start))

	// One token back after a fifth of the window.
	require.True(t, limiter.allowAt("uid", start.Add(3*time.Minute)))
	require.False(t, limiter.allowAt("uid", start.Add(3*time.Minute)))
}

func TestRateLimiterPrunesIdleBuckets(t *testing.T) {
	limiter := newRateLimiter(1, time.Minute)
	start := time.Unix(1788700000, 0)

	for i := 0; i < maxRateBuckets; i++ {
		limiter.allowAt(fmt.Sprintf("uid-%d", i), start)
	}
	require.Len(t, limiter.buckets, maxRateBuckets)

	limiter.allowAt("newcomer", start.Add(rateBucketTTL+time.Minute))
	require.Less(t, len(limiter.buckets), maxRateBuckets, "idle buckets must be reclaimed")
}

func TestValidStreamKey(t *testing.T) {
	require.True(t, validStreamKey("videokey-villa"))
	require.True(t, validStreamKey("Stream_1.2"))
	require.True(t, validStreamKey("ünïcøde-name"))

	require.False(t, validStreamKey(""))
	require.False(t, validStreamKey("has space"))
	require.False(t, validStreamKey("../../escape"))
	require.False(t, validStreamKey(strings.Repeat("a", maxStreamKeyRunes+1)))

	// Underscores are legal in Broadcast Box stream keys, so a key that looks
	// like another KV namespace is allowed — what makes it safe is that the
	// prefix is always prepended, so it can only ever land inside link_key_.
	require.True(t, validStreamKey("link_user_someone"))
	require.Equal(t, "link_key_link_user_someone", kvLinkByKeyPrefix+normalizeKey("link_user_someone"))
	require.NotEqual(t, kvLinkByUserPrefix+"someone", kvLinkByKeyPrefix+normalizeKey("link_user_someone"))
}

// A KV read failure must not look like "no link", or unlinking silently leaves
// the reverse mapping and the user keeps being badged.
func TestClearLinkFailsLoudlyWhenKVIsUnreadable(t *testing.T) {
	api := &plugintest.API{}
	api.On("KVGet", kvLinkByUserPrefix+"uid").Return(nil, &model.AppError{Message: "kv down"})

	p := &Plugin{}
	p.SetAPI(api)

	require.Error(t, p.clearLink("uid"))
	api.AssertNotCalled(t, "KVDelete", mock.Anything)
}

func TestDecorateCapsStreamCount(t *testing.T) {
	api := &plugintest.API{}
	api.On("KVGet", mock.Anything).Return(nil, nil)
	api.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()

	p := &Plugin{handleSecret: testHandleSecret}
	p.SetAPI(api)

	cfg := &configuration{}
	cfg.normalize()

	raw := make([]bbStream, maxStreams+50)
	for i := range raw {
		raw[i] = bbStream{StreamKey: fmt.Sprintf("stream-%d", i), VideoTracks: []bbVideoTrack{{RID: "Video"}}}
	}

	require.Len(t, p.decorate(raw, cfg), maxStreams)
}

// A slow poll finishing after a newer one must not roll the panel backwards.
func TestPublishDropsAStaleSnapshot(t *testing.T) {
	api := &plugintest.API{}
	api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return()

	p := &Plugin{}
	p.SetAPI(api)

	p.publish(&snapshot{Streams: []apiStream{{Handle: "new"}}, FetchedAt: 2000, Configured: true})
	p.publish(&snapshot{Streams: []apiStream{{Handle: "old"}}, FetchedAt: 1000, Configured: true})

	require.Equal(t, "new", p.getSnapshot().Streams[0].Handle, "the newer snapshot must survive")
}

// While anybody is on the air the boards' seconds move on every poll. Until
// 3.6.0 the boards rode the snapshot, so that was the whole snapshot to every
// connection once a minute, for as long as anybody streamed or watched. The
// boards are a document of their own now (boards.go): seconds that only tick
// push nothing at all, however long they tick for, and whoever is looking at
// a board reads them. A board that itself changes (a new row, an overtake, a
// new name) still tells every client at once, in the one word the snapshot
// carries about the boards.
func TestBoardSecondsThatOnlyTickPublishNothing(t *testing.T) {
	api := &plugintest.API{}
	api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return()

	p := &Plugin{}
	p.SetAPI(api)

	pushes := func() int {
		count := 0
		for _, call := range api.Calls {
			if call.Method == "PublishWebSocketEvent" && call.Arguments.Get(0) == wsEventStreams {
				count++
			}
		}

		return count
	}

	// Two streamers and two viewers, every figure on a second of its own.
	board := func(tick int64) *snapshot {
		return &snapshot{
			Configured:   true,
			Streams:      []apiStream{{Handle: "h1"}, {Handle: "h2"}},
			AllTime:      []leaderEntry{{Rank: 1, Title: "Dave", Seconds: 3600 + tick, TwitchSeconds: 1795 + tick}, {Rank: 2, Title: "Bob", Seconds: 1217 + tick}},
			Month:        []leaderEntry{{Rank: 1, Title: "Dave", Seconds: 931 + tick}, {Rank: 2, Title: "Bob", Seconds: 644 + tick}},
			WatchAllTime: []leaderEntry{{Rank: 1, Title: "Trudy", Seconds: 2048 + tick}, {Rank: 2, Title: "Craig", Seconds: 77 + tick}},
			WatchMonth:   []leaderEntry{{Rank: 1, Title: "Trudy", Seconds: 319 + tick}, {Rank: 2, Title: "Craig", Seconds: 23 + tick}},
		}
	}

	// A poll every fifteen seconds from the top of a minute.
	const minute = int64(60_000)
	start := 29_000_000 * minute
	poll := func(n int64, current *snapshot) {
		current.FetchedAt = start + n*15_000
		p.publish(current)
	}
	tags := func() (version, tag string) {
		p.snapshotLock.RLock()
		defer p.snapshotLock.RUnlock()

		return p.snapshot.Boards.Version, p.boardsTag
	}

	poll(0, board(0))
	require.Equal(t, 1, pushes(), "the first snapshot goes out")
	version, tag := tags()
	require.NotEmpty(t, version)

	// Three minutes of nothing but seconds.
	for n := int64(1); n <= 12; n++ {
		poll(n, board(n*15))
	}
	require.Equal(t, 1, pushes(), "figures that only tick are never a push, in this minute or the next")

	stillVersion, movedTag := tags()
	require.Equal(t, version, stillVersion, "and the snapshot says the boards are what they were")
	require.NotEqual(t, tag, movedTag, "while somebody reading the boards is given the seconds as counted")
	require.Equal(t, int64(3600+180), p.boardsNow().AllTime[0].Seconds)
	require.Equal(t, int64(3600+180), p.getSnapshot().AllTime[0].Seconds, "as is the server's own copy")

	// Nobody on the air any more: the figures stand still, and so does the
	// boards' tag, so a reader with a copy is told "that one still".
	poll(13, board(180))
	_, tag = tags()
	poll(14, board(180))
	poll(18, board(180))
	_, sameTag := tags()
	require.Equal(t, tag, sameTag)
	require.Equal(t, 1, pushes())

	// An overtake is a different board, whatever the second.
	overtaken := board(185)
	overtaken.Month = []leaderEntry{{Rank: 1, Title: "Bob", Seconds: 1100}, {Rank: 2, Title: "Dave", Seconds: 1041}}
	poll(19, overtaken)
	require.Equal(t, 2, pushes(), "a change of place goes out on the poll that sees it")
	movedVersion, _ := tags()
	require.NotEqual(t, version, movedVersion, "as the word that says to read the boards again")

	// So is somebody's first appearance, and they then tick like the rest.
	joined := func(tick int64) *snapshot {
		current := board(185 + tick)
		current.Month = []leaderEntry{{Rank: 1, Title: "Bob", Seconds: 1100 + tick}, {Rank: 2, Title: "Dave", Seconds: 1041 + tick}}
		current.WatchMonth = append(current.WatchMonth, leaderEntry{Rank: 3, Title: "Faythe", Seconds: 5 + tick})

		return current
	}
	poll(20, joined(0))
	require.Equal(t, 3, pushes(), "a new row goes out at once")

	poll(21, joined(15))
	poll(24, joined(60))
	poll(28, joined(120))
	require.Equal(t, 3, pushes(), "and its seconds tick like everybody's")

	// A slower poll that finishes after a newer one is dropped whole: neither
	// the snapshot nor the boards go back to it.
	stale := board(0)
	stale.FetchedAt = start
	p.publish(stale)
	require.Equal(t, 3, pushes(), "a discarded snapshot is not a change")
	require.Equal(t, int64(3600+185+120), p.boardsNow().AllTime[0].Seconds)
	require.Len(t, p.boardsNow().WatchMonth, 3)
}

// Broadcast Box measures a live stream's bitrate afresh on every read, and no
// panel shows it. Counted as a change, it pushed the whole snapshot to every
// connection on every poll, all day long while the 24/7 channel was on the air.
// A change somebody can see still goes out, with the bitrate of the moment in
// it, and the snapshot kept for the routes and the recorders always has it.
func TestABitrateThatMovesOnItsOwnPublishesNothing(t *testing.T) {
	fake := newMediaFake(t)
	g := newGoldenScene(t, &configuration{})

	var reads, viewers atomic.Int64
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		read := reads.Add(1)

		sessions := []bbSession{}
		for i := range viewers.Load() {
			sessions = append(sessions, bbSession{ID: fmt.Sprintf("bbbbbbbb-0000-0000-0000-%012d", i), VideoBitrate: 56702 + read})
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]bbStream{
			{StreamKey: "videokey-dave", StreamStart: "2099-01-01T00:00:00Z", VideoTracks: []bbVideoTrack{{RID: "Video", Bitrate: 2500000 + read*1000}}, AudioTracks: []bbAudioTrack{{}}, Sessions: sessions},
			{StreamKey: "videokey-watts", StreamStart: "2099-01-01T00:00:00Z", VideoTracks: []bbVideoTrack{{RID: "Video", Bitrate: 39420 + read}}},
		})
	}))
	t.Cleanup(origin.Close)

	cfg := g.p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = origin.URL
	g.p.setConfiguration(cfg)
	g.p.httpClient = origin.Client()

	pushed := func() []string {
		var payloads []string
		for _, call := range g.api.Calls {
			if call.Method == "PublishWebSocketEvent" && call.Arguments.Get(0) == wsEventStreams {
				data, ok := call.Arguments.Get(1).(map[string]any)
				require.True(t, ok)
				payloads = append(payloads, data["payload"].(string))
			}
		}

		return payloads
	}

	g.p.pollOnce(t.Context(), true)
	first := len(pushed())
	require.NotZero(t, first, "the first poll is news")

	for range 20 {
		g.p.pollOnce(t.Context(), true)
	}
	require.Equal(t, first, len(pushed()), "twenty polls, nothing but the bitrate moved, nothing pushed")

	dave := func(current *snapshot) apiStream {
		for _, stream := range current.Streams {
			if stream.UserID == "u-dave" {
				return stream
			}
		}
		require.Fail(t, "Dave is live")

		return apiStream{}
	}
	require.Equal(t, int64(2500000+21*1000), dave(g.p.getSnapshot()).Bitrate, "the kept snapshot has the bitrate as last read")

	// Somebody starts watching: that is a change, and it carries the bitrate.
	viewers.Store(1)
	g.p.pollOnce(t.Context(), true)
	payloads := pushed()
	require.Equal(t, first+1, len(payloads))

	var shown snapshot
	require.NoError(t, json.Unmarshal([]byte(payloads[len(payloads)-1]), &shown))
	require.Equal(t, 1, dave(&shown).Viewers)
	require.Equal(t, int64(2500000+22*1000), dave(&shown).Bitrate)
	require.Empty(t, fake.requests())
}

func TestValidateRejectsCredentialsInTheURL(t *testing.T) {
	cfg := &configuration{BroadcastBoxURL: "http://user:secret@192.168.1.20:8080"}
	cfg.normalize()
	require.ErrorContains(t, cfg.validate(), "Basic Auth settings")
}

// An unrecognised value must leave people alone rather than do something
// surprising every time they load the page.
func TestOpenPanelIsNormalised(t *testing.T) {
	for value, want := range map[string]string{
		"live":     "live",
		"always":   "always",
		"never":    "never",
		"":         "never",
		"  live  ": "live",
		"Always":   "never",
		"yes":      "never",
	} {
		cfg := &configuration{OpenPanel: value}
		require.Equal(t, want, cfg.openPanel(), "for %q", value)
	}
}

// A dropdown value that is not recognised is the default, not "off": a typo
// should not quietly remove a feature nobody asked to remove.
func TestNoSignalGhostIsNormalised(t *testing.T) {
	for given, want := range map[string]string{
		"":           "occasional",
		"occasional": "occasional",
		"rare":       "rare",
		"OFTEN":      "often",
		" off ":      "off",
		"sometimes":  "occasional",
	} {
		cfg := &configuration{NoSignalGhost: given}
		require.Equal(t, want, cfg.noSignalGhost(), "given %q", given)
	}
}
