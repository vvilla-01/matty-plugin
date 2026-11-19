import React from 'react';

import {popOutURL, streamPictureURL} from '../client';
import {scissorsView} from '../clipLogic';
import type {ScissorsView} from '../clipLogic';
import {startClip, useClipEditor} from '../clips';
import {useAccess, useIdentity} from '../identity';
import {composing} from '../keys';
import {
    getSessionInfo,
    mountVideo,
    quietSession,
    retrySession,
    setSessionVolume,
    takeSound,
    UNMUTE_VOLUME,
    useSessionInfo,
} from '../sessions';
import {useSnapshot} from '../store';
import {useFocusHome} from '../focusHome';
import {isPasteKey} from '../focusTrap';
import type {ClipTail, Stream} from '../types';

import {Hud, HudButton, TileInfo} from './Hud';
import {ExitFullscreenIcon, FullscreenIcon, PopOutIcon, ScissorsIcon, SpeakerIcon, SpeakerMutedIcon} from './icons';
import {Plate, Screen, Tuning} from './NoSignal';

// ---- The scissors (spec 3.4, addendum 3.8) ----
//
// What they say and do is worked out from the snapshot in clipLogic.ts
// (scissorsView): ready to clip the last two minutes, or what has been
// recorded so far with its length in the corner, or why not, greyed. For
// somebody who has not linked a stream, always the one enabled button asking
// them to; nothing at all while that is not known. The server decides again
// when they are pressed.
//
// The clock ticks only while it changes what they say: while the recording is
// shorter than a press would take, the corner tag counts up, and the button
// wakes once five seconds are held; and while the end of an earlier stretch
// is held after a drop, which they offer until its time is up. Once a
// second, while the tile is there, and then not at all.
function useSecondsWhile(running: boolean): void {
    const [, tick] = React.useReducer((count: number) => count + 1, 0);

    React.useEffect(() => {
        if (!running) {
            return undefined;
        }

        const timer = window.setInterval(tick, 1000);

        return () => window.clearInterval(timer);
    }, [running]);
}

function useScissors(stream: Stream): {view: ScissorsView | null; lengthSeconds: number} {
    const snapshot = useSnapshot();
    const access = useAccess();
    const editor = useClipEditor();

    const info = snapshot.clips;
    const lengthSeconds = info?.lengthSeconds || 120;
    const now = Date.now();
    const growing = stream.clip === 'ready' && typeof stream.clipFrom === 'number' &&
        now - stream.clipFrom < (lengthSeconds + 1) * 1000;
    const tailHeld = typeof stream.clipTail === 'number' && stream.clipTail > now;
    useSecondsWhile(growing || tailHeld);

    const view = scissorsView({
        tile: {
            kind: 'live',
            alwaysOn: stream.alwaysOn,
            clip: stream.clip,
            clipFrom: stream.clipFrom,

            // The end of the stretch before a drop, still held while the new
            // recording warms up: clipped as a tail, "before the stream
            // dropped", and as much of it as was held.
            clipTail: stream.clipTail,
            tailHeldMs: snapshot.clipTails?.find((held) => held.handle === stream.handle)?.heldMs,
        },
        linked: access === 'linked',
        clipsEnabled: Boolean(info?.enabled),
        lengthSeconds,
        name: stream.title,
        cutting: editor?.phase === 'cutting' && editor.handle === stream.handle,
        now,
    });

    return {view, lengthSeconds};
}

// The scissors on a tile waiting for its stream to come back: the end of the
// stream, while the server still holds it (snapshot.clipTails), for the
// people who were watching when it stopped. Always a tail. Looked at again
// every second while the tile is up and the end is held, so they go when it
// does.
function useTailScissors(handle: string, name: string): {view: ScissorsView | null; lengthSeconds: number; tail: ClipTail | null} {
    const snapshot = useSnapshot();
    const access = useAccess();
    const editor = useClipEditor();

    const info = snapshot.clips;
    const lengthSeconds = info?.lengthSeconds || 120;
    const now = Date.now();
    const tail = snapshot.clipTails?.find((held) => held.handle === handle && held.until > now) || null;
    useSecondsWhile(tail !== null);

    const view = scissorsView({
        tile: {kind: 'waiting', tailUntil: tail?.until, tailHeldMs: tail?.heldMs, tailReason: tail?.reason},
        linked: access === 'linked',
        clipsEnabled: Boolean(info?.enabled),
        lengthSeconds,
        name,
        cutting: editor?.phase === 'cutting' && editor.handle === handle,
        now,
    });

    return {view, lengthSeconds, tail};
}

// A press of the scissors, or C: out of full screen first, in the same press,
// or the editor's dialog would open under the picture's top layer and nobody
// could see it; then the editor, which hands focus back to the scissors when
// it closes. tail is why the stream stopped, for a press that clips the end
// of it.
function pressScissors(scissors: ScissorsView | null, stream: {handle: string; title: string}, lengthSeconds: number,
    button: HTMLButtonElement | null, tail?: 'ended' | 'dropped'): void {
    if (!scissors || scissors.action === 'none') {
        return;
    }

    if (document.fullscreenElement) {
        void document.exitFullscreen().catch(() => {
            // Ignored.
        });
    }

    if (scissors.source) {
        startClip(stream, scissors.source, lengthSeconds, button, scissors.source === 'tail' ? tail : undefined);
    }
}

// The scissors on the picture, as a HUD button.
const ScissorsButton: React.FC<{view: ScissorsView; buttonRef: React.Ref<HTMLButtonElement>; onPress: () => void}> = ({view, buttonRef, onPress}) => (
    <HudButton
        label={view.label}
        title={view.title}
        ariaDisabled={view.disabled}
        busy={view.busy}
        tag={view.tag}
        labelKey={`${view.action}|${view.source || ''}|${view.disabled}|${view.busy}|${view.tag ? 'growing' : 'whole'}`}
        keyShortcuts={view.action === 'none' ? undefined : 'C'}
        buttonRef={buttonRef}
        onClick={onPress}
    ><ScissorsIcon size={16}/></HudButton>
);

// A key pressed in something that types: C there is a letter.
function typingIn(target: EventTarget | null): boolean {
    if (!(target instanceof HTMLElement)) {
        return false;
    }
    if (target.isContentEditable || target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement) {
        return true;
    }

    return target instanceof HTMLInputElement &&
        !['range', 'checkbox', 'radio', 'button', 'submit', 'reset', 'color', 'file', 'image'].includes(target.type);
}

// C on the tile clips it, as the scissors do (spec 3.4 and 3.5): with no
// modifier, so Ctrl+C and ⌘+C still copy; not while an input method is
// composing; and not in anything that types. Stopped here, as every key
// the panel acts on is, so Mattermost never moves it to its message box.
// While the scissors are greyed, C is still theirs and does nothing.
//
// The tile can take focus (tabIndex -1, never a Tab stop) so a click on the
// picture lands there, and C works straight after it. Other printable keys
// pressed on the tile itself stop there too, as they do on the panel
// (Sidebar.tsx), or the next letter would go to the message box.
function onTileKey(event: React.KeyboardEvent<HTMLDivElement>, scissors: ScissorsView | null, press: () => void): void {
    const command = event.ctrlKey || event.metaKey;
    if (scissors && (event.key === 'c' || event.key === 'C') && !command && !event.altKey &&
        !composing(event) && !typingIn(event.target)) {
        event.preventDefault();
        event.stopPropagation();
        if (!event.repeat) {
            press();
        }

        return;
    }

    if (event.target !== event.currentTarget) {
        return;
    }

    // A paste by Mattermost's own rule (focusTrap.ts), which knows a Ctrl+V
    // from a keyboard that types another letter on that key.
    const paste = isPasteKey(event);
    if (paste || (event.key.length === 1 && !command && !event.altKey)) {
        event.stopPropagation();
    }
}

type Props = {
    stream: Stream;

    // Which tile has the sound is owned by the panel, not by the tile: unmuting
    // one has to mute the others, and a tile cannot do that to its neighbours.
    audible: boolean;
    onAudio: (handle: string | null) => void;
    onClose: () => void;

    // Being switched off: the tube collapses to a line and a dot. The panel
    // takes the tile away once that has played.
    closing?: boolean;
};

// A tile does not own its stream any more. The connection and the one <video>
// it plays into belong to the session pool (sessions.ts), which outlives the
// panel; the tile lends it an empty slot, and the pool moves the element into
// it. So the panel can be covered by a thread and come back, or be remounted
// for any other reason, and the tile picks up the same picture, still playing,
// with no new connection and no tuning burst.
//
// What the tile shows — tuning, playing, failed, and the volume — is read from
// the pool, which is where it is true.
export const Player: React.FC<Props> = ({stream, audible: hasSound, onAudio, onClose, closing}) => {
    const handle = stream.handle;
    const frameRef = React.useRef<HTMLDivElement | null>(null);
    const slotRef = React.useRef<HTMLDivElement | null>(null);

    // Bumped by Retry when there is no session to retry, to ask the pool for
    // one again.
    const [mountKey, setMountKey] = React.useState(0);

    // Whether the slot has been lent yet. Before that, no session is not news;
    // after it, it means the pool would not start one, and the tile says so
    // rather than tuning for ever.
    const [lent, setLent] = React.useState(false);
    const [fullscreen, setFullscreen] = React.useState(false);

    // The icon has to follow the real state, which the viewer can also leave
    // with Escape rather than the button.
    React.useEffect(() => {
        const sync = () => setFullscreen(document.fullscreenElement === frameRef.current);
        document.addEventListener('fullscreenchange', sync);

        return () => document.removeEventListener('fullscreenchange', sync);
    }, []);

    // A layout effect, so the picture is in the slot before the browser paints
    // and a remounted tile never shows a frame without it. Setting lent here
    // renders again before that paint too, now with the session the pool has
    // just found or started.
    React.useLayoutEffect(() => {
        const slot = slotRef.current;
        if (!slot) {
            return undefined;
        }

        const unmount = mountVideo(handle, slot);
        setLent(true);

        return unmount;
    }, [handle, mountKey]);

    const info = useSessionInfo(handle);

    // The sound cuts the instant the switch is pressed, while the picture
    // plays its animation.
    React.useLayoutEffect(() => {
        quietSession(handle, Boolean(closing));
    }, [handle, closing]);

    // Whether this tile really has the sound: the panel's say-so, and the
    // browser not having refused it. A refused tile offers "Listen to this
    // stream" again, whose click is what the browser was waiting for.
    const audible = hasSound && !(info?.blocked);
    const volume = info ? info.volume : UNMUTE_VOLUME;
    const tuning = info ? info.status === 'connecting' : !lent;
    const missing = lent && info === null;
    const error = info?.status === 'failed' ? info.error : '';

    // Taking the sound has to happen inside the click itself. Browsers treat
    // unmuting as needing a user gesture, and a React effect runs after the
    // event has finished.
    const takeAudio = React.useCallback(() => takeSound(handle), [handle]);

    const applyVolume = React.useCallback((next: number) => setSessionVolume(handle, next), [handle]);

    const retry = React.useCallback(() => {
        if (getSessionInfo(handle)) {
            retrySession(handle);
        } else {
            setMountKey((value) => value + 1);
        }
    }, [handle]);

    const toggleFullscreen = React.useCallback(() => {
        const frame = frameRef.current;
        if (!frame) {
            return;
        }

        if (document.fullscreenElement) {
            void document.exitFullscreen().catch(() => {
                // Ignored.
            });
            return;
        }

        void frame.requestFullscreen?.().catch(() => {
            // Ignored: some embeddings disallow it.
        });
    }, []);

    // Pop out to the public watch page: for linked people only, while an admin
    // has set one, and never for a 24/7 channel (design/lock-screen.md 3.10).
    // A plain link to the plugin, which redirects; the stream key it hands
    // out is never in anything this page was sent.
    const me = useIdentity();
    const access = useAccess();
    const watchURL = access === 'linked' && me?.popOut && !stream.alwaysOn ? popOutURL(handle) : '';
    const sliderValue = audible ? Math.round(volume * 100) : 0;

    const {view: scissors, lengthSeconds} = useScissors(stream);
    const scissorsRef = React.useRef<HTMLButtonElement | null>(null);

    // A live tile's tail is always the end before a drop: a stream that went
    // off has no live tile.
    const press = () => pressScissors(scissors, {handle, title: stream.title}, lengthSeconds, scissorsRef.current, 'dropped');

    // The scissors going while they have focus — the end before a drop let
    // go, Clips switched off — or Retry once it has worked: focus stays on
    // the tile rather than falling to the page.
    const keepFocus = useFocusHome(() => frameRef.current);

    return (
        <div
            className='vntv-player'
            ref={frameRef}
            tabIndex={-1}
            onKeyDown={(event) => onTileKey(event, scissors, press)}
            onFocus={keepFocus.onFocus}
            onBlur={keepFocus.onBlur}
        >
            {/* Snow and scanlines only when there is no picture. While it is
                tuning the burst below brings its own, so this stays off and
                there is never more than one animating layer; once it is
                playing there is nothing to dress up. An error is a no-signal
                state like any other, so that one gets the static. */}
            <Screen
                noise={error !== '' || missing}
                power={closing ? 'off' : undefined}
            >
                {/* The slot the pool puts the picture in. First, and always
                    there, so React never replaces it: a replaced slot would
                    take the picture out of the page with it. Nothing else is
                    ever rendered into it. */}
                <div
                    className='vntv-video-slot'
                    ref={slotRef}
                />

                {/* Between channels. Held until the picture arrives rather than
                    for a fixed time, so it never clears onto a black rectangle
                    and never lingers over a stream that is already playing. */}
                {tuning && <Tuning title={stream.title}/>}

                {/* The pool would not start a session for this stream, which
                    should not happen while the panel shows it; never a tuning
                    burst that waits for nothing. */}
                {missing && <Plate title='Not connected'/>}

                {/* On the picture, and only while somebody is pointing at it:
                    the switch in the middle, sound and full screen along the
                    bottom. See Hud.tsx for when they show. */}
                <Hud
                    powerLabel={`Switch off ${stream.title}`}
                    onPower={onClose}
                    off={closing}
                >
                    {/* No aria-pressed: the accessible name already flips, and a
                        pressed state alongside a flipping name contradicts it —
                        "Listen to this stream, pressed" on a silent tile. */}
                    <HudButton
                        label={audible ? 'Mute' : 'Listen to this stream'}
                        onClick={() => (audible ? onAudio(null) : takeAudio())}
                    >{audible ? <SpeakerIcon size={16}/> : <SpeakerMutedIcon size={16}/>}</HudButton>
                    <input
                        type='range'
                        className='vntv-volume'
                        min={0}
                        max={100}
                        step={1}
                        value={sliderValue}
                        aria-label='Volume'
                        title={audible ? `Volume ${sliderValue}%` : 'Muted — drag to listen to this stream'}
                        onChange={(event) => applyVolume(Number(event.target.value) / 100)}
                    />
                    <span className='vntv-hud-spacer'/>
                    {scissors ? (
                        <ScissorsButton
                            view={scissors}
                            buttonRef={scissorsRef}
                            onPress={press}
                        />
                    ) : null}
                    {watchURL !== '' && (
                        <a
                            className='vntv-hud-button'
                            href={watchURL}
                            target='_blank'
                            rel='noopener noreferrer'
                            aria-label='Open in a new tab'
                            title='Open in a new tab'
                        ><PopOutIcon size={16}/></a>
                    )}
                    <HudButton
                        label={fullscreen ? 'Exit full screen' : 'Full screen'}
                        onClick={toggleFullscreen}
                    >{fullscreen ? <ExitFullscreenIcon size={16}/> : <FullscreenIcon size={16}/>}</HudButton>
                </Hud>
            </Screen>

            <TileInfo
                userId={stream.userId}
                picture={streamPictureURL(stream)}
                name={stream.title}
                note={stream.motd}
                nowPlaying={stream.alwaysOn ? undefined : stream.nowPlaying}
            />
            {(error !== '' || missing) && (
                <div className='vntv-player-status vntv-error'>
                    {error || 'Not connected'}
                    <button
                        type='button'
                        className='vntv-pill'
                        style={{marginLeft: 8}}
                        onClick={retry}
                    >{'Retry'}</button>
                </div>
            )}
        </div>
    );
};

type WaitingProps = {
    // The stream's handle, for the scissors that clip the end of it while the
    // server still holds it.
    handle: string;
    title: string;

    // The stream as it was last seen, for whose face and what they were
    // streaming. Absent if it was never seen at all.
    seen?: Stream | null;
    onClose: () => void;
    closing?: boolean;
};

// A watched stream that has stopped appearing in the snapshot keeps its place
// for a while instead of vanishing. Deleting the tile immediately reflows the
// grid and resizes every other POV, which is a heavy punishment for an OBS
// reconnect that will be over in ten seconds.
//
// The switch is still on the picture, so it can be turned off while it waits.
// There is no sound to take and nothing to make full screen; the one other
// thing there is the scissors, for the end of the stream while the server
// holds it (spec 3.4), in a row of their own, where they sit on a live tile:
// the slots of what follows them there are kept empty, so they do not jump
// sideways under the pointer the moment the stream goes. The tile gives up
// waiting long before the end is let go; after that it is clipped from Just
// ended, in the Clips dialog.
export const WaitingTile: React.FC<WaitingProps> = ({handle, title, seen, onClose, closing}) => {
    // Named as the tile names them, or, for a stream this panel never saw,
    // as the server named it when it stopped.
    const tailName = useSnapshot().clipTails?.find((held) => held.handle === handle)?.title;
    const name = seen?.title || tailName || title;
    const {view: scissors, lengthSeconds, tail} = useTailScissors(handle, name);
    const scissorsRef = React.useRef<HTMLButtonElement | null>(null);
    const press = () => pressScissors(scissors, {handle, title: name}, lengthSeconds, scissorsRef.current, tail?.reason);

    // The live tile's Pop out kept its place, so the scissors stay put when
    // the stream drops and this tile takes its place.
    const me = useIdentity();
    const access = useAccess();
    const poppable = access === 'linked' && Boolean(me?.popOut) && Boolean(seen) && !seen?.alwaysOn;

    // The scissors go with the end of the stream — let go after its ten
    // minutes, wiped, or Clips switched off — and focus on them stays on the
    // tile rather than falling to the page.
    const rootRef = React.useRef<HTMLDivElement | null>(null);
    const keepFocus = useFocusHome(() => rootRef.current);

    return (
        <div
            className='vntv-player vntv-player-waiting'
            ref={rootRef}
            tabIndex={-1}
            onKeyDown={(event) => onTileKey(event, scissors, press)}
            onFocus={keepFocus.onFocus}
            onBlur={keepFocus.onBlur}
        >
            <Screen power={closing ? 'off' : undefined}>
                <Plate
                    title='No Signal'
                    note={`Waiting for ${title}`}
                />
                <Hud
                    powerLabel={`Switch off ${title}`}
                    onPower={onClose}
                    off={closing}
                >
                    {scissors ? (
                        <>
                            <span className='vntv-hud-spacer'/>
                            <ScissorsButton
                                view={scissors}
                                buttonRef={scissorsRef}
                                onPress={press}
                            />
                            {poppable ? (
                                <span
                                    className='vntv-hud-slot'
                                    aria-hidden={true}
                                />
                            ) : null}
                            <span
                                className='vntv-hud-slot'
                                aria-hidden={true}
                            />
                        </>
                    ) : null}
                </Hud>
            </Screen>
            <TileInfo
                userId={seen?.userId}
                picture={seen ? streamPictureURL(seen) : undefined}
                name={title}
                note={seen?.motd}
                nowPlaying={seen && !seen.alwaysOn ? seen.nowPlaying : undefined}
            />
        </div>
    );
};
