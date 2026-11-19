import React from 'react';
import type {Store} from 'redux';

import {findLanded, initialAway, latchCandidate, panelOf, rhsSliceOf, shouldReturnAfterLanded, step} from './awayLogic';
import type {Away, Effect, Hint, Panel, PostLike, RhsSlice, World} from './awayLogic';
import {accessNow} from './identity';
import {mayWatch} from './lockLogic';
import {channelOf} from './mattermost';
import {nameNow} from './name';
import {startFocusGuard} from './focusGuard';
import {RETURN_DELAY_MS} from './motion';
import {getSessionInfo, holdSession, HOLD_MS, stopAllSessions} from './sessions';
import {isTwitchHandle} from './twitch';
import {currentSettings} from './userSettings';
import {clearWatching, getAudio, getWatching, onWatchingChange} from './watching';

// Where the panel is in Mattermost's side panel, followed from Mattermost's own
// store, and what that means for the streams.
//
// Replying to a message opens Mattermost's thread in the side panel in
// the panel's place, and Mattermost gives a thread no way back to a plugin: it
// forgets which plugin was there (reducers/views/rhs.ts). So the panel
// remembers for it. The rules are in awayLogic.ts, which is pure; this is
// everything that touches the page:
//
//   * The plugin's icon, Ctrl+. while it is showing, a team or product switch,
//     or the phone layout's menu CLOSE the side panel while the panel is in
//     it. That switches the television off: every stream is hung up at once,
//     as it always was. The watch list is kept, as it always was.
//   * A thread, a search or another plugin taking its place is a detour.
//     The panel comes back by itself a moment after a reply from this tab
//     lands in the thread, or as soon as the thread (or the search, or any
//     other of Mattermost's own panels) is closed — never over another
//     plugin's panel. Meanwhile the stream that was in front goes on playing
//     in a small player over the channel (MiniPlayer.tsx).
//   * Coming back, the panel picks the same connections up again from the
//     session pool (sessions.ts) without reconnecting anything.
//
// It never dispatches from inside Mattermost's store listener: every
// dispatch is deferred and the state checked again first. Mattermost's own
// components may render before or after this listener runs depending on the
// kind of React root it uses, and nothing here depends on which.

export type WindowKind = 'main' | 'channel-popout' | 'other-popout';

// The lab's stand-in for being another kind of window, for its shared clips'
// cards and the lock's openings; null everywhere else.
let kindForLab: WindowKind | null = null;

export function pretendWindowKind(kind: WindowKind | null): void {
    kindForLab = kind;
}

type MattermostWindow = Window & {basename?: unknown};

// The address after Mattermost's own base path, which it keeps in
// window.basename when it is served under one (root.tsx).
function localPath(): string {
    const base = (window as MattermostWindow).basename;
    const path = window.location.pathname;
    if (typeof base === 'string' && base !== '' && path.startsWith(base)) {
        return path.slice(base.length);
    }

    return path;
}

// Which kind of window this is. A popped-out channel renders
// Mattermost's own side panel beside the channel, so the panel sits there in an
// ordinary side panel and is closed and covered there exactly as in the main
// window. Every other popped-out window (a thread, help, or the plugin's own
// panel on its own) has no side panel to follow; closing the window is what
// ends the streams in it.
export function windowKind(): WindowKind {
    if (kindForLab !== null) {
        return kindForLab;
    }

    const path = localPath();
    if (path.startsWith('/_popout/channel/')) {
        return 'channel-popout';
    }

    if (path.startsWith('/_popout/') || document.querySelector('.rhs-popout') !== null) {
        return 'other-popout';
    }

    return 'main';
}

export type Phase = 'idle' | 'docked' | 'away';

// The "Back to thread" strip's thread, set when a reply brought the panel back.
export type Undo = {root: string; channelId: string; channelName: string; shownAt: number};

// idle:   the panel is not in the side panel.
// docked: it is, and showing.
// away:   it has been pushed aside, and the streams carry on without it.
export type AwayView = {
    phase: Phase;
    cause?: 'own' | 'plugin' | 'suppressed';
    panel: Panel;
    suppressed: boolean;
    mobile: boolean;
    channelId: string;

    // The stream the small player shows, and whether its Hide was pressed.
    latched: string | null;
    hidden: boolean;

    // When the panel is due back after a landed reply (Date.now() time), or 0.
    returningUntil: number;
    undo: Undo | null;
};

const IDLE_VIEW: AwayView = {
    phase: 'idle',
    panel: 'closed',
    suppressed: false,
    mobile: false,
    channelId: '',
    latched: null,
    hidden: false,
    returningUntil: 0,
    undo: null,
};

// How long a press of Back to thread is remembered for, so the step that
// sees that thread open knows it is the one to come back from on close only.
const HINT_MS = 2000;

// How long after coming back the panel still counts as having come back, for
// the panel deciding whether to play its boot sweep. Long enough to cover a
// React that renders after this listener rather than before it.
const CAME_BACK_MS = 1000;

type Deps = {
    store: Store;

    // The id Mattermost gave the plugin's side-panel registration, which is
    // what pluggableId holds while the panel is showing.
    sidebarId: string;

    // Opens the panel: the registration's showRHSPlugin.
    show: () => void;

    // Whether this window has a small player to show while the panel is away:
    // the main window, with a place to mount it registered.
    miniHost: boolean;
};

// ---- The controller's state ----

let deps: Deps | null = null;
let state: Away = {phase: 'idle'};
let world: World | null = null;
let panel: Panel = 'closed';
let hint: Hint | null = null;
let hintTimer = 0;

// The wait after a landed reply, and the listeners that cancel it.
let pending = 0;
let returningUntil = 0;
let unlistenEngagement: (() => void) | null = null;

// The reply-landed watch: the thread's reply ids last looked at, and the ids
// seen and this tab's placeholders among them (awayLogic.ts, findLanded).
let lastIds: unknown;
let seen = new Set<string>();
let mine = new Set<string>();

// Releases for the holds this controller has put on sessions.
let holds: Array<() => void> = [];

let dockedFromAwayAt = -Infinity;
let focusAtDisplacement: HTMLElement | null = null;
let undo: Undo | null = null;
let stopGuard: (() => void) | null = null;

// Whether the small player is on screen, as it reports itself, and whether it
// has been at any point in this detour.
let miniShown = false;
let miniShownThisDetour = false;

// The small player showed "Waiting for" this stream, so it may start it again
// when it comes back (see MiniPlayer.tsx). Cleared when the latch moves.
let waitingShown: string | null = null;

let view: AwayView = IDLE_VIEW;
const listeners = new Set<() => void>();

function sameView(a: AwayView, b: AwayView): boolean {
    return a.phase === b.phase && a.cause === b.cause && a.panel === b.panel && a.suppressed === b.suppressed &&
        a.mobile === b.mobile && a.channelId === b.channelId && a.latched === b.latched && a.hidden === b.hidden &&
        a.returningUntil === b.returningUntil && a.undo === b.undo;
}

function publish(): void {
    const next: AwayView = {
        phase: state.phase,
        cause: state.phase === 'away' ? state.cause : undefined,
        panel,
        suppressed: world?.suppressed || false,
        mobile: world?.mobile || false,
        channelId: world?.channelId || '',
        latched: state.phase === 'away' ? state.latched : null,
        hidden: state.phase === 'away' ? state.hidden : false,
        returningUntil,
        undo,
    };
    if (sameView(next, view)) {
        return;
    }

    view = next;
    Array.from(listeners).forEach((listener) => listener());
}

export function getAway(): AwayView {
    return view;
}

// The watching.ts pattern: no useSyncExternalStore, which Mattermost's React
// 17 does not have.
export function useAway(): AwayView {
    const [, forceRender] = React.useState(0);
    const rendered = React.useRef(view);
    rendered.current = view;

    React.useEffect(() => {
        const listener = () => {
            if (view !== rendered.current) {
                forceRender((value) => value + 1);
            }
        };
        listeners.add(listener);

        // Anything that changed between the render and now.
        listener();

        return () => {
            listeners.delete(listener);
        };
    }, []);

    return view;
}

// ---- Reading Mattermost's store ----

// Only the parts read here, every one of them optional and checked: it is
// somebody else's state, and its shape is theirs to change.
type State = {
    views?: {
        rhs?: unknown;
        rhsSuppressed?: unknown;
        browser?: {windowSize?: unknown};
    };
    entities?: {
        users?: {currentUserId?: unknown};
        teams?: {currentTeamId?: unknown};
        channels?: {currentChannelId?: unknown};
        posts?: {
            posts?: Record<string, (PostLike & {channel_id?: unknown; delete_at?: unknown}) | undefined>;
            postsInThread?: Record<string, unknown>;
        };
    };
};

const text = (value: unknown): string => (typeof value === 'string' ? value : '');

function readState(): State | null {
    if (!deps) {
        return null;
    }

    try {
        return (deps.store.getState() as State | undefined) || {};
    } catch {
        return null;
    }
}

function worldOf(raw: State, rhs: RhsSlice): World {
    return {
        rhs,
        suppressed: raw.views?.rhsSuppressed === true,
        mobile: raw.views?.browser?.windowSize === 'mobileView',
        teamId: text(raw.entities?.teams?.currentTeamId),
        path: localPath().split('/')[1] || '',
        channelId: text(raw.entities?.channels?.currentChannelId),
    };
}

function readWorld(): World | null {
    const raw = readState();
    const slice = raw ? rhsSliceOf(raw.views?.rhs) : null;

    return raw && slice ? worldOf(raw, slice) : null;
}

function sidebarRight(): HTMLElement | null {
    return document.getElementById('sidebar-right');
}

// Something on the page outside the side panel, which is where Mattermost
// had focus before the side panel changed, and where it should stay.
function focusedOutsidePanel(): HTMLElement | null {
    const active = document.activeElement as HTMLElement | null;
    const rhs = sidebarRight();
    if (!active || active === document.body || (rhs && rhs.contains(active))) {
        return null;
    }

    return active;
}

// ---- What a step asks for ----

// The stream the small player would show if a detour began now.
function candidate(): string | null {
    return latchCandidate(getAudio(), getWatching(), (handle) => getSessionInfo(handle) !== null, watchCapable(), isTwitchHandle);
}

// Whether the reader may watch at all, as the page shows their access: a
// locked reader is never brought back, and has no small player
// (design/lock-screen.md 4.9).
function watchCapable(): boolean {
    return mayWatch(accessNow());
}

// Keeps the small player's stream connected while it cannot be seen: under
// the Threads page, or before the small player has appeared. Only that one
// stream, only with the small player switched on, and never on a phone-sized
// screen or in a window without a small player. Bounded by HOLD_MS, then the
// pool's minute of grace.
function hold(): void {
    if (!deps?.miniHost || state.phase !== 'away' || state.hidden || world?.mobile) {
        return;
    }

    const latched = state.latched;
    if (latched === null || currentSettings().mini !== 'on' || getSessionInfo(latched) === null) {
        return;
    }

    holds.push(holdSession(latched, HOLD_MS));
}

function release(): void {
    const all = holds;
    holds = [];
    all.forEach((done) => done());
}

function clearHint(): void {
    hint = null;
    window.clearTimeout(hintTimer);
    hintTimer = 0;
}

function clearPlaceholders(): void {
    seen = new Set();
    mine = new Set();
    lastIds = undefined;
}

function cancelPending(): void {
    window.clearTimeout(pending);
    pending = 0;
    unlistenEngagement?.();
    unlistenEngagement = null;
    returningUntil = 0;
}

function stopFocusGuard(): void {
    stopGuard?.();
    stopGuard = null;
}

// Back to idle without coming back: the small player goes, and the streams
// get the pool's minute.
function endAway(): void {
    cancelPending();
    release();
    state = {phase: 'idle'};
    undo = null;
    waitingShown = null;
    clearPlaceholders();
    publish();
}

function apply(effect: Effect): void {
    switch (effect.kind) {
    case 'hold':
        hold();
        break;
    case 'release':
        release();
        break;
    case 'stopAll':
        stopAllSessions('closed');
        release();
        break;
    case 'cancelPending':
        cancelPending();
        break;
    case 'markDocked':
        dockedFromAwayAt = performance.now();

        // After a reply, what the strip at the top of the panel says. It
        // is said from here, through the small player's region, which is
        // already in the page: the panel and its strip are only now being
        // put back, and a live region that arrives with its words in it is
        // not read out. Otherwise said only where the small player was
        // showing something.
        if (undo !== null) {
            announce(`${replyPostedWords(undo.channelName)} ${backToThreadWords(nameNow())}`);
        } else if (miniShownThisDetour) {
            announce(`${nameNow()} is back in the side panel.`);
        }
        break;
    case 'clearPlaceholders':
        clearPlaceholders();
        break;
    case 'return':
        returnOnClose(effect.expand);
        break;
    }
}

// ---- Coming back when the detour is closed ----

// After the dispatch that closed the detour has finished, never inside it.
// Mattermost has restored focus by then if it had somewhere to restore it to
// (sidebar_right.tsx, handleRHSFocus), and the focus guard keeps it there.
function returnOnClose(expand: boolean): void {
    cancelPending();
    Promise.resolve().then(() => {
        if (!deps || state.phase !== 'away') {
            return;
        }

        const now = readWorld();
        const p = now ? panelOf(now.rhs, deps.sidebarId) : null;
        const ok = now !== null && (p === 'closed' || p === 'empty') && !now.suppressed && !now.mobile &&
            now.teamId === state.teamId && now.path === state.path && currentSettings().returnAfter !== 'never';
        if (!ok) {
            endAway();

            return;
        }

        const target = focusedOutsidePanel() || (focusAtDisplacement?.isConnected ? focusAtDisplacement : 'panel');
        deps.show();
        if (expand) {
            try {
                deps.store.dispatch({type: 'SET_RHS_EXPANDED', expanded: true});
            } catch {
                // Left collapsed.
            }
        }
        startReturnFocusGuard(target);
    }).catch(() => undefined);
}

// ---- Coming back after a reply ----

function armedForReply(): string | null {
    if (state.phase !== 'away' || state.cause !== 'own' || state.replyRoot === null || panel !== 'thread' ||
        !world || world.rhs.selectedPostId !== state.replyRoot || world.suppressed) {
        return null;
    }

    return state.replyRoot;
}

// Cheap on the hot path: the thread's reply ids are compared by identity, and
// only looked through when Mattermost has replaced them.
function watchReplies(raw: State, root: string): void {
    const ids = raw.entities?.posts?.postsInThread?.[root];
    if (ids === lastIds) {
        return;
    }
    lastIds = ids;
    if (!Array.isArray(ids)) {
        return;
    }

    const posts = raw.entities?.posts?.posts || {};
    const me = text(raw.entities?.users?.currentUserId);
    if (me !== '' && findLanded(ids as string[], posts, me, seen, mine)) {
        landed(root);
    }
}

// A reply from this tab is in. The panel comes back in a moment, unless the
// person carries on in the thread meanwhile: typing in the reply box, or
// pressing, scrolling or keying anywhere in the side panel. Any of those
// cancels this return and leaves the thread armed for the next reply or for
// being closed.
function landed(root: string): void {
    cancelPending();

    const engaged = (event: Event) => {
        const target = event.target;
        if (event.type === 'input') {
            if (target instanceof HTMLElement && target.id === 'reply_textbox') {
                cancelPending();
                publish();
            }

            return;
        }

        const rhs = sidebarRight();
        if (rhs && target instanceof Node && rhs.contains(target)) {
            cancelPending();
            publish();
        }
    };
    const kinds = ['input', 'pointerdown', 'wheel', 'keydown'];
    kinds.forEach((kind) => document.addEventListener(kind, engaged, true));
    unlistenEngagement = () => kinds.forEach((kind) => document.removeEventListener(kind, engaged, true));

    returningUntil = Date.now() + RETURN_DELAY_MS;
    pending = window.setTimeout(() => fire(root), RETURN_DELAY_MS);
    publish();
}

// The wait is over. Comes back only if nothing has changed the plan and the
// person is plainly finished with the thread: nothing left in the reply box,
// no edit open, and focus not on anything in the thread other than the empty
// reply box, nor in a menu, an emoji picker or a dialog. Otherwise it stays
// armed: the next reply, or closing the thread, still brings the panel back.
function fire(root: string): void {
    unlistenEngagement?.();
    unlistenEngagement = null;
    pending = 0;
    returningUntil = 0;

    const now = readWorld();
    if (!deps || !now || !shouldReturnAfterLanded(state, now, currentSettings(), root, deps.sidebarId)) {
        publish();

        return;
    }

    const rhs = sidebarRight();
    const box = rhs?.querySelector('#reply_textbox') as HTMLTextAreaElement | HTMLInputElement | null;
    const draftEmpty = (!box || String(box.value || '').trim() === '') &&
        !rhs?.querySelector('.file-preview__container') &&
        !rhs?.querySelector('#edit_textbox');
    const active = document.activeElement as HTMLElement | null;
    const popup = Boolean(active?.closest('.a11y__popup, .a11y__modal, [role="dialog"], [aria-modal="true"]'));
    const inside = Boolean(active && rhs && rhs.contains(active));
    const focusOk = !active || active === document.body || !inside || active.id === 'reply_textbox';
    if (!draftEmpty || popup || !focusOk) {
        publish();

        return;
    }

    const raw = readState();
    const channelId = text(raw?.entities?.posts?.posts?.[root]?.channel_id);
    const channel = channelId === '' ? undefined : channelOf(channelId);
    const direct = channel?.type === 'D' || channel?.type === 'G';
    undo = {
        root,
        channelId,
        channelName: channel && !direct ? channel.display_name || channel.name || '' : '',
        shownAt: Date.now(),
    };
    publish();

    deps.show();

    // Never the message box: keystrokes there would be posted into the
    // channel. The panel itself, one Tab from Back to thread, unless focus
    // was already somewhere outside the side panel, where it stays.
    startReturnFocusGuard(focusedOutsidePanel() || 'panel');
}

// ---- Focus after a return ----

// See focusGuard.ts, which the lock screen shares: the panel itself, or what
// had focus outside the side panel, once Mattermost has finished moving it.
function startReturnFocusGuard(target: HTMLElement | 'panel'): void {
    stopFocusGuard();
    stopGuard = startFocusGuard(() => {
        const panelElement = document.querySelector('#sidebar-right .vntv-panel') as HTMLElement | null;

        return target === 'panel' || !target.isConnected ? panelElement : target;
    });
}

// ---- Following the store ----

let unsubscribe: (() => void) | null = null;
let unwatch: (() => void) | null = null;

// Compared by identity, so the listener costs next to nothing on the great
// majority of Mattermost's dispatches, which touch none of these.
// combineReducers only replaces a slice that changed.
let lastRhs: unknown;
let lastSuppressed: unknown;
let lastSize: unknown;
let lastTeam: unknown;
let lastChannel: unknown;
let first = true;

function check(): void {
    const raw = readState();
    if (!deps || !raw) {
        return;
    }

    const rhs = raw.views?.rhs;
    const suppressed = raw.views?.rhsSuppressed;
    const size = raw.views?.browser?.windowSize;
    const team = raw.entities?.teams?.currentTeamId;
    const channel = raw.entities?.channels?.currentChannelId;
    if (first || rhs !== lastRhs || suppressed !== lastSuppressed || size !== lastSize || team !== lastTeam || channel !== lastChannel) {
        first = false;
        lastRhs = rhs;
        lastSuppressed = suppressed;
        lastSize = size;
        lastTeam = team;
        lastChannel = channel;
        follow(raw);
    }

    const root = armedForReply();
    if (root !== null) {
        watchReplies(raw, root);
    }
}

function follow(raw: State): void {
    if (!deps) {
        return;
    }

    const slice = rhsSliceOf(raw.views?.rhs);
    if (!slice) {
        // Not the side panel this was written against: inert, which is the
        // behaviour from before any of this existed.
        if (state.phase === 'away') {
            endAway();
        }
        state = {phase: 'idle'};
        world = null;
        panel = 'closed';
        publish();

        return;
    }

    const next = worldOf(raw, slice);
    const before = world;
    world = next;
    const previousPanel = panel;
    panel = panelOf(next.rhs, deps.sidebarId);

    if (!before) {
        state = initialAway(next, deps.sidebarId);
        publish();

        return;
    }

    const result = step(state, before, next, {
        ours: deps.sidebarId,
        settings: currentSettings(),
        now: Date.now(),
        hint,
        latch: candidate(),
        watchCapable: watchCapable(),
    });

    const from: Away['phase'] = state.phase;
    const to: Away['phase'] = result.next.phase;
    state = result.next;
    if (from !== 'away' && to === 'away') {
        // Where focus was as the detour began, outside the side panel: what
        // Mattermost saved to restore on close, since it only moves focus in
        // timers, and so where a close-return puts it back.
        focusAtDisplacement = focusedOutsidePanel();
        miniShownThisDetour = false;
        waitingShown = null;
        clearPlaceholders();
    }
    if (to === 'idle' || (from === 'docked' && to === 'away')) {
        undo = null;
    }
    if (to !== 'away') {
        waitingShown = null;
    }
    if (panel !== previousPanel) {
        clearHint();
    }

    result.effects.forEach(apply);
    publish();
}

// The small player's stream has left the watch list: switched off, or gone
// from the air for longer than its tile waits. The small player moves to the
// next candidate, and so does its hold.
function relatch(): void {
    if (state.phase !== 'away' || state.latched === null || getWatching().includes(state.latched)) {
        return;
    }

    state = {...state, latched: candidate()};
    waitingShown = null;
    release();
    hold();
    publish();
}

export function installAway(next: Deps): () => void {
    if (windowKind() === 'other-popout') {
        return () => undefined;
    }

    deps = next;
    state = {phase: 'idle'};
    world = null;
    panel = 'closed';
    first = true;
    unsubscribe = next.store.subscribe(check);
    unwatch = onWatchingChange(relatch);
    check();

    return () => {
        unsubscribe?.();
        unsubscribe = null;
        unwatch?.();
        unwatch = null;
        cancelPending();
        stopFocusGuard();
        release();
        clearHint();
        clearPlaceholders();
        deps = null;
        state = {phase: 'idle'};
        world = null;
        panel = 'closed';
        undo = null;
        waitingShown = null;
        miniShown = false;
        miniShownThisDetour = false;
        focusAtDisplacement = null;
        dockedFromAwayAt = -Infinity;
        announcement = {text: '', n: announcement.n + 1};
        Array.from(announcers).forEach((listener) => listener());
        publish();
    };
}

// ---- For the panel, the strip and the small player ----

// Whether the panel mounting now is coming back from a detour, so
// that it comes back as it left, without its boot sweep. True while still
// away (Mattermost rendered before this listener ran) and for a moment after
// docking (it rendered after).
export function cameBack(): boolean {
    return state.phase === 'away' || performance.now() - dockedFromAwayAt < CAME_BACK_MS;
}

// The small player's Back to button, and anything else that brings the panel
// back by hand.
export function backToPanel(): void {
    deps?.show();
}

// Whether Back to thread can reopen the strip's thread: it still exists, and
// there is a side panel to open it in.
export function canGoBackToThread(): boolean {
    if (!undo || !world || world.suppressed || world.mobile) {
        return false;
    }

    const post = readState()?.entities?.posts?.posts?.[undo.root];

    return Boolean(post) && (post?.delete_at === undefined || post.delete_at === 0);
}

// Reopens the thread the panel came back from. That thread then brings the panel
// back when it is closed, and a reply in it no longer does: the person went
// back to it on purpose.
export function backToThread(): void {
    if (!deps || !undo || !canGoBackToThread()) {
        return;
    }

    const {root, channelId} = undo;
    undo = null;
    clearHint();
    hint = {root, until: Date.now() + HINT_MS};
    hintTimer = window.setTimeout(clearHint, HINT_MS);
    publish();

    deps.store.dispatch({type: 'SELECT_POST', postId: root, channelId, timestamp: Date.now()});
}

// The strip has gone: timed out, or the person moved on.
export function dismissUndo(): void {
    if (undo !== null) {
        undo = null;
        publish();
    }
}

// Hide on the small player: gone for the rest of this detour, and its stream
// let go after the pool's minute unless the panel comes back first. Coming back
// by itself is not affected.
export function hideMini(): void {
    if (state.phase !== 'away' || state.hidden) {
        return;
    }

    state = {...state, hidden: true};
    release();
    publish();
}

// The power switch on the small player, which stands for the whole watch
// list: every stream off, and the list emptied. The detour carries on, and
// the panel still comes back (it is the panel coming back, not the stream).
export function switchOffAll(): void {
    stopAllSessions('power');
    clearWatching();
}

// The small player says when it is and is not on screen. Going out of sight
// while still away (under the Threads page, or out of room) starts a fresh
// hold, so each spell out of sight is bounded on its own. True the first time
// it appears in a detour, which is when it says so out loud.
export function reportMiniVisible(visible: boolean): boolean {
    if (visible === miniShown) {
        return false;
    }

    miniShown = visible;
    if (visible) {
        const firstTime = !miniShownThisDetour;
        miniShownThisDetour = true;

        return firstTime;
    }

    if (state.phase === 'away' && !state.hidden) {
        release();
        hold();
    }

    return false;
}

export function getWaitingShown(): string | null {
    return waitingShown;
}

export function markWaitingShown(handle: string | null): void {
    waitingShown = state.phase === 'away' ? handle : null;
}

// ---- Said out loud ----

// Through the small player's live region, which is mounted wherever there is
// a small player, whether or not it is showing; nothing is said where there
// is none.
let announcement: {text: string; n: number} = {text: '', n: 0};
const announcers = new Set<() => void>();

// Whether announce() has anywhere to say anything: false in a channel popped
// out on its own, whose strip then says it itself (ReturnStrip.tsx).
export function canAnnounce(): boolean {
    return Boolean(deps?.miniHost);
}

// The strip's words after a reply, which are also what is said, with the
// server's name for the plugin (name.ts).
export const backToThreadWords = (name: string): string => `${name} is back. Back to thread is at the top of the panel.`;

export function replyPostedWords(channelName: string): string {
    return channelName ? `Reply posted in ${channelName}.` : 'Reply posted.';
}

export function announce(words: string): void {
    if (!deps?.miniHost) {
        return;
    }

    announcement = {text: words, n: announcement.n + 1};
    Array.from(announcers).forEach((listener) => listener());
}

export function useAnnouncement(): {text: string; n: number} {
    const [, forceRender] = React.useState(0);

    React.useEffect(() => {
        const listener = () => forceRender((value) => value + 1);
        announcers.add(listener);

        return () => {
            announcers.delete(listener);
        };
    }, []);

    return announcement;
}
