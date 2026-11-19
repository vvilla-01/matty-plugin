import React from 'react';
import {createPortal} from 'react-dom';

import {mediaImageURL} from '../client';
import type {MediaInfo} from '../types';

import {Avatar} from './Avatar';

// The little profile of a movie, show or game that a planned stream is linked
// to: its cover, what it is, a few lines about it, and where to read more.
//
// Drawn on document.body and placed beside the card by hand, not inside the
// rail: the rail scrolls sideways, and a scrolling box clips everything that
// hangs out of it in both directions. Placed under the card when it fits and
// over it when it does not, and never off the side of the window.
//
// Everything in it is text the server wrote, rendered as text. The one link is
// built by the server from an id, and is checked again here against the only
// four places it can point — a link that somehow pointed anywhere else is
// simply not drawn.

type Props = {
    id: string;
    media: MediaInfo;

    // The card, for placing this beside it — and for following it when the
    // rail or the page scrolls, rather than being left behind.
    anchor: HTMLElement;

    // Whose plan and when, as the card says it.
    context: string;

    // A plan that is on air now can be watched from here — which is the only
    // way to on a touch screen, where a tap on a linked card opens this.
    onWatch?: () => void;

    // Pointing into it and away, for a profile that opens on hover. One that
    // opens on a press, as what is on a stream does, has no use for them.
    onPointerEnter?: (event: React.PointerEvent) => void;
    onPointerLeave?: (event: React.PointerEvent) => void;

    // Escape: close, and the card takes focus back.
    onClose: () => void;

    // Focus has left this — to wherever next is, which may be the card.
    onFocusLeave: (next: Node | null) => void;

    // Set when it was opened from the keyboard, so focus goes into it.
    focusInside?: boolean;

    // Who is in on it: the host, and anybody who has joined. A plan's
    // profile lists them; what is on a stream has nobody to list but the
    // streamer, who is already named in the context line, so it leaves the
    // list out by giving no host.
    host?: {userId?: string; who: string};
    joined?: Array<{userId: string; who: string}>;

    // Joining from here, for somebody who can.
    onJoin?: (join: boolean) => void;
    amIn?: boolean;
    busy?: boolean;

    // Opened from inside one of the panel's own dialogs (a clip's tag in the
    // Clips dialog), which sit above Mattermost's modals: the profile goes
    // above the dialog in turn, or it would open underneath it.
    layer?: 'modal';

    // Where it is drawn, when not on the page's body: that dialog's card, so
    // it is inside the dialog for its Tab trap and for a screen reader that
    // hides everything outside an aria-modal dialog. Still placed on the
    // window (position: fixed; the card has no transform), so where it shows
    // is the same.
    container?: Element | null;
};

const WIDTH = 312;
const GAP = 8;
const MARGIN = 8;

const LINK_HOSTS = ['https://www.imdb.com/', 'https://www.themoviedb.org/', 'https://store.steampowered.com/', 'https://www.igdb.com/'];

export const MediaProfile: React.FC<Props> = ({id, media, anchor, context, onWatch, onPointerEnter, onPointerLeave, onClose, onFocusLeave, focusInside, host, joined = [], onJoin, amIn, busy, layer, container}) => {
    const boxRef = React.useRef<HTMLDivElement | null>(null);
    const [place, setPlace] = React.useState<{top: number; left: number} | null>(null);
    const [coverFailed, setCoverFailed] = React.useState(false);
    const [backdropFailed, setBackdropFailed] = React.useState(false);

    // Measured before it is shown, so it appears where it belongs rather than
    // jumping there — and again whenever anything scrolls or the window
    // changes size, once a frame at most, so it stays with its card.
    const placeIt = React.useCallback(() => {
        const box = boxRef.current;
        if (!box || !anchor.isConnected) {
            return;
        }

        const card = anchor.getBoundingClientRect();
        const height = box.offsetHeight;
        const below = card.bottom + GAP;
        const fitsBelow = below + height <= window.innerHeight - MARGIN;
        const top = fitsBelow ? below : Math.max(MARGIN, card.top - GAP - height);
        const left = Math.min(Math.max(MARGIN, card.left), window.innerWidth - WIDTH - MARGIN);

        setPlace((was) => (was && was.top === top && was.left === left ? was : {top, left}));
    }, [anchor]);

    // Every render, not only when the card changes: the rail re-renders when
    // the schedule does, and things above it opening or closing move the card
    // without anything scrolling. placeIt does nothing when nothing moved.
    React.useLayoutEffect(placeIt);

    // And when it grows by itself — somebody joining adds a name to it.
    React.useEffect(() => {
        const box = boxRef.current;
        if (!box || typeof ResizeObserver === 'undefined') {
            return undefined;
        }

        const observer = new ResizeObserver(() => placeIt());
        observer.observe(box);

        return () => observer.disconnect();
    }, [placeIt]);

    React.useEffect(() => {
        let frame = 0;
        const soon = () => {
            if (!frame) {
                frame = window.requestAnimationFrame(() => {
                    frame = 0;
                    placeIt();
                });
            }
        };

        window.addEventListener('scroll', soon, true);
        window.addEventListener('resize', soon);

        return () => {
            window.cancelAnimationFrame(frame);
            window.removeEventListener('scroll', soon, true);
            window.removeEventListener('resize', soon);
        };
    }, [placeIt]);

    React.useEffect(() => {
        if (focusInside && place) {
            boxRef.current?.querySelector<HTMLElement>('a, button')?.focus();
        }
    }, [focusInside, place]);

    const link = media.link && LINK_HOSTS.some((host) => media.link?.startsWith(host)) ? media.link : '';
    const linkLabel = (() => {
        if (link.startsWith('https://store.steampowered.com/')) {
            return 'Steam store';
        }
        if (link.startsWith('https://www.igdb.com/')) {
            return 'IGDB';
        }

        return link.startsWith('https://www.imdb.com/') ? 'IMDb' : 'TMDB';
    })();

    const kindLabel = (() => {
        switch (media.kind) {
        case 'movie':
            return 'Movie';
        case 'tv':
            return 'TV series';
        case 'game':
            return 'Game';
        default:
            return '';
        }
    })();

    return createPortal(
        <div
            ref={boxRef}
            id={id}
            className='vntv vntv-mediaprofile'
            role='dialog'
            aria-label={media.title}
            data-kind={media.kind}
            data-layer={layer}

            // Focusable itself, though not a tab stop: a click on its words
            // then lands focus here, inside it, instead of on the page — which
            // the card would read as focus leaving, and close it.
            tabIndex={-1}
            style={place ? {top: place.top, left: place.left, width: WIDTH} : {top: 0, left: 0, width: WIDTH, visibility: 'hidden'}}
            onPointerEnter={onPointerEnter}
            onPointerLeave={onPointerLeave}

            // Drawn on the page body, but React still hands its clicks up to
            // whatever opened it, as if it were inside: the Live now card,
            // which watches the stream when pressed and closes every other
            // tile. Nothing pressed in here is a press on that. Watch now
            // asks for it outright, through onWatch.
            onClick={(event) => event.stopPropagation()}
            onBlur={(event) => {
                const next = event.relatedTarget as Node | null;
                if (!next || !event.currentTarget.contains(next)) {
                    onFocusLeave(next);
                }
            }}
            onKeyDown={(event) => {
                if (event.key === 'Escape') {
                    event.stopPropagation();
                    onClose();
                }
            }}
        >
            {!backdropFailed && (
                <img
                    className='vntv-mediaprofile-backdrop'
                    src={mediaImageURL(media.ref, 'backdrop')}
                    alt=''
                    aria-hidden={true}
                    draggable={false}
                    onError={() => setBackdropFailed(true)}
                />
            )}

            <div className='vntv-mediaprofile-head'>
                <span
                    className='vntv-mediaprofile-cover'
                    data-kind={media.kind}
                >
                    {!coverFailed && (
                        <img
                            src={mediaImageURL(media.ref, 'cover')}
                            alt={`Cover of ${media.title}`}
                            draggable={false}
                            onError={() => setCoverFailed(true)}
                        />
                    )}
                </span>

                <div className='vntv-mediaprofile-heading'>
                    <p className='vntv-mediaprofile-eyebrow'>{[kindLabel, media.year].filter(Boolean).join(' · ')}</p>
                    <p className='vntv-mediaprofile-title'>{media.title}</p>
                    {media.facts ? <p className='vntv-mediaprofile-facts'>{media.facts}</p> : null}
                    <p className='vntv-mediaprofile-context'>{context}</p>
                </div>
            </div>

            {media.overview ? <p className='vntv-mediaprofile-overview'>{media.overview}</p> : null}

            {/* Who is in: the host first, then everybody who joined, by
                name — the card only has room for their faces. */}
            {host ? (
                <div className='vntv-mediaprofile-crew'>
                    <p className='vntv-mediaprofile-crew-head'>{joined.length > 0 ? `Who's in · ${joined.length + 1}` : "Who's in"}</p>
                    <ul className='vntv-mediaprofile-crew-list'>
                        {[{userId: host.userId, who: host.who, hosting: true}, ...joined.map((person) => ({...person, hosting: false}))].map((person) => (
                            <li
                                key={person.userId || person.who}
                                className='vntv-mediaprofile-crew-person'
                            >
                                <Avatar
                                    className='vntv-mediaprofile-crew-face'
                                    userId={person.userId}
                                    name={person.who}
                                    decorative={true}
                                />
                                <span>{person.who}</span>
                                {person.hosting ? <span className='vntv-mediaprofile-crew-host'>{'host'}</span> : null}
                            </li>
                        ))}
                    </ul>
                </div>
            ) : null}

            <div className='vntv-mediaprofile-foot'>
                {link !== '' && (
                    <a
                        className='vntv-mediaprofile-link'
                        href={link}
                        target='_blank'
                        rel='noopener noreferrer nofollow'
                    >{`${linkLabel} ↗`}</a>
                )}

                {/* TMDB and IGDB both ask for this wherever their data is
                    shown. */}
                {media.source ? (
                    <span className='vntv-mediaprofile-source'>{`Data from ${media.source}`}</span>
                ) : null}

                {/* Not disabled while saving: a disabled button drops the
                    keyboard focus it had, and the profile with it. */}
                {onJoin ? (
                    <button
                        type='button'
                        className={`vntv-pill vntv-mediaprofile-join${amIn ? ' vntv-pill-on' : ''}`}
                        aria-pressed={Boolean(amIn)}
                        aria-disabled={busy ? true : undefined}
                        onClick={() => {
                            if (!busy) {
                                onJoin(!amIn);
                            }
                        }}
                    >{amIn ? 'In ✓' : 'Join'}</button>
                ) : null}

                {onWatch ? (
                    <button
                        type='button'
                        className='vntv-pill vntv-mediaprofile-watch'
                        onClick={onWatch}
                    >{'Watch now'}</button>
                ) : null}
            </div>
        </div>,
        container || document.body,
    );
};
