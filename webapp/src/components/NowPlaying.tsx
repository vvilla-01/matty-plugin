import React from 'react';

import type {NowPlaying} from '../types';

import {categoryOf, CategoryIcon} from './categories';
import {MediaProfile} from './MediaProfile';

// What is on a stream, wherever a viewer sees it: a small tag with the kind's
// icon and the title, on the Live now card and on the line under the picture.
//
// Which title that is was decided by the server (nowPlayingFor, in
// nowplaying.go): what the streamer said with What's on?, or else the title
// of their plan that is on now. The panel never works it out again, so the
// card, the line under the picture and the profile popover cannot disagree.
//
// A title linked to the real movie, show or game is a button, and pressing it
// opens the same little profile an Up next card has: the cover, a few lines
// about it, and its IMDb, Steam or IGDB page. Pressing, not pointing: the tag
// sits on things that already do something when pressed — the card watches
// the stream — and a profile that opened as the pointer crossed it on its way
// there would be in the way. Words with no title behind them are just words.

type Props = {
    nowPlaying: NowPlaying;

    // Whose stream it is, for the profile's context line.
    who: string;

    // Where it sits, which sets its size: the card or the line under the
    // picture.
    className: string;

    // The card offers to watch the stream from the profile, as a live Up next
    // card does. The line under a picture that is already playing does not.
    onWatch?: () => void;
};

// Ids for the profile, unique per tag, from a counter; see TitleSearch.
let nextProfileId = 0;

// "ELDEN RING · 2022 · Game": what is on, in one line, for labels and the
// profile popover.
export function nowPlayingLine(nowPlaying: NowPlaying): string {
    return [nowPlaying.title, nowPlaying.media?.year, kindLabel(nowPlaying)].filter(Boolean).join(' · ');
}

// Everything known about it, for a tooltip: a tag has room for a title, not a
// paragraph.
export function describeNowPlaying(nowPlaying: NowPlaying): string {
    return [nowPlayingLine(nowPlaying), nowPlaying.media?.facts, nowPlaying.media?.overview].filter(Boolean).join('\n');
}

function kindLabel(nowPlaying: NowPlaying): string {
    switch (nowPlaying.media?.kind) {
    case 'tv':
        return 'TV series';
    case 'movie':
        return 'Movie';
    case 'game':
        return 'Game';
    default:
        return categoryOf(nowPlaying.category)?.label || '';
    }
}

export const OnNow: React.FC<Props> = ({nowPlaying, who, className, onWatch}) => {
    const media = nowPlaying.media;
    const chipRef = React.useRef<HTMLButtonElement | null>(null);

    // Open or not, and whether the keyboard opened it: then focus goes into
    // it, to reach its link.
    const [open, setOpen] = React.useState<{keyboard: boolean} | null>(null);
    const [profileId] = React.useState(() => {
        nextProfileId += 1;

        return `villanet-onnow-${nextProfileId}`;
    });

    // Something else coming on closes the profile of what was on before.
    const ref = media?.ref || '';
    React.useEffect(() => setOpen(null), [ref]);

    // A press anywhere else closes it. A press, not focus leaving: Safari
    // does not focus a button that is clicked, so focus says nothing reliable
    // about where the pointer went.
    React.useEffect(() => {
        if (!open) {
            return undefined;
        }

        const away = (event: PointerEvent) => {
            const target = event.target as Node | null;
            if (target && (chipRef.current?.contains(target) || document.getElementById(profileId)?.contains(target))) {
                return;
            }

            setOpen(null);
        };

        document.addEventListener('pointerdown', away, true);

        return () => document.removeEventListener('pointerdown', away, true);
    }, [open, profileId]);

    const words = (
        <>
            <CategoryIcon
                category={nowPlaying.category}
                className='vntv-onnow-kind'
            />
            <span className='vntv-onnow-title'>{nowPlaying.title}</span>
        </>
    );

    if (!media) {
        return (
            <span
                className={`vntv-onnow ${className}`}
                title={describeNowPlaying(nowPlaying)}
            >{words}</span>
        );
    }

    return (
        <>
            <button
                ref={chipRef}
                type='button'
                className={`vntv-onnow ${className}`}
                data-media='true'
                data-open={open ? 'true' : undefined}
                aria-haspopup='dialog'
                aria-expanded={Boolean(open)}
                aria-controls={open ? profileId : undefined}
                aria-label={`On now: ${nowPlayingLine(nowPlaying)}. Show what it is`}
                title={describeNowPlaying(nowPlaying)}
                onClick={(event) => {
                    // The card under it watches the stream when it is
                    // pressed, which this press is not asking for.
                    event.stopPropagation();

                    // detail is 0 for a press made with the keyboard.
                    setOpen((was) => (was ? null : {keyboard: event.detail === 0}));
                }}
                onKeyDown={(event) => {
                    if (event.key === 'Escape' && open) {
                        event.stopPropagation();
                        setOpen(null);
                    }
                }}
                onBlur={(event) => {
                    // Tabbing on past it closes it; tabbing into it does not.
                    const next = event.relatedTarget as Node | null;
                    if (next && !document.getElementById(profileId)?.contains(next)) {
                        setOpen(null);
                    }
                }}
            >{words}</button>

            {open && chipRef.current ? (
                <MediaProfile
                    key={media.ref}
                    id={profileId}
                    media={media}
                    anchor={chipRef.current}
                    context={`${who} · live now`}
                    onWatch={onWatch ? () => {
                        setOpen(null);
                        onWatch();
                    } : undefined}
                    onClose={() => {
                        setOpen(null);
                        chipRef.current?.focus();
                    }}
                    onFocusLeave={(next) => {
                        // Only somewhere known: a click on nothing in
                        // particular is the press handler's to judge.
                        if (next && !chipRef.current?.contains(next)) {
                            setOpen(null);
                        }
                    }}
                    focusInside={open.keyboard}
                />
            ) : null}
        </>
    );
};
