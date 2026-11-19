package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Twitch live (design/twitch-live.md §5): people who connected their Twitch
// account show as live here while they are live on Twitch.
//
// Twitch is asked, never told. Once a minute, while the switch is on and
// somebody is connected, Get Streams says who of them is live, with the title,
// category, viewer count and picture their card needs; EventSub would say only
// that somebody went live, and needs things this design refuses to keep. What
// the last good read said is kept in a store that outlives the poller, and
// every poll of Broadcast Box merges it into the stream list from memory, so
// Broadcast Box is never kept waiting on Twitch.
//
// The hosts, all asked by the server and none by a browser: api.twitch.tv for
// Get Streams and, once a day, Get Users to keep names right; id.twitch.tv for
// the application's token (twitch_app.go); and static-cdn.jtvnw.net for the
// preview pictures, relayed (twitch_preview.go). Connecting adds id.twitch.tv
// and api.twitch.tv once per connection (twitch_oauth.go).
//
// Everything that contacts Twitch, shows a Twitch stream or credits Twitch
// time asks twitchLiveOn first. Taking things away never does: disconnecting,
// blocking and purging work whatever the switch says (twitch_links.go).

const (
	// Helix is cached for about a minute, so asking more often learns
	// nothing.
	twitchReadEvery = 60 * time.Second

	// The shortest gap between reads when a connect or a disconnect asks for
	// one sooner.
	twitchKickGap = 10 * time.Second

	// Failed reads keep the last good list this long. After that it is
	// dropped: a card nobody can confirm is worse than none.
	twitchKeepFor = 5 * time.Minute

	// Get Streams can briefly leave out a stream that is live, because the
	// list moves while it is paged. One good read without it is forgiven.
	twitchMissedReads = 1

	// Get Streams takes at most this many user_id in one call.
	twitchBatch = 100

	// A whole pass, every batch included.
	twitchReadBudget = 20 * time.Second

	// Failures back off 60 s, 120 s, 240 s and so on, up to this.
	twitchBackoffMax = 10 * time.Minute

	// With fewer points than this left in the bucket, the next read waits
	// for the bucket to refill.
	twitchRateFloor = 5

	// Names are confirmed with Get Users this often, and an account Get
	// Users has not found this many days running is disconnected.
	twitchNamesEvery = 24 * time.Hour
	twitchGoneChecks = 2

	// A daily names check that failed is not tried again for this long.
	twitchNamesRetry = 10 * time.Minute

	// At most this many people connected: five calls a minute at most.
	maxTwitchLinks = 500

	// What Twitch lists a stream under is a chip on its card.
	maxTwitchCategory = 80

	// linkSourceTwitch is the LinkSource of a Twitch stream: whose it is
	// comes from the link document, not from a stream key.
	linkSourceTwitch = "twitch"

	// sourceTwitch is apiStream.Source for a Twitch stream.
	sourceTwitch = "twitch"
)

// twitchLiveOn: the switch is on and an application is set up. Everything
// in this feature that contacts Twitch, shows a Twitch stream or credits
// Twitch time asks this one question first, so a server with the switch
// off makes no Twitch request. Taking things away does not ask it:
// disconnecting, blocking and purging work whatever the switch says,
// because a person's right to withdraw does not depend on an admin's
// setting. With the switch off, the only Twitch KV reads are one read of
// twitch_links per /me and one at activation.
func (c *configuration) twitchLiveOn() bool {
	_, ok := c.twitchCredentials()

	return c.EnableTwitchLive && ok
}

// twitchEmbedOn: Twitch streams may play in the panel, in Twitch's player.
func (c *configuration) twitchEmbedOn() bool {
	return c.twitchLiveOn() && c.TwitchEmbed
}

// sameTwitchLive: nothing that changes what the Twitch loop does has changed.
// TwitchEmbed is not in it: changing that only changes the snapshot, which
// the poller's restart republishes.
func sameTwitchLive(a, b *configuration) bool {
	if a == nil || b == nil {
		return false
	}

	return a.EnableTwitchLive == b.EnableTwitchLive &&
		a.TwitchClientID == b.TwitchClientID &&
		a.TwitchClientSecret == b.TwitchClientSecret
}

// twitchLive is one live stream, as Get Streams last described it.
type twitchLive struct {
	TwitchID, Login, Name string
	Title, Category       string // cleaned
	Viewers               int
	StartedAt             string // RFC 3339, as Twitch sent it
	Thumb                 string // the thumbnail_url template, host-checked
	Missed                int    // good reads in a row that did not list it
}

// twitchStatus is what an admin is shown about the loop.
type twitchStatus struct {
	lastTry, lastGood time.Time
	problem           string
	live              int
}

// twitchStore is Twitch live's memory: who is live, the link document as last
// read, how the last reads went, and the relayed pictures. Made on first use
// and kept for the life of the plugin, so a poller or loop restart loses
// nothing. Nothing in it is ever written to KV.
//
// Locks, in the only order any of them is taken together: runLock (starting
// and stopping the loop), then the plugin's statsWriteLock, then lock. The
// purge worker's own purgeLock is only ever taken alone.
type twitchStore struct {
	plugin *Plugin

	now   func() time.Time                     // time.Now; tests replace it
	after func(time.Duration) <-chan time.Time // time.After; tests replace it

	lock     sync.Mutex
	live     map[string]twitchLive // by Twitch id
	links    *twitchLinks          // cached document, nil before the first read
	linksBad bool                  // the stored document will not decode
	lastTry  time.Time
	lastGood time.Time
	fails    int
	nextAt   time.Time // when the next read is due
	holding  bool      // nextAt is a backoff or a wait Twitch asked for, which a kick never cuts short
	problem  string    // admin sentence, "" when fine
	rate     twitchRate

	// creditAt is when the last Twitch airtime pass ran, in milliseconds:
	// Twitch's own evidence of having been watched, kept apart from
	// Broadcast Box's lastPollAt. In memory on purpose, as that is.
	creditAt int64

	namesRetryAt time.Time

	pictures       map[string]*twitchPicture // by Twitch id
	pictureFlights flightGroup

	connectLimiter, previewLimiter *rateLimiter

	runLock sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	kickCh  chan struct{} // capacity 1, coalesced like refreshCh

	// The purge worker (twitch_links.go): at most one, started by a kick.
	purgeLock   sync.Mutex
	purgeCancel context.CancelFunc
	purgeDone   chan struct{}
	purgeAgain  bool // kicked while running: look again before stopping
	purgeOff    bool // the plugin is stopping: no new worker
}

func (p *Plugin) twitchLive() *twitchStore {
	p.twitchLiveOnce.Do(func() {
		p.twitchLiveState = &twitchStore{
			plugin:   p,
			now:      time.Now,
			after:    time.After,
			live:     map[string]twitchLive{},
			pictures: map[string]*twitchPicture{},
			kickCh:   make(chan struct{}, 1),

			// Five connects a quarter of an hour: each is a state written
			// and a trip to Twitch's sign-in pages.
			connectLimiter: newRateLimiter(5, 15*time.Minute),

			// A preview is fetched from Twitch only on a miss, and a panel
			// asks for each card's once per version, so this is generous.
			previewLimiter: newRateLimiter(120, time.Minute),
		}
	})

	return p.twitchLiveState
}

// restartTwitchLive stops the running loop, if any, and waits for it, the way
// restartPoller does. It starts a new one only while the switch is on; with
// it off it empties the live list, so the poll the settings save triggers
// anyway shows no Twitch card. Badges come off after the usual grace.
func (p *Plugin) restartTwitchLive() {
	s := p.twitchLive()

	s.runLock.Lock()
	defer s.runLock.Unlock()

	s.stopLoopLocked()
	if !p.getConfiguration().twitchLiveOn() {
		s.clearLive(false)
		return
	}

	s.startLoopLocked()
}

// stopTwitchLive stops the loop and waits for it.
func (p *Plugin) stopTwitchLive() {
	s := p.twitchLive()

	s.runLock.Lock()
	defer s.runLock.Unlock()

	s.stopLoopLocked()
}

// stopLoopLocked cancels the loop and waits for it, with runLock held and no
// stats lock: a step ends within one KV read or one Helix request of being
// cancelled, so this holds up a settings save by no more than that.
func (s *twitchStore) stopLoopLocked() {
	if s.cancel == nil {
		return
	}

	s.cancel()
	<-s.done
	s.cancel, s.done = nil, nil
}

func (s *twitchStore) startLoopLocked() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.cancel, s.done = cancel, done

	go s.run(ctx, done)
}

// run is the loop: a step now, then a step whenever one is due or a kick asks
// for one sooner. A step that panics is followed by a pause, then the loop
// (recovery.go), since it may have panicked before saying when the next is
// due; the locks it takes are let go by a defer or held only around
// assignments.
func (s *twitchStore) run(ctx context.Context, done chan struct{}) {
	defer close(done)

	for {
		passSafely(ctx, s.plugin.API, "the Twitch live loop", loopPanicPause, func() {
			s.step(ctx, s.plugin.getConfiguration(), s.now())
		})

		for {
			if ctx.Err() != nil {
				return
			}

			s.lock.Lock()
			wait := s.nextAt.Sub(s.now())
			s.lock.Unlock()

			if wait <= 0 {
				break
			}

			select {
			case <-ctx.Done():
				return
			case <-s.kickCh:
				// nextAt may have moved closer; look again.
			case <-s.after(wait):
			}
		}
	}
}

// kick asks for a read no sooner than twitchKickGap after the last one. It
// never blocks, and is a no-op while the loop is not running. It never cuts
// short a backoff or a wait Twitch asked for.
func (s *twitchStore) kick() {
	s.lock.Lock()
	if !s.holding {
		if soonest := s.lastTry.Add(twitchKickGap); soonest.Before(s.nextAt) {
			s.nextAt = soonest
		}
	}
	s.lock.Unlock()

	select {
	case s.kickCh <- struct{}{}:
	default:
	}
}

// helixStream is one entry of Get Streams' answer: only the fields used.
type helixStream struct {
	UserID       string `json:"user_id"`
	UserLogin    string `json:"user_login"`
	UserName     string `json:"user_name"`
	GameName     string `json:"game_name"`
	Type         string `json:"type"`
	Title        string `json:"title"`
	ViewerCount  int    `json:"viewer_count"`
	StartedAt    string `json:"started_at"`
	ThumbnailURL string `json:"thumbnail_url"`
}

// step is one pass. The goroutine only calls it on a timer and on a kick, and
// tests call it directly with a fake clock and fake hosts.
func (s *twitchStore) step(ctx context.Context, cfg *configuration, now time.Time) {
	p := s.plugin

	if !cfg.twitchLiveOn() {
		// The loop is stopped whenever the switch goes off; a step that
		// sees it off first only empties the list and waits for that.
		s.clearLive(false)

		s.lock.Lock()
		s.nextAt = now.Add(twitchReadEvery)
		s.lock.Unlock()

		return
	}
	creds, _ := cfg.twitchCredentials()

	if ctx.Err() != nil {
		return
	}

	// The document, read fresh every pass: one KV read a minute. A read that
	// fails keeps the copy in hand; bytes that will not decode are set aside
	// and show no Twitch card at all until an admin starts again.
	doc, err := p.readTwitchLinks()
	switch {
	case errors.Is(err, errTwitchLinksUnreadable):
		s.linksUnreadable(now)
		return
	case err != nil:
		p.API.LogWarn("Could not read the list of Twitch connections; using the last one read", "error", err.Error())
	default:
		s.noteLinks(doc)
	}

	s.lock.Lock()
	links := s.links
	due := !s.nextAt.After(now)
	s.lock.Unlock()

	if due {
		connected := links.twitchIDs()
		if len(connected) == 0 {
			// Nobody to ask about, and so nothing asked.
			s.lock.Lock()
			s.live = map[string]twitchLive{}
			s.nextAt = now.Add(twitchReadEvery)
			s.lock.Unlock()
		} else if listed, good := s.read(ctx, creds, connected, now); good {
			// Credited from Twitch's own reads, and only from what this read
			// listed: a stream kept through a missed read earns nothing.
			p.recordTwitchTime(listed, cfg, now)
			p.renameTwitchLinks(listed, now)
			p.checkTwitchNames(ctx, creds, now)
		}
	}

	s.lock.Lock()
	if now.Sub(s.lastGood) > twitchKeepFor {
		s.live = map[string]twitchLive{}
	}
	s.dropPicturesLocked()
	if !s.nextAt.After(now) {
		s.nextAt = now.Add(twitchReadEvery)
	}
	s.lock.Unlock()
}

// read asks Get Streams about everybody connected, a hundred at a time, and
// merges a good answer into the live list. All or nothing: if one batch
// fails, no batch's answer is used, so nobody vanishes because their batch
// did. listed is exactly what this read returned.
func (s *twitchStore) read(outer context.Context, creds twitchCredentials, connected []string, now time.Time) ([]twitchLive, bool) {
	p := s.plugin

	ctx, cancel := context.WithTimeout(outer, twitchReadBudget)
	defer cancel()

	s.lock.Lock()
	s.lastTry = now
	s.lock.Unlock()

	var answered []helixStream
	rate := twitchRate{limit: -1, remaining: -1}

	for start := 0; start < len(connected); start += twitchBatch {
		if ctx.Err() != nil {
			// Stopped, which is nobody's failure; or out of time, which is.
			if outer.Err() == nil {
				s.failed(errTwitchUnavailable, now)
			}

			return nil, false
		}

		query := url.Values{}
		query.Set("type", "live")
		query.Set("first", fmt.Sprint(twitchBatch))
		query["user_id"] = connected[start:min(start+twitchBatch, len(connected))]

		body, batchRate, err := p.twitchApp().helixGet(ctx, creds, "/helix/streams", query)
		if err != nil {
			if outer.Err() == nil {
				s.failed(err, now)
			}

			return nil, false
		}
		rate = batchRate

		var page struct {
			Data []helixStream `json:"data"`
		}
		if json.Unmarshal(body, &page) != nil {
			p.twitchApp().warnOnce("twitch-helix", "Twitch's list of live streams was not the expected shape.", "reason", "streams")
			s.failed(errTwitchBadAnswer, now)

			return nil, false
		}
		answered = append(answered, page.Data...)
	}

	wanted := make(map[string]bool, len(connected))
	for _, id := range connected {
		wanted[id] = true
	}

	listed := make([]twitchLive, 0, len(answered))
	seen := map[string]bool{}
	for _, entry := range answered {
		if entry.Type != "live" || !wanted[entry.UserID] || seen[entry.UserID] {
			continue
		}

		// A stream whose login or id is not the shape Twitch documents is
		// left out of this read, so no card can carry an unchecked login
		// into the player's address.
		who, ok := cleanTwitchUser(entry.UserID, entry.UserLogin, entry.UserName)
		if !ok {
			p.twitchApp().warnOnce("twitch-helix", "Twitch listed a live stream Matty could not check; it is left out.", "reason", "user")
			continue
		}
		seen[entry.UserID] = true

		thumb := ""
		if twitchThumbOK(entry.ThumbnailURL) {
			thumb = entry.ThumbnailURL
		} else if entry.ThumbnailURL != "" {
			p.twitchApp().warnOnce("twitch-preview", "Twitch gave a preview picture address Matty does not fetch from.", "reason", "thumbnail")
		}

		listed = append(listed, twitchLive{
			TwitchID:  who.ID,
			Login:     who.Login,
			Name:      who.DisplayName,
			Title:     cleanText(entry.Title, maxMOTDLength),
			Category:  cleanText(entry.GameName, maxTwitchCategory),
			Viewers:   max(entry.ViewerCount, 0),
			StartedAt: entry.StartedAt,
			Thumb:     thumb,
		})
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	next := make(map[string]twitchLive, len(listed))
	for _, stream := range listed {
		next[stream.TwitchID] = stream
	}
	for id, known := range s.live {
		if _, again := next[id]; again || !wanted[id] {
			continue
		}
		known.Missed++
		if known.Missed <= twitchMissedReads {
			next[id] = known
		}
	}

	s.live = next
	s.lastGood = now
	s.fails = 0
	s.problem = ""
	s.holding = false
	s.rate = rate
	s.nextAt = now.Add(twitchReadEvery)

	// Nearly out of points: the next read waits for the bucket to refill,
	// never longer than the backoff's cap.
	if rate.remaining >= 0 && rate.remaining < twitchRateFloor && rate.reset.After(s.nextAt) {
		s.nextAt = minTime(rate.reset, now.Add(twitchBackoffMax))
		s.holding = true
	}

	return listed, true
}

// failed records a read that went wrong. The list is left as it was: failed
// reads keep it for twitchKeepFor.
func (s *twitchStore) failed(err error, now time.Time) {
	s.lock.Lock()
	defer s.lock.Unlock()

	var slow *twitchSlowDown
	if errors.As(err, &slow) {
		wait := slow.reset.Sub(now)
		wait = max(wait, twitchReadEvery)
		wait = min(wait, twitchBackoffMax)

		s.nextAt = now.Add(wait)
		s.holding = true
		s.problem = fmt.Sprintf("Twitch is asking "+productName+" to slow down; checking again at %s UTC.", s.nextAt.UTC().Format("15:04"))

		return
	}

	s.fails++
	backoff := twitchBackoffMax
	if s.fails <= 10 {
		backoff = min(twitchReadEvery<<(s.fails-1), twitchBackoffMax)
	}
	s.nextAt = now.Add(backoff)
	s.holding = true

	if s.fails < 3 {
		return
	}

	switch {
	case errors.Is(err, errTwitchRefused):
		s.problem = "Twitch refused the application's Client ID or Client Secret. Fix them in the Twitch section."
	case s.lastGood.IsZero():
		s.problem = "Twitch could not be reached; there has been no good check yet."
	default:
		s.problem = fmt.Sprintf("Twitch could not be reached; the last good check was %d minutes ago.", int(now.Sub(s.lastGood)/time.Minute))
	}
}

// linksUnreadable is the loop finding a link document that will not decode.
// It is copied aside once, never overwritten, and no Twitch card shows until
// an admin deletes all Twitch data.
func (s *twitchStore) linksUnreadable(now time.Time) {
	s.lock.Lock()
	first := !s.linksBad
	s.linksBad = true
	s.links = nil
	s.live = map[string]twitchLive{}
	s.pictures = map[string]*twitchPicture{}
	s.problem = twitchLinksProblem
	s.nextAt = now.Add(twitchReadEvery)
	s.lock.Unlock()

	if first {
		if raw, appErr := s.plugin.API.KVGet(kvTwitchLinks); appErr == nil && len(raw) > 0 {
			s.plugin.quarantine(kvTwitchLinks, raw)
		}
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}

	return b
}

// dropPicturesLocked forgets the pictures of streams that are no longer live.
func (s *twitchStore) dropPicturesLocked() {
	for id := range s.pictures {
		if _, live := s.live[id]; !live {
			delete(s.pictures, id)
		}
	}
}

// liveNow is a copy of the streams to show, in Twitch id order: those the
// last good read listed and those forgiven one missed read, and nothing once
// no read has succeeded for twitchKeepFor.
func (s *twitchStore) liveNow(now time.Time) []twitchLive {
	s.lock.Lock()
	defer s.lock.Unlock()

	if now.Sub(s.lastGood) > twitchKeepFor {
		return nil
	}

	streams := make([]twitchLive, 0, len(s.live))
	for _, stream := range s.live {
		if stream.Missed <= twitchMissedReads {
			streams = append(streams, stream)
		}
	}
	sort.Slice(streams, func(i, j int) bool { return streams[i].TwitchID < streams[j].TwitchID })

	return streams
}

// liveEntry is one stream from liveNow, by Twitch id.
func (s *twitchStore) liveEntry(twitchID string, now time.Time) (twitchLive, bool) {
	for _, stream := range s.liveNow(now) {
		if stream.TwitchID == twitchID {
			return stream, true
		}
	}

	return twitchLive{}, false
}

// cachedLinks is the link document as last read. When the switch is on and
// nothing has been read yet, it reads it itself, once: the first poll after a
// start comes before the loop's first step, and must not see nobody connected
// (it would credit a restart's backlog with no paid spans, and push boards
// with every Twitch row missing). A read that fails is errStore, which the
// callers take as "skip, never guess" for that one poll. Bytes that will not
// decode are errTwitchLinksUnreadable, a standing problem rather than a
// passing one, which the callers take as "nobody connected to Twitch":
// Broadcast Box is paid in full and the boards are its own. Quarantining is
// left to the loop.
func (s *twitchStore) cachedLinks() (*twitchLinks, error) {
	s.lock.Lock()
	links, bad := s.links, s.linksBad
	s.lock.Unlock()

	if bad {
		return nil, errTwitchLinksUnreadable
	}
	if links != nil {
		return links, nil
	}

	doc, err := s.plugin.readTwitchLinks()
	if err != nil {
		return nil, err
	}

	s.lock.Lock()
	defer s.lock.Unlock()

	if s.links == nil {
		s.links = doc
	}

	return s.links, nil
}

// owners maps each connected Twitch id to the Mattermost user it belongs to,
// for the boards and the medals. Nil with the switch off.
func (s *twitchStore) owners(cfg *configuration) (map[string]string, error) {
	if !cfg.twitchLiveOn() {
		return nil, nil
	}

	links, err := s.cachedLinks()
	if err != nil {
		return nil, err
	}

	owners := make(map[string]string, len(links.Users))
	for userID, link := range links.Users {
		owners[link.ID] = userID
	}

	return owners, nil
}

// peopleIfOn is everybody connected, by Mattermost user id, 24/7 channels
// included so that paid spans stay exact when the flag is cleared. Empty, and
// read from nowhere, while the switch is off.
func (s *twitchStore) peopleIfOn(cfg *configuration) (map[string]bool, error) {
	if !cfg.twitchLiveOn() {
		return map[string]bool{}, nil
	}

	links, err := s.cachedLinks()
	if err != nil {
		return nil, err
	}

	people := make(map[string]bool, len(links.Users))
	for userID := range links.Users {
		people[userID] = true
	}

	return people, nil
}

// connectedID is the Twitch id somebody has connected, from the document as
// last read, and whether that id is free of a pending purge.
func (s *twitchStore) connectedID(userID string) (string, bool) {
	s.lock.Lock()
	defer s.lock.Unlock()

	if s.links == nil {
		return "", false
	}

	link, connected := s.links.Users[userID]
	if !connected {
		return "", false
	}
	_, purging := s.links.Purge[link.ID]

	return link.ID, !purging
}

// twitchLiveFor is whether somebody is live on Twitch right now, as the store
// last heard. Always false while the switch is off.
func (s *twitchStore) twitchLiveFor(userID string) bool {
	if userID == "" || !s.plugin.getConfiguration().twitchLiveOn() {
		return false
	}

	id, _ := s.connectedID(userID)
	if id == "" {
		return false
	}

	_, live := s.liveEntry(id, s.now())

	return live
}

// noteLinks replaces the cached document after a read or a successful write.
func (s *twitchStore) noteLinks(doc *twitchLinks) {
	if doc == nil {
		return
	}

	s.lock.Lock()
	recovered := s.linksBad
	s.links = doc
	s.linksBad = false
	if s.problem == twitchLinksProblem {
		s.problem = ""
	}
	s.lock.Unlock()

	// While the document would not decode, Broadcast Box was paid in full
	// with no paid spans kept, and Twitch was credited nothing. Sessions
	// running now earn nothing for that stretch, once each, as when the
	// switch goes on: backdating it would pay a simulcast twice.
	if recovered {
		s.plugin.armTwitchResync(s.now().UnixMilli())
	}
}

// forgetLive drops one stream and its picture now, so the very next poll has
// no card for somebody who has just disconnected.
func (s *twitchStore) forgetLive(twitchID string) {
	s.lock.Lock()
	defer s.lock.Unlock()

	delete(s.live, twitchID)
	delete(s.pictures, twitchID)
}

// clearLive drops the live list, the pictures, and the failure and rate
// state. The cached document goes too only for Delete all Twitch data.
func (s *twitchStore) clearLive(links bool) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.live = map[string]twitchLive{}
	s.pictures = map[string]*twitchPicture{}
	s.fails = 0
	s.problem = ""
	s.rate = twitchRate{limit: -1, remaining: -1}
	s.nextAt = time.Time{}
	s.holding = false
	s.lastTry, s.lastGood = time.Time{}, time.Time{}
	s.namesRetryAt = time.Time{}

	if links {
		s.links = nil
		s.linksBad = false
	}
}

// status is what the admin route and /me show about the loop.
func (s *twitchStore) status() twitchStatus {
	s.lock.Lock()
	defer s.lock.Unlock()

	live := 0
	for _, stream := range s.live {
		if stream.Missed <= twitchMissedReads {
			live++
		}
	}

	return twitchStatus{lastTry: s.lastTry, lastGood: s.lastGood, problem: s.problem, live: live}
}

// twitchStreams builds this poll's Twitch entries for the stream list, from
// memory and the link document as last read. It makes no Twitch request and
// no KV read, apart from the link and token reads used for redaction.
//
// Nil at once while the switch is off, before anything else, so a server
// without Twitch asks nothing new of anybody on any poll.
func (p *Plugin) twitchStreams(cfg *configuration, now time.Time) []apiStream {
	if !cfg.twitchLiveOn() {
		return nil
	}

	// What is live is judged by the store's own clock, which is the one its
	// reads were stamped with; the poll's instant only versions the pictures.
	s := p.twitchLive()
	live := s.liveNow(s.now())
	if len(live) == 0 {
		return nil
	}

	s.lock.Lock()
	links := s.links
	s.lock.Unlock()
	if links == nil {
		return nil
	}

	owners := make(map[string]string, len(links.Users))
	for userID, link := range links.Users {
		owners[link.ID] = userID
	}

	streams := make([]apiStream, 0, len(live))
	for _, stream := range live {
		owner := owners[stream.TwitchID]
		if owner == "" {
			continue
		}
		link := links.Users[owner]

		// The login that drives the player and every Watch on Twitch link is
		// the one this read gave, checked once more here; never the one in
		// the link document, which a failed write or a rename could leave
		// naming somebody else's channel.
		if !twitchLoginPattern.MatchString(stream.Login) {
			continue
		}

		handle := p.twitchHandle(stream.TwitchID)
		if handle == "" {
			continue
		}

		user, appErr := p.API.GetUser(owner)
		if appErr != nil || user == nil || user.DeleteAt != 0 {
			continue
		}

		entry := apiStream{
			Handle:         handle,
			Source:         sourceTwitch,
			UserID:         user.Id,
			Username:       user.Username,
			DisplayName:    displayNameOf(user),
			LinkSource:     linkSourceTwitch,
			TwitchLogin:    stream.Login,
			TwitchID:       stream.TwitchID,
			TwitchCategory: stream.Category,
			AlwaysOn:       link.AllDay,
			Viewers:        max(stream.Viewers, 0),
		}
		entry.Title = entry.DisplayName

		// Twitch's title, with the owner's publish token and stream key taken
		// out, as decorate does for a note written outside the panel: some
		// people paste the same text everywhere.
		motd := cleanText(stream.Title, maxMOTDLength)
		if token := p.getLinkedToken(owner); token != "" {
			motd = redactStreamKey(motd, token, entry.Title)
		}
		if key := p.getLinkedKey(owner); key != "" {
			motd = redactStreamKey(motd, key, entry.Title)
		}
		if utf8.RuneCountInString(motd) > maxMOTDLength {
			motd = string([]rune(motd)[:maxMOTDLength])
		}
		entry.MOTD = strings.TrimSpace(motd)

		started, startErr := time.Parse(time.RFC3339Nano, stream.StartedAt)
		if startErr == nil {
			entry.StreamStart = stream.StartedAt
		}

		if cfg.LivePreviews && stream.Thumb != "" {
			startedAt := int64(0)
			if startErr == nil {
				startedAt = started.Unix()
			}
			entry.PreviewVersion = fmt.Sprintf("%d-%d", startedAt, now.Unix()/300)
		}

		streams = append(streams, entry)
	}

	if len(streams) == 0 {
		return nil
	}

	return streams
}
