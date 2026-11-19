import React from 'react';

import {getLockCause, noteGateMounted} from '../access';
import {now as clockNow} from '../clock';
import {getIdentity, latestAccess, refreshIdentity, setIdentity, useAccess, useIdentity} from '../identity';
import {LET_IN_WORDS, LINKED_WORDS, UNLOCKED_WORDS, WATCH_ONLY_WORDS, lockLine, lockedWords, mayWatch} from '../lockLogic';
import {PENDING_MS, lockOpenMs, reducedMotion} from '../motion';
import {nameNow} from '../name';

import {LockScreen} from './LockScreen';
import type {LockFace} from './LockScreen';
import {Sidebar} from './Sidebar';

// What Mattermost's side panel shows for the panel (registered in index.tsx in
// place of the Sidebar): the lock screen, or the panel, and never both
// (design/lock-screen.md 4.1). Nothing of the panel is mounted behind the
// lock, so nothing of it runs: no polling, no tiles, no stills, no clips.
//
// It reads "me" again each time it mounts, so access is always read fresh
// when the panel opens, and again when its window is looked at after a while
// (a popped-out panel may not hear the "me" event at all). What it shows
// comes from the identity (identity.ts accessNow): the panel for linked and
// let-in people — a fresh one when somebody moves between the two, so what it
// loads once is loaded for their rights — and the lock for everybody else. A
// store that does not answer after a definite answer keeps the panel, with a
// quiet notice; a page that never had an answer shows the lock's NO SIGNAL
// face, never LOCKED.

// On <body> while the panel is the side panel's, lock or panel: Mattermost's
// own close button in its header is hidden wherever there is another way to
// close it (see the stylesheet).
const OPEN_CLASS = 'vntv-rhs-open';

// A window looked at again reads "me" at most this often.
const LOOKED_AGAIN_MS = 10000;

export const PanelGate: React.FC = () => {
    const me = useIdentity();
    const access = useAccess();
    const latest = latestAccess();
    const watch = mayWatch(access);

    // Whether this mount's own read of "me" has come back, either way: until
    // it has, a page with no answer at all is starting, not NO SIGNAL.
    const [settled, setSettled] = React.useState(false);
    React.useEffect(() => {
        let alive = true;
        void refreshIdentity().then(() => {
            if (alive) {
                setSettled(true);
            }
        });

        return () => {
            alive = false;
        };
    }, []);

    React.useEffect(() => {
        document.body.classList.add(OPEN_CLASS);

        return () => document.body.classList.remove(OPEN_CLASS);
    }, []);

    React.useEffect(noteGateMounted, []);

    // An unlock, a let-in or a let-out in another window shows here when this
    // one is looked at, even if it never hears the "me" event.
    React.useEffect(() => {
        let lastAt = -Infinity;
        const lookAgain = () => {
            if (document.visibilityState === 'hidden' || Date.now() - lastAt < LOOKED_AGAIN_MS) {
                return;
            }

            lastAt = Date.now();
            void refreshIdentity();
        };

        window.addEventListener('focus', lookAgain);
        document.addEventListener('visibilitychange', lookAgain);

        return () => {
            window.removeEventListener('focus', lookAgain);
            document.removeEventListener('visibilitychange', lookAgain);
        };
    }, []);

    // The lock opening. The padlock swings open, UNLOCKED, the lock fades,
    // and the panel mounts with its own boot sweep (section 4.6); with less
    // motion asked for, at once. Said either way, from a live region of this
    // component's own, which outlives the swap: "Unlocked." for the reader's
    // own Unlock or an unlock elsewhere, or that an admin let them in. The
    // panel locking again is said there too, with the lock's line, which
    // says why: focus may be in the message box, where nothing else would
    // tell them.
    const [panelShown, setPanelShown] = React.useState(watch);
    const [unlocking, setUnlocking] = React.useState(false);
    const [said, setSaid] = React.useState({text: '', n: 0});
    const say = (text: string) => setSaid((current) => ({text, n: current.n + 1}));

    // This page's own Unlock was accepted: the opening it causes is said as
    // "Unlocked.", not as an admin letting them in. Spent by that opening, and
    // also forgotten whenever an opening ends or the lock comes back — an
    // Unlock answered after the lock had already begun to open (the access
    // event or a fresh read of "me" can win the race) would otherwise stay
    // noted for the life of the panel, and word a later let-in as "Unlocked."
    const ownUnlock = React.useRef(false);
    const timer = React.useRef(0);

    React.useEffect(() => {
        if (watch && !panelShown) {
            if (timer.current !== 0) {
                return;
            }

            const words = !ownUnlock.current && access === 'letIn' ? LET_IN_WORDS : UNLOCKED_WORDS;
            ownUnlock.current = false;
            say(words);

            if (reducedMotion()) {
                setPanelShown(true);

                return;
            }

            setUnlocking(true);
            timer.current = window.setTimeout(() => {
                timer.current = 0;
                ownUnlock.current = false;
                setUnlocking(false);
                setPanelShown(true);
            }, lockOpenMs());
        } else if (!watch && (panelShown || timer.current !== 0)) {
            window.clearTimeout(timer.current);
            timer.current = 0;
            ownUnlock.current = false;
            setUnlocking(false);
            setPanelShown(false);

            // A definite lock, not somebody signing out (who has no answer
            // at all). Anything waiting went with the access, so the line is
            // the reason the panel locked.
            if (access === 'locked') {
                const name = nameNow();
                say(lockedWords(lockLine({pending: null, cause: getLockCause(), noSignal: false, now: clockNow(), pendingMs: PENDING_MS, name}), name));
            }
        }
    }, [watch, panelShown, access]);

    // Moving between let in and linked remounts the panel (its footer
    // changes entirely, and focus lands on its root): said, so a screen
    // reader hears what changed and not only that it is in the panel again.
    const accessBefore = React.useRef(access);
    React.useEffect(() => {
        const before = accessBefore.current;
        accessBefore.current = access;
        if (!panelShown) {
            return;
        }
        if (before === 'letIn' && access === 'linked') {
            say(LINKED_WORDS);
        } else if (before === 'linked' && access === 'letIn') {
            say(WATCH_ONLY_WORDS);
        }
    }, [access, panelShown]);

    React.useEffect(() => () => window.clearTimeout(timer.current), []);

    // A lock that could not open after all — accepted on a page that never
    // had a "me" to open it with, and "me" still not to be had — is drawn
    // afresh, its bar ready for another try.
    const [lockKey, setLockKey] = React.useState(0);

    // The bar's token was accepted: linked now. Taken into the identity at
    // once, as the footer always has, and "me" read again behind it.
    const unlocked = React.useCallback((streamKey: string) => {
        ownUnlock.current = true;
        const base = getIdentity();
        if (base) {
            setIdentity({...base, streamKey, access: 'linked', clipsStreamer: undefined});
            void refreshIdentity();

            return;
        }

        void refreshIdentity().then((fresh) => {
            if (!fresh || !mayWatch(latestAccess())) {
                ownUnlock.current = false;
                setLockKey((key) => key + 1);
            }
        });
    }, []);

    // Focus when one root takes the other's place: to the new root, but only
    // when focus was in the old one — which the browser has by now dropped
    // to the page's body, the old root having gone — or nowhere. Anywhere
    // else, such as Mattermost's message box, it stays: the new root's stray
    // key rule would otherwise swallow what was being typed there.
    const rootRef = React.useRef<HTMLElement | null>(null);
    const setRoot = React.useCallback((element: HTMLElement | null) => {
        rootRef.current = element;
    }, []);
    const contentKey = panelShown && watch ? `panel:${access}` : `lock:${lockKey}`;
    const lastKey = React.useRef(contentKey);
    React.useLayoutEffect(() => {
        if (lastKey.current === contentKey) {
            return;
        }

        lastKey.current = contentKey;
        const active = document.activeElement;
        if (!active || active === document.body) {
            rootRef.current?.focus({preventScroll: true});
        }
    }, [contentKey]);

    let content: React.ReactNode;
    if (panelShown && watch) {
        content = (
            <Sidebar
                key={access}
                access={access === 'letIn' ? 'letIn' : 'linked'}
                onRoot={setRoot}
                accessUnchecked={latest === 'unknown'}
            />
        );
    } else {
        let face: LockFace = 'locked';
        if (access === 'unknown') {
            face = me === null && !settled ? 'starting' : 'noSignal';
        }

        content = (
            <LockScreen
                key={lockKey}
                me={me}
                face={face}
                unlocking={unlocking}
                onUnlocked={unlocked}
                rootRef={setRoot}
            />
        );
    }

    return (
        <>
            {content}

            {/* The same words said twice in a row are still said: a
                no-break space tells the two apart. */}
            <p
                className='vntv-sr-only'
                role='status'
                aria-live='polite'
            >{said.text === '' ? '' : `${said.text}${said.n % 2 === 0 ? ' ' : ''}`}</p>
        </>
    );
};
