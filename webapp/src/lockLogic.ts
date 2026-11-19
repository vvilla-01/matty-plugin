// The lock screen's rules, without a page (design/lock-screen.md, sections 4
// and 7): what somebody may do in the panel, what the lock says, and how its
// bearer token bar moves from one state to the next.
//
// Pure and importing nothing, like awayLogic.ts and clipLogic.ts, so that
// build/check-away.mjs can run every rule here without a browser. Anything
// that needs the clock is handed the time, and anything that needs a window
// of time is handed its length (motion.ts keeps those numbers).

// ---- Who may do what ----

// What "me" says about the reader, as the server decided it: linked their own
// stream with their bearer token (everything), let in by a system admin
// (watch only), or neither (the lock). 'unknown' is the store not answering,
// or "me" not read at all; it is never a reason to lock or to unlock.
export type Access = 'linked' | 'letIn' | 'locked' | 'unknown';
export type DefiniteAccess = 'linked' | 'letIn' | 'locked';

const WORDS: Record<string, Access> = {linked: 'linked', letIn: 'letIn', locked: 'locked', unknown: 'unknown'};

export function isDefinite(access: Access): access is DefiniteAccess {
    return access === 'linked' || access === 'letIn' || access === 'locked';
}

// Watching streams and clips: linked people and let-in people.
export function mayWatch(access: Access): boolean {
    return access === 'linked' || access === 'letIn';
}

// Everything else in the panel: linked people only.
export function isLinked(access: Access): boolean {
    return access === 'linked';
}

// The reader's access from "me". No "me" is not known. A "me" about somebody
// other than whoever Mattermost says is signed in is somebody else's, and a
// "me" with no access in it, or a word this build does not know, comes from a
// server that has not said: both read as locked, so a mistake can only ever
// keep somebody out.
export function accessOfMe(me: {userId?: unknown; access?: unknown} | null | undefined, signedInUserId: string): Access {
    if (!me) {
        return 'unknown';
    }
    if (me.userId !== signedInUserId) {
        return 'locked';
    }

    return typeof me.access === 'string' && me.access in WORDS ? WORDS[me.access] : 'locked';
}

// What the page shows: the latest answer when it is a definite one, and
// otherwise the last definite answer this page had. A store that does not
// answer for a moment never shuts out somebody it already knew, nor shows a
// lock to somebody who was never told they were locked.
export function shownAccessOf(latest: Access, lastDefinite: DefiniteAccess | null): Access {
    if (isDefinite(latest)) {
        return latest;
    }

    return lastDefinite || 'unknown';
}

// How a definite answer differs from the one before it, for what the page
// has to undo: 'lost' is watching taken away (unlinked, a key taken over, let
// out), 'downgraded' is linked to let in, 'gained' is the lock opening.
export type AccessChange = 'none' | 'lost' | 'downgraded' | 'gained';

export function accessChangeOf(before: DefiniteAccess | null, after: DefiniteAccess): AccessChange {
    const could = before !== null && mayWatch(before);
    if (could && after === 'locked') {
        return 'lost';
    }
    if (before === 'linked' && after === 'letIn') {
        return 'downgraded';
    }
    if (!could && mayWatch(after)) {
        return 'gained';
    }

    return 'none';
}

// Why the lock came back, for its line: the reader's own Unlink in this page,
// a link lost any other way, or being let out.
export type LockCause = 'self' | 'unlinked' | 'letOut';

export function lossCauseOf(before: DefiniteAccess | null, selfUnlink: boolean): LockCause | null {
    if (before === 'letIn') {
        return 'letOut';
    }
    if (before === 'linked') {
        return selfUnlink ? 'self' : 'unlinked';
    }

    return null;
}

// ---- Bearer tokens ----

const BEARER = /^bearer(?:\s+|$)/i;

// What would be sent: trimmed, with one leading "Bearer " taken off, as the
// server does (cleanStreamKey) - and "Bearer" on its own is nothing at all.
export function cleanToken(raw: string): string {
    const trimmed = raw.trim();
    const prefix = BEARER.exec(trimmed);

    return prefix ? trimmed.slice(prefix[0].length).trim() : trimmed;
}

// An empty bar is never sent: "" is what unlinks.
export function isEmptyToken(raw: string): boolean {
    return cleanToken(raw) === '';
}

// Broadcast Box makes its tokens with uuid.New(): a message that is one, and
// nothing else, is a token about to be posted to a channel by mistake.
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function looksLikeToken(text: string): boolean {
    return UUID.test(cleanToken(typeof text === 'string' ? text : ''));
}

// ---- The bar's states (section 4.4) ----

// idle: editable. empty: Unlock pressed with nothing in the bar, and nothing
// sent. checking: the token is with the server, the bar read-only so focus
// and a screen reader's place stay put. refused: the server said no, the
// token kept so a typo can be fixed. unlocking: accepted, the token already
// gone from here.
export type TokenPhase = 'idle' | 'empty' | 'checking' | 'refused' | 'unlocking';

export type TokenState = {
    phase: TokenPhase;
    token: string;

    // What the server said no with: its code, and its own words for a code
    // this build has none for.
    code: string;
    text: string;
};

export type TokenAction =
    | {type: 'type'; value: string}
    | {type: 'submit'}
    | {type: 'refused'; code: string; text?: string}
    | {type: 'accepted'}
    | {type: 'escape'}
    | {type: 'reset'};

export const TOKEN_START: TokenState = {phase: 'idle', token: '', code: '', text: ''};

export function tokenReducer(state: TokenState, action: TokenAction): TokenState {
    const busy = state.phase === 'checking' || state.phase === 'unlocking';

    switch (action.type) {
    case 'type':
        if (busy) {
            return state;
        }

        // Typing clears an error: the person is fixing it.
        return {phase: 'idle', token: action.value, code: '', text: ''};
    case 'submit':
        if (busy) {
            return state;
        }
        if (isEmptyToken(state.token)) {
            return {...state, phase: 'empty', code: '', text: ''};
        }

        return {...state, phase: 'checking', code: '', text: ''};
    case 'refused':
        return {...state, phase: 'refused', code: action.code, text: action.text || ''};
    case 'accepted':
        return {phase: 'unlocking', token: '', code: '', text: ''};
    case 'escape':
        // An error state cleared, and nothing else: Escape is what people
        // press to dismiss an autofill list, and a pasted token should not
        // vanish with it.
        if (state.phase === 'refused' || state.phase === 'empty') {
            return {...state, phase: 'idle', code: '', text: ''};
        }

        return state;
    case 'reset':
        return TOKEN_START;
    default:
        return state;
    }
}

// Whether the bar takes typing in this state.
export function tokenEditable(state: TokenState): boolean {
    return state.phase !== 'checking' && state.phase !== 'unlocking';
}

// ---- Words (section 4.3) ----

// Every sentence that names the plugin takes the server's name for it (name,
// from name.ts nameNow or useName) at the moment it is said: this file
// imports nothing, so the caller hands it over.
export const lockNotAnswering = (name: string): string => `${name} is not answering. Try again in a moment.`;

// The not-set-up face's one sentence, the same for everybody, system admins
// included: an admin sets the plugin up in the System Console, which says
// how. And the status line's words when Unlock is refused for the same
// reason, which the server words the same (server/api.go linkNotSetUp).
export const lockNotSetUp = (name: string): string => `${name} is not set up yet, so it can’t be unlocked.`;

// The one refusal the panel raises itself: a link answered with no stream in
// it (LockScreen.tsx, TokenBar). The words the server uses for a token it
// turns down.
export const TOKEN_NOT_ACCEPTED = 'That token was not accepted. Check it and try again.';

// What the status line says for a /me/link refusal: the server's sentence, as
// it is sent. The server words every refusal (server/api.go, the link…
// sentences), and neither this panel nor Matty keeps a copy of them. The
// panel's own words are for no answer at all, or the plugin restarting (404
// everywhere), which is nobody's sentence.
export function lockCopy(code: string, serverText: string, name: string): string {
    if (code === '' || code === 'no_answer' || code === 'network') {
        return lockNotAnswering(name);
    }

    return serverText.trim() !== '' ? serverText.trim() : lockNotAnswering(name);
}

// The empty bar's words: on the lock and the let-in footer, and in the linked
// relink form, where an empty bar once meant unlink.
export const TOKEN_EMPTY = 'Paste your bearer token first.';
export const RELINK_EMPTY = 'Paste a bearer token to relink.';
export const TOKEN_CHECKING = 'Checking your token…';
export const UNLOCKED_WORDS = 'Unlocked.';
export const LET_IN_WORDS = 'An admin let you in to watch.';

// Said, from PanelGate's own live region, when the panel changes under the
// reader without their asking — a screen reader would otherwise hear only
// that it arrived in the panel again, or, with focus in the message box,
// nothing at all: the panel locking (with the lock's line, which says why),
// a let-in panel becoming a linked one, and the other way round.
export const LINKED_WORDS = 'Linked.';
export const WATCH_ONLY_WORDS = 'Unlinked. You can still watch.';

export function lockedWords(line: string, name: string): string {
    return line === '' ? `${name} is locked.` : `${name} is locked. ${line}`;
}

// The status line by the bar, and whether it is an error.
export function tokenStatus(state: TokenState, name: string, relink = false): {text: string; error: boolean} {
    switch (state.phase) {
    case 'empty':
        return {text: relink ? RELINK_EMPTY : TOKEN_EMPTY, error: true};
    case 'checking':
        return {text: TOKEN_CHECKING, error: false};
    case 'refused':
        return {text: lockCopy(state.code, state.text, name), error: true};
    default:
        return {text: '', error: false};
    }
}

// Where to find a bearer token. The let-in footer and the relink sheet say it
// under their bars all the time; the lock has no room for a hint of its own,
// so it says it only when it is needed: after an empty Unlock, and after a
// token refused as wrong. Somebody who knows their token never reads it.
export const TOKEN_HINT = 'OBS calls it Bearer Token, under Settings → Stream.';

const HINTED_REFUSALS = new Set(['not_accepted', 'too_long']);

// The lock's status line: the same words as tokenStatus, with the hint after
// the ones that mean the person may not know what to paste.
export function lockStatus(state: TokenState, name: string): {text: string; error: boolean} {
    const status = tokenStatus(state, name);
    const hinted = state.phase === 'empty' || (state.phase === 'refused' && HINTED_REFUSALS.has(state.code));

    return hinted ? {...status, text: `${status.text} ${TOKEN_HINT}`} : status;
}

// What was asked for from outside the panel while it was locked: a stream
// from a profile popover's Watch, or a clip from a card or a link. Stamped,
// so an unlock long after does not start something nobody asked for now.
export type LockPendingItem =
    | {kind: 'stream'; handle: string; name: string; at: number}
    | {kind: 'clip'; clipId: string; at: number};

// Whether something stamped at is still good at now, within windowMs. A stamp
// from the future (a clock put back) is not good.
export function fresh(at: number, now: number, windowMs: number): boolean {
    return at <= now && now - at < windowMs;
}

export const lineNoSignal = (name: string): string => `${name} is not answering, so it can’t tell whether you’re unlocked.`;
export const lineClip = (name: string): string => `Unlock ${name} to watch this clip.`;
export const LINE_SELF = 'You unlinked your stream.';
export const LINE_UNLINKED = 'Your stream was unlinked here, by an admin or because another account linked it.';
export const LINE_LET_OUT = 'You are no longer let in.';

// The lock's one line under its emblem, or '' for none: the padlock already
// says it is locked, and the bar what to do. A reason, never a way in: the
// lock does not say that anybody can be let in without a token, or whom to
// ask (Victor, 2026-10-06: "Don't advertise that"), and it has nothing that
// only a system admin reads, who finds it all in the System Console.
export function lockLine(input: {pending: LockPendingItem | null; cause: LockCause | null; noSignal: boolean; now: number; pendingMs: number; name: string}): string {
    if (input.noSignal) {
        return lineNoSignal(input.name);
    }

    const pending = input.pending && fresh(input.pending.at, input.now, input.pendingMs) ? input.pending : null;
    if (pending?.kind === 'stream') {
        return `Unlock ${input.name} to watch ${pending.name.trim() || 'this stream'}.`;
    }
    if (pending?.kind === 'clip') {
        return lineClip(input.name);
    }

    switch (input.cause) {
    case 'self':
        return LINE_SELF;
    case 'unlinked':
        return LINE_UNLINKED;
    case 'letOut':
        return LINE_LET_OUT;
    default:
        return '';
    }
}
