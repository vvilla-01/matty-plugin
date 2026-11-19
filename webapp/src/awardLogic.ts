import type {AchievementKind, AwardsMine, BesideAward, FanView, HeldAward, Snapshot} from './types';

// What the panel still works out about awards, which since 3.6.0 is very
// little (contract/README.md, "Awards and the boards").
//
// Who holds what, which of it goes beside a name and what a profile says are
// decided on the server (server/awards_decide.go) and arrive decided: beside
// a name in the snapshot's people index, on a card with the card, a profile's
// rows with the boards, and the reader's own awards with GET /awards/mine.
// Until then this file was the rules themselves, and Matty had a second copy
// of half of them.
//
// What is left is the picker's working state (a choice being made is shown
// as it will look before it is saved, by the same rule the server applies
// once it is), the words for an achievement's ladder, and what the toast for
// a new step says.
//
// Imports nothing but types, so build/check-awards.mjs can hold it to its
// rules without a browser.

// The most anybody shows beside their name. Four is the most 3.1.0 ever drew
// (crown, calendar, binoculars, clapper), so with the headphones in front a
// post header is never wider than it is today.
export const MAX_SHOWN = 4;

// Automatic: exactly 3.1.0, the four old awards whenever held, in this order.
// Nothing new ever joins it by itself, past months included.
export const AUTOMATIC: readonly string[] = ['alltime', 'month', 'snooper', 'clipper'];

export type {AwardKind, BesideAward, HeldAward} from './types';

function plural(count: number, one: string, many: string): string {
    return `${count} ${count === 1 ? one : many}`;
}

// A step's name, as everybody reads it: "Streamed 100 hours", "On the podium
// 1 month", "50 thumbs up", "Lo-fi radio: 10 hours". The server's words, sent
// with the family; a step past the last is the last.
export function stepName(kind: AchievementKind, tier: number): string {
    const names = kind.names || [];

    return names[Math.max(0, Math.min(names.length, tier) - 1)] || kind.name;
}

// A step's value in a word, for a ladder: "250 h", "6 months", "100".
export function stepShort(kind: AchievementKind, step: number): string {
    switch (kind.unit) {
    case 'months':
        return plural(step, 'month', 'months');
    case 'thumbs':
        return `${step}`;
    default:
        return `${step} h`;
    }
}

// The head a family is listed under: ON AIR, SNOOPER, PODIUM, CLIPPER, or the
// channel's own name.
export function familyHead(kind: AchievementKind): string {
    switch (kind.id) {
    case 'streamed':
        return 'On air';
    case 'snooped':
        return 'Snooper';
    case 'podium':
        return 'Podium';
    case 'thumbs':
        return 'Clipper';
    default:
        return kind.name;
    }
}

// What a family counts, under its head.
export function familyCounts(kind: AchievementKind): string {
    switch (kind.id) {
    case 'streamed':
        return 'Hours streamed';
    case 'snooped':
        return 'Hours watching others';
    case 'podium':
        return 'Months ended on a podium';
    case 'thumbs':
        return 'Thumbs up on their clips';
    default:
        return 'Hours watching';
    }
}

// What goes beside somebody's name, as the server decided it: their choice,
// or Automatic. Nothing for the great majority of people.
export function besideName(snapshot: Snapshot, userId?: string): BesideAward[] {
    return (userId && snapshot.people?.[userId]?.awards) || [];
}

// Whose clapperboard shows beside "Clipped by", if anybody's: whoever has it
// among the awards by their name, which is the Best Clipper unless they chose
// what shows there and left it out. Beside the name is beside the name (3.5).
export function clapperBeside(snapshot: Snapshot): string | undefined {
    const people = snapshot.people || {};

    return Object.keys(people).find((userId) => (people[userId].awards || []).some((award) => award.kind === 'clipper'));
}

// What a choice would draw beside a name, for the picker, which shows one
// being made before it is saved. The server applies this same rule to the
// choice it stores (awards_decide.go shownAwards), and that is what everybody
// else is sent. Automatic: the old four, whenever held, in their order. A
// choice: the ones chosen that are held now, in the chosen order; one not
// held now is skipped and kept, so it comes back where it was put. Never more
// than four. The headphones are not awards and are not here.
export function shownAwards(held: HeldAward[], choice: string[] | null): HeldAward[] {
    if (choice === null) {
        return AUTOMATIC.
            map((id) => held.find((award) => award.id === id)).
            filter((award): award is HeldAward => Boolean(award));
    }

    const seen = new Set<string>();

    return choice.
        filter((id) => {
            if (seen.has(id)) {
                return false;
            }
            seen.add(id);

            return true;
        }).
        slice(0, MAX_SHOWN).
        map((id) => held.find((award) => award.id === id)).
        filter((award): award is HeldAward => Boolean(award));
}

// What the You tab's picker draws from: this opening's answer from GET
// /awards/mine once it is in (mine); until then, and while it fails, the
// last answer this page had for the reader (kept), drawn as Automatic until
// the choice arrives, as the picker always was while it loaded; and with
// neither, nothing held and, for the preview, what the server already shows
// beside the reader's name (beside). known says whether what they hold is
// known at all: until it is, the picker never says there is nothing to show.
// One list for nothing held, so a render while it loads is not a change.
const NOTHING_HELD: HeldAward[] = [];

export function pickerFrom(mine: AwardsMine | null, kept: AwardsMine | null, draft: string[] | null, beside: BesideAward[]): {held: HeldAward[]; shown: BesideAward[]; known: boolean} {
    const from = mine || kept;
    if (!from) {
        return {held: NOTHING_HELD, shown: beside, known: false};
    }

    return {held: from.held, shown: shownAwards(from.held, draft), known: true};
}

// The awards marked NEW in the picker: held now, not chosen, and not held when
// the picker was last opened in this browser. Nothing the first time a browser
// opens it, so everything held already does not arrive as new.
export function newAwards(held: HeldAward[], seen: string[] | null, choice: string[] | null): string[] {
    if (seen === null) {
        return [];
    }

    return held.
        map((award) => award.id).
        filter((id) => !seen.includes(id) && !(choice || []).includes(id));
}

// What an award not held now says in the picker, kept in a choice.
export const NOT_HELD = 'not yours right now';

// The name of an id somebody chose but does not hold now, for its dimmed chip.
export function nameOfId(id: string, kinds: AchievementKind[], fans: FanView[] | undefined): string {
    switch (id) {
    case 'alltime':
        return 'All-time place';
    case 'month':
        return 'This month’s place';
    case 'snooper':
        return 'Top Snooper';
    case 'clipper':
        return 'Best Clipper';
    case 'past':
        return 'Monthly champion';
    default:
        break;
    }

    if (id.startsWith('fan:')) {
        const fan = (fans || []).find((entry) => entry.streamerId === id.slice(4));

        return fan ? `Biggest fan of ${fan.streamer}` : 'Biggest fan';
    }

    const kind = kinds.find((entry) => `ach:${entry.id}` === id);

    return kind ? familyHead(kind) : id;
}

// What the toasts for new steps say, if anything (design/3.2.0.md 5.4): one
// for every family that has reached a step this browser has not said yet, in
// catalogue order, at the highest step it has reached. Every one is said:
// two families stepping up in one refresh (a few days away from the panel)
// are two toasts, one after the other, not one and a step recorded as said.
// Pure, so build/check-awards.mjs can hold the first-sight rules too.
//
// "seen" is what this browser recorded for this person last time, or null
// the first time. Every family in the catalogue is recorded, at 0 where none
// of its steps is held, and each keeps the highest step ever recorded:
//   - the first sight of somebody says nothing, so backdated steps do not
//     arrive as a burst;
//   - a family never recorded (switched on since, or a new 24/7 channel)
//     says nothing either: it is a first sight of that family;
//   - a figure that dips and comes back (a Twitch disconnect takes hours
//     away) is not news twice.
// The caller asks only once a real snapshot is in hand with achievements
// on; the store's empty first snapshot would otherwise read as "holds
// nothing" and make the first real one look new.
export type StepsSeen = Record<string, number>;

export function stepToSay(
    seen: StepsSeen | null,
    kinds: AchievementKind[],
    have: Array<{id: string; tier: number}>,
): {keep: StepsSeen; says: Array<{id: string; tier: number}>} {
    const keep: StepsSeen = {...(seen || {})};
    const says: Array<{id: string; tier: number}> = [];

    kinds.forEach((kind) => {
        const tier = have.find((entry) => entry.id === kind.id)?.tier || 0;
        if (seen !== null && kind.id in seen && tier > seen[kind.id]) {
            says.push({id: kind.id, tier});
        }
        keep[kind.id] = Math.max(keep[kind.id] || 0, tier);
    });

    return {keep, says};
}
