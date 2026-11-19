package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Downloading a clip (design/3.2.1.md §1): off until an admin switches it on,
// for linked people only, never past a streamer who turned it off, through a
// ticket spent once and a download address that is the person's own.

// downloadScene is the clips scene with two clips, both kept: someClipID, cut
// from a stream nobody here is linked to, and otherClipID, cut from Dave's
// stream (uid-linked's), with a title a file name cannot carry as it is.
// Downloads are on, and the clock stands still until a test moves it.
type downloadScene struct {
	*clipsScene
	now time.Time
}

func newDownloadScene(t *testing.T) *downloadScene {
	t.Helper()

	s := newClipsScene(t)
	s.start(t)

	// Tickets are kept with an expiry, which this store keeps like any
	// other write; their own time is what the plugin goes by.
	s.api.On("KVSetWithExpiry", mock.Anything, mock.Anything, mock.Anything).Return(func(key string, value []byte, _ int64) *model.AppError {
		s.faults.lock.Lock()
		defer s.faults.lock.Unlock()

		s.faults.wrote(key)
		if s.faults.writeFails[key] {
			return model.NewAppError("KVSetWithExpiry", "kv.write", nil, "injected failure", http.StatusInternalServerError)
		}
		s.store[key] = value
		s.faults.writes[key]++

		return nil
	})

	daves := testClip(otherClipID, "uid-admin-linked", time.Now().Add(-time.Hour), nil)
	daves.StreamerID = "uid-linked"
	daves.StreamName = "Dave"
	daves.StreamTag = s.p.streamTag("videokey-dave")
	daves.Title = `Bob's "best" jump / part 2`
	daves.StartAt = time.Date(2026, 10, 13, 21, 5, 30, 0, time.UTC).UnixMilli()
	publishFixtureClip(t, s.p, daves, false)

	scene := &downloadScene{clipsScene: s, now: time.Now()}
	s.p.clips().now = func() time.Time { return scene.now }
	scene.downloads(true)

	return scene
}

// downloads is the admin's switch.
func (s *downloadScene) downloads(on bool) {
	cfg := s.p.getConfiguration().Clone()
	cfg.AllowClipDownloads = on
	s.p.setConfiguration(cfg)
}

// ticket asks for a download ticket as caller, and answers the response.
func (s *downloadScene) ticket(t *testing.T, caller, clipID string) *httptest.ResponseRecorder {
	t.Helper()

	response, _ := s.call(t, caller, clipCall{method: http.MethodPost, target: clipsPath + "/download-ticket", body: `{"id":"` + clipID + `"}`})

	return response
}

// ticketURL asks for a ticket that must be given, and answers the address the
// panel is told to open, relative to the plugin.
func (s *downloadScene) ticketURL(t *testing.T, caller, clipID string) string {
	t.Helper()

	response := s.ticket(t, caller, clipID)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var answer clipTicketResponse
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &answer))

	prefix := "/plugins/" + manifestID
	require.True(t, strings.HasPrefix(answer.URL, prefix+clipsPath+"/download?t="), answer.URL)

	return strings.TrimPrefix(answer.URL, prefix)
}

// get is a GET as the browser makes it, with whatever headers.
func (s *downloadScene) get(t *testing.T, caller, target string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	response, _ := s.call(t, caller, clipCall{method: http.MethodGet, target: target, header: header})

	return response
}

// download spends a fresh ticket and answers where it sends the browser,
// relative to the plugin, as the browser resolves the answer's Location.
func (s *downloadScene) download(t *testing.T, caller, clipID string) string {
	t.Helper()

	spent := s.get(t, caller, s.ticketURL(t, caller, clipID), nil)
	require.Equal(t, http.StatusSeeOther, spent.Code, spent.Body.String())
	require.Equal(t, "no-store", spent.Header().Get("Cache-Control"))

	location := spent.Header().Get("Location")
	require.True(t, strings.HasPrefix(location, "download?d="), "relative to the address the browser asked for: %s", location)

	return clipsPath + "/" + location
}

// canDownload is what the list says of a clip, for caller.
func (s *downloadScene) canDownload(t *testing.T, caller, clipID string) bool {
	t.Helper()

	response, _ := s.call(t, caller, clipCall{method: http.MethodGet, target: clipsPath})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	var list struct {
		Clips []clipView `json:"clips"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &list))
	for _, clip := range list.Clips {
		if clip.ID == clipID {
			single, _ := s.call(t, caller, clipCall{method: http.MethodGet, target: clipsPath + "/clip?id=" + clipID})
			var one clipView
			require.NoError(t, json.Unmarshal(single.Body.Bytes(), &one))
			require.Equal(t, clip.CanDownload, one.CanDownload, "the list and the clip agree")

			return clip.CanDownload
		}
	}
	require.Fail(t, "no such clip in the list", clipID)

	return false
}

// requireDownloadPage checks a refusal a browser may show: plain words, a status,
// and nothing anybody may keep or run.
func requireDownloadPage(t *testing.T, response *httptest.ResponseRecorder, status int, words string) {
	t.Helper()

	require.Equal(t, status, response.Code, response.Body.String())
	require.Equal(t, "text/plain; charset=utf-8", response.Header().Get("Content-Type"))
	require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "no-store", response.Header().Get("Cache-Control"))
	require.Empty(t, response.Header().Get("Content-Disposition"))
	require.Empty(t, response.Header().Get("Location"))
	require.Equal(t, words+"\n", response.Body.String())
}

func TestDownloadsAreOffUntilAnAdminSwitchesThemOn(t *testing.T) {
	s := newDownloadScene(t)
	s.downloads(false)

	require.False(t, s.canDownload(t, "uid-linked", someClipID))
	requireClipError(t, s.ticket(t, "uid-linked", someClipID), http.StatusForbidden, clipErrDownloadsOff, "no ticket while downloads are off")
	require.Empty(t, s.store[kvClipTicketPrefix+"uid-linked"], "no ticket")

	s.downloads(true)
	require.True(t, s.canDownload(t, "uid-linked", someClipID))
	address := s.download(t, "uid-linked", someClipID)

	// Switched off again: a ticket in hand and an address in hand are
	// both refused, with the switch's own words.
	url := s.ticketURL(t, "uid-linked", someClipID)
	s.downloads(false)
	requireDownloadPage(t, s.get(t, "uid-linked", url, nil), http.StatusForbidden, "Downloads are off.")
	requireDownloadPage(t, s.get(t, "uid-linked", address, nil), http.StatusForbidden, "Downloads are off.")
	require.NotEmpty(t, s.store[kvClipTicketPrefix+"uid-linked"], "and the refused ticket is not spent")
}

func TestALetInReaderCanWatchButNotDownload(t *testing.T) {
	s := newDownloadScene(t)

	require.False(t, s.canDownload(t, "uid-guest", someClipID), "let in: watching only")
	require.False(t, s.canDownload(t, "uid-admin-guest", someClipID), "an admin let in is no different")
	requireClipError(t, s.ticket(t, "uid-guest", someClipID), http.StatusForbidden, clipErrWatchOnly, "no ticket for somebody let in")

	// Nor with an address somebody linked was given.
	address := s.download(t, "uid-linked", someClipID)
	requireClipError(t, s.get(t, "uid-guest", address, nil), http.StatusForbidden, clipErrWatchOnly, "nor the file")
	requireClipError(t, s.get(t, "uid-unlinked", address, nil), http.StatusForbidden, clipErrLocked, "nor for somebody locked")

	// The System Console's view is no download either.
	console, err := s.p.adminClipsView("uid-admin", s.now)
	require.NoError(t, err)
	for _, clip := range console.Clips {
		require.False(t, clip.CanDownload)
	}
}

func TestAClipNobodyMayWatchCannotBeDownloaded(t *testing.T) {
	s := newDownloadScene(t)

	requireClipError(t, s.ticket(t, "uid-linked", "0123456789abcdef"), http.StatusGone, clipErrClipGone, "a clip that is not kept")
	requireClipError(t, s.ticket(t, "uid-linked", "not-a-clip"), http.StatusBadRequest, clipErrBadRequest, "a clip id that is not one")
	requireClipError(t, s.ticket(t, "uid-unlinked", someClipID), http.StatusForbidden, clipErrLocked, "somebody locked")

	// Kept for 30 days: a day past that, the ticket in hand is no good.
	url := s.ticketURL(t, "uid-linked", someClipID)
	address := s.download(t, "uid-linked", someClipID)
	s.now = s.now.Add(31 * 24 * time.Hour)
	requireDownloadPage(t, s.get(t, "uid-linked", url, nil), http.StatusGone, clipDownloadStale(productName))
	requireDownloadPage(t, s.get(t, "uid-linked", address, nil), http.StatusGone, clipDownloadStale(productName))
}

// A clip deleted while its address is still good is not served either.
func TestADeletedClipIsNotServedFromAnAddressStillGood(t *testing.T) {
	s := newDownloadScene(t)

	address := s.download(t, "uid-linked", otherClipID)
	require.NoError(t, s.p.removeClip(otherClipID, "uid-admin", clipGoneAdmin, s.now))

	requireDownloadPage(t, s.get(t, "uid-linked", address, nil), http.StatusGone, "This clip has gone.")
}

func TestAStreamersBlockReachesEveryClipOfTheirStream(t *testing.T) {
	s := newDownloadScene(t)
	before := s.download(t, "uid-admin-linked", otherClipID)

	// Dave, linked to his stream, turns downloads of it off.
	response, _ := s.call(t, "uid-linked", clipCall{method: http.MethodPut, target: clipsPath + "/streamer", body: `{"downloads":false}`})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.JSONEq(t, `{"downloads":false}`, response.Body.String())
	stored, err := s.p.clipsDocument(s.now)
	require.NoError(t, err)
	require.Contains(t, stored.NoDownloads, s.p.streamTag("videokey-dave"), "kept by stream, beside clipping's switch")
	require.Empty(t, stored.OptOut, "which it leaves alone")

	// The clip made before it, for everybody, Dave included.
	for _, caller := range []string{"uid-admin-linked", "uid-linked"} {
		require.False(t, s.canDownload(t, caller, otherClipID), caller)
		requireClipError(t, s.ticket(t, caller, otherClipID), http.StatusForbidden, clipErrStreamerDownloadsOff, "blocked by its streamer")
	}
	requireDownloadPage(t, s.get(t, "uid-admin-linked", before, nil), http.StatusForbidden, "The streamer has turned off downloads of their clips.")

	// Another stream's clip is not his to block.
	require.True(t, s.canDownload(t, "uid-admin-linked", someClipID))

	// A clip of his stream made afterwards is blocked too.
	later := testClip("00000000000000aa", "uid-admin-linked", s.now.Add(-time.Minute), nil)
	later.StreamTag = s.p.streamTag("videokey-dave")
	publishFixtureClip(t, s.p, later, false)
	require.False(t, s.canDownload(t, "uid-admin-linked", later.ID))

	// It stays with the stream when he unlinks, and goes with his switch.
	s.faults.lock.Lock()
	delete(s.store, kvLinkByUserPrefix+"uid-linked")
	delete(s.store, kvLinkByKeyPrefix+"videokey-dave")
	s.faults.lock.Unlock()
	s.p.accessForget("uid-linked")
	require.False(t, s.canDownload(t, "uid-admin-linked", otherClipID), "an unlink changes nothing")
	s.faults.lock.Lock()
	linkForTest(s.store, "uid-linked", "videokey-dave")
	s.faults.lock.Unlock()
	s.p.accessForget("uid-linked")

	response, _ = s.call(t, "uid-linked", clipCall{method: http.MethodPut, target: clipsPath + "/streamer", body: `{"downloads":true}`})
	require.Equal(t, http.StatusOK, response.Code)
	require.True(t, s.canDownload(t, "uid-admin-linked", otherClipID))
	require.True(t, s.canDownload(t, "uid-admin-linked", later.ID))
}

// The switch is the streamer's own, on their own stream only, and a request
// that says both switches, or neither, is not one the panel sends.
func TestTheStreamersDownloadSwitchIsTheirsAlone(t *testing.T) {
	s := newDownloadScene(t)

	for _, body := range []string{`{}`, `{"allow":true,"downloads":false}`, `{"downloads":"no"}`} {
		response, _ := s.call(t, "uid-linked", clipCall{method: http.MethodPut, target: clipsPath + "/streamer", body: body})
		requireClipError(t, response, http.StatusBadRequest, clipErrBadRequest, "one switch at a time")
	}

	// Bob blocks his own stream, which no clip here is of.
	response, _ := s.call(t, "uid-admin-linked", clipCall{method: http.MethodPut, target: clipsPath + "/streamer", body: `{"downloads":false}`})
	require.Equal(t, http.StatusOK, response.Code)
	require.True(t, s.canDownload(t, "uid-linked", otherClipID), "Dave's clip is not Bob's to block")

	response, _ = s.call(t, "uid-guest", clipCall{method: http.MethodPut, target: clipsPath + "/streamer", body: `{"downloads":false}`})
	requireClipError(t, response, http.StatusForbidden, clipErrWatchOnly, "the switch is for streamers")
}

func TestTheStreamersDownloadSwitchIsOnMeOnlyWhileAllowed(t *testing.T) {
	s := newDownloadScene(t)

	view := s.p.clipsStreamerFor("videokey-dave")
	require.NotNil(t, view.Downloads)
	require.True(t, *view.Downloads, "allowed until he says otherwise")

	response, _ := s.call(t, "uid-linked", clipCall{method: http.MethodPut, target: clipsPath + "/streamer", body: `{"downloads":false}`})
	require.Equal(t, http.StatusOK, response.Code)
	view = s.p.clipsStreamerFor("videokey-dave")
	require.False(t, *view.Downloads)
	require.True(t, view.Allowed, "clipping is another switch")

	s.downloads(false)
	view = s.p.clipsStreamerFor("videokey-dave")
	require.Nil(t, view.Downloads, "no switch while the admin allows no downloads")
	encoded, err := json.Marshal(view)
	require.NoError(t, err)
	require.JSONEq(t, `{"allowed":true}`, string(encoded), "/me is 3.2.0's")
}

// Clips switched off, or with nowhere to keep anything (a directory that had
// stopped taking writes when the plugin started), still serve the clips
// already made, and downloads of them go on while the admin allows them. So
// the streamer's block has to reach them then as well: /me still says where
// the switch is, which is all the panel shows it by while the snapshot says
// Clips are off, and the switch still works (design/3.2.1.md 1.3).
func TestAStreamerCanBlockDownloadsWhileClipsAreOffOrCannotBeKept(t *testing.T) {
	for _, why := range []string{"clipping switched off", "nowhere to keep clips"} {
		t.Run(why, func(t *testing.T) {
			s := newDownloadScene(t)
			if why == "clipping switched off" {
				cfg := s.p.getConfiguration().Clone()
				cfg.EnableClips = false
				s.p.setConfiguration(cfg)
			} else {
				root, _ := s.p.clips().storage()
				s.p.clips().setStorage(root, "Clips cannot write to "+root+".")
			}

			current := &snapshot{Configured: true}
			s.p.addClips(current, s.p.getConfiguration(), s.now)
			require.False(t, current.Clips.Enabled, "the panel is told Clips are off")

			view := s.p.clipsStreamerFor("videokey-dave")
			require.NotNil(t, view)
			require.NotNil(t, view.Downloads, "and /me still says where the downloads switch is")
			require.True(t, *view.Downloads)
			require.True(t, s.canDownload(t, "uid-admin-linked", otherClipID), "a clip already made can still be downloaded")
			address := s.download(t, "uid-admin-linked", otherClipID)

			response, _ := s.call(t, "uid-linked", clipCall{method: http.MethodPut, target: clipsPath + "/streamer", body: `{"downloads":false}`})
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.False(t, *s.p.clipsStreamerFor("videokey-dave").Downloads)
			require.False(t, s.canDownload(t, "uid-admin-linked", otherClipID), "Dave's block holds")
			requireDownloadPage(t, s.get(t, "uid-admin-linked", address, nil), http.StatusForbidden, "The streamer has turned off downloads of their clips.")
		})
	}
}

func TestADownloadTicketIsSpentOnce(t *testing.T) {
	s := newDownloadScene(t)
	url := s.ticketURL(t, "uid-linked", someClipID)

	spent := s.get(t, "uid-linked", url, nil)
	require.Equal(t, http.StatusSeeOther, spent.Code)
	require.Empty(t, s.store[kvClipTicketPrefix+"uid-linked"], "spent")

	requireDownloadPage(t, s.get(t, "uid-linked", url, nil), http.StatusGone, clipDownloadStale(productName))

	// A new press is a new ticket, and the one before it is no more.
	first := s.ticketURL(t, "uid-linked", someClipID)
	second := s.ticketURL(t, "uid-linked", someClipID)
	requireDownloadPage(t, s.get(t, "uid-linked", first, nil), http.StatusGone, clipDownloadStale(productName))
	require.Equal(t, http.StatusSeeOther, s.get(t, "uid-linked", second, nil).Code)
}

func TestADownloadTicketRunsOutAndIsBoundToItsPersonAndClip(t *testing.T) {
	s := newDownloadScene(t)

	// Another linked person cannot spend Dave's ticket, and trying leaves it
	// where it is.
	url := s.ticketURL(t, "uid-linked", otherClipID)
	requireDownloadPage(t, s.get(t, "uid-admin-linked", url, nil), http.StatusGone, clipDownloadStale(productName))
	address := clipsPath + "/" + s.get(t, "uid-linked", url, nil).Header().Get("Location")

	// It was for the clip it was asked for, and no other.
	served := s.get(t, "uid-linked", address, nil)
	require.Equal(t, http.StatusOK, served.Code)
	require.Contains(t, served.Header().Get("Content-Disposition"), "Bob's best jump part 2")

	// A minute after it was made, it has run out.
	stale := s.ticketURL(t, "uid-linked", someClipID)
	s.now = s.now.Add(clipTicketSeconds * time.Second)
	requireDownloadPage(t, s.get(t, "uid-linked", stale, nil), http.StatusGone, clipDownloadStale(productName))

	// Made up, it is no ticket at all.
	requireDownloadPage(t, s.get(t, "uid-linked", clipsPath+"/download?t="+strings.Repeat("0", 64), nil), http.StatusGone, clipDownloadStale(productName))
	requireDownloadPage(t, s.get(t, "uid-linked", clipsPath+"/download?t=short", nil), http.StatusGone, clipDownloadStale(productName))
	requireDownloadPage(t, s.get(t, "uid-linked", clipsPath+"/download", nil), http.StatusGone, clipDownloadStale(productName))
}

// The download address is for the person it was made for, may be asked again
// (the desktop app asks twice, a browser resumes with a range), and lasts ten
// minutes.
func TestTheDownloadAddressIsThePersonsOwnAndRunsOut(t *testing.T) {
	s := newDownloadScene(t)
	address := s.download(t, "uid-linked", someClipID)

	require.Equal(t, http.StatusOK, s.get(t, "uid-linked", address, nil).Code)
	require.Equal(t, http.StatusOK, s.get(t, "uid-linked", address, nil).Code, "asked again, as the desktop app does")
	requireDownloadPage(t, s.get(t, "uid-admin-linked", address, nil), http.StatusGone, clipDownloadStale(productName))

	// Changed by a digit, it is nobody's.
	tampered := strings.Replace(address, someClipID, otherClipID, 1)
	requireDownloadPage(t, s.get(t, "uid-linked", tampered, nil), http.StatusGone, clipDownloadStale(productName))

	s.now = s.now.Add(clipAddressSeconds * time.Second)
	requireDownloadPage(t, s.get(t, "uid-linked", address, nil), http.StatusGone, clipDownloadStale(productName))
}

func TestADownloadIsAnAttachmentWithRanges(t *testing.T) {
	s := newDownloadScene(t)
	fixture, err := os.ReadFile(clipFixture)
	require.NoError(t, err)

	address := s.download(t, "uid-linked", otherClipID)
	whole := s.get(t, "uid-linked", address, nil)
	require.Equal(t, http.StatusOK, whole.Code)
	require.Equal(t, fixture, whole.Body.Bytes())

	header := whole.Header()
	require.Equal(t, "video/mp4", header.Get("Content-Type"))
	require.Equal(t, "nosniff", header.Get("X-Content-Type-Options"))
	require.Equal(t, "no-store", header.Get("Cache-Control"))
	require.Equal(t, "default-src 'none'; sandbox", header.Get("Content-Security-Policy"))
	require.Equal(t, "bytes", header.Get("Accept-Ranges"))

	name := downloadFileName(clipStreamerOf(&clipEntry{StreamerID: "uid-linked", StreamName: "Dave"}, s.p.clipNamesNow(s.now)).Name, s.p.brandName(),
		`Bob's "best" jump / part 2`, time.Date(2026, 10, 13, 21, 5, 30, 0, time.UTC))
	require.True(t, strings.HasSuffix(name, " - Bob's best jump part 2 - 2026-10-13 2105.mp4"), name)
	require.Equal(t, attachmentDisposition(name), header.Get("Content-Disposition"))
	require.True(t, strings.HasPrefix(header.Get("Content-Disposition"), "attachment; filename=\""))

	part := s.get(t, "uid-linked", address, map[string]string{"Range": "bytes=0-99"})
	require.Equal(t, http.StatusPartialContent, part.Code)
	require.Equal(t, fixture[:100], part.Body.Bytes())
	require.Equal(t, "bytes 0-99/"+strings.TrimSpace(header.Get("Content-Length")), part.Header().Get("Content-Range"))

	rest := s.get(t, "uid-linked", address, map[string]string{"Range": "bytes=100-"})
	require.Equal(t, http.StatusPartialContent, rest.Code)
	require.Equal(t, fixture[100:], rest.Body.Bytes(), "a resume gets the rest")
}

func TestDownloadNamesAreClean(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	for _, tried := range []struct{ streamer, title, want string }{
		{"Dave", "That jump", "Dave - That jump - 2026-01-02 0304.mp4"},
		{"Dave", "", "Dave - clip - 2026-01-02 0304.mp4"},
		{"", "   ", "Matty - clip - 2026-01-02 0304.mp4"},
		{"Mi/lo", `a\b:c*d?e"f<g>h|i`, "Mi lo - a b c d e f g h i - 2026-01-02 0304.mp4"},
		{"Dave", "line\none\ttab", "Dave - line one tab - 2026-01-02 0304.mp4"},
		{"Dave", "evil‮gpj.exe", "Dave - evil gpj.exe - 2026-01-02 0304.mp4"},
		{"Dave", "...dots...", "Dave - dots - 2026-01-02 0304.mp4"},
		{"Erin Cat", "Café ✂ ünïcode", "Erin Cat - Café ✂ ünïcode - 2026-01-02 0304.mp4"},
	} {
		require.Equal(t, tried.want, downloadFileName(tried.streamer, "", tried.title, at), tried.title)
	}

	long := downloadFileName(strings.Repeat("s", 100), "Alice TV", strings.Repeat("t", 200), at)
	require.Equal(t, strings.Repeat("s", maxDownloadStreamerRunes)+" - "+strings.Repeat("t", maxDownloadTitleRunes)+" - 2026-01-02 0304.mp4", long)

	// A streamer with no name: the server's name for the plugin, cleaned.
	require.Equal(t, "Alice TV - clip - 2026-01-02 0304.mp4", downloadFileName("", "Alice TV", "", at))
	require.Equal(t, "Alice TV - clip - 2026-01-02 0304.mp4", downloadFileName(" ", "Alice/TV", "", at))

	// In the reader's own zone.
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	require.NoError(t, err)
	require.Equal(t, "Dave - clip - 2026-01-02 1204.mp4", downloadFileName("Dave", "Alice TV", "", at.In(tokyo)))

	// A plain name for old browsers, and the real one for the rest.
	require.Equal(t, `attachment; filename="Caf_ - clip.mp4"; filename*=UTF-8''Caf%C3%A9%20-%20clip.mp4`, attachmentDisposition("Café - clip.mp4"))
	require.Equal(t, `attachment; filename="Bob's - x.mp4"; filename*=UTF-8''Bob%27s%20-%20x.mp4`, attachmentDisposition("Bob's - x.mp4"))
}

func TestDownloadTicketsAreMeteredAndRefusalsCostNothing(t *testing.T) {
	s := newDownloadScene(t)

	// Refused for the clip, the switch or the stream: no token spent.
	s.ticket(t, "uid-linked", "0123456789abcdef")
	s.downloads(false)
	s.ticket(t, "uid-linked", someClipID)
	s.downloads(true)

	for range clipDownloadsPerWindow {
		require.Equal(t, http.StatusOK, s.ticket(t, "uid-linked", someClipID).Code)
	}
	requireClipError(t, s.ticket(t, "uid-linked", someClipID), http.StatusTooManyRequests, clipErrRateLimited, "ten in ten minutes")
	require.Equal(t, http.StatusOK, s.ticket(t, "uid-admin-linked", someClipID).Code, "per person")

	// A store that cannot keep the ticket gives the token back.
	s.now = s.now.Add(clipDownloadWindow)
	s.faults.failWrite(kvClipTicketPrefix + "uid-admin-linked")
	for range clipDownloadsPerWindow + 2 {
		requireClipError(t, s.ticket(t, "uid-admin-linked", someClipID), http.StatusServiceUnavailable, clipErrStore, "the store could not keep it")
	}
	s.faults.healWrite(kvClipTicketPrefix + "uid-admin-linked")
	require.Equal(t, http.StatusOK, s.ticket(t, "uid-admin-linked", someClipID).Code)
}

func TestARefusedDownloadWritesNothing(t *testing.T) {
	s := newDownloadScene(t)
	url := s.ticketURL(t, "uid-linked", otherClipID)
	address := s.download(t, "uid-linked", someClipID)
	other := s.ticketURL(t, "uid-admin-linked", someClipID)

	writes := 0
	s.faults.setOnWrite(func(string) { writes++ })

	s.get(t, "uid-admin-linked", url, nil)
	s.get(t, "uid-guest", url, nil)
	s.get(t, "uid-linked", clipsPath+"/download?t="+strings.Repeat("1", 64), nil)
	s.get(t, "uid-admin-linked", address, nil)
	s.downloads(false)
	s.get(t, "uid-linked", url, nil)
	s.get(t, "uid-admin-linked", other, nil)
	s.ticket(t, "uid-linked", someClipID)

	s.faults.setOnWrite(nil)
	require.Zero(t, writes, "nothing is written for a refusal")
}

func TestEveryDownloadIsLoggedWithNothingSecret(t *testing.T) {
	s := newDownloadScene(t)
	url := s.ticketURL(t, "uid-linked", otherClipID)
	ticket := strings.TrimPrefix(url, clipsPath+"/download?t=")

	address := clipsPath + "/" + s.get(t, "uid-linked", url, nil).Header().Get("Location")
	s.get(t, "uid-linked", address, nil)
	s.get(t, "uid-linked", address, map[string]string{"Range": "bytes=0-9"})

	var downloads []string
	for _, line := range infoLines(t, s.p) {
		if strings.HasPrefix(line, "A clip was downloaded") {
			downloads = append(downloads, line)
		}
	}
	require.Equal(t, []string{"A clip was downloaded user_id=uid-linked clip_id=" + otherClipID}, downloads, "once per download, not per range")

	for _, call := range s.api.Calls {
		if !strings.HasPrefix(call.Method, "Log") {
			continue
		}
		for _, argument := range call.Arguments {
			text, _ := argument.(string)
			require.NotContains(t, text, ticket, "never the ticket")
			require.NotContains(t, text, strings.TrimPrefix(address, clipsPath+"/download?d="), "never the address")
			require.NotContains(t, text, string(s.p.handleSecret))
		}
	}
}

// An open viewer asks about its clip again when the clips' version moves, so
// the version moves with either switch while downloads are on, and with
// downloads off it is 3.2.0's, byte for byte.
func TestTheClipsVersionFollowsTheDownloadSwitches(t *testing.T) {
	s := newDownloadScene(t)
	doc, err := s.p.clipsDocument(s.now)
	require.NoError(t, err)

	versionOf := func(on bool, doc *clipsDoc) string {
		cfg := s.p.getConfiguration().Clone()
		cfg.AllowClipDownloads = on

		return clipsViewOf(doc, cfg, true, s.now.UnixMilli(), func(string) bool { return true }, s.p.handleSecret).Version
	}

	off := versionOf(false, doc)
	on := versionOf(true, doc)
	require.NotEqual(t, off, on, "switching downloads on moves it")

	blocked := *doc
	blocked.NoDownloads = map[string]clipMark{s.p.streamTag("videokey-dave"): {By: "uid-linked", At: 1}}
	require.NotEqual(t, on, versionOf(true, &blocked), "a streamer's block moves it")
	require.Equal(t, off, versionOf(false, &blocked), "and with downloads off nothing about them is in it")

	elsewhere := *doc
	elsewhere.NoDownloads = map[string]clipMark{s.p.streamTag("videokey-bob"): {By: "uid-admin-linked", At: 1}}
	require.Equal(t, on, versionOf(true, &elsewhere), "a block on a stream with no clips moves nothing")
}
