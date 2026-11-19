package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// The starting birthday list reaches a system admin and nobody else, and only
// through the route the System Console asks. Since 3.7.2 it holds nobody.
func TestOnlyASystemAdminGetsTheStartingBirthdayList(t *testing.T) {
	p, _ := settingsPlugin(t, fullConfig(), map[string]any{})

	recorder := httptest.NewRecorder()
	p.handleBirthdaySeed(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/birthdays/seed", nil), "uid-user")
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "birth_year")

	recorder = httptest.NewRecorder()
	p.handleBirthdaySeed(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/admin/birthdays/seed", nil), "uid-admin")
	require.Equal(t, http.StatusMethodNotAllowed, recorder.Code)

	recorder = httptest.NewRecorder()
	p.handleBirthdaySeed(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/birthdays/seed", nil), "uid-admin")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "no-store", recorder.Header().Get("Cache-Control"))

	// A list, with nobody on it: [] and never null, which the System Console
	// would not read as a list.
	require.JSONEq(t, `[]`, recorder.Body.String())

	var entries []birthdayEntry
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &entries))
	require.NotNil(t, entries)
	require.Empty(t, entries)
}

// The plugin ships nobody's name or date of birth: the list the old cron
// script held went in 3.7.2. Somebody put back fails here, whoever they are.
func TestTheStartingBirthdayListHoldsNobody(t *testing.T) {
	require.NotNil(t, birthdaySeed)
	require.Empty(t, birthdaySeed)

	encoded, err := json.Marshal(birthdaySeed)
	require.NoError(t, err)
	require.Equal(t, `[]`, string(encoded))
}

// A shape check only: the entry the route answers in is the shape the
// birthday list is saved in, so anything it ever offers is read back exactly
// as a list typed by hand. It marshals its own made-up people and never reads
// birthdaySeed, so it says nothing about what the starting list holds.
func TestTheStartingListKeepsTheShapeOfTheBirthdayList(t *testing.T) {
	encoded, err := json.Marshal([]birthdaySeedEntry{
		{Username: "alice", Name: "Alice Example", Birthday: "03-14", BirthYear: 1990},
		{Username: "bob", Name: "Bob Example", Birthday: "07-01", BirthYear: 1988},
		{Name: "Carol Example", Birthday: "11-30", BirthYear: 2018, ParentUsername: []string{"alice", "bob"}},
	})
	require.NoError(t, err)

	entries, err := parseBirthdays(string(encoded))
	require.NoError(t, err)
	require.Len(t, entries, 3)
	require.Equal(t, "alice", entries[0].Username)
	require.Equal(t, "Bob Example", entries[1].Name)
	require.Equal(t, "11-30", entries[2].Birthday)
	require.Equal(t, 2018, entries[2].BirthYear)
	require.Equal(t, []string{"alice", "bob"}, entries[2].parents())
}

// The lock (design/lock-screen.md 8.2): the System Console works without a
// token, so the starting list reaches a system admin whose own panel is
// locked, through the plugin's front door, and still nobody else, who is
// told no as somebody who is not an admin rather than as somebody locked.
func TestTheStartingBirthdayListIsASystemConsoleRoute(t *testing.T) {
	require.True(t, underPath("/api/v1/admin/birthdays/seed", adminPathRoot))
	require.Contains(t, adminRoutesInAPI(t), "/api/v1/admin/birthdays/seed")

	s := newAccessScene(t)

	admin := s.serve(http.MethodGet, "/api/v1/admin/birthdays/seed", sceneLockedAdmin, "", nil)
	require.Equal(t, http.StatusOK, admin.Code)
	require.JSONEq(t, `[]`, admin.Body.String())

	refused := s.serve(http.MethodGet, "/api/v1/admin/birthdays/seed", sceneLocked, "", nil)
	require.Equal(t, http.StatusForbidden, refused.Code)
	require.NotEqual(t, accessCodeLocked, codedErrorOrEmpty(refused))
	require.Equal(t, codeForbidden, codedErrorOrEmpty(refused))
}
