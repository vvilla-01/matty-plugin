// The time, for the lock's stamped flags and its timers: Date.now(), plus
// however far the lab has wound its clock forward.
//
// Only the lab winds it (Clock +60 s, +10 min, +15 s), so that a pending
// stream lapsing, a revealed token hiding itself and the while-playing re-read
// can be looked at without waiting. Everything that reads it is told when it
// moves, and looks again. The lab can also hold it still, so a soundboard
// tile's ring or a cooldown's sweep is caught at the same place every time.

type Listener = () => void;

let skew = 0;
let held: number | null = null;
const listeners = new Set<Listener>();

export function now(): number {
    return held === null ? Date.now() + skew : held;
}

export function advanceClock(ms: number): void {
    skew += Math.max(0, ms);
    if (held !== null) {
        held += Math.max(0, ms);
    }
    Array.from(listeners).forEach((listener) => listener());
}

// Held, it stays where it is until advanced or let go; let go, it carries on
// from where it was held, so nothing timed by it jumps back.
export function holdClock(on: boolean): void {
    if (on && held === null) {
        held = now();
    } else if (!on && held !== null) {
        skew = held - Date.now();
        held = null;
    }
    Array.from(listeners).forEach((listener) => listener());
}

export function onClockAdvance(listener: Listener): () => void {
    listeners.add(listener);

    return () => {
        listeners.delete(listener);
    };
}
