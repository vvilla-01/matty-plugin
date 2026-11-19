// How long each piece of the panel's motion takes.
//
// In one place because two things have to agree on every one of these: the
// stylesheet that plays the animation, and the timer that waits for it to
// finish before taking the thing away. They used to be two numbers in two
// files, and a tile removed 40ms before its animation ended is a tile that
// vanishes mid-frame. styles.ts reads these same constants into the CSS.

// The whole panel coming up when it is opened: a scan line sweeping down it.
export const BOOT_MS = 640;

// A screen turning on — a point, a line, then the picture — and how long after
// the panel starts booting it waits, so it happens as the scan line passes it
// rather than underneath the part of the panel still dark.
export const CRT_ON_MS = 620;
export const CRT_ON_DELAY_MS = 200;

// A screen turning off: the picture collapses to a bright line, the line to a
// dot, and the dot goes out.
export const CRT_OFF_MS = 460;

// With other tiles still up, the one switched off then slides up behind them
// and fades; they glide into the space it left.
export const LEAVE_MS = 380;
export const GLIDE_MS = 360;

// The half-second of picture a No Signal screen catches now and then.
export const GHOST_MS = 560;

// Between ghosts, per setting: a random wait in [low, high) milliseconds.
// Random rather than regular because a signal drifting in and out on a timer
// reads as a timer.
export const GHOST_INTERVALS: Record<string, [number, number]> = {
    rare: [45000, 80000],
    occasional: [14000, 28000],
    often: [4500, 9000],
};

// A stream title too long for its line scrolls like a ticker: pixels a second,
// slow enough to read while it moves; the gap between the end of the words and
// the start of them again; and the share of each lap spent resting at the
// start. A share rather than a time because a keyframe cannot take a variable —
// the time follows from it.
export const MARQUEE_SPEED = 30;
export const MARQUEE_GAP = 48;
export const MARQUEE_REST = 0.18;
export const MARQUEE_REST_PERCENT = Math.round(MARQUEE_REST * 100);

// How long after a reply lands the panel waits before coming back, so the
// reply can be seen arriving in the thread. The small player's red line drains
// over the same time, so the two have to agree.
export const RETURN_DELAY_MS = 1500;

// How long the "Back to thread" strip stays after the panel comes back, while
// nobody is pointing at it or focused on it. Thirty seconds rather than a
// toast's five: it is the only way back to the thread that was just closed.
export const UNDO_MS = 30000;

// The small player appearing, going, and changing size.
export const MINI_FADE_MS = 180;

// The small player settling into a corner after a drag, or a Move.
export const SNAP_MS = 240;

// The lock screen opening (design/lock-screen.md 4.6): the padlock swings
// open and the lock fades, and the panel takes its place at this point.
export const LOCK_OPEN_MS = 440;

// The lab slows the unlock down tenfold to look at it; nothing else changes
// this. PanelGate waits lockOpenMs(), and the lab slows the stylesheet's
// share of it to match.
let lockOpenScale = 1;

export function lockOpenMs(): number {
    return LOCK_OPEN_MS * lockOpenScale;
}

export function slowLockOpening(scale: number): void {
    lockOpenScale = scale > 0 ? scale : 1;
}

// A press that opened the panel counts as opened by hand for this long, so the
// lock may put the bearer token bar in focus. A flag nobody used in that time
// is ignored, so it can never focus the bar on a later, automatic opening.
export const OPENED_BY_HAND_MS = 1000;

// How often a window with anything playing reads its access again, in case
// the event saying it changed never arrived.
export const ACCESS_RECHECK_MS = 60000;

// How long something asked for from outside a locked panel - a stream, a
// clip - is still wanted when the lock opens.
export const PENDING_MS = 600000;

// Everything above is decoration, so all of it steps aside for somebody who has
// asked their system for less motion. Checked when it is needed rather than
// once, because the preference can change while the panel is open.
export function reducedMotion(): boolean {
    try {
        return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    } catch {
        return false;
    }
}
