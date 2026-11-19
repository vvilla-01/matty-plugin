import React from 'react';

import {twitchPreviewURL} from '../client';
import {useFocusHome} from '../focusHome';
import {isPasteKey} from '../focusTrap';
import {useIdentity} from '../identity';
import {useSnapshot} from '../store';
import {closeTheatre, getTheatre, noteTileInline, openTheatre, useTheatre} from '../twitch';
import {embedHost, TwitchFrame} from '../twitchEmbed';
import {channelURL, inlineSize, onTwitchWords, tileMode, TWITCH_MIN_H, TWITCH_MIN_W, validLogin} from '../twitchLogic';
import type {TileMode} from '../twitchLogic';
import type {Stream} from '../types';

import {CloseIcon, FullscreenIcon, PopOutIcon, RetryIcon, SpeakerIcon, SpeakerMutedIcon} from './icons';
import {Backdrop} from './LiveCard';
import {TwitchTheatre, useTheatreRoom} from './TwitchTheatre';

// A Twitch stream's tile (design/twitch-live.md 6.4): Twitch's own player, in
// a plain frame of player.twitch.tv, in place of the Broadcast Box player.
// Sidebar.tsx renders it with the same props as Player.
//
// Two rows, and nothing laid over the player, because Twitch's rules say
// nothing may obscure it: a bar with whose it is and the tile's controls,
// and under it the stage, with the player or, when the player cannot run
// here, a plate. No HUD, no tuning screen, no static, no scissors: none of
// the signals they show exist for a frame that cannot be seen into, and
// Twitch's video must never be recorded (Schedule 1, :298).
//
// The plain embed can choose the sound only as it loads, so a change of who
// has the sound reloads the player; so does Reload, the tile's only retry,
// since nothing about the frame can be seen from here. Both are in the
// frame's key.

type Props = {
    stream: Stream;
    audible: boolean;
    onAudio: (handle: string | null) => void;
    onClose: () => void;
    closing?: boolean;
};

// What the plate says in each mode that has one.
const PLATE_WORDS: Record<Exclude<TileMode, 'inline'>, string> = {
    off: 'Twitch streams open on Twitch.',
    blocked: 'Twitch’s player can’t run on this address.',
    theatre: 'Playing in the bigger player.',
    small: `Too narrow for Twitch’s player, which needs ${TWITCH_MIN_W}\u00a0×\u00a0${TWITCH_MIN_H}.`,
};

// Printable keys pressed on the tile itself stop there, as on any tile
// (Player.tsx), or the next letter would go to Mattermost's message box.
function onTileKey(event: React.KeyboardEvent<HTMLDivElement>): void {
    if (event.target !== event.currentTarget) {
        return;
    }

    // A paste by Mattermost's own rule (focusTrap.ts), which knows a Ctrl+V
    // from a keyboard that types another letter on that key.
    const command = event.ctrlKey || event.metaKey;
    const paste = isPasteKey(event);
    if (paste || (event.key.length === 1 && !command && !event.altKey)) {
        event.stopPropagation();
    }
}

export const TwitchPlayer: React.FC<Props> = ({stream, audible, onAudio, onClose, closing}) => {
    const handle = stream.handle;
    const login = stream.twitchLogin || '';
    const snapshot = useSnapshot();
    const me = useIdentity();
    const theatre = useTheatre();
    const room = useTheatreRoom();
    const host = embedHost();

    const rootRef = React.useRef<HTMLDivElement | null>(null);
    const stageRef = React.useRef<HTMLDivElement | null>(null);
    const biggerRef = React.useRef<HTMLButtonElement | null>(null);

    // How wide the stage is, and whether the player fits it: measured, and
    // decided with the hysteresis in twitchLogic.ts, so a panel dragged to
    // about 400 never flips the tile between the two, reloading the player.
    const [size, setSize] = React.useState<{width: number; fits: boolean}>({width: 0, fits: false});
    React.useLayoutEffect(() => {
        const stage = stageRef.current;
        if (!stage) {
            return undefined;
        }

        const take = () => {
            const width = stage.getBoundingClientRect().width;
            setSize((current) => {
                const fits = width > 0 && inlineSize(width, current.fits) !== null;

                return current.width === width && current.fits === fits ? current : {width, fits};
            });
        };
        take();

        if (typeof ResizeObserver === 'undefined') {
            window.addEventListener('resize', take);

            return () => window.removeEventListener('resize', take);
        }

        const observer = new ResizeObserver(take);
        observer.observe(stage);

        return () => observer.disconnect();
    }, []);

    const mode = tileMode({
        embedOn: Boolean(snapshot.twitchEmbed),
        hostOk: host.ok,
        loginOk: validLogin(login),
        theatre: theatre === handle,
        fits: size.fits,
    });
    const playerSize = mode === 'inline' ? inlineSize(size.width, true) : null;

    // Which tiles are inline, for a card pressed again (twitch.ts): a tile
    // already playing inline stays inline down to 400.
    React.useEffect(() => {
        noteTileInline(handle, mode === 'inline');

        return () => noteTileInline(handle, false);
    }, [handle, mode]);

    // The theatre open for this stream while it cannot play here after all —
    // the admin switched the player off, or the window got too small — is
    // closed; and it goes with this tile.
    const theatreHere = theatre === handle;
    React.useEffect(() => {
        if (theatreHere && (mode !== 'theatre' || room === null)) {
            closeTheatre();
        }
    }, [theatreHere, mode, room]);

    React.useEffect(() => () => {
        if (getTheatre() === handle) {
            closeTheatre();
        }
    }, [handle]);

    // Reload: a new frame. Counted, because the count is in its key.
    const [reloads, setReloads] = React.useState(0);

    // Play bigger: the theatre, with the sound, inside the click — the sound
    // first, so the theatre's player loads once, already with it.
    const playBigger = () => {
        onAudio(handle);
        openTheatre(handle);
    };

    // The theatre closing from inside it: focus back on Play bigger, which
    // the tile now shows, or on the tile.
    const [refocus, setRefocus] = React.useState(0);
    React.useEffect(() => {
        if (refocus > 0) {
            (biggerRef.current || rootRef.current)?.focus({preventScroll: true});
        }
    }, [refocus]);
    const closeBigger = () => {
        closeTheatre();
        setRefocus((count) => count + 1);
    };

    // A control going while it has focus — Bring it back, the sound and
    // Reload when the tile leaves inline — leaves focus on the tile, never
    // on the page.
    const keepFocus = useFocusHome(() => rootRef.current);

    const name = stream.title;
    const out = channelURL(login);
    const canBigger = room !== null && (mode === 'inline' || mode === 'small');
    const showFrame = mode === 'inline' && playerSize !== null && !closing;

    // The plate's picture: the relayed preview while previews are on, else
    // the person's own, as their card shows.
    const preview = snapshot.livePreviews && stream.previewVersion ? twitchPreviewURL(handle, stream.previewVersion) : '';

    // Why it cannot run here, for an admin: the page's address, or the
    // channel's name.
    let reason = '';
    if (mode === 'blocked' && me?.isAdmin) {
        reason = host.ok ? 'That channel’s name can’t be used in Twitch’s player.' : host.reason;
    }

    const watchOnTwitch = out !== '' ? (
        <a
            className='vntv-twitch-button'
            href={out}
            target='_blank'
            rel='noopener noreferrer'
            aria-label='Watch on Twitch'
            title='Watch on Twitch ↗'
        ><PopOutIcon size={15}/></a>
    ) : null;

    return (
        <div
            ref={rootRef}
            className='vntv-player vntv-twitch-tile'
            data-mode={mode}
            data-closing={closing ? 'true' : undefined}
            tabIndex={-1}
            onKeyDown={onTileKey}
            onFocus={keepFocus.onFocus}
            onBlur={keepFocus.onBlur}
        >
            <div
                className='vntv-twitch-bar'
                role='group'
                aria-label={`${name} on Twitch`}
            >
                <span
                    className='vntv-twitch-name'
                    title={stream.motd ? `${name}: ${stream.motd}` : name}
                >{name}</span>
                <span className='vntv-live-tag vntv-card-tag-twitch'>{'Twitch'}</span>
                <span className='vntv-twitch-count'>{onTwitchWords(stream.viewers)}</span>

                <span className='vntv-twitch-tools'>
                    {mode === 'inline' ? (
                        <>
                            <button
                                type='button'
                                className='vntv-twitch-button'
                                aria-pressed={audible}
                                aria-label='Sound (reloads Twitch’s player)'
                                title={audible ? 'Sound on: press to mute (reloads Twitch’s player)' : 'Sound (reloads Twitch’s player)'}
                                onClick={() => onAudio(audible ? null : handle)}
                            >{audible ? <SpeakerIcon size={15}/> : <SpeakerMutedIcon size={15}/>}</button>
                            <button
                                type='button'
                                className='vntv-twitch-button'
                                aria-label='Reload Twitch’s player'
                                title='Reload Twitch’s player'
                                onClick={() => setReloads((count) => count + 1)}
                            ><RetryIcon size={15}/></button>
                        </>
                    ) : null}
                    {canBigger ? (
                        <button
                            ref={biggerRef}
                            type='button'
                            className='vntv-twitch-button'
                            aria-label='Play bigger'
                            title='Play bigger'
                            onClick={playBigger}
                        ><FullscreenIcon size={15}/></button>
                    ) : null}
                    {watchOnTwitch}
                    <button
                        type='button'
                        className='vntv-twitch-button'
                        aria-label={`Switch off ${name}`}
                        title={`Switch off ${name}`}
                        disabled={closing}
                        onClick={onClose}
                    ><CloseIcon size={15}/></button>
                </span>
            </div>

            <div
                ref={stageRef}
                className='vntv-twitch-stage'
            >
                {showFrame && playerSize ? (
                    <TwitchFrame
                        login={login}
                        host={host.ok ? host.host : ''}
                        muted={!audible}
                        width={playerSize.w}
                        height={playerSize.h}
                        reloadKey={`${handle}|${audible ? 's' : 'm'}|${reloads}`}
                        title={`Twitch player: ${name}`}
                    />
                ) : (
                    <div
                        className='vntv-twitch-plate'
                        data-mode={mode}
                    >
                        {preview ? (
                            <img
                                className='vntv-twitch-plate-picture'
                                src={preview}
                                alt=''
                                draggable={false}
                            />
                        ) : (
                            <Backdrop userId={stream.userId}/>
                        )}
                        {closing || mode === 'inline' ? null : (
                            <span className='vntv-twitch-plate-words'>
                                <span className='vntv-twitch-plate-text'>{PLATE_WORDS[mode]}</span>
                                {reason !== '' ? <span className='vntv-twitch-plate-reason'>{reason}</span> : null}
                                {mode === 'theatre' ? (
                                    <button
                                        type='button'
                                        className='vntv-pill'
                                        onClick={closeBigger}
                                    >{'Bring it back'}</button>
                                ) : null}
                                {mode === 'small' && canBigger ? (
                                    <button
                                        type='button'
                                        className='vntv-pill'
                                        onClick={playBigger}
                                    >{'Play bigger'}</button>
                                ) : null}
                                {(mode === 'off' || mode === 'blocked' || (mode === 'small' && !canBigger)) && out !== '' ? (
                                    <a
                                        className='vntv-pill vntv-twitch-out'
                                        href={out}
                                        target='_blank'
                                        rel='noopener noreferrer'
                                    >{'Watch on Twitch ↗'}</a>
                                ) : null}
                            </span>
                        )}
                    </div>
                )}
            </div>

            {theatreHere && mode === 'theatre' && host.ok ? (
                <TwitchTheatre
                    stream={stream}
                    host={host.host}
                    login={login}
                    audible={audible}
                    reloads={reloads}
                    onClose={closeBigger}
                />
            ) : null}
        </div>
    );
};
