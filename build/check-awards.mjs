// What shows beside a name, the achievements' words and their toast, and a
// shared plan's card (design/3.2.0.md 3.9, 5.8 and 6.9), checked without a
// browser.
//
// Who holds what and what goes beside a name are decided on the server since
// 3.6.0 (server/awards_decide.go, held by awards_decide_test.go). Two things
// of it are still TypeScript, and are held here to the same cases as the Go:
//
//   - the lab's stand-in for the server (webapp/lab/awards.ts), which makes
//     what its world's server would have sent, so the lab never shows what a
//     server would not;
//   - what is left in the panel (webapp/src/awardLogic.ts): the picker's
//     preview of a choice being made, which draws by the rule the server
//     then applies to it, reading what the server decided, the NEW marks and
//     the toast.
//
// webapp/src/awardLogic.ts imports only types, so it is transpiled with the
// project's own TypeScript, imported from a data URL and run here, as
// check-twitch.mjs does with twitchLogic.ts; the lab's file imports that one,
// and is handed it. webapp/src/planLogic.ts imports time.ts, which imports
// nothing, so time.ts is transpiled too and handed to it in place of
// './time'. Run from the project root (make check-webapp).

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

// A module with its relative imports swapped for other modules' sources.
async function loadWith(file, standIns = {}) {
    const swapped = transpile(file).replace(/from '(\.[^']*)'/g, (_, path) => {
        assert.ok(path in standIns, `${file} imports ${path}, and there is no stand-in for it`);

        return `from '${dataURL(standIns[path])}'`;
    });

    return import(dataURL(swapped));
}

const awards = await loadWith('awardLogic.ts');
const lab = await loadWith('../lab/awards.ts', {'../src/awardLogic': transpile('awardLogic.ts')});
const plans = await loadWith('planLogic.ts', {'./time': transpile('time.ts')});

let checks = 0;
function check(name, run) {
    try {
        run();
        checks++;
    } catch (error) {
        console.error(`check-awards: ${name}`);
        throw error;
    }
}

// ---- Boards to decide awards from ----

const KINDS = [
    {id: 'streamed', name: 'Streamed', unit: 'hours', tiers: [10, 50, 100, 250, 500, 1000]},
    {id: 'snooped', name: 'Snooped', unit: 'hours', tiers: [10, 50, 100, 250, 500]},
    {id: 'podium', name: 'Podium', unit: 'months', tiers: [1, 3, 6, 12]},
    {id: 'thumbs', name: 'Thumbs up', unit: 'thumbs', tiers: [10, 50, 100, 250]},
    {id: 'ch-1a2b3c4d5e6f', name: 'Lo-fi radio', unit: 'hours', tiers: [1, 10, 50, 100, 250]},
].map(lab.namedKind);

// Somebody who holds whichever of the four old awards are asked for, and
// whatever else is added.
function holding({alltime = 0, month = 0, snooper = false, clipper = false, userId = 'u-v'} = {}, more = {}) {
    return {
        allTime: alltime ? [{rank: alltime, title: 'x', seconds: 1, userId}] : [{rank: 1, title: 'y', seconds: 9, userId: 'u-other'}],
        month: month ? [{rank: month, title: 'x', seconds: 1, userId}] : [],
        monthLabel: '',
        watchAllTime: snooper ? [{rank: 1, title: 'x', seconds: 1, userId}] : [{rank: 1, title: 'y', seconds: 1, userId: 'u-other'}],
        bestClipper: clipper ? {userId, upvotes: 3, clips: 1} : undefined,
        monthMedals: [],
        fans: [],
        kinds: [],
        achievements: [],
        choices: {},
        ...more,
    };
}

const ids = (list) => list.map((award) => award.id);

// The snapshot's people index, as the server would have filled it from these
// boards: what the panel reads.
const snapshotOf = (facts) => ({
    streams: [],
    fetchedAt: 1,
    configured: true,
    people: Object.fromEntries(lab.holdersOf(facts).
        map((userId) => [userId, {awards: lab.besideOf(facts, userId)}]).
        filter(([, person]) => person.awards.length > 0)),
});

// ---- Automatic ----

check('Automatic is 3.1.0’s set exactly: the four old awards whenever held, in their order, for every combination', () => {
    for (let mask = 0; mask < 16; mask++) {
        const has = {alltime: mask & 1 ? 2 : 0, month: mask & 2 ? 1 : 0, snooper: Boolean(mask & 4), clipper: Boolean(mask & 8)};
        const facts = holding(has);
        const expected = ['alltime', 'month', 'snooper', 'clipper'].filter((id, index) => mask & (1 << index));
        assert.deepEqual(ids(lab.besideOf(facts, 'u-v')), expected, `mask ${mask}`);
        assert.deepEqual(ids(awards.shownAwards(lab.heldOf(facts, 'u-v'), null)), expected, `the picker’s preview, mask ${mask}`);
        assert.deepEqual(ids(awards.besideName(snapshotOf(facts), 'u-v')), expected, `as the panel reads it, mask ${mask}`);
    }
});

check('Automatic never includes a past month, a fan title or an achievement, held or not', () => {
    const facts = holding({alltime: 1}, {
        monthMedals: [{userId: 'u-v', gold: ['2026-08', '2026-09']}],
        fans: [{streamerId: 'u-m', streamer: 'Dave', fanId: 'u-v', fan: 'Victor', hours: 4}],
        kinds: KINDS,
        achievements: [{userId: 'u-v', have: [{id: 'streamed', tier: 4}]}],
    });
    assert.deepEqual(ids(lab.heldOf(facts, 'u-v')), ['alltime', 'past', 'fan:u-m', 'ach:streamed'], 'all held');
    assert.deepEqual(ids(lab.besideOf(facts, 'u-v')), ['alltime']);
});

check('nobody chose is Automatic, and a choice is drawn in its own order, at most four', () => {
    const facts = holding({alltime: 1, month: 2, clipper: true}, {choices: {'u-v': ['clipper', 'month'], 'u-b': ['a', 'b', 'c', 'd', 'e']}});
    assert.deepEqual(ids(lab.besideOf(facts, 'u-v')), ['clipper', 'month']);
    assert.deepEqual(lab.besideOf(facts, 'u-nobody'), []);
    assert.deepEqual(lab.besideOf(holding({alltime: 1, clipper: true}, {choices: {'u-v': []}}), 'u-v'), [], 'an empty choice is a choice: nothing');
});

// ---- A choice ----

check('a choice keeps its order, whatever order the awards are held in', () => {
    const held = lab.heldOf(holding({alltime: 1, month: 2, snooper: true, clipper: true}), 'u-v');
    assert.deepEqual(ids(awards.shownAwards(held, ['clipper', 'alltime', 'snooper'])), ['clipper', 'alltime', 'snooper']);
});

check('an award chosen but not held now is skipped, and kept: held again, it is back where it was put', () => {
    const choice = ['snooper', 'month', 'alltime'];
    const without = lab.heldOf(holding({alltime: 1, month: 0, snooper: true}), 'u-v');
    assert.deepEqual(ids(awards.shownAwards(without, choice)), ['snooper', 'alltime']);
    assert.deepEqual(choice, ['snooper', 'month', 'alltime'], 'the choice itself is untouched');
    const again = lab.heldOf(holding({alltime: 1, month: 3, snooper: true}), 'u-v');
    assert.deepEqual(ids(awards.shownAwards(again, choice)), ['snooper', 'month', 'alltime']);
});

check('never more than four, an unknown id is ignored, a repeat is drawn once, and an empty choice shows nothing', () => {
    const facts = holding({alltime: 1, month: 1, snooper: true, clipper: true}, {monthMedals: [{userId: 'u-v', bronze: ['2026-07']}]});
    const held = lab.heldOf(facts, 'u-v');
    assert.equal(awards.shownAwards(held, ['alltime', 'month', 'snooper', 'clipper', 'past']).length, 4);
    assert.deepEqual(ids(awards.shownAwards(held, ['nonsense', 'month'])), ['month']);
    assert.deepEqual(ids(awards.shownAwards(held, ['month', 'month', 'alltime'])), ['month', 'alltime']);
    assert.deepEqual(awards.shownAwards(held, []), []);

    // The four are counted before what is not held is skipped: a fifth is
    // never promoted into a gap. As the server counts them.
    // The fifth (a past month's medal) is held, so it would show if the four
    // were counted after the gap.
    const without = lab.heldOf(holding({alltime: 1, month: 0, snooper: true, clipper: true}, {monthMedals: [{userId: 'u-v', bronze: ['2026-07']}]}), 'u-v');
    assert.ok(without.some((award) => award.id === 'past'), 'the fifth is held');
    assert.ok(!without.some((award) => award.id === 'month'), 'and the second is not');
    assert.deepEqual(ids(awards.shownAwards(without, ['alltime', 'month', 'snooper', 'clipper', 'past'])), ['alltime', 'snooper', 'clipper']);
});

check('the headphones are never an award: they are not held, not chosen and not counted in the four', () => {
    const held = lab.heldOf(holding({alltime: 1, month: 1, snooper: true, clipper: true}), 'u-v');
    assert.ok(!held.some((award) => /voice|mumble|headphones/i.test(award.id)));
    assert.equal(awards.shownAwards(held, ['alltime', 'month', 'snooper', 'clipper']).length, 4, 'four, and the headphones on top of them');
    assert.equal(awards.MAX_SHOWN, 4);
});

// ---- Past months ----

check('a past month is held exactly when a medal is, at the best place held, named as the profile names it', () => {
    const one = (medals) => lab.heldOf(holding({}, {monthMedals: [{userId: 'u-v', ...medals}]}), 'u-v').find((award) => award.id === 'past');
    assert.equal(one({}), undefined, 'no medal, no award');
    assert.deepEqual([one({bronze: ['2026-01']}).rank, one({bronze: ['2026-01']}).label], [3, 'Monthly podium']);
    assert.equal(one({silver: ['2026-02'], bronze: ['2026-01']}).rank, 2);
    const champion = one({gold: ['2026-03', '2026-05'], silver: ['2026-02']});
    assert.deepEqual([champion.rank, champion.label, champion.short], [1, 'Monthly champion, 2 months', 'Monthly champion ×2']);
    assert.equal(one({gold: ['2026-03']}).short, 'Monthly champion', '×N only from two golds');
});

// ---- Fans and achievements ----

check('a fan title is held one per streamer, as the list has them', () => {
    const fans = [{streamerId: 'u-m', streamer: 'Dave', fanId: 'u-v', fan: 'Victor', hours: 41}, {streamerId: 'u-a', streamer: 'Bob', fanId: 'u-v', fan: 'Victor', hours: 2}];
    assert.deepEqual(ids(lab.heldOf(holding({}, {fans}), 'u-v')), ['fan:u-a', 'fan:u-m']);
    assert.equal(lab.heldOf(holding({}, {fans}), 'u-v')[0].label, 'Biggest fan of Bob');
    assert.deepEqual(ids(lab.heldOf(holding(), 'u-v')), [], 'and none while biggest fans are off, when there is no list');
});

check('an achievement is held at its highest step, in catalogue order, and named by its step', () => {
    const facts = holding({}, {kinds: KINDS, achievements: [{userId: 'u-v', have: [{id: 'ch-1a2b3c4d5e6f', tier: 1}, {id: 'streamed', tier: 3}]}]});
    const held = lab.heldOf(facts, 'u-v');
    assert.deepEqual(held.map((award) => [award.id, award.tier, award.label]), [
        ['ach:streamed', 3, 'Streamed 100 hours'],
        ['ach:ch-1a2b3c4d5e6f', 1, 'Lo-fi radio: 1 hour'],
    ]);
    const off = {...facts, kinds: KINDS.filter((kind) => kind.id !== 'streamed')};
    assert.deepEqual(ids(lab.heldOf(off, 'u-v')), ['ach:ch-1a2b3c4d5e6f'], 'a family switched off is not held');
});

check('the steps’ names are the server’s, sent with the family: one hour, one month, thumbs up, and a channel’s own name', () => {
    const kind = (id) => KINDS.find((entry) => entry.id === id);
    assert.deepEqual(kind('streamed').names, ['Streamed 10 hours', 'Streamed 50 hours', 'Streamed 100 hours', 'Streamed 250 hours', 'Streamed 500 hours', 'Streamed 1000 hours']);
    assert.equal(awards.stepName(kind('streamed'), 1), 'Streamed 10 hours');
    assert.equal(awards.stepName(kind('snooped'), 2), 'Snooped 50 hours');
    assert.equal(awards.stepName(kind('podium'), 1), 'On the podium 1 month');
    assert.equal(awards.stepName(kind('podium'), 3), 'On the podium 6 months');
    assert.equal(awards.stepName(kind('thumbs'), 2), '50 thumbs up');
    assert.equal(awards.stepName(kind('ch-1a2b3c4d5e6f'), 1), 'Lo-fi radio: 1 hour');
    assert.equal(awards.stepName(kind('ch-1a2b3c4d5e6f'), 2), 'Lo-fi radio: 10 hours');
    assert.equal(awards.stepName(kind('streamed'), 9), 'Streamed 1000 hours', 'a step past the last is the last');
    assert.equal(awards.stepName({id: 'x', name: 'Old server', unit: 'hours', tiers: [1]}, 1), 'Old server', 'and with no names sent, the family’s');
    assert.doesNotMatch(readFileSync(new URL('../webapp/src/awardLogic.ts', import.meta.url), 'utf8'), /plural\(step, 'hour'|`\$\{step\} thumbs up`/, 'the panel words no step itself');
    assert.equal(awards.stepShort(kind('podium'), 1), '1 month');
    assert.equal(awards.stepShort(kind('streamed'), 250), '250 h');
    assert.equal(awards.familyHead(kind('streamed')), 'On air');
    assert.equal(awards.familyHead(kind('ch-1a2b3c4d5e6f')), 'Lo-fi radio');
});

// ---- The Live now card and beside "Clipped by" ----

check('a card’s own places stand in for the boards’, and Automatic on a card stays 3.1.0’s card set, no clapper', () => {
    const facts = holding({alltime: 0, month: 0, snooper: true, clipper: true});
    const card = {userId: 'u-v'};
    const places = {allTime: 2, month: 1};
    assert.deepEqual(ids(lab.onCardOf(facts, card, places)), ['alltime', 'month', 'snooper'], 'the card’s ranks, and no clapper');
    assert.deepEqual(lab.onCardOf(facts, card, places)[0], {id: 'alltime', kind: 'alltime', rank: 2, label: '2nd for airtime, all time'});
    const chose = {...facts, choices: {'u-v': ['clipper', 'month']}};
    assert.deepEqual(ids(lab.onCardOf(chose, card, places)), ['clipper', 'month'], 'a choice is the same on the card as by a post');
    assert.equal(lab.onCardOf(chose, card, places)[1].rank, 1, 'with the card’s place');
    assert.deepEqual(ids(lab.besideOf(chose, 'u-v')), ['clipper'], 'by a post the boards’ own place counts, and they hold none');
    assert.deepEqual(ids(lab.onCardOf(facts, {...card, source: 'twitch'}, places)), ['alltime', 'month'], 'no binoculars on the Top Snooper’s Twitch card');
    assert.deepEqual(ids(lab.onCardOf(holding({alltime: 1, month: 1, snooper: true}), {}, {allTime: 3, month: 0})), ['alltime'], 'a card with no person keeps its places');
    assert.deepEqual(lab.onCardOf(chose, {...card, alwaysOn: true}, {allTime: 1, month: 1}), [], 'a 24/7 channel has none');
});

check('the clapper is “Best Clipper, all time” beside a name and “Best Clipper” on its chip, and the panel words it itself either way', () => {
    const facts = holding({clipper: true});
    assert.deepEqual(lab.besideOf(facts, 'u-v'), [{id: 'clipper', kind: 'clipper', rank: undefined, family: undefined, tier: undefined, label: 'Best Clipper, all time'}]);
    assert.deepEqual(lab.heldOf(facts, 'u-v').map((award) => [award.label, award.short]), [['Best Clipper', 'Best Clipper']]);
    const mark = readFileSync(new URL('../webapp/src/components/AwardMark.tsx', import.meta.url), 'utf8');
    assert.match(mark, /case 'clipper':\n\s+return \{label: CLIPPER_LABEL, title: CLIPPER_HINT\};/, 'what the panel says beside a name is its own, whatever the label');
});

check('the clapper beside "Clipped by" is the Best Clipper’s, unless their choice leaves it out', () => {
    const facts = holding({clipper: true});
    assert.equal(awards.clapperBeside(snapshotOf(facts)), 'u-v');
    assert.equal(awards.clapperBeside(snapshotOf({...facts, choices: {'u-v': ['alltime']}})), undefined);
    assert.equal(awards.clapperBeside(snapshotOf({...facts, choices: {'u-v': ['clipper']}})), 'u-v');
    assert.equal(awards.clapperBeside(snapshotOf(holding())), undefined, 'nobody holds it');
    assert.equal(awards.clapperBeside({streams: [], fetchedAt: 0, configured: true}), undefined, 'nor before the first snapshot');
});

// ---- A profile ----

check('a profile keeps the whole record whatever is shown beside the name, row for row as the server lists it', () => {
    const facts = {
        allTime: [{rank: 2, title: 'x', seconds: 1483200, userId: 'u-v'}],
        month: [{rank: 1, title: 'x', seconds: 7260, userId: 'u-v'}],
        monthLabel: 'November 2026',
        watchAllTime: [{rank: 1, title: 'x', seconds: 90000, userId: 'u-v'}],
        bestClipper: {userId: 'u-v', upvotes: 142, clips: 9},
        monthMedals: [{userId: 'u-v', gold: ['2026-03', '2026-08', '2026-03'], bronze: ['2026-01', '2026-02']}],
        fans: [
            {streamerId: 'u-a', streamer: 'bob', fanId: 'u-v', hours: 41},
            {streamerId: 'u-b', streamer: 'Trudy', fanId: 'u-v', hours: 41},
            {streamerId: 'u-m', streamer: 'Dave', fanId: 'u-v', hours: 60},
        ],
        kinds: KINDS,
        achievements: [{userId: 'u-v', have: [{id: 'podium', tier: 2}, {id: 'streamed', tier: 4}]}],
        choices: {'u-v': []},
    };
    assert.deepEqual(lab.besideOf(facts, 'u-v'), [], 'they chose to show nothing');

    // The same record, in the same words, as TestAProfileKeepsTheWholeRecord
    // in server/awards_decide_test.go.
    assert.deepEqual(JSON.parse(JSON.stringify(lab.rowsOf(facts, 'u-v'))), [
        {kind: 'board', id: 'alltime', rank: 2, title: 'Runner-up', board: 'All time', seconds: 1483200},
        {kind: 'board', id: 'month', rank: 1, title: 'Champion', board: 'November 2026', seconds: 7260},
        {kind: 'board', id: 'snooper', rank: 1, title: 'Top Snooper', board: 'All time', seconds: 90000, doing: 'watching others'},
        {kind: 'clipper', id: 'clipper', rank: 1, title: 'Best Clipper', upvotes: 142, clips: 9},
        {kind: 'fan', id: 'fan:u-m', rank: 1, title: 'Biggest fan of Dave', streamerId: 'u-m', streamer: 'Dave', hours: 60},
        {kind: 'fan', id: 'fan:u-a', rank: 1, title: 'Biggest fan of bob', streamerId: 'u-a', streamer: 'bob', hours: 41},
        {kind: 'fan', id: 'fan:u-b', rank: 1, title: 'Biggest fan of Trudy', streamerId: 'u-b', streamer: 'Trudy', hours: 41},
        {kind: 'achievements', id: 'achievements', rank: 0, title: 'Achievements', steps: [
            {id: 'streamed', tier: 4, name: 'Streamed 250 hours'},
            {id: 'podium', tier: 2, name: 'On the podium 3 months'},
        ]},
        {kind: 'months', id: 'past', rank: 1, title: 'Monthly champion', gold: ['2026-08', '2026-03'], tallies: [{rank: 3, months: ['2026-02', '2026-01']}]},
    ]);
    assert.deepEqual(lab.rowsOf(facts, 'u-nobody'), []);

    // Fans tied on hours are in the order of their names as a reader reads them, an accent and
    // a capital being the letter they are on and punctuation coming before a digit (Óscar before
    // Peggy, the_gamer before the9gamer), then by the streamer's id: the server's order since
    // 3.1.0 (fans.ts until 3.6.0), which awards_decide.go keeps with a collator.
    const tied = {...facts, fans: [
        {streamerId: 'u-p', streamer: 'Peggy', fanId: 'u-v', hours: 2},
        {streamerId: 'u-o2', streamer: 'oscar', fanId: 'u-v', hours: 2},
        {streamerId: 'u-o', streamer: 'Óscar', fanId: 'u-v', hours: 2},
        {streamerId: 'u-9', streamer: 'the9gamer', fanId: 'u-v', hours: 5},
        {streamerId: 'u-_', streamer: 'the_gamer', fanId: 'u-v', hours: 5},
    ]};
    assert.deepEqual(lab.rowsOf(tied, 'u-v').filter((row) => row.kind === 'fan').map((row) => row.streamer), ['the_gamer', 'the9gamer', 'Óscar', 'oscar', 'Peggy']);

    const list = readFileSync(new URL('../webapp/src/components/AwardList.tsx', import.meta.url), 'utf8');
    assert.match(list, /const rows = recordOf\(useBoards\(\), userId\);/, 'the profile draws the rows it is given');
    assert.doesNotMatch(list, /awardsFor|topSnooperOf|fanTitlesOf|useSnapshot|snapshot\./, 'and works nobody’s record out of the boards itself');
});

// ---- NEW on the You tab's chips ----

check('NEW: nothing the first time a browser opens the tab; after that, awards held since and not chosen', () => {
    const held = lab.heldOf(holding({alltime: 1, month: 2}), 'u-v');
    assert.deepEqual(awards.newAwards(held, null, null), []);
    assert.deepEqual(awards.newAwards(held, ['alltime'], null), ['month']);
    assert.deepEqual(awards.newAwards(held, ['alltime'], ['month']), [], 'a chosen one is not new');

    // What is held comes with the reader's choice, so nothing is marked, or
    // recorded as seen, before that answer: an empty list while it loads
    // would make everything new the next time.
    const picker = readFileSync(new URL('../webapp/src/components/AwardPicker.tsx', import.meta.url), 'utf8');
    assert.match(picker, /if \(mine\) \{\n\s+setDraft\(mine\.show\);\n\s+setFresh\(newAwards\(mine\.held, readSeen\(userId\), null\)\);\n\s+writeSeen\(userId, mine\.held\.map\(\(award\) => award\.id\)\);/);
});

// ---- The toast (an approved departure: stepToSay is pure and checked here) ----

check('the toast: the first sight of somebody says nothing, so backdated steps do not arrive as a burst', () => {
    const first = awards.stepToSay(null, KINDS, [{id: 'streamed', tier: 4}, {id: 'podium', tier: 2}]);
    assert.deepEqual(first.says, []);
    assert.equal(first.keep.streamed, 4);
    assert.equal(first.keep.snooped, 0, 'every family recorded, at 0 where none is held');
});

check('the toast: a new step says so, once', () => {
    const seen = awards.stepToSay(null, KINDS, [{id: 'streamed', tier: 2}]).keep;
    const next = awards.stepToSay(seen, KINDS, [{id: 'streamed', tier: 3}]);
    assert.deepEqual(next.says, [{id: 'streamed', tier: 3}]);
    assert.deepEqual(awards.stepToSay(next.keep, KINDS, [{id: 'streamed', tier: 3}]).says, [], 'not twice');
});

check('the toast: two families stepping up in one refresh are both said, in catalogue order, and neither again', () => {
    const seen = awards.stepToSay(null, KINDS, [{id: 'streamed', tier: 5}, {id: 'snooped', tier: 4}]).keep;
    const next = awards.stepToSay(seen, KINDS, [{id: 'streamed', tier: 6}, {id: 'snooped', tier: 5}]);
    assert.deepEqual(next.says, [{id: 'streamed', tier: 6}, {id: 'snooped', tier: 5}]);
    assert.equal(next.keep.snooped, 5);
    assert.deepEqual(awards.stepToSay(next.keep, KINDS, [{id: 'streamed', tier: 6}, {id: 'snooped', tier: 5}]).says, []);
    const toast = readFileSync(new URL('../webapp/src/components/AchievementToast.tsx', import.meta.url), 'utf8');
    assert.match(toast, /says\.forEach\(\(step\) => show\(step\.id, step\.tier\)\)/, 'the toast queues every one');
});

check('the toast: a family switched on later is a first sight of that family, and says nothing', () => {
    const before = awards.stepToSay(null, KINDS.slice(0, 4), [{id: 'streamed', tier: 1}]).keep;
    const later = awards.stepToSay(before, KINDS, [{id: 'streamed', tier: 1}, {id: 'ch-1a2b3c4d5e6f', tier: 2}]);
    assert.deepEqual(later.says, []);
    assert.equal(later.keep['ch-1a2b3c4d5e6f'], 2);
});

check('the toast: a dip and a recovery are not news twice', () => {
    let seen = awards.stepToSay(null, KINDS, [{id: 'snooped', tier: 3}]).keep;
    seen = awards.stepToSay(seen, KINDS, [{id: 'snooped', tier: 1}]).keep;
    assert.equal(seen.snooped, 3, 'the highest step ever recorded is kept');
    assert.deepEqual(awards.stepToSay(seen, KINDS, [{id: 'snooped', tier: 3}]).says, []);
    assert.deepEqual(awards.stepToSay(seen, KINDS, [{id: 'snooped', tier: 4}]).says, [{id: 'snooped', tier: 4}]);
});

check('the toast: nothing fires before a real snapshot; the empty first one records nothing to make the real one look new', () => {
    const empty = awards.stepToSay(null, [], []);
    assert.deepEqual(empty.says, []);
    assert.deepEqual(empty.keep, {});
    const real = awards.stepToSay(empty.keep, KINDS, [{id: 'streamed', tier: 4}]);
    assert.deepEqual(real.says, [], 'the backdated steps of the first real snapshot are silent');
    const toast = readFileSync(new URL('../webapp/src/components/AchievementToast.tsx', import.meta.url), 'utf8');
    assert.match(toast, /const ready = boardsRead\(\);/, 'nothing until the boards have really been read');
    assert.match(toast, /if \(!ready \|\| kinds\.length === 0\) \{/);
});

check('the picker: a change still waiting to be saved when the tab goes is sent, not dropped; and the segments are one radio group', () => {
    const picker = readFileSync(new URL('../webapp/src/components/AwardPicker.tsx', import.meta.url), 'utf8');
    assert.match(picker, /if \(waiting && waiting\.generation === ownGeneration\(\)\) \{\n\s+saveAwardChoice\(waiting\.next\)/, 'sent at once on unmount, unless signed out since');
    assert.match(picker, /tabIndex=\{automatic \? 0 : -1\}/, 'one tab stop, the checked segment');
    assert.match(picker, /tabIndex=\{automatic \? -1 : 0\}/);
    assert.match(picker, /\['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'\]\.includes\(event\.key\)/, 'and the arrows choose the other');
    assert.match(picker, /const stored = storedOf\(answer, next\);/, 'what the server stored is what the chips show after a save (a retired 24/7 family comes out)');
});

check('the picker while the reader’s own answer is on its way: the last one kept, or what the server shows beside the name, and never “nothing to show” before it is known', () => {
    const mine = (held, show = null) => ({held, show, readable: true});
    const crown = {id: 'alltime', kind: 'alltime', rank: 1, label: '1st for airtime, all time', short: '1st, all time'};
    const clapper = {id: 'clipper', kind: 'clipper', label: 'Best Clipper', short: 'Best Clipper'};
    const medal = {id: 'past', kind: 'past', rank: 2, label: 'Past months', short: 'Past months'};
    const beside = [{id: 'clipper', kind: 'clipper', label: 'Best Clipper, all time'}];

    // Nothing known yet (the first opening on this page): no chips, the preview as everybody
    // sees the reader now, and holdings not known.
    const first = awards.pickerFrom(null, null, null, beside);
    assert.deepEqual(first, {held: [], shown: beside, known: false});
    assert.equal(awards.pickerFrom(null, null, null, []).held, awards.pickerFrom(null, null, null, []).held, 'one list for nothing, so a render while it loads is not a change');

    // The last answer kept: every chip at once, and the preview Automatic, as the picker always
    // was while it loaded.
    const kept = mine([crown, clapper, medal], ['past']);
    assert.deepEqual(awards.pickerFrom(null, kept, null, beside), {held: [crown, clapper, medal], shown: [crown, clapper], known: true});
    assert.deepEqual(awards.pickerFrom(null, mine([]), null, beside), {held: [], shown: [], known: true}, 'nothing held is known');

    // This opening's answer: what it says, with the choice being made.
    assert.deepEqual(awards.pickerFrom(mine([clapper, medal], ['past']), kept, ['past'], beside).shown, [medal]);
    assert.deepEqual(awards.pickerFrom(mine([clapper]), kept, null, beside).held, [clapper]);

    const picker = readFileSync(new URL('../webapp/src/components/AwardPicker.tsx', import.meta.url), 'utf8');
    assert.match(picker, /const beside = besideName\(snapshot, userId\);\n\s+const \{held, shown, known\} = pickerFrom\(mine, kept, draft, beside\);/);
    assert.match(picker, /if \(held\.length === 0 && automatic && \(known \|\| beside\.length === 0\)\) \{\n\s+hint = 'Nothing to show yet\./, '“Nothing to show yet” once it is known, and until then only where nothing is beside their name either');
    assert.match(picker, /\} else if \(automatic\) \{\n\s+hint = 'Shows your places on the boards, Top Snooper and Best Clipper whenever you hold them\.';/);
    assert.match(picker, /setFresh\(newAwards\(mine\.held, readSeen\(userId\), null\)\);/, 'the NEW marks from a real answer, never the kept one');
    const you = readFileSync(new URL('../webapp/src/components/TrophiesYou.tsx', import.meta.url), 'utf8');
    assert.match(you, /kept=\{keptAwardsMine\(userId, boards\.version\)\}/);
    assert.match(you, /askAwardsMine\(userId, boards\.version\)\.then\(/);
    const card = readFileSync(new URL('../webapp/src/components/LeaderboardModal.tsx', import.meta.url), 'utf8');
    assert.match(card, /if \(access === 'linked' && meUserId\) \{\n\s+readyAwardsMine\(meUserId, boards\.version\);/, 'asked as the card opens for a linked reader');
});

check('your achievements: no figures until yours arrive, only the steps the boards say you hold', () => {
    const you = readFileSync(new URL('../webapp/src/components/TrophiesYou.tsx', import.meta.url), 'utf8');
    assert.match(you, /const tier = known \? kind\.tiers\.filter\(\(step\) => value >= step\)\.length : \(held\.find\(\(entry\) => entry\.id === kind\.id\)\?\.tier \|\| 0\);/);
    assert.match(you, /if \(known\) \{\n\s+meta = next > 0/, 'never "0 of 10 hours" before they are known');
});

// ---- A shared plan's card (planLogic.ts) ----

const HOUR = 3600000;
const NOW = Date.UTC(2026, 9, 12, 17, 0);
const ID = '0123456789abcdef';
const post = (props, type = 'custom_villanet_plan') => ({type, message: '📅 Matty plan', props});
const props = (startsAt = NOW + (2 * HOUR)) => ({villanet_plan_id: ID, villanet_plan_starts_at: startsAt});
const plan = (fields = {}) => ({id: ID, title: 'Arma co-op', startsAt: NOW + (2 * HOUR), userId: 'u-m', who: 'Dave', category: 'game', ...fields});
const view = (fields = {}) => plans.planCardView({
    post: post(props()),
    schedule: [plan()],
    liveHandles: {},
    reader: 'linked',
    meUserId: 'u-v',
    now: NOW,
    zone: 'UTC',
    name: 'Matty',
    ...fields,
});

check('plan card: props that are not the plugin’s show the plain message', () => {
    assert.equal(view({post: post({villanet_plan_id: 'nothex', villanet_plan_starts_at: NOW})}).state, 'fallback');
    assert.equal(view({post: post({villanet_plan_id: ID, villanet_plan_starts_at: 'soon'})}).state, 'fallback');
    assert.equal(view({post: post({villanet_plan_id: ID})}).state, 'fallback');
    assert.equal(view({post: post(props(), 'custom_villanet_plan_copy')}).state, 'fallback');
    assert.equal(view({post: post(null)}).state, 'fallback');
    assert.equal(view({post: post(props())}).state, 'upcoming');
});

check('plan card: before the first snapshot, the time from the post and nothing else', () => {
    const waiting = view({schedule: null});
    assert.equal(waiting.state, 'waiting');
    assert.equal(waiting.sentence, 'A plan in Matty');
    assert.equal(waiting.label, `A plan in Matty, ${waiting.day}, 19:00`);
    assert.equal(view({schedule: null, name: 'Movie Night Club'}).sentence, 'A plan in Movie Night Club', 'the server’s name, whatever it is');
    assert.equal(waiting.title, undefined);
    assert.equal(waiting.join, undefined);
    assert.equal(waiting.clock, '19:00');
});

check('plan card: while listed, everything comes from the snapshot, the start included, never the props', () => {
    const moved = view({post: post(props(NOW + (50 * HOUR))), schedule: [plan({title: 'Renamed', startsAt: NOW + (3 * HOUR)})]});
    assert.equal(moved.title, 'Renamed');
    assert.equal(moved.startsAt, NOW + (3 * HOUR));
    assert.equal(moved.clock, '20:00');
    assert.equal(moved.lede, 'in 3 hours');
});

check('plan card: upcoming, due, live; Join for a linked streamer who is not the host', () => {
    const upcoming = view();
    assert.equal(upcoming.state, 'upcoming');
    assert.equal(upcoming.join.kind, 'join');
    assert.equal(upcoming.join.label, 'Join Dave on Arma co-op');
    assert.equal(upcoming.watch, undefined);

    const due = view({now: NOW + (2 * HOUR) + 60000});
    assert.equal(due.state, 'due');
    assert.equal(due.lede, 'Due now');

    const live = view({now: NOW + (2 * HOUR) + 60000, liveHandles: {'u-m': 'h-dave'}});
    assert.equal(live.state, 'live');
    assert.deepEqual(live.watch, {handle: 'h-dave', who: 'Dave', label: 'Watch Dave now'});
    assert.equal(live.join.kind, 'join', 'Join stays while there is room');
});

check('plan card: being on the air speaks only for a plan whose time has come (Up next’s rule)', () => {
    assert.equal(view({liveHandles: {'u-m': 'h-dave'}}).state, 'upcoming');
});

check('plan card: the first live joiner’s stream while the host is not on', () => {
    const joined = [{userId: 'u-a', who: 'Bob'}, {userId: 'u-n', who: 'Erin'}];
    const live = view({schedule: [plan({joined})], now: NOW + (2 * HOUR) + 1, liveHandles: {'u-n': 'h-erin'}});
    assert.equal(live.state, 'live');
    assert.equal(live.watch.handle, 'h-erin');
});

check('plan card: Your plan for the host, In ✓ for one already in, Full at twelve', () => {
    assert.equal(view({meUserId: 'u-m'}).join.kind, 'mine');
    assert.equal(view({schedule: [plan({joined: [{userId: 'u-v', who: 'Victor'}]})]}).join.kind, 'in');
    const twelve = Array.from({length: 12}, (_, index) => ({userId: `u-${index}`, who: `P${index}`}));
    const full = view({schedule: [plan({joined: twelve})]}).join;
    assert.equal(full.kind, 'full');
    assert.equal(full.words, 'Full');
    assert.equal(full.label, 'Arma co-op is full');
    assert.equal(full.title, '12 streamers are in already');

    // The card counts the names it is sent, as it always has: the server's full, which also
    // counts a place held by somebody whose account has gone, is Matty's in this release, and
    // the panel's only once the owner has seen it change the card.
    assert.equal(plans.PLAN_ROOM, 12);
    assert.equal(view({schedule: [plan({joined: twelve.slice(0, 11), full: true})]}).join.kind, 'join');
    assert.equal(view({schedule: [plan({joined: twelve, full: false})]}).join.kind, 'full');

    // Somebody already in a full plan is still shown as in, and may leave.
    assert.equal(view({schedule: [plan({joined: [{userId: 'u-v', who: 'Victor'}].concat(twelve.slice(0, 11))})]}).join.kind, 'in');
});

check('plan card: who is in, in words', () => {
    assert.equal(plans.companyOf([]), '');
    assert.equal(plans.companyOf([{who: 'Bob'}]), 'with Bob');
    assert.equal(plans.companyOf([{who: 'Bob'}, {who: 'Erin'}]), 'with Bob and Erin');
    assert.equal(plans.companyOf([{who: 'Bob'}, {who: 'Erin'}, {who: 'Caro'}, {who: 'Trudy'}]), 'with Bob and 3 others');
});

check('plan card: a let-in reader never gets Join, but keeps In ✓, since leaving is open', () => {
    assert.equal(view({reader: 'letIn'}).join, undefined);
    assert.equal(view({reader: 'letIn', schedule: [plan({joined: [{userId: 'u-v', who: 'Victor'}]})]}).join.kind, 'in');
});

check('plan card: a locked reader sees the card in full, with nothing to press but Watch, and makes no request', () => {
    const locked = view({reader: 'locked', schedule: [plan({joined: [{userId: 'u-v', who: 'Victor'}]})]});
    assert.equal(locked.title, 'Arma co-op', 'in full: an approved departure from 12.1');
    assert.equal(locked.join, undefined, 'no Join and no In ✓: no request to a gated route');
    const live = view({reader: 'locked', now: NOW + (2 * HOUR) + 1, liveHandles: {'u-m': 'h-dave'}});
    assert.equal(live.watch.handle, 'h-dave', 'Watch, which opens the lock');
    assert.equal(view({reader: undefined}).join, undefined, 'unknown reads as locked');
    const card = readFileSync(new URL('../webapp/src/components/PlanPostCard.tsx', import.meta.url), 'utf8');
    assert.match(card, /import \{joinPlan, mediaImageURL\} from '\.\.\/client';/, 'the card asks the server for nothing but a Join pressed, and the open picture route');
});

check('plan card: its label names what the server calls the plugin', () => {
    assert.ok(view().label.startsWith('Matty plan: Arma co-op, Dave, '));
    assert.ok(view({name: 'Movie Night Club'}).label.startsWith('Movie Night Club plan: Arma co-op, Dave, '));
    assert.equal(view({schedule: []}).label, 'Matty plan. This plan was cancelled.');
    assert.equal(plans.planNoPanel('Matty'), 'Open Matty in the main window to watch.');
    assert.equal(view({schedule: [], name: 'Movie Night Club'}).label, 'Movie Night Club plan. This plan was cancelled.');
    assert.equal(plans.planNoPanel('Movie Night Club'), 'Open Movie Night Club in the main window to watch.');
});

check('plan card: off the list, cancelled before its time is up, been and gone after, with no title or names', () => {
    const cancelled = view({schedule: []});
    assert.deepEqual([cancelled.state, cancelled.sentence, cancelled.title, cancelled.host, cancelled.dimmed], ['cancelled', 'This plan was cancelled.', undefined, undefined, true]);
    const ended = view({schedule: [], now: NOW + (5 * HOUR) + 1});
    assert.deepEqual([ended.state, ended.sentence, ended.title], ['ended', 'This stream has been and gone.', undefined]);
    assert.equal(view({schedule: [], now: NOW + (5 * HOUR) - 1}).state, 'cancelled', 'three hours’ grace after the start');
});

check('plan card: the ledes, in the reader’s own zone', () => {
    assert.equal(view({schedule: [plan({startsAt: NOW + (40 * 60000)})]}).lede, 'in 40 min');
    assert.equal(view({schedule: [plan({startsAt: NOW + (3 * HOUR)})]}).lede, 'in 3 hours');
    assert.equal(view({schedule: [plan({startsAt: NOW + (26 * HOUR)})]}).lede, 'tomorrow');
    assert.equal(view({schedule: [plan({startsAt: NOW + (74 * HOUR)})]}).lede, 'in 3 days');
    assert.equal(view({schedule: [plan({startsAt: NOW + (2 * HOUR)})], zone: 'Asia/Tokyo'}).clock, '04:00');
});

console.log(`check-awards: ${checks} checks passed`);
