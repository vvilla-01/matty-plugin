package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image"
	"image/jpeg"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi/cluster"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// The test fixture: ten seconds of H.264 and Opus, made by
// build/make-test-clip.sh.
const clipFixture = "testdata/clip-h264-opus.mp4"

// A sweep schedule that runs nothing by itself. The test calls the sweep.
type fakeClipsJob struct {
	lock   sync.Mutex
	run    func()
	closed int
}

func (j *fakeClipsJob) Close() error {
	j.lock.Lock()
	defer j.lock.Unlock()

	j.closed++

	return nil
}

func (j *fakeClipsJob) closes() int {
	j.lock.Lock()
	defer j.lock.Unlock()

	return j.closed
}

// Republish timers that go off when the test says.
type fakeClipsTimers struct {
	lock  sync.Mutex
	armed []*fakeClipsTimer
}

type fakeClipsTimer struct {
	wait    time.Duration
	run     func()
	stopped bool
}

func (t *fakeClipsTimer) Stop() bool {
	t.stopped = true

	return true
}

func (f *fakeClipsTimers) after(wait time.Duration, run func()) clipsTimer {
	f.lock.Lock()
	defer f.lock.Unlock()

	timer := &fakeClipsTimer{wait: wait, run: run}
	f.armed = append(f.armed, timer)

	return timer
}

func (f *fakeClipsTimers) count() int {
	f.lock.Lock()
	defer f.lock.Unlock()

	return len(f.armed)
}

// fire sets off the latest timer armed.
func (f *fakeClipsTimers) fire(t *testing.T) {
	t.Helper()

	f.lock.Lock()
	require.NotEmpty(t, f.armed, "a republish was armed")
	timer := f.armed[len(f.armed)-1]
	f.lock.Unlock()

	timer.run()
}

// clipsHarness is Clips started on a test's plugin: its files in a fresh
// directory, and the sweep and the republish held for the test to run.
type clipsHarness struct {
	dir    string
	root   string
	job    *fakeClipsJob
	timers *fakeClipsTimers
}

// holdClipsTimers replaces the sweep's schedule, the minute's housekeeping
// and the republish timer, and says how much room the disk has left: plenty.
// The housekeeping never runs by itself; a test calls housekeepClips.
func holdClipsTimers(p *Plugin) (*fakeClipsJob, *fakeClipsTimers) {
	job := &fakeClipsJob{}
	timers := &fakeClipsTimers{}

	s := p.clips()
	s.schedule = func(_ cluster.JobPluginAPI, run func()) (clipsJob, error) {
		job.lock.Lock()
		defer job.lock.Unlock()

		job.run = run

		return job, nil
	}
	s.every = func(time.Duration, func()) clipsJob {
		return &fakeClipsJob{}
	}
	s.afterFunc = timers.after
	s.freeSpace = func(string) (uint64, uint64, bool) {
		return 500 << 30, 1000 << 30, true
	}

	return job, timers
}

// clipsStorageConfig is Mattermost's file settings, as GetConfig hands them
// to a plugin.
func clipsStorageConfig(directory, driver string) *model.Config {
	cfg := &model.Config{}
	cfg.FileSettings.Directory = model.NewPointer(directory)
	cfg.FileSettings.DriverName = model.NewPointer(driver)

	return cfg
}

// startClipsIn starts Clips with Mattermost's files kept in a fresh
// directory, on the driver given.
func startClipsIn(t *testing.T, p *Plugin, api *plugintest.API, driver string) *clipsHarness {
	t.Helper()

	dir := t.TempDir()
	api.On("GetConfig").Return(clipsStorageConfig(dir, driver))

	job, timers := holdClipsTimers(p)
	p.startClips()
	t.Cleanup(p.stopClips)

	return &clipsHarness{dir: dir, root: filepath.Join(dir, manifestID), job: job, timers: timers}
}

// startedClipsWorld is newClipsWorld with Clips started on local storage, at
// clipsNow.
func startedClipsWorld(t *testing.T, people ...string) (*clipsWorld, *clipsHarness) {
	t.Helper()

	w := newClipsWorld(t, people...)
	setClipsClock(w.p, clipsNow)
	h := startClipsIn(t, w.p, w.api, model.ImageDriverLocal)

	return w, h
}

// clipsAdmins makes these people system admins, and nobody else.
func clipsAdmins(api *plugintest.API, admins ...string) {
	api.On("HasPermissionTo", mock.Anything, model.PermissionManageSystem).Return(func(userID string, _ *model.Permission) bool {
		for _, admin := range admins {
			if admin == userID {
				return true
			}
		}

		return false
	})
}

func testJPEG(t *testing.T) []byte {
	t.Helper()

	var picture bytes.Buffer
	require.NoError(t, jpeg.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 32, 18)), nil))

	return picture.Bytes()
}

// testJPEGETag is the ETag a clip's picture is served with when it is
// testJPEG: what a browser that already holds it sends back as If-None-Match.
func testJPEGETag(t *testing.T) string {
	t.Helper()

	sum := sha256.Sum256(testJPEG(t))

	return `"` + hex.EncodeToString(sum[:8]) + `"`
}

// publishFixtureClip is the test-only way a clip is published: the fixture
// written into clips/ as a publish leaves it, through a temporary file and a
// rename, its picture beside it when asked for, and the entry appended in one
// write. Its files are dated when the clip was published.
func publishFixtureClip(t *testing.T, p *Plugin, entry clipEntry, withThumb bool) clipEntry {
	t.Helper()

	s := p.clips()
	root := s.writableRoot()
	require.NotEmpty(t, root, "clips have somewhere to go")
	dir := filepath.Join(root, clipsFilesDir)
	published := time.UnixMilli(entry.CreatedAt)

	video, err := os.ReadFile(clipFixture)
	require.NoError(t, err)

	file, temp, err := createClipTemp(dir, entry.ID, clipVideoExt)
	require.NoError(t, err)
	_, err = file.Write(video)
	require.NoError(t, err)
	require.NoError(t, file.Close())

	videoPath := filepath.Join(dir, entry.ID+clipVideoExt)
	require.NoError(t, os.Rename(temp, videoPath))
	require.NoError(t, os.Chtimes(videoPath, published, published))

	entry.Bytes = int64(len(video))
	entry.Width, entry.Height = 320, 180
	entry.DurationMs = 10000
	entry.Codecs = "avc1.64000d,opus"
	used := entry.Bytes

	if withThumb {
		picture := testJPEG(t)
		sum := sha256.Sum256(picture)
		entry.Thumb = hex.EncodeToString(sum[:])[:8]

		thumbPath := filepath.Join(dir, entry.ID+clipThumbExt)
		require.NoError(t, os.WriteFile(thumbPath, picture, clipsFileMode))
		require.NoError(t, os.Chtimes(thumbPath, published, published))
		used += int64(len(picture))
	}

	_, err = p.clipsCAS(published, func(doc *clipsDoc, _ *clipsWrite) (bool, error) {
		return doc.addClip(entry)
	}, entry.ClipperID)
	require.NoError(t, err)
	s.addUsage(entry.ClipperID, used)

	return entry
}

// writeAged puts a file at path, dated at.
func writeAged(t *testing.T, path string, data []byte, at time.Time) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), clipsDirMode))
	require.NoError(t, os.WriteFile(path, data, clipsFileMode))
	require.NoError(t, os.Chtimes(path, at, at))
}

func exists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}

// namesIn is what a directory holds, by name.
func namesIn(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)

	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names
}

// Clips are kept on Mattermost's own disk or nowhere. With any other file
// driver, or settings that say nothing, or a directory that will not take a
// write, nothing can be made and the admins are told why; clips already in
// the document can still be listed, nothing is created on the disk, and the
// sweep does nothing.
func TestClipsNeedTheLocalFileDriver(t *testing.T) {
	for _, tried := range []struct {
		about   string
		config  func(dir string) *model.Config
		problem string
	}{
		{"object storage", func(dir string) *model.Config { return clipsStorageConfig(dir, "amazons3") }, clipsProblemNotLocal},
		{"no settings at all", func(string) *model.Config { return nil }, clipsProblemNoSettings},
		{"no directory", func(string) *model.Config {
			cfg := &model.Config{}
			cfg.FileSettings.DriverName = model.NewPointer(model.ImageDriverLocal)

			return cfg
		}, clipsProblemNoSettings},
		{"an empty directory", func(string) *model.Config { return clipsStorageConfig("  ", model.ImageDriverLocal) }, clipsProblemNoSettings},
	} {
		w := newClipsWorld(t, "uid-bob", "uid-dave")
		clipsAdmins(w.api, "uid-dave")
		setClipsClock(w.p, clipsNow)
		w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil)}})

		dir := t.TempDir()
		w.api.On("GetConfig").Return(tried.config(dir))
		job, _ := holdClipsTimers(w.p)
		w.p.startClips()

		root, problem := w.p.clips().storage()
		require.Equal(t, tried.problem, problem, tried.about)
		require.Empty(t, root, tried.about)
		require.Empty(t, namesIn(t, dir), "%s: nothing is made on the disk", tried.about)

		snap := &snapshot{}
		w.p.addClips(snap, w.p.getConfiguration(), clipsNow)
		require.False(t, snap.Clips.Enabled, tried.about)
		require.Equal(t, 1, snap.Clips.Count, "%s: the clips already published are still there", tried.about)

		response := w.call(t, "uid-bob", clipCall{method: http.MethodGet, target: clipsPath})
		require.Equal(t, http.StatusOK, response.Code, tried.about)
		require.Contains(t, response.Body.String(), clipA)

		response = w.call(t, "uid-bob", clipCall{method: http.MethodGet, target: clipsPath + "/video?id=" + clipA})
		requireClipError(t, response, http.StatusServiceUnavailable, clipErrStorageUnavailable, tried.about)

		require.Contains(t, w.p.clipsAlertFor(w.p.getConfiguration()), "they are switched off", tried.about)

		before := w.kvGet(kvClips)
		require.False(t, w.p.sweepClipsAt(context.Background(), clipsNow.Add(400*24*time.Hour)).Ran, tried.about)
		require.Equal(t, before, w.kvGet(kvClips), "%s: the sweep did nothing", tried.about)

		w.p.stopClips()
		require.Equal(t, 1, job.closes(), tried.about)
		require.Empty(t, namesIn(t, dir), tried.about)
	}

	// A directory that will not take a write: something already in the way.
	w := newClipsWorld(t, "uid-bob")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, manifestID), []byte("in the way"), 0o600))
	w.api.On("GetConfig").Return(clipsStorageConfig(dir, model.ImageDriverLocal))
	holdClipsTimers(w.p)
	w.p.startClips()
	t.Cleanup(w.p.stopClips)

	root, problem := w.p.clips().storage()
	require.Equal(t, filepath.Join(dir, manifestID), root)
	require.Contains(t, problem, "Clips cannot write to "+root+": ")
	require.Empty(t, w.p.clips().writableRoot())

	snap := &snapshot{}
	w.p.addClips(snap, w.p.getConfiguration(), clipsNow)
	require.False(t, snap.Clips.Enabled)

	// And the local file system, as Mattermost ships it: a directory relative
	// to where it runs, made absolute.
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd()
	require.NoError(t, err)

	w = newClipsWorld(t, "uid-bob")
	w.api.On("GetConfig").Return(clipsStorageConfig("./data/", model.ImageDriverLocal))
	holdClipsTimers(w.p)
	w.p.startClips()
	t.Cleanup(w.p.stopClips)

	root, problem = w.p.clips().storage()
	require.Empty(t, problem)
	require.Equal(t, filepath.Join(cwd, "data", manifestID), root)
	require.True(t, filepath.IsAbs(root))
	require.ElementsMatch(t, []string{clipsRingDir, clipsDraftsDir, clipsFilesDir}, namesIn(t, root), "made, and nothing left behind by the check")
	for _, sub := range []string{clipsRingDir, clipsDraftsDir, clipsFilesDir} {
		info, err := os.Stat(filepath.Join(root, sub))
		require.NoError(t, err)
		require.Zero(t, info.Mode().Perm()&^fs.FileMode(clipsDirMode), "%s: never more open than 0750, whatever the umask", sub)
	}

	snap = &snapshot{}
	w.p.addClips(snap, w.p.getConfiguration(), clipsNow)
	require.True(t, snap.Clips.Enabled)
	w.api.AssertCalled(t, "LogInfo", "Clips are kept in this directory", "root", root)
}

// Activation empties the rolling recordings and the drafts, which belonged to
// the process before, and leaves every published clip alone, counting the
// room they take.
func TestClipsRootIsWipedOfRingAndDraftsOnActivate(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, manifestID)
	earlier := clipsNow.Add(-time.Hour)

	writeAged(t, filepath.Join(root, clipsRingDir, someHandle, "1", "000001.seg"), []byte("segment"), earlier)
	writeAged(t, filepath.Join(root, clipsDraftsDir, clipB+".mp4"), []byte("a draft"), earlier)
	writeAged(t, filepath.Join(root, clipsDraftsDir, ".tmp-"+clipB+"-00000000.mp4"), []byte("half"), earlier)
	writeAged(t, filepath.Join(root, clipsFilesDir, clipA+".mp4"), []byte("a published clip"), earlier)
	writeAged(t, filepath.Join(root, clipsFilesDir, clipA+".jpg"), []byte("its picture"), earlier)

	w := newClipsWorld(t, "uid-bob")
	w.seed(t, &clipsDoc{V: 1, Rev: 1, Clips: []clipEntry{testClip(clipA, "uid-bob", earlier, nil)}})
	w.api.On("GetConfig").Return(clipsStorageConfig(dir, model.ImageDriverLocal))
	holdClipsTimers(w.p)
	w.p.startClips()
	t.Cleanup(w.p.stopClips)

	require.Empty(t, namesIn(t, filepath.Join(root, clipsRingDir)))
	require.Empty(t, namesIn(t, filepath.Join(root, clipsDraftsDir)))
	require.ElementsMatch(t, []string{clipA + ".mp4", clipA + ".jpg"}, namesIn(t, filepath.Join(root, clipsFilesDir)))

	used, mine := w.p.clips().usage("uid-bob")
	require.EqualValues(t, len("a published clip")+len("its picture"), used)
	require.Equal(t, used, mine)
}

// Deactivation stops the sweep, telling one under way to stop where it is,
// stops a republish waiting to go, starts nothing more, and empties ring/
// and drafts/. Published clips and the document are left exactly as they are.
func TestStopClipsWipesRingAndDraftsAndStopsTheSweep(t *testing.T) {
	w, h := startedClipsWorld(t, "uid-bob", "uid-dave")
	publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil), true)

	_, _, err := w.p.voteClip(clipA, "uid-dave", true, clipsNow)
	require.NoError(t, err)
	require.Equal(t, 1, h.timers.count(), "a republish is waiting")

	writeAged(t, filepath.Join(h.root, clipsRingDir, someHandle, "1", "000001.seg"), []byte("segment"), clipsNow)
	writeAged(t, filepath.Join(h.root, clipsDraftsDir, clipB+".mp4"), []byte("a draft"), clipsNow)
	before := w.kvGet(kvClips)

	w.p.stopClips()

	require.Equal(t, 1, h.job.closes())
	require.True(t, h.timers.armed[0].stopped)
	require.Empty(t, namesIn(t, filepath.Join(h.root, clipsRingDir)))
	require.Empty(t, namesIn(t, filepath.Join(h.root, clipsDraftsDir)))
	require.ElementsMatch(t, []string{clipA + ".mp4", clipA + ".jpg"}, namesIn(t, filepath.Join(h.root, clipsFilesDir)))
	require.Equal(t, before, w.kvGet(kvClips))

	// The sweep the job was given has been told to stop: run now, it reads
	// nothing.
	var reads []string
	w.faults.lock.Lock()
	w.faults.onRead = func(key string) { reads = append(reads, key) }
	w.faults.lock.Unlock()

	h.job.lock.Lock()
	run := h.job.run
	h.job.lock.Unlock()
	run()

	// Nothing more is armed, rearmed or published.
	_, _, err = w.p.voteClip(clipA, "uid-dave", false, clipsNow)
	require.NoError(t, err)
	w.p.clipsLinkChanged("uid-dave")
	w.p.clips().background.Wait()
	require.Equal(t, 1, h.timers.count())

	// A timer that went off as it was being stopped publishes nothing.
	h.timers.fire(t)
	w.api.AssertNotCalled(t, "PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything)

	w.faults.lock.Lock()
	require.Equal(t, []string{kvClips}, reads, "only the vote read anything")
	w.faults.lock.Unlock()
}

// Without a root that may be written to — never started, or started with a
// problem — nothing on the disk is touched: no wipe, no delete, no recount,
// and above all no path built from an empty root, which would land wherever
// Mattermost happens to be running.
func TestClipFilesAreNeverTouchedWithoutARoot(t *testing.T) {
	cwd := t.TempDir()
	t.Chdir(cwd)
	for _, sub := range []string{clipsRingDir, clipsDraftsDir, clipsFilesDir} {
		writeAged(t, filepath.Join(cwd, sub, clipA+".mp4"), []byte("not ours"), clipsNow.Add(-48*time.Hour))
	}

	touch := func(p *Plugin) {
		p.wipeClipsScratch()
		p.clips().removeClipFiles(clipA, "uid-bob")
		p.clips().removeClipThumb(clipA, "uid-bob")
		p.clips().recountUsage(&clipsDoc{V: 1})
		p.clipsSettled([]settledClip{{ID: clipA, ClipperID: "uid-bob"}})
		_, _ = p.sweepClipOrphans(context.Background(), p.clips().writableRoot(), &clipsDoc{V: 1}, clipsNow)
		p.sweepClipsAt(context.Background(), clipsNow)
		p.stopClips()

		require.Empty(t, p.clips().clipFile(clipA, clipVideoExt))
		require.Error(t, p.clips().roomFor("uid-bob", 1, p.getConfiguration()))
	}

	never := newClipsWorld(t, "uid-bob")
	touch(never.p)

	for _, cfg := range []*model.Config{clipsStorageConfig("", model.ImageDriverLocal), clipsStorageConfig(cwd, "azure"), nil} {
		w := newClipsWorld(t, "uid-bob")
		w.api.On("GetConfig").Return(cfg)
		holdClipsTimers(w.p)
		w.p.startClips()
		touch(w.p)
	}

	for _, sub := range []string{clipsRingDir, clipsDraftsDir, clipsFilesDir} {
		require.Equal(t, []string{clipA + ".mp4"}, namesIn(t, filepath.Join(cwd, sub)), sub)
	}
	require.ElementsMatch(t, []string{clipsRingDir, clipsDraftsDir, clipsFilesDir}, namesIn(t, cwd))

	require.Error(t, prepareClipsRoot("relative/root"))
	require.Error(t, (&clipsStore{}).wipeClipsDir(clipsFilesDir), "published clips are never wiped")
}

// A file still being written has a name nobody else can have, and is only
// ever a new file.
func TestTempNamesNeverCollide(t *testing.T) {
	dir := t.TempDir()
	shape := regexp.MustCompile(`^\.tmp-` + clipA + `-[0-9a-f]{8}\.mp4$`)

	seen := map[string]bool{}
	for range 200 {
		file, path, err := createClipTemp(dir, clipA, clipVideoExt)
		require.NoError(t, err)
		require.NoError(t, file.Close())

		require.Regexp(t, shape, filepath.Base(path))
		require.False(t, seen[path])
		seen[path] = true

		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, fs.FileMode(clipsFileMode), info.Mode().Perm())
	}
	require.Len(t, namesIn(t, dir), 200)

	_, _, err := createClipTemp(filepath.Join(dir, "missing"), clipA, clipVideoExt)
	require.Error(t, err)

	// A name already taken is never opened again: another is drawn, and the
	// file that had it is left as it was.
	saved := clipTempRandom
	defer func() { clipTempRandom = saved }()

	clipTempRandom = &repeatingRandom{repeats: 2}
	first, firstPath, err := createClipTemp(dir, clipB, clipVideoExt)
	require.NoError(t, err)
	_, err = first.WriteString("a trim in progress")
	require.NoError(t, err)
	require.NoError(t, first.Close())
	require.Equal(t, filepath.Join(dir, clipTempPrefix+clipB+"-00000000"+clipVideoExt), firstPath)

	second, secondPath, err := createClipTemp(dir, clipB, clipVideoExt)
	require.NoError(t, err)
	require.NoError(t, second.Close())
	require.NotEqual(t, firstPath, secondPath)
	kept, err := os.ReadFile(firstPath)
	require.NoError(t, err)
	require.Equal(t, "a trim in progress", string(kept), "the first file was not touched")

	// Four names in a row taken: given up, as taken.
	clipTempRandom = &repeatingRandom{repeats: 4}
	_, _, err = createClipTemp(dir, clipB, clipVideoExt)
	require.ErrorIs(t, err, fs.ErrExist)

	// A source of names that fails is an error, never a fixed name.
	clipTempRandom = io.MultiReader()
	_, _, err = createClipTemp(dir, clipB, clipVideoExt)
	require.Error(t, err)
	require.NotErrorIs(t, err, fs.ErrExist)
}

// repeatingRandom hands out zeros the first times it is read, so the same
// temporary name comes up again, and then bytes it has not given before.
type repeatingRandom struct {
	repeats int
	next    byte
}

func (r *repeatingRandom) Read(p []byte) (int, error) {
	if r.repeats > 0 {
		r.repeats--
		clear(p)

		return len(p), nil
	}

	r.next++
	for i := range p {
		p[i] = r.next
	}

	return len(p), nil
}

// A clip's video is served for seeking: a range is answered with exactly that
// range, HEAD says how big it is, and nothing about it may be kept or run.
func TestClipVideoServesRanges(t *testing.T) {
	w, h := startedClipsWorld(t, "uid-bob", "uid-dave")
	clipsAdmins(w.api, "uid-boss")
	entry := publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil), false)

	fixture, err := os.ReadFile(clipFixture)
	require.NoError(t, err)
	size := strconv.Itoa(len(fixture))

	for _, target := range []string{clipsPath + "/video?id=" + clipA, adminClipsPath + "/video?id=" + clipA} {
		caller := "uid-dave"
		if strings.HasPrefix(target, adminClipsPath) {
			caller = "uid-boss"
		}

		response := w.call(t, caller, clipCall{method: http.MethodGet, target: target, header: map[string]string{"Range": "bytes=100-199"}})
		require.Equal(t, http.StatusPartialContent, response.Code, target)
		require.Equal(t, "bytes 100-199/"+size, response.Header().Get("Content-Range"))
		require.Equal(t, fixture[100:200], response.Body.Bytes())
		require.Equal(t, "100", response.Header().Get("Content-Length"))
		require.Equal(t, "video/mp4", response.Header().Get("Content-Type"))
		require.Equal(t, "private, no-cache", response.Header().Get("Cache-Control"))
		require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
		require.Equal(t, "default-src 'none'; sandbox", response.Header().Get("Content-Security-Policy"))
		require.Equal(t, `inline; filename="clip-`+clipA+`.mp4"`, response.Header().Get("Content-Disposition"))
		require.Equal(t, "bytes", response.Header().Get("Accept-Ranges"))

		response = w.call(t, caller, clipCall{method: http.MethodHead, target: target})
		require.Equal(t, http.StatusOK, response.Code, target)
		require.Equal(t, size, response.Header().Get("Content-Length"))
		require.Empty(t, response.Body.Bytes())

		response = w.call(t, caller, clipCall{method: http.MethodGet, target: target})
		require.Equal(t, http.StatusOK, response.Code, target)
		require.Equal(t, fixture, response.Body.Bytes())

		response = w.call(t, caller, clipCall{method: http.MethodGet, target: target, header: map[string]string{"Range": "bytes=" + size + "-"}})
		require.Equal(t, http.StatusRequestedRangeNotSatisfiable, response.Code, target)
	}

	// A malformed id, a clip that never was, one whose file has gone, and one
	// past its time.
	response := w.call(t, "uid-dave", clipCall{method: http.MethodGet, target: clipsPath + "/video?id=../../etc"})
	requireClipError(t, response, http.StatusBadRequest, clipErrBadRequest, "malformed")

	response = w.call(t, "uid-dave", clipCall{method: http.MethodGet, target: clipsPath + "/video?id=" + clipB})
	requireClipError(t, response, http.StatusGone, clipErrClipGone, "never was")

	require.NoError(t, os.Remove(filepath.Join(h.root, clipsFilesDir, clipA+clipVideoExt)))
	response = w.call(t, "uid-dave", clipCall{method: http.MethodGet, target: clipsPath + "/video?id=" + clipA})
	require.Equal(t, http.StatusNotFound, response.Code)
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))

	setClipsClock(w.p, time.UnixMilli(entry.CreatedAt).Add(31*24*time.Hour))
	response = w.call(t, "uid-dave", clipCall{method: http.MethodGet, target: clipsPath + "/video?id=" + clipA})
	requireClipError(t, response, http.StatusGone, clipErrClipGone, "expired")
}

// One person's clips may take a fifth of the space for clips, and never less
// than a gigabyte. What each person and everybody take is counted as clips
// come and go, and counted again from the disk by every sweep; past either
// bound, or below the disk's own floor, there is no room.
func TestPerPersonShare(t *testing.T) {
	for limitGB, wantGiB := range map[int64]int64{1: 1, 5: 1, 10: 2, 20: 4, 100: 20} {
		require.Equal(t, wantGiB<<30, clipShareBytes(limitGB<<30), "%d GB", limitGB)
	}

	w, h := startedClipsWorld(t, "uid-bob", "uid-dave")
	clipsAdmins(w.api, "uid-boss")
	cfg := w.p.getConfiguration().Clone()
	cfg.ClipStorageLimitGB = 10
	w.p.setConfiguration(cfg)

	first := publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-2*time.Hour), nil), true)
	second := publishFixtureClip(t, w.p, testClip(clipB, "uid-bob", clipsNow.Add(-time.Hour), nil), false)
	third := publishFixtureClip(t, w.p, testClip(clipC, "uid-dave", clipsNow.Add(-time.Hour), nil), false)

	picture := int64(len(testJPEG(t)))
	bobUses := first.Bytes + picture + second.Bytes

	s := w.p.clips()
	used, mine := s.usage("uid-bob")
	require.Equal(t, bobUses, mine)
	require.Equal(t, bobUses+third.Bytes, used)
	_, mine = s.usage("uid-dave")
	require.Equal(t, third.Bytes, mine)

	// Counted again from the disk, the same; a draft on the disk is
	// everybody's room, and nobody's share.
	writeAged(t, filepath.Join(h.root, clipsDraftsDir, clipD+".mp4"), make([]byte, 1000), clipsNow)
	s.recountUsage(w.stored(t))
	used, mine = s.usage("uid-bob")
	require.Equal(t, bobUses, mine)
	require.Equal(t, bobUses+third.Bytes+1000, used)

	response := w.call(t, "uid-boss", clipCall{method: http.MethodGet, target: adminClipsPath})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	view := decodeAdminClips(t, response)
	require.Equal(t, int64(10)<<30, view.LimitBytes)
	require.Equal(t, int64(2)<<30, view.ShareBytes)
	require.Equal(t, used, view.UsedBytes)
	require.EqualValues(t, 500<<30, view.FreeBytes)
	require.EqualValues(t, 1000<<30, view.TotalBytes)
	require.Equal(t, h.root, view.Root)
	bytesOf := map[string]int64{}
	for _, person := range view.People {
		bytesOf[person.UserID] = person.Bytes
	}
	require.Equal(t, map[string]int64{"uid-bob": bobUses, "uid-dave": third.Bytes}, bytesOf)

	// Room: inside the share and the limit; past the share; past the limit;
	// and past the disk's own floor, whatever the settings say.
	require.NoError(t, s.roomFor("uid-bob", 1<<30, cfg))
	requireRefusal(t, s.roomFor("uid-bob", 2<<30, cfg), http.StatusInsufficientStorage, clipErrUserStorageFull)
	s.addUsage("", 9<<30)
	requireRefusal(t, s.roomFor("uid-dave", 1<<30, cfg), http.StatusInsufficientStorage, clipErrStorageFull)
	s.addUsage("", -(9 << 30))
	s.freeSpace = func(string) (uint64, uint64, bool) { return 3 << 30, 20 << 30, true }
	require.NoError(t, s.roomFor("uid-dave", 512<<20, cfg))
	requireRefusal(t, s.roomFor("uid-dave", 1<<30+1, cfg), http.StatusInsufficientStorage, clipErrStorageFull)
	s.freeSpace = func(string) (uint64, uint64, bool) { return 10 << 30, 1000 << 30, true }
	// On a large disk the floor is a twentieth of it.
	requireRefusal(t, s.roomFor("uid-dave", 1, cfg), http.StatusInsufficientStorage, clipErrStorageFull)
	s.freeSpace = func(string) (uint64, uint64, bool) { return 0, 0, false }
	require.NoError(t, s.roomFor("uid-dave", 1<<30, cfg), "a disk that cannot be asked is not taken for a full one")

	// Deleting a clip gives its room back.
	s.freeSpace = func(string) (uint64, uint64, bool) { return 500 << 30, 1000 << 30, true }
	response = w.call(t, "uid-bob", clipCall{method: http.MethodDelete, target: clipsPath + "/clip?id=" + clipA})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	_, mine = s.usage("uid-bob")
	require.Equal(t, second.Bytes, mine)
}

// The sweep settles every clip past its time in one write, banking the
// upvotes that count, stores how long clips are kept, deletes the files of
// what it settled, and leaves everything else alone. A second sweep with
// nothing to do writes nothing.
func TestSweepExpiresBanksAndDeletesFiles(t *testing.T) {
	w, h := startedClipsWorld(t, "uid-bob", "uid-dave", "uid-dan")
	old := publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-31*24*time.Hour), votesAt(clipsNow.Add(-30*24*time.Hour), "uid-dave", "uid-dan")), true)
	young := publishFixtureClip(t, w.p, testClip(clipB, "uid-bob", clipsNow.Add(-time.Hour), nil), false)
	publishFixtureClip(t, w.p, testClip(clipC, "uid-dave", clipsNow.Add(-40*24*time.Hour), nil), false)
	revBefore := w.stored(t).Rev
	_, bobBefore := w.p.clips().usage("uid-bob")

	report := w.p.sweepClipsAt(context.Background(), clipsNow)
	require.True(t, report.Ran)
	require.Equal(t, 2, report.Expired)
	require.Zero(t, report.Deferred)
	require.Positive(t, report.Freed)

	doc := w.stored(t)
	require.Equal(t, revBefore+1, doc.Rev, "one write")
	require.Len(t, doc.Clips, 1)
	require.Equal(t, clipB, doc.Clips[0].ID)
	require.Equal(t, bankedScore{Up: 2, Clips: 1, LastUpAt: votesAt(clipsNow.Add(-30*24*time.Hour), "uid-dave", "uid-dan")["uid-dan"]}, doc.Banked["uid-bob"])
	require.Equal(t, bankedScore{Clips: 1}, doc.Banked["uid-dave"])
	require.Equal(t, retentionRecord{Days: 30}, doc.Retention)
	require.Equal(t, "uid-bob", doc.Holder.UserID, "the expired clip's upvotes still count")

	require.ElementsMatch(t, []string{clipB + ".mp4"}, namesIn(t, filepath.Join(h.root, clipsFilesDir)))
	_, bobAfter := w.p.clips().usage("uid-bob")
	require.Equal(t, young.Bytes, bobAfter)
	require.Less(t, bobAfter, bobBefore)
	_ = old

	w.api.AssertCalled(t, "LogInfo", "Clips swept", "expired", 2, "deferred", 0, "orphans", 0, "bytes", report.Freed)

	// Nothing more to do: nothing written.
	before := w.kvGet(kvClips)
	report = w.p.sweepClipsAt(context.Background(), clipsNow.Add(time.Minute))
	require.True(t, report.Ran)
	require.Zero(t, report.Expired)
	require.Equal(t, before, w.kvGet(kvClips))

	// The republish the sweep armed shows one clip left.
	w.api.On("PublishWebSocketEvent", mock.Anything, mock.Anything, mock.Anything).Return()
	h.timers.fire(t)
	require.Equal(t, 1, w.p.getSnapshot().Clips.Count)
}

// A sweep that could not read the document deletes no file, whatever it
// looks like: a store that did not answer, and a document that would not
// decode, which halts every write and every sweep after it.
func TestSweepNeverDeletesFilesWhenTheDocIsUnreadable(t *testing.T) {
	w, h := startedClipsWorld(t, "uid-bob")
	publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-40*24*time.Hour), nil), true)

	stale := clipsNow.Add(-48 * time.Hour)
	orphan := filepath.Join(h.root, clipsFilesDir, clipB+".mp4")
	leftover := filepath.Join(h.root, clipsFilesDir, ".tmp-"+clipC+"-0badf00d.mp4")
	writeAged(t, orphan, []byte("nobody's"), stale)
	writeAged(t, leftover, []byte("half"), stale)
	files := namesIn(t, filepath.Join(h.root, clipsFilesDir))

	w.faults.failRead(kvClips)
	report := w.p.sweepClipsAt(context.Background(), clipsNow)
	require.False(t, report.Ran)
	require.Equal(t, files, namesIn(t, filepath.Join(h.root, clipsFilesDir)))
	halted, _ := w.p.clips().halt.state()
	require.False(t, halted, "a store that did not answer is not a broken document")
	w.faults.healRead(kvClips)

	garbage := []byte(`{"v":1,"rev":`)
	w.kvSet(kvClips, garbage)
	report = w.p.sweepClipsAt(context.Background(), clipsNow)
	require.False(t, report.Ran)
	require.Equal(t, files, namesIn(t, filepath.Join(h.root, clipsFilesDir)))
	halted, _ = w.p.clips().halt.state()
	require.True(t, halted)

	report = w.p.sweepClipsAt(context.Background(), clipsNow.Add(time.Hour))
	require.False(t, report.Ran)
	require.Equal(t, files, namesIn(t, filepath.Join(h.root, clipsFilesDir)))
	require.Equal(t, garbage, w.kvGet(kvClips))
}

// A document that turns unreadable between the sweep's read and its own
// write halts Clips in that write, and the sweep stops there: it deletes no
// file on the strength of the copy it read a moment before. The same for a
// document a newer version of VillaNET wrote in that moment.
func TestSweepStopsWhenItsOwnWriteHalts(t *testing.T) {
	for _, c := range []struct {
		about   string
		written []byte
	}{
		{"broken", []byte(`{"v":1,"rev":`)},
		{"from a newer version", []byte(`{"v":2,"rev":99,"clips":[]}`)},
	} {
		t.Run(c.about, func(t *testing.T) {
			w, h := startedClipsWorld(t, "uid-bob")
			publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-time.Hour), nil), true)

			dir := filepath.Join(h.root, clipsFilesDir)
			stale := clipsNow.Add(-48 * time.Hour)
			writeAged(t, filepath.Join(dir, clipB+".mp4"), []byte("nobody's"), stale)
			writeAged(t, filepath.Join(dir, ".tmp-"+clipC+"-0badf00d.mp4"), []byte("half"), stale)
			files := namesIn(t, dir)

			// The sweep's own read answers as it was; the next read of the
			// document, which is its write's, finds it changed underneath.
			clipReads := 0
			w.faults.lock.Lock()
			w.faults.onRead = func(key string) {
				if key != kvClips {
					return
				}
				clipReads++
				if clipReads == 2 {
					w.store[kvClips] = c.written
				}
			}
			w.faults.lock.Unlock()

			report := w.p.sweepClipsAt(context.Background(), clipsNow)
			require.True(t, report.Ran, "the read before it was good")
			require.Zero(t, report.Orphans)
			require.Equal(t, 2, clipReads)

			halted, _ := w.p.clips().halt.state()
			require.True(t, halted)
			require.Equal(t, files, namesIn(t, dir), "nothing deleted once halted")
			require.Equal(t, c.written, w.kvGet(kvClips), "and nothing written over it")
		})
	}
}

// Files no clip accounts for go once they are a day old, and writes that
// never finished once they are an hour old. Anything younger is left, in
// case it belongs to a clip being published this moment; and so is anything
// the sweep does not recognise, and anything outside clips/.
func TestOrphansOlderThanADayAreRemoved(t *testing.T) {
	w, h := startedClipsWorld(t, "uid-bob")
	kept := publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-10*24*time.Hour), nil), false)
	dir := filepath.Join(h.root, clipsFilesDir)

	dayAndMore := clipsNow.Add(-25 * time.Hour)
	almostADay := clipsNow.Add(-23 * time.Hour)
	writeAged(t, filepath.Join(dir, clipA+".jpg"), []byte("a picture its clip no longer shows"), dayAndMore)
	writeAged(t, filepath.Join(dir, clipB+".mp4"), []byte("gone"), dayAndMore)
	writeAged(t, filepath.Join(dir, clipB+".jpg"), []byte("gone"), dayAndMore)
	writeAged(t, filepath.Join(dir, clipC+".mp4"), []byte("being published"), almostADay)
	writeAged(t, filepath.Join(dir, ".tmp-"+clipD+"-00000001.mp4"), []byte("abandoned"), clipsNow.Add(-2*time.Hour))
	writeAged(t, filepath.Join(dir, ".tmp-"+clipD+"-00000002.mp4"), []byte("in progress"), clipsNow.Add(-30*time.Minute))
	writeAged(t, filepath.Join(dir, "README.txt"), []byte("somebody else's"), dayAndMore)
	writeAged(t, filepath.Join(dir, "0123.mp4"), []byte("not an id"), dayAndMore)
	writeAged(t, filepath.Join(h.root, clipsDraftsDir, clipB+".mp4"), []byte("a draft"), dayAndMore)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, clipD+".mp4"), clipsDirMode))

	report := w.p.sweepClipsAt(context.Background(), clipsNow)
	require.True(t, report.Ran)
	require.Equal(t, 4, report.Orphans)
	require.EqualValues(t, len("a picture its clip no longer shows")+2*len("gone")+len("abandoned"), report.Freed)

	require.ElementsMatch(t, []string{
		kept.ID + ".mp4", clipC + ".mp4", ".tmp-" + clipD + "-00000002.mp4", "README.txt", "0123.mp4", clipD + ".mp4",
	}, namesIn(t, dir))
	require.Equal(t, []string{clipB + ".mp4"}, namesIn(t, filepath.Join(h.root, clipsDraftsDir)), "drafts are not the sweep's")

	w.api.AssertCalled(t, "LogInfo", "Clips swept", "expired", 0, "deferred", 0, "orphans", 4, "bytes", report.Freed)
}

// Settling is one write, and settling again changes nothing. A crash between
// that write and deleting the files banks the upvotes once and leaves the
// files to the sweep, which takes them once they are a day old.
func TestSettleIsOneWriteAndOrphansAreSwept(t *testing.T) {
	w, h := startedClipsWorld(t, "uid-bob", "uid-dave")
	expired := publishFixtureClip(t, w.p, testClip(clipA, "uid-bob", clipsNow.Add(-31*24*time.Hour), votesAt(clipsNow.Add(-30*24*time.Hour), "uid-dave")), true)
	rev := w.stored(t).Rev

	// The write lands, and the process goes before it deletes anything.
	outcome, err := w.p.settleClips(map[string]string{expired.ID: clipGoneExpired}, clipsNow)
	require.NoError(t, err)
	require.Len(t, outcome.Settled, 1)
	require.Equal(t, rev+1, w.stored(t).Rev)

	outcome, err = w.p.settleClips(map[string]string{expired.ID: clipGoneExpired}, clipsNow)
	require.NoError(t, err)
	require.Empty(t, outcome.Settled)
	require.Equal(t, rev+1, w.stored(t).Rev, "settling twice is settling once")
	require.Equal(t, bankedScore{Up: 1, Clips: 1, LastUpAt: clipsNow.Add(-30 * 24 * time.Hour).UnixMilli()}, w.stored(t).Banked["uid-bob"])

	files := []string{expired.ID + ".mp4", expired.ID + ".jpg"}
	dir := filepath.Join(h.root, clipsFilesDir)

	// The files are dated when the clip was published, a month ago; dated
	// as just written, they are left until they are a day old.
	for _, name := range files {
		require.NoError(t, os.Chtimes(filepath.Join(dir, name), clipsNow, clipsNow))
	}
	report := w.p.sweepClipsAt(context.Background(), clipsNow.Add(time.Hour))
	require.Zero(t, report.Orphans)
	require.ElementsMatch(t, files, namesIn(t, dir))

	report = w.p.sweepClipsAt(context.Background(), clipsNow.Add(25*time.Hour))
	require.Equal(t, 2, report.Orphans)
	require.Empty(t, namesIn(t, dir))
	require.Equal(t, bankedScore{Up: 1, Clips: 1, LastUpAt: clipsNow.Add(-30 * 24 * time.Hour).UnixMilli()}, w.stored(t).Banked["uid-bob"], "banked once")

	_, mine := w.p.clips().usage("uid-bob")
	require.Zero(t, mine)
}
