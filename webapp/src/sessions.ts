import React from 'react';

import {getSnapshot} from './store';
import {offerFrames, withdrawFrames} from './stills';
import {getAudio, getWatching, MAX_WATCHING, onWatchingChange, setAudio} from './watching';
import {WhepPlayer} from './whep';

// The media session pool: one WebRTC session and one <video> element for each
// watched stream, owned here rather than by whichever component is showing it.
//
// Before this, a tile's player owned its connection, so anything that
// unmounted the panel hung up every stream in it — and Mattermost unmounts the
// panel every time a thread or a search takes its place in the side panel.
// Coming back meant a fresh WHEP exchange per stream, counted against the
// playback rate limit, and a fresh Broadcast Box session asking the streamer
// for a keyframe, with the tuning burst played over all of it.
//
// So a component no longer has a <video>. It renders an empty slot and lends
// it to the pool, which MOVES the stream's one element into the slot mounted
// most recently. Going back into the panel re-attaches the same element, still
// playing the same MediaStream, without reconnecting anything.
//
// One element per stream also keeps the sound simple: nothing is ever handed
// from one element to another, and two elements can never play the same audio.
//
// Every session is bounded. A stream nobody is showing, and nobody is holding
// on to, is let go a minute after its last slot unmounts; a stream that leaves
// the watch list, or the snapshot, is let go at once.

// A session with no slot and no hold is stopped this long after its last slot
// unmounted. Long enough for the panel to come back from a thread or a search
// without reconnecting; short enough that a stream nobody can see stops costing
// the streamer a viewer.
export const GRACE_MS = 60000;

// How long a hold keeps a session that nothing is showing. Only ever the one
// stream the small player shows while something else has the side panel.
export const HOLD_MS = 240000;

// Negotiation can succeed and no picture ever arrive — a paused publisher is
// still listed, and still accepts a WHEP exchange. Without this the tile sits
// on "Tuning" for ever with no way to retry.
const STALL_TIMEOUT_MS = 12000;

// A live stream has no pause control, so a pause can only come from the
// browser reclaiming a decoder. Resuming is right, but resuming forever is a
// livelock: with several tiles up, each resume re-applies the pressure that
// caused the pause.
const RESUME_BACKOFF_MS = [250, 1000, 4000];

// A resume budget spent over a whole session is not a budget, it is a countdown
// to a dead tile. It resets once playback has been stable for this long.
const RESUME_BUDGET_WINDOW_MS = 60000;

// Taking an element out of the page pauses it, but not at once: the browser
// waits for the current task to finish and then queues the pause event as a
// task of its own (the HTML standard's "await a stable state" on removal). By
// then the element can already be in its next slot, playing, so a pause this
// soon after a detach is the detach's own and must not be fought as if the
// browser had reclaimed the decoder — that would spend the resume budget on
// every trip out of the panel and back.
const DETACH_PAUSE_MS = 1000;

// Volume to jump to when taking the sound from silence, so the button always
// does something audible.
export const UNMUTE_VOLUME = 0.6;

export type SessionStatus = 'connecting' | 'playing' | 'failed';

// What a component may know about a session. A new object each time anything
// in it changes, and the same object otherwise, so it can be compared by
// identity to decide whether to render.
export type SessionInfo = Readonly<{
    status: SessionStatus;
    error: string;
    volume: number;

    // The browser refused to play this stream with its sound on. Whether a
    // tile has the sound is the watch list's intent (getAudio) AND not this:
    // a tile never claims a sound that is not coming out of it.
    blocked: boolean;
    version: number;
}>;

type Session = {
    handle: string;
    video: HTMLVideoElement;
    player: WhepPlayer;
    status: SessionStatus;
    error: string;
    volume: number;
    blocked: boolean;

    // The tile is switching off: silent for the length of its animation,
    // before the watch list lets it go.
    quiet: boolean;
    slot: HTMLElement | null;

    // Out of every slot since it last played, and when it was taken out; see
    // DETACH_PAUSE_MS.
    detached: boolean;
    detachedAt: number;

    // Hold id -> the timer that releases it.
    holds: Map<number, number>;
    grace: number;
    stall: number;
    resumes: number;
    lastPause: number;
    resumeTimer: number;
    version: number;

    // Which negotiation is the current one. A negotiation that has since been
    // replaced (by Retry) still settles, usually as an aborted request, and
    // its failure is not this session's.
    attempt: number;

    // A play() has been asked for and has not settled yet; see playSafely.
    playPending: boolean;
    stopped: boolean;
    info: SessionInfo;
    unlisten: () => void;
};

const sessions = new Map<string, Session>();
const infoListeners = new Set<() => void>();

let nextHold = 0;

// The last session to stop, and why, for the lab's counters: the one thing a
// grace or a sweep leaves behind that can be looked at afterwards.
let lastStop: {handle: string; reason: string; at: number} | null = null;

function notify(): void {
    Array.from(infoListeners).forEach((listener) => listener());
}

function changed(s: Session): void {
    s.version++;
    s.info = Object.freeze({
        status: s.status,
        error: s.error,
        volume: s.volume,
        blocked: s.blocked,
        version: s.version,
    });
    notify();
}

function setStatus(s: Session, status: SessionStatus, error: string): void {
    if (s.status === status && s.error === error) {
        return;
    }

    s.status = status;
    s.error = error;
    changed(s);
}

// ---- Playing, and the sound ----

// play(), and if the browser refuses because the element has its sound on,
// play it silently instead and say so. The refusal is remembered (blocked)
// until somebody clicks, because nothing short of a click will change the
// browser's mind — and trying again on every resize event a WebRTC receiver
// fires would turn one refusal into a stream that keeps stopping.
//
// Never a second play() while one is still settling: the second would be
// refused for the same reason as the first, and the two refusals would race to
// say so.
function playSafely(s: Session): void {
    const video = s.video;
    if (s.playPending) {
        return;
    }

    let started: Promise<void> | undefined;
    try {
        started = video.play();
    } catch {
        return;
    }
    if (!started) {
        return;
    }

    s.playPending = true;
    started.then(() => {
        s.playPending = false;
    }, (reason: unknown) => {
        s.playPending = false;
        if (s.stopped || (reason as {name?: string} | null)?.name !== 'NotAllowedError') {
            // Interrupted by the next change of source, or by being taken out
            // of the page: whatever comes next plays it.
            return;
        }

        if (!video.muted) {
            video.muted = true;
            if (getAudio() === s.handle && !s.blocked) {
                s.blocked = true;
                changed(s);
            }
        }

        if (video.isConnected) {
            video.play().catch(() => {
                // Refused even silently: there is nothing left to try.
            });
        }
    });
}

// Plays an element the browser has paused, if it is in the page and has a
// stream to play. Taking an element out of the page pauses it and takes away
// its permission to autoplay, so putting it back, or pictures arriving after
// it was put back, has to start it again by hand.
function resume(s: Session): void {
    const video = s.video;
    if (!s.stopped && s.status !== 'failed' && video.isConnected && video.paused && video.srcObject) {
        playSafely(s);
    }
}

// Puts this element's sound where the watch list says it belongs. Exactly one
// stream is audible: the one getAudio() names, unless it is switching off or
// the browser refused it.
function applyAudio(s: Session): void {
    if (s.stopped) {
        return;
    }

    const video = s.video;
    if (getAudio() !== s.handle) {
        video.muted = true;

        // The sound has moved on, so the refusal no longer describes anything.
        if (s.blocked) {
            s.blocked = false;
            changed(s);
        }

        return;
    }

    if (s.quiet || s.blocked) {
        video.muted = true;

        return;
    }

    if (s.volume === 0) {
        s.volume = UNMUTE_VOLUME;
        changed(s);
    }
    video.volume = s.volume;
    video.muted = false;
    resume(s);
}

// Every session's sound, the ones going quiet first, so that there is no
// moment in which the stream taking the sound and the one losing it are both
// unmuted.
function applyAudioToAll(): void {
    const all = Array.from(sessions.values());
    all.filter((s) => s.handle !== getAudio()).forEach(applyAudio);
    all.filter((s) => s.handle === getAudio()).forEach(applyAudio);
}

// ---- A session's life ----

function negotiate(s: Session): void {
    const attempt = ++s.attempt;
    window.clearTimeout(s.stall);
    window.clearTimeout(s.resumeTimer);
    s.resumeTimer = 0;
    s.resumes = 0;
    s.lastPause = 0;

    // Browsers only allow autoplay while muted. The sound goes back where it
    // belongs once pictures arrive (onPictures).
    s.video.muted = true;
    s.video.volume = s.volume;
    setStatus(s, 'connecting', '');

    const failed = (reason: string) => {
        if (!s.stopped && s.attempt === attempt) {
            fail(s, reason);
        }
    };

    // "Tuning" ends when pictures arrive, not when the answer is applied.
    // Applying an answer only means the negotiation was accepted; ICE can still
    // fail afterwards, and a tile that quietly shows a black rectangle forever
    // is much harder to spot in a grid of four.
    s.stall = window.setTimeout(() => failed('No video arrived from this stream.'), STALL_TIMEOUT_MS);

    s.player.play(s.handle, s.video, failed).catch((reason: unknown) => {
        failed(reason instanceof Error ? reason.message : String(reason));
    });
}

function onPictures(s: Session): void {
    if (s.stopped) {
        return;
    }

    window.clearTimeout(s.stall);
    s.stall = 0;

    // Only a negotiation in progress becomes "playing". A failure stays on
    // screen, with its Retry, until somebody presses it: a late frame from a
    // connection that has been reported lost is not a recovery.
    if (s.status === 'connecting') {
        setStatus(s, 'playing', '');
    }

    // Pictures are arriving: the element can supply the Live Now card's
    // preview still for free, but only while it is in the page, where the
    // browser is obliged to keep decoding it. See stills.ts.
    if (s.status === 'playing' && s.video.isConnected) {
        offerFrames(s.handle, s.video);
    }

    applyAudio(s);

    // An element that was taken out of the page has lost its permission to
    // autoplay, so pictures arriving after it was put back do not start it.
    resume(s);
}

function onPause(s: Session): void {
    const video = s.video;
    if (s.stopped || !video.isConnected || s.detached || performance.now() - s.detachedAt < DETACH_PAUSE_MS) {
        return;
    }

    // Stable for a while since the last pause? Then this is a fresh problem,
    // not a losing fight, and it gets a fresh budget.
    const now = Date.now();
    if (now - s.lastPause > RESUME_BUDGET_WINDOW_MS) {
        s.resumes = 0;
    }
    s.lastPause = now;

    const attempts = s.resumes;
    if (attempts >= RESUME_BACKOFF_MS.length) {
        fail(s, 'Playback keeps stopping. Retry to reconnect.');

        return;
    }

    s.resumes = attempts + 1;
    window.clearTimeout(s.resumeTimer);
    s.resumeTimer = window.setTimeout(() => {
        s.resumeTimer = 0;
        resume(s);
    }, RESUME_BACKOFF_MS[attempts]);
}

function fail(s: Session, reason: string): void {
    window.clearTimeout(s.stall);
    s.stall = 0;

    // What is left on screen is the last frame from before the failure, not
    // what is on now: no longer a preview anybody should be shown.
    withdrawFrames(s.handle, s.video);
    setStatus(s, 'failed', reason);

    // Nobody is looking at it and nobody is keeping it: a failure there is
    // simply the end of it, and the next slot to ask negotiates afresh.
    if (!s.slot && s.holds.size === 0) {
        stop(s, 'failed');
    }
}

function cancelGrace(s: Session): void {
    window.clearTimeout(s.grace);
    s.grace = 0;
}

function maybeGrace(s: Session): void {
    if (s.stopped || s.slot || s.holds.size > 0 || s.grace) {
        return;
    }

    s.grace = window.setTimeout(() => {
        s.grace = 0;
        stop(s, 'grace');
    }, GRACE_MS);
}

function stop(s: Session, reason: string): void {
    if (s.stopped) {
        return;
    }

    s.stopped = true;
    window.clearTimeout(s.stall);
    window.clearTimeout(s.grace);
    window.clearTimeout(s.resumeTimer);
    s.holds.forEach((timer) => window.clearTimeout(timer));
    s.holds.clear();

    // Listeners off first, so the pause that taking the element out causes is
    // not mistaken for anything.
    s.unlisten();
    s.player.stop();

    // Nothing left pointing at a dead MediaStream.
    s.video.srcObject = null;
    s.video.remove();
    s.slot = null;
    withdrawFrames(s.handle, s.video);

    if (sessions.get(s.handle) === s) {
        sessions.delete(s.handle);
    }

    lastStop = {handle: s.handle, reason, at: Date.now()};
    notify();
}

// The same truth the panel renders a player on: watched, in the snapshot, and
// within the four decoders the panel allows itself. Never a Twitch stream,
// which plays in Twitch's own player and has nothing to ask Broadcast Box
// for: no <video>, no WHEP exchange (design/twitch-live.md 6.7).
function mayCreate(handle: string): boolean {
    return getWatching().includes(handle) &&
        getSnapshot().streams.some((stream) => stream.handle === handle && !stream.source) &&
        sessions.size < MAX_WATCHING;
}

function create(handle: string): Session {
    const video = document.createElement('video');
    video.className = 'vntv-video';

    // No controls attribute: this is live, and a click-to-pause surface
    // covering the whole frame is worse than useless.
    video.autoplay = true;
    video.playsInline = true;
    video.defaultMuted = true;
    video.muted = true;

    const s: Session = {
        handle,
        video,
        player: new WhepPlayer(),
        status: 'connecting',
        error: '',
        volume: UNMUTE_VOLUME,
        blocked: false,
        quiet: false,
        slot: null,
        detached: false,
        detachedAt: -Infinity,
        holds: new Map(),
        grace: 0,
        stall: 0,
        resumes: 0,
        lastPause: 0,
        resumeTimer: 0,
        version: 0,
        attempt: 0,
        playPending: false,
        stopped: false,
        info: Object.freeze({status: 'connecting', error: '', volume: UNMUTE_VOLUME, blocked: false, version: 0}),
        unlisten: () => undefined,
    };

    const pictures = () => onPictures(s);
    const playing = () => {
        s.detached = false;
    };
    const paused = () => onPause(s);

    // loadeddata for the first picture; resize for every one after a change of
    // resolution, which is also what a renegotiated stream looks like.
    video.addEventListener('loadeddata', pictures);
    video.addEventListener('resize', pictures);
    video.addEventListener('playing', playing);
    video.addEventListener('pause', paused);
    s.unlisten = () => {
        video.removeEventListener('loadeddata', pictures);
        video.removeEventListener('resize', pictures);
        video.removeEventListener('playing', playing);
        video.removeEventListener('pause', paused);
    };

    sessions.set(handle, s);
    negotiate(s);

    // Anything already asking about this stream — a tile that found no
    // session a moment ago — can now see one.
    notify();

    return s;
}

function unmount(s: Session, slot: HTMLElement): void {
    // Already moved on to a newer slot, or gone: the order in which slots
    // mount and unmount is therefore irrelevant, which matters because React
    // promises nothing about it across two different parts of the page.
    if (s.stopped || s.slot !== slot) {
        return;
    }

    s.video.remove();
    s.slot = null;
    s.detached = true;
    s.detachedAt = performance.now();
    withdrawFrames(s.handle, s.video);
    maybeGrace(s);
}

// ---- The API ----

// Puts the stream's element in this slot, starting a session first if there is
// none and one may be started. For a layout effect, so the element is in place
// before the browser paints; returns the cleanup.
//
// create: false borrows a session that already exists and never starts one.
export function mountVideo(handle: string, slot: HTMLElement, opts: {create?: boolean} = {}): () => void {
    let found = sessions.get(handle);
    if (!found) {
        if (opts.create === false || !mayCreate(handle)) {
            return () => undefined;
        }
        found = create(handle);
    }

    const s = found;
    cancelGrace(s);

    // appendChild MOVES an element that is somewhere else. Moved within one
    // task it never stops playing; see DETACH_PAUSE_MS for the other case.
    slot.appendChild(s.video);
    s.slot = slot;
    if (!s.video.paused) {
        s.detached = false;
    }

    applyAudio(s);

    // An element that spent any time out of the page was paused by the
    // browser, and has lost its permission to autoplay.
    resume(s);

    if (s.status === 'playing') {
        offerFrames(handle, s.video);
    }

    return () => unmount(s, slot);
}

// Keeps a session that nothing is showing, for up to ms. Releasing it, or its
// running out, does exactly the same thing: the session gets its grace if
// nothing else is keeping it. A no-op when there is no session.
export function holdSession(handle: string, ms: number = HOLD_MS): () => void {
    const s = sessions.get(handle);
    if (!s || s.stopped) {
        return () => undefined;
    }

    const id = ++nextHold;
    const release = () => {
        const timer = s.holds.get(id);
        if (timer === undefined) {
            return;
        }

        window.clearTimeout(timer);
        s.holds.delete(id);
        maybeGrace(s);
    };

    s.holds.set(id, window.setTimeout(release, ms));
    cancelGrace(s);

    return release;
}

// Stops every session whose stream is not in the snapshot. Its tile becomes a
// waiting tile, as it always has, and the next player to mount for it
// negotiates afresh — with its own stall watchdog — when it comes back.
export function sweepMissing(liveHandles: string[]): void {
    const live = new Set(liveHandles);
    Array.from(sessions.values()).forEach((s) => {
        if (!live.has(s.handle)) {
            stop(s, 'missing');
        }
    });
}

// Retry: the same element, a new negotiation. The generation guard in
// WhepPlayer still keeps a stale answer from tearing down the new one.
export function retrySession(handle: string): void {
    const s = sessions.get(handle);
    if (!s || s.stopped) {
        return;
    }

    s.player.stop();
    s.video.srcObject = null;
    withdrawFrames(handle, s.video);
    negotiate(s);
}

// A fresh exchange for a stream that is PLAYING, done as Retry does it, so
// the server hears of this viewer again. It learns who watches what from the
// exchange alone, and keeps that in memory, so a restart (or Clips switched
// on after the tile started) leaves somebody visibly watching unknown to it,
// and a playing tile never negotiates again by itself. Only ever for a
// refused clip (clips.ts, renewBeforeRefusing), whose editor covers the tile
// while it tunes in again.
//
// Settles true once pictures arrive from the new session, false if there was
// no playing session to renew, or the new one failed or was stopped. It
// always settles: negotiate's stall watchdog fails a session that never
// shows a picture.
export function renewSession(handle: string): Promise<boolean> {
    const s = sessions.get(handle);
    if (!s || s.stopped || s.status !== 'playing') {
        return Promise.resolve(false);
    }

    return new Promise((resolve) => {
        const listener = () => {
            if (s.stopped || s.status === 'failed') {
                infoListeners.delete(listener);
                resolve(false);
            } else if (s.status === 'playing') {
                infoListeners.delete(listener);
                resolve(true);
            }
        };

        // Connecting from here on, synchronously, so the listener added after
        // it hears only the new session's outcome.
        retrySession(handle);
        infoListeners.add(listener);
    });
}

// Taking the sound. Has to be called INSIDE the click: browsers treat
// unmuting as needing a user gesture, and an effect runs after the event has
// finished. The other streams are muted first, through the watch list, so two
// are never audible at once; this one is then unmuted, and played if the
// browser had paused it.
export function takeSound(handle: string): void {
    const s = sessions.get(handle);
    if (s && !s.stopped) {
        if (s.volume === 0) {
            s.volume = UNMUTE_VOLUME;
        }
        s.video.volume = s.volume;
        s.blocked = false;
        changed(s);
    }

    if (getAudio() !== handle) {
        setAudio(handle);
    }

    if (s && !s.stopped) {
        applyAudio(s);
    }
}

// The volume slider. Dragging it to nothing gives the sound up; dragging a
// silent tile's slider takes the sound for it, as the speaker button does.
export function setSessionVolume(handle: string, volume: number): void {
    const s = sessions.get(handle);
    if (!s || s.stopped) {
        return;
    }

    s.volume = volume;
    s.video.volume = volume;

    if (volume === 0) {
        s.video.muted = true;
        changed(s);
        if (getAudio() === handle) {
            setAudio(null);
        }

        return;
    }

    changed(s);
    if (getAudio() !== handle || s.blocked) {
        takeSound(handle);
    }
}

// A tile switching off: the sound cuts the instant the switch is pressed,
// while the picture plays its animation.
export function quietSession(handle: string, quiet: boolean): void {
    const s = sessions.get(handle);
    if (!s || s.stopped || s.quiet === quiet) {
        return;
    }

    s.quiet = quiet;
    applyAudio(s);
}

export function getSessionInfo(handle: string): SessionInfo | null {
    return sessions.get(handle)?.info || null;
}

// A component's view of one session, re-rendered only when it changes: a
// subscription made by hand, which on the legacy root Mattermost 11.11 mounts
// by default does what React 18's useSyncExternalStore would.
export function useSessionInfo(handle: string): SessionInfo | null {
    const [, forceRender] = React.useState(0);
    const current = getSessionInfo(handle);
    const rendered = React.useRef(current);
    rendered.current = current;

    React.useEffect(() => {
        const listener = () => {
            if (getSessionInfo(handle) !== rendered.current) {
                forceRender((value) => value + 1);
            }
        };
        infoListeners.add(listener);

        // Anything that changed between the render and now.
        listener();

        return () => {
            infoListeners.delete(listener);
        };
    }, [handle]);

    return current;
}

export function stopSession(handle: string, reason: string): void {
    const s = sessions.get(handle);
    if (s) {
        stop(s, reason);
    }
}

export function stopAllSessions(reason: string): void {
    Array.from(sessions.values()).forEach((s) => stop(s, reason));
}

export function sessionCount(): number {
    return sessions.size;
}

export function lastSessionStop(): {handle: string; reason: string; at: number} | null {
    return lastStop;
}

// Follows the watch list: a stream that leaves it is hung up at once, and the
// sound goes wherever the list now says. Installed by initialize, before any
// component can mount; the watch list tells this before any component.
export function installSessions(): () => void {
    return onWatchingChange(() => {
        const watched = new Set(getWatching());
        Array.from(sessions.values()).forEach((s) => {
            if (!watched.has(s.handle)) {
                stop(s, 'unwatched');
            }
        });

        applyAudioToAll();
    });
}
