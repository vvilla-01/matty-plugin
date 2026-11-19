package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Pictures for the 24/7 channels.
//
// A stream that belongs to somebody wears their Mattermost profile picture. An
// always-on channel belongs to nobody, so it had only its initials — "LF" in a
// circle for a channel called Lo-fi radio. These are its picture instead: one
// per always-on stream key, uploaded from the System Console.
//
// Kept in the KV store for the same reason as the rest of the artwork, and
// checked the same way: from the bytes, never from what the browser said.
//
// The stream key never reaches anybody but an admin. Everybody else asks for a
// channel's picture by the stream's opaque HANDLE, exactly as they ask for its
// video; the admin side, which is only for people who can already read the
// keys in the System Console, is the one place a key is named.
//
// Each channel has two pictures, for two different places:
//
//   - picture: the square one, where a person's stream shows their face.
//   - thumb: the wide one on its card in the Live Now list, where a person's
//     stream shows a preview still. A 24/7 channel is never previewed (see
//     stills.go), so this is the only way its card gets a picture. There is no
//     shipped one: a card without it shows what it showed before.

const (
	kvChannelPicturePrefix = "channelpic_"
	kvChannelThumbPrefix   = "channelthumb_"

	channelPictureKind = "picture"
	channelThumbKind   = "thumb"

	// How long a remembered version is trusted before the store is read
	// again. An upload on THIS node forgets it at once; this is for the other
	// nodes of a cluster, which are not told, and would otherwise go on naming
	// the old picture until they restarted.
	channelVersionTTL = 5 * time.Minute
)

// channelPictureKey is where a channel's upload is kept. Hashed, so a stream
// key of any length makes a valid KV key and none is written out in the clear
// in the store's key list.
func channelPictureKey(streamKey string) string {
	return hashedChannelKey(kvChannelPicturePrefix, streamKey)
}

// channelThumbKey is where a channel's card picture is kept, hashed the same way.
func channelThumbKey(streamKey string) string {
	return hashedChannelKey(kvChannelThumbPrefix, streamKey)
}

func hashedChannelKey(prefix, streamKey string) string {
	sum := sha256.Sum256([]byte(normalizeKey(streamKey)))

	return prefix + hex.EncodeToString(sum[:12])
}

// channelImageKey is where a channel's picture of either kind is kept.
func channelImageKey(kind, streamKey string) string {
	if kind == channelThumbKind {
		return channelThumbKey(streamKey)
	}

	return channelPictureKey(streamKey)
}

// channelImageKind reads the kind a request names. No kind is the square
// picture, which is what every request meant before there were two.
func channelImageKind(r *http.Request) (string, bool) {
	switch strings.TrimSpace(r.URL.Query().Get("kind")) {
	case "", channelPictureKind:
		return channelPictureKind, true
	case channelThumbKind:
		return channelThumbKind, true
	default:
		return "", false
	}
}

// loadChannelUpload reads an admin's upload out of the KV store, or nil.
func (p *Plugin) loadChannelUpload(kvKey string) *storedBrand {
	raw, appErr := p.API.KVGet(kvKey)
	if appErr != nil || len(raw) == 0 {
		return nil
	}

	var stored storedBrand
	if err := json.Unmarshal(raw, &stored); err == nil && len(stored.Data) > 0 && stored.ContentType != "" {
		return &stored
	}

	p.API.LogWarn("A stored channel picture could not be read; carrying on as if none had been uploaded")

	return nil
}

// channelThumbnail is a channel's card picture, or nil. Nothing is shipped.
func (p *Plugin) channelThumbnail(streamKey string) *storedBrand {
	return p.loadChannelUpload(channelThumbKey(streamKey))
}

// channelImage is a channel's picture of either kind, and whether it is the
// admin's own rather than a shipped one.
func (p *Plugin) channelImage(kind, streamKey string) (*storedBrand, bool) {
	if kind == channelThumbKind {
		thumb := p.channelThumbnail(streamKey)

		return thumb, thumb != nil
	}

	return p.channelPicture(streamKey)
}

// channelPicture is a channel's picture: the admin's upload, or nil. No
// picture is built in, so custom is true whenever there is one.
func (p *Plugin) channelPicture(streamKey string) (picture *storedBrand, custom bool) {
	if stored := p.loadChannelUpload(channelPictureKey(streamKey)); stored != nil {
		return stored, true
	}

	return nil, false
}

// pictureVersion names one version of a picture. It is part of the address the
// panel loads it from, so a new picture is a new address — which is what lets
// that address be cached, and what makes every panel pick up a change on the
// next poll without being told.
func pictureVersion(data []byte) string {
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:6])
}

// channelVersion is one remembered answer to "which version of this picture is
// current" — including "there is none", which is the usual answer for a card
// picture and just as worth not asking the store again.
type channelVersion struct {
	version string
	checked time.Time
}

func channelVersionKey(kind, streamKey string) string {
	return kind + ":" + normalizeKey(streamKey)
}

// channelImageVersion is the version of a channel's picture of either kind, or
// "" when it has none, for the snapshot.
//
// Remembered, because the poller asks for every 24/7 channel on every poll, and
// the honest answer is a KV read, a base64 decode of up to half a megabyte and a
// hash — to learn, nearly every time, that nothing has changed. An upload or a
// reset forgets the answer at once, so the change reaches the very next poll.
//
// The generation guards the one race that matters: a poll that read the store
// just BEFORE an upload landed must not put the old version back in after the
// upload forgot it, where it would stay until it timed out.
func (p *Plugin) channelImageVersion(kind, streamKey string, now time.Time) string {
	key := channelVersionKey(kind, streamKey)

	p.channelVersionsLock.Lock()
	cached, known := p.channelVersions[key]
	generation := p.channelVersionsGen
	p.channelVersionsLock.Unlock()

	if known && now.Sub(cached.checked) < channelVersionTTL {
		return cached.version
	}

	version := ""
	if picture, _ := p.channelImage(kind, streamKey); picture != nil {
		version = pictureVersion(picture.Data)
	}

	p.channelVersionsLock.Lock()
	if p.channelVersionsGen == generation {
		if p.channelVersions == nil {
			p.channelVersions = map[string]channelVersion{}
		}
		p.channelVersions[key] = channelVersion{version: version, checked: now}
	}
	p.channelVersionsLock.Unlock()

	return version
}

// forgetChannelImageVersion drops what is remembered about a channel's picture
// of one kind, after it has been changed.
func (p *Plugin) forgetChannelImageVersion(kind, streamKey string) {
	p.channelVersionsLock.Lock()
	defer p.channelVersionsLock.Unlock()

	p.channelVersionsGen++
	delete(p.channelVersions, channelVersionKey(kind, streamKey))
}

// forgetAllChannelImageVersions drops everything remembered about the 24/7
// pictures, when the configuration changes.
//
// An upload forgets only on the node that took it. A settings save reaches
// every node of a cluster, so this makes saving the settings the way to have
// every node look at the pictures again now rather than within
// channelVersionTTL. It also lets go of the versions of keys that are no
// longer always-on, which nothing would ask about again.
func (p *Plugin) forgetAllChannelImageVersions() {
	p.channelVersionsLock.Lock()
	defer p.channelVersionsLock.Unlock()

	p.channelVersionsGen++
	p.channelVersions = nil
}

// rememberChannelHandles records the exact key behind each always-on stream in
// this poll, and forgets any whose key is no longer always-on. Called from
// decorate, which is where handles are made.
func (p *Plugin) rememberChannelHandles(seen map[string]string, alwaysOn map[string]bool) {
	p.channelHandlesLock.Lock()
	defer p.channelHandlesLock.Unlock()

	if p.channelHandles == nil {
		p.channelHandles = map[string]string{}
	}

	for handle, key := range seen {
		p.channelHandles[handle] = key
	}

	for handle, key := range p.channelHandles {
		if !alwaysOn[normalizeKey(key)] {
			delete(p.channelHandles, handle)
		}
	}
}

func (p *Plugin) rememberedChannelKey(handle string) string {
	p.channelHandlesLock.Lock()
	defer p.channelHandlesLock.Unlock()

	return p.channelHandles[handle]
}

// channelKeyForPictureHandle resolves a handle to the always-on stream key it
// stands for. A live stream first, as for playback. Then, so a channel that
// has dropped off the air for a moment keeps its picture on the tile waiting
// for it: the exact key remembered from when it was last seen, and failing
// that the configured keys as written. Anything else — a person's stream, a
// handle that means nothing — resolves to nothing.
func (p *Plugin) channelKeyForPictureHandle(handle string, alwaysOn map[string]bool) string {
	if handle == "" {
		return ""
	}

	if key := p.streamKeyForHandle(handle); key != "" {
		if alwaysOn[normalizeKey(key)] {
			return key
		}

		return ""
	}

	if key := p.rememberedChannelKey(handle); key != "" && alwaysOn[normalizeKey(key)] {
		return key
	}

	for key := range alwaysOn {
		if candidate := p.streamHandle(key); candidate != "" && hmac.Equal([]byte(candidate), []byte(handle)) {
			return key
		}
	}

	return ""
}

// handleChannelPicture answers /api/v1/channel/picture?handle=…, behind the
// access gate, for people who can watch. The panel's <img> sends the session
// cookie like any other request to this server, so there is no reason for this
// to be public the way the stylesheet's artwork has to be.
func (p *Plugin) handleChannelPicture(w http.ResponseWriter, r *http.Request) {
	p.serveChannelImage(w, r, channelPictureKind)
}

// handleChannelThumbnail answers /api/v1/channel/thumbnail?handle=…, the card
// picture, on exactly the same terms as the square one.
func (p *Plugin) handleChannelThumbnail(w http.ResponseWriter, r *http.Request) {
	p.serveChannelImage(w, r, channelThumbKind)
}

func (p *Plugin) serveChannelImage(w http.ResponseWriter, r *http.Request, kind string) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}

	key := p.channelKeyForPictureHandle(strings.TrimSpace(r.URL.Query().Get("handle")), p.getConfiguration().alwaysOnSet())
	if key == "" {
		writeError(w, http.StatusNotFound, "Not found.")
		return
	}

	picture, _ := p.channelImage(kind, key)
	if picture == nil {
		writeError(w, http.StatusNotFound, "Not found.")
		return
	}

	// Revalidated every time, whatever version was asked for, and private: a
	// picture kept for a day would be shown again without the access gate
	// hearing of it, after a let-out, an unlink, or somebody else signing in
	// on the same browser. The ETag makes a re-display a cheap 304 for
	// whoever may still see it. The version stays in the address, so a new
	// picture still comes with a new address.
	writeImage(w, r, picture.ContentType, picture.Data, "private, no-cache")
}

// adminChannel is one always-on channel as the settings page lists it. The
// key is shown there; the handle is what the page names the channel by in an
// address, as the panel's own pictures are.
type adminChannel struct {
	Key    string `json:"key"`
	Handle string `json:"handle"`
	Name   string `json:"name,omitempty"`
	Custom bool   `json:"custom"`

	// Always false: no channel picture is built in. Kept so a settings page
	// that reads it goes on offering Remove.
	Shipped bool   `json:"shipped"`
	Version string `json:"version,omitempty"`

	// The card picture. Nothing is shipped, so it is either the admin's or
	// there is none.
	ThumbCustom  bool   `json:"thumbCustom"`
	ThumbVersion string `json:"thumbVersion,omitempty"`
}

// handleChannelPictures is the admin side: which always-on channels there are
// and what each has, a preview, replacing one, and putting one back.
//
// One channel is named by its handle, ?handle=, never by its key: an address
// is written down in a reverse proxy's access log, which is read by more
// people than the settings page is, and a 24/7 key is a stream key anybody
// can publish with. ?key= is still taken, so that a System Console page
// opened before an upgrade keeps working until it is reloaded; it goes in a
// later version.
//
// Only keys that are CURRENTLY configured as always-on can be given a picture.
// A picture for any other key would be stored for nothing — the panel only
// looks for one on an always-on stream — and would sit in the store unseen.
//
// kind=thumb does all of the same to the card picture instead; no kind is the
// square one.
func (p *Plugin) handleChannelPictures(w http.ResponseWriter, r *http.Request, userID string) {
	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, "Only a system admin can change the channel pictures.")
		return
	}

	kind, known := channelImageKind(r)
	if !known {
		writeError(w, http.StatusBadRequest, "Say which picture this is: picture or thumb.")
		return
	}

	cfg := p.getConfiguration()
	alwaysOn := cfg.alwaysOnSet()
	query := r.URL.Query()
	named := query.Has("handle") || query.Has("key")
	key := normalizeKey(strings.TrimSpace(query.Get("key")))
	if query.Has("handle") {
		key = normalizeKey(p.channelKeyForPictureHandle(strings.TrimSpace(query.Get("handle")), alwaysOn))
	}

	switch r.Method {
	case http.MethodGet:
		if named {
			// A preview of one.
			if !alwaysOn[key] {
				writeError(w, http.StatusNotFound, "Not found.")
				return
			}

			picture, _ := p.channelImage(kind, key)
			if picture == nil {
				writeError(w, http.StatusNotFound, "Not found.")
				return
			}

			writeImage(w, r, picture.ContentType, picture.Data, "private, no-cache")

			return
		}

		names := cfg.nameOverrides()
		channels := make([]adminChannel, 0, len(alwaysOn))
		for configured := range alwaysOn {
			picture, custom := p.channelPicture(configured)

			channel := adminChannel{Key: configured, Handle: p.streamHandle(configured), Name: names[configured], Custom: custom}
			if picture != nil {
				channel.Version = pictureVersion(picture.Data)
			}

			if thumb := p.channelThumbnail(configured); thumb != nil {
				channel.ThumbCustom = true
				channel.ThumbVersion = pictureVersion(thumb.Data)
			}

			channels = append(channels, channel)
		}
		sort.Slice(channels, func(i, j int) bool { return channels[i].Key < channels[j].Key })

		writeJSON(w, http.StatusOK, channels)

	case http.MethodPost:
		if !alwaysOn[key] {
			writeError(w, http.StatusBadRequest, "That is not one of the 24/7 stream keys. Add it to 24/7 stream keys and save first.")
			return
		}

		contentType, image, ok := readImageUpload(w, r)
		if !ok {
			return
		}

		stored, err := json.Marshal(storedBrand{ContentType: contentType, Data: image})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "That image could not be stored.")
			return
		}

		if appErr := p.API.KVSet(channelImageKey(kind, key), stored); appErr != nil {
			p.API.LogWarn("Could not store a channel picture", "kind", kind, "error", appErr.Error())
			writeError(w, http.StatusInternalServerError, "That image could not be stored.")

			return
		}
		p.forgetChannelImageVersion(kind, key)

		// The key is not logged: the log is read by more people than the
		// settings page is.
		p.API.LogWarn("A system admin changed a 24/7 channel's picture",
			"user_id", userID, "kind", kind, "bytes", len(image), "type", contentType)

		// Everybody's panel learns the new version on the next poll; asking
		// for that poll now rather than waiting up to a whole interval.
		p.refreshSoon()

		writeJSON(w, http.StatusOK, map[string]string{"contentType": contentType, "version": pictureVersion(image)})

	case http.MethodDelete:
		if !alwaysOn[key] {
			writeError(w, http.StatusBadRequest, "That is not one of the 24/7 stream keys.")
			return
		}

		if appErr := p.API.KVDelete(channelImageKey(kind, key)); appErr != nil {
			writeError(w, http.StatusInternalServerError, "That could not be removed.")
			return
		}
		p.forgetChannelImageVersion(kind, key)

		p.API.LogWarn("A system admin put a 24/7 channel's picture back", "user_id", userID, "kind", kind)
		p.refreshSoon()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
	}
}
