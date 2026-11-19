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

console.log('check-voice: the In voice row by channel, its order and its marks hold');

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
    const registry = readFileSync(new URL('../webapp/src/adminSettings.ts', import.meta.url), 'utf8');
    assert.match(registry, /\{key: 'voicetalkstatus', component: AdminVoice, showTitle: true\}/);
}

console.log('check-voice: the Talking from Matty card and its alert say what the server sends');
