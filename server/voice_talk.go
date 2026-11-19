package main

import (
	"math/rand/v2"
	"sort"
	"sync"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/opus"
	"github.com/pion/rtp"
)

// Passing voice between a phone and its Mumble session, without decoding it.
//
// Up, from the phone: every Opus packet the phone sends arrives with its
// audio level, and the person counts as talking while the level says they
// are, much as Mumble's own voice activation does. Only then do packets go
// to the voice server, as Mumble voice packets in the person's channel, so
// that a phone in a quiet room does not light up as talking all evening.
//
// Down, to the phone: the voice server sends this session everybody who
// talks in its channel. The phone has a few audio tracks ("slots"), and each
// person talking takes one for as long as they talk; the packets go out on
// it as RTP, with timestamps and sequence numbers the phone's jitter buffer
// can follow.

const (
	// Talking starts at a packet this loud or louder (RFC 6464 levels are
	// in -dBov, so a smaller number is louder)...
	voiceTalkStartLevel = 40

	// ...and lasts while packets this loud or louder keep coming, and for
	// voiceTalkHangover after the last of them.
	voiceTalkKeepLevel = 50
	voiceTalkHangover  = 400 * time.Millisecond

	// How many packets from before the start are sent with it, so the
	// first syllable is not cut.
	voiceTalkPreroll = 2

	// The audio level of a packet that came without one.
	voiceLevelUnknown = 127

	// Packets bigger than this, or more of them than this in a second, are
	// dropped: a phone that sends them is not sending speech.
	voiceMaxPayload   = 1024
	voiceMaxPerSecond = 100

	// Mumble counts sequence numbers in 10 ms frames, 480 samples at 48 kHz,
	// which is also the RTP clock of Opus.
	voiceFrameSamples = 480
	voiceRTPClock     = 48000

	// The longest Opus packet, 120 ms.
	voiceMaxPacketSamples = 5760

	// How many people a phone hears at once, and how long after a person's
	// last packet their slot is free again.
	voiceSlots    = 4
	voiceSlotIdle = 300 * time.Millisecond
)

// voiceClock is the time the voice code runs on. Tests put one behind it
// that moves only when they say.
type voiceClock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) voiceTimer
}

// voiceTimer is a timer AfterFunc made.
type voiceTimer interface {
	Stop() bool
}

type realVoiceClock struct{}

func (realVoiceClock) Now() time.Time { return time.Now() }

func (realVoiceClock) AfterFunc(d time.Duration, f func()) voiceTimer { return time.AfterFunc(d, f) }

// voiceOpusSamples is how long an Opus packet plays, in 48 kHz samples, or
// 0 for one whose first byte does not read as Opus.
func voiceOpusSamples(payload []byte) int64 {
	samples := opus.PacketDuration2(payload)
	if samples <= 0 || samples > voiceMaxPacketSamples {
		return 0
	}

	return samples
}

// ---------------------------------------------------------------- up

// voiceUplink is the phone's voice on its way to the voice server.
type voiceUplink struct {
	clock voiceClock

	// send writes one voice packet to the voice server.
	send func(sequence int64, final bool, payload []byte)

	// changed is called whenever talking may have changed, outside the lock.
	changed func()

	// safely runs the hangover timer's work.
	safely func(where string, f func()) bool

	lock sync.Mutex

	// Muted, deafened, server muted, suppressed, or without a phone: nothing
	// goes up.
	blocked bool

	// The RTP timestamps of the phone's packets, unwrapped to 64 bits: the
	// first one, and the latest.
	started bool
	first   int64
	latest  int64

	// The Mumble sequence number the first packet maps to, and the one
	// after the last packet sent.
	base int64
	next int64

	// Packets kept from before talking started.
	preroll []voiceUpPacket

	talking bool
	timer   voiceTimer
	timerAt uint64

	// The packets in the current second.
	windowStart time.Time
	windowCount int
}

// voiceUpPacket is a packet kept for the preroll.
type voiceUpPacket struct {
	sequence int64
	payload  []byte
}

// packet takes one Opus packet from the phone: its RTP timestamp, payload
// and audio level (0 to 127, in -dBov; voiceLevelUnknown when absent).
func (u *voiceUplink) packet(timestamp uint32, payload []byte, level uint8) {
	u.lock.Lock()
	started := u.packetLocked(timestamp, payload, level)
	u.lock.Unlock()

	if started {
		u.changed()
	}
}

// packetLocked is packet under the lock, and whether talking started.
func (u *voiceUplink) packetLocked(timestamp uint32, payload []byte, level uint8) bool {
	if len(payload) > voiceMaxPayload {
		return false
	}

	samples := voiceOpusSamples(payload)
	if samples == 0 {
		return false
	}

	now := u.clock.Now()
	if u.windowStart.IsZero() || now.Sub(u.windowStart) >= time.Second {
		u.windowStart, u.windowCount = now, 0
	}
	u.windowCount++
	if u.windowCount > voiceMaxPerSecond {
		return false
	}

	// Unwrapped across 2^32 from the latest timestamp, so a phone that runs
	// for more than a day still counts on, and a packet that arrives late
	// lands before the latest instead of a day later.
	var at int64
	if !u.started {
		u.started, u.first, u.latest = true, int64(timestamp), int64(timestamp)
		at = u.first
	} else {
		at = u.latest + int64(int32(timestamp-uint32(u.latest)))
		if at > u.latest {
			u.latest = at
		}
	}
	if at < u.first {
		return false
	}

	// So that a lost packet leaves a gap, as it would have from Mumble's
	// own client.
	sequence := u.base + (at-u.first)/voiceFrameSamples
	frames := max(samples/voiceFrameSamples, 1)

	if u.blocked {
		return false
	}

	if !u.talking {
		if level > voiceTalkStartLevel {
			u.preroll = append(u.preroll, voiceUpPacket{sequence: sequence, payload: payload})
			if len(u.preroll) > voiceTalkPreroll {
				u.preroll = u.preroll[len(u.preroll)-voiceTalkPreroll:]
			}

			return false
		}

		u.talking = true
		for _, kept := range u.preroll {
			u.sendLocked(kept.sequence, kept.payload, max(voiceOpusSamples(kept.payload)/voiceFrameSamples, 1))
		}
		u.preroll = nil
		u.sendLocked(sequence, payload, frames)
		u.armLocked()

		return true
	}

	u.sendLocked(sequence, payload, frames)
	if level <= voiceTalkKeepLevel {
		u.armLocked()
	}

	return false
}

// sendLocked sends one packet and notes the sequence after it.
func (u *voiceUplink) sendLocked(sequence int64, payload []byte, frames int64) {
	u.send(sequence, false, payload)
	u.next = max(u.next, sequence+frames)
}

// armLocked starts the hangover again.
func (u *voiceUplink) armLocked() {
	if u.timer != nil {
		u.timer.Stop()
	}

	u.timerAt++
	at := u.timerAt
	u.timer = u.clock.AfterFunc(voiceTalkHangover, func() {
		u.safely("the end of somebody talking from Matty", func() { u.hangoverOver(at) })
	})
}

// hangoverOver is the hangover timer firing: talking ends, unless the timer
// was started again since.
func (u *voiceUplink) hangoverOver(at uint64) {
	u.lock.Lock()
	if at != u.timerAt || !u.talking {
		u.lock.Unlock()
		return
	}
	u.stopTalkingLocked()
	u.lock.Unlock()

	u.changed()
}

// stopTalkingLocked ends a spell of talking with a zero-length last frame at
// the next sequence number, which is how Mumble's own client ends one.
func (u *voiceUplink) stopTalkingLocked() bool {
	if u.timer != nil {
		u.timer.Stop()
		u.timer = nil
	}
	u.timerAt++

	if !u.talking {
		return false
	}

	u.talking = false
	u.send(u.next, true, nil)
	u.next++

	return true
}

// setBlocked stops voice going up (muted, deafened, server muted,
// suppressed, no phone) or lets it go up again. Talking ends at once.
func (u *voiceUplink) setBlocked(blocked bool) {
	u.lock.Lock()
	u.blocked = blocked
	stopped := false
	if blocked {
		u.preroll = nil
		stopped = u.stopTalkingLocked()
	}
	u.lock.Unlock()

	if stopped {
		u.changed()
	}
}

// restart is a new phone taking over: its RTP timestamps start afresh, and
// the Mumble sequence carries on after the last one sent.
func (u *voiceUplink) restart() {
	u.lock.Lock()
	stopped := u.stopTalkingLocked()
	u.started = false
	u.base = u.next
	u.preroll = nil
	u.windowStart, u.windowCount = time.Time{}, 0
	u.lock.Unlock()

	if stopped {
		u.changed()
	}
}

// isTalking is whether the phone's voice is going up now.
func (u *voiceUplink) isTalking() bool {
	u.lock.Lock()
	defer u.lock.Unlock()

	return u.talking
}

// ---------------------------------------------------------------- down

// voiceDownlink is everybody else's voice on its way to the phone.
type voiceDownlink struct {
	clock voiceClock

	// changed is called whenever who is heard may have changed, outside
	// the lock.
	changed func()

	safely func(where string, f func()) bool

	lock  sync.Mutex
	peer  voicePeer
	count int
	deaf  bool
	slots [voiceSlots]voiceSlot
}

// voiceSlot is one of the phone's audio tracks.
type voiceSlot struct {
	// The session talking on it, while active.
	active  bool
	speaker uint32

	// The session that had it last, and when it was let go.
	used    bool
	last    uint32
	freedAt time.Time

	// The idle timer, and its generation.
	timer   voiceTimer
	timerAt uint64

	// RTP as written so far on this track.
	written   bool
	sequence  uint16
	timestamp uint32
	duration  uint32
	wroteAt   time.Time

	// The spell of talking now on it: its first timestamp and Mumble
	// sequence number, and the last Mumble sequence number written.
	inSpell    bool
	spellStamp uint32
	spellFirst int64
	spellLast  int64
}

// attach gives the downlink a new phone, whose tracks start afresh.
func (d *voiceDownlink) attach(peer voicePeer) {
	d.lock.Lock()
	d.freeAllLocked()
	for i := range d.slots {
		d.slots[i] = voiceSlot{}
	}
	d.peer = peer
	d.count = 0
	if peer != nil {
		d.count = min(max(peer.slots(), 0), voiceSlots)
	}
	d.lock.Unlock()

	d.changed()
}

// setDeaf stops everything going down while deafened.
func (d *voiceDownlink) setDeaf(deaf bool) {
	d.lock.Lock()
	d.deaf = deaf
	freed := false
	if deaf {
		freed = d.freeAllLocked()
	}
	d.lock.Unlock()

	if freed {
		d.changed()
	}
}

// freeAllLocked lets every slot go, and is whether one was in use.
func (d *voiceDownlink) freeAllLocked() bool {
	now := d.clock.Now()
	freed := false
	for i := range d.slots {
		if d.slots[i].active {
			d.freeLocked(i, now)
			freed = true
		}
	}

	return freed
}

// freeLocked lets one slot go.
func (d *voiceDownlink) freeLocked(i int, at time.Time) {
	slot := &d.slots[i]
	if slot.timer != nil {
		slot.timer.Stop()
		slot.timer = nil
	}
	slot.timerAt++
	slot.active = false
	slot.inSpell = false
	slot.freedAt = at
}

// gone is somebody who left the voice server: their slot is free at once.
func (d *voiceDownlink) gone(session uint32) {
	d.lock.Lock()
	freed := false
	for i := range d.slots {
		if d.slots[i].active && d.slots[i].speaker == session {
			d.freeLocked(i, d.clock.Now())
			freed = true
		}
	}
	d.lock.Unlock()

	if freed {
		d.changed()
	}
}

// packet takes one voice packet from the voice server and writes it to the
// speaker's slot, when they have one or one is free.
func (d *voiceDownlink) packet(sender uint32, sequence int64, payload []byte, final bool) {
	d.lock.Lock()
	changed := d.packetLocked(sender, sequence, payload, final)
	d.lock.Unlock()

	if changed {
		d.changed()
	}
}

func (d *voiceDownlink) packetLocked(sender uint32, sequence int64, payload []byte, final bool) bool {
	if d.peer == nil || d.deaf || d.count == 0 {
		return false
	}

	now := d.clock.Now()
	changed := false

	i := d.slotOfLocked(sender)
	if i < 0 {
		if len(payload) == 0 {
			return false
		}

		i = d.freeSlotLocked(sender)
		if i < 0 {
			return false
		}

		slot := &d.slots[i]
		slot.active, slot.speaker = true, sender
		slot.used, slot.last = true, sender
		slot.inSpell = false
		changed = true
	}

	// A frame with nothing in it only ends the spell.
	if len(payload) == 0 {
		d.freeLocked(i, now)
		return true
	}

	slot := &d.slots[i]
	marker := false

	if !slot.inSpell || sequence <= slot.spellLast {
		start := rand.Uint32()
		if slot.written {
			elapsed := uint32(max(now.Sub(slot.wroteAt), 0) / time.Microsecond * voiceRTPClock / 1_000_000)
			start = slot.timestamp + max(slot.duration, elapsed)
		}
		slot.inSpell, slot.spellStamp, slot.spellFirst = true, start, sequence
		marker = true
	}

	if !slot.written {
		slot.sequence = uint16(rand.Uint32())
	} else {
		slot.sequence++
	}

	timestamp := slot.spellStamp + uint32((sequence-slot.spellFirst)*voiceFrameSamples)
	samples := voiceOpusSamples(payload)
	if samples == 0 {
		samples = 2 * voiceFrameSamples
	}

	slot.written = true
	slot.timestamp = timestamp
	slot.duration = uint32(samples)
	slot.wroteAt = now
	slot.spellLast = sequence

	_ = d.peer.writeSlot(i, &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			Marker:         marker,
			SequenceNumber: slot.sequence,
			Timestamp:      timestamp,
		},
		Payload: payload,
	})

	if final {
		d.freeLocked(i, now)
		return true
	}

	d.armLocked(i)

	return changed
}

// slotOfLocked is the slot a session is talking on, or -1.
func (d *voiceDownlink) slotOfLocked(sender uint32) int {
	for i := range d.count {
		if d.slots[i].active && d.slots[i].speaker == sender {
			return i
		}
	}

	return -1
}

// freeSlotLocked is the slot a session that starts talking gets: the one it
// had last when that is free, else the one free longest; -1 when none is.
func (d *voiceDownlink) freeSlotLocked(sender uint32) int {
	best := -1
	for i := range d.count {
		slot := &d.slots[i]
		if slot.active {
			continue
		}
		if slot.used && slot.last == sender {
			return i
		}
		if best < 0 || slot.freedAt.Before(d.slots[best].freedAt) {
			best = i
		}
	}

	return best
}

// armLocked starts a slot's idle timer again.
func (d *voiceDownlink) armLocked(i int) {
	slot := &d.slots[i]
	if slot.timer != nil {
		slot.timer.Stop()
	}

	slot.timerAt++
	at := slot.timerAt
	slot.timer = d.clock.AfterFunc(voiceSlotIdle, func() {
		d.safely("a voice slot falling idle", func() { d.idle(i, at) })
	})
}

// idle is a slot's timer firing: its speaker has gone quiet.
func (d *voiceDownlink) idle(i int, at uint64) {
	d.lock.Lock()
	slot := &d.slots[i]
	if slot.timerAt != at || !slot.active {
		d.lock.Unlock()
		return
	}
	d.freeLocked(i, d.clock.Now())
	d.lock.Unlock()

	d.changed()
}

// speakers is the sessions heard now, in order.
func (d *voiceDownlink) speakers() []uint32 {
	d.lock.Lock()
	defer d.lock.Unlock()

	var speakers []uint32
	for i := range d.slots {
		if d.slots[i].active {
			speakers = append(speakers, d.slots[i].speaker)
		}
	}
	sort.Slice(speakers, func(a, b int) bool { return speakers[a] < speakers[b] })

	return speakers
}
