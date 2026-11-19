package main

import (
	"crypto/hmac"
	"net/http"
	"net/url"
	"strings"
)

// Pop out, without a stream key in the snapshot (design/lock-screen.md §3.10).
//
// The snapshot is one broadcast to everybody, so it cannot be trimmed per
// person, and it used to carry the public watch address, stream key and all,
// for every stream whenever an admin had set one. Now the panel's Pop out is a
// plain link to GET /api/v1/watch/popout?handle=, which answers with a redirect
// to that address. It is for linked people only (the gate's class), because a
// stream key once handed out cannot be taken back by a let-out or an unlink:
// it keeps working on the public watch page, and at Broadcast Box as a WHEP
// bearer. A plain link works because a GET with the session cookie is
// authenticated without a CSRF token.
//
// Nothing here logs the address or the key.

const noWatchPage = "There is no watch page for that stream."

// handleWatchPopOut answers GET /api/v1/watch/popout?handle=…, behind the
// gate.
func (p *Plugin) handleWatchPopOut(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	site := p.getConfiguration().PublicSiteURL
	handle := strings.TrimSpace(r.URL.Query().Get("handle"))
	if site == "" || handle == "" {
		writeError(w, http.StatusNotFound, noWatchPage)
		return
	}

	// Only a stream on the air now, and never a 24/7 channel: the places Pop
	// out was offered before.
	key := ""
	for _, stream := range p.getSnapshot().Streams {
		if hmac.Equal([]byte(stream.Handle), []byte(handle)) {
			if !stream.AlwaysOn {
				key = stream.StreamKey
			}

			break
		}
	}
	if key == "" {
		writeError(w, http.StatusNotFound, noWatchPage)
		return
	}

	// The page that opens must not learn this address, which is where it was
	// sent from, and nothing on the way may keep the answer.
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Location", site+"/"+url.PathEscape(key))
	w.WriteHeader(http.StatusFound)
}
