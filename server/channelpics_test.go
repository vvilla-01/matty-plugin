package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Enough of a PNG for the sniffer: the signature is what it goes by.
var pngBytes = []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 64))

// A raw Broadcast Box entry that is on the air.
func rawLive(key string) bbStream {
	return bbStream{StreamKey: key, VideoTracks: []bbVideoTrack{{RID: "Video"}}}
}

// A plugin with two always-on channels configured, neither with a picture yet
// (none is built in), and a handle secret so handles can be made.
func channelPicturePlugin(t *testing.T) (*Plugin, map[string][]byte) {
	t.Helper()

	p, store := brandPlugin(t)
	p.handleSecret = testHandleSecret
	cfg := &configuration{
		AlwaysOnStreamKeys:  "videokey-watts, Lofi-Radio",
		StreamNameOverrides: "videokey-watts = Alan Watts Chillstep",
	}
	cfg.normalize()
	p.setConfiguration(cfg)

	return p, store
}

// seedChannelPicture keeps a JPEG as a channel's uploaded picture, as an
// admin's upload keeps it, and returns it.
func seedChannelPicture(t *testing.T, store map[string][]byte, key string) *storedBrand {
	t.Helper()

	data, err := os.ReadFile("testdata/banner-square.jpg")
	require.NoError(t, err)
	picture := &storedBrand{ContentType: "image/jpeg", Data: data}
	stored, err := json.Marshal(picture)
	require.NoError(t, err)
	store[channelPictureKey(key)] = stored

	return picture
}

func channelAdmin(t *testing.T, p *Plugin, method, query string, body []byte, userID string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, "/api/v1/admin/channel-pictures"+query, strings.NewReader(string(body)))

	p.handleChannelPictures(recorder, request, userID)

	return recorder
}

func channelServe(t *testing.T, p *Plugin, handle string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/channel/picture?handle="+handle, nil)

	p.handleChannelPicture(recorder, request)

	return recorder
}

// No channel picture is built in: a channel has one once an admin uploads it,
// kept under the normalised key, so the spelling in Broadcast Box does not
// matter.
func TestNoChannelPictureIsBuiltIn(t *testing.T) {
	p, store := channelPicturePlugin(t)

	picture, custom := p.channelPicture("videokey-watts")
	require.Nil(t, picture, "a channel with nothing uploaded has no picture")
	require.False(t, custom)
	require.Equal(t, http.StatusNotFound, channelServe(t, p, p.streamHandle("videokey-watts")).Code)

	seedChannelPicture(t, store, "videokey-watts")
	picture, custom = p.channelPicture("VideoKey-Watts")
	require.NotNil(t, picture)
	require.True(t, custom)
	require.Equal(t, "image/jpeg", picture.ContentType)

	picture, _ = p.channelPicture("lofi-radio")
	require.Nil(t, picture)
}

func TestAChannelPictureIsServedByHandleNotByKey(t *testing.T) {
	p, store := channelPicturePlugin(t)
	seedChannelPicture(t, store, "videokey-watts")

	response := channelServe(t, p, p.streamHandle("videokey-watts"))
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "image/jpeg", response.Header().Get("Content-Type"))
	require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))

	// The key itself is not a handle, and is not answered.
	require.Equal(t, http.StatusNotFound, channelServe(t, p, "videokey-watts").Code)
	require.Equal(t, http.StatusNotFound, channelServe(t, p, "").Code)
	require.Equal(t, http.StatusNotFound, channelServe(t, p, "0123456789abcdef0123456789abcdef").Code)
}

func TestAPersonsStreamHasNoChannelPictureToServe(t *testing.T) {
	p, _ := channelPicturePlugin(t)

	// Live, but not always-on: resolves as a stream, and is still refused.
	p.setSnapshot(&snapshot{Streams: []apiStream{{StreamKey: "videokey-dave", Handle: p.streamHandle("videokey-dave")}}})

	require.Equal(t, http.StatusNotFound, channelServe(t, p, p.streamHandle("videokey-dave")).Code)
}

func TestAnUploadedChannelPictureReplacesTheLastAndCanBeRemoved(t *testing.T) {
	p, store := channelPicturePlugin(t)

	before := seedChannelPicture(t, store, "videokey-watts")

	response := channelAdmin(t, p, http.MethodPost, "?key=videokey-watts", pngBytes, "uid-admin")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	picture, custom := p.channelPicture("videokey-watts")
	require.True(t, custom)
	require.Equal(t, "image/png", picture.ContentType)
	require.NotEqual(t, pictureVersion(before.Data), pictureVersion(picture.Data))

	// Stored under a hash, not under the key in the clear.
	for stored := range store {
		require.NotContains(t, stored, "watts")
	}

	response = channelAdmin(t, p, http.MethodDelete, "?key=videokey-watts", nil, "uid-admin")
	require.Equal(t, http.StatusOK, response.Code)

	picture, custom = p.channelPicture("videokey-watts")
	require.False(t, custom)
	require.Nil(t, picture, "nothing built in to go back to")
	require.Empty(t, store)
}

func TestOnlyAlwaysOnChannelsCanBeGivenAPicture(t *testing.T) {
	p, store := channelPicturePlugin(t)

	response := channelAdmin(t, p, http.MethodPost, "?key=videokey-dave", pngBytes, "uid-admin")
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Empty(t, store)

	// Matched however the key is spelled in the settings.
	response = channelAdmin(t, p, http.MethodPost, "?key=LOFI-radio", pngBytes, "uid-admin")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
}

func TestOnlyRealImagesBecomeChannelPictures(t *testing.T) {
	p, store := channelPicturePlugin(t)

	response := channelAdmin(t, p, http.MethodPost, "?key=lofi-radio", []byte("<html><script>alert(1)</script></html>"), "uid-admin")
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Empty(t, store)
}

func TestOnlyASystemAdminCanChangeChannelPictures(t *testing.T) {
	p, store := channelPicturePlugin(t)

	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		response := channelAdmin(t, p, method, "?key=videokey-watts", pngBytes, "uid-user")
		require.Equal(t, http.StatusForbidden, response.Code, method)
	}
	require.Empty(t, store)
}

func TestTheSettingsPageIsToldEachChannelAndWhatItHas(t *testing.T) {
	p, store := channelPicturePlugin(t)
	seedChannelPicture(t, store, "videokey-watts")

	response := channelAdmin(t, p, http.MethodGet, "", nil, "uid-admin")
	require.Equal(t, http.StatusOK, response.Code)

	var channels []adminChannel
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &channels))
	require.Len(t, channels, 2)

	require.Equal(t, "lofi-radio", channels[0].Key)
	require.False(t, channels[0].Shipped)
	require.Empty(t, channels[0].Version)

	require.Equal(t, "videokey-watts", channels[1].Key)
	require.Equal(t, "Alan Watts Chillstep", channels[1].Name)
	require.False(t, channels[1].Shipped, "nothing is built in")
	require.True(t, channels[1].Custom)
	require.NotEmpty(t, channels[1].Version)

	// And a preview, by key, for the admin only.
	response = channelAdmin(t, p, http.MethodGet, "?key=videokey-watts", nil, "uid-admin")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "image/jpeg", response.Header().Get("Content-Type"))
}

// The settings page names a channel by its handle in every address, so that no
// 24/7 key is written into an access log: the list says each one's handle, and
// a preview, an upload and a put-back each go by it. A handle that is no
// 24/7 channel's is refused as an unknown key is, never taken for the list.
func TestTheSettingsPageNamesAChannelByItsHandle(t *testing.T) {
	p, store := channelPicturePlugin(t)

	var channels []adminChannel
	require.NoError(t, json.Unmarshal(channelAdmin(t, p, http.MethodGet, "", nil, "uid-admin").Body.Bytes(), &channels))
	require.Len(t, channels, 2)
	handles := map[string]string{}
	for _, channel := range channels {
		require.Equal(t, p.streamHandle(channel.Key), channel.Handle, channel.Key)
		require.NotContains(t, channel.Handle, channel.Key)
		handles[channel.Key] = channel.Handle
	}

	watts := "?handle=" + handles["videokey-watts"]
	response := channelAdmin(t, p, http.MethodGet, watts, nil, "uid-admin")
	require.Equal(t, http.StatusNotFound, response.Code, "nothing uploaded, and nothing built in")

	response = channelAdmin(t, p, http.MethodPost, watts, pngBytes, "uid-admin")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	picture, custom := p.channelPicture("videokey-watts")
	require.True(t, custom)
	require.Equal(t, "image/png", picture.ContentType)
	require.Equal(t, "image/png", channelAdmin(t, p, http.MethodGet, watts, nil, "uid-admin").Header().Get("Content-Type"))

	thumb := "?handle=" + handles["lofi-radio"] + "&kind=thumb"
	require.Equal(t, http.StatusOK, channelAdmin(t, p, http.MethodPost, thumb, pngBytes, "uid-admin").Code)
	require.NotNil(t, p.channelThumbnail("lofi-radio"))
	require.Equal(t, http.StatusOK, channelAdmin(t, p, http.MethodDelete, thumb, nil, "uid-admin").Code)
	require.Nil(t, p.channelThumbnail("lofi-radio"))

	require.Equal(t, http.StatusOK, channelAdmin(t, p, http.MethodDelete, watts, nil, "uid-admin").Code)
	_, custom = p.channelPicture("videokey-watts")
	require.False(t, custom)
	require.Empty(t, store)

	// Not a 24/7 channel's handle, or none at all.
	for _, query := range []string{"?handle=" + p.streamHandle("videokey-dave"), "?handle=", "?handle=nonsense"} {
		response := channelAdmin(t, p, http.MethodGet, query, nil, "uid-admin")
		require.Equal(t, http.StatusNotFound, response.Code, query)
		require.Equal(t, http.StatusBadRequest, channelAdmin(t, p, http.MethodPost, query, pngBytes, "uid-admin").Code, query)
		require.Equal(t, http.StatusBadRequest, channelAdmin(t, p, http.MethodDelete, query, nil, "uid-admin").Code, query)
	}
	require.Empty(t, store)

	// A handle wins over a key beside it.
	both := "?handle=" + p.streamHandle("videokey-dave") + "&key=videokey-watts"
	require.Equal(t, http.StatusBadRequest, channelAdmin(t, p, http.MethodPost, both, pngBytes, "uid-admin").Code)
	require.Empty(t, store)

	// And ?key=, from a page opened before the upgrade, still works for now:
	// the tests above this one go by it.
	seedChannelPicture(t, store, "videokey-watts")
	require.Equal(t, http.StatusOK, channelAdmin(t, p, http.MethodGet, "?key=videokey-watts", nil, "uid-admin").Code)
	require.Equal(t, http.StatusNotFound, channelAdmin(t, p, http.MethodGet, "?key=", nil, "uid-admin").Code, "named, if by nothing")
}

func TestAnAlwaysOnStreamCarriesItsPictureVersion(t *testing.T) {
	p, store := channelPicturePlugin(t)
	uploaded := seedChannelPicture(t, store, "videokey-watts")
	cfg := p.getConfiguration()

	streams := p.decorate([]bbStream{rawLive("videokey-watts"), rawLive("lofi-radio")}, cfg)
	require.Len(t, streams, 2)

	byKey := map[string]apiStream{}
	for _, stream := range streams {
		byKey[stream.StreamKey] = stream
	}

	require.Equal(t, pictureVersion(uploaded.Data), byKey["videokey-watts"].PictureVersion)
	require.Empty(t, byKey["lofi-radio"].PictureVersion, "nothing to show, so nothing to ask for")

	// And it is never the key that reaches the browser.
	encoded, err := json.Marshal(streams)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "videokey-watts")
}

// A handle is an HMAC of the key exactly as Broadcast Box relays it, while the
// setting is case-folded. A channel spelled with capitals has to keep its
// picture when it drops off the air all the same.
func TestAMixedCaseChannelKeepsItsPictureOffTheAir(t *testing.T) {
	p, store := channelPicturePlugin(t)
	seedChannelPicture(t, store, "videokey-watts")
	cfg := p.getConfiguration()

	streams := p.decorate([]bbStream{rawLive("VideoKey-Watts")}, cfg)
	require.Len(t, streams, 1)
	handle := streams[0].Handle

	// On the air: resolved from the snapshot.
	p.setSnapshot(&snapshot{Streams: streams})
	require.Equal(t, http.StatusOK, channelServe(t, p, handle).Code)

	// Off the air: resolved from what decorate remembered.
	p.setSnapshot(&snapshot{})
	require.Equal(t, http.StatusOK, channelServe(t, p, handle).Code)

	// No longer always-on: forgotten on the next poll, and refused.
	demoted := &configuration{AlwaysOnStreamKeys: "lofi-radio"}
	demoted.normalize()
	p.setConfiguration(demoted)
	p.decorate(nil, demoted)
	require.Equal(t, http.StatusNotFound, channelServe(t, p, handle).Code)
}

// A channel picture is revalidated every time, whatever version the address
// names: one kept for a day would be shown again without the access gate
// hearing of it, after a let-out or an unlink. The ETag makes that a 304.
func TestAChannelPictureIsAlwaysRevalidated(t *testing.T) {
	p, store := channelPicturePlugin(t)

	uploaded := seedChannelPicture(t, store, "videokey-watts")
	current := pictureVersion(uploaded.Data)
	handle := p.streamHandle("videokey-watts")

	for _, version := range []string{current, "000000000000", ""} {
		recorder := httptest.NewRecorder()
		p.handleChannelPicture(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/channel/picture?handle="+handle+"&v="+version, nil))
		require.Equal(t, http.StatusOK, recorder.Code, version)
		require.Equal(t, "private, no-cache", recorder.Header().Get("Cache-Control"), version)
		require.NotEmpty(t, recorder.Header().Get("ETag"), version)

		request := httptest.NewRequest(http.MethodGet, "/api/v1/channel/picture?handle="+handle+"&v="+version, nil)
		request.Header.Set("If-None-Match", recorder.Header().Get("ETag"))
		again := httptest.NewRecorder()
		p.handleChannelPicture(again, request)
		require.Equal(t, http.StatusNotModified, again.Code, version)
	}
}

func thumbServe(t *testing.T, p *Plugin, query string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	p.handleChannelThumbnail(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/channel/thumbnail"+query, nil))

	return recorder
}

// The card picture is a second picture, not a second copy of the first: it is
// stored, listed, served and put back on its own, and the square one is not
// touched by any of it.
func TestAChannelCardPictureIsItsOwnPicture(t *testing.T) {
	p, store := channelPicturePlugin(t)

	square := seedChannelPicture(t, store, "videokey-watts")

	response := channelAdmin(t, p, http.MethodPost, "?key=videokey-watts&kind=thumb", pngBytes, "uid-admin")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var saved map[string]string
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &saved))
	require.Equal(t, pictureVersion(pngBytes), saved["version"])

	thumb := p.channelThumbnail("videokey-watts")
	require.NotNil(t, thumb)
	require.Equal(t, "image/png", thumb.ContentType)

	picture, custom := p.channelPicture("videokey-watts")
	require.True(t, custom, "the square picture is still the one uploaded before")
	require.Equal(t, pictureVersion(square.Data), pictureVersion(picture.Data))

	// Under its own hashed key, never the stream key in the clear.
	require.Len(t, store, 2)
	for stored := range store {
		if stored == channelPictureKey("videokey-watts") {
			continue
		}
		require.True(t, strings.HasPrefix(stored, kvChannelThumbPrefix), stored)
		require.NotContains(t, stored, "watts")
	}

	// Listed alongside the square one.
	response = channelAdmin(t, p, http.MethodGet, "", nil, "uid-admin")
	require.Equal(t, http.StatusOK, response.Code)

	var channels []adminChannel
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &channels))
	require.Len(t, channels, 2)
	require.Equal(t, "lofi-radio", channels[0].Key)
	require.False(t, channels[0].ThumbCustom)
	require.Empty(t, channels[0].ThumbVersion)
	require.Equal(t, "videokey-watts", channels[1].Key)
	require.True(t, channels[1].ThumbCustom)
	require.Equal(t, pictureVersion(pngBytes), channels[1].ThumbVersion)
	require.True(t, channels[1].Custom)
	require.Equal(t, pictureVersion(square.Data), channels[1].Version)

	// The admin's preview, by key and kind.
	response = channelAdmin(t, p, http.MethodGet, "?key=videokey-watts&kind=thumb", nil, "uid-admin")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "image/png", response.Header().Get("Content-Type"))
	require.Equal(t, http.StatusNotFound, channelAdmin(t, p, http.MethodGet, "?key=lofi-radio&kind=thumb", nil, "uid-admin").Code)

	// Taken away again: there is nothing built in to go back to, and the
	// square picture stays as it was.
	response = channelAdmin(t, p, http.MethodDelete, "?key=videokey-watts&kind=thumb", nil, "uid-admin")
	require.Equal(t, http.StatusOK, response.Code)
	require.Nil(t, p.channelThumbnail("videokey-watts"))
	require.Len(t, store, 1)

	picture, _ = p.channelPicture("videokey-watts")
	require.Equal(t, pictureVersion(square.Data), pictureVersion(picture.Data))
}

func TestOnlyASystemAdminCanChangeCardPictures(t *testing.T) {
	p, store := channelPicturePlugin(t)

	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		response := channelAdmin(t, p, method, "?key=videokey-watts&kind=thumb", pngBytes, "uid-user")
		require.Equal(t, http.StatusForbidden, response.Code, method)
	}
	require.Empty(t, store)
}

func TestOnlyAlwaysOnChannelsCanBeGivenACardPicture(t *testing.T) {
	p, store := channelPicturePlugin(t)

	response := channelAdmin(t, p, http.MethodPost, "?key=videokey-dave&kind=thumb", pngBytes, "uid-admin")
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Equal(t, http.StatusBadRequest, channelAdmin(t, p, http.MethodDelete, "?key=videokey-dave&kind=thumb", nil, "uid-admin").Code)
	require.Equal(t, http.StatusNotFound, channelAdmin(t, p, http.MethodGet, "?key=videokey-dave&kind=thumb", nil, "uid-admin").Code)

	response = channelAdmin(t, p, http.MethodPost, "?key=lofi-radio&kind=thumb", []byte("<html></html>"), "uid-admin")
	require.Equal(t, http.StatusBadRequest, response.Code, "and only a real image")
	require.Empty(t, store)
}

func TestAnUnknownPictureKindIsRefused(t *testing.T) {
	p, store := channelPicturePlugin(t)

	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		response := channelAdmin(t, p, method, "?key=videokey-watts&kind=banner", pngBytes, "uid-admin")
		require.Equal(t, http.StatusBadRequest, response.Code, method)
	}
	require.Empty(t, store)

	// "picture" is the same as saying nothing.
	response := channelAdmin(t, p, http.MethodPost, "?key=lofi-radio&kind=picture", pngBytes, "uid-admin")
	require.Equal(t, http.StatusOK, response.Code)
	_, custom := p.channelPicture("lofi-radio")
	require.True(t, custom)
	require.Nil(t, p.channelThumbnail("lofi-radio"))
}

func TestACardPictureIsServedByHandleAndRevalidated(t *testing.T) {
	p, store := channelPicturePlugin(t)
	handle := p.streamHandle("videokey-watts")

	require.Equal(t, http.StatusNotFound, thumbServe(t, p, "?handle="+handle).Code, "nothing is shipped")

	require.Equal(t, http.StatusOK, channelAdmin(t, p, http.MethodPost, "?key=videokey-watts&kind=thumb", pngBytes, "uid-admin").Code)

	response := thumbServe(t, p, "?handle="+handle+"&v="+pictureVersion(pngBytes))
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "image/png", response.Header().Get("Content-Type"))
	require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "private, no-cache", response.Header().Get("Cache-Control"), "never kept without asking")
	require.Equal(t, pngBytes, response.Body.Bytes())

	response = thumbServe(t, p, "?handle="+handle+"&v=000000000000")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "private, no-cache", response.Header().Get("Cache-Control"))

	// Not by key, not for a person's stream, not for nothing.
	require.Equal(t, http.StatusNotFound, thumbServe(t, p, "?handle=videokey-watts").Code)
	require.Equal(t, http.StatusNotFound, thumbServe(t, p, "").Code)
	p.setSnapshot(&snapshot{Streams: []apiStream{{StreamKey: "videokey-dave", Handle: p.streamHandle("videokey-dave")}}})
	require.Equal(t, http.StatusNotFound, thumbServe(t, p, "?handle="+p.streamHandle("videokey-dave")).Code)

	// And through the router, for anybody who can watch, and nobody else.
	routed := func(userID string) int {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/channel/thumbnail?handle="+handle, nil)
		request.Header.Set("Mattermost-User-Id", userID)
		recorder := httptest.NewRecorder()
		p.ServeHTTP(nil, recorder, request)

		return recorder.Code
	}
	require.Equal(t, http.StatusForbidden, routed("uid-user"), "locked")
	linkForTest(store, "uid-user", "videokey-user")
	p.accessForget("uid-user")
	require.Equal(t, http.StatusOK, routed("uid-user"))
}

func TestAnAlwaysOnStreamCarriesItsCardPictureVersion(t *testing.T) {
	p, _ := channelPicturePlugin(t)
	cfg := p.getConfiguration()
	raw := []bbStream{rawLive("videokey-watts"), rawLive("videokey-dave")}

	byKey := func() map[string]apiStream {
		streams := p.decorate(raw, cfg)
		keyed := map[string]apiStream{}
		for _, stream := range streams {
			keyed[stream.StreamKey] = stream
		}

		return keyed
	}

	encoded, err := json.Marshal(p.decorate(raw, cfg))
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "thumbnailVersion", "absent, not empty, until there is one")

	require.Equal(t, http.StatusOK, channelAdmin(t, p, http.MethodPost, "?key=videokey-watts&kind=thumb", pngBytes, "uid-admin").Code)

	// The upload forgot the remembered answer, so the very next poll has it.
	streams := byKey()
	require.Equal(t, pictureVersion(pngBytes), streams["videokey-watts"].ThumbnailVersion)
	require.Empty(t, streams["videokey-dave"].ThumbnailVersion, "a person's stream has a preview instead")

	encoded, err = json.Marshal(p.decorate(raw, cfg))
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "videokey-watts")

	require.Equal(t, http.StatusOK, channelAdmin(t, p, http.MethodDelete, "?key=videokey-watts&kind=thumb", nil, "uid-admin").Code)
	require.Empty(t, byKey()["videokey-watts"].ThumbnailVersion)
}

// The poller asks for both versions of every 24/7 channel on every poll. The
// answer is remembered; an upload on this node forgets it at once, and a change
// made behind its back — another node of a cluster — is noticed once the
// remembered answer times out.
func TestChannelPictureVersionsAreRememberedBetweenPolls(t *testing.T) {
	p, store := channelPicturePlugin(t)
	now := time.Now()

	uploaded := seedChannelPicture(t, store, "videokey-watts")
	require.Equal(t, pictureVersion(uploaded.Data), p.channelImageVersion(channelPictureKind, "videokey-watts", now))
	require.Empty(t, p.channelImageVersion(channelThumbKind, "videokey-watts", now))

	// Written straight into the store, as another node would.
	for kind, key := range map[string]string{channelPictureKind: channelPictureKey("videokey-watts"), channelThumbKind: channelThumbKey("videokey-watts")} {
		stored, err := json.Marshal(storedBrand{ContentType: "image/png", Data: pngBytes})
		require.NoError(t, err)
		store[key] = stored

		later := now.Add(channelVersionTTL - time.Second)
		require.NotEqual(t, pictureVersion(pngBytes), p.channelImageVersion(kind, "VideoKey-Watts", later), "%s: remembered", kind)

		later = now.Add(channelVersionTTL)
		require.Equal(t, pictureVersion(pngBytes), p.channelImageVersion(kind, "videokey-watts", later), "%s: looked at again", kind)
	}
}

// The one race the version cache has to get right: a poll reads the old
// picture, an upload lands and forgets the version, and only then does the
// poll come to remember what it read. Remembered, the old version would stay
// on the card until channelVersionTTL.
func TestAPollThatReadThePictureBeforeAnUploadDoesNotRememberTheOldOne(t *testing.T) {
	encode := func(data []byte) []byte {
		stored, err := json.Marshal(storedBrand{ContentType: "image/png", Data: data})
		require.NoError(t, err)

		return stored
	}

	old, replacement := []byte("the old card picture"), []byte("the new card picture")
	key := channelThumbKey("videokey-watts")
	store := map[string][]byte{key: encode(old)}

	p := &Plugin{}
	api := &plugintest.API{}
	p.SetAPI(api)

	// The upload lands in the middle of the poll's read: the poll gets the
	// bytes that were there, and the upload has forgotten the version before
	// the poll gets round to remembering it.
	uploadDuringRead := true
	api.On("KVGet", mock.Anything).Return(
		func(kvKey string) []byte {
			current := store[kvKey]
			if uploadDuringRead && kvKey == key {
				uploadDuringRead = false
				store[kvKey] = encode(replacement)
				p.forgetChannelImageVersion(channelThumbKind, "videokey-watts")
			}

			return current
		},
		func(string) *model.AppError { return nil },
	)

	now := time.Now()
	require.Equal(t, pictureVersion(old), p.channelImageVersion(channelThumbKind, "videokey-watts", now), "that poll saw what was there")
	require.Equal(t, pictureVersion(replacement), p.channelImageVersion(channelThumbKind, "videokey-watts", now), "and the next one is not told it again")
}

func TestASettingsSaveMakesThePicturesBeLookedAtAgain(t *testing.T) {
	p, store := channelPicturePlugin(t)
	now := time.Now()

	require.Empty(t, p.channelImageVersion(channelThumbKind, "videokey-watts", now))

	// Written straight into the store, as another node would, which this one
	// is not told about.
	stored, err := json.Marshal(storedBrand{ContentType: "image/png", Data: pngBytes})
	require.NoError(t, err)
	store[channelThumbKey("videokey-watts")] = stored
	require.Empty(t, p.channelImageVersion(channelThumbKind, "videokey-watts", now), "remembered")

	// A save reaches every node. The same settings, so nothing else moves.
	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("LoadPluginConfiguration", mock.Anything).Run(func(args mock.Arguments) {
		saved, isConfiguration := args.Get(0).(*configuration)
		require.True(t, isConfiguration)
		*saved = *p.getConfiguration()
	}).Return(nil)
	// The Site URL, which the app check's rules are read with on every load.
	api.On("GetConfig").Return(&model.Config{}).Maybe()

	// Every setting saved, as the console stores them, so no manifest default
	// fills anything in.
	before := p.getConfiguration()
	current, err := settingsMap(before)
	require.NoError(t, err)
	settings := map[string]any{}
	for key, value := range current {
		settings[strings.ToLower(key)] = value
	}
	api.On("GetUnsanitizedConfig").Return(&model.Config{PluginSettings: model.PluginSettings{
		Plugins: map[string]map[string]any{manifestID: settings},
	}})

	require.NoError(t, p.OnConfigurationChange())
	require.Equal(t, before, p.getConfiguration(), "nothing else moved")
	require.Equal(t, pictureVersion(pngBytes), p.channelImageVersion(channelThumbKind, "videokey-watts", now))
}
