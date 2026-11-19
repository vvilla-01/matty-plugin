import React from 'react';

import {closeClips, closeEditor, dropClipCaches, getClipEditor, getClipsDialog, openWaitingClip} from './clips';
import {loadAccounts} from './accounts';
import {setAccessRefusalListener} from './client';
import {now as clockNow, onClockAdvance} from './clock';
import {getIdentity, lastDefiniteAccess, lastIdentityReadAt, latestAccess, onIdentityChange, refreshAccess, refreshIdentity, refreshIdentityAfter, accessNow} from './identity';
import {accessChangeOf, fresh, isLinked, lossCauseOf, looksLikeToken} from './lockLogic';
import type {DefiniteAccess, LockCause} from './lockLogic';
import {nameNow} from './name';
import {clearPending, takePending} from './lockPending';
import {ACCESS_RECHECK_MS} from './motion';
import {sessionCount, stopAllSessions} from './sessions';
import {getSnapshot, refreshSnapshot} from './store';
import {addWatching, clearWatching} from './watching';

// Following the reader's access while the page is open (design/lock-screen.md
// 3.9 and 4.9): hearing that it may have changed, reading "me" again, and
// undoing what they may no longer have.
//
// The server decides on every request; this is the page keeping up with it.
// It hears of a change from whichever comes first: the content-free "me"
// event the server sends that one person, a WebSocket reconnect (an event sent
// while the socket was down may never arrive), any request refused for access
// (client.ts), the window being looked at again (PanelGate), and — because a
// stream already playing makes no requests at all, and nothing can end it
// from outside — a re-read every minute while anything plays. Every one of
// them only ever reads "me" again: none of them is the thing that unlocks.
//
// Installed once per window by index.tsx, and by the lab, which presses the
// very same handlers from its buttons (the "me" event's is me.ts meHeard).

// ---- What was undone, and why ----

type Listener = () => void;

let cause: LockCause | null = null;
const causeListeners = new Set<Listener>();

function setCause(next: LockCause | null): void {
    if (cause !== next) {
        cause = next;
        Array.from(causeListeners).forEach((listener) => listener());
    }
}

export const getLockCause = (): LockCause | null => cause;

export function useLockCause(): LockCause | null {
    const [, forceRender] = React.useState(0);

    React.useEffect(() => {
        const listener = () => forceRender((value) => value + 1);
        causeListeners.add(listener);

        return () => {
            causeListeners.delete(listener);
        };
    }, []);

    return cause;
}

// The reader pressed Unlink in this page: the lock that follows says so,
// rather than that somebody else unlinked them. Noted as the request is sent,
// not when it answers — the "me" event, or another read of "me", can bring
// the lock before the answer does — and forgotten if it fails.
let selfUnlinkAt = -Infinity;
const SELF_UNLINK_MS = 30000;

export function noteSelfUnlink(sent = true): void {
    selfUnlinkAt = sent ? clockNow() : -Infinity;
}

// How many PanelGates are mounted in this window: a stream asked for from the
// lock is started only where there is a panel to show it.
let gates = 0;

export function noteGateMounted(): () => void {
    gates++;

    return () => {
        gates--;
    };
}

// What a locked reader may no longer have, all of it at once: every stream
// stopped and the watch list emptied (so no small player either), everything
// fetched about clips dropped, the Clips dialog and the editor closed, and
// whatever was waiting for the lock forgotten.
function tearDown(): void {
    stopAllSessions('locked');
    clearWatching();
    closeEditor(false);
    closeClips();
    dropClipCaches();
    clearPending();
}

// The lock opened: what was asked for while it was shown happens now, if it
// is still wanted — the stream, while it is still live and there is a panel
// here to show it; the clip, in the Clips dialog.
function takeWhatWasWaiting(): void {
    const item = takePending();
    if (item?.kind === 'stream' && gates > 0 && getSnapshot().streams.some((stream) => stream.handle === item.handle)) {
        addWatching(item.handle);
    }
    openWaitingClip(item?.kind === 'clip' ? item.clipId : null);
}

let previous: DefiniteAccess | null = null;
let unknownTimer = 0;
const UNKNOWN_AGAIN_MS = 5000;

function followAccess(): void {
    const latest = latestAccess();
    if (latest === 'unknown') {
        // Nothing is torn down for a store that did not answer, or a "me"
        // that could not be read: the server's 503 already keeps anything new
        // from starting, and a blip must not shut out somebody it knew. One
        // more read a moment later. A cleared identity is somebody signing
        // out, which signedOut() deals with.
        if (getIdentity() === null) {
            previous = null;

            return;
        }
        if (previous !== null && unknownTimer === 0) {
            unknownTimer = window.setTimeout(() => {
                unknownTimer = 0;
                refreshIdentity();
            }, UNKNOWN_AGAIN_MS);
        }

        return;
    }

    window.clearTimeout(unknownTimer);
    unknownTimer = 0;

    const before = previous;
    previous = latest;
    switch (accessChangeOf(before, latest)) {
    case 'lost':
        setCause(lossCauseOf(before, fresh(selfUnlinkAt, clockNow(), SELF_UNLINK_MS)));
        selfUnlinkAt = -Infinity;
        tearDown();
        break;
    case 'downgraded':
        // Linked to let in: the streams play on, but clipping goes — the
        // editor closes, and drafts and the clips list fetched with a linked
        // reader's rights are dropped, to be fetched again as watch-only.
        selfUnlinkAt = -Infinity;
        closeEditor(false);
        dropClipCaches();
        break;
    case 'gained':
        setCause(null);
        takeWhatWasWaiting();
        break;
    default:
        break;
    }
}

// Somebody signed out without the page loading again: nothing of theirs
// carries over to whoever signs in next — no tiles, no small player, nothing
// waiting for a lock that was theirs.
export function signedOut(): void {
    stopAllSessions('signed out');
    clearWatching();
    clearPending();
    setCause(null);
    previous = null;
    selfUnlinkAt = -Infinity;
}

// ---- Hearing that access may have changed ----

// The "me" event, which says only "read me again": the whole of "me", since
// what changed may be the reader's access or one of their connections. Once
// now, by a read that starts after the event (one already on its way may
// have been answered before the change); and a little later, by when another
// node of a cluster has caught up, the small question of whether their
// access is still what was read. That one at a random moment of up to three
// seconds more, so that a "me" sent to everybody (an admin deleting all of a
// service's data) is not asked about by every open window in one instant.
const EVENT_AGAIN_MS = 12000;
const EVENT_SPREAD_MS = 3000;
let eventTimer = 0;

// Resolves when that first read has come back, either way: what the others
// who hear the event wait for before they look at "me" (me.ts).
export function accessEventHeard(): Promise<unknown> {
    const read = refreshIdentityAfter();
    window.clearTimeout(eventTimer);
    eventTimer = window.setTimeout(() => {
        eventTimer = 0;
        refreshAccess();
    }, EVENT_AGAIN_MS + Math.floor(Math.random() * EVENT_SPREAD_MS));

    return read;
}

// After every WebSocket reconnect: an event sent while it was down may never
// come, and nor may a push. So "me" is read again, and the snapshot and
// everybody's accounts are asked for with the tag of the copy in hand, which
// the server answers "that one still" unless something was missed.
export function socketReconnected(): void {
    refreshIdentity();
    refreshSnapshot().catch(() => {
        // The poll, or the next push, will bring it.
    });
    void loadAccounts();
}

// A request refused for access, from anywhere in the page: "me" again, at
// most once every two seconds however many are refused at once.
const REFUSAL_FLOOR_MS = 2000;
let lastRefusalRead = -Infinity;

export function accessRefused(): void {
    const at = clockNow();
    if (at - lastRefusalRead < REFUSAL_FLOOR_MS) {
        return;
    }

    lastRefusalRead = at;
    refreshAccess();
}

// While anything plays in this window — a tile, the small player, a clip — the
// reader's access is asked for at least once a minute. A stream already
// playing asks the server nothing, and pictures and clip media are refused
// through <img> and <video>, which never tell the page; this is what stops
// them within a minute of a let-out the page did not hear about.
//
// While this window is looked at, that is a read of the whole of "me", as it
// always was: the panel and the small player show what only "me" carries and
// no event announces (an admin's alerts, the banner, the reader's own stream
// being live, whether there is a Pop out), and it shows within a minute. In a
// window nobody is looking at (a tab in the background, playing for its
// sound) it is the small question (GET /api/v1/access: see identity.ts
// refreshAccess): nothing is on show there to be late. Looked at again, the
// next minute's read is of "me", and an open panel reads it at once
// (PanelGate).
function playing(): boolean {
    const dialog = getClipsDialog();

    return sessionCount() > 0 || (dialog !== null && dialog.view !== 'locked') || getClipEditor() !== null;
}

function windowLookedAt(): boolean {
    return typeof document === 'undefined' || document.visibilityState !== 'hidden';
}

export function recheckWhilePlaying(): void {
    if (playing() && clockNow() - lastIdentityReadAt() >= ACCESS_RECHECK_MS) {
        if (windowLookedAt()) {
            refreshIdentity();
        } else {
            refreshAccess();
        }
    }
}

// How often the re-read looks whether it is due: far more often than it is,
// so it is never much more than a minute late.
const RECHECK_LOOK_MS = 10000;

// ---- A token about to be posted to a channel ----

let tokenBars = 0;

// A bearer token bar on screen (the lock, the let-in footer's, the relink
// form's): while one is, a message shaped like a token is refused.
export function noteTokenBarOpen(): () => void {
    tokenBars++;

    return () => {
        tokenBars--;
    };
}

// What the message box is told, with the server's name for the plugin.
export const tokenPostRefusal = (name: string): string => `That looks like your bearer token. Paste it into the bar in the ${name} panel instead.`;

// Mattermost's MessageWillBePosted hook. With synced drafts on, a token that
// reached the message box is already saved on the server as a draft, so this
// is the belt: the real defence is that the lock's keys never send a paste
// there (focusTrap.ts keepTokenKeys).
export function messageWillBePosted<P extends {message?: unknown}>(post: P): {post: P} | {error: {message: string}} {
    const guarded = !isLinked(accessNow()) || tokenBars > 0;
    if (guarded && looksLikeToken(typeof post?.message === 'string' ? post.message : '')) {
        return {error: {message: tokenPostRefusal(nameNow())}};
    }

    return {post};
}

// ---- Installing it ----

// As Mattermost 11.11 has them (webapp/channels/src/plugins/registry.ts).
type Registry = {
    registerWebSocketEventHandler: (event: string, handler: (message: unknown) => void) => void;
    unregisterWebSocketEventHandler: (event: string) => void;
    registerReconnectHandler: (handler: () => void) => void;
    unregisterReconnectHandler: () => void;
    registerMessageWillBePostedHook: (hook: (post: {message?: unknown}) => unknown) => unknown;
};

// Everything above, wired to one window's registry. The event's name is
// custom_<plugin id>_me, and heard is what it does: accessEventHeard, with
// whatever else follows the same event (me.ts meHeard). Returns what
// undoes it, for uninitialize.
export function installAccess(registry: Registry, eventName: string, heard: (message?: unknown) => void = accessEventHeard): () => void {
    previous = lastDefiniteAccess();
    const unfollow = onIdentityChange(followAccess);
    setAccessRefusalListener(accessRefused);

    registry.registerWebSocketEventHandler(eventName, heard);
    registry.registerReconnectHandler(socketReconnected);
    registry.registerMessageWillBePostedHook(messageWillBePosted);

    const timer = window.setInterval(recheckWhilePlaying, RECHECK_LOOK_MS);
    const unclock = onClockAdvance(recheckWhilePlaying);

    return () => {
        unfollow();
        unclock();
        window.clearInterval(timer);
        window.clearTimeout(eventTimer);
        window.clearTimeout(unknownTimer);
        eventTimer = 0;
        unknownTimer = 0;
        setAccessRefusalListener(null);
        registry.unregisterWebSocketEventHandler(eventName);
        registry.unregisterReconnectHandler();
        previous = null;
        setCause(null);
    };
}
