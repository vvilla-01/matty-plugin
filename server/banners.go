package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Profile banners (contract/README.md, "Profile banners"): one picture a
// person, shown across the top of their profile in Matty. The plugin keeps
// them and Matty sets and shows them; the web panel shows and sets nothing of
// it.
//
// The pictures are files in a directory of their own beside the clips',
// because a GIF may be eight megabytes and that is no size for a value in the
// KV store, where the 24/7 channels' half-megabyte pictures are kept:
//
//	<FileSettings.Directory>/ceo.villa.matty/banners/<user id>-<v>[-<x>-<y>-<w>-<h>].<jpg|png|gif>
//
// The name is the whole record: whose it is, its version, which is a
// fingerprint of its bytes, the part of it to show when one was chosen, and
// its type, which is one of three extensions this file chose. So there is no
// document beside the pictures to fall out of step with them, and the nodes
// of a cluster that share the directory share the banners too. Nothing a
// client sent has any part in a name but a user id checked as one and four
// whole numbers checked as a crop, and a picture is only ever served as one of
// the three types.
//
// The crop (bannerCrop) is in the name for the same reason: it can never be
// read with another picture than its own, it is read with the directory and
// costs nothing more when the list is built, and it goes when the picture
// goes, whoever takes it away. Changing it renames the file, so the directory
// holds the name before or the name after, never neither; a reader still
// holding the name before looks in the directory again (handleBanner).
//
// Only on the local file driver, as for Clips (dataroot.go): on any other,
// nobody can set a banner, and the list is empty.
//
// Nothing is decoded but the header. The header is what says the kind and the
// size, and decoding the rest would decompress, on this server, a picture
// somebody else chose. Nothing is re-encoded either, so an animated GIF is
// kept, and served, as it was sent.

const (
	bannersDir = "banners"

	bannerTypeJPEG = "image/jpeg"
	bannerTypePNG  = "image/png"
	bannerTypeGIF  = "image/gif"

	// The most a banner may be: a photograph has room to be sharp at the
	// widest phone, and an animation room for a few seconds of it.
	maxBannerBytes    = 2 << 20
	maxBannerGIFBytes = 8 << 20

	// Each side, in pixels: no smaller than is worth drawing across a
	// profile, and no larger than any screen it is drawn on.
	minBannerSide = 64
	maxBannerSide = 2400

	// The most frames a GIF may have, and the most pixels they may cover
	// between them: well above a sticker of a few hundred small frames or a
	// few seconds at the largest size (about seventy full frames of 2400 by
	// 2400), and far below what eight megabytes of compressed frames could
	// make every viewer's phone decode.
	maxBannerGIFFrames = 300
	maxBannerGIFPixels = 400 << 20

	// Six uploads in ten minutes a person: room to try a few, and not a way
	// to keep every client reading the list.
	bannerUploads      = 6
	bannerUploadWindow = 10 * time.Minute

	// Thirty crop changes in ten minutes a person: a few tries at the part of
	// an animation to show cost nothing, and each moves the list everybody
	// reads, so not without end.
	bannerCrops      = 30
	bannerCropWindow = 10 * time.Minute

	// The most a crop change's body may be: {"crop": [x, y, w, h], "v": "<v>"}
	// with room for spaces, and no room for anything else.
	maxBannerCropBody = 1 << 10

	// How long the list read from the directory is believed. An upload or a
	// takeback on this node forgets it at once; this is for the other nodes
	// of a cluster, which are not told.
	bannerListTTL = 30 * time.Second

	// A temporary file this old was left by a write that never finished.
	bannerTempPrefix = ".tmp-"
	bannerTempAge    = time.Hour

	bannerDirMode = 0o750

	codeBadImage = "bad_image"
	codeBadCrop  = "bad_crop"

	bannerBadImageText = "A banner must be a JPEG, a PNG or a GIF, from 64 to 2400 pixels on each side."
	bannerTooLargeText = "That picture is too large. A JPEG or a PNG can be up to 2 MB, and a GIF up to 8 MB."
	bannerNotYoursText = "Guest accounts and bots cannot have a banner."
	bannerTooManyText  = "Too many banners in a short time. Wait a few minutes, then try again."
	bannerStoreText    = "Your banner could not be saved just now. Try again in a moment."
	bannerTakeText     = "That banner could not be taken away just now. Try again in a moment."
	bannerListText     = "The banners could not be read just now. Try again in a moment."
	bannerNotFoundText = "There is no such banner."
	bannerWhoseText    = "Say whose banner to take away."
	bannerAdminText    = "Only a system admin can take somebody's banner away."
	bannerBadCropText  = "That part of the picture cannot be a banner. Choose a part inside the picture, about three times as wide as it is high."
	bannerCropManyText = "Too many changes in a short time. Wait a few minutes, then try again."
	bannerNoneText     = "You have no banner to adjust."
	bannerStaleText    = "Your banner has changed since you chose that part. Adjust the new one instead."
)

// bannerExtensions is the only place a stored type becomes part of a name.
var bannerExtensions = map[string]string{
	bannerTypeJPEG: "jpg",
	bannerTypePNG:  "png",
	bannerTypeGIF:  "gif",
}

// bannerTypes is the other way round.
var bannerTypes = map[string]string{
	"jpg": bannerTypeJPEG,
	"png": bannerTypePNG,
	"gif": bannerTypeGIF,
}

// A banner's file: a Mattermost user id, a version as shortSum makes one, and
// one of the three extensions. Anything else in the directory is not a
// banner.
var (
	bannerFilePattern    = regexp.MustCompile(`^([a-z0-9]{26})-([0-9a-f]{16})(?:-(0|[1-9][0-9]{0,4})-(0|[1-9][0-9]{0,4})-(0|[1-9][0-9]{0,4})-(0|[1-9][0-9]{0,4}))?\.(jpg|png|gif)$`)
	bannerVersionPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)
	bannerUserPattern    = regexp.MustCompile(`^[a-z0-9]{26}$`)
)

// validBannerUser is whether an id is a Mattermost user id as Mattermost makes
// them, checked before it goes anywhere near a name in the directory.
func validBannerUser(userID string) bool {
	return model.IsValidId(userID) && bannerUserPattern.MatchString(userID)
}

// bannerEntry is somebody's banner as the list says it.
type bannerEntry struct {
	V    string `json:"v"`
	Type string `json:"type"`

	// The part of the picture to show, when one was chosen; without one,
	// the middle of the picture fills the banner.
	Crop *bannerCrop `json:"crop,omitempty"`
}

// bannerCrop is the part of a banner that shows: x, y, w and h, in
// ten-thousandths of the picture's own width and height, (x, y) its top left
// corner. Whole numbers, so that four decimal places are kept, compared and
// written back exactly as they came, with no rounding of their own.
type bannerCrop [4]int

const (
	// One, in a crop's ten-thousandths.
	cropWhole = 10000

	// The narrowest and the shortest a crop may be: a twentieth of the
	// picture, below which there is nothing left to show across a profile.
	minCropSide = 500

	// How far x+w or y+h may reach past the picture's edge: one in the
	// fourth place, which is what rounding two numbers to four places can
	// come to. A client draws no further than the picture.
	cropSlack = 1
)

// cropNumberPattern is one of a crop's numbers as it may be written: a plain
// decimal from 0 to 1 with at most four places. No sign, no exponent, no
// leading point, and no NaN or Infinity, which are not numbers to begin with.
var cropNumberPattern = regexp.MustCompile(`^[01](?:\.[0-9]{1,4})?$`)

// cropNumber is one of a crop's numbers in ten-thousandths, read from the
// text as it was written rather than through a float.
func cropNumber(text string) (int, bool) {
	if !cropNumberPattern.MatchString(text) {
		return 0, false
	}

	whole, fraction, _ := strings.Cut(text, ".")
	places, err := strconv.Atoi(fraction + strings.Repeat("0", 4-len(fraction)))
	if err != nil {
		return 0, false
	}

	value := int(whole[0]-'0')*cropWhole + places

	return value, value <= cropWhole
}

// cropOf is a crop from its four numbers as written, when they are four
// numbers and lie inside the picture.
func cropOf(texts []string) (bannerCrop, bool) {
	var crop bannerCrop
	if len(texts) != len(crop) {
		return crop, false
	}

	for i, text := range texts {
		value, ok := cropNumber(text)
		if !ok {
			return crop, false
		}
		crop[i] = value
	}

	return crop, crop.inside()
}

// inside is whether the crop lies inside the picture, with the slack that
// rounding needs, and is no thinner or shorter than a banner can show.
func (c bannerCrop) inside() bool {
	for _, value := range c {
		if value < 0 || value > cropWhole {
			return false
		}
	}

	x, y, w, h := c[0], c[1], c[2], c[3]

	return w >= minCropSide && h >= minCropSide && x+w <= cropWhole+cropSlack && y+h <= cropWhole+cropSlack
}

// fits is whether the crop is inside a picture of these pixels and, measured
// on them, from two and a half to three and a half times as wide as it is
// high: the banner is three by one, and a crop far from that would be drawn
// stretched or cut again.
func (c bannerCrop) fits(width, height int) bool {
	if !c.inside() || width <= 0 || height <= 0 {
		return false
	}

	across := int64(c[2]) * int64(width)
	down := int64(c[3]) * int64(height)

	return 2*across >= 5*down && 2*across <= 7*down
}

// cropText is one of a crop's numbers as the list writes it: the shortest
// plain decimal, with at most four places.
func cropText(value int) string {
	whole, places := value/cropWhole, value%cropWhole
	if places == 0 {
		return strconv.Itoa(whole)
	}

	text := strconv.Itoa(cropWhole + places)[1:]

	return strconv.Itoa(whole) + "." + strings.TrimRight(text, "0")
}

// MarshalJSON writes the crop as four plain decimals, exactly as kept.
func (c bannerCrop) MarshalJSON() ([]byte, error) {
	out := []byte{'['}
	for i, value := range c {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, cropText(value)...)
	}

	return append(out, ']'), nil
}

// UnmarshalJSON reads a crop as the list writes it, by the same rules a
// client's crop is held to.
func (c *bannerCrop) UnmarshalJSON(raw []byte) error {
	var numbers []json.Number
	if err := json.Unmarshal(raw, &numbers); err != nil {
		return err
	}

	texts := make([]string, len(numbers))
	for i, number := range numbers {
		texts[i] = number.String()
	}

	crop, ok := cropOf(texts)
	if !ok {
		return errors.New("not a banner's crop")
	}
	*c = crop

	return nil
}

// name is the crop's part of a file's name, "" for none.
func (c *bannerCrop) name() string {
	if c == nil {
		return ""
	}

	return "-" + strconv.Itoa(c[0]) + "-" + strconv.Itoa(c[1]) + "-" + strconv.Itoa(c[2]) + "-" + strconv.Itoa(c[3])
}

// cropInName is a crop from the four whole numbers of a file's name, when
// they lie inside the picture. The pattern has already held each to digits.
func cropInName(parts []string) (bannerCrop, bool) {
	var crop bannerCrop
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			return crop, false
		}
		crop[i] = value
	}

	return crop, crop.inside()
}

// cropFromQuery is the crop an upload's query asks for: nil when it asks for
// none, and not ok when the crop it names is not four numbers inside the
// picture, or is named twice. Only the crop's own pair is read, so whatever
// else a query carries means what it meant before.
func cropFromQuery(raw string) (*bannerCrop, bool) {
	var found *bannerCrop
	for _, pair := range strings.Split(raw, "&") {
		key, value, _ := strings.Cut(pair, "=")
		if named, err := url.QueryUnescape(key); err != nil || named != "crop" {
			continue
		}
		if found != nil {
			return nil, false
		}

		text, err := url.QueryUnescape(value)
		if err != nil {
			return nil, false
		}
		crop, ok := cropOf(strings.Split(text, ","))
		if !ok {
			return nil, false
		}
		found = &crop
	}

	return found, true
}

// cropFromBody is the crop a change's body asks for: {"crop": [x, y, w, h]},
// or {"crop": null} for none, and the v of the picture it was chosen for when
// the body names one: {"crop": ..., "v": "<v>"}, in either order. Read token by
// token, so that nothing but those two fields, crop always and v at most, each
// written once, with numbers as cropNumber reads them, a v as the list writes
// one and nothing after the object, is a crop.
func cropFromBody(raw []byte) (*bannerCrop, string, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	next := func() (json.Token, bool) {
		token, err := decoder.Token()
		return token, err == nil
	}

	if token, ok := next(); !ok || token != json.Delim('{') {
		return nil, "", false
	}

	var (
		crop    *bannerCrop
		version string
		hasCrop bool
		hasV    bool
	)
	for {
		key, ok := next()
		if !ok {
			return nil, "", false
		}
		if key == json.Delim('}') {
			break
		}

		switch {
		case key == "crop" && !hasCrop:
			hasCrop = true
			if crop, ok = cropValue(next); !ok {
				return nil, "", false
			}
		case key == "v" && !hasV:
			hasV = true
			value, ok := next()
			text, isText := value.(string)
			if !ok || !isText || !bannerVersionPattern.MatchString(text) {
				return nil, "", false
			}
			version = text
		default:
			return nil, "", false
		}
	}

	if !hasCrop {
		return nil, "", false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, "", false
	}

	return crop, version, true
}

// cropValue is the value of a body's crop field: null for none, or four
// numbers as cropNumber reads them.
func cropValue(next func() (json.Token, bool)) (*bannerCrop, bool) {
	value, ok := next()
	switch {
	case !ok:
		return nil, false
	case value == nil:
		// null: no crop.
		return nil, true
	case value == json.Delim('['):
		texts := make([]string, 0, len(bannerCrop{}))
		for {
			token, ok := next()
			if !ok {
				return nil, false
			}
			if token == json.Delim(']') {
				break
			}
			number, isNumber := token.(json.Number)
			if !isNumber || len(texts) == len(bannerCrop{}) {
				return nil, false
			}
			texts = append(texts, number.String())
		}

		parsed, ok := cropOf(texts)
		if !ok {
			return nil, false
		}

		return &parsed, true
	default:
		return nil, false
	}
}

// bannersView is GET /api/v1/banners.
type bannersView struct {
	// The same word the snapshot carries in banners.version.
	Version string `json:"version"`

	// Everybody who has a banner, by user id. Always there, {} when nobody
	// has one.
	People map[string]bannerEntry `json:"people"`
}

// bannersRef is what the snapshot says of the banners: only when to read them
// again.
type bannersRef struct {
	Version string `json:"version"`
}

// bannerStore is where this node keeps banners, and what it last read of
// them.
type bannerStore struct {
	lock sync.Mutex

	// dir is <root>/banners, or "" before activation or when Mattermost's
	// files are not on this server's own disk; problem says why, for the log.
	dir     string
	problem string
	told    bool

	// The list as last read from the directory, and when. gen moves with
	// every forget, so that a read which began before a change on this node
	// is not kept as the list after it.
	list   *bannersView
	listed time.Time
	gen    uint64

	// afterRead, when a test sets it, runs between reading the directory and
	// keeping what was read, which is where a change can land.
	afterRead func()

	// writes is one upload or takeback at a time on this node, so that two
	// of one person's never leave both pictures behind.
	writes sync.Mutex

	limiter *rateLimiter

	// cropLimiter meters changes of the crop alone, which cost no upload.
	cropLimiter *rateLimiter

	// The clock, which a test replaces.
	now func() time.Time

	// removeFile takes a file away; a test replaces it to make that fail.
	removeFile func(string) error

	// renameFile renames a picture for its crop; a test replaces it to have
	// another node rename the picture first.
	renameFile func(string, string) error
}

func (p *Plugin) banners() *bannerStore {
	p.bannersOnce.Do(func() {
		p.bannersState = &bannerStore{
			limiter:     newRateLimiter(bannerUploads, bannerUploadWindow),
			cropLimiter: newRateLimiter(bannerCrops, bannerCropWindow),
			now:         time.Now,
			removeFile:  os.Remove,
			renameFile:  os.Rename,
		}
	})

	return p.bannersState
}

// startBanners works out where banners are kept, from OnActivate. It makes
// nothing: the directory is made by the first upload.
func (p *Plugin) startBanners() {
	dir, problem := "", ""

	root, err := p.resolveDataRoot()
	switch {
	case err == nil:
		dir = filepath.Join(root, bannersDir)
	case errors.Is(err, errDataRootNotLocal):
		problem = "Mattermost's File Storage System is not Local file system"
	default:
		problem = err.Error()
	}

	p.banners().setDir(dir, problem)
}

func (s *bannerStore) setDir(dir, problem string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.dir, s.problem, s.told = dir, problem, false
	s.list, s.listed = nil, time.Time{}
	s.gen++
}

// directory is where banners are, or "" with the reason, which is said in the
// log the first time somebody is refused for it.
func (s *bannerStore) directory() (string, string, bool) {
	s.lock.Lock()
	defer s.lock.Unlock()

	if filepath.IsAbs(s.dir) {
		return s.dir, "", false
	}

	first := !s.told
	s.told = true

	problem := s.problem
	if problem == "" {
		problem = "the plugin has not worked out where to keep files"
	}

	return "", problem, first
}

// forget drops the list as last read, after a change on this node.
func (s *bannerStore) forget() {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.list, s.listed = nil, time.Time{}
	s.gen++
}

// emptyBanners is the list of nobody.
func emptyBanners() *bannersView {
	people := map[string]bannerEntry{}

	return &bannersView{Version: bannersVersion(people), People: people}
}

// bannersVersion is a fingerprint of the list, which moves whenever a banner
// is set, replaced or taken away.
func bannersVersion(people map[string]bannerEntry) string {
	raw, err := json.Marshal(people)
	if err != nil {
		return ""
	}

	return shortSum(raw)
}

// current is the list: as last read, while that is recent, or read from the
// directory again. A directory that cannot be read gives the last list there
// was, or the error when there was none. What was read is kept only if nothing
// changed on this node while it was read: this caller asked before the change
// and may be answered with the list from before it, but the next one reads
// again.
func (s *bannerStore) current() (*bannersView, error) {
	s.lock.Lock()
	dir, list, listed, gen := s.dir, s.list, s.listed, s.gen
	s.lock.Unlock()

	now := s.now()
	if list != nil && !now.Before(listed) && now.Sub(listed) < bannerListTTL {
		return list, nil
	}

	if !filepath.IsAbs(dir) {
		return emptyBanners(), nil
	}

	read, err := readBanners(dir)
	if err != nil {
		if list != nil {
			return list, nil
		}

		return nil, err
	}

	if s.afterRead != nil {
		s.afterRead()
	}

	s.lock.Lock()
	if s.dir == dir && s.gen == gen {
		s.list, s.listed = read, now
	}
	s.lock.Unlock()

	return read, nil
}

// word is what the snapshot says of the banners. Never an error: a list that
// cannot be read is the last one there was, or nobody's.
func (s *bannerStore) word() bannersRef {
	list, err := s.current()
	if err != nil {
		list = emptyBanners()
	}

	return bannersRef{Version: list.Version}
}

// bannerFile is one banner on the disk.
type bannerFile struct {
	name     string
	userID   string
	version  string
	kind     string
	crop     *bannerCrop
	modified time.Time
}

// bannerName is the file a banner is kept in: its name is its whole record.
func bannerName(userID string, entry bannerEntry) string {
	return userID + "-" + entry.V + entry.Crop.name() + "." + bannerExtensions[entry.Type]
}

// bannerFiles is every banner in the directory, and with sweep, takes away the
// temporary files that writes which never finished left there. A directory
// that is not there yet holds none.
func bannerFiles(dir string, sweep bool) ([]bannerFile, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	files := make([]bannerFile, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()

		if sweep && strings.HasPrefix(name, bannerTempPrefix) {
			if info, infoErr := entry.Info(); infoErr == nil && time.Since(info.ModTime()) > bannerTempAge {
				_ = os.Remove(filepath.Join(dir, name))
			}

			continue
		}

		match := bannerFilePattern.FindStringSubmatch(name)
		if match == nil || !entry.Type().IsRegular() {
			continue
		}

		// A crop in a name is one this file wrote, so one that is not
		// inside the picture makes the name nobody's banner.
		var crop *bannerCrop
		if match[3] != "" {
			parsed, ok := cropInName(match[3:7])
			if !ok {
				continue
			}
			crop = &parsed
		}

		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}

		files = append(files, bannerFile{
			name: name, userID: match[1], version: match[2], kind: bannerTypes[match[7]], crop: crop, modified: info.ModTime(),
		})
	}

	return files, nil
}

// readBanners is the list as the directory has it. Somebody has one picture,
// unless two nodes took an upload of theirs at the same moment; then the newer
// is theirs, and the next upload or takeback clears the other away.
func readBanners(dir string) (*bannersView, error) {
	files, err := bannerFiles(dir, false)
	if err != nil {
		return nil, err
	}

	newest := map[string]bannerFile{}
	for _, file := range files {
		held, found := newest[file.userID]
		if !found || file.newerThan(held) {
			newest[file.userID] = file
		}
	}

	people := make(map[string]bannerEntry, len(newest))
	for userID, file := range newest {
		people[userID] = file.entry()
	}

	return &bannersView{Version: bannersVersion(people), People: people}, nil
}

// newerThan is whether a file is somebody's banner rather than another file
// of theirs: the newer, and of two as new, the one whose name sorts last.
func (file bannerFile) newerThan(other bannerFile) bool {
	return file.modified.After(other.modified) || (file.modified.Equal(other.modified) && file.name > other.name)
}

// entry is the file as the list says it.
func (file bannerFile) entry() bannerEntry {
	return bannerEntry{V: file.version, Type: file.kind, Crop: file.crop}
}

// filesOf is somebody's pictures in the directory: one, or none.
func filesOf(dir, userID string, sweep bool) ([]bannerFile, error) {
	files, err := bannerFiles(dir, sweep)
	if err != nil {
		return nil, err
	}

	mine := files[:0]
	for _, file := range files {
		if file.userID == userID {
			mine = append(mine, file)
		}
	}

	return mine, nil
}

// put keeps a picture as somebody's banner, in place of any they had, and
// says its version and whether their banner changed (the same picture again
// changes nothing). The picture is written to a temporary file and renamed
// into place, so a reader finds a whole picture or none.
//
// Once the new picture is in place it is their banner, whatever happens to
// the old one: an old picture that cannot be taken away is said as the error
// beside the version, and stays on the disk, never served, until their next
// upload or takeback clears it.
func (s *bannerStore) put(dir, userID, kind string, data []byte) (string, bool, error) {
	return s.putCropped(dir, userID, kind, data, nil)
}

// putCropped is put with the part of the picture to show, nil for none. The
// crop is part of the new name, so the same picture with another crop, or
// with none, is a change, and the name it had before goes as any old picture
// does.
func (s *bannerStore) putCropped(dir, userID, kind string, data []byte, crop *bannerCrop) (string, bool, error) {
	s.writes.Lock()
	defer s.writes.Unlock()

	version := shortSum(data)
	name := bannerName(userID, bannerEntry{V: version, Type: kind, Crop: crop})

	if err := os.MkdirAll(dir, bannerDirMode); err != nil {
		return "", false, err
	}

	before, err := filesOf(dir, userID, true)
	if err != nil {
		return "", false, err
	}

	temp, err := os.CreateTemp(dir, bannerTempPrefix+"*")
	if err != nil {
		return "", false, err
	}
	_, writeErr := temp.Write(data)
	closeErr := temp.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr == nil {
		writeErr = os.Rename(temp.Name(), filepath.Join(dir, name))
	}
	if writeErr != nil {
		_ = os.Remove(temp.Name())

		return "", false, writeErr
	}

	// The old picture goes as soon as the new one is in place: one picture
	// a person, so the directory holds at most one per person.
	changed := true
	var leftover error
	for _, file := range before {
		if file.name == name {
			changed = len(before) > 1
			continue
		}
		if removeErr := s.removeFile(filepath.Join(dir, file.name)); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) && leftover == nil {
			leftover = removeErr
		}
	}

	s.forget()

	return version, changed, leftover
}

// remove takes somebody's banner away, and says whether they had one. With
// none, nothing is written.
func (s *bannerStore) remove(dir, userID string) (bool, error) {
	if !filepath.IsAbs(dir) {
		return false, nil
	}

	s.writes.Lock()
	defer s.writes.Unlock()

	mine, err := filesOf(dir, userID, false)
	if err != nil || len(mine) == 0 {
		return false, err
	}

	// Another node may rename a picture of theirs to change its crop between
	// a listing and the removal, which the writes lock of this node does not
	// keep apart, and may do it again while this looks once more; that
	// picture is still the one taken away. A rename needs the old name to be
	// there, so a file found gone is the only sign of one: look again until a
	// pass takes away everything it listed. A file that appears after that
	// can only be a new upload, which is a new choice.
	for len(mine) > 0 {
		gone, err := s.removeAll(dir, mine)
		if err != nil {
			return false, err
		}
		if !gone {
			break
		}

		if mine, err = filesOf(dir, userID, false); err != nil {
			return false, err
		}
	}

	s.forget()

	return true, nil
}

// removeAll takes files of the banner directory away, and says whether any
// was gone already, which is fine.
func (s *bannerStore) removeAll(dir string, files []bannerFile) (bool, error) {
	gone := false
	for _, file := range files {
		removeErr := s.removeFile(filepath.Join(dir, file.name))
		if errors.Is(removeErr, fs.ErrNotExist) {
			gone = true
			continue
		}
		if removeErr != nil {
			return gone, removeErr
		}
	}

	return gone, nil
}

// errCropShape is a crop that does not fit the picture it would be kept
// with.
var errCropShape = errors.New("the crop does not fit the picture")

// errCropStale is a crop chosen for a picture that is not somebody's banner
// any more: another device of theirs set a new one while it was being chosen.
var errCropStale = errors.New("the crop was chosen for another picture")

// setCrop changes or clears the crop of somebody's banner now, and says the
// banner as the list will then say it, whether that changed, and whether they
// have a banner at all. The picture stays the same file under a new name, so
// a crop is never anywhere but beside its own picture; the shape is measured
// on that picture's header. With no change, nothing is written. A version
// says the picture the crop was chosen for, "" for whichever is there: when it
// is not the file about to be renamed, nothing is written and the answer is
// errCropStale. A rename for a crop keeps the version, so only a new picture
// makes one stale.
func (s *bannerStore) setCrop(dir, userID string, crop *bannerCrop, version string) (bannerEntry, bool, bool, error) {
	if !filepath.IsAbs(dir) {
		return bannerEntry{}, false, false, nil
	}

	s.writes.Lock()
	defer s.writes.Unlock()

	// Another node may rename the picture for a crop, or take it away,
	// between the listing and the rename, which the writes lock of this node
	// does not keep apart. A file found gone is the only sign of either, so
	// look again until a listing finds no file (taken away) or a pass finds
	// nothing gone, as remove does. A rename for a crop keeps the version, so
	// the picture renamed is still the one the crop was chosen for.
	for {
		mine, err := filesOf(dir, userID, false)
		if err != nil || len(mine) == 0 {
			return bannerEntry{}, false, false, err
		}

		file := mine[0]
		for _, other := range mine[1:] {
			if other.newerThan(file) {
				file = other
			}
		}

		if version != "" && file.version != version {
			return bannerEntry{}, false, true, errCropStale
		}

		entry := file.entry()
		entry.Crop = crop

		if crop != nil {
			width, height, sizeErr := storedBannerSize(filepath.Join(dir, file.name), file.kind)
			if errors.Is(sizeErr, fs.ErrNotExist) {
				continue
			}
			if sizeErr != nil {
				return bannerEntry{}, false, true, sizeErr
			}
			if !crop.fits(width, height) {
				return bannerEntry{}, false, true, errCropShape
			}
		}

		name := bannerName(userID, entry)
		if name == file.name {
			return entry, false, true, nil
		}

		if err := s.renameFile(filepath.Join(dir, file.name), filepath.Join(dir, name)); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}

			return bannerEntry{}, false, true, err
		}

		s.forget()

		return entry, true, true, nil
	}
}

// storedBannerSize is a kept picture's sides, read from its header alone.
func storedBannerSize(path, kind string) (int, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	shape, ok := bannerHeader(io.LimitReader(file, int64(bannerLimit(kind))), kind)
	if !ok {
		return 0, 0, errors.New("a kept banner's header could not be read")
	}

	return shape.Width, shape.Height, nil
}

// bannerLimit is the most a banner of a kind may be.
func bannerLimit(kind string) int {
	if kind == bannerTypeGIF {
		return maxBannerGIFBytes
	}

	return maxBannerBytes
}

// bannerKindOf is the type a request says its body is, when it is one of the
// three; never the client's own string.
func bannerKindOf(r *http.Request) (string, bool) {
	declared, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return "", false
	}

	_, known := bannerExtensions[declared]
	if !known {
		return "", false
	}

	return declared, true
}

// checkBanner is whether the bytes are a picture of the kind declared, of a
// size a banner may be. The kind is decided twice from the bytes, by the
// sniffer and by the kind's own header decoder from the standard library, and
// both must agree with what was declared. Only the header is read: a GIF's
// sides are its logical screen's. A GIF's blocks are walked as well, without
// decoding them, because its header says nothing of how many frames follow
// and every client that shows the banner decodes all of them.
func checkBanner(data []byte, kind string) bool {
	_, ok := bannerShape(data, kind)

	return ok
}

// bannerShape is checkBanner that also says the picture's sides, which a crop
// is measured on.
func bannerShape(data []byte, kind string) (image.Config, bool) {
	if http.DetectContentType(data) != kind {
		return image.Config{}, false
	}

	shape, ok := bannerHeader(bytes.NewReader(data), kind)
	if !ok {
		return image.Config{}, false
	}

	if shape.Width < minBannerSide || shape.Height < minBannerSide ||
		shape.Width > maxBannerSide || shape.Height > maxBannerSide {
		return image.Config{}, false
	}

	if kind == bannerTypeGIF && !gifWithinBudget(data, shape.Width, shape.Height) {
		return image.Config{}, false
	}

	return shape, true
}

// bannerHeader is what a picture's header says of it, by its kind's own
// decoder from the standard library, which reads the header and no further.
func bannerHeader(reader io.Reader, kind string) (image.Config, bool) {
	var shape image.Config
	var err error
	switch kind {
	case bannerTypeJPEG:
		shape, err = jpeg.DecodeConfig(reader)
	case bannerTypePNG:
		shape, err = png.DecodeConfig(reader)
	case bannerTypeGIF:
		shape, err = gif.DecodeConfig(reader)
	default:
		return image.Config{}, false
	}

	return shape, err == nil
}

// gifWithinBudget walks a GIF's blocks from the end of its header to its
// trailer, counting its frames and their area, and is whether there are no
// more of either than a banner needs and every frame lies inside the logical
// screen. Nothing is decompressed and nothing is allocated: the walk only
// steps over the compressed data's sub-blocks, so its cost is the file's
// length, whatever the frames would come to.
func gifWithinBudget(data []byte, screenWidth, screenHeight int) bool {
	// The header and the logical screen descriptor, then the global colour
	// table if there is one.
	at := 13
	if len(data) < at {
		return false
	}
	if data[10]&0x80 != 0 {
		at += 3 << (data[10]&0x07 + 1)
	}

	// skipSubBlocks steps over a run of sub-blocks and its terminator, and is
	// whether the run ended inside the file.
	skipSubBlocks := func() bool {
		for at < len(data) {
			size := int(data[at])
			at++
			if size == 0 {
				return true
			}
			at += size
		}

		return false
	}

	frames, area := 0, 0
	for at < len(data) {
		switch data[at] {
		case 0x3B: // The trailer.
			return frames > 0
		case 0x21: // An extension: its label, then its sub-blocks.
			at += 2
			if !skipSubBlocks() {
				return false
			}
		case 0x2C: // An image descriptor: one frame.
			if at+10 > len(data) {
				return false
			}
			left := int(binary.LittleEndian.Uint16(data[at+1:]))
			top := int(binary.LittleEndian.Uint16(data[at+3:]))
			width := int(binary.LittleEndian.Uint16(data[at+5:]))
			height := int(binary.LittleEndian.Uint16(data[at+7:]))
			if width == 0 || height == 0 || left+width > screenWidth || top+height > screenHeight {
				return false
			}
			frames++
			area += width * height
			if frames > maxBannerGIFFrames || area > maxBannerGIFPixels {
				return false
			}

			flags := data[at+9]
			at += 10
			if flags&0x80 != 0 {
				at += 3 << (flags&0x07 + 1)
			}
			at++ // The LZW minimum code size.
			if !skipSubBlocks() {
				return false
			}
		default:
			return false
		}
	}

	return false
}

// handleBanners answers GET /api/v1/banners: everybody's banner, by user id.
// Conditional, like the boards: a reader who sends the tag of the copy in hand
// is told "that one still" until somebody's banner changes.
func (p *Plugin) handleBanners(w http.ResponseWriter, r *http.Request) {
	list, err := p.banners().current()
	if err != nil {
		p.API.LogWarn("Could not read the banners", "error", err.Error())
		w.Header().Set("Cache-Control", "no-store")
		writeCodedError(w, http.StatusServiceUnavailable, bannerListText, accessCodeStore)

		return
	}

	w.Header().Set("Cache-Control", "private, no-cache")

	if unchangedSince(w, r, list.Version) {
		return
	}

	writeJSON(w, http.StatusOK, list)
}

// handleBanner answers GET /api/v1/banner?user=…&v=…: the picture, as it was
// sent. It is only ever the version asked for, so it is kept for a year;
// another picture is another address. Loaded as a picture, so it asks for no
// X-Requested-With.
func (p *Plugin) handleBanner(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("user")
	version := r.URL.Query().Get("v")

	dir, _, _ := p.banners().directory()
	if dir == "" || !validBannerUser(userID) || !bannerVersionPattern.MatchString(version) {
		w.Header().Set("Cache-Control", "no-store")
		writeError(w, http.StatusNotFound, bannerNotFoundText)

		return
	}

	// Only the person's banner now: a picture they replaced may still be on
	// the disk, and is not theirs any more.
	entry, current := p.banners().currentEntry(dir, userID, version)
	if current {
		data, err := os.ReadFile(filepath.Join(dir, bannerName(userID, entry)))
		if errors.Is(err, fs.ErrNotExist) {
			// Its crop was changed a moment ago, here or on another node
			// whose change this node's list has not caught up with: the
			// same picture, under its new name.
			if fresh, found := freshBannerEntry(dir, userID, version); found {
				entry = fresh
				data, err = os.ReadFile(filepath.Join(dir, bannerName(userID, entry)))
			}
		}
		if err == nil && len(data) <= bannerLimit(entry.Type) {
			w.Header().Set("Content-Type", entry.Type)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
			w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)

			return
		}
	}

	w.Header().Set("Cache-Control", "no-store")
	writeError(w, http.StatusNotFound, bannerNotFoundText)
}

// currentEntry is somebody's banner, when the version is theirs now: from the
// list as last read, or, when that does not say so, from the directory
// itself, since another node may have taken the upload a moment ago and this
// node's list is believed for a while.
func (s *bannerStore) currentEntry(dir, userID, version string) (bannerEntry, bool) {
	if list, err := s.current(); err == nil {
		if entry, found := list.People[userID]; found && entry.V == version {
			return entry, true
		}
	}

	return freshBannerEntry(dir, userID, version)
}

// freshBannerEntry is somebody's banner as the directory has it now, when the
// version is theirs.
func freshBannerEntry(dir, userID, version string) (bannerEntry, bool) {
	fresh, err := readBanners(dir)
	if err != nil {
		return bannerEntry{}, false
	}

	entry, found := fresh.People[userID]
	if !found || entry.V != version {
		return bannerEntry{}, false
	}

	return entry, true
}

// handleMyBanner is POST and DELETE /api/v1/me/banner: setting your own, and
// taking it away.
func (p *Plugin) handleMyBanner(w http.ResponseWriter, r *http.Request, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	switch r.Method {
	case http.MethodPost:
		p.setMyBanner(w, r, userID)
	case http.MethodDelete:
		p.takeBanner(w, userID, "", bannerTakeText)
	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
	}
}

// mayHaveBanner is whether the caller may set a banner, and when not, says
// why. A regular member's only: a guest account is somebody let into a
// channel or two, and a bot has no profile anybody opens.
func (p *Plugin) mayHaveBanner(w http.ResponseWriter, userID string) bool {
	user, appErr := p.API.GetUser(userID)
	if appErr != nil || user == nil {
		if appErr != nil {
			p.API.LogWarn("Could not look somebody up to set their banner", "error", appErr.Error())
		}
		writeCodedError(w, http.StatusServiceUnavailable, bannerStoreText, accessCodeStore)

		return false
	}
	if user.IsBot || user.IsGuest() || !validBannerUser(user.Id) {
		writeCodedError(w, http.StatusForbidden, bannerNotYoursText, codeForbidden)
		return false
	}

	return true
}

// setMyBanner keeps the body as the caller's banner, with the part of it to
// show when ?crop= says one.
func (p *Plugin) setMyBanner(w http.ResponseWriter, r *http.Request, userID string) {
	if !p.mayHaveBanner(w, userID) {
		return
	}

	store := p.banners()

	dir, problem, first := store.directory()
	if dir == "" {
		if first {
			p.API.LogWarn("Banners cannot be kept on this server", "problem", problem)
		}
		writeCodedError(w, http.StatusServiceUnavailable, bannerStoreText, accessCodeStore)

		return
	}

	kind, known := bannerKindOf(r)
	if !known {
		writeCodedError(w, http.StatusBadRequest, bannerBadImageText, codeBadImage)
		return
	}

	// What the crop says is read before anything is spent, as the type is;
	// whether it fits the picture, once the picture is read.
	crop, cropOK := cropFromQuery(r.URL.RawQuery)
	if !cropOK {
		writeCodedError(w, http.StatusBadRequest, bannerBadCropText, codeBadCrop)
		return
	}

	// Before the body is read: an upload past the meter costs nothing.
	now := store.now()
	if !store.limiter.allowAt(userID, now) {
		writeTooMany(w, bannerTooManyText, store.limiter, userID, now)
		return
	}

	// A length said up front that is too much is refused without reading
	// anything, and one that was not said is read to the limit and a byte,
	// and no further.
	limit := bannerLimit(kind)
	if r.ContentLength > int64(limit) {
		writeCodedError(w, http.StatusRequestEntityTooLarge, bannerTooLargeText, codeTooLarge)
		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, int64(limit)+1))
	if err != nil {
		writeCodedError(w, http.StatusBadRequest, bannerBadImageText, codeBadImage)
		return
	}
	if len(data) > limit {
		writeCodedError(w, http.StatusRequestEntityTooLarge, bannerTooLargeText, codeTooLarge)
		return
	}

	shape, isBanner := bannerShape(data, kind)
	if !isBanner {
		writeCodedError(w, http.StatusBadRequest, bannerBadImageText, codeBadImage)
		return
	}
	if crop != nil && !crop.fits(shape.Width, shape.Height) {
		writeCodedError(w, http.StatusBadRequest, bannerBadCropText, codeBadCrop)
		return
	}

	version, changed, err := store.putCropped(dir, userID, kind, data, crop)
	if err != nil && version == "" {
		p.API.LogWarn("Could not keep a banner", "user_id", userID, "error", err.Error())
		writeCodedError(w, http.StatusServiceUnavailable, bannerStoreText, accessCodeStore)

		return
	}
	if err != nil {
		// The new picture is their banner; the old one is only left behind.
		p.API.LogWarn("Could not take an old banner away", "user_id", userID, "error", err.Error())
	}

	if changed {
		// Who put which picture on their profile, so one that should not be
		// there can be traced to an account.
		p.API.LogInfo("A banner was set", "user_id", userID, "version", version, "type", kind)
		p.publishRosterSoon()
	}

	writeJSON(w, http.StatusCreated, bannerEntry{V: version, Type: kind, Crop: crop})
}

// handleMyBannerCrop is PUT /api/v1/me/banner/crop: changing or clearing the
// part of your banner that shows, without sending the picture again. It is
// for an animated GIF above all, which a phone cannot cut and send again; a
// still picture is cut on the phone. Set as a banner is, by a regular member,
// and metered on its own: it is no takeback.
func (p *Plugin) handleMyBannerCrop(w http.ResponseWriter, r *http.Request, userID string) {
	w.Header().Set("Cache-Control", "no-store")

	if r.Method != http.MethodPut {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	if !p.mayHaveBanner(w, userID) {
		return
	}

	store := p.banners()

	dir, problem, first := store.directory()
	if dir == "" {
		if first {
			p.API.LogWarn("Banners cannot be kept on this server", "problem", problem)
		}
		writeCodedError(w, http.StatusServiceUnavailable, bannerStoreText, accessCodeStore)

		return
	}

	now := store.now()
	if !store.cropLimiter.allowAt(userID, now) {
		writeTooMany(w, bannerCropManyText, store.cropLimiter, userID, now)
		return
	}

	if r.ContentLength > maxBannerCropBody {
		writeCodedError(w, http.StatusBadRequest, bannerBadCropText, codeBadCrop)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBannerCropBody+1))
	if err != nil || len(body) > maxBannerCropBody {
		writeCodedError(w, http.StatusBadRequest, bannerBadCropText, codeBadCrop)
		return
	}

	crop, version, ok := cropFromBody(body)
	if !ok {
		writeCodedError(w, http.StatusBadRequest, bannerBadCropText, codeBadCrop)
		return
	}

	entry, changed, found, err := store.setCrop(dir, userID, crop, version)
	switch {
	case errors.Is(err, errCropShape):
		writeCodedError(w, http.StatusBadRequest, bannerBadCropText, codeBadCrop)
		return
	case errors.Is(err, errCropStale):
		writeError(w, http.StatusConflict, bannerStaleText)
		return
	case err != nil:
		p.API.LogWarn("Could not change a banner's crop", "user_id", userID, "error", err.Error())
		writeCodedError(w, http.StatusServiceUnavailable, bannerStoreText, accessCodeStore)

		return
	case !found:
		writeError(w, http.StatusNotFound, bannerNoneText)
		return
	}

	if changed {
		p.API.LogInfo("A banner's crop was changed", "user_id", userID, "version", entry.V)
		p.publishRosterSoon()
	}

	writeJSON(w, http.StatusOK, entry)
}

// takeBanner takes somebody's banner away: their own, or by a system admin.
// A takeback, so never metered, and with nothing to take away, nothing is
// written and nobody is told.
func (p *Plugin) takeBanner(w http.ResponseWriter, userID, adminID, failed string) {
	dir, _, _ := p.banners().directory()

	removed, err := p.banners().remove(dir, userID)
	if err != nil {
		p.API.LogWarn("Could not take a banner away", "user_id", userID, "error", err.Error())
		writeCodedError(w, http.StatusServiceUnavailable, failed, accessCodeStore)

		return
	}

	if removed {
		if adminID != "" {
			p.API.LogInfo("A system admin took a banner away", "user_id", adminID, "banner_user_id", userID)
		}
		p.publishRosterSoon()
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAdminBanner is DELETE /api/v1/admin/banner?user=…: a system admin
// taking anybody's banner away. Like every System Console route, it ignores
// the lock and asks for a system admin first.
func (p *Plugin) handleAdminBanner(w http.ResponseWriter, r *http.Request, adminID string) {
	w.Header().Set("Cache-Control", "no-store")

	if !p.isSystemAdmin(adminID) {
		writeCodedError(w, http.StatusForbidden, bannerAdminText, codeForbidden)
		return
	}

	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	userID := r.URL.Query().Get("user")
	if !validBannerUser(userID) {
		writeCodedError(w, http.StatusBadRequest, bannerWhoseText, codeBadRequest)
		return
	}

	p.takeBanner(w, userID, adminID, bannerTakeText)
}
