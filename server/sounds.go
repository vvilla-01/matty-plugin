package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
)

// The soundboard (design/3.2.0.md §7): one shared board of short sounds that
// linked people in Mumble play into the Mumble channel they are in.
//
// The board is one KV document, sounds, which names every sound, its emoji,
// who added it and who is blocked; each sound's packets are a KV entry of
// their own, sound_<id>, at most 40 KB. The board is seen only by linked
// people matched to a Mumble session at that moment, which the server holds
// them to, not only the panel: Victor's "unlock when in Mumble". Adding and
// renaming need the same; deleting your own, one or all, is a takeback, open
// to anybody, soundboard on or off, linked or not.
//
// Kept in the KV store rather than on disk: a sound is small, a KV write is
// atomic, and it works whatever Mattermost's file storage is. The blob is
// written first and the board second; if the board's write fails, the blob is
// deleted. A blob nothing names any more — a board write that failed and the
// delete with it, or a board started again by an admin — is swept at the
// plugin's next start, never one added in the last ten minutes, which may be
// another node's upload in progress, and never after a read of the board that
// failed or would not decode, when no blob would have an entry.

const (
	kvSounds      = "sounds"
	kvSoundPrefix = "sound_"

	soundsVersion = 1

	maxSoundsPerPerson = 16
	maxSoundsInAll     = 120
	maxSoundsBlocked   = 50
	maxSoundName       = 16

	// The upload's body: 40 000 bytes of packets as base64, and a name and
	// an emoji.
	maxSoundBody = 80 << 10

	// How old this node's copy of the board may get before a route reads it
	// again, and how many sounds' packets are kept in memory.
	soundsMaxAge      = 30 * time.Second
	soundCacheEntries = 32

	// A blob is swept only once it is this old.
	soundSweepAge = 10 * time.Minute

	// The blob's header: "VNS2", when it was added, how many packets, how
	// long each is, and the SHA-256 of what follows.
	soundBlobMagic  = "VNS2"
	soundBlobHeader = 4 + 8 + 2 + 1 + 32

	soundNeedsLink = "Link your stream first — the soundboard is for streamers."
)

// The codes the soundboard's routes refuse with (§7.14 words them).
const (
	soundCodeOff           = "off"
	soundCodeBadRequest    = "bad_request"
	soundCodeRateLimited   = "rate_limited"
	soundCodeBlocked       = "blocked"
	soundCodeGone          = "gone"
	soundCodeBotOffline    = "bot_offline"
	soundCodeBotCannot     = "bot_cannot"
	soundCodeNotInVoice    = "not_in_voice"
	soundCodeTwoSessions   = "two_sessions"
	soundCodeMuted         = "muted"
	soundCodeDeafened      = "deafened"
	soundCodeSuppressed    = "suppressed"
	soundCodeNotRegistered = "not_registered"
	soundCodeMoved         = "moved"
	soundCodeStore         = "store"
	soundCodeNotYours      = "not_yours"
	soundCodeBadName       = "bad_name"
	soundCodeBadEmoji      = "bad_emoji"
	soundCodeYoursFull     = "yours_full"
	soundCodeFull          = "full"
)

// soundText is a refusal as a person reads it, for anything that shows the
// text; the panel words most of them for itself, by code. name is what the
// reader calls the plugin: the server's name (brandName) for people, and
// productName in the System Console.
func soundText(code, name string) string {
	switch code {
	case soundCodeBotOffline:
		return name + " can't reach Mumble just now."
	case soundCodeBotCannot:
		return name + " can't play into your channel: an admin needs to check its Mumble permissions."
	case soundCodeBadSound:
		return name + " couldn't check that sound. Make it again from the file."
	case soundCodeTooLoud:
		return "That's much louder than " + name + " makes sounds. Make it again from the file."
	}

	if text, known := soundTexts[code]; known {
		return text
	}

	return name + " couldn't reach its store. Try again."
}

// soundTexts are the refusals that do not name the plugin (soundText).
var soundTexts = map[string]string{
	soundCodeOff:           "The soundboard has been switched off.",
	soundCodeBadRequest:    "That request did not make sense.",
	soundCodeRateLimited:   "Too many at once. Wait a moment and try again.",
	soundCodeBlocked:       "An admin has turned the soundboard off for you.",
	soundCodeGone:          "That sound has been deleted.",
	soundCodeNotInVoice:    "Join Mumble to add or change sounds.",
	soundCodeTwoSessions:   "You're in Mumble twice. Leave one to play sounds.",
	soundCodeMuted:         "You're muted in Mumble. Unmute to play sounds.",
	soundCodeDeafened:      "You're deafened in Mumble. Undeafen to play sounds.",
	soundCodeSuppressed:    "Mumble isn't letting you speak in this channel, so sounds are off.",
	soundCodeNotRegistered: "Sounds are only for registered Mumble names. Register yours on the Mumble server to use them.",
	soundCodeMoved:         "You moved channel in Mumble as you pressed. Press it again to play it there.",
	soundCodeNotYours:      "Only the person who added it can do that.",
	soundCodeBadName:       "Give it a name of 16 characters at most, on one line.",
	soundCodeBadEmoji:      "That emoji isn't available here any more. Pick another.",
	soundCodeYoursFull:     "You have 16 sounds, the most. Delete one to add another.",
	soundCodeFull:          "The board is full. Ask an admin to make room.",
	soundCodeSilent:        "That sounds silent.",
	soundCodeCooldown:      "Wait a moment before your next sound.",
	soundCodeBusy:          "Another sound is playing. Try again in a moment.",
	soundCodeTooMany:       "That's a lot of sounds this minute. Try again in a moment.",
}

// soundRefusal is every soundboard refusal: the text, the code and, where it
// is about time, how long to wait.
type soundRefusal struct {
	Error        string `json:"error"`
	Code         string `json:"code"`
	RetryAfterMs int64  `json:"retryAfterMs,omitempty"`
}

// writeSoundRefusal answers a refusal, never cached: whether somebody may do
// something with the soundboard is only true now.
func writeSoundRefusal(w http.ResponseWriter, status int, code string, retry time.Duration, name string) {
	refusal := soundRefusal{Error: soundText(code, name), Code: code}
	if retry > 0 {
		refusal.RetryAfterMs = max(retry.Milliseconds(), 1)
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, refusal)
}

// soundsDoc is the board.
type soundsDoc struct {
	Version int                   `json:"v"`
	Entries []soundEntry          `json:"entries"`
	Blocked map[string]soundBlock `json:"blocked,omitempty"`
}

// soundEntry is one sound on the board. The emoji is always the server's own
// lookup, never what a browser said; ms is the packets' count × 20; level the
// server's whole-sound level, for the admin card.
type soundEntry struct {
	ID    string     `json:"id"`
	Owner string     `json:"owner"`
	Name  string     `json:"name"`
	Emoji soundEmoji `json:"emoji"`
	Ms    int64      `json:"ms"`
	Bytes int        `json:"bytes"`
	Level float64    `json:"level"`
	At    int64      `json:"at"`
}

// soundEmoji is a system emoji, by name and its image's unified code, or a
// custom one, by name and id: Mattermost's own images, on Mattermost's own
// origin (/static/emoji/<unified>.png, /api/v4/emoji/<id>/image).
type soundEmoji struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Unified string `json:"unified,omitempty"`
	ID      string `json:"id,omitempty"`
}

// soundBlock is somebody an admin has blocked from the soundboard.
type soundBlock struct {
	By string `json:"by"`
	At int64  `json:"at"`
}

// soundBlob is one sound's packets as kept in memory.
type soundBlob struct {
	addedAt int64
	section []byte // the packets section, as uploaded
	packets [][]byte
	sum     string // hex SHA-256 of section past its count, the ETag
	used    time.Time
}

// soundStore is this node's side of the soundboard: its copy of the board,
// the packets of the sounds played lately, the gate, and what the last check
// of VillaNET's own Mumble session found.
type soundStore struct {
	lock sync.Mutex

	// One conversation with the store about the board at a time on this
	// node: a read and the install of what it read, or a write and the
	// install of what it wrote. Without it a re-read that fetched the bytes
	// from just before a write could be installed after the write's own, and
	// put the old board back for up to 30 s: a block undone, a new sound
	// missing. Taken before lock, never while holding it.
	io sync.Mutex

	doc      *soundsDoc
	raw      []byte
	readable bool
	loaded   bool
	readAt   time.Time
	badSeen  [32]byte

	cache map[string]*soundBlob

	gate *soundGate

	// What is wrong with VillaNET's own Mumble session for playing, if
	// anything: its own state, the last channel it lacked Whisper in, and the
	// server's bandwidth limit. For the admins' line (§7.12).
	botLine       string
	whisperLine   string
	bandwidthLine string

	// How VillaNET's permissions in a channel are asked, and how long a
	// press waits for the answer: gumble's and a second, unless a test says
	// otherwise.
	perms       mumblePermissions
	whisperWait time.Duration

	// The clock, which a test replaces.
	now func() time.Time
}

func (p *Plugin) sounds() *soundStore {
	p.soundsOnce.Do(func() {
		p.soundsState = &soundStore{
			doc:      &soundsDoc{Version: soundsVersion},
			readable: true,
			cache:    map[string]*soundBlob{},
			gate:     newSoundGate(),
			now:      time.Now,
		}
	})

	return p.soundsState
}

func decodeSounds(raw []byte) (*soundsDoc, bool) {
	doc := &soundsDoc{Version: soundsVersion}
	if len(raw) == 0 {
		return doc, true
	}

	if err := json.Unmarshal(raw, doc); err != nil || doc.Version > soundsVersion {
		return &soundsDoc{Version: soundsVersion}, false
	}

	return doc, true
}

// install makes these stored bytes this node's copy. Bytes that will not
// decode are copied aside once, and the board reads as empty, refusing adds
// and plays, until an admin starts it again. The caller holds io, so what it
// installs is what the store last said to this node.
func (p *Plugin) installSounds(raw []byte, now time.Time) bool {
	doc, ok := decodeSounds(raw)

	st := p.sounds()
	st.lock.Lock()
	sum := sha256.Sum256(raw)
	first := !ok && st.badSeen != sum
	if !ok {
		st.badSeen = sum
	}
	st.doc, st.raw, st.readable, st.loaded, st.readAt = doc, append([]byte(nil), raw...), ok, true, now
	st.lock.Unlock()

	if first {
		p.quarantine(kvSounds, raw)
		p.API.LogError("The soundboard's list of sounds could not be read; it is empty until a system admin starts it again",
			"key", kvSounds, "bytes", len(raw), "quarantine_key", kvQuarantinePrefix+kvSounds)
	}

	return ok
}

// readSounds reads the board when this node's copy is older than 30 s, or
// was never read, or force says so. A read the store did not answer keeps the
// copy there was, and says so.
func (p *Plugin) readSounds(force bool) error {
	st := p.sounds()
	if !force && !st.due(st.now()) {
		return nil
	}

	st.io.Lock()
	defer st.io.Unlock()

	// Asked again: the read this one waited behind may have just done it.
	now := st.now()
	if !force && !st.due(now) {
		return nil
	}

	return p.readSoundsLocked(now)
}

// due is whether this node's copy of the board is old enough to read again.
func (st *soundStore) due(now time.Time) bool {
	st.lock.Lock()
	defer st.lock.Unlock()

	return !st.loaded || now.Sub(st.readAt) >= soundsMaxAge || now.Before(st.readAt)
}

// readSoundsLocked reads the board and installs it, with io held.
func (p *Plugin) readSoundsLocked(now time.Time) error {
	raw, appErr := p.API.KVGet(kvSounds)
	if appErr != nil {
		p.API.LogWarn("Could not read the soundboard's list of sounds", "error", appErr.Error())
		return errStore
	}

	p.installSounds(raw, now)

	return nil
}

// board is this node's copy of the board, after a read if it is due: a copy
// the caller may keep, and whether it could be read.
func (p *Plugin) board() (soundsDoc, bool, error) {
	err := p.readSounds(false)

	st := p.sounds()
	st.lock.Lock()
	defer st.lock.Unlock()

	if err != nil && !st.loaded {
		return soundsDoc{}, false, err
	}

	copied := soundsDoc{Version: st.doc.Version, Entries: append([]soundEntry(nil), st.doc.Entries...), Blocked: map[string]soundBlock{}}
	for userID, block := range st.doc.Blocked {
		copied.Blocked[userID] = block
	}

	return copied, st.readable, nil
}

// soundVersion is the first twelve hex of the SHA-256 of the board's stored
// bytes: it changes when the board does and says nothing about it.
func (st *soundStore) version() string {
	st.lock.Lock()
	defer st.lock.Unlock()

	sum := sha256.Sum256(st.raw)

	return hex.EncodeToString(sum[:])[:12]
}

// soundboardView rides the snapshot while the soundboard is on and Mumble is
// connected (§2.3).
type soundboardView struct {
	V string `json:"v"`
}

// addSoundboard puts the soundboard's fingerprint on a snapshot, from memory:
// a publish path that copies the last snapshot must not read the store.
func (p *Plugin) addSoundboard(current *snapshot, cfg *configuration) {
	current.Soundboard = nil

	if !cfg.soundboardOn() || !p.getVoice().connected {
		return
	}

	current.Soundboard = &soundboardView{V: p.sounds().version()}
}

// loadSoundsForPoll reads the board on a poll, if this node never has, while
// the soundboard is on, so the snapshot's fingerprint is the board's from the
// first poll after it is switched on. After that only the routes read it.
func (p *Plugin) loadSoundsForPoll(cfg *configuration) {
	if !cfg.soundboardOn() {
		return
	}

	st := p.sounds()
	st.lock.Lock()
	loaded := st.loaded
	st.lock.Unlock()

	if !loaded {
		_ = p.readSounds(true)
	}
}

// soundsOwnedBy is how many sounds somebody has on the board, for their own
// profile popover's Delete my sounds. From memory, read at most once every
// 30 s for the whole server, soundboard on or off.
func (p *Plugin) soundsOwnedBy(userID string) int {
	doc, _, err := p.board()
	if err != nil {
		return 0
	}

	owned := 0
	for _, entry := range doc.Entries {
		if entry.Owner == userID {
			owned++
		}
	}

	return owned
}

// changeSounds is the only way the board is written: by casUpdate, refusing
// a board that will not decode (only an admin's restart replaces that), and
// with this node's copy replaced by what was written.
func (p *Plugin) changeSounds(change func(doc *soundsDoc) (bool, error)) error {
	st := p.sounds()
	st.io.Lock()
	defer st.io.Unlock()

	var written []byte

	err := p.casUpdate(kvSounds, func(raw []byte) ([]byte, error) {
		written = nil

		doc, ok := decodeSounds(raw)
		if !ok {
			return nil, errSoundsUnreadable
		}

		changed, err := change(doc)
		if err != nil || !changed {
			return nil, err
		}

		doc.Version = soundsVersion
		next, err := json.Marshal(doc)
		written = next

		return next, err
	})
	if errors.Is(err, errSoundsUnreadable) {
		_ = p.readSoundsLocked(st.now())
	}
	if err != nil {
		return err
	}

	if written != nil {
		p.installSounds(written, st.now())
	}

	return nil
}

var (
	errSoundsUnreadable = errors.New("the soundboard's list of sounds cannot be read")
	errSoundYoursFull   = errors.New("this person has as many sounds as they may")
	errSoundBoardFull   = errors.New("the board is full")
	errSoundGone        = errors.New("that sound is not on the board")
	errSoundNotYours    = errors.New("that sound is somebody else's")
)

// soundBlobOf is a blob's bytes: the header, then the packets section past its
// count, so the SHA-256 in the header covers exactly what is played.
func soundBlobOf(addedAt int64, packets [][]byte) ([]byte, string) {
	body := encodeSoundPackets(packets)[2:]
	sum := sha256.Sum256(body)

	blob := make([]byte, 0, soundBlobHeader+len(body))
	blob = append(blob, soundBlobMagic...)
	blob = binary.BigEndian.AppendUint64(blob, uint64(addedAt))
	blob = binary.BigEndian.AppendUint16(blob, uint16(len(packets)))
	blob = append(blob, soundFrameMs)
	blob = append(blob, sum[:]...)
	blob = append(blob, body...)

	return blob, hex.EncodeToString(sum[:])
}

// readSoundBlob takes a blob apart and checks it: its magic, its frame
// length, its count against what follows, and its checksum. A blob that fails
// any of them is refused, never played.
func readSoundBlob(blob []byte) (*soundBlob, bool) {
	if len(blob) < soundBlobHeader || string(blob[:4]) != soundBlobMagic || blob[14] != soundFrameMs {
		return nil, false
	}

	addedAt := int64(binary.BigEndian.Uint64(blob[4:12]))
	count := binary.BigEndian.Uint16(blob[12:14])
	body := blob[soundBlobHeader:]

	sum := sha256.Sum256(body)
	if !bytes.Equal(sum[:], blob[15:47]) {
		return nil, false
	}

	section := binary.BigEndian.AppendUint16(nil, count)
	section = append(section, body...)

	packets, ok := parseSoundPackets(section)
	if !ok {
		return nil, false
	}

	return &soundBlob{addedAt: addedAt, section: section, packets: packets, sum: hex.EncodeToString(sum[:])}, true
}

// blobAddedAt is when a blob says it was added, or 0 for a blob whose header
// cannot be read, which the sweep counts as old.
func blobAddedAt(blob []byte) int64 {
	if len(blob) < soundBlobHeader || string(blob[:4]) != soundBlobMagic {
		return 0
	}

	return int64(binary.BigEndian.Uint64(blob[4:12]))
}

// soundBlobFor is a sound's packets, from memory or else the store, checked.
// errSoundGone for a blob that is missing or fails its check; errStore for a
// store that did not answer.
func (p *Plugin) soundBlobFor(soundID string) (*soundBlob, error) {
	st := p.sounds()
	now := st.now()

	st.lock.Lock()
	if cached := st.cache[soundID]; cached != nil {
		cached.used = now
		st.lock.Unlock()

		return cached, nil
	}
	st.lock.Unlock()

	raw, appErr := p.API.KVGet(kvSoundPrefix + soundID)
	if appErr != nil {
		p.API.LogWarn("Could not read a sound", "sound_id", soundID, "error", appErr.Error())
		return nil, errStore
	}
	if len(raw) == 0 {
		return nil, errSoundGone
	}

	blob, ok := readSoundBlob(raw)
	if !ok {
		p.API.LogWarn("A sound's audio failed its check and was not played", "sound_id", soundID)
		return nil, errSoundGone
	}
	blob.used = now

	st.lock.Lock()
	defer st.lock.Unlock()

	if len(st.cache) >= soundCacheEntries {
		oldest := ""
		for id, cached := range st.cache {
			if oldest == "" || cached.used.Before(st.cache[oldest].used) {
				oldest = id
			}
		}
		delete(st.cache, oldest)
	}
	st.cache[soundID] = blob

	return blob, nil
}

// forgetSoundBlob drops a sound's packets from memory.
func (p *Plugin) forgetSoundBlob(soundID string) {
	st := p.sounds()
	st.lock.Lock()
	delete(st.cache, soundID)
	st.lock.Unlock()
}

// startSounds is the soundboard's part of activation: while it is on, the
// board is read, and if it was read and decoded, the blobs it does not name
// are swept (§7.8).
func (p *Plugin) startSounds(now time.Time) {
	if !p.getConfiguration().soundboardOn() {
		return
	}

	if err := p.readSounds(true); err != nil {
		return
	}

	p.sweepSoundBlobs(now)
}

// sweepSoundBlobs deletes the sound_ entries the board does not name, added
// more than ten minutes ago, and logs how many. Only after a read of the
// board in this activation that decoded: never after a store error or a
// quarantine, when no blob would have an entry.
func (p *Plugin) sweepSoundBlobs(now time.Time) {
	st := p.sounds()
	st.lock.Lock()
	ready := st.loaded && st.readable
	named := map[string]bool{}
	for _, entry := range st.doc.Entries {
		named[entry.ID] = true
	}
	st.lock.Unlock()

	if !ready {
		return
	}

	deleted := 0
	for _, key := range p.kvKeysWithPrefix(kvSoundPrefix) {
		if named[strings.TrimPrefix(key, kvSoundPrefix)] {
			continue
		}

		raw, appErr := p.API.KVGet(key)
		if appErr != nil {
			continue
		}
		if added := blobAddedAt(raw); added > 0 && now.Sub(time.UnixMilli(added)) < soundSweepAge {
			continue
		}

		if appErr := p.API.KVDelete(key); appErr != nil {
			p.API.LogWarn("Could not delete a sound nothing names any more", "key", key, "error", appErr.Error())
			continue
		}
		deleted++
	}

	if deleted > 0 {
		p.API.LogInfo("Deleted sounds the soundboard no longer names", "count", deleted)
	}
}

// cleanSoundName is a sound's name as it will be kept, or false: 1 to 16
// characters after trimming, on one line, no control characters, with the
// author's own publish token and stream key taken out first, as a plan
// title's are.
func (p *Plugin) cleanSoundName(userID, raw string) (string, bool) {
	for _, r := range raw {
		if unicode.IsControl(r) {
			return "", false
		}
	}

	name := strings.TrimSpace(p.withoutOwnKey(userID, strings.TrimSpace(model.SanitizeUnicode(raw))))
	count := utf8.RuneCountInString(name)
	if count < 1 || count > maxSoundName {
		return "", false
	}

	return name, true
}

type soundEmojiRequest struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// resolveSoundEmoji is an emoji as the server itself finds it: a system emoji
// by Mattermost's own list, a custom one by asking Mattermost, and only while
// custom emoji are switched on. Never what the browser said it was.
func (p *Plugin) resolveSoundEmoji(request soundEmojiRequest) (soundEmoji, bool) {
	name := strings.Trim(strings.TrimSpace(request.Name), ":")
	if name == "" || len(name) > model.EmojiNameMaxLength {
		return soundEmoji{}, false
	}

	switch request.Kind {
	case "system":
		unified, found := model.GetSystemEmojiId(name)
		if !found {
			return soundEmoji{}, false
		}

		return soundEmoji{Kind: "system", Name: name, Unified: unified}, true

	case "custom":
		config := p.API.GetConfig()
		if config == nil || config.ServiceSettings.EnableCustomEmoji == nil || !*config.ServiceSettings.EnableCustomEmoji {
			return soundEmoji{}, false
		}

		emoji, appErr := p.API.GetEmojiByName(name)
		if appErr != nil || emoji == nil || emoji.Id == "" || emoji.DeleteAt != 0 {
			return soundEmoji{}, false
		}

		return soundEmoji{Kind: "custom", Name: emoji.Name, ID: emoji.Id}, true
	}

	return soundEmoji{}, false
}

// soundsOn refuses with 404 "off" while the soundboard is off, and says so.
func soundsOn(w http.ResponseWriter, cfg *configuration) bool {
	if cfg.soundboardOn() {
		return true
	}

	writeSoundRefusal(w, http.StatusNotFound, soundCodeOff, 0, productName)

	return false
}

// soundListItem is one sound on the board as GET /sounds gives it: owner is a
// display name, and mine says whether it is the reader's.
type soundListItem struct {
	ID    string     `json:"id"`
	Name  string     `json:"name"`
	Emoji soundEmoji `json:"emoji"`
	Ms    int64      `json:"ms"`
	Owner string     `json:"owner"`
	Mine  bool       `json:"mine"`
}

type soundVoiceView struct {
	In      bool   `json:"in"`
	Channel string `json:"channel,omitempty"`
	Can     bool   `json:"can"`
	Why     string `json:"why"`
}

type soundBotView struct {
	OK bool `json:"ok"`
}

type soundLimits struct {
	MaxMs      int64 `json:"maxMs"`
	PerPerson  int   `json:"perPerson"`
	Total      int   `json:"total"`
	Name       int   `json:"name"`
	Left       int   `json:"left"`
	CooldownMs int64 `json:"cooldownMs"`
}

type soundListResponse struct {
	Voice         soundVoiceView `json:"voice"`
	Bot           soundBotView   `json:"bot"`
	CooldownUntil int64          `json:"cooldownUntil"`
	BusyUntil     int64          `json:"busyUntil"`
	V             string         `json:"v"`
	Blocked       bool           `json:"blocked,omitempty"`
	// Never omitempty: an empty board must come as [], so the panel can tell
	// "nobody has added a sound yet" (the invitation to add the first) from
	// "no board for you" (null, for somebody not in Mumble). With omitempty an
	// empty board was left out, and the panel waited for a list that never
	// came: a sheet with nothing in it, and no way to add the first sound.
	Sounds []soundListItem `json:"sounds"`
	Limits *soundLimits    `json:"limits,omitempty"`
}

func millisOrZero(at time.Time) int64 {
	if at.IsZero() {
		return 0
	}

	return at.UnixMilli()
}

// handleSoundsList is GET /api/v1/sounds: for every linked caller, their
// voice state, read live from Mumble, and the waits; and only for somebody
// matched to a Mumble session now, in any state, the board itself. The
// Mumble channel's name goes only to the person in it.
func (p *Plugin) handleSoundsList(w http.ResponseWriter, userID string) {
	cfg := p.getConfiguration()
	if !soundsOn(w, cfg) {
		return
	}

	doc, readable, err := p.board()
	if err != nil {
		writeSoundRefusal(w, http.StatusServiceUnavailable, soundCodeStore, 0, p.brandName())
		return
	}

	read := p.voiceStateOf(userID, cfg)
	if read.code == soundCodeStore {
		writeSoundRefusal(w, http.StatusServiceUnavailable, soundCodeStore, 0, p.brandName())
		return
	}

	st := p.sounds()
	now := st.now()
	cooldownUntil, busyUntil := st.gate.state(userID, now)

	answer := soundListResponse{
		Voice:         soundVoiceView{In: read.in, Channel: read.channelName, Can: read.code == "", Why: read.code},
		Bot:           soundBotView{OK: read.botOK},
		CooldownUntil: millisOrZero(cooldownUntil),
		BusyUntil:     millisOrZero(busyUntil),
		V:             st.version(),
	}
	if read.code == soundCodeTwoSessions || !read.in {
		answer.Voice.Channel = ""
	}
	if _, blocked := doc.Blocked[userID]; blocked {
		answer.Blocked = true
	}

	if read.in {
		mine := 0
		people := map[string]string{}
		answer.Sounds = []soundListItem{}
		if readable {
			for _, entry := range doc.Entries {
				owner, named := people[entry.Owner]
				if !named {
					owner = "Somebody"
					if user, appErr := p.API.GetUser(entry.Owner); appErr == nil && user != nil {
						owner = displayNameOf(user)
					}
					people[entry.Owner] = owner
				}

				if entry.Owner == userID {
					mine++
				}

				answer.Sounds = append(answer.Sounds, soundListItem{
					ID: entry.ID, Name: entry.Name, Emoji: entry.Emoji, Ms: entry.Ms, Owner: owner, Mine: entry.Owner == userID,
				})
			}
		}

		answer.Limits = &soundLimits{
			MaxMs:      maxSoundMs,
			PerPerson:  maxSoundsPerPerson,
			Total:      maxSoundsInAll,
			Name:       maxSoundName,
			Left:       max(0, min(maxSoundsPerPerson-mine, maxSoundsInAll-len(doc.Entries))),
			CooldownMs: int64(cfg.soundCooldownSeconds()) * 1000,
		}
		if !readable {
			answer.Limits.Left = 0
		}
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, answer)
}

type soundAddRequest struct {
	Name    string            `json:"name"`
	Emoji   soundEmojiRequest `json:"emoji"`
	Packets string            `json:"packets"`
}

// readStrictJSON reads a body of at most limit bytes as exactly one JSON
// value of into's shape, no field it does not know.
func readStrictJSON(w http.ResponseWriter, r *http.Request, limit int64, into any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return false
	}

	// Nothing after the value.
	var extra json.RawMessage
	return decoder.Decode(&extra) == io.EOF
}

// handleSoundAdd is POST /api/v1/sounds: a new sound on the board, from
// somebody linked, not blocked, and in Mumble now, after every check of §7.8.
// The blob is written first, then the board; a board write that fails takes
// the blob down again.
func (p *Plugin) handleSoundAdd(w http.ResponseWriter, r *http.Request, userID string) {
	cfg := p.getConfiguration()
	if !soundsOn(w, cfg) {
		return
	}

	if !p.requireLinkedStream(w, userID, soundNeedsLink) {
		return
	}

	doc, readable, err := p.board()
	if err != nil || !readable {
		writeSoundRefusal(w, http.StatusServiceUnavailable, soundCodeStore, 0, p.brandName())
		return
	}
	if _, blocked := doc.Blocked[userID]; blocked {
		writeSoundRefusal(w, http.StatusForbidden, soundCodeBlocked, 0, p.brandName())
		return
	}

	var request soundAddRequest
	if !readStrictJSON(w, r, maxSoundBody, &request) {
		writeSoundRefusal(w, http.StatusBadRequest, soundCodeBadSound, 0, p.brandName())
		return
	}

	if !p.refuseUnlessInVoice(w, userID, cfg) {
		return
	}

	st := p.sounds()
	now := st.now()
	if !p.limiters().soundUpload.allowAt(userID, now) {
		writeSoundRefusal(w, http.StatusTooManyRequests, soundCodeRateLimited, p.limiters().soundUpload.wait(userID, now), p.brandName())
		return
	}

	name, ok := p.cleanSoundName(userID, request.Name)
	if !ok {
		writeSoundRefusal(w, http.StatusBadRequest, soundCodeBadName, 0, p.brandName())
		return
	}

	emoji, ok := p.resolveSoundEmoji(request.Emoji)
	if !ok {
		writeSoundRefusal(w, http.StatusBadRequest, soundCodeBadEmoji, 0, p.brandName())
		return
	}

	mine := 0
	for _, entry := range doc.Entries {
		if entry.Owner == userID {
			mine++
		}
	}
	if mine >= maxSoundsPerPerson {
		writeSoundRefusal(w, http.StatusConflict, soundCodeYoursFull, 0, p.brandName())
		return
	}
	if len(doc.Entries) >= maxSoundsInAll {
		writeSoundRefusal(w, http.StatusConflict, soundCodeFull, 0, p.brandName())
		return
	}

	section, err := base64.StdEncoding.DecodeString(request.Packets)
	if err != nil {
		writeSoundRefusal(w, http.StatusBadRequest, soundCodeBadSound, 0, p.brandName())
		return
	}

	packets, ok := parseSoundPackets(section)
	if !ok {
		writeSoundRefusal(w, http.StatusBadRequest, soundCodeBadSound, 0, p.brandName())
		return
	}

	checked, code, err := checkSoundPackets(packets)
	if code != "" {
		if errors.Is(err, errDecoderPanic) {
			p.API.LogWarn("The Opus decoder panicked on a sound somebody uploaded; it was refused", "user_id", userID)
		}
		writeSoundRefusal(w, http.StatusBadRequest, code, 0, p.brandName())

		return
	}

	soundID := newID()
	if soundID == "" {
		writeSoundRefusal(w, http.StatusServiceUnavailable, soundCodeStore, 0, p.brandName())
		return
	}

	addedAt := now.UnixMilli()
	blob, _ := soundBlobOf(addedAt, packets)
	if appErr := p.API.KVSet(kvSoundPrefix+soundID, blob); appErr != nil {
		p.API.LogWarn("Could not save a sound", "user_id", userID, "error", appErr.Error())
		writeSoundRefusal(w, http.StatusServiceUnavailable, soundCodeStore, 0, p.brandName())

		return
	}

	err = p.changeSounds(func(doc *soundsDoc) (bool, error) {
		if _, blocked := doc.Blocked[userID]; blocked {
			return false, errSoundBlocked
		}

		owned := 0
		for _, entry := range doc.Entries {
			if entry.ID == soundID {
				return false, nil
			}
			if entry.Owner == userID {
				owned++
			}
		}
		if owned >= maxSoundsPerPerson {
			return false, errSoundYoursFull
		}
		if len(doc.Entries) >= maxSoundsInAll {
			return false, errSoundBoardFull
		}

		doc.Entries = append(doc.Entries, soundEntry{
			ID: soundID, Owner: userID, Name: name, Emoji: emoji,
			Ms: checked.ms, Bytes: len(section), Level: checked.level, At: addedAt,
		})

		return true, nil
	})
	if err != nil {
		if appErr := p.API.KVDelete(kvSoundPrefix + soundID); appErr != nil {
			p.API.LogWarn("Could not take back a sound the board did not take", "sound_id", soundID, "error", appErr.Error())
		}

		p.writeSoundWriteError(w, err)

		return
	}

	p.API.LogInfo("Added a sound", "user_id", userID, "sound_id", soundID, "ms", checked.ms)
	p.refreshSoon()

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]string{"id": soundID})
}

var errSoundBlocked = errors.New("blocked from the soundboard")

// writeSoundWriteError answers a board write that did not land.
func (p *Plugin) writeSoundWriteError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errSoundBlocked):
		writeSoundRefusal(w, http.StatusForbidden, soundCodeBlocked, 0, p.brandName())
	case errors.Is(err, errSoundYoursFull):
		writeSoundRefusal(w, http.StatusConflict, soundCodeYoursFull, 0, p.brandName())
	case errors.Is(err, errSoundBoardFull):
		writeSoundRefusal(w, http.StatusConflict, soundCodeFull, 0, p.brandName())
	case errors.Is(err, errSoundGone):
		writeSoundRefusal(w, http.StatusGone, soundCodeGone, 0, p.brandName())
	case errors.Is(err, errSoundNotYours):
		writeSoundRefusal(w, http.StatusForbidden, soundCodeNotYours, 0, p.brandName())
	default:
		writeSoundRefusal(w, http.StatusServiceUnavailable, soundCodeStore, 0, p.brandName())
	}
}

// refuseUnlessInVoice answers 403 not_in_voice for somebody not matched to a
// Mumble session now, in any state, and 503 bot_offline while Mumble is not
// connected; true when they are in.
func (p *Plugin) refuseUnlessInVoice(w http.ResponseWriter, userID string, cfg *configuration) bool {
	read := p.voiceStateOf(userID, cfg)

	switch {
	case read.in:
		return true
	case read.code == soundCodeBotOffline:
		writeSoundRefusal(w, http.StatusServiceUnavailable, soundCodeBotOffline, 0, p.brandName())
	case read.code == soundCodeStore:
		writeSoundRefusal(w, http.StatusServiceUnavailable, soundCodeStore, 0, p.brandName())
	default:
		writeSoundRefusal(w, http.StatusForbidden, soundCodeNotInVoice, 0, p.brandName())
	}

	return false
}

type soundEditRequest struct {
	Name  *string            `json:"name"`
	Emoji *soundEmojiRequest `json:"emoji"`
}

// handleSoundEdit is POST /api/v1/sounds/edit?id=: a new name or emoji for
// one of your own sounds, while you are in Mumble.
func (p *Plugin) handleSoundEdit(w http.ResponseWriter, r *http.Request, userID string) {
	cfg := p.getConfiguration()
	if !soundsOn(w, cfg) {
		return
	}

	if !p.requireLinkedStream(w, userID, soundNeedsLink) {
		return
	}

	st := p.sounds()
	now := st.now()
	if !p.limiters().soundEdit.allowAt(userID, now) {
		writeSoundRefusal(w, http.StatusTooManyRequests, soundCodeRateLimited, p.limiters().soundEdit.wait(userID, now), p.brandName())
		return
	}

	soundID := r.URL.Query().Get("id")
	var request soundEditRequest
	if !validPlanID(soundID) || !readStrictJSON(w, r, maxAwardBody, &request) || (request.Name == nil && request.Emoji == nil) {
		writeSoundRefusal(w, http.StatusBadRequest, soundCodeBadRequest, 0, p.brandName())
		return
	}

	doc, readable, err := p.board()
	if err != nil || !readable {
		writeSoundRefusal(w, http.StatusServiceUnavailable, soundCodeStore, 0, p.brandName())
		return
	}

	var entry *soundEntry
	for i := range doc.Entries {
		if doc.Entries[i].ID == soundID {
			entry = &doc.Entries[i]
		}
	}
	if entry == nil {
		writeSoundRefusal(w, http.StatusGone, soundCodeGone, 0, p.brandName())
		return
	}
	if entry.Owner != userID {
		writeSoundRefusal(w, http.StatusForbidden, soundCodeNotYours, 0, p.brandName())
		return
	}

	if !p.refuseUnlessInVoice(w, userID, cfg) {
		return
	}

	name := entry.Name
	if request.Name != nil {
		cleaned, ok := p.cleanSoundName(userID, *request.Name)
		if !ok {
			writeSoundRefusal(w, http.StatusBadRequest, soundCodeBadName, 0, p.brandName())
			return
		}
		name = cleaned
	}

	emoji := entry.Emoji
	if request.Emoji != nil {
		resolved, ok := p.resolveSoundEmoji(*request.Emoji)
		if !ok {
			writeSoundRefusal(w, http.StatusBadRequest, soundCodeBadEmoji, 0, p.brandName())
			return
		}
		emoji = resolved
	}

	err = p.changeSounds(func(doc *soundsDoc) (bool, error) {
		for i := range doc.Entries {
			stored := &doc.Entries[i]
			if stored.ID != soundID {
				continue
			}
			if stored.Owner != userID {
				return false, errSoundNotYours
			}
			if stored.Name == name && stored.Emoji == emoji {
				return false, nil
			}

			stored.Name, stored.Emoji = name, emoji

			return true, nil
		}

		return false, errSoundGone
	})
	if err != nil {
		p.writeSoundWriteError(w, err)
		return
	}

	p.refreshSoon()

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// removeSounds takes sounds off the board, those keep says, and then their
// blobs, and answers which went. A blob whose delete fails is left for the
// next start's sweep.
func (p *Plugin) removeSounds(keep func(entry soundEntry) (bool, error)) ([]string, error) {
	var removed []string

	err := p.changeSounds(func(doc *soundsDoc) (bool, error) {
		removed = nil

		kept := doc.Entries[:0:0]
		for _, entry := range doc.Entries {
			stays, err := keep(entry)
			if err != nil {
				return false, err
			}
			if stays {
				kept = append(kept, entry)
				continue
			}
			removed = append(removed, entry.ID)
		}

		if len(removed) == 0 {
			return false, nil
		}
		doc.Entries = kept

		return true, nil
	})
	if err != nil {
		return nil, err
	}

	for _, soundID := range removed {
		p.forgetSoundBlob(soundID)
		if appErr := p.API.KVDelete(kvSoundPrefix + soundID); appErr != nil {
			p.API.LogWarn("Could not delete a sound's audio; the next start's sweep will", "sound_id", soundID, "error", appErr.Error())
		}
	}

	return removed, nil
}

// handleSoundRemove is POST /api/v1/sounds/remove?id=: one of your own sounds
// off the board. Open, a takeback: it works linked or not, in Mumble or not,
// soundboard on or off.
func (p *Plugin) handleSoundRemove(w http.ResponseWriter, r *http.Request, userID string) {
	st := p.sounds()
	now := st.now()
	if !p.limiters().soundEdit.allowAt(userID, now) {
		writeSoundRefusal(w, http.StatusTooManyRequests, soundCodeRateLimited, p.limiters().soundEdit.wait(userID, now), p.brandName())
		return
	}

	soundID := r.URL.Query().Get("id")
	if !validPlanID(soundID) {
		writeSoundRefusal(w, http.StatusBadRequest, soundCodeBadRequest, 0, p.brandName())
		return
	}

	found := false
	removed, err := p.removeSounds(func(entry soundEntry) (bool, error) {
		if entry.ID != soundID {
			return true, nil
		}
		found = true
		if entry.Owner != userID {
			return false, errSoundNotYours
		}

		return false, nil
	})
	if err == nil && (!found || len(removed) == 0) {
		err = errSoundGone
	}
	if errors.Is(err, errSoundsUnreadable) {
		err = errSoundGone
	}
	if err != nil {
		p.writeSoundWriteError(w, err)
		return
	}

	p.API.LogInfo("Deleted a sound", "user_id", userID, "sound_id", soundID)
	p.refreshSoon()

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSoundRemoveMine is POST /api/v1/sounds/remove-mine: every sound the
// caller added, gone. Open, a takeback, and the caller's own only.
func (p *Plugin) handleSoundRemoveMine(w http.ResponseWriter, userID string) {
	st := p.sounds()
	now := st.now()
	if !p.limiters().soundEdit.allowAt(userID, now) {
		writeSoundRefusal(w, http.StatusTooManyRequests, soundCodeRateLimited, p.limiters().soundEdit.wait(userID, now), p.brandName())
		return
	}

	removed, err := p.removeSounds(func(entry soundEntry) (bool, error) {
		return entry.Owner != userID, nil
	})
	if errors.Is(err, errSoundsUnreadable) {
		removed, err = nil, nil
	}
	if err != nil {
		p.writeSoundWriteError(w, err)
		return
	}

	for _, soundID := range removed {
		p.API.LogInfo("Deleted a sound", "user_id", userID, "sound_id", soundID)
	}
	if len(removed) > 0 {
		p.refreshSoon()
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]int{"deleted": len(removed)})
}

// setSoundFileHeaders are 3.1.0's file headers, and the ETag of the sound's
// own checksum, so a repeat Listen is a cheap 304 after the checks have run
// again: never served from a browser's cache without them.
func setSoundFileHeaders(w http.ResponseWriter, sum string) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", `"`+sum+`"`)
}

// serveSoundAudio answers with a sound's packets, as uploaded: a big-endian
// u16 count, then each packet's big-endian u16 length and bytes.
func (p *Plugin) serveSoundAudio(w http.ResponseWriter, r *http.Request, soundID string) {
	blob, err := p.soundBlobFor(soundID)
	if err != nil {
		if errors.Is(err, errSoundGone) {
			writeSoundRefusal(w, http.StatusGone, soundCodeGone, 0, p.brandName())
		} else {
			writeSoundRefusal(w, http.StatusServiceUnavailable, soundCodeStore, 0, p.brandName())
		}

		return
	}

	setSoundFileHeaders(w, blob.sum)

	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, `"`+blob.sum+`"`) {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(blob.section)
}

// handleSoundAudio is GET /api/v1/sounds/audio?id=, for Listen here: a
// sound's packets, to its owner only. Answered only to a request the panel
// made (madeByScript), checked before the meter and before anything is read,
// because Mattermost checks no CSRF token on a GET, and a Markdown image in a
// post is fetched with every reader's session.
func (p *Plugin) handleSoundAudio(w http.ResponseWriter, r *http.Request, userID string) {
	if !madeByScript(r) {
		w.Header().Set("Cache-Control", "no-store")
		writeError(w, http.StatusForbidden, "Ask from the panel.")

		return
	}

	cfg := p.getConfiguration()
	if !soundsOn(w, cfg) {
		return
	}

	soundID := r.URL.Query().Get("id")
	if !validPlanID(soundID) {
		writeSoundRefusal(w, http.StatusBadRequest, soundCodeBadRequest, 0, p.brandName())
		return
	}

	doc, readable, err := p.board()
	if err != nil || !readable {
		writeSoundRefusal(w, http.StatusServiceUnavailable, soundCodeStore, 0, p.brandName())
		return
	}

	owner := ""
	for _, entry := range doc.Entries {
		if entry.ID == soundID {
			owner = entry.Owner
		}
	}
	switch {
	case owner == "":
		writeSoundRefusal(w, http.StatusGone, soundCodeGone, 0, p.brandName())
		return
	case owner != userID:
		writeSoundRefusal(w, http.StatusForbidden, soundCodeNotYours, 0, p.brandName())
		return
	}

	st := p.sounds()
	now := st.now()
	if !p.limiters().soundAudio.allowAt(userID, now) {
		writeSoundRefusal(w, http.StatusTooManyRequests, soundCodeRateLimited, p.limiters().soundAudio.wait(userID, now), p.brandName())
		return
	}

	p.serveSoundAudio(w, r, soundID)
}

// sortedSoundOwners is the board's owners, in order, for counting people.
func sortedSoundOwners(entries []soundEntry) []string {
	seen := map[string]bool{}
	for _, entry := range entries {
		seen[entry.Owner] = true
	}

	owners := make([]string, 0, len(seen))
	for owner := range seen {
		owners = append(owners, owner)
	}
	sort.Strings(owners)

	return owners
}
