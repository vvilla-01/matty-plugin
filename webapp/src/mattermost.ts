import type {Store} from 'redux';

import {shareLabel} from './clipLogic';
import type {ShareLabel} from './clipLogic';
import {PLUGIN_ID} from './manifest';
import {isPopoutWindow} from './popout';
import type {MattermostChannel, MattermostUser} from './types';

// Mattermost's own Redux store, and the few things this plugin reads from it.
//
// initialize() hands the store over and uninitialize() takes it back. Without
// one — the lab, or the moment after the plugin is switched off — every reader
// here answers with Mattermost's own defaults rather than throwing, so nothing
// that uses them has to check.
//
// Everything read here is read the way Mattermost's own selectors read it,
// with the file each rule comes from beside it, because the point of reading
// it at all is to agree with what Mattermost shows next to the panel.

let store: Store | null = null;

export function setMattermostStore(next: Store | null): void {
    store = next;
}

export function mattermostStore(): Store | null {
    return store;
}

type Team = {id?: string; name?: string};

// Only the parts of Mattermost's state this plugin looks at, and every one of
// them optional: it is somebody else's state, and its shape is theirs to change.
type State = {
    entities?: {
        general?: {
            config?: Record<string, string | undefined>;
            license?: Record<string, string | undefined>;
        };
        users?: {
            currentUserId?: string;
            profiles?: Record<string, MattermostUser | undefined>;
        };
        teams?: {
            currentTeamId?: string;
            teams?: Record<string, Team | undefined>;
        };
        channels?: {
            currentChannelId?: string;
            channels?: Record<string, MattermostChannel | undefined>;
        };
        preferences?: {myPreferences?: Record<string, {value?: string} | undefined>};
    };
    views?: {browser?: {windowSize?: string}};
};

function state(): State {
    try {
        return (store?.getState() as State | undefined) || {};
    } catch {
        return {};
    }
}

function config(): Record<string, string | undefined> {
    return state().entities?.general?.config || {};
}

function preference(category: string, name: string): {value?: string} | undefined {
    return state().entities?.preferences?.myPreferences?.[`${category}--${name}`];
}

// One of the plugin's own settings under Settings › Plugin preferences, as
// Mattermost stores it: a preference whose category is "pp_" and the plugin
// id, cut to 32 characters (utils/plugins/preferences.tsx, 9.11 to master).
// The fallback when it has never been set.
export function pluginPreference(name: string, fallback: string): string {
    const value = preference(`pp_${PLUGIN_ID}`.slice(0, 32), name)?.value;

    return typeof value === 'string' && value !== '' ? value : fallback;
}

// The shortest hashtag Mattermost turns into a link: ServiceSettings'
// MinimumHashtagLength, sent to signed-in clients as a string, and 3 when it
// is not there (model/config.go; read in components/markdown/index.ts).
export function hashtagMinimum(): number {
    const parsed = parseInt(config().MinimumHashtagLength || '', 10);

    return Number.isFinite(parsed) && parsed >= 1 ? parsed : 3;
}

// Mattermost's phone layout, the way it decides it itself
// (selectors/views/browser.ts, getIsMobileView): by the window's size class,
// which it keeps in the store.
export function isMobileView(): boolean {
    return state().views?.browser?.windowSize === 'mobileView';
}

export function currentUserId(): string {
    return state().entities?.users?.currentUserId || '';
}

export function currentTeam(): {id: string; name: string} {
    const teams = state().entities?.teams;
    const id = teams?.currentTeamId || '';

    return {id, name: teams?.teams?.[id]?.name || ''};
}

export function teamNameOf(teamId: string): string {
    return state().entities?.teams?.teams?.[teamId]?.name || '';
}

export function profileOf(userId: string): MattermostUser | undefined {
    return state().entities?.users?.profiles?.[userId];
}

export function channelOf(channelId: string): MattermostChannel | undefined {
    return state().entities?.channels?.channels?.[channelId];
}

// The channel the reader has open, as Mattermost's own getCurrentChannelId
// reads it (mattermost-redux selectors/entities/common.ts). '' where there is
// none: the global Threads view clears it (actions/views/lhs.ts). In a
// popped-out thread window it is the channel the window was opened from, and
// does not follow the main window.
export function currentChannelId(): string {
    return state().entities?.channels?.currentChannelId || '';
}

// What the Share button says about the channel a clip would go to: its name,
// the other person in a direct message, or why it cannot be shared there. The
// rules are clipLogic.ts's shareLabel, where they can be checked without a
// store; this only reads the store for them.
export function shareTargetOf(channelId: string): ShareLabel {
    if (channelId === '') {
        return shareLabel({kind: ''});
    }

    const channel = channelOf(channelId);
    if (!channel) {
        return shareLabel({kind: 'unknown'});
    }

    const shared = channel.shared === true;
    if (channel.type === 'D') {
        // A direct message is named after its two people's ids, "idA__idB";
        // one with yourself has your id twice.
        const me = currentUserId();
        const ids = (channel.name || '').split('__');
        const other = ids.find((id) => id !== '' && id !== me) || (ids.includes(me) ? me : '');
        const profile = other === '' ? undefined : profileOf(other);
        let otherName = profile ? displayNameOf(profile) : '';
        if (otherName !== '' && other === me) {
            // As Mattermost writes your own direct message in the sidebar.
            otherName = `${otherName} (you)`;
        }

        return shareLabel({kind: 'D', otherName, shared});
    }

    return shareLabel({kind: channel.type || 'unknown', displayName: channel.display_name || '', shared});
}

// Whether a webhook's own name for itself is shown on its posts
// (components/post/user_profile.tsx).
export function webhookNamesShown(): boolean {
    return config().EnablePostUsernameOverride === 'true';
}

// Teammate Name Display: the reader's own choice, unless an admin has locked
// it with a license that allows locking, and then the server's
// (selectors/entities/preferences.ts, getTeammateNameDisplaySetting).
function nameDisplay(): string {
    const general = state().entities?.general;
    const locked = general?.license?.LockTeammateNameDisplay === 'true' && general?.config?.LockTeammateNameDisplay === 'true';
    const chosen = preference('display_settings', 'name_format');
    if (chosen && !locked) {
        return chosen.value || '';
    }

    return config().TeammateNameDisplay || 'username';
}

// A person's name exactly as Mattermost writes it beside their posts
// (mattermost-redux/utils/user_utils.ts, displayUsername), so a result here
// names somebody the way the channel beside it does.
export function displayNameOf(user: MattermostUser | undefined): string {
    if (!user) {
        return 'Someone';
    }

    const full = [user.first_name, user.last_name].filter(Boolean).join(' ');
    const setting = nameDisplay();
    let name = '';
    if (setting === 'nickname_full_name') {
        name = user.nickname || full;
    } else if (setting === 'full_name') {
        name = full;
    } else {
        name = user.remote_id && user.props?.RemoteUsername ? user.props.RemoteUsername : (user.username || '');
    }

    return name.trim() === '' ? (user.username || 'Someone') : name;
}

// Collapsed Reply Threads, as isCollapsedThreadsEnabled decides it: allowed
// by the server, and then either always on or switched on by the reader,
// whose default follows the server's.
export function collapsedThreads(): boolean {
    const setting = config().CollapsedThreads;
    if (setting === undefined || setting === 'disabled') {
        return false;
    }

    const fallback = setting === 'default_on' || setting === 'always_on' ? 'on' : 'off';
    const chosen = preference('display_settings', 'collapsed_reply_threads');

    return setting === 'always_on' || (chosen ? chosen.value : fallback) === 'on';
}

// ---- Leaving the panel for somewhere in Mattermost ----

type History = {
    push: (path: string) => void;

    // Optional, and looked for before it is used, like everything else read
    // off somebody else's object: the push above is all this plugin needed
    // until clip links.
    replace?: (path: string) => void;
};

type Popouts = {
    sendToParent?: (channel: string, ...args: unknown[]) => void;
    onMessageFromParent?: (listener: (channel: string, ...args: unknown[]) => void) => void;
};

// Mattermost's emoji search, as plugins are given it (design/3.2.0.md M7):
// handlePretextChanged(':' and at least two letters, callback), whose
// callback gets groups of {name, emoji} items, system and custom alike, the
// reader's recent ones first and their skin tone applied. It never cancels a
// stale call; the caller drops one. Plugins cannot open Mattermost's own emoji
// picker; this is the next best thing.
export type EmoticonItem = {name: string; emoji: {unified?: string; id?: string; name?: string}};
export type EmoticonResults = {groups?: Array<{items?: EmoticonItem[]}>};
export type EmoticonProvider = new () => {
    handlePretextChanged: (pretext: string, callback: (results: EmoticonResults) => void) => boolean;
};

type WebappUtils = {browserHistory?: History; popouts?: Popouts; editor?: {providers?: {Emoticon?: EmoticonProvider}}};

type MattermostWindow = Window & {WebappUtils?: WebappUtils; basename?: unknown};

// Read every time rather than kept: the history behind it is Mattermost's to
// replace.
function webappUtils(win: Window = window): WebappUtils | undefined {
    return (win as MattermostWindow).WebappUtils;
}

// Messaging between a popped-out panel and the window it came from, which
// Mattermost gives plugins on WebappUtils.popouts. Undefined where the page
// has no WebappUtils, as in the lab.
export function popoutMessaging(): Popouts | undefined {
    return webappUtils()?.popouts;
}

// The emoji search, feature-detected: it is public but untyped, and a
// Mattermost without it leaves the soundboard's chooser its suggested list.
export function emoticonProvider(): EmoticonProvider | undefined {
    const Provider = webappUtils()?.editor?.providers?.Emoticon;

    return typeof Provider === 'function' ? Provider : undefined;
}

// The reader's recent emoji, most used first, as Mattermost keeps them: the
// preference recent_emojis named for the user, a JSON list of {name,
// usageCount} (M8). Nothing when it cannot be read.
export function recentEmojiNames(): string[] {
    const userId = currentUserId();
    const value = userId === '' ? undefined : preference('recent_emojis', userId)?.value;
    try {
        const list: unknown = JSON.parse(value || '[]');
        if (!Array.isArray(list)) {
            return [];
        }

        return list.
            map((entry) => (typeof entry === 'string' ? {name: entry, usageCount: 0} : entry as {name?: unknown; usageCount?: unknown})).
            filter((entry) => typeof entry.name === 'string').
            sort((a, b) => Number(b.usageCount || 0) - Number(a.usageCount || 0)).
            map((entry) => String(entry.name));
    } catch {
        return [];
    }
}

// Mattermost's own test for its desktop app (utils/user_agent.ts).
export function isDesktopApp(): boolean {
    const agent = navigator.userAgent;

    return agent.includes('Mattermost') && agent.includes('Electron');
}

// A permalink as an address, for a new tab: Mattermost served under a path
// of its own keeps that path in window.basename, and its history adds it to
// everything pushed (root.tsx, utils/browser_history.tsx).
export function permalinkHref(path: string): string {
    const base = (window as MattermostWindow).basename;

    return `${typeof base === 'string' ? base : ''}${path}`;
}

// Moves Mattermost to another of its own addresses without leaving a step in
// the browser's history: a clip link's route, once it has done its work, or
// the sign-in page for a reader who is signed out. Through Mattermost's own
// history when there is one, so the app moves without a reload; otherwise as
// a plain replace, under the path Mattermost is served from.
export function replacePath(path: string): void {
    const history = webappUtils()?.browserHistory;
    if (history && typeof history.replace === 'function') {
        history.replace(path);

        return;
    }

    window.location.replace(permalinkHref(path));
}

// Closes the plugin's own side panel: the registry's hideRHSPlugin, handed over
// by initialize() and taken back by uninitialize().
let closePanel: (() => void) | null = null;

export function setPanelCloser(closer: (() => void) | null): void {
    closePanel = closer;
}

// What Mattermost's own Jump does to the side panel before it goes
// (components/post/post_component.tsx, handleJumpClick, 9.0 to master).
//
// In the phone-sized layout the panel covers the whole screen
// (sass/responsive/_mobile.scss), so it is closed, or the message would open
// out of sight behind it and the press would seem to do nothing. That stops
// the video, but there it was all that could be seen; and it is the window
// size Mattermost itself goes by (selectors/views/browser.ts, getIsMobileView).
//
// An expanded panel is put back to its usual width, or the message would open
// in the strip of channel left beside it: nothing else un-expands it when the
// channel changes (reducers/views/rhs.ts, isSidebarExpanded). The action is
// setRhsExpanded's, whose type is its own name in every version from 9.0.
function makeRoomForMessage(): void {
    const current = store;
    if (!current) {
        return;
    }

    if (isMobileView()) {
        closePanel?.();
    }

    try {
        current.dispatch({type: 'SET_RHS_EXPANDED', expanded: false});
    } catch {
        // Left as it was; the message still opens.
    }
}

// Going to a message the way Mattermost's own Jump does: its permalink pushed
// into its own history (components/post/post_component.tsx), which changes
// the channel and, on anything wider than a phone, leaves the side panel —
// and the video in it — open; see makeRoomForMessage.
//
// A popped-out panel is a window with nothing in it but the panel, so the
// message is opened in the window it came from. In a browser that is the
// opener, called directly rather than messaged, so it still works after that
// window has reloaded; in the desktop app the push itself is handed to the
// main window by the app. With neither, a new tab is the only place left.
export function jumpToPost(path: string, href: string): void {
    if (isPopoutWindow() && !isDesktopApp()) {
        try {
            const main = window.opener as MattermostWindow | null;
            const history = main && !main.closed ? webappUtils(main)?.browserHistory : undefined;
            if (main && history && typeof history.push === 'function') {
                history.push(path);
                main.focus();

                return;
            }
        } catch {
            // Gone, or no longer a Mattermost window.
        }

        window.open(href, '_blank', 'noopener');

        return;
    }

    const history = webappUtils()?.browserHistory;
    if (history && typeof history.push === 'function') {
        makeRoomForMessage();
        history.push(path);

        return;
    }

    window.open(href, '_blank', 'noopener');
}

// How many animation frames to wait for Mattermost's search box to take the
// words: about a second.
const SEARCH_FRAMES = 60;

function waitFor<T>(find: () => T | null, found: (thing: T) => void, gaveUp: () => void, frames = SEARCH_FRAMES): void {
    const thing = find();
    if (thing) {
        found(thing);

        return;
    }

    if (frames <= 0) {
        gaveUp();

        return;
    }

    window.requestAnimationFrame(() => waitFor(find, found, gaveUp, frames - 1));
}

// Mattermost's own search, opened on these words in THIS window — which
// replaces whatever is in the side panel, the panel included, as a click on a
// hashtag in a post does. False when there is no search bar to open: the
// phone-sized layout has none, and neither does a popped-out window.
//
// There is no plugin API for this. A hashtag in a post runs searchForTerm,
// which is the search box's words set and then a search run from them
// (actions/post_actions.ts), and neither half is given to plugins. So the
// words go in through the same action the search box itself sends — its type
// is its own name in every version from 9.0 — and then the search box is
// asked to search, the way a person would ask it. On 11.11 it is a button
// that opens a box whose input reads the words once, when it opens
// (components/new_search/new_search.tsx and search_box.tsx): it is pressed,
// and Enter pressed in the input, which it listens for.
//
// Should that change shape, the box is left open on the words, and one Enter
// finishes the job.
export function openSearchHere(term: string): boolean {
    const current = store;
    if (!current || !document.getElementById('searchFormContainer')) {
        return false;
    }

    try {
        // Messages rather than files, in this team: what a hashtag click in
        // a post searches. A search type or team left from an earlier search
        // would otherwise come with it.
        current.dispatch({type: 'UPDATE_RHS_SEARCH_TYPE', searchType: ''});
        const team = currentTeam().id;
        if (team !== '') {
            current.dispatch({type: 'UPDATE_RHS_SEARCH_TEAM', teamId: team});
        }
        current.dispatch({type: 'UPDATE_RHS_SEARCH_TERMS', terms: term});
    } catch {
        return false;
    }

    // After React has drawn the words into the search box.
    window.setTimeout(() => runSearchBox(term), 0);

    return true;
}

function runSearchBox(term: string): void {
    const container = document.getElementById('searchFormContainer');
    if (!container) {
        return;
    }

    container.click();
    waitFor(
        () => document.querySelector<HTMLInputElement>('#searchBox input[type="search"]'),
        (input) => {
            if (input.value === term) {
                input.dispatchEvent(new KeyboardEvent('keydown', {key: 'Enter', code: 'Enter', bubbles: true, cancelable: true}));
            } else {
                input.focus();
            }
        },
        () => undefined,
    );
}
