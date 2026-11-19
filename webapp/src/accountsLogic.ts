import type {AccountsPerson, AccountsSteam, AccountsView, MeAccount, MeTwitch} from './types';

// Steam and Hytale in the panel, worked out without a browser
// (design/3.4.0.md 5): the exact words (5.8); which rows of Connected
// accounts a reader gets and what each says; the owner's own Steam status;
// the rows in somebody's profile popover and the Playing now line; what the
// Disconnect line of somebody not linked says for every set of connections;
// the takeover notices in the order they show; and when Waiting gives its
// hints and gives up.
//
// Imports nothing but types, which the compiler drops, so
// build/check-accounts.mjs can transpile it on its own and run it, as
// check-twitch.mjs does with twitchLogic.ts. Twitch's own rules stay in
// twitchLogic.ts; Twitch is here only as one row of the list and one name on
// the Disconnect line.

export type Service = 'steam' | 'hytale';

// Twitch is one more wherever every connection is listed (the rows, the
// Disconnect line, the notices), and always first: it came first, and the
// line reads the same each time.
export type AnyService = 'twitch' | Service;

export const SERVICE_NAME: Record<AnyService, string> = {
    twitch: 'Twitch',
    steam: 'Steam',
    hytale: 'Hytale',
};

const ORDER: AnyService[] = ['twitch', 'steam', 'hytale'];

// ---- The words (5.8), exactly ----
//
// A sentence that names the plugin takes the server's name for it (name.ts
// nameNow or useName), handed over by the caller: this file imports nothing
// of the page's.

export const CONSENT_HEAD: Record<Service, string> = {
    steam: 'Connect your Steam account?',
    hytale: 'Connect your Hytale account?',
};

export function consentNote(service: Service, name: string): string {
    if (service === 'steam') {
        return `Everyone who can open ${name} here sees your Steam name, whether Steam says you are online, busy or away, and the game you are playing (Steam shares the game only if your profile and its game details are public). ${name} keeps your Steam ID and name, never a password, and asks Steam about you once a minute while you stay connected. Disconnect removes it all. Continuing opens Steam’s own sign-in page.`;
    }

    return `Everyone who can open ${name} here sees the Hytale username you pick, so friends can find you in-game. Hytale tells ${name} nothing else: not whether you are playing, nor where. ${name} keeps that username and its profile id, never a password or token; if you rename the profile, the old name shows until you connect again. Disconnect removes it. Continuing opens Hytale’s own sign-in page.`;
}

// The panel's own plain button, not Valve's image.
export const CONTINUE_ON: Record<Service, string> = {
    steam: 'Continue on Steam',
    hytale: 'Continue on Hytale',
};

export const GETTING_READY = 'Getting ready…';

export const ROW_PROMPT: Record<AnyService, string> = {
    twitch: 'Show when you are live on Twitch',
    steam: 'Show your Steam status here',
    hytale: 'Show your Hytale name here',
};

export function justDisconnected(service: Service, name: string): string {
    if (service === 'steam') {
        return `Disconnected. ${name} has deleted your Steam ID and name, and asks Steam nothing more about you.`;
    }

    return `Disconnected. ${name} has deleted your Hytale username and profile id. It kept no Hytale token, so it holds nothing else.`;
}

export function disconnectFailed(service: AnyService): string {
    return `Could not disconnect ${SERVICE_NAME[service]}. Try again in a moment.`;
}

// The first press's words: what the second press does. Never cut short, so
// they always have a line of their own.
export function armedWords(service: AnyService): string {
    return `Disconnect ${SERVICE_NAME[service]}? Press again`;
}

export const PRIVATE_ROW = 'Private: people here see you as offline';
export const PRIVATE_OWNER = 'Your Steam profile is private, so people here see you as offline.';

// The relink key's sheet while account rows show (Victor's P1 pick, B): the
// list's heading, the sheet's head, and the key's name and tooltip then.
// Connect and Connected, never "link": that word is the panel's for
// unlocking with a bearer token, and a connection unlocks nothing.
export const LIST_HEADING = 'Connected accounts';
export const SHEET_HEAD_WITH_ACCOUNTS = 'Your stream and accounts';
export const SHEET_HEAD_WITHOUT = 'Relink your stream';

export function relinkKeyWithAccounts(key: string): string {
    return `Linked as ${key}. Your stream and connected accounts.`;
}

// A service's flow, in the sheet's place: its head, and the arrow back.
export function flowHeading(service: AnyService, connected: boolean): string {
    return connected ? SERVICE_NAME[service] : `Connect ${SERVICE_NAME[service]}`;
}

export const BACK_TO_LIST = 'Back to connected accounts';

export function waitingWords(service: Service): string {
    return `Finish on ${SERVICE_NAME[service]} in the window that opened.`;
}

export function noWindowWords(service: Service): string {
    return `No window? Allow pop-ups for this site, then press Connect ${SERVICE_NAME[service]} again.`;
}

export const HYTALE_REDIRECT_HINT = 'If Hytale showed an error page instead, an admin needs to check the application’s Redirect URI and scopes.';

export function unavailableWords(service: AnyService, problem: string | undefined): string {
    const name = SERVICE_NAME[service];

    return `Connect ${name} is not available: ${problem || `${name} is not set up on this server yet.`}`;
}

export function switchedOffWords(service: AnyService): string {
    return `(${SERVICE_NAME[service]} is switched off here)`;
}

// ---- Which rows a linked reader gets (5.2, 5.3) ----

// Whether a service has a row, by the rule the Twitch tool had: never while
// an admin has blocked the reader from it; otherwise while connecting is
// possible or something is connected, and for an admin also while it is
// not, to read why.
export function accountRowShown(account: MeAccount | undefined, admin: boolean): boolean {
    if (!account || account.blocked) {
        return false;
    }

    return account.available || Boolean(account.connected) || admin;
}

export function twitchRowShown(twitch: MeTwitch | undefined, admin: boolean): boolean {
    if (!twitch || twitch.blocked) {
        return false;
    }

    return twitch.available || Boolean(twitch.login) || admin;
}

type MeLike = {
    access?: string;
    isAdmin?: boolean;
    twitch?: MeTwitch;
    steam?: MeAccount;
    hytale?: MeAccount;
};

// The rows of Connected accounts, in their order. Only for somebody linked:
// connecting is linked-only, and the list lives in the relink key's sheet,
// which nobody else has.
export function accountRowsFor(me: MeLike | null, access: string): AnyService[] {
    if (!me || access !== 'linked') {
        return [];
    }

    const admin = Boolean(me.isAdmin);
    const shown: Record<AnyService, boolean> = {
        twitch: twitchRowShown(me.twitch, admin),
        steam: accountRowShown(me.steam, admin),
        hytale: accountRowShown(me.hytale, admin),
    };

    return ORDER.filter((service) => shown[service]);
}

// ---- The owner's own Steam status ----

// What the reader's own Steam row says after the name: what everybody else
// sees, from the same view. '' while Steam has not been read lately: names
// are shown, statuses never guessed.
export function steamStatusWords(steam: AccountsSteam | undefined): string {
    if (!steam) {
        return '';
    }
    if (steam.inGame) {
        return steam.game ? `Playing ${steam.game}` : 'In a game';
    }

    switch (steam.state) {
    case 'online':
        return 'Online';
    case 'busy':
        return 'Busy';
    case 'away':
        return 'Away';
    case 'offline':
        return 'Offline';
    default:
        return '';
    }
}

// ---- One row at rest, as words ----

export type RowTone = '' | 'live' | 'private' | 'muted' | 'problem';

export type RowView = {
    service: AnyService;
    connected: boolean;

    // The account's name, while connected; '' otherwise.
    name: string;

    // What follows it: the status, the prompt, or why Connect is not there.
    sub: string;
    tone: RowTone;

    // Connect is not available, for an admin: the reason and no button.
    unavailable: boolean;

    // The row's tooltip, which holds what a narrow row cuts short.
    title: string;
};

export function accountRowView(service: Service, account: MeAccount, steam?: AccountsSteam): RowView {
    const connected = Boolean(account.connected);
    const name = connected ? account.name || '' : '';
    if (!connected && !account.available) {
        return {service, connected, name, sub: unavailableWords(service, account.problem), tone: 'problem', unavailable: true, title: ''};
    }
    if (!connected) {
        return {
            service,
            connected,
            name,
            sub: ROW_PROMPT[service],
            tone: '',
            unavailable: false,
            title: service === 'steam' ? 'Steam: not connected. Connect your Steam account so people here see when you are playing.' : '',
        };
    }

    let sub = '';
    let tone: RowTone = '';
    if (!account.available) {
        sub = switchedOffWords(service);
        tone = 'muted';
    } else if (service === 'steam' && account.hidden) {
        sub = PRIVATE_ROW;
        tone = 'private';
    } else if (service === 'steam') {
        sub = steamStatusWords(steam);
    }

    return {
        service,
        connected,
        name,
        sub,
        tone,
        unavailable: false,
        title: service === 'steam' ?
            `Steam: connected as ${name}` :
            `Hytale: connected as ${name}. Friends can copy it to find you in-game.`,
    };
}

// Twitch's row at rest, the Twitch tool's words moved onto a row: its live
// dot is "Live now" on the row, in red.
export function twitchRowView(twitch: MeTwitch): RowView {
    const login = twitch.login || '';
    const connected = login !== '';
    const name = connected ? twitch.displayName || login : '';
    if (!connected && !twitch.available) {
        return {service: 'twitch', connected, name, sub: unavailableWords('twitch', twitch.problem), tone: 'problem', unavailable: true, title: ''};
    }
    if (!connected) {
        return {
            service: 'twitch',
            connected,
            name,
            sub: ROW_PROMPT.twitch,
            tone: '',
            unavailable: false,
            title: 'Twitch: not connected. Connect your Twitch account so you show as live here when you stream on Twitch.',
        };
    }

    const live = Boolean(twitch.live);
    let sub = '';
    let tone: RowTone = '';
    if (live) {
        sub = 'Live now';
        tone = 'live';
    } else if (!twitch.available) {
        sub = switchedOffWords('twitch');
        tone = 'muted';
    }

    return {
        service: 'twitch',
        connected,
        name,
        sub,
        tone,
        unavailable: false,
        title: `Twitch: connected as ${name} (twitch.tv/${login})${live ? ' · live now' : ''}`,
    };
}

// Twitch's own warning, on its Disconnect wherever it is: the row, and the
// Disconnect line of somebody not linked.
export function twitchDisconnectTitle(name: string, brand: string): string {
    return `Your Twitch account, ${name}, is connected to ${brand}. Disconnecting removes it and your Twitch hours.`;
}

// ---- Other people: who may see, the popover's rows, Playing now (5.4) ----

// Only linked readers and readers let in to watch see anybody's Steam status
// or Hytale name, outside the panel too: a locked reader is never shown
// any, and never asks (Victor's answer, 2026-11-11).
export function accountsVisible(access: string): boolean {
    return access === 'linked' || access === 'letIn';
}

export type PopoverRow = {
    service: Service;
    title: string;
    meta: string;

    // Offline, private and not fresh are one thing to everybody else: the
    // name, dimmed.
    dim: boolean;

    // The whole row in words, for its tooltip: a long game is cut short.
    full: string;

    // Steam: the Steam name, which names the profile pill, and the profile's
    // address, '' (and no pill) for anything but Steam's own profile page.
    // Hytale: the username, which Copy copies, and no address.
    name: string;
    url: string;
};

const STATE_WORDS: Record<string, string> = {online: 'Online', busy: 'Busy', away: 'Away'};

// In a game, the title is the game alone, the one fact the row is there
// for, and the meta "On Steam · {name}", so the name fits beside the profile
// pill in Mattermost's 240 px popover (Victor's pick, Q8 · 2). "Steam" is
// always written, so the row is never taken for Mattermost's own status
// dot.
export function steamPopoverRow(steam: AccountsSteam): PopoverRow {
    const pill = {name: steam.name, url: steamProfileURL(steam.url)};

    if (steam.inGame) {
        const game = (steam.game || '').trim();
        if (game !== '') {
            return {service: 'steam', title: game, meta: `On Steam · ${steam.name}`, dim: false, full: `Playing ${game} on Steam · ${steam.name}`, ...pill};
        }

        return {service: 'steam', title: 'In a game', meta: `On Steam · ${steam.name}`, dim: false, full: `In a game on Steam · ${steam.name}`, ...pill};
    }

    const word = STATE_WORDS[steam.state];
    if (word) {
        return {service: 'steam', title: word, meta: `On Steam · ${steam.name}`, dim: false, full: `${word} on Steam · ${steam.name}`, ...pill};
    }

    return {service: 'steam', title: steam.name, meta: 'Steam', dim: true, full: `${steam.name} on Steam`, ...pill};
}

export const HYTALE_META = 'Hytale username';

export function hytalePopoverRow(name: string): PopoverRow {
    return {service: 'hytale', title: name, meta: HYTALE_META, dim: false, full: `${name}, ${HYTALE_META}`, name, url: ''};
}

// The icon-only profile pill's name and title.
export function steamProfileName(name: string): string {
    return `${name}’s Steam profile`;
}

// The profile pill's address: the one the server builds from the SteamID64,
// https://steamcommunity.com/profiles/<id>, and nothing else, so a pill can
// never be a link anywhere but Steam's own profile page. '' for anything
// else, and then there is no pill.
const STEAM_PROFILE = /^https:\/\/steamcommunity\.com\/profiles\/[0-9]{17}$/;

export function steamProfileURL(url: string | undefined): string {
    return url && STEAM_PROFILE.test(url) ? url : '';
}

export const COPY = 'Copy';
export const COPIED = 'Copied';

// Somebody's rows in their profile popover: Steam, then Hytale. None at all
// for a reader who may not watch, whatever the view holds. The popover draws
// exactly these (PopoverLive.tsx), so this is the one gate.
export function popoverRowsFor(view: AccountsView | null, userId: string, access: string): PopoverRow[] {
    if (!accountsVisible(access) || !view) {
        return [];
    }

    const person: AccountsPerson | undefined = view.people[userId];
    const rows: PopoverRow[] = [];
    if (person?.steam) {
        rows.push(steamPopoverRow(person.steam));
    }
    if (person?.hytale) {
        rows.push(hytalePopoverRow(person.hytale.name));
    }

    return rows;
}

export const PLAYING_EYEBROW = 'Playing';

export type PlayingItem = {
    userId: string;
    name: string;
    game: string;

    // What follows the name: the game, or "In a game" for one Steam does
    // not name.
    what: string;

    // "{display name} · {game}", and the item's title.
    words: string;
    title: string;
};

// Who is in a Steam game right now, by Mattermost display name, by name.
// Online people who are not playing are not here, and Hytale never is: it
// says who somebody is there, not what they are doing. Nothing for a reader
// who may not watch.
export function playingNow(view: AccountsView | null, access: string, nameOf: (userId: string) => string): PlayingItem[] {
    if (!accountsVisible(access) || !view) {
        return [];
    }

    return Object.entries(view.people).
        filter(([, person]) => Boolean(person.steam?.inGame)).
        map(([userId, person]) => {
            const name = nameOf(userId) || 'Someone';
            const game = (person.steam?.game || '').trim();
            const what = game || 'In a game';

            return {
                userId,
                name,
                game,
                what,
                words: `${name} · ${what}`,
                title: game ? `${name} is playing ${game} on Steam` : `${name} is in a game on Steam`,
            };
        }).
        sort((a, b) => a.name.localeCompare(b.name) || (a.userId < b.userId ? -1 : 1));
}

// ---- Disconnect for somebody not linked (5.5, Victor's P3 pick B) ----

export type Connection = {service: AnyService; name: string};

// What a person has connected, in the one order the line uses.
export function connectionsOf(me: MeLike | null): Connection[] {
    const list: Connection[] = [];
    if (me?.twitch?.login) {
        list.push({service: 'twitch', name: me.twitch.displayName || me.twitch.login});
    }
    if (me?.steam?.connected) {
        list.push({service: 'steam', name: me.steam.name || ''});
    }
    if (me?.hytale?.connected) {
        list.push({service: 'hytale', name: me.hytale.name || ''});
    }

    return list;
}

// twitchLogic.ts's disconnectLineShown, for the set: the line is in the page
// while there is anything to disconnect, while a press is out, and while the
// words after one are shown, whatever "me" says meanwhile. The Twitch
// function itself is not changed.
export function accountsLineShown(at: {connected: number; busy: boolean; saying: boolean}): boolean {
    return at.connected > 0 || at.busy || at.saying;
}

export type DisconnectLine =

    // Nothing connected: no line.
    {kind: 'none'} |

    // Twitch alone: today's TwitchDisconnectKey, unchanged.
    {kind: 'twitch'} |

    // "Disconnect", then each connected service's name as a key; with one
    // service, one key, "Disconnect Steam".
    {kind: 'list'; lead: string; keys: {service: AnyService; word: string; title: string}[]} |

    // One name pressed once: the whole line is that service's own two-press
    // words, never cut short, until the press runs out.
    {kind: 'armed'; service: AnyService; words: string; title: string} |

    // The second press on its way.
    {kind: 'busy'; service: AnyService; words: string};

export function keyTitle(connection: Connection, brand: string): string {
    if (connection.service === 'twitch') {
        return twitchDisconnectTitle(connection.name, brand);
    }

    return `Your ${SERVICE_NAME[connection.service]} account, ${connection.name}, is connected to ${brand}. Disconnecting removes it.`;
}

// saying: the words after a press of this line are still shown. Twitch
// alone then stays a key of this line, beside them, and becomes Twitch's own
// key only once they have gone, so they never vanish with the line.
export function disconnectLineOf(connections: Connection[], armed: AnyService | '', busy: AnyService | '', brand: string, saying = false): DisconnectLine {
    if (busy !== '') {
        return {kind: 'busy', service: busy, words: 'Disconnecting…'};
    }
    if (connections.length === 0) {
        return {kind: 'none'};
    }
    if (connections.length === 1 && connections[0].service === 'twitch' && !saying) {
        return {kind: 'twitch'};
    }

    const pressed = connections.find((connection) => connection.service === armed);
    if (pressed) {
        return {kind: 'armed', service: pressed.service, words: armedWords(pressed.service), title: keyTitle(pressed, brand)};
    }

    return {
        kind: 'list',
        lead: 'Disconnect',
        keys: connections.map((connection) => ({service: connection.service, word: SERVICE_NAME[connection.service], title: keyTitle(connection, brand)})),
    };
}

// ---- Takeover notices (5.6) ----

export type Notice = {service: AnyService; name: string; at: number};

// Every takeover notice a person has, newest first; one shows at a time over
// the linked bar, and OK shows the next. None for a service they are blocked
// from, as nothing else of it shows them.
export function noticesOf(me: MeLike | null): Notice[] {
    const list: Notice[] = [];
    if (me?.twitch?.lost && !me.twitch.blocked) {
        list.push({service: 'twitch', name: me.twitch.lost.login, at: me.twitch.lost.at});
    }
    if (me?.steam?.lost && !me.steam.blocked) {
        list.push({service: 'steam', name: me.steam.lost.name, at: me.steam.lost.at});
    }
    if (me?.hytale?.lost && !me.hytale.blocked) {
        list.push({service: 'hytale', name: me.hytale.lost.name, at: me.hytale.lost.at});
    }

    return list.sort((a, b) => b.at - a.at);
}

export function lostWords(service: AnyService, name: string, date: string): string {
    return `Your ${SERVICE_NAME[service]} account ${name} was connected to another Mattermost account on ${date}. If that was not you, tell an admin.`;
}

// ---- Connecting: the timings, and what Waiting says when ----

// A ticket lives ten minutes on the server; one held this long is asked for
// again before Continue can be pressed on a dead one.
export const TICKET_REFRESH_MS = 9 * 60 * 1000;

// While waiting: "me" read again this often, and for no longer than this.
export const WAITING_POLL_MS = 3000;
export const WAITING_GIVE_UP_MS = 5 * 60 * 1000;

// No sign of the window this long after Continue (this window never lost
// focus to it, and no word came from the server): pop-ups may be blocked.
export const NO_WINDOW_HINT_MS = 4000;

// Hytale shows its own error page when the application's Redirect URI or
// scopes are wrong, and nothing comes back: after this long, Waiting says
// what an admin should look at.
export const REDIRECT_HINT_MS = 60 * 1000;

// How long the words after a Disconnect stay.
export const DISCONNECTED_MS = 6000;

export type WaitingView = {
    // Whether "me" is still read every few seconds.
    polling: boolean;
    noWindow: boolean;
    redirect: boolean;
};

// Waiting, this long after Continue, having seen the window or not.
export function waitingAt(service: Service, elapsedMs: number, sawWindow: boolean): WaitingView {
    return {
        polling: elapsedMs < WAITING_GIVE_UP_MS,
        noWindow: !sawWindow && elapsedMs >= NO_WINDOW_HINT_MS,
        redirect: service === 'hytale' && elapsedMs >= REDIRECT_HINT_MS,
    };
}

// The reader's own connection to one service, as "me" has it, in a word that
// changes exactly when the connection does: connected or not, to which
// account, blocked, taken over. Not what moves by itself (live on Twitch, a
// Steam profile gone private, a fault an admin sees), nor whether the service
// is switched on. The "me" event is the server's word that the Connect window
// did its work only when the "me" read after it says that this changed: the
// same event goes out for the reader's other connections and their access,
// and to everybody when an admin deletes a service's data.
export function ownConnectionOf(account: MeTwitch | MeAccount | undefined): string {
    if (!account) {
        return '';
    }
    const either = account as Partial<MeTwitch & MeAccount>;

    return JSON.stringify([either.login || '', Boolean(either.connected), either.name || '', Boolean(either.blocked), either.lost || null]);
}

// Whether the "me" read made after an event came back with nothing to go by:
// it failed, or the store did not answer ("unknown"), so the reader's
// connections are still as they were read before. The event is then taken at
// its word, as every such event was until 3.6.0: better a hint that is not
// shown than one that says "No window?" under a window that did its work.
export function readSaidNothing(read: unknown): boolean {
    return !read || (read as {access?: string}).access === 'unknown';
}
