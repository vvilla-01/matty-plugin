import React from 'react';

import {fetchAccess, fetchMe} from './client';
import {now as clockNow} from './clock';
import {rememberBanner} from './banner';
import {accessOfMe, isDefinite, shownAccessOf} from './lockLogic';
import type {Access, DefiniteAccess} from './lockLogic';
import {currentUserId, mattermostStore} from './mattermost';
import {takeBrand} from './name';
import type {Me} from './types';

// Who is reading, for the places outside the panel that need to know, and for
// the panel itself in the moment after it opens.
//
// Module-level for the same reason store.ts is: the right-hand sidebar TITLE is
// a separate React tree from the panel underneath it — Mattermost renders them
// in different places — so the trophy button in the header cannot be handed
// anything by the Sidebar through props.
//
// Fetched once when the plugin loads, and kept up to date after that by the
// panel, with the "me" it fetches each time it opens and with a link made or
// undone from its footer. Most of the page only needs the user id, which never
// changes; the panel also reads the link from here, so that it opens already
// knowing whether the reader can plan a stream rather than finding out a
// moment later and moving the Up next row under them (see Sidebar).
//
// Clips read the link from here too, and everything about them is for people
// who have linked their stream. So anything that finds the link has changed —
// a clip route answering not_linked, a sign-in after the page loaded — asks
// for "me" again with refreshIdentity(), and every surface follows. Signing
// out forgets it (setIdentity(null)), so nothing is shown or asked for on
// the strength of the person who has gone.
//
// And the lock (design/lock-screen.md): what the reader may do in the panel,
// "me".access, read here by everything that shows or hides something for
// it. The latest answer, and the last DEFINITE one this page had, so that a
// store that does not answer for a moment ("unknown", or "me" not read at
// all) neither locks somebody it knew nor unlocks anybody (shownAccessOf).

type Listener = () => void;

const listeners = new Set<Listener>();

let me: Me | null = null;

// A request to open the panel's bearer token bar (Link your stream, for
// somebody let in), waiting for the panel to take it, and who wants to hear
// about one. See requestLinkForm below.
let linkRequested = false;
const linkListeners = new Set<Listener>();

// How many times the identity has been cleared, which uninitialize does. A
// refresh that was asked for before then is about a plugin that has since
// been switched off, and is dropped rather than put back.
let clearings = 0;

// The last definite access this page had (linked, let in or locked), until
// somebody signs out; and whether the newest read of "me" gave no definite
// answer — it failed outright, or the store did not answer.
let lastDefinite: DefiniteAccess | null = null;
let readFailed = false;

// Who Mattermost says is signed in, which a "me" has to be about. Without
// Mattermost's store (the moment after the plugin is switched off) the "me"
// is taken at its word, as clips.ts linkOf does.
function signedInFor(next: Me): string {
    return mattermostStore() ? currentUserId() : next.userId;
}

export function setIdentity(next: Me | null): void {
    if (next === null) {
        clearings++;

        // A request to open the link form was somebody's, signed in, in a
        // plugin that is now going away: not one for whatever loads next.
        linkRequested = false;
        lastDefinite = null;
    } else {
        const access = accessOfMe(next, signedInFor(next));
        if (isDefinite(access)) {
            lastDefinite = access;
        }

        // The banner as the server sent it, drawn on the next reload until
        // "me" answers there (banner.ts).
        rememberBanner(next);
    }

    me = next;
    readFailed = false;
    listeners.forEach((listener) => listener());
}

// The newest answer: 'unknown' while "me" is not known, or the last read of
// it failed.
export function latestAccess(): Access {
    if (readFailed || !me) {
        return 'unknown';
    }

    return accessOfMe(me, signedInFor(me));
}

export function lastDefiniteAccess(): DefiniteAccess | null {
    return lastDefinite;
}

// What the page goes by: the newest definite answer, or the last one.
export function accessNow(): Access {
    return shownAccessOf(latestAccess(), lastDefinite);
}

// Told whenever "me" or what is known about it changes, outside React: for
// the access follower in access.ts. Returns what stops it.
export function onIdentityChange(listener: Listener): () => void {
    listeners.add(listener);

    return () => {
        listeners.delete(listener);
    };
}

// accessNow(), for a component, which re-renders when it changes.
export function useAccess(): Access {
    useIdentity();

    return accessNow();
}

export function getIdentity(): Me | null {
    return me;
}

export function useIdentity(): Me | null {
    const [, forceRender] = React.useState(0);

    React.useEffect(() => {
        const listener = () => forceRender((value) => value + 1);
        listeners.add(listener);

        return () => {
            listeners.delete(listener);
        };
    }, []);

    return me;
}

// The one request in flight, which every caller shares: a channel full of
// shared clips that all hear not_linked at once asks once.
let refreshing: Promise<Me | null> | null = null;

// When a read of "me" last finished, either way, for the re-read while
// something plays (access.ts): a read for any reason counts.
let lastReadAt = 0;

export const lastIdentityReadAt = (): number => lastReadAt;

// Asks the server who is reading again, and tells everybody listening. It
// never rejects: it resolves to the new identity, or to null when it could not
// be read, in which case the old one is kept — a failed request is not news
// that somebody unlinked.
export function refreshIdentity(): Promise<Me | null> {
    if (refreshing) {
        return refreshing;
    }

    const asked = clearings;
    const request: Promise<Me | null> = fetchMe().then((fresh) => {
        if (asked !== clearings) {
            return null;
        }

        // An answer says what the plugin is called and which pictures it has
        // (name.ts), whatever it says about the reader.
        takeBrand(fresh);

        // The store did not answer: for a reader already known, nothing that
        // was known is replaced — their stream key, their switch, Pop out —
        // by the blank answer an unknown "me" carries; the page only notes
        // that it could not check. A page with no "me" at all takes it, and
        // shows the lock's NO SIGNAL face.
        if (fresh?.access === 'unknown' && me && me.userId === fresh.userId) {
            if (!readFailed) {
                readFailed = true;
                listeners.forEach((listener) => listener());
            }

            return fresh;
        }

        setIdentity(fresh);

        return fresh;
    }).catch(() => {
        // Kept, as said above; but the page now knows its newest read
        // failed, which the panel says quietly and a fresh lock shows as NO
        // SIGNAL.
        if (asked === clearings && !readFailed) {
            readFailed = true;
            listeners.forEach((listener) => listener());
        }

        return null;
    }).then((result) => {
        refreshing = null;
        lastReadAt = clockNow();

        return result;
    });
    refreshing = request;

    return request;
}

// A read of "me" that starts after the call: for news that something has
// changed (the "me" event), which an answer already on its way may have been
// made before. A read on its way is let finish, and one more is made after
// it; whoever asks meanwhile shares that one, so a burst of events costs one
// read more at most. Not after the identity has been cleared since.
let following: Promise<Me | null> | null = null;

export function refreshIdentityAfter(): Promise<Me | null> {
    if (!refreshing) {
        return refreshIdentity();
    }
    if (!following) {
        const asked = clearings;
        following = refreshing.then(() => {
            following = null;

            return asked === clearings ? refreshIdentity() : null;
        });
    }

    return following;
}

// The small question: is the reader's access still what this page holds? For
// the reads that are about nothing else, which are also the ones made most
// often: once a minute while something plays, and after a request was refused
// (access.ts). It asks GET /api/v1/access, which costs the server one cached
// lookup where "me" costs it five or six reads of its store, and "me" itself
// is read only when the answer is not what is held: then something has
// changed, and everything "me" carries may have changed with it. A page with
// no "me" yet reads all of it. Like refreshIdentity, it never rejects, and a
// read that fails or that the store could not answer takes nothing away.
let checking: Promise<void> | null = null;

export function refreshAccess(): Promise<void> {
    if (!me) {
        return refreshIdentity().then(() => undefined);
    }
    if (refreshing) {
        return refreshing.then(() => undefined);
    }
    if (checking) {
        return checking;
    }

    const asked = clearings;
    const held = me;
    const request = fetchAccess().then((answer) => {
        if (asked !== clearings) {
            return undefined;
        }

        // As for "me": the name and the artwork.
        takeBrand(answer);
        if (me !== held) {
            return undefined;
        }

        // The store did not answer: what was known stands, and the page
        // notes that it could not check, as for "me".
        if (answer?.access === 'unknown' || typeof answer?.access !== 'string') {
            if (!readFailed) {
                readFailed = true;
                listeners.forEach((listener) => listener());
            }

            return undefined;
        }

        if (answer.access !== held.access || (typeof answer.configured === 'boolean' && answer.configured !== held.configured)) {
            return refreshIdentity().then(() => undefined);
        }

        // Still as held: a good read, which clears an earlier failed one.
        if (readFailed) {
            readFailed = false;
            listeners.forEach((listener) => listener());
        }

        return undefined;
    }).catch(() => {
        if (asked === clearings && !readFailed) {
            readFailed = true;
            listeners.forEach((listener) => listener());
        }
    }).then(() => {
        checking = null;
        lastReadAt = clockNow();
    });
    checking = request;

    return request;
}

// ---- Asking for the link form from outside the panel ----

// For somebody let in to watch only, "Link your stream" opens the bearer
// token bar in the panel's footer, whose open state is the Sidebar's own. The
// clip editor's "link" action cannot reach it, so it leaves a request here
// (lockPending.ts openLock), opens the panel, and the Sidebar takes the
// request — when it mounts, or at once if it already is — and opens its bar.
// For a locked reader the same press opens the lock instead.
export function requestLinkForm(): void {
    linkRequested = true;
    linkListeners.forEach((listener) => listener());
}

// Whether a request was waiting, and it is not any more. Only one taker gets
// it: two panels, the main window's and a popped-out one, do not both open
// their form for one click.
export function takeLinkRequest(): boolean {
    if (!linkRequested) {
        return false;
    }

    linkRequested = false;
    linkListeners.forEach((listener) => listener());

    return true;
}

// Whether a request is waiting, for the Sidebar's effect to take it.
export function useLinkRequest(): boolean {
    const [, forceRender] = React.useState(0);

    React.useEffect(() => {
        const listener = () => forceRender((value) => value + 1);
        linkListeners.add(listener);

        return () => {
            linkListeners.delete(listener);
        };
    }, []);

    return linkRequested;
}

// ---- Signing in after the page loaded ----

// Mattermost's store, as far as this reads it: who is signed in.
type StoreLike = {
    getState: () => unknown;
    subscribe: (listener: () => void) => () => void;
};

function signedInAs(store: StoreLike): string {
    try {
        const state = store.getState() as {entities?: {users?: {currentUserId?: string}}} | undefined;

        return state?.entities?.users?.currentUserId || '';
    } catch {
        return '';
    }
}

// Calls onSignedOut whenever the person who was signed in no longer is, and
// onSignedIn whenever somebody signs in, without the page loading again; and
// returns what stops it listening. A change straight from one person to
// another is both, in that order.
//
// Mattermost starts plugins on the sign-in page, before anybody has signed
// in, and does not start them again afterwards: signing in with a password
// moves to the app inside the same page. So the "me" the plugin asked for as
// it started was refused, and nothing outside the panel would ever ask again
// — a linked reader's clapper and shared clips would wait for the
// panel to be opened. Mattermost's store says who is signed in, so a change
// to somebody (from nobody, or from somebody else) is the moment to ask.
//
// Signing out does not load the page again either: Mattermost clears who is
// signed in and moves to its sign-in page in place (actions/global_actions
// emitUserLoggedOutEvent: logout, then a history push), and an expired
// session ends the same way. The plugin stays loaded, so whatever it knew
// about the person — who they are, their link, their clips and drafts —
// has to be forgotten then, or the next person to sign in on that page would
// be shown it, and asked for as if they were them, until their own "me"
// arrived.
export function followSignIn(store: StoreLike, onSignedIn: () => void, onSignedOut: () => void): () => void {
    let last = signedInAs(store);

    return store.subscribe(() => {
        const next = signedInAs(store);
        if (next === last) {
            return;
        }

        const before = last;
        last = next;
        if (before !== '') {
            onSignedOut();
        }
        if (next !== '') {
            onSignedIn();
        }
    });
}
