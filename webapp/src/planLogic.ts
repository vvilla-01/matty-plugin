import {clockTime, dayDate, planLede} from './time';
import type {ScheduleItem} from './types';

// ---- A shared plan's card (design/3.2.0.md 6.5) ----
//
// What the card for a post of the plugin's plan type shows, worked out from the
// post and the snapshot, with nothing else: pure, so build/check-awards.mjs
// can hold it to the contract's table.
//
// The post carries only the plan's id and its start (6.3): anybody could have
// made a post of this type while the plugin was not running, so nothing else is
// read from it. While the plan is on snapshot.schedule, everything comes from
// there: title, host, joiners, time, category, art. Once it is not, the card
// names nothing at all, so a title the host has since changed or taken back
// never outlives them in somebody else's post.

export const PLAN_POST_TYPE = 'custom_villanet_plan';

// The schedule's own limits (server/community.go): ids are 16 hex, a plan
// stays listed for three hours after its start, and twelve may join one.
const PLAN_ID = /^[0-9a-f]{16}$/;
export const PLAN_GRACE_MS = 3 * 3600000;
export const PLAN_ROOM = 12;

// The sentences that name the plugin take the server's name for it (name.ts),
// handed over by the caller: this file imports nothing of the page's.
export const planWaiting = (name: string): string => `A plan in ${name}`;
export const PLAN_CANCELLED = 'This plan was cancelled.';
export const PLAN_ENDED = 'This stream has been and gone.';
export const planNoPanel = (name: string): string => `Open ${name} in the main window to watch.`;

export type PlanCardState = 'fallback' | 'waiting' | 'upcoming' | 'due' | 'live' | 'ended' | 'cancelled';

// Who is reading, as identity.ts says: undefined until it is known, which the
// card treats as locked (nothing to press but Watch, which then checks).
export type PlanReader = 'linked' | 'letIn' | 'locked' | undefined;

export type PlanCardInput = {
    post: {type?: string; message?: string; props?: unknown};

    // snapshot.schedule, or null before the first snapshot has arrived.
    schedule: ScheduleItem[] | null;

    // Who is on the air, by user id, with the handle to watch them by
    // (the snapshot's people index, read by twitchLogic.ts liveHandlesOf): never a 24/7 channel.
    liveHandles: Record<string, string>;
    reader: PlanReader;
    meUserId?: string;
    now: number;
    zone: string | undefined;

    // What this server calls the plugin (name.ts), for the card's words.
    name: string;
};

export type PlanPerson = {userId?: string; who: string};

// The one control beside Watch, if any: Join, In ✓ (leave), "Your plan" for
// the host, or Full.
export type PlanJoin = {kind: 'join' | 'in' | 'mine' | 'full'; words: string; label: string; title: string};

export type PlanCardView = {
    state: PlanCardState;
    id: string;

    // The post's message: all the fallback shows.
    message: string;
    startsAt: number;

    // From the snapshot, while the plan is listed; absent otherwise.
    title?: string;
    category?: string;
    art?: string;
    artTitle?: string;
    host?: PlanPerson;
    joined: PlanPerson[];

    // "with Bob and Erin"; '' with nobody.
    company: string;

    // "Fri 17 Oct", "20:00", and "in 3 days" (or "Due now"; '' while live).
    day: string;
    clock: string;
    lede: string;

    // The one sentence a waiting or gone card says.
    sentence?: string;
    dimmed: boolean;

    // Watch, while somebody in it is on the air: whose stream it opens.
    watch?: {handle: string; who: string; label: string};
    join?: PlanJoin;

    // The card's aria-label.
    label: string;
};

// The post's two props, or null when either is not what the plugin writes.
function planPropsOf(post: PlanCardInput['post']): {id: string; startsAt: number} | null {
    if (post.type !== PLAN_POST_TYPE) {
        return null;
    }

    const props = post.props;
    if (typeof props !== 'object' || props === null || Array.isArray(props)) {
        return null;
    }

    const read = props as Record<string, unknown>;
    const id = read.villanet_plan_id;
    const startsAt = read.villanet_plan_starts_at;
    if (typeof id !== 'string' || !PLAN_ID.test(id) || typeof startsAt !== 'number' || !Number.isFinite(startsAt) || startsAt <= 0) {
        return null;
    }

    return {id, startsAt};
}

// "with Bob", "with Bob and Erin", "with Bob and 3 others".
export function companyOf(joined: PlanPerson[]): string {
    if (joined.length === 0) {
        return '';
    }
    if (joined.length === 1) {
        return `with ${joined[0].who}`;
    }
    if (joined.length === 2) {
        return `with ${joined[0].who} and ${joined[1].who}`;
    }

    return `with ${joined[0].who} and ${joined.length - 1} others`;
}

export function planCardView(input: PlanCardInput): PlanCardView {
    const message = typeof input.post.message === 'string' ? input.post.message : '';
    const props = planPropsOf(input.post);
    const blank = {
        id: '',
        message,
        startsAt: 0,
        joined: [],
        company: '',
        day: '',
        clock: '',
        lede: '',
        dimmed: false,
        label: '',
    };
    if (!props) {
        return {...blank, state: 'fallback'};
    }

    const base = {
        ...blank,
        id: props.id,
        startsAt: props.startsAt,
        day: dayDate(props.startsAt, input.zone),
        clock: clockTime(props.startsAt, input.zone),
    };

    // Before the first snapshot: the time from the post, and nothing else.
    if (input.schedule === null) {
        const waiting = planWaiting(input.name);

        return {...base, state: 'waiting', sentence: waiting, label: `${waiting}, ${base.day}, ${base.clock}`};
    }

    const plan = input.schedule.find((item) => item.id === props.id);
    if (!plan) {
        const ended = input.now >= props.startsAt + PLAN_GRACE_MS;
        const sentence = ended ? PLAN_ENDED : PLAN_CANCELLED;

        return {...base, state: ended ? 'ended' : 'cancelled', sentence, dimmed: true, label: `${input.name} plan. ${sentence}`};
    }

    // Listed: the snapshot's word for everything, the start included (an
    // edit may have moved it).
    const startsAt = plan.startsAt;
    const joined = plan.joined || [];
    const host: PlanPerson = {userId: plan.userId, who: plan.who};
    const started = startsAt <= input.now;

    // On the air once its time has come: the host, or else the first joiner
    // who is (Up next's rule: being on air speaks only for a plan whose time
    // has come).
    let watch: PlanCardView['watch'];
    if (started) {
        const live = [host, ...joined].find((person) => person.userId && input.liveHandles[person.userId]);
        if (live?.userId) {
            watch = {handle: input.liveHandles[live.userId], who: live.who, label: `Watch ${live.who} now`};
        }
    }

    let state: PlanCardState = 'upcoming';
    if (watch) {
        state = 'live';
    } else if (started) {
        state = 'due';
    }

    // Join by today's rules (V17): linked, not the host, room for twelve.
    // Leaving is open, so In ✓ shows to anybody who is in but a locked
    // reader, whose card offers nothing but Watch (6.2).
    const mine = Boolean(input.meUserId && plan.userId === input.meUserId);
    const amIn = Boolean(input.meUserId && joined.some((person) => person.userId === input.meUserId));
    let join: PlanJoin | undefined;
    if (input.reader === 'linked' || input.reader === 'letIn') {
        if (mine) {
            join = {kind: 'mine', words: 'Your plan', label: 'Your plan', title: 'You planned this one'};
        } else if (amIn) {
            join = {kind: 'in', words: 'In ✓', label: `You are in on ${plan.title}. Leave it`, title: 'You are in — press to leave'};
        } else if (input.reader === 'linked' && joined.length >= PLAN_ROOM) {
            join = {kind: 'full', words: 'Full', label: `${plan.title} is full`, title: `${PLAN_ROOM} streamers are in already`};
        } else if (input.reader === 'linked') {
            join = {kind: 'join', words: 'Join', label: `Join ${plan.who} on ${plan.title}`, title: 'Say you are streaming in this too'};
        }
    }

    let lede = '';
    if (state === 'due') {
        lede = 'Due now';
    } else if (state === 'upcoming') {
        lede = planLede(startsAt, input.zone, input.now);
    }

    const company = companyOf(joined);
    const day = dayDate(startsAt, input.zone);
    const clock = clockTime(startsAt, input.zone);
    const when = lede === '' ? `${day}, ${clock}` : `${day}, ${clock}, ${lede}`;

    // A locked reader sees the card in full (6.2, an approved departure from
    // 12.1's "no title"): plans are already shown to everybody in profile
    // popovers, as clip cards are. They get nothing to press but Watch, which
    // opens the lock with the stream waiting.
    return {
        ...base,
        state,
        startsAt,
        day,
        clock,
        lede,
        title: plan.title,
        category: plan.category,
        art: plan.media?.ref,
        artTitle: plan.media?.title,
        host,
        joined,
        company,
        watch,
        join,
        label: `${input.name} plan: ${plan.title}, ${plan.who}${company ? ` ${company}` : ''}, ${state === 'live' ? 'live now' : when}`,
    };
}
