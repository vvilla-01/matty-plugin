import type {
    AccountService,
    AccountsView,
    AchievementProgress,
    AdminClips,
    AdminHytale,
    AdminSounds,
    AdminSteam,
    AdminTwitch,
    AirtimeStatus,
    AwardsMine,
    Boards,
    Clip,
    ClipCard,
    ClipDraft,
    ClipErrorCode,
    ClipSource,
    LetInList,
    MattermostChannel,
    MattermostPost,
    MattermostUser,
    Me,
    NowPlaying,
    PostSearchPage,
    Profile,
    Snapshot,
    SoundBoard,
    SoundPlay,
    Takebacks,
} from '../../src/types';
import {now as clockNow} from '../../src/clock';
import {PLAN_POST_TYPE} from '../../src/planLogic';
import {packetSection} from '../../src/soundLogic';
import fixture from '../fixtures/sound-packets.json';
import {labEmojiNamed} from './emojiImages';
import radioPicture from '../pictures/channel.svg';
import sampleClipData from '../media/sample-clip.mp4';
import {CLIP_POST_TYPE, MIN_CLIP_MS} from '../../src/clipLogic';
import {channelOf, mattermostStore} from '../../src/mattermost';
import type {AdminAppCheck, AdminChannel, AdminPush, AdminStills, ChannelPictureKind, MediaCacheStatus, NewSound, NowPlayingAnswer, PostSearch, StillClaim} from '../../src/client';
import {findHashtags} from '../../src/linkify';
import {PRODUCT_NAME} from '../../src/name';
import {
    accountsViewOf,
    labHytaleOn,
    labSteamOn,
    LAB_ACHIEVEMENT_KINDS,
    LAB_CHANNELS,
    LAB_POSTS,
    LAB_STEAM_FRIENDS,
    LAB_USERS,
    ME_ID,
    RADIO_HANDLE,
    change,
    clipCardOf,
    clipExpiresAt,
    clipPersonOf,
    clipTagOf,
    clipTailsOf,
    clipViewOf,
    countedVotes,
    forgottenMediaCache,
    adminSoundsOf,
    freshId,
    freshPlanId,
    getWorld,
    labAccessOf,
    labDownloadRefusal,
    labShareMessage,
    labTwitchOn,
    labShareProps,
    labPlanShareMessage,
    labPlanShareProps,
    labTailsOf,
    liveClipsOf,
    meOf,
    profileOf,
    shownVotes,
    boardsOf,
    boardsTagOf,
    heldByMe,
    snapshotOf,
    soundBoardOf,
    subscribe,
    labAdminNews,
    labAppCheckView,
    labPushView,
    labReleaseAnswer,
} from '../world';
import type {LabClip, LabDraft, World} from '../world';
import {BUILT_IN_BRAND, PLUGIN_BASE, PLUGIN_ID} from './manifest';
import {heardWatching, labCounters} from './whep';
import {artOf, detailsOf, searchCatalogue} from '../media';
import type {MediaResult} from '../../src/types';

// The plugin's HTTP client, answered from memory.
//
// Every export of src/client.ts is here with the same signature, because
// webpack swaps this module in for that one and a missing export would be an
// undefined at runtime rather than an error at build time.
//
// The write rules are the SERVER's rules, repeated: a title is required, the
// time has to be in the future and inside ninety days, and three plans each is
// the limit. Repeated deliberately — a lab that accepts what the server rejects
// teaches the wrong thing about the form.

const HORIZON_MS = 90 * 24 * 3600000;
const PER_PERSON = 3;

// A refusal rejects, as the real client's does on an error status: thrown
// inside the timer, it would escape as an uncaught error and leave the promise
// hanging for ever.
function answer<T>(value: () => T): Promise<T> {
    const world = getWorld();

    return new Promise((resolve, reject) => {
        window.setTimeout(() => {
            try {
                resolve(value());
            } catch (reason) {
                reject(reason instanceof Error ? reason : new Error(String(reason)));
            }
        }, world.latencyMs);
    });
}

function write<T>(value: () => T): Promise<T> {
    const world = getWorld();

    return new Promise((resolve, reject) => {
        window.setTimeout(() => {
            if (world.failWrites) {
                reject(new Error('Could not save that just now. Try again in a moment.'));
                return;
            }

            try {
                resolve(value());
            } catch (reason) {
                reject(reason instanceof Error ? reason : new Error(String(reason)));
            }
        }, world.latencyMs);
    });
}

export const fetchStreams = (): Promise<Snapshot> => answer(snapshotOf);

// A refusal from a plugin route, as the real client's: the server's words as
// the message, with its status, its code and its own text.
export class RequestError extends Error {
    status: number;
    code: string;
    serverText: string;

    constructor(message: string, status: number, code = '', serverText = '') {
        super(message);
        this.name = 'RequestError';
        this.status = status;
        this.code = code;
        this.serverText = serverText;
    }
}

const refused = (status: number, code: string, text: string): RequestError => new RequestError(text, status, code, text);

// Who hears a refusal for access (client.ts): access.ts, which reads "me"
// again. The lab's own buttons call it too, as a tile's next request would
// carry a let-out to the page (labRefusal).
let refusalListener: ((code: string) => void) | null = null;

export function setAccessRefusalListener(listener: ((code: string) => void) | null): void {
    refusalListener = listener;
}

export function labRefusal(code: string): void {
    refusalListener?.(code);
}

// The server's refusal for a route that is not open, by Victor's access:
// locked everywhere, and watch only where a link is needed. Told to the page,
// as the real client tells it. The server's sentences name the plugin as the
// server calls it (its Name setting); its words for admins say the product's
// own name, Matty.
const serverName = (): string => getWorld().me.name;

const ACCESS_TEXT = {
    locked: (): string => `${serverName()} is locked. Unlock it with your bearer token.`,
    watch_only: (): string => 'You are let in to watch only. Link your stream to do that.',
};

function accessGate(needsLink: boolean): void {
    const access = labAccessOf(getWorld());
    if (access === 'linked' || (access === 'letIn' && !needsLink)) {
        return;
    }

    const code = access === 'letIn' ? 'watch_only' : 'locked';
    labRefusal(code);
    throw refused(403, code, ACCESS_TEXT[code]());
}

// "me": held back for the Access row's "unknown" (the page has none, and
// asking again — as a shared clip's card does — gets none either); slow for
// its "starting"; and once 'unknown' for each store blip.
export const fetchMe = (): Promise<Me> => {
    const world = getWorld();
    const delay = world.meDelayMs;
    world.meDelayMs = 0;

    return new Promise((resolve, reject) => {
        window.setTimeout(() => {
            const now = getWorld();
            if (now.meWithheld) {
                reject(new RequestError('Not configured, or no session yet.', 404));

                return;
            }
            if (now.storeBlips > 0) {
                now.storeBlips -= 1;
                resolve({...meOf(), access: 'unknown', streamKey: '', clipsStreamer: undefined});

                return;
            }

            resolve(meOf());
        }, delay > 0 ? delay : world.latencyMs);
    });
};

// What POST /me/link answers, as the lock screen's switch in the lab says:
// the server's statuses, codes and fixed texts.
const LINK_REFUSALS: Record<string, [number, string]> = {
    not_accepted: [403, 'That publish token was not accepted. Check the token in your streaming software and try again.'],
    rate_limited: [429, 'Too many attempts. Wait a few minutes and try again.'],
    unreachable: [502, 'Could not reach Broadcast Box.'],
    bad_name: [502, 'Broadcast Box returned a stream name this plugin cannot use.'],
    not_configured: [503, 'Broadcast Box is not configured.'],
    too_long: [400, 'That publish token is too long.'],
    store: [500, 'Could not save the link.'],
    bad_request: [400, 'Could not read the request.'],
};

export const linkStream = (token: string): Promise<{streamKey: string}> => write(() => {
    const mode = getWorld().linkAnswer;
    if (token !== '') {
        if (mode === 'no_answer') {
            throw new RequestError('Request failed (404)', 404);
        }
        if (mode in LINK_REFUSALS) {
            const [status, text] = LINK_REFUSALS[mode];
            throw refused(status, mode, text);
        }
        if (mode === 'empty_key') {
            return {streamKey: ''};
        }

        // The lab never holds a real token: six characters or more is a
        // token Broadcast Box knows, and anything shorter is not.
        if (token.length < 6) {
            throw refused(403, 'not_accepted', LINK_REFUSALS.not_accepted[1]);
        }
    }

    // Victor's stream follows, as the real server's next poll has it: linked
    // by hand once a token is proven, and matched to nobody once unlinked
    // (his username is not his stream key) — so a link made from the footer
    // shows the linked, live footer in the lab too.
    const key = token === '' ? '' : 'live-victor';
    const source = key === '' ? 'none' : 'manual';
    change((current) => {
        current.me = {...current.me, streamKey: key};
        current.victorSource = source;
        current.streams = current.streams.map((item) => (
            item.userId === ME_ID ? {...item, linkSource: source === 'none' ? undefined : source} : item
        ));
    });

    return {streamKey: key};
});

// Unlinking: the same change to the lab's world as linking nothing.
export const unlinkStream = (): Promise<{streamKey: string}> => linkStream('');

// The lab has no server, so these produce a real file from the lab's own world
// rather than pretending. Clicking Export in the lab downloads something, which
// is the only way to see that the button works at all — the version that was a
// plain link could not be told apart from a broken one here.
export const downloadJSON = (path: string, filename: string): Promise<void> => write(() => {
    const body = path.includes('airtime') ? {totals: {'live-victor': {seconds: 154000}}} : {kind: 'matty-settings', version: 1, settings: {}};
    const url = URL.createObjectURL(new Blob([JSON.stringify(body, null, 4)], {type: 'application/json'}));
    const link = document.createElement('a');
    link.href = url;
    link.download = filename;
    document.body.appendChild(link);
    link.click();
    link.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 0);

    return undefined;
});

export const exportAirtime = (): Promise<void> => downloadJSON('/api/v1/admin/airtime', 'matty-airtime.json');

export const fetchAirtimeStatus = (): Promise<AirtimeStatus> => answer(() => ({
    pluginVersion: 'lab',
    recording: true,
    readable: true,
    recordedSeconds: 325000,
    streamers: 4,
    months: 3,
    halted: false,
    hasBackup: true,
    backupSavedAt: Date.now() - 86400000,
    backupVersion: 'lab',
    backupSeconds: 325000,
    backupStreamers: 4,
    backupAheadSeconds: 0,
    twitchSeparate: labTwitchOn(getWorld()),

    // 3.2.0: the fan and 24/7 channel ledgers (design/3.2.0.md 2.2).
    ...getWorld().ledgerAlert,
}));

export const importAirtime = (exported: string): Promise<{changed: number; message: string}> => write(() => {
    let parsed: Record<string, unknown>;
    try {
        parsed = JSON.parse(exported) as Record<string, unknown>;
    } catch {
        throw new Error('That is not a readable airtime export.');
    }

    // The same two refusals the real one makes, in the same order, so the lab
    // shows the message an admin would actually get.
    if (!parsed || typeof parsed !== 'object' || !('totals' in parsed)) {
        throw new Error('That file is not an airtime export. A settings backup goes in the Backup section instead.');
    }

    return {changed: Object.keys((parsed.totals as Record<string, unknown>) || {}).length, message: 'Airtime imported.'};
});

export const restoreAirtime = (): Promise<{changed: number; message: string}> =>
    write(() => ({changed: 0, message: 'Nothing to restore in the lab.'}));

// A theme an admin has pasted (a fresh server has none, and the card says
// there is nothing to apply): a dark one of their own choosing.
export const fetchTheme = (): Promise<{theme: string; whitened?: string[]}> =>
    answer(() => ({
        theme: JSON.stringify({
            sidebarBg: '#202228',
            sidebarTextActiveBorder: '#4a7ce8',
            centerChannelBg: '#191b1f',
            centerChannelColor: '#e3e4e8',
            linkColor: '#5d89ea',
            buttonBg: '#4a7ce8',
            mentionBg: '#4a7ce8',
            codeTheme: 'monokai',
        }),
        whitened: [],
    }));

export const applyThemeToEveryone = (): Promise<{applied: number}> =>
    write(() => ({applied: 7}));

// The plugin's starting list of birthdays, which is empty (server/birthday_seed.go):
// nothing in the System Console asks for it any more.
export const fetchBirthdaySeed = (): Promise<Array<{name: string; birthday: string; birth_year: number; username?: string; parent_username?: string[]}>> =>
    answer(() => []);

// The artwork an admin has uploaded, kept in memory so the lab's settings page
// behaves like the real one: upload, see the preview change, put the default
// back.
const uploaded: Record<string, string> = {};

export const fetchBrand = (): Promise<Record<string, boolean>> =>
    answer(() => ({logo: Boolean(uploaded.logo), icon: Boolean(uploaded.icon), ghost: Boolean(uploaded.ghost)}));

export const uploadBrand = (which: string, file: File): Promise<void> => write(() => {
    // The same rule the server applies, from the bytes rather than the header:
    // anything that is not an image is refused.
    if (!file.type.startsWith('image/')) {
        throw new Error('That is not an image. SVG, PNG, JPEG, GIF or WebP.');
    }

    uploaded[which] = URL.createObjectURL(file);

    return undefined;
});

export const resetBrand = (which: string): Promise<{ok: boolean}> => write(() => {
    delete uploaded[which];

    return {ok: true};
});

export const brandURL = (which: string, stamp?: number): string => {
    void stamp;

    return uploaded[which] || BUILT_IN_BRAND[which] || '';
};

// The 24/7 channel pictures. One always-on key in the lab, with a picture an
// admin uploaded (the lab's stand-in, lab/pictures/channel.svg); an upload
// replaces it in memory, like the artwork, and Remove leaves none, since the
// plugin ships no channel picture.
const LAB_CHANNEL = 'radio';
const channelUploads: Record<string, string> = {[LAB_CHANNEL]: radioPicture};

const channelPicture = (key: string): string => channelUploads[key] || '';

export const channelPictureURL = (handle: string, version: string): string => {
    void version;

    return handle === RADIO_HANDLE ? channelPicture(LAB_CHANNEL) : '';
};

export const streamPictureURL = (stream: {handle: string; pictureVersion?: string}): string | undefined =>
    (stream.pictureVersion ? channelPictureURL(stream.handle, stream.pictureVersion) || undefined : undefined);

export const fetchChannelPictures = (): Promise<AdminChannel[]> => answer(() => [{
    key: LAB_CHANNEL,
    handle: RADIO_HANDLE,
    name: 'Lo-fi radio',
    custom: Boolean(channelUploads[LAB_CHANNEL]),
    shipped: false,
    version: channelUploads[LAB_CHANNEL] ? 'uploaded' : undefined,
    thumbCustom: Boolean(channelThumbs[LAB_CHANNEL]),
    thumbVersion: channelThumbs[LAB_CHANNEL] ? 'uploaded' : undefined,
}]);

// The settings page names a channel by its handle, as the server's routes do.
const keyOfHandle = (handle: string): string => (handle === RADIO_HANDLE ? LAB_CHANNEL : '');

export const uploadChannelPicture = (handle: string, file: File, kind: ChannelPictureKind = 'picture'): Promise<void> => write(() => {
    const key = keyOfHandle(handle);
    if (!file.type.startsWith('image/')) {
        throw new Error('That is not an image. SVG, PNG, JPEG, GIF or WebP.');
    }

    if (kind === 'thumb') {
        channelThumbs[key] = URL.createObjectURL(file);
        change((current) => {
            current.streams = current.streams.map((item) => (
                item.handle === RADIO_HANDLE ? {...item, thumbnailVersion: `t${Date.now()}`} : item
            ));
        });

        return undefined;
    }

    channelUploads[key] = URL.createObjectURL(file);

    // Every panel picks the new picture up on its next poll.
    change((current) => {
        current.streams = current.streams.map((item) => (
            item.handle === RADIO_HANDLE ? {...item, pictureVersion: `v${Date.now()}`} : item
        ));
    });

    return undefined;
});

export const resetChannelPicture = (handle: string, kind: ChannelPictureKind = 'picture'): Promise<{ok: boolean}> => write(() => {
    const key = keyOfHandle(handle);
    if (kind === 'thumb') {
        delete channelThumbs[key];
        change((current) => {
            current.streams = current.streams.map((item) => (
                item.handle === RADIO_HANDLE ? {...item, thumbnailVersion: undefined} : item
            ));
        });

        return {ok: true};
    }

    delete channelUploads[key];
    change((current) => {
        current.streams = current.streams.map((item) => (
            item.handle === RADIO_HANDLE ? {...item, pictureVersion: undefined} : item
        ));
    });

    return {ok: true};
});

export const adminChannelPictureURL = (handle: string, version?: string, kind: ChannelPictureKind = 'picture'): string => {
    void version;
    const key = keyOfHandle(handle);

    return kind === 'thumb' ? (channelThumbs[key] || '') : channelPicture(key);
};

// The 24/7 card pictures: no shipped one, an upload in memory otherwise.
const channelThumbs: Record<string, string> = {};

export const channelThumbnailURL = (handle: string, version: string): string => {
    void version;

    return handle === RADIO_HANDLE ? (channelThumbs[LAB_CHANNEL] || '') : '';
};

export const streamThumbnailURL = (stream: {handle: string; thumbnailVersion?: string}): string | undefined =>
    (stream.thumbnailVersion ? channelThumbnailURL(stream.handle, stream.thumbnailVersion) : undefined);

// Live preview stills, shared the way the server shares them: one lease per
// stream at a time, a head start for a browser that is already watching, and
// the newest still riding on the stream in the snapshot. The same numbers as
// server/stills.go.
const STILL_STALE_MS = 90000;
const WATCHER_HEAD_START_MS = 60000;
const LEASE_MS = 20000;

const stills: Record<string, {url: string; at: number; by: string}> = {};
const leases: Record<string, {token: string; expires: number}> = {};

// Who is blocked from sharing stills, and since when: the moderation tools'
// state, as the server keeps it in its KV store.
const blocked: Record<string, number> = {};

const BLOCKED = 'You can\'t share preview pictures.';

// Everybody the lab knows by id, for the moderation list's names.
function personOf(userId: string): {username: string; displayName: string} {
    const me = getWorld().me;
    if (userId === me.userId) {
        return {username: me.username || 'victor', displayName: 'Victor'};
    }

    const name = userId.replace(/^u-/, '');

    return {username: name, displayName: name.charAt(0).toUpperCase() + name.slice(1)};
}

function dropStill(handle: string): void {
    if (stills[handle]) {
        URL.revokeObjectURL(stills[handle].url);
        delete stills[handle];
    }
    delete leases[handle];
    change((current) => {
        current.streams = current.streams.map((item) => (
            item.handle === handle ? {...item, stillVersion: undefined, stillAt: undefined} : item
        ));
    });
}

export const fetchAdminStills = (): Promise<AdminStills> => answer(() => {
    const world = getWorld();

    // The server's rules: nothing while previews are off, never a 24/7
    // channel, only pictures still on a live stream.
    return {
        stills: Object.entries(stills).
            filter(([handle]) => world.livePreviews && world.streams.some((item) => item.handle === handle && !item.alwaysOn)).
            map(([handle, still]) => {
                const person = personOf(still.by);

                return {
                    handle,
                    title: world.streams.find((item) => item.handle === handle)?.title || `Stream ${handle.slice(0, 6)}`,
                    version: `s${still.at}`,
                    at: new Date(still.at).toISOString(),
                    byUserId: still.by,
                    byUsername: person.username,
                    byDisplayName: person.displayName,
                };
            }).
            sort((a, b) => a.title.localeCompare(b.title)),
        blocked: Object.entries(blocked).
            map(([userId, at]) => ({userId, ...personOf(userId), blockedAt: new Date(at).toISOString()})).
            sort((a, b) => a.username.localeCompare(b.username) || a.userId.localeCompare(b.userId)),
    };
});

export const removeStill = (handle: string): Promise<{ok: boolean}> => write(() => {
    if (!stills[handle]) {
        throw new Error('There is no preview picture for that stream.');
    }
    dropStill(handle);

    return {ok: true};
});

export const blockSharer = (userId: string): Promise<{ok: boolean}> => write(() => {
    blocked[userId] = blocked[userId] || Date.now();
    Object.entries(stills).filter(([, still]) => still.by === userId).forEach(([handle]) => dropStill(handle));

    return {ok: true};
});

export const unblockSharer = (userId: string): Promise<{ok: boolean}> => write(() => {
    delete blocked[userId];

    return {ok: true};
});

export const stillURL = (handle: string, version: string): string => {
    void version;

    return stills[handle]?.url || '';
};

export const claimStill = (handle: string, mode: 'watching' | 'peek'): Promise<StillClaim> => answer(() => {
    accessGate(true);
    const world = getWorld();
    const live = world.streams.find((item) => item.handle === handle);
    if (!world.livePreviews) {
        throw new Error('Live previews are off.');
    }
    if (!live || live.alwaysOn) {
        throw new Error('That stream is not live right now.');
    }
    if (blocked[world.me.userId]) {
        throw new Error(BLOCKED);
    }

    const now = Date.now();
    const staleAfter = STILL_STALE_MS + (mode === 'peek' ? WATCHER_HEAD_START_MS : 0);
    const age = stills[handle] ? now - stills[handle].at : Infinity;
    const lease = leases[handle];
    if (lease && lease.expires > now) {
        return {lease: '', retryAfter: Math.max(5, Math.ceil((lease.expires - now) / 1000))};
    }
    if (age < staleAfter) {
        return {lease: '', retryAfter: Math.min(300, Math.max(5, Math.ceil((staleAfter - age) / 1000)))};
    }

    const token = freshId('lease');
    leases[handle] = {token, expires: now + LEASE_MS};

    return {lease: token, ttl: LEASE_MS / 1000};
});

export const uploadStill = (handle: string, lease: string, jpeg: Blob): Promise<void> => write(() => {
    accessGate(true);
    const held = leases[handle];
    if (!held || held.token !== lease || held.expires <= Date.now()) {
        throw new Error('That capture was not yours to make.');
    }
    if (jpeg.type !== 'image/jpeg') {
        throw new Error('That is not a JPEG.');
    }
    const me = getWorld().me.userId;
    if (blocked[me]) {
        throw new Error(BLOCKED);
    }

    delete leases[handle];
    if (stills[handle]) {
        URL.revokeObjectURL(stills[handle].url);
    }
    const at = Date.now();
    labCounters().stills++;
    stills[handle] = {url: URL.createObjectURL(jpeg), at, by: me};

    // The next poll carries it to every panel.
    change((current) => {
        current.streams = current.streams.map((item) => (
            item.handle === handle ? {...item, stillVersion: `s${at}`, stillAt: new Date(at).toISOString()} : item
        ));
    });

    return undefined;
});

// ---- Access: the let-in list and unlinking (System Console) ----

const ADMIN_ONLY = 'That is not allowed.';
const LET_IN_MAX = 500;

function adminOnly(): void {
    if (!getWorld().me.isAdmin) {
        throw refused(403, 'forbidden', ADMIN_ONLY);
    }
}

// The server's words for each add refusal, by code, for the lab's add-error
// switch.
const LET_IN_REFUSALS: Record<string, [number, string]> = {
    bad_request: [400, 'Say who to let in.'],
    no_user: [404, 'There is no such user.'],
    bot: [400, 'Bots cannot be let in.'],
    deactivated: [400, 'That account is deactivated.'],
    list_full: [409, 'The list is full (500 people). Take somebody off it first.'],
    newer: [503, `This list was saved by a newer ${PRODUCT_NAME}, so it cannot be changed here.`],
    busy: [503, 'Too many changes at once. Try again in a moment.'],
    store: [503, 'The list could not be saved, so nothing has changed. Try again in a moment.'],
};

// Accounts the lab treats as deactivated, as the "four people" list shows
// them.
const LAB_DEACTIVATED = new Set(['u-erin']);

// A let-in row's name as letin.go sends it: Mattermost's display name
// (nickname, then full name, then username, poller.go displayNameOf), or ""
// when it would only repeat the username.
function letInNameOf(user: {username?: string; nickname?: string; first_name?: string; last_name?: string}): string {
    const username = user.username || '';
    const full = [user.first_name, user.last_name].filter(Boolean).join(' ').trim();
    const shown = (user.nickname || '').trim() || full || username;

    return shown === username ? '' : shown;
}

const sortedLetIn = (people: LetInList['people']): LetInList['people'] => people.slice().sort((a, b) => {
    if ((a.state === 'gone') !== (b.state === 'gone')) {
        return a.state === 'gone' ? 1 : -1;
    }

    return a.username.toLowerCase() < b.username.toLowerCase() ? -1 : 1;
});

export const fetchLetIn = (): Promise<LetInList> => answer(() => {
    adminOnly();
    const world = getWorld();

    return {
        people: sortedLetIn(world.letInPeople),
        count: world.letInPeople.length,
        max: LET_IN_MAX,
        problem: world.letInProblem,
        readOnly: world.letInReadOnly,
    };
});

export const letIn = (username: string): Promise<{ok: boolean; added: boolean; linked: boolean}> => write(() => {
    adminOnly();
    const world = getWorld();
    const name = username.trim().replace(/^@/, '').toLowerCase();
    if (world.letInReadOnly) {
        throw refused(503, 'newer', LET_IN_REFUSALS.newer[1]);
    }
    if (world.letInAddError !== '') {
        const [status, text] = LET_IN_REFUSALS[world.letInAddError] || [503, 'Could not save.'];
        throw refused(status, world.letInAddError, text);
    }
    if (name === '') {
        throw refused(400, 'bad_request', LET_IN_REFUSALS.bad_request[1]);
    }
    // The Access card's "full (500)" people are accounts too (viewer001 to
    // viewer500, as the lab's manyLetIn names them), so Undo on one of them,
    // taken off from far down the list, lets them back in as it would anybody.
    const many = (/^viewer(\d{3})$/).exec(name);
    const user = LAB_USERS.find((candidate) => candidate.username === name) ||
        (many && Number(many[1]) >= 1 && Number(many[1]) <= LET_IN_MAX ? {id: `u-many-${String(Number(many[1]) - 1).padStart(20, '0')}`, username: name} : undefined);
    if (!user) {
        throw refused(404, 'no_user', LET_IN_REFUSALS.no_user[1]);
    }

    // As letin.go refuses them, whatever the add switch says: the status
    // hook is a bot, and Erin's account is the one the sample list shows as
    // deactivated — so her row's Undo, which the card no longer offers,
    // would be refused here as on a real server.
    if (name === 'status-hook') {
        throw refused(400, 'bot', LET_IN_REFUSALS.bot[1]);
    }
    if (LAB_DEACTIVATED.has(user.id)) {
        throw refused(400, 'deactivated', LET_IN_REFUSALS.deactivated[1]);
    }
    if (world.letInPeople.some((person) => person.userId === user.id)) {
        return {ok: true, added: false, linked: user.id !== ME_ID || world.me.streamKey !== ''};
    }
    if (world.letInPeople.length >= LET_IN_MAX) {
        throw refused(409, 'list_full', LET_IN_REFUSALS.list_full[1]);
    }

    change((current) => {
        current.letInPeople = current.letInPeople.concat({
            userId: user.id,
            username: name,
            name: letInNameOf(user),
            at: Date.now(),
            byUsername: 'victor',
            state: 'active',
        });
        if (user.id === ME_ID) {
            current.letIn = true;
        }
    });

    // Everybody in the lab but Victor (and Caro) has linked a stream.
    return {ok: true, added: true, linked: user.id === ME_ID ? world.me.streamKey !== '' : user.id !== 'u-carol'};
});

export const letOut = (userId: string): Promise<{ok: boolean; removed: boolean}> => write(() => {
    adminOnly();
    const world = getWorld();
    if (world.letInReadOnly) {
        throw refused(503, 'newer', LET_IN_REFUSALS.newer[1]);
    }

    const removed = world.letInPeople.some((person) => person.userId === userId);
    change((current) => {
        current.letInPeople = current.letInPeople.filter((person) => person.userId !== userId);
        if (userId === ME_ID) {
            current.letIn = false;
        }
    });

    return {ok: true, removed};
});

export const adminUnlink = (username: string): Promise<{ok: boolean; wasLinked: boolean; letIn: boolean}> => write(() => {
    adminOnly();
    const world = getWorld();
    const name = username.trim().replace(/^@/, '').toLowerCase();
    const user = LAB_USERS.find((candidate) => candidate.username === name);
    if (!user) {
        throw refused(404, 'no_user', 'There is no such user.');
    }

    const onList = world.letInPeople.some((person) => person.userId === user.id);
    if (user.id === ME_ID) {
        const wasLinked = world.me.streamKey !== '';
        change((current) => {
            current.me = {...current.me, streamKey: ''};
        });

        return {ok: true, wasLinked, letIn: onList};
    }

    // Caro never linked; everybody else in the lab has.
    return {ok: true, wasLinked: user.id !== 'u-carol', letIn: onList};
});

// Pop out: the lab has no public watch page, so the link goes nowhere real.
export const popOutURL = (handle: string): string => `${PLUGIN_BASE}/api/v1/watch/popout?handle=${encodeURIComponent(handle)}`;

export const exportSettings = (withSecrets: boolean): Promise<void> =>
    downloadJSON(
        `/api/v1/admin/settings${withSecrets ? '?secrets=1' : ''}`,
        withSecrets ? 'matty-settings-with-passwords.json' : 'matty-settings.json',
    );

export type SettingsImportResult = {
    ok: boolean;
    changed: number;
    kept?: string[];
    unknown?: string[];
    from?: string;
    warning?: string;
};

// The interesting answer rather than the boring one: a file with no passwords in
// it, from an older build, carrying one setting this build has dropped. That is
// the shape of every line the panel has to render.
export const importSettings = (backup: string): Promise<SettingsImportResult> => write(() => {
    let parsed: {kind?: string} = {};
    try {
        parsed = JSON.parse(backup) as {kind?: string};
    } catch {
        throw new Error('That is not a readable settings backup.');
    }

    // The kind this plugin writes, and the one its predecessor wrote.
    if (parsed.kind !== 'matty-settings' && parsed.kind !== 'villanet-tv-settings') {
        throw new Error(`That file is not a ${PRODUCT_NAME} settings backup. An airtime export goes in the Airtime section instead.`);
    }

    return {
        ok: true,
        changed: 12,
        from: '1.23.0',
        kept: ['BasicAuthPassword', 'MumblePassword'],
        unknown: ['VoteToWatchNext'],
    };
});

export type BotIdentity = {
    userId: string;
    username: string;
    displayName: string;
    description: string;
    error?: string;
    adoptable?: {userId: string; username: string; owner: string};
    adoptBlocked?: string;
};

// A server whose Bot username is held by a bot another plugin made and left
// behind: the plugin's bot stays on its old name, and the other can be taken
// over (server/botadopt.go).
let adopted = false;

const BOT_DESCRIPTION = 'Relays chat between Mumble and Mattermost, and posts birthdays.';

export const fetchBotIdentity = (): Promise<BotIdentity> =>
    answer(() => (adopted ? {
        userId: 'uid-bot-old',
        username: 'matty-bot',
        displayName: 'Matty',
        description: BOT_DESCRIPTION,
    } : {
        userId: 'uid-bot-new',
        username: 'streams-bot',
        displayName: 'Matty',
        description: BOT_DESCRIPTION,
        error: 'An account with that username already exists. — the bot has been left as "streams-bot".',
        adoptable: {userId: 'uid-bot-old', username: 'matty-bot', owner: 'com.example.streams'},
    }));

export const adoptBot = (userId: string): Promise<{ok: boolean}> => write(() => {
    if (userId !== 'uid-bot-old') {
        throw new Error('That is no longer the bot holding this name. Reload the page and look again.');
    }
    adopted = true;

    return {ok: true};
});

// The lab has no user-image store, so this only pretends. The preview in the
// settings component will show a broken image, which is the honest result of
// there being no Mattermost here.
export const uploadBotAvatar = (file: File): Promise<void> => write(() => {
    if (!file.type.startsWith('image/')) {
        throw new Error('Mattermost would not accept that image; PNG or JPEG, please');
    }

    return undefined;
});

// Planning and changing a plan are for somebody with a stream linked, as on the
// server, and refused in the server's words. Cancelling never is.
export const planStream = (title: string, startsAt: number, category: string, media?: string): Promise<{ok: boolean; id?: string}> => write(() => {
    if (!getWorld().me.streamKey) {
        throw new Error('Link your stream first — planning is for streamers.');
    }

    const clean = title.trim();
    if (clean === '') {
        throw new Error('say what you are streaming');
    }
    if (startsAt <= Date.now()) {
        throw new Error('pick a time in the future');
    }
    if (startsAt > Date.now() + HORIZON_MS) {
        throw new Error('that is too far ahead');
    }

    const world = getWorld();
    const mine = world.schedule.filter((item) => item.userId === ME_ID).length;
    if (mine >= PER_PERSON) {
        throw new Error('you already have three planned; remove one first');
    }

    const id = freshPlanId();
    change((current) => {
        current.schedule = current.schedule.concat({
            id,
            title: clean,

            // Exactly what the server does with it: keep the two it knows,
            // drop everything else rather than refuse the request.
            category: category === 'movie' || category === 'game' ? category : '',
            who: current.me.username === 'victor' ? 'Victor' : current.me.username,
            userId: ME_ID,
            startsAt,
            media: media ? detailsOf(media) || undefined : undefined,
        });
    });

    // Since 3.2.0 the new plan's id comes back, for the sheet's offer to
    // share it (design/3.2.0.md 6.6).
    return {ok: true, id};
});

// The lab's lookups: a small catalogue, answered after the usual latency, and
// art drawn on a canvas. 'not configured' in the Server row stands in for no
// TMDB token: movies become unavailable, games still work. Games come from
// IGDB as well while me.media.igdb says a Twitch application is set up.
export const searchMedia = (kind: 'movie' | 'game', query: string, signal?: AbortSignal): Promise<{results: MediaResult[]; unavailable?: string; incomplete?: string}> =>
    answer(() => {
        if (signal?.aborted) {
            throw new DOMException('Aborted', 'AbortError');
        }

        if (kind === 'movie' && !getWorld().configured) {
            return {results: [], unavailable: 'Movies and shows cannot be looked up on this server yet.'};
        }

        // 'IGDB slow': Steam's games alone, saying IGDB is missing from them.
        if (kind === 'game' && getWorld().igdbSlow && getWorld().me.media?.igdb) {
            return {results: searchCatalogue(kind, query, false), incomplete: 'IGDB'};
        }

        return {results: searchCatalogue(kind, query, Boolean(getWorld().me.media?.igdb))};
    });

// Switched off means switched off, as on the server, which stops relaying art
// the moment an admin saves: a picture the lab will not hand over is an empty
// address, which fails to load like the server's 404 and shows the stand-in.
// An IGDB game also needs IGDB, as the server's allowsRef says.
function lookupAllows(ref: string): boolean {
    const media = getWorld().me.media;

    if (ref.startsWith('steam:')) {
        return Boolean(media?.games);
    }
    if (ref.startsWith('igdb:')) {
        return Boolean(media?.games && media?.igdb);
    }

    return Boolean(media?.movies);
}

export const mediaImageURL = (ref: string, size: 'thumb' | 'cover' | 'backdrop'): string => (lookupAllows(ref) ? artOf(ref, size) : '');

// Remembered titles (media lookups): the System Console card's counts, from
// the lab's world, and forgetting them, refused in the server's words while
// 'forget fails' is on. Admin only, as on the server, which works whether or
// not lookups are on.
const ONLY_ADMINS_REMEMBER = 'Only a system admin can see or forget remembered titles.';

export const fetchMediaCache = (): Promise<MediaCacheStatus> => answer(() => {
    const world = getWorld();
    if (!world.me.isAdmin) {
        throw new Error(ONLY_ADMINS_REMEMBER);
    }

    return {...world.mediaCache, byProvider: {...world.mediaCache.byProvider}};
});

export const forgetMediaCache = (): Promise<{ok: boolean; forgotten: number; picturesForgotten?: number}> => answer(() => {
    const world = getWorld();
    if (!world.me.isAdmin) {
        throw new Error(ONLY_ADMINS_REMEMBER);
    }
    if (world.mediaCacheForgetFails) {
        throw new Error('Some remembered titles could not be forgotten just now. Try again.');
    }

    const before = world.mediaCache;
    change((current) => {
        // Memory only stays memory only: there was nothing on disk to forget.
        current.mediaCache = before.picturesKept === false ?
            {...forgottenMediaCache(), picturesKept: false, pictures: undefined, pictureBytes: undefined, pictureLimitBytes: undefined, pictureLimit: undefined} :
            forgottenMediaCache();
    });

    return {ok: true, forgotten: before.titles + before.searches + before.missing, picturesForgotten: before.pictures || 0};
});

// What's on?, by the server's rules (nowplaying.go): only with a stream
// linked; a kind and some words, both needed; a picked title only under its
// own icon, looked up here rather than taken from the panel, and kept as it
// was when the same one is saved again. Taking it down never needs the link.
const ON_NOW_NEEDS_LINK = 'Link your stream first — saying what is on is for streamers.';

export const fetchNowPlaying = (): Promise<NowPlayingAnswer> => answer(() => {
    const world = getWorld();
    if (!world.me.streamKey) {
        throw new Error(ON_NOW_NEEDS_LINK);
    }

    return {nowPlaying: world.nowPlaying[ME_ID] || null};
});

export const saveNowPlaying = (category: string, title: string, media?: string): Promise<NowPlayingAnswer> => write(() => {
    const world = getWorld();
    if (!world.me.streamKey) {
        throw new Error(ON_NOW_NEEDS_LINK);
    }
    if (category !== 'movie' && category !== 'game') {
        throw new Error('Say whether it is a movie or a game.');
    }

    const words = Array.from(title.trim()).slice(0, 80).join('').trim();
    if (words === '') {
        throw new Error('Say what is on.');
    }

    let details: NowPlaying['media'];
    if (media) {
        if ((media.startsWith('steam:') || media.startsWith('igdb:') ? 'game' : 'movie') !== category) {
            throw new Error('A movie or show goes with the film icon, and a game with the game icon.');
        }

        const stored = world.nowPlaying[ME_ID]?.media;
        if (stored?.ref === media) {
            details = stored;
        } else if (lookupAllows(media)) {
            details = detailsOf(media) || undefined;
        }
    }

    const saved: NowPlaying = {category, title: words, ...(details ? {media: details} : {})};
    change((current) => {
        current.nowPlaying = {...current.nowPlaying, [ME_ID]: saved};
    });

    return {nowPlaying: saved};
});

export const clearNowPlaying = (): Promise<NowPlayingAnswer> => write(() => {
    change((current) => {
        const {[ME_ID]: _gone, ...rest} = current.nowPlaying;
        current.nowPlaying = rest;
    });

    return {nowPlaying: null};
});

export const editPlan = (id: string, title: string, startsAt: number, category: string, media?: string): Promise<{ok: boolean}> => write(() => {
    if (!getWorld().me.streamKey) {
        throw new Error('Link your stream first — changing a plan is for streamers. Cancelling it never needs one.');
    }

    const clean = title.trim();
    if (clean === '') {
        throw new Error('say what you are streaming');
    }
    if (startsAt <= Date.now() || startsAt > Date.now() + HORIZON_MS) {
        throw new Error('pick a time in the next ninety days');
    }

    const existing = getWorld().schedule.find((item) => item.id === id);
    if (!existing) {
        throw new Error('that plan is not there any more');
    }
    if (existing.userId !== ME_ID) {
        throw new Error('only the person who planned it can change it');
    }

    change((current) => {
        current.schedule = current.schedule.map((item) => (item.id === id ? {
            ...item,
            title: clean,
            startsAt,
            category: category === 'movie' || category === 'game' ? category : '',
            media: media ? (item.media?.ref === media ? item.media : detailsOf(media) || undefined) : undefined,
        } : item));
    });

    return {ok: true};
});

export const joinPlan = (id: string, join: boolean): Promise<{ok: boolean}> => write(() => {
    const world = getWorld();
    if (!world.me.streamKey) {
        throw new Error('Link your stream first — joining is for streamers.');
    }

    const plan = world.schedule.find((item) => item.id === id);
    if (!plan) {
        throw new Error('that plan is not there any more');
    }
    if (plan.userId === ME_ID) {
        throw new Error('it is your own plan');
    }

    change((current) => {
        current.schedule = current.schedule.map((item) => {
            if (item.id !== id) {
                return item;
            }

            const others = (item.joined || []).filter((person) => person.userId !== ME_ID);

            return {...item, joined: join ? others.concat({userId: ME_ID, who: 'Victor'}) : others};
        });
    });

    return {ok: true};
});

export const unplanStream = (id: string): Promise<{ok: boolean}> => write(() => {
    change((current) => {
        current.schedule = current.schedule.filter((item) => item.id !== id);
    });

    return {ok: true};
});

export const fetchMyProfile = (): Promise<Profile> => answer(profileOf);

export const saveMyProfile = (motd: string, isPublic: boolean): Promise<Profile> => write(() => {
    change((current) => {
        current.profile = {...current.profile, motd, isPublic};
    });

    return getWorld().profile;
});

// ---- Twitch (design/twitch-live.md 4.1), answered from the world ----

// The connect ticket, after a moment, by the server's rules in their order:
// linked only, the switch, a callback address, not blocked, the limiter
// (the lab's "ticket refused" switch stands for it).
export const fetchConnectTicket = (): Promise<{ticket: string; expiresIn: number}> => new Promise((resolve, reject) => {
    window.setTimeout(() => {
        const world = getWorld();
        try {
            accessGate(true);
            if (!labTwitchOn(world)) {
                throw refused(404, '', 'Connecting Twitch accounts is switched off on this server.');
            }
            if (world.twitchCallbackProblem) {
                throw refused(503, '', world.me.isAdmin ? world.twitchCallbackProblem : 'An admin needs to finish setting up Twitch.');
            }
            if (world.twitchBlocked[ME_ID]) {
                throw refused(403, '', 'An admin has turned off Connect Twitch for your account.');
            }
            if (world.twitchTicket === 'refused') {
                throw refused(429, '', 'Too many tries. Wait a few minutes and press Connect Twitch again.');
            }
        } catch (reason) {
            reject(reason);

            return;
        }

        resolve({ticket: '1ab0'.repeat(16), expiresIn: 600});
    }, 300);
});

export const twitchConnectURL = (ticket: string): string =>
    `${PLUGIN_BASE}/api/v1/twitch/connect?t=${encodeURIComponent(ticket)}`;

// Disconnecting, open to everybody whatever the switch says: the link goes,
// and the hours with it (the purge, at once in the lab).
function disconnectTwitch(current: ReturnType<typeof getWorld>, userId: string): void {
    const {[userId]: _link, ...links} = current.twitchLinks;
    const {[userId]: _live, ...live} = current.twitchLive;
    const {[userId]: _seconds, ...seconds} = current.twitchSeconds;
    current.twitchLinks = links;
    current.twitchLive = live;
    current.twitchSeconds = seconds;
}

export const unlinkTwitch = (): Promise<{ok: boolean}> => write(() => {
    change((current) => disconnectTwitch(current, ME_ID));

    return {ok: true};
});

export const dismissTwitchNotice = (): Promise<{ok: boolean}> => write(() => {
    change((current) => {
        current.twitchLost = null;
    });

    return {ok: true};
});

// ---- Steam and Hytale (design/3.4.0.md 2.4-2.9 and 6.3), from the world ----

const ACCOUNT_NAME: Record<AccountService, string> = {steam: 'Steam', hytale: 'Hytale'};

// The ticket, by the server's rules in their order (2.5 T1-T7): linked only,
// an interactive session, the switch, set up, not blocked, the limiter; and
// the store, by the lab's Connect accounts row. Each refusal is the server's
// sentence with its code (6.3).
export const fetchAccountTicket = (service: AccountService): Promise<{ticket: string; expiresIn: number}> => new Promise((resolve, reject) => {
    window.setTimeout(() => {
        const world = getWorld();
        const word = ACCOUNT_NAME[service];
        try {
            accessGate(true);
            if (world.accountTicket === 'interactive_only') {
                throw refused(403, 'interactive_only', `Connect ${word} while signed in with your password or single sign-on, not a personal access token.`);
            }
            // Off is the switch with its secrets set, as the server's steamOn
            // and hytaleOn are; set up is then the Site URL.
            if (!(service === 'steam' ? labSteamOn(world) : labHytaleOn(world))) {
                throw refused(404, 'off', `Connecting ${word} accounts is switched off on this server.`);
            }
            const callback = service === 'steam' ? world.steamCallbackProblem : world.hytaleCallbackProblem;
            if (callback) {
                throw refused(503, 'setup', world.me.isAdmin ? callback : `An admin needs to finish setting up ${word}.`);
            }
            if ((service === 'steam' ? world.steamBlocked : world.hytaleBlocked)[ME_ID]) {
                throw refused(403, 'blocked', `An admin has turned off Connect ${word} for your account.`);
            }
            if (world.accountTicket === 'rate_limited') {
                throw refused(429, 'rate_limited', `Too many tries. Wait a few minutes and press Connect ${word} again.`);
            }
            if (world.accountTicket === 'store') {
                throw refused(503, 'store', `${serverName()} could not save that just now. Try again in a moment.`);
            }
        } catch (reason) {
            reject(reason);

            return;
        }

        resolve({ticket: (service === 'steam' ? '5a7e' : '4b7e').repeat(16), expiresIn: 600});
    }, 300);
});

export const accountConnectURL = (service: AccountService, ticket: string): string =>
    `${PLUGIN_BASE}/api/v1/${service}/connect?t=${encodeURIComponent(ticket)}`;

// One write deletes the entry, the status in memory and every takeover
// notice naming the same account (2.6, 2.7).
function disconnectAccount(current: ReturnType<typeof getWorld>, service: AccountService, userId: string): void {
    if (service === 'steam') {
        const link = current.steamLinks[userId];
        const {[userId]: _link, ...links} = current.steamLinks;
        const {[userId]: _status, ...status} = current.steamStatus;
        current.steamLinks = links;
        current.steamStatus = status;
        if (link && current.steamLost?.name === link.name) {
            current.steamLost = null;
        }
    } else {
        const link = current.hytaleLinks[userId];
        const {[userId]: _link, ...links} = current.hytaleLinks;
        current.hytaleLinks = links;
        if (link && current.hytaleLost?.name === link.name) {
            current.hytaleLost = null;
        }
    }
}

// Open to everybody, whatever the switch says, and idempotent. The lab's
// "Disconnect fails" stands for the store refusing the write.
export const unlinkAccount = (service: AccountService): Promise<{ok: boolean}> => write(() => {
    if (getWorld().accountUnlinkFails) {
        throw refused(503, 'store', `${serverName()} could not disconnect ${ACCOUNT_NAME[service]} just now. Try again in a moment.`);
    }
    change((current) => disconnectAccount(current, service, ME_ID));

    return {ok: true};
});

export const dismissAccountNotice = (service: AccountService): Promise<{ok: boolean}> => write(() => {
    change((current) => {
        if (service === 'steam') {
            current.steamLost = null;
        } else {
            current.hytaleLost = null;
        }
    });

    return {ok: true};
});

// Everybody's, for readers who may watch: the gate's 403 for the locked.
export const fetchAccounts = (): Promise<AccountsView> => answer(() => {
    accessGate(false);

    return accountsViewOf(getWorld());
});

// The lab's server keeps no tags: a read that says which copy is in hand is
// answered whole every time, as a real server's is when something changed.
export const fetchStreamsIfChanged = (_tag: string): Promise<{value: Snapshot; tag: string} | null> =>
    fetchStreams().then((value) => ({value, tag: ''}));

export const fetchAccountsIfChanged = (_tag: string): Promise<{value: AccountsView; tag: string} | null> =>
    fetchAccounts().then((value) => ({value, tag: ''}));

// The boards, as the server answers for them: "that one still" while not even
// a second on them has moved.
export const fetchBoardsIfChanged = (tag: string): Promise<{value: Boards; tag: string} | null> => answer(() => {
    const value = boardsOf();
    const now = boardsTagOf(value);

    return tag === now ? null : {value, tag: now};
});

// The small read about the reader: their access, as "me" has it, and what
// the server is called, its artwork, its release and the app check's word.
export const fetchAccess = (): Promise<{access?: Me['access']; configured?: boolean; name?: string; artwork?: Me['artwork']; release?: Me['release']; app?: Me['app']}> =>
    fetchMe().then((me) => ({access: me.access, configured: me.configured, contract: 4, version: me.release.version, name: me.name, artwork: me.artwork, release: me.release, app: me.app}));

// ---- What system admins are newly told (the Admin news row) ----
// Whether the plugin is a signed release, as the lab's server says: the
// manifest too whenever there is a release folder. The row's "cannot read" is
// a proxy in front of a signed release failing this one read, as the panel's
// client words an answer with no sentence.
export const fetchRelease = (): Promise<Me['release'] & {reason?: string; manifest?: string; rsa?: string; mldsa87?: string}> => answer(() => {
    if (labAdminNews.release === 'unreadable') {
        throw new RequestError('Request failed (502)', 502, '', '');
    }

    return labReleaseAnswer();
});

// The app check's verdicts, for a system admin; the store not answering for
// the row's "error", and the server's refusal for anybody else for "refused".
export const fetchAdminAppCheck = (): Promise<AdminAppCheck> => answer(() => {
    if (labAdminNews.appCheck === 'refused') {
        throw refused(403, 'forbidden', 'Only a system admin can see the app check\'s results.');
    }
    const view = labAppCheckView();
    if (!view) {
        throw refused(503, 'store', 'This server could not read its app check records. Try again in a moment.');
    }

    return view as AdminAppCheck;
});

// Push notifications' state, for a system admin, as the Push notifications
// row has it; the store not answering for "cannot read", and the server's
// refusal for anybody else for "refused".
export const fetchAdminPush = (): Promise<AdminPush> => answer(() => {
    if (labAdminNews.push === 'refused') {
        throw refused(403, 'forbidden', 'Only a system admin can see push notifications\' state.');
    }
    const view = labPushView();
    if (!view) {
        throw refused(503, 'store', 'This server could not read or write its push records. Try again in a moment.');
    }

    return view;
});

// The Mattermost name an admin card shows: first and last, as the console
// does.
function fullNameOf(userId: string): string {
    const user = LAB_USERS.find((candidate) => candidate.id === userId);
    const full = [user?.first_name, user?.last_name].filter(Boolean).join(' ');

    return full || user?.nickname || user?.username || userId;
}

// As the server's adminAccountPeople: somebody whose Mattermost account is
// gone for good comes back with no name, and deleted.
function adminAccountPeople(links: Record<string, {id: string; name: string; at: number}>, service: AccountService) {
    const world = getWorld();

    return Object.entries(links).map(([userId, link]) => ({
        userId,
        name: world.inactive[userId] === 'deleted' ? '' : fullNameOf(userId),
        ...(service === 'steam' ? {steamName: link.name, url: `https://steamcommunity.com/profiles/${link.id}`} : {hytaleName: link.name}),
        since: link.at,
        active: !world.inactive[userId],
        deleted: world.inactive[userId] === 'deleted',
    })).sort((a, b) => a.name.localeCompare(b.name) || a.userId.localeCompare(b.userId));
}

function adminAccountBlocked(blocked: Record<string, number>) {
    const world = getWorld();

    return Object.entries(blocked).map(([userId, at]) => ({
        userId,
        name: world.inactive[userId] === 'deleted' ? '' : fullNameOf(userId),
        since: at,
    })).sort((a, b) => a.name.localeCompare(b.name) || a.userId.localeCompare(b.userId));
}

// System Console → Steam connections (3.8): how many active people Steam was
// asked about and answered for, never anybody's status.
export const fetchAdminSteam = (): Promise<AdminSteam> => answer(() => {
    twitchAdminGate();
    const world = getWorld();
    const on = labSteamOn(world);
    const asked = Object.keys(world.steamLinks).filter((userId) => !world.inactive[userId] && !world.steamBlocked[userId]).length;
    const good = on && world.steamProblem === '' && world.steamKeyProblem === '' && asked > 0;

    return {
        enabled: world.steamSwitch,
        keySet: world.steamKeySet,
        keyProblem: world.steamKeyProblem,
        callbackUrl: world.steamCallbackProblem ? '' : `https://chat.example.com/plugins/${PLUGIN_ID}/api/v1/steam/callback`,
        realm: world.steamCallbackProblem ? '' : 'https://chat.example.com',
        callbackProblem: world.steamCallbackProblem,
        lastTry: on && asked > 0 ? Date.now() - 40000 : 0,
        lastGood: good ? Date.now() - 40000 : (on && asked > 0 ? Date.now() - (14 * 60000) : 0),
        problem: on ? (world.steamKeyProblem || world.steamProblem) : '',
        // The day's calls stay counted when the switch goes off, as the
        // server's do: they were made.
        callsToday: world.steamCallsToday,
        asked: good ? asked : 0,
        answered: good ? Math.max(0, asked - world.steamMissed) : 0,
        people: adminAccountPeople(world.steamLinks, 'steam'),
        blocked: adminAccountBlocked(world.steamBlocked),
        quarantined: world.steamQuarantined,
    };
});

// System Console → Hytale connections (4.7).
export const fetchAdminHytale = (): Promise<AdminHytale> => answer(() => {
    twitchAdminGate();
    const world = getWorld();

    return {
        enabled: world.hytaleSwitch,
        credentials: world.hytaleCredentials,
        callbackUrl: world.hytaleCallbackProblem ? '' : `https://chat.example.com/plugins/${PLUGIN_ID}/api/v1/hytale/callback`,
        callbackProblem: world.hytaleCallbackProblem,
        problem: labHytaleOn(world) ? world.hytaleProblem : '',
        people: adminAccountPeople(world.hytaleLinks, 'hytale'),
        blocked: adminAccountBlocked(world.hytaleBlocked),
        quarantined: world.hytaleQuarantined,
    };
});

// Disconnect, and Disconnect and block: whatever the switch says.
export const adminUnlinkAccount = (service: AccountService, userId: string, block = false): Promise<{ok: boolean}> => write(() => {
    twitchAdminGate();
    change((current) => {
        disconnectAccount(current, service, userId);
        if (block && service === 'steam') {
            current.steamBlocked = {...current.steamBlocked, [userId]: Date.now()};
        } else if (block) {
            current.hytaleBlocked = {...current.hytaleBlocked, [userId]: Date.now()};
        }
    });

    return {ok: true};
});

// Everything of that service's but the block list, the set-aside copy
// included.
export const deleteAllAccount = (service: AccountService): Promise<{ok: boolean}> => write(() => {
    twitchAdminGate();
    change((current) => {
        if (service === 'steam') {
            current.steamLinks = {};
            current.steamStatus = {};
            current.steamLost = null;
            current.steamQuarantined = false;
        } else {
            current.hytaleLinks = {};
            current.hytaleLost = null;
            current.hytaleQuarantined = false;
        }
    });

    return {ok: true};
});

// Blocking disconnects too.
export const adminBlockAccount = (service: AccountService, userId: string, blocked: boolean): Promise<{ok: boolean}> => write(() => {
    twitchAdminGate();
    change((current) => {
        if (blocked) {
            disconnectAccount(current, service, userId);
        }
        const list = {...(service === 'steam' ? current.steamBlocked : current.hytaleBlocked)};
        if (blocked) {
            list[userId] = Date.now();
        } else {
            delete list[userId];
        }
        if (service === 'steam') {
            current.steamBlocked = list;
        } else {
            current.hytaleBlocked = list;
        }
    });

    return {ok: true};
});

export const adminClearAccountBlocks = (service: AccountService): Promise<{ok: boolean}> => write(() => {
    twitchAdminGate();
    change((current) => {
        if (service === 'steam') {
            current.steamBlocked = {};
        } else {
            current.hytaleBlocked = {};
        }
    });

    return {ok: true};
});

// A Twitch stream's preview, as the server relays it: drawn here, a colour
// of its own per stream, so a card and a plate can be told apart.
const twitchPreviews: Record<string, string> = {};

export const twitchPreviewURL = (handle: string, version: string): string => {
    void version;
    if (twitchPreviews[handle] || typeof document === 'undefined') {
        return twitchPreviews[handle] || '';
    }

    const canvas = document.createElement('canvas');
    canvas.width = 640;
    canvas.height = 360;
    const g = canvas.getContext('2d');
    if (!g) {
        return '';
    }

    let hash = 7;
    for (const char of handle) {
        hash = ((hash * 31) + char.charCodeAt(0)) >>> 0;
    }
    const hue = hash % 360;
    const sky = g.createLinearGradient(0, 0, 0, 360);
    sky.addColorStop(0, `hsl(${hue} 45% 30%)`);
    sky.addColorStop(0.6, `hsl(${(hue + 30) % 360} 55% 18%)`);
    sky.addColorStop(1, '#060606');
    g.fillStyle = sky;
    g.fillRect(0, 0, 640, 360);

    // A road running into the distance, for something picture-like.
    g.fillStyle = 'rgba(20, 20, 24, 0.9)';
    g.beginPath();
    g.moveTo(200, 360);
    g.lineTo(300, 200);
    g.lineTo(340, 200);
    g.lineTo(520, 360);
    g.fill();
    g.strokeStyle = 'rgba(255, 255, 255, 0.7)';
    g.setLineDash([18, 16]);
    g.lineWidth = 4;
    g.beginPath();
    g.moveTo(360, 360);
    g.lineTo(320, 200);
    g.stroke();
    g.setLineDash([]);
    g.fillStyle = `hsl(${(hue + 180) % 360} 80% 60% / 0.8)`;
    g.beginPath();
    g.arc(470, 110, 34, 0, Math.PI * 2);
    g.fill();
    twitchPreviews[handle] = canvas.toDataURL('image/jpeg', 0.82);

    return twitchPreviews[handle];
};

function twitchAdminGate(): void {
    if (!getWorld().me.isAdmin) {
        throw refused(403, '', 'Only a system admin can do that.');
    }
}

export const fetchAdminTwitch = (): Promise<AdminTwitch> => answer(() => {
    twitchAdminGate();
    const world = getWorld();
    const on = labTwitchOn(world);
    const byName = (a: {name: string}, b: {name: string}) => a.name.localeCompare(b.name);
    const seconds = Object.values(world.twitchSeconds).filter((value) => value > 0);

    return {
        enabled: world.twitchSwitch,
        embed: world.twitchEmbed,
        credentials: world.twitchCredentials,
        // As the server writes it from the Site URL: the plugin's real
        // address, not the lab's PLUGIN_BASE.
        callbackUrl: world.twitchCallbackProblem ? '' : `https://chat.example.com/plugins/${PLUGIN_ID}/api/v1/twitch/callback`,
        callbackProblem: world.twitchCallbackProblem,
        embedProblem: world.twitchEmbedProblem,
        lastTry: on && Object.keys(world.twitchLinks).length > 0 ? Date.now() - 40000 : 0,
        lastGood: on && !world.twitchProblem && Object.keys(world.twitchLinks).length > 0 ? Date.now() - 40000 : 0,
        problem: on ? world.twitchProblem : '',
        people: Object.entries(world.twitchLinks).map(([userId, link]) => ({
            userId,
            name: personOf(userId).displayName,
            login: link.login,
            twitchName: link.name,
            since: link.at,
            live: on && Boolean(world.twitchLive[userId]),
            allDay: link.allDay,
        })).sort(byName),
        blocked: Object.entries(world.twitchBlocked).map(([userId, at]) => ({userId, name: personOf(userId).displayName, since: at})).sort(byName),
        hours: {
            channels: seconds.length,
            seconds: seconds.reduce((sum, value) => sum + value, 0),
            halted: world.twitchHalted,
            purging: world.twitchPurging,
            quarantined: world.twitchQuarantined,
        },
    };
});

export const adminUnlinkTwitch = (userId: string, block = false): Promise<{ok: boolean}> => write(() => {
    twitchAdminGate();
    change((current) => {
        disconnectTwitch(current, userId);
        if (block) {
            current.twitchBlocked = {...current.twitchBlocked, [userId]: Date.now()};
        }
    });

    return {ok: true};
});

// Everything Twitch, except the block list, which holds no Twitch data.
export const deleteAllTwitch = (): Promise<{ok: boolean}> => write(() => {
    twitchAdminGate();
    change((current) => {
        current.twitchLinks = {};
        current.twitchLive = {};
        current.twitchSeconds = {};
        current.twitchLost = null;
        current.twitchHalted = '';
        current.twitchPurging = 0;
        current.twitchQuarantined = false;
    });

    return {ok: true};
});

export const adminBlockTwitch = (userId: string, blocked: boolean): Promise<{ok: boolean}> => write(() => {
    twitchAdminGate();
    change((current) => {
        if (blocked) {
            disconnectTwitch(current, userId);
            current.twitchBlocked = {...current.twitchBlocked, [userId]: Date.now()};
        } else {
            const {[userId]: _gone, ...rest} = current.twitchBlocked;
            current.twitchBlocked = rest;
        }
    });

    return {ok: true};
});

export const adminClearTwitchBlocks = (): Promise<{ok: boolean}> => write(() => {
    twitchAdminGate();
    change((current) => {
        current.twitchBlocked = {};
    });

    return {ok: true};
});

export const adminTwitchAllDay = (userId: string, allDay: boolean): Promise<{ok: boolean}> => write(() => {
    twitchAdminGate();
    change((current) => {
        const link = current.twitchLinks[userId];
        if (!link) {
            throw refused(404, '', 'That person has no Twitch account connected.');
        }
        current.twitchLinks = {...current.twitchLinks, [userId]: {...link, allDay}};
    });

    return {ok: true};
});

// Mattermost's own search, answered the way its database search answers a
// hashtag: the whole hashtag, in any case, in the team's channels and the
// reader's direct and group messages, archived channels only when asked,
// newest first. It pages the way Elasticsearch does — the database sends
// everything at once — so that "Load more" has something to load, and like
// Elasticsearch it drops deleted messages only after cutting the page
// (store/searchlayer/post_layer.go), so a page can come back short with more
// after it. The modes are the lab's Hashtag search row.
function aborted(signal?: AbortSignal): void {
    if (signal?.aborted) {
        throw new DOMException('Aborted', 'AbortError');
    }
}

export const searchPosts = (search: PostSearch, signal?: AbortSignal): Promise<PostSearchPage> => answer(() => {
    aborted(signal);

    const world = getWorld();
    if (world.tagSearch === 'fails') {
        throw new Error('Mattermost is too busy to search right now. Try again in a moment.');
    }
    if (world.tagSearch === 'off') {
        throw new Error('Searching messages is switched off on this server.');
    }
    if (world.tagSearch === 'none') {
        return {order: [], posts: {}};
    }

    const wanted = search.terms.replace(/^#+/, '').toUpperCase();
    const channels = new Map(LAB_CHANNELS.map((channel) => [channel.id, channel]));
    const matching = LAB_POSTS.
        filter((post) => {
            const channel = channels.get(post.channel_id);
            if (!channel || (search.teamId !== '' && channel.team_id !== '' && channel.team_id !== search.teamId)) {
                return false;
            }
            if ((channel.delete_at || 0) > 0 && !search.archived) {
                return false;
            }

            return findHashtags(post.message).some((found) => found.tag.toUpperCase() === wanted);
        }).
        sort((a, b) => b.create_at - a.create_at).
        slice(search.page * search.perPage, (search.page + 1) * search.perPage).
        filter((post) => post.props?.labDeleted !== true);

    const posts: Record<string, MattermostPost> = {};
    matching.forEach((post) => {
        posts[post.id] = post;
    });

    return {order: matching.map((post) => post.id), posts};
});

export const fetchUsersByIds = (ids: string[], signal?: AbortSignal): Promise<MattermostUser[]> => answer(() => {
    aborted(signal);

    return [...LAB_USERS, ...LAB_STEAM_FRIENDS].filter((user) => ids.includes(user.id));
});

export const fetchChannel = (channelId: string, signal?: AbortSignal): Promise<MattermostChannel> => answer(() => {
    aborted(signal);

    const channel = LAB_CHANNELS.find((item) => item.id === channelId);
    if (!channel) {
        throw new Error('Mattermost could not answer (404)');
    }

    return channel;
});

// Never reached: the WHEP mock paints a canvas rather than negotiating. Here so
// the module's surface matches the one it replaces.
export async function exchangeWHEP(): Promise<string> {
    throw new Error('The lab does not negotiate WebRTC.');
}

// ---------------------------------------------------------------- clips

// The clip routes, answered from the world by the server's rules (clips_api.go
// and the addendum): every panel route behind the link, every System Console
// route behind the admin check and nothing else, in the server's order, with
// its codes and its fixed texts. Refusals are ClipRequestErrors, as the real
// client's are, so the panel's wording for each code can be looked at.

export class ClipRequestError extends Error {
    status: number;
    code: ClipErrorCode;
    serverText: string;

    constructor(status: number, code: ClipErrorCode, serverText = '') {
        super(serverText || `Clip request failed (${status === 0 ? 'no answer' : status})`);
        this.name = 'ClipRequestError';
        this.status = status;
        this.code = code;
        this.serverText = serverText;
    }
}

// The server's fixed text for each code (clips.go clipErrorTexts).
const CLIP_TEXT: Partial<Record<ClipErrorCode, string>> = {
    clips_off: 'Clipping has been switched off.',
    blocked: 'An admin has stopped you making clips.',
    forbidden: 'That is not allowed.',
    not_allowed: 'Only the person who made it, the streamer or an admin can do that.',
    own_clip: 'You can\'t upvote your own clip.',
    not_watching: 'Tune in to this stream to clip it.',
    streamer_off: 'The streamer has switched clipping off.',
    not_live: 'That stream is not live right now.',
    not_clippable: 'This stream can\'t be clipped.',
    warming_up: 'Clipping starts a few seconds after a stream does. Try again in a moment.',
    draft_cap: 'You already have 3 drafts. Publish or discard one first.',
    duplicate: 'You have already published most of this moment.',
    publishing: 'This clip is already being published.',
    draft_gone: 'This draft has expired.',
    draft_wiped: 'The streamer wiped this part of their stream.',
    tail_gone: 'The end of that stream is no longer held.',
    clip_gone: 'This clip has gone.',
    too_short: 'A clip has to be at least 5 seconds long.',
    bad_range: 'That request did not make sense.',
    bad_request: 'That request did not make sense.',
    bad_thumb: 'That picture is not usable.',
    rate_limited: 'Too many at once. Wait a moment and try again.',
    recording_failed: 'That stream could not be clipped just now.',
    store: 'Clips could not be saved just now. Try again.',
    storage_full: 'The server is out of room for clips.',
    user_storage_full: 'You are using your share of clip space. Delete one of your clips first.',
    not_linked: 'Link your account first — clips are for people who have linked their stream.',
    get locked(): string {
        return ACCESS_TEXT.locked();
    },
    watch_only: 'You are let in to watch only. Link your stream to do that.',
    cannot_post: 'You can\'t post in that channel.',
    share_cap: 'This clip has already been shared in 10 places.',
};

const refuse = (status: number, code: ClipErrorCode): ClipRequestError => new ClipRequestError(status, code, CLIP_TEXT[code] || '');

// No answer at all: what the real client makes of a fetch that failed.
const noAnswer = (): ClipRequestError => new ClipRequestError(0, 'network');

const CLIP_ID = /^[0-9a-f]{16}$/;

// The access gate in front of every panel route (requireClipsAccess): the
// five that only look are open to people let in, and every other one needs a
// link. Locked readers are refused with locked, let-in ones with watch_only,
// and the page is told, as the real client tells it. The System Console's
// admin check asks for neither.
function clipAccessRefusal(needsLink: boolean): void {
    const access = labAccessOf(getWorld());
    if (access === 'linked' || (access === 'letIn' && !needsLink)) {
        return;
    }

    const code = access === 'letIn' ? 'watch_only' : 'locked';
    labRefusal(code);
    throw refuse(403, code);
}

function linkGate(): void {
    clipAccessRefusal(true);
}

function watchGate(): void {
    clipAccessRefusal(false);
}

function adminGate(): void {
    if (!getWorld().me.isAdmin) {
        throw refuse(403, 'forbidden');
    }
}

// One clip request: the gate first, as on the server, then for a write the
// lab's "writes fail" switch as a store failure, then the route itself. Every
// panel route is counted (labCounters().clips), so "nothing is asked for
// while unlinked" can be read off the lab's counters.
function clipCall<T>(gate: () => void, route: () => T, options: {write?: boolean; latencyMs?: number} = {}): Promise<T> {
    const world = getWorld();
    if (gate === linkGate || gate === watchGate) {
        labCounters().clips++;
    }

    return new Promise((resolve, reject) => {
        window.setTimeout(() => {
            try {
                gate();
                if (options.write && getWorld().failWrites) {
                    throw refuse(503, 'store');
                }
                resolve(route());
            } catch (reason) {
                reject(reason instanceof Error ? reason : new Error(String(reason)));
            }
        }, options.latencyMs ?? world.latencyMs);
    });
}

const fetchLatency = (): number | undefined => (getWorld().clipFetch === 'slow' ? 3000 : undefined);

// How the Clip fetch row says one clip, or a page of cards, is fetched.
function clipFetchMode(): void {
    switch (getWorld().clipFetch) {
    case 'fails':
        throw refuse(503, 'store');
    case 'network':
        throw noAnswer();
    default:
        break;
    }
}

function liveClip(id: string, now = Date.now()): LabClip | undefined {
    const world = getWorld();

    return world.clips.find((entry) => entry.id === id && now < clipExpiresAt(entry, world));
}

// The sample clip, as a blob of this page's own, once it has been made: a
// picture drawn from a same-origin blob leaves the canvas readable, so the
// editor's thumbnail capture works here as it does on the server. The data
// URL serves until then.
let sampleClipURL: string = sampleClipData;
if (typeof fetch === 'function' && typeof URL.createObjectURL === 'function') {
    fetch(sampleClipData).
        then((response) => response.blob()).
        then((blob) => {
            sampleClipURL = URL.createObjectURL(blob);
        }).
        catch(() => undefined);
}

// Each stream's colour on the pictures drawn for its clips.
const CLIP_COLOURS: Record<string, string> = {
    Victor: '#7a1022',
    Dave: '#1d4f7a',
    Bob: '#7a5714',
    Erin: '#1c6a4a',
    Carol: '#4c2a7c',
};

const drawn = new Map<string, string>();

// A clip's picture, drawn once: the stream's colour, the title and a play
// mark, 480 × 270 as the editor captures them.
function drawThumb(entry: LabClip): string {
    const key = `${entry.id}|${entry.thumb || ''}|${entry.title}`;
    const cached = drawn.get(key);
    if (cached !== undefined) {
        return cached;
    }

    const canvas = document.createElement('canvas');
    canvas.width = 480;
    canvas.height = 270;
    const context = canvas.getContext('2d');
    if (!context) {
        return '';
    }

    const colour = CLIP_COLOURS[entry.streamName] || '#333333';
    const fill = context.createLinearGradient(0, 0, 480, 270);
    fill.addColorStop(0, colour);
    fill.addColorStop(1, '#050505');
    context.fillStyle = fill;
    context.fillRect(0, 0, 480, 270);

    context.fillStyle = 'rgba(255, 255, 255, 0.85)';
    context.beginPath();
    context.arc(240, 120, 42, 0, Math.PI * 2);
    context.fill();
    context.fillStyle = colour;
    context.beginPath();
    context.moveTo(226, 98);
    context.lineTo(226, 142);
    context.lineTo(262, 120);
    context.closePath();
    context.fill();

    context.fillStyle = '#ffffff';
    context.font = '600 22px sans-serif';
    context.textAlign = 'center';
    const title = entry.title || `${entry.streamName}’s stream`;
    context.fillText(title.length > 36 ? `${title.slice(0, 35)}…` : title, 240, 215, 440);
    context.font = '14px sans-serif';
    context.fillStyle = 'rgba(255, 255, 255, 0.7)';
    context.fillText(entry.streamName, 240, 240, 440);

    const url = canvas.toDataURL('image/jpeg', 0.8);
    drawn.set(key, url);

    return url;
}

function thumbOf(entry: LabClip | undefined): string {
    if (!entry || !entry.thumb) {
        return '';
    }

    return entry.thumbUrl || drawThumb(entry);
}

// A clip's file and picture are fetched by the browser itself, through the
// same gate as every other route: for somebody locked, or a clip that has
// gone, there is nothing, and the <video> or <img> fails as it would on a 403.
const mayWatchClips = (): boolean => labAccessOf(getWorld()) !== 'locked';

// The Clip file row's two other states: a file that never arrives, as a
// MediaSource nobody feeds (one each time the state is chosen, since a
// MediaSource can be played only once), and one that is not a video at all,
// which fails as a file the browser cannot read.
let hangingURL = '';
subscribe(() => {
    if (getWorld().clipFile !== 'hang') {
        hangingURL = '';
    }
});

function clipFileURL(): string {
    const mode = getWorld().clipFile;
    if (mode === 'fail') {
        return 'data:video/mp4;base64,AAAAAAAAAAA=';
    }
    if (mode === 'hang' && typeof MediaSource !== 'undefined') {
        if (hangingURL === '') {
            hangingURL = URL.createObjectURL(new MediaSource());
        }

        return hangingURL;
    }

    return sampleClipURL;
}

export const clipVideoURL = (id: string): string => (mayWatchClips() && liveClip(id) ? clipFileURL() : '');

// The sample clip's own file, whatever the Clip file row says, for the lab
// to draw clips' pictures from (lab/clipPlayer.ts).
export const labSampleClipURL = (): string => sampleClipURL;

export const clipThumbURL = (id: string, v: string): string => {
    void v;

    return mayWatchClips() ? thumbOf(liveClip(id)) : '';
};

export const adminClipVideoURL = (id: string): string => (getWorld().me.isAdmin && liveClip(id) ? clipFileURL() : '');

export const adminClipThumbURL = (id: string, v: string): string => {
    void v;

    return getWorld().me.isAdmin ? thumbOf(liveClip(id)) : '';
};

const newestFirst = (a: LabClip, b: LabClip): number => b.createdAt - a.createdAt;

// The list goes as the Clip fetch row says too, so the dialog's loading and
// error states can be seen; "gone" is about one clip, and leaves it alone.
export const fetchClips = (): Promise<{clips: Clip[]}> => clipCall(watchGate, () => {
    clipFetchMode();
    const world = getWorld();

    return {clips: liveClipsOf(world, Date.now()).sort(newestFirst).map((entry) => clipViewOf(world, entry))};
}, {latencyMs: fetchLatency()});

export const fetchClip = (id: string): Promise<Clip> => clipCall(watchGate, () => {
    clipFetchMode();
    if (!CLIP_ID.test(id)) {
        throw refuse(400, 'bad_request');
    }

    const entry = getWorld().clipFetch === 'gone' ? undefined : liveClip(id);
    if (!entry) {
        throw refuse(410, 'clip_gone');
    }

    return clipViewOf(getWorld(), entry);
}, {latencyMs: fetchLatency()});

// The cards route answers 1 to 25 distinct ids per request and refuses
// anything else with 400 (clipCardIDs). The real client splits a longer list
// into requests of 25 at most and merges the answers, and so does this one,
// one counted request (labCounters().cards) per batch, so a channel of cards
// asks here as often as it would there.
const CARDS_PER_REQUEST = 25;

function cardsRoute(batch: string[]): Promise<{cards: Record<string, ClipCard | null>}> {
    return clipCall(watchGate, () => {
        labCounters().cards++;
        clipFetchMode();
        if (batch.length === 0 || batch.length > CARDS_PER_REQUEST || new Set(batch).size !== batch.length || batch.some((id) => !CLIP_ID.test(id))) {
            throw refuse(400, 'bad_request');
        }

        // 3.2.0 (design/3.2.0.md 9.2): the count of linked voters, the same
        // for everybody; whether this reader voted (a let-in reader's old
        // vote still shows); and whether they may: not let in, nor the
        // clipper on their own clip.
        const world = getWorld();
        const gone = world.clipFetch === 'gone';
        const linked = labAccessOf(world) === 'linked';
        const cards: Record<string, ClipCard | null> = {};
        batch.forEach((id) => {
            const entry = gone ? undefined : liveClip(id);
            cards[id] = entry ? {
                ...clipCardOf(entry),
                upvotes: shownVotes(world, entry),
                ...(ME_ID in entry.voters ? {voted: true} : {}),
                ...(linked && entry.clipperId !== ME_ID ? {canVote: true} : {}),
            } : null;
        });

        return {cards};
    }, {latencyMs: fetchLatency()});
}

export const fetchClipCards = async (ids: string[]): Promise<{cards: Record<string, ClipCard | null>}> => {
    const wanted = Array.from(new Set(ids));
    const batches: string[][] = [];
    for (let at = 0; at < wanted.length; at += CARDS_PER_REQUEST) {
        batches.push(wanted.slice(at, at + CARDS_PER_REQUEST));
    }

    const answers = await Promise.all(batches.map(cardsRoute));
    const cards: Record<string, ClipCard | null> = {};
    answers.forEach((answer) => Object.assign(cards, answer.cards));

    return {cards};
};

export const voteClip = (id: string, up: boolean): Promise<{upvotes: number; voted: boolean}> => clipCall(linkGate, () => {
    const entry = liveClip(id);
    if (!entry) {
        throw refuse(410, 'clip_gone');
    }
    if (entry.clipperId === ME_ID) {
        throw refuse(403, 'own_clip');
    }

    change(() => {
        if (up) {
            entry.voters = {...entry.voters, [ME_ID]: entry.voters[ME_ID] || Date.now()};
        } else {
            const {[ME_ID]: _gone, ...rest} = entry.voters;
            entry.voters = rest;
        }
    });

    return {upvotes: shownVotes(getWorld(), entry), voted: ME_ID in entry.voters};
}, {write: true});

export const reportClip = (id: string): Promise<{ok: boolean}> => clipCall(linkGate, () => {
    const entry = liveClip(id);
    if (!entry) {
        throw refuse(410, 'clip_gone');
    }
    if (entry.clipperId === ME_ID) {
        throw refuse(403, 'forbidden');
    }

    change(() => {
        entry.reports = {...entry.reports, [ME_ID]: entry.reports[ME_ID] || Date.now()};
    });

    return {ok: true};
}, {write: true});

// What the server's marker does to each post a clip was shared in once it has
// gone (addendum 4.5): says so, and for a deletion takes the title and the
// names out. The message, the server's fixed text, is never touched.
function markSharesGone(entry: LabClip, value: 'expired' | 'deleted'): void {
    const store = mattermostStore();
    const posts = (store?.getState() as {entities?: {posts?: {posts?: Record<string, MattermostPost>}}} | undefined)?.entities?.posts?.posts || {};
    entry.shares.forEach((postId) => {
        const post = posts[postId];
        if (!post || post.type !== CLIP_POST_TYPE || post.props?.villanet_clip_id !== entry.id) {
            return;
        }

        const props: Record<string, unknown> = {...post.props, villanet_clip_gone: value};
        if (value === 'deleted') {
            ['villanet_clip_title', 'villanet_clip_clipper', 'villanet_clip_clipper_id', 'villanet_clip_streamer', 'villanet_clip_streamer_id',
                'villanet_clip_tag', 'villanet_clip_tag_kind'].
                forEach((key) => delete props[key]);
        }
        store?.dispatch({type: 'RECEIVED_POST', data: {...post, props}});
    });
}

// Settling a clip (spec 2.9): its upvotes banked for its clipper when it
// expired or its streamer deleted it, voided when the clipper or an admin
// did; then it is gone, and so are its shared cards.
function settle(entry: LabClip, reason: 'clipper' | 'streamer' | 'admin' | 'expired'): void {
    change((current) => {
        if (reason === 'expired' || reason === 'streamer') {
            const counted = countedVotes(current, entry);
            const banked = current.clipBanked[entry.clipperId] || {up: 0, clips: 0, lastUpAt: 0};
            current.clipBanked = {
                ...current.clipBanked,
                [entry.clipperId]: {up: banked.up + counted.count, clips: banked.clips + 1, lastUpAt: Math.max(banked.lastUpAt, counted.latest)},
            };
        }
        current.clips = current.clips.filter((candidate) => candidate.id !== entry.id);
    });
    markSharesGone(entry, reason === 'expired' ? 'expired' : 'deleted');
}

// The panel's delete, in the addendum's order (3.3): gone already is fine and
// leaves an expired clip for the sweep to bank, then an admin who did not
// make it, then its clipper, then the streamer it was clipped from.
export const deleteClip = (id: string): Promise<{ok: boolean}> => clipCall(linkGate, () => {
    if (!CLIP_ID.test(id)) {
        throw refuse(400, 'bad_request');
    }

    const world = getWorld();
    const entry = liveClip(id);
    if (!entry) {
        return {ok: true};
    }

    if (world.me.isAdmin && entry.clipperId !== ME_ID) {
        settle(entry, 'admin');
    } else if (entry.clipperId === ME_ID) {
        settle(entry, 'clipper');
    } else if (world.me.streamKey !== '' && entry.streamKey === world.me.streamKey) {
        settle(entry, 'streamer');
    } else {
        throw refuse(403, 'not_allowed');
    }

    return {ok: true};
}, {write: true});

const SHARE_CAP = 10;
const SHARE_REPEAT_MS = 60000;

// Shares in the last minute, by clip and channel: a retry posts nothing new.
const recentShares = new Map<string, {postId: string; at: number}>();

// Sharing, in the server's order (addendum 4.2), with the Share row's modes.
// A new post arrives in the lab's Mattermost the way the server's would, over
// the WebSocket, in the channel it was shared in.
export const shareClip = (id: string, channelId: string): Promise<{postId: string; channelId: string}> => clipCall(linkGate, () => {
    const world = getWorld();
    if (!CLIP_ID.test(id) || channelId === '') {
        throw refuse(400, 'bad_request');
    }
    if (world.shareMode === 'network') {
        throw noAnswer();
    }

    const entry = world.shareMode === 'gone' ? undefined : liveClip(id);
    if (!entry) {
        throw refuse(410, 'clip_gone');
    }

    const now = Date.now();
    const repeatKey = `${id}|${channelId}`;
    const repeat = recentShares.get(repeatKey);
    if (repeat && now - repeat.at < SHARE_REPEAT_MS) {
        return {postId: repeat.postId, channelId};
    }

    if (world.shareMode === 'share_cap' || entry.shares.length >= SHARE_CAP) {
        throw refuse(409, 'share_cap');
    }

    const channel = channelOf(channelId) || LAB_CHANNELS.find((candidate) => candidate.id === channelId);
    if (world.shareMode === 'cannot_post' || !channel || (channel.delete_at || 0) > 0 || channel.shared === true) {
        throw refuse(403, 'cannot_post');
    }
    if (world.shareMode === 'rate') {
        throw refuse(429, 'rate_limited');
    }

    const post = {
        id: freshId('post'),
        channel_id: channelId,
        user_id: ME_ID,
        root_id: '',
        message: labShareMessage(entry),
        create_at: now,
        delete_at: 0,
        type: CLIP_POST_TYPE,
        // The server blanks the one-time pass rather than taking it out
        // (clips_share.go MessageWillBePosted), so every genuine post
        // carries it, empty.
        props: {...labShareProps(entry), villanet_share_nonce: ''},
    };
    const store = mattermostStore();
    store?.dispatch({type: 'RECEIVED_NEW_POST', data: post});

    // The clip went between the checks and the post: the server takes its
    // post back rather than leave one it cannot mark.
    if (world.shareMode === 'gone_on_arrival') {
        store?.dispatch({type: 'POST_REMOVED', data: {id: post.id}});
        throw refuse(410, 'clip_gone');
    }

    change(() => {
        entry.shares = entry.shares.concat(post.id);
    });
    recentShares.set(repeatKey, {postId: post.id, at: now});

    return {postId: post.id, channelId};
}, {write: true});

// ---- Drafts ----

const DRAFT_TTL_MS = 2 * 3600000;
const DRAFT_CAP = 3;

// The key a stream is published with, as the lab names them: a person's is
// "live-" and their username, as Victor's is; a stream nobody is linked
// to is known by its handle alone.
const streamKeyOf = (userId: string | undefined, handle: string): string =>
    (userId ? `live-${clipPersonOf(userId).username || ''}` : `live-${handle}`);

const freshClipId = (): string => Array.from({length: 16}, () => Math.floor(Math.random() * 16).toString(16)).join('');

function draftViewOf(draft: LabDraft): ClipDraft {
    const {owner: _owner, streamKey: _key, wiped: _wiped, thumbUrl: _thumb, ...view} = draft;

    return view;
}

const draftShowing = (draft: LabDraft, now: number): boolean => draft.owner === ME_ID && !draft.wiped && draft.expiresAt > now;

export const fetchDrafts = (): Promise<{drafts: ClipDraft[]}> => clipCall(linkGate, () => {
    const now = Date.now();

    return {drafts: getWorld().drafts.filter((draft) => draftShowing(draft, now) && draft.state !== 'published').map(draftViewOf)};
});

// One press of the scissors, in the server's order (spec 2.10.1 with the
// addendum's gate), with the Recording and Capture rows deciding how it goes.
export const createDraft = (handle: string, source: ClipSource): Promise<ClipDraft> => clipCall(linkGate, () => {
    const world = getWorld();
    const now = Date.now();
    if (source !== 'live' && source !== 'tail') {
        throw refuse(400, 'bad_request');
    }
    if (!world.clipsEnabled) {
        throw refuse(403, 'clips_off');
    }
    if (world.clipBlocked[ME_ID]) {
        throw refuse(403, 'blocked');
    }

    const live = world.streams.find((item) => item.handle === handle && !item.alwaysOn);
    const tail = labTailsOf(world, now).find((held) => held.handle === handle);
    if (source === 'live' && !live) {
        throw refuse(404, 'not_live');
    }
    if (source === 'tail' && !tail) {
        throw refuse(410, 'tail_gone');
    }

    const streamerId = source === 'live' ? live?.userId : tail?.userId;
    if (streamerId === ME_ID && !world.streamerAllowed) {
        throw refuse(403, 'streamer_off');
    }
    if (world.captureMode === 'not_watching') {
        throw refuse(403, 'not_watching');
    }

    // The server after a restart: a stream's own streamer may always clip
    // it, and anybody else only once a tune-in since then has told it they
    // are watching (clips_access.go mayClip).
    if (world.captureMode === 'forgotten' && source === 'live' && streamerId !== ME_ID && !heardWatching(handle)) {
        throw refuse(403, 'not_watching');
    }

    const length = world.clipLength * 1000;
    // An end held for less than a whole length is all there is of it.
    let durationMs = source === 'tail' ? Math.min(length, tail?.heldMs || length) : length;
    let partial = durationMs < length;
    if (source === 'live' && live) {
        const snapshotStream = snapshotOf().streams.find((item) => item.handle === handle);
        const state = snapshotStream?.clip;
        const recorded = state === 'ready' && snapshotStream?.clipFrom ? now - snapshotStream.clipFrom : 0;
        if (state === 'starting' || (state === 'ready' && recorded < MIN_CLIP_MS)) {
            throw refuse(409, 'warming_up');
        }
        if (state === 'failed' || state === 'full' || state === 'queued') {
            throw refuse(503, 'recording_failed');
        }
        if (state !== 'ready') {
            throw refuse(409, 'not_clippable');
        }
        if (recorded < length) {
            durationMs = Math.floor(recorded / 1000) * 1000;
            partial = true;
        }
    }

    const mine = world.drafts.filter((draft) => draftShowing(draft, now) && draft.state !== 'published');
    if (world.captureMode === 'cap' || mine.length >= DRAFT_CAP) {
        throw refuse(409, 'draft_cap');
    }
    if (world.captureMode === 'rate') {
        throw refuse(429, 'rate_limited');
    }
    if (world.captureMode === 'fails') {
        throw refuse(503, 'recording_failed');
    }

    const truncated = source === 'live' && world.recording === 'truncated';
    if (truncated) {
        durationMs = 84000;
    }

    const streamName = (source === 'live' ? live?.title : tail?.title) || 'Stream 3f9a0c';

    // What the stream was tagged with at the click (addendum 4.14), as the
    // snapshot says it: the stream's What's on?, or its plan that is on. A
    // tail's is what its stream was on when it stopped, kept with it.
    const tag = clipTagOf(source === 'live' ? snapshotOf().streams.find((item) => item.handle === handle)?.nowPlaying : tail?.nowPlaying);
    const draft: LabDraft = {
        id: freshClipId(),
        handle,
        source,
        state: 'ready',
        createdAt: now,

        // "draft expired": cut, and already past its two hours by the time
        // anything is done with it.
        expiresAt: world.captureMode === 'expired' ? now : now + DRAFT_TTL_MS,
        // The last stretch before the click, or before the end of a stream
        // that stopped.
        startAt: (source === 'tail' && tail ? Math.min(now, tail.endedAt) : now) - durationMs,
        durationMs,
        requestedMs: length,
        partial,
        truncated,
        width: 256,
        height: 144,
        codecs: 'avc1.64000c,opus',
        bytes: Math.round(415867 * durationMs / 120000),
        hasThumb: false,
        streamer: clipPersonOf(streamerId, streamName),
        ...(live?.motd && source === 'live' ? {streamNote: live.motd} : {}),
        ...(tag ? {tag} : {}),

        // As the server keeps it with a tail's draft: how that stream had
        // stopped, still known after the end itself has been let go.
        ...(source === 'tail' && tail ? {tailReason: tail.reason} : {}),
        owner: ME_ID,
        streamKey: streamKeyOf(streamerId, handle),

        // "wiped": the streamer wiped the stream just after it was cut.
        wiped: world.captureMode === 'wiped',
    };
    change((current) => {
        current.drafts = current.drafts.concat(draft);
    });

    return draftViewOf(draft);
}, {write: true, latencyMs: getWorld().captureMode === 'slow' ? 3000 : undefined});

// A draft the reader may act on, or the server's refusal for it.
function ownDraft(id: string, now: number): LabDraft {
    const draft = getWorld().drafts.find((candidate) => candidate.id === id && candidate.owner === ME_ID);
    if (!draft) {
        throw refuse(404, 'draft_gone');
    }
    if (draft.wiped) {
        throw refuse(410, 'draft_wiped');
    }
    if (draft.expiresAt <= now && draft.state !== 'published') {
        throw refuse(410, 'draft_gone');
    }

    return draft;
}

// Throwing a draft away, as the server's discardOwn answers it: one never
// made, or somebody else's, is not there at all (404 draft_gone, which the
// panel's Discard says and then refreshes the drafts for); one being
// published is busy; one already gone, wiped, run out or published is as good
// as thrown away, and nothing changes; only a ready draft is taken away.
export const deleteDraft = (id: string): Promise<{ok: boolean}> => clipCall(linkGate, () => {
    if (!CLIP_ID.test(id)) {
        throw refuse(400, 'bad_request');
    }

    const now = Date.now();
    const draft = getWorld().drafts.find((candidate) => candidate.id === id && candidate.owner === ME_ID);
    if (!draft) {
        throw refuse(404, 'draft_gone');
    }
    if (draft.state === 'publishing') {
        throw refuse(409, 'publishing');
    }
    if (draft.wiped || draft.state === 'published' || draft.expiresAt <= now) {
        return {ok: true};
    }

    change((current) => {
        current.drafts = current.drafts.filter((candidate) => candidate.id !== id);
    });

    return {ok: true};
}, {write: true});

export const draftVideoURL = (id: string): string => {
    const now = Date.now();
    const draft = getWorld().drafts.find((candidate) => candidate.id === id);

    return getWorld().me.streamKey && draft && draftShowing(draft, now) ? sampleClipURL : '';
};

// The draft's file, asked for its first byte, as the server's draft video
// route answers it: behind the link, then the owner's own draft, expired or
// wiped said as such. A published draft's file has become the clip's.
export const checkDraftVideo = (id: string): Promise<void> => clipCall(linkGate, () => {
    const draft = ownDraft(id, Date.now());
    if (draft.state === 'published') {
        throw refuse(410, 'draft_gone');
    }

    return undefined;
});

// The server checks the bytes as it checks a preview still; the lab checks
// what it can: a JPEG, of no more than 200 KB.
export const uploadDraftThumb = (id: string, jpeg: Blob): Promise<void> => clipCall(linkGate, () => {
    const draft = ownDraft(id, Date.now());
    if (draft.state === 'publishing') {
        throw refuse(409, 'publishing');
    }
    if (draft.state === 'published') {
        throw refuse(410, 'draft_gone');
    }
    if (jpeg.type !== 'image/jpeg' || jpeg.size === 0 || jpeg.size > 200 * 1024) {
        throw refuse(400, 'bad_thumb');
    }

    change(() => {
        if (draft.thumbUrl) {
            URL.revokeObjectURL(draft.thumbUrl);
        }
        draft.thumbUrl = URL.createObjectURL(jpeg);
        draft.hasThumb = true;
    });

    return undefined;
}, {write: true});

// Drafts whose publish has already pretended to lose its answer once.
const answersLost = new Set<string>();

// Publishing, in the server's order (spec 2.10.2 with the addendum's gate),
// with the Publish row deciding how it goes. The clip's id is the draft's.
export const publishDraft = (id: string, body: {title: string; inMs: number; outMs: number}): Promise<Clip> => clipCall(linkGate, () => {
    const world = getWorld();
    const now = Date.now();
    if (!CLIP_ID.test(id)) {
        throw refuse(400, 'bad_request');
    }

    const draft = ownDraft(id, now);
    if (draft.state === 'published') {
        const entry = liveClip(id);
        if (!entry) {
            throw refuse(410, 'clip_gone');
        }

        return clipViewOf(world, entry);
    }
    if (draft.state === 'publishing') {
        throw refuse(409, 'publishing');
    }
    if (world.publishMode === 'expired') {
        throw refuse(410, 'draft_gone');
    }
    if (!world.clipsEnabled) {
        throw refuse(403, 'clips_off');
    }
    if (world.clipBlocked[ME_ID]) {
        throw refuse(403, 'blocked');
    }

    const {inMs, outMs} = body;
    if (!Number.isInteger(inMs) || !Number.isInteger(outMs) || inMs < 0 || outMs > draft.durationMs || outMs <= inMs) {
        throw refuse(400, 'bad_range');
    }
    if (outMs - inMs < MIN_CLIP_MS) {
        throw refuse(400, 'too_short');
    }

    switch (world.publishMode) {
    case 'duplicate':
        throw refuse(409, 'duplicate');
    case 'full':
        throw refuse(507, 'storage_full');
    case 'share_full':
        throw refuse(507, 'user_storage_full');
    case 'fails':
        throw refuse(503, 'store');
    default:
        break;
    }

    const durationMs = outMs - inMs;
    const entry: LabClip = {
        id: draft.id,
        title: Array.from(body.title.trim()).slice(0, 80).join('').trim(),
        createdAt: now,
        startAt: draft.startAt + inMs,
        durationMs,

        // The cut starts on the keyframe before the start, at most a two-second
        // GOP earlier, and the player skips what comes before it.
        ...(inMs % 2000 > 0 ? {leadMs: inMs % 2000} : {}),
        width: draft.width,
        height: draft.height,
        codecs: draft.codecs,
        bytes: Math.round(draft.bytes * durationMs / Math.max(1, draft.durationMs)),
        ...(draft.thumbUrl ? {thumb: freshClipId().slice(0, 8), thumbUrl: draft.thumbUrl} : {}),
        clipperId: ME_ID,
        ...(draft.streamer.userId ? {streamerId: draft.streamer.userId} : {}),
        streamName: draft.streamer.name,
        ...(draft.streamNote ? {streamNote: draft.streamNote} : {}),
        ...(draft.tag ? {tag: draft.tag} : {}),
        handle: draft.handle,
        streamKey: draft.streamKey,
        voters: {},
        reports: {},
        shares: [],
    };
    change((current) => {
        current.clips = current.clips.concat(entry);
        current.drafts = current.drafts.map((candidate) => (candidate.id === id ? {...candidate, state: 'published', clipId: id} : candidate));
    });

    // "network then ok": the clip is published, and the answer is lost on
    // the way back, once; looking for it with fetchClip finds it.
    if (world.publishMode === 'network_then_ok' && !answersLost.has(id)) {
        answersLost.add(id);
        throw noAnswer();
    }

    return clipViewOf(getWorld(), entry);
}, {write: true, latencyMs: getWorld().publishMode === 'slow' ? 12000 : Math.max(getWorld().latencyMs, 1200)});

// ---- The streamer's own controls ----

// Victor's stream: the one cut from the key he is linked to, or, unlinked,
// the one the lab attributes to him.
const victorsKey = 'live-victor';

// A wipe (spec 2.10.6): the recording cut, so it starts again from now; the
// end of an earlier stretch let go; and every draft cut from the stream,
// whoever made it, gone. Published clips stay.
function wipeHandle(current: World, handle: string): number {
    current.restartedAt = {...current.restartedAt, [handle]: Date.now()};
    let removed = 0;
    current.drafts = current.drafts.map((draft) => {
        if (draft.handle !== handle || draft.wiped || draft.state === 'published') {
            return draft;
        }
        removed++;

        return {...draft, wiped: true};
    });
    current.tails = current.tails.filter((tail) => tail.handle !== handle);

    return removed;
}

export const setClipsAllowed = (allow: boolean): Promise<{allowed: boolean}> => clipCall(linkGate, () => {
    change((current) => {
        current.streamerAllowed = allow;
        if (!allow) {
            // Switching it off wipes what is held of the stream, and every
            // draft cut from it, whoever made it.
            current.drafts = current.drafts.map((draft) => (
                draft.streamKey === victorsKey && draft.state !== 'published' ? {...draft, wiped: true} : draft
            ));
            current.tails = current.tails.filter((tail) => tail.userId !== ME_ID);
            current.streams.filter((item) => item.userId === ME_ID).forEach((item) => {
                current.restartedAt = {...current.restartedAt, [item.handle]: Date.now()};
            });
        }
    });

    return {allowed: allow};
}, {write: true});

// The streamer's second switch (design/3.2.1.md 1.3): every clip of Victor's
// stream, past and future, whatever the admin's switch says. Deletes nothing.
export const setClipDownloads = (downloads: boolean): Promise<{downloads: boolean}> => clipCall(linkGate, () => {
    change((current) => {
        current.streamerNoDownloads = !downloads;
    });

    return {downloads};
}, {write: true});

// A download ticket (design/3.2.1.md 1.4), refused in the server's order: the
// admin's switch, the clip, the link, the clip's streamer. The address is the
// shape the real server answers, under the lab's own base.
export const fetchClipDownload = (id: string): Promise<{url: string}> => clipCall(linkGate, () => {
    const world = getWorld();
    if (!world.downloadsAllowed) {
        throw refuse(403, 'downloads_off');
    }
    if (!CLIP_ID.test(id)) {
        throw refuse(400, 'bad_request');
    }

    const entry = liveClip(id);
    if (!entry) {
        throw refuse(410, 'clip_gone');
    }

    const why = labDownloadRefusal(world, entry);
    if (why !== '') {
        throw refuse(403, why);
    }

    const ticket = Array.from({length: 64}, () => '0123456789abcdef'[Math.floor(Math.random() * 16)]).join('');

    return {url: `${PLUGIN_BASE}/api/v1/clips/download?t=${ticket}`};
}, {write: true});

// Saving from that address: in the lab there is no server to save from, so
// the press is counted and said, and nothing is fetched.
export const saveClipFrom = (url: string): void => {
    // The lab's own window object may have been made before the counters
    // were, so a missing count starts at nothing rather than at NaN.
    const counters = labCounters();
    counters.downloads = (counters.downloads || 0) + 1;
    labDownloads.push(url);
};

// Every address the lab was asked to save from, for the screenshots' checks.
export const labDownloads: string[] = [];

// Wiping what is held of a stream: by its streamer, by the link they hold now,
// or by an admin.
export const wipeRecording = (handle: string): Promise<{ok: boolean; draftsRemoved: number}> => clipCall(linkGate, () => {
    const world = getWorld();
    const now = Date.now();
    const stream = world.streams.find((item) => item.handle === handle) || clipTailsOf(world, now).find((tail) => tail.handle === handle);
    const theirs = Boolean(stream && stream.userId === ME_ID && world.me.streamKey === victorsKey);
    if (!theirs && !world.me.isAdmin) {
        throw refuse(403, 'not_allowed');
    }

    let removed = 0;
    change((current) => {
        removed = wipeHandle(current, handle);
    });

    return {ok: true, draftsRemoved: removed};
}, {write: true});

// ---- The System Console ----

const GB = 1024 * 1024 * 1024;

export const fetchAdminClips = (): Promise<AdminClips> => clipCall(adminGate, () => {
    const world = getWorld();
    const now = Date.now();
    const live = liveClipsOf(world, now);
    const snapshot = snapshotOf();
    const reported = (entry: LabClip) => Object.keys(entry.reports).length > 0;
    // Everybody with a score, as the server lists them: clips still kept, or
    // upvotes banked from clips that have gone. upvotes is the whole score,
    // the banked ones included, counted only while the person may hold Best
    // Clipper; banked says how many of them are kept from clips gone.
    const people = new Map<string, {clips: number; bytes: number; upvotes: number}>();
    const personOf = (userId: string) => {
        const person = people.get(userId) || {clips: 0, bytes: 0, upvotes: 0};
        people.set(userId, person);

        return person;
    };
    live.forEach((entry) => {
        const person = personOf(entry.clipperId);
        person.clips += 1;
        person.bytes += entry.bytes;
        person.upvotes += countedVotes(world, entry).count;
    });
    Object.entries(world.clipBanked).forEach(([userId, banked]) => {
        if (banked.up > 0 || banked.clips > 0) {
            personOf(userId).upvotes += banked.up;
        }
    });
    const drafts = world.drafts.filter((draft) => !draft.wiped && draft.expiresAt > now && draft.state !== 'published');

    return {
        problem: '',
        halted: '',
        root: `/opt/mattermost/data/${PLUGIN_ID}`,
        usedBytes: live.reduce((sum, entry) => sum + entry.bytes, 0) + drafts.reduce((sum, draft) => sum + draft.bytes, 0),
        limitBytes: 20 * GB,
        shareBytes: 4 * GB,
        freeBytes: 410 * GB,
        totalBytes: 1000 * GB,
        ringBytes: snapshot.streams.filter((item) => item.clip === 'ready').length * 63 * 1024 * 1024,
        recorders: snapshot.streams.filter((item) => item.clip !== undefined).map((item) => ({
            handle: item.handle,
            title: item.title,
            state: item.clip || 'starting',
            heldSeconds: item.clip === 'ready' && item.clipFrom ? Math.min(world.clipLength, Math.floor((now - item.clipFrom) / 1000)) : 0,
            bitrate: item.bitrate,
            truncated: world.recording === 'truncated',
            reason: item.clip === 'failed' ? 'ICE failed: no route to 192.168.1.20:8189' :
                item.clip === 'unsupported' ? 'not H.264 (AV1)' :
                item.clip === 'off' ? 'the streamer switched clipping off' :
                item.clip === 'queued' ? 'recorder limit' :
                item.clip === 'full' ? 'disk low' : '',
        })),
        tails: clipTailsOf(world, now),
        drafts: drafts.length,
        clips: live.slice().sort((a, b) => (Number(reported(b)) - Number(reported(a))) || newestFirst(a, b)).map((entry) => clipViewOf(world, entry, true)),
        people: Array.from(people.entries()).map(([userId, person]) => {
            const named = clipPersonOf(userId);

            return {
                userId,
                username: named.username || '',
                displayName: named.displayName || '',
                ...person,
                banked: world.clipBanked[userId]?.up || 0,
            };
        }).sort((a, b) => b.bytes - a.bytes),
        blocked: Object.entries(world.clipBlocked).map(([userId, block]) => {
            const named = clipPersonOf(userId);

            return {userId, username: named.username || '', displayName: named.displayName || '', blockedAt: block.at, by: block.by};
        }),
        // "Retention pending" stands for "Keep clips for" lowered from the
        // lab's 30 days to 3, the spec's own example, in its day of grace.
        retention: {
            days: world.retentionPending > 0 ? 3 : world.retentionDays,
            pendingRemovals: world.retentionPending,
            ...(world.retentionPending > 0 ? {removalAt: now + (20 * 3600000)} : {}),
        },
        bestClipper: world.bestClipper,
    };
});

export const adminDeleteClip = (id: string): Promise<{ok: boolean}> => clipCall(adminGate, () => {
    const entry = liveClip(id);
    if (entry) {
        settle(entry, 'admin');
    }

    return {ok: true};
}, {write: true});

export const adminWipeRecording = (handle: string): Promise<{ok: boolean; draftsRemoved: number}> => clipCall(adminGate, () => {
    let removed = 0;
    change((current) => {
        removed = wipeHandle(current, handle);
    });

    return {ok: true, draftsRemoved: removed};
}, {write: true});

export const blockClipper = (userId: string): Promise<{ok: boolean; draftsRemoved: number}> => clipCall(adminGate, () => {
    if (userId === '') {
        throw refuse(400, 'bad_request');
    }

    // Blocking discards the person's drafts, and leaves their clips alone.
    const removed = getWorld().drafts.filter((draft) => draft.owner === userId && draft.state === 'ready').length;
    change((current) => {
        current.clipBlocked = {...current.clipBlocked, [userId]: current.clipBlocked[userId] || {at: Date.now(), by: ME_ID}};
        current.drafts = current.drafts.filter((draft) => !(draft.owner === userId && draft.state === 'ready'));
    });

    return {ok: true, draftsRemoved: removed};
}, {write: true});

export const unblockClipper = (userId: string): Promise<{ok: boolean}> => clipCall(adminGate, () => {
    change((current) => {
        const {[userId]: _gone, ...rest} = current.clipBlocked;
        current.clipBlocked = rest;
    });

    return {ok: true};
}, {write: true});

export const resetClipperScore = (userId: string): Promise<{ok: boolean}> => clipCall(adminGate, () => {
    if (userId === '') {
        throw refuse(400, 'bad_request');
    }

    change((current) => {
        current.clipBanked = {...current.clipBanked, [userId]: {up: 0, clips: 0, lastUpAt: 0}};
        current.clipResets = {...current.clipResets, [userId]: Date.now()};
    });

    return {ok: true};
}, {write: true});

export const removeClipThumb = (id: string): Promise<{ok: boolean}> => clipCall(adminGate, () => {
    const entry = liveClip(id);
    if (!entry) {
        throw refuse(410, 'clip_gone');
    }

    change(() => {
        entry.thumb = undefined;
        entry.thumbUrl = undefined;
    });

    return {ok: true};
}, {write: true});

export const clearClipReports = (id: string): Promise<{ok: boolean}> => clipCall(adminGate, () => {
    const entry = liveClip(id);
    if (!entry) {
        throw refuse(410, 'clip_gone');
    }

    change(() => {
        entry.reports = {};
    });

    return {ok: true};
}, {write: true});

// ---- Awards by your name, biggest fans, achievements (design/3.2.0.md 3-5) ----
// The server's checks, repeated as the planning ones are.

// A choice (awards.go): at most four, no repeats, each of a known form;
// whether the person holds it is not checked, because they may choose ahead.
const AWARD_FORMS = /^(alltime|month|snooper|clipper|past|fan:[A-Za-z0-9_-]+|ach:[a-z0-9-]+)$/;

export const fetchMyAwards = (): Promise<AwardsMine> => answer(() => {
    accessGate(true);
    const world = getWorld();
    const entry = world.awardChoices[ME_ID];

    return {
        show: world.awardsReadable && entry ? entry.show : null,
        readable: world.awardsReadable,
        held: heldByMe(),
    };
});

export const saveAwardChoice = (show: string[] | null): Promise<{ok: boolean; show: string[] | null}> => write(() => {
    accessGate(true);
    if (show !== null && (show.length > 4 || new Set(show).size !== show.length || show.some((id) => !AWARD_FORMS.test(id)))) {
        throw refused(400, 'bad_choice', 'Choose up to four of your awards, each once.');
    }
    change((current) => {
        const {[ME_ID]: _mine, ...rest} = current.awardChoices;
        current.awardChoices = show === null ? rest : {...rest, [ME_ID]: {show, at: Date.now()}};
        current.awardsReadable = true;
    });

    return {ok: true, show};
});

// A takeback: open to everybody signed in, linked or not.
export const clearAwardChoice = (): Promise<{ok: boolean; show: string[] | null}> => write(() => {
    change((current) => {
        const {[ME_ID]: _mine, ...rest} = current.awardChoices;
        current.awardChoices = rest;
    });

    return {ok: true, show: null};
});

// Answered from what the server holds in memory, open to everybody signed in:
// from 3.2.1 only the reader's sounds.
export const fetchTakebacks = (): Promise<Takebacks> => answer(() => {
    const world = getWorld();

    return {sounds: world.sounds.filter((sound) => sound.ownerId === ME_ID).length};
});

export const fetchMyAchievements = (): Promise<AchievementProgress> => answer(() => {
    accessGate(true);
    const world = getWorld();
    const values = world.achievementValues[ME_ID] || {};

    return {
        progress: (world.achievementsOn ? LAB_ACHIEVEMENT_KINDS : []).map((kind) => {
            const value = values[kind.id] || 0;

            return {id: kind.id, value, next: kind.tiers.find((step) => step > value) || 0};
        }),
    };
});

// ---- Shared plans (design/3.2.0.md 6.3) ----

// Plan shares in the last minute, by plan and channel: a retry posts
// nothing new.
const recentPlanShares = new Map<string, {postId: string; at: number}>();

// In the server's order: linked only; a fresh read of the schedule, so a plan
// cancelled a moment ago is not posted (410 gone); the channel's checks; the
// meter. With the Share row's answers, which the clip shares use too. The
// post goes into the lab's Mattermost as the server's would: fixed words and
// a link, and props with only the plan's id and start.
export const sharePlan = (id: string, channelId: string): Promise<{postId: string; channelId: string}> => clipCall(linkGate, () => {
    const world = getWorld();
    if (!(/^[0-9a-f]{16}$/).test(id) || channelId === '') {
        throw refuse(400, 'bad_request');
    }
    if (world.shareMode === 'network') {
        throw noAnswer();
    }

    const plan = world.shareMode === 'gone' ? undefined : world.schedule.find((item) => item.id === id);
    if (!plan) {
        throw refuse(410, 'gone');
    }

    const channel = channelOf(channelId) || LAB_CHANNELS.find((candidate) => candidate.id === channelId);
    if (world.shareMode === 'cannot_post' || !channel || (channel.delete_at || 0) > 0 || channel.shared === true) {
        throw refuse(403, 'cannot_post');
    }

    const now = Date.now();
    const repeatKey = `${id}|${channelId}`;
    const repeat = recentPlanShares.get(repeatKey);
    if (repeat && now - repeat.at < SHARE_REPEAT_MS) {
        return {postId: repeat.postId, channelId};
    }
    if (world.shareMode === 'rate') {
        throw refuse(429, 'rate_limited');
    }

    const post = {
        id: freshId('post'),
        channel_id: channelId,
        user_id: ME_ID,
        root_id: '',
        message: labPlanShareMessage(id),
        create_at: now,
        delete_at: 0,
        type: PLAN_POST_TYPE,
        props: labPlanShareProps(id, plan.startsAt),
    };
    mattermostStore()?.dispatch({type: 'RECEIVED_NEW_POST', data: post});
    recentPlanShares.set(repeatKey, {postId: post.id, at: now});

    return {postId: post.id, channelId};
}, {write: true});

// ---- The soundboard (design/3.2.0.md 7.9 and 7.11), as the server answers ----
// Its rules repeated: linked people in Mumble only, while it is on and they
// are not blocked; a press refused while Mumble says no, while they wait, or
// while anybody's sound plays; ten seconds after their own. Timed by the
// lab's clock, so the Soundboard row's states can wind it.

export class SoundRequestError extends Error {
    status: number;
    code: string;
    retryAfterMs: number;
    serverText: string;

    constructor(status: number, code: string, retryAfterMs: number, serverText = '') {
        super(serverText || `Sound request failed (${status === 0 ? 'no answer' : status})`);
        this.name = 'SoundRequestError';
        this.status = status;
        this.code = code;
        this.retryAfterMs = retryAfterMs;
        this.serverText = serverText;
    }
}

const SOUND_COOLDOWN_MS = 10000;
const SOUND_LIMITS = {perPerson: 16, total: 120};

// The soundboard's own gate (soundGate): the access gate first, then the
// switch, then the block list, which the board itself does not ask (the
// server answers a blocked reader the board, saying blocked).
function soundGate(blockedToo = true): void {
    accessGate(true);
    const world = getWorld();
    if (!world.soundboardOn) {
        throw new SoundRequestError(403, 'off', 0, 'The soundboard has been switched off.');
    }
    if (blockedToo && world.soundBlocked.some((person) => person.userId === ME_ID)) {
        throw new SoundRequestError(403, 'blocked', 0, 'An admin has turned the soundboard off for you.');
    }
}

// Adding, renaming and deleting need a matched Mumble session, in any state.
function inVoiceGate(): void {
    const board = soundBoardOf(getWorld(), clockNow());
    if (!board.voice.in || board.voice.why === 'bot_offline') {
        throw new SoundRequestError(409, 'not_in_voice', 0);
    }
}

function soundCall<T>(route: () => T, options: {write?: boolean; blockedToo?: boolean} = {}): Promise<T> {
    return (options.write ? write : answer)(() => {
        soundGate(options.blockedToo !== false);

        return route();
    });
}

// A sound's packets as the server stores them: the header, then each packet
// (design/3.2.0.md 7.8), from the fixture's real Opus, as many as its length.
function storedSound(ms: number): ArrayBuffer {
    const count = Math.min(fixture.fixtures.full6s.length, Math.max(13, Math.round(ms / 20) + 1));
    const packets = fixture.fixtures.full6s.slice(0, count).map((packet) => Uint8Array.from(atob(packet), (char) => char.charCodeAt(0)));
    const section = packetSection(packets);
    const out = new Uint8Array(47 + section.length - 2);
    out.set([0x56, 0x4e, 0x53, 0x32]);
    const view = new DataView(out.buffer);
    view.setUint16(12, count);
    out[14] = 20;
    out.set(section.subarray(2), 47);

    return out.buffer;
}

export const fetchSounds = (): Promise<SoundBoard> => soundCall(() => soundBoardOf(getWorld(), clockNow()), {blockedToo: false});

export const playSound = (id: string): Promise<SoundPlay> => soundCall(() => {
    const world = getWorld();
    const at = clockNow();
    const board = soundBoardOf(world, at);
    if (!board.voice.in) {
        throw new SoundRequestError(409, 'not_in_voice', 0);
    }
    if (board.voice.why !== '') {
        throw new SoundRequestError(409, board.voice.why, 0);
    }
    if (board.cooldownUntil > at) {
        throw new SoundRequestError(429, 'cooldown', board.cooldownUntil - at);
    }
    if (board.busyUntil > at) {
        throw new SoundRequestError(409, 'busy', board.busyUntil - at);
    }
    const sound = world.sounds.find((entry) => entry.id === id);
    if (!sound) {
        throw new SoundRequestError(410, 'gone', 0);
    }

    const endsAt = at + sound.ms;
    const cooldownUntil = endsAt + SOUND_COOLDOWN_MS;
    change((current) => {
        current.soundCooldownUntil = cooldownUntil;
        current.soundBusyUntil = endsAt;
    });

    return {startedAt: at, endsAt, cooldownUntil, channel: world.soundChannel};
});

// The upload checked as the server checks it: the section's shape and
// bounds (the server decodes it too, which the lab cannot), then the name,
// the emoji and the room left.
function packetCount(packets: string): number {
    let bytes: Uint8Array;
    try {
        bytes = Uint8Array.from(atob(packets), (char) => char.charCodeAt(0));
    } catch {
        return -1;
    }
    if (bytes.length < 2 || bytes.length > 40000) {
        return -1;
    }
    const view = new DataView(bytes.buffer);
    const count = view.getUint16(0);
    let at = 2;
    for (let index = 0; index < count; index += 1) {
        if (at + 2 > bytes.length) {
            return -1;
        }
        const length = view.getUint16(at);
        if (length < 1 || length > 128) {
            return -1;
        }
        at += 2 + length;
    }

    return at === bytes.length && count >= 13 && count <= 301 ? count : -1;
}

export const addSound = (sound: NewSound): Promise<{id: string}> => soundCall(() => {
    inVoiceGate();
    const world = getWorld();
    const name = sound.name.trim();
    if (name === '' || Array.from(name).length > 16 || (/[\r\n]/).test(name)) {
        throw new SoundRequestError(400, 'bad_name', 0);
    }
    const emoji = labEmojiNamed(sound.emoji.name);
    if (!emoji || emoji.kind !== sound.emoji.kind || world.soundAddAnswer === 'bad_emoji') {
        throw new SoundRequestError(400, 'bad_emoji', 0);
    }
    if (world.sounds.filter((entry) => entry.ownerId === ME_ID).length >= SOUND_LIMITS.perPerson) {
        throw new SoundRequestError(409, 'yours_full', 0);
    }
    if (world.sounds.length >= SOUND_LIMITS.total || world.soundAddAnswer === 'full') {
        throw new SoundRequestError(409, 'full', 0);
    }
    const count = packetCount(sound.packets);
    if (count < 0 || world.soundAddAnswer === 'bad_sound') {
        throw new SoundRequestError(400, 'bad_sound', 0);
    }
    if (world.soundAddAnswer === 'too_loud') {
        throw new SoundRequestError(400, 'too_loud', 0);
    }
    if (world.soundAddAnswer === 'store') {
        throw new SoundRequestError(503, 'store', 0);
    }

    const id = freshPlanId();
    change((current) => {
        current.sounds = current.sounds.concat({id, ownerId: ME_ID, name, emoji, ms: count * 20, at: Date.now()});
        current.soundV += 1;
    });

    return {id};
}, {write: true});

function ownSound(id: string) {
    const sound = getWorld().sounds.find((entry) => entry.id === id);
    if (!sound) {
        throw new SoundRequestError(410, 'gone', 0);
    }
    if (sound.ownerId !== ME_ID) {
        throw new SoundRequestError(403, 'not_yours', 0);
    }

    return sound;
}

export const editSound = (id: string, edit: {name?: string; emoji?: {kind: 'system' | 'custom'; name: string}}): Promise<{ok: boolean}> => soundCall(() => {
    inVoiceGate();
    ownSound(id);
    const name = edit.name === undefined ? undefined : edit.name.trim();
    if (name !== undefined && (name === '' || Array.from(name).length > 16)) {
        throw new SoundRequestError(400, 'bad_name', 0);
    }
    const emoji = edit.emoji ? labEmojiNamed(edit.emoji.name) : undefined;
    if (edit.emoji && !emoji) {
        throw new SoundRequestError(400, 'bad_emoji', 0);
    }
    change((current) => {
        current.sounds = current.sounds.map((sound) => (sound.id === id ? {...sound, name: name || sound.name, emoji: emoji || sound.emoji} : sound));
        current.soundV += 1;
    });

    return {ok: true};
}, {write: true});

// A takeback, as on the server: the owner's own, whatever the switch, the
// block list or Mumble say.
export const removeSound = (id: string): Promise<{ok: boolean}> => write(() => {
    ownSound(id);
    change((current) => {
        current.sounds = current.sounds.filter((sound) => sound.id !== id);
        current.soundV += 1;
    });

    return {ok: true};
});

// A takeback: open to anybody signed in, whether the soundboard is on or off.
export const removeMySounds = (): Promise<{deleted: number}> => write(() => {
    const deleted = getWorld().sounds.filter((sound) => sound.ownerId === ME_ID).length;
    change((current) => {
        current.sounds = current.sounds.filter((sound) => sound.ownerId !== ME_ID);
        current.soundV += 1;
    });

    return {deleted};
});

// Listen here: the owner's own sounds only.
export const fetchSoundAudio = (id: string): Promise<ArrayBuffer> => soundCall(() => storedSound(ownSound(id).ms));

// System Console → Sounds on the server: system admins only, whatever the
// switch says.
function adminSoundCall<T>(route: () => T, options: {write?: boolean} = {}): Promise<T> {
    return (options.write ? write : answer)(() => {
        if (!getWorld().me.isAdmin) {
            throw new SoundRequestError(403, 'forbidden', 0, 'That is not allowed.');
        }

        return route();
    });
}

export const fetchAdminSounds = (): Promise<AdminSounds> => adminSoundCall(() => adminSoundsOf(getWorld()));

export const fetchAdminSoundAudio = (id: string): Promise<ArrayBuffer> => adminSoundCall(() => {
    const sound = getWorld().sounds.find((entry) => entry.id === id);
    if (!sound) {
        throw new SoundRequestError(410, 'gone', 0);
    }

    return storedSound(sound.ms);
});

export const adminRemoveSound = (id: string): Promise<{ok: boolean}> => adminSoundCall(() => {
    change((current) => {
        current.sounds = current.sounds.filter((sound) => sound.id !== id);
        current.soundV += 1;
    });

    return {ok: true};
}, {write: true});

export const adminBlockSounds = (username: string, blocked: boolean): Promise<{ok: boolean; userId: string}> => adminSoundCall(() => {
    const user = LAB_USERS.find((candidate) => candidate.username === username.toLowerCase());
    if (!user) {
        throw new SoundRequestError(404, 'no_such_user', 0);
    }
    const world = getWorld();
    if (blocked && !world.soundBlocked.some((person) => person.userId === user.id) && world.soundBlocked.length >= 50) {
        throw new SoundRequestError(409, 'full', 0);
    }
    change((current) => {
        const others = current.soundBlocked.filter((person) => person.userId !== user.id);
        current.soundBlocked = blocked ? others.concat({
            userId: user.id,
            username: user.username || '',
            name: [user.first_name, user.last_name].filter(Boolean).join(' '),
            by: 'Victor',
            at: Date.now(),
        }) : others;
    });

    return {ok: true, userId: user.id};
}, {write: true});

export const adminCheckSounds = (): Promise<{ok: boolean; line?: string}> => adminSoundCall(() => adminSoundsOf(getWorld()).bot);

export const adminRestartSounds = (): Promise<{ok: boolean}> => adminSoundCall(() => {
    change((current) => {
        current.sounds = [];
        current.soundsReadable = true;
        current.soundV += 1;
    });

    return {ok: true};
}, {write: true});
