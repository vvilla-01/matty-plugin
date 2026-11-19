import React from 'react';

import {
    backToThread,
    backToThreadWords,
    canAnnounce,
    canGoBackToThread,
    dismissUndo,
    replyPostedWords,
    useAway,
} from '../away';
import {UNDO_MS} from '../motion';
import {nameNow} from '../name';

// Long enough for the region to be in the page before its words are, as a
// screen reader sees it.
const SAY_AFTER_MS = 250;

// The strip at the top of the panel after the panel came back by itself once a
// reply was posted: what happened, and the way back to the thread it closed.
//
// The keyboard lands on the panel itself after that kind of return (away.ts),
// and this is the first thing in it, so one Tab reaches Back to thread.
//
// It stays until the person has plainly moved on — a click elsewhere in the
// panel, or focus on another of its controls — or for thirty seconds with
// nobody pointing at it or focused on it. Never while somebody is: a button
// that vanishes from under the pointer, or from under the keyboard, is worse
// than none.
export const ReturnStrip: React.FC = () => {
    const away = useAway();
    const undo = away.phase === 'docked' ? away.undo : null;
    const stripRef = React.useRef<HTMLDivElement | null>(null);
    const [hovered, setHovered] = React.useState(false);
    const [focused, setFocused] = React.useState(false);

    // What happened, read out. In the main window the small player's region
    // says it (away.ts). In a channel popped out on its own there is none,
    // so it is said here, a moment after the strip appears, into a region
    // that is always in the panel: one that arrives with its words already
    // in it, as the strip and the whole panel do, is not read out.
    const [said, setSaid] = React.useState('');
    React.useEffect(() => {
        if (!undo || canAnnounce()) {
            setSaid('');

            return undefined;
        }

        const words = `${replyPostedWords(undo.channelName)} ${backToThreadWords(nameNow())}`;
        const timer = window.setTimeout(() => setSaid(words), SAY_AFTER_MS);

        return () => window.clearTimeout(timer);
    }, [undo]);

    React.useEffect(() => {
        if (!undo || hovered || focused) {
            return undefined;
        }

        const timer = window.setTimeout(dismissUndo, UNDO_MS);

        return () => window.clearTimeout(timer);
    }, [undo, hovered, focused]);

    React.useEffect(() => {
        const strip = stripRef.current;
        const panel = strip?.closest('.vntv-panel') as HTMLElement | null;
        if (!undo || !strip || !panel) {
            return undefined;
        }

        // Gone on the click, not on the press: everything under the strip
        // moves up when it goes, and going between the press and the release
        // would put something else under the pointer, which the click would
        // then miss. A click is delivered to where it was aimed even if the
        // strip goes as it starts.
        let pressing = false;
        const pressed = () => {
            pressing = true;
        };
        const released = () => {
            pressing = false;
        };
        const clicked = (event: MouseEvent) => {
            if (event.target instanceof Node && !strip.contains(event.target)) {
                dismissUndo();
            }
        };

        // The panel itself taking focus is where a return puts it, not the
        // person moving on. Nor is a press taking focus to what it is on: the
        // click that follows does it, for the same reason as above.
        const moved = (event: FocusEvent) => {
            const target = event.target;
            if (!pressing && target instanceof Node && target !== panel && !strip.contains(target)) {
                dismissUndo();
            }
        };

        panel.addEventListener('pointerdown', pressed, true);
        window.addEventListener('pointerup', released, true);
        window.addEventListener('pointercancel', released, true);
        panel.addEventListener('click', clicked, true);
        panel.addEventListener('focusin', moved, true);

        return () => {
            panel.removeEventListener('pointerdown', pressed, true);
            window.removeEventListener('pointerup', released, true);
            window.removeEventListener('pointercancel', released, true);
            panel.removeEventListener('click', clicked, true);
            panel.removeEventListener('focusin', moved, true);
        };
    }, [undo]);

    // Gone with the panel: coming back to it later is not coming back from
    // that reply.
    React.useEffect(() => () => dismissUndo(), []);

    const region = (
        <span
            className='vntv-sr-only'
            role='status'
            aria-live='polite'
        >{said}</span>
    );

    if (!undo) {
        return region;
    }

    return (
        <>
            {region}
            <div
                ref={stripRef}
                className='vntv-return'
                onMouseEnter={() => setHovered(true)}
                onMouseLeave={() => setHovered(false)}
                onFocus={() => setFocused(true)}
                onBlur={(event) => {
                    if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
                        setFocused(false);
                    }
                }}
            >
                <span className='vntv-return-text'>{replyPostedWords(undo.channelName)}</span>
                {canGoBackToThread() && (
                    <button
                        type='button'
                        className='vntv-pill'
                        onClick={backToThread}
                    >{'Back to thread'}</button>
                )}
            </div>
        </>
    );
};
