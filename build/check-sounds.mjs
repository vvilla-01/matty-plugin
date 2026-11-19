// The soundboard's rules (design/3.2.0.md 7, and 7.17's webapp checks),
// checked without a browser: what the tiles are doing at any moment, how far
// a ring or a wait has run, the roving tab stop, the trim's bounds, the bytes
// an upload carries, which browsers may make a sound at all, and the loudness
// measure against answers known in advance.
//
// webapp/src/soundLogic.ts imports only types, and webapp/src/loudness.ts
// nothing, so both are transpiled with the project's own TypeScript, imported
// from a data URL and run here, as check-twitch.mjs does with twitchLogic.ts.
// The packets the lab plays are the ones B0 made in headless Chrome with
// build/wcprobe.html (webapp/lab/fixtures/sound-packets.json). Run from the
// project root (make check-webapp).

import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createRequire} from 'node:module';

const require = createRequire(import.meta.url);
const ts = require('../webapp/node_modules/typescript');

async function load(file) {
    const source = readFileSync(new URL(`../webapp/src/${file}`, import.meta.url), 'utf8');
    const out = ts.transpileModule(source, {
        compilerOptions: {module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2020},
        fileName: file,
    });

    return import(`data:text/javascript;base64,${Buffer.from(out.outputText).toString('base64')}`);
}

const sound = await load('soundLogic.ts');
const loud = await load('loudness.ts');
const fixture = JSON.parse(readFileSync(new URL('../webapp/lab/fixtures/sound-packets.json', import.meta.url), 'utf8'));

let checks = 0;
function check(name, run) {
    try {
        run();
        checks++;
    } catch (error) {
        console.error(`check-sounds: ${name}`);
        throw error;
    }
}

const close = (actual, expected, within, what) => assert.ok(Math.abs(actual - expected) <= within, `${what}: ${actual} is not within ${within} of ${expected}`);

// A board as GET /sounds answers it, in voice and able to play.
const board = (fields = {}) => ({
    voice: {in: true, channel: 'Lounge', can: true, why: ''},
    bot: {ok: true},
    cooldownUntil: 0,
    busyUntil: 0,
    v: 'abc',
    sounds: [],
    limits: {maxMs: 6000, perPerson: 16, total: 120, name: 16, left: 12, cooldownMs: 10000},
    ...fields,
});

// ---- The tiles' state ----

check('no board yet is ready, and draws nothing pressed', () => {
    assert.deepEqual(sound.boardPhase(null, null, null, 1000), {kind: 'ready'});
    assert.deepEqual(sound.boardPhase(board(), null, null, 1000), {kind: 'ready'});
});

check('your own sound plays: the ring runs from 0 to 1 over its length, on the tile pressed', () => {
    const own = {id: 's1', startedAt: 1000, endsAt: 3000, cooldownUntil: 13000, channel: 'Lounge'};
    assert.deepEqual(sound.boardPhase(board(), own, null, 1000), {kind: 'playing', id: 's1', done: 0});
    assert.deepEqual(sound.boardPhase(board(), own, null, 2000), {kind: 'playing', id: 's1', done: 0.5});
    assert.equal(sound.boardPhase(board(), own, null, 2999).done > 0.99, true);
});

check('after your sound, the wait: the tile pressed counts whole seconds down, the share left drains from 1 to 0', () => {
    const own = {id: 's1', startedAt: 1000, endsAt: 3000, cooldownUntil: 13000, channel: 'Lounge'};
    const start = sound.boardPhase(board(), own, null, 3000);
    assert.equal(start.kind, 'cooldown');
    assert.equal(start.id, 's1');
    assert.equal(start.left, 1);
    assert.equal(start.seconds, 10);
    const mid = sound.boardPhase(board(), own, null, 8000);
    assert.equal(mid.left, 0.5);
    assert.equal(mid.seconds, 5);
    assert.equal(sound.boardPhase(board(), own, null, 12001).seconds, 1, 'rounded up: never "ready in 0"');
    assert.deepEqual(sound.boardPhase(board(), own, null, 13000), {kind: 'ready'});
});

check('a wait the server knows of from before the sheet opened is drawn over the cooldown the server says', () => {
    const phase = sound.boardPhase(board({cooldownUntil: 6000}), null, null, 1000);
    assert.equal(phase.kind, 'cooldown');
    assert.equal(phase.id, '', 'no tile of yours to sweep');
    assert.equal(phase.left, 0.5, 'five of the ten seconds the board says waits are');
});

check('somebody else’s sound: busy, the bar emptying from when it was heard', () => {
    const heard = {until: 5000, heardAt: 1000};
    const start = sound.boardPhase(board(), null, heard, 1000);
    assert.deepEqual(start, {kind: 'busy', left: 1});
    assert.deepEqual(sound.boardPhase(board(), null, heard, 3000), {kind: 'busy', left: 0.5});
    assert.deepEqual(sound.boardPhase(board(), null, heard, 5000), {kind: 'ready'});
    assert.equal(sound.boardPhase(board({busyUntil: 4000}), null, null, 2000).kind, 'busy', 'the board’s own word for it too');
});

check('Mumble saying no wins over everything, and the plugin’s own account unable is its own reason', () => {
    const own = {id: 's1', startedAt: 1000, endsAt: 3000, cooldownUntil: 13000, channel: 'Lounge'};
    for (const why of ['muted', 'deafened', 'suppressed', 'not_registered', 'two_sessions', 'not_in_voice', 'bot_offline']) {
        assert.deepEqual(sound.boardPhase(board({voice: {in: true, can: false, why}}), own, {until: 9000, heardAt: 0}, 2000), {kind: 'off', why}, why);
    }
    assert.deepEqual(sound.boardPhase(board({bot: {ok: false}}), null, null, 2000), {kind: 'off', why: 'bot_cannot'});
});

check('an admin’s block turns the tiles and adding off, whatever Mumble says', () => {
    assert.deepEqual(sound.boardPhase(board({blocked: true}), null, null, 2000), {kind: 'off', why: 'blocked'});
    assert.deepEqual(sound.boardPhase(board({blocked: true, voice: {in: true, can: false, why: 'muted'}}), null, null, 2000), {kind: 'off', why: 'blocked'});
    assert.equal(sound.canAddWhile('blocked'), false);
    assert.equal(sound.whyLine('blocked', 'Matty'), 'An admin has turned the soundboard off for you.');
    assert.equal(sound.refusalLine('blocked', 'Matty'), 'An admin has turned the soundboard off for you.');
});

check('the line under the head: the channel, the wait, somebody else’s sound, and nothing while off', () => {
    assert.equal(sound.noteOf({kind: 'ready'}, 'Lounge'), 'Plays in Lounge');
    assert.equal(sound.noteOf({kind: 'playing', id: 's', done: 0.3}, 'Lounge'), 'Plays in Lounge');
    assert.equal(sound.noteOf({kind: 'cooldown', id: 's', left: 0.7, seconds: 7}, 'Lounge'), 'Ready again in 7 seconds');
    assert.equal(sound.noteOf({kind: 'cooldown', id: 's', left: 0.1, seconds: 1}, 'Lounge'), 'Ready again in 1 second');
    assert.equal(sound.noteOf({kind: 'busy', left: 0.5}, 'Lounge'), 'Another sound is playing.');
    assert.equal(sound.noteOf({kind: 'off', why: 'muted'}, 'Lounge'), '');
});

check('adding needs only a matched session in any state: muted people may add, a reader gone from Mumble may not', () => {
    for (const why of ['', 'muted', 'deafened', 'suppressed', 'not_registered', 'two_sessions', 'bot_cannot']) {
        assert.equal(sound.canAddWhile(why), true, why);
    }
    assert.equal(sound.canAddWhile('bot_offline'), false);
    assert.equal(sound.canAddWhile('not_in_voice'), false);
});

check('every reason and refusal has the contract’s words', () => {
    assert.equal(sound.whyLine('muted', 'Matty'), 'You’re muted in Mumble. Unmute to play sounds.');
    assert.equal(sound.whyLine('bot_cannot', 'Matty', 'Lounge'), 'Matty can’t play into Lounge: an admin needs to check its Mumble permissions.');
    assert.equal(sound.refusalLine('not_in_voice', 'Matty'), 'Join Mumble to add or change sounds.');
    assert.equal(sound.refusalLine('busy', 'Matty'), 'Another sound is playing. Try again in a moment.');
    assert.equal(sound.refusalLine('moved', 'Matty'), 'You moved channel in Mumble as you pressed. Press it again to play it there.');
    assert.equal(sound.refusalLine('too_many', 'Matty', 4200), 'That’s a lot of sounds this minute. Try again in 5 seconds.');
    assert.equal(sound.refusalLine('too_loud', 'Matty'), 'That’s much louder than Matty makes sounds. Make it again from the file.');
    assert.equal(sound.refusalLine('bad_emoji', 'Matty'), 'That emoji isn’t available here any more. Pick another.');
    assert.equal(sound.refusalLine('yours_full', 'Matty'), 'You have 16 sounds, the most. Delete one to add another.');
    assert.equal(sound.refusalLine('full', 'Matty'), 'The board is full. Ask an admin to make room.');
    assert.equal(sound.refusalLine('store', 'Matty'), 'Matty couldn’t reach its store. Try again.');
    assert.equal(sound.whyLine('bot_offline', 'Matty'), 'Matty can’t reach Mumble just now.');
    assert.equal(sound.refusalLine('bad_sound', 'Matty'), 'Matty couldn’t check that sound. Make it again from the file.');
    // With the server's name for the plugin, whatever it is.
    assert.equal(sound.whyLine('bot_offline', 'Movie Night Club'), 'Movie Night Club can’t reach Mumble just now.');
    assert.equal(sound.whyLine('bot_cannot', 'Movie Night Club', 'Lounge'), 'Movie Night Club can’t play into Lounge: an admin needs to check its Mumble permissions.');
    assert.equal(sound.refusalLine('bot_cannot', 'Movie Night Club', 0, 'Lounge'), 'Movie Night Club can’t play into Lounge: an admin needs to check its Mumble permissions.');
    assert.equal(sound.refusalLine('bad_sound', 'Movie Night Club'), 'Movie Night Club couldn’t check that sound. Make it again from the file.');
    assert.equal(sound.refusalLine('too_loud', 'Movie Night Club'), 'That’s much louder than Movie Night Club makes sounds. Make it again from the file.');
    assert.equal(sound.refusalLine('store', 'Movie Night Club'), 'Movie Night Club couldn’t reach its store. Try again.');
    assert.equal(sound.emptyBoard('Matty'), 'No sounds yet. Add the first: a few seconds from a sound file, your microphone or a clip in Matty.');
    assert.equal(sound.emptyBoard('Movie Night Club'), 'No sounds yet. Add the first: a few seconds from a sound file, your microphone or a clip in Movie Night Club.');
});

// ---- The roving tab stop ----

check('arrows move within the grid, Home and End go to its ends, and anything else is not a move', () => {
    // Ten tiles, four to a row.
    assert.equal(sound.nextIndex(0, 'ArrowRight', 10, 4), 1);
    assert.equal(sound.nextIndex(9, 'ArrowRight', 10, 4), 9, 'stops at the end');
    assert.equal(sound.nextIndex(0, 'ArrowLeft', 10, 4), 0, 'stops at the start');
    assert.equal(sound.nextIndex(1, 'ArrowDown', 10, 4), 5);
    assert.equal(sound.nextIndex(7, 'ArrowDown', 10, 4), 7, 'no tile under it: stays');
    assert.equal(sound.nextIndex(6, 'ArrowUp', 10, 4), 2);
    assert.equal(sound.nextIndex(2, 'ArrowUp', 10, 4), 2);
    assert.equal(sound.nextIndex(5, 'Home', 10, 4), 0);
    assert.equal(sound.nextIndex(5, 'End', 10, 4), 9);
    assert.equal(sound.nextIndex(5, 'Enter', 10, 4), -1);
    assert.equal(sound.nextIndex(5, 'a', 10, 4), -1);
});

// ---- The trim ----

check('the trim keeps a quarter of a second to six seconds, and a handle pulled past six drags the other along', () => {
    assert.deepEqual(sound.clampTrim(0, 6000, 20000, 'out'), {inMs: 0, outMs: 6000});
    assert.deepEqual(sound.clampTrim(0, 9000, 20000, 'out'), {inMs: 3000, outMs: 9000}, 'the window slides');
    assert.deepEqual(sound.clampTrim(1000, 9000, 20000, 'in'), {inMs: 1000, outMs: 7000});
    assert.deepEqual(sound.clampTrim(5000, 5100, 20000, 'out'), {inMs: 5000, outMs: 5250}, 'never shorter than 250 ms');
    assert.deepEqual(sound.clampTrim(5000, 5100, 20000, 'in'), {inMs: 4850, outMs: 5100});
    assert.deepEqual(sound.clampTrim(-50, 3000, 20000, 'in'), {inMs: 0, outMs: 3000}, 'never before the start');
    assert.deepEqual(sound.clampTrim(0, 3000, 2000, 'out'), {inMs: 0, outMs: 2000}, 'never past the end');
    assert.deepEqual(sound.clampTrim(0, 100, 150, 'out'), {inMs: 0, outMs: 150}, 'a sound shorter than 250 ms is all of it');
});

check('the trim keeps its quarter of a second against the sound’s own ends: the other handle gives way there', () => {
    // The first loud moment in the last slice of a 1.5 s recording.
    assert.deepEqual(sound.clampTrim(1475, 2975, 1500, 'out'), {inMs: 1250, outMs: 1500});
    assert.deepEqual(sound.clampTrim(1490, 1500, 1500, 'in'), {inMs: 1250, outMs: 1500});
    assert.deepEqual(sound.clampTrim(0, 100, 20000, 'in'), {inMs: 0, outMs: 250});
    assert.deepEqual(sound.clampTrim(-40, 60, 20000, 'out'), {inMs: 0, outMs: 250});
    for (const [inMs, outMs, durationMs, moved] of [[1475, 2975, 1500, 'out'], [0, 10, 300, 'in'], [290, 300, 300, 'out'], [9990, 30000, 10000, 'out'], [-5, 2, 400, 'in']]) {
        const kept = sound.clampTrim(inMs, outMs, durationMs, moved);
        const length = kept.outMs - kept.inMs;
        assert.ok(length >= Math.min(250, durationMs) && length <= 6000, `${inMs}..${outMs} of ${durationMs} kept ${length} ms`);
        assert.ok(kept.inMs >= 0 && kept.outMs <= durationMs);
    }
});

check('a whole sound shorter than a quarter of a second ends in silence up to it, so it encodes to the 13 packets the server takes', () => {
    const click = new Float32Array(7200).fill(0.5);
    const padded = sound.atLeastShortest(click);
    assert.equal(padded.length, 12000, '250 ms at 48 kHz');
    assert.equal(padded[0], 0.5);
    assert.equal(padded[7199], 0.5);
    assert.equal(padded[7200], 0, 'silence after it');
    const long = new Float32Array(48000);
    assert.equal(sound.atLeastShortest(long), long, 'anything longer as it is');
    const encode = readFileSync(new URL('../webapp/src/soundEncode.ts', import.meta.url), 'utf8');
    assert.match(encode, /atLeastShortest\(made\.samples\)/, 'what is encoded is padded');
});

check('recording keeps its six seconds by a timer and stops when the page is hidden; a cancelled one closes the microphone at once', () => {
    const encode = readFileSync(new URL('../webapp/src/soundEncode.ts', import.meta.url), 'utf8');
    assert.match(encode, /window\.setTimeout\(stop, RECORD_MS\)/, 'not by the waveform\u2019s frames alone, which a hidden tab stops');
    assert.match(encode, /addEventListener\('visibilitychange', onHidden\)/);
    assert.match(encode, /export type Recording = \{stop: \(\) => void; cancel: \(\) => void\};/);
    assert.match(encode, /\} catch \(reason\) \{\n\s+release\(\);/, 'a throw once the microphone is open closes it');
    const add = readFileSync(new URL('../webapp/src/components/AddSound.tsx', import.meta.url), 'utf8');
    assert.match(add, /if \(!ours\(\)\) \{\n\s+started\.cancel\(\);/, 'a recording that starts after the flow was left is thrown away');
});

check('an encoder that errs is the browser’s trouble, said as such, and closed', () => {
    const encode = readFileSync(new URL('../webapp/src/soundEncode.ts', import.meta.url), 'utf8');
    const body = encode.slice(encode.indexOf('async function encode('), encode.indexOf('function closeQuietly('));
    assert.match(body, /await encoder\.flush\(\);\n\s+\} catch \{\n\s+throw new SoundMakeError\('browser'\);\n\s+\} finally \{\n\s+closeQuietly\(encoder\);\n\s+closeQuietly\(data\);/);
});

check('a length reads in tenths of a second, and a name’s characters left are counted as people count them', () => {
    assert.equal(sound.seconds(2400), '2.4 s');
    assert.equal(sound.seconds(6020), '6.0 s', 'a full trim of 301 packets');
    assert.equal(sound.nameLeft('Airhorn'), 9);
    assert.equal(sound.nameLeft('🎺🎺'), 14, 'an emoji is one character, not two');
    assert.equal(sound.tileTitle({name: 'Airhorn', owner: 'Dave', ms: 2400}), 'Airhorn · added by Dave · 2.4 s');
});

// ---- Which browsers may make a sound ----

check('only a constant bitrate counts: a config echoed without bitrateMode offers no add flow', () => {
    assert.equal(sound.encoderSupported({supported: true, config: {bitrateMode: 'constant'}}), true);
    assert.equal(sound.encoderSupported({supported: true, config: {}}), false, 'a browser that does not know bitrateMode drops it');
    assert.equal(sound.encoderSupported({supported: true, config: {bitrateMode: 'variable'}}), false);
    assert.equal(sound.encoderSupported({supported: false, config: {bitrateMode: 'constant'}}), false);
    assert.equal(sound.encoderSupported(null), false);
    assert.deepEqual(sound.ENCODER_CONFIG, {
        codec: 'opus', sampleRate: 48000, numberOfChannels: 1, bitrate: 40000, bitrateMode: 'constant',
        opus: {frameDuration: 20000, complexity: 10},
    }, 'the contract’s settings, as build/wcprobe.html has them');
});

check('a browser that cannot make a sound is told so wherever adding starts: the first step, and the empty board’s invitation', () => {
    const board = readFileSync(new URL('../webapp/src/components/Soundboard.tsx', import.meta.url), 'utf8');
    assert.match(board, /makeable === false \? <p className='vntv-snd-why'>\{UNSUPPORTED\}<\/p> : <SoundSources onPick=\{startAdd\}\/>/);
    const add = readFileSync(new URL('../webapp/src/components/AddSound.tsx', import.meta.url), 'utf8');
    assert.match(add, /if \(supported === false && !editing && step !== 'source'\) \{/, 'Record or From a clip, opened straight away, go back to the first step to say so');
});

// ---- The upload's bytes ----

const packetsOf = (name) => fixture.fixtures[name].map((packet) => new Uint8Array(Buffer.from(packet, 'base64')));

check('the fixture’s packets were made at the contract’s settings: supported, constant, 100 bytes each', () => {
    assert.equal(fixture.supported, true);
    assert.equal(fixture.bitrateMode, 'constant');
    assert.equal(sound.encoderSupported({supported: fixture.supported, config: {bitrateMode: fixture.bitrateMode}}), true);
});

check('13 to 301 packets of at most 128 bytes fit; a full 6.00 s trim is 301 packets and fits', () => {
    assert.equal(packetsOf('full6s').length, 301);
    assert.equal(packetsOf('blip').length, 13);
    for (const name of ['tone1s', 'noise2s', 'blip', 'full6s', 'loud', 'quiet']) {
        assert.equal(sound.packetsFit(packetsOf(name)), true, name);
    }
    assert.equal(sound.MAX_PACKETS, 301);
});

check('12 packets, 302 packets, an empty packet and one of 129 bytes do not fit', () => {
    const full = packetsOf('full6s');
    assert.equal(sound.packetsFit(packetsOf('blip').slice(0, 12)), false);
    assert.equal(sound.packetsFit([...full, full[0]]), false);
    assert.equal(sound.packetsFit([...packetsOf('blip'), new Uint8Array(0)]), false);
    assert.equal(sound.packetsFit([...packetsOf('blip'), new Uint8Array(129)]), false);
    assert.equal(sound.packetsFit([...packetsOf('blip'), new Uint8Array(128)]), true);
});

check('the packets section is a big-endian count, then each length and its bytes, at most 40,000 bytes', () => {
    const blip = packetsOf('blip');
    const section = sound.packetSection(blip);
    assert.equal(section.length, 2 + (13 * 102));
    assert.equal((section[0] << 8) | section[1], 13);
    assert.equal((section[2] << 8) | section[3], 100);
    assert.deepEqual(Array.from(section.slice(4, 104)), Array.from(blip[0]));
    assert.ok(sound.packetSection(packetsOf('full6s')).length <= sound.MAX_SECTION_BYTES);
    assert.equal(sound.packetSection(packetsOf('full6s')).length, 2 + (301 * 102));
});

check('the section reads back, with or without the stored header in front; anything else is not a sound', () => {
    const tone = packetsOf('tone1s');
    const section = sound.packetSection(tone);
    assert.deepEqual(sound.readPackets(section).map((packet) => Array.from(packet)), tone.map((packet) => Array.from(packet)));

    // "VNS2", added at (8 bytes), the count, 20 ms, a checksum, then the
    // packets without their count.
    const header = new Uint8Array(47);
    header.set([0x56, 0x4e, 0x53, 0x32]);
    header[12] = 0;
    header[13] = tone.length;
    header[14] = 20;
    const stored = new Uint8Array(47 + section.length - 2);
    stored.set(header);
    stored.set(section.slice(2), 47);
    assert.equal(sound.readPackets(stored).length, 51);

    assert.equal(sound.readPackets(section.slice(0, section.length - 1)), null, 'cut short');
    assert.equal(sound.readPackets(new Uint8Array([0, 0])), null, 'no packets');
});

check('base64 is standard, padded, and the same as Node’s', () => {
    for (const length of [0, 1, 2, 3, 4, 5, 100, 1327]) {
        const bytes = new Uint8Array(length).map((_, index) => (index * 37 + 11) & 255);
        assert.equal(sound.toBase64(bytes), Buffer.from(bytes).toString('base64'), `${length} bytes`);
    }
});

check('the suggested emoji are forty, the speaker first, each with its picture’s code', () => {
    assert.equal(sound.SUGGESTED_EMOJI.length, 40);
    assert.deepEqual(sound.SUGGESTED_EMOJI[0], {name: 'loud_sound', unified: '1f50a'});
    assert.equal(new Set(sound.SUGGESTED_EMOJI.map((emoji) => emoji.name)).size, 40);
    for (const emoji of sound.SUGGESTED_EMOJI) {
        assert.match(emoji.unified, /^[0-9a-f]{4,5}(-[0-9a-f]{4,5})*$/, emoji.name);
    }
});

// ---- Loudness (BS.1770-4) ----

const RATE = 48000;
const sine = (seconds, amplitude, hz = 997) => Float32Array.from({length: Math.round(seconds * RATE)}, (_, index) => amplitude * Math.sin((2 * Math.PI * hz * index) / RATE));

check('a 997 Hz sine of RMS −20 dBFS reads −20.0 LUFS, within 0.1', () => {
    close(loud.integratedLoudness(sine(3, 0.1414)), -20, 0.1, '997 Hz at 0.1414');
    close(loud.integratedLoudness(sine(3, 1)), -3.01, 0.1, 'and full scale reads −3.01, as the standard says');
});

check('a sound half silence reads as its loud half: the gates leave the silence out', () => {
    const half = new Float32Array(RATE * 4);
    half.set(sine(2, 0.1414));

    // The seventeen blocks wholly in the tone, and the three that straddle
    // its end (three quarters, half and a quarter of tone), which pass both
    // gates; the seventeen of silence do not.
    close(loud.integratedLoudness(half), -20 + (10 * Math.log10(18.5 / 20)), 0.05, 'two seconds of tone, two of nothing');
});

check('a sound shorter than 400 ms is measured as one block padded with silence', () => {
    const blip = sine(0.3, 0.1414);
    close(loud.integratedLoudness(blip), -20 + (10 * Math.log10(0.3 / 0.4)), 0.15, 'a 0.3 s blip');
});

check('silence has no loudness, and is refused rather than levelled', () => {
    assert.equal(loud.integratedLoudness(new Float32Array(RATE)), -Infinity);
    assert.equal(loud.levelled(new Float32Array(RATE)).loudness, -Infinity);
});

check('levelled brings a quiet and a loud sound to −20 LUFS, and nothing past −1 dBFS', () => {
    for (const amplitude of [0.01, 0.1414, 0.9]) {
        const made = loud.levelled(sine(2, amplitude, 440));
        close(loud.integratedLoudness(made.samples), -20, 0.3, `a 440 Hz tone at ${amplitude}`);
        assert.ok(loud.peakOf(made.samples) <= loud.CEILING + 1e-6, 'under the ceiling');
    }
});

check('a sound with one loud click is held under the ceiling by the limiter, and the rest keeps its level', () => {
    const pcm = sine(2, 0.05, 440);
    pcm[RATE] = 1;
    const made = loud.levelled(pcm);
    assert.ok(loud.peakOf(made.samples) <= loud.CEILING + 1e-6);
    close(loud.integratedLoudness(made.samples), -20, 0.5, 'the tone around it');
});

check('the limiter never lets a sample past the ceiling, and leaves a quiet sound alone', () => {
    const quiet = sine(0.5, 0.2, 440);
    assert.deepEqual(Array.from(loud.limit(quiet)), Array.from(quiet));
    const hot = sine(0.5, 3, 440);
    assert.ok(loud.peakOf(loud.limit(hot)) <= loud.CEILING + 1e-6);
});

check('the fades leave no click: a levelled sound starts and ends at nothing', () => {
    const made = loud.levelled(sine(1, 0.5, 440));
    assert.equal(made.samples[0], 0);
    assert.ok(Math.abs(made.samples[made.samples.length - 1]) < 0.01);
});

console.log(`check-sounds: ${checks} checks passed`);
