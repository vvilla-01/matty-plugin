//go:build !windows

package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// What the disk can do to the pictures that only a Unix disk does in a test:
// refuse a write to a process that is not root, and hold a link or a pipe
// under a picture's name.

func TestADirectoryThatWillNotTakeAWriteIsFoundOutAndForgetSaysSo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes to a read-only directory all the same")
	}

	fake := newMediaFake(t)
	heatSearch(fake)
	fake.bytes("/t/p/w92/heat.jpg", jpegBytes)

	p, _, _ := mediaPluginWithFaults(t, movieConfig(), "an-admin")
	clock := newFakeClock()
	dir := t.TempDir()
	media := filepath.Join(dir, manifestID, mediaDiskDir)

	// A picture kept before the volume went read-only.
	whole, err := encodeMediaDisk("tmdb:movie:1/thumb", testPicture(1), clock.now(), clock.now().Add(day))
	require.NoError(t, err)
	stuck := filepath.Join(media, mediaDiskName("tmdb:movie:1/thumb"))
	writeAged(t, stuck, whole, clock.now())
	require.NoError(t, os.Chmod(media, 0o500))
	t.Cleanup(func() { _ = os.Chmod(media, clipsDirMode) })

	pictureDiskIn(t, p, dir, clock)

	mustSearch(t, p, "movie", "heat")
	require.Equal(t, http.StatusOK, mediaPictureOf(t, p, "tmdb:movie:949", "thumb").Code, "from memory, as before")
	settle(p)
	require.Equal(t, 1, memoryOnlyLines(p))

	summary, _ := pictureSummary(t, p)
	require.False(t, summary.PicturesKept)
	require.Contains(t, summary.PictureProblem, "will not take a write")

	// The picture there cannot be removed: the admin is told so, rather
	// than that everything was forgotten.
	response := adminCache(t, p, http.MethodDelete, "an-admin")
	require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
	require.FileExists(t, stuck)
}

func TestALinkOrAPipeUnderAPictureNameIsNeverFollowed(t *testing.T) {
	fake := newMediaFake(t)
	heatSearch(fake)
	fake.bytes("/t/p/w92/heat.jpg", jpegBytes)

	p, _, _ := mediaPluginWithFaults(t, movieConfig())
	clock := newFakeClock()
	media := pictureDiskIn(t, p, t.TempDir(), clock)
	require.NotEmpty(t, p.pictureDir())

	// A whole, fresh picture of this very key, somewhere else, and a link to
	// it under the picture's name.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.pic")
	whole, err := encodeMediaDisk("tmdb:movie:949/thumb", testPicture(1), clock.now(), clock.now().Add(day))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(elsewhere, whole, clipsFileMode))

	link := pictureFile(media, "tmdb:movie:949", "thumb")
	require.NoError(t, os.Symlink(elsewhere, link))
	require.Nil(t, readPicture(media, "tmdb:movie:949/thumb", clock.now()))

	mustSearch(t, p, "movie", "heat")
	response := mediaPictureOf(t, p, "tmdb:movie:949", "thumb")
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, jpegBytes, response.Body.Bytes(), "fetched, not read through the link")
	require.Equal(t, 1, fake.requestsTo("/t/p/w92/heat.jpg"))
	settle(p)

	info, err := os.Lstat(link)
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular(), "the link is replaced by a picture of its own")
	after, err := os.ReadFile(elsewhere)
	require.NoError(t, err)
	require.Equal(t, whole, after, "and what it pointed at is untouched")

	// A pipe would hold a read for ever, and every write on this node behind
	// it: it is never opened, by a read, a write or the tidy.
	pipe := pictureFile(media, "tmdb:movie:2", "thumb")
	require.NoError(t, unix.Mkfifo(pipe, clipsFileMode))
	require.Nil(t, readPicture(media, "tmdb:movie:2/thumb", clock.now()))
	clock.advance(2 * time.Hour)
	keepNow(p, media, "tmdb:movie:2/thumb", testPicture(2), clock, day)
	hourlyTidy(t, p)

	kept := readPicture(media, "tmdb:movie:2/thumb", clock.now())
	require.NotNil(t, kept, "the pipe was replaced by the picture")
	require.Equal(t, testPicture(2).data, kept.picture.data)
}
