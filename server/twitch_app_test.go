package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/require"
)

// The Twitch application's token is shared by everything that talks to
// Twitch, so these tests are about the token alone: fetched once, shared,
// renewed, dropped when refused, never fetched for nothing, and never written
// anywhere a person could read it. Twitch is the media fake, and time is a
// clock the test moves by hand.

// gate is a handler that has been entered and is held until released. release
// is safe to call more than once, and runs at cleanup too, so a failing test
// never leaves the fake server waiting on a handler for ever.
type gate struct {
	entered chan struct{}
	enter   sync.Once
	release chan struct{}
	open    sync.Once
}

func newGate(t *testing.T) *gate {
	t.Helper()

	g := &gate{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(g.Release)

	return g
}

// Hold marks the gate entered and waits to be released.
func (g *gate) Hold() {
	g.enter.Do(func() { close(g.entered) })
	<-g.release
}

func (g *gate) Release() {
	g.open.Do(func() { close(g.release) })
}

func writeTwitchToken(w http.ResponseWriter, token string, expiresIn int) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": token, "expires_in": expiresIn, "token_type": "bearer"})
}

// twitchFixture is a plugin with Twitch credentials, its clock, and the
// credentials as the token component is handed them.
func twitchFixture(t *testing.T, cfg *configuration) (*Plugin, *fakeClock, twitchCredentials) {
	t.Helper()

	p, _ := mediaPlugin(t, cfg)
	clock := newFakeClock()
	useClock(p, clock)

	creds, _ := p.getConfiguration().twitchCredentials()

	return p, clock, creds
}

func tokenPosts(fake *mediaFake) int {
	return fake.requestsTo("/oauth2/token")
}

func warnings(p *Plugin, containing string) int {
	api, _ := p.API.(*plugintest.API)

	count := 0
	for _, call := range api.Calls {
		if call.Method == "LogWarn" && strings.Contains(fmt.Sprint(call.Arguments...), containing) {
			count++
		}
	}

	return count
}

func TestTheAppTokenIsFetchedOnceAndReused(t *testing.T) {
	fake := newMediaFake(t)
	fake.igdbToken("tok-1", 5000000)

	p, _, creds := twitchFixture(t, twitchConfig())

	for i := 0; i < 2; i++ {
		token, err := p.twitchApp().appToken(t.Context(), creds)
		require.NoError(t, err)
		require.Equal(t, "tok-1", token)
	}

	asked := fake.requests()
	require.Len(t, asked, 1, "one fetch, then the same token again")
	require.Equal(t, http.MethodPost, asked[0].method)
	require.Equal(t, "/oauth2/token", asked[0].path)
	require.Empty(t, asked[0].query, "nothing in the address, so the secret is never part of a URL")
	require.Equal(t, "application/x-www-form-urlencoded", asked[0].contentType)

	form, err := url.ParseQuery(asked[0].body)
	require.NoError(t, err)
	require.Equal(t, url.Values{
		"client_id":     {testTwitchClientID},
		"client_secret": {testTwitchClientSecret},
		"grant_type":    {"client_credentials"},
	}, form)
}

// The token belongs to the Twitch application, not to the movie lookups: a
// later feature with its own switch uses it with lookups off.
func TestTheTokenDoesNotDependOnLookupsBeingOn(t *testing.T) {
	fake := newMediaFake(t)
	fake.igdbToken("tok-1", 5000000)

	cfg := twitchConfig()
	cfg.MediaLookup = false
	p, _, creds := twitchFixture(t, cfg)

	token, err := p.twitchApp().appToken(t.Context(), creds)
	require.NoError(t, err)
	require.Equal(t, "tok-1", token)
	require.Equal(t, 1, tokenPosts(fake))
}

func TestEverybodyWaitingForATokenSharesOneFetch(t *testing.T) {
	fake := newMediaFake(t)
	held := newGate(t)
	fake.route("/oauth2/token", func(w http.ResponseWriter, _ *http.Request) {
		held.Hold()
		writeTwitchToken(w, "tok-shared", 5000000)
	})

	p, _, creds := twitchFixture(t, twitchConfig())
	app := p.twitchApp()

	tokens := make([]string, 20)
	errs := make([]error, 20)
	var done sync.WaitGroup
	ask := func(i int) {
		done.Add(1)
		go func() {
			defer done.Done()
			tokens[i], errs[i] = app.appToken(t.Context(), creds)
		}()
	}

	// The first is inside the fetch before the rest start. The flight is
	// registered before the handler runs and stays registered until it is
	// released, so every later caller either joins it or finds its token.
	ask(0)
	<-held.entered
	for i := 1; i < 20; i++ {
		ask(i)
	}
	held.Release()
	done.Wait()

	for i := range tokens {
		require.NoError(t, errs[i])
		require.Equal(t, "tok-shared", tokens[i])
	}
	require.Equal(t, 1, tokenPosts(fake))
}

func TestTheTokenIsRenewedNearItsExpiry(t *testing.T) {
	fake := newMediaFake(t)

	var issued atomic.Int32
	fake.route("/oauth2/token", func(w http.ResponseWriter, _ *http.Request) {
		writeTwitchToken(w, fmt.Sprintf("tok-%d", issued.Add(1)), 10800)
	})

	p, clock, creds := twitchFixture(t, twitchConfig())
	start := clock.now()
	fake.route("/oauth2/validate", func(w http.ResponseWriter, _ *http.Request) {
		left := start.Add(3 * time.Hour).Sub(clock.now())
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": testTwitchClientID, "expires_in": int(left.Seconds())})
	})

	app := p.twitchApp()
	token, err := app.appToken(t.Context(), creds)
	require.NoError(t, err)
	require.Equal(t, "tok-1", token)

	clock.advance(30 * time.Minute)
	token, err = app.appToken(t.Context(), creds)
	require.NoError(t, err)
	require.Equal(t, "tok-1", token)
	require.Len(t, fake.requests(), 1, "nothing asked half an hour in")

	clock.advance(31 * time.Minute)
	token, err = app.appToken(t.Context(), creds)
	require.NoError(t, err)
	require.Equal(t, "tok-1", token)
	require.Equal(t, 1, fake.requestsTo("/oauth2/validate"), "validated after an hour")
	require.Equal(t, 1, tokenPosts(fake), "and not renewed")

	// Less than an hour left: renewed.
	clock.advance(time.Hour)
	token, err = app.appToken(t.Context(), creds)
	require.NoError(t, err)
	require.Equal(t, "tok-2", token)
	require.Equal(t, 2, tokenPosts(fake))
}

// A token in its last hour still works. Twitch failing or refusing to renew
// it then does not take it away: it is used until it expires, and the
// renewal is tried again after the usual pause.
func TestARenewalThatFailsKeepsTheTokenItHas(t *testing.T) {
	for _, tried := range []struct {
		name   string
		status int
		pause  time.Duration
	}{
		{"Twitch failing", http.StatusServiceUnavailable, twitchFailurePause},
		{"Twitch refusing", http.StatusForbidden, twitchRefusedPause},
	} {
		t.Run(tried.name, func(t *testing.T) {
			fake := newMediaFake(t)
			fake.igdbToken("tok-1", 10800)

			p, clock, creds := twitchFixture(t, twitchConfig())
			app := p.twitchApp()

			token, err := app.appToken(t.Context(), creds)
			require.NoError(t, err)
			require.Equal(t, "tok-1", token)

			// Fifty-nine minutes left, and no renewal to be had.
			fake.status("/oauth2/token", tried.status)
			clock.advance(2*time.Hour + time.Minute)
			token, err = app.appToken(t.Context(), creds)
			require.NoError(t, err)
			require.Equal(t, "tok-1", token)
			require.Equal(t, 2, tokenPosts(fake))

			clock.advance(tried.pause - time.Second)
			token, err = app.appToken(t.Context(), creds)
			require.NoError(t, err)
			require.Equal(t, "tok-1", token)
			require.Equal(t, 2, tokenPosts(fake), "not asked again within the pause")

			clock.advance(2 * time.Second)
			token, err = app.appToken(t.Context(), creds)
			require.NoError(t, err)
			require.Equal(t, "tok-1", token)
			require.Equal(t, 3, tokenPosts(fake), "asked again after it")

			// Expired: nothing left to fall back on.
			clock.advance(time.Hour)
			token, err = app.appToken(t.Context(), creds)
			require.Error(t, err)
			require.Empty(t, token)
		})
	}
}

func TestA401LeadsToANewToken(t *testing.T) {
	fake := newMediaFake(t)

	var issued atomic.Int32
	fake.route("/oauth2/token", func(w http.ResponseWriter, _ *http.Request) {
		writeTwitchToken(w, fmt.Sprintf("tok-%d", issued.Add(1)), 5000000)
	})

	p, _, creds := twitchFixture(t, twitchConfig())
	app := p.twitchApp()

	token, err := app.appToken(t.Context(), creds)
	require.NoError(t, err)
	require.Equal(t, "tok-1", token)

	// Whoever was refused with it says so, and the next caller gets another.
	app.invalidate("tok-1")
	token, err = app.appToken(t.Context(), creds)
	require.NoError(t, err)
	require.Equal(t, "tok-2", token)
	require.Equal(t, 2, tokenPosts(fake))

	// A late report about the old token does not throw away the new one.
	app.invalidate("tok-1")
	token, err = app.appToken(t.Context(), creds)
	require.NoError(t, err)
	require.Equal(t, "tok-2", token)
	require.Equal(t, 2, tokenPosts(fake))
}

func TestTheTokenIsValidatedHourly(t *testing.T) {
	for _, tried := range []struct {
		name     string
		answer   func(w http.ResponseWriter)
		newToken bool
	}{
		{"still good", func(w http.ResponseWriter) {
			_ = json.NewEncoder(w).Encode(map[string]any{"client_id": testTwitchClientID, "expires_in": 4000000})
		}, false},
		{"refused", func(w http.ResponseWriter) { w.WriteHeader(http.StatusUnauthorized) }, true},
		{"another application's", func(w http.ResponseWriter) {
			_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "someone-else", "expires_in": 4000000})
		}, true},
	} {
		t.Run(tried.name, func(t *testing.T) {
			fake := newMediaFake(t)

			var issued atomic.Int32
			fake.route("/oauth2/token", func(w http.ResponseWriter, _ *http.Request) {
				writeTwitchToken(w, fmt.Sprintf("tok-%d", issued.Add(1)), 5000000)
			})
			fake.route("/oauth2/validate", func(w http.ResponseWriter, _ *http.Request) { tried.answer(w) })

			p, clock, creds := twitchFixture(t, twitchConfig())
			app := p.twitchApp()

			_, err := app.appToken(t.Context(), creds)
			require.NoError(t, err)

			clock.advance(61 * time.Minute)
			token, err := app.appToken(t.Context(), creds)
			require.NoError(t, err)

			var validated []seenRequest
			for _, seen := range fake.requests() {
				if seen.path == "/oauth2/validate" {
					validated = append(validated, seen)
				}
			}
			require.Len(t, validated, 1)
			require.Equal(t, http.MethodGet, validated[0].method)
			require.Equal(t, "OAuth tok-1", validated[0].auth)

			if tried.newToken {
				require.Equal(t, "tok-2", token)
				require.Equal(t, 2, tokenPosts(fake))
			} else {
				require.Equal(t, "tok-1", token)
				require.Equal(t, 1, tokenPosts(fake))
			}
		})
	}

	// Twitch failing to answer is not Twitch saying no: the token is used
	// anyway, and asked about again five minutes later.
	t.Run("not answered", func(t *testing.T) {
		fake := newMediaFake(t)
		fake.igdbToken("tok-1", 5000000)
		fake.status("/oauth2/validate", http.StatusInternalServerError)

		p, clock, creds := twitchFixture(t, twitchConfig())
		app := p.twitchApp()

		_, err := app.appToken(t.Context(), creds)
		require.NoError(t, err)

		clock.advance(61 * time.Minute)
		token, err := app.appToken(t.Context(), creds)
		require.NoError(t, err)
		require.Equal(t, "tok-1", token)
		require.Equal(t, 1, fake.requestsTo("/oauth2/validate"))

		clock.advance(4 * time.Minute)
		_, err = app.appToken(t.Context(), creds)
		require.NoError(t, err)
		require.Equal(t, 1, fake.requestsTo("/oauth2/validate"), "not again within five minutes")

		clock.advance(time.Minute)
		_, err = app.appToken(t.Context(), creds)
		require.NoError(t, err)
		require.Equal(t, 2, fake.requestsTo("/oauth2/validate"), "again after five")
		require.Equal(t, 1, tokenPosts(fake))
	})
}

func TestRefusedCredentialsAreReportedOnceAndNotRetriedAtOnce(t *testing.T) {
	fake := newMediaFake(t)
	fake.route("/oauth2/token", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"status":400,"message":"invalid client"}`))
	})

	p, clock, creds := twitchFixture(t, twitchConfig())
	app := p.twitchApp()

	_, err := app.appToken(t.Context(), creds)
	require.ErrorIs(t, err, errTwitchRefused)
	require.Equal(t, 1, tokenPosts(fake))
	require.Equal(t, 1, warnings(p, "Twitch refused the Twitch application's Client ID or Client Secret"))

	clock.advance(4 * time.Minute)
	_, err = app.appToken(t.Context(), creds)
	require.ErrorIs(t, err, errTwitchRefused)
	require.Equal(t, 1, tokenPosts(fake), "the same credentials are not tried again at once")
	require.Equal(t, 1, warnings(p, "Twitch refused"), "and the admin is not told twice")

	clock.advance(2 * time.Minute)
	_, err = app.appToken(t.Context(), creds)
	require.ErrorIs(t, err, errTwitchRefused)
	require.Equal(t, 2, tokenPosts(fake), "tried again after five minutes")

	// A newly pasted secret is tried at once.
	fake.igdbToken("tok-new", 5000000)
	fixed := twitchCredentials{clientID: creds.clientID, secret: "a-new-secret"}
	token, err := app.appToken(t.Context(), fixed)
	require.NoError(t, err)
	require.Equal(t, "tok-new", token)
	require.Equal(t, 3, tokenPosts(fake))
}

func TestATransientFailurePausesBriefly(t *testing.T) {
	fake := newMediaFake(t)
	fake.status("/oauth2/token", http.StatusInternalServerError)

	p, clock, creds := twitchFixture(t, twitchConfig())
	app := p.twitchApp()

	_, err := app.appToken(t.Context(), creds)
	require.ErrorIs(t, err, errTwitchUnavailable)
	require.Equal(t, 1, warnings(p, "Twitch did not issue an access token just now"))

	clock.advance(10 * time.Second)
	_, err = app.appToken(t.Context(), creds)
	require.ErrorIs(t, err, errTwitchUnavailable)
	require.Equal(t, 1, tokenPosts(fake))

	fake.igdbToken("tok-1", 5000000)
	clock.advance(6 * time.Second)
	token, err := app.appToken(t.Context(), creds)
	require.NoError(t, err)
	require.Equal(t, "tok-1", token)
	require.Equal(t, 2, tokenPosts(fake))
}

func TestAStrangeTokenAnswerIsNotKept(t *testing.T) {
	for _, tried := range []struct {
		name   string
		answer map[string]any
		kept   bool
	}{
		{"no token", map[string]any{"expires_in": 5000000, "token_type": "bearer"}, false},
		{"another kind of token", map[string]any{"access_token": "tok", "expires_in": 5000000, "token_type": "mac"}, false},
		{"a token with a space", map[string]any{"access_token": "tok en", "expires_in": 5000000, "token_type": "bearer"}, false},
		{"a token too long", map[string]any{"access_token": strings.Repeat("t", 513), "expires_in": 5000000, "token_type": "bearer"}, false},
		{"Bearer in capitals", map[string]any{"access_token": "tok", "expires_in": 5000000, "token_type": "Bearer"}, true},
		{"no expiry", map[string]any{"access_token": "tok", "token_type": "bearer"}, true},
	} {
		t.Run(tried.name, func(t *testing.T) {
			fake := newMediaFake(t)
			fake.json("/oauth2/token", tried.answer)

			p, clock, creds := twitchFixture(t, twitchConfig())
			app := p.twitchApp()

			token, err := app.appToken(t.Context(), creds)
			if !tried.kept {
				require.ErrorIs(t, err, errTwitchUnavailable)
				require.Empty(t, token)

				app.lock.Lock()
				require.Empty(t, app.token, "nothing strange is held")
				app.lock.Unlock()

				return
			}

			require.NoError(t, err)
			require.Equal(t, "tok", token)

			app.lock.Lock()
			defer app.lock.Unlock()
			if _, given := tried.answer["expires_in"]; !given {
				require.Equal(t, clock.now().Add(24*time.Hour), app.expiresAt, "a missing expiry counts as a day")
			}
		})
	}
}

func TestChangingTheCredentialsDropsTheToken(t *testing.T) {
	t.Run("between fetches", func(t *testing.T) {
		fake := newMediaFake(t)
		fake.route("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			writeTwitchToken(w, "tok-for-"+r.PostForm.Get("client_secret"), 5000000)
		})

		p, _, creds := twitchFixture(t, twitchConfig())
		app := p.twitchApp()

		token, err := app.appToken(t.Context(), creds)
		require.NoError(t, err)
		require.Equal(t, "tok-for-"+testTwitchClientSecret, token)

		changed := twitchCredentials{clientID: creds.clientID, secret: "the-new-secret"}
		token, err = app.appToken(t.Context(), changed)
		require.NoError(t, err)
		require.Equal(t, "tok-for-the-new-secret", token)

		asked := fake.requests()
		require.Len(t, asked, 2)
		form, err := url.ParseQuery(asked[1].body)
		require.NoError(t, err)
		require.Equal(t, "the-new-secret", form.Get("client_secret"))
	})

	t.Run("during a fetch", func(t *testing.T) {
		fake := newMediaFake(t)
		held := newGate(t)
		fake.route("/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
			_ = r.ParseForm()
			secret := r.PostForm.Get("client_secret")
			if secret == testTwitchClientSecret {
				held.Hold()
			}
			writeTwitchToken(w, "tok-for-"+secret, 5000000)
		})

		p, _, creds := twitchFixture(t, twitchConfig())
		app := p.twitchApp()

		var old string
		var oldErr error
		var done sync.WaitGroup
		done.Add(1)
		go func() {
			defer done.Done()
			old, oldErr = app.appToken(t.Context(), creds)
		}()
		<-held.entered

		// The admin pastes a new secret while the old one's fetch is out: the
		// new one gets its own fetch rather than joining the old.
		changed := twitchCredentials{clientID: creds.clientID, secret: "the-new-secret"}
		token, err := app.appToken(t.Context(), changed)
		require.NoError(t, err)
		require.Equal(t, "tok-for-the-new-secret", token)

		held.Release()
		done.Wait()
		require.NoError(t, oldErr)
		require.Equal(t, "tok-for-"+testTwitchClientSecret, old, "the old fetch's own caller still gets its answer")

		// But that late answer does not displace the new token.
		token, err = app.appToken(t.Context(), changed)
		require.NoError(t, err)
		require.Equal(t, "tok-for-the-new-secret", token)
		require.Equal(t, 2, tokenPosts(fake))
	})
}

func TestNothingIsFetchedWithoutCredentials(t *testing.T) {
	fake := newMediaFake(t)
	fake.igdbToken("tok-1", 5000000)

	p, _, _ := twitchFixture(t, twitchConfig())

	for _, creds := range []twitchCredentials{
		{},
		{clientID: testTwitchClientID},
		{secret: testTwitchClientSecret},
	} {
		_, err := p.twitchApp().appToken(t.Context(), creds)
		require.ErrorIs(t, err, errTwitchNotSetUp)
	}

	for _, cfg := range []*configuration{
		{MediaLookup: true, TwitchClientID: testTwitchClientID},
		{MediaLookup: true, TwitchClientSecret: testTwitchClientSecret},
		{MediaLookup: true, TwitchClientID: testTwitchClientID, TwitchClientSecret: redactedSetting},
	} {
		cfg.normalize()
		_, set := cfg.twitchCredentials()
		require.False(t, set)
	}

	require.Empty(t, fake.requests())
}

func TestTheSecretAndTokenNeverReachALog(t *testing.T) {
	const token = "tok-NEVER-LOGGED-9f8e7d"

	fake := newMediaFake(t)
	p, clock, creds := twitchFixture(t, twitchConfig())
	app := p.twitchApp()

	var errorsSeen []string
	note := func(err error) {
		if err != nil {
			errorsSeen = append(errorsSeen, err.Error())
		}
	}

	// Refused, echoing the secret back as some services do.
	fake.route("/oauth2/token", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"bad client_secret ` + testTwitchClientSecret + `"}`))
	})
	_, err := app.appToken(t.Context(), creds)
	require.ErrorIs(t, err, errTwitchRefused)
	note(err)

	// Failing, likewise.
	clock.advance(6 * time.Minute)
	fake.route("/oauth2/token", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`client_secret=` + testTwitchClientSecret))
	})
	_, err = app.appToken(t.Context(), creds)
	require.ErrorIs(t, err, errTwitchUnavailable)
	note(err)

	// Succeeding, then validated, then validation failing.
	clock.advance(time.Minute)
	fake.igdbToken(token, 5000000)
	got, err := app.appToken(t.Context(), creds)
	require.NoError(t, err)
	require.Equal(t, token, got)

	fake.route("/oauth2/validate", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(token))
	})
	clock.advance(61 * time.Minute)
	_, err = app.appToken(t.Context(), creds)
	note(err)

	api, _ := p.API.(*plugintest.API)
	logged := 0
	for _, call := range api.Calls {
		if !strings.HasPrefix(call.Method, "Log") {
			continue
		}
		logged++

		line := fmt.Sprint(call.Arguments...)
		require.NotContains(t, line, testTwitchClientSecret)
		require.NotContains(t, line, token)
		require.NotContains(t, line, "client_secret")
	}
	require.Positive(t, logged, "something was logged, or this test proves nothing")

	for _, text := range errorsSeen {
		require.NotContains(t, text, testTwitchClientSecret)
		require.NotContains(t, text, token)
		require.NotContains(t, text, "client_secret")
	}
}

// Twitch live's additions to the shared component (design/twitch-live.md §3).

// The code is traded on the server, with the secret in the form body and
// nowhere else. The ID token comes back for checking; the refresh token
// Twitch also sends never leaves exchangeCode.
func TestTheCodeExchangeSendsTheSecretInTheBodyOnly(t *testing.T) {
	s := newTwitchScene(t)
	s.fake.grant("the-code", fakeGrant{sub: "1001", nonce: "n"})
	creds, _ := s.p.getConfiguration().twitchCredentials()

	const redirect = "https://chat.example.com/mm/plugins/ceo.villa.matty/api/v1/twitch/callback"
	token, idToken, err := s.p.twitchApp().exchangeCode(t.Context(), creds, "the-code", redirect)
	require.NoError(t, err)
	require.Equal(t, "utok-the-code", token)
	require.NotEmpty(t, idToken)

	asked := s.fake.requests()
	require.Len(t, asked, 1)
	require.Equal(t, http.MethodPost, asked[0].method)
	require.Equal(t, "/oauth2/token", asked[0].path)
	require.Empty(t, asked[0].query, "nothing in the address")
	require.Equal(t, "application/x-www-form-urlencoded", asked[0].contentType)

	form, err := url.ParseQuery(asked[0].body)
	require.NoError(t, err)
	require.Equal(t, url.Values{
		"client_id":     {testTwitchClientID},
		"client_secret": {testTwitchClientSecret},
		"code":          {"the-code"},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {redirect},
	}, form)
	require.Equal(t, redirect, form.Get("redirect_uri"), "byte for byte the address the authorize request named")

	// Nothing returned carries the refresh token.
	require.NotContains(t, token+idToken, "rtok-")
}

func TestCheckIDToken(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	good := map[string]any{"iss": twitchIssuer, "aud": "client", "exp": now.Add(time.Hour).Unix(), "nonce": "the-nonce", "sub": "141981764"}
	with := func(key string, value any) map[string]any {
		claims := map[string]any{}
		for k, v := range good {
			claims[k] = v
		}
		if value == nil {
			delete(claims, key)
		} else {
			claims[key] = value
		}

		return claims
	}

	sub, err := checkIDToken(fakeIDToken(good), "client", "the-nonce", now)
	require.NoError(t, err)
	require.Equal(t, "141981764", sub)

	sub, err = checkIDToken(fakeIDToken(with("aud", []string{"client"})), "client", "the-nonce", now)
	require.NoError(t, err, "an array of one is the same audience")
	require.Equal(t, "141981764", sub)

	_, err = checkIDToken(fakeIDToken(with("exp", now.Add(-4*time.Minute).Unix())), "client", "the-nonce", now)
	require.NoError(t, err, "inside the clock leeway")

	encodedPayload := func(raw string) string {
		return "eyJhbGciOiJSUzI1NiJ9." + base64.RawURLEncoding.EncodeToString([]byte(raw)) + ".sig"
	}

	for _, tried := range []struct {
		name, token, nonce, code string
	}{
		{"too big", fakeIDToken(with("pad", strings.Repeat("x", maxIDTokenBytes))), "the-nonce", "size"},
		{"two parts", "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxIn0", "the-nonce", "shape"},
		{"bad base64", "eyJhbGciOiJSUzI1NiJ9.!!!.sig", "the-nonce", "shape"},
		{"bad JSON", encodedPayload("{not json"), "the-nonce", "shape"},
		{"wrong iss", fakeIDToken(with("iss", "https://evil.example/oauth2")), "the-nonce", "iss"},
		{"aud another client", fakeIDToken(with("aud", "someone-else")), "the-nonce", "aud"},
		{"aud an array of two", fakeIDToken(with("aud", []string{"client", "someone-else"})), "the-nonce", "aud"},
		{"expired beyond the leeway", fakeIDToken(with("exp", now.Add(-6*time.Minute).Unix())), "the-nonce", "exp"},
		{"wrong nonce", fakeIDToken(good), "another-nonce", "nonce"},
		{"missing nonce", fakeIDToken(with("nonce", nil)), "the-nonce", "nonce"},
		{"no nonce expected", fakeIDToken(good), "", "nonce"},
		{"non-numeric sub", fakeIDToken(with("sub", "twitchdev")), "the-nonce", "sub"},
	} {
		_, err := checkIDToken(tried.token, "client", tried.nonce, now)
		var refused *twitchIDTokenError
		require.ErrorAs(t, err, &refused, tried.name)
		require.Equal(t, tried.code, refused.code, tried.name)
		require.NotContains(t, err.Error(), "the-nonce", "%s: a refusal carries only its code word", tried.name)
	}
}

// Helix is asked whose a user token is with that token and our Client-Id,
// which Helix checks the token was issued to; exactly one user must answer.
func TestWhoseTokenUsesTheUserTokenAndOurClientID(t *testing.T) {
	s := newTwitchScene(t)
	creds, _ := s.p.getConfiguration().twitchCredentials()

	s.fake.grant("one", fakeGrant{who: daveTwitch})
	who, err := s.p.twitchApp().whoseToken(t.Context(), creds, "utok-one")
	require.NoError(t, err)
	require.Equal(t, daveTwitch, who)

	asked := s.fake.requests()
	require.Len(t, asked, 1)
	require.Equal(t, "/helix/users", asked[0].path)
	require.Equal(t, "Bearer utok-one", asked[0].auth)
	require.Empty(t, asked[0].query)

	for _, answer := range []string{`{"data":[]}`, `{"data":[{"id":"1","login":"a","display_name":"A"},{"id":"2","login":"b","display_name":"B"}]}`} {
		s.fake.bytes("/helix/users", []byte(answer))
		_, err := s.p.twitchApp().whoseToken(t.Context(), creds, "utok-one")
		require.Error(t, err, answer)
	}

	s.fake.route("/helix/users", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, testTwitchClientID, r.Header.Get("Client-Id"))
		_, _ = w.Write([]byte(`{"data":[{"id":"1001","login":"Dave TV!","display_name":"x"}]}`))
	})
	_, err = s.p.twitchApp().whoseToken(t.Context(), creds, "utok-one")
	require.Error(t, err, "a login that is not the shape Twitch documents is refused")
}

func TestHelixRetriesOnceAfterA401(t *testing.T) {
	s := newTwitchScene(t)
	creds, _ := s.p.getConfiguration().twitchCredentials()

	var issued, asked atomic.Int32
	s.fake.route("/oauth2/token", func(w http.ResponseWriter, _ *http.Request) {
		writeTwitchToken(w, fmt.Sprintf("app-%d", issued.Add(1)), 5000000)
	})
	s.fake.route("/helix/streams", func(w http.ResponseWriter, r *http.Request) {
		if asked.Add(1) == 1 {
			require.Equal(t, "Bearer app-1", r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusUnauthorized)

			return
		}
		require.Equal(t, "Bearer app-2", r.Header.Get("Authorization"), "a fresh token for the retry")
		_, _ = w.Write([]byte(`{"data":[]}`))
	})

	body, _, err := s.p.twitchApp().helixGet(t.Context(), creds, "/helix/streams", nil)
	require.NoError(t, err)
	require.JSONEq(t, `{"data":[]}`, string(body))
	require.EqualValues(t, 2, asked.Load())
	require.EqualValues(t, 2, issued.Load())

	// Two in a row is an error, and no third try.
	s.fake.status("/helix/streams", http.StatusUnauthorized)
	before := s.requestsTo("/helix/streams")
	_, _, err = s.p.twitchApp().helixGet(t.Context(), creds, "/helix/streams", nil)
	require.ErrorIs(t, err, errTwitchRefused)
	require.Equal(t, before+2, s.requestsTo("/helix/streams"))
}

func TestHelix429CarriesTheReset(t *testing.T) {
	s := newTwitchScene(t)
	creds, _ := s.p.getConfiguration().twitchCredentials()

	reset := time.Date(2026, 10, 2, 12, 3, 0, 0, time.UTC)
	s.fake.route("/helix/streams", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Ratelimit-Limit", "800")
		w.Header().Set("Ratelimit-Remaining", "0")
		w.Header().Set("Ratelimit-Reset", fmt.Sprint(reset.Unix()))
		w.WriteHeader(http.StatusTooManyRequests)
	})

	_, rate, err := s.p.twitchApp().helixGet(t.Context(), creds, "/helix/streams", nil)
	require.ErrorIs(t, err, errTwitchSlowDown)

	var slow *twitchSlowDown
	require.ErrorAs(t, err, &slow)
	require.True(t, slow.reset.Equal(reset))
	require.Equal(t, 800, rate.limit)
	require.Equal(t, 0, rate.remaining)
	require.Equal(t, 1, s.requestsTo("/helix/streams"), "a 429 is never retried at once")
}

func TestHelix503IsRetriedOnce(t *testing.T) {
	s := newTwitchScene(t)
	creds, _ := s.p.getConfiguration().twitchCredentials()

	var waited []time.Duration
	s.p.twitchApp().after = func(d time.Duration) <-chan time.Time {
		waited = append(waited, d)
		return immediately(d)
	}

	var asked atomic.Int32
	s.fake.route("/helix/streams", func(w http.ResponseWriter, _ *http.Request) {
		if asked.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	})

	_, _, err := s.p.twitchApp().helixGet(t.Context(), creds, "/helix/streams", nil)
	require.NoError(t, err)
	require.EqualValues(t, 2, asked.Load())
	require.Equal(t, []time.Duration{time.Second}, waited, "one retry, a second later, on the injected clock")

	// Two 503s are Twitch being unavailable, and there is no third try.
	s.fake.status("/helix/streams", http.StatusServiceUnavailable)
	_, _, err = s.p.twitchApp().helixGet(t.Context(), creds, "/helix/streams", nil)
	require.ErrorIs(t, err, errTwitchUnavailable)
	require.EqualValues(t, 4, s.requestsTo("/helix/streams"))
}

// Every log line written across every step of connecting, and every way it
// can fail, is free of the secret, the tokens, the code, the nonce, the
// ticket and the state; and a callback's error value only reaches the log
// when it is one of Twitch's plain code words.
func TestNoTwitchSecretTokenOrCodeIsLogged(t *testing.T) {
	s := newTwitchScene(t)

	var secrets []string
	remember := func(values ...string) { secrets = append(secrets, values...) }
	remember(testTwitchClientSecret, "app-token")

	// A connection that works: ticket, connect, exchange, whose, revoke.
	ticket := s.ticket(t, twDave)
	remember(ticket)
	response := s.connectWith(twDave, web(twDave), ticket, nil)
	require.Equal(t, http.StatusFound, response.Code)
	location, err := url.Parse(response.Header().Get("Location"))
	require.NoError(t, err)
	state, nonce := location.Query().Get("state"), location.Query().Get("nonce")
	remember(state, nonce)
	s.fake.grant("good-code", fakeGrant{sub: daveTwitch.ID, nonce: nonce, who: daveTwitch})
	remember("good-code", "utok-good-code", "rtok-good-code")
	require.Equal(t, http.StatusOK, s.callback(twDave, web(twDave), "code=good-code&state="+state).Code)

	// An ID token that fails, and a revoke that fails.
	state, nonce = s.startConnect(t, twErin)
	remember(state, nonce)
	s.fake.grant("bad-nonce", fakeGrant{sub: erinTwitch.ID, nonce: "not-" + nonce, who: erinTwitch})
	remember("bad-nonce", "utok-bad-nonce", "not-"+nonce)
	s.fake.status("/oauth2/revoke", http.StatusInternalServerError)
	require.Equal(t, http.StatusBadGateway, s.callback(twErin, web(twErin), "code=bad-nonce&state="+state).Code)

	// An exchange Twitch refuses.
	state, _ = s.startConnect(t, twBob)
	remember(state)
	require.Equal(t, http.StatusBadGateway, s.callback(twBob, web(twBob), "code=nobody-knows&state="+state).Code)
	remember("nobody-knows")

	// Twitch sending somebody back with an error made of anything at all.
	state, _ = s.startConnect(t, twBob)
	long := strings.Repeat("A", 10<<10) + "\nINJECTED log line"
	require.Equal(t, http.StatusBadGateway, s.callback(twBob, web(twBob), "error="+url.QueryEscape(long)+"&error_description=secret-description&state="+state).Code)

	// Twitch refusing the application outright.
	s.fake.status("/oauth2/token", http.StatusForbidden)
	s.p.twitchApp().invalidate("app-token")
	s.clock.advance(time.Hour)
	s.fake.goLive(daveTwitch, "t", "g", 1, s.now())
	s.step(t)

	joined := strings.Join(s.logged(), "\n")
	for _, secret := range secrets {
		require.NotContains(t, joined, secret)
	}
	require.NotContains(t, joined, "INJECTED")
	require.NotContains(t, joined, "secret-description")

	other := false
	for _, call := range s.api.Calls {
		if call.Method == "LogWarn" && len(call.Arguments) == 3 && call.Arguments[1] == "error" {
			other = other || call.Arguments[2] == "other"
		}
	}
	require.True(t, other, "an error value that is not a plain code word is logged as other")
}
