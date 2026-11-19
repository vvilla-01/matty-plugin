import {PLUGIN_BASE} from './manifest';
import type {
    AccountService,
    AppWord,
    BrandArtwork,
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
    MattermostUser,
    Me,
    MediaResult,
    NowPlaying,
    PostSearchPage,
    Profile,
    ReleaseAnswer,
    ReleaseWord,
    Snapshot,
    SoundBoard,
    SoundPlay,
    Takebacks,
} from './types';

// Mattermost accepts either the X-Requested-With marker or an explicit CSRF
// token. Sending both means the plugin does not depend on the older path
// continuing to be honoured.
function csrfToken(): string {
    const match = (typeof document === 'undefined' ? '' : document.cookie).match(/(?:^|;\s*)MMCSRF=([^;]+)/);

    return match ? decodeURIComponent(match[1]) : '';
}

function baseHeaders(): Record<string, string> {
    const headers: Record<string, string> = {'X-Requested-With': 'XMLHttpRequest'};

    const token = csrfToken();
    if (token !== '') {
        headers['X-CSRF-Token'] = token;
    }

    return headers;
}

const jsonHeaders = (): Record<string, string> => ({
    ...baseHeaders(),
    'Content-Type': 'application/json',
});

// A refusal from one of the plugin's routes: message is the server's own
// words (or a placeholder when it sent none), so every caller that shows
// .message keeps working; status and code are for the callers that word a
// refusal themselves, such as the lock screen. code is '' when the server
// sent none.
export class RequestError extends Error {
    status: number;
    code: string;

    // Only what the plugin's server wrote in "error", and '' when it wrote
    // nothing (Mattermost's own refusal, a proxy's page).
    serverText: string;

    constructor(message: string, status: number, code = '', serverText = '') {
        super(message);
        this.name = 'RequestError';
        this.status = status;
        this.code = code;
        this.serverText = serverText;
    }
}

// The server's words and code for a refusal, from its {"error", "code"} body.
async function readRefusal(response: Response): Promise<{text: string; code: string}> {
    try {
        const body = await response.json();

        return {
            text: body && typeof body.error === 'string' ? body.error : '',
            code: body && typeof body.code === 'string' ? body.code : '',
        };
    } catch {
        // Not a JSON error body.
        return {text: '', code: ''};
    }
}

async function readError(response: Response, fallback: string): Promise<string> {
    const {text, code} = await readRefusal(response);
    heardRefusal(code);

    return text || fallback;
}

// ---- Telling the page about a refusal for access ----

// Every route that is not open refuses a locked reader with code "locked" and
// a let-in one, where it needs a link, with "watch_only" (design/lock-screen.md
// 3.4); "not_linked" is what a server from before the lock said. Whoever
// hears one tells the page, which reads "me" again, so a link undone or a
// let-out is followed everywhere at once. A listener rather than an import:
// identity.ts already imports this module.
const ACCESS_CODES = new Set(['locked', 'watch_only', 'not_linked']);

let refusalListener: ((code: string) => void) | null = null;

export function setAccessRefusalListener(listener: ((code: string) => void) | null): void {
    refusalListener = listener;
}

// Every refusal from every route comes through here.
function heardRefusal(code: string): void {
    if (ACCESS_CODES.has(code)) {
        refusalListener?.(code);
    }
}

async function requestJSON<T>(path: string, init: RequestInit = {}): Promise<T> {
    const response = await fetch(PLUGIN_BASE + path, {
        credentials: 'same-origin',
        ...init,
        headers: {...jsonHeaders(), ...(init.headers || {})},
    });

    if (!response.ok) {
        const {text, code} = await readRefusal(response);
        heardRefusal(code);
        throw new RequestError(text || `Request failed (${response.status})`, response.status, code, text);
    }

    return response.json() as Promise<T>;
}

export const fetchStreams = (): Promise<Snapshot> => requestJSON<Snapshot>('/api/v1/streams');

export const fetchMe = (): Promise<Me> => requestJSON<Me>('/api/v1/me');

// GET /api/v1/access: the one small read about the reader, which costs the
// server nothing like "me" does (contract/README.md). For the reads that are
// about nothing but whether the reader may still watch: once a minute while
// something plays, and after a request was refused (identity.ts
// refreshAccess).
//
// It also carries what this server calls the plugin and its artwork
// (name.ts), whether it is a signed release, and the app check's word for
// this session, as "me" does.
export type AccessAnswer = {
    access?: Me['access'];
    configured?: boolean;
    contract?: number;
    version?: string;
    name?: string;
    artwork?: BrandArtwork;
    release?: ReleaseWord;
    app?: AppWord;
};

export const fetchAccess = (): Promise<AccessAnswer> => requestJSON<AccessAnswer>('/api/v1/access');

// GET /api/v1/release: whether this plugin is a signed release, and, when it
// has a release folder, the manifest and both signatures as found
// (contract/README.md, "A signed release").
export const fetchRelease = (): Promise<ReleaseAnswer> => requestJSON<ReleaseAnswer>('/api/v1/release');

// GET /api/v1/admin/appcheck, for system admins: every person whose Matty
// has a verdict that has not expired, and each of their sign-ins' verdicts
// (server/app_check.go appCheckAdminView). check is the setting; problem says
// which lines of the two lists of digests were left out. Of each verdict:
// verified, and development beside it when only the plugin's development
// build would pass it; reason, the server's words, when it is not verified;
// at and expires (ms); versionCode, the app's version code as Android
// attested it; stale, a verdict of verified given under what this server no
// longer accepts, which the server already sends as not verified with its
// reason. The rest of what the server keeps (the session, the phone's
// security level and boot state, what the app says of itself) is not read.
export type AdminAppCheckVerdict = {
    verified: boolean;
    development?: boolean;
    reason?: string;
    at: number;
    expires: number;
    versionCode?: number;
    stale?: boolean;
};

export type AdminAppCheckPerson = {
    userId: string;
    username?: string;
    sessions: AdminAppCheckVerdict[];
};

export type AdminAppCheck = {
    check: AppWord['check'];
    problem?: string;
    people: AdminAppCheckPerson[];
};

export const fetchAdminAppCheck = (): Promise<AdminAppCheck> => requestJSON<AdminAppCheck>('/api/v1/admin/appcheck');

// GET /api/v1/admin/push, for system admins (server/push_api.go
// pushAdminView): whether pushes can go out (on, the plugin's switch; ready,
// its push key loaded; mattermost, whether Mattermost decides to push at
// all, and mattermostProblem why not, naming the console path), how many
// people and phones are registered, and one row per push server host:
// phones, working, waiting for their first push, failing (failingSince, ms,
// the earliest) and refused. full: more people than the index keeps, so the
// counts are of the most recent. problem: the server's sentence for lines of
// Push servers on your own network that were left out. dropped: the
// answering server's counts of pushes not sent since it started, by the
// server's word for why. Nothing per person: which push server somebody uses
// is theirs.
export type AdminPushHost = {
    host: string;
    devices: number;
    working: number;
    waiting: number;
    failing: number;
    refused: number;
    failingSince?: number;
};

export type AdminPush = {
    on: boolean;
    ready: boolean;
    mattermost: boolean;
    mattermostProblem?: string;
    people: number;
    devices: number;
    hosts: AdminPushHost[];
    full?: boolean;
    problem?: string;
    dropped?: Record<string, number>;
};

export const fetchAdminPush = (): Promise<AdminPush> => requestJSON<AdminPush>('/api/v1/admin/push');

// GET /api/v1/admin/voice, for system admins (server/voice_api.go
// voiceAdminView): talking from Matty on the server that answers. on is the
// switch with a voice server set; connected, whether the voice server is;
// port, open and addresses, the UDP port, whether it answers phones, and
// what phones are told to reach; problem, the server's sentence for why it
// does not; talking, how many people talk from Matty through it now;
// noBudget, whether the plugin's own limit of connections to the voice
// server is lifted; refused and refusedAt (ms), the voice server's last
// refusal of somebody joining from Matty. Never who.
export type AdminVoice = {
    on: boolean;
    connected: boolean;
    port: number;
    open: boolean;
    addresses: string[];
    problem?: string;
    talking: number;
    noBudget: boolean;
    refused?: string;
    refusedAt?: number;
};

export const fetchAdminVoice = (): Promise<AdminVoice> => requestJSON<AdminVoice>('/api/v1/admin/voice');

// A read the server can answer with "you already have it" (contract duty 6).
// tag is the tag the copy in hand came under: this route's ETag, or, for the
// snapshot, the tag its push carried, in quotes. null comes back when that
// copy is still what the server would send (304), which costs a few bytes
// where the whole document was sent, parsed and laid over itself to learn
// the same. '' asks for it whatever is held.
export type Tagged<T> = {value: T; tag: string};

async function requestIfChanged<T>(path: string, tag: string): Promise<Tagged<T> | null> {
    const response = await fetch(PLUGIN_BASE + path, {
        credentials: 'same-origin',
        headers: {...jsonHeaders(), ...(tag === '' ? {} : {'If-None-Match': tag})},
    });

    if (response.status === 304) {
        // There is nothing in it, and it is read all the same: a browser puts
        // an answer nobody read down as a request that failed (cancelled, in
        // red, in its network log), which this one, the commonest answer
        // there is to these reads, did not.
        await response.text().catch(() => '');

        return null;
    }
    if (!response.ok) {
        const {text, code} = await readRefusal(response);
        heardRefusal(code);
        throw new RequestError(text || `Request failed (${response.status})`, response.status, code, text);
    }

    return {value: await response.json() as T, tag: response.headers.get('ETag') || ''};
}

export const fetchStreamsIfChanged = (tag: string): Promise<Tagged<Snapshot> | null> =>
    requestIfChanged<Snapshot>('/api/v1/streams', tag);

// The boards and everybody's record, which are not in the snapshot: read by
// boards.ts when somebody looks, with the tag of the copy in hand.
export const fetchBoardsIfChanged = (tag: string): Promise<Tagged<Boards> | null> =>
    requestIfChanged<Boards>('/api/v1/boards', tag);

// The bearer token (the publish token the streaming software uses) is posted,
// never a stream key: Broadcast Box resolves it to the stream it belongs to,
// which is what proves ownership. A refusal is a RequestError carrying the
// server's code (not_accepted, rate_limited, unreachable and so on), which the
// lock screen words itself. The answer also says what the caller's access is
// now, as GET /api/v1/access would.
export type LinkAnswer = {streamKey: string; access?: Me['access']; configured?: boolean};

export const linkStream = (token: string): Promise<LinkAnswer> =>
    requestJSON<LinkAnswer>('/api/v1/me/link', {
        method: 'POST',
        body: JSON.stringify({token}),
    });

// Taking the link back. A DELETE, which no meter stands in front of: like
// every other takeback it works however many times a token was mistyped.
export const unlinkStream = (): Promise<LinkAnswer> =>
    requestJSON<LinkAnswer>('/api/v1/me/link', {method: 'DELETE'});

// Admin only. Fetched and saved from a blob rather than being a plain <a href>.
//
// A link was the obvious way and it fails SILENTLY. Whether a browser downloads
// or navigates is left to Content-Disposition, an anchor in a single-page
// console can have its click taken by the page around it, and when any of that
// goes wrong there is nothing to show — the button simply does nothing, which is
// exactly how it was reported. A fetch either produces a file or produces a
// message saying why not.
//
// GET needs no CSRF header (Mattermost only checks CSRF for non-GET), but the
// same headers go out as everywhere else so there is one way to call this API.
export async function downloadJSON(path: string, filename: string): Promise<void> {
    const response = await fetch(PLUGIN_BASE + path, {
        credentials: 'same-origin',
        headers: baseHeaders(),
    });

    if (!response.ok) {
        throw new Error(await readError(response, `Could not build the file (${response.status})`));
    }

    const blob = await response.blob();
    if (blob.size === 0) {
        throw new Error('The server returned an empty file.');
    }

    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = filename;

    // Appended before clicking: a detached anchor is ignored by Firefox.
    document.body.appendChild(link);
    link.click();
    link.remove();

    // Kept for ten minutes, not let go at once: a browser reads the blob when
    // it starts saving, and Mattermost's desktop app only does that after its
    // own handling of the download (and a "save as" dialog, where one shows).
    // Let go on the next turn of the loop, the desktop app found nothing left
    // to read and showed the download as cancelled at 0 B.
    window.setTimeout(() => URL.revokeObjectURL(url), 10 * 60 * 1000);
}

// ---- Access: who is let in without a token, and unlinking (admin only) ----

export const fetchLetIn = (): Promise<LetInList> => requestJSON<LetInList>('/api/v1/admin/let-in');

export const letIn = (username: string): Promise<{ok: boolean; added: boolean; linked: boolean}> =>
    requestJSON<{ok: boolean; added: boolean; linked: boolean}>('/api/v1/admin/let-in', {
        method: 'POST',
        body: JSON.stringify({username}),
    });

// By id, because the row may belong to a renamed, deactivated or deleted
// account.
export const letOut = (userId: string): Promise<{ok: boolean; removed: boolean}> =>
    requestJSON<{ok: boolean; removed: boolean}>(`/api/v1/admin/let-in?userId=${encodeURIComponent(userId)}`, {method: 'DELETE'});

export const adminUnlink = (username: string): Promise<{ok: boolean; wasLinked: boolean; letIn: boolean}> =>
    requestJSON<{ok: boolean; wasLinked: boolean; letIn: boolean}>('/api/v1/admin/unlink', {
        method: 'POST',
        body: JSON.stringify({username}),
    });

// Pop out, for linked people while a Public watch URL is set: a plain link to
// the plugin, which answers with a redirect to the public watch page. The key
// that page needs never sits in anything the browser was sent.
export const popOutURL = (handle: string): string =>
    `${PLUGIN_BASE}/api/v1/watch/popout?handle=${encodeURIComponent(handle)}`;

export const exportAirtime = (): Promise<void> =>
    downloadJSON('/api/v1/admin/airtime', 'matty-airtime.json');

// Admin only. Which pieces of artwork are the admin's own rather than the ones
// the plugin ships with.
export const fetchBrand = (): Promise<Record<string, boolean>> =>
    requestJSON<Record<string, boolean>>('/api/v1/admin/brand');

// Admin only. Sent as raw bytes rather than a form or base64: the server reads
// the image out of the body and decides what it is from the bytes themselves,
// so there is nothing else in the request to parse or to trust.
export const uploadBrand = async (which: string, file: File): Promise<void> => {
    const response = await fetch(`${PLUGIN_BASE}/api/v1/admin/brand?which=${encodeURIComponent(which)}`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: {...baseHeaders(), 'Content-Type': file.type || 'application/octet-stream'},
        body: file,
    });

    if (!response.ok) {
        throw new Error(await readError(response, `Could not save that image (${response.status})`));
    }
};

// Admin only. Puts the shipped artwork back.
export const resetBrand = (which: string): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>(`/api/v1/admin/brand?which=${encodeURIComponent(which)}`, {method: 'DELETE'});

// Where the artwork is served from. A cache buster is appended when it has just
// been changed, because the browser is told to revalidate rather than to never
// cache, and the settings page is the one place that must show the new image
// the instant it is uploaded.
export const brandURL = (which: string, stamp?: number): string =>
    `${PLUGIN_BASE}/static/brand/${which}${stamp ? `?t=${stamp}` : ''}`;

// A 24/7 channel's picture, asked for by the stream's handle — never by its
// key, which does not reach the browser. The version is in the address, so a
// new picture is a new address and the old one can be cached.
export const channelPictureURL = (handle: string, version: string): string =>
    `${PLUGIN_BASE}/api/v1/channel/picture?handle=${encodeURIComponent(handle)}&v=${encodeURIComponent(version)}`;

// The picture a stream shows instead of a face, when it has one.
export const streamPictureURL = (stream: {handle: string; pictureVersion?: string}): string | undefined =>
    (stream.pictureVersion ? channelPictureURL(stream.handle, stream.pictureVersion) : undefined);

// A 24/7 channel's card picture: the wide one an admin chose to stand in for a
// live preview, by handle and version like the square one.
export const channelThumbnailURL = (handle: string, version: string): string =>
    `${PLUGIN_BASE}/api/v1/channel/thumbnail?handle=${encodeURIComponent(handle)}&v=${encodeURIComponent(version)}`;

export const streamThumbnailURL = (stream: {handle: string; thumbnailVersion?: string}): string | undefined =>
    (stream.thumbnailVersion ? channelThumbnailURL(stream.handle, stream.thumbnailVersion) : undefined);

// A live stream's latest preview still, shared by whoever last captured one.
// The version is the picture's own, so each new still is a new address.
export const stillURL = (handle: string, version: string): string =>
    `${PLUGIN_BASE}/api/v1/stills?handle=${encodeURIComponent(handle)}&v=${encodeURIComponent(version)}`;

// Asks whether this browser should capture a still of a stream now. 'watching'
// is a frame from a tile already playing, which costs nothing; 'peek' is a
// short hidden session opened only to take one. The server hands out one
// lease per stream at a time, so a still is taken once for everybody.
export type StillClaim = {lease: string; ttl?: number; retryAfter?: number};

export const claimStill = (handle: string, mode: 'watching' | 'peek'): Promise<StillClaim> =>
    requestJSON<StillClaim>(`/api/v1/stills/claim?handle=${encodeURIComponent(handle)}&mode=${mode}`, {method: 'POST'});

// Admin only. Every preview still showing right now, who took each one, and
// who is blocked from sharing them — the moderation tools, since a still is
// whatever the browser that took it sent.
export type AdminStill = {
    handle: string;
    title: string;
    version: string;
    at: string;
    byUserId: string;
    byUsername: string;
    byDisplayName: string;
};

export type BlockedSharer = {
    userId: string;
    username: string;
    displayName: string;
    blockedAt: string;
};

export type AdminStills = {stills: AdminStill[]; blocked: BlockedSharer[]};

export const fetchAdminStills = (): Promise<AdminStills> => requestJSON<AdminStills>('/api/v1/admin/stills');

export const removeStill = (handle: string): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>(`/api/v1/admin/stills?handle=${encodeURIComponent(handle)}`, {method: 'DELETE'});

export const blockSharer = (userId: string): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>('/api/v1/admin/stills/block', {method: 'POST', body: JSON.stringify({userId})});

export const unblockSharer = (userId: string): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>(`/api/v1/admin/stills/block?userId=${encodeURIComponent(userId)}`, {method: 'DELETE'});

// The captured still, as raw JPEG bytes under the lease that asked for it.
export const uploadStill = async (handle: string, lease: string, jpeg: Blob): Promise<void> => {
    const response = await fetch(`${PLUGIN_BASE}/api/v1/stills?handle=${encodeURIComponent(handle)}&lease=${encodeURIComponent(lease)}`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: {...baseHeaders(), 'Content-Type': 'image/jpeg'},
        body: jpeg,
    });

    if (!response.ok) {
        throw new Error(await readError(response, `Could not share that still (${response.status})`));
    }
};

// One always-on channel, as the settings page lists it. Admin only: this is the
// one place a stream key is named, to people who can read them in the console
// anyway.
export type AdminChannel = {
    key: string;

    // What the channel is named by in an address, as on the panel's own
    // pictures: an address can be written down in a proxy's log.
    handle: string;
    name?: string;
    custom: boolean;
    shipped: boolean;
    version?: string;

    // The wide card picture, which has no shipped default.
    thumbCustom?: boolean;
    thumbVersion?: string;
};

// Which of a channel's two pictures: the square one where a face would be, or
// the wide one on its card.
export type ChannelPictureKind = 'picture' | 'thumb';

const kindParam = (kind: ChannelPictureKind): string => (kind === 'thumb' ? '&kind=thumb' : '');

export const fetchChannelPictures = (): Promise<AdminChannel[]> =>
    requestJSON<AdminChannel[]>('/api/v1/admin/channel-pictures');

// Admin only. Raw bytes, like the artwork: the server decides what it is.
export const uploadChannelPicture = async (handle: string, file: File, kind: ChannelPictureKind = 'picture'): Promise<void> => {
    const response = await fetch(`${PLUGIN_BASE}/api/v1/admin/channel-pictures?handle=${encodeURIComponent(handle)}${kindParam(kind)}`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: {...baseHeaders(), 'Content-Type': file.type || 'application/octet-stream'},
        body: file,
    });

    if (!response.ok) {
        throw new Error(await readError(response, `Could not save that picture (${response.status})`));
    }
};

// Admin only. Back to the shipped picture, or to none.
export const resetChannelPicture = (handle: string, kind: ChannelPictureKind = 'picture'): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>(`/api/v1/admin/channel-pictures?handle=${encodeURIComponent(handle)}${kindParam(kind)}`, {method: 'DELETE'});

// Admin only. The settings page's preview, by handle.
export const adminChannelPictureURL = (handle: string, version?: string, kind: ChannelPictureKind = 'picture'): string =>
    `${PLUGIN_BASE}/api/v1/admin/channel-pictures?handle=${encodeURIComponent(handle)}${kindParam(kind)}${version ? `&v=${encodeURIComponent(version)}` : ''}`;

// Admin only. What the configured theme looks like, and which of its values
// Mattermost would quietly turn white on save.
export const fetchTheme = (): Promise<{theme: string; whitened?: string[]}> =>
    requestJSON<{theme: string; whitened?: string[]}>('/api/v1/admin/theme');

// Admin only. The starting list for "Start from the old bot's list", which
// comes from the server to a system admin rather than living in this bundle,
// which every user loads. Empty since 3.7.2 (server/birthday_seed.go).
export type BirthdaySeedEntry = {
    name: string;
    birthday: string;
    birth_year: number;
    username?: string;
    parent_username?: string[];
};

export const fetchBirthdaySeed = (): Promise<BirthdaySeedEntry[]> =>
    requestJSON<BirthdaySeedEntry[]>('/api/v1/admin/birthdays/seed');

// Admin only. Puts the theme on everybody who already has an account.
export const applyThemeToEveryone = (): Promise<{applied: number}> =>
    requestJSON<{applied: number}>('/api/v1/admin/theme', {method: 'POST'});

// Admin only. The query string is the only difference between a backup with
// passwords in it and one without.
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

// Admin only. The file goes up as it came down, so anything the plugin did not
// write is rejected by the server rather than guessed at here.
export const importSettings = (backup: string): Promise<SettingsImportResult> =>
    requestJSON<SettingsImportResult>('/api/v1/admin/settings', {
        method: 'POST',
        body: backup,
    });

export type BotIdentity = {
    userId: string;
    username: string;
    displayName: string;
    description: string;

    // Why a rename did not take. Mattermost refuses a username somebody else
    // already has, and the account keeps the one it had.
    error?: string;

    // The bot sitting on the configured name, when the plugin may take it
    // over — typically the original, left behind by an earlier plugin id — or
    // why it may not.
    adoptable?: {userId: string; username: string; owner: string};
    adoptBlocked?: string;
};

// Admin only. Moves the plugin onto an existing bot and retires the one it was
// using. The server looks the candidate up again rather than trusting this id,
// so a stale page cannot point it anywhere else.
export const adoptBot = (userId: string): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>('/api/v1/admin/bot/adopt', {
        method: 'POST',
        body: JSON.stringify({userId}),
    });

// Admin only. Which account the plugin posts as, so the settings page can show
// the picture and the name it is actually going by — which is not always the
// name in the setting above it.
export const fetchBotIdentity = (): Promise<BotIdentity> =>
    requestJSON<BotIdentity>('/api/v1/admin/avatar');

// Admin only. Puts an image on the plugin's bot. Sent as raw bytes rather than
// a form or base64: the server hands them straight to SetProfileImage, and
// there is nothing else in the request to parse.
export const uploadBotAvatar = async (file: File): Promise<void> => {
    const response = await fetch(`${PLUGIN_BASE}/api/v1/admin/avatar`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: {...baseHeaders(), 'Content-Type': file.type || 'application/octet-stream'},
        body: file,
    });

    if (!response.ok) {
        throw new Error(await readError(response, `Could not set the avatar (${response.status})`));
    }
};

// Admin only. Counts and timestamps for the System Console panel; deliberately
// not the tables themselves, which are keyed by stream key.
export const fetchAirtimeStatus = (): Promise<AirtimeStatus> =>
    requestJSON<AirtimeStatus>('/api/v1/admin/airtime/status');

// Admin only. Merges the safety copy taken when the plugin last started back
// into the live totals, keeping whichever figure is larger, so a restore can
// never undo airtime earned since the backup.
export const restoreAirtime = (): Promise<{changed: number; message: string}> =>
    requestJSON<{changed: number; message: string}>('/api/v1/admin/airtime', {
        method: 'POST',
        body: JSON.stringify({mode: 'restore'}),
    });

// Admin only. Takes an exported airtime file back in.
//
// Merge, not replace: the larger figure wins for each streamer, so importing
// cannot cost anybody hours and running it twice does nothing. This is how
// recorded history moves between servers — and between plugin IDs, which
// Mattermost treats as two entirely separate plugins with separate stores.
export const importAirtime = (exported: string): Promise<{changed: number; message: string}> => {
    let parsed: Record<string, unknown>;
    try {
        parsed = JSON.parse(exported) as Record<string, unknown>;
    } catch {
        throw new Error('That is not a readable airtime export.');
    }

    if (!parsed || typeof parsed !== 'object' || !('totals' in parsed)) {
        throw new Error('That file is not an airtime export. A settings backup goes in the Backup section instead.');
    }

    return requestJSON<{changed: number; message: string}>('/api/v1/admin/airtime', {
        method: 'POST',
        body: JSON.stringify({...parsed, mode: 'merge'}),
    });
};

// The schedule. Each call changes the document and returns; the new state
// arrives over the WebSocket with everything else, so neither returns the thing
// it changed.
const post = (path: string, body: unknown): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>(path, {method: 'POST', body: JSON.stringify(body)});

// startsAt is an instant in epoch milliseconds, converted from the streamer's
// own wall-clock pick by their own browser. The server never guesses a zone.
//
// category is 'movie', 'game' or '' for nothing said. The server keeps the two
// it knows and drops anything else rather than refusing the request.
// media is the ref of a title picked from the suggestions, when one was. The
// server looks it up again itself rather than taking the details from here.
//
// The answer names the new plan (design/3.2.0.md 2.2), so the sheet can offer
// to share it the moment it is saved. A server from before 3.2.0 sends no id,
// and then there is nothing to offer.
export const planStream = (title: string, startsAt: number, category: string, media?: string): Promise<{ok: boolean; id?: string}> =>
    requestJSON<{ok: boolean; id?: string}>('/api/v1/schedule', {
        method: 'POST',
        body: JSON.stringify({title, startsAt, category, ...(media ? {media} : {})}),
    });

// Changing a plan already made: its words, its time, what kind it is and
// which title it is linked to. Only the person who made it can.
export const editPlan = (id: string, title: string, startsAt: number, category: string, media?: string) =>
    post('/api/v1/schedule/edit', {id, title, startsAt, category, ...(media ? {media} : {})});

// Saying you are in on somebody's plan, or taking it back. Only for somebody
// whose own stream is linked — they are the ones who can stream in it.
export const joinPlan = (id: string, join: boolean) =>
    post('/api/v1/schedule/join', {id, join});

export type MediaSearch = {
    results: MediaResult[];

    // Set instead of results when this kind cannot be looked up here: switched
    // off, or no TMDB token for movies. Never for want of a Twitch application:
    // games then come from Steam alone, without IGDB.
    unavailable?: string;

    // A source that was asked and is missing from the results, 'Steam' or
    // 'IGDB': it did not answer in time, or failed. The results are the other
    // source's alone, and asking again shortly may well find more.
    incomplete?: string;
};

// Suggestions while typing a movie or game. The server does the looking up;
// the browser only ever talks to this plugin.
// A POST, with what was typed in the body: a search asks a provider in the
// reader's name, and nothing that merely loads an address can make one
// (server/media_api.go handleMediaSearch). It also keeps the words out of
// every log that records addresses.
export const searchMedia = (kind: 'movie' | 'game', query: string, signal?: AbortSignal): Promise<MediaSearch> =>
    requestJSON<MediaSearch>('/api/v1/media/search', {method: 'POST', body: JSON.stringify({kind, q: query}), signal});

// A title's art, relayed by the server: a small thumb for the suggestions,
// the cover for the profile, and the wide backdrop faded behind a card.
export const mediaImageURL = (ref: string, size: 'thumb' | 'cover' | 'backdrop'): string =>
    `${PLUGIN_BASE}/api/v1/media/image?ref=${encodeURIComponent(ref)}&size=${size}`;

// Remembered titles (media lookups). Admin only. What the server has kept of
// the titles and searches it looked up, counted only when this is asked, and
// never the titles themselves: the System Console card needs how much, not
// what.
export type MediaCacheStatus = {
    titles: number;
    searches: number;

    // Titles a provider said do not exist, remembered for a day.
    missing: number;

    // As stored, against the cache's whole allowance.
    bytes: number;
    limitBytes: number;

    // Entries by where they came from: tmdb, steam and igdb.
    byProvider?: Record<string, number>;

    // When the oldest entry was looked up, epoch milliseconds; 0 when empty.
    oldest: number;

    // Cover art kept on the server's own disk, so a picture fetched once is
    // not fetched again, even after a restart. Only where Mattermost's file
    // storage is local: picturesKept is false otherwise, and pictureProblem
    // says why when the folder itself could not be used. Absent from a server
    // older than the disk tier.
    picturesKept?: boolean;
    pictureProblem?: string;
    pictures?: number;
    pictureBytes?: number;
    pictureLimitBytes?: number;
    pictureLimit?: number;
};

export const fetchMediaCache = (): Promise<MediaCacheStatus> =>
    requestJSON<MediaCacheStatus>('/api/v1/admin/media/cache');

// Empties every remembered title and search, and this server's memory of them,
// and the pictures kept on disk. forgotten is how many entries there were,
// picturesForgotten how many picture files.
export const forgetMediaCache = (): Promise<{ok: boolean; forgotten: number; picturesForgotten?: number}> =>
    requestJSON<{ok: boolean; forgotten: number; picturesForgotten?: number}>('/api/v1/admin/media/cache', {method: 'DELETE'});

export const unplanStream = (id: string) =>
    post('/api/v1/schedule/remove', {id});

// What you are streaming right now, from the footer's What's on?. Only for
// somebody whose stream is linked, like planning. Every answer is what is
// stored now, null when nothing is, so the footer shows what the server has
// rather than what it asked for.
export type NowPlayingAnswer = {nowPlaying: NowPlaying | null};

export const fetchNowPlaying = (): Promise<NowPlayingAnswer> =>
    requestJSON<NowPlayingAnswer>('/api/v1/nowplaying');

// category is 'movie' or 'game', and one of them is needed. media is the ref
// of a title picked from the suggestions, which the server looks up itself.
export const saveNowPlaying = (category: string, title: string, media?: string): Promise<NowPlayingAnswer> =>
    requestJSON<NowPlayingAnswer>('/api/v1/nowplaying', {
        method: 'POST',
        body: JSON.stringify({category, title, ...(media ? {media} : {})}),
    });

// Taking it down. It also goes by itself once your stream has been off the
// air for a little while.
export const clearNowPlaying = (): Promise<NowPlayingAnswer> =>
    requestJSON<NowPlayingAnswer>('/api/v1/nowplaying', {method: 'DELETE'});

export const fetchMyProfile = (): Promise<Profile> => requestJSON<Profile>('/api/v1/me/profile');

export const saveMyProfile = (motd: string, isPublic: boolean): Promise<Profile> =>
    requestJSON<Profile>('/api/v1/me/profile', {
        method: 'POST',
        body: JSON.stringify({motd, isPublic}),
    });

// ---- Awards by your name, biggest fans, achievements (design/3.2.0.md 3-5) ----

// The reader's own choice of what shows beside their name (null for
// Automatic), and everything they hold to choose from. Linked only.
export const fetchMyAwards = (): Promise<AwardsMine> => requestJSON<AwardsMine>('/api/v1/awards/mine');

// Up to four award ids, in order, or null for Automatic. Linked only.
export const saveAwardChoice = (show: string[] | null): Promise<{ok: boolean; show: string[] | null}> =>
    requestJSON<{ok: boolean; show: string[] | null}>('/api/v1/awards/display', {
        method: 'POST',
        body: JSON.stringify({show}),
    });

// Back to Automatic: a takeback, open to everybody signed in.
export const clearAwardChoice = (): Promise<{ok: boolean; show: string[] | null}> =>
    requestJSON<{ok: boolean; show: string[] | null}>('/api/v1/awards/display', {method: 'DELETE'});

// What the reader's own profile popover may take back, answered from what the
// server holds in memory. Open, so somebody who has unlinked can reach it.
export const fetchTakebacks = (): Promise<Takebacks> => requestJSON<Takebacks>('/api/v1/takebacks');

// How far the reader is along each achievement family. Linked only.
export const fetchMyAchievements = (): Promise<AchievementProgress> =>
    requestJSON<AchievementProgress>('/api/v1/achievements/mine');

// ---- The soundboard (design/3.2.0.md 7.9 and 7.11) ----

// A refusal from a soundboard route: its code (busy, cooldown, muted...) and,
// where it is about time, how long until it would be let through.
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

async function requestSound<T>(path: string, init: RequestInit = {}): Promise<T> {
    let response: Response;
    try {
        response = await fetch(PLUGIN_BASE + path, {
            credentials: 'same-origin',
            ...init,
            headers: {...jsonHeaders(), ...(init.headers || {})},
        });
    } catch {
        throw new SoundRequestError(0, 'network', 0);
    }

    if (!response.ok) {
        let text = '';
        let code = '';
        let retryAfterMs = 0;
        try {
            const body = await response.json();
            text = body && typeof body.error === 'string' ? body.error : '';
            code = body && typeof body.code === 'string' ? body.code : '';
            retryAfterMs = body && typeof body.retryAfterMs === 'number' && body.retryAfterMs > 0 ? body.retryAfterMs : 0;
        } catch {
            // Not a JSON body; the status says what it can.
        }
        heardRefusal(code);
        throw new SoundRequestError(response.status, code || (response.status >= 500 ? 'store' : 'bad_request'), retryAfterMs, text);
    }

    return response.json() as Promise<T>;
}

const soundQuery = (id: string): string => `id=${encodeURIComponent(id)}`;

// The board, the reader's voice state read live from Mumble, and the waits.
// The list comes only while the reader is in Mumble.
export const fetchSounds = (): Promise<SoundBoard> => requestSound<SoundBoard>('/api/v1/sounds');

// A press. Answered once packet 0 is on the wire, or refused with a code and,
// where it is about time, retryAfterMs.
export const playSound = (id: string): Promise<SoundPlay> =>
    requestSound<SoundPlay>(`/api/v1/sounds/play?${soundQuery(id)}`, {method: 'POST'});

// A new sound: its name, the emoji by kind and name (the server looks it up
// itself), and the packets the browser encoded, as one base64 blob of a u16
// count and then each packet's u16 length and bytes (7.8).
export type NewSound = {
    name: string;
    emoji: {kind: 'system' | 'custom'; name: string};
    packets: string;
};

export const addSound = (sound: NewSound): Promise<{id: string}> =>
    requestSound<{id: string}>('/api/v1/sounds', {method: 'POST', body: JSON.stringify(sound)});

// Renaming one of your own, or giving it another emoji.
export const editSound = (id: string, change: {name?: string; emoji?: {kind: 'system' | 'custom'; name: string}}): Promise<{ok: boolean}> =>
    requestSound<{ok: boolean}>(`/api/v1/sounds/edit?${soundQuery(id)}`, {method: 'POST', body: JSON.stringify(change)});

// One of your own, gone: a takeback.
export const removeSound = (id: string): Promise<{ok: boolean}> =>
    requestSound<{ok: boolean}>(`/api/v1/sounds/remove?${soundQuery(id)}`, {method: 'POST'});

// Every sound the reader added, gone at once: a takeback, open to anybody,
// whether the soundboard is on or off.
export const removeMySounds = (): Promise<{deleted: number}> =>
    requestSound<{deleted: number}>('/api/v1/sounds/remove-mine', {method: 'POST'});

// A sound's stored packets, for Listen here: the owner's own only. Sent with
// the panel's headers, which is what lets the server answer it at all (a
// Markdown image cannot send them).
async function soundBytes(path: string): Promise<ArrayBuffer> {
    let response: Response;
    try {
        response = await fetch(PLUGIN_BASE + path, {credentials: 'same-origin', headers: baseHeaders()});
    } catch {
        throw new SoundRequestError(0, 'network', 0);
    }
    if (!response.ok) {
        const {text, code} = await readRefusal(response);
        heardRefusal(code);
        throw new SoundRequestError(response.status, code || 'bad_request', 0, text);
    }

    return response.arrayBuffer();
}

export const fetchSoundAudio = (id: string): Promise<ArrayBuffer> => soundBytes(`/api/v1/sounds/audio?${soundQuery(id)}`);

// System Console → Sounds on the server. System admins only, with or without a
// token, as every admin route.
export const fetchAdminSounds = (): Promise<AdminSounds> => requestSound<AdminSounds>('/api/v1/admin/sounds');

export const fetchAdminSoundAudio = (id: string): Promise<ArrayBuffer> => soundBytes(`/api/v1/admin/sounds/audio?${soundQuery(id)}`);

export const adminRemoveSound = (id: string): Promise<{ok: boolean}> =>
    requestSound<{ok: boolean}>(`/api/v1/admin/sounds/remove?${soundQuery(id)}`, {method: 'POST'});

export const adminBlockSounds = (username: string, blocked: boolean): Promise<{ok: boolean; userId: string}> =>
    requestSound<{ok: boolean; userId: string}>('/api/v1/admin/sounds/block', {method: 'POST', body: JSON.stringify({username, blocked})});

export const adminCheckSounds = (): Promise<{ok: boolean; line?: string}> =>
    requestSound<{ok: boolean; line?: string}>('/api/v1/admin/sounds/check', {method: 'POST'});

export const adminRestartSounds = (): Promise<{ok: boolean}> =>
    requestSound<{ok: boolean}>('/api/v1/admin/sounds/restart', {method: 'POST'});

// ---- Twitch (design/twitch-live.md 4.1 and 5.10) ----

// A single-use ticket for one Connect window, by a POST that carries the CSRF
// token, so a link, an embedded picture or another site cannot start a
// connection with the reader's session: only the panel can. Asked for on the
// first press of Connect Twitch, so it is in hand for the second.
export const fetchConnectTicket = (): Promise<{ticket: string; expiresIn: number}> =>
    requestJSON<{ticket: string; expiresIn: number}>('/api/v1/twitch/connect-ticket', {method: 'POST'});

// Where the Connect window goes: a plugin address, which spends the ticket
// and sends the window on to Twitch. A plugin address because in the desktop
// app only one opens the in-app window that carries the Mattermost session.
export const twitchConnectURL = (ticket: string): string =>
    `${PLUGIN_BASE}/api/v1/twitch/connect?t=${encodeURIComponent(ticket)}`;

// Disconnecting: open to everybody, the locked included, whatever the switch
// says, because a person's right to withdraw does not depend on it.
export const unlinkTwitch = (): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>('/api/v1/twitch/link', {method: 'DELETE'});

// "Your Twitch account was connected to someone else", read.
export const dismissTwitchNotice = (): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>('/api/v1/twitch/notice', {method: 'POST'});

// A Twitch stream's preview picture, fetched by the server from Twitch and
// relayed, so browsers never fetch Twitch's pictures themselves.
export const twitchPreviewURL = (handle: string, version: string): string =>
    `${PLUGIN_BASE}/api/v1/twitch/preview?handle=${encodeURIComponent(handle)}&v=${encodeURIComponent(version)}`;

// System Console → Twitch connections. Every one of these works whatever the
// switch says, so connections can always be taken away.
export const fetchAdminTwitch = (): Promise<AdminTwitch> => requestJSON<AdminTwitch>('/api/v1/admin/twitch');

export const adminUnlinkTwitch = (userId: string, block = false): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>(`/api/v1/admin/twitch?user=${encodeURIComponent(userId)}${block ? '&block=1' : ''}`, {method: 'DELETE'});

export const deleteAllTwitch = (): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>('/api/v1/admin/twitch?all=1', {method: 'DELETE'});

export const adminBlockTwitch = (userId: string, blocked: boolean): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>('/api/v1/admin/twitch/block', {method: 'POST', body: JSON.stringify({userId, blocked})});

export const adminClearTwitchBlocks = (): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>('/api/v1/admin/twitch/block', {method: 'POST', body: JSON.stringify({all: true, blocked: false})});

export const adminTwitchAllDay = (userId: string, allDay: boolean): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>('/api/v1/admin/twitch/allday', {method: 'POST', body: JSON.stringify({userId, allDay})});

// ---- Steam and Hytale (design/3.4.0.md 2.4 and 5.7) ----
// One set of calls for both, by service: the server serves them through one
// core, and every route has the same shape under /steam and /hytale. Each
// refusal is {"error", "code"}, kept on the RequestError, so the panel shows
// the server's sentence.

// A single-use ticket for one Connect window, as Twitch's: a POST that
// carries the CSRF token, so only the panel can start a connection with the
// reader's session. Asked for as the Confirm step opens, so it is in hand
// when Continue is pressed.
export const fetchAccountTicket = (service: AccountService): Promise<{ticket: string; expiresIn: number}> =>
    requestJSON<{ticket: string; expiresIn: number}>(`/api/v1/${service}/connect-ticket`, {method: 'POST'});

// Where the Connect window goes: a plugin address, which spends the ticket
// and sends the window on to Steam's or Hytale's own sign-in page. A plugin
// address, as Twitch's is, because in the desktop app only one opens the
// in-app window that carries the Mattermost session.
export const accountConnectURL = (service: AccountService, ticket: string): string =>
    `${PLUGIN_BASE}/api/v1/${service}/connect?t=${encodeURIComponent(ticket)}`;

// Disconnecting: open to everybody, the locked included, whatever the switch
// says, because a person's right to withdraw does not depend on it.
export const unlinkAccount = (service: AccountService): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>(`/api/v1/${service}/link`, {method: 'DELETE'});

// "Your Steam account was connected to someone else", read.
export const dismissAccountNotice = (service: AccountService): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>(`/api/v1/${service}/notice`, {method: 'POST'});

// Everybody's Steam status and Hytale usernames, for readers who may watch;
// the gate refuses the locked. Asked by accounts.ts only.
export const fetchAccounts = (): Promise<AccountsView> => requestJSON<AccountsView>('/api/v1/accounts');

export const fetchAccountsIfChanged = (tag: string): Promise<Tagged<AccountsView> | null> =>
    requestIfChanged<AccountsView>('/api/v1/accounts', tag);

// System Console → Steam connections and Hytale connections. Every one of
// these works whatever the switch says, so connections can always be taken
// away.
export const fetchAdminSteam = (): Promise<AdminSteam> => requestJSON<AdminSteam>('/api/v1/admin/steam');

export const fetchAdminHytale = (): Promise<AdminHytale> => requestJSON<AdminHytale>('/api/v1/admin/hytale');

export const adminUnlinkAccount = (service: AccountService, userId: string, block = false): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>(`/api/v1/admin/${service}?user=${encodeURIComponent(userId)}${block ? '&block=1' : ''}`, {method: 'DELETE'});

export const deleteAllAccount = (service: AccountService): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>(`/api/v1/admin/${service}?all=1`, {method: 'DELETE'});

export const adminBlockAccount = (service: AccountService, userId: string, blocked: boolean): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>(`/api/v1/admin/${service}/block`, {method: 'POST', body: JSON.stringify({userId, blocked})});

export const adminClearAccountBlocks = (service: AccountService): Promise<{ok: boolean}> =>
    requestJSON<{ok: boolean}>(`/api/v1/admin/${service}/block`, {method: 'POST', body: JSON.stringify({all: true, blocked: false})});

// ---- Clips ----
// Every clip route answers a refusal as {"error": text, "code": code}, and the
// panel words each code itself, so these keep both. All clip HTTP is here:
// the lab swaps this one module, so a request made anywhere else would go to
// the network from the lab.

// What a clip request was refused with: the HTTP status, the server's code,
// and the server's own words. status is 0, and code 'network', when nothing
// came back at all. An aborted request is not one of these: its AbortError is
// thrown as it is, so a caller can tell "stopped" from "failed".
//
// serverText is only ever what the plugin's server wrote in "error", and ''
// when it wrote nothing: Mattermost refusing before the plugin saw the
// request, a proxy's error page, no answer. The message falls back to a
// placeholder for logs, and is never shown as if the server had said it.
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

// The code for an answer that carried none: a proxy's error page, Mattermost's
// own refusal before the plugin saw the request, or an older server. Worded
// for what the person can do about it, since that is all a code is for.
function codeForStatus(status: number): ClipErrorCode {
    if (status === 429) {
        return 'rate_limited';
    }
    if (status === 413) {
        return 'bad_thumb';
    }
    if (status === 401 || status === 403) {
        return 'forbidden';
    }
    if (status >= 500) {
        // A gateway that could not reach Mattermost, or Mattermost that could
        // not reach the plugin: as good as no answer, and as worth retrying.
        return 'network';
    }

    return 'bad_request';
}

async function clipErrorOf(response: Response): Promise<ClipRequestError> {
    let text = '';
    let code: ClipErrorCode = codeForStatus(response.status);
    try {
        const body = await response.json();
        if (body && typeof body.error === 'string') {
            text = body.error;
        }

        // Taken as sent, even one this build has no words for: the panel then
        // shows the server's own text, which is fixed and safe to show.
        if (body && typeof body.code === 'string' && body.code !== '') {
            code = body.code as ClipErrorCode;
        }
    } catch {
        // Not a JSON body; the status says what it can.
    }

    heardRefusal(code);

    return new ClipRequestError(response.status, code, text);
}

function isAbort(reason: unknown): boolean {
    return typeof reason === 'object' && reason !== null && (reason as {name?: unknown}).name === 'AbortError';
}

// requestJSON for the clip routes: the same headers and the same session, but
// a refusal keeps its status and code instead of becoming a bare message.
async function requestClip<T>(path: string, init: RequestInit = {}): Promise<T> {
    let response: Response;
    try {
        response = await fetch(PLUGIN_BASE + path, {
            credentials: 'same-origin',
            ...init,
            headers: {...jsonHeaders(), ...(init.headers || {})},
        });
    } catch (reason) {
        if (isAbort(reason)) {
            throw reason;
        }

        // fetch rejects with a TypeError when no answer came at all: offline,
        // refused, or cut off.
        throw new ClipRequestError(0, 'network');
    }

    if (!response.ok) {
        throw await clipErrorOf(response);
    }

    try {
        return await response.json() as T;
    } catch (reason) {
        if (isAbort(reason)) {
            throw reason;
        }

        // A 200 whose body never arrived whole is a broken answer, and as
        // worth retrying as no answer.
        throw new ClipRequestError(0, 'network');
    }
}

const clipQuery = (name: string, value: string): string => `${name}=${encodeURIComponent(value)}`;

const clipJSON = <T>(method: string, path: string, body?: unknown): Promise<T> =>
    requestClip<T>(path, {method, ...(body === undefined ? {} : {body: JSON.stringify(body)})});

export const fetchClips = (): Promise<{clips: Clip[]}> => requestClip<{clips: Clip[]}>('/api/v1/clips');

// 410 clip_gone once it has expired or been deleted.
export const fetchClip = (id: string): Promise<Clip> => requestClip<Clip>(`/api/v1/clips/clip?${clipQuery('id', id)}`);

// The server takes at most this many ids in one request.
const CARDS_PER_REQUEST = 25;

// The cards of shared clips, by id: null for a clip that has gone. Asked for
// in as few requests as the server allows, each id once, and not at all for
// none.
export const fetchClipCards = async (ids: string[]): Promise<{cards: Record<string, ClipCard | null>}> => {
    const wanted = Array.from(new Set(ids));
    const batches: string[][] = [];
    for (let at = 0; at < wanted.length; at += CARDS_PER_REQUEST) {
        batches.push(wanted.slice(at, at + CARDS_PER_REQUEST));
    }

    const answers = await Promise.all(batches.map((batch) => (
        requestClip<{cards: Record<string, ClipCard | null>}>(`/api/v1/clips/cards?${clipQuery('ids', batch.join(','))}`)
    )));

    const cards: Record<string, ClipCard | null> = {};
    answers.forEach((answer) => {
        Object.entries(answer?.cards || {}).forEach(([id, card]) => {
            cards[id] = card || null;
        });
    });

    return {cards};
};

// The clip itself and its picture, by address: the browser fetches them for a
// <video> and an <img>, with the session cookie, and the server gates each
// request like every other. The picture's version is in the address so a new
// one is a new address.
export const clipVideoURL = (id: string): string => `${PLUGIN_BASE}/api/v1/clips/video?${clipQuery('id', id)}`;

export const clipThumbURL = (id: string, v: string): string =>
    `${PLUGIN_BASE}/api/v1/clips/thumb?${clipQuery('id', id)}&${clipQuery('v', v)}`;

// up is the state wanted, never a toggle, so a retry cannot undo itself.
export const voteClip = (id: string, up: boolean): Promise<{upvotes: number; voted: boolean}> =>
    clipJSON<{upvotes: number; voted: boolean}>('POST', `/api/v1/clips/vote?${clipQuery('id', id)}`, {up});

export const reportClip = (id: string): Promise<{ok: boolean}> =>
    clipJSON<{ok: boolean}>('POST', `/api/v1/clips/report?${clipQuery('id', id)}`);

export const deleteClip = (id: string): Promise<{ok: boolean}> =>
    clipJSON<{ok: boolean}>('DELETE', `/api/v1/clips/clip?${clipQuery('id', id)}`);

// Posts the clip into a channel as the person sharing it. 201 for a new post,
// 200 with the same post for a repeat within a minute, so a retry after a
// network error cannot post twice.
export const shareClip = (id: string, channelId: string): Promise<{postId: string; channelId: string}> =>
    clipJSON<{postId: string; channelId: string}>('POST', `/api/v1/clips/share?${clipQuery('id', id)}`, {channelId});

// Posts a plan on the list into a channel as the person sharing it
// (design/3.2.0.md 6.3): a card whose props carry only the plan's id and
// start, never its title or a name. The same answer as a clip's share, and the
// same minute in which a repeat gets the post already made. Answered the clip
// routes' way, so the share machinery words its refusals alike.
export const sharePlan = (id: string, channelId: string): Promise<{postId: string; channelId: string}> =>
    clipJSON<{postId: string; channelId: string}>('POST', '/api/v1/schedule/share', {id, channelId});

export const fetchDrafts = (): Promise<{drafts: ClipDraft[]}> => requestClip<{drafts: ClipDraft[]}>('/api/v1/clips/drafts');

// One press of the scissors: the server cuts the last few minutes of that
// stream, or of the end of it it still holds, into a draft for this person.
export const createDraft = (handle: string, source: ClipSource): Promise<ClipDraft> =>
    clipJSON<ClipDraft>('POST', '/api/v1/clips/drafts', {handle, source});

export const deleteDraft = (id: string): Promise<{ok: boolean}> =>
    clipJSON<{ok: boolean}>('DELETE', `/api/v1/clips/draft?${clipQuery('id', id)}`);

export const draftVideoURL = (id: string): string => `${PLUGIN_BASE}/api/v1/clips/draft/video?${clipQuery('id', id)}`;

// Whether a draft's file can still be had, asked for its first byte: for the
// editor, when its <video> would not load. A <video> that fails says only
// that it failed, the same for a draft that expired, one the streamer wiped,
// a link that has gone and a dropped connection; the route itself says which,
// in its code. Resolves when the file is there; rejects as requestClip does.
export const checkDraftVideo = async (id: string): Promise<void> => {
    let response: Response;
    try {
        response = await fetch(draftVideoURL(id), {
            credentials: 'same-origin',
            headers: {...baseHeaders(), Range: 'bytes=0-0'},
        });
    } catch (reason) {
        if (isAbort(reason)) {
            throw reason;
        }

        throw new ClipRequestError(0, 'network');
    }

    if (!response.ok) {
        throw await clipErrorOf(response);
    }

    // The one byte is not wanted; the answer was.
    try {
        await response.body?.cancel();
    } catch {
        // Already read or already gone, which is as good.
    }
};

// The picture for a draft, taken in the browser from the frame it opens on, as
// raw JPEG bytes: the server checks them as it checks a preview still.
export const uploadDraftThumb = async (id: string, jpeg: Blob): Promise<void> => {
    await requestClip<{ok: boolean}>(`/api/v1/clips/draft/thumb?${clipQuery('id', id)}`, {
        method: 'PUT',
        headers: {...baseHeaders(), 'Content-Type': 'image/jpeg'},
        body: jpeg,
    });
};

// The clip id is the draft's, so a publish whose answer was lost can be looked
// for with fetchClip(draftId).
export const publishDraft = (id: string, body: {title: string; inMs: number; outMs: number}): Promise<Clip> =>
    clipJSON<Clip>('POST', `/api/v1/clips/draft/publish?${clipQuery('id', id)}`, body);

// Whether clips may be made of the caller's own linked stream. Switching it
// off also wipes what is held of it.
export const setClipsAllowed = (allow: boolean): Promise<{allowed: boolean}> =>
    clipJSON<{allowed: boolean}>('PUT', '/api/v1/clips/streamer', {allow});

// Whether people may download clips of the caller's own linked stream
// (3.2.1): the streamer's second switch, beside the first.
export const setClipDownloads = (downloads: boolean): Promise<{downloads: boolean}> =>
    clipJSON<{downloads: boolean}>('PUT', '/api/v1/clips/streamer', {downloads});

// A ticket for one download of one clip (design/3.2.1.md 1.4), by a POST that
// carries the CSRF token, so only the panel can start a download. The answer
// is the address to save it from, which the server spends the ticket at.
export const fetchClipDownload = (id: string): Promise<{url: string}> =>
    clipJSON<{url: string}>('POST', '/api/v1/clips/download-ticket', {id});

// Saving from that address: a temporary <a download>, pressed and taken away,
// so the browser saves the file to disk as it arrives and the page never
// holds it; a five-minute clip is a large file. The browser, or the desktop
// app, keeps the file's own name from the server.
export const saveClipFrom = (url: string): void => {
    const link = document.createElement('a');
    link.href = url;
    link.download = '';
    link.rel = 'noopener';
    link.style.display = 'none';
    document.body.appendChild(link);
    link.click();
    link.remove();
};

// Throws away what is held of a stream, by its streamer.
export const wipeRecording = (handle: string): Promise<{ok: boolean; draftsRemoved: number}> =>
    clipJSON<{ok: boolean; draftsRemoved: number}>('POST', `/api/v1/clips/wipe?${clipQuery('handle', handle)}`);

// System Console only, for any system admin, linked or not. Its own routes,
// so the panel's link rule stays whole.
export const fetchAdminClips = (): Promise<AdminClips> => requestClip<AdminClips>('/api/v1/admin/clips');

export const adminClipVideoURL = (id: string): string => `${PLUGIN_BASE}/api/v1/admin/clips/video?${clipQuery('id', id)}`;

export const adminClipThumbURL = (id: string, v: string): string =>
    `${PLUGIN_BASE}/api/v1/admin/clips/thumb?${clipQuery('id', id)}&${clipQuery('v', v)}`;

// Deleting from the console voids the clip's upvotes, as an admin's delete
// from the panel does.
export const adminDeleteClip = (id: string): Promise<{ok: boolean}> =>
    clipJSON<{ok: boolean}>('DELETE', `/api/v1/admin/clips/clip?${clipQuery('id', id)}`);

export const adminWipeRecording = (handle: string): Promise<{ok: boolean; draftsRemoved: number}> =>
    clipJSON<{ok: boolean; draftsRemoved: number}>('POST', `/api/v1/admin/clips/wipe?${clipQuery('handle', handle)}`);

export const blockClipper = (userId: string): Promise<{ok: boolean; draftsRemoved: number}> =>
    clipJSON<{ok: boolean; draftsRemoved: number}>('POST', '/api/v1/admin/clips/block', {userId});

export const unblockClipper = (userId: string): Promise<{ok: boolean}> =>
    clipJSON<{ok: boolean}>('DELETE', `/api/v1/admin/clips/block?${clipQuery('userId', userId)}`);

export const resetClipperScore = (userId: string): Promise<{ok: boolean}> =>
    clipJSON<{ok: boolean}>('POST', '/api/v1/admin/clips/reset-score', {userId});

export const removeClipThumb = (id: string): Promise<{ok: boolean}> =>
    clipJSON<{ok: boolean}>('DELETE', `/api/v1/admin/clips/thumb?${clipQuery('id', id)}`);

export const clearClipReports = (id: string): Promise<{ok: boolean}> =>
    clipJSON<{ok: boolean}>('DELETE', `/api/v1/admin/clips/reports?${clipQuery('id', id)}`);

// ---- Mattermost's own API ----
// For the hashtag search: the same server, the same session and the same
// headers as every call above — X-Requested-With and the CSRF token are what
// Mattermost wants on a POST from a signed-in browser (web/handlers.go,
// checkCSRFToken) — so these ask only what the reader could ask Mattermost
// themselves, and see only what their own search would show them.

// Mattermost's errors carry their reason in message, not error. A few are
// worth saying in words of the panel's own, because the reason is the server's
// setup rather than anything the reader can retry.
async function readMattermostError(response: Response, fallback: string): Promise<string> {
    switch (response.status) {
    case 401:
        return 'Your Mattermost session has ended. Reload the page to sign in again.';
    case 403:
        return 'You can’t search the messages here.';
    case 501:
        return 'Searching messages is switched off on this server.';
    case 503:
        return 'Mattermost is too busy to search right now. Try again in a moment.';
    default:
        break;
    }

    try {
        const body = await response.json();
        if (body && typeof body.message === 'string' && body.message) {
            return body.message;
        }
    } catch {
        // Not a JSON error body.
    }

    return fallback;
}

async function requestMattermost<T>(path: string, init: RequestInit = {}): Promise<T> {
    const response = await fetch(`/api/v4${path}`, {
        credentials: 'same-origin',
        ...init,
        headers: {...jsonHeaders(), ...(init.headers || {})},
    });

    if (!response.ok) {
        throw new Error(await readMattermostError(response, `Mattermost could not answer (${response.status})`));
    }

    return response.json() as Promise<T>;
}

export type PostSearch = {
    // Empty for every team the reader is in. A team's search also covers
    // their direct and group messages, which belong to no team.
    teamId: string;
    terms: string;
    page: number;
    perPage: number;
    archived: boolean;
};

// Exactly the request Mattermost's own search box sends (Client4
// searchPostsWithParams, from actions/views/rhs.ts performSearch). The time
// zone only matters to on:, before: and after:, and is sent as Mattermost
// sends it, in seconds east of UTC.
export const searchPosts = (search: PostSearch, signal?: AbortSignal): Promise<PostSearchPage> =>
    requestMattermost<PostSearchPage>(search.teamId === '' ? '/posts/search' : `/teams/${encodeURIComponent(search.teamId)}/posts/search`, {
        method: 'POST',
        signal,
        body: JSON.stringify({
            terms: search.terms,
            is_or_search: false,
            time_zone_offset: -new Date().getTimezoneOffset() * 60,
            page: search.page,
            per_page: search.perPage,
            include_deleted_channels: search.archived,
        }),
    });

// People Mattermost has not already loaded, in one request. Mattermost
// refuses an empty list, so none is sent.
export const fetchUsersByIds = (ids: string[], signal?: AbortSignal): Promise<MattermostUser[]> =>
    (ids.length === 0 ? Promise.resolve([]) : requestMattermost<MattermostUser[]>('/users/ids', {
        method: 'POST',
        signal,
        body: JSON.stringify(ids),
    }));

export const fetchChannel = (channelId: string, signal?: AbortSignal): Promise<MattermostChannel> =>
    requestMattermost<MattermostChannel>(`/channels/${encodeURIComponent(channelId)}`, {signal});

// exchangeWHEP hands the SDP offer to the plugin, which relays it to Broadcast
// Box. The browser never talks to Broadcast Box's API directly, so there is no
// CORS surface and no credentials in the page.
//
// peekLease marks a session opened only to take a preview still, so the server
// can leave it out of the stream's viewer count.
export async function exchangeWHEP(handle: string, offer: string, signal?: AbortSignal, peekLease?: string): Promise<string> {
    const headers: Record<string, string> = {
        ...baseHeaders(),
        'Content-Type': 'application/sdp',
        'X-Stream-Handle': handle,
    };
    if (peekLease) {
        headers['X-Matty-Peek'] = peekLease;
    }

    const response = await fetch(`${PLUGIN_BASE}/api/v1/whep`, {
        method: 'POST',
        credentials: 'same-origin',
        signal,
        headers,
        body: offer,
    });

    if (!response.ok) {
        const {text, code} = await readRefusal(response);
        heardRefusal(code);
        throw new RequestError(text || `Could not start playback (${response.status})`, response.status, code, text);
    }

    return response.text();
}
