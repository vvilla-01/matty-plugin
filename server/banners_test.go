package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Profile banners (banners.go). Everything here goes through ServeHTTP, as
// Mattermost would call it, on a directory of the test's own.

// People with ids as Mattermost makes them, which is all a banner is ever kept
// under, beside the access scene's six.
const (
	bannerMember = "bannermember00000000000001"
	bannerLetIn  = "bannerletin000000000000001"
	bannerLinked = "bannerlinked00000000000001"
	bannerGuest  = "bannerguest000000000000001"
	bannerBot    = "bannerbot00000000000000001"
)

// bannerScene is the access scene with banners kept in a directory of its own,
// somebody of each kind, and a republish that can be seen.
func bannerScene(t *testing.T) (*accessScene, string) {
	t.Helper()

	s := newAccessScene(t)

	s.lock.Lock()
	s.users[bannerMember] = &model.User{Id: bannerMember, Username: "member", Roles: model.SystemUserRoleId}
	s.users[bannerLetIn] = &model.User{Id: bannerLetIn, Username: "letin", Roles: model.SystemUserRoleId}
	s.users[bannerLinked] = &model.User{Id: bannerLinked, Username: "linked", Roles: model.SystemUserRoleId}
	s.users[bannerGuest] = &model.User{Id: bannerGuest, Username: "guest", Roles: model.SystemGuestRoleId}
	s.users[bannerBot] = &model.User{Id: bannerBot, Username: "bannerbot", IsBot: true, Roles: model.SystemUserRoleId}
	s.lock.Unlock()

	s.faults.lock.Lock()
	linkForTest(s.store, bannerLinked, "videokey-linked")
	s.faults.lock.Unlock()
	_, err := s.p.letIn(bannerLetIn, sceneLockedAdmin, time.Now())
	require.NoError(t, err)

	dir := filepath.Join(t.TempDir(), bannersDir)
	s.p.banners().setDir(dir, "")
	s.p.rosterCh = make(chan struct{}, 1)
	s.api.On("LogInfo", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	s.api.On("LogInfo", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	s.api.On("LogWarn", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	s.api.On("LogWarn", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	return s, dir
}

// republished is whether a republish was asked for since the last look.
func republished(s *accessScene) bool {
	select {
	case <-s.p.rosterCh:
		return true
	default:
		return false
	}
}

func uploadBanner(s *accessScene, userID, kind string, data []byte) *httptest.ResponseRecorder {
	return s.serve(http.MethodPost, "/api/v1/me/banner", userID, string(data), map[string]string{
		"Content-Type": kind, "X-Requested-With": "XMLHttpRequest",
	})
}

func bannerList(t *testing.T, s *accessScene) bannersView {
	t.Helper()

	response := s.serve(http.MethodGet, "/api/v1/banners", sceneLocked, "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var list bannersView
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))

	return list
}

func bannerPicture(s *accessScene, userID, version string) *httptest.ResponseRecorder {
	return s.serve(http.MethodGet, "/api/v1/banner?user="+userID+"&v="+version, sceneLocked, "", nil)
}

func bannerPNG(t *testing.T, width, height int) []byte {
	t.Helper()

	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	picture.Set(1, 1, color.RGBA{R: uint8(width), G: uint8(height), A: 255})

	var out bytes.Buffer
	require.NoError(t, png.Encode(&out, picture))

	return out.Bytes()
}

func bannerJPEG(t *testing.T, width, height int) []byte {
	t.Helper()

	var out bytes.Buffer
	require.NoError(t, jpeg.Encode(&out, image.NewRGBA(image.Rect(0, 0, width, height)), nil))

	return out.Bytes()
}

// bannerGIF is an animation of three frames.
func bannerGIF(t *testing.T, width, height int) []byte {
	t.Helper()

	animation := &gif.GIF{Config: image.Config{Width: width, Height: height, ColorModel: color.Palette(palette.Plan9)}}
	for frame := range 3 {
		picture := image.NewPaletted(image.Rect(0, 0, width, height), palette.Plan9)
		picture.SetColorIndex(frame, frame, uint8(frame+1))
		animation.Image = append(animation.Image, picture)
		animation.Delay = append(animation.Delay, 10)
	}

	var out bytes.Buffer
	require.NoError(t, gif.EncodeAll(&out, animation))

	return out.Bytes()
}

// padded is a picture with nothing after its header but filler, to exactly
// this many bytes: the server decodes only the header, so the size is all
// that is being tested.
func padded(data []byte, size int) []byte {
	return append(append([]byte(nil), data...), make([]byte, size-len(data))...)
}

// Who may do what, through the gate and the handlers both.
func TestBannerRoutesWhoMay(t *testing.T) {
	s, _ := bannerScene(t)
	picture := bannerPNG(t, 300, 100)

	// Nobody signed in is refused everything.
	for _, call := range []struct{ method, target string }{
		{http.MethodGet, "/api/v1/banners"},
		{http.MethodGet, "/api/v1/banner?user=" + bannerMember + "&v=0123456789abcdef"},
		{http.MethodPost, "/api/v1/me/banner"},
		{http.MethodDelete, "/api/v1/me/banner"},
		{http.MethodDelete, "/api/v1/admin/banner?user=" + bannerMember},
	} {
		requireRefused(t, s.serve(call.method, call.target, "", "", nil), http.StatusUnauthorized, codeSignedOut, call.method+" "+call.target)
	}

	// Everybody who is a regular member may set one, whatever their access
	// to the panel: locked, let in or linked. Being a system admin is not
	// asked about.
	for _, userID := range []string{bannerMember, bannerLetIn, bannerLinked} {
		response := uploadBanner(s, userID, bannerTypePNG, picture)
		require.Equal(t, http.StatusCreated, response.Code, "%s: %s", userID, response.Body.String())
	}

	// A guest and a bot may not, and nothing is kept for them.
	for _, userID := range []string{bannerGuest, bannerBot} {
		requireRefused(t, uploadBanner(s, userID, bannerTypePNG, picture), http.StatusForbidden, codeForbidden, userID)
	}

	// Everybody reads the list and the pictures, the guest and the bot
	// included, and the locked; and anybody may take their own away.
	list := bannerList(t, s)
	require.Len(t, list.People, 3)
	require.NotContains(t, list.People, bannerGuest)
	require.NotContains(t, list.People, bannerBot)
	for _, caller := range []string{bannerGuest, bannerBot, sceneLocked, sceneLetIn, sceneLinked} {
		require.Equal(t, http.StatusOK, s.serve(http.MethodGet, "/api/v1/banners", caller, "", nil).Code, caller)
		require.Equal(t, http.StatusOK,
			s.serve(http.MethodGet, "/api/v1/banner?user="+bannerMember+"&v="+list.People[bannerMember].V, caller, "", nil).Code, caller)
		require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/me/banner", caller, "", nil).Code, caller)
	}

	// Only a system admin takes somebody else's away, locked or linked.
	requireRefused(t, s.serve(http.MethodDelete, "/api/v1/admin/banner?user="+bannerMember, sceneLinked, "", nil),
		http.StatusForbidden, codeForbidden, "not an admin")
	require.Contains(t, bannerList(t, s).People, bannerMember)
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/admin/banner?user="+bannerMember, sceneLockedAdmin, "", nil).Code)
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/admin/banner?user="+bannerLetIn, sceneLinkedAdmin, "", nil).Code)
	require.Equal(t, []string{bannerLinked}, keysOf(bannerList(t, s).People))

	// A method nobody listed is for linked people, as on every route, and
	// only then not allowed.
	requireRefused(t, s.serve(http.MethodPut, "/api/v1/me/banner", bannerMember, "", nil), http.StatusForbidden, accessCodeLocked, "PUT, locked")
	requireRefused(t, s.serve(http.MethodPut, "/api/v1/me/banner", bannerLinked, "", nil), http.StatusMethodNotAllowed, codeMethodNotAllowed, "PUT")
	requireRefused(t, s.serve(http.MethodPost, "/api/v1/banners", bannerLinked, "", nil), http.StatusMethodNotAllowed, codeMethodNotAllowed, "POST list")
	requireRefused(t, s.serve(http.MethodPost, "/api/v1/banner", bannerLinked, "", nil), http.StatusMethodNotAllowed, codeMethodNotAllowed, "POST picture")
	requireRefused(t, s.serve(http.MethodGet, "/api/v1/admin/banner?user="+bannerMember, sceneLockedAdmin, "", nil),
		http.StatusMethodNotAllowed, codeMethodNotAllowed, "GET admin")
	requireRefused(t, s.serve(http.MethodDelete, "/api/v1/admin/banner?user=../etc", sceneLockedAdmin, "", nil),
		http.StatusBadRequest, codeBadRequest, "a user that is no id")
}

func keysOf(people map[string]bannerEntry) []string {
	keys := make([]string, 0, len(people))
	for key := range people {
		keys = append(keys, key)
	}

	return keys
}

// A JPEG, a PNG and an animated GIF are each kept and served back exactly.
func TestBannersAreServedBackByteForByte(t *testing.T) {
	s, dir := bannerScene(t)

	for _, c := range []struct {
		kind    string
		picture []byte
	}{
		{bannerTypeJPEG, bannerJPEG(t, 640, 200)},
		{bannerTypePNG, bannerPNG(t, 640, 200)},
		{bannerTypeGIF, bannerGIF(t, 640, 200)},
	} {
		kind, picture := c.kind, c.picture

		response := uploadBanner(s, bannerMember, kind, picture)
		require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

		var set bannerEntry
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &set))
		require.Equal(t, kind, set.Type)
		require.Regexp(t, `^[0-9a-f]{12,16}$`, set.V)

		require.Equal(t, set, bannerList(t, s).People[bannerMember])

		served := bannerPicture(s, bannerMember, set.V)
		require.Equal(t, http.StatusOK, served.Code)
		require.Equal(t, picture, served.Body.Bytes(), kind)
		require.Equal(t, kind, served.Header().Get("Content-Type"))
		require.Equal(t, "nosniff", served.Header().Get("X-Content-Type-Options"))
		require.Equal(t, "default-src 'none'; sandbox", served.Header().Get("Content-Security-Policy"))
		require.Equal(t, "private, max-age=31536000, immutable", served.Header().Get("Cache-Control"))
		require.Empty(t, served.Header().Get("Content-Disposition"))

		// One picture a person: the one before has gone from the disk.
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)
		require.Len(t, entries, 1, kind)
	}

	// A GIF is kept as it came: every frame.
	animation, err := gif.DecodeAll(bytes.NewReader(bannerPicture(s, bannerMember, bannerList(t, s).People[bannerMember].V).Body.Bytes()))
	require.NoError(t, err)
	require.Len(t, animation.Image, 3)
}

// The server's own words for every refusal of an upload, and none of them
// keeps anything.
func TestBannerUploadRefusals(t *testing.T) {
	s, dir := bannerScene(t)
	pngBanner := bannerPNG(t, 300, 100)

	// Every refusal below but the meter's is about the picture, and each
	// spends a token: the meter is held open until its own turn.
	s.p.banners().limiter = newRateLimiter(1000, time.Minute)

	for _, c := range []struct {
		about, kind string
		data        []byte
	}{
		{"a text file named as a picture", bannerTypePNG, []byte(strings.Repeat("not a picture at all ", 20))},
		{"a PNG sent as a JPEG", bannerTypeJPEG, pngBanner},
		{"a JPEG sent as a GIF", bannerTypeGIF, bannerJPEG(t, 300, 100)},
		{"a type that is none of the three", "image/webp", pngBanner},
		{"no type at all", "", pngBanner},
		{"a truncated header", bannerTypePNG, pngBanner[:20]},
		{"a truncated GIF header", bannerTypeGIF, bannerGIF(t, 300, 100)[:12]},
		{"too narrow", bannerTypePNG, bannerPNG(t, 63, 100)},
		{"too short", bannerTypeJPEG, bannerJPEG(t, 300, 63)},
		{"too wide", bannerTypePNG, bannerPNG(t, 2401, 100)},
		{"too tall", bannerTypeGIF, bannerGIF(t, 100, 2401)},
	} {
		response := uploadBanner(s, bannerMember, c.kind, c.data)
		requireRefused(t, response, http.StatusBadRequest, codeBadImage, c.about)
		require.Equal(t, bannerBadImageText, refusalOf(t, response).Error, c.about)
	}

	// The sides at their limits are fine.
	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypePNG, bannerPNG(t, 64, 2400)).Code)
	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerLinked, bannerTypePNG, bannerPNG(t, 2400, 64)).Code)
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/me/banner", bannerMember, "", nil).Code)
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/me/banner", bannerLinked, "", nil).Code)

	// The limits: exactly each is fine, a byte more is too large.
	for _, c := range []struct {
		kind    string
		picture []byte
		limit   int
		userID  string
	}{
		{bannerTypeJPEG, bannerJPEG(t, 300, 100), 2 << 20, bannerMember},
		{bannerTypePNG, pngBanner, 2 << 20, bannerLetIn},
		{bannerTypeGIF, bannerGIF(t, 300, 100), 8 << 20, bannerLinked},
	} {
		requireRefused(t, uploadBanner(s, c.userID, c.kind, padded(c.picture, c.limit+1)), http.StatusRequestEntityTooLarge, codeTooLarge, c.kind)
		require.Equal(t, http.StatusCreated, uploadBanner(s, c.userID, c.kind, padded(c.picture, c.limit)).Code, c.kind)
	}

	// With no length said, what is read stops a byte past the limit.
	body := &countingReader{left: maxBannerGIFBytes + 4096}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/me/banner", body)
	request.ContentLength = -1
	request.Header.Set("Mattermost-User-Id", bannerMember)
	request.Header.Set("Content-Type", bannerTypePNG)
	recorder := httptest.NewRecorder()
	s.p.ServeHTTP(nil, recorder, request)
	requireRefused(t, recorder, http.StatusRequestEntityTooLarge, codeTooLarge, "unsaid length")
	require.Equal(t, maxBannerBytes+1, body.read)

	// The seventh upload in ten minutes is refused, and says how long until
	// another can go: one of the six back, a hundred seconds at most.
	s.p.banners().limiter = newRateLimiter(bannerUploads, bannerUploadWindow)
	for range 6 {
		require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypePNG, pngBanner).Code)
	}
	limited := uploadBanner(s, bannerMember, bannerTypePNG, pngBanner)
	requireRefused(t, limited, http.StatusTooManyRequests, codeRateLimited, "seventh")
	wait := refusalOf(t, limited).RetryAfterMs
	require.Positive(t, wait)
	require.LessOrEqual(t, wait, int64(100_000))
	require.Equal(t, bannerTooManyText, refusalOf(t, limited).Error)

	// Nothing refused was kept.
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	require.Len(t, names, 3, "the three at their limits, one of them replaced by the meter's: %v", names)
}

// countingReader is a body of zeros that counts what is read of it.
type countingReader struct {
	left, read int
}

func (c *countingReader) Read(buffer []byte) (int, error) {
	if c.left == 0 {
		return 0, io.EOF
	}

	n := min(len(buffer), c.left)
	clear(buffer[:n])
	c.left -= n
	c.read += n

	return n, nil
}

// Replacing, taking away and an old version; the list's tag; and the
// snapshot's word, which moves with every change and with nothing else, and
// goes out when it does.
func TestBannerListAndTheSnapshotsWord(t *testing.T) {
	s, dir := bannerScene(t)
	s.api.On("PublishWebSocketEvent", wsEventStreams, mock.Anything, mock.Anything).Return().Maybe()

	empty := bannerList(t, s)
	require.Equal(t, map[string]bannerEntry{}, empty.People)
	require.Equal(t, empty.Version, s.p.banners().word().Version)

	// Taking away what is not there writes nothing, and tells nobody.
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/me/banner", bannerMember, "", nil).Code)
	require.JSONEq(t, `{"ok":true}`, s.serve(http.MethodDelete, "/api/v1/me/banner", bannerMember, "", nil).Body.String())
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/admin/banner?user="+bannerMember, sceneLockedAdmin, "", nil).Code)
	_, err := os.Stat(dir)
	require.ErrorIs(t, err, os.ErrNotExist, "not even the directory is made")
	require.False(t, republished(s))
	require.Equal(t, empty.Version, s.p.banners().word().Version)

	// Set: the word moves, and the snapshot goes out again with it.
	first := uploadBanner(s, bannerMember, bannerTypePNG, bannerPNG(t, 300, 100))
	require.Equal(t, http.StatusCreated, first.Code)
	var firstSet bannerEntry
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstSet))

	set := bannerList(t, s)
	require.NotEqual(t, empty.Version, set.Version)
	require.Equal(t, set.Version, s.p.banners().word().Version)
	require.True(t, republished(s))

	s.p.publishRosterNow()
	require.Equal(t, set.Version, s.p.getSnapshot().Banners.Version, "the republish carries the new word")
	encoded, err := json.Marshal(s.p.getSnapshot())
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"banners":{"version":"`+set.Version+`"}`)
	require.NotContains(t, string(encoded), bannerMember, "nothing else about banners rides the snapshot")

	// The list is conditional.
	tagged := s.serve(http.MethodGet, "/api/v1/banners", sceneLocked, "", nil)
	require.Equal(t, `"`+set.Version+`"`, tagged.Header().Get("ETag"))
	require.Equal(t, "private, no-cache", tagged.Header().Get("Cache-Control"))
	held := map[string]string{"If-None-Match": tagged.Header().Get("ETag")}
	require.Equal(t, http.StatusNotModified, s.serve(http.MethodGet, "/api/v1/banners", sceneLetIn, "", held).Code)

	// The same picture again changes nothing.
	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypePNG, bannerPNG(t, 300, 100)).Code)
	require.Equal(t, set.Version, s.p.banners().word().Version)
	require.False(t, republished(s))
	require.Equal(t, http.StatusNotModified, s.serve(http.MethodGet, "/api/v1/banners", sceneLetIn, "", held).Code)

	// Replace: the word moves, the old picture is gone from the disk and its
	// address answers 404, and the tag held is no longer the list's.
	second := uploadBanner(s, bannerMember, bannerTypeGIF, bannerGIF(t, 300, 100))
	require.Equal(t, http.StatusCreated, second.Code)
	var secondSet bannerEntry
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &secondSet))
	require.NotEqual(t, firstSet.V, secondSet.V)

	replaced := bannerList(t, s)
	require.NotEqual(t, set.Version, replaced.Version)
	require.Equal(t, secondSet, replaced.People[bannerMember])
	require.True(t, republished(s))
	require.Equal(t, http.StatusOK, s.serve(http.MethodGet, "/api/v1/banners", sceneLetIn, "", held).Code)

	requireRefused(t, bannerPicture(s, bannerMember, firstSet.V), http.StatusNotFound, codeNotFound, "the old version")
	require.Equal(t, http.StatusOK, bannerPicture(s, bannerMember, secondSet.V).Code)
	_, err = os.Stat(filepath.Join(dir, bannerMember+"-"+firstSet.V+".png"))
	require.ErrorIs(t, err, os.ErrNotExist)

	// Addresses that are no banner are 404, and never reach the disk as
	// anything but a name this file made.
	for _, target := range []string{
		"/api/v1/banner?user=" + bannerLinked + "&v=" + secondSet.V,
		"/api/v1/banner?user=../" + bannerMember + "&v=" + secondSet.V,
		"/api/v1/banner?user=" + bannerMember + "&v=../../x",
		"/api/v1/banner?user=" + strings.ToUpper(bannerMember) + "&v=" + secondSet.V,
		"/api/v1/banner",
	} {
		requireRefused(t, s.serve(http.MethodGet, target, sceneLocked, "", nil), http.StatusNotFound, codeNotFound, target)
	}

	// Remove: the word moves again, back to the empty list's.
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/me/banner", bannerMember, "", nil).Code)
	require.Equal(t, empty.Version, s.p.banners().word().Version)
	require.True(t, republished(s))
	requireRefused(t, bannerPicture(s, bannerMember, secondSet.V), http.StatusNotFound, codeNotFound, "taken away")
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)

	// A refused upload moves nothing.
	requireRefused(t, uploadBanner(s, bannerMember, bannerTypePNG, []byte("nope")), http.StatusBadRequest, codeBadImage, "refused")
	require.Equal(t, empty.Version, s.p.banners().word().Version)
	require.False(t, republished(s))
}

// Another node sharing the directory sees a change within bannerListTTL.
func TestAnotherNodeSeesABannerWithinItsListTime(t *testing.T) {
	s, dir := bannerScene(t)

	other := &Plugin{}
	at := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	other.banners().now = func() time.Time { return at }
	other.banners().setDir(dir, "")
	before := other.banners().word()

	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypePNG, bannerPNG(t, 300, 100)).Code)
	require.Equal(t, before, other.banners().word(), "believed until the list is old")

	at = at.Add(bannerListTTL)
	require.Equal(t, s.p.banners().word(), other.banners().word())
}

// A server whose files are not on its own disk keeps no banners, lists nobody
// and says so in the store's words.
func TestBannersNeedTheLocalFileDriver(t *testing.T) {
	s, _ := bannerScene(t)
	s.p.banners().setDir("", "Mattermost's File Storage System is not Local file system")

	refused := uploadBanner(s, bannerMember, bannerTypePNG, bannerPNG(t, 300, 100))
	requireRefused(t, refused, http.StatusServiceUnavailable, accessCodeStore, "no directory")
	require.Equal(t, bannerStoreText, refusalOf(t, refused).Error)

	require.Equal(t, map[string]bannerEntry{}, bannerList(t, s).People)
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/me/banner", bannerMember, "", nil).Code)
}

// rawGIF is a GIF written by hand, so that it can say what no encoder would:
// a logical screen of these sides, then one image descriptor for each frame
// given as {left, top, width, height}, each with a single sub-block of
// compressed data that is never decoded, then the trailer.
func rawGIF(width, height int, frames [][4]int) []byte {
	le := func(value int) []byte { return []byte{byte(value), byte(value >> 8)} }

	// The header, the logical screen with a global table of two colours, and
	// the table.
	out := append([]byte("GIF89a"), le(width)...)
	out = append(out, le(height)...)
	out = append(out, 0x80, 0, 0, 0, 0, 0, 255, 255, 255)
	for _, frame := range frames {
		out = append(out, 0x21, 0xF9, 4, 0, 10, 0, 0, 0) // A delay of a tenth of a second.
		out = append(out, 0x2C)
		for _, value := range frame {
			out = append(out, le(value)...)
		}
		out = append(out, 0, 2, 2, 0x4C, 0x01, 0)
	}

	return append(out, 0x3B)
}

// fullFrames is this many frames, each covering the whole screen.
func fullFrames(count, width, height int) [][4]int {
	frames := make([][4]int, count)
	for i := range frames {
		frames[i] = [4]int{0, 0, width, height}
	}

	return frames
}

// An animated GIF is held to a number of frames and of pixels, each frame
// inside its screen, and a structure that reaches its trailer, all found
// without decoding a frame; and refused, if it is not, as any other picture
// that is not a banner is.
func TestBannerGIFBudget(t *testing.T) {
	// The written GIFs pass through the encoder's own, so a real one passes
	// and so does the most of each budget.
	require.True(t, checkBanner(bannerGIF(t, 640, 200), bannerTypeGIF))
	require.True(t, checkBanner(rawGIF(64, 64, fullFrames(maxBannerGIFFrames, 64, 64)), bannerTypeGIF))
	largest := maxBannerGIFPixels / (2400 * 2400)
	require.True(t, checkBanner(rawGIF(2400, 2400, fullFrames(largest, 2400, 2400)), bannerTypeGIF))
	require.True(t, checkBanner(rawGIF(300, 100, [][4]int{{200, 50, 100, 50}}), bannerTypeGIF), "a frame at the screen's corner")

	// One frame too many, from the real encoder.
	tooMany := &gif.GIF{Config: image.Config{Width: 64, Height: 64, ColorModel: color.Palette(palette.Plan9)}}
	for range maxBannerGIFFrames + 1 {
		tooMany.Image = append(tooMany.Image, image.NewPaletted(image.Rect(0, 0, 64, 64), palette.Plan9))
		tooMany.Delay = append(tooMany.Delay, 2)
	}
	var encoded bytes.Buffer
	require.NoError(t, gif.EncodeAll(&encoded, tooMany))

	whole := rawGIF(300, 100, fullFrames(3, 300, 100))
	s, _ := bannerScene(t)
	s.p.banners().limiter = newRateLimiter(1000, time.Minute)
	for _, c := range []struct {
		about string
		data  []byte
	}{
		{"one frame too many", encoded.Bytes()},
		{"a frame past the screen's right", rawGIF(300, 100, [][4]int{{1, 0, 300, 100}})},
		{"a frame past the screen's bottom", rawGIF(300, 100, [][4]int{{0, 0, 300, 101}})},
		{"an empty frame", rawGIF(300, 100, [][4]int{{0, 0, 0, 100}})},
		{"more pixels than the budget", rawGIF(2400, 2400, fullFrames(largest+1, 2400, 2400))},
		{"no frame at all", rawGIF(300, 100, nil)},
		{"no trailer", whole[:len(whole)-1]},
		{"cut inside a frame's data", whole[:len(whole)-4]},
		{"cut inside a descriptor", whole[:len(whole)-12]},
		{"a block of no known kind", append(whole[:len(whole)-1:len(whole)-1], 0x00, 0x3B)},
	} {
		require.False(t, checkBanner(c.data, bannerTypeGIF), c.about)
		response := uploadBanner(s, bannerMember, bannerTypeGIF, c.data)
		requireRefused(t, response, http.StatusBadRequest, codeBadImage, c.about)
		require.Equal(t, bannerBadImageText, refusalOf(t, response).Error, c.about)
	}

	// The walk allocates nothing, so what the frames would decode to costs
	// the server nothing: only the file's length is walked.
	budget := rawGIF(2400, 2400, fullFrames(largest, 2400, 2400))
	require.Zero(t, testing.AllocsPerRun(10, func() { gifWithinBudget(budget, 2400, 2400) }))
}

// A list read from the directory before a change on this node is not kept as
// the list after it: the next look reads again, and sees the change.
func TestABannerListReadBeforeAChangeIsNotKept(t *testing.T) {
	s, dir := bannerScene(t)
	store := s.p.banners()
	before := store.word()

	// The upload lands while a read of the directory is under way.
	store.forget()
	store.afterRead = func() {
		store.afterRead = nil
		_, changed, err := store.put(dir, bannerMember, bannerTypePNG, bannerPNG(t, 300, 100))
		require.NoError(t, err)
		require.True(t, changed)
	}
	require.Equal(t, before, store.word(), "the read under way may answer with the list from before")

	after := store.word()
	require.NotEqual(t, before, after, "but the list from before is not believed afterwards")
	require.Contains(t, bannerList(t, s).People, bannerMember)

	// And the same for a takeback.
	store.afterRead = func() {
		store.afterRead = nil
		removed, err := store.remove(dir, bannerMember)
		require.NoError(t, err)
		require.True(t, removed)
	}
	store.forget()
	require.Equal(t, after, store.word())
	require.Equal(t, before, store.word())
}

// An old picture that cannot be taken away does not make the new one fail:
// the upload is a success, the list moves at once and the snapshot goes out,
// the old picture is never served, and the next change clears it away.
func TestABannerWhoseOldPictureStaysIsStillSet(t *testing.T) {
	s, dir := bannerScene(t)
	store := s.p.banners()

	first := uploadBanner(s, bannerMember, bannerTypePNG, bannerPNG(t, 300, 100))
	require.Equal(t, http.StatusCreated, first.Code)
	var firstSet bannerEntry
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstSet))
	require.True(t, republished(s))
	require.Equal(t, firstSet, bannerList(t, s).People[bannerMember], "the list is read, and believed")

	store.removeFile = func(string) error { return os.ErrPermission }
	second := uploadBanner(s, bannerMember, bannerTypeGIF, bannerGIF(t, 300, 100))
	require.Equal(t, http.StatusCreated, second.Code, second.Body.String())
	var secondSet bannerEntry
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &secondSet))
	require.Equal(t, secondSet, bannerList(t, s).People[bannerMember])
	require.True(t, republished(s))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 2, "the old picture is left behind")
	requireRefused(t, bannerPicture(s, bannerMember, firstSet.V), http.StatusNotFound, codeNotFound, "the old picture")
	require.Equal(t, http.StatusOK, bannerPicture(s, bannerMember, secondSet.V).Code)

	// The same picture again, once the old one can go, takes it away.
	store.removeFile = os.Remove
	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypeGIF, bannerGIF(t, 300, 100)).Code)
	entries, err = os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

// GET /banner serves only somebody's banner now, even while an older picture
// of theirs is on the disk; and a picture another node has just put in place
// is served before this node's list has caught up with it.
func TestOnlyTheCurrentBannerIsServed(t *testing.T) {
	s, dir := bannerScene(t)
	require.NoError(t, os.MkdirAll(dir, bannerDirMode))

	older, newer := bannerPNG(t, 300, 100), bannerPNG(t, 400, 100)
	olderV, newerV := shortSum(older), shortSum(newer)
	at := time.Now()
	for _, c := range []struct {
		version string
		data    []byte
		when    time.Time
	}{
		{olderV, older, at.Add(-time.Hour)},
		{newerV, newer, at},
	} {
		path := filepath.Join(dir, bannerMember+"-"+c.version+".png")
		require.NoError(t, os.WriteFile(path, c.data, 0o600))
		require.NoError(t, os.Chtimes(path, c.when, c.when))
	}

	require.Equal(t, newerV, bannerList(t, s).People[bannerMember].V)
	requireRefused(t, bannerPicture(s, bannerMember, olderV), http.StatusNotFound, codeNotFound, "the older picture")
	served := bannerPicture(s, bannerMember, newerV)
	require.Equal(t, http.StatusOK, served.Code)
	require.Equal(t, newer, served.Body.Bytes())

	// Another node's upload, which this node's list does not know of yet.
	other := bannerPNG(t, 500, 100)
	otherV := shortSum(other)
	path := filepath.Join(dir, bannerLinked+"-"+otherV+".png")
	require.NoError(t, os.WriteFile(path, other, 0o600))
	require.NotContains(t, bannerList(t, s).People, bannerLinked, "this node's list is believed for a while")
	require.Equal(t, http.StatusOK, bannerPicture(s, bannerLinked, otherV).Code)
}

// The part of a banner that shows (contract/README.md, "Profile banners"):
// kept in the picture's own name, set with an upload or changed alone.

func uploadCropped(s *accessScene, userID, kind string, data []byte, crop string) *httptest.ResponseRecorder {
	return s.serve(http.MethodPost, "/api/v1/me/banner?crop="+crop, userID, string(data), map[string]string{
		"Content-Type": kind, "X-Requested-With": "XMLHttpRequest",
	})
}

func putCrop(s *accessScene, userID, body string) *httptest.ResponseRecorder {
	return s.serve(http.MethodPut, "/api/v1/me/banner/crop", userID, body, map[string]string{
		"Content-Type": "application/json", "X-Requested-With": "XMLHttpRequest",
	})
}

// entryOf is the banner an answer says.
func entryOf(t *testing.T, response *httptest.ResponseRecorder) bannerEntry {
	t.Helper()

	var entry bannerEntry
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &entry), response.Body.String())

	return entry
}

// bannerNames is every name in the banners' directory.
func bannerNames(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names
}

// Every rule of what a crop may be, with the edge of each bound: as the
// upload's query says it, as a change's body says it, and measured on a
// picture three times as wide as it is high, so that a crop's shape on the
// picture is three times its w over its h.
func TestWhatABannersCropMayBe(t *testing.T) {
	for _, c := range []struct {
		about, numbers string
		want           bannerCrop
		ok, fits       bool
	}{
		{"the whole picture", "0,0,1,1", bannerCrop{0, 0, 10000, 10000}, true, true},
		{"four places, trailing zeros", "0.0000,0.0000,1.0000,1.0000", bannerCrop{0, 0, 10000, 10000}, true, true},
		{"the right half", "0.5,0,0.5,0.5", bannerCrop{5000, 0, 5000, 5000}, true, true},
		{"x+w one past the edge, for rounding", "0.3334,0,0.6667,0.6667", bannerCrop{3334, 0, 6667, 6667}, true, true},
		{"y+h one past the edge, for rounding", "0,0.3334,0.6667,0.6667", bannerCrop{0, 3334, 6667, 6667}, true, true},
		{"the smallest sides", "0,0,0.05,0.05", bannerCrop{0, 0, 500, 500}, true, true},
		{"exactly two and a half to one", "0,0,0.5,0.6", bannerCrop{0, 0, 5000, 6000}, true, true},
		{"exactly three and a half to one", "0,0,0.7,0.6", bannerCrop{0, 0, 7000, 6000}, true, true},
		{"just under two and a half to one", "0,0,0.4999,0.6", bannerCrop{0, 0, 4999, 6000}, true, false},
		{"just over three and a half to one", "0,0,0.7001,0.6", bannerCrop{0, 0, 7001, 6000}, true, false},

		{"x+w two past the edge", "0.3335,0,0.6667,0.6667", bannerCrop{}, false, false},
		{"y+h two past the edge", "0,0.3335,0.6667,0.6667", bannerCrop{}, false, false},
		{"w under a twentieth", "0,0,0.0499,0.05", bannerCrop{}, false, false},
		{"h under a twentieth", "0,0,0.05,0.0499", bannerCrop{}, false, false},
		{"a number over 1", "1.0001,0,0.5,0.5", bannerCrop{}, false, false},
		{"a number of 2", "2,0,0.5,0.5", bannerCrop{}, false, false},
		{"a negative zero", "-0,0,1,1", bannerCrop{}, false, false},
		{"a plus sign", "+0,0,1,1", bannerCrop{}, false, false},
		{"a leading point", ".5,0,0.5,0.5", bannerCrop{}, false, false},
		{"a trailing point", "0.,0,1,1", bannerCrop{}, false, false},
		{"five places", "0.12345,0,0.5,0.5", bannerCrop{}, false, false},
		{"an exponent", "1e0,0,1,1", bannerCrop{}, false, false},
		{"a capital exponent", "0,0,1E0,1", bannerCrop{}, false, false},
		{"NaN", "NaN,0,1,1", bannerCrop{}, false, false},
		{"Infinity", "0,0,Infinity,1", bannerCrop{}, false, false},
		{"hexadecimal", "0x0,0,1,1", bannerCrop{}, false, false},
		{"three numbers", "0,0,1", bannerCrop{}, false, false},
		{"five numbers", "0,0,1,1,0", bannerCrop{}, false, false},
		{"a trailing comma", "0,0,1,1,", bannerCrop{}, false, false},
		{"nothing", "", bannerCrop{}, false, false},
	} {
		crop, ok := cropFromQuery("crop=" + c.numbers)
		require.Equal(t, c.ok, ok, "query: %s", c.about)

		body, _, bodyOK := cropFromBody([]byte(`{"crop": [` + c.numbers + `]}`))
		require.Equal(t, c.ok, bodyOK, "body: %s", c.about)

		if !c.ok {
			continue
		}
		require.Equal(t, c.want, *crop, c.about)
		require.Equal(t, c.want, *body, c.about)
		require.Equal(t, c.fits, crop.fits(300, 100), c.about)
	}

	// The shape is measured on the picture's pixels: what is three to one on
	// a picture of 300 by 100 is six to one on one of 600 by 100.
	half := bannerCrop{0, 0, 5000, 10000}
	require.False(t, half.fits(300, 100))
	require.True(t, half.fits(600, 100))

	// The query: none named is no crop, a comma may come escaped, the other
	// pairs mean nothing to it, and a crop named twice is refused.
	for raw, want := range map[string]*bannerCrop{
		"":                       nil,
		"x=1":                    nil,
		"crop=0%2C0%2C1%2C1":     {0, 0, 10000, 10000},
		"x=1&crop=0,0,1,1&y=2":   {0, 0, 10000, 10000},
		"cro%70=0.5,0,0.5,0.5":   {5000, 0, 5000, 5000},
		"crop%20=0,0,1,1":        nil,
		"notcrop=0,0,1,1&crop2=": nil,
	} {
		crop, ok := cropFromQuery(raw)
		require.True(t, ok, raw)
		require.Equal(t, want, crop, raw)
	}
	for _, raw := range []string{"crop=0,0,1,1&crop=0,0,1,1", "crop", "crop=", "crop=0,0,1,1%", "crop=0%2C0%2C1%2C1+", "crop=0, 0,1,1", "crop=0,0,1,1 "} {
		_, ok := cropFromQuery(raw)
		require.False(t, ok, raw)
	}

	// The body: the one field, once, and nothing else.
	for raw, want := range map[string]*bannerCrop{
		`{"crop":null}`:                       nil,
		` { "crop" : null } `:                 nil,
		"{\n  \"crop\": [0, 0, 1, 1]\n}\n":    {0, 0, 10000, 10000},
		`{"crop":[0.3334,0,0.6667,0.6667]}`:   {3334, 0, 6667, 6667},
		`{"crop":[0.0001,0.0001,0.05,0.05]} `: {1, 1, 500, 500},
	} {
		crop, _, ok := cropFromBody([]byte(raw))
		require.True(t, ok, raw)
		require.Equal(t, want, crop, raw)
	}
	for _, raw := range []string{
		``, `null`, `{}`, `[0,0,1,1]`, `"crop"`,
		`{"crop":[0,0,1,1],"extra":1}`, `{"extra":1,"crop":[0,0,1,1]}`,
		`{"crop":[0,0,1,1],"crop":null}`, `{"crop":null,"crop":[0,0,1,1]}`,
		`{"Crop":[0,0,1,1]}`, `{"CROP":null}`,
		`{"crop":["0","0","1","1"]}`, `{"crop":[0,0,1,1,]}`, `{"crop":[0,0,[1],1]}`, `{"crop":[]}`,
		`{"crop":true}`, `{"crop":{"x":0}}`, `{"crop":"0,0,1,1"}`, `{"crop":0}`,
		`{"crop":[0,0,1,1]} x`, `{"crop":[0,0,1,1]}{}`, `{"crop":[0,0,1,1]`, `{"crop":[0,0,1,1]}}`,
		`{"crop":[NaN,0,1,1]}`, `{"crop":[0,0,Infinity,1]}`, `{"crop":[1e-1,0,1,1]}`,
	} {
		_, _, ok := cropFromBody([]byte(raw))
		require.False(t, ok, raw)
	}
}

// A crop is written back as it was kept: the shortest plain decimal, at most
// four places, and read back the same.
func TestABannersCropIsWrittenInFourPlaces(t *testing.T) {
	crop := bannerCrop{0, 3333, 10000, 500}
	encoded, err := json.Marshal(bannerEntry{V: "0123456789abcdef", Type: bannerTypePNG, Crop: &crop})
	require.NoError(t, err)
	require.JSONEq(t, `{"v":"0123456789abcdef","type":"image/png","crop":[0,0.3333,1,0.05]}`, string(encoded))
	require.Contains(t, string(encoded), `[0,0.3333,1,0.05]`)

	var back bannerEntry
	require.NoError(t, json.Unmarshal(encoded, &back))
	require.Equal(t, crop, *back.Crop)

	for value, text := range map[int]string{0: "0", 1: "0.0001", 10: "0.001", 1200: "0.12", 9999: "0.9999", 10000: "1"} {
		require.Equal(t, text, cropText(value))
	}

	// None is no field at all, as before.
	encoded, err = json.Marshal(bannerEntry{V: "0123456789abcdef", Type: bannerTypePNG})
	require.NoError(t, err)
	require.JSONEq(t, `{"v":"0123456789abcdef","type":"image/png"}`, string(encoded))
}

// An upload with ?crop= keeps it with the picture, served exactly as it was
// sent; the same picture without one, or with another, is a change; and a
// crop that cannot be one is refused without keeping anything.
func TestABannerUploadWithAndWithoutACrop(t *testing.T) {
	s, dir := bannerScene(t)
	s.api.On("PublishWebSocketEvent", wsEventStreams, mock.Anything, mock.Anything).Return().Maybe()
	animation := bannerGIF(t, 600, 400)

	// A crop of the top half: two to one in fractions, three to one on a
	// picture three wide by two high.
	response := uploadCropped(s, bannerMember, bannerTypeGIF, animation, "0,0,1,0.5")
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	set := entryOf(t, response)
	require.Equal(t, &bannerCrop{0, 0, 10000, 5000}, set.Crop)
	require.JSONEq(t, `{"v":"`+set.V+`","type":"image/gif","crop":[0,0,1,0.5]}`, response.Body.String())
	require.Equal(t, set, bannerList(t, s).People[bannerMember])
	require.True(t, republished(s))
	require.Equal(t, []string{bannerMember + "-" + set.V + "-0-0-10000-5000.gif"}, bannerNames(t, dir))

	served := bannerPicture(s, bannerMember, set.V)
	require.Equal(t, http.StatusOK, served.Code)
	require.Equal(t, animation, served.Body.Bytes(), "the GIF as it was sent, whatever its crop")

	// The same again changes nothing.
	withCrop := bannerList(t, s).Version
	require.Equal(t, http.StatusCreated, uploadCropped(s, bannerMember, bannerTypeGIF, animation, "0,0,1,0.5").Code)
	require.False(t, republished(s))
	require.Equal(t, withCrop, s.p.banners().word().Version)

	// The same picture sent without one has none: the version of the
	// picture stays, the list's moves.
	plain := uploadBanner(s, bannerMember, bannerTypeGIF, animation)
	require.Equal(t, http.StatusCreated, plain.Code)
	require.JSONEq(t, `{"v":"`+set.V+`","type":"image/gif"}`, plain.Body.String())
	require.Nil(t, bannerList(t, s).People[bannerMember].Crop)
	require.NotEqual(t, withCrop, bannerList(t, s).Version)
	require.True(t, republished(s))
	require.Equal(t, []string{bannerMember + "-" + set.V + ".gif"}, bannerNames(t, dir))

	// A still picture may be sent with one too.
	still := uploadCropped(s, bannerLinked, bannerTypePNG, bannerPNG(t, 300, 100), "0.25,0.25,0.5,0.5")
	require.Equal(t, http.StatusCreated, still.Code, still.Body.String())
	require.Equal(t, &bannerCrop{2500, 2500, 5000, 5000}, bannerList(t, s).People[bannerLinked].Crop)
	require.True(t, republished(s))

	// What a crop can be is read before the meter: a refusal of it spends
	// nothing. A shape that does not fit the picture is refused once the
	// picture is read, which is an upload like any other refused.
	before := bannerList(t, s)
	s.p.banners().limiter = newRateLimiter(1, bannerUploadWindow)
	for _, crop := range []string{"0,0,1", "1e0,0,1,1", "0,0,1,1&crop=0,0,1,1", "NaN,0,1,1"} {
		refused := uploadCropped(s, bannerLetIn, bannerTypePNG, bannerPNG(t, 300, 100), crop)
		requireRefused(t, refused, http.StatusBadRequest, codeBadCrop, crop)
		require.Equal(t, bannerBadCropText, refusalOf(t, refused).Error)
	}
	refused := uploadCropped(s, bannerLetIn, bannerTypePNG, bannerPNG(t, 600, 100), "0,0,1,1")
	requireRefused(t, refused, http.StatusBadRequest, codeBadCrop, "six to one on the picture")
	requireRefused(t, uploadCropped(s, bannerLetIn, bannerTypePNG, bannerPNG(t, 600, 100), "0,0,0.5,1"),
		http.StatusTooManyRequests, codeRateLimited, "the one token went on the shape")

	// A picture that is no banner is still bad_image, crop or none.
	s.p.banners().limiter = newRateLimiter(1000, time.Minute)
	requireRefused(t, uploadCropped(s, bannerLetIn, bannerTypePNG, []byte("nope"), "0,0,1,1"), http.StatusBadRequest, codeBadImage, "no picture")

	require.Equal(t, before, bannerList(t, s), "nothing refused was kept")
	require.False(t, republished(s))
	require.Len(t, bannerNames(t, dir), 2)
}

// PUT /me/banner/crop: changing the part that shows without sending the
// picture again, and what the list, its tag and the snapshot's word do.
func TestChangingABannersCrop(t *testing.T) {
	s, dir := bannerScene(t)
	s.api.On("PublishWebSocketEvent", wsEventStreams, mock.Anything, mock.Anything).Return().Maybe()

	// With no banner there is nothing to change, and a guest or a bot never
	// has one.
	none := putCrop(s, bannerMember, `{"crop":[0,0,1,1]}`)
	requireRefused(t, none, http.StatusNotFound, codeNotFound, "no banner")
	require.Equal(t, bannerNoneText, refusalOf(t, none).Error)
	requireRefused(t, putCrop(s, bannerMember, `{"crop":null}`), http.StatusNotFound, codeNotFound, "no banner to clear")
	for _, userID := range []string{bannerGuest, bannerBot} {
		requireRefused(t, putCrop(s, userID, `{"crop":[0,0,1,1]}`), http.StatusForbidden, codeForbidden, userID)
	}
	requireRefused(t, s.serve(http.MethodPut, "/api/v1/me/banner/crop", "", `{"crop":null}`, nil), http.StatusUnauthorized, codeSignedOut, "signed out")
	requireRefused(t, s.serve(http.MethodPost, "/api/v1/me/banner/crop", bannerLinked, `{"crop":null}`, nil),
		http.StatusMethodNotAllowed, codeMethodNotAllowed, "POST")
	require.False(t, republished(s))

	// A picture three wide by two high, sent without a crop.
	animation := bannerGIF(t, 600, 400)
	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypeGIF, animation).Code)
	require.True(t, republished(s))
	plain := bannerList(t, s)
	version := plain.People[bannerMember].V
	tag := map[string]string{"If-None-Match": `"` + plain.Version + `"`}
	require.Equal(t, http.StatusNotModified, s.serve(http.MethodGet, "/api/v1/banners", sceneLetIn, "", tag).Code)

	// Set: the answer is the banner as the list now says it, and the list,
	// its tag and the snapshot's word all move, and the snapshot goes out.
	set := putCrop(s, bannerMember, `{"crop":[0,0.25,1,0.5]}`)
	require.Equal(t, http.StatusOK, set.Code, set.Body.String())
	require.Equal(t, "no-store", set.Header().Get("Cache-Control"))
	require.JSONEq(t, `{"v":"`+version+`","type":"image/gif","crop":[0,0.25,1,0.5]}`, set.Body.String())
	cropped := bannerList(t, s)
	require.Equal(t, entryOf(t, set), cropped.People[bannerMember])
	require.NotEqual(t, plain.Version, cropped.Version)
	require.Equal(t, cropped.Version, s.p.banners().word().Version)
	require.Equal(t, http.StatusOK, s.serve(http.MethodGet, "/api/v1/banners", sceneLetIn, "", tag).Code, "the tag held is not the list's")
	require.True(t, republished(s))
	s.p.publishRosterNow()
	require.Equal(t, cropped.Version, s.p.getSnapshot().Banners.Version, "the republish carries the new word")

	// The picture is the same picture at the same address.
	served := bannerPicture(s, bannerMember, version)
	require.Equal(t, http.StatusOK, served.Code)
	require.Equal(t, animation, served.Body.Bytes())
	require.Equal(t, []string{bannerMember + "-" + version + "-0-2500-10000-5000.gif"}, bannerNames(t, dir))

	// The same crop again changes nothing and tells nobody.
	again := putCrop(s, bannerMember, `{"crop": [0, 0.2500, 1.0, 0.5]}`)
	require.Equal(t, http.StatusOK, again.Code)
	require.JSONEq(t, set.Body.String(), again.Body.String())
	require.Equal(t, cropped.Version, s.p.banners().word().Version)
	require.False(t, republished(s))
	held := map[string]string{"If-None-Match": `"` + cropped.Version + `"`}
	require.Equal(t, http.StatusNotModified, s.serve(http.MethodGet, "/api/v1/banners", sceneLetIn, "", held).Code)

	// Change: moves again.
	require.Equal(t, http.StatusOK, putCrop(s, bannerMember, `{"crop":[0,0.5,1,0.5]}`).Code)
	changed := bannerList(t, s)
	require.Equal(t, &bannerCrop{0, 5000, 10000, 5000}, changed.People[bannerMember].Crop)
	require.NotEqual(t, cropped.Version, changed.Version)
	require.True(t, republished(s))

	// The shape is the stored picture's: the whole of it is three to two,
	// and a crop that is three to one in fractions is four and a half to one
	// on it.
	for _, body := range []string{`{"crop":[0,0,1,1]}`, `{"crop":[0,0,1,0.3333]}`, `{"crop":[0,0,0.05,1]}`, `{"crop":[0,0,1,0.0499]}`, `{"crop":[0.6,0,0.5,0.25]}`, `{"crop":1}`, `{}`} {
		refused := putCrop(s, bannerMember, body)
		requireRefused(t, refused, http.StatusBadRequest, codeBadCrop, body)
		require.Equal(t, bannerBadCropText, refusalOf(t, refused).Error, body)
	}

	// A body past its limit is refused without reading it all.
	long := `{"crop":[0,0.5,1,0.5]` + strings.Repeat(" ", maxBannerCropBody) + `}`
	requireRefused(t, putCrop(s, bannerMember, long), http.StatusBadRequest, codeBadCrop, "a body past the limit")
	request := httptest.NewRequest(http.MethodPut, "/api/v1/me/banner/crop", &countingReader{left: 1 << 20})
	request.ContentLength = -1
	request.Header.Set("Mattermost-User-Id", bannerMember)
	request.Header.Set("X-Requested-With", "XMLHttpRequest")
	recorder := httptest.NewRecorder()
	s.p.ServeHTTP(nil, recorder, request)
	requireRefused(t, recorder, http.StatusBadRequest, codeBadCrop, "unsaid length")
	require.Equal(t, changed, bannerList(t, s), "nothing refused moved anything")
	require.False(t, republished(s))

	// Clear: back to no crop, and clearing none is no change.
	cleared := putCrop(s, bannerMember, `{"crop":null}`)
	require.Equal(t, http.StatusOK, cleared.Code)
	require.JSONEq(t, `{"v":"`+version+`","type":"image/gif"}`, cleared.Body.String())
	require.Nil(t, bannerList(t, s).People[bannerMember].Crop)
	require.Equal(t, plain.Version, s.p.banners().word().Version, "the list without a crop is the list it was")
	require.True(t, republished(s))
	require.Equal(t, http.StatusOK, putCrop(s, bannerMember, `{"crop":null}`).Code)
	require.False(t, republished(s))
	require.Equal(t, []string{bannerMember + "-" + version + ".gif"}, bannerNames(t, dir))
}

// A crop change's body may name the picture its part was chosen for, by the
// v the list wrote for it, in either order beside the crop; a body without
// one reads as it did before.
func TestACropChangesBodyMayNameItsPicture(t *testing.T) {
	const version = "0123456789abcdef"
	for raw, want := range map[string]*bannerCrop{
		`{"crop":[0,0,1,1],"v":"0123456789abcdef"}`:             {0, 0, 10000, 10000},
		`{"v":"0123456789abcdef","crop":[0,0,1,1]}`:             {0, 0, 10000, 10000},
		` { "v" : "0123456789abcdef" , "crop" : null } ` + "\n": nil,
		`{"crop":null,"v":"0123456789abcdef"}`:                  nil,
	} {
		crop, named, ok := cropFromBody([]byte(raw))
		require.True(t, ok, raw)
		require.Equal(t, want, crop, raw)
		require.Equal(t, version, named, raw)
	}

	crop, named, ok := cropFromBody([]byte(`{"crop":[0,0,1,1]}`))
	require.True(t, ok)
	require.Equal(t, &bannerCrop{0, 0, 10000, 10000}, crop)
	require.Empty(t, named, "no picture named")

	for _, raw := range malformedCropBodies(version) {
		_, _, ok := cropFromBody([]byte(raw))
		require.False(t, ok, raw)
	}
}

// malformedCropBodies is every body near the two fields' forms that is not
// one, around a v that would be one.
func malformedCropBodies(version string) []string {
	quoted := `"` + version + `"`

	return []string{
		`{"crop":[0,0,1,1],"v":` + quoted + `,"v":` + quoted + `}`,
		`{"v":` + quoted + `,"crop":null,"v":` + quoted + `}`,
		`{"crop":[0,0,1,1],"v":` + quoted + `,"crop":null}`,
		`{"crop":null,"crop":null,"v":` + quoted + `}`,
		`{"crop":null,"v":1234567890123456}`,
		`{"crop":null,"v":null}`,
		`{"crop":null,"v":"` + version[:15] + `"}`,
		`{"crop":null,"v":"` + version + `0"}`,
		`{"crop":null,"v":"0123456789ABCDEF"}`,
		`{"crop":null,"v":"0123456789abcdeF"}`,
		`{"crop":null,"v":"0123456789abcdeg"}`,
		`{"crop":null,"v":""}`,
		`{"crop":null,"v":[` + quoted + `]}`,
		`{"crop":null,"v":{"v":` + quoted + `}}`,
		`{"crop":null,"v":true}`,
		`{"crop":null,"v":` + quoted + `,"x":1}`,
		`{"x":1,"crop":null,"v":` + quoted + `}`,
		`{"crop":null,"V":` + quoted + `}`,
		`{"v":` + quoted + `}`,
		`{"crop":null,"v":` + quoted + `,}`,
		`{"crop":null,"v":` + quoted + `} x`,
		`{"v":` + quoted + `,"crop":null}{}`,
		`{"crop":null,"v":` + quoted,
	}
}

// A crop names the picture it was chosen for, so that a part chosen on one
// device never lands on a picture another device set meanwhile. With the
// picture the banner is, it is set as any other; with another, nothing moves
// and the answer says why; with no banner, there is none to adjust.
func TestACropChosenForAnotherPictureChangesNothing(t *testing.T) {
	s, dir := bannerScene(t)
	s.api.On("PublishWebSocketEvent", wsEventStreams, mock.Anything, mock.Anything).Return().Maybe()

	none := putCrop(s, bannerMember, `{"crop":[0,0,1,1],"v":"0123456789abcdef"}`)
	requireRefused(t, none, http.StatusNotFound, codeNotFound, "no banner")
	require.Equal(t, bannerNoneText, refusalOf(t, none).Error)
	requireRefused(t, putCrop(s, bannerMember, `{"v":"0123456789abcdef","crop":null}`), http.StatusNotFound, codeNotFound, "no banner to clear")
	require.False(t, republished(s))

	// The first picture, three wide by one high, is opened to be adjusted.
	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypeGIF, bannerGIF(t, 600, 200)).Code)
	require.True(t, republished(s))
	first := bannerList(t, s).People[bannerMember].V

	// Its own v sets the part, in either order, as a body without one would.
	set := putCrop(s, bannerMember, `{"crop":[0.25,0,0.5,0.5],"v":"`+first+`"}`)
	require.Equal(t, http.StatusOK, set.Code, set.Body.String())
	require.JSONEq(t, `{"v":"`+first+`","type":"image/gif","crop":[0.25,0,0.5,0.5]}`, set.Body.String())
	require.Equal(t, entryOf(t, set), bannerList(t, s).People[bannerMember])
	require.True(t, republished(s))
	before := bannerList(t, s).Version
	require.Equal(t, http.StatusOK, putCrop(s, bannerMember, `{"v":"`+first+`","crop":[0.1,0.1,0.8,0.8]}`).Code)
	require.Equal(t, &bannerCrop{1000, 1000, 8000, 8000}, bannerList(t, s).People[bannerMember].Crop)
	require.NotEqual(t, before, bannerList(t, s).Version)
	require.True(t, republished(s))

	// Another device sets another picture, of the same shape, so the part
	// chosen for the first would fit it.
	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypePNG, bannerPNG(t, 300, 100)).Code)
	require.True(t, republished(s))
	second := bannerList(t, s).People[bannerMember].V
	require.NotEqual(t, first, second)

	// The part chosen for the first is refused, and nothing moves: not the
	// file's name, not the list or its tag, and nobody is told.
	stale := func(body, about string) {
		t.Helper()

		names := bannerNames(t, dir)
		list := bannerList(t, s)
		tag := map[string]string{"If-None-Match": `"` + list.Version + `"`}

		refused := putCrop(s, bannerMember, body)
		requireRefused(t, refused, http.StatusConflict, codeConflict, about)
		require.Equal(t, bannerStaleText, refusalOf(t, refused).Error, about)
		require.Equal(t, names, bannerNames(t, dir), about)
		require.Equal(t, list, bannerList(t, s), about)
		require.Equal(t, list.Version, s.p.banners().word().Version, about)
		require.Equal(t, http.StatusNotModified, s.serve(http.MethodGet, "/api/v1/banners", sceneLetIn, "", tag).Code, about)
		require.False(t, republished(s), about)
	}
	stale(`{"crop":[0.1,0.1,0.8,0.8],"v":"`+first+`"}`, "the first picture's part")
	stale(`{"v":"`+first+`","crop":[0,0,1,1]}`, "the other order")
	stale(`{"crop":[0,0,1,1],"v":"0123456789abcdef"}`, "a picture never kept")
	require.Equal(t, []string{bannerMember + "-" + second + ".png"}, bannerNames(t, dir))

	// The second's own v sets its part; clearing it with the first's is
	// refused and leaves it, and with its own clears it.
	require.Equal(t, http.StatusOK, putCrop(s, bannerMember, `{"v":"`+second+`","crop":[0,0,1,1]}`).Code)
	require.True(t, republished(s))
	stale(`{"crop":null,"v":"`+first+`"}`, "clearing with the first picture's v")
	require.Equal(t, &bannerCrop{0, 0, 10000, 10000}, bannerList(t, s).People[bannerMember].Crop)
	cleared := putCrop(s, bannerMember, `{"crop":null,"v":"`+second+`"}`)
	require.Equal(t, http.StatusOK, cleared.Code)
	require.JSONEq(t, `{"v":"`+second+`","type":"image/png"}`, cleared.Body.String())
	require.True(t, republished(s))
	require.Equal(t, []string{bannerMember + "-" + second + ".png"}, bannerNames(t, dir))

	// Taken away, the v of the picture that was is no banner to adjust.
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/me/banner", bannerMember, "", nil).Code)
	requireRefused(t, putCrop(s, bannerMember, `{"crop":null,"v":"`+second+`"}`), http.StatusNotFound, codeNotFound, "taken away")
}

// A body that is not one of the two fields' forms is refused as any other
// malformed body, and moves nothing.
func TestAMalformedCropChangeNamingAPictureIsRefused(t *testing.T) {
	s, dir := bannerScene(t)
	s.api.On("PublishWebSocketEvent", wsEventStreams, mock.Anything, mock.Anything).Return().Maybe()
	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypeGIF, bannerGIF(t, 600, 200)).Code)
	require.True(t, republished(s))
	list := bannerList(t, s)
	version := list.People[bannerMember].V
	names := bannerNames(t, dir)

	for _, body := range malformedCropBodies(version) {
		refused := putCrop(s, bannerMember, body)
		requireRefused(t, refused, http.StatusBadRequest, codeBadCrop, body)
		require.Equal(t, bannerBadCropText, refusalOf(t, refused).Error, body)
	}
	require.Equal(t, list, bannerList(t, s))
	require.Equal(t, names, bannerNames(t, dir))
	require.False(t, republished(s))

	// The same picture's v, well written, is a crop.
	require.Equal(t, http.StatusOK, putCrop(s, bannerMember, `{"v":"`+version+`","crop":[0,0,1,1]}`).Code)
	require.True(t, republished(s))
}

// Changing a crop has a meter of its own, thirty in ten minutes, which says
// how long to wait; the upload's meter is not spent by it, and taking the
// banner away is still never metered.
func TestABannersCropChangeIsMetered(t *testing.T) {
	s, _ := bannerScene(t)
	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypePNG, bannerPNG(t, 300, 100)).Code)

	for i := range bannerCrops {
		body := `{"crop":null}`
		if i%2 == 0 {
			body = `{"crop":[0,0,1,1]}`
		}
		require.Equal(t, http.StatusOK, putCrop(s, bannerMember, body).Code, i)
	}

	limited := putCrop(s, bannerMember, `{"crop":null}`)
	requireRefused(t, limited, http.StatusTooManyRequests, codeRateLimited, "the thirty-first")
	require.Equal(t, bannerCropManyText, refusalOf(t, limited).Error)
	wait := refusalOf(t, limited).RetryAfterMs
	require.Positive(t, wait)
	require.LessOrEqual(t, wait, int64(20_000), "one of thirty back in ten minutes: twenty seconds at most")

	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypePNG, bannerPNG(t, 400, 100)).Code, "the upload's meter is its own")
	for range 3 {
		require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/me/banner", bannerMember, "", nil).Code)
	}
	require.NotContains(t, bannerList(t, s).People, bannerMember)
}

// A crop goes when its banner goes: the owner's takeback, a system admin's
// removal, a new upload without one. Nothing is left on the disk.
func TestABannersCropGoesWithIt(t *testing.T) {
	s, dir := bannerScene(t)
	picture := bannerPNG(t, 300, 100)

	require.Equal(t, http.StatusCreated, uploadCropped(s, bannerMember, bannerTypePNG, picture, "0,0,1,1").Code)
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/me/banner", bannerMember, "", nil).Code)
	require.NotContains(t, bannerList(t, s).People, bannerMember)
	require.Empty(t, bannerNames(t, dir))
	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypePNG, picture).Code)
	require.Nil(t, bannerList(t, s).People[bannerMember].Crop, "an upload after a takeback starts with none")

	require.Equal(t, http.StatusOK, putCrop(s, bannerMember, `{"crop":[0.25,0.25,0.5,0.5]}`).Code)
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/admin/banner?user="+bannerMember, sceneLockedAdmin, "", nil).Code)
	require.NotContains(t, bannerList(t, s).People, bannerMember)
	require.Empty(t, bannerNames(t, dir))
	requireRefused(t, putCrop(s, bannerMember, `{"crop":[0,0,1,1]}`), http.StatusNotFound, codeNotFound, "taken away")

	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypePNG, picture).Code)
	require.Equal(t, http.StatusOK, putCrop(s, bannerMember, `{"crop":[0.25,0.25,0.5,0.5]}`).Code)
	other := bannerPNG(t, 400, 100)
	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypePNG, other).Code)
	require.Equal(t, bannerEntry{V: shortSum(other), Type: bannerTypePNG}, bannerList(t, s).People[bannerMember], "a new picture without one has none")
	require.Equal(t, []string{bannerMember + "-" + shortSum(other) + ".png"}, bannerNames(t, dir))
}

// A takeback or an admin's removal on this node takes the picture away even
// when another node renames it to change its crop between this node's
// listing and its removal: the renamed file is the picture taken away.
func TestABannerRenamedForItsCropWhileItIsTakenAwayStillGoes(t *testing.T) {
	for _, c := range []struct {
		about, path, caller string
	}{
		{"the owner's takeback", "/api/v1/me/banner", bannerMember},
		{"an admin's removal", "/api/v1/admin/banner?user=" + bannerMember, sceneLockedAdmin},
	} {
		s, dir := bannerScene(t)
		store := s.p.banners()
		require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypePNG, bannerPNG(t, 300, 100)).Code, c.about)

		other := &Plugin{}
		other.banners().setDir(dir, "")
		store.removeFile = func(path string) error {
			store.removeFile = os.Remove
			_, changed, found, err := other.banners().setCrop(dir, bannerMember, &bannerCrop{0, 0, 10000, 10000}, "")
			require.NoError(t, err, c.about)
			require.True(t, found && changed, c.about)

			return os.Remove(path)
		}

		require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, c.path, c.caller, "", nil).Code, c.about)
		require.Empty(t, bannerNames(t, dir), c.about)
		require.NotContains(t, bannerList(t, s).People, bannerMember, c.about)
	}
}

// The same holds when the crop is changed again, from another device through
// another node, while this node looks for the picture once more: every
// rename is followed until the picture is gone.
func TestABannerRenamedTwiceForItsCropWhileItIsTakenAwayStillGoes(t *testing.T) {
	s, dir := bannerScene(t)
	store := s.p.banners()
	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypePNG, bannerPNG(t, 300, 100)).Code)

	nodeB := &Plugin{}
	nodeB.banners().setDir(dir, "")
	nodeC := &Plugin{}
	nodeC.banners().setDir(dir, "")
	renames := []struct {
		node *Plugin
		crop *bannerCrop
	}{
		{nodeB, &bannerCrop{0, 0, 10000, 10000}},
		{nodeC, &bannerCrop{1000, 0, 9000, 10000}},
	}
	store.removeFile = func(path string) error {
		if len(renames) > 0 {
			rename := renames[0]
			renames = renames[1:]
			_, changed, found, err := rename.node.banners().setCrop(dir, bannerMember, rename.crop, "")
			require.NoError(t, err)
			require.True(t, found && changed)
		}

		return os.Remove(path)
	}

	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/me/banner", bannerMember, "", nil).Code)
	require.Empty(t, renames, "both renames ran")
	require.Empty(t, bannerNames(t, dir))
	require.NotContains(t, bannerList(t, s).People, bannerMember)
}

// A crop is on the disk, in the picture's name: a fresh store over the same
// directory, as after a restart or on another node, knows it.
func TestABannersCropOutlivesARestart(t *testing.T) {
	s, dir := bannerScene(t)
	require.Equal(t, http.StatusCreated, uploadCropped(s, bannerMember, bannerTypePNG, bannerPNG(t, 300, 100), "0.1,0.2,0.6,0.6").Code)
	want := bannerList(t, s)

	restarted := &Plugin{}
	restarted.banners().setDir(dir, "")
	list, err := restarted.banners().current()
	require.NoError(t, err)
	require.Equal(t, want, *list)
	require.Equal(t, &bannerCrop{1000, 2000, 6000, 6000}, list.People[bannerMember].Crop)
}

// A crop changed on another node renames the picture there; this node, whose
// list is believed for a while, still serves the picture at its address, and
// sees the new crop once its list is old.
func TestABannersCropChangedOnAnotherNode(t *testing.T) {
	s, dir := bannerScene(t)
	at := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	s.p.banners().now = func() time.Time { return at }
	picture := bannerPNG(t, 300, 100)
	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypePNG, picture).Code)
	before := bannerList(t, s)
	version := before.People[bannerMember].V

	other := &Plugin{}
	other.banners().setDir(dir, "")
	entry, changed, found, err := other.banners().setCrop(dir, bannerMember, &bannerCrop{0, 0, 10000, 10000}, "")
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, changed)
	require.Equal(t, &bannerCrop{0, 0, 10000, 10000}, entry.Crop)

	require.Equal(t, before, bannerList(t, s), "believed until the list is old")
	served := bannerPicture(s, bannerMember, version)
	require.Equal(t, http.StatusOK, served.Code, "the picture under its new name")
	require.Equal(t, picture, served.Body.Bytes())

	at = at.Add(bannerListTTL)
	require.Equal(t, entry, bannerList(t, s).People[bannerMember])
}

// A crop changed or cleared on this node is kept even when another node
// renames the same picture for a crop between this node's listing and its
// rename, as two devices of one person adjusting at once do: the picture is
// still theirs and still the one the part was chosen for, so the answer is
// the part asked for, never "no banner". Taken away instead, it is 404.
func TestACropChangedWhileAnotherNodeRenamesThePictureIsKept(t *testing.T) {
	for _, c := range []struct {
		about, body string
		before      *bannerCrop
		want        *bannerCrop
		suffix      string
	}{
		{"a crop", `{"crop":[0,0,1,1]}`, nil, &bannerCrop{0, 0, 10000, 10000}, "-0-0-10000-10000.png"},
		{"a crop naming its picture", `{"crop":[0,0,1,1],"v":"%s"}`, nil, &bannerCrop{0, 0, 10000, 10000}, "-0-0-10000-10000.png"},
		{"a clear naming its picture", `{"crop":null,"v":"%s"}`, &bannerCrop{0, 0, 10000, 10000}, nil, ".png"},
	} {
		s, dir := bannerScene(t)
		store := s.p.banners()
		picture := bannerPNG(t, 300, 100)
		version := shortSum(picture)
		require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypePNG, picture).Code, c.about)
		if c.before != nil {
			_, _, _, err := store.setCrop(dir, bannerMember, c.before, "")
			require.NoError(t, err, c.about)
		}
		republished(s)

		other := &Plugin{}
		other.banners().setDir(dir, "")
		renames := 0
		store.renameFile = func(from, to string) error {
			renames++
			if renames == 1 {
				_, changed, found, err := other.banners().setCrop(dir, bannerMember, &bannerCrop{1000, 0, 9000, 10000}, "")
				require.NoError(t, err, c.about)
				require.True(t, found && changed, c.about)
			}

			return os.Rename(from, to)
		}

		response := putCrop(s, bannerMember, strings.ReplaceAll(c.body, "%s", version))
		require.Equal(t, http.StatusOK, response.Code, c.about+": "+response.Body.String())
		require.Equal(t, 2, renames, c.about+": looked again after the other node's rename")
		require.Equal(t, bannerEntry{V: version, Type: bannerTypePNG, Crop: c.want}, entryOf(t, response), c.about)
		require.Equal(t, []string{bannerMember + "-" + version + c.suffix}, bannerNames(t, dir), c.about)
		require.Equal(t, c.want, bannerList(t, s).People[bannerMember].Crop, c.about)
		require.True(t, republished(s), c.about)
	}

	// Taken away by the other node instead, the person has no banner.
	s, dir := bannerScene(t)
	store := s.p.banners()
	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypePNG, bannerPNG(t, 300, 100)).Code)
	other := &Plugin{}
	other.banners().setDir(dir, "")
	store.renameFile = func(from, to string) error {
		removed, err := other.banners().remove(dir, bannerMember)
		require.NoError(t, err)
		require.True(t, removed)

		return os.Rename(from, to)
	}
	requireRefused(t, putCrop(s, bannerMember, `{"crop":[0,0,1,1]}`), http.StatusNotFound, codeNotFound, "taken away")
	require.Empty(t, bannerNames(t, dir))

	// A new picture set through the other node instead makes a crop naming
	// the old one stale, found when this node looks again.
	s, dir = bannerScene(t)
	store = s.p.banners()
	first := bannerPNG(t, 300, 100)
	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypePNG, first).Code)
	other = &Plugin{}
	other.banners().setDir(dir, "")
	second := bannerPNG(t, 600, 200)
	store.renameFile = func(from, to string) error {
		_, _, err := other.banners().put(dir, bannerMember, bannerTypePNG, second)
		require.NoError(t, err)

		return os.Rename(from, to)
	}
	conflict := putCrop(s, bannerMember, `{"crop":[0,0,1,1],"v":"`+shortSum(first)+`"}`)
	require.Equal(t, http.StatusConflict, conflict.Code, conflict.Body.String())
	require.Equal(t, codeConflict, codedError(t, conflict).Code)
	require.Equal(t, []string{bannerMember + "-" + shortSum(second) + ".png"}, bannerNames(t, dir))
}

// Names in the directory that only look like a banner with a crop are
// nobody's banner: a crop this file would never have written.
func TestANameWithACropThatCannotBeOneIsNoBanner(t *testing.T) {
	s, dir := bannerScene(t)
	require.NoError(t, os.MkdirAll(dir, bannerDirMode))
	picture := bannerPNG(t, 300, 100)
	version := shortSum(picture)

	for _, crop := range []string{"-0-0-10001-10000", "-0-0-499-500", "-0-00-10000-10000", "-6000-0-5000-5000", "-0-0-1-", "-0-0-10000"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, bannerMember+"-"+version+crop+".png"), picture, 0o600))
	}
	require.Equal(t, map[string]bannerEntry{}, bannerList(t, s).People)
	requireRefused(t, bannerPicture(s, bannerMember, version), http.StatusNotFound, codeNotFound, "no banner")

	// One this file would write is somebody's, once the list is read again.
	require.NoError(t, os.WriteFile(filepath.Join(dir, bannerLinked+"-"+version+"-5000-0-5000-10000.png"), picture, 0o600))
	s.p.banners().forget()
	require.Equal(t, &bannerCrop{5000, 0, 5000, 10000}, bannerList(t, s).People[bannerLinked].Crop)
}

// Uploads, crop changes and takebacks of one person's banner at once, from
// many requests: the directory ends with at most one picture of theirs, the
// list says exactly what is there, and the picture it names is served.
func TestBannerWritersAtOnce(t *testing.T) {
	s, dir := bannerScene(t)
	s.p.banners().limiter = newRateLimiter(1000, time.Minute)
	s.p.banners().cropLimiter = newRateLimiter(1000, time.Minute)
	pictures := [][]byte{bannerPNG(t, 300, 100), bannerPNG(t, 600, 200), bannerGIF(t, 300, 100)}
	kinds := []string{bannerTypePNG, bannerTypePNG, bannerTypeGIF}

	done := make(chan struct{})
	for writer := range 8 {
		go func() {
			defer func() { done <- struct{}{} }()

			for round := range 12 {
				which := (writer + round) % len(pictures)
				switch (writer * round) % 4 {
				case 0:
					uploadBanner(s, bannerMember, kinds[which], pictures[which])
				case 1:
					uploadCropped(s, bannerMember, kinds[which], pictures[which], "0.25,0.25,0.5,0.5")
				case 2:
					putCrop(s, bannerMember, `{"crop":[0,0,1,1]}`)
				default:
					if round%3 == 0 {
						s.serve(http.MethodDelete, "/api/v1/me/banner", bannerMember, "", nil)
					} else {
						putCrop(s, bannerMember, `{"crop":null}`)
					}
				}
			}
		}()
	}
	for range 8 {
		<-done
	}

	names := bannerNames(t, dir)
	require.LessOrEqual(t, len(names), 1, "%v", names)

	restarted := &Plugin{}
	restarted.banners().setDir(dir, "")
	fresh, err := restarted.banners().current()
	require.NoError(t, err)
	require.Equal(t, *fresh, bannerList(t, s), "this node's list is what the directory holds")

	if len(names) == 1 {
		entry := fresh.People[bannerMember]
		require.Equal(t, []string{bannerName(bannerMember, entry)}, names)
		require.Equal(t, http.StatusOK, bannerPicture(s, bannerMember, entry.V).Code)
	}

	// And one more of each, in a known order, after all that.
	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypePNG, pictures[0]).Code)
	require.Equal(t, http.StatusOK, putCrop(s, bannerMember, `{"crop":[0,0,1,1]}`).Code)
	require.Equal(t, []string{bannerMember + "-" + shortSum(pictures[0]) + "-0-0-10000-10000.png"}, bannerNames(t, dir))
}
