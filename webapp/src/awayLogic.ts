// What is in Mattermost's side panel, decided from its store alone.
//
// Pure, and importing nothing, so the rules can be checked by a script without
// a browser. The store subscription, the timers and everything that touches the
// page live in away.ts.
//
// The state paths are the same from 9.0.0 to master (reducers/views/rhs.ts):
// rhsState says what kind of panel it is, pluggableId which plugin when it is a
// plugin's, and selectedPostId the root of an open thread. Opening a thread
// sets rhsState to null and pluggableId to '', so Mattermost forgets which
// plugin was there — which is why a thread can replace the panel but never hand
// the panel back to it.

export type RhsSlice = {
    rhsState: string | null;
    pluggableId: string;
    selectedPostId: string;
    selectedPostCardId?: string;
    isSidebarOpen: boolean;
    isSidebarExpanded: boolean;
};

// villanet: the panel is in the side panel.
// closed:   nothing is.
// thread:   a thread.
// own:      another of Mattermost's own panels: search, mentions, saved,
//           pinned, files, channel info, members, edit history, a post card.
// plugin:   another plugin's.
// empty:    open with nothing in it; what going back to a forgotten plugin, or
//           the thread's root being deleted, leaves.
export type Panel = 'villanet' | 'closed' | 'thread' | 'own' | 'plugin' | 'empty';

export function panelOf(r: RhsSlice, ours: string): Panel {
    if (!r.isSidebarOpen) {
        return 'closed';
    }

    if (r.rhsState === 'plugin') {
        if (r.pluggableId === ours) {
            return 'villanet';
        }

        // '' is a plugin panel Mattermost has forgotten the plugin of: going
        // back to it from a thread renders an empty panel.
        return r.pluggableId === '' ? 'empty' : 'plugin';
    }

    if (r.rhsState === null) {
        if (r.selectedPostId) {
            return 'thread';
        }

        // A deleted root clears selectedPostId and leaves the panel open.
        return r.selectedPostCardId ? 'own' : 'empty';
    }

    // Search, mentions, saved, pinned, files, channel info, members and edit
    // history. Edit history has a selectedPostId too, but it is not a thread.
    return 'own';
}

const text = (value: unknown): string => (typeof value === 'string' ? value : '');

// The side panel's state, or null when it is not shaped the way it has been
// from 9.0 to master. Then nothing acts on it at all: no coming back and no
// small player, and the streams simply end by the pool's grace, which is the
// safe way to be wrong.
export function rhsSliceOf(rhs: unknown): RhsSlice | null {
    if (!rhs || typeof rhs !== 'object') {
        return null;
    }

    const r = rhs as Record<string, unknown>;
    if (!(r.rhsState === null || typeof r.rhsState === 'string') || typeof r.isSidebarOpen !== 'boolean') {
        return null;
    }

    return {
        rhsState: r.rhsState as string | null,
        pluggableId: text(r.pluggableId),
        selectedPostId: text(r.selectedPostId),
        selectedPostCardId: text(r.selectedPostCardId),
        isSidebarOpen: r.isSidebarOpen,
        isSidebarExpanded: r.isSidebarExpanded === true,
    };
}

// Everything the side panel's story depends on, read from the store at once.
// path is the first segment of the address after Mattermost's base path, which
// changes when the person moves between products.
export type World = {
    rhs: RhsSlice;
    suppressed: boolean;
    mobile: boolean;
    teamId: string;
    path: string;
    channelId: string;
};

// ---- Where the panel stands ----

// A detour: the panel pushed aside by something else in the side panel, and
// what it means to do about it.
export type Detour = {
    phase: 'away';

    // own:        a thread, or another of Mattermost's own panels.
    // plugin:     another plugin's panel, which the panel never comes back over.
    // suppressed: the Threads page, which hides the side panel without
    //             changing what is in it; Mattermost puts the panel back itself.
    cause: 'own' | 'plugin' | 'suppressed';

    // Come back when the detour is closed, or left empty.
    returnOnEnd: boolean;

    // Come back once a reply from this tab lands in this thread.
    replyRoot: string | null;

    // A thread reopened with Back to thread: closing it brings the panel back,
    // and replying in it does not.
    noReplyFor: string | null;

    // Where the person was as the detour began. A team or product switch ends
    // it, and expansion is only put back in the same channel.
    teamId: string;
    path: string;
    channelId: string;

    // The panel's expansion as the detour began. A mouse Reply has already been
    // collapsed by Mattermost's click-outside rule by then, which is treated as
    // the person's own doing; only a keyboard-opened detour gets it back.
    expanded: boolean;

    // The stream the small player shows (see away.ts), and whether its Hide
    // was pressed.
    latched: string | null;
    hidden: boolean;
};

export type Away = {phase: 'idle'} | {phase: 'docked'} | Detour;

// The two per-person settings (userSettings.ts), read again whenever they
// decide anything, so a change takes effect in the middle of a detour.
export type Settings = {
    returnAfter: 'reply' | 'close' | 'never';
    mini: 'on' | 'off';
};

// Left by Back to thread for the step that sees the thread open: that thread
// is to bring the panel back on close only.
export type Hint = {root: string; until: number};

export type Ctx = {
    ours: string;
    settings: Settings;
    now: number;
    hint: Hint | null;

    // The stream the small player would show if a detour began now, worked
    // out by away.ts before each step (latchCandidate).
    latch: string | null;

    // Whether the reader may watch at all (linked or let in), as the page
    // shows their access. A locked person is never brought back after a reply
    // or a close: there is nothing of theirs to bring back, and the lock would
    // only cover the thread they were using (design/lock-screen.md 4.9).
    // Absent counts as able, which is how everything before the lock behaved.
    watchCapable?: boolean;
};

// What a step asks away.ts to do. Each is described where it is carried out.
export type Effect =
    | {kind: 'hold'}
    | {kind: 'release'}
    | {kind: 'stopAll'}
    | {kind: 'cancelPending'}
    | {kind: 'markDocked'}
    | {kind: 'clearPlaceholders'}
    | {kind: 'return'; expand: boolean};

// row is the transition table's row number (design/rhs-return-spec.md, 3.4),
// for the checks in build/check-away.mjs and for anybody reading a trace.
export type Step = {next: Away; effects: Effect[]; hintUsed: boolean; row: number};

const IDLE: Away = {phase: 'idle'};
const DOCKED: Away = {phase: 'docked'};
const HOLD: Effect = {kind: 'hold'};
const RELEASE: Effect = {kind: 'release'};
const STOP_ALL: Effect = {kind: 'stopAll'};
const CANCEL: Effect = {kind: 'cancelPending'};
const MARK_DOCKED: Effect = {kind: 'markDocked'};
const CLEAR_PLACEHOLDERS: Effect = {kind: 'clearPlaceholders'};

// Where the panel starts out when the controller is installed.
export function initialAway(world: World, ours: string): Away {
    return panelOf(world.rhs, ours) === 'villanet' && !world.suppressed ? DOCKED : IDLE;
}

function hinted(ctx: Ctx, root: string): boolean {
    return ctx.hint !== null && root !== '' && ctx.hint.root === root && ctx.now < ctx.hint.until;
}

// The detour a thread or one of Mattermost's own panels begins. A reply can
// only bring the panel back from a thread, only with the setting at "reply",
// and never from a thread Back to thread has just reopened.
export function arm(before: World, after: World, ctx: Ctx, keepLatched?: string | null): {detour: Detour; hintUsed: boolean} {
    const thread = panelOf(after.rhs, ctx.ours) === 'thread';
    const root = after.rhs.selectedPostId;
    const hint = thread && hinted(ctx, root);
    const capable = ctx.watchCapable !== false;

    return {
        detour: {
            phase: 'away',
            cause: 'own',
            returnOnEnd: capable && ctx.settings.returnAfter !== 'never',
            replyRoot: capable && thread && ctx.settings.returnAfter === 'reply' && !hint ? root : null,
            noReplyFor: hint ? root : null,
            teamId: before.teamId,
            path: before.path,
            channelId: before.channelId,
            expanded: before.rhs.isSidebarExpanded,
            latched: keepLatched ?? ctx.latch,
            hidden: false,
        },
        hintUsed: hint,
    };
}

// A detour that is not the panel's to end: another plugin's panel, or the
// Threads page.
function standAside(cause: 'plugin' | 'suppressed', before: World, ctx: Ctx): Detour {
    return {
        phase: 'away',
        cause,
        returnOnEnd: false,
        replyRoot: null,
        noReplyFor: null,
        teamId: before.teamId,
        path: before.path,
        channelId: before.channelId,
        expanded: before.rhs.isSidebarExpanded,
        latched: ctx.latch,
        hidden: false,
    };
}

// One change of Mattermost's side panel, from before to after. The rows are
// tried in order and the first that matches wins, as in the spec's table; the
// numbers are that table's.
export function step(prev: Away, before: World, after: World, ctx: Ctx): Step {
    const panel = panelOf(after.rhs, ctx.ours);
    const suppressed = after.suppressed;
    const mobile = after.mobile;
    const root = after.rhs.selectedPostId;
    const out = (row: number, next: Away, effects: Effect[] = [], hintUsed = false): Step => ({row, next, effects, hintUsed});

    if (prev.phase === 'idle') {
        return panel === 'villanet' && !suppressed ? out(1, DOCKED) : out(2, prev);
    }

    if (prev.phase === 'docked') {
        if (panel === 'villanet' && !suppressed) {
            return out(3, prev);
        }

        // The Threads page: nothing to dispatch, and Mattermost shows the panel
        // again by itself when the person leaves it.
        if (panel === 'villanet') {
            return out(4, standAside('suppressed', before, ctx), [HOLD]);
        }

        // The plugin's icon, Ctrl+., a team or product switch, the phone menu:
        // the television is switched off.
        if (panel === 'closed') {
            return out(5, IDLE, [STOP_ALL]);
        }

        // No detours on a phone-sized screen, where the side panel covers
        // everything: the panel's streams just get their minute.
        if (mobile && (panel === 'thread' || panel === 'own' || panel === 'plugin')) {
            return out(6, IDLE);
        }

        if (panel === 'thread' || panel === 'own') {
            const {detour, hintUsed} = arm(before, after, ctx);

            return out(panel === 'thread' ? 7 : 8, detour, [HOLD], hintUsed);
        }

        if (panel === 'plugin') {
            return out(9, standAside('plugin', before, ctx), [HOLD]);
        }

        return out(10, prev);
    }

    const away = prev;

    if (panel === 'villanet' && !suppressed) {
        return out(11, DOCKED, [CANCEL, RELEASE, MARK_DOCKED]);
    }

    if (panel === 'villanet') {
        return out(12, away);
    }

    // A team or product switch while away turns the television off, as it
    // does while the panel is showing.
    if (after.teamId !== away.teamId || after.path !== away.path) {
        return out(13, IDLE, [CANCEL, STOP_ALL]);
    }

    if (mobile) {
        return out(14, IDLE, [CANCEL, RELEASE]);
    }

    if ((panel === 'closed' || panel === 'empty') && away.returnOnEnd && !suppressed) {
        return out(15, away, [{kind: 'return', expand: away.expanded && after.channelId === away.channelId}]);
    }

    if (panel === 'closed' || panel === 'empty') {
        return out(16, IDLE, [CANCEL, RELEASE]);
    }

    if (panel === 'plugin') {
        return out(17, {...away, cause: 'plugin', returnOnEnd: false, replyRoot: null}, [CANCEL]);
    }

    if (away.cause === 'plugin') {
        return out(18, away);
    }

    if (away.cause === 'suppressed' && !suppressed) {
        const {detour, hintUsed} = arm(before, after, ctx, away.latched);

        return out(19, detour, [CANCEL], hintUsed);
    }

    if (away.cause === 'suppressed') {
        return out(20, away);
    }

    const changedThread = panel === 'thread' && root !== before.rhs.selectedPostId;
    if (changedThread && away.replyRoot !== null) {
        const hint = hinted(ctx, root);
        if (root === away.noReplyFor || hint) {
            return out(21, {...away, replyRoot: null, noReplyFor: root}, [CANCEL, CLEAR_PLACEHOLDERS], hint);
        }

        return out(21, {...away, replyRoot: root}, [CANCEL, CLEAR_PLACEHOLDERS]);
    }

    if (changedThread) {
        const hint = hinted(ctx, root);

        return out(22, hint ? {...away, noReplyFor: root} : away, [CANCEL], hint);
    }

    if (panel === 'own' && away.replyRoot !== null) {
        return out(23, {...away, replyRoot: null}, [CANCEL]);
    }

    return out(24, away);
}

// Checked again when the wait after a landed reply runs out: is the panel
// still meant to come back after a reply in this thread?
export function shouldReturnAfterLanded(away: Away, world: World, settings: Settings, root: string, ours: string): boolean {
    return away.phase === 'away' &&
        away.cause === 'own' &&
        away.replyRoot === root &&
        panelOf(world.rhs, ours) === 'thread' &&
        world.rhs.selectedPostId === root &&
        !world.suppressed &&
        !world.mobile &&
        settings.returnAfter === 'reply';
}

// The stream the small player shows: the one with the sound if it is
// watched, else the first watched stream still connected, else the first
// watched at all. It follows the picture that was in front, not the sound:
// the sound is released the moment its stream drops (watching.ts), and the
// small player should then say it is waiting rather than jump elsewhere.
//
// excluded says which watched streams it can never show: Twitch's, whose
// player is a frame of Twitch's own that cannot be moved without reloading
// and must never play hidden (design/twitch-live.md 6.7). They are left out
// before anything else is asked, the sound included: a Twitch stream with
// the sound would otherwise be latched, and the Broadcast Box stream beside
// it get no small player and be hung up.
export function latchCandidate(audio: string | null, watching: string[], connected: (handle: string) => boolean,
    watchCapable = true, excluded: (handle: string) => boolean = () => false): string | null {
    // Nothing for somebody who may not watch: the small player never shows
    // for a locked reader, whatever the watch list says.
    if (!watchCapable) {
        return null;
    }

    const playable = watching.filter((handle) => !excluded(handle));
    if (audio !== null && playable.includes(audio)) {
        return audio;
    }

    return playable.find(connected) ?? playable[0] ?? null;
}

// ---- A reply from this tab landing ----

// What a post in Mattermost's store looks like, as far as this is concerned.
export type PostLike = {
    id?: unknown;
    pending_post_id?: unknown;
    user_id?: unknown;
    failed?: unknown;
};

// Looks through a thread's reply ids for this tab's reply arriving.
//
// Sending a reply puts a placeholder in the thread first, whose id IS its
// pending_post_id, and the server's post replaces it with the same
// pending_post_id and an id of its own (redux actions/posts.ts, createPost).
// Only this tab ever sees its own placeholders, so a real post carrying the
// pending id of a placeholder seen here is a reply from this tab that the
// server has accepted. Another tab's or device's replies never match.
//
// A failed send re-receives the placeholder with failed set and the same id,
// already seen and so ignored; a retry reuses the pending id, so it can still
// land. seen and mine carry over from call to call, and are emptied when the
// thread changes.
export function findLanded(ids: readonly string[], posts: Record<string, PostLike | undefined>, me: string,
    seen: Set<string>, mine: Set<string>): boolean {
    let landed = false;
    for (const id of ids) {
        if (seen.has(id)) {
            continue;
        }
        seen.add(id);

        const post = posts[id];
        if (!post) {
            continue;
        }

        const pending = typeof post.pending_post_id === 'string' ? post.pending_post_id : '';
        if (pending !== '' && post.id === pending && post.user_id === me) {
            mine.add(pending);
        } else if (pending !== '' && mine.has(pending) && post.failed !== true) {
            landed = true;
        }
    }

    return landed;
}
