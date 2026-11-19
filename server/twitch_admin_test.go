package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Twitch connections in the System Console (design/twitch-live.md §2.4, §4.10).

func TestTheTwitchAdminRoutesAreForSystemAdminsOnly(t *testing.T) {
	s := newTwitchScene(t)
	s.p.twitchLive().purgeOff = true
	s.connected(t, map[string]twitchUser{twErin: erinTwitch})

	calls := []struct{ method, target, body string }{
		{http.MethodGet, adminTwitchPath, ""},
		{http.MethodPost, adminTwitchAllDayPath, `{"userId":"` + twErin + `","allDay":true}`},
		{http.MethodPost, adminTwitchBlockPath, `{"userId":"` + twErin + `","blocked":true}`},
		{http.MethodPost, adminTwitchBlockPath, `{"all":true,"blocked":false}`},
		{http.MethodDelete, adminTwitchPath + "?user=" + twErin, ""},
		{http.MethodDelete, adminTwitchPath + "?all=1", ""},
	}

	for _, call := range calls {
		// Linked, or locked: neither is an admin.
		for _, userID := range []string{twDave, twDan} {
			response := s.serve(call.method, call.target, userID, web(userID), call.body, nil)
			require.Equal(t, http.StatusForbidden, response.Code, "%s %s as %s", call.method, call.target, userID)
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
		}
	}
	require.Contains(t, s.links(t).Users, twErin, "nothing a refused caller sent was acted on")

	for _, call := range calls {
		response := s.serve(call.method, call.target, twBoss, web(twBoss), call.body, nil)
		require.Equal(t, http.StatusOK, response.Code, "%s %s: %s", call.method, call.target, response.Body.String())
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	}

	// A request that names nobody, or nobody connected.
	bad := s.serve(http.MethodDelete, adminTwitchPath+"?user=not-an-id", twBoss, web(twBoss), "", nil)
	require.Equal(t, http.StatusBadRequest, bad.Code)
	missing := s.serve(http.MethodPost, adminTwitchAllDayPath, twBoss, web(twBoss), `{"userId":"`+twBob+`","allDay":true}`, nil)
	require.Equal(t, http.StatusNotFound, missing.Code)
}

func adminStatus(t *testing.T, s *twitchScene) (adminTwitchStatus, string) {
	t.Helper()

	response := s.serve(http.MethodGet, adminTwitchPath, twBoss, web(twBoss), "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var status adminTwitchStatus
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &status))

	return status, response.Body.String()
}

func TestTheTwitchAdminStatusForEachSiteURL(t *testing.T) {
	for _, tried := range []struct {
		site, callback, callbackProblem, embedProblem string
	}{
		{"https://chat.example.com/mm/", "https://chat.example.com/mm/plugins/ceo.villa.matty/api/v1/twitch/callback", "", ""},
		{"https://chat.example.com", "https://chat.example.com/plugins/ceo.villa.matty/api/v1/twitch/callback", "", ""},
		{"", "", "Mattermost's Site URL is not set", "not set"},
		{"http://chat.example.com", "", "Twitch only accepts an https:// callback address, and the Site URL is http://", "only runs on https:// pages"},
		{"http://localhost:8065", "http://localhost:8065/plugins/ceo.villa.matty/api/v1/twitch/callback", "", ""},
		{"https://chat.example.com:8065", "https://chat.example.com:8065/plugins/ceo.villa.matty/api/v1/twitch/callback", "", "the standard https port (443); this server uses :8065."},
		{"https://chat.example.com:443", "https://chat.example.com:443/plugins/ceo.villa.matty/api/v1/twitch/callback", "", ""},
		{"https://192.168.1.20", "https://192.168.1.20/plugins/ceo.villa.matty/api/v1/twitch/callback", "", "does not run on a site reached by IP address"},
		{"https://[2001:db8::1]", "https://[2001:db8::1]/plugins/ceo.villa.matty/api/v1/twitch/callback", "", "does not run on a site reached by IP address"},
		{"https://chat.example.com/?x=1", "", "could not be read as an address", ""},
	} {
		t.Run(tried.site, func(t *testing.T) {
			s := newTwitchScene(t)
			s.setSite(tried.site)

			status, _ := adminStatus(t, s)
			require.Equal(t, tried.callback, status.CallbackURL)
			if tried.callbackProblem == "" {
				require.Empty(t, status.CallbackProblem)
			} else {
				require.Contains(t, status.CallbackProblem, tried.callbackProblem)
			}
			if tried.embedProblem == "" {
				require.Empty(t, status.EmbedProblem)
			} else {
				require.Contains(t, status.EmbedProblem, tried.embedProblem)
			}
		})
	}
}

func TestTheTwitchAdminStatusNamesPeopleNotTwitchIDs(t *testing.T) {
	s := newTwitchScene(t)
	s.connected(t, map[string]twitchUser{twErin: erinTwitch, twDave: daveTwitch})
	doc := s.links(t)
	doc.Blocked[twDan] = 7
	link := doc.Users[twDave]
	link.AllDay = true
	doc.Users[twDave] = link
	s.putLinks(t, doc)
	s.seedTable(t, kvTwitchAirTotals, statTable{"1001": {Seconds: 3600}, "1002": {Seconds: 600}, "1003": {Seconds: 0}})
	s.fake.goLive(erinTwitch, "Live", "Art", 3, s.now())
	s.step(t)

	status, body := adminStatus(t, s)
	require.True(t, status.Enabled)
	require.False(t, status.Embed)
	require.True(t, status.Credentials)
	require.Equal(t, s.now().UnixMilli(), status.LastTry)
	require.Equal(t, s.now().UnixMilli(), status.LastGood)
	require.Empty(t, status.Problem)

	at := s.now().Add(-time.Hour).UnixMilli()
	require.Equal(t, []adminTwitchPerson{
		{UserID: twDave, Name: "Dave", Login: "dave_tv", TwitchName: "Dave_TV", Since: at, AllDay: true},
		{UserID: twErin, Name: "Erin", Login: "erin_streams", TwitchName: "Erin_Streams", Since: at, Live: true},
	}, status.People, "sorted by name")
	require.Equal(t, []adminTwitchBlocked{{UserID: twDan, Name: "Dan", Since: 7}}, status.Blocked)
	require.Equal(t, adminTwitchHours{Channels: 2, Seconds: 4200}, status.Hours)

	for _, id := range []string{"1001", "1002"} {
		require.NotContains(t, body, `"`+id+`"`, "no Twitch id is sent")
	}
	require.NotContains(t, body, testTwitchClientSecret)

	// With the switch off it still answers, and the people can still be
	// disconnected from it.
	cfg := s.p.getConfiguration().Clone()
	cfg.EnableTwitchLive = false
	s.p.setConfiguration(cfg)
	off, _ := adminStatus(t, s)
	require.False(t, off.Enabled)
	require.Len(t, off.People, 2)
	for _, person := range off.People {
		require.False(t, person.Live, "nobody is live while it is off")
	}

	// The set-aside copy is reported, so an admin knows to delete it.
	s.setKV(kvQuarantinePrefix+kvTwitchLinks, []byte("old"))
	quarantined, _ := adminStatus(t, s)
	require.True(t, quarantined.Hours.Quarantined)
	require.False(t, strings.Contains(body, "quarantined\":true"))
}

// The airtime card counts Broadcast Box hours only, as its export and backup
// do, so while Twitch is on it says where the Twitch hours are instead
// (design/twitch-live.md §7.12). With Twitch off it says nothing new.
func TestTheAirtimeCardSaysTwitchHoursAreKeptApart(t *testing.T) {
	s := newTwitchScene(t)
	s.seedTable(t, kvTwitchAirTotals, statTable{"1002": {Seconds: 600}})

	airtime := func() airtimeStatus {
		t.Helper()

		response := s.serve(http.MethodGet, "/api/v1/admin/airtime/status", twBoss, web(twBoss), "", nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())

		var status airtimeStatus
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &status))

		return status
	}

	on := airtime()
	require.True(t, on.TwitchSeparate)
	require.Zero(t, on.RecordedSeconds, "Twitch hours are not airtime the card counts")

	cfg := s.p.getConfiguration().Clone()
	cfg.EnableTwitchLive = false
	s.p.setConfiguration(cfg)
	require.False(t, airtime().TwitchSeparate)
}
