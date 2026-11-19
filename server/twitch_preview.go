package main

import (
	"context"
	"crypto/hmac"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// A Twitch card's preview picture (design/twitch-live.md §5.7), fetched by the
// SERVER from Twitch's image host and relayed, so no browser ever fetches a
// Twitch picture whatever the embed switch says. It follows Live preview
// pictures: with that off, a Twitch card shows its streamer's own picture, as
// a Broadcast Box card does.
//
// Kept in memory only, for two minutes at most, at most a hundred of them,
// and dropped when the stream leaves the live list: well inside the 24 hours
// Twitch's agreement allows a cache.

// The host Twitch's preview templates must name. A variable only so tests can
// point it at a fake.
var twitchImageHost = "static-cdn.jtvnw.net"

const (
	// A picture is served from memory for this long, then fetched again.
	twitchPreviewHold = 2 * time.Minute

	maxTwitchPreviewBytes = 256 << 10
	maxTwitchPreviews     = 100

	twitchPreviewBudget = 8 * time.Second

	twitchPreviewPath        = "/previews-ttv/"
	twitchPreviewPlaceholder = "{width}x{height}"
	twitchPreviewSize        = "640x360"
)

var errTwitchNoPicture = errors.New("Twitch had no preview picture for that stream")

// twitchPicture is one relayed preview.
type twitchPicture struct {
	data []byte
	kind string // the sniffed type
	at   time.Time
}

// twitchThumbOK is whether a thumbnail_url from Get Streams is one VillaNET
// will fetch: https, on Twitch's image host, under its live previews, with
// the size placeholder exactly once. Anything else is never stored, so the
// server can never be pointed somewhere else by what Twitch, or anything
// pretending to be it, answers.
func twitchThumbOK(template string) bool {
	if template == "" || strings.Count(template, twitchPreviewPlaceholder) != 1 {
		return false
	}

	address, err := url.Parse(template)
	if err != nil || address.User != nil || address.RawQuery != "" || address.Fragment != "" {
		return false
	}

	return address.Scheme == "https" &&
		strings.EqualFold(address.Host, twitchImageHost) &&
		strings.HasPrefix(address.Path, twitchPreviewPath)
}

// heldPicture is the picture held for a stream, while it is fresh enough.
func (s *twitchStore) heldPicture(twitchID string, now time.Time) *twitchPicture {
	s.lock.Lock()
	defer s.lock.Unlock()

	picture := s.pictures[twitchID]
	if picture == nil || now.Before(picture.at) || now.Sub(picture.at) >= twitchPreviewHold {
		return nil
	}

	return picture
}

// keepPicture holds a picture for a stream that is still live, making room
// by dropping the oldest when there are already as many as are kept.
func (s *twitchStore) keepPicture(twitchID string, picture *twitchPicture) {
	s.lock.Lock()
	defer s.lock.Unlock()

	if _, live := s.live[twitchID]; !live {
		return
	}

	if _, held := s.pictures[twitchID]; !held && len(s.pictures) >= maxTwitchPreviews {
		oldest := ""
		for id, kept := range s.pictures {
			if oldest == "" || kept.at.Before(s.pictures[oldest].at) || (kept.at.Equal(s.pictures[oldest].at) && id < oldest) {
				oldest = id
			}
		}
		delete(s.pictures, oldest)
	}

	s.pictures[twitchID] = picture
}

// fetchTwitchPreview fetches one picture from a checked template. An offline
// channel is redirected to a placeholder elsewhere on the same host, which
// the shared client follows; landing anywhere but under the live previews is
// "no picture".
func fetchTwitchPreview(ctx context.Context, template string, now time.Time) (*twitchPicture, error) {
	if !twitchThumbOK(template) {
		return nil, errTwitchNoPicture
	}

	ctx, cancel := context.WithTimeout(ctx, twitchPreviewBudget)
	defer cancel()

	address := strings.Replace(template, twitchPreviewPlaceholder, twitchPreviewSize, 1)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, errTwitchNoPicture
	}
	request.Header.Set("Accept", "image/webp,image/jpeg,image/png")

	response, err := sendMedia(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK || response.Request == nil || response.Request.URL == nil ||
		!strings.HasPrefix(response.Request.URL.Path, twitchPreviewPath) {
		return nil, errTwitchNoPicture
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, maxTwitchPreviewBytes+1))
	if err != nil || len(data) == 0 || len(data) > maxTwitchPreviewBytes {
		return nil, errTwitchNoPicture
	}

	kind, ok := imageType(data)
	if !ok || (kind != "image/jpeg" && kind != "image/png" && kind != "image/webp") {
		return nil, errTwitchNoPicture
	}

	return &twitchPicture{data: data, kind: kind, at: now}, nil
}

// handleTwitchPreview answers GET /api/v1/twitch/preview?handle=&v=: the
// relayed picture of a Twitch stream in the snapshot, while Twitch live and
// Live preview pictures are both on.
func (p *Plugin) handleTwitchPreview(w http.ResponseWriter, r *http.Request, userID string) {
	notFound := func() {
		w.Header().Set("Cache-Control", "no-store")
		writeError(w, http.StatusNotFound, "There is no picture for that stream.")
	}

	cfg := p.getConfiguration()
	if !cfg.twitchLiveOn() || !cfg.LivePreviews {
		notFound()
		return
	}

	handle := r.URL.Query().Get("handle")
	if !validClipHandle(handle) {
		notFound()
		return
	}

	var stream *apiStream
	for _, listed := range p.getSnapshot().Streams {
		if listed.Source == sourceTwitch && hmac.Equal([]byte(listed.Handle), []byte(handle)) {
			stream = &listed
			break
		}
	}
	if stream == nil || stream.TwitchID == "" {
		notFound()
		return
	}

	s := p.twitchLive()
	now := s.now()

	live, known := s.liveEntry(stream.TwitchID, now)
	if !known || live.Thumb == "" {
		notFound()
		return
	}

	picture := s.heldPicture(stream.TwitchID, now)
	if picture == nil {
		// Counted only on a miss: a panel showing the same pictures again
		// costs nothing here.
		if !s.previewLimiter.allowAt(userID, now) {
			writeTooMany(w, "Too many pictures at once. Wait a moment and try again.", s.previewLimiter, userID, now)

			return
		}

		fetched, err := s.pictureFlights.do(stream.TwitchID, func() (any, error) {
			if held := s.heldPicture(stream.TwitchID, now); held != nil {
				return held, nil
			}

			fresh, err := fetchTwitchPreview(r.Context(), live.Thumb, now)
			if err != nil {
				return nil, err
			}
			s.keepPicture(stream.TwitchID, fresh)

			return fresh, nil
		})
		if err != nil {
			notFound()
			return
		}
		picture, _ = fetched.(*twitchPicture)
		if picture == nil {
			notFound()
			return
		}
	}

	w.Header().Set("Content-Type", picture.kind)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("v") == stream.PreviewVersion && stream.PreviewVersion != "" {
		w.Header().Set("Cache-Control", "private, max-age=300")
	} else {
		w.Header().Set("Cache-Control", "private, no-cache")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(picture.data)
}
