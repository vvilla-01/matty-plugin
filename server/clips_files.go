package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sync"
)

// Clip files. The video and picture of every published clip, every draft,
// and the rolling recordings they are cut from live in one directory of this
// plugin's own, beside Mattermost's other files:
//
//	<FileSettings.Directory>/ceo.villa.matty/
//	  ring/<handle>/…      the last few minutes of each live stream
//	  drafts/<id>.mp4      a clip nobody has published yet
//	  clips/<id>.mp4 .jpg  published clips and their pictures
//	  media/               not Clips': the title lookups' pictures (media_disk.go)
//	  banners/             not Clips' either: profile banners (banners.go)
//
// Not Mattermost's file store: the plugin API can write a file there but
// never delete one, so nothing would ever be freed when a clip expired. That
// is also why only the local file driver will do. All three are on one
// filesystem, so moving a draft into clips/ is a rename.
//
// Nothing here is ever built from an empty root. A root that was never
// resolved, or that cannot be written to, makes every write, wipe and delete
// return at once: filepath.Join("", "ring") is a path relative to wherever
// Mattermost happens to be running.

const (
	clipsRingDir   = "ring"
	clipsDraftsDir = "drafts"
	clipsFilesDir  = "clips"

	clipVideoExt = ".mp4"
	clipThumbExt = ".jpg"

	// Every file a write is still being made in starts with this, so nothing
	// half written is ever mistaken for a clip, and the sweep can tell what
	// was left behind by a write that never finished.
	clipTempPrefix = ".tmp-"

	clipsDirMode  = 0o750
	clipsFileMode = 0o600

	// A clip's picture is a JPEG the browser made, refused over this size when
	// it is uploaded. One read back bigger than this is not one VillaNET kept.
	maxClipThumbBytes = 200 << 10

	// How much of a video goes into one write to Mattermost. Each write
	// through the plugin RPC is two calls, so a player's range request in
	// small writes would be thousands of them.
	clipServeBuffer = 512 << 10
)

const (
	clipsProblemNoSettings = "Clips cannot read Mattermost's file storage settings; they are switched off."
	clipsProblemNotLocal   = "Clips need Mattermost's File Storage System set to Local file system; they are switched off."
)

// clipsDisk is where this node keeps clip files and how much room they take.
type clipsDisk struct {
	lock sync.Mutex

	// root is the absolute directory, or "" before startClips has resolved
	// one or when there is none to resolve. problem says why clips cannot be
	// kept, when they cannot: no local storage, or a directory that will not
	// take a write. A root with a problem may still be read from, so clips
	// already kept can still be watched, but nothing is written to it.
	root    string
	problem string

	// used is every file under clips/ and drafts/; byClipper is each
	// person's published clips, pictures included. Kept up to date by the
	// writes on this node and recounted from the disk by every sweep, which
	// is how drift, and the writes of other nodes on a shared disk, are put
	// right.
	used      int64
	byClipper map[string]int64
}

// storage is the root and the problem, as startClips left them.
func (s *clipsStore) storage() (string, string) {
	s.disk.lock.Lock()
	defer s.disk.lock.Unlock()

	return s.disk.root, s.disk.problem
}

func (s *clipsStore) setStorage(root, problem string) {
	s.disk.lock.Lock()
	defer s.disk.lock.Unlock()

	s.disk.root, s.disk.problem = root, problem
}

// readRoot is the root to read clip files from: "" when there is none.
func (s *clipsStore) readRoot() string {
	root, _ := s.storage()
	if !filepath.IsAbs(root) {
		return ""
	}

	return root
}

// writableRoot is the root to write, wipe and delete under: "" while there is
// none, or while it has a problem.
func (s *clipsStore) writableRoot() string {
	root, problem := s.storage()
	if problem != "" || !filepath.IsAbs(root) {
		return ""
	}

	return root
}

// validClipID is the shape of every clip and draft id: 16 lower-case hex, as
// newID makes them. Checked before an id goes anywhere near a path.
func validClipID(id string) bool {
	if len(id) != 16 {
		return false
	}

	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return true
}

// clipFile is where a published clip's video or picture is, to read: "" when
// there is no root, or the id is not one.
func (s *clipsStore) clipFile(id, ext string) string {
	root := s.readRoot()
	if root == "" || !validClipID(id) {
		return ""
	}

	return filepath.Join(root, clipsFilesDir, id+ext)
}

// resolveClipsRoot works out where clip files go, or why they cannot go
// anywhere, in the words an admin is shown. The plugin's own directory is
// worked out by resolveDataRoot (dataroot.go), which the pictures of the
// title lookups share without depending on Clips.
func (p *Plugin) resolveClipsRoot() (string, string) {
	root, err := p.resolveDataRoot()

	var unusable *dataRootError
	switch {
	case err == nil:
		return root, ""
	case errors.Is(err, errDataRootNotLocal):
		return "", clipsProblemNotLocal
	case errors.As(err, &unusable):
		return "", fmt.Sprintf("Clips cannot write to %s: %v.", unusable.directory, unusable.err)
	default:
		return "", clipsProblemNoSettings
	}
}

// prepareClipsRoot makes the directories and proves each one takes a write,
// so that a disk mounted read-only is found out now, as a problem an admin is
// shown, rather than by the first person to publish.
func prepareClipsRoot(root string) error {
	if !filepath.IsAbs(root) {
		return errors.New("the directory is not an absolute path")
	}

	for _, sub := range []string{clipsRingDir, clipsDraftsDir, clipsFilesDir} {
		dir := filepath.Join(root, sub)
		if err := os.MkdirAll(dir, clipsDirMode); err != nil {
			return err
		}

		probe, path, err := createClipTemp(dir, "probe", ".tmp")
		if err != nil {
			return err
		}
		_ = probe.Close()
		if err := os.Remove(path); err != nil {
			return err
		}
	}

	return nil
}

// clipTempRandom is where temporary names come from. A variable so that a
// test can hand out the same name twice and see that the second is refused
// rather than written over.
var clipTempRandom io.Reader = rand.Reader

// createClipTemp opens a new file for a write still in progress, under a name
// nobody else can have: the id it is for and eight random hex, created only
// if it does not exist. Two exports of one draft, or a retried publish
// landing beside the first, can never write into each other's file.
func createClipTemp(dir, id, ext string) (*os.File, string, error) {
	for range 4 {
		random := make([]byte, 4)
		if _, err := io.ReadFull(clipTempRandom, random); err != nil {
			return nil, "", err
		}

		path := filepath.Join(dir, clipTempPrefix+id+"-"+hex.EncodeToString(random)+ext)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, clipsFileMode)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", err
		}

		return file, path, nil
	}

	return nil, "", fs.ErrExist
}

// wipeClipsDir empties ring/ or drafts/, leaving the directory itself. These
// hold nothing that outlives the process anyway: a restart loses the rolling
// recordings and the drafts, and the README says so. Never clips/, which is
// what people published.
func (s *clipsStore) wipeClipsDir(sub string) error {
	if sub != clipsRingDir && sub != clipsDraftsDir {
		return fmt.Errorf("%s is not a directory that is wiped", sub)
	}

	root := s.writableRoot()
	if root == "" {
		return nil
	}

	dir := filepath.Join(root, sub)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	var first error
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil && first == nil {
			first = err
		}
	}

	return first
}

// sizesIn is the size of every regular file directly in a directory, by name.
// A directory that cannot be read has nothing in it, as far as accounting
// goes: the next recount tries again.
func sizesIn(dir string) map[string]int64 {
	sizes := map[string]int64{}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return sizes
	}

	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		sizes[entry.Name()] = info.Size()
	}

	return sizes
}

// recountUsage counts the room clips and drafts take from the disk itself:
// everything under clips/ and drafts/, leftovers included, since they take
// room too, and each person's published clips by the document. Without a
// document, what each person was last counted as stands.
func (s *clipsStore) recountUsage(doc *clipsDoc) {
	root := s.readRoot()
	if root == "" {
		return
	}

	clips := sizesIn(filepath.Join(root, clipsFilesDir))
	drafts := sizesIn(filepath.Join(root, clipsDraftsDir))

	used := int64(0)
	for _, size := range clips {
		used += size
	}
	for _, size := range drafts {
		used += size
	}

	var byClipper map[string]int64
	if doc != nil {
		byClipper = map[string]int64{}
		for i := range doc.Clips {
			entry := &doc.Clips[i]
			byClipper[entry.ClipperID] += clips[entry.ID+clipVideoExt] + clips[entry.ID+clipThumbExt]
		}
	}

	s.disk.lock.Lock()
	defer s.disk.lock.Unlock()

	s.disk.used = used
	if byClipper != nil {
		s.disk.byClipper = byClipper
	}
}

// addUsage counts bytes a write on this node has just added to the disk, or,
// negative, freed: published clips under their clipper, drafts under nobody.
func (s *clipsStore) addUsage(clipperID string, bytes int64) {
	s.disk.lock.Lock()
	defer s.disk.lock.Unlock()

	s.disk.used = max(0, s.disk.used+bytes)
	if clipperID == "" {
		return
	}

	if s.disk.byClipper == nil {
		s.disk.byClipper = map[string]int64{}
	}
	s.disk.byClipper[clipperID] = max(0, s.disk.byClipper[clipperID]+bytes)
	if s.disk.byClipper[clipperID] == 0 {
		delete(s.disk.byClipper, clipperID)
	}
}

// usage is the room clips and drafts take in all, and one person's published
// clips.
func (s *clipsStore) usage(clipperID string) (int64, int64) {
	s.disk.lock.Lock()
	defer s.disk.lock.Unlock()

	return s.disk.used, s.disk.byClipper[clipperID]
}

// clipShareBytes is the most one person's published clips may take: a fifth
// of the space for clips, and never less than a gigabyte, so that one person
// cannot fill the server and stop everybody else publishing for a month.
func clipShareBytes(limitBytes int64) int64 {
	return max(int64(userShareMinBytes), limitBytes/5)
}

// clipFreeFloor is how much a volume must keep free: the larger of a fixed
// floor and a share of the volume.
func clipFreeFloor(total uint64) uint64 {
	return max(uint64(freeFloorBytes), total/100*freeFloorPercent)
}

// roomFor is whether estimate more bytes of somebody's clips fit: inside their
// share, inside the space for clips, and without taking the volume below its
// floor. Nothing is ever deleted to make room; the answer is a refusal the
// panel has words for.
func (s *clipsStore) roomFor(clipperID string, estimate int64, cfg *configuration) error {
	root := s.writableRoot()
	if root == "" {
		return refuseClip(http.StatusServiceUnavailable, clipErrStorageUnavailable)
	}

	limit := cfg.clipStorageLimitBytes()
	used, mine := s.usage(clipperID)

	if clipperID != "" && mine+estimate > clipShareBytes(limit) {
		return refuseClip(http.StatusInsufficientStorage, clipErrUserStorageFull)
	}
	if used+estimate > limit {
		return refuseClip(http.StatusInsufficientStorage, clipErrStorageFull)
	}

	if free, total, ok := s.freeSpace(root); ok {
		wanted := uint64(max(0, estimate))
		if free < wanted || free-wanted < clipFreeFloor(total) {
			return refuseClip(http.StatusInsufficientStorage, clipErrStorageFull)
		}
	}

	return nil
}

// removeClipFiles deletes a published clip's video and picture, a file that
// is already gone being as good as deleted, and gives back the room they took.
func (s *clipsStore) removeClipFiles(id, clipperID string) int64 {
	root := s.writableRoot()
	if root == "" || !validClipID(id) {
		return 0
	}

	freed := int64(0)
	for _, ext := range []string{clipVideoExt, clipThumbExt} {
		path := filepath.Join(root, clipsFilesDir, id+ext)

		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if err := os.Remove(path); err == nil || errors.Is(err, fs.ErrNotExist) {
			freed += info.Size()
		}
	}

	s.addUsage(clipperID, -freed)

	return freed
}

// removeClipThumb deletes a clip's picture, and gives back its room. Whether
// the document still points at one is the caller's to have settled first.
func (s *clipsStore) removeClipThumb(id, clipperID string) {
	root := s.writableRoot()
	if root == "" || !validClipID(id) {
		return
	}

	path := filepath.Join(root, clipsFilesDir, id+clipThumbExt)
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if err := os.Remove(path); err == nil || errors.Is(err, fs.ErrNotExist) {
		s.addUsage(clipperID, -info.Size())
	}
}

// clipsSettled is what follows a settle that landed: the files of the clips it
// removed are deleted, and their room given back, and then the posts they
// were shared in start being told they have gone. A crash between the write
// and this leaves files nobody's clip names, which the sweep removes once they
// are a day old.
func (p *Plugin) clipsSettled(settled []settledClip) int64 {
	s := p.clips()

	freed := int64(0)
	for _, gone := range settled {
		freed += s.removeClipFiles(gone.ID, gone.ClipperID)
	}

	p.markSharesGoneSoon(settled)

	return freed
}

// readClipThumb reads a clip's picture into memory, to be served through
// writeImage. Small enough to hold, and held so that the answer can carry a
// hash of it.
func readClipThumb(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxClipThumbBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxClipThumbBytes {
		return nil, errors.New("the picture is larger than any kept")
	}

	return data, nil
}

// bufferedResponse gathers what is written to a response into large writes;
// see clipServeBuffer.
type bufferedResponse struct {
	http.ResponseWriter
	buf *bufio.Writer
}

func (b *bufferedResponse) Write(data []byte) (int, error) {
	return b.buf.Write(data)
}

// serveClipFile serves a clip's video, with everything a player needs to seek
// in it: ranges, If-Range and HEAD, all by http.ServeContent.
//
// Revalidated every time, never kept: whether this person may watch it is
// decided anew on every request (see requireClipsAccess). Inline, because
// it is played in the page, and with nothing a browser could run.
func serveClipFile(w http.ResponseWriter, r *http.Request, path, downloadName string) {
	file, info, err := openClipFile(path)
	if err != nil {
		writeError(w, http.StatusNotFound, "Not found.")
		return
	}
	defer file.Close()

	serveOpenClipFile(w, r, file, info, downloadName)
}

// openClipFile opens a video to serve: a regular file, and nothing else.
func openClipFile(path string) (*os.File, fs.FileInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}

	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	if err != nil {
		_ = file.Close()
		return nil, nil, err
	}

	return file, info, nil
}

// serveOpenClipFile serves a video already open, as serveClipFile says.
func serveOpenClipFile(w http.ResponseWriter, r *http.Request, file *os.File, info fs.FileInfo, downloadName string) {
	header := w.Header()
	header.Set("Content-Type", "video/mp4")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	header.Set("Cache-Control", "private, no-cache")
	header.Set("Content-Disposition", `inline; filename="`+downloadName+`"`)

	buffered := &bufferedResponse{ResponseWriter: w, buf: bufio.NewWriterSize(w, clipServeBuffer)}
	http.ServeContent(buffered, r, downloadName, info.ModTime(), file)
	_ = buffered.buf.Flush()
}
