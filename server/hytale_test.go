package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Connect Hytale (design/3.4.0.md §4, §8.1): the authorize address, PKCE, the
// application's Basic sign-in, the ID token, userinfo, the revoke, and the
// pages Hytale's own errors come back as.

func TestHytaleStartIsExact(t *testing.T) {
	s := newAccountsScene(t)

	state, location := s.startConnect(t, hytaleProvider, acDave)

	var record accountState
	require.NoError(t, json.Unmarshal(s.kv("hytale_state_"+state), &record))
	redirect := "https://chat.example.org/plugins/ceo.villa.matty/api/v1/hytale/callback"
	require.Equal(t, redirect, record.Return, "the Redirect URI, byte for byte")
	require.Regexp(t, hex64, record.Nonce)
	require.Len(t, record.Verifier, 43)
	require.Regexp(t, `^[A-Za-z0-9_-]{43}$`, record.Verifier)
	require.Equal(t, accountModeWeb, record.Mode)

	sum := sha256.Sum256([]byte(record.Verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	require.Equal(t, s.fake.server.URL+"/oauth2/auth?response_type=code"+
		"&client_id="+testHytaleClientID+
		"&redirect_uri=https%3A%2F%2Fchat.example.org%2Fplugins%2Fceo.villa.matty%2Fapi%2Fv1%2Fhytale%2Fcallback"+
		"&scope=openid%20hytale%3Aprofile"+
		"&state="+state+
		"&nonce="+record.Nonce+
		"&code_challenge="+challenge+
		"&code_challenge_method=S256", location.String())
	require.NotContains(t, location.String(), "prompt=")
	require.NotContains(t, location.String(), "response_mode")
	require.NotContains(t, location.String(), "offline")
	require.NotContains(t, location.String(), record.Verifier, "the verifier never leaves the server")
	require.Empty(t, s.fake.requests())

	// Two connects, two verifiers.
	second, _ := s.startConnect(t, hytaleProvider, acErin)
	var other accountState
	require.NoError(t, json.Unmarshal(s.kv("hytale_state_"+second), &other))
	require.NotEqual(t, record.Verifier, other.Verifier)

	// The Redirect URI with a Site URL that has a path, and the http rule.
	s.setSite("https://chat.example.org/mm/")
	_, location = s.startConnect(t, hytaleProvider, acBob)
	require.Equal(t, "https://chat.example.org/mm/plugins/ceo.villa.matty/api/v1/hytale/callback", location.Query().Get("redirect_uri"))
	s.setSite("http://chat.example.org")
	require.Equal(t, "Hytale needs an https:// Redirect URI, and the Site URL is http://…", hytaleSetup(s.p, s.p.getConfiguration()))
	s.setSite("http://[::1]:8065")
	require.Empty(t, hytaleSetup(s.p, s.p.getConfiguration()))

	// The application's two halves.
	require.Equal(t, "Fill in the Client ID and Client Secret above first.", hytaleSetup(s.p, &configuration{EnableHytale: true, HytaleClientID: "x"}))
	require.False(t, (&configuration{EnableHytale: true, HytaleClientID: "x"}).hytaleOn())
	require.Contains(t, hytaleSetup(s.p, &configuration{EnableHytale: true, HytaleClientID: "x", HytaleClientSecret: "a\x01b"}), "characters")
}

func TestHytaleExchangeUsesEscapedBasicAndNothingInTheBody(t *testing.T) {
	// The header as the OAuth standard says: each half URL-escaped, then
	// joined, then base64. A secret with a +, a %, a : and a space shows the
	// difference from a bare SetBasicAuth.
	header := hytaleBasic("id:with+plus", "s+e%c:r et")
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(header, "Basic "))
	require.NoError(t, err)
	require.Equal(t, "id%3Awith%2Bplus:s%2Be%25c%3Ar+et", string(decoded))
	id, secret, ok := hytaleClientFrom(header)
	require.True(t, ok)
	require.Equal(t, "id:with+plus", id)
	require.Equal(t, "s+e%c:r et", secret)

	// And the whole flow against an issuer that unescapes as fosite does.
	s := newAccountsScene(t)
	state, location := s.startConnect(t, hytaleProvider, acDave)
	s.grantHytale(t, location, "the-code", hytaleGrant{info: hytaleProfile(daveHytaleUUID, "Steve_Hytale")})

	response := s.callback(hytaleProvider, acDave, web(acDave), "code=the-code&scope=openid+hytale%3Aprofile&state="+state)
	requirePage(t, response, http.StatusOK, "Hytale connected")
	require.Contains(t, response.Body.String(), "Connected as <strong>Steve_Hytale</strong> (Hytale username).")

	auths, bodies := s.hytale.tokenRequests()
	require.Len(t, auths, 1)
	require.Equal(t, hytaleBasic(testHytaleClientID, testHytaleSecret), auths[0])
	require.Contains(t, auths[0], base64.StdEncoding.EncodeToString([]byte(testHytaleClientID+":hy%2Btale%25sec%3Aret%2F42")))

	form, err := url.ParseQuery(bodies[0])
	require.NoError(t, err)
	require.Equal(t, []string{"code", "code_verifier", "grant_type", "redirect_uri"}, sortedKeys(form))
	require.Equal(t, "authorization_code", form.Get("grant_type"))
	require.Equal(t, "the-code", form.Get("code"))
	require.Equal(t, "https://chat.example.org/plugins/ceo.villa.matty/api/v1/hytale/callback", form.Get("redirect_uri"))
	require.Len(t, form.Get("code_verifier"), 43)
	require.NotContains(t, bodies[0], testHytaleClientID)
	require.NotContains(t, bodies[0], url.QueryEscape(testHytaleSecret))
	require.Equal(t, "grant_type=authorization_code&code=the-code&redirect_uri="+
		url.QueryEscape("https://chat.example.org/plugins/ceo.villa.matty/api/v1/hytale/callback")+"&code_verifier="+form.Get("code_verifier"), bodies[0], "in a fixed order")

	for _, seen := range s.fake.requests() {
		if seen.path == "/oauth2/token" {
			require.Equal(t, "application/x-www-form-urlencoded", seen.contentType)
			require.Empty(t, seen.query)
		}
	}

	require.Equal(t, accountLink{ID: daveHytaleUUID, Name: "Steve_Hytale", At: s.now().UnixMilli()}, s.links(t, hytaleProvider).Users[acDave])
}

func sortedKeys(values url.Values) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}

func TestHytaleIDTokenChecks(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	nonce := strings.Repeat("ab", 32)

	token := func(change map[string]any) string {
		claims := map[string]any{"iss": hytaleIssuer, "aud": testHytaleClientID, "exp": now.Add(time.Hour).Unix(), "nonce": nonce, "sub": testHytaleSub}
		for name, value := range change {
			if value == nil {
				delete(claims, name)
				continue
			}
			claims[name] = value
		}

		return fakeIDToken(claims)
	}

	for _, tried := range []struct {
		name   string
		token  string
		refuse string // "" when accepted
	}{
		{"as Hytale issues it", token(nil), ""},
		{"an audience of one in an array", token(map[string]any{"aud": []string{testHytaleClientID}}), ""},
		{"a 43-character base64url sub", token(map[string]any{"sub": "Kk9cYlqd1WOTjw4xI65u8mo5CHhcpiG3wu24dHul6kM"}), ""},
		{"a uuid-shaped sub", token(map[string]any{"sub": "8f14e45f-ceea-467f-a8f8-7b4e2b5c3d21"}), ""},
		{"expired a little, inside the leeway", token(map[string]any{"exp": now.Add(-4 * time.Minute).Unix()}), ""},
		{"another issuer", token(map[string]any{"iss": "https://oauth.accounts.hytale.com"}), "iss"},
		{"the fake's own host as issuer", token(map[string]any{"iss": "https://127.0.0.1:1234"}), "iss"},
		{"another audience", token(map[string]any{"aud": "somebody-else"}), "aud"},
		{"an audience of two", token(map[string]any{"aud": []string{testHytaleClientID, "somebody-else"}}), "aud"},
		{"no audience", token(map[string]any{"aud": nil}), "aud"},
		{"expired", token(map[string]any{"exp": now.Add(-6 * time.Minute).Unix()}), "exp"},
		{"no expiry", token(map[string]any{"exp": nil}), "exp"},
		{"an expiry that is not an integer", token(map[string]any{"exp": "tomorrow"}), "shape"},
		{"another nonce", token(map[string]any{"nonce": strings.Repeat("cd", 32)}), "nonce"},
		{"no nonce", token(map[string]any{"nonce": nil}), "nonce"},
		{"no sub", token(map[string]any{"sub": nil}), "sub"},
		{"an empty sub", token(map[string]any{"sub": ""}), "sub"},
		{"a sub over 255 bytes", token(map[string]any{"sub": strings.Repeat("s", 256)}), "sub"},
		{"a sub with a control character", token(map[string]any{"sub": "abc\ndef"}), "sub"},
		{"a sub that is not a string", token(map[string]any{"sub": 12345}), "shape"},
		{"two parts", "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ4In0", "shape"},
		{"not base64", "a.%%%.c", "shape"},
		{"not JSON", "a." + base64.RawURLEncoding.EncodeToString([]byte("not json")) + ".c", "shape"},
		{"oversized", token(map[string]any{"padding": strings.Repeat("p", 9000)}), "size"},
	} {
		sub, err := checkHytaleIDToken(tried.token, testHytaleClientID, nonce, now)
		if tried.refuse == "" {
			require.NoError(t, err, tried.name)
			require.NotEmpty(t, sub, tried.name)
			continue
		}

		var refused *hytaleIDTokenError
		require.ErrorAs(t, err, &refused, tried.name)
		require.Equal(t, tried.refuse, refused.code, tried.name)
		require.Empty(t, sub, tried.name)
	}

	// An empty nonce on our side never matches, whatever the token says.
	_, err := checkHytaleIDToken(fakeIDToken(map[string]any{"iss": hytaleIssuer, "aud": testHytaleClientID, "exp": now.Add(time.Hour).Unix(), "nonce": "", "sub": "x"}), testHytaleClientID, "", now)
	require.Error(t, err)
}

func TestHytaleUserinfoMustMatchAndHaveAProfile(t *testing.T) {
	for _, tried := range []struct {
		name    string
		grant   hytaleGrant
		status  int
		heading string
		code    string
	}{
		{"another person's sub", hytaleGrant{info: map[string]any{"sub": "somebody-else", "profile": map[string]any{"uuid": daveHytaleUUID, "username": "Steve_Hytale"}}},
			http.StatusBadGateway, "Hytale did not confirm it", accountCodeProviderError},
		{"no profile", hytaleGrant{info: map[string]any{"sub": testHytaleSub}}, http.StatusConflict, "No game profile", accountCodeNoProfile},
		{"a null profile", hytaleGrant{info: map[string]any{"sub": testHytaleSub, "profile": nil}}, http.StatusConflict, "No game profile", accountCodeNoProfile},
		{"a uuid that is not one", hytaleGrant{info: hytaleProfile("not-a-uuid", "Steve_Hytale")}, http.StatusBadGateway, "Hytale did not confirm it", accountCodeProviderError},
		{"a username with a space", hytaleGrant{info: hytaleProfile(daveHytaleUUID, "Steve Hytale")}, http.StatusBadGateway, "Hytale did not confirm it", accountCodeProviderError},
		{"a username too long", hytaleGrant{info: hytaleProfile(daveHytaleUUID, strings.Repeat("s", 33))}, http.StatusBadGateway, "Hytale did not confirm it", accountCodeProviderError},
		{"a signed answer", hytaleGrant{infoType: "application/jwt"}, http.StatusBadGateway, "Hytale did not confirm it", accountCodeProviderError},
	} {
		t.Run(tried.name, func(t *testing.T) {
			s := newAccountsScene(t)

			state, location := s.startConnect(t, hytaleProvider, acDave)
			s.grantHytale(t, location, "code-web", tried.grant)
			requirePage(t, s.callback(hytaleProvider, acDave, web(acDave), "code=code-web&state="+state), tried.status, tried.heading)

			state, location = s.appStart(t, hytaleProvider, acDave)
			s.grantHytale(t, location, "code-app", tried.grant)
			requireRefused(t, s.appFinish(hytaleProvider, acDave, matty(acDave), "code=code-app&state="+state), accountCodeStatus[tried.code], tried.code, "in Matty")

			require.Empty(t, s.links(t, hytaleProvider).Users)
		})
	}

	// A profile id is kept in lower case, and the username as Hytale sent it.
	s := newAccountsScene(t)
	s.connectHytale(t, acDave, strings.ToUpper(daveHytaleUUID), "Steve_Hytale")
	require.Equal(t, daveHytaleUUID, s.links(t, hytaleProvider).Users[acDave].ID)
}

func TestHytaleRevokesWhateverHappens(t *testing.T) {
	for _, tried := range []struct {
		name    string
		grant   hytaleGrant
		prepare func(s *accountsScene)
		status  int
	}{
		{name: "connected", status: http.StatusOK},
		{name: "an ID token refused", grant: hytaleGrant{claims: map[string]any{"aud": "somebody-else"}}, status: http.StatusBadGateway},
		{name: "userinfo refused", grant: hytaleGrant{info: map[string]any{"sub": "somebody-else"}}, status: http.StatusBadGateway},
		{name: "no profile", grant: hytaleGrant{info: map[string]any{"sub": testHytaleSub}}, status: http.StatusConflict},
		{name: "a store failure", prepare: func(s *accountsScene) {
			s.faults.setOnRead(nil)
			s.faults.failWrite(kvHytaleLinks)
		}, status: http.StatusServiceUnavailable},
	} {
		t.Run(tried.name, func(t *testing.T) {
			s := newAccountsScene(t)
			state, location := s.startConnect(t, hytaleProvider, acDave)
			s.grantHytale(t, location, "code-r", tried.grant)
			if tried.prepare != nil {
				tried.prepare(s)
			}

			response := s.callback(hytaleProvider, acDave, web(acDave), "code=code-r&state="+state)
			require.Equal(t, tried.status, response.Code, response.Body.String())

			auths, forms := s.hytale.revoked()
			require.Len(t, auths, 1, "one revoke")
			require.Equal(t, hytaleBasic(testHytaleClientID, testHytaleSecret), auths[0], "with the application's Basic sign-in")
			require.Equal(t, testHytaleAccessToken+"code-r", forms[0].Get("token"))
			require.Equal(t, "access_token", forms[0].Get("token_type_hint"))
			require.Empty(t, forms[0].Get("client_secret"))
		})
	}

	// No token, nothing to revoke.
	s := newAccountsScene(t)
	s.hytale.answerTokens(http.StatusBadRequest, `{"error":"invalid_grant"}`)
	state, _ := s.startConnect(t, hytaleProvider, acDave)
	requirePage(t, s.callback(hytaleProvider, acDave, web(acDave), "code=code-x&state="+state), http.StatusBadGateway, "Hytale did not confirm it")
	auths, _ := s.hytale.revoked()
	require.Empty(t, auths)

	// A token answer without the ID token, or not Bearer, is no token.
	for _, answer := range []string{
		`{"access_token":"abc","token_type":"bearer"}`,
		`{"access_token":"abc","id_token":"a.b.c","token_type":"mac"}`,
		`{"access_token":"a b","id_token":"a.b.c","token_type":"Bearer"}`,
		`{"access_token":"` + strings.Repeat("a", 4097) + `","id_token":"a.b.c","token_type":"Bearer"}`,
	} {
		s.hytale.answerTokens(http.StatusOK, answer)
		state, _ := s.startConnect(t, hytaleProvider, acErin)
		requirePage(t, s.callback(hytaleProvider, acErin, web(acErin), "code=code-y&state="+state), http.StatusBadGateway, "Hytale did not confirm it", answer[:30])
		s.clock.advance(4 * time.Minute)
	}
}

func TestHytaleErrorsComeBackAsPages(t *testing.T) {
	s := newAccountsScene(t)

	call := func(query string) (string, string) {
		state, _ := s.startConnect(t, hytaleProvider, acDave)
		before := len(s.logged())
		response := s.callback(hytaleProvider, acDave, web(acDave), query+"&state="+state)
		s.clock.advance(4 * time.Minute)

		return response.Body.String(), strings.Join(s.logged()[before:], "\n")
	}

	// The person said no on Hytale's page.
	page, logged := call("error=access_denied&error_description=The+resource+owner+denied+the+request")
	require.Contains(t, page, "<h1>Not connected</h1>")
	require.Contains(t, page, "<script>"+twitchPageScript+"</script>", "the window may close")
	require.Empty(t, logged)

	// The application was not granted the profile scope: the page says so,
	// an admin's /me and the page's admin line too.
	page, _ = call("error=invalid_scope")
	require.Contains(t, page, "<h1>Not approved yet</h1>")
	require.NotContains(t, page, `class="problem"`, "Dave is no admin")
	require.Equal(t, "Hytale has not granted the application the scope hytale:profile.", s.me(t, acBoss).Hytale.Problem)
	require.Empty(t, s.me(t, acDave).Hytale.Problem)
	state, _ := s.startConnect(t, hytaleProvider, acBoss)
	admin := s.callback(hytaleProvider, acBoss, web(acBoss), "error=invalid_scope&state="+state)
	require.Contains(t, admin.Body.String(), accountScopeProblem)

	// Any other word is logged as itself; anything else as other.
	_, logged = call("error=server_error")
	require.Contains(t, logged, "errorserver_error")
	s.clock.advance(16 * time.Minute)
	_, logged = call("error=" + url.QueryEscape(strings.Repeat("x", 10000)+"\nthen this"))
	require.Contains(t, logged, "errorother")
	require.NotContains(t, logged, "then this")
	require.NotContains(t, logged, "xxxxxxxxxx")

	// Nothing was asked of Hytale for any of these.
	require.Empty(t, s.fake.requests())

	// A code that is missing, repeated or too long is no code.
	for _, query := range []string{"scope=openid", "code=a&code=b", "code=" + strings.Repeat("c", 513)} {
		page, _ := call(query)
		require.Contains(t, page, "<h1>This link has run out</h1>", query)
	}
	require.Empty(t, s.fake.requests())

	// A connect that works clears the admin's scope line.
	s.connectHytale(t, acErin, erinHytaleUUID, "Erin_Builds")
	require.Empty(t, s.me(t, acBoss).Hytale.Problem)
}

func TestHytaleRefusingTheApplicationTellsTheAdmin(t *testing.T) {
	s := newAccountsScene(t)
	s.configure(func(cfg *configuration) { cfg.HytaleClientSecret = "the-wrong-secret" })

	state, location := s.startConnect(t, hytaleProvider, acDave)
	s.grantHytale(t, location, "code-c", hytaleGrant{})
	requirePage(t, s.callback(hytaleProvider, acDave, web(acDave), "code=code-c&state="+state), http.StatusBadGateway, "Hytale did not confirm it")

	require.Equal(t, "Hytale refused the application's Client ID or Client Secret. Check them in the Hytale section.", s.me(t, acBoss).Hytale.Problem)
	require.Empty(t, s.me(t, acDave).Hytale.Problem)
	auths, _ := s.hytale.revoked()
	require.Empty(t, auths, "no token was issued")
}
