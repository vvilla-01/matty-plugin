import React from 'react';

import {now as clockNow} from './clock';
import {sweepMissing} from './sessions';
import {getSnapshot, subscribe} from './store';

// Which streams the panel is showing, which one of them has the sound, and the
// hook that lets another surface (the profile popover) open the panel on a
// particular stream.
//
// A module-level observable rather than Redux or context, matching store.ts.
// Two properties matter and are load-bearing:
//
//   * The array handed to React is replaced ONLY when its membership really
//     changes. reconcile() runs on every snapshot, so an array rebuilt each
//     time would change identity every 15 seconds and remount every tile,
//     tearing down healthy WebRTC sessions.
//   * Nothing here notifies unless something changed, so the effect that calls
//     reconcile() cannot drive itself in a loop.

// Four tiles. Media does not pass through Mattermost — each tile is a direct
// WebRTC connection to the origin pulling a full-quality encode, because
// Broadcast Box has no route for asking for a smaller layer through this
// plugin. Four gaming POVs is already tens of megabits and four simultaneous
// hardware decodes; past that the honest answer is a second window.
export const MAX_WATCHING = 4;

// How long a tile is held open after its stream stops appearing in the
// snapshot. An OBS reconnect or a playlist restart routinely outlasts a poll,
// and dropping the tile immediately reflows the whole grid under the viewer's
// cursor and resizes every other POV. It is a deliberate wait, not a delay in
// noticing: the tile says so, and carries its own Close button.
export const MISSING_GRACE_MS = 45000;

type Listener = () => void;

const listeners = new Set<Listener>();

// Listeners outside React: the media session pool (sessions.ts). Told before
// any component, so that by the time a tile re-renders the session of a stream
// that was just switched off has already stopped, and the sound has already
// moved, rather than both waiting on the order React happens to render in.
const outside = new Set<Listener>();

let watching: string[] = [];
let audio: string | null = null;
let openSidebar: (() => void) | null = null;

// Handle -> when it was first missing from the snapshot.
const missingSince = new Map<string, number>();

function notify(): void {
    Array.from(outside).forEach((listener) => listener());
    listeners.forEach((listener) => listener());
}

// For code that is not a component and has to hear about every change, in the
// same call that made it.
export function onWatchingChange(listener: Listener): () => void {
    outside.add(listener);

    return () => {
        outside.delete(listener);
    };
}

function useObservable<T>(read: () => T): T {
    const [, forceRender] = React.useState(0);

    React.useEffect(() => {
        const listener = () => forceRender((value) => value + 1);
        listeners.add(listener);

        return () => {
            listeners.delete(listener);
        };
    }, []);

    return read();
}

export const getWatching = (): string[] => watching;

export const getAudio = (): string | null => audio;

export const useWatching = (): string[] => useObservable(getWatching);

export const useAudio = (): string | null => useObservable(getAudio);

// setAudio moves the sound. Exactly one tile is ever audible: passing a handle
// mutes every other tile in the same render, so there is no moment in which two
// streams play at once.
export function setAudio(handle: string | null): void {
    const next = handle !== null && watching.includes(handle) ? handle : null;
    if (next === audio) {
        return;
    }

    audio = next;
    notify();
}

// watchOnly is the gesture the panel has always had: clicking a stream card
// means "show me this one", and everything else closes.
export function watchOnly(handle: string): void {
    if (watching.length === 1 && watching[0] === handle) {
        return;
    }

    watching = [handle];
    missingSince.delete(handle);

    // The tile that held the sound may have just been closed.
    if (audio !== handle) {
        audio = null;
    }

    notify();
}

// addWatching is the new gesture: show this one AS WELL, without disturbing
// what is already playing. No renegotiation of the existing tiles, and the
// sound does not move.
export function addWatching(handle: string): void {
    if (watching.includes(handle) || watching.length >= MAX_WATCHING) {
        return;
    }

    watching = [...watching, handle];
    missingSince.delete(handle);
    notify();
}

export function removeWatching(handle: string): void {
    if (!watching.includes(handle)) {
        return;
    }

    watching = watching.filter((candidate) => candidate !== handle);
    missingSince.delete(handle);

    // The sound is never handed to a neighbouring tile on its own: a stream
    // you were not listening to suddenly becoming audible is worse than
    // silence.
    if (audio === handle) {
        audio = null;
    }

    notify();
}

export function toggleWatching(handle: string): void {
    if (watching.includes(handle)) {
        removeWatching(handle);
        return;
    }

    addWatching(handle);
}

export const isWatching = (handle: string): boolean => watching.includes(handle);

// waitingFor reports whether a watched handle is currently missing from the
// snapshot, so its tile can say it is waiting rather than showing a dead frame.
export const waitingFor = (handle: string): boolean => missingSince.has(handle);

// reconcile gives up tiles whose stream has been gone for longer than the
// grace. It is called from an effect on every snapshot and must be silent when
// nothing changed: the membership check below is what stops it re-rendering,
// and therefore re-running, forever.
export function reconcile(liveHandles: string[], now: number = Date.now()): void {
    if (watching.length === 0) {
        missingSince.clear();
        return;
    }

    const live = new Set(liveHandles);
    const kept: string[] = [];

    // Release the sound as soon as its stream stops appearing, not when the
    // tile is finally given up. Otherwise the tile that comes back from a hold
    // tries to start unmuted with no click behind it, which browsers refuse —
    // and a refused play() leaves a black rectangle rather than a quiet one.
    let nextAudio = audio;
    if (nextAudio !== null && !live.has(nextAudio)) {
        nextAudio = null;
    }

    for (const handle of watching) {
        if (live.has(handle)) {
            missingSince.delete(handle);
            kept.push(handle);
            continue;
        }

        const since = missingSince.get(handle);
        if (since === undefined) {
            missingSince.set(handle, now);
            kept.push(handle);
            continue;
        }

        if (now - since < MISSING_GRACE_MS) {
            kept.push(handle);
            continue;
        }

        missingSince.delete(handle);
    }

    if (kept.length === watching.length && nextAudio === audio) {
        return;
    }

    watching = kept;
    audio = nextAudio !== null && kept.includes(nextAudio) ? nextAudio : null;

    notify();
}

// Switching everything off at once and keeping nothing: what the power switch
// on a player standing in for the whole watch list does. Closing the panel is
// not this — the list outlives the panel, as it always has.
export function clearWatching(): void {
    missingSince.clear();
    if (watching.length === 0 && audio === null) {
        return;
    }

    watching = [];
    audio = null;
    notify();
}

// ---- Keeping the list honest while the panel is not the one doing it ----
//
// The panel reconciles the list from an effect, on every snapshot it renders.
// Anything that shows a watched stream while the panel is not mounted needs the
// same done without it, so this subscribes to the snapshot directly.
//
// Two jobs, deliberately treated differently:
//
//   * A stream that has gone from the snapshot has its connection stopped at
//     once, whatever is or is not on screen. That is what happened before
//     there was a pool, when the tile's player was replaced by a waiting tile
//     and its cleanup hung up; the next player to mount negotiates afresh.
//   * Giving up a tile after its grace is only done while isActive() says
//     something is showing the list. Doing it while the panel is closed would
//     empty the watch list of somebody who closed the panel during an OBS
//     restart, and the list is meant to be there when they open it again.

// Bumped after each reconcile done here, for a surface that has to re-render
// when a stream starts waiting, which reconcile() does not announce.
let reconciled = 0;
const reconciledListeners = new Set<Listener>();

export function useReconciled(): number {
    const [, forceRender] = React.useState(0);

    React.useEffect(() => {
        const listener = () => forceRender((value) => value + 1);
        reconciledListeners.add(listener);

        return () => {
            reconciledListeners.delete(listener);
        };
    }, []);

    return reconciled;
}

// When the first tile still inside its grace runs out of it, or null.
function nextGraceEnd(): number | null {
    let earliest: number | null = null;
    for (const since of missingSince.values()) {
        const end = since + MISSING_GRACE_MS;
        if (earliest === null || end < earliest) {
            earliest = end;
        }
    }

    return earliest;
}

export function installReconciler(isActive: () => boolean): () => void {
    let timer = 0;

    const run = () => {
        window.clearTimeout(timer);
        timer = 0;

        const handles = getSnapshot().streams.map((stream) => stream.handle);
        sweepMissing(handles);

        if (!isActive()) {
            return;
        }

        reconcile(handles);
        reconciled++;
        Array.from(reconciledListeners).forEach((listener) => listener());

        // Snapshots only arrive when something changes, or every 30 seconds,
        // so a tile's grace would otherwise end whenever the next one happened
        // to come. A little past the end, so the check that it has ended
        // cannot land a millisecond short of it.
        const end = nextGraceEnd();
        if (end !== null) {
            timer = window.setTimeout(run, Math.max(0, end - Date.now()) + 20);
        }
    };

    const unsubscribe = subscribe(run);

    return () => {
        unsubscribe();
        window.clearTimeout(timer);
        timer = 0;
    };
}

export function setSidebarOpener(opener: (() => void) | null): void {
    openSidebar = opener;
}

// Opens the panel, and does nothing else: for a clip's Watch on a post
// in a channel, a clip link, and the lock (lockPending.ts). The same opener
// the profile popover's Watch uses, through the registration's
// showRHSPlugin, which shows the panel rather than toggling it. Never by
// dispatching Mattermost's own side-panel actions from here: away.ts follows
// the store and must see the panel arrive the way it always does.
export function openPanel(): void {
    openSidebar?.();
}

// ---- Opened by hand ----

// Whether the panel was just opened by somebody's own press — the App Bar or
// channel header button when it opens the panel, a profile popover's Watch, a
// shared clip's Watch, a clip link — stamped with when, so the lock screen may
// put its bearer token bar in focus (design/lock-screen.md 4.5). The lock
// takes it once, and only while it is fresh (OPENED_BY_HAND_MS), so a stamp
// nobody used can never focus the bar on a later, automatic opening.
let openedByHandAt = -Infinity;
const handListeners = new Set<Listener>();

export function markOpenedByHand(): void {
    openedByHandAt = clockNow();
    Array.from(handListeners).forEach((listener) => listener());
}

// When it was last stamped, and stamped no more: only one taker.
export function takeOpenedByHand(): number {
    const at = openedByHandAt;
    openedByHandAt = -Infinity;

    return at;
}

// For the lock, which may already be on screen when an opener fires.
export function onOpenedByHand(listener: Listener): () => void {
    handListeners.add(listener);

    return () => {
        handListeners.delete(listener);
    };
}

// watchStream is the profile popover's "Watch" button. It ADDS rather than
// replaces: the moment it gets used is a friend going live part way through a
// session, and throwing away the stream already playing is the opposite of
// what was asked for.
export function watchStream(handle: string): void {
    if (!watching.includes(handle) && watching.length < MAX_WATCHING) {
        watching = [...watching, handle];
        missingSince.delete(handle);
        notify();
    }

    // At the cap nothing is evicted. Throwing away a tile the viewer chose,
    // from a button on a different surface, is worse than opening the panel
    // and letting them see the stream sitting in the list with its own
    // control. Either way the panel opens, which is what the click asked for.
    openSidebar?.();
}

// resetWatching exists for tests and for uninitialize; nothing in the panel
// calls it.
export function resetWatching(): void {
    watching = [];
    audio = null;
    missingSince.clear();
    listeners.clear();
    outside.clear();
    reconciledListeners.clear();
    openSidebar = null;
}
