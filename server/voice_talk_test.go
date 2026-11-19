package main

import (
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Voice up and down on a clock that moves only when the test says.

// Opus packets by their first byte: CELT fullband 10 ms and 20 ms, SILK
// wideband 60 ms, one frame each.
var (
	opus10ms = []byte{30 << 3, 0x01, 0x02}
	opus20ms = []byte{31 << 3, 0x01, 0x02}
	opus60ms = []byte{11 << 3, 0x01, 0x02}
)

// fakeVoiceClock is a clock whose timers fire as the test advances it, in
// order, on the test's goroutine.
type fakeVoiceClock struct {
	lock   sync.Mutex
	now    time.Time
	timers []*fakeVoiceTimer
}

type fakeVoiceTimer struct {
	clock *fakeVoiceClock
	at    time.Time
	f     func()
	done  bool
}

func newFakeVoiceClock() *fakeVoiceClock {
	return &fakeVoiceClock{now: time.Date(2026, 11, 19, 21, 0, 0, 0, time.UTC)}
}

func (c *fakeVoiceClock) Now() time.Time {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.now
}

func (c *fakeVoiceClock) AfterFunc(d time.Duration, f func()) voiceTimer {
	c.lock.Lock()
	defer c.lock.Unlock()

	timer := &fakeVoiceTimer{clock: c, at: c.now.Add(d), f: f}
	c.timers = append(c.timers, timer)

	return timer
}

func (t *fakeVoiceTimer) Stop() bool {
	t.clock.lock.Lock()
	defer t.clock.lock.Unlock()

	was := !t.done
	t.done = true

	return was
}

// advance moves the clock on, firing every timer due on the way.
func (c *fakeVoiceClock) advance(by time.Duration) {
	c.lock.Lock()
	target := c.now.Add(by)
	c.lock.Unlock()

	for {
		c.lock.Lock()
		var next *fakeVoiceTimer
		for _, timer := range c.timers {
			if !timer.done && !timer.at.After(target) && (next == nil || timer.at.Before(next.at)) {
				next = timer
			}
		}
		if next == nil {
			c.now = target
			c.lock.Unlock()

			return
		}
		next.done = true
		if next.at.After(c.now) {
			c.now = next.at
		}
		c.lock.Unlock()

		next.f()
	}
}

// voiceTestSafely is safely for tests: a panic is counted, and fails the
// test when it ends.
func voiceTestSafely(t *testing.T) func(string, func()) bool {
	t.Helper()

	var panics atomic.Int32
	t.Cleanup(func() { assert.Zero(t, panics.Load(), "panics caught") })

	return func(where string, f func()) bool {
		if safely(nil, where, f) {
			panics.Add(1)
			return true
		}

		return false
	}
}

// sentUp is a packet the uplink sent to the voice server.
type sentUp struct {
	sequence int64
	final    bool
	size     int
}

// upScene is an uplink with what it sends and how often talking changed.
type upScene struct {
	clock *fakeVoiceClock
	up    *voiceUplink

	lock    sync.Mutex
	sent    []sentUp
	changes int
}

func newUpScene(t *testing.T) *upScene {
	t.Helper()

	s := &upScene{clock: newFakeVoiceClock()}
	s.up = &voiceUplink{
		clock:  s.clock,
		safely: voiceTestSafely(t),
		send: func(sequence int64, final bool, payload []byte) {
			s.lock.Lock()
			defer s.lock.Unlock()

			s.sent = append(s.sent, sentUp{sequence: sequence, final: final, size: len(payload)})
		},
		changed: func() {
			s.lock.Lock()
			defer s.lock.Unlock()

			s.changes++
		},
	}

	return s
}

func (s *upScene) take() []sentUp {
	s.lock.Lock()
	defer s.lock.Unlock()

	sent := s.sent
	s.sent = nil

	return sent
}

func (s *upScene) sequences() []int64 {
	var sequences []int64
	for _, sent := range s.take() {
		sequences = append(sequences, sent.sequence)
	}

	return sequences
}

// talk sends 20 ms packets at these levels, 20 ms apart, from timestamp ts,
// and returns the timestamp after them.
func (s *upScene) talk(ts uint32, levels ...uint8) uint32 {
	for _, level := range levels {
		s.up.packet(ts, opus20ms, level)
		ts += 960
		s.clock.advance(20 * time.Millisecond)
	}

	return ts
}

func TestVoiceUpStartsAtLoudKeepsForTheHangoverAndEndsWithALastFrame(t *testing.T) {
	s := newUpScene(t)

	// Quiet, then a level of 45: loud enough to keep talking going, not to
	// start it.
	ts := s.talk(0, 90, 80, 45)
	assert.Empty(t, s.take(), "nothing goes up before the first packet at 40 or louder")
	assert.False(t, s.up.isTalking())

	// The start takes the two packets before it along.
	ts = s.talk(ts, 38)
	assert.Equal(t, []int64{2, 4, 6}, s.sequences(), "the start and the two packets before it")
	assert.True(t, s.up.isTalking())

	// A packet at 50 keeps it going; quieter ones go up while the hangover
	// runs, 400 ms after the last at 50 or louder.
	ts = s.talk(ts, 50)
	quiet := make([]uint8, 18)
	for i := range quiet {
		quiet[i] = 90
	}
	ts = s.talk(ts, quiet...)
	sent := s.take()
	require.Len(t, sent, 19, "the packet at 50, and every one while the hangover ran")
	assert.Equal(t, int64(44), sent[18].sequence)
	assert.True(t, s.up.isTalking(), "380 ms after the last packet at 50")

	// 400 ms after that packet the spell ends: a zero-length last frame at
	// the next sequence number.
	s.clock.advance(20 * time.Millisecond)
	assert.False(t, s.up.isTalking())
	assert.Equal(t, []sentUp{{sequence: 46, final: true, size: 0}}, s.take())

	// Quiet again: nothing.
	s.talk(ts+960, 90, 90)
	assert.Empty(t, s.take())
}

func TestVoiceUpSequencesCountTenMillisecondFramesAndALostPacketLeavesAGap(t *testing.T) {
	t.Run("20 ms", func(t *testing.T) {
		s := newUpScene(t)
		s.talk(48000, 30, 30, 30)
		assert.Equal(t, []int64{0, 2, 4}, s.sequences())
	})

	t.Run("60 ms", func(t *testing.T) {
		s := newUpScene(t)
		for i := range 3 {
			s.up.packet(uint32(i*2880), opus60ms, 30)
			s.clock.advance(60 * time.Millisecond)
		}
		assert.Equal(t, []int64{0, 6, 12}, s.sequences())

		// The spell's end follows the last 60 ms packet.
		s.clock.advance(time.Second)
		assert.Equal(t, []sentUp{{sequence: 18, final: true}}, s.take())
	})

	t.Run("a lost packet", func(t *testing.T) {
		s := newUpScene(t)
		s.up.packet(0, opus20ms, 30)
		s.up.packet(960, opus20ms, 30)
		s.up.packet(2880, opus20ms, 30)
		assert.Equal(t, []int64{0, 2, 6}, s.sequences())
	})

	t.Run("across the timestamp's wrap", func(t *testing.T) {
		s := newUpScene(t)
		s.up.packet(1<<32-960, opus20ms, 30)
		s.up.packet(0, opus20ms, 30)
		s.up.packet(960, opus20ms, 30)
		assert.Equal(t, []int64{0, 2, 4}, s.sequences())
	})

	t.Run("a new phone carries on", func(t *testing.T) {
		s := newUpScene(t)
		s.up.packet(5000, opus20ms, 30)
		s.up.packet(5960, opus20ms, 30)
		s.up.restart()
		assert.Equal(t, []sentUp{{sequence: 0, size: 3}, {sequence: 2, size: 3}, {sequence: 4, final: true}}, s.take())

		s.up.packet(777, opus20ms, 30)
		s.up.packet(777+960, opus20ms, 30)
		assert.Equal(t, []int64{5, 7}, s.sequences(), "after the last one sent")
	})
}

func TestVoiceUpSendsNothingWhileBlocked(t *testing.T) {
	s := newUpScene(t)

	ts := s.talk(0, 30, 30)
	require.Len(t, s.take(), 2)

	// Muted (or deafened, server muted, suppressed): the spell ends at once.
	s.up.setBlocked(true)
	assert.Equal(t, []sentUp{{sequence: 4, final: true}}, s.take())
	assert.False(t, s.up.isTalking())

	ts = s.talk(ts, 30, 30, 30)
	assert.Empty(t, s.take(), "nothing while blocked")

	// The packets from while blocked are not kept for a preroll either.
	s.up.setBlocked(false)
	ts = s.talk(ts, 90)
	s.talk(ts, 30)
	assert.Equal(t, []int64{10, 12}, s.sequences())
}

func TestVoiceUpDropsWhatIsNotSpeech(t *testing.T) {
	s := newUpScene(t)

	big := append([]byte{31 << 3}, make([]byte, 1024)...)
	s.up.packet(0, big, 30)
	s.up.packet(0, nil, 30)
	s.up.packet(0, []byte{31<<3 | 3}, 30)
	assert.Empty(t, s.take(), "too big, empty, and a frame count missing")

	// More than 100 in a second: the rest of that second is dropped.
	for i := range 150 {
		s.up.packet(uint32(i*480), opus10ms, 30)
	}
	assert.Len(t, s.take(), 100)

	s.clock.advance(time.Second)
	assert.Equal(t, []sentUp{{sequence: 100, final: true}}, s.take(), "the hangover ran out")
	s.up.packet(150*480, opus10ms, 30)
	assert.Len(t, s.take(), 1, "the next second")
}

// fakeVoicePeer is a phone's peer for tests.
type fakeVoicePeer struct {
	tracks int

	lock    sync.Mutex
	written map[int][]*rtp.Packet
	signals []any
	closed  bool

	// Found gone before its session took it.
	gone bool
}

func newFakeVoicePeer(tracks int) *fakeVoicePeer {
	return &fakeVoicePeer{tracks: tracks, written: map[int][]*rtp.Packet{}}
}

func (f *fakeVoicePeer) slots() int { return f.tracks }

func (f *fakeVoicePeer) writeSlot(slot int, packet *rtp.Packet) error {
	f.lock.Lock()
	defer f.lock.Unlock()

	copied := *packet
	f.written[slot] = append(f.written[slot], &copied)

	return nil
}

func (f *fakeVoicePeer) signal(message any) {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.signals = append(f.signals, message)
}

func (f *fakeVoicePeer) close() {
	f.lock.Lock()
	defer f.lock.Unlock()

	f.closed = true
}

func (f *fakeVoicePeer) isGone() bool {
	f.lock.Lock()
	defer f.lock.Unlock()

	return f.gone
}

func (f *fakeVoicePeer) isClosed() bool {
	f.lock.Lock()
	defer f.lock.Unlock()

	return f.closed
}

func (f *fakeVoicePeer) on(slot int) []*rtp.Packet {
	f.lock.Lock()
	defer f.lock.Unlock()

	return append([]*rtp.Packet(nil), f.written[slot]...)
}

func (f *fakeVoicePeer) all() []any {
	f.lock.Lock()
	defer f.lock.Unlock()

	return append([]any(nil), f.signals...)
}

// talking is the last talking set sent, and whether one was.
func (f *fakeVoicePeer) talking() ([]uint32, bool) {
	signals := f.all()
	for i := len(signals) - 1; i >= 0; i-- {
		if talking, ok := signals[i].(voiceTalkingSignal); ok {
			return talking.Talking, true
		}
	}

	return nil, false
}

// lastSelf is the last self state sent, and whether one was.
func (f *fakeVoicePeer) lastSelf() (voiceSelfState, bool) {
	signals := f.all()
	for i := len(signals) - 1; i >= 0; i-- {
		if self, ok := signals[i].(voiceSelfSignal); ok {
			return self.Self, true
		}
	}

	return voiceSelfState{}, false
}

// ended is the end signal sent, and whether one was.
func (f *fakeVoicePeer) ended() (voiceEndSignal, bool) {
	for _, signal := range f.all() {
		if end, ok := signal.(voiceEndSignal); ok {
			return end, true
		}
	}

	return voiceEndSignal{}, false
}

// downScene is a downlink to a fake peer.
type downScene struct {
	clock *fakeVoiceClock
	down  *voiceDownlink
	peer  *fakeVoicePeer

	changes atomic.Int32
}

func newDownScene(t *testing.T, tracks int) *downScene {
	t.Helper()

	s := &downScene{clock: newFakeVoiceClock(), peer: newFakeVoicePeer(tracks)}
	s.down = &voiceDownlink{clock: s.clock, safely: voiceTestSafely(t), changed: func() { s.changes.Add(1) }}
	s.down.attach(s.peer)

	return s
}

// slotOf is the slot a speaker's payload went out on, by the payload's
// second byte, which the tests set to the speaker.
func (s *downScene) slotsBySpeaker() map[byte]int {
	slots := map[byte]int{}
	for slot := range voiceSlots {
		for _, packet := range s.peer.on(slot) {
			slots[packet.Payload[1]] = slot
		}
	}

	return slots
}

func spoken(speaker byte) []byte { return []byte{31 << 3, speaker} }

func TestVoiceDownFourSpeakersAndAFifth(t *testing.T) {
	s := newDownScene(t, 8)

	for speaker := byte(1); speaker <= 5; speaker++ {
		s.down.packet(uint32(speaker), 0, spoken(speaker), false)
	}
	assert.Equal(t, map[byte]int{1: 0, 2: 1, 3: 2, 4: 3}, s.slotsBySpeaker(), "four slots, whatever the phone offers")
	assert.Equal(t, []uint32{1, 2, 3, 4}, s.down.speakers(), "the fifth is not heard")

	// Speaker 2 ends with a last frame: the fifth takes the slot at once.
	s.down.packet(2, 2, spoken(2), true)
	s.down.packet(5, 2, spoken(5), false)
	assert.Equal(t, 1, s.slotsBySpeaker()[5])
	assert.Equal(t, []uint32{1, 3, 4, 5}, s.down.speakers())

	// The rest fall quiet: 300 ms after their last packet, every slot is free.
	s.clock.advance(299 * time.Millisecond)
	assert.Len(t, s.down.speakers(), 4)
	s.clock.advance(time.Millisecond)
	assert.Empty(t, s.down.speakers())
}

func TestVoiceDownReusesASpeakersSlotElseTheOneFreeLongest(t *testing.T) {
	s := newDownScene(t, 2)

	s.down.packet(1, 0, spoken(1), false) // slot 0
	s.clock.advance(100 * time.Millisecond)
	s.down.packet(2, 0, spoken(2), false) // slot 1
	s.clock.advance(time.Second)
	require.Empty(t, s.down.speakers())

	// Speaker 2's slot is free: it takes it, though slot 0 has been free
	// longer.
	s.down.packet(2, 50, spoken(2), false)
	assert.Equal(t, 1, s.slotsBySpeaker()[2])

	// Speaker 3 is new: the one free longest.
	s.down.packet(3, 0, spoken(3), false)
	assert.Equal(t, 0, s.slotsBySpeaker()[3])

	// Speaker 1's slot is taken now, and none is free: dropped.
	s.down.packet(1, 100, spoken(1), false)
	assert.Equal(t, []uint32{2, 3}, s.down.speakers())
	assert.Len(t, s.peer.on(0), 2, "speaker 1's first packet and speaker 3's")
}

func TestVoiceDownTimestampsFollowTheSequenceAndTheClock(t *testing.T) {
	s := newDownScene(t, 4)

	// A spell: Mumble sequence 0, 2, 4 (20 ms apart), then a zero-length
	// last frame, which is not written.
	for _, sequence := range []int64{0, 2, 4} {
		s.down.packet(9, sequence, opus20ms, false)
		s.clock.advance(20 * time.Millisecond)
	}
	s.down.packet(9, 6, nil, true)
	assert.Empty(t, s.down.speakers(), "the last frame frees the slot")

	// The next spell, a second later, starting over at 0.
	s.clock.advance(time.Second)
	s.down.packet(9, 0, opus20ms, false)
	s.clock.advance(20 * time.Millisecond)
	s.down.packet(9, 2, opus20ms, false)

	// A sequence not ahead of the last starts a spell too, even right away.
	s.down.packet(9, 1, opus20ms, false)

	packets := s.peer.on(0)
	require.Len(t, packets, 6)

	first := packets[0]
	for i, packet := range packets {
		assert.Equal(t, uint8(2), packet.Version)
		assert.Equal(t, first.SequenceNumber+uint16(i), packet.SequenceNumber, "a running sequence number")
	}

	stamps := make([]uint32, len(packets))
	markers := make([]bool, len(packets))
	for i, packet := range packets {
		stamps[i] = packet.Timestamp - first.Timestamp
		markers[i] = packet.Marker
	}

	// Within a spell 480 a frame; the gap between spells is the wall time,
	// 1,020 ms since the last packet written, in samples; a spell that
	// starts at once is the last packet's own length on.
	assert.Equal(t, []uint32{0, 960, 1920, 1920 + 48960, 1920 + 48960 + 960, 1920 + 48960 + 960 + 960}, stamps)
	assert.Equal(t, []bool{true, false, false, true, false, true}, markers)
}

func TestVoiceDownPassesNothingWhileDeafened(t *testing.T) {
	s := newDownScene(t, 4)

	s.down.packet(1, 0, spoken(1), false)
	require.Equal(t, []uint32{1}, s.down.speakers())

	s.down.setDeaf(true)
	assert.Empty(t, s.down.speakers(), "the slots go")
	s.down.packet(1, 2, spoken(1), false)
	s.down.packet(2, 0, spoken(2), false)
	assert.Len(t, s.peer.on(0), 1)
	assert.Empty(t, s.peer.on(1))

	s.down.setDeaf(false)
	s.down.packet(2, 2, spoken(2), false)
	assert.Equal(t, []uint32{2}, s.down.speakers())
}

func TestVoiceDownWithoutAPeerOrTracks(t *testing.T) {
	s := newDownScene(t, 0)
	s.down.packet(1, 0, spoken(1), false)
	assert.Empty(t, s.down.speakers())

	s.down.attach(newFakeVoicePeer(1))
	s.down.packet(1, 0, spoken(1), false)
	s.down.packet(2, 0, spoken(2), false)
	assert.Equal(t, []uint32{1}, s.down.speakers(), "one track, one voice")

	s.down.gone(1)
	assert.Empty(t, s.down.speakers(), "somebody who left is let go at once")

	s.down.attach(nil)
	s.down.packet(2, 4, spoken(2), false)
	assert.Empty(t, s.down.speakers())
}

func TestVoiceOpusSamples(t *testing.T) {
	cases := map[string]struct {
		payload []byte
		samples int64
	}{
		"10 ms":                     {opus10ms, 480},
		"20 ms":                     {opus20ms, 960},
		"60 ms":                     {opus60ms, 2880},
		"two 20 ms frames":          {[]byte{31<<3 | 1, 0}, 1920},
		"three by count":            {[]byte{31<<3 | 3, 3}, 2880},
		"empty":                     {nil, 0},
		"a count byte missing":      {[]byte{31<<3 | 3}, 0},
		"longer than 120 ms":        {[]byte{11<<3 | 3, 3}, 0},
		"a count of nothing at all": {[]byte{31<<3 | 3, 0}, 0},
	}

	names := make([]string, 0, len(cases))
	for name := range cases {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		assert.Equal(t, cases[name].samples, voiceOpusSamples(cases[name].payload), name)
	}
}
