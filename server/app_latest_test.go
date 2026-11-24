package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The latest release of Matty as the minimum (app_latest.go), against the
// test's own GitHub on this machine, over HTTPS.

const appLatestPath = "/repos/vvilla-01/matty/releases/latest"

// appRelease answers the newest release of Matty as a tag with these files,
// among fields nothing reads.
func (g *fakeGitHub) appRelease(tag string, draft, prerelease bool, assets ...string) {
	files := make([]map[string]any, 0, len(assets))
	for _, name := range assets {
		files = append(files, map[string]any{"name": name, "browser_download_url": "https://elsewhere.example/" + name, "size": 1})
	}
	body, _ := json.Marshal(map[string]any{
		"tag_name": tag, "draft": draft, "prerelease": prerelease, "assets": files,
		"name": "Release notes nobody reads", "body": "**Markdown** that must never be shown", "html_url": "https://elsewhere.example/page",
		"target_commitish": "main", "published_at": "2026-11-20T10:00:00Z",
	})
	g.route(appLatestPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
}

// appAsked is how many times the app's repository was asked; pluginAsked,
// the plugin's.
func (g *fakeGitHub) appAsked() int { return g.askedAt(appLatestPath) }
func (g *fakeGitHub) pluginAsked() int {
	return g.askedAt("/repos/vvilla-01/matty-plugin/releases/latest")
}

func (g *fakeGitHub) askedAt(path string) int {
	count := 0
	for _, request := range g.asked() {
		if request.URL.Path == path {
			count++
		}
	}

	return count
}

// latestScene is the update scene's nodes, each with the latest's runtime
// started under Minimum Matty version as given, and counting what it is
// told and what it schedules.
type latestScene struct {
	*updateScene

	counts sync.Mutex
	jobs   int
	shut   int
	events map[*Plugin]int
}

func newLatestScene(t *testing.T) *latestScene {
	return &latestScene{updateScene: newUpdateScene(t), events: map[*Plugin]int{}}
}

func (l *latestScene) latestNode(updatesOn bool, minimum string) *Plugin {
	return l.latestNodeWith(updatesOn, minimum, nil)
}

// latestNodeWith is latestNode, with before done to the node before its
// latest's runtime starts.
func (l *latestScene) latestNodeWith(updatesOn bool, minimum string, before func(p *Plugin)) *Plugin {
	l.t.Helper()

	p := l.nodeWith(updatesOn, nil)
	cfg := p.getConfiguration().Clone()
	cfg.MinimumAppVersion = minimum
	p.setConfiguration(cfg)

	// The mock changes only while nothing of the node runs.
	p.stopUpdates()
	defer p.startUpdates()
	if before != nil {
		before(p)
	}

	api := p.API.(*plugintest.API)
	api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Run(func(arguments mock.Arguments) {
		if arguments.String(0) == wsEventMe {
			l.counts.Lock()
			l.events[p]++
			l.counts.Unlock()
		}
	}).Return().Maybe()
	// Ahead of the one the store's mock allows.
	counted := api.ExpectedCalls[len(api.ExpectedCalls)-1]
	api.ExpectedCalls = append([]*mock.Call{counted}, api.ExpectedCalls[:len(api.ExpectedCalls)-1]...)

	r := p.appLatest()
	r.tickEvery = time.Hour
	r.schedule = func(cluster.JobPluginAPI, func()) (clipsJob, error) {
		l.counts.Lock()
		l.jobs++
		l.counts.Unlock()
		return latestJob{l}, nil
	}
	p.startAppLatest()
	l.t.Cleanup(p.stopAppLatest)

	return p
}

func withTalk(s minimumScene) func(p *Plugin) {
	return func(p *Plugin) { p.voiceTalkOnce.Do(func() { p.voiceTalkState = s.r }) }
}

type latestJob struct{ l *latestScene }

func (j latestJob) Close() error {
	j.l.counts.Lock()
	j.l.shut++
	j.l.counts.Unlock()
	return nil
}

func (l *latestScene) toldMe(p *Plugin) int {
	l.counts.Lock()
	defer l.counts.Unlock()

	return l.events[p]
}

func (l *latestScene) jobsMade() (made, closed int) {
	l.counts.Lock()
	defer l.counts.Unlock()

	return l.jobs, l.shut
}

func (l *latestScene) stored() appLatestSeen {
	var seen appLatestSeen
	if raw := l.kv(kvAppLatestSeen); len(raw) > 0 {
		require.NoError(l.t, json.Unmarshal(raw, &seen))
	}

	return seen
}

func (l *latestScene) checkLatest(p *Plugin, userID string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	p.handleAdminAppLatestCheck(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/admin/appcheck/latest", nil), userID)

	return recorder
}

func lookLatest(p *Plugin) {
	p.lookForAppLatest(context.Background())
}

// ---------------------------------------------------------------- the setting

// Three values: blank, latest in any case with spaces around it, or N.N.N.
// Anything else is none, and only that is a problem on the card.
func TestTheMinimumSettingHasThreeValues(t *testing.T) {
	for value, want := range map[string]string{
		"": "", "  ": "", "latest": appMinimumFromLatest, " LATEST ": appMinimumFromLatest, "Latest ": appMinimumFromLatest,
		"0.1.32": appMinimumFromSetting, " 0.1.33 ": appMinimumFromSetting,
		"lates": "", "latest release": "", "0.1": "", "v0.1.32": "",
	} {
		cfg := &configuration{MinimumAppVersion: value}
		require.Equal(t, want, cfg.appMinimumFrom(), "%q", value)
		require.Equal(t, want == appMinimumFromLatest, cfg.appMinimumLatest(), "%q", value)
	}

	for _, value := range []string{"latest", " LATEST ", "Latest "} {
		require.Empty(t, (&configuration{MinimumAppVersion: value}).appMinimumProblem(), value)
		require.Empty(t, (&configuration{MinimumAppVersion: value}).appMinimum(), "the setting's own version")
	}
	require.Equal(t, "“lates” in Minimum Matty version is not a version such as 0.1.32, so no minimum is in force.",
		(&configuration{MinimumAppVersion: " lates "}).appMinimumProblem())
}

// ---------------------------------------------------------------- looking

// The look asks the app's repository with the plugin's own User-Agent and
// no cookie, uses the tag, whether it is a draft or a prerelease and the
// names of its files, and keeps only the version, when it looked and the
// last trouble. It wants the app among the files, keeps the version seen
// before when it fails, takes an older one, and treats a 404 as nothing
// known until a version was, and then as trouble that keeps it.
func TestTheAppLookReadsOnlyWhatItNeedsAndKeepsTheLastVersion(t *testing.T) {
	l := newLatestScene(t)
	p := l.latestNode(false, "latest")
	require.Eventually(t, func() bool { return l.github.appAsked() == 1 }, 5*time.Second, 5*time.Millisecond, "the look at a start with nothing known")
	require.Eventually(t, func() bool { return l.stored().LookedAt != 0 }, 5*time.Second, 5*time.Millisecond)
	require.Equal(t, appLatestSeen{LookedAt: l.clock().UnixMilli()}, l.stored(), "a 404 with nothing known is nothing known, and no trouble")
	require.Empty(t, p.appMinimumInForce())

	request := l.github.asked()[0]
	require.Equal(t, mediaUserAgent, request.Header.Get("User-Agent"))
	require.Equal(t, "application/vnd.github+json", request.Header.Get("Accept"))
	require.Empty(t, request.Header.Get("Cookie"))
	require.Equal(t, http.MethodGet, request.Method)

	steps := []struct {
		name    string
		answer  func()
		version string
		trouble string
	}{
		{"a release with the app", func() { l.github.appRelease("v0.1.33", false, false, "matty-0.1.33.apk", "matty-0.1.33.apk.sig") }, "0.1.33", ""},
		{"a newer one without the app", func() {
			l.github.appRelease("v0.1.34", false, false, "matty-0.1.34-debug.apk", "matty-0.1.33.apk", "matty-0.1.34.aab")
		}, "0.1.33", "The newest release of Matty holds no app to install yet."},
		{"GitHub failing", func() {
			l.github.route(appLatestPath, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
		}, "0.1.33", "GitHub answered 503."},
		{"a redirect, never followed", func() {
			l.github.route(appLatestPath, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/elsewhere", http.StatusFound) })
		}, "0.1.33", "GitHub answered 302."},
		{"an answer over the size limit", func() {
			huge := `{"tag_name":"v0.1.40","assets":[{"name":"matty-0.1.40.apk"}],"body":"` + strings.Repeat("x", updateLookMaxBytes) + `"}`
			l.github.route(appLatestPath, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(huge)) })
		}, "0.1.33", "GitHub's answer was larger than 256 KB."},
		{"a tag that is not a version", func() { l.github.appRelease("0.1.35", false, false, "matty-0.1.35.apk") }, "0.1.33", "The newest release of Matty has a tag that is not a version such as v0.1.33."},
		{"an answer that is not JSON", func() {
			l.github.route(appLatestPath, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) })
		}, "0.1.33", "GitHub's answer could not be read."},
		{"an older release with the app", func() { l.github.appRelease("v0.1.32", false, false, "matty-0.1.32.apk") }, "0.1.32", ""},
		{"a 404 once a version was known", func() {
			l.github.route(appLatestPath, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
		}, "0.1.32", "GitHub says Matty has no release."},
		{"a draft", func() { l.github.appRelease("v0.1.36", true, false, "matty-0.1.36.apk") }, "0.1.32", "GitHub says Matty has no release."},
		{"a prerelease", func() { l.github.appRelease("v0.1.36", false, true, "matty-0.1.36.apk") }, "0.1.32", "GitHub says Matty has no release."},
		{"the app again", func() { l.github.appRelease("v0.1.36", false, false, "matty-0.1.36.apk") }, "0.1.36", ""},
	}
	elsewhere, _ := json.Marshal(map[string]any{"tag_name": "v0.1.40", "assets": []map[string]any{{"name": "matty-0.1.40.apk"}}})
	l.github.route("/elsewhere", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(elsewhere) })
	for _, step := range steps {
		l.advance(time.Hour)
		step.answer()
		lookLatest(p)

		want := appLatestSeen{Version: step.version, LookedAt: l.clock().UnixMilli()}
		if step.trouble != "" {
			want.Trouble, want.TroubleAt = step.trouble, l.clock().UnixMilli()
		}
		require.Equal(t, want, l.stored(), step.name)
		require.Equal(t, want, p.appLatestNow(), step.name)
		require.Equal(t, step.version, p.appMinimumInForce(), step.name)
	}

	require.Zero(t, l.github.askedAt("/elsewhere"), "a redirect's target is never asked")

	// Only the four fields are kept: nothing of the answer's other fields.
	raw := string(l.kv(kvAppLatestSeen))
	for _, word := range []string{"elsewhere", "Markdown", "notes", "main", "published"} {
		require.NotContains(t, raw, word)
	}

	// GitHub out of reach.
	l.github.server.Close()
	l.advance(time.Hour)
	lookLatest(p)
	require.Equal(t, appLatestSeen{Version: "0.1.36", LookedAt: l.clock().UnixMilli(), Trouble: "GitHub could not be reached.", TroubleAt: l.clock().UnixMilli()}, l.stored())
	for _, line := range l.warnings(p) {
		require.NotContains(t, line, "127.0.0.1", "the log says the failure's kind only")
	}
}

// The look of the app and the plugin's are two: two jobs, two pairs of
// claims, and neither skips the other.
func TestTheAppLookHasItsOwnJobAndClaims(t *testing.T) {
	require.Equal(t, "app_latest", appLatestJob)
	require.Equal(t, "plugin_updates", updateLookJob)
	require.Equal(t, "app_latest_claim", kvAppLatestClaim)
	require.Equal(t, "app_latest_check_claim", kvAppLatestCheckClaim)
	require.Equal(t, "app_latest_seen", kvAppLatestSeen)
	for _, key := range []string{kvUpdateLookClaim, kvUpdateCheckClaim, kvUpdateSeen} {
		require.NotContains(t, []string{kvAppLatestClaim, kvAppLatestCheckClaim, kvAppLatestSeen}, key)
	}

	l := newLatestScene(t)
	l.github.appRelease("v0.1.33", false, false, "matty-0.1.33.apk")
	l.github.releases("v"+testNewer, false, false, updateAssetName(testNewer, testPlatform))
	p := l.latestNode(true, "latest")
	q := l.latestNode(true, "latest")
	require.Eventually(t, func() bool { return l.github.appAsked() == 1 }, 5*time.Second, 5*time.Millisecond, "the start's look, once for the whole server")
	made, _ := l.jobsMade()
	require.Equal(t, 2, made, "each node's daily job of its own")

	// The plugin's claims held: the app's look still looks.
	l.advance(2 * time.Minute)
	require.Equal(t, http.StatusOK, l.checkNow(p, "uid-alice").Code)
	p.lookClaimed(context.Background())
	asked := l.github.appAsked()
	p.lookForAppLatestClaimed(context.Background())
	require.Equal(t, asked+1, l.github.appAsked(), "the plugin's claims do not skip the app's look")
	require.Equal(t, http.StatusOK, l.checkLatest(q, "uid-alice").Code, "nor Check now for the app")

	// The app's claims held: the plugin's look still looks.
	l.advance(2 * time.Minute)
	q.lookForAppLatestClaimed(context.Background())
	require.Equal(t, http.StatusOK, l.checkLatest(p, "uid-alice").Code)
	plugin := l.github.pluginAsked()
	q.lookClaimed(context.Background())
	require.Equal(t, plugin+1, l.github.pluginAsked(), "the app's claims do not skip the plugin's look")
	require.Equal(t, http.StatusOK, l.checkNow(q, "uid-alice").Code, "nor Check now for the plugin")

	// The daily look and the look at a save share one claim: a second
	// within the minute is skipped.
	asked = l.github.appAsked()
	p.lookForAppLatestClaimed(context.Background())
	require.Equal(t, asked, l.github.appAsked())
}

// A node's copy is taken from the store again once it is five seconds old,
// so another node's look reaches it within seconds; a store that cannot be
// read leaves the copy as it is.
func TestANodesCopyIsReadAgainAfterFiveSeconds(t *testing.T) {
	l := newLatestScene(t)
	l.github.appRelease("v0.1.33", false, false, "matty-0.1.33.apk")
	p := l.latestNode(false, "latest")
	require.Eventually(t, func() bool { return l.stored().Version == "0.1.33" }, 5*time.Second, 5*time.Millisecond)
	q := l.latestNode(false, "latest")
	require.Equal(t, "0.1.33", q.appMinimumInForce())

	l.advance(time.Hour)
	require.Equal(t, "0.1.33", q.appMinimumInForce(), "read now")
	l.github.appRelease("v0.1.34", false, false, "matty-0.1.34.apk")
	lookLatest(p)
	require.Equal(t, "0.1.34", p.appMinimumInForce(), "the node that looked")
	require.Equal(t, "0.1.33", q.appMinimumInForce(), "read less than five seconds ago")
	l.advance(4 * time.Second)
	require.Equal(t, "0.1.33", q.appMinimumInForce())
	l.advance(time.Second)
	require.Equal(t, "0.1.34", q.appMinimumInForce(), "five seconds on")

	l.advance(time.Hour)
	require.Equal(t, "0.1.34", q.appMinimumInForce())
	l.github.appRelease("v0.1.35", false, false, "matty-0.1.35.apk")
	lookLatest(p)
	l.failReads(kvAppLatestSeen, true)
	l.advance(10 * time.Second)
	require.Equal(t, "0.1.34", q.appMinimumInForce(), "a store that cannot be read leaves the copy")
	l.failReads(kvAppLatestSeen, false)
	l.advance(10 * time.Second)
	require.Equal(t, "0.1.35", q.appMinimumInForce())
}

// The app's repository is asked only while the setting is latest, whatever
// Look for updates says; and with Look for updates off and the setting
// latest, the plugin's repository is never asked.
func TestNothingIsAskedOfTheAppsRepositoryUnlessTheSettingIsLatest(t *testing.T) {
	for _, minimum := range []string{"", "0.1.32", "lates"} {
		l := newLatestScene(t)
		l.github.appRelease("v0.1.33", false, false, "matty-0.1.33.apk")
		l.github.releases("v"+testNewer, false, false, updateAssetName(testNewer, testPlatform))
		p := l.latestNode(true, minimum)
		look(p)
		lookLatest(p)
		p.lookForAppLatestClaimed(context.Background())
		p.appLatestTick()
		refused := l.checkLatest(p, "uid-alice")
		require.Equal(t, http.StatusConflict, refused.Code, minimum)
		require.Equal(t, errorResponse{Error: "Minimum Matty version is not the latest release. Choose it above and save first.", Code: codeConflict}, refusalOf(t, refused))
		if minimum == "0.1.32" {
			require.Equal(t, "0.1.32", p.appMinimumInForce())
		} else {
			require.Empty(t, p.appMinimumInForce(), "%q", minimum)
		}
		time.Sleep(50 * time.Millisecond)
		require.Zero(t, l.github.appAsked(), "%q", minimum)
		require.Nil(t, l.kv(kvAppLatestSeen), "%q", minimum)
		made, _ := l.jobsMade()
		require.Zero(t, made, "no job while the setting is %q", minimum)
	}

	// Look for updates off, the setting latest: only the app's repository.
	l := newLatestScene(t)
	l.github.appRelease("v0.1.33", false, false, "matty-0.1.33.apk")
	l.github.releases("v"+testNewer, false, false, updateAssetName(testNewer, testPlatform))
	p := l.latestNode(false, "latest")
	require.Eventually(t, func() bool { return l.github.appAsked() == 1 }, 5*time.Second, 5*time.Millisecond)
	look(p)
	p.lookClaimed(context.Background())
	l.advance(2 * time.Minute)
	require.Equal(t, http.StatusOK, l.checkLatest(p, "uid-alice").Code)
	require.Zero(t, l.github.pluginAsked())
	for _, request := range l.github.asked() {
		require.Equal(t, appLatestPath, request.URL.Path)
	}

	// The setting no longer latest: the job stops and nothing more is
	// asked.
	latest := p.getConfiguration()
	none := latest.Clone()
	none.MinimumAppVersion = "0.1.32"
	p.setConfiguration(none)
	p.appLatestChanged(latest, none)
	made, closed := l.jobsMade()
	require.Equal(t, made, closed)
	require.False(t, p.appLatest().onNow())
	asked := l.github.appAsked()
	l.advance(2 * time.Minute)
	lookLatest(p)
	p.lookForAppLatestClaimed(context.Background())
	require.Equal(t, http.StatusConflict, l.checkLatest(p, "uid-alice").Code)
	require.Equal(t, asked, l.github.appAsked())

	// Latest again by a save: the job, and one look at once.
	p.setConfiguration(latest)
	p.appLatestChanged(none, latest)
	require.True(t, p.appLatest().onNow())
	require.Eventually(t, func() bool { return l.github.appAsked() == asked+1 }, 5*time.Second, 5*time.Millisecond)
	made, _ = l.jobsMade()
	require.Equal(t, 2, made)
}

// At a start with the setting latest and a version already known, nothing
// is asked until the daily job's turn.
func TestAStartWithAVersionKnownAsksNothing(t *testing.T) {
	l := newLatestScene(t)
	l.github.appRelease("v0.1.33", false, false, "matty-0.1.33.apk")
	raw, err := json.Marshal(appLatestSeen{Version: "0.1.32", LookedAt: 1})
	require.NoError(t, err)
	l.setKV(kvAppLatestSeen, raw)

	p := l.latestNode(false, "latest")
	time.Sleep(100 * time.Millisecond)
	require.Zero(t, l.github.appAsked())
	require.Equal(t, "0.1.32", p.appMinimumInForce())
}

// A look that changes the latest ends the voice of a Matty below it on
// every node and tells every open client once, from the node that looked;
// another node learns of it from the store, ends voice itself, and tells
// nobody. A look that finds what is already kept tells nobody either.
func TestALookThatChangesTheLatestEndsVoiceAndTellsEverybodyOnce(t *testing.T) {
	l := newLatestScene(t)
	l.github.appRelease("v0.1.31", false, false, "matty-0.1.31.apk")
	here, there := minimumScene{newTalkScene(t)}, minimumScene{newTalkScene(t)}
	p := l.latestNodeWith(false, "latest", withTalk(here))
	require.Eventually(t, func() bool { return l.toldMe(p) == 1 }, 5*time.Second, 5*time.Millisecond, "nothing known before, so a change")
	require.Equal(t, "0.1.31", l.stored().Version)
	q := l.latestNodeWith(false, "latest", withTalk(there))
	require.Equal(t, "0.1.31", q.appMinimumInForce())
	alice := here.joinFrom(talkAlice, mattyOlder)
	bob := here.joinFrom(talkBob, mattyEqual)
	carol := there.joinFrom(talkCarol, mattyOlder)
	dave := there.joinFrom("uid-dave", browserAgent)

	l.advance(time.Hour)
	l.github.appRelease("v0.1.32", false, false, "matty-0.1.32.apk")
	lookLatest(p)
	require.Eventually(t, alice.isClosed, 5*time.Second, 5*time.Millisecond)
	end, _ := alice.ended()
	assert.Equal(t, voiceEndSignal{End: voiceEndOff, Reason: "This server needs Matty 0.1.32 or newer. You have 0.1.31."}, end)
	require.Eventually(t, func() bool { return l.toldMe(p) == 2 }, 5*time.Second, 5*time.Millisecond, "by the node whose look changed it")
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, 2, l.toldMe(p), "once")
	assert.False(t, bob.isClosed())

	// The other node, a minute on, without anybody asking.
	time.Sleep(50 * time.Millisecond)
	assert.False(t, carol.isClosed(), "not before it learns of it")
	l.advance(time.Minute)
	q.appLatestTick()
	require.Eventually(t, carol.isClosed, 5*time.Second, 5*time.Millisecond)
	end, _ = carol.ended()
	assert.Equal(t, voiceEndSignal{End: voiceEndOff, Reason: "This server needs Matty 0.1.32 or newer. You have 0.1.31."}, end)
	assert.False(t, dave.isClosed(), "does not say it is Matty")
	assert.Zero(t, l.toldMe(q), "a node that learnt it from the store tells nobody")

	// The same version again: no change, nobody told.
	l.advance(time.Hour)
	lookLatest(p)
	lookLatest(q)
	require.Equal(t, 2, l.toldMe(p))
	require.Zero(t, l.toldMe(q))

	// A look by the other node that finds what the first already kept:
	// its copy changes, so it ends voice, and the store does not, so it
	// tells nobody.
	l.advance(time.Hour)
	l.github.appRelease("v0.1.40", false, false, "matty-0.1.40.apk")
	erin := there.joinFrom("uid-erin", mattyEqual)
	lookLatest(p)
	require.Equal(t, 3, l.toldMe(p))
	lookLatest(q)
	require.Eventually(t, erin.isClosed, 5*time.Second, 5*time.Millisecond)
	assert.Zero(t, l.toldMe(q))
}

// Check now for the latest: system admins only, POST, only while the
// setting is latest, at most once a minute for the whole server with the
// time left, and 503 when the store does not answer. It answers with the
// app check's card.
func TestCheckNowForTheLatest(t *testing.T) {
	l := newLatestScene(t)
	l.github.appRelease("v0.1.33", false, false, "matty-0.1.33.apk")
	p := l.latestNode(false, "latest")
	q := l.latestNode(false, "latest")
	require.Eventually(t, func() bool { return l.github.appAsked() == 1 }, 5*time.Second, 5*time.Millisecond)

	refused := l.checkLatest(p, "uid-carol")
	require.Equal(t, http.StatusForbidden, refused.Code)
	require.Equal(t, "no-store", refused.Header().Get("Cache-Control"))
	wrong := httptest.NewRecorder()
	p.handleAdminAppLatestCheck(wrong, httptest.NewRequest(http.MethodGet, "/api/v1/admin/appcheck/latest", nil), "uid-alice")
	require.Equal(t, http.StatusMethodNotAllowed, wrong.Code)
	require.Equal(t, 1, l.github.appAsked())

	l.advance(time.Hour)
	l.github.appRelease("v0.1.34", false, false, "matty-0.1.34.apk")
	answer := l.checkLatest(p, "uid-alice")
	require.Equal(t, http.StatusOK, answer.Code, answer.Body.String())
	require.Equal(t, "no-store", answer.Header().Get("Cache-Control"))
	var view appCheckAdminView
	require.NoError(t, json.Unmarshal(answer.Body.Bytes(), &view))
	require.Equal(t, "0.1.34", view.Minimum)
	require.Equal(t, appMinimumFromLatest, view.MinimumFrom)
	require.Equal(t, &appLatestSeen{Version: "0.1.34", LookedAt: l.clock().UnixMilli()}, view.Latest)
	require.Equal(t, 2, l.github.appAsked())

	// Within the minute, from any node: the time left.
	l.advance(20 * time.Second)
	tooSoon := l.checkLatest(q, "uid-bob")
	require.Equal(t, http.StatusTooManyRequests, tooSoon.Code)
	var body errorResponse
	require.NoError(t, json.Unmarshal(tooSoon.Body.Bytes(), &body))
	require.Equal(t, errorResponse{Error: "Somebody looked less than a minute ago. Try again in a minute.", Code: codeRateLimited, RetryAfterMs: 40000}, body)
	l.advance(39500 * time.Millisecond)
	require.NoError(t, json.Unmarshal(l.checkLatest(q, "uid-bob").Body.Bytes(), &body))
	require.Equal(t, int64(1000), body.RetryAfterMs, "at least a second")
	require.Equal(t, 2, l.github.appAsked())

	// The store not answering the claim.
	l.advance(time.Minute)
	l.failReads(kvAppLatestCheckClaim, true)
	broken := l.checkLatest(q, "uid-bob")
	require.Equal(t, http.StatusServiceUnavailable, broken.Code)
	require.Equal(t, errorResponse{Error: "This server could not read how the latest Matty stands just now. Try again in a moment.", Code: accessCodeStore}, refusalOf(t, broken))
	require.Equal(t, 2, l.github.appAsked())
	l.failReads(kvAppLatestCheckClaim, false)
	require.Equal(t, http.StatusOK, l.checkLatest(q, "uid-bob").Code)
	require.Equal(t, 3, l.github.appAsked())
}

// The route is POST /api/v1/admin/appcheck/latest, for system admins.
func TestCheckNowForTheLatestIsRouted(t *testing.T) {
	a := newAppCheckScene(t, appCheckReport)
	a.setMinimum("0.1.32")

	refused := a.call(http.MethodPost, adminAppCheck+"/latest", sceneLinked, davesLaptop, browserAgent, nil)
	require.Equal(t, http.StatusForbidden, refused.Code, refused.Body.String())
	conflict := a.call(http.MethodPost, adminAppCheck+"/latest", sceneLockedAdmin, "", browserAgent, nil)
	require.Equal(t, http.StatusConflict, conflict.Code, conflict.Body.String())
	require.Equal(t, codeConflict, refusalOf(t, conflict).Code)
}

// ---------------------------------------------------------------- in force

func (a *appCheckScene) keepLatest(t *testing.T, seen appLatestSeen) {
	raw, err := json.Marshal(seen)
	require.NoError(t, err)
	a.faults.lock.Lock()
	a.store[kvAppLatestSeen] = raw
	a.faults.lock.Unlock()
	a.p.appLatest().forget()
}

func (a *appCheckScene) adminCard(t *testing.T) appCheckAdminView {
	t.Helper()

	card := a.call(http.MethodGet, adminAppCheck, sceneLockedAdmin, "", browserAgent, nil)
	require.Equal(t, http.StatusOK, card.Code, card.Body.String())
	var view appCheckAdminView
	require.NoError(t, json.Unmarshal(card.Body.Bytes(), &view))

	return view
}

// With the setting latest, the version this node knows is the minimum in
// force: in the gate, in /access and /me, and on the card, which says where
// it comes from and how the last look went. With none known nobody is
// refused.
func TestTheLatestIsTheMinimumInForce(t *testing.T) {
	a := newAppCheckScene(t, appCheckReport)
	a.setMinimum(" Latest ")

	// Nothing known.
	require.Equal(t, http.StatusOK, a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyNoWord, nil).Code)
	require.Equal(t, map[string]any{"check": "report", "state": "none"}, a.appWordOf(t, "/api/v1/access", sceneLinked, davesPhone))
	view := a.adminCard(t)
	require.Empty(t, view.Minimum)
	require.Empty(t, view.Problem)
	require.Equal(t, appMinimumFromLatest, view.MinimumFrom)
	require.Equal(t, &appLatestSeen{}, view.Latest)

	// Known.
	a.keepLatest(t, appLatestSeen{Version: "0.1.32", LookedAt: 1700, Trouble: "GitHub answered 503.", TroubleAt: 1800})
	refused := a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyOlder, nil)
	require.Equal(t, http.StatusForbidden, refused.Code)
	require.Equal(t, errorResponse{Error: "This server needs Matty 0.1.32 or newer. You have 0.1.31.", Code: codeAppOutdated}, refusalOf(t, refused))
	require.Equal(t, http.StatusOK, a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyEqual, nil).Code)
	for _, path := range []string{"/api/v1/access", "/api/v1/me"} {
		require.Equal(t, map[string]any{"check": "report", "state": "none", "minimum": "0.1.32"}, a.appWordOf(t, path, sceneLinked, davesPhone), path)
	}
	view = a.adminCard(t)
	require.Equal(t, "0.1.32", view.Minimum)
	require.Equal(t, appMinimumFromLatest, view.MinimumFrom)
	require.Equal(t, &appLatestSeen{Version: "0.1.32", LookedAt: 1700, Trouble: "GitHub answered 503.", TroubleAt: 1800}, view.Latest)

	// A version in the setting: from the setting, and no latest.
	a.setMinimum("0.1.30")
	view = a.adminCard(t)
	require.Equal(t, "0.1.30", view.Minimum)
	require.Equal(t, appMinimumFromSetting, view.MinimumFrom)
	require.Nil(t, view.Latest)
	require.Equal(t, http.StatusOK, a.call(http.MethodGet, "/api/v1/streams", sceneLinked, davesPhone, mattyOlder, nil).Code)

	// None: neither.
	a.setMinimum("")
	view = a.adminCard(t)
	require.Empty(t, view.MinimumFrom)
	require.Nil(t, view.Latest)
	raw := a.call(http.MethodGet, adminAppCheck, sceneLockedAdmin, "", browserAgent, nil).Body.String()
	require.NotContains(t, raw, "minimumFrom")
	require.NotContains(t, raw, `"latest"`)
}

// A join reads the minimum in force, the latest included.
func TestAVoiceJoinMeetsTheLatest(t *testing.T) {
	p, store, faults := kvBackedPluginWithFaultsOn(map[string][]byte{}, newKVFaults())
	p.setConfiguration(&configuration{MinimumAppVersion: "latest"})
	raw, err := json.Marshal(appLatestSeen{Version: "0.1.32", LookedAt: 1})
	require.NoError(t, err)
	faults.lock.Lock()
	store[kvAppLatestSeen] = raw
	faults.lock.Unlock()

	s := minimumScene{newTalkScene(t)}
	s.r.minimum = p.appMinimumInForce
	join := func(userID, agent string) error {
		ask := s.ask(userID, 1, false, false)
		ask.Matty, ask.Version = mattyOf(agent)
		_, err := s.r.join(context.Background(), ask, s.dial, func(*voiceSession) (voicePeer, error) { return newFakeVoicePeer(4), nil })
		return err
	}

	assert.Equal(t, &voiceRefusal{Status: http.StatusForbidden, Code: codeAppOutdated, Sentence: "This server needs Matty 0.1.32 or newer. You have 0.1.31."}, refusal(t, join(talkAlice, mattyOlder)))
	require.NoError(t, join(talkBob, mattyEqual))
	require.NoError(t, join(talkCarol, browserAgent))
}

// A save that changes the minimum in force tells every open client, as
// before, the latest counted as the version this node knows: from a version
// to the latest that is the same version is no change.
func TestASaveToTheLatestTellsEverybodyWhenTheMinimumInForceChanges(t *testing.T) {
	const site = "http://localhost:8065"
	w, save := appCheckSaves(t)
	raw, err := json.Marshal(appLatestSeen{Version: "0.1.33", LookedAt: 1})
	require.NoError(t, err)
	w.lock.Lock()
	w.kv[kvAppLatestSeen] = raw
	w.lock.Unlock()
	settings := func(minimum string) map[string]any {
		return map[string]any{"appcheck": "report", "minimumappversion": minimum}
	}

	setActivation(w.p, true)
	require.Zero(t, save(settings(""), site), "the first load")
	require.Equal(t, 1, save(settings("latest"), site), "the latest, known")
	require.Zero(t, save(settings("0.1.33"), site), "the same version, from the setting")
	require.Zero(t, save(settings(" LATEST "), site), "and back")
	require.Equal(t, 1, save(settings("0.1.32"), site))
	require.Equal(t, 1, save(settings(""), site))
}

// forget makes the node's copy due to be read from the store again.
func (r *appLatestRuntime) forget() {
	r.mu.Lock()
	r.readAt = time.Time{}
	r.mu.Unlock()
}

// The plugin's stop ends the minute's read and cuts a look under way short:
// the stop does not wait its five seconds, and a look that was stopped
// keeps nothing.
func TestTheStopEndsTheMinutesReadAndALookUnderWay(t *testing.T) {
	l := newLatestScene(t)
	l.github.appRelease("v0.1.33", false, false, "matty-0.1.33.apk")
	p := l.latestNode(false, "latest")
	require.Eventually(t, func() bool { return l.stored().Version == "0.1.33" }, 5*time.Second, 5*time.Millisecond)
	require.True(t, p.appLatest().onNow())

	entered := make(chan struct{}, 1)
	l.github.route(appLatestPath, func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-r.Context().Done()
	})
	l.advance(time.Hour)
	require.True(t, p.appLatest().spawn(func(ctx context.Context) { p.lookForAppLatest(ctx) }))
	<-entered

	started := time.Now()
	p.stopAppLatest()
	require.Less(t, time.Since(started), 2*time.Second, "the stop waited for something that does not end")
	for _, line := range l.warnings(p) {
		require.NotContains(t, line, "Stopped waiting")
	}
	require.Equal(t, "0.1.33", l.stored().Version)
	require.Empty(t, l.stored().Trouble, "a look that was stopped is no look")
}

// The setting no longer latest: the minute's read ends on this node.
func TestTheMinutesReadEndsWithTheSetting(t *testing.T) {
	l := newLatestScene(t)
	l.github.appRelease("v0.1.33", false, false, "matty-0.1.33.apk")
	p := l.latestNode(false, "latest")
	require.Eventually(t, func() bool { return l.stored().Version == "0.1.33" }, 5*time.Second, 5*time.Millisecond)

	latest := p.getConfiguration()
	none := latest.Clone()
	none.MinimumAppVersion = ""
	p.setConfiguration(none)
	p.appLatestChanged(latest, none)

	done := make(chan struct{})
	go func() { p.appLatest().wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a goroutine of the latest still runs with the setting off")
	}
}
