package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// The artwork, changeable without a rebuild.
//
// Separate pieces, because they are different jobs and one image cannot do
// both well: the LOGO is the emblem faded into the corner of the panel, seen at
// roughly three hundred pixels, and the ICON is the button in the App Bar, seen
// at twenty-four. Only the icon is built in (Matty's own, a round chat
// bubble); a server has an emblem and a No Signal ghost only once an admin
// uploads them, and /static/brand/logo and /static/brand/ghost answer 404
// until then.
//
// Stored in the KV store rather than in the configuration. A few hundred
// kilobytes of base64 in a config value is written to disk, held in memory and
// shipped to every node in the cluster on every configuration change — for a
// picture. This is the same reasoning as the bot's avatar, which is why neither
// of them is a setting in the ordinary sense.
//
// Served from the same public path as the fonts, and for the same reason: a
// stylesheet's url() and the App Bar's <img> are not requests anybody should
// have to reason about cookies for.

const (
	// maxBrandBytes bounds an upload.
	maxBrandBytes = 512 << 10

	brandLogo = "logo"
	brandIcon = "icon"

	// The picture that flickers through the static on the No Signal screen now
	// and then, as if the set had caught a channel for half a second.
	brandGhost = "ghost"
)

// brandDefaults are the pieces of artwork, each with the file served when
// nothing has been uploaded: embedded in the binary, or "" for a piece that
// has none built in.
var brandDefaults = map[string]string{
	brandLogo:  "",
	brandIcon:  "static/matty-icon.svg",
	brandGhost: "",
}

// brandArtworkTTL is how long a node takes the answer to "which pieces does
// this server have" as given. An upload or a removal on this node clears it at
// once; other nodes in a cluster follow within it.
const brandArtworkTTL = time.Minute

// brandArtwork says which of the pieces a server may lack it has: whether
// there is an emblem and a No Signal ghost to show. The icon always exists, so
// it is not here. Sent in /access, /me and the "me" event.
type brandArtwork struct {
	Emblem bool `json:"emblem"`
	Ghost  bool `json:"ghost"`
}

// brandArtworkSeen is the last answer, and when it was read.
type brandArtworkSeen struct {
	at  time.Time
	art brandArtwork
}

// artwork is which pieces this server has, from the store at most once a
// brandArtworkTTL.
func (p *Plugin) artwork() brandArtwork {
	if seen := p.artworkSeen.Load(); seen != nil && time.Since(seen.at) < brandArtworkTTL {
		return seen.art
	}

	art := brandArtwork{
		Emblem: p.hasBrand(brandLogo),
		Ghost:  p.hasBrand(brandGhost),
	}
	p.artworkSeen.Store(&brandArtworkSeen{at: time.Now(), art: art})

	return art
}

// hasBrand reports whether there is anything to serve for one piece.
func (p *Plugin) hasBrand(which string) bool {
	return brandDefaults[which] != "" || p.loadBrand(which) != nil
}

// artworkChanged forgets the last answer and tells every open client, which
// asks again and draws what there is now.
func (p *Plugin) artworkChanged() {
	p.artworkSeen.Store(nil)
	p.tellEverybodyMe()
}

// storedBrand is one uploaded image. The bytes go through JSON as base64, which
// is what encoding/json does with a []byte, so the type and the image are
// written and read as one value rather than two that could disagree.
type storedBrand struct {
	ContentType string `json:"contentType"`
	Data        []byte `json:"data"`
}

func brandKey(which string) string {
	return kvBrandPrefix + which
}

func validBrandName(which string) bool {
	_, known := brandDefaults[which]

	return known
}

// loadBrand returns the uploaded image, or nil when there is none.
func (p *Plugin) loadBrand(which string) *storedBrand {
	raw, appErr := p.API.KVGet(brandKey(which))
	if appErr != nil || len(raw) == 0 {
		return nil
	}

	var stored storedBrand
	if err := json.Unmarshal(raw, &stored); err != nil {
		p.API.LogWarn("The stored artwork could not be read; falling back to the built-in one, if there is one",
			"which", which, "error", err.Error())

		return nil
	}

	if len(stored.Data) == 0 || stored.ContentType == "" {
		return nil
	}

	return &stored
}

// serveBrand answers /static/brand/<logo|icon|ghost>: the upload, or the
// built-in file, or 404 for a piece with neither.
//
// No session is required, exactly as for the fonts: this is referenced from a
// stylesheet and from an <img> in Mattermost's own App Bar.
func (p *Plugin) serveBrand(w http.ResponseWriter, r *http.Request, which string) {
	if !validBrandName(which) {
		writeError(w, http.StatusNotFound, "Not found.")
		return
	}

	var contentType string
	var data []byte

	if stored := p.loadBrand(which); stored != nil {
		contentType = stored.ContentType
		data = stored.Data
	} else {
		if brandDefaults[which] == "" {
			writeError(w, http.StatusNotFound, "Not found.")
			return
		}

		embedded, err := staticFiles.ReadFile(brandDefaults[which])
		if err != nil {
			writeError(w, http.StatusNotFound, "Not found.")
			return
		}
		data = embedded

		// From the bytes, like an upload, rather than assumed: the defaults
		// used to all be SVG and this said so, and the first PNG default would
		// have gone out labelled as an SVG — which nosniff then refuses to
		// draw at all.
		contentType, _ = imageType(embedded)
	}

	writeImage(w, r, contentType, data, "no-cache")
}

// writeImage sends an image the plugin holds — shipped or uploaded — with the
// headers every one of them needs.
//
// cacheControl is the caller's: the artwork lives at a stable URL whose
// CONTENTS change, so it is revalidated every time ("no-cache") — a long cache
// would mean an admin uploading new artwork and seeing the old for seven days
// with no way to tell why. The ETag makes that revalidation a 304 in the
// ordinary case. A URL that carries a version of its own can be cached longer.
func writeImage(w http.ResponseWriter, r *http.Request, contentType string, data []byte, cacheControl string) {
	sum := sha256.Sum256(data)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`

	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("X-Content-Type-Options", "nosniff")

	// An SVG served from this origin is a document, and a document can carry a
	// script. Only a system admin can upload one and a system admin can already
	// install plugins, so this is not a privilege boundary — but the URL is
	// reachable by anybody, and a link to it should not be able to run anything
	// against the person who follows it.
	//
	// The sandbox directive is kept because it was MEASURED not to break the
	// image: served with these exact headers, an SVG renders identically in an
	// <img> and in a CSS background-image, with and without it. The worry was
	// that a browser would treat the whole response as a sandboxed document and
	// refuse to draw it, which would have taken the watermark and the App Bar
	// icon out at once.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")

	if match := r.Header.Get("If-None-Match"); match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handleBrand is the admin side: what is installed, replacing it, and putting
// the built-in icon back or removing an emblem or a ghost.
func (p *Plugin) handleBrand(w http.ResponseWriter, r *http.Request, userID string) {
	if !p.isSystemAdmin(userID) {
		writeError(w, http.StatusForbidden, "Only a system admin can change the artwork.")
		return
	}

	which := strings.TrimSpace(r.URL.Query().Get("which"))

	switch r.Method {
	case http.MethodGet:
		// Which pieces are the admin's own uploads, so the settings page can
		// offer to put the built-in icon back, or to remove an emblem or a
		// ghost, only when there is an upload to take away.
		custom := map[string]bool{}
		for which := range brandDefaults {
			custom[which] = p.loadBrand(which) != nil
		}
		writeJSON(w, http.StatusOK, custom)

	case http.MethodPost:
		if !validBrandName(which) {
			writeError(w, http.StatusBadRequest, "Say which piece of artwork this is.")
			return
		}
		p.uploadBrand(w, r, which, userID)

	case http.MethodDelete:
		if !validBrandName(which) {
			writeError(w, http.StatusBadRequest, "Say which piece of artwork this is.")
			return
		}
		if appErr := p.API.KVDelete(brandKey(which)); appErr != nil {
			writeError(w, http.StatusInternalServerError, "That could not be removed.")
			return
		}

		if brandDefaults[which] != "" {
			p.API.LogWarn("A system admin restored the built-in artwork", "user_id", userID, "which", which)
		} else {
			p.API.LogWarn("A system admin removed the artwork", "user_id", userID, "which", which)
		}
		p.artworkChanged()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
	}
}

func (p *Plugin) uploadBrand(w http.ResponseWriter, r *http.Request, which, userID string) {
	contentType, image, ok := readImageUpload(w, r)
	if !ok {
		return
	}

	stored, err := json.Marshal(storedBrand{ContentType: contentType, Data: image})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "That image could not be stored.")
		return
	}

	if appErr := p.API.KVSet(brandKey(which), stored); appErr != nil {
		p.API.LogWarn("Could not store the artwork", "which", which, "error", appErr.Error())
		writeError(w, http.StatusInternalServerError, "That image could not be stored.")

		return
	}

	p.API.LogWarn("A system admin changed the artwork",
		"user_id", userID, "which", which, "bytes", len(image), "type", contentType)
	p.artworkChanged()

	writeJSON(w, http.StatusOK, map[string]string{"contentType": contentType})
}

// readImageUpload reads an uploaded image out of the request body and decides
// what it is. On anything wrong it has already answered, and ok is false.
func readImageUpload(w http.ResponseWriter, r *http.Request) (contentType string, image []byte, ok bool) {
	image, err := io.ReadAll(io.LimitReader(r.Body, maxBrandBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "Could not read the image.")
		return "", nil, false
	}

	if len(image) > maxBrandBytes {
		writeError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("That image is too large; %d KB is the limit.", maxBrandBytes>>10))

		return "", nil, false
	}

	contentType, isImage := imageType(image)
	if !isImage {
		writeError(w, http.StatusBadRequest, "That is not an image. SVG, PNG, JPEG, GIF or WebP.")
		return "", nil, false
	}

	return contentType, image, true
}

// imageType decides what an upload actually IS, from its bytes rather than from
// the header the browser sent. Serving an HTML file back as text/html from this
// origin because somebody labelled it image/png is the whole reason this does
// not trust the Content-Type.
func imageType(data []byte) (string, bool) {
	sniffed := http.DetectContentType(data)

	if strings.HasPrefix(sniffed, "image/") {
		// DetectContentType returns the type with no parameters for images.
		return sniffed, true
	}

	// SVG is XML, so it sniffs as text. Accepted only when it really opens as
	// an SVG document, after any XML declaration or comments.
	if strings.HasPrefix(sniffed, "text/xml") || strings.HasPrefix(sniffed, "text/plain") {
		head := data
		if len(head) > 1024 {
			head = head[:1024]
		}
		if strings.Contains(strings.ToLower(string(head)), "<svg") {
			return "image/svg+xml", true
		}
	}

	return "", false
}
