import {currentChannelId} from './mattermost';
import {markOpenedByHand, openPanel} from './watching';

// Where a plan link takes somebody (design/3.2.0.md 6.7): the panel opened,
// once Mattermost has a channel to open it in, with that plan's Up next card
// scrolled into view and focused. The link lands on PlanLinkRoute, which
// sends Mattermost on to the reader's team at once; the card to focus waits
// here for the panel's Up next to draw it, and for no more than two minutes
// (a locked reader's panel is the lock until they unlock it).

const FOCUS_MS = 120000;

// How long the link waits for Mattermost to have a channel open before it
// stops waiting to open the panel, as a clip link does.
const ARRIVAL_MS = 15000;

type Listener = () => void;

let wanted: {id: string; at: number} | null = null;
let listener: Listener | null = null;
let arrival: (() => void) | null = null;

// One Up next is drawn at a time, so one listener: it is told when a plan is
// waiting to be focused.
export function onPlanFocus(next: Listener | null): void {
    listener = next;
}

// The plan the panel should bring into view, if it is among those listed, and
// forgotten once taken: a link is followed once.
export function takePlanFocus(listed: string[], now = Date.now()): string | null {
    if (!wanted || now - wanted.at > FOCUS_MS) {
        wanted = null;

        return null;
    }
    if (!listed.includes(wanted.id)) {
        return null;
    }

    const id = wanted.id;
    wanted = null;

    return id;
}

type StoreLike = {subscribe: (listener: () => void) => () => void};

// A plan link was followed: the plan waits to be focused, and the panel opens
// as soon as there is a channel to open it in.
export function arriveAtPlan(id: string, store: StoreLike | null): void {
    wanted = {id, at: Date.now()};
    listener?.();

    arrival?.();
    if (!store) {
        return;
    }

    let unsubscribe: (() => void) | null = null;
    let timer = 0;
    const stop = () => {
        window.clearTimeout(timer);
        unsubscribe?.();
        unsubscribe = null;
        arrival = null;
    };
    const check = () => {
        if (currentChannelId() !== '') {
            stop();
            markOpenedByHand();
            openPanel();
        }
    };

    arrival = stop;
    timer = window.setTimeout(stop, ARRIVAL_MS);
    unsubscribe = store.subscribe(check);
    check();
}

// Signing out, or the plugin going: nothing waits to be focused for whoever
// comes next.
export function forgetPlanLink(): void {
    arrival?.();
    wanted = null;
}
