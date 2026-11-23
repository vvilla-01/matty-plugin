package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Matty's User-Agents, as the app sends them: a release, a debug build, and
// what a changed copy might say.
const (
	mattyOlder   = "Matty/0.1.31 (Android 17; Mattermost client)"
	mattyEqual   = "Matty/0.1.32 (Android 17; Mattermost client)"
	mattyNewer   = "Matty/0.1.40 (Android 17; Mattermost client)"
	mattyDebug   = "Matty/0.1.32-debug (Android 17; Mattermost client)"
	mattyOldDbg  = "Matty/0.1.31-debug (Android 17; Mattermost client)"
	mattyNoWord  = "Matty/ (Android 17; Mattermost client)"
	mattyGarbled = "Matty/latest (Android 17; Mattermost client)"
)

func (a *appCheckScene) setMinimum(minimum string) {
	cfg := a.p.getConfiguration().Clone()
	cfg.MinimumAppVersion = minimum
	a.p.setConfiguration(cfg)
}

func TestTheVersionIsReadFromMattysUserAgent(t *testing.T) {
	for agent, want := range map[string]string{
		mattyEqual:                    "0.1.32",
		mattyDebug:                    "0.1.32",
		"Matty/0.1.32+7 (Android 17)": "0.1.32",
		"Matty/1.0.0":                 "1.0.0",
		mattyNoWord:                   "",
		mattyGarbled:                  "",
		"Matty/0.1 (Android 17)":      "",
		"Matty/01.1.32 (Android 17)":  "",
		"Matty/v0.1.32 (Android 17)":  "",
		"matty/0.1.32":                "",
		browserAgent:                  "",
		"":                            "",
	} {
		require.Equal(t, want, mattyVersionOf(agent), agent)
	}
}

// Only N.N.N is a minimum: blank, or anything else, is none, and the card
// says so for anything else.
func TestTheMinimumSettingReadsAsThePageShowsIt(t *testing.T) {
	for value, want := range map[string]string{"": "", " 0.1.32 ": "0.1.32", "1.0.0": "1.0.0", "0.1": "", "v0.1.32": "", "0.1.32-debug": "", "0.1.032": "", "latest": ""} {
		require.Equal(t, want, (&configuration{MinimumAppVersion: value}).appMinimum(), value)
	}

	require.Empty(t, (&configuration{}).appMinimumProblem())
	require.Empty(t, (&configuration{MinimumAppVersion: "  "}).appMinimumProblem())
	require.Empty(t, (&configuration{MinimumAppVersion: "0.1.32"}).appMinimumProblem())
	require.Equal(t, "“0.1” in Minimum Matty version is not a version such as 0.1.32, so no minimum is in force.",
		(&configuration{MinimumAppVersion: " 0.1 "}).appMinimumProblem())
}

// Older, or unreadable, is refused on every route but the ones the app check
// leaves open; equal, newer and a debug build of either pass; whatever does
// not say it is Matty is never refused.
func TestAMattyOlderThanTheMinimumIsRefused(t *testing.T) {
	a := newAppCheckScene(t, appCheckReport)

	// No minimum: nothing is refused for its version.
	require.Equal(t, http.StatusOK, a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyOlder, nil).Code)
	require.NotContains(t, a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone), "minimum")

	a.setMinimum("0.1.32")
	for _, target := range []string{"/api/v1/streams", "/api/v1/boards", "/api/v1/accounts", "/api/v1/clips/thumb?id=x", "/api/v1/admin/appcheck", "/api/v1/nowhere"} {
		for agent, sentence := range map[string]string{
			mattyOlder:   "This server needs Matty 0.1.32 or newer. You have 0.1.31.",
			mattyOldDbg:  "This server needs Matty 0.1.32 or newer. You have 0.1.31.",
			mattyNoWord:  "This server needs Matty 0.1.32 or newer.",
			mattyGarbled: "This server needs Matty 0.1.32 or newer.",
		} {
			refused := a.call(http.MethodGet, target, sceneLinked, davesPhone, agent, nil)
			require.Equal(t, http.StatusForbidden, refused.Code, "%s %q", target, agent)
			require.Equal(t, "no-store", refused.Header().Get("Cache-Control"))
			require.Equal(t, errorResponse{Error: sentence, Code: codeAppOutdated}, refusalOf(t, refused), "%s %q", target, agent)
		}

		for _, agent := range []string{mattyEqual, mattyDebug, mattyNewer, "Matty/1.0.0", browserAgent, "", "matty/0.1.1", "Mattermost/2.0 Matty/0.1.1"} {
			require.NotEqual(t, codeAppOutdated, codedErrorOrEmpty(a.call(http.MethodGet, target, sceneLinked, davesPhone, agent, nil)), "%s %q", target, agent)
		}
	}
	require.Equal(t, http.StatusOK, a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyEqual, nil).Code)

	// Somebody signed out is told only that: never the minimum, whatever
	// Matty they say they are.
	for _, target := range []string{"/api/v1/streams", "/api/v1/access"} {
		for _, agent := range []string{mattyOlder, mattyNoWord} {
			signedOut := a.call(http.MethodGet, target, "", "", agent, nil)
			require.Equal(t, http.StatusUnauthorized, signedOut.Code, "%s %q", target, agent)
			require.Equal(t, codeSignedOut, refusalOf(t, signedOut).Code, "%s %q", target, agent)
			require.NotContains(t, signedOut.Body.String(), "0.1.32", "%s %q", target, agent)
		}
	}

	// Every route the app check leaves open still answers an old Matty.
	for _, route := range []struct{ method, target string }{
		{http.MethodGet, "/api/v1/access"},
		{http.MethodGet, "/api/v1/me"},
		{http.MethodGet, "/api/v1/release"},
		{http.MethodPost, appChallengePath},
		{http.MethodPost, appAttestPath},
		{http.MethodDelete, mePushPath},
		{http.MethodPost, voiceLeavePath},
	} {
		answer := a.call(route.method, route.target, sceneLinked, davesPhone, mattyOlder, nil)
		require.NotEqual(t, codeAppOutdated, codedErrorOrEmpty(answer), "%s %s", route.method, route.target)
	}
	require.Equal(t, http.StatusOK, a.call(http.MethodPost, appChallengePath, sceneLinked, davesPhone, mattyOlder, nil).Code)
	// The takebacks are open by method, as the app check's are: another
	// method on the same path is not.
	require.Equal(t, codeAppOutdated, codedErrorOrEmpty(a.call(http.MethodPut, mePushPath, sceneLinked, davesPhone, mattyOlder, nil)))

	// Part by part, not as text.
	a.setMinimum("0.1.10")
	require.Equal(t, codeAppOutdated, codedErrorOrEmpty(a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, "Matty/0.1.9", nil)))
	require.Equal(t, http.StatusOK, a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, "Matty/0.1.10", nil).Code)
	require.Equal(t, http.StatusOK, a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, "Matty/0.2.0", nil).Code)

	// A setting that is not N.N.N is none: nothing is refused, /access says
	// no minimum, and the card says why.
	a.setMinimum("0.1")
	require.Equal(t, http.StatusOK, a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyNoWord, nil).Code)
	require.NotContains(t, a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone), "minimum")
	var view appCheckAdminView
	card := a.call(http.MethodGet, adminAppCheck, sceneLockedAdmin, "", browserAgent, nil)
	require.Equal(t, http.StatusOK, card.Code, card.Body.String())
	require.NoError(t, json.Unmarshal(card.Body.Bytes(), &view))
	require.Empty(t, view.Minimum)
	require.Equal(t, "“0.1” in Minimum Matty version is not a version such as 0.1.32, so no minimum is in force.", view.Problem)

	// Beside a problem with the lists, both are said.
	cfg := a.p.getConfiguration().Clone()
	cfg.AppCheckCertificates = "x"
	a.p.setConfiguration(cfg)
	card = a.call(http.MethodGet, adminAppCheck, sceneLockedAdmin, "", browserAgent, nil)
	require.NoError(t, json.Unmarshal(card.Body.Bytes(), &view))
	require.Equal(t, "Not a SHA-256 digest, so left out: line 1 of More signing certificates. “0.1” in Minimum Matty version is not a version such as 0.1.32, so no minimum is in force.", view.Problem)

	a.setMinimum("0.1.32")
	card = a.call(http.MethodGet, adminAppCheck, sceneLockedAdmin, "", browserAgent, nil)
	require.NoError(t, json.Unmarshal(card.Body.Bytes(), &view))
	require.Equal(t, "0.1.32", view.Minimum)
	require.Equal(t, "Not a SHA-256 digest, so left out: line 1 of More signing certificates.", view.Problem)
}

// A Matty that is too old is told so before it is asked to prove itself:
// the minimum's gate comes before the app check's.
func TestTheMinimumComesBeforeTheAppCheck(t *testing.T) {
	a := newAppCheckScene(t, appCheckRequire)
	a.setMinimum("0.1.32")

	require.Equal(t, codeAppOutdated, codedErrorOrEmpty(a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyOlder, nil)))
	require.Equal(t, codeAppUnverified, codedErrorOrEmpty(a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyEqual, nil)))

	// Proved, a new enough Matty passes both, and an old one on the same
	// session is still too old.
	require.Equal(t, http.StatusOK, a.prove(t, sceneLinked, davesPhone, officialPhone, appSite).Code)
	require.Equal(t, http.StatusOK, a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyEqual, nil).Code)
	require.Equal(t, codeAppOutdated, codedErrorOrEmpty(a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyOlder, nil)))
}

// /access and /me carry the minimum for everybody, whatever the app check's
// setting, and only while one is set.
func TestAccessSaysTheMinimumToEverybody(t *testing.T) {
	a := newAppCheckScene(t, appCheckOff)
	a.setMinimum("0.1.32")

	for _, user := range []string{sceneLinked, sceneLocked, sceneLockedAdmin} {
		for _, path := range []string{"/api/v1/access", "/api/v1/me"} {
			require.Equal(t, map[string]any{"check": "off", "state": "none", "minimum": "0.1.32"}, a.appWordOf(t, path, user, davesPhone), "%s %s", user, path)
		}
	}

	a.setMode(appCheckReport)
	require.Equal(t, map[string]any{"check": "report", "state": "none", "minimum": "0.1.32"}, a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone))
	a.setMinimum("")
	require.Equal(t, map[string]any{"check": "report", "state": "none"}, a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone))
}

// Every open client is told when the minimum in force changes, as when the
// app check's setting does, so that Matty reads /access again.
func TestEveryOpenClientIsToldWhenTheMinimumChanges(t *testing.T) {
	const site = "http://localhost:8065"
	w, save := appCheckSaves(t)
	settings := func(minimum string) map[string]any {
		return map[string]any{"appcheck": "report", "minimumappversion": minimum}
	}

	setActivation(w.p, true)
	require.Zero(t, save(settings(""), site), "the first load")
	require.Equal(t, 1, save(settings("0.1.32"), site), "a minimum set")
	require.Zero(t, save(settings(" 0.1.32 "), site), "the same minimum written another way")
	require.Equal(t, 1, save(settings("0.1.33"), site), "another minimum")
	require.Equal(t, 1, save(settings("0.1"), site), "not a version, so none")
	require.Zero(t, save(settings(""), site), "none, as before")
}

// The contract's files for the minimum: /access with one set, and the
// refusal.
func TestContractAppMinimum(t *testing.T) {
	a := newAppCheckScene(t, appCheckReport)
	a.setMinimum("0.1.32")

	refused := a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyOlder, nil)
	require.Equal(t, http.StatusForbidden, refused.Code)
	contractFixture(t, "refusal-app-outdated.json", refused.Body.Bytes())

	access := a.call(http.MethodGet, "/api/v1/access", sceneLinked, davesPhone, mattyOlder, nil)
	require.Equal(t, http.StatusOK, access.Code)
	contractFixture(t, "access-app-minimum.json", access.Body.Bytes())
}

// minimumScene is phone sessions on a fake voice server, each joined from
// the User-Agent given.
type minimumScene struct {
	*talkScene
}

func (s minimumScene) joinFrom(userID, agent string) *fakeVoicePeer {
	s.t.Helper()

	ask := s.ask(userID, 1, false, false)
	ask.Matty, ask.Version = mattyOf(agent)
	peer := newFakeVoicePeer(4)
	_, err := s.r.join(context.Background(), ask, s.dial, func(*voiceSession) (voicePeer, error) { return peer, nil })
	require.NoError(s.t, err)

	return peer
}

// A voice session keeps the Matty its join said it was. When the minimum in
// force changes, every session from a Matty that does not meet it, older or
// with no version that can be read, ends with the refusal's sentence; one
// that meets it stays, and so does one from a client that does not say it is
// Matty; no minimum ends nobody.
func TestAMinimumEndsTheVoiceOfAMattyBelowIt(t *testing.T) {
	s := minimumScene{newTalkScene(t)}
	const dave = "uid-dave"

	alice := s.joinFrom(talkAlice, mattyOlder)
	bob := s.joinFrom(talkBob, mattyEqual)
	carol := s.joinFrom(talkCarol, browserAgent)
	daves := s.joinFrom(dave, mattyGarbled)

	s.r.endOutdated("")
	require.Equal(t, 4, s.r.count(), "no minimum ends nobody")

	s.r.endOutdated("0.1.32")
	require.Eventually(t, alice.isClosed, 5*time.Second, 5*time.Millisecond)
	require.Eventually(t, daves.isClosed, 5*time.Second, 5*time.Millisecond)
	end, _ := alice.ended()
	assert.Equal(t, voiceEndSignal{End: voiceEndOff, Reason: "This server needs Matty 0.1.32 or newer. You have 0.1.31."}, end)
	end, _ = daves.ended()
	assert.Equal(t, voiceEndSignal{End: voiceEndOff, Reason: "This server needs Matty 0.1.32 or newer."}, end)
	assert.False(t, bob.isClosed(), "meets the minimum")
	assert.False(t, carol.isClosed(), "does not say it is Matty")
	assert.Equal(t, 2, s.r.count())

	// The session keeps the newest join's word: Bob, joined from 0.1.32,
	// takes his session over from 0.1.40, and a higher minimum leaves him.
	s.joinFrom(talkBob, mattyNewer)
	require.Eventually(t, bob.isClosed, 5*time.Second, 5*time.Millisecond)
	end, _ = bob.ended()
	assert.Equal(t, voiceEndReplaced, end.End)
	s.r.endOutdated("0.1.33")
	assert.Equal(t, 2, s.r.count(), "0.1.40 meets 0.1.33")

	s.r.endOutdated("0.2.0")
	assert.Equal(t, 1, s.r.count(), "only the client that does not say it is Matty stays")
	assert.False(t, carol.isClosed())
}

// Through the plugin's own hook: a save that sets a minimum ends the voice
// of a Matty below it, and one that clears it ends nobody.
func TestSavingAMinimumEndsTheVoiceOfAMattyBelowIt(t *testing.T) {
	const site = "http://localhost:8065"
	w, save := appCheckSaves(t)
	s := minimumScene{newTalkScene(t)}
	w.p.voiceTalkOnce.Do(func() { w.p.voiceTalkState = s.r })
	settings := func(minimum string) map[string]any {
		return map[string]any{"appcheck": "report", "minimumappversion": minimum}
	}

	setActivation(w.p, true)
	save(settings(""), site)
	alice := s.joinFrom(talkAlice, mattyOlder)
	bob := s.joinFrom(talkBob, mattyEqual)

	save(settings("0.1.32"), site)
	require.Eventually(t, alice.isClosed, 5*time.Second, 5*time.Millisecond)
	end, _ := alice.ended()
	assert.Equal(t, voiceEndSignal{End: voiceEndOff, Reason: "This server needs Matty 0.1.32 or newer. You have 0.1.31."}, end)

	carol := s.joinFrom(talkCarol, mattyOlder)
	save(settings(""), site)
	save(settings(" "), site)
	time.Sleep(100 * time.Millisecond)
	assert.False(t, bob.isClosed())
	assert.False(t, carol.isClosed(), "clearing the minimum ends nobody")
	assert.Equal(t, 2, s.r.count())
}

// A join from a Matty below a minimum that is saved while the join's
// connection to the voice server is still being made: the hook's pass finds
// no session to end, and the join, once the session is its own, reads the
// minimum in force, ends the session as the hook's pass ends one, and is
// refused as the gate refuses. Nobody is left in voice. A Matty that meets
// the minimum, and a client that does not say it is Matty, join as before.
func TestAMinimumSavedDuringAJoinEndsIt(t *testing.T) {
	const site = "http://localhost:8065"
	w, save := appCheckSaves(t)
	s := minimumScene{newTalkScene(t)}
	s.r.minimum = w.p.appMinimumInForce
	w.p.voiceTalkOnce.Do(func() { w.p.voiceTalkState = s.r })
	settings := func(minimum string) map[string]any {
		return map[string]any{"appcheck": "report", "minimumappversion": minimum}
	}

	setActivation(w.p, true)
	save(settings(""), site)

	dialing, release := make(chan struct{}, 1), make(chan struct{})
	dial := s.r.dial
	s.r.dial = func(ctx context.Context, d voiceMumbleDial, h voiceMumbleHandler, safely func(string, func()) bool) (*voiceMumble, error) {
		dialing <- struct{}{}
		<-release

		return dial(ctx, d, h, safely)
	}

	type result struct {
		peer *fakeVoicePeer
		err  error
	}
	joinFrom := func(userID, agent string) <-chan result {
		done := make(chan result, 1)
		ask := s.ask(userID, 1, false, false)
		ask.Matty, ask.Version = mattyOf(agent)
		go func() {
			peer := newFakeVoicePeer(4)
			_, err := s.r.join(context.Background(), ask, s.dial, func(*voiceSession) (voicePeer, error) { return peer, nil })
			done <- result{peer, err}
		}()
		<-dialing

		return done
	}

	alice := joinFrom(talkAlice, mattyOlder)
	save(settings("0.1.32"), site)
	time.Sleep(100 * time.Millisecond)
	close(release)

	joined := <-alice
	assert.Equal(t, &voiceRefusal{Status: http.StatusForbidden, Code: codeAppOutdated, Sentence: "This server needs Matty 0.1.32 or newer. You have 0.1.31."}, refusal(t, joined.err))
	require.Eventually(t, joined.peer.isClosed, 5*time.Second, 5*time.Millisecond)
	end, _ := joined.peer.ended()
	assert.Equal(t, voiceEndSignal{End: voiceEndOff, Reason: "This server needs Matty 0.1.32 or newer. You have 0.1.31."}, end)
	assert.Zero(t, s.r.count(), "nobody is left in voice")
	assert.Zero(t, s.phoneCount())

	// The dial is let go from here on.
	bob := joinFrom(talkBob, mattyEqual)
	carol := joinFrom(talkCarol, browserAgent)
	require.NoError(t, (<-bob).err, "meets the minimum")
	require.NoError(t, (<-carol).err, "does not say it is Matty")
	assert.Equal(t, 2, s.r.count())
}

// Through the route and the hook, as Mattermost runs them: a join through
// POST /api/v1/voice/join keeps the Matty word of its User-Agent, and a
// minimum of 0.1.32 saved afterwards ends the voice of the Matty 0.1.31 that
// joined, with the refusal's sentence; a browser's join stays.
func TestAMinimumEndsTheVoiceOfAMattyThatJoinedThroughTheRoute(t *testing.T) {
	s := newVoiceScene(t)

	// The settings as saved: every one as the scene has it, and the
	// minimum.
	minimum := ""
	stored := func() map[string]any {
		cfg := s.p.getConfiguration().Clone()
		cfg.MinimumAppVersion = minimum
		encoded, err := json.Marshal(cfg)
		require.NoError(t, err)
		var fields map[string]any
		require.NoError(t, json.Unmarshal(encoded, &fields))
		saved := map[string]any{}
		for key, value := range fields {
			saved[strings.ToLower(key)] = value
		}

		return saved
	}
	s.api.On("GetUnsanitizedConfig").Return(func() *model.Config {
		return &model.Config{PluginSettings: model.PluginSettings{Plugins: map[string]map[string]any{manifestID: stored()}}}
	}).Maybe()
	s.api.On("LoadPluginConfiguration", mock.Anything).Run(func(args mock.Arguments) {
		encoded, err := json.Marshal(stored())
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(encoded, args.Get(0)))
	}).Return(nil).Maybe()
	s.api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
	setActivation(s.p, true)

	joinFrom := func(phone *testVoicePhone, userID, agent string) voiceJoinResponse {
		t.Helper()

		body, err := json.Marshal(map[string]any{"channel": 1, "muted": false, "deafened": false, "sdp": phone.offer()})
		require.NoError(t, err)
		recorder := s.serve(http.MethodPost, "/api/v1/voice/join", userID, string(body), map[string]string{"X-Requested-With": "XMLHttpRequest", "User-Agent": agent})
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		var answer voiceJoinResponse
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &answer))
		phone.accept(answer.SDP)

		return answer
	}

	dave := newTestVoicePhone(t, 4)
	joinFrom(dave, sceneLinked, mattyOlder)
	erin := newTestVoicePhone(t, 4)
	joinFrom(erin, sceneLetIn, browserAgent)
	require.Eventually(t, func() bool { return dave.self().Channel == 1 && erin.self().Channel == 1 }, 5*time.Second, 5*time.Millisecond)

	minimum = "0.1.32"
	require.NoError(t, s.p.OnConfigurationChange())
	require.Eventually(t, func() bool { _, ended := dave.ended(); return ended }, 5*time.Second, 5*time.Millisecond)
	end, _ := dave.ended()
	assert.Equal(t, voiceEndSignal{End: voiceEndOff, Reason: "This server needs Matty 0.1.32 or newer. You have 0.1.31."}, end)

	time.Sleep(200 * time.Millisecond)
	_, ended := erin.ended()
	assert.False(t, ended, "a browser's join stays")
	assert.Equal(t, 1, s.p.voiceTalk().count())

	// And a join of that Matty now is refused before it reaches the voice
	// server, as the gate refuses it.
	refused := s.serve(http.MethodPost, "/api/v1/voice/join", sceneLinked, `{"channel":1}`, map[string]string{"X-Requested-With": "XMLHttpRequest", "User-Agent": mattyOlder})
	requireRefused(t, refused, http.StatusForbidden, codeAppOutdated, "a join below the minimum")
	assert.Equal(t, "no-store", refused.Header().Get("Cache-Control"))
}

// The app check card's line about its own setting speaks of the check
// alone: Minimum Matty version refuses on its own, and its line is the one
// beside it.
func TestTheAppCheckCardSaysOnlyWhatTheCheckRefuses(t *testing.T) {
	source, err := os.ReadFile("../webapp/src/components/AdminAppCheck.tsx")
	require.NoError(t, err)
	require.False(t, strings.Contains(strings.ToLower(string(source)), "nobody is refused"), "the card says nobody is refused")
	require.Contains(t, string(source), "'The check is off: no Matty is asked to prove itself.'")
	require.Contains(t, string(source), "'Each Matty proves itself when it opens, and what it found is shown to that person and here. The check refuses nobody.'")

	// The minimum's line says what refuses an old Matty: this plugin's API,
	// not chat or Mattermost itself.
	require.Contains(t, string(source), "is refused by this plugin's API everywhere but the few places it needs to show why.`")
	require.NotContains(t, string(source), "is refused everywhere but the few places")
}
