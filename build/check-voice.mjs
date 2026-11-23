// The In voice row's rules, checked without a browser: who is shown under
// which channel, in what order, and the mark beside each name.
//
// webapp/src/voice.ts imports nothing (but types), so it is transpiled with
// the project's own TypeScript, imported from a data URL and run here, as
// build/check-away.mjs does with the modules it checks. Run from the project
// root (make check).

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

const voice = await load('voice.ts');

const names = (groups) => groups.map((group) => [group.channel ? group.channel.name : null, group.people.map((person) => person.name)]);

// The snapshot exactly as the server writes it (contract/fixtures): the
// channels in the server's order, the empty ones left out, and Dave twice in
// the Lobby, once through Matty.
{
    const snapshot = JSON.parse(readFileSync(new URL('../contract/fixtures/snapshot.json', import.meta.url), 'utf8'));
    assert.deepEqual(names(voice.voiceGroups(snapshot.voice)), [
        ['Test', ['Guest']],
        ['Gaming', ['Bob']],
        ['Lobby', ['Dave', 'dave (Matty)']],
    ]);
}

// Channels to show (contract/fixtures/voice-shown.json): only the channels
// an admin listed and those under them, a listed channel under one that is
// not shown a top of its own, and nobody from the channels left out.
{
    const shown = JSON.parse(readFileSync(new URL('../contract/fixtures/voice-shown.json', import.meta.url), 'utf8'));
    assert.deepEqual(names(voice.voiceGroups(shown.voice)), [
        ['Gaming', ['dave (Matty)']],
        ['Duo', ['Bob']],
        ['Lobby', ['Victor']],
    ]);
}

const channels = [
    {id: 0, name: 'Lounge', depth: 0},
    {id: 1, name: 'Gaming', parent: 0, depth: 1},
    {id: 2, name: 'Duo', parent: 1, depth: 2, max: 2},
    {id: 3, name: 'AFK', parent: 0, depth: 1},
];

// Tree order is the server's order, whatever order the people come in.
assert.deepEqual(names(voice.voiceGroups({connected: true, channels, people: [
    {name: 'Alice', channel: 3},
    {name: 'Bob', channel: 2},
    {name: 'Carol'},
    {name: 'Dave', channel: 2},
]})), [
    ['Lounge', ['Carol']],
    ['Duo', ['Bob', 'Dave']],
    ['AFK', ['Alice']],
], 'a channel is shown only with somebody in it, the root included');

// Somebody in a channel the list does not hold is put with the root.
assert.deepEqual(names(voice.voiceGroups({connected: true, channels, people: [{name: 'Erin', channel: 9}, {name: 'Carol'}]})), [
    ['Lounge', ['Erin', 'Carol']],
]);

// A server before 4.3.0 sends no channels: one group with no name over it.
assert.deepEqual(names(voice.voiceGroups({connected: true, people: [{name: 'Victor'}, {name: 'Dave', muted: true}]})), [
    [null, ['Victor', 'Dave']],
]);
assert.deepEqual(names(voice.voiceGroups({connected: true, channels: [], people: [{name: 'Victor'}]})), [[null, ['Victor']]]);

// Nobody, or not connected, is no row at all: not connected is not "nobody".
assert.deepEqual(voice.voiceGroups(undefined), []);
assert.deepEqual(voice.voiceGroups({connected: true, channels}), []);
assert.deepEqual(voice.voiceGroups({connected: true, channels, people: []}), []);
assert.deepEqual(voice.voiceGroups({connected: false, channels, people: [{name: 'Victor'}]}), []);

// The marks: deafened before muted, since a deafened person is muted too.
assert.equal(voice.voiceMark({name: 'Bob', muted: true, deaf: true}), 'deaf');
assert.equal(voice.voiceMark({name: 'Bob', deaf: true}), 'deaf');
assert.equal(voice.voiceMark({name: 'Dave', muted: true}), 'muted');
assert.equal(voice.voiceMark({name: 'Victor'}), '');
assert.equal(voice.voicePersonLabel({name: 'Bob', muted: true, deaf: true}), 'Bob, deafened');
assert.equal(voice.voicePersonLabel({name: 'Dave', muted: true}), 'Dave, muted');
assert.equal(voice.voicePersonLabel({name: 'Victor', phone: true}), 'Victor');

console.log('check-voice: the In voice row by channel, its order, its marks and the channels shown hold');

// Talking from Matty's card in the System Console and its alert in the
// panel (webapp/src/voiceAdmin.ts), from what the server sends: the
// contract's own answer, and the ways it can stand.
{
    const source = readFileSync(new URL('../webapp/src/voiceAdmin.ts', import.meta.url), 'utf8');
    assert.deepEqual(source.match(/^import .*$/gm), ["import type {AdminVoice} from './client';"], 'voiceAdmin.ts imports nothing but types');

    const admin = await load('voiceAdmin.ts');
    const when = () => 'an hour ago';
    const texts = (view) => admin.voiceAdminLines(view, when).map((line) => [line.tone, line.text]);

    const fixture = JSON.parse(readFileSync(new URL('../contract/fixtures/admin-voice.json', import.meta.url), 'utf8'));
    assert.deepEqual(texts(fixture), [
        ['plain', 'Phones reach this server on UDP port 8445, at 203.0.113.7.'],
        ['quiet', 'The voice server is connected.'],
        ['plain', '1 person is talking from Matty through this server now.'],
        ['quiet', 'Matty opens at most 8 connections to the voice server in any two minutes, below the autoban of a Mumble server as it comes.'],
        ['quiet', 'The voice server last turned somebody away an hour ago: The voice server did not let you into that channel.'],
    ]);

    const base = {on: true, connected: true, port: 8445, open: true, addresses: ['203.0.113.7', '203.0.113.8', '198.51.100.4'], talking: 0, noBudget: true};
    assert.deepEqual(texts(base), [
        ['plain', 'Phones reach this server on UDP port 8445, at 203.0.113.7, 203.0.113.8 and 198.51.100.4.'],
        ['quiet', 'The voice server is connected.'],
        ['plain', 'Nobody is talking from Matty through this server now.'],
        ['quiet', 'The voice server’s autoban is marked as off, so Matty connects to it as often as people join.'],
    ]);
    assert.equal(texts({...base, talking: 3})[2][1], '3 people are talking from Matty through this server now.');

    const down = texts({...base, open: false, addresses: [], connected: false, problem: 'Set the address phones reach this server at.'});
    assert.deepEqual(down.slice(0, 2), [
        ['error', 'Nobody can join from Matty: Set the address phones reach this server at.'],
        ['error', 'The voice server is not connected, so nobody can join from Matty until it is.'],
    ]);
    assert.deepEqual(texts({...base, open: false, addresses: []})[0], ['quiet', 'The voice port 8445 is not open yet. Refresh in a moment.']);
    assert.deepEqual(texts({...base, on: false}), [['plain', 'Talking from Matty is off: the app shows who is in voice, and nobody can join a channel from it.']]);

    // The lines of Channels to show that name no channel, quoted as the
    // admin wrote them, whether talk is on or not.
    assert.deepEqual(texts({...base, unmatched: ['Gamng']}).at(-1), ['error', '“Gamng” in Channels to show matches no channel on the voice server.']);
    assert.deepEqual(texts({...base, unmatched: ['Gamng', 'Lobby/Duo', 'AFK']}).at(-1),
        ['error', '“Gamng”, “Lobby/Duo” and “AFK” in Channels to show match no channel on the voice server.']);
    assert.deepEqual(texts({...base, on: false, unmatched: ['Gamng']}), [
        ['plain', 'Talking from Matty is off: the app shows who is in voice, and nobody can join a channel from it.'],
        ['error', '“Gamng” in Channels to show matches no channel on the voice server.'],
    ]);
    assert.equal(texts({...base, unmatched: []}).length, texts(base).length, 'nothing to say when every line names a channel');
    assert.deepEqual(texts({...base, unmatched: ['A', 'B', 'C', 'D', 'E', 'F', 'G']}).at(-1),
        ['error', '“A”, “B”, “C”, “D”, “E” and 2 more in Channels to show match no channel on the voice server.'], 'five quoted, the rest counted');

    // The lines that name more than one channel, with the channels they
    // name: five channels at most, then a count; five lines at most, then a
    // count.
    assert.deepEqual(texts({...base, several: [{line: 'Lobby', paths: ['Lobby (the root)', '/Lobby', 'Staff/Lobby']}]}).at(-1),
        ['quiet', '“Lobby” in Channels to show matches more than one channel: Lobby (the root), /Lobby and Staff/Lobby.']);
    const rooms = ['/Room', 'Room/Room', 'Room/Room/Room', 'Room/Room/Room/Room', 'Room/Room/Room/Room/Room'];
    assert.deepEqual(texts({...base, several: [{line: 'room', paths: rooms, more: 2}]}).at(-1),
        ['quiet', '“room” in Channels to show matches more than one channel: /Room, Room/Room, Room/Room/Room, Room/Room/Room/Room, Room/Room/Room/Room/Room and 2 more.']);
    const many = ['A', 'B', 'C', 'D', 'E', 'F', 'G'].map((line) => ({line, paths: [`/${line}`, `Staff/${line}`]}));
    const manyTexts = texts({...base, unmatched: ['Gamng'], several: many});
    assert.deepEqual(manyTexts.slice(-7).map(([tone]) => tone), ['error', 'quiet', 'quiet', 'quiet', 'quiet', 'quiet', 'quiet']);
    assert.deepEqual(manyTexts.at(-1), ['quiet', '2 more lines in Channels to show match more than one channel.']);
    assert.deepEqual(texts({...base, on: false, several: [{line: 'Lobby', paths: ['/Lobby', 'Staff/Lobby']}]}).at(-1),
        ['quiet', '“Lobby” in Channels to show matches more than one channel: /Lobby and Staff/Lobby.'], 'whether talk is on or not');

    // The lines that are not used: too long, five quoted and the rest
    // counted, and the lines after the 200th; whether talk is on or not,
    // after the lines that name nothing and before the ones that name more
    // than one.
    const cut = 'Gaming/Gaming/Gaming/Gaming/Gaming/Gamin…';
    assert.deepEqual(texts({...base, tooLong: [cut]}).at(-1), ['error', `“${cut}” in Channels to show is longer than 256 characters, so it is not used.`]);
    assert.deepEqual(texts({...base, tooLong: ['A…', 'B…']}).at(-1), ['error', '“A…” and “B…” in Channels to show are longer than 256 characters, so they are not used.']);
    assert.deepEqual(texts({...base, tooLong: ['A…', 'B…', 'C…', 'D…', 'E…', 'F…', 'G…']}).at(-1),
        ['error', '“A…”, “B…”, “C…”, “D…”, “E…” and 2 more in Channels to show are longer than 256 characters, so they are not used.']);
    assert.deepEqual(texts({...base, dropped: 1}).at(-1), ['error', 'Only the first 200 lines of Channels to show are used; 1 more is not.']);
    assert.deepEqual(texts({...base, on: false, dropped: 37}), [
        ['plain', 'Talking from Matty is off: the app shows who is in voice, and nobody can join a channel from it.'],
        ['error', 'Only the first 200 lines of Channels to show are used; 37 more are not.'],
    ]);
    assert.deepEqual(texts({...base, unmatched: ['Gamng'], tooLong: [cut], dropped: 2, several: [{line: 'Lobby', paths: ['/Lobby', 'Staff/Lobby']}]}).slice(-4).map(([tone, text]) => [tone, text.slice(0, 12)]), [
        ['error', '“Gamng” in C'],
        ['error', `“${cut.slice(0, 11)}`],
        ['error', 'Only the fir'],
        ['quiet', '“Lobby” in C'],
    ]);
    assert.equal(texts({...base, tooLong: [], dropped: 0}).length, texts(base).length, 'nothing to say when every line is used');

    assert.equal(admin.voiceTalkAlertText(undefined), '');
    assert.equal(admin.voiceTalkAlertText(' '), '');
    assert.equal(admin.voiceTalkAlertText('The voice port 8445 (UDP) could not be opened: bind: address already in use'),
        'Talking from Matty is down: The voice port 8445 (UDP) could not be opened: bind: address already in use.');

    // The card is the VoiceTalkStatus setting's, in the Mumble section,
    // registered under its lowercased key.
    const manifest = JSON.parse(readFileSync(new URL('../plugin.json', import.meta.url), 'utf8'));
    const section = manifest.settings_schema.sections.find((candidate) => candidate.key === 'voice');
    assert.equal(section.title, admin.VOICE_SECTION);
    const status = section.settings.find((setting) => setting.key === 'VoiceTalkStatus');
    assert.equal(status.type, 'custom');
    const channelsShown = section.settings.find((setting) => setting.key === 'VoiceChannelsShown');
    assert.equal(channelsShown.display_name, admin.VOICE_CHANNELS_SETTING);
    assert.equal(channelsShown.type, 'longtext');
    const registry = readFileSync(new URL('../webapp/src/adminSettings.ts', import.meta.url), 'utf8');
    assert.match(registry, /\{key: 'voicetalkstatus', component: AdminVoice, showTitle: true\}/);
}

console.log('check-voice: the Talking from Matty card and its alert say what the server sends');
