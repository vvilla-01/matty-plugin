import {clipVideoURL} from './client';
import {levelled, RATE} from './loudness';
import {isDesktopApp} from './mattermost';
import {atLeastShortest, ENCODER_CONFIG, encoderSupported, packetSection, packetsFit, readPackets, SOUND_MAX_MS, toBase64} from './soundLogic';

// The browser's half of a sound (design/3.2.0.md 7.2, and Victor's answer to
// question 10): whether this browser can make one at all, a file's, a clip's
// or the microphone's sound decoded, and the stretch kept made into the Opus
// packets the server checks, keeps and plays. All three sources end in the
// same decoded 48 kHz mono sound, so the server cannot tell them apart.
//
// The plugin's server cannot encode Opus (gumble's codec needs cgo, and the build
// has none), so the browser does: WebCodecs' AudioEncoder at Mumble's own
// client default, 40 kbit/s at a constant bitrate in 20 ms frames, mono.
// Loudness is levelled first (loudness.ts), so every sound on the board is
// about as loud as every other.
//
// Its own module so the lab, which has no microphone and nothing to decode,
// can swap in a stand-in (lab/mocks/soundEncode.ts), as it swaps ./client.

// What WebCodecs needs, as far as this uses it. TypeScript's DOM library has
// the video half of WebCodecs but not the audio half, so the few members used
// are written out here, and every one is looked for on window before use.
type AudioDataInit = {format: 'f32-planar'; sampleRate: number; numberOfFrames: number; numberOfChannels: number; timestamp: number; data: Float32Array};
type AudioDataLike = {numberOfFrames: number; copyTo: (destination: Float32Array, options: {planeIndex: number; format?: 'f32-planar'}) => void; close: () => void};
type ChunkLike = {byteLength: number; copyTo: (destination: Uint8Array) => void};
type SupportAnswer = {supported?: boolean; config?: {bitrateMode?: string}};
type EncoderLike = {
    configure: (config: unknown) => void;
    encode: (data: AudioDataLike) => void;
    flush: () => Promise<void>;
    close: () => void;
};
type DecoderLike = {
    configure: (config: unknown) => void;
    decode: (chunk: unknown) => void;
    flush: () => Promise<void>;
    close: () => void;
};
type WebCodecs = {
    AudioEncoder?: {
        new (init: {output: (chunk: ChunkLike) => void; error: (reason: unknown) => void}): EncoderLike;
        isConfigSupported: (config: unknown) => Promise<SupportAnswer>;
    };
    AudioDecoder?: {
        new (init: {output: (data: AudioDataLike) => void; error: (reason: unknown) => void}): DecoderLike;
        isConfigSupported: (config: unknown) => Promise<{supported?: boolean}>;
    };
    AudioData?: new (init: AudioDataInit) => AudioDataLike;
    EncodedAudioChunk?: new (init: {type: 'key'; timestamp: number; duration: number; data: Uint8Array}) => unknown;
};

const codecs = (): WebCodecs => window as unknown as WebCodecs;

// A sound file is decoded whole into memory, which is why it is held to 4 MB.
export const FILE_LIMIT = 4 * 1024 * 1024;

// Recording stops by itself at six seconds; somebody can stop it sooner.
export const RECORD_MS = 6000;

// How a sound looks on the trim: its loudest moment in each slice, 0 to 1.
export const PEAKS = 120;

export type Decoded = {samples: Float32Array; durationMs: number; peaks: number[]};

// Why a sound could not be made: the browser's encoder cannot do it right, or
// there is nothing in it to hear.
export class SoundMakeError extends Error {
    reason: 'browser' | 'silent';

    constructor(reason: 'browser' | 'silent') {
        super(reason === 'silent' ? 'That sounds silent.' : 'This browser can’t make sounds.');
        this.name = 'SoundMakeError';
        this.reason = reason;
    }
}

// Only where WebCodecs encodes Opus at a constant bitrate (Chromium 119 and
// the desktop app, Firefox 130, Safari 26): a browser that quietly encoded at
// a variable one would make packets the server refuses (7.2 step 1.4).
export async function canMakeSounds(): Promise<boolean> {
    const {AudioEncoder, AudioData} = codecs();
    if (!AudioEncoder || !AudioData) {
        return false;
    }

    try {
        return encoderSupported(await AudioEncoder.isConfigSupported(ENCODER_CONFIG));
    } catch {
        return false;
    }
}

const DECODER_CONFIG = {codec: 'opus', sampleRate: RATE, numberOfChannels: 1};

// Whether Listen here can play a stored sound in this browser; where it
// cannot, the item is not offered (7.13).
export async function canHearSounds(): Promise<boolean> {
    const {AudioDecoder, EncodedAudioChunk} = codecs();
    if (!AudioDecoder || !EncodedAudioChunk) {
        return false;
    }

    try {
        const answer = await AudioDecoder.isConfigSupported(DECODER_CONFIG);

        return answer.supported === true;
    } catch {
        return false;
    }
}

// Mattermost's desktop app asks for the microphone with a prompt of its own,
// whose answer is kept for the whole server and shared with Calls (M15), so
// the record step warns about "Deny Permanently" there and only there.
export function inDesktopApp(): boolean {
    return isDesktopApp();
}

// One slice every 50 ms, and never fewer than PEAKS: the trim draws a long
// sound through a window, and needs the detail inside it.
function peaksOf(samples: Float32Array): number[] {
    const slices = Math.max(PEAKS, Math.round(samples.length / (RATE * 0.05)));
    const size = Math.max(1, Math.floor(samples.length / slices));
    const peaks: number[] = [];
    let loudest = 0;
    for (let slice = 0; slice < slices; slice += 1) {
        let peak = 0;
        const end = Math.min(samples.length, (slice + 1) * size);
        for (let at = slice * size; at < end; at += 1) {
            peak = Math.max(peak, Math.abs(samples[at]));
        }
        peaks.push(peak);
        loudest = Math.max(loudest, peak);
    }

    return peaks.map((peak) => (loudest > 0 ? peak / loudest : 0));
}

// Decoded whole and resampled to 48 kHz by the browser (an
// OfflineAudioContext's decodeAudioData resamples to its own rate), then its
// channels averaged into one.
export async function decodeBytes(bytes: ArrayBuffer): Promise<Decoded> {
    const context = new OfflineAudioContext(1, 1, RATE);
    const audio = await context.decodeAudioData(bytes);
    const samples = new Float32Array(audio.length);
    for (let channel = 0; channel < audio.numberOfChannels; channel += 1) {
        const data = audio.getChannelData(channel);
        for (let i = 0; i < samples.length; i += 1) {
            samples[i] += data[i] / audio.numberOfChannels;
        }
    }

    return {samples, durationMs: Math.round((samples.length / RATE) * 1000), peaks: peaksOf(samples)};
}

export async function decodeFile(file: File): Promise<Decoded> {
    return decodeBytes(await file.arrayBuffer());
}

// A clip's own MP4, which the plugin already serves to anybody who may watch
// it, decoded here: only clips the reader may watch can be a source.
export async function decodeClip(clipId: string): Promise<Decoded> {
    const response = await fetch(clipVideoURL(clipId), {credentials: 'same-origin', headers: {'X-Requested-With': 'XMLHttpRequest'}});
    if (!response.ok) {
        throw new Error('That clip couldn’t be read as sound.');
    }

    return decodeBytes(await response.arrayBuffer());
}

// A recording under way. stop ends it and hands over what was recorded;
// cancel ends it and throws it away, for a flow that has been left.
export type Recording = {stop: () => void; cancel: () => void};

// The microphone, for at most six seconds: a level for the growing waveform
// while it records, and the decoded sound when it stops. The browser (or the
// desktop app) asks first; a refusal rejects.
//
// Six seconds is a promise ("Stops by itself at 6 seconds"), so it is kept by
// a timer, not by the waveform's frames, which a browser stops drawing in a
// hidden tab; and the recording stops the moment the page is hidden, when
// nobody can see that the microphone is on. Whatever goes wrong once the
// microphone is open closes it again.
export async function startRecording(
    onLevel: (levels: number[], ms: number) => void,
    onDone: (decoded: Decoded) => void,
    onFailed: () => void,
): Promise<Recording> {
    const stream = await navigator.mediaDevices.getUserMedia({audio: {echoCancellation: false, noiseSuppression: false, autoGainControl: false}});

    let context: AudioContext | null = null;
    let released = false;
    const release = () => {
        if (released) {
            return;
        }
        released = true;
        stream.getTracks().forEach((track) => track.stop());
        void context?.close().catch(() => undefined);
    };

    try {
        context = new AudioContext();
        const analyser = context.createAnalyser();
        analyser.fftSize = 1024;
        context.createMediaStreamSource(stream).connect(analyser);
        const recorder = new MediaRecorder(stream);
        const chunks: Blob[] = [];
        const levels: number[] = [];
        const frame = new Float32Array(analyser.fftSize);
        let started = 0;
        let ticking = 0;
        let limit = 0;
        let stopped = false;
        let dropped = false;

        const end = (drop: boolean) => {
            if (stopped) {
                return;
            }
            stopped = true;
            dropped = drop;
            window.cancelAnimationFrame(ticking);
            window.clearTimeout(limit);
            document.removeEventListener('visibilitychange', onHidden);
            const recording = recorder.state !== 'inactive';
            if (recording) {
                recorder.stop();
            }

            // Thrown away: the microphone goes off now, not when the
            // recorder gets round to saying it has stopped.
            if (drop || !recording) {
                release();
            }
        };
        const stop = () => end(false);
        function onHidden() {
            if (document.visibilityState === 'hidden') {
                stop();
            }
        }

        const tick = () => {
            analyser.getFloatTimeDomainData(frame);
            levels.push(frame.reduce((peak, sample) => Math.max(peak, Math.abs(sample)), 0));
            const ms = performance.now() - started;
            onLevel(levels.slice(), Math.min(ms, RECORD_MS));
            if (ms >= RECORD_MS) {
                stop();

                return;
            }
            ticking = window.requestAnimationFrame(tick);
        };

        recorder.ondataavailable = (event) => chunks.push(event.data);
        recorder.onstop = () => {
            release();
            if (!dropped) {
                new Blob(chunks).arrayBuffer().then(decodeBytes).then(onDone).catch(onFailed);
            }
        };
        recorder.start();
        started = performance.now();
        limit = window.setTimeout(stop, RECORD_MS);
        document.addEventListener('visibilitychange', onHidden);
        ticking = window.requestAnimationFrame(tick);
        if (document.visibilityState === 'hidden') {
            stop();
        }

        return {stop, cancel: () => end(true)};
    } catch (reason) {
        release();

        throw reason;
    }
}

// The stretch kept, as samples.
function stretch(decoded: Decoded, inMs: number, outMs: number): Float32Array {
    const from = Math.max(0, Math.round((inMs / 1000) * RATE));
    const to = Math.min(decoded.samples.length, Math.round((Math.min(outMs, inMs + SOUND_MAX_MS) / 1000) * RATE));

    return decoded.samples.slice(from, Math.max(from, to));
}

// The stretch kept, levelled, as it will sound on the board; null for
// silence. A whole sound shorter than a quarter of a second, which the trim
// keeps all of, ends in silence up to that quarter, the shortest sound the
// server takes.
export function prepared(decoded: Decoded, inMs: number, outMs: number): Float32Array | null {
    const made = levelled(stretch(decoded, inMs, outMs));

    return Number.isFinite(made.loudness) ? atLeastShortest(made.samples) : null;
}

// Any trouble inside the encoder is the browser's: WebCodecs closes an
// encoder that errs and rejects its flush with the same error, so that is
// caught here and said as such, never left to read as the store failing.
async function encode(pcm: Float32Array): Promise<Uint8Array[]> {
    const {AudioEncoder, AudioData} = codecs();
    if (!AudioEncoder || !AudioData) {
        throw new SoundMakeError('browser');
    }

    const packets: Uint8Array[] = [];
    let failed: unknown = null;
    let encoder: EncoderLike | null = null;
    let data: AudioDataLike | null = null;
    try {
        encoder = new AudioEncoder({
            output: (chunk) => {
                const bytes = new Uint8Array(chunk.byteLength);
                chunk.copyTo(bytes);
                packets.push(bytes);
            },
            error: (reason) => {
                failed = reason;
            },
        });
        encoder.configure(ENCODER_CONFIG);
        data = new AudioData({format: 'f32-planar', sampleRate: RATE, numberOfFrames: pcm.length, numberOfChannels: 1, timestamp: 0, data: pcm});
        encoder.encode(data);
        await encoder.flush();
    } catch {
        throw new SoundMakeError('browser');
    } finally {
        closeQuietly(encoder);
        closeQuietly(data);
    }
    if (failed !== null) {
        throw new SoundMakeError('browser');
    }

    return packets;
}

// Closing something WebCodecs has already closed throws; nothing is lost.
function closeQuietly(thing: {close: () => void} | null): void {
    try {
        thing?.close();
    } catch {
        // Already closed.
    }
}

// What an upload carries: the packets section in base64, and how long it is.
export type Made = {packets: string; ms: number};

// The sound made: trimmed, levelled, encoded, and the encoder's packets
// checked against the server's bounds before anything is sent, so a browser
// that encodes wrongly is told so rather than refused (7.2 step 1.4).
export async function makeSound(decoded: Decoded, inMs: number, outMs: number): Promise<Made> {
    const pcm = prepared(decoded, inMs, outMs);
    if (!pcm) {
        throw new SoundMakeError('silent');
    }

    const packets = await encode(pcm);
    if (!packetsFit(packets)) {
        throw new SoundMakeError('browser');
    }

    return {packets: toBase64(packetSection(packets)), ms: packets.length * 20};
}

export type Playing = {stop: () => void};

function playSamples(samples: Float32Array, onEnd: () => void): Playing {
    const context = new AudioContext({sampleRate: RATE});
    const buffer = context.createBuffer(1, Math.max(1, samples.length), RATE);
    buffer.copyToChannel(samples, 0);
    const source = context.createBufferSource();
    source.buffer = buffer;
    source.connect(context.destination);
    let ended = false;
    const end = () => {
        if (!ended) {
            ended = true;
            void context.close();
            onEnd();
        }
    };
    source.onended = end;
    source.start();

    return {
        stop: () => {
            try {
                source.stop();
            } catch {
                // Already over.
            }
            end();
        },
    };
}

// Listen, in the add flow: the stretch as it will sound on the board, in this
// browser only.
export function listenHere(decoded: Decoded, inMs: number, outMs: number, onEnd: () => void): Playing {
    return playSamples(prepared(decoded, inMs, outMs) || stretch(decoded, inMs, outMs), onEnd);
}

// Listen here, on one of your own tiles: the stored packets, decoded with
// WebCodecs' AudioDecoder and played in this browser only.
export async function playStored(bytes: ArrayBuffer, onEnd: () => void): Promise<Playing> {
    const {AudioDecoder, EncodedAudioChunk} = codecs();
    const packets = readPackets(new Uint8Array(bytes));
    if (!AudioDecoder || !EncodedAudioChunk || !packets) {
        throw new Error('This browser can’t play it here.');
    }

    const pieces: Float32Array[] = [];
    const decoder = new AudioDecoder({
        output: (data) => {
            const piece = new Float32Array(data.numberOfFrames);
            data.copyTo(piece, {planeIndex: 0, format: 'f32-planar'});
            pieces.push(piece);
            data.close();
        },
        error: () => undefined,
    });
    decoder.configure(DECODER_CONFIG);
    packets.forEach((packet, index) => {
        decoder.decode(new EncodedAudioChunk({type: 'key', timestamp: index * 20000, duration: 20000, data: packet}));
    });
    await decoder.flush();
    decoder.close();

    const samples = new Float32Array(pieces.reduce((sum, piece) => sum + piece.length, 0));
    let at = 0;
    pieces.forEach((piece) => {
        samples.set(piece, at);
        at += piece.length;
    });

    return playSamples(samples, onEnd);
}
