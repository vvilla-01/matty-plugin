package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
)

// The two endpoints behind looking titles up: the planning form's search, and
// the pictures the panel shows. Both are for signed-in people only, like the
// rest of /api/v1, and neither is formatted for anybody in particular — what
// they answer is the same for everyone who asks the same thing.

type mediaSearchResponse struct {
	Results []mediaResult `json:"results"`

	// Unavailable is a sentence for the person planning, set instead of
	// results when this kind of title cannot be looked up here at all. Not an
	// error: the form still works, it just cannot find titles for them.
	Unavailable string `json:"unavailable,omitempty"`

	// Incomplete names a source that was asked and is missing from the
	// results, "Steam" or "IGDB": it did not answer in time, or failed. The
	// results are the other source's alone, so the form says that rather
	// than "nothing on either", and does not keep the answer.
	Incomplete string `json:"incomplete,omitempty"`
}

// mediaSearchRequest is the body of a title search.
type mediaSearchRequest struct {
	Kind string `json:"kind"` // "movie" or "game"
	Q    string `json:"q"`
}

// handleMediaSearch answers POST /api/v1/media/search, with what to look for
// in the body: {"kind": "movie"|"game", "q": "…"}.
//
// A POST, where it used to be a GET with a query string. A search costs
// something on the caller's behalf: a provider asked in their name, one of
// their searches, the answer kept, and every title it lists made shown here
// (refShownHere), so that anybody could have its pictures fetched. A GET can
// be made for somebody without their asking, by anything that loads an
// address with their session: a Markdown image in a post, in every reader's
// browser, and in any other client that shows the post. The GET was answered
// only with the header a script sends and an image cannot (madeByScript),
// which holds for a browser and for nothing else: a native app decides for
// itself which headers its picture loader sends. Nothing that loads an address
// makes a POST, and on a cookie session Mattermost checks its CSRF token, so
// the route no longer rests on what a client happens to send.
//
// It also keeps what somebody typed out of every log that records addresses.
func (p *Plugin) handleMediaSearch(w http.ResponseWriter, r *http.Request) {
	var request mediaSearchRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "Could not read the search.")
		return
	}

	kind := strings.ToLower(strings.TrimSpace(request.Kind))
	if kind != mediaKindMovie && kind != mediaKindGame {
		writeError(w, http.StatusBadRequest, "Say whether this is a movie or a game.")
		return
	}

	// Runs of spaces are one space, so "the  thing" and "the thing" are one
	// search and one cache entry.
	text := strings.Join(strings.Fields(model.SanitizeUnicode(request.Q)), " ")
	if length := utf8.RuneCountInString(text); length < minMediaQuery || length > maxMediaQuery {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("Type between %d and %d characters to search.", minMediaQuery, maxMediaQuery))

		return
	}

	w.Header().Set("Cache-Control", "no-store")

	cfg := p.getConfiguration()
	if message := cfg.mediaFlags().unavailable(kind); message != "" {
		writeJSON(w, http.StatusOK, mediaSearchResponse{Results: []mediaResult{}, Unavailable: message})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), mediaSearchBudget)
	defer cancel()

	results, incomplete, err := p.searchMedia(ctx, cfg, kind, text)
	if err != nil {
		p.API.LogWarn("A title search failed", "kind", kind, "error", err.Error())

		what := "movies and shows"
		if kind == mediaKindGame {
			what = "games"
		}
		writeError(w, http.StatusBadGateway, "Could not search for "+what+" just now. Try again in a moment.")

		return
	}

	writeJSON(w, http.StatusOK, mediaSearchResponse{Results: results, Incomplete: incomplete})
}

// madeByScript reports whether a request carries the header only a script can
// put on it, which the panel sends with every request it makes (client.ts,
// baseHeaders).
//
// For a GET that costs something on the caller's behalf, the session alone
// does not say they asked. A Markdown image in a post is fetched by every
// reader's browser with their session, as a GET, and Mattermost checks no CSRF
// token on a GET. In a browser an image cannot carry a header, and another
// site's script cannot add one to a request here unless Mattermost's CORS
// settings let that site in.
//
// That is a fact about browsers. A native client chooses its own headers, so
// this guards only reads that are cheap and stay on this server (the caller's
// own stream profile, the soundboard's own files). Whatever reaches a provider
// outside is a POST instead: see handleMediaSearch. And a native client keeps
// its half: it never sends this header on anything it loads as a picture or as
// video (contract/README.md, "What a client must do").
func madeByScript(r *http.Request) bool {
	return r.Header.Get("X-Requested-With") == "XMLHttpRequest"
}

// handleMediaImage answers /api/v1/media/image?ref=…&size=thumb|cover|backdrop
// with the picture itself, relayed.
//
// Relayed rather than handed to the browser as an address, which is the whole
// of why the browser never contacts any provider: the panel's <img> points
// here, carries the Mattermost session like any other request to this server,
// and gets bytes that were checked to be a picture on the way through.
func (p *Plugin) handleMediaImage(w http.ResponseWriter, r *http.Request, userID string) {
	query := r.URL.Query()

	ref, ok := parseMediaRef(strings.TrimSpace(query.Get("ref")))
	if !ok {
		writeError(w, http.StatusBadRequest, "That is not a title this server can look up.")
		return
	}

	size := strings.TrimSpace(query.Get("size"))
	if size != mediaSizeThumb && size != mediaSizeCover && size != mediaSizeBackdrop {
		writeError(w, http.StatusBadRequest, "Ask for a thumb, a cover or a backdrop.")
		return
	}

	// Switched off means switched off, even for a picture already in memory
	// or on the disk: the panel should look the same for everybody the moment
	// an admin saves.
	cfg := p.getConfiguration()
	if !cfg.mediaFlags().allowsRef(ref) {
		writeError(w, http.StatusNotFound, "Not found.")
		return
	}

	store := p.mediaStore()
	key := mediaPictureKey(ref, size)

	// Memory, then this server's disk, then the provider (media_disk.go).
	picture, held := store.pictures.get(key)
	if !held {
		picture, held = p.pictureFromDisk(key)
	}
	if !held {
		// Before the meter, so a refusal spends none of somebody's pictures.
		if !p.mayFetchPictureFor(userID, ref) {
			writeError(w, http.StatusNotFound, "Not found.")
			return
		}
		if !store.fetchLimiter.allow(userID) {
			writeTooMany(w, "Too many pictures at once. Wait a moment and try again.", store.fetchLimiter, userID, time.Now())
			return
		}

		// One fetch per picture however many panels ask at once — a new plan
		// appears on all of them together. On a context of its own, so the
		// panel that happened to ask first closing does not fail the others
		// waiting on the same fetch.
		shared, err := store.flights.do("picture/"+key, func() (any, error) {
			if held, again := store.pictures.get(key); again {
				return held, nil
			}

			return p.fetchPicture(cfg, ref, size, key)
		})
		if err != nil {
			// Not remembered: this is "could not find out", and the next
			// panel to ask should try again.
			p.API.LogWarn("A title's picture could not be fetched", "ref", ref.String(), "size", size, "error", err.Error())
			writeError(w, http.StatusBadGateway, "That picture could not be fetched just now.")

			return
		}

		loaded, _ := shared.(*mediaPicture)
		if loaded == nil {
			loaded = &mediaPicture{}
		}

		picture = loaded
	}

	if len(picture.data) == 0 {
		writeError(w, http.StatusNotFound, "Not found.")
		return
	}

	// A title's art does not change under its ref often enough to matter, and
	// it is only served to people who are signed in.
	writeImage(w, r, picture.contentType, picture.data, "private, max-age=86400")
}

// mayFetchPictureFor is whether a picture this server does not hold may be
// fetched for userID: for a title the panel can be showing anybody
// (refShownHere), or one it can be showing userID only because they have
// linked a stream (refShownToLinked). Anybody asking about any other title is
// answered without a provider being asked. Otherwise an account could walk
// TMDB's, Steam's and IGDB's ids through this server, spending the budget the
// providers share between everybody and filling the remembered titles and the
// pictures on the disk with whatever it liked.
//
// That goes for somebody linked too. It used to let them have any title at
// all, since somebody who can plan could search for any title anyway — but the
// person whose session comes with a GET is not always the person asking. A
// Markdown image in a post is fetched by every reader's browser with their
// session, and Mattermost checks no CSRF token on a GET, so anybody who could
// post — a locked member, a guest, somebody in Mumble through the chat bridge
// — could have every linked reader walk the ids for them. A search does make
// any title it lists shown here, and a search is a POST (handleMediaSearch),
// which an image in a post cannot make.
func (p *Plugin) mayFetchPictureFor(userID string, ref mediaRef) bool {
	if p.refShownHere(ref) {
		return true
	}

	// Linked as the gate means it, so exactly the people who can plan: a
	// forward entry left behind by a race or a failed cleanup belongs to
	// somebody locked or let in. A store that does not answer is "not linked"
	// here: the picture is missing for a moment, and nothing is fetched on a
	// guess. Asked last, so a title nobody here shows is refused without the
	// store being read at all.
	return p.refShownToLinked(userID, ref) && p.hasLink(userID)
}

// refShownToLinked is whether ref is a title the panel can be showing userID,
// as somebody linked, that refShownHere does not know of: a clip's tag, since
// clips are for linked people only, or the What's on? they have chosen and not
// yet aired, which the footer shows them before any card does. From memory,
// but for the clips, which are this node's copy and read again at most once a
// minute.
func (p *Plugin) refShownToLinked(userID string, ref mediaRef) bool {
	wanted := ref.String()

	p.nowPlayingLock.Lock()
	choice, chose := p.nowPlayingLast[userID]
	p.nowPlayingLock.Unlock()

	if chose && choice.Media != nil && sameMediaRef(choice.Media.Ref, wanted) {
		return true
	}

	doc, err := p.clipsDocument(time.Now())
	if err != nil || doc == nil {
		return false
	}

	for _, clip := range doc.Clips {
		if clip.Tag != nil && clip.Tag.Media != nil && sameMediaRef(clip.Tag.Media.Ref, wanted) {
			return true
		}
	}

	return false
}

// refShownHere is whether ref is a title the panel can be showing right now,
// to anybody: a plan in the schedule, what is on a live stream, or a title a
// search has just listed (its thumbnail in the planning form). From memory
// alone. A clip's tag is not here: clips are for linked people only, and
// refShownToLinked answers for those.
func (p *Plugin) refShownHere(ref mediaRef) bool {
	wanted := ref.String()

	if _, hinted := p.mediaStore().hints.get(wanted); hinted {
		return true
	}

	current := p.getSnapshot()
	for _, item := range current.Schedule {
		if item.Media != nil && sameMediaRef(item.Media.Ref, wanted) {
			return true
		}
	}
	for _, stream := range current.Streams {
		if stream.NowPlaying != nil && stream.NowPlaying.Media != nil && sameMediaRef(stream.NowPlaying.Media.Ref, wanted) {
			return true
		}
	}

	return false
}

// sameMediaRef compares a stored ref with a canonical one, through the parser
// when they are not spelled alike, so "tmdb:movie:0949" is still 949.
func sameMediaRef(stored, canonical string) bool {
	if stored == canonical {
		return true
	}

	ref, ok := parseMediaRef(stored)

	return ok && ref.String() == canonical
}
