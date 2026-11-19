// The rules for Steam and Hytale in the panel (design/3.4.0.md 5 and 8.2),
// checked without a browser: which rows of Connected accounts a reader gets
// for each "me", what each row says in each of Steam's states, the rows in a
// profile popover and the Playing now line, what the Disconnect line of
// somebody not linked says for every set of connections, the takeover
// notices' order, when Waiting gives its hints and gives up, and that a
// locked reader is shown nothing anywhere. And that the profile popover and
// the flow's Waiting are drawn from these very rules (popoverRowsFor,
// waitingAt) rather than copies of them, which the checks would not see.
//
// webapp/src/accountsLogic.ts imports nothing but types, so it is transpiled
// with the project's own TypeScript, imported from a data URL and run here,
// as check-twitch.mjs does with twitchLogic.ts. Run from the project root
// (node build/check-accounts.mjs; make check-webapp once it is listed there).

import assert from 'node:assert/strict';
import {readdirSync, readFileSync} from 'node:fs';
import {createRequire} from 'node:module';

const require = createRequire(import.meta.url);
const ts = require('../webapp/node_modules/typescript');

const SOURCE = readFileSync(new URL('../webapp/src/accountsLogic.ts', import.meta.url), 'utf8');

async function load() {
    const out = ts.transpileModule(SOURCE, {
        compilerOptions: {module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2020},
        fileName: 'accountsLogic.ts',
    });

    return import(`data:text/javascript;base64,${Buffer.from(out.outputText).toString('base64')}`);
}

const accounts = await load();

let checks = 0;
function check(name, run) {
    try {
        run();
        checks++;
    } catch (error) {
        console.error(`check-accounts: ${name}`);
        throw error;
    }
}

check('accountsLogic.ts imports nothing but types, so it can be run on its own', () => {
    const imports = SOURCE.match(/^import .*$/gm) || [];
    for (const line of imports) {
        assert.match(line, /^import type /, line);
    }
});

// ---- The words, exactly (5.8) ----

check('the consent heads, notes and buttons are the contract’s words', () => {
    assert.equal(accounts.CONSENT_HEAD.steam, 'Connect your Steam account?');
    assert.equal(accounts.CONSENT_HEAD.hytale, 'Connect your Hytale account?');
    assert.ok(accounts.consentNote('steam', 'Matty').startsWith('Everyone who can open Matty here sees your Steam name, whether Steam says you are online, busy or away'));
    assert.ok(accounts.consentNote('steam', 'Matty').includes(' Matty keeps your Steam ID and name, never a password,'));
    assert.ok(accounts.consentNote('steam', 'Matty').endsWith('Continuing opens Steam’s own sign-in page.'));
    assert.ok(accounts.consentNote('hytale', 'Matty').includes('Hytale tells Matty nothing else: not whether you are playing, nor where.'));
    assert.ok(accounts.consentNote('hytale', 'Matty').includes(' Matty keeps that username and its profile id,'));
    assert.ok(accounts.consentNote('hytale', 'Matty').endsWith('Continuing opens Hytale’s own sign-in page.'));
    // With the server's name for the plugin, whatever it is, in every place.
    assert.ok(accounts.consentNote('steam', 'Movie Night Club').startsWith('Everyone who can open Movie Night Club here sees your Steam name,'));
    assert.ok(accounts.consentNote('steam', 'Movie Night Club').includes(' Movie Night Club keeps your Steam ID and name,'));
    assert.ok(accounts.consentNote('hytale', 'Movie Night Club').includes('Hytale tells Movie Night Club nothing else'));
    assert.ok(accounts.consentNote('hytale', 'Movie Night Club').includes(' Movie Night Club keeps that username and its profile id,'));
    assert.equal(accounts.consentNote('hytale', 'Movie Night Club').includes('Matty'), false);
    assert.equal(accounts.CONTINUE_ON.steam, 'Continue on Steam', 'the panel’s plain button, not Valve’s (Q9)');
    assert.equal(accounts.CONTINUE_ON.hytale, 'Continue on Hytale');
    assert.equal(accounts.GETTING_READY, 'Getting ready…');
});

check('the relink key’s sheet: its heading, its head with account rows and without, and the key’s words', () => {
    assert.equal(accounts.LIST_HEADING, 'Connected accounts');
    assert.equal(accounts.SHEET_HEAD_WITH_ACCOUNTS, 'Your stream and accounts');
    assert.equal(accounts.SHEET_HEAD_WITHOUT, 'Relink your stream', 'today’s, exactly');
    assert.equal(accounts.relinkKeyWithAccounts('live-victor'), 'Linked as live-victor. Your stream and connected accounts.');
    for (const words of [accounts.LIST_HEADING, accounts.SHEET_HEAD_WITH_ACCOUNTS, accounts.relinkKeyWithAccounts('k')]) {
        assert.doesNotMatch(words.replace('Linked as', ''), /\blink/i, 'connecting is never called linking');
    }
});

check('a flow’s head, its arrow back, and Waiting’s words', () => {
    assert.equal(accounts.flowHeading('steam', false), 'Connect Steam');
    assert.equal(accounts.flowHeading('steam', true), 'Steam');
    assert.equal(accounts.flowHeading('hytale', false), 'Connect Hytale');
    assert.equal(accounts.flowHeading('twitch', false), 'Connect Twitch');
    assert.equal(accounts.BACK_TO_LIST, 'Back to connected accounts');
    assert.equal(accounts.waitingWords('steam'), 'Finish on Steam in the window that opened.');
    assert.equal(accounts.waitingWords('hytale'), 'Finish on Hytale in the window that opened.');
    assert.equal(accounts.noWindowWords('steam'), 'No window? Allow pop-ups for this site, then press Connect Steam again.');
    assert.equal(accounts.HYTALE_REDIRECT_HINT, 'If Hytale showed an error page instead, an admin needs to check the application’s Redirect URI and scopes.');
});

check('Disconnect: the two-press words, the words after, and the failure', () => {
    assert.equal(accounts.armedWords('steam'), 'Disconnect Steam? Press again');
    assert.equal(accounts.armedWords('hytale'), 'Disconnect Hytale? Press again');
    assert.equal(accounts.armedWords('twitch'), 'Disconnect Twitch? Press again', 'Twitch’s own');
    assert.equal(accounts.justDisconnected('steam', 'Matty'), 'Disconnected. Matty has deleted your Steam ID and name, and asks Steam nothing more about you.');
    assert.equal(accounts.justDisconnected('hytale', 'Matty'), 'Disconnected. Matty has deleted your Hytale username and profile id. It kept no Hytale token, so it holds nothing else.');
    assert.equal(accounts.justDisconnected('steam', 'Movie Night Club'), 'Disconnected. Movie Night Club has deleted your Steam ID and name, and asks Steam nothing more about you.');
    assert.equal(accounts.justDisconnected('hytale', 'Movie Night Club'), 'Disconnected. Movie Night Club has deleted your Hytale username and profile id. It kept no Hytale token, so it holds nothing else.');
    assert.equal(accounts.disconnectFailed('steam'), 'Could not disconnect Steam. Try again in a moment.');
    assert.equal(accounts.disconnectFailed('hytale'), 'Could not disconnect Hytale. Try again in a moment.');
    assert.equal(accounts.twitchDisconnectTitle('VictorPlays', 'Matty'), 'Your Twitch account, VictorPlays, is connected to Matty. Disconnecting removes it and your Twitch hours.');
    assert.equal(accounts.twitchDisconnectTitle('VictorPlays', 'Movie Night Club'), 'Your Twitch account, VictorPlays, is connected to Movie Night Club. Disconnecting removes it and your Twitch hours.');
});

// ---- Which rows a reader gets ----

const on = {available: true};
const twitchOn = {available: true};
const linked = (fields = {}) => ({access: 'linked', isAdmin: false, ...fields});

check('rows: none for a "me" that says nothing of any service, and none for anybody not linked', () => {
    assert.deepEqual(accounts.accountRowsFor(null, 'linked'), []);
    assert.deepEqual(accounts.accountRowsFor(linked(), 'linked'), [], 'every switch off: the sheet is today’s');
    const everything = linked({twitch: twitchOn, steam: on, hytale: on});
    assert.deepEqual(accounts.accountRowsFor(everything, 'letIn'), [], 'let in: watching only');
    assert.deepEqual(accounts.accountRowsFor(everything, 'locked'), []);
    assert.deepEqual(accounts.accountRowsFor(everything, 'unknown'), []);
});

check('rows: Twitch, Steam and Hytale, always in that order, each while it is available', () => {
    assert.deepEqual(accounts.accountRowsFor(linked({hytale: on, steam: on, twitch: twitchOn}), 'linked'), ['twitch', 'steam', 'hytale']);
    assert.deepEqual(accounts.accountRowsFor(linked({twitch: twitchOn}), 'linked'), ['twitch'], 'Twitch alone is a row too: the tool has left the bar');
    assert.deepEqual(accounts.accountRowsFor(linked({hytale: on}), 'linked'), ['hytale']);
});

check('rows: blocked hides a row, connected-while-off shows it, and not available shows only to an admin', () => {
    const blocked = linked({twitch: {available: true, blocked: true}, steam: {available: true, blocked: true}, hytale: on});
    assert.deepEqual(accounts.accountRowsFor(blocked, 'linked'), ['hytale']);

    const off = linked({twitch: {available: false, login: 'victorplays'}, steam: {available: false, connected: true, name: 'VictorPlays'}, hytale: {available: false, connected: true, name: 'VictorTheBuilder'}});
    assert.deepEqual(accounts.accountRowsFor(off, 'linked'), ['twitch', 'steam', 'hytale'], 'Disconnect is always reachable');

    const broken = {twitch: {available: false, problem: 'p'}, steam: {available: false, problem: 'p'}, hytale: {available: false}};
    assert.deepEqual(accounts.accountRowsFor(linked(broken), 'linked'), []);
    assert.deepEqual(accounts.accountRowsFor(linked({...broken, isAdmin: true}), 'linked'), ['twitch', 'steam', 'hytale'], 'an admin, to read why');
    assert.deepEqual(accounts.accountRowsFor(linked({isAdmin: true, steam: {available: false, blocked: true}}), 'linked'), [], 'blocked hides it from an admin too');
});

// ---- What a row says ----

const steamOf = (state, more = {}) => ({name: 'VictorPlays', url: 'https://steamcommunity.com/profiles/76561198000000001', state, ...more});
const steamMe = {available: true, connected: true, name: 'VictorPlays', url: 'https://steamcommunity.com/profiles/76561198000000001'};

check('the owner’s Steam row says what everybody else sees, in each of Steam’s states', () => {
    const says = (steam) => accounts.accountRowView('steam', steamMe, steam).sub;
    assert.equal(says(steamOf('online')), 'Online');
    assert.equal(says(steamOf('busy')), 'Busy');
    assert.equal(says(steamOf('away')), 'Away');
    assert.equal(says(steamOf('offline')), 'Offline');
    assert.equal(says(steamOf('online', {inGame: true, game: 'ELDEN RING'})), 'Playing ELDEN RING', 'the owner’s row says Playing');
    assert.equal(says(steamOf('online', {inGame: true, game: ''})), 'In a game');
    assert.equal(says(steamOf('')), '', 'not fresh: no status guessed');
    assert.equal(says(undefined), '', 'not in the view yet');
    const row = accounts.accountRowView('steam', steamMe, steamOf('online'));
    assert.deepEqual([row.connected, row.name, row.unavailable, row.title], [true, 'VictorPlays', false, 'Steam: connected as VictorPlays']);
});

check('a private profile says so on the owner’s row, whatever the view says', () => {
    const row = accounts.accountRowView('steam', {...steamMe, hidden: true}, steamOf('offline'));
    assert.equal(row.sub, 'Private: people here see you as offline');
    assert.equal(row.tone, 'private');
    assert.equal(accounts.PRIVATE_OWNER, 'Your Steam profile is private, so people here see you as offline.');
});

check('a row that is not connected prompts, and one that cannot connect says why, for an admin', () => {
    const steam = accounts.accountRowView('steam', {available: true});
    assert.deepEqual([steam.connected, steam.name, steam.sub], [false, '', 'Show your Steam status here']);
    assert.equal(steam.title, 'Steam: not connected. Connect your Steam account so people here see when you are playing.');
    assert.equal(accounts.accountRowView('hytale', {available: true}).sub, 'Show your Hytale name here');

    const broken = accounts.accountRowView('steam', {available: false, problem: 'Steam refused the Web API key. Check it in the Steam section.'});
    assert.equal(broken.sub, 'Connect Steam is not available: Steam refused the Web API key. Check it in the Steam section.');
    assert.deepEqual([broken.unavailable, broken.tone], [true, 'problem']);
    assert.equal(accounts.accountRowView('hytale', {available: false}).sub, 'Connect Hytale is not available: Hytale is not set up on this server yet.');
});

check('switched off while connected: the connected row and a muted line, Disconnect still there', () => {
    const steam = accounts.accountRowView('steam', {...steamMe, available: false, hidden: true}, steamOf('online'));
    assert.deepEqual([steam.connected, steam.sub, steam.tone, steam.unavailable], [true, '(Steam is switched off here)', 'muted', false]);
    const hytale = accounts.accountRowView('hytale', {available: false, connected: true, name: 'VictorTheBuilder'});
    assert.deepEqual([hytale.sub, hytale.unavailable], ['(Hytale is switched off here)', false]);
});

check('the owner’s Hytale row is the name alone: no status, and no Copy', () => {
    const row = accounts.accountRowView('hytale', {available: true, connected: true, name: 'VictorTheBuilder'});
    assert.deepEqual([row.name, row.sub], ['VictorTheBuilder', '']);
    assert.equal(row.title, 'Hytale: connected as VictorTheBuilder. Friends can copy it to find you in-game.');
});

check('the Twitch row: Live now in red while live, the prompt, the reason, and the tool’s tooltips', () => {
    const live = accounts.twitchRowView({available: true, login: 'victorplays', displayName: 'VictorPlays', live: true});
    assert.deepEqual([live.name, live.sub, live.tone], ['VictorPlays', 'Live now', 'live']);
    assert.equal(live.title, 'Twitch: connected as VictorPlays (twitch.tv/victorplays) · live now');
    const quiet = accounts.twitchRowView({available: true, login: 'victorplays', displayName: 'VictorPlays'});
    assert.deepEqual([quiet.sub, quiet.tone], ['', '']);
    assert.equal(accounts.twitchRowView({available: false, login: 'victorplays'}).sub, '(Twitch is switched off here)');
    const none = accounts.twitchRowView({available: true});
    assert.deepEqual([none.connected, none.sub], [false, 'Show when you are live on Twitch']);
    assert.ok(none.title.startsWith('Twitch: '));
    const broken = accounts.twitchRowView({available: false, problem: 'Fill in the Client ID.'});
    assert.deepEqual([broken.sub, broken.unavailable], ['Connect Twitch is not available: Fill in the Client ID.', true]);
});

// ---- Other people (5.4, with Victor's Q8 pick) ----

check('the popover’s Steam row: in a game the title is the game and the meta "On Steam · {name}"', () => {
    const playing = accounts.steamPopoverRow(steamOf('online', {inGame: true, game: 'ELDEN RING'}));
    assert.deepEqual([playing.title, playing.meta, playing.dim], ['ELDEN RING', 'On Steam · VictorPlays', false]);
    assert.equal(playing.full, 'Playing ELDEN RING on Steam · VictorPlays');

    const long = accounts.steamPopoverRow(steamOf('away', {inGame: true, game: 'Warhammer 40,000: Space Marine 2'}));
    assert.equal(long.title, 'Warhammer 40,000: Space Marine 2', 'whole, for the row to ellipsise, and whole in its title');
    assert.ok(long.full.includes('Warhammer 40,000: Space Marine 2'));

    const nameless = accounts.steamPopoverRow(steamOf('online', {inGame: true, game: ''}));
    assert.deepEqual([nameless.title, nameless.meta], ['In a game', 'On Steam · VictorPlays']);

    for (const row of [playing, long, nameless]) {
        assert.doesNotMatch(row.meta, /Playing on Steam/, 'not "Playing on Steam" (Q8 · 2)');
    }
});

check('the popover’s Steam row: online, busy, away; and offline, private or not fresh, the name dimmed', () => {
    for (const [state, word] of [['online', 'Online'], ['busy', 'Busy'], ['away', 'Away']]) {
        const row = accounts.steamPopoverRow(steamOf(state));
        assert.deepEqual([row.title, row.meta, row.dim], [word, 'On Steam · VictorPlays', false]);
    }
    for (const state of ['offline', '']) {
        const row = accounts.steamPopoverRow(steamOf(state));
        assert.deepEqual([row.title, row.meta, row.dim], ['VictorPlays', 'Steam', true], JSON.stringify(state));
    }
    assert.equal(accounts.steamProfileName('Dave'), 'Dave’s Steam profile');
});

check('the profile pill links only to a Steam profile page as the server builds it, or there is no pill', () => {
    assert.equal(accounts.steamProfileURL('https://steamcommunity.com/profiles/76561197960287930'), 'https://steamcommunity.com/profiles/76561197960287930');
    for (const url of [undefined, '', 'javascript:alert(1)', 'http://steamcommunity.com/profiles/76561197960287930',
        'https://steamcommunity.com/id/dave', 'https://steamcommunity.com.evil.example/profiles/76561197960287930',
        'https://steamcommunity.com/profiles/76561197960287930/../x', 'https://steamcommunity.com/profiles/123']) {
        assert.equal(accounts.steamProfileURL(url), '', String(url));
    }
});

check('the popover’s Hytale row: the username, "Hytale username", and Copy', () => {
    const row = accounts.hytalePopoverRow('Steve_Hytale');
    assert.deepEqual([row.title, row.meta, row.dim], ['Steve_Hytale', 'Hytale username', false]);
    assert.deepEqual([accounts.COPY, accounts.COPIED], ['Copy', 'Copied']);
});

const view = {
    v: '3f2a9c01b7de',
    steam: {on: true, fresh: true},
    hytale: {on: true},
    people: {
        'u-dave': {steam: {name: 'dave_racer', url: 'https://steamcommunity.com/profiles/1', state: 'online', inGame: true, game: 'ELDEN RING'}, hytale: {name: 'DaveBuilds'}},
        'u-bob': {steam: {name: 'tarnished', url: 'https://steamcommunity.com/profiles/2', state: 'online', inGame: true, game: ''}},
        'u-erin': {steam: {name: 'erin.pw', url: 'https://steamcommunity.com/profiles/3', state: 'away'}},
        'u-caro': {hytale: {name: 'CaroBuilds'}},
    },
};
const names = {'u-dave': 'Dave', 'u-bob': 'Bob', 'u-erin': 'Erin', 'u-caro': 'Caro'};
const nameOf = (userId) => names[userId] || '';

check('somebody’s popover rows: Steam then Hytale, for readers who may watch, and nothing for anybody else', () => {
    assert.deepEqual(accounts.popoverRowsFor(view, 'u-dave', 'letIn').map((row) => [row.service, row.title]), [['steam', 'ELDEN RING'], ['hytale', 'DaveBuilds']]);
    assert.deepEqual(accounts.popoverRowsFor(view, 'u-caro', 'linked').map((row) => row.service), ['hytale']);
    assert.deepEqual(accounts.popoverRowsFor(view, 'u-nobody', 'linked'), []);
    assert.deepEqual(accounts.popoverRowsFor(null, 'u-dave', 'linked'), []);
    for (const access of ['locked', 'unknown', '']) {
        assert.deepEqual(accounts.popoverRowsFor(view, 'u-dave', access), [], `${access || 'no access'}: nothing, whatever the view holds`);
    }
});

check('Playing now: only people in a game, by Mattermost name, with the contract’s words', () => {
    const items = accounts.playingNow(view, 'linked', nameOf);
    assert.deepEqual(items.map((item) => item.words), ['Bob · In a game', 'Dave · ELDEN RING'], 'Erin is online but not playing; Caro is Hytale only');
    assert.equal(items[1].title, 'Dave is playing ELDEN RING on Steam');
    assert.deepEqual(items.map((item) => [item.name, item.what]), [['Bob', 'In a game'], ['Dave', 'ELDEN RING']], 'the name, then what follows it');
    assert.equal(items[0].title, 'Bob is in a game on Steam');
    assert.equal(accounts.PLAYING_EYEBROW, 'Playing');
    assert.deepEqual(accounts.playingNow(view, 'linked', () => '').map((item) => item.name), ['Someone', 'Someone'], 'a name not known yet');
    assert.deepEqual(accounts.playingNow({...view, people: {}}, 'linked', nameOf), [], 'nobody playing: nothing, so the line is not drawn');
});

check('a locked reader is shown nothing anywhere: no rows, no popover rows, no Playing now', () => {
    assert.equal(accounts.accountsVisible('linked'), true);
    assert.equal(accounts.accountsVisible('letIn'), true);
    for (const access of ['locked', 'unknown', '']) {
        assert.equal(accounts.accountsVisible(access), false);
        assert.deepEqual(accounts.playingNow(view, access, nameOf), []);
        assert.deepEqual(accounts.popoverRowsFor(view, 'u-dave', access), []);
        assert.deepEqual(accounts.accountRowsFor(linked({steam: on, hytale: on, twitch: twitchOn}), access), []);
    }
});

check('the popover draws exactly popoverRowsFor’s rows, so its gate is the one checked above', () => {
    const popover = readFileSync(new URL('../webapp/src/components/PopoverLive.tsx', import.meta.url), 'utf8');
    assert.match(popover, /const accountRows = popoverRowsFor\(accounts, userID, access\);/);
    assert.doesNotMatch(popover, /\.people\[/, 'no way round it to the view itself');
    assert.match(popover, /accountRows\.map\(\(row\) => \(row\.service === 'steam' \? \(\n\s+<SteamRow\n\s+key=\{row\.service\}\n\s+row=\{row\}/);
    assert.match(popover, /!live && !voice && accountRows\.length === 0 &&/, 'and nothing else of theirs makes the popover appear');

    // The rows take a row and nothing else, and nothing but the popover
    // draws them.
    const rows = readFileSync(new URL('../webapp/src/components/AccountRows.tsx', import.meta.url), 'utf8');
    assert.match(rows, /export const SteamRow: React\.FC<\{row: PopoverRow\}>/);
    assert.match(rows, /export const HytaleRow: React\.FC<\{row: PopoverRow\}>/);
    assert.doesNotMatch(rows, /steamPopoverRow|hytalePopoverRow|AccountsSteam/);
    const components = new URL('../webapp/src/components/', import.meta.url);
    const drawing = readdirSync(components).filter((file) => /\.tsx$/.test(file) && /<(?:Steam|Hytale)Row\b/.test(readFileSync(new URL(file, components), 'utf8')));
    assert.deepEqual(drawing, ['PopoverLive.tsx']);

    // The pill's name and address come with the row: Steam's own profile
    // page only.
    const dave = accounts.popoverRowsFor(view, 'u-dave', 'linked');
    assert.deepEqual(dave.map((row) => [row.name, row.url]), [['dave_racer', ''], ['DaveBuilds', '']], 'a made-up address is no address');
    const real = accounts.steamPopoverRow({name: 'dave_racer', url: 'https://steamcommunity.com/profiles/76561197960287930', state: 'away'});
    assert.equal(real.url, 'https://steamcommunity.com/profiles/76561197960287930');
    assert.equal(accounts.steamPopoverRow({name: 'x', url: 'https://steamcommunity.com.example/profiles/76561197960287930', state: 'away'}).url, '');
});

// ---- The Disconnect line of somebody not linked (Victor's P3 pick, B) ----

const meWith = (services) => ({
    ...(services.includes('twitch') ? {twitch: {available: true, login: 'victorplays', displayName: 'VictorPlays'}} : {}),
    ...(services.includes('steam') ? {steam: {available: false, connected: true, name: 'VictorPlays'}} : {}),
    ...(services.includes('hytale') ? {hytale: {available: true, connected: true, name: 'VictorTheBuilder'}} : {}),
});
const subsets = [[], ['twitch'], ['steam'], ['hytale'], ['twitch', 'steam'], ['twitch', 'hytale'], ['steam', 'hytale'], ['twitch', 'steam', 'hytale']];

check('the line, at rest, for every set of connections', () => {
    for (const set of subsets) {
        const connections = accounts.connectionsOf(meWith(set));
        assert.deepEqual(connections.map((connection) => connection.service), set, 'Twitch, Steam, Hytale');
        const line = accounts.disconnectLineOf(connections, '', '', 'Matty');
        if (set.length === 0) {
            assert.deepEqual(line, {kind: 'none'});
        } else if (set.length === 1 && set[0] === 'twitch') {
            assert.deepEqual(line, {kind: 'twitch'}, 'Twitch alone: today’s TwitchDisconnectKey, unchanged');
        } else {
            assert.equal(line.kind, 'list', set.join('+'));
            assert.equal(line.lead, 'Disconnect');
            assert.deepEqual(line.keys.map((key) => key.word), set.map((service) => accounts.SERVICE_NAME[service]));
        }
    }
    const all = accounts.disconnectLineOf(accounts.connectionsOf(meWith(['twitch', 'steam', 'hytale'])), '', '', 'Matty');
    assert.equal(`${all.lead}  ${all.keys.map((key) => key.word).join(' · ')}`, 'Disconnect  Twitch · Steam · Hytale');
});

check('the line, armed: the whole line is that one service’s own words, Twitch keeping its own and its hours warning', () => {
    for (const set of subsets.filter((candidate) => candidate.length > 0 && !(candidate.length === 1 && candidate[0] === 'twitch'))) {
        const connections = accounts.connectionsOf(meWith(set));
        for (const service of set) {
            const line = accounts.disconnectLineOf(connections, service, '', 'Matty');
            assert.deepEqual([line.kind, line.service, line.words], ['armed', service, accounts.armedWords(service)], `${set.join('+')}, ${service} pressed`);
        }
    }
    const twitch = accounts.disconnectLineOf(accounts.connectionsOf(meWith(['twitch', 'steam'])), 'twitch', '', 'Matty');
    assert.equal(twitch.words, 'Disconnect Twitch? Press again');
    assert.equal(twitch.title, 'Your Twitch account, VictorPlays, is connected to Matty. Disconnecting removes it and your Twitch hours.');
    const steam = accounts.disconnectLineOf(accounts.connectionsOf(meWith(['twitch', 'steam'])), 'steam', '', 'Matty');
    assert.doesNotMatch(steam.title, /Twitch hours/);
    assert.match(steam.title, /^Your Steam account, .+, is connected to Matty\. Disconnecting removes it\.$/);
    const named = accounts.disconnectLineOf(accounts.connectionsOf(meWith(['twitch', 'steam'])), 'steam', '', 'Movie Night Club');
    assert.match(named.title, /^Your Steam account, .+, is connected to Movie Night Club\. Disconnecting removes it\.$/);

    // Pressed, and the account went before the second press (an admin, or
    // another tab): the line is the list again.
    assert.equal(accounts.disconnectLineOf(accounts.connectionsOf(meWith(['steam', 'hytale'])), 'twitch', '', 'Matty').kind, 'list');
});

check('Twitch alone stays a key of the line while the words after a press are still shown, then is Twitch’s own key', () => {
    const twitchOnly = accounts.connectionsOf(meWith(['twitch']));
    assert.deepEqual(accounts.disconnectLineOf(twitchOnly, '', '', 'Matty', false), {kind: 'twitch'});
    const saying = accounts.disconnectLineOf(twitchOnly, '', '', 'Matty', true);
    assert.equal(saying.kind, 'list');
    assert.deepEqual(saying.keys.map((key) => key.word), ['Twitch']);
    const armed = accounts.disconnectLineOf(twitchOnly, 'twitch', '', 'Matty', true);
    assert.deepEqual([armed.kind, armed.words], ['armed', 'Disconnect Twitch? Press again']);
    assert.match(armed.title, /Disconnecting removes it and your Twitch hours\.$/);
});

check('the line while the second press is out, and when it is in the page at all', () => {
    assert.deepEqual(accounts.disconnectLineOf([], '', 'steam', 'Matty'), {kind: 'busy', service: 'steam', words: 'Disconnecting…'}, 'the account gone before the answer');
    assert.equal(accounts.accountsLineShown({connected: 0, busy: false, saying: false}), false);
    assert.equal(accounts.accountsLineShown({connected: 2, busy: false, saying: false}), true);
    assert.equal(accounts.accountsLineShown({connected: 0, busy: true, saying: false}), true, 'the press out');
    assert.equal(accounts.accountsLineShown({connected: 0, busy: false, saying: true}), true, 'the words after');
});

// ---- Takeover notices (5.6) ----

check('takeover notices: newest first, none for a service the reader is blocked from', () => {
    const me = {
        twitch: {available: true, lost: {login: 'victorplays', at: 3}},
        steam: {available: true, lost: {name: 'VictorPlays', at: 7}},
        hytale: {available: true, lost: {name: 'VictorTheBuilder', at: 5}},
    };
    assert.deepEqual(accounts.noticesOf(me).map((notice) => notice.service), ['steam', 'hytale', 'twitch']);
    assert.deepEqual(accounts.noticesOf({...me, steam: {...me.steam, blocked: true}}).map((notice) => notice.service), ['hytale', 'twitch']);
    assert.deepEqual(accounts.noticesOf(null), []);
    assert.equal(accounts.lostWords('steam', 'VictorPlays', '12 Nov 2026'),
        'Your Steam account VictorPlays was connected to another Mattermost account on 12 Nov 2026. If that was not you, tell an admin.');
});

// ---- Waiting ----

check('Waiting: the pop-up hint after 4 s with no sign of the window, Hytale’s after a minute, and polling for 5 minutes', () => {
    assert.deepEqual(accounts.waitingAt('steam', 3999, false), {polling: true, noWindow: false, redirect: false});
    assert.deepEqual(accounts.waitingAt('steam', 4000, false), {polling: true, noWindow: true, redirect: false});
    assert.deepEqual(accounts.waitingAt('steam', 4000, true), {polling: true, noWindow: false, redirect: false}, 'the window took focus');
    assert.deepEqual(accounts.waitingAt('steam', 120000, true), {polling: true, noWindow: false, redirect: false}, 'Steam has no set-up hint');
    assert.deepEqual(accounts.waitingAt('hytale', 59999, true), {polling: true, noWindow: false, redirect: false});
    assert.deepEqual(accounts.waitingAt('hytale', 60000, true), {polling: true, noWindow: false, redirect: true});
    assert.equal(accounts.waitingAt('hytale', 5 * 60 * 1000 - 1, true).polling, true);
    assert.equal(accounts.waitingAt('hytale', 5 * 60 * 1000, true).polling, false, 'given up');
    assert.deepEqual([accounts.WAITING_POLL_MS, accounts.TICKET_REFRESH_MS, accounts.DISCONNECTED_MS], [3000, 9 * 60 * 1000, 6000]);
});

check('the flow’s Waiting says and does what waitingAt says, and keeps no rule of its own', () => {
    const sheet = readFileSync(new URL('../webapp/src/components/AccountSheet.tsx', import.meta.url), 'utf8');
    const flow = sheet.slice(sheet.indexOf('export const AccountFlow'));
    assert.match(flow, /const waiting = waitingAt\(service, lookedAt - waitFrom, sawWindow \|\| looking\);/);
    assert.match(flow, /\{waiting\.noWindow \? <p className='vntv-twitch-note vntv-twitch-muted'>\{noWindowWords\(service\)\}<\/p> : null\}/);
    assert.match(flow, /\{waiting\.redirect \? <p className='vntv-twitch-note vntv-twitch-muted'>\{HYTALE_REDIRECT_HINT\}<\/p> : null\}/);
    assert.match(flow, /if \(waitingAt\(service, clockNow\(\) - waitFrom, true\)\.polling\) \{\n\s+read\(\);\n\s+\} else \{\n\s+window\.clearInterval\(poll\);/, 'and stops reading "me" when it says to');

    // The window's sign only sets what waitingAt is told; the moments only
    // say when to look at the clock again.
    assert.match(flow, /const seen = \(\) => setSawWindow\(true\);/);
    assert.match(flow, /\[NO_WINDOW_HINT_MS, REDIRECT_HINT_MS, WAITING_GIVE_UP_MS\]\.map\(/);
    assert.doesNotMatch(flow, /setHint|setRedirectHint|noWindow: |redirect: /, 'no second copy of the rule');
    assert.equal((flow.match(/NO_WINDOW_HINT_MS|REDIRECT_HINT_MS|WAITING_GIVE_UP_MS/g) || []).length, 3, 'each moment named once, in that list');
});

check('the reader’s own connection, as the "me" event is held to it: it changes when the connection does, and only then', () => {
    const own = accounts.ownConnectionOf;
    assert.equal(own(undefined), '');
    assert.equal(own({available: true}), own({available: false}), 'a service switched on or off is not the reader’s connection');
    assert.equal(own({available: true}), own({available: true, problem: 'No client secret.'}));
    assert.equal(own({available: true, login: 'dave'}), own({available: true, login: 'dave', live: true, displayName: 'Dave'}), 'nor is being live on Twitch');
    assert.equal(own({available: true, connected: true, name: 'Dave'}), own({available: true, connected: true, name: 'Dave', hidden: true, url: 'https://steamcommunity.com/profiles/1'}), 'nor a Steam profile gone private');
    const before = own({available: true});
    for (const after of [
        {available: true, login: 'dave'},
        {available: true, connected: true, name: 'Dave'},
        {available: true, blocked: true},
        {available: true, lost: {login: 'dave', at: 5}},
        {available: true, lost: {name: 'Dave', at: 5}},
    ]) {
        assert.notEqual(own(after), before, JSON.stringify(after));
    }
    assert.notEqual(own({available: true, lost: {name: 'Dave', at: 5}}), own({available: true, lost: {name: 'Dave', at: 6}}), 'taken over again');
});

check('the "me" event is the Connect window’s sign only when the "me" read after it shows the connection changed, and a hint waits for that read', () => {
    const sheet = readFileSync(new URL('../webapp/src/components/AccountSheet.tsx', import.meta.url), 'utf8');
    const flow = sheet.slice(sheet.indexOf('export const AccountFlow'));
    assert.match(flow, /const before = ownConnectionOf\(getIdentity\(\)\?\.\[service\]\);/, 'the connection as Waiting began');
    assert.match(flow, /void eventRead\.then\(\(fresh\) => \{\n\s+if \(!live\) \{\n\s+return;\n\s+\}\n\s+if \(readSaidNothing\(fresh\) \|\| ownConnectionOf\(getIdentity\(\)\?\.\[service\]\) !== before\) \{\n\s+seen\(\);/);
    assert.match(flow, /const unlink = onAccountLink\(heard\);/);
    assert.doesNotMatch(flow, /onAccountLink\(seen\)/, 'never the bare event');
    assert.match(flow, /const holds = !noWindowShown\.current;/, 'a hint already showing is left as it is until the read is back');

    const twitch = readFileSync(new URL('../webapp/src/components/TwitchConnect.tsx', import.meta.url), 'utf8');
    assert.match(twitch, /const before = ownConnectionOf\(getIdentity\(\)\?\.twitch\);/);
    assert.match(twitch, /if \(readSaidNothing\(fresh\) \|\| ownConnectionOf\(getIdentity\(\)\?\.twitch\) !== before\) \{\n\s+seen\(\);\n\s+\} else if \(looking === 0 && hintDue && !sign\) \{/);

    // A read that says nothing (it failed, or the store did not answer) leaves the event to be
    // taken at its word, as it was until 3.6.0; one that came back says for itself.
    const nothing = accounts.readSaidNothing;
    assert.equal(nothing(null), true);
    assert.equal(nothing(undefined), true);
    assert.equal(nothing({access: 'unknown'}), true);
    assert.equal(nothing({access: 'linked'}), false);
    assert.equal(nothing({access: 'locked'}), false);
    assert.match(twitch, /const unlink = onTwitchLink\(heard\);/);
    assert.doesNotMatch(twitch, /onTwitchLink\(seen\)/, 'never the bare event');
    assert.match(twitch, /if \(looking > 0\) \{\n\s+hintDue = true;\n\s+\} else \{\n\s+setHint\(true\);/, 'the hint’s moment waits for a read still out');
});

console.log(`check-accounts: ${checks} checks passed`);
