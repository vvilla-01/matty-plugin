// The rules for Twitch in the panel (design/twitch-live.md 6 and 11.2),
// checked without a browser: whether a page may run Twitch's player, how big
// it may be, the one way its address is made, which of a person's streams
// stands for them in the lists that show one card per person, and when the
// Disconnect Twitch line of somebody not linked is in the page, as Twitch's
// own key and as Twitch's part of 3.4.0's Disconnect line (AccountsKey.tsx).
//
// webapp/src/twitchLogic.ts imports nothing, so it is transpiled with the
// project's own TypeScript, imported from a data URL and run here, as
// check-away.mjs does with awayLogic.ts. Run from the project root (make
// check-webapp).

import assert from 'node:assert/strict';
import {readFileSync, readdirSync} from 'node:fs';
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

const twitch = await load('twitchLogic.ts');

let checks = 0;
function check(name, run) {
    try {
        run();
        checks++;
    } catch (error) {
        console.error(`check-twitch: ${name}`);
        throw error;
    }
}

const at = (protocol, hostname, port = '') => ({protocol, hostname, port});

// ---- Can this page embed? ----

check('embedHostFor: an https page on the standard port under a name embeds, as that name in lower case', () => {
    assert.deepEqual(twitch.embedHostFor(at('https:', 'chat.example.com')), {ok: true, host: 'chat.example.com'});
    assert.deepEqual(twitch.embedHostFor(at('https:', 'chat.example.com', '443')), {ok: true, host: 'chat.example.com'});
    assert.deepEqual(twitch.embedHostFor(at('https:', 'Chat.Example.COM')), {ok: true, host: 'chat.example.com'});
});

check('embedHostFor: a custom port, plain http and an IP address are refused, each with its reason', () => {
    assert.deepEqual(twitch.embedHostFor(at('https:', 'chat.example.com', '8065')), {ok: false, reason: twitch.EMBED_PORT_443});
    assert.deepEqual(twitch.embedHostFor(at('http:', 'chat.example.com')), {ok: false, reason: twitch.EMBED_HTTPS_ONLY});
    assert.deepEqual(twitch.embedHostFor(at('https:', '192.168.1.20')), {ok: false, reason: twitch.EMBED_NO_IP});
    assert.deepEqual(twitch.embedHostFor(at('https:', '[2001:db8::1]')), {ok: false, reason: twitch.EMBED_NO_IP});
    assert.deepEqual(twitch.embedHostFor(at('https:', '2001:db8::1')), {ok: false, reason: twitch.EMBED_NO_IP});
    assert.deepEqual(twitch.embedHostFor(at('https:', '[::1]')), {ok: false, reason: twitch.EMBED_NO_IP});
});

check('embedHostFor: localhost on any port and scheme, and 127.0.0.1, are Twitch’s exceptions', () => {
    assert.deepEqual(twitch.embedHostFor(at('http:', 'localhost', '8065')), {ok: true, host: 'localhost'});
    assert.deepEqual(twitch.embedHostFor(at('https:', 'localhost')), {ok: true, host: 'localhost'});
    assert.deepEqual(twitch.embedHostFor(at('http:', '127.0.0.1', '8079')), {ok: true, host: '127.0.0.1'});
});

// ---- How big ----

check('inlineSize: a small tile goes inline only from 408, and an inline one leaves only below 400', () => {
    for (const wasInline of [false, true]) {
        assert.equal(twitch.inlineSize(399, wasInline), null, `399 from ${wasInline ? 'inline' : 'small'}`);
    }
    assert.equal(twitch.inlineSize(400, false), null, 'entering needs 408');
    assert.equal(twitch.inlineSize(407, false), null, 'entering needs 408');
    assert.deepEqual(twitch.inlineSize(408, false), {w: 408, h: 300});
    assert.deepEqual(twitch.inlineSize(400, true), {w: 400, h: 300}, 'staying holds down to 400');
    assert.deepEqual(twitch.inlineSize(407, true), {w: 407, h: 300});
});

check('inlineSize: 16:9 once that is taller than 300, never below 300 tall, whole pixels', () => {
    for (const wasInline of [false, true]) {
        assert.deepEqual(twitch.inlineSize(640, wasInline), {w: 640, h: 360});
        assert.deepEqual(twitch.inlineSize(1000, wasInline), {w: 1000, h: 563});
        assert.deepEqual(twitch.inlineSize(436.7, wasInline), {w: 436, h: 300});
    }
});

check('inlineSize: dragging the stage from 440 down to 396 and back, in 2 px steps, switches once each way', () => {
    let size = twitch.inlineSize(440, false);
    let flips = 0;
    const walk = (from, to, step) => {
        for (let width = from; step < 0 ? width >= to : width <= to; width += step) {
            const next = twitch.inlineSize(width, size !== null);
            if ((next === null) !== (size === null)) {
                flips++;
            }
            size = next;
        }
    };
    walk(440, 396, -2);
    assert.equal(size, null, 'small at 396');
    walk(396, 440, 2);
    assert.notEqual(size, null, 'inline again at 440');
    assert.equal(flips, 2, 'one reload each way');
});

check('the panel’s body keeps its scrollbar’s room, so a tile’s stage never changes width as the body starts or stops scrolling', () => {
    const styles = readFileSync(new URL('../webapp/src/styles.ts', import.meta.url), 'utf8');
    const body = styles.match(/^\.vntv-body \{([^}]*)\}/m);
    assert.ok(body, 'the .vntv-body rule');
    assert.match(body[1], /scrollbar-gutter: stable;/);

    // What that prevents: a body at the edge of overflowing, which the taller
    // inline player tips over and the shorter plate does not, under Firefox's
    // 12 px classic scrollbar. A stage that loses the scrollbar's width only
    // while the player shows flips the tile every frame, which the 8 px of
    // hysteresis cannot stop; a stage that always has it decides once.
    const switches = (stage) => {
        let inline = false;
        let count = 0;
        for (let frame = 0; frame < 20; frame++) {
            const next = twitch.inlineSize(stage(inline), inline) !== null;
            if (next !== inline) {
                count++;
            }
            inline = next;
        }

        return count;
    };
    const scrollbar = 12;
    for (const room of [404, 410, 416, 420, 424]) {
        assert.ok(switches(() => room - scrollbar) <= 1, `${room} px with the room kept`);
    }
    assert.equal(switches((inline) => 410 - (inline ? scrollbar : 0)), 20, 'with the room coming and going, every frame');
});

check('theatreSize: none on a phone, room for the header on a laptop, and never past 1280 wide', () => {
    assert.equal(twitch.theatreSize(375, 812), null, 'a phone');
    assert.deepEqual(twitch.theatreSize(1280, 720), {w: 1248, h: 600});
    assert.deepEqual(twitch.theatreSize(900, 500), {w: 868, h: 380});
    assert.deepEqual(twitch.theatreSize(2560, 1440), {w: 1280, h: 720});
    assert.equal(twitch.theatreSize(1280, 410), null, 'too short for 300 under the header');
});

check('fullRowStage: the row less the tile’s borders, of which it has none now, never below nothing', () => {
    assert.equal(twitch.TWITCH_TILE_BORDER, 0, 'the tile has no frame of its own');
    assert.equal(twitch.fullRowStage(440), 440);
    assert.equal(twitch.fullRowStage(1), 1);
    assert.equal(twitch.fullRowStage(-1), 0);
});

check('tileMode: off, blocked, theatre, small and inline, in that order', () => {
    const all = {embedOn: true, hostOk: true, loginOk: true, theatre: false, fits: true};
    assert.equal(twitch.tileMode(all), 'inline');
    assert.equal(twitch.tileMode({...all, fits: false}), 'small');
    assert.equal(twitch.tileMode({...all, theatre: true}), 'theatre');
    assert.equal(twitch.tileMode({...all, theatre: true, hostOk: false}), 'blocked');
    assert.equal(twitch.tileMode({...all, loginOk: false}), 'blocked');
    assert.equal(twitch.tileMode({...all, embedOn: false, hostOk: false}), 'off');
});

// ---- The address ----

check('validLogin and channelURL: only Twitch’s own shape of login makes a link', () => {
    assert.equal(twitch.validLogin('erin_streams'), true);
    assert.equal(twitch.validLogin('a'.repeat(25)), true);
    for (const bad of ['', 'a b', 'A"', 'Erin', 'a'.repeat(26), 'erin/../x', 'erin?x=1']) {
        assert.equal(twitch.validLogin(bad), false, JSON.stringify(bad));
        assert.equal(twitch.channelURL(bad), '', JSON.stringify(bad));
    }
    assert.equal(twitch.channelURL('erin_streams'), 'https://www.twitch.tv/erin_streams');
});

check('frameURL: the player prefix, exactly the four parameters in order, the host encoded', () => {
    const url = twitch.frameURL('erin_streams', 'chat.example.com', true);
    assert.ok(url.startsWith(twitch.TWITCH_PLAYER_PREFIX));
    assert.equal(url, 'https://player.twitch.tv/?channel=erin_streams&parent=chat.example.com&muted=true&autoplay=true');
    assert.equal(twitch.frameURL('erin_streams', 'chat.example.com', false),
        'https://player.twitch.tv/?channel=erin_streams&parent=chat.example.com&muted=false&autoplay=true');

    const odd = twitch.frameURL('erin', 'a b&c=d', false);
    assert.ok(odd.startsWith(twitch.TWITCH_PLAYER_PREFIX));
    const query = new URLSearchParams(odd.slice(twitch.TWITCH_PLAYER_PREFIX.length));
    assert.deepEqual(Array.from(query.keys()), ['channel', 'parent', 'muted', 'autoplay']);
    assert.equal(query.get('parent'), 'a b&c=d', 'the host survives as one parameter');
});

check('frameURL: nothing at all for a bad login or no host', () => {
    for (const bad of ['a b', 'A"', 'a'.repeat(26), '']) {
        assert.equal(twitch.frameURL(bad, 'chat.example.com', true), null, JSON.stringify(bad));
    }
    assert.equal(twitch.frameURL('erin', '', true), null);
});

// ---- One card per person ----

const bb = (handle, userId, more = {}) => ({handle, userId, paused: false, alwaysOn: false, ...more});
const tw = (handle, userId, more = {}) => bb(handle, userId, {source: 'twitch', ...more});

check('liveCards: a covered Twitch entry is left out, everything else stays in order', () => {
    const streams = [bb('v-bb', 'u-victor', {alsoTwitch: 'victor'}), tw('v-tw', 'u-victor', {coveredBy: 'v-bb'}), tw('n-tw', 'u-erin')];
    assert.deepEqual(twitch.liveCards(streams).map((stream) => stream.handle), ['v-bb', 'n-tw']);
});

// The rule itself is the server's now (server/people.go liveHandles, and its
// test with these same people in server/people_test.go); the panel only reads
// the answer out of the snapshot's index.
check('liveHandlesOf: reads who is live out of the people index, and nobody else', () => {
    assert.deepEqual(twitch.liveHandlesOf({
        'u-victor': {live: 'v-bb', voice: 'open'},
        'u-erin': {live: 'n-tw', badge: 'live'},
        'u-dave': {voice: 'muted'},
        'u-dan': {badge: 'birthday'},
    }), {'u-victor': 'v-bb', 'u-erin': 'n-tw'});
    assert.deepEqual(twitch.liveHandlesOf(undefined), {});
});

check('popoverStream: Broadcast Box on the air, else Twitch, else Broadcast Box paused; never covered or 24/7', () => {
    const both = [tw('v-tw', 'u-victor', {coveredBy: 'v-bb'}), bb('v-bb', 'u-victor')];
    assert.equal(twitch.popoverStream(both, 'u-victor').handle, 'v-bb');
    const pausedHere = [bb('v-bb', 'u-victor', {paused: true}), tw('v-tw', 'u-victor')];
    assert.equal(twitch.popoverStream(pausedHere, 'u-victor').handle, 'v-tw', 'live on Twitch is live');
    assert.equal(twitch.popoverStream([bb('v-bb', 'u-victor', {paused: true})], 'u-victor').handle, 'v-bb');
    assert.equal(twitch.popoverStream([bb('w', 'u-victor', {alwaysOn: true})], 'u-victor'), undefined);
    assert.equal(twitch.popoverStream([tw('v-tw', 'u-victor', {coveredBy: 'x'})], 'u-victor'), undefined);
});

check('the audience is always said to be Twitch’s, even at nothing', () => {
    assert.equal(twitch.onTwitchWords(0), '0 on Twitch');
    assert.equal(twitch.onTwitchWords(-3), '0 on Twitch');
    assert.equal(twitch.watchingOnTwitchWords(1), '1 watching on Twitch');
    assert.ok(twitch.onTwitchWords(1234).endsWith(' on Twitch'));
});

// ---- Twitch's Glitch: the Twitch row's mark ----
//
// Victor's choice of 2026-10-06 (design/twitch-live.md, the amendment at the
// top), for the footer's Twitch tool; since his P1 pick of 2026-11-12
// (design/3.4.0.md) the tool has left the bar and the Glitch is the mark of
// the Twitch row in Connected accounts, beside the word TWITCH. Twitch's
// brand rules let its mark be scaled but never altered, so the components
// are read as TypeScript sees them and the icon held to Simple Icons
// 16.34.0's path, character for character: one path and nothing else, filled
// in one colour, square, with no stroke, turn or fill rule of its own.

// Simple Icons 16.34.0, icons/twitch.svg (CC0; source brand.twitch.tv).
const GLITCH = 'M11.571 4.714h1.715v5.143H11.57zm4.715 0H18v5.143h-1.714zM6 0L1.714 4.286v15.428h5.143V24l4.286-4.286h3.428L22.286 12V0zm14.571 11.143l-3.428 3.428h-3.429l-3 3v-3H6.857V1.714h13.714Z';

function component(file, name) {
    const source = readFileSync(new URL(`../webapp/src/${file}`, import.meta.url), 'utf8');
    const tree = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
    let found = null;
    const visit = (node) => {
        if (found === null && ts.isVariableDeclaration(node) && node.name.getText(tree) === name) {
            found = node;
        }
        if (found === null) {
            ts.forEachChild(node, visit);
        }
    };
    visit(tree);
    assert.ok(found, `${name} in ${file}`);

    return {tree, node: found};
}

// Every JSX element in a component, in order: its tag, its attributes as
// written (a string for a string, the source text otherwise), and the
// strings among its children.
function elementsOf({tree, node}) {
    const elements = [];
    const visit = (child) => {
        const opening = ts.isJsxElement(child) ? child.openingElement : child;
        if (ts.isJsxSelfClosingElement(opening) || ts.isJsxOpeningElement(opening)) {
            if (ts.isJsxSelfClosingElement(child) || ts.isJsxElement(child)) {
                const attributes = {};
                for (const attribute of opening.attributes.properties) {
                    if (!ts.isJsxAttribute(attribute)) {
                        attributes['{...}'] = attribute.getText(tree);
                        continue;
                    }
                    const value = attribute.initializer;
                    let written = true;
                    if (value && ts.isStringLiteral(value)) {
                        written = value.text;
                    } else if (value && ts.isJsxExpression(value) && value.expression) {
                        written = ts.isStringLiteralLike(value.expression) ? value.expression.text : value.expression.getText(tree);
                    }
                    attributes[attribute.name.getText(tree)] = written;
                }
                const words = ts.isJsxElement(child) ? child.children.flatMap((inner) => {
                    if (ts.isJsxText(inner)) {
                        return inner.text.trim() === '' ? [] : [inner.text.trim()];
                    }
                    if (ts.isJsxExpression(inner) && inner.expression && ts.isStringLiteralLike(inner.expression)) {
                        return [inner.expression.text];
                    }

                    return [];
                }) : [];
                elements.push({tag: opening.tagName.getText(tree), attributes, words});
            }
        }
        ts.forEachChild(child, visit);
    };
    visit(node);

    return elements;
}

check('TwitchIcon is Twitch’s Glitch unaltered: Simple Icons’ one path, filled in one colour, scaled only as a whole', () => {
    const elements = elementsOf(component('components/icons.tsx', 'TwitchIcon'));
    assert.deepEqual(elements.map((element) => element.tag), ['svg', 'path'], 'one svg and one path: no shape added');

    const [svg, path] = elements;
    assert.deepEqual(path.attributes, {d: GLITCH}, 'the path is the Glitch, character for character, with nothing of its own');
    assert.deepEqual(svg.attributes, {
        width: 'size',
        height: 'size',
        viewBox: '0 0 24 24',
        fill: 'currentColor',
        'aria-hidden': 'true',
    }, 'square, in the bar’s colour, with no stroke, transform, fill rule or style');
});

check('the Twitch row’s mark is TwitchIcon, drawn whole, with nothing beside it in the mark', () => {
    const mark = component('components/AccountSheet.tsx', 'ServiceMark');
    const elements = elementsOf(mark);
    const glitches = elements.filter((element) => element.tag === 'TwitchIcon');
    assert.equal(glitches.length, 1, 'one TwitchIcon');
    assert.deepEqual(Object.keys(glitches[0].attributes), ['size'], 'its size is the one thing given to it');
    assert.match(glitches[0].attributes.size, /^\d+$/, 'a whole number of pixels, the same both ways');
    assert.deepEqual(elements.flatMap((element) => element.words), [], 'no words inside the mark: the word TWITCH is the row’s own');
    assert.match(mark.node.getText(mark.tree), /service === 'twitch' \? <TwitchIcon size=\{\d+\}\/> : null/, 'the Glitch is Twitch’s mark, and only Twitch’s');

    // The stylesheet only places it: no rule turns, stretches, outlines or
    // fills anything inside the mark.
    const styles = readFileSync(new URL('../webapp/src/styles.ts', import.meta.url), 'utf8');
    const rules = Array.from(styles.matchAll(/^([^{}\n]*\.vntv-account-mark[^{}\n]*) \{([^}]*)\}/gm));
    assert.ok(rules.length > 0, 'the mark’s rules');
    for (const [, selector, body] of rules) {
        assert.doesNotMatch(body, /\b(?:transform|rotate|scale|stroke|fill|filter|clip-path|mask)\b/, selector);
    }
});

check('the footer’s bar has no Twitch tool any more, and the Glitch shows nowhere but the Twitch row', () => {
    const connect = readFileSync(new URL('../webapp/src/components/TwitchConnect.tsx', import.meta.url), 'utf8');
    assert.doesNotMatch(connect, /export const TwitchTool\b/);
    const sidebar = readFileSync(new URL('../webapp/src/components/Sidebar.tsx', import.meta.url), 'utf8');
    assert.doesNotMatch(sidebar, /TwitchTool|vntv-twitch-connect/);
    const components = new URL('../webapp/src/components/', import.meta.url);
    const drawing = readdirSync(components).filter((file) => /\.tsx$/.test(file) && readFileSync(new URL(file, components), 'utf8').includes('<TwitchIcon'));
    assert.deepEqual(drawing, ['AccountSheet.tsx']);
    const styles = readFileSync(new URL('../webapp/src/styles.ts', import.meta.url), 'utf8');
    assert.doesNotMatch(styles, /vntv-twitch-connect/, 'the tool’s rules went with it');
});

// ---- Disconnect Twitch for somebody not linked: where focus can go ----

check('disconnectLineShown: the words stay in the page from the press until their time is up, whatever "me" says meanwhile', () => {
    const shown = (connected, busy, saying) => twitch.disconnectLineShown({connected, busy, saying});

    assert.equal(shown(false, false, false), false, 'nothing connected and nothing pressed: no line');
    assert.equal(shown(true, false, false), true, 'connected: the key');

    // "Me" says the account is gone before the answer to the press comes, as
    // the server's socket event makes it: the key goes, and the words must be
    // there for focus to go to, empty until the answer fills them.
    assert.equal(shown(false, true, false), true, 'the account gone while the press is out');
    assert.equal(shown(false, false, true), true, 'the answer in, the words shown');
    assert.equal(shown(true, true, false), true, 'the answer first, the usual order');
    assert.equal(shown(false, false, false), false, 'the words lapsed: the line goes');

    // And the component goes by this rule, rather than one of its own.
    const source = readFileSync(new URL('../webapp/src/components/TwitchConnect.tsx', import.meta.url), 'utf8');
    const key = source.slice(source.indexOf('export const TwitchDisconnectKey'));
    assert.match(key, /if \(!disconnectLineShown\(\{connected: login !== '', busy, saying: said\.text !== ''\}\)\) \{\n\s+return null;/);
    assert.match(key, /tabIndex=\{-1\}/, 'the words can take focus');
    assert.match(key, /closest<HTMLElement>\('\.vntv-panel'\)/, 'and hand it to the lock or the panel when they go');
});

// ---- The same, for the key people see from 3.4.0 (design/3.4.0.md 5.5, 8.2) ----
// AccountsDisconnectKey stands where TwitchDisconnectKey stood, on the lock
// and over WATCH ONLY, so these are the checks above held to it too.

const accounts = await load('accountsLogic.ts');
const accountsKey = (() => {
    const source = readFileSync(new URL('../webapp/src/components/AccountsKey.tsx', import.meta.url), 'utf8');

    return {source, key: source.slice(source.indexOf('export const AccountsDisconnectKey'))};
})();

check('AccountsDisconnectKey keeps its words in the page by the same rule, and focus on them never falls to the body', () => {
    const {key} = accountsKey;
    assert.match(key, /if \(!accountsLineShown\(\{connected: connections\.length, busy: busy !== '', saying\}\)\) \{\n\s+return null;/);
    assert.match(key, /tabIndex=\{-1\}/, 'the words can take focus');
    assert.match(key, /closest<HTMLElement>\('\.vntv-panel'\)/, 'and hand it to the lock or the panel when they go');

    // The rule is twitchLogic.ts's, for the set of services.
    const shown = (connected, busy, saying) => accounts.accountsLineShown({connected, busy, saying});
    assert.equal(shown(0, false, false), false);
    assert.equal(shown(0, true, false), true, 'the account gone while the press is out');
    assert.equal(shown(0, false, true), true, 'the answer in, the words shown');

    // Where focus is, read as each render begins, whatever is changing the
    // line: its own press, or a Disconnect somewhere else, which takes a key
    // away with no blur, or turns the line into Twitch's own key.
    assert.match(key, /const activeNow = document\.activeElement;\n\s+const rootNow = rootRef\.current;\n\s+focusBefore\.current = activeNow && activeNow !== document\.body && rootNow\?\.contains\(activeNow\)/);

    // After the change, focus that was in the line and fell to the body goes
    // to the words while the line's own press is out or saying, else to the
    // key that took the gone one's place; and with the line gone, to
    // Twitch's own key, found in the place both are drawn in, or the panel.
    const after = key.slice(key.indexOf('React.useLayoutEffect(() => {\n        const root = rootRef.current;'));
    assert.ok(after.length > 0, 'the rule runs after every change, not only after a press');
    assert.match(after, /if \(!before\.inLine \|\| \(active && active !== document\.body\)\) \{\n\s+return;/);
    assert.match(after, /const key = pressing \|\| keys\.length === 0 \? null : keys\[/);
    assert.match(after, /\(key \|\| statusRef\.current\)\?\.focus\(/);
    assert.match(after, /:scope > \.vntv-twitch-lock \.vntv-twitch-disconnect/);
    assert.match(after, /\(twitchKey \|\| place\?\.closest<HTMLElement>\('\.vntv-panel'\)\)\?\.focus\(/);
    assert.match(key, /const pressing = busy !== '' \|\| saying;/);
    assert.doesNotMatch(key, /hadFocus/, 'not only after a press of its own');
});

check('AccountsDisconnectKey is TwitchDisconnectKey for Twitch alone, and keeps Twitch’s words, its hours warning and its call otherwise', () => {
    const {source, key} = accountsKey;
    assert.match(key, /const asTwitchKey = busy === '' && !saying && \(twitchAlone \|\| \(connections\.length === 0 && twitchKeyStays\.current\)\);/);
    assert.match(key, /if \(asTwitchKey\) \{\n\s+return \(\n\s+<TwitchDisconnectKey\n\s+me=\{me\}\n\s+where=\{where\}/);
    assert.match(source, /service === 'twitch' \? unlinkTwitch\(\) : unlinkAccount\(service\)/);
    assert.match(source, /service === 'twitch' \? TWITCH_JUST_DISCONNECTED/);

    const both = accounts.connectionsOf({
        twitch: {available: true, login: 'victorplays', displayName: 'VictorPlays'},
        steam: {available: true, connected: true, name: 'VictorPlays'},
    });
    assert.deepEqual(accounts.disconnectLineOf(both.slice(0, 1), '', '', 'Matty'), {kind: 'twitch'});
    const armed = accounts.disconnectLineOf(both, 'twitch', '', 'Matty');
    assert.equal(armed.words, 'Disconnect Twitch? Press again', 'the same words as TwitchDisconnectKey');
    assert.equal(armed.title, 'Your Twitch account, VictorPlays, is connected to Matty. Disconnecting removes it and your Twitch hours.');
    assert.equal(accounts.disconnectLineOf(both, 'twitch', '', 'Movie Night Club').title,
        armed.title.replace(', is connected to Matty.', ', is connected to Movie Night Club.'));
    const resting = accounts.disconnectLineOf(both, '', '', 'Matty');
    assert.match(resting.keys.find((entry) => entry.service === 'twitch').title, /Disconnecting removes it and your Twitch hours\.$/);

    // And TwitchDisconnectKey itself says them exactly so.
    const connect = readFileSync(new URL('../webapp/src/components/TwitchConnect.tsx', import.meta.url), 'utf8');
    assert.ok(connect.includes("words = 'Disconnect Twitch? Press again';"));
    assert.ok(connect.includes('is connected to ${name}. Disconnecting removes it and your Twitch hours.`}'));
    assert.match(connect, /export const TwitchDisconnectKey[^]*?const name = useName\(\);/, 'with the server’s name for the plugin');
    // Twitch's Connections list the admin's own application, under the name
    // the admin gave it at Twitch: the words after a Disconnect name none.
    assert.ok(connect.includes('Removing this server’s Twitch application in Twitch’s Settings > Connections does not disconnect you here; this button does.'));
});

check('the lock and the WATCH ONLY line draw AccountsDisconnectKey, which draws TwitchDisconnectKey: nothing else does', () => {
    const lock = readFileSync(new URL('../webapp/src/components/LockScreen.tsx', import.meta.url), 'utf8');
    assert.equal((lock.match(/<AccountsDisconnectKey me=\{me\}\/>/g) || []).length, 2, 'under the docked bar, and under the not-set-up words');
    const sidebar = readFileSync(new URL('../webapp/src/components/Sidebar.tsx', import.meta.url), 'utf8');
    assert.match(sidebar, /<AccountsDisconnectKey\n\s+me=\{me\}\n\s+where='watchonly'/);
    const components = new URL('../webapp/src/components/', import.meta.url);
    const drawing = readdirSync(components).filter((file) => /\.tsx$/.test(file) && readFileSync(new URL(file, components), 'utf8').includes('<TwitchDisconnectKey'));
    assert.deepEqual(drawing, ['AccountsKey.tsx']);
});

console.log(`check-twitch: ${checks} checks passed`);
