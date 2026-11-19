import React from 'react';

import {PLAYING_EYEBROW} from '../accountsLogic';
import type {PlayingItem} from '../accountsLogic';
import {useIdentity} from '../identity';

import {GamepadIcon} from './icons';

type Props = {
    items: PlayingItem[];
};

// The gap between two people, which is also where a swipe leaves the person
// it settles on (scroll-padding in the stylesheet): the person before them
// ends exactly at the row's edge, so nothing of them shows.
const SNAP_GAP = 16;

// Who is in a Steam game right now, as one row under In voice
// (design/3.4.0.md 5.4, Victor's P2 pick, B): VoiceStrip's twin, and for the
// same reasons. It renders NOTHING unless somebody connected is in a game,
// so on an evening nobody plays it costs nothing, and on one they do it says
// who is in what at the moment friends gather, without anybody opening
// anything.
//
// One line that never wraps and scrolls sideways, as the voice row does: a
// row that grew on a busy night would push the players down the panel. Each
// item is a name and a game, so four people are wider than a 400 px panel;
// a soft fade at whichever end has more beyond it says so, as the Trophies
// card's tabs do, rather than a word cut off at the edge.
//
// A swipe settles on a whole person (scroll snapping, in the stylesheet),
// and so does the end: the list is given just enough room after the last
// person for them to come to the start too. Without it, swiped to the end,
// the row began with the tail of somebody's game half-faded against its
// word, which read like stray letters.
//
// Snapping also carries the row along with the person it rests on when the
// people in it change. At rest at the start, that would hide whoever just
// started playing: Bob sorted in before Dave, and the row moved on to keep
// Dave first, with Bob behind the fade and nobody having scrolled. So a row
// the reader left at its start stays there when who is playing changes; one
// they scrolled along keeps their place, as snapping keeps it.
//
// Only people in a game, each by their Mattermost name, not their Steam one:
// this is a row about the people here. Online is not news, offline is never
// drawn for others in the panel, and Hytale is who you are there, not what
// you are doing. The word Playing shows at every width.
export const PlayingStrip: React.FC<Props> = ({items}) => {
    const me = useIdentity();
    const list = React.useRef<HTMLUListElement>(null);
    const [edges, setEdges] = React.useState({start: false, end: false, tail: 0});

    const measure = React.useCallback(() => {
        const row = list.current;
        if (!row) {
            return;
        }

        const people = row.children;
        const first = people[0] as HTMLElement | undefined;
        const last = people[people.length - 1] as HTMLElement | undefined;
        const wide = first && last ? last.offsetLeft + last.offsetWidth - first.offsetLeft : 0;
        const tail = last && wide > row.clientWidth + 1 ? Math.max(0, row.clientWidth - last.offsetWidth - SNAP_GAP) : 0;
        const start = row.scrollLeft > 1;
        const end = row.scrollLeft + row.clientWidth < row.scrollWidth - 1;
        setEdges((current) => (current.start === start && current.end === end && current.tail === tail ? current : {start, end, tail}));
    }, []);

    // Whether the reader left the row at its start. Nobody has scrolled a new
    // row, so it starts out true.
    const atStart = React.useRef(true);
    const scrolled = React.useCallback(() => {
        const row = list.current;
        if (row) {
            atStart.current = row.scrollLeft <= 1;
        }
        measure();
    }, [measure]);

    React.useLayoutEffect(() => {
        const row = list.current;
        if (!row) {
            atStart.current = true;
            return undefined;
        }
        if (!atStart.current) {
            measure();
            return undefined;
        }

        // Back to the start. Chrome re-snaps to the person the row rested on
        // as soon as anything reads the new layout, which setting scrollLeft
        // does first, so this puts it back before it is ever painted. The
        // next frame does it again for a browser that re-snaps later.
        row.scrollLeft = 0;
        measure();
        let pending = true;
        const frame = requestAnimationFrame(() => {
            pending = false;
            row.scrollLeft = 0;
            measure();
        });

        // Another change before that frame keeps the row at its start too,
        // whatever the re-snap's own scroll said about it meanwhile.
        return () => {
            if (pending) {
                cancelAnimationFrame(frame);
                atStart.current = true;
            }
        };
    }, [items, measure]);

    // Again when the panel's width changes, and once the display font has
    // arrived, which widens every name.
    const shown = items.length > 0;
    React.useEffect(() => {
        const row = list.current;
        let current = true;
        document.fonts?.ready.then(() => {
            if (current) {
                measure();
            }
        });
        if (!row || typeof ResizeObserver === 'undefined') {
            return () => {
                current = false;
            };
        }

        const observer = new ResizeObserver(measure);
        observer.observe(row);

        return () => {
            current = false;
            observer.disconnect();
        };
    }, [shown, measure]);

    if (!shown) {
        return null;
    }

    return (
        <div className='vntv-voice vntv-playing'>
            <span
                className='vntv-voice-icon'
                aria-hidden={true}
            ><GamepadIcon size={16}/></span>
            <span
                className='vntv-playing-eyebrow'
                aria-hidden={true}
            >{PLAYING_EYEBROW}</span>
            <ul
                ref={list}
                className='vntv-voice-list'
                aria-label={PLAYING_EYEBROW}
                data-more-start={edges.start ? 'true' : undefined}
                data-more-end={edges.end ? 'true' : undefined}
                style={edges.tail > 0 ? {paddingInlineEnd: edges.tail} : undefined}
                onScroll={scrolled}
            >
                {items.map((item) => (
                    <li
                        key={item.userId}
                        className='vntv-voice-person'
                        data-me={Boolean(me?.userId) && item.userId === me?.userId}
                        title={item.title}
                    >
                        {item.name}
                        <span className='vntv-playing-game'>{` · ${item.what}`}</span>
                    </li>
                ))}
            </ul>
        </div>
    );
};
