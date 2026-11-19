// The rules for bringing the panel back, for placing the small player, for
// what Clips show, and for the lock screen, checked without a browser.
//
// webapp/src/awayLogic.ts, webapp/src/miniLayout.ts, webapp/src/clipLogic.ts,
// webapp/src/lockLogic.ts, webapp/src/motion.ts, webapp/src/focusTrap.ts,
// webapp/src/twoClicksLogic.ts, webapp/src/time.ts and webapp/src/pushAdmin.ts import nothing (but
// types), so they are transpiled with
// the project's own TypeScript, imported from a data URL and run here. webapp/src/tagSearch.ts is
// run the same way, with the modules it imports swapped for stand-ins (loadWith). There is no
// other JavaScript test runner in the project; this is all of it. Run from the project root (make
// check).
//
// The side panel is driven by a copy of the few reducers of Mattermost's that
// matter (reducers/views/rhs.ts and rhs_suppressed.ts, as the lab has them),
// so a check reads as the sequence of actions a person causes rather than as
// hand-made states.

import assert from 'node:assert/strict';
import {readFileSync, readdirSync} from 'node:fs';
import {createRequire} from 'node:module';

const require = createRequire(import.meta.url);
const ts = require('../webapp/node_modules/typescript');

// The plugin's id, as plugin.json has it, and the addresses under it.
const PLUGIN_ID = JSON.parse(readFileSync(new URL('../plugin.json', import.meta.url), 'utf8')).id;
const PLUGIN_BASE = `/plugins/${PLUGIN_ID}`;

async function load(file) {
    const source = readFileSync(new URL(`../webapp/src/${file}`, import.meta.url), 'utf8');
    const out = ts.transpileModule(source, {
        compilerOptions: {module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2020},
        fileName: file,
    });

    return import(`data:text/javascript;base64,${Buffer.from(out.outputText).toString('base64')}`);
}

// The same for a module that imports others: each of its imports, by the path
// it asks for ('./client' and the like), is swapped for the source of a
// stand-in module, so a check can play Mattermost's part. A path with no
// stand-in fails rather than being left to resolve against nothing.
async function loadWith(file, standIns) {
    const source = readFileSync(new URL(`../webapp/src/${file}`, import.meta.url), 'utf8');
    const out = ts.transpileModule(source, {
        compilerOptions: {module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2020},
        fileName: file,
    });
    const swapped = out.outputText.replace(/from '(\.[^']*)'/g, (_, path) => {
        assert.ok(path in standIns, `${file} imports ${path}, and there is no stand-in for it`);

        return `from 'data:text/javascript;base64,${Buffer.from(standIns[path]).toString('base64')}'`;
    });

    return import(`data:text/javascript;base64,${Buffer.from(swapped).toString('base64')}`);
}

const logic = await load('awayLogic.ts');
const layout = await load('miniLayout.ts');
const clips = await load('clipLogic.ts');
const lock = await load('lockLogic.ts');
const motion = await load('motion.ts');
const keys = await load('focusTrap.ts');
const twice = await load('twoClicksLogic.ts');
const time = await load('time.ts');
const push = await load('pushAdmin.ts');

let checks = 0;
function check(name, run) {
    try {
        run();
        checks++;
    } catch (error) {
        console.error(`check-away: ${name}`);
        throw error;
    }
}

// The same for a check that has to wait, as a search does for its answers.
async function checkAsync(name, run) {
    try {
        await run();
        checks++;
    } catch (error) {
        console.error(`check-away: ${name}`);
        throw error;
    }
}

// ---- A side panel, as Mattermost's reducers move it ----

const OURS = 'panel-id';
const OTHER = 'other-plugin';

const SHOWN = {
    rhs: {rhsState: 'plugin', pluggableId: OURS, selectedPostId: '', selectedPostCardId: '', isSidebarOpen: true, isSidebarExpanded: false},
    suppressed: false,
    mobile: false,
    teamId: 't1',
    path: 'team-one',
    channelId: 'c1',
};

function reduce(world, action) {
    const rhs = world.rhs;
    switch (action.type) {
    case 'UPDATE_RHS_STATE': {
        const state = action.state || null;

        return {
            ...world,
            rhs: {
                ...rhs,
                rhsState: state,
                pluggableId: state === 'plugin' ? action.pluggableId || '' : '',
                selectedPostId: action.postId || '',
                selectedPostCardId: '',
                isSidebarOpen: Boolean(state),
                isSidebarExpanded: state ? rhs.isSidebarExpanded : false,
            },
            suppressed: state === null ? world.suppressed : false,
        };
    }
    case 'SELECT_POST':
        return {
            ...world,
            rhs: {
                ...rhs,
                rhsState: null,
                pluggableId: '',
                selectedPostId: action.postId,
                selectedPostCardId: '',
                isSidebarOpen: Boolean(action.postId),
                isSidebarExpanded: action.postId ? rhs.isSidebarExpanded : false,
            },
            suppressed: action.postId === '' ? world.suppressed : false,
        };
    case 'RHS_GO_BACK':
        return {...world, rhs: {...rhs, rhsState: action.state, selectedPostId: ''}};
    case 'POST_REMOVED':
        return rhs.selectedPostId === action.postId ? {...world, rhs: {...rhs, selectedPostId: ''}} : world;
    case 'SET_RHS_EXPANDED':
        return {...world, rhs: {...rhs, isSidebarExpanded: action.expanded}};
    case 'SUPPRESS_RHS':
        return {...world, rhs: {...rhs, isSidebarExpanded: false}, suppressed: true};
    case 'UNSUPPRESS_RHS':
        return {...world, suppressed: false};
    case 'CLOSE':
        return reduce(reduce(world, {type: 'UPDATE_RHS_STATE', state: null}), {type: 'SELECT_POST', postId: ''});
    case 'SWITCH_TEAM':
        return reduce({...world, teamId: action.teamId, path: action.path, channelId: 'c-elsewhere'}, {type: 'CLOSE'});
    case 'SWITCH_CHANNEL':
        return {...world, channelId: action.channelId};
    case 'MOBILE':
        return {...world, mobile: action.mobile};
    case 'PRODUCT':
        return {...world, path: action.path};
    default:
        throw new Error(`no reducer for ${action.type}`);
    }
}

const ON = {returnAfter: 'reply', mini: 'on'};

// Runs actions through the state machine the way away.ts does: a step per
// change, the hint cleared once the panel has changed, and the latch worked out
// before each step.
function drive(actions, {start = SHOWN, settings = ON, latch = 'a', hint = null, prev, watchCapable} = {}) {
    let world = start;
    let away = prev || logic.initialAway(world, OURS);
    let currentHint = hint;
    const steps = [];
    for (const action of actions) {
        const next = typeof action === 'function' ? action(world) : reduce(world, action);
        const panelBefore = logic.panelOf(world.rhs, OURS);
        const ctx = {ours: OURS, settings, now: 1000, hint: currentHint, latch, ...(watchCapable === undefined ? {} : {watchCapable})};
        const result = logic.step(away, world, next, ctx);
        steps.push(result);
        away = result.next;
        if (logic.panelOf(next.rhs, OURS) !== panelBefore) {
            currentHint = null;
        }
        world = next;
    }

    return {away, world, steps, last: steps[steps.length - 1]};
}

const kinds = (step) => step.effects.map((effect) => effect.kind);
const reply = (root) => ({type: 'SELECT_POST', postId: root, channelId: 'c1'});
const close = {type: 'CLOSE'};
const search = {type: 'UPDATE_RHS_STATE', state: 'search'};
const otherPlugin = {type: 'UPDATE_RHS_STATE', state: 'plugin', pluggableId: OTHER};
const showPanel = {type: 'UPDATE_RHS_STATE', state: 'plugin', pluggableId: OURS};
const CLOSED = reduce(SHOWN, close);

// ---- panelOf and the store's shape ----

check('panelOf names every kind of panel', () => {
    assert.equal(logic.panelOf(SHOWN.rhs, OURS), 'villanet');
    assert.equal(logic.panelOf(CLOSED.rhs, OURS), 'closed');
    assert.equal(logic.panelOf(reduce(SHOWN, reply('r1')).rhs, OURS), 'thread');
    assert.equal(logic.panelOf(reduce(SHOWN, search).rhs, OURS), 'own');
    assert.equal(logic.panelOf(reduce(SHOWN, otherPlugin).rhs, OURS), 'plugin');
    for (const state of ['mention', 'flag', 'pin', 'channel-files', 'channel-info', 'channel-members']) {
        assert.equal(logic.panelOf(reduce(SHOWN, {type: 'UPDATE_RHS_STATE', state}).rhs, OURS), 'own', state);
    }
    const history = {...SHOWN.rhs, rhsState: 'edit-history', pluggableId: '', selectedPostId: 'p1'};
    assert.equal(logic.panelOf(history, OURS), 'own');
    const card = {...SHOWN.rhs, rhsState: null, pluggableId: '', selectedPostId: '', selectedPostCardId: 'p2'};
    assert.equal(logic.panelOf(card, OURS), 'own');
    const back = reduce(reduce(SHOWN, reply('r1')), {type: 'RHS_GO_BACK', state: 'plugin'});
    assert.equal(logic.panelOf(back.rhs, OURS), 'empty');
    const deleted = reduce(reduce(SHOWN, reply('r1')), {type: 'POST_REMOVED', postId: 'r1'});
    assert.equal(logic.panelOf(deleted.rhs, OURS), 'empty');
});

check('an unrecognised store shape is inert', () => {
    assert.equal(logic.rhsSliceOf(undefined), null);
    assert.equal(logic.rhsSliceOf({rhsState: 3, isSidebarOpen: true}), null);
    assert.equal(logic.rhsSliceOf({rhsState: 'plugin'}), null);
    assert.deepEqual(logic.rhsSliceOf({rhsState: null, isSidebarOpen: false}), {
        rhsState: null, pluggableId: '', selectedPostId: '', selectedPostCardId: '', isSidebarOpen: false, isSidebarExpanded: false,
    });
});

check('install: docked only while the panel shows outside the Threads page', () => {
    assert.equal(logic.initialAway(SHOWN, OURS).phase, 'docked');
    assert.equal(logic.initialAway({...SHOWN, suppressed: true}, OURS).phase, 'idle');
    assert.equal(logic.initialAway(CLOSED, OURS).phase, 'idle');
});

// ---- One case per row of the transition table ----

check('row 1: idle, the panel opened', () => {
    const run = drive([showPanel], {start: CLOSED});
    assert.equal(run.last.row, 1);
    assert.equal(run.away.phase, 'docked');
});

check('row 2: idle stays idle when a thread opens on a closed panel (R11)', () => {
    const run = drive([reply('r1'), close], {start: CLOSED});
    assert.deepEqual(run.steps.map((step) => step.row), [2, 2]);
    assert.equal(run.away.phase, 'idle');
    assert.deepEqual(run.steps.flatMap(kinds), []);
});

check('row 3: docked, expansion toggled', () => {
    const run = drive([{type: 'SET_RHS_EXPANDED', expanded: true}]);
    assert.equal(run.last.row, 3);
    assert.equal(run.away.phase, 'docked');
});

check('row 4: docked, the Threads page', () => {
    const run = drive([{type: 'SUPPRESS_RHS'}], {latch: 'b'});
    assert.equal(run.last.row, 4);
    assert.equal(run.away.cause, 'suppressed');
    assert.equal(run.away.returnOnEnd, false);
    assert.equal(run.away.latched, 'b');
    assert.deepEqual(kinds(run.last), ['hold']);
});

check('row 5: docked, closed (icon, Ctrl+., team switch) stops everything', () => {
    const run = drive([close]);
    assert.equal(run.last.row, 5);
    assert.equal(run.away.phase, 'idle');
    assert.deepEqual(kinds(run.last), ['stopAll']);
});

check('row 6: docked, phone-sized, a thread opens: idle with no effects', () => {
    const run = drive([reply('r1')], {start: {...SHOWN, mobile: true}});
    assert.equal(run.last.row, 6);
    assert.equal(run.away.phase, 'idle');
    assert.deepEqual(kinds(run.last), []);
});

check('row 7: docked, a thread arms a reply-return', () => {
    const run = drive([reply('r1')]);
    assert.equal(run.last.row, 7);
    assert.equal(run.away.cause, 'own');
    assert.equal(run.away.replyRoot, 'r1');
    assert.equal(run.away.returnOnEnd, true);
    assert.equal(run.away.noReplyFor, null);
    assert.equal(run.away.teamId, 't1');
    assert.equal(run.away.channelId, 'c1');
    assert.deepEqual(kinds(run.last), ['hold']);
});

check('row 8: docked, one of Mattermost own panels arms a close-return only', () => {
    const run = drive([search]);
    assert.equal(run.last.row, 8);
    assert.equal(run.away.replyRoot, null);
    assert.equal(run.away.returnOnEnd, true);
});

check('row 9: docked, another plugin', () => {
    const run = drive([otherPlugin]);
    assert.equal(run.last.row, 9);
    assert.equal(run.away.cause, 'plugin');
    assert.equal(run.away.returnOnEnd, false);
    assert.deepEqual(kinds(run.last), ['hold']);
});

check('row 10: docked, an empty panel', () => {
    const run = drive([{type: 'UPDATE_RHS_STATE', state: 'plugin', pluggableId: ''}]);
    assert.equal(run.last.row, 10);
    assert.equal(run.away.phase, 'docked');
});

check('row 11: away, the panel shown again (icon over a thread, R9)', () => {
    const run = drive([reply('r1'), showPanel]);
    assert.equal(run.last.row, 11);
    assert.equal(run.away.phase, 'docked');
    assert.deepEqual(kinds(run.last), ['cancelPending', 'release', 'markDocked']);
});

check('row 12: away, the plugin in the side panel behind the Threads page', () => {
    const run = drive([{type: 'SUPPRESS_RHS'}, {type: 'SET_RHS_EXPANDED', expanded: false}]);
    assert.equal(run.last.row, 12);
    assert.equal(run.away.cause, 'suppressed');
});

check('row 13: away, a team switch stops everything (R14)', () => {
    const run = drive([reply('r1'), {type: 'SWITCH_TEAM', teamId: 't2', path: 'team-two'}]);
    assert.equal(run.last.row, 13);
    assert.equal(run.away.phase, 'idle');
    assert.deepEqual(kinds(run.last), ['cancelPending', 'stopAll']);
});

check('row 13: away, a product switch stops everything', () => {
    const run = drive([reply('r1'), {type: 'PRODUCT', path: 'boards'}]);
    assert.equal(run.last.row, 13);
    assert.deepEqual(kinds(run.last), ['cancelPending', 'stopAll']);
});

check('row 14: away, narrowed to phone size', () => {
    const run = drive([reply('r1'), {type: 'MOBILE', mobile: true}]);
    assert.equal(run.last.row, 14);
    assert.equal(run.away.phase, 'idle');
    assert.deepEqual(kinds(run.last), ['cancelPending', 'release']);
});

check('row 15: away, the thread closed: the panel returns (R2)', () => {
    const run = drive([reply('r1'), close]);
    assert.equal(run.last.row, 15);
    assert.equal(run.away.phase, 'away');
    assert.deepEqual(run.last.effects, [{kind: 'return', expand: false}]);
});

check('row 16: away, another plugin closed: idle, nothing returns (R10)', () => {
    const run = drive([otherPlugin, close]);
    assert.equal(run.last.row, 16);
    assert.equal(run.away.phase, 'idle');
    assert.deepEqual(kinds(run.last), ['cancelPending', 'release']);
});

check('row 17: away, another plugin takes over from a thread', () => {
    const run = drive([reply('r1'), otherPlugin]);
    assert.equal(run.last.row, 17);
    assert.equal(run.away.cause, 'plugin');
    assert.equal(run.away.returnOnEnd, false);
    assert.equal(run.away.replyRoot, null);
    assert.deepEqual(kinds(run.last), ['cancelPending']);
});

check('row 18: away for another plugin, a thread opens over it', () => {
    const run = drive([otherPlugin, reply('r1')]);
    assert.equal(run.last.row, 18);
    assert.equal(run.away.cause, 'plugin');
    assert.equal(run.away.returnOnEnd, false);
});

check('row 19: the Threads page left for a thread arms, keeping the latch', () => {
    const run = drive([{type: 'SUPPRESS_RHS'}, (world) => ({...reduce(world, reply('r1'))})], {latch: 'a'});
    assert.equal(run.steps[0].next.latched, 'a');
    const second = logic.step(run.steps[0].next, reduce(SHOWN, {type: 'SUPPRESS_RHS'}),
        reduce(reduce(SHOWN, {type: 'SUPPRESS_RHS'}), reply('r1')),
        {ours: OURS, settings: ON, now: 0, hint: null, latch: 'z'});
    assert.equal(second.row, 19);
    assert.equal(second.next.cause, 'own');
    assert.equal(second.next.replyRoot, 'r1');
    assert.equal(second.next.latched, 'a');
    assert.deepEqual(kinds(second), ['cancelPending']);
});

check('row 20: away for the Threads page, still suppressed', () => {
    const prev = drive([{type: 'SUPPRESS_RHS'}]).away;
    const before = reduce(SHOWN, {type: 'SUPPRESS_RHS'});
    const after = {...before, rhs: {...before.rhs, rhsState: null, pluggableId: '', selectedPostId: 'r1'}};
    const result = logic.step(prev, before, after, {ours: OURS, settings: ON, now: 0, hint: null, latch: null});
    assert.equal(result.row, 20);
    assert.equal(result.next, prev);
});

check('row 21: thread to thread retargets the reply-return', () => {
    const run = drive([reply('r1'), reply('r2')]);
    assert.equal(run.last.row, 21);
    assert.equal(run.away.replyRoot, 'r2');
    assert.deepEqual(kinds(run.last), ['cancelPending', 'clearPlaceholders']);
});

check('row 21: back into the thread Back to thread reopened stays close-only', () => {
    const prev = {...drive([reply('r1')]).away, noReplyFor: 'r2'};
    const before = reduce(SHOWN, reply('r1'));
    const result = logic.step(prev, before, reduce(before, reply('r2')), {ours: OURS, settings: ON, now: 0, hint: null, latch: null});
    assert.equal(result.row, 21);
    assert.equal(result.next.replyRoot, null);
    assert.equal(result.next.noReplyFor, 'r2');
});

check('row 22: thread to thread with no reply-return armed', () => {
    const run = drive([search, reply('r1')]);
    assert.equal(run.last.row, 22);
    assert.equal(run.away.replyRoot, null);
    assert.deepEqual(kinds(run.last), ['cancelPending']);
});

check('row 23: thread to search drops the reply-return', () => {
    const run = drive([reply('r1'), search]);
    assert.equal(run.last.row, 23);
    assert.equal(run.away.replyRoot, null);
    assert.equal(run.away.returnOnEnd, true);
});

check('row 24: away, expansion toggled in the thread', () => {
    const run = drive([reply('r1'), {type: 'SET_RHS_EXPANDED', expanded: true}]);
    assert.equal(run.last.row, 24);
    assert.equal(run.away.replyRoot, 'r1');
});

// ---- The edge cases that need no page ----

check('thread, search, back to results, thread: close-return only', () => {
    const run = drive([reply('r1'), search, reply('r1')]);
    assert.equal(run.away.replyRoot, null);
    assert.equal(run.away.returnOnEnd, true);
    const closed = drive([close], {start: run.world, prev: run.away});
    assert.equal(closed.last.row, 15);
});

check('the root deleted while armed returns the panel', () => {
    const run = drive([reply('r1'), {type: 'POST_REMOVED', postId: 'r1'}]);
    assert.equal(run.last.row, 15);
});

check('going back to a forgotten plugin returns the panel', () => {
    const run = drive([reply('r1'), {type: 'RHS_GO_BACK', state: 'plugin'}]);
    assert.equal(run.last.row, 15);
});

check('a channel switch keeps the thread armed, and expansion only comes back in that channel', () => {
    const expanded = {...SHOWN, rhs: {...SHOWN.rhs, isSidebarExpanded: true}};
    const run = drive([reply('r1'), {type: 'SWITCH_CHANNEL', channelId: 'c2'}], {start: expanded});
    assert.equal(run.last.row, 24);
    assert.equal(run.away.replyRoot, 'r1');
    const closed = drive([close], {start: run.world, prev: run.away});
    assert.deepEqual(closed.last.effects, [{kind: 'return', expand: false}]);
    const same = drive([{type: 'SWITCH_CHANNEL', channelId: 'c1'}, close], {start: run.world, prev: run.away});
    assert.deepEqual(same.last.effects, [{kind: 'return', expand: true}]);
});

check('expansion: a mouse Reply comes back collapsed, a keyboard one expanded', () => {
    const expanded = {...SHOWN, rhs: {...SHOWN.rhs, isSidebarExpanded: true}};
    const mouse = drive([{type: 'SET_RHS_EXPANDED', expanded: false}, reply('r1')], {start: expanded});
    assert.equal(mouse.away.expanded, false);
    const keyboard = drive([reply('r1')], {start: expanded});
    assert.equal(keyboard.away.expanded, true);
    assert.equal(keyboard.world.rhs.isSidebarExpanded, true);
    assert.deepEqual(drive([close], {start: keyboard.world, prev: keyboard.away}).last.effects, [{kind: 'return', expand: true}]);
});

check('settings: close gives no reply-return, never gives none at all', () => {
    const closeOnly = drive([reply('r1')], {settings: {returnAfter: 'close', mini: 'on'}});
    assert.equal(closeOnly.away.replyRoot, null);
    assert.equal(closeOnly.away.returnOnEnd, true);
    const never = drive([reply('r1'), close], {settings: {returnAfter: 'never', mini: 'on'}});
    assert.equal(never.steps[0].next.returnOnEnd, false);
    assert.equal(never.last.row, 16);
    assert.equal(never.away.phase, 'idle');
    assert.deepEqual(kinds(never.last), ['cancelPending', 'release']);
});

// ---- Back to thread ----

check('Back to thread: the reopened thread returns on close only (R19)', () => {
    const hint = {root: 'r1', until: 3000};
    const run = drive([reply('r1')], {hint});
    assert.equal(run.last.row, 7);
    assert.equal(run.last.hintUsed, true);
    assert.equal(run.away.replyRoot, null);
    assert.equal(run.away.noReplyFor, 'r1');
    assert.equal(run.away.returnOnEnd, true);
    assert.equal(logic.shouldReturnAfterLanded(run.away, run.world, ON, 'r1', OURS), false);
    assert.equal(drive([close], {start: run.world, prev: run.away}).last.row, 15);
});

check('Back to thread: a stale or mismatched hint is not used', () => {
    assert.equal(drive([reply('r1')], {hint: {root: 'r1', until: 999}}).away.replyRoot, 'r1');
    assert.equal(drive([reply('r1')], {hint: {root: 'r9', until: 3000}}).away.replyRoot, 'r1');
});

check('Back to thread: going on to another thread keeps it close-only', () => {
    const run = drive([reply('r1'), reply('r2')], {hint: {root: 'r1', until: 3000}});
    assert.equal(run.last.row, 22);
    assert.equal(run.away.replyRoot, null);
});

// ---- shouldReturnAfterLanded ----

check('shouldReturnAfterLanded truth table', () => {
    const armed = drive([reply('r1')]);
    const yes = (away, world, settings = ON, root = 'r1') => logic.shouldReturnAfterLanded(away, world, settings, root, OURS);
    assert.equal(yes(armed.away, armed.world), true);
    assert.equal(yes(armed.away, armed.world, ON, 'r2'), false, 'another root');
    assert.equal(yes({phase: 'docked'}, armed.world), false, 'not away');
    assert.equal(yes({...armed.away, cause: 'plugin'}, armed.world), false, 'cause plugin');
    assert.equal(yes({...armed.away, replyRoot: null}, armed.world), false, 'disarmed');
    assert.equal(yes(armed.away, reduce(armed.world, reply('r2'))), false, 'another thread showing');
    assert.equal(yes(armed.away, reduce(armed.world, search)), false, 'not a thread');
    assert.equal(yes(armed.away, {...armed.world, suppressed: true}), false, 'Threads page');
    assert.equal(yes(armed.away, {...armed.world, mobile: true}), false, 'phone-sized');
    assert.equal(yes(armed.away, armed.world, {returnAfter: 'close', mini: 'on'}), false, 'setting close');
    assert.equal(yes(armed.away, armed.world, {returnAfter: 'never', mini: 'on'}), false, 'setting never');
    assert.equal(yes(armed.away, armed.world, {returnAfter: 'reply', mini: 'off'}), true, 'the mini setting does not matter');
});

// ---- The exits ----

check('exits: team, product, never, plugin closed, mobile away, mobile docked', () => {
    assert.deepEqual(kinds(drive([reply('r1'), {type: 'SWITCH_TEAM', teamId: 't2', path: 'team-two'}]).last), ['cancelPending', 'stopAll']);
    assert.deepEqual(kinds(drive([search, {type: 'PRODUCT', path: 'playbooks'}]).last), ['cancelPending', 'stopAll']);
    const never = drive([reply('r1'), close], {settings: {returnAfter: 'never', mini: 'on'}});
    assert.equal(never.away.phase, 'idle');
    assert.deepEqual(kinds(never.last), ['cancelPending', 'release']);
    assert.deepEqual(kinds(drive([otherPlugin, close]).last), ['cancelPending', 'release']);
    const mobileAway = drive([reply('r1'), {type: 'MOBILE', mobile: true}]);
    assert.equal(mobileAway.away.phase, 'idle');
    assert.deepEqual(kinds(mobileAway.last), ['cancelPending', 'release']);
    const mobileDocked = drive([{type: 'MOBILE', mobile: true}, reply('r1')]);
    assert.equal(mobileDocked.away.phase, 'idle');
    assert.deepEqual(kinds(mobileDocked.last), []);
});

// ---- The latch ----

check('latch: kept across thread, search, thread', () => {
    const run = drive([reply('r1'), search, reply('r2')], {latch: 'a'});
    assert.equal(run.steps[0].next.latched, 'a');
    // A different candidate later does not move it.
    const later = drive([search, reply('r3')], {start: run.world, prev: run.away, latch: 'z'});
    assert.equal(later.away.latched, 'a');
});

check('latchCandidate: the sound, else a connected stream, else the first', () => {
    const connected = (handle) => handle === 'c';
    assert.equal(logic.latchCandidate('b', ['a', 'b', 'c'], connected), 'b');
    assert.equal(logic.latchCandidate('x', ['a', 'b', 'c'], connected), 'c');
    assert.equal(logic.latchCandidate(null, ['a', 'b'], connected), 'a');
    assert.equal(logic.latchCandidate(null, [], connected), null);
});

check('latchCandidate: nothing at all for a reader who may not watch', () => {
    const connected = () => true;
    assert.equal(logic.latchCandidate('b', ['a', 'b'], connected, false), null);
    assert.equal(logic.latchCandidate(null, ['a'], connected, false), null);
    assert.equal(logic.latchCandidate('b', ['a', 'b'], connected, true), 'b');
});

check('latchCandidate: the sound on a Twitch handle plus a connected Broadcast Box handle latches the Broadcast Box handle', () => {
    // design/twitch-live.md 6.7: a Twitch stream is never in the small
    // player, so with the sound on one, the Broadcast Box stream beside it
    // is the one held, rather than nothing being held and it hung up.
    const twitch = (handle) => handle.startsWith('tw-');
    const connected = (handle) => handle === 'bb';
    assert.equal(logic.latchCandidate('tw-erin', ['tw-erin', 'bb'], connected, true, twitch), 'bb');
    assert.equal(logic.latchCandidate('tw-erin', ['bb', 'tw-erin'], () => false, true, twitch), 'bb', 'connected or not');
    assert.equal(logic.latchCandidate(null, ['tw-erin', 'bb2', 'bb'], connected, true, twitch), 'bb', 'the connected one first');

    // Only Twitch handles watched: nothing is latched, so there is no small
    // player at all.
    assert.equal(logic.latchCandidate('tw-erin', ['tw-erin', 'tw-bob'], () => true, true, twitch), null);
    assert.equal(logic.latchCandidate(null, ['tw-erin'], () => true, true, twitch), null);
});

check('locked: no return after a reply or a close, and the thread stays (design/lock-screen.md 4.9)', () => {
    const locked = drive([reply('r1')], {watchCapable: false});
    assert.equal(locked.away.phase, 'away');
    assert.equal(locked.away.replyRoot, null, 'a reply brings nothing back');
    assert.equal(locked.away.returnOnEnd, false, 'closing the thread brings nothing back');
    const closed = drive([close], {start: locked.world, prev: locked.away, watchCapable: false});
    assert.equal(closed.away.phase, 'idle');
    assert.ok(!kinds(closed.last).includes('return'), 'no return on close');

    // Watch-capable, nothing changes: the reply is armed as ever.
    const able = drive([reply('r1')], {watchCapable: true});
    assert.equal(able.away.replyRoot, 'r1');
    assert.equal(able.away.returnOnEnd, true);
});

// ---- A reply landing ----

check('findLanded: this tab placeholder, then the real post', () => {
    const seen = new Set();
    const mine = new Set();
    const posts = {old: {id: 'old', user_id: 'me'}};
    assert.equal(logic.findLanded(['old'], posts, 'me', seen, mine), false);
    posts['me:1'] = {id: 'me:1', pending_post_id: 'me:1', user_id: 'me'};
    assert.equal(logic.findLanded(['old', 'me:1'], posts, 'me', seen, mine), false);
    assert.ok(mine.has('me:1'));
    posts.real = {id: 'real', pending_post_id: 'me:1', user_id: 'me'};
    assert.equal(logic.findLanded(['old', 'real'], posts, 'me', seen, mine), true);
});

check('findLanded: a reply from another device or person is not this tab', () => {
    const seen = new Set();
    const mine = new Set();
    const posts = {
        elsewhere: {id: 'elsewhere', pending_post_id: 'me:9', user_id: 'me'},
        theirs: {id: 'theirs', pending_post_id: 'u2:4', user_id: 'u2'},
        'u2:5': {id: 'u2:5', pending_post_id: 'u2:5', user_id: 'u2'},
    };
    assert.equal(logic.findLanded(['elsewhere', 'theirs', 'u2:5'], posts, 'me', seen, mine), false);
    assert.equal(mine.size, 0);
});

check('findLanded: a failed send does not land, its retry does (R6)', () => {
    const seen = new Set();
    const mine = new Set();
    const posts = {'me:2': {id: 'me:2', pending_post_id: 'me:2', user_id: 'me'}};
    assert.equal(logic.findLanded(['me:2'], posts, 'me', seen, mine), false);
    posts['me:2'] = {...posts['me:2'], failed: true};
    assert.equal(logic.findLanded(['me:2'], posts, 'me', seen, mine), false);
    posts['me:2'] = {id: 'me:2', pending_post_id: 'me:2', user_id: 'me'};
    posts.retried = {id: 'retried', pending_post_id: 'me:2', user_id: 'me'};
    assert.equal(logic.findLanded(['retried'], posts, 'me', seen, mine), true);
});

// ---- miniLayout ----

const anchor = (width, height) => ({left: 0, top: 0, right: width, bottom: height});

check('miniLayout: picture down to 240 wide, then hidden by width', () => {
    const at = (width) => layout.layoutMini(anchor(width, 900), null, 'tr', 0);
    assert.equal(at(272).mode, 'picture');
    assert.equal(at(272).w, 240);
    assert.equal(at(271).mode, 'hidden');
    assert.equal(at(1280).w, 384);
    assert.equal(at(1000).w, 300);
    assert.equal(at(1000).h, Math.round(300 * 9 / 16) + 32);
    assert.equal(layout.NAME_LINE, 32, 'the name line under the picture, the player having no frame of its own');
});

check('miniLayout: picture to bar by height, bar to hidden at 108', () => {
    const at = (height, width = 800) => layout.layoutMini(anchor(width, height), null, 'tr', 0);
    assert.equal(at(372).mode, 'picture');
    assert.equal(at(371).mode, 'bar');
    assert.equal(at(371).w, 360);
    assert.equal(at(371).h, 44);
    assert.equal(at(108).mode, 'bar');
    assert.equal(at(107).mode, 'hidden');
    assert.equal(at(200, 272).mode, 'bar');
    assert.equal(at(200, 272).w, 240);
    assert.equal(at(200, 271).mode, 'hidden');
});

check('miniLayout: a tall list caps the picture at 45 percent of its height', () => {
    const tall = layout.layoutMini(anchor(1400, 500), null, 'tr', 0);
    assert.equal(tall.mode, 'picture');
    assert.ok(tall.h <= 0.45 * 500 + 1);
    assert.equal(tall.w, Math.floor(((0.45 * 500) - 32) * 16 / 9));
});

check('miniLayout: the expanded side panel lying over the list is taken off', () => {
    const rhs = {left: 600, top: 0, right: 1200, bottom: 800};
    const room = layout.measureRoom(anchor(1000, 800), rhs, 8);
    assert.equal(room.width, 592);
    const placed = layout.layoutMini(anchor(1000, 800), rhs, 'tr', 8);
    assert.ok(placed.x + placed.w <= 600 - 16 - 8);
    // A side panel beside the list, not over it, changes nothing.
    assert.equal(layout.measureRoom(anchor(1000, 800), {left: 1000, top: 0, right: 1400, bottom: 800}, 0).width, 1000);
    // A closed one, of no width, neither.
    assert.equal(layout.measureRoom(anchor(1000, 800), {left: 500, top: 0, right: 500, bottom: 0}, 0).width, 1000);
});

check('miniLayout: the four corners, inside the insets', () => {
    const list = {left: 100, top: 50, right: 1100, bottom: 850};
    const tr = layout.layoutMini(list, null, 'tr', 8);
    assert.equal(tr.w, 298);
    assert.equal(tr.h, 168 + 32);
    assert.deepEqual([tr.x, tr.y], [1076 - 298, 102]);
    const tl = layout.layoutMini(list, null, 'tl', 8);
    assert.deepEqual([tl.x, tl.y], [116, 102]);
    const br = layout.layoutMini(list, null, 'br', 8);
    assert.deepEqual([br.x, br.y], [1076 - 298, 838 - 200]);
    const bl = layout.layoutMini(list, null, 'bl', 8);
    assert.deepEqual([bl.x, bl.y], [116, 838 - 200]);
});

check('miniLayout: corners cycle clockwise, arrows jump, drags snap and clamp', () => {
    assert.deepEqual(['tr', 'br', 'bl', 'tl'].map(layout.nextCorner), ['br', 'bl', 'tl', 'tr']);
    assert.equal(layout.cornerToward('tr', 'ArrowLeft'), 'tl');
    assert.equal(layout.cornerToward('tr', 'ArrowDown'), 'br');
    assert.equal(layout.cornerToward('bl', 'ArrowUp'), 'tl');
    assert.equal(layout.cornerToward('bl', 'ArrowRight'), 'br');
    assert.equal(layout.cornerToward('bl', 'Enter'), 'bl');
    const box = layout.boxOf({left: 0, top: 0, right: 1000, bottom: 800}, null, 0);
    assert.equal(layout.nearestCorner(box, 300, 200, 20, 60), 'tl');
    assert.equal(layout.nearestCorner(box, 300, 200, 600, 500), 'br');
    assert.deepEqual(layout.clampPosition(box, 300, 200, -50, 5000), {x: box.left, y: box.bottom - 200});
    assert.equal(layout.parseCorner('bl'), 'bl');
    assert.equal(layout.parseCorner('middle'), 'tr');
    assert.equal(layout.parseCorner(null), 'tr');
});

// ---- Clips: the shared clip's card (addendum 4.9) ----

const NOW = Date.UTC(2026, 9, 2, 21, 4);
const DAY = 86400000;

// A post as the server makes one, with whatever is changed on top.
function clipPost(props = {}, more = {}) {
    return {
        type: 'custom_villanet_clip',
        message: `🎬 Matty clip · 0:45\nWatch it: https://chat.example.com/plug/${PLUGIN_ID}/clip/3f9a0c2e7b1d4a55`,
        props: {
            villanet_clip_id: '3f9a0c2e7b1d4a55',
            villanet_clip_title: 'that jump',
            villanet_clip_duration_ms: 45000,
            villanet_clip_created_at: NOW - (3 * DAY),
            villanet_clip_clipper_id: 'u-bob',
            villanet_clip_clipper: 'Bob',
            villanet_clip_streamer_id: 'u-dave',
            villanet_clip_streamer: 'Dave',
            ...props,
        },
        ...more,
    };
}

const SERVER_CARD = {
    id: '3f9a0c2e7b1d4a55',
    title: 'that jump',
    createdAt: NOW - (3 * DAY),
    durationMs: 45000,
    thumb: 'a1b2c3d4',
    clipper: {userId: 'u-bob', name: 'Bob'},
    streamer: {userId: 'u-dave', name: 'Dave'},
};

// mayWatch: the reader may watch clips (linked, or let in by an admin); false
// is a locked reader; undefined is not known yet.
// name: what the server calls the plugin, Matty unless it says otherwise.
const card = (post, mayWatch, fetch = {status: 'idle'}, retentionDays = 30, now = NOW, name = 'Matty') =>
    clips.cardView({post, mayWatch, retentionDays, now, fetch, name});

// A refusal's words, with the server's name for the plugin (Matty, unless a
// check says another).
const clipCopy = (code, context, values = {}, serverText) => clips.clipErrorCopy(code, context, {brand: 'Matty', ...values}, serverText);

check('clipLogic imports nothing, so this script can load it', () => {
    const source = readFileSync(new URL('../webapp/src/clipLogic.ts', import.meta.url), 'utf8');
    assert.equal(/^\s*import\s/m.test(source), false);
});

check('card: anything the plugin did not write is the plain-text fallback', () => {
    const fallbacks = [
        clipPost({}, {type: ''}),
        clipPost({}, {type: 'custom_other'}),
        clipPost({}, {props: null}),
        clipPost({}, {props: ['villanet_clip_id']}),
        clipPost({villanet_clip_id: '3F9A0C2E7B1D4A55'}),
        clipPost({villanet_clip_id: '3f9a0c2e7b1d4a5'}),
        clipPost({villanet_clip_id: 42}),
        clipPost({villanet_clip_title: 7}),
        clipPost({villanet_clip_clipper: {name: 'Bob'}}),
        clipPost({villanet_clip_duration_ms: '45000'}),
        clipPost({villanet_clip_duration_ms: undefined}),
        clipPost({villanet_clip_created_at: -1}),
        clipPost({villanet_clip_created_at: Number.NaN}),
        clipPost({villanet_clip_gone: true}),
        clipPost({villanet_clip_gone: 'hidden'}),
    ];
    fallbacks.forEach((post, index) => {
        const view = card(post, true, {status: 'ok', card: SERVER_CARD});
        assert.equal(view.state, 'fallback', `fallback ${index}`);
        assert.equal(view.message, post.message);
        assert.equal(view.watch, false);
        assert.equal(view.text, undefined);
    });
    // A message that is not a string never reaches the page as one.
    assert.equal(card({type: 'custom_villanet_clip', message: {x: 1}, props: {}}, true).message, '');
});

check('card: expired, by the server\'s mark or by its own age, before anything else', () => {
    const marked = card(clipPost({villanet_clip_gone: 'expired'}), true, {status: 'ok', card: SERVER_CARD});
    assert.equal(marked.state, 'gone-expired');
    assert.equal(marked.sentence, 'This clip has expired.');
    assert.equal(marked.text.title, 'that jump');
    assert.equal(marked.picture, 'placeholder');
    assert.equal(marked.dimmed, true);
    assert.equal(marked.watch, false);
    assert.equal(marked.thumb, undefined);
    assert.equal(marked.label, 'Matty clip: “that jump”, 45 seconds, clipped by Bob from Dave’s stream, expired');

    // Never marked (an archived channel, a marker that failed): its age says
    // it, for every reader, with no request.
    const made = NOW - (3 * DAY);
    [true, false, undefined].forEach((linked) => {
        assert.equal(card(clipPost(), linked, {status: 'idle'}, 3, made + (3 * DAY)).state, 'gone-expired');
        assert.notEqual(card(clipPost(), linked, {status: 'idle'}, 3, made + (3 * DAY) - 1).state, 'gone-expired');
    });
    // Without the snapshot's clips there is no retention to go by.
    assert.equal(clips.cardView({post: clipPost(), mayWatch: false, now: NOW + (400 * DAY), fetch: {status: 'idle'}, name: 'Matty'}).state, 'locked');
    assert.equal(card(clipPost(), false, {status: 'idle'}, 0, NOW + (400 * DAY)).state, 'locked');
    // A deleted mark is not aged into "expired".
    assert.equal(card(clipPost({villanet_clip_gone: 'deleted'}), true, {status: 'idle'}, 3, NOW + (400 * DAY)).state, 'gone-deleted');
});

check('card: a post asks for its card only while it is loading, so the count\'s "expired" stands; a card in hand can make it expire sooner', () => {
    // What PostCard does: the post's own view comes first, with nothing
    // asked, and only a view that is loading asks for the card. So a post
    // the panel's count of retentionDays says has expired is shown expired,
    // and asks nothing, whatever the server's card would say (a retention
    // lowered within the server's day of grace, or a post's date patched):
    // as at 3.5.0.
    const post = readFileSync(new URL('../webapp/src/components/ClipPostCard.tsx', import.meta.url), 'utf8');
    assert.match(post, /const before = cardView\(\{post, mayWatch: watch, retentionDays, now, fetch: \{status: 'idle'\}, name: brand\}\);\n\s+const asks = watch === true && before\.state === 'loading';/);
    assert.match(post, /const view = asks \? cardView\(\{post, mayWatch: watch, retentionDays, now, fetch, name: brand\}\) : before;/);
    assert.equal(card(clipPost(), true, {status: 'idle'}, 3, NOW).state, 'gone-expired', 'three days old, three days kept: expired before anything is asked');

    // cardView itself, handed a card, goes by when the card says it goes:
    // which, as PostCard asks only while loading, only ever makes a card
    // expire sooner than the count (the next case below). The clip made three
    // days ago, and a card that says it goes tomorrow:
    const goesTomorrow = {...SERVER_CARD, expiresAt: NOW + DAY};
    assert.equal(card(clipPost(), true, {status: 'ok', card: goesTomorrow}, 3, NOW).state, 'live');
    // To the moment.
    assert.equal(card(clipPost(), true, {status: 'ok', card: goesTomorrow}, 3, NOW + DAY - 1).state, 'live');
    const gone = card(clipPost(), true, {status: 'ok', card: goesTomorrow}, 3, NOW + DAY);
    assert.equal(gone.state, 'gone-expired');
    assert.equal(gone.sentence, 'This clip has expired.');
    assert.equal(gone.watch, false);

    // And the other way: thirty days by the panel's count, gone already by
    // the server's.
    assert.equal(card(clipPost(), true, {status: 'ok', card: {...SERVER_CARD, expiresAt: NOW - 1}}, 30, NOW).state, 'gone-expired');

    // The server's mark on the post still comes first, and a card that says
    // nothing of when it goes (or nonsense) leaves the count as it was.
    assert.equal(card(clipPost({villanet_clip_gone: 'deleted'}), true, {status: 'ok', card: goesTomorrow}, 3, NOW).state, 'gone-deleted');
    assert.equal(card(clipPost(), true, {status: 'ok', card: SERVER_CARD}, 3, NOW).state, 'gone-expired');
    assert.equal(card(clipPost(), true, {status: 'ok', card: {...SERVER_CARD, expiresAt: 'soon'}}, 30, NOW).state, 'live');

    // Nobody without a card is told anything by one: a locked reader's is
    // never asked for.
    assert.equal(card(clipPost(), false, {status: 'idle'}, 3, NOW).state, 'gone-expired');
});

check('card: a deleted clip names nothing', () => {
    const scrubbed = clipPost({
        villanet_clip_gone: 'deleted',
        villanet_clip_title: undefined,
        villanet_clip_clipper: undefined,
        villanet_clip_clipper_id: undefined,
        villanet_clip_streamer: undefined,
        villanet_clip_streamer_id: undefined,
    });
    Object.keys(scrubbed.props).forEach((key) => scrubbed.props[key] === undefined && delete scrubbed.props[key]);
    [scrubbed, clipPost({villanet_clip_gone: 'deleted'})].forEach((post) => {
        const view = card(post, true, {status: 'ok', card: SERVER_CARD});
        assert.equal(view.state, 'gone-deleted');
        assert.equal(view.sentence, 'This clip was deleted.');
        assert.equal(view.text, undefined);
        assert.equal(view.label, 'Matty clip, deleted');
        assert.equal(view.watch, false);
        assert.ok(!JSON.stringify(view).includes('that jump'));
        assert.ok(!JSON.stringify(view).includes('Bob'));
    });
});

check('card: unknown and locked readers get the props and no picture', () => {
    const unknown = card(clipPost(), undefined, {status: 'ok', card: SERVER_CARD});
    assert.equal(unknown.state, 'unknown');
    const unlinked = card(clipPost({villanet_clip_title: 'what the post says'}), false, {status: 'ok', card: SERVER_CARD});
    assert.equal(unlinked.state, 'locked');
    [unknown, unlinked].forEach((view) => {
        assert.equal(view.picture, 'placeholder');
        assert.equal(view.thumb, undefined);
        assert.equal(view.watch, true);
        assert.equal(view.pulse, false);
    });
    // Even with a card somehow in hand, an unlinked reader sees only props.
    assert.equal(unlinked.text.title, 'what the post says');
    assert.equal(unlinked.text.length, '0:45');
    assert.equal(unlinked.text.stream, 'Dave’s stream');
    assert.equal(unlinked.watchLabel, 'Watch “what the post says” in Matty');
});

check('card: a linked reader\'s card follows its request', () => {
    assert.equal(card(clipPost(), true, {status: 'idle'}).state, 'loading');
    const loading = card(clipPost(), true, {status: 'loading'});
    assert.equal(loading.state, 'loading');
    assert.equal(loading.pulse, true);
    assert.equal(loading.watch, true);
    assert.equal(loading.picture, 'placeholder');

    const gone = card(clipPost(), true, {status: 'gone'});
    assert.equal(gone.state, 'gone');
    assert.equal(gone.sentence, 'This clip has gone — it expired or somebody deleted it.');
    assert.equal(gone.text, undefined);
    assert.equal(gone.label, 'Matty clip, gone');
    assert.equal(gone.watch, false);
    assert.equal(gone.dimmed, true);

    const failed = card(clipPost(), true, {status: 'error'});
    assert.equal(failed.state, 'error');
    assert.equal(failed.note, 'Couldn’t check this clip just now.');
    assert.equal(failed.text.title, 'that jump');
    assert.equal(failed.watch, true);
});

check('card: live shows the server\'s clip, never the props', () => {
    const forged = clipPost({villanet_clip_title: 'forged', villanet_clip_clipper: 'Mallory', villanet_clip_duration_ms: 1000});
    const live = card(forged, true, {status: 'ok', card: SERVER_CARD});
    assert.equal(live.state, 'live');
    assert.equal(live.id, '3f9a0c2e7b1d4a55');
    assert.equal(live.text.title, 'that jump');
    assert.equal(live.text.clipper.name, 'Bob');
    assert.equal(live.text.length, '0:45');
    assert.equal(live.picture, 'thumb');
    assert.equal(live.thumb, 'a1b2c3d4');
    assert.equal(live.label, 'Matty clip: “that jump”, 45 seconds, clipped by Bob from Dave’s stream');
    assert.equal(live.watchLabel, 'Watch “that jump” in Matty');

    const bare = card(clipPost(), true, {status: 'ok', card: {...SERVER_CARD, thumb: undefined, title: ''}});
    assert.equal(bare.picture, 'backdrop');
    assert.equal(bare.thumb, undefined);
    assert.equal(bare.text.title, '');
    assert.equal(bare.label, 'Matty clip, 45 seconds, clipped by Bob from Dave’s stream');
    assert.equal(bare.watchLabel, 'Watch Dave’s clip in Matty');

    // A stream nobody was linked to: its own name, and no face to show.
    const nobody = card(clipPost(), true, {status: 'ok', card: {...SERVER_CARD, thumb: '', title: '', streamer: {name: 'Stream 3f9a0c'}}});
    assert.equal(nobody.picture, 'placeholder');
    assert.equal(nobody.text.stream, 'Stream 3f9a0c');
    assert.equal(nobody.label, 'Matty clip, 45 seconds, clipped by Bob from Stream 3f9a0c');
    assert.equal(nobody.watchLabel, 'Watch this clip in Matty');
});

check('card: its words name what the server calls the plugin, whatever it is', () => {
    const name = 'Movie Night Club';
    const live = card(clipPost(), true, {status: 'ok', card: SERVER_CARD}, 30, NOW, name);
    assert.equal(live.label, 'Movie Night Club clip: “that jump”, 45 seconds, clipped by Bob from Dave’s stream');
    assert.equal(live.watchLabel, 'Watch “that jump” in Movie Night Club');
    const bare = card(clipPost(), true, {status: 'ok', card: {...SERVER_CARD, thumb: undefined, title: ''}}, 30, NOW, name);
    assert.equal(bare.watchLabel, 'Watch Dave’s clip in Movie Night Club');
    assert.equal(card(clipPost({villanet_clip_title: '', villanet_clip_clipper: '', villanet_clip_streamer: ''}), false, {status: 'idle'}, 30, NOW, name).watchLabel,
        'Watch this clip in Movie Night Club');
    assert.equal(card(clipPost({villanet_clip_gone: 'deleted'}), true, {status: 'idle'}, 30, NOW, name).label, 'Movie Night Club clip, deleted');
    assert.equal(card(clipPost(), true, {status: 'gone'}, 30, NOW, name).label, 'Movie Night Club clip, gone');
    assert.equal(card(clipPost({villanet_clip_gone: 'expired'}), true, {status: 'idle'}, 30, NOW, name).label,
        'Movie Night Club clip: “that jump”, 45 seconds, clipped by Bob from Dave’s stream, expired');
});

check('card: a blank "gone", which is how the server says none, is no mark', () => {
    // The server's hook can only blank a prop, never take it out, so "" is
    // its "none" (clips_share.go MessageWillBePosted).
    assert.equal(card(clipPost({villanet_clip_gone: ''}), true, {status: 'ok', card: SERVER_CARD}).state, 'live');
    assert.equal(card(clipPost({villanet_clip_gone: ''}), false).state, 'locked');
    assert.equal(card(clipPost({villanet_clip_gone: ''}), false, {status: 'idle'}, 3, NOW + DAY).state, 'gone-expired');
    // A blank title or name reads as none too.
    const blank = card(clipPost({villanet_clip_title: '', villanet_clip_clipper: '', villanet_clip_streamer: ''}), false);
    assert.equal(blank.text.title, '');
    assert.equal(blank.text.stream, '');
    assert.equal(blank.label, 'Matty clip, 45 seconds');
    assert.equal(blank.watchLabel, 'Watch this clip in Matty');
    // Only the clipper missing: still says whose stream.
    assert.equal(card(clipPost({villanet_clip_clipper: ''}), false).label, 'Matty clip: “that jump”, 45 seconds, from Dave’s stream');
});

check('card: the stream\'s tag, from the props for everybody and from the server for a linked reader', () => {
    const tagged = clipPost({villanet_clip_tag: 'Hytale', villanet_clip_tag_kind: 'game'});
    const unlinked = card(tagged, false);
    assert.deepEqual(unlinked.text.tag, {title: 'Hytale', category: 'game', word: 'Game'});
    assert.equal(unlinked.label, 'Matty clip: “that jump” — Hytale, 45 seconds, clipped by Bob from Dave’s stream');
    assert.equal(card(tagged, undefined).text.tag.title, 'Hytale');

    // A show is drawn with the movie icon and called what it is.
    assert.deepEqual(card(clipPost({villanet_clip_tag: 'Severance', villanet_clip_tag_kind: 'tv'}), false).text.tag,
        {title: 'Severance', category: 'movie', word: 'TV series'});
    // A kind the server never writes keeps the words and drops the kind;
    // no words, no tag.
    assert.deepEqual(card(clipPost({villanet_clip_tag: 'Elden Ring', villanet_clip_tag_kind: 'book'}), false).text.tag,
        {title: 'Elden Ring', category: '', word: ''});
    assert.equal(card(clipPost({villanet_clip_tag: '  ', villanet_clip_tag_kind: 'game'}), false).text.tag, undefined);
    assert.equal(card(clipPost(), false).text.tag, undefined);
    // Not a string: the props are not the plugin's.
    assert.equal(card(clipPost({villanet_clip_tag: {title: 'Hytale'}}), false).state, 'fallback');
    assert.equal(card(clipPost({villanet_clip_tag_kind: 7}), false).state, 'fallback');

    // Expired keeps it; deleted and gone name nothing, the tag included.
    assert.equal(card(clipPost({villanet_clip_tag: 'Hytale', villanet_clip_gone: 'expired'}), true).text.tag.title, 'Hytale');
    assert.equal(card(clipPost({villanet_clip_tag: 'Hytale', villanet_clip_gone: 'expired'}), true).label,
        'Matty clip: “that jump” — Hytale, 45 seconds, clipped by Bob from Dave’s stream, expired');
    const deleted = card(clipPost({villanet_clip_tag: 'Hytale', villanet_clip_gone: 'deleted'}), true);
    assert.ok(!JSON.stringify(deleted).includes('Hytale'));
    assert.ok(!JSON.stringify(card(tagged, true, {status: 'gone'})).includes('Hytale'));

    // A linked reader sees the server's tag, whatever the post says.
    const fromServer = card(tagged, true, {status: 'ok', card: {...SERVER_CARD, tag: {category: 'movie', title: 'Heat',
        media: {ref: 'tmdb:movie:949', kind: 'movie', title: 'Heat', year: '1995', source: 'tmdb'}}}});
    assert.deepEqual(fromServer.text.tag, {title: 'Heat', category: 'movie', word: 'Movie'});
    assert.equal(fromServer.label, 'Matty clip: “that jump” — Heat, 45 seconds, clipped by Bob from Dave’s stream');
    assert.equal(card(tagged, true, {status: 'ok', card: SERVER_CARD}).text.tag, undefined);
    // No title: the tag still follows the words.
    assert.equal(card(clipPost({villanet_clip_title: '', villanet_clip_tag: 'Hytale', villanet_clip_tag_kind: 'game'}), false).label,
        'Matty clip — Hytale, 45 seconds, clipped by Bob from Dave’s stream');
});

check('card: when a mounting card asks the server again', () => {
    assert.equal(clips.cardNeedsFetch(undefined, NOW), true);
    assert.equal(clips.cardNeedsFetch({status: 'gone', at: 0}, NOW), false);
    assert.equal(clips.cardNeedsFetch({status: 'loading', at: 0}, NOW), false);
    assert.equal(clips.cardNeedsFetch({status: 'ok', at: NOW - clips.CARD_FRESH_MS + 1}, NOW), false);
    assert.equal(clips.cardNeedsFetch({status: 'ok', at: NOW - clips.CARD_FRESH_MS}, NOW), true);
    assert.equal(clips.CARD_FRESH_MS, 600000);
    assert.equal(clips.cardNeedsFetch({status: 'error', at: NOW - clips.CARD_RETRY_MS + 1}, NOW), false);
    assert.equal(clips.cardNeedsFetch({status: 'error', at: NOW - clips.CARD_RETRY_MS}, NOW), true);
    // Marked old when a clip went: asked on the next mount.
    assert.equal(clips.cardNeedsFetch({status: 'ok', at: 0}, NOW), true);
});

// ---- Clips: the scissors (spec 3.4, addendum 3.8) ----

const liveTile = (more = {}) => ({kind: 'live', alwaysOn: false, clip: 'ready', clipFrom: NOW - 600000, ...more});

const scissors = (tile, more = {}) => clips.scissorsView({
    tile,
    linked: true,
    clipsEnabled: true,
    lengthSeconds: 120,
    name: 'Dave',
    cutting: false,
    now: NOW,
    ...more,
});

const HINT = 'In the editor, I and O set the start and end at the playhead.';

check('scissors: none while the link is unknown, Clips are off, on 24/7 channels, or with nothing to clip', () => {
    assert.equal(scissors(liveTile(), {linked: undefined}), null);
    assert.equal(scissors(liveTile(), {clipsEnabled: false}), null);
    assert.equal(scissors(liveTile({alwaysOn: true})), null);
    assert.equal(scissors(liveTile({alwaysOn: true}), {linked: false}), null);
    assert.equal(scissors(liveTile({clip: undefined})), null);
    assert.equal(scissors(liveTile({clip: undefined}), {linked: false}), null);
    assert.equal(scissors(liveTile({clip: undefined, clipTail: NOW})), null);
    assert.equal(scissors({kind: 'waiting'}), null);
    assert.equal(scissors({kind: 'waiting', tailUntil: NOW}), null);
    assert.equal(scissors({kind: 'waiting', tailUntil: NOW}, {linked: false}), null);
    // A state from a newer server: nothing rather than a guess.
    assert.equal(scissors(liveTile({clip: 'mystery', clipFrom: undefined})), null);
});

check('scissors: nobody but a linked reader has any — let in to watch only, or locked, gets none rather than a prompt', () => {
    [
        liveTile(),
        liveTile({clipFrom: NOW - 3000}),
        liveTile({clip: 'starting', clipFrom: undefined}),
        liveTile({clip: 'failed'}),
        liveTile({clip: 'off'}),
        liveTile({clip: 'unsupported'}),
        liveTile({clip: undefined, clipTail: NOW + 1}),
        {kind: 'waiting', tailUntil: NOW + 60000},
    ].forEach((tile, index) => {
        assert.equal(scissors(tile, {linked: false}), null, `let in ${index}`);
        assert.equal(scissors(tile, {linked: false, cutting: true}), null, `let in cutting ${index}`);
        assert.equal(scissors(tile, {linked: undefined}), null, `not known ${index}`);
    });
});

check('scissors: the linked rows, top to bottom', () => {
    const cutting = scissors(liveTile(), {cutting: true});
    assert.deepEqual(cutting, {action: 'none', disabled: true, busy: true, label: 'Clipping…', title: 'Clipping…'});
    assert.equal(scissors({kind: 'waiting', tailUntil: NOW + 1}, {cutting: true}).busy, true);

    const full = scissors(liveTile({clipFrom: NOW - 120000}));
    assert.deepEqual(full, {action: 'clip', source: 'live', disabled: false, busy: false,
        label: 'Clip the last 2 minutes', title: `Clip the last 2:00 (C)\n${HINT}`});

    const partial = scissors(liveTile({clipFrom: NOW - 47900}));
    assert.equal(partial.action, 'clip');
    assert.equal(partial.source, 'live');
    assert.equal(partial.tag, '0:47');
    assert.equal(partial.label, 'Clip the last 47 seconds — all that has been recorded so far');
    assert.equal(partial.title, `Clip the last 47 seconds — all that has been recorded so far\n${HINT}`);
    assert.equal(scissors(liveTile({clipFrom: NOW - 5000})).tag, '0:05');
    assert.equal(scissors(liveTile({clipFrom: NOW - 119999})).tag, '1:59');

    const early = scissors(liveTile({clipFrom: NOW - 4999}));
    assert.deepEqual(early, {action: 'none', disabled: true, busy: false, label: 'Clipping starts in a few seconds', title: 'Clipping starts in a few seconds'});
    assert.equal(scissors(liveTile({clip: 'starting', clipFrom: undefined})).label, 'Clipping starts in a few seconds');
    assert.equal(scissors(liveTile({clipFrom: undefined})).label, 'Clipping starts in a few seconds');

    // Not ready with five seconds, but the end before a drop is held.
    const dropped = scissors(liveTile({clip: 'starting', clipFrom: undefined, clipTail: NOW + 60000}));
    assert.deepEqual(dropped, {action: 'clip', source: 'tail', disabled: false, busy: false,
        label: 'Clip the last 2:00 before the stream dropped', title: `Clip the last 2:00 before the stream dropped\n${HINT}`});
    assert.equal(scissors(liveTile({clipFrom: NOW - 3000, clipTail: NOW + 1})).source, 'tail');
    assert.equal(scissors(liveTile({clip: 'failed', clipTail: NOW + 1})).source, 'tail');
    assert.equal(scissors(liveTile({clip: undefined, clipTail: NOW + 1})).source, 'tail');
    // Ready with enough beats a held tail; a tail past its time is not held.
    assert.equal(scissors(liveTile({clipTail: NOW + 1})).source, 'live');
    assert.equal(scissors(liveTile({clip: 'starting', clipFrom: undefined, clipTail: NOW})).label, 'Clipping starts in a few seconds');

    const waiting = scissors({kind: 'waiting', tailUntil: NOW + 60000});
    assert.equal(waiting.source, 'tail');
    assert.equal(waiting.label, 'Clip the last 2:00 before Dave went off');

    // As much as is held, and why it stopped, as Just ended says them.
    assert.equal(scissors({kind: 'waiting', tailUntil: NOW + 60000, tailHeldMs: 47000, tailReason: 'dropped'}).label,
        'Clip the last 0:47 before the stream dropped');
    assert.equal(scissors({kind: 'waiting', tailUntil: NOW + 60000, tailHeldMs: 47000, tailReason: 'ended'}).label,
        'Clip the last 0:47 before Dave went off');
    assert.equal(scissors({kind: 'waiting', tailUntil: NOW + 60000, tailHeldMs: 600000, tailReason: 'ended'}).label,
        'Clip the last 2:00 before Dave went off', 'never more than a clip');
    assert.equal(scissors(liveTile({clip: 'starting', clipFrom: undefined, clipTail: NOW + 60000, tailHeldMs: 31000})).label,
        'Clip the last 0:31 before the stream dropped');

    const disabled = {
        failed: 'Clipping isn’t working on this stream just now',
        full: 'The server is out of room for clipping just now',
        queued: 'Clipping is busy with other streams right now',
        off: 'Dave has switched clipping off',
        unsupported: 'This stream’s video format can’t be clipped yet',
    };
    Object.entries(disabled).forEach(([state, label]) => {
        assert.deepEqual(scissors(liveTile({clip: state, clipFrom: undefined})), {action: 'none', disabled: true, busy: false, label, title: label});
    });
});

check('scissors: the length comes from the setting', () => {
    const ninety = scissors(liveTile(), {lengthSeconds: 90});
    assert.equal(ninety.label, 'Clip the last 90 seconds');
    assert.equal(ninety.title, `Clip the last 1:30 (C)\n${HINT}`);
    assert.equal(scissors(liveTile(), {lengthSeconds: 300}).label, 'Clip the last 5 minutes');
    assert.equal(scissors(liveTile({clipFrom: NOW - 299000}), {lengthSeconds: 300}).tag, '4:59');
    assert.equal(scissors({kind: 'waiting', tailUntil: NOW + 1}, {lengthSeconds: 60}).label, 'Clip the last 1:00 before Dave went off');
});

// ---- Clips: the trimmer (spec 3.7) ----

check('trimmer: it starts on the whole draft and only shrinks it', () => {
    assert.deepEqual(clips.trimFrom(118043), {inMs: 0, outMs: 118043, durationMs: 118043});
    assert.deepEqual(clips.trimFrom(-5), {inMs: 0, outMs: 0, durationMs: 0});
});

check('trimmer: the start keeps to 100 ms steps and five seconds before the end', () => {
    const trim = {inMs: 0, outMs: 120000, durationMs: 120000};
    assert.equal(clips.clampIn(-500, trim), 0);
    assert.equal(clips.clampIn(37449, trim), 37400);
    assert.equal(clips.clampIn(37450, trim), 37500);
    assert.equal(clips.clampIn(116000, trim), 115000);
    assert.equal(clips.clampIn(130000, {...trim, outMs: 45000}), 40000);
    // The handles never cross.
    assert.equal(clips.clampIn(45000, {...trim, outMs: 45000}), 40000);
});

check('trimmer: the end keeps five seconds after the start and reaches the end exactly', () => {
    const trim = {inMs: 10000, outMs: 119990, durationMs: 119990};
    assert.equal(clips.clampOut(3000, trim), 15000);
    assert.equal(clips.clampOut(10000, trim), 15000);
    assert.equal(clips.clampOut(83449, trim), 83400);
    assert.equal(clips.clampOut(119960, trim), 119990);
    assert.equal(clips.clampOut(119990, trim), 119990);
    assert.equal(clips.clampOut(500000, trim), 119990);
    // A draft too short to trim stays whole.
    const tiny = {inMs: 0, outMs: 3000, durationMs: 3000};
    assert.equal(clips.clampIn(1000, tiny), 0);
    assert.equal(clips.clampOut(1000, tiny), 3000);
    assert.equal(clips.clampPosition(-1, 3000), 0);
    assert.equal(clips.clampPosition(1234, 3000), 1234);
    assert.equal(clips.clampPosition(9000, 3000), 3000);
});

check('trimmer: keys move a thumb within its own range, and leave other keys alone', () => {
    const trim = {inMs: 20000, outMs: 80000, durationMs: 120000};
    const key = (thumb, name, value, shift = false) => clips.trimKey(thumb, name, shift, value, trim);
    assert.equal(key('start', 'ArrowRight', 20000), 21000);
    assert.equal(key('start', 'ArrowUp', 20000), 21000);
    assert.equal(key('start', 'ArrowLeft', 20000), 19000);
    assert.equal(key('start', 'ArrowDown', 20000), 19000);
    assert.equal(key('start', 'ArrowRight', 20000, true), 30000);
    assert.equal(key('start', 'PageUp', 20000), 30000);
    assert.equal(key('start', 'PageDown', 20000), 10000);
    assert.equal(key('start', 'ArrowLeft', 500), 0);
    assert.equal(key('start', 'Home', 20000), 0);
    assert.equal(key('start', 'End', 20000), 75000);
    assert.equal(key('start', 'PageUp', 72000), 75000);

    assert.equal(key('end', 'ArrowLeft', 80000), 79000);
    assert.equal(key('end', 'ArrowRight', 80000, true), 90000);
    assert.equal(key('end', 'End', 80000), 120000);
    assert.equal(key('end', 'Home', 80000), 25000);
    assert.equal(key('end', 'PageDown', 28000), 25000);

    assert.equal(key('position', 'ArrowRight', 1234), 2234);
    assert.equal(key('position', 'ArrowLeft', 1234, true), 0);
    assert.equal(key('position', 'End', 1234), 120000);
    assert.equal(key('position', 'Home', 50000), 0);

    ['a', 'c', ' ', 'Enter', 'Tab', 'Escape', 'i', 'o'].forEach((name) => {
        assert.equal(key('start', name, 20000), null, name);
        assert.equal(key('position', name, 20000), null, name);
    });
});

// ---- Clips: times and words ----

check('clip times and lengths in words', () => {
    assert.equal(clips.clipTime(45000), '0:45');
    assert.equal(clips.clipTime(119960), '2:00');
    assert.equal(clips.clipTime(118043), '1:58');
    assert.equal(clips.clipTime(0), '0:00');
    assert.equal(clips.clipTime(-10), '0:00');
    assert.equal(clips.clipTime(600000), '10:00');
    assert.equal(clips.positionTime(37900), '0:37');
    assert.equal(clips.positionTime(83000), '1:23');
    assert.equal(clips.lengthInWords(1), '1 second');
    assert.equal(clips.lengthInWords(45), '45 seconds');
    assert.equal(clips.lengthInWords(60), '1 minute');
    assert.equal(clips.lengthInWords(90), '90 seconds');
    assert.equal(clips.lengthInWords(120), '2 minutes');
    assert.equal(clips.lengthInWords(150), '2 minutes 30 seconds');
    assert.equal(clips.lengthInWords(300), '5 minutes');
    assert.equal(clips.streamLabel({userId: 'u-dave', name: 'Dave'}), 'Dave’s stream');
    assert.equal(clips.streamLabel({name: 'Stream 3f9a0c'}), 'Stream 3f9a0c');
});

// ---- Clips: the Share button's label (addendum 4.10) ----

check('share label: where it goes, by the kind of channel', () => {
    assert.deepEqual(clips.shareLabel({kind: ''}), {label: 'Share', where: '', title: 'Open a channel to share this clip', disabled: true});
    assert.deepEqual(clips.shareLabel({kind: 'O', displayName: 'Town Square'}),
        {label: 'Share in Town Square', where: 'in Town Square', title: '', disabled: false});
    assert.equal(clips.shareLabel({kind: 'P', displayName: 'Crew'}).label, 'Share in Crew');
    assert.equal(clips.shareLabel({kind: 'D', otherName: 'Dave Hill'}).label, 'Share with Dave Hill');
    assert.equal(clips.shareLabel({kind: 'D', otherName: 'Dave Hill'}).where, 'with Dave Hill');
    assert.equal(clips.shareLabel({kind: 'G', displayName: 'bob, dave, victor'}).label, 'Share in this group message');
    assert.equal(clips.shareLabel({kind: 'unknown'}).label, 'Share in this channel');
    assert.equal(clips.shareLabel({kind: 'O', displayName: '  '}).label, 'Share in this channel');
    assert.equal(clips.shareLabel({kind: 'D', otherName: ''}).label, 'Share in this channel');
    assert.equal(clips.shareLabel({kind: 'D', otherName: 'Dave', displayName: 'ignored'}).label, 'Share with Dave');
});

check('share label: never into a shared channel', () => {
    ['O', 'P', 'D', 'G', 'unknown'].forEach((kind) => {
        const label = clips.shareLabel({kind, displayName: 'Partners', otherName: 'Remote Rupert', shared: true});
        assert.equal(label.disabled, true, kind);
        assert.equal(label.title, 'Clips can’t be shared into a shared channel', kind);
    });
    assert.equal(clips.shareLabel({kind: 'O', displayName: 'Partners', shared: true}).label, 'Share in Partners');
    assert.equal(clips.shareLabel({kind: 'O', displayName: 'Partners', shared: false}).disabled, false);
    assert.equal(clips.shareLabel({kind: 'O', displayName: 'Partners', shared: null}).disabled, false);
});

check('share label: names past 32 characters are cut, whole in the tooltip', () => {
    const exact = 'A channel name of 32 characters!';
    assert.equal(Array.from(exact).length, 32);
    assert.deepEqual(clips.shareLabel({kind: 'O', displayName: exact}),
        {label: `Share in ${exact}`, where: `in ${exact}`, title: '', disabled: false});

    const long = 'Erin Pemberton-Whitfield of the Sunday Racing League';
    const cut = clips.shareLabel({kind: 'D', otherName: long});
    const name = cut.where.replace(/^with /, '');
    assert.equal(Array.from(name).length, 32);
    assert.ok(name.endsWith('…'));
    assert.ok(long.startsWith(name.slice(0, -1)));
    assert.equal(cut.label, `Share with ${name}`);
    assert.equal(cut.title, `Share with ${long}`);
    assert.equal(cut.disabled, false);

    // Characters, not UTF-16 units: an emoji is never split.
    const emoji = `${'🏁'.repeat(40)}`;
    const flags = clips.shareLabel({kind: 'O', displayName: emoji}).where.replace(/^in /, '');
    assert.equal(Array.from(flags).length, 32);
    assert.equal(flags, `${'🏁'.repeat(31)}…`);

    // A space before the cut is not left hanging before the "…".
    const spaced = `${'a'.repeat(30)} bcdef`;
    assert.equal(clips.shareLabel({kind: 'O', displayName: spaced}).where, `in ${'a'.repeat(30)}…`);

    // A shared channel's reason wins over the long name.
    assert.equal(clips.shareLabel({kind: 'O', displayName: long, shared: true}).title, 'Clips can’t be shared into a shared channel');
});

check('share words: the second press, and where it went', () => {
    assert.equal(clips.shareConfirm(clips.shareLabel({kind: 'O', displayName: 'Town Square'}).where), 'Click again to post it in Town Square');
    assert.equal(clips.shareConfirm(clips.shareLabel({kind: 'G', displayName: 'x'}).where), 'Click again to post it in this group message');
    assert.equal(clips.shareConfirm(clips.shareLabel({kind: 'D', otherName: 'Dave Hill'}).where), 'Click again to send it to Dave Hill');
    assert.equal(clips.shareConfirm(clips.shareLabel({kind: 'unknown'}).where), 'Click again to post it in this channel');
    assert.equal(clips.shareDone('in Town Square'), 'Shared in Town Square.');
    assert.equal(clips.shareDone('with Dave Hill'), 'Shared with Dave Hill.');
    // The share's own refusals, in the addendum's words.
    assert.equal(clipCopy('cannot_post', 'share').text, 'You can’t post in that channel.');
    assert.equal(clipCopy('share_cap', 'share').text, 'This clip has already been shared in 10 places, which is the most it can be.');
    assert.equal(clipCopy('rate_limited', 'share').text, 'That’s a lot of sharing in a row. Try again in a few minutes.');
    assert.equal(clipCopy('clip_gone', 'share').text, 'This clip has gone, so it can’t be shared.');
    assert.deepEqual(clipCopy('network', 'share'), {text: 'The clip could not be shared just now.', actions: ['retry']});
    assert.deepEqual(clipCopy('not_linked', 'share'), {text: 'Matty is locked.', actions: []});
    assert.deepEqual(clipCopy('watch_only', 'share'), {text: 'You can watch, but not do that.', actions: []});
});

// ---- What a refusal says, and the Clips dialog's words (spec 3.9, 3.10, 3.13; addendum 3.7) ----

check('refusal copy: locked and watch only (and not_linked, from a server before the lock), by where it was heard', () => {
    // No call to link anywhere but the editor: the panel is the lock.
    assert.deepEqual(clipCopy('locked', 'list'), {text: 'Matty is locked.', actions: []});
    assert.deepEqual(clipCopy('locked', 'clip', {brand: 'Movie Night Club'}), {text: 'Movie Night Club is locked.', actions: []}, 'the server’s name, whatever it is');
    assert.deepEqual(clipCopy('not_linked', 'list'), clipCopy('locked', 'list'));
    assert.deepEqual(clipCopy('not_linked', 'clip'), clipCopy('locked', 'clip'));
    assert.deepEqual(clipCopy('watch_only', 'vote'), {text: 'You can watch, but not do that.', actions: []});
    assert.equal(clipCopy('watch_only', 'report').text, 'You can watch, but not do that.');

    // The editor's own words, for a draft open when the link went: link it
    // again (the let-in footer's bar), or leave it.
    for (const code of ['watch_only', 'locked', 'not_linked']) {
        const editor = clipCopy(code, 'editor', {keptUntil: '21:40'});
        assert.equal(editor.text, 'Your stream is no longer linked. Link it again to publish this draft (kept until 21:40).', code);
        // No Discard: unlinked people cannot delete their own drafts either
        // (Victor's answer to question B), so the server would refuse it.
        assert.deepEqual(editor.actions, ['link', 'later'], code);
        assert.deepEqual(clipCopy(code, 'publish').actions, ['link', 'later'], code);
    }
    assert.equal(clipCopy('watch_only', 'editor').text, 'Your stream is no longer linked. Link it again to publish this draft.');
});

check('refusal copy: no answer, by surface', () => {
    assert.deepEqual(clipCopy('network', 'capture'), {text: 'That stream could not be clipped just now.', actions: ['retry']});
    assert.deepEqual(clipCopy('network', 'publish'), {text: 'Checking whether it was published…', actions: ['check']});
    assert.deepEqual(clipCopy('network', 'share'), {text: 'The clip could not be shared just now.', actions: ['retry']});
    assert.deepEqual(clipCopy('network', 'list'), {text: 'Clips could not be loaded just now.', actions: ['retry']});
    assert.equal(clipCopy('store', 'publish').text, 'The clip could not be published just now. Your draft is still here.');
    assert.deepEqual(clipCopy('store', 'publish').actions, ['retry']);
    assert.equal(clipCopy('store', 'list').text, 'Clips could not be loaded just now.');
});

check('refusal copy: the codes that depend on where they were heard', () => {
    assert.equal(clipCopy('rate_limited', 'capture').text, 'That’s a lot of clips in a row. Try again in a minute.');
    assert.equal(clipCopy('rate_limited', 'vote').text, 'Slow down a little.');
    assert.equal(clipCopy('rate_limited', 'share').text, 'That’s a lot of sharing in a row. Try again in a few minutes.');
    assert.equal(clipCopy('clip_gone', 'clip').text, 'This clip has gone — it expired or somebody deleted it.');
    assert.equal(clipCopy('clip_gone', 'share').text, 'This clip has gone, so it can’t be shared.');
    assert.equal(clipCopy('not_watching', 'capture').text, 'Tune in to this stream to clip it.');
    assert.equal(clipCopy('not_watching', 'tail').text, 'Only people who were watching when it ended can clip it.');
    assert.equal(clipCopy('streamer_off', 'capture', {name: 'Dave'}).text, 'Dave has switched clipping off.');
    assert.equal(clipCopy('streamer_off', 'capture').text, 'The streamer has switched clipping off.');
});

check('refusal copy: lengths come from the setting, and the actions are offered', () => {
    assert.equal(clipCopy('draft_gone', 'editor', {lengthSeconds: 120}).text,
        'This draft has expired. Clip again while the moment is still in the last 2 minutes.');
    assert.equal(clipCopy('draft_gone', 'editor', {lengthSeconds: 300}).text,
        'This draft has expired. Clip again while the moment is still in the last 5 minutes.');
    assert.deepEqual(clipCopy('draft_cap', 'capture').actions, ['drafts']);
    assert.deepEqual(clipCopy('duplicate', 'publish').actions, ['clips']);
    assert.deepEqual(clipCopy('user_storage_full', 'publish').actions, ['yours']);
    assert.deepEqual(clipCopy('warming_up', 'capture').actions, ['retry']);
    assert.deepEqual(clipCopy('publishing', 'publish').actions, ['check']);
    assert.equal(clipCopy('share_cap', 'share').text, 'This clip has already been shared in 10 places, which is the most it can be.');
    assert.equal(clipCopy('cannot_post', 'share').text, 'You can’t post in that channel.');
    assert.equal(clipCopy('own_clip', 'vote').text, 'You can’t upvote your own clip.');
});

check('refusal copy: a code with no words of its own says the server’s, or the surface’s', () => {
    assert.equal(clipCopy('not_allowed', 'delete', {}, 'Only the person who made it can do that.').text,
        'Only the person who made it can do that.');
    assert.equal(clipCopy('not_allowed', 'delete').text, 'The clip could not be deleted just now.');
    assert.equal(clipCopy('something_new', 'report', {}, '  Newer words.  ').text, 'Newer words.');
    assert.equal(clipCopy('something_new', 'list').text, 'Clips could not be loaded just now.');

    // Mattermost's own 401, or a proxy's 404, carries no words of the
    // plugin's: the client passes '' (never its "Clip request failed (401)"
    // placeholder), and the surface's own words show.
    assert.equal(clipCopy('forbidden', 'vote', {}, '').text, 'Your upvote could not be saved just now.');
    assert.equal(clipCopy('bad_request', 'report', {}, '').text, 'The report could not be sent just now.');
});

const clipOf = (id, createdAt, upvotes) => ({id, createdAt, upvotes});

check('clip order: Top is most upvoted, a tie newest first; New is newest first', () => {
    const list = [clipOf('a', 100, 5), clipOf('b', 300, 5), clipOf('c', 200, 9), clipOf('d', 400, 0)];
    assert.deepEqual(clips.orderClips(list, 'top').map((clip) => clip.id), ['c', 'b', 'a', 'd']);
    assert.deepEqual(clips.orderClips(list, 'new').map((clip) => clip.id), ['d', 'b', 'c', 'a']);
    assert.deepEqual(list.map((clip) => clip.id), ['a', 'b', 'c', 'd'], 'the list itself is left alone');
    assert.deepEqual(clips.orderClips([clipOf('y', 1, 1), clipOf('x', 1, 1)], 'top').map((clip) => clip.id), ['x', 'y']);
});

check('clip order while the list is open: an upvote moves nothing, a new clip takes its sorted place', () => {
    // a was voted up past b: the fresh sort says a, b, c; what is shown stays b, a, c.
    assert.deepEqual(clips.steadyOrder(['b', 'a', 'c'], ['a', 'b', 'c']), ['b', 'a', 'c']);

    // n arrives, sorted first: it goes first, and the rest keep their order among themselves.
    assert.deepEqual(clips.steadyOrder(['b', 'a', 'c'], ['n', 'a', 'b', 'c']), ['n', 'b', 'a', 'c']);

    // b has gone: the rest close up in the order they were in.
    assert.deepEqual(clips.steadyOrder(['b', 'a', 'c'], ['c', 'a']), ['a', 'c']);

    // Nothing shown before: the fresh sort, as it is.
    assert.deepEqual(clips.steadyOrder([], ['a', 'b']), ['a', 'b']);
    assert.deepEqual(clips.steadyOrder(['a'], []), []);
});

check('clip ages', () => {
    const at = 1_000_000_000_000;
    assert.equal(clips.agoWords(at, at + 2000), 'just now');
    assert.equal(clips.agoWords(at, at + 40000), '40 s ago');
    assert.equal(clips.agoWords(at, at + (2 * 60000) + 5000), '2 min ago');
    assert.equal(clips.agoWords(at, at + 3600000), '1 hour ago');
    assert.equal(clips.agoWords(at, at + (5 * 3600000)), '5 hours ago');
    assert.equal(clips.agoWords(at, at + (3 * DAY)), '3 days ago');
    assert.equal(clips.agoWords(at, at - 5000), 'just now', 'a clock a little behind is not the future');
});

check('when a clip or a draft goes, in words, by the reader’s calendar', () => {
    const morning = new Date(2026, 9, 2, 8, 0, 0).getTime();
    assert.equal(clips.goesWords(morning + 30000, morning), 'goes in a moment');
    assert.equal(clips.goesWords(morning + (52 * 60000), morning), 'goes in 52 min');
    assert.equal(clips.goesWords(morning + (5 * 3600000), morning), 'goes in 5 hours');
    assert.equal(clips.goesWords(morning + (14 * 3600000), morning), 'goes today');
    assert.equal(clips.goesWords(new Date(2026, 9, 3, 18, 0).getTime(), morning), 'goes tomorrow');
    assert.equal(clips.goesWords(new Date(2026, 9, 29, 8, 0).getTime(), morning), 'goes in 27 days');
});

check('the expiry tag shows only in the last three days', () => {
    const now = 1_000_000_000_000;
    assert.equal(clips.expiryTag(now + (4 * DAY), now), null);
    assert.equal(clips.expiryTag(now + (3 * DAY), now), 'GOES IN 3D');
    assert.equal(clips.expiryTag(now + (2 * DAY) - 60000, now), 'GOES IN 2D');
    assert.equal(clips.expiryTag(now + DAY + (20 * 3600000), now), 'GOES IN 2D');
    assert.equal(clips.expiryTag(now + (19 * 3600000), now), 'GOES IN 19H');
    assert.equal(clips.expiryTag(now + (20 * 60000), now), 'GOES IN 1H');
});

const bob = {userId: 'u-bob', name: 'Bob'};
const dave = {userId: 'u-dave', name: 'Dave'};

check('a clip’s names: the upvote keeps one name whether pressed or not', () => {
    assert.equal(clips.upvoteLabel({title: 'that jump', streamer: dave, upvotes: 14}), 'Upvote “that jump”, 14 upvotes');
    assert.equal(clips.upvoteLabel({title: '', streamer: dave, upvotes: 1}), 'Upvote Dave’s clip, 1 upvote');
    assert.equal(clips.upvoteLabel({title: '', streamer: {name: 'Stream 3f9a0c'}, upvotes: 0}), 'Upvote this clip, 0 upvotes');

    const now = 1_000_000_000_000;
    const card = {clipper: bob, streamer: dave, durationMs: 45000, createdAt: now - (3 * DAY), upvotes: 14};
    assert.equal(clips.clipCardLabel(card, 'that jump', now),
        'Play that jump — clipped by Bob from Dave’s stream, 45 seconds, 3 days ago, 14 upvotes');
    // With what the stream was tagged with (addendum 4.14).
    assert.equal(clips.clipCardLabel(card, 'that jump', now, 'Hytale'),
        'Play that jump — Hytale, clipped by Bob from Dave’s stream, 45 seconds, 3 days ago, 14 upvotes');
    assert.equal(clips.clipCardLabel(card, 'that jump', now, '  '),
        'Play that jump — clipped by Bob from Dave’s stream, 45 seconds, 3 days ago, 14 upvotes');
});

check('what Delete says it will do to the upvotes', () => {
    const clip = (more) => ({mine: false, ofMyStream: false, upvotes: 14, clipper: bob, ...more});
    assert.equal(clips.deleteConfirm(clip({mine: true}), false), 'Click again — its 14 upvotes stop counting');
    assert.equal(clips.deleteConfirm(clip({}), true), 'Click again — its 14 upvotes stop counting');
    assert.equal(clips.deleteConfirm(clip({ofMyStream: true}), false), 'Click again to delete — Bob keeps its upvotes');
    assert.equal(clips.deleteConfirm(clip({ofMyStream: true}), true), 'Click again — its 14 upvotes stop counting', 'an admin’s delete voids them');
    assert.equal(clips.deleteConfirm(clip({mine: true, upvotes: 1}), false), 'Click again — its 1 upvote stops counting');
    assert.equal(clips.deleteConfirm(clip({mine: true, upvotes: 0}), false), 'Click again to delete it');
});

check('a stream that went off, or dropped, with its end still held', () => {
    const now = 1_000_000_000_000;
    const ended = {title: 'Dave', userId: 'u-dave', reason: 'ended', endedAt: now - 120000, until: now + (8 * 60000), heldMs: 120000};
    assert.deepEqual(clips.tailLine(ended, now, 120),
        {text: 'Dave went off 2 min ago — the last 2:00 is held for 8 more minutes.', button: 'Clip the end', label: 'Clip the end of Dave’s stream'});

    const dropped = {...ended, reason: 'dropped', endedAt: now - 40000, until: now + 30000, heldMs: 47000};
    assert.deepEqual(clips.tailLine(dropped, now, 120),
        {text: 'Dave’s stream dropped 40 s ago — the last 0:47 is held for 1 more minute.', button: 'Clip before the drop', label: 'Clip Dave’s stream before the drop'});
    assert.equal(clips.tailLine({...dropped, userId: undefined, title: 'Stream 3f9a0c'}, now, 120).text.split(' — ')[0],
        'Stream 3f9a0c dropped 40 s ago');
    assert.equal(clips.tailLine({...ended, userId: undefined, title: 'Stream 3f9a0c'}, now, 120).label, 'Clip the end of Stream 3f9a0c');
});

check('the ends still held, by the browser’s clock, and when the first goes', () => {
    const now = 1_000_000_000_000;
    const tails = [
        {handle: 'h-dave', until: now + 60000},
        {handle: 'h-bob', until: now + 1000},
        {handle: 'h-gone', until: now},
        {handle: 'h-erin', until: now + 30000},
    ];
    assert.deepEqual(clips.heldTails(tails, now).map((tail) => tail.handle), ['h-dave', 'h-bob', 'h-erin']);
    assert.deepEqual(clips.heldTails(tails, now, ['h-bob', 'h-victor']).map((tail) => tail.handle), ['h-dave', 'h-erin'],
        'a stream live again is its own tile’s to clip');
    assert.deepEqual(clips.heldTails(undefined, now), []);
    assert.equal(clips.nextTailEnd(clips.heldTails(tails, now)), now + 1000);
    assert.equal(clips.nextTailEnd([]), 0);
});

// ---- 3.2.1: downloading a clip (design/3.2.1.md 1.6) ----

check('Download shows in the clip viewer only for a linked reader the server says may download that clip', () => {
    assert.equal(clips.clipDownloadOffered({canDownload: true}, 'linked'), true);
    assert.equal(clips.clipDownloadOffered({canDownload: false}, 'linked'), false, 'the admin’s switch, or the streamer’s, said no');
    assert.equal(clips.clipDownloadOffered({}, 'linked'), false, 'a server from before 3.2.1 says nothing, which is no');
    for (const access of ['letIn', 'locked', 'unknown', 'starting', '']) {
        assert.equal(clips.clipDownloadOffered({canDownload: true}, access), false, `${access || 'nobody'}: never`);
    }
    assert.equal(clips.clipDownloadOffered(null, 'linked'), false, 'no clip yet');
    assert.equal(clips.clipDownloadOffered(undefined, 'linked'), false);
});

check('the panel opens only the plugin’s own download address, with a ticket', () => {
    const base = PLUGIN_BASE;
    const ticket = 'ab'.repeat(32);
    assert.equal(clips.isDownloadAddress(`${base}/api/v1/clips/download?t=${ticket}`, base), true);
    assert.equal(clips.isDownloadAddress(`https://elsewhere.example/api/v1/clips/download?t=${ticket}`, base), false);
    assert.equal(clips.isDownloadAddress(`//elsewhere.example${base}/api/v1/clips/download?t=${ticket}`, base), false);
    assert.equal(clips.isDownloadAddress(`${base}/api/v1/clips/download?t=${ticket}&next=https://x`, base), false);
    assert.equal(clips.isDownloadAddress(`${base}/api/v1/clips/video?id=0011223344556677`, base), false);
    assert.equal(clips.isDownloadAddress(`${base}/api/v1/clips/download?t=short`, base), false);
    assert.equal(clips.isDownloadAddress(undefined, base), false);
    assert.equal(clips.isDownloadAddress(42, base), false);
});

check('downloads: every refusal in plain words', () => {
    assert.deepEqual(clipCopy('downloads_off', 'download'), {text: 'Downloads are off.', actions: []});
    assert.deepEqual(clipCopy('streamer_downloads_off', 'download'), {text: 'The streamer has turned off downloads of their clips.', actions: []});
    assert.equal(clipCopy('clip_gone', 'download').text, 'This clip has gone — it expired or somebody deleted it.');
    assert.equal(clipCopy('rate_limited', 'download').text, 'Slow down a little.');
    assert.equal(clipCopy('watch_only', 'download').text, 'You can watch, but not do that.');
    assert.equal(clipCopy('network', 'download').text, 'The download could not be started just now.');
    assert.equal(clipCopy('store', 'downloads').text, 'Downloads of your stream’s clips could not be changed just now.');
    assert.equal(clipCopy('rate_limited', 'downloads').text, 'Slow down a little.');
});

check('the streamer’s download switch is there only while an admin allows downloads, in either position of clipping', () => {
    const now = 1_000_000_000_000;
    const base = {clipsEnabled: true, linked: true, allowed: true, lengthSeconds: 120, own: null, ownTail: null, attributed: null, now};
    const view = (more) => clips.streamerView({...base, ...more});

    assert.equal('downloads' in view({}), false, 'no downloads in "me": no switch');
    assert.equal(view({downloads: true}).downloads, true);
    assert.equal(view({downloads: false}).downloads, false);
    assert.equal(view({allowed: false, downloads: false}).downloads, false, 'with clipping off too');
    assert.equal(view({linked: false, downloads: true, attributed: {clip: 'ready'}}).kind, 'attributed', 'never for somebody not linked');
    assert.equal('downloads' in view({linked: false, downloads: true, attributed: {clip: 'ready'}}), false);
    assert.equal(clips.STREAMER_DOWNLOADS_SWITCH, 'Let people download clips of my stream');
    assert.equal(clips.streamerDownloadsTitle('Matty'), 'Whether people can download clips of your stream from Matty. It goes with the stream, like Allow clips of my stream.');
    assert.equal(clips.streamerDownloadsTitle('Movie Night Club'), 'Whether people can download clips of your stream from Movie Night Club. It goes with the stream, like Allow clips of my stream.');
});

check('with Clips off, a streamer keeps the download switch alone while an admin allows downloads', () => {
    // Clipping switched off, or Clips with nowhere to keep anything (a
    // directory that will not take writes): clips already made can still be
    // watched and downloaded, so the streamer's block has to stay in reach.
    const now = 1_000_000_000_000;
    const base = {clipsEnabled: false, linked: true, allowed: true, lengthSeconds: 120, own: {clip: 'ready'}, ownTail: null, attributed: null, now};
    const view = (more) => clips.streamerView({...base, ...more});

    assert.deepEqual(view({downloads: true}), {kind: 'downloads', downloads: true});
    assert.deepEqual(view({downloads: false}), {kind: 'downloads', downloads: false});
    assert.deepEqual(view({allowed: false, downloads: false}), {kind: 'downloads', downloads: false}, 'clipping’s own position is not said');
    assert.equal(view({}), null, 'no downloads either: nothing at all, as before');
    assert.equal(view({allowed: undefined}), null, '"me" carried no clipsStreamer');
    assert.equal(view({linked: undefined, downloads: true}), null, '"me" not known yet');
    assert.equal(view({linked: false, downloads: true}), null, 'never for somebody not linked');
    assert.equal(view({linked: false, downloads: true, attributed: {clip: 'ready'}}), null);
});

check('a pill taken from the clip viewer’s row hands focus to the one that took its place, then to Back', () => {
    // Share, Download, Report, Delete: Download goes with focus on it, and
    // Report, which took its place, has it.
    assert.equal(clips.actionAfterLoss(1, 3), 1);
    assert.equal(clips.actionAfterLoss(0, 3), 0, 'the first one gone: the new first');
    assert.equal(clips.actionAfterLoss(3, 3), 2, 'the last one gone: the new last');
    assert.equal(clips.actionAfterLoss(1, 1), 0, 'Share alone left');
    assert.equal(clips.actionAfterLoss(1, 0), -1, 'none left: Back');
    assert.equal(clips.actionAfterLoss(-1, 2), 0, 'somewhere unknown: the first');
});

check('the streamer’s own controls: what is said, and only what the server said', () => {
    const now = 1_000_000_000_000;
    const base = {clipsEnabled: true, linked: true, allowed: true, lengthSeconds: 120, own: null, ownTail: null, attributed: null, now};
    const view = (more) => clips.streamerView({...base, ...more});
    const held = {until: now + (8 * 60000) + 1000, heldMs: 120000};

    // Nothing at all while Clips are off, "me" is not known, or "me" carried
    // no clipsStreamer: never a switch in a guessed position.
    assert.equal(view({clipsEnabled: false}), null);
    assert.equal(view({linked: undefined}), null);
    assert.equal(view({allowed: undefined}), null);
    assert.equal(view({allowed: undefined, own: {clip: 'ready'}}), null);

    assert.deepEqual(view({}), {kind: 'streamer', allowed: true, wipe: false, wipeWords: 'Wipe the last 2:00',
        notice: 'When you’re live, the last 2:00 of your stream is kept on this server so viewers can clip it.'});
    const recording = view({own: {clip: 'ready'}});
    assert.deepEqual(recording, {kind: 'streamer', allowed: true, wipe: true, wipeWords: 'Wipe the last 2:00',
        notice: 'Clips are on: the last 2:00 of your stream is kept on this server so viewers can clip it.'});
    assert.equal(view({own: {clip: 'starting'}}).notice, recording.notice);
    assert.equal(view({own: {clip: 'starting'}}).wipe, false);
    assert.equal(view({own: {clip: 'starting'}, ownTail: held}).wipe, true, 'back after a drop, the end before it can be wiped');
    assert.equal(view({own: {clip: 'starting'}, ownTail: held}).notice, recording.notice);
    assert.equal(view({own: {clip: 'failed'}}).notice, 'Clips are on, but your stream can’t be kept for clipping just now.');
    assert.equal(view({own: {clip: 'failed'}}).wipe, false);
    assert.equal(view({own: {clip: 'ready'}, lengthSeconds: 300}).notice,
        'Clips are on: the last 5:00 of your stream is kept on this server so viewers can clip it.');

    // Gone off with its end held: said, so Wipe under it has something to
    // wipe, and Wipe says as much as is held. Let go, back to "When you’re
    // live".
    assert.deepEqual(view({ownTail: held}), {kind: 'streamer', allowed: true, wipe: true, wipeWords: 'Wipe the last 2:00',
        notice: 'Your stream has stopped: its last 2:00 is held here for 9 more minutes so viewers who were watching can clip it.'});
    const short = view({ownTail: {until: now + 30000, heldMs: 47000}});
    assert.equal(short.notice, 'Your stream has stopped: its last 0:47 is held here for 1 more minute so viewers who were watching can clip it.');
    assert.equal(short.wipeWords, 'Wipe the last 0:47');
    assert.equal(view({ownTail: {until: now + 60000}}).wipeWords, 'Wipe the last 2:00', 'how much, when not known: the whole length');
    assert.equal(view({ownTail: {until: now, heldMs: 120000}}).wipe, false, 'let go');
    assert.equal(view({ownTail: {until: now, heldMs: 120000}}).notice, view({}).notice);

    // Off while "me" says allowed: kept only in the moment after this panel
    // switched it on, before the server's next poll records it again. Any
    // other off is not called kept.
    assert.equal(view({own: {clip: 'off'}, justAllowed: true}).notice, recording.notice);
    assert.equal(view({own: {clip: 'off'}}).notice, 'Clips are on, but your stream can’t be kept for clipping just now.');
    assert.equal(view({own: {clip: 'off'}}).wipe, false);

    // Off: said, and nothing left to wipe.
    assert.deepEqual(view({allowed: false, own: {clip: 'off'}, ownTail: held}),
        {kind: 'streamer', allowed: false, wipe: false, wipeWords: 'Wipe the last 2:00', notice: 'Clipping of your stream is off.'});

    // Not linked: no switch, ever. Only a notice while a stream matched to
    // them by username is being kept.
    assert.equal(view({linked: false}), null);
    assert.equal(view({linked: false, own: {clip: 'ready'}}), null);
    assert.equal(view({linked: false, attributed: {}}), null, 'not being recorded');
    assert.equal(view({linked: false, attributed: {clip: 'off'}}), null, 'switched off by whoever proved the key');
    ['unsupported', 'queued', 'failed', 'full', 'mystery'].forEach((clip) => {
        assert.equal(view({linked: false, attributed: {clip}}), null, `${clip}: nothing is being kept`);
    });
    assert.equal(view({linked: false, attributed: {clip: 'ready'}}).kind, 'attributed');
    // Let in, with their stream matched by username: one line, no switch, no
    // wipe (design/lock-screen.md question 8).
    assert.deepEqual(view({linked: false, allowed: undefined, attributed: {clip: 'starting'}}), {kind: 'attributed',
        notice: 'Your stream is being kept for clipping. Link it to switch that off.'});
    assert.equal(view({linked: false, clipsEnabled: false, attributed: {clip: 'ready'}}), null);

    assert.equal(clips.wipeLabel(120), 'Wipe the last 2:00');
    assert.equal(clips.wipeLabel(90), 'Wipe the last 1:30');
    assert.equal(clips.ofMyStreamLine(0), '');
    assert.equal(clips.ofMyStreamLine(1), '1 clip of your stream');
    assert.equal(clips.ofMyStreamLine(3), '3 clips of your stream');
    assert.match(clips.streamerSwitchTitle(120), /unlinking leaves it as it is/);

    assert.equal(clipCopy('store', 'allow').text, 'Clipping of your stream could not be changed just now.');
    assert.equal(clipCopy('network', 'wipe').text, 'What is held of your stream could not be wiped just now.');
    assert.equal(clipCopy('rate_limited', 'wipe').text, 'Slow down a little.');
    assert.equal(clipCopy('not_allowed', 'wipe', {}, 'Only the person who made it, the streamer or an admin can do that.').text,
        'Only the person who made it, the streamer or an admin can do that.');
    assert.deepEqual(clipCopy('watch_only', 'allow').actions, []);
});

check('whether a browser can play a clip, and with its sound', () => {
    const everything = () => 'probably';
    const noOpus = (type) => (type.includes('opus') ? '' : 'maybe');
    const nothing = () => '';
    assert.equal(clips.playability('avc1.64000c,opus', everything), 'ok');
    assert.equal(clips.playability('avc1.64000c,opus', noOpus), 'no-sound');
    assert.equal(clips.playability('avc1.64000c', noOpus), 'ok');
    assert.equal(clips.playability('avc1.64000c,opus', nothing), 'no-video');

    const asked = [];
    clips.playability('avc1.640028, opus', (type) => {
        asked.push(type);

        return 'maybe';
    });
    assert.deepEqual(asked, ['video/mp4; codecs="avc1.640028"', 'audio/mp4; codecs="opus"']);
});

// ---- Clips: the header's clapper and the panel's row (spec 3.9, addendum 3.8) ----

check('clapper: nothing while the reader is not known, is locked, or Clips are not running', () => {
    const info = {enabled: true, count: 23};
    assert.equal(clips.clapperView({info, access: 'unknown', drafts: 2, unseen: true}), null);
    assert.equal(clips.clapperView({info, access: 'locked', drafts: 2, unseen: true}), null, 'the header is the wordmark alone');
    assert.equal(clips.clapperView({info: undefined, access: 'linked', drafts: 2, unseen: true}), null);
});

check('clapper: a linked reader’s, with drafts counted and new clips dotted', () => {
    assert.deepEqual(clips.clapperView({info: {enabled: true, count: 23}, access: 'linked', drafts: 0, unseen: false}),
        {label: 'Clips', drafts: 0, dot: false});
    assert.deepEqual(clips.clapperView({info: {enabled: true, count: 23}, access: 'linked', drafts: 1, unseen: false}),
        {label: 'Clips — you have 1 draft', drafts: 1, dot: false});
    assert.deepEqual(clips.clapperView({info: {enabled: true, count: 23}, access: 'linked', drafts: 2, unseen: true}),
        {label: 'Clips — you have 2 drafts — new clips', drafts: 2, dot: true});

    // Switched on with nothing yet: there, as the way to clipping; switched
    // off, only while there are clips or drafts left.
    assert.equal(clips.clapperView({info: {enabled: true, count: 0}, access: 'linked', drafts: 0, unseen: false}).label, 'Clips');
    assert.equal(clips.clapperView({info: {enabled: false, count: 0}, access: 'linked', drafts: 0, unseen: false}), null);
    assert.equal(clips.clapperView({info: {enabled: false, count: 3}, access: 'linked', drafts: 0, unseen: false}).label, 'Clips');
    assert.equal(clips.clapperView({info: {enabled: false, count: 0}, access: 'linked', drafts: 1, unseen: false}).drafts, 1);

    // No dot with no clips to be new.
    assert.equal(clips.clapperView({info: {enabled: true, count: 0}, access: 'linked', drafts: 0, unseen: true}).dot, false);
});

check('clapper: somebody let in has it with no count of drafts, and the dot for new clips', () => {
    assert.deepEqual(clips.clapperView({info: {enabled: true, count: 23}, access: 'letIn', drafts: 3, unseen: false}),
        {label: 'Clips', drafts: 0, dot: false});
    assert.deepEqual(clips.clapperView({info: {enabled: true, count: 23}, access: 'letIn', drafts: 3, unseen: true}),
        {label: 'Clips — new clips', drafts: 0, dot: true});
    assert.equal(clips.clapperView({info: {enabled: false, count: 0}, access: 'letIn', drafts: 3, unseen: true}), null);
});

check('Best Clipper in words', () => {
    assert.equal(clips.CLIPPER_LABEL, 'Best Clipper, all time');
    assert.equal(clips.clipperScore(142, 9), '142 upvotes · 9 clips');
    assert.equal(clips.clipperScore(1, 1), '1 upvote · 1 clip');
    assert.equal(clips.clipperAward(142, 9), 'All time · 142 upvotes on 9 clips');
});

// ---- Clips: the System Console's lines (spec 3.14) ----

check('console: sizes and bitrates', () => {
    const MB = 1024 * 1024;
    const GB = 1024 * MB;
    assert.equal(clips.bytesInWords(38 * MB), '38 MB');
    assert.equal(clips.bytesInWords(1.8 * GB), '1.8 GB');
    assert.equal(clips.bytesInWords(20 * GB), '20 GB');
    assert.equal(clips.bytesInWords(410 * GB), '410 GB');
    assert.equal(clips.bytesInWords(4 * GB), '4 GB');
    assert.equal(clips.bytesInWords(415867), '406 KB');
    assert.equal(clips.bytesInWords(0), '0 B');
    assert.equal(clips.bitrateInWords(6000000), '6 Mbit/s');
    assert.equal(clips.bitrateInWords(2500000), '2.5 Mbit/s');
    assert.equal(clips.bitrateInWords(800000), '800 kbit/s');
});

check('console: people, recordings and held ends', () => {
    assert.equal(clips.adminPerson({displayName: 'Bob', username: 'bob'}), 'Bob (@bob)');
    assert.equal(clips.adminPerson({username: 'bob'}), '@bob');
    assert.equal(clips.adminPerson({displayName: 'Bob'}), 'Bob');
    assert.equal(clips.adminPerson({userId: 'u1'}), 'u1');

    const recorder = {title: 'Dave', state: 'ready', heldSeconds: 120, bitrate: 6000000, truncated: false, reason: ''};
    assert.equal(clips.recorderLine(recorder, 120), 'Dave — 2:00 held · 6 Mbit/s');
    assert.equal(clips.recorderLine({...recorder, title: 'Erin', heldSeconds: 47, bitrate: 0}, 120), 'Erin — 0:47 held');
    assert.equal(clips.recorderLine({...recorder, heldSeconds: 84, truncated: true}, 120),
        'Dave — 1:24 held · 6 Mbit/s · too high a bitrate for the whole 2:00');
    assert.equal(clips.recorderLine({...recorder, title: 'Carol', state: 'failed', reason: 'ICE failed'}, 120), 'Carol — failing: ICE failed');
    assert.equal(clips.recorderLine({...recorder, title: 'Carol', state: 'failed'}, 120), 'Carol — failing');
    assert.equal(clips.recorderLine({...recorder, title: 'Judy', state: 'queued', reason: 'recorder limit'}, 120), 'Judy — busy with other streams');
    assert.equal(clips.recorderLine({...recorder, state: 'off'}, 120), 'Dave — switched off by its streamer');
    assert.equal(clips.recorderLine({...recorder, state: 'starting'}, 120), 'Dave — starting');

    const now = Date.UTC(2026, 9, 2, 12, 0, 0);
    assert.equal(clips.heldTailLine({title: 'Dave', reason: 'ended', endedAt: now - (3 * 60000)}, now, '21:14'),
        'Dave — went off 3 min ago, held until 21:14');
    assert.equal(clips.heldTailLine({title: 'Bob', reason: 'dropped', endedAt: now - 40000}, now, '12:09'),
        'Bob — dropped 40 s ago, held until 12:09');
});

check('console: a clip, a person, the summary and the retention warning', () => {
    const now = Date.UTC(2026, 9, 2, 12, 0, 0);
    const DAY = 86400000;
    const MB = 1024 * 1024;
    const GB = 1024 * MB;
    const clip = {
        clipper: {displayName: 'Bob', username: 'bob', name: 'Bob', userId: 'u-bob'},
        streamer: {name: 'Dave'},
        durationMs: 45000,
        createdAt: now - (3 * DAY),
        expiresAt: now + (27 * DAY),
        bytes: 38 * MB,
        upvotes: 14,
        reports: 2,
    };
    // The upvotes are drawn as a thumbs up, so the line comes in three parts.
    const said = (line) => `${line.before} · [${line.upvotes}]${line.after}`;
    assert.equal(said(clips.adminClipLine(clip, now)), 'Bob (@bob) clipped Dave · 0:45 · 3 days ago · goes in 27 days · 38 MB · [14] · 2 reports');
    assert.equal(said(clips.adminClipLine({...clip, reports: 0}, now)), 'Bob (@bob) clipped Dave · 0:45 · 3 days ago · goes in 27 days · 38 MB · [14]');
    assert.equal(said(clips.adminClipLine({...clip, reports: 0, tag: {category: 'game', title: 'Hytale'}}, now)),
        'Bob (@bob) clipped Dave · Hytale · 0:45 · 3 days ago · goes in 27 days · 38 MB · [14]');
    assert.equal(said(clips.adminClipLine({...clip, reports: 0, tag: {category: 'game', title: ' '}}, now)),
        'Bob (@bob) clipped Dave · 0:45 · 3 days ago · goes in 27 days · 38 MB · [14]');
    assert.equal(clips.adminClipLine({...clip, upvotes: -2}, now).upvotes, 0);

    const person = {displayName: 'Bob', username: 'bob', userId: 'u-bob', clips: 4, bytes: 180 * MB, upvotes: 31, banked: 12};
    assert.equal(said(clips.adminPersonLine(person, 4 * GB)), 'Bob (@bob) — 4 clips · 180 MB of 4 GB · [31] (12 banked)');
    assert.equal(said(clips.adminPersonLine({...person, banked: 0, clips: 1}, 0)), 'Bob (@bob) — 1 clip · 180 MB · [31]');

    const summary = {
        count: 23,
        usedBytes: 1.8 * GB,
        limitBytes: 20 * GB,
        shareBytes: 4 * GB,
        freeBytes: 410 * GB,
        totalBytes: 1000 * GB,
        ringBytes: 410 * MB,
        oldestGoesAt: now + (2 * DAY),
        best: {name: 'Bob', upvotes: 142},
    };
    assert.equal(clips.adminSummary(summary, now),
        '23 clips · 1.8 GB of 20 GB (4 GB each at most) · 410 GB free · recordings 410 MB · the oldest goes in 2 days · Best Clipper: Bob (142)');
    assert.equal(clips.adminSummary({...summary, count: 0, usedBytes: 0, totalBytes: 0, ringBytes: 0, oldestGoesAt: 0, best: null}, now),
        '0 clips · 0 B of 20 GB (4 GB each at most)');

    assert.equal(clips.retentionLine(3, 41, 'at 21:04 tomorrow'),
        'Keep clips for was lowered to 3 days: 41 clips will be removed at 21:04 tomorrow. Set it back before then to keep them.');
    assert.equal(clips.adminDeleteConfirm('“Clutch”', 14), 'Click again to delete “Clutch”: its 14 upvotes stop counting.');
    assert.equal(clips.adminDeleteConfirm('“Clutch”', 1), 'Click again to delete “Clutch”: its 1 upvote stops counting.');
    assert.equal(clips.adminDeleteConfirm('“Clutch”', 0), 'Click again to delete “Clutch”.');
});

// ---- Clips: the trimmer's words, and the editor (spec 3.6, 3.7) ----

check('trimmer: what each thumb says, and the line under the track', () => {
    const trim = {inMs: 37500, outMs: 83000, durationMs: 120000};
    assert.equal(clips.trimValueText('start', trim, 0), '0:38 of 2:00');
    assert.equal(clips.trimValueText('position', trim, 52900), '0:52');
    assert.equal(clips.trimValueText('end', trim, 0), '1:23 — the clip is 46 seconds');
    assert.equal(clips.trimValueText('end', {inMs: 38000, outMs: 83000, durationMs: 120000}, 0), '1:23 — the clip is 45 seconds');
    assert.equal(clips.trimValueText('end', {inMs: 0, outMs: 119990, durationMs: 119990}, 0), '2:00 — the clip is 2 minutes');
    assert.equal(clips.trimReadout({inMs: 38000, outMs: 83000, durationMs: 120000}), '0:45 of 2:00');
    assert.equal(clips.trimReadout({inMs: 0, outMs: 118043, durationMs: 118043}), '1:58 of 1:58');
});

check('editor: the clock on the picture counts within the selection', () => {
    const trim = {inMs: 20000, outMs: 65000, durationMs: 120000};
    assert.equal(clips.selectionTime(trim, 32500), '0:12 / 0:45');
    assert.equal(clips.selectionTime(trim, 5000), '0:00 / 0:45');
    assert.equal(clips.selectionTime(trim, 90000), '0:45 / 0:45');
});

check('editor: play starts in the selection, and stays in it', () => {
    const trim = {inMs: 20000, outMs: 65000, durationMs: 120000};
    assert.equal(clips.playFrom(30000, trim), 30000);
    assert.equal(clips.playFrom(20000, trim), 20000);
    assert.equal(clips.playFrom(5000, trim), 20000, 'before the start plays from the start');
    assert.equal(clips.playFrom(65000, trim), 20000, 'stopped at the end goes round again');
    assert.equal(clips.playFrom(90000, trim), 20000);

    assert.deepEqual(clips.playbackStep(30000, trim, true), {action: 'continue'});
    assert.deepEqual(clips.playbackStep(65000, trim, true), {action: 'jump', toMs: 20000});
    assert.deepEqual(clips.playbackStep(65083, trim, false), {action: 'stop', atMs: 65000});
    assert.deepEqual(clips.playbackStep(12000, trim, false), {action: 'jump', toMs: 20000}, 'the start moved past the playhead');
});

check('editor: the picture is taken half a second in, fitted whole into 480 × 270', () => {
    assert.equal(clips.thumbnailAt({inMs: 20000, outMs: 65000, durationMs: 120000}), 20.5);
    assert.equal(clips.thumbnailAt({inMs: 1000, outMs: 1600, durationMs: 120000}), 1.3, 'halfway through a selection shorter than a second');
    assert.equal(clips.thumbnailAt({inMs: 0, outMs: 120000, durationMs: 120000}), 0.5);

    assert.deepEqual(clips.containFit(1280, 720, 480, 270), {x: 0, y: 0, width: 480, height: 270});
    assert.deepEqual(clips.containFit(256, 144, 480, 270), {x: 0, y: 0, width: 480, height: 270});
    assert.deepEqual(clips.containFit(1080, 1920, 480, 270), {x: 164, y: 0, width: 152, height: 270}, 'a portrait stream, pillarboxed');
    assert.deepEqual(clips.containFit(2560, 1080, 480, 270), {x: 0, y: 34, width: 480, height: 203}, 'an ultrawide one, letterboxed');
    assert.deepEqual(clips.containFit(0, 0, 480, 270), {x: 0, y: 0, width: 480, height: 270}, 'no size known: the whole box');
});

check('editor: what it says while it cuts, when it is ready and while it publishes', () => {
    assert.equal(clips.cuttingNote(120, 'Dave'), 'the last 2:00 of Dave');
    assert.equal(clips.cuttingStatus(120, 'Dave'), 'Cutting the last 2:00 of Dave…');
    assert.equal(clips.cuttingStatus(300, 'Dave'), 'Cutting the last 5:00 of Dave…');
    assert.equal(clips.readyStatus(118043), 'Draft ready — 1:58 recorded.', 'the draft, not the trimmed selection');
    assert.equal(clips.publishingStatus(45000, false), 'Publishing — cutting 0:45 and making its picture.');
    assert.equal(clips.publishingStatus(45000, true),
        'Publishing — cutting 0:45 and making its picture. Still working — longer clips take a moment.');
    assert.equal(clips.PUBLISH_SLOW_MS, 10000);
});

check('editor: a not_watching refusal on a playing tile tunes in again once, and nothing else does', () => {
    // The server forgot the viewer (a restart, or Clips switched on after the
    // tile started): one fresh exchange, then the cut is asked for again.
    assert.equal(clips.renewBeforeRefusing('live', 'not_watching', 'playing', false), true);
    assert.equal(clips.renewBeforeRefusing('live', 'not_watching', 'playing', true), false, 'only once per cut');
    assert.equal(clips.renewBeforeRefusing('tail', 'not_watching', 'playing', false), false, 'an ended stream cannot be tuned in to');
    assert.equal(clips.renewBeforeRefusing('live', 'not_watching', 'connecting', false), false, 'not while the tile is still tuning');
    assert.equal(clips.renewBeforeRefusing('live', 'not_watching', 'failed', false), false);
    assert.equal(clips.renewBeforeRefusing('live', 'not_watching', '', false), false, 'no tile for it in this window');
    for (const code of ['streamer_off', 'not_linked', 'warming_up', 'not_live', 'network', 'rate_limited']) {
        assert.equal(clips.renewBeforeRefusing('live', code, 'playing', false), false, code);
    }
});

check('editor: published, without "for everybody" (addendum 3.10)', () => {
    assert.equal(clips.publishedSentence({title: 'that jump', streamer: dave}, '31 Oct'), '“that jump” is in Clips until 31 Oct.');
    assert.equal(clips.publishedSentence({title: '', streamer: dave}, '31 Oct'), 'Dave’s clip is in Clips until 31 Oct.');
    assert.equal(clips.publishedSentence({title: '', streamer: {name: 'Stream 3f9a0c'}}, '1 Nov'), 'This clip is in Clips until 1 Nov.');
});

check('editor: a draft says when it is less than a press asks for', () => {
    assert.deepEqual(clips.draftNotes({partial: false, truncated: false, durationMs: 120000}), []);
    assert.deepEqual(clips.draftNotes({partial: true, truncated: false, durationMs: 47000}),
        ['Only 0:47 had been recorded — the recording starts when the stream does.']);
    assert.deepEqual(clips.draftNotes({partial: false, truncated: true, durationMs: 84000}),
        ['Only 1:24 fitted — this stream’s bitrate is higher than clips are sized for.']);
});

check('editor: where the draft is from, with the stream’s tag (addendum 4.14)', () => {
    const line = (more) => clips.editorContext({streamer: dave, tag: '', note: '', from: '21:02:17', to: '21:04:17', ...more});
    assert.equal(line({tag: 'Hytale'}), 'From Dave’s stream · Hytale · 21:02:17 – 21:04:17');
    assert.equal(line({}), 'From Dave’s stream · 21:02:17 – 21:04:17', 'no tag: nothing left empty');
    assert.equal(line({tag: 'Hytale', note: 'Arma 3 ops night'}), 'From Dave’s stream · Hytale · “Arma 3 ops night” · 21:02:17 – 21:04:17');
    assert.equal(line({note: 'Arma 3 ops night'}), 'From Dave’s stream · “Arma 3 ops night” · 21:02:17 – 21:04:17');
    assert.equal(line({tail: 'ended'}), 'From Dave’s stream · 21:02:17 – 21:04:17 · before it went off');
    assert.equal(line({tail: 'dropped'}), 'From Dave’s stream · 21:02:17 – 21:04:17 · before it dropped');
    assert.equal(line({tail: 'unknown'}), 'From Dave’s stream · 21:02:17 – 21:04:17 · from the end of the stream',
        'a draft reopened once why its stream stopped is no longer known');
    assert.equal(clips.editorContext({streamer: {name: 'Stream 3f9a0c'}, tag: '', note: '', from: '1', to: '2'}), 'From Stream 3f9a0c · 1 – 2');
});

// ---- Clips: a clip's tag (addendum 4.14) ----

check('tags: the viewer’s line, the kind, and nothing for no tag', () => {
    const hytale = {category: 'game', title: 'Hytale', media: {ref: 'igdb:113360', kind: 'game', title: 'Hytale', year: '2026', source: 'IGDB'}};
    assert.equal(clips.tagLine(hytale), 'Hytale · Game · 2026');
    assert.equal(clips.tagLine({category: 'movie', title: 'Heat', media: {ref: 'tmdb:movie:949', kind: 'movie', title: 'Heat', year: '1995', source: 'TMDB'}}), 'Heat · Movie · 1995');
    assert.equal(clips.tagLine({category: 'movie', title: 'Breaking Bad', media: {ref: 'tmdb:tv:1396', kind: 'tv', title: 'Breaking Bad', source: 'TMDB'}}), 'Breaking Bad · TV series');
    assert.equal(clips.tagLine({category: 'game', title: 'Elden Ring'}), 'Elden Ring · Game');
    assert.equal(clips.tagLine({category: 'mystery', title: 'Something'}), 'Something');

    assert.equal(clips.tagTitleOf(undefined), '');
    assert.equal(clips.tagTitleOf(null), '');
    assert.equal(clips.tagTitleOf({title: '  Hytale '}), 'Hytale');
    assert.equal(clips.tagTitleOf({title: 7}), '');

    assert.equal(clips.tagKindOf(hytale), 'game');
    assert.equal(clips.tagKindOf({category: 'movie', title: 'Breaking Bad', media: {kind: 'tv'}}), 'tv');
    assert.equal(clips.tagKindOf({category: 'movie', title: 'Heat'}), 'movie');
    assert.equal(clips.tagKindOf({category: 'game', title: 'Elden Ring'}), 'game');
    assert.equal(clips.tagKindOf({category: 'other', title: 'x', media: {kind: 'book'}}), '');
});


// ---- The lock screen (design/lock-screen.md section 11) ----

check('lockLogic and motion import nothing, so this script can load them', () => {
    for (const file of ['lockLogic.ts', 'motion.ts']) {
        const source = readFileSync(new URL(`../webapp/src/${file}`, import.meta.url), 'utf8');
        assert.equal(/^\s*import\s/m.test(source), false, file);
    }
});

check('lock: access from "me" — none is unknown, no word or a stranger is locked, and the three words', () => {
    assert.equal(lock.accessOfMe(null, 'u1'), 'unknown');
    assert.equal(lock.accessOfMe(undefined, 'u1'), 'unknown');
    assert.equal(lock.accessOfMe({userId: 'u1'}, 'u1'), 'locked', 'a server that does not say is locked');
    assert.equal(lock.accessOfMe({userId: 'u1', access: 'superuser'}, 'u1'), 'locked', 'a word this build does not know');
    assert.equal(lock.accessOfMe({userId: 'u2', access: 'linked'}, 'u1'), 'locked', 'somebody else’s "me"');
    assert.equal(lock.accessOfMe({userId: 'u1', access: 'linked'}, ''), 'locked', 'nobody signed in');
    assert.equal(lock.accessOfMe({userId: 'u1', access: 'linked'}, 'u1'), 'linked');
    assert.equal(lock.accessOfMe({userId: 'u1', access: 'letIn'}, 'u1'), 'letIn');
    assert.equal(lock.accessOfMe({userId: 'u1', access: 'locked'}, 'u1'), 'locked');
    assert.equal(lock.accessOfMe({userId: 'u1', access: 'unknown'}, 'u1'), 'unknown');
});

check('lock: mayWatch and linked', () => {
    assert.deepEqual(['linked', 'letIn', 'locked', 'unknown'].map(lock.mayWatch), [true, true, false, false]);
    assert.deepEqual(['linked', 'letIn', 'locked', 'unknown'].map(lock.isLinked), [true, false, false, false]);
});

check('lock: what is shown — a definite answer wins, unknown keeps the last one, and with none it is unknown', () => {
    assert.equal(lock.shownAccessOf('locked', 'linked'), 'locked', 'a definite answer wins');
    assert.equal(lock.shownAccessOf('letIn', null), 'letIn');
    assert.equal(lock.shownAccessOf('unknown', 'linked'), 'linked', 'a blip never shuts out somebody it knew');
    assert.equal(lock.shownAccessOf('unknown', 'letIn'), 'letIn');
    assert.equal(lock.shownAccessOf('unknown', 'locked'), 'locked');
    assert.equal(lock.shownAccessOf('unknown', null), 'unknown', 'never LOCKED for somebody never told');
});

check('lock: what a change of access undoes, and why the lock came back', () => {
    assert.equal(lock.accessChangeOf('linked', 'locked'), 'lost');
    assert.equal(lock.accessChangeOf('letIn', 'locked'), 'lost');
    assert.equal(lock.accessChangeOf('linked', 'letIn'), 'downgraded');
    assert.equal(lock.accessChangeOf('locked', 'letIn'), 'gained');
    assert.equal(lock.accessChangeOf(null, 'linked'), 'gained');
    assert.equal(lock.accessChangeOf('letIn', 'linked'), 'none', 'linking from the footer is a remount, not an unlock');
    assert.equal(lock.accessChangeOf(null, 'locked'), 'none');
    assert.equal(lock.accessChangeOf('locked', 'locked'), 'none');

    assert.equal(lock.lossCauseOf('linked', true), 'self');
    assert.equal(lock.lossCauseOf('linked', false), 'unlinked');
    assert.equal(lock.lossCauseOf('letIn', false), 'letOut');
    assert.equal(lock.lossCauseOf(null, false), null);
});

check('lock: the empty check — nothing, spaces and "Bearer " alone are empty, and are never sent', () => {
    for (const empty of ['', '   ', 'Bearer ', 'bearer', '  BEARER   ', '\t\n']) {
        assert.equal(lock.isEmptyToken(empty), true, JSON.stringify(empty));
    }
    assert.equal(lock.isEmptyToken('Bearerabc'), false);
    assert.equal(lock.cleanToken('  Bearer 6f1c-token  '), '6f1c-token');
    assert.equal(lock.cleanToken('bearer\tabc'), 'abc');
    assert.equal(lock.cleanToken('Bearer Bearer abc'), 'Bearer abc', 'one prefix, as the server strips one');
    assert.equal(lock.cleanToken('abc'), 'abc');
});

check('lock: looksLikeToken — a UUID alone, with or without "Bearer " and spaces, and nothing else', () => {
    const uuid = '00000000-0000-4000-8000-000000000000';
    assert.equal(lock.looksLikeToken(uuid), true);
    assert.equal(lock.looksLikeToken(`  ${uuid.toUpperCase()}\n`), true);
    assert.equal(lock.looksLikeToken(`Bearer ${uuid}`), true);
    assert.equal(lock.looksLikeToken(`bearer   ${uuid} `), true);
    assert.equal(lock.looksLikeToken(`my token is ${uuid}`), false, 'inside a sentence');
    assert.equal(lock.looksLikeToken(`${uuid} lol`), false);
    assert.equal(lock.looksLikeToken('hello'), false);
    assert.equal(lock.looksLikeToken(''), false);
    assert.equal(lock.looksLikeToken(undefined), false);
});

check('lock: the bar’s states — empty sends nothing, checking ignores a second press, typing clears an error, success clears the token', () => {
    const typed = (value) => lock.tokenReducer(lock.TOKEN_START, {type: 'type', value});

    const empty = lock.tokenReducer(typed('Bearer '), {type: 'submit'});
    assert.equal(empty.phase, 'empty');
    assert.deepEqual(lock.tokenStatus(empty, 'Matty'), {text: 'Paste your bearer token first.', error: true});
    assert.deepEqual(lock.tokenStatus(empty, 'Matty', true), {text: 'Paste a bearer token to relink.', error: true});

    const checking = lock.tokenReducer(typed('abc123'), {type: 'submit'});
    assert.equal(checking.phase, 'checking');
    assert.equal(lock.tokenEditable(checking), false);
    assert.equal(lock.tokenReducer(checking, {type: 'submit'}), checking, 'a second press does nothing');
    assert.equal(lock.tokenReducer(checking, {type: 'type', value: 'x'}), checking, 'read-only while checking');
    assert.deepEqual(lock.tokenStatus(checking, 'Matty'), {text: 'Checking your token…', error: false});

    const refused = lock.tokenReducer(checking, {type: 'refused', code: 'not_accepted', text: 'That token was not accepted. Check it and try again.'});
    assert.equal(refused.phase, 'refused');
    assert.equal(refused.token, 'abc123', 'kept, so a typo can be fixed');
    assert.equal(lock.tokenStatus(refused, 'Matty').text, 'That token was not accepted. Check it and try again.');
    assert.equal(lock.tokenReducer(refused, {type: 'type', value: 'abc124'}).phase, 'idle', 'typing clears the error');

    const escaped = lock.tokenReducer(refused, {type: 'escape'});
    assert.equal(escaped.phase, 'idle');
    assert.equal(escaped.token, 'abc123', 'Escape never clears the bar');
    assert.equal(lock.tokenReducer(typed('abc'), {type: 'escape'}).token, 'abc');

    const accepted = lock.tokenReducer(checking, {type: 'accepted'});
    assert.deepEqual(accepted, {phase: 'unlocking', token: '', code: '', text: ''});
    assert.equal(lock.tokenEditable(accepted), false);
});

// A bearer token pasted into the message box is refused by the browser when
// it catches it (access.ts) and by the server when it does not
// (server/tokenguard.go): one sentence, whichever it was.
check('lock: the message box and the server refuse a pasted token in the same words', () => {
    const server = readFileSync(new URL('../server/tokenguard.go', import.meta.url), 'utf8').
        match(/func bearerTokenPostRefused\(name string\) string \{\n\s*return "([^"]*)" \+ name \+ "([^"]*)"/);
    assert.ok(server, 'server/tokenguard.go has no bearerTokenPostRefused(name)');

    const panel = readFileSync(new URL('../webapp/src/access.ts', import.meta.url), 'utf8').
        match(/export const tokenPostRefusal = \(name: string\): string => `([^`$]*)\$\{name\}([^`]*)`;/);
    assert.ok(panel, 'access.ts has no tokenPostRefusal(name)');

    assert.deepEqual([panel[1], panel[2]], [server[1], server[2]]);
});

// What the server says for each /me/link refusal, read from its own source
// (server/api.go): the lock shows what it is sent, and keeps no copy to show
// instead. By the code each is sent with.
const serverSource = readFileSync(new URL('../server/api.go', import.meta.url), 'utf8');
const serverSays = (name) => {
    const found = serverSource.match(new RegExp(`\\b${name}\\s*=\\s*"([^"]*)"`));
    assert.ok(found, `server/api.go has no ${name}`);

    return found[1];
};
// A sentence that names the plugin is a function of the server's name for it
// (brandName): `func linkNotSetUp(name string) string { return name + "..." }`.
const serverSaysNamed = (name, as) => {
    const found = serverSource.match(new RegExp(`\\bfunc ${name}\\(name string\\) string \\{\\n\\s*return name \\+ "([^"]*)"`));
    assert.ok(found, `server/api.go has no ${name}(name)`);

    return as + found[1];
};
const LINK_SAYS = {
    not_accepted: serverSays('linkNotAccepted'),
    rate_limited: serverSays('linkTooMany'),
    unreachable: serverSays('linkUnreachable'),
    bad_name: serverSays('linkBadName'),
    not_configured: serverSaysNamed('linkNotSetUp', 'Matty'),
    too_long: serverSays('linkTooLong'),
    bad_request: serverSays('linkUnreadable'),
    store: serverSays('linkNotSaved'),
};

check('lock: a /me/link refusal is worded by the server, in the sentences the lock has always shown', () => {
    // The sentences themselves, which were this panel's own table until the
    // server took them over word for word: changing one changes what the lock
    // says, here and in Matty.
    assert.deepEqual(LINK_SAYS, {
        not_accepted: 'That token was not accepted. Check it and try again.',
        rate_limited: 'Too many tries. Wait a few minutes, then try again.',
        unreachable: 'Broadcast Box is not answering, so your token can’t be checked. Try again later.',
        bad_name: 'Your token was accepted, but its stream name can’t be used here. Ask an admin to look at it.',
        not_configured: 'Matty is not set up yet, so it can’t be unlocked.',
        too_long: 'That is too long to be a bearer token.',
        bad_request: 'That did not get through. Try again.',
        store: 'Your token was accepted but could not be saved. Try again in a moment.',
    });
    // An empty bar is caught by the lock, in its own words, before anything
    // is sent; a token that is empty only once the server has cleaned it is
    // answered as one that did not get through.
    assert.equal(lock.TOKEN_EMPTY, 'Paste your bearer token first.');
    assert.match(serverSource, /if token == "" \{\n\s+writeCodedError\(w, http\.StatusBadRequest, linkUnreadable, codeBadRequest\)/);

    // The lock shows what it is sent, whatever the code, and has no words of
    // its own for any of them.
    for (const code of [...Object.keys(LINK_SAYS), 'brand_new']) {
        assert.equal(lock.lockCopy(code, ' The server said this. ', 'Matty'), 'The server said this.', code);
        assert.equal(lock.lockCopy(code, '', 'Matty'), 'Matty is not answering. Try again in a moment.', `${code}, with no sentence`);
    }
    assert.doesNotMatch(readFileSync(new URL('../webapp/src/lockLogic.ts', import.meta.url), 'utf8'), /const REFUSALS\b/, 'no table of its own');

    // Its own words are for no answer at all.
    assert.equal(lock.lockCopy('no_answer', 'ignored', 'Matty'), 'Matty is not answering. Try again in a moment.');
    assert.equal(lock.lockCopy('network', '', 'Matty'), 'Matty is not answering. Try again in a moment.');
    assert.equal(lock.lockCopy('', 'somebody else’s page', 'Matty'), 'Matty is not answering. Try again in a moment.');
    // With the server's name for the plugin, whatever it is.
    assert.equal(lock.lockCopy('network', '', 'Movie Night Club'), 'Movie Night Club is not answering. Try again in a moment.');

    // An empty-key 200 is worded as not accepted: the bar is refused with that
    // code and the same sentence, by the panel itself (LockScreen.tsx TokenBar).
    assert.equal(lock.TOKEN_NOT_ACCEPTED, LINK_SAYS.not_accepted);
    assert.equal(lock.tokenStatus({phase: 'refused', token: 'x', code: 'not_accepted', text: lock.TOKEN_NOT_ACCEPTED}, 'Matty').text, LINK_SAYS.not_accepted);
    assert.match(readFileSync(new URL('../webapp/src/components/LockScreen.tsx', import.meta.url), 'utf8'),
        /dispatch\(\{type: 'refused', code: 'not_accepted', text: TOKEN_NOT_ACCEPTED\}\)/);
});

check('lock: where to find a token is said on the lock only after an empty Unlock or a token refused as wrong', () => {
    const hint = 'OBS calls it Bearer Token, under Settings → Stream.';
    assert.equal(lock.TOKEN_HINT, hint);
    const at = (phase, code = '') => lock.lockStatus({phase, token: 'x', code, text: LINK_SAYS[code] || ''}, 'Matty');
    assert.deepEqual(at('empty'), {text: `Paste your bearer token first. ${hint}`, error: true});
    assert.deepEqual(at('refused', 'not_accepted'), {text: `That token was not accepted. Check it and try again. ${hint}`, error: true});
    assert.deepEqual(at('refused', 'too_long'), {text: `That is too long to be a bearer token. ${hint}`, error: true});
    for (const code of ['rate_limited', 'unreachable', 'bad_name', 'not_configured', 'bad_request', 'store', 'no_answer']) {
        assert.deepEqual(at('refused', code), lock.tokenStatus({phase: 'refused', token: 'x', code, text: LINK_SAYS[code] || ''}, 'Matty'), code);
    }
    assert.deepEqual(at('checking'), {text: 'Checking your token…', error: false});
    assert.deepEqual(at('idle'), {text: '', error: false});
    assert.deepEqual(at('unlocking'), {text: '', error: false});
    // The footer's and the relink sheet's words are not changed: they keep
    // the hint under their bars.
    assert.equal(lock.tokenStatus({phase: 'empty', token: '', code: '', text: ''}, 'Matty').text, 'Paste your bearer token first.');
});

check('lock: the line under the emblem, for each thing waiting and each reason', () => {
    const at = 1_000_000;
    const line = (more) => lock.lockLine({pending: null, cause: null, noSignal: false, now: at, pendingMs: motion.PENDING_MS, name: 'Matty', ...more});
    assert.equal(line({}), '', 'the padlock says it already');
    assert.equal(line({pending: {kind: 'stream', handle: 'h', name: 'Dave', at}}), 'Unlock Matty to watch Dave.');
    assert.equal(line({pending: {kind: 'stream', handle: 'h', name: ' ', at}}), 'Unlock Matty to watch this stream.');
    assert.equal(line({pending: {kind: 'clip', clipId: 'c', at}}), 'Unlock Matty to watch this clip.');
    assert.equal(line({cause: 'self'}), 'You unlinked your stream.');
    assert.equal(line({cause: 'unlinked'}), 'Your stream was unlinked here, by an admin or because another account linked it.');
    assert.equal(line({cause: 'letOut'}), 'You are no longer let in.');
    assert.equal(line({noSignal: true, cause: 'self', pending: {kind: 'clip', clipId: 'c', at}}),
        'Matty is not answering, so it can’t tell whether you’re unlocked.');
    assert.equal(line({pending: {kind: 'clip', clipId: 'c', at}, cause: 'letOut'}), 'Unlock Matty to watch this clip.', 'what was asked for first');
    // With the server's name for the plugin, whatever it is.
    assert.equal(line({pending: {kind: 'stream', handle: 'h', name: 'Dave', at}, name: 'Movie Night Club'}), 'Unlock Movie Night Club to watch Dave.');
    assert.equal(line({pending: {kind: 'clip', clipId: 'c', at}, name: 'Movie Night Club'}), 'Unlock Movie Night Club to watch this clip.');
    assert.equal(line({noSignal: true, name: 'Movie Night Club'}), 'Movie Night Club is not answering, so it can’t tell whether you’re unlocked.');
    assert.equal(lock.lockedWords('', 'Matty'), 'Matty is locked.');
    assert.equal(lock.lockedWords('You are no longer let in.', 'Movie Night Club'), 'Movie Night Club is locked. You are no longer let in.');
    // Lapsed after ten minutes: the reason, or nothing.
    const old = {kind: 'stream', handle: 'h', name: 'Dave', at: at - motion.PENDING_MS};
    assert.equal(line({pending: old}), '');
    assert.equal(line({pending: old, cause: 'letOut'}), 'You are no longer let in.');
});

check('lock: freshness — opened by hand counts for a second, and anything waiting for ten minutes', () => {
    assert.equal(motion.OPENED_BY_HAND_MS, 1000);
    assert.equal(motion.PENDING_MS, 600000);
    assert.equal(motion.LOCK_OPEN_MS, 440);
    assert.equal(motion.ACCESS_RECHECK_MS, 60000);
    const at = 5000;
    assert.equal(lock.fresh(at, at, motion.OPENED_BY_HAND_MS), true);
    assert.equal(lock.fresh(at, at + 999, motion.OPENED_BY_HAND_MS), true);
    assert.equal(lock.fresh(at, at + 1000, motion.OPENED_BY_HAND_MS), false);
    assert.equal(lock.fresh(-Infinity, at, motion.OPENED_BY_HAND_MS), false, 'never stamped');
    assert.equal(lock.fresh(at + 10, at, motion.OPENED_BY_HAND_MS), false, 'a stamp from the future');
    assert.equal(lock.fresh(at, at + motion.PENDING_MS - 1, motion.PENDING_MS), true);
    assert.equal(lock.fresh(at, at + motion.PENDING_MS, motion.PENDING_MS), false);
});

// What the lock may say (design/lock-screen.md, the amendment of 2026-10-06):
// the reason line, the status line over the bar, the not-set-up sentence and
// the No signal line. Never that people with no token can be let in, or whom
// to ask (Victor: "Don't advertise that"), and nothing only a system admin
// reads (Victor: "Remove admin lines too"): an admin's lock is everybody's.
const NOT_ON_THE_LOCK = /let (?:you|yourself|people|them) in|just watching|whom? to ask|ask (?:a|an|your) (?:system )?admin to let|system console|needs attention|without a token|watch only/i;

// A component's code without its comments, which may say what it once did.
function codeOf(file) {
    return readFileSync(new URL(`../webapp/src/${file}`, import.meta.url), 'utf8').
        replace(/\/\*[\s\S]*?\*\//g, '').
        replace(/^\s*\/\/.*$/gm, '');
}

check('lock: every word it can say is a reason, a status or a fact, never a way in and never for admins only', () => {
    assert.equal(lock.lockNotSetUp('Matty'), 'Matty is not set up yet, so it can’t be unlocked.');
    assert.equal(lock.lockNotSetUp('Movie Night Club'), 'Movie Night Club is not set up yet, so it can’t be unlocked.');
    assert.equal(LINK_SAYS.not_configured, lock.lockNotSetUp('Matty'), 'the face and the refusal say the same');

    const at = 1_000_000;
    const said = [lock.lockNotSetUp('Matty'), lock.lineNoSignal('Matty'), lock.TOKEN_HINT];
    for (const more of [{}, {cause: 'self'}, {cause: 'unlinked'}, {cause: 'letOut'}, {noSignal: true},
        {pending: {kind: 'stream', handle: 'h', name: 'Dave', at}}, {pending: {kind: 'clip', clipId: 'c', at}}]) {
        said.push(lock.lockLine({pending: null, cause: null, noSignal: false, now: at, pendingMs: motion.PENDING_MS, name: 'Matty', ...more}));
    }
    for (const phase of ['idle', 'empty', 'checking', 'unlocking']) {
        said.push(lock.lockStatus({phase, token: 'x', code: '', text: ''}, 'Matty').text);
    }
    for (const code of ['not_accepted', 'rate_limited', 'unreachable', 'bad_name', 'not_configured', 'too_long', 'bad_request', 'store', 'no_answer', '']) {
        // The server's sentence for each, which is what the lock shows.
        said.push(lock.lockStatus({phase: 'refused', token: 'x', code, text: LINK_SAYS[code] || ''}, 'Matty').text);
    }
    for (const words of said) {
        assert.doesNotMatch(words, NOT_ON_THE_LOCK, words);
    }

    // "You are no longer let in." says why the lock is up, which is allowed.
    assert.equal(lock.lockLine({pending: null, cause: 'letOut', noSignal: false, now: at, pendingMs: motion.PENDING_MS, name: 'Matty'}), 'You are no longer let in.');

    // The viewer's line, the admin's line and the alert line are gone, and
    // with them the "Who to ask" text that fed the first.
    for (const gone of ['viewerLineWords', 'viewerRowWords', 'LOCK_NEEDS_ATTENTION', 'cleanContact']) {
        assert.equal(gone in lock, false, gone);
    }
});

check('lock: no viewer line and no admin line on the screen, and nothing on it depends on being an admin', () => {
    const screen = codeOf('components/LockScreen.tsx');
    assert.doesNotMatch(screen, NOT_ON_THE_LOCK);
    for (const gone of ['isAdmin', 'AdminAlerts', 'hasAdminAlerts', 'accessContact', 'vntv-lock-alert', 'vntv-lock-quiet', 'vntv-lock-viewer', 'quiet=']) {
        assert.equal(screen.includes(gone), false, gone);
    }
    // Alone.
    assert.match(screen, /\{configured \? null : <p className='vntv-lock-line'>\{lockNotSetUp\(name\)\}<\/p>\}/, 'the not-set-up sentence, alone');

    // The rules that drew the removed lines went with them.
    const styles = readFileSync(new URL('../webapp/src/styles.ts', import.meta.url), 'utf8');
    for (const gone of ['vntv-lock-alert', 'vntv-lock-quiet']) {
        assert.equal(styles.includes(gone), false, gone);
    }

    // The full alerts stay, in the unlocked panel.
    assert.match(codeOf('components/Sidebar.tsx'), /<AdminAlerts me=\{me\}\/>/);
});

// ---- Push notifications: the System Console card and the admin alert ----
// (server/push_api.go pushAdminView, server/push.go pushAlertFor). The card's
// words are pushAdmin.ts's, run here; that the card and the alert are drawn
// from them is read from the components.

const PUSH_VIEW = (fields = {}) => ({on: true, ready: true, mattermost: true, people: 0, devices: 0, hosts: [], ...fields});
const PUSH_OFF_WORDS = 'Mattermost\'s own push notifications are switched off on this server, and Matty\'s go out only when Mattermost decides to push. ' +
    'An admin switches them on in System Console > Environment > Push Notification Server, where Enable Push Notifications must not be "Do not send push notifications".';

check('push: pushAdmin.ts imports nothing but types, so it is run here as written', () => {
    const source = readFileSync(new URL('../webapp/src/pushAdmin.ts', import.meta.url), 'utf8');
    for (const line of source.match(/^import .*$/gm) || []) {
        assert.match(line, /^import type /, line);
    }
});

check('push card: whether pushes can go out, from the plugin’s switch, its key and Mattermost’s own', () => {
    const said = (view) => push.pushSwitchLines(view).map((line) => `${line.tone}: ${line.text}`);
    assert.deepEqual(said(PUSH_VIEW()), [
        'plain: Push notifications are on: each Matty registered here gets a push whenever Mattermost decides to push to that person.',
        'quiet: Mattermost’s own push notifications are on.',
    ]);
    assert.deepEqual(said(PUSH_VIEW({on: false})), [
        'plain: Push notifications are off: no Matty can register for them here, and none is sent.',
        'quiet: Mattermost’s own push notifications are on.',
    ]);
    assert.deepEqual(said(PUSH_VIEW({ready: false})), [
        'error: Push notifications are on, but this server’s push key is not ready yet, so no Matty can register. Refresh in a moment.',
        'quiet: Mattermost’s own push notifications are on.',
    ]);

    // Mattermost pushing to nobody: in red while the switch is on, with the
    // server's sentence and its console path; quiet while it is off, as
    // what switching on would need.
    assert.deepEqual(said(PUSH_VIEW({mattermost: false, mattermostProblem: PUSH_OFF_WORDS})), [
        'error: Push notifications are on, but none go out, because Mattermost decides to push to nobody.',
        `error: ${PUSH_OFF_WORDS}`,
    ]);
    assert.deepEqual(said(PUSH_VIEW({on: false, mattermost: false, mattermostProblem: PUSH_OFF_WORDS})), [
        'plain: Push notifications are off: no Matty can register for them here, and none is sent.',
        `quiet: ${PUSH_OFF_WORDS}`,
    ]);
    assert.equal(said(PUSH_VIEW({mattermost: false, mattermostProblem: 'Could not read it just now'}))[1], 'error: Could not read it just now.', 'a sentence, whatever the server ended with');
    assert.equal(said(PUSH_VIEW({mattermost: false}))[1], 'error: Mattermost’s own push notifications are not on.', 'no reason sent');
});

check('push card: the head counts phones and people, and what is working, waiting, failing and refused', () => {
    assert.deepEqual(push.pushHead(PUSH_VIEW()), {bold: '', rest: 'No phone is registered for push notifications here.'});
    assert.deepEqual(push.pushHead(PUSH_VIEW({on: false})), {bold: '', rest: 'No phone is registered for push notifications here.'});
    const one = PUSH_VIEW({people: 1, devices: 1, hosts: [{host: 'ntfy.sh', devices: 1, working: 1, waiting: 0, failing: 0, refused: 0}]});
    assert.deepEqual(push.pushHead(one), {bold: '1 phone', rest: ' of 1 person: 1 working'});
    const mixed = PUSH_VIEW({people: 8, devices: 11, hosts: [
        {host: 'ntfy.sh', devices: 5, working: 4, waiting: 1, failing: 0, refused: 0},
        {host: 'ntfy.home.lan:8080', devices: 3, working: 1, waiting: 0, failing: 0, refused: 2},
        {host: 'push.example.net', devices: 3, working: 1, waiting: 0, failing: 2, refused: 0, failingSince: 5},
    ]});
    assert.deepEqual(push.pushHead(mixed), {bold: '11 phones', rest: ' of 8 people: 6 working, 1 waiting for their first push, 2 failing, 2 refused'});
    assert.equal(push.pushFullLine(mixed), '');
    assert.equal(push.pushFullLine({...mixed, full: true}), 'More than 5000 people have registered a phone: these counts are of the most recent 5000.');
});

check('push card: a push server’s row says its phones and each state it has any of, its worst word, and since when it fails', () => {
    const when = (at) => `[${at}]`;
    assert.deepEqual(push.pushHostParts({host: 'ntfy.sh', devices: 1, working: 1, waiting: 0, failing: 0, refused: 0}, when), ['1 phone', '1 working']);
    assert.deepEqual(push.pushHostParts({host: 'h', devices: 6, working: 1, waiting: 2, failing: 2, refused: 1, failingSince: 1234}, when),
        ['6 phones', '1 working', '2 waiting for the first push', '2 failing since [1234]', '1 refused']);
    assert.deepEqual(push.pushHostParts({host: 'h', devices: 1, working: 0, waiting: 0, failing: 1, refused: 0}, when), ['1 phone', '1 failing'], 'no time sent');
    assert.equal(push.pushHostFlag({host: 'h', devices: 2, working: 2, waiting: 0, failing: 0, refused: 0}), null);
    assert.equal(push.pushHostFlag({host: 'h', devices: 2, working: 1, waiting: 1, failing: 0, refused: 0}), null, 'waiting is not trouble');
    assert.deepEqual(push.pushHostFlag({host: 'h', devices: 2, working: 0, waiting: 0, failing: 1, refused: 0}), {word: 'Failing', state: 'failing'});
    assert.deepEqual(push.pushHostFlag({host: 'h', devices: 2, working: 0, waiting: 0, failing: 1, refused: 1}), {word: 'Refused', state: 'refused'}, 'the worst');

    // What the words mean, once, only when a row has them.
    const row = (fields) => ({host: 'h', devices: 1, working: 0, waiting: 0, failing: 0, refused: 0, ...fields});
    assert.deepEqual(push.pushTroubleLines(PUSH_VIEW({hosts: [row({working: 1}), row({waiting: 1})]})), []);
    const both = push.pushTroubleLines(PUSH_VIEW({hosts: [row({refused: 1}), row({failing: 1}), row({refused: 1})]}));
    assert.equal(both.length, 2);
    assert.match(both[0], /^Refused: .*tried with one of their pushes an hour at most, and work again by themselves once one goes through\. .*anonymous publishing allowed to topics that start with “up”\.$/);
    assert.match(both[1], /^Failing: .*ten failures in a row, or an hour of nothing else with at least three, a phone counts as failing \(until then, as working\), is tried with one of its pushes at most every ten minutes, and works again by itself once one goes through\.$/);
});

check('push card: the pushes not sent, most first, in the card’s words, and a word it does not know as the server sent it', () => {
    assert.equal(push.pushDroppedLine(PUSH_VIEW()), '');
    assert.equal(push.pushDroppedLine(PUSH_VIEW({dropped: {}})), '');
    assert.equal(push.pushDroppedLine(PUSH_VIEW({dropped: {'queue full': 0}})), '', 'a zero is nothing to say');
    assert.equal(push.pushDroppedLine(PUSH_VIEW({dropped: {'too many for one phone': 2, 'other plugin': 7, 'queue full': 2, store: 1, 'too many waiting to try again': 3, 'something new': 1, 'switched off': 4}})),
        'Not sent since the plugin last started on this server: 7 made by another plugin, such as Calls; 4 waiting when push notifications were switched off; 3 too many waiting to be tried again; 2 more than the queues hold; 2 too many at once for one phone; 1 something new; 1 the push records could not be read.');
});

check('push card: every word the server counts a push not sent under has the card’s words for it', () => {
    const source = readFileSync(new URL('../server/push_send.go', import.meta.url), 'utf8');
    const counted = new Set([...source.matchAll(/r\.count\("([^"]+)"\)|r\.dropped\["([^"]+)"\]/g)].map((m) => m[1] || m[2]));
    assert.ok(counted.size >= 6, `found the words push_send.go counts: ${[...counted].join(', ')}`);
    for (const why of counted) {
        assert.ok(push.PUSH_DROPPED_WORDS[why], `the card has no words for “${why}”`);
    }
});

check('push card: it is the PushStatus setting’s, drawn from these words, and never says anything of a person', () => {
    const manifest = JSON.parse(readFileSync(new URL('../plugin.json', import.meta.url), 'utf8'));
    const section = manifest.settings_schema.sections.find((candidate) => candidate.key === 'push');
    assert.ok(section, 'plugin.json has the push section');
    assert.equal(section.title, push.PUSH_SECTION, 'the alert names the section by its title');
    const status = section.settings.find((setting) => setting.key === 'PushStatus');
    assert.equal(status?.type, 'custom');
    assert.match(codeOf('adminSettings.ts'), /\{key: 'pushstatus', component: AdminPush, showTitle: true\}/, 'registered under the lowercased key');

    const card = codeOf('components/AdminPush.tsx');
    assert.match(card, /fetchAdminPush\(\)/);
    for (const drawn of ['pushHead(view)', 'pushSwitchLines(view)', 'pushFullLine(view)', 'pushDroppedLine(view)', 'pushTroubleLines(view)', 'pushHostFlag(row)', 'pushHostParts(row, ']) {
        assert.ok(card.includes(drawn), drawn);
    }
    assert.match(card, /<AdminStatus[\s\S]*?error=\{error\}/, 'a failed read says why, as the App check card does');
    assert.doesNotMatch(card, /onChange|userId|username|endpoint/, 'read-only, and nothing per person');
    assert.match(codeOf('client.ts'), /export const fetchAdminPush = \(\): Promise<AdminPush> => requestJSON<AdminPush>\('\/api\/v1\/admin\/push'\);/);
});

check('push alert: the server’s words as a sentence, for an admin, beside the other admin alerts, never on the lock', () => {
    assert.equal(push.pushAlertText(undefined), '');
    assert.equal(push.pushAlertText('  '), '');
    assert.equal(push.pushAlertText(PUSH_OFF_WORDS), `Push notifications need attention: ${PUSH_OFF_WORDS}`);
    assert.equal(push.pushAlertText('Could not read it'), 'Push notifications need attention: Could not read it.');

    const alerts = codeOf('components/AdminAlerts.tsx');
    assert.match(alerts, /function hasAdminAlerts\([^)]*\)[^{]*\{[^}]*pushAlertText\(me\.pushAlert\)[^}]*\}/, 'an alert of its own is enough to show the alerts');
    assert.match(alerts, /const push = pushAlertText\(me\.pushAlert\);/);
    assert.match(alerts, /\{push && \([\s\S]*?\{push\}[\s\S]*?Only you can see this\.[\s\S]*?consolePath\(PUSH_SECTION\)/);
    assert.equal(codeOf('components/LockScreen.tsx').includes('pushAlert'), false);
});

// Every file under a folder of the webapp's, but what is built or installed.
function filesIn(dir) {
    return readdirSync(dir, {withFileTypes: true}).flatMap((entry) => {
        if (entry.name === 'dist' || entry.name === 'node_modules') {
            return [];
        }
        const path = new URL(entry.name + (entry.isDirectory() ? '/' : ''), dir);

        return entry.isDirectory() ? filesIn(path) : [path];
    });
}

check('Try again hands focus on it to the lock or the panel as it goes, never to the page’s body', () => {
    // An answer takes it out of the page (the lock's No signal face changes,
    // the panel's quiet notice goes) with focus on it after a keyboard press,
    // and from the body the next letter went to Mattermost's message box. Its
    // cleanup has to be a layout effect's, which runs while the button is
    // still there to be asked whether it has focus.
    const tryAgain = codeOf('components/TryAgain.tsx');
    assert.match(tryAgain, /React\.useLayoutEffect\(\(\) => \{\n\s+const key = keyRef\.current;\n\n\s+return \(\) => \{\n\s+if \(key && document\.activeElement === key\) \{\n\s+key\.closest<HTMLElement>\('\.vntv-panel'\)\?\.focus\(\{preventScroll: true\}\);/);
    assert.match(tryAgain, /ref=\{keyRef\}/, 'on the button itself');

    // Both places it is used are inside a focusable root of that class.
    assert.match(codeOf('components/LockScreen.tsx'), /className='vntv vntv-panel vntv-lock'[\s\S]*?tabIndex=\{-1\}/);
    assert.match(codeOf('components/Sidebar.tsx'), /className='vntv vntv-panel'[\s\S]*?tabIndex=\{-1\}/);
});

check('"Who to ask" is gone from the webapp and the lab', () => {
    const files = [...filesIn(new URL('../webapp/src/', import.meta.url)), ...filesIn(new URL('../webapp/lab/', import.meta.url))].
        filter((path) => /\.(?:tsx?|html)$/.test(path.pathname));
    assert.ok(files.length > 50, 'found the sources');
    for (const path of files) {
        assert.doesNotMatch(readFileSync(path, 'utf8'), /accessContact|who to ask/i, path.pathname);
    }
});

// ---- A bearer token bar's keys (focusTrap.ts keepTokenKeys) ----

check('focusTrap imports nothing, so this script can load it', () => {
    const source = readFileSync(new URL('../webapp/src/focusTrap.ts', import.meta.url), 'utf8');
    assert.equal(/^\s*import\s/m.test(source), false);
});

// A key as the browser hands it over, pressed on a button beside the bar.
function keyOn(target, init) {
    const event = {key: '', keyCode: 0, ctrlKey: false, metaKey: false, altKey: false, target, stopped: false, ...init};
    event.stopPropagation = () => {
        event.stopped = true;
    };

    return event;
}

function fakeBar(more = {}) {
    const bar = {tagName: 'INPUT', isConnected: true, readOnly: false, disabled: false, focused: false, ...more};
    bar.focus = () => {
        bar.focused = true;
    };

    return bar;
}

const BUTTON = {tagName: 'BUTTON', isContentEditable: false, closest: () => ({})};

check('token keys: a paste is told by the letter, or by keyCode 86 where the layout types another letter, as Mattermost tells it', () => {
    assert.equal(keys.isPasteKey({key: 'v', keyCode: 86, ctrlKey: true, metaKey: false, altKey: false}), true, 'US Ctrl+V');
    assert.equal(keys.isPasteKey({key: 'V', keyCode: 86, ctrlKey: true, metaKey: false, altKey: false}), true, 'Ctrl+Shift+V');
    assert.equal(keys.isPasteKey({key: 'v', keyCode: 86, ctrlKey: false, metaKey: true, altKey: false}), true, 'Cmd+V');
    for (const letter of ['м', 'ω', 'ה', 'ر']) {
        assert.equal(keys.isPasteKey({key: letter, keyCode: 86, ctrlKey: true, metaKey: false, altKey: false}), true, `Ctrl+V typing ${letter}`);
    }
    assert.equal(keys.isPasteKey({key: 'v', keyCode: 86, ctrlKey: false, metaKey: false, altKey: false}), false, 'v alone');
    assert.equal(keys.isPasteKey({key: 'v', keyCode: 86, ctrlKey: true, metaKey: false, altKey: true}), false, 'AltGr+V');
    assert.equal(keys.isPasteKey({key: 'Process', keyCode: 229, ctrlKey: true, metaKey: false, altKey: false}), false, 'an input method');
    assert.equal(keys.isPasteKey({key: 'c', keyCode: 67, ctrlKey: true, metaKey: false, altKey: false}), false, 'Ctrl+C');
});

check('token keys: Ctrl+V on Unlock lands in the bar on a Russian keyboard as on a US one', () => {
    for (const init of [{key: 'v', keyCode: 86, ctrlKey: true}, {key: 'м', keyCode: 86, ctrlKey: true}, {key: 'ω', keyCode: 86, ctrlKey: true}]) {
        const bar = fakeBar();
        const event = keyOn(BUTTON, init);
        keys.keepTokenKeys(event, bar);
        assert.equal(bar.focused, true, `${init.key}: the bar takes focus during the keydown`);
        assert.equal(event.stopped, true, `${init.key}: and the key goes no further`);
    }
});

check('token keys: a letter goes into the bar; while the bar is busy the key is only stopped; the bar’s own keys and Space on a button are left alone', () => {
    let bar = fakeBar();
    let event = keyOn(BUTTON, {key: 'a', keyCode: 65});
    keys.keepTokenKeys(event, bar);
    assert.deepEqual([bar.focused, event.stopped], [true, true], 'a letter on Show');

    bar = fakeBar({readOnly: true});
    event = keyOn(BUTTON, {key: 'м', keyCode: 86, ctrlKey: true});
    keys.keepTokenKeys(event, bar);
    assert.deepEqual([bar.focused, event.stopped], [false, true], 'checking: stopped, focus not moved');

    bar = fakeBar();
    event = keyOn(bar, {key: 'м', keyCode: 86, ctrlKey: true});
    keys.keepTokenKeys(event, bar);
    assert.deepEqual([bar.focused, event.stopped], [false, false], 'in the bar itself');

    bar = fakeBar();
    event = keyOn(BUTTON, {key: ' ', keyCode: 32});
    keys.keepTokenKeys(event, bar);
    assert.deepEqual([bar.focused, event.stopped], [false, false], 'Space presses the button');

    for (const key of ['Tab', 'Enter', 'Escape', 'F6']) {
        bar = fakeBar();
        event = keyOn(BUTTON, {key});
        keys.keepTokenKeys(event, bar);
        assert.deepEqual([bar.focused, event.stopped], [false, false], key);
    }
});

check('every rule that stops a stray paste tells one as Mattermost does (isPasteKey), never by the letter alone', () => {
    // By the letter, a Ctrl+V on a Russian, Greek, Hebrew or Arabic keyboard
    // was not a paste here, and Mattermost, which also goes by keyCode 86,
    // took it to its message box: from the panel, a tile, the Twitch sheet,
    // and the words after Disconnect Twitch in the watch-only footer.
    const sources = filesIn(new URL('../webapp/src/', import.meta.url)).filter((path) => /\.tsx?$/.test(path.pathname));
    for (const path of sources) {
        const file = path.pathname.slice(path.pathname.indexOf('/webapp/src/') + '/webapp/src/'.length);
        if (file === 'focusTrap.ts') {
            continue;
        }
        assert.doesNotMatch(codeOf(file), /key(?:\.toLowerCase\(\))? === '[vV]'|keyCode === 86/, file);
    }
    for (const file of ['components/Sidebar.tsx', 'components/Player.tsx', 'components/TwitchPlayer.tsx', 'components/TwitchConnect.tsx']) {
        assert.match(codeOf(file), /const paste = isPasteKey\(event\);/, file);
    }
});

// ---- A two-press button's second press (twoClicksLogic.ts) ----

// A press at a time after the arming one: from the mouse (detail 1), the
// keyboard (detail 0) or a double click's second half (detail 2), with Enter
// or Space down or not, and how many key releases the page has seen.
const press = (at, detail, keyDown, releases) => ({at, detail, keyDown, releases});

check('two presses: a click arms, and a separate click after half a second acts; sooner, or a double click, does not', () => {
    const arming = twice.armingOf(press(1000, 1, false, 0));
    assert.equal(twice.confirms(arming, press(1000 + twice.CONFIRM_MIN_MS - 1, 1, false, 0)), false, 'too quick to have read it');
    assert.equal(twice.confirms(arming, press(1000 + twice.CONFIRM_MIN_MS, 2, false, 0)), false, 'a double click');
    assert.equal(twice.confirms(arming, press(1000 + twice.CONFIRM_MIN_MS, 1, false, 0)), true, 'a click of its own');
});

check('two presses: Enter held down never confirms on its own repeats, however long it is held', () => {
    // Enter's keydown presses the focused button (or the box's handler):
    // armed with the key down and no release yet.
    const arming = twice.armingOf(press(1000, 0, true, 3));
    for (const after of [530, 560, 1000, 3900]) {
        assert.equal(twice.confirms(arming, press(1000 + after, 0, true, 3)), false, `a repeat ${after} ms on`);
    }
});

check('two presses: Enter let go and pressed again confirms; so does a click while it is still held', () => {
    const arming = twice.armingOf(press(1000, 0, true, 3));
    assert.equal(twice.confirms(arming, press(1650, 0, true, 4)), true, 'released, then pressed again');
    assert.equal(twice.confirms(arming, press(1100, 0, true, 4)), false, 'released, but pressed again too soon');
    assert.equal(twice.confirms(arming, press(1650, 1, true, 3)), true, 'a mouse click while Enter is held is a press of its own');
});

check('two presses: armed by Space (it presses on its release) or by the mouse, the next Enter acts', () => {
    // Space presses a button when it comes up, so by the press it is not down.
    const bySpace = twice.armingOf(press(1000, 0, false, 5));
    assert.equal(twice.confirms(bySpace, press(1600, 0, true, 5)), true, 'Enter after Space');
    const byMouse = twice.armingOf(press(1000, 1, false, 5));
    assert.equal(twice.confirms(byMouse, press(1600, 0, true, 5)), true, 'Enter after a click');
});

// ---- Dates and times: each formatter made once (time.ts) ----

// How many Intl.DateTimeFormat run makes. time.ts looks Intl up as it is
// called, so it makes them through this counting stand-in.
function formattersMade(run) {
    const Real = Intl.DateTimeFormat;
    let made = 0;
    const Counted = function DateTimeFormat(...args) {
        made++;

        return new Real(...args);
    };
    Counted.prototype = Real.prototype;
    Counted.supportedLocalesOf = Real.supportedLocalesOf;
    Intl.DateTimeFormat = Counted;
    try {
        run();
    } finally {
        Intl.DateTimeFormat = Real;
    }

    return made;
}

const MADRID = 'Europe/Madrid';
const SIX_PM = Date.UTC(2026, 9, 6, 16, 0);

// What an Up next card and an untitled clip ask for on every render.
function aCardsWords(i) {
    const at = SIX_PM + (i % 48) * 3600000;
    time.formatWhen(at, MADRID, SIX_PM);
    time.leadWhen(at, MADRID, SIX_PM);
    time.clockTime(at, MADRID);
    time.shortStamp(at - 86400000, MADRID);
    time.postedWhen(at - 9 * 86400000, MADRID, SIX_PM);
    time.postedAt(at, MADRID);
}

check('time: five hundred cards’ words make a formatter for each kind of date once, and five hundred more make none', () => {
    const first = formattersMade(() => {
        for (let i = 0; i < 500; i++) {
            aCardsWords(i);
        }
    });
    assert.ok(first <= 8, `${first} made for the first five hundred: one for each set of options at most`);
    const again = formattersMade(() => {
        for (let i = 0; i < 500; i++) {
            aCardsWords(i);
        }
    });
    assert.equal(again, 0, 'all of them kept');
});

check('time: a kept formatter is only ever used in its own zone, however the zones take turns', () => {
    const at = Date.UTC(2026, 9, 6, 13, 7);
    const zones = [MADRID, 'Australia/Adelaide', 'Asia/Kathmandu', 'America/New_York', 'UTC'];
    for (let round = 0; round < 3; round++) {
        for (const zone of zones) {
            const fresh = new Intl.DateTimeFormat(undefined, {hour: '2-digit', minute: '2-digit', hour12: false, timeZone: zone});
            assert.equal(time.clockTime(at, zone), fresh.format(at), zone);
        }
    }

    // 14:00 UTC is still today in London, and half past midnight tomorrow in
    // Adelaide, at +10:30.
    const now = Date.UTC(2026, 9, 6, 13, 0);
    const later = Date.UTC(2026, 9, 6, 14, 0);
    for (let round = 0; round < 3; round++) {
        assert.equal(time.dayWord(later, 'UTC', now), 'Today');
        assert.equal(time.dayWord(later, 'Australia/Adelaide', now), 'Tomorrow');
    }
});

check('time: a zone Intl does not know is read in the machine’s zone, every time it is asked for', () => {
    const at = Date.UTC(2026, 9, 6, 13, 7);
    const machine = new Intl.DateTimeFormat(undefined, {hour: '2-digit', minute: '2-digit', hour12: false}).format(at);
    for (let round = 0; round < 3; round++) {
        assert.equal(time.clockTime(at, 'Not/AZone'), machine);
        assert.equal(time.dayWord(at, 'Not/AZone', at), 'Today');
    }
});

// Once the code running now has finished, as between two renders.
const afterThisRender = () => new Promise((resolve) => setTimeout(resolve, 0));

await checkAsync('time: the machine changing its zone is seen at the next render, by viewerZone and by a time read in no zone', async () => {
    const at = Date.UTC(2026, 9, 6, 13, 7);
    const options = {hour: '2-digit', minute: '2-digit', hour12: false};
    const saved = process.env.TZ;
    try {
        await afterThisRender();
        process.env.TZ = 'Asia/Kathmandu';
        assert.match(time.viewerZone(), /^Asia\/Kath?mandu$/);
        const there = time.clockTime(at, undefined);
        assert.equal(there, new Intl.DateTimeFormat(undefined, {...options, timeZone: 'Asia/Kathmandu'}).format(at));

        await afterThisRender();
        process.env.TZ = 'America/New_York';
        assert.equal(time.viewerZone(), 'America/New_York');
        const here = time.clockTime(at, undefined);
        assert.equal(here, new Intl.DateTimeFormat(undefined, {...options, timeZone: 'America/New_York'}).format(at));
        assert.notEqual(here, there);
    } finally {
        if (saved === undefined) {
            delete process.env.TZ;
        } else {
            process.env.TZ = saved;
        }
        await afterThisRender();
    }
});

await checkAsync('time: a render of a hundred untitled clips reads the machine’s zone once, and the next render reads it again', async () => {
    await afterThisRender();
    const zone = time.viewerZone();
    time.shortStamp(SIX_PM, zone);
    await afterThisRender();
    const once = formattersMade(() => {
        for (let i = 0; i < 100; i++) {
            assert.equal(time.viewerZone(), zone);
            time.shortStamp(SIX_PM - i * 60000, zone);
        }
    });
    assert.equal(once, 1, 'the one read of the machine’s zone');
    await afterThisRender();
    assert.equal(formattersMade(() => time.viewerZone()), 1, 'read again');
});

// ---- Signing out: what a hashtag search was told (tagSearch.ts) ----

// Mattermost, as tagSearch.ts reaches it: who is signed in, a store that has
// loaded nobody (it is emptied on signing out), and a server that sends first
// and last names to a system admin and blanks them for everybody else, as
// it does while Show Full Name is off. Names are shown as full names, which
// is what the Teammate Name Display setting asks for here.
const tagWorld = {
    me: 'u-admin',
    admin: true,
    userAsks: 0,
    channelAsks: 0,
    held: null,
};
globalThis.villanetTagWorld = tagWorld;

const tagSearch = await loadWith('tagSearch.ts', {
    './client': `
        const world = globalThis.villanetTagWorld;
        export const searchPosts = async () => ({
            order: ['p1'],
            posts: {p1: {id: 'p1', user_id: 'u-dave', channel_id: 'c-games', message: '#arma3 tonight', create_at: 1}},
        });
        export const fetchChannel = async (id) => {
            world.channelAsks++;
            return {id, type: 'O', name: 'games', display_name: 'Games', team_id: 't1'};
        };
        export const fetchUsersByIds = async (ids) => {
            // As client.ts does, nobody to ask about is no request at all.
            if (ids.length === 0) {
                return [];
            }
            world.userAsks++;
            const answer = ids.map((id) => ({
                id,
                username: 'dave',
                first_name: world.admin ? 'Dave' : '',
                last_name: world.admin ? 'Secret-Surname' : '',
            }));
            if (!world.held) {
                return answer;
            }
            const held = world.held;
            world.held = null;
            held.asked();
            await held.until;
            return answer;
        };
    `,
    './linkify': 'export const isHashtagTerm = () => true;',
    './mattermost': `
        const world = globalThis.villanetTagWorld;
        export const channelOf = () => undefined;
        export const profileOf = () => undefined;
        export const collapsedThreads = () => false;
        export const currentTeam = () => ({id: 't1', name: 'team-one'});
        export const currentUserId = () => world.me;
        export const teamNameOf = () => 'team-one';
        export const displayNameOf = (user) => (user ? [user.first_name, user.last_name].filter(Boolean).join(' ') || user.username : 'Someone');
        export const openSearchHere = () => false;
        export const permalinkHref = (path) => path;
        export const popoutMessaging = () => undefined;
        export const webhookNamesShown = () => false;
    `,
    './popout': 'export const isPopoutWindow = () => false;',
});

function signInAs(me, admin) {
    tagWorld.me = me;
    tagWorld.admin = admin;
}

// What index.tsx does when the store says nobody is signed in any more.
function signOut() {
    tagSearch.forgetTagSearch();
    signInAs('', false);
}

await checkAsync('hashtag search: the same reader’s next search asks Mattermost for nobody it was already told about', async () => {
    signInAs('u-admin', true);
    const first = await tagSearch.searchTag('arma3', 0);
    assert.equal(first.hits[0].author, 'Dave Secret-Surname');
    const asks = [tagWorld.userAsks, tagWorld.channelAsks];
    const next = await tagSearch.searchTag('arma3', 1);
    assert.equal(next.hits[0].where, 'Games');
    assert.deepEqual([tagWorld.userAsks, tagWorld.channelAsks], asks, 'answered from what it was told');
    signOut();
});

await checkAsync('hashtag search: after an admin signs out, a member on the same page is shown only what Mattermost tells them', async () => {
    signInAs('u-admin', true);
    assert.equal((await tagSearch.searchTag('arma3', 0)).hits[0].author, 'Dave Secret-Surname');
    signOut();

    signInAs('u-member', false);
    const asks = [tagWorld.userAsks, tagWorld.channelAsks];
    const page = await tagSearch.searchTag('arma3', 0);
    assert.equal(page.hits[0].author, 'dave', 'never the admin’s full name');
    assert.deepEqual([tagWorld.userAsks, tagWorld.channelAsks], [asks[0] + 1, asks[1] + 1], 'Mattermost asked again, as the member');
    signOut();
});

await checkAsync('hashtag search: a search still on its way when its reader signs out leaves nothing of theirs for the next person', async () => {
    signInAs('u-admin', true);
    let release;
    const asked = new Promise((resolve) => {
        tagWorld.held = {asked: resolve, until: new Promise((done) => {
            release = done;
        })};
    });
    const theirs = tagSearch.searchTag('arma3', 0);
    await asked;

    // The answer is the admin's, and comes back after they have gone.
    signOut();
    release();
    await theirs;

    signInAs('u-member', false);
    const asks = tagWorld.userAsks;
    const page = await tagSearch.searchTag('arma3', 0);
    assert.equal(page.hits[0].author, 'dave');
    assert.equal(tagWorld.userAsks, asks + 1, 'asked again, as the member');
    signOut();
});

check('index.tsx forgets what a hashtag search was told when its reader signs out, and when the plugin stops', () => {
    const index = readFileSync(new URL('../webapp/src/index.tsx', import.meta.url), 'utf8');
    const signedOut = index.match(/followSignIn\(store, \(\) => \{[\s\S]*?\}, \(\) => \{([\s\S]*?)\}\);/);
    assert.ok(signedOut, 'the onSignedOut callback given to followSignIn');
    assert.match(signedOut[1], /^\s*forgetTagSearch\(\);$/m);
    const uninitialize = index.slice(index.indexOf('public uninitialize('));
    assert.match(uninitialize, /^\s*forgetTagSearch\(\);$/m);
});

// ---- 3.2.0: what is told in the reader's own name (design/3.2.0.md 2.6) ----

const own = await load('ownAnswers.ts');
const soundboard = await load('soundboard.ts');

await checkAsync('own answers: one that arrives after its reader signed out is answered to nobody', async () => {
    let release;
    const theirs = own.askOwn(() => new Promise((resolve) => {
        release = resolve;
    }));
    own.forgetOwnAnswers();
    release({show: ['alltime'], readable: true});
    assert.equal(await theirs, null);
    assert.deepEqual(await own.askOwn(async () => ({show: null})), {show: null}, 'the next reader’s own answer is theirs');
});

check('the soundboard: somebody’s sound heard from the event, in either shape, and forgotten at sign-out', () => {
    soundboard.heardSound({busyUntil: 5000}, 1000);
    assert.deepEqual(soundboard.getSoundBusy(), {until: 5000, heardAt: 1000});
    soundboard.heardSound({payload: JSON.stringify({busyUntil: 7000})}, 2000);
    assert.deepEqual(soundboard.getSoundBusy(), {until: 7000, heardAt: 2000});
    soundboard.heardSound({busyUntil: 'soon'}, 3000);
    assert.equal(soundboard.getSoundBusy().until, 7000, 'nonsense is ignored');
    let told = 0;
    const stop = soundboard.onSoundBusy(() => told++);
    soundboard.forgetSoundboard();
    stop();
    assert.deepEqual(soundboard.getSoundBusy(), {until: 0, heardAt: 0});
    assert.equal(told, 1);
});

check('index.tsx forgets 3.2.0’s own answers, the soundboard and a plan link when a reader signs out, and when the plugin stops', () => {
    const index = readFileSync(new URL('../webapp/src/index.tsx', import.meta.url), 'utf8');
    const signedOut = index.match(/followSignIn\(store, \(\) => \{[\s\S]*?\}, \(\) => \{([\s\S]*?)\}\);/);
    assert.ok(signedOut, 'the onSignedOut callback given to followSignIn');
    const uninitialize = index.slice(index.indexOf('public uninitialize('));
    for (const forget of ['forgetOwnAnswers', 'forgetSoundboard', 'forgetPlanLink', 'resetClips']) {
        assert.match(signedOut[1], new RegExp(`^\\s*${forget}\\(\\);$`, 'm'), `${forget} at sign-out`);
        assert.match(uninitialize, new RegExp(`^\\s*${forget}\\(\\);$`, 'm'), `${forget} at uninitialize`);
    }
});

check('what is asked in the reader’s own name goes through askOwn, so a late answer cannot land for the next reader', () => {
    const read = (file) => readFileSync(new URL(`../webapp/src/components/${file}`, import.meta.url), 'utf8');
    assert.match(readFileSync(new URL('../webapp/src/awardsMine.ts', import.meta.url), 'utf8'), /askOwn\(fetchMyAwards\)/, 'the You tab\'s awards, kept for the reader they were asked for (awardsMine.ts)');
    assert.match(read('TrophiesYou.tsx'), /askAwardsMine\(userId, boards\.version\)/);
    assert.doesNotMatch(read('TrophiesYou.tsx'), /fetchMyAwards/, 'and asked nowhere else');
    assert.match(read('TrophiesYou.tsx'), /askOwn\(fetchMyAchievements\)/);
    assert.match(read('PopoverTakebacks.tsx'), /askOwn\(fetchTakebacks\)/);
    assert.match(read('Soundboard.tsx'), /askOwn\(fetchSounds\)/);
});

check('@names keep no cache of their own: titleParts.ts and linkify.tsx hold no module-level Map or Set', () => {
    for (const file of ['titleParts.ts', 'linkify.tsx']) {
        const source = readFileSync(new URL(`../webapp/src/${file}`, import.meta.url), 'utf8');
        assert.doesNotMatch(source, /^(?:export\s+)?(?:const|let|var)\s+\w+[^=\n]*=\s*new\s+(?:Weak)?(?:Map|Set)\b/m, file);
        assert.doesNotMatch(source, /^(?:export\s+)?(?:const|let|var)\s+\w+\s*:\s*(?:Weak)?(?:Map|Set)</m, file);
    }
});

await checkAsync('a plan link waiting to be focused is forgotten at sign-out', async () => {
    const link = await loadWith('planLink.ts', {
        './mattermost': 'export const currentChannelId = () => \'\';',
        './watching': 'export const markOpenedByHand = () => {}; export const openPanel = () => {};',
    });
    link.arriveAtPlan('0123456789abcdef', null);
    link.forgetPlanLink();
    assert.equal(link.takePlanFocus(['0123456789abcdef']), null);
    link.arriveAtPlan('0123456789abcdef', null);
    assert.equal(link.takePlanFocus(['ffffffffffffffff']), null, 'not on the rail: kept');
    assert.equal(link.takePlanFocus(['0123456789abcdef']), '0123456789abcdef');
    assert.equal(link.takePlanFocus(['0123456789abcdef']), null, 'a link is followed once');
});

// ---- 3.2.0: the thumb on a shared clip's card (design/3.2.0.md 9.4, 9.7) ----

check('the cards on screen are asked again only after a vote, at most once every 30 s, after up to 3 s of their own, none while hidden', () => {
    const delay = (fields) => clips.cardRefreshDelay({shown: 3, visible: true, lastAt: 0, now: 100000, jitter: 0, ...fields});
    assert.equal(delay({shown: 0}), null, 'no card on screen asks');
    assert.equal(delay({visible: false}), null, 'a hidden page waits to be shown');
    assert.equal(delay({}), 0);
    assert.equal(delay({jitter: 1}), 3000, 'spread over three seconds');
    assert.equal(delay({jitter: 0.5}), 1500);
    assert.equal(delay({lastAt: 90000}), 20000, 'thirty seconds after the last time, not sooner');
    assert.equal(delay({lastAt: 90000, jitter: 1}), 23000);
});

check('a locked reader’s card has no count and asks for nothing', () => {
    const locked = clips.cardView({
        post: {type: 'custom_villanet_clip', message: 'x', props: {villanet_clip_id: '0123456789abcdef', villanet_clip_title: 't', villanet_clip_duration_ms: 1000, villanet_clip_created_at: 1}},
        mayWatch: false,
        now: 2,
        fetch: {status: 'ok', card: {title: 't', createdAt: 1, durationMs: 1000, clipper: {name: 'a'}, streamer: {name: 'b'}, upvotes: 4, voted: true, canVote: true}},
        name: 'Matty',
    });
    assert.equal(locked.votes, undefined);
    const card = readFileSync(new URL('../webapp/src/components/ClipPostCard.tsx', import.meta.url), 'utf8');
    assert.match(card, /const asks = watch === true && before\.state === 'loading';/);
    assert.match(card, /useSharedClip\(asks \? before\.id : '', watch\)/);
});

check('a thumbs up shown at once moves the count by one, never below nothing', () => {
    assert.deepEqual(clips.votedNow({upvotes: 4, voted: false}, true), {upvotes: 5, voted: true});
    assert.deepEqual(clips.votedNow({upvotes: 0, voted: true}, false), {upvotes: 0, voted: false});
    assert.deepEqual(clips.votedNow({upvotes: 4, voted: true}, true), {upvotes: 4, voted: true});
});

// clips.ts itself, with every module it asks for played by a stand-in and the
// cards route and the vote route answered from here.
const voteWorld = {voteFails: false, voteCode: 'store', votes: 0, downloadURL: '', saved: []};
globalThis.villanetVoteWorld = voteWorld;
globalThis.window = globalThis.window || globalThis;
const CLIP = '0123456789abcdef';
const clipStore = await (async () => {
    const swapped = ts.transpileModule(readFileSync(new URL('../webapp/src/clips.ts', import.meta.url), 'utf8'), {
        compilerOptions: {module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2020},
        fileName: 'clips.ts',
    }).outputText;
    const transpiled = (file) => ts.transpileModule(readFileSync(new URL(`../webapp/src/${file}`, import.meta.url), 'utf8'), {
        compilerOptions: {module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2020},
        fileName: file,
    }).outputText;
    const standIns = {
        react: 'export default {};',
        './away': 'export const windowKind = () => \'main\';',
        './clipLogic': transpiled('clipLogic.ts'),
        './client': `
            const world = globalThis.villanetVoteWorld;
            const card = {title: 'that jump', createdAt: 1, durationMs: 45000, clipper: {userId: 'u-a', name: 'Bob'}, streamer: {userId: 'u-m', name: 'Dave'}, upvotes: 30, voted: false, canVote: true};
            export class ClipRequestError extends Error {
                constructor(status, code) { super(code); this.status = status; this.code = code; this.serverText = ''; }
            }
            export const fetchClipCards = async (ids) => ({cards: Object.fromEntries(ids.map((id) => [id, {...card, id}]))});
            export const fetchClip = async (id) => ({...card, id, upvotes: 30, voted: false, reported: false, mine: false});
            export const voteClip = async (id, up) => {
                world.votes++;
                if (world.voteFails) {
                    throw new ClipRequestError(world.voteCode === 'store' ? 500 : 409, world.voteCode);
                }
                return {upvotes: up ? 31 : 30, voted: up};
            };
            export const fetchClipDownload = async () => ({url: world.downloadURL});
            export const saveClipFrom = (url) => { world.saved.push(url); };
            const unused = async () => { throw new Error('not in this check'); };
            export const checkDraftVideo = unused, createDraft = unused, deleteClip = unused, deleteDraft = unused,
                fetchClips = unused, fetchDrafts = unused, publishDraft = unused, reportClip = unused,
                setClipDownloads = unused, setClipsAllowed = unused, shareClip = unused, uploadDraftThumb = unused, wipeRecording = unused;
        `,
        './identity': 'export const accessNow = () => \'linked\'; export const getIdentity = () => ({userId: \'u-v\'}); export const refreshIdentity = async () => null;',
        './lockLogic': transpiled('lockLogic.ts'),
        './lockPending': 'export const getPending = () => null; export const onPendingChange = () => () => {}; export const openLock = () => {};',
        './manifest': transpiled('manifest.ts'),
        './mattermost': 'export const currentChannelId = () => \'c1\'; export const currentUserId = () => \'u-v\';',
        './sessions': 'export const getSessionInfo = () => undefined; export const renewSession = async () => {};',
        './store': 'export const getSnapshot = () => ({streams: [], fetchedAt: 1, configured: true}); export const subscribe = () => () => {};',
        './time': transpiled('time.ts'),
        './watching': 'export const markOpenedByHand = () => {}; export const openPanel = () => {};',
    };
    const code = swapped.replace(/from '([^']*)'/g, (whole, path) => {
        if (path === './types') {
            return whole;
        }
        assert.ok(path in standIns, `clips.ts imports ${path}, and there is no stand-in for it`);

        return `from 'data:text/javascript;base64,${Buffer.from(standIns[path]).toString('base64')}'`;
    }).replace(/^import type .*$/gm, '');

    return import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
})();

const settle = () => new Promise((resolve) => setTimeout(resolve, 80));
const cardVotes = () => {
    const fetch = clipStore.sharedCardOf(CLIP);

    return fetch.status === 'ok' ? [fetch.card.upvotes, fetch.card.voted] : fetch.status;
};

await checkAsync('voteOnCard: the card moves at once, then the server’s answer stands', async () => {
    clipStore.refreshSharedClip(CLIP);
    await settle();
    assert.deepEqual(cardVotes(), [30, false]);
    const voting = clipStore.voteOnCard(CLIP, true);
    assert.deepEqual(cardVotes(), [31, true], 'at once');
    await voting;
    assert.deepEqual(cardVotes(), [31, true]);
});

await checkAsync('voteOnCard: a vote that fails puts the card back', async () => {
    voteWorld.voteFails = true;
    const voting = clipStore.voteOnCard(CLIP, false);
    assert.deepEqual(cardVotes(), [30, false]);
    await assert.rejects(voting);
    assert.deepEqual(cardVotes(), [31, true], 'rolled back');
    voteWorld.voteFails = false;
});

await checkAsync('voteOnCard: with the Clips dialog holding the clip too, both copies move together, and both go back together', async () => {
    await clipStore.loadClip(CLIP);
    assert.equal(clipStore.getClip(CLIP).upvotes, 30);
    voteWorld.voteFails = true;
    const voting = clipStore.voteOnCard(CLIP, true);
    assert.equal(clipStore.getClip(CLIP).upvotes, 31, 'the dialog’s copy at once');
    assert.equal(cardVotes()[0], 31, 'the card’s copy at once');
    await assert.rejects(voting);
    assert.equal(clipStore.getClip(CLIP).upvotes, 30, 'the dialog’s copy back');
    assert.equal(cardVotes()[0], 30, 'the card’s copy back');
    voteWorld.voteFails = false;
    await clipStore.voteOn(CLIP, true);
    assert.deepEqual([clipStore.getClip(CLIP).upvotes, cardVotes()[0]], [31, 31], 'a vote in the dialog moves the card the same way');
});

check('a card’s refused vote keeps the clip vote’s own words; anything else is the card’s own', () => {
    assert.equal(clips.cardVoteWords('store', '', 'Matty'), clips.CARD_VOTE_FAILED);
    assert.equal(clips.cardVoteWords('network', '', 'Matty'), clips.CARD_VOTE_FAILED);
    assert.equal(clips.CARD_VOTE_FAILED, 'Couldn’t count that. Try again.');
    assert.equal(clips.cardVoteWords('rate_limited', '', 'Matty'), 'Slow down a little.');
    assert.equal(clips.cardVoteWords('own_clip', '', 'Matty'), 'You can’t upvote your own clip.');
    assert.equal(clips.cardVoteWords('clip_gone', '', 'Matty'), clips.CARD_GONE);
    assert.equal(clips.cardVoteWords('watch_only', '', 'Matty'), clips.CLIP_WATCH_ONLY);
    assert.equal(clips.cardVoteWords('locked', '', 'Matty'), 'Matty is locked.');
    assert.equal(clips.cardVoteWords('locked', '', 'Movie Night Club'), 'Movie Night Club is locked.');
    assert.equal(clips.cardVoteWords('forbidden', '', 'Matty'), clips.CARD_VOTE_FAILED, 'no words of the server’s own: the card’s');
    assert.equal(clips.cardVoteWords('forbidden', 'Not that one.', 'Matty'), 'Not that one.');
    const card = readFileSync(new URL('../webapp/src/components/ClipPostCard.tsx', import.meta.url), 'utf8');
    assert.match(card, /onFailed\(cardVoteWords\(clipErrorCodeOf\(reason\), clipErrorTextOf\(reason\), nameNow\(\)\)\)/, 'the card says them');
});

await checkAsync('voteOnCard: a vote refused because the clip has gone turns the card to its gone face', async () => {
    clipStore.resetClips();
    clipStore.refreshSharedClip(CLIP);
    await settle();
    voteWorld.voteFails = true;
    voteWorld.voteCode = 'clip_gone';
    await assert.rejects(clipStore.voteOnCard(CLIP, true));
    assert.equal(clipStore.sharedCardOf(CLIP).status, 'gone');
    voteWorld.voteFails = false;
    voteWorld.voteCode = 'store';
});

await checkAsync('signing out drops every card, with what this reader voted and may vote', async () => {
    clipStore.resetClips();
    assert.equal(clipStore.sharedCardOf(CLIP).status, 'idle');
});

await checkAsync('downloadClip saves from the plugin’s own download address, and from nowhere else', async () => {
    const good = `${PLUGIN_BASE}/api/v1/clips/download?t=${'cd'.repeat(32)}`;
    voteWorld.downloadURL = good;
    await clipStore.downloadClip(CLIP);
    assert.deepEqual(voteWorld.saved, [good]);

    voteWorld.downloadURL = `https://elsewhere.example/steal?t=${'cd'.repeat(32)}`;
    await assert.rejects(clipStore.downloadClip(CLIP), (reason) => reason.code === 'store');
    assert.deepEqual(voteWorld.saved, [good], 'nothing opened');
});

// ---- 3.4.0: everybody's Steam status and Hytale names (design/3.4.0.md 5.7) ----

// accounts.ts, with "me", the clock and GET /api/v1/accounts played from
// here. Each request is held until the check answers it, so the order the
// answers come in is the check's to choose.
const accountsWorld = {access: 'locked', now: 1000000, asked: 0, held: [], tags: [], identity: new Set(), timers: [], loaded: {}, namesAsked: [], namesHeld: []};
globalThis.villanetAccountsWorld = accountsWorld;
const accountsStore = await (async () => {
    const transpiled = (file) => ts.transpileModule(readFileSync(new URL(`../webapp/src/${file}`, import.meta.url), 'utf8'), {
        compilerOptions: {module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2020},
        fileName: file,
    }).outputText;
    const standIns = {
        './accountsLogic': transpiled('accountsLogic.ts'),
        './client': `
            const world = globalThis.villanetAccountsWorld;
            export class RequestError extends Error {
                constructor(message, status, code) { super(message); this.status = status; this.code = code; }
            }
            world.RequestError = RequestError;
            // The read that says which answer is in hand. The check answers it
            // with a view, which comes back under a tag of its own, or with
            // null: "the one you hold still" (a 304).
            export const fetchAccountsIfChanged = (tag) => {
                world.asked++;
                world.tags.push(tag);
                const asked = world.asked;
                return new Promise((resolve, reject) => world.held.push({resolve, reject})).
                    then((view) => (view === null ? null : {value: view, tag: '"tag-' + asked + '"'}));
            };
            export const fetchUsersByIds = (ids) => {
                world.namesAsked.push(ids);
                return new Promise((resolve, reject) => world.namesHeld.push({resolve, reject}));
            };
        `,
        './mattermost': `
            const world = globalThis.villanetAccountsWorld;
            export const profileOf = (userId) => world.loaded[userId];
            export const displayNameOf = (user) => (user ? user.first_name || user.username : 'Someone');
        `,
        './clock': 'export const now = () => globalThis.villanetAccountsWorld.now;',
        './identity': `
            const world = globalThis.villanetAccountsWorld;
            export const accessNow = () => world.access;
            export const onIdentityChange = (listener) => {
                world.identity.add(listener);
                return () => world.identity.delete(listener);
            };
        `,
    };
    const swapped = transpiled('accounts.ts').replace(/from '([^']*)'/g, (_, path) => {
        assert.ok(path in standIns, `accounts.ts imports ${path}, and there is no stand-in for it`);

        return `from 'data:text/javascript;base64,${Buffer.from(standIns[path]).toString('base64')}'`;
    });

    return import(`data:text/javascript;base64,${Buffer.from(swapped).toString('base64')}`);
})();

const answerOf = (version, people = {}) => ({v: version, steam: {on: true, fresh: true}, hytale: {on: true}, people});
const nextHeld = () => accountsWorld.held.shift();
const settleAccounts = () => new Promise((resolve) => setImmediate(resolve));
const meChanged = (access) => {
    accountsWorld.access = access;
    Array.from(accountsWorld.identity).forEach((listener) => listener());
};

await checkAsync('accounts: a re-read says which answer is in hand, and "that one still" changes nothing', async () => {
    accountsStore.clearAccounts();
    accountsWorld.access = 'linked';
    let told = 0;
    const unsubscribe = accountsStore.onAccounts(() => told++);

    // Nothing in hand: asked for whole.
    const first = accountsStore.loadAccounts();
    assert.equal(accountsWorld.tags.at(-1), '', 'no tag with nothing to keep');
    nextHeld().resolve(answerOf('v1', {'u-dave': {steam: {name: 'Dave', url: '', state: 'online'}}}));
    await first;
    const held = accountsStore.getAccounts();
    assert.equal(held.v, 'v1');
    const firstAsk = accountsWorld.asked;

    // In hand: its tag goes back, and a 304 keeps the very same view.
    const toldBefore = told;
    const second = accountsStore.loadAccounts();
    assert.equal(accountsWorld.tags.at(-1), `"tag-${firstAsk}"`, 'the tag the answer in hand came under');
    nextHeld().resolve(null);
    await second;
    assert.equal(accountsStore.getAccounts(), held, 'the same view, not a copy of it');
    assert.equal(told, toldBefore, 'and nobody is told of a change there was not');

    // A change comes whole, under a new tag, which the next read sends.
    const third = accountsStore.loadAccounts();
    nextHeld().resolve(answerOf('v2'));
    await third;
    assert.equal(accountsStore.getAccounts().v, 'v2');
    const fourth = accountsStore.loadAccounts();
    assert.equal(accountsWorld.tags.at(-1), `"tag-${accountsWorld.asked - 1}"`);
    nextHeld().resolve(null);
    await fourth;

    // Emptied (locked, signed out): nothing to keep, so no tag.
    accountsStore.clearAccounts();
    const fifth = accountsStore.loadAccounts();
    assert.equal(accountsWorld.tags.at(-1), '');
    nextHeld().resolve(answerOf('v3'));
    await fifth;
    unsubscribe();
    accountsStore.clearAccounts();
});

await checkAsync('accounts: a reader who may not watch asks nothing, and a request for one empties the store', async () => {
    accountsStore.clearAccounts();
    accountsWorld.access = 'locked';
    const asked = accountsWorld.asked;
    await accountsStore.loadAccounts();
    accountsStore.loadAccountsIfOld();
    accountsStore.accountsEventHeard();
    assert.equal(accountsWorld.asked, asked, 'no GET /api/v1/accounts at all');
    assert.equal(accountsStore.getAccounts(), null);
});

await checkAsync('accounts: the newest request’s answer wins, whichever comes back first', async () => {
    accountsStore.clearAccounts();
    accountsWorld.access = 'letIn';
    const first = accountsStore.loadAccounts();
    const second = accountsStore.loadAccounts();
    const [older, newer] = [nextHeld(), nextHeld()];
    newer.resolve(answerOf('new'));
    await second;
    older.resolve(answerOf('old'));
    await first;
    assert.equal(accountsStore.getAccounts().v, 'new');
});

await checkAsync('accounts: an answer still on its way when the reader signs out is dropped, and nothing of theirs is left', async () => {
    accountsStore.clearAccounts();
    accountsWorld.access = 'linked';
    await (async () => {
        const read = accountsStore.loadAccounts();
        nextHeld().resolve(answerOf('theirs', {'u-dave': {hytale: {name: 'DaveBuilds'}}}));
        await read;
    })();
    assert.equal(accountsStore.getAccounts().v, 'theirs');

    const late = accountsStore.loadAccounts();
    const held = nextHeld();

    // What index.tsx does when the store says nobody is signed in any more.
    accountsStore.clearAccounts();
    assert.equal(accountsStore.getAccounts(), null, 'emptied at once');
    held.resolve(answerOf('late', {'u-dave': {hytale: {name: 'DaveBuilds'}}}));
    await late;
    assert.equal(accountsStore.getAccounts(), null, 'and not refilled by the answer that came after');
});

await checkAsync('accounts: the gate’s 403 empties the store; any other failure keeps what was known', async () => {
    accountsStore.clearAccounts();
    accountsWorld.access = 'letIn';
    let read = accountsStore.loadAccounts();
    nextHeld().resolve(answerOf('known'));
    await read;

    read = accountsStore.loadAccounts();
    nextHeld().reject(new accountsWorld.RequestError('Matty could not answer just now.', 503, 'store'));
    await read;
    assert.equal(accountsStore.getAccounts().v, 'known', 'a server that did not answer is no news');

    read = accountsStore.loadAccounts();
    nextHeld().reject(new TypeError('Failed to fetch'));
    await read;
    assert.equal(accountsStore.getAccounts().v, 'known', 'nor is a network that did not');

    read = accountsStore.loadAccounts();
    nextHeld().reject(new accountsWorld.RequestError('Matty is locked. Unlock it with your bearer token.', 403, 'locked'));
    await read;
    assert.equal(accountsStore.getAccounts(), null, 'refused: emptied');
});

await checkAsync('accounts: the panel or a popover opening asks again only when the last answer is a minute old', async () => {
    accountsStore.clearAccounts();
    accountsWorld.access = 'linked';
    accountsStore.loadAccountsIfOld();
    assert.equal(accountsWorld.held.length, 1, 'nothing known: asked');
    accountsStore.loadAccountsIfOld();
    assert.equal(accountsWorld.held.length, 1, 'one on its way: not asked twice');
    const held = nextHeld();
    held.resolve(answerOf('a'));
    await settleAccounts();

    accountsWorld.now += 59999;
    accountsStore.loadAccountsIfOld();
    assert.equal(accountsWorld.held.length, 0, 'under a minute: not asked');
    accountsWorld.now += 1;
    accountsStore.loadAccountsIfOld();
    assert.equal(accountsWorld.held.length, 1, 'a minute: asked');
    nextHeld().resolve(answerOf('b'));
    await settleAccounts();
});

await checkAsync('accounts: a burst of accounts events is one read a second after the first, and none for a reader who may not watch', async () => {
    const realWindow = globalThis.window;
    const timers = [];
    globalThis.window = {
        setTimeout: (run, ms) => timers.push({run, ms}) && timers.length,
        clearTimeout: () => undefined,
    };
    try {
        accountsStore.clearAccounts();
        accountsWorld.access = 'locked';
        accountsStore.accountsEventHeard();
        assert.equal(timers.length, 0, 'locked: the event is ignored');

        accountsWorld.access = 'letIn';
        accountsStore.accountsEventHeard();
        accountsStore.accountsEventHeard();
        accountsStore.accountsEventHeard();
        assert.equal(timers.length, 1, 'one wait for the burst');
        assert.equal(timers[0].ms, 1000);
        const asked = accountsWorld.asked;
        timers[0].run();
        assert.equal(accountsWorld.asked, asked + 1, 'one read');
        nextHeld().resolve(answerOf('c'));
        await settleAccounts();
    } finally {
        globalThis.window = realWindow;
    }
});

await checkAsync('accounts: a "me" event reads them with the accounts event, at a random moment of up to a second, and at once for an open Connect flow', async () => {
    const realWindow = globalThis.window;
    const realRandom = Math.random;
    const timers = [];
    globalThis.window = {
        setTimeout: (run, ms) => timers.push({run, ms}) && timers.length,
        clearTimeout: () => undefined,
    };
    try {
        accountsStore.clearAccounts();
        accountsWorld.access = 'linked';
        const read = Promise.resolve(null);
        const asked = accountsWorld.asked;

        // Sent to everybody (an admin deleted all Steam data), with the accounts event after it:
        // one read for the two, spread over a second, and nothing in the instant itself.
        Math.random = () => 0.5;
        accountsStore.accountLinkHeard(read);
        accountsStore.accountsEventHeard();
        assert.equal(accountsWorld.asked, asked, 'nothing in the same instant as every other window');
        assert.deepEqual(timers.map((timer) => timer.ms), [500]);
        timers[0].run();
        assert.equal(accountsWorld.asked, asked + 1, 'one read for both');
        nextHeld().resolve(answerOf('e'));
        await settleAccounts();

        // A Connect flow open in this page: its listener is told, with the event's read, and the
        // accounts are read at once, as the reader looks at their own row.
        const heard = [];
        const stop = accountsStore.onAccountLink((eventRead) => heard.push(eventRead));
        accountsStore.accountLinkHeard(read);
        assert.deepEqual(heard, [read]);
        assert.equal(accountsWorld.asked, asked + 2, 'read at once');
        assert.equal(timers.length, 1);
        stop();
        nextHeld().resolve(null);
        await settleAccounts();

        // A reader who may not watch reads nothing for it.
        accountsWorld.access = 'locked';
        accountsStore.accountLinkHeard(read);
        assert.equal(timers.length, 1);
        assert.equal(accountsWorld.asked, asked + 2);
    } finally {
        globalThis.window = realWindow;
        Math.random = realRandom;
        accountsWorld.access = 'locked';
    }
});

await checkAsync('accounts: read as soon as "me" says the reader may watch, and emptied as soon as it says not', async () => {
    accountsStore.clearAccounts();
    accountsWorld.access = 'locked';
    const stop = accountsStore.followAccountsAccess();
    assert.equal(accountsWorld.held.length, 0, 'locked: nothing asked');
    meChanged('letIn');
    assert.equal(accountsWorld.held.length, 1, 'let in: asked');
    nextHeld().resolve(answerOf('d'));
    await settleAccounts();
    assert.equal(accountsStore.getAccounts().v, 'd');

    meChanged('locked');
    assert.equal(accountsStore.getAccounts(), null, 'let out: emptied');
    stop();
    meChanged('letIn');
    assert.equal(accountsWorld.held.length, 0, 'stopped: nothing asked');
    accountsWorld.access = 'locked';
});

await checkAsync('accounts: names Mattermost has not loaded are asked for once, only for people in a game, and go at sign-out', async () => {
    accountsStore.clearAccounts();
    accountsWorld.access = 'linked';
    accountsWorld.loaded = {'u-dave': {id: 'u-dave', username: 'dave', first_name: 'Dave'}};
    accountsWorld.namesAsked = [];
    const playing = {name: 'x', url: '', state: 'online', inGame: true, game: 'ELDEN RING'};
    const people = {
        'u-dave': {steam: playing},
        'u-erin': {steam: playing},
        'u-bob': {steam: {name: 'y', url: '', state: 'online'}},
        'u-caro': {hytale: {name: 'CaroBuilds'}},
    };
    let read = accountsStore.loadAccounts();
    nextHeld().resolve(answerOf('e', people));
    await read;
    assert.deepEqual(accountsWorld.namesAsked, [['u-erin']], 'Dave is loaded; Bob is online, not playing; Caro is Hytale only');
    assert.equal(accountsStore.personName('u-dave'), 'Dave', 'from Mattermost’s own store');
    assert.equal(accountsStore.personName('u-erin'), '', 'not known yet');
    accountsWorld.namesHeld.shift().resolve([{id: 'u-erin', username: 'erin', first_name: 'Erin'}]);
    await settleAccounts();
    assert.equal(accountsStore.personName('u-erin'), 'Erin');

    read = accountsStore.loadAccounts();
    nextHeld().resolve(answerOf('f', people));
    await read;
    assert.equal(accountsWorld.namesAsked.length, 1, 'asked once');

    // Signing out drops the names, and an answer on its way cannot put one back.
    accountsStore.clearAccounts();
    assert.equal(accountsStore.personName('u-erin'), '');
    read = accountsStore.loadAccounts();
    nextHeld().resolve(answerOf('g', people));
    await read;
    assert.equal(accountsWorld.namesAsked.length, 2, 'asked again after the store was emptied');
    const late = accountsWorld.namesHeld.shift();
    accountsStore.clearAccounts();
    late.resolve([{id: 'u-erin', username: 'erin', first_name: 'Erin'}]);
    await settleAccounts();
    assert.equal(accountsStore.personName('u-erin'), '', 'not refilled by the answer that came after');
    accountsWorld.loaded = {};
    accountsWorld.access = 'locked';
});

check('index.tsx registers the accounts event and the one "me" event, empties the store at sign-out and when the plugin stops, and follows access', () => {
    const index = readFileSync(new URL('../webapp/src/index.tsx', import.meta.url), 'utf8');
    const signedOut = index.match(/followSignIn\(store, \(\) => \{[\s\S]*?\}, \(\) => \{([\s\S]*?)\}\);/);
    assert.ok(signedOut, 'the onSignedOut callback given to followSignIn');
    assert.match(signedOut[1], /^\s*clearAccounts\(\);$/m, 'clearAccounts at sign-out');
    const uninitialize = index.slice(index.indexOf('public uninitialize('));
    assert.match(uninitialize, /^\s*clearAccounts\(\);$/m, 'clearAccounts at uninitialize');
    assert.match(uninitialize, /this\.unfollowAccounts\?\.\(\);/);
    assert.match(index, /this\.unfollowAccounts = followAccountsAccess\(\);/);
    assert.match(index, /registry\.registerWebSocketEventHandler\(ACCOUNTS_EVENT, /, 'ACCOUNTS_EVENT registered');
    assert.match(uninitialize, /registry\.unregisterWebSocketEventHandler\(ACCOUNTS_EVENT\);/, 'ACCOUNTS_EVENT unregistered');
    assert.match(index, /const ACCOUNTS_EVENT = `custom_\$\{PLUGIN_ID\}_accounts`;/);

    // Everything about the reader's own state is one event since contract 3,
    // "me", registered (and unregistered) with the access follower, and
    // heard by all three that used to have an event each: the reader's
    // access, the Connect Twitch sheet, and the Steam and Hytale flows. One
    // handler, me.ts meHeard, which the page installs and the lab presses.
    const me = readFileSync(new URL('../webapp/src/me.ts', import.meta.url), 'utf8');
    assert.match(me, /export const ME_EVENT = `custom_\$\{PLUGIN_ID\}_me`;/);
    assert.match(index, /import \{ME_EVENT, meHeard\} from '\.\/me';/);
    assert.match(index, /this\.uninstallAccess = installAccess\(registry, ME_EVENT, meHeard\);/);
    const heard = me.match(/export function meHeard\(message\?: unknown\): void \{([\s\S]*?)\n\}/);
    assert.ok(heard, 'meHeard');
    // The event carries the server's name for the plugin and its artwork,
    // taken before anything is read again (name.ts).
    assert.ok(heard[1].trimStart().startsWith('takeBrand((message as {data?: unknown} | undefined)?.data);'), 'the "me" event takes the name and the artwork first');
    for (const call of ['const read = accessEventHeard();', 'twitchLinkHeard(read);', 'accountLinkHeard(read);']) {
        assert.ok(heard[1].includes(call), `the "me" event calls ${call}`);
    }
    const lab = readFileSync(new URL('../webapp/lab/index.tsx', import.meta.url), 'utf8');
    assert.match(lab, /installAccess\(LAB_ACCESS_REGISTRY, ME_EVENT, meHeard\);/, 'the lab installs the same handler');
    for (const file of ['../webapp/src/index.tsx', '../webapp/src/me.ts', '../webapp/lab/index.tsx', '../webapp/lab/rows340.tsx', '../webapp/lab/mocks/twitchEmbed.tsx']) {
        const source = readFileSync(new URL(file, import.meta.url), 'utf8');
        for (const gone of ['_access`', '_twitch_link`', '_account_link`', 'villanet_access\'', 'twitch_link', 'account_link']) {
            assert.ok(!source.includes(gone), `${file}: nothing left of the event that was ${gone}`);
        }
        assert.doesNotMatch(source, /^\s*(accessEventHeard|twitchLinkHeard|accountLinkHeard)\(\);/m, `${file}: a part of the "me" event played on its own`);
    }
    const access = readFileSync(new URL('../webapp/src/access.ts', import.meta.url), 'utf8');
    assert.match(access, /registry\.registerWebSocketEventHandler\(eventName, heard\);/);
    assert.match(access, /registry\.unregisterWebSocketEventHandler\(eventName\);/);
});

console.log(`check-away: ${checks} checks passed`);
