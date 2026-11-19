package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Pictures on the disk: where a relayed picture is looked for after memory and
// before its provider. Asked for by the admin who runs the server, so that no
// picture is fetched from TMDB, Steam or IGDB twice, even across restarts.
//
//	<FileSettings.Directory>/ceo.villa.matty/media/<32 hex>.pic
//
// One file per title and size, named by a hash of the two, so that no ref and
// no provider's path is ever part of a name, and nothing a provider said has
// any say in where a file goes. Each file is a short header and the picture:
//
//	VNPIC1
//	{"key":"tmdb:movie:949/cover","type":"image/jpeg","at":…,"until":…,"bytes":…,"sha256":"…"}
//	<the picture>
//
// The header is inside the file rather than in a file beside it, so that one
// rename puts a whole picture in place. A sidecar is a second file, two
// renames are not one, and a crash or a second node between them would pair
// one picture with another's type and dates. The length and the checksum are
// what make a file torn by a power cut a miss rather than a broken picture;
// the write is not synced to the disk, because all a lost write costs is the
// picture being fetched again.
//
// Only on the local file driver, as for Clips (dataroot.go). On any other, or
// when the directory will not take a write, pictures are held in memory only,
// exactly as before, and the log says so once.
//
// Looked at only behind the relay's gate: while lookups are off, or the
// title's provider is not set up, no picture is read or written, and the
// pictures 404 as they always did. Switched back on, whatever is still fresh is
// served again. The one thing that goes on regardless is the tidy, at
// activation and hourly, which only ever removes what may no longer be kept at
// all: IGDB art past its seven days whether or not anybody asks for it again,
// anything past the stale limit, broken files, and writes that never finished.
//
// The dates in a file are this or another node's clock, so they are believed
// only as far as they can be: a picture dated later than now, past a little
// slack for clocks that disagree, is neither fresh nor a stand-in, and no
// picture is believed for longer after it was fetched than its provider's
// titles ever are.
//
// Several nodes may share the directory. Every write goes to a temporary file
// of its own, created only if it did not exist, and is then renamed into
// place, which is atomic: a reader finds a whole picture or none. Two nodes
// fetching one picture at once both write it, and whichever renames last is
// the one that stays.

const (
	mediaDiskDir = "media"
	mediaDiskExt = ".pic"

	// The first line names the layout's version. A file of a newer version
	// is a later build's: a miss here, never written over, and left alone by
	// the tidy, so a cluster part way through an upgrade does not undo
	// itself. Only Forget and eviction, which are about room rather than
	// content, take it like any other.
	mediaDiskMagic   = "VNPIC"
	mediaDiskVersion = 1

	maxMediaDiskHeader = 1 << 10
	maxMediaDiskFile   = len(mediaDiskMagic) + 8 + maxMediaDiskHeader + maxMediaImageBytes

	// 256 MiB, and 4,096 pictures. A title's thumb, cover and backdrop are
	// about 5, 50 and 150 KB, so this is every picture of well over a
	// thousand titles: as many pictures as the remembered titles hold
	// entries, about ten times what memory holds, and an eighth of the 2 GB
	// the volume always keeps free.
	mediaDiskBytes   = 256 << 20
	mediaDiskEntries = 4096

	// A picture read from the disk is marked used at most this often. The
	// mark is the file's modification time, which least-recently-used
	// eviction goes by, so a picture in use costs one small change an hour at
	// most rather than a write per read.
	mediaDiskTouchEvery = time.Hour

	// How often, at most, the pictures that may no longer be kept are looked
	// for and removed: at activation, then on the hour's timer or when a
	// picture is written, whichever comes first; never on a read.
	mediaDiskTidyEvery = time.Hour

	// A temporary file this old was left by a write that never finished.
	mediaDiskTempAge = time.Hour

	// How far ahead of this node's clock a picture's date may be and still
	// be believed: the nodes of a cluster, or one clock corrected, disagree
	// by seconds, not by this.
	mediaDiskClockSlack = 10 * time.Minute

	// Pictures waiting to be written, at most. Each holds its picture until
	// it is written, so a disk slower than pictures arrive must not let them
	// pile up: past this, a picture is held in memory only, and is written
	// the next time it is fetched.
	mediaDiskQueue      = 16
	mediaDiskQueueBytes = 8 << 20

	// How long deactivation waits for the writes under way, so that an
	// upgrade leaves no half-written file behind; past it the plugin stops
	// anyway, and the tidy takes whatever was left.
	mediaDiskStopWait = 2 * time.Second

	// A picture just fetched is believed for at least this long, even when
	// its title's details are about to be looked up again.
	mediaPictureMinLife = time.Hour
)

var (
	mediaDiskNamePattern = regexp.MustCompile(`^[0-9a-f]{32}\.pic$`)

	mediaPictureSizes = []string{mediaSizeThumb, mediaSizeCover, mediaSizeBackdrop}

	errMediaDiskTorn  = errors.New("not a whole picture as this plugin keeps one")
	errMediaDiskNewer = errors.New("kept by a newer version of the plugin")
)

// mediaDisk is where this node keeps pictures on the disk, if anywhere.
type mediaDisk struct {
	lock sync.Mutex

	// started is whether activation has looked for a directory. Before it,
	// as in a test that never activates, pictures are in memory only and
	// nothing is said about it.
	started bool

	// path is <root>/media, or "" when Mattermost's files are not on this
	// server's own disk. problem says why pictures are in memory only when
	// they are: no local file storage, or a directory that would not take a
	// write. prepared is the directory made and shown to take a write; told
	// is the log having said why not.
	path     string
	problem  string
	prepared bool
	told     bool

	// tidiedAt is when the pictures that may no longer be kept were last
	// looked for, and tidyJob the hour's tidy, from activation to
	// deactivation.
	tidiedAt time.Time
	tidyJob  clipsJob

	// queued and queuedBytes are the pictures waiting to be written, and
	// generation moves on with every Forget, so that a picture fetched before
	// it does not land after it.
	queued      int
	queuedBytes int
	generation  uint64

	// writes is one write, tidy or forgetting at a time on this node.
	// pending is the writes and tidies still under way, which deactivation
	// and a test wait for. Nothing is added to it once stopped, and only ever
	// under lock, so that deactivation's wait never races a write starting.
	writes  sync.Mutex
	pending sync.WaitGroup
	stopped bool

	// The bounds, and how the room left on the volume is found out: fields
	// so that a test can make them small, and make the volume's answer not
	// depend on the machine running it.
	maxBytes   int64
	maxEntries int
	freeSpace  func(path string) (free, total uint64, ok bool)

	// every is how the hour's tidy is timed, which a test replaces so that
	// it runs when the test says.
	every func(interval time.Duration, run func()) clipsJob

	// tidyFault, when a test sets it before the disk starts, is called as a
	// tidy that is due begins, so that a test can make one panic.
	tidyFault func()
}

func (p *Plugin) mediaDisk() *mediaDisk {
	p.mediaDiskOnce.Do(func() {
		p.mediaDiskState = &mediaDisk{
			maxBytes:   mediaDiskBytes,
			maxEntries: mediaDiskEntries,
			freeSpace:  freeBytes,
			every:      everyClipsInterval,
		}
	})

	return p.mediaDiskState
}

// mediaPictureKey names one size of one title's art, in memory and, hashed,
// on the disk.
func mediaPictureKey(ref mediaRef, size string) string {
	return ref.String() + "/" + size
}

// mediaDiskName is the file a picture is kept in: a hash, so the name says
// nothing about the title, and only ever the shape the pattern allows.
func mediaDiskName(key string) string {
	sum := sha256.Sum256([]byte("villanet-media-picture\x00" + key))

	return hex.EncodeToString(sum[:16]) + mediaDiskExt
}

// startMediaDisk works out where pictures are kept, from OnActivate, and
// starts the tidy: once now, in the background so that a slow disk does not
// hold activation up, and then every hour. It makes nothing: the directory is
// made the first time a picture is looked for, so a server that never
// switches lookups on never has one, and the tidy of a directory that is not
// there does nothing.
func (p *Plugin) startMediaDisk(now time.Time) {
	// A second activation without a deactivation between has an hour's tidy
	// of its own already. Stopped first, so there are never two.
	p.stopMediaDiskTidy()

	path, problem := "", ""

	root, err := p.resolveDataRoot()
	var unusable *dataRootError
	switch {
	case err == nil:
		path = filepath.Join(root, mediaDiskDir)
	case errors.Is(err, errDataRootNotLocal):
		problem = "Mattermost's File Storage System is not Local file system"
	case errors.As(err, &unusable):
		problem = "the file storage directory " + unusable.directory + " could not be found: " + unusable.err.Error()
	default:
		problem = "Mattermost's file storage settings could not be read"
	}

	d := p.mediaDisk()

	d.lock.Lock()
	d.started, d.stopped, d.path, d.problem = true, false, path, problem
	d.prepared, d.told, d.tidiedAt = false, false, time.Time{}
	if path != "" {
		d.pending.Add(1)
	}
	d.lock.Unlock()

	if path == "" {
		return
	}

	// A tidy that panics is logged and goes no further; pending and the
	// write lock are given back by the defers (recovery.go).
	go func() {
		defer d.pending.Done()

		p.safely("the first tidy of the pictures on disk", func() {
			d.writes.Lock()
			defer d.writes.Unlock()

			d.tidyIfDue(path, now)
		})
	}()

	// Each pass run safely, and the next comes on the clock (recovery.go).
	job := d.every(mediaDiskTidyEvery, func() {
		p.safely("tidying the pictures on disk", func() { p.tidyMediaDisk(p.mediaStore().now()) })
	})
	d.lock.Lock()
	d.tidyJob = job
	d.lock.Unlock()
}

// stopMediaDisk is OnDeactivate's: the hour's tidy stops, and the writes under
// way are given a moment to land, so that an upgrade does not leave a
// temporary file behind for every picture that was being written.
func (p *Plugin) stopMediaDisk() {
	d := p.mediaDisk()

	d.lock.Lock()
	d.stopped = true
	d.lock.Unlock()

	p.stopMediaDiskTidy()

	landed := make(chan struct{})
	go func() {
		d.pending.Wait()
		close(landed)
	}()

	select {
	case <-landed:
	case <-time.After(mediaDiskStopWait):
	}
}

func (p *Plugin) stopMediaDiskTidy() {
	d := p.mediaDisk()

	d.lock.Lock()
	job := d.tidyJob
	d.tidyJob = nil
	d.lock.Unlock()

	if job != nil {
		_ = job.Close()
	}
}

// tidyMediaDisk is the hour's tidy, whatever the lookup settings are: what it
// removes may no longer be kept at all, switched off or not.
func (p *Plugin) tidyMediaDisk(now time.Time) {
	d := p.mediaDisk()

	d.lock.Lock()
	dir := d.path
	if dir == "" || d.stopped {
		d.lock.Unlock()
		return
	}
	d.pending.Add(1)
	d.lock.Unlock()

	defer d.pending.Done()

	d.writes.Lock()
	defer d.writes.Unlock()

	d.tidyIfDue(dir, now)
}

// tidyIfDue removes what may no longer be kept, and the temporary files of
// writes that never finished, when that has not been done for an hour. Under
// d.writes. A last tidy dated well after now is a clock that has been put
// back, and is no reason to wait; one a moment after now is only a picture
// fetched later being written first.
func (d *mediaDisk) tidyIfDue(dir string, now time.Time) {
	d.lock.Lock()
	since := now.Sub(d.tidiedAt)
	due := since >= mediaDiskTidyEvery || since < -mediaDiskClockSlack
	if due {
		d.tidiedAt = now
	}
	d.lock.Unlock()

	if due {
		if d.tidyFault != nil {
			d.tidyFault()
		}
		sweepMediaTemps(dir, now)
		tidyPictures(dir, now)
	}
}

// prepare makes the directory the first time it is asked for, and says where
// it is, what is wrong with it, whether that is to be said in the log now, and
// whether there is a disk at all. The lock is let go by a defer: making the
// directory is a write to the disk, held under it so that only one caller
// makes it.
func (d *mediaDisk) prepare() (dir, problem string, tell, started bool) {
	d.lock.Lock()
	defer d.lock.Unlock()

	if !d.started {
		return "", "", false, false
	}
	if d.problem == "" && !d.prepared {
		if err := prepareMediaDir(d.path); err != nil {
			d.problem = d.path + " will not take a write: " + err.Error()
		} else {
			d.prepared = true
		}
	}
	dir, problem = d.path, d.problem
	tell = problem != "" && !d.told
	d.told = d.told || tell

	return dir, problem, tell, true
}

// pictureDir is the directory to read and write pictures in, or "" for memory
// only. The first time it is asked for after activation, the directory is made
// and shown to take a write, so that a disk mounted read-only is found out
// once rather than by every picture; when it will not, or there is no local
// file storage, the log says once that pictures are held in memory only.
func (p *Plugin) pictureDir() string {
	d := p.mediaDisk()

	dir, problem, tell, started := d.prepare()
	if !started {
		return ""
	}

	if tell {
		p.API.LogInfo("Pictures of movies, shows and games are held in memory only, not kept on this server's disk",
			"reason", problem)
	}
	if problem != "" {
		return ""
	}

	return dir
}

// prepareMediaDir makes the directory and proves it takes a write.
func prepareMediaDir(dir string) error {
	if !filepath.IsAbs(dir) {
		return errors.New("the directory is not an absolute path")
	}
	if err := os.MkdirAll(dir, clipsDirMode); err != nil {
		return err
	}

	probe, path, err := createClipTemp(dir, "probe", ".tmp")
	if err != nil {
		return err
	}
	_ = probe.Close()

	return os.Remove(path)
}

// sweepMediaTemps removes the temporary files of writes that never finished.
// Only old ones: a younger one may be a write still under way on another node.
func sweepMediaTemps(dir string, now time.Time) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}

	removed := 0
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), clipTempPrefix) || !entry.Type().IsRegular() {
			continue
		}

		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) < mediaDiskTempAge {
			continue
		}
		if os.Remove(filepath.Join(dir, entry.Name())) == nil {
			removed++
		}
	}

	return removed
}

// keptPicture is a picture as the disk holds it: when it was fetched, and
// until when it is believed.
type keptPicture struct {
	picture *mediaPicture
	at      time.Time
	until   time.Time
}

// mediaDiskHeader is the second line of a picture's file.
type mediaDiskHeader struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	At    int64  `json:"at"`    // fetched, unix ms
	Until int64  `json:"until"` // believed until, unix ms
	Bytes int    `json:"bytes"`
	Sum   string `json:"sha256"`
}

func encodeMediaDisk(key string, picture *mediaPicture, at, until time.Time) ([]byte, error) {
	sum := sha256.Sum256(picture.data)

	header, err := json.Marshal(mediaDiskHeader{
		Key:   key,
		Type:  picture.contentType,
		At:    at.UnixMilli(),
		Until: until.UnixMilli(),
		Bytes: len(picture.data),
		Sum:   hex.EncodeToString(sum[:]),
	})
	if err != nil {
		return nil, err
	}
	if len(header) > maxMediaDiskHeader {
		return nil, errMediaDiskTorn
	}

	var encoded bytes.Buffer
	encoded.Grow(len(mediaDiskMagic) + 3 + len(header) + len(picture.data))
	encoded.WriteString(mediaDiskMagic + strconv.Itoa(mediaDiskVersion) + "\n")
	encoded.Write(header)
	encoded.WriteByte('\n')
	encoded.Write(picture.data)

	return encoded.Bytes(), nil
}

// splitMediaDisk is a file's header and what follows it. Nothing past the
// header is looked at here.
func splitMediaDisk(raw []byte) (mediaDiskHeader, []byte, error) {
	var header mediaDiskHeader

	if !bytes.HasPrefix(raw, []byte(mediaDiskMagic)) {
		return header, nil, errMediaDiskTorn
	}
	rest := raw[len(mediaDiskMagic):]

	end := bytes.IndexByte(rest[:min(len(rest), 8)], '\n')
	if end < 1 {
		return header, nil, errMediaDiskTorn
	}
	version, err := strconv.Atoi(string(rest[:end]))
	switch {
	case err != nil || version < 1:
		return header, nil, errMediaDiskTorn
	case version > mediaDiskVersion:
		return header, nil, errMediaDiskNewer
	}
	rest = rest[end+1:]

	end = bytes.IndexByte(rest[:min(len(rest), maxMediaDiskHeader+1)], '\n')
	if end < 0 {
		return header, nil, errMediaDiskTorn
	}
	if err := json.Unmarshal(rest[:end], &header); err != nil {
		return header, nil, errMediaDiskTorn
	}

	return header, rest[end+1:], nil
}

// decodeMediaDisk is a whole file, checked as if a stranger wrote it: the
// picture it says it is, all of it, and a picture the relay would have served.
func decodeMediaDisk(raw []byte, key string) (*keptPicture, error) {
	header, data, err := splitMediaDisk(raw)
	if err != nil {
		return nil, err
	}

	if header.Key != key || header.Bytes != len(data) || header.At <= 0 || header.Until < header.At ||
		len(data) == 0 || len(data) > maxMediaImageBytes {
		return nil, errMediaDiskTorn
	}

	sum := sha256.Sum256(data)
	if header.Sum != hex.EncodeToString(sum[:]) {
		return nil, errMediaDiskTorn
	}

	contentType, isImage := imageType(data)
	if !isImage || contentType == "image/svg+xml" || contentType != header.Type {
		return nil, errMediaDiskTorn
	}

	return &keptPicture{
		picture: &mediaPicture{contentType: contentType, data: data},
		at:      time.UnixMilli(header.At),
		until:   believedUntil(header),
	}, nil
}

// pictureLongest is the longest any picture of a provider's is believed,
// which is the longest any of its titles is.
func pictureLongest(provider string) time.Duration {
	if provider == mediaProviderIGDB {
		return mediaKeepIGDBTitle
	}

	return mediaKeepTitle
}

// believedUntil is a header's date, but never further from when the picture
// was fetched than its provider's titles are ever believed, whatever the node
// that wrote it said.
func believedUntil(header mediaDiskHeader) time.Time {
	at := time.UnixMilli(header.At)
	until := time.UnixMilli(header.Until)
	provider, _, _ := strings.Cut(header.Key, ":")

	if latest := at.Add(pictureLongest(provider)); until.After(latest) {
		return latest
	}

	return until
}

// fetchedBy is whether a picture's date is one this node can go by: not later
// than now, give or take clocks that disagree a little.
func fetchedBy(at, now time.Time) bool {
	return !at.After(now.Add(mediaDiskClockSlack))
}

// readMediaDiskHeader is a file's header alone, for the tidy and before a file
// is written over. Only a regular file is opened: a pipe would hold the read,
// and with it every write on this node, for ever.
func readMediaDiskHeader(path string) (mediaDiskHeader, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return mediaDiskHeader{}, err
	}
	if !info.Mode().IsRegular() {
		return mediaDiskHeader{}, errMediaDiskTorn
	}

	file, err := os.Open(path)
	if err != nil {
		return mediaDiskHeader{}, err
	}
	defer file.Close()

	head := make([]byte, len(mediaDiskMagic)+8+maxMediaDiskHeader+1)
	n, err := io.ReadFull(file, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return mediaDiskHeader{}, err
	}

	header, _, err := splitMediaDisk(head[:n])

	return header, err
}

// readPicture is one picture from the disk, fresh or not, or nil. A file that
// is not a whole picture of this key is of no use to anybody, and is removed.
// One that will be served is marked used.
func readPicture(dir, key string, now time.Time) *keptPicture {
	path := filepath.Join(dir, mediaDiskName(key))

	// Lstat first and then the file opened compared with it: never a link,
	// never a pipe (which would hold the read for ever), and never a file
	// that was swapped in between.
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() {
		return nil
	}
	if before.Size() > int64(maxMediaDiskFile) {
		removeIfSame(path, before)
		return nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil || !os.SameFile(before, info) {
		return nil
	}

	raw, err := io.ReadAll(io.LimitReader(file, int64(maxMediaDiskFile)+1))
	if err != nil {
		return nil
	}

	kept, err := decodeMediaDisk(raw, key)
	switch {
	case errors.Is(err, errMediaDiskNewer):
		return nil
	case err != nil:
		removeIfSame(path, info)
		return nil
	}

	if kept.fresh(now) && now.Sub(info.ModTime()) >= mediaDiskTouchEvery {
		_ = os.Chtimes(path, now, now)
	}

	return kept
}

// removeIfSame removes a file, unless it has been replaced since it was looked
// at: a whole picture another node has just renamed into place stays.
func removeIfSame(path string, seen fs.FileInfo) {
	if now, err := os.Lstat(path); err == nil && os.SameFile(seen, now) {
		_ = os.Remove(path)
	}
}

// fresh is whether a picture is still believed. Nothing fetched longer ago
// than the stale limit is, whatever its file says, as for the remembered
// titles, and nothing dated after now.
func (k *keptPicture) fresh(now time.Time) bool {
	return fetchedBy(k.at, now) && now.Before(k.until) && now.Sub(k.at) < mediaKeepStaleLimit
}

// pictureMayFallBack is whether a picture past its date may still be served
// while its provider cannot be reached. Never an IGDB picture: what IGDB gives
// is kept no longer than its seven days. Nothing past the stale limit, and
// nothing dated after now.
func pictureMayFallBack(key string, at, now time.Time) bool {
	return !strings.HasPrefix(key, mediaProviderIGDB+":") && fetchedBy(at, now) && now.Sub(at) < mediaKeepStaleLimit
}

// pictureUntil is how long a picture just fetched is believed: exactly as long
// as its title is, from the details in memory, or else from the remembered
// title. A picture fetched from a search's hint, or Steam's box art, can come
// before either; a title nobody has looked up yet is believed for as long as
// the least settled title is, so that its picture never outlives it. Never
// longer than its provider's titles from now, whatever another node's clock
// said, and never less than an hour.
func (p *Plugin) pictureUntil(ref mediaRef, now time.Time) time.Time {
	longest := pictureLongest(ref.provider)

	until := now.Add(mediaKeepChanging)
	if details, held := p.mediaStore().details.get(ref.String()); held && details != nil {
		until = details.until
	} else if kept, found := p.recallMedia(mediaTitleKey(ref)); found && keptDetails(kept, ref) != nil {
		until = time.UnixMilli(kept.Until)
	}

	if latest := now.Add(longest); until.After(latest) {
		until = latest
	}
	if earliest := now.Add(mediaPictureMinLife); until.Before(earliest) {
		until = earliest
	}

	return until
}

// pictureFromDisk is a fresh picture from the disk, put in memory, or false.
// Not metered: it costs nobody outside anything. Many panels asking for the
// same picture at once read it once.
func (p *Plugin) pictureFromDisk(key string) (*mediaPicture, bool) {
	dir := p.pictureDir()
	if dir == "" {
		return nil, false
	}

	store := p.mediaStore()
	shared, _ := store.flights.do("picture-disk/"+key, func() (any, error) {
		if held, again := store.pictures.get(key); again {
			return held, nil
		}

		now := store.now()
		kept := readPicture(dir, key, now)
		if kept == nil || !kept.fresh(now) {
			return nil, nil
		}

		store.pictures.put(key, kept.picture, memoryLife(mediaPictureTTL, kept.until.UnixMilli(), now))

		return kept.picture, nil
	})

	picture, _ := shared.(*mediaPicture)

	return picture, picture != nil
}

// fetchPicture is the picture from its provider, inside the picture's flight,
// kept in memory and on the disk. The disk is asked once more first: another
// node, or this one a moment ago, may have just kept it. A picture past its
// date stands in while the provider cannot be reached, when it may
// (pictureMayFallBack); one the provider no longer has goes.
func (p *Plugin) fetchPicture(cfg *configuration, ref mediaRef, size, key string) (*mediaPicture, error) {
	store := p.mediaStore()
	dir := p.pictureDir()
	now := store.now()

	var fallback *keptPicture
	if dir != "" {
		if kept := readPicture(dir, key, now); kept != nil {
			switch {
			case kept.fresh(now):
				store.pictures.put(key, kept.picture, memoryLife(mediaPictureTTL, kept.until.UnixMilli(), now))
				return kept.picture, nil
			case pictureMayFallBack(key, kept.at, now):
				fallback = kept
			default:
				p.dropPicture(dir, key)
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), mediaPictureBudget)
	defer cancel()

	loaded, err := p.loadMediaPicture(ctx, cfg, ref, size)
	switch {
	case err != nil && fallback != nil:
		// Held only briefly, so the provider is asked again soon.
		store.pictures.put(key, fallback.picture, mediaMissTTL)
		return fallback.picture, nil

	case errors.Is(err, errMediaPictureUnsure):
		// No picture, as it always was for a little while, and whatever the
		// disk holds stays: nothing said it is gone.
		loaded = &mediaPicture{}
		store.pictures.put(key, loaded, mediaMissTTL)

		return loaded, nil

	case err != nil:
		return nil, err

	case loaded == nil:
		loaded = &mediaPicture{}
		store.pictures.put(key, loaded, mediaMissTTL)

		if dir != "" {
			p.dropPicture(dir, key)
		}

		return loaded, nil
	}

	until := p.pictureUntil(ref, now)
	store.pictures.put(key, loaded, memoryLife(mediaPictureTTL, until.UnixMilli(), now))
	p.keepPicture(dir, key, loaded, now, until)

	return loaded, nil
}

// keepPicture writes a picture to the disk, in the background, so nobody
// waiting for it waits for the disk too. Best effort: a picture that cannot be
// kept is held in memory as before, and the log says so once in a while. So is
// one that arrives while the disk is still writing as many as it may hold
// waiting, and it is written the next time it is fetched.
func (p *Plugin) keepPicture(dir, key string, picture *mediaPicture, at, until time.Time) {
	size := len(picture.data)
	if dir == "" || size == 0 || size > maxMediaImageBytes {
		return
	}

	d := p.mediaDisk()

	d.lock.Lock()
	switch {
	case d.stopped:
		d.lock.Unlock()
		return

	case d.queued >= mediaDiskQueue || d.queuedBytes+size > mediaDiskQueueBytes:
		d.lock.Unlock()
		p.warnMediaOnce("media-disk-busy",
			"Pictures are arriving faster than the server's disk takes them, so some are held in memory only for now",
			"directory", dir)

		return
	}
	d.queued++
	d.queuedBytes += size
	generation := d.generation
	d.pending.Add(1)
	d.lock.Unlock()

	go func() {
		defer d.pending.Done()
		defer func() {
			d.lock.Lock()
			d.queued--
			d.queuedBytes -= size
			d.lock.Unlock()
		}()

		// A write that panics is logged, and the picture stays in memory only,
		// as after a write that failed; the queue, pending and the write lock
		// are given back by the defers (recovery.go). A temporary file it
		// leaves is swept by the next tidy.
		p.safely("keeping a picture on disk", func() {
			d.writes.Lock()
			defer d.writes.Unlock()

			if err := p.writePicture(d, dir, key, picture, at, until, generation); err != nil {
				p.warnMediaOnce("media-disk",
					"A picture could not be kept on the server's disk, so it is held in memory only",
					"error", err.Error())
			}
		})
	}()
}

// writePicture puts one picture in place, under d.writes: nothing at all if
// the admin has forgotten the pictures since it was fetched, then room on the
// volume, then within the bounds, then written aside and renamed in. Encoded
// only here, so that a picture waiting its turn is never held twice.
func (p *Plugin) writePicture(d *mediaDisk, dir, key string, picture *mediaPicture, now, until time.Time, generation uint64) error {
	d.lock.Lock()
	forgotten := d.generation != generation
	d.lock.Unlock()
	if forgotten {
		return nil
	}

	encoded, err := encodeMediaDisk(key, picture, now, until)
	if err != nil {
		return err
	}

	name := mediaDiskName(key)
	target := filepath.Join(dir, name)

	// The same floor Clips keep to, so that pictures can never be what fills
	// the disk Mattermost and Clips write to.
	if free, total, ok := d.freeSpace(dir); ok {
		need := uint64(len(encoded))
		if free < need || free-need < clipFreeFloor(total) {
			p.warnMediaOnce("media-disk-full",
				"Pictures are not being kept on the server's disk because the disk is nearly full; they are held in memory only",
				"directory", dir)

			return nil
		}
	}

	if _, err := readMediaDiskHeader(target); errors.Is(err, errMediaDiskNewer) {
		return nil
	}

	d.tidyIfDue(dir, now)

	if !makeRoomForPicture(dir, name, int64(len(encoded)), d.maxBytes, d.maxEntries) {
		return errors.New("the pictures already kept could not be cleared to make room")
	}

	file, temp, err := createClipTemp(dir, "media", mediaDiskExt)
	if err != nil {
		return err
	}
	_, err = file.Write(encoded)
	if closed := file.Close(); err == nil {
		err = closed
	}
	if err == nil {
		// Its modification time is when it was last used, which is now.
		err = os.Chtimes(temp, now, now)
	}
	if err == nil {
		err = os.Rename(temp, target)
	}
	if err != nil {
		_ = os.Remove(temp)
		return err
	}

	return nil
}

// diskPicture is one picture file, as a scan of the directory sees it.
type diskPicture struct {
	name string
	size int64
	used time.Time
}

// scanPictures is every picture file in the directory: the names this plugin
// gives pictures, and nothing else.
func scanPictures(dir string) ([]diskPicture, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	pictures := make([]diskPicture, 0, len(entries))
	for _, entry := range entries {
		if !mediaDiskNamePattern.MatchString(entry.Name()) || !entry.Type().IsRegular() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}
		pictures = append(pictures, diskPicture{name: entry.Name(), size: info.Size(), used: info.ModTime()})
	}

	return pictures, nil
}

// makeRoomForPicture removes the least recently used pictures until one more,
// of this many bytes, fits within both bounds. The picture being replaced does
// not count, and is never what is removed. Counted from the directory itself
// every time, so the other nodes' pictures are counted too. False when it
// could not be made to fit.
func makeRoomForPicture(dir, replacing string, adding, maxBytes int64, maxEntries int) bool {
	// One that could never fit clears nothing out for nothing.
	if adding > maxBytes || maxEntries < 1 {
		return false
	}

	pictures, err := scanPictures(dir)
	if err != nil {
		return false
	}

	others := pictures[:0]
	total := int64(0)
	for _, picture := range pictures {
		if picture.name == replacing {
			continue
		}
		others = append(others, picture)
		total += picture.size
	}

	sort.Slice(others, func(i, j int) bool {
		if !others[i].used.Equal(others[j].used) {
			return others[i].used.Before(others[j].used)
		}

		return others[i].name < others[j].name
	})

	count := len(others)
	for len(others) > 0 && (count+1 > maxEntries || total+adding > maxBytes) {
		err := os.Remove(filepath.Join(dir, others[0].name))
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			count--
			total -= others[0].size
		}
		others = others[1:]
	}

	return count+1 <= maxEntries && total+adding <= maxBytes
}

// tidyPictures removes the pictures that may no longer be kept at all: an IGDB
// picture past its date, any picture past the stale limit, and anything that
// is not a whole picture under its own name. Each file's header is read, and
// nothing more.
func tidyPictures(dir string, now time.Time) int {
	pictures, err := scanPictures(dir)
	if err != nil {
		return 0
	}

	removed := 0
	for _, picture := range pictures {
		path := filepath.Join(dir, picture.name)

		header, err := readMediaDiskHeader(path)
		switch {
		case errors.Is(err, errMediaDiskNewer), errors.Is(err, fs.ErrNotExist):
			continue
		case err == nil && mediaDiskName(header.Key) == picture.name && header.At > 0:
			kept := &keptPicture{at: time.UnixMilli(header.At), until: believedUntil(header)}
			if kept.fresh(now) || pictureMayFallBack(header.Key, kept.at, now) {
				continue
			}
		}

		if os.Remove(path) == nil {
			removed++
		}
	}

	return removed
}

// dropPicture removes what the disk holds for one picture: one its provider
// no longer has, or one that may no longer be kept.
func (p *Plugin) dropPicture(dir, key string) {
	path := filepath.Join(dir, mediaDiskName(key))
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
		_ = os.Remove(path)
	}
}

// dropPicturesOf removes a title's pictures from the disk, when its provider
// is believed to no longer have it.
func (p *Plugin) dropPicturesOf(ref mediaRef) {
	dir := p.pictureDir()
	if dir == "" {
		return
	}

	for _, size := range mediaPictureSizes {
		p.dropPicture(dir, mediaPictureKey(ref, size))
	}
}

// forgetPicturesOf lets go of a title's pictures everywhere this node holds
// them, when its art has changed: the next panel fetches the new art.
func (p *Plugin) forgetPicturesOf(ref mediaRef) {
	store := p.mediaStore()
	for _, size := range mediaPictureSizes {
		store.pictures.forget(mediaPictureKey(ref, size))
	}

	p.dropPicturesOf(ref)
}

// forgetPictures removes every picture the disk holds, for the admin's Forget,
// whether or not lookups are on. Pictures only: a temporary file is a write
// still under way on some node, whose picture is fresh, and the sweep takes
// any a crash left. A directory that will not take a write is tried all the
// same, and says so.
func (p *Plugin) forgetPictures() (int, error) {
	d := p.mediaDisk()

	// Every picture fetched before now and still waiting to be written is
	// forgotten too, rather than landing after this has said it is done.
	d.lock.Lock()
	dir := d.path
	d.generation++
	d.lock.Unlock()
	if dir == "" {
		return 0, nil
	}

	d.writes.Lock()
	defer d.writes.Unlock()

	pictures, err := scanPictures(dir)
	if err != nil {
		// No directory there, or something else in its way, where it or a
		// directory above it would be, is nothing to forget. One that is
		// there and cannot be read is a failure the admin is told about.
		info, statErr := os.Stat(dir)
		if statErr != nil && !errors.Is(statErr, fs.ErrPermission) || statErr == nil && !info.IsDir() {
			return 0, nil
		}

		return 0, err
	}

	removed := 0
	var first error
	for _, picture := range pictures {
		err := os.Remove(filepath.Join(dir, picture.name))
		switch {
		case err == nil:
			removed++
		case errors.Is(err, fs.ErrNotExist):
		case first == nil:
			first = err
		}
	}

	return removed, first
}

// pictureDiskSummary is what the admin card says about the pictures.
type pictureDiskSummary struct {
	kept       bool
	problem    string
	pictures   int
	bytes      int64
	limitBytes int64
	limit      int
}

// describePictures counts the pictures the disk holds, only when asked.
func (p *Plugin) describePictures() pictureDiskSummary {
	d := p.mediaDisk()

	d.lock.Lock()
	summary := pictureDiskSummary{
		kept:       d.started && d.problem == "",
		problem:    d.problem,
		limitBytes: d.maxBytes,
		limit:      d.maxEntries,
	}
	dir := d.path
	d.lock.Unlock()

	if dir == "" {
		return summary
	}

	pictures, _ := scanPictures(dir)
	for _, picture := range pictures {
		summary.pictures++
		summary.bytes += picture.size
	}

	return summary
}
