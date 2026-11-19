import React from 'react';

import {getAway, windowKind} from './away';
import {now as clockNow, onClockAdvance} from './clock';
import {accessNow, requestLinkForm} from './identity';
import {fresh} from './lockLogic';
import type {LockPendingItem} from './lockLogic';
import {PENDING_MS} from './motion';
import {markOpenedByHand, openPanel} from './watching';

// What a locked reader asked for from outside the panel, and the way they are
// sent to the lock to unlock it (design/lock-screen.md section 7).
//
// Outside the panel everything stays visible to everybody: the LIVE badge,
// medals, the profile popover's live row, shared clips' cards. For somebody
// who may not watch, every action there opens the lock instead, and what they
// asked for is remembered here so that it happens once they unlock: the
// popover's stream starts, the card's clip opens.
//
// Only the latest thing asked for, in memory, for this page alone, and
// stamped: it lapses after PENDING_MS, so an unlock hours later does not
// start somebody's stream unasked. It is let go on sign-out, when access is
// lost (access.ts), once it has been used, and on a plain opening of the
// panel from its own button, which asks for the panel and not for that stream.
// A clip asked for is also kept in the Clips dialog's own state ({view:
// 'locked'}), which clips.ts lets go when something else replaces it here.

type Listener = () => void;

let pending: LockPendingItem | null = null;
const listeners = new Set<Listener>();

function changed(): void {
    Array.from(listeners).forEach((listener) => listener());
}

export type PendingRequest =
    | {kind: 'stream'; handle: string; name: string}
    | {kind: 'clip'; clipId: string};

export function setPending(item: PendingRequest): void {
    pending = {...item, at: clockNow()};
    changed();
}

export function clearPending(): void {
    if (pending !== null) {
        pending = null;
        changed();
    }
}

// What is waiting, while it is still wanted; null otherwise.
export function getPending(): LockPendingItem | null {
    return pending && fresh(pending.at, clockNow(), PENDING_MS) ? pending : null;
}

// What is waiting, and nothing waiting any more: for the moment the lock
// opens. Lapsed is the same as nothing.
export function takePending(): LockPendingItem | null {
    const item = getPending();
    clearPending();

    return item;
}

export function onPendingChange(listener: Listener): () => void {
    listeners.add(listener);

    return () => {
        listeners.delete(listener);
    };
}

// The lock subscribes, so a second Watch pressed while it is open changes its
// line in place, and the line goes when what was asked for lapses — by the
// lab's clock too.
export function usePending(): LockPendingItem | null {
    const [, forceRender] = React.useState(0);
    const item = getPending();

    React.useEffect(() => {
        const again = () => forceRender((value) => value + 1);
        const unlisten = onPendingChange(again);
        const unclock = onClockAdvance(again);

        return () => {
            unlisten();
            unclock();
        };
    }, []);

    const lapsesAt = item ? item.at + PENDING_MS : 0;
    React.useEffect(() => {
        if (lapsesAt === 0) {
            return undefined;
        }

        const timer = window.setTimeout(() => forceRender((value) => value + 1), Math.max(0, lapsesAt - clockNow()) + 50);

        return () => window.clearTimeout(timer);
    }, [lapsesAt]);

    return item;
}

// The one way to the lock from outside the panel: a profile popover's Watch,
// a shared clip's Watch and picture, a clip link, and the clip editor's
// "link" action. Remembers what was asked for, then opens what can unlock it:
//
//   - for somebody let in (watch only), the panel's own bearer token bar, in
//     its footer (the link request identity.ts keeps);
//   - for anybody else, the panel itself, which is the lock. In the main
//     window and a popped-out channel, which have a side panel; any other
//     popped-out window has nothing to open, and its cards say so instead.
//
// Each of these is the person's own press, so the lock may put the bar in
// focus (watching.ts markOpenedByHand).
export function openLock(item?: PendingRequest): void {
    if (item) {
        setPending(item);
    }

    if (accessNow() === 'letIn') {
        requestLinkForm();
    }

    if (windowKind() === 'other-popout') {
        return;
    }

    markOpenedByHand();
    openPanel();
}

// The App Bar and channel header buttons, which toggle the panel: a press that
// opens it is the person's own opening, and a plain one, which asks for
// the panel rather than for whatever was pending. A press that closes it sets
// nothing. Wraps the registration's toggle, read the way away.ts reads the
// side panel.
export function openerPress(toggle: () => void): () => void {
    return () => {
        if (getAway().panel !== 'villanet') {
            clearPending();
            markOpenedByHand();
        }
        toggle();
    };
}
