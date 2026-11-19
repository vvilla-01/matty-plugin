package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Steam connections and Hytale connections in the System Console
// (design/3.4.0.md §3.8, §4.7, §8.1): what the cards are told, on the raw
// JSON, and every action, for a system admin who is locked or not, with the
// switches on or off.

// adminCard is a card as a system admin's console reads it, raw.
func (s *accountsScene) adminCard(t *testing.T, prov *accountProvider, userID string) []byte {
	t.Helper()

	response := s.serve(http.MethodGet, "/api/v1/admin/"+prov.id, userID, web(userID), "", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

	return response.Body.Bytes()
}

// adminDo is one of the card's buttons.
func (s *accountsScene) adminDo(t *testing.T, method, target, userID, body string) {
	t.Helper()

	response := s.serve(method, target, userID, web(userID), body, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"ok":true}`, response.Body.String())
}

func TestAdminAccountCardsWithNobodyListNobody(t *testing.T) {
	s := newAccountsScene(t)

	// Every field there, and the lists [] rather than null or absent.
	require.JSONEq(t, `{
		"enabled": true, "keySet": true, "keyProblem": "",
		"callbackUrl": "https://chat.example.org/plugins/ceo.villa.matty/api/v1/steam/callback",
		"realm": "https://chat.example.org", "callbackProblem": "",
		"lastTry": 0, "lastGood": 0, "problem": "", "callsToday": 0,
		"asked": 0, "answered": 0,
		"people": [], "blocked": [], "quarantined": false
	}`, string(s.adminCard(t, steamProvider, acBoss)))
	require.JSONEq(t, `{
		"enabled": true, "credentials": true,
		"callbackUrl": "https://chat.example.org/plugins/ceo.villa.matty/api/v1/hytale/callback",
		"callbackProblem": "", "problem": "",
		"people": [], "blocked": [], "quarantined": false
	}`, string(s.adminCard(t, hytaleProvider, acBoss)))

	// Not set up, and off: said so, still with the lists.
	s.setSite("http://chat.example.org")
	s.configure(func(cfg *configuration) {
		cfg.EnableSteam, cfg.EnableHytale = false, false
		cfg.SteamWebAPIKey, cfg.HytaleClientSecret = "", ""
	})
	var steam adminSteamStatus
	require.NoError(t, json.Unmarshal(s.adminCard(t, steamProvider, acChief), &steam))
	require.False(t, steam.Enabled)
	require.False(t, steam.KeySet)
	require.Empty(t, steam.CallbackURL)
	require.Empty(t, steam.Realm)
	require.Equal(t, "Steam's sign-in needs an https:// address to send people back to, and the Site URL is http://…", steam.CallbackProblem)
	require.NotNil(t, steam.People)

	var hytale adminHytaleStatus
	require.NoError(t, json.Unmarshal(s.adminCard(t, hytaleProvider, acChief), &hytale))
	require.False(t, hytale.Credentials)
	require.Equal(t, "Hytale needs an https:// Redirect URI, and the Site URL is http://…", hytale.CallbackProblem)
	require.Contains(t, string(s.adminCard(t, hytaleProvider, acChief)), `"blocked":[]`)
}

func TestAdminSteamCardShowsNobodysStatus(t *testing.T) {
	s := newAccountsScene(t)
	s.putLinks(t, steamProvider, &accountLinks{
		Users: map[string]accountLink{
			acDave: {ID: daveSteamID, Name: "Dave", At: 11},
			acErin: {ID: erinSteamID, Name: "Erin", At: 12},
			acBob:  {ID: bobSteamID, Name: "", At: 13},
		},
		Blocked: map[string]int64{acDan: 14},
	})
	s.player(steamPlayer{SteamID: daveSteamID, Name: "Dave", State: 1, Visibility: 3, GameName: "ELDEN RING"})
	s.player(steamPlayer{SteamID: erinSteamID, Name: "Erin", State: 2, Visibility: 1})
	s.deactivate(acBob)
	s.step(t)

	// Asked about the two active people; Steam answered for both.
	card := s.adminCard(t, steamProvider, acChief)
	var steam adminSteamStatus
	require.NoError(t, json.Unmarshal(card, &steam))
	require.Equal(t, s.now().UnixMilli(), steam.LastTry)
	require.Equal(t, s.now().UnixMilli(), steam.LastGood)
	require.Equal(t, 2, steam.Asked)
	require.Equal(t, 2, steam.Answered)
	require.Equal(t, 1, steam.CallsToday)
	require.Equal(t, []adminSteamPerson{
		{UserID: acBob, Name: "Bob", SteamName: "", URL: "https://steamcommunity.com/profiles/" + bobSteamID, Since: 13, Active: false},
		{UserID: acDave, Name: "Dave", SteamName: "Dave", URL: "https://steamcommunity.com/profiles/" + daveSteamID, Since: 11, Active: true},
		{UserID: acErin, Name: "Erin", SteamName: "Erin", URL: "https://steamcommunity.com/profiles/" + erinSteamID, Since: 12, Active: true},
	}, steam.People)
	require.Equal(t, []adminAccountBlocked{{UserID: acDan, Name: "Dan", Since: 14}}, steam.Blocked)

	// A locked admin can read it, so nothing in it says how anybody is.
	for _, word := range []string{"state", "online", "busy", "offline", "inGame", "ELDEN RING", "hidden", "private", "\"game\""} {
		require.NotContains(t, string(card), word)
	}
	require.Contains(t, string(card), `"steamName":""`, "a name not known yet is still a field")

	// Somebody Steam left out: answered for fewer than asked.
	s.steam.lock.Lock()
	delete(s.steam.players, erinSteamID)
	s.steam.lock.Unlock()
	s.clock.advance(time.Minute)
	s.step(t)
	require.NoError(t, json.Unmarshal(s.adminCard(t, steamProvider, acBoss), &steam))
	require.Equal(t, 2, steam.Asked)
	require.Equal(t, 1, steam.Answered)

	// A refused key, and a key that cannot be sent.
	s.steam.answerSummaries(http.StatusForbidden, "")
	s.clock.advance(time.Minute)
	s.step(t)
	require.NoError(t, json.Unmarshal(s.adminCard(t, steamProvider, acBoss), &steam))
	require.Equal(t, steamKeyRefused, steam.KeyProblem)
	require.Equal(t, steamKeyRefused, steam.Problem)
	require.Equal(t, s.now().UnixMilli(), steam.LastTry)
	require.Equal(t, s.now().Add(-time.Minute).UnixMilli(), steam.LastGood)

	s.configure(func(cfg *configuration) { cfg.SteamWebAPIKey = "two words" })
	s.p.accounts().liftSteamHold()
	require.NoError(t, json.Unmarshal(s.adminCard(t, steamProvider, acBoss), &steam))
	require.True(t, steam.KeySet)
	require.Equal(t, "The Steam Web API key has characters a key cannot have.", steam.KeyProblem)

	// A set-aside list: no people, the sentence, and the flag.
	s.setKV(kvSteamLinks, []byte("{damaged"))
	s.setKV(kvQuarantinePrefix+kvSteamLinks, []byte("{damaged"))
	require.NoError(t, json.Unmarshal(s.adminCard(t, steamProvider, acBoss), &steam))
	require.Empty(t, steam.People)
	require.NotNil(t, steam.People)
	require.True(t, steam.Quarantined)
	require.Equal(t, accountLinksProblem(steamProvider), steam.Problem)
}

func TestAdminHytaleCardSaysWhatHytaleSaid(t *testing.T) {
	s := newAccountsScene(t)
	s.putLinks(t, hytaleProvider, &accountLinks{Users: map[string]accountLink{
		acDave: {ID: daveHytaleUUID, Name: "Steve_Hytale", At: 21},
	}})
	s.forget(acDave)

	var hytale adminHytaleStatus
	require.NoError(t, json.Unmarshal(s.adminCard(t, hytaleProvider, acBoss), &hytale))
	require.Equal(t, []adminHytalePerson{{UserID: acDave, Name: "", HytaleName: "Steve_Hytale", Since: 21, Active: false, Deleted: true}}, hytale.People, "an account Mattermost no longer has")
	require.NotContains(t, string(s.adminCard(t, hytaleProvider, acBoss)), daveHytaleUUID, "never the profile id")

	store := s.p.accounts()
	store.noteSignInProblem(accountHytale, hytaleProblemCredentials)
	require.NoError(t, json.Unmarshal(s.adminCard(t, hytaleProvider, acBoss), &hytale))
	require.Equal(t, "Hytale refused the Client ID or Client Secret.", hytale.Problem)

	store.noteSignInProblem(accountHytale, hytaleProblemScope)
	require.NoError(t, json.Unmarshal(s.adminCard(t, hytaleProvider, acBoss), &hytale))
	require.Equal(t, "Hytale has not granted this application the scope hytale:profile yet.", hytale.Problem)

	s.configure(func(cfg *configuration) { cfg.HytaleClientSecret = "two words" })
	require.NoError(t, json.Unmarshal(s.adminCard(t, hytaleProvider, acBoss), &hytale))
	require.True(t, hytale.Credentials)
	require.Equal(t, "The Hytale Client ID or Client Secret has characters they cannot have.", hytale.Problem)

	// When Mattermost cannot say who people are, the card still lists them,
	// and says nobody's account is deleted: a missing name is no proof.
	s.failUsers(true)
	require.NoError(t, json.Unmarshal(s.adminCard(t, hytaleProvider, acBoss), &hytale))
	require.Len(t, hytale.People, 1)
	require.False(t, hytale.People[0].Deleted)
	require.Contains(t, string(s.adminCard(t, hytaleProvider, acBoss)), `"deleted":false`, "always in the JSON")
}

func TestAdminAccountRoutesAreForSystemAdminsOnly(t *testing.T) {
	s := newAccountsScene(t)
	s.putLinks(t, steamProvider, &accountLinks{Users: map[string]accountLink{acErin: {ID: erinSteamID, Name: "Erin", At: 1}}})

	for _, prov := range accountProviders {
		for _, request := range []struct{ method, target, body string }{
			{http.MethodGet, "/api/v1/admin/" + prov.id, ""},
			{http.MethodDelete, "/api/v1/admin/" + prov.id + "?user=" + acErin, ""},
			{http.MethodDelete, "/api/v1/admin/" + prov.id + "?all=1", ""},
			{http.MethodPost, "/api/v1/admin/" + prov.id + "/block", `{"userId":"` + acErin + `","blocked":true}`},
		} {
			for _, caller := range []string{acDave, acLetIn, acDan} {
				response := s.serve(request.method, request.target, caller, web(caller), request.body, nil)
				require.Equal(t, http.StatusForbidden, response.Code, "%s %s by %s", request.method, request.target, caller)
				require.Contains(t, response.Body.String(), "Only a system admin can manage "+prov.name+" connections.")
			}
		}
	}
	require.Contains(t, s.links(t, steamProvider).Users, acErin, "nothing was done")

	// Wrong methods and bad requests.
	require.Equal(t, http.StatusMethodNotAllowed, s.serve(http.MethodPut, "/api/v1/admin/steam", acBoss, web(acBoss), "", nil).Code)
	require.Equal(t, http.StatusMethodNotAllowed, s.serve(http.MethodGet, "/api/v1/admin/hytale/block", acBoss, web(acBoss), "", nil).Code)
	require.Equal(t, http.StatusBadRequest, s.serve(http.MethodDelete, "/api/v1/admin/steam?user=nobody", acBoss, web(acBoss), "", nil).Code)
	require.Equal(t, http.StatusBadRequest, s.serve(http.MethodPost, "/api/v1/admin/steam/block", acBoss, web(acBoss), "{", nil).Code)
	require.Equal(t, http.StatusBadRequest, s.serve(http.MethodPost, "/api/v1/admin/steam/block", acBoss, web(acBoss), `{"userId":"x","blocked":true}`, nil).Code)
	blockAll := s.serve(http.MethodPost, "/api/v1/admin/hytale/block", acBoss, web(acBoss), `{"all":true,"blocked":true}`, nil)
	require.Equal(t, http.StatusBadRequest, blockAll.Code)
	require.Contains(t, blockAll.Body.String(), "Block people one at a time.")
}

func TestAdminCanDisconnectBlockUnblockClearAndDeleteAll(t *testing.T) {
	for _, prov := range accountProviders {
		for _, on := range []bool{true, false} {
			for _, admin := range []string{acBoss, acChief} {
				name := prov.id + "/on"
				if !on {
					name = prov.id + "/off"
				}
				if admin == acChief {
					name += "/locked admin"
				}

				t.Run(name, func(t *testing.T) {
					s := newAccountsScene(t)
					ids := map[string]string{acDave: daveSteamID, acErin: erinSteamID, acBob: bobSteamID}
					if prov.id == accountHytale {
						ids = map[string]string{acDave: daveHytaleUUID, acErin: erinHytaleUUID, acBob: "123e4567-e89b-42d3-a456-426614174002"}
					}
					s.putLinks(t, prov, &accountLinks{Users: map[string]accountLink{
						acDave: {ID: ids[acDave], Name: "Dave", At: 1},
						acErin: {ID: ids[acErin], Name: "Erin", At: 1},
						acBob:  {ID: ids[acBob], Name: "Bob", At: 1},
					}})
					if !on {
						s.configure(func(cfg *configuration) { cfg.EnableSteam, cfg.EnableHytale = false, false })
					}
					base := "/api/v1/admin/" + prov.id
					told := len(s.accountEvents(t))

					// Disconnect.
					s.adminDo(t, http.MethodDelete, base+"?user="+acDave, admin, "")
					require.NotContains(t, s.links(t, prov).Users, acDave)
					require.NotContains(t, s.links(t, prov).Blocked, acDave)
					require.NotContains(t, string(s.steamViewBody(t)), acDave)

					// Twice is harmless.
					s.adminDo(t, http.MethodDelete, base+"?user="+acDave, admin, "")

					// Disconnect and block, and blocking somebody with nothing
					// connected.
					s.adminDo(t, http.MethodDelete, base+"?user="+acErin+"&block=1", admin, "")
					s.adminDo(t, http.MethodDelete, base+"?user="+acDan+"&block=1", admin, "")
					doc := s.links(t, prov)
					require.NotContains(t, doc.Users, acErin)
					require.Contains(t, doc.Blocked, acErin)
					require.Contains(t, doc.Blocked, acDan)

					// Block from the list, which disconnects.
					s.adminDo(t, http.MethodPost, base+"/block", admin, `{"userId":"`+acBob+`","blocked":true}`)
					doc = s.links(t, prov)
					require.Empty(t, doc.Users)
					require.Len(t, doc.Blocked, 3)

					// Unblock one, then clear the rest.
					s.adminDo(t, http.MethodPost, base+"/block", admin, `{"userId":"`+acErin+`","blocked":false}`)
					require.NotContains(t, s.links(t, prov).Blocked, acErin)
					s.adminDo(t, http.MethodPost, base+"/block", admin, `{"all":true,"blocked":false}`)
					require.Empty(t, s.links(t, prov).Blocked)

					// Each person was told to read /me again.
					events := s.accountEvents(t)[told:]
					for _, person := range []string{acDave, acErin, acDan, acBob} {
						require.Contains(t, events, person)
					}

					// Delete all, with a block to keep.
					s.adminDo(t, http.MethodPost, base+"/block", admin, `{"userId":"`+acDan+`","blocked":true}`)
					s.putLinks(t, prov, &accountLinks{
						Users:   map[string]accountLink{acDave: {ID: ids[acDave], Name: "Dave", At: 1}},
						Blocked: map[string]int64{acDan: 1},
					})
					s.adminDo(t, http.MethodDelete, base+"?all=1", admin, "")
					doc = s.links(t, prov)
					require.Empty(t, doc.Users)
					require.Equal(t, map[string]int64{acDan: 1}, doc.Blocked)
					events = s.accountEvents(t)
					require.Equal(t, "", events[len(events)-1], "everybody reads /me again")
				})
			}
		}
	}
}

func TestAnAdminDisconnectThatCannotBeSavedSaysSo(t *testing.T) {
	s := newAccountsScene(t)
	s.putLinks(t, steamProvider, &accountLinks{Users: map[string]accountLink{acDave: {ID: daveSteamID, Name: "Dave", At: 1}}})
	s.faults.failRead(kvSteamLinks)

	failed := s.serve(http.MethodDelete, "/api/v1/admin/steam?user="+acDave, acBoss, web(acBoss), "", nil)
	requireRefused(t, failed, http.StatusServiceUnavailable, accountCodeStore, "disconnect")
	require.Contains(t, failed.Body.String(), "Matty could not disconnect Steam just now. Try again in a moment.")

	failed = s.serve(http.MethodPost, "/api/v1/admin/steam/block", acBoss, web(acBoss), `{"userId":"`+acDave+`","blocked":false}`, nil)
	require.Equal(t, http.StatusServiceUnavailable, failed.Code)
	require.Contains(t, failed.Body.String(), "nothing has changed")

	failed = s.serve(http.MethodDelete, "/api/v1/admin/steam?all=1", acBoss, web(acBoss), "", nil)
	require.Equal(t, http.StatusServiceUnavailable, failed.Code)
	require.Contains(t, failed.Body.String(), "Press Delete all Steam data again.")

	s.faults.healRead(kvSteamLinks)
	require.Contains(t, s.links(t, steamProvider).Users, acDave, "nothing was half done")

	// A full block list is said so.
	blocked := map[string]int64{}
	for i := 0; i < maxAccountBlocked; i++ {
		blocked[strings.Repeat("b", 20)+jsonNumber(int64(100000+i))] = 1
	}
	s.putLinks(t, steamProvider, &accountLinks{Users: map[string]accountLink{}, Blocked: blocked})
	full := s.serve(http.MethodDelete, "/api/v1/admin/steam?user="+acErin+"&block=1", acBoss, web(acBoss), "", nil)
	require.Equal(t, http.StatusConflict, full.Code)
	require.Contains(t, full.Body.String(), "The block list is full")
}

// steamViewBody is GET /api/v1/accounts as a let-in reader sees it, raw.
func (s *accountsScene) steamViewBody(t *testing.T) []byte {
	t.Helper()

	code, body := s.accountsAnswer(t, acLetIn)
	require.Equal(t, http.StatusOK, code)

	return body
}
