// The clip player's rules (design/3.3.0.md), checked without a browser: how
// long a clip is and where it has got to, the timeline running from the
// clip's own start to its own end, the clock under the picture, where a press
// on the scrubber lands, what a screen reader hears of it, what each key
// does, and that Download is offered in one place only, and still offered
// when the file will not play. A few guards on the source as well, for what
// only React or the stylesheet can do: the scrubber out of play over the
// loading screen, the clip's plate never fading for its controls, and a
// press slid off a control not counting as a click on the picture.
//
// webapp/src/clipLook.ts imports only clipLogic.ts, which imports nothing, so
// both are transpiled with the project's own TypeScript and clipLook.ts is
// imported from a data URL with clipLogic.ts standing in for its import, as
// check-away.mjs does with tagSearch.ts. Run from the project root (make
// check-webapp).

import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createRequire} from 'node:module';

const require = createRequire(import.meta.url);
const ts = require('../webapp/node_modules/typescript');

function transpile(file) {
    const source = readFileSync(new URL(`../webapp/src/${file}`, import.meta.url), 'utf8');

    return ts.transpileModule(source, {
        compilerOptions: {module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2020},
        fileName: file,
    }).outputText;
}

const dataURL = (code) => `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`;

// clipLook.ts with each of its imports swapped for a module's own source. A
// path with no stand-in fails rather than being left to resolve against
// nothing.
async function loadWith(file, standIns) {
    const swapped = transpile(file).replace(/from '(\.[^']*)'/g, (_, path) => {
        assert.ok(path in standIns, `${file} imports ${path}, and there is no stand-in for it`);

        return `from '${dataURL(standIns[path])}'`;
    });

    return import(dataURL(swapped));
}

const look = await loadWith('clipLook.ts', {'./clipLogic': transpile('clipLogic.ts')});

let checks = 0;
function check(name, run) {
    try {
        run();
        checks++;
    } catch (error) {
        console.error(`check-player: ${name}`);
        throw error;
    }
}

const read = (path) => readFileSync(new URL(`../webapp/src/${path}`, import.meta.url), 'utf8');

// ---- How long, and where ----

check('a clip is as long as the server says, and no longer than its file past the lead-in', () => {
    assert.equal(look.clipLength(45000, 0), 45, 'the file not known yet');
    assert.equal(look.clipLength(45000, 46.1), 45, 'a file that runs on past the clip');
    assert.equal(look.clipLength(45000, 44.2), 44.2, 'a file that ends early');
    assert.equal(look.clipLength(0, 30), 30, 'no length from the server: the file’s');
    assert.equal(look.clipLength(0, 0), 0, 'nothing known');
    assert.equal(look.clipLength(45000, NaN), 45);
    assert.equal(look.clipLength(45000, Infinity), 45, 'a file that never says');
});

check('the file’s length is counted from the clip’s own start', () => {
    assert.equal(look.fileSecondsOf(47.4, 1.4), 46);
    assert.equal(look.fileSecondsOf(1, 1.4), 0, 'never below nothing');
    assert.equal(look.fileSecondsOf(NaN, 1.4), 0, 'before the file says');
    assert.equal(look.fileSecondsOf(Infinity, 0), 0);
});

check('the lead-in is off the timeline, and so is anything past the clip’s own end', () => {
    assert.equal(look.clipAt(0, 1.4, 45), 0, 'the keyframe before the start');
    assert.equal(look.clipAt(1.4, 1.4, 45), 0, 'the clip’s own start');
    assert.ok(Math.abs(look.clipAt(13.4, 1.4, 45) - 12) < 1e-9);
    assert.equal(look.clipAt(47, 1.4, 45), 45, 'the file runs on; the clip does not');
    assert.equal(look.clipAt(60, 0, 0), 60, 'only the start holds it while the length is unknown');
});

check('a seek lands inside the clip, start to end, and nowhere while its length is unknown', () => {
    assert.equal(look.seekTarget(-3, 45), 0);
    assert.equal(look.seekTarget(12.5, 45), 12.5);
    assert.equal(look.seekTarget(50, 45), 45);
    assert.equal(look.seekTarget(12, 0), 0);
});

check('the end is the clip’s own end, within a frame or so, and a clip of no length has none', () => {
    assert.equal(look.atEnd(45, 45), true);
    assert.equal(look.atEnd(44.96, 45), true, 'the picture stops on a frame');
    assert.equal(look.atEnd(44.9, 45), false);
    assert.equal(look.atEnd(0, 0), false);
});

check('a playing clip stops at its own end, not a frame before it', () => {
    assert.equal(look.ranPastEnd(44.97, 45), false);
    assert.equal(look.ranPastEnd(45, 45), true);
    assert.equal(look.ranPastEnd(46, 45), true);
    assert.equal(look.ranPastEnd(10, 0), false, 'no end while the length is unknown');
});

check('a clip from the server, played through: lead-in skipped, stopped at its own end, Replay from its start', () => {
    // "that jump" in the lab: a lead-in of 1.4 s, 45 s of clip, a file that
    // runs a second past it.
    const lead = 1.4;
    const length = look.clipLength(45000, look.fileSecondsOf(47.4, lead));
    assert.equal(length, 45);
    assert.equal(look.clipAt(lead, lead, length), 0, 'where it opens');
    assert.equal(look.ranPastEnd(look.clipAt(46.2, lead, 0), length), false);
    assert.equal(look.ranPastEnd(look.clipAt(46.4, lead, 0), length), true, 'stopped here');
    assert.equal(look.atEnd(look.clipAt(46.4, lead, length), length), true, 'and its button is Replay');
    assert.equal(lead + look.seekTarget(0, length), lead, 'Replay goes back to the clip’s start, not the file’s');
});

// ---- The scrubber ----

check('the scrubber’s red runs from nothing to the whole track', () => {
    assert.equal(look.shareOf(0, 45), 0);
    assert.equal(look.shareOf(22.5, 45), 0.5);
    assert.equal(look.shareOf(45, 45), 1);
    assert.equal(look.shareOf(50, 45), 1);
    assert.equal(look.shareOf(10, 0), 0, 'nothing while the length is unknown');
});

check('what has come of the file is counted from the clip’s own start', () => {
    assert.equal(look.loadedShare(1.4, 1.4, 45), 0);
    assert.equal(look.loadedShare(23.9, 1.4, 45), 0.5);
    assert.equal(look.loadedShare(47.4, 1.4, 45), 1, 'the run-on is not more than all of it');
    assert.equal(look.loadedShare(1, 1.4, 45), 0);
    assert.equal(look.loadedShare(10, 0, 0), 0);
});

check('a press on the track lands where it is, and a drag past either end stays at that end', () => {
    // A track 300 px wide starting 50 px in, on a clip of 45 s.
    assert.equal(look.secondsAt(50, 50, 300, 45), 0);
    assert.equal(look.secondsAt(200, 50, 300, 45), 22.5);
    assert.equal(look.secondsAt(350, 50, 300, 45), 45);
    assert.equal(look.secondsAt(10, 50, 300, 45), 0, 'dragged off the left');
    assert.equal(look.secondsAt(900, 50, 300, 45), 45, 'dragged off the right');
    assert.equal(look.secondsAt(200, 50, 0, 45), 0, 'a track with no width yet');
    assert.equal(look.secondsAt(200, 50, 300, 0), 0, 'a clip of unknown length');
});

check('the scrubber’s own keys: arrows five seconds, Page Up and Down ten, Home and End the ends', () => {
    assert.deepEqual(look.scrubberKey('ArrowLeft', false), {by: -5});
    assert.deepEqual(look.scrubberKey('ArrowDown', false), {by: -5});
    assert.deepEqual(look.scrubberKey('ArrowRight', false), {by: 5});
    assert.deepEqual(look.scrubberKey('ArrowUp', false), {by: 5});
    assert.deepEqual(look.scrubberKey('PageDown', false), {by: -10});
    assert.deepEqual(look.scrubberKey('PageUp', false), {by: 10});
    assert.deepEqual(look.scrubberKey('Home', false), {to: 'start'});
    assert.deepEqual(look.scrubberKey('End', false), {to: 'end'});
    assert.equal(look.scrubberKey('ArrowLeft', true), null, 'with Ctrl, Command or Alt: the browser’s');
    assert.equal(look.scrubberKey(' ', false), null, 'Space goes on to the player');
    assert.equal(look.scrubberKey('Tab', false), null);
});

// ---- The clock ----

check('the clock reads where the clip is, rounded down, over its length', () => {
    const at = (seconds, length) => {
        const clock = look.clipClock(seconds, length);

        return `${clock.at} / ${clock.length}`;
    };
    assert.equal(at(0, 45), '0:00 / 0:45');
    assert.equal(at(12.9, 45), '0:12 / 0:45', 'a second only once it has started');
    assert.equal(at(44.9, 45), '0:44 / 0:45');
    assert.equal(at(45, 45), '0:45 / 0:45');
    assert.equal(at(75.2, 120), '1:15 / 2:00');
    assert.equal(at(0, 0), '0:00 / 0:00', 'before anything is known');
});

check('at its end the clock reads the clip’s length, never a second short of it', () => {
    const clock = look.clipClock(44.6, 44.6);
    assert.deepEqual(clock, {at: '0:45', length: '0:45'});
    assert.equal(look.clipClock(44.58, 44.6).at, '0:45', 'within a frame of the end');
    assert.equal(look.clipClock(43.6, 44.6).at, '0:43', 'a second before it, as it is');
});

check('the scrubber says the clock in words, which is what a screen reader hears', () => {
    assert.equal(look.clockWords(12.9, 45), '0:12 of 0:45');
    assert.equal(look.clockWords(44.6, 44.6), '0:45 of 0:45');
});

check('the focused scrubber says nothing new while the clip plays on, and where it is once paused, dragged or moved', () => {
    // Not focused: where the clip is, every frame.
    assert.equal(look.heardAt(12.4, null, false), 12.4);

    // Focused at 3 s and played on to 9 s: still 3 s, so a screen reader
    // does not read the time out every second over the clip's sound.
    assert.equal(look.heardAt(9, 3, false), 3);

    // Paused, or dragged: where it is.
    assert.equal(look.heardAt(9, 3, true), 9);

    // The reader's own key moved it to 14 s while it plays: the player holds
    // 14, which is then what is said.
    assert.equal(look.heardAt(14.2, 14, false), 14);
});

check('the loading screen takes the scrubber out of play, not only out of sight', () => {
    const tile = read('components/ClipTile.tsx');
    assert.match(tile, /const asleep = media\.loading === 'first';/);
    assert.match(tile, /tabIndex=\{asleep \? -1 : 0\}/, 'no Tab stop over the loading screen');
    const styles = read('styles.ts');
    const rule = styles.match(/\.vntv-cliptile\[data-loading="first"\] \.vntv-hud \.vntv-clip-scrub \{([^}]*)\}/);
    assert.ok(rule, 'the hiding rule, one class above the reveals');
    assert.match(rule[1], /visibility: hidden;/);
    assert.match(rule[1], /pointer-events: none;/);
});

check('a clip’s LOADING and CAN’T PLAY do not fade for its controls: it has no switch to make room for', () => {
    const styles = read('styles.ts');
    for (const reveal of ['\\.vntv-hud\\[data-awake="true"\\]', '\\.vntv-hud :focus-visible']) {
        const fade = new RegExp(`\\n\\.vntv-screen:has\\(> ${reveal}\\) \\.vntv-plate \\{\\s*opacity: 0\\.16;`);
        const kept = new RegExp(`\\n\\.vntv-cliptile \\.vntv-screen:has\\(> ${reveal}\\) \\.vntv-plate \\{\\s*opacity: 1;`);
        assert.match(styles, fade, 'the stream tile’s fade, unchanged');
        assert.match(styles, kept, 'undone in a clip');
    }
});

// ---- What it shows ----

check('the player’s state: a failure, then loading, then the end, then paused or playing', () => {
    const state = (now) => look.playerState({failed: false, loading: false, ended: false, paused: false, ...now});
    assert.equal(state({}), 'playing');
    assert.equal(state({paused: true}), 'paused');
    assert.equal(state({paused: true, ended: true}), 'ended');
    assert.equal(state({paused: true, ended: true, loading: true}), 'loading');
    assert.equal(state({loading: true, failed: true}), 'failed');
});

check('what it waits for: the file before its length while paused, its first frame or more of it while playing', () => {
    const wait = (now) => look.fileWait({failed: false, cannotPlay: false, paused: true, metadata: false, firstFrame: false, stalled: false, ...now});
    assert.equal(wait({}), 'first', 'opened, and nothing has come');
    assert.equal(wait({metadata: true}), '', 'its length is known: the picture is its poster');
    assert.equal(wait({paused: false, metadata: true}), 'first', 'play pressed before the first frame');
    assert.equal(wait({paused: false, metadata: true, firstFrame: true}), '');
    assert.equal(wait({paused: false, metadata: true, firstFrame: true, stalled: true}), 'stalled');
    assert.equal(wait({failed: true}), '', 'a failure is said, not waited on');
    assert.equal(wait({cannotPlay: true}), '', 'nor a browser that cannot play it');
});

// ---- The keys ----

check('Space or K plays and pauses, but Space on a button presses the button', () => {
    const where = {modified: false, onButton: false, onSlider: false};
    assert.deepEqual(look.playerKey(' ', where), {act: 'toggle'});
    assert.deepEqual(look.playerKey('Spacebar', where), {act: 'toggle'}, 'an old browser’s name for it');
    assert.deepEqual(look.playerKey('k', where), {act: 'toggle'});
    assert.deepEqual(look.playerKey('K', where), {act: 'toggle'});
    assert.equal(look.playerKey(' ', {...where, onButton: true}), null);
    assert.deepEqual(look.playerKey('k', {...where, onButton: true}), {act: 'toggle'}, 'K is not a button’s key');
    assert.deepEqual(look.playerKey(' ', {...where, onSlider: true}), {act: 'toggle'}, 'Space on the scrubber or the volume');
});

check('the arrows go back and on five seconds, J and L ten, but an arrow on a slider moves the slider', () => {
    const where = {modified: false, onButton: false, onSlider: false};
    assert.deepEqual(look.playerKey('ArrowLeft', where), {act: 'seek', by: -5});
    assert.deepEqual(look.playerKey('ArrowRight', where), {act: 'seek', by: 5});
    assert.deepEqual(look.playerKey('ArrowRight', {...where, onButton: true}), {act: 'seek', by: 5});
    assert.equal(look.playerKey('ArrowLeft', {...where, onSlider: true}), null);
    assert.equal(look.playerKey('ArrowUp', where), null, 'up and down are the page’s');
    assert.deepEqual(look.playerKey('j', where), {act: 'seek', by: -10});
    assert.deepEqual(look.playerKey('L', where), {act: 'seek', by: 10});
});

check('M mutes and F is full screen', () => {
    const where = {modified: false, onButton: false, onSlider: false};
    assert.deepEqual(look.playerKey('m', where), {act: 'mute'});
    assert.deepEqual(look.playerKey('M', where), {act: 'mute'});
    assert.deepEqual(look.playerKey('f', where), {act: 'fullscreen'});
    assert.deepEqual(look.playerKey('F', where), {act: 'fullscreen'});
});

check('nothing with Ctrl, Command or Alt, or while composing, and nothing that is not the player’s', () => {
    const modified = {modified: true, onButton: false, onSlider: false};
    for (const key of [' ', 'k', 'ArrowLeft', 'ArrowRight', 'j', 'l', 'm', 'f']) {
        assert.equal(look.playerKey(key, modified), null, key);
    }
    const where = {modified: false, onButton: false, onSlider: false};
    for (const key of ['Escape', 'Tab', 'Enter', 'c', 'Home', 'End', 'PageUp']) {
        assert.equal(look.playerKey(key, where), null, `${key} goes on to the dialog`);
    }
});

check('the keys and the steps are the ones the player names', () => {
    assert.equal(look.STEP, 5);
    assert.equal(look.LEAP, 10);
    const tile = read('components/ClipTile.tsx');
    assert.match(tile, /Space or K plays and pauses, the arrows go back and on, M mutes and F is full screen\./);
    assert.match(tile, /keyShortcuts='K'/);
    assert.match(tile, /keyShortcuts='M'/);
    assert.match(tile, /keyShortcuts='F'/);
});

// ---- One Download ----

check('a clip that will not play keeps Download while the reader may download it, and nothing else', () => {
    assert.deepEqual(look.playerControls({failed: false, downloadOffered: true}), {play: true, download: true});
    assert.deepEqual(look.playerControls({failed: false, downloadOffered: false}), {play: true, download: false});
    assert.deepEqual(
        look.playerControls({failed: true, downloadOffered: true}),
        {play: false, download: true},
        'CAN’T PLAY: the file is worth downloading to play somewhere else',
    );
    assert.deepEqual(look.playerControls({failed: true, downloadOffered: false}), {play: false, download: false});

    // And the tile draws its controls by that rule, Download included.
    const tile = read('components/ClipTile.tsx');
    assert.match(tile, /const controls = playerControls\(\{failed, downloadOffered: download !== undefined\}\);/);
    assert.match(tile, /\{controls\.play \|\| controls\.download \? \(\s*<Hud/);
    assert.match(tile, /\{download && controls\.download \? <DownloadButton download=\{download\}\/> : null\}/);
});

check('a press that begins on a control and is let go over the picture is not a click on the picture', () => {
    const hud = read('components/Hud.tsx');
    assert.match(hud, /surfacePressRef\.current = event\.target === event\.currentTarget \? event\.pointerType : '';/);
    assert.match(hud, /onSurface && event\.target === event\.currentTarget && surfacePressRef\.current === 'mouse'/);
});

check('Download is offered in the player only: the viewer’s row of pills has none, and the console passes none', () => {
    const viewer = read('components/ClipViewer.tsx');
    assert.doesNotMatch(viewer, /\bDOWNLOAD\b|DOWNLOAD_PREPARING/, 'no Download words of its own');
    assert.match(viewer, /download=\{clipDownloadOffered\(clip, access\) \? \{/, 'only while the server says this reader may');
    const consolePage = read('components/AdminClips.tsx');
    const tile = consolePage.match(/<ClipTile[\s\S]*?\/>/);
    assert.ok(tile, 'the console plays clips in the same player');
    assert.doesNotMatch(tile[0], /download=|says=/);
});

check('the picture has no controls or menu of its own: Download is the one way', () => {
    const tile = read('components/ClipTile.tsx');
    const video = tile.match(/<video\s+ref=\{media\.video\}[\s\S]*?\/>/);
    assert.ok(video, 'the player’s one <video>');
    assert.doesNotMatch(video[0], /\bcontrols\b/);
    assert.match(video[0], /onContextMenu=\{\(event\) => event\.preventDefault\(\)\}/);
});

console.log(`check-player: ${checks} checks passed`);
