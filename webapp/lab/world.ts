import type {
    AchievementHolder,
    AchievementKind,
    AdminSounds,
    BestClipper,
    Boards,
    Clip,
    ClipCard,
    ClipDraft,
    ClipPerson,
    ClipTag,
    ClipTail,
    ClipsInfo,
    HeldAward,
    Leader,
    MattermostChannel,
    MattermostPost,
    MattermostUser,
    Me,
    MonthMedals,
    NowPlaying,
    Profile,
    ScheduleItem,
    Snapshot,
    Stream,
    VoiceChannel,
    VoicePerson,
} from '../src/types';
import {clipTime, tagKindOf, tagTitleOf} from '../src/clipLogic';
import type {AccountsSteamState, AccountsView, ClipPostProps, FanView, LedgerAlert, LetInPerson, Sound, SoundBoard, SoundEmoji, SoundWhy} from '../src/types';
import {labEmojiNamed} from './mocks/emojiImages';
import {besideOf, heldOf, holdersOf, namedKind, onCardOf, rowsOf} from './awards';
import type {AwardFacts} from './awards';

import type {AdminPush, MediaCacheStatus} from '../src/client';
import {detailsOf} from './media';
import type {ReleaseAlert, ReleaseAnswer} from '../src/types';
import {PLUGIN_ID} from './mocks/manifest';

// The lab's voice server: Lounge (the root), Gaming under it with Duo, which
// takes two, under that, and AFK last.
export const LAB_VOICE_CHANNELS: VoiceChannel[] = [
    {id: 0, name: 'Lounge', depth: 0},
    {id: 1, name: 'Gaming', parent: 0, depth: 1},
    {id: 2, name: 'Duo', parent: 1, depth: 2, max: 2},
    {id: 3, name: 'AFK', parent: 0, depth: 1},
];

// The lab's fake server.
//
// One mutable object standing in for everything the plugin would ask a
// Mattermost server and a Broadcast Box origin for. The mocked client reads and
// writes it, the control panel reads and writes it, and both go through notify
// so the panel re-renders the moment anything changes.
//
// The point of the lab is that the PANEL is the real thing — the same Sidebar,
// the same styles, the same timezone code. Only the two edges are replaced: the
// HTTP client and the WebRTC player. So a layout that looks right here looks
// right in Mattermost, and a wrong "Tomorrow" is wrong in both.

export type World = {
    me: Me;
    profile: Profile;
    streams: Stream[];
    schedule: ScheduleItem[];
    allTime: Leader[];
    month: Leader[];
    monthLabel: string;
    allTimeTotal: number;
    monthTotal: number;

    // Where people finished in the months already over, as the snapshot
    // carries it: for the profile popover's row and the trophy menu's
    // Monthly medals table, and never as a badge beside a name in a post.
    monthMedals: MonthMedals[];

    // The Top Snoopers boards behind the trophy's Snoopers tab, and whether
    // the setting is on at all: on with both empty is "Nobody has watched
    // yet", and off hides the tab.
    watchBoards: boolean;
    watchMonth: Leader[];
    watchAllTime: Leader[];
    watchMonthTotal: number;
    watchAllTimeTotal: number;
    configured: boolean;
    error: string;

    // How long the fake server takes to answer, and whether the next write
    // fails. Both are here so the panel's saving and error states can actually
    // be looked at rather than imagined.
    latencyMs: number;
    failWrites: boolean;

    // Who is in voice, and whether the plugin is connected to Mumble at all.
    // Two separate things: an empty list with connected true means nobody is in
    // there, and connected false means the panel is not in a position to say.
    voiceConnected: boolean;
    voice: VoicePerson[];

    // The voice server's channels, in the order the server sends them (a
    // channel before the ones under it). Empty is a server before 4.3.0,
    // which sends none.
    voiceChannels: VoiceChannel[];

    // The "Live preview pictures" setting.
    livePreviews: boolean;

    // What each streamer said with What's on?, by user id, as the server
    // stores it. Dave's is linked to a real game, so it has art; Bob's was
    // said with lookups off, so it is a kind and words alone. Victor's is set
    // from the panel's own footer.
    nowPlaying: Record<string, NowPlaying>;

    // How Mattermost answers a hashtag search: with the sample messages
    // below, with none, with an error, or as a server with search switched
    // off. And whether a popped-out panel's own window still answers it.
    tagSearch: 'messages' | 'none' | 'fails' | 'off';
    mainWindowGone: boolean;

    // How many times the lab's own "linked" switch has been pressed. The
    // panel's remount key counts these rather than reading the stream key, so
    // linking or unlinking from the panel's own footer changes the panel in
    // place, as it does in Mattermost, while the switch — a change made
    // somewhere the panel could not see — still remounts it.
    linkSwitches: number;

    // Remembered titles (media lookups): what the System Console card counts,
    // and whether forgetting them fails, as the server's 500 does.
    mediaCache: MediaCacheStatus;
    mediaCacheForgetFails: boolean;

    // IGDB slower than a game search waits for: answers come from Steam
    // alone and say IGDB is missing from them, as the server's do.
    igdbSlow: boolean;

    // "me" cannot be read: the Access row's "unknown", where the page has no
    // "me" and asking again gets none either, so a shared clip's card stays
    // as it is for a reader nobody knows (and its Watch says it could not
    // check) rather than the lab answering the card's own ask for "me".
    meWithheld: boolean;

    // ---- The lock (design/lock-screen.md) ----
    // Victor's access is worked out from these as the server would: linked
    // while he has a stream key, else let in while a system admin has him on
    // the list, else locked (labAccessOf).
    letIn: boolean;

    // How long the next "me" takes, for the panel's starting face, and how
    // many of the next answers say the store did not answer ('unknown').
    meDelayMs: number;
    storeBlips: number;

    // What the next POST /me/link answers, the lock's switch.
    linkAnswer: LabLinkAnswer;

    // A Public watch URL being set (Pop out), and every admin alert at once,
    // for an admin: in the unlocked panel, never on the lock.
    publicWatch: boolean;
    everyAlert: boolean;

    // The System Console's Access card: who is let in, as the server's list
    // has them; what it says is wrong with the list; whether a newer version
    // saved it; and how the next add answers.
    letInPeople: LetInPerson[];
    letInProblem: string;
    letInReadOnly: boolean;
    letInAddError: string;

    // ---- Clips ----
    // The server's clips document as the lab keeps it: every published clip
    // with who voted for it and who reported it, the drafts, the banked
    // scores, and the switches. The mock client answers each request from
    // these by the server's rules, working out each reader's view of a clip
    // the way the server does, rather than storing views.
    clips: LabClip[];
    drafts: LabDraft[];

    // EnableClips, ClipLengthSeconds and ClipRetentionDays.
    clipsEnabled: boolean;
    clipLength: number;
    retentionDays: number;

    // The one Best Clipper, as the server last worked it out: recomputed by
    // every vote, delete, block and reset (recomputeBestClipper), and set
    // straight by the lab's own control.
    bestClipper: BestClipper | null;

    // The newest clip the reader had seen, for "new" (the browser keeps it).
    clipsSeen: number;

    // The ends of streams that went off or dropped, still held, each with
    // what its stream was tagged with when it stopped, for a draft cut from
    // it (addendum 4.14; the server keeps that with the tail and never sends
    // it).
    tails: LabTail[];

    // When a stream's recording last started again, by handle: after a wipe
    // (by its streamer, by an admin, or by switching clipping of it off), or
    // after it came back from a drop. Its recording counts from then; after
    // a wipe the end of an earlier stretch from before then is gone too.
    restartedAt: Record<string, number>;

    // Whether clips may be made of Victor's stream: the streamer's switch.
    // The opt-out belongs to the stream, so it stays off when he unlinks.
    streamerAllowed: boolean;

    // 3.2.1's downloads (design/3.2.1.md 1): the admin's switch, off as it
    // ships, and Victor's own block on downloads of his stream's clips,
    // which belongs to the stream as clipping's switch does.
    downloadsAllowed: boolean;
    streamerNoDownloads: boolean;

    // How Victor's stream is matched to him when it goes live: linked by
    // hand ('manual'), matched by his username while he has not linked it
    // ('username'), or not matched to anybody ('none'). The Streamer row.
    victorSource: 'manual' | 'username' | 'none';

    // How the recordings of the live streams are doing, all at once, and
    // since when the current one ran (for "warming" and "partial", whose
    // recorded time is counted from it). 'dropped' is a stream that came
    // back after a drop: a new recording just starting, and the end of the
    // old one held.
    recording: LabRecording;
    recordingFrom: number;

    // How the next capture and the next publish go: as the server would
    // answer, or refused with one of its codes.
    // 'forgotten' is the server after a restart, which has no record of
    // anybody watching until their tile tunes in again (mocks/whep.ts).
    captureMode: 'ok' | 'slow' | 'fails' | 'rate' | 'cap' | 'expired' | 'not_watching' | 'forgotten' | 'wiped';
    publishMode: 'ok' | 'slow' | 'fails' | 'expired' | 'full' | 'share_full' | 'duplicate' | 'network_then_ok';

    // Safari, which plays the pictures of a clip but not its Opus sound.
    noSound: boolean;

    // How many clips the System Console says a lowered "Keep clips for"
    // will remove when its day of grace ends.
    retentionPending: number;

    // How fetching the list, one clip, or a channel's shared-clip cards goes.
    // 'gone' is about one clip: the list is fetched as usual.
    clipFetch: 'ok' | 'slow' | 'fails' | 'network' | 'gone';

    // How a clip's file comes to the player (ClipTile.tsx), in the Clips
    // dialog and the System Console alike: at once, never ('hang', the
    // player's loading), or as something the browser cannot read ('fail',
    // its error with Try again).
    clipFile: 'ok' | 'hang' | 'fail';

    // How the next share goes. The rest of the Share row — no channel, a
    // shared channel, a direct or group message — is where the lab's
    // Mattermost has the reader, not how the server answers.
    shareMode: 'ok' | 'cannot_post' | 'share_cap' | 'rate' | 'gone' | 'gone_on_arrival' | 'network';

    // The rest of the document: upvotes kept from clips that expired or that
    // their streamer deleted, score resets, and who is blocked from
    // clipping, by user id.
    clipBanked: Record<string, {up: number; clips: number; lastUpAt: number}>;
    clipResets: Record<string, number>;
    clipBlocked: Record<string, {at: number; by: string}>;

    // ---- Twitch (design/twitch-live.md) ----
    // Show people live on Twitch and Play Twitch streams inside the panel, and
    // whether the Twitch application's Client ID and Secret are set. The
    // snapshot's twitchOn is the first two of these together, as the server's
    // twitchLiveOn is.
    twitchSwitch: boolean;
    twitchEmbed: boolean;
    twitchCredentials: boolean;

    // What the server says is wrong, for admins: the Site URL makes no
    // callback address, Twitch's player cannot run at the Site URL, and
    // Twitch is not answering.
    twitchCallbackProblem: string;
    twitchEmbedProblem: string;
    twitchProblem: string;

    // This page cannot run Twitch's player: the lab's stand-in for a
    // Mattermost on a custom port (mocks/twitchEmbed.tsx embedHost).
    twitchPlayerBlocked: boolean;

    // The link document, as the server keeps it: who has connected which
    // Twitch account, who is blocked, and Victor's takeover notice.
    twitchLinks: Record<string, LabTwitchLink>;
    twitchBlocked: Record<string, number>;
    twitchLost: {login: string; at: number} | null;

    // Who is live on Twitch now, as the server's last read had it, and
    // whether each one's Twitch entry is covered by their Broadcast Box
    // card, which changes only after the lab's grace (LAB_COVER_GRACE_MS).
    twitchLive: Record<string, LabTwitchLive>;
    twitchCovered: Record<string, boolean>;

    // The Twitch ledger, by person, and its state for the System Console.
    twitchSeconds: Record<string, number>;
    twitchHalted: string;
    twitchPurging: number;
    twitchQuarantined: boolean;
    twitchLastTry: number;

    // How Connect goes: the ticket, the window (opened, stopped by a pop-up
    // blocker, or the desktop app's own), and what happens in it.
    twitchTicket: 'ok' | 'refused';
    twitchPopup: 'opens' | 'blocked' | 'desktop';
    twitchWindow: 'connects' | 'fails' | 'never';

    // ---- 3.2.0 (design/3.2.0.md 3 to 7) ----

    // award_prefs: who chose what shows beside their name, and when; and
    // whether the document could be read.
    awardChoices: Record<string, {show: string[]; at: number}>;
    awardsReadable: boolean;

    // Biggest fans: EnableBiggestFans, and the hours of each streamer's top
    // viewer, as the last credit pass saved them. Nobody can opt out from
    // 3.2.1 (design/3.2.1.md 2).
    fansOn: boolean;
    fanHours: Array<{streamerId: string; fanId: string; hours: number}>;

    // Achievements: EnableAchievements, and each person's figure per family
    // (whole hours, months or thumbs), from which the steps are worked out as
    // the server works them out at every refresh.
    achievementsOn: boolean;
    achievementValues: Record<string, Record<string, number>>;

    // The admin-only lines on "me" for the fan and channel ledgers, the
    // soundboard and the 24/7 channel achievements setting.
    ledgerAlert: LedgerAlert;
    soundAlert: string;
    achievementsAlert: string;

    // The soundboard: EnableSoundboard (off by default on a server; on in the
    // lab, so it has something to show); the shared board, oldest first; its
    // version, which the snapshot carries; why the tiles are off when the lab
    // says so ('' follows the voice row: muted there is muted here); whether
    // the plugin's own Mumble account can speak; the channel Victor is in; the
    // server's two waits; whether the sounds document could be read; who is
    // blocked; and whether this "browser" can encode at a constant bitrate.
    soundboardOn: boolean;
    sounds: LabSound[];
    soundV: number;
    soundWhy: SoundWhy;
    soundBotOk: boolean;
    soundChannel: string;
    soundCooldownUntil: number;
    soundBusyUntil: number;
    soundsReadable: boolean;
    soundBlocked: Array<{userId: string; username: string; name: string; by: string; at: number}>;
    soundEncoder: 'constant' | 'none';

    // What the server says to the next sound added: yes, or one of its
    // refusals (design/3.2.0.md 7.14), for the add flow's error line.
    soundAddAnswer: '' | 'too_loud' | 'bad_sound' | 'bad_emoji' | 'full' | 'store';

    // ---- 3.4.0 (design/3.4.0.md): Steam and Hytale ----

    // Each switch, whether its secrets are set, and what is wrong with it:
    // the callback address (a Site URL the service will not take), a refused
    // key or client, Steam out of reach (its last good read too old, so no
    // statuses). Who is connected as what, who is blocked, and Victor's
    // takeover notice; for Steam, what its last read said about each
    // connected person; and the counts the System Console card shows.
    steamSwitch: boolean;
    steamKeySet: boolean;
    steamKeyProblem: string;
    steamCallbackProblem: string;
    steamProblem: string;
    steamLinks: Record<string, LabAccountLink>;
    steamStatus: Record<string, LabSteamStatus>;
    steamBlocked: Record<string, number>;
    steamLost: {name: string; at: number} | null;
    steamCallsToday: number;
    steamMissed: number;
    steamQuarantined: boolean;
    hytaleSwitch: boolean;
    hytaleCredentials: boolean;
    hytaleCallbackProblem: string;
    hytaleProblem: string;
    hytaleLinks: Record<string, LabAccountLink>;
    hytaleBlocked: Record<string, number>;
    hytaleLost: {name: string; at: number} | null;
    hytaleQuarantined: boolean;

    // How Connect goes for both: the ticket (or the server's refusal of it,
    // by code), the window (opened, stopped by a pop-up blocker, or the
    // desktop app's own), whether the service connects Victor or never
    // answers, and Disconnect failing as a store refusing the write would.
    accountTicket: 'ok' | 'rate_limited' | 'store' | 'interactive_only';
    accountPopup: 'opens' | 'blocked' | 'desktop';
    accountWindow: 'connects' | 'never';
    accountUnlinkFails: boolean;

    // Mattermost accounts no longer active, for the System Console cards
    // and the view, which leaves them out.
    inactive: Record<string, 'deactivated' | 'deleted'>;
};

// A Steam or Hytale account connected to somebody, as the link document
// holds it: the SteamID64 or the Hytale profile's uuid, the name, and when.
export type LabAccountLink = {id: string; name: string; at: number};

// What Steam's last read said about one person: public is whether the
// profile is (only a public one shows anything but offline), and game is
// there while Steam reports one ('' for a game it does not name).
export type LabSteamStatus = {state: 'online' | 'busy' | 'away' | 'offline'; game?: string; public: boolean};

export type LabSound = {id: string; ownerId: string; name: string; emoji: SoundEmoji; ms: number; at: number; ok?: boolean};

// A Twitch account connected to somebody, as the link document holds it.
export type LabTwitchLink = {login: string; name: string; at: number; allDay: boolean};

// A Twitch stream live now, as Get Streams described it.
export type LabTwitchLive = {title: string; category: string; viewers: number; startedAt: string};

export type LabRecording = 'ready' | 'warming' | 'partial' | 'truncated' | 'failed' | 'unsupported' | 'full' | 'queued' | 'off' | 'dropped';

// What POST /me/link answers next: success, each refusal the server words
// with a code, a 200 with no stream key, and no answer at all.
export type LabLinkAnswer = 'success' | 'not_accepted' | 'rate_limited' | 'unreachable' | 'bad_name' | 'not_configured'
    | 'too_long' | 'store' | 'bad_request' | 'empty_key' | 'no_answer';

// Victor's access, as the server would decide it.
export function labAccessOf(current: World): 'linked' | 'letIn' | 'locked' {
    if (current.me.streamKey !== '') {
        return 'linked';
    }

    return current.letIn ? 'letIn' : 'locked';
}

// The let-in list for the System Console's "four people": Caro let in by
// Victor, Erin deactivated, one account deleted, and Victor himself, which
// the card marks "(you)". The lab starts with the first three, Victor being
// linked and not on it.
export function sampleLetIn(): LetInPerson[] {
    const day = 24 * HOUR;
    const at = Date.now();

    return [
        {userId: 'u-carol', username: 'carol', name: 'Caro', at: at - (3 * day), byUsername: 'victor', state: 'active'},
        {userId: 'u-erin', username: 'erin', name: 'Erin Pemberton-Whitfield', at: at - (40 * day), byUsername: 'victor', state: 'deactivated'},
        {userId: 'u-gone-0000000000000000000', username: '', name: '', at: at - (90 * day), byUsername: 'victor', state: 'gone'},
        {userId: ME_ID, username: 'victor', name: 'Victor', at: at - day, byUsername: 'victor', state: 'active'},
    ];
}

// A held end as the server keeps it: what the snapshot says, and what the
// stream was tagged with when it stopped.
export type LabTail = ClipTail & {nowPlaying?: NowPlaying};

// A published clip as the server's document holds it (clips.go clipEntry),
// with the lab's own picture for it when one was uploaded. streamKey stands
// for the server's streamTag: which stream it came from, by key, so "of my
// stream" follows whoever is linked to that key now.
export type LabClip = {
    id: string;
    title: string;
    createdAt: number;
    startAt: number;
    durationMs: number;
    leadMs?: number;
    width: number;
    height: number;
    codecs: string;
    bytes: number;
    thumb?: string;
    thumbUrl?: string;
    clipperId: string;
    streamerId?: string;
    streamName: string;
    streamNote?: string;

    // What the stream was tagged with when it was cut (addendum 4.14).
    tag?: ClipTag;
    handle: string;
    streamKey: string;

    // Who voted, and who reported it, each with when.
    voters: Record<string, number>;
    reports: Record<string, number>;

    // The posts it was shared in, at most ten.
    shares: string[];
};

// A draft as the server holds it in memory: what its owner is shown, plus
// whose it is, which stream it was cut from, and whether the streamer has
// since wiped that stream.
export type LabDraft = ClipDraft & {
    owner: string;
    streamKey: string;
    wiped?: boolean;
    thumbUrl?: string;
};

export const ME_ID = 'u-victor';

const MINUTE = 60000;
const HOUR = 3600000;

let nextId = 1;

export function freshId(prefix: string): string {
    nextId += 1;

    return `${prefix}-${nextId}`;
}

// What each lab streamer says they are streaming. Dave's is long on purpose:
// it is the one that has to scroll under the picture. Between them they have
// every kind of hashtag: one in the middle of a sentence and one on the end,
// with "#gg" between, too short for Mattermost to count as one (Dave); one
// beside a link (Bob); and one on a title short enough to sit still (Victor).
const NOTES: Record<string, string> = {
    Dave: 'Arma 3 — ALiVE #milsim campaign night on Altis, bring a radio and a sense of humour #gg #arma3',
    Bob: 'Elden Ring, no summons #soulslike — build at https://example.com/builds/bob',
    Victor: 'Arma most nights #arma3',
};

// Who is watching whom, as the server would report it: Dave has a crowd, so
// the "+N" shows; Bob has one; the rest nobody with a face.
const WATCHERS: Record<string, Array<{userId: string; who: string}>> = {
    Dave: [
        {userId: 'u-bob', who: 'Bob'},
        {userId: 'u-erin', who: 'Erin'},
        {userId: ME_ID, who: 'Victor'},
        {userId: 'u-carol', who: 'Carol'},
    ],
    Bob: [{userId: 'u-dave', who: 'Dave'}],
};

function stream(name: string, userId: string, options: Partial<Stream> = {}): Stream {
    return {
        handle: freshId('h'),
        title: name,
        paused: false,
        alwaysOn: false,
        viewers: WATCHERS[name] ? WATCHERS[name].length + 1 : 2,
        bitrate: 4200000,
        videoTracks: 1,
        audioTracks: 1,
        streamStart: new Date(Date.now() - (37 * MINUTE)).toISOString(),
        userId,
        username: name.toLowerCase(),
        displayName: name,
        motd: NOTES[name],
        watchers: WATCHERS[name],

        // And one more nobody can name: watching some other way.
        others: WATCHERS[name] ? 1 : undefined,

        // Victor linked his stream by hand, as anybody who can manage what
        // clips are kept of it has — unless the Streamer row says otherwise.
        linkSource: userId === ME_ID ? victorLinkSource() : undefined,
        ...options,
    };
}

// What Victor's stream is matched to him by, as the stream row carries it.
function victorLinkSource(): string | undefined {
    const source = world.victorSource;

    return source === 'none' ? undefined : source;
}

// A plan's id, as the server makes them: 16 hex (server/community.go), which
// a shared plan's card holds its post to (design/3.2.0.md 6.5).
export function freshPlanId(): string {
    return Array.from({length: 16}, () => Math.floor(Math.random() * 16).toString(16)).join('');
}

function planned(title: string, who: string, userId: string, inMs: number, category = '', media?: string): ScheduleItem {
    return {id: freshPlanId(), title, who, userId, category, startsAt: Date.now() + inMs, media: media ? detailsOf(media) || undefined : undefined};
}

function board(rows: Array<[string, number]>): Leader[] {
    return rows.map(([title, seconds], index) => ({
        rank: index + 1,
        title,
        seconds,
        userId: `u-${title.toLowerCase()}`,
        username: title.toLowerCase(),
        displayName: title,
    }));
}

// "YYYY-MM" for the UTC month this many before the current one. Worked out
// from today rather than written down, so the lab's history is always months
// that have ended, whenever it is opened.
function monthsAgo(back: number): string {
    const now = new Date();
    const at = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() - back, 1));

    return `${at.getUTCFullYear()}-${String(at.getUTCMonth() + 1).padStart(2, '0')}`;
}

// The month board's heading the way the server writes it (monthLabel in
// stats.go, always English), for the UTC month in progress. Worked out from
// today for the same reason as monthsAgo: written down, it would sooner or
// later name a month the history above already counts as over, and the
// profile would show that month twice.
function thisMonthLabel(): string {
    return new Intl.DateTimeFormat('en-US', {month: 'long', year: 'numeric', timeZone: 'UTC'}).format(new Date());
}

// The monthly podiums already decided, last month first. Chosen so every state
// of the profile row can be looked at: Dave champion twice and runner-up once,
// so a count beside the title; Erin champion once, so none; Bob never first, so
// only the smaller places; Victor five times, so "+2 more".
const PAST_PODIUMS: string[][] = [
    ['Dave', 'Victor', 'Bob'],
    ['Dave', 'Bob', 'Erin'],
    ['Victor', 'Dave', 'Erin'],
    ['Erin', 'Victor', 'Bob'],
    ['Victor', 'Bob'],
    ['Victor', 'Erin', 'Bob'],
    ['Victor'],
    ['Victor', 'Bob'],
];

// monthMedals as the server assembles it from those: one entry per person,
// named as the boards name them, ordered by user id, each list oldest first.
function medalsOf(podiums: string[][]): MonthMedals[] {
    const places = ['gold', 'silver', 'bronze'] as const;
    const byUser = new Map<string, MonthMedals>();

    podiums.forEach((names, index) => {
        const month = monthsAgo(index + 1);

        names.forEach((name, place) => {
            const userId = `u-${name.toLowerCase()}`;
            const entry = byUser.get(userId) || {userId, username: name.toLowerCase(), displayName: name};
            const key = places[place];

            // Walking back from last month, so each one goes in front.
            entry[key] = [month].concat(entry[key] || []);
            byUser.set(userId, entry);
        });
    });

    return Array.from(byUser.values()).sort((a, b) => (a.userId < b.userId ? -1 : 1));
}

function sampleNowPlaying(): Record<string, NowPlaying> {
    const arma = detailsOf('steam:107410');

    return {
        'u-dave': {category: 'game', title: 'Arma 3', media: arma || undefined},
        'u-bob': {category: 'game', title: 'Elden Ring'},
    };
}

// What a stream's tag becomes on a clip (addendum 4.14), as the server keeps
// it: the kind and the words, and the real title's ref, kind, title, year,
// link and source when lookups found it — never its overview or facts, which
// a document of five hundred clips has no room for. undefined for no tag.
export function clipTagOf(nowPlaying?: NowPlaying): ClipTag | undefined {
    if (!nowPlaying || nowPlaying.title.trim() === '') {
        return undefined;
    }

    const media = nowPlaying.media;

    return {
        category: nowPlaying.category,
        title: nowPlaying.title,
        ...(media ? {media: {
            ref: media.ref,
            kind: media.kind,
            title: media.title,
            ...(media.year ? {year: media.year} : {}),
            ...(media.link ? {link: media.link} : {}),
            source: media.source || '',
        }} : {}),
    };
}

// Hytale, which Steam does not sell, as IGDB has it (igdb:113360): a tag with
// the real game behind it, whose line in the viewer opens its profile, with
// IGDB's link and attribution. The same said while lookups were off: words
// only, which is what a clip of it keeps then. Heat, a movie that lookups
// found; and Elden Ring, a game they found on Steam.
export function hytaleNowPlaying(): NowPlaying {
    return {category: 'game', title: 'Hytale', media: detailsOf('igdb:113360') || undefined};
}

export function hytaleWordsOnly(): NowPlaying {
    return {category: 'game', title: 'Hytale'};
}

export function eldenRingNowPlaying(): NowPlaying {
    return {category: 'game', title: 'Elden Ring', media: detailsOf('steam:1245620') || undefined};
}

export function heatNowPlaying(): NowPlaying {
    return {category: 'movie', title: 'Heat', media: detailsOf('tmdb:movie:949') || undefined};
}

// ---------------------------------------------------------------- clips

const DAY = 24 * HOUR;

// Who the sample clips and their upvotes are from. Plain ids, not LAB_USERS,
// which is declared further down and not there yet when the world is first
// made. The "fans" are linked people who never appear anywhere else, so a
// clip can have the three-figure counts a busy server would.
const CLIP_NAMES: Record<string, string> = {
    [ME_ID]: 'Victor',
    'u-dave': 'Dave',
    'u-bob': 'Bob',
    'u-erin': 'Erin',
    'u-carol': 'Carol',
};

const fans = (count: number, from = 1): string[] => Array.from({length: count}, (_, index) => `u-fan-${String(from + index).padStart(2, '0')}`);

// The sample clip is 256 × 144, about 416 KB for two minutes; each clip's
// size is its share of that, as a trimmed clip's would be.
const SAMPLE_BYTES = 415867;

function sampleClip(id: string, clipperId: string, streamerId: string, title: string, agoMs: number, durationMs: number,
    voters: string[], more: Partial<LabClip> = {}): LabClip {
    const createdAt = Date.now() - agoMs;
    const votedAt = createdAt + (10 * MINUTE);
    const streamName = CLIP_NAMES[streamerId] || 'Stream 3f9a0c';

    return {
        id,
        title,
        createdAt,
        startAt: createdAt - (6 * MINUTE),
        durationMs,
        width: 256,
        height: 144,
        codecs: 'avc1.64000c,opus',
        bytes: Math.round(SAMPLE_BYTES * durationMs / 120000),
        thumb: id.slice(0, 8),
        clipperId,
        streamerId,
        streamName,
        streamNote: NOTES[streamName],
        handle: `h-${streamName.toLowerCase()}`,
        streamKey: `live-${streamName.toLowerCase()}`,
        voters: Object.fromEntries(voters.map((voter, index) => [voter, votedAt + (index * MINUTE)])),
        reports: {},
        shares: [],
        ...more,
    };
}

// The six sample clips (spec 3.16): one by Victor, one of Victor's stream,
// one with two days left, one with an 80-character title, one with no
// picture (and no title, so the card names the streamer and the time), and
// one with a lead-in of 1.4 s and a report. Bob's two and the upvotes banked
// from seven clips of his that have expired make him the Best Clipper with
// 142 on 9 clips, the spec's own example; Victor has voted for his best one.
//
// And what each stream was tagged with when it was cut (addendum 4.14): Dave
// was on Hytale for "that jump", which IGDB found, so the viewer's line opens
// its profile with IGDB's link; on Hytale again for Victor's clip of him, but
// said while lookups were off, so words only; Victor's film night was Heat, a
// movie, and Bob was on Elden Ring, which lookups found on Steam, so their
// lines open their profiles too; the rest had no tag.
function sampleClips(): LabClip[] {
    return [
        sampleClip('3f9a0c2e7b1d4a55', 'u-bob', 'u-dave', 'that jump', 3 * DAY, 45000,
            [ME_ID, 'u-dave', 'u-erin', 'u-carol', ...fans(27)],
            {leadMs: 1400, reports: {'u-erin': Date.now() - (2 * DAY)}, tag: clipTagOf(hytaleNowPlaying())}),
        sampleClip('a1b2c3d4e5f60718', ME_ID, 'u-dave', 'that landing, absolutely cursed', 5 * HOUR, 120000,
            ['u-dave', 'u-bob', 'u-erin', ...fans(3, 40)], {tag: clipTagOf(hytaleWordsOnly())}),
        sampleClip('5d6e7f8091a2b3c4', 'u-bob', ME_ID, 'Victor reads the briefing', DAY, 87300,
            ['u-dave', 'u-erin', ...fans(9, 50)], {tag: clipTagOf(heatNowPlaying())}),
        sampleClip('0f1e2d3c4b5a6978', 'u-erin', 'u-bob', 'no summons, no problem', 28 * DAY, 30000,
            ['u-bob', 'u-carol', ...fans(2, 70)], {tag: clipTagOf(eldenRingNowPlaying())}),
        sampleClip('9a8b7c6d5e4f3a2b', 'u-dave', 'u-erin',
            'Sunday racing: the whole field goes three wide into turn one and nobody spun out', 6 * DAY, 118000,
            ['u-erin', 'u-carol', ...fans(5, 80)]),
        sampleClip('b0c1d2e3f4a5b6c7', 'u-carol', 'u-dave', '', 40 * MINUTE, 62500,
            ['u-dave'], {thumb: undefined}),
    ];
}

// Upvotes kept from clips that have gone: Bob's seven expired clips, and
// three of Dave's.
function startingBanked(): World['clipBanked'] {
    return {
        'u-bob': {up: 100, clips: 7, lastUpAt: Date.now() - (10 * DAY)},
        'u-dave': {up: 23, clips: 3, lastUpAt: Date.now() - (20 * DAY)},
    };
}

function startingClips(): Pick<World, 'clips' | 'drafts' | 'clipsEnabled' | 'clipLength' | 'retentionDays' | 'bestClipper' |
    'clipsSeen' | 'tails' | 'restartedAt' | 'streamerAllowed' | 'downloadsAllowed' | 'streamerNoDownloads' | 'victorSource' | 'recording' | 'recordingFrom' | 'captureMode' |
    'publishMode' | 'noSound' | 'retentionPending' | 'clipFetch' | 'clipFile' | 'shareMode' | 'clipBanked' | 'clipResets' | 'clipBlocked'> {
    return {
        clips: sampleClips(),
        drafts: [],
        clipsEnabled: true,
        clipLength: 120,
        retentionDays: 30,

        // Bob has held it for twelve days; recomputeBestClipper keeps that
        // date for as long as he goes on holding it.
        bestClipper: {userId: 'u-bob', username: 'bob', displayName: 'Bob', upvotes: 0, clips: 0, since: Date.now() - (12 * DAY)},
        clipsSeen: 0,
        tails: [],
        restartedAt: {},
        streamerAllowed: true,
        downloadsAllowed: false,
        streamerNoDownloads: false,
        victorSource: 'manual',
        recording: 'ready',
        recordingFrom: 0,
        captureMode: 'ok',
        publishMode: 'ok',
        noSound: false,
        retentionPending: 0,
        clipFetch: 'ok',
        clipFile: 'ok',
        shareMode: 'ok',
        clipBanked: startingBanked(),
        clipResets: {},
        clipBlocked: {},
    };
}

function starting(): World {
    return {
        me: {
            userId: ME_ID,
            username: 'victor',
            streamKey: 'live-victor',
            badgeEnabled: true,
            autoLinkByUsername: false,
            linkedLive: false,
            styleCodeBlocks: false,
            openPanel: 'never',

            // What the server calls the plugin and which artwork it has, as
            // a fresh server says them: Matty, and no emblem and no ghost
            // (the Server name and Artwork rows switch them).
            name: 'Matty',
            artwork: {emblem: false, ghost: false},
            release: {state: 'unsigned', version: '4.5.0'},
            app: {check: 'report', state: 'none'},

            // Often, in the lab, so a ghost is seen without waiting for one
            // (on a server that has one).
            noSignalGhost: 'often',
            media: {movies: true, games: true, igdb: true},

            // No banner, as on a fresh server; with no colour saved a banner
            // shows its fallback, the theme's accent (the Banner row).
            bannerText: '',
            bannerColor: '',
            bannerPlacement: 'both',
            configured: true,
            isAdmin: true,
        },
        profile: {available: true, motd: 'Arma most nights #arma3', isPublic: true},
        streams: [],
        schedule: [
            {...planned('Arma co-op', 'Dave', 'u-dave', 25 * MINUTE, 'game', 'steam:107410'), joined: [{userId: 'u-bob', who: 'Bob'}, {userId: 'u-erin', who: 'Erin'}]},
            planned('Film night: Heat', 'Victor', ME_ID, 5 * HOUR, 'movie', 'tmdb:movie:949'),
            planned('Elden Ring, no summons', 'Bob', 'u-bob', 27 * HOUR, 'game', 'steam:1245620'),
            planned('Sunday racing', 'Erin', 'u-erin', 4 * 24 * HOUR),
            planned('Hytale first look', 'Erin', 'u-erin', 2 * HOUR, 'game', 'igdb:113360'),
        ],
        // Victor 262 hours, Dave 131, Bob 58, Erin 12, so the Streamed
        // achievements below agree with the board.
        allTime: board([['Victor', 943200], ['Dave', 471600], ['Bob', 208800], ['Erin', 43200]]),
        month: board([['Dave', 41000], ['Victor', 30000], ['Bob', 9000]]),
        monthLabel: thisMonthLabel(),
        allTimeTotal: 4,
        monthTotal: 3,
        monthMedals: medalsOf(PAST_PODIUMS),

        // Not the streamers' order: Erin watches the most and streams the
        // least. Victor is on both, so the me-row shows on each. Erin's first
        // place all time makes her the Top Snooper, so the binoculars are
        // hers. Dave has watched nobody this month, but 52 hours in all, as
        // his Snooped achievement and his 12 hours as Bob's biggest fan say
        // (design/3.2.0.md 4.9, 5.8).
        watchBoards: true,
        watchMonth: board([['Erin', 52000], ['Victor', 38000], ['Carol', 12000], ['Bob', 4100]]),
        watchAllTime: board([['Erin', 412000], ['Bob', 260000], ['Victor', 198000], ['Dave', 187200], ['Carol', 61000]]),
        watchMonthTotal: 4,
        watchAllTimeTotal: 5,
        configured: true,
        error: '',
        latencyMs: 180,
        failWrites: false,
        voiceConnected: true,
        voice: [
            {session: 2, name: 'Victor', userId: ME_ID},
            {session: 3, name: 'Dave', userId: 'u-dave'},
            {session: 4, name: 'Bob', userId: 'u-bob', channel: 1, muted: true, deaf: true},
        ],
        voiceChannels: LAB_VOICE_CHANNELS,
        livePreviews: true,
        nowPlaying: sampleNowPlaying(),
        tagSearch: 'messages',
        mainWindowGone: false,
        linkSwitches: 0,
        meWithheld: false,
        letIn: false,
        meDelayMs: 0,
        storeBlips: 0,
        linkAnswer: 'success',
        publicWatch: false,
        everyAlert: false,
        letInPeople: sampleLetIn().filter((person) => person.userId !== ME_ID),
        letInProblem: '',
        letInReadOnly: false,
        letInAddError: '',
        ...startingClips(),
        mediaCache: rememberedMediaCache(),
        mediaCacheForgetFails: false,
        igdbSlow: false,
        ...startingTwitch(),
        ...starting320(),
        ...starting340(),
    };
}

// The catalogue as the server sends it: the four built-in families and the
// Lo-fi radio channel (a ChannelAchievements line, "radio = Lo-fi radio = 1,
// 10, 50, 100, 250"), whose id is a hash of its key.
export const LAB_ACHIEVEMENT_KINDS: AchievementKind[] = ([
    {id: 'streamed', name: 'Streamed', unit: 'hours', tiers: [10, 50, 100, 250, 500, 1000]},
    {id: 'snooped', name: 'Snooped', unit: 'hours', tiers: [10, 50, 100, 250, 500]},
    {id: 'podium', name: 'Podium', unit: 'months', tiers: [1, 3, 6, 12]},
    {id: 'thumbs', name: 'Thumbs up', unit: 'thumbs', tiers: [10, 50, 100, 250]},
    {id: 'ch-1a2b3c4d5e6f', name: 'Lo-fi radio', unit: 'hours', tiers: [1, 10, 50, 100, 250]},
] as Array<Omit<AchievementKind, 'names'>>).map(namedKind);

// How many steps of a family a figure reaches.
export function tierOf(kind: AchievementKind, value: number): number {
    return kind.tiers.filter((step) => value >= step).length;
}

// The biggest fans as the lab starts (design/3.2.0.md 4.9): Bob is Dave's
// (41 h) and Erin is Victor's; Dave is Bob's, so he has a fan title to choose
// (3.9); and Victor is Erin's, so his own profile has both sides and the
// takeback. Each figure fits inside its viewer's Snoopers total.
export function startingFanHours(): World['fanHours'] {
    return [
        {streamerId: 'u-dave', fanId: 'u-bob', hours: 41},
        {streamerId: ME_ID, fanId: 'u-erin', hours: 23},
        {streamerId: 'u-bob', fanId: 'u-dave', hours: 12},
        {streamerId: 'u-erin', fanId: ME_ID, hours: 9},
    ];
}

// The achievements' figures as the lab starts, agreeing with the rest of the
// world: streamed hours with the all-time board, snooped hours with the
// Snoopers' all-time board, podium months with the past podiums, thumbs with
// the clips' upvotes. Victor: Streamed 250 and Podium 6 months; Dave: Snooped
// 50 and Lo-fi radio 10 (design/3.2.0.md 5.8). The Lo-fi radio hours started
// at zero on the day 3.2.0 went in.
export function startingAchievementValues(): World['achievementValues'] {
    return {
        [ME_ID]: {streamed: 262, snooped: 55, podium: 7, 'ch-1a2b3c4d5e6f': 1},
        'u-dave': {streamed: 131, snooped: 52, podium: 3, thumbs: 23, 'ch-1a2b3c4d5e6f': 12},
        'u-bob': {streamed: 58, snooped: 72, podium: 6, thumbs: 112},
        'u-erin': {streamed: 12, snooped: 114, podium: 4, 'ch-1a2b3c4d5e6f': 131},
    };
}

// 3.2.0 as the lab starts: nobody has chosen (so every badge is 3.1.0's),
// biggest fans and achievements on, the soundboard on with a board of nine.
function starting320(): Pick<World, 'awardChoices' | 'awardsReadable' | 'fansOn' | 'fanHours' |
    'achievementsOn' | 'achievementValues' | 'ledgerAlert' | 'soundAlert' | 'achievementsAlert' | 'soundboardOn' | 'sounds' |
    'soundV' | 'soundWhy' | 'soundBotOk' | 'soundChannel' | 'soundCooldownUntil' | 'soundBusyUntil' | 'soundsReadable' |
    'soundBlocked' | 'soundEncoder' | 'soundAddAnswer'> {
    return {
        awardChoices: {},
        awardsReadable: true,
        fansOn: true,
        fanHours: startingFanHours(),
        achievementsOn: true,
        achievementValues: startingAchievementValues(),
        ledgerAlert: {},
        soundAlert: '',
        achievementsAlert: '',
        soundboardOn: true,
        sounds: sampleSounds(),
        soundV: 1,
        soundWhy: '',
        soundBotOk: true,
        soundChannel: 'Lounge',
        soundCooldownUntil: 0,
        soundBusyUntil: 0,
        soundsReadable: true,
        soundBlocked: [{userId: 'u-carol', username: 'carol', name: 'Caro', by: 'Victor', at: Date.now() - (6 * 24 * HOUR)}],
        soundEncoder: 'constant',
        soundAddAnswer: '',
    };
}

// The board as it might be a few weeks in: nine sounds from the four of
// them, oldest first, with three of the server's own emoji among them, and
// one name of the full sixteen characters, the longest a name may be.
export function sampleSounds(): LabSound[] {
    const made: Array<[string, string, string, number]> = [
        ['u-dave', 'Airhorn', 'loudspeaker', 2400],
        [ME_ID, 'Ba dum tss', 'drum_with_drumsticks', 1900],
        ['u-bob', 'Emotional damage', 'sob', 2700],
        ['u-erin', 'Goat', 'goat', 1600],
        ['u-dave', 'Sad trombone', 'trumpet', 3800],
        [ME_ID, 'Incoming!', 'rotating_light', 2100],
        ['u-bob', 'GG', 'gg', 1200],
        ['u-erin', 'Wake up', 'wakeup', 5200],
        [ME_ID, 'Fanfare', 'fanfare', 3000],
    ];
    const start = Date.now() - (20 * 24 * HOUR);

    return made.map(([ownerId, name, emoji, ms], index) => ({
        id: `5d0${index}a1b2c3d4e5f6`.slice(0, 16),
        ownerId,
        name,
        emoji: labEmojiNamed(emoji) || {kind: 'system', name: 'loud_sound', unified: '1f50a'},
        ms,
        at: start + (index * 2 * 24 * HOUR),
    }));
}

function nameOfUser(userId: string): string {
    const user = LAB_USERS.find((candidate) => candidate.id === userId);

    return user?.first_name || user?.nickname || user?.username || userId;
}

// What the server gathers about fans and achievements (3.2.0): each
// streamer's top fan, leaving out anybody not linked now, only while fans are
// on with Top Snoopers; the catalogue; and everybody's steps, linked people
// only.
function gathered320(current: World): {fans: FanView[]; fansOn: boolean; kinds: AchievementKind[]; achievements: AchievementHolder[]} {
    const linked = (userId: string) => userId !== ME_ID || labAccessOf(current) === 'linked';
    const byUser = (a: {userId: string}, b: {userId: string}) => (a.userId < b.userId ? -1 : 1);

    const fansOn = current.fansOn && current.watchBoards;
    const fans: FanView[] = fansOn ? current.fanHours.
        filter((row) => row.hours >= 1 && linked(row.streamerId) && linked(row.fanId)).
        map((row) => ({
            streamerId: row.streamerId,
            streamer: nameOfUser(row.streamerId),
            fanId: row.fanId,
            fan: nameOfUser(row.fanId),
            fanUsername: row.fanId.replace(/^u-/, ''),
            hours: row.hours,
        })).
        sort((a, b) => (a.streamerId < b.streamerId ? -1 : 1)) : [];

    const kinds = current.achievementsOn ? LAB_ACHIEVEMENT_KINDS : [];
    const achievements: AchievementHolder[] = Object.entries(current.achievementValues).
        filter(([userId]) => linked(userId)).
        map(([userId, values]) => {
            const user = LAB_USERS.find((candidate) => candidate.id === userId);

            return {
                userId,
                displayName: user ? [user.first_name, user.last_name].filter(Boolean).join(' ') || user.username : undefined,
                username: user?.username,
                have: kinds.
                    map((kind) => ({id: kind.id, tier: tierOf(kind, values[kind.id] || 0)})).
                    filter((entry) => entry.tier > 0),
            };
        }).
        filter((holder) => holder.have.length > 0).
        sort(byUser);

    return {fans, fansOn, kinds, achievements};
}

const SOUND_OWNERS: Record<string, string> = {[ME_ID]: 'Victor', 'u-dave': 'Dave', 'u-bob': 'Bob', 'u-erin': 'Erin'};

// GET /api/v1/sounds as the server answers Victor: his voice state read from
// the roster (the lab's stand-in for Mumble's live state), the waits, and the
// list only while he is in Mumble.
export function soundBoardOf(current: World, now: number): SoundBoard {
    const mine = current.voice.filter((person) => person.userId === ME_ID);
    const inVoice = current.voiceConnected && mine.length > 0;
    let why: SoundWhy = current.soundWhy;
    if (!current.voiceConnected) {
        why = 'bot_offline';
    } else if (!inVoice) {
        why = 'not_in_voice';
    } else if (why === '' && mine.length > 1) {
        why = 'two_sessions';
    } else if (why === '' && mine.every((person) => person.muted)) {
        why = 'muted';
    } else if (why === '' && !current.soundBotOk) {
        why = 'bot_cannot';
    }
    const sounds: Sound[] = current.sounds.map((sound) => ({
        id: sound.id,
        name: sound.name,
        emoji: sound.emoji,
        ms: sound.ms,
        owner: SOUND_OWNERS[sound.ownerId] || 'Somebody',
        mine: sound.ownerId === ME_ID,
    }));
    const yours = current.sounds.filter((sound) => sound.ownerId === ME_ID).length;
    const listed = inVoice && why !== 'bot_offline';

    return {
        voice: {in: inVoice, channel: inVoice ? current.soundChannel : undefined, can: why === '', why},
        bot: {ok: current.soundBotOk && why !== 'bot_offline'},
        cooldownUntil: current.soundCooldownUntil > now ? current.soundCooldownUntil : 0,
        busyUntil: current.soundBusyUntil > now ? current.soundBusyUntil : 0,
        v: String(current.soundV),

        // As the server says it: the board still answers a blocked reader,
        // and only playing and adding are refused.
        ...(current.soundBlocked.some((person) => person.userId === ME_ID) ? {blocked: true} : {}),
        ...(listed ? {
            sounds,
            limits: {maxMs: 6000, perPerson: 16, total: 120, name: 16, left: 16 - yours, cooldownMs: 10000},
        } : {}),
    };
}

// GET /api/v1/admin/sounds, as the server answers an admin.
export function adminSoundsOf(current: World): AdminSounds {
    const people = new Set(current.sounds.map((sound) => sound.ownerId));
    const bytesOf = (sound: LabSound) => Math.round(((sound.ms / 20) + 1) * 102) + 2;

    return {
        on: current.soundboardOn,
        readable: current.soundsReadable,
        bot: current.soundBotOk ? {ok: true, line: 'Matty can play sounds into Mumble.'} : {ok: false, line: 'The soundboard can’t play: Matty’s Mumble account is muted on the Mumble server.'},
        totals: {
            sounds: current.soundsReadable ? current.sounds.length : 0,
            bytes: current.soundsReadable ? current.sounds.reduce((sum, sound) => sum + bytesOf(sound), 0) : 0,
            people: current.soundsReadable ? people.size : 0,
        },
        sounds: current.soundsReadable ? current.sounds.map((sound) => ({
            id: sound.id,
            name: sound.name,
            emoji: sound.emoji,
            ownerId: sound.ownerId,
            owner: nameOfUser(sound.ownerId),
            ms: (Math.round(sound.ms / 20) + 1) * 20,
            bytes: bytesOf(sound),
            level: -21.3 + ((sound.name.length % 5) * 0.3),
            at: sound.at,
            ok: sound.ok !== false,
        })) : [],
        blocked: current.soundBlocked,
    };
}

// ---- 3.2.0: @names in titles (design/3.2.0.md 8.7) ----

// A title with two @names and a hashtag; one with a name nobody on the server
// has; titles that end in a name; and three that make no mention at all.
export const NAMES_TITLE = 'Arma night with @dave and @bob #arma3';
export const UNKNOWN_TITLE = 'Elden Ring with @rupert, no summons #soulslike';
export const TRAILING_TITLES: Record<string, string> = {
    Erin: 'Racing tonight with @dave',
    Bob: 'Co-op night with @rupert',
};
export const NO_NAME_TITLES: Record<string, string> = {
    Dave: '@all welcome',
    Bob: 'mail me@example.com',
    Erin: 'https://twitch.tv/@dave',
};

// A Twitch title with an @handle in it, which makes no mention: a Twitch
// handle is not anybody's Mattermost name.
export const TWITCH_HANDLE_TITLE = 'Sunday racing with @dave_races #simracing';

// Exactly these people on the air, in this order, with any titles given; and
// Victor's own stream title, which is what the footer shows.
export function liveWith(names: string[], titles: Record<string, string> = {}, footer?: string): void {
    change((current) => {
        current.streams = names.map((name) => {
            const userId = name === 'Victor' ? ME_ID : `u-${name.toLowerCase()}`;

            return stream(name, userId, titles[name] ? {motd: titles[name]} : {});
        });
        if (footer !== undefined) {
            current.profile = {...current.profile, motd: footer};
        }
    });
}

// ---- 3.2.0: shared plans (design/3.2.0.md 6) ----

// A shared plan's post, a copy of what the server's postSharedPlan makes
// (6.3): fixed words and a link, never the title, the time or a name; and
// props with only the plan's id, its start and the blanked one-time pass.
export function labPlanShareMessage(id: string): string {
    return `📅 ${world.me.name} plan\nSee it: ${LAB_SITE_URL}/plug/${PLUGIN_ID}/plan/${id}`;
}

export function labPlanShareProps(id: string, startsAt: number): Record<string, unknown> {
    return {villanet_plan_id: id, villanet_plan_starts_at: startsAt, villanet_share_nonce: ''};
}

// The plans the lab's plan cards are about, with fixed ids so their posts
// can be written down:
//   upcoming   Dave's Arma co-op at the next half hour at least 20 minutes
//              off, Bob and Erin in (Join);
//   live       Erin's Hytale first look, started at the quarter hour before
//              last, Erin on the air (Watch);
//   due        Bob's Valheim build night, started then too, Bob not on yet
//              (Due now), no art of its own;
//   in         Dave's Arma Reforger night ops in two days, Victor in (In ✓);
//   full       Caro's Star Citizen fleet night in three days, twelve in (Full);
//   mine       Victor's Film night: Heat, tomorrow at 21:00 (Your plan);
//   cancelled  taken off the list two days before it was due;
//   ended      five hours ago, and off the list since.
export const CARD_PLANS = {
    upcoming: '7c41d09e5ab2f316',
    live: 'e5a90f3c21b7d846',
    due: '2a7d5c1e9b3f8064',
    in: 'c8e1f4a7b2d50369',
    full: '5f2b8d0c6e1a7394',
    mine: 'b6c2e8a4f0d13579',
    cancelled: '3d8b62f0c9e14a57',
    ended: '9f0e1d2c3b4a5968',
};

const CARD_TITLES = ['Arma co-op', 'Hytale first look', 'Film night: Heat', 'Valheim build night', 'Arma Reforger: night ops', 'Star Citizen: fleet night'];

// The twelve in Caro's fleet night: three of the lab's people and nine more.
const FLEET = [
    {userId: 'u-bob', who: 'Bob'}, {userId: 'u-dave', who: 'Dave'}, {userId: 'u-erin', who: 'Erin'},
    ...['Niaj', 'Judy', 'Mike', 'Wendy', 'Olivia', 'Pat', 'Heidi', 'Trudy', 'Chad'].map((who) => ({userId: `u-fleet-${who.toLowerCase()}`, who})),
];

const roundUp = (at: number, step: number): number => Math.ceil(at / step) * step;
const roundDown = (at: number, step: number): number => Math.floor(at / step) * step;

// A day and a time on the browser's own calendar, days from today.
function dayAt(days: number, hour: number, minute: number): number {
    const day = new Date();
    day.setDate(day.getDate() + days);
    day.setHours(hour, minute, 0, 0);

    return day.getTime();
}

function cardStarts(): Record<keyof typeof CARD_PLANS, number> {
    const now = Date.now();
    const begun = roundDown(now - (5 * MINUTE), 15 * MINUTE);

    return {
        upcoming: roundUp(now + (20 * MINUTE), 30 * MINUTE),
        live: begun,
        due: begun,
        in: dayAt(2, 20, 30),
        full: dayAt(3, 20, 0),
        mine: dayAt(1, 21, 0),
        cancelled: dayAt(2, 20, 0),
        ended: roundDown(now - (5 * HOUR), 30 * MINUTE),
    };
}

function cardPlan(key: keyof typeof CARD_PLANS, title: string, who: string, userId: string, category: string, media?: string): ScheduleItem {
    return {...planned(title, who, userId, 0, category, media), id: CARD_PLANS[key], startsAt: cardStarts()[key]};
}

// The plans of the cards' every state on the list, and who is on the air for
// them: Erin for the live one, and not Bob, for the one that is due.
export function cardPlans(): void {
    change((current) => {
        const keep = current.schedule.filter((item) => !Object.values(CARD_PLANS).includes(item.id) && !CARD_TITLES.includes(item.title));
        current.schedule = keep.concat(
            {...cardPlan('upcoming', 'Arma co-op', 'Dave', 'u-dave', 'game', 'steam:107410'),
                joined: [{userId: 'u-bob', who: 'Bob'}, {userId: 'u-erin', who: 'Erin'}]},
            {...cardPlan('live', 'Hytale first look', 'Erin', 'u-erin', 'game', 'igdb:113360'),
                joined: [{userId: 'u-dave', who: 'Dave'}]},
            {...cardPlan('due', 'Valheim build night', 'Bob', 'u-bob', 'game'),
                joined: [{userId: 'u-dave', who: 'Dave'}]},
            {...cardPlan('in', 'Arma Reforger: night ops', 'Dave', 'u-dave', 'game', 'steam:1874880'),
                joined: [{userId: ME_ID, who: 'Victor'}, {userId: 'u-bob', who: 'Bob'}]},
            {...cardPlan('full', 'Star Citizen: fleet night', 'Caro', 'u-carol', 'game', 'igdb:900001'), joined: FLEET, full: true},
            cardPlan('mine', 'Film night: Heat', 'Victor', ME_ID, 'movie', 'tmdb:movie:949'),
        );
        if (!current.streams.some((item) => item.userId === 'u-erin' && !item.source)) {
            current.streams = current.streams.concat(stream('Erin', 'u-erin'));
        }
        current.streams = current.streams.filter((item) => item.userId !== 'u-bob' || Boolean(item.source));
    });
}

// When each card's plan starts, as its post's props say.
export function cardStartOf(key: keyof typeof CARD_PLANS): number {
    const listed = world.schedule.find((item) => item.id === CARD_PLANS[key]);

    return listed ? listed.startsAt : cardStarts()[key];
}

// Twitch as the lab starts: switched off, so nothing new shows anywhere,
// with Erin and Caro already connected and their hours kept (off the boards
// while the switch is off), and a status hook blocked from connecting.
function startingTwitch(): Pick<World, 'twitchSwitch' | 'twitchEmbed' | 'twitchCredentials' | 'twitchCallbackProblem' |
    'twitchEmbedProblem' | 'twitchProblem' | 'twitchPlayerBlocked' | 'twitchLinks' | 'twitchBlocked' | 'twitchLost' |
    'twitchLive' | 'twitchCovered' | 'twitchSeconds' | 'twitchHalted' | 'twitchPurging' | 'twitchQuarantined' |
    'twitchLastTry' | 'twitchTicket' | 'twitchPopup' | 'twitchWindow'> {
    const day = 24 * HOUR;

    return {
        twitchSwitch: false,
        twitchEmbed: false,
        twitchCredentials: true,
        twitchCallbackProblem: '',
        twitchEmbedProblem: '',
        twitchProblem: '',
        twitchPlayerBlocked: false,
        twitchLinks: {
            'u-erin': {login: 'erin_streams', name: 'Erin_Streams', at: Date.now() - (12 * day), allDay: false},
            'u-carol': {login: 'carol_tv', name: 'CarolTV', at: Date.now() - (3 * day), allDay: false},
        },
        twitchBlocked: {'u-hooks': Date.now() - (20 * day)},
        twitchLost: null,
        twitchLive: {},
        twitchCovered: {},
        twitchSeconds: {'u-erin': 41400, 'u-carol': 5400},
        twitchHalted: '',
        twitchPurging: 0,
        twitchQuarantined: false,
        twitchLastTry: 0,
        twitchTicket: 'ok',
        twitchPopup: 'opens',
        twitchWindow: 'connects',
    };
}

// Steam and Hytale as the lab starts: both switched off, as on a server that
// has just been upgraded, so nothing new shows anywhere; Dave, Bob and Erin
// already connected to Steam and Dave and Bob to Hytale, so the switches
// have something to show when they go on; Victor connected to neither, and
// the status hook blocked from Steam.
function starting340(): Pick<World, 'steamSwitch' | 'steamKeySet' | 'steamKeyProblem' | 'steamCallbackProblem' | 'steamProblem' |
    'steamLinks' | 'steamStatus' | 'steamBlocked' | 'steamLost' | 'steamCallsToday' | 'steamMissed' | 'steamQuarantined' |
    'hytaleSwitch' | 'hytaleCredentials' | 'hytaleCallbackProblem' | 'hytaleProblem' | 'hytaleLinks' | 'hytaleBlocked' |
    'hytaleLost' | 'hytaleQuarantined' | 'accountTicket' | 'accountPopup' | 'accountWindow' | 'accountUnlinkFails' | 'inactive'> {
    const day = 24 * HOUR;

    return {
        steamSwitch: false,
        steamKeySet: true,
        steamKeyProblem: '',
        steamCallbackProblem: '',
        steamProblem: '',
        steamLinks: {
            'u-dave': {id: '76561197960287930', name: 'dave_racer', at: Date.now() - (9 * day)},
            'u-bob': {id: '76561198044412207', name: 'tarnished', at: Date.now() - (6 * day)},
            'u-erin': {id: '76561198090881133', name: 'erin.pw', at: Date.now() - (2 * day)},
        },
        steamStatus: {
            'u-dave': {state: 'online', game: 'ELDEN RING', public: true},
            'u-bob': {state: 'online', game: 'Warhammer 40,000: Space Marine 2', public: true},
            'u-erin': {state: 'away', public: true},
        },
        steamBlocked: {'u-hooks': Date.now() - (20 * day)},
        steamLost: null,
        steamCallsToday: 1240,
        steamMissed: 0,
        steamQuarantined: false,
        hytaleSwitch: false,
        hytaleCredentials: true,
        hytaleCallbackProblem: '',
        hytaleProblem: '',
        hytaleLinks: {
            'u-dave': {id: '6f1c2a90-4d1e-4b8f-9a63-1d2e3f4a5b6c', name: 'DaveBuilds', at: Date.now() - (8 * day)},
            'u-bob': {id: '0b9e8d7c-6a5f-4e3d-8c2b-1a0f9e8d7c6b', name: 'Steve_Hytale', at: Date.now() - (4 * day)},
        },
        hytaleBlocked: {},
        hytaleLost: null,
        hytaleQuarantined: false,
        accountTicket: 'ok',
        accountPopup: 'opens',
        accountWindow: 'connects',
        accountUnlinkFails: false,
        inactive: {},
    };
}

// Remembered titles (media lookups): the counts the System Console card shows,
// shaped like the server's answer. The example from the design, with the
// oldest entry a few weeks back; and the same after Forget, when every one of
// the sixteen documents is left empty rather than deleted, so a few hundred
// bytes are still stored.
export function rememberedMediaCache(): MediaCacheStatus {
    return {
        titles: 12,
        searches: 40,
        missing: 2,
        bytes: 81234,
        limitBytes: 2097152,
        byProvider: {tmdb: 20, steam: 25, igdb: 9},
        oldest: Date.now() - (29 * 24 * HOUR),
        ...picturesOnDisk(37, 3250000),
    };
}

// The disk tier's counts, as the server sends them where file storage is
// local: 256 MB and 4,096 pictures at most.
function picturesOnDisk(pictures: number, bytes: number): Partial<MediaCacheStatus> {
    return {picturesKept: true, pictures, pictureBytes: bytes, pictureLimitBytes: 268435456, pictureLimit: 4096};
}

// Where Mattermost's file storage is not local: pictures in memory only, as
// before the disk tier, and no counts for them.
export function memoryOnlyMediaCache(): MediaCacheStatus {
    const {pictures, pictureBytes, pictureLimitBytes, pictureLimit, ...rest} = rememberedMediaCache();

    return {...rest, picturesKept: false};
}

export function forgottenMediaCache(): MediaCacheStatus {
    return {titles: 0, searches: 0, missing: 0, bytes: 320, limitBytes: 2097152, byProvider: {}, oldest: 0, ...picturesOnDisk(0, 0)};
}

// A server that has never kept anything: no documents at all.
export function unusedMediaCache(): MediaCacheStatus {
    return {...forgottenMediaCache(), bytes: 0};
}


// The world as it starts, with its Best Clipper worked out from its clips.
function freshWorld(): World {
    const fresh = starting();
    fresh.bestClipper = bestClipperOf(fresh, Date.now());

    return fresh;
}

let world = freshWorld();

const listeners = new Set<() => void>();

// ---- What system admins are newly told: the release, the app check ----
// The Admin news row's states (lab/index.tsx): what the lab's server says in
// "me" about its own files, what GET /release answers, and what GET
// /admin/appcheck holds. The panel's admin alerts draw the first; the System
// Console's App check card the last two. The sentences are the server's
// (server/release_check.go, server/appcheck), for a server at
// https://chat.example.com.
export type LabRelease = 'unsigned' | 'signed' | 'unlisted' | 'altered' | 'unreadable';
export type LabAppCheck = 'some' | 'many' | 'empty' | 'off' | 'problem' | 'latest' | 'unknown' | 'require' | 'error' | 'refused';

// Push notifications (server/push_api.go, server/push.go): what GET
// /admin/push holds, for the Push notifications card, and whether Mattermost
// decides to push, which the card says and which, while the plugin's switch
// is on, is the panel's push alert for admins.
export type LabPush = 'some' | 'empty' | 'off' | 'unready' | 'full' | 'problem' | 'error' | 'refused';
export type LabMattermostPush = 'on' | 'off' | 'licence' | 'unknown';

export const labAdminNews = {
    release: 'unsigned' as LabRelease,
    appCheck: 'some' as LabAppCheck,
    push: 'some' as LabPush,
    mattermostPush: 'on' as LabMattermostPush,
};

export function setLabAdminNews(next: Partial<typeof labAdminNews>): void {
    Object.assign(labAdminNews, next);
    change(() => undefined);
}

// A manifest in the release's own form, with made-up sums.
const LAB_MANIFEST = [
    'matty-release-manifest 1',
    `product ${PLUGIN_ID}`,
    'kind plugin',
    'version 4.5.0',
    'platform linux-amd64',
    'files 4',
    `${'1a'.repeat(32)} 2140 assets/icon.svg`,
    `${'2b'.repeat(32)} 31877 plugin.json`,
    `${'3c'.repeat(32)} 41250816 server/dist/plugin-linux-amd64`,
    `${'4d'.repeat(32)} 3348211 webapp/dist/main.js`,
    '',
].join('\n');

const ALTERED_REASON = 'server/dist/plugin-linux-amd64 has changed';

export function labReleaseAnswer(): ReleaseAnswer {
    switch (labAdminNews.release) {
    case 'unsigned':
        return {state: 'unsigned', version: '4.5.0'};
    case 'altered':
        return {state: 'altered', version: '4.5.0', reason: ALTERED_REASON, manifest: LAB_MANIFEST};
    default:
        return {state: 'signed', version: '4.5.0', manifest: LAB_MANIFEST};
    }
}

function labReleaseAlert(): ReleaseAlert | undefined {
    switch (labAdminNews.release) {
    case 'altered':
        return {
            state: 'altered',
            problem: `This plugin's files do not match the signed release they came with: ${ALTERED_REASON}. It goes on running. Install the release again from its published bundle.`,
            file: 'server/dist/plugin-linux-amd64',
        };
    case 'unlisted':
        return {
            state: 'signed',
            unlisted: 13,
            unlistedFiles: Array.from({length: 10}, (_, at) => `server/dist/notes/extra-${String(at + 1).padStart(2, '0')}.txt`),
        };
    default:
        return undefined;
    }
}

// Why Mattermost decides to push to nobody, in the server's words
// (server/push.go), or '' while it pushes.
const LAB_MATTERMOST_PUSH: Record<LabMattermostPush, string> = {
    on: '',
    off: 'Mattermost\'s own push notifications are switched off on this server, and Matty\'s go out only when Mattermost decides to push. ' +
        'An admin switches them on in System Console > Environment > Push Notification Server, where Enable Push Notifications must not be "Do not send push notifications".',
    licence: 'Mattermost\'s push notification server here needs a licence this server does not have, so Mattermost decides to push to nobody. ' +
        'An admin chooses another in System Console > Environment > Push Notification Server.',
    unknown: 'This server could not read Mattermost\'s own push notification settings just now.',
};

// The plugin's switch, as the Push notifications row has it: off only for
// 'off'; the store not answering and the refusal leave it on.
const labPushOn = (): boolean => labAdminNews.push !== 'off';

// What "me" carries of it, for a system admin: the release for everybody.
export function labAdminMe(admin: boolean): Partial<Me> {
    const {state, version} = labReleaseAnswer();
    const releaseAlert = admin ? labReleaseAlert() : undefined;

    // As the server's pushAlertFor: while the switch is on, why Mattermost
    // pushes to nobody.
    const pushAlert = admin && labPushOn() ? LAB_MATTERMOST_PUSH[labAdminNews.mattermostPush] : '';

    return {
        release: {state, version},
        ...(releaseAlert ? {releaseAlert} : {}),
        ...(pushAlert ? {pushAlert} : {}),
    };
}

// GET /admin/push: whether pushes can go out, and the counts by push server,
// as the server sends them. null for 'error', which the mock client answers
// 503; 'refused' the mock client answers 403.
export function labPushView(): AdminPush | null {
    const mode = labAdminNews.push;
    if (mode === 'error') {
        return null;
    }

    const problem = LAB_MATTERMOST_PUSH[labAdminNews.mattermostPush];
    const base: AdminPush = {
        on: mode !== 'off',
        ready: mode !== 'unready',
        mattermost: problem === '',
        ...(problem ? {mattermostProblem: problem} : {}),
        people: 0,
        devices: 0,
        hosts: [],
    };
    if (mode === 'empty' || mode === 'off' || mode === 'unready') {
        return base;
    }

    const now = Date.now();
    const hosts: AdminPush['hosts'] = [
        {host: 'ntfy.sh', devices: 5, working: 4, waiting: 1, failing: 0, refused: 0},
        {host: 'ntfy.home.lan:8080', devices: 3, working: 1, waiting: 0, failing: 0, refused: 2},
        {host: 'push.example.net', devices: 2, working: 1, waiting: 0, failing: 1, refused: 0, failingSince: now - (3 * HOUR)},
        {host: 'up.example.org', devices: 1, working: 1, waiting: 0, failing: 0, refused: 0},
    ];
    if (mode === 'full') {
        return {
            ...base,
            people: 5000,
            devices: 6212,
            full: true,
            hosts: [
                {host: 'ntfy.sh', devices: 4980, working: 4870, waiting: 61, failing: 37, refused: 12, failingSince: now - (26 * HOUR)},
                {host: 'push.example.net', devices: 1232, working: 1229, waiting: 3, failing: 0, refused: 0},
            ],
            dropped: {'queue full': 4, 'too many for one phone': 18},
        };
    }

    return {
        ...base,
        people: 8,
        devices: 11,
        hosts,
        ...(mode === 'problem' ? {problem: 'Lines 2, 5 of Push servers on your own network are not a host or host:port, or past the first 100 entries, and left out.'} : {}),
        dropped: {'other plugin': 7, 'too many for one phone': 2},
    };
}

// GET /admin/appcheck: the people and their sign-ins' verdicts, as the
// server lists them, each with the version its Matty said as it proved
// itself, and the minimum in force: a version for 'some' and 'require' (the
// check at refuse, so it goes by what each phone proved), the latest release
// known and looked for an hour ago, with the last look's trouble, for
// 'latest', and the latest not known yet for 'unknown'. null for 'error',
// which the mock client answers 503; 'refused' the mock client answers 403.
export type LabAppCheckView = {
    check: string;
    minimum?: string;
    minimumFrom?: 'setting' | 'latest';
    latest?: {version?: string; lookedAt?: number; trouble?: string; troubleAt?: number};
    byWord?: boolean;
    problem?: string;
    people: Array<{userId: string; username?: string; sessions: Array<Record<string, unknown>>}>;
};

export function labAppCheckView(): LabAppCheckView | null {
    const mode = labAdminNews.appCheck;
    if (mode === 'error') {
        return null;
    }

    const now = Date.now();
    const verdict = (ago: number, extra: Record<string, unknown>) => ({at: now - ago, expires: now - ago + (30 * 24 * HOUR), ...extra});
    const stale = 'This was verified for a certificate, a boot key or a revocation list this server no longer accepts. The app proves itself again.';
    const unproven = 'This was verified with no version of Matty kept beside it, which this server keeps with every check it counts. The app is asked to prove itself again.';
    if (mode === 'empty' || mode === 'off') {
        return {check: mode === 'off' ? 'off' : 'report', people: []};
    }

    const people = [
        {userId: ME_ID, username: 'victor', sessions: [
            verdict(6 * MINUTE, {verified: true, versionCode: 34, said: '0.1.33'}),
            verdict(3 * 24 * HOUR, {verified: true, development: true, versionCode: 34, said: '0.1.33'}),
        ]},
        {userId: 'u-dave', username: 'dave', sessions: [verdict(2 * HOUR, {verified: false, reason: 'This phone\'s bootloader is unlocked.', versionCode: 34, said: '0.1.33'})]},
        {userId: 'u-bob', username: 'bob', sessions: [verdict(26 * HOUR, {verified: false, stale: true, reason: mode === 'require' ? unproven : stale, versionCode: 33})]},
        {userId: 'u-carol', username: 'carol', sessions: [verdict(5 * 24 * HOUR, {verified: false, reason: 'This copy of Matty was signed with a certificate this server does not accept (3F:A2:19:C0:5B:7E:D4:81).', versionCode: 34, said: '0.1.33'})]},
        {userId: 'u-gone-0000000000000000000', sessions: [verdict(12 * 24 * HOUR, {verified: true, versionCode: 32, said: '0.1.31'})]},
    ];
    if (mode === 'many') {
        for (let n = 0; n < 36; n += 1) {
            people.push({userId: `u-many-${String(n).padStart(20, '0')}`, username: `viewer${String(n + 1).padStart(3, '0')}`, sessions: [
                verdict((n + 1) * 7 * HOUR, n % 5 === 3 ? {verified: false, reason: 'The app\'s proof does not come from Google\'s hardware attestation roots, so no phone\'s secure hardware vouches for it.'} : {verified: true, versionCode: 34, said: '0.1.33'}),
            ]});
        }
    }

    if (mode === 'latest') {
        return {
            check: 'report',
            minimum: '0.1.33',
            minimumFrom: 'latest',
            byWord: true,
            latest: {version: '0.1.33', lookedAt: now - HOUR, trouble: 'GitHub could not be reached.', troubleAt: now - HOUR},
            people,
        };
    }
    if (mode === 'unknown') {
        return {
            check: 'report',
            minimumFrom: 'latest',
            latest: {lookedAt: now - (5 * MINUTE), trouble: 'GitHub answered 503.', troubleAt: now - (5 * MINUTE)},
            people,
        };
    }
    if (mode === 'require') {
        return {check: 'require', minimum: '0.1.33', minimumFrom: 'setting', people};
    }

    return {
        check: 'report',
        ...(mode === 'problem' ?
            {problem: 'Not a SHA-256 digest, so left out: line 2 of More signing certificates. “0.1” in Minimum Matty version is not a version such as 0.1.32, so no minimum is in force.'} :
            {minimum: '0.1.33', minimumFrom: 'setting' as const, byWord: true}),
        people,
    };
}

export function getWorld(): World {
    return world;
}

export function subscribe(listener: () => void): () => void {
    listeners.add(listener);

    return () => {
        listeners.delete(listener);
    };
}

// Every change ends with Best Clipper worked out again, as every write to
// the server's clips document does, and as unlinking does there: so a vote,
// a delete, a block, or Victor's link coming or going moves the title the
// way the server would. A change that set bestClipper itself keeps what it
// set, until the next one.
export function change(apply: (current: World) => void): void {
    const before = world.bestClipper;
    apply(world);
    if (world.bestClipper === before) {
        world.bestClipper = bestClipperOf(world, Date.now());
    }
    reviewTwitchCover();
    listeners.forEach((listener) => listener());
}

// The boards, and nothing else. reset() replaces the whole world, which is not
// what somebody clicking a chip under "Airtime board" is asking for.
export function restoreBoards(): void {
    const fresh = starting();

    change((current) => {
        current.allTime = fresh.allTime;
        current.month = fresh.month;
        current.monthLabel = fresh.monthLabel;
        current.allTimeTotal = fresh.allTimeTotal;
        current.monthTotal = fresh.monthTotal;
        current.monthMedals = fresh.monthMedals;
    });
}

// The watchers' boards as they start, the setting on.
export function restoreWatchBoards(): void {
    const fresh = starting();

    change((current) => {
        current.watchBoards = true;
        current.watchMonth = fresh.watchMonth;
        current.watchAllTime = fresh.watchAllTime;
        current.watchMonthTotal = fresh.watchMonthTotal;
        current.watchAllTimeTotal = fresh.watchAllTimeTotal;
    });
}

// Whoever is first on the all-time Snoopers board while it is on: the Top
// Snooper, as the panel works it out from the snapshot.
export function topSnooperIn(current: World): string | undefined {
    return current.watchBoards ? current.watchAllTime.find((row) => row.rank === 1)?.userId : undefined;
}

// Victor first on the all-time Snoopers board, or back to third: the
// binoculars beside his crown in one post header, and on his own card while he
// is live. His hours are changed and the board ranked again by them, as the
// server would, so the trophy card agrees with the badges; a long name already
// on it stays. The boards come back first if they were empty or off.
export function toggleVictorSnoops(): void {
    const fresh = starting();

    change((current) => {
        if (!current.watchBoards || current.watchAllTime.length === 0) {
            current.watchBoards = true;
            current.watchMonth = fresh.watchMonth;
            current.watchAllTime = fresh.watchAllTime;
            current.watchMonthTotal = fresh.watchMonthTotal;
            current.watchAllTimeTotal = fresh.watchAllTimeTotal;
        }

        const others = current.watchAllTime.filter((row) => row.userId !== ME_ID);
        const seconds = topSnooperIn(current) === ME_ID ?
            fresh.watchAllTime.find((row) => row.userId === ME_ID)?.seconds ?? 0 :
            Math.max(0, ...others.map((row) => row.seconds)) + (7 * 3600);

        current.watchAllTime = current.watchAllTime.
            map((row) => (row.userId === ME_ID ? {...row, seconds} : row)).
            sort((a, b) => b.seconds - a.seconds).
            map((row, index) => ({...row, rank: index + 1}));
    });
}

// The setting on and nobody counted yet, or the setting off.
export function emptyWatchBoards(on: boolean): void {
    change((current) => {
        current.watchBoards = on;
        current.watchMonth = [];
        current.watchAllTime = [];
        current.watchMonthTotal = 0;
        current.watchAllTimeTotal = 0;
    });
}

// Both boards empty and the months already won kept, as after an import of
// past months alone. The medals come back if "empty" cleared them first, or
// this would be an empty snapshot with no trophy, which looks like a bug in
// SidebarTitle rather than the case being looked at. Medals still there are
// left alone, so a long name already on them stays.
export function pastMonthsOnly(): void {
    const fresh = starting();

    change((current) => {
        current.allTime = [];
        current.month = [];
        current.allTimeTotal = 0;
        current.monthTotal = 0;
        if (current.monthMedals.length === 0) {
            current.monthMedals = fresh.monthMedals;
        }
    });
}

export function reset(): void {
    // Counted as a press of the link switch: a panel that unlinked itself from
    // its footer would otherwise go on showing that after the reset put the
    // link back.
    world = {...freshWorld(), linkSwitches: world.linkSwitches + 1};
    listeners.forEach((listener) => listener());
}

// A display name far wider than any column, the way a full name and a team
// can be, on everything that names Erin: every board and the monthly medals.
// Pressing it again puts "Erin" back.
export const LONG_NAME = 'Erin Pemberton-Whitfield of the Sunday Racing League';

export function hasLongName(current: World): boolean {
    return current.allTime.concat(current.month, current.watchMonth, current.watchAllTime).some((row) => row.displayName === LONG_NAME) ||
        current.monthMedals.some((entry) => entry.displayName === LONG_NAME);
}

export function toggleLongName(): void {
    change((current) => {
        const name = hasLongName(current) ? 'Erin' : LONG_NAME;
        const rename = (row: Leader) => (row.userId === 'u-erin' ? {...row, title: name, displayName: name} : row);

        current.allTime = current.allTime.map(rename);
        current.month = current.month.map(rename);
        current.watchMonth = current.watchMonth.map(rename);
        current.watchAllTime = current.watchAllTime.map(rename);
        current.monthMedals = current.monthMedals.map((entry) => (entry.userId === 'u-erin' ? {...entry, displayName: name} : entry));
    });
}

// What is on a stream, by the server's one rule (nowPlayingFor, in
// nowplaying.go): never on a 24/7 channel; what the streamer said, when they
// said something; otherwise the title of their own plan that started most
// recently, when that plan has one.
function nowPlayingOf(item: Stream, schedule: ScheduleItem[]): NowPlaying | undefined {
    if (item.alwaysOn || !item.userId) {
        return undefined;
    }

    const said = world.nowPlaying[item.userId];
    if (said) {
        return said;
    }

    const now = Date.now();
    const latest = schedule.
        filter((plan) => plan.userId === item.userId && plan.startsAt <= now).
        sort((a, b) => (b.startsAt - a.startsAt) || (a.id < b.id ? 1 : -1))[0];
    if (!latest?.media) {
        return undefined;
    }

    return {category: latest.media.kind === 'game' ? 'game' : 'movie', title: latest.media.title, media: latest.media};
}

// What's on? samples: one with art, one without, for whoever is live.
export function restoreNowPlaying(): void {
    change((current) => {
        current.nowPlaying = {...sampleNowPlaying(), ...(current.nowPlaying[ME_ID] ? {[ME_ID]: current.nowPlaying[ME_ID]} : {})};
    });
}

export function clearAllNowPlaying(): void {
    change((current) => {
        current.nowPlaying = {};
    });
}

// Erin's film night, started half an hour ago and linked to Heat: with Erin
// live and nothing said, her card shows the plan's title, which is the rule's
// second half.
export function erinFilmOn(): void {
    change((current) => {
        current.schedule = current.schedule.concat(planned('Film night', 'Erin', 'u-erin', -30 * MINUTE, 'movie', 'tmdb:movie:949'));
    });
}

// ---------------------------------------------------------------- clips, lab

// The six sample clips back, as the world starts with them.
export function restoreClips(): void {
    change((current) => {
        current.clips = sampleClips();
    });
}

// No clips at all, with the upvotes already banked left alone: Bob stays
// Best Clipper on them.
export function clearClips(): void {
    change((current) => {
        current.clips = [];
    });
}

// Who holds Best Clipper, by the scores rather than by decree, so the next
// vote or delete in the lab keeps it where the server would:
//   none:   an admin has reset everybody's score, so nobody has a counted
//           upvote (the clips still show theirs);
//   bob:    the starting scores, where Bob's 142 lead;
//   victor: Victor with 180 banked from clips that have gone, ahead of Bob —
//           while he is linked, as only linked people count.
export function setLabBestClipper(who: 'none' | 'bob' | 'victor'): void {
    const now = Date.now();
    change((current) => {
        current.clipBanked = startingBanked();
        current.clipResets = {};
        if (who === 'none') {
            current.clipBanked = {};
            current.clips.forEach((entry) => {
                current.clipResets[entry.clipperId] = now;
            });
        } else if (who === 'victor') {
            current.clipBanked[ME_ID] = {up: 180, clips: 8, lastUpAt: now - (2 * DAY)};
        }

        // Worked out afresh rather than kept: the holder moves when the
        // scores say so, and a tie would otherwise keep whoever held it.
        current.bestClipper = null;
        current.bestClipper = bestClipperOf(current, now);
    });
}

// The clip with the most upvotes, "that jump", with an 80-character title
// instead, for the places a title has to give way: the Trophies' best clip
// line, the System Console's row, the list and the viewer.
export const LONG_CLIP_TITLE = 'that jump off the radio tower into the river, with the whole convoy watching it!';

export function hasLongClipTitle(current: World): boolean {
    return current.clips.some((entry) => entry.id === '3f9a0c2e7b1d4a55' && entry.title === LONG_CLIP_TITLE);
}

export function toggleLongClipTitle(): void {
    change((current) => {
        const long = hasLongClipTitle(current);
        current.clips = current.clips.map((entry) => (
            entry.id === '3f9a0c2e7b1d4a55' ? {...entry, title: long ? 'that jump' : LONG_CLIP_TITLE} : entry
        ));
    });
}

// Victor, Dave, Bob and Erin live at once, for a full set of four tiles.
export function everybodyLive(): void {
    change((current) => {
        const names: Array<[string, string]> = [['Victor', ME_ID], ['Dave', 'u-dave'], ['Bob', 'u-bob'], ['Erin', 'u-erin']];
        names.forEach(([name, userId]) => {
            if (!current.streams.some((item) => item.userId === userId)) {
                current.streams = current.streams.concat(stream(name, userId));
            }
        });
    });
}

// The end of a stream that has stopped, held for clipping: Dave went off two
// minutes ago, or Bob's stream dropped forty seconds ago and has not come
// back. Whoever it is goes off the air now, under the handle they were live
// with, so a tile watching them waits with the scissors for the end, and the
// dialog's Just ended lists it; somebody not live leaves a handle of their
// own. What they were on goes with them, as going off takes it down, and is
// kept with the end for a draft cut from it.
export function setLabTail(kind: 'none' | 'ended' | 'dropped'): void {
    const now = Date.now();
    change((current) => {
        if (kind === 'none') {
            current.tails = [];
            return;
        }

        const who = kind === 'ended' ? {userId: 'u-dave', name: 'Dave'} : {userId: 'u-bob', name: 'Bob'};
        const live = current.streams.find((item) => item.userId === who.userId && !item.alwaysOn);
        const handle = live?.handle || `h-${who.name.toLowerCase()}-${kind === 'ended' ? 'went-off' : 'dropped'}`;
        const nowPlaying = live ? nowPlayingOf(live, current.schedule) : current.nowPlaying[who.userId];
        if (live) {
            current.streams = current.streams.filter((item) => item.handle !== live.handle);
            const {[who.userId]: _gone, ...rest} = current.nowPlaying;
            current.nowPlaying = rest;
        }

        const endedAt = kind === 'ended' ? now - (2 * MINUTE) : now - 40000;
        const tail: LabTail = {
            handle,
            title: who.name,
            userId: who.userId,
            reason: kind,
            endedAt,
            until: endedAt + TAIL_MS,
            heldMs: kind === 'ended' ? current.clipLength * 1000 : 47000,
            ...(nowPlaying ? {nowPlaying} : {}),
        };
        current.tails = current.tails.filter((held) => held.handle !== handle && held.reason !== kind).concat(tail);
    });
}

// The four of them live and watched two by two, with every kind of scissors
// that clips an end: Dave has just gone off, so his tile waits with the
// scissors for the end of his stream, and the three still live have come
// back after a drop, so theirs clip the end before it.
export function everybodyLiveWithTails(): void {
    setLabRecording('dropped');
    setLabTail('ended');
}

// Two drafts of Victor's: one of Dave's stream with 52 minutes left, tagged
// Hytale, which Edit opens in the editor, and one of Bob's being published,
// which has no buttons. Or none.
export function setLabDrafts(on: boolean): void {
    const now = Date.now();
    const draft = (id: string, streamerId: string, durationMs: number, ageMs: number, state: LabDraft['state']): LabDraft => {
        const streamer = clipPersonOf(streamerId);

        return {
            id,
            handle: `h-${(streamer.username || 'stream')}`,
            source: 'live',
            state,
            createdAt: now - ageMs,
            expiresAt: now - ageMs + (2 * HOUR),
            startAt: now - ageMs - durationMs,
            durationMs,
            requestedMs: world.clipLength * 1000,
            partial: false,
            truncated: false,
            width: 256,
            height: 144,
            codecs: 'avc1.64000c,opus',
            bytes: Math.round(SAMPLE_BYTES * durationMs / 120000),
            hasThumb: false,
            streamer,
            owner: ME_ID,
            streamKey: `live-${streamer.username || 'stream'}`,
        };
    };

    change((next) => {
        next.drafts = on ? [
            {...draft('d1a2b3c4d5e6f708', 'u-dave', 118000, 68 * MINUTE, 'ready'), tag: clipTagOf(hytaleNowPlaying())},
            draft('e1f2a3b4c5d6e7f8', 'u-bob', 45000, 5 * MINUTE, 'publishing'),
        ] : [];
    });
}

// What Dave's stream is tagged with, for the clips cut from it: Hytale (a
// game IGDB found), Heat (a movie lookups found), or nothing at all.
export type LabTag = 'hytale' | 'heat' | 'none';

export function labTagOf(current: World): LabTag | 'other' {
    const said = current.nowPlaying['u-dave'];
    if (!said) {
        return 'none';
    }
    if (said.media?.ref === 'igdb:113360') {
        return 'hytale';
    }

    return said.media?.ref === 'tmdb:movie:949' ? 'heat' : 'other';
}

export function setDaveTag(tag: LabTag): void {
    change((current) => {
        const {'u-dave': _dave, ...rest} = current.nowPlaying;
        if (tag === 'none') {
            current.nowPlaying = rest;

            return;
        }

        current.nowPlaying = {...rest, 'u-dave': tag === 'hytale' ? hytaleNowPlaying() : heatNowPlaying()};
    });
}

// The Recording row: how the recordings of the live streams are doing. Warming
// has three seconds and partial 47 recorded, counted from now, so the
// scissors wake at five seconds and the corner tag counts up; the others
// count from each stream's start.
export function setLabRecording(recording: LabRecording): void {
    const now = Date.now();
    change((current) => {
        current.recording = recording;
        if (recording === 'warming') {
            current.recordingFrom = now - 3000;
        } else if (recording === 'partial') {
            current.recordingFrom = now - 47000;
        } else if (recording === 'dropped') {
            // When they dropped: now, so the end before it is held for ten
            // minutes from here.
            current.recordingFrom = now;
        } else {
            current.recordingFrom = 0;
        }
    });
}

// The Streamer row: Victor's say over clips of his own stream (spec 3.11,
// addendum 3.8).
//   not-linked: not linked, and his stream, when live, matched to nobody —
//               no switch and no notice, only Link Your Account;
//   username:   not linked, but live and matched to him by his username, so
//               it is kept for clipping all the same — the read-only notice
//               above Link Your Account;
//   allowed:    linked by hand, clips of his stream allowed;
//   off:        linked by hand, clipping of his stream switched off.
// The first two leave clips of his stream allowed, as a stream nobody has
// switched off is; the one with a notice goes live for it.
export type LabStreamer = 'not-linked' | 'username' | 'allowed' | 'off';

export function labStreamerOf(current: World): LabStreamer {
    if (current.me.streamKey !== '') {
        return current.streamerAllowed ? 'allowed' : 'off';
    }

    return current.victorSource === 'username' ? 'username' : 'not-linked';
}

export function setLabStreamer(mode: LabStreamer): void {
    change((current) => {
        const linked = mode === 'allowed' || mode === 'off';
        const source = linked ? 'manual' : (mode === 'username' ? 'username' : 'none');
        current.me = {...current.me, streamKey: linked ? 'live-victor' : ''};
        current.victorSource = source;
        current.streamerAllowed = mode !== 'off';
        current.streams = current.streams.map((item) => (
            item.userId === ME_ID ? {...item, linkSource: source === 'none' ? undefined : source} : item
        ));
        if (mode === 'username' && !current.streams.some((item) => item.userId === ME_ID)) {
            current.streams = current.streams.concat(stream('Victor', ME_ID));
        }
    });
}

// Dave live, for the scissors to be looked at on; his handle.
export function ensureDaveLive(): string {
    const live = world.streams.find((item) => item.userId === 'u-dave');
    if (live) {
        return live.handle;
    }

    goLive('Dave', 'u-dave');

    return world.streams.find((item) => item.userId === 'u-dave')?.handle || '';
}

// ---------------------------------------------------------------- clips, read

// How long the end of a stream that went off is held for clipping.
export const TAIL_MS = 10 * MINUTE;

// When a clip goes: its retention, counted from when it was made. The lab
// has no day of grace after "Keep clips for" is lowered; the System Console's
// pending count stands in for it.
export function clipExpiresAt(entry: LabClip, current: World = world): number {
    return entry.createdAt + (current.retentionDays * DAY);
}

export function liveClipsOf(current: World, now: number): LabClip[] {
    return current.clips.filter((entry) => now < clipExpiresAt(entry, current));
}

// Whether somebody's link stands now. Victor's is the lab's own switch;
// everybody else in the lab has linked theirs.
function linkedNow(current: World, userId: string): boolean {
    return userId === ME_ID ? current.me.streamKey !== '' : true;
}

// eligible(u) (spec 2.9 with the addendum's 3.4): linked now, and not blocked
// from clipping. Only these people's votes count, and only they can hold
// Best Clipper.
export function clipEligible(current: World, userId: string): boolean {
    return linkedNow(current, userId) && !current.clipBlocked[userId];
}

// The upvotes everybody is shown on a clip.
export function shownVotes(current: World, entry: LabClip): number {
    return Object.keys(entry.voters).filter((voter) => clipEligible(current, voter)).length;
}

// The upvotes that count toward Best Clipper: the shown ones, less any cast
// before an admin reset the clipper's score, and when the last of them was.
export function countedVotes(current: World, entry: LabClip): {count: number; latest: number} {
    const reset = current.clipResets[entry.clipperId] || 0;
    const counted = Object.entries(entry.voters).filter(([voter, at]) => clipEligible(current, voter) && at > reset);

    return {count: counted.length, latest: Math.max(0, ...counted.map(([, at]) => at))};
}

// A person as a clip names them.
export function clipPersonOf(userId: string | undefined, streamName = ''): ClipPerson {
    if (!userId) {
        return {name: streamName || 'Stream 3f9a0c'};
    }

    const name = CLIP_NAMES[userId] || userId.replace(/^u-/, '');

    return {userId, username: name.toLowerCase(), displayName: name, name};
}

// Best Clipper by the server's rule (spec 2.9): everybody's banked upvotes
// plus the counted ones on their clips still in the document; among the
// eligible with at least one, the most wins, a tie keeps the holder, and
// otherwise whoever got there first. Clips past their time that nobody has
// swept count as they are, which is what banking them would give.
export function bestClipperOf(current: World, now: number): BestClipper | null {
    const scores = new Map<string, {up: number; clips: number; reachedAt: number}>();
    const scoreOf = (userId: string) => {
        const found = scores.get(userId) || {up: 0, clips: 0, reachedAt: 0};
        scores.set(userId, found);

        return found;
    };

    Object.entries(current.clipBanked).forEach(([userId, banked]) => {
        const score = scoreOf(userId);
        score.up += banked.up;
        score.clips += banked.clips;
        score.reachedAt = Math.max(score.reachedAt, banked.lastUpAt);
    });
    current.clips.forEach((entry) => {
        const score = scoreOf(entry.clipperId);
        const counted = countedVotes(current, entry);
        score.up += counted.count;
        score.clips += 1;
        score.reachedAt = Math.max(score.reachedAt, counted.latest);
    });

    const candidates = Array.from(scores.entries()).filter(([userId, score]) => clipEligible(current, userId) && score.up >= 1);
    if (candidates.length === 0) {
        return null;
    }

    const most = Math.max(...candidates.map(([, score]) => score.up));
    const holder = current.bestClipper?.userId;
    const keeps = candidates.find(([userId, score]) => userId === holder && score.up === most);
    const [winner, score] = keeps || candidates.
        filter(([, candidate]) => candidate.up === most).
        sort(([a, first], [b, second]) => (first.reachedAt - second.reachedAt) || (a < b ? -1 : 1))[0];

    // Their most upvoted clip still showing, the newest of a tie.
    const best = liveClipsOf(current, now).
        filter((entry) => entry.clipperId === winner).
        sort((a, b) => (shownVotes(current, b) - shownVotes(current, a)) || (b.createdAt - a.createdAt))[0];
    const person = clipPersonOf(winner);

    return {
        userId: winner,
        username: person.username,
        displayName: person.displayName,
        upvotes: score.up,
        clips: score.clips,
        since: winner === holder && current.bestClipper ? current.bestClipper.since : now,
        ...(best ? {bestClip: {id: best.id}} : {}),
    };
}

// A clip as the reader sees it (spec 2.10.3): the counts, and what this
// reader may do with it. Of their own stream means cut from the stream whose
// key they are linked to now. The System Console's view may delete anything
// and sees how many reports there are.
export function clipViewOf(current: World, entry: LabClip, inConsole = false): Clip {
    const viewer = current.me.userId;
    const key = current.me.streamKey;
    const mine = entry.clipperId === viewer;
    const ofMyStream = key !== '' && entry.streamKey === key;

    // Let in to watch only: every flag false, whatever Admin or Mine say
    // (the server's WatchOnly view, design/lock-screen.md 3.6).
    const watchOnly = !inConsole && labAccessOf(current) !== 'linked';

    return {
        id: entry.id,
        title: entry.title,
        createdAt: entry.createdAt,
        expiresAt: clipExpiresAt(entry, current),
        startAt: entry.startAt,
        durationMs: entry.durationMs,
        ...(entry.leadMs ? {leadMs: entry.leadMs} : {}),
        width: entry.width,
        height: entry.height,
        codecs: entry.codecs,
        bytes: entry.bytes,
        ...(entry.thumb ? {thumb: entry.thumb} : {}),
        clipper: clipPersonOf(entry.clipperId),
        streamer: clipPersonOf(entry.streamerId, entry.streamName),
        ...(entry.streamNote ? {streamNote: entry.streamNote} : {}),
        ...(entry.tag ? {tag: entry.tag} : {}),
        upvotes: shownVotes(current, entry),
        voted: viewer in entry.voters,
        mine,
        ofMyStream,
        canVote: !watchOnly && !mine,
        canDelete: inConsole || (!watchOnly && (mine || ofMyStream || current.me.isAdmin)),
        canReport: !watchOnly && !mine,
        canShare: !watchOnly,
        reported: viewer in entry.reports,
        ...(inConsole ? {reports: Object.keys(entry.reports).length} : {}),

        // The server's rule (clipDownloadRefusal): the admin's switch, a
        // link, and the streamer's say; never in the console.
        canDownload: !inConsole && !watchOnly && labDownloadRefusal(current, entry) === '',
    };
}

// Why Victor may not download a clip, or '' when he may, as the server
// answers it: the admin's switch, then his link, then the clip's streamer.
// Only Victor's own stream has a block the lab can switch.
export function labDownloadRefusal(current: World, entry: LabClip): '' | 'downloads_off' | 'watch_only' | 'streamer_downloads_off' {
    if (!current.downloadsAllowed) {
        return 'downloads_off';
    }
    if (labAccessOf(current) !== 'linked') {
        return 'watch_only';
    }
    if (current.streamerNoDownloads && entry.streamKey === 'live-victor') {
        return 'streamer_downloads_off';
    }

    return '';
}

// A clip as a shared post's card shows it: no counts and no flags.
// Where share links point: the lab's stand-in for Mattermost's Site URL.
const LAB_SITE_URL = 'https://chat.example.com';

// A shared post's message, a copy of the server's clipShareMessage (addendum
// 4.3, clips_share.go): the server's own words, with its name, and the
// link, and nothing anybody typed.
export function labShareMessage(entry: {id: string; durationMs: number}): string {
    return `🎬 ${world.me.name} clip · ${clipTime(entry.durationMs)}\nWatch it: ${LAB_SITE_URL}/plug/${PLUGIN_ID}/clip/${entry.id}`;
}

// What a shared post carries for its card to show, a copy of the server's
// clipShareProps: only what a reader who has not linked may see (addendum
// 4.3), and what the stream was tagged with (4.14), left out with no tag.
export function labShareProps(entry: LabClip): ClipPostProps {
    const clipper = clipPersonOf(entry.clipperId);
    const streamer = clipPersonOf(entry.streamerId, entry.streamName);
    const tag = entry.tag && tagTitleOf(entry.tag) !== '' ? entry.tag : undefined;
    const kind = tag ? tagKindOf(tag) : '';

    return {
        villanet_clip_id: entry.id,
        villanet_clip_title: entry.title,
        villanet_clip_duration_ms: entry.durationMs,
        villanet_clip_created_at: entry.createdAt,
        villanet_clip_clipper_id: entry.clipperId,
        villanet_clip_clipper: clipper.name,
        ...(entry.streamerId ? {villanet_clip_streamer_id: entry.streamerId} : {}),
        villanet_clip_streamer: streamer.name,
        ...(tag ? {villanet_clip_tag: tagTitleOf(tag)} : {}),
        ...(tag && kind !== '' ? {villanet_clip_tag_kind: kind} : {}),
    };
}

export function clipCardOf(entry: LabClip): ClipCard {
    return {
        id: entry.id,
        title: entry.title,
        createdAt: entry.createdAt,
        durationMs: entry.durationMs,
        ...(entry.thumb ? {thumb: entry.thumb} : {}),
        clipper: clipPersonOf(entry.clipperId),
        streamer: clipPersonOf(entry.streamerId, entry.streamName),
        ...(entry.tag ? {tag: entry.tag} : {}),
    };
}

// Sixteen hex characters that change whenever the text does, standing in for
// the server's sha256: the lab only needs "different".
function hashHex(text: string): string {
    let a = 0x811c9dc5;
    let b = 0x2545f491;
    for (let index = 0; index < text.length; index++) {
        const code = text.charCodeAt(index);
        a = Math.imul(a ^ code, 0x01000193) >>> 0;
        b = Math.imul(b ^ code, 0x5bd1e995) >>> 0;
    }

    return a.toString(16).padStart(8, '0') + b.toString(16).padStart(8, '0');
}

// snapshot.clips (spec 2.12): counts, and a version over every clip's id,
// title, upvotes and picture, so an open list knows to fetch again; and from
// 3.2.1, while downloads are on, over which clips their streamer blocked, so
// an open viewer's Download follows either switch.
export function clipsInfoOf(current: World, now: number): ClipsInfo {
    const live = liveClipsOf(current, now);
    const newest = Math.max(0, ...live.map((entry) => entry.createdAt));
    const blocked = (entry: LabClip) => (current.downloadsAllowed && current.streamerNoDownloads && entry.streamKey === 'live-victor' ? '|no downloads' : '');
    const version = live.length === 0 ? '' :
        hashHex((current.downloadsAllowed ? 'downloads\n' : '') +
            live.map((entry) => `${entry.id}|${entry.title}|${shownVotes(current, entry)}|${entry.thumb || ''}${blocked(entry)}\n`).join(''));

    return {
        enabled: current.clipsEnabled,
        lengthSeconds: current.clipLength,
        retentionDays: current.retentionDays,
        count: live.length,
        ...(newest > 0 ? {newest} : {}),
        ...(version ? {version} : {}),
    };
}

function streamStartOf(item: Stream, now: number): number {
    const at = item.streamStart ? Date.parse(item.streamStart) : NaN;

    return Number.isNaN(at) ? now : at;
}

// The end of the stretch before a drop, held while a live stream's new
// recording starts: what "dropped" means in the Recording row. With what the
// stream was on, for a draft cut from it.
function droppedTailOf(current: World, item: Stream, now: number): LabTail | undefined {
    // Switching clipping off wipes what was held of the stream, its tail
    // included, and so does a wipe after the drop.
    if (current.recording !== 'dropped' || item.alwaysOn || (item.userId === ME_ID && !current.streamerAllowed)) {
        return undefined;
    }

    const endedAt = current.recordingFrom || streamStartOf(item, now);
    if ((current.restartedAt[item.handle] || 0) >= endedAt) {
        return undefined;
    }

    const nowPlaying = nowPlayingOf(item, current.schedule);

    return {
        handle: item.handle,
        title: item.title,
        ...(item.userId ? {userId: item.userId} : {}),
        reason: 'dropped',
        endedAt,
        until: endedAt + TAIL_MS,
        heldMs: current.clipLength * 1000,
        ...(nowPlaying ? {nowPlaying} : {}),
    };
}

// Every end held now, newest first, as the server keeps them: the stopped
// streams', and the live ones' from before a drop.
export function labTailsOf(current: World, now: number): LabTail[] {
    const dropped = current.streams.
        map((item) => droppedTailOf(current, item, now)).
        filter((tail): tail is LabTail => Boolean(tail));

    return current.tails.concat(dropped).
        filter((tail) => tail.until > now && tail.heldMs >= 5000).
        sort((a, b) => b.endedAt - a.endedAt);
}

// The same as snapshot.clipTails carries them: without what each stream was
// on, which the server keeps to itself.
export function clipTailsOf(current: World, now: number): ClipTail[] {
    return labTailsOf(current, now).map(({nowPlaying: _kept, ...tail}) => tail);
}

// What a live stream says about its recording (spec 2.12), by the Recording
// row: never on a 24/7 channel, nothing while Clips are off, and "off" for
// Victor's own while he has switched clipping of it off.
function clipFieldsOf(current: World, item: Stream, now: number): Pick<Stream, 'clip' | 'clipFrom' | 'clipTail'> {
    if (!current.clipsEnabled || item.alwaysOn) {
        return {};
    }
    if (item.userId === ME_ID && !current.streamerAllowed) {
        return {clip: 'off'};
    }

    const tail = clipTailsOf(current, now).find((held) => held.handle === item.handle);
    const held = tail ? {clipTail: tail.until} : {};

    // A wipe cuts the recording: it starts again from then.
    const started = Math.max(streamStartOf(item, now), current.restartedAt[item.handle] || 0);
    switch (current.recording) {
    case 'ready':
    case 'truncated':
        return {clip: 'ready', clipFrom: started, ...held};
    case 'warming':
    case 'partial':
        return {clip: 'ready', clipFrom: Math.max(current.recordingFrom, started), ...held};
    case 'dropped':
        return {clip: 'starting', ...held};
    default:
        return {clip: current.recording, ...held};
    }
}

// me.clipsAlert, for admins: what needs putting right, in counts.
function clipsAlertOf(current: World, now: number): string {
    const parts: string[] = [];
    const reported = liveClipsOf(current, now).filter((entry) => Object.keys(entry.reports).length > 0).length;
    if (reported > 0) {
        parts.push(reported === 1 ? '1 clip has been reported' : `${reported} clips have been reported`);
    }
    if (current.retentionPending > 0) {
        const at = new Date(now + (20 * HOUR)).toLocaleString('en-GB', {weekday: 'short', hour: '2-digit', minute: '2-digit'});
        parts.push(`Keep clips for was lowered: ${current.retentionPending} clips will be removed ${at}`);
    }

    return parts.join(' · ');
}

// peopleOf is the snapshot's people index, as the server builds it
// (server/people.go): one live handle a person, never a covered entry, a
// paused stream or a 24/7 channel, the Broadcast Box stream before the Twitch
// one; and in voice, muted only when every session of theirs is. The lab has
// no badges to hold. {} when nobody has anything to say, never absent.
function peopleOf(listed: Array<{handle: string; userId?: string; paused: boolean; alwaysOn: boolean; source?: string; coveredBy?: string}>, voice: VoicePerson[]): Snapshot['people'] {
    const people: NonNullable<Snapshot['people']> = {};
    const fromBroadcastBox: Record<string, boolean> = {};

    listed.forEach((item) => {
        if (!item.userId || item.paused || item.alwaysOn || item.coveredBy) {
            return;
        }

        const broadcastBox = !item.source;
        if (fromBroadcastBox[item.userId] && !broadcastBox) {
            return;
        }

        people[item.userId] = {...people[item.userId], live: item.handle};
        fromBroadcastBox[item.userId] = broadcastBox;
    });

    voice.forEach((person) => {
        if (!person.userId) {
            return;
        }

        const before = people[person.userId]?.voice;
        people[person.userId] = {...people[person.userId], voice: person.muted && before !== 'open' ? 'muted' : 'open'};
    });

    return people;
}

// The airtime boards as the server ranks them. With Twitch on, the boards are
// by person, Broadcast Box and Twitch time together; with it off, the Twitch
// hours are kept and left off (design/twitch-live.md 7.8, question 3).
function airtimeBoards(): {allTime: Leader[]; month: Leader[]} {
    const twitchOn = labTwitchOn(world);

    return {
        allTime: twitchOn ? withTwitchHours(world.allTime, world.twitchSeconds) : world.allTime,
        month: twitchOn ? withTwitchHours(world.month, twitchMonthOf(world.twitchSeconds)) : world.month,
    };
}

// Everything the awards are decided from, as the server has it in hand when
// it publishes: awards.ts is asked of this, as awards_decide.go is of the
// server's.
function awardFacts(): AwardFacts {
    const {allTime, month} = airtimeBoards();
    const {fans, kinds, achievements} = gathered320(world);

    return {
        allTime,
        month,
        monthLabel: world.monthLabel,
        watchAllTime: world.watchBoards ? world.watchAllTime : [],
        bestClipper: world.clipsEnabled && world.bestClipper ? world.bestClipper : undefined,
        monthMedals: world.monthMedals,
        fans,
        kinds,
        achievements,
        choices: Object.fromEntries(Object.entries(world.awardChoices).map(([userId, entry]) => [userId, entry.show])),
    };
}

// A fingerprint, for a version and a tag: all that matters of it here is that
// it moves when what it was made from does.
function sumOf(text: string): string {
    let sum = 5381;
    for (let index = 0; index < text.length; index++) {
        sum = ((sum * 33) ^ text.charCodeAt(index)) >>> 0;
    }

    return sum.toString(16).padStart(8, '0');
}

// boardsOf is the /api/v1/boards payload: the boards and lists under the
// names the snapshot gave them until 3.6.0, and everybody's record. Its
// version moves with everything in it but a board's seconds, as the server's
// does.
export function boardsOf(): Boards {
    const facts = awardFacts();
    const {fans, fansOn} = gathered320(world);

    const people: Boards['people'] = {};
    holdersOf(facts).forEach((userId) => {
        const rows = rowsOf(facts, userId);
        if (rows.length > 0) {
            people[userId] = {rows};
        }
    });

    const boards: Boards = {
        version: '',
        allTime: facts.allTime.length > 0 ? facts.allTime : undefined,
        month: facts.month.length > 0 ? facts.month : undefined,
        monthLabel: world.monthLabel,

        // The boards count people, as the server's do: somebody only on
        // Twitch is one more.
        allTimeTotal: world.allTimeTotal + (facts.allTime.length - world.allTime.length),
        monthTotal: world.monthTotal + (facts.month.length - world.month.length),

        // Absent rather than empty, as the server's omitempty sends it.
        monthMedals: world.monthMedals.length > 0 ? world.monthMedals : undefined,

        // All absent while the setting is off, and the boards absent when
        // nobody is on them, as the server sends them.
        watchBoards: world.watchBoards || undefined,
        watchMonth: world.watchBoards && world.watchMonth.length > 0 ? world.watchMonth : undefined,
        watchAllTime: world.watchBoards && world.watchAllTime.length > 0 ? world.watchAllTime : undefined,
        watchMonthTotal: world.watchBoards && world.watchMonthTotal > 0 ? world.watchMonthTotal : undefined,
        watchAllTimeTotal: world.watchBoards && world.watchAllTimeTotal > 0 ? world.watchAllTimeTotal : undefined,

        // The holder only while Clips are on.
        bestClipper: facts.bestClipper,

        fans: fans.length > 0 ? fans : undefined,
        fansOn: fansOn || undefined,
        achievementKinds: facts.kinds.length > 0 ? facts.kinds : undefined,
        achievements: facts.achievements.length > 0 ? facts.achievements : undefined,
        people,
    };

    boards.version = sumOf(JSON.stringify(boards, (key, value) => (key === 'seconds' || key === 'twitchSeconds' ? 0 : value)));

    return boards;
}

// The tag GET /api/v1/boards answers under: it moves with every second.
export function boardsTagOf(boards: Boards): string {
    return `"${sumOf(JSON.stringify(boards))}"`;
}

// What the reader holds, for GET /api/v1/awards/mine.
export function heldByMe(): HeldAward[] {
    return heldOf(awardFacts(), ME_ID);
}

// snapshotOf is the /api/v1/streams payload, assembled exactly as the server
// assembles it: soonest first, each card with its badges, and each person
// with what goes beside their name. The boards themselves are boardsOf's.
export function snapshotOf(): Snapshot {
    const now = Date.now();
    const schedule = world.schedule.
        filter((item) => item.startsAt + (3 * HOUR) > Date.now()).
        slice().
        sort((a, b) => (a.startsAt - b.startsAt) || (a.id < b.id ? -1 : 1));

    const rank = (rows: Leader[], userId?: string) => {
        const row = rows.find((entry) => entry.userId === userId);

        return row && row.rank <= 3 ? row.rank : 0;
    };

    const twitchOn = labTwitchOn(world);
    const facts = awardFacts();
    const listed = twitchOn ? mergedWithTwitch(world) : world.streams;

    // The people index, and beside each name what the server decided goes
    // there.
    const people = peopleOf(listed, world.voiceConnected ? world.voice : []) || {};
    holdersOf(facts).forEach((userId) => {
        const awards = besideOf(facts, userId);
        if (awards.length > 0) {
            people[userId] = {...people[userId], awards};
        }
    });

    return {
        streams: listed.map((item) => {
            // A card's places are its person's on the boards; a 24/7 channel
            // has none.
            const awards = onCardOf(facts, item, {
                allTime: item.alwaysOn ? 0 : rank(facts.allTime, item.userId),
                month: item.alwaysOn ? 0 : rank(facts.month, item.userId),
            });

            return {
                ...item,
                awards: awards.length > 0 ? awards : undefined,
                nowPlaying: nowPlayingOf(item, schedule),
                ...(item.source ? {} : clipFieldsOf(world, item, now)),
            };
        }),
        boards: {version: boardsOf().version},
        schedule,
        voice: {
            connected: world.voiceConnected,
            channels: world.voiceConnected && world.voiceChannels.length > 0 ? world.voiceChannels : undefined,
            people: world.voiceConnected && world.voice.length > 0 ? world.voice : undefined,
        },
        people,
        fetchedAt: Date.now(),
        configured: world.configured,
        error: world.error || undefined,
        livePreviews: world.livePreviews,

        // Always there, as once the plugin is running; the tails only while
        // Clips are on, as the server stops them with it.
        clips: clipsInfoOf(world, now),
        clipTails: world.clipsEnabled && clipTailsOf(world, now).length > 0 ? clipTailsOf(world, now) : undefined,

        // Absent while off, as the server's omitempty sends them.
        twitchOn: twitchOn || undefined,
        twitchEmbed: twitchOn && world.twitchEmbed ? true : undefined,

        // Only while it is on and Mumble is connected.
        soundboard: world.soundboardOn && world.voiceConnected ? {v: String(world.soundV)} : undefined,
    };
}

export function meOf(): Me {
    const access = labAccessOf(world);
    const linked = access === 'linked';
    const admin = world.me.isAdmin;
    const alert = admin ? clipsAlertOf(world, Date.now()) : '';
    const {media, ...rest} = world.me;
    const every = admin && world.everyAlert;

    return {
        ...rest,
        access,
        configured: world.configured,
        linkedLive: linked && world.streams.some((item) => item.userId === ME_ID),

        // The planning form's lookups, Pop out and Unlink's words: for
        // somebody linked only, as the server sends them.
        ...(linked && media ? {media} : {}),
        ...(linked && world.publicWatch ? {popOut: true} : {}),
        ...(linked ? {alsoLetIn: world.letIn} : {}),

        // Twitch: there while the switch is on, and while Victor has an
        // account connected or a takeover notice even when it is off.
        ...twitchMeOf(world, admin),

        // Steam and Hytale, the same way (design/3.4.0.md 2.9).
        ...accountsMeOf(world, admin),

        // The streamer's switch, for everybody linked; the alerts, for
        // admins, in any access state.
        ...(linked ? {clipsStreamer: {
            allowed: world.streamerAllowed,
            ...(world.downloadsAllowed ? {downloads: !world.streamerNoDownloads} : {}),
        }} : {}),
        ...(alert ? {clipsAlert: alert} : {}),
        ...(every ? {
            letInAlert: 'The let-in list could not be read and was set aside. Nobody is let in until people are added again.',
            voiceAlert: 'connection refused.',
            birthdayAlert: 'the list is not valid JSON.',
            airtimeAlert: {missingSeconds: 5400, backupTaken: Date.now() - 3600000, pluginVersion: '3.0.0'},
        } : {}),

        // 3.2.0's admin lines (design/3.2.0.md 2.3).
        ...(admin && world.soundAlert ? {soundAlert: world.soundAlert} : {}),
        ...(admin && world.achievementsAlert ? {achievementsAlert: world.achievementsAlert} : {}),
        ...(admin && Object.keys(world.ledgerAlert).length > 0 ? {ledgerAlert: world.ledgerAlert} : {}),

        // The release and its alert (the Admin news row).
        ...labAdminMe(admin),
    };
}

export function profileOf(): Profile {
    return world.profile;
}

// ---------------------------------------------------------------- scenarios

// The 24/7 channel: nobody's stream, so no userId, and a picture an admin
// uploaded for it (the lab's own stand-in; nothing is built in). Its handle
// stands in for the one the server would make from its key.
export const RADIO_HANDLE = 'h-radio';

export function toggleRadio(): void {
    change((current) => {
        if (current.streams.some((item) => item.handle === RADIO_HANDLE)) {
            current.streams = current.streams.filter((item) => item.handle !== RADIO_HANDLE);
            return;
        }

        current.streams = current.streams.concat({
            ...stream('Lo-fi radio', ''),
            handle: RADIO_HANDLE,
            userId: undefined,
            username: undefined,
            displayName: undefined,
            alwaysOn: true,
            motd: 'Music around the clock.',
            pictureVersion: 'uploaded',
        });
    });
}

export function goLive(name: string, userId: string): void {
    change((current) => {
        current.streams = current.streams.concat(stream(name, userId));
    });
}

// What the server keeps of a stream the moment it leaves the list (spec
// 2.4.8): the end of its recording, for ten minutes, when there were five
// seconds or more of it — or else an end it already held from before a drop.
// Nothing for a stream that was not being recorded.
function endOf(current: World, item: Stream, now: number): LabTail | undefined {
    if (!current.clipsEnabled || item.alwaysOn) {
        return undefined;
    }

    const fields = clipFieldsOf(current, item, now);
    if (fields.clip === 'ready' && typeof fields.clipFrom === 'number' && now - fields.clipFrom >= 5000) {
        const nowPlaying = nowPlayingOf(item, current.schedule);

        return {
            handle: item.handle,
            title: item.title,
            ...(item.userId ? {userId: item.userId} : {}),
            reason: 'ended',
            endedAt: now,
            until: now + TAIL_MS,
            heldMs: Math.min(current.clipLength * 1000, now - fields.clipFrom),
            ...(nowPlaying ? {nowPlaying} : {}),
        };
    }

    return labTailsOf(current, now).find((held) => held.handle === item.handle);
}

function keepEnd(current: World, item: Stream, now: number): void {
    const end = endOf(current, item, now);
    if (end) {
        current.tails = current.tails.filter((held) => held.handle !== item.handle).concat(end);
    }
}

// Going off the air takes down what they said was on, as the server does once
// the stream has been gone for the offline grace — at once here, since the
// lab has no polls to count. The end of the stream is held for clipping, as
// the server holds it.
export function goOffline(handle: string): void {
    const now = Date.now();
    change((current) => {
        const leaving = current.streams.find((item) => item.handle === handle);
        if (leaving) {
            keepEnd(current, leaving, now);
        }
        current.streams = current.streams.filter((item) => item.handle !== handle);
        if (leaving?.userId && current.nowPlaying[leaving.userId]) {
            const {[leaving.userId]: _gone, ...rest} = current.nowPlaying;
            current.nowPlaying = rest;
        }
    });
}

// A stream dropping off the list and coming back under the same handle a few
// seconds later, as an OBS reconnect does. The tile waits for it, with the
// scissors for the end of it, and picks it up again with a fresh connection
// once it is back — when a new recording starts, and the end of the old one
// is still held, for its tile to clip "before the stream dropped".
export function dropFor(handle: string, ms: number): void {
    const leaving = world.streams.find((item) => item.handle === handle);
    if (!leaving) {
        return;
    }

    change((current) => {
        keepEnd(current, leaving, Date.now());
        current.streams = current.streams.filter((item) => item.handle !== handle);
    });
    window.setTimeout(() => change((current) => {
        if (!current.streams.some((item) => item.handle === handle)) {
            current.streams = current.streams.concat(leaving);
            current.restartedAt = {...current.restartedAt, [handle]: Date.now()};
        }
    }), ms);
}

export function plan(title: string, who: string, userId: string, inMs: number, category = ''): void {
    change((current) => {
        current.schedule = current.schedule.concat(planned(title, who, userId, inMs, category));
    });
}

export function unplan(id: string): void {
    change((current) => {
        current.schedule = current.schedule.filter((item) => item.id !== id);
    });
}

export const MINUTE_MS = MINUTE;
export const HOUR_MS = HOUR;

// ---------------------------------------------------------------- twitch

// Twitch streams can appear at all: the switch, with an application set up
// (the server's twitchLiveOn).
export function labTwitchOn(current: World): boolean {
    return current.twitchSwitch && current.twitchCredentials;
}

// How long a simulcaster's Twitch card waits before it is covered by their
// Broadcast Box card, or uncovered: the server waits OfflineGraceChecks polls
// (about 45 s); the lab, a few seconds, so it can be watched.
export const LAB_COVER_GRACE_MS = 5000;

// A Twitch stream as the server builds it (twitchStreams, 5.3): no stream
// key, a handle of its own, Twitch's title as the note, Twitch's category
// and whole audience, and the login that drives the player.
export function twitchStream(name: string, userId: string, options: Partial<Stream> = {}): Stream {
    return {
        handle: `tw-${userId.replace(/^u-/, '')}`,
        title: name,
        paused: false,
        alwaysOn: false,
        viewers: 0,
        bitrate: 0,
        videoTracks: 0,
        audioTracks: 0,
        userId,
        username: userId.replace(/^u-/, ''),
        displayName: name,
        linkSource: 'twitch',
        source: 'twitch',
        ...options,
    };
}

function nameOf(userId: string): string {
    const user = LAB_USERS.find((candidate) => candidate.id === userId);

    return user?.first_name || user?.nickname || user?.username || userId;
}

// This poll's Twitch entries: everybody live on Twitch who is connected and
// not blocked. The picture's version changes with the stream, as the
// server's does.
function twitchEntriesOf(current: World): Stream[] {
    return Object.entries(current.twitchLive).
        filter(([userId]) => current.twitchLinks[userId] && !current.twitchBlocked[userId]).
        map(([userId, live]) => {
            const link = current.twitchLinks[userId];

            return twitchStream(nameOf(userId), userId, {
                motd: live.title,
                twitchLogin: link.login,
                twitchCategory: live.category || undefined,
                viewers: live.viewers,
                streamStart: live.startedAt,
                alwaysOn: link.allDay,
                previewVersion: current.livePreviews ? `${Math.floor(Date.parse(live.startedAt) / 1000)}-lab` : undefined,
            });
        });
}

// mergeStreams, as the lab keeps it: Twitch entries after the attributed
// Broadcast Box streams and before the rest; a simulcaster's Twitch entry
// covered by their Broadcast Box card once the grace has passed, that card
// saying Also on Twitch. Copies throughout: the world's streams are not
// changed.
function mergedWithTwitch(current: World): Stream[] {
    const twitch = twitchEntriesOf(current).sort((a, b) => a.title.toLowerCase().localeCompare(b.title.toLowerCase()));
    if (twitch.length === 0) {
        return current.streams;
    }

    const bb = current.streams.map((item) => ({...item}));
    const entries = twitch.map((entry) => {
        if (entry.alwaysOn || !entry.userId || !current.twitchCovered[entry.userId]) {
            return entry;
        }

        // Covered, it stays covered by their Broadcast Box card through a
        // pause shorter than the grace: nothing swaps.
        const cover = bb.find((item) => item.userId === entry.userId && !item.alwaysOn);
        if (!cover) {
            return entry;
        }

        cover.alsoTwitch = entry.twitchLogin;

        return {...entry, coveredBy: cover.handle};
    });

    const first = bb.filter((item) => item.userId && !item.alwaysOn);
    const rest = bb.filter((item) => !item.userId || item.alwaysOn);

    return [...first, ...entries.filter((entry) => !entry.alwaysOn), ...rest, ...entries.filter((entry) => entry.alwaysOn)];
}

// Whether a person's Twitch entry should be covered: they have a Broadcast
// Box stream on the air that is not a 24/7 channel.
function coverWanted(current: World, userId: string): boolean {
    return current.streams.some((item) => item.userId === userId && !item.paused && !item.alwaysOn);
}

const coverTimers = new Map<string, number>();

// The cover's hysteresis (5.4 step 3): set at once the first time a
// person's Twitch entry is seen, and after that changed only once the
// evidence has disagreed with it for the whole grace. A pause shorter than
// the grace swaps nothing.
function reviewTwitchCover(): void {
    Object.keys(world.twitchCovered).forEach((userId) => {
        if (!world.twitchLive[userId]) {
            delete world.twitchCovered[userId];
        }
    });

    Object.keys(world.twitchLive).forEach((userId) => {
        const want = coverWanted(world, userId);
        if (world.twitchCovered[userId] === undefined) {
            world.twitchCovered[userId] = want;

            return;
        }
        if (world.twitchCovered[userId] === want || coverTimers.has(userId)) {
            return;
        }

        coverTimers.set(userId, window.setTimeout(() => {
            coverTimers.delete(userId);
            if (world.twitchLive[userId] && coverWanted(world, userId) !== world.twitchCovered[userId]) {
                change((current) => {
                    current.twitchCovered = {...current.twitchCovered, [userId]: coverWanted(current, userId)};
                });
            }
        }, LAB_COVER_GRACE_MS));
    });
}

// The boards by person: each person's Twitch seconds added to their row
// (a new row for somebody who streams only on Twitch), ranked again, with
// twitchSeconds for the hours cell's tooltip.
function withTwitchHours(rows: Leader[], twitch: Record<string, number>): Leader[] {
    const merged = rows.map((row) => ({...row}));
    Object.entries(twitch).forEach(([userId, seconds]) => {
        if (seconds <= 0) {
            return;
        }

        const row = merged.find((candidate) => candidate.userId === userId);
        if (row) {
            row.seconds += seconds;
            row.twitchSeconds = seconds;

            return;
        }

        const name = nameOf(userId);
        merged.push({rank: 0, title: name, seconds, userId, username: userId.replace(/^u-/, ''), displayName: name, twitchSeconds: seconds});
    });

    return merged.
        sort((a, b) => (b.seconds - a.seconds) || a.title.toLowerCase().localeCompare(b.title.toLowerCase())).
        map((row, index) => ({...row, rank: index + 1}));
}

// This month's share of each person's Twitch hours, for the month board.
function twitchMonthOf(twitch: Record<string, number>): Record<string, number> {
    return Object.fromEntries(Object.entries(twitch).map(([userId, seconds]) => [userId, Math.round(seconds * 0.3)]));
}

// me.twitch, as the server answers it (4.10).
function twitchMeOf(current: World, admin: boolean): {twitch?: Me['twitch']} {
    const link = current.twitchLinks[ME_ID];
    const lost = current.twitchLost;
    if (!current.twitchSwitch && !link && !lost) {
        return {};
    }

    const on = labTwitchOn(current);
    let problem = '';
    if (admin && current.twitchSwitch) {
        problem = current.twitchCallbackProblem || current.twitchProblem;
    }

    return {
        twitch: {
            available: on && current.twitchCallbackProblem === '',
            ...(link ? {login: link.login, displayName: link.name} : {}),
            ...(link && on && current.twitchLive[ME_ID] ? {live: true} : {}),
            ...(current.twitchBlocked[ME_ID] ? {blocked: true} : {}),
            ...(lost ? {lost} : {}),
            ...(problem ? {problem} : {}),
        },
    };
}

// ---------------------------------------------------------------- steam and hytale

// Steam statuses can be asked for, and Hytale connected: the switch with
// its secrets set (the server's steamOn and hytaleOn).
export function labSteamOn(current: World): boolean {
    return current.steamSwitch && current.steamKeySet;
}

export function labHytaleOn(current: World): boolean {
    return current.hytaleSwitch && current.hytaleCredentials;
}

// me.steam and me.hytale (meAccount, 2.9): present whenever that switch is
// on, and whenever Victor has a connection or a notice even while it is off;
// the problem for admins only, while it is on.
function accountsMeOf(current: World, admin: boolean): {steam?: Me['steam']; hytale?: Me['hytale']} {
    const answer: {steam?: Me['steam']; hytale?: Me['hytale']} = {};

    // "On" is the server's steamOn and hytaleOn: the switch with its secrets
    // set. A switch with no key is off, so it shows nothing but a connection
    // still to disconnect, and the admin is told on the System Console card.
    const steam = current.steamLinks[ME_ID];
    if (labSteamOn(current) || steam || current.steamLost) {
        let problem = '';
        if (admin && labSteamOn(current)) {
            problem = current.steamCallbackProblem || current.steamKeyProblem || current.steamProblem;
        }
        const status = current.steamStatus[ME_ID];
        answer.steam = {
            available: labSteamOn(current) && current.steamCallbackProblem === '',
            ...(steam ? {connected: true, name: steam.name, url: `https://steamcommunity.com/profiles/${steam.id}`} : {}),
            ...(steam && status && !status.public ? {hidden: true} : {}),
            ...(current.steamBlocked[ME_ID] ? {blocked: true} : {}),
            ...(current.steamLost ? {lost: current.steamLost} : {}),
            ...(problem ? {problem} : {}),
        };
    }

    const hytale = current.hytaleLinks[ME_ID];
    if (labHytaleOn(current) || hytale || current.hytaleLost) {
        let problem = '';
        if (admin && labHytaleOn(current)) {
            problem = current.hytaleCallbackProblem || current.hytaleProblem;
        }
        answer.hytale = {
            available: labHytaleOn(current) && current.hytaleCallbackProblem === '',
            ...(hytale ? {connected: true, name: hytale.name} : {}),
            ...(current.hytaleBlocked[ME_ID] ? {blocked: true} : {}),
            ...(current.hytaleLost ? {lost: current.hytaleLost} : {}),
            ...(problem ? {problem} : {}),
        };
    }

    return answer;
}

// GET /api/v1/accounts (2.9 and 6.4), from the world: everybody connected
// to a service that is on, but the blocked and the inactive, keys sorted as
// Go's map encoding sorts them; statuses only while the last read is fresh,
// a private profile as offline, and a game only for a public one. people is
// {} when nobody is, as the real answer is.
export function accountsViewOf(current: World): AccountsView {
    const steamOn = labSteamOn(current);
    const hytaleOn = labHytaleOn(current);
    const fresh = steamOn && current.steamProblem === '' && current.steamKeyProblem === '';
    const people: AccountsView['people'] = {};
    const shown = (userId: string, blocked: Record<string, number>) => !blocked[userId] && !current.inactive[userId];

    if (steamOn) {
        Object.entries(current.steamLinks).filter(([userId]) => shown(userId, current.steamBlocked)).forEach(([userId, link]) => {
            const status = current.steamStatus[userId];
            const open = Boolean(status?.public);
            // Somebody Steam's last good answer left out has no status at
            // all, as on the server: '' rather than a guess.
            let state: AccountsSteamState = '';
            if (fresh && status) {
                state = open ? status.state : 'offline';
            }
            const playing = fresh && open && status?.game !== undefined;
            people[userId] = {
                ...people[userId],
                steam: {
                    name: link.name,
                    url: `https://steamcommunity.com/profiles/${link.id}`,
                    state,
                    ...(playing ? {inGame: true, game: status?.game || ''} : {}),
                },
            };
        });
    }
    if (hytaleOn) {
        Object.entries(current.hytaleLinks).filter(([userId]) => shown(userId, current.hytaleBlocked)).forEach(([userId, link]) => {
            people[userId] = {...people[userId], hytale: {name: link.name}};
        });
    }

    const sorted = Object.fromEntries(Object.keys(people).sort().map((userId) => [userId, people[userId]]));
    const text = JSON.stringify(sorted);
    let hash = 0;
    for (let index = 0; index < text.length; index++) {
        hash = ((hash * 31) + text.charCodeAt(index)) >>> 0;
    }

    return {
        v: hash.toString(16).padStart(12, '0').slice(0, 12),
        steam: steamOn ? {on: true, fresh} : {on: false, fresh: false},
        hytale: {on: hytaleOn},
        people: sorted,
    };
}

// Victor's own Steam, for the lab's row: not connected; connected and
// online, playing, playing a game with a long name, offline, with a private
// profile; his account taken over by somebody else (the notice); blocked.
export type LabVictorSteam = 'none' | 'online' | 'playing' | 'long' | 'offline' | 'private' | 'lost' | 'blocked';

export const VICTOR_STEAM_NAME = 'VictorPlays';

// Sixteen characters, the longest name 304 px must hold whole.
export const VICTOR_HYTALE_NAME = 'VictorTheBuilder';

export function victorSteamLink(): LabAccountLink {
    return {id: '76561198012345678', name: VICTOR_STEAM_NAME, at: Date.now()};
}

export function victorHytaleLink(): LabAccountLink {
    return {id: '9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d', name: VICTOR_HYTALE_NAME, at: Date.now()};
}

export function labVictorSteamOf(current: World): LabVictorSteam {
    if (current.steamBlocked[ME_ID]) {
        return 'blocked';
    }
    if (current.steamLost) {
        return 'lost';
    }
    if (!current.steamLinks[ME_ID]) {
        return 'none';
    }

    const status = current.steamStatus[ME_ID];
    if (!status?.public) {
        return 'private';
    }
    if (status.game === 'ELDEN RING') {
        return 'playing';
    }
    if (status.game !== undefined) {
        return 'long';
    }

    return status.state === 'offline' ? 'offline' : 'online';
}

const VICTOR_STEAM_STATUS: Record<'online' | 'playing' | 'long' | 'offline' | 'private', LabSteamStatus> = {
    online: {state: 'online', public: true},
    playing: {state: 'online', game: 'ELDEN RING', public: true},
    long: {state: 'online', game: 'Warhammer 40,000: Space Marine 2', public: true},
    offline: {state: 'offline', public: true},
    private: {state: 'offline', public: false},
};

export function setVictorSteam(mode: LabVictorSteam): void {
    change((current) => {
        const {[ME_ID]: _link, ...links} = current.steamLinks;
        const {[ME_ID]: _status, ...status} = current.steamStatus;
        const {[ME_ID]: _blocked, ...blocked} = current.steamBlocked;
        const connected = mode !== 'none' && mode !== 'lost' && mode !== 'blocked';
        current.steamLinks = connected ? {...links, [ME_ID]: current.steamLinks[ME_ID] || victorSteamLink()} : links;
        current.steamStatus = connected ? {...status, [ME_ID]: VICTOR_STEAM_STATUS[mode as keyof typeof VICTOR_STEAM_STATUS]} : status;
        current.steamBlocked = mode === 'blocked' ? {...blocked, [ME_ID]: Date.now()} : blocked;
        current.steamLost = mode === 'lost' ? {name: VICTOR_STEAM_NAME, at: Date.now() - (2 * HOUR)} : null;
    });
}

// Victor's own Hytale: not connected, connected (as VictorTheBuilder), his
// account taken over, or blocked.
export type LabVictorHytale = 'none' | 'connected' | 'lost' | 'blocked';

export function labVictorHytaleOf(current: World): LabVictorHytale {
    if (current.hytaleBlocked[ME_ID]) {
        return 'blocked';
    }
    if (current.hytaleLost) {
        return 'lost';
    }

    return current.hytaleLinks[ME_ID] ? 'connected' : 'none';
}

export function setVictorHytale(mode: LabVictorHytale): void {
    change((current) => {
        const {[ME_ID]: _link, ...links} = current.hytaleLinks;
        const {[ME_ID]: _blocked, ...blocked} = current.hytaleBlocked;
        current.hytaleLinks = mode === 'connected' ? {...links, [ME_ID]: current.hytaleLinks[ME_ID] || victorHytaleLink()} : links;
        current.hytaleBlocked = mode === 'blocked' ? {...blocked, [ME_ID]: Date.now()} : blocked;
        current.hytaleLost = mode === 'lost' ? {name: VICTOR_HYTALE_NAME, at: Date.now() - HOUR} : null;
    });
}

// Other people's Steam, for the popover's rows and Playing now (design/3.4.0.md
// 8.3 items 9 and 10): Dave's status in each of the states his row has; the
// line under In voice with nobody in a game, one, the two the lab starts
// with, or eight, wider than any panel.
export type LabOthersSteam = 'elden' | 'thirty' | 'sixty' | 'nameless' | 'online' | 'offline';

// A game name of 30 characters and one of 60, the lengths P2 measured.
export const GAME_30 = 'Warhammer 40,000: Darktide 30';
export const GAME_60 = 'The Elder Scrolls V: Skyrim Special Edition Anniversary Ed.';

const DAVE_STEAM: Record<LabOthersSteam, LabSteamStatus> = {
    elden: {state: 'online', game: 'ELDEN RING', public: true},
    thirty: {state: 'online', game: GAME_30, public: true},
    sixty: {state: 'busy', game: GAME_60, public: true},
    nameless: {state: 'online', game: '', public: true},
    online: {state: 'online', public: true},
    offline: {state: 'offline', public: true},
};

export function setDaveSteam(mode: LabOthersSteam): void {
    change((current) => {
        current.steamStatus = {...current.steamStatus, 'u-dave': DAVE_STEAM[mode]};
    });
}

// Three more people with Steam, so eight can be in a game at once: lab
// people Mattermost has not loaded, whose names the panel has to ask for
// (the fake server's /users/ids answers for them).
export const LAB_STEAM_FRIENDS: MattermostUser[] = [
    {id: 'u-frank', username: 'frank', first_name: 'Frank', last_name: 'Harper'},
    {id: 'u-grace', username: 'grace', first_name: 'Grace', last_name: 'Ward'},
    {id: 'u-sybil', username: 'sybil', first_name: 'Sybil'},
];

export type LabPlaying = 'nobody' | 'one' | 'two' | 'eight';

const EIGHT_GAMES: Record<string, string> = {
    [ME_ID]: 'Arma Reforger',
    'u-dave': 'ELDEN RING',
    'u-bob': 'Warhammer 40,000: Space Marine 2',
    'u-erin': 'Hades II',
    'u-carol': 'Counter-Strike 2',
    'u-frank': 'Deep Rock Galactic',
    'u-grace': 'Baldur’s Gate 3',
    'u-sybil': 'Valheim',
};

export function setPlaying(mode: LabPlaying): void {
    change((current) => {
        const day = 24 * HOUR;
        const links = {...current.steamLinks};
        const status: Record<string, LabSteamStatus> = {};
        Object.keys(links).forEach((userId) => {
            status[userId] = {state: 'online', public: true};
        });
        if (mode === 'one') {
            status['u-dave'] = {state: 'online', game: 'ELDEN RING', public: true};
        } else if (mode === 'two') {
            status['u-dave'] = {state: 'online', game: 'ELDEN RING', public: true};
            status['u-bob'] = {state: 'online', game: 'Hades', public: true};
        } else if (mode === 'eight') {
            Object.entries(EIGHT_GAMES).forEach(([userId, game], index) => {
                links[userId] = links[userId] || {id: `7656119800000000${index}`, name: `${userId.slice(2)}_on_steam`, at: Date.now() - day};
                status[userId] = {state: 'online', game, public: true};
            });
        }
        if (mode !== 'eight') {
            LAB_STEAM_FRIENDS.forEach((user) => {
                delete links[user.id];
                delete status[user.id];
            });
        }
        current.steamLinks = links;
        current.steamStatus = status;
    });
}

// The lab's Twitch row.

export function setTwitch(apply: (current: World) => void): void {
    change(apply);
}

// Victor's own Twitch: not connected, connected (as VictorPlays), his
// account taken over by somebody else (the notice), or blocked.
export type LabVictorTwitch = 'none' | 'connected' | 'lost' | 'blocked';

export function labVictorTwitchOf(current: World): LabVictorTwitch {
    if (current.twitchBlocked[ME_ID]) {
        return 'blocked';
    }
    if (current.twitchLost) {
        return 'lost';
    }

    return current.twitchLinks[ME_ID] ? 'connected' : 'none';
}

export function setVictorTwitch(mode: LabVictorTwitch): void {
    change((current) => {
        const {[ME_ID]: _link, ...links} = current.twitchLinks;
        const {[ME_ID]: _blocked, ...blocked} = current.twitchBlocked;
        current.twitchLinks = mode === 'connected' ? {...links, [ME_ID]: victorTwitchLink()} : links;
        current.twitchBlocked = mode === 'blocked' ? {...blocked, [ME_ID]: Date.now()} : blocked;
        current.twitchLost = mode === 'lost' ? {login: 'victorplays', at: Date.now() - (2 * HOUR)} : null;
        if (mode !== 'connected') {
            const {[ME_ID]: _live, ...live} = current.twitchLive;
            current.twitchLive = live;
        }
    });
}

export function victorTwitchLink(): LabTwitchLink {
    return {login: 'victorplays', name: 'VictorPlays', at: Date.now(), allDay: false};
}

const TWITCH_LIVE: Record<string, LabTwitchLive> = {
    'u-erin': {title: 'Sunday racing practice — come and heckle my braking #simracing', category: 'Assetto Corsa Competizione', viewers: 1234, startedAt: ''},
    [ME_ID]: {title: 'Arma most nights, on Twitch too #arma3', category: 'Arma 3', viewers: 87, startedAt: ''},
    'u-carol': {title: 'Just chatting before the raid', category: '', viewers: 0, startedAt: ''},
};

export function isTwitchLive(current: World, userId: string): boolean {
    return Boolean(current.twitchLive[userId]);
}

// Somebody going live on Twitch, or stopping. Victor needs an account
// connected first; going live connects him, as the lab's shortcut.
export function toggleTwitchLive(userId: string): void {
    change((current) => {
        if (current.twitchLive[userId]) {
            const {[userId]: _gone, ...rest} = current.twitchLive;
            current.twitchLive = rest;

            return;
        }

        if (!current.twitchLinks[userId] && userId === ME_ID) {
            current.twitchLinks = {...current.twitchLinks, [ME_ID]: victorTwitchLink()};
            current.twitchBlocked = Object.fromEntries(Object.entries(current.twitchBlocked).filter(([id]) => id !== ME_ID));
            current.twitchLost = null;
        }
        current.twitchLive = {...current.twitchLive, [userId]: {...TWITCH_LIVE[userId], startedAt: new Date(Date.now() - (52 * MINUTE)).toISOString()}};
    });
}

// Victor live on Broadcast Box and Twitch at once: one card, his Broadcast
// Box one, with Also on Twitch.
export function victorSimulcast(): void {
    if (!world.streams.some((item) => item.userId === ME_ID)) {
        goLive('Victor', ME_ID);
    }
    if (!world.twitchLive[ME_ID]) {
        toggleTwitchLive(ME_ID);
    }
}

// Erin's Broadcast Box stream, while she is live on Twitch: the handoff.
export function toggleErinBroadcastBox(): void {
    const live = world.streams.find((item) => item.userId === 'u-erin' && !item.alwaysOn);
    if (live) {
        goOffline(live.handle);
    } else {
        goLive('Erin', 'u-erin');
    }
}

// Erin's Broadcast Box stream paused for about one poll, and back: shorter
// than the grace, so nothing swaps.
export function pauseErinOnePoll(): void {
    const live = world.streams.find((item) => item.userId === 'u-erin' && !item.alwaysOn);
    if (!live) {
        return;
    }

    const setPaused = (paused: boolean) => change((current) => {
        current.streams = current.streams.map((item) => (item.handle === live.handle ? {...item, paused} : item));
    });
    setPaused(true);
    window.setTimeout(() => setPaused(false), 2000);
}

// The Twitch ledger's state, for the System Console.
export type LabTwitchLedger = 'ok' | 'halted' | 'purging' | 'setaside';

export function setTwitchLedger(mode: LabTwitchLedger): void {
    change((current) => {
        current.twitchHalted = mode === 'halted' ? 'twitchair_month_2026-09 could not be read and was set aside (quarantine_twitchair_month_2026-09).' : '';
        current.twitchPurging = mode === 'purging' ? 1 : 0;
        current.twitchQuarantined = mode === 'setaside' || mode === 'halted';
    });
}

export function labTwitchLedgerOf(current: World): LabTwitchLedger {
    if (current.twitchHalted) {
        return 'halted';
    }
    if (current.twitchPurging > 0) {
        return 'purging';
    }

    return current.twitchQuarantined ? 'setaside' : 'ok';
}

// ---------------------------------------------------------------- chat

// Mattermost itself, as far as the hashtag search sees it: one team, its
// people and channels, and messages with hashtags in them.
//
// The lab's copy of Mattermost's store (lab/mattermost.ts) has only some of
// these loaded, as a real one would: Erin and Carol are nobody Victor has
// talked to yet, and the group message and the archived channel are not in
// the sidebar, so the search has to ask for them — which is the path the
// fake server's users and channels answer.
export const LAB_TEAM = {id: 't-myteam', name: 'myteam', display_name: 'My Team'};

export const LAB_USERS: MattermostUser[] = [
    {id: ME_ID, username: 'victor', first_name: 'Victor'},
    {id: 'u-dave', username: 'dave', first_name: 'Dave', last_name: 'Hill'},
    {id: 'u-bob', username: 'bob', first_name: 'Bob'},
    {id: 'u-erin', username: 'erin', first_name: 'Erin', last_name: 'Pemberton-Whitfield'},
    {id: 'u-carol', username: 'carol', nickname: 'Caro'},
    {id: 'u-hooks', username: 'status-hook'},
];

export const LOADED_USERS = [ME_ID, 'u-dave', 'u-bob', 'u-hooks'];

export const LAB_CHANNELS: MattermostChannel[] = [
    {id: 'c-town', team_id: LAB_TEAM.id, type: 'O', name: 'town-square', display_name: 'Town Square'},
    {id: 'c-arma', team_id: LAB_TEAM.id, type: 'O', name: 'arma', display_name: 'Arma night'},
    {id: 'c-crew', team_id: LAB_TEAM.id, type: 'P', name: 'crew', display_name: 'Crew'},
    {id: 'c-dm-dave', team_id: '', type: 'D', name: `u-dave__${ME_ID}`, display_name: ''},
    {id: 'c-group', team_id: '', type: 'G', name: 'f3a9c1', display_name: 'bob, dave, victor'},
    {id: 'c-old', team_id: LAB_TEAM.id, type: 'O', name: 'arma-2024', display_name: 'Arma 2024', delete_at: Date.now() - (90 * 24 * HOUR)},
];

export const LOADED_CHANNELS = ['c-town', 'c-arma', 'c-crew', 'c-dm-dave'];

const AT = Date.now();
let postId = 0;

function said(channelId: string, userId: string, minutesAgo: number, message: string, more: Partial<MattermostPost> = {}): MattermostPost {
    postId += 1;

    return {
        id: `p-${postId}`,
        create_at: AT - (minutesAgo * MINUTE),
        user_id: userId,
        channel_id: channelId,
        root_id: '',
        message,
        type: '',
        props: {},
        ...more,
    };
}

// Enough #milsim for three pages, so "Load more" can be pressed after the
// second, which the results card asks for by itself. One on the first page is
// a message deleted since it was indexed, which a server searching with
// Elasticsearch or Bleve drops after cutting the page, so that page comes back
// one short and must not be taken for the last (see the mock's searchPosts).
const MILSIM_LINES = [
    'Slots for tonight are up, sign up in the sheet #milsim',
    'Who has the radio pack? #milsim',
    '#milsim briefing at 19:30, mods updated',
    'Good op last night everyone #MilSim',
    'Bring your own map markers (#milsim)',
    'Anyone fancy a #milsim training run on Sunday?',
    'New ACE build is out, check your launcher before #milsim',
];
const MILSIM_PEOPLE = ['u-dave', 'u-bob', ME_ID, 'u-erin', 'u-carol'];
const MILSIM_ROOMS = ['c-arma', 'c-town', 'c-crew'];

export const LAB_POSTS: MattermostPost[] = [
    said('c-arma', 'u-dave', 4, 'Server is up, ALiVE campaign tonight #arma3 #milsim\nBring a radio.'),
    said('c-arma', 'u-bob', 3, 'On my way, saving a seat in the heli #arma3', {root_id: 'p-1'}),
    said('c-dm-dave', 'u-dave', 50, 'Can you check the #arma3 modpack before tonight? Something is off with **ACE**.'),
    said('c-group', 'u-bob', 26 * 60, 'Next week: #Arma3 or #soulslike night? Vote below'),
    said('c-old', 'u-dave', 400 * 24 * 60, 'Last op of the year, thanks all #arma3'),
    said('c-town', 'u-hooks', 95, 'Game server restarted: #arma3 is back online', {props: {from_webhook: 'true', override_username: 'Server status'}}),
    said('c-town', 'u-carol', 3 * 24 * 60, 'Tips are in the wiki: https://example.com/wiki#arma3 — and the #arma3 channel has the rest'),
    said('c-crew', 'u-erin', 9 * 24 * 60, '#soulslike co-op tonight, no summons except me'),
    said('c-town', ME_ID, 40 * 24 * 60, 'Clip from last night #soulslike'),
    ...Array.from({length: 46}, (_, index) => said(
        MILSIM_ROOMS[index % MILSIM_ROOMS.length],
        MILSIM_PEOPLE[index % MILSIM_PEOPLE.length],
        30 + (index * 7 * 60),
        MILSIM_LINES[index % MILSIM_LINES.length],
        index === 3 ? {props: {labDeleted: true}} : {},
    )),
];
