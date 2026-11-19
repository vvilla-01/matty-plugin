package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Connect Twitch (design/twitch-live.md §4, §11.1): the ticket, the redirect,
// the callback, the pages, and what an admin can take back.

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

func requirePage(t *testing.T, response *httptest.ResponseRecorder, status int, heading string, about ...any) {
	t.Helper()

	require.Equal(t, status, response.Code, about...)
	require.Equal(t, "text/html; charset=utf-8", response.Header().Get("Content-Type"), about...)
	require.Contains(t, response.Body.String(), "<h1>"+heading+"</h1>", about...)
}

func (s *twitchScene) noStates(t *testing.T, about ...any) {
	t.Helper()

	require.Empty(t, s.keysWith(kvTwitchStatePrefix), about...)
}

func TestTheTicketIsIssuedOnlyByPOSTToAnInteractiveSession(t *testing.T) {
	s := newTwitchScene(t)

	ticket := s.ticket(t, twDave)
	require.Regexp(t, hex64, ticket)

	var stored twitchTicket
	require.NoError(t, json.Unmarshal(s.kv(kvTwitchTicketPrefix+twDave), &stored))
	sum := sha256.Sum256([]byte(ticket))
	require.Equal(t, hex.EncodeToString(sum[:]), stored.Hash, "the hash, never the ticket")
	require.Equal(t, web(twDave), stored.Session)
	require.Equal(t, s.now().UnixMilli(), stored.At)
	require.EqualValues(t, 600, s.expiryOf(kvTwitchTicketPrefix+twDave))
	require.NotContains(t, string(s.kv(kvTwitchTicketPrefix+twDave)), ticket)

	// Only a POST.
	require.Equal(t, http.StatusMethodNotAllowed, s.serve(http.MethodGet, twitchConnectTicketPath, twErin, web(twErin), "", nil).Code)

	// Not a personal access token, a bot or an OAuth app acting for somebody.
	for _, session := range []string{"pat-" + twErin, "bot-" + twErin, "app-" + twErin, ""} {
		response := s.serve(http.MethodPost, twitchConnectTicketPath, twErin, session, "", nil)
		require.Equal(t, http.StatusForbidden, response.Code, session)
		require.Contains(t, response.Body.String(), twitchInteractiveOnly(productName))
	}
	require.Nil(t, s.kv(kvTwitchTicketPrefix+twErin))

	// Not for somebody who is not linked: the gate says so before anything.
	require.Equal(t, http.StatusForbidden, s.serve(http.MethodPost, twitchConnectTicketPath, twDan, web(twDan), "", nil).Code)
	require.Equal(t, http.StatusForbidden, s.serve(http.MethodPost, twitchConnectTicketPath, twLetIn, web(twLetIn), "", nil).Code)

	// Off.
	cfg := s.p.getConfiguration().Clone()
	cfg.EnableTwitchLive = false
	s.p.setConfiguration(cfg)
	off := s.serve(http.MethodPost, twitchConnectTicketPath, twErin, web(twErin), "", nil)
	require.Equal(t, http.StatusNotFound, off.Code)
	require.Contains(t, off.Body.String(), twitchSwitchedOff)
	cfg = cfg.Clone()
	cfg.EnableTwitchLive = true
	s.p.setConfiguration(cfg)

	// No address for Twitch to send people back to: admins are told why.
	s.setSite("http://chat.example.com")
	unset := s.serve(http.MethodPost, twitchConnectTicketPath, twErin, web(twErin), "", nil)
	require.Equal(t, http.StatusServiceUnavailable, unset.Code)
	require.Contains(t, unset.Body.String(), twitchNeedsSetup)
	admin := s.serve(http.MethodPost, twitchConnectTicketPath, twBoss, web(twBoss), "", nil)
	require.Equal(t, http.StatusServiceUnavailable, admin.Code)
	require.Contains(t, admin.Body.String(), "https://")
	s.setSite("https://chat.example.com/mm")

	// Blocked.
	s.putLinks(t, &twitchLinks{Users: map[string]twitchLink{}, Blocked: map[string]int64{twErin: 1}})
	blocked := s.serve(http.MethodPost, twitchConnectTicketPath, twErin, web(twErin), "", nil)
	require.Equal(t, http.StatusForbidden, blocked.Code)
	require.Contains(t, blocked.Body.String(), twitchBlockedText)
	require.Nil(t, s.kv(kvTwitchTicketPrefix+twErin))
	require.Nil(t, s.kv(kvTwitchTicketPrefix+twBoss))

	// Five tries a quarter of an hour, and every refusal above counted none.
	for i := 0; i < 5; i++ {
		s.ticket(t, twBob)
	}
	tooMany := s.serve(http.MethodPost, twitchConnectTicketPath, twBob, web(twBob), "", nil)
	require.Equal(t, http.StatusTooManyRequests, tooMany.Code)
	require.Equal(t, "no-store", tooMany.Header().Get("Cache-Control"))
}

func TestConnectRedirectsToTwitchWithAStateBoundToTheUser(t *testing.T) {
	s := newTwitchScene(t)

	response := s.connectWith(twDave, web(twDave), s.ticket(t, twDave), nil)
	require.Equal(t, http.StatusFound, response.Code, response.Body.String())

	location := response.Header().Get("Location")
	parsed, err := url.Parse(location)
	require.NoError(t, err)
	state, nonce := parsed.Query().Get("state"), parsed.Query().Get("nonce")
	require.Regexp(t, hex64, state)
	require.Regexp(t, hex64, nonce)
	require.NotEqual(t, state, nonce)

	callback := "https://chat.example.com/mm/plugins/ceo.villa.matty/api/v1/twitch/callback"
	require.Equal(t, s.fake.server.URL+"/oauth2/authorize?response_type=code&client_id="+testTwitchClientID+
		"&redirect_uri="+url.QueryEscape(callback)+"&scope=openid&state="+state+"&nonce="+nonce+"&force_verify=true", location)

	var record twitchState
	require.NoError(t, json.Unmarshal(s.kv(kvTwitchStatePrefix+state), &record))
	require.Equal(t, twitchState{User: twDave, At: s.now().UnixMilli(), Redirect: callback, Nonce: nonce}, record)
	require.EqualValues(t, 600, s.expiryOf(kvTwitchStatePrefix+state))

	require.Nil(t, s.kv(kvTwitchTicketPrefix+twDave), "the ticket is spent")
	require.Empty(t, s.fake.requests(), "the server asks Twitch nothing; the browser goes there")
}

func TestConnectWithoutAValidTicketDoesNothing(t *testing.T) {
	type attempt struct {
		name    string
		ticket  func(s *twitchScene) string
		session string
		header  map[string]string
		kept    bool // the real ticket is still there afterwards
	}

	attempts := []attempt{
		{name: "no ticket", ticket: func(*twitchScene) string { return "" }, kept: true},
		{name: "a malformed ticket", ticket: func(*twitchScene) string { return "XYZ" }, kept: true},
		{name: "an unknown ticket", ticket: func(*twitchScene) string { return strings.Repeat("ab", 32) }, kept: true},
		{name: "a ticket of another session", session: "tab-" + twDave, kept: true},
		{name: "an image fetching it", header: map[string]string{"Sec-Fetch-Dest": "image"}, kept: true},
		{name: "another site navigating to it", header: map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Dest": "document"}, kept: true},
	}

	for _, tried := range attempts {
		t.Run(tried.name, func(t *testing.T) {
			s := newTwitchScene(t)
			ticket := s.ticket(t, twDave)
			if tried.ticket != nil {
				ticket = tried.ticket(s)
			}
			session := tried.session
			if session == "" {
				session = web(twDave)
			}

			limiter := s.p.twitchLive().connectLimiter
			limiter.lock.Lock()
			tokens := limiter.buckets[twDave].tokens
			limiter.lock.Unlock()

			response := s.connectWith(twDave, session, ticket, tried.header)
			requirePage(t, response, http.StatusBadRequest, "Start from Matty", tried.name)
			require.Empty(t, response.Header().Get("Location"))
			s.noStates(t)
			require.Empty(t, s.fake.requests())
			require.Equal(t, tried.kept, s.kv(kvTwitchTicketPrefix+twDave) != nil)

			limiter.lock.Lock()
			require.InDelta(t, tokens, limiter.buckets[twDave].tokens, 0.01, "nothing counted")
			limiter.lock.Unlock()
		})
	}

	t.Run("a spent ticket", func(t *testing.T) {
		s := newTwitchScene(t)
		ticket := s.ticket(t, twDave)
		require.Equal(t, http.StatusFound, s.connectWith(twDave, web(twDave), ticket, nil).Code)
		states := s.keysWith(kvTwitchStatePrefix)

		again := s.connectWith(twDave, web(twDave), ticket, nil)
		requirePage(t, again, http.StatusBadRequest, "Start from Matty")
		require.Empty(t, again.Header().Get("Location"))
		require.Equal(t, states, s.keysWith(kvTwitchStatePrefix), "no second state")
	})

	t.Run("the desktop app's own window", func(t *testing.T) {
		s := newTwitchScene(t)
		response := s.connectWith(twDave, web(twDave), s.ticket(t, twDave), map[string]string{"Sec-Fetch-Site": "none", "Sec-Fetch-Dest": "document"})
		require.Equal(t, http.StatusFound, response.Code)
	})
}

func TestConnectPages(t *testing.T) {
	type page struct {
		name    string
		user    string
		session string
		prepare func(t *testing.T, s *twitchScene)
		status  int
		heading string
		says    string
	}

	switchOff := func(change func(cfg *configuration)) func(t *testing.T, s *twitchScene) {
		return func(_ *testing.T, s *twitchScene) {
			cfg := s.p.getConfiguration().Clone()
			change(cfg)
			s.p.setConfiguration(cfg)
		}
	}

	pages := []page{
		{name: "off", prepare: switchOff(func(cfg *configuration) { cfg.EnableTwitchLive = false }), status: http.StatusNotFound, heading: "Twitch is switched off"},
		{name: "no credentials", prepare: switchOff(func(cfg *configuration) { cfg.TwitchClientSecret = "" }), status: http.StatusNotFound, heading: "Twitch is switched off"},
		{name: "no Site URL", prepare: func(_ *testing.T, s *twitchScene) { s.setSite("") }, status: http.StatusServiceUnavailable, heading: "Not set up yet", says: twitchNeedsSetup},
		{name: "an http Site URL", prepare: func(_ *testing.T, s *twitchScene) { s.setSite("http://chat.example.com") }, status: http.StatusServiceUnavailable, heading: "Not set up yet"},
		{name: "an http Site URL, to an admin", user: twBoss, prepare: func(_ *testing.T, s *twitchScene) { s.setSite("http://chat.example.com") }, status: http.StatusServiceUnavailable, heading: "Not set up yet", says: "Twitch only accepts an https:// callback address"},
		{name: "http://localhost", prepare: func(_ *testing.T, s *twitchScene) { s.setSite("http://localhost:8065") }, status: http.StatusFound},
		{name: "signed out", session: "-", status: http.StatusUnauthorized, heading: "Sign in first"},
		{name: "an integration session", session: "pat-" + twDave, status: http.StatusUnauthorized, heading: "Sign in first"},
		{name: "blocked", prepare: func(t *testing.T, s *twitchScene) {
			s.putLinks(t, &twitchLinks{Users: map[string]twitchLink{}, Blocked: map[string]int64{twDave: 1}})
		}, status: http.StatusForbidden, heading: "Not allowed", says: twitchBlockedText},
		{name: "full", prepare: func(t *testing.T, s *twitchScene) {
			doc := &twitchLinks{Users: map[string]twitchLink{}}
			for i := 0; i < maxTwitchLinks; i++ {
				doc.Users[fmt.Sprintf("someone%021d", i)] = twitchLink{ID: fmt.Sprint(5000 + i), Login: fmt.Sprintf("someone%d", i)}
			}
			s.putLinks(t, doc)
		}, status: http.StatusConflict, heading: "No room"},
		{name: "a store failure", prepare: func(_ *testing.T, s *twitchScene) { s.faults.failRead(kvTwitchTicketPrefix + twDave) }, status: http.StatusServiceUnavailable, heading: "Could not save"},
		{name: "unlinked since the ticket", prepare: func(_ *testing.T, s *twitchScene) {
			s.setKV(kvLinkByUserPrefix+twDave, nil)
		}, status: http.StatusForbidden, heading: "Not allowed", says: "unlocked Matty with their bearer token"},
	}

	for _, tried := range pages {
		t.Run(tried.name, func(t *testing.T) {
			s := newTwitchScene(t)
			user := tried.user
			if user == "" {
				user = twDave
			}
			ticket := s.ticket(t, user)
			if tried.prepare != nil {
				tried.prepare(t, s)
			}

			session := tried.session
			header := user
			switch session {
			case "":
				session = web(user)
			case "-":
				session, header = "", ""
			}

			response := s.serve(http.MethodGet, twitchConnectPath+"?t="+ticket, header, session, "", nil)
			if tried.status == http.StatusFound {
				require.Equal(t, http.StatusFound, response.Code, response.Body.String())
				return
			}

			requirePage(t, response, tried.status, tried.heading)
			if tried.says != "" {
				require.Contains(t, response.Body.String(), tried.says)
			}
			s.noStates(t)
			require.Empty(t, response.Header().Get("Location"))
			require.Empty(t, s.fake.requests(), "no outside request")
		})
	}
}

// The let-in and the locked are never offered Connect, and the connect
// window, above the gate, refuses them itself.
func TestOnlyLinkedPeopleCanConnectTwitch(t *testing.T) {
	s := newTwitchScene(t)

	for _, userID := range []string{twDan, twLetIn} {
		response := s.connectWith(userID, web(userID), strings.Repeat("ab", 32), nil)
		requirePage(t, response, http.StatusForbidden, "Not allowed", userID)
	}

	// A state started while linked, finished after an unlink.
	state, nonce := s.startConnect(t, twErin)
	s.fake.grant("late", fakeGrant{sub: erinTwitch.ID, nonce: nonce, who: erinTwitch})
	s.setKV(kvLinkByUserPrefix+twErin, nil)

	requirePage(t, s.callback(twErin, web(twErin), "code=late&state="+state), http.StatusForbidden, "Not allowed")
	require.Nil(t, s.kv(kvTwitchStatePrefix+state), "the state is thrown away")
	require.Zero(t, s.requestsTo("/oauth2/token"), "nothing was asked of Twitch")
	require.Empty(t, s.links(t).Users)
}

func TestCallbackMakesNoRequestWithoutALiveState(t *testing.T) {
	s := newTwitchScene(t)

	used, nonce := s.startConnect(t, twDave)
	s.fake.grant("used-code", fakeGrant{sub: daveTwitch.ID, nonce: nonce, who: daveTwitch})
	require.Equal(t, http.StatusOK, s.callback(twDave, web(twDave), "code=used-code&state="+used).Code)

	expired := strings.Repeat("cd", 32)
	record, err := json.Marshal(twitchState{User: twErin, At: s.now().Add(-11 * time.Minute).UnixMilli(), Redirect: "x", Nonce: "n"})
	require.NoError(t, err)
	s.setKV(kvTwitchStatePrefix+expired, record)

	before := len(s.fake.requests())
	for _, tried := range []struct{ name, user, state string }{
		{"a bad format", twErin, "not-a-state"},
		{"an unknown state", twErin, strings.Repeat("ef", 32)},
		{"an expired state", twErin, expired},
		{"a state already used", twDave, used},
	} {
		response := s.callback(tried.user, web(tried.user), "code=whatever&state="+tried.state)
		requirePage(t, response, http.StatusBadRequest, "This link has run out", tried.name)
		require.Contains(t, response.Body.String(), "Do not share this page")
	}

	require.Len(t, s.fake.requests(), before, "no request to Twitch")
	require.Nil(t, s.kv(kvTwitchStatePrefix+expired), "an expired state is deleted")
}

func TestTheStateIsSingleUse(t *testing.T) {
	s := newTwitchScene(t)

	state, nonce := s.startConnect(t, twDave)
	s.fake.grant("the-code", fakeGrant{sub: daveTwitch.ID, nonce: nonce, who: daveTwitch})

	require.Equal(t, http.StatusOK, s.callback(twDave, web(twDave), "code=the-code&state="+state).Code)
	requirePage(t, s.callback(twDave, web(twDave), "code=the-code&state="+state), http.StatusBadRequest, "This link has run out")

	exchanges := 0
	for _, seen := range s.fake.requests() {
		if seen.path == "/oauth2/token" && strings.Contains(seen.body, "authorization_code") {
			exchanges++
		}
	}
	require.Equal(t, 1, exchanges)
}

func TestCallbackFromAnotherAccountIsRefusedAndUsesTheState(t *testing.T) {
	s := newTwitchScene(t)

	state, nonce := s.startConnect(t, twDave)
	s.fake.grant("the-code", fakeGrant{sub: daveTwitch.ID, nonce: nonce, who: daveTwitch})

	response := s.callback(twErin, web(twErin), "code=the-code&state="+state)
	requirePage(t, response, http.StatusForbidden, "Started by another account")
	require.Contains(t, response.Body.String(), "Do not share this page")
	require.Zero(t, s.requestsTo("/oauth2/token"))
	require.Nil(t, s.kv(kvTwitchStatePrefix+state), "the state is used up")
	require.Empty(t, s.links(t).Users)
}

func TestCallbackWithoutASessionLinksNothing(t *testing.T) {
	s := newTwitchScene(t)

	state, nonce := s.startConnect(t, twDave)
	s.fake.grant("the-code", fakeGrant{sub: daveTwitch.ID, nonce: nonce, who: daveTwitch})
	requirePage(t, s.callback("", "", "code=the-code&state="+state), http.StatusUnauthorized, "Sign in first")

	// A personal access token of the very person who started it, which an
	// address with access_token= appended would carry, is not them.
	state, nonce = s.startConnect(t, twDave)
	s.fake.grant("the-code", fakeGrant{sub: daveTwitch.ID, nonce: nonce, who: daveTwitch})
	requirePage(t, s.callback(twDave, "pat-"+twDave, "code=the-code&state="+state), http.StatusUnauthorized, "Sign in first")

	require.Zero(t, s.requestsTo("/oauth2/token"))
	require.Empty(t, s.links(t).Users)
}

// A code that leaked unspent from somebody else's connection is redeemed by
// an attacker with a state of their own: the ID token carries the victim's
// nonce, so it fails, and the token it bought is revoked.
func TestACodeFromAnotherStateIsRefused(t *testing.T) {
	s := newTwitchScene(t)

	_, victimNonce := s.startConnect(t, twErin)
	s.fake.grant("victims-code", fakeGrant{sub: erinTwitch.ID, nonce: victimNonce, who: erinTwitch})

	attackerState, _ := s.startConnect(t, twDave)
	response := s.callback(twDave, web(twDave), "code=victims-code&state="+attackerState)
	requirePage(t, response, http.StatusBadGateway, "Twitch did not confirm it")

	require.Equal(t, 1, s.requestsTo("/oauth2/token"), "one exchange")
	require.Equal(t, 1, s.requestsTo("/oauth2/revoke"), "and its token revoked")
	require.Zero(t, s.requestsTo("/helix/users"))
	require.Empty(t, s.links(t).Users, "no link written")
	require.Empty(t, s.links(t).Lost, "no takeover")
}

func TestTheIDTokenMustAgreeWithGetUsers(t *testing.T) {
	s := newTwitchScene(t)

	state, nonce := s.startConnect(t, twDave)
	s.fake.grant("the-code", fakeGrant{sub: daveTwitch.ID, nonce: nonce, who: otherTwitch})

	requirePage(t, s.callback(twDave, web(twDave), "code=the-code&state="+state), http.StatusBadGateway, "Twitch did not confirm it")
	require.Equal(t, 1, s.requestsTo("/oauth2/revoke"))
	require.Empty(t, s.links(t).Users)
}

func TestCancelledOnTwitch(t *testing.T) {
	s := newTwitchScene(t)

	state, _ := s.startConnect(t, twDave)
	response := s.callback(twDave, web(twDave), "error=access_denied&error_description=The+user+denied&state="+state)
	requirePage(t, response, http.StatusOK, "Not connected")
	require.Contains(t, response.Body.String(), twitchPageScript, "the window closes itself where it may")
	require.Zero(t, s.requestsTo("/oauth2/token"))
}

func TestCallbackLinksAndRevokes(t *testing.T) {
	s := newTwitchScene(t)

	state, nonce := s.startConnect(t, twDave)
	s.fake.grant("the-code", fakeGrant{sub: daveTwitch.ID, nonce: nonce, who: daveTwitch})
	response := s.callback(twDave, web(twDave), "code=the-code&scope=openid&state="+state)
	requirePage(t, response, http.StatusOK, "Twitch connected")
	require.Contains(t, response.Body.String(), "Connected as <strong>Dave_TV</strong> (twitch.tv/dave_tv)")

	var paths []string
	for _, seen := range s.fake.requests() {
		paths = append(paths, seen.path)
	}
	require.Equal(t, []string{"/oauth2/token", "/helix/users", "/oauth2/revoke"}, paths, "exchange, whose, revoke, in that order")

	revoke := s.fake.requests()[2]
	form, err := url.ParseQuery(revoke.body)
	require.NoError(t, err)
	require.Equal(t, url.Values{"client_id": {testTwitchClientID}, "token": {"utok-the-code"}}, form)

	link := s.links(t).Users[twDave]
	require.Equal(t, twitchLink{ID: "1001", Login: "dave_tv", Name: "Dave_TV", At: s.now().UnixMilli(), Checked: s.now().UnixMilli()}, link)
	require.Equal(t, []string{twDave}, s.twitchEvents(t))

	s.faults.lock.Lock()
	for key, value := range s.store {
		for _, secret := range []string{"the-code", "utok-the-code", "rtok-the-code", nonce, state} {
			require.NotContains(t, string(value), secret, key)
			require.NotContains(t, key, secret)
		}
	}
	s.faults.lock.Unlock()

	// And /me says so, at once.
	me := s.me(t, twDave)
	require.NotNil(t, me.Twitch)
	require.True(t, me.Twitch.Available)
	require.Equal(t, "dave_tv", me.Twitch.Login)
	require.Equal(t, "Dave_TV", me.Twitch.DisplayName)
	require.Equal(t, "linked", me.Access, "and it changes nothing about the lock")
}

func TestATokenForAnotherClientLinksNothing(t *testing.T) {
	s := newTwitchScene(t)

	state, nonce := s.startConnect(t, twDave)
	s.fake.grant("the-code", fakeGrant{sub: daveTwitch.ID, nonce: nonce, who: daveTwitch, usersStatus: http.StatusUnauthorized})

	requirePage(t, s.callback(twDave, web(twDave), "code=the-code&state="+state), http.StatusBadGateway, "Twitch did not confirm it")
	require.Equal(t, 1, s.requestsTo("/oauth2/revoke"), "revoked all the same")
	require.Empty(t, s.links(t).Users)
}

func TestCallbackPagesAreSafe(t *testing.T) {
	s := newTwitchScene(t)

	state, nonce := s.startConnect(t, twDave)
	sneaky := twitchUser{ID: "1001", Login: "dave_tv", DisplayName: `<img src=x onerror=alert(1)>`}
	s.fake.grant("the-code", fakeGrant{sub: sneaky.ID, nonce: nonce, who: sneaky})

	response := s.callback(twDave, web(twDave), "code=the-code&state="+state)
	require.Equal(t, http.StatusOK, response.Code)

	sum := sha256.Sum256([]byte(twitchPageScript))
	header := response.Header()
	require.Equal(t, "default-src 'none'; style-src 'unsafe-inline'; script-src 'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+
		"'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'", header.Get("Content-Security-Policy"))
	require.Equal(t, "no-store", header.Get("Cache-Control"))
	require.Equal(t, "nosniff", header.Get("X-Content-Type-Options"))
	require.Equal(t, "no-referrer", header.Get("Referrer-Policy"))

	body := response.Body.String()
	require.NotContains(t, body, "<img")
	require.Contains(t, body, "&lt;img src=x onerror=alert(1)&gt;")
	require.Contains(t, body, "<script>"+twitchPageScript+"</script>", "the script is exactly the text the hash is of")
	require.Equal(t, 1, strings.Count(body, "<script"))

	// A page that does not close itself carries no script.
	requirePage(t, s.callback(twDave, web(twDave), "code=x&state="+strings.Repeat("0", 64)), http.StatusBadRequest, "This link has run out")
	require.NotContains(t, s.callback(twDave, web(twDave), "state=x").Body.String(), "<script")
}

func TestAVerifiedProofTakesTheAccountOver(t *testing.T) {
	s := newTwitchScene(t)
	s.seedTable(t, kvTwitchAirTotals, statTable{"1001": {Seconds: 3600, Title: "Dave"}})

	s.connectAs(t, twDave, daveTwitch)
	response := s.connectAs(t, twErin, daveTwitch)
	require.Contains(t, response.Body.String(), "It was connected to another Mattermost account before.")

	doc := s.links(t)
	require.NotContains(t, doc.Users, twDave)
	require.Equal(t, "1001", doc.Users[twErin].ID)
	require.Equal(t, twitchLost{Login: "dave_tv", At: s.now().UnixMilli()}, doc.Lost[twDave])
	require.Empty(t, doc.Purge, "the channel moved, so nothing of it is purged")
	require.EqualValues(t, 3600, s.table(t, kvTwitchAirTotals)["1001"].Seconds, "its hours stay with the channel")

	events := s.twitchEvents(t)
	require.Equal(t, []string{twDave, twErin, twDave}, events, "connected, then the new owner and the old one told")

	me := s.me(t, twDave)
	require.NotNil(t, me.Twitch)
	require.Empty(t, me.Twitch.Login)
	require.Equal(t, &twitchLost{Login: "dave_tv", At: s.now().UnixMilli()}, me.Twitch.Lost)

	// Read, and dismissed.
	ok := s.serve(http.MethodPost, twitchNoticePath, twDave, web(twDave), "", nil)
	require.Equal(t, http.StatusOK, ok.Code)
	require.NotContains(t, s.links(t).Lost, twDave)

	// A notice nobody read goes after thirty days, on the next write.
	doc = s.links(t)
	doc.Lost[twBob] = twitchLost{Login: "old_one", At: s.now().Add(-31 * 24 * time.Hour).UnixMilli()}
	s.putLinks(t, doc)
	require.NoError(t, s.p.setTwitchAllDay(twErin, true, s.now().UnixMilli()))
	require.NotContains(t, s.links(t).Lost, twBob)
}

func TestABlockedPersonCannotConnect(t *testing.T) {
	s := newTwitchScene(t)
	s.connectAs(t, twDave, daveTwitch)

	s.p.twitchLive().purgeOff = true
	disconnect := s.serve(http.MethodDelete, adminTwitchPath+"?user="+twDave+"&block=1", twBoss, web(twBoss), "", nil)
	require.Equal(t, http.StatusOK, disconnect.Code, disconnect.Body.String())

	doc := s.links(t)
	require.NotContains(t, doc.Users, twDave)
	require.Contains(t, doc.Blocked, twDave)
	require.Contains(t, doc.Purge, "1001", "the purge is queued")

	s.p.startTwitchPurges()
	s.waitForPurges()
	require.Empty(t, s.links(t).Purge)

	refused := s.serve(http.MethodPost, twitchConnectTicketPath, twDave, web(twDave), "", nil)
	require.Equal(t, http.StatusForbidden, refused.Code)
	require.True(t, s.me(t, twDave).Twitch.Blocked)

	// A connection already in flight when the block lands.
	state, nonce := s.startConnect(t, twErin)
	s.fake.grant("in-flight", fakeGrant{sub: erinTwitch.ID, nonce: nonce, who: erinTwitch})
	block := s.serve(http.MethodPost, adminTwitchBlockPath, twBoss, web(twBoss), `{"userId":"`+twErin+`","blocked":true}`, nil)
	require.Equal(t, http.StatusOK, block.Code)
	exchanges := s.requestsTo("/oauth2/token")
	requirePage(t, s.callback(twErin, web(twErin), "code=in-flight&state="+state), http.StatusForbidden, "Not allowed")
	require.Equal(t, exchanges, s.requestsTo("/oauth2/token"), "a blocked person makes the server ask Twitch nothing")

	// Unblocked, they can connect again.
	unblock := s.serve(http.MethodPost, adminTwitchBlockPath, twBoss, web(twBoss), `{"userId":"`+twErin+`","blocked":false}`, nil)
	require.Equal(t, http.StatusOK, unblock.Code)
	s.connectAs(t, twErin, erinTwitch)

	// Delete all keeps the block list; clearing it empties it.
	require.Equal(t, http.StatusOK, s.serve(http.MethodDelete, adminTwitchPath+"?all=1", twBoss, web(twBoss), "", nil).Code)
	require.Contains(t, s.links(t).Blocked, twDave)
	require.Empty(t, s.links(t).Users)

	clear := s.serve(http.MethodPost, adminTwitchBlockPath, twBoss, web(twBoss), `{"all":true,"blocked":false}`, nil)
	require.Equal(t, http.StatusOK, clear.Code)
	require.Empty(t, s.links(t).Blocked)
}

func TestA247ChannelGetsNoBadgeAndNoAirtime(t *testing.T) {
	s := newTwitchScene(t)
	start := s.now().Add(-30 * time.Minute)
	s.connected(t, map[string]twitchUser{twDave: daveTwitch, twErin: erinTwitch})
	s.fake.goLive(daveTwitch, "Always on", "Music", 3, start)
	s.fake.goLive(erinTwitch, "Erin live", "Art", 5, start)
	s.seedTable(t, kvTwitchAirTotals, statTable{"1001": {Seconds: 600, Title: "Dave"}, "1002": {Seconds: 100, Title: "Erin"}})

	allDay := s.serve(http.MethodPost, adminTwitchAllDayPath, twBoss, web(twBoss), `{"userId":"`+twDave+`","allDay":true}`, nil)
	require.Equal(t, http.StatusOK, allDay.Code, allDay.Body.String())

	// Dave's Broadcast Box card is on the air too: a 24/7 channel is never
	// covered by it.
	dave := apiStream{StreamKey: "videokey-dave", Handle: s.p.streamHandle("videokey-dave"), Title: "Dave", UserID: twDave, LinkSource: linkSourceManual}

	s.step(t)
	streams := s.p.mergeStreams([]apiStream{dave}, s.p.twitchStreams(s.p.getConfiguration(), s.now()), true)
	twitchDave := cardOf(t, streams, s.p.twitchHandle("1001"))
	require.True(t, twitchDave.AlwaysOn)
	require.Empty(t, twitchDave.CoveredBy)
	require.Equal(t, s.p.twitchHandle("1001"), streams[len(streams)-1].Handle, "sorted last")

	current := &snapshot{Streams: streams}
	s.p.addBoards(current, s.p.getConfiguration(), s.now())
	twitchDave = cardOf(t, current.Streams, s.p.twitchHandle("1001"))
	require.Zero(t, twitchDave.AllTimeRank)
	require.Zero(t, twitchDave.MonthRank)

	// No badge from the Twitch entry: only Erin's.
	s.p.reconcileBadges([]apiStream{twitchDave, cardOf(t, current.Streams, s.p.twitchHandle("1002"))}, s.p.getConfiguration(), s.now(), true)
	for _, call := range s.api.Calls {
		if call.Method == "UpdateUserCustomStatus" {
			require.Equal(t, twErin, call.Arguments[0])
		}
	}

	// No credit while 24/7, and nothing marked paid; Erin's time counts.
	s.clock.advance(10 * time.Minute)
	s.step(t)
	require.EqualValues(t, 600, s.table(t, kvTwitchAirTotals)["1001"].Seconds)
	paid, err := s.p.loadTwitchPaid()
	require.NoError(t, err)
	require.Empty(t, paid[twDave])
	require.Greater(t, s.table(t, kvTwitchAirTotals)["1002"].Seconds, int64(100))

	// Clearing the flag does not pay back the stretch it was 24/7.
	cleared := s.serve(http.MethodPost, adminTwitchAllDayPath, twBoss, web(twBoss), `{"userId":"`+twDave+`","allDay":false}`, nil)
	require.Equal(t, http.StatusOK, cleared.Code)
	s.clock.advance(time.Minute)
	s.step(t)
	require.EqualValues(t, 660, s.table(t, kvTwitchAirTotals)["1001"].Seconds, "only the minute since it was cleared")
}

func TestReplacingYourTwitchAccountPurgesTheOldOne(t *testing.T) {
	s := newTwitchScene(t)
	s.p.twitchLive().purgeOff = true // the purge is driven by hand here

	s.connectAs(t, twDave, daveTwitch)
	s.seedTable(t, kvTwitchAirTotals, statTable{"1001": {Seconds: 3600, Title: "Dave"}})
	s.seedTable(t, twitchMonthBucket(s.now()), statTable{"1001": {Seconds: 1800, Title: "Dave"}})

	s.connectAs(t, twDave, otherTwitch)
	doc := s.links(t)
	require.Equal(t, "1009", doc.Users[twDave].ID)
	require.Equal(t, twitchPurgeEntry{User: twDave, At: s.now().UnixMilli(), Login: "dave_tv"}, doc.Purge["1001"])

	more, err := s.p.purgeStep(t.Context(), s.now())
	require.NoError(t, err)
	require.True(t, more)
	require.NotContains(t, s.table(t, kvTwitchAirTotals), "1001")
	require.NotContains(t, s.table(t, twitchMonthBucket(s.now())), "1001")
	require.Empty(t, s.links(t).Purge)
}

func TestAChannelBeingPurgedCannotBeConnected(t *testing.T) {
	s := newTwitchScene(t)
	s.putLinks(t, &twitchLinks{Users: map[string]twitchLink{}, Purge: map[string]twitchPurgeEntry{"1002": {User: twBob, At: 1}}})
	s.p.twitchLive().purgeOff = true

	state, nonce := s.startConnect(t, twErin)
	s.fake.grant("the-code", fakeGrant{sub: erinTwitch.ID, nonce: nonce, who: erinTwitch})
	requirePage(t, s.callback(twErin, web(twErin), "code=the-code&state="+state), http.StatusConflict, "Not just yet")
	require.Empty(t, s.links(t).Users)
}

func TestAnUnreadableLinkDocumentIsNeverOverwritten(t *testing.T) {
	s := newTwitchScene(t)
	state, nonce := s.startConnect(t, twDave)
	s.fake.grant("the-code", fakeGrant{sub: daveTwitch.ID, nonce: nonce, who: daveTwitch})

	garbage := []byte(`{"v":1,"users":{"` + twBob + `":`)
	s.setKV(kvTwitchLinks, garbage)
	s.p.twitchLive().noteLinks(nil)

	requirePage(t, s.callback(twDave, web(twDave), "code=the-code&state="+state), http.StatusServiceUnavailable, "Could not save")
	require.Equal(t, garbage, s.kv(kvTwitchLinks), "never overwritten")
	require.Equal(t, garbage, s.kv(kvQuarantinePrefix+kvTwitchLinks), "copied aside")
	require.Zero(t, s.requestsTo("/oauth2/token"))

	// The writers refuse it too, whatever they were doing.
	err := s.p.disconnectTwitch(twBob, false, s.now().UnixMilli())
	require.ErrorIs(t, err, errStore)
	require.Equal(t, garbage, s.kv(kvTwitchLinks))

	// And the admin is told what to do.
	me := s.me(t, twBoss)
	require.Equal(t, twitchLinksProblem, me.Twitch.Problem)
}
