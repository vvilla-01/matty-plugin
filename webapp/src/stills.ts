import {claimStill, uploadStill} from './client';
import {getSnapshot, snapshotAge, subscribe} from './store';
import type {Stream} from './types';
import {getWatching, MAX_WATCHING} from './watching';
import {WhepPlayer} from './whep';

// Preview stills for the Live Now cards: a recent picture of each live stream,
// taken by somebody's browser and shared through the plugin with everybody.
//
// Broadcast Box has no thumbnails, and the plugin server cannot decode video,
// so a still can only come from a browser that is playing the stream. Two ways:
//
//   - A tile already playing in this panel: a frame from its <video>, which
//     costs nothing at all.
//   - A stream nobody here is watching: a PEEK — a short hidden session, just
//     long enough for the first picture, then closed.
//
// Either way the server decides who takes it. A browser asks for a lease on a
// stream, and gets one only when that stream's still has gone stale and nobody
// else holds one — so a still is taken once for every open panel, not once per
// panel. A browser that is watching gets a head start over one that would have
// to peek, because its frame is free. When the answer is "not now", it says
// when to ask again, and this asks then and not before.
//
// Everything is gentle on purpose: one capture at a time, only while the panel
// is open and the page is visible, never a peek for a stream that is paused or
// a 24/7 channel (those have a picture an admin chose), and never a peek that
// would take this browser past the four decoders the panel allows itself.

// The still that is sent: 16:9, cropped from the middle like the card crops
// it, and big enough for a card on a high-density screen.
const STILL_WIDTH = 480;
const STILL_HEIGHT = 270;
const STILL_QUALITY = 0.72;

// How often the list is looked over, when nothing else has prompted it.
const TICK_MS = 5000;

// A peek that has shown no picture by now is given up. The same allowance the
// player gives a tile before it calls it stalled.
const PEEK_TIMEOUT_MS = 12000;

// Breathing space between two peeks from this browser.
const PEEK_GAP_MS = 4000;

// How long to leave a stream alone after something went wrong with it.
const BACKOFF_MS = 60000;

// After a request to the plugin fails, everything waits: a minute, doubling
// with each failure in a row up to ten, until one succeeds. A plugin that is
// restarting answers 404 on every route, and a panel that kept asking would be
// the steady trickle of 404s a brute-force jail in front of Mattermost bans
// people for.
const SUSPEND_MS = 60000;
const SUSPEND_MAX_MS = 600000;

// A snapshot older than this means the panel has lost touch with the server: it
// polls every 30 s while the panel, or anything else showing a stream, is up,
// is pushed every change, and backs off by itself when polls fail (store.ts).
// Nothing is captured on what may be a stale list.
const STALE_SNAPSHOT_MS = 75000;

type Mode = 'watching' | 'peek';

// Streams playing in this window, and the <video> each one plays into. The
// session pool (sessions.ts) offers a stream's element once pictures arrive
// while it is in the page, and withdraws it when it leaves the page, fails or
// stops.
const playing = new Map<string, HTMLVideoElement>();

export function offerFrames(handle: string, video: HTMLVideoElement): void {
    playing.set(handle, video);
    soon();
}

export function withdrawFrames(handle: string, video: HTMLVideoElement): void {
    if (playing.get(handle) === video) {
        playing.delete(handle);
    }
}

// When each stream may next be asked about, per way of asking. The server's
// own answer, so no clock here has to agree with the server's.
const notBefore = new Map<string, number>();

let running = 0;
let busy = false;
let failures = 0;
let suspendedUntil = 0;
let lastPeek = 0;
let timer = 0;
let prompt = 0;
let peeking: WhepPlayer | null = null;

function soon(): void {
    if (running === 0 || prompt) {
        return;
    }

    prompt = window.setTimeout(() => {
        prompt = 0;
        void tick();
    }, 250);
}

// For a React effect in the panel: capture while it is open. Counted rather
// than switched, so a remount that overlaps its own cleanup cannot stop it.
export function captureStillsWhileOpen(): () => void {
    running++;
    if (running === 1) {
        timer = window.setInterval(() => void tick(), TICK_MS);
    }

    const unsubscribe = subscribe(soon);
    soon();

    return () => {
        unsubscribe();
        running--;
        if (running === 0) {
            window.clearInterval(timer);
            window.clearTimeout(prompt);
            timer = 0;
            prompt = 0;
            peeking?.stop();
        }
    };
}

const key = (handle: string, mode: Mode): string => `${mode}:${handle}`;

function due(handle: string, mode: Mode, now: number): boolean {
    return (notBefore.get(key(handle, mode)) || 0) <= now;
}

function later(handle: string, mode: Mode, ms: number): void {
    notBefore.set(key(handle, mode), Date.now() + ms);
}

// A request to the plugin went through, or did not.
function succeeded(): void {
    failures = 0;
    suspendedUntil = 0;
}

function failed(): void {
    suspendedUntil = Date.now() + Math.min(SUSPEND_MS * (2 ** failures), SUSPEND_MAX_MS);
    failures++;
}

async function tick(): Promise<void> {
    if (busy || running === 0 || document.hidden || Date.now() < suspendedUntil || snapshotAge() > STALE_SNAPSHOT_MS) {
        return;
    }

    const snapshot = getSnapshot();
    if (snapshot.livePreviews !== true) {
        return;
    }

    // Forget streams that have gone.
    const live = new Set(snapshot.streams.map((stream) => stream.handle));
    for (const entry of Array.from(notBefore.keys())) {
        if (!live.has(entry.slice(entry.indexOf(':') + 1))) {
            notBefore.delete(entry);
        }
    }

    // Never a 24/7 channel, and never a Twitch stream: a frame of Twitch's
    // player cannot be drawn, Twitch's video must not be copied, and the
    // server relays Twitch's own preview instead (design/twitch-live.md 6.8).
    const now = Date.now();
    const candidates = snapshot.streams.filter((stream) => !stream.alwaysOn && !stream.source);

    // A frame from a tile already playing first: it is free — as long as the
    // tile really is playing. One that has lost its connection, or given up
    // resuming, still shows its last frame, which is not what is on now.
    for (const stream of candidates) {
        const video = playing.get(stream.handle);
        if (video && due(stream.handle, 'watching', now) && moving(video)) {
            busy = true;
            try {
                await captureWatching(stream, video);
            } finally {
                busy = false;
            }

            return;
        }
    }

    if (!mayPeek(now)) {
        return;
    }

    // Never a peek at a stream this panel has a tile for, even one still
    // tuning in: that tile will supply the frame for free once it plays.
    const shown = new Set(getWatching());
    for (const stream of candidates) {
        if (!playing.has(stream.handle) && !shown.has(stream.handle) && !stream.paused && due(stream.handle, 'peek', now)) {
            busy = true;
            try {
                await capturePeek(stream);
            } finally {
                busy = false;
                lastPeek = Date.now();
            }

            return;
        }
    }
}

function mayPeek(now: number): boolean {
    if (now - lastPeek < PEEK_GAP_MS) {
        return false;
    }

    // Somebody who has asked their browser to save data is not spending it on
    // pictures of streams they are not watching.
    const connection = (navigator as Navigator & {connection?: {saveData?: boolean}}).connection;
    if (connection?.saveData) {
        return false;
    }

    // A peek is one more decoder, briefly. Never past the panel's own limit.
    return getWatching().length < MAX_WATCHING;
}

// Asks for the lease, and on "not now" remembers when to ask again.
async function lease(stream: Stream, mode: Mode): Promise<string> {
    try {
        const claim = await claimStill(stream.handle, mode);
        succeeded();
        if (claim.lease) {
            return claim.lease;
        }

        later(stream.handle, mode, Math.max(5, claim.retryAfter || 60) * 1000);
    } catch {
        failed();
        later(stream.handle, mode, BACKOFF_MS);
    }

    return '';
}

// Whether a tile's video is actually moving: in the page, playing, with a
// picture, and a new frame decoded since the last capture from it.
//
// In the page as well, because one element now outlives the tiles that show it
// (sessions.ts): it is withdrawn when it is taken out of one, but an element
// out of the page is one the browser is no longer obliged to decode, and its
// last frame is not what is on now.
const framesAtCapture = new WeakMap<HTMLVideoElement, number>();

function moving(video: HTMLVideoElement): boolean {
    if (!video.isConnected || video.paused || video.readyState < 2) {
        return false;
    }

    const quality = typeof video.getVideoPlaybackQuality === 'function' ? video.getVideoPlaybackQuality() : null;
    if (!quality) {
        return true;
    }

    const frames = quality.totalVideoFrames;
    if (framesAtCapture.get(video) === frames) {
        return false;
    }
    framesAtCapture.set(video, frames);

    return true;
}

async function share(stream: Stream, mode: Mode, token: string, still: Blob | null): Promise<void> {
    if (!still) {
        // Nothing worth showing — a black frame, a stream that sent none.
        // Leave it for a while; the lease simply runs out.
        later(stream.handle, mode, BACKOFF_MS);

        return;
    }

    try {
        await uploadStill(stream.handle, token, still);
        succeeded();
    } catch {
        failed();
        later(stream.handle, mode, BACKOFF_MS);
    }
}

async function captureWatching(stream: Stream, video: HTMLVideoElement): Promise<void> {
    const token = await lease(stream, 'watching');
    if (token) {
        await share(stream, 'watching', token, await grab(video));
    }
}

async function capturePeek(stream: Stream): Promise<void> {
    const token = await lease(stream, 'peek');
    if (token) {
        await share(stream, 'peek', token, await peek(stream.handle, token));
    }
}

// A short hidden session, just long enough for the first picture.
//
// The element is in the page, not detached, and a few pixels in size rather
// than none: browsers are entitled to stop decoding a video nobody can see.
// It is transparent, silent, out of the way of the pointer and of assistive
// technology, and gone again within seconds.
async function peek(handle: string, token: string): Promise<Blob | null> {
    const video = document.createElement('video');
    video.muted = true;
    video.autoplay = true;
    video.playsInline = true;
    video.setAttribute('aria-hidden', 'true');
    video.tabIndex = -1;
    Object.assign(video.style, {
        position: 'fixed',
        right: '0',
        bottom: '0',
        width: '16px',
        height: '9px',
        opacity: '0.01',
        pointerEvents: 'none',
        zIndex: '-1',
    });
    document.body.appendChild(video);

    const player = new WhepPlayer();
    peeking = player;

    let timeout = 0;
    let arrived: () => void = () => undefined;

    try {
        const pictures = new Promise<void>((resolve, reject) => {
            timeout = window.setTimeout(() => reject(new Error('no picture')), PEEK_TIMEOUT_MS);
            arrived = () => {
                if (video.videoWidth > 0 && video.videoHeight > 0) {
                    window.clearTimeout(timeout);
                    resolve();
                }
            };
            video.addEventListener('loadeddata', arrived);
            video.addEventListener('resize', arrived);
        });

        // Observed from the start, so a negotiation that fails before this
        // is awaited does not leave it to reject unhandled later.
        pictures.catch(() => undefined);

        await player.play(handle, video, undefined, token);
        video.play().catch(() => undefined);
        await pictures;
        await nextFrame(video);

        return await grab(video);
    } catch {
        return null;
    } finally {
        window.clearTimeout(timeout);
        video.removeEventListener('loadeddata', arrived);
        video.removeEventListener('resize', arrived);
        player.stop();
        if (peeking === player) {
            peeking = null;
        }
        video.srcObject = null;
        video.remove();
    }
}

// The first decoded frame can still be the one before the picture settles;
// waiting for the next one costs a frame's time.
function nextFrame(video: HTMLVideoElement): Promise<void> {
    return new Promise((resolve) => {
        const withCallback = video as HTMLVideoElement & {requestVideoFrameCallback?: (callback: () => void) => number};
        const fallback = window.setTimeout(resolve, 250);
        withCallback.requestVideoFrameCallback?.(() => {
            window.clearTimeout(fallback);
            resolve();
        });
    });
}

// One frame, cropped from the middle to 16:9 and encoded as a JPEG. Null for a
// frame that is all but black — a publisher between scenes, or nothing
// decoded yet — which would only replace a good still with a worse one.
//
// Drawing a WebRTC <video> leaves the canvas readable: the stream comes from a
// plain RTCPeerConnection, not an isolated one.
function grab(video: HTMLVideoElement): Promise<Blob | null> {
    const sourceWidth = video.videoWidth;
    const sourceHeight = video.videoHeight;
    if (!sourceWidth || !sourceHeight) {
        return Promise.resolve(null);
    }

    const aspect = STILL_WIDTH / STILL_HEIGHT;
    let cropWidth = sourceWidth;
    let cropHeight = sourceWidth / aspect;
    if (cropHeight > sourceHeight) {
        cropHeight = sourceHeight;
        cropWidth = sourceHeight * aspect;
    }

    const canvas = document.createElement('canvas');
    canvas.width = STILL_WIDTH;
    canvas.height = STILL_HEIGHT;
    const context = canvas.getContext('2d');
    if (!context) {
        return Promise.resolve(null);
    }

    try {
        context.drawImage(
            video,
            (sourceWidth - cropWidth) / 2,
            (sourceHeight - cropHeight) / 2,
            cropWidth,
            cropHeight,
            0,
            0,
            STILL_WIDTH,
            STILL_HEIGHT,
        );

        if (blank(canvas)) {
            return Promise.resolve(null);
        }
    } catch {
        return Promise.resolve(null);
    }

    return new Promise((resolve) => {
        canvas.toBlob((blob) => resolve(blob && blob.type === 'image/jpeg' ? blob : null), 'image/jpeg', STILL_QUALITY);
    });
}

// Whether a frame is all but black, judged from a thumbnail of it.
function blank(source: HTMLCanvasElement): boolean {
    const sample = document.createElement('canvas');
    sample.width = 32;
    sample.height = 18;
    const context = sample.getContext('2d');
    if (!context) {
        return false;
    }

    context.drawImage(source, 0, 0, sample.width, sample.height);
    const {data} = context.getImageData(0, 0, sample.width, sample.height);
    for (let index = 0; index < data.length; index += 4) {
        if (data[index] > 12 || data[index + 1] > 12 || data[index + 2] > 12) {
            return false;
        }
    }

    return true;
}
