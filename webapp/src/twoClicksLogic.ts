// When the second press of a two-press button counts (twoClicks.ts). It
// imports nothing, so build/check-away.mjs runs it without a page.

// How long the first press waits for the second, and how soon after it a
// second press is too quick to have read the words the button changed into.
export const CONFIRM_MS = 4000;
export const CONFIRM_MIN_MS = 500;

// One press, as twoClicks.ts sees it. detail is the click's count: 0 for a
// press from the keyboard (or a screen reader's), 2 for the second half of a
// double click. keyDown is whether Enter or Space was down when it came, and
// releases how many times either has come up on this page so far.
export type Press = {
    at: number;
    detail: number;
    keyDown: boolean;
    releases: number;
};

// The press that armed a button: when, and whether a key was down then and
// how many releases there had been, so a later press can tell whether that
// key has come up since.
export type Arming = {
    at: number;
    keyDown: boolean;
    releases: number;
};

export function armingOf(press: Press): Arming {
    return {at: press.at, keyDown: press.keyDown, releases: press.releases};
}

// Whether this press, on a button armed by that one, should act. Not the
// second half of a double click; not one too quick to have read the words;
// and not the same key still held down. Held, Enter repeats after about half
// a second on the usual desktop settings, and each repeat presses a focused
// button or a box that acts on Enter again: without this, holding Enter for
// a moment would arm and then confirm on its own. A key that has come up
// since the arming press, or a click of the mouse, is a press of its own.
export function confirms(arming: Arming, press: Press): boolean {
    if (press.detail > 1) {
        return false;
    }

    if (press.at - arming.at < CONFIRM_MIN_MS) {
        return false;
    }

    if (press.detail === 0 && arming.keyDown && press.releases === arming.releases) {
        return false;
    }

    return true;
}
