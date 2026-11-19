package main

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Downloading a clip (design/3.2.1.md §1). Victor asked for "a server toggle to
// allow/deny clip downloading": off until an admin switches it on, for linked
// people only, from the clip viewer only, and a streamer may turn it off for
// clips of their own stream whatever the admin says.
//
// A clip can be five minutes, so it is never fetched into the page: the
// browser saves it from the plugin's own address, through an <a download>.
// That address is a GET, and Mattermost asks no CSRF token of a GET carried by
// the session cookie (11.11.1, server/channels/app/plugin_requests.go), so a
// GET alone could be made by any page or a Markdown image. Two steps, then, as
// Connect Twitch does it:
//
//  1. POST download-ticket {id}, from the panel. A POST carries Mattermost's
//     CSRF check. Every rule is checked, and the answer is a ticket: random,
//     kept only as its hash, for one person and one clip, good for 60 seconds,
//     one at a time per person.
//  2. GET download?t=<ticket>. Every rule is checked again, then the ticket is
//     spent, the download is logged, and the browser is sent on to a download
//     address for that one download: download?d=<clip>.<until>.<mac>.
//  3. GET download?d=<address>. The address is signed with the plugin's own
//     secret for the person asking, so it needs no storage and works on any
//     node, and lasts ten minutes. Every rule is checked again, and the file
//     is served, with ranges.
//
// Why the third step, rather than serving the file on the second: the
// desktop app cancels a download as it starts and asks for the same address
// again after its own save dialog (Mattermost Desktop v6.3.0,
// src/main/downloadsManager.ts, handleNewDownload), and a browser resumes an
// interrupted download from the same address with a Range request. A ticket
// spent by the first request would refuse both. Both ask again for the last
// address of the redirect chain (Chromium's DownloadItemImpl::GetURL), which
// is the download address, never the spent ticket.
//
// Whatever refuses comes before anything is written: a refused request leaves
// the store as it found it, and the ticket is spent only by a request that is
// going to be served.

const (
	// One ticket per person, replaced by the next.
	kvClipTicketPrefix = "clip_ticket_"

	clipTicketBytes    = 32
	clipTicketSeconds  = 60
	clipAddressSeconds = 600

	// Ten tickets per person per ten minutes: far more Download presses than
	// anybody makes, and few enough five-minute files that nobody can keep
	// the disk busy for the rest.
	clipDownloadsPerWindow = 10
	clipDownloadWindow     = 10 * time.Minute

	// Keeps a download address's signature from ever being a handle's or a
	// stream tag's, which are taken under the same secret.
	clipAddressDomain = "villanet-clip-download\x00"

	// How much of the streamer's name and the title a file name keeps.
	maxDownloadStreamerRunes = 40
	maxDownloadTitleRunes    = 80
)

var (
	clipTicketPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	clipAddressPattern = regexp.MustCompile(`^([0-9a-f]{16})\.([0-9]{1,15})\.([0-9a-f]{32})$`)
)

// clipTicket is a download ticket as stored: its hash, never itself, and the
// clip it is for.
type clipTicket struct {
	Hash string `json:"h"`
	Clip string `json:"c"`
	At   int64  `json:"at"`
}

type clipTicketRequest struct {
	ID string `json:"id"`
}

type clipTicketResponse struct {
	URL string `json:"url"`
}

// clipDownloadRefusal is why somebody may not download a clip, or "" when they
// may: the admin's switch, then the reader's link, then the clip's streamer.
// Whether the clip is still kept is the caller's (it is no view at all
// otherwise), as is whether clips have somewhere to be kept. A clip whose
// stream nobody could name has an empty tag, which no switch can match.
func clipDownloadRefusal(on, linked bool, entry *clipEntry, doc *clipsDoc) clipErrorCode {
	switch {
	case !on:
		return clipErrDownloadsOff
	case !linked:
		return clipErrWatchOnly
	}

	if _, blocked := doc.NoDownloads[entry.StreamTag]; blocked && entry.StreamTag != "" {
		return clipErrStreamerDownloadsOff
	}

	return ""
}

// clipDownloadStatus is the status that goes with a refusal of a download.
func clipDownloadStatus(code clipErrorCode) int {
	switch code {
	case clipErrClipGone:
		return http.StatusGone
	case clipErrStorageUnavailable, clipErrStore:
		return http.StatusServiceUnavailable
	default:
		return http.StatusForbidden
	}
}

// downloadable is a clip that may be downloaded by somebody linked, now, and
// where its file is: the rules of §1.2 asked of this node's copy of the
// document, a refusal code, or the store's trouble.
func (p *Plugin) downloadable(clipID string, now time.Time) (*clipEntry, string, clipErrorCode, error) {
	if !p.getConfiguration().AllowClipDownloads {
		return nil, "", clipErrDownloadsOff, nil
	}

	doc, err := p.clipsDocument(now)
	if err != nil {
		return nil, "", "", err
	}

	nowMS := now.UnixMilli()
	kept := effectiveRetention(doc.Retention, p.getConfiguration().clipRetentionDays(), nowMS)
	entry := doc.liveClip(clipID, kept, nowMS)
	if entry == nil {
		return nil, "", clipErrClipGone, nil
	}

	if code := clipDownloadRefusal(true, true, entry, doc); code != "" {
		return nil, "", code, nil
	}

	path := p.clips().clipFile(clipID, clipVideoExt)
	if path == "" {
		return nil, "", clipErrStorageUnavailable, nil
	}

	return entry, path, "", nil
}

// POST /api/v1/clips/download-ticket {"id"}: a ticket for one download of
// one clip, for the person asking, good for a minute. The gate has found them
// linked. Metered only once the request is known to be good, so a refusal
// costs nobody a download, and refunded when the store fails.
func (p *Plugin) handleClipDownloadTicket(w http.ResponseWriter, r *http.Request, userID, _ string) {
	if !p.getConfiguration().AllowClipDownloads {
		p.writeClipError(w, http.StatusForbidden, clipErrDownloadsOff)
		return
	}

	var request clipTicketRequest
	if !readClipJSON(r, &request) || !validClipID(request.ID) {
		p.writeClipError(w, http.StatusBadRequest, clipErrBadRequest)
		return
	}

	s := p.clips()
	now := s.now()

	_, _, code, err := p.downloadable(request.ID, now)
	if err != nil {
		p.writeClipRefusal(w, err)
		return
	}
	if code != "" {
		p.writeClipError(w, clipDownloadStatus(code), code)
		return
	}

	if !s.downloadLimiter.allowAt(userID, now) {
		writeClipTooMany(w, s.downloadLimiter, userID, now)
		return
	}

	ticket, err := randomHex(clipTicketBytes)
	if err != nil {
		s.downloadLimiter.refund(userID)
		p.writeClipError(w, http.StatusServiceUnavailable, clipErrStore)

		return
	}

	record, err := json.Marshal(clipTicket{Hash: hashHex(ticket), Clip: request.ID, At: now.UnixMilli()})
	if err == nil {
		if appErr := p.API.KVSetWithExpiry(kvClipTicketPrefix+userID, record, clipTicketSeconds); appErr != nil {
			err = appErr
		}
	}
	if err != nil {
		p.API.LogWarn("Could not keep a clip download ticket", "error", err.Error())
		s.downloadLimiter.refund(userID)
		p.writeClipError(w, http.StatusServiceUnavailable, clipErrStore)

		return
	}

	writeJSON(w, http.StatusOK, clipTicketResponse{URL: "/plugins/" + manifestID + clipsPath + "/download?t=" + ticket})
}

// GET /api/v1/clips/download: ?t= spends a ticket and hands out a download
// address, and ?d= is that address, which serves the file. Either way the
// answer to anything stale is a plain page, since a browser may show it.
func (p *Plugin) handleClipDownload(w http.ResponseWriter, r *http.Request, userID, _ string) {
	query := r.URL.Query()
	ticket, address := query.Get("t"), query.Get("d")

	switch {
	case ticket != "" && address == "":
		p.spendClipTicket(w, userID, ticket)
	case address != "" && ticket == "":
		p.serveClipDownload(w, r, userID, address)
	default:
		writeDownloadPage(w, http.StatusGone, clipDownloadStale(p.brandName()))
	}
}

// spendClipTicket is the second step: the caller's own ticket, in time, for a
// clip they may still download, is spent, the download is logged, and the
// browser goes on to the download address. Nothing is written before every
// check has passed.
func (p *Plugin) spendClipTicket(w http.ResponseWriter, userID, ticket string) {
	if !clipTicketPattern.MatchString(ticket) {
		writeDownloadPage(w, http.StatusGone, clipDownloadStale(p.brandName()))
		return
	}

	key := kvClipTicketPrefix + userID
	raw, appErr := p.API.KVGet(key)
	if appErr != nil {
		writeDownloadPage(w, http.StatusServiceUnavailable, clipDownloadStore(p.brandName()))
		return
	}

	// Somebody else's ticket is not in this person's key, so it can never
	// match here, and whoever sends a wrong one cannot spend the real one:
	// it is left exactly where it is.
	var stored clipTicket
	if len(raw) == 0 || json.Unmarshal(raw, &stored) != nil ||
		subtle.ConstantTimeCompare([]byte(hashHex(ticket)), []byte(stored.Hash)) != 1 {
		writeDownloadPage(w, http.StatusGone, clipDownloadStale(p.brandName()))
		return
	}

	// The store's expiry is not trusted to the second; the ticket's own
	// time is. A clock a few seconds behind another node's is not a stale
	// ticket.
	now := p.clips().now()
	age := now.UnixMilli() - stored.At
	if age >= clipTicketSeconds*1000 || age < -5000 {
		writeDownloadPage(w, http.StatusGone, clipDownloadStale(p.brandName()))
		return
	}

	if !p.downloadRulesHold(w, stored.Clip, now) {
		return
	}

	// Spent: the first write, and only a request that is going to be served
	// reaches it.
	spent, appErr := p.API.KVCompareAndDelete(key, raw)
	if appErr != nil || !spent {
		writeDownloadPage(w, http.StatusGone, clipDownloadStale(p.brandName()))
		return
	}

	address, err := p.clipDownloadAddress(userID, stored.Clip, now.Add(clipAddressSeconds*time.Second).UnixMilli())
	if err != nil {
		writeDownloadPage(w, http.StatusServiceUnavailable, clipDownloadStore(p.brandName()))
		return
	}

	p.API.LogInfo("A clip was downloaded", "user_id", userID, "clip_id", stored.Clip)

	// Relative, so it is the path the browser asked for, whatever Mattermost
	// stripped from it before it reached the plugin (/plugins/<id>, and a
	// subpath). http.Redirect would make it absolute from the stripped path.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", "download?d="+address)
	w.WriteHeader(http.StatusSeeOther)
}

// serveClipDownload is the third step: the file, for the person the address
// was made for, while it lasts and while every rule still holds.
func (p *Plugin) serveClipDownload(w http.ResponseWriter, r *http.Request, userID, address string) {
	now := p.clips().now()

	clipID, ok := p.readClipDownloadAddress(userID, address, now)
	if !ok {
		writeDownloadPage(w, http.StatusGone, clipDownloadStale(p.brandName()))
		return
	}

	entry, path, refused := p.downloadRules(w, clipID, now)
	if refused {
		return
	}

	file, info, err := openClipFile(path)
	if err != nil {
		writeDownloadPage(w, http.StatusNotFound, "This clip's file is missing from the server.")
		return
	}
	defer file.Close()

	name := p.clipDownloadName(entry, userID, now)

	header := w.Header()
	header.Set("Content-Type", "video/mp4")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Disposition", attachmentDisposition(name))

	// Streamed from the disk, a range at a time when one is asked for: a
	// browser resuming an interrupted download asks for the rest.
	buffered := &bufferedResponse{ResponseWriter: w, buf: bufio.NewWriterSize(w, clipServeBuffer)}
	http.ServeContent(buffered, r, "", info.ModTime(), file)
	_ = buffered.buf.Flush()
}

// downloadRulesHold answers a page and says false when a clip may not be
// downloaded now.
func (p *Plugin) downloadRulesHold(w http.ResponseWriter, clipID string, now time.Time) bool {
	_, _, refused := p.downloadRules(w, clipID, now)

	return !refused
}

// downloadRules is downloadable for the GETs, which answer in pages.
func (p *Plugin) downloadRules(w http.ResponseWriter, clipID string, now time.Time) (*clipEntry, string, bool) {
	entry, path, code, err := p.downloadable(clipID, now)
	if err != nil {
		status, code := clipErrorFor(err)
		writeDownloadPage(w, status, clipErrorText(code, p.brandName()))

		return nil, "", true
	}
	if code != "" {
		writeDownloadPage(w, clipDownloadStatus(code), clipErrorText(code, p.brandName()))
		return nil, "", true
	}

	return entry, path, false
}

// clipDownloadAddress is a download address: the clip, when it lapses, and a
// signature of both for this person under the plugin's own secret.
func (p *Plugin) clipDownloadAddress(userID, clipID string, untilMS int64) (string, error) {
	until := strconv.FormatInt(untilMS, 10)
	signature, err := p.clipAddressSignature(userID, clipID, until)
	if err != nil {
		return "", err
	}

	return clipID + "." + until + "." + signature, nil
}

// readClipDownloadAddress is the clip a download address is for, when it was
// made for this person and has not lapsed.
func (p *Plugin) readClipDownloadAddress(userID, address string, now time.Time) (string, bool) {
	parts := clipAddressPattern.FindStringSubmatch(address)
	if parts == nil {
		return "", false
	}
	clipID, until, signature := parts[1], parts[2], parts[3]

	untilMS, err := strconv.ParseInt(until, 10, 64)
	if err != nil || now.UnixMilli() >= untilMS {
		return "", false
	}

	expected, err := p.clipAddressSignature(userID, clipID, until)
	if err != nil || !hmac.Equal([]byte(expected), []byte(signature)) {
		return "", false
	}

	return clipID, true
}

func (p *Plugin) clipAddressSignature(userID, clipID, until string) (string, error) {
	if len(p.handleSecret) == 0 || userID == "" {
		return "", fmt.Errorf("there is no secret to sign a download address with")
	}

	mac := hmac.New(sha256.New, p.handleSecret)
	mac.Write([]byte(clipAddressDomain + userID + "\x00" + clipID + "\x00" + until))

	return hex.EncodeToString(mac.Sum(nil))[:32], nil
}

// clipDownloadName is what a downloaded clip is called: "<Streamer> - <title
// or 'clip'> - <YYYY-MM-DD HHMM>.mp4", the time when the clip starts, in the
// reader's own Mattermost time zone, or UTC when theirs cannot be had.
func (p *Plugin) clipDownloadName(entry *clipEntry, userID string, now time.Time) string {
	streamer := clipStreamerOf(entry, p.clipNamesNow(now)).Name

	at := entry.StartAt
	if at <= 0 {
		at = entry.CreatedAt
	}

	zone := time.UTC
	if user, appErr := p.API.GetUser(userID); appErr == nil && user != nil {
		if name := user.GetPreferredTimezone(); name != "" {
			if location, err := time.LoadLocation(name); err == nil {
				zone = location
			}
		}
	}

	return downloadFileName(streamer, p.brandName(), entry.Title, time.UnixMilli(at).In(zone))
}

// downloadFileName puts a file name together from its parts, each cleaned. A
// clip of a streamer with no name is named after the server's name for the
// plugin (server), or productName when that cleans away to nothing.
func downloadFileName(streamer, server, title string, at time.Time) string {
	streamer = cleanDownloadPart(streamer, maxDownloadStreamerRunes)
	if streamer == "" {
		streamer = cleanDownloadPart(server, maxDownloadStreamerRunes)
	}
	if streamer == "" {
		streamer = productName
	}

	title = cleanDownloadPart(title, maxDownloadTitleRunes)
	if title == "" {
		title = "clip"
	}

	return streamer + " - " + title + " - " + at.Format("2006-01-02 1504") + ".mp4"
}

// cleanDownloadPart makes one part of a file name safe on every system a
// browser saves to: no control or formatting characters (a right-to-left
// override could make "clip.mp4" read as something else), none of the
// characters Windows refuses, one space at a time, no spaces or dots at
// either end, and no longer than max characters.
func cleanDownloadPart(value string, max int) string {
	var cleaned strings.Builder
	space := false

	for _, r := range value {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.IsSpace(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
			space = true
			continue
		}

		if space && cleaned.Len() > 0 {
			cleaned.WriteByte(' ')
		}
		space = false
		cleaned.WriteRune(r)
	}

	result := []rune(cleaned.String())
	if len(result) > max {
		result = result[:max]
	}

	return strings.Trim(string(result), " .")
}

// attachmentDisposition is the Content-Disposition of a download: a plain
// ASCII name for old browsers and the real one, encoded, for every other
// (RFC 6266 and RFC 8187).
func attachmentDisposition(name string) string {
	var plain, encoded strings.Builder

	for _, r := range name {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' {
			plain.WriteByte('_')
		} else {
			plain.WriteRune(r)
		}
	}

	for _, b := range []byte(name) {
		if b < 0x80 && (b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.IndexByte("!#$&+-.^_`|~", b) >= 0) {
			encoded.WriteByte(b)
		} else {
			fmt.Fprintf(&encoded, "%%%02X", b)
		}
	}

	return `attachment; filename="` + plain.String() + `"; filename*=UTF-8''` + encoded.String()
}

// clipDownloadStale is the page for a download link that is spent or made
// up, with the server's name for the plugin (brandName).
func clipDownloadStale(name string) string {
	return "This download link has run out or was already used. Press Download in " + name + " again."
}

// clipDownloadStore is the page for a download the store could not start.
func clipDownloadStore(name string) string {
	return "The download could not be started just now. Press Download in " + name + " again."
}

// writeDownloadPage answers a download with a short page of plain words, which
// a browser may show in place of the file, and nothing it could run.
func writeDownloadPage(w http.ResponseWriter, status int, text string) {
	header := w.Header()
	header.Set("Content-Type", "text/plain; charset=utf-8")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	header.Set("Cache-Control", "no-store")
	header.Del("Content-Disposition")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, text+"\n")
}

// setClipDownloads is the streamer's second switch (design/3.2.1.md §1.3),
// for the stream they have linked, by its tag: whether people may download
// clips of it. Kept beside clipping's switch, and on the same terms: by
// stream, so it stays with the stream across an unlink, and it reaches every
// clip of the stream, past and future. It can be set whatever the admin's
// switch says; the panel only shows it while downloads are allowed. Turning
// it off deletes nothing.
func (p *Plugin) setClipDownloads(w http.ResponseWriter, userID, tag string, allow bool, now time.Time) {
	s := p.clips()

	switched := false
	_, err := p.clipsCAS(now, func(doc *clipsDoc, write *clipsWrite) (bool, error) {
		switched = false

		_, off := doc.NoDownloads[tag]
		switch {
		case allow && off:
			delete(doc.NoDownloads, tag)
			if len(doc.NoDownloads) == 0 {
				doc.NoDownloads = nil
			}
			switched = true
		case !allow && !off:
			if doc.NoDownloads == nil {
				doc.NoDownloads = map[string]clipMark{}
			}
			doc.NoDownloads[tag] = clipMark{By: userID, At: write.now}
			switched = true
		}

		return switched, nil
	})
	if err != nil {
		refundClipToken(s.writeLimiter, userID, err)
		p.writeClipRefusal(w, err)

		return
	}

	switch {
	case switched && allow:
		p.API.LogInfo("A streamer turned downloads of their clips back on", "user_id", userID)
	case switched:
		p.API.LogInfo("A streamer turned downloads of their clips off", "user_id", userID)
	}

	// A clip open in somebody's viewer asks about itself again when the
	// clips move, so its Download follows the switch.
	if switched {
		p.republishClips()
	}

	writeJSON(w, http.StatusOK, clipDownloadsResponse{Downloads: allow})
}
