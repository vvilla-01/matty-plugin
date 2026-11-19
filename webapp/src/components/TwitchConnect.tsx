import React from 'react';

import {dismissTwitchNotice, fetchConnectTicket, twitchConnectURL, unlinkTwitch} from '../client';
import {useFocusHome} from '../focusHome';
import {isPasteKey} from '../focusTrap';
import {ownConnectionOf, readSaidNothing} from '../accountsLogic';
import {getIdentity, refreshIdentity} from '../identity';
import {composing} from '../keys';
import {useName} from '../name';
import {onTwitchLink} from '../twitch';
import {openConnectWindow} from '../twitchEmbed';
import {channelURL, disconnectLineShown} from '../twitchLogic';
import {useTwoClicks} from '../twoClicks';
import type {Me, MeTwitch} from '../types';

import {SheetHead} from './SheetHead';

// Connect Twitch (design/twitch-live.md 4.9, amended for the lock screen and
// the footer's one quiet bar, and by 3.4.0): for somebody linked, the Twitch
// row of Connected accounts, in the relink key's sheet (AccountSheet.tsx,
// Victor's P1 pick of 2026-11-12, which took the Twitch tool out of the
// footer's bar). Never in the let-in footer's WATCH ONLY line, and never on
// the lock screen, which have only Disconnect Twitch (below), because
// connecting makes somebody a streamer everybody sees live, which is more
// than watching. A Twitch connection is not "linked": it unlocks nothing.
//
// Two presses connect, as the contract asks: the first, the row's Connect,
// turns the sheet into this one, with what connecting means, and asks the
// server for a single-use ticket; the second, Continue on Twitch, opens
// Twitch in a window of its own with that ticket, inside the click, so no
// pop-up blocker stops it. What that window does is the server's business;
// the sheet waits for "me" to say who is connected.
//
// The row's mark is the one place the panel shows Twitch's logo (icons.tsx
// says how it may be drawn).
// Everywhere else, the cards, tiles and popovers, Twitch stays the plain
// word, which with Watch on Twitch is the way back Twitch's agreement asks
// for.

// A ticket lives ten minutes on the server; one held this long is asked for
// again before Continue can be pressed on a dead one.
const TICKET_REFRESH_MS = 9 * 60 * 1000;

// While waiting: "me" read again this often, and for no longer than this.
const WAITING_POLL_MS = 3000;
const WAITING_GIVE_UP_MS = 5 * 60 * 1000;

// No sign of the window this long after Continue — this window never lost
// focus to it, and no word came from the server — and the sheet says pop-ups
// may be blocked.
const NO_WINDOW_HINT_MS = 4000;

// How long the words after a Disconnect stay.
const DISCONNECTED_MS = 6000;

export const CONSENT_HEAD = 'Connect your Twitch account?';
// With the server's name for the plugin (name.ts).
export const consentNote = (name: string): string => `While you are live on Twitch, everyone here sees your Twitch title, category, viewers and picture, and your time live on Twitch counts on the airtime boards. ${name} keeps your Twitch name and those hours, never a password or token. Disconnect removes them. Continuing opens Twitch, where you sign in and approve.`;

// Twitch's Settings > Connections lists this server's own Twitch application,
// under whatever name the admin gave it at Twitch, so the words name no name.
export const JUST_DISCONNECTED = 'Disconnected. Your Twitch hours have been removed from the boards (minutes you were also live on Broadcast Box stay). Removing this server’s Twitch application in Twitch’s Settings > Connections does not disconnect you here; this button does. To remove the application from your Twitch account too, use those Connections as well.';
export const DISCONNECT_FAILED = 'Could not disconnect Twitch. Try again in a moment.';

function errorText(reason: unknown): string {
    return reason instanceof Error ? reason.message : String(reason);
}

function connectedOf(twitch: MeTwitch | undefined): boolean {
    return Boolean(twitch?.login);
}

type Phase = 'unavailable' | 'confirm' | 'refused' | 'waiting' | 'connected' | 'disconnected';

function phaseFor(twitch: MeTwitch | undefined): Phase {
    if (connectedOf(twitch)) {
        return 'connected';
    }

    return twitch?.available ? 'confirm' : 'unavailable';
}

type SheetProps = {
    me: Me;
    sheetId: string;

    // Closes the sheet: its ×, Cancel, Escape, and the words after a
    // Disconnect when their time is up. Embedded, all of these go back to
    // the list instead.
    onClose: () => void;

    // Its own Disconnect has been pressed. From then on its place keeps the
    // sheet until the sheet closes, so the words after it have their few
    // seconds even when "me" no longer gives the Twitch row anything to show
    // (Twitch switched off here, with nothing connected now). From the press,
    // not the answer: "me" can be read again on the server's word before the
    // answer comes.
    onDisconnecting?: () => void;

    // Drawn inside Connected accounts (design/3.4.0.md 5.2, "What A or B
    // would change in Twitch", item 1): under that place's head, which has
    // the arrow back to the list, so without a head or a box of its own.
    embedded?: boolean;
};

// A printable key, or a paste, pressed on the sheet itself or on its words,
// which take no typing: stopped there, as on a tile (TwitchPlayer.tsx), or
// Mattermost would send it to its message box. A paste by Mattermost's own
// rule (focusTrap.ts), which knows a Ctrl+V from a keyboard that types
// another letter on that key.
function strayKey(event: React.KeyboardEvent<HTMLElement>): boolean {
    const command = event.ctrlKey || event.metaKey;
    const paste = isPasteKey(event);

    return paste || (event.key.length === 1 && !command && !event.altKey);
}

// What the Twitch row's Connect opens. Mounted each time it opens, so every
// opening starts afresh: a new ticket, nothing left over from the last.
export const TwitchSheet: React.FC<SheetProps> = ({me, sheetId, onClose, onDisconnecting, embedded = false}) => {
    const brand = useName();
    const twitch = me.twitch;
    const [phase, setPhase] = React.useState<Phase>(() => phaseFor(twitch));
    const [ticket, setTicket] = React.useState<string | null>(null);
    const [refusal, setRefusal] = React.useState('');
    const [hint, setHint] = React.useState(false);
    const [disconnecting, setDisconnecting] = React.useState(false);
    const [disconnectError, setDisconnectError] = React.useState('');
    const twice = useTwoClicks();
    const continueRef = React.useRef<HTMLButtonElement | null>(null);
    const rootRef = React.useRef<HTMLDivElement | null>(null);
    const wordsRef = React.useRef<HTMLParagraphElement | null>(null);

    // The button with focus going with its phase — Continue on Twitch once
    // it has opened the window, Disconnect once it has worked — leaves focus
    // on the words that say what is happening now, or on the sheet, never on
    // the page's body, from where the next letter would go to Mattermost's
    // message box (focusHome.ts). From Waiting's words, Tab reaches Cancel.
    const keepFocus = useFocusHome(() => wordsRef.current || rootRef.current);

    const alive = React.useRef(true);
    React.useEffect(() => () => {
        alive.current = false;
    }, []);

    const closeRef = React.useRef(onClose);
    closeRef.current = onClose;
    const disconnectingRef = React.useRef(onDisconnecting);
    disconnectingRef.current = onDisconnecting;

    // The ticket for this opening, asked for as the sheet opens, and again
    // before the server's ten minutes are up. Continue waits for it.
    React.useEffect(() => {
        if (phase !== 'confirm') {
            return undefined;
        }

        let current = true;
        const ask = () => {
            setTicket(null);
            fetchConnectTicket().then((answer) => {
                if (current && alive.current) {
                    setTicket(answer.ticket);
                }
            }).catch((reason: unknown) => {
                if (current && alive.current) {
                    setRefusal(errorText(reason));
                    setPhase('refused');
                }
            });
        };
        ask();
        const timer = window.setInterval(ask, TICKET_REFRESH_MS);

        return () => {
            current = false;
            window.clearInterval(timer);
        };
    }, [phase]);

    // Connected while the sheet was open — the window did its work, or it
    // was done in another tab — and the sheet says so.
    //
    // And the other way: disconnected elsewhere — another tab, an admin —
    // while it showed the account, it shows what there is to do now. Not
    // during or after its own Disconnect, whose words stay their few seconds.
    const login = twitch?.login || '';
    const available = Boolean(twitch?.available);
    React.useEffect(() => {
        if (login !== '' && (phase === 'confirm' || phase === 'waiting' || phase === 'refused' || phase === 'unavailable')) {
            setPhase('connected');
        } else if (login === '' && phase === 'connected' && !disconnecting) {
            setPhase(available ? 'confirm' : 'unavailable');
        }
    }, [login, phase, available, disconnecting]);

    // Waiting: "me" read again on the server's word, when this window gets
    // focus back, and every few seconds, for a while. And whether the window
    // ever seemed to open: this window losing focus to it, or the server's
    // word, within a few seconds; if neither, pop-ups may be blocked. Neither
    // window.open's answer, which is null with noopener and in the desktop
    // app whatever happened.
    //
    // The server's word is the "me" event, which also goes out for the
    // reader's other connections, their access, and to everybody when an
    // admin deletes a service's data: it is the window's sign only when the
    // "me" read after it shows that their Twitch connection is not what it
    // was when Waiting began (or that read says nothing at all: it failed,
    // or the store did not answer). Until that read is back, a hint whose moment
    // has come waits for it, so that it never shows for an instant before
    // the event turns out to be about this connection.
    React.useEffect(() => {
        if (phase !== 'waiting') {
            return undefined;
        }

        let live = true;
        let sign = false;
        let looking = 0;
        let hintDue = false;
        const seen = () => {
            sign = true;
            setHint(false);
        };
        const read = () => {
            void refreshIdentity();
        };
        const focused = () => read();
        const before = ownConnectionOf(getIdentity()?.twitch);
        const heard = (eventRead: Promise<unknown>) => {
            looking++;
            void eventRead.then((fresh) => {
                looking--;
                if (!live) {
                    return;
                }
                if (readSaidNothing(fresh) || ownConnectionOf(getIdentity()?.twitch) !== before) {
                    seen();
                } else if (looking === 0 && hintDue && !sign) {
                    hintDue = false;
                    setHint(true);
                }
            });
        };

        window.addEventListener('blur', seen);
        window.addEventListener('focus', focused);
        const unlink = onTwitchLink(heard);
        const poll = window.setInterval(read, WAITING_POLL_MS);
        const giveUp = window.setTimeout(() => window.clearInterval(poll), WAITING_GIVE_UP_MS);
        const hintTimer = window.setTimeout(() => {
            if (sign) {
                return;
            }
            if (looking > 0) {
                hintDue = true;
            } else {
                setHint(true);
            }
        }, NO_WINDOW_HINT_MS);

        return () => {
            live = false;
            window.removeEventListener('blur', seen);
            window.removeEventListener('focus', focused);
            unlink();
            window.clearInterval(poll);
            window.clearTimeout(giveUp);
            window.clearTimeout(hintTimer);
        };
    }, [phase]);

    // The words after a Disconnect, for a few seconds, and then the sheet
    // goes.
    React.useEffect(() => {
        if (phase !== 'disconnected') {
            return undefined;
        }

        const timer = window.setTimeout(() => closeRef.current(), DISCONNECTED_MS);

        return () => window.clearTimeout(timer);
    }, [phase]);

    // Focus into the sheet as it opens, and only then: on Continue, which is
    // the second of Connect's two presses, and otherwise on the sheet itself,
    // never on Disconnect, where a stray Enter would arm it.
    const openedAs = React.useRef(phase);
    React.useEffect(() => {
        if (openedAs.current === 'confirm') {
            continueRef.current?.focus({preventScroll: true});
        } else {
            rootRef.current?.focus({preventScroll: true});
        }
    }, []);

    // Continue on Twitch: inside the click, with the ticket in hand, so the
    // window opens. Always to Waiting, whatever the browser did with it.
    const proceed = () => {
        if (ticket === null) {
            return;
        }

        openConnectWindow(twitchConnectURL(ticket));
        setTicket(null);
        setHint(false);
        setPhase('waiting');
    };

    const disconnect = (event: React.MouseEvent<HTMLButtonElement>) => {
        if (disconnecting || !twice.press('disconnect', event)) {
            return;
        }

        setDisconnecting(true);
        setDisconnectError('');
        disconnectingRef.current?.();
        unlinkTwitch().then(() => {
            if (alive.current) {
                setPhase('disconnected');
            }
            void refreshIdentity();
        }).catch(() => {
            if (alive.current) {
                setDisconnectError(DISCONNECT_FAILED);
            }
        }).finally(() => {
            if (alive.current) {
                setDisconnecting(false);
            }
        });
    };

    // Connected, the line under the head says whose account it is, so the
    // head is the one word.
    const name = twitch?.displayName || login;
    const heading = phase === 'connected' || phase === 'disconnected' ? 'Twitch' : 'Connect Twitch';

    let body: React.ReactNode = null;
    switch (phase) {
    case 'unavailable':
        body = (
            <p className='vntv-twitch-note vntv-twitch-muted'>
                {`Connect Twitch is not available: ${twitch?.problem || 'Twitch is not set up on this server yet.'}`}
            </p>
        );
        break;
    case 'confirm':
        body = (
            <div className='vntv-twitch-confirm'>
                <p className='vntv-twitch-note'>
                    <b>{CONSENT_HEAD}</b>{' '}{consentNote(brand)}
                </p>
                <div className='vntv-token-actions'>
                    <button
                        ref={continueRef}
                        type='button'
                        className='vntv-sheet-primary'
                        aria-disabled={ticket === null ? true : undefined}
                        onClick={proceed}
                    >{ticket === null ? 'Getting ready…' : 'Continue on Twitch'}</button>
                    <button
                        type='button'
                        className='vntv-sheet-action'
                        onClick={onClose}
                    >{'Cancel'}</button>
                </div>
            </div>
        );
        break;
    case 'refused':
        body = (
            <div className='vntv-twitch-confirm'>
                <p
                    className='vntv-twitch-note vntv-twitch-error'
                    role='alert'
                >{refusal}</p>
                <div className='vntv-token-actions'>
                    <button
                        type='button'
                        className='vntv-sheet-action'
                        onClick={onClose}
                    >{'Close'}</button>
                </div>
            </div>
        );
        break;
    case 'waiting':
        body = (
            <div className='vntv-twitch-confirm'>
                <p
                    ref={wordsRef}
                    className='vntv-twitch-note'
                    role='status'
                    tabIndex={-1}
                >{'Finish on Twitch in the window that opened.'}</p>
                {hint ? (
                    <p className='vntv-twitch-note vntv-twitch-muted'>
                        {'No window? Allow pop-ups for this site, then press Connect Twitch again.'}
                    </p>
                ) : null}
                <div className='vntv-token-actions'>
                    <button
                        type='button'
                        className='vntv-sheet-action'
                        onClick={onClose}
                    >{'Cancel'}</button>
                </div>
            </div>
        );
        break;
    case 'connected':
        body = (
            <div className='vntv-twitch-confirm'>
                <p className='vntv-twitch-who'>
                    <span className='vntv-twitch-who-name'>{`Twitch: ${name}`}</span>
                    {twitch?.live ? (
                        <span
                            className='vntv-twitch-live'
                            title='Live on Twitch now'
                        >
                            <span
                                className='vntv-twitch-dot'
                                aria-hidden={true}
                            />
                            {'live'}
                        </span>
                    ) : null}
                    <a
                        className='vntv-twitch-out'
                        href={channelURL(login) || undefined}
                        target='_blank'
                        rel='noopener noreferrer'
                    >{`twitch.tv/${login} ↗`}</a>
                    {twitch?.available ? null : <span className='vntv-twitch-muted'>{'(Twitch is switched off here)'}</span>}
                </p>
                <div className='vntv-token-actions'>
                    <button
                        type='button'
                        className='vntv-sheet-action vntv-twitch-disconnect'
                        data-armed={twice.armed === 'disconnect' ? 'true' : undefined}
                        aria-disabled={disconnecting ? true : undefined}
                        onClick={disconnect}
                    >{disconnectWords(disconnecting, twice.armed === 'disconnect')}</button>
                </div>
                <p
                    className='vntv-sr-only'
                    role='status'
                >{twice.armed === 'disconnect' && !disconnecting ? 'Disconnect Twitch? Press again' : ''}</p>
                {disconnectError !== '' ? (
                    <p
                        className='vntv-twitch-note vntv-twitch-error'
                        role='alert'
                    >{disconnectError}</p>
                ) : null}
            </div>
        );
        break;
    case 'disconnected':
        body = (
            <p
                ref={wordsRef}
                className='vntv-twitch-note'
                role='status'
                tabIndex={-1}
            >{JUST_DISCONNECTED}</p>
        );
        break;
    }

    return (
        <div
            ref={rootRef}
            id={sheetId}
            className={embedded ? 'vntv-twitch-sheet vntv-twitch-embedded' : 'vntv-sheet vntv-twitch-sheet'}
            role='group'
            aria-label={heading}
            tabIndex={-1}
            onFocus={keepFocus.onFocus}
            onBlur={keepFocus.onBlur}
            onKeyDown={(event) => {
                if (event.target === event.currentTarget || event.target === wordsRef.current) {
                    if (strayKey(event)) {
                        event.stopPropagation();

                        return;
                    }
                }
                if (event.key !== 'Escape' || composing(event)) {
                    return;
                }

                event.preventDefault();
                event.stopPropagation();
                onClose();
            }}
        >
            {embedded ? null : (
                <SheetHead
                    title={heading}
                    onClose={onClose}
                />
            )}
            {body}
        </div>
    );
};

function disconnectWords(disconnecting: boolean, armed: boolean): string {
    if (disconnecting) {
        return 'Disconnecting…';
    }

    return armed ? 'Disconnect Twitch? Press again' : 'Disconnect';
}

function when(at: number): string {
    try {
        return new Date(at).toLocaleDateString(undefined, {day: 'numeric', month: 'short', year: 'numeric'});
    } catch {
        return '';
    }
}

// Their Twitch account was proved by somebody else and moved to them: said
// over the bar, on the same glass as the bar's other warning, until they say
// they have read it, so a hijack cannot go unnoticed.
export const TwitchLostNotice: React.FC<{lost: {login: string; at: number}}> = ({lost}) => {
    const [sending, setSending] = React.useState(false);
    const [error, setError] = React.useState('');

    const dismiss = () => {
        if (sending) {
            return;
        }

        setSending(true);
        setError('');
        dismissTwitchNotice().then(() => {
            void refreshIdentity();
        }).catch((reason: unknown) => {
            setError(errorText(reason));
            setSending(false);
        });
    };

    return (
        <div
            className='vntv-bar-alert vntv-twitch-lost'
            role='status'
        >
            <span className='vntv-bar-alert-text'>
                {`Your Twitch account ${lost.login} was connected to another Mattermost account on ${when(lost.at)}. If that was not you, tell an admin.`}
                {error !== '' ? <span className='vntv-twitch-error'>{` ${error}`}</span> : null}
            </span>
            <button
                type='button'
                className='vntv-sheet-action'
                aria-disabled={sending ? true : undefined}
                onClick={dismiss}
            >{'OK'}</button>
        </div>
    );
};

// The one Twitch control for somebody who is not linked (lock-screen.md
// 9.2): whoever has a Twitch account connected can always disconnect it,
// because Twitch's agreement says their data goes when they ask. On the lock
// screen, a small text key on a line of its own under the bar (under the
// not-set-up words where there is no bar), after Unlock in the tab order. In
// the footer of somebody let in to watch, the same key on a line of its own
// over the WATCH ONLY bar: they connected while they were linked, and being
// unlinked since must not leave them without a way to withdraw. Never
// Connect, in either place. Two presses, as in the panel; and the same words
// after.
export const TwitchDisconnectKey: React.FC<{me: Me | null; where?: 'lock' | 'watchonly'}> = ({me, where = 'lock'}) => {
    const name = useName();
    const login = me?.twitch?.login || '';
    const twice = useTwoClicks();
    const [busy, setBusy] = React.useState(false);
    const [said, setSaid] = React.useState<{text: string; error: boolean}>({text: '', error: false});
    const statusRef = React.useRef<HTMLParagraphElement | null>(null);
    const keyRef = React.useRef<HTMLButtonElement | null>(null);

    // The words after a Disconnect go after a while, and focus on them goes
    // first: to the key, if the account is still there, or else to the root
    // of the lock or of the panel, both focusable, and both keep a typed key
    // from Mattermost's message box. Never to the page's body, where the
    // words being taken out of the page would leave it.
    React.useEffect(() => {
        if (said.text === '' || said.error) {
            return undefined;
        }

        const timer = window.setTimeout(() => {
            const note = statusRef.current;
            if (note && document.activeElement === note) {
                (keyRef.current || note.closest<HTMLElement>('.vntv-panel'))?.focus({preventScroll: true});
            }
            setSaid({text: '', error: false});
        }, DISCONNECTED_MS);

        return () => window.clearTimeout(timer);
    }, [said]);

    // The key goes once "me" no longer has the account, and focus that was on
    // it goes to the words that say what happened, never to the page's body,
    // from where a letter would go to Mattermost's message box. "Me" can say
    // so before the answer to the press comes: the server tells this page
    // over its socket as it disconnects, and the lab's server changes "me"
    // at once. So the words stay in the page from the press until their time
    // is up, empty until the answer fills them, and are there to take focus
    // whichever comes first. They once went with the key in that order, and
    // focus fell to the body.
    const hadFocus = React.useRef(false);
    React.useLayoutEffect(() => {
        if (login === '' && hadFocus.current) {
            hadFocus.current = false;
            const active = document.activeElement;
            if (!active || active === document.body) {
                statusRef.current?.focus({preventScroll: true});
            }
        }
    }, [login]);

    if (!disconnectLineShown({connected: login !== '', busy, saying: said.text !== ''})) {
        return null;
    }

    const press = (event: React.MouseEvent<HTMLButtonElement>) => {
        if (busy || !twice.press('disconnect', event)) {
            return;
        }

        setBusy(true);
        setSaid({text: '', error: false});
        hadFocus.current = document.activeElement === keyRef.current;
        unlinkTwitch().then(() => {
            setSaid({text: JUST_DISCONNECTED, error: false});
            void refreshIdentity();
        }).catch(() => {
            hadFocus.current = false;
            setSaid({text: DISCONNECT_FAILED, error: true});
        }).finally(() => setBusy(false));
    };

    let words = 'Disconnect Twitch';
    if (busy) {
        words = 'Disconnecting…';
    } else if (twice.armed === 'disconnect') {
        words = 'Disconnect Twitch? Press again';
    }

    return (
        <div className={where === 'lock' ? 'vntv-twitch-lock' : 'vntv-twitch-lock vntv-twitch-watchonly'}>
            {login !== '' ? (
                <button
                    ref={keyRef}
                    type='button'
                    className='vntv-textkey vntv-twitch-disconnect'
                    data-armed={twice.armed === 'disconnect' ? 'true' : undefined}
                    aria-disabled={busy ? true : undefined}
                    title={`Your Twitch account, ${me?.twitch?.displayName || login}, is connected to ${name}. Disconnecting removes it and your Twitch hours.`}
                    onClick={press}
                >{words}</button>
            ) : null}
            <p
                ref={statusRef}
                className='vntv-twitch-note'
                data-tone={said.error ? 'error' : undefined}
                role='status'
                tabIndex={-1}

                // A letter typed with focus on the words: on the lock it goes
                // into the bar, as from anywhere on the lock (keepTokenKeys);
                // in the footer it stops here, as on the panel itself, or
                // Mattermost would send it to its message box.
                onKeyDown={(event) => {
                    if (where === 'watchonly' && event.target === event.currentTarget && strayKey(event)) {
                        event.stopPropagation();
                    }
                }}
            >{said.text}</p>
        </div>
    );
};
