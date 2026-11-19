import React from 'react';

import {clipThumbURL, clipVideoURL} from '../client';
import {CLIP_CANNOT_PLAY, CLIP_NOT_LOADED, CLIP_NO_SOUND, DOWNLOAD, DOWNLOAD_PREPARING, playability} from '../clipLogic';
import {
    atEnd,
    clipAt,
    clipClock,
    clipLength,
    clockWords,
    fileSecondsOf,
    fileWait,
    heardAt,
    loadedShare,
    playerControls,
    playerKey,
    playerState,
    ranPastEnd,
    scrubberKey,
    secondsAt,
    seekTarget,
    shareOf,
} from '../clipLook';
import {shownTitleOf} from '../clips';
import {useFocusHome} from '../focusHome';
import {composing} from '../keys';
import {getSessionInfo, takeSound, UNMUTE_VOLUME} from '../sessions';
import {isTwitchHandle} from '../twitch';
import type {Clip} from '../types';
import {getAudio, getWatching, setAudio, useAudio} from '../watching';

import {Hud, HudButton, TileInfo} from './Hud';
import {
    DownloadIcon,
    ExitFullscreenIcon,
    FullscreenIcon,
    PauseIcon,
    PlayIcon,
    ReplayIcon,
    SpeakerIcon,
    SpeakerMutedIcon,
} from './icons';
import {Plate, Screen, Tuning} from './NoSignal';

// A clip, played in the stream tile's own frame (Victor, 2026-10-13: "Have
// the clip viewer player be the same aesthetic as the main video stream
// player"; he chose look A, "Tile", design/3.3.0.md). The Clips dialog plays
// clips in it, and so does the System Console's Clips page, from its own
// routes.
//
// It is the tile exactly. The picture sits in the tile's 16:9 screen, with
// whose stream it was on the tile's line under it: the streamer only, since
// the clip's title is the viewer's heading just under the tile. The controls
// are the tile's, white icons on its shade and red when pointed at, and come
// and go over the picture as a stream's do: play, the sound and its volume,
// Download when the reader may, and full screen. They stay up while the clip
// is paused. A slim red line along the picture's bottom edge shows how far it
// has got, and grows into a scrubber while the controls are up. The time is
// at the end of the line under the picture. A click on the picture plays or
// pauses. At rest there is only the picture, the name and the time.
//
// The timeline runs from the clip's own start to its own end (clipLook.ts):
// the lead-in before its start is skipped and off the line, and it stops at
// its own end with Replay rather than running on or jumping back.
//
// Red is where the clip is: the scrubber's line and handle. The volume's
// handle, red on a stream (which has no timeline), is white here.
//
// The sound. Clips play with their sound, and only one thing in the page is
// ever audible: pressing play with the sound on takes it from the stream that
// had it, and pausing, going back or closing gives it back, to that stream
// only if it is still playing, and through takeSound, which has to happen in
// the press that caused it, since unmuting needs one. Somebody taking the
// sound back on a stream meanwhile pauses the clip, and it is theirs.
//
// A file that will not load. The browser says only that the <video> failed,
// the same for a 403, a 410, a 503 and a dropped connection, none of which is
// the browser's fault. So whether this browser can play the clip at all is
// judged from canPlayType alone, before anything is fetched, and a failure to
// load is asked about (explain), said on the tile's status line as a stream's
// failure is, and offered again. It clears as soon as the file loads after
// all.
//
// Download (design/3.2.1.md 1.4) is one of the player's controls, offered
// only when the viewer passes it, which it does only when the server says
// this reader may download the clip. What it says goes on the tile's status
// line too. It stays when the file will not play, the one control left on
// the picture, since that is when playing it somewhere else is the way out.
// There is no other Download: the picture's own menu has no Save video as,
// and the System Console passes none.
//
// The keys, from anywhere in the player (clipLook.ts playerKey): Space or K
// plays and pauses, the arrows go back or on five seconds (J and L ten), M
// mutes and F is full screen. Space on a button presses that button, and the
// arrows on a slider move that slider.

// How long something has to be loading before the player says so: a file
// that arrives at once never flickers a loading screen over its picture.
const SLOW_MS = 300;

// What a file that would not load is called when nothing better is known.
const FAILED_TO_LOAD = CLIP_NOT_LOADED;

type Explain = (mediaError: number) => Promise<string>;

// Download, as the viewer offers it: 3.2.1's ticket and then the browser's
// own save (ClipViewer.tsx), pressed from the player.
export type PlayerDownload = {
    // Asking for the ticket: "Preparing…".
    preparing: boolean;

    // Something else in the viewer is busy (a report, a delete).
    disabled: boolean;
    press: () => void;
};

// What Download last said, for the status line under the picture: that it
// started, or a refusal in plain words.
export type PlayerSays = {text: string; error: boolean};

type ClipMedia = {
    video: React.MutableRefObject<HTMLVideoElement | null>;
    frame: React.MutableRefObject<HTMLDivElement | null>;
    play: 'ok' | 'no-video' | 'no-sound';
    failure: string;
    paused: boolean;
    ended: boolean;

    // Loading for long enough to say so: the file before its first frame
    // ('first'), or stalled while it plays ('stalled').
    loading: '' | 'first' | 'stalled';

    // Where the clip is and how long it is, in seconds of the clip itself;
    // loaded is how much of it has come, as a share.
    at: number;
    length: number;
    loaded: number;
    volume: number;
    muted: boolean;
    fullscreen: boolean;
    scrubbing: boolean;
    toggle: () => void;

    // Where a seek landed, in seconds of the clip; null when it could not
    // (no file, or a length not known yet).
    seekTo: (seconds: number) => number | null;
    seekBy: (seconds: number) => number | null;
    setVolume: (volume: number) => void;
    toggleMute: () => void;
    toggleFullscreen: () => void;
    scrubStart: () => void;
    scrubEnd: () => void;
    retry: (event: React.MouseEvent<HTMLButtonElement>) => void;
    onKey: (event: React.KeyboardEvent<HTMLElement>) => void;
    events: React.VideoHTMLAttributes<HTMLVideoElement>;
};

function useClipMedia(clip: Clip, explain?: Explain): ClipMedia {
    const video = React.useRef<HTMLVideoElement | null>(null);
    const frame = React.useRef<HTMLDivElement | null>(null);
    const lead = (clip.leadMs || 0) / 1000;

    // ---- The sound ----

    // The stream that had the sound before this took it, or undefined while
    // this does not have it. null is "nothing had it".
    const held = React.useRef<string | null | undefined>(undefined);
    const audio = useAudio();

    const take = () => {
        if (held.current !== undefined) {
            return;
        }

        held.current = getAudio();
        setAudio(null);
    };

    // A Twitch stream that had it gets it back while it is still watched,
    // knowingly: it has no session to be playing, and getting the sound back
    // reloads its player a second time (taking it reloaded it muted). If the
    // browser then wants a click for the sound, Twitch's player shows its
    // own button for it (design/twitch-live.md 8, row 36).
    const giveBack = () => {
        const before = held.current;
        held.current = undefined;
        const twitchStill = before !== null && before !== undefined && isTwitchHandle(before) && getWatching().includes(before);
        if (before && (getSessionInfo(before)?.status === 'playing' || twitchStill)) {
            takeSound(before);
        }
    };

    // Somebody took the sound for a stream while the clip had it.
    React.useEffect(() => {
        if (audio !== null && held.current !== undefined) {
            held.current = undefined;
            video.current?.pause();
        }
    }, [audio]);

    // Whether this browser can play it at all, and with its sound.
    const [play, setPlay] = React.useState<'ok' | 'no-video' | 'no-sound'>('ok');
    React.useEffect(() => {
        const element = video.current;
        if (element) {
            setPlay(playability(clip.codecs || '', (type) => element.canPlayType(type)));
        }
    }, [clip.codecs]);

    // ---- A file that would not load ----

    // What to say about it, '' while it has not failed (or has since loaded).
    // asking numbers the failures, so an explanation that comes back after
    // the file loaded, or after a newer failure, is dropped.
    const [failure, setFailure] = React.useState('');
    const asking = React.useRef(0);
    const mounted = React.useRef(true);
    React.useEffect(() => () => {
        mounted.current = false;
    }, []);

    const failed = (event: React.SyntheticEvent<HTMLVideoElement>) => {
        const element = event.currentTarget;
        if (play === 'no-video' || !element.hasAttribute('src')) {
            // Already said, or the file was let go on purpose.
            return;
        }

        // A file that fails while it plays has stopped, but not every
        // browser says so: Firefox fires no pause after a dropped connection,
        // and a source that never loads leaves a pressed play standing in
        // all of them. Paused here, the player hears it (onPause), so it
        // stops saying Pause and the stream gets its sound back now, rather
        // than whenever the clip next plays and pauses.
        if (!element.paused) {
            element.pause();
        }

        const asked = ++asking.current;
        const told = explain ? explain(element.error?.code || 0) : Promise.resolve(FAILED_TO_LOAD);
        told.catch(() => FAILED_TO_LOAD).then((text) => {
            if (mounted.current && asked === asking.current) {
                setFailure(text);
            }
        });
    };

    // ---- Where it is ----

    const [paused, setPaused] = React.useState(true);
    const [metadata, setMetadata] = React.useState(false);
    const [firstFrame, setFirstFrame] = React.useState(false);
    const [stalled, setStalled] = React.useState(false);
    const [at, setAt] = React.useState(0);
    const [fileSeconds, setFileSeconds] = React.useState(0);
    const [loaded, setLoaded] = React.useState(0);
    const [volume, setVolumeState] = React.useState(1);
    const [muted, setMuted] = React.useState(false);
    const [fullscreen, setFullscreen] = React.useState(false);
    const [scrubbing, setScrubbing] = React.useState(false);
    const scrubbingRef = React.useRef(false);
    const resumeAfterScrub = React.useRef(false);

    const length = clipLength(clip.durationMs, fileSeconds);
    const lengthRef = React.useRef(length);
    lengthRef.current = length;

    const ended = atEnd(at, length) && !scrubbing;

    const readAt = (element: HTMLVideoElement) => clipAt(element.currentTime, lead, lengthRef.current);
    const readLoaded = (element: HTMLVideoElement) => {
        const ranges = element.buffered;

        return ranges.length ? loadedShare(ranges.end(ranges.length - 1), lead, lengthRef.current) : 0;
    };

    // Try again: the same file asked for again, in the same element. Focus
    // that was on the button, which goes as it is pressed, goes to the player
    // rather than to the page under the dialog.
    //
    // Paused first, out loud: load() stops a playing element without a
    // pause event, which would leave the player saying Pause, its controls
    // asleep, over a clip that has stopped.
    const retry = (event: React.MouseEvent<HTMLButtonElement>) => {
        const hadFocus = document.activeElement === event.currentTarget;
        const element = video.current;
        asking.current++;
        setFailure('');
        setMetadata(false);
        setFirstFrame(false);
        if (hadFocus) {
            frame.current?.focus({preventScroll: true});
        }
        if (element && !element.paused) {
            element.pause();
        }
        element?.load();
    };

    // Going: the sound goes back, and the file stops downloading. Taking the
    // element out of the page does not stop a download in every browser, so
    // it is emptied first. A layout effect, so it happens in the same press
    // that closed the dialog or went back, which is what takeSound needs.
    React.useLayoutEffect(() => {
        const element = video.current;

        return () => {
            giveBack();
            if (element) {
                element.pause();
                element.removeAttribute('src');
                element.load();
            }
        };
    }, []);

    // While it plays, every frame, so the line moves smoothly; and it stops
    // itself at the clip's own end, which can come before the file's.
    React.useEffect(() => {
        if (paused) {
            return undefined;
        }

        let frameId = 0;
        const tick = () => {
            const element = video.current;
            if (element) {
                const now = clipAt(element.currentTime, lead, 0);
                const end = lengthRef.current;
                if (ranPastEnd(now, end) && !scrubbingRef.current) {
                    element.pause();
                    setAt(end);

                    return;
                }
                setAt(clipAt(element.currentTime, lead, end));
                setLoaded(readLoaded(element));
            }
            frameId = window.requestAnimationFrame(tick);
        };
        frameId = window.requestAnimationFrame(tick);

        return () => window.cancelAnimationFrame(frameId);
    }, [paused, lead]);

    // Loading, said once it has gone on for a moment.
    const waiting = fileWait({failed: failure !== '', cannotPlay: play === 'no-video', paused, metadata, firstFrame, stalled});
    const waitingNow = waiting !== '';
    const [slow, setSlow] = React.useState(false);
    React.useEffect(() => {
        if (!waitingNow) {
            setSlow(false);

            return undefined;
        }

        const timer = window.setTimeout(() => setSlow(true), SLOW_MS);

        return () => window.clearTimeout(timer);
    }, [waitingNow]);
    const loading = slow ? waiting : '';

    React.useEffect(() => {
        const sync = () => setFullscreen(document.fullscreenElement !== null && document.fullscreenElement === frame.current);
        document.addEventListener('fullscreenchange', sync);

        return () => document.removeEventListener('fullscreenchange', sync);
    }, []);

    // ---- What the controls do ----

    // Nowhere before the file has said how long it is: its arrival puts it
    // back at the clip's start (onLoadedMetadata), undoing any seek before.
    const seekTo = (seconds: number) => {
        const element = video.current;
        if (!element || lengthRef.current <= 0 || element.readyState < HTMLMediaElement.HAVE_METADATA) {
            return null;
        }

        const target = seekTarget(seconds, lengthRef.current);
        element.currentTime = lead + target;
        setAt(target);

        return target;
    };

    const seekBy = (seconds: number) => {
        const element = video.current;

        return element ? seekTo(readAt(element) + seconds) : null;
    };

    const toggle = () => {
        const element = video.current;
        if (!element || play === 'no-video' || failure !== '') {
            return;
        }

        if (ended) {
            // Replay: from the clip's own start.
            element.currentTime = lead;
            setAt(0);
            void element.play().catch(() => undefined);

            return;
        }

        if (element.paused) {
            void element.play().catch(() => undefined);
        } else {
            element.pause();
        }
    };

    const setVolume = (next: number) => {
        const element = video.current;
        if (!element) {
            return;
        }

        element.volume = Math.min(1, Math.max(0, next));
        element.muted = next <= 0;
    };

    // Unmuting a clip turned all the way down brings it up to where a stream
    // unmutes to.
    const toggleMute = () => {
        const element = video.current;
        if (!element) {
            return;
        }

        if (element.muted || element.volume === 0) {
            if (element.volume === 0) {
                element.volume = UNMUTE_VOLUME;
            }
            element.muted = false;
        } else {
            element.muted = true;
        }
    };

    // The whole tile, so the line under the picture and the controls go with
    // it, as a stream's do.
    const toggleFullscreen = () => {
        if (document.fullscreenElement) {
            void document.exitFullscreen().catch(() => undefined);

            return;
        }

        void frame.current?.requestFullscreen?.().catch(() => undefined);
    };

    // A drag on the scrubber holds the clip where the pointer is, and plays
    // on from there when it lets go if it was playing. Its pause and play are
    // not a pause and a play to the sound: it keeps it throughout.
    const scrubStart = () => {
        const element = video.current;
        if (!element) {
            return;
        }

        scrubbingRef.current = true;
        setScrubbing(true);
        resumeAfterScrub.current = !element.paused;
        element.pause();
    };

    const scrubEnd = () => {
        const element = video.current;
        scrubbingRef.current = false;
        setScrubbing(false);
        if (!element) {
            return;
        }

        if (resumeAfterScrub.current && !atEnd(readAt(element), lengthRef.current)) {
            void element.play().catch(() => undefined);
        } else if (resumeAfterScrub.current) {
            // Let go at the end: it stays there, and the sound goes back.
            giveBack();
        }
        resumeAfterScrub.current = false;
    };

    const onKey = (event: React.KeyboardEvent<HTMLElement>) => {
        const target = event.target as HTMLElement;
        const action = playerKey(event.key, {
            modified: event.ctrlKey || event.metaKey || event.altKey || composing(event),
            onButton: target !== event.currentTarget && target.closest('button, a') !== null,
            onSlider: target instanceof HTMLInputElement || target.getAttribute('role') === 'slider',
        });
        if (!action) {
            return;
        }

        event.preventDefault();
        event.stopPropagation();
        switch (action.act) {
        case 'toggle':
            toggle();
            break;
        case 'seek':
            seekBy(action.by);
            break;
        case 'mute':
            toggleMute();
            break;
        case 'fullscreen':
            toggleFullscreen();
            break;
        }
    };

    const events: React.VideoHTMLAttributes<HTMLVideoElement> = {
        onLoadedMetadata: (event) => {
            // Files begin at the keyframe before the clip's own start; the
            // lead-in is skipped, and is not on the timeline.
            const element = event.currentTarget;
            if (lead > 0) {
                element.currentTime = lead;
            }
            setFileSeconds(fileSecondsOf(element.duration, lead));
            setMetadata(true);
            setAt(0);
        },
        onDurationChange: (event) => setFileSeconds(fileSecondsOf(event.currentTarget.duration, lead)),
        onLoadedData: () => {
            asking.current++;
            setFailure('');
            setFirstFrame(true);
        },
        onPlay: (event) => {
            setPaused(false);

            // A browser that cannot play the clip's sound plays it silent,
            // and leaves the stream's sound alone.
            if (!event.currentTarget.muted && event.currentTarget.volume > 0 && play !== 'no-sound') {
                take();
            }
        },
        onPlaying: () => {
            setStalled(false);
            setFirstFrame(true);
        },
        onWaiting: () => setStalled(true),
        onCanPlay: () => setStalled(false),
        onPause: (event) => {
            setPaused(true);
            setAt(readAt(event.currentTarget));
            if (!scrubbingRef.current) {
                giveBack();
            }
        },
        onSeeked: (event) => setAt(readAt(event.currentTarget)),
        onTimeUpdate: (event) => {
            if (event.currentTarget.paused) {
                setAt(readAt(event.currentTarget));
            }
        },
        onProgress: (event) => setLoaded(readLoaded(event.currentTarget)),
        onVolumeChange: (event) => {
            const element = event.currentTarget;
            setVolumeState(element.volume);
            setMuted(element.muted);
            if (element.paused || play === 'no-sound') {
                return;
            }
            if (element.muted || element.volume === 0) {
                giveBack();
            } else {
                take();
            }
        },
        onEnded: (event) => {
            setPaused(true);
            setAt(readAt(event.currentTarget));
        },
        onError: failed,
    };

    return {
        video,
        frame,
        play,
        failure,
        paused,
        ended,
        loading,
        at,
        length,
        loaded,
        volume,
        muted,
        fullscreen,
        scrubbing,
        toggle,
        seekTo,
        seekBy,
        setVolume,
        toggleMute,
        toggleFullscreen,
        scrubStart,
        scrubEnd,
        retry,
        onKey,
        events,
    };
}

// ---- The pieces ----

// Where the clip is, along the picture's bottom edge: a slim red line on a
// dark track, there always, which lifts into a scrubber with a handle while
// the controls are up. A WAI-ARIA slider (clipLook.ts scrubberKey). A press
// anywhere on it goes there, and a drag scrubs; the pointer is captured for
// the drag, and touch-action none keeps a drag on a phone from scrolling the
// dialog. While it is dragged the time on the line under the picture moves
// with it.
//
// While it has focus and the clip plays, what a screen reader hears of it
// holds still (clipLook.ts heardAt), and moves only when the reader moves it
// or the clip stops.
//
// Over the loading screen it is not drawn (the stylesheet), and it is out of
// play there too: no Tab stop and no clicks, where a seek before the file has
// come would only be undone when it arrives. Focus that was on it goes to the
// player, where the keys still work, rather than to the page.
const Scrubber: React.FC<{media: ClipMedia}> = ({media}) => {
    const slider = React.useRef<HTMLDivElement | null>(null);
    const track = React.useRef<HTMLDivElement | null>(null);
    const dragging = React.useRef<number | null>(null);

    const [held, setHeld] = React.useState<number | null>(null);
    const live = media.paused || media.scrubbing;
    React.useEffect(() => {
        if (live && held !== null && held !== media.at) {
            setHeld(media.at);
        }
    }, [live, held, media.at]);
    const heard = heardAt(media.at, held, live);

    const asleep = media.loading === 'first';
    React.useLayoutEffect(() => {
        if (asleep && document.activeElement === slider.current) {
            media.frame.current?.focus({preventScroll: true});
        }
    }, [asleep]);

    const pointedAt = (clientX: number) => {
        const box = track.current?.getBoundingClientRect();

        return box ? secondsAt(clientX, box.left, box.width, media.length) : 0;
    };

    const finish = () => {
        if (dragging.current !== null) {
            dragging.current = null;
            media.scrubEnd();
        }
    };

    const style = {
        '--vntv-clip-at': `${shareOf(media.at, media.length) * 100}%`,
        '--vntv-clip-loaded': `${media.loaded * 100}%`,
    } as React.CSSProperties;

    return (
        <div
            ref={slider}
            className='vntv-clip-scrub vntv-hud-control'
            role='slider'
            tabIndex={asleep ? -1 : 0}
            aria-label='Where the clip is'
            aria-orientation='horizontal'
            aria-valuemin={0}
            aria-valuemax={Math.round(media.length)}
            aria-valuenow={Math.round(heard)}
            aria-valuetext={clockWords(heard, media.length)}
            data-scrubbing={media.scrubbing ? 'true' : undefined}
            style={style}
            onFocus={() => setHeld(media.at)}
            onBlur={() => setHeld(null)}
            onPointerDown={(event) => {
                if (event.button !== 0 || media.length <= 0 || asleep) {
                    return;
                }

                event.preventDefault();
                event.currentTarget.focus({preventScroll: true});
                dragging.current = event.pointerId;
                try {
                    event.currentTarget.setPointerCapture(event.pointerId);
                } catch {
                    // A pointer the browser no longer knows: the drag still
                    // follows it while it stays over the scrubber.
                }
                media.scrubStart();
                media.seekTo(pointedAt(event.clientX));
            }}
            onPointerMove={(event) => {
                if (dragging.current === event.pointerId) {
                    media.seekTo(pointedAt(event.clientX));
                }
            }}
            onPointerUp={finish}
            onPointerCancel={finish}
            onLostPointerCapture={finish}
            onKeyDown={(event) => {
                const action = scrubberKey(event.key, event.ctrlKey || event.metaKey || event.altKey);
                if (!action) {
                    return;
                }

                event.preventDefault();
                event.stopPropagation();
                const landed = 'by' in action ? media.seekBy(action.by) : media.seekTo(action.to === 'start' ? 0 : media.length);

                // Moved by the reader: said at once, even while it plays.
                if (landed !== null) {
                    setHeld(landed);
                }
            }}
        >
            <div
                ref={track}
                className='vntv-clip-scrub-track'
            >
                <span className='vntv-clip-scrub-loaded'/>
                <span className='vntv-clip-scrub-fill'/>
                <span className='vntv-clip-scrub-handle'/>
            </div>
        </div>
    );
};

// The time, "0:12 / 0:45", at the end of the line under the picture. Heard
// through the scrubber's value instead, so not twice.
const Time: React.FC<{media: ClipMedia}> = ({media}) => {
    const clock = clipClock(media.at, media.length);

    return (
        <span
            className='vntv-clip-time'
            aria-hidden={true}
        >
            {clock.at}
            <span className='vntv-clip-time-total'>{` / ${clock.length}`}</span>
        </span>
    );
};

const PlayButton: React.FC<{media: ClipMedia}> = ({media}) => {
    let label = 'Pause';
    let icon = <PauseIcon size={16}/>;
    if (media.ended) {
        label = 'Replay';
        icon = <ReplayIcon size={16}/>;
    } else if (media.paused) {
        label = 'Play';
        icon = <PlayIcon size={16}/>;
    }

    return (
        <HudButton
            label={label}
            keyShortcuts='K'
            onClick={media.toggle}
        >{icon}</HudButton>
    );
};

const MuteButton: React.FC<{media: ClipMedia}> = ({media}) => {
    const silent = media.muted || media.volume === 0;

    return (
        <HudButton
            label={silent ? 'Unmute' : 'Mute'}
            keyShortcuts='M'
            onClick={media.toggleMute}
        >{silent ? <SpeakerMutedIcon size={16}/> : <SpeakerIcon size={16}/>}</HudButton>
    );
};

const Volume: React.FC<{media: ClipMedia}> = ({media}) => {
    const value = media.muted ? 0 : Math.round(media.volume * 100);

    return (
        <input
            type='range'
            className='vntv-volume'
            min={0}
            max={100}
            step={1}
            value={value}
            aria-label='Volume'
            title={value === 0 ? 'Muted' : `Volume ${value}%`}
            onChange={(event) => media.setVolume(Number(event.target.value) / 100)}
        />
    );
};

const FullscreenButton: React.FC<{media: ClipMedia}> = ({media}) => (
    <HudButton
        label={media.fullscreen ? 'Exit full screen' : 'Full screen'}
        keyShortcuts='F'
        onClick={media.toggleFullscreen}
    >{media.fullscreen ? <ExitFullscreenIcon size={16}/> : <FullscreenIcon size={16}/>}</HudButton>
);

// Download. It pulses while the ticket is asked for, as the scissors do
// while they cut, and the line under the picture says how it went.
const DownloadButton: React.FC<{download: PlayerDownload}> = ({download}) => (
    <HudButton
        label={download.preparing ? DOWNLOAD_PREPARING : DOWNLOAD}
        title={download.preparing ? DOWNLOAD_PREPARING : 'Download the clip'}
        ariaDisabled={download.disabled}
        busy={download.preparing}
        onClick={download.press}
    ><DownloadIcon size={16}/></HudButton>
);

// Waiting for more of the file while it plays: a ring turning in the plugin
// red, in the glass of the stream tile's switch, in the middle.
const Stalled: React.FC = () => (
    <span
        className='vntv-clip-wait'
        role='status'
        aria-label='Loading'
    >
        <span className='vntv-clip-ring'/>
    </span>
);

type Props = {
    clip: Clip;

    // The file and its picture, when not the panel's own routes: the System
    // Console plays clips from its own, which any system admin may use
    // whether or not they have linked a stream. There picture-in-picture is
    // switched off as well, so a clip under review cannot float off out of
    // the page.
    src?: string;
    poster?: string;
    noPictureInPicture?: boolean;

    // Why the file would not load, asked once per failure with the media
    // error's code: resolves to what to say beside Try again. The panel's
    // viewer asks the server about the clip, which also locks the dialog
    // when the reader is no longer linked and says when the clip has gone.
    // Without it, the failure is said plainly.
    explain?: Explain;

    // Download, when the reader may download the clip, and what it last
    // said. Only the Clips dialog's viewer passes them.
    download?: PlayerDownload;
    says?: PlayerSays | null;
};

export const ClipTile: React.FC<Props> = ({clip, src, poster: posterGiven, noPictureInPicture, explain, download, says}) => {
    const media = useClipMedia(clip, explain);
    const shown = shownTitleOf(clip);

    // A control that goes with focus on it (Download, once a switch turns
    // downloads off) leaves focus with the player, where its keys still
    // work, rather than on the page's body.
    const keepFocus = useFocusHome(() => media.frame.current);

    let poster = posterGiven;
    if (poster === undefined) {
        poster = clip.thumb ? clipThumbURL(clip.id, clip.thumb) : '';
    }

    const failed = media.failure !== '' && media.play !== 'no-video';
    const state = playerState({failed, loading: media.loading !== '', ended: media.ended, paused: media.paused});

    // Once the file has failed only Download is left on the picture, coming
    // up as the controls do, when pointed at or tabbed to; at rest the tile
    // shows the static and CAN'T PLAY, as before.
    const controls = playerControls({failed, downloadOffered: download !== undefined});

    return (
        <>
            <div
                ref={media.frame}
                className='vntv-player vntv-cliptile'
                data-state={state}
                data-loading={media.loading || undefined}
                tabIndex={-1}
                role='group'
                aria-label={`The clip: ${shown}. Space or K plays and pauses, the arrows go back and on, M mutes and F is full screen.`}
                onKeyDown={media.onKey}
                onFocus={keepFocus.onFocus}
                onBlur={keepFocus.onBlur}
            >
                <Screen noise={failed}>
                    <video
                        ref={media.video}
                        src={src ?? clipVideoURL(clip.id)}
                        poster={poster || undefined}
                        disablePictureInPicture={noPictureInPicture || undefined}
                        playsInline={true}
                        preload='metadata'
                        aria-hidden={true}

                        // No Save video as on the picture's own menu:
                        // Download, through its ticket, is the one way to
                        // download a clip (design/3.2.1.md 1.6). A nudge, not
                        // a lock: whatever a browser can play, it can save.
                        onContextMenu={(event) => event.preventDefault()}
                        {...media.events}
                    />
                    {media.loading === 'first' ? (
                        <Tuning
                            title={shown}
                            word='Loading'
                        />
                    ) : null}
                    {media.loading === 'stalled' ? <Stalled/> : null}
                    {failed ? <Plate title='Can’t play'/> : null}
                    {controls.play || controls.download ? (
                        <Hud
                            keepUp={controls.play && media.paused && media.loading === ''}
                            onSurface={controls.play ? media.toggle : undefined}
                            edge={controls.play ? <Scrubber media={media}/> : undefined}
                        >
                            {controls.play ? <PlayButton media={media}/> : null}
                            {controls.play ? <MuteButton media={media}/> : null}
                            {controls.play ? <Volume media={media}/> : null}
                            <span className='vntv-hud-spacer'/>
                            {download && controls.download ? <DownloadButton download={download}/> : null}
                            {controls.play ? <FullscreenButton media={media}/> : null}
                        </Hud>
                    ) : null}
                </Screen>

                <TileInfo
                    userId={clip.streamer.userId}
                    name={clip.streamer.name}
                    end={<Time media={media}/>}
                />

                {/* Why it will not play, on the tile's own status line, as a
                    stream's failure is. */}
                {failed ? (
                    <div
                        className='vntv-player-status vntv-error'
                        role='alert'
                    >
                        {media.failure}
                        <button
                            type='button'
                            className='vntv-pill'
                            style={{marginLeft: 8}}
                            onClick={media.retry}
                        >{'Try again'}</button>
                    </div>
                ) : null}

                {/* What Download said, there all along so what is put in it
                    is announced. */}
                <div
                    className='vntv-clip-said'
                    role='status'
                >
                    {download?.preparing || says ? (
                        <p className={says?.error && !download?.preparing ? 'vntv-player-status vntv-error' : 'vntv-player-status'}>
                            {download?.preparing ? DOWNLOAD_PREPARING : says?.text}
                        </p>
                    ) : null}
                </div>
            </div>
            {media.play !== 'ok' && (
                <p
                    className='vntv-clipview-note'
                    role='status'
                >{media.play === 'no-video' ? CLIP_CANNOT_PLAY : CLIP_NO_SOUND}</p>
            )}
        </>
    );
};
