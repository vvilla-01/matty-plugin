import {shownAwards} from '../src/awardLogic';
import type {AchievementHolder, AchievementKind, AwardRow, BesideAward, BestClipper, FanView, HeldAward, Leader, MonthMedals} from '../src/types';

// The lab's stand-in for server/awards_decide.go: who holds what, what goes
// beside a name and on a card, and what a profile says.
//
// The panel decides none of this since 3.6.0: it is sent decided, and draws
// it. The lab has no server, so its world (world.ts) needs these rules to
// make what the server would have sent. They are the rules the panel itself
// applied until then, and build/check-awards.mjs holds them to the cases the
// server's own tests hold the Go to (awards_decide_test.go), so the lab does
// not come to show what a server never would. Nothing in webapp/src imports
// this file.

// The boards and lists the rules are asked of, as the lab's world has them.
export type AwardFacts = {
    allTime: Leader[];
    month: Leader[];
    monthLabel: string;
    watchAllTime: Leader[];
    bestClipper?: BestClipper;
    monthMedals: MonthMedals[];

    // Empty while biggest fans are off.
    fans: FanView[];
    kinds: AchievementKind[];
    achievements: AchievementHolder[];

    // Who chose what shows beside their name. No entry is Automatic.
    choices: Record<string, string[]>;
};

// A Live now card's own places on the two airtime boards.
export type CardPlaces = {allTime: number; month: number};

const TITLES: Record<number, string> = {1: 'Champion', 2: 'Runner-up', 3: 'Third place'};

function ordinal(rank: number): string {
    switch (rank) {
    case 1:
        return '1st';
    case 2:
        return '2nd';
    case 3:
        return '3rd';
    default:
        return `${rank}th`;
    }
}

function counted(count: number, one: string, many: string): string {
    return `${count} ${count === 1 ? one : many}`;
}

// A step's name, in the server's words (stepName in awards_decide.go).
export function stepNameOf(kind: Omit<AchievementKind, 'names'>, tier: number): string {
    const step = kind.tiers[Math.max(0, Math.min(kind.tiers.length, tier) - 1)] || 0;

    switch (kind.id) {
    case 'streamed':
        return `Streamed ${counted(step, 'hour', 'hours')}`;
    case 'snooped':
        return `Snooped ${counted(step, 'hour', 'hours')}`;
    case 'podium':
        return `On the podium ${counted(step, 'month', 'months')}`;
    case 'thumbs':
        return `${step} thumbs up`;
    default:
        return `${kind.name}: ${counted(step, 'hour', 'hours')}`;
    }
}

// A family as the boards carry it: with what each of its steps is called.
export function namedKind(kind: Omit<AchievementKind, 'names'>): AchievementKind {
    return {...kind, names: kind.tiers.map((_, index) => stepNameOf(kind, index + 1))};
}

function place(rows: Leader[], userId: string): number {
    const row = rows.find((entry) => entry.userId === userId);

    return row && row.rank >= 1 && row.rank <= 3 ? row.rank : 0;
}

function topSnooper(facts: AwardFacts): Leader | undefined {
    return facts.watchAllTime.find((row) => row.rank === 1);
}

function newestFirst(months: string[] | undefined): string[] {
    return Array.from(new Set(months || [])).sort().reverse();
}

// Everything somebody holds now, in one order: the four old awards, the past
// months, the fan titles, then the achievements in catalogue order.
export function heldOf(facts: AwardFacts, userId: string, card?: CardPlaces): HeldAward[] {
    const held: HeldAward[] = [];
    if (!userId) {
        return held;
    }

    const allTime = card ? card.allTime : place(facts.allTime, userId);
    if (allTime) {
        held.push({id: 'alltime', kind: 'alltime', rank: allTime, label: `${ordinal(allTime)} for airtime, all time`, short: `${ordinal(allTime)}, all time`});
    }

    const month = card ? card.month : place(facts.month, userId);
    if (month) {
        held.push({id: 'month', kind: 'month', rank: month, label: `${ordinal(month)} for airtime this month`, short: `${ordinal(month)} this month`});
    }

    if (topSnooper(facts)?.userId === userId) {
        held.push({id: 'snooper', kind: 'snooper', label: 'Top Snooper, all time', short: 'Top Snooper'});
    }

    if (facts.bestClipper?.userId === userId) {
        held.push({id: 'clipper', kind: 'clipper', label: 'Best Clipper', short: 'Best Clipper'});
    }

    const medals = facts.monthMedals.find((entry) => entry.userId === userId);
    if (medals) {
        const gold = new Set(medals.gold || []).size;
        const silver = new Set(medals.silver || []).size;
        const bronze = new Set(medals.bronze || []).size;
        const months = gold + silver + bronze;
        if (months > 0) {
            let best = 3;
            if (gold > 0) {
                best = 1;
            } else if (silver > 0) {
                best = 2;
            }
            const title = gold > 0 ? 'Monthly champion' : 'Monthly podium';
            held.push({
                id: 'past',
                kind: 'past',
                rank: best,
                wins: gold || undefined,
                months,
                label: gold > 0 ? `${title}, ${counted(gold, 'month', 'months')}` : title,
                short: gold >= 2 ? `${title} ×${gold}` : title,
            });
        }
    }

    facts.fans.
        filter((fan) => fan.fanId === userId).
        slice().
        sort((a, b) => (a.streamerId < b.streamerId ? -1 : 1)).
        forEach((fan) => {
            held.push({
                id: `fan:${fan.streamerId}`,
                kind: 'fan',
                streamer: fan.streamer,
                hours: fan.hours,
                label: `Biggest fan of ${fan.streamer}`,
                short: `Biggest fan of ${fan.streamer}`,
            });
        });

    const mine = facts.achievements.find((holder) => holder.userId === userId);
    facts.kinds.forEach((kind) => {
        const tier = mine?.have.find((entry) => entry.id === kind.id)?.tier || 0;
        if (tier < 1) {
            return;
        }

        const name = stepNameOf(kind, tier);
        held.push({id: `ach:${kind.id}`, kind: 'ach', family: kind.id, tier, label: name, short: name});
    });

    return held;
}

// Beside a name the clapper says "Best Clipper, all time", as the server
// sends it (awards_decide.go), where its chip in the picker says "Best
// Clipper"; the panel words the clapper itself either way (AwardMark.tsx).
function beside(shown: HeldAward[]): BesideAward[] {
    return shown.map(({id, kind, rank, family, tier, label}) => ({id, kind, rank, family, tier, label: kind === 'clipper' ? 'Best Clipper, all time' : label}));
}

function choiceOf(facts: AwardFacts, userId: string): string[] | null {
    return userId in facts.choices ? facts.choices[userId].slice(0, 4) : null;
}

// What goes by somebody's name wherever it appears.
export function besideOf(facts: AwardFacts, userId: string): BesideAward[] {
    return beside(shownAwards(heldOf(facts, userId), choiceOf(facts, userId)));
}

// A Live now card's set. A person who chose gets their choice, with the
// card's own places standing in for the boards'. Everybody else, and a card
// with no person, keep 3.1.0's card set: the crown and the calendar at the
// card's places, and the binoculars on the Top Snooper's own Broadcast Box
// card; never the clapper. A 24/7 channel has none.
export function onCardOf(facts: AwardFacts, stream: {alwaysOn?: boolean; userId?: string; source?: string}, places: CardPlaces): BesideAward[] {
    if (stream.alwaysOn) {
        return [];
    }

    const userId = stream.userId || '';
    const choice = userId ? choiceOf(facts, userId) : null;
    if (choice !== null) {
        return beside(shownAwards(heldOf(facts, userId, places), choice));
    }

    const set: BesideAward[] = [];
    if (places.allTime) {
        set.push({id: 'alltime', kind: 'alltime', rank: places.allTime, label: `${ordinal(places.allTime)} for airtime, all time`});
    }
    if (places.month) {
        set.push({id: 'month', kind: 'month', rank: places.month, label: `${ordinal(places.month)} for airtime this month`});
    }
    if (userId && !stream.source && topSnooper(facts)?.userId === userId) {
        set.push({id: 'snooper', kind: 'snooper', label: 'Top Snooper, all time'});
    }

    return set;
}

// Somebody's whole record, as their profile shows it, whatever they chose to
// show beside their name.
export function rowsOf(facts: AwardFacts, userId: string): AwardRow[] {
    const rows: AwardRow[] = [];
    if (!userId) {
        return rows;
    }

    const podium = (list: Leader[]) => {
        const row = list.find((entry) => entry.userId === userId);

        return row && row.rank >= 1 && row.rank <= 3 ? row : undefined;
    };

    const allTime = podium(facts.allTime);
    if (allTime) {
        rows.push({kind: 'board', id: 'alltime', rank: allTime.rank, title: TITLES[allTime.rank], board: 'All time', seconds: allTime.seconds});
    }

    const month = podium(facts.month);
    if (month) {
        rows.push({kind: 'board', id: 'month', rank: month.rank, title: TITLES[month.rank], board: facts.monthLabel || 'This month', seconds: month.seconds});
    }

    const snooper = topSnooper(facts);
    if (snooper && snooper.userId === userId) {
        rows.push({kind: 'board', id: 'snooper', rank: 1, title: 'Top Snooper', board: 'All time', seconds: snooper.seconds, doing: 'watching others'});
    }

    if (facts.bestClipper?.userId === userId) {
        rows.push({kind: 'clipper', id: 'clipper', rank: 1, title: 'Best Clipper', upvotes: facts.bestClipper.upvotes, clips: facts.bestClipper.clips});
    }

    facts.fans.
        filter((fan) => fan.fanId === userId).
        slice().
        sort((a, b) => (b.hours - a.hours) || a.streamer.localeCompare(b.streamer, undefined, {sensitivity: 'base'}) || (a.streamerId < b.streamerId ? -1 : Number(a.streamerId > b.streamerId))).
        forEach((fan) => rows.push({
            kind: 'fan',
            id: `fan:${fan.streamerId}`,
            rank: 1,
            title: `Biggest fan of ${fan.streamer}`,
            streamerId: fan.streamerId,
            streamer: fan.streamer,
            hours: fan.hours,
        }));

    const mine = facts.achievements.find((holder) => holder.userId === userId);
    const steps = facts.kinds.
        map((kind) => {
            const tier = mine?.have.find((entry) => entry.id === kind.id)?.tier || 0;

            return {id: kind.id, tier, name: tier > 0 ? stepNameOf(kind, tier) : ''};
        }).
        filter((step) => step.tier > 0);
    if (steps.length > 0) {
        rows.push({kind: 'achievements', id: 'achievements', rank: 0, title: 'Achievements', steps});
    }

    const medals = facts.monthMedals.find((entry) => entry.userId === userId);
    if (medals) {
        const gold = newestFirst(medals.gold);
        const tallies = [
            {rank: 2, months: newestFirst(medals.silver)},
            {rank: 3, months: newestFirst(medals.bronze)},
        ].filter((tally) => tally.months.length > 0);

        if (gold.length > 0) {
            rows.push({kind: 'months', id: 'past', rank: 1, title: 'Monthly champion', gold, tallies: tallies.length > 0 ? tallies : undefined});
        } else if (tallies.length > 0) {
            rows.push({kind: 'months', id: 'past', rank: tallies[0].rank, title: 'Monthly podium', tallies});
        }
    }

    return rows;
}

// Everybody who holds anything at all, sorted.
export function holdersOf(facts: AwardFacts): string[] {
    const seen = new Set<string>();
    const note = (userId?: string) => {
        if (userId) {
            seen.add(userId);
        }
    };

    facts.allTime.forEach((row) => note(row.userId));
    facts.month.forEach((row) => note(row.userId));
    note(topSnooper(facts)?.userId);
    note(facts.bestClipper?.userId);
    facts.monthMedals.forEach((entry) => note(entry.userId));
    facts.fans.forEach((fan) => note(fan.fanId));
    facts.achievements.forEach((holder) => note(holder.userId));

    return Array.from(seen).sort();
}
