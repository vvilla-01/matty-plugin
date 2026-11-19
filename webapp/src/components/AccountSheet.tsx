import React from 'react';

import {loadAccounts, onAccountLink} from '../accounts';
import {accountRowView, armedWords, BACK_TO_LIST, CONSENT_HEAD, consentNote, CONTINUE_ON, DISCONNECTED_MS, disconnectFailed, flowHeading, GETTING_READY, HYTALE_REDIRECT_HINT, justDisconnected, LIST_HEADING, NO_WINDOW_HINT_MS, noWindowWords, ownConnectionOf, PRIVATE_OWNER, readSaidNothing, REDIRECT_HINT_MS, SERVICE_NAME, TICKET_REFRESH_MS, twitchDisconnectTitle, twitchRowView, unavailableWords, WAITING_GIVE_UP_MS, WAITING_POLL_MS, waitingAt, waitingWords} from '../accountsLogic';
import type {AnyService, RowView, Service} from '../accountsLogic';
import {accountConnectURL, fetchAccountTicket, unlinkAccount, unlinkTwitch} from '../client';
import {now as clockNow, onClockAdvance} from '../clock';
import {useFocusHome} from '../focusHome';
import {isPasteKey} from '../focusTrap';
import {getIdentity, refreshIdentity} from '../identity';
import {composing} from '../keys';
import {nameNow, useName} from '../name';
import {openConnectWindow} from '../twitchEmbed';
import {useTwoClicks} from '../twoClicks';
import type {AccountsSteam, Me} from '../types';

import {CubeIcon, GamepadIcon, TwitchIcon} from './icons';
import {SheetHead} from './SheetHead';
import {JUST_DISCONNECTED as TWITCH_JUST_DISCONNECTED, TwitchSheet} from './TwitchConnect';

// Connected accounts (design/3.4.0.md 5.2-5.3, with Victor's P1 pick B, "Your
// stream and accounts"): the list under the token bar and Unlink in the
// relink key's sheet, one row for each of Twitch, Steam and Hytale that the
// reader has, and the flow a row's Connect turns the sheet into, one service
// at a time, with an arrow back to the list. A connection is never "linked":
// the rows say Connect and Connected, never link, so the panel's own word for
// unlocking with a bearer token keeps its one meaning. Only for somebody
// linked: connecting is, and the server says so three times.
//
// A row at rest is one line: the service's mark and word, whose account it
// is and what Steam says about it, and one button. A row whose one line
// would cut its name or its status puts the status on a second, muted line
// under the word, and Disconnect's armed words always take that line, so the
// name keeps its room and the button stays where it is. Below 300 px of
// content the marks give their room to the names (the stylesheet).
//
// The marks: Twitch's Glitch, unaltered (Victor's pick for this row); for
// Steam and Hytale a game controller and a plain cube in the bar's line
// style, never their logos, whose brand rules were not read.

function errorText(reason: unknown): string {
    return reason instanceof Error ? reason.message : String(reason);
}

// A printable key, or a paste, pressed on a row or a flow, which take no
// typing: stopped there, so it reaches neither the token bar above nor
// Mattermost's message box (TwitchSheet's rule).
function strayKey(event: React.KeyboardEvent<HTMLElement>): boolean {
    const command = event.ctrlKey || event.metaKey;

    return isPasteKey(event) || (event.key.length === 1 && !command && !event.altKey);
}

export const ServiceMark: React.FC<{service: AnyService}> = ({service}) => (
    <span
        className='vntv-account-mark'
        aria-hidden={true}
    >
        {service === 'twitch' ? <TwitchIcon size={13}/> : null}
        {service === 'steam' ? <GamepadIcon size={16}/> : null}
        {service === 'hytale' ? <CubeIcon size={15}/> : null}
    </span>
);

// Whether a row's one line cuts its name or its status. Measured on the row
// as it is drawn on one line, so the caller takes the second line away
// first.
function cutOnOneLine(row: HTMLElement): boolean {
    const cut = (element: HTMLElement | null) => Boolean(element && element.scrollWidth > element.clientWidth + 1);

    return cut(row.querySelector<HTMLElement>('.vntv-account-main')) ||
        cut(row.querySelector<HTMLElement>('.vntv-account-name')) ||
        cut(row.querySelector<HTMLElement>('.vntv-account-sub'));
}

type ListProps = {
    me: Me;
    services: AnyService[];

    // The reader's own Steam status, as everybody else sees it (GET
    // /api/v1/accounts).
    steam?: AccountsSteam;

    // Under the token bar: a hairline and the list's heading. Inside a
    // flow's Connected step: the one row, with no heading, and the private
    // profile said in full under it rather than on the row.
    inFlow?: boolean;
    onConnect: (service: AnyService) => void;

    // Disconnect's second press, then '' once its words have had their few
    // seconds, or it failed. The place holds itself open meanwhile, so the
    // words stay even when "me" no longer gives the row anything to show:
    // the hold starts at the press, not the answer, because "me" can be read
    // again on the server's word before the answer comes (TwitchSheet's
    // onDisconnecting).
    onSaying: (service: AnyService | '') => void;
};

type Said = {service: AnyService | ''; text: string; error: boolean};

const NOTHING_SAID: Said = {service: '', text: '', error: false};

const ORDER: AnyService[] = ['twitch', 'steam', 'hytale'];

// The list. Disconnect is two presses, and its words after stay their few
// seconds in the row's place, with focus if the button pressed had it.
export const AccountsList: React.FC<ListProps> = ({me, services, steam, inFlow = false, onConnect, onSaying}) => {
    const brand = useName();
    const twice = useTwoClicks();
    const [busy, setBusy] = React.useState<AnyService | ''>('');
    const [said, setSaid] = React.useState<Said>(NOTHING_SAID);
    const listRef = React.useRef<HTMLDivElement | null>(null);
    const saidRef = React.useRef<HTMLSpanElement | null>(null);

    // The row as it was when Disconnect was pressed: drawn while the answer
    // is on its way, since "me" may already have lost the account.
    const pressedView = React.useRef<RowView | null>(null);

    // The service whose words were last said, for focus once they go.
    const lastSaid = React.useRef<AnyService | ''>('');

    const alive = React.useRef(true);
    React.useEffect(() => () => {
        alive.current = false;
    }, []);

    const sayingRef = React.useRef(onSaying);
    sayingRef.current = onSaying;

    // Taken away while its words show (the sheet closing): the place is let
    // go with it.
    const holding = React.useRef(false);
    React.useEffect(() => () => {
        if (holding.current) {
            sayingRef.current('');
        }
    }, []);

    // Disconnect's button goes as its words come, and the words as the row's
    // Connect comes back: focus on whichever is there now, never on the
    // page's body (focusHome.ts).
    const keepFocus = useFocusHome(() => {
        if (saidRef.current) {
            return saidRef.current;
        }

        const service = lastSaid.current || busy;
        const connect = service ? listRef.current?.querySelector<HTMLElement>(`.vntv-account-act[data-service="${service}"]`) : null;

        return connect || listRef.current;
    });

    // The words after a Disconnect, for their few seconds; the row went
    // first, while focus waited on the list, it goes on to them.
    React.useEffect(() => {
        if (said.service === '' || said.error) {
            return undefined;
        }

        lastSaid.current = said.service;
        if (document.activeElement === listRef.current) {
            saidRef.current?.focus({preventScroll: true});
        }
        const timer = window.setTimeout(() => {
            holding.current = false;
            setSaid(NOTHING_SAID);
            sayingRef.current('');
        }, DISCONNECTED_MS);

        return () => window.clearTimeout(timer);
    }, [said]);

    // One line or two, row by row: each row is drawn on one line, and goes
    // to two only where that line cut its name or its status. Again after
    // every render, whenever the list's width changes (the sheet follows the
    // panel's), and once the fonts have come.
    const fit = React.useCallback(() => {
        const list = listRef.current;
        if (!list) {
            return;
        }

        const rows = Array.from(list.querySelectorAll<HTMLElement>('.vntv-account-row'));
        rows.forEach((row) => row.removeAttribute('data-wrap'));
        const cut = rows.map((row) => !row.dataset.problem && !row.dataset.said && !row.dataset.unavailable && cutOnOneLine(row));
        rows.forEach((row, index) => {
            if (cut[index]) {
                row.setAttribute('data-wrap', 'true');
            }
        });
    }, []);

    React.useLayoutEffect(fit);

    React.useEffect(() => {
        const list = listRef.current;
        if (!list || typeof ResizeObserver === 'undefined') {
            return undefined;
        }

        let width = list.clientWidth;
        const observer = new ResizeObserver(() => {
            if (list.clientWidth !== width) {
                width = list.clientWidth;
                fit();
            }
        });
        observer.observe(list);
        void document.fonts?.ready.then(() => {
            if (alive.current) {
                fit();
            }
        });

        return () => observer.disconnect();
    }, [fit]);

    const viewOf = (service: AnyService): RowView | null => {
        if (service === 'twitch') {
            return me.twitch ? twitchRowView(me.twitch) : null;
        }

        const account = me[service];

        return account ? accountRowView(service, account, service === 'steam' ? steam : undefined) : null;
    };

    const disconnect = (service: AnyService, view: RowView, event: React.MouseEvent<HTMLButtonElement>) => {
        if (busy !== '' || !twice.press(service, event)) {
            return;
        }

        pressedView.current = view;
        lastSaid.current = '';
        holding.current = true;
        sayingRef.current(service);
        setBusy(service);
        setSaid(NOTHING_SAID);
        const call = service === 'twitch' ? unlinkTwitch() : unlinkAccount(service);
        call.then(() => {
            if (alive.current) {
                setSaid({service, text: service === 'twitch' ? TWITCH_JUST_DISCONNECTED : justDisconnected(service, nameNow()), error: false});
            }
            void refreshIdentity();
            if (service !== 'twitch') {
                void loadAccounts();
            }
        }).catch(() => {
            if (alive.current) {
                holding.current = false;
                setSaid({service, text: disconnectFailed(service), error: true});
                sayingRef.current('');
            }
        }).finally(() => {
            if (alive.current) {
                setBusy('');
            }
        });
    };

    const shown = ORDER.filter((service) => services.includes(service) || busy === service || (said.service === service && !said.error));

    return (
        <div
            ref={listRef}
            className={inFlow ? 'vntv-accounts' : 'vntv-accounts vntv-accounts-under'}
            role='group'
            aria-label={LIST_HEADING}
            tabIndex={-1}
            onFocus={keepFocus.onFocus}
            onBlur={keepFocus.onBlur}
            onKeyDown={(event) => {
                if (strayKey(event)) {
                    event.stopPropagation();
                }
            }}
        >
            {inFlow ? null : <p className='vntv-accounts-heading'>{LIST_HEADING}</p>}
            {shown.map((service) => {
                const word = SERVICE_NAME[service];

                // Just disconnected: the words, in the row's place.
                if (said.service === service && !said.error) {
                    return (
                        <div
                            key={service}
                            className='vntv-account-row'
                            data-said='true'
                        >
                            <ServiceMark service={service}/>
                            <span className='vntv-account-word'>{word}</span>
                            <span className='vntv-account-main'>
                                <span
                                    ref={saidRef}
                                    className='vntv-account-sub'
                                    role='status'
                                    tabIndex={-1}
                                >{said.text}</span>
                            </span>
                        </div>
                    );
                }

                const view = busy === service && pressedView.current ? pressedView.current : viewOf(service);
                if (!view) {
                    return null;
                }

                const armed = twice.armed === service && busy !== service;
                const failed = said.service === service && said.error ? said.text : '';
                let sub: React.ReactNode = view.sub;
                let tone: string = view.tone;
                // Armed, the question is the button's own words, as
                // in Twitch's sheet and on the lock; the row keeps its status.
                if (failed) {
                    sub = failed;
                    tone = 'problem';
                } else if (inFlow && view.tone === 'private') {
                    sub = '';
                } else if (view.tone === 'live') {
                    sub = (
                        <>
                            <span
                                className='vntv-twitch-dot'
                                aria-hidden={true}
                            />
                            {view.sub}
                        </>
                    );
                }

                let action: React.ReactNode = null;
                if (view.connected) {
                    action = (
                        <button
                            type='button'
                            className='vntv-sheet-action vntv-account-act vntv-account-disconnect'
                            data-armed={armed ? 'true' : undefined}
                            aria-disabled={busy === service ? true : undefined}
                            aria-label={armed ? armedWords(service) : `Disconnect ${word}, ${view.name}`}
                            title={service === 'twitch' ? twitchDisconnectTitle(view.name, brand) : undefined}
                            onClick={(event) => disconnect(service, view, event)}
                        >{(() => {
                                if (busy === service) {
                                    return 'Disconnecting…';
                                }

                                return armed ? armedWords(service) : 'Disconnect';
                            })()}</button>
                    );
                } else if (!view.unavailable) {
                    action = (
                        <button
                            type='button'
                            className='vntv-sheet-action vntv-account-act'
                            data-service={service}
                            aria-label={`Connect ${word}`}
                            onClick={() => onConnect(service)}
                        >{'Connect'}</button>
                    );
                }

                return (
                    <div
                        key={service}
                        className='vntv-account-row'
                        data-connected={view.connected ? 'true' : undefined}
                        data-problem={failed ? 'true' : undefined}
                        data-unavailable={view.unavailable ? 'true' : undefined}
                        title={view.title || undefined}
                    >
                        <ServiceMark service={service}/>
                        <span className='vntv-account-word'>{word}</span>
                        <span className='vntv-account-main'>
                            {view.name ? <span className='vntv-account-name'>{view.name}</span> : null}
                            {sub ? (
                                <span
                                    className='vntv-account-sub'
                                    data-tone={tone || undefined}
                                    role={failed ? 'alert' : undefined}
                                >{sub}</span>
                            ) : null}
                        </span>
                        {action}
                    </div>
                );
            })}

            {/* What a first press changed the row to, read out. */}
            <p
                className='vntv-sr-only'
                role='status'
            >{twice.armed !== '' && busy === '' ? armedWords(twice.armed as AnyService) : ''}</p>
        </div>
    );
};

type Phase = 'unavailable' | 'confirm' | 'refused' | 'waiting' | 'connected';

type FlowProps = {
    service: Service;
    me: Me;
    steam?: AccountsSteam;

    // The arrow back, Cancel, Close after a refusal and Escape: to the list,
    // as in Twitch's flow, so Escape does one thing in every flow. And the
    // words after a Disconnect running out: the flow has nothing left to
    // show, and the list, where Connect is, takes its place.
    onBack: () => void;

    // The head's ×: the whole sheet.
    onClose: () => void;

    // As the list's: the place held from Disconnect's second press until its
    // words have gone.
    onSaying: (service: AnyService | '') => void;
};

// One service's flow, in the sheet's place: TwitchSheet's phases (Confirm,
// Refused, Waiting, Connected, and Unavailable for admins), with the
// Disconnect of the Connected step the list's own. Mounted afresh each time
// a row's Connect is pressed, so every one starts with a new ticket.
export const AccountFlow: React.FC<FlowProps> = ({service, me, steam, onBack, onClose, onSaying}) => {
    const brand = useName();
    const account = me[service];
    const [phase, setPhase] = React.useState<Phase>(() => {
        if (account?.connected) {
            return 'connected';
        }

        return account?.available ? 'confirm' : 'unavailable';
    });
    const [ticket, setTicket] = React.useState<string | null>(null);
    const [refusal, setRefusal] = React.useState('');
    const continueRef = React.useRef<HTMLButtonElement | null>(null);
    const rootRef = React.useRef<HTMLDivElement | null>(null);
    const wordsRef = React.useRef<HTMLParagraphElement | null>(null);

    // Focus that loses its button to a phase change (Continue, once it has
    // opened the window) goes to the words that say what is happening now,
    // or to the flow, never to the page's body (focusHome.ts).
    const keepFocus = useFocusHome(() => wordsRef.current || rootRef.current);

    const alive = React.useRef(true);
    React.useEffect(() => () => {
        alive.current = false;
    }, []);

    const backRef = React.useRef(onBack);
    backRef.current = onBack;
    const sayingRef = React.useRef(onSaying);
    sayingRef.current = onSaying;

    // The ticket for this opening, asked for as Confirm shows, and again
    // before the server's ten minutes are up. Continue waits for it.
    React.useEffect(() => {
        if (phase !== 'confirm') {
            return undefined;
        }

        let current = true;
        const ask = () => {
            setTicket(null);
            fetchAccountTicket(service).then((answer) => {
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
    }, [phase, service]);

    // Connected while the flow was open, by its window or in another tab:
    // the flow says so, and everybody's view (the row's status) is read.
    // And disconnected elsewhere while it showed the account (another tab,
    // an admin): back to the list, where Connect is. Not during or after its
    // own Disconnect, whose words keep the flow their few seconds.
    const connected = Boolean(account?.connected);
    const saying = React.useRef(false);
    React.useEffect(() => {
        if (connected && phase !== 'connected') {
            setPhase('connected');
            void loadAccounts();
        } else if (!connected && phase === 'connected' && !saying.current) {
            backRef.current();
        }
    }, [connected, phase]);

    const connectedRef = React.useRef(connected);
    connectedRef.current = connected;
    const said = React.useCallback((next: AnyService | '') => {
        saying.current = next !== '';
        sayingRef.current(next);
        if (next === '' && !connectedRef.current) {
            backRef.current();
        }
    }, []);

    // Waiting: "me" read again on the server's word (the "me" event), when
    // this window gets focus back, and every few seconds, for a while. And
    // whether the window ever seemed to open: this window losing focus to
    // it, or the server's word; if neither within a few seconds, pop-ups may
    // be blocked. The "me" event also goes out for the reader's other
    // connections, their access, and to everybody when an admin deletes a
    // service's data, so it is the window's sign only when the "me" read
    // after it shows that their own connection to this service is not what
    // it was when Waiting began; until that read is back, a hint not yet
    // showing waits for it, so that it never shows for an instant before the
    // event turns out to be about this connection. Never window.open's answer, which is null with noopener
    // and in the desktop app whatever happened. Hytale shows its own error
    // page for a wrong Redirect URI or scope and sends nothing back, so
    // after a minute Waiting says what an admin should look at.
    //
    // What Waiting says and whether it still reads "me" are waitingAt's
    // answer (accountsLogic.ts, checked by check-accounts.mjs) for the time
    // since Continue on the panel's clock (clock.ts), and nothing of the
    // flow's own: the flow only looks at the clock again at the moments that
    // answer can change, and when the clock is wound on.
    const [waitFrom, setWaitFrom] = React.useState(0);
    const [lookedAt, setLookedAt] = React.useState(0);
    const [sawWindow, setSawWindow] = React.useState(false);
    const [looking, setLooking] = React.useState(false);
    const waiting = waitingAt(service, lookedAt - waitFrom, sawWindow || looking);
    const noWindowShown = React.useRef(false);
    noWindowShown.current = waiting.noWindow;
    React.useEffect(() => {
        if (phase !== 'waiting') {
            return undefined;
        }

        let live = true;
        let reads = 0;
        const seen = () => setSawWindow(true);
        const read = () => {
            void refreshIdentity();
        };
        const look = () => setLookedAt((was) => Math.max(was, clockNow()));
        const before = ownConnectionOf(getIdentity()?.[service]);
        const heard = (eventRead: Promise<unknown>) => {
            const holds = !noWindowShown.current;
            if (holds) {
                reads++;
                setLooking(true);
            }
            void eventRead.then((fresh) => {
                if (!live) {
                    return;
                }
                if (readSaidNothing(fresh) || ownConnectionOf(getIdentity()?.[service]) !== before) {
                    seen();
                }
                if (holds) {
                    reads--;
                    if (reads === 0) {
                        setLooking(false);
                    }
                }
            });
        };

        window.addEventListener('blur', seen);
        window.addEventListener('focus', read);
        const unlink = onAccountLink(heard);
        const unclock = onClockAdvance(look);
        const poll = window.setInterval(() => {
            if (waitingAt(service, clockNow() - waitFrom, true).polling) {
                read();
            } else {
                window.clearInterval(poll);
            }
        }, WAITING_POLL_MS);

        // Each moment counts as reached when its timer fires, even a
        // millisecond early, or while the lab holds the clock still; and the
        // time looked at never goes back.
        const moments = [NO_WINDOW_HINT_MS, REDIRECT_HINT_MS, WAITING_GIVE_UP_MS].map((after) => {
            const moment = waitFrom + after;

            return window.setTimeout(() => setLookedAt((was) => Math.max(was, clockNow(), moment)), Math.max(0, moment - clockNow()));
        });

        return () => {
            live = false;
            setLooking(false);
            window.removeEventListener('blur', seen);
            window.removeEventListener('focus', read);
            unlink();
            unclock();
            window.clearInterval(poll);
            moments.forEach((moment) => window.clearTimeout(moment));
        };
    }, [phase, service, waitFrom]);

    // Focus into the flow as it opens, and only then: on Continue, the
    // second of Connect's two presses, and otherwise on the flow itself,
    // never on Disconnect, where a stray Enter would arm it.
    const openedAs = React.useRef(phase);
    React.useEffect(() => {
        if (openedAs.current === 'confirm') {
            continueRef.current?.focus({preventScroll: true});
        } else {
            rootRef.current?.focus({preventScroll: true});
        }
    }, []);

    // Continue: inside the click, with the ticket in hand, so the window
    // opens. Always to Waiting, whatever the browser did with it.
    const proceed = () => {
        if (ticket === null) {
            return;
        }

        openConnectWindow(accountConnectURL(service, ticket));
        const at = clockNow();
        setTicket(null);
        setWaitFrom(at);
        setLookedAt(at);
        setSawWindow(false);
        setPhase('waiting');
    };

    let body: React.ReactNode = null;
    switch (phase) {
    case 'unavailable':
        body = (
            <p className='vntv-twitch-note vntv-twitch-muted'>{unavailableWords(service, account?.problem)}</p>
        );
        break;
    case 'confirm':
        body = (
            <div className='vntv-twitch-confirm'>
                <p className='vntv-twitch-note'>
                    <b>{CONSENT_HEAD[service]}</b>{' '}{consentNote(service, brand)}
                </p>
                <div className='vntv-token-actions'>
                    <button
                        ref={continueRef}
                        type='button'
                        className='vntv-sheet-primary'
                        aria-disabled={ticket === null ? true : undefined}
                        onClick={proceed}
                    >{ticket === null ? GETTING_READY : CONTINUE_ON[service]}</button>
                    <button
                        type='button'
                        className='vntv-sheet-action'
                        onClick={onBack}
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
                        onClick={onBack}
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
                >{waitingWords(service)}</p>
                {waiting.noWindow ? <p className='vntv-twitch-note vntv-twitch-muted'>{noWindowWords(service)}</p> : null}
                {waiting.redirect ? <p className='vntv-twitch-note vntv-twitch-muted'>{HYTALE_REDIRECT_HINT}</p> : null}
                <div className='vntv-token-actions'>
                    <button
                        type='button'
                        className='vntv-sheet-action'
                        onClick={onBack}
                    >{'Cancel'}</button>
                </div>
            </div>
        );
        break;
    case 'connected':
        body = (
            <div className='vntv-twitch-confirm'>
                <AccountsList
                    me={me}
                    services={connected ? [service] : []}
                    steam={steam}
                    inFlow={true}
                    onConnect={backRef.current}
                    onSaying={said}
                />
                {service === 'steam' && account?.hidden && account.available ? (
                    <p className='vntv-twitch-note vntv-twitch-muted'>{PRIVATE_OWNER}</p>
                ) : null}
            </div>
        );
        break;
    }

    return (
        <div
            ref={rootRef}
            className='vntv-account-flow'
            data-service={service}
            data-phase={phase}
            role='group'
            aria-label={flowHeading(service, phase === 'connected')}
            tabIndex={-1}
            onFocus={keepFocus.onFocus}
            onBlur={keepFocus.onBlur}
            onKeyDown={(event) => {
                if (strayKey(event)) {
                    event.stopPropagation();

                    return;
                }
                if (event.key !== 'Escape' || composing(event)) {
                    return;
                }

                event.preventDefault();
                event.stopPropagation();
                onBack();
            }}
        >
            <SheetHead
                title={flowHeading(service, phase === 'connected')}
                onBack={onBack}
                backLabel={BACK_TO_LIST}
                onClose={onClose}
            />
            {body}
        </div>
    );
};

type TwitchFlowProps = {
    me: Me;
    sheetId: string;
    onBack: () => void;
    onClose: () => void;

    // Its Disconnect pressed: the place holds itself for the words after,
    // as the bar held the Twitch tool and its sheet before 3.4.0 (V33).
    onDisconnecting: () => void;
};

// Twitch's own flow from its row: TwitchSheet itself in its embedded form,
// under a head with the arrow back. Its Cancel, Close, Escape and the end of
// its words after a Disconnect all go back to the list (its onClose), and
// Escape on the head's arrow or × does too.
export const TwitchFlow: React.FC<TwitchFlowProps> = ({me, sheetId, onBack, onClose, onDisconnecting}) => {
    // Connected, or just disconnected from here, the head is the one word,
    // as TwitchSheet's own head was.
    const [pressed, setPressed] = React.useState(false);
    const heading = flowHeading('twitch', Boolean(me.twitch?.login) || pressed);

    return (
        <div
            className='vntv-account-flow'
            data-service='twitch'
            onKeyDown={(event) => {
                if (event.key !== 'Escape' || composing(event)) {
                    return;
                }

                event.preventDefault();
                event.stopPropagation();
                onBack();
            }}
        >
            <SheetHead
                title={heading}
                onBack={onBack}
                backLabel={BACK_TO_LIST}
                onClose={onClose}
            />
            <TwitchSheet
                me={me}
                sheetId={`${sheetId}-twitch`}
                embedded={true}
                onClose={onBack}
                onDisconnecting={() => {
                    setPressed(true);
                    onDisconnecting();
                }}
            />
        </div>
    );
};
