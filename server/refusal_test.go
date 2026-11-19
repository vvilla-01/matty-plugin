package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// A refusal is one body on every route: a sentence, a code, and with a
// refusal that is about time, how long to wait (errorResponse). These hold
// that for the routes both clients call; contract/README.md says what each
// code means.

// refusalOf is a refusal as a client decodes one: exactly the three fields,
// and the first two never empty.
func refusalOf(t *testing.T, recorder *httptest.ResponseRecorder) errorResponse {
	t.Helper()

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &fields), recorder.Body.String())
	for name := range fields {
		require.Contains(t, []string{"error", "code", "retryAfterMs"}, name, recorder.Body.String())
	}

	var refusal errorResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &refusal))
	require.NotEmpty(t, refusal.Error, "a sentence: %s", recorder.Body.String())
	require.NotEmpty(t, refusal.Code, "a code: %s", recorder.Body.String())

	return refusal
}

// No refusal goes out without a code: one whose handler names none has its
// status's.
func TestEveryRefusalHasACode(t *testing.T) {
	for status, code := range map[int]string{
		http.StatusBadRequest:            codeBadRequest,
		http.StatusUnauthorized:          codeSignedOut,
		http.StatusForbidden:             codeForbidden,
		http.StatusNotFound:              codeNotFound,
		http.StatusMethodNotAllowed:      codeMethodNotAllowed,
		http.StatusConflict:              codeConflict,
		http.StatusGone:                  codeGone,
		http.StatusRequestEntityTooLarge: codeTooLarge,
		http.StatusTooManyRequests:       codeRateLimited,
		http.StatusInternalServerError:   codeFailed,
		http.StatusBadGateway:            codeUnreachable,
		http.StatusServiceUnavailable:    codeUnavailable,
		http.StatusTeapot:                codeFailed,
	} {
		plain := httptest.NewRecorder()
		writeError(plain, status, "A sentence.")
		require.Equal(t, errorResponse{Error: "A sentence.", Code: code}, refusalOf(t, plain), status)

		// A handler that passes an empty code by mistake still sends one.
		unnamed := httptest.NewRecorder()
		writeCodedError(unnamed, status, "A sentence.", "")
		require.Equal(t, code, refusalOf(t, unnamed).Code, status)
	}

	named := httptest.NewRecorder()
	writeCodedError(named, http.StatusForbidden, "A sentence.", accessCodeLocked)
	require.Equal(t, accessCodeLocked, refusalOf(t, named).Code, "a handler's own code is kept")

	// As the routes answer: nobody signed in, a path that is no route, a
	// method a route does not take, in the plain routes and in the clips'.
	s := newAccessScene(t)
	for _, c := range []struct {
		method, target, userID string
		status                 int
		code                   string
	}{
		{http.MethodGet, "/api/v1/streams", "", http.StatusUnauthorized, codeSignedOut},
		{http.MethodGet, "/api/v1/nowhere", sceneLinked, http.StatusNotFound, codeNotFound},
		{http.MethodPut, "/api/v1/streams", sceneLinked, http.StatusMethodNotAllowed, codeMethodNotAllowed},
		{http.MethodPut, "/api/v1/whep", sceneLinked, http.StatusMethodNotAllowed, codeMethodNotAllowed},
		{http.MethodGet, "/api/v1/clips/nowhere", sceneLinked, http.StatusNotFound, codeNotFound},
		{http.MethodPatch, "/api/v1/clips", sceneLinked, http.StatusMethodNotAllowed, codeMethodNotAllowed},
		{http.MethodGet, "/api/v1/accounts", sceneLocked, http.StatusForbidden, accessCodeLocked},
		{http.MethodPost, "/api/v1/schedule/share", sceneLetIn, http.StatusForbidden, accessCodeWatchOnly},
	} {
		response := s.serve(c.method, c.target, c.userID, "", nil)
		about := c.method + " " + c.target
		require.Equal(t, c.status, response.Code, about)
		require.Equal(t, c.code, refusalOf(t, response).Code, about)
	}
}

// What a link that is refused says is the server's to word: the sentences
// the lock screen and Matty used to keep a table of each, now sent. Each with
// its code, which is what a client acts on.
func TestALinkIsRefusedInTheServersOwnWords(t *testing.T) {
	s := newAccessScene(t)
	fake := newFakeBroadcastBox(t, map[string]string{dansToken: "videokey-dan", "bad-name-token": "not a usable name!"})
	s.pointedAt(fake)

	refused := func(response *httptest.ResponseRecorder, status int, code, sentence string) {
		t.Helper()

		require.Equal(t, status, response.Code, response.Body.String())
		require.Equal(t, errorResponse{Error: sentence, Code: code}, refusalOf(t, response))
	}

	refused(s.serve(http.MethodPost, "/api/v1/me/link", sceneLocked, "{not json", nil),
		http.StatusBadRequest, codeBadRequest, "That did not get through. Try again.")
	refused(s.link(sceneLocked, "  "),
		http.StatusBadRequest, codeBadRequest, "That did not get through. Try again.")
	refused(s.link(sceneLocked, strings.Repeat("x", 257)),
		http.StatusBadRequest, "too_long", "That is too long to be a bearer token.")
	refused(s.link(sceneLocked, "nobodys-token"),
		http.StatusForbidden, "not_accepted", "That token was not accepted. Check it and try again.")

	// A meter of its own from here, so the refusals above do not count
	// against the ones below.
	s.p.linkLimiter = newRateLimiter(5, 15*time.Minute)
	refused(s.link(sceneLocked, "bad-name-token"),
		http.StatusBadGateway, "bad_name", "Your token was accepted, but its stream name can’t be used here. Ask an admin to look at it.")

	// Broadcast Box not answering: said in a sentence that names no address
	// and no setting, whatever went wrong behind it.
	fake.server.Close()
	unreachable := s.link(sceneLocked, dansToken)
	refused(unreachable, http.StatusBadGateway, codeUnreachable,
		"Broadcast Box is not answering, so your token can’t be checked. Try again later.")
	require.NotContains(t, unreachable.Body.String(), "127.0.0.1")

	// Not set up: the sentence is the lock's own, not which setting is missing.
	cfg := s.p.getConfiguration().Clone()
	cfg.BroadcastBoxURL = ""
	s.p.setConfiguration(cfg)
	refused(s.link(sceneLocked, dansToken),
		http.StatusServiceUnavailable, codeNotConfigured, linkNotSetUp(productName))
}

// A meter's refusal says how long until asking again can work, read from the
// meter itself. Five links a quarter of an hour is one every three minutes.
func TestAMetersRefusalSaysHowLongToWait(t *testing.T) {
	s := newAccessScene(t)
	fake := newFakeBroadcastBox(t, map[string]string{dansToken: "videokey-dan"})
	s.pointedAt(fake)

	for range 5 {
		require.Equal(t, http.StatusForbidden, s.link(sceneLocked, "nobodys-token").Code)
	}

	response := s.link(sceneLocked, dansToken)
	require.Equal(t, http.StatusTooManyRequests, response.Code)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

	refusal := refusalOf(t, response)
	require.Equal(t, codeRateLimited, refusal.Code)
	require.Equal(t, "Too many tries. Wait a few minutes, then try again.", refusal.Error)
	require.Greater(t, refusal.RetryAfterMs, int64(170_000), "the best part of three minutes")
	require.LessOrEqual(t, refusal.RetryAfterMs, int64(180_000))

	// Somebody else is not kept waiting, and is told nothing of the kind.
	require.Equal(t, http.StatusForbidden, s.link(sceneLetIn, "nobodys-token").Code)

	// Taking a link back is never metered, so it never has a wait to tell.
	for range 8 {
		require.Equal(t, http.StatusOK, s.unlink(sceneLocked).Code)
	}

	// Playback's meter says its own.
	dave := s.p.streamHandle("videokey-dave")
	s.p.playbackLimiter = newRateLimiter(1, time.Minute)
	require.True(t, s.p.playbackLimiter.allow(sceneLinked))
	playback := s.serve(http.MethodPost, "/api/v1/whep", sceneLinked, viewerOffer, map[string]string{"X-Stream-Handle": dave})
	require.Equal(t, http.StatusTooManyRequests, playback.Code)
	wait := refusalOf(t, playback)
	require.Equal(t, codeRateLimited, wait.Code)
	require.Greater(t, wait.RetryAfterMs, int64(50_000))
	require.LessOrEqual(t, wait.RetryAfterMs, int64(60_000))

	// A wait of nothing is left out, not sent as zero; a wait too short to
	// count is a millisecond, never none.
	require.Zero(t, retryAfterMs(0))
	require.Zero(t, retryAfterMs(-time.Second))
	require.Equal(t, int64(1), retryAfterMs(time.Microsecond))
	require.Equal(t, int64(1500), retryAfterMs(1500*time.Millisecond))

	idle := httptest.NewRecorder()
	writeTooMany(idle, "A sentence.", newRateLimiter(1, time.Minute), "nobody", time.Now())
	require.NotContains(t, idle.Body.String(), "retryAfterMs")
}

// A clips meter says the same: the three fields, under the same names.
func TestAClipsMeterSaysHowLongToWait(t *testing.T) {
	w, _ := startedClipsWorld(t, "uid-bob", "uid-dave")
	clipsAdmins(w.api)
	publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil), true)

	vote := func() *httptest.ResponseRecorder {
		return w.call(t, "uid-dave", clipCall{method: http.MethodPost, target: clipsPath + "/vote?id=" + clipA, body: `{"up":true}`})
	}

	last := vote()
	for tries := 0; last.Code != http.StatusTooManyRequests; tries++ {
		require.Less(t, tries, 200, "the meter never said no")
		require.Equal(t, http.StatusOK, last.Code, last.Body.String())
		last = vote()
	}

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(last.Body.Bytes(), &fields))
	require.Len(t, fields, 3, last.Body.String())

	var refusal clipErrorResponse
	require.NoError(t, json.Unmarshal(last.Body.Bytes(), &refusal))
	require.Equal(t, clipErrRateLimited, refusal.Code)
	require.Equal(t, clipErrorTexts[clipErrRateLimited], refusal.Error)
	require.Positive(t, refusal.RetryAfterMs)
	require.Equal(t, "no-store", last.Header().Get("Cache-Control"))

	// A refusal that is not about time has no wait in it.
	gone := w.call(t, "uid-dave", clipCall{method: http.MethodGet, target: clipsPath + "/clip?id=" + clipB})
	require.Equal(t, http.StatusGone, gone.Code)
	require.NotContains(t, gone.Body.String(), "retryAfterMs")
}

// Joining a plan: each way it can be refused has its own code, so that a
// client can tell a plan that filled up from one that went.
func TestAPlanRefusalSaysWhich(t *testing.T) {
	p, store := communityPlugin(t)
	require.NoError(t, p.addScheduleEntry("villa", "Big night", "", inAnHour(), nowMillis()))
	plan := onlyPlan(t, p)

	join := func(who, id string) errorResponse {
		t.Helper()

		response := postPlanChange(t, p, "/api/v1/schedule/join", who, map[string]any{"id": id, "join": true})
		require.NotEqual(t, http.StatusOK, response.Code)

		return refusalOf(t, response)
	}

	require.Equal(t, accessCodeLocked, join("stranger", plan.ID).Code, "nobody without a stream of their own")

	linkStream(store, "villa")
	require.Equal(t, planCodeOwnPlan, join("villa", plan.ID).Code)

	linkStream(store, "late")
	require.Equal(t, planCodeGone, join("late", "0000000000000000").Code)

	for i := range maxScheduleJoined {
		who := fmt.Sprintf("s%d", i)
		linkStream(store, who)
		require.Equal(t, http.StatusOK, postPlanChange(t, p, "/api/v1/schedule/join", who, map[string]any{"id": plan.ID, "join": true}).Code)
	}
	full := join("late", plan.ID)
	require.Equal(t, planCodeFull, full.Code)
	require.Equal(t, "that one is full", full.Error)

	// Somebody else's plan is not one to change.
	edit := postPlanChange(t, p, "/api/v1/schedule/edit", "late", map[string]any{"id": plan.ID, "title": "Mine now", "startsAt": inAnHour()})
	require.Equal(t, http.StatusForbidden, edit.Code)
	require.Equal(t, planCodeNotYours, refusalOf(t, edit).Code)
}

// Playback: a stream that is not there to play is "not_live", which a player
// asks about again; the gate's refusal keeps the gate's code.
func TestAPlaybackRefusalSaysWhich(t *testing.T) {
	s := newAccessScene(t)

	gone := s.serve(http.MethodPost, "/api/v1/whep", sceneLinked, viewerOffer, map[string]string{"X-Stream-Handle": "0123456789abcdef"})
	require.Equal(t, http.StatusNotFound, gone.Code)
	require.Equal(t, errorResponse{Error: "That stream is not live right now.", Code: whepCodeNotLive}, refusalOf(t, gone))

	unnamed := s.serve(http.MethodPost, "/api/v1/whep", sceneLinked, viewerOffer, nil)
	require.Equal(t, http.StatusBadRequest, unnamed.Code)
	require.Equal(t, codeBadRequest, refusalOf(t, unnamed).Code)

	locked := s.serve(http.MethodPost, "/api/v1/whep", sceneLocked, viewerOffer, map[string]string{"X-Stream-Handle": "0123456789abcdef"})
	require.Equal(t, http.StatusForbidden, locked.Code)
	require.Equal(t, accessCodeLocked, refusalOf(t, locked).Code)
}

// Every meter's refusal says how long to wait, because every 429 this server
// writes is written by a helper that reads the meter that refused. A 429
// written any other way has no wait in it (two were: the award picker's and
// the connect ticket's), so the source is held to the helpers.
func TestEveryMetersRefusalIsWrittenWithItsWait(t *testing.T) {
	allowed := []*regexp.Regexp{
		// The helpers themselves.
		regexp.MustCompile(`writeJSON\(w, http\.StatusTooManyRequests, (errorResponse|clipErrorResponse)\{`),
		regexp.MustCompile(`writeSoundRefusal\(w, http\.StatusTooManyRequests, soundCodeRateLimited, .*\.wait\(`),

		// A person's share of voice's budget: the refusal carries its wait,
		// which voiceRefusal.write writes as retryAfterMs.
		regexp.MustCompile(`&voiceRefusal\{Status: http\.StatusTooManyRequests, Code: codeRateLimited, .*RetryAfter: wait\}`),

		// A status looked at, mapped or handed on, never written here.
		regexp.MustCompile(`case http\.StatusTooManyRequests:`),
		regexp.MustCompile(`== http\.StatusTooManyRequests`),
		regexp.MustCompile(`return http\.StatusTooManyRequests`),
		regexp.MustCompile(`accountCodeRateLimited:\s+http\.StatusTooManyRequests,`),
	}

	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	require.NotEmpty(t, files)

	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}

		source, err := os.ReadFile(file)
		require.NoError(t, err)

		for number, line := range strings.Split(string(source), "\n") {
			if strings.Contains(line, "writeAccountCode(") && strings.Contains(line, "accountCodeRateLimited") {
				t.Errorf("%s:%d: a meter's refusal written without its wait: use writeTooMany", file, number+1)
			}
			if !strings.Contains(line, "http.StatusTooManyRequests") {
				continue
			}

			known := false
			for _, pattern := range allowed {
				known = known || pattern.MatchString(line)
			}
			if !known {
				t.Errorf("%s:%d: a 429 written without its wait: use writeTooMany\n\t%s", file, number+1, strings.TrimSpace(line))
			}
		}
	}
}

// The two that were: choosing awards too often, and asking for a connect
// ticket too often.
func TestTheAwardPickersMeterSaysHowLong(t *testing.T) {
	s := newAccessScene(t)
	s.api.On("PublishWebSocketEvent", wsEventStreams, mock.Anything, mock.Anything).Return().Maybe()

	var last *httptest.ResponseRecorder
	for range 11 {
		last = s.serve(http.MethodPost, "/api/v1/awards/display", sceneLinked, `{"show":["month"]}`, nil)
	}
	require.Equal(t, http.StatusTooManyRequests, last.Code)

	var refusal errorResponse
	require.NoError(t, json.Unmarshal(last.Body.Bytes(), &refusal))
	require.Equal(t, codeRateLimited, refusal.Code)
	require.Positive(t, refusal.RetryAfterMs)
	require.LessOrEqual(t, refusal.RetryAfterMs, int64(60_000))
}
