package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/vvilla-01/matty-plugin/server/appcheck"
	"github.com/vvilla-01/matty-plugin/server/appcheck/appchecktest"
	"github.com/vvilla-01/matty-plugin/server/release"
)

// With the app check at "refuse", the minimum goes by the version a Matty
// said when Android vouched for it (app_check.go): what is kept with a
// verdict, the gate's third step, what /access says, a node's memory, voice
// and the admins' card.

// The third step's sentences, under a minimum of 0.1.32.
const (
	provedOlderText  = "Matty answers only the official Matty app, and this copy has to be checked again: this server needs Matty 0.1.32 or newer, and this phone last proved Matty 0.1.31."
	provedNothingTxt = "Matty answers only the official Matty app, and this copy has to be checked again: this server needs Matty 0.1.32 or newer."
)

// proveAs is Matty proving itself as phone, its request saying agent.
func (a *appCheckScene) proveAs(t *testing.T, userID, sessionID string, phone appchecktest.Phone, agent string) *httptest.ResponseRecorder {
	t.Helper()

	challenge := a.challenge(t, userID, sessionID)
	chain := a.root.Chain(phone, appcheck.DeriveChallenge(appSite, userID, challenge))

	return a.call(http.MethodPost, appAttestPath, userID, sessionID, agent, attestBody(challenge, appSite, chain))
}

func (a *appCheckScene) keptResult(t *testing.T, sessionID string) appCheckResult {
	t.Helper()

	var kept appCheckResult
	require.NoError(t, json.Unmarshal(a.kv(kvAppCheckResultPrefix+sessionID), &kept))

	return kept
}

// provenPass is a pass as handleAppAttest keeps one, for userID, the app
// having said said.
func provenPass(userID, said string, now time.Time) appCheckResult {
	return appCheckResult{
		User: userID, Verified: true, At: now.UnixMilli(), Expires: now.Add(time.Hour).UnixMilli(),
		Package: mattyPackage, Digest: release.OfficialRSASHA256, RevocationList: appcheck.RevocationListDate,
		Said: said, SaidKept: true,
	}
}

// onNode is a request to another node's plugin, with a session.
func onNode(p *Plugin, method, target, userID, sessionID, agent string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("Mattermost-User-Id", userID)
	request.Header.Set("User-Agent", agent)
	request.Header.Set("X-Requested-With", "XMLHttpRequest")
	recorder := httptest.NewRecorder()
	p.ServeHTTP(&plugin.Context{SessionId: sessionID}, recorder, request)

	return recorder
}

// A verdict, pass or fail, keeps the version the attest request's
// User-Agent said, "" when none could be read, and the mark that it keeps
// one; a pass with no readable version is not stale for that.
func TestTheVerdictKeepsTheVersionTheAppSaid(t *testing.T) {
	a := newAppCheckScene(t, appCheckReport)

	require.Equal(t, http.StatusOK, a.proveAs(t, sceneLinked, davesPhone, officialPhone, mattyEqual).Code)
	kept := a.keptResult(t, davesPhone)
	require.True(t, kept.Verified)
	require.Equal(t, "0.1.32", kept.Said)
	require.True(t, kept.SaidKept)

	require.Equal(t, http.StatusOK, a.proveAs(t, sceneLinked, davesPhone, officialPhone, mattyDebug).Code)
	require.Equal(t, "0.1.32", a.keptResult(t, davesPhone).Said, "without its ending")

	require.Equal(t, http.StatusOK, a.proveAs(t, sceneLinked, davesLaptop, officialPhone, mattyNoWord).Code)
	kept = a.keptResult(t, davesLaptop)
	require.Empty(t, kept.Said)
	require.True(t, kept.SaidKept, "a verdict with no readable version still carries the mark")
	require.Equal(t, map[string]any{"check": "report", "state": "verified", "versionCode": float64(28)}, a.appWordOf(t, "/api/v1/access", sceneLinked, davesLaptop))

	unlocked := officialPhone
	unlocked.Locked, unlocked.BootState = false, 2
	require.Equal(t, http.StatusOK, a.proveAs(t, sceneLocked, dansPhone, unlocked, mattyOlder).Code)
	kept = a.keptResult(t, dansPhone)
	require.False(t, kept.Verified)
	require.Equal(t, "0.1.31", kept.Said)
	require.True(t, kept.SaidKept)
}

// A pass kept without the mark is stale, at every setting of the check: /access
// says none with the rules' reason, the gate asks for a new check, and the
// card says why.
func TestPassesFromBeforeAreStale(t *testing.T) {
	a := newAppCheckScene(t, appCheckRequire)
	now := a.p.appCheck().now()

	before := provenPass(sceneLinked, "", now)
	before.SaidKept = false
	require.NoError(t, a.p.keepResult(davesPhone, &before, now))
	require.Equal(t, map[string]any{"check": "require", "state": "none", "reason": appCheckRulesChangedText}, a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone))
	refused := a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyEqual, nil)
	require.Equal(t, errorResponse{Error: "Matty answers only the official Matty app, and this copy has to be checked again: this server's rules for the check have changed.", Code: codeAppUnverified}, refusalOf(t, refused))

	a.setMode(appCheckReport)
	require.Equal(t, map[string]any{"check": "report", "state": "none", "reason": appCheckRulesChangedText}, a.appWordOf(t, "/api/v1/me", sceneLinked, davesPhone))
	session := a.adminCard(t).People[0].Sessions[0]
	require.True(t, session.Stale)
	require.False(t, session.Verified)
	require.Equal(t, staleUnprovenAppCheckReason, session.Reason)

	// The same pass with the mark, and no version read, counts.
	marked := provenPass(sceneLinked, "", now)
	require.NoError(t, a.p.keepResult(davesPhone, &marked, now))
	require.Equal(t, "verified", a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone)["state"])
	require.False(t, a.adminCard(t).People[0].Sessions[0].Stale)
}

// The third step: under "refuse" and a minimum, a session that passed with
// a version older than the minimum, or none, is refused, after the
// minimum's own step and the check's second; a session proved at or above
// the minimum passes whatever its User-Agent says now, as long as the first
// step lets it through. The exempt routes stay open, and it says nothing at
// "report", at "off" or without a minimum.
func TestAPassBelowTheMinimumIsCheckedAgain(t *testing.T) {
	a := newAppCheckScene(t, appCheckRequire)
	a.setMinimum("0.1.32")
	streams := func(agent string) *httptest.ResponseRecorder {
		return a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, agent, nil)
	}

	// Never proved: the second step, as before.
	require.Equal(t, "Matty answers only the official Matty app, and this copy has not been checked yet.", refusalOf(t, streams(mattyEqual)).Error)

	// Proved as 0.1.31, now saying 0.1.32 or 0.1.40.
	require.Equal(t, http.StatusOK, a.proveAs(t, sceneLinked, davesPhone, officialPhone, mattyOlder).Code)
	for _, agent := range []string{mattyEqual, mattyNewer, mattyDebug} {
		refused := streams(agent)
		require.Equal(t, http.StatusForbidden, refused.Code, agent)
		require.Equal(t, "no-store", refused.Header().Get("Cache-Control"))
		require.Equal(t, errorResponse{Error: provedOlderText, Code: codeAppUnverified}, refusalOf(t, refused), agent)
	}
	for _, target := range []string{"/api/v1/boards", "/api/v1/clips/thumb?id=x", "/api/v1/admin/appcheck", "/api/v1/nowhere"} {
		require.Equal(t, errorResponse{Error: provedOlderText, Code: codeAppUnverified}, refusalOf(t, a.call(http.MethodGet, target, sceneLinked, davesPhone, mattyEqual, nil)), target)
	}

	// The first step comes first.
	require.Equal(t, codeAppOutdated, codedErrorOrEmpty(streams(mattyOlder)))

	// Whatever does not say it is Matty is not asked.
	require.Equal(t, http.StatusOK, streams(browserAgent).Code)

	// The exempt routes.
	for _, route := range []struct{ method, target string }{
		{http.MethodGet, "/api/v1/access"},
		{http.MethodGet, "/api/v1/me"},
		{http.MethodGet, "/api/v1/release"},
		{http.MethodPost, appChallengePath},
		{http.MethodPost, appAttestPath},
		{http.MethodDelete, mePushPath},
		{http.MethodPost, voiceLeavePath},
	} {
		answer := a.call(route.method, route.target, sceneLinked, davesPhone, mattyEqual, nil)
		require.NotEqual(t, codeAppUnverified, codedErrorOrEmpty(answer), "%s %s", route.method, route.target)
	}

	// Silent at "report", at "off", and without a minimum.
	a.setMode(appCheckReport)
	require.Equal(t, http.StatusOK, streams(mattyEqual).Code)
	a.setMode(appCheckOff)
	require.Equal(t, http.StatusOK, streams(mattyEqual).Code)
	a.setMode(appCheckRequire)
	a.setMinimum("")
	require.Equal(t, http.StatusOK, streams(mattyEqual).Code)
	a.setMinimum("0.1.32")

	// Proved with no version read: the sentence without its last clause.
	require.Equal(t, http.StatusOK, a.proveAs(t, sceneLinked, davesPhone, officialPhone, mattyNoWord).Code)
	require.Equal(t, errorResponse{Error: provedNothingTxt, Code: codeAppUnverified}, refusalOf(t, streams(mattyEqual)))

	// Failed, below the minimum: the second step's sentence, not the third's.
	unlocked := officialPhone
	unlocked.Locked, unlocked.BootState = false, 2
	require.Equal(t, http.StatusOK, a.proveAs(t, sceneLinked, davesPhone, unlocked, mattyOlder).Code)
	require.True(t, strings.HasSuffix(refusalOf(t, streams(mattyEqual)).Error, "did not pass the check. This phone's bootloader is unlocked."))

	// Proved at the minimum, or above it: through, whatever it says now.
	require.Equal(t, http.StatusOK, a.proveAs(t, sceneLinked, davesPhone, officialPhone, mattyEqual).Code)
	require.Equal(t, http.StatusOK, streams(mattyEqual).Code)
	require.Equal(t, http.StatusOK, streams(mattyNewer).Code)
	require.Equal(t, http.StatusOK, a.proveAs(t, sceneLinked, davesPhone, officialPhone, mattyNewer).Code)
	require.Equal(t, http.StatusOK, streams(mattyEqual).Code)
	require.Equal(t, codeAppOutdated, codedErrorOrEmpty(streams(mattyOlder)))

	// The latest release as the minimum, known: the same.
	a.setMinimum("latest")
	a.keepLatest(t, appLatestSeen{Version: "0.1.41", LookedAt: 1})
	require.Equal(t, "Matty answers only the official Matty app, and this copy has to be checked again: this server needs Matty 0.1.41 or newer, and this phone last proved Matty 0.1.40.",
		refusalOf(t, streams("Matty/0.1.41 (Android 17; Mattermost client)")).Error)
}

// While the third step would refuse a session, /access and /me say none,
// no versionCode, and its sentence, to a Matty whose User-Agent meets the
// minimum; to one below it, and to anything not saying it is Matty, they say
// what they say without the third step.
func TestAccessSaysNoneWhileTheThirdStepRefuses(t *testing.T) {
	a := newAppCheckScene(t, appCheckRequire)
	a.setMinimum("0.1.32")
	require.Equal(t, http.StatusOK, a.proveAs(t, sceneLinked, davesPhone, officialPhone, mattyOlder).Code)

	word := func(path, agent string) map[string]any {
		t.Helper()
		response := a.call(http.MethodGet, path, sceneLinked, davesPhone, agent, nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())

		return decoded(t, response)["app"].(map[string]any)
	}

	refusing := map[string]any{"check": "require", "state": "none", "reason": provedOlderText, "minimum": "0.1.32"}
	verified := map[string]any{"check": "require", "state": "verified", "versionCode": float64(28), "minimum": "0.1.32"}
	for _, path := range []string{"/api/v1/access", "/api/v1/me"} {
		require.Equal(t, refusing, word(path, mattyEqual), path)
		require.Equal(t, refusing, word(path, mattyNewer), path)
		require.Equal(t, verified, word(path, mattyOlder), "%s: below the minimum, as without the third step", path)
		require.Equal(t, verified, word(path, mattyNoWord), path)
		require.Equal(t, verified, word(path, browserAgent), path)
	}

	a.setMode(appCheckReport)
	require.Equal(t, "verified", word("/api/v1/access", mattyEqual)["state"])

	a.setMode(appCheckRequire)
	require.Equal(t, http.StatusOK, a.proveAs(t, sceneLinked, davesPhone, officialPhone, mattyEqual).Code)
	require.Equal(t, verified, word("/api/v1/access", mattyEqual))
}

// A node remembers a pass for a minute only while it lets the gate through.
// A remembered pass the third step would refuse is read again from the
// store before the refusal is sent, and is remembered for five seconds, so a
// proof that lands on one node is taken on another at once.
func TestAProofOnOneNodeLetsTheNextRequestThroughOnAnother(t *testing.T) {
	a := newAppCheckScene(t, appCheckRequire)
	twin := newAccessSceneOn(t, a.store, a.faults)
	twin.p.appCheck().now = a.p.appCheck().now
	setBoth := func(minimum string) {
		for _, p := range []*Plugin{a.p, twin.p} {
			cfg := p.getConfiguration().Clone()
			cfg.AppCheck, cfg.MinimumAppVersion = appCheckRequire, minimum
			p.setConfiguration(cfg)
		}
	}

	// Proved as 0.1.31 while that is the minimum: the twin remembers a pass
	// that lets it through.
	setBoth("0.1.31")
	require.Equal(t, http.StatusOK, a.proveAs(t, sceneLinked, davesPhone, officialPhone, mattyOlder).Code)
	require.Equal(t, http.StatusOK, onNode(twin.p, http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyOlder).Code)

	// The minimum rises and the phone, updated, proves itself on the first
	// node: the twin's next request goes through at once.
	setBoth("0.1.32")
	require.Equal(t, provedOlderText, refusalOf(t, onNode(twin.p, http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyEqual)).Error)
	require.Equal(t, http.StatusOK, a.proveAs(t, sceneLinked, davesPhone, officialPhone, mattyEqual).Code)
	require.Equal(t, http.StatusOK, onNode(twin.p, http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyEqual).Code)

	// A pass the third step refuses is remembered for a few seconds only,
	// and one it lets through for a minute; /access on the twin says what
	// the store holds as soon as the phone has proved itself again.
	rememberedFor := func(sessionID string) time.Duration {
		memory := twin.p.appCheck()
		memory.lock.Lock()
		defer memory.lock.Unlock()

		return memory.remembered[sessionID].until.Sub(memory.now())
	}
	require.Equal(t, time.Minute, rememberedFor(davesPhone))
	require.Equal(t, http.StatusOK, a.proveAs(t, sceneLinked, davesLaptop, officialPhone, mattyOlder).Code)
	access := func() map[string]any {
		return decoded(t, onNode(twin.p, http.MethodGet, "/api/v1/access", sceneLinked, davesLaptop, mattyEqual))["app"].(map[string]any)
	}
	require.Equal(t, provedOlderText, access()["reason"])
	require.Equal(t, 5*time.Second, rememberedFor(davesLaptop))
	require.Equal(t, http.StatusOK, a.proveAs(t, sceneLinked, davesLaptop, officialPhone, mattyEqual).Code)
	require.Equal(t, "verified", access()["state"])
	require.Equal(t, time.Minute, rememberedFor(davesLaptop))
}

// voiceProvenScene is talk sessions beside an app check scene's store, each
// joined from its own Mattermost session.
type voiceProvenScene struct {
	minimumScene
	a *appCheckScene
}

func newVoiceProvenScene(t *testing.T) voiceProvenScene {
	a := newAppCheckScene(t, appCheckRequire)
	s := voiceProvenScene{minimumScene{newTalkScene(t)}, a}
	s.r.minimum = a.p.appMinimumInForce
	s.r.provenBelow = a.p.voiceProvenBelow

	return s
}

func (s voiceProvenScene) join(userID, agent string) (*fakeVoicePeer, error) {
	ask := s.ask(userID, 1, false, false)
	ask.Matty, ask.Version = mattyOf(agent)
	peer := newFakeVoicePeer(4)
	_, err := s.r.join(context.Background(), ask, s.dial, func(*voiceSession) (voicePeer, error) { return peer, nil })

	return peer, err
}

func (s voiceProvenScene) keep(t *testing.T, userID string, result appCheckResult) {
	t.Helper()
	require.NoError(t, s.a.p.keepResult("session-"+userID, &result, s.a.p.appCheck().now()))
}

// endOutdated under "refuse" also ends the voice of a Matty whose
// Mattermost session holds a pass kept with an older version, with the
// third step's sentence. A verdict that cannot be read, a stale one or none
// ends nobody, nor does anything at "report"; and a join still connecting
// when the minimum rises is ended and refused the same way.
func TestTheMinimumEndsTheVoiceOfAPhoneThatProvedLess(t *testing.T) {
	s := newVoiceProvenScene(t)
	now := s.a.p.appCheck().now()
	const dave, erin = "uid-dave", "uid-erin"

	s.keep(t, talkAlice, provenPass(talkAlice, "0.1.31", now))
	s.keep(t, talkBob, provenPass(talkBob, "0.1.32", now))
	before := provenPass(talkCarol, "0.1.31", now)
	before.SaidKept = false
	s.keep(t, talkCarol, before)
	s.keep(t, erin, provenPass(erin, "0.1.31", now))

	alice, err := s.join(talkAlice, mattyEqual)
	require.NoError(t, err)
	bob, err := s.join(talkBob, mattyEqual)
	require.NoError(t, err)
	carol, err := s.join(talkCarol, mattyEqual)
	require.NoError(t, err)
	daves, err := s.join(dave, mattyEqual)
	require.NoError(t, err)
	erins, err := s.join(erin, mattyEqual)
	require.NoError(t, err)

	// At "report", the minimum goes by the word alone.
	s.a.setMode(appCheckReport)
	s.r.endOutdated("0.1.32")
	assert.Equal(t, 5, s.r.count())

	s.a.setMode(appCheckRequire)
	s.a.faults.failRead(kvAppCheckResultPrefix + "session-" + erin)
	s.r.endOutdated("0.1.32")
	require.Eventually(t, alice.isClosed, 5*time.Second, 5*time.Millisecond)
	end, _ := alice.ended()
	assert.Equal(t, voiceEndSignal{End: voiceEndOff, Reason: provedOlderText}, end)
	for name, peer := range map[string]*fakeVoicePeer{"proved 0.1.32": bob, "stale": carol, "never proved": daves, "unread": erins} {
		assert.False(t, peer.isClosed(), name)
	}
	assert.Equal(t, 4, s.r.count())

	// A join whose connection is made while the minimum is in force, from a
	// session that proved less: ended, and refused as the gate refuses.
	s.a.setMinimum("0.1.32")
	_, err = s.join(talkAlice, mattyEqual)
	assert.Equal(t, &voiceRefusal{Status: http.StatusForbidden, Code: codeAppUnverified, Sentence: provedOlderText}, refusal(t, err))
	assert.Equal(t, 4, s.r.count())
}

// Through the route, as Mattermost runs it: under "refuse" and a minimum, a
// join from a session that proved less is refused before it reaches the
// voice server; one that proved enough joins, and a save that raises the
// minimum above what it proved ends it with the third step's sentence.
func TestAVoiceJoinThroughTheRouteGoesByWhatThePhoneProved(t *testing.T) {
	s := newVoiceScene(t)
	minimum := "0.1.32"
	stored := func() map[string]any {
		cfg := s.p.getConfiguration().Clone()
		cfg.AppCheck, cfg.MinimumAppVersion = appCheckRequire, minimum
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
	s.api.On("GetSession", mock.Anything).Return(&model.Session{Id: davesPhone, UserId: sceneLinked, ExpiresAt: s.now.Add(sessionLength).UnixMilli()}, nil).Maybe()
	setActivation(s.p, true)
	require.NoError(t, s.p.OnConfigurationChange())
	s.p.appCheck().now = func() time.Time {
		s.lock.Lock()
		defer s.lock.Unlock()

		return s.now
	}
	now := s.p.appCheck().now()

	join := func(phone *testVoicePhone) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(map[string]any{"channel": 1, "muted": false, "deafened": false, "sdp": phone.offer()})
		require.NoError(t, err)
		request := httptest.NewRequest(http.MethodPost, "/api/v1/voice/join", strings.NewReader(string(body)))
		request.Header.Set("Mattermost-User-Id", sceneLinked)
		request.Header.Set("User-Agent", mattyNewer)
		request.Header.Set("X-Requested-With", "XMLHttpRequest")
		recorder := httptest.NewRecorder()
		s.p.ServeHTTP(&plugin.Context{SessionId: davesPhone}, recorder, request)

		return recorder
	}

	older := provenPass(sceneLinked, "0.1.31", now)
	require.NoError(t, s.p.keepResult(davesPhone, &older, now))
	refused := join(newTestVoicePhone(t, 4))
	requireRefused(t, refused, http.StatusForbidden, codeAppUnverified, "a join from a phone that proved less")
	assert.Equal(t, provedOlderText, refusalOf(t, refused).Error)
	assert.Equal(t, "no-store", refused.Header().Get("Cache-Control"))
	assert.Zero(t, s.p.voiceTalk().count())

	enough := provenPass(sceneLinked, "0.1.32", now)
	require.NoError(t, s.p.keepResult(davesPhone, &enough, now))
	dave := newTestVoicePhone(t, 4)
	joined := join(dave)
	require.Equal(t, http.StatusOK, joined.Code, joined.Body.String())
	var answer voiceJoinResponse
	require.NoError(t, json.Unmarshal(joined.Body.Bytes(), &answer))
	dave.accept(answer.SDP)
	require.Eventually(t, func() bool { return dave.self().Channel == 1 }, 5*time.Second, 5*time.Millisecond)

	minimum = "0.1.33"
	require.NoError(t, s.p.OnConfigurationChange())
	require.Eventually(t, func() bool { _, ended := dave.ended(); return ended }, 5*time.Second, 5*time.Millisecond)
	end, _ := dave.ended()
	assert.Equal(t, voiceEndSignal{End: voiceEndOff, Reason: "Matty answers only the official Matty app, and this copy has to be checked again: this server needs Matty 0.1.33 or newer, and this phone last proved Matty 0.1.32."}, end)
}

// The card says whether the minimum goes by each Matty's word (a minimum in
// force and the check not at "refuse"), and each session's row the version
// kept with its verdict; never the mark.
func TestTheCardSaysByWhatTheMinimumGoes(t *testing.T) {
	a := newAppCheckScene(t, appCheckReport)
	require.Equal(t, http.StatusOK, a.proveAs(t, sceneLinked, davesPhone, officialPhone, mattyEqual).Code)

	require.False(t, a.adminCard(t).ByWord, "no minimum")
	a.setMinimum("0.1.32")
	require.True(t, a.adminCard(t).ByWord)
	a.setMode(appCheckOff)
	require.True(t, a.adminCard(t).ByWord)
	a.setMode(appCheckRequire)
	require.False(t, a.adminCard(t).ByWord)

	a.setMode(appCheckReport)
	a.setMinimum("latest")
	require.False(t, a.adminCard(t).ByWord, "the latest, not known: no minimum in force")
	a.keepLatest(t, appLatestSeen{Version: "0.1.32", LookedAt: 1})
	require.True(t, a.adminCard(t).ByWord)

	view := a.adminCard(t)
	require.Equal(t, "0.1.32", view.People[0].Sessions[0].Said)
	raw := a.call(http.MethodGet, adminAppCheck, sceneLockedAdmin, "", browserAgent, nil).Body.String()
	require.Contains(t, raw, `"byWord":true`)
	require.Contains(t, raw, `"said":"0.1.32"`)
	require.NotContains(t, raw, "saidKept")
}

// keepFor keeps a verdict for one Mattermost session.
func (s voiceProvenScene) keepFor(t *testing.T, sessionID string, result appCheckResult) {
	t.Helper()
	require.NoError(t, s.a.p.keepResult(sessionID, &result, s.a.p.appCheck().now()))
}

// joinAs is a join by userID from one of the person's Mattermost sessions.
func (s voiceProvenScene) joinAs(userID, sessionID, agent string) (*fakeVoicePeer, error) {
	ask := s.ask(userID, 1, false, false)
	ask.SessionID = sessionID
	ask.Matty, ask.Version = mattyOf(agent)
	peer := newFakeVoicePeer(4)
	_, err := s.r.join(context.Background(), ask, s.dial, func(*voiceSession) (voicePeer, error) { return peer, nil })

	return peer, err
}

// A voice session taken over by a join from another Mattermost session of
// the same person goes by the verdict of the session that holds it now:
// a rise of the minimum keeps a phone that proved it, and ends one that
// proved less with the third step's sentence naming that phone's version.
func TestAVoiceTakeoverGoesByThePhoneThatHoldsIt(t *testing.T) {
	const phoneOne, phoneTwo = "phone1-bob", "phone2-bob"

	t.Run("the phone that took over proved the minimum", func(t *testing.T) {
		s := newVoiceProvenScene(t)
		now := s.a.p.appCheck().now()
		s.keepFor(t, phoneOne, provenPass(talkBob, "0.1.31", now))
		s.keepFor(t, phoneTwo, provenPass(talkBob, "0.1.32", now))

		first, err := s.joinAs(talkBob, phoneOne, mattyOlder)
		require.NoError(t, err)
		second, err := s.joinAs(talkBob, phoneTwo, mattyEqual)
		require.NoError(t, err)
		require.Eventually(t, first.isClosed, 5*time.Second, 5*time.Millisecond)

		s.r.endOutdated("0.1.32")
		assert.Equal(t, 1, s.r.count(), "the phone that proved the minimum lost its voice")
		assert.False(t, second.isClosed())
	})

	t.Run("the phone that took over proved less", func(t *testing.T) {
		s := newVoiceProvenScene(t)
		now := s.a.p.appCheck().now()
		s.keepFor(t, phoneTwo, provenPass(talkBob, "0.1.31", now))

		first, err := s.joinAs(talkBob, phoneOne, mattyEqual)
		require.NoError(t, err)
		second, err := s.joinAs(talkBob, phoneTwo, mattyEqual)
		require.NoError(t, err)
		require.Eventually(t, first.isClosed, 5*time.Second, 5*time.Millisecond)

		s.r.endOutdated("0.1.32")
		require.Eventually(t, second.isClosed, 5*time.Second, 5*time.Millisecond)
		end, _ := second.ended()
		assert.Equal(t, voiceEndSignal{End: voiceEndOff, Reason: provedOlderText}, end)
		assert.Zero(t, s.r.count())
	})

	t.Run("signing out ends the voice of the session that holds it", func(t *testing.T) {
		s := newVoiceProvenScene(t)

		_, err := s.joinAs(talkBob, phoneOne, mattyEqual)
		require.NoError(t, err)
		second, err := s.joinAs(talkBob, phoneTwo, mattyEqual)
		require.NoError(t, err)

		s.lock.Lock()
		s.sessions[phoneOne] = voiceSessionOver
		s.lock.Unlock()
		s.r.sweep()
		assert.Equal(t, 1, s.r.count(), "the session that opened it signed out, not the one that holds it")
		assert.False(t, second.isClosed())

		s.lock.Lock()
		s.sessions[phoneTwo] = voiceSessionOver
		s.lock.Unlock()
		s.r.sweep()
		require.Eventually(t, second.isClosed, 5*time.Second, 5*time.Millisecond)
		end, _ := second.ended()
		assert.Equal(t, voiceEndSignal{End: voiceEndSession, Reason: "Your Mattermost session has ended."}, end)
	})
}
