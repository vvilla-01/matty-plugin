export type Stream = {
    // The real stream key never reaches the browser; this opaque handle
    // identifies a stream and the plugin maps it back server-side.
    handle: string;
    title: string;
    motd?: string;
    paused: boolean;
    alwaysOn: boolean;
    viewers: number;
    bitrate: number;
    streamStart?: string;
    videoTracks: number;
    audioTracks: number;
    userId?: string;
    username?: string;
    displayName?: string;
    linkSource?: string;

    // The badges beside the streamer's name on the card, in the order they
    // are drawn, as the server decides them: the person's own choice with
    // this card's places on the boards, or 3.1.0's card set (the crown, the
    // calendar and the Top Snooper's binoculars). Absent when there are none.
    awards?: BesideAward[];

    // A 24/7 channel with a picture of its own: which version, so the panel
    // can load it by handle. Absent on a person's stream, which wears their
    // profile picture instead.
    pictureVersion?: string;

    // A 24/7 channel's wide card picture, set by an admin, in place of a live
    // preview.
    thumbnailVersion?: string;

    // The latest preview still of a person's live stream, and when it was
    // taken (RFC 3339). Absent until somebody's browser has captured one.
    stillVersion?: string;
    stillAt?: string;

    // Who is watching it in the panel, by name, while the "Show who is
    // watching" setting is on. viewers can be higher: somebody watching some
    // other way has no face to show.
    watchers?: Array<{userId: string; who: string}>;

    // How many more are watching than the faces sent: people the plugin
    // cannot name, and any past the first twenty. Sent with watchers.
    others?: number;

    // What is on it: the movie, show or game the streamer said with What's
    // on?, or else the one linked to their plan that is on now. Decided by
    // the server, once, so the card, the line under the picture and the
    // profile all show the same thing. Never on a 24/7 channel.
    nowPlaying?: NowPlaying;

    // Clips, while they are switched on and this is not a 24/7 channel: how
    // the server's recording of the stream is doing, since when it has held
    // an unbroken stretch (ms, only while ready), and until when the end of
    // an earlier stretch of it is still held (ms), after a drop. What the
    // scissors on its tile show is worked out from these (clipLogic.ts); the
    // server decides again when somebody presses them.
    clip?: ClipState;
    clipFrom?: number;
    clipTail?: number;

    // Where it is: absent for Broadcast Box, 'twitch' for Twitch
    // (design/twitch-live.md 5.3). A Twitch stream plays in Twitch's own
    // player, in a frame of player.twitch.tv addressed by twitchLogin, never
    // over WHEP; it has no stream key, no stills, no faces and no clips.
    source?: 'twitch';

    // For a Twitch stream: whose channel (a public name the server has
    // checked, ^[a-z0-9_]{1,25}$, and checks again here before it builds an
    // address from it), the category Twitch lists it under, and which of the
    // server's relayed preview pictures is current.
    twitchLogin?: string;
    twitchCategory?: string;
    previewVersion?: string;

    // On a Broadcast Box card: this person is also live on Twitch, at this
    // login. Their Twitch card is left out of Live now in favour of this one,
    // which says "Also on Twitch".
    alsoTwitch?: string;

    // On a Twitch entry: left out of Live now in favour of this Broadcast Box
    // card (its handle). Only what lists cards — one per person — skips it:
    // the watch list, its tiles and the theatre work by handle, so a Twitch
    // tile somebody is watching keeps playing when its card is covered.
    coveredBy?: string;
};

// What somebody is streaming right now, as a kind and a title, with the real
// movie, show or game when one was picked and lookups found it.
export type NowPlaying = {
    // 'movie' or 'game'. A plain string, like a plan's category.
    category: string;
    title: string;
    media?: MediaInfo;
};

export type Leader = {
    rank: number;
    title: string;
    seconds: number;
    userId?: string;
    username?: string;
    displayName?: string;

    // Of seconds, how many were live on Twitch: the boards rank people, their
    // Broadcast Box time and their Twitch time together, each second once
    // (design/twitch-live.md 7.8). Absent when none were.
    twitchSeconds?: number;
};

// The months somebody finished on the monthly podium, for their profile and
// the trophy menu. Each list is "YYYY-MM" months in UTC, oldest first, and only
// months that have ended: the one in progress is already on the board as it
// stands. Worked out by the server from the same order the board used, and
// attributed to whoever the stream key is linked to now. Named the way a
// Leader is, so the menu can list everybody without a lookup of its own.
export type MonthMedals = {
    userId: string;
    username?: string;
    displayName?: string;
    gold?: string[];
    silver?: string[];
    bronze?: string[];
};

// One person connected to the voice server (contract/README.md, "Voice").
// session, channel, deaf and phone come from 4.3.0; channel absent is the
// root, 0.
export type VoicePerson = {
    session?: number;
    name: string;
    userId?: string;
    channel?: number;
    muted?: boolean;
    deaf?: boolean;
    phone?: boolean;
};

// One channel of the voice server's tree, from 4.3.0. parent is missing for
// the root, id 0, and, from 4.4.0, for a shown channel whose parent is not
// shown under Channels to show (the root then not being in the tree); max
// only where the channel has a limit of its own.
export type VoiceChannel = {
    id: number;
    name: string;
    parent?: number;
    depth: number;
    max?: number;
};

export type Voice = {
    connected: boolean;

    // In the order to draw them: a channel before the ones under it.
    channels?: VoiceChannel[];
    people?: VoicePerson[];
};

// One person in the snapshot's index (server/people.go): what the cards, the
// voice roster and the badges say about them, with the rules already applied.
// Every field is absent when it has nothing to say.
export type Person = {

    // The handle of the card that stands for them while they are on the air:
    // never a 24/7 channel, a paused stream or a covered entry, and their
    // Broadcast Box stream before their Twitch one.
    live?: string;

    // 'open', or 'muted' when every voice session of theirs is.
    voice?: string;

    // The custom status the plugin is holding for them: 'live' or 'birthday'.
    badge?: string;

    // The awards drawn beside their name, in order, at most four: their own
    // choice, or Automatic. Decided by the server (awards_decide.go).
    awards?: BesideAward[];
};

// ---- Awards, as the server decides them (contract/README.md, "Awards and
// the boards") ----

export type AwardKind = 'alltime' | 'month' | 'snooper' | 'clipper' | 'past' | 'fan' | 'ach';

// One award as it is drawn beside a name or on a card. The id says which
// award, never the place; kind picks the artwork, with the place (the crown
// and the calendars, 1 to 3; a past month's best) or the achievement's family
// and step; label is its whole name, for a screen reader and a tooltip ("1st
// for airtime, all time").
export type BesideAward = {
    id: string;
    kind: AwardKind;
    rank?: number;
    family?: string;
    tier?: number;
    label: string;
};

// One award somebody holds now, with what the picker says of it (GET
// /api/v1/awards/mine).
export type HeldAward = BesideAward & {

    // A past month's medal: the months won, and on the podium at all.
    wins?: number;
    months?: number;

    // A biggest fan: whose, and for how long.
    streamer?: string;
    hours?: number;

    // The words on its chip in the picker.
    short: string;
};

// One row of somebody's record, as their profile shows it. Only the facts of
// its own kind are there, and none of them is formatted: seconds are seconds
// and months are "YYYY-MM", newest first.
export type AwardRow = {
    kind: 'board' | 'clipper' | 'fan' | 'achievements' | 'months';

    // alltime, month, snooper, clipper, fan:<streamer id>, achievements, past.
    id: string;

    // The place that tints the row and picks its artwork: 1 to 3, and 0 for
    // the achievements, which have none.
    rank: number;
    title: string;

    // A place on a board: which, how long, and what the time was spent on
    // when it was not streaming.
    board?: string;
    seconds?: number;
    doing?: string;

    // The Best Clipper's score.
    upvotes?: number;
    clips?: number;

    // A biggest fan: of whom, and for how long.
    streamerId?: string;
    streamer?: string;
    hours?: number;

    // The achievements, one step a family, in catalogue order.
    steps?: Array<{id: string; tier: number; name: string}>;

    // The months already over: the ones won, and second and third places.
    gold?: string[];
    tallies?: Array<{rank: number; months: string[]}>;
};

// GET /api/v1/boards: the boards, the medals, the fans and the achievements,
// which rode the snapshot until 3.6.0 under these same names, and everybody's
// record. Read when somebody looks (boards.ts); the snapshot says when
// anything in it but a board's seconds has changed (boards.version).
export type Boards = {
    version: string;

    allTime?: Leader[];
    month?: Leader[];
    monthLabel?: string;
    allTimeTotal?: number;
    monthTotal?: number;

    // Past monthly podiums, one entry per person with at least one. Sent only
    // while the leaderboard is on.
    monthMedals?: MonthMedals[];

    // The Top Snoopers boards: time spent watching other people's streams in
    // the panel, this UTC month (under monthLabel) and all time, counted only
    // for people with a linked stream. Plain Leaders, ranked by the server.
    // watchBoards says the setting is on and the boards could be read, so an
    // empty Snoopers tab can say nobody has watched yet; all of these are
    // absent while it is off.
    watchBoards?: boolean;
    watchMonth?: Leader[];
    watchAllTime?: Leader[];
    watchMonthTotal?: number;
    watchAllTimeTotal?: number;

    // There while Clips are on and somebody holds it.
    bestClipper?: BestClipper;

    // Each linked streamer's biggest fan, sorted by streamer id, and whether
    // biggest fans are on at all (the setting and Top Snoopers). Counted only
    // while the cards show who is watching (Victor's answer to Q1), and from
    // 3.2.1 nobody can opt out (design/3.2.1.md 2).
    fans?: FanView[];
    fansOn?: boolean;

    // The achievement families that are on, in catalogue order, and who holds
    // which step of each, sorted by user id.
    achievementKinds?: AchievementKind[];
    achievements?: AchievementHolder[];

    // Everybody's record, by user id, for everybody who holds anything. {}
    // when nobody does.
    people: Record<string, {rows: AwardRow[]}>;
};

export type ScheduleItem = {
    id: string;
    title: string;
    startsAt: number;

    // 'movie', 'game' or absent. Deliberately a plain string: it comes off the
    // wire, and anything the client does not recognise shows no icon.
    category?: string;

    userId?: string;
    who: string;

    // The real movie, show or game the plan is for, when the streamer picked
    // one while planning. Looked up and written by the server; see MediaInfo.
    media?: MediaInfo;

    // Other streamers who have said they are in: people with a linked stream
    // of their own, in the order they joined. Never includes the host.
    joined?: Array<{userId: string; who: string}>;

    // No room for anybody else: the join the server would refuse, by its own
    // count of the places taken (server/community.go, scheduleFull). Never
    // worked out here from the names above, which leave out anybody whose
    // account has gone and who still holds a place.
    full?: boolean;
};

// A movie, show or game as the server looked it up — from TMDB, Steam or IGDB,
// on the server, never from the browser. Everything here is plain text the
// server wrote, rendered as text; the link is one the server built.
export type MediaInfo = {
    // tmdb:movie:949, tmdb:tv:1396, steam:1245620, igdb:113360. What the art is
    // asked for by.
    ref: string;

    // 'movie', 'tv' or 'game'. A plain string, like category.
    kind: string;
    title: string;
    year?: string;
    overview?: string;

    // One short line: "Crime · Thriller · 2h 50m".
    facts?: string;

    // IMDb, TMDB, the Steam store or IGDB.
    link?: string;

    // 'TMDB', 'Steam' or 'IGDB', for the attribution TMDB and IGDB ask for.
    source?: string;
};

// One suggestion while typing a title.
export type MediaResult = {
    ref: string;
    kind: string;
    title: string;
    year?: string;

    // "Movie", "TV series", "Game".
    label?: string;
};

export type Snapshot = {
    streams: Stream[];

    // When to read the boards again (GET /api/v1/boards, boards.ts): a word
    // that moves when anything in them changes other than a board's seconds.
    // The boards themselves, the medals, the fans and the achievements rode
    // the snapshot until 3.6.0; what is decided from them, which award goes
    // beside whose name, is in people and on each card.
    boards?: {version: string};

    // When to read GET /api/v1/banners again (contract/README.md, "Profile
    // banners"), from 3.7.0. The panel reads nothing of banners: they are
    // Matty's.
    banners?: {version: string};
    schedule?: ScheduleItem[];
    voice?: Voice;

    // The same people, by user id (server/people.go). {} when nobody is live,
    // in voice or wearing a badge; only a snapshot from before the first poll
    // (the panel's own empty one) has none.
    people?: Record<string, Person>;
    fetchedAt: number;
    configured: boolean;
    error?: string;

    // Whether live streams' cards show preview stills at all (a setting).
    livePreviews?: boolean;

    // Clips. clips is there once the plugin is running, whether or not they
    // are switched on, and carries counts, never a title. clipTails are the
    // ends of streams that went off, still held for clipping, newest first.
    // (Who holds Best Clipper is with the boards.)
    clips?: ClipsInfo;
    clipTails?: ClipTail[];

    // Twitch: whether Twitch streams can appear at all (Show people live on
    // Twitch, with an application set up), and whether they may play inside
    // the panel (Play Twitch streams inside the panel). Here rather than on
    // "me" because it is the same for everybody and has to reach open panels
    // when an admin changes it. Absent while off.
    twitchOn?: boolean;
    twitchEmbed?: boolean;

    // The soundboard: there only while it is switched on and the panel is
    // connected to Mumble. v changes when the board does and says nothing
    // about it; an open sheet asks for the board again when it moves.
    soundboard?: {v: string};
};

// A streamer's biggest fan: the linked viewer who has watched them most in the
// panel, all time, once it is an hour. Names resolved by the server now; hours
// are whole hours, rounded down, so the figure moves at most once an hour.
export type FanView = {
    streamerId: string;
    streamer: string;
    fanId: string;
    fan: string;
    fanUsername?: string;
    hours: number;
};

// An achievement family: its id ("streamed", "snooped", "podium", "thumbs", or
// "ch-" and twelve hex for a 24/7 channel), its name as an admin wrote it for a
// channel, what it counts, and the steps, lowest first.
export type AchievementKind = {
    id: string;
    name: string;
    unit: 'hours' | 'months' | 'thumbs';
    tiers: number[];

    // What each step is called, lowest first, as the server words it:
    // "Streamed 100 hours", "On the podium 1 month", "Lo-fi radio: 10 hours".
    names: string[];
};

// What one person holds: for each family, how many of its steps (1-based).
// Their names ride along for the Achievements tab (an approved departure from
// design/3.2.0.md 5.3).
export type AchievementHolder = {
    userId: string;
    displayName?: string;
    username?: string;
    have: Array<{id: string; tier: number}>;
};

// GET /api/v1/awards/mine: the reader's own choice (null for Automatic), and
// whether the document behind it could be read.
export type AwardsMine = {
    show: string[] | null;
    readable: boolean;

    // Everything the reader holds now, in the order the picker lists it,
    // which is what a choice picks from.
    held: HeldAward[];
};

// GET /api/v1/takebacks: what the reader's own profile popover may offer to
// take back, open to everybody signed in: from 3.2.1 only their sounds, as
// there is no biggest-fan opt-out any more.
export type Takebacks = {
    sounds: number;
};

// GET /api/v1/achievements/mine: how far the reader is along each family.
// value is whole hours, months or thumbs; next is 0 once every step is reached.
export type AchievementProgress = {
    progress: Array<{id: string; value: number; next: number}>;
};

// Admins only, on "me" (design/3.2.0.md 2.3): a fan or 24/7 channel ledger
// that was started again from zero since the plugin last started (ms).
export type LedgerAlert = {
    fansResetAt?: number;
    channelsResetAt?: number;
};

export type AirtimeAlert = {
    missingSeconds: number;
    backupTaken: number;
    pluginVersion: string;
    halted?: string;
};

export type AirtimeStatus = {
    pluginVersion: string;
    recording: boolean;
    readable: boolean;
    recordedSeconds: number;
    streamers: number;
    months: number;
    halted: boolean;
    haltedReason?: string;
    quarantined?: string[];
    hasBackup: boolean;
    backupSavedAt?: number;
    backupVersion?: string;
    backupSeconds?: number;
    backupStreamers?: number;
    backupAheadSeconds: number;

    // Show people live on Twitch is on: Twitch hours are kept apart from
    // these figures, the export and the backup (design/twitch-live.md 7.12).
    twitchSeparate?: boolean;

    // 3.2.0 (design/3.2.0.md 2.2): when a fan or 24/7 channel ledger was
    // started again from zero since the plugin last started (ms).
    fansResetAt?: number;
    channelsResetAt?: number;
};

// What this server calls the plugin and which artwork it has (name.ts),
// whether the plugin is a signed release, and what the app check found for
// the reader's session: in GET /api/v1/access and GET /api/v1/me alike
// (contract/README.md, revision 4).
//
// artwork: whether there is an emblem and a No Signal ghost to draw; the
// icon always exists.
export type BrandArtwork = {emblem: boolean; ghost: boolean};

// release: 'signed', 'unsigned' (a development build, or somebody's own) or
// 'altered' (server/release_check.go releaseWord). version is the plugin's.
export type ReleaseWord = {state: 'signed' | 'unsigned' | 'altered' | string; version: string};

// GET /api/v1/release: releaseWord, and, whenever there is a release
// folder, its manifest and both signatures as found; reason with altered
// (server/release_check.go releaseResponse).
export type ReleaseAnswer = ReleaseWord & {
    reason?: string;
    manifest?: string;
    rsa?: string;
    mldsa87?: string;
};

// app: check is the server's setting ('off', 'report' or 'require'); state
// is 'verified', 'unverified' (reason says why, in the server's words) or
// 'none'; development beside verified when only the plugin's development
// build would pass it; versionCode as Android attested it; minimum, the
// oldest Matty this server answers, while one is set, which never refuses
// the panel (server/app_check.go appWord, server/app_version.go).
export type AppWord = {
    check: 'off' | 'report' | 'require' | string;
    state: 'verified' | 'unverified' | 'none' | string;
    reason?: string;
    development?: boolean;
    versionCode?: number;
    minimum?: string;
};

// For system admins: the plugin's files are not as signed, or a signed
// release's folder holds files nobody signed (server/release_check.go
// releaseAlert). problem says what, file which file; unlisted counts the
// files nobody signed and unlistedFiles names some of them.
export type ReleaseAlert = {
    state: string;
    problem?: string;
    file?: string;
    unlisted?: number;
    unlistedFiles?: string[];
};

export type Me = {
    // Which contract the server speaks, and which version it is.
    contract?: number;
    version?: string;

    // What this server calls the plugin (never empty), which artwork it has,
    // whether it is a signed release, and the app check's word for this
    // session: as GET /api/v1/access gives them.
    name: string;
    artwork: BrandArtwork;
    release: ReleaseWord;
    app: AppWord;

    userId: string;
    username: string;

    // What the reader may do in the panel, decided by the server for every
    // request (design/lock-screen.md 3.8): 'linked' (their own stream, proved
    // with their bearer token: everything), 'letIn' (a system admin let them
    // in: watch only), 'locked' (the lock screen) or 'unknown' (the store did
    // not answer). Optional only so that a "me" without it reads as locked
    // (lockLogic.ts accessOfMe). The panel decides from this, never from
    // streamKey or clipsStreamer.
    access?: 'linked' | 'letIn' | 'locked' | 'unknown';

    // The linked reader's own stream key, for relinking and What's on?; ''
    // for everybody else.
    streamKey: string;
    badgeEnabled: boolean;
    autoLinkByUsername: boolean;
    linkedLive: boolean;
    styleCodeBlocks: boolean;

    // never | live | always. What the panel does when somebody loads the page.
    openPanel: string;

    // How often the No Signal screen catches a ghost of a picture: 'off',
    // 'rare', 'occasional' or 'often'.
    noSignalGhost?: string;

    // Which kinds of title the planning form can look up: movies and shows
    // need a TMDB token, games do not, and an admin can switch both off. Games
    // also come from IGDB when the admin set up a Twitch application.
    media?: {movies: boolean; games: boolean; igdb?: boolean};
    bannerText: string;
    bannerColor: string;

    // Where the banner shows, as Mattermost's Classification Markings put
    // theirs: 'top' only, or 'both', the top and the bottom. Absent from a
    // server older than the setting, which means both.
    bannerPlacement?: 'top' | 'both';
    configured: boolean;
    isAdmin: boolean;
    airtimeAlert?: AirtimeAlert;
    voiceAlert?: string;
    birthdayAlert?: string;

    // Admins only (design/3.2.0.md 2.3): why the plugin's Mumble account cannot
    // play sounds, a line of 24/7 channel achievements that could not be read,
    // and the fan and channel ledgers' state.
    soundAlert?: string;
    achievementsAlert?: string;
    ledgerAlert?: LedgerAlert;

    // Admins only, linked or not: what about Clips needs putting right in
    // System Console. Counts and states, never anything from a clip.
    clipsAlert?: string;

    // Admins only, in any access state: the let-in list could not be read,
    // or was saved by a newer version of the plugin.
    letInAlert?: string;

    // Linked people only, and only while a Public watch URL is set: Pop out
    // exists for them (GET /api/v1/watch/popout).
    popOut?: boolean;

    // Linked people only: whether they are on the let-in list too, so that
    // Unlink can say they would still be able to watch.
    alsoLetIn?: boolean;

    // Everybody with a linked stream: whether clips may be made of it. Absent
    // for everybody else, who has no stream of their own to switch — and also
    // for somebody linked when the server cannot tell (its store not answering,
    // or the clips document halted with no copy on that node), because a
    // switch shown in a guessed position could be wrong. So never read this to
    // decide whether somebody is linked: that is access.
    //
    // downloads (3.2.1, design/3.2.1.md 1.3) is whether people may download
    // clips of their stream, there only while an admin allows downloads at
    // all: its absence is what hides the streamer's second switch.
    clipsStreamer?: {allowed: boolean; downloads?: boolean};

    // Twitch (design/twitch-live.md 4.10): there whenever Show people live on
    // Twitch is on, and whenever the reader has a Twitch account connected
    // or a takeover notice even while it is off, so Disconnect can always be
    // reached. A connection is never "linked": it unlocks nothing.
    twitch?: MeTwitch;

    // Steam and Hytale (design/3.4.0.md 2.9), each shaped like twitch: there
    // whenever that switch is on, and whenever the reader has a connection
    // or a takeover notice even while it is off, so Disconnect can always be
    // reached. Neither is "linked" either.
    steam?: MeAccount;
    hytale?: MeAccount;

    // System admins only (above).
    releaseAlert?: ReleaseAlert;

    // System admins only, from 4.1.0: push notifications are switched on
    // here and Mattermost decides to push to nobody, in the server's words,
    // which name where in the System Console Mattermost's own are switched
    // on.
    pushAlert?: string;

    // System admins only, from 4.3.0: talking from Matty is switched on
    // here and its voice port is down, in the server's words.
    voiceTalkAlert?: string;

    // System admins only, from 4.4.0: a newer release of the plugin is
    // out, while Look for updates is on, and its page.
    updateAlert?: {version: string; url: string};
};

// The two services connected the 3.4.0 way, through one core on the server
// (design/3.4.0.md 2.1). Twitch keeps its own shape and its own routes.
export type AccountService = 'steam' | 'hytale';

// What "me" says about the reader's own Steam or Hytale account
// (design/3.4.0.md 2.9 and 6.4).
export type MeAccount = {
    // The switch on and set up: Connect can be offered.
    available: boolean;
    connected?: boolean;

    // The Steam name or the Hytale username, while connected.
    name?: string;

    // Steam only: the profile's address, built by the server from the
    // SteamID64, and whether Steam's last read said the profile is not
    // public, so that everybody else sees the reader as offline.
    url?: string;
    hidden?: boolean;

    // An admin has blocked them from connecting: nothing about it shows.
    blocked?: boolean;

    // Their account was proved by somebody else and moved to them: which
    // name, and when (ms). The account's id never leaves the server.
    lost?: {name: string; at: number};

    // What is wrong with that service on this server, for system admins
    // only.
    problem?: string;
};

// What Steam's last read said about somebody, while that read is fresh;
// '' when it is not, so names are shown and statuses never guessed.
export type AccountsSteamState = 'online' | 'busy' | 'away' | 'offline' | '';

export type AccountsSteam = {
    name: string;
    url: string;
    state: AccountsSteamState;

    // Only while Steam reports a game for a public profile; game may be ''
    // with inGame true (a game Steam does not name).
    inGame?: boolean;
    game?: string;
};

// What somebody is doing, in a line, as the server decides it once for both
// clients (server/accounts_activity.go): "playing" a game, or "streaming" it
// when their own stream is of that very game, or "streaming" the movie or
// show their What's on? names while they are live. title is '' only for a game
// Steam does not name; source is who names it: 'steam', 'panel' (What's
// on?) or 'twitch'.
export type AccountsActivity = {
    kind: string;
    title: string;
    source: string;
};

export type AccountsPerson = {
    steam?: AccountsSteam;
    hytale?: {name: string};

    // Matty draws this under names. The panel does not show it yet: its
    // Playing strip and popover rows are Steam's alone, and say nothing of
    // streaming. Somebody on the air whose stream names a game, a movie or a
    // show is in people for this alone, with neither service.
    activity?: AccountsActivity;
};

// GET /api/v1/accounts (design/3.4.0.md 2.9 and 6.4): everybody's Steam
// status and Hytale username, for readers who may watch, and never in the
// snapshot, which reaches the locked too. people is {} when nobody is
// connected, never null.
export type AccountsView = {
    v: string;
    steam: {on: boolean; fresh: boolean};
    hytale: {on: boolean};
    people: Record<string, AccountsPerson>;
};

// System Console → Steam connections and Hytale connections
// (GET /api/v1/admin/steam and /hytale, design/3.4.0.md 3.8 and 4.7). Nobody's
// Steam status: the cards work for a locked admin, and locked people see
// none.
export type AdminAccountPerson = {
    userId: string;

    // The Mattermost name; the Steam name and profile, or the Hytale
    // username.
    name: string;
    steamName?: string;
    url?: string;
    hytaleName?: string;
    since: number;

    // False for a deactivated or deleted Mattermost account, which Steam is
    // not asked about. deleted only when Mattermost said it no longer has the
    // account at all, which then has no name; a name missing because
    // Mattermost did not answer is not that.
    active: boolean;
    deleted: boolean;
};

export type AdminSteam = {
    enabled: boolean;
    keySet: boolean;
    keyProblem: string;

    // Where Steam sends people back to (without the state), the realm its
    // sign-in page names, or why there cannot be one.
    callbackUrl: string;
    realm: string;
    callbackProblem: string;

    // The last time Steam was asked and the last time it answered (ms; 0 for
    // never), what is wrong, the calls made today (UTC), and the last good
    // read's active people asked about and answered for.
    lastTry: number;
    lastGood: number;
    problem: string;
    callsToday: number;
    asked: number;
    answered: number;
    people: AdminAccountPerson[];
    blocked: AdminTwitchBlocked[];
    quarantined: boolean;
};

export type AdminHytale = {
    enabled: boolean;
    credentials: boolean;

    // The Redirect URI to register with Hytale, exactly, or why there cannot
    // be one.
    callbackUrl: string;
    callbackProblem: string;
    problem: string;
    people: AdminAccountPerson[];
    blocked: AdminTwitchBlocked[];
    quarantined: boolean;
};

export type MeTwitch = {
    // Whether connecting is possible here now: the switch, the application
    // and a callback address.
    available: boolean;

    // The reader's connected Twitch account, when there is one.
    login?: string;
    displayName?: string;

    // Live on Twitch right now, as the server's last read of Twitch had it.
    live?: boolean;

    // An admin has blocked them from connecting: nothing about Twitch is
    // shown to them.
    blocked?: boolean;

    // The Twitch account they had connected was proved by somebody else and
    // moved to them, so a hijack cannot go unnoticed: which login, and when
    // (ms).
    lost?: {login: string; at: number};

    // What is wrong with Twitch on this server, for system admins only.
    problem?: string;
};

// System Console → Twitch connections (GET /api/v1/admin/twitch,
// design/twitch-live.md 4.10). Twitch ids are never sent.
export type AdminTwitchPerson = {
    userId: string;

    // The Mattermost name, and the Twitch login and display name.
    name: string;
    login: string;
    twitchName: string;
    since: number;
    live: boolean;
    allDay: boolean;
};

export type AdminTwitchBlocked = {
    userId: string;
    name: string;
    since: number;
};

export type AdminTwitch = {
    // Show people live on Twitch, Play Twitch streams inside the panel, and
    // whether a Client ID and Client Secret are set.
    enabled: boolean;
    embed: boolean;
    credentials: boolean;

    // The OAuth Redirect URL to add to the Twitch application, or why there
    // cannot be one ("" when there can).
    callbackUrl: string;
    callbackProblem: string;

    // Why Twitch's player cannot run at this server's address, or "".
    embedProblem: string;

    // The last time Twitch was asked, the last time it answered, and what is
    // wrong (ms; 0 for never).
    lastTry: number;
    lastGood: number;
    problem: string;
    people: AdminTwitchPerson[];
    blocked: AdminTwitchBlocked[];
    hours: {
        channels: number;
        seconds: number;
        halted: string;
        purging: number;
        quarantined: boolean;
    };
};

export type Profile = {
    available: boolean;
    motd: string;
    isPublic: boolean;
    message?: string;
};

// ---- Clips ----
// As the server sends them (design/clips-spec.md 2.10.3 and 2.12, with the
// addendum's changes). Every clip route is for people with a linked stream
// only, admins included; the System Console's are for any system admin.

// How the server's rolling recording of a live stream is doing. 'starting'
// until it has a few seconds, 'off' when its streamer switched clipping of it
// off; the rest say why it cannot be clipped right now.
export type ClipState = 'starting' | 'ready' | 'failed' | 'unsupported' | 'full' | 'queued' | 'off';

// What every client knows about Clips, from the snapshot. enabled means new
// clips can be made now; published ones stay watchable while it is false.
// version changes whenever a clip, a title, a picture or a count does, so an
// open list knows to fetch again; newest is when the newest clip was made.
export type ClipsInfo = {
    enabled: boolean;
    lengthSeconds: number;
    retentionDays: number;
    count: number;
    newest?: number;
    version?: string;
};

// The end of a stream that went off ('ended') or dropped ('dropped'), held
// until 'until' (ms) so the people who were watching can still clip it.
export type ClipTail = {
    handle: string;
    title: string;
    userId?: string;
    reason: 'ended' | 'dropped';
    endedAt: number;
    until: number;
    heldMs: number;
};

// The one Best Clipper, all time, by upvotes. bestClip names their most
// upvoted clip by id only: its title and its count are clip content, which
// only a linked reader may fetch (GET /clips/clip).
export type BestClipper = {
    userId: string;
    username?: string;
    displayName?: string;
    upvotes: number;
    clips: number;
    since: number;
    bestClip?: {id: string};
};

// The game, movie or show a clip's stream was tagged with when it was cut
// (addendum 4.14): the stream's What's on?, or else the title of its plan
// that was on then — apiStream.nowPlaying, captured with the draft and kept
// with the clip, never edited afterwards. Compact on purpose, since every
// clip carries it in one stored document: the real title's kind, year, link
// and source when lookups found it, and never its overview or facts. A
// stream with no tag gives a clip with none, and nothing is shown for it.
export type ClipTagMedia = {
    ref: string;

    // 'movie', 'tv' or 'game'. A plain string, as MediaInfo's is.
    kind: string;
    title: string;
    year?: string;
    link?: string;
    source: string;
};

export type ClipTag = {
    // 'movie' or 'game', as NowPlaying's category is.
    category: string;
    title: string;
    media?: ClipTagMedia;
};

// Somebody a clip names. name is what to print; a stream nobody is linked to
// has only that.
export type ClipPerson = {
    userId?: string;
    username?: string;
    displayName?: string;
    name: string;
};

// A published clip as the reader may see it. The flags are the server's
// answer for this reader: theirs, of their own stream, and what they may do.
// leadMs is how much of the file comes before the clip's own start, for the
// player to skip. reports only ever comes in the System Console's list.
export type Clip = {
    id: string;
    title: string;
    createdAt: number;
    expiresAt: number;
    startAt: number;
    durationMs: number;
    leadMs?: number;
    width: number;
    height: number;
    codecs: string;
    bytes: number;
    thumb?: string;
    clipper: ClipPerson;
    streamer: ClipPerson;
    streamNote?: string;
    tag?: ClipTag;
    upvotes: number;
    voted: boolean;
    mine: boolean;
    ofMyStream: boolean;
    canVote: boolean;
    canDelete: boolean;
    canReport: boolean;

    // Whether the reader may share it into a channel: false for somebody let
    // in to watch only. Share is hidden whenever it is false.
    canShare?: boolean;
    reported: boolean;
    reports?: number;

    // Whether the reader may download it (3.2.1): an admin allows downloads,
    // the reader is linked, and its streamer has not turned them off. The
    // server works it out; Download shows only while it is true.
    canDownload?: boolean;
};

// A shared clip's card: what a post in a channel shows a linked reader, and
// nothing about who voted or what they may do (GET /clips/cards).
export type ClipCard = {
    id: string;
    title: string;
    createdAt: number;
    durationMs: number;
    thumb?: string;
    clipper: ClipPerson;
    streamer: ClipPerson;
    tag?: ClipTag;

    // When the clip goes, by the retention in force when the server answered.
    // A card in hand says "expired" by this and by nothing worked out here.
    expiresAt?: number;

    // design/3.2.0.md 9.2: the thumbs up that count now (linked voters, the
    // same for everybody), whether this reader gave one, and whether they may
    // (linked, not a bot, not their own clip). These make the answer per
    // reader, which is why the cards are dropped at sign-out.
    upvotes?: number;
    voted?: boolean;
    canVote?: boolean;
};

export type ClipSource = 'live' | 'tail';

// A draft: the last few minutes of a stream, cut for one person, kept for two
// hours for them to trim, name and publish. Only its owner ever sees it.
export type ClipDraft = {
    id: string;
    handle: string;
    source: ClipSource;
    state: 'ready' | 'publishing' | 'published';
    clipId?: string;
    createdAt: number;
    expiresAt: number;
    startAt: number;
    durationMs: number;
    requestedMs: number;
    partial: boolean;
    truncated: boolean;
    width: number;
    height: number;
    codecs: string;
    bytes: number;
    hasThumb: boolean;
    streamer: ClipPerson;
    streamNote?: string;
    tag?: ClipTag;

    // A draft cut from the held end of a stream: how that stream had stopped,
    // for the editor's line saying where the draft is from. The server keeps
    // it with the draft, so it is right after a reload too, and after the end
    // it was cut from has been let go.
    tailReason?: 'ended' | 'dropped';
};

// What a clip route refused with, as the server's "code". The panel words
// each one itself, falling back to the server's text for any it does not
// know. 'network' is the client's own: no answer came back at all.
export type ClipErrorCode = 'clips_off' | 'blocked' | 'forbidden' | 'not_allowed' | 'own_clip' | 'not_watching'
    | 'streamer_off' | 'not_live' | 'not_clippable' | 'warming_up' | 'draft_cap' | 'clip_cap' | 'duplicate'
    | 'publishing' | 'draft_gone' | 'draft_wiped' | 'tail_gone' | 'clip_gone' | 'too_short' | 'bad_range'
    | 'bad_request' | 'bad_thumb' | 'rate_limited' | 'recording_failed' | 'busy' | 'storage_unavailable'
    | 'store' | 'storage_full' | 'user_storage_full' | 'not_linked' | 'cannot_post' | 'share_cap' | 'network'
    | 'locked' | 'watch_only' | 'gone' | 'downloads_off' | 'streamer_downloads_off';

// One recording, in the System Console. reason is free text for admins, saying
// why it is not recording when it is not.
export type AdminClipRecorder = {
    handle: string;
    title: string;
    state: ClipState;
    heldSeconds: number;
    bitrate: number;
    truncated: boolean;
    reason: string;
};

export type AdminClipper = {
    userId: string;
    username: string;
    displayName: string;
    clips: number;
    bytes: number;
    upvotes: number;
    banked: number;
};

export type AdminBlockedClipper = {
    userId: string;
    username: string;
    displayName: string;
    blockedAt: number;
    by: string;
};

// Everything the System Console shows about Clips on this server. Named like
// the AdminClips component, so import it under another name there, as
// AdminStills does with its own.
export type AdminClips = {
    problem: string;
    halted: string;
    root: string;
    usedBytes: number;
    limitBytes: number;
    shareBytes: number;
    freeBytes: number;
    totalBytes: number;
    ringBytes: number;
    recorders: AdminClipRecorder[];
    tails: ClipTail[];
    drafts: number;
    clips: Clip[];
    people: AdminClipper[];
    blocked: AdminBlockedClipper[];
    retention: {days: number; pendingRemovals: number; removalAt?: number};
    bestClipper: BestClipper | null;
};

// What a shared clip's post carries in its props (addendum 4.3 and 4.14),
// written by the server when somebody presses Share. Anybody can forge or try
// to patch a post's props, so the card reads these only as an untrusted hint
// (clipLogic.ts cardView), and a linked reader's card shows the server's own
// ClipCard instead. villanet_clip_gone is the server's alone, written once the
// clip has gone; a deletion also takes the title, the names and the tag out.
export type ClipPostProps = {
    villanet_clip_id: string;
    villanet_clip_title?: string;
    villanet_clip_duration_ms: number;
    villanet_clip_created_at: number;
    villanet_clip_clipper_id?: string;
    villanet_clip_clipper?: string;
    villanet_clip_streamer_id?: string;
    villanet_clip_streamer?: string;

    // What the stream was tagged with: its title, and 'movie', 'tv' or
    // 'game'. Both left out for a clip with no tag.
    villanet_clip_tag?: string;
    villanet_clip_tag_kind?: string;
    villanet_clip_gone?: 'expired' | 'deleted';
};

// ---- Who is let in without a token (System Console, Access) ----

// One person on the let-in list, as GET /api/v1/admin/let-in sends it. state
// is 'gone' for an account Mattermost no longer has; name is the display
// name, or ''.
export type LetInPerson = {
    userId: string;
    username: string;
    name: string;
    at: number;
    byUsername: string;
    state: 'active' | 'deactivated' | 'gone';
};

export type LetInList = {
    people: LetInPerson[];
    count: number;
    max: number;

    // Why the list cannot be trusted just now, for admins; '' when it can.
    problem: string;

    // Saved by a newer version of the plugin: shown, and not to be changed here.
    readOnly: boolean;
};

// ---- Mattermost's own shapes ----
// Only the fields the hashtag search reads, as Mattermost's REST API sends
// them (@mattermost/types: posts.ts, users.ts, channels.ts). Every one is
// optional that Mattermost may leave out or blank.

export type MattermostPost = {
    id: string;
    create_at: number;
    edit_at?: number;
    user_id: string;
    channel_id: string;

    // Empty for a post that starts a thread; the thread's first post otherwise.
    root_id?: string;
    message: string;

    // '' for an ordinary post; 'system_*' for Mattermost's own notices.
    type?: string;
    props?: {from_webhook?: string; override_username?: string; [key: string]: unknown};
};

export type MattermostUser = {
    id: string;
    username?: string;
    nickname?: string;
    first_name?: string;
    last_name?: string;

    // A user from a connected server, whose own username is in their props.
    remote_id?: string;
    props?: {RemoteUsername?: string; [key: string]: unknown};
};

export type MattermostChannel = {
    id: string;
    team_id?: string;

    // O public, P private, D direct, G group.
    type?: string;

    // A direct message's name is the two people's ids: "idA__idB".
    name?: string;
    display_name?: string;
    delete_at?: number;

    // A channel shared with another Mattermost server (Shared Channels). True
    // only when it is; Mattermost sends null or leaves it out otherwise.
    shared?: boolean | null;
};

// POST /api/v4/teams/{team}/posts/search: the ids newest first, and the posts
// by id. The rest of what comes back is not used.
export type PostSearchPage = {
    order?: string[];
    posts?: Record<string, MattermostPost | undefined>;
};

// ---- The soundboard (design/3.2.0.md 7) ----

// An emoji as the server resolved it: a system one by its name and the
// unified code its picture is filed under, or one of the server's own by id.
export type SoundEmoji =
    {kind: 'system'; name: string; unified: string} |
    {kind: 'custom'; name: string; id: string};

export type Sound = {
    id: string;
    name: string;
    emoji: SoundEmoji;
    ms: number;

    // The display name of whoever added it, and whether that is the reader.
    owner: string;
    mine: boolean;
};

// Why the tiles are off, from Mumble's live state at the moment of asking
// (design/3.2.0.md 7.5 step 4); '' while the reader may play.
export type SoundWhy = '' | 'muted' | 'deafened' | 'suppressed' | 'not_registered' | 'two_sessions' |
    'not_in_voice' | 'bot_offline' | 'bot_cannot';

// GET /api/v1/sounds (7.9). The list only while the reader is in Mumble, and
// the channel's name only to the person in it.
export type SoundBoard = {
    voice: {in: boolean; channel?: string; can: boolean; why: SoundWhy};
    bot: {ok: boolean};
    cooldownUntil: number;
    busyUntil: number;
    v: string;

    // An admin has blocked the reader from the soundboard (7.11): the board
    // still shows, and nothing on it can be played or added.
    blocked?: boolean;
    sounds?: Sound[];
    limits?: {maxMs: number; perPerson: number; total: number; name: number; left: number; cooldownMs?: number};
};

// POST /api/v1/sounds/play, once packet 0 is on the wire.
export type SoundPlay = {startedAt: number; endsAt: number; cooldownUntil: number; channel: string};

// System Console → Sounds on the server (GET /api/v1/admin/sounds, 2.2).
export type AdminSound = {
    id: string;
    name: string;
    emoji: SoundEmoji;
    ownerId: string;
    owner: string;
    ms: number;
    bytes: number;
    level: number;
    at: number;

    // false when its audio is missing or fails its checksum.
    ok: boolean;
};

export type AdminSounds = {
    on: boolean;
    readable: boolean;

    // line is there only when something is wrong.
    bot: {ok: boolean; line?: string};
    totals: {sounds: number; bytes: number; people: number};
    sounds: AdminSound[];
    blocked: Array<{userId: string; username: string; name: string; by: string; at: number}>;
};
