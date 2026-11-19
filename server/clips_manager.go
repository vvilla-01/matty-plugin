package main

import (
	"context"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// The recorder manager (spec §2.4.5–§2.4.8, §2.12–§2.14; A§2.1, A§3.3, A§3.5,
// A§4.14): which live streams are recorded for clipping, and what is still
// held of the ones that stopped.
//
// Every live stream that is not a 24/7 channel, and whose streamer has not
// switched clipping of it off, is recorded, up to maxRecorders at once. A
// recorder keeps its slot for as long as its stream stays on the air, whatever
// order the snapshot puts it in: somebody who tuned in to another stream ten
// seconds ago must never take a running recorder's slot and throw away what it
// holds. A slot that frees goes to the most watched stream waiting for one.
//
// When a stream goes off the air for good — gone from the listing for the
// offline grace, counted only on polls that reached Broadcast Box, so a hiccup
// never tears anything down — its recorder stops, and what it held becomes a
// TAIL: the stream's last minutes, which the people who were watching may
// still clip for ten minutes. A recorder hands over tails of its own when a
// stream drops and carries on. One per stream, at most maxTails, and never
// more on the disk between rings and tails than ringBudgetBytes, the oldest
// going first.
//
// The manager outlives the poller, which every settings save restarts and
// which must not drop what is recorded. It is made on first use, like the
// clips store, and making it starts nothing and asks nothing: startClips gives
// it somewhere to record, and stopClips stops it.
//
// What the panels are told about it — each stream's recording state, and the
// tails — is read from here by addClips, from memory alone, so a failed poll
// and a clips republish say what a good poll does.

// Why a recorder stopped, as the log says it.
const (
	whyStreamEnded    = "the stream went off the air"
	whyOptedOut       = "its streamer switched clipping off"
	whyNotRecordable  = "it is a 24/7 channel now"
	whyClipsOff       = "Clips was switched off"
	whyNoStorage      = "there is nowhere to keep clips"
	whyHalted         = "the stored clips could not be read"
	whyLowSpace       = "the disk is nearly full"
	whyURLChanged     = "the Broadcast Box URL changed"
	whyUnconfigured   = "Broadcast Box is not set up"
	whyPluginStopping = "the plugin is stopping"
)

const (
	// What an admin is told about a stream that cannot be recorded for want
	// of room.
	fullDiskReason   = "the disk is nearly full"
	fullBudgetReason = "the recordings have used all the room they are allowed"

	// How long every recorder must have been failing before admins are told
	// that Clips cannot reach Broadcast Box's media port (§2.12).
	recorderAlertAfter = 5 * time.Minute
)

// clipRecorderHandle is what the manager drives a recorder through: the
// recorder of clips_recorder.go, or a test's stand-in, which moves only when
// the test says, so that the manager's own rules are tested without WebRTC or
// real time.
type clipRecorderHandle interface {
	start(ctx context.Context, stream apiStream, cfg *configuration)
	update(stream apiStream)
	setWindow(window time.Duration)
	wipe()
	stop(ctx context.Context, keep bool) (clipTail, bool)

	state() string
	clipFrom() time.Time
	reason() string
	bitrate() float64
	heldSeconds() int64
	live() clipLive
}

// recorderSlot is one running recorder and the stream it records.
type recorderSlot struct {
	handle string
	rec    clipRecorderHandle

	// The stream as the latest poll had it, for its name in the System
	// Console; its key is never logged or sent. tag is the stream's tag, for
	// its streamer's switch.
	stream apiStream
	tag    string

	// Successful ticks in a row that have not listed the stream.
	missing int

	// Set under mu when the manager lets the recorder go without keeping
	// what it holds: a tail it hands over on the way out is deleted rather
	// than kept.
	dropTails bool
}

// heldTail is a tail the manager holds, and the tag of its stream.
type heldTail struct {
	tail clipTail
	tag  string
}

// waitingStream is a live stream that could be recorded but is not: queued
// behind the recorders running, full for want of room, or switched off by its
// streamer.
type waitingStream struct {
	state  string
	stream apiStream
	tag    string
	reason string
}

// recorderStop is a recorder the manager has let go of, to be stopped.
type recorderStop struct {
	slot *recorderSlot
	keep bool
	why  string
}

type clipRecorders struct {
	p *Plugin

	// control is held by whatever starts, stops or wipes recorders —
	// reconcile, stopAll, an opt-out, a wipe, the housekeeping — so two of
	// them never decide about the same recorder at once. It is held while
	// recorders stop, which can take seconds; nothing a recorder calls back
	// into ever takes it. runCtx, the context recorders run under from
	// startClips to stopClips, is under it too.
	control   sync.Mutex
	runCtx    context.Context
	runCancel context.CancelFunc

	// mu covers what follows. It is held for moments only, and never across
	// a call that waits on a recorder: a recorder's events take it, and a
	// recorder being stopped waits for its events.
	mu      sync.Mutex
	slots   map[string]*recorderSlot
	tails   map[string]*heldTail
	waiting map[string]waitingStream
	window  time.Duration

	// idle is set once stopAll has left nothing running and nothing held,
	// and cleared when a recorder starts: stopAll runs on every poll while
	// Clips is off or Broadcast Box is not set up, and must then do nothing
	// at all.
	idle bool

	// url is the Broadcast Box what is recorded and held came from, from the
	// first recorder started after everything last stopped. Under control.
	url string

	// lowSpace is set while the disk is below recorderStopFree, when nothing
	// is recorded.
	lowSpace bool

	// Since when every recorder running has been failing, and why the
	// latest of them says it is: for the admins' alert.
	failingSince  time.Time
	failingReason string

	// One pion API and one set of the sessions recorders have opened, shared
	// by every recorder (clips_recorder.go).
	peers *clipPeerAPI
	own   *clipOwnSessions

	// newRecorder makes a recorder for a stream, recording into dir; a test
	// puts a stand-in behind it. stopWait is how long stopping recorders may
	// take all together, and budget the most rings and tails may hold.
	newRecorder func(handle, dir string, onEvent func(recorderEvent)) clipRecorderHandle
	stopWait    time.Duration
	budget      int64
}

func newClipRecorders(p *Plugin) *clipRecorders {
	m := &clipRecorders{
		p:        p,
		slots:    map[string]*recorderSlot{},
		tails:    map[string]*heldTail{},
		idle:     true,
		peers:    newClipPeerAPI(),
		own:      &clipOwnSessions{},
		stopWait: recorderCloseWait,
		budget:   ringBudgetBytes,
	}
	m.newRecorder = func(handle, dir string, onEvent func(recorderEvent)) clipRecorderHandle {
		return p.newClipRecorder(handle, dir, m.peers, m.own, onEvent)
	}

	return m
}

// locked runs f with mu held, and lets it go by a defer. It is for the spans
// under mu that call into the rings: a panic in one, which ServeHTTP or a
// loop's recover turns into one refused request or one failed pass, must not
// leave mu held and every clip and recorder waiting for it for good.
func (m *clipRecorders) locked(f func()) {
	m.mu.Lock()
	defer m.mu.Unlock()

	f()
}

// recorders is the manager, made on first use. Making it calls nothing, so
// that the snapshot of a plugin whose Clips never started can still ask it.
func (p *Plugin) recorders() *clipRecorders {
	p.recordersOnce.Do(func() {
		p.recordersState = newClipRecorders(p)
	})

	return p.recordersState
}

// begin is startClips's: recorders may start from here on.
func (m *clipRecorders) begin() {
	m.control.Lock()
	defer m.control.Unlock()

	if m.runCancel != nil {
		m.runCancel()
	}
	m.runCtx, m.runCancel = context.WithCancel(context.Background())
}

// shutdown is stopClips's, and startClips's for an activation that came with
// no deactivation before it: every recorder stops, within the five seconds
// stopping may take all together (§2.14), and nothing starts again until
// begin. A recorder that did not stop in time ends when its context does,
// keeping nothing.
func (m *clipRecorders) shutdown(now time.Time) {
	m.control.Lock()
	defer m.control.Unlock()

	m.stopAllLocked(false, whyPluginStopping, now)
	if m.runCancel != nil {
		m.runCancel()
	}
	m.runCtx, m.runCancel = nil, nil
}

// reconcile brings the recorders into line with the streams a successful poll
// has just listed (spec §2.4.5, A§3.5). It only changes the manager; addClips
// tells the panels.
//
//  1. Nothing records while Clips is switched off, while there is nowhere to
//     keep clips, while the stored clips cannot be read, or while the disk is
//     nearly full: every recorder stops, and every tail goes.
//  2. A stream whose streamer has switched clipping off — by its stream's
//     tag, however it is attributed — is not recorded, and what was held of
//     it goes, its drafts included, as when the switch is pressed.
//  3. A 24/7 channel is never recorded, and a stream that becomes one stops
//     with nothing kept.
//  4. Running recorders keep their slots; free ones go to the most watched
//     streams waiting, then the longest on the air, then by handle. A stream
//     there is no slot for is queued, and one there is no room for is full.
//  5. A recorder whose stream the poll did not list counts a miss, and once
//     it has missed the offline grace, stops, its end kept as a tail. Only a
//     tick counts a miss (ticked is pollOnce's): a poll somebody's click
//     asked for, a moment after one, would otherwise stop a recording while
//     OBS was still reconnecting. Any poll that lists the stream again starts
//     the count afresh.
//
// Only the settings as they are now are acted on. A poll reads them once, as
// it starts, and a save can land while it is out asking Broadcast Box: that
// save has already stopped what it had to (OnConfigurationChange stores the
// settings first, then stops recorders under control), and a poll that went
// on with the settings it began with would start recording again, from a
// Broadcast Box that is no longer the one, or after Clips went off. Such a
// poll leaves the recorders alone; the poller the save restarts decides.
func (m *clipRecorders) reconcile(streams []apiStream, cfg *configuration, now time.Time, ticked bool) {
	s := m.p.clips()

	m.control.Lock()
	defer m.control.Unlock()

	if cfg != m.p.getConfiguration() {
		return
	}

	root := s.writableRoot()
	halted, _ := s.halt.state()
	switch {
	case !cfg.EnableClips:
		m.stopAllLocked(true, whyClipsOff, now)
		return
	case root == "" || m.runCtx == nil:
		m.stopAllLocked(false, whyNoStorage, now)
		return
	case halted:
		m.stopAllLocked(false, whyHalted, now)
		return
	}

	// What was recorded belongs to the Broadcast Box it came from (§2.14).
	// The save that changes it stops everything itself; this is so that a
	// recorder of the old one can never outlive it, whatever order things
	// happen in.
	if m.url != "" && m.url != cfg.BroadcastBoxURL {
		m.stopAllLocked(false, whyURLChanged, now)
	}

	if free, _, known := s.freeSpace(root); known && free < recorderStopFree {
		if streams == nil {
			streams = []apiStream{}
		}
		m.stopForSpaceLocked(streams, free)
		return
	}
	m.roomAgainLocked()

	// Who has switched their stream off, from this node's copy, read again
	// when it is more than a minute old so another node's switch arrives.
	// Never read at all is not known: nothing new is recorded until it is.
	doc, err := m.p.clipsDocument(now)
	if err != nil {
		doc = s.cached()
	}
	switchedOff := func(tag string) bool {
		if doc == nil || tag == "" {
			return false
		}
		_, off := doc.OptOut[tag]

		return off
	}

	listed := map[string]bool{}
	eligible := map[string]apiStream{}
	off := map[string]apiStream{}
	for _, stream := range streams {
		if stream.Handle == "" {
			continue
		}
		listed[stream.Handle] = true

		// Never a Twitch stream (design/twitch-live.md §9): Twitch's video
		// may only be shown through Twitch's own player, so it never takes
		// a slot, never opens a session and never reaches the disk. The
		// poller does not hand one over; this is the second guard.
		if stream.Source != "" || stream.AlwaysOn || stream.StreamKey == "" || !validClipHandle(stream.Handle) {
			continue
		}
		if switchedOff(m.p.streamTag(stream.StreamKey)) {
			off[stream.Handle] = stream
			continue
		}
		eligible[stream.Handle] = stream
	}

	grace := max(cfg.OfflineGraceChecks, 1)
	window := cfg.ringWindow()

	type update struct {
		rec    clipRecorderHandle
		stream apiStream
	}

	var stops []recorderStop
	var updates []update
	var goneRings []*ring
	wipedTags := map[string]bool{}

	var windowChanged bool
	m.locked(func() {
		windowChanged = m.window != window
		m.window = window

		for handle, slot := range m.slots {
			stream, wanted := eligible[handle]
			switch {
			case wanted:
				slot.missing = 0
				slot.stream = stream
				updates = append(updates, update{rec: slot.rec, stream: stream})
				continue
			case off[handle].Handle != "" || switchedOff(slot.tag):
				slot.dropTails = true
				stops = append(stops, recorderStop{slot: slot, why: whyOptedOut})
				wipedTags[slot.tag] = true
			case listed[handle]:
				slot.dropTails = true
				stops = append(stops, recorderStop{slot: slot, why: whyNotRecordable})
			case !ticked:
				continue
			default:
				slot.missing++
				if slot.missing < grace {
					continue
				}
				stops = append(stops, recorderStop{slot: slot, keep: true, why: whyStreamEnded})
			}
		}
		for _, stop := range stops {
			delete(m.slots, stop.slot.handle)
		}

		for handle, held := range m.tails {
			expired := !now.Before(held.tail.until) || held.tail.ring.isDeleted()
			optedOut := switchedOff(held.tag)
			if !expired && !optedOut {
				continue
			}
			if optedOut {
				wipedTags[held.tag] = true
			}
			delete(m.tails, handle)
			goneRings = append(goneRings, held.tail.ring)
		}
	})

	for _, next := range updates {
		next.rec.update(next.stream)
		if windowChanged {
			next.rec.setWindow(window)
		}
	}
	m.stopRecorders(stops)
	removeRings(goneRings)

	// What its streamer switched off goes as it would have from the switch:
	// a switch pressed on another node reaches this one's drafts here.
	for tag := range wipedTags {
		if tag != "" {
			m.p.wipeDrafts(func(draft *clipDraft) bool { return draft.capture.StreamTag == tag }, now)
		}
	}

	waiting := map[string]waitingStream{}
	for handle, stream := range off {
		waiting[handle] = waitingStream{state: clipStateOff, stream: stream, tag: m.p.streamTag(stream.StreamKey)}
	}

	if doc != nil {
		m.startWaitingLocked(eligible, waiting, cfg, root, now)
	}

	m.mu.Lock()
	m.waiting = waiting
	m.mu.Unlock()

	m.noteHealthLocked(now)
}

// startWaitingLocked gives each free slot to a stream that has none, the most
// watched first, and says of the rest why they are not recorded. Under
// control.
func (m *clipRecorders) startWaitingLocked(eligible map[string]apiStream, waiting map[string]waitingStream, cfg *configuration, root string, now time.Time) {
	m.mu.Lock()
	var candidates []apiStream
	for handle, stream := range eligible {
		if m.slots[handle] == nil {
			candidates = append(candidates, stream)
		}
	}
	running := len(m.slots)
	m.mu.Unlock()

	sortCandidates(candidates)

	for _, stream := range candidates {
		tag := m.p.streamTag(stream.StreamKey)
		if running >= maxRecorders {
			waiting[stream.Handle] = waitingStream{state: clipStateQueued, stream: stream, tag: tag}
			continue
		}
		if reason := m.roomForRecorderLocked(stream, cfg.ringWindow(), root, now); reason != "" {
			waiting[stream.Handle] = waitingStream{state: clipStateFull, stream: stream, tag: tag, reason: reason}
			continue
		}

		m.startLocked(stream, tag, cfg, root)
		running++
	}
}

// sortCandidates puts the streams waiting for a slot in the order slots go to
// them: the most viewers first, then the longest on the air, a start that
// cannot be read going after every one that can, then by handle, so that two
// nodes, or two polls, always agree.
func sortCandidates(candidates []apiStream) {
	started := func(stream apiStream) (time.Time, bool) {
		at, err := time.Parse(time.RFC3339Nano, stream.StreamStart)

		return at, err == nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.Viewers != b.Viewers {
			return a.Viewers > b.Viewers
		}

		aAt, aKnown := started(a)
		bAt, bKnown := started(b)
		if aKnown != bKnown {
			return aKnown
		}
		if aKnown && !aAt.Equal(bAt) {
			return aAt.Before(bAt)
		}

		return a.Handle < b.Handle
	})
}

// startLocked starts recording a stream into ring/<handle>. Under control.
func (m *clipRecorders) startLocked(stream apiStream, tag string, cfg *configuration, root string) {
	slot := &recorderSlot{handle: stream.Handle, stream: stream, tag: tag}
	dir := filepath.Join(root, clipsRingDir, stream.Handle)
	slot.rec = m.newRecorder(stream.Handle, dir, func(event recorderEvent) {
		m.onEvent(slot, event)
	})

	m.mu.Lock()
	m.slots[stream.Handle] = slot
	m.idle = false
	m.mu.Unlock()
	m.url = cfg.BroadcastBoxURL

	slot.rec.start(m.runCtx, stream, cfg)
}

// roomForRecorderLocked says why a new recorder for stream cannot start, or
// "" when it can: room on the disk above its floor, and room in the budget,
// for the most its ring will hold, once tails that have run out are gone and,
// if need be, the oldest of the rest. Under control.
//
// A tail goes only when the tails held could make the room between them. The
// disk's floor is a share of the whole volume, far more than a few minutes of
// video can ever free, and a stream left waiting as full would otherwise take
// every other stream's held end at every poll, for nothing: the people who
// were watching would lose "clip the end", and the waiting stream would wait
// all the same.
func (m *clipRecorders) roomForRecorderLocked(stream apiStream, window time.Duration, root string, now time.Time) string {
	need := ringCap(window, float64(max(stream.Bitrate, 0))/8)

	var gone []*ring
	var budget int64
	m.locked(func() {
		gone = m.expireTailsLocked(now)
		budget = m.budget
	})
	removeRings(gone)

	for {
		committed := m.committedBytes(window)

		// How much more there must be, in the budget and on the disk.
		var budgetShort, diskShort int64
		if over := committed + need - budget; over > 0 {
			budgetShort = over
		}
		if free, total, known := m.p.clips().freeSpace(root); known {
			if wanted := uint64(need) + clipFreeFloor(total); free < wanted {
				diskShort = int64(min(wanted-free, uint64(1<<62)))
			}
		}

		reason := ""
		switch {
		case budgetShort > 0:
			reason = fullBudgetReason
		case diskShort > 0:
			reason = fullDiskReason
		default:
			return ""
		}

		oldest := ""
		m.locked(func() {
			held := int64(0)
			for _, tail := range m.tails {
				held += tail.tail.ring.size()
			}
			if held >= max(budgetShort, diskShort) {
				oldest = m.oldestTailLocked()
			}
			if oldest != "" {
				gone = []*ring{m.tails[oldest].tail.ring}
				delete(m.tails, oldest)
			}
		})

		if oldest == "" {
			return reason
		}
		removeRings(gone)
	}
}

// committedBytes is what the rings and tails hold or may grow to: each
// running ring at the larger of its size and its cap, one not recording yet
// at the cap its stream's bitrate gives it, and each tail at its size.
func (m *clipRecorders) committedBytes(window time.Duration) int64 {
	m.mu.Lock()
	type running struct {
		rec  clipRecorderHandle
		rate int64
	}
	slots := make([]running, 0, len(m.slots))
	for _, slot := range m.slots {
		slots = append(slots, running{rec: slot.rec, rate: slot.stream.Bitrate})
	}
	m.mu.Unlock()
	_, tails := m.held()

	total := int64(0)
	for _, slot := range slots {
		if ring := slot.rec.live().ring; ring != nil {
			status := ring.status()
			total += max(status.bytes, status.capBytes)
		} else {
			total += ringCap(window, float64(max(slot.rate, 0))/8)
		}
	}
	for _, held := range tails {
		total += held.tail.ring.size()
	}

	return total
}

// held is the recorders running and the tails held, as they are now.
func (m *clipRecorders) held() ([]*recorderSlot, []*heldTail) {
	m.mu.Lock()
	defer m.mu.Unlock()

	slots := make([]*recorderSlot, 0, len(m.slots))
	for _, slot := range m.slots {
		slots = append(slots, slot)
	}
	tails := make([]*heldTail, 0, len(m.tails))
	for _, held := range m.tails {
		tails = append(tails, held)
	}

	return slots, tails
}

// onEvent is a recorder's news, on its goroutine: its state moved, or it
// handed over a tail. Either way the panels hear of it now, rather than at
// the next poll, through the clips republish.
func (m *clipRecorders) onEvent(slot *recorderSlot, event recorderEvent) {
	if event.kind == recorderTailKept && event.tail != nil {
		m.keepTail(slot, *event.tail)
	}

	m.p.republishClips()
}

// keepTail holds a tail a recorder has handed over, unless it is not to be
// kept: deleted already by a wipe that landed as it was handed over, from a
// recorder the manager let go of without keeping anything, or too short to
// clip. It replaces any tail of the same stream, and the oldest goes when
// there are too many, or too much held on the disk.
func (m *clipRecorders) keepTail(slot *recorderSlot, tail clipTail) {
	if tail.ring == nil {
		return
	}

	var gone []*ring

	m.locked(func() {
		switch {
		case tail.ring.isDeleted():
		case slot.dropTails || tail.ring.heldMs() < tailMinHeld.Milliseconds():
			gone = append(gone, tail.ring)
		default:
			gone = m.holdTailLocked(slot.handle, &heldTail{tail: tail, tag: slot.tag})
		}
	})

	removeRings(gone)
	m.enforceBudget()
}

// holdTailLocked keeps a tail for a stream, and answers the rings that go to
// make way for it: the stream's older tail, and the oldest of the rest when
// there are more than maxTails. Under mu.
func (m *clipRecorders) holdTailLocked(handle string, held *heldTail) []*ring {
	var gone []*ring
	if previous := m.tails[handle]; previous != nil && previous.tail.ring != held.tail.ring {
		gone = append(gone, previous.tail.ring)
	}
	m.tails[handle] = held

	for len(m.tails) > maxTails {
		oldest := m.oldestTailLocked()
		gone = append(gone, m.tails[oldest].tail.ring)
		delete(m.tails, oldest)
	}

	return gone
}

// oldestTailLocked is the handle of the tail that ended first, or "" when
// none is held. Under mu.
func (m *clipRecorders) oldestTailLocked() string {
	oldest := ""
	var oldestEnd time.Time
	for handle, held := range m.tails {
		end := held.tail.ring.endTime()
		if oldest == "" || end.Before(oldestEnd) || (end.Equal(oldestEnd) && handle < oldest) {
			oldest, oldestEnd = handle, end
		}
	}

	return oldest
}

// expireTailsLocked lets go of every tail past its time, or deleted since it
// was handed over, and answers their rings for the caller to delete. Under
// mu.
func (m *clipRecorders) expireTailsLocked(now time.Time) []*ring {
	var gone []*ring
	for handle, held := range m.tails {
		if now.Before(held.tail.until) && !held.tail.ring.isDeleted() {
			continue
		}
		delete(m.tails, handle)
		gone = append(gone, held.tail.ring)
	}

	return gone
}

// enforceBudget deletes tails, the oldest first, while the rings and tails
// together hold more than the budget.
func (m *clipRecorders) enforceBudget() {
	slots, _ := m.held()

	liveBytes := int64(0)
	for _, slot := range slots {
		if ring := slot.rec.live().ring; ring != nil {
			liveBytes += ring.size()
		}
	}

	var gone []*ring

	m.locked(func() {
		for len(m.tails) > 0 {
			tailBytes := int64(0)
			for _, held := range m.tails {
				tailBytes += held.tail.ring.size()
			}
			if liveBytes+tailBytes <= m.budget {
				break
			}

			oldest := m.oldestTailLocked()
			gone = append(gone, m.tails[oldest].tail.ring)
			delete(m.tails, oldest)
		}
	})

	removeRings(gone)
}

// removeRings deletes rings the manager has let go of. A ring an export is
// reading goes when the export lets go of it (ring.remove).
func removeRings(rings []*ring) {
	for _, r := range rings {
		if r != nil {
			r.remove()
		}
	}
}

// stopRecorders stops recorders the manager has let go of, all at once and
// within stopWait all together, and keeps the ends of those it was asked to.
// Under control, never under mu: a recorder's last events take it.
func (m *clipRecorders) stopRecorders(stops []recorderStop) {
	if len(stops) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.stopWait)
	defer cancel()

	var stopping sync.WaitGroup
	for _, order := range stops {
		stopping.Add(1)
		// A stop that panics is logged and goes no further: the others still
		// stop, and the defer gives its place in the wait back (recovery.go).
		// What it would have handed over is lost with it, as a tail its ring
		// could not finish would be.
		go func() {
			defer stopping.Done()

			safely(m.p.API, "stopping a clip recorder", func() {
				tail, held := order.slot.rec.stop(ctx, order.keep)
				if held {
					m.keepTail(order.slot, tail)
				}
			})
		}()
	}
	stopping.Wait()

	for _, order := range stops {
		m.p.API.LogInfo("A clip recorder stopped", "stream_handle", order.slot.handle, "reason", order.why)
	}
}

// stopAll stops every recorder, deletes every tail and empties ring/, keeping
// nothing; with dropDrafts, for Clips switched off, it also takes away every
// draft (see clipsTurnedOff). It does
// nothing at all — no call to Mattermost, no file touched — when nothing is
// running or held, which is every poll while Clips is off or Broadcast Box is
// not set up, and a store that never started.
func (m *clipRecorders) stopAll(dropDrafts bool, why string, now time.Time) {
	m.control.Lock()
	defer m.control.Unlock()

	m.stopAllLocked(dropDrafts, why, now)
}

func (m *clipRecorders) stopAllLocked(dropDrafts bool, why string, now time.Time) {
	m.mu.Lock()
	wasIdle := m.idle && len(m.slots) == 0 && len(m.tails) == 0
	var stops []recorderStop
	var gone []*ring
	for handle, slot := range m.slots {
		slot.dropTails = true
		stops = append(stops, recorderStop{slot: slot, why: why})
		delete(m.slots, handle)
	}
	for handle, held := range m.tails {
		gone = append(gone, held.tail.ring)
		delete(m.tails, handle)
	}
	m.waiting = nil
	m.idle = true
	m.failingSince, m.failingReason = time.Time{}, ""
	m.mu.Unlock()
	m.url = ""

	if !wasIdle {
		m.stopRecorders(stops)
		removeRings(gone)

		// Whatever a recorder that would not stop in time left behind, and
		// nothing at all without a root to build the path from.
		if err := m.p.clips().wipeClipsDir(clipsRingDir); err != nil {
			m.p.API.LogWarn("Could not empty a clips directory", "directory", clipsRingDir, "error", err.Error())
		}

		m.p.republishClips()
	}

	if dropDrafts {
		m.p.forgetClipDrafts(now)
	}
}

// stopForSpaceLocked is the disk falling below recorderStopFree, with free
// bytes left: every recorder stops and every tail goes, nothing kept, and
// every stream that would be recorded is full until there is room again —
// those of a poll's listing, or, from the housekeeping, those the last poll
// listed. Said once, when it happens. Under control.
func (m *clipRecorders) stopForSpaceLocked(streams []apiStream, free uint64) {
	fromPoll := streams != nil
	waiting := map[string]waitingStream{}

	m.mu.Lock()
	first := !m.lowSpace
	m.lowSpace = true

	var stops []recorderStop
	var gone []*ring
	for handle, slot := range m.slots {
		slot.dropTails = true
		stops = append(stops, recorderStop{slot: slot, why: whyLowSpace})
		waiting[handle] = waitingStream{state: clipStateFull, stream: slot.stream, tag: slot.tag, reason: fullDiskReason}
		delete(m.slots, handle)
	}
	for handle, held := range m.tails {
		gone = append(gone, held.tail.ring)
		delete(m.tails, handle)
	}
	if !fromPoll {
		for handle, previous := range m.waiting {
			if previous.state != clipStateOff {
				previous.state, previous.reason = clipStateFull, fullDiskReason
			}
			waiting[handle] = previous
		}
	}
	m.failingSince, m.failingReason = time.Time{}, ""
	m.mu.Unlock()

	if first {
		m.p.API.LogWarn("Clips stopped recording because the disk is nearly full", "free_bytes", free)
	}

	m.stopRecorders(stops)
	removeRings(gone)

	if fromPoll {
		waiting = map[string]waitingStream{}
		doc := m.p.clips().cached()
		for _, stream := range streams {
			if stream.Handle == "" || stream.Source != "" || stream.AlwaysOn || stream.StreamKey == "" || !validClipHandle(stream.Handle) {
				continue
			}

			tag := m.p.streamTag(stream.StreamKey)
			if _, off := doc.optedOut(tag); off {
				waiting[stream.Handle] = waitingStream{state: clipStateOff, stream: stream, tag: tag}
				continue
			}
			waiting[stream.Handle] = waitingStream{state: clipStateFull, stream: stream, tag: tag, reason: fullDiskReason}
		}
	}

	m.mu.Lock()
	m.waiting = waiting
	m.mu.Unlock()

	if len(stops) > 0 || len(gone) > 0 {
		m.p.republishClips()
	}
}

// roomAgainLocked notes that the disk has room again, once.
func (m *clipRecorders) roomAgainLocked() {
	m.mu.Lock()
	recovered := m.lowSpace
	m.lowSpace = false
	m.mu.Unlock()

	if recovered {
		m.p.API.LogInfo("Clips can record again: the disk has room")
	}
}

// optedOut is whether a stream's streamer has switched clipping of it off,
// and since when. A document never read switches nothing off.
func (doc *clipsDoc) optedOut(tag string) (clipMark, bool) {
	if doc == nil || tag == "" {
		return clipMark{}, false
	}
	mark, off := doc.OptOut[tag]

	return mark, off
}

// optOut is a streamer switching clipping of their stream off (A§3.3, A§3.5):
// the stream with this tag stops being recorded at once, and what is held of
// it — its ring and its tail — goes, nothing kept. Its drafts are the
// caller's to take away. Until the next poll, the stream says off.
func (m *clipRecorders) optOut(tag string, now time.Time) {
	if tag == "" {
		return
	}

	m.control.Lock()
	defer m.control.Unlock()

	var stops []recorderStop
	var gone []*ring

	m.mu.Lock()
	for handle, slot := range m.slots {
		if slot.tag != tag {
			continue
		}
		slot.dropTails = true
		stops = append(stops, recorderStop{slot: slot, why: whyOptedOut})
		delete(m.slots, handle)
		if m.waiting == nil {
			m.waiting = map[string]waitingStream{}
		}
		m.waiting[handle] = waitingStream{state: clipStateOff, stream: slot.stream, tag: tag}
	}
	for handle, held := range m.tails {
		if held.tag != tag {
			continue
		}
		gone = append(gone, held.tail.ring)
		delete(m.tails, handle)
	}
	for handle, waiting := range m.waiting {
		if waiting.tag == tag && waiting.state != clipStateOff {
			waiting.state, waiting.reason = clipStateOff, ""
			m.waiting[handle] = waiting
		}
	}
	m.mu.Unlock()

	m.stopRecorders(stops)
	removeRings(gone)
	m.noteHealthLocked(now)
}

// wipe deletes what is held of one stream, for its streamer or an admin
// (§2.10.6 steps 1 and 2): its live ring is cut with nothing kept, recording
// carrying on from the next keyframe, and its tail goes. A tail being handed
// over as the wipe lands is deleted by the recorder itself. Its drafts are
// the caller's to take away.
func (m *clipRecorders) wipe(handle string) {
	m.control.Lock()
	defer m.control.Unlock()

	m.mu.Lock()
	slot := m.slots[handle]
	m.mu.Unlock()

	if slot != nil {
		slot.rec.wipe()
	}

	m.mu.Lock()
	held := m.tails[handle]
	delete(m.tails, handle)
	m.mu.Unlock()

	if held != nil {
		held.tail.ring.remove()
	}
}

// setWindow is the Clip length changing: running recorders take it at their
// next segment. Under control, so that it comes after any reconcile already
// starting recorders: one started with the old length in between would never
// be told otherwise, since every later reconcile finds the length unchanged.
func (m *clipRecorders) setWindow(window time.Duration) {
	m.control.Lock()
	defer m.control.Unlock()

	m.mu.Lock()
	m.window = window
	m.mu.Unlock()

	slots, _ := m.held()
	for _, slot := range slots {
		slot.rec.setWindow(window)
	}
}

// housekeep is the minute's housekeeping of the recordings (spec §2.13):
// tails past their time go, the oldest go while rings and tails hold more
// than the budget, everything stops if the disk is nearly full, sessions the
// recorders closed a minute ago are no longer theirs, and the recorders'
// health is looked at again for the admins' alert.
func (m *clipRecorders) housekeep(now time.Time) {
	m.control.Lock()
	defer m.control.Unlock()

	m.own.prune(now)

	var gone []*ring
	var busy bool
	m.locked(func() {
		gone = m.expireTailsLocked(now)
		busy = len(m.slots) > 0 || len(m.tails) > 0
	})
	removeRings(gone)

	m.enforceBudget()

	s := m.p.clips()
	if root := s.writableRoot(); busy && root != "" {
		if free, _, known := s.freeSpace(root); known && free < recorderStopFree {
			m.stopForSpaceLocked(nil, free)
		}
	}

	m.noteHealthLocked(now)
}

// noteHealthLocked looks at whether every recorder running is failing, and
// since when, for the admins' alert. Under control.
func (m *clipRecorders) noteHealthLocked(now time.Time) {
	slots, _ := m.held()

	failing := len(slots) > 0
	reason := ""
	for _, slot := range slots {
		if slot.rec.state() != clipStateFailed {
			failing = false
			break
		}
		if reason == "" {
			reason = slot.rec.reason()
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if !failing {
		m.failingSince, m.failingReason = time.Time{}, ""
		return
	}
	if m.failingSince.IsZero() {
		m.failingSince = now
	}
	m.failingReason = reason
}

// healthAlert is what admins are told when no stream can be recorded because
// every recorder has been failing for five minutes or more: almost always
// UDP between this server and Broadcast Box's media port. "" otherwise.
func (m *clipRecorders) healthAlert(now time.Time) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.failingSince.IsZero() || now.Sub(m.failingSince) < recorderAlertAfter {
		return ""
	}

	alert := "Clips cannot reach Broadcast Box's media port from this server"
	if m.failingReason != "" {
		alert += ": " + m.failingReason
	}

	return alert
}

// The manager as clipRecordings: what drafts are cut from.

// live is a live stream's recording, or what keeps it from being recorded.
func (m *clipRecorders) live(handle string) clipLive {
	m.mu.Lock()
	slot := m.slots[handle]
	waiting := m.waiting[handle]
	m.mu.Unlock()

	if slot == nil {
		return clipLive{state: waiting.state}
	}

	return slot.rec.live()
}

// tail is the held end of a stream, while it is held.
func (m *clipRecorders) tail(handle string, now time.Time) (clipTail, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	held := m.tails[handle]
	if held == nil || !now.Before(held.tail.until) || held.tail.ring.isDeleted() {
		return clipTail{}, false
	}

	return held.tail, true
}

// streamKeyOf is the key of the stream behind a handle, as a running recorder
// or a held tail has it, or "".
func (m *clipRecorders) streamKeyOf(handle string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if slot := m.slots[handle]; slot != nil && slot.stream.StreamKey != "" {
		return slot.stream.StreamKey
	}
	if held := m.tails[handle]; held != nil {
		return held.tail.stream.StreamKey
	}

	return ""
}

// What the panels and the admins are shown.

// clipStreamView is what a live stream's card is told about clipping it.
type clipStreamView struct {
	state    string
	clipFrom time.Time // while ready
	tail     time.Time // until when a tail of the stream is held
}

// streamViews is every stream's clip state as the manager last left it, by
// handle. From memory alone.
func (m *clipRecorders) streamViews(now time.Time) map[string]clipStreamView {
	var views map[string]clipStreamView
	var slots []*recorderSlot
	m.locked(func() {
		views = make(map[string]clipStreamView, len(m.slots)+len(m.waiting)+len(m.tails))
		for handle, waiting := range m.waiting {
			views[handle] = clipStreamView{state: waiting.state}
		}
		for handle, held := range m.tails {
			if now.Before(held.tail.until) && !held.tail.ring.isDeleted() {
				view := views[handle]
				view.tail = held.tail.until
				views[handle] = view
			}
		}
		slots = make([]*recorderSlot, 0, len(m.slots))
		for _, slot := range m.slots {
			slots = append(slots, slot)
		}
	})

	for _, slot := range slots {
		view := views[slot.handle]
		view.state = slot.rec.state()
		view.clipFrom = time.Time{}
		if view.state == clipStateReady {
			view.clipFrom = slot.rec.clipFrom()
		}
		views[slot.handle] = view
	}

	return views
}

// tailViews is every tail still held, the newest first, as the snapshot and
// the System Console show them: whose stream, how it ended, until when, and
// how much of it one press would take.
func (m *clipRecorders) tailViews(now time.Time, window time.Duration) []clipTailView {
	_, tails := m.held()

	views := []clipTailView{}
	for _, held := range tails {
		tail := held.tail
		if !now.Before(tail.until) || tail.ring.isDeleted() {
			continue
		}

		views = append(views, clipTailView{
			Handle:  tail.stream.Handle,
			Title:   tail.stream.Title,
			UserID:  tail.stream.UserID,
			Reason:  tail.reason,
			EndedAt: tail.ring.endTime().UnixMilli(),
			Until:   tail.until.UnixMilli(),
			HeldMs:  min(tail.ring.heldMs(), window.Milliseconds()),
		})
	}

	sort.Slice(views, func(i, j int) bool {
		if views[i].EndedAt != views[j].EndedAt {
			return views[i].EndedAt > views[j].EndedAt
		}

		return views[i].Handle < views[j].Handle
	})

	return views
}

// adminRows is one row per stream recorded or waiting to be, for the System
// Console, by name. Reasons are the recorders' own, which never carry a key.
func (m *clipRecorders) adminRows(now time.Time) []adminRecorderView {
	type running struct {
		handle, title string
		rec           clipRecorderHandle
	}

	m.mu.Lock()
	rows := make([]adminRecorderView, 0, len(m.slots)+len(m.waiting))
	for handle, waiting := range m.waiting {
		if m.slots[handle] != nil {
			continue
		}
		rows = append(rows, adminRecorderView{Handle: handle, Title: waiting.stream.Title, State: waiting.state, Reason: waiting.reason})
	}
	slots := make([]running, 0, len(m.slots))
	for _, slot := range m.slots {
		slots = append(slots, running{handle: slot.handle, title: slot.stream.Title, rec: slot.rec})
	}
	m.mu.Unlock()

	for _, slot := range slots {
		row := adminRecorderView{
			Handle:      slot.handle,
			Title:       slot.title,
			State:       slot.rec.state(),
			HeldSeconds: int(slot.rec.heldSeconds()),
			Bitrate:     int64(slot.rec.bitrate() * 8),
			Reason:      slot.rec.reason(),
		}
		if ring := slot.rec.live().ring; ring != nil {
			status := ring.status()
			row.Truncated = status.truncation != "" && status.truncatedUntil > now.UnixNano()
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Title != rows[j].Title {
			return rows[i].Title < rows[j].Title
		}

		return rows[i].Handle < rows[j].Handle
	})

	return rows
}

// ringBytes is what the rings and tails hold on the disk now.
func (m *clipRecorders) ringBytes() int64 {
	slots, tails := m.held()

	total := int64(0)
	for _, slot := range slots {
		if ring := slot.rec.live().ring; ring != nil {
			total += ring.size()
		}
	}
	for _, held := range tails {
		total += held.tail.ring.size()
	}

	return total
}

// withoutOwn leaves the recorders' own playback sessions out of a stream's
// sessions (§2.4.7), so a recorder is never counted as somebody watching, nor
// as a stranger beside the faces. From memory alone.
func (m *clipRecorders) withoutOwn(sessions []bbSession, now time.Time) []bbSession {
	return m.own.without(sessions, now)
}

// without is sessions less the ones that are ours. The same slice when none
// is, which is every stream not being recorded.
func (o *clipOwnSessions) without(sessions []bbSession, now time.Time) []bbSession {
	o.mu.Lock()
	defer o.mu.Unlock()

	if len(o.ids) == 0 {
		return sessions
	}

	ours := func(id string) bool {
		until, known := o.ids[id]

		return known && (until.IsZero() || now.Before(until))
	}

	kept := sessions
	for i, session := range sessions {
		if !ours(session.ID) {
			continue
		}

		kept = append(make([]bbSession, 0, len(sessions)), sessions[:i]...)
		for _, rest := range sessions[i+1:] {
			if !ours(rest.ID) {
				kept = append(kept, rest)
			}
		}

		break
	}

	return kept
}

// addRecordings puts the recordings on a snapshot (spec §2.12, A§2.1): each
// live stream's clip state, since when it has been recorded unbroken while
// ready, and until when an earlier stretch of it is held; and the tails. All
// from what the manager last knew, never by asking anything, so every path to
// publish says the same. Only while clips can be made at all: otherwise none
// of it is there, and a card carried over from an earlier snapshot loses what
// it said.
//
// It writes into a copy of the streams, never into the slice current was
// handed, which readers of an earlier snapshot may hold without a lock.
func (p *Plugin) addRecordings(current *snapshot, enabled bool, cfg *configuration, now time.Time) {
	var views map[string]clipStreamView
	current.ClipTails = nil
	if enabled {
		m := p.recorders()
		views = m.streamViews(now)
		if tails := m.tailViews(now, cfg.ringWindow()); len(tails) > 0 {
			current.ClipTails = tails
		}
	}

	touched := len(views) > 0
	for i := 0; !touched && i < len(current.Streams); i++ {
		stream := &current.Streams[i]
		touched = stream.Clip != "" || stream.ClipFrom != 0 || stream.ClipTail != 0
	}
	if !touched || len(current.Streams) == 0 {
		return
	}

	streams := make([]apiStream, len(current.Streams))
	copy(streams, current.Streams)

	for i := range streams {
		stream := &streams[i]
		stream.Clip, stream.ClipFrom, stream.ClipTail = "", 0, 0
		if stream.AlwaysOn || stream.Handle == "" || stream.Source != "" {
			continue
		}

		view, known := views[stream.Handle]
		if !known {
			continue
		}
		stream.Clip = view.state
		if view.state == clipStateReady && !view.clipFrom.IsZero() {
			stream.ClipFrom = view.clipFrom.UnixMilli()
		}
		if !view.tail.IsZero() {
			stream.ClipTail = view.tail.UnixMilli()
		}
	}

	current.Streams = streams
}

// wipeStream wipes what is held of the stream behind a handle (§2.10.6): its
// live ring and its tail, its exports cut short, and every draft cut from it,
// by anybody, gone as wiped — one being published included, which is
// cancelled, unless its clip is already written. Published clips stay. It
// answers how many drafts went.
func (p *Plugin) wipeStream(handle string, now time.Time) int {
	p.recorders().wipe(handle)
	removed := p.wipeDrafts(func(draft *clipDraft) bool { return draft.capture.Handle == handle }, now)
	p.republishClips()

	return removed
}

// streamKeyBehind is the key of the stream a handle names, wherever this node
// still knows it from: the stream on the air, a recorder or a held tail of it,
// or a draft cut from it. "" when none does.
func (p *Plugin) streamKeyBehind(handle string) string {
	if key := p.streamKeyForHandle(handle); key != "" {
		return key
	}
	if key := p.recorders().streamKeyOf(handle); key != "" {
		return key
	}

	return p.clips().drafts.streamKeyOf(handle)
}
