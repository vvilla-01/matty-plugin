import type {SoundBoard, SoundEmoji, SoundPlay, SoundWhy} from './types';

// The soundboard's rules that are only arithmetic (design/3.2.0.md 7.13):
// what the tiles are doing at a given moment, the words for why they are
// off, the trim's bounds, the emoji the chooser suggests, whether this browser
// may make sounds at all, and the bytes an upload carries. Pure, and with no
// imports but types, so build/check-sounds.mjs can load it as check-twitch.mjs
// loads twitchLogic.ts.

// A sound is a quarter of a second to six seconds long (7.6), its name 16
// characters at most (7.8).
export const SOUND_MIN_MS = 250;
export const SOUND_MAX_MS = 6000;
export const SOUND_NAME_MAX = 16;

// The wait after your own sound, when the server has not said how long it is:
// SoundCooldownSeconds' default.
export const SOUND_COOLDOWN_MS = 10000;

// Your own press, as the server answered it, with which sound it was.
export type OwnPlay = SoundPlay & {id: string};

// Somebody else's sound, as the sound event told it: until when, and when
// this panel heard, which is where the bar under the head starts emptying.
export type BusyHeard = {until: number; heardAt: number};

// What every tile is doing now. Fractions run from 0 to 1: done is how much
// of your sound has played, left how much of a wait is still to go.
export type BoardPhase =
    {kind: 'ready'} |
    {kind: 'playing'; id: string; done: number} |
    {kind: 'cooldown'; id: string; left: number; seconds: number} |
    {kind: 'busy'; left: number} |
    {kind: 'off'; why: OffWhy};

// Why the tiles are off: Mumble's reasons, or an admin's block, which the
// board carries beside them rather than in voice.why.
export type OffWhy = SoundWhy | 'blocked';

const unit = (value: number): number => Math.min(1, Math.max(0, value));

// In order: an admin's block, which no change in Mumble undoes; then Mumble
// saying no (muted, gone, the panel unable), because nothing can play; then
// your own sound while it plays; then your wait; then somebody else's sound.
// A board not yet loaded is ready, and draws no tiles.
export function boardPhase(board: SoundBoard | null, own: OwnPlay | null, busy: BusyHeard | null, now: number): BoardPhase {
    if (!board) {
        return {kind: 'ready'};
    }

    if (board.blocked) {
        return {kind: 'off', why: 'blocked'};
    }
    if (board.voice.why !== '') {
        return {kind: 'off', why: board.voice.why};
    }
    if (!board.bot.ok) {
        return {kind: 'off', why: 'bot_cannot'};
    }

    if (own && now < own.endsAt) {
        const length = Math.max(1, own.endsAt - own.startedAt);

        return {kind: 'playing', id: own.id, done: unit((now - own.startedAt) / length)};
    }

    const coolUntil = Math.max(board.cooldownUntil, own ? own.cooldownUntil : 0);
    if (now < coolUntil) {
        const wait = own && own.cooldownUntil >= coolUntil ?
            own.cooldownUntil - own.endsAt :
            board.limits?.cooldownMs || SOUND_COOLDOWN_MS;

        return {
            kind: 'cooldown',
            id: own ? own.id : '',
            left: unit((coolUntil - now) / Math.max(1, wait)),
            seconds: Math.ceil((coolUntil - now) / 1000),
        };
    }

    const busyUntil = Math.max(board.busyUntil, busy ? busy.until : 0);
    if (now < busyUntil) {
        const from = busy && busy.until >= busyUntil ? busy.heardAt : now;

        return {kind: 'busy', left: unit((busyUntil - now) / Math.max(1, busyUntil - from))};
    }

    return {kind: 'ready'};
}

// The sheet's line under SOUNDBOARD, while the tiles can be pressed or are
// only waiting (7.14). The reasons they are off are whyLine's.
//
// Two lines are shown here that 7.14 gives only to screen readers or to a
// refused press, so that what the dimmed tiles mean is on screen: the wait
// ("Ready again in 7 seconds", the screen readers' words without the full
// stop) and somebody else's sound ("Another sound is playing.", the first
// sentence of the busy refusal; nothing can be pressed meanwhile, so "try
// again" has nothing to answer). Both are one line at 304 px. While your own
// sound plays the line stays "Plays in {channel}". (The P1 preview's proposals
// to 7.14, approved with Victor's pick.)
export function noteOf(phase: BoardPhase, channel?: string): string {
    switch (phase.kind) {
    case 'cooldown':
        return `Ready again in ${phase.seconds} ${phase.seconds === 1 ? 'second' : 'seconds'}`;
    case 'busy':
        return 'Another sound is playing.';
    case 'off':
        return '';
    default:
        return channel ? `Plays in ${channel}` : '';
    }
}

// Adding a sound needs only a matched Mumble session, in any state (7.7):
// muted, deafened or suppressed people may add one (it makes no sound), and
// so may somebody whose sounds the plugin cannot play. Only Mumble out of reach,
// the reader gone from it, or an admin's block (which the server refuses an
// add for) stops it.
export const canAddWhile = (why: OffWhy): boolean => why !== 'bot_offline' && why !== 'not_in_voice' && why !== 'blocked';

// Why the tiles are off, in the words of design/3.2.0.md 7.14, with the
// server's name for the plugin (name.ts), which the caller hands over: this
// file imports nothing of the page's.
export function whyLine(why: OffWhy, name: string, channel?: string): string {
    const where = channel || 'your channel';
    switch (why) {
    case 'muted':
        return 'You’re muted in Mumble. Unmute to play sounds.';
    case 'deafened':
        return 'You’re deafened in Mumble. Undeafen to play sounds.';
    case 'suppressed':
        return 'Mumble isn’t letting you speak in this channel, so sounds are off.';
    case 'not_registered':
        return 'Sounds are only for registered Mumble names. Register yours on the Mumble server to use them.';
    case 'two_sessions':
        return 'You’re in Mumble twice. Leave one to play sounds.';
    case 'not_in_voice':
        return 'You’ve left Mumble.';
    case 'bot_offline':
        return `${name} can’t reach Mumble just now.`;
    case 'bot_cannot':
        return `${name} can’t play into ${where}: an admin needs to check its Mumble permissions.`;
    case 'blocked':
        return 'An admin has turned the soundboard off for you.';
    default:
        return '';
    }
}

// The trim: a window of a quarter of a second to six seconds inside the
// sound. Moving one end past six seconds from the other brings the other
// along, so the window slides rather than the handle stopping dead. The end
// moved gives way to keep the quarter of a second, unless it is against the
// sound's own start or end: then the other end does. A sound shorter than a
// quarter of a second is all of it.
export function clampTrim(inMs: number, outMs: number, durationMs: number, moved: 'in' | 'out'): {inMs: number; outMs: number} {
    const shortest = Math.min(SOUND_MIN_MS, durationMs);
    if (moved === 'in') {
        const wanted = Math.max(0, Math.min(inMs, outMs - shortest));
        const end = Math.min(durationMs, Math.max(outMs, wanted + shortest));
        const start = Math.max(0, Math.min(wanted, end - shortest));

        return {inMs: start, outMs: end - start > SOUND_MAX_MS ? start + SOUND_MAX_MS : end};
    }

    const end = Math.min(durationMs, Math.max(outMs, Math.max(0, inMs) + shortest));
    const start = Math.max(0, Math.min(inMs, end - shortest));

    return {inMs: end - start > SOUND_MAX_MS ? end - SOUND_MAX_MS : start, outMs: end};
}

// The shortest sound the server takes is a quarter of a second, at the
// encoder's 48 kHz.
const SHORTEST_SAMPLES = (SOUND_MIN_MS / 1000) * 48000;

// A sound shorter than that, which the trim keeps whole, with silence after
// it up to a quarter of a second; anything longer as it is. Silence lowers
// its measured loudness and never raises it, so the server's ceiling holds.
export function atLeastShortest(samples: Float32Array): Float32Array {
    if (samples.length >= SHORTEST_SAMPLES) {
        return samples;
    }

    const padded = new Float32Array(SHORTEST_SAMPLES);
    padded.set(samples);

    return padded;
}

// "2.4 s", as the readout, the tooltip and the trim say a length.
export function seconds(ms: number): string {
    return `${(Math.round(ms / 100) / 10).toFixed(1)} s`;
}

// Characters left in a name, counted as a person counts them.
export function nameLeft(name: string): number {
    return SOUND_NAME_MAX - Array.from(name).length;
}

// The roving tab stop: where an arrow key, Home or End goes from index in a
// grid of count tiles, columns to a row. -1 for a key that is not a move.
export function nextIndex(index: number, key: string, count: number, columns: number): number {
    const last = count - 1;
    switch (key) {
    case 'ArrowRight':
        return Math.min(last, index + 1);
    case 'ArrowLeft':
        return Math.max(0, index - 1);
    case 'ArrowDown':
        return index + columns <= last ? index + columns : index;
    case 'ArrowUp':
        return index - columns >= 0 ? index - columns : index;
    case 'Home':
        return 0;
    case 'End':
        return last;
    default:
        return -1;
    }
}

// The forty the chooser suggests before anybody types (7.13), each checked
// against model.SystemEmojis by a Go test in the build, with the unified code
// Mattermost files its picture under. 🔊 loud_sound first: a new sound's
// emoji until one is picked.
export const SUGGESTED_EMOJI: Array<{name: string; unified: string}> = [
    ['loud_sound', '1f50a'], ['tada', '1f389'], ['clap', '1f44f'], ['joy', '1f602'], ['rofl', '1f923'],
    ['scream', '1f631'], ['skull', '1f480'], ['fire', '1f525'], ['100', '1f4af'], ['boom', '1f4a5'],
    ['drum_with_drumsticks', '1f941'], ['trumpet', '1f3ba'], ['bell', '1f514'], ['rotating_light', '1f6a8'],
    ['ghost', '1f47b'], ['robot_face', '1f916'], ['goat', '1f410'], ['rooster', '1f413'], ['cat', '1f431'],
    ['dog', '1f436'], ['wolf', '1f43a'], ['cow', '1f42e'], ['frog', '1f438'], ['see_no_evil', '1f648'],
    ['eyes', '1f440'], ['+1', '1f44d'], ['-1', '1f44e'], ['wave', '1f44b'], ['pray', '1f64f'],
    ['muscle', '1f4aa'], ['heart', '2764-fe0f'], ['broken_heart', '1f494'], ['zzz', '1f4a4'],
    ['face_palm', '1f926'], ['crown', '1f451'], ['trophy', '1f3c6'], ['video_game', '1f3ae'],
    ['popcorn', '1f37f'], ['beer', '1f37a'], ['rocket', '1f680'],
].map(([name, unified]) => ({name, unified}));

export const DEFAULT_EMOJI: SoundEmoji = {kind: 'system', name: 'loud_sound', unified: '1f50a'};

// What a refused press, add, rename or delete says (7.14), by the server's
// code. A code with a reason of Mumble's own is whyLine's; time in seconds,
// rounded up, for the per-minute cap.
export function refusalLine(code: string, name: string, retryAfterMs = 0, channel?: string): string {
    const why = whyLine(code as OffWhy, name, channel);
    if (why !== '' && code !== 'not_in_voice') {
        return why;
    }

    switch (code) {
    case 'not_in_voice':
        return 'Join Mumble to add or change sounds.';
    case 'cooldown': {
        const left = Math.max(1, Math.ceil(retryAfterMs / 1000));

        return `Ready again in ${left} ${left === 1 ? 'second' : 'seconds'}.`;
    }
    case 'busy':
        return 'Another sound is playing. Try again in a moment.';
    case 'moved':
        // The server found the presser in another channel by the time the
        // sound would have started, and played nothing rather than play it
        // into the room they had left.
        return 'You moved channel in Mumble as you pressed. Press it again to play it there.';
    case 'too_many': {
        const left = Math.max(1, Math.ceil(retryAfterMs / 1000));

        return `That’s a lot of sounds this minute. Try again in ${left} ${left === 1 ? 'second' : 'seconds'}.`;
    }
    case 'gone':
        return 'That sound has been deleted.';
    case 'off':
        return 'The soundboard has been switched off.';
    case 'bad_sound':
        return `${name} couldn’t check that sound. Make it again from the file.`;
    case 'too_loud':
        return `That’s much louder than ${name} makes sounds. Make it again from the file.`;
    case 'silent':
        return 'That sounds silent.';
    case 'bad_name':
        return 'Give it a name of 16 characters at most, on one line.';
    case 'bad_emoji':
        return 'That emoji isn’t available here any more. Pick another.';
    case 'yours_full':
        return YOURS_FULL;
    case 'full':
        return BOARD_FULL;
    case 'not_yours':
        return 'Only whoever added a sound can change it.';
    case 'rate_limited':
        return 'That’s a lot just now. Try again in a minute.';
    default:
        return `${name} couldn’t reach its store. Try again.`;
    }
}

export const YOURS_FULL = 'You have 16 sounds, the most. Delete one to add another.';
export const BOARD_FULL = 'The board is full. Ask an admin to make room.';
export const emptyBoard = (name: string): string => `No sounds yet. Add the first: a few seconds from a sound file, your microphone or a clip in ${name}.`;

// ---- Making a sound in the browser (7.2) ----

// The encoder's settings: Mumble's own client default, 40 kbit/s in 20 ms
// frames, mono, at a constant bitrate, which gives packets of 100 bytes.
export const ENCODER_CONFIG = {
    codec: 'opus',
    sampleRate: 48000,
    numberOfChannels: 1,
    bitrate: 40000,
    bitrateMode: 'constant',
    opus: {frameDuration: 20000, complexity: 10},
};

// Only where WebCodecs encodes Opus at a constant bitrate (Chromium 119 and the
// desktop app, Firefox 130, Safari 26). A browser that does not know
// bitrateMode drops it from the config it echoes, and would quietly encode at
// a variable bitrate, making packets up to 165 bytes that the server refuses;
// so supported alone is not enough.
export function encoderSupported(answer: {supported?: boolean; config?: {bitrateMode?: string}} | null | undefined): boolean {
    return Boolean(answer && answer.supported === true && answer.config && answer.config.bitrateMode === 'constant');
}

// The server's bounds (7.8): 13 packets (a quarter of a second) to 301 (six
// seconds, and one more for the encoder's delay), each 1 to 128 bytes, no more
// than 40,000 bytes in all.
export const MIN_PACKETS = 13;
export const MAX_PACKETS = SOUND_MAX_MS / 20 + 1;
export const MAX_PACKET_BYTES = 128;
export const MAX_SECTION_BYTES = 40000;

// Whether the encoder's packets are ones the server will take. Checked before
// uploading, so a browser that encodes wrongly is told so rather than sending
// something that will be refused.
export function packetsFit(packets: Uint8Array[]): boolean {
    if (packets.length < MIN_PACKETS || packets.length > MAX_PACKETS) {
        return false;
    }
    if (packets.some((packet) => packet.length < 1 || packet.length > MAX_PACKET_BYTES)) {
        return false;
    }

    return sectionBytes(packets) <= MAX_SECTION_BYTES;
}

const sectionBytes = (packets: Uint8Array[]): number => packets.reduce((sum, packet) => sum + 2 + packet.length, 2);

// The packets section as the server stores and reads it: a count, then each
// packet's length and bytes, every number a big-endian (network order) u16.
export function packetSection(packets: Uint8Array[]): Uint8Array {
    const out = new Uint8Array(sectionBytes(packets));
    const view = new DataView(out.buffer);
    view.setUint16(0, packets.length);
    let at = 2;
    packets.forEach((packet) => {
        view.setUint16(at, packet.length);
        out.set(packet, at + 2);
        at += 2 + packet.length;
    });

    return out;
}

// The packets back out of a section, or null when the bytes are not one. A
// stored sound comes back with its header ("VNS2", when it was added, the
// count, the frame length and a checksum) in front, which is skipped.
export function readPackets(bytes: Uint8Array): Uint8Array[] | null {
    const header = bytes.length >= 47 && bytes[0] === 0x56 && bytes[1] === 0x4e && bytes[2] === 0x53 && bytes[3] === 0x32;
    if (header) {
        return readSection(bytes.subarray(47), new DataView(bytes.buffer, bytes.byteOffset + 12, 2).getUint16(0));
    }

    return readSection(bytes.subarray(2), bytes.length >= 2 ? new DataView(bytes.buffer, bytes.byteOffset, 2).getUint16(0) : -1);
}

function readSection(body: Uint8Array, count: number): Uint8Array[] | null {
    if (count < 1) {
        return null;
    }

    const view = new DataView(body.buffer, body.byteOffset, body.byteLength);
    const packets: Uint8Array[] = [];
    let at = 0;
    for (let index = 0; index < count; index += 1) {
        if (at + 2 > body.length) {
            return null;
        }
        const length = view.getUint16(at);
        if (length < 1 || at + 2 + length > body.length) {
            return null;
        }
        packets.push(body.subarray(at + 2, at + 2 + length));
        at += 2 + length;
    }

    return at === body.length ? packets : null;
}

const BASE64 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';

// Standard base64 with padding, as Go's encoding/base64 StdEncoding reads it.
export function toBase64(bytes: Uint8Array): string {
    let out = '';
    for (let at = 0; at < bytes.length; at += 3) {
        const a = bytes[at];
        const b = at + 1 < bytes.length ? bytes[at + 1] : 0;
        const c = at + 2 < bytes.length ? bytes[at + 2] : 0;
        out += BASE64[a >> 2] + BASE64[((a & 3) << 4) | (b >> 4)];
        out += at + 1 < bytes.length ? BASE64[((b & 15) << 2) | (c >> 6)] : '=';
        out += at + 2 < bytes.length ? BASE64[c & 63] : '=';
    }

    return out;
}

// The tile's tooltip: "Airhorn · added by Dave · 2.4 s".
export function tileTitle(sound: {name: string; owner: string; ms: number}): string {
    return `${sound.name} · added by ${sound.owner} · ${seconds(sound.ms)}`;
}
