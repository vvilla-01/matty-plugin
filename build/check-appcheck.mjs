// System Console > Plugins > Matty > App check: the Minimum Matty version
// editor's three choices, to and from the string the setting stores, and
// the lines the Results card says of the minimum (webapp/src/appCheckAdmin.ts),
// checked without a browser.
//
// appCheckAdmin.ts imports nothing but types, so it is transpiled with the
// project's own TypeScript, imported from a data URL and run here, as
// build/check-update.mjs does. Run from the project root (make check).

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

const source = readFileSync(new URL('../webapp/src/appCheckAdmin.ts', import.meta.url), 'utf8');
assert.deepEqual(source.match(/^import .*$/gm), ["import type {AdminAppCheck, AdminAppCheckVerdict} from './client';"], 'appCheckAdmin.ts imports nothing but types');

const card = await load('appCheckAdmin.ts');

// ---- The editor ----

// The stored string read as the server reads it: blank for none; "latest"
// in any case, spaces around it ignored; a version N.N.N, trimmed; and
// anything else under "This version or newer" exactly as typed.
const read = (stored) => card.minimumChoiceOf(stored);
assert.deepEqual(read(''), {choice: 'none', typed: ''});
assert.deepEqual(read(undefined), {choice: 'none', typed: ''});
assert.deepEqual(read('   '), {choice: 'none', typed: ''}, 'spaces alone are blank, as the server trims them');
assert.deepEqual(read('latest'), {choice: 'latest', typed: ''});
for (const stored of ['LATEST', 'Latest ', ' laTEst\t', ' latest　', 'lateſt']) {
    assert.deepEqual(read(stored), {choice: 'latest', typed: ''}, JSON.stringify(stored));
}
assert.deepEqual(read('0.1.33'), {choice: 'version', typed: '0.1.33'});
assert.deepEqual(read(' 0.1.33 '), {choice: 'version', typed: '0.1.33'}, 'a version is shown trimmed');
assert.deepEqual(read('10.0.0'), {choice: 'version', typed: '10.0.0'});

// What the server counts as none is shown as typed, under "This version or
// newer", and never replaced: reading it changes nothing.
for (const stored of ['0.1', 'v0.1.33', '0.1.33-debug', '00.1.33', 'latest release', ' 0.1 ', 'last', '﻿latest']) {
    assert.deepEqual(read(stored), {choice: 'version', typed: stored}, JSON.stringify(stored));
    assert.equal(card.minimumStored('version', read(stored).typed), stored, 'stored again as it was');
}

// The choices to the stored string.
assert.equal(card.minimumStored('none', '0.1.33'), '');
assert.equal(card.minimumStored('latest', '0.1.33'), 'latest');
assert.equal(card.minimumStored('version', '0.1.34'), '0.1.34');
assert.equal(card.minimumStored('version', ''), '', 'the box empty saves no minimum');
assert.equal(card.minimumStored('version', ' 0.1.34 '), ' 0.1.34 ', 'what is typed is stored as typed; the server trims it');

// Each choice there and back.
for (const [choice, typed] of [['none', ''], ['latest', ''], ['version', '0.1.33']]) {
    assert.deepEqual(read(card.minimumStored(choice, typed)), {choice, typed});
}

// The choices, in order, in their words.
assert.deepEqual(card.MINIMUM_CHOICES.map((option) => [option.choice, option.words]), [
    ['none', 'No minimum'],
    ['latest', 'The latest release of Matty'],
    ['version', 'This version or newer'],
]);

// What the box says under it: nothing for a version, why a value counts as
// none, and what an empty box saves.
assert.equal(card.minimumTypedNote('0.1.33'), null);
assert.equal(card.minimumTypedNote(' 0.1.33 '), null);
assert.deepEqual(card.minimumTypedNote(''), {tone: 'quiet', text: 'Saved empty, this is no minimum.'});
assert.deepEqual(card.minimumTypedNote('  '), {tone: 'quiet', text: 'Saved empty, this is no minimum.'});
assert.deepEqual(card.minimumTypedNote('0.1'), {tone: 'error', text: '“0.1” is not a version such as 0.1.33, so it counts as no minimum.'});

// The component stores through onChange and then setSaveNeeded, keeps its
// own choice, greys every control with disabled, and draws no help text.
{
    const editor = readFileSync(new URL('../webapp/src/components/AdminMinimum.tsx', import.meta.url), 'utf8');
    assert.match(editor, /onChange\(id, next\);\s*\}\s*if \(setSaveNeeded\) \{\s*setSaveNeeded\(\);/);
    assert.match(editor, /React\.useState<MinimumChoice>\(\(\) => minimumChoiceOf\(value\)\.choice\)/, 'the choice is the editor’s own, taken from the stored value when the page opens');
    assert.equal((editor.match(/disabled=\{disabled\}/g) || []).length, 2, 'the radio choices and the box');
    assert.match(editor, /type='radio'/);
    const settings = readFileSync(new URL('../webapp/src/adminSettings.ts', import.meta.url), 'utf8');
    assert.match(settings, /\{key: 'minimumappversion', component: AdminMinimum, showTitle: true\}/);
    const manifest = JSON.parse(readFileSync(new URL('../plugin.json', import.meta.url), 'utf8'));
    const setting = manifest.settings_schema.sections.flatMap((section) => section.settings).find((s) => s.key === 'MinimumAppVersion');
    assert.equal(setting.type, 'custom');
    assert.equal(setting.default, '');
}

// ---- The Results card ----

const when = (at) => `at ${at}`;
const lines = (view) => card.minimumLines(view, when).map((line) => [line.tone, line.text]);
const base = {check: 'report', people: []};
const byWordLine = 'The minimum goes by what each Matty says of itself, so a changed copy can say any version. With Check the Matty app at “Check, and refuse a Matty that fails”, it goes by what the phone proved.';
const provedLine = 'The minimum goes by what each phone proved in its check.';
const refused = (minimum) => `A Matty older than ${minimum}, or one whose version cannot be read, is refused by this plugin's API everywhere but the few places it needs to show why.`;

// No minimum: nothing is said of one.
assert.deepEqual(lines(base), []);
assert.deepEqual(lines({...base, check: 'require'}), []);

// A version.
assert.deepEqual(lines({...base, minimum: '0.1.33', minimumFrom: 'setting', byWord: true}), [
    ['plain', 'The oldest Matty this server answers: 0.1.33.'],
    ['plain', refused('0.1.33')],
    ['plain', byWordLine],
]);
assert.deepEqual(lines({...base, check: 'require', minimum: '0.1.33', minimumFrom: 'setting'}), [
    ['plain', 'The oldest Matty this server answers: 0.1.33.'],
    ['plain', refused('0.1.33')],
    ['plain', provedLine],
]);
assert.deepEqual(lines({...base, check: 'off', minimum: '0.1.33', minimumFrom: 'setting', byWord: true})[2], ['plain', byWordLine]);

// The latest, known, and the last trouble under it when the last look failed.
const latest = {...base, minimum: '0.1.33', minimumFrom: 'latest', byWord: true, latest: {version: '0.1.33', lookedAt: 5}};
assert.deepEqual(lines(latest), [
    ['plain', 'The oldest Matty this server answers: 0.1.33, the latest release. Last looked: at 5.'],
    ['plain', refused('0.1.33')],
    ['plain', byWordLine],
]);
assert.deepEqual(lines({...latest, latest: {...latest.latest, trouble: 'GitHub could not be reached.', troubleAt: 9}}), [
    ['plain', 'The oldest Matty this server answers: 0.1.33, the latest release. Last looked: at 5.'],
    ['error', 'at 9: GitHub could not be reached.'],
    ['plain', refused('0.1.33')],
    ['plain', byWordLine],
]);
assert.deepEqual(lines({...latest, check: 'require', byWord: false}).at(-1), ['plain', provedLine]);
assert.deepEqual(lines({...latest, latest: {version: '0.1.33'}})[0], ['plain', 'The oldest Matty this server answers: 0.1.33, the latest release.']);

// The latest, not known: no minimum in force, and nothing said of what it
// goes by; the last trouble under it when there is one.
const unknown = {...base, minimumFrom: 'latest', latest: {}};
assert.deepEqual(lines(unknown), [
    ['plain', 'Minimum Matty version is the latest release, and this server does not know it yet, so no minimum is in force.'],
]);
assert.deepEqual(lines({...unknown, latest: {lookedAt: 5, trouble: 'GitHub answered 503', troubleAt: 5}}), [
    ['plain', 'Minimum Matty version is the latest release, and this server does not know it yet, so no minimum is in force.'],
    ['error', 'at 5: GitHub answered 503.'],
]);
assert.deepEqual(lines({...unknown, latest: undefined}).length, 1, 'a card without latest reads as nothing known');
assert.deepEqual(lines({...unknown, latest: {trouble: 'GitHub says Matty has no release.'}})[1], ['error', 'GitHub says Matty has no release.']);

// Check now is offered only while the setting is the latest release.
assert.equal(card.latestCheckOffered(latest), true);
assert.equal(card.latestCheckOffered(unknown), true);
assert.equal(card.latestCheckOffered({...base, minimum: '0.1.33', minimumFrom: 'setting'}), false);
assert.equal(card.latestCheckOffered(base), false);
assert.equal(card.latestCheckOffered(null), false);

// Each row: the version kept with the verdict beside the version code.
assert.equal(card.verdictVersion({said: '0.1.33', versionCode: 34}), 'Matty 0.1.33 (34)');
assert.equal(card.verdictVersion({said: '0.1.33'}), 'Matty 0.1.33');
assert.equal(card.verdictVersion({versionCode: 34}), 'app version code 34');
assert.equal(card.verdictVersion({}), '');

// The card draws these, and Check now posts to its own route.
{
    const component = readFileSync(new URL('../webapp/src/components/AdminAppCheck.tsx', import.meta.url), 'utf8');
    assert.match(component, /minimumLines\(view, \(at\) => postedWhen\(at, zone\)\)/);
    assert.match(component, /verdictVersion\(row\)/);
    assert.match(component, /checkAdminAppLatest\(\)/);
    assert.match(component, /latestCheckOffered\(view\)/);
    const client = readFileSync(new URL('../webapp/src/client.ts', import.meta.url), 'utf8');
    assert.match(client, /export const checkAdminAppLatest = \(\): Promise<AdminAppCheck> => requestJSON<AdminAppCheck>\('\/api\/v1\/admin\/appcheck\/latest', \{method: 'POST'\}\);/);
}

console.log('check-appcheck: the Minimum Matty version editor and the Results card’s lines hold');
