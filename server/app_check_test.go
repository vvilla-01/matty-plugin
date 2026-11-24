package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/vvilla-01/matty-plugin/server/appcheck"
	"github.com/vvilla-01/matty-plugin/server/appcheck/appchecktest"
	"github.com/vvilla-01/matty-plugin/server/release"
)

// The app check's routes, its store and its gate (app_check.go), on the lock
// screen's scene, with attestations made under a test root that the plugin
// is told to accept in place of Google's. What an attestation must be is
// server/appcheck's tests; these are about the challenge, the session, the
// origin, what is kept and who is refused.

const (
	appSite       = "https://chat.example.com"
	mattyAgent    = "Matty/0.1.27 (Android 17; Mattermost client)"
	browserAgent  = "Mozilla/5.0 (X11; Linux x86_64) Gecko/20100101 Firefox/150.0"
	davesPhone    = "sess-dave-phone-00000000000"
	davesLaptop   = "sess-dave-laptop-0000000000"
	dansPhone     = "sess-dan-phone-000000000000"
	sessionLength = 90 * 24 * time.Hour
)

type appCheckScene struct {
	*accessScene

	root *appchecktest.Root

	lock     sync.Mutex
	site     string
	sessions map[string]*model.Session
}

func newAppCheckScene(t *testing.T, mode string) *appCheckScene {
	t.Helper()

	s := newAccessScene(t)
	a := &appCheckScene{accessScene: s, root: appchecktest.NewRoot(), site: appSite}

	expires := s.now.Add(sessionLength).UnixMilli()
	a.sessions = map[string]*model.Session{
		davesPhone:  {Id: davesPhone, UserId: sceneLinked, ExpiresAt: expires},
		davesLaptop: {Id: davesLaptop, UserId: sceneLinked, ExpiresAt: expires},
		dansPhone:   {Id: dansPhone, UserId: sceneLocked, ExpiresAt: expires},
	}

	// This scene's Site URL, in place of the lock scene's none.
	kept := s.api.ExpectedCalls[:0]
	for _, call := range s.api.ExpectedCalls {
		if call.Method != "GetConfig" {
			kept = append(kept, call)
		}
	}
	s.api.ExpectedCalls = kept
	s.api.On("GetConfig").Return(func() *model.Config {
		a.lock.Lock()
		defer a.lock.Unlock()

		config := &model.Config{}
		if a.site != "" {
			site := a.site
			config.ServiceSettings.SiteURL = &site
		}

		return config
	}).Maybe()
	s.api.On("GetSession", mock.Anything).Return(func(sessionID string) (*model.Session, *model.AppError) {
		a.lock.Lock()
		defer a.lock.Unlock()

		session, known := a.sessions[sessionID]
		if !known {
			return nil, model.NewAppError("GetSession", "app.session.missing", nil, "", http.StatusUnauthorized)
		}
		copied := *session

		return &copied, nil
	}).Maybe()

	a.setMode(mode)
	s.p.appCheckRoots = map[string]bool{a.root.Key(): true}
	s.p.appCheck().now = func() time.Time {
		s.lock.Lock()
		defer s.lock.Unlock()

		return s.now
	}

	return a
}

func (a *appCheckScene) setMode(mode string) {
	cfg := a.p.getConfiguration().Clone()
	cfg.AppCheck = mode
	a.p.setConfiguration(cfg)
}

func (a *appCheckScene) setSite(site string) {
	a.lock.Lock()
	defer a.lock.Unlock()

	a.site = site
}

// call is one request as Mattermost hands it on: with the caller's session,
// and the User-Agent the client sent.
func (a *appCheckScene) call(method, target, userID, sessionID, agent string, body any) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			panic(err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}

	request := httptest.NewRequest(method, target, reader)
	request.Header.Set("Mattermost-User-Id", userID)
	request.Header.Set("User-Agent", agent)
	request.Header.Set("X-Requested-With", "XMLHttpRequest")
	recorder := httptest.NewRecorder()

	a.p.ServeHTTP(&plugin.Context{SessionId: sessionID}, recorder, request)

	return recorder
}

func (a *appCheckScene) challenge(t *testing.T, userID, sessionID string) string {
	t.Helper()

	response := a.call(http.MethodPost, appChallengePath, userID, sessionID, mattyAgent, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

	var answer struct {
		Challenge string `json:"challenge"`
		Expires   int64  `json:"expires"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer))
	raw, err := base64.StdEncoding.DecodeString(answer.Challenge)
	require.NoError(t, err)
	require.Len(t, raw, 32)
	require.Equal(t, a.now.Add(5*time.Minute).UnixMilli(), answer.Expires)

	return answer.Challenge
}

func attestBody(challenge, origin string, chain [][]byte) map[string]any {
	encoded := make([]string, len(chain))
	for i, der := range chain {
		encoded[i] = base64.StdEncoding.EncodeToString(der)
	}

	return map[string]any{
		"challenge": challenge,
		"origin":    origin,
		"chain":     encoded,
		"claims": map[string]any{
			"package": "ceo.villa.matty", "versionCode": 28, "hybrid": true,
			"currentSigner": "75:78:CB:63", "history": []string{"E5:52:50:BE", "75:78:CB:63"},
		},
	}
}

// prove is Matty proving itself as phone, from origin.
func (a *appCheckScene) prove(t *testing.T, userID, sessionID string, phone appchecktest.Phone, origin string) *httptest.ResponseRecorder {
	t.Helper()

	challenge := a.challenge(t, userID, sessionID)
	chain := a.root.Chain(phone, appcheck.DeriveChallenge(origin, userID, challenge))

	return a.call(http.MethodPost, appAttestPath, userID, sessionID, mattyAgent, attestBody(challenge, origin, chain))
}

func (a *appCheckScene) appWordOf(t *testing.T, path, userID, sessionID string) map[string]any {
	t.Helper()

	response := a.call(http.MethodGet, path, userID, sessionID, mattyAgent, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	word, ok := body["app"].(map[string]any)
	require.True(t, ok, "%s has no app", path)

	return word
}

func decoded(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var body map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body), response.Body.String())

	return body
}

var officialPhone = appchecktest.Intact(release.OfficialRSASHA256)

// debugCertificate stands for a debug build's signing certificate.
const debugCertificate = "5a6b7c8d9eaf00112233445566778899aabbccddeeff00112233445566778899"

func TestTheOfficialMattyProvesItselfForItsSession(t *testing.T) {
	a := newAppCheckScene(t, appCheckReport)

	require.Equal(t, map[string]any{"check": "report", "state": "none"}, a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone))

	response := a.prove(t, sceneLinked, davesPhone, officialPhone, appSite)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Equal(t, map[string]any{
		"state": "verified", "versionCode": float64(28),
		"expires": float64(a.now.AddDate(0, 0, 30).UnixMilli()),
	}, decoded(t, response))

	want := map[string]any{"check": "report", "state": "verified", "versionCode": float64(28)}
	require.Equal(t, want, a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone))
	require.Equal(t, want, a.appWordOf(t, "/api/v1/me", sceneLinked, davesPhone))

	// The session's, and nobody else's: the same person's laptop has not
	// proved anything, and another node reads the same from the store.
	require.Equal(t, "none", a.appWordOf(t, "/api/v1/access", sceneLinked, davesLaptop)["state"])
	other := newAccessSceneOn(t, a.store, a.faults)
	other.p.appCheck().now = a.p.appCheck().now
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/access", nil)
	request.Header.Set("Mattermost-User-Id", sceneLinked)
	other.p.ServeHTTP(&plugin.Context{SessionId: davesPhone}, recorder, request)
	require.Equal(t, "verified", decoded(t, recorder)["app"].(map[string]any)["state"])

	// What is kept: the verdict, the facts, the app's own word, labelled,
	// and when it goes; nothing that tells this phone from another.
	var kept appCheckResult
	require.NoError(t, json.Unmarshal(a.kv(kvAppCheckResultPrefix+davesPhone), &kept))
	require.Equal(t, appCheckResult{
		User: sceneLinked, Verified: true, At: a.now.UnixMilli(), Expires: a.now.AddDate(0, 0, 30).UnixMilli(),
		VersionCode: 28, Level: "tee", BootState: "verified",
		Package: "ceo.villa.matty", Digest: release.OfficialRSASHA256, RevocationList: appcheck.RevocationListDate,
		Claims: &appCheckClaims{Package: "ceo.villa.matty", VersionCode: 28, CurrentSigner: "75:78:CB:63", History: []string{"E5:52:50:BE", "75:78:CB:63"}, Hybrid: true},
		Said:   "0.1.27",
		Rules:  rulesNow(t, a),

		SaidKept: true,
	}, kept)
	require.Empty(t, a.kv(appCheckChallengeKey(davesPhone, 0)), "the challenge is spent")

	// A session is one person's: a verdict kept for it says nothing to a
	// request from anybody else that names it.
	require.Equal(t, "none", a.appWordOf(t, "/api/v1/access", sceneLocked, davesPhone)["state"])
}

func TestAChallengeAnswersOnceForItsOwnSessionWithinFiveMinutes(t *testing.T) {
	a := newAppCheckScene(t, appCheckReport)

	challenge := a.challenge(t, sceneLinked, davesPhone)
	chain := a.root.Chain(officialPhone, appcheck.DeriveChallenge(appSite, sceneLinked, challenge))
	body := attestBody(challenge, appSite, chain)

	// Another session of the same person cannot spend it.
	stale := a.call(http.MethodPost, appAttestPath, sceneLinked, davesLaptop, mattyAgent, body)
	require.Equal(t, http.StatusConflict, stale.Code)
	require.Equal(t, appCheckStaleText, decoded(t, stale)["error"])

	require.Equal(t, http.StatusOK, a.call(http.MethodPost, appAttestPath, sceneLinked, davesPhone, mattyAgent, body).Code)

	again := a.call(http.MethodPost, appAttestPath, sceneLinked, davesPhone, mattyAgent, body)
	require.Equal(t, http.StatusConflict, again.Code, "spent")

	// One left too long.
	challenge = a.challenge(t, sceneLinked, davesPhone)
	a.advance(5*time.Minute + time.Second)
	chain = a.root.Chain(officialPhone, appcheck.DeriveChallenge(appSite, sceneLinked, challenge))
	require.Equal(t, http.StatusConflict, a.call(http.MethodPost, appAttestPath, sceneLinked, davesPhone, mattyAgent, attestBody(challenge, appSite, chain)).Code)

	// A few may be outstanding, and the fourth pushes out the oldest.
	first := a.challenge(t, sceneLinked, davesPhone)
	a.advance(time.Second)
	second := a.challenge(t, sceneLinked, davesPhone)
	a.advance(time.Second)
	a.challenge(t, sceneLinked, davesPhone)
	a.advance(time.Second)
	a.challenge(t, sceneLinked, davesPhone)
	for challenge, want := range map[string]int{first: http.StatusConflict, second: http.StatusOK} {
		chain := a.root.Chain(officialPhone, appcheck.DeriveChallenge(appSite, sceneLinked, challenge))
		require.Equal(t, want, a.call(http.MethodPost, appAttestPath, sceneLinked, davesPhone, mattyAgent, attestBody(challenge, appSite, chain)).Code)
	}
}

func TestAnAttestationIsForThisServerAndThisPerson(t *testing.T) {
	a := newAppCheckScene(t, appCheckReport)

	// The app reached some other address.
	response := a.prove(t, sceneLinked, davesPhone, officialPhone, "https://elsewhere.example")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "The app reached this server at https://elsewhere.example, but its Site URL is https://chat.example.com.", decoded(t, response)["reason"])
	require.Equal(t, "unverified", a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone)["state"])

	// The same address written another way is the same origin, but the
	// challenge is derived from the origin as Origin writes it: an app that
	// derived it from the address as written answered another challenge.
	written := "HTTPS://Chat.Example.com:443/"
	challenge := a.challenge(t, sceneLinked, davesPhone)
	chain := a.root.Chain(officialPhone, appcheck.DeriveChallenge(appSite, sceneLinked, challenge))
	response = a.call(http.MethodPost, appAttestPath, sceneLinked, davesPhone, mattyAgent, attestBody(challenge, written, chain))
	require.Equal(t, "verified", decoded(t, response)["state"])
	response = a.prove(t, sceneLinked, davesPhone, officialPhone, written)
	require.Equal(t, "The app's attestation was made for another challenge, so it is not an answer to this one.", decoded(t, response)["reason"])

	// An attestation made for somebody else's id.
	challenge = a.challenge(t, sceneLinked, davesPhone)
	chain = a.root.Chain(officialPhone, appcheck.DeriveChallenge(appSite, sceneLocked, challenge))
	response = a.call(http.MethodPost, appAttestPath, sceneLinked, davesPhone, mattyAgent, attestBody(challenge, appSite, chain))
	require.Equal(t, "The app's attestation was made for another challenge, so it is not an answer to this one.", decoded(t, response)["reason"])

	// A server with no Site URL cannot say what its own address is.
	a.setSite("")
	response = a.prove(t, sceneLinked, davesPhone, officialPhone, appSite)
	require.Equal(t, "This server has no usable Site URL to check the app's address against. An admin sets it in System Console > Environment > Web Server.", decoded(t, response)["reason"])
}

func TestWhatFailsIsSaidAndKept(t *testing.T) {
	a := newAppCheckScene(t, appCheckReport)

	unlocked := officialPhone
	unlocked.Locked, unlocked.BootState = false, 2
	response := a.prove(t, sceneLocked, dansPhone, unlocked, appSite)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, map[string]any{
		"state": "unverified", "reason": "This phone's bootloader is unlocked.",
		"expires": float64(a.now.AddDate(0, 0, 30).UnixMilli()),
	}, decoded(t, response))

	require.Equal(t, map[string]any{"check": "report", "state": "unverified", "reason": "This phone's bootloader is unlocked."},
		a.appWordOf(t, "/api/v1/access", sceneLocked, dansPhone))

	// Somebody else's build: refused, until an admin lists its certificate.
	const ownBuild = "0011223344556677889900112233445566778899001122334455667788990011"
	build := appchecktest.Intact(ownBuild)
	require.Equal(t, "This copy of Matty was signed with a certificate this server does not accept (00:11:22:33:44:55:66:77…).",
		decoded(t, a.prove(t, sceneLocked, dansPhone, build, appSite))["reason"])

	cfg := a.p.getConfiguration().Clone()
	cfg.AppCheckCertificates = "\n00:11:22:33:44:55:66:77:88:99:00:11:22:33:44:55:66:77:88:99:00:11:22:33:44:55:66:77:88:99:00:11\nnot a digest\n"
	a.p.setConfiguration(cfg)
	require.Equal(t, "verified", decoded(t, a.prove(t, sceneLocked, dansPhone, build, appSite))["state"])
	require.Equal(t, "Not a SHA-256 digest, so left out: line 3 of More signing certificates.", cfg.appCheckProblem())
}

// A verdict of verified counts only while what it was given for is still
// accepted: a certificate or a boot key taken off the settings' lists, or a
// release built with another copy of Google's revocation list, and the app
// is "none" again, on the gate as on /access, at once and on every node.
func TestAVerdictStopsCountingOnceWhatItWasGivenForIsNot(t *testing.T) {
	a := newAppCheckScene(t, appCheckRequire)
	streams := func(sessionID string) int {
		return a.call(http.MethodGet, "/api/v1/streams", sceneLinked, sessionID, mattyAgent, nil).Code
	}
	setLists := func(certificates, bootKeys string) {
		cfg := a.p.getConfiguration().Clone()
		cfg.AppCheckCertificates, cfg.AppCheckBootKeys = certificates, bootKeys
		a.p.setConfiguration(cfg)
	}

	// A community's own build, listed, and then taken off the list.
	const ownBuild = "0011223344556677889900112233445566778899001122334455667788990011"
	setLists(ownBuild, "")
	require.Equal(t, "verified", decoded(t, a.prove(t, sceneLinked, davesPhone, appchecktest.Intact(ownBuild), appSite))["state"])
	require.Equal(t, http.StatusOK, streams(davesPhone))

	// What the admins' list says of Dave's phone's verdict.
	listed := func() appCheckSessionView {
		t.Helper()
		response := a.call(http.MethodGet, adminAppCheck, sceneLockedAdmin, "", browserAgent, nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var view appCheckAdminView
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &view))
		for _, person := range view.People {
			for _, session := range person.Sessions {
				if session.Session == davesPhone {
					return session
				}
			}
		}
		require.Fail(t, "Dave's phone is not on the admins' list")
		return appCheckSessionView{}
	}
	require.True(t, listed().Verified)
	require.False(t, listed().Stale)

	setLists("", "")
	require.Equal(t, map[string]any{"check": "require", "state": "none", "reason": appCheckRulesChangedText}, a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone))
	require.Equal(t, "none", a.appWordOf(t, "/api/v1/me", sceneLinked, davesPhone)["state"])
	require.Equal(t, appCheckRulesChangedText, a.appWordOf(t, "/api/v1/me", sceneLinked, davesPhone)["reason"])
	require.Equal(t, http.StatusForbidden, streams(davesPhone))
	refused := a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyAgent, nil)
	require.Equal(t, "Matty answers only the official Matty app, and this copy has to be checked again: this server's rules for the check have changed.", decoded(t, refused)["error"])
	shown := listed()
	require.False(t, shown.Verified, "the admins' list says what the gate does")
	require.True(t, shown.Stale)
	require.Equal(t, staleAppCheckReason, shown.Reason)
	var kept appCheckResult
	require.NoError(t, json.Unmarshal(a.kv(kvAppCheckResultPrefix+davesPhone), &kept))
	require.True(t, kept.Verified, "the record itself is kept as it was")

	setLists(ownBuild, "")
	require.Equal(t, http.StatusOK, streams(davesPhone), "listed again, the same verdict counts again")
	require.True(t, listed().Verified)
	require.False(t, listed().Stale)

	// A phone running its owner's own system, its key listed, and then not.
	const ownersKey = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	owners := officialPhone
	owners.BootState, owners.BootKey = 1, ownersKey
	setLists("", ownersKey)
	require.Equal(t, "verified", decoded(t, a.prove(t, sceneLinked, davesLaptop, owners, appSite))["state"])
	kept = appCheckResult{}
	require.NoError(t, json.Unmarshal(a.kv(kvAppCheckResultPrefix+davesLaptop), &kept))
	require.Equal(t, ownersKey, kept.BootKey)
	require.Equal(t, http.StatusOK, streams(davesLaptop))

	setLists("", "")
	require.Equal(t, "none", a.appWordOf(t, "/api/v1/access", sceneLinked, davesLaptop)["state"])
	require.Equal(t, http.StatusForbidden, streams(davesLaptop))

	// Judged against another build's revocation list, or kept before a
	// verdict said what it was given for.
	now := a.p.appCheck().now()
	good := appCheckResult{
		User: sceneLinked, Verified: true, At: now.UnixMilli(), Expires: now.Add(time.Hour).UnixMilli(),
		Package: mattyPackage, Digest: release.OfficialRSASHA256, RevocationList: appcheck.RevocationListDate,
		SaidKept: true,
	}
	require.NoError(t, a.p.keepResult(davesPhone, &good, now))
	require.Equal(t, http.StatusOK, streams(davesPhone))

	for name, change := range map[string]func(*appCheckResult){
		"an older list":      func(r *appCheckResult) { r.RevocationList = "2026-01-01" },
		"no list":            func(r *appCheckResult) { r.RevocationList = "" },
		"nothing it was for": func(r *appCheckResult) { r.Package, r.Digest, r.RevocationList = "", "", "" },
	} {
		stale := good
		change(&stale)
		require.NoError(t, a.p.keepResult(davesPhone, &stale, now))
		require.Equal(t, "none", a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone)["state"], name)
		require.Equal(t, http.StatusForbidden, streams(davesPhone), name)
		require.True(t, listed().Stale, name)
		require.False(t, listed().Verified, name)

		// Another node reads the same record from the store, and says the same.
		twin := newAccessSceneOn(t, a.store, a.faults)
		twin.p.appCheck().now = a.p.appCheck().now
		cfg := twin.p.getConfiguration().Clone()
		cfg.AppCheck = appCheckRequire
		twin.p.setConfiguration(cfg)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/access", nil)
		request.Header.Set("Mattermost-User-Id", sceneLinked)
		twin.p.ServeHTTP(&plugin.Context{SessionId: davesPhone}, recorder, request)
		require.Equal(t, "none", decoded(t, recorder)["app"].(map[string]any)["state"], name)
	}
}

// rulesNow is the digest of the rules a verdict is judged by under the
// scene's settings as they are.
func rulesNow(t *testing.T, a *appCheckScene) string {
	t.Helper()

	rules, err := a.p.appCheckRules(a.p.getConfiguration())
	require.NoError(t, err)
	require.Len(t, rules, 64)

	return rules
}

// A verdict of not verified counts only under the rules it was judged by:
// once an admin changes what the check accepts, it is "none" again, said as
// a check to be made again rather than one never made, on /access, /me, the
// gate and the admins' list, until the app proves itself again. Put back as
// they were, the rules are the same, and so is the verdict.
func TestAFailedVerdictIsJudgedAgainOnceTheRulesChange(t *testing.T) {
	a := newAppCheckScene(t, appCheckReport)
	setLists := func(certificates, bootKeys string) {
		cfg := a.p.getConfiguration().Clone()
		cfg.AppCheckCertificates, cfg.AppCheckBootKeys = certificates, bootKeys
		a.p.setConfiguration(cfg)
	}
	listed := func() appCheckSessionView {
		t.Helper()
		response := a.call(http.MethodGet, adminAppCheck, sceneLockedAdmin, "", browserAgent, nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.NotContains(t, response.Body.String(), `"rules"`, "the rules' digest is not shown")
		var view appCheckAdminView
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &view))
		for _, person := range view.People {
			for _, session := range person.Sessions {
				if session.Session == davesPhone {
					return session
				}
			}
		}
		require.Fail(t, "Dave's phone is not on the admins' list")
		return appCheckSessionView{}
	}

	// A community's own build, not yet listed: it fails, and says why.
	const ownBuild = "0011223344556677889900112233445566778899001122334455667788990011"
	const why = "This copy of Matty was signed with a certificate this server does not accept (00:11:22:33:44:55:66:77…)."
	build := appchecktest.Intact(ownBuild)
	require.Equal(t, why, decoded(t, a.prove(t, sceneLinked, davesPhone, build, appSite))["reason"])
	failed := map[string]any{"check": "report", "state": "unverified", "reason": why, "versionCode": float64(28)}
	require.Equal(t, failed, a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone))

	var kept appCheckResult
	require.NoError(t, json.Unmarshal(a.kv(kvAppCheckResultPrefix+davesPhone), &kept))
	require.False(t, kept.Verified)
	require.Equal(t, rulesNow(t, a), kept.Rules, "kept with the rules it was judged by")

	// The setting is not one of the rules: refusing is what is done with
	// the same verdict.
	a.setMode(appCheckRequire)
	refused := a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyAgent, nil)
	require.Equal(t, "Matty answers only the official Matty app, and this copy did not pass the check. "+why, decoded(t, refused)["error"])
	a.setMode(appCheckReport)
	require.False(t, listed().Stale)
	require.Equal(t, why, listed().Reason)

	// The admin lists its certificate: the verdict no longer counts, at once.
	setLists(ownBuild, "")
	again := map[string]any{"check": "report", "state": "none", "reason": appCheckRulesChangedText}
	require.Equal(t, again, a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone))
	require.Equal(t, again, a.appWordOf(t, "/api/v1/me", sceneLinked, davesPhone))
	shown := listed()
	require.True(t, shown.Stale)
	require.False(t, shown.Verified)
	require.Equal(t, staleUnverifiedAppCheckReason, shown.Reason)

	a.setMode(appCheckRequire)
	refused = a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyAgent, nil)
	require.Equal(t, http.StatusForbidden, refused.Code)
	require.Equal(t, codeAppUnverified, codedErrorOrEmpty(refused))
	require.Equal(t, "Matty answers only the official Matty app, and this copy has to be checked again: this server's rules for the check have changed.", decoded(t, refused)["error"])

	// Taken off again, the rules are what they were, and the verdict with them.
	setLists("", "")
	require.Equal(t, "unverified", a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone)["state"])
	setLists(ownBuild, "")

	// Proved again, under the new rules, it passes.
	require.Equal(t, "verified", decoded(t, a.prove(t, sceneLinked, davesPhone, build, appSite))["state"])
	require.Equal(t, http.StatusOK, a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyAgent, nil).Code)

	// Any other list counts as a change too: a boot key added.
	a.setMode(appCheckReport)
	unlocked := officialPhone
	unlocked.Locked, unlocked.BootState = false, 2
	require.Equal(t, "unverified", decoded(t, a.prove(t, sceneLinked, davesLaptop, unlocked, appSite))["state"])
	setLists(ownBuild, "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789")
	require.Equal(t, again, a.appWordOf(t, "/api/v1/access", sceneLinked, davesLaptop))

	// A verdict of verified is not judged again for a certificate added
	// that has nothing to do with it.
	require.Equal(t, "verified", a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone)["state"])
	setLists(ownBuild+"\n"+debugCertificate, "")
	require.Equal(t, "verified", a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone)["state"])
	require.False(t, listed().Stale)

	// A failed verdict kept without the rules it was judged by is judged again.
	now := a.p.appCheck().now()
	old := appCheckResult{User: sceneLinked, Reason: why, At: now.UnixMilli(), Expires: now.Add(time.Hour).UnixMilli()}
	require.NoError(t, a.p.keepResult(davesPhone, &old, now))
	require.Equal(t, again, a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone))
	require.True(t, listed().Stale)

	// And one never checked is still said as never checked.
	a.setMode(appCheckRequire)
	never := a.call(http.MethodGet, "/api/v1/streams", sceneLocked, dansPhone, mattyAgent, nil)
	require.Equal(t, "Matty answers only the official Matty app, and this copy has not been checked yet.", decoded(t, never)["error"])
	require.Equal(t, map[string]any{"check": "require", "state": "none"}, a.appWordOf(t, "/api/v1/access", sceneLocked, dansPhone))
}

// A verdict that failed on the Site URL (none set, or another address than
// the app reached) is judged again once an admin sets it, as the reason
// tells them to; the same Site URL written another way is no change.
func TestAVerdictOnTheSiteURLIsJudgedAgainOnceItIsFixed(t *testing.T) {
	a := newAppCheckScene(t, appCheckReport)
	// The app draws it on its own.
	const changed = "This server's rules for the app check have changed, and this Matty is to be checked under the new ones."
	again := map[string]any{"check": "report", "state": "none", "reason": changed}

	a.setSite("")
	response := a.prove(t, sceneLinked, davesPhone, officialPhone, appSite)
	require.Equal(t, "This server has no usable Site URL to check the app's address against. An admin sets it in System Console > Environment > Web Server.", decoded(t, response)["reason"])
	require.Equal(t, "unverified", a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone)["state"])

	a.setSite(appSite)
	require.Equal(t, again, a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone))
	require.Equal(t, "verified", decoded(t, a.prove(t, sceneLinked, davesPhone, officialPhone, appSite))["state"])

	response = a.prove(t, sceneLinked, davesLaptop, officialPhone, "https://elsewhere.example")
	require.Equal(t, "The app reached this server at https://elsewhere.example, but its Site URL is https://chat.example.com.", decoded(t, response)["reason"])
	a.setSite("HTTPS://Chat.Example.com:443/")
	require.Equal(t, "unverified", a.appWordOf(t, "/api/v1/access", sceneLinked, davesLaptop)["state"])
	a.setSite("https://elsewhere.example")
	require.Equal(t, again, a.appWordOf(t, "/api/v1/access", sceneLinked, davesLaptop))
}

// The rules' digest is every list's entries in a fixed order, the date of
// the revocation list, the development allowances and the Site URL, and
// nothing else.
func TestTheRulesDigestIsWhatTheCheckAccepts(t *testing.T) {
	policy := func(packages, digests, bootKeys []string, development bool) appcheck.Policy {
		set := func(entries []string) map[string]bool {
			out := map[string]bool{}
			for _, entry := range entries {
				out[entry] = true
			}
			return out
		}
		return appcheck.Policy{
			Roots: map[string]bool{"root": true}, Revoked: map[string]bool{"01": true},
			Packages: set(packages), Digests: set(digests), BootKeys: set(bootKeys), Development: development,
		}
	}

	base := appCheckRulesOf(policy([]string{"a", "b"}, []string{"c", "d"}, []string{"e"}, false), "2026-11-16", appSite)
	require.Len(t, base, 64)
	require.Equal(t, base, appCheckRulesOf(policy([]string{"b", "a"}, []string{"d", "c"}, []string{"e"}, false), "2026-11-16", appSite), "in any order")

	refused := policy([]string{"a", "b"}, []string{"c", "d"}, []string{"e"}, false)
	refused.Digests["x"] = false
	refused.Roots = map[string]bool{"another root": true}
	refused.Revoked = map[string]bool{}
	require.Equal(t, base, appCheckRulesOf(refused, "2026-11-16", appSite), "an entry set false is not accepted, and roots and serials are the build's")

	for name, other := range map[string]string{
		"a package":           appCheckRulesOf(policy([]string{"a"}, []string{"c", "d"}, []string{"e"}, false), "2026-11-16", appSite),
		"a certificate":       appCheckRulesOf(policy([]string{"a", "b"}, []string{"c", "d", "f"}, []string{"e"}, false), "2026-11-16", appSite),
		"a boot key":          appCheckRulesOf(policy([]string{"a", "b"}, []string{"c", "d"}, nil, false), "2026-11-16", appSite),
		"a list moved":        appCheckRulesOf(policy([]string{"a", "b"}, []string{"c"}, []string{"d", "e"}, false), "2026-11-16", appSite),
		"the revocation list": appCheckRulesOf(policy([]string{"a", "b"}, []string{"c", "d"}, []string{"e"}, false), "2026-12-01", appSite),
		"development":         appCheckRulesOf(policy([]string{"a", "b"}, []string{"c", "d"}, []string{"e"}, true), "2026-11-16", appSite),
		"the Site URL":        appCheckRulesOf(policy([]string{"a", "b"}, []string{"c", "d"}, []string{"e"}, false), "2026-11-16", "https://elsewhere.example"),
		"no Site URL":         appCheckRulesOf(policy([]string{"a", "b"}, []string{"c", "d"}, []string{"e"}, false), "2026-11-16", ""),
	} {
		require.NotEqual(t, base, other, name)
	}

	// The setting is not in it.
	a := newAppCheckScene(t, appCheckReport)
	rules := rulesNow(t, a)
	for _, mode := range []string{appCheckRequire, appCheckOff, appCheckReport} {
		a.setMode(mode)
		require.Equal(t, rules, rulesNow(t, a), mode)
	}
}

func TestAVerdictLastsAsLongAsItsSessionAndThirtyDaysAtMost(t *testing.T) {
	a := newAppCheckScene(t, appCheckReport)

	a.lock.Lock()
	a.sessions[davesPhone].ExpiresAt = a.now.Add(2 * time.Hour).UnixMilli()
	a.lock.Unlock()

	response := a.prove(t, sceneLinked, davesPhone, officialPhone, appSite)
	require.Equal(t, float64(a.now.Add(2*time.Hour).UnixMilli()), decoded(t, response)["expires"])

	a.advance(2*time.Hour + time.Minute)
	require.Equal(t, "none", a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone)["state"])

	require.Equal(t, http.StatusOK, a.prove(t, sceneLocked, dansPhone, officialPhone, appSite).Code)
	a.advance(30*24*time.Hour + time.Minute)
	require.Equal(t, "none", a.appWordOf(t, "/api/v1/access", sceneLocked, dansPhone)["state"])
}

func TestRequireRefusesAnUncheckedMattyAndNothingElse(t *testing.T) {
	a := newAppCheckScene(t, appCheckRequire)

	// Never checked: refused everywhere but the five routes, the clips
	// included; the web panel, and anything not saying it is Matty, never.
	for _, target := range []string{"/api/v1/streams", "/api/v1/boards", "/api/v1/accounts", "/api/v1/clips/thumb?id=x", "/api/v1/admin/appcheck", "/api/v1/nowhere"} {
		refused := a.call(http.MethodGet, target, sceneLinked, davesPhone, mattyAgent, nil)
		require.Equal(t, http.StatusForbidden, refused.Code, target)
		require.Equal(t, "no-store", refused.Header().Get("Cache-Control"))
		body := decoded(t, refused)
		require.Equal(t, codeAppUnverified, body["code"], target)
		require.Equal(t, "Matty answers only the official Matty app, and this copy has not been checked yet.", body["error"])

		for _, agent := range []string{browserAgent, "", "matty/0.1.27", "Mattermost/2.0 Matty/0.1.27"} {
			require.NotEqual(t, codeAppUnverified, codedErrorOrEmpty(a.call(http.MethodGet, target, sceneLinked, davesPhone, agent, nil)), "%s %q", target, agent)
		}
	}
	for _, target := range []string{"/api/v1/access", "/api/v1/me", "/api/v1/release"} {
		require.Equal(t, http.StatusOK, a.call(http.MethodGet, target, sceneLinked, davesPhone, mattyAgent, nil).Code, target)
	}
	require.Equal(t, map[string]any{"check": "require", "state": "none"}, a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone))

	// Checked, and failed: refused with why.
	unlocked := officialPhone
	unlocked.Locked, unlocked.BootState = false, 2
	require.Equal(t, http.StatusOK, a.prove(t, sceneLinked, davesPhone, unlocked, appSite).Code)
	refused := a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyAgent, nil)
	require.Equal(t, http.StatusForbidden, refused.Code)
	require.Equal(t, "Matty answers only the official Matty app, and this copy did not pass the check. This phone's bootloader is unlocked.", decoded(t, refused)["error"])

	// Passed: let through, on this session.
	require.Equal(t, http.StatusOK, a.prove(t, sceneLinked, davesPhone, officialPhone, appSite).Code)
	require.Equal(t, http.StatusOK, a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyAgent, nil).Code)
	require.Equal(t, codeAppUnverified, codedErrorOrEmpty(a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesLaptop, mattyAgent, nil)))

	// The setting takes effect with the next request, either way.
	a.setMode(appCheckReport)
	require.Equal(t, http.StatusOK, a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesLaptop, mattyAgent, nil).Code)
	a.setMode(appCheckRequire)
	require.Equal(t, http.StatusForbidden, a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesLaptop, mattyAgent, nil).Code)
	a.setMode(appCheckOff)
	require.Equal(t, http.StatusOK, a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesLaptop, mattyAgent, nil).Code)
}

// A verdict proved on another node reaches this one's gate within seconds,
// and a store that cannot be read refuses rather than lets through.
func TestTheGateReadsTheStore(t *testing.T) {
	a := newAppCheckScene(t, appCheckRequire)

	require.Equal(t, http.StatusForbidden, a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyAgent, nil).Code)

	twin := newAccessSceneOn(t, a.store, a.faults)
	twin.p.appCheck().now = a.p.appCheck().now
	cfg := twin.p.getConfiguration().Clone()
	cfg.AppCheck = appCheckRequire
	twin.p.setConfiguration(cfg)
	onTwin := func() int {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/streams", nil)
		request.Header.Set("Mattermost-User-Id", sceneLinked)
		request.Header.Set("User-Agent", mattyAgent)
		twin.p.ServeHTTP(&plugin.Context{SessionId: davesPhone}, recorder, request)

		return recorder.Code
	}
	require.Equal(t, http.StatusForbidden, onTwin())

	// Proved here: the twin goes on refusing for what is left of the few
	// seconds it remembers "none" for, and then asks the store again.
	require.Equal(t, http.StatusOK, a.prove(t, sceneLinked, davesPhone, officialPhone, appSite).Code)
	require.Equal(t, http.StatusForbidden, onTwin())
	a.advance(6 * time.Second)
	require.Equal(t, http.StatusOK, onTwin())

	a.faults.failRead(kvAppCheckResultPrefix + davesLaptop)
	failed := a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesLaptop, mattyAgent, nil)
	require.Equal(t, http.StatusServiceUnavailable, failed.Code)
	require.Equal(t, "This server could not read its app check records just now.", a.appWordOf(t, "/api/v1/access", sceneLinked, davesLaptop)["reason"])
}

func TestTheCheckRoutesAskForASessionAMeterAndTheSetting(t *testing.T) {
	a := newAppCheckScene(t, appCheckOff)

	require.Equal(t, map[string]any{"check": "off", "state": "none"}, a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone))
	for _, path := range []string{appChallengePath, appAttestPath} {
		off := a.call(http.MethodPost, path, sceneLinked, davesPhone, mattyAgent, map[string]any{})
		require.Equal(t, http.StatusForbidden, off.Code)
		require.Equal(t, appCheckOffText, decoded(t, off)["error"])
	}

	a.setMode(appCheckReport)

	// No session: a request from inside the server, or one Mattermost did
	// not tie to a session.
	require.Equal(t, http.StatusUnauthorized, a.call(http.MethodPost, appChallengePath, sceneLinked, "", mattyAgent, nil).Code)
	require.Equal(t, http.StatusMethodNotAllowed, a.call(http.MethodGet, appChallengePath, sceneLinked, davesPhone, mattyAgent, nil).Code)
	require.Equal(t, http.StatusMethodNotAllowed, a.call(http.MethodGet, appAttestPath, sceneLinked, davesPhone, mattyAgent, nil).Code)

	// Bodies that are not an attestation request.
	challenge := a.challenge(t, sceneLinked, davesPhone)
	for _, body := range []map[string]any{
		{"challenge": "short", "origin": appSite, "chain": []string{"AAAA"}},
		{"challenge": challenge, "origin": appSite, "chain": []string{}},
		{"challenge": challenge, "origin": appSite, "chain": []string{"not base64!"}},
	} {
		require.Equal(t, http.StatusBadRequest, a.call(http.MethodPost, appAttestPath, sceneLinked, davesPhone, mattyAgent, body).Code)
	}
	huge := attestBody(challenge, appSite, [][]byte{bytes.Repeat([]byte{1}, maxAttestBody)})
	require.Equal(t, http.StatusBadRequest, a.call(http.MethodPost, appAttestPath, sceneLinked, davesPhone, mattyAgent, huge).Code)

	// Locked people may prove their app too: it is about the app.
	require.Equal(t, http.StatusOK, a.prove(t, sceneLocked, dansPhone, officialPhone, appSite).Code)

	// Ten a minute each, per person.
	for range 9 {
		a.challenge(t, sceneLocked, dansPhone)
	}
	limited := a.call(http.MethodPost, appChallengePath, sceneLocked, dansPhone, mattyAgent, nil)
	require.Equal(t, http.StatusTooManyRequests, limited.Code)
	require.Equal(t, codeRateLimited, codedErrorOrEmpty(limited))
	a.advance(time.Minute)
	a.challenge(t, sceneLocked, dansPhone)
}

func TestAdminsSeeEverybodysResults(t *testing.T) {
	a := newAppCheckScene(t, appCheckReport)

	require.Equal(t, http.StatusOK, a.prove(t, sceneLinked, davesPhone, officialPhone, appSite).Code)
	a.advance(time.Minute)
	unlocked := officialPhone
	unlocked.Locked, unlocked.BootState = false, 2
	require.Equal(t, http.StatusOK, a.prove(t, sceneLinked, davesLaptop, unlocked, appSite).Code)
	require.Equal(t, http.StatusOK, a.prove(t, sceneLocked, dansPhone, officialPhone, appSite).Code)

	refused := a.call(http.MethodGet, adminAppCheck, sceneLinked, davesPhone, browserAgent, nil)
	require.Equal(t, http.StatusForbidden, refused.Code)

	response := a.call(http.MethodGet, adminAppCheck, sceneLockedAdmin, "", browserAgent, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var view appCheckAdminView
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &view))
	require.Equal(t, appCheckReport, view.Check)
	require.Len(t, view.People, 2)

	people := map[string]appCheckPersonView{}
	for _, person := range view.People {
		people[person.UserID] = person
	}
	dave := people[sceneLinked]
	require.Equal(t, "dave", dave.Username)
	require.Len(t, dave.Sessions, 2)
	require.Equal(t, davesLaptop, dave.Sessions[0].Session, "the latest first")
	require.False(t, dave.Sessions[0].Verified)
	require.Equal(t, "This phone's bootloader is unlocked.", dave.Sessions[0].Reason)
	require.True(t, dave.Sessions[1].Verified)
	require.Equal(t, "tee", dave.Sessions[1].Level)
	require.Equal(t, "verified", dave.Sessions[1].BootState)
	require.Equal(t, "ceo.villa.matty", dave.Sessions[1].Claims.Package)
	require.Len(t, people[sceneLocked].Sessions, 1)

	// Expired results drop off the list.
	a.advance(31 * 24 * time.Hour)
	response = a.call(http.MethodGet, adminAppCheck, sceneLockedAdmin, "", browserAgent, nil)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &view))
	require.Empty(t, view.People)
}

// Claims are the app's word, shown to admins as text: kept small and
// printable.
func TestTheAppsClaimsAreKeptSmallAndPrintable(t *testing.T) {
	long := strings.Repeat("x", 1000)
	cleaned := cleanClaims(&appCheckClaims{
		Package: "ceo.villa.matty\n\x00", CurrentSigner: long,
		History: []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"},
	})
	require.Equal(t, "ceo.villa.matty", cleaned.Package)
	require.Len(t, cleaned.CurrentSigner, 128)
	require.Len(t, cleaned.History, 8)
	require.Nil(t, cleanClaims(nil))
}

func TestTheAppCheckSettingReadsAsThePageShowsIt(t *testing.T) {
	for value, want := range map[string]string{"": "report", "report": "report", " off ": "off", "require": "require", "Require": "report", "nonsense": "report"} {
		require.Equal(t, want, (&configuration{AppCheck: value}).appCheckMode(), value)
	}

	digests, bad := digestLines("E5:52:50:BE:A6:69:00:39:B7:84:9E:33:B0:49:08:8E:CB:B9:98:D9:F2:0A:AA:8C:7E:B2:DC:5A:43:63:27:3C\n\n  \nE5:52\n")
	require.Equal(t, map[string]bool{release.OfficialRSASHA256: true}, digests)
	require.Equal(t, []int{4}, bad)
	require.Empty(t, (&configuration{}).appCheckProblem())
	require.Equal(t, "Not a SHA-256 digest, so left out: line 1 of More signing certificates; line 2, 3 of More verified-boot keys.",
		(&configuration{AppCheckCertificates: "x", AppCheckBootKeys: "\nyy\nzz"}).appCheckProblem())
}

// The contract's files for the app check: a challenge, the two verdicts,
// and the refusal while it is required.
func TestContractAppCheck(t *testing.T) {
	a := newAppCheckScene(t, appCheckRequire)

	challenge := a.call(http.MethodPost, appChallengePath, sceneLinked, davesPhone, mattyAgent, nil)
	require.Equal(t, http.StatusOK, challenge.Code)
	answer := decoded(t, challenge)
	answer["challenge"] = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="
	encoded, err := json.Marshal(answer)
	require.NoError(t, err)
	contractFixture(t, "app-challenge.json", encoded)

	unlocked := officialPhone
	unlocked.Locked, unlocked.BootState = false, 2
	failed := a.prove(t, sceneLinked, davesPhone, unlocked, appSite)
	contractFixture(t, "app-attest-unverified.json", failed.Body.Bytes())

	refused := a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyAgent, nil)
	require.Equal(t, http.StatusForbidden, refused.Code)
	contractFixture(t, "refusal-app-unverified.json", refused.Body.Bytes())

	passed := a.prove(t, sceneLinked, davesPhone, officialPhone, appSite)
	contractFixture(t, "app-attest-verified.json", passed.Body.Bytes())

	access := a.call(http.MethodGet, "/api/v1/access", sceneLinked, davesPhone, mattyAgent, nil)
	contractFixture(t, "access-app-verified.json", access.Body.Bytes())
}

// appCheckWorld is a server whose settings have not been loaded yet: its
// configuration, the WebSocket events published and how often the
// configuration was read.
type appCheckWorld struct {
	p *Plugin

	lock    sync.Mutex
	config  *model.Config
	kv      map[string][]byte
	events  []string
	configs int
}

func (w *appCheckWorld) count(field *int) int {
	w.lock.Lock()
	defer w.lock.Unlock()

	return *field
}

func (w *appCheckWorld) publishedEvents() []string {
	w.lock.Lock()
	defer w.lock.Unlock()

	return append([]string(nil), w.events...)
}

func newAppCheckWorld(t *testing.T) *appCheckWorld {
	t.Helper()

	config := &model.Config{}
	config.SetDefaults()
	*config.ServiceSettings.SiteURL = "http://localhost:8065"
	config.PluginSettings.Plugins = map[string]map[string]any{}

	w := &appCheckWorld{config: config, kv: map[string][]byte{}}
	api := &plugintest.API{}
	p := &Plugin{}
	p.SetAPI(api)
	w.p = p

	api.On("KVGet", mock.Anything).Return(func(key string) []byte {
		w.lock.Lock()
		defer w.lock.Unlock()

		return w.kv[key]
	}, func(string) *model.AppError { return nil })
	api.On("KVSet", mock.Anything, mock.Anything).Return(func(key string, value []byte) *model.AppError {
		w.lock.Lock()
		defer w.lock.Unlock()

		w.kv[key] = value

		return nil
	})
	api.On("KVSetWithOptions", mock.Anything, mock.Anything, mock.Anything).Return(
		func(key string, value []byte, options model.PluginKVSetOptions) bool {
			w.lock.Lock()
			defer w.lock.Unlock()

			current, present := w.kv[key]
			if options.Atomic && (options.OldValue == nil && present || options.OldValue != nil && (!present || !bytes.Equal(current, options.OldValue))) {
				return false
			}
			if value == nil {
				delete(w.kv, key)
			} else {
				w.kv[key] = value
			}

			return true
		},
		func(string, []byte, model.PluginKVSetOptions) *model.AppError { return nil },
	)
	api.On("KVDelete", mock.Anything).Return(func(key string) *model.AppError {
		w.lock.Lock()
		defer w.lock.Unlock()

		delete(w.kv, key)

		return nil
	})
	api.On("GetUnsanitizedConfig").Return(func() *model.Config {
		w.lock.Lock()
		defer w.lock.Unlock()

		return w.config.Clone()
	})
	api.On("GetConfig").Return(func() *model.Config {
		w.lock.Lock()
		defer w.lock.Unlock()

		w.configs++

		return w.config.Clone()
	})
	api.On("LoadPluginConfiguration", mock.Anything).Run(func(args mock.Arguments) {
		w.lock.Lock()
		encoded, _ := json.Marshal(w.config.PluginSettings.Plugins[manifestID])
		w.lock.Unlock()

		_ = json.Unmarshal(encoded, args.Get(0))
	}).Return(nil)
	api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		w.lock.Lock()
		defer w.lock.Unlock()

		w.events = append(w.events, args.String(0))
	}).Return().Maybe()
	for _, level := range []string{"LogWarn", "LogError", "LogInfo", "LogDebug"} {
		for pairs := 0; pairs <= 10; pairs++ {
			args := make([]any, 0, 1+pairs*2)
			for i := 0; i < 1+pairs*2; i++ {
				args = append(args, mock.Anything)
			}
			api.On(level, args...).Return().Maybe()
		}
	}

	return w
}

// appCheckSaves is an appCheckWorld, and save, which stores settings (and
// the Site URL) and runs OnConfigurationChange as a save does, saying how
// many "me" events that sent.
func appCheckSaves(t *testing.T) (*appCheckWorld, func(settings map[string]any, site string) int) {
	t.Helper()

	w := newAppCheckWorld(t)
	t.Cleanup(w.p.stopPoller)

	sent := func() int {
		count := 0
		for _, event := range w.publishedEvents() {
			if event == wsEventMe {
				count++
			}
		}

		return count
	}
	save := func(settings map[string]any, site string) int {
		t.Helper()

		w.lock.Lock()
		w.config.PluginSettings.Plugins[manifestID] = settings
		*w.config.ServiceSettings.SiteURL = site
		w.lock.Unlock()

		before := sent()
		require.NoError(t, w.p.OnConfigurationChange())

		return sent() - before
	}

	return w, save
}

func setActivation(p *Plugin, activated bool) {
	p.activatedLock.Lock()
	p.activated = activated
	p.activatedLock.Unlock()
}

// Every open client is told when the app check's setting or what it
// accepts changes, a Matty then asking /access again: one event a save,
// and none for a save that changes nothing about the check. The Site URL is
// Mattermost's setting, not the plugin's, and is told like the others.
func TestEveryOpenClientIsToldWhenTheAppCheckChanges(t *testing.T) {
	const (
		site        = "http://localhost:8065"
		certificate = "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99"
		bootKey     = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	)
	w, save := appCheckSaves(t)
	settings := func(changes ...any) map[string]any {
		saved := map[string]any{"name": "Acme TV", "appcheck": "report", "pollintervalseconds": 15}
		for i := 0; i+1 < len(changes); i += 2 {
			saved[changes[i].(string)] = changes[i+1]
		}

		return saved
	}

	// The first load only keeps what is in force, even on a plugin that
	// is already up: there is nothing to compare it with.
	setActivation(w.p, true)
	require.Zero(t, save(settings(), site), "the first load")

	require.Zero(t, save(settings(), site), "the same settings saved again")
	require.Zero(t, save(settings("pollintervalseconds", 30), site), "a setting that is not the check's")
	require.Zero(t, save(settings("pollintervalseconds", 30), site+"/"), "the same Site URL written another way")

	require.Equal(t, 1, save(settings("appcheck", "require"), site), "the setting")
	require.Equal(t, 1, save(settings("appcheck", "off"), site), "the setting, switched off")
	require.Equal(t, 1, save(settings("appcheck", "off", "appcheckcertificates", certificate), site), "a certificate listed")
	require.Zero(t, save(settings("appcheck", "off", "appcheckcertificates", strings.ToLower(certificate)), site), "the same certificate written another way")
	require.Equal(t, 1, save(settings("appcheck", "off"), site), "the certificate taken off")
	require.Equal(t, 1, save(settings("appcheck", "off"), appSite), "the Site URL")
	require.Equal(t, 1, save(settings("appcheck", "off", "name", "Acme Live", "appcheckbootkeys", bootKey), appSite), "the name and a boot key: one event")
	require.Equal(t, 1, save(settings("appcheck", "off", "name", "Acme TV", "appcheckbootkeys", bootKey), appSite), "the name alone, as before")
	require.Zero(t, save(settings("appcheck", "off", "appcheckbootkeys", bootKey), appSite), "nothing that is told")
}

// Before the plugin is up nobody is told, but what is in force is kept all
// the same: the first save once it is up tells everybody when it changed
// something since, and nobody when it did not.
func TestNobodyIsToldOfTheAppCheckBeforeThePluginIsUp(t *testing.T) {
	t.Run("changed since", func(t *testing.T) {
		w, save := appCheckSaves(t)
		require.Zero(t, save(map[string]any{"appcheck": "report"}, "http://localhost:8065"), "Mattermost's load before OnActivate")

		setActivation(w.p, true)
		require.Equal(t, 1, save(map[string]any{"appcheck": "require"}, "http://localhost:8065"), "the first save once up")
	})

	t.Run("a load before it is up", func(t *testing.T) {
		w, save := appCheckSaves(t)
		require.Zero(t, save(map[string]any{"appcheck": "report"}, "http://localhost:8065"), "Mattermost's load before OnActivate")
		require.Zero(t, save(map[string]any{"appcheck": "require"}, appSite), "another load before it is up")

		setActivation(w.p, true)
		require.Zero(t, save(map[string]any{"appcheck": "require"}, appSite), "up, and nothing changed since the last load")
		require.Equal(t, 1, save(map[string]any{"appcheck": "report"}, appSite), "up, and changed")
	})
}

// Two loads can run at once, because Mattermost runs this hook after letting
// go of its configuration lock. One that is slow to keep what it found must
// not keep a Site URL older than the one kept before it: it reads the Site
// URL only once it holds the lock, so it finds what is in force and tells
// nobody again.
func TestAnOlderSiteURLIsNeverKeptAfterANewerOne(t *testing.T) {
	w, save := appCheckSaves(t)
	setActivation(w.p, true)
	require.Zero(t, save(map[string]any{"appcheck": "report"}, "http://localhost:8065"), "the first load")
	cfg := *w.p.getConfiguration()

	// The slow load starts while another holds the lock, and is given time
	// to read the Site URL before it can lock.
	kept := &w.p.appCheckKept
	kept.lock.Lock()
	read := w.count(&w.configs)
	done := make(chan bool, 1)
	go func() { done <- w.p.noteAppCheckInForce(&cfg) }()
	for deadline := time.Now().Add(250 * time.Millisecond); time.Now().Before(deadline) && w.count(&w.configs) == read; {
		time.Sleep(time.Millisecond)
	}

	// The other load, for a newer Site URL, keeps it and lets go.
	w.lock.Lock()
	*w.config.ServiceSettings.SiteURL = appSite
	w.lock.Unlock()
	newer, err := w.p.appCheckRules(&cfg)
	require.NoError(t, err)
	kept.rules = newer
	kept.lock.Unlock()

	require.False(t, <-done, "the slow load finds what is in force")
	kept.lock.Lock()
	defer kept.lock.Unlock()
	require.Equal(t, newer, kept.rules, "the newer Site URL is still kept")
}
