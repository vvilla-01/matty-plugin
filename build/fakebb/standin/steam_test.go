package standin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// steamServer is the stand-in on a port of its own, and a client that does
// not follow where a sign-in sends it.
func steamServer(t *testing.T, accounts ...string) (*Steam, *httptest.Server, *http.Client) {
	t.Helper()

	steam := NewSteam(accounts...)
	server := httptest.NewServer(steam)
	t.Cleanup(server.Close)

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	return steam, server, client
}

// signInAddress is where the plugin sends somebody to sign in, to come back
// to returnTo.
func signInAddress(server *httptest.Server, returnTo, realm string) string {
	return server.URL + "/openid/login?" + url.Values{
		"openid.ns":         {openIDNamespace},
		"openid.mode":       {"checkid_setup"},
		"openid.return_to":  {returnTo},
		"openid.realm":      {realm},
		"openid.identity":   {openIDSelect},
		"openid.claimed_id": {openIDSelect},
	}.Encode()
}

const (
	testReturn = "http://localhost:8065/plugins/ceo.villa.matty/api/v1/steam/callback?state=abc"
	testRealm  = "http://localhost:8065"
)

// signIn signs who in and returns what came back in the return address.
func signIn(t *testing.T, server *httptest.Server, client *http.Client, who string) url.Values {
	t.Helper()

	response, err := client.Get(signInAddress(server, testReturn, testRealm) + "&as=" + url.QueryEscape(who))
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusFound, response.StatusCode)

	back, err := url.Parse(response.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "localhost:8065", back.Host)
	require.Equal(t, "/plugins/ceo.villa.matty/api/v1/steam/callback", back.Path)

	return back.Query()
}

// checked is what check_authentication says of an assertion.
func checked(t *testing.T, server *httptest.Server, assertion url.Values) string {
	t.Helper()

	form := url.Values{}
	for name, values := range assertion {
		if strings.HasPrefix(name, "openid.") {
			form[name] = values
		}
	}
	form.Set("openid.mode", "check_authentication")

	response, err := http.PostForm(server.URL+"/openid/login", form)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)

	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)

	return string(body)
}

func TestSteamVouchesForWhoeverIsPicked(t *testing.T) {
	_, server, client := steamServer(t)

	back := signIn(t, server, client, "alice")

	// The state the plugin put in its return address comes back with it.
	require.Equal(t, "abc", back.Get("state"))

	identity := steamIdentityPrefix + SteamID("alice")
	require.Equal(t, openIDNamespace, back.Get("openid.ns"))
	require.Equal(t, "id_res", back.Get("openid.mode"))
	require.Equal(t, server.URL+"/openid/login", back.Get("openid.op_endpoint"), "the sign-in's own address, as it was reached")
	require.Equal(t, identity, back.Get("openid.claimed_id"))
	require.Equal(t, identity, back.Get("openid.identity"))
	require.Equal(t, testReturn, back.Get("openid.return_to"))
	require.Equal(t, openIDSigned, back.Get("openid.signed"))
	require.Regexp(t, `^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z.+`, back.Get("openid.response_nonce"))
	require.NotEmpty(t, back.Get("openid.sig"))

	// Exactly Steam's ten fields, each once.
	fields := 0
	for name, values := range back {
		if strings.HasPrefix(name, "openid.") {
			fields++
			require.Len(t, values, 1, name)
		}
	}
	require.Equal(t, 10, fields)
}

func TestSteamConfirmsOnlyWhatItSignedAndOnlyOnce(t *testing.T) {
	_, server, client := steamServer(t)

	assertion := signIn(t, server, client, "alice")
	require.Equal(t, "ns:"+openIDNamespace+"\nis_valid:true\n", checked(t, server, assertion))
	require.Equal(t, "ns:"+openIDNamespace+"\nis_valid:false\n", checked(t, server, assertion), "a nonce is good once, as with Steam")

	// Somebody else's account put into an assertion Steam signed for alice.
	moved := signIn(t, server, client, "alice")
	moved.Set("openid.claimed_id", steamIdentityPrefix+SteamID("bob"))
	moved.Set("openid.identity", steamIdentityPrefix+SteamID("bob"))
	require.Contains(t, checked(t, server, moved), "is_valid:false")

	// And one moved to somebody else's state.
	elsewhere := signIn(t, server, client, "alice")
	elsewhere.Set("openid.return_to", strings.Replace(testReturn, "state=abc", "state=def", 1))
	require.Contains(t, checked(t, server, elsewhere), "is_valid:false")

	// A signature nobody made.
	forged := signIn(t, server, client, "alice")
	forged.Set("openid.sig", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	require.Contains(t, checked(t, server, forged), "is_valid:false")
}

func TestSteamSignInRefusesWhatSteamWould(t *testing.T) {
	_, server, client := steamServer(t)

	get := func(address string) int {
		response, err := client.Get(address)
		require.NoError(t, err)
		defer response.Body.Close()

		return response.StatusCode
	}

	require.Equal(t, http.StatusBadRequest, get(signInAddress(server, "http://elsewhere.example/callback", testRealm)+"&as=alice"),
		"a return address outside the realm")
	require.Equal(t, http.StatusBadRequest, get(signInAddress(server, "http://localhost:8065.evil.example/callback", testRealm)+"&as=alice"))
	require.Equal(t, http.StatusBadRequest, get(server.URL+"/openid/login?openid.mode=checkid_setup"), "no namespace")
	require.Equal(t, http.StatusBadRequest, get(signInAddress(server, testReturn, testRealm)+"&as=Not%20A%20Name"))

	// A Site URL with a path under it is its own realm's.
	require.Equal(t, http.StatusFound, get(signInAddress(server, "https://chat.example.org/mm/plugins/x/callback?state=1", "https://chat.example.org")+"&as=alice"))
}

func TestSteamCancelComesBackWithNoAccount(t *testing.T) {
	_, server, client := steamServer(t)

	response, err := client.Get(signInAddress(server, testReturn, testRealm) + "&cancel=1")
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusFound, response.StatusCode)

	back, err := url.Parse(response.Header.Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "cancel", back.Query().Get("openid.mode"))
	require.Equal(t, "abc", back.Query().Get("state"))
	require.Empty(t, back.Query().Get("openid.claimed_id"))
}

func TestSteamPageOffersTheAccountsThereAre(t *testing.T) {
	_, server, client := steamServer(t, "alice=Hades", "bob")

	response, err := client.Get(signInAddress(server, testReturn, testRealm))
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Contains(t, response.Header.Get("Content-Type"), "text/html")

	raw, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	page := string(raw)

	require.Contains(t, page, "as=alice")
	require.Contains(t, page, "as=bob")
	require.Contains(t, page, "in Hades")
	require.Contains(t, page, "cancel=1")
	require.Contains(t, page, "This is not Steam")

	// A press with nothing typed shows the page again, and its links still work.
	again, err := client.Get(signInAddress(server, testReturn, testRealm) + "&as=")
	require.NoError(t, err)
	defer again.Body.Close()
	require.Equal(t, http.StatusOK, again.StatusCode)
	raw, err = io.ReadAll(again.Body)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "as=&")
}

// summariesOf asks GetPlayerSummaries about ids, with a key unless key is "".
func summariesOf(t *testing.T, server *httptest.Server, key string, ids ...string) (int, []map[string]any) {
	t.Helper()

	request, err := http.NewRequest(http.MethodGet, server.URL+"/ISteamUser/GetPlayerSummaries/v2/?steamids="+strings.Join(ids, ","), nil)
	require.NoError(t, err)
	if key != "" {
		request.Header.Set("x-webapi-key", key)
	}

	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return response.StatusCode, nil
	}

	var answer struct {
		Response struct {
			Players []map[string]any `json:"players"`
		} `json:"response"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&answer))

	return response.StatusCode, answer.Response.Players
}

func steer(t *testing.T, server *httptest.Server, query string) int {
	t.Helper()

	response, err := http.Post(server.URL+"/fake/steam?"+query, "", nil)
	require.NoError(t, err)
	defer response.Body.Close()

	return response.StatusCode
}

func TestSteamSummariesSayWhatSteamSays(t *testing.T) {
	steam, server, _ := steamServer(t, "alice=Hades", "bob")

	status, players := summariesOf(t, server, "any-key", SteamID("alice"), SteamID("bob"), SteamID("nobody"))
	require.Equal(t, http.StatusOK, status)
	require.Len(t, players, 2, "nothing about an account there is not")

	alice, bob := players[0], players[1]
	require.Equal(t, SteamID("alice"), alice["steamid"])
	require.Equal(t, "alice", alice["personaname"])
	require.EqualValues(t, 3, alice["communityvisibilitystate"])
	require.EqualValues(t, 1, alice["personastate"])
	require.Equal(t, "Hades", alice["gameextrainfo"])
	require.Equal(t, "1145360", alice["gameid"], "Steam's own id for a game it knows")

	require.NotContains(t, bob, "gameid")
	require.NotContains(t, bob, "gameextrainfo")

	// A profile that is not public says nothing of what its owner is doing.
	require.Equal(t, http.StatusOK, steer(t, server, "who=alice&private=true"))
	_, players = summariesOf(t, server, "any-key", SteamID("alice"))
	require.EqualValues(t, 1, players[0]["communityvisibilitystate"])
	require.EqualValues(t, 0, players[0]["personastate"])
	require.NotContains(t, players[0], "gameid")
	require.NotContains(t, players[0], "gameextrainfo")

	// No key is Steam's 403; and it answers what it is told to.
	status, _ = summariesOf(t, server, "", SteamID("bob"))
	require.Equal(t, http.StatusForbidden, status)

	require.Equal(t, http.StatusOK, steer(t, server, "api=429"))
	status, _ = summariesOf(t, server, "any-key", SteamID("bob"))
	require.Equal(t, http.StatusTooManyRequests, status)

	require.Equal(t, http.StatusOK, steer(t, server, "api=200"))
	status, _ = summariesOf(t, server, "any-key", SteamID("bob"))
	require.Equal(t, http.StatusOK, status)

	require.Equal(t, 5, steam.Calls(), "every call is counted, answered or not")
}

func TestSteamIsSteeredOneThingAtATime(t *testing.T) {
	steam, server, _ := steamServer(t, "bob")

	require.Equal(t, http.StatusOK, steer(t, server, "who=bob&game=Factorio"))
	bob, known := steam.Account("bob")
	require.True(t, known)
	require.Equal(t, "Factorio", bob.Game)
	require.Equal(t, "427520", bob.GameID)
	require.Equal(t, "online", bob.State, "what the address did not name stays")

	require.Equal(t, http.StatusOK, steer(t, server, "who=bob&state=away&name=Bob+on+Steam"))
	bob, _ = steam.Account("bob")
	require.Equal(t, "away", bob.State)
	require.Equal(t, "Bob on Steam", bob.Name)
	require.Equal(t, "Factorio", bob.Game)

	// A game Steam has an id for and no name.
	require.Equal(t, http.StatusOK, steer(t, server, "who=bob&game=&gameid=12345"))
	bob, _ = steam.Account("bob")
	require.Empty(t, bob.Game)
	require.Equal(t, "12345", bob.GameID)
	_, players := summariesOf(t, server, "any-key", SteamID("bob"))
	require.Equal(t, "12345", players[0]["gameid"])
	require.NotContains(t, players[0], "gameextrainfo")

	// A game nobody has heard of still has an id, the same one every time.
	require.Equal(t, http.StatusOK, steer(t, server, "who=bob&game=Made+Up+Quest"))
	bob, _ = steam.Account("bob")
	require.Equal(t, AppID("made up quest"), bob.GameID)
	require.Regexp(t, `^9[0-9]{9}$`, bob.GameID)

	// Out of the game.
	require.Equal(t, http.StatusOK, steer(t, server, "who=bob&game="))
	bob, _ = steam.Account("bob")
	require.Empty(t, bob.Game)
	require.Empty(t, bob.GameID)

	// Somebody new is made by being named.
	require.Equal(t, http.StatusOK, steer(t, server, "who=dave&game=Hades"))
	dave, known := steam.Account("dave")
	require.True(t, known)
	require.Equal(t, "dave", dave.Name)

	require.Equal(t, http.StatusBadRequest, steer(t, server, "who=bob&state=dancing"))
	require.Equal(t, http.StatusBadRequest, steer(t, server, "who=Not+A+Name"))
	require.Equal(t, http.StatusBadRequest, steer(t, server, "api=banana"))
}

func TestASteamIDIsTheSameForTheSameName(t *testing.T) {
	require.Regexp(t, `^76561[0-9]{12}$`, SteamID("alice"))
	require.Equal(t, SteamID("alice"), SteamID(" Alice "))
	require.NotEqual(t, SteamID("alice"), SteamID("bob"))
	require.Equal(t, "76561197960287930", SteamID("76561197960287930"), "a SteamID is its own")

	// The same account after a restart: a second stand-in knows it by the same id.
	first, second := NewSteam("alice"), NewSteam("alice")
	one, _ := first.Account("alice")
	two, _ := second.Account("alice")
	require.Equal(t, one.ID, two.ID)
}
