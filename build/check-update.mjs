// The Updates card in the System Console, its Install, and the update alert
// in the panel (webapp/src/updateAdmin.ts), checked without a browser, from
// what the server sends: the contract's own answers, and the ways it can
// stand.
//
// updateAdmin.ts imports nothing but types, so it is transpiled with the
// project's own TypeScript, imported from a data URL and run here, as
// build/check-voice.mjs does. Run from the project root (make check).

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

const fixture = (name) => JSON.parse(readFileSync(new URL(`../contract/fixtures/${name}`, import.meta.url), 'utf8'));

const source = readFileSync(new URL('../webapp/src/updateAdmin.ts', import.meta.url), 'utf8');
assert.deepEqual(source.match(/^import .*$/gm), ["import type {AdminUpdate} from './client';"], 'updateAdmin.ts imports nothing but types');

const update = await load('updateAdmin.ts');
const when = (at) => `at ${at}`;
const texts = (view) => update.updateLines(view, when).map((line) => [line.tone, line.text]);

// The card as the server answers it (contract/fixtures/admin-update.json):
// on, a newer release that can be installed from here, phones in voice, and
// the last bundle's trouble.
{
    const view = fixture('admin-update.json');
    assert.equal(update.updateHead(view), `This server runs Matty Plugin ${view.running}.`);
    assert.deepEqual(texts(view), [
        ['plain', 'Once a day it looks on GitHub for a newer release, and tells the system admins.'],
        ['plain', `Matty Plugin ${view.latest} is out. Last looked: at ${view.lookedAt}.`],
        ['error', `at ${view.troubleAt}: ${view.trouble}`],
    ]);
    assert.equal(update.updatePageLinkable(view.url), true);
    assert.equal(update.installOffered(view), true);
    assert.equal(update.installButtonText(view.latest), `Install ${view.latest}`);
    assert.deepEqual(update.installQuestion(view.latest, view.inVoice), [
        `Install Matty Plugin ${view.latest}? The plugin restarts, which takes a few seconds.`,
        `${view.inVoice} phones are in voice through Matty and will be dropped.`,
    ]);
}

assert.equal(fixture('admin-update.json').platform, 'linux-amd64', 'the card names this server’s platform');
const base = {enabled: true, running: '4.4.0', platform: 'linux-amd64', newer: false};

// Off, and before the first look.
assert.deepEqual(texts({...base, enabled: false}), [
    ['plain', 'Look for updates is off: nothing is asked of GitHub about Matty Plugin’s releases.'],
    ['quiet', 'It has not looked.'],
]);
assert.deepEqual(texts(base), [
    ['plain', 'Once a day it looks on GitHub for a newer release, and tells the system admins.'],
    ['quiet', 'It has not looked yet.'],
]);

// No release, the newest, and one that is out.
assert.deepEqual(texts({...base, lookedAt: 5})[1], ['quiet', 'No release was found. Last looked: at 5.']);
assert.deepEqual(texts({...base, lookedAt: 5, latest: '4.4.0'})[1], ['quiet', 'This is the newest release. Last looked: at 5.']);
assert.deepEqual(texts({...base, lookedAt: 5, latest: '4.5.0', newer: true})[1], ['plain', 'Matty Plugin 4.5.0 is out. Last looked: at 5.']);

// What keeps it from being installed from here, every reason in the
// server's order, in the words of the bundle route's refusals; and then no
// Install.
const out = {...base, lookedAt: 5, latest: '4.5.0', newer: true};
const blocked = {...out, install: {ready: false, missing: ['unsigned', 'asset', 'uploads']}};
assert.deepEqual(texts(blocked).slice(2), [
    ['plain', 'This copy of Matty Plugin is not a signed release, so it does not install releases. Install 4.5.0 by hand from its page.'],
    ['plain', 'Matty Plugin 4.5.0 has no bundle for this server’s platform (linux-amd64).'],
    ['plain', 'Mattermost does not take plugin uploads here: in System Console > Plugins > Plugin Management, Enable Plugins and Enable Plugin Uploads must be on and Require Plugin Signature off.'],
]);
assert.equal(update.installOffered(blocked), false);
assert.deepEqual(texts({...out, install: {ready: false, missing: ['something new']}}).length, 2, 'a reason this panel does not know is not shown as one');
assert.deepEqual(texts({...out, install: {ready: true}}).length, 2, 'ready: nothing keeps it');
assert.equal(update.installOffered({...out, install: {ready: true}}), true);
assert.equal(update.installOffered({...out, install: {ready: true}, enabled: false}), false, 'not while the setting is off');
assert.equal(update.installOffered({...out}), false, 'not without the server saying it can');
assert.equal(update.installOffered(null), false);
assert.deepEqual(texts({...blocked, enabled: false}).length, 2, 'the reasons are about installing, and said only while on');

// Install, step by step: asked first, with the phones in voice; the
// bundle, then Mattermost; done; and a failure in the sentence the plugin or
// Mattermost gave, with the buttons back.
assert.deepEqual(update.installQuestion('4.5.0', 0), ['Install Matty Plugin 4.5.0? The plugin restarts, which takes a few seconds.']);
assert.deepEqual(update.installQuestion('4.5.0', undefined), ['Install Matty Plugin 4.5.0? The plugin restarts, which takes a few seconds.']);
assert.equal(update.installQuestion('4.5.0', 1)[1], '1 phone is in voice through Matty and will be dropped.');
assert.equal(update.installQuestion('4.5.0', 3)[1], '3 phones are in voice through Matty and will be dropped.');
{
    const step = (state, ...events) => events.reduce((s, e) => update.installNext(s, e), state);
    const idle = update.INSTALL_IDLE;
    const asking = step(idle, {kind: 'press'});
    assert.deepEqual(asking, {step: 'asking'});
    assert.deepEqual(update.installButtons(asking), {install: false, confirm: true, disabled: false});
    assert.deepEqual(step(asking, {kind: 'cancel'}), idle);
    assert.deepEqual(update.installButtons(idle), {install: true, confirm: false, disabled: false});

    const fetching = step(asking, {kind: 'confirm'});
    assert.equal(update.installStepText(fetching, '4.5.0'), 'Getting and checking the bundle…');
    assert.deepEqual(update.installButtons(fetching), {install: true, confirm: false, disabled: true});
    assert.deepEqual(step(fetching, {kind: 'press'}), fetching, 'a press while a step runs does nothing');
    assert.deepEqual(step(fetching, {kind: 'cancel'}), fetching);

    const handing = step(fetching, {kind: 'fetched'});
    assert.equal(update.installStepText(handing, '4.5.0'), 'Handing it to Mattermost…');
    assert.deepEqual(update.installButtons(handing).disabled, true);

    const done = step(handing, {kind: 'handed'});
    assert.equal(update.installStepText(done, '4.5.0'), 'Matty Plugin 4.5.0 is installed. Reload this page to see its panel.');
    assert.deepEqual(update.installButtons(done), {install: false, confirm: false, disabled: true});
    assert.deepEqual(step(done, {kind: 'press'}), done);

    for (const from of [fetching, handing]) {
        const failed = step(from, {kind: 'failed', error: 'A bundle was asked for less than a minute ago. Try again in a minute.'});
        assert.equal(update.installStepText(failed, '4.5.0'), 'A bundle was asked for less than a minute ago. Try again in a minute.');
        assert.deepEqual(update.installButtons(failed), {install: true, confirm: false, disabled: false}, 'the buttons come back');
        assert.deepEqual(step(failed, {kind: 'press'}), {step: 'asking'});
    }
    assert.equal(update.installStepText(step(fetching, {kind: 'failed', error: ' '}), '4.5.0'), 'It could not be installed.');
    assert.deepEqual(step(idle, {kind: 'fetched'}), idle, 'an answer that belongs to no step is dropped');
    assert.equal(update.installStepText(idle, '4.5.0'), '');
}

// What the bundle route answered is uploaded only when it is 200, holds at
// least one byte and says a version, and that version is the one the admin
// was asked about; otherwise nothing is uploaded and the card is read again.
assert.equal(update.bundleProblem(200, 12, '4.5.0', '4.5.0'), '');
for (const [status, bytes, version] of [[200, 0, '4.5.0'], [204, 12, '4.5.0'], [200, 12, ''], [200, 12, 'v4.5.0'], [200, 12, '4.5'], [200, 12, '04.5.0'], [206, 12, '4.5.0']]) {
    assert.equal(update.bundleProblem(status, bytes, version, '4.5.0'), 'The bundle did not arrive whole. Try again in a moment.', `${status} ${bytes} ${version}`);
}
assert.equal(update.bundleProblem(200, 12, '4.6.0', '4.5.0'), 'A newer version, 4.6.0, was found meanwhile. Press Install again in a minute.', 'the bundle refused has spent the minute');

// The Install button names the version a press installs: while a step runs,
// the one being installed; otherwise, a failed Install included, the card's
// newest, which the next press asks about.
{
    const step = (state, ...events) => events.reduce((s, e) => update.installNext(s, e), state);
    const fetching = step(update.INSTALL_IDLE, {kind: 'press'}, {kind: 'confirm'});
    const handing = step(fetching, {kind: 'fetched'});
    const failed = step(fetching, {kind: 'failed', error: 'A newer version, 4.6.0, was found meanwhile. Press Install again in a minute.'});
    assert.equal(update.installButtonVersion(fetching, '4.5.0', '4.6.0'), '4.5.0');
    assert.equal(update.installButtonVersion(handing, '4.6.0', '4.6.0'), '4.6.0');
    assert.equal(update.installButtonVersion(failed, '4.5.0', '4.6.0'), '4.6.0', 'after a failure, the version the next press installs');
    assert.equal(update.installButtonVersion(update.INSTALL_IDLE, '4.5.0', '4.6.0'), '4.6.0');
    assert.equal(update.installButtonVersion(fetching, '', '4.6.0'), '4.6.0');
    assert.deepEqual(update.installQuestion('4.6.0', 0), ['Install Matty Plugin 4.6.0? The plugin restarts, which takes a few seconds.'], 'and the press asks about that one');
    const card = readFileSync(new URL('../webapp/src/components/AdminUpdate.tsx', import.meta.url), 'utf8');
    assert.match(card, /\{installButtonText\(installButtonVersion\(install, installed, version\)\)\}/, 'the card names the button so');
    assert.doesNotMatch(card, /installButtonText\(installed \|\| version\)/);
}

// A failed Install says its sentence once: the card's trouble line with the
// same words is left out while the step line shows it, and comes back
// otherwise.
{
    const failedView = {...base, lookedAt: 5, latest: '4.5.0', newer: true, install: {ready: true}, trouble: 'GitHub could not be reached for the bundle.', troubleAt: 6};
    assert.deepEqual(update.updateLines(failedView, when, 'GitHub could not be reached for the bundle.').map((line) => line.tone), ['plain', 'plain']);
    assert.deepEqual(update.updateLines(failedView, when, 'GitHub could not be reached for the bundle').map((line) => line.tone), ['plain', 'plain'], 'the same sentence, however it ends');
    assert.deepEqual(update.updateLines(failedView, when, 'Mattermost did not take the bundle (500).').at(-1), {tone: 'error', text: 'at 6: GitHub could not be reached for the bundle.'});
    assert.deepEqual(update.updateLines(failedView, when).at(-1), {tone: 'error', text: 'at 6: GitHub could not be reached for the bundle.'});
}

// The upload is Mattermost's own, as the console makes it: the fields
// plugin and force, force "true", the panel's usual headers with the CSRF
// token, and no Content-Type of its own (the browser writes the multipart
// one).
{
    const client = readFileSync(new URL('../webapp/src/client.ts', import.meta.url), 'utf8');
    const upload = client.slice(client.indexOf('export async function uploadPluginBundle'));
    const body = upload.slice(0, upload.indexOf('\n}\n'));
    assert.match(body, /form\.append\('plugin', bundle\.file, bundle\.name\);/);
    assert.match(body, /form\.append\('force', 'true'\);/);
    assert.match(body, /fetch\('\/api\/v4\/plugins', \{\s*method: 'POST',\s*credentials: 'same-origin',\s*headers: baseHeaders\(\),\s*body: form,\s*\}\)/);
    assert.doesNotMatch(body, /Content-Type/);
    const bundle = client.slice(client.indexOf('export async function fetchUpdateBundle'));
    const fetching = bundle.slice(0, bundle.indexOf('\n}\n'));
    assert.match(fetching, /^export async function fetchUpdateBundle\(asked: string\)/);
    assert.match(fetching, /method: 'POST',\s*credentials: 'same-origin',\s*headers: baseHeaders\(\),/);
    assert.match(fetching, /const problem = bundleProblem\(response\.status, file\.size, version, asked\);\s*if \(problem\) \{\s*throw new Error\(problem\);\s*\}/);
    assert.ok(fetching.indexOf('bundleProblem(') < fetching.lastIndexOf('return {file, name, version};'), 'the answer is held before it is handed on');
    const card = readFileSync(new URL('../webapp/src/components/AdminUpdate.tsx', import.meta.url), 'utf8');
    assert.match(card, /fetchUpdateBundle\(version\)\.then\(\(bundle\) => \{[\s\S]*?return uploadPluginBundle\(bundle\);/, 'the card asks for the version it asked the admin about, and uploads only what came back');
    assert.match(card, /\.catch\(\(reason: unknown\) => \{[\s\S]*?load\(\);/, 'a failure reads the card again');
    assert.match(client, /headers\['X-CSRF-Token'\] = token;/);
}

// The last trouble, in the server's words, as a sentence with when.
assert.deepEqual(texts({...base, lookedAt: 5, trouble: 'GitHub could not be reached.', troubleAt: 5}).at(-1), ['error', 'at 5: GitHub could not be reached.']);
assert.deepEqual(texts({...base, lookedAt: 5, trouble: 'GitHub answered with status 500'}).at(-1), ['error', 'GitHub answered with status 500.']);

// The alert in the panel: the newer version, linked only to a release's
// page on GitHub, and nothing when there is none. "me"'s updateAlert has the
// shape of /access's update, whose answer the contract holds.
{
    const access = fixture('access-update.json');
    assert.equal(access.update.install, true, '/access says Matty may offer Install');
    const alert = update.updateAlertText(access.update);
    assert.deepEqual(alert, {text: `Matty Plugin ${access.update.version} is out.`, url: `https://github.com/vvilla-01/matty-plugin/releases/tag/v${access.update.version}`, linked: true});
}
assert.equal(update.updateAlertText(undefined), null);
assert.equal(update.updateAlertText({version: '', url: ''}), null);
for (const url of ['https://example.org/vvilla-01/matty-plugin/', 'http://github.com/vvilla-01/matty-plugin/releases/tag/v4.5.0', 'https://github.com/vvilla-01/matty-plugin-evil/x', 'javascript:alert(1)', 'https://github.com/vvilla-01/matty-plugin/a"b']) {
    assert.equal(update.updateAlertText({version: '4.5.0', url}).linked, false, url);
}

console.log('check-update: the Updates card, its reasons, its Install, its trouble and the panel’s alert hold');
