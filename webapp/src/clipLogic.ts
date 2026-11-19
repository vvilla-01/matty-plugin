// Clips, decided from plain values alone: what a shared clip's card in a
// channel shows, what the scissors on a stream say and do, how far the
// trimmer's handles may go, what the Share button calls the channel, what
// every refusal says, the Clips dialog's order, ages and labels, when the
// header's clapper shows and what it says, Best
// Clipper in words, and the System Console's lines.
//
// Pure, and importing nothing — not even a type — so build/check-away.mjs can
// transpile this one file and run its rules without a browser, as it does
// awayLogic.ts. Everything that reads a store, the clock or the page is the
// caller's: each rule here takes "now", the snapshot's numbers and the
// reader's link as plain arguments, and the components pass them in.
//
// The rules are design/clips-spec.md 3.4, 3.6, 3.7, 3.9, 3.10, 3.12, 3.13 and
// 3.14 and the addendum's 3.7, 3.8, 4.9, 4.10 and 4.14. Where the two disagree
// the addendum wins, and Victor's own rules win over both: above all, that
// every part of Clips is for people who have linked their stream.

// The type a shared clip's post is made with. Mattermost renders a post of a
// "custom_" type through the component a plugin registered for it.
export const CLIP_POST_TYPE = 'custom_villanet_clip';

// The shortest clip there can be, and the step the trimmer's handles move in.
// The server refuses anything shorter as too_short.
export const MIN_CLIP_MS = 5000;
export const TRIM_STEP_MS = 100;

const DAY_MS = 86400000;
const CLIP_ID = /^[0-9a-f]{16}$/;

// ---- Words and times ----

// Whole seconds as m:ss, the way a player writes a time.
function minutesAndSeconds(total: number): string {
    const seconds = Math.max(0, Math.floor(total));

    return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
}

// How long a clip is, as m:ss: "0:45", "2:00". Rounded to the nearest second,
// so a two-minute cut that came out a few milliseconds short, as a cut on a
// keyframe does, still says 2:00.
export function clipTime(ms: number): string {
    return minutesAndSeconds(Math.round(Math.max(0, ms) / 1000));
}

// A point in a clip, as m:ss: rounded down, as a player's clock is, so it
// does not show the next second before it has started.
export function positionTime(ms: number): string {
    return minutesAndSeconds(Math.max(0, ms) / 1000);
}

// A length in words, for what a screen reader says and for labels that read
// as a sentence: "45 seconds", "2 minutes", "90 seconds", "2 minutes 30
// seconds".
export function lengthInWords(seconds: number): string {
    const whole = Math.max(0, Math.round(seconds));
    const unit = (count: number, name: string) => `${count} ${name}${count === 1 ? '' : 's'}`;
    if (whole < 120 && whole % 60 !== 0) {
        return unit(whole, 'second');
    }

    const minutes = Math.floor(whole / 60);
    const rest = whole % 60;

    return rest === 0 ? unit(minutes, 'minute') : `${unit(minutes, 'minute')} ${unit(rest, 'second')}`;
}

// Whose stream a clip came from. A stream linked to somebody is theirs; one
// nobody is linked to is known only by its own title, which already reads as
// a stream ("Stream 3f9a0c"), and gets no possessive.
export function streamLabel(streamer: {userId?: string; name: string}): string {
    return streamer.userId ? `${streamer.name}’s stream` : streamer.name;
}

// ---- The shared clip's card (addendum 4.9) ----

// A post as Mattermost hands it to the card. props is whatever the post
// carries: anybody can make a post of this type through the REST API while
// the plugin is not running, and an author can try to patch it, so nothing in
// it is trusted beyond being shown as text.
export type CardPost = {
    type?: string;
    message?: string;
    props?: unknown;
};

export type CardPerson = {userId?: string; name: string};

// What the server says about the clip, for a linked reader: the cards route's
// answer for one id. A ClipCard from types.ts is one of these.
export type CardClip = {
    title: string;
    createdAt: number;
    durationMs: number;
    thumb?: string;
    clipper: CardPerson;
    streamer: CardPerson;
    tag?: TagInput;

    // When the clip goes, as the server counts it.
    expiresAt?: number;

    // design/3.2.0.md 9.2: the thumbs up that count now, whether this reader
    // gave one, and whether they may.
    upvotes?: number;
    voted?: boolean;
    canVote?: boolean;
};

// What the clip's stream was tagged with, as the card shows it (addendum
// 4.14): the title, the category whose icon goes beside it ('movie' or
// 'game', '' when not known), and the kind in a word ("Game"), for its
// tooltip.
export type CardTag = {title: string; category: string; word: string};

// Where the card's request for that stands. 'idle' is before it has been
// asked for, which shows as loading: a linked reader's card always asks.
export type CardFetch =
    | {status: 'idle' | 'loading' | 'gone' | 'error'}
    | {status: 'ok'; card: CardClip};

export type CardInput = {
    post: CardPost;

    // Whether the reader may watch clips (linked, or let in by an admin):
    // undefined until it is known.
    mayWatch: boolean | undefined;

    // snapshot.clips.retentionDays, or undefined while the snapshot has no
    // clips at all.
    retentionDays?: number;
    now: number;
    fetch: CardFetch;

    // What this server calls the plugin (name.ts), for the card's labels.
    name: string;
};

// The rows of the addendum's table, in its order.
export type CardState = 'fallback' | 'gone-expired' | 'gone-deleted' | 'unknown' | 'locked' | 'loading' | 'gone' | 'error' | 'live';

export type CardText = {
    // '' when the clip has none: the card then writes the streamer and the
    // time, in the reader's own zone, which only the page can do.
    title: string;
    createdAt: number;
    clipper: CardPerson;
    streamer: CardPerson;

    // "Dave’s stream", or an unattributed stream's own name; '' when the
    // post names no stream at all.
    stream: string;

    // "0:45".
    length: string;

    // What the stream was tagged with; absent for nothing.
    tag?: CardTag;
};

export type CardView = {
    state: CardState;

    // The clip's id; '' only for the fallback.
    id: string;

    // The post's message, which is all the fallback shows: the plugin's own
    // fixed text on a genuine post, as plain text either way.
    message: string;

    // The eyebrow, title and meta lines. Absent where the card names nothing:
    // the fallback, a deleted clip and a gone one.
    text?: CardText;

    // The one sentence a gone card says: "This clip has expired." and so on.
    sentence?: string;

    // A muted line under the text, when the card could not ask about the clip.
    note?: string;

    // 'thumb' only with a ClipCard that has a picture; 'backdrop' for one
    // without, which wears the streamer's picture; 'placeholder' otherwise. A
    // reader who is not linked never gets more than the placeholder, and no
    // request is made for them.
    picture: 'none' | 'placeholder' | 'backdrop' | 'thumb';
    thumb?: string;
    dimmed: boolean;
    pulse: boolean;

    // Whether there is a Watch button at all.
    watch: boolean;

    // The card's aria-label, and the Watch button's.
    label: string;
    watchLabel: string;

    // The thumbs up (design/3.2.0.md 9.3), only with the server's card in
    // hand, which only a reader who may watch is ever given: the count, and
    // whether this reader gave one and may give or take one now. A locked
    // reader's card, drawn from the post, has none: props never carry a count.
    votes?: {count: number; voted: boolean; canVote: boolean};
};

type ClipProps = {
    id: string;
    title: string;
    durationMs: number;
    createdAt: number;
    clipperId?: string;
    clipper: string;
    streamerId?: string;
    streamer: string;
    tag?: CardTag;
    gone?: 'expired' | 'deleted';
};

// The kinds a shared post's villanet_clip_tag_kind may say, and the category
// whose icon each is drawn with: a show is filed under movies, as What's on?
// files it.
const TAG_CATEGORIES: Record<string, string> = {movie: 'movie', tv: 'movie', game: 'game'};

// The clip's props, or null when anything is not what the plugin writes: a
// different type, an id that is not 16 hex, a prop of the wrong type, or a
// "gone" that is neither of the two the server writes. A missing title or
// name is allowed — a deleted clip's are scrubbed — but not a missing length
// or time, without which the card cannot be drawn or aged.
//
// An empty string is as good as missing. The server's hook can only blank a
// prop, never take it out (Mattermost decodes a hook's answer into the post
// it sent, and a prop deleted there comes back as it was), so "" is how it
// says "none" — of a "gone" mark as much as of a title. A tag kind it never
// writes is dropped rather than refused: the tag's words still show.
function clipPropsOf(post: CardPost): ClipProps | null {
    if (post.type !== CLIP_POST_TYPE) {
        return null;
    }

    const props = post.props;
    if (typeof props !== 'object' || props === null || Array.isArray(props)) {
        return null;
    }

    const read = props as Record<string, unknown>;
    const text = (key: string): string | null | undefined => {
        const value = read[key];
        if (value === undefined) {
            return undefined;
        }

        return typeof value === 'string' ? value : null;
    };
    const count = (key: string): number | null => {
        const value = read[key];

        return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : null;
    };

    const id = text('villanet_clip_id');
    const durationMs = count('villanet_clip_duration_ms');
    const createdAt = count('villanet_clip_created_at');
    const strings = ['villanet_clip_title', 'villanet_clip_clipper_id', 'villanet_clip_clipper',
        'villanet_clip_streamer_id', 'villanet_clip_streamer', 'villanet_clip_tag', 'villanet_clip_tag_kind',
        'villanet_clip_gone'].map(text);
    if (typeof id !== 'string' || !CLIP_ID.test(id) || durationMs === null || createdAt === null || strings.includes(null)) {
        return null;
    }

    const [title, clipperId, clipper, streamerId, streamer, tagTitle, tagKind, mark] = strings as Array<string | undefined>;
    const gone = mark === '' ? undefined : mark;
    if (gone !== undefined && gone !== 'expired' && gone !== 'deleted') {
        return null;
    }

    let tag: CardTag | undefined;
    const tagWords = (tagTitle || '').trim();
    if (tagWords !== '') {
        const kind = Object.prototype.hasOwnProperty.call(TAG_CATEGORIES, tagKind || '') ? tagKind || '' : '';
        const category = kind === '' ? '' : TAG_CATEGORIES[kind];
        tag = {title: tagWords, category, word: kind === '' ? '' : tagKindWord({category, title: tagWords, media: {kind}})};
    }

    return {
        id,
        title: title || '',
        durationMs,
        createdAt,
        clipperId: clipperId || undefined,
        clipper: clipper || '',
        streamerId: streamerId || undefined,
        streamer: streamer || '',
        tag,
        gone,
    };
}

// The tag a ClipCard carries, as the card shows it.
function cardTagOf(tag?: TagInput): CardTag | undefined {
    const title = tagTitleOf(tag);
    if (!tag || title === '') {
        return undefined;
    }

    return {title, category: tag.category === 'movie' || tag.category === 'game' ? tag.category : '', word: tagKindWord(tag)};
}

function textOf(clip: CardClip, tag: CardTag | undefined): CardText {
    return {
        title: clip.title,
        createdAt: clip.createdAt,
        clipper: clip.clipper,
        streamer: clip.streamer,
        stream: clip.streamer.name.trim() === '' ? '' : streamLabel(clip.streamer),
        length: clipTime(clip.durationMs),
        ...(tag ? {tag} : {}),
    };
}

// "Matty clip: “that jump” — Hytale, 45 seconds, clipped by Bob from Dave’s
// stream", with the server's name for the plugin first. A name the post does
// not carry is left out rather than read out as nothing.
function cardLabel(text: CardText, durationMs: number, name: string): string {
    let what = text.title !== '' ? `${name} clip: “${text.title}”` : `${name} clip`;
    if (text.tag) {
        what += ` — ${text.tag.title}`;
    }

    let label = `${what}, ${lengthInWords(durationMs / 1000)}`;
    if (text.clipper.name.trim() !== '') {
        label += `, clipped by ${text.clipper.name}`;
    }
    if (text.stream !== '') {
        label += `${text.clipper.name.trim() !== '' ? '' : ','} from ${text.stream}`;
    }

    return label;
}

function watchLabelOf(text: CardText, name: string): string {
    if (text.title !== '') {
        return `Watch “${text.title}” in ${name}`;
    }

    return text.streamer.userId && text.streamer.name.trim() !== '' ? `Watch ${text.streamer.name}’s clip in ${name}` : `Watch this clip in ${name}`;
}

export const CARD_EXPIRED = 'This clip has expired.';
export const CARD_DELETED = 'This clip was deleted.';
export const CARD_GONE = 'This clip has gone — it expired or somebody deleted it.';
export const CARD_UNCHECKED = 'Couldn’t check this clip just now.';

// What the card shows, by the first row of the addendum's table that matches.
export function cardView(input: CardInput): CardView {
    const message = typeof input.post.message === 'string' ? input.post.message : '';
    const props = clipPropsOf(input.post);
    if (!props) {
        return {state: 'fallback', id: '', message, picture: 'none', dimmed: false, pulse: false, watch: false, label: '', watchLabel: ''};
    }

    const fromProps = textOf({
        title: props.title,
        createdAt: props.createdAt,
        durationMs: props.durationMs,
        clipper: {userId: props.clipperId, name: props.clipper},
        streamer: {userId: props.streamerId, name: props.streamer},
    }, props.tag);
    const base = {id: props.id, message, dimmed: false, pulse: false};

    // Expired: said so by the server, by its mark on the post or by when the
    // card it sent says the clip goes. Only a reader with no card in hand (not
    // linked, or not asked yet) has it worked out here, from when the clip was
    // made, so that a post the server could not mark (an archived channel, or
    // a marker that failed) still says it and that reader needs no request to
    // know. A card in hand is the server's word, and nothing here overrules
    // it: not the post's props, which their author can patch, and not this
    // arithmetic.
    const retention = input.retentionDays;
    const goesAt = input.fetch.status === 'ok' ? input.fetch.card.expiresAt : undefined;
    const said = typeof goesAt === 'number' && Number.isFinite(goesAt) && goesAt > 0;
    const pastRetention = props.gone === undefined && (said ? (goesAt as number) <= input.now :
        typeof retention === 'number' && retention > 0 && props.createdAt + (retention * DAY_MS) <= input.now);
    if (props.gone === 'expired' || pastRetention) {
        return {
            ...base,
            state: 'gone-expired',
            text: fromProps,
            sentence: CARD_EXPIRED,
            picture: 'placeholder',
            dimmed: true,
            watch: false,
            label: `${cardLabel(fromProps, props.durationMs, input.name)}, expired`,
            watchLabel: '',
        };
    }

    // Deleted: the server has already taken the title and the names out of
    // the props, and the card names nothing either.
    if (props.gone === 'deleted') {
        return {...base, state: 'gone-deleted', sentence: CARD_DELETED, picture: 'placeholder', dimmed: true, watch: false,
            label: `${input.name} clip, deleted`, watchLabel: ''};
    }

    const fromPropsView = {
        ...base,
        text: fromProps,
        picture: 'placeholder' as const,
        watch: true,
        label: cardLabel(fromProps, props.durationMs, input.name),
        watchLabel: watchLabelOf(fromProps, input.name),
    };

    // Not known yet, and locked: what the props say, and no request. A
    // locked reader's Watch opens the lock (design/lock-screen.md 7).
    if (input.mayWatch === undefined) {
        return {...fromPropsView, state: 'unknown'};
    }
    if (!input.mayWatch) {
        return {...fromPropsView, state: 'locked'};
    }

    switch (input.fetch.status) {
    case 'ok': {
        const card = input.fetch.card;
        const fromCard = textOf(card, cardTagOf(card.tag));
        const picture = card.thumb ? 'thumb' : card.streamer.userId ? 'backdrop' : 'placeholder';

        // The server's word, not the props': a linked reader sees only what
        // the clip itself says now, whatever the post was patched to say.
        const votes = typeof card.upvotes === 'number' && Number.isFinite(card.upvotes) && card.upvotes >= 0 ?
            {count: Math.floor(card.upvotes), voted: card.voted === true, canVote: card.canVote === true} : undefined;

        return {
            ...base,
            state: 'live',
            text: fromCard,
            picture,
            thumb: card.thumb || undefined,
            watch: true,
            label: cardLabel(fromCard, card.durationMs, input.name),
            watchLabel: watchLabelOf(fromCard, input.name),
            ...(votes ? {votes} : {}),
        };
    }
    case 'gone':
        return {...base, state: 'gone', sentence: CARD_GONE, picture: 'placeholder', dimmed: true, watch: false,
            label: `${input.name} clip, gone`, watchLabel: ''};
    case 'error':
        return {...fromPropsView, state: 'error', note: CARD_UNCHECKED};
    default:
        return {...fromPropsView, state: 'loading', pulse: true};
    }
}

// How long what the cards route said about a clip is good for, and how soon
// a card that could not ask may ask again. What the server said is asked again
// on a mount ten minutes later, on Watch, when a clip has gone from the
// server — and, since 3.2.0's thumbs up on the card, when somebody votes
// (cardRefreshDelay, below) — all of which the caller sees to.
export const CARD_FRESH_MS = 10 * 60000;
export const CARD_RETRY_MS = 30000;

export type CardEntry = {status: 'loading' | 'ok' | 'gone' | 'error'; at: number};

// Whether a card mounting now should ask about its clip, given what was last
// heard: always when nothing was; never once the server said it has gone,
// which it cannot come back from, nor while it is being asked already; and
// otherwise once what was heard is old. A failure is tried again sooner than
// an answer is, so a card the server could not answer for a moment does not
// say so for ten minutes.
export function cardNeedsFetch(entry: CardEntry | undefined, now: number): boolean {
    if (!entry) {
        return true;
    }

    switch (entry.status) {
    case 'gone':
    case 'loading':
        return false;
    case 'error':
        return now - entry.at >= CARD_RETRY_MS;
    default:
        return now - entry.at >= CARD_FRESH_MS;
    }
}

// Somebody's vote anywhere changes the clips' version (every vote does), and
// the cards on screen then show a stale count. Following the version naively
// would have every reader fetch every card in the channel on every vote, the
// storm addendum review fix #3 warned of; so (design/3.2.0.md 9.4) only the
// cards mounted now are asked about again, in one batch, after a random wait of
// up to three seconds so a channel's readers do not all ask at once, at most
// once every 30 s per page, and only while the page is visible.
//
// How long to wait before that, or null for not now: no card on show, or the
// page hidden, when the caller waits for it to be shown again. jitter is a
// number from 0 to 1, Math.random()'s, passed in so this stays pure.
export const CARD_VOTES_EVERY_MS = 30000;
export const CARD_VOTES_SPREAD_MS = 3000;

export function cardRefreshDelay(input: {shown: number; visible: boolean; lastAt: number; now: number; jitter: number}): number | null {
    if (input.shown <= 0 || !input.visible) {
        return null;
    }

    const wait = Math.max(0, input.lastAt + CARD_VOTES_EVERY_MS - input.now);

    return wait + Math.floor(Math.max(0, Math.min(1, input.jitter)) * CARD_VOTES_SPREAD_MS);
}

// A thumbs up shown at once, before the server's answer: the count moves by
// one when the reader's mark changes, and never below nothing.
export function votedNow(votes: {upvotes: number; voted: boolean}, up: boolean): {upvotes: number; voted: boolean} {
    if (votes.voted === up) {
        return votes;
    }

    return {voted: up, upvotes: Math.max(0, votes.upvotes + (up ? 1 : -1))};
}

// ---- The scissors on a stream (spec 3.4, addendum 3.8) ----

// The tile the scissors sit on: a live one, from its stream in the snapshot,
// or a waiting one, whose stream has left the snapshot and which is clipped
// only from the end of it the server still holds. tailHeldMs is how much of
// that end is held (snapshot.clipTails), which can be less than a whole
// clip: a stream that dropped a minute after it started. tailReason is why a
// waiting tile's stream stopped: it went off, or it dropped and has not come
// back.
export type ScissorsTile =
    | {kind: 'live'; alwaysOn: boolean; clip?: string; clipFrom?: number; clipTail?: number; tailHeldMs?: number}
    | {kind: 'waiting'; tailUntil?: number; tailHeldMs?: number; tailReason?: 'ended' | 'dropped'};

export type ScissorsInput = {
    tile: ScissorsTile;

    // Whether the reader has linked their stream: only they clip. undefined
    // until it is known.
    linked: boolean | undefined;

    // snapshot.clips: whether new clips can be made, and how long one press
    // keeps, in seconds.
    clipsEnabled: boolean;
    lengthSeconds: number;

    // Whose stream it is, as the tile names them: "Dave has switched clipping
    // off".
    name: string;

    // The editor is open, still cutting, on this stream.
    cutting: boolean;
    now: number;
};

export type ScissorsView = {
    // What a click, or the C key, does: start a clip, or nothing at all (a
    // disabled state).
    action: 'clip' | 'none';
    source?: 'live' | 'tail';
    disabled: boolean;
    busy: boolean;

    // The corner tag, when less than a full length has been recorded.
    tag?: string;
    label: string;
    title: string;
};

// A recording needs this much before it can be clipped at all.
const SHORTEST_RECORDING_S = 5;

// The second line of every enabled scissors' tooltip.
export const SCISSORS_EDITOR_HINT = 'In the editor, I and O set the start and end at the playhead.';

const enabled = (action: 'clip', source: 'live' | 'tail', label: string, title = label, tag?: string): ScissorsView => ({
    action,
    source,
    disabled: false,
    busy: false,
    ...(tag ? {tag} : {}),
    label,
    title: `${title}\n${SCISSORS_EDITOR_HINT}`,
});

const disabled = (label: string): ScissorsView => ({action: 'none', disabled: true, busy: false, label, title: label});

// What the scissors on a tile show, or null where there are none. The rows
// are checked top to bottom and the first match wins. Only somebody linked
// clips: anybody else — let in to watch only, or not known yet — has no
// scissors at all, rather than a prompt (design/lock-screen.md 5).
export function scissorsView(input: ScissorsInput): ScissorsView | null {
    const {tile, now} = input;
    if (input.linked !== true || !input.clipsEnabled) {
        return null;
    }

    // Placement. A live tile has scissors while the server reports a
    // recording state for it, or still holds the end of an earlier stretch of
    // it; a waiting tile only while the end of its stream is held. A tail is
    // taken as held only until its time, by this browser's clock, so the
    // scissors go when it does rather than at the next snapshot.
    let tailHeld = false;
    if (tile.kind === 'live') {
        tailHeld = typeof tile.clipTail === 'number' && tile.clipTail > now;
        if (tile.alwaysOn || (tile.clip === undefined && !tailHeld)) {
            return null;
        }
    } else if (!(typeof tile.tailUntil === 'number' && tile.tailUntil > now)) {
        return null;
    }

    if (input.cutting) {
        return {action: 'none', disabled: true, busy: true, label: 'Clipping…', title: 'Clipping…'};
    }

    const length = Math.max(0, Math.round(input.lengthSeconds));
    const lengthTime = minutesAndSeconds(length);

    // What a press on the end of a stream takes: the whole length, or all
    // that was held when it stopped, as Just ended says it.
    const tailTime = clipTime(typeof tile.tailHeldMs === 'number' ? Math.min(tile.tailHeldMs, length * 1000) : length * 1000);

    if (tile.kind === 'waiting') {
        return tile.tailReason === 'dropped' ?
            enabled('clip', 'tail', `Clip the last ${tailTime} before the stream dropped`) :
            enabled('clip', 'tail', `Clip the last ${tailTime} before ${input.name} went off`);
    }

    const recorded = tile.clip === 'ready' && typeof tile.clipFrom === 'number' ?
        Math.floor((now - tile.clipFrom) / 1000) : -1;
    if (recorded >= length) {
        return enabled('clip', 'live', `Clip the last ${lengthInWords(length)}`, `Clip the last ${lengthTime} (C)`);
    }
    if (recorded >= SHORTEST_RECORDING_S) {
        return enabled('clip', 'live', `Clip the last ${lengthInWords(recorded)} — all that has been recorded so far`,
            undefined, minutesAndSeconds(recorded));
    }
    if (tailHeld) {
        return enabled('clip', 'tail', `Clip the last ${tailTime} before the stream dropped`);
    }

    switch (tile.clip) {
    case 'starting':
    case 'ready':
        return disabled('Clipping starts in a few seconds');
    case 'failed':
        return disabled('Clipping isn’t working on this stream just now');
    case 'full':
        return disabled('The server is out of room for clipping just now');
    case 'queued':
        return disabled('Clipping is busy with other streams right now');
    case 'off':
        return disabled(`${input.name} has switched clipping off`);
    case 'unsupported':
        return disabled('This stream’s video format can’t be clipped yet');
    default:
        // A state from a newer server that this build has no words for:
        // nothing, rather than a guess.
        return null;
    }
}

// ---- The trimmer (spec 3.7) ----

export type Trim = {inMs: number; outMs: number; durationMs: number};

export type TrimThumb = 'start' | 'position' | 'end';

const snap = (ms: number): number => Math.round(ms / TRIM_STEP_MS) * TRIM_STEP_MS;

// The whole draft, which is where the trimmer starts: it only ever shrinks a
// clip.
export function trimFrom(durationMs: number): Trim {
    const duration = Math.max(0, durationMs);

    return {inMs: 0, outMs: duration, durationMs: duration};
}

// The start: in 100 ms steps, from 0 to five seconds before the end, so the
// handles can never cross or make a clip too short to publish.
export function clampIn(ms: number, trim: Trim): number {
    const highest = Math.max(0, trim.outMs - MIN_CLIP_MS);

    return Math.min(highest, Math.max(0, snap(ms)));
}

// The end: in 100 ms steps, from five seconds after the start to the end of
// the draft, which it reaches exactly even when that is not on a step.
export function clampOut(ms: number, trim: Trim): number {
    const lowest = Math.min(trim.durationMs, trim.inMs + MIN_CLIP_MS);

    return Math.max(lowest, Math.min(trim.durationMs, ms >= trim.durationMs ? trim.durationMs : snap(ms)));
}

// The playhead goes anywhere in the draft, selection or not.
export function clampPosition(ms: number, durationMs: number): number {
    return Math.min(Math.max(0, durationMs), Math.max(0, ms));
}

// Where a key moves a thumb to, or null for a key the trimmer leaves alone
// (so it keeps its usual meaning). Arrows move a second, ten with Shift, Page
// Up and Page Down ten, and Home and End go to the ends of the thumb's own
// range — the multi-thumb slider pattern, so a thumb's range is the other
// thumbs' positions.
export function trimKey(thumb: TrimThumb, key: string, shift: boolean, value: number, trim: Trim): number | null {
    const big = 10000;
    const small = shift ? big : 1000;
    let next: number;
    switch (key) {
    case 'ArrowLeft':
    case 'ArrowDown':
        next = value - small;
        break;
    case 'ArrowRight':
    case 'ArrowUp':
        next = value + small;
        break;
    case 'PageDown':
        next = value - big;
        break;
    case 'PageUp':
        next = value + big;
        break;
    case 'Home':
        next = -Infinity;
        break;
    case 'End':
        next = Infinity;
        break;
    default:
        return null;
    }

    if (thumb === 'start') {
        return clampIn(next === -Infinity ? 0 : next === Infinity ? trim.outMs : next, trim);
    }
    if (thumb === 'end') {
        return clampOut(next === -Infinity ? 0 : next === Infinity ? trim.durationMs : next, trim);
    }

    return clampPosition(next === -Infinity ? 0 : next === Infinity ? trim.durationMs : next, trim.durationMs);
}

// What a thumb says to a screen reader, and the line under the track: the
// start and the end as a length is written, rounded, so the end of a draft
// reads as its length; the playhead as a clock reads, rounded down.
// "0:38 of 2:00", "0:52", "1:23 — the clip is 45 seconds"; "0:45 of 2:00".
export function trimValueText(thumb: TrimThumb, trim: Trim, positionMs: number): string {
    if (thumb === 'start') {
        return `${clipTime(trim.inMs)} of ${clipTime(trim.durationMs)}`;
    }
    if (thumb === 'end') {
        return `${clipTime(trim.outMs)} — the clip is ${lengthInWords((trim.outMs - trim.inMs) / 1000)}`;
    }

    return positionTime(positionMs);
}

export function trimReadout(trim: Trim): string {
    return `${clipTime(trim.outMs - trim.inMs)} of ${clipTime(trim.durationMs)}`;
}

// ---- The editor (spec 3.6) ----

// The clock on the picture: where the playhead is within the selection, and
// how long the selection is. "0:12 / 0:45".
export function selectionTime(trim: Trim, positionMs: number): string {
    const length = Math.max(0, trim.outMs - trim.inMs);

    return `${positionTime(Math.min(length, Math.max(0, positionMs - trim.inMs)))} / ${clipTime(length)}`;
}

// Where play starts: where the playhead is, unless that is outside the
// selection, which plays from its start. The end itself counts as outside,
// so play after a stop at the end goes round again.
export function playFrom(positionMs: number, trim: Trim): number {
    return positionMs < trim.inMs || positionMs >= trim.outMs ? trim.inMs : positionMs;
}

// What playback does next, looked at every frame while it plays, so it stays
// inside the selection: carry on; jump back to the start (Loop on, at the
// end; or the start moved past the playhead while it played); or stop at the
// end (Loop off).
export type PlaybackStep = {action: 'continue'} | {action: 'jump'; toMs: number} | {action: 'stop'; atMs: number};

export function playbackStep(positionMs: number, trim: Trim, loop: boolean): PlaybackStep {
    if (positionMs >= trim.outMs) {
        return loop ? {action: 'jump', toMs: trim.inMs} : {action: 'stop', atMs: trim.outMs};
    }
    if (positionMs < trim.inMs) {
        return {action: 'jump', toMs: trim.inMs};
    }

    return {action: 'continue'};
}

// The frame the clip's picture is taken from: half a second in, or halfway
// through a selection shorter than a second, in seconds, as a video seeks.
export function thumbnailAt(trim: Trim): number {
    return (trim.inMs + Math.min(500, Math.max(0, trim.outMs - trim.inMs) / 2)) / 1000;
}

// A picture fitted whole inside a box, centred, as object-fit: contain does
// it, for drawing a frame of any shape onto the 480 × 270 thumbnail with black
// round it.
export function containFit(width: number, height: number, boxWidth: number, boxHeight: number): {x: number; y: number; width: number; height: number} {
    if (width <= 0 || height <= 0 || boxWidth <= 0 || boxHeight <= 0) {
        return {x: 0, y: 0, width: Math.max(0, boxWidth), height: Math.max(0, boxHeight)};
    }

    const scale = Math.min(boxWidth / width, boxHeight / height);
    const fitWidth = Math.round(width * scale);
    const fitHeight = Math.round(height * scale);

    return {x: Math.round((boxWidth - fitWidth) / 2), y: Math.round((boxHeight - fitHeight) / 2), width: fitWidth, height: fitHeight};
}

// The plate on the picture while the server cuts, and the line saying so.
// "the last 2:00 of Dave"; "Cutting the last 2:00 of Dave…".
export function cuttingNote(lengthSeconds: number, name: string): string {
    return `the last ${clipTime(Math.max(0, lengthSeconds) * 1000)} of ${name}`;
}

export function cuttingStatus(lengthSeconds: number, name: string): string {
    return `Cutting ${cuttingNote(lengthSeconds, name)}…`;
}

// "Draft ready — 1:58 recorded." The DRAFT's length, and said to be: it sits
// under the trimmer's "0:59 of 1:58", and "Clip ready — 1:58." there read as
// the length of the clip about to be published, which a trim has changed. Not
// the selection's length either: this line is read out as it changes, and
// every move of a handle would be another announcement.
export function readyStatus(durationMs: number): string {
    return `Draft ready — ${clipTime(durationMs)} recorded.`;
}

// Whether a cut the server refused as not_watching is worth one fresh
// playback session and a second try before the refusal is shown. The server
// learns who is watching from the WHEP exchange that starts a tile, and keeps
// that in memory only, so after a restart, or Clips being switched on while
// the tile was already playing, somebody visibly watching is unknown to it —
// and a tile that is playing never negotiates again by itself. Only for a
// live stream (an ended one cannot be tuned in to again), only when this
// window's session for it is playing (status), and once per cut (renewed).
export function renewBeforeRefusing(source: string, code: string, status: string, renewed: boolean): boolean {
    return source === 'live' && code === 'not_watching' && status === 'playing' && !renewed;
}

// "Publishing — cutting 0:45 and making its picture.", and after ten seconds
// of it, "Still working — longer clips take a moment." as well.
export const PUBLISH_SLOW_MS = 10000;

export function publishingStatus(lengthMs: number, slow: boolean): string {
    const line = `Publishing — cutting ${clipTime(lengthMs)} and making its picture.`;

    return slow ? `${line} Still working — longer clips take a moment.` : line;
}

// After it is published: "“that jump” is in Clips until 31 Oct." until is the
// day it goes, already in the reader's own words.
export function publishedSentence(clip: NamedClip, until: string): string {
    const name = clipNameOf(clip);

    return `${name.charAt(0).toUpperCase()}${name.slice(1)} is in Clips until ${until}.`;
}

// What a draft says under its picture when it is less than a press asks for:
// the recording had not been going that long, or the stream's bitrate let
// less of it fit.
export function draftNotes(draft: {partial: boolean; truncated: boolean; durationMs: number}): string[] {
    const notes: string[] = [];
    if (draft.partial) {
        notes.push(`Only ${clipTime(draft.durationMs)} had been recorded — the recording starts when the stream does.`);
    }
    if (draft.truncated) {
        notes.push(`Only ${clipTime(draft.durationMs)} fitted — this stream’s bitrate is higher than clips are sized for.`);
    }

    return notes;
}

export type EditorContextInput = {
    streamer: CardPerson;

    // What the stream was tagged with, by its title; '' for nothing.
    tag: string;

    // The stream's own note when it was cut; '' for none.
    note: string;

    // When the draft starts and ends, as the reader's clock reads them:
    // "21:02:17".
    from: string;
    to: string;

    // The end of a stream that went off or dropped, rather than one still
    // live; 'unknown' for the end of one whose reason this page no longer
    // knows (a draft reopened after a reload, or once that end was let go:
    // a draft does not say why its stream stopped).
    tail?: 'ended' | 'dropped' | 'unknown';
};

// The line under the picture saying where the draft is from: "From Dave’s
// stream · Hytale · “Arma 3 ops night” · 21:02:17 – 21:04:17", and " · before
// it went off", " · before it dropped" or " · from the end of the stream" for
// the end of a stream. The tag (addendum 4.14) and the note only when there
// are any.
export function editorContext(input: EditorContextInput): string {
    const parts = [`From ${streamLabel(input.streamer)}`];
    if (input.tag.trim() !== '') {
        parts.push(input.tag.trim());
    }
    if (input.note.trim() !== '') {
        parts.push(`“${input.note.trim()}”`);
    }
    parts.push(`${input.from} – ${input.to}`);

    let line = parts.join(' · ');
    if (input.tail === 'ended') {
        line += ' · before it went off';
    } else if (input.tail === 'dropped') {
        line += ' · before it dropped';
    } else if (input.tail === 'unknown') {
        line += ' · from the end of the stream';
    }

    return line;
}

// ---- A clip's tag (addendum 4.14) ----

export type TagInput = {category: string; title: string; media?: {kind: string; year?: string}};

// Its title, '' for no tag or one with no words: nothing is shown for it.
export function tagTitleOf(tag?: {title?: unknown} | null): string {
    return tag && typeof tag.title === 'string' ? tag.title.trim() : '';
}

// What kind of thing it is, in a word: the real title's kind when lookups
// found it, otherwise the kind the streamer picked. '' for neither.
export function tagKindWord(tag: TagInput): string {
    switch (tag.media?.kind) {
    case 'tv':
        return 'TV series';
    case 'movie':
        return 'Movie';
    case 'game':
        return 'Game';
    default:
        break;
    }

    if (tag.category === 'movie') {
        return 'Movie';
    }

    return tag.category === 'game' ? 'Game' : '';
}

// The kind a shared post's props carry for it (villanet_clip_tag_kind,
// addendum 4.14): the real title's kind when lookups found it, else 'movie'
// for the category movie and 'game' for game. '' for anything else, which a
// post leaves out.
export function tagKindOf(tag: TagInput): string {
    const kind = tag.media?.kind;
    if (kind === 'movie' || kind === 'tv' || kind === 'game') {
        return kind;
    }

    return tag.category === 'movie' || tag.category === 'game' ? tag.category : '';
}

// The viewer's line under the title: "Hytale · Game · 2026".
export function tagLine(tag: TagInput): string {
    return [tagTitleOf(tag), tagKindWord(tag), (tag.media?.year || '').trim()].filter((part) => part !== '').join(' · ');
}

// ---- The Share button's label (addendum 4.10) ----

// The channel a clip would be shared into, from Mattermost's store (see
// shareTargetOf in mattermost.ts). kind is '' with no channel at all — the
// global Threads view — 'unknown' for one the store does not have, and
// otherwise Mattermost's own type: O open, P private, D direct, G group.
export type ShareChannel = {
    kind: string;
    displayName?: string;

    // A direct message's other person, by the name Mattermost shows them.
    otherName?: string;

    // Shared with another Mattermost server, which a clip may not go to.
    shared?: boolean;
};

export type ShareLabel = {
    // "Share in Town Square".
    label: string;

    // "in Town Square", for "Click again to post it in Town Square" and
    // "Shared in Town Square.". '' when there is nowhere to share.
    where: string;

    // The tooltip: why it is disabled, or the whole name when it was cut.
    // '' when the label already says everything.
    title: string;
    disabled: boolean;
};

// The longest a name may be in the label before it is cut.
export const SHARE_NAME_MAX = 32;

// A name cut to SHARE_NAME_MAX characters, the last of them "…". Counted in
// characters, not UTF-16 units, so an emoji is never split in half.
function cut(name: string): string {
    const characters = Array.from(name.trim());
    if (characters.length <= SHARE_NAME_MAX) {
        return characters.join('');
    }

    return `${characters.slice(0, SHARE_NAME_MAX - 1).join('').trimEnd()}…`;
}

export const SHARE_NO_CHANNEL = 'Open a channel to share this clip';
export const SHARE_SHARED_CHANNEL = 'Clips can’t be shared into a shared channel';

export function shareLabel(channel: ShareChannel): ShareLabel {
    if (channel.kind === '') {
        return {label: 'Share', where: '', title: SHARE_NO_CHANNEL, disabled: true};
    }

    let where = 'in this channel';
    let full = where;
    const name = (channel.kind === 'D' ? channel.otherName : channel.displayName) || '';
    if ((channel.kind === 'O' || channel.kind === 'P') && name.trim() !== '') {
        where = `in ${cut(name)}`;
        full = `in ${name.trim()}`;
    } else if (channel.kind === 'D' && name.trim() !== '') {
        where = `with ${cut(name)}`;
        full = `with ${name.trim()}`;
    } else if (channel.kind === 'G') {
        where = 'in this group message';
        full = where;
    }

    const label = `Share ${where}`;
    if (channel.shared === true) {
        return {label, where, title: SHARE_SHARED_CHANNEL, disabled: true};
    }

    return {label, where, title: where === full ? '' : `Share ${full}`, disabled: false};
}

// What the button says after its first press: "Click again to post it in
// Town Square", and for a direct message "Click again to send it to Dave".
export function shareConfirm(where: string): string {
    return where.startsWith('with ') ? `Click again to send it to ${where.slice(5)}` : `Click again to post it ${where}`;
}

// The line under it once it has gone: "Shared in Town Square.", "Shared with
// Dave.".
export function shareDone(where: string): string {
    return `Shared ${where}.`;
}

// ---- What a refusal says (spec 3.13, addendum 3.7) ----

// Every clip route refuses with a code, and this is the one place each code
// is put into words, for every surface: the Clips dialog now, the editor, the
// scissors, the Share button and the post card as they arrive. A code this
// build has no words for shows the server's own text, which is fixed and safe
// to show; failing that, the surface's own "could not … just now".

// Where the refusal happened, which changes the words for some codes: the
// network failing is "could not be clipped" for a capture and "could not be
// shared" for a share; being unlinked while looking at one clip is "to watch
// this clip". 'tail' is a capture of the end of a stream that went off.
// 'clip' is one clip being opened to watch, however it was asked for.
export type ClipErrorContext = 'capture' | 'tail' | 'publish' | 'editor' | 'list' | 'clip' | 'vote' | 'report' | 'delete'
    | 'draft' | 'share' | 'allow' | 'wipe' | 'download' | 'downloads';

// What the surface offers beside the words: try again, open the drafts, open
// Clips, open your own clips, link the account, discard the draft, or leave
// it for later. 'check' is publish's own: look for the clip before offering
// to try again, since the publish may have worked.
export type ClipErrorAction = 'retry' | 'drafts' | 'clips' | 'yours' | 'link' | 'discard' | 'later' | 'check';

export type ClipErrorCopy = {text: string; actions: ClipErrorAction[]};

export type ClipCopyValues = {
    // snapshot.clips.lengthSeconds: "the last 2 minutes".
    lengthSeconds?: number;

    // Whose stream: "Dave has switched clipping off".
    name?: string;

    // Until when an unpublished draft is kept, as the reader's clock reads
    // it: "21:40".
    keptUntil?: string;

    // What this server calls the plugin (name.ts): "Matty is locked."
    brand: string;
};

// The words for a refusal for access (design/lock-screen.md 4.8). A server
// from before the lock said not_linked, which reads as locked.
export const clipLocked = (name: string): string => `${name} is locked.`;
export const CLIP_WATCH_ONLY = 'You can watch, but not do that.';
export const CLIP_CANNOT_PLAY = 'This browser cannot play this clip.';
export const CLIP_NO_SOUND = 'This browser will play this clip without sound.';

// What each surface says when it simply did not work: no answer, the
// server's store failing, or a refusal with no words of its own. The spec
// words the capture, the publish, the share and the list; the rest are worded
// the same way.
const FAILED: Record<ClipErrorContext, ClipErrorCopy> = {
    capture: {text: 'That stream could not be clipped just now.', actions: ['retry']},
    tail: {text: 'That stream could not be clipped just now.', actions: ['retry']},
    publish: {text: 'The clip could not be published just now. Your draft is still here.', actions: ['retry']},
    editor: {text: 'The clip could not be published just now. Your draft is still here.', actions: ['retry']},
    list: {text: 'Clips could not be loaded just now.', actions: ['retry']},
    clip: {text: 'This clip could not be loaded just now.', actions: ['retry']},
    vote: {text: 'Your upvote could not be saved just now.', actions: []},
    report: {text: 'The report could not be sent just now.', actions: []},
    delete: {text: 'The clip could not be deleted just now.', actions: []},
    draft: {text: 'The draft could not be discarded just now.', actions: []},
    share: {text: 'The clip could not be shared just now.', actions: ['retry']},

    // The streamer's own switch and wipe, in the panel's footer. No Try
    // again: the switch and the button are right there to press again.
    allow: {text: 'Clipping of your stream could not be changed just now.', actions: []},
    wipe: {text: 'What is held of your stream could not be wiped just now.', actions: []},

    // 3.2.1: Download in the viewer, and the streamer's switch for it.
    download: {text: 'The download could not be started just now.', actions: []},
    downloads: {text: 'Downloads of your stream’s clips could not be changed just now.', actions: []},
};

// What a clip that would not load is called when nothing better is known.
export const CLIP_NOT_LOADED = FAILED.clip.text;

const say = (text: string, actions: ClipErrorAction[] = []): ClipErrorCopy => ({text, actions});

export function clipErrorCopy(code: string, context: ClipErrorContext, values: ClipCopyValues, serverText = ''): ClipErrorCopy {
    const length = Math.max(0, Math.round(values.lengthSeconds ?? 120));
    const failed = FAILED[context];

    switch (code) {
    case 'not_linked':
    case 'locked':
    case 'watch_only':
        if (context === 'editor' || context === 'publish') {
            const until = values.keptUntil ? ` (kept until ${values.keptUntil})` : '';

            // No Discard: somebody who is not linked cannot delete their own
            // drafts either (Victor's answer to question B), so the server
            // would refuse it. Linking again, or leaving it, are what is left.
            return say(`Your stream is no longer linked. Link it again to publish this draft${until}.`, ['link', 'later']);
        }

        return say(code === 'watch_only' ? CLIP_WATCH_ONLY : clipLocked(values.brand));
    case 'network':
        // A publish whose answer was lost may have worked: look before
        // offering to do it again.
        return context === 'publish' || context === 'editor' ? say('Checking whether it was published…', ['check']) : failed;
    case 'store':
        return failed;
    case 'recording_failed':
    case 'busy':
        return say('That stream could not be clipped just now.', ['retry']);
    case 'warming_up':
        return say('Clipping starts a few seconds after a stream does. Try again in a moment.', ['retry']);
    case 'not_watching':
        return say(context === 'tail' ? 'Only people who were watching when it ended can clip it.' : 'Tune in to this stream to clip it.');
    case 'streamer_off':
        return say(`${values.name || 'The streamer'} has switched clipping off.`);
    case 'not_live':
        return say('That stream isn’t live any more.');
    case 'tail_gone':
        return say('The end of that stream is no longer held — it’s kept for 10 minutes.');
    case 'not_clippable':
        return say('This stream can’t be clipped.');
    case 'draft_gone':
        return say(`This draft has expired. Clip again while the moment is still in the last ${lengthInWords(length)}.`);
    case 'draft_wiped':
        return say('The streamer wiped this part of their stream, so this draft is gone.');
    case 'publishing':
        return say('This clip is already being published.', ['check']);
    case 'rate_limited':
        if (context === 'share') {
            return say('That’s a lot of sharing in a row. Try again in a few minutes.');
        }
        if (context === 'vote' || context === 'report' || context === 'delete' || context === 'draft' ||
            context === 'allow' || context === 'wipe' || context === 'download' || context === 'downloads') {
            return say('Slow down a little.');
        }

        return say('That’s a lot of clips in a row. Try again in a minute.');
    case 'draft_cap':
        return say('You already have 3 drafts. Publish or discard one first.', ['drafts']);
    case 'clip_cap':
        return say('There’s no room for another clip. Delete an old one first.');
    case 'duplicate':
        return say('You’ve already published most of this moment.', ['clips']);
    case 'storage_full':
        return say('The server is out of room for clips. Ask an admin.');
    case 'user_storage_full':
        return say('You’re using your share of clip space — delete one of your clips first.', ['yours']);
    case 'storage_unavailable':
        return say('Clips aren’t set up on this server.');
    case 'clips_off':
        return say('Clipping has been switched off.');
    case 'blocked':
        return say('An admin has stopped you making clips.');
    case 'too_short':
        return say('A clip has to be at least 5 seconds long.');
    case 'clip_gone':
        return say(context === 'share' ? 'This clip has gone, so it can’t be shared.' : CARD_GONE);
    case 'own_clip':
        return say('You can’t upvote your own clip.');
    case 'cannot_post':
        return say('You can’t post in that channel.');
    case 'share_cap':
        return say('This clip has already been shared in 10 places, which is the most it can be.');
    case 'downloads_off':
        return say(DOWNLOADS_OFF);
    case 'streamer_downloads_off':
        return say(STREAMER_DOWNLOADS_BLOCKED);
    default:
        // forbidden, not_allowed, the bad_ codes, and anything newer: the
        // server's words when it sent any.
        return serverText.trim() !== '' ? say(serverText.trim()) : failed;
    }
}

// ---- Downloading a clip (3.2.1, design/3.2.1.md 1) ----
// Off until an admin switches it on, for linked people only, from the clip
// viewer only, and never past a streamer who turned it off. The server works
// all of that out per clip, as canDownload; the panel adds only that the
// reader is linked as it last heard, so a reader just let out loses the
// button with everything else.

export const DOWNLOAD = 'Download';
export const DOWNLOAD_PREPARING = 'Preparing…';
export const DOWNLOAD_STARTED = 'Your download has started.';
export const DOWNLOADS_OFF = 'Downloads are off.';
export const STREAMER_DOWNLOADS_BLOCKED = 'The streamer has turned off downloads of their clips.';

// Whether the clip viewer offers Download.
export function clipDownloadOffered(clip: {canDownload?: boolean} | null | undefined, access: string): boolean {
    return access === 'linked' && Boolean(clip) && clip?.canDownload === true;
}

// Which of the clip viewer's actions takes focus from one taken out of the
// row while it had it, by where the lost one stood (at) and how many are
// left: the one that took its place, which is the one after it, or the last
// when it was last; -1 when none is left, and Back has it. Download goes by
// itself whenever an admin or the clip's streamer switches downloads off,
// with the viewer open on it.
export function actionAfterLoss(at: number, left: number): number {
    if (left <= 0) {
        return -1;
    }

    return Math.min(Math.max(0, at), left - 1);
}

// Whether an address the server answered a ticket with is one the panel will
// open: the plugin's own download route with a ticket, and nothing else, so
// the browser is never sent anywhere but this server.
export function isDownloadAddress(url: unknown, pluginBase: string): boolean {
    if (typeof url !== 'string') {
        return false;
    }

    const prefix = `${pluginBase}/api/v1/clips/download?t=`;

    return url.startsWith(prefix) && /^[0-9a-f]{64}$/.test(url.slice(prefix.length));
}

// The streamer's second switch, in Clips of your stream, while an admin
// allows downloads at all.
export const STREAMER_DOWNLOADS_SWITCH = 'Let people download clips of my stream';
export const STREAMER_DOWNLOADS_ON = 'People who have linked their stream can download clips of yours.';
export const STREAMER_DOWNLOADS_OFF = 'Nobody can download clips of your stream, those already made or any to come.';
export const STREAMER_DOWNLOADS_ON_DONE = 'On. Download is back on every clip of your stream.';
export const STREAMER_DOWNLOADS_OFF_DONE = 'Off. Download is gone from every clip of your stream.';
export const streamerDownloadsTitle = (name: string): string => `Whether people can download clips of your stream from ${name}. It goes with the stream, like Allow clips of my stream.`;

// What a shared clip's card says under its thumbs up when a vote did not
// count (design/3.2.0.md 9.6): a refusal keeps the clip vote's own words
// (own_clip, rate_limited, gone, and the lock's); anything that simply did not
// work says the card's own, which a second press may well mend.
export const CARD_VOTE_FAILED = 'Couldn’t count that. Try again.';

export function cardVoteWords(code: string, serverText: string, name: string): string {
    const copy = clipErrorCopy(code, 'vote', {brand: name}, serverText);

    return copy.text === FAILED.vote.text ? CARD_VOTE_FAILED : copy.text;
}

// ---- The Clips dialog (spec 3.9 and 3.10) ----

const HOUR_MS = 3600000;
const MINUTE_MS = 60000;

export type ClipOrder = 'top' | 'new';

// Top is the most upvotes first, a tie newest first; New is newest first.
// The id settles anything left, so two readers see one order.
export function orderClips<T extends {id: string; createdAt: number; upvotes: number}>(clips: T[], order: ClipOrder): T[] {
    return clips.slice().sort((a, b) => (
        (order === 'top' ? b.upvotes - a.upvotes : 0) ||
        (b.createdAt - a.createdAt) ||
        (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)
    ));
}

// The order the cards stay in while the list is open: the ones already shown
// keep their places among themselves, and any that have just arrived take the
// places a fresh sort gives them. fresh is the list sorted now, previous what
// was shown. An upvote changes a count and nothing else: moving the card
// under the reader's pointer is bad enough, and the browser takes focus away
// from an element that is moved, so a card that dropped below its neighbour
// would send keyboard focus to the page under the dialog.
export function steadyOrder(previous: string[], fresh: string[]): string[] {
    const wanted = new Set(fresh);
    const survivors = previous.filter((id) => wanted.has(id));
    const known = new Set(survivors);
    let next = 0;

    return fresh.map((id) => {
        if (!known.has(id)) {
            return id;
        }

        const kept = survivors[next];
        next++;

        return kept;
    });
}

const plural = (count: number, one: string, many = `${one}s`): string => `${count} ${count === 1 ? one : many}`;

// "14 upvotes", "1 upvote".
export const upvotesInWords = (count: number): string => plural(Math.max(0, count), 'upvote');

// How long ago, for a clip's card and a stream that went off: "just now",
// "40 s ago", "2 min ago", "5 hours ago", "3 days ago".
export function agoWords(at: number, now: number): string {
    const seconds = Math.max(0, Math.floor((now - at) / 1000));
    if (seconds < 5) {
        return 'just now';
    }
    if (seconds < 60) {
        return `${seconds} s ago`;
    }
    if (seconds < 3600) {
        return `${Math.floor(seconds / 60)} min ago`;
    }
    if (seconds < 86400) {
        return `${plural(Math.floor(seconds / 3600), 'hour')} ago`;
    }

    return `${plural(Math.floor(seconds / 86400), 'day')} ago`;
}

// Calendar days from one instant to another, in this machine's own zone,
// which is the reader's: tomorrow is tomorrow even when it is ten hours away.
function daysBetween(from: number, to: number): number {
    const midnight = (at: number) => {
        const day = new Date(at);

        return Date.UTC(day.getFullYear(), day.getMonth(), day.getDate());
    };

    return Math.round((midnight(to) - midnight(from)) / DAY_MS);
}

// When something goes, as a phrase: "goes in 52 min", "goes in 5 hours",
// "goes today", "goes tomorrow", "goes in 27 days". For a clip's expiry in
// the viewer and a draft's two hours.
export function goesWords(at: number, now: number): string {
    const left = at - now;
    if (left <= MINUTE_MS) {
        return 'goes in a moment';
    }
    if (left < HOUR_MS) {
        return `goes in ${Math.ceil(left / MINUTE_MS)} min`;
    }
    if (left < 12 * HOUR_MS) {
        return `goes in ${plural(Math.round(left / HOUR_MS), 'hour')}`;
    }

    const days = daysBetween(now, at);
    if (days <= 0) {
        return 'goes today';
    }
    if (days === 1) {
        return 'goes tomorrow';
    }

    return `goes in ${days} days`;
}

// The tag on a clip's picture once it has three days or less: "GOES IN 2D",
// "GOES IN 5H". Rounded, so a clip with a day and twenty hours left says 2D.
// null with more than three days left.
export function expiryTag(at: number, now: number): string | null {
    const left = Math.max(0, at - now);
    if (left > 3 * DAY_MS) {
        return null;
    }
    if (left < 20 * HOUR_MS) {
        return `GOES IN ${Math.max(1, Math.round(left / HOUR_MS))}H`;
    }

    return `GOES IN ${Math.max(1, Math.round(left / DAY_MS))}D`;
}

type NamedClip = {title: string; streamer: CardPerson};

// A clip as a sentence names it: its title in quotes, or else whose stream
// it is from.
export function clipNameOf(clip: NamedClip): string {
    if (clip.title !== '') {
        return `“${clip.title}”`;
    }

    return clip.streamer.userId ? `${clip.streamer.name}’s clip` : 'this clip';
}

// The upvote button's name, the same whether or not it is pressed: what is
// pressed is said by aria-pressed, and a name that flipped with it would say
// it twice and differently (the rule the player's buttons follow).
export function upvoteLabel(clip: NamedClip & {upvotes: number}): string {
    return `Upvote ${clipNameOf(clip)}, ${upvotesInWords(clip.upvotes)}`;
}

// A card's play button: "Play that jump — Hytale, clipped by Bob from Dave’s
// stream, 45 seconds, 3 days ago, 14 upvotes". shown is the title as the card
// shows it, which for a clip without one is the streamer and the time; tag is
// the title of what the stream was tagged with, '' for nothing.
export function clipCardLabel(clip: {clipper: CardPerson; streamer: CardPerson; durationMs: number; createdAt: number; upvotes: number},
    shown: string, now: number, tag = ''): string {
    const what = tag.trim() !== '' ? ` — ${tag.trim()},` : ' —';

    return `Play ${shown}${what} clipped by ${clip.clipper.name} from ${streamLabel(clip.streamer)}, ` +
        `${lengthInWords(clip.durationMs / 1000)}, ${agoWords(clip.createdAt, now)}, ${upvotesInWords(clip.upvotes)}`;
}

// What Delete says once pressed, which is what deleting does to the
// clipper's score. An admin who did not make it, or its clipper, voids its
// upvotes; its streamer leaves them with the clipper. The server decides in
// the same order (addendum 3.3).
export function deleteConfirm(clip: {mine: boolean; ofMyStream: boolean; upvotes: number; clipper: CardPerson}, admin: boolean): string {
    if (!clip.mine && !admin && clip.ofMyStream) {
        return `Click again to delete — ${clip.clipper.name} keeps its upvotes`;
    }
    if (clip.upvotes <= 0) {
        return 'Click again to delete it';
    }

    return clip.upvotes === 1 ? 'Click again — its 1 upvote stops counting' : `Click again — its ${clip.upvotes} upvotes stop counting`;
}

export type TailInput = {title: string; userId?: string; reason: 'ended' | 'dropped'; endedAt: number; until: number; heldMs: number};

// A stream that went off, or dropped, whose end is still held: the line
// above its button in the dialog, what the button says, and its name for a
// screen reader, which says whose stream it clips: two streams that went off
// are otherwise two buttons both called "Clip the end".
export function tailLine(tail: TailInput, now: number, lengthSeconds: number): {text: string; button: string; label: string} {
    const held = clipTime(Math.min(tail.heldMs, Math.max(0, lengthSeconds) * 1000));
    const minutesLeft = Math.max(1, Math.ceil((tail.until - now) / MINUTE_MS));
    const rest = `the last ${held} is held for ${plural(minutesLeft, 'more minute', 'more minutes')}.`;
    const whose = streamLabel({userId: tail.userId, name: tail.title});
    if (tail.reason === 'dropped') {
        return {text: `${whose} dropped ${agoWords(tail.endedAt, now)} — ${rest}`, button: 'Clip before the drop', label: `Clip ${whose} before the drop`};
    }

    return {text: `${tail.title} went off ${agoWords(tail.endedAt, now)} — ${rest}`, button: 'Clip the end', label: `Clip the end of ${whose}`};
}

// The ends of streams still held, by this browser's clock: the snapshot's
// clipTails less any whose time has run out since it came, so a surface lets
// one go when it goes rather than at the next snapshot. Given the live
// handles, only those of streams that are not live again: a live stream's
// own tile already offers the end before its drop.
export function heldTails<T extends {handle: string; until: number}>(tails: T[] | undefined, now: number, live?: string[]): T[] {
    const on = new Set(live || []);

    return (tails || []).filter((tail) => typeof tail.until === 'number' && tail.until > now && !on.has(tail.handle));
}

// When the first of them goes, for a surface to look again then; 0 for none.
export function nextTailEnd(tails: Array<{until: number}>): number {
    return tails.reduce((soonest, tail) => (soonest === 0 || tail.until < soonest ? tail.until : soonest), 0);
}

// Whether this browser can play a clip, by its codecs ("avc1.64000c,opus"):
// not at all, without its sound, or properly. canPlay is the video element's
// canPlayType.
export function playability(codecs: string, canPlay: (type: string) => string): 'ok' | 'no-video' | 'no-sound' {
    const parts = codecs.split(',').map((part) => part.trim()).filter(Boolean);
    const video = parts[0] || 'avc1.64001f';
    if (canPlay(`video/mp4; codecs="${video}"`) === '') {
        return 'no-video';
    }

    const opus = parts.some((part) => part.toLowerCase() === 'opus');
    if (opus && canPlay('audio/mp4; codecs="opus"') === '') {
        return 'no-sound';
    }

    return 'ok';
}

// ---- Best Clipper, in words (spec 3.12) ----

// What the award is called, in the one place it is spelled, as the Top
// Snooper's is in snooper.ts: the badge's tooltip, the profile's row and the
// Trophies tab all say it.
export const CLIPPER_TITLE = 'Best Clipper';

// What the clapper says to a screen reader, worded like the binoculars'.
export const CLIPPER_LABEL = `${CLIPPER_TITLE}, all time`;

// And when it is pointed at, where there is room to say what it is for.
export const CLIPPER_HINT = `${CLIPPER_TITLE} — the most upvoted clips, all time`;

// The holder's score: "142 upvotes · 9 clips" in the Trophies, and "All time
// · 142 upvotes on 9 clips" on their profile.
export function clipperScore(upvotes: number, clips: number): string {
    return `${upvotesInWords(upvotes)} · ${plural(Math.max(0, clips), 'clip')}`;
}

export function clipperAward(upvotes: number, clips: number): string {
    return `All time · ${upvotesInWords(upvotes)} on ${plural(Math.max(0, clips), 'clip')}`;
}

// ---- Where Clips are opened from: the header's clapper ----
// (spec 3.9, addendum 3.8, design/lock-screen.md 5 and 7). Only for somebody
// who may watch clips: linked, or let in by an admin. Nobody else has it — a
// locked reader's header is the wordmark alone, and they have no panel — and
// it is not there while "me" is not known. Somebody let in has no drafts,
// which are for clipping.

export type ClipsEntryInput = {
    // snapshot.clips: absent until the plugin is running.
    info?: {enabled: boolean; count: number};

    // What the reader may do, as "me" says: 'linked', 'letIn', or anything
    // else ('locked', 'unknown'), which has neither.
    access: string;

    // The reader's drafts still waiting to be published.
    drafts: number;

    // There are clips newer than the newest the reader has seen.
    unseen: boolean;
};

export type ClapperView = {
    // The button's name: "Clips — you have 2 drafts — new clips".
    label: string;

    // The red count on it, 0 for none.
    drafts: number;

    // The small dot on it, for clips the reader has not seen.
    dot: boolean;
};

export function clapperView(input: ClipsEntryInput): ClapperView | null {
    const {info, access} = input;
    if (!info || (access !== 'linked' && access !== 'letIn')) {
        return null;
    }

    const drafts = access === 'linked' ? Math.max(0, input.drafts) : 0;
    if (!info.enabled && info.count <= 0 && drafts === 0) {
        return null;
    }

    const dot = input.unseen && info.count > 0;
    let label = 'Clips';
    if (drafts > 0) {
        label += ` — you have ${plural(drafts, 'draft')}`;
    }
    if (dot) {
        label += ' — new clips';
    }

    return {label, drafts, dot};
}

// ---- The streamer's own controls (spec 3.11, addendum 3.5 and 3.8) ----
// In the panel's footer, for somebody whose stream the server keeps for
// clipping: what is kept, the switch to stop it, and a way to wipe what is
// held. Only while Clips are on, and only from what the server said: a
// linked reader whose "me" carries no clipsStreamer is one the server could
// not answer for, and gets nothing rather than a switch in a guessed
// position. Somebody who has not linked a stream (let in to watch only) has
// no switch at all — the server would refuse it — but is told when a stream
// of theirs, matched to them by their username, is being kept all the same
// (design/lock-screen.md question 8).
//
// The one exception is the downloads switch (3.2.1). Clips already made can
// still be watched, and downloaded while an admin allows it, when clipping is
// switched off or Clips cannot keep anything on this server just now, and a
// streamer can block downloads of their stream's clips whatever the admin
// allows (design/3.2.1.md 1.3). So while "me" says where that switch is, a
// linked streamer keeps it even then, alone in the sheet: there is nothing
// about recording to say or switch.

export type StreamerInput = {
    // snapshot.clips.enabled.
    clipsEnabled: boolean;

    // The reader's link; undefined while "me" is not known.
    linked: boolean | undefined;

    // me.clipsStreamer.allowed; undefined when "me" carried none.
    allowed?: boolean;

    // me.clipsStreamer.downloads (3.2.1): whether people may download clips
    // of the stream, undefined while an admin allows no downloads at all.
    downloads?: boolean;
    lengthSeconds: number;

    // The reader's own stream, linked by hand, while it is live: its
    // recording state, as the snapshot has it. null while it is not live.
    own: {clip?: string} | null;

    // The end of the reader's own stream, while the server holds it: after
    // it dropped, or after it went off. until is when it is let go, heldMs
    // how much of it there is, when known. null for none.
    ownTail: {until: number; heldMs?: number} | null;

    // This panel switched clips of the stream on a moment ago. The server
    // starts recording again at its next poll, and the stream says off until
    // then: that off is not news.
    justAllowed?: boolean;

    // A live stream matched to the reader by their username only, for
    // somebody who has not linked one. null for none.
    attributed: {clip?: string} | null;
    now: number;
};

// wipeWords is what Wipe says: the whole length while live, or as much as is
// held of a stream that has stopped. downloads is the second switch's
// position, there only while an admin allows downloads. 'downloads' is that
// switch alone, while Clips are off (see above).
export type StreamerView =
    | {kind: 'streamer'; notice: string; allowed: boolean; wipe: boolean; wipeWords: string; downloads?: boolean}
    | {kind: 'downloads'; downloads: boolean}
    | {kind: 'attributed'; notice: string};

export const STREAMER_SWITCH = 'Allow clips of my stream';
export const STREAMER_ATTRIBUTED = 'Your stream is being kept for clipping. Link it to switch that off.';
export const STREAMER_OFF = 'Clipping of your stream is off.';
export const STREAMER_OFF_DONE = 'Off. What was held is wiped, and drafts made from it are gone.';
export const STREAMER_ON_DONE = 'On. Your stream is kept for clipping again while you’re live.';
export const WIPE_CONFIRM = 'Click again to wipe';
export const WIPE_DONE = 'Wiped. Drafts made from it are gone; published clips are not — delete those from Clips.';

// A recording that is being kept, or about to be: a stream that has just
// come back after a drop starts again. One just allowed is picked up at the
// next poll, still saying off until then, which justAllowed covers; any
// other off is the server's, and is not called kept.
const KEEPING = new Set(['ready', 'starting']);

export function streamerView(input: StreamerInput): StreamerView | null {
    if (input.linked === undefined) {
        return null;
    }

    if (!input.clipsEnabled) {
        return input.linked && typeof input.downloads === 'boolean' ? {kind: 'downloads', downloads: input.downloads} : null;
    }

    const lengthMs = Math.max(0, input.lengthSeconds) * 1000;
    const held = clipTime(lengthMs);
    if (!input.linked) {
        // Only while it really is kept: a stream the server cannot record
        // (unsupported, queued, failed, out of room) holds nothing.
        const clip = input.attributed?.clip;
        if (clip === undefined || !KEEPING.has(clip)) {
            return null;
        }

        return {kind: 'attributed', notice: STREAMER_ATTRIBUTED};
    }

    if (input.allowed === undefined) {
        return null;
    }
    const downloads = typeof input.downloads === 'boolean' ? {downloads: input.downloads} : {};
    if (!input.allowed) {
        return {kind: 'streamer', notice: STREAMER_OFF, allowed: false, wipe: false, wipeWords: wipeLabel(input.lengthSeconds), ...downloads};
    }

    const tail = input.ownTail !== null && input.ownTail.until > input.now ? input.ownTail : null;
    const clip = input.own?.clip || '';
    let notice = `When you’re live, the last ${held} of your stream is kept on this server so viewers can clip it.`;
    let wipeWords = wipeLabel(input.lengthSeconds);
    if (input.own && (KEEPING.has(clip) || (clip === 'off' && input.justAllowed))) {
        notice = `Clips are on: the last ${held} of your stream is kept on this server so viewers can clip it.`;
    } else if (input.own) {
        notice = 'Clips are on, but your stream can’t be kept for clipping just now.';
    } else if (tail) {
        // Gone off, with the end of it still held: said, so the Wipe under
        // it has something to wipe; minutes as Just ended counts them.
        const tailTime = clipTime(typeof tail.heldMs === 'number' ? Math.min(tail.heldMs, lengthMs) : lengthMs);
        const minutesLeft = Math.max(1, Math.ceil((tail.until - input.now) / MINUTE_MS));
        notice = `Your stream has stopped: its last ${tailTime} is held here for ${plural(minutesLeft, 'more minute', 'more minutes')} ` +
            'so viewers who were watching can clip it.';
        wipeWords = `Wipe the last ${tailTime}`;
    }

    return {
        kind: 'streamer',
        notice,
        allowed: true,
        wipe: clip === 'ready' || tail !== null,
        wipeWords,
        ...downloads,
    };
}

// The switch's tooltip: what it does, and that it goes with the stream rather
// than the person (addendum 3.5).
export function streamerSwitchTitle(lengthSeconds: number): string {
    return `Whether this server keeps the last ${clipTime(Math.max(0, lengthSeconds) * 1000)} of your stream so viewers can clip it. ` +
        'The choice stays with the stream: unlinking leaves it as it is, and another stream you link starts with clips allowed.';
}

export function wipeLabel(lengthSeconds: number): string {
    return `Wipe the last ${clipTime(Math.max(0, lengthSeconds) * 1000)}`;
}

// "3 clips of your stream", from the last list fetched; '' for none.
export function ofMyStreamLine(count: number): string {
    return count > 0 ? `${plural(count, 'clip')} of your stream` : '';
}

// ---- The System Console's words (spec 3.14) ----

const KB = 1024;
const MB = KB * 1024;
const GB = MB * 1024;

// A size as the console says it: "38 MB", "1.8 GB", "20 GB". Gigabytes keep
// one decimal place below ten, where it still says something.
export function bytesInWords(bytes: number): string {
    const size = Math.max(0, bytes);
    if (size >= GB) {
        const gb = size / GB;
        const shown = gb < 10 ? Math.round(gb * 10) / 10 : Math.round(gb);

        return `${shown} GB`;
    }
    if (size >= MB) {
        return `${Math.round(size / MB)} MB`;
    }
    if (size >= KB) {
        return `${Math.round(size / KB)} KB`;
    }

    return `${Math.round(size)} B`;
}

// A stream's bitrate, from bits a second: "6 Mbit/s", "2.5 Mbit/s", "800 kbit/s".
export function bitrateInWords(bitsPerSecond: number): string {
    const rate = Math.max(0, bitsPerSecond);
    if (rate >= 1000000) {
        return `${Math.round(rate / 100000) / 10} Mbit/s`;
    }

    return `${Math.round(rate / 1000)} kbit/s`;
}

// Somebody as the console names them: "Bob (@bob)", or whichever of the two
// there is, or else their id.
export function adminPerson(person: {displayName?: string; username?: string; name?: string; userId?: string}): string {
    const display = (person.displayName || '').trim();
    const username = (person.username || '').trim();
    if (display !== '' && username !== '') {
        return `${display} (@${username})`;
    }
    if (username !== '') {
        return `@${username}`;
    }

    return display || (person.name || '').trim() || person.userId || 'Somebody';
}

export type RecorderInput = {
    title: string;
    state: string;
    heldSeconds: number;
    bitrate: number;
    truncated: boolean;
    reason: string;
};

// One recording now: "Dave — 2:00 held · 6 Mbit/s", "Carol — failing: ICE
// failed", "Judy — busy with other streams". reason is the server's own text
// for admins, said after the state it explains.
export function recorderLine(recorder: RecorderInput, lengthSeconds: number): string {
    const why = recorder.reason.trim();
    const because = why !== '' ? `: ${why}` : '';
    const name = recorder.title.trim() || 'A stream';
    switch (recorder.state) {
    case 'ready': {
        let line = `${name} — ${clipTime(Math.max(0, recorder.heldSeconds) * 1000)} held`;
        if (recorder.bitrate > 0) {
            line += ` · ${bitrateInWords(recorder.bitrate)}`;
        }
        if (recorder.truncated) {
            line += ` · too high a bitrate for the whole ${clipTime(Math.max(0, lengthSeconds) * 1000)}`;
        }

        return line;
    }
    case 'starting':
        return `${name} — starting`;
    case 'failed':
        return `${name} — failing${because}`;
    case 'queued':
        return `${name} — busy with other streams`;
    case 'full':
        return `${name} — out of room${because}`;
    case 'unsupported':
        return `${name} — can’t be clipped${because}`;
    case 'off':
        return `${name} — switched off by its streamer`;
    default:
        return `${name} — ${recorder.state}${because}`;
    }
}

// The end of a stream still held: "Dave — went off 3 min ago, held until
// 21:14". until is the time already written in the reader's clock.
export function heldTailLine(tail: {title: string; reason: string; endedAt: number}, now: number, until: string): string {
    const what = tail.reason === 'dropped' ? 'dropped' : 'went off';

    return `${tail.title.trim() || 'A stream'} — ${what} ${agoWords(tail.endedAt, now)}, held until ${until}`;
}

export type AdminClipInput = {
    clipper: {displayName?: string; username?: string; name: string; userId?: string};
    streamer: {name: string};
    tag?: {title: string};
    durationMs: number;
    createdAt: number;
    expiresAt: number;
    bytes: number;
    upvotes: number;
    reports?: number;
};

// A console line with a count of upvotes in it, in three parts, because the
// count is drawn as a thumbs up and the number rather than written: the words
// before it, the count, and the words after it with their own separator ('' for
// none). The console puts " · " between the first two.
export type AdminVotesLine = {before: string; upvotes: number; after: string};

// One clip, under its title: "Bob (@bob) clipped Dave · Hytale · 0:45 · 3 days
// ago · goes in 27 days · 38 MB", then its upvotes, then " · 2 reports"; the
// stream's tag only when it had one.
export function adminClipLine(clip: AdminClipInput, now: number): AdminVotesLine {
    const tag = tagTitleOf(clip.tag);
    const parts = [
        `${adminPerson(clip.clipper)} clipped ${clip.streamer.name}`,
        ...(tag !== '' ? [tag] : []),
        clipTime(clip.durationMs),
        agoWords(clip.createdAt, now),
        goesWords(clip.expiresAt, now),
        bytesInWords(clip.bytes),
    ];
    const reports = clip.reports || 0;

    return {
        before: parts.join(' · '),
        upvotes: Math.max(0, clip.upvotes),
        after: reports > 0 ? ` · ${plural(reports, 'report')}` : '',
    };
}

// One person with clips: "Bob (@bob) — 4 clips · 180 MB of 4 GB", then their
// upvotes, then " (12 banked)". upvotes is their whole score, the banked ones
// included.
export function adminPersonLine(person: {displayName: string; username: string; userId: string; clips: number; bytes: number; upvotes: number; banked: number},
    shareBytes: number): AdminVotesLine {
    const share = shareBytes > 0 ? ` of ${bytesInWords(shareBytes)}` : '';

    return {
        before: `${adminPerson(person)} — ${plural(Math.max(0, person.clips), 'clip')} · ${bytesInWords(person.bytes)}${share}`,
        upvotes: Math.max(0, person.upvotes),
        after: person.banked > 0 ? ` (${person.banked} banked)` : '',
    };
}

export type AdminSummaryInput = {
    count: number;
    usedBytes: number;
    limitBytes: number;
    shareBytes: number;
    freeBytes: number;
    totalBytes: number;
    ringBytes: number;

    // When the clip that goes first goes; 0 with no clips.
    oldestGoesAt: number;
    best?: {name: string; upvotes: number} | null;
};

// The console's summary line: "23 clips · 1.8 GB of 20 GB (4 GB each at most)
// · 410 GB free · recordings 410 MB · the oldest goes in 2 days · Best
// Clipper: Bob (142)". Free space only when the server could read it, and the
// recordings only while there are any.
export function adminSummary(input: AdminSummaryInput, now: number): string {
    let storage = `${bytesInWords(input.usedBytes)} of ${bytesInWords(input.limitBytes)}`;
    if (input.shareBytes > 0) {
        storage += ` (${bytesInWords(input.shareBytes)} each at most)`;
    }

    const parts = [plural(Math.max(0, input.count), 'clip'), storage];
    if (input.totalBytes > 0) {
        parts.push(`${bytesInWords(input.freeBytes)} free`);
    }
    if (input.ringBytes > 0) {
        parts.push(`recordings ${bytesInWords(input.ringBytes)}`);
    }
    if (input.count > 0 && input.oldestGoesAt > 0) {
        parts.push(`the oldest ${goesWords(input.oldestGoesAt, now)}`);
    }
    if (input.best) {
        parts.push(`${CLIPPER_TITLE}: ${input.best.name} (${Math.max(0, input.best.upvotes)})`);
    }

    return parts.join(' · ');
}

// After "Keep clips for" was lowered, what goes when its day of grace ends:
// "Keep clips for was lowered to 3 days: 41 clips will be removed at 21:04
// tomorrow. Set it back before then to keep them." when is already in words.
export function retentionLine(days: number, pending: number, when: string): string {
    const at = when.trim() !== '' ? ` ${when.trim()}` : '';

    return `Keep clips for was lowered to ${plural(Math.max(0, days), 'day')}: ` +
        `${plural(Math.max(0, pending), 'clip')} will be removed${at}. Set it back before then to keep them.`;
}

// What the console says while its Delete waits for the second press (the
// pill itself says only "Delete?"): deleting from here voids the clip's
// upvotes, as an admin's deletion always does.
export function adminDeleteConfirm(title: string, upvotes: number): string {
    if (upvotes <= 0) {
        return `Click again to delete ${title}.`;
    }

    return upvotes === 1 ?
        `Click again to delete ${title}: its 1 upvote stops counting.` :
        `Click again to delete ${title}: its ${upvotes} upvotes stop counting.`;
}
