import type {AdminPush, AdminPushHost} from './client';

// The words of System Console > Plugins > Matty > Push notifications > Status
// (components/AdminPush.tsx) and of the push alert in the panel's admin
// alerts (components/AdminAlerts.tsx), worked out from what the server sends
// (GET /api/v1/admin/push, and "me"'s pushAlert). It imports nothing but
// types, so build/check-away.mjs runs it as it is written.
//
// Nothing here is per person: the server sends counts by push server host,
// since which push server somebody uses is theirs to know.

// The section's title in plugin.json, where the card and the switch are.
export const PUSH_SECTION = 'Push notifications';

// A line of the card, and how it is worn: the page's own colour, quieter, or
// the danger red.
export type PushLine = {text: string; tone: 'plain' | 'quiet' | 'error'};

const plural = (n: number, one: string, many: string): string => `${n} ${n === 1 ? one : many}`;

// The server's words as a sentence, ending in a full stop whatever they
// ended with.
function sentence(words: string): string {
    const trimmed = words.trim();

    return /[.!?…]$/.test(trimmed) ? trimmed : `${trimmed}.`;
}

// Whether pushes can go out, in a line or two: the plugin's switch and its
// key, then Mattermost's own push notifications, without which Mattermost
// decides to push to nobody. Mattermost's line names where in the System
// Console it is switched on, in the server's words; while the plugin's
// switch is off it is said quietly, as what switching on would need.
export function pushSwitchLines(view: AdminPush): PushLine[] {
    const lines: PushLine[] = [];
    if (!view.on) {
        lines.push({text: 'Push notifications are off: no Matty can register for them here, and none is sent.', tone: 'plain'});
    } else if (!view.ready) {
        lines.push({text: 'Push notifications are on, but this server’s push key is not ready yet, so no Matty can register. Refresh in a moment.', tone: 'error'});
    } else if (view.mattermost) {
        lines.push({text: 'Push notifications are on: each Matty registered here gets a push whenever Mattermost decides to push to that person.', tone: 'plain'});
    } else {
        lines.push({text: 'Push notifications are on, but none go out, because Mattermost decides to push to nobody.', tone: 'error'});
    }

    if (view.mattermost) {
        lines.push({text: 'Mattermost’s own push notifications are on.', tone: 'quiet'});
    } else {
        lines.push({text: sentence(view.mattermostProblem || 'Mattermost’s own push notifications are not on.'), tone: view.on ? 'error' : 'quiet'});
    }

    return lines;
}

// The head: how many phones of how many people, and how they are doing in
// all. bold is the count, said first and in bold, as the App check card
// does; rest follows it.
export function pushHead(view: AdminPush): {bold: string; rest: string} {
    if (view.devices <= 0) {
        return {bold: '', rest: 'No phone is registered for push notifications here.'};
    }

    const total = {working: 0, waiting: 0, failing: 0, refused: 0};
    for (const row of view.hosts) {
        total.working += row.working;
        total.waiting += row.waiting;
        total.failing += row.failing;
        total.refused += row.refused;
    }
    const parts = [
        total.working > 0 ? `${total.working} working` : '',
        total.waiting > 0 ? `${total.waiting} waiting for their first push` : '',
        total.failing > 0 ? `${total.failing} failing` : '',
        total.refused > 0 ? `${total.refused} refused` : '',
    ].filter(Boolean);

    return {
        bold: plural(view.devices, 'phone', 'phones'),
        rest: ` of ${plural(view.people, 'person', 'people')}${parts.length > 0 ? `: ${parts.join(', ')}` : ''}`,
    };
}

// When the index of people is full, the counts are of the most recent.
export function pushFullLine(view: AdminPush): string {
    return view.full ? 'More than 5000 people have registered a phone: these counts are of the most recent 5000.' : '';
}

// What a push server's row says beside its host: its phones, then each state
// it has any of. since is how the card says when (time.ts postedWhen), passed
// in so this file needs nothing of the page.
export function pushHostParts(row: AdminPushHost, since: (at: number) => string): string[] {
    const parts = [plural(row.devices, 'phone', 'phones')];
    if (row.working > 0) {
        parts.push(`${row.working} working`);
    }
    if (row.waiting > 0) {
        parts.push(`${row.waiting} waiting for the first push`);
    }
    if (row.failing > 0) {
        parts.push(row.failingSince ? `${row.failing} failing since ${since(row.failingSince)}` : `${row.failing} failing`);
    }
    if (row.refused > 0) {
        parts.push(`${row.refused} refused`);
    }

    return parts;
}

// The word on a push server's row: the worst of its states, or none while
// every phone there works or waits.
export function pushHostFlag(row: AdminPushHost): {word: string; state: 'refused' | 'failing'} | null {
    if (row.refused > 0) {
        return {word: 'Refused', state: 'refused'};
    }
    if (row.failing > 0) {
        return {word: 'Failing', state: 'failing'};
    }

    return null;
}

// What refused and failing mean, said once under the rows when any row has
// them, rather than on every row.
export function pushTroubleLines(view: AdminPush): string[] {
    const lines: string[] = [];
    if (view.hosts.some((row) => row.refused > 0)) {
        lines.push('Refused: that push server answered that this server may not send there, so those phones are tried with one of their pushes an hour at most, and work again by themselves once one goes through. An ntfy server needs anonymous publishing allowed to topics that start with “up”.');
    }
    if (view.hosts.some((row) => row.failing > 0)) {
        lines.push('Failing: that push server could not be reached, or did not take the pushes. After ten failures in a row, or an hour of nothing else with at least three, a phone counts as failing (until then, as working), is tried with one of its pushes at most every ten minutes, and works again by itself once one goes through.');
    }

    return lines;
}

// The server's words for why a push was not sent (server/push_send.go), as
// the card says them. "queue full" is a phone's queue, or all of a node's
// together.
export const PUSH_DROPPED_WORDS: Record<string, string> = {
    'other plugin': 'made by another plugin, such as Calls',
    'queue full': 'more than the queues hold',
    'too many for one phone': 'too many at once for one phone',
    'too many waiting to try again': 'too many waiting to be tried again',
    'switched off': 'waiting when push notifications were switched off',
    store: 'the push records could not be read',
};

// The pushes the answering server did not send since the plugin last
// started there, by why, most first; a word the card does not know is said
// as the server sent it. Only that server's: in a cluster, each counts its
// own.
export function pushDroppedLine(view: AdminPush): string {
    const counts = Object.entries(view.dropped || {}).filter(([, n]) => typeof n === 'number' && n > 0);
    if (counts.length === 0) {
        return '';
    }
    counts.sort((a, b) => (b[1] - a[1]) || a[0].localeCompare(b[0]));

    const parts = counts.map(([why, n]) => `${n} ${PUSH_DROPPED_WORDS[why] || why}`);

    return `Not sent since the plugin last started on this server: ${parts.join('; ')}.`;
}

// The panel's alert for a system admin: push notifications are switched on
// and Mattermost decides to push to nobody, in the server's words. '' when
// "me" says nothing.
export function pushAlertText(alert: string | undefined): string {
    const words = (alert || '').trim();

    return words === '' ? '' : `Push notifications need attention: ${sentence(words)}`;
}
