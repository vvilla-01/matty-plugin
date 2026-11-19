import React from 'react';

import {hasLinks, renderLinked} from '../linkify';
import type {MentionMirror} from '../linkify';
import {MARQUEE_GAP, MARQUEE_REST, MARQUEE_SPEED, reducedMotion} from '../motion';
import type {NowPlaying} from '../types';

import {Avatar} from './Avatar';
import {PowerIcon, TvIcon} from './icons';
import {OnNow} from './NowPlaying';

// The controls that live ON the picture rather than under it.
//
// Nothing shows until somebody points at the picture: then a power switch
// fades up in the middle and the sound and full-screen controls along the
// bottom, and they go again when the mouse leaves or stops moving. Under the
// picture there is only whose stream it is — their face, their name and what
// they are streaming, on one line.
//
// Awake is decided here, in script, rather than by :hover, because :hover lies
// on a touch screen: a tap leaves it stuck on until the next tap somewhere
// else, and a tap on the middle of the picture would have landed on a switch
// that was not visible when the finger came down. So:
//
//   - A mouse wakes them by moving over the picture, and they sleep again when
//     it leaves or has been still for a moment — but never while it is over a
//     control, or the volume would vanish from under a drag. "Still" is timed
//     from the last move, the last button release and the last time the
//     pointer found itself over something new without moving — a drag let go
//     over the picture, or full screen rearranging everything under a resting
//     cursor — or the controls would stay up for good after either.
//   - A tap wakes them, and a tap on the picture while they are up puts them
//     away. A tap that WOKE them is never also a press on one of them. A finger
//     held on a control keeps them up until it lifts, so a slow volume drag
//     does not lose its slider. A touch the browser takes over for scrolling
//     (pointercancel) was never a tap, and undoes whatever it woke.
//   - The keyboard wakes them by focusing one. That is CSS, :focus-visible, so
//     a mouse click that leaves focus on a button does not pin them up.
//
// Asleep, they take no clicks at all, so nothing can be pressed that cannot be
// seen.
//
// A clip in the Clips dialog plays in the same tile, with these same controls
// on it (ClipTile.tsx): no switch, a line of where it is along the bottom,
// everything held up while it is paused, and a click on the picture to play
// or pause. Those are props a stream's tile never passes; see HudProps.

// How long the controls stay up once the mouse stops moving over the picture.
const IDLE_MS = 2600;

// And after a tap, which has no "still over it" to go by.
const TAP_MS = 4000;

type HudProps = {
    // What the switch does, said out loud: "Switch off Dave". A clip
    // (ClipTile.tsx) has no switch, and passes neither: nothing on a clip
    // ends anything, and the middle of its picture is left to the picture.
    powerLabel?: string;

    onPower?: () => void;

    // Switching off. The controls go at once and stay gone; the picture is
    // collapsing under them.
    off?: boolean;

    // Along the bottom: sound, the scissors, full screen. A tile waiting for
    // its stream to come back has no sound and nothing to make full screen:
    // its row is for the scissors alone, while the end of its stream is still
    // held for clipping (spec 3.4), and it has none otherwise. (The idle No
    // Signal set has no Hud at all.)
    children?: React.ReactNode;

    // The switch after the row in the tab order rather than before it, for a
    // surface where switching off should be the last thing reached. Only the
    // order changes: the switch is absolutely centred on the picture
    // wherever it is in the DOM.
    powerLast?: boolean;

    // The rest is a clip's (ClipTile.tsx), and a stream's tile passes none of
    // it.
    //
    // edge goes under the row, along the picture's bottom edge: a clip's line
    // of where it is, drawn always, though the row above it comes and goes.
    edge?: React.ReactNode;

    // keepUp holds everything up with nobody pointing at the picture: a
    // paused clip.
    keepUp?: boolean;

    // onSurface is a mouse click on the bare picture, not on a control: a
    // clip plays or pauses. A tap there still only wakes the controls or
    // puts them away.
    onSurface?: () => void;
};

export const Hud: React.FC<HudProps> = ({powerLabel, onPower, off, children, powerLast, edge, keepUp, onSurface}) => {
    const [awake, setAwakeState] = React.useState(false);

    // Readable from the pointer handlers in the same event, before React has
    // rendered the change.
    const awakeRef = React.useRef(false);
    const timerRef = React.useRef(0);
    const tapWokeRef = React.useRef(false);
    const keepUpRef = React.useRef(keepUp);
    keepUpRef.current = keepUp;

    // For onSurface: the pointer of a press that began on the bare picture,
    // and '' for one that began on a control. Sliding off a control before
    // letting go is how a press is taken back, and the browser then sends
    // its click to the picture, which is not a click on the picture.
    const surfacePressRef = React.useRef('');

    const setAwake = React.useCallback((next: boolean) => {
        awakeRef.current = next;
        setAwakeState(next);
    }, []);

    const hold = React.useCallback(() => window.clearTimeout(timerRef.current), []);

    const sleepIn = React.useCallback((ms: number) => {
        window.clearTimeout(timerRef.current);
        timerRef.current = window.setTimeout(() => setAwake(false), ms);
    }, [setAwake]);

    React.useEffect(() => () => window.clearTimeout(timerRef.current), []);

    React.useEffect(() => {
        if (off) {
            hold();
            setAwake(false);
        }
    }, [off, hold, setAwake]);

    const overControl = (target: EventTarget | null) => (
        target instanceof Element && target.closest('.vntv-hud-control') !== null
    );

    // Whether the controls can be seen right now: woken by a pointer, or
    // brought up by the keyboard, which is CSS and so never sets awake. The
    // keyboard half mirrors the stylesheet's rule exactly; an engine without
    // :has() throws on it, and has no keyboard reveal to mirror either.
    const shown = (hud: Element) => {
        if (awakeRef.current || keepUpRef.current) {
            return true;
        }

        try {
            return hud.matches(':has(:focus-visible)');
        } catch {
            return false;
        }
    };

    // A mouse that has come to rest somewhere: over a control it holds them
    // up, anywhere else it starts the countdown again. Nothing to count down
    // from while they are asleep.
    const mouseSettled = (target: EventTarget | null) => {
        if (overControl(target)) {
            hold();
        } else if (awakeRef.current) {
            sleepIn(IDLE_MS);
        }
    };

    // A finger lifted, or taken over by the browser: the countdown starts from
    // now rather than from when it went down.
    const touchLifted = () => {
        if (awakeRef.current) {
            sleepIn(TAP_MS);
        }
    };

    const power = onPower ? (
        <button
            type='button'
            className='vntv-hud-power vntv-hud-control'
            aria-label={powerLabel}
            title={powerLabel}
            disabled={off}
            onClick={onPower}
        ><PowerIcon size={24}/></button>
    ) : null;

    const bar = children ? (
        <div className='vntv-hud-bar vntv-hud-control'>{children}</div>
    ) : null;

    return (
        <div
            className='vntv-hud'
            data-awake={awake ? 'true' : 'false'}
            data-keep-up={keepUp ? 'true' : undefined}
            onClick={(event) => {
                if (onSurface && event.target === event.currentTarget && surfacePressRef.current === 'mouse') {
                    onSurface();
                }
            }}
            onPointerMove={(event) => {
                if (off || event.pointerType !== 'mouse') {
                    return;
                }

                if (!awakeRef.current) {
                    setAwake(true);
                }

                mouseSettled(event.target);
            }}
            onPointerOver={(event) => {
                // No move, but something new under the pointer: a drag let go
                // over the picture, full screen changing everything round a
                // resting cursor, a control fading up beneath it.
                if (!off && event.pointerType === 'mouse') {
                    mouseSettled(event.target);
                }
            }}
            onPointerUp={(event) => {
                if (off) {
                    return;
                }

                if (event.pointerType === 'mouse') {
                    mouseSettled(event.target);
                } else {
                    touchLifted();
                }
            }}
            onPointerCancel={(event) => {
                if (off || event.pointerType === 'mouse') {
                    return;
                }

                // A scroll that happened to start on the picture. If it woke
                // the controls it puts them straight back.
                if (tapWokeRef.current) {
                    tapWokeRef.current = false;
                    hold();
                    setAwake(false);
                } else {
                    touchLifted();
                }
            }}
            onPointerLeave={(event) => {
                if (event.pointerType === 'mouse') {
                    hold();
                    setAwake(false);
                }
            }}
            onPointerDownCapture={(event) => {
                if (off) {
                    return;
                }

                // Before anything below changes it, and before the press moves
                // focus: were they visible when this press began? A mouse
                // click on a clip's picture is play or pause, whether or not
                // it woke them.
                const wasShown = shown(event.currentTarget);
                surfacePressRef.current = event.target === event.currentTarget ? event.pointerType : '';
                tapWokeRef.current = !wasShown && !(onSurface && event.pointerType === 'mouse');

                if (event.pointerType === 'mouse') {
                    setAwake(true);
                    if (!overControl(event.target)) {
                        sleepIn(IDLE_MS);
                    }

                    return;
                }

                if (!wasShown) {
                    setAwake(true);
                    sleepIn(TAP_MS);
                } else if (overControl(event.target)) {
                    // Held up for as long as the finger is down; the
                    // countdown starts when it lifts.
                    setAwake(true);
                    hold();
                } else {
                    hold();
                    setAwake(false);
                }
            }}
            onClickCapture={(event) => {
                // The press that brought the controls up is not a press ON one
                // of them. detail is 0 for a click made with the keyboard,
                // which never needs waking.
                const swallow = tapWokeRef.current && event.detail !== 0;
                tapWokeRef.current = false;
                if (swallow) {
                    event.preventDefault();
                    event.stopPropagation();
                }
            }}
        >
            {powerLast ? null : power}
            {bar}
            {edge}
            {powerLast ? power : null}
        </div>
    );
};

// Whose stream this is, under the picture: their face, their name, and what
// they are streaming — one line. A dead set says No Signal, with an empty ring
// where a face goes, so the line is the same shape before and after.
//
// The name and the title sit on one baseline, in a row of their own, and it
// is that row that is centred against the face. Centred one by one, as they
// were, each box is centred on its own font: the name is 14px Oswald and the
// title 12px body text, whose letters sit at different heights in their boxes,
// and the title came out a couple of pixels higher than the name beside it.
// Matching line heights does not help — the difference is in the fonts.
//
// What is on goes between the two, in that same row and on that same
// baseline: a tag with an edge of its own, whose words still sit on the line
// the name and the title share (see .vntv-player-onnow in the stylesheet).
type InfoProps = {
    userId?: string;

    // A 24/7 channel's own picture, in place of a face.
    picture?: string;
    name: string;
    note?: string;
    dead?: boolean;

    // What is on, as the server decided it for the card; see NowPlaying.tsx.
    nowPlaying?: NowPlaying;

    // A Twitch stream's title: no @names in it (design/3.2.0.md 8.2), whose
    // @ is for Twitch handles.
    twitch?: boolean;

    // At the end of the line: a clip's time (ClipTile.tsx).
    end?: React.ReactNode;
};

export const TileInfo: React.FC<InfoProps> = ({userId, picture, name, note, dead, nowPlaying, twitch, end}) => (
    <div className='vntv-player-bar'>
        {dead ? (
            <span
                className='vntv-face vntv-player-face vntv-face-empty'
                aria-hidden={true}
            >
                {/* A quiet set in the empty ring. */}
                <TvIcon size={13}/>
            </span>
        ) : (

            // Decorative here: the name is right beside it.
            <Avatar
                className='vntv-player-face'
                userId={userId}
                src={picture}
                name={name}
                decorative={true}
            />
        )}
        <span className='vntv-player-text'>
            <span
                className='vntv-player-name'
                title={name}
            >{name}</span>
            {nowPlaying && !dead ? (
                <OnNow
                    nowPlaying={nowPlaying}
                    who={name}
                    className='vntv-player-onnow'
                />
            ) : null}
            {note ? (
                <Marquee
                    key={note}
                    className='vntv-player-note'
                    text={note}
                    mentions={!twitch}
                />
            ) : null}
        </span>
        {end}
    </div>
);

// A stream title too long for the line scrolls, the way a ticker does: a pause
// to read the start, then along at an even pace, round and back to the start,
// which is seamless because the words are there twice with a gap between.
//
// Only when it does not fit — a title that fits sits still — and never for
// anybody who asked for less motion, who gets the ellipsis instead; that is
// re-checked when the preference changes, not only when the line is measured.
// Pointing at it holds it still, and a click, a tap, or Enter or Space on it
// stops it until the next one — scrolling text that cannot be stopped is a
// problem for some readers, and a touch screen has no pointing and a keyboard
// no hovering. While it moves it is a toggle button to match, and a tab stop;
// a title that fits is plain text and neither. The whole of it is in the
// tooltip either way.

// The pace, the gap and the rest at the start are in motion.ts, shared with
// the stylesheet's keyframes.
//
// @names in it (design/3.2.0.md 8.3) are Mattermost's own mentions in the
// first copy only. The second, inert copy draws each as plain text saying
// what the first one says once Mattermost has drawn it, so the two copies are
// always the same width: "@dave" can become "@Dave Ryder" when Mattermost
// names people its own way, a moment after the first measure, and two copies
// of different widths would jump at the seam.
const Marquee: React.FC<{text: string; className: string; mentions?: boolean}> = ({text, className, mentions = true}) => {
    const boxRef = React.useRef<HTMLSpanElement | null>(null);
    const wordsRef = React.useRef<HTMLSpanElement | null>(null);
    const [mirror, setMirror] = React.useState<MentionMirror>([]);

    // How far one lap travels; 0 when the words fit and nothing moves.
    const [lap, setLap] = React.useState(0);
    const [paused, setPaused] = React.useState(false);

    // The link or hashtag in it that has focus from the keyboard, if one
    // does; see onFocus.
    const [keyLink, setKeyLink] = React.useState<HTMLElement | null>(null);

    // Brought into view once the ticker has stopped for it, so it is measured
    // against the words at rest rather than wherever they had got to.
    React.useLayoutEffect(() => {
        const box = boxRef.current;
        if (!box || !keyLink) {
            return;
        }

        const end = keyLink.offsetLeft + keyLink.offsetWidth;
        box.scrollLeft = Math.max(0, end - box.clientWidth + 8);
    }, [keyLink]);

    React.useLayoutEffect(() => {
        const box = boxRef.current;
        const words = wordsRef.current;
        if (!box || !words) {
            return undefined;
        }

        const measure = () => {
            // scrollWidth is the words' own width whether or not they are
            // clipped to an ellipsis at that moment.
            const natural = words.scrollWidth;
            const next = natural > box.clientWidth + 1 && !reducedMotion() ? natural + MARQUEE_GAP : 0;
            setLap((current) => (current === next ? current : next));

            // What each @name in the live copy says now, read as text, never
            // as HTML, and whether Mattermost found the person (and so drew
            // its chip, which has padding the inert copy must have too).
            const shown: MentionMirror = Array.from(words.querySelectorAll('.vntv-mention')).map((mention) => ({
                text: mention.textContent || '',
                resolved: mention.querySelector('.mention-link') !== null,
            }));
            setMirror((current) => (current.length === shown.length && current.every((was, index) => was.text === shown[index].text && was.resolved === shown[index].resolved) ? current : shown));
        };

        measure();

        // The words as well as the box: the box is clipped and never
        // resizes, so a mention that Mattermost redraws wider inside it is
        // only seen on the words themselves (design/3.2.0.md V34). And their
        // text, for a redraw that keeps the width.
        const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(measure);
        observer?.observe(box);
        if (mentions) {
            observer?.observe(words);
        }
        const redrawn = mentions && typeof MutationObserver !== 'undefined' ? new MutationObserver(measure) : null;
        redrawn?.observe(words, {childList: true, characterData: true, subtree: true});

        // Asking for less motion while a title is already scrolling stops it,
        // and asking for motion back starts it again.
        let motion: MediaQueryList | null = null;
        try {
            motion = window.matchMedia('(prefers-reduced-motion: reduce)');
        } catch {
            motion = null;
        }
        if (motion?.addEventListener) {
            motion.addEventListener('change', measure);
        } else {
            motion?.addListener?.(measure);
        }

        return () => {
            observer?.disconnect();
            redrawn?.disconnect();
            if (motion?.removeEventListener) {
                motion.removeEventListener('change', measure);
            } else {
                motion?.removeListener?.(measure);
            }
        };
    }, [text, mentions]);

    const moving = lap > 0;

    // A title with a link, a hashtag or an @name in it cannot also BE a
    // button — a control inside a button is two controls in one, and neither
    // works properly. It still stops for a click on the words around them,
    // and it stops while one has keyboard focus, so it can be read and
    // pressed.
    const linked = React.useMemo(() => hasLinks(text, mentions), [text, mentions]);
    const toggle = moving && !linked;

    const style = moving ? {
        '--vntv-marquee-lap': `${lap}px`,
        animationDuration: `${Math.round((lap / MARQUEE_SPEED / (1 - MARQUEE_REST)) * 1000)}ms`,
    } as React.CSSProperties : undefined;

    return (
        <span
            ref={boxRef}
            className={`vntv-marquee ${className}`}
            data-moving={moving ? 'true' : undefined}
            data-paused={moving && paused ? 'true' : undefined}
            data-keyboard={keyLink ? 'true' : undefined}
            title={text}
            role={toggle ? 'button' : undefined}
            tabIndex={toggle ? 0 : undefined}
            aria-pressed={toggle ? paused : undefined}
            onClick={moving ? () => setPaused((current) => !current) : undefined}

            // A link or hashtag focused from the keyboard: the ticker stops
            // back at its start (the stylesheet does that, on data-keyboard)
            // and it is brought into view, instead of being wherever the words
            // had got to. Back to the start again when focus leaves.
            //
            // Only from the keyboard. A mouse press focuses what it lands on
            // too, before the button comes up; stopping and scrolling then
            // moved the words out from under the pointer, so the release
            // landed on something else and the press never became a click on
            // the link — and a hashtag at the end of a long title, only ever
            // seen while it scrolls, could not be clicked at all. Pointing
            // already holds the ticker still for a mouse. An engine that does
            // not know :focus-visible throws, and is treated as a mouse: its
            // keyboard can still press a link wherever the words have got to.
            onFocus={(event) => {
                const link = event.target as HTMLElement;
                if (!link.classList.contains('vntv-link') && link.closest('.vntv-mention') === null) {
                    return;
                }

                let fromKeyboard = false;
                try {
                    fromKeyboard = link.matches(':focus-visible');
                } catch {
                    fromKeyboard = false;
                }

                setKeyLink(fromKeyboard ? link : null);
            }}
            onBlur={(event) => {
                if (boxRef.current && !event.currentTarget.contains(event.relatedTarget as Node | null)) {
                    setKeyLink(null);
                    boxRef.current.scrollLeft = 0;
                }
            }}
            onKeyDown={toggle ? (event) => {
                if (event.key === 'Enter' || event.key === ' ') {
                    event.preventDefault();
                    setPaused((current) => !current);
                }
            } : undefined}
        >
            <span
                className='vntv-marquee-track'
                style={style}
            >
                <span
                    ref={wordsRef}
                    className='vntv-marquee-words'
                >{renderLinked(text, false, mentions)}</span>
                {moving && (
                    <span
                        className='vntv-marquee-words'
                        aria-hidden={true}
                    >{renderLinked(text, true, mentions, mirror)}</span>
                )}
            </span>
        </span>
    );
};

// The buttons along the bottom of the picture. Plain icons on the shade rather
// than pills: over a picture, a row of outlined lozenges is a row of labels.
//
// The scissors use the rest (spec 3.4). ariaDisabled is a button that says
// why it cannot be pressed, in its name and its tooltip, and does nothing when
// it is: aria-disabled rather than disabled, so it can still be reached and
// read, and keeps focus if it has it. title, when the tooltip says more than
// the name. tag is the small corner tag, "0:47" while less than a whole
// length has been recorded; it says nothing to a screen reader, because the
// name already says it in words. busy pulses, while the editor is cutting.
// Both show only while the controls are up, like everything on the picture.
//
// labelKey is for a name that changes by itself, as the scissors' does while
// the recording grows ("Clip the last 12 seconds…", 13, 14…): while the button
// has focus, its name and tooltip stay as they were when it took focus, as
// long as labelKey stays the same, so a screen reader that reads out the
// focused control's name whenever it changes does not read it every second.
// A change that means something — the recording full, or failing — comes
// with a new labelKey and is read at once. The corner tag still counts.
export const HudButton: React.FC<{
    label: string;
    onClick: () => void;
    children: React.ReactNode;
    ariaDisabled?: boolean;
    title?: string;
    keyShortcuts?: string;
    tag?: string;
    busy?: boolean;
    labelKey?: string;
    buttonRef?: React.Ref<HTMLButtonElement>;
}> = ({label, onClick, children, ariaDisabled, title, keyShortcuts, tag, busy, labelKey, buttonRef}) => {
    const [held, setHeld] = React.useState<{key: string; label: string; title: string} | null>(null);
    const steady = held !== null && labelKey !== undefined && held.key === labelKey;

    // A new labelKey while it has focus: the new name is shown, and read,
    // once, and then held in its turn.
    React.useEffect(() => {
        if (held !== null && labelKey !== undefined && held.key !== labelKey) {
            setHeld({key: labelKey, label, title: title ?? label});
        }
    }, [labelKey]);
    const shownLabel = steady && held ? held.label : label;
    const shownTitle = steady && held ? held.title : title ?? label;

    return (
        <button
            ref={buttonRef}
            type='button'
            className={busy ? 'vntv-hud-button vntv-hud-busy' : 'vntv-hud-button'}
            aria-label={shownLabel}
            aria-disabled={ariaDisabled ? true : undefined}
            aria-keyshortcuts={keyShortcuts}
            title={shownTitle}
            onClick={ariaDisabled ? undefined : onClick}
            onFocus={labelKey === undefined ? undefined : () => setHeld({key: labelKey, label, title: title ?? label})}
            onBlur={labelKey === undefined ? undefined : () => setHeld(null)}
        >
            {children}
            {tag ? (
                <span
                    className='vntv-hud-tag'
                    aria-hidden={true}
                >{tag}</span>
            ) : null}
        </button>
    );
};
