package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/stretchr/testify/require"
)

// Pictures kept on the disk are a cache that outlives a restart, like the
// remembered titles, so these tests restart too: a fresh Plugin, with nothing
// in memory, on the same KV store and the same directory. Time is a clock the
// test moves, and files are dated by it, so nothing waits on real time.
// Pictures are written in the background; settle waits for the writes.

const memoryOnlyLine = "Pictures of movies, shows and games are held in memory only, not kept on this server's disk"

// plentyOfRoom is a volume with room to spare, whatever machine runs the test.
func plentyOfRoom(string) (uint64, uint64, bool) {
	return 500 << 30, 1000 << 30, true
}

// pictureDiskIn activates p's pictures on local file storage under dir, at the
// clock's time, and is the directory they are kept in. The tidy activation
// starts has finished when it returns; the hour's tidy runs when the test says
// (hourlyTidy).
func pictureDiskIn(t *testing.T, p *Plugin, dir string, clock *fakeClock) string {
	t.Helper()

	api, ok := p.API.(*plugintest.API)
	require.True(t, ok)
	api.On("GetConfig").Return(clipsStorageConfig(dir, model.ImageDriverLocal))

	useClock(p, clock)
	d := p.mediaDisk()
	d.freeSpace = plentyOfRoom
	d.every = func(_ time.Duration, run func()) clipsJob {
		return &fakeClipsJob{run: run}
	}
	p.startMediaDisk(clock.now())
	settle(p)

	// A test that ends on a fetch has a write still landing: it lands before
	// the directory is cleaned away.
	t.Cleanup(func() { settle(p) })

	return filepath.Join(dir, manifestID, mediaDiskDir)
}

// hourlyTidy is the hour's tidy going off, by the store's clock, and waited
// for.
func hourlyTidy(t *testing.T, p *Plugin) {
	t.Helper()

	d := p.mediaDisk()
	d.lock.Lock()
	job, ok := d.tidyJob.(*fakeClipsJob)
	d.lock.Unlock()
	require.True(t, ok, "the hour's tidy is scheduled while there is a directory to tidy")

	job.lock.Lock()
	run := job.run
	job.lock.Unlock()

	run()
	settle(p)
}

// restartOnto is the server restarted, its pictures on the same directory.
func restartOnto(t *testing.T, p *Plugin, dir string, clock *fakeClock) *Plugin {
	t.Helper()

	next := restart(t, p)
	pictureDiskIn(t, next, dir, clock)

	return next
}

// settle waits for the pictures being written in the background.
func settle(nodes ...*Plugin) {
	for _, p := range nodes {
		p.mediaDisk().pending.Wait()
	}
}

// filesIn is every name in a directory, or none when there is no directory.
func filesIn(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names
}

func pictureFile(dir, ref, size string) string {
	return filepath.Join(dir, mediaDiskName(ref+"/"+size))
}

func memoryOnlyLines(p *Plugin) int {
	api, _ := p.API.(*plugintest.API)

	count := 0
	for _, call := range api.Calls {
		if call.Method == "LogInfo" && len(call.Arguments) > 0 && call.Arguments[0] == memoryOnlyLine {
			count++
		}
	}

	return count
}

func warningsAbout(p *Plugin, text string) int {
	api, _ := p.API.(*plugintest.API)

	count := 0
	for _, call := range api.Calls {
		if call.Method == "LogWarn" && strings.Contains(fmt.Sprint(call.Arguments...), text) {
			count++
		}
	}

	return count
}

func pictureSummary(t *testing.T, p *Plugin) (mediaCacheSummary, string) {
	t.Helper()

	response := adminCache(t, p, http.MethodGet, "an-admin")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var summary mediaCacheSummary
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &summary))

	return summary, response.Body.String()
}

// testPicture is a small picture of its own for n, so that two of them can be
// told apart.
func testPicture(n int) *mediaPicture {
	data := append(append([]byte(nil), jpegBytes...), bytes.Repeat([]byte{byte('a' + n%26)}, 1000)...)

	return &mediaPicture{contentType: "image/jpeg", data: data}
}

func testPictureKey(n int) string {
	return fmt.Sprintf("tmdb:movie:%d/thumb", n)
}

// keepNow keeps a picture as the relay would, believed for life, and waits for
// it to land.
func keepNow(p *Plugin, dir, key string, picture *mediaPicture, clock *fakeClock, life time.Duration) {
	now := clock.now()
	p.keepPicture(dir, key, picture, now, now.Add(life))
	settle(p)
}

func TestAPictureKeptOnTheDiskNeedsNobodyAfterARestart(t *testing.T) {
	fake := newMediaFake(t)
	heatSearch(fake)
	fake.json("/3/movie/949", heatDetails())
	fake.bytes("/t/p/w92/heat.jpg", jpegBytes)
	fake.bytes("/t/p/w342/heat.jpg", pngBytes)

	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	clock := newFakeClock()
	dir := t.TempDir()
	media := pictureDiskIn(t, p, dir, clock)

	mustSearch(t, p, "movie", "heat")
	require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "thumb").Code)
	require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "cover").Code)
	settle(p)
	require.ElementsMatch(t, []string{mediaDiskName("tmdb:movie:949/thumb"), mediaDiskName("tmdb:movie:949/cover")}, filesIn(t, media))

	// A restart: nothing in memory, not even where the pictures are.
	clock.advance(day)
	q := restartOnto(t, p, dir, clock)
	asked := len(fake.requests())

	// Somebody who has used up their pictures for the minute: what the disk
	// holds costs nobody anything, so it is not metered, as memory is not.
	for q.mediaStore().fetchLimiter.allow("uid-user") {
	}

	for size, want := range map[string][]byte{mediaSizeThumb: jpegBytes, mediaSizeCover: pngBytes} {
		response := mediaPictureOf(t, q, "tmdb:movie:949", size)
		require.Equal(t, http.StatusOK, response.Code, size)
		require.Equal(t, want, response.Body.Bytes(), size)
	}
	require.Len(t, fake.requests(), asked, "no outside request at all: not the pictures, not the title's details")
	require.Equal(t, http.StatusTooManyRequests, mediaPictureOf(t, q, "tmdb:movie:949", "backdrop").Code,
		"one that has to be fetched still is")

	// What the disk gave went into memory: with the files gone, the next
	// panel is still answered, and still without asking anybody.
	for _, name := range filesIn(t, media) {
		require.NoError(t, os.Remove(filepath.Join(media, name)))
	}
	require.Equal(t, http.StatusOK, mediaPictureOf(t, q, "tmdb:movie:949", "thumb").Code)
	require.Len(t, fake.requests(), asked)
}

func TestPicturesOnTheDiskGoStaleWithTheirTitles(t *testing.T) {
	type expiry struct {
		about  string
		config func() *configuration
		setup  func(fake *mediaFake)
		// prepare runs before the picture is first fetched: a search, so the
		// picture's address is known, and for some cases the title's details
		// a day before.
		prepare func(t *testing.T, p *Plugin)
		ref     string
		image   string
		fresh   time.Duration // after the first fetch: still served from the disk
		stale   time.Duration // after the first fetch: fetched again
	}

	// aDayLater moves p's clock on a day, past details just fetched.
	aDayLater := func(p *Plugin) {
		p.mediaStore().now = func() time.Time { return newFakeClock().now().Add(day) }
	}

	for _, tried := range []expiry{
		{
			// Nothing says how settled the title is, so its picture is
			// believed no longer than the least settled title is.
			about:  "a TMDB title nobody has looked up, known only from a search: three days",
			config: movieConfig,
			setup: func(fake *mediaFake) {
				heatSearch(fake)
				fake.json("/3/movie/949", heatDetails())
				fake.bytes("/t/p/w92/heat.jpg", jpegBytes)
			},
			prepare: func(t *testing.T, p *Plugin) { mustSearch(t, p, "movie", "heat") },
			ref:     "tmdb:movie:949", image: "/t/p/w92/heat.jpg",
			fresh: 3*day - time.Hour, stale: 3*day + time.Hour,
		},
		{
			// The details have gone from memory, as they do after six hours,
			// and the remembered title says how long it is believed.
			about:  "a TMDB title remembered from a day before, not in memory: the twenty-nine days it has left",
			config: movieConfig,
			setup: func(fake *mediaFake) {
				heatSearch(fake)
				fake.json("/3/movie/949", heatDetails())
				fake.bytes("/t/p/w92/heat.jpg", jpegBytes)
			},
			prepare: func(t *testing.T, p *Plugin) {
				_, err := detailsOf(t, p, "tmdb:movie:949")
				require.NoError(t, err)
				p.mediaStore().details.clear()
				aDayLater(p)
				mustSearch(t, p, "movie", "heat")
			},
			ref: "tmdb:movie:949", image: "/t/p/w92/heat.jpg",
			fresh: 29*day - time.Hour, stale: 29*day + time.Hour,
		},
		{
			about:  "an IGDB game, its details in hand: seven days",
			config: twitchConfig,
			setup: func(fake *mediaFake) {
				steamItems(fake)
				fake.igdbToken("tok", 50000000)
				fake.igdbGames(igdbCatalogue(hytaleRow()))
				fake.bytes("/igdb/image/upload/t_cover_small/cobc4t.jpg", jpegBytes)
			},
			prepare: func(t *testing.T, p *Plugin) {
				_, err := detailsOf(t, p, "igdb:113360")
				require.NoError(t, err)
				mustSearch(t, p, "game", "hytale")
			},
			ref: "igdb:113360", image: "/igdb/image/upload/t_cover_small/cobc4t.jpg",
			fresh: 7*day - time.Hour, stale: 7*day + time.Hour,
		},
		{
			about:  "an IGDB game known only from a search: three days",
			config: twitchConfig,
			setup: func(fake *mediaFake) {
				steamItems(fake)
				fake.igdbToken("tok", 50000000)
				fake.igdbGames(igdbCatalogue(hytaleRow()))
				fake.bytes("/igdb/image/upload/t_cover_small/cobc4t.jpg", jpegBytes)
			},
			prepare: func(t *testing.T, p *Plugin) { mustSearch(t, p, "game", "hytale") },
			ref:     "igdb:113360", image: "/igdb/image/upload/t_cover_small/cobc4t.jpg",
			fresh: 3*day - time.Hour, stale: 3*day + time.Hour,
		},
		{
			// The details were fetched the day before the picture, and a
			// film still being made is believed for three days: the
			// picture goes with the title, two days after it was fetched.
			about:  "a film not out yet, its details in hand: three days from the details",
			config: movieConfig,
			setup: func(fake *mediaFake) {
				heatSearch(fake)
				details := heatDetails()
				details["status"] = "Post Production"
				fake.json("/3/movie/949", details)
				fake.bytes("/t/p/w92/heat.jpg", jpegBytes)
			},
			prepare: func(t *testing.T, p *Plugin) {
				_, err := detailsOf(t, p, "tmdb:movie:949")
				require.NoError(t, err)
				aDayLater(p)
				mustSearch(t, p, "movie", "heat")
			},
			ref: "tmdb:movie:949", image: "/t/p/w92/heat.jpg",
			fresh: 2*day - time.Hour, stale: 2*day + time.Hour,
		},
		{
			// Read back from the remembered titles, which do not keep
			// whether a title is changing, only until when it is believed.
			about:  "a film not out yet, its details read back from the remembered titles: three days from the details",
			config: movieConfig,
			setup: func(fake *mediaFake) {
				details := heatDetails()
				details["status"] = "Post Production"
				fake.json("/3/movie/949", details)
				fake.bytes("/t/p/w92/heat.jpg", jpegBytes)
			},
			prepare: func(t *testing.T, p *Plugin) {
				_, err := detailsOf(t, p, "tmdb:movie:949")
				require.NoError(t, err)
				p.mediaStore().details.clear()
				aDayLater(p)
			},
			ref: "tmdb:movie:949", image: "/t/p/w92/heat.jpg",
			fresh: 2*day - time.Hour, stale: 2*day + time.Hour,
		},
	} {
		fake := newMediaFake(t)
		tried.setup(fake)

		p, _, _ := mediaPluginWithFaults(t, tried.config())
		clock := newFakeClock()
		dir := t.TempDir()
		media := pictureDiskIn(t, p, dir, clock)

		tried.prepare(t, p)
		started := p.mediaStore().now()
		require.Equal(t, http.StatusOK, mediaPictureOf(t, p, tried.ref, "thumb").Code, tried.about)
		settle(p)
		require.Equal(t, 1, fake.requestsTo(tried.image), tried.about)

		clock.advance(started.Sub(clock.now()) + tried.fresh)
		q := restartOnto(t, p, dir, clock)
		asked := len(fake.requests())
		require.Equal(t, http.StatusOK, mediaPictureOf(t, q, tried.ref, "thumb").Code, tried.about)
		require.Len(t, fake.requests(), asked, "%s: still fresh, so nobody is asked", tried.about)

		clock.advance(tried.stale - tried.fresh)
		r := restartOnto(t, q, dir, clock)
		require.Equal(t, http.StatusOK, mediaPictureOf(t, r, tried.ref, "thumb").Code, tried.about)
		require.Equal(t, 2, fake.requestsTo(tried.image), "%s: stale, so fetched again", tried.about)
		settle(r)

		kept := readPicture(media, tried.ref+"/thumb", clock.now())
		require.NotNil(t, kept, tried.about)
		require.Equal(t, clock.now().UnixMilli(), kept.at.UnixMilli(), "%s: the file is the new fetch", tried.about)
	}
}

func TestAnExpiredPictureStandsInOnlyWhileItsProviderFails(t *testing.T) {
	fake := newMediaFake(t)
	heatSearch(fake)
	fake.json("/3/movie/949", heatDetails())
	fake.bytes("/t/p/w92/heat.jpg", jpegBytes)
	steamItems(fake)
	fake.igdbToken("tok", 50000000)
	fake.igdbGames(igdbCatalogue(hytaleRow()))
	fake.bytes("/igdb/image/upload/t_cover_small/cobc4t.jpg", jpegBytes)

	cfg := twitchConfig()
	cfg.TMDBToken = testTMDBToken
	p, _, _ := mediaPluginWithFaults(t, cfg)
	clock := newFakeClock()
	dir := t.TempDir()
	media := pictureDiskIn(t, p, dir, clock)

	mustSearch(t, p, "movie", "heat")
	mustSearch(t, p, "game", "hytale")
	require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "thumb").Code)
	require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "igdb:113360", "thumb").Code)
	settle(p)

	heat := pictureFile(media, "tmdb:movie:949", "thumb")
	hytale := pictureFile(media, "igdb:113360", "thumb")
	require.FileExists(t, heat)
	require.FileExists(t, hytale)

	// Past their dates, with both image hosts failing.
	fake.status("/t/p/w92/heat.jpg", http.StatusServiceUnavailable)
	fake.status("/igdb/image/upload/t_cover_small/cobc4t.jpg", http.StatusServiceUnavailable)
	clock.advance(31 * day)
	q := restartOnto(t, p, dir, clock)

	response := mediaPictureOf(t, q, "tmdb:movie:949", "thumb")
	require.Equal(t, http.StatusOK, response.Code, "a TMDB picture a month old is better than none while TMDB is down")
	require.Equal(t, jpegBytes, response.Body.Bytes())
	require.FileExists(t, heat)

	response = mediaPictureOf(t, q, "igdb:113360", "thumb")
	require.Equal(t, http.StatusBadGateway, response.Code, "an IGDB picture is never served past IGDB's seven days")
	require.NoFileExists(t, hytale, "and it is not kept past them either")

	// Past the stale limit, nothing stands in at all.
	clock.advance(60 * day)
	r := restartOnto(t, q, dir, clock)
	require.Equal(t, http.StatusBadGateway, mediaPictureOf(t, r, "tmdb:movie:949", "thumb").Code)
	require.NoFileExists(t, heat)

	// A provider that answers there is no such picture any more is believed:
	// the disk's copy goes.
	fake.bytes("/t/p/w92/heat.jpg", jpegBytes)
	s := restartOnto(t, r, dir, clock)
	require.Equal(t, http.StatusOK, mediaPictureOf(t, s, "tmdb:movie:949", "thumb").Code)
	settle(s)
	require.FileExists(t, heat)

	fake.status("/t/p/w92/heat.jpg", http.StatusNotFound)
	clock.advance(31 * day)
	u := restartOnto(t, s, dir, clock)
	require.Equal(t, http.StatusNotFound, mediaPictureOf(t, u, "tmdb:movie:949", "thumb").Code)
	require.NoFileExists(t, heat)
}

func TestPicturesOnTheDiskStayWithinTheirBounds(t *testing.T) {
	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	clock := newFakeClock()
	media := pictureDiskIn(t, p, t.TempDir(), clock)
	require.NotEmpty(t, p.pictureDir())

	encoded, err := encodeMediaDisk(testPictureKey(1), testPicture(1), clock.now(), clock.now())
	require.NoError(t, err)
	size := int64(len(encoded))

	d := p.mediaDisk()
	d.maxBytes = 3*size + size/2
	d.maxEntries = 100

	keep := func(n int) {
		keepNow(p, media, testPictureKey(n), testPicture(n), clock, 30*day)
		clock.advance(2 * time.Hour)
	}
	names := func(ns ...int) []string {
		var all []string
		for _, n := range ns {
			all = append(all, mediaDiskName(testPictureKey(n)))
		}

		return all
	}
	total := func() int64 {
		bytes := int64(0)
		for _, name := range filesIn(t, media) {
			info, err := os.Stat(filepath.Join(media, name))
			require.NoError(t, err)
			bytes += info.Size()
		}

		return bytes
	}

	keep(1)
	keep(2)
	keep(3)
	require.ElementsMatch(t, names(1, 2, 3), filesIn(t, media))

	// 1 is looked at again, which leaves 2 the least recently used.
	require.NotNil(t, readPicture(media, testPictureKey(1), clock.now()))
	clock.advance(2 * time.Hour)

	keep(4)
	require.ElementsMatch(t, names(1, 3, 4), filesIn(t, media), "the least recently used went to make room")
	require.LessOrEqual(t, total(), d.maxBytes)

	// Writing over a picture already kept takes no room from the others.
	keep(4)
	require.ElementsMatch(t, names(1, 3, 4), filesIn(t, media))

	// And the number of pictures is bounded as well as their bytes.
	d.maxEntries = 2
	keep(5)
	require.ElementsMatch(t, names(4, 5), filesIn(t, media))

	// A picture that could never fit clears nothing out for nothing, and one
	// larger than the relay would ever serve is not written at all.
	d.maxBytes = size - 1
	keep(6)
	require.ElementsMatch(t, names(4, 5), filesIn(t, media))
	require.Equal(t, 1, warningsAbout(p, "could not be kept on the server's disk"))

	d.maxBytes, d.maxEntries = mediaDiskBytes, mediaDiskEntries
	huge := &mediaPicture{contentType: "image/jpeg", data: append(append([]byte(nil), jpegBytes...), make([]byte, maxMediaImageBytes)...)}
	p.keepPicture(media, testPictureKey(7), huge, clock.now(), clock.now().Add(day))
	settle(p)
	require.ElementsMatch(t, names(4, 5), filesIn(t, media))
}

func TestForgettingTheRememberedTitlesForgetsThePictures(t *testing.T) {
	fake := newMediaFake(t)
	heatSearch(fake)
	fake.bytes("/t/p/w92/heat.jpg", jpegBytes)

	p, _, _ := mediaPluginWithFaults(t, movieConfig(), "an-admin")
	clock := newFakeClock()
	media := pictureDiskIn(t, p, t.TempDir(), clock)

	mustSearch(t, p, "movie", "heat")
	require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "thumb").Code)
	settle(p)

	info, err := os.Stat(pictureFile(media, "tmdb:movie:949", "thumb"))
	require.NoError(t, err)

	summary, body := pictureSummary(t, p)
	require.True(t, summary.PicturesKept)
	require.Empty(t, summary.PictureProblem)
	require.Equal(t, 1, summary.Pictures)
	require.Equal(t, info.Size(), summary.PictureBytes)
	require.Equal(t, int64(268435456), summary.PictureLimitBytes)
	require.Equal(t, 4096, summary.PictureLimit)
	require.Contains(t, body, `"picturesKept":true`)
	require.Contains(t, body, `"pictureLimitBytes":268435456`)
	require.NotContains(t, body, "pictureProblem")

	// A write still under way on another node is left to finish.
	underWay := filepath.Join(media, clipTempPrefix+"media-0a0b0c0d"+mediaDiskExt)
	writeAged(t, underWay, []byte("half a picture"), clock.now())

	response := adminCache(t, p, http.MethodDelete, "an-admin")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"picturesForgotten":1`)
	require.Equal(t, []string{filepath.Base(underWay)}, filesIn(t, media))

	summary, _ = pictureSummary(t, p)
	require.Zero(t, summary.Pictures)
	require.Zero(t, summary.PictureBytes)

	// Forgotten everywhere: the next panel fetches it again.
	asked := fake.requestsTo("/t/p/w92/heat.jpg")
	mustSearch(t, p, "movie", "heat")
	require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "thumb").Code)
	require.Equal(t, asked+1, fake.requestsTo("/t/p/w92/heat.jpg"))
	settle(p)

	// Forgetting works with lookups switched off, as the rest of the card does.
	off := &configuration{}
	off.normalize()
	p.setConfiguration(off)
	response = adminCache(t, p, http.MethodDelete, "an-admin")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), `"picturesForgotten":1`)
	require.Equal(t, []string{filepath.Base(underWay)}, filesIn(t, media))
}

func TestPicturesStayInMemoryWithoutALocalDiskToKeepThemOn(t *testing.T) {
	for _, tried := range []struct {
		about   string
		config  func(dir string) *model.Config
		problem string
	}{
		{"object storage", func(dir string) *model.Config { return clipsStorageConfig(dir, "amazons3") }, "not Local file system"},
		{"no file settings", func(string) *model.Config { return nil }, "could not be read"},
		{"a directory that will not take a write", func(dir string) *model.Config {
			// Something already where the plugin's directory would be.
			if err := os.WriteFile(filepath.Join(dir, manifestID), []byte("in the way"), 0o600); err != nil {
				panic(err)
			}

			return clipsStorageConfig(dir, model.ImageDriverLocal)
		}, "will not take a write"},
		{"a file where the pictures' own directory would be", func(dir string) *model.Config {
			// The plugin's directory is there, for Clips, and media/ is not
			// a directory: there is nothing to forget, rather than a Forget
			// that fails every time.
			if err := os.MkdirAll(filepath.Join(dir, manifestID), clipsDirMode); err != nil {
				panic(err)
			}
			if err := os.WriteFile(filepath.Join(dir, manifestID, mediaDiskDir), []byte("in the way"), 0o600); err != nil {
				panic(err)
			}

			return clipsStorageConfig(dir, model.ImageDriverLocal)
		}, "will not take a write"},
	} {
		fake := newMediaFake(t)
		heatSearch(fake)
		fake.bytes("/t/p/w92/heat.jpg", jpegBytes)
		fake.bytes("/t/p/w342/heat.jpg", pngBytes)

		p, _, _ := mediaPluginWithFaults(t, movieConfig(), "an-admin")
		clock := newFakeClock()
		useClock(p, clock)
		dir := t.TempDir()
		api, _ := p.API.(*plugintest.API)
		api.On("GetConfig").Return(tried.config(dir))
		p.mediaDisk().freeSpace = plentyOfRoom
		before := filesIn(t, dir)
		p.startMediaDisk(clock.now())

		mustSearch(t, p, "movie", "heat")
		require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "thumb").Code, tried.about)
		require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "cover").Code, tried.about)
		require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "thumb").Code, tried.about)
		settle(p)

		require.Equal(t, before, filesIn(t, dir), "%s: nothing is made on the disk", tried.about)
		require.Equal(t, 1, memoryOnlyLines(p), "%s: said once", tried.about)
		require.Equal(t, 2, fake.requestsTo("/t/p/w92/heat.jpg")+fake.requestsTo("/t/p/w342/heat.jpg"),
			"%s: memory still holds them, exactly as before", tried.about)

		summary, _ := pictureSummary(t, p)
		require.False(t, summary.PicturesKept, tried.about)
		require.Contains(t, summary.PictureProblem, tried.problem, tried.about)
		require.Zero(t, summary.Pictures, tried.about)

		response := adminCache(t, p, http.MethodDelete, "an-admin")
		require.Equal(t, http.StatusOK, response.Code, tried.about)
		require.Contains(t, response.Body.String(), `"picturesForgotten":0`, tried.about)
	}
}

func TestPictureFilesAreNamedByAHashAlone(t *testing.T) {
	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	clock := newFakeClock()
	dir := t.TempDir()
	media := pictureDiskIn(t, p, dir, clock)
	require.NotEmpty(t, p.pictureDir())

	keys := []string{"tmdb:movie:949/thumb", "tmdb:tv:1396/cover", "steam:1245620/backdrop", "igdb:113360/cover"}
	for i, key := range keys {
		keepNow(p, media, key, testPicture(i), clock, day)
	}

	shape := regexp.MustCompile(`^[0-9a-f]{32}\.pic$`)
	names := filesIn(t, media)
	require.Len(t, names, len(keys), "one file each")
	for _, name := range names {
		require.Regexp(t, shape, name)
		for _, word := range []string{"tmdb", "steam", "igdb", "movie", "tv", "thumb", "cover", "backdrop", ":", "/"} {
			require.NotContains(t, strings.TrimSuffix(name, mediaDiskExt), word)
		}

		info, err := os.Stat(filepath.Join(media, name))
		require.NoError(t, err)
		require.Zero(t, info.Mode().Perm()&^fs.FileMode(clipsFileMode), "never more open than 0600")
	}

	info, err := os.Stat(media)
	require.NoError(t, err)
	require.Zero(t, info.Mode().Perm()&^fs.FileMode(clipsDirMode), "never more open than 0750")
	require.Equal(t, []string{manifestID}, filesIn(t, dir))
	require.Equal(t, []string{mediaDiskDir}, filesIn(t, filepath.Join(dir, manifestID)), "only media/: Clips make their own")
}

func TestUnfinishedWritesAreSweptAtStartupAndEveryHour(t *testing.T) {
	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	clock := newFakeClock()
	dir := t.TempDir()
	media := filepath.Join(dir, manifestID, mediaDiskDir)

	old := filepath.Join(media, clipTempPrefix+"media-00000001"+mediaDiskExt)
	young := filepath.Join(media, clipTempPrefix+"media-00000002"+mediaDiskExt)
	notOurs := filepath.Join(media, "notes.txt")
	kept := pictureFile(media, "tmdb:movie:949", "thumb")
	torn := pictureFile(media, "tmdb:movie:1", "thumb")

	whole, err := encodeMediaDisk("tmdb:movie:949/thumb", testPicture(1), clock.now().Add(-2*day), clock.now().Add(28*day))
	require.NoError(t, err)

	writeAged(t, old, []byte("left by a crash"), clock.now().Add(-2*time.Hour))
	writeAged(t, young, []byte("still being written"), clock.now().Add(-10*time.Minute))
	writeAged(t, notOurs, []byte("somebody's"), clock.now().Add(-48*time.Hour))
	writeAged(t, kept, whole, clock.now().Add(-48*time.Hour))
	writeAged(t, torn, []byte("half a picture"), clock.now().Add(-48*time.Hour))

	pictureDiskIn(t, p, dir, clock)

	require.ElementsMatch(t, []string{filepath.Base(young), filepath.Base(notOurs), filepath.Base(kept)}, filesIn(t, media),
		"activation's tidy: the old temporary file and the broken picture go")

	// The young one was a write cut off by the restart a moment before: no
	// node is writing it any more, and with no picture written since, the
	// hour's tidy takes it once it is old.
	clock.advance(30 * time.Minute)
	hourlyTidy(t, p)
	require.FileExists(t, young, "not yet an hour since the last tidy")

	clock.advance(time.Hour)
	hourlyTidy(t, p)
	require.ElementsMatch(t, []string{filepath.Base(notOurs), filepath.Base(kept)}, filesIn(t, media))

	// And a picture written an hour or more after the last tidy sweeps too.
	another := filepath.Join(media, clipTempPrefix+"media-00000003"+mediaDiskExt)
	writeAged(t, another, []byte("cut off"), clock.now().Add(-2*time.Hour))
	clock.advance(time.Hour)
	keepNow(p, media, testPictureKey(2), testPicture(2), clock, day)
	require.NoFileExists(t, another)
}

func TestTwoNodesKeepingOnePictureLeaveOneWholeFile(t *testing.T) {
	fake := newMediaFake(t)
	heatSearch(fake)

	// Both nodes' fetches are held until both have asked, so they really are
	// at the same time.
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	var open sync.Once
	t.Cleanup(func() { open.Do(func() { close(release) }) })
	fake.route("/t/p/w92/heat.jpg", func(w http.ResponseWriter, _ *http.Request) {
		arrived <- struct{}{}
		<-release
		_, _ = w.Write(jpegBytes)
	})

	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	clock := newFakeClock()
	dir := t.TempDir()
	media := pictureDiskIn(t, p, dir, clock)
	q := restartOnto(t, p, dir, clock)

	mustSearch(t, p, "movie", "heat")
	mustSearch(t, q, "movie", "heat")

	codes := make([]int, 2)
	var done sync.WaitGroup
	for i, node := range []*Plugin{p, q} {
		done.Add(1)
		go func() {
			defer done.Done()
			codes[i] = mediaPictureOf(t, node, "tmdb:movie:949", "thumb").Code
		}()
	}
	<-arrived
	<-arrived
	open.Do(func() { close(release) })
	done.Wait()
	settle(p, q)

	require.Equal(t, []int{http.StatusOK, http.StatusOK}, codes)
	require.Equal(t, 2, fake.requestsTo("/t/p/w92/heat.jpg"), "each node fetched it once, which is allowed")
	require.Equal(t, []string{mediaDiskName("tmdb:movie:949/thumb")}, filesIn(t, media), "one file, and no temporary one left")
	kept := readPicture(media, "tmdb:movie:949/thumb", clock.now())
	require.NotNil(t, kept)
	require.Equal(t, jpegBytes, kept.picture.data)

	// Many writes of one picture at once, from both nodes, each a different
	// picture: whichever lands last, the file is one of them, whole.
	key := testPictureKey(42)
	for i := range 16 {
		node := p
		if i%2 == 1 {
			node = q
		}
		node.keepPicture(media, key, testPicture(i), clock.now(), clock.now().Add(day))
	}
	settle(p, q)

	kept = readPicture(media, key, clock.now())
	require.NotNil(t, kept)
	whole := false
	for i := range 16 {
		whole = whole || bytes.Equal(testPicture(i).data, kept.picture.data)
	}
	require.True(t, whole, "one of the pictures written, not a mix of them")
	require.Len(t, filesIn(t, media), 2)
	for _, name := range filesIn(t, media) {
		require.False(t, strings.HasPrefix(name, clipTempPrefix), name)
	}
}

func TestPicturesAreNotKeptOnANearlyFullDisk(t *testing.T) {
	fake := newMediaFake(t)
	heatSearch(fake)
	fake.bytes("/t/p/w92/heat.jpg", jpegBytes)

	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	clock := newFakeClock()
	media := pictureDiskIn(t, p, t.TempDir(), clock)
	d := p.mediaDisk()

	// Clips' floor: 2 GB, or 5% of a large volume, whichever is more.
	for _, tried := range []struct {
		about       string
		free, total uint64
	}{
		{"just over 2 GB free on a small volume", 2<<30 + 100, 20 << 30},
		{"40 GB free on a 1 TB volume", 40 << 30, 1000 << 30},
	} {
		d.freeSpace = func(string) (uint64, uint64, bool) { return tried.free, tried.total, true }
		keepNow(p, media, testPictureKey(1), testPicture(1), clock, day)
		require.Empty(t, filesIn(t, media), tried.about)
	}
	require.Equal(t, 1, warningsAbout(p, "the disk is nearly full"), "said once in a while, not per picture")

	// The relay still answers, from memory.
	mustSearch(t, p, "movie", "heat")
	require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "thumb").Code)
	settle(p)
	require.Empty(t, filesIn(t, media))

	d.freeSpace = plentyOfRoom
	keepNow(p, media, testPictureKey(1), testPicture(1), clock, day)
	require.Len(t, filesIn(t, media), 1)
}

func TestClipsNeverTouchThePictures(t *testing.T) {
	w, h := startedClipsWorld(t, "uid-bob")

	picture := pictureFile(filepath.Join(h.root, mediaDiskDir), "tmdb:movie:949", "thumb")
	writeAged(t, picture, []byte("a picture"), clipsNow.Add(-400*24*time.Hour))
	temp := filepath.Join(h.root, mediaDiskDir, clipTempPrefix+"media-00000003"+mediaDiskExt)
	writeAged(t, temp, []byte("a picture being written"), clipsNow.Add(-400*24*time.Hour))

	w.p.wipeClipsScratch()
	w.p.sweepClipsAt(t.Context(), clipsNow.Add(400*24*time.Hour))
	w.p.stopClips()
	w.p.startClips()

	require.FileExists(t, picture)
	require.FileExists(t, temp)
}

func TestPicturesOnTheDiskAreLeftAloneWhileLookupsAreOff(t *testing.T) {
	fake := newMediaFake(t)
	heatSearch(fake)
	fake.bytes("/t/p/w92/heat.jpg", jpegBytes)

	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	clock := newFakeClock()
	dir := t.TempDir()
	media := pictureDiskIn(t, p, dir, clock)

	mustSearch(t, p, "movie", "heat")
	require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "thumb").Code)
	settle(p)

	heat := pictureFile(media, "tmdb:movie:949", "thumb")
	content, err := os.ReadFile(heat)
	require.NoError(t, err)

	// Dated two days back, so that a read would mark it used and show.
	stamp := clock.now().Add(-2 * day)
	require.NoError(t, os.Chtimes(heat, stamp, stamp))

	q := restartOnto(t, p, dir, clock)
	off := &configuration{TMDBToken: testTMDBToken}
	off.normalize()
	q.setConfiguration(off)
	asked := len(fake.requests())

	require.Equal(t, http.StatusNotFound, mediaPictureOf(t, q, "tmdb:movie:949", "thumb").Code)
	require.Len(t, fake.requests(), asked)

	info, err := os.Stat(heat)
	require.NoError(t, err)
	require.True(t, info.ModTime().Equal(stamp), "not read")
	after, err := os.ReadFile(heat)
	require.NoError(t, err)
	require.Equal(t, content, after, "not written")
	require.Equal(t, []string{filepath.Base(heat)}, filesIn(t, media), "nothing added, nothing taken away")

	// Switched on again, what is still fresh is served at once.
	on := movieConfig()
	on.normalize()
	q.setConfiguration(on)
	response := mediaPictureOf(t, q, "tmdb:movie:949", "thumb")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, jpegBytes, response.Body.Bytes())
	require.Len(t, fake.requests(), asked)

	// And a server that has never had lookups on never makes the directory.
	fresh := t.TempDir()
	r, _, _ := mediaPluginWithFaults(t, off)
	pictureDiskIn(t, r, fresh, clock)
	require.Equal(t, http.StatusNotFound, mediaPictureOf(t, r, "tmdb:movie:949", "thumb").Code)
	require.Empty(t, filesIn(t, fresh))
}

func TestATornOrMislabelledPictureIsNeverServed(t *testing.T) {
	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	clock := newFakeClock()
	media := pictureDiskIn(t, p, t.TempDir(), clock)
	require.NotEmpty(t, p.pictureDir())

	key := testPictureKey(1)
	whole, err := encodeMediaDisk(key, testPicture(1), clock.now(), clock.now().Add(day))
	require.NoError(t, err)
	another, err := encodeMediaDisk(testPictureKey(2), testPicture(2), clock.now(), clock.now().Add(day))
	require.NoError(t, err)
	svg, err := encodeMediaDisk(key, &mediaPicture{contentType: "image/svg+xml", data: []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`)}, clock.now(), clock.now().Add(day))
	require.NoError(t, err)

	flipped := append([]byte(nil), whole...)
	flipped[len(flipped)-1] ^= 0xff

	for _, tried := range []struct {
		about   string
		content []byte
	}{
		{"cut short", whole[:len(whole)-10]},
		{"a byte of the picture changed", flipped},
		{"another picture's file under this name", another},
		{"an SVG", svg},
		{"empty", nil},
		{"not a picture file at all", []byte("hello")},
		{"a header and nothing more", whole[:bytes.IndexByte(whole[len(mediaDiskMagic)+2:], '\n')+len(mediaDiskMagic)+3]},
		{"larger than any picture's file can be", append(append([]byte(nil), whole...), make([]byte, maxMediaDiskFile)...)},
	} {
		path := filepath.Join(media, mediaDiskName(key))
		require.NoError(t, os.WriteFile(path, tried.content, clipsFileMode))

		require.Nil(t, readPicture(media, key, clock.now()), tried.about)
		require.NoFileExists(t, path, "%s: of no use to anybody, so it goes", tried.about)
	}

	// A newer build's file is a miss, and is left exactly as it is: never
	// removed, never written over.
	newer := append([]byte(mediaDiskMagic+"2\n"), whole[len(mediaDiskMagic)+2:]...)
	path := filepath.Join(media, mediaDiskName(key))
	require.NoError(t, os.WriteFile(path, newer, clipsFileMode))
	require.Nil(t, readPicture(media, key, clock.now()))
	keepNow(p, media, key, testPicture(3), clock, day)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, newer, after)

	// Through the relay: a torn file is fetched again, and replaced.
	fake := newMediaFake(t)
	heatSearch(fake)
	fake.bytes("/t/p/w92/heat.jpg", jpegBytes)
	mustSearch(t, p, "movie", "heat")
	heat := pictureFile(media, "tmdb:movie:949", "thumb")
	good, err := encodeMediaDisk("tmdb:movie:949/thumb", &mediaPicture{contentType: "image/jpeg", data: jpegBytes}, clock.now(), clock.now().Add(day))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(heat, good[:len(good)-1], clipsFileMode))

	response := mediaPictureOf(t, p, "tmdb:movie:949", "thumb")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, jpegBytes, response.Body.Bytes())
	require.Equal(t, 1, fake.requestsTo("/t/p/w92/heat.jpg"))
	settle(p)
	require.NotNil(t, readPicture(media, "tmdb:movie:949/thumb", clock.now()), "whole again")
}

func TestAPictureGoesWithItsTitle(t *testing.T) {
	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	clock := newFakeClock()
	media := pictureDiskIn(t, p, t.TempDir(), clock)
	require.NotEmpty(t, p.pictureDir())

	for i, key := range []string{"tmdb:movie:949/thumb", "tmdb:movie:949/backdrop", "tmdb:movie:1/thumb"} {
		keepNow(p, media, key, testPicture(i), clock, 30*day)
	}

	ref, _ := parseMediaRef("tmdb:movie:949")
	_, err := p.titleNotFound(ref, nil)
	require.ErrorIs(t, err, errMediaNotFound)

	require.Equal(t, []string{mediaDiskName("tmdb:movie:1/thumb")}, filesIn(t, media), "the other title's picture stays")
}

func TestPicturesThatMayNoLongerBeKeptAreTidiedAway(t *testing.T) {
	p, _, _ := mediaPluginWithFaults(t, twitchConfig())
	clock := newFakeClock()
	media := pictureDiskIn(t, p, t.TempDir(), clock)
	require.NotEmpty(t, p.pictureDir())

	igdb := "igdb:113360/cover"
	fresh := "tmdb:movie:949/cover"
	stale := "tmdb:movie:1/cover"
	keepNow(p, media, igdb, testPicture(1), clock, 7*day)
	keepNow(p, media, fresh, testPicture(2), clock, 100*day)
	keepNow(p, media, stale, testPicture(3), clock, day)

	// Not whole, under its own name; another picture's file under a name not
	// its own; and a newer build's.
	torn := filepath.Join(media, mediaDiskName("tmdb:movie:2/cover"))
	require.NoError(t, os.WriteFile(torn, []byte(mediaDiskMagic+"1\n{"), clipsFileMode))
	moved := filepath.Join(media, mediaDiskName("tmdb:movie:3/cover"))
	misnamed, err := encodeMediaDisk("tmdb:movie:4/cover", testPicture(4), clock.now(), clock.now().Add(day))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(moved, misnamed, clipsFileMode))
	newer := filepath.Join(media, mediaDiskName("tmdb:movie:5/cover"))
	require.NoError(t, os.WriteFile(newer, []byte(mediaDiskMagic+"2\nwhatever comes next"), clipsFileMode))

	// The tidy runs when a picture is written, at most once an hour: the
	// first write since starting already ran it, so nothing goes yet.
	clock.advance(30 * time.Minute)
	keepNow(p, media, testPictureKey(10), testPicture(10), clock, day)
	require.FileExists(t, torn)

	// Eight days on: the IGDB picture is past its seven days, and goes. The
	// TMDB picture past its date stays, to stand in if TMDB is down.
	clock.advance(8 * day)
	keepNow(p, media, testPictureKey(11), testPicture(11), clock, day)
	left := filesIn(t, media)
	require.NotContains(t, left, mediaDiskName(igdb))
	require.Contains(t, left, mediaDiskName(fresh))
	require.Contains(t, left, mediaDiskName(stale))
	require.NotContains(t, left, filepath.Base(torn))
	require.NotContains(t, left, filepath.Base(moved))
	require.Contains(t, left, filepath.Base(newer), "a newer build's is left alone")

	// Past the stale limit, nothing stays, whatever it said.
	clock.advance(90 * day)
	keepNow(p, media, testPictureKey(12), testPicture(12), clock, day)
	left = filesIn(t, media)
	require.NotContains(t, left, mediaDiskName(fresh))
	require.NotContains(t, left, mediaDiskName(stale))
	require.Contains(t, left, mediaDiskName(testPictureKey(12)))
}

// memoryExpiry is when memory lets go of a picture, by the real clock memory
// keeps.
func memoryExpiry(t *testing.T, p *Plugin, key string) time.Time {
	t.Helper()

	cache := p.mediaStore().pictures
	cache.lock.Lock()
	defer cache.lock.Unlock()

	entry, ok := cache.entries[key]
	require.True(t, ok, key)

	return entry.expires
}

func TestAPictureDatedAheadOfThisClockIsNotBelieved(t *testing.T) {
	fake := newMediaFake(t)
	steamItems(fake)
	fake.igdbToken("tok", 50000000)
	fake.igdbGames(igdbCatalogue(hytaleRow()))
	fake.bytes("/igdb/image/upload/t_cover_small/cobc4t.jpg", jpegBytes)

	p, _, _ := mediaPluginWithFaults(t, twitchConfig())
	clock := newFakeClock()
	media := pictureDiskIn(t, p, t.TempDir(), clock)
	require.NotEmpty(t, p.pictureDir())

	write := func(key string, at, until time.Time) string {
		encoded, err := encodeMediaDisk(key, testPicture(1), at, until)
		require.NoError(t, err)

		path := filepath.Join(media, mediaDiskName(key))
		require.NoError(t, os.WriteFile(path, encoded, clipsFileMode))

		return path
	}
	now := clock.now()

	// Written by a node whose clock was a year fast, or by this one before
	// its clock was put back: neither fresh nor a stand-in.
	hytale := write("igdb:113360/thumb", now.Add(365*day), now.Add(372*day))
	kept := readPicture(media, "igdb:113360/thumb", now)
	require.NotNil(t, kept)
	require.False(t, kept.fresh(now))
	require.False(t, pictureMayFallBack("tmdb:movie:949/thumb", kept.at, now), "not even a TMDB one")

	// So the relay fetches it again, and the file is one dated now.
	mustSearch(t, p, "game", "hytale")
	response := mediaPictureOf(t, p, "igdb:113360", "thumb")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, jpegBytes, response.Body.Bytes())
	require.Equal(t, 1, fake.requestsTo("/igdb/image/upload/t_cover_small/cobc4t.jpg"))
	settle(p)
	kept = readPicture(media, "igdb:113360/thumb", now)
	require.NotNil(t, kept)
	require.Equal(t, now.UnixMilli(), kept.at.UnixMilli())
	require.FileExists(t, hytale)

	// A little ahead is only clocks that disagree, and is believed. However
	// long any node said a picture was good for, it is believed no longer
	// after it was fetched than its provider's titles ever are.
	ahead := write("tmdb:movie:2/cover", now.Add(5*time.Minute), now.Add(day))
	farIGDB := write("igdb:2/cover", now, now.Add(365*day))
	farTMDB := write("tmdb:movie:3/cover", now, now.Add(365*day))
	future := write("tmdb:movie:4/cover", now.Add(200*day), now.Add(230*day))

	require.True(t, readPicture(media, "tmdb:movie:2/cover", now).fresh(now))
	require.Equal(t, now.Add(7*day).UnixMilli(), readPicture(media, "igdb:2/cover", now).until.UnixMilli())
	require.Equal(t, now.Add(30*day).UnixMilli(), readPicture(media, "tmdb:movie:3/cover", now).until.UnixMilli())

	// And the tidy takes what may not be kept, asked for or not.
	clock.advance(2 * time.Hour)
	hourlyTidy(t, p)
	require.NoFileExists(t, future, "dated after now: neither fresh nor a stand-in")
	require.FileExists(t, ahead)
	require.FileExists(t, farIGDB)

	clock.advance(7 * day)
	hourlyTidy(t, p)
	require.NoFileExists(t, farIGDB, "IGDB art past its seven days, whatever the file said")
	require.FileExists(t, farTMDB, "a TMDB picture past its date may still stand in")

	// This node's clock put back a year: the last tidy is dated a year
	// ahead, which is no reason to wait a year for the next.
	clock.advance(-365 * day)
	expired := write("igdb:3/cover", clock.now().Add(-8*day), clock.now().Add(-day))
	hourlyTidy(t, p)
	require.NoFileExists(t, expired)
}

func TestAnExpiredPictureStandsInUnlessTheProviderSaysItIsGone(t *testing.T) {
	webPage := []byte("<!doctype html><html><body>Service temporarily unavailable</body></html>")

	for _, tried := range []struct {
		about string
		// api and image are how TMDB's API and its image host answer, a
		// month after the picture was kept.
		api, image func(w http.ResponseWriter)
		standsIn   bool
	}{
		{
			about:    "TMDB down, and something in the way answering a web page for the picture",
			api:      func(w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) },
			image:    func(w http.ResponseWriter) { _, _ = w.Write(webPage) },
			standsIn: true,
		},
		{
			about:    "TMDB down, and the old address gone: the new one cannot be found out",
			api:      func(w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) },
			image:    func(w http.ResponseWriter) { w.WriteHeader(http.StatusNotFound) },
			standsIn: true,
		},
		{
			about: "the image host answering 403, which a firewall does too",
			api: func(w http.ResponseWriter) {
				body, _ := json.Marshal(heatDetails())
				_, _ = w.Write(body)
			},
			image:    func(w http.ResponseWriter) { w.WriteHeader(http.StatusForbidden) },
			standsIn: true,
		},
		{
			about: "the image host answering a web page",
			api: func(w http.ResponseWriter) {
				body, _ := json.Marshal(heatDetails())
				_, _ = w.Write(body)
			},
			image:    func(w http.ResponseWriter) { _, _ = w.Write(webPage) },
			standsIn: true,
		},
		{
			about: "TMDB standing by the address, and the image host saying 404: gone",
			api: func(w http.ResponseWriter) {
				body, _ := json.Marshal(heatDetails())
				_, _ = w.Write(body)
			},
			image:    func(w http.ResponseWriter) { w.WriteHeader(http.StatusNotFound) },
			standsIn: false,
		},
	} {
		fake := newMediaFake(t)
		fake.json("/3/movie/949", heatDetails())
		fake.bytes("/t/p/w92/heat.jpg", jpegBytes)

		p, _, _ := mediaPluginWithFaults(t, movieConfig())
		clock := newFakeClock()
		dir := t.TempDir()
		media := pictureDiskIn(t, p, dir, clock)

		// The title remembered, and its thumb kept, a month ago.
		_, err := detailsOf(t, p, "tmdb:movie:949")
		require.NoError(t, err, tried.about)
		require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "thumb").Code, tried.about)
		settle(p)
		heat := pictureFile(media, "tmdb:movie:949", "thumb")
		require.FileExists(t, heat, tried.about)

		fake.route("/3/movie/949", func(w http.ResponseWriter, _ *http.Request) { tried.api(w) })
		fake.route("/t/p/w92/heat.jpg", func(w http.ResponseWriter, _ *http.Request) { tried.image(w) })
		clock.advance(31 * day)
		q := restartOnto(t, p, dir, clock)

		response := mediaPictureOf(t, q, "tmdb:movie:949", "thumb")
		settle(q)
		if tried.standsIn {
			require.Equal(t, http.StatusOK, response.Code, tried.about)
			require.Equal(t, jpegBytes, response.Body.Bytes(), tried.about)
			require.FileExists(t, heat, "%s: nothing said it is gone, so it is kept", tried.about)
		} else {
			require.Equal(t, http.StatusNotFound, response.Code, tried.about)
			require.NoFileExists(t, heat, tried.about)
		}
	}

	// The title still believed, but its details more than a day old, and the
	// picture's old address gone while TMDB is down: asking again for the
	// details hands back what was remembered, which is no proof either.
	fake := newMediaFake(t)
	heatSearch(fake)
	fake.json("/3/movie/949", heatDetails())
	fake.bytes("/t/p/w92/heat.jpg", jpegBytes)

	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	clock := newFakeClock()
	dir := t.TempDir()
	media := pictureDiskIn(t, p, dir, clock)

	mustSearch(t, p, "movie", "heat")
	require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "thumb").Code)
	_, err := detailsOf(t, p, "tmdb:movie:949")
	require.NoError(t, err)
	settle(p)
	heat := pictureFile(media, "tmdb:movie:949", "thumb")

	fake.status("/3/movie/949", http.StatusServiceUnavailable)
	fake.status("/t/p/w92/heat.jpg", http.StatusNotFound)
	clock.advance(4 * day)
	q := restartOnto(t, p, dir, clock)

	response := mediaPictureOf(t, q, "tmdb:movie:949", "thumb")
	settle(q)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, jpegBytes, response.Body.Bytes())
	require.Equal(t, 2, fake.requestsTo("/3/movie/949"), "the details were asked for again")
	require.FileExists(t, heat)

	// Remembered details past their date, standing in while TMDB is down,
	// that name no picture of this size: no proof there is none either.
	fake = newMediaFake(t)
	fake.json("/3/movie/77", map[string]any{"title": "No Art", "status": "Released", "release_date": "1990-01-01"})

	p, _, _ = mediaPluginWithFaults(t, movieConfig())
	clock = newFakeClock()
	dir = t.TempDir()
	media = pictureDiskIn(t, p, dir, clock)

	_, err = detailsOf(t, p, "tmdb:movie:77")
	require.NoError(t, err)
	require.NotEmpty(t, p.pictureDir())
	keepNow(p, media, "tmdb:movie:77/backdrop", testPicture(1), clock, 30*day)
	backdrop := pictureFile(media, "tmdb:movie:77", "backdrop")

	fake.status("/3/movie/77", http.StatusServiceUnavailable)
	clock.advance(31 * day)
	q = restartOnto(t, p, dir, clock)

	response = mediaPictureOf(t, q, "tmdb:movie:77", "backdrop")
	settle(q)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, testPicture(1).data, response.Body.Bytes())
	require.FileExists(t, backdrop)

	// The same, with nothing remembered to hand back, so that asking again
	// fails outright: the details this node still holds are no proof.
	fake = newMediaFake(t)
	heatSearch(fake)
	fake.json("/3/movie/949", heatDetails())
	fake.bytes("/t/p/w92/heat.jpg", jpegBytes)

	p, store, faults := mediaPluginWithFaults(t, movieConfig())
	clock = newFakeClock()
	media = pictureDiskIn(t, p, t.TempDir(), clock)

	mustSearch(t, p, "movie", "heat")
	require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "thumb").Code)
	_, err = detailsOf(t, p, "tmdb:movie:949")
	require.NoError(t, err)
	settle(p)
	heat = pictureFile(media, "tmdb:movie:949", "thumb")

	faults.lock.Lock()
	for _, shard := range mediaShardKeys() {
		delete(store, shard)
	}
	faults.lock.Unlock()
	p.mediaStore().pictures.clear()

	fake.status("/3/movie/949", http.StatusServiceUnavailable)
	fake.status("/t/p/w92/heat.jpg", http.StatusNotFound)
	clock.advance(4 * day)

	response = mediaPictureOf(t, p, "tmdb:movie:949", "thumb")
	settle(p)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, jpegBytes, response.Body.Bytes())
	require.Equal(t, 2, fake.requestsTo("/3/movie/949"), "the details were asked for again")
	require.FileExists(t, heat)

	// With nothing on the disk to stand in, an answer that is no proof is
	// no picture for a little while, exactly as it always was.
	fake = newMediaFake(t)
	heatSearch(fake)
	fake.json("/3/movie/949", heatDetails())
	fake.bytes("/t/p/w92/heat.jpg", webPage)

	p, _, _ = mediaPluginWithFaults(t, movieConfig())
	pictureDiskIn(t, p, t.TempDir(), newFakeClock())
	mustSearch(t, p, "movie", "heat")
	require.Equal(t, http.StatusNotFound, mediaPictureOf(t, p, "tmdb:movie:949", "thumb").Code)
	asked := len(fake.requests())
	require.Equal(t, http.StatusNotFound, mediaPictureOf(t, p, "tmdb:movie:949", "thumb").Code)
	require.Len(t, fake.requests(), asked, "remembered as no picture, as before")
}

func TestPicturesWaitingForTheDiskAreBounded(t *testing.T) {
	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	clock := newFakeClock()
	media := pictureDiskIn(t, p, t.TempDir(), clock)
	require.NotEmpty(t, p.pictureDir())
	d := p.mediaDisk()

	waiting := func() (int, int) {
		d.lock.Lock()
		defer d.lock.Unlock()

		return d.queued, d.queuedBytes
	}

	// A disk busy with something else: the writer is held, and pictures
	// keep arriving. Let go before anything is checked, so that a failure
	// does not leave the writes waiting for ever.
	d.writes.Lock()
	for n := range mediaDiskQueue + 4 {
		p.keepPicture(media, testPictureKey(n), testPicture(n), clock.now(), clock.now().Add(day))
	}
	queued, _ := waiting()
	d.writes.Unlock()
	require.Equal(t, mediaDiskQueue, queued, "no more than this many wait")
	require.Equal(t, 1, warningsAbout(p, "faster than the server's disk takes them"))
	settle(p)
	require.Len(t, filesIn(t, media), mediaDiskQueue, "those that waited are written; the rest are in memory")

	// And by their bytes: large pictures fill the queue sooner.
	large := func(n int) *mediaPicture {
		data := append(append([]byte(nil), jpegBytes...), bytes.Repeat([]byte{byte('a' + n)}, maxMediaImageBytes-len(jpegBytes))...)

		return &mediaPicture{contentType: "image/jpeg", data: data}
	}
	d.writes.Lock()
	for n := range 6 {
		p.keepPicture(media, fmt.Sprintf("tmdb:movie:%d/backdrop", n), large(n), clock.now(), clock.now().Add(day))
	}
	queued, queuedBytes := waiting()
	d.writes.Unlock()
	require.Equal(t, mediaDiskQueueBytes/maxMediaImageBytes, queued)
	require.LessOrEqual(t, queuedBytes, mediaDiskQueueBytes)
	settle(p)
	require.Len(t, filesIn(t, media), mediaDiskQueue+mediaDiskQueueBytes/maxMediaImageBytes)

	queued, queuedBytes = waiting()
	require.Zero(t, queued)
	require.Zero(t, queuedBytes)
}

func TestAPictureFetchedBeforeForgetDoesNotLandAfterIt(t *testing.T) {
	p, _, _ := mediaPluginWithFaults(t, movieConfig(), "an-admin")
	clock := newFakeClock()
	media := pictureDiskIn(t, p, t.TempDir(), clock)
	require.NotEmpty(t, p.pictureDir())
	d := p.mediaDisk()

	generation := func() uint64 {
		d.lock.Lock()
		defer d.lock.Unlock()

		return d.generation
	}

	// Fetched, and waiting for a busy disk, when the admin presses Forget:
	// whichever gets the disk first, nothing is left.
	d.writes.Lock()
	p.keepPicture(media, testPictureKey(1), testPicture(1), clock.now(), clock.now().Add(day))
	answered := make(chan *httptest.ResponseRecorder, 1)
	go func() { answered <- adminCache(t, p, http.MethodDelete, "an-admin") }()
	d.writes.Unlock()

	response := <-answered
	settle(p)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Empty(t, filesIn(t, media))

	// The write that gets the disk after Forget has had it: it writes
	// nothing, because Forget has moved the generation on.
	fetched := generation()
	response = adminCache(t, p, http.MethodDelete, "an-admin")
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Greater(t, generation(), fetched)

	d.writes.Lock()
	err := p.writePicture(d, media, testPictureKey(2), testPicture(2), clock.now(), clock.now().Add(day), fetched)
	d.writes.Unlock()
	require.NoError(t, err)
	require.Empty(t, filesIn(t, media), "forgotten means forgotten")

	// One fetched after it is kept as usual.
	keepNow(p, media, testPictureKey(3), testPicture(3), clock, day)
	require.Len(t, filesIn(t, media), 1)
}

func TestAPictureOfAChangingTitleGoesWithIt(t *testing.T) {
	fake := newMediaFake(t)
	heatSearch(fake)
	details := heatDetails()
	details["status"] = "Post Production"
	fake.json("/3/movie/949", details)
	fake.bytes("/t/p/w92/heat.jpg", jpegBytes)

	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	clock := newFakeClock()
	dir := t.TempDir()
	media := pictureDiskIn(t, p, dir, clock)

	// The planning form's thumbnail, from the search alone.
	mustSearch(t, p, "movie", "heat")
	require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "thumb").Code)
	settle(p)
	require.Zero(t, fake.requestsTo("/3/movie/949"))

	kept := readPicture(media, "tmdb:movie:949/thumb", clock.now())
	require.NotNil(t, kept)
	require.Equal(t, 3*day, kept.until.Sub(kept.at), "nobody has looked the title up, so no longer than any title")

	looked, err := detailsOf(t, p, "tmdb:movie:949")
	require.NoError(t, err)
	require.Equal(t, 3*day, looked.until.Sub(looked.fetchedAt), "and the title itself: a film not out yet")

	// Four days on the film has a new poster, and the old picture is not
	// what the panel shows.
	clock.advance(4 * day)
	details["poster_path"] = "/heat-new.jpg"
	fake.json("/3/movie/949", details)
	poster := append(append([]byte(nil), jpegBytes...), []byte("the new poster")...)
	fake.bytes("/t/p/w92/heat-new.jpg", poster)

	q := restartOnto(t, p, dir, clock)
	response := mediaPictureOf(t, q, "tmdb:movie:949", "thumb")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, poster, response.Body.Bytes())
}

func TestAPictureFetchedBeforeItsDetailsGoesWithTheRememberedTitle(t *testing.T) {
	fake := newMediaFake(t)
	soon := eldenRingDetails(fake)
	entry, _ := soon["2855530"].(map[string]any)
	data, _ := entry["data"].(map[string]any)
	data["release_date"] = map[string]any{"coming_soon": true, "date": "Coming soon"}
	fake.json("/api/appdetails", soon)
	fake.bytes("/steamcdn/apps/1245620/library_600x900.jpg", jpegBytes)

	p, _, _ := mediaPluginWithFaults(t, &configuration{MediaLookup: true})
	clock := newFakeClock()
	dir := t.TempDir()
	media := pictureDiskIn(t, p, dir, clock)

	// A game not out yet is remembered for three days.
	remembered := clock.now()
	_, err := detailsOf(t, p, "steam:1245620")
	require.NoError(t, err)

	// A day later, after a restart, its box art is fetched from Steam's fixed
	// address, before and without its details: it goes when the title does.
	clock.advance(day)
	q := restartOnto(t, p, dir, clock)
	require.Equal(t, http.StatusOK, mediaPictureOf(t, q, "steam:1245620", "cover").Code)
	settle(q)
	require.Equal(t, 1, fake.requestsTo("/api/appdetails"))

	kept := readPicture(media, "steam:1245620/cover", clock.now())
	require.NotNil(t, kept)
	require.Equal(t, remembered.Add(3*day).UnixMilli(), kept.until.UnixMilli())
}

func TestNewArtReplacesEverySizeOfTheOld(t *testing.T) {
	fake := newMediaFake(t)
	fake.json("/3/movie/949", heatDetails())
	fake.bytes("/t/p/w92/heat.jpg", jpegBytes)
	fake.bytes("/t/p/w780/heat-wide.jpg", pngBytes)

	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	clock := newFakeClock()
	media := pictureDiskIn(t, p, t.TempDir(), clock)

	_, err := detailsOf(t, p, "tmdb:movie:949")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "thumb").Code)
	require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "backdrop").Code)
	settle(p)
	thumb := pictureFile(media, "tmdb:movie:949", "thumb")
	backdrop := pictureFile(media, "tmdb:movie:949", "backdrop")
	require.FileExists(t, thumb)
	require.FileExists(t, backdrop)

	// Two days on TMDB has new art, and the cover, never fetched before,
	// finds its old address gone, which asks for the details again
	// (TestOldArtAddressesAreRefreshed).
	moved := heatDetails()
	moved["poster_path"] = "/heat-new.jpg"
	moved["backdrop_path"] = "/heat-wide-new.jpg"
	fake.json("/3/movie/949", moved)
	fake.bytes("/t/p/w342/heat-new.jpg", jpegBytes)
	poster := append(append([]byte(nil), jpegBytes...), []byte("the new poster")...)
	fake.bytes("/t/p/w92/heat-new.jpg", poster)
	clock.advance(2 * day)

	require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "cover").Code)
	settle(p)
	require.Equal(t, 2, fake.requestsTo("/3/movie/949"))

	// The thumb and the backdrop were of the old art. Their own dates are
	// weeks away, but they go now, from memory and from the disk.
	require.NoFileExists(t, thumb)
	require.NoFileExists(t, backdrop)
	require.FileExists(t, pictureFile(media, "tmdb:movie:949", "cover"))

	response := mediaPictureOf(t, p, "tmdb:movie:949", "thumb")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, poster, response.Body.Bytes())
}

func TestIGDBPicturesGoAfterSevenDaysWithNobodyAsking(t *testing.T) {
	p, _, _ := mediaPluginWithFaults(t, twitchConfig())
	clock := newFakeClock()
	dir := t.TempDir()
	media := pictureDiskIn(t, p, dir, clock)
	require.NotEmpty(t, p.pictureDir())

	hytale := pictureFile(media, "igdb:113360", "cover")
	heat := pictureFile(media, "tmdb:movie:949", "cover")
	keepNow(p, media, "igdb:113360/cover", testPicture(1), clock, 7*day)
	keepNow(p, media, "tmdb:movie:949/cover", testPicture(2), clock, 30*day)

	// Lookups switched off, and not one picture written since: the hour's
	// tidy still takes IGDB art past its seven days.
	off := &configuration{}
	off.normalize()
	p.setConfiguration(off)

	clock.advance(7*day + time.Hour)
	hourlyTidy(t, p)
	require.NoFileExists(t, hytale)
	require.FileExists(t, heat)

	// A server that was off for two months takes it at activation.
	vintage := pictureFile(media, "igdb:69547", "cover")
	keepNow(p, media, "igdb:69547/cover", testPicture(3), clock, 7*day)
	clock.advance(60 * day)
	restartOnto(t, p, dir, clock)
	require.NoFileExists(t, vintage)
	require.FileExists(t, heat, "a TMDB picture past its date may still stand in")
}

func TestMemoryNeverHoldsAPictureLongerThanItsFile(t *testing.T) {
	fake := newMediaFake(t) // nothing answers: a request would show

	p, _, _ := mediaPluginWithFaults(t, twitchConfig())
	clock := newFakeClock()
	media := pictureDiskIn(t, p, t.TempDir(), clock)
	require.NotEmpty(t, p.pictureDir())

	// Read by a panel, with half an hour of its seven days left.
	keepNow(p, media, "igdb:113360/thumb", testPicture(1), clock, 7*day)
	clock.advance(7*day - 30*time.Minute)
	picture, ok := p.pictureFromDisk("igdb:113360/thumb")
	require.True(t, ok)
	require.Equal(t, testPicture(1).data, picture.data)
	require.WithinDuration(t, time.Now().Add(30*time.Minute), memoryExpiry(t, p, "igdb:113360/thumb"), 2*time.Minute)

	// Or found by a fetch, because another node kept it a moment before:
	// served from the disk, nobody asked, and held no longer than its date.
	keepNow(p, media, "igdb:113360/cover", testPicture(2), clock, 20*time.Minute)
	ref, _ := parseMediaRef("igdb:113360")
	loaded, err := p.fetchPicture(p.getConfiguration(), ref, mediaSizeCover, "igdb:113360/cover")
	require.NoError(t, err)
	require.Equal(t, testPicture(2).data, loaded.data)
	require.Empty(t, fake.requests())
	require.WithinDuration(t, time.Now().Add(20*time.Minute), memoryExpiry(t, p, "igdb:113360/cover"), 2*time.Minute)
}

func TestAPictureJustFetchedIsBelievedAnHourAndAStandInOnlyBriefly(t *testing.T) {
	fake := newMediaFake(t)
	fake.json("/3/movie/949", heatDetails())
	fake.bytes("/t/p/w342/heat.jpg", jpegBytes)

	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	clock := newFakeClock()
	dir := t.TempDir()
	media := pictureDiskIn(t, p, dir, clock)

	// The title's details have ten minutes left: a picture fetched now is
	// still believed an hour, so it is not fetched again at once.
	_, err := detailsOf(t, p, "tmdb:movie:949")
	require.NoError(t, err)
	clock.advance(30*day - 10*time.Minute)

	require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "cover").Code)
	settle(p)
	kept := readPicture(media, "tmdb:movie:949/cover", clock.now())
	require.NotNil(t, kept)
	require.Equal(t, clock.now().Add(time.Hour).UnixMilli(), kept.until.UnixMilli())
	require.WithinDuration(t, time.Now().Add(time.Hour), memoryExpiry(t, p, "tmdb:movie:949/cover"), 2*time.Minute)

	// Past it, with the image host failing, it stands in, and memory holds
	// it only briefly, so the host is asked again soon.
	fake.status("/t/p/w342/heat.jpg", http.StatusServiceUnavailable)
	clock.advance(2 * time.Hour)
	q := restartOnto(t, p, dir, clock)
	require.Equal(t, http.StatusOK, mediaPictureOf(t, q, "tmdb:movie:949", "cover").Code)
	require.WithinDuration(t, time.Now().Add(mediaMissTTL), memoryExpiry(t, q, "tmdb:movie:949/cover"), 2*time.Minute)
}

func TestAPictureIsReplacedByARenameNeverWrittenOver(t *testing.T) {
	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	clock := newFakeClock()
	media := pictureDiskIn(t, p, t.TempDir(), clock)
	require.NotEmpty(t, p.pictureDir())

	key := testPictureKey(1)
	keepNow(p, media, key, testPicture(1), clock, day)
	target := filepath.Join(media, mediaDiskName(key))
	old, err := os.ReadFile(target)
	require.NoError(t, err)

	// A reader that has the old file open: a second name for it shows
	// whether it was written over.
	held := filepath.Join(t.TempDir(), "held")
	require.NoError(t, os.Link(target, held))

	keepNow(p, media, key, testPicture(2), clock, day)
	still, err := os.ReadFile(held)
	require.NoError(t, err)
	require.Equal(t, old, still, "the old file is whole, and the new one is another file")
	require.Equal(t, testPicture(2).data, readPicture(media, key, clock.now()).picture.data)
}

func TestARemovalLeavesAFileThatReplacedTheOneItSaw(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, mediaDiskName(testPictureKey(1)))

	require.NoError(t, os.WriteFile(path, []byte("torn"), clipsFileMode))
	seen, err := os.Lstat(path)
	require.NoError(t, err)

	// Another node renames a whole picture into place in between.
	replacement := filepath.Join(dir, "whole")
	require.NoError(t, os.WriteFile(replacement, []byte("whole"), clipsFileMode))
	require.NoError(t, os.Rename(replacement, path))

	removeIfSame(path, seen)
	require.FileExists(t, path)

	seen, err = os.Lstat(path)
	require.NoError(t, err)
	removeIfSame(path, seen)
	require.NoFileExists(t, path)
}

// The picture files are kept only if activation starts them and deactivation
// stops them, which no test can see from the outside without running a whole
// activation: so the wiring is read from plugin.go itself.
func TestActivationStartsAndDeactivationStopsThePictureDisk(t *testing.T) {
	parsed, err := parser.ParseFile(token.NewFileSet(), "plugin.go", nil, 0)
	require.NoError(t, err)

	calls := func(method string) []string {
		var called []string
		for _, decl := range parsed.Decls {
			function, ok := decl.(*ast.FuncDecl)
			if !ok || function.Name.Name != method || function.Recv == nil {
				continue
			}

			ast.Inspect(function.Body, func(node ast.Node) bool {
				if call, ok := node.(*ast.CallExpr); ok {
					if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
						if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == "p" {
							called = append(called, selector.Sel.Name)
						}
					}
				}

				return true
			})
		}

		return called
	}

	// What activation starts is the usual start's list, and what
	// deactivation stops, stopUsual's (plugin.go).
	require.Contains(t, calls("usualStarters"), "startMediaDisk")
	require.Contains(t, calls("stopUsual"), "stopMediaDisk")
}

func TestDeactivationStopsTheTidyAndLetsWritesLand(t *testing.T) {
	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	clock := newFakeClock()
	media := pictureDiskIn(t, p, t.TempDir(), clock)
	require.NotEmpty(t, p.pictureDir())
	d := p.mediaDisk()

	d.lock.Lock()
	job, _ := d.tidyJob.(*fakeClipsJob)
	d.lock.Unlock()
	require.NotNil(t, job)

	// A picture being written when the plugin is told to stop lands rather
	// than leaving half a file behind.
	d.writes.Lock()
	p.keepPicture(media, testPictureKey(1), testPicture(1), clock.now(), clock.now().Add(day))
	stopped := make(chan struct{})
	go func() {
		p.stopMediaDisk()
		close(stopped)
	}()
	d.writes.Unlock()
	<-stopped

	require.Equal(t, 1, job.closes(), "the hour's tidy is stopped")
	require.Equal(t, []string{mediaDiskName(testPictureKey(1))}, filesIn(t, media))

	// And nothing more is started.
	p.keepPicture(media, testPictureKey(2), testPicture(2), clock.now(), clock.now().Add(day))
	p.tidyMediaDisk(clock.now().Add(2 * time.Hour))
	settle(p)
	require.Equal(t, []string{mediaDiskName(testPictureKey(1))}, filesIn(t, media))

	// Until it is activated again.
	pictureDiskIn(t, p, filepath.Dir(filepath.Dir(media)), clock)
	keepNow(p, media, testPictureKey(2), testPicture(2), clock, day)
	require.Len(t, filesIn(t, media), 2)
}
