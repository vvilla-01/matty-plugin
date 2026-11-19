package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// panickingBody is a request body whose first read panics with value.
type panickingBody struct{ value any }

func (b panickingBody) Read([]byte) (int, error) { panic(b.value) }

// A request whose handler panics is answered with the plugin's own refusal, a
// 500, and logged by its route with nothing of its body or headers; the plugin
// serves the next request as if nothing had happened. A handler that means to
// abort its response still can.
func TestAPanicServingARequestIsOneRefusal(t *testing.T) {
	s, _ := bannerScene(t)
	s.api.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()

	send := func(value any) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/me/banner", panickingBody{value})
		request.ContentLength = -1
		request.Header.Set("Mattermost-User-Id", bannerMember)
		request.Header.Set("Content-Type", bannerTypePNG)
		request.Header.Set("Authorization", "Bearer a-secret-in-a-header")
		recorder := httptest.NewRecorder()
		s.p.ServeHTTP(nil, recorder, request)

		return recorder
	}

	response := send("the body went wrong")
	requireRefused(t, response, http.StatusInternalServerError, codeFailed, "a panic")
	require.Equal(t, panicRefusalText, refusalOf(t, response).Error)

	logged := logged(s.api, "LogError", panicLogged)
	require.Len(t, logged, 1)
	require.Equal(t, []any{"where", "serving POST /api/v1/me/banner", "panic", "the body went wrong"}, []any(logged[0][1:5]))
	require.Equal(t, "stack", logged[0][5])
	require.Contains(t, logged[0][6], "panickingBody.Read", "with the stack it came from")
	require.NotContains(t, logged[0][6], "a-secret-in-a-header")

	// The next request is served.
	require.Equal(t, http.StatusCreated, uploadBanner(s, bannerMember, bannerTypePNG, bannerPNG(t, 300, 100)).Code)

	// A handler cutting its response short on purpose is not a bug to catch.
	require.PanicsWithValue(t, http.ErrAbortHandler, func() { send(http.ErrAbortHandler) })
	require.Len(t, logged, 1)
}

// testPanicLog counts what is logged through it.
type testPanicLog struct{ errors atomic.Int32 }

func (l *testPanicLog) LogError(string, ...any) { l.errors.Add(1) }

// A pass of a loop that panics is logged and the loop goes on, after the
// pause; a pass that does not panic is not followed by one.
func TestALoopPassThatPanicsRunsAgain(t *testing.T) {
	log := &testPanicLog{}
	passes := 0
	started := time.Now()
	for passes < 3 {
		passSafely(context.Background(), log, "a test loop", 20*time.Millisecond, func() {
			passes++
			if passes == 1 {
				panic("once")
			}
		})
	}
	require.Equal(t, 3, passes)
	require.Equal(t, int32(1), log.errors.Load())
	require.GreaterOrEqual(t, time.Since(started), 20*time.Millisecond, "the pause after the panic")

	// A loop being stopped does not wait the pause out.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started = time.Now()
	passSafely(ctx, log, "a test loop", time.Hour, func() { panic("again") })
	require.Less(t, time.Since(started), time.Minute)
	require.Equal(t, int32(2), log.errors.Load())
}

// A clip ring's writer that panics over a sample starts the ring again, as
// after a write that failed, and records the samples that follow.
func TestAClipWriterThatPanicsCarriesOn(t *testing.T) {
	log := &recorderTestLog{}
	var opens atomic.Int32
	tp := newMediaTestPipe(t, 30*time.Second, func(cfg *pipelineConfig) {
		cfg.log = log
		cfg.openSegment = func(path string) (segmentFile, error) {
			if opens.Add(1) == 1 {
				panic("the first segment")
			}

			return openSegmentFile(path)
		}
	})
	frames := newMediaTimedFrames(t, tp)
	frames.send(300)

	restarts := tp.eventsOf(pipeEventRestarted)
	require.Len(t, restarts, 1)
	require.Equal(t, restartWriteError+": "+recorderPanicReason, restarts[0].reason)
	require.Equal(t, 1, log.count("error", panicLogged))
	require.NotEmpty(t, tp.eventsOf(pipeEventReady), "recording carries on")
	require.Positive(t, tp.live().heldMs())
}

// A clip recorder's reader that panics over what a stream sent ends its
// session as one whose stream cannot be recorded, rather than the plugin.
func TestAClipReaderThatPanicsEndsItsSession(t *testing.T) {
	log := &recorderTestLog{}
	r := &clipRecorder{log: log}
	s := &recorderSession{wake: make(chan struct{}, 1)}

	r.readSafely(s, "a test reader", func() { panic("a packet") })

	require.ErrorIs(t, s.unsupported, errRecorderPanicked)
	require.Equal(t, 1, log.count("error", panicLogged))
}

// What reaches the log of a request is its method and path.
func TestARequestPanicLogsNoQuery(t *testing.T) {
	s, _ := bannerScene(t)
	s.api.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()

	request := httptest.NewRequest(http.MethodPost, "/api/v1/me/banner?token=in-a-query", io.Reader(panickingBody{"x"}))
	request.Header.Set("Mattermost-User-Id", bannerMember)
	request.Header.Set("Content-Type", bannerTypePNG)
	recorder := httptest.NewRecorder()
	s.p.ServeHTTP(nil, recorder, request)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	for _, value := range logged(s.api, "LogError", panicLogged)[0] {
		require.False(t, strings.Contains(value.(string), "in-a-query"))
	}
}

// A panic while a request holds a lock that others wait on lets the lock go:
// Delete all Steam data, which holds the accounts loop's lock and Steam's
// link writes across the store, panics in the store's answer, is one refused
// request, and the next press is served rather than waiting for good.
func TestAPanicUnderARequestsLockLetsItGo(t *testing.T) {
	s := newAccountsScene(t)
	s.api.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return()

	press := func() *httptest.ResponseRecorder {
		return s.serve(http.MethodDelete, "/api/v1/admin/steam?all=1", acBoss, web(acBoss), "", nil)
	}

	hookRead(t, s.p, kvSteamLinks, func() { panic("the store went wrong") })
	requireRefused(t, press(), http.StatusInternalServerError, codeFailed, "a panic")
	require.Len(t, logged(s.api, "LogError", panicLogged), 1)

	served := make(chan int, 1)
	go func() { served <- press().Code }()
	select {
	case code := <-served:
		require.Equal(t, http.StatusOK, code)
	case <-time.After(10 * time.Second):
		t.Fatal("the next press is still waiting for a lock the panic left held")
	}
}

// anyLogError lets the panics these tests cause be logged.
func anyLogError(api *plugintest.API) {
	api.On("LogError", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return().Maybe()
}

// panicsLoggedAt is how many panics were logged as caught at where.
func panicsLoggedAt(api *plugintest.API, where string) int {
	n := 0
	for _, line := range logged(api, "LogError", panicLogged) {
		if len(line) > 2 && line[2] == where {
			n++
		}
	}

	return n
}

// waitOrFail waits for done, failing the test after a few seconds.
func waitOrFail(t *testing.T, done <-chan struct{}, about string) {
	t.Helper()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal(about)
	}
}

// waitGroupOrFail waits for a wait group, failing the test after a few
// seconds.
func waitGroupOrFail(t *testing.T, wait *sync.WaitGroup, about string) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		wait.Wait()
		close(done)
	}()
	waitOrFail(t, done, about)
}

// A delayed accounts event that panics is logged once and goes out no more;
// it leaves neither its lock held nor itself counted as waiting, so the next
// change asks for an event of its own.
func TestADelayedAccountsEventThatPanicsLetsTheNextOneWait(t *testing.T) {
	s := newAccountsScene(t)
	anyLogError(s.api)
	store := s.p.accounts()

	var waits atomic.Int32
	fire := make(chan time.Time)
	store.after = func(time.Duration) <-chan time.Time {
		waits.Add(1)
		return fire
	}
	var broken atomic.Bool
	clock := store.now
	store.now = func() time.Time {
		if broken.Load() {
			panic("the clock went wrong")
		}
		return clock()
	}

	store.announce() // the first of a quiet spell, at once
	store.announce() // the next, after the gap
	require.Equal(t, 1, s.accountsEvents(t))
	require.EqualValues(t, 1, waits.Load())

	broken.Store(true)
	fire <- time.Time{}
	waitGroupOrFail(t, &store.eventWait, "the event that panicked was waited for for good")
	broken.Store(false)
	require.Equal(t, 1, panicsLoggedAt(s.api, "the delayed accounts event"))
	require.Equal(t, 1, s.accountsEvents(t), "nothing went out")

	announced := make(chan struct{})
	go func() {
		store.announce()
		close(announced)
	}()
	waitOrFail(t, announced, "the next change waited on the lock the panic left held")
	require.EqualValues(t, 2, waits.Load(), "the next change waits for an event of its own")
	store.stopEvents()
}

// Work shared by a flight that panics is logged once and answers
// errMediaPanicked to everybody waiting for it, and the next caller runs the
// work afresh.
func TestASharedFetchThatPanicsAnswersEveryWaiter(t *testing.T) {
	var g flightGroup
	log := &testPanicLog{}
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var runs atomic.Int32

	answers := make(chan error, 2)
	ask := func() {
		_, err := g.doDetached(context.Background(), log, "a shared fetch", "key", func() (any, error) {
			runs.Add(1)
			once.Do(func() { close(started) })
			<-release
			panic("the fetch went wrong")
		})
		answers <- err
	}
	go ask()
	<-started
	go ask()
	// A moment for the second caller to join the call under way. Should it
	// come too late it runs the work again, which panics the same way: each
	// caller is still answered, and each run logged once.
	time.Sleep(20 * time.Millisecond)
	close(release)

	for range 2 {
		select {
		case err := <-answers:
			require.ErrorIs(t, err, errMediaPanicked)
		case <-time.After(10 * time.Second):
			t.Fatal("a caller was left waiting for the fetch that panicked")
		}
	}
	require.Equal(t, runs.Load(), log.errors.Load(), "one panic logged for each run of the work")

	value, err := g.doDetached(context.Background(), log, "a shared fetch", "key", func() (any, error) { return "fresh", nil })
	require.NoError(t, err)
	require.Equal(t, "fresh", value)
}

// Either half of a game search that panics is logged once and counts as
// that source failing: the other half answers, with the missing source
// named; both panicking is the search's usual refusal.
func TestAGameSearchHalfThatPanicsIsASourceThatFailed(t *testing.T) {
	for _, tried := range []struct {
		panics     []string
		status     int
		incomplete string
	}{
		{[]string{mediaProviderSteam}, http.StatusOK, mediaSourceSteam},
		{[]string{mediaProviderIGDB}, http.StatusOK, mediaSourceIGDB},
		{[]string{mediaProviderSteam, mediaProviderIGDB}, http.StatusBadGateway, ""},
	} {
		fake := newMediaFake(t)
		steamItems(fake, steamRow(42, "Hytale Modding Kit"))
		fake.igdbToken("tok", 5000000)
		fake.igdbGames(igdbCatalogue(hytaleRow()))

		p, _ := mediaPlugin(t, twitchConfig())
		api, ok := p.API.(*plugintest.API)
		require.True(t, ok)
		anyLogError(api)
		p.mediaStore().searchFault = func(provider string) {
			if slices.Contains(tried.panics, provider) {
				panic(provider + " went wrong")
			}
		}

		answered := make(chan *httptest.ResponseRecorder, 1)
		go func() { answered <- mediaSearch(t, p, "game", "hytale") }()
		var recorder *httptest.ResponseRecorder
		select {
		case recorder = <-answered:
		case <-time.After(10 * time.Second):
			t.Fatalf("%v: the search waited for a half that panicked", tried.panics)
		}

		require.Equal(t, tried.status, recorder.Code, "%v: %s", tried.panics, recorder.Body.String())
		if tried.status == http.StatusOK {
			var found mediaSearchResponse
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &found))
			require.Equal(t, tried.incomplete, found.Incomplete)
			require.NotEmpty(t, found.Results)
		} else {
			requireRefused(t, recorder, http.StatusBadGateway, refusalCodeOf(http.StatusBadGateway), "both halves panicked")
		}
		require.Equal(t, slices.Contains(tried.panics, mediaProviderSteam), panicsLoggedAt(api, "the Steam half of a game search") == 1)
		require.Equal(t, slices.Contains(tried.panics, mediaProviderIGDB), panicsLoggedAt(api, "the IGDB half of a game search") == 1)
		require.Len(t, logged(api, "LogError", panicLogged), len(tried.panics))
	}
}

// The first tidy of the pictures on disk that panics is logged once, is
// waited for no longer than any other, and leaves the write lock free, so
// the next picture is kept.
func TestAFirstPictureTidyThatPanicsLetsTheDiskGoOn(t *testing.T) {
	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	anyLogError(api)
	dir := t.TempDir()
	api.On("GetConfig").Return(clipsStorageConfig(dir, model.ImageDriverLocal))
	clock := newFakeClock()
	useClock(p, clock)

	d := p.mediaDisk()
	d.freeSpace = plentyOfRoom
	d.every = func(_ time.Duration, run func()) clipsJob { return &fakeClipsJob{run: run} }
	var tidies atomic.Int32
	d.tidyFault = func() {
		if tidies.Add(1) == 1 {
			panic("the tidy went wrong")
		}
	}
	p.startMediaDisk(clock.now())
	waitGroupOrFail(t, &d.pending, "the tidy that panicked was waited for for good")
	require.Equal(t, 1, panicsLoggedAt(api, "the first tidy of the pictures on disk"))

	media := filepath.Join(dir, manifestID, mediaDiskDir)
	require.Equal(t, media, p.pictureDir(), "the directory is made")
	kept := make(chan struct{})
	go func() {
		keepNow(p, media, testPictureKey(1), testPicture(1), clock, day)
		close(kept)
	}()
	waitOrFail(t, kept, "the next picture waited on the lock the panic left held")
	require.Equal(t, []string{mediaDiskName(testPictureKey(1))}, filesIn(t, media))
}

// Keeping a picture on disk that panics is logged once and leaves the
// picture in memory only, as a write that failed: its place in the queue and
// the write lock are given back, so the next picture is kept.
func TestKeepingAPictureThatPanicsLetsTheNextOneLand(t *testing.T) {
	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	anyLogError(api)
	clock := newFakeClock()
	media := pictureDiskIn(t, p, t.TempDir(), clock)
	require.Equal(t, media, p.pictureDir(), "the directory is made")
	d := p.mediaDisk()

	d.freeSpace = func(string) (uint64, uint64, bool) { panic("the volume went wrong") }
	p.keepPicture(media, testPictureKey(1), testPicture(1), clock.now(), clock.now().Add(day))
	waitGroupOrFail(t, &d.pending, "the write that panicked was waited for for good")
	require.Equal(t, 1, panicsLoggedAt(api, "keeping a picture on disk"))
	require.Empty(t, filesIn(t, media))
	d.lock.Lock()
	queued, queuedBytes := d.queued, d.queuedBytes
	d.lock.Unlock()
	require.Zero(t, queued)
	require.Zero(t, queuedBytes)

	d.freeSpace = plentyOfRoom
	kept := make(chan struct{})
	go func() {
		keepNow(p, media, testPictureKey(2), testPicture(2), clock, day)
		close(kept)
	}()
	waitOrFail(t, kept, "the next picture waited on the lock the panic left held")
	require.Equal(t, []string{mediaDiskName(testPictureKey(2))}, filesIn(t, media))
}
