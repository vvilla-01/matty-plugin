// The panel's reads of the server, by the rule both clients keep (contract/README.md, "What a
// client must do"): the small question about the reader on the hot paths (identity.ts
// refreshAccess), re-reads that say which copy is in hand (store.ts refreshSnapshot), and the
// requests themselves (client.ts), and the boards, which are read only when somebody looks or the
// snapshot says they have changed (boards.ts). Each module is run here as it is written, with the
// server, the clock and Mattermost played from here.
//
//   node build/check-reads.mjs

import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createRequire} from 'node:module';

const require = createRequire(import.meta.url);
const ts = require('../webapp/node_modules/typescript');

let checks = 0;
async function check(name, body) {
    try {
        await body();
        checks++;
    } catch (error) {
        console.error(`FAILED: ${name}`);
        throw error;
    }
}

const transpiled = (file) => ts.transpileModule(readFileSync(new URL(`../webapp/src/${file}`, import.meta.url), 'utf8'), {
    compilerOptions: {module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2020},
    fileName: file,
}).outputText;
const dataURL = (source) => `data:text/javascript;base64,${Buffer.from(source).toString('base64')}`;

// A module as written, with each of its imports pointed at a stand-in.
const wired = (file, standIns) => transpiled(file).replace(/from '([^']*)'/g, (_, path) => {
    assert.ok(path in standIns, `${file} imports ${path}, and there is no stand-in for it`);

    return `from '${dataURL(standIns[path])}'`;
});
const load = (file, standIns) => import(dataURL(wired(file, standIns)));

// The plugin's id, as plugin.json has it: every address the client asks for is under it.
const PLUGIN_ID = JSON.parse(readFileSync(new URL('../plugin.json', import.meta.url), 'utf8')).id;
const PLUGIN_BASE = `/plugins/${PLUGIN_ID}`;

// The server, as each test plays it: every request is noted, and answered by whatever the test
// put in line for it.
const world = {now: 1000000, asked: [], answers: [], signedIn: 'u-dave'};
globalThis.villanetReadsWorld = world;
const next = (path, extra) => {
    world.asked.push(extra === undefined ? path : `${path} ${extra}`);
    const answer = world.answers.shift();
    if (answer === undefined) {
        return Promise.reject(new Error(`nothing in line for ${path}`));
    }

    return answer instanceof Error ? Promise.reject(answer) : Promise.resolve(answer);
};
world.next = next;
const settle = () => new Promise((resolve) => setImmediate(resolve));

const react = 'export default {useState: () => [0, () => {}], useEffect: () => {}, useRef: (value) => ({current: value})};';

// What the server calls the plugin (name.ts) and the banner remembered from the last
// "me" (banner.ts), each as written. One copy of each, the same for every module that
// imports it, as in the page: a module imported by the same address is the same module.
const nameModule = wired('name.ts', {react});
const bannerModule = wired('banner.ts', {'./color': transpiled('color.ts')});
const name = await import(dataURL(nameModule));
const client = `
    const world = globalThis.villanetReadsWorld;
    export const fetchMe = () => world.next('/me');
    export const fetchAccess = () => world.next('/access');
    export const fetchStreamsIfChanged = (tag) => world.next('/streams', tag === '' ? '(no tag)' : tag);
    export const fetchBoardsIfChanged = (tag) => world.next('/boards', tag === '' ? '(no tag)' : tag);
`;

const identity = await load('identity.ts', {
    react,
    './client': client,
    './clock': 'export const now = () => globalThis.villanetReadsWorld.now;',
    './banner': bannerModule,
    './lockLogic': transpiled('lockLogic.ts'),
    './mattermost': 'export const currentUserId = () => globalThis.villanetReadsWorld.signedIn; export const mattermostStore = () => ({});',
    './name': nameModule,
});

const me = (access, more = {}) => ({userId: 'u-dave', username: 'dave', access, configured: true, streamKey: access === 'linked' ? 'live-dave' : '', ...more});

await check('the small question: /access, and "me" only when the answer is not what is held', async () => {
    identity.setIdentity(null);
    world.asked.length = 0;

    // Nothing held yet: the whole of "me".
    world.answers.push(me('linked'));
    await identity.refreshAccess();
    assert.deepEqual(world.asked, ['/me']);
    assert.equal(identity.accessNow(), 'linked');

    // Held, and still so: one small read, and "me" is not asked for.
    world.answers.push({access: 'linked', configured: true, contract: 3, version: '3.6.0'});
    world.now += 60000;
    await identity.refreshAccess();
    assert.deepEqual(world.asked, ['/me', '/access']);
    assert.equal(identity.getIdentity().streamKey, 'live-dave', 'nothing that was known is replaced');
    assert.equal(identity.lastIdentityReadAt(), world.now, 'a read for any reason counts as a read');

    // Let out since: the answer differs, so everything is read again, and the page follows.
    world.answers.push({access: 'locked', configured: true}, me('locked'));
    await identity.refreshAccess();
    assert.deepEqual(world.asked, ['/me', '/access', '/access', '/me']);
    assert.equal(identity.accessNow(), 'locked');
    assert.equal(identity.getIdentity().streamKey, '');

    // The plugin no longer set up is a change too.
    world.answers.push({access: 'locked', configured: false}, me('locked', {configured: false}));
    await identity.refreshAccess();
    assert.equal(identity.getIdentity().configured, false);
});

await check('the small question takes nothing away when it is not answered', async () => {
    identity.setIdentity(me('linked'));
    world.asked.length = 0;
    let told = 0;
    const stop = identity.onIdentityChange(() => told++);

    // The store did not answer: what was known stands, and the page notes it could not check.
    world.answers.push({access: 'unknown', configured: true});
    await identity.refreshAccess();
    assert.deepEqual(world.asked, ['/access'], '"me" is not read for it');
    assert.equal(identity.latestAccess(), 'unknown');
    assert.equal(identity.accessNow(), 'linked', 'nobody known is shut out by a store that did not answer');
    assert.equal(told, 1);

    // No answer at all: the same.
    world.answers.push(new Error('offline'));
    await identity.refreshAccess();
    assert.equal(identity.accessNow(), 'linked');
    assert.equal(told, 1, 'said once, not at every failed read');

    // And a good answer clears it.
    world.answers.push({access: 'linked', configured: true});
    await identity.refreshAccess();
    assert.equal(identity.latestAccess(), 'linked');
    assert.equal(told, 2);
    stop();
});

await check('the small question shares a read already on its way, and is dropped with the plugin', async () => {
    identity.setIdentity(me('linked'));
    world.asked.length = 0;

    world.answers.push({access: 'linked', configured: true});
    const both = [identity.refreshAccess(), identity.refreshAccess()];
    await Promise.all(both);
    assert.deepEqual(world.asked, ['/access'], 'two askers, one request');

    // Switched off while the read was out: its answer is about a plugin that has gone.
    world.answers.push({access: 'locked', configured: true});
    const late = identity.refreshAccess();
    identity.setIdentity(null);
    await late;
    assert.deepEqual(world.asked, ['/access', '/access'], 'no "me" read for an answer nobody is waiting for');
    assert.equal(identity.getIdentity(), null);
});

await check('the name and the artwork are taken from whichever answer carries them, and nothing else is', async () => {
    identity.setIdentity(null);
    world.asked.length = 0;
    assert.equal(name.nameNow(), 'Matty', 'before any answer, the product\'s own name');
    assert.equal(name.artworkNow(), null, 'and no pictures until the server has said');

    // "me" says it.
    world.answers.push(me('linked', {name: 'Movie Night Club', artwork: {emblem: true, ghost: false}}));
    await identity.refreshAccess();
    assert.equal(name.nameNow(), 'Movie Night Club');
    assert.deepEqual(name.artworkNow(), {emblem: true, ghost: false});

    // The small question says it too, and is taken though "me" is not read again.
    world.answers.push({access: 'linked', configured: true, name: 'Saturday Night Film & Games Club', artwork: {emblem: false, ghost: true}});
    world.now += 60000;
    await identity.refreshAccess();
    assert.deepEqual(world.asked, ['/me', '/access']);
    assert.equal(name.nameNow(), 'Saturday Night Film & Games Club', 'thirty-two characters is a name');

    // What is not a name, or not artwork, leaves what was there.
    for (const bad of ['', '   ', 'x'.repeat(33), 'two\nlines', 7]) {
        world.answers.push({access: 'linked', configured: true, name: bad, artwork: {emblem: 'yes'}});
        await identity.refreshAccess();
        assert.equal(name.nameNow(), 'Saturday Night Film & Games Club', JSON.stringify(bad));
        assert.deepEqual(name.artworkNow(), {emblem: false, ghost: true});
    }

    // And an answer that says nothing of either.
    world.answers.push({access: 'linked', configured: true});
    await identity.refreshAccess();
    assert.equal(name.nameNow(), 'Saturday Night Film & Games Club');

    world.answers.push(me('linked', {name: 'Matty', artwork: {emblem: false, ghost: false}}));
    identity.setIdentity(null);
    await identity.refreshAccess();
    assert.equal(name.nameNow(), 'Matty');
});

await check('a "me" event reads "me" with a request that starts after it, and a burst costs one read more at most', async () => {
    identity.setIdentity(me('linked'));
    world.asked.length = 0;

    // Nothing on its way: one read, now.
    world.answers.push(me('linked'));
    await identity.refreshIdentityAfter();
    assert.deepEqual(world.asked, ['/me']);

    // A read on its way, answered before the change: it is let finish, and one more read is made
    // after it, shared by every event of the burst.
    let answerEarlier;
    world.next = (path) => {
        world.asked.push(path);

        return new Promise((resolve) => {
            answerEarlier = resolve;
        });
    };
    const earlier = identity.refreshIdentity();
    world.next = next;
    const after = [identity.refreshIdentityAfter(), identity.refreshIdentityAfter(), identity.refreshIdentityAfter()];
    assert.equal(after[0], after[1]);
    assert.equal(after[0], after[2]);
    assert.deepEqual(world.asked, ['/me', '/me'], 'nothing more until the earlier answer is in');
    world.answers.push(me('linked', {twitch: {available: true}}));
    answerEarlier(me('linked', {twitch: {available: true, login: 'dave'}}));
    await earlier;
    assert.equal(identity.getIdentity().twitch.login, 'dave', 'the earlier answer');
    await Promise.all(after);
    assert.deepEqual(world.asked, ['/me', '/me', '/me']);
    assert.equal(identity.getIdentity().twitch.login, undefined, 'and the one made after the event is what stays');

    // Not after the identity was cleared meanwhile (sign-out, the plugin going).
    world.next = (path) => {
        world.asked.push(path);

        return new Promise((resolve) => {
            answerEarlier = resolve;
        });
    };
    const again = identity.refreshIdentity();
    world.next = next;
    const queued = identity.refreshIdentityAfter();
    identity.setIdentity(null);
    answerEarlier(me('linked'));
    await again;
    assert.equal(await queued, null);
    assert.deepEqual(world.asked, ['/me', '/me', '/me', '/me']);
});

// access.ts as written, with the rest of the page played from here: which reads it asks for and when.
world.calls = [];
world.sessions = 0;
const access = await load('access.ts', {
    react,
    './clips': 'export const closeClips = () => {}; export const closeEditor = () => {}; export const dropClipCaches = () => {}; export const getClipEditor = () => null; export const getClipsDialog = () => null; export const openWaitingClip = () => {};',
    './accounts': 'export const loadAccounts = () => Promise.resolve();',
    './client': 'export const setAccessRefusalListener = () => {};',
    './clock': 'export const now = () => globalThis.villanetReadsWorld.now; export const onClockAdvance = () => () => {};',
    './identity': `
        const world = globalThis.villanetReadsWorld;
        let readAt = 0;
        const read = (what) => {
            world.calls.push(what);
            readAt = world.now;

            return Promise.resolve(null);
        };
        export const getIdentity = () => null;
        export const lastDefiniteAccess = () => 'linked';
        export const lastIdentityReadAt = () => readAt;
        export const latestAccess = () => 'linked';
        export const onIdentityChange = () => () => {};
        export const refreshAccess = () => read('/access');
        export const refreshIdentity = () => read('/me');
        export const refreshIdentityAfter = () => read('/me, after');
        export const accessNow = () => 'linked';
    `,
    './lockLogic': transpiled('lockLogic.ts'),
    './lockPending': 'export const clearPending = () => {}; export const takePending = () => null;',
    './motion': 'export const ACCESS_RECHECK_MS = 60000;',
    './name': nameModule,
    './sessions':'export const sessionCount = () => globalThis.villanetReadsWorld.sessions; export const stopAllSessions = () => {};',
    './store': 'export const getSnapshot = () => ({streams: []}); export const refreshSnapshot = () => Promise.resolve();',
    './watching': 'export const addWatching = () => {}; export const clearWatching = () => {};',
});

await check('while something plays, the whole of "me" once a minute in a window that is looked at, and the small question in one that is not', () => {
    const realDocument = globalThis.document;
    globalThis.document = {visibilityState: 'visible'};
    try {
        world.calls.length = 0;
        world.sessions = 1;

        // A window that is looked at, its panel open or not (the small player shows what only
        // "me" carries too): "me", as it always was.
        world.now += 60000;
        access.recheckWhilePlaying();
        assert.deepEqual(world.calls, ['/me']);

        // Not a minute since: nothing.
        world.now += 59999;
        access.recheckWhilePlaying();
        assert.deepEqual(world.calls, ['/me']);

        // The window hidden (a tab in the background, playing for its sound): the small question.
        globalThis.document.visibilityState = 'hidden';
        world.now += 1;
        access.recheckWhilePlaying();
        assert.deepEqual(world.calls, ['/me', '/access']);

        // Looked at again: "me" at the next minute.
        globalThis.document.visibilityState = 'visible';
        world.now += 60000;
        access.recheckWhilePlaying();
        assert.deepEqual(world.calls, ['/me', '/access', '/me']);

        // Nothing playing: nothing at all.
        world.sessions = 0;
        world.now += 60000;
        access.recheckWhilePlaying();
        assert.deepEqual(world.calls, ['/me', '/access', '/me']);
    } finally {
        globalThis.document = realDocument;
        world.sessions = 0;
    }
});

await check('a "me" event reads "me" after it at once, and asks about access again 12 to 15 seconds later, at a random moment', async () => {
    const timers = [];
    const realSetTimeout = globalThis.setTimeout;
    const realClearTimeout = globalThis.clearTimeout;
    const realRandom = Math.random;
    globalThis.window = globalThis;
    globalThis.setTimeout = (run, ms) => timers.push({run, ms}) - 1;
    globalThis.clearTimeout = () => {};
    try {
        for (const [share, ms] of [[0, 12000], [0.5, 13500], [0.9999, 14999]]) {
            world.calls.length = 0;
            timers.length = 0;
            Math.random = () => share;
            const read = access.accessEventHeard();
            assert.deepEqual(world.calls, ['/me, after'], 'read at once, by a read that starts after the event');
            assert.ok(read instanceof Promise, 'and the others who hear it are handed that read');
            assert.deepEqual(timers.map((timer) => timer.ms), [ms]);
            timers[0].run();
            assert.deepEqual(world.calls, ['/me, after', '/access']);
        }
    } finally {
        globalThis.setTimeout = realSetTimeout;
        globalThis.clearTimeout = realClearTimeout;
        Math.random = realRandom;
    }
});

const store = await load('store.ts', {react, './client': client});
const snapshotAt = (fetchedAt, title = 'Dave') => ({streams: [{handle: 'h1', title}], fetchedAt, configured: true});

await check('a re-read of the snapshot says which one is in hand', async () => {
    world.asked.length = 0;
    let told = 0;
    const stop = store.subscribe(() => told++);

    // Nothing in hand: asked for whole, and kept with the tag it came under.
    world.answers.push({value: snapshotAt(100), tag: '"aaaa"'});
    assert.equal((await store.refreshSnapshot()).fetchedAt, 100);
    assert.deepEqual(world.asked, ['/streams (no tag)']);
    assert.equal(told, 1);

    // "That one still": the same snapshot, handed to everybody listening as a new copy, as every
    // poll's answer was until 3.6.0 (a card's "live for" is worked out from the clock as it is
    // drawn, and a popped-out panel ends a tile's grace on a new list of streams); and the copy
    // counts as fresh. The browser's clock is moved on first, past any staleness, so that only
    // this answer can have made it fresh.
    const realDateNow = Date.now;
    try {
        const later = realDateNow() + 120000;
        Date.now = () => later;
        world.answers.push(null);
        const before = store.getSnapshot();
        const after = await store.refreshSnapshot();
        assert.deepEqual(world.asked.at(-1), '/streams "aaaa"');
        assert.equal(told, 2, 'everybody listening is told');
        assert.notEqual(after, before);
        assert.notEqual(after.streams, before.streams, 'a new list of streams');
        assert.notEqual(after.streams[0], before.streams[0]);
        assert.deepEqual(after, before, 'with nothing in it changed');
        assert.ok(store.snapshotAge() < 1000, 'a 304 counts the copy as fresh');
        world.answers.push(null);
        await store.refreshSnapshot();
        assert.deepEqual(world.asked.at(-1), '/streams "aaaa"', 'under the same tag');
    } finally {
        Date.now = realDateNow;
    }

    // A push carries its tag bare; the next read sends it quoted, as an entity tag is.
    store.setPushedSnapshot(snapshotAt(200, 'Dave, later'), 'bbbb');
    assert.equal(told, 4);
    world.answers.push(null);
    await store.refreshSnapshot();
    assert.deepEqual(world.asked.at(-1), '/streams "bbbb"');

    // A push with no tag, or a snapshot set without one, is read whole next time.
    store.setPushedSnapshot(snapshotAt(300), undefined);
    world.answers.push({value: snapshotAt(300), tag: '"cccc"'});
    await store.refreshSnapshot();
    assert.deepEqual(world.asked.at(-1), '/streams (no tag)');
    stop();
});

await check('a read never puts back a snapshot older than the one a push brought meanwhile', async () => {
    store.setPushedSnapshot(snapshotAt(500, 'pushed'), 'dddd');
    world.answers.push({value: snapshotAt(400, 'read, and older'), tag: '"old"'});
    await store.refreshSnapshot();
    assert.equal(store.getSnapshot().streams[0].title, 'pushed');

    // And the tag in hand is still the push's.
    world.answers.push(null);
    await store.refreshSnapshot();
    assert.deepEqual(world.asked.at(-1), '/streams "dddd"');

    // A failed read keeps what is in hand, and says so by rejecting.
    world.answers.push(new Error('offline'));
    await assert.rejects(store.refreshSnapshot());
    assert.equal(store.getSnapshot().streams[0].title, 'pushed');
    await settle();
});

await check('a read with the same fetchedAt as a push that landed while it was on its way does not undo that push', async () => {
    // A change of the roster or the clips is the snapshot the server holds, republished with that
    // change: the same fetchedAt, another tag. Here it lands while a read answered before it is on
    // its way: the push stays, with its tag.
    store.setPushedSnapshot(snapshotAt(600, 'before bob joined'), 'eeee');
    let answer;
    world.next = (path, extra) => {
        world.asked.push(`${path} ${extra}`);

        return new Promise((resolve) => {
            answer = resolve;
        });
    };
    const read = store.refreshSnapshot();
    world.next = next;
    store.setPushedSnapshot(snapshotAt(600, 'bob in voice'), 'ffff');
    answer({value: snapshotAt(600, 'before bob joined'), tag: '"eeee"'});
    await read;
    assert.equal(store.getSnapshot().streams[0].title, 'bob in voice');
    world.answers.push(null);
    await store.refreshSnapshot();
    assert.deepEqual(world.asked.at(-1), '/streams "ffff"');

    // The other way round: the push of that change was missed (a reconnect), and a read brings
    // it with the same fetchedAt. With no push meanwhile, the read's copy is the one kept.
    world.answers.push({value: snapshotAt(600, 'clip voted'), tag: '"gggg"'});
    await store.refreshSnapshot();
    assert.equal(store.getSnapshot().streams[0].title, 'clip voted');
    world.answers.push(null);
    await store.refreshSnapshot();
    assert.deepEqual(world.asked.at(-1), '/streams "gggg"');
});

// ---- The boards: read when somebody looks, or when the snapshot says they have changed ----

// The snapshot store, played: boards.ts follows its one word about the boards.
world.snapshot = {streams: [], fetchedAt: 0, configured: true};
world.snapshotListeners = new Set();
const pushSnapshot = (version) => {
    world.snapshot = {streams: [], fetchedAt: 1, configured: true, ...(version ? {boards: {version}} : {})};
    Array.from(world.snapshotListeners).forEach((listener) => listener());
};

// Timers as a browser has them, and no wait before a read that follows a change: the spread is
// a random share of three seconds, and here the share is none.
globalThis.window = globalThis;
const realRandom = Math.random;
Math.random = () => 0;

const boards = await load('boards.ts', {
    './client': client,
    './clock': 'export const now = () => globalThis.villanetReadsWorld.now;',
    './store': `
        const world = globalThis.villanetReadsWorld;
        export const getSnapshot = () => world.snapshot;
        export const subscribe = (listener) => {
            world.snapshotListeners.add(listener);

            return () => world.snapshotListeners.delete(listener);
        };
    `,
});
const boardsOf = (version, seconds = 60) => ({
    version,
    allTime: [{rank: 1, title: 'Dave', seconds, userId: 'u-dave'}],
    people: {'u-dave': {rows: [{kind: 'board', id: 'alltime', rank: 1, title: 'Champion', board: 'All time', seconds}]}},
});

await check('the boards are read as the page starts, beside the snapshot, and again only when its word for them moves', async () => {
    world.asked.length = 0;
    let told = 0;

    // As the page starts: read at once, whole, without waiting for the snapshot; and the snapshot,
    // saying the version that read brings while it is on its way, makes no second one.
    let answerFirst;
    world.next = (path, extra) => {
        world.asked.push(`${path} ${extra}`);

        return new Promise((resolve) => {
            answerFirst = resolve;
        });
    };
    const uninstall = boards.installBoards();
    world.next = next;
    const stop = boards.onBoards(() => told++);
    assert.deepEqual(world.asked, ['/boards (no tag)']);
    pushSnapshot('');
    pushSnapshot('v1');
    await settle();
    assert.deepEqual(world.asked, ['/boards (no tag)']);
    assert.equal(boards.boardsRead(), false);
    assert.deepEqual(boards.getBoards(), {version: '', people: {}});
    answerFirst({value: boardsOf('v1'), tag: '"t1"'});
    await settle();
    assert.deepEqual(world.asked, ['/boards (no tag)'], 'one read, not two');
    assert.equal(boards.getBoards().people['u-dave'].rows[0].title, 'Champion');
    assert.equal(boards.boardsRead(), true);
    assert.equal(told, 1);

    // Any number of snapshots with the same word, and a minute of seconds ticking: nothing.
    pushSnapshot('v1');
    pushSnapshot('v1');
    await settle();
    assert.deepEqual(world.asked, ['/boards (no tag)']);

    // The word moves (a place changed): read again, with the tag in hand, a moment later.
    world.answers.push({value: boardsOf('v2'), tag: '"t2"'});
    pushSnapshot('v2');
    assert.deepEqual(world.asked, ['/boards (no tag)'], 'not in the same instant as every other window');
    await new Promise((resolve) => setTimeout(resolve, 5));
    await settle();
    assert.deepEqual(world.asked, ['/boards (no tag)', '/boards "t1"']);
    assert.equal(boards.getBoards().version, 'v2');
    assert.equal(told, 2);

    // A read for a word that comes back with an older copy (another node of a cluster had not
    // caught up) gives the word back: the first snapshot to say it again half a minute later reads
    // again, none sooner, and so on until the copy has caught up.
    world.answers.push({value: boardsOf('v2'), tag: '"t2b"'});
    pushSnapshot('v3');
    pushSnapshot('v3');
    await new Promise((resolve) => setTimeout(resolve, 5));
    await settle();
    assert.equal(world.asked.length, 3);
    pushSnapshot('v3');
    await new Promise((resolve) => setTimeout(resolve, 5));
    await settle();
    assert.equal(world.asked.length, 3, 'not by the very next snapshot');
    world.now += 30000;
    world.answers.push({value: boardsOf('v2'), tag: '"t2c"'});
    pushSnapshot('v3');
    await new Promise((resolve) => setTimeout(resolve, 5));
    await settle();
    assert.equal(world.asked.length, 4, 'half a minute later, again');
    world.now += 30000;
    world.answers.push({value: boardsOf('v3'), tag: '"t3"'});
    pushSnapshot('v3');
    await new Promise((resolve) => setTimeout(resolve, 5));
    await settle();
    assert.equal(world.asked.length, 5, 'and again, until it is caught up with');
    assert.equal(boards.getBoards().version, 'v3');
    pushSnapshot('v3');
    await new Promise((resolve) => setTimeout(resolve, 5));
    await settle();
    assert.equal(world.asked.length, 5, 'and then no more');

    stop();
    uninstall();
    assert.deepEqual(boards.getBoards(), {version: '', people: {}}, 'and the plugin switched off leaves nothing');
    assert.equal(boards.boardsRead(), false);
});

await check('a read of the boards that fails is made again for the word half a minute later, and a read on its way when the word moved is followed by one more', async () => {
    pushSnapshot('');
    world.asked.length = 0;

    // The page's first read fails (a proxy's 502): the snapshot that names the boards asks again,
    // at once, since nothing is in hand.
    world.answers.push(new Error('502'));
    const uninstall = boards.installBoards();
    await settle();
    assert.equal(boards.boardsRead(), false);
    world.answers.push(new Error('502'));
    pushSnapshot('v1');
    await settle();
    assert.equal(world.asked.length, 2, 'asked again for the word');

    // That failed too. The snapshots that repeat the word ask nothing for half a minute, however
    // many there are (no storm); the first one after that (the poll's, thirty seconds on) asks
    // again, and so it goes on for as long as the server fails. A change of word is asked about
    // at once.
    pushSnapshot('v1');
    pushSnapshot('v1');
    await settle();
    assert.equal(world.asked.length, 2, 'no storm');
    world.now += 30000;
    world.answers.push(new Error('502'));
    pushSnapshot('v1');
    await settle();
    assert.equal(world.asked.length, 3, 'half a minute later, again');
    pushSnapshot('v1');
    pushSnapshot('v1');
    await settle();
    assert.equal(world.asked.length, 3, 'and not sooner');
    world.answers.push({value: boardsOf('v2'), tag: '"t2"'});
    pushSnapshot('v2');
    await settle();
    assert.equal(world.asked.length, 4);
    assert.equal(boards.getBoards().version, 'v2');

    // A read on its way (somebody opened a profile), asked before the word moved, comes back with
    // the copy before it: one read more, at once, for the word.
    world.now += 60000;
    let answerLook;
    world.next = (path, extra) => {
        world.asked.push(`${path} ${extra}`);

        return new Promise((resolve) => {
            answerLook = resolve;
        });
    };
    boards.lookAtBoards();
    world.next = next;
    assert.equal(world.asked.length, 5);
    pushSnapshot('v3');
    await new Promise((resolve) => setTimeout(resolve, 5));
    await settle();
    assert.equal(world.asked.length, 5, 'the read on its way is waited for');
    world.answers.push({value: boardsOf('v3'), tag: '"t3"'});
    answerLook(null);
    await settle();
    await settle();
    assert.deepEqual(world.asked.slice(5), ['/boards "t2"']);
    assert.equal(boards.getBoards().version, 'v3');

    // And when the read on its way already brings the word, nothing more.
    world.now += 60000;
    world.next = (path, extra) => {
        world.asked.push(`${path} ${extra}`);

        return new Promise((resolve) => {
            answerLook = resolve;
        });
    };
    boards.lookAtBoards();
    world.next = next;
    pushSnapshot('v4');
    await new Promise((resolve) => setTimeout(resolve, 5));
    answerLook({value: boardsOf('v4'), tag: '"t4"'});
    await settle();
    await settle();
    assert.equal(world.asked.length, 7);
    assert.equal(boards.getBoards().version, 'v4');
    uninstall();
});

await check('somebody looking is given the seconds as they stand, at most once a minute, and a failed read takes nothing away', async () => {
    // A snapshot that says nothing of the boards, so that nothing here is read for a change; and
    // the page's first read, as it starts, failed.
    pushSnapshot('');
    world.answers.push(new Error('offline'));
    const uninstall = boards.installBoards();
    await settle();
    world.asked.length = 0;
    let told = 0;
    const stop = boards.onBoards(() => told++);

    // Never read: a profile opening asks.
    world.answers.push({value: boardsOf('v1', 60), tag: '"t1"'});
    boards.lookAtBoards();
    await settle();
    assert.deepEqual(world.asked, ['/boards (no tag)']);

    // Opened again within the minute: the copy in hand is shown, and nothing is asked.
    world.now += 30000;
    boards.lookAtBoards();
    await settle();
    assert.equal(world.asked.length, 1);

    // A minute on: asked with the tag, and "that one still" changes nothing and tells nobody.
    world.now += 30000;
    world.answers.push(null);
    boards.lookAtBoards();
    await settle();
    assert.deepEqual(world.asked.at(-1), '/boards "t1"');
    assert.equal(told, 1);

    // That answer vouched for the copy: it is fresh for another minute.
    world.now += 59000;
    boards.lookAtBoards();
    await settle();
    assert.equal(world.asked.length, 2);

    // The seconds have moved: the same version, a new tag, and the figures as counted.
    world.now += 1000;
    world.answers.push({value: boardsOf('v1', 120), tag: '"t2"'});
    boards.lookAtBoards();
    await settle();
    assert.equal(boards.getBoards().allTime[0].seconds, 120);
    assert.equal(told, 2);

    // No answer: what was shown stays, and the next look asks again.
    world.now += 60000;
    world.answers.push(new Error('offline'));
    boards.lookAtBoards();
    await settle();
    assert.equal(boards.getBoards().allTime[0].seconds, 120);
    assert.equal(told, 2);
    world.answers.push(null);
    boards.lookAtBoards();
    await settle();
    assert.deepEqual(world.asked.at(-1), '/boards "t2"');

    // Two askers at once share one request.
    world.now += 60000;
    world.answers.push(null);
    await Promise.all([boards.loadBoards(), boards.loadBoards()]);
    assert.equal(world.asked.length, 6);

    // An open board asks as it opens and is counted, so two of them are one timer.
    const timers = [];
    const realSetInterval = globalThis.setInterval;
    const realClearInterval = globalThis.clearInterval;
    globalThis.setInterval = (run, ms) => timers.push({run, ms, cleared: false}) - 1;
    globalThis.clearInterval = (id) => {
        timers[id].cleared = true;
    };

    world.now += 60000;
    world.answers.push(null);
    const closeOne = boards.watchBoards();
    const closeTwo = boards.watchBoards();
    await settle();
    assert.equal(world.asked.length, 7, 'read once as the first opens; the second finds it fresh');
    assert.deepEqual(timers.map((timer) => timer.ms), [60000], 'one timer for the two, a minute long');

    // The minute's read asks whatever the clock says of the copy: the answer to the read made
    // as the board opened landed a moment after the timer started, so by the copy's age the
    // minute is never quite up, and going by that it read every other minute.
    world.now += 59990;
    world.answers.push({value: boardsOf('v1', 180), tag: '"t3"'});
    timers[0].run();
    await settle();
    assert.equal(world.asked.length, 8);
    assert.equal(boards.getBoards().allTime[0].seconds, 180);

    closeOne();
    closeOne();
    assert.equal(timers[0].cleared, false, 'the second is still open');
    closeTwo();
    assert.equal(timers[0].cleared, true);
    globalThis.setInterval = realSetInterval;
    globalThis.clearInterval = realClearInterval;

    stop();
    uninstall();
});

await check('a board left open in a hidden tab is read as the tab is looked at again, if the copy is a minute old', async () => {
    const realDocument = globalThis.document;
    const realSetInterval = globalThis.setInterval;
    const realClearInterval = globalThis.clearInterval;
    const seen = new Set();
    globalThis.document = {
        visibilityState: 'visible',
        addEventListener: (name, listener) => name === 'visibilitychange' && seen.add(listener),
        removeEventListener: (name, listener) => name === 'visibilitychange' && seen.delete(listener),
    };
    globalThis.setInterval = () => 1;
    globalThis.clearInterval = () => {};
    const lookedAt = () => Array.from(seen).forEach((listener) => listener());
    try {
        pushSnapshot('');
        world.answers.push({value: boardsOf('v1'), tag: '"t1"'});
        const uninstall = boards.installBoards();
        await settle();
        world.asked.length = 0;

        const closeOne = boards.watchBoards();
        const closeTwo = boards.watchBoards();
        assert.equal(seen.size, 1, 'one listener for any number of open boards');
        assert.deepEqual(world.asked, [], 'the copy is fresh');

        // Hidden for a minute and more: nothing read, as the minute's read skips a hidden page.
        globalThis.document.visibilityState = 'hidden';
        lookedAt();
        world.now += 60000;
        lookedAt();
        assert.deepEqual(world.asked, []);

        // Looked at again: read, the copy being a minute old.
        globalThis.document.visibilityState = 'visible';
        world.answers.push(null);
        lookedAt();
        await settle();
        assert.deepEqual(world.asked, ['/boards "t1"']);

        // Looked at again within the minute: nothing.
        world.now += 30000;
        lookedAt();
        await settle();
        assert.equal(world.asked.length, 1);

        closeOne();
        assert.equal(seen.size, 1);
        closeTwo();
        assert.equal(seen.size, 0, 'and gone with the last open board');
        uninstall();
    } finally {
        globalThis.document = realDocument;
        globalThis.setInterval = realSetInterval;
        globalThis.clearInterval = realClearInterval;
    }
});

await check('a component reading the boards misses no change made between its first render and its subscription', () => {
    const hook = readFileSync(new URL('../webapp/src/useBoards.ts', import.meta.url), 'utf8');
    const useBoards = hook.slice(hook.indexOf('export function useBoards('), hook.indexOf('export function useBoardsLookedAt('));
    assert.match(useBoards, /rendered\.current = current;/);
    assert.match(useBoards, /\/\/ Anything that changed between the render and now\.\n\s+listener\(\);/);
});

Math.random = realRandom;

// ---- The reader's own awards, kept for the You tab (awardsMine.ts) ----

const own = await load('ownAnswers.ts', {});
const awardsMine = await load('awardsMine.ts', {
    './client': client + 'export const fetchMyAwards = () => world.next(\'/awards/mine\');',
    './ownAnswers': transpiled('ownAnswers.ts'),
});

await check('the reader’s own awards are kept for them and for the boards they were asked under, shared while on their way, and gone at sign-out', async () => {
    world.asked.length = 0;
    const mine = (show) => ({held: [{id: 'clipper', kind: 'clipper', label: 'Best Clipper', short: 'Best Clipper'}], show, readable: true});
    assert.equal(awardsMine.keptAwardsMine('u-dave', 'v1'), null);

    // The card opening asks once; the You tab chosen meanwhile shares that read.
    let answer;
    world.next = (path) => {
        world.asked.push(path);

        return new Promise((resolve) => {
            answer = resolve;
        });
    };
    awardsMine.readyAwardsMine('u-dave', 'v1');
    awardsMine.readyAwardsMine('u-dave', 'v1');
    const tab = awardsMine.askAwardsMine('u-dave', 'v1');
    world.next = next;
    assert.deepEqual(world.asked, ['/awards/mine']);
    answer(mine(null));
    assert.deepEqual(await tab, mine(null));
    assert.deepEqual(awardsMine.keptAwardsMine('u-dave', 'v1'), mine(null));

    // Kept: the next opening asks nothing first, and asks again when the tab opens.
    awardsMine.readyAwardsMine('u-dave', 'v1');
    assert.deepEqual(world.asked, ['/awards/mine']);
    world.answers.push(mine(['clipper']));
    await awardsMine.askAwardsMine('u-dave', 'v1');
    assert.deepEqual(awardsMine.keptAwardsMine('u-dave', 'v1').show, ['clipper']);

    // A read that fails keeps what was kept.
    world.answers.push(new Error('offline'));
    await assert.rejects(awardsMine.askAwardsMine('u-dave', 'v1'));
    assert.deepEqual(awardsMine.keptAwardsMine('u-dave', 'v1').show, ['clipper']);

    // Good only for the boards it was asked under: once their version has moved (a place won or
    // lost), what was kept is not drawn, and the card opening asks again.
    assert.equal(awardsMine.keptAwardsMine('u-dave', 'v2'), null);
    assert.deepEqual(awardsMine.keptAwardsMine('u-dave', 'v1').show, ['clipper'], 'still there for the version it is of');
    world.answers.push(mine(['clipper']));
    awardsMine.readyAwardsMine('u-dave', 'v2');
    await settle();
    assert.equal(world.asked.length, 4, 'asked again for the new version');
    assert.deepEqual(awardsMine.keptAwardsMine('u-dave', 'v2').show, ['clipper']);
    assert.equal(awardsMine.keptAwardsMine('u-dave', 'v1'), null);

    // Gone at sign-out, with an answer still on its way: it does not bring it back.
    world.next = (path) => {
        world.asked.push(path);

        return new Promise((resolve) => {
            answer = resolve;
        });
    };
    const late = awardsMine.askAwardsMine('u-dave', 'v2', true);
    world.next = next;
    own.forgetOwnAnswers();
    assert.equal(awardsMine.keptAwardsMine('u-dave', 'v2'), null, 'gone with the sign-in it was asked in');
    answer(mine(null));
    assert.equal(await late, null);
    assert.equal(awardsMine.keptAwardsMine('u-dave', 'v2'), null);

    // Never anybody else's.
    world.answers.push(mine(['clipper']));
    await awardsMine.askAwardsMine('u-dave', 'v2');
    assert.deepEqual(awardsMine.keptAwardsMine('u-dave', 'v2').show, ['clipper']);
    assert.equal(awardsMine.keptAwardsMine('u-bob', 'v2'), null);
});

// ---- The requests themselves: client.ts as written, with fetch played from here ----

const sent = [];
const replies = [];
const realFetch = globalThis.fetch;
globalThis.fetch = (url, init = {}) => {
    sent.push({url, method: init.method || 'GET', headers: {...init.headers}, credentials: init.credentials});
    const reply = replies.shift();
    assert.ok(reply, `no reply in line for ${url}`);

    return Promise.resolve(reply);
};
const reply = (status, body, headers = {}) => {
    const response = new Response(body === null ? null : JSON.stringify(body), {status, headers});
    const text = response.text.bind(response);
    response.readToItsEnd = false;
    response.text = () => {
        response.readToItsEnd = true;

        return text();
    };

    return response;
};

const real = await load('client.ts', {'./manifest': transpiled('manifest.ts'), './updateAdmin': transpiled('updateAdmin.ts')});

await check('a re-read carries the tag in hand, and "that one still" is nothing to take', async () => {
    // Nothing in hand: no condition, and the answer comes with the tag to keep.
    replies.push(reply(200, snapshotAt(100), {ETag: '"aaaa"'}));
    const first = await real.fetchStreamsIfChanged('');
    assert.equal(sent.at(-1).url, `${PLUGIN_BASE}/api/v1/streams`);
    assert.equal(sent.at(-1).credentials, 'same-origin');
    assert.equal(sent.at(-1).headers['X-Requested-With'], 'XMLHttpRequest');
    assert.ok(!('If-None-Match' in sent.at(-1).headers));
    assert.deepEqual(first, {value: snapshotAt(100), tag: '"aaaa"'});

    // That tag, sent back as it came: 304, and nothing to take.
    const still = reply(304, null);
    replies.push(still);
    assert.equal(await real.fetchStreamsIfChanged('"aaaa"'), null);
    assert.equal(sent.at(-1).headers['If-None-Match'], '"aaaa"');

    // Read to its end though there is nothing in it: a browser logs an answer nobody read as a
    // request that failed, and this is the commonest answer these reads get.
    assert.equal(still.readToItsEnd, true);

    // The accounts are read the same way, and the boards.
    replies.push(reply(304, null));
    assert.equal(await real.fetchAccountsIfChanged('"bbbb"'), null);
    assert.equal(sent.at(-1).url, `${PLUGIN_BASE}/api/v1/accounts`);
    assert.equal(sent.at(-1).headers['If-None-Match'], '"bbbb"');
    replies.push(reply(304, null));
    assert.equal(await real.fetchBoardsIfChanged('"cccc"'), null);
    assert.equal(sent.at(-1).url, `${PLUGIN_BASE}/api/v1/boards`);
    assert.equal(sent.at(-1).headers['If-None-Match'], '"cccc"');

    // An answer with no tag is kept with none, so the next read asks for it whole.
    replies.push(reply(200, {people: {}}));
    assert.deepEqual(await real.fetchAccountsIfChanged('"bbbb"'), {value: {people: {}}, tag: ''});
});

await check('a refused re-read is a refusal like any other, and tells the page when it is about access', async () => {
    const heard = [];
    real.setAccessRefusalListener((code) => heard.push(code));

    replies.push(reply(403, {error: 'Link your stream to see who is playing what.', code: 'locked'}));
    await assert.rejects(real.fetchAccountsIfChanged('"bbbb"'), (error) => {
        assert.equal(error.name, 'RequestError');
        assert.equal(error.status, 403);
        assert.equal(error.code, 'locked');
        assert.equal(error.message, 'Link your stream to see who is playing what.', 'the server\'s sentence is the wording');

        return true;
    });
    assert.deepEqual(heard, ['locked']);

    // Too many requests is not about access: nobody re-reads who they are for it.
    replies.push(reply(429, {error: 'Too many requests. Try again in a moment.', code: 'rate_limited', retryAfterMs: 2000}));
    await assert.rejects(real.fetchStreamsIfChanged(''), (error) => error.code === 'rate_limited' && error.status === 429);
    assert.deepEqual(heard, ['locked']);
    real.setAccessRefusalListener(null);

    // And the small question is a plain read of its own route.
    replies.push(reply(200, {access: 'linked', configured: true, contract: 3, version: '3.6.0'}));
    assert.deepEqual(await real.fetchAccess(), {access: 'linked', configured: true, contract: 3, version: '3.6.0'});
    assert.equal(sent.at(-1).url, `${PLUGIN_BASE}/api/v1/access`);
    assert.equal(sent.at(-1).method, 'GET');
});

globalThis.fetch = realFetch;

console.log(`check-reads: ${checks} checks passed`);
