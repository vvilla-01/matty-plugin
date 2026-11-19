import React from 'react';

import {streamPictureURL} from '../client';
import {nameNow, useName} from '../name';
import {useAccess} from '../identity';
import {mayWatch} from '../lockLogic';
import {
    announce,
    backToPanel,
    getWaitingShown,
    hideMini,
    markWaitingShown,
    reportMiniVisible,
    switchOffAll,
    useAnnouncement,
    useAway,
    windowKind,
} from '../away';
import {
    boxOf,
    clampPosition,
    CORNER_NAMES,
    cornerToward,
    DEFAULT_SCROLLBAR,
    layoutMini,
    nearestCorner,
    nextCorner,
    parseCorner,
} from '../miniLayout';
import type {Corner, MiniBox, MiniLayout, Rect} from '../miniLayout';
import {reducedMotion, RETURN_DELAY_MS, MINI_FADE_MS, SNAP_MS} from '../motion';
import {mountVideo, retrySession, setSessionVolume, takeSound, UNMUTE_VOLUME, useSessionInfo} from '../sessions';
import {captureStillsWhileOpen} from '../stills';
import {pollWhileShown, useSnapshot} from '../store';
import type {Stream} from '../types';
import {miniSetting} from '../userSettings';
import {isTwitchHandle} from '../twitch';
import {setAudio, useAudio, useWatching, waitingFor} from '../watching';

import {BrandIcon} from './BrandIcon';
import {Hud, HudButton, TileInfo} from './Hud';
import {CornerIcon, HideIcon, RetryIcon, SpeakerIcon, SpeakerMutedIcon} from './icons';
import {Plate, Screen, Tuning} from './NoSignal';

// The small player: the stream that was in front, in a corner of the channel,
// while a thread, a search or another plugin has the panel's place in the side
// panel (away.ts). It is the same <video> element the panel's tile was
// showing, moved here by the session pool, so nothing reconnects on the way
// out or on the way back.
//
// Mounted once for the whole app, beside the side panel, with Mattermost's
// registerGlobalComponent (or registerRootComponent where that is missing),
// and only in the main window. It never takes focus when it appears, it never
// covers the channel header, the message box or the side panel, and it is
// gone on a phone-sized screen.

// Streams as last seen, for the name on a stream that has dropped off the
// air while the small player waits for it.
const seenStreams = new Map<string, Stream>();

let nextId = 0;

const CORNER_KEY = 'villanet.miniCorner';

// The corner is remembered per browser. Storage can be refused (private
// windows, strict settings): then it is simply the top right each time.
function readCorner(): Corner {
    try {
        return parseCorner(window.localStorage.getItem(CORNER_KEY));
    } catch {
        return 'tr';
    }
}

function saveCorner(corner: Corner): void {
    try {
        window.localStorage.setItem(CORNER_KEY, corner);
    } catch {
        // Not remembered; it still moves.
    }
}

const HIDDEN: MiniLayout = {mode: 'hidden', x: 0, y: 0, w: 0, h: 0};

const sameLayout = (a: MiniLayout, b: MiniLayout): boolean =>
    a.mode === b.mode && a.x === b.x && a.y === b.y && a.w === b.w && a.h === b.h;

function rectOf(element: Element): Rect {
    const r = element.getBoundingClientRect();

    return {left: r.left, top: r.top, right: r.right, bottom: r.bottom};
}

// The message list's scrollbar: the first element in it that scrolls, found
// once and kept while it is still there. Overlay scrollbars measure 0, which
// is right; a list with nothing scrollable in it yet gets the usual 8px.
let scroller: HTMLElement | null = null;

function scrollbarOf(list: HTMLElement): number {
    if (!scroller || !scroller.isConnected || !list.contains(scroller)) {
        scroller = null;
        const queue: Element[] = [list];
        for (let looked = 0; queue.length > 0 && looked < 60; looked++) {
            const element = queue.shift() as Element;
            if (element instanceof HTMLElement) {
                const overflow = window.getComputedStyle(element).overflowY;
                if (overflow === 'auto' || overflow === 'scroll') {
                    scroller = element;
                    break;
                }
            }
            queue.push(...Array.from(element.children));
        }
    }

    return scroller ? Math.max(0, scroller.offsetWidth - scroller.clientWidth) : DEFAULT_SCROLLBAR;
}

// Mattermost wraps a Global component in its own error boundary, which prints
// an error over the whole app. A small player that fails renders nothing
// instead; the panel is unaffected and the stream is let go by the pool.
class Quiet extends React.Component<{children: React.ReactNode}, {failed: boolean}> {
    state = {failed: false};

    static getDerivedStateFromError() {
        return {failed: true};
    }

    render() {
        return this.state.failed ? null : this.props.children;
    }
}

// What the small player says, read out and never seen. Always mounted, so a
// region that already exists is updated, which is what screen readers
// announce. A new key per announcement, so the same words twice are said
// twice.
const Announcer: React.FC = () => {
    const said = useAnnouncement();

    return (
        <span
            className='vntv-sr-only'
            role='status'
            aria-live='polite'
        >
            <span key={said.n}>{said.text}</span>
        </span>
    );
};

// For registerGlobalComponent.
export const MiniPlayerHost: React.FC = () => (
    <>
        <Quiet><Announcer/></Quiet>
        <Quiet><MiniPlayerGate/></Quiet>
    </>
);

// For registerRootComponent, which also renders in popped-out windows: only
// in the main window.
export const MiniPlayerRoot: React.FC = () => (windowKind() === 'main' ? <MiniPlayerHost/> : null);

// Whether there is a small player at all: away from the panel, something else
// in the side panel, the setting on, not phone-sized, and a stream to show
// that is connected, waiting to come back, or coming back after this player
// showed it waiting.
const MiniPlayerGate: React.FC = () => {
    // Never for somebody who may not watch: no small player for a locked
    // reader, whatever is left in the watch list (design/lock-screen.md 7).
    const watch = mayWatch(useAccess());
    const away = useAway();
    const watching = useWatching();
    const snapshot = useSnapshot();
    const latched = away.latched;
    const info = useSessionInfo(latched || '');

    React.useEffect(() => {
        snapshot.streams.forEach((stream) => seenStreams.set(stream.handle, stream));
    }, [snapshot]);

    // Never a Twitch stream (design/twitch-live.md 6.7): the latch already
    // leaves them out (awayLogic.ts latchCandidate), and this is the second
    // guard. By handle, not by the snapshot's entry, which a stream already
    // gone from the snapshot no longer has.
    const covered = away.panel === 'thread' || away.panel === 'own' || away.panel === 'plugin';
    const shown = watch && away.phase === 'away' && !away.hidden && covered && !away.suppressed && !away.mobile &&
        latched !== null && !isTwitchHandle(latched) && watching.includes(latched) && miniSetting() === 'on' &&
        (info !== null || waitingFor(latched) || !snapshot.streams.some((s) => s.handle === latched) || getWaitingShown() === latched);

    // Going because the panel has come back, it fades out where it is while
    // the panel takes the picture back. Going for any other reason, it is
    // simply gone. Closing the thread empties the side panel a moment before
    // the panel is put back in it, so an emptied panel counts as coming back.
    const lastHandle = React.useRef<string | null>(null);
    if (shown) {
        lastHandle.current = latched;
    }
    const docking = away.phase === 'docked' ||
        (away.phase === 'away' && !away.hidden && latched !== null && (away.panel === 'closed' || away.panel === 'empty'));
    const [wasShown, setWasShown] = React.useState(shown);
    const [leaving, setLeaving] = React.useState(false);
    if (wasShown !== shown) {
        setWasShown(shown);
        setLeaving(!shown && docking && !reducedMotion());
    }

    React.useEffect(() => {
        if (!leaving) {
            return undefined;
        }

        const timer = window.setTimeout(() => setLeaving(false), MINI_FADE_MS);

        return () => window.clearTimeout(timer);
    }, [leaving]);

    const handle = shown ? latched : lastHandle.current;
    if (!handle || !(shown || leaving) || isTwitchHandle(handle)) {
        return null;
    }

    return (
        <MiniPlayer
            handle={handle}
            leaving={!shown}
        />
    );
};

type MiniProps = {
    handle: string;
    leaving: boolean;
};

const MiniPlayer: React.FC<MiniProps> = ({handle, leaving}) => {
    // What this server calls the plugin, for the player's own labels.
    const brand = useName();
    const away = useAway();
    const snapshot = useSnapshot();
    const watching = useWatching();
    const audio = useAudio();
    const info = useSessionInfo(handle);
    const [id] = React.useState(() => {
        nextId += 1;

        return `villanet-mini-${nextId}`;
    });

    const sectionRef = React.useRef<HTMLElement | null>(null);
    const slotRef = React.useRef<HTMLDivElement | null>(null);

    // ---- Where it goes ----

    const [corner, setCorner] = React.useState<Corner>(readCorner);
    const cornerRef = React.useRef(corner);
    cornerRef.current = corner;

    const [layout, setLayout] = React.useState<MiniLayout>(HIDDEN);
    const layoutRef = React.useRef(layout);
    const boxRef = React.useRef<MiniBox | null>(null);

    const measure = React.useCallback(() => {
        const list = document.getElementById('post-list');
        let next = HIDDEN;
        if (list) {
            const rhsElement = document.getElementById('sidebar-right');
            const anchor = rectOf(list);
            const rhs = rhsElement ? rectOf(rhsElement) : null;
            const scrollbar = scrollbarOf(list);
            next = layoutMini(anchor, rhs, cornerRef.current, scrollbar);
            boxRef.current = boxOf(anchor, rhs, scrollbar);
        }

        if (!sameLayout(layoutRef.current, next)) {
            layoutRef.current = next;
            setLayout(next);
        }
    }, []);

    // On every render, which covers every change of the side panel and of
    // the channel; layoutRef keeps it from rendering again unless the answer
    // changed.
    React.useLayoutEffect(() => {
        measure();
    });

    // And whenever the page moves under it. Mattermost replaces #post-list
    // with a new node as a channel renders (channel_view.tsx, post_view.tsx),
    // so what is observed is checked again on every change in the page,
    // at most once a frame. A timer stands in for the frame where frames do
    // not come, in a tab that cannot be seen.
    React.useEffect(() => {
        let frame = 0;
        let timer = 0;
        let list: Element | null = null;
        let rhs: Element | null = null;
        const resizes = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(() => schedule());

        const follow = () => {
            const nextList = document.getElementById('post-list');
            if (nextList !== list) {
                if (list) {
                    resizes?.unobserve(list);
                }
                if (nextList) {
                    resizes?.observe(nextList);
                }
                list = nextList;
            }

            const nextRhs = document.getElementById('sidebar-right');
            if (nextRhs !== rhs) {
                if (rhs) {
                    resizes?.unobserve(rhs);
                }
                if (nextRhs) {
                    resizes?.observe(nextRhs);
                }
                rhs = nextRhs;
            }
        };

        const run = () => {
            window.cancelAnimationFrame(frame);
            window.clearTimeout(timer);
            frame = 0;
            timer = 0;
            follow();
            measure();
        };

        function schedule() {
            if (frame || timer) {
                return;
            }
            frame = window.requestAnimationFrame(run);
            timer = window.setTimeout(run, 100);
        }

        follow();
        const content = document.getElementById('app-content');
        const mutations = typeof MutationObserver === 'undefined' ? null : new MutationObserver(schedule);
        mutations?.observe(content || document.body, content ? {childList: true, subtree: true} : {childList: true});
        window.addEventListener('resize', schedule);

        return () => {
            resizes?.disconnect();
            mutations?.disconnect();
            window.removeEventListener('resize', schedule);
            window.cancelAnimationFrame(frame);
            window.clearTimeout(timer);
        };
    }, [measure]);

    // A file dragged over the window goes through the player to the channel
    // under it, which is where Mattermost takes uploads.
    const [fileDrag, setFileDrag] = React.useState(false);
    React.useEffect(() => {
        let timer = 0;
        const over = (event: DragEvent) => {
            const types = event.dataTransfer?.types;
            if (!types || !Array.from(types).includes('Files')) {
                return;
            }
            setFileDrag(true);
            window.clearTimeout(timer);
            timer = window.setTimeout(() => setFileDrag(false), 300);
        };
        const done = () => {
            window.clearTimeout(timer);
            setFileDrag(false);
        };
        window.addEventListener('dragenter', over, true);
        window.addEventListener('dragover', over, true);
        window.addEventListener('drop', done, true);
        window.addEventListener('dragend', done, true);

        return () => {
            window.clearTimeout(timer);
            window.removeEventListener('dragenter', over, true);
            window.removeEventListener('dragover', over, true);
            window.removeEventListener('drop', done, true);
            window.removeEventListener('dragend', done, true);
        };
    }, []);

    // A change of size crossfades. The structure stays exactly the same, so
    // the picture's slot is never remounted; two names for one animation,
    // swapped, so it plays again each time.
    const [shownMode, setShownMode] = React.useState(layout.mode);
    const [flip, setFlip] = React.useState<'a' | 'b' | undefined>(undefined);
    if (shownMode !== layout.mode) {
        setShownMode(layout.mode);
        if (shownMode !== 'hidden' && layout.mode !== 'hidden') {
            setFlip((current) => (current === 'a' ? 'b' : 'a'));
        }
    }

    // Gliding to a new corner, after a drag or a Move.
    const [snapping, setSnapping] = React.useState(false);
    React.useEffect(() => {
        if (!snapping) {
            return undefined;
        }

        const timer = window.setTimeout(() => setSnapping(false), SNAP_MS + 40);

        return () => window.clearTimeout(timer);
    }, [snapping]);

    const moveTo = (next: Corner, say: boolean) => {
        if (next === cornerRef.current) {
            return;
        }

        cornerRef.current = next;
        setCorner(next);
        saveCorner(next);
        setSnapping(!reducedMotion());
        if (say) {
            announce(`Moved to the ${CORNER_NAMES[next]}.`);
        }
    };

    // ---- Dragging ----

    const [dragAt, setDragAt] = React.useState<{x: number; y: number} | null>(null);
    const swallowClick = React.useRef(false);

    // Lets go of the press being followed, if there is one.
    const endDrag = React.useRef<(() => void) | null>(null);
    React.useEffect(() => () => endDrag.current?.(), []);

    // A press on the picture or the name line, followed on the window rather
    // than on the player: a quick pull can be outside the player before the
    // first move arrives, and the release can land anywhere. Either way the
    // press ends with that release, so nothing is left following the
    // pointer afterwards with no button held.
    const startDrag = (event: React.PointerEvent<HTMLElement>) => {
        endDrag.current?.();

        const section = event.currentTarget;
        const id = event.pointerId;
        const x0 = event.clientX;
        const y0 = event.clientY;
        const {x: left, y: top, w, h} = layout;
        let moved = false;
        let at = {x: left, y: top};

        const stop = () => {
            window.removeEventListener('pointermove', move, true);
            window.removeEventListener('pointerup', release, true);
            window.removeEventListener('pointercancel', cancel, true);
            endDrag.current = null;
        };

        function release(up: PointerEvent) {
            const box = boxRef.current;
            if (up.pointerId !== id) {
                return;
            }

            stop();
            setDragAt(null);
            if (!moved || !box) {
                return;
            }

            // The release at the end of a drag is not a click on whatever it
            // ended over. A mouse's click comes in the same task as its
            // release; a touch moved this far brings none, and the next tap
            // must not be the one taken instead.
            swallowClick.current = true;
            window.setTimeout(() => {
                swallowClick.current = false;
            }, 0);

            const next = nearestCorner(box, w, h, at.x, at.y);
            setSnapping(!reducedMotion());
            if (next !== cornerRef.current) {
                moveTo(next, false);
            }
        }

        function cancel(gone: PointerEvent) {
            if (gone.pointerId === id) {
                stop();
                setDragAt(null);
            }
        }

        function move(over: PointerEvent) {
            const box = boxRef.current;
            if (over.pointerId !== id || !box) {
                return;
            }

            // A release that never reached the window, outside it.
            if ((over.buttons & 1) === 0) {
                release(over);

                return;
            }

            const dx = over.clientX - x0;
            const dy = over.clientY - y0;
            if (!moved) {
                if (Math.hypot(dx, dy) <= 6) {
                    return;
                }
                moved = true;
                try {
                    section.setPointerCapture(id);
                } catch {
                    // Followed without capture, while the pointer is in the window.
                }
            }

            at = clampPosition(box, w, h, left + dx, top + dy);
            setDragAt(at);
        }

        window.addEventListener('pointermove', move, true);
        window.addEventListener('pointerup', release, true);
        window.addEventListener('pointercancel', cancel, true);
        endDrag.current = stop;
    };

    // ---- What is on it ----

    const stream = snapshot.streams.find((candidate) => candidate.handle === handle) || null;
    const seen = stream || seenStreams.get(handle) || null;
    const name = seen?.title || 'Stream';
    const waiting = !stream;
    const shown = layout.mode !== 'hidden';
    const visible = shown && !leaving;

    // The picture into its slot, a layout effect so it is there before the
    // browser paints. A session is started here only for a stream this
    // player was showing as waiting and that has come back, as the panel does
    // when a stream returns; otherwise it adopts the session there is, or
    // shows none, and never reconnects a stream nobody is looking at.
    React.useLayoutEffect(() => {
        const slot = slotRef.current;
        if (!slot || !shown) {
            return undefined;
        }

        return mountVideo(handle, slot, {create: getWaitingShown() === handle});
    }, [handle, shown, waiting]);

    React.useEffect(() => {
        if (waiting && visible) {
            markWaitingShown(handle);
        } else if (info && getWaitingShown() === handle) {
            markWaitingShown(null);
        }
    }, [waiting, visible, info, handle]);

    const others = watching.filter((candidate) => candidate !== handle).length;

    // While it is on screen: the preview stills keep coming from its picture
    // (from linked people only, as in the panel), and the list of streams
    // keeps being fetched, as the panel does.
    const linked = useAccess() === 'linked';
    React.useEffect(() => {
        if (!visible) {
            return undefined;
        }

        if (reportMiniVisible(true)) {
            announce(`${nameNow()} is still playing ${name}${others > 0 ? ` and ${others} more` : ''} in the corner of the channel.`);
        }
        const stopStills = linked ? captureStillsWhileOpen() : () => undefined;
        const stopPolling = pollWhileShown();

        return () => {
            stopStills();
            stopPolling();
            reportMiniVisible(false);
        };

        // Said once, as it appears, with the name it appeared with: visible
        // alone decides when this runs.
    }, [visible]);

    // Where focus goes when Hide or the power switch takes the player away
    // from under it: back where it was before it came into the player, or
    // into the reply box beside it. Never the channel's message box, where a
    // stray keystroke would be posted.
    const focusBefore = React.useRef<HTMLElement | null>(null);
    const focusAfterLeaving = (): HTMLElement | null => {
        const section = sectionRef.current;
        const active = document.activeElement;
        if (!section || !active || !section.contains(active)) {
            return null;
        }

        const before = focusBefore.current;
        if (before && before.isConnected && !section.contains(before) && before.id !== 'post_textbox') {
            return before;
        }

        return document.querySelector('#sidebar-right #reply_textbox') as HTMLElement | null;
    };

    const hide = () => {
        const target = focusAfterLeaving();
        hideMini();
        announce(`Player hidden. Your streams are kept in ${nameNow()}.`);
        target?.focus({preventScroll: true});
    };

    const switchOff = () => {
        const target = focusAfterLeaving();
        switchOffAll();
        announce('Switched off.');
        target?.focus({preventScroll: true});
    };

    const audible = audio === handle && !(info?.blocked);
    const blocked = audio === handle && Boolean(info?.blocked);
    const volume = info ? info.volume : UNMUTE_VOLUME;
    const sliderValue = audible ? Math.round(volume * 100) : 0;
    const failed = info?.status === 'failed';
    const tuning = !waiting && (info === null || info.status === 'connecting');
    const powerLabel = watching.length > 1 ? `Switch off all ${watching.length} streams` : `Switch off ${name}`;
    const description = others === 1 ? `1 more stream waiting in ${brand}` : `${others} more streams waiting in ${brand}`;

    const speaker = (
        <HudButton
            label={audible ? 'Mute' : 'Listen to this stream'}
            onClick={() => (audible ? setAudio(null) : takeSound(handle))}
        >{audible ? <SpeakerIcon size={16}/> : <SpeakerMutedIcon size={16}/>}</HudButton>
    );

    const move = (
        <button
            type='button'
            className='vntv-hud-button vntv-mini-move'
            aria-label='Move to another corner'
            title='Move to another corner'
            onClick={() => moveTo(nextCorner(cornerRef.current), true)}
            onKeyDown={(event) => {
                if (event.key.startsWith('Arrow')) {
                    event.preventDefault();
                    moveTo(cornerToward(cornerRef.current, event.key), true);
                }
            }}
        ><CornerIcon size={16}/></button>
    );

    const hideButton = (
        <button
            type='button'
            className='vntv-hud-button vntv-mini-hide'
            aria-label='Hide player'
            title={`Hide the player. Your streams stay in ${brand}.`}
            onClick={hide}
        ><HideIcon size={16}/></button>
    );

    // Drained right to left while the panel is about to come back after a
    // reply. Started part-way when the player appears part-way through.
    const now = Date.now();
    const draining = away.returningUntil > now && !reducedMotion();

    if (!shown) {
        return null;
    }

    const at = dragAt || {x: layout.x, y: layout.y};
    const style = {
        '--vntv-mini-x': `${at.x}px`,
        '--vntv-mini-y': `${at.y}px`,
        '--vntv-mini-w': `${layout.w}px`,
    } as React.CSSProperties;

    return (
        <section
            ref={sectionRef}
            role='region'
            className='vntv vntv-mini a11y__region'
            data-a11y-sort-order='4'
            aria-label={`${brand} player`}
            aria-describedby={others > 0 ? `${id}-desc` : undefined}
            data-mode={layout.mode}
            data-corner={corner}
            data-flip={flip}
            data-dragging={dragAt ? 'true' : undefined}
            data-snapping={snapping ? 'true' : undefined}
            data-file-drag={fileDrag ? 'true' : undefined}
            data-leaving={leaving ? 'true' : undefined}
            style={style}
            onFocus={(event) => {
                const from = event.relatedTarget as HTMLElement | null;
                if (from && !event.currentTarget.contains(from)) {
                    focusBefore.current = from;
                }
            }}
            onPointerDown={(event) => {
                const target = event.target as Element;
                if (event.button !== 0 || !event.isPrimary || leaving ||
                    target.closest('button, input, a, select, textarea, [role="button"]') ||
                    !target.closest('.vntv-mini-screen, .vntv-mini-bar')) {
                    return;
                }

                startDrag(event);
            }}
            onClickCapture={(event) => {
                // See release in startDrag.
                if (swallowClick.current) {
                    swallowClick.current = false;
                    event.preventDefault();
                    event.stopPropagation();
                }
            }}
        >
            {/* First in the DOM, so Back to the panel is the first stop; under
                the picture on screen in picture mode, by CSS order. */}
            <div
                className='vntv-mini-bar'
                data-state={waiting || failed ? 'true' : undefined}
            >
                <button
                    type='button'
                    className='vntv-mini-back'
                    aria-label={`Back to ${brand}`}
                    title={`Back to ${brand}`}
                    onClick={backToPanel}
                >
                    <BrandIcon
                        size={20}
                        alt=''
                    />
                </button>
                <TileInfo
                    userId={seen?.userId}
                    picture={seen ? streamPictureURL(seen) : undefined}
                    name={name}
                    note={seen?.motd}
                    nowPlaying={seen && !seen.alwaysOn ? seen.nowPlaying : undefined}
                />

                {/* Bar mode's word for the No Signal plate, which has no room
                    on its thumbnail: under the name, the whole of it in the
                    tooltip. */}
                {(waiting || failed) && (
                    <span
                        className='vntv-mini-state'
                        title={waiting ? `Waiting for ${name}` : info?.error}
                    >{waiting ? 'Waiting' : 'No signal'}</span>
                )}
            </div>

            <div className='vntv-mini-screen'>
                <Screen noise={waiting || failed}>
                    {/* The slot the pool puts the picture in. Always first and
                        always there, so React never replaces it. */}
                    <div
                        className='vntv-video-slot'
                        ref={slotRef}
                    />

                    {waiting && (
                        <Plate
                            title='No Signal'
                            note={`Waiting for ${name}`}
                        />
                    )}
                    {failed && (
                        <Plate
                            title='No Signal'
                            note={info?.error}
                        />
                    )}
                    {tuning && <Tuning title={name}/>}

                    {others > 0 && (
                        <span
                            className='vntv-mini-more'
                            aria-hidden={true}
                        >{`+${others}`}</span>
                    )}

                    {/* The browser refused the sound: one click on this is
                        what it was waiting for. */}
                    {blocked && (
                        <button
                            type='button'
                            className='vntv-pill vntv-mini-sound'
                            onClick={() => takeSound(handle)}
                        >{'Turn sound on'}</button>
                    )}
                    {failed && (
                        <button
                            type='button'
                            className='vntv-pill vntv-mini-retry'
                            onClick={() => retrySession(handle)}
                        >{'Retry'}</button>
                    )}

                    {/* The tile's own controls, woken the same way. The switch
                        comes last in the tab order here: it is the one thing on
                        the player that ends something. */}
                    <Hud
                        powerLabel={powerLabel}
                        onPower={switchOff}
                        powerLast={true}
                    >
                        {speaker}
                        {layout.w >= 300 && (
                            <input
                                type='range'
                                className='vntv-volume'
                                min={0}
                                max={100}
                                step={1}
                                value={sliderValue}
                                aria-label='Volume'
                                title={audible ? `Volume ${sliderValue}%` : 'Muted — drag to listen to this stream'}
                                onChange={(event) => setSessionVolume(handle, Number(event.target.value) / 100)}
                            />
                        )}
                        <span className='vntv-hud-spacer'/>
                        {move}
                        {hideButton}
                    </Hud>
                </Screen>
            </div>

            {/* The same three in bar mode, where the picture is a thumbnail
                with no room for controls on it, or for the Retry over it:
                that takes Move's place while the stream has failed. */}
            <div className='vntv-mini-controls'>
                {speaker}
                {failed ? (
                    <HudButton
                        label='Retry'
                        onClick={() => retrySession(handle)}
                    ><RetryIcon size={16}/></HudButton>
                ) : move}
                {hideButton}
            </div>

            {draining && (
                <div
                    key={away.returningUntil}
                    className='vntv-mini-drain'
                    aria-hidden={true}
                    style={{animationDelay: `${-(RETURN_DELAY_MS - (away.returningUntil - now))}ms`}}
                />
            )}

            <span
                className='vntv-sr-only'
                id={`${id}-desc`}
            >{others > 0 ? description : ''}</span>
        </section>
    );
};
