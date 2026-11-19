import React from 'react';

import {CONFIRM_MIN_MS, CONFIRM_MS, armingOf, confirms} from './twoClicksLogic';
import type {Arming, Press} from './twoClicksLogic';

// A button that has to be pressed twice, for anything that cannot be undone:
// discarding a draft, reporting or deleting a clip, sharing it. The first
// press changes its words to "Click again …" for a few seconds; only a
// separate second press acts. Not the second half of a double click, not one
// that comes too quickly to have read the words it changed into — the
// AdminStills pattern, with its timings — and not a key that was never let
// go (twoClicksLogic.ts).
export {CONFIRM_MS, CONFIRM_MIN_MS};

export type TwoClicks = {
    // Which button is waiting for its second press, or ''.
    armed: string;

    // Call from the button's onClick with its own name: true when this is the
    // second press and the button should act, false when it has only armed.
    press: (id: string, event: {detail: number}) => boolean;
    disarm: () => void;
};

// Enter and Space on this page: whether one is down now, and how many times
// one has come up. Watched on the window, in the capture phase, so a key is
// seen before the press it causes: Enter presses a focused button on its
// keydown, and a box that acts on Enter does so from its own keydown, both
// after this. Losing the window (another application, or focus in a frame)
// counts as a release, since the key's own release will not arrive here.
const keys = {down: false, releases: 0};
let watchers = 0;

function pressKey(event: KeyboardEvent): boolean {
    return event.key === 'Enter' || event.key === ' ';
}

function onKeyDown(event: KeyboardEvent): void {
    if (pressKey(event)) {
        keys.down = true;
    }
}

function onKeyUp(event: KeyboardEvent): void {
    if (pressKey(event)) {
        keys.down = false;
        keys.releases += 1;
    }
}

function onWindowBlur(): void {
    if (keys.down) {
        keys.down = false;
        keys.releases += 1;
    }
}

// Listened for only while some two-press button is on the page, so nothing
// is left on Mattermost's window once the plugin's last one has gone.
function watchKeys(): () => void {
    if (watchers === 0) {
        window.addEventListener('keydown', onKeyDown, true);
        window.addEventListener('keyup', onKeyUp, true);
        window.addEventListener('blur', onWindowBlur);
    }
    watchers += 1;

    return () => {
        watchers -= 1;
        if (watchers === 0) {
            window.removeEventListener('keydown', onKeyDown, true);
            window.removeEventListener('keyup', onKeyUp, true);
            window.removeEventListener('blur', onWindowBlur);
            keys.down = false;
        }
    };
}

export function useTwoClicks(): TwoClicks {
    const [armed, setArmed] = React.useState('');
    const arming = React.useRef<Arming | null>(null);

    // A layout effect, so the keys are watched from the moment the button is
    // on the page, before any key can reach it.
    React.useLayoutEffect(() => watchKeys(), []);

    React.useEffect(() => {
        if (armed === '') {
            return undefined;
        }

        const timer = window.setTimeout(() => setArmed(''), CONFIRM_MS);

        return () => window.clearTimeout(timer);
    }, [armed]);

    const press = React.useCallback((id: string, event: {detail: number}) => {
        const now: Press = {at: Date.now(), detail: event.detail, keyDown: keys.down, releases: keys.releases};
        if (armed !== id) {
            setArmed(id);
            arming.current = armingOf(now);

            return false;
        }

        if (!arming.current || !confirms(arming.current, now)) {
            return false;
        }

        setArmed('');

        return true;
    }, [armed]);

    const disarm = React.useCallback(() => setArmed(''), []);

    return {armed, press, disarm};
}
