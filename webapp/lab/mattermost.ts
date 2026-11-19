import React from 'react';
import type {Store} from 'redux';

import {setMattermostStore, setPanelCloser} from '../src/mattermost';
import {answerPopout} from '../src/tagSearch';
import type {MattermostChannel, MattermostUser} from '../src/types';

import {PLUGIN_ID} from './mocks/manifest';
import {LAB_CHANNELS, LAB_TEAM, LAB_USERS, LOADED_CHANNELS, LOADED_USERS, ME_ID, freshId, getWorld} from './world';

// Mattermost's side of the hashtag search, in the lab: its store, its
// browser history, its search bar, and the messaging between a popped-out
// panel and its window. Nothing here reaches anything; each stands in for a
// part of Mattermost the plugin talks to, and says what it was asked to do,
// so the hand-offs can be seen working rather than taken on trust.

type Action = {type: string; [key: string]: unknown};

// The id the plugin's side panel is registered under in the lab. On a server it
// is whatever registerRightHandSidebarComponent hands back.
export const LAB_PANEL = 'lab-panel';

// Another plugin's side panel, for the lab to put in the panel's place.
export const LAB_OTHER_PLUGIN = 'lab-other-plugin';

// The team a team switch goes to, and back from.
export const LAB_OTHER_TEAM = {id: 't-elsewhere', name: 'elsewhere', display_name: 'Elsewhere'};

// A post as Mattermost's store holds one, with the two fields a reply being
// sent goes through: the placeholder's pending_post_id, and failed.
export type LabPost = {
    id: string;
    channel_id: string;
    user_id: string;
    root_id: string;
    message: string;
    create_at: number;
    delete_at: number;
    type: string;
    pending_post_id?: string;
    failed?: boolean;

    // What a plugin's post carries for its own component: a shared clip's
    // card reads it. Absent on ordinary messages.
    props?: Record<string, unknown>;
};

// The messages in the lab's message list: six in each of two channels, each
// with a reply, so there is always a thread to open.
const SAMPLE_LINES: Array<[string, string, string]> = [
    ['u-dave', 'Anyone up for Arma tonight?', 'Count me in.'],
    ['u-bob', 'Server restarts at nine, save your loadouts.', 'Noted, thanks.'],
    [ME_ID, 'Streaming the briefing at half past.', 'I will be watching.'],
    ['u-dave', 'New map rotation is in the wiki.', 'The desert one again?'],
    ['u-bob', 'Who has the spare headset?', 'In the drawer by the door.'],
    ['u-dave', 'Clip of that landing, absolutely cursed.', 'How did the heli survive that'],
];

function samplePosts(): {posts: Record<string, LabPost>; postsInThread: Record<string, string[]>} {
    const posts: Record<string, LabPost> = {};
    const postsInThread: Record<string, string[]> = {};
    const at = Date.now();
    ['c-town', 'c-arma'].forEach((channel, room) => {
        SAMPLE_LINES.forEach(([who, line, answer], index) => {
            const id = `lab-root-${room + 1}-${index + 1}`;
            const reply = `${id}-r`;
            const minutes = (SAMPLE_LINES.length - index) * 7;
            posts[id] = {id, channel_id: channel, user_id: who, root_id: '', message: room === 0 ? line : `${line} (Arma night)`,
                create_at: at - (minutes * 60000), delete_at: 0, type: ''};
            posts[reply] = {id: reply, channel_id: channel, user_id: who === 'u-bob' ? 'u-dave' : 'u-bob', root_id: id, message: answer,
                create_at: at - ((minutes - 2) * 60000), delete_at: 0, type: ''};
            postsInThread[id] = [reply];
        });
    });

    return {posts, postsInThread};
}

// The roots of the messages in a channel, oldest first.
export function rootsIn(channelId: string): LabPost[] {
    return Object.values(state.entities.posts.posts).
        filter((post) => post.channel_id === channelId && post.root_id === '' && post.delete_at === 0).
        sort((a, b) => a.create_at - b.create_at);
}

type LabRhs = {
    rhsState: string | null;
    pluggableId: string;
    selectedPostId: string;
    selectedPostCardId: string;
    isSidebarOpen: boolean;
    isSidebarExpanded: boolean;

    // Where a thread opened from search results goes back to: the header's
    // Back arrow, which Mattermost shows for search, mentions, saved and
    // pinned only (rhs_header_post.tsx).
    previousRhsState: string | null;
    searchTerms: string;
    searchType: string;
    searchTeam: string | null;
};

type LabState = {
    entities: {
        general: {config: Record<string, string>; license: Record<string, string>};
        users: {currentUserId: string; profiles: Record<string, MattermostUser>};
        teams: {currentTeamId: string; teams: Record<string, typeof LAB_TEAM>};
        channels: {currentChannelId: string; channels: Record<string, MattermostChannel>};
        preferences: {myPreferences: Record<string, {value: string}>};
        posts: {posts: Record<string, LabPost>; postsInThread: Record<string, string[]>};
    };
    views: {
        rhs: LabRhs;
        rhsSuppressed: boolean;
        browser: {windowSize: string};
    };
};

const byId = <T extends {id: string}>(items: T[], ids: string[]): Record<string, T> => {
    const out: Record<string, T> = {};
    items.filter((item) => ids.includes(item.id)).forEach((item) => {
        out[item.id] = item;
    });

    return out;
};

let state: LabState = {
    entities: {
        general: {
            // The defaults a fresh server sends, except names as full names
            // and Collapsed Reply Threads on, so the lab shows both.
            config: {
                MinimumHashtagLength: '3',
                TeammateNameDisplay: 'full_name',
                LockTeammateNameDisplay: 'false',
                EnablePostUsernameOverride: 'true',
                CollapsedThreads: 'default_on',
            },
            license: {},
        },
        users: {currentUserId: ME_ID, profiles: byId(LAB_USERS, LOADED_USERS)},
        teams: {currentTeamId: LAB_TEAM.id, teams: {[LAB_TEAM.id]: LAB_TEAM, [LAB_OTHER_TEAM.id]: LAB_OTHER_TEAM}},
        channels: {currentChannelId: LAB_CHANNELS[0]?.id || '', channels: byId(LAB_CHANNELS, LOADED_CHANNELS)},
        preferences: {myPreferences: {}},
        posts: samplePosts(),
    },
    views: {
        // The panel open, as it is when somebody is watching.
        rhs: {
            rhsState: 'plugin',
            pluggableId: LAB_PANEL,
            selectedPostId: '',
            selectedPostCardId: '',
            isSidebarOpen: true,
            isSidebarExpanded: false,
            previousRhsState: null,
            searchTerms: '',
            searchType: '',
            searchTeam: null,
        },
        rhsSuppressed: false,
        browser: {windowSize: 'desktopView'},
    },
};

// The lab's own views of the store, which stand for Mattermost's components,
// are told about a change before anything that subscribed through the store —
// as Mattermost's Provider subscribes before any plugin initializes, so on a
// legacy root its components have already re-rendered by the time a plugin's
// listener runs. The lab mounts a legacy root too, as Mattermost 11.11 does by
// default, so the same holds here; under ?root=concurrent it may not, as on a
// server with EnableConcurrentReact.
const viewListeners = new Set<() => void>();
const listeners = new Set<() => void>();

function announce(): void {
    Array.from(viewListeners).forEach((listener) => listener());
    Array.from(listeners).forEach((listener) => listener());
}

// A post arriving: the placeholder of a reply being sent, the server's post
// that replaces it, or somebody else's. The real post takes the placeholder's
// place in its thread, and the placeholder is deleted (redux
// reducers/entities/posts.ts). A thread's list is always a new array, as it is
// there whenever the thread changes.
function receivePost(current: LabState, post: LabPost): LabState {
    const posts = {...current.entities.posts.posts, [post.id]: post};
    const pending = post.pending_post_id || '';
    const replaces = pending !== '' && pending !== post.id;
    if (replaces) {
        delete posts[pending];
    }

    let postsInThread = current.entities.posts.postsInThread;
    if (post.root_id) {
        const ids = postsInThread[post.root_id] || [];
        let next: string[];
        if (replaces && ids.includes(pending)) {
            next = ids.map((id) => (id === pending ? post.id : id));
        } else if (ids.includes(post.id)) {
            next = [...ids];
        } else {
            next = [...ids, post.id];
        }
        postsInThread = {...postsInThread, [post.root_id]: next};
    }

    return {...current, entities: {...current.entities, posts: {posts, postsInThread}}};
}

function removePost(current: LabState, id: string): LabState {
    const posts = {...current.entities.posts.posts};
    const root = posts[id]?.root_id || '';
    delete posts[id];
    const postsInThread = {...current.entities.posts.postsInThread};
    delete postsInThread[id];
    if (root && postsInThread[root]) {
        postsInThread[root] = postsInThread[root].filter((candidate) => candidate !== id);
    }

    // Deleting the root of the open thread leaves the panel open and empty
    // (reducers/views/rhs.ts, selectedPostId on POST_REMOVED).
    const rhs = current.views.rhs.selectedPostId === id ? {...current.views.rhs, selectedPostId: ''} : current.views.rhs;

    return {
        ...current,
        entities: {...current.entities, posts: {posts, postsInThread}},
        views: {...current.views, rhs},
    };
}

// The few reducers of Mattermost's that the plugin's actions reach, with
// what they do in 9.0 to master (reducers/views/rhs.ts,
// reducers/views/rhs_suppressed.ts, reducers/views/browser.ts, and the
// entities' teams, channels, posts and preferences).
// BROWSER_WINDOW_RESIZED is the lab's "phone-sized" chip, as Mattermost sends
// it when its window crosses into the phone layout.
function reduce(current: LabState, action: Action): LabState {
    const rhs = current.views.rhs;
    const withRhs = (next: LabRhs, suppressed = current.views.rhsSuppressed): LabState => ({
        ...current,
        views: {...current.views, rhs: next, rhsSuppressed: suppressed},
    });
    switch (action.type) {
    case 'UPDATE_RHS_SEARCH_TERMS':
        return withRhs({...rhs, searchTerms: String(action.terms ?? '')});
    case 'UPDATE_RHS_SEARCH_TYPE':
        return withRhs({...rhs, searchType: String(action.searchType ?? '')});
    case 'UPDATE_RHS_SEARCH_TEAM':
        return withRhs({...rhs, searchTeam: action.teamId === null ? null : String(action.teamId)});
    case 'UPDATE_RHS_STATE': {
        const next = typeof action.state === 'string' && action.state !== '' ? action.state : null;
        const search = next === 'search';

        return withRhs({
            ...rhs,
            rhsState: next,
            pluggableId: next === 'plugin' ? String(action.pluggableId ?? '') : '',
            selectedPostId: typeof action.postId === 'string' ? action.postId : '',
            selectedPostCardId: '',
            isSidebarOpen: next !== null,
            isSidebarExpanded: next ? rhs.isSidebarExpanded : false,
            previousRhsState: null,
            searchTerms: search ? rhs.searchTerms : '',
            searchType: search ? rhs.searchType : '',
            searchTeam: search ? rhs.searchTeam : null,
        }, next === null ? current.views.rhsSuppressed : false);
    }
    case 'SELECT_POST': {
        const postId = typeof action.postId === 'string' ? action.postId : '';

        return withRhs({
            ...rhs,
            rhsState: null,
            pluggableId: '',
            selectedPostId: postId,
            selectedPostCardId: '',
            isSidebarOpen: postId !== '',
            isSidebarExpanded: postId ? rhs.isSidebarExpanded : false,
            previousRhsState: typeof action.previousRhsState === 'string' ? action.previousRhsState : null,
        }, postId === '' ? current.views.rhsSuppressed : false);
    }
    case 'RHS_GO_BACK':
        return withRhs({...rhs, rhsState: typeof action.state === 'string' ? action.state : null, selectedPostId: '', previousRhsState: null});
    case 'RECEIVED_NEW_POST':
    case 'RECEIVED_POST':
        return receivePost(current, action.data as LabPost);
    case 'POST_REMOVED':
        return removePost(current, String((action.data as {id?: string})?.id || ''));
    case 'SELECT_CHANNEL':
        return {...current, entities: {...current.entities, channels: {...current.entities.channels, currentChannelId: String(action.data)}}};

    // A channel arriving in the store (reducers/entities/channels.ts), for
    // the Share row's group message and shared channel, which the lab's
    // sidebar does not have loaded.
    case 'RECEIVED_CHANNEL': {
        const channel = action.data as MattermostChannel;

        return {...current, entities: {...current.entities, channels: {...current.entities.channels,
            channels: {...current.entities.channels.channels, [channel.id]: channel}}}};
    }
    case 'SELECT_TEAM':
        return {...current, entities: {...current.entities, teams: {...current.entities.teams, currentTeamId: String(action.data)}}};
    case 'RECEIVED_PREFERENCES': {
        const mine = {...current.entities.preferences.myPreferences};
        (action.data as Array<{category: string; name: string; value: string}>).forEach((preference) => {
            mine[`${preference.category}--${preference.name}`] = {value: preference.value};
        });

        return {...current, entities: {...current.entities, preferences: {myPreferences: mine}}};
    }
    case 'SET_RHS_EXPANDED':
        return withRhs({...rhs, isSidebarExpanded: action.expanded === true});
    case 'SUPPRESS_RHS':
        return withRhs({...rhs, isSidebarExpanded: false}, true);
    case 'UNSUPPRESS_RHS':
        return withRhs(rhs, false);
    case 'BROWSER_WINDOW_RESIZED':
        return {...current, views: {...current.views, browser: {windowSize: String(action.data ?? 'desktopView')}}};

    // Who is signed in, as mattermost-redux's users reducer keeps it
    // (reducers/entities/users.ts currentUserId): the lab's "signed in after
    // load", where the page loaded on the sign-in page with nobody, and then
    // somebody signed in without the page loading again.
    case 'RECEIVED_ME':
        return {...current, entities: {...current.entities, users: {...current.entities.users, currentUserId: String((action.data as {id?: string})?.id || '')}}};
    case 'LOGOUT_SUCCESS':
        return {...current, entities: {...current.entities, users: {...current.entities.users, currentUserId: ''}}};
    default:
        return current;
    }
}

const store = {
    getState: () => state,
    dispatch: (action: Action) => {
        state = reduce(state, action);
        announce();

        return action;
    },
    subscribe: (listener: () => void) => {
        listeners.add(listener);

        return () => {
            listeners.delete(listener);
        };
    },
};

// Mattermost's store, for the plugin's own code that subscribes to it.
export const labStore = store as unknown as Store;

export function labDispatch(action: Action): void {
    store.dispatch(action);
}

// Several actions, and one announcement, as Mattermost's batchActions does:
// closeRightHandSide closes the panel and clears the thread in one go.
export function labBatch(actions: Action[]): void {
    state = actions.reduce(reduce, state);
    announce();
}

// The registration's showRHSPlugin, and the App Bar icon's toggleRHSPlugin,
// which hides the panel only when it is the one showing (actions/views/rhs.ts).
export function labShowPanel(): void {
    labDispatch({type: 'UPDATE_RHS_STATE', state: 'plugin', pluggableId: LAB_PANEL});
}

export function labCloseSidePanel(): void {
    labBatch([{type: 'UPDATE_RHS_STATE', state: null}, {type: 'SELECT_POST', postId: ''}]);
}

export function labTogglePanel(): void {
    const rhs = state.views.rhs;
    if (rhs.isSidebarOpen && rhs.rhsState === 'plugin' && rhs.pluggableId === LAB_PANEL) {
        labCloseSidePanel();

        return;
    }

    labShowPanel();
}

// ---- Replying, as Mattermost's createPost does it ----

// A send puts a placeholder in the thread at once, whose id is its
// pending_post_id; 400 ms later the server's post replaces it, or, with
// "Reply fails" on, the placeholder comes back marked failed and stays, with
// a Retry that sends it again under the same pending id.
let replyFails = false;

export function setReplyFails(fails: boolean): void {
    replyFails = fails;
}

export function getReplyFails(): boolean {
    return replyFails;
}

function settle(placeholder: LabPost): void {
    window.setTimeout(() => {
        if (replyFails) {
            labDispatch({type: 'RECEIVED_NEW_POST', data: {...placeholder, failed: true}});

            return;
        }

        const {failed: _failed, ...sent} = placeholder;
        labDispatch({type: 'RECEIVED_POST', data: {...sent, id: freshId('post')}});
    }, 400);
}

export function labSendReply(root: string, message: string): void {
    const at = Date.now();
    const pending = `${ME_ID}:${at}`;
    const placeholder: LabPost = {
        id: pending,
        pending_post_id: pending,
        channel_id: state.entities.posts.posts[root]?.channel_id || state.entities.channels.currentChannelId,
        user_id: ME_ID,
        root_id: root,
        message,
        create_at: at,
        delete_at: 0,
        type: '',
    };
    labDispatch({type: 'RECEIVED_NEW_POST', data: placeholder});
    settle(placeholder);
}

export function labRetryReply(pending: string): void {
    const placeholder = state.entities.posts.posts[pending];
    if (!placeholder) {
        return;
    }

    const {failed: _failed, ...again} = placeholder;
    labDispatch({type: 'RECEIVED_NEW_POST', data: again});
    settle(again);
}

// The same person replying from their phone: the server's post arrives here
// over the WebSocket with a pending id this tab never saw as a placeholder.
export function labReplyFromElsewhere(root: string): void {
    const at = Date.now();
    labDispatch({type: 'RECEIVED_POST', data: {
        id: freshId('post'),
        pending_post_id: `${ME_ID}:${at - 1}`,
        channel_id: state.entities.posts.posts[root]?.channel_id || state.entities.channels.currentChannelId,
        user_id: ME_ID,
        root_id: root,
        message: 'Sent from my phone.',
        create_at: at,
        delete_at: 0,
        type: '',
    }});
}

// Settings › Plugin preferences, as Mattermost stores what is chosen there.
export function labSetPreference(name: string, value: string): void {
    labDispatch({type: 'RECEIVED_PREFERENCES', data: [{category: `pp_${PLUGIN_ID}`.slice(0, 32), name, value}]});
}

export function useLabState(): LabState {
    const [, bump] = React.useState(0);
    React.useEffect(() => {
        const listener = () => bump((value) => value + 1);
        viewListeners.add(listener);

        return () => {
            viewListeners.delete(listener);
        };
    }, []);

    return state;
}

// What Mattermost was last asked to do, in words, for the lab's column.
let said = '';
const sayListeners = new Set<() => void>();

export function labSay(words: string): void {
    said = words;
    sayListeners.forEach((listener) => listener());
}

export function useLabSaid(): string {
    const [, bump] = React.useState(0);
    React.useEffect(() => {
        const listener = () => bump((value) => value + 1);
        sayListeners.add(listener);

        return () => {
            sayListeners.delete(listener);
        };
    }, []);

    return said;
}

// Whether the plugin closed its panel just before going to a message, as it
// does in the phone layout. The lab's panel stays where it is, so it can be
// tried again; what Mattermost would have done is said instead.
let panelClosed = false;

// Every address a clip link's page sent Mattermost to, oldest first, for the
// lab's "Open a clip link" to show and to act on.
let replaced: string[] = [];
const replaceListeners = new Set<() => void>();

export function clearLabReplaces(): void {
    replaced = [];
    replaceListeners.forEach((listener) => listener());
}

export function useLabReplaces(): string[] {
    const [, bump] = React.useState(0);
    React.useEffect(() => {
        const listener = () => bump((value) => value + 1);
        replaceListeners.add(listener);

        // A clip link's page sends Mattermost on from its own effect, which
        // runs before this one: caught up with here.
        listener();

        return () => {
            replaceListeners.delete(listener);
        };
    }, []);

    return replaced;
}

const history = {
    // Where a clip link's page sends Mattermost once it has done its work, or
    // a signed-out reader to sign in. Said and kept, never done: the lab is
    // one page.
    replace: (path: string) => {
        replaced = [...replaced, path];
        replaceListeners.forEach((listener) => listener());
        labSay(`Mattermost would replace the address with ${path}, leaving no step in the history.`);
    },
    push: (path: string) => {
        const closed = panelClosed;
        panelClosed = false;
        labSay(closed ?
            `Mattermost would close the panel, which covers a phone-sized screen, and go to ${path}: the message in its channel.` :
            `Mattermost would go to ${path}: the message in its channel, with the panel left open.`);
    },
};

// A popped-out panel and the window it came from, both in this one page: the
// panel's messages go to the plugin's own main-window half, answerPopout, as
// Mattermost would deliver them, and its answers come back the same way.
const fromPopout = new Set<(channel: string, ...args: unknown[]) => void>();
const toPopout = new Set<(channel: string, ...args: unknown[]) => void>();

const popouts = {
    sendToParent: (channel: string, ...args: unknown[]) => {
        // A window that has gone, or reloaded, simply never answers.
        if (getWorld().mainWindowGone) {
            return;
        }

        window.setTimeout(() => fromPopout.forEach((listener) => listener(channel, ...args)), 0);
    },
    onMessageFromParent: (listener: (channel: string, ...args: unknown[]) => void) => {
        toPopout.add(listener);
    },
};

export function connectLabMattermost(): void {
    setMattermostStore(labStore);
    setPanelCloser(() => {
        panelClosed = true;
    });

    const win = window as unknown as {WebappUtils?: unknown; opener: unknown};
    win.WebappUtils = {browserHistory: history, popouts};

    // The window a popped-out panel was opened from, which is where its
    // results are opened. The lab's popout is in the same page, so it is this
    // one, standing in.
    win.opener = {closed: false, WebappUtils: {browserHistory: history}, focus: () => undefined};

    answerPopout({
        sendToPopout: (channel, ...args) => {
            window.setTimeout(() => toPopout.forEach((listener) => listener(channel, ...args)), 0);
        },
        onMessageFromPopout: (listener) => {
            fromPopout.add(listener);
        },
    });
}
