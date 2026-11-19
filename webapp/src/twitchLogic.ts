// Twitch in the panel, worked out without a browser (design/twitch-live.md 6):
// whether this page may run Twitch's player at all, how big it may be, the
// one way its address is ever made, which of a person's streams stands for
// them in the lists that show one card per person, and when the Disconnect
// Twitch line of somebody not linked is in the page.
//
// Imports nothing, so build/check-twitch.mjs can transpile it on its own and
// run it, as check-away.mjs does with awayLogic.ts.

// ---- Can this page embed? (6.2) ----

export type EmbedHost = {ok: true; host: string} | {ok: false; reason: string};

export const EMBED_HTTPS_ONLY = 'Twitch’s player only runs on https:// pages.';
export const EMBED_PORT_443 = 'Twitch’s player only runs on the standard https port (443).';
export const EMBED_NO_IP = 'Twitch’s player does not run on a site reached by IP address.';

// From the page's own location, because the parent Twitch checks is the
// frame's real ancestor, whatever the Site URL says. Twitch answers with a
// frame-ancestors policy naming each parent, and takes no port, no scheme
// and no IP address in one: so a Mattermost served over http, on a custom
// port or at an IP address can never show it. localhost and 127.0.0.1 are
// Twitch's exceptions, on any scheme and port.
export function embedHostFor(loc: {protocol: string; hostname: string; port: string}): EmbedHost {
    const hostname = loc.hostname;
    if (hostname === 'localhost' || hostname === '127.0.0.1') {
        return {ok: true, host: hostname};
    }
    if (loc.protocol !== 'https:') {
        return {ok: false, reason: EMBED_HTTPS_ONLY};
    }
    if (loc.port !== '' && loc.port !== '443') {
        return {ok: false, reason: EMBED_PORT_443};
    }
    if ((/^\d{1,3}(\.\d{1,3}){3}$/).test(hostname) || hostname.includes(':') || hostname.startsWith('[')) {
        return {ok: false, reason: EMBED_NO_IP};
    }

    return {ok: true, host: hostname.toLowerCase()};
}

// ---- The player's address (6.3) ----

// Every player address starts with exactly this. TwitchFrame renders a frame
// only for an address that does, so nothing else can ever be framed by it.
export const TWITCH_PLAYER_PREFIX = 'https://player.twitch.tv/?';

const LOGIN = /^[a-z0-9_]{1,25}$/;

// A Twitch login as the server checks it. Checked again here because it goes
// into an address: anything else builds no address at all.
export function validLogin(login: string): boolean {
    return LOGIN.test(login);
}

// The channel on twitch.tv: Watch on Twitch, and where a card goes when the
// player cannot run here. '' for a login that is not one.
export function channelURL(login: string): string {
    return validLogin(login) ? `https://www.twitch.tv/${login}` : '';
}

// The only way a player address is made: the four parameters in this order,
// encoded by URLSearchParams. muted is the one thing the plain embed lets the
// page choose, and only as it loads. null for a bad login or no host.
export function frameURL(login: string, host: string, muted: boolean): string | null {
    if (!validLogin(login) || host === '') {
        return null;
    }

    const query = new URLSearchParams();
    query.append('channel', login);
    query.append('parent', host);
    query.append('muted', muted ? 'true' : 'false');
    query.append('autoplay', 'true');

    return TWITCH_PLAYER_PREFIX + query.toString();
}

// ---- How big (6.4, 6.6) ----

// Twitch's minimum for an embedded player, which the page may never go below.
export const TWITCH_MIN_W = 400;
export const TWITCH_MIN_H = 300;

// The extra width needed to switch a small tile to inline: a resize that
// wobbles around 400 must not flip between the two and reload the player.
// Only upwards, because Twitch's minimum is a floor that may never be broken.
export const TWITCH_INLINE_HYSTERESIS = 8;

// null when the player may not run at this width. wasInline is the tile's
// current mode, so leaving inline happens below 400 and entering it at 408.
// Never under 300 tall: at 400 wide the player is 400 × 300, and Twitch's
// player letterboxes its picture inside.
export function inlineSize(width: number, wasInline: boolean): {w: number; h: number} | null {
    if (width < TWITCH_MIN_W + (wasInline ? 0 : TWITCH_INLINE_HYSTERESIS)) {
        return null;
    }

    const w = Math.floor(width);

    return {w, h: Math.max(TWITCH_MIN_H, Math.round((w * 9) / 16))};
}

// The theatre: the panel's overlay that gives the player room when the panel
// has none. No wider than this, and with room for its header and margins.
export const THEATRE_MAX_W = 1280;
export const THEATRE_CHROME = 120;

// null when even the whole window is too small for Twitch's minimum: a
// phone-sized window gets no Play bigger, only Watch on Twitch.
export function theatreSize(innerW: number, innerH: number): {w: number; h: number} | null {
    const w = Math.min(innerW - 32, THEATRE_MAX_W);
    const h = Math.min(Math.round((w * 9) / 16), innerH - THEATRE_CHROME);

    return w >= TWITCH_MIN_W && h >= TWITCH_MIN_H ? {w, h} : null;
}

// A tile's border, each side (.vntv-player in the stylesheet). A Twitch tile
// spans the whole row of the grid, so its stage is the row less the two.
// The player has no frame, so none.
export const TWITCH_TILE_BORDER = 0;

export function fullRowStage(rowWidth: number): number {
    return Math.max(0, rowWidth - (2 * TWITCH_TILE_BORDER));
}

// What a Twitch tile shows, checked in this order (6.4): the switch off; a
// page or a login the player cannot run on; the theatre open for it; too
// narrow; and otherwise the player itself. Only the last has a frame: every
// other mode is a plate, so nothing is ever laid over the player.
export type TileMode = 'off' | 'blocked' | 'theatre' | 'small' | 'inline';

export function tileMode(at: {embedOn: boolean; hostOk: boolean; loginOk: boolean; theatre: boolean; fits: boolean}): TileMode {
    if (!at.embedOn) {
        return 'off';
    }
    if (!at.hostOk || !at.loginOk) {
        return 'blocked';
    }
    if (at.theatre) {
        return 'theatre';
    }

    return at.fits ? 'inline' : 'small';
}

// ---- One card per person (5.4, 5.6, 5.9) ----

// Only what these read of a stream, so nothing has to be imported.
type Listed = {
    handle: string;
    userId?: string;
    paused: boolean;
    alwaysOn: boolean;
    source?: string;
    coveredBy?: string;
};

// What Live now lists: everything but a Twitch entry covered by its owner's
// Broadcast Box card, which says "Also on Twitch" instead. The entry stays in
// the snapshot for the tile and the watch list.
export function liveCards<T extends Listed>(streams: T[]): T[] {
    return streams.filter((stream) => !stream.coveredBy);
}

// Who is on air, by person, for the Up next rail: one handle each. The rule
// (never a covered entry, a paused stream or a 24/7 channel, and the
// Broadcast Box stream rather than the Twitch one when a person has both) is
// the server's since 3.5.0: it applies it once, in the snapshot's people index
// (server/people.go liveHandles), so that the panel and a native app can
// never disagree about who is live. This only reads the answer out.
export function liveHandlesOf(people: Record<string, {live?: string}> | undefined): Record<string, string> {
    const byUser: Record<string, string> = {};

    Object.keys(people || {}).forEach((userId) => {
        const handle = people![userId].live;
        if (handle) {
            byUser[userId] = handle;
        }
    });

    return byUser;
}

// The stream a person's profile popover talks about: never a 24/7 channel or
// a covered entry; their Broadcast Box stream while it is on the air, else
// their Twitch stream, else a paused Broadcast Box one (which the popover
// shows as not live). Live on Twitch with Broadcast Box paused is live.
export function popoverStream<T extends Listed>(streams: T[], userId: string): T | undefined {
    const theirs = streams.filter((stream) => stream.userId === userId && !stream.alwaysOn && !stream.coveredBy);

    return theirs.find((stream) => !stream.source && !stream.paused) ||
        theirs.find((stream) => stream.source === 'twitch') ||
        theirs.find((stream) => !stream.source);
}

// Twitch's whole audience, as Twitch counts it, and always said to be that.
export function onTwitchWords(viewers: number): string {
    return `${Math.max(0, viewers).toLocaleString()} on Twitch`;
}

export function watchingOnTwitchWords(viewers: number): string {
    return `${Math.max(0, viewers).toLocaleString()} watching on Twitch`;
}

// ---- Disconnect Twitch for somebody not linked (lock-screen.md 9.2) ----

// Whether the key's line is in the page: while there is an account to
// disconnect, while a press is out, and while the words after one are shown.
// From the press on, whatever "me" says meanwhile: "me" can lose the account
// before the answer comes (the server tells the page over its socket first),
// and the words are where focus goes when the key goes. Taken out with the
// key in that order, they left focus on the page's body, from where a letter
// went to Mattermost's message box.
export function disconnectLineShown(at: {connected: boolean; busy: boolean; saying: boolean}): boolean {
    return at.connected || at.busy || at.saying;
}
