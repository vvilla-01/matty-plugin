import React from 'react';

import {unlinkAccount, unlinkTwitch} from '../client';
import {accountsLineShown, connectionsOf, disconnectFailed, disconnectLineOf, justDisconnected} from '../accountsLogic';
import type {AnyService, Connection} from '../accountsLogic';
import {isPasteKey} from '../focusTrap';
import {refreshIdentity} from '../identity';
import {nameNow, useName} from '../name';
import {useTwoClicks} from '../twoClicks';
import type {Me} from '../types';

import {DISCONNECT_FAILED as TWITCH_DISCONNECT_FAILED, JUST_DISCONNECTED as TWITCH_JUST_DISCONNECTED, TwitchDisconnectKey} from './TwitchConnect';

// How long the words after a Disconnect stay, as on Twitch's key.
const DISCONNECTED_MS = 6000;

// A printable key, or a paste, pressed on the words, which take no typing:
// stopped there in the footer, as on the panel itself, or Mattermost would
// send it to its message box (TwitchConnect.tsx's rule).
function strayKey(event: React.KeyboardEvent<HTMLElement>): boolean {
    const command = event.ctrlKey || event.metaKey;

    return isPasteKey(event) || (event.key.length === 1 && !command && !event.altKey);
}

// Twitch's part keeps Twitch's own call, as its key does.
function unlink(service: AnyService): Promise<unknown> {
    return service === 'twitch' ? unlinkTwitch() : unlinkAccount(service);
}

function saidAfter(service: AnyService): string {
    return service === 'twitch' ? TWITCH_JUST_DISCONNECTED : justDisconnected(service, nameNow());
}

function failedWords(service: AnyService): string {
    return service === 'twitch' ? TWITCH_DISCONNECT_FAILED : disconnectFailed(service);
}

type Props = {me: Me | null; where?: 'lock' | 'watchonly'};

// Disconnect for somebody who is not linked (design/3.4.0.md 5.5, Victor's
// P3 pick, B): a connection survives unlinking, so the lock screen and the
// WATCH ONLY line need a way to withdraw from each service, and with three
// services it must not become three lines. Exactly where TwitchDisconnectKey
// was: under the lock's docked bar (under the not-set-up words where there
// is no bar), and on a line of its own over the WATCH ONLY bar; after Unlock
// in the tab order. Never Connect, in either place.
//
// With Twitch alone it is TwitchDisconnectKey itself, unchanged. With one
// other service, one key, "Disconnect Steam". With more, one line of words,
// "Disconnect  Twitch · Steam · Hytale", each name a key of its own; a
// first press turns the whole line into that one service's own two-press
// words, "Disconnect Steam? Press again", for four seconds, and then the
// line comes back. Never cut short: the pressed word alone ("Steam? Press
// again") would make the line too wide at 304 px. Twitch's part keeps
// Twitch's armed words, its title with the hours warning, and its call.
//
// Focus (the preview's rule, adopted): a key the line keeps stays the same
// element, so focus on it stays; focus in the line when its key goes with
// its service goes to the words that say what happened, or, when the
// service went somewhere else and nothing is being said, to the key that
// takes its place; and when the line turns into Twitch's own key, to that
// key, whatever turned it. Never to the page's body.
export const AccountsDisconnectKey: React.FC<Props> = ({me, where = 'lock'}) => {
    const brand = useName();
    const connections = connectionsOf(me);
    const twice = useTwoClicks();
    const [busy, setBusy] = React.useState<AnyService | ''>('');
    const [said, setSaid] = React.useState<{text: string; error: boolean}>({text: '', error: false});
    const statusRef = React.useRef<HTMLParagraphElement | null>(null);
    const rootRef = React.useRef<HTMLDivElement | null>(null);
    const saying = said.text !== '';

    // Twitch alone, and nothing being said or pressed: Twitch's own key
    // (below). Kept for the timer, which runs after this render.
    const twitchAlone = connections.length === 1 && connections[0].service === 'twitch';
    const becomesTwitchKey = React.useRef(false);
    becomesTwitchKey.current = twitchAlone && busy === '';

    // Where focus is as the line changes, whatever changed it: this line's
    // own press, or a Disconnect somewhere else (another tab, an admin,
    // Matty) taking a key away, or turning the line into Twitch's own key.
    // Read as each render begins, while the page still holds what the last
    // one drew, so it is known even when what has focus is about to go: a
    // key that went takes no blur with it. The place both this line and
    // Twitch's key are drawn in stays, and is remembered for when neither
    // this line nor its root is there any more.
    const placeRef = React.useRef<HTMLElement | null>(null);
    const focusBefore = React.useRef<{inLine: boolean; key: number}>({inLine: false, key: -1});
    const activeNow = document.activeElement;
    const rootNow = rootRef.current;
    focusBefore.current = activeNow && activeNow !== document.body && rootNow?.contains(activeNow) ? {
        inLine: true,
        key: Array.from(rootNow.querySelectorAll('button')).indexOf(activeNow as HTMLButtonElement),
    } : {inLine: false, key: -1};

    // Focus that was in the line and has gone with what had it goes, as
    // the preview's rule says, never to the page's body: while this line's
    // own press is out or its words are showing, to the words, which say
    // what happened; else to the key that now stands where the gone one
    // stood; and when the line has turned into Twitch's own key, to that
    // key, or, with nothing left at all, to the root of the lock or of the
    // panel, both focusable.
    const pressing = busy !== '' || saying;
    React.useLayoutEffect(() => {
        const root = rootRef.current;
        if (root?.parentElement) {
            placeRef.current = root.parentElement;
        }

        const before = focusBefore.current;
        const active = document.activeElement;
        if (!before.inLine || (active && active !== document.body)) {
            return;
        }

        if (root) {
            const keys = Array.from(root.querySelectorAll<HTMLElement>('button'));
            const key = pressing || keys.length === 0 ? null : keys[Math.min(Math.max(before.key, 0), keys.length - 1)];
            (key || statusRef.current)?.focus({preventScroll: true});

            return;
        }

        const place = placeRef.current;
        const twitchKey = place?.querySelector<HTMLElement>(':scope > .vntv-twitch-lock .vntv-twitch-disconnect');
        (twitchKey || place?.closest<HTMLElement>('.vntv-panel'))?.focus({preventScroll: true});
    });

    // The words after a Disconnect go after a while, and focus on them goes
    // first: to a key still there, or else to the root of the lock or of the
    // panel, both focusable. When the line is about to become Twitch's own
    // key, the key here would go with it, so focus is left where it is, and
    // the rule above hands it to Twitch's key as the line turns into it.
    React.useEffect(() => {
        if (said.text === '' || said.error) {
            return undefined;
        }

        const timer = window.setTimeout(() => {
            const note = statusRef.current;
            if (!becomesTwitchKey.current && note && document.activeElement === note) {
                const key = rootRef.current?.querySelector<HTMLElement>('button');
                (key || note.closest<HTMLElement>('.vntv-panel'))?.focus({preventScroll: true});
            }
            setSaid({text: '', error: false});
        }, DISCONNECTED_MS);

        return () => window.clearTimeout(timer);
    }, [said]);

    // Twitch alone: Twitch's own key, unchanged. Not while this line is still
    // saying what its last press did. And kept once Twitch has gone with
    // nothing else connected, since that was the key's own press: its words
    // after it are still in it, with focus on them, and it takes itself out
    // of the page when they have gone, as it always has.
    const twitchKeyStays = React.useRef(false);
    const asTwitchKey = busy === '' && !saying && (twitchAlone || (connections.length === 0 && twitchKeyStays.current));
    twitchKeyStays.current = asTwitchKey;
    if (asTwitchKey) {
        return (
            <TwitchDisconnectKey
                me={me}
                where={where}
            />
        );
    }

    if (!accountsLineShown({connected: connections.length, busy: busy !== '', saying})) {
        return null;
    }

    const press = (service: AnyService) => (event: React.MouseEvent<HTMLButtonElement>) => {
        if (busy !== '' || !twice.press(service, event)) {
            return;
        }

        setBusy(service);
        setSaid({text: '', error: false});
        unlink(service).then(() => {
            setSaid({text: saidAfter(service), error: false});
            void refreshIdentity();
        }).catch(() => {
            setSaid({text: failedWords(service), error: true});
        }).finally(() => setBusy(''));
    };

    const line = disconnectLineOf(connections, twice.armed as AnyService | '', busy, brand, saying);

    // The service whose own words are the whole line: pressed once, or its
    // second press on its way.
    const held: AnyService | '' = line.kind === 'armed' || line.kind === 'busy' ? line.service : '';
    const single = connections.length === 1;
    const titleOf = (connection: Connection): string => {
        if (line.kind === 'armed' && line.service === connection.service) {
            return line.title;
        }

        return line.kind === 'list' ? line.keys.find((key) => key.service === connection.service)?.title || '' : '';
    };
    const wordsOf = (connection: Connection): string => {
        if (line.kind === 'armed' || line.kind === 'busy') {
            return line.words;
        }
        const word = line.kind === 'list' ? line.keys.find((key) => key.service === connection.service)?.word || '' : '';

        return single && line.kind === 'list' ? `${line.lead} ${word}` : word;
    };

    // One line. Each key is the same element whatever the line says, so
    // focus on it stays as its words change.
    const keys = (
        <span
            className='vntv-acctkey-line'
            role={single ? undefined : 'group'}
            aria-label={single ? undefined : 'Disconnect'}
            data-single={single ? 'true' : undefined}
            data-armed={held !== '' ? 'true' : undefined}
        >
            {held === '' && !single ? (
                <span
                    className='vntv-acctkey-label'
                    aria-hidden={true}
                >{line.kind === 'list' ? line.lead : ''}</span>
            ) : null}
            {connections.map((connection, index) => (held !== '' && held !== connection.service ? null : (
                <React.Fragment key={connection.service}>
                    {index > 0 && held === '' ? (
                        <span
                            className='vntv-acctkey-dot'
                            aria-hidden={true}
                        >{'·'}</span>
                    ) : null}
                    <button
                        type='button'
                        className={single ? 'vntv-textkey vntv-acctkey-key' : 'vntv-textkey vntv-acctkey-key vntv-acctkey-word'}
                        data-armed={twice.armed === connection.service ? 'true' : undefined}
                        aria-disabled={busy !== '' ? true : undefined}
                        aria-label={held === '' && !single ? `Disconnect ${wordsOf(connection)}` : undefined}
                        title={titleOf(connection) || undefined}
                        onClick={press(connection.service)}
                    >{wordsOf(connection)}</button>
                </React.Fragment>
            )))}
        </span>
    );

    return (
        <div
            ref={rootRef}
            className={where === 'lock' ? 'vntv-acctkey' : 'vntv-acctkey vntv-acctkey-watchonly'}
        >
            {keys}
            <p
                ref={statusRef}
                className='vntv-twitch-note vntv-acctkey-note'
                data-tone={said.error ? 'error' : undefined}
                role='status'
                tabIndex={-1}

                // A letter typed with focus on the words: on the lock it goes
                // into the bar, as from anywhere on the lock; in the footer it
                // stops here (TwitchDisconnectKey's rule).
                onKeyDown={(event) => {
                    if (where === 'watchonly' && event.target === event.currentTarget && strayKey(event)) {
                        event.stopPropagation();
                    }
                }}
            >{said.text}</p>
        </div>
    );
};
