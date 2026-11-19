package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// /me goes to everybody who is signed in, so it must not carry the Broadcast
// Box URL: the origin's address on the LAN, and, until somebody fixes it, any
// user name and password saved inside it. Linked or not, admin or not.
func TestMeNeverCarriesTheBroadcastBoxURL(t *testing.T) {
	w := newClipsWorld(t, "uid-dave")
	w.person("uid-dan")
	w.person("uid-boss")
	clipsAdmins(w.api, "uid-boss")

	const origin = "http://viewer:hunter2@192.168.1.20:8080"
	cfg := w.p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = origin
	w.p.setConfiguration(cfg)

	for _, userID := range []string{"uid-dave", "uid-dan", "uid-boss"} {
		recorder := httptest.NewRecorder()
		w.p.handleMe(recorder, userID)
		require.Equal(t, http.StatusOK, recorder.Code, userID)

		body := recorder.Body.String()
		require.NotContains(t, body, "192.168.1.20", userID)
		require.NotContains(t, body, "hunter2", userID)

		var answer map[string]any
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer), userID)
		require.NotContains(t, answer, "siteUrl", userID)
	}
}
