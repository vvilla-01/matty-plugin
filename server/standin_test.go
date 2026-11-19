//go:build villanet_standin

package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/vvilla-01/matty-plugin/build/fakebb/standin"
)

// The stand-in build (standin.go) and the stand-ins themselves
// (build/fakebb/standin), held to each other: the plugin's own code, with
// nothing replaced, against what fakebb runs. Run with
//
//	go test -tags villanet_standin -run StandIn ./server/
//
// which make check does.

func TestStandInBuildAsksThisMachineForSteam(t *testing.T) {
	require.Equal(t, standInSteam, steamOpenIDBase)
	require.Equal(t, standInSteam, steamAPIBase)

	address, err := url.Parse(standInSteam)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", address.Hostname(), "this machine, and nothing else")
}

// The Makefile refuses to release a binary holding the sentence a stand-in
// build refuses with, so the two must agree: read here from the Makefile itself.
func TestStandInRefusalIsWhatTheReleaseGuardLooksFor(t *testing.T) {
	makefile, err := os.ReadFile("../Makefile")
	require.NoError(t, err)

	guard := regexp.MustCompile(`NO_STANDIN = @! grep -q -a -r "([^"]+)" server/dist`).FindSubmatch(makefile)
	require.NotNil(t, guard, "the Makefile's NO_STANDIN guard")
	require.Contains(t, standInSiteRefusal("https://chat.example.org"), string(guard[1]))

	// The signing script refuses the same build by the same sentence.
	script, err := os.ReadFile("../build/sign-release.sh")
	require.NoError(t, err)
	require.Contains(t, string(script), `grep -q -a "`+string(guard[1])+`" -- "$root/$executable"`)
}

func TestStandInBuildStartsOnlyOnThisMachine(t *testing.T) {
	for _, site := range []string{"http://localhost:8065", "http://127.0.0.1:8065", "http://[::1]:8065", "https://localhost"} {
		require.Empty(t, standInSiteRefusal(site), site)
	}
	for _, site := range []string{
		"", "https://chat.example.org", "http://10.0.2.2:8065", "http://192.168.1.20:8065",
		"https://localhost.example.org", "https://example.org/localhost", "http://localhost@example.org", "::::",
	} {
		require.NotEmpty(t, standInSiteRefusal(site), site)
	}

	// On a server people use, activating is refused before anything else is
	// done: this API knows no other call, and one would panic.
	api := &plugintest.API{}
	site := "https://chat.example.org"
	config := &model.Config{}
	config.ServiceSettings.SiteURL = &site
	api.On("GetConfig").Return(config)

	p := &Plugin{}
	p.SetAPI(api)

	err := p.OnActivate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "development build")
}

// The Site URL is looked at again where the stand-in's word is taken, so a
// stand-in build whose Site URL is changed after it started takes it no more:
// no sign-in starts, and one already started is not finished.
func TestStandInBuildTakesTheStandInsWordOnlyWhileTheSiteIsThisMachine(t *testing.T) {
	s := newAccountsScene(t)
	standInSteamFor(t, s, "dave")

	_, location := s.appStart(t, steamProvider, acDave)
	browser := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := browser.Get(location.String() + "&as=dave")
	require.NoError(t, err)
	defer response.Body.Close()
	back, err := url.Parse(response.Header.Get("Location"))
	require.NoError(t, err)

	s.setSite("https://chat.example.org")
	finish := s.appFinish(steamProvider, acDave, matty(acDave), back.RawQuery)
	require.Equal(t, http.StatusBadGateway, finish.Code, finish.Body.String())
	require.Empty(t, s.links(t, steamProvider).Users)

	start := s.appConnect(steamProvider, acErin, matty(acErin))
	require.NotEqual(t, http.StatusOK, start.Code)
	require.NotContains(t, start.Body.String(), standInSteam)
}

// standInSteamFor puts the stand-in Steam where the scene's plugin asks.
func standInSteamFor(t *testing.T, s *accountsScene, accounts ...string) (*standin.Steam, *httptest.Server) {
	t.Helper()

	steam := standin.NewSteam(accounts...)
	steam.Now = s.now
	server := httptest.NewServer(steam)
	t.Cleanup(server.Close)

	// The scene puts both back when the test ends.
	steamOpenIDBase, steamAPIBase = server.URL, server.URL

	// A development server: plain http, on this machine.
	s.setSite("http://localhost:8065")

	return steam, server
}

// standInSignIn is the whole of connecting Steam as Matty does it, with the
// stand-in's sign-in in the middle: app-connect, the sign-in as who, and
// app-finish with the query that came back.
func standInSignIn(t *testing.T, s *accountsScene, userID, who string) *httptest.ResponseRecorder {
	t.Helper()

	_, location := s.appStart(t, steamProvider, userID)

	browser := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := browser.Get(location.String() + "&as=" + url.QueryEscape(who))
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusFound, response.StatusCode)

	back, err := url.Parse(response.Header.Get("Location"))
	require.NoError(t, err)

	return s.appFinish(steamProvider, userID, matty(userID), back.RawQuery)
}

// steamSeenBy is somebody's Steam row in GET /accounts as reader is given it.
func steamSeenBy(t *testing.T, s *accountsScene, reader, userID string) (accountsView, *accountsViewSteamPerson) {
	t.Helper()

	code, body := s.accountsAnswer(t, reader)
	require.Equal(t, http.StatusOK, code, string(body))

	var view accountsView
	require.NoError(t, json.Unmarshal(body, &view))

	return view, view.People[userID].Steam
}

func steerStandIn(t *testing.T, server *httptest.Server, query string) {
	t.Helper()

	response, err := http.Post(server.URL+"/fake/steam?"+query, "", nil)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
}

// Every check the plugin makes of a Steam sign-in passes against the
// stand-in's, the account is the one picked, and what the stand-in is steered
// to say is what everybody who may watch is told.
func TestStandInSteamConnectsAndIsRead(t *testing.T) {
	s := newAccountsScene(t)
	steam, server := standInSteamFor(t, s, "dave=Hades")

	finish := standInSignIn(t, s, acDave, "dave")
	require.Equal(t, http.StatusOK, finish.Code, finish.Body.String())

	link := s.links(t, steamProvider).Users[acDave]
	require.Equal(t, standin.SteamID("dave"), link.ID)
	require.Equal(t, "dave", link.Name, "the name, which the sign-in does not say, from the stand-in's Web API")

	s.step(t)
	view, dave := steamSeenBy(t, s, acErin, acDave)
	require.True(t, view.Steam.On)
	require.True(t, view.Steam.Fresh)
	require.NotNil(t, dave)
	require.Equal(t, "online", dave.State)
	require.True(t, dave.InGame)
	require.Equal(t, "Hades", *dave.Game)
	require.Equal(t, "https://steamcommunity.com/profiles/"+standin.SteamID("dave"), dave.URL)

	// Steered: out of the game, and away.
	steerStandIn(t, server, "who=dave&game=&state=away")
	s.clock.advance(time.Minute)
	s.step(t)
	_, dave = steamSeenBy(t, s, acErin, acDave)
	require.Equal(t, "away", dave.State)
	require.False(t, dave.InGame)
	require.Nil(t, dave.Game)

	// A game Steam gives an id and no name.
	steerStandIn(t, server, "who=dave&state=online&gameid=12345")
	s.clock.advance(time.Minute)
	s.step(t)
	_, dave = steamSeenBy(t, s, acErin, acDave)
	require.True(t, dave.InGame)
	require.Empty(t, *dave.Game)

	// A profile that is not public shows no game, whatever its owner is in.
	steerStandIn(t, server, "who=dave&game=Factorio&private=true")
	s.clock.advance(time.Minute)
	s.step(t)
	_, dave = steamSeenBy(t, s, acErin, acDave)
	require.Equal(t, "offline", dave.State)
	require.False(t, dave.InGame)

	// A new Steam name is taken up.
	steerStandIn(t, server, "who=dave&private=false&name=Dave+on+Steam")
	s.clock.advance(time.Minute)
	s.step(t)
	_, dave = steamSeenBy(t, s, acErin, acDave)
	require.Equal(t, "Dave on Steam", dave.Name)
	require.Equal(t, "Factorio", *dave.Game)

	// Somebody locked is told nothing, as ever.
	code, _ := s.accountsAnswer(t, acDan)
	require.Equal(t, http.StatusForbidden, code)

	require.Positive(t, steam.Calls())
}

// What the stand-in can be told to do wrong is what the plugin has to cope
// with from Steam: a sign-in cancelled, an answer played twice, an account
// taken over, and a Web API that says to slow down.
func TestStandInSteamGoesWrongTheWaySteamDoes(t *testing.T) {
	s := newAccountsScene(t)
	_, server := standInSteamFor(t, s, "dave", "erin")

	browser := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	comeBack := func(userID, answer string) *url.URL {
		t.Helper()

		_, location := s.appStart(t, steamProvider, userID)
		response, err := browser.Get(location.String() + answer)
		require.NoError(t, err)
		defer response.Body.Close()
		require.Equal(t, http.StatusFound, response.StatusCode)

		back, err := url.Parse(response.Header.Get("Location"))
		require.NoError(t, err)

		return back
	}
	code := func(response *httptest.ResponseRecorder) string {
		t.Helper()

		var refusal struct {
			Code string `json:"code"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &refusal), response.Body.String())

		return refusal.Code
	}

	// Cancel on Steam's page: nothing is connected.
	cancelled := s.appFinish(steamProvider, acDave, matty(acDave), comeBack(acDave, "&cancel=1").RawQuery)
	require.Equal(t, accountCodeCancelled, code(cancelled))
	require.Empty(t, s.links(t, steamProvider).Users)

	// An answer changed on its way back is not what the stand-in signed.
	moved := comeBack(acDave, "&as=dave").Query()
	moved.Set("openid.claimed_id", "https://steamcommunity.com/openid/id/"+standin.SteamID("erin"))
	moved.Set("openid.identity", "https://steamcommunity.com/openid/id/"+standin.SteamID("erin"))
	forged := s.appFinish(steamProvider, acDave, matty(acDave), moved.Encode())
	require.Equal(t, accountCodeProviderError, code(forged))
	require.Empty(t, s.links(t, steamProvider).Users)

	// Connected; and erin signing in to the same Steam account takes it over.
	require.Equal(t, http.StatusOK, standInSignIn(t, s, acDave, "dave").Code)
	require.Equal(t, http.StatusOK, standInSignIn(t, s, acErin, "dave").Code)
	users := s.links(t, steamProvider).Users
	require.NotContains(t, users, acDave)
	require.Equal(t, standin.SteamID("dave"), users[acErin].ID)

	// The Web API says to slow down: the last good statuses stand, and Steam
	// is left alone for a while.
	s.step(t)
	view, erin := steamSeenBy(t, s, acBob, acErin)
	require.True(t, view.Steam.Fresh)
	require.Equal(t, "online", erin.State)

	steerStandIn(t, server, "api=429")
	s.clock.advance(time.Minute)
	s.step(t)
	view, erin = steamSeenBy(t, s, acBob, acErin)
	require.True(t, view.Steam.Fresh, "one bad read takes nothing away")
	require.Equal(t, "online", erin.State)
	require.Contains(t, s.p.accounts().adminProblem(steamProvider), "slow down")

	// Still refusing six minutes on: nothing is guessed.
	s.clock.advance(6 * time.Minute)
	s.step(t)
	view, erin = steamSeenBy(t, s, acBob, acErin)
	require.False(t, view.Steam.Fresh)
	require.Empty(t, erin.State)

	steerStandIn(t, server, "api=200")
	s.clock.advance(10 * time.Minute)
	s.step(t)
	view, erin = steamSeenBy(t, s, acBob, acErin)
	require.True(t, view.Steam.Fresh)
	require.Equal(t, "online", erin.State)
}

// The plugin's own Mumble connection, to the stand-in: who is in voice
// arrives, with each name matched to a Mattermost account by the rule in
// voice.go, and so does every change after.
func TestStandInMumbleIsReadLikeAnyMumble(t *testing.T) {
	server, err := standin.NewMumble("dave", "erin=muted", "Caller")
	require.NoError(t, err)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	go func() { _ = server.Serve(listener) }()

	api := &plugintest.API{}
	api.On("GetUserByUsername", mock.Anything).Return(func(username string) (*model.User, *model.AppError) {
		switch username {
		case "dave":
			return &model.User{Id: acDave, Username: "dave"}, nil
		case "erin":
			return &model.User{Id: acErin, Username: "erin"}, nil
		default:
			return nil, model.NewAppError("GetUserByUsername", "app.user.missing", nil, "", http.StatusNotFound)
		}
	}).Maybe()
	api.On("LogWarn", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()

	p := &Plugin{}
	p.SetAPI(api)
	p.rosterCh = make(chan struct{}, 16)

	cfg := &configuration{
		EnableVoice:         true,
		MumbleServer:        listener.Addr().String(),
		MumbleUsername:      "VillaNET",
		VoiceMatchUsernames: true,
	}
	cfg.normalize()
	p.setConfiguration(cfg)

	p.restartVoice()
	t.Cleanup(p.stopVoice)

	roster := func() []voicePerson { return p.getVoice().people }
	eventually := func(want []voicePerson, why string) {
		t.Helper()

		require.Eventually(t, func() bool {
			got := roster()
			if !p.getVoice().connected || len(got) != len(want) {
				return false
			}
			// The session numbers are the stand-in's to give: each is
			// there, and is left out of the comparison.
			for i := range want {
				person := got[i]
				if person.Session == 0 {
					return false
				}
				person.Session = 0
				if person != want[i] {
					return false
				}
			}

			return true
		}, 10*time.Second, 10*time.Millisecond, why)
	}

	// The stand-in's people are all in its one channel under the root.
	eventually([]voicePerson{
		{Name: "Caller", Channel: 1},
		{Name: "dave", UserID: acDave, Channel: 1},
		{Name: "erin", UserID: acErin, Channel: 1, Muted: true},
	}, "everybody on the server, without the plugin's own connection")
	require.Equal(t, []voiceChannel{
		{ID: 0, Name: "Root", Depth: 0},
		{ID: 1, Name: "Voice", Parent: parentOf(0), Depth: 1},
	}, p.getVoice().channels, "and the channel tree")

	// What the people index says of them, which is what both clients draw.
	require.Equal(t, map[string]string{acDave: voiceOpen, acErin: voiceMuted}, voiceByUser(p.voiceOf(cfg)))
	require.Equal(t, []string{"VillaNET"}, server.Connected())

	server.Join("dave", proto.Bool(true), nil)
	eventually([]voicePerson{
		{Name: "Caller", Channel: 1},
		{Name: "dave", UserID: acDave, Channel: 1, Muted: true},
		{Name: "erin", UserID: acErin, Channel: 1, Muted: true},
	}, "a mute")

	server.Leave("erin")
	server.Join("bob", nil, nil)
	eventually([]voicePerson{
		{Name: "bob", Channel: 1},
		{Name: "Caller", Channel: 1},
		{Name: "dave", UserID: acDave, Channel: 1, Muted: true},
	}, "somebody leaving and somebody arriving")

	// Each change asked for a push of the roster.
	require.NotEmpty(t, p.rosterCh)
}
