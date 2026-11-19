package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// bbVideoTrack is one inbound video track reported by GET /api/status.
type bbVideoTrack struct {
	RID             string `json:"rid"`
	Bitrate         int64  `json:"bitrate"`
	PacketsReceived int64  `json:"packetsReceived"`
	PacketsDropped  int64  `json:"packetsDropped"`
	LastKeyframe    string `json:"lastKeyframe"`
}

// bbAudioTrack is one inbound audio track reported by GET /api/status.
type bbAudioTrack struct {
	RID             string `json:"rid"`
	PacketsReceived int64  `json:"packetsReceived"`
	PacketsDropped  int64  `json:"packetsDropped"`
}

// bbSession is one WHEP viewer currently pulling the stream. Counting these
// gives the audience size.
type bbSession struct {
	ID           string `json:"id"`
	VideoBitrate int64  `json:"videoBitrate"`
}

// bbStream is one entry of GET /api/status.
//
// The optional flags are pointers so that a Broadcast Box build which does not
// report them is distinguishable from one that reports them as false.
type bbStream struct {
	StreamKey string `json:"streamKey"`
	// IsPublic is always true on anything /api/status returns: Broadcast Box
	// filters unlisted streams out of the listing before it answers.
	IsPublic    *bool          `json:"isPublic"`
	MOTD        string         `json:"motd"`
	StreamStart string         `json:"streamStart"`
	AudioTracks []bbAudioTrack `json:"audioTracks"`
	VideoTracks []bbVideoTrack `json:"videoTracks"`
	Sessions    []bbSession    `json:"sessions"`
	Paused      *bool          `json:"paused"`
}

// live mirrors how the Broadcast Box web UI itself decides a stream is
// watchable: at least one video track is arriving.
func (s bbStream) live() bool {
	return len(s.VideoTracks) > 0
}

func (s bbStream) isPaused() bool {
	return s.Paused != nil && *s.Paused
}

// bitrate is the combined inbound video bitrate, in bits per second.
func (s bbStream) bitrate() int64 {
	var total int64
	for _, track := range s.VideoTracks {
		total += track.Bitrate
	}
	return total
}

// errStatusDisabled is returned when Broadcast Box has its status API turned
// off, which it signals with HTTP 503.
var errStatusDisabled = fmt.Errorf("the Broadcast Box status API is disabled on the server")

// fetchStatus calls GET /api/status, adding HTTP Basic Auth when configured.
func (p *Plugin) fetchStatus(ctx context.Context, cfg *configuration) ([]bbStream, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.BroadcastBoxURL+"/api/status", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if cfg.hasBasicAuth() {
		req.SetBasicAuth(cfg.BasicAuthUsername, cfg.BasicAuthPassword)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach Broadcast Box: %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusServiceUnavailable:
		return nil, errStatusDisabled
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, fmt.Errorf("Broadcast Box rejected the credentials (HTTP 401) - check the Basic Auth username and password")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("Broadcast Box returned HTTP %d for /api/status", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxStatusBytes))
	if err != nil {
		return nil, err
	}

	var streams []bbStream
	if err := json.Unmarshal(body, &streams); err != nil {
		return nil, fmt.Errorf("could not parse the Broadcast Box status response: %w", err)
	}

	return streams, nil
}

// candidateLines pulls the ICE candidates out of an SDP. These decide where
// media actually flows, so they are the first thing to look at when a viewer
// cannot play, and unlike the rest of the SDP they carry no session secrets.
// keyPrefix identifies a stream in a log line without writing the whole key,
// which on this Broadcast Box build is close enough to a credential.
func keyPrefix(streamKey string) string {
	if len(streamKey) <= 4 {
		return "…"
	}

	return streamKey[:4] + "…"
}

func candidateLines(sdp string) []string {
	var candidates []string

	for _, line := range strings.Split(sdp, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "a=candidate:") {
			candidates = append(candidates, line)
		}
	}

	return candidates
}

// exchangeWHEP forwards a WHEP SDP offer to Broadcast Box and returns the SDP
// answer, and Broadcast Box's id for the session it opened. The stream key
// travels as a bearer token, which is what Broadcast Box expects.
//
// Note that Basic Auth is deliberately NOT applied here: WHEP already uses the
// Authorization header for the stream key, so the two cannot coexist. Broadcast
// Box's /api/whep must be reachable without Basic Auth.
//
// The session id is for the plugin alone and never goes to the browser. It is
// how a peek is told apart from a viewer in /api/status (see stills.go) and how
// a viewer's face is put on the card (see watchers.go), and it is also, on
// Broadcast Box, the whole of the authority to PATCH that session's ICE —
// /api/whep/<id> asks for nothing else. The browser never talks to Broadcast
// Box and has no use for it.
func (p *Plugin) exchangeWHEP(ctx context.Context, cfg *configuration, streamKey, offer string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BroadcastBoxURL+"/api/whep", strings.NewReader(offer))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/sdp")
	req.Header.Set("Authorization", "Bearer "+streamKey)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("could not reach Broadcast Box: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSDPBytes))
	if err != nil {
		return "", "", err
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		// Without the key, before anything else sees it, and before it is cut
		// short, so a key across the cut cannot leave its first half behind.
		// A refusal from Broadcast Box can name the stream it refused, and
		// this text is logged by everybody who calls this: the panel's
		// playback relay as well as the clips recorder, which used to be the
		// only one that took the key out.
		detail := strings.TrimSpace(redactStreamKey(string(body), streamKey, "the stream"))
		if len(detail) > 200 {
			detail = detail[:200]
		}
		if detail == "" {
			detail = "no detail"
		}
		return "", "", fmt.Errorf("Broadcast Box refused the playback request (HTTP %d): %s", resp.StatusCode, detail)
	}

	// Before anything else sees it: the answer names its media stream after
	// the stream key.
	answer := withoutStreamIDs(string(body))

	// The origin's DEBUG_PRINT_ANSWER is off, so this is the only place the
	// negotiation can be inspected.
	candidates := candidateLines(answer)

	if cfg.DebugLogSDP {
		// The prefix, never the key: this setting is switched on to debug
		// playback, which is exactly when logs get pasted into a ticket.
		p.API.LogInfo("WHEP exchange",
			"stream", keyPrefix(streamKey),
			"offer_sdp", offer,
			"answer_sdp", answer,
			"answer_candidates", strings.Join(candidates, " | "))
	}

	// An answer with no candidates means the origin advertised no address to
	// send media to, so playback will fail silently however good the SDP looks.
	if len(candidates) == 0 {
		p.API.LogWarn("Broadcast Box returned an answer with no ICE candidates; check NAT_1_TO_1_IP and the UDP mux port",
			"stream", keyPrefix(streamKey))
	}

	return answer, whepSessionID(resp.Header.Get("Location")), nil
}

// answerStreamID is the media stream id every WHEP answer is given in place
// of Broadcast Box's own.
const answerStreamID = "stream"

// withoutStreamIDs takes Broadcast Box's media stream id out of a WHEP answer.
//
// Broadcast Box v2.0.2 names the tracks of every playback session after the
// stream key (codecs.GetDefaultTracks(streamKey) gives it as their stream id),
// and pion writes a track's stream id into the answer as the msid and as the
// ssrc's cname, msid and mslabel. Left there, every answer would carry the key
// in plain text: to every viewer's browser through handleWHEP, which is
// exactly what the handles exist to stop, and into the log with "Log WHEP
// negotiation" on. Nothing needs the real value. A browser only groups tracks
// by it, and a recorder never reads it, so one neutral id for every track
// keeps the picture and the sound together and in sync.
//
// Only the values of those attributes change, line by line, so nothing else
// in the answer can be touched; "-", which means no stream at all, is left
// as it is. The line endings are kept as they were.
func withoutStreamIDs(sdp string) string {
	lines := strings.SplitAfter(sdp, "\n")
	for i, line := range lines {
		body := strings.TrimRight(line, "\r\n")
		if rewritten, changed := neutralStreamID(body); changed {
			lines[i] = rewritten + line[len(body):]
		}
	}

	return strings.Join(lines, "")
}

// neutralStreamID rewrites one SDP line that carries a stream id:
//
//	a=msid:<stream id> <track id>
//	a=ssrc:<ssrc> cname:<stream id>
//	a=ssrc:<ssrc> msid:<stream id> <track id>
//	a=ssrc:<ssrc> mslabel:<stream id>
func neutralStreamID(line string) (string, bool) {
	// replaceFirst puts answerStreamID in place of the first field of value,
	// keeping whatever follows it.
	replaceFirst := func(prefix, value string) (string, bool) {
		id, rest, _ := strings.Cut(value, " ")
		if id == "" || id == "-" || id == answerStreamID {
			return line, false
		}
		if rest != "" {
			rest = " " + rest
		}

		return prefix + answerStreamID + rest, true
	}

	if value, ok := strings.CutPrefix(line, "a=msid:"); ok {
		return replaceFirst("a=msid:", value)
	}

	value, ok := strings.CutPrefix(line, "a=ssrc:")
	if !ok {
		return line, false
	}
	ssrc, attribute, found := strings.Cut(value, " ")
	if !found {
		return line, false
	}
	for _, name := range []string{"cname:", "msid:", "mslabel:"} {
		if rest, ok := strings.CutPrefix(attribute, name); ok {
			return replaceFirst("a=ssrc:"+ssrc+" "+name, rest)
		}
	}

	return line, false
}

// whepSessionID reads the session id out of the Location Broadcast Box answers
// a WHEP offer with, "/api/whep/<uuid>". The same uuid is the session's "id" in
// /api/status. Anything that does not look like one is no id at all, rather
// than something stored and compared against: an origin behind a proxy that
// rewrites the header should cost a peek its place outside the viewer count,
// not put arbitrary text into the plugin's memory.
func whepSessionID(location string) string {
	if location == "" {
		return ""
	}

	parsed, err := url.Parse(location)
	if err != nil {
		return ""
	}

	id := path.Base(parsed.Path)
	if len(id) < 8 || len(id) > 64 {
		return ""
	}

	for _, r := range id {
		if !strings.ContainsRune("0123456789abcdefABCDEF-", r) {
			return ""
		}
	}

	return id
}

// bbProfile is a reserved stream's profile, as returned by GET /api/whip/profile.
type bbProfile struct {
	MOTD      string `json:"motd"`
	IsPublic  bool   `json:"isPublic"`
	IsActive  bool   `json:"isActive"`
	StreamKey string `json:"streamKey"`
}

// profileRequest is the body POST /api/whip/profile expects.
type profileRequest struct {
	MOTD     string `json:"motd"`
	IsPublic bool   `json:"isPublic"`
}

// The profile endpoints authenticate with the WHIP publish token, which is
// base64 of the stream key. Like WHEP, they use the Authorization header for
// that, so HTTP Basic Auth cannot be layered on top: reach Broadcast Box
// directly rather than through a proxy that adds it.
func (p *Plugin) profileRequest(ctx context.Context, cfg *configuration, method, token string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, cfg.BroadcastBoxURL+"/api/whip/profile", body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+publishToken(token))

	return req, nil
}

// fetchProfile returns nil without error when Broadcast Box has no reserved
// profile for the key, which it signals with HTTP 204.
// fetchProfileByToken resolves a publish token to the stream it belongs to.
// Broadcast Box matches only the token half of a profile's "<key>_<token>"
// filename, so a successful lookup proves the caller holds that stream's secret.
func (p *Plugin) fetchProfileByToken(ctx context.Context, cfg *configuration, token string) (*bbProfile, error) {
	req, err := p.profileRequest(ctx, cfg, http.MethodGet, token, nil)
	if err != nil {
		return nil, err
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach Broadcast Box: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Broadcast Box returned HTTP %d for the stream profile", resp.StatusCode)
	}

	var profile bbProfile
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxProfileBytes)).Decode(&profile); err != nil {
		return nil, fmt.Errorf("could not parse the stream profile: %w", err)
	}

	return &profile, nil
}

func (p *Plugin) saveProfile(ctx context.Context, cfg *configuration, token, motd string, isPublic bool) error {
	encoded, err := json.Marshal(profileRequest{MOTD: motd, IsPublic: isPublic})
	if err != nil {
		return err
	}

	req, err := p.profileRequest(ctx, cfg, http.MethodPost, token, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach Broadcast Box: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusNoContent {
			return fmt.Errorf("Broadcast Box has no reserved profile for that stream key")
		}
		return fmt.Errorf("Broadcast Box rejected the change (HTTP %d)", resp.StatusCode)
	}

	return nil
}
