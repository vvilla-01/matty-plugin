import fixture from '../fixtures/sound-packets.json';
import {packetSection, readPackets, SOUND_MAX_MS, toBase64} from '../../src/soundLogic';
import {getWorld} from '../world';

// The lab's stand-in for the browser's half of making a sound
// (src/soundEncode.ts), swapped in by webpack as ./client is. The lab has no
// microphone to ask and nothing worth decoding, so every sound here has a
// made-up shape, the same every time for the same source so a screenshot is
// the same every time; and what it "encodes" is real Opus, packets headless
// Chrome's WebCodecs made in B0 (fixtures/sound-packets.json), cut to the
// length kept, so the upload the mock client checks is the real format.

export const FILE_LIMIT = 4 * 1024 * 1024;
export const RECORD_MS = 6000;
export const PEAKS = 120;

export type Decoded = {samples: Float32Array; durationMs: number; peaks: number[]};
export type Recording = {stop: () => void; cancel: () => void};
export type Made = {packets: string; ms: number};
export type Playing = {stop: () => void};

export class SoundMakeError extends Error {
    reason: 'browser' | 'silent';

    constructor(reason: 'browser' | 'silent') {
        super(reason === 'silent' ? 'That sounds silent.' : 'This browser can’t make sounds.');
        this.name = 'SoundMakeError';
        this.reason = reason;
    }
}

// Whether this "browser" encodes at a constant bitrate is the lab's to say
// (the Soundboard row's Encoder), so the sheet's "can't make sounds" state can
// be looked at.
export const canMakeSounds = (): Promise<boolean> => Promise.resolve(getWorld().soundEncoder === 'constant');
export const canHearSounds = (): Promise<boolean> => Promise.resolve(getWorld().soundEncoder === 'constant');

// The lab plays the desktop app, the one place the microphone warning shows.
export const inDesktopApp = (): boolean => true;

// A small, repeatable noise.
function noise(seed: number): () => number {
    let state = seed || 1;

    return () => {
        state = (state * 16807) % 2147483647;

        return state / 2147483647;
    };
}

// Bursts of sound with gaps between, like somebody talking or a game going
// off, louder where loud is given.
export function labPeaks(seed: number, count = PEAKS, loud: Array<[number, number]> = []): number[] {
    const next = noise(seed);
    let envelope = 0.3;

    return Array.from({length: count}, (_, index) => {
        const at = index / count;
        if (next() < 0.08) {
            envelope = 0.12 + (next() * 0.36);
        }
        const boost = loud.some(([from, to]) => at >= from && at <= to);
        const level = boost ? 0.75 + (next() * 0.25) : envelope * (0.55 + (next() * 0.45));

        return Math.min(1, level);
    });
}

const shaped = (durationMs: number, peaks: number[]): Decoded => ({samples: new Float32Array(0), durationMs, peaks});

export const decodeBytes = (bytes: ArrayBuffer): Promise<Decoded> =>
    Promise.resolve(shaped(4200 + (bytes.byteLength % 3000), labPeaks(bytes.byteLength)));

// A file "picked" in the lab: an airhorn, a second of quiet and then the
// horn, nine seconds in all, so the trim has something to cut. A file whose
// name says it is silent is: nothing in it to hear. One whose name says it
// is broken cannot be read.
export const decodeFile = (file: File): Promise<Decoded> =>
    new Promise((resolve, reject) => window.setTimeout(() => {
        if ((/broken/i).test(file.name)) {
            reject(new Error('not audio'));

            return;
        }
        if ((/silen/i).test(file.name)) {
            resolve(shaped(3000, Array.from({length: PEAKS}, () => 0)));

            return;
        }
        resolve(shaped(9400, labPeaks(file.size || 7, 188, [[0.12, 0.38], [0.52, 0.6]]).map((peak, index) => (index < 19 ? peak * 0.12 : peak))));
    }, 260));

// Every lab clip is the same sample clip; its sound gets a shape of its own
// for each clip, with the moment worth keeping a third of the way in.
export const decodeClip = (clipId: string): Promise<Decoded> =>
    new Promise((resolve, reject) => window.setTimeout(() => {
        const clip = getWorld().clips.find((candidate) => candidate.id === clipId);
        if (!clip) {
            reject(new Error('That clip couldn’t be read as sound.'));

            return;
        }
        resolve(shaped(clip.durationMs, labPeaks(clip.title.length + 11, Math.max(PEAKS, Math.round(clip.durationMs / 50)), [[0.3, 0.33]])));
    }, 320));

// Recording: a level every 50 ms, for six seconds or until stopped. The lab's
// microphone can be refused (the Soundboard row's Microphone), as a browser's
// prompt can be.
export function startRecording(
    onLevel: (levels: number[], ms: number) => void,
    onDone: (decoded: Decoded) => void,
    onFailed: () => void,
): Promise<Recording> {
    if (labMicrophone === 'refused') {
        return Promise.reject(new DOMException('Permission denied', 'NotAllowedError'));
    }

    const levels = labPeaks(42, PEAKS, [[0.25, 0.55]]);
    const started = Date.now();
    let stopped = false;

    // As a real recorder does, what was recorded arrives a moment after the
    // stop, once the recorder has said it stopped and the sound is decoded;
    // a cancelled one never arrives.
    const end = (drop: boolean) => {
        if (stopped) {
            return;
        }
        stopped = true;
        window.clearInterval(timer);
        if (drop) {
            return;
        }
        const ms = Math.max(400, Math.min(RECORD_MS, Date.now() - started));
        window.setTimeout(() => {
            if (labMicrophone === 'fails') {
                onFailed();

                return;
            }
            onDone(shaped(ms, levels.slice(0, Math.max(8, Math.round((ms / RECORD_MS) * PEAKS)))));
        }, 150);
    };
    const stop = () => end(false);
    const timer = window.setInterval(() => {
        const ms = Math.min(RECORD_MS, Date.now() - started);
        onLevel(levels.slice(0, Math.round((ms / RECORD_MS) * PEAKS)), ms);
        if (ms >= RECORD_MS) {
            stop();
        }
    }, 50);

    return Promise.resolve({stop, cancel: () => end(true)});
}

let labMicrophone: 'allowed' | 'refused' | 'fails' = 'allowed';

export function setLabMicrophone(next: typeof labMicrophone): void {
    labMicrophone = next;
}

export function prepared(decoded: Decoded, inMs: number, outMs: number): Float32Array | null {
    return stretchIsSilent(decoded, inMs, outMs) ? null : new Float32Array(Math.round((Math.max(250, outMs - inMs) / 1000) * 48000));
}

function stretchIsSilent(decoded: Decoded, inMs: number, outMs: number): boolean {
    const per = decoded.durationMs / Math.max(1, decoded.peaks.length);
    const from = Math.floor(inMs / per);
    const to = Math.max(from + 1, Math.ceil(outMs / per));

    return decoded.peaks.slice(from, to).every((peak) => peak < 0.02);
}

// The stretch kept, "encoded": as many of the fixture's real packets as the
// encoder would have made for it (one more than its 20 ms frames, as
// WebCodecs' delay makes; fx-1), after a pause like the real one's.
export function makeSound(decoded: Decoded, inMs: number, outMs: number): Promise<Made> {
    return new Promise((resolve, reject) => window.setTimeout(() => {
        if (getWorld().soundEncoder !== 'constant') {
            reject(new SoundMakeError('browser'));

            return;
        }
        if (stretchIsSilent(decoded, inMs, outMs)) {
            reject(new SoundMakeError('silent'));

            return;
        }
        const ms = Math.min(SOUND_MAX_MS, Math.max(250, outMs - inMs));
        const count = Math.min(fixture.fixtures.full6s.length, Math.round(ms / 20) + 1);
        const packets = fixture.fixtures.full6s.slice(0, count).map((packet) => Uint8Array.from(atob(packet), (char) => char.charCodeAt(0)));
        resolve({packets: toBase64(packetSection(packets)), ms: count * 20});
    }, 380));
}

// Listening, in the lab: nothing to hear, so only the time it takes.
function pretendToPlay(ms: number, onEnd: () => void): Playing {
    const timer = window.setTimeout(onEnd, Math.max(300, ms));

    return {
        stop: () => {
            window.clearTimeout(timer);
            onEnd();
        },
    };
}

export function listenHere(_decoded: Decoded, inMs: number, outMs: number, onEnd: () => void): Playing {
    return pretendToPlay(outMs - inMs, onEnd);
}

// A stored sound read as the real one reads it, header and all, and only the
// time it takes played.
export function playStored(bytes: ArrayBuffer, onEnd: () => void): Promise<Playing> {
    const packets = readPackets(new Uint8Array(bytes));
    if (!packets) {
        return Promise.reject(new Error('This browser can’t play it here.'));
    }

    return Promise.resolve(pretendToPlay(packets.length * 20, onEnd));
}
