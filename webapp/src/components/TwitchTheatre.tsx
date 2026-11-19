import React from 'react';
import {createPortal} from 'react-dom';

import {catchStrayKey, focusStops, keepKeysInside, trapTab} from '../focusTrap';
import type {Stream} from '../types';
import {TwitchFrame} from '../twitchEmbed';
import {channelURL, theatreSize} from '../twitchLogic';

import {CloseIcon} from './icons';

// The theatre (design/twitch-live.md 6.6): the panel's overlay that gives
// Twitch's player room when the panel has none. Twitch's player may not be
// smaller than 400 × 300, which a side panel only reaches in a window 1201 px
// or wider; anywhere narrower a Twitch stream plays here, opened by the same
// click that started it.
//
// Rendered by the tile whose stream it is (TwitchPlayer), through a portal
// onto document.body as the Trophies card is, so it closes when that tile
// goes: the panel giving way to a thread, the tile switched off, the stream
// ending. Its frame is another element from the tile's, so the tile shows a
// plate while it is open: one Twitch player per stream, ever.

// How big the theatre can be in this window, as the window changes size:
// null when even the whole window is too small for Twitch's minimum.
export function useTheatreRoom(): {w: number; h: number} | null {
    const read = () => theatreSize(window.innerWidth, window.innerHeight);
    const [room, setRoom] = React.useState(read);

    React.useEffect(() => {
        const resized = () => setRoom((current) => {
            const next = read();
            if (current && next && current.w === next.w && current.h === next.h) {
                return current;
            }

            return next;
        });
        window.addEventListener('resize', resized);

        return () => window.removeEventListener('resize', resized);
    }, []);

    return room;
}

type Props = {
    stream: Stream;
    host: string;
    login: string;

    // Whether this stream has the sound, which decides muted as the frame
    // loads; and how many times its tile's Reload was pressed. Both are in
    // the frame's key, as on the tile.
    audible: boolean;
    reloads: number;

    onClose: () => void;
};

export const TwitchTheatre: React.FC<Props> = ({stream, host, login, audible, reloads, onClose}) => {
    const room = useTheatreRoom();
    const card = React.useRef<HTMLDivElement | null>(null);
    const closer = React.useRef<HTMLButtonElement | null>(null);
    const title = stream.title;

    const closeRef = React.useRef(onClose);
    closeRef.current = onClose;

    // The window made too small for Twitch's minimum: closed, rather than a
    // player Twitch does not allow.
    React.useEffect(() => {
        if (room === null) {
            closeRef.current();
        }
    }, [room]);

    // Escape and Tab, on the document in the capture phase, as the other
    // dialogs do: Escape closes it and goes no further, Tab goes round the
    // card, Twitch's player included, and a letter typed with focus on
    // nothing is kept from Mattermost's message box. Close takes focus as it
    // opens; the tile puts it back on Play bigger when it closes.
    React.useEffect(() => {
        closer.current?.focus({preventScroll: true});

        const onKey = (event: KeyboardEvent) => {
            if (event.key === 'Escape') {
                if (document.fullscreenElement) {
                    return;
                }

                event.stopPropagation();
                closeRef.current();

                return;
            }

            if (catchStrayKey(event, card.current)) {
                return;
            }

            trapTab(event, card.current);
        };

        document.addEventListener('keydown', onKey, true);

        return () => document.removeEventListener('keydown', onKey, true);
    }, []);

    if (room === null) {
        return null;
    }

    const out = channelURL(login);

    // Tab from the last of the player's own controls leaves its frame for
    // whatever comes next in the page, and a key pressed inside a frame from
    // another site never reaches the document's listener above, so trapTab
    // cannot see it. What comes next is this, just after the card, which
    // hands focus straight back to the top of it. Shift+Tab out of the frame
    // needs nothing: it lands on Close, in the card already.
    const roundAgain = () => {
        const box = card.current;
        if (box) {
            (focusStops(box)[0] || box).focus({preventScroll: true});
        }
    };

    return createPortal(
        <div
            className='vntv vntv-modal vntv-twitch-theatre'
            role='presentation'
            data-handle={stream.handle}
            onMouseDown={(event) => {
                // Only a press that both starts and ends on the backdrop.
                if (event.target === event.currentTarget) {
                    closeRef.current();
                }
            }}
        >
            <div
                ref={card}
                className='vntv-modal-card vntv-twitch-theatre-card'
                role='dialog'
                aria-modal={true}
                aria-label={`Twitch player: ${title}`}
                tabIndex={-1}
                style={{width: room.w + 2}}
                onMouseDown={(event) => event.stopPropagation()}
                onKeyDown={keepKeysInside}
            >
                <header className='vntv-modal-head vntv-twitch-theatre-head'>
                    <h2
                        className='vntv-modal-title vntv-twitch-theatre-title'
                        title={stream.motd ? `${title}: ${stream.motd}` : title}
                    >{title}</h2>
                    <span className='vntv-live-tag vntv-card-tag-twitch'>{'Twitch'}</span>
                    {out !== '' ? (
                        <a
                            className='vntv-twitch-out'
                            href={out}
                            target='_blank'
                            rel='noopener noreferrer'
                        >{'Watch on Twitch ↗'}</a>
                    ) : null}
                    <button
                        ref={closer}
                        type='button'
                        className='vntv-modal-close'
                        aria-label='Close the bigger player'
                        title='Close'
                        onClick={() => closeRef.current()}
                    ><CloseIcon size={16}/></button>
                </header>

                <div className='vntv-twitch-theatre-stage'>
                    <TwitchFrame
                        login={login}
                        host={host}
                        muted={!audible}
                        width={room.w}
                        height={room.h}
                        reloadKey={`theatre|${stream.handle}|${audible ? 's' : 'm'}|${reloads}`}
                        title={`Twitch player: ${title}`}
                    />
                </div>
            </div>
            <span
                className='vntv-sr-only'
                tabIndex={0}
                onFocus={roundAgain}
            />
        </div>,
        document.body,
    );
};
