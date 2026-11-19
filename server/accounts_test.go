package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Connecting Steam and Hytale, the parts both share (design/3.4.0.md §2, §6,
// §8.1): who may reach each route, the ticket, the connect page, the callback,
// Matty's two routes, the link documents, takeover notices, Disconnect,
// Delete all, /me and the pages.

// connectedSteam is dave connected to Steam the way a finished sign-in leaves
// him, with Steam saying he is online and public.
func (s *accountsScene) connectedSteam(t *testing.T) {
	t.Helper()

	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3})
	s.connectSteam(t, acDave, daveSteamID)
}

func TestAccountRoutesAreForTheRightPeople(t *testing.T) {
	s := newAccountsScene(t)
	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3})

	// Everybody's Steam status and Hytale names are watching.
	code, body := s.accountsAnswer(t, acDan)
	require.Equal(t, http.StatusForbidden, code)
	require.Contains(t, string(body), `"code":"locked"`)
	for _, reader := range []string{acLetIn, acDave} {
		code, _ := s.accountsAnswer(t, reader)
		require.Equal(t, http.StatusOK, code, reader)
	}

	for _, prov := range accountProviders {
		// Connecting is for linked people: the gate says so before anything.
		for _, route := range []string{ticketPath(prov), appPath(prov, "connect"), appPath(prov, "finish")} {
			requireRefused(t, s.serve(http.MethodPost, route, acDan, web(acDan), "{}", nil), http.StatusForbidden, accessCodeLocked, route)
			requireRefused(t, s.serve(http.MethodPost, route, acLetIn, web(acLetIn), "{}", nil), http.StatusForbidden, accessCodeWatchOnly, route)
		}
		require.Regexp(t, hex64, s.ticket(t, prov, acDave))
		require.Equal(t, http.StatusOK, s.appConnect(prov, acDave, matty(acDave)).Code)

		// A linked person finishes in Matty.
		state, location := s.appStart(t, prov, acDave)
		query := ""
		if prov.id == accountSteam {
			query = steamReturn(state, s.steamAssertion(s.steamCallbackFor(state), daveSteamID))
		} else {
			s.grantHytale(t, location, "code-routes", hytaleGrant{info: hytaleProfile(daveHytaleUUID, "Steve_Hytale")})
			query = "code=code-routes&state=" + state
		}
		finished := s.appFinish(prov, acDave, matty(acDave), query)
		require.Equal(t, http.StatusOK, finished.Code, finished.Body.String())

		// Taking things back is for everybody, the lock screen included.
		for _, caller := range []string{acDan, acLetIn, acDave} {
			for _, route := range []struct{ method, path string }{
				{http.MethodDelete, "/api/v1/" + prov.id + "/link"},
				{http.MethodPost, "/api/v1/" + prov.id + "/notice"},
			} {
				response := s.serve(route.method, route.path, caller, web(caller), "", nil)
				require.Equal(t, http.StatusOK, response.Code, "%s %s %s", caller, route.method, route.path)
				require.JSONEq(t, `{"ok":true}`, response.Body.String())
				require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
			}
		}
	}
}

func TestAccountTicketsAreBoundToTheSessionAndRefusedToIntegrations(t *testing.T) {
	for _, prov := range accountProviders {
		s := newAccountsScene(t)

		ticket := s.ticket(t, prov, acDave)
		var stored accountTicket
		require.NoError(t, json.Unmarshal(s.kv(prov.id+"_ticket_"+acDave), &stored))
		require.Equal(t, hashHex(ticket), stored.Hash, "the hash, never the ticket")
		require.Equal(t, web(acDave), stored.Session)
		require.EqualValues(t, 600, s.expiryOf(prov.id+"_ticket_"+acDave))
		require.NotContains(t, string(s.kv(prov.id+"_ticket_"+acDave)), ticket)

		// Only a POST.
		require.Equal(t, http.StatusMethodNotAllowed, s.serve(http.MethodGet, ticketPath(prov), acErin, web(acErin), "", nil).Code)

		// Not a personal access token, a bot or an OAuth app acting for somebody.
		for _, session := range []string{"pat-" + acErin, "bot-" + acErin, "app-" + acErin, ""} {
			for _, route := range []string{ticketPath(prov), appPath(prov, "connect"), appPath(prov, "finish")} {
				response := s.serve(http.MethodPost, route, acErin, session, "{}", nil)
				requireRefused(t, response, http.StatusForbidden, accountCodeInteractiveOnly, session+" "+route)
				require.Contains(t, response.Body.String(), "not a personal access token")
			}
		}
		require.Nil(t, s.kv(prov.id+"_ticket_"+acErin))
		require.Empty(t, s.keysWith(prov.id+"_state_"))

		// Off.
		s.configure(func(cfg *configuration) { cfg.EnableSteam, cfg.EnableHytale = false, false })
		requireRefused(t, s.serve(http.MethodPost, ticketPath(prov), acErin, web(acErin), "", nil), http.StatusNotFound, accountCodeOff, "off")
		s.configure(func(cfg *configuration) { cfg.EnableSteam, cfg.EnableHytale = true, true })

		// Not set up: admins are told why.
		s.setSite("http://chat.example.org")
		unset := s.serve(http.MethodPost, ticketPath(prov), acErin, web(acErin), "", nil)
		requireRefused(t, unset, http.StatusServiceUnavailable, accountCodeSetup, "setup")
		require.Contains(t, unset.Body.String(), "An admin needs to finish setting up "+prov.name+".")
		admin := s.serve(http.MethodPost, ticketPath(prov), acBoss, web(acBoss), "", nil)
		requireRefused(t, admin, http.StatusServiceUnavailable, accountCodeSetup, "setup, for an admin")
		require.Contains(t, admin.Body.String(), "https://")
		s.setSite("https://chat.example.org")

		// Blocked.
		s.putLinks(t, prov, &accountLinks{Users: map[string]accountLink{}, Blocked: map[string]int64{acErin: 1}})
		blocked := s.serve(http.MethodPost, ticketPath(prov), acErin, web(acErin), "", nil)
		requireRefused(t, blocked, http.StatusForbidden, accountCodeBlocked, "blocked")
		require.Contains(t, blocked.Body.String(), "An admin has turned off Connect "+prov.name+" for your account.")
		require.Nil(t, s.kv(prov.id+"_ticket_"+acErin))

		// Five tries a quarter of an hour, counted at the ticket and at
		// app-connect alike, and none of the refusals above counted.
		for i := 0; i < 3; i++ {
			s.ticket(t, prov, acBob)
		}
		require.Equal(t, http.StatusOK, s.appConnect(prov, acBob, matty(acBob)).Code)
		s.ticket(t, prov, acBob)
		tooMany := s.serve(http.MethodPost, ticketPath(prov), acBob, web(acBob), "", nil)
		requireRefused(t, tooMany, http.StatusTooManyRequests, accountCodeRateLimited, "rate limited")
		require.Equal(t, "no-store", tooMany.Header().Get("Cache-Control"))
		requireRefused(t, s.appConnect(prov, acBob, matty(acBob)), http.StatusTooManyRequests, accountCodeRateLimited, "rate limited")

		// The other service keeps its own count.
		other := steamProvider
		if prov.id == accountSteam {
			other = hytaleProvider
		}
		require.Regexp(t, hex64, s.ticket(t, other, acBob))
	}
}

func TestATicketOfOneServiceCannotStartAnother(t *testing.T) {
	s := newAccountsScene(t)

	steamTicket := s.ticket(t, steamProvider, acDave)
	hytaleTicket := s.ticket(t, hytaleProvider, acDave)

	// A Steam ticket at Hytale's connect, and Hytale's at Steam's.
	for _, tried := range []struct {
		at     *accountProvider
		ticket string
	}{{hytaleProvider, steamTicket}, {steamProvider, hytaleTicket}} {
		response := s.connectWith(tried.at, acDave, web(acDave), tried.ticket, nil)
		requirePage(t, response, http.StatusBadRequest, "Start from Matty", tried.at.id)
		require.Empty(t, response.Header().Get("Location"))
	}

	// A Twitch ticket, as Connect Twitch stores one, at Steam's connect; and a
	// Steam ticket at Twitch's.
	fromTwitch := strings.Repeat("c1", 32)
	record, err := json.Marshal(twitchTicket{Hash: hashHex(fromTwitch), Session: web(acDave), At: s.now().UnixMilli()})
	require.NoError(t, err)
	s.setKV(kvTwitchTicketPrefix+acDave, record)

	response := s.connectWith(steamProvider, acDave, web(acDave), fromTwitch, nil)
	requirePage(t, response, http.StatusBadRequest, "Start from Matty")
	twitch := s.serve(http.MethodGet, twitchConnectPath+"?t="+steamTicket, acDave, web(acDave), "", nil)
	require.Equal(t, http.StatusBadRequest, twitch.Code)
	require.Empty(t, twitch.Header().Get("Location"))

	require.Empty(t, s.keysWith("steam_state_"), "nothing written")
	require.Empty(t, s.keysWith("hytale_state_"))
	require.Empty(t, s.keysWith(kvTwitchStatePrefix))
	require.NotNil(t, s.kv("steam_ticket_"+acDave), "each real ticket is left where it is")
	require.NotNil(t, s.kv("hytale_ticket_"+acDave))
	require.NotNil(t, s.kv(kvTwitchTicketPrefix+acDave))
	require.Empty(t, s.fake.requests())

	// Each still works where it belongs.
	require.Equal(t, http.StatusFound, s.connectWith(steamProvider, acDave, web(acDave), steamTicket, nil).Code)
	require.Equal(t, http.StatusFound, s.connectWith(hytaleProvider, acDave, web(acDave), hytaleTicket, nil).Code)
}

func TestAccountConnectWritesNothingWithoutAValidTicket(t *testing.T) {
	type attempt struct {
		name    string
		ticket  func(real string) string
		session string
		header  map[string]string
	}

	attempts := []attempt{
		{name: "no ticket", ticket: func(string) string { return "" }},
		{name: "a malformed ticket", ticket: func(string) string { return "XYZ" }},
		{name: "an unknown ticket", ticket: func(string) string { return strings.Repeat("ab", 32) }},
		{name: "a ticket in capitals", ticket: strings.ToUpper},
		{name: "a ticket of another session", session: "tab-" + acDave},
		{name: "an image fetching it", header: map[string]string{"Sec-Fetch-Dest": "image"}},
		{name: "another site navigating to it", header: map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Dest": "document"}},
	}

	for _, prov := range accountProviders {
		for _, tried := range attempts {
			t.Run(prov.id+": "+tried.name, func(t *testing.T) {
				s := newAccountsScene(t)
				ticket := s.ticket(t, prov, acDave)
				stored := s.kv(prov.id + "_ticket_" + acDave)
				if tried.ticket != nil {
					ticket = tried.ticket(ticket)
				}
				session := tried.session
				if session == "" {
					session = web(acDave)
				}

				limiter := s.p.accounts().connectLimiter(prov)
				limiter.lock.Lock()
				tokens := limiter.buckets[acDave].tokens
				limiter.lock.Unlock()

				response := s.connectWith(prov, acDave, session, ticket, tried.header)
				requirePage(t, response, http.StatusBadRequest, "Start from Matty", tried.name)
				require.Empty(t, response.Header().Get("Location"))
				require.Empty(t, s.keysWith(prov.id+"_state_"))
				require.Empty(t, s.fake.requests())
				require.Equal(t, stored, s.kv(prov.id+"_ticket_"+acDave), "the real ticket survives")

				limiter.lock.Lock()
				require.InDelta(t, tokens, limiter.buckets[acDave].tokens, 0.01, "nothing counted")
				limiter.lock.Unlock()
			})
		}

		t.Run(prov.id+": a spent ticket", func(t *testing.T) {
			s := newAccountsScene(t)
			ticket := s.ticket(t, prov, acDave)
			require.Equal(t, http.StatusFound, s.connectWith(prov, acDave, web(acDave), ticket, nil).Code)
			states := s.keysWith(prov.id + "_state_")
			require.Len(t, states, 1)

			again := s.connectWith(prov, acDave, web(acDave), ticket, nil)
			requirePage(t, again, http.StatusBadRequest, "Start from Matty")
			require.Empty(t, again.Header().Get("Location"))
			require.Equal(t, states, s.keysWith(prov.id+"_state_"), "no second state")
		})

		t.Run(prov.id+": the desktop app's own window", func(t *testing.T) {
			s := newAccountsScene(t)
			response := s.connectWith(prov, acDave, web(acDave), s.ticket(t, prov, acDave), map[string]string{"Sec-Fetch-Site": "none", "Sec-Fetch-Dest": "document"})
			require.Equal(t, http.StatusFound, response.Code)
		})

		t.Run(prov.id+": not the exact path, or not a GET", func(t *testing.T) {
			s := newAccountsScene(t)
			ticket := s.ticket(t, prov, acDave)
			requirePage(t, s.serve(http.MethodPost, prov.connectPath+"?t="+ticket, acDave, web(acDave), "", nil), http.StatusNotFound, "Start from Matty")
			requirePage(t, s.serve(http.MethodGet, prov.connectPath+"/x?t="+ticket, acDave, web(acDave), "", nil), http.StatusNotFound, "Start from Matty")
			requirePage(t, s.serve(http.MethodGet, prov.callbackPath+"/x", acDave, web(acDave), "", nil), http.StatusNotFound, "This link has run out")
			require.NotNil(t, s.kv(prov.id+"_ticket_"+acDave))
		})
	}
}

func TestAccountConnectStoresAStateBoundToTheUser(t *testing.T) {
	for _, prov := range accountProviders {
		s := newAccountsScene(t)

		state, _ := s.startConnect(t, prov, acDave)

		var record accountState
		require.NoError(t, json.Unmarshal(s.kv(prov.id+"_state_"+state), &record))
		require.Equal(t, prov.id, record.Provider)
		require.Equal(t, acDave, record.User)
		require.Equal(t, web(acDave), record.Session)
		require.Equal(t, accountModeWeb, record.Mode)
		require.Equal(t, s.now().UnixMilli(), record.At)
		require.EqualValues(t, 600, s.expiryOf(prov.id+"_state_"+state))
		require.Nil(t, s.kv(prov.id+"_ticket_"+acDave), "the ticket is spent")
		require.Empty(t, s.fake.requests(), "the server asks the service nothing; the browser goes there")
	}
}

func TestAccountConnectPages(t *testing.T) {
	type page struct {
		name    string
		user    string
		session string
		prepare func(t *testing.T, s *accountsScene, prov *accountProvider)
		status  int
		heading string
		says    string
	}

	pages := []page{
		{name: "off", prepare: func(_ *testing.T, s *accountsScene, _ *accountProvider) {
			s.configure(func(cfg *configuration) { cfg.EnableSteam, cfg.EnableHytale = false, false })
		}, status: http.StatusNotFound, heading: "is switched off"},
		{name: "no Site URL", prepare: func(_ *testing.T, s *accountsScene, _ *accountProvider) { s.setSite("") }, status: http.StatusServiceUnavailable, heading: "Not set up yet", says: "An admin needs to finish setting up"},
		{name: "an http Site URL, to an admin", user: acBoss, prepare: func(_ *testing.T, s *accountsScene, _ *accountProvider) {
			s.setSite("http://chat.example.org")
		}, status: http.StatusServiceUnavailable, heading: "Not set up yet", says: "https://"},
		{name: "http://localhost", prepare: func(_ *testing.T, s *accountsScene, _ *accountProvider) { s.setSite("http://localhost:8065") }, status: http.StatusFound},
		{name: "signed out", session: "-", status: http.StatusUnauthorized, heading: "Sign in first"},
		{name: "an integration session", session: "pat-" + acDave, status: http.StatusUnauthorized, heading: "Sign in first"},
		{name: "blocked", prepare: func(t *testing.T, s *accountsScene, prov *accountProvider) {
			s.putLinks(t, prov, &accountLinks{Users: map[string]accountLink{}, Blocked: map[string]int64{acDave: 1}})
		}, status: http.StatusForbidden, heading: "Not allowed", says: "turned off Connect"},
		{name: "full", prepare: func(t *testing.T, s *accountsScene, prov *accountProvider) {
			doc := &accountLinks{Users: map[string]accountLink{}}
			for i := 0; i < maxAccountLinks; i++ {
				doc.Users[strings.Repeat("z", 6)+strings.Repeat("0", 14)+string(rune('a'+i/26))+string(rune('a'+i%26))+"0000"] = accountLink{ID: "id" + string(rune('a'+i/26)) + string(rune('a'+i%26)), Name: "someone"}
			}
			s.putLinks(t, prov, doc)
		}, status: http.StatusConflict, heading: "No room", says: "100"},
		{name: "a store failure", prepare: func(_ *testing.T, s *accountsScene, prov *accountProvider) {
			s.faults.failRead(prov.id + "_ticket_" + acDave)
		}, status: http.StatusServiceUnavailable, heading: "Could not save"},
		{name: "unlinked since the ticket", prepare: func(_ *testing.T, s *accountsScene, _ *accountProvider) {
			s.setKV(kvLinkByUserPrefix+acDave, nil)
		}, status: http.StatusForbidden, heading: "Not allowed", says: "unlocked Matty with their bearer token"},
	}

	for _, prov := range accountProviders {
		for _, tried := range pages {
			t.Run(prov.id+": "+tried.name, func(t *testing.T) {
				s := newAccountsScene(t)
				user := tried.user
				if user == "" {
					user = acDave
				}
				ticket := s.ticket(t, prov, user)
				if tried.prepare != nil {
					tried.prepare(t, s, prov)
				}

				session := tried.session
				header := user
				switch session {
				case "":
					session = web(user)
				case "-":
					session, header = "", ""
				}

				response := s.connectWith(prov, header, session, ticket, nil)
				require.Equal(t, tried.status, response.Code, response.Body.String())
				if tried.status == http.StatusFound {
					return
				}
				require.Contains(t, response.Body.String(), tried.heading)
				require.Contains(t, response.Body.String(), tried.says)
				require.Empty(t, response.Header().Get("Location"))
				require.Empty(t, s.keysWith(prov.id+"_state_"))
			})
		}
	}
}

func TestAccountCallbackRefusesAnotherAccountAndIntegrations(t *testing.T) {
	for _, prov := range accountProviders {
		for _, tried := range []struct {
			name, user, session string
			status              int
			heading             string
		}{
			{name: "another person", user: acErin, session: web(acErin), status: http.StatusForbidden, heading: "Started by another account"},
			{name: "a personal access token", user: acDave, session: "pat-" + acDave, status: http.StatusUnauthorized, heading: "Sign in first"},
			{name: "a bot", user: acDave, session: "bot-" + acDave, status: http.StatusUnauthorized, heading: "Sign in first"},
			{name: "an OAuth app", user: acDave, session: "app-" + acDave, status: http.StatusUnauthorized, heading: "Sign in first"},
			{name: "signed out", status: http.StatusUnauthorized, heading: "Sign in first"},
		} {
			t.Run(prov.id+": "+tried.name, func(t *testing.T) {
				s := newAccountsScene(t)
				state, location := s.startConnect(t, prov, acDave)

				query := ""
				if prov.id == accountSteam {
					query = steamReturn(state, s.steamAssertion(s.steamCallbackFor(state), daveSteamID))
				} else {
					s.grantHytale(t, location, "code-x", hytaleGrant{})
					query = "code=code-x&state=" + state
				}

				response := s.callback(prov, tried.user, tried.session, query)
				requirePage(t, response, tried.status, tried.heading)
				require.Nil(t, s.kv(prov.id+"_state_"+state), "the state is spent either way")
				require.Empty(t, s.fake.requests(), "nobody asks the service anything for somebody else")
				require.Empty(t, s.links(t, prov).Users)

				// And it cannot be finished afterwards by the right person.
				requirePage(t, s.callback(prov, acDave, web(acDave), query), http.StatusBadRequest, "This link has run out")
			})
		}
	}
}

func TestAnAppStateIsNeverFinishedByABrowser(t *testing.T) {
	for _, prov := range accountProviders {
		s := newAccountsScene(t)

		state, location := s.appStart(t, prov, acDave)
		require.NotNil(t, s.kv(prov.id+"_state_"+state))

		query := ""
		if prov.id == accountSteam {
			query = steamReturn(state, s.steamAssertion(s.steamCallbackFor(state), daveSteamID))
		} else {
			s.grantHytale(t, location, "code-app", hytaleGrant{})
			query = "code=code-app&state=" + state
		}

		// Somebody's browser arrives with it: Dave's own, or anybody's.
		for _, caller := range []string{acDave, acErin} {
			response := s.callback(prov, caller, web(caller), query)
			if caller == acDave {
				requirePage(t, response, http.StatusBadRequest, "Finish in Matty")
				require.Contains(t, response.Body.String(), "started in the Matty app")
				require.NotContains(t, response.Body.String(), "<script>", "no close script: the person is not done")
			} else {
				requirePage(t, response, http.StatusBadRequest, "This link has run out")
			}
		}

		require.Nil(t, s.kv(prov.id+"_state_"+state), "spent")
		require.Empty(t, s.fake.requests(), "no outside request")
		require.Empty(t, s.links(t, prov).Users, "nothing linked")

		// Matty finds nothing left to finish.
		requireRefused(t, s.appFinish(prov, acDave, matty(acDave), query), http.StatusBadRequest, accountCodeExpired, "spent by the browser")
	}
}

func TestAppFinishNeedsTheSameSessionAndAnAppState(t *testing.T) {
	queryFor := func(t *testing.T, s *accountsScene, prov *accountProvider, state string, location *url.URL) string {
		t.Helper()

		if prov.id == accountSteam {
			return steamReturn(state, s.steamAssertion(s.steamCallbackFor(state), daveSteamID))
		}
		s.grantHytale(t, location, "code-"+state[:8], hytaleGrant{})

		return "code=code-" + state[:8] + "&state=" + state
	}

	for _, prov := range accountProviders {
		t.Run(prov.id+": another person's state", func(t *testing.T) {
			s := newAccountsScene(t)
			state, location := s.appStart(t, prov, acDave)
			query := queryFor(t, s, prov, state, location)

			requireRefused(t, s.appFinish(prov, acErin, matty(acErin), query), http.StatusForbidden, accountCodeOtherSession, "erin")
			require.Nil(t, s.kv(prov.id+"_state_"+state), "spent")
			requireRefused(t, s.appFinish(prov, acDave, matty(acDave), query), http.StatusBadRequest, accountCodeExpired, "dave, afterwards")
			require.Empty(t, s.fake.requests())
		})

		t.Run(prov.id+": another session's state", func(t *testing.T) {
			s := newAccountsScene(t)
			state, location := s.appStart(t, prov, acDave)
			query := queryFor(t, s, prov, state, location)

			requireRefused(t, s.appFinish(prov, acDave, "tab-"+acDave, query), http.StatusForbidden, accountCodeOtherSession, "a second session")
			require.Nil(t, s.kv(prov.id+"_state_"+state))
			require.Empty(t, s.links(t, prov).Users)
		})

		t.Run(prov.id+": a state the browser started", func(t *testing.T) {
			s := newAccountsScene(t)
			state, location := s.startConnect(t, prov, acDave)
			query := queryFor(t, s, prov, state, location)

			requireRefused(t, s.appFinish(prov, acDave, web(acDave), query), http.StatusForbidden, accountCodeOtherSession, "a web state")
			require.Nil(t, s.kv(prov.id+"_state_"+state))
			require.Empty(t, s.fake.requests())
		})

		t.Run(prov.id+": a body that cannot be read", func(t *testing.T) {
			s := newAccountsScene(t)
			for _, body := range []string{"", "not json", `{"query":"%zz"}`, `{"query":"` + strings.Repeat("a", maxAccountAppFinishBytes) + `"}`} {
				response := s.serve(http.MethodPost, appPath(prov, "finish"), acDave, matty(acDave), body, nil)
				requireRefused(t, response, http.StatusBadRequest, accountCodeBadRequest, body[:min(len(body), 20)])
			}
			requireRefused(t, s.appFinish(prov, acDave, matty(acDave), "state=nope"), http.StatusBadRequest, accountCodeExpired, "no such state")
		})

		t.Run(prov.id+": the same session finishes", func(t *testing.T) {
			s := newAccountsScene(t)
			s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3})
			state, location := s.appStart(t, prov, acDave)
			query := queryFor(t, s, prov, state, location)

			response := s.appFinish(prov, acDave, matty(acDave), query)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

			var answer struct {
				OK      bool      `json:"ok"`
				Account meAccount `json:"account"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer))
			require.True(t, answer.OK)
			require.True(t, answer.Account.Available)
			require.True(t, answer.Account.Connected)
			if prov.id == accountSteam {
				require.Equal(t, "Dave", answer.Account.Name)
				require.Equal(t, "https://steamcommunity.com/profiles/"+daveSteamID, answer.Account.URL)
			} else {
				require.Equal(t, "Steve_Hytale", answer.Account.Name)
			}
			require.Equal(t, []string{acDave}, s.accountEvents(t))
		})
	}
}

// What Matty is told when it starts, exactly (design/3.4.0.md §6.4).
func TestAppConnectAnswersWhereMattyGoesAndWhereToStop(t *testing.T) {
	for _, prov := range accountProviders {
		s := newAccountsScene(t)

		response := s.appConnect(prov, acDave, matty(acDave))
		require.Equal(t, http.StatusOK, response.Code)
		require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

		var answer map[string]any
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer))
		require.Len(t, answer, 3)
		require.EqualValues(t, 600, answer["expiresIn"])
		require.Equal(t, "https://chat.example.org/plugins/ceo.villa.matty/api/v1/"+prov.id+"/callback", answer["finishAt"])
		require.True(t, strings.HasPrefix(answer["url"].(string), s.fake.server.URL+"/"), answer["url"])

		states := s.keysWith(prov.id + "_state_")
		require.Len(t, states, 1)
		var record accountState
		require.NoError(t, json.Unmarshal(s.kv(states[0]), &record))
		require.Equal(t, accountModeApp, record.Mode)
		require.Equal(t, matty(acDave), record.Session)
		require.Empty(t, s.fake.requests())

		// A full server, a blocked caller and a switch off are refused with
		// their codes, never a redirect.
		s.putLinks(t, prov, &accountLinks{Users: map[string]accountLink{}, Blocked: map[string]int64{acDave: 1}})
		requireRefused(t, s.appConnect(prov, acDave, matty(acDave)), http.StatusForbidden, accountCodeBlocked, "blocked")
		s.configure(func(cfg *configuration) { cfg.EnableSteam, cfg.EnableHytale = false, false })
		requireRefused(t, s.appConnect(prov, acDave, matty(acDave)), http.StatusNotFound, accountCodeOff, "off")
	}
}

func TestAnUnlinkInsideTheStateStopsTheConnection(t *testing.T) {
	for _, prov := range accountProviders {
		t.Run(prov.id+": in the browser", func(t *testing.T) {
			s := newAccountsScene(t)
			state, location := s.startConnect(t, prov, acDave)
			query := ""
			if prov.id == accountSteam {
				query = steamReturn(state, s.steamAssertion(s.steamCallbackFor(state), daveSteamID))
			} else {
				s.grantHytale(t, location, "code-u", hytaleGrant{})
				query = "code=code-u&state=" + state
			}

			s.setKV(kvLinkByUserPrefix+acDave, nil)

			response := s.callback(prov, acDave, web(acDave), query)
			requirePage(t, response, http.StatusForbidden, "Not allowed")
			require.Contains(t, response.Body.String(), "unlocked Matty with their bearer token")
			require.Nil(t, s.kv(prov.id+"_state_"+state), "spent")
			require.Empty(t, s.fake.requests())
			require.Empty(t, s.links(t, prov).Users)
		})

		t.Run(prov.id+": in Matty", func(t *testing.T) {
			s := newAccountsScene(t)
			state, location := s.appStart(t, prov, acDave)
			query := ""
			if prov.id == accountSteam {
				query = steamReturn(state, s.steamAssertion(s.steamCallbackFor(state), daveSteamID))
			} else {
				s.grantHytale(t, location, "code-u", hytaleGrant{})
				query = "code=code-u&state=" + state
			}

			s.setKV(kvLinkByUserPrefix+acDave, nil)

			requireRefused(t, s.appFinish(prov, acDave, matty(acDave), query), http.StatusForbidden, accessCodeLocked, "unlinked")
			require.Empty(t, s.fake.requests())
			require.Empty(t, s.links(t, prov).Users)
		})
	}
}

func TestAnExpiredStateIsRefusedWithoutAskingAnybody(t *testing.T) {
	for _, prov := range accountProviders {
		s := newAccountsScene(t)
		state, location := s.startConnect(t, prov, acDave)
		query := ""
		if prov.id == accountSteam {
			query = steamReturn(state, s.steamAssertion(s.steamCallbackFor(state), daveSteamID))
		} else {
			s.grantHytale(t, location, "code-late", hytaleGrant{})
			query = "code=code-late&state=" + state
		}

		// A compare-and-delete does not look at expiry, so the age is read
		// from the record itself.
		s.clock.advance(11 * time.Minute)

		requirePage(t, s.callback(prov, acDave, web(acDave), query), http.StatusBadRequest, "This link has run out")
		require.Nil(t, s.kv(prov.id+"_state_"+state))
		require.Empty(t, s.fake.requests())

		// Two states in one query, or none, are no state at all.
		requirePage(t, s.callback(prov, acDave, web(acDave), "state="+state+"&state="+state), http.StatusBadRequest, "This link has run out")
		requirePage(t, s.callback(prov, acDave, web(acDave), ""), http.StatusBadRequest, "This link has run out")
	}
}

func TestAccountTakeoverLeavesANotice(t *testing.T) {
	s := newAccountsScene(t)
	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3})

	s.connectSteam(t, acDave, daveSteamID)
	before := len(s.accountEvents(t))

	// Erin proves the same Steam account: it moves to her.
	taken := s.connectSteam(t, acErin, daveSteamID)
	require.Contains(t, taken.Body.String(), "It was connected to another Mattermost account before.")

	doc := s.links(t, steamProvider)
	require.Equal(t, daveSteamID, doc.Users[acErin].ID)
	require.NotContains(t, doc.Users, acDave)
	require.Equal(t, accountLost{ID: daveSteamID, Name: "Dave", At: s.now().UnixMilli()}, doc.Lost[acDave])
	require.ElementsMatch(t, []string{acErin, acDave}, s.accountEvents(t)[before:], "both are told")

	// Dave's /me says so, by name and time only: the account's id never
	// leaves the server.
	response := s.serve(http.MethodGet, "/api/v1/me", acDave, web(acDave), "", nil)
	var raw struct {
		Steam map[string]json.RawMessage `json:"steam"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &raw))
	require.JSONEq(t, `{"name":"Dave","at":`+jsonNumber(s.now().UnixMilli())+`}`, string(raw.Steam["lost"]))
	require.NotContains(t, response.Body.String(), daveSteamID)
	require.Nil(t, raw.Steam["connected"])

	// Reading it dismisses it, and pressing again is harmless.
	for i := 0; i < 2; i++ {
		ok := s.serve(http.MethodPost, "/api/v1/steam/notice", acDave, web(acDave), "", nil)
		require.Equal(t, http.StatusOK, ok.Code)
	}
	require.NotContains(t, s.links(t, steamProvider).Lost, acDave)
	require.Nil(t, s.me(t, acDave).Steam.Lost)

	// A notice nobody reads is gone after 30 days, on the next write.
	s.connectSteam(t, acDave, daveSteamID)
	require.Contains(t, s.links(t, steamProvider).Lost, acErin)
	s.clock.advance(31 * 24 * time.Hour)
	require.Nil(t, s.me(t, acErin).Steam.Lost, "not shown once it is old")
	require.Contains(t, s.links(t, steamProvider).Lost, acErin, "still stored until the next write")
	require.Equal(t, http.StatusOK, s.serve(http.MethodPost, "/api/v1/steam/notice", acBob, web(acBob), "", nil).Code)
	require.NotContains(t, s.links(t, steamProvider).Lost, acErin, "gone with somebody else's write")
}

func jsonNumber(value int64) string {
	encoded, _ := json.Marshal(value)

	return string(encoded)
}

func TestRemovingAnAccountRemovesTheNoticesNamingIt(t *testing.T) {
	// Dave owned Steve's profile, Erin took it over, so Dave holds a notice
	// naming it; Bob holds a notice about an account that is still connected
	// to somebody, which nothing here removes.
	seed := func(t *testing.T) *accountsScene {
		s := newAccountsScene(t)
		s.putLinks(t, hytaleProvider, &accountLinks{
			Users: map[string]accountLink{
				acErin: {ID: daveHytaleUUID, Name: "Steve_Hytale", At: 1},
				acBoss: {ID: erinHytaleUUID, Name: "Other", At: 1},
			},
			Lost: map[string]accountLost{
				acDave: {ID: daveHytaleUUID, Name: "Steve_Hytale", At: s.now().UnixMilli()},
				acBob:  {ID: erinHytaleUUID, Name: "Other", At: s.now().UnixMilli()},
			},
		})

		return s
	}

	for _, tried := range []struct {
		name   string
		remove func(t *testing.T, s *accountsScene)
	}{
		{"Erin disconnects", func(t *testing.T, s *accountsScene) {
			require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/hytale/link", acErin, web(acErin), "", nil).Code)
		}},
		{"an admin removes Erin", func(t *testing.T, s *accountsScene) {
			require.NoError(t, s.p.disconnectAccount(hytaleProvider, acErin, false, s.now().UnixMilli()))
		}},
		{"an admin removes and blocks Erin", func(t *testing.T, s *accountsScene) {
			require.NoError(t, s.p.disconnectAccount(hytaleProvider, acErin, true, s.now().UnixMilli()))
			require.Contains(t, s.links(t, hytaleProvider).Blocked, acErin)
		}},
		{"Erin connects another profile instead", func(t *testing.T, s *accountsScene) {
			s.connectHytale(t, acErin, "123e4567-e89b-42d3-a456-426614174009", "Erin_Builds")
		}},
	} {
		t.Run(tried.name, func(t *testing.T) {
			s := seed(t)
			writes := s.faults.writesTo(kvHytaleLinks)

			tried.remove(t, s)

			doc := s.links(t, hytaleProvider)
			require.NotContains(t, doc.Lost, acDave, "the notice went with the account it names")
			require.Contains(t, doc.Lost, acBob, "a notice naming another account is kept")
			require.Equal(t, writes+1, s.faults.writesTo(kvHytaleLinks), "in the same write")
			require.Contains(t, s.accountEvents(t), acDave, "and Dave is told")
		})
	}
}

func TestDisconnectIsOpenIdempotentAndWorksWhileOff(t *testing.T) {
	s := newAccountsScene(t)
	s.connectedSteam(t)
	s.connectHytale(t, acDave, daveHytaleUUID, "Steve_Hytale")
	s.p.accounts().lock.Lock()
	s.p.accounts().steamLastGood = s.now()
	s.p.accounts().lock.Unlock()

	_, body := s.accountsAnswer(t, acErin)
	require.Contains(t, string(body), acDave)

	// Gone from everybody's view the moment he presses it.
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/steam/link", acDave, web(acDave), "", nil).Code)
	_, body = s.accountsAnswer(t, acErin)
	require.NotContains(t, string(body), `"steam":{"name"`)
	s.p.accounts().lock.Lock()
	require.NotContains(t, s.p.accounts().steam, daveSteamID, "and from memory")
	s.p.accounts().lock.Unlock()

	// A locked caller, with both switches off, and nothing left to take.
	s.putLinks(t, hytaleProvider, &accountLinks{Users: map[string]accountLink{acDan: {ID: erinHytaleUUID, Name: "Dan_Plays", At: 1}}})
	s.configure(func(cfg *configuration) { cfg.EnableSteam, cfg.EnableHytale = false, false })
	for i := 0; i < 2; i++ {
		for _, prov := range accountProviders {
			response := s.serve(http.MethodDelete, "/api/v1/"+prov.id+"/link", acDan, web(acDan), "", nil)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.JSONEq(t, `{"ok":true}`, response.Body.String())
		}
	}
	require.NotContains(t, s.links(t, hytaleProvider).Users, acDan)

	// Nothing to take is no write at all.
	writes := s.faults.writesTo(kvSteamLinks)
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, "/api/v1/steam/link", acDan, web(acDan), "", nil).Code)
	require.Equal(t, writes, s.faults.writesTo(kvSteamLinks))

	// A store that does not answer is said so, with a code.
	s.faults.failRead(kvHytaleLinks)
	failed := s.serve(http.MethodDelete, "/api/v1/hytale/link", acDave, web(acDave), "", nil)
	requireRefused(t, failed, http.StatusServiceUnavailable, accountCodeStore, "store")
	require.Contains(t, failed.Body.String(), "Matty could not disconnect Hytale just now.")
}

func TestDeleteAllKeepsTheBlockList(t *testing.T) {
	s := newAccountsScene(t)
	s.connectedSteam(t)
	s.putLinks(t, steamProvider, &accountLinks{
		Users:   map[string]accountLink{acDave: {ID: daveSteamID, Name: "Dave", At: 1}},
		Blocked: map[string]int64{acDan: 5},
		Lost:    map[string]accountLost{acBob: {ID: bobSteamID, Name: "Bob", At: s.now().UnixMilli()}},
	})
	s.setKV(kvQuarantinePrefix+kvSteamLinks, []byte("{damaged"))
	s.setKV("steam_ticket_"+acErin, []byte(`{}`))
	s.setKV("steam_state_"+strings.Repeat("a", 64), []byte(`{}`))
	s.setKV(kvSteamNoncePrefix+strings.Repeat("b", 64), []byte(`{}`))
	s.setKV("hytale_ticket_"+acErin, []byte(`{}`))
	s.connectHytale(t, acDave, daveHytaleUUID, "Steve_Hytale")

	// A store that will not answer the read writes nothing at all.
	s.faults.failRead(kvSteamLinks)
	require.Error(t, s.p.deleteAllAccountData(steamProvider, s.now()))
	s.faults.healRead(kvSteamLinks)
	require.Contains(t, s.links(t, steamProvider).Users, acDave)
	require.NotNil(t, s.kv(kvQuarantinePrefix+kvSteamLinks))
	require.NotNil(t, s.kv("steam_ticket_"+acErin))

	require.NoError(t, s.p.deleteAllAccountData(steamProvider, s.now()))

	doc := s.links(t, steamProvider)
	require.Empty(t, doc.Users)
	require.Empty(t, doc.Lost)
	require.Equal(t, map[string]int64{acDan: 5}, doc.Blocked, "the block list is kept")
	require.Equal(t, s.now().UnixMilli(), doc.Cleared, "and when everything else went")
	require.Nil(t, s.kv(kvQuarantinePrefix+kvSteamLinks))
	require.Empty(t, s.keysWith("steam_ticket_"))
	require.Empty(t, s.keysWith("steam_state_"))
	require.Empty(t, s.keysWith(kvSteamNoncePrefix))
	s.p.accounts().lock.Lock()
	require.Empty(t, s.p.accounts().steam, "statuses gone from memory")
	s.p.accounts().lock.Unlock()

	// Hytale's are not Steam's to delete.
	require.NotNil(t, s.kv("hytale_ticket_"+acErin))
	require.Contains(t, s.links(t, hytaleProvider).Users, acDave)

	// Everybody's panels read /me again.
	events := s.accountEvents(t)
	require.Equal(t, "", events[len(events)-1])

	// An empty store gets nothing but when it was emptied, which a first
	// connection still on its way must be able to see
	// (TestDeleteAllStopsAConnectionAlreadyOnItsWay).
	empty := newAccountsScene(t)
	require.NoError(t, empty.p.deleteAllAccountData(hytaleProvider, empty.now()))
	require.JSONEq(t, fmt.Sprintf(`{"v":1,"users":{},"cleared":%d}`, empty.now().UnixMilli()), string(empty.kv(kvHytaleLinks)))
}

func TestDeleteAllStopsAConnectionAlreadyOnItsWay(t *testing.T) {
	// Dave's connection has spent its state and is still talking to the
	// service (Steam's name lookup, Hytale's userinfo, held open here) when
	// an admin deletes all of that service's data. When it comes back it is
	// refused, and nothing of it is written: in the browser, in Matty, and
	// with the switch turned off first, as the card tells an admin to do
	// when they stop using the service for good.
	for _, prov := range accountProviders {
		for _, how := range []string{"browser", "Matty", "switched off first"} {
			t.Run(prov.id+", "+how, func(t *testing.T) {
				s := newAccountsScene(t)
				s.putLinks(t, prov, &accountLinks{Blocked: map[string]int64{acDan: 5}})
				s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3})

				var state string
				var location *url.URL
				if how == "Matty" {
					state, location = s.appStart(t, prov, acDave)
				} else {
					state, location = s.startConnect(t, prov, acDave)
				}
				var query string
				var arrived <-chan struct{}
				var release func()
				if prov.id == accountSteam {
					query = steamReturn(state, s.steamAssertion(s.steamCallbackFor(state), daveSteamID))
					arrived, release = s.steam.holdSummaries()
				} else {
					s.grantHytale(t, location, "code-dave", hytaleGrant{info: hytaleProfile(daveHytaleUUID, "Steve_Hytale")})
					query = "code=code-dave&scope=openid+hytale%3Aprofile&state=" + state
					arrived, release = s.hytale.holdUserinfo()
				}
				t.Cleanup(release)

				finished := make(chan *httptest.ResponseRecorder, 1)
				go func() {
					if how == "Matty" {
						finished <- s.appFinish(prov, acDave, matty(acDave), query)
					} else {
						finished <- s.callback(prov, acDave, web(acDave), query)
					}
				}()
				<-arrived
				require.Nil(t, s.kv(prov.stateKey(state)), "the state is spent: Delete all cannot reach this one")

				if how == "switched off first" {
					previous := s.p.getConfiguration().Clone()
					s.configure(func(cfg *configuration) { cfg.EnableSteam, cfg.EnableHytale = false, false })
					s.p.restartAccountsLive(previous)
				}
				s.adminDo(t, http.MethodDelete, "/api/v1/admin/"+prov.id+"?all=1", acBoss, "")
				release()
				response := <-finished

				switch how {
				case "Matty":
					requireRefused(t, response, http.StatusBadRequest, accountCodeExpired, "app-finish")
				case "browser":
					require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
					require.Contains(t, response.Body.String(), "This link has run out")
				default:
					require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
					require.Contains(t, response.Body.String(), "is switched off")
				}

				doc := s.links(t, prov)
				require.Empty(t, doc.Users, "nothing written after Delete all")
				require.Equal(t, map[string]int64{acDan: 5}, doc.Blocked)
				for _, gone := range []string{daveSteamID, daveHytaleUUID, "Steve_Hytale", acDave} {
					require.NotContains(t, string(s.kv(prov.linksKey)), gone)
				}
				require.Empty(t, s.p.accounts().cachedLinks(prov).Users, "nor cached")
				s.p.accounts().lock.Lock()
				require.Empty(t, s.p.accounts().steam, "nor seeded")
				s.p.accounts().lock.Unlock()
			})
		}
	}

	// A connection started after Delete all goes through as ever.
	s := newAccountsScene(t)
	require.NoError(t, s.p.deleteAllAccountData(steamProvider, s.now()))
	s.clock.advance(time.Millisecond)
	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3})
	s.connectSteam(t, acDave, daveSteamID)
	require.True(t, s.me(t, acDave).Steam.Connected)
	require.Equal(t, s.now().Add(-time.Millisecond).UnixMilli(), s.links(t, steamProvider).Cleared, "and the time stays")
}

func TestAnUnreadableLinksDocumentIsSetAside(t *testing.T) {
	s := newAccountsScene(t)
	s.setKV(kvSteamLinks, []byte("{not a document"))

	requireRefused(t, s.serve(http.MethodPost, ticketPath(steamProvider), acDave, web(acDave), "", nil), http.StatusServiceUnavailable, accountCodeStore, "ticket")
	require.Equal(t, []byte("{not a document"), s.kv(kvQuarantinePrefix+kvSteamLinks), "set aside")
	require.Equal(t, []byte("{not a document"), s.kv(kvSteamLinks), "and never overwritten")

	// A later, different damage does not replace the first copy.
	s.setKV(kvSteamLinks, []byte(`{"v":9}`))
	requireRefused(t, s.appConnect(steamProvider, acDave, matty(acDave)), http.StatusServiceUnavailable, accountCodeStore, "app-connect")
	requireRefused(t, s.serve(http.MethodDelete, "/api/v1/steam/link", acDave, web(acDave), "", nil), http.StatusServiceUnavailable, accountCodeStore, "disconnect")
	require.Equal(t, []byte("{not a document"), s.kv(kvQuarantinePrefix+kvSteamLinks))
	require.Equal(t, []byte(`{"v":9}`), s.kv(kvSteamLinks))

	// Nothing of Steam shows, and an admin is told.
	_, body := s.accountsAnswer(t, acErin)
	require.Contains(t, string(body), `"people":{}`)
	require.Contains(t, s.me(t, acBoss).Steam.Problem, "quarantine_steam_links")
	require.Empty(t, s.me(t, acDave).Steam.Problem)
	require.True(t, s.p.accountQuarantined(steamProvider))

	// Hytale is not affected.
	require.Regexp(t, hex64, s.ticket(t, hytaleProvider, acDave))

	// Delete all starts again.
	require.NoError(t, s.p.deleteAllAccountData(steamProvider, s.now()))
	require.False(t, s.p.accountQuarantined(steamProvider))
	require.Regexp(t, hex64, s.ticket(t, steamProvider, acDave))
	require.Empty(t, s.me(t, acBoss).Steam.Problem)
}

func TestMeAccountFields(t *testing.T) {
	s := newAccountsScene(t)

	// On, nothing connected: Connect can be offered.
	me := s.me(t, acDave)
	require.Equal(t, &meAccount{Available: true}, me.Steam)
	require.Equal(t, &meAccount{Available: true}, me.Hytale)
	require.NotNil(t, s.me(t, acDan).Steam, "in any access state")

	// Connected: the name and a profile link built from the SteamID.
	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 1})
	s.connectSteam(t, acDave, daveSteamID)
	s.connectHytale(t, acDave, daveHytaleUUID, "Steve_Hytale")
	me = s.me(t, acDave)
	require.Equal(t, &meAccount{Available: true, Connected: true, Name: "Dave", URL: "https://steamcommunity.com/profiles/" + daveSteamID, Hidden: true}, me.Steam)
	require.Equal(t, &meAccount{Available: true, Connected: true, Name: "Steve_Hytale"}, me.Hytale)

	// One read of each document per /me.
	reads := 0
	s.faults.setOnRead(func(key string) {
		if key == kvSteamLinks {
			reads++
		}
	})
	s.me(t, acDave)
	s.faults.setOnRead(nil)
	require.Equal(t, 1, reads)

	// Blocked: nothing to offer.
	s.putLinks(t, hytaleProvider, &accountLinks{Users: map[string]accountLink{}, Blocked: map[string]int64{acErin: 1}})
	require.Equal(t, &meAccount{Available: true, Blocked: true}, s.me(t, acErin).Hytale)

	// A problem is for system admins only.
	s.setSite("http://chat.example.org")
	require.Equal(t, &meAccount{Available: false}, s.me(t, acErin).Steam)
	require.Contains(t, s.me(t, acBoss).Steam.Problem, "https://")
	require.Contains(t, s.me(t, acChief).Hytale.Problem, "Redirect URI", "locked admins too")
	s.setSite("https://chat.example.org")

	// Off: absent, unless there is something to take back.
	s.configure(func(cfg *configuration) { cfg.EnableSteam, cfg.EnableHytale = false, false })
	require.Nil(t, s.me(t, acErin).Steam)
	require.Nil(t, s.me(t, acErin).Hytale)
	require.Equal(t, &meAccount{Connected: true, Name: "Dave", URL: "https://steamcommunity.com/profiles/" + daveSteamID, Hidden: true}, s.me(t, acDave).Steam)
	require.Empty(t, s.me(t, acBoss).Steam, "no problem while off")

	// Raw: an absent service is absent, never null.
	response := s.serve(http.MethodGet, "/api/v1/me", acErin, web(acErin), "", nil)
	require.NotContains(t, response.Body.String(), `"steam"`)
	require.NotContains(t, response.Body.String(), `"hytale"`)
	require.Contains(t, response.Body.String(), `"access":"linked"`)
}

// accountPageScene is a request answered with one page, as the handlers
// answer: the headers first, then the page.
func renderAccountPage(p *Plugin, prov *accountProvider, refusal *accountRefusal, userID string, data *accountPageData) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	twitchPageHeaders(recorder)
	if data != nil {
		p.writeAccountPageData(recorder, prov, refusal.status, refusal.page, *data)
	} else {
		p.writeAccountPage(recorder, prov, refusal, userID)
	}

	return recorder
}

func TestAccountPagesRender(t *testing.T) {
	s := newAccountsScene(t)
	out := os.Getenv("VILLANET_PAGES_OUT")
	if out != "" {
		require.NoError(t, os.MkdirAll(out, 0o755))
	}

	csp := "default-src 'none'; style-src 'unsafe-inline'; script-src 'sha256-" + twitchPageScriptHash +
		"'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

	for _, prov := range accountProviders {
		for page := range accountPageWords {
			for _, admin := range []bool{false, true} {
				refusal := accountRefused(page, "")
				if page == accountPageSetup {
					refusal.problem = "Mattermost's Site URL is not set (System Console > Environment > Web Server > Site URL), so " + prov.name + " has nowhere to send people back to."
				}
				if page == accountPageScope {
					refusal.problem = accountScopeProblem
				}
				if admin && refusal.problem == "" {
					continue
				}

				user := acDave
				if admin {
					user = acBoss
				}
				response := renderAccountPage(s.p, prov, refusal, user, nil)
				about := prov.id + " " + page

				require.Equal(t, accountPageStatus[page], response.Code, about)
				require.Equal(t, "text/html; charset=utf-8", response.Header().Get("Content-Type"), about)
				require.Equal(t, "no-store", response.Header().Get("Cache-Control"), about)
				require.Equal(t, "no-referrer", response.Header().Get("Referrer-Policy"), about)
				require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"), about)
				require.Equal(t, csp, response.Header().Get("Content-Security-Policy"), about)

				html := response.Body.String()
				require.Equal(t, page == accountPageConnected || page == accountPageCancelled, strings.Contains(html, "<script>"+twitchPageScript+"</script>"), about)
				require.NotContains(t, html, "{{", about)
				require.Contains(t, html, "<h1>", about)
				if page != accountPageBusy && page != accountPageNoProfile && page != accountPageScope && page != accountPageStore {
					require.Contains(t, html, prov.name, about)
				}
				require.Equal(t, admin, strings.Contains(html, `class="problem"`), about)

				name := prov.id + "-" + page
				if admin {
					name += "-admin"
				}
				if out != "" {
					require.NoError(t, os.WriteFile(filepath.Join(out, name+".html"), response.Body.Bytes(), 0o644))
				}
			}
		}

		// The connected page, with every sentence it can carry, and the name
		// escaped.
		for _, variant := range []struct {
			name string
			data accountPageData
		}{
			{"connected-plain", accountPageData{Name: "Dave"}},
			{"connected-takenover", accountPageData{Name: "Dave", TakenOver: true}},
			{"connected-noname", accountPageData{}},
			{"connected-hidden", accountPageData{Name: "Dave", Hidden: prov.id == accountSteam, TakenOver: true}},
			{"connected-escaped", accountPageData{Name: `<b>M&"</b>`}},
		} {
			response := renderAccountPage(s.p, prov, accountRefused(accountPageConnected, ""), "", &variant.data)
			html := response.Body.String()
			require.Equal(t, http.StatusOK, response.Code)
			require.Contains(t, html, "<h1>"+prov.name+" connected</h1>")
			switch variant.name {
			case "connected-plain":
				require.Contains(t, html, "Connected as <strong>Dave</strong> ("+prov.nameKind+").")
			case "connected-takenover":
				require.Contains(t, html, "It is connected to yours now.")
			case "connected-noname":
				require.Contains(t, html, "Your "+prov.name+" account is connected.")
			case "connected-hidden":
				require.Equal(t, prov.id == accountSteam, strings.Contains(html, "Your Steam profile is not public"))
			case "connected-escaped":
				require.NotContains(t, html, "<b>M")
				require.Contains(t, html, "&lt;b&gt;M&amp;&#34;&lt;/b&gt;")
			}
			if out != "" {
				require.NoError(t, os.WriteFile(filepath.Join(out, prov.id+"-"+variant.name+".html"), response.Body.Bytes(), 0o644))
			}
		}
	}
}

func TestNoAccountLogLineCarriesASecret(t *testing.T) {
	s := newAccountsScene(t)
	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3})

	var secrets []string
	remember := func(values ...string) { secrets = append(secrets, values...) }
	remember(testSteamKey, testHytaleSecret, url.QueryEscape(testHytaleSecret), testHytaleAccessToken, testHytaleSub,
		daveSteamID, erinSteamID, bobSteamID, daveHytaleUUID, "Steve_Hytale", "openid.", "check_authentication", "steamids")

	// Every state's nonce and verifier, and every ticket, as they are made.
	noteStates := func() {
		for _, prefix := range []string{"steam_state_", "hytale_state_"} {
			for _, key := range s.keysWith(prefix) {
				var st accountState
				_ = json.Unmarshal(s.kv(key), &st)
				remember(strings.TrimPrefix(key, prefix), st.Nonce, st.Verifier)
			}
		}
	}

	// Steam: a connect, a refused check, a replay, a slow Steam, a key Steam
	// refuses, and Steam out of reach for the name.
	s.connectSteam(t, acDave, daveSteamID)
	for _, answer := range []struct {
		status int
		body   string
	}{{0, "ns:" + steamOpenIDNamespace + "\nis_valid:false\n"}, {http.StatusTooManyRequests, ""}, {http.StatusInternalServerError, ""}} {
		s.steam.answerChecks(answer.status, answer.body)
		state, _ := s.startConnect(t, steamProvider, acErin)
		noteStates()
		assertion := s.steamAssertion(s.steamCallbackFor(state), erinSteamID)
		remember(assertion.Get("openid.sig"), assertion.Get("openid.response_nonce"), state)
		s.callback(steamProvider, acErin, web(acErin), steamReturn(state, assertion))
		s.clock.advance(16 * time.Minute)
	}
	s.steam.answerChecks(0, "")
	s.steam.answerSummaries(http.StatusForbidden, "")
	s.connectSteam(t, acErin, erinSteamID)
	s.configure(func(cfg *configuration) { cfg.SteamWebAPIKey = "WRONG" + testSteamKey })
	remember("WRONG" + testSteamKey)
	s.connectSteam(t, acBob, bobSteamID)

	// Hytale: a connect, every refusal, and a revoke that fails.
	s.connectHytale(t, acDave, daveHytaleUUID, "Steve_Hytale")
	noteStates()
	for i, grant := range []hytaleGrant{
		{claims: map[string]any{"nonce": "somebody-elses"}},
		{info: map[string]any{"sub": "another", "profile": map[string]any{"uuid": erinHytaleUUID, "username": "Erin_Builds"}}},
		{info: map[string]any{"sub": testHytaleSub}},
		{infoType: "application/jwt"},
	} {
		state, location := s.startConnect(t, hytaleProvider, acErin)
		noteStates()
		code := "secret-code-" + string(rune('a'+i))
		remember(code)
		s.grantHytale(t, location, code, grant)
		s.callback(hytaleProvider, acErin, web(acErin), "code="+code+"&state="+state)
		s.clock.advance(16 * time.Minute)
	}
	state, _ := s.startConnect(t, hytaleProvider, acErin)
	s.callback(hytaleProvider, acErin, web(acErin), "error=line%0Abreak+"+strings.Repeat("x", 100)+"&error_description=secret+description&state="+state)
	remember("secret description", "line\nbreak")
	s.hytale.answerTokens(http.StatusUnauthorized, `{"error":"invalid_client","error_description":"secret description"}`)
	state, _ = s.startConnect(t, hytaleProvider, acErin)
	s.callback(hytaleProvider, acErin, web(acErin), "code=secret-code-z&state="+state)
	remember("secret-code-z")

	// The loop: a key Steam refuses, Steam out of reach, a name to write
	// back that cannot be saved, a list that cannot be read, Mattermost not
	// saying who people are; and the admin cards.
	store := s.p.accounts()
	s.step(t)
	s.configure(func(cfg *configuration) { cfg.SteamWebAPIKey = testSteamKey })
	store.liftSteamHold()
	s.steam.answerSummaries(http.StatusInternalServerError, "")
	for i := 0; i < steamProblemAfter; i++ {
		s.clock.advance(steamBackoffMax)
		s.step(t)
	}
	s.steam.answerSummaries(0, "")
	s.player(steamPlayer{SteamID: bobSteamID, Name: "Bob_Renamed", State: 1, Visibility: 3})
	s.faults.failWrite(kvSteamLinks)
	s.clock.advance(steamBackoffMax)
	s.step(t)
	s.faults.healWrite(kvSteamLinks)
	remember("Bob_Renamed")
	s.faults.failRead(kvSteamLinks)
	s.clock.advance(time.Minute)
	s.step(t)
	s.faults.healRead(kvSteamLinks)
	s.failUsers(true)
	s.clock.advance(time.Minute)
	s.step(t)
	s.failUsers(false)
	s.serve(http.MethodGet, "/api/v1/admin/steam", acBoss, web(acBoss), "", nil)
	s.serve(http.MethodGet, "/api/v1/admin/hytale", acBoss, web(acBoss), "", nil)
	s.serve(http.MethodDelete, "/api/v1/admin/hytale?user="+acErin+"&block=1", acBoss, web(acBoss), "", nil)

	// Matty, Disconnect, Delete all.
	s.hytale.answerTokens(0, "")
	appState, location := s.appStart(t, hytaleProvider, acBob)
	noteStates()
	s.grantHytale(t, location, "secret-app-code", hytaleGrant{})
	remember("secret-app-code", appState)
	s.appFinish(hytaleProvider, acBob, matty(acBob), "code=secret-app-code&state="+appState)
	s.serve(http.MethodDelete, "/api/v1/steam/link", acDave, web(acDave), "", nil)
	require.NoError(t, s.p.deleteAllAccountData(hytaleProvider, s.now()))

	lines := s.logged()
	require.NotEmpty(t, lines, "the paths above do log")
	for _, line := range lines {
		for _, secret := range secrets {
			if secret == "" {
				continue
			}
			require.NotContains(t, line, secret)
		}
	}
}

func TestAccountsViewShape(t *testing.T) {
	// Both off: on false twice, and people an empty object, on the raw JSON.
	off := newAccountsSceneWith(t, &configuration{BroadcastBoxURL: "http://192.0.2.10:8080"})
	code, body := off.accountsAnswer(t, acDave)
	require.Equal(t, http.StatusOK, code)
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &raw))
	require.JSONEq(t, `{"on":false,"fresh":false}`, string(raw["steam"]))
	require.JSONEq(t, `{"on":false}`, string(raw["hytale"]))
	require.Equal(t, "{}", string(raw["people"]))

	// On, nobody connected: still an object.
	s := newAccountsScene(t)
	_, body = s.accountsAnswer(t, acDave)
	require.NoError(t, json.Unmarshal(body, &raw))
	require.Equal(t, "{}", string(raw["people"]))
	require.JSONEq(t, `{"on":true,"fresh":false}`, string(raw["steam"]))
	require.JSONEq(t, `{"on":true}`, string(raw["hytale"]))
	emptyV := string(raw["v"])

	// People: blocked and deactivated left out, statuses only while fresh,
	// a game only for a public profile.
	s.putLinks(t, steamProvider, &accountLinks{Users: map[string]accountLink{
		acDave: {ID: daveSteamID, Name: "Dave", At: 1},
		acErin: {ID: erinSteamID, Name: "Erin", At: 1},
		acBob:  {ID: bobSteamID, Name: "Bob", At: 1},
	}})
	s.putLinks(t, hytaleProvider, &accountLinks{
		Users:   map[string]accountLink{acDave: {ID: daveHytaleUUID, Name: "Steve_Hytale", At: 1}},
		Blocked: map[string]int64{acDan: 1},
	})
	store := s.p.accounts()
	store.lock.Lock()
	store.steam[daveSteamID] = steamStatusOf(steamPlayer{SteamID: daveSteamID, State: 1, Visibility: 3, GameName: "ELDEN RING"})
	store.steam[erinSteamID] = steamStatusOf(steamPlayer{SteamID: erinSteamID, State: 1, Visibility: 1})
	store.steam[bossSteamID] = steamStatusOf(steamPlayer{SteamID: bossSteamID, State: 1, Visibility: 3})
	store.inactive[acBob] = true
	store.lock.Unlock()

	_, stale := s.accountsAnswer(t, acDave)
	require.Contains(t, string(stale), `"state":""`, "not fresh: names, not statuses")
	require.NotContains(t, string(stale), "ELDEN RING")

	store.lock.Lock()
	store.steamLastGood = s.now()
	store.lock.Unlock()

	_, body = s.accountsAnswer(t, acErin)
	require.JSONEq(t, `{
		"v": `+jsonString(t, raw, body)+`,
		"steam": {"on": true, "fresh": true},
		"hytale": {"on": true},
		"people": {
			"`+acDave+`": {
				"steam": {"name": "Dave", "url": "https://steamcommunity.com/profiles/`+daveSteamID+`", "state": "online", "inGame": true, "game": "ELDEN RING"},
				"hytale": {"name": "Steve_Hytale"},
				"activity": {"kind": "playing", "title": "ELDEN RING", "source": "steam"}
			},
			"`+acErin+`": {"steam": {"name": "Erin", "url": "https://steamcommunity.com/profiles/`+erinSteamID+`", "state": "offline"}}
		}
	}`, string(body))
	require.NotContains(t, string(body), bossSteamID, "a status no cached entry holds is never shown")
	require.Less(t, strings.Index(string(body), acDave), strings.Index(string(body), acErin), "keys sorted")

	// The same people are the same v; different people are another.
	_, again := s.accountsAnswer(t, acDave)
	require.Equal(t, string(body), string(again))
	require.NotEqual(t, emptyV, jsonString(t, raw, body))
}

// jsonString is the v of an accounts answer, as JSON.
func jsonString(t *testing.T, scratch map[string]json.RawMessage, body []byte) string {
	t.Helper()

	require.NoError(t, json.Unmarshal(body, &scratch))

	return string(scratch["v"])
}

func TestARenameNeverBringsBackADisconnectedAccount(t *testing.T) {
	s := newAccountsScene(t)
	s.putLinks(t, steamProvider, &accountLinks{Users: map[string]accountLink{
		acDave: {ID: daveSteamID, Name: "Dave", At: 1},
		acErin: {ID: erinSteamID, Name: "Erin", At: 1},
	}})

	// The read was out while Dave disconnected and Erin connected another
	// account.
	read := map[string]accountLink{
		acDave: {ID: daveSteamID, Name: "Dave2"},
		acErin: {ID: erinSteamID, Name: "Erin2"},
	}
	require.NoError(t, s.p.disconnectAccount(steamProvider, acDave, false, s.now().UnixMilli()))
	_, err := s.p.renameAccountLinks(steamProvider, map[string]accountLink{acErin: {ID: erinSteamID, Name: "Erin"}}, s.now().UnixMilli())
	require.NoError(t, err)
	s.putLinks(t, steamProvider, &accountLinks{Users: map[string]accountLink{acErin: {ID: bobSteamID, Name: "Bob", At: 2}}})

	doc, err := s.p.renameAccountLinks(steamProvider, read, s.now().UnixMilli())
	require.NoError(t, err)
	require.NotContains(t, doc.Users, acDave, "never created")
	require.Equal(t, accountLink{ID: bobSteamID, Name: "Bob", At: 2}, doc.Users[acErin], "another account is not renamed")
	require.Equal(t, doc, s.p.accounts().cachedLinks(steamProvider), "the write's own result is the cache")

	// An entry still connected to the same account is.
	doc, err = s.p.renameAccountLinks(steamProvider, map[string]accountLink{acErin: {ID: bobSteamID, Name: "Bob_Again"}}, s.now().UnixMilli())
	require.NoError(t, err)
	require.Equal(t, "Bob_Again", doc.Users[acErin].Name)
}

func TestALinkWriteIsKeptOverAStepThatReadBeforeIt(t *testing.T) {
	s := newAccountsScene(t)
	store := s.p.accounts()

	// A step notes the counter and reads; a Disconnect lands meanwhile.
	s.putLinks(t, steamProvider, &accountLinks{Users: map[string]accountLink{acDave: {ID: daveSteamID, Name: "Dave", At: 1}}})
	noted := store.linkWrites(steamProvider)
	stale, _, err := s.p.readAccountLinks(steamProvider)
	require.NoError(t, err)
	require.NoError(t, s.p.disconnectAccount(steamProvider, acDave, false, s.now().UnixMilli()))

	require.False(t, store.installRead(steamProvider, stale, noted), "the step's read is older than the write")
	require.NotContains(t, store.cachedLinks(steamProvider).Users, acDave)

	fresh, _, err := s.p.readAccountLinks(steamProvider)
	require.NoError(t, err)
	require.True(t, store.installRead(steamProvider, fresh, store.linkWrites(steamProvider)))
}
