package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func brandPlugin(t *testing.T) (*Plugin, map[string][]byte) {
	t.Helper()

	store := map[string][]byte{}

	api := &plugintest.API{}
	api.On("HasPermissionTo", "uid-admin", model.PermissionManageSystem).Return(true)
	api.On("HasPermissionTo", "uid-user", model.PermissionManageSystem).Return(false)
	api.On("KVGet", mock.Anything).Return(
		func(key string) []byte { return append([]byte(nil), store[key]...) },
		func(_ string) *model.AppError { return nil },
	)
	api.On("KVSet", mock.Anything, mock.Anything).Return(
		func(key string, value []byte) *model.AppError {
			store[key] = append([]byte(nil), value...)

			return nil
		},
	)
	api.On("KVDelete", mock.Anything).Return(
		func(key string) *model.AppError {
			delete(store, key)

			return nil
		},
	)
	for _, level := range []string{"LogWarn", "LogError", "LogInfo", "LogDebug"} {
		for pairs := 0; pairs <= 8; pairs++ {
			args := make([]any, 0, 1+(pairs*2))
			for i := 0; i < 1+(pairs*2); i++ {
				args = append(args, mock.Anything)
			}
			api.On(level, args...).Return()
		}
	}

	api.On("PublishWebSocketEvent", wsEventMe, mock.Anything, mock.Anything).Return()

	p := &Plugin{}
	p.SetAPI(api)

	return p, store
}

func upload(t *testing.T, p *Plugin, which string, body []byte, userID string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/brand?which="+which, strings.NewReader(string(body)))

	p.handleBrand(recorder, request, userID)

	return recorder
}

func serve(t *testing.T, p *Plugin, which, ifNoneMatch string) *httptest.ResponseRecorder {
	t.Helper()

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/static/brand/"+which, nil)
	if ifNoneMatch != "" {
		request.Header.Set("If-None-Match", ifNoneMatch)
	}

	p.serveBrand(recorder, request, which)

	return recorder
}

// Only the icon is built in, Matty's own, the same file the bundle's
// manifest names: a server has an emblem and a ghost only once an admin
// uploads them, and until then there is nothing to serve for either.
func TestOnlyTheIconIsBuiltIn(t *testing.T) {
	p, _ := brandPlugin(t)

	icon := serve(t, p, brandIcon, "")
	require.Equal(t, http.StatusOK, icon.Code)
	require.Equal(t, "image/svg+xml", icon.Header().Get("Content-Type"))
	require.Contains(t, icon.Body.String(), "<title>Matty</title>")
	require.NotEmpty(t, icon.Header().Get("ETag"))

	bundled, err := os.ReadFile("../assets/icon.svg")
	require.NoError(t, err)
	require.Equal(t, bundled, icon.Body.Bytes(), "the App Bar's icon is the Marketplace's")

	for _, which := range []string{brandLogo, brandGhost} {
		require.Equal(t, http.StatusNotFound, serve(t, p, which, "").Code, which)
	}
	require.Equal(t, brandArtwork{}, p.artwork())
}

// Which artwork a server has follows its uploads at once on the node that
// took them, and every open client is told to ask again.
func TestTheArtworkFlagsFollowUploads(t *testing.T) {
	p, _ := brandPlugin(t)
	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)

	require.Equal(t, brandArtwork{}, p.artwork())

	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 32))
	require.Equal(t, http.StatusOK, upload(t, p, brandGhost, png, "uid-admin").Code)
	require.Equal(t, brandArtwork{Ghost: true}, p.artwork())
	require.Equal(t, http.StatusOK, upload(t, p, brandLogo, png, "uid-admin").Code)
	require.Equal(t, brandArtwork{Emblem: true, Ghost: true}, p.artwork())

	remove := httptest.NewRecorder()
	p.handleBrand(remove, httptest.NewRequest(http.MethodDelete, "/api/v1/admin/brand?which=ghost", nil), "uid-admin")
	require.Equal(t, http.StatusOK, remove.Code)
	require.Equal(t, brandArtwork{Emblem: true}, p.artwork())
	require.Equal(t, http.StatusNotFound, serve(t, p, brandGhost, "").Code, "removed, and nothing built in to fall back on")

	var told []map[string]any
	for _, call := range api.Calls {
		if call.Method == "PublishWebSocketEvent" {
			told = append(told, call.Arguments[1].(map[string]any))
		}
	}
	require.Equal(t, []map[string]any{
		{"name": "Matty", "artwork": map[string]any{"emblem": false, "ghost": true}},
		{"name": "Matty", "artwork": map[string]any{"emblem": true, "ghost": true}},
		{"name": "Matty", "artwork": map[string]any{"emblem": true, "ghost": false}},
	}, told)
}

func TestTheGhostCanBeReplacedLikeTheOtherArtwork(t *testing.T) {
	p, _ := brandPlugin(t)

	gif := []byte("GIF89a" + strings.Repeat("x", 32))
	require.Equal(t, http.StatusOK, upload(t, p, brandGhost, gif, "uid-admin").Code)

	recorder := serve(t, p, brandGhost, "")
	require.Equal(t, "image/gif", recorder.Header().Get("Content-Type"))
	require.Equal(t, gif, recorder.Body.Bytes())
}

func TestAnUploadedImageIsServedInsteadAndCanBePutBack(t *testing.T) {
	p, store := brandPlugin(t)

	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 64))

	require.Equal(t, http.StatusOK, upload(t, p, brandLogo, png, "uid-admin").Code)
	require.Contains(t, store, kvBrandPrefix+brandLogo)

	recorder := serve(t, p, brandLogo, "")
	require.Equal(t, "image/png", recorder.Header().Get("Content-Type"))
	require.Equal(t, png, recorder.Body.Bytes())

	// The other piece is untouched.
	require.Contains(t, serve(t, p, brandIcon, "").Body.String(), "<svg")

	// And removing it really removes it: there is no emblem built in.
	remove := httptest.NewRecorder()
	p.handleBrand(remove, httptest.NewRequest(http.MethodDelete, "/api/v1/admin/brand?which=logo", nil), "uid-admin")
	require.Equal(t, http.StatusOK, remove.Code)
	require.NotContains(t, store, kvBrandPrefix+brandLogo)
	require.Equal(t, http.StatusNotFound, serve(t, p, brandLogo, "").Code)

	// The icon's upload, taken away, puts the built-in one back.
	require.Equal(t, http.StatusOK, upload(t, p, brandIcon, png, "uid-admin").Code)
	require.Equal(t, png, serve(t, p, brandIcon, "").Body.Bytes())
	putBack := httptest.NewRecorder()
	p.handleBrand(putBack, httptest.NewRequest(http.MethodDelete, "/api/v1/admin/brand?which=icon", nil), "uid-admin")
	require.Equal(t, http.StatusOK, putBack.Code)
	require.Contains(t, serve(t, p, brandIcon, "").Body.String(), "<title>Matty</title>")
}

// The URL is stable and only its CONTENTS change, so it is revalidated rather
// than cached — otherwise an admin uploads new artwork and sees the old artwork
// for a week with no way to tell why. The ETag keeps that revalidation cheap.
func TestTheArtworkIsRevalidatedAndAnswersNotModified(t *testing.T) {
	p, _ := brandPlugin(t)

	first := serve(t, p, brandIcon, "")
	require.Equal(t, "no-cache", first.Header().Get("Cache-Control"))

	etag := first.Header().Get("ETag")
	require.NotEmpty(t, etag)

	again := serve(t, p, brandIcon, etag)
	require.Equal(t, http.StatusNotModified, again.Code)
	require.Empty(t, again.Body.String())

	// A different image is a different ETag, so the browser fetches it.
	require.Equal(t, http.StatusOK, upload(t, p, brandIcon, []byte("\x89PNG\r\n\x1a\nxxxx"), "uid-admin").Code)
	require.NotEqual(t, etag, serve(t, p, brandIcon, "").Header().Get("ETag"))
}

// The Content-Type the browser sends is not evidence. Serving an HTML file back
// as text/html from this origin, because somebody labelled it image/png, is the
// whole reason the bytes are sniffed instead.
func TestOnlyRealImagesAreAccepted(t *testing.T) {
	p, store := brandPlugin(t)

	refused := map[string][]byte{
		"html":  []byte("<!doctype html><script>alert(1)</script>"),
		"text":  []byte("this is just a note"),
		"empty": {},
		"pdf":   []byte("%PDF-1.7\n1 0 obj"),
		"zip":   []byte("PK\x03\x04dummy"),
	}

	for name, body := range refused {
		recorder := upload(t, p, brandLogo, body, "uid-admin")
		require.Equal(t, http.StatusBadRequest, recorder.Code, name)
		require.NotContains(t, store, kvBrandPrefix+brandLogo, name)
	}

	accepted := map[string][]byte{
		"png":  []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 32)),
		"gif":  []byte("GIF89a" + strings.Repeat("x", 32)),
		"jpeg": []byte("\xff\xd8\xff" + strings.Repeat("x", 32)),
		"svg":  []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>`),
	}

	for name, body := range accepted {
		require.Equal(t, http.StatusOK, upload(t, p, brandLogo, body, "uid-admin").Code, name)
	}
}

// An SVG served from this origin is a document, and a document can carry a
// script. Only a system admin can upload one, so this is not a privilege
// boundary — but the URL is reachable by anybody, and a link to it must not be
// able to run anything against whoever follows it.
func TestAnSVGIsServedWithoutTheAbilityToRunAnything(t *testing.T) {
	p, _ := brandPlugin(t)

	hostile := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(document.cookie)</script></svg>`)
	require.Equal(t, http.StatusOK, upload(t, p, brandLogo, hostile, "uid-admin").Code)

	recorder := serve(t, p, brandLogo, "")
	require.Equal(t, "image/svg+xml", recorder.Header().Get("Content-Type"))
	require.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))

	policy := recorder.Header().Get("Content-Security-Policy")
	require.Contains(t, policy, "default-src 'none'")
	require.Contains(t, policy, "sandbox")
}

func TestAnOversizedImageIsRefused(t *testing.T) {
	p, store := brandPlugin(t)

	huge := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, maxBrandBytes+1024)...)

	recorder := upload(t, p, brandLogo, huge, "uid-admin")
	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	require.Contains(t, recorder.Body.String(), "512 KB")
	require.NotContains(t, store, kvBrandPrefix+brandLogo)
}

func TestOnlyASystemAdminCanChangeTheArtwork(t *testing.T) {
	p, store := brandPlugin(t)

	require.Equal(t, http.StatusForbidden,
		upload(t, p, brandLogo, []byte("\x89PNG\r\n\x1a\nxxx"), "uid-user").Code)
	require.Empty(t, store)

	// But serving it needs no session at all, exactly like the fonts: it is
	// referenced from a stylesheet and from an img in Mattermost's App Bar.
	require.Equal(t, http.StatusOK, serve(t, p, brandIcon, "").Code)
}

func TestAnUnknownPieceOfArtworkIsNotServedOrStored(t *testing.T) {
	p, store := brandPlugin(t)

	require.Equal(t, http.StatusNotFound, serve(t, p, "../../etc/passwd", "").Code)
	require.Equal(t, http.StatusNotFound, serve(t, p, "banner", "").Code)
	require.Equal(t, http.StatusBadRequest,
		upload(t, p, "banner", []byte("\x89PNG\r\n\x1a\nxxx"), "uid-admin").Code)
	require.Empty(t, store)
}

// A value that will not decode must not take the App Bar's icon with it; an
// emblem that will not decode is no emblem.
func TestUnreadableStoredArtworkFallsBackToTheShippedOne(t *testing.T) {
	p, store := brandPlugin(t)
	store[kvBrandPrefix+brandIcon] = []byte("{not json")
	store[kvBrandPrefix+brandLogo] = []byte("{not json")

	recorder := serve(t, p, brandIcon, "")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "<svg")

	require.Equal(t, http.StatusNotFound, serve(t, p, brandLogo, "").Code)
	require.False(t, p.artwork().Emblem)
}

func TestTheSettingsPageIsToldWhichArtworkIsCustom(t *testing.T) {
	p, _ := brandPlugin(t)

	read := func() map[string]bool {
		recorder := httptest.NewRecorder()
		p.handleBrand(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/brand", nil), "uid-admin")
		require.Equal(t, http.StatusOK, recorder.Code)

		var body map[string]bool
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))

		return body
	}

	require.Equal(t, map[string]bool{brandLogo: false, brandIcon: false, brandGhost: false}, read())

	require.Equal(t, http.StatusOK, upload(t, p, brandIcon, []byte("GIF89a"+strings.Repeat("x", 32)), "uid-admin").Code)
	require.Equal(t, map[string]bool{brandLogo: false, brandIcon: true, brandGhost: false}, read())
}
