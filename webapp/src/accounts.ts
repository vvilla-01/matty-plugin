import {accountsVisible} from './accountsLogic';
import {fetchAccountsIfChanged, fetchUsersByIds, RequestError} from './client';
import {now as clockNow} from './clock';
import {accessNow, onIdentityChange} from './identity';
import {displayNameOf, profileOf} from './mattermost';
import type {AccountsView} from './types';

// Everybody's Steam status and Hytale username (design/3.4.0.md 5.7), as the
// server last told this page: GET /api/v1/accounts, which is never in the
// snapshot, because the snapshot reaches locked readers too. Module-level,
// like store.ts, because the panel, its header and every profile popover are
// separate React trees.
//
// Asked only while the reader may watch (linked, or let in), and the newest
// request's answer wins; a 403 empties it. Nothing polls. It is asked when
// the reader comes to be allowed to watch (the first "me", the access
// event: identity.ts tells this module), on the accounts event (one read for
// a burst of them), on the "me" event, and when the panel or a profile popover
// opens if the last answer is a minute old. The reader losing the right to
// watch, signing out, and the plugin stopping all empty it, with a
// generation count, so that an answer already on its way cannot put back
// what was emptied.
//
// No React here, so check-away.mjs can load it; useAccounts.ts is the hook.
//
// The view names nobody: the Playing now line says who is in a game by
// their Mattermost name, which Mattermost's own store usually has. For
// anybody it has not loaded, the names are asked for here, once, and go with
// everything else when the store is emptied.

type Listener = () => void;

const listeners = new Set<Listener>();

let view: AccountsView | null = null;

// The tag the view in hand came under (the ETag of GET /api/v1/accounts),
// sent back with the next read of it.
let viewTag = '';

// When the newest answer came, by the page's clock; 0 for none.
let readAt = 0;

// The newest request's number (only its answer is taken), whether it is
// still on its way, and how many times the store has been emptied.
let sent = 0;
let inFlight = false;
let generation = 0;

// A read asked for by the accounts event, a second after the first of a
// burst, or by the "me" event, which joins it; 0 when none is waiting.
let eventTimer = 0;

// Mattermost names of people in a game whom Mattermost's own store has not
// loaded, by user id, and whom they have been asked for already (asked
// once: a person Mattermost does not return stays "Someone").
const names = new Map<string, string>();
const namesAsked = new Set<string>();

// An answer this old is asked for again when the panel or a popover opens.
export const ACCOUNTS_STALE_MS = 60000;

// The accounts event says only "something changed", to everybody, at most
// once every ten seconds; this joins whatever else comes within a second of
// the first into one read.
export const ACCOUNTS_EVENT_WAIT_MS = 1000;

function tell(): void {
    Array.from(listeners).forEach((listener) => listener());
}

export function getAccounts(): AccountsView | null {
    return view;
}

export function onAccounts(listener: Listener): () => void {
    listeners.add(listener);

    return () => {
        listeners.delete(listener);
    };
}

// Emptied: signing out, the plugin stopping, the reader no longer allowed to
// watch. A request on its way is not stopped, but its answer is dropped.
export function clearAccounts(): void {
    generation++;
    inFlight = false;
    readAt = 0;
    names.clear();
    namesAsked.clear();
    if (eventTimer !== 0) {
        window.clearTimeout(eventTimer);
        eventTimer = 0;
    }
    if (view !== null) {
        view = null;
        tell();
    }
}

// Somebody's name as Mattermost writes it beside their posts, for the
// Playing now line; '' while it is not known.
export function personName(userId: string): string {
    const profile = profileOf(userId);
    if (profile) {
        return displayNameOf(profile);
    }

    return names.get(userId) || '';
}

// The people in a game whose names Mattermost has not loaded, asked for in
// one request. Only they: nobody else in the view is named in the panel.
function lookUpNames(answer: AccountsView): void {
    const missing = Object.keys(answer.people).filter((userId) => answer.people[userId].steam?.inGame && !profileOf(userId) && !namesAsked.has(userId));
    if (missing.length === 0) {
        return;
    }

    missing.forEach((userId) => namesAsked.add(userId));
    const sentIn = generation;
    fetchUsersByIds(missing).then((users) => {
        if (sentIn !== generation) {
            return;
        }

        users.forEach((user) => names.set(user.id, displayNameOf(user)));
        tell();
    }, () => {
        // Unnamed for now: the line says "Someone" rather than nothing.
    });
}

export function loadAccounts(): Promise<void> {
    if (!accountsVisible(accessNow())) {
        clearAccounts();

        return Promise.resolve();
    }

    sent++;
    const ticket = sent;
    const sentIn = generation;
    inFlight = true;
    const current = () => ticket === sent && sentIn === generation;

    // With the tag of the answer in hand: nearly every re-read (the panel
    // opening, a reconnect) finds nothing changed, and the server says just
    // that. Only while there is an answer to keep: none is asked for whole.
    return fetchAccountsIfChanged(view ? viewTag : '').then((fresh) => {
        if (!current()) {
            return;
        }

        readAt = clockNow();
        if (!fresh) {
            return;
        }

        // people is always an object on the wire; a server that ever sent
        // null must not leave the panel reading a property of nothing.
        view = {...fresh.value, people: fresh.value.people || {}};
        viewTag = fresh.tag;
        tell();
        lookUpNames(view);
    }, (reason: unknown) => {
        if (!current()) {
            return;
        }

        // Refused: locked, or let out. Anything else keeps what was known.
        if (reason instanceof RequestError && reason.status === 403) {
            view = null;
            readAt = 0;
            tell();
        }
    }).then(() => {
        if (ticket === sent) {
            inFlight = false;
        }
    });
}

// The panel or a profile popover opening: asked again only when the last
// answer is a minute old, or there is none, and nothing is on its way.
export function loadAccountsIfOld(): void {
    if (inFlight) {
        return;
    }
    if (readAt === 0 || clockNow() - readAt >= ACCOUNTS_STALE_MS) {
        void loadAccounts();
    }
}

// The accounts event: everybody hears it, so a reader who may not watch
// does nothing with it (a read would only be refused).
export function accountsEventHeard(): void {
    if (!accountsVisible(accessNow()) || eventTimer !== 0) {
        return;
    }

    eventTimer = window.setTimeout(() => {
        eventTimer = 0;
        void loadAccounts();
    }, ACCOUNTS_EVENT_WAIT_MS);
}

// The "me" event (me.ts): one of the reader's own connections may have
// changed (connected, disconnected, blocked, taken over), and "me" is read
// again (`read`, which resolves when it has come back). The Connect flow
// listens here, to look then whether the window did its work, as Twitch's
// does in twitch.ts; and everybody's view is read again,
// since the reader's own row says what it says about them. With a flow open
// in this page, at once; otherwise with the accounts event's own read, which
// the same change usually sends everybody, at a random moment of up to a
// second, so that a "me" sent to everybody is not read by every open window
// in one instant.
type LinkListener = (read: Promise<unknown>) => void;

const linkListeners = new Set<LinkListener>();

export function accountLinkHeard(read: Promise<unknown>): void {
    Array.from(linkListeners).forEach((listener) => listener(read));
    if (linkListeners.size > 0) {
        void loadAccounts();

        return;
    }
    if (!accountsVisible(accessNow()) || eventTimer !== 0) {
        return;
    }

    eventTimer = window.setTimeout(() => {
        eventTimer = 0;
        void loadAccounts();
    }, Math.floor(Math.random() * ACCOUNTS_EVENT_WAIT_MS));
}

export function onAccountLink(listener: LinkListener): () => void {
    linkListeners.add(listener);

    return () => {
        linkListeners.delete(listener);
    };
}

// Following the reader's access, from index.tsx (and the lab): read as soon
// as "me" says they may watch, and emptied the moment it says they may not.
// The "me" event comes here this way too: it reads "me" again. Returns
// what stops it.
export function followAccountsAccess(): () => void {
    let visible = accountsVisible(accessNow());
    if (visible) {
        void loadAccounts();
    }

    return onIdentityChange(() => {
        const next = accountsVisible(accessNow());
        if (next === visible) {
            return;
        }

        visible = next;
        if (next) {
            void loadAccounts();
        } else {
            clearAccounts();
        }
    });
}
