import React from 'react';

import {noteTokenBarOpen, useLockCause} from '../access';
import {getAway} from '../away';
import {bannerOf} from '../banner';
import {now as clockNow, onClockAdvance} from '../clock';
import {linkStream, RequestError} from '../client';
import {fromAnythingButText, startFocusGuard} from '../focusGuard';
import {keepTokenKeys} from '../focusTrap';
import {composing} from '../keys';
import {TOKEN_NOT_ACCEPTED, TOKEN_START, cleanToken, fresh, isEmptyToken, lockLine, lockNotSetUp, lockStatus, tokenEditable, tokenReducer, tokenStatus} from '../lockLogic';
import type {TokenPhase} from '../lockLogic';
import {usePending} from '../lockPending';
import {OPENED_BY_HAND_MS, PENDING_MS} from '../motion';
import {useArtwork, useName} from '../name';
import {fitPopoutWindow} from '../popout';
import type {Me} from '../types';
import {onOpenedByHand, takeOpenedByHand} from '../watching';

import {EyeIcon, EyeOffIcon, PadlockIcon} from './icons';
import {TryAgain} from './TryAgain';
import {AccountsDisconnectKey} from './AccountsKey';

// The lock screen (design/lock-screen.md section 4): what the panel
// is for everybody, system admins included, until they unlock it with their
// own bearer token — the publish token their streaming software uses, which
// is what linking a stream always took — or a system admin lets them in to
// watch. It REPLACES the panel rather than covering it (PanelGate.tsx), so
// nothing of the panel runs behind it: no streams, no pictures, no clips, no
// polling. It makes one request when it mounts (the shared "me", which
// PanelGate asks for) and one more when somebody presses Unlock.
//
// The same look as the rest of the plugin, and as little of it as will do
// (design/lock-screen.md, the amendment of 2026-10-06): the panel itself looks
// locked. Its emblem, large and dimmed as if behind glass, with a padlock on
// it, and nothing else in the middle; the black bar for the token docked at
// the panel's foot, where the unlocked panel's own bar will be, with a status
// line over it that says something only when there is something to say. A
// line under the emblem only when there is a reason the lock is up.
//
// Nothing else, for anybody. Not that people with no token can be let in, or
// whom to ask (Victor: "Don't advertise that"), and nothing that only a system
// admin reads: no way to let themselves in, no alerts, no set-up words. An
// admin finds all of that in the System Console, and the alerts in full in
// the panel once it is unlocked (AdminAlerts.tsx). A system admin's lock is
// everybody's lock.

// A revealed token hides itself again, so sharing a screen after a peek at it
// does not put a credential on display.
const REVEAL_MS = 15000;

// The first moment of "starting" is plain black, so a fast answer shows no
// emblem at all.
const STARTING_DARK_MS = 250;

// Whether this browser can mask a plain text field (-webkit-text-security).
// Where it can, the bar is not a password field: a browser's own password
// manager ignores autocomplete="off" on a password field, could offer the
// person's saved Mattermost password here — which would then go to Broadcast
// Box as a token — and would likely offer to save the token as the site's
// password when the lock opens. Asked once.
let maskable: boolean | null = null;

function canMask(): boolean {
    if (maskable === null) {
        try {
            maskable = typeof CSS !== 'undefined' && typeof CSS.supports === 'function' && CSS.supports('-webkit-text-security', 'disc');
        } catch {
            maskable = false;
        }
    }

    return maskable;
}

// Ids from a counter (see TitleSearch). Never with vntv- in them, which the
// style check reads as a class.
let nextBar = 0;
let nextLock = 0;

// What a refusal from /me/link was, for the status line's words. No answer at
// all, and the plugin restarting (it answers 404 everywhere), are the same
// thing to the person: the panel is not answering.
function refusalOf(reason: unknown): {code: string; text: string} {
    if (reason instanceof RequestError) {
        if (reason.status === 0 || reason.status === 404) {
            return {code: 'no_answer', text: ''};
        }

        return {code: reason.code || 'unworded', text: reason.serverText};
    }

    return {code: 'no_answer', text: ''};
}

// Whether there is a soft keyboard to think of: Mattermost's phone layout, or
// a coarse pointer. The bar is not put in focus there, or the keyboard would
// cover half the full-screen side panel for everybody who only came to read
// the lock.
function onTouchScreen(): boolean {
    if (getAway().mobile) {
        return true;
    }

    try {
        return window.matchMedia('(pointer: coarse)').matches;
    } catch {
        return false;
    }
}

export type TokenBarVariant = 'unlock' | 'link' | 'relink';

type TokenBarProps = {
    // unlock: the lock screen's, docked at the panel's foot with its padlock,
    // Show and Unlock inside it, no label to see and no hint (lockStatus says
    // where to find a token when it is needed). link: the let-in footer's,
    // with Link and Cancel. relink: a linked person's, with Save.
    variant: TokenBarVariant;

    // The server accepted the token: the stream key it is linked to now.
    onLinked: (streamKey: string) => void;

    // Cancel, and Escape once there is no error to clear: closes the sheet
    // the bar is in. The lock has none.
    onCancel?: () => void;

    // Escape once there is no error to clear, where there is no Cancel pill
    // to go with it: the relink sheet, which closes by its × and by Escape.
    // Without either, as on the lock, Escape only ever clears an error.
    onEscape?: () => void;

    // More for a screen reader to hear on landing in the bar: the lock's
    // line, when it has one.
    describedBy?: string;
    barRef?: React.MutableRefObject<HTMLInputElement | null>;

    // Put in focus as it mounts: a sheet the person has just opened.
    focusOnMount?: boolean;

    // Told whenever the bar's state moves on, for the lock's padlock.
    onPhase?: (phase: TokenPhase) => void;

    // The lock is opening for some other reason — an admin let the person in
    // while they looked at it: the bar is done with, as after its own Unlock.
    opening?: boolean;

    // After the pills: the relink form's Unlink. On the lock, after the bar:
    // Disconnect Twitch, which the stylesheet sets on a line of its own under
    // the bar, so it never takes width from the status line.
    children?: React.ReactNode;
};

// The black bar for a bearer token, with Show and the button that sends it,
// and its status line: the same on the lock screen, in the let-in footer, and
// in a linked person's relink form (design/lock-screen.md 4.4, 4.5), with a
// hint under it in the two sheets. The token lives in this component's state
// and nowhere else — never in a module, in storage, in the identity or the
// console — and goes as soon as it has been accepted, and when the bar goes.
export const TokenBar: React.FC<TokenBarProps> = ({variant, onLinked, onCancel, onEscape, describedBy, barRef, focusOnMount, onPhase, opening, children}) => {
    const [state, dispatch] = React.useReducer(tokenReducer, TOKEN_START);
    const name = useName();
    const stateRef = React.useRef(state);
    stateRef.current = state;
    const ownBar = React.useRef<HTMLInputElement | null>(null);
    const bar = barRef || ownBar;
    const [revealed, setRevealed] = React.useState(false);
    const [refocus, setRefocus] = React.useState(0);
    const [ids] = React.useState(() => {
        nextBar += 1;

        return {input: `villanet-token-${nextBar}`, hint: `villanet-token-hint-${nextBar}`, status: `villanet-token-status-${nextBar}`};
    });

    const alive = React.useRef(true);
    React.useEffect(() => {
        alive.current = true;

        return () => {
            alive.current = false;
        };
    }, []);

    // While a token bar is on screen, a message shaped like a token is not
    // posted (access.ts).
    React.useEffect(noteTokenBarOpen, []);

    React.useEffect(() => {
        onPhase?.(state.phase);
    }, [state.phase, onPhase]);

    React.useEffect(() => {
        if (focusOnMount) {
            bar.current?.focus({preventScroll: true});
        }
    }, [focusOnMount, bar]);

    // Shown, it hides itself after a while, by the lab's clock too.
    React.useEffect(() => {
        if (!revealed) {
            return undefined;
        }

        const shownAt = clockNow();
        const timer = window.setTimeout(() => setRevealed(false), REVEAL_MS);
        const unclock = onClockAdvance(() => {
            if (clockNow() - shownAt >= REVEAL_MS) {
                setRevealed(false);
            }
        });

        return () => {
            window.clearTimeout(timer);
            unclock();
        };
    }, [revealed]);

    // After an empty press or a refusal, focus goes back in the bar — but only
    // from one of the bar's neighbours (Unlock, Show, Try again) or from
    // nowhere. Left on the Unlock that was clicked, the next Ctrl+V would go
    // to Mattermost's message box. Somebody who has clicked into the message
    // box meanwhile is left there.
    React.useEffect(() => {
        if (refocus === 0) {
            return;
        }

        const input = bar.current;
        if (!input) {
            return;
        }

        const active = document.activeElement;
        const scope = input.closest('.vntv-lock, .vntv-sheet');
        if (!active || active === document.body || (scope && scope.contains(active))) {
            input.focus({preventScroll: true});
        }
    }, [refocus, bar]);

    const submit = () => {
        const current = stateRef.current;
        const next = tokenReducer(current, {type: 'submit'});
        if (next === current) {
            return;
        }

        dispatch({type: 'submit'});
        if (next.phase === 'empty') {
            setRefocus((count) => count + 1);

            return;
        }

        // Never "": that unlinks. The empty check above was made on what is
        // sent, with one leading "Bearer " taken off as the server would.
        linkStream(cleanToken(current.token)).then((saved) => {
            if (!alive.current) {
                return;
            }
            setRevealed(false);
            if (!saved || typeof saved.streamKey !== 'string' || saved.streamKey === '') {
                dispatch({type: 'refused', code: 'not_accepted', text: TOKEN_NOT_ACCEPTED});
                setRefocus((count) => count + 1);

                return;
            }

            dispatch({type: 'accepted'});
            onLinked(saved.streamKey);
        }, (reason: unknown) => {
            if (!alive.current) {
                return;
            }

            const {code, text} = refusalOf(reason);
            setRevealed(false);
            dispatch({type: 'refused', code, text});
            setRefocus((count) => count + 1);
        });
    };

    const editable = tokenEditable(state) && !opening;
    const checking = state.phase === 'checking';
    const error = state.phase === 'refused' || state.phase === 'empty';
    const docked = variant === 'unlock';
    const status = docked ? lockStatus(state, name) : tokenStatus(state, name, variant === 'relink');
    const masked = !revealed;
    const plainField = canMask();

    const field = (
        <input
            ref={bar}
            id={ids.input}
            className='vntv-lock-input'
            type={plainField || revealed ? 'text' : 'password'}
            data-masked={plainField && masked ? 'true' : undefined}
            autoComplete={plainField ? 'off' : 'new-password'}
            autoCapitalize='off'
            autoCorrect='off'
            spellCheck={false}
            data-1p-ignore='true'
            data-lpignore='true'
            data-bwignore='true'
            placeholder={docked ? 'Bearer token to unlock' : 'Paste it here'}
            value={state.token}
            readOnly={!editable}
            aria-invalid={error ? true : undefined}
            aria-describedby={[describedBy, docked ? '' : ids.hint, ids.status].filter(Boolean).join(' ')}
            onChange={(event) => dispatch({type: 'type', value: event.target.value})}
            onKeyDown={(event) => {
                if (composing(event)) {
                    return;
                }
                if (event.key === 'Enter') {
                    event.preventDefault();
                    event.stopPropagation();
                    submit();
                } else if (event.key === 'Escape') {
                    // An error first, and only then the sheet: one
                    // Escape clears what went wrong, the next closes.
                    // Never the token itself (section 4.5).
                    event.preventDefault();
                    event.stopPropagation();
                    const close = onEscape || onCancel;
                    if (error || !close) {
                        dispatch({type: 'escape'});
                    } else {
                        close();
                    }
                }
            }}
        />
    );

    const statusLine = (
        <p
            id={ids.status}
            className='vntv-lock-status'
            role='status'
            aria-live='polite'
            data-tone={status.error ? 'error' : undefined}
        >{status.text}</p>
    );

    if (docked) {
        // The lock opening: its own Unlock accepted, or an admin letting the
        // person in while they looked at it.
        const done = state.phase === 'unlocking' || Boolean(opening);
        let unlockWords = checking ? 'Checking…' : 'Unlock';
        if (done) {
            unlockWords = 'Unlocked';
        }

        // Red once there is something to send, and while it is sent.
        const ready = checking || done || !isEmptyToken(state.token);
        const showWords = revealed ? 'Hide token' : 'Show token';

        return (
            <div className='vntv-token vntv-token-dock'>
                {/* The status line has its place over the bar, the room for
                    the lock's longest words kept whether or not it is saying
                    anything: what went wrong shows where the eye already is,
                    just over the bar, and nothing on the lock moves when it
                    does. Empty, it is only black. */}
                <div className='vntv-lock-say'>
                    {statusLine}
                </div>

                <div
                    className='vntv-lock-bar'
                    data-error={error ? 'true' : undefined}
                >
                    {/* The padlock is the bar's label: pressed, it puts the
                        bar in focus, and it holds the words a screen reader
                        names the bar by. Its shackle swings open with the
                        emblem's as the lock opens. */}
                    <label
                        htmlFor={ids.input}
                        className='vntv-lock-pad'
                    >
                        <PadlockIcon size={16}/>
                        <span className='vntv-sr-only'>{'Bearer token'}</span>
                    </label>

                    {field}

                    {/* Its name changes; it is not also marked pressed, which
                        would read as "Hide token, toggle button, pressed". */}
                    <button
                        type='button'
                        className='vntv-lock-tool'
                        aria-controls={ids.input}
                        aria-label={showWords}
                        title={showWords}
                        onClick={() => setRevealed((shown) => !shown)}
                    >{revealed ? <EyeOffIcon size={18}/> : <EyeIcon size={18}/>}</button>

                    {/* Kept while the lock opens, named Unlocked and pressing
                        nothing, rather than taken away: taken away, it took
                        the keyboard focus with it to the page's body, from
                        where a key went to Mattermost's message box. A key
                        pressed on it now is only stopped (the bar is shut). */}
                    <button
                        type='button'
                        className='vntv-lock-tool vntv-lock-go'
                        aria-label={unlockWords}
                        title={unlockWords}
                        aria-disabled={checking || done ? true : undefined}
                        data-ready={ready ? 'true' : undefined}
                        data-done={done ? 'true' : undefined}
                        onClick={() => {
                            if (!checking && !done) {
                                submit();
                            }
                        }}
                    >
                        {/* A stock primary button with its word,
                            where the message box has Send. */}
                        {unlockWords}
                    </button>
                </div>

                {children}
            </div>
        );
    }

    const sendWords = variant === 'link' ? 'Link' : 'Save';

    return (
        <div className='vntv-token'>
            <label
                htmlFor={ids.input}
                className='vntv-lock-label'
            >{'Bearer token'}</label>
            <div
                className='vntv-lock-bar'
                data-error={error ? 'true' : undefined}
            >
                {field}

                {/* Its words change; it is not also marked pressed, which would
                    read as "Hide, toggle button, pressed". */}
                <button
                    type='button'
                    className='vntv-lock-reveal'
                    aria-controls={ids.input}
                    onClick={() => setRevealed((shown) => !shown)}
                >{revealed ? 'Hide' : 'Show'}</button>
            </div>
            <p
                id={ids.hint}
                className='vntv-lock-hint'
            >{'The token your streaming software publishes with. OBS calls it Bearer Token, under Settings → Stream.'}</p>

            <div className='vntv-token-actions'>
                <button
                    type='button'
                    className='vntv-sheet-primary'
                    aria-disabled={checking ? true : undefined}
                    onClick={() => {
                        if (!checking) {
                            submit();
                        }
                    }}
                >{checking ? 'Checking…' : sendWords}</button>
                {onCancel ? (
                    <button
                        type='button'
                        className='vntv-sheet-action'
                        onClick={onCancel}
                    >{'Cancel'}</button>
                ) : null}
                {children}
            </div>

            {statusLine}
        </div>
    );
};

export type LockFace = 'starting' | 'locked' | 'noSignal';

type LockProps = {
    me: Me | null;

    // starting: "me" not read in this window yet. noSignal: it could not be,
    // or the store did not answer, and this page has never had a definite
    // answer — a linked person during a plugin restart is never told they
    // are locked. locked: the lock.
    face: LockFace;

    // The lock opening, for whatever reason: its own Unlock, or an admin
    // letting the person in while they look at it (PanelGate).
    unlocking: boolean;

    // The bar's token was accepted: the stream key it is linked to now.
    onUnlocked: (streamKey: string) => void;

    // The lock's root, for PanelGate's focus rule.
    rootRef: (element: HTMLDivElement | null) => void;
};

export const LockScreen: React.FC<LockProps> = ({me, face, unlocking, onUnlocked, rootRef}) => {
    const root = React.useRef<HTMLDivElement | null>(null);
    const bar = React.useRef<HTMLInputElement | null>(null);
    const pending = usePending();
    const cause = useLockCause();
    const [phase, setPhase] = React.useState<TokenPhase>('idle');
    const [ids] = React.useState(() => {
        nextLock += 1;

        return {line: `villanet-lock-line-${nextLock}`};
    });

    // In a window of its own, as tall as the window, so the lock is centred
    // and the bottom banner sits at the bottom of it (popout.ts).
    React.useEffect(() => fitPopoutWindow(root.current), []);

    const starting = face === 'starting';
    const [dark, setDark] = React.useState(starting);
    React.useEffect(() => {
        if (!dark) {
            return undefined;
        }

        const timer = window.setTimeout(() => setDark(false), STARTING_DARK_MS);

        return () => window.clearTimeout(timer);
    }, [dark]);

    const name = useName();
    const artwork = useArtwork();
    const configured = me ? me.configured : true;
    const formShown = !starting && configured;
    const barReady = formShown && phase !== 'checking' && phase !== 'unlocking' && !unlocking;

    // Opened by the person — the App Bar or header button, a Watch, a clip
    // link — and not on a touch screen: the bar is put in focus once, through
    // the guard that puts right Mattermost's own move into the side panel.
    // Taken at once, and from whatever was pressed to open it: Mattermost
    // often leaves focus on that button (it moves it only when the side panel
    // was closed, not when it showed a thread or a search), and a Ctrl+V there
    // goes to its message box. Never out of a text field, and never when the
    // panel opened by itself: somebody typing a message would type it into the
    // token field. An opener pressed while the lock is already up applies at
    // once.
    React.useEffect(() => {
        if (!barReady) {
            return undefined;
        }

        let stop: (() => void) | null = null;
        const consider = () => {
            const at = takeOpenedByHand();
            if (!fresh(at, clockNow(), OPENED_BY_HAND_MS) || onTouchScreen()) {
                return;
            }

            stop?.();
            stop = startFocusGuard(() => bar.current, {from: fromAnythingButText, atOnce: true});
        };

        consider();
        const unlisten = onOpenedByHand(consider);

        return () => {
            unlisten();
            stop?.();
        };
    }, [barReady]);

    const banner = bannerOf(me);
    const opening = unlocking || phase === 'unlocking';

    // The line as it stood when the lock began to open: what was waiting is
    // taken, and the reason cleared, the moment it opens, and the column must
    // not jump under the fading lock.
    let lineNow = '';
    if (!starting) {
        lineNow = lockLine({pending, cause, noSignal: face === 'noSignal', now: clockNow(), pendingMs: PENDING_MS, name});
    }
    const lineAsOpened = React.useRef(lineNow);
    if (!opening) {
        lineAsOpened.current = lineNow;
    }
    const line = lineAsOpened.current;

    // Nothing on the screen says LOCKED in words, the padlock does; a screen
    // reader lands on a heading saying what the panel is.
    let heading = `${name} is locked`;
    if (opening) {
        heading = `${name} is unlocked`;
    } else if (face === 'noSignal') {
        heading = `${name} is not answering`;
    }

    return (
        <div
            ref={(element) => {
                root.current = element;
                rootRef(element);
            }}
            className='vntv vntv-panel vntv-lock'
            style={banner.style}
            data-state={face}
            data-emblem={artwork.emblem ? 'true' : undefined}
            data-dark={dark ? 'true' : undefined}
            data-unlocking={opening ? 'true' : undefined}
            role='region'
            aria-label={name}
            aria-busy={starting ? true : undefined}

            // The line, when there is one: arriving here after losing
            // access, a screen reader hears why ("You are no longer let in.").
            aria-describedby={line !== '' ? ids.line : undefined}

            // Focusable, not a tab stop: where focus goes when the lock takes
            // the panel's place, and where a click on its empty part puts it.
            tabIndex={-1}

            // Every key that would go to Mattermost's message box goes into
            // the bar instead, from anywhere on the lock (focusTrap.ts).
            onKeyDown={(event) => keepTokenKeys(event, bar.current)}
        >
            {banner.text !== '' && <div className='vntv-banner'>{banner.text}</div>}

            <div className='vntv-lock-stage'>
                <div className='vntv-lock-column'>
                    <div
                        className='vntv-lock-mark'
                        aria-hidden={true}
                    >
                        {/* The server's emblem, when it has one; the
                            padlock alone, in the middle, when not. */}
                        {artwork.emblem ? <div className='vntv-lock-emblem'/> : null}

                        {/* Only on the lock itself. Starting is not known
                            yet, and No signal is not known at all: a linked
                            person during a plugin restart is never shown a
                            padlock, only the line and Try again. */}
                        {face === 'locked' ? (
                            <span className='vntv-lock-badge'>
                                <PadlockIcon size={20}/>
                            </span>
                        ) : null}
                    </div>

                    {starting ? (
                        <p className='vntv-sr-only'>{`Starting ${name}…`}</p>
                    ) : (
                        <>
                            <h2 className='vntv-sr-only'>{heading}</h2>

                            {line !== '' && (
                                <p
                                    id={ids.line}
                                    className='vntv-lock-line'
                                >{line}</p>
                            )}

                            {face === 'noSignal' && !opening ? <TryAgain className='vntv-lock-retry'/> : null}

                            {/* The same sentence for everybody: there is no
                                bar, since a token could only fail. */}
                            {configured ? null : <p className='vntv-lock-line'>{lockNotSetUp(name)}</p>}

                            {/* With no bar to sit beside, Disconnect stays
                                here: somebody locked who has a Twitch, Steam
                                or Hytale account connected can always
                                disconnect it (design/3.4.0.md 5.5), as
                                Twitch's agreement and Steam's terms ask. */}
                            {configured ? null : <AccountsDisconnectKey me={me}/>}
                        </>
                    )}
                </div>
            </div>

            {formShown ? (
                <div
                    className='vntv-lock-dock'

                    // The 24px strip under the bar is the bottom
                    // banner when there is one.
                    data-strip={banner.atBottom ? 'banner' : undefined}
                >
                    <TokenBar
                        variant='unlock'
                        barRef={bar}
                        describedBy={line !== '' ? ids.line : undefined}
                        onPhase={setPhase}
                        opening={unlocking}
                        onLinked={onUnlocked}
                    >
                        {/* Somebody locked who has a Twitch, Steam or
                            Hytale account connected can always disconnect
                            it: one quiet line under the bar, after Unlock in
                            the tab order as on the screen
                            (design/lock-screen.md 9.2, design/3.4.0.md 5.5).
                            With Twitch alone it is Twitch's own key.
                            Connecting is never offered here. */}
                        <AccountsDisconnectKey me={me}/>
                    </TokenBar>
                </div>
            ) : null}

            {banner.atBottom && <div className='vntv-banner'>{banner.text}</div>}
        </div>
    );
};
