import {fetchChannel, fetchUsersByIds, searchPosts} from './client';
import {isHashtagTerm} from './linkify';
import {
    channelOf,
    collapsedThreads,
    currentTeam,
    currentUserId,
    displayNameOf,
    openSearchHere,
    permalinkHref,
    popoutMessaging,
    profileOf,
    teamNameOf,
    webhookNamesShown,
} from './mattermost';
import {isPopoutWindow} from './popout';
import type {MattermostChannel, MattermostPost, MattermostUser} from './types';

// Searching Mattermost's messages for a hashtag from a stream title, and
// handing that search on to Mattermost's own search.
//
// The search is Mattermost's own, asked the way its search box asks it, so it
// finds what the reader's own search would and nothing more: messages in the
// current team's channels they belong to, and in their direct and group
// messages, newest first. A hashtag search matches the whole hashtag, in any
// case: #Arma3 finds #arma3, not #arma or the word arma3.

// What Mattermost's own search asks for at a time.
export const TAG_PAGE_SIZE = 20;

// One message, as the results list shows it.
export type TagHit = {
    id: string;
    at: number;
    authorId: string;
    author: string;

    // Where it was posted, in words; empty if Mattermost would not say.
    where: string;

    // A reply in a thread.
    reply: boolean;

    // A reply that Collapsed Reply Threads keeps out of its channel, so a
    // click goes to the thread's first message instead; see searchTag.
    inThread: boolean;
    message: string;

    // What a click pushes into Mattermost's history, and the message's own
    // address for a new tab. Both empty when there is no team to put in them.
    path: string;
    href: string;
};

export type TagPage = {
    hits: TagHit[];

    // Whether asking for the next page could find anything. The server never
    // says: its has_next is not set for a search. Nor is a short page the
    // end: a server searching with Elasticsearch or Bleve cuts the page first
    // and then drops what the reader may not see, deleted messages among them
    // (store/searchlayer/post_layer.go, app/post.go SearchPostsForUser), so a
    // page of 19 can have more after it. Only an empty page is the end, as in
    // Mattermost's own search (mattermost-redux actions/search.ts:
    // isEnd is posts.order.length === 0). A database search sends everything
    // on the first page, so its second is always that empty one.
    more: boolean;
};

// People and channels Mattermost had not loaded, once fetched, for as long as
// the same person is signed in: the next page, or the next hashtag, is usually
// the same few. No longer than that, as what Mattermost answers depends on who
// asks: it sends a system admin everybody's first and last names, and blanks
// them for everybody else while Show Full Name is off (api4/user.go
// getUsersByIds, model.User.Sanitize). So signing out forgets them all
// (forgetTagSearch), and the next person's own answers take their place.
const users = new Map<string, MattermostUser>();
const channels = new Map<string, MattermostChannel>();

// Counts the times they were forgotten, so a search still on its way when
// its reader signs out cannot put what it was told back for the next person.
let forgotten = 0;

// Signing out, or the plugin stopping: nothing Mattermost told one person is
// shown to whoever signs in next on the same page, which Mattermost does not
// load again.
export function forgetTagSearch(): void {
    forgotten += 1;
    users.clear();
    channels.clear();
}

const userOf = (id: string): MattermostUser | undefined => profileOf(id) || users.get(id);
const channelFor = (id: string): MattermostChannel | undefined => channelOf(id) || channels.get(id);

// The other person in a direct message, from its name: "idA__idB"
// (utils/channel_utils.ts, getUserIdFromChannelName). Both are the reader in
// a direct message to themselves.
function otherPerson(channel: MattermostChannel, me: string): string {
    const ids = (channel.name || '').split('__');
    if (ids.length !== 2) {
        return '';
    }

    return ids[0] === me ? ids[1] : ids[0];
}

function whereOf(channel: MattermostChannel | undefined, me: string): string {
    if (!channel) {
        return '';
    }

    const archived = (channel.delete_at || 0) > 0 ? ' (archived)' : '';
    if (channel.type === 'D') {
        const other = otherPerson(channel, me);
        if (other === me) {
            return 'Direct message to yourself';
        }

        return other === '' ? 'Direct message' : `Direct message with ${displayNameOf(userOf(other))}`;
    }

    // A group message's display name is its members' usernames, which is
    // what Mattermost's own server writes there.
    if (channel.type === 'G') {
        return `Group message: ${channel.display_name || channel.name || ''}`;
    }

    return `${channel.display_name || channel.name || ''}${archived}`;
}

// Which team a permalink names: the channel's own, or for a direct or group
// message, which belong to none, the current one (components/post/index.tsx).
// Mattermost moves the address to the right team itself once it has the post.
function teamFor(channel: MattermostChannel | undefined): string {
    const current = currentTeam().name;
    if (!channel || !channel.team_id || channel.type === 'D' || channel.type === 'G') {
        return current;
    }

    return teamNameOf(channel.team_id) || current;
}

// A webhook may post under a name of its own, which Mattermost shows only when
// an admin allows it.
function authorOf(post: MattermostPost): string {
    const props = post.props || {};
    if (props.from_webhook === 'true' && webhookNamesShown() && typeof props.override_username === 'string' && props.override_username !== '') {
        return props.override_username;
    }

    return displayNameOf(userOf(post.user_id));
}

// Whatever Mattermost had not loaded: channels one at a time, as Mattermost
// has no request for several, and people all in one. A lookup that fails
// leaves that name out rather than the whole page.
async function fillIn(posts: MattermostPost[], signal?: AbortSignal): Promise<void> {
    const me = currentUserId();
    const asked = forgotten;

    const missingChannels = Array.from(new Set(posts.map((post) => post.channel_id))).filter((id) => id && !channelFor(id));
    await Promise.all(missingChannels.map((id) => fetchChannel(id, signal).then((channel) => {
        if (channel && channel.id === id && asked === forgotten) {
            channels.set(id, channel);
        }
    }).catch(() => undefined)));

    const wanted = new Set<string>();
    posts.forEach((post) => {
        wanted.add(post.user_id);
        const channel = channelFor(post.channel_id);
        if (channel?.type === 'D') {
            wanted.add(otherPerson(channel, me));
        }
    });

    const missingUsers = Array.from(wanted).filter((id) => id && !userOf(id));
    try {
        (await fetchUsersByIds(missingUsers, signal)).forEach((user) => {
            if (user && typeof user.id === 'string' && asked === forgotten) {
                users.set(user.id, user);
            }
        });
    } catch (reason) {
        if (signal?.aborted) {
            throw reason;
        }
    }
}

// One page of messages tagged with a hashtag, newest first.
//
// Mattermost's own notices — "joined the channel" and the like — are left
// out, as its search leaves them out; the server already does, and this is
// the same test its webapp uses, in case one ever gets through.
//
// A reply opens differently depending on Collapsed Reply Threads. Off, it is
// in its channel like any message, and the click goes straight to it. On, it
// is not in the channel at all, and Mattermost opens a permalink to it by
// putting its thread in the side panel — in place of the panel, and the video
// with it. So there the click goes to the thread's first message instead,
// which is in the channel, with the reply count under it; the new-tab address
// is still the reply's own.
export async function searchTag(tag: string, page: number, signal?: AbortSignal): Promise<TagPage> {
    const team = currentTeam();
    const found = await searchPosts({
        teamId: team.id,
        terms: `#${tag}`,
        page,
        perPage: TAG_PAGE_SIZE,

        // Archived channels too, as Mattermost's own search always asks
        // (actions/views/rhs.ts, performSearch, v11.11.1).
        archived: true,
    }, signal);

    const order = Array.isArray(found.order) ? found.order : [];
    const posts = order.
        map((id) => found.posts?.[id]).
        filter((post): post is MattermostPost => Boolean(post && typeof post.id === 'string' && !(post.type || '').startsWith('system_')));

    await fillIn(posts, signal);

    const me = currentUserId();
    const threads = collapsedThreads();
    const hits = posts.map((post): TagHit => {
        const channel = channelFor(post.channel_id);
        const teamName = teamFor(channel);
        const reply = Boolean(post.root_id);
        const inThread = reply && threads;
        const own = teamName ? `/${teamName}/pl/${post.id}` : '';

        return {
            id: post.id,
            at: Number.isFinite(post.create_at) ? post.create_at : 0,
            authorId: post.user_id,
            author: authorOf(post),
            where: whereOf(channel, me),
            reply,
            inThread,
            message: post.message || '',
            path: teamName && inThread ? `/${teamName}/pl/${post.root_id}` : own,
            href: own ? permalinkHref(own) : '',
        };
    });

    return {hits, more: order.length > 0};
}

// ---- Mattermost's own search ----

// How the hand-off went: Mattermost's search is open on the hashtag; there is
// no search bar to open in this window; or, from a popped-out panel, the
// window it came from did not answer.
export type HandOff = 'opened' | 'unavailable' | 'no-main-window';

const ASK = 'villanet:search';
const DONE = 'villanet:search:done';

// How long a popped-out panel waits to hear that its window has started the
// search. It answers at once; this is only for a window that has gone, or
// reloaded since it opened the popout and so no longer knows it is there.
const ANSWER_MS = 1500;

let asked = 0;
let listening = false;
const waiting = new Map<number, (opened: boolean) => void>();

function askMainWindow(term: string): Promise<HandOff> {
    const messaging = popoutMessaging();
    if (!messaging?.sendToParent || !messaging.onMessageFromParent) {
        return Promise.resolve('no-main-window');
    }

    // Once per page, because it cannot be taken back: a browser popout's
    // listener is added to the window and nothing to remove it is returned.
    if (!listening) {
        listening = true;
        messaging.onMessageFromParent((channel, ...args) => {
            if (channel !== DONE) {
                return;
            }

            const answer = waiting.get(Number(args[0]));
            waiting.delete(Number(args[0]));
            answer?.(args[1] === true);
        });
    }

    asked += 1;
    const id = asked;

    return new Promise((resolve) => {
        const timer = window.setTimeout(() => {
            waiting.delete(id);
            resolve('no-main-window');
        }, ANSWER_MS);

        waiting.set(id, (opened) => {
            window.clearTimeout(timer);
            resolve(opened ? 'opened' : 'unavailable');
        });

        try {
            messaging.sendToParent?.(ASK, term, id);
        } catch {
            window.clearTimeout(timer);
            waiting.delete(id);
            resolve('no-main-window');
        }
    });
}

// Opens Mattermost's own search on the hashtag. In the main window that
// replaces the panel in the side panel, which is what the button says it does;
// a popped-out panel has no search of its own, so the window it came from is
// asked to search instead, and the panel and its video stay where they are.
export function searchInMattermost(tag: string): Promise<HandOff> {
    const term = `#${tag}`;
    if (isPopoutWindow()) {
        return askMainWindow(term);
    }

    return Promise.resolve(openSearchHere(term) ? 'opened' : 'unavailable');
}

export type PopoutListeners = {
    sendToPopout?: (channel: string, ...args: unknown[]) => void;
    onMessageFromPopout?: (listener: (channel: string, ...args: unknown[]) => void) => void;
};

// The main window's half: registered with Mattermost for every panel
// of the plugin's popped out of it. It only ever searches for one hashtag, whatever the
// message says, so nothing else can be put into the search box this way.
export function answerPopout(listeners: PopoutListeners | undefined): void {
    listeners?.onMessageFromPopout?.((channel, ...args) => {
        if (channel !== ASK) {
            return;
        }

        const [term, id] = args;
        const opened = typeof term === 'string' && isHashtagTerm(term) && openSearchHere(term);
        try {
            listeners.sendToPopout?.(DONE, id, opened);
        } catch {
            // The popout has gone; it will say so itself when nobody answers.
        }

        if (opened) {
            window.focus();
        }
    });
}
