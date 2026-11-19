import {clipTime, positionTime} from './clipLogic';

// The clip player's rules (components/ClipTile.tsx): the parts of it that are
// arithmetic rather than React, so build/check-player.mjs can run them
// without a browser. How long a clip is and where it has got to, what the
// clock under the picture says, where a press on the scrubber lands, and what
// each key does.
//
// The player is Victor's pick of 2026-10-13 (design/3.3.0.md): the stream
// tile's own look. Its timeline runs from the clip's own start to its own
// end. The file begins at the keyframe before the clip's start (the lead-in),
// and can run on past its end; neither is on the timeline.

// A position this close to the end is the end: a clip's own length is in
// milliseconds, and the picture stops on a frame.
export const END_SLACK = 0.05;

// How far a key moves the clip, in seconds: the arrows, and J and L.
export const STEP = 5;
export const LEAP = 10;

// How long the clip is, in seconds: its own length from the server, but no
// longer than the file turns out to be past its lead-in. Either can still be
// unknown (0); a clip with no length of its own goes by its file.
export function clipLength(durationMs: number, fileSeconds: number): number {
    const own = durationMs > 0 ? durationMs / 1000 : 0;
    if (!(fileSeconds > 0) || !Number.isFinite(fileSeconds)) {
        return own;
    }

    return own > 0 ? Math.min(own, fileSeconds) : fileSeconds;
}

// The file's length past its lead-in, from the element's duration, which is
// NaN until the file says and Infinity for a file that never does.
export function fileSecondsOf(duration: number, lead: number): number {
    return Number.isFinite(duration) ? Math.max(0, duration - lead) : 0;
}

// Where the clip is, in seconds of the clip itself, from where the file is.
// Until its length is known only its start holds it in.
export function clipAt(fileTime: number, lead: number, length: number): number {
    const at = Math.max(0, fileTime - lead);

    return length > 0 ? Math.min(at, length) : at;
}

// Where a seek lands: somewhere in the clip, its start to its end. Nowhere
// but the start while its length is unknown.
export function seekTarget(seconds: number, length: number): number {
    if (!(length > 0)) {
        return 0;
    }

    return Math.min(Math.max(0, seconds), length);
}

// Whether the clip has got to its own end: it stops there, and its play
// button is Replay.
export function atEnd(at: number, length: number): boolean {
    return length > 0 && at >= length - END_SLACK;
}

// Whether a clip that is playing has run past its own end, which comes
// before the file's when the file runs on: the player stops it there.
export function ranPastEnd(at: number, length: number): boolean {
    return length > 0 && at >= length;
}

// How far along the clip is, as a share of the scrubber, 0 to 1.
export function shareOf(at: number, length: number): number {
    if (!(length > 0)) {
        return 0;
    }

    return Math.min(1, Math.max(0, at / length));
}

// How much of the clip has come, as a share, from where the part of the file
// the browser holds ends.
export function loadedShare(bufferedEnd: number, lead: number, length: number): number {
    if (!(length > 0)) {
        return 0;
    }

    return Math.min(1, Math.max(0, bufferedEnd - lead) / length);
}

// Where in the clip a press on the scrubber lands, from where the pointer is
// and where the track is. Held to the track, so a drag past either end stays
// at that end.
export function secondsAt(clientX: number, left: number, width: number, length: number): number {
    if (!(width > 0) || !(length > 0)) {
        return 0;
    }

    return Math.min(1, Math.max(0, (clientX - left) / width)) * length;
}

// The clock under the picture, "0:12 / 0:45": where the clip is, rounded down
// as a player's clock is, and how long it is. At its end it reads its length,
// so a clip of 44.6 seconds ends at "0:45 / 0:45" rather than "0:44 / 0:45".
export type ClipClock = {at: string; length: string};

export function clipClock(at: number, length: number): ClipClock {
    const total = clipTime(length * 1000);

    return {at: atEnd(at, length) ? total : positionTime(at * 1000), length: total};
}

// The same in words, for the scrubber's value, which is what a screen reader
// hears instead of the clock: "0:12 of 0:45".
export function clockWords(at: number, length: number): string {
    const clock = clipClock(at, length);

    return `${clock.at} of ${clock.length}`;
}

// What the player shows, for the stylesheet and for what its controls say.
// A file that would not load comes first; then waiting for the file, which
// covers the picture; then the end, where it stops with Replay.
export type PlayerState = 'paused' | 'playing' | 'ended' | 'loading' | 'failed';

export function playerState(now: {failed: boolean; loading: boolean; ended: boolean; paused: boolean}): PlayerState {
    if (now.failed) {
        return 'failed';
    }
    if (now.loading) {
        return 'loading';
    }
    if (now.ended) {
        return 'ended';
    }

    return now.paused ? 'paused' : 'playing';
}

// What the player's controls hold (design/3.3.0.md 1.1 and 2). While the clip
// can be played, everything that plays it (play, the sound and its volume,
// full screen, the scrubber, and a click on the picture), and Download while
// the reader may download it. Once its file has failed there is nothing left
// to play, but Download stays: a file this browser could not play is exactly
// the one worth downloading to play somewhere else. With neither, there are
// no controls at all.
export type PlayerControls = {play: boolean; download: boolean};

export function playerControls(now: {failed: boolean; downloadOffered: boolean}): PlayerControls {
    return {play: !now.failed, download: now.downloadOffered};
}

// What the scrubber says to a screen reader, in seconds of the clip: where the
// clip is, except while the scrubber has focus and the clip plays on by
// itself. Then it holds at held, where the clip was when focus came or when
// the reader last moved it, because a screen reader reads the focused
// slider's value out whenever it changes, and would say the time every second
// over the clip's own sound. The scissors' name is held still the same way
// (Hud.tsx, labelKey). Paused or dragged (live), it says where the clip is,
// once. held is null while the scrubber does not have focus.
export function heardAt(at: number, held: number | null, live: boolean): number {
    return held === null || live ? at : held;
}

// What the player is waiting for, if anything: its file before it knows how
// long it is, while paused; and, while it plays, its first frame ('first')
// or more of the file ('stalled'). Nothing once the file has failed, or in a
// browser that cannot play it at all. The player says so only once it has
// waited a moment (ClipTile.tsx), so a file that comes at once never flashes
// a loading screen over its picture.
export type FileWait = '' | 'first' | 'stalled';

export function fileWait(now: {
    failed: boolean;
    cannotPlay: boolean;
    paused: boolean;
    metadata: boolean;
    firstFrame: boolean;
    stalled: boolean;
}): FileWait {
    if (now.failed || now.cannotPlay) {
        return '';
    }
    if (now.paused) {
        return now.metadata ? '' : 'first';
    }
    if (!now.firstFrame) {
        return 'first';
    }

    return now.stalled ? 'stalled' : '';
}

// What a key pressed anywhere in the player does, or null for a key that is
// not the player's and goes on to the dialog (Escape, Tab and the rest):
//
//   - Space or K plays and pauses, and is Replay at the end; Space on a
//     button presses that button instead, as it always does.
//   - The arrows go back or on five seconds, except on a slider, which they
//     move. J and L go ten.
//   - M mutes and unmutes; F is full screen.
//
// Never with Ctrl, Command or Alt, which are the browser's, nor while an
// input method is composing: modified says either.
export type PlayerKey =
    | {act: 'toggle'}
    | {act: 'seek'; by: number}
    | {act: 'mute'}
    | {act: 'fullscreen'};

export function playerKey(key: string, where: {modified: boolean; onButton: boolean; onSlider: boolean}): PlayerKey | null {
    if (where.modified) {
        return null;
    }

    switch (key) {
    case ' ':
    case 'Spacebar':
        return where.onButton ? null : {act: 'toggle'};
    case 'k':
    case 'K':
        return {act: 'toggle'};
    case 'ArrowLeft':
    case 'ArrowRight':
        if (where.onSlider) {
            return null;
        }

        return {act: 'seek', by: key === 'ArrowLeft' ? -STEP : STEP};
    case 'j':
    case 'J':
        return {act: 'seek', by: -LEAP};
    case 'l':
    case 'L':
        return {act: 'seek', by: LEAP};
    case 'm':
    case 'M':
        return {act: 'mute'};
    case 'f':
    case 'F':
        return {act: 'fullscreen'};
    default:
        return null;
    }
}

// The scrubber's own keys, as any slider's are: the arrows five seconds,
// Page Up and Page Down ten, Home and End to the clip's start and end.
export type ScrubKey = {by: number} | {to: 'start' | 'end'};

export function scrubberKey(key: string, modified: boolean): ScrubKey | null {
    if (modified) {
        return null;
    }

    switch (key) {
    case 'ArrowLeft':
    case 'ArrowDown':
        return {by: -STEP};
    case 'ArrowRight':
    case 'ArrowUp':
        return {by: STEP};
    case 'PageDown':
        return {by: -LEAP};
    case 'PageUp':
        return {by: LEAP};
    case 'Home':
        return {to: 'start'};
    case 'End':
        return {to: 'end'};
    default:
        return null;
    }
}
