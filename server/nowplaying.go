package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// What a streamer is streaming right now, said on the spot.
//
// A plan says what somebody means to stream, and plenty of streams are never
// planned: somebody starts a game on a whim and goes live. The "What's on?"
// control beside the Stream title in the panel's footer lets them say which
// movie, show or game it is, with the same kind toggles and title search as
// the planning form, and every panel then shows it on their Live now card,
// under their picture and in their profile.
//
// One document for everybody, keyed by user id, rather than a key per person.
// The poller reads it on every poll, to put it on the cards and to take down
// whatever has gone stale, and one read is cheaper than paging through the
// whole store to find the keys. It is written through casUpdate, like the
// schedule, because the streamer and the poller can both write it in the same
// second.
//
// It is short-lived on purpose. It clears itself once the stream it was about
// has been off the air for the same grace the live badge waits, so it cannot
// still be claiming Elden Ring at the start of tomorrow's film night. One set
// before going live, for a stream that never happens, lapses after twelve
// hours.

const (
	kvNowPlaying = "now_playing"

	// Versioned inside the document, as the schedule is, and for the same
	// reason: statsSchemaVersion governs the airtime tables, and this changing
	// shape must never be able to stop the hours being counted.
	nowPlayingVersion = 1

	// How long a choice made before going live waits for the stream. Long
	// enough for "after dinner", short enough that a stream which never
	// happened does not wait for the next one.
	nowPlayingUnairedMS = int64(12 * 60 * 60 * 1000)

	// A bound on a document every poll reads. Far above the number of people
	// who could be streaming at once on a server this size.
	maxNowPlaying = 200
)

// Refused in the sidebar's own voice, like planning.
const nowPlayingNeedsLink = "Link your stream first — saying what is on is for streamers."

// nowPlayingEntry is one streamer's choice as stored.
type nowPlayingEntry struct {
	// "movie" or "game". Never absent: a choice with no kind would only be a
	// second stream title, which the bar beside it already is.
	Category string `json:"category"`

	// The words, as typed or as the picked title filled them in.
	Title string `json:"title"`

	// The movie, show or game it is linked to, in the shape a plan carries,
	// looked up by the server when it was saved. Absent when nothing was
	// picked, or when lookups are off.
	Media *scheduleMedia `json:"media,omitempty"`

	// When it was saved, in epoch milliseconds. Also what tells a decision
	// the poller made about the last choice from a fresh one made since.
	SetAt int64 `json:"setAt"`

	// Whether their stream has been seen on the air since. Only then does
	// the stream going away clear it; until then it waits, for twelve hours.
	Aired bool `json:"aired,omitempty"`
}

type nowPlayingDoc struct {
	Version int                        `json:"v"`
	Entries map[string]nowPlayingEntry `json:"entries"`
}

// apiNowPlaying is what is on a live stream, as every panel is shown it.
type apiNowPlaying struct {
	Category string         `json:"category"`
	Title    string         `json:"title"`
	Media    *scheduleMedia `json:"media,omitempty"`
}

// offAirCount is how many ticks in a row somebody's stream has been off the
// air, for the choice saved at setAt. A new choice starts the count again.
type offAirCount struct {
	setAt int64
	polls int
}

type nowPlayingRequest struct {
	Category string `json:"category"`
	Title    string `json:"title"`

	// The ref of the title picked from the suggestions, or absent. Only the
	// ref: the details are looked up here, as a plan's are.
	Media string `json:"media"`
}

type nowPlayingResponse struct {
	NowPlaying *apiNowPlaying `json:"nowPlaying"`
}

// current reports whether an entry still stands at nowMS. One that has been
// on the air stands until the poller sees the stream gone for the grace; one
// that never aired lapses twelve hours after it was saved, whether or not the
// poller has got round to deleting it yet.
func (e nowPlayingEntry) current(nowMS int64) bool {
	return e.Aired || nowMS < e.SetAt+nowPlayingUnairedMS
}

// view is the entry as a panel may show it, or nil. Checked on the way out as
// well as on the way in, as a plan's media is: the document could have been
// written by a later build or by hand, and the webapp trusts this object.
func (e nowPlayingEntry) view() *apiNowPlaying {
	category := scheduleCategory(e.Category)
	title := cleanText(e.Title, maxScheduleTitle)
	if category == "" || title == "" {
		return nil
	}

	media := e.Media.checked()
	if media != nil {
		if ref, ok := parseMediaRef(media.Ref); !ok || ref.category() != category {
			media = nil
		}
	}

	return &apiNowPlaying{Category: category, Title: title, Media: media}
}

func emptyNowPlaying() nowPlayingDoc {
	return nowPlayingDoc{Version: nowPlayingVersion, Entries: map[string]nowPlayingEntry{}}
}

// decodeNowPlaying reads the stored bytes. False is bytes that will not
// decode, or that a later build wrote; nothing at all is an empty document.
func decodeNowPlaying(raw []byte) (nowPlayingDoc, bool) {
	if len(raw) == 0 {
		return emptyNowPlaying(), true
	}

	var doc nowPlayingDoc
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Version > nowPlayingVersion {
		return emptyNowPlaying(), false
	}
	if doc.Entries == nil {
		doc.Entries = map[string]nowPlayingEntry{}
	}

	return doc, true
}

// readNowPlaying is the document as stored. A false is "the store would not
// answer", which is not the same as "nobody has said anything" and must not be
// shown as though it were.
//
// A document that will not decode is set aside and read as empty, as the
// schedule's is. It is a few people's say-so for the next few hours, and the
// feature working again matters more than the broken copy.
func (p *Plugin) readNowPlaying() (nowPlayingDoc, bool) {
	raw, appErr := p.API.KVGet(kvNowPlaying)
	if appErr != nil {
		p.API.LogWarn("Could not read what is on", "error", appErr.Error())

		return nowPlayingDoc{}, false
	}

	doc, ok := decodeNowPlaying(raw)
	if !ok {
		p.setAside(kvNowPlaying, raw, "What is on could not be read and has been set aside")

		return emptyNowPlaying(), true
	}

	return doc, true
}

// changeNowPlaying is the only way the document is written. change edits the
// entries as stored and says whether it changed anything. It can run more
// than once, because casUpdate retries a write that lost a race, so it must
// decide from what it is handed and nothing else.
func (p *Plugin) changeNowPlaying(change func(entries map[string]nowPlayingEntry) (bool, error)) error {
	return p.casUpdate(kvNowPlaying, func(raw []byte) ([]byte, error) {
		doc, ok := decodeNowPlaying(raw)
		if !ok {
			// A copy first, then written over: refusing every write for ever
			// because of one bad document would switch the feature off.
			p.quarantine(kvNowPlaying, raw)
			doc = emptyNowPlaying()
		}

		changed, err := change(doc.Entries)
		if err != nil {
			return nil, err
		}
		if !changed && ok {
			return nil, nil
		}

		doc.Version = nowPlayingVersion

		return json.Marshal(doc)
	})
}

// saveNowPlaying stores one streamer's choice in place of whatever they had.
// Choices that have lapsed go at the same time, so the bound below is only
// ever reached by choices that still stand.
func (p *Plugin) saveNowPlaying(userID string, entry nowPlayingEntry) error {
	return p.changeNowPlaying(func(entries map[string]nowPlayingEntry) (bool, error) {
		for other, stored := range entries {
			if other != userID && !stored.current(entry.SetAt) {
				delete(entries, other)
			}
		}

		if _, theirs := entries[userID]; !theirs && len(entries) >= maxNowPlaying {
			return false, errTooManyItems
		}

		entries[userID] = entry

		return true, nil
	})
}

// clearNowPlaying takes one streamer's choice down. Nothing to take down is
// not an error, and writes nothing.
func (p *Plugin) clearNowPlaying(userID string) error {
	return p.changeNowPlaying(func(entries map[string]nowPlayingEntry) (bool, error) {
		if _, set := entries[userID]; !set {
			return false, nil
		}

		delete(entries, userID)

		return true, nil
	})
}

// addNowPlaying puts what is on onto each person's card in the snapshot, by
// nowPlayingFor.
//
// settle is true only on a poll that has just read the status listing, and
// only then is anything taken down: a listing that could not be read is no
// evidence that anybody stopped streaming, which is the same reason a failed
// poll keeps the last known cards. ticked is pollOnce's, and only a tick
// counts toward the grace (see settleNowPlaying).
//
// The cards are copied before anything is written on them. On a failed poll
// they are the previous snapshot's own, which other requests may be reading
// at this moment.
func (p *Plugin) addNowPlaying(current *snapshot, cfg *configuration, now time.Time, settle, ticked bool) {
	nowMS := now.UnixMilli()

	var entries map[string]nowPlayingEntry
	if doc, ok := p.readNowPlaying(); ok {
		entries = doc.Entries
		if settle {
			entries = p.settleNowPlaying(entries, current.Streams, cfg, nowMS, ticked)
		}

		p.nowPlayingLock.Lock()
		p.nowPlayingLast = entries
		p.nowPlayingLock.Unlock()
	} else {
		// The store would not answer: what it said last time, rather than
		// every card losing what is on at once over a database blip.
		p.nowPlayingLock.Lock()
		entries = p.nowPlayingLast
		p.nowPlayingLock.Unlock()
	}

	if len(current.Streams) == 0 {
		return
	}

	streams := make([]apiStream, len(current.Streams))
	copy(streams, current.Streams)

	for i := range streams {
		streams[i].NowPlaying = nowPlayingFor(streams[i], entries, current.Schedule, nowMS)
	}

	current.Streams = streams
}

// settleNowPlaying brings the document into line with the listing just read,
// and returns the entries as they now stand.
//
//   - Somebody on the air has their choice marked as aired, once.
//   - Somebody whose aired choice has seen their stream off the air for the
//     offline grace — the same number of ticks the live badge waits, so an OBS
//     reconnect costs nothing — has it taken down. Only a tick counts one:
//     a poll somebody's click asked for, a moment after a tick, would
//     otherwise delete what was on while OBS was still reconnecting.
//   - A choice that never aired is taken down twelve hours after it was made.
//
// Every write is matched on SetAt, so a fresh choice made while this was
// deciding about the old one is never what gets taken down or marked.
func (p *Plugin) settleNowPlaying(entries map[string]nowPlayingEntry, streams []apiStream, cfg *configuration, nowMS int64, ticked bool) map[string]nowPlayingEntry {
	// On the air is a stream their own link proves is theirs, or their
	// Twitch channel: somebody linked who is live on Twitch has said what is
	// on just the same, and it settles when that stream ends.
	onAir := map[string]bool{}
	for _, stream := range streams {
		if stream.UserID != "" && !stream.AlwaysOn && (stream.LinkSource == linkSourceManual || stream.LinkSource == linkSourceTwitch) {
			onAir[stream.UserID] = true
		}
	}

	grace := cfg.OfflineGraceChecks
	if grace < 1 {
		grace = 1
	}

	aired := map[string]int64{}
	gone := map[string]int64{}

	p.nowPlayingLock.Lock()
	if p.nowPlayingOff == nil {
		p.nowPlayingOff = map[string]offAirCount{}
	}

	for userID, count := range p.nowPlayingOff {
		if entry, set := entries[userID]; !set || entry.SetAt != count.setAt {
			delete(p.nowPlayingOff, userID)
		}
	}

	for userID, entry := range entries {
		switch {
		// Lapsed first, even for somebody on the air now: a choice made the
		// night before, for a stream that never happened, is not what they
		// are streaming today.
		case !entry.current(nowMS):
			gone[userID] = entry.SetAt

		case onAir[userID]:
			delete(p.nowPlayingOff, userID)
			if !entry.Aired {
				aired[userID] = entry.SetAt
			}

		case entry.Aired:
			// Off the air. A count the grace has already run out on is acted
			// on by any poll, since a write that failed is tried again; only
			// a tick adds to it.
			count := p.nowPlayingOff[userID]
			if ticked {
				count.setAt = entry.SetAt
				count.polls++
				p.nowPlayingOff[userID] = count
			}

			if count.polls >= grace {
				gone[userID] = entry.SetAt
			}
		}
	}
	p.nowPlayingLock.Unlock()

	if len(aired) == 0 && len(gone) == 0 {
		return entries
	}

	err := p.changeNowPlaying(func(stored map[string]nowPlayingEntry) (bool, error) {
		changed := false

		for userID, setAt := range gone {
			if entry, set := stored[userID]; set && entry.SetAt == setAt {
				delete(stored, userID)
				changed = true
			}
		}

		for userID, setAt := range aired {
			if entry, set := stored[userID]; set && entry.SetAt == setAt && !entry.Aired {
				entry.Aired = true
				stored[userID] = entry
				changed = true
			}
		}

		return changed, nil
	})
	if err != nil {
		// Tried again on the next poll: the count is kept, so the decision
		// comes out the same.
		p.API.LogWarn("Could not bring what is on up to date", "error", err.Error())

		return entries
	}

	p.nowPlayingLock.Lock()
	for userID := range gone {
		delete(p.nowPlayingOff, userID)
	}
	p.nowPlayingLock.Unlock()

	settled := make(map[string]nowPlayingEntry, len(entries))
	for userID, entry := range entries {
		if _, drop := gone[userID]; drop {
			continue
		}
		if _, on := aired[userID]; on {
			entry.Aired = true
		}
		settled[userID] = entry
	}

	return settled
}

// nowPlayingFor is THE rule for what is on a stream. Every place the panel
// says what somebody is streaming — their Live now card, the line under their
// picture, their profile — reads what this put on the card, so the three can
// never disagree.
//
//  1. Never on a 24/7 channel, and never on a stream nobody is named for.
//  2. What the streamer said with What's on?, while it stands, and only on the
//     stream their own link proves is theirs. It beats a plan's title: a plan
//     is what they meant to stream, and this is what they say they are
//     streaming.
//  3. Otherwise, the title linked to their own plan that started most
//     recently, when that plan has one. Started plans stay listed for three
//     hours, so a film at seven and a game at nine are both there at half
//     past nine, and it is the game that is on. Only then is it asked whether
//     that plan has a title: one that does not is not stood in for by an
//     older one that does.
//
// Everything it reads is in the snapshot or the document, and it reads no
// clock but nowMS, so the same state always makes the same card.
func nowPlayingFor(stream apiStream, entries map[string]nowPlayingEntry, schedule []scheduleItem, nowMS int64) *apiNowPlaying {
	if stream.AlwaysOn || stream.UserID == "" {
		return nil
	}

	if stream.LinkSource == linkSourceManual || stream.LinkSource == linkSourceTwitch {
		if entry, set := entries[stream.UserID]; set && entry.current(nowMS) {
			if shown := entry.view(); shown != nil {
				return shown
			}
		}
	}

	var latest *scheduleItem
	for i := range schedule {
		item := &schedule[i]
		if item.UserID != stream.UserID || item.StartsAt > nowMS {
			continue
		}

		// The same order the schedule is kept in, asked again rather than
		// assumed: the latest start wins, and between two at the same moment
		// the later id, as the profile always chose.
		if latest == nil || item.StartsAt > latest.StartsAt || (item.StartsAt == latest.StartsAt && item.ID > latest.ID) {
			latest = item
		}
	}

	if latest == nil {
		return nil
	}

	media := latest.Media.checked()
	if media == nil {
		return nil
	}

	ref, ok := parseMediaRef(media.Ref)
	if !ok {
		return nil
	}

	return &apiNowPlaying{Category: ref.category(), Title: media.Title, Media: media}
}

// handleGetNowPlaying answers GET /api/v1/nowplaying with the caller's own
// choice, for the footer: their card only carries it while they are live,
// and the control has to show it before they are.
func (p *Plugin) handleGetNowPlaying(w http.ResponseWriter, userID string) {
	if !p.requireLinkedStream(w, userID, nowPlayingNeedsLink) {
		return
	}

	doc, ok := p.readNowPlaying()
	if !ok {
		communityError(w, errStore)
		return
	}

	w.Header().Set("Cache-Control", "no-store")

	var shown *apiNowPlaying
	if entry, set := doc.Entries[userID]; set && entry.current(nowMillis()) {
		shown = entry.view()
	}

	writeJSON(w, http.StatusOK, nowPlayingResponse{NowPlaying: shown})
}

// handleSetNowPlaying answers POST /api/v1/nowplaying: what the caller is
// streaming, checked exactly as a plan's words and title are.
func (p *Plugin) handleSetNowPlaying(w http.ResponseWriter, r *http.Request, userID string) {
	// Before the body is read or a title looked up: somebody who cannot say
	// what is on must not make the server call TMDB, Steam or IGDB either.
	if !p.requireLinkedStream(w, userID, nowPlayingNeedsLink) {
		return
	}

	var request nowPlayingRequest
	if !decodeBody(r, &request) {
		writeError(w, http.StatusBadRequest, "Could not read the request.")
		return
	}

	category := scheduleCategory(request.Category)
	if category == "" {
		writeError(w, http.StatusBadRequest, "Say whether it is a movie or a game.")
		return
	}

	// Clamped again after the redaction, as a plan's title is: the words
	// "my stream" can be longer than a short stream key they replace.
	title := p.cleanTyped(userID, request.Title, maxScheduleTitle)
	if title == "" {
		writeError(w, http.StatusBadRequest, "Say what is on.")
		return
	}

	var media *scheduleMedia

	if picked := strings.TrimSpace(request.Media); picked != "" {
		ref, ok := parseMediaRef(picked)
		if !ok {
			writeError(w, http.StatusBadRequest, "That title is not one this server can look up.")
			return
		}

		if ref.category() != category {
			writeError(w, http.StatusBadRequest, "A movie or show goes with the film icon, and a game with the game icon.")
			return
		}

		// The same title again keeps the details already stored: no call
		// out, and nothing lost if lookups have been switched off since —
		// the rule for a plan being changed.
		doc, read := p.readNowPlaying()
		if !read {
			communityError(w, errStore)
			return
		}

		if stored, set := doc.Entries[userID]; set && stored.Media != nil && stored.Media.Ref == ref.String() {
			media = stored.Media
		} else {
			media = p.planMedia(r.Context(), ref)
		}
	}

	// Saved while already on the air, it counts as aired at once: a poll
	// that cannot reach Broadcast Box for a while afterwards must not leave
	// it waiting twelve hours instead of the offline grace.
	entry := nowPlayingEntry{
		Category: category,
		Title:    title,
		Media:    media,
		SetAt:    nowMillis(),
		Aired:    p.linkedKeyIsLive(p.getLinkedKey(userID)) || p.twitchLive().twitchLiveFor(userID),
	}

	if err := p.saveNowPlaying(userID, entry); err != nil {
		communityError(w, err)
		return
	}

	// The cards carry it, so refresh instead of waiting for the tick.
	p.refreshSoon()

	writeJSON(w, http.StatusOK, nowPlayingResponse{NowPlaying: entry.view()})
}

// handleClearNowPlaying answers DELETE /api/v1/nowplaying. No link is asked
// for, as none is for cancelling a plan: taking your own choice down is
// always safe, and somebody who has just unlinked must not be left unable to.
func (p *Plugin) handleClearNowPlaying(w http.ResponseWriter, userID string) {
	if err := p.clearNowPlaying(userID); err != nil {
		communityError(w, err)
		return
	}

	p.refreshSoon()

	writeJSON(w, http.StatusOK, nowPlayingResponse{})
}
