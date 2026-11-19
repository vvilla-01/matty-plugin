package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Connect Steam (design/3.4.0.md §3, §8.1): the address Continue on Steam goes
// to, every check of what Steam sends back before Steam is asked, the one
// question asked of it, the response nonce, the Web API key, and what a
// GetPlayerSummaries answer means.

func TestSteamStartIsExact(t *testing.T) {
	s := newAccountsScene(t)

	state, location := s.startConnect(t, steamProvider, acDave)

	returnTo := "https://chat.example.org/plugins/ceo.villa.matty/api/v1/steam/callback?state=" + state
	require.Equal(t, s.fake.server.URL+"/openid/login?"+
		"openid.ns=http%3A%2F%2Fspecs.openid.net%2Fauth%2F2.0"+
		"&openid.mode=checkid_setup"+
		"&openid.return_to="+url.QueryEscape(returnTo)+
		"&openid.realm=https%3A%2F%2Fchat.example.org"+
		"&openid.identity=http%3A%2F%2Fspecs.openid.net%2Fauth%2F2.0%2Fidentifier_select"+
		"&openid.claimed_id=http%3A%2F%2Fspecs.openid.net%2Fauth%2F2.0%2Fidentifier_select", location.String())

	var record accountState
	require.NoError(t, json.Unmarshal(s.kv("steam_state_"+state), &record))
	require.Equal(t, accountState{Provider: accountSteam, User: acDave, Session: web(acDave), At: s.now().UnixMilli(), Mode: accountModeWeb, Return: returnTo}, record)
	require.Empty(t, s.fake.requests(), "no discovery, and nothing asked of Steam at all")

	// The realm is the origin, whatever path the Site URL has; a port is kept
	// only when it is not the scheme's own.
	for _, site := range []struct{ site, realm, callback string }{
		{"https://chat.example.org/mm/", "https://chat.example.org", "https://chat.example.org/mm/plugins/ceo.villa.matty/api/v1/steam/callback"},
		{"https://chat.example.org:443", "https://chat.example.org", "https://chat.example.org:443/plugins/ceo.villa.matty/api/v1/steam/callback"},
		{"https://chat.example.org:8443/", "https://chat.example.org:8443", "https://chat.example.org:8443/plugins/ceo.villa.matty/api/v1/steam/callback"},
		{"http://localhost:8065", "http://localhost:8065", "http://localhost:8065/plugins/ceo.villa.matty/api/v1/steam/callback"},
		{"http://127.0.0.1:8065", "http://127.0.0.1:8065", "http://127.0.0.1:8065/plugins/ceo.villa.matty/api/v1/steam/callback"},
	} {
		s.setSite(site.site)
		_, location := s.startConnect(t, steamProvider, acErin)
		require.Equal(t, site.realm, location.Query().Get("openid.realm"), site.site)
		require.True(t, strings.HasPrefix(location.Query().Get("openid.return_to"), site.callback+"?state="), site.site)
		s.clock.advance(4 * time.Minute)
	}

	// http anywhere but this machine is refused, and says why to an admin.
	s.setSite("http://chat.example.org")
	require.Equal(t, "Steam's sign-in needs an https:// address to send people back to, and the Site URL is http://…", steamSetup(s.p, s.p.getConfiguration()))
	requireRefused(t, s.serve(http.MethodPost, ticketPath(steamProvider), acDave, web(acDave), "", nil), http.StatusServiceUnavailable, accountCodeSetup, "http")
	s.setSite("")
	require.Contains(t, steamSetup(s.p, s.p.getConfiguration()), "Steam has nowhere to send people back to")

	// And the key: none, or one that cannot travel in a header.
	s.setSite("https://chat.example.org")
	s.configure(func(cfg *configuration) { cfg.SteamWebAPIKey = "two words" })
	require.Equal(t, "The Steam Web API key has characters a key cannot have.", steamSetup(s.p, s.p.getConfiguration()))
	require.True(t, s.p.getConfiguration().steamOn(), "still on, so the admin is told rather than shown nothing")
	require.False(t, s.me(t, acDave).Steam.Available)
	s.configure(func(cfg *configuration) { cfg.SteamWebAPIKey = strings.Repeat("k", 257) })
	require.Contains(t, steamSetup(s.p, s.p.getConfiguration()), "characters a key cannot have")
	require.Equal(t, "Fill in the Steam Web API key first.", steamSetup(s.p, &configuration{EnableSteam: true}))
}

func TestSteamCallbackChecks(t *testing.T) {
	type tried struct {
		name    string
		change  func(s *accountsScene, query url.Values)
		raw     func(query string) string // the whole query, after change
		heading string
		reason  string
	}

	identityOf := func(id string) string { return "https://steamcommunity.com/openid/id/" + id }
	set := func(field, value string) func(*accountsScene, url.Values) {
		return func(_ *accountsScene, query url.Values) { query.Set("openid."+field, value) }
	}

	cases := []tried{
		{name: "cancelled on Steam", change: func(_ *accountsScene, query url.Values) {
			for key := range query {
				if key != "state" {
					query.Del(key)
				}
			}
			query.Set("openid.ns", steamOpenIDNamespace)
			query.Set("openid.mode", "cancel")
		}, heading: "Not connected"},
		{name: "Steam's error", change: set("mode", "error"), heading: "Steam did not confirm it", reason: "steam_error"},
		{name: "another mode", change: set("mode", "check_authentication"), heading: "This link has run out", reason: "mode"},
		{name: "no mode", change: func(_ *accountsScene, query url.Values) { query.Del("openid.mode") }, heading: "This link has run out", reason: "mode"},
		{name: "an unknown openid key", change: set("ext1.value", "x"), heading: "Steam did not confirm it", reason: "fields"},
		{name: "a repeated openid key", change: func(_ *accountsScene, query url.Values) {
			query.Add("openid.claimed_id", identityOf(erinSteamID))
		}, heading: "Steam did not confirm it", reason: "fields"},
		{name: "a missing openid key", change: func(_ *accountsScene, query url.Values) { query.Del("openid.assoc_handle") }, heading: "Steam did not confirm it", reason: "fields"},
		{name: "an oversized value", change: set("assoc_handle", strings.Repeat("1", 513)), heading: "This link has run out", reason: "size"},
		{name: "an oversized return_to", change: func(_ *accountsScene, query url.Values) {
			query.Set("openid.return_to", query.Get("openid.return_to")+"&x="+strings.Repeat("y", 1024))
		}, heading: "This link has run out", reason: "size"},
		{name: "an oversized query", raw: func(query string) string { return query + "&padding=" + strings.Repeat("p", 9000) }, heading: "This link has run out", reason: "size"},
		{name: "a wrong ns", change: set("ns", "http://specs.openid.net/auth/1.1"), heading: "Steam did not confirm it", reason: "ns"},
		{name: "a wrong op_endpoint", change: set("op_endpoint", "https://steamcommunity.com.evil.example/openid/login"), heading: "Steam did not confirm it", reason: "op_endpoint"},
		{name: "claimed_id is not identity", change: set("claimed_id", identityOf(erinSteamID)), heading: "Steam did not confirm it", reason: "identity"},
		{name: "an unescaped dot", change: func(_ *accountsScene, query url.Values) {
			query.Set("openid.claimed_id", "https://steamcommunityXcom/openid/id/"+daveSteamID)
			query.Set("openid.identity", "https://steamcommunityXcom/openid/id/"+daveSteamID)
		}, heading: "Steam did not confirm it", reason: "identity"},
		{name: "http", change: func(_ *accountsScene, query url.Values) {
			query.Set("openid.claimed_id", "http://steamcommunity.com/openid/id/"+daveSteamID)
			query.Set("openid.identity", "http://steamcommunity.com/openid/id/"+daveSteamID)
		}, heading: "Steam did not confirm it", reason: "identity"},
		{name: "an 18-digit id", change: func(_ *accountsScene, query url.Values) {
			query.Set("openid.claimed_id", identityOf(daveSteamID+"1"))
			query.Set("openid.identity", identityOf(daveSteamID+"1"))
		}, heading: "Steam did not confirm it", reason: "identity"},
		{name: "a different signed list", change: set("signed", "signed,op_endpoint,claimed_id,identity,response_nonce,assoc_handle"), heading: "Steam did not confirm it", reason: "signed"},
		{name: "return_to with another state", change: func(s *accountsScene, query url.Values) {
			query.Set("openid.return_to", s.steamCallbackFor(strings.Repeat("e", 64)))
		}, heading: "Steam did not confirm it", reason: "return_to"},
		{name: "return_to with an extra parameter", change: func(_ *accountsScene, query url.Values) {
			query.Set("openid.return_to", query.Get("openid.return_to")+"&extra=1")
		}, heading: "Steam did not confirm it", reason: "return_to"},
		{name: "a nonce six minutes old", change: func(s *accountsScene, query url.Values) {
			query.Set("openid.response_nonce", steamNonceAt(s.now().Add(-6*time.Minute)))
		}, heading: "Steam did not confirm it", reason: "nonce_time"},
		{name: "a nonce from the future", change: func(s *accountsScene, query url.Values) {
			query.Set("openid.response_nonce", steamNonceAt(s.now().Add(6*time.Minute)))
		}, heading: "Steam did not confirm it", reason: "nonce_time"},
		{name: "a nonce with no time", change: set("response_nonce", "whenever"), heading: "Steam did not confirm it", reason: "nonce_time"},
		{name: "a nonce too long", change: func(s *accountsScene, query url.Values) {
			query.Set("openid.response_nonce", steamNonceAt(s.now())+strings.Repeat("n", 250))
		}, heading: "Steam did not confirm it", reason: "nonce_time"},
		{name: "a reused nonce", change: func(s *accountsScene, query url.Values) {
			s.setKV(kvSteamNoncePrefix+hashHex(query.Get("openid.response_nonce")), []byte(`{"at":1}`))
		}, heading: "Steam did not confirm it", reason: "nonce_reuse"},
	}

	for _, tried := range cases {
		t.Run(tried.name, func(t *testing.T) {
			s := newAccountsScene(t)
			state, _ := s.startConnect(t, steamProvider, acDave)

			query := s.steamAssertion(s.steamCallbackFor(state), daveSteamID)
			query.Set("state", state)
			if tried.change != nil {
				tried.change(s, query)
			}
			raw := query.Encode()
			if tried.raw != nil {
				raw = tried.raw(raw)
			}

			response := s.callback(steamProvider, acDave, web(acDave), raw)
			require.Contains(t, response.Body.String(), "<h1>"+tried.heading+"</h1>", response.Body.String())
			require.Empty(t, s.steam.checkBodies(), "Steam is never asked")
			keys, _ := s.steam.summaryCalls()
			require.Empty(t, keys)
			require.Empty(t, s.links(t, steamProvider).Users)
			require.Nil(t, s.kv("steam_state_"+state), "the state is spent")

			found := ""
			for _, line := range s.logged() {
				if strings.Contains(line, "did not confirm a new connection") {
					found = line
				}
			}
			if tried.reason == "" {
				require.Empty(t, found)
				return
			}
			require.Contains(t, found, "reason"+tried.reason+"user_id", "fixed words only, with the user id")
			require.Contains(t, found, acDave)
		})
	}
}

func TestSteamCheckAuthenticationIsExact(t *testing.T) {
	s := newAccountsScene(t)
	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3})

	state, _ := s.startConnect(t, steamProvider, acDave)
	assertion := s.steamAssertion(s.steamCallbackFor(state), daveSteamID)
	response := s.callback(steamProvider, acDave, web(acDave), steamReturn(state, assertion))
	requirePage(t, response, http.StatusOK, "Steam connected")
	require.Contains(t, response.Body.String(), "Connected as <strong>Dave</strong> (Steam).")

	// Exactly the ten fields, in order, with the mode changed and nothing
	// added: not the state.
	var want []string
	for _, name := range steamOpenIDFields {
		value := assertion.Get("openid." + name)
		if name == "mode" {
			value = "check_authentication"
		}
		want = append(want, url.QueryEscape("openid."+name)+"="+url.QueryEscape(value))
	}
	require.Equal(t, []string{strings.Join(want, "&")}, s.steam.checkBodies())
	sent, err := url.ParseQuery(s.steam.checkBodies()[0])
	require.NoError(t, err)
	require.NotContains(t, sent, "state", "the state travels only inside return_to, which Steam signed")
	require.Len(t, sent, len(steamOpenIDFields))
	for _, seen := range s.fake.requests() {
		if seen.path == "/openid/login" {
			require.Equal(t, http.MethodPost, seen.method)
			require.Equal(t, "application/x-www-form-urlencoded", seen.contentType)
			require.Empty(t, seen.query, "nothing in the address")
		}
	}
	require.Equal(t, accountLink{ID: daveSteamID, Name: "Dave", At: s.now().UnixMilli()}, s.links(t, steamProvider).Users[acDave])

	for _, answer := range []struct {
		name    string
		status  int
		body    string
		heading string
		code    int
	}{
		{"is_valid:false", 0, "ns:" + steamOpenIDNamespace + "\nis_valid:false\n", "Steam did not confirm it", http.StatusBadGateway},
		{"no ns", 0, "is_valid:true\n", "Steam did not confirm it", http.StatusBadGateway},
		{"is_valid twice", 0, "ns:" + steamOpenIDNamespace + "\nis_valid:true\nis_valid:false\n", "Steam did not confirm it", http.StatusBadGateway},
		{"not Key-Value", 0, "<html>is_valid:true</html>", "Steam did not confirm it", http.StatusBadGateway},
		{"too long", 0, "ns:" + steamOpenIDNamespace + "\nis_valid:true\n" + strings.Repeat("x:y\n", 1100), "Steam did not confirm it", http.StatusBadGateway},
		{"403", http.StatusForbidden, "", "Steam is busy", http.StatusServiceUnavailable},
		{"429", http.StatusTooManyRequests, "", "Steam is busy", http.StatusServiceUnavailable},
		{"500", http.StatusInternalServerError, "", "Steam did not confirm it", http.StatusBadGateway},
	} {
		s.steam.answerChecks(answer.status, answer.body)
		state, _ := s.startConnect(t, steamProvider, acErin)
		response := s.callback(steamProvider, acErin, web(acErin), steamReturn(state, s.steamAssertion(s.steamCallbackFor(state), erinSteamID)))
		requirePage(t, response, answer.code, answer.heading, answer.name)
		require.NotContains(t, s.links(t, steamProvider).Users, acErin, answer.name)
		s.clock.advance(4 * time.Minute)
	}

	// Matty is given each as a code.
	s.steam.answerChecks(http.StatusTooManyRequests, "")
	appState, _ := s.appStart(t, steamProvider, acBob)
	requireRefused(t, s.appFinish(steamProvider, acBob, matty(acBob), steamReturn(appState, s.steamAssertion(s.steamCallbackFor(appState), bobSteamID))),
		http.StatusServiceUnavailable, accountCodeBusy, "busy")
	s.steam.answerChecks(0, "ns:"+steamOpenIDNamespace+"\nis_valid:false\n")
	appState, _ = s.appStart(t, steamProvider, acBob)
	requireRefused(t, s.appFinish(steamProvider, acBob, matty(acBob), steamReturn(appState, s.steamAssertion(s.steamCallbackFor(appState), bobSteamID))),
		http.StatusBadGateway, accountCodeProviderError, "is_valid:false")
	s.steam.answerChecks(0, "")
	appState, _ = s.appStart(t, steamProvider, acBob)
	requireRefused(t, s.appFinish(steamProvider, acBob, matty(acBob), "state="+appState+"&openid.mode=cancel&openid.ns="+url.QueryEscape(steamOpenIDNamespace)),
		http.StatusBadRequest, accountCodeCancelled, "cancelled")
}

func TestTwoCallbacksWithOneAssertionLinkOnce(t *testing.T) {
	s := newAccountsScene(t)
	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3})

	state, _ := s.startConnect(t, steamProvider, acDave)
	query := steamReturn(state, s.steamAssertion(s.steamCallbackFor(state), daveSteamID))

	var wait sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wait.Add(1)
		go func() {
			defer wait.Done()
			codes[i] = s.callback(steamProvider, acDave, web(acDave), query).Code
		}()
	}
	wait.Wait()

	require.ElementsMatch(t, []int{http.StatusOK, http.StatusBadRequest}, codes)
	require.Len(t, s.steam.checkBodies(), 1, "Steam is asked once")

	// Even with the state put back, the response nonce is spent: the
	// assertion never passes twice.
	var record accountState
	require.NoError(t, json.Unmarshal([]byte(`{"p":"steam","u":"`+acDave+`","s":"`+web(acDave)+`","m":"web","r":"`+s.steamCallbackFor(state)+`"}`), &record))
	parsed, err := url.ParseQuery(query)
	require.NoError(t, err)
	_, refusal := steamFinish(t.Context(), s.p, s.p.getConfiguration(), state, record, parsed, s.now())
	require.NotNil(t, refusal)
	require.Equal(t, "nonce_reuse", refusal.reason)
	require.Len(t, s.steam.checkBodies(), 1)

	// Two different sign-ins at once both work.
	s.player(steamPlayer{SteamID: erinSteamID, Name: "Erin", State: 1, Visibility: 3})
	s.player(steamPlayer{SteamID: bobSteamID, Name: "Bob", State: 1, Visibility: 3})
	queries := map[string]string{}
	for user, id := range map[string]string{acErin: erinSteamID, acBob: bobSteamID} {
		state, _ := s.startConnect(t, steamProvider, user)
		queries[user] = steamReturn(state, s.steamAssertion(s.steamCallbackFor(state), id))
	}
	for user, query := range queries {
		wait.Add(1)
		go func() {
			defer wait.Done()
			require.Equal(t, http.StatusOK, s.callback(steamProvider, user, web(user), query).Code)
		}()
	}
	wait.Wait()
	require.Len(t, s.links(t, steamProvider).Users, 3)
}

func TestSteamKeyNeverInAnAddressOrALog(t *testing.T) {
	s := newAccountsScene(t)
	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3})

	s.connectSteam(t, acDave, daveSteamID)
	keys, queries := s.steam.summaryCalls()
	require.Equal(t, []string{testSteamKey}, keys, "in the header")
	require.Equal(t, []string{"steamids=" + daveSteamID}, queries, "and nowhere in the address")

	var errs []error
	ask := func() {
		_, err := s.p.askSteamSummaries(t.Context(), s.p.getConfiguration().SteamWebAPIKey, []string{daveSteamID})
		require.Error(t, err)
		errs = append(errs, err)
	}

	// A key Steam refuses, a 500, and Steam out of reach.
	s.configure(func(cfg *configuration) { cfg.SteamWebAPIKey = "REFUSED" + testSteamKey })
	ask()
	require.Equal(t, "key", errs[0].(*steamReadError).reason)
	s.configure(func(cfg *configuration) { cfg.SteamWebAPIKey = testSteamKey })
	s.steam.answerSummaries(http.StatusInternalServerError, "")
	ask()
	require.Equal(t, "status", errs[1].(*steamReadError).reason)
	s.steam.answerSummaries(http.StatusTooManyRequests, "")
	ask()
	require.Equal(t, "slow", errs[2].(*steamReadError).reason)
	steamAPIBase = "https://127.0.0.1:1"
	ask()
	require.Equal(t, "unreachable", errs[3].(*steamReadError).reason)
	steamAPIBase = s.fake.server.URL
	s.steam.answerSummaries(0, "{not json")
	ask()
	require.Equal(t, "answer", errs[4].(*steamReadError).reason)

	// A connect whose name read fails still connects, and says so in the log
	// with a status only.
	s.steam.answerSummaries(http.StatusInternalServerError, "")
	s.connectSteam(t, acErin, erinSteamID)
	require.Equal(t, accountLink{ID: erinSteamID, At: s.now().UnixMilli()}, s.links(t, steamProvider).Users[acErin], "no name yet")

	for _, err := range errs {
		require.NotContains(t, err.Error(), testSteamKey)
		require.NotContains(t, err.Error(), "key=")
	}
	for _, line := range s.logged() {
		require.NotContains(t, line, testSteamKey)
	}
	for _, seen := range s.fake.requests() {
		require.NotContains(t, seen.query.Encode(), testSteamKey)
		require.NotContains(t, seen.body, testSteamKey)
		require.Empty(t, seen.query.Get("key"))
	}
}

func TestSteamSummariesMapping(t *testing.T) {
	game := func(name string, id string) steamPlayer {
		player := steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3, GameName: name}
		if id != "" {
			player.GameID = json.RawMessage(id)
		}

		return player
	}

	for _, tried := range []struct {
		name   string
		player steamPlayer
		want   steamStatus
	}{
		{"private, whatever personastate says", steamPlayer{Name: "Dave", State: 1, Visibility: 1, GameName: "ELDEN RING", GameID: json.RawMessage(`"1245620"`)}, steamStatus{State: "offline", Hidden: true, Name: "Dave"}},
		{"private, looking to trade", steamPlayer{Name: "Dave", State: 5, Visibility: 1}, steamStatus{State: "offline", Hidden: true, Name: "Dave"}},
		{"private, looking to play", steamPlayer{Name: "Dave", State: 6, Visibility: 2}, steamStatus{State: "offline", Hidden: true, Name: "Dave"}},
		{"offline", steamPlayer{Name: "Dave", State: 0, Visibility: 3}, steamStatus{State: "offline", Name: "Dave"}},
		{"online", steamPlayer{Name: "Dave", State: 1, Visibility: 3}, steamStatus{State: "online", Name: "Dave"}},
		{"looking to trade", steamPlayer{Name: "Dave", State: 5, Visibility: 3}, steamStatus{State: "online", Name: "Dave"}},
		{"looking to play", steamPlayer{Name: "Dave", State: 6, Visibility: 3}, steamStatus{State: "online", Name: "Dave"}},
		{"busy", steamPlayer{Name: "Dave", State: 2, Visibility: 3}, steamStatus{State: "busy", Name: "Dave"}},
		{"away", steamPlayer{Name: "Dave", State: 3, Visibility: 3}, steamStatus{State: "away", Name: "Dave"}},
		{"snooze", steamPlayer{Name: "Dave", State: 4, Visibility: 3}, steamStatus{State: "away", Name: "Dave"}},
		{"invisible", steamPlayer{Name: "Dave", State: 7, Visibility: 3}, steamStatus{State: "offline", Name: "Dave"}},
		{"a state Steam does not document", steamPlayer{Name: "Dave", State: 99, Visibility: 3}, steamStatus{State: "offline", Name: "Dave"}},
		{"playing", game("ELDEN RING", `"1245620"`), steamStatus{State: "online", InGame: true, Game: "ELDEN RING", GameID: "1245620", Name: "Dave"}},
		{"playing, the gameid a number", game("ELDEN RING", `1245620`), steamStatus{State: "online", InGame: true, Game: "ELDEN RING", GameID: "1245620", Name: "Dave"}},
		{"a game name cleaned and capped", game("  "+strings.Repeat("Ω", 90)+"​ ", ""), steamStatus{State: "online", InGame: true, Game: strings.Repeat("Ω", 80), Name: "Dave"}},
		{"a shortcut's huge gameid as a string: in a game, and no app id kept", game("", `"18446744073709551615"`), steamStatus{State: "online", InGame: true, Name: "Dave"}},
		{"a gameid past anything Steam writes: in a game, and no id kept", game("", `"18446744073709551615000"`), steamStatus{State: "online", InGame: true, Name: "Dave"}},
		{"a gameid as a number", game("", `12345678901234567890123`), steamStatus{State: "online", InGame: true, Name: "Dave"}},
		{"a gameid that is not digits: in a game, and no id kept", game("", `"12ab"`), steamStatus{State: "online", InGame: true, Name: "Dave"}},
		{"no gameid", game("", ""), steamStatus{State: "online", Name: "Dave"}},
		{"a null gameid", game("", `null`), steamStatus{State: "online", Name: "Dave"}},
		{"a name cleaned and capped", steamPlayer{Name: " " + strings.Repeat("n", 70), State: 1, Visibility: 3}, steamStatus{State: "online", Name: strings.Repeat("n", 64)}},
	} {
		require.Equal(t, tried.want, steamStatusOf(tried.player), tried.name)
	}

	// The answer as Steam sends it: unknown fields ignored, nothing else
	// decoded, a player not asked about dropped.
	s := newAccountsScene(t)
	s.steam.answerSummaries(0, `{"response":{"players":[
		{"steamid":"`+daveSteamID+`","personaname":"Dave","personastate":1,"communityvisibilitystate":3,
		 "gameextrainfo":"ELDEN RING","gameid":"1245620","realname":"Dave Ryder","loccountrycode":"IT",
		 "gameserverip":"192.0.2.1:27015","avatarfull":"https://avatars.example/dave.jpg","profileurl":"https://evil.example/"},
		{"steamid":"`+erinSteamID+`","personaname":"Not asked","personastate":1,"communityvisibilitystate":3}
	]}}`)
	players, err := s.p.askSteamSummaries(t.Context(), testSteamKey, []string{daveSteamID})
	require.NoError(t, err)
	require.Equal(t, map[string]steamPlayer{daveSteamID: {
		SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3, GameName: "ELDEN RING", GameID: json.RawMessage(`"1245620"`),
	}}, players)
}

func TestSteamAsksAboutEverybodyInOneCall(t *testing.T) {
	s := newAccountsScene(t)

	ids := make([]string, maxSteamIDsPerCall)
	doc := &accountLinks{Users: map[string]accountLink{}}
	for i := range ids {
		ids[i] = fmt.Sprintf("765611979603%05d", i)
		doc.Users[fmt.Sprintf("people%020d", i)] = accountLink{ID: ids[i], Name: "someone"}
	}

	_, err := s.p.askSteamSummaries(t.Context(), testSteamKey, ids)
	require.NoError(t, err)
	_, queries := s.steam.summaryCalls()
	require.Equal(t, []string{"steamids=" + strings.Join(ids, ",")}, queries, "one call, a hundred ids")

	_, err = s.p.askSteamSummaries(t.Context(), testSteamKey, append(ids, "76561197960399999"))
	require.Error(t, err, "a hundred and one is never sent")
	_, err = s.p.askSteamSummaries(t.Context(), testSteamKey, []string{"76561197960287930&key=x", "nope"})
	require.Error(t, err, "nor anything that is not a SteamID")
	_, queries = s.steam.summaryCalls()
	require.Len(t, queries, 1)

	// Anything else beside real ones is left out of the address.
	_, err = s.p.askSteamSummaries(t.Context(), testSteamKey, []string{daveSteamID, "x,y", daveSteamID})
	require.NoError(t, err)
	_, queries = s.steam.summaryCalls()
	require.Equal(t, "steamids="+daveSteamID, queries[1])

	// A pass of the loop is one call for all of them, in order: 1,440 a day.
	for userID := range doc.Users {
		s.person(userID, "Someone")
	}
	s.putLinks(t, steamProvider, doc)
	s.step(t)
	_, queries = s.steam.summaryCalls()
	require.Len(t, queries, 3)
	require.Equal(t, "steamids="+strings.Join(ids, ","), queries[2], "one call from the loop, a hundred ids")

	// So a hundred and first person cannot connect.
	requireRefused(t, s.appConnect(steamProvider, acDave, matty(acDave)), http.StatusConflict, accountCodeFull, "full")
	requirePage(t, s.connectWith(steamProvider, acDave, web(acDave), s.ticket(t, steamProvider, acDave), nil), http.StatusConflict, "No room")

	// Somebody already connected can connect again.
	doc.Users[acDave] = accountLink{ID: daveSteamID}
	delete(doc.Users, "people00000000000000000000")
	s.putLinks(t, steamProvider, doc)
	s.connectSteam(t, acDave, daveSteamID)
}

func TestSteamCallCountResetsAtMidnightUTC(t *testing.T) {
	s := newAccountsScene(t)
	store := s.p.accounts()
	s.clock.advance(11*time.Hour + 58*time.Minute) // 23:58 UTC

	s.connectSteam(t, acDave, daveSteamID)
	_, err := s.p.askSteamSummaries(t.Context(), testSteamKey, []string{daveSteamID})
	require.NoError(t, err)
	require.Equal(t, 2, store.steamCallsToday(s.now()), "the connect's name read and the loop's read alike")

	s.clock.advance(3 * time.Minute) // 00:01 UTC
	require.Equal(t, 0, store.steamCallsToday(s.now()))
	_, err = s.p.askSteamSummaries(t.Context(), testSteamKey, []string{daveSteamID})
	require.NoError(t, err)
	require.Equal(t, 1, store.steamCallsToday(s.now()))

	// Steam's day, not this server's: a clock in another zone counts the
	// same.
	pacific := time.FixedZone("UTC-8", -8*60*60)
	require.Equal(t, 1, store.steamCallsToday(s.now().In(pacific)))
	store.countSteamCall(s.now().In(pacific))
	require.Equal(t, 2, store.steamCallsToday(s.now()))
}

func TestAPrivateSteamProfileConnectsAndSaysSo(t *testing.T) {
	s := newAccountsScene(t)
	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 1, GameName: "ELDEN RING"})

	response := s.connectSteam(t, acDave, daveSteamID)
	require.Contains(t, response.Body.String(), "Your Steam profile is not public")
	require.True(t, s.me(t, acDave).Steam.Hidden)

	// Nobody else is told it is private: to them it is offline.
	store := s.p.accounts()
	store.lock.Lock()
	store.steamLastGood = s.now()
	store.lock.Unlock()
	_, body := s.accountsAnswer(t, acErin)
	require.Contains(t, string(body), `"state":"offline"`)
	require.NotContains(t, string(body), "hidden")
	require.NotContains(t, string(body), "private")
	require.NotContains(t, string(body), "ELDEN RING")
}

func TestSteamFinishNeedsNoKeyToProveTheAccount(t *testing.T) {
	s := newAccountsScene(t)
	state, _ := s.startConnect(t, steamProvider, acDave)
	query, err := url.ParseQuery(steamReturn(state, s.steamAssertion(s.steamCallbackFor(state), daveSteamID)))
	require.NoError(t, err)

	var record accountState
	require.NoError(t, json.Unmarshal(s.kv("steam_state_"+state), &record))

	// A key that cannot travel is never sent: the account is still proved,
	// with no name.
	cfg := s.p.getConfiguration().Clone()
	cfg.SteamWebAPIKey = "two words"
	proof, refusal := steamFinish(context.Background(), s.p, cfg, state, record, query, s.now())
	require.Nil(t, refusal)
	require.Equal(t, accountProof{ID: daveSteamID}, proof)
	keys, _ := s.steam.summaryCalls()
	require.Empty(t, keys)
}
