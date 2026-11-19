package main

import (
	"sync"
	"time"
)

// The soundboard's gate (design/3.2.0.md §7.5, §7.6): one sound at a time on
// the server, a wait for each person after their own sound ends, and eight
// sounds a minute in all.
//
// Every press is admitted here, under one mutex, with nothing else held, and
// the whole decision is made before the lock is released: this person's wait,
// anybody's sound in flight or the quiet after one, then the server-wide
// token, then the mark that a sound is in flight and a provisional end to
// this person's wait. So presses sent at the same moment, from one person in
// five tabs or five people at once, give exactly one admission, and every
// other press is told to wait, never queued.
//
// A press admitted here that then fails before its first packet is on the
// wire is undone: the gate is freed, the person's wait goes back to what it
// was, and the server-wide token is given back. A sound that plays ends here,
// which frees the gate after a quarter of a second of quiet and corrects the
// person's wait to the sound's real end. No other code reads or writes these
// fields.

const (
	// The quiet after any sound before the next may start.
	soundGapAfter = 250 * time.Millisecond

	// Sounds a minute, server-wide.
	soundsPerMinute = 8

	// What the gate says while a sound is admitted but not yet started:
	// long enough for the start, short enough to be worth asking again.
	soundStartingRetry = time.Second
)

// The codes the gate refuses with.
const (
	soundCodeCooldown = "cooldown"
	soundCodeBusy     = "busy"
	soundCodeTooMany  = "too_many"
)

type soundGate struct {
	lock sync.Mutex

	// The server-wide eight a minute, one bucket keyed "".
	global *rateLimiter

	// Whose sound is admitted or playing, "" when none is; and once it has
	// started, when it will end.
	inFlight     string
	playingUntil time.Time

	// Until when the quiet after the last sound lasts.
	busyUntil time.Time

	// Until when each person waits.
	cooldownUntil map[string]time.Time
}

func newSoundGate() *soundGate {
	return &soundGate{
		global:        newRateLimiter(soundsPerMinute, time.Minute),
		cooldownUntil: map[string]time.Time{},
	}
}

// gateTicket is one admission, for undoing or ending it.
type gateTicket struct {
	userID   string
	previous time.Time
}

// busyLeft is how long until a sound may start, given what is in flight, or
// zero. Under lock.
func (g *soundGate) busyLeft(now time.Time) time.Duration {
	if g.inFlight != "" {
		if g.playingUntil.IsZero() {
			return soundStartingRetry
		}

		return max(g.playingUntil.Add(soundGapAfter).Sub(now), time.Millisecond)
	}

	if g.busyUntil.After(now) {
		return g.busyUntil.Sub(now)
	}

	return 0
}

// cooldownLeft is how long this person still waits, or zero. Under lock.
func (g *soundGate) cooldownLeft(userID string, now time.Time) time.Duration {
	until, waiting := g.cooldownUntil[userID]
	if g.inFlight == userID {
		return max(until.Sub(now), time.Millisecond)
	}
	if waiting && until.After(now) {
		return until.Sub(now)
	}

	return 0
}

// peek is a look at the gate, changing nothing, for the cheap refusals before
// Mumble is asked anything. Only admit decides.
func (g *soundGate) peek(userID string, now time.Time) (string, time.Duration) {
	g.lock.Lock()
	defer g.lock.Unlock()

	if left := g.cooldownLeft(userID, now); left > 0 {
		return soundCodeCooldown, left
	}
	if left := g.busyLeft(now); left > 0 {
		return soundCodeBusy, left
	}

	return "", 0
}

// admit is the one decision (§7.5 step 5), in order: this person's own sound
// or wait; anybody's sound or the quiet after it; the server-wide token; then
// the mark, and this person's wait set provisionally to now, plus the longest
// sound and a packet, plus their wait.
func (g *soundGate) admit(userID string, now time.Time, cooldown time.Duration) (gateTicket, string, time.Duration) {
	g.lock.Lock()
	defer g.lock.Unlock()

	if left := g.cooldownLeft(userID, now); left > 0 {
		return gateTicket{}, soundCodeCooldown, left
	}
	if left := g.busyLeft(now); left > 0 {
		return gateTicket{}, soundCodeBusy, left
	}
	if !g.global.allowAt("", now) {
		return gateTicket{}, soundCodeTooMany, max(g.global.wait("", now), time.Millisecond)
	}

	// Waits that have run out go, so the map holds only people waiting.
	for other, until := range g.cooldownUntil {
		if !until.After(now) {
			delete(g.cooldownUntil, other)
		}
	}

	ticket := gateTicket{userID: userID, previous: g.cooldownUntil[userID]}
	g.inFlight = userID
	g.playingUntil = time.Time{}
	g.cooldownUntil[userID] = now.Add(maxSoundMs*time.Millisecond + soundFrameMs*time.Millisecond + cooldown)

	return ticket, "", 0
}

// undo takes an admission back, for a press that failed before its first
// packet: the gate is free, the person's wait is what it was, and the
// server-wide token is given back.
func (g *soundGate) undo(ticket gateTicket) {
	g.lock.Lock()
	defer g.lock.Unlock()

	if g.inFlight == ticket.userID {
		g.inFlight = ""
		g.playingUntil = time.Time{}
	}

	if ticket.previous.IsZero() {
		delete(g.cooldownUntil, ticket.userID)
	} else {
		g.cooldownUntil[ticket.userID] = ticket.previous
	}

	g.global.refund("")
}

// started notes that the first packet is on the wire, and when the sound
// will end; the person's wait, provisional until now, is from that end.
func (g *soundGate) started(ticket gateTicket, endsAt time.Time, cooldown time.Duration) {
	g.lock.Lock()
	defer g.lock.Unlock()

	if g.inFlight == ticket.userID {
		g.playingUntil = endsAt
		g.cooldownUntil[ticket.userID] = endsAt.Add(cooldown)
	}
}

// ended frees the gate after a sound, early or not: a quarter of a second of
// quiet for everybody, and the person's wait from the real end.
func (g *soundGate) ended(ticket gateTicket, now time.Time, cooldown time.Duration) {
	g.lock.Lock()
	defer g.lock.Unlock()

	if g.inFlight == ticket.userID {
		g.inFlight = ""
		g.playingUntil = time.Time{}
	}

	g.busyUntil = now.Add(soundGapAfter)
	g.cooldownUntil[ticket.userID] = now.Add(cooldown)
}

// state is when this person's wait ends and when the server's next sound may
// start, as GET /sounds and the sound event say them; zero for "now".
func (g *soundGate) state(userID string, now time.Time) (time.Time, time.Time) {
	g.lock.Lock()
	defer g.lock.Unlock()

	var cooldownUntil, busyUntil time.Time
	if left := g.cooldownLeft(userID, now); left > 0 {
		cooldownUntil = now.Add(left)
	}
	if left := g.busyLeft(now); left > 0 {
		busyUntil = now.Add(left)
	}

	return cooldownUntil, busyUntil
}
