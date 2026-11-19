package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

// Drafts through the routes, as a browser makes them: Dave is live, his
// stream played from the fixture through the media core's readers into a
// ring, which stands in for the recording the recorder manager will keep, and
// Bob is watching it. Every arrival is a number the test picks, and the
// handlers' clock is one the test moves: nothing here waits on real time.

// The clip length every draft here is cut at: half a minute, so a draft is a
// few hundred kilobytes.
const draftsClipLength = 30 * time.Second

// fakeLive is a live stream's recording as a test sets it up: what it is
// doing, and the pipeline whose current ring it is cutting from.
type fakeLive struct {
	state string
	tp    *mediaTestPipe
	rate  float64
}

// fakeClipRecordings stands in for the recorder manager.
type fakeClipRecordings struct {
	lock  sync.Mutex
	lives map[string]fakeLive
	tails map[string]clipTail

	// onLive runs once, the next time a click asks after a live stream: after
	// the click has read the settings and the document afresh, and before it
	// holds a place, which is where something else can slip in.
	onLive func()
}

func (f *fakeClipRecordings) live(handle string) clipLive {
	f.lock.Lock()
	hook := f.onLive
	f.onLive = nil

	live, known := f.lives[handle]
	f.lock.Unlock()

	if hook != nil {
		hook()
	}
	if !known {
		return clipLive{}
	}

	recording := clipLive{state: live.state, bytesPerSecond: live.rate}
	if live.tp != nil && live.state == clipStateReady {
		recording.ring = live.tp.live()
	}

	return recording
}

// beforeNextLive arranges for run to happen inside the next click, between
// its fresh read and the place it holds (see onLive).
func (f *fakeClipRecordings) beforeNextLive(run func()) {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.onLive = run
}

func (f *fakeClipRecordings) tail(handle string, _ time.Time) (clipTail, bool) {
	f.lock.Lock()
	defer f.lock.Unlock()

	tail, held := f.tails[handle]

	return tail, held
}

func (f *fakeClipRecordings) setLive(handle string, live fakeLive) {
	f.lock.Lock()
	defer f.lock.Unlock()

	if f.lives == nil {
		f.lives = map[string]fakeLive{}
	}
	f.lives[handle] = live
}

func (f *fakeClipRecordings) setTail(handle string, tail clipTail) {
	f.lock.Lock()
	defer f.lock.Unlock()

	if f.tails == nil {
		f.tails = map[string]clipTail{}
	}
	f.tails[handle] = tail
}

// draftsWorld is a server where Bob, Dave, Dan and Boss (an admin) have each
// linked a stream; Dave is live, recorded for the last minute; and Bob is
// watching him, in a session the latest poll listed. The clock stands at a
// minute into Dave's stream.
type draftsWorld struct {
	*clipsWorld
	h       *clipsHarness
	tp      *mediaTestPipe
	stream  *mediaFixtureStream
	rec     *fakeClipRecordings
	dave    string
	move    func(at time.Time)
	streams []apiStream
}

func newDraftsWorld(t *testing.T) *draftsWorld {
	t.Helper()

	w := newClipsWorld(t, "uid-bob", "uid-dave", "uid-dan", "uid-boss")
	clipsAdmins(w.api, "uid-boss")

	cfg := w.p.getConfiguration().Clone()
	cfg.ClipLengthSeconds = "30"
	w.p.setConfiguration(cfg)

	tp := newMediaTestPipe(t, draftsClipLength)
	stream := newMediaFixtureStream(t, tp)
	stream.play(60 * time.Second)

	move := movableClock(w.p)
	move(stream.at(60 * time.Second))

	d := &draftsWorld{
		clipsWorld: w,
		tp:         tp,
		stream:     stream,
		rec:        &fakeClipRecordings{},
		dave:       w.p.streamHandle("videokey-dave"),
		move:       move,
	}
	d.h = startClipsIn(t, w.p, w.api, model.ImageDriverLocal)
	w.p.clips().recordings = d.rec

	d.goLive(apiStream{StreamKey: "videokey-dave", Title: "Dave", UserID: "uid-dave", Username: "dave", DisplayName: "Dave", LinkSource: "manual", MOTD: "Arma night"})
	d.rec.setLive(d.dave, fakeLive{state: clipStateReady, tp: tp, rate: 12000})

	d.watch("sess-bob", "uid-bob", d.dave)

	return d
}

// goLive puts a stream in the snapshot, as a poll would, or replaces the one
// with its key.
func (d *draftsWorld) goLive(stream apiStream) {
	stream.Handle = d.p.streamHandle(stream.StreamKey)

	replaced := false
	for i := range d.streams {
		if d.streams[i].StreamKey == stream.StreamKey {
			d.streams[i], replaced = stream, true
		}
	}
	if !replaced {
		d.streams = append(d.streams, stream)
	}

	d.p.setSnapshot(&snapshot{Streams: append([]apiStream(nil), d.streams...), Configured: true})
}

// now is the handlers' clock.
func (d *draftsWorld) now() time.Time {
	return d.p.clips().now()
}

// watch opens a panel session for somebody now, and has a poll list it.
func (d *draftsWorld) watch(sessionID, userID, handle string) {
	now := d.now()
	d.p.noteClipViewer(sessionID, userID, handle, now)
	d.p.noteClipSessions(d.listing(map[string][]string{handle: {sessionID}}), now)
}

// listing is Broadcast Box's listing of the live streams, with these sessions
// open on them, by handle.
func (d *draftsWorld) listing(sessions map[string][]string) []bbStream {
	var raw []bbStream
	for _, stream := range d.streams {
		raw = append(raw, rawWatched(stream.StreamKey, sessions[stream.Handle]...))
	}

	return raw
}

// at moves the stream and the clock to the same moment.
func (d *draftsWorld) at(t *testing.T, media time.Duration) {
	t.Helper()

	d.stream.play(media)
	d.move(d.stream.at(media))
}

func (d *draftsWorld) cut(t *testing.T, userID, handle, source string) *httptest.ResponseRecorder {
	t.Helper()

	return d.call(t, userID, clipCall{
		method: http.MethodPost,
		target: clipsPath + "/drafts",
		body:   `{"handle":"` + handle + `","source":"` + source + `"}`,
	})
}

func decodeDraft(t *testing.T, response *httptest.ResponseRecorder, status int) draftView {
	t.Helper()

	require.Equal(t, status, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

	var draft draftView
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &draft))

	return draft
}

// makeDraft cuts Dave's stream for somebody, now.
func (d *draftsWorld) makeDraft(t *testing.T, userID string) draftView {
	t.Helper()

	return decodeDraft(t, d.cut(t, userID, d.dave, clipSourceLive), http.StatusCreated)
}

func (d *draftsWorld) drafts(t *testing.T, userID string) []draftView {
	t.Helper()

	response := d.call(t, userID, clipCall{method: http.MethodGet, target: clipsPath + "/drafts"})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

	var list draftListResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))
	require.NotNil(t, list.Drafts, "an empty list, never null")

	return list.Drafts
}

func publishBody(title string, inMs, outMs int64) string {
	body, _ := json.Marshal(map[string]any{"title": title, "inMs": inMs, "outMs": outMs})

	return string(body)
}

func (d *draftsWorld) publish(t *testing.T, userID, draftID, body string) *httptest.ResponseRecorder {
	t.Helper()

	return d.call(t, userID, clipCall{method: http.MethodPost, target: clipsPath + "/draft/publish?id=" + draftID, body: body})
}

func decodeClipView(t *testing.T, response *httptest.ResponseRecorder, status int) clipView {
	t.Helper()

	require.Equal(t, status, response.Code, response.Body.String())
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

	var view clipView
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &view))

	return view
}

func (d *draftsWorld) draftVideo(t *testing.T, userID, draftID string) *httptest.ResponseRecorder {
	t.Helper()

	return d.call(t, userID, clipCall{method: http.MethodGet, target: clipsPath + "/draft/video?id=" + draftID})
}

func (d *draftsWorld) putThumb(t *testing.T, userID, draftID string, picture []byte) *httptest.ResponseRecorder {
	t.Helper()

	return d.call(t, userID, clipCall{
		method: http.MethodPut,
		target: clipsPath + "/draft/thumb?id=" + draftID,
		body:   string(picture),
		header: map[string]string{"Content-Type": "image/jpeg"},
	})
}

func (d *draftsWorld) discard(t *testing.T, userID, draftID string) *httptest.ResponseRecorder {
	t.Helper()

	return d.call(t, userID, clipCall{method: http.MethodDelete, target: clipsPath + "/draft?id=" + draftID})
}

func (d *draftsWorld) draftPath(id, ext string) string {
	return filepath.Join(d.h.root, clipsDraftsDir, id+ext)
}

func (d *draftsWorld) clipPath(id, ext string) string {
	return filepath.Join(d.h.root, clipsFilesDir, id+ext)
}

// callWith makes a request under a context of the test's own, as a browser
// that goes away part way through would.
func (d *draftsWorld) callWith(ctx context.Context, userID string, c clipCall) *httptest.ResponseRecorder {
	request := httptest.NewRequest(c.method, c.target, strings.NewReader(c.body)).WithContext(ctx)
	request.Header.Set("Mattermost-User-Id", userID)
	recorder := httptest.NewRecorder()

	d.p.ServeHTTP(nil, recorder, request)

	return recorder
}

// holdSlots takes both export slots, as two other exports would, until the
// release it answers is called, or the test ends.
func holdSlots(t *testing.T) func() {
	t.Helper()

	first, err := acquireExportSlot(context.Background())
	require.NoError(t, err)
	second, err := acquireExportSlot(context.Background())
	require.NoError(t, err)

	var once sync.Once
	release := func() {
		once.Do(func() {
			first()
			second()
		})
	}
	t.Cleanup(release)

	return release
}

// clickWaiting clicks on Dave's stream for somebody while the export slots are
// held, and comes back once the click holds its place and its export has
// taken its range and is waiting for a slot. The answer comes on the channel.
func (d *draftsWorld) clickWaiting(t *testing.T, userID string) <-chan *httptest.ResponseRecorder {
	t.Helper()

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- d.callWith(context.Background(), userID, clipCall{
			method: http.MethodPost,
			target: clipsPath + "/drafts",
			body:   `{"handle":"` + d.dave + `","source":"live"}`,
		})
	}()
	mediaWaitPinned(t, d.tp.live())

	return done
}

// publishWaiting publishes a draft under ctx while the export slots are held,
// and comes back once the draft is being published and the publish is
// waiting for a slot. The answer comes on the channel.
func (d *draftsWorld) publishWaiting(t *testing.T, ctx context.Context, userID, draftID, body string) <-chan *httptest.ResponseRecorder {
	t.Helper()

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- d.callWith(ctx, userID, clipCall{method: http.MethodPost, target: clipsPath + "/draft/publish?id=" + draftID, body: body})
	}()
	d.waitForDraftState(t, draftID, draftPublishing)

	return done
}

// waitForDraftState waits for a draft to be in state, as another goroutine
// puts it there.
func (d *draftsWorld) waitForDraftState(t *testing.T, draftID, state string) {
	t.Helper()

	drafts := &d.p.clips().drafts
	deadline := time.Now().Add(time.Minute)
	for {
		drafts.mu.Lock()
		now := ""
		if draft := drafts.byID[draftID]; draft != nil {
			now = draft.state
		}
		drafts.mu.Unlock()

		if now == state {
			return
		}
		require.True(t, time.Now().Before(deadline), "the draft never became %s", state)
		runtime.Gosched()
	}
}

// answerOf is the answer a request running elsewhere gives.
func answerOf(t *testing.T, done <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()

	select {
	case response := <-done:
		return response
	case <-time.After(time.Minute):
		t.Fatal("the request never answered")
	}

	return nil
}

// setDraftState puts a draft in a state by hand, as a request elsewhere would.
func (d *draftsWorld) setDraftState(draftID, state string) {
	drafts := &d.p.clips().drafts
	drafts.mu.Lock()
	defer drafts.mu.Unlock()

	drafts.byID[draftID].state = state
}

// switchClipsOff is what saving the console with Clips switched off does to
// them: the setting first, then everything it takes away.
func (d *draftsWorld) switchClipsOff() {
	off := d.p.getConfiguration().Clone()
	off.EnableClips = false
	d.p.setConfiguration(off)
	d.p.clipsTurnedOff(d.now())
}

// draftJPEG is a picture of a card's shape, as the editor makes one from a
// frame.
func draftJPEG(t *testing.T, shade uint8) []byte {
	t.Helper()

	picture := image.NewRGBA(image.Rect(0, 0, 320, 180))
	for i := range picture.Pix {
		picture.Pix[i] = shade
	}

	var encoded bytes.Buffer
	require.NoError(t, jpeg.Encode(&encoded, picture, nil))

	return encoded.Bytes()
}

func draftIDs(drafts []draftView) []string {
	ids := []string{}
	for _, draft := range drafts {
		ids = append(ids, draft.ID)
	}

	return ids
}

// tempsIn is the writes in progress, or left behind, in a directory.
func tempsIn(t *testing.T, dir string) []string {
	t.Helper()

	var temps []string
	for _, name := range namesIn(t, dir) {
		if strings.HasPrefix(name, clipTempPrefix) {
			temps = append(temps, name)
		}
	}

	return temps
}

// A click cuts the minutes that end at it: from the first keyframe inside the
// clip length before it, never longer than the clip length, as an MP4 with
// its index first that reads back sample for sample. Only its owner lists it,
// and it is logged by handle, never by key. A stream younger than the clip
// length gives a partial draft (spec §2.19 item 9, through the route).
func TestDraftRouteExportsTheClickedWindow(t *testing.T) {
	d := newDraftsWorld(t)

	response := d.cut(t, "uid-bob", d.dave, clipSourceLive)
	draft := decodeDraft(t, response, http.StatusCreated)
	require.True(t, validClipID(draft.ID))
	require.Equal(t, d.dave, draft.Handle)
	require.Equal(t, clipSourceLive, draft.Source)
	require.Equal(t, draftReady, draft.State)
	require.Equal(t, d.stream.ms(60*time.Second), draft.CreatedAt)
	require.Equal(t, draft.CreatedAt+draftTTL.Milliseconds(), draft.ExpiresAt)

	// The window is [30 s, 60 s], and a keyframe opens it.
	require.Equal(t, d.stream.ms(30*time.Second), draft.StartAt)
	require.Equal(t, int64(30000), draft.DurationMs)
	require.Equal(t, int64(30000), draft.RequestedMs)
	require.False(t, draft.Partial)
	require.False(t, draft.Truncated)
	require.Equal(t, 320, draft.Width)
	require.Equal(t, 180, draft.Height)
	require.Equal(t, codecString(d.stream.fix.sps, true), draft.Codecs)
	require.False(t, draft.HasThumb)
	require.Equal(t, clipPerson{UserID: "uid-dave", Username: "dave", DisplayName: "Dave", Name: "Dave"}, draft.Streamer)
	require.Equal(t, "Arma night", draft.StreamNote)
	require.Nil(t, draft.Tag)
	require.Empty(t, draft.ClipID)
	require.NotContains(t, strings.ToLower(response.Body.String()), "videokey-dave")
	require.NotContains(t, response.Body.String(), `"tag"`)

	path := d.draftPath(draft.ID, clipVideoExt)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, draft.Bytes, info.Size())
	require.Equal(t, os.FileMode(clipsFileMode), info.Mode().Perm())
	require.Equal(t, []string{"ftyp", "moov", "mdat"}, mediaTopLevelBoxes(t, path))

	cut := openMediaMP4(t, path)
	require.Len(t, cut.video.Samples, 450)
	require.False(t, cut.video.Samples[0].IsNonSyncSample)
	require.Equal(t, draft.DurationMs, mediaTrackMs(cut.video))
	for i, sample := range cut.video.Samples {
		require.Equal(t, d.stream.fix.video[(450+i)%150].avcc, mediaPayload(t, sample), "frame %d", i)
	}
	require.NotNil(t, cut.audio)
	require.InDelta(t, float64(draft.DurationMs), float64(mediaTrackMs(cut.audio)), 40)

	// Half a second past a keyframe, the window starts on the next one.
	d.at(t, 61500*time.Millisecond)
	later := decodeDraft(t, d.cut(t, "uid-bob", d.dave, clipSourceLive), http.StatusCreated)
	require.Equal(t, d.stream.ms(32*time.Second), later.StartAt)
	require.Equal(t, int64(29500), later.DurationMs)

	// A stream younger than the clip length gives what it has.
	young := newMediaTestPipe(t, draftsClipLength)
	youngStream := newMediaFixtureStream(t, young)
	youngStream.start = d.stream.start + int64(41500*time.Millisecond)
	youngStream.play(20 * time.Second)
	d.goLive(apiStream{StreamKey: "videokey-bob", Title: "Bob", UserID: "uid-bob", Username: "bob", DisplayName: "Bob", LinkSource: "manual"})
	bob := d.p.streamHandle("videokey-bob")
	d.rec.setLive(bob, fakeLive{state: clipStateReady, tp: young})

	partial := decodeDraft(t, d.cut(t, "uid-bob", bob, clipSourceLive), http.StatusCreated)
	require.True(t, partial.Partial)
	require.False(t, partial.Truncated)
	require.Equal(t, youngStream.ms(0), partial.StartAt)
	require.Equal(t, int64(20000), partial.DurationMs)
	require.Equal(t, int64(30000), partial.RequestedMs)

	// Its owner lists them, newest first — the two cut at the same moment in
	// either order — and nobody else sees any of them.
	listed := draftIDs(d.drafts(t, "uid-bob"))
	require.ElementsMatch(t, []string{partial.ID, later.ID, draft.ID}, listed)
	require.Equal(t, draft.ID, listed[2], "the oldest last")
	require.Empty(t, d.drafts(t, "uid-dan"))
	require.Empty(t, d.drafts(t, "uid-boss"))

	lines := logged(d.api, "LogInfo", "A clip draft was made")
	require.Len(t, lines, 3)
	require.Equal(t, []any{"A clip draft was made", "user_id", "uid-bob", "draft_id", draft.ID, "stream_handle", d.dave, "source", clipSourceLive, "duration_ms", int64(30000)}, []any(lines[0]))
	requireNoStreamKeys(t, d.clipsWorld, response.Body.String())

	// What drafts take is counted, under nobody.
	used, mine := d.p.clips().usage("uid-bob")
	require.Equal(t, draft.Bytes+later.Bytes+partial.Bytes, used)
	require.Zero(t, mine)

	// And no snapshot says anything of them (spec §2.19 item 19).
	snap := &snapshot{Streams: append([]apiStream(nil), d.streams...), Configured: true, FetchedAt: 1}
	d.p.addClips(snap, d.p.getConfiguration(), d.now())
	encoded, err := json.Marshal(snap)
	require.NoError(t, err)
	for _, secret := range []string{draft.ID, later.ID, partial.ID, "videokey-dave", "videokey-bob", d.p.streamTag("videokey-dave")} {
		require.NotContains(t, string(encoded), secret)
	}
}

// The click is the moment the request arrived, before the gate read the link
// and however long the export then waits for a slot: the draft ends there,
// not when it was cut (spec §2.19 item 9, through the route).
func TestDraftRouteClickTimeIsTakenBeforeTheGate(t *testing.T) {
	d := newDraftsWorld(t)

	// The gate's own read of the link takes five seconds, as a slow store's
	// would, and a poll lands meanwhile with a new note on the stream.
	d.faults.lock.Lock()
	slowOnce := false
	d.faults.onRead = func(key string) {
		if key == kvLinkByUserPrefix+"uid-bob" && !slowOnce {
			slowOnce = true
			d.move(d.stream.at(65 * time.Second))
			d.goLive(apiStream{StreamKey: "videokey-dave", Title: "Dave, later", UserID: "uid-dave", LinkSource: "manual", MOTD: "after the click"})
		}
	}
	d.faults.lock.Unlock()

	// And both export slots are busy.
	releaseFirst, err := acquireExportSlot(context.Background())
	require.NoError(t, err)
	releaseSecond, err := acquireExportSlot(context.Background())
	require.NoError(t, err)
	defer releaseSecond()

	done := make(chan *httptest.ResponseRecorder, 1)
	live := d.tp.live()
	go func() {
		done <- d.callWith(context.Background(), "uid-bob", clipCall{
			method: http.MethodPost,
			target: clipsPath + "/drafts",
			body:   `{"handle":"` + d.dave + `","source":"live"}`,
		})
	}()
	mediaWaitPinned(t, live)

	// Twenty seconds of the stream go by before a slot frees.
	d.at(t, 80*time.Second)
	releaseFirst()

	var response *httptest.ResponseRecorder
	select {
	case response = <-done:
	case <-time.After(time.Minute):
		t.Fatal("the draft was never cut")
	}

	draft := decodeDraft(t, response, http.StatusCreated)
	require.Equal(t, d.stream.ms(60*time.Second), draft.CreatedAt, "the click, not the gate")
	require.Equal(t, "Arma night", draft.StreamNote, "the stream as it was at the click")
	require.Equal(t, d.stream.ms(30*time.Second), draft.StartAt)
	require.Equal(t, d.stream.ms(60*time.Second), draft.StartAt+draft.DurationMs, "the window of the click, not of the slot")

	cut := openMediaMP4(t, d.draftPath(draft.ID, clipVideoExt))
	for i, sample := range cut.video.Samples {
		require.Equal(t, d.stream.fix.video[(450+i)%150].avcc, mediaPayload(t, sample), "frame %d", i)
	}
}

// mdatAt is where an MP4's media starts.
func mdatAt(t *testing.T, path string) int64 {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	for pos := 0; pos+8 <= len(data); {
		size := int(binary.BigEndian.Uint32(data[pos:]))
		if string(data[pos+4:pos+8]) == "mdat" {
			return int64(pos)
		}
		require.GreaterOrEqual(t, size, 8)
		pos += size
	}
	t.Fatal("no mdat")

	return 0
}

// Publishing cuts the clip from the keyframe at or before the in-point, with
// the difference as its lead, to the end of the frame the out-point falls in.
// A range outside the draft, or one under five seconds, is refused before a
// token is spent. The whole of a draft is renamed into place rather than cut
// again, and a trim that fails leaves no file behind and the draft as it was
// (spec §2.19 item 10, through the route).
func TestPublishRouteTrimsFromTheKeyframeAtOrBeforeTheIn(t *testing.T) {
	d := newDraftsWorld(t)
	draft := d.makeDraft(t, "uid-bob")

	for _, bad := range []struct {
		in, out int64
		code    clipErrorCode
	}{
		{1000, 5500, clipErrTooShort},
		{0, 30051, clipErrBadRange},
		{5000, 5000, clipErrBadRange},
		{-1, 10000, clipErrBadRange},
	} {
		about := publishBody("", bad.in, bad.out)
		requireClipError(t, d.publish(t, "uid-bob", draft.ID, about), http.StatusBadRequest, bad.code, about)
	}
	_, spent := tokensLeft(d.p.clips().publishLimiter, "uid-bob")
	require.False(t, spent, "a range that cannot be cut costs nothing")
	requireClipError(t, d.publish(t, "uid-bob", draft.ID, `{"title":"x","inMs":0}`), http.StatusBadRequest, clipErrBadRequest, "no out-point")
	requireClipError(t, d.publish(t, "uid-bob", draft.ID, `{"title":7,"inMs":0,"outMs":9000}`), http.StatusBadRequest, clipErrBadRequest, "a title that is not text")

	// Published three minutes after it was cut: a clip is as old as its
	// publishing, and its days run from then (spec §2.8).
	d.move(d.now().Add(3 * time.Minute))

	// In at 3.3 s, out at 20 s: the file starts on the keyframe at 2 s.
	view := decodeClipView(t, d.publish(t, "uid-bob", draft.ID, publishBody("that jump", 3300, 20000)), http.StatusCreated)
	require.Equal(t, draft.ID, view.ID)
	require.Equal(t, "that jump", view.Title)
	require.Equal(t, int64(1300), view.LeadMs)
	require.Equal(t, int64(16700), view.DurationMs)
	require.Equal(t, draft.StartAt+3300, view.StartAt)
	require.Equal(t, d.now().UnixMilli(), view.CreatedAt)
	require.Equal(t, draft.CreatedAt+3*time.Minute.Milliseconds(), view.CreatedAt, "the publish, not the click")
	require.Equal(t, view.CreatedAt+30*dayMs, view.ExpiresAt)
	require.Equal(t, 320, view.Width)
	require.Equal(t, draft.Codecs, view.Codecs)
	require.True(t, view.Mine)
	require.Equal(t, clipPerson{UserID: "uid-bob", Username: "bob", DisplayName: "Bob", Name: "Bob"}, view.Clipper)
	require.Equal(t, clipPerson{UserID: "uid-dave", Username: "dave", DisplayName: "Dave", Name: "Dave"}, view.Streamer)
	require.Equal(t, "Arma night", view.StreamNote)
	require.Empty(t, view.Thumb)

	clip := openMediaMP4(t, d.clipPath(draft.ID, clipVideoExt))
	require.False(t, clip.video.Samples[0].IsNonSyncSample)
	require.Equal(t, int64(18000), mediaTrackMs(clip.video), "from the keyframe at 2 s to the end of the frame at 20 s")
	require.Equal(t, d.stream.fix.video[30].avcc, mediaPayload(t, clip.video.Samples[0]), "the stream's frame at 32 s")
	require.NotNil(t, clip.audio)
	require.InDelta(t, 18000, float64(mediaTrackMs(clip.audio)), 40)
	info, err := os.Stat(d.clipPath(draft.ID, clipVideoExt))
	require.NoError(t, err)
	require.Equal(t, info.Size(), view.Bytes)

	stored := d.stored(t).clip(draft.ID)
	require.NotNil(t, stored)
	require.Equal(t, d.p.streamTag("videokey-dave"), stored.StreamTag)
	require.Equal(t, d.dave, stored.Handle)
	require.Equal(t, "uid-dave", stored.StreamerID)
	require.Equal(t, "manual", stored.StreamerLink)
	require.Equal(t, "Dave", stored.StreamName)
	require.Equal(t, view.Bytes, stored.Bytes)

	// The draft is a clip now: listed as published, with its clip's id, and
	// its video gone.
	listed := d.drafts(t, "uid-bob")
	require.Len(t, listed, 1)
	require.Equal(t, draftPublished, listed[0].State)
	require.Equal(t, draft.ID, listed[0].ClipID)
	requireClipError(t, d.draftVideo(t, "uid-bob", draft.ID), http.StatusGone, clipErrDraftGone, "a published draft")

	// The whole of a draft is the draft's own file, renamed, not cut again.
	d.at(t, 95*time.Second)
	whole := d.makeDraft(t, "uid-bob")
	before, err := os.ReadFile(d.draftPath(whole.ID, clipVideoExt))
	require.NoError(t, err)

	renamed := decodeClipView(t, d.publish(t, "uid-bob", whole.ID, publishBody("", 0, whole.DurationMs)), http.StatusCreated)
	require.Zero(t, renamed.LeadMs)
	require.Equal(t, whole.DurationMs, renamed.DurationMs)
	require.Equal(t, whole.StartAt, renamed.StartAt)
	require.Equal(t, int64(len(before)), renamed.Bytes)
	after, err := os.ReadFile(d.clipPath(whole.ID, clipVideoExt))
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.False(t, exists(d.draftPath(whole.ID, clipVideoExt)))

	// A draft whose media cannot be read back: the trim fails part way, and
	// leaves nothing in clips/ and the draft as it was.
	d.at(t, 130*time.Second)
	broken := d.makeDraft(t, "uid-bob")
	path := d.draftPath(broken.ID, clipVideoExt)
	require.NoError(t, os.Truncate(path, mdatAt(t, path)+2000))
	tokens, _ := tokensLeft(d.p.clips().publishLimiter, "uid-bob")

	requireClipError(t, d.publish(t, "uid-bob", broken.ID, publishBody("", 1000, 20000)), http.StatusServiceUnavailable, clipErrStore, "a trim that failed")
	require.Len(t, logged(d.api, "LogWarn", "Could not cut a clip from its draft"), 1, "its range was read; the cut itself failed")
	require.Empty(t, tempsIn(t, filepath.Join(d.h.root, clipsFilesDir)))
	require.False(t, exists(d.clipPath(broken.ID, clipVideoExt)))
	require.True(t, exists(path))
	require.Nil(t, d.stored(t).clip(broken.ID))
	left, _ := tokensLeft(d.p.clips().publishLimiter, "uid-bob")
	require.GreaterOrEqual(t, left, tokens, "the store's trouble costs nothing; the bucket has only refilled since")
	require.Equal(t, draftReady, d.drafts(t, "uid-bob")[0].State)

	require.Len(t, logged(d.api, "LogInfo", "A clip was published"), 2)
}

// Two publishes of one draft at once — a double click, two tabs — make one
// clip: one is 201, the other is told the draft is being published, or, if
// it comes in after, handed the same clip. Real goroutines, and the store's
// real compare-and-set (spec §2.19 item 11).
func TestConcurrentPublishOfOneDraftMakesOneClip(t *testing.T) {
	d := newDraftsWorld(t)

	for round := range 4 {
		draft := d.makeDraft(t, "uid-bob")
		in := int64(round) * 6000
		body := publishBody("round", in, in+5500)

		var start, finish sync.WaitGroup
		start.Add(1)
		responses := make([]*httptest.ResponseRecorder, 2)
		for i := range responses {
			finish.Add(1)
			go func() {
				defer finish.Done()
				start.Wait()
				responses[i] = d.callWith(context.Background(), "uid-bob", clipCall{method: http.MethodPost, target: clipsPath + "/draft/publish?id=" + draft.ID, body: body})
			}()
		}
		start.Done()
		finish.Wait()

		created := 0
		for _, response := range responses {
			switch response.Code {
			case http.StatusCreated:
				created++
				require.Equal(t, draft.ID, decodeClipView(t, response, http.StatusCreated).ID)
			case http.StatusOK:
				require.Equal(t, draft.ID, decodeClipView(t, response, http.StatusOK).ID)
			default:
				requireClipError(t, response, http.StatusConflict, clipErrPublishing, "the second publish")
			}
		}
		require.Equal(t, 1, created, "round %d", round)

		entries := 0
		for _, entry := range d.stored(t).Clips {
			if entry.ID == draft.ID {
				entries++
			}
		}
		require.Equal(t, 1, entries, "round %d", round)

		clip := openMediaMP4(t, d.clipPath(draft.ID, clipVideoExt))
		require.False(t, clip.video.Samples[0].IsNonSyncSample)
		require.Empty(t, tempsIn(t, filepath.Join(d.h.root, clipsFilesDir)))
	}

	require.Len(t, d.stored(t).Clips, 4)
}

// A publish retried after it landed — its answer lost on the way back — is
// answered with the clip it made, at no cost, until that clip has gone
// (spec §2.19 item 12).
func TestRetriedPublishAfterSuccessReturnsTheClip(t *testing.T) {
	d := newDraftsWorld(t)
	draft := d.makeDraft(t, "uid-bob")
	body := publishBody("that jump", 2000, 12000)

	first := decodeClipView(t, d.publish(t, "uid-bob", draft.ID, body), http.StatusCreated)
	tokens, _ := tokensLeft(d.p.clips().publishLimiter, "uid-bob")

	again := decodeClipView(t, d.publish(t, "uid-bob", draft.ID, body), http.StatusOK)
	require.Equal(t, first, again)

	// Even with a different title or range: the draft is a clip now.
	other := decodeClipView(t, d.publish(t, "uid-bob", draft.ID, publishBody("something else", 0, 9000)), http.StatusOK)
	require.Equal(t, first, other)

	left, _ := tokensLeft(d.p.clips().publishLimiter, "uid-bob")
	require.Equal(t, tokens, left, "a retry costs nothing")
	require.Len(t, d.stored(t).Clips, 1)
	require.Len(t, logged(d.api, "LogInfo", "A clip was published"), 1)

	response := d.call(t, "uid-boss", clipCall{method: http.MethodDelete, target: adminClipsPath + "/clip?id=" + draft.ID})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	requireClipError(t, d.publish(t, "uid-bob", draft.ID, body), http.StatusGone, clipErrClipGone, "its clip has gone")
}

// A trimmed publish takes the draft's picture with it and leaves nothing of
// the draft behind; the room it takes is now its clipper's (spec §2.19 item
// 13).
func TestTrimmedPublishLeavesNoDraftFile(t *testing.T) {
	d := newDraftsWorld(t)
	draft := d.makeDraft(t, "uid-bob")
	picture := draftJPEG(t, 90)

	response := d.putThumb(t, "uid-bob", draft.ID, picture)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.True(t, d.drafts(t, "uid-bob")[0].HasThumb)

	view := decodeClipView(t, d.publish(t, "uid-bob", draft.ID, publishBody("", 4000, 16000)), http.StatusCreated)
	require.Equal(t, testThumbOf(picture), view.Thumb)

	require.Empty(t, namesIn(t, filepath.Join(d.h.root, clipsDraftsDir)))
	require.True(t, exists(d.clipPath(draft.ID, clipVideoExt)))
	kept, err := os.ReadFile(d.clipPath(draft.ID, clipThumbExt))
	require.NoError(t, err)
	require.Equal(t, picture, kept)

	video, err := os.Stat(d.clipPath(draft.ID, clipVideoExt))
	require.NoError(t, err)
	used, mine := d.p.clips().usage("uid-bob")
	require.Equal(t, video.Size()+int64(len(picture)), mine)
	require.Equal(t, mine, used, "nothing of the draft is counted any more")

	// The clip's picture is served as any clip's is.
	response = d.call(t, "uid-dan", clipCall{method: http.MethodGet, target: clipsPath + "/thumb?id=" + draft.ID + "&v=" + view.Thumb})
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, picture, response.Body.Bytes())
}

// testThumbOf is a picture's version, as a clip carries it.
func testThumbOf(picture []byte) string {
	return clipThumbVersion(picture)
}

// The minute's housekeeping: a draft past its two hours is expired and its
// files deleted, unless it is being published; a tombstone is forgotten ten
// minutes on; and drafts/ is swept of whatever no draft accounts for, once it
// is ten minutes old, while a draft's own files stay however old they are
// (spec §2.19 item 13).
func TestStrayDraftFilesAreSwept(t *testing.T) {
	d := newDraftsWorld(t)
	keep := d.makeDraft(t, "uid-bob")
	old := d.makeDraft(t, "uid-bob")
	dir := filepath.Join(d.h.root, clipsDraftsDir)
	now := d.now()

	// The drafts' own files, made to look old: kept because they are drafts'.
	for _, id := range []string{keep.ID, old.ID} {
		require.NoError(t, os.Chtimes(d.draftPath(id, clipVideoExt), now.Add(-time.Hour), now.Add(-time.Hour)))
	}

	stray := filepath.Join(dir, "00000000000000ff"+clipVideoExt)
	strayThumb := filepath.Join(dir, "00000000000000ff"+clipThumbExt)
	young := filepath.Join(dir, "00000000000000ee"+clipVideoExt)
	staleTemp := filepath.Join(dir, clipTempPrefix+"00000000000000dd-0a0b0c0d.mp4")
	freshTemp := filepath.Join(dir, clipTempPrefix+"00000000000000cc-0a0b0c0d.jpg")
	notOurs := filepath.Join(dir, "notes.txt")
	writeAged(t, stray, []byte("stray"), now.Add(-11*time.Minute))
	writeAged(t, strayThumb, []byte("stray"), now.Add(-11*time.Minute))
	writeAged(t, young, []byte("young"), now.Add(-9*time.Minute))
	writeAged(t, staleTemp, []byte("half"), now.Add(-11*time.Minute))
	writeAged(t, freshTemp, []byte("half"), now.Add(-time.Minute))
	writeAged(t, notOurs, []byte("notes"), now.Add(-48*time.Hour))

	d.p.housekeepClips(now)
	for _, gone := range []string{stray, strayThumb, staleTemp} {
		require.False(t, exists(gone), gone)
	}
	for _, kept := range []string{young, freshTemp, notOurs, d.draftPath(keep.ID, clipVideoExt), d.draftPath(old.ID, clipVideoExt)} {
		require.True(t, exists(kept), kept)
	}
	require.Len(t, logged(d.api, "LogInfo", "Stray clip draft files were removed"), 1)

	// Two hours on, a draft that is being published is left to finish, and
	// the other one is expired.
	d.p.clips().drafts.mu.Lock()
	d.p.clips().drafts.byID[keep.ID].state = draftPublishing
	d.p.clips().drafts.mu.Unlock()
	requireClipError(t, d.discard(t, "uid-bob", keep.ID), http.StatusConflict, clipErrPublishing, "thrown away mid-publish")

	expiry := time.UnixMilli(old.ExpiresAt)
	d.move(expiry)
	d.p.housekeepClips(expiry)
	require.False(t, exists(d.draftPath(old.ID, clipVideoExt)))
	require.True(t, exists(d.draftPath(keep.ID, clipVideoExt)))
	requireClipError(t, d.draftVideo(t, "uid-bob", old.ID), http.StatusGone, clipErrDraftGone, "expired")
	requireClipError(t, d.publish(t, "uid-bob", old.ID, publishBody("", 0, 10000)), http.StatusGone, clipErrDraftGone, "expired")
	require.Equal(t, []string{keep.ID}, draftIDs(d.drafts(t, "uid-bob")))

	// Ten minutes later still, the tombstone is forgotten: an expired draft
	// is as unknown as one that never was.
	later := expiry.Add(draftTombstone)
	d.move(later)
	d.p.housekeepClips(later)
	requireClipError(t, d.draftVideo(t, "uid-bob", old.ID), http.StatusNotFound, clipErrDraftGone, "forgotten")

	// And who was watching what is forgotten a quarter of an hour after it
	// was last heard of.
	require.False(t, d.p.clips().viewers.watchingAt("uid-bob", d.dave, d.stream.at(0), d.now()))
}

// A publish whose write fails after the draft's files were moved into
// clips/ puts them back: the draft still plays, as it was, and can be
// published again (spec §2.19 item 14).
func TestFailedFullRangePublishPutsTheDraftBack(t *testing.T) {
	d := newDraftsWorld(t)
	draft := d.makeDraft(t, "uid-bob")
	picture := draftJPEG(t, 120)
	require.Equal(t, http.StatusOK, d.putThumb(t, "uid-bob", draft.ID, picture).Code)
	video, err := os.ReadFile(d.draftPath(draft.ID, clipVideoExt))
	require.NoError(t, err)
	usedBefore, _ := d.p.clips().usage("")

	// Every compare-and-set of the document loses to another writer.
	rival := 0
	d.faults.setInterfere(func(key string, store map[string][]byte) {
		if key == kvClips {
			rival++
			store[key] = []byte(fmt.Sprintf(`{"v":1,"rev":%d,"clips":[]}`, 1000+rival))
		}
	})

	body := publishBody("", 0, draft.DurationMs)
	requireClipError(t, d.publish(t, "uid-bob", draft.ID, body), http.StatusServiceUnavailable, clipErrStore, "the write failed")

	require.False(t, exists(d.clipPath(draft.ID, clipVideoExt)))
	require.False(t, exists(d.clipPath(draft.ID, clipThumbExt)))
	back, err := os.ReadFile(d.draftPath(draft.ID, clipVideoExt))
	require.NoError(t, err)
	require.Equal(t, video, back)
	thumb, err := os.ReadFile(d.draftPath(draft.ID, clipThumbExt))
	require.NoError(t, err)
	require.Equal(t, picture, thumb)
	usedAfter, mine := d.p.clips().usage("uid-bob")
	require.Equal(t, usedBefore, usedAfter)
	require.Zero(t, mine)

	listed := d.drafts(t, "uid-bob")
	require.Len(t, listed, 1)
	require.Equal(t, draftReady, listed[0].State)
	require.True(t, listed[0].HasThumb)
	_, spent := tokensLeft(d.p.clips().publishLimiter, "uid-bob")
	require.True(t, spent)
	left, _ := tokensLeft(d.p.clips().publishLimiter, "uid-bob")
	require.Equal(t, float64(10), left, "refunded: the store's trouble, not the caller's")

	response := d.call(t, "uid-bob", clipCall{method: http.MethodGet, target: clipsPath + "/draft/video?id=" + draft.ID, header: map[string]string{"Range": "bytes=100-199"}})
	require.Equal(t, http.StatusPartialContent, response.Code)
	require.Equal(t, video[100:200], response.Body.Bytes())

	// While it is being published, its video has been renamed into clips/
	// to be written as a clip, and the editor still plays it: the same
	// bytes, from wherever they are, whether the write then fails or lands.
	played, spoil := 0, true
	d.faults.setInterfere(func(key string, store map[string][]byte) {
		if key != kvClips {
			return
		}
		played++
		require.False(t, exists(d.draftPath(draft.ID, clipVideoExt)), "renamed away")
		response := draftVideoNow(d, "uid-bob", draft.ID, "bytes=100-199")
		require.Equal(t, http.StatusPartialContent, response.Code, response.Body.String())
		require.Equal(t, video[100:200], response.Body.Bytes())
		if spoil {
			rival++
			store[key] = []byte(fmt.Sprintf(`{"v":1,"rev":%d,"clips":[]}`, 1000+rival))
		}
	})
	requireClipError(t, d.publish(t, "uid-bob", draft.ID, body), http.StatusServiceUnavailable, clipErrStore, "the write failed again")
	require.GreaterOrEqual(t, played, 1)

	played, spoil = 0, false
	view := decodeClipView(t, d.publish(t, "uid-bob", draft.ID, body), http.StatusCreated)
	require.GreaterOrEqual(t, played, 1)
	d.faults.setInterfere(nil)
	require.Equal(t, testThumbOf(picture), view.Thumb)
	moved, err := os.ReadFile(d.clipPath(draft.ID, clipVideoExt))
	require.NoError(t, err)
	require.Equal(t, video, moved)
	require.Empty(t, namesIn(t, filepath.Join(d.h.root, clipsDraftsDir)))
}

// draftVideoNow asks for a draft's video past the gate, as a Range request
// from the editor would: for a test that is inside the store, where the gate's
// own read of the link would wait for the store.
func draftVideoNow(d *draftsWorld, userID, draftID, byteRange string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, clipsPath+"/draft/video?id="+draftID, nil)
	request.Header.Set("Range", byteRange)
	recorder := httptest.NewRecorder()
	d.p.handleDraftVideo(recorder, request, userID, "")

	return recorder
}

// A trimmed publish writes a new file into clips/ while the draft's own stays
// where it is, and the editor plays the draft, not the clip, until the
// publish is over; then the draft is gone. A video that cannot be found is
// answered with a code the panel knows: the draft is gone, or, while it is
// being published, busy (spec §2.10 draft/video).
func TestDraftPlaysWhileItIsPublished(t *testing.T) {
	d := newDraftsWorld(t)
	draft := d.makeDraft(t, "uid-bob")
	video, err := os.ReadFile(d.draftPath(draft.ID, clipVideoExt))
	require.NoError(t, err)

	played := 0
	d.faults.setInterfere(func(key string, _ map[string][]byte) {
		if key != kvClips {
			return
		}
		played++
		require.True(t, exists(d.clipPath(draft.ID, clipVideoExt)), "the trim is written")
		response := draftVideoNow(d, "uid-bob", draft.ID, "bytes=0-")
		require.Equal(t, http.StatusPartialContent, response.Code, response.Body.String())
		require.Equal(t, video, response.Body.Bytes(), "the draft, not the clip cut from it")
	})
	view := decodeClipView(t, d.publish(t, "uid-bob", draft.ID, publishBody("", 4000, 16000)), http.StatusCreated)
	d.faults.setInterfere(nil)
	require.GreaterOrEqual(t, played, 1)
	require.Less(t, view.Bytes, int64(len(video)))
	require.False(t, exists(d.draftPath(draft.ID, clipVideoExt)))
	requireClipError(t, d.draftVideo(t, "uid-bob", draft.ID), http.StatusGone, clipErrDraftGone, "published")

	// A ready draft whose video has gone from the disk, and one being
	// published whose video is nowhere.
	d.at(t, 95*time.Second)
	lost := d.makeDraft(t, "uid-bob")
	require.NoError(t, os.Remove(d.draftPath(lost.ID, clipVideoExt)))
	requireClipError(t, d.draftVideo(t, "uid-bob", lost.ID), http.StatusNotFound, clipErrDraftGone, "no video")
	d.setDraftState(lost.ID, draftPublishing)
	requireClipError(t, d.draftVideo(t, "uid-bob", lost.ID), http.StatusConflict, clipErrPublishing, "no video while published")
}

// The System Console says how many drafts this node holds: those ready and
// those being published, not one still being cut or one gone (spec §2.10.3).
func TestAdminSeesHowManyDraftsAreHeld(t *testing.T) {
	d := newDraftsWorld(t)
	d.makeDraft(t, "uid-bob")
	busy := d.makeDraft(t, "uid-bob")
	thrown := d.makeDraft(t, "uid-bob")
	d.setDraftState(busy.ID, draftPublishing)
	require.Equal(t, http.StatusOK, d.discard(t, "uid-bob", thrown.ID).Code)

	d.watch("sess-dan", "uid-dan", d.dave)
	release := holdSlots(t)
	waiting := d.clickWaiting(t, "uid-dan")

	console := decodeAdminClips(t, d.call(t, "uid-boss", clipCall{method: http.MethodGet, target: adminClipsPath}))
	require.Equal(t, 2, console.Drafts)

	release()
	decodeDraft(t, answerOf(t, waiting), http.StatusCreated)
	console = decodeAdminClips(t, d.call(t, "uid-boss", clipCall{method: http.MethodGet, target: adminClipsPath}))
	require.Equal(t, 3, console.Drafts)
}

// A draft's picture is a JPEG of a card's shape, of 200 KB at most, judged
// by its bytes and never by what the request says it is. A second one
// replaces the first, and the room it takes is counted once. Each upload is
// logged with who made it, so a picture that should not be there can be
// traced (spec §2.10, §2.10.5).
func TestDraftThumbIsAJPEGOfACardsShape(t *testing.T) {
	d := newDraftsWorld(t)
	draft := d.makeDraft(t, "uid-bob")

	tall := image.NewRGBA(image.Rect(0, 0, 180, 320))
	var tallJPEG bytes.Buffer
	require.NoError(t, jpeg.Encode(&tallJPEG, tall, nil))

	for about, c := range map[string]struct {
		body   []byte
		status int
	}{
		"not a picture":  {[]byte("\xff\xd8\xff\xe0 not really"), http.StatusBadRequest},
		"a PNG":          {[]byte("\x89PNG\r\n\x1a\n...."), http.StatusBadRequest},
		"standing up":    {tallJPEG.Bytes(), http.StatusBadRequest},
		"too small":      {testJPEG(t), http.StatusBadRequest},
		"over 200 KB":    {append(draftJPEG(t, 1), make([]byte, maxClipThumbBytes)...), http.StatusRequestEntityTooLarge},
		"nothing at all": {nil, http.StatusBadRequest},
	} {
		requireClipError(t, d.putThumb(t, "uid-bob", draft.ID, c.body), c.status, clipErrBadThumb, about)
	}
	require.False(t, d.drafts(t, "uid-bob")[0].HasThumb)
	require.False(t, exists(d.draftPath(draft.ID, clipThumbExt)))
	require.Empty(t, tempsIn(t, filepath.Join(d.h.root, clipsDraftsDir)))

	first, second := draftJPEG(t, 40), draftJPEG(t, 220)
	require.Equal(t, http.StatusOK, d.putThumb(t, "uid-bob", draft.ID, first).Code)
	response := d.putThumb(t, "uid-bob", draft.ID, second)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"ok":true}`, response.Body.String())

	kept, err := os.ReadFile(d.draftPath(draft.ID, clipThumbExt))
	require.NoError(t, err)
	require.Equal(t, second, kept)
	used, _ := d.p.clips().usage("")
	require.Equal(t, draft.Bytes+int64(len(second)), used)
	require.Empty(t, tempsIn(t, filepath.Join(d.h.root, clipsDraftsDir)))

	lines := logged(d.api, "LogInfo", "A clip picture was uploaded")
	require.Len(t, lines, 2)
	require.Equal(t, []any{"A clip picture was uploaded", "user_id", "uid-bob", "draft_id", draft.ID, "bytes", len(second)}, []any(lines[1]))

	view := decodeClipView(t, d.publish(t, "uid-bob", draft.ID, publishBody("", 0, draft.DurationMs)), http.StatusCreated)
	require.Equal(t, testThumbOf(second), view.Thumb)

	// A published draft takes no more pictures.
	requireClipError(t, d.putThumb(t, "uid-bob", draft.ID, first), http.StatusGone, clipErrDraftGone, "published")
}

// A share, a draft and a publish each meter the attempt before anything
// costly: before the whole document is read afresh, Mattermost is asked
// anything, or a draft's file is parsed. Sixty a minute, the three routes
// together, refused or not; their own tokens are left for requests that are
// good, so none of these refusals spent one.
func TestRefusedClipWritesAreMeteredBeforeTheyCostAnything(t *testing.T) {
	d := newDraftsWorld(t)

	var lock sync.Mutex
	docReads := 0
	d.faults.lock.Lock()
	previous := d.faults.onRead
	d.faults.onRead = func(key string) {
		if previous != nil {
			previous(key)
		}
		if key == kvClips {
			lock.Lock()
			docReads++
			lock.Unlock()
		}
	}
	d.faults.lock.Unlock()
	readsSoFar := func() int {
		lock.Lock()
		defer lock.Unlock()

		return docReads
	}

	// Dan is linked but watching nothing, and asks about a clip and a draft
	// that do not exist: refused, every time, and each refusal is an
	// attempt.
	share := func(userID string) *httptest.ResponseRecorder {
		return d.call(t, userID, clipCall{method: http.MethodPost, target: clipsPath + "/share?id=" + someClipID, body: `{"channelId":"` + someChannel + `"}`})
	}
	cut := func(userID string) *httptest.ResponseRecorder {
		return d.cut(t, userID, d.dave, clipSourceLive)
	}
	publish := func(userID string) *httptest.ResponseRecorder {
		return d.publish(t, userID, otherClipID, publishBody("", 0, 10000))
	}

	for range clipAttemptsPerMinute / 3 {
		requireClipError(t, share("uid-dan"), http.StatusGone, clipErrClipGone, "no such clip")
		requireClipError(t, cut("uid-dan"), http.StatusForbidden, clipErrNotWatching, "watching nothing")
		requireClipError(t, publish("uid-dan"), http.StatusNotFound, clipErrDraftGone, "no such draft")
	}

	// The sixty-first, whichever route it is on, is turned away before the
	// document is read.
	before := readsSoFar()
	for _, attempt := range []func(string) *httptest.ResponseRecorder{share, cut, publish} {
		requireClipError(t, attempt("uid-dan"), http.StatusTooManyRequests, clipErrRateLimited, "past sixty")
	}
	require.Equal(t, before, readsSoFar(), "nothing read for an attempt over the meter")

	for _, limiter := range []*rateLimiter{d.p.clips().shareLimiter, d.p.clips().captureLimiter, d.p.clips().publishLimiter} {
		_, spent := tokensLeft(limiter, "uid-dan")
		require.False(t, spent, "a refusal costs no share, draft or publish")
	}

	// Per person: Bob is not held up by Dan.
	requireClipError(t, share("uid-bob"), http.StatusGone, clipErrClipGone, "Bob's own first attempt")

	// And it refills with the minute.
	d.move(d.now().Add(time.Minute))
	requireClipError(t, share("uid-dan"), http.StatusGone, clipErrClipGone, "a minute on")
}
