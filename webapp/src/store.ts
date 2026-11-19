import React from 'react';

import {fetchStreamsIfChanged} from './client';
import type {Snapshot} from './types';

// A tiny observable so both the sidebar and the profile popover can read the
// same live-stream snapshot without the plugin owning a Redux reducer.

type Listener = () => void;

const listeners = new Set<Listener>();

let snapshot: Snapshot = {streams: [], fetchedAt: 0, configured: true};

// When the current snapshot arrived, by this browser's own clock — not the
// server's fetchedAt, which another machine's clock wrote. Zero until one has.
let receivedAt = 0;

export function getSnapshot(): Snapshot {
    return snapshot;
}

export function snapshotAge(now: number = Date.now()): number {
    return receivedAt === 0 ? Infinity : now - receivedAt;
}

// The tag the snapshot in hand came under: the ETag of GET /api/v1/streams,
// or the tag its push carried, in quotes, which is the same tag. '' when it
// came with none (the lab's, or the empty one a sign-out leaves), so that the
// next read asks for the snapshot whatever is held.
let snapshotTag = '';

export function setSnapshot(next: Snapshot, tag = ''): void {
    snapshot = next;
    snapshotTag = tag;
    receivedAt = Date.now();
    listeners.forEach((listener) => listener());
}

// How many pushes have landed, so that a read can tell whether one landed
// while it was on its way.
let pushes = 0;

// A push: the snapshot and its tag, bare, as the streams event carries them.
export function setPushedSnapshot(next: Snapshot, tag: unknown): void {
    pushes++;
    setSnapshot(next, typeof tag === 'string' && tag !== '' ? `"${tag}"` : '');
}

// Reads the snapshot again, saying which one is in hand: the server answers
// "that one still" in a few bytes when nothing a person could see has
// changed, which is nearly every time (every change is pushed). Resolves to
// the snapshot as it now stands. A copy the server has just vouched for is as
// fresh as one just received.
export function refreshSnapshot(): Promise<Snapshot> {
    const sent = pushes;

    return fetchStreamsIfChanged(snapshotTag).then((fresh) => {
        takeRead(fresh, sent);

        return snapshot;
    });
}

// What a read came back with, `sent` being the count of pushes when it was
// asked. Never backwards: a push that landed while the read was on its way
// is newer than the read's answer, and stays. That includes a push with the
// same fetchedAt, which a change of the roster or the clips is (the server
// republishes the snapshot it holds with that one change); with no push
// meanwhile, an answer with the same fetchedAt is the one to keep, since it
// came under the server's newest tag.
//
// "That one still" is handed to everybody listening as a copy of the
// snapshot in hand, under the same tag, as every poll's answer was until
// 3.6.0: what is worked out from the clock as it is drawn (a card's "live
// for", the clapper's count of waiting drafts) moves on, and what waits for
// a new list of streams (the end of a tile's grace in a popped-out panel)
// gets one, with no request more than the few bytes of the answer.
function takeRead(fresh: {value: Snapshot; tag: string} | null, sent: number): void {
    if (fresh && (fresh.value.fetchedAt > snapshot.fetchedAt || (fresh.value.fetchedAt === snapshot.fetchedAt && pushes === sent))) {
        setSnapshot(fresh.value, fresh.tag);
    } else if (!fresh) {
        setSnapshot(JSON.parse(JSON.stringify(snapshot)) as Snapshot, snapshotTag);
    } else {
        receivedAt = Date.now();
    }
}

export function subscribe(listener: Listener): () => void {
    listeners.add(listener);

    return () => {
        listeners.delete(listener);
    };
}

export function useSnapshot(): Snapshot {
    const [, forceRender] = React.useState(0);

    React.useEffect(() => subscribe(() => forceRender((value) => value + 1)), []);

    return snapshot;
}

// ---- Polling while something is showing streams ----

// The WebSocket keeps this current; polling is only a safety net for a dropped
// connection. It is also what keeps the snapshot from going stale during a
// quiet spell, because the server only pushes when something changes, and
// preview stills are not taken from a stale list (stills.ts).
const REFRESH_MS = 30000;

// If the plugin endpoint is failing, back off hard rather than retrying on a
// fixed interval. A steady 30-second retry against an endpoint returning 4xx is
// indistinguishable from probing, and a reverse proxy's brute-force jail will
// ban the viewer for it.
const MAX_REFRESH_MS = 600000;

// How many surfaces are polling, and which run of the poll is current, so a
// request still in flight from a run that has ended cannot start another.
let pollers = 0;
let pollRun = 0;
let pollTimer = 0;

function startPolling(): void {
    const run = ++pollRun;
    let failures = 0;

    const tick = () => {
        const sent = pushes;
        fetchStreamsIfChanged(snapshotTag).then((fresh) => {
            if (run !== pollRun) {
                return;
            }
            failures = 0;
            takeRead(fresh, sent);
        }).catch(() => {
            failures = Math.min(failures + 1, 6);
        }).then(() => {
            if (run !== pollRun) {
                return;
            }
            const delay = Math.min(REFRESH_MS * Math.pow(2, failures), MAX_REFRESH_MS);
            pollTimer = window.setTimeout(tick, delay);
        });
    };

    tick();
}

// For an effect in anything that shows streams: the panel, and anything that
// shows one while the panel is away. Fetches at once and then every 30 seconds
// while at least one of them is mounted. Counted rather than switched, so a
// remount that overlaps its own cleanup cannot stop it, and two surfaces never
// poll twice.
export function pollWhileShown(): () => void {
    pollers++;
    if (pollers === 1) {
        startPolling();
    }

    let released = false;

    return () => {
        if (released) {
            return;
        }
        released = true;

        pollers--;
        if (pollers === 0) {
            pollRun++;
            window.clearTimeout(pollTimer);
            pollTimer = 0;
        }
    };
}
