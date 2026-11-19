import React from 'react';

import {widestFor} from './rhsWidth';
import {getSnapshot, subscribe} from './store';
import type {Snapshot, Stream} from './types';
import {embedHost, openOutside} from './twitchEmbed';
import {channelURL, fullRowStage, inlineSize, theatreSize, validLogin} from './twitchLogic';
import {getWatching, isWatching, onWatchingChange, setAudio, toggleWatching, watchOnly, watchStream} from './watching';

// Twitch streams in the panel (design/twitch-live.md 6): which handles are
// Twitch's, the theatre that gives Twitch's player room when the panel has
// none, how wide a tile would be, and what a card, the profile popover or Up
// next does when somebody asks to watch a Twitch stream.
//
// Module-level stores with hand-made subscriptions, as store.ts and
// watching.ts are, so the theatre's state outlives any one render of the tile
// that draws it, and a card in the list can open it.

type Listener = () => void;

function useObservable<T>(read: () => T, listeners: Set<Listener>): T {
    const [, forceRender] = React.useState(0);

    React.useEffect(() => {
        const listener = () => forceRender((value) => value + 1);
        listeners.add(listener);

        return () => {
            listeners.delete(listener);
        };
    }, [listeners]);

    return read();
}

// ---- The theatre (6.6) ----

// Which stream's player is in the theatre, or null. One at a time: opening
// one for another stream closes the first.
let theatre: string | null = null;
const theatreListeners = new Set<Listener>();

function theatreChanged(): void {
    Array.from(theatreListeners).forEach((listener) => listener());
}

export const getTheatre = (): string | null => theatre;

export function openTheatre(handle: string): void {
    if (theatre === handle) {
        return;
    }

    theatre = handle;
    theatreChanged();
}

export function closeTheatre(): void {
    if (theatre === null) {
        return;
    }

    theatre = null;
    theatreChanged();
}

export const useTheatre = (): string | null => useObservable(getTheatre, theatreListeners);

// ---- Which handles are Twitch's (6.7) ----

// Every handle seen with source 'twitch', kept while it is still in the
// snapshot as one or still in the watch list. A Twitch stream that has just
// left the snapshot, and whose tile is waiting for it, still counts: the
// small player and the clip viewer must not take it for a Broadcast Box
// stream with no connection.
const twitchHandles = new Set<string>();

function noteHandles(snapshot: Snapshot): void {
    const listed = new Set<string>();
    snapshot.streams.forEach((stream) => {
        if (stream.source === 'twitch') {
            listed.add(stream.handle);
            twitchHandles.add(stream.handle);
        }
    });

    const watched = new Set(getWatching());
    Array.from(twitchHandles).forEach((handle) => {
        if (!listed.has(handle) && !watched.has(handle)) {
            twitchHandles.delete(handle);
        }
    });
}

export function isTwitchHandle(handle: string | null): boolean {
    if (!handle) {
        return false;
    }

    return twitchHandles.has(handle) || getSnapshot().streams.some((stream) => stream.handle === handle && stream.source === 'twitch');
}

// ---- How wide a tile would be (6.9) ----

// The width of the panel's row of tiles, as last measured (Sidebar), which
// outlives the panel: a Watch from somebody's profile, with the panel
// closed, goes by the panel as it last was. null until it has been seen.
let measuredRow: number | null = null;

// The handles whose tile is playing inline right now: a tile already inline
// stays so down to 400, below the 408 a new one needs.
const inlineHandles = new Set<string>();

export function noteTileInline(handle: string, inline: boolean): void {
    if (inline) {
        inlineHandles.add(handle);
    } else {
        inlineHandles.delete(handle);
    }
}

// Watches the panel's stage, the full width of a row of tiles, for as long
// as it is there. For a layout effect; returns what stops it.
export function measureStageRow(element: HTMLElement | null): () => void {
    if (!element) {
        return () => undefined;
    }

    const take = () => {
        const width = element.getBoundingClientRect().width;
        if (width > 0) {
            measuredRow = width;
        }
    };
    take();

    if (typeof ResizeObserver === 'undefined') {
        window.addEventListener('resize', take);

        return () => window.removeEventListener('resize', take);
    }

    const observer = new ResizeObserver(take);
    observer.observe(element);

    return () => observer.disconnect();
}

// What a Twitch tile, which spans the whole row, would get: the row less its
// border. Before the panel has ever been seen, the width the panel would open
// at in a window this wide, less its padding — 400 below 1201 px, where
// Mattermost allows no wider, and Twitch therefore always plays bigger.
export function stageWidth(): number {
    if (measuredRow !== null) {
        return fullRowStage(measuredRow);
    }

    return fullRowStage((widestFor(window.innerWidth) ?? 400) - 24);
}

// ---- Starting to watch (6.9) ----

// Whether a stream can play inside the panel: always for Broadcast Box; for
// Twitch, while the admin allows it, on a page Twitch's player can run on,
// for a channel whose login could make a player address.
export function canWatchInPanel(stream: Stream, snapshot: Snapshot): boolean {
    return stream.source !== 'twitch' ||
        (Boolean(snapshot.twitchEmbed) && embedHost().ok && validLogin(stream.twitchLogin || ''));
}

// What a card, the profile popover or Up next does when somebody asks to
// watch: 'toggle' (a card's round button, adding it), 'only' (a card's body,
// Up next) or 'add' (the profile popover, which opens the panel).
//
// A Twitch stream that cannot play here opens on twitch.tv, a link the
// viewer chose. One that can is watched as any stream is; and when its tile
// would be too narrow for Twitch's player but the window has room, the
// theatre opens in the same click, with the sound — a narrow panel never
// needs a second click, and the sound still has the click behind it.
export function watchOrOpen(stream: Stream, how: 'toggle' | 'only' | 'add'): void {
    if (!canWatchInPanel(stream, getSnapshot())) {
        openOutside(channelURL(stream.twitchLogin || ''));

        return;
    }

    const handle = stream.handle;
    if (how === 'toggle') {
        toggleWatching(handle);
    } else if (how === 'only') {
        watchOnly(handle);
    } else {
        watchStream(handle);
    }

    if (stream.source !== 'twitch' || !isWatching(handle)) {
        return;
    }

    // The sound first, so the theatre's player loads once, already with it.
    const fits = inlineHandles.has(handle) || inlineSize(stageWidth(), false) !== null;
    if (!fits && theatreSize(window.innerWidth, window.innerHeight) !== null) {
        setAudio(handle);
        openTheatre(handle);
    }
}

// The same, for what has only a handle: the Up next rail, a card's body. A
// handle no longer in the snapshot is watched as before, and its tile says
// it is waiting.
export function watchHandle(handle: string, how: 'only' | 'add'): void {
    const stream = getSnapshot().streams.find((candidate) => candidate.handle === handle);
    if (stream) {
        watchOrOpen(stream, how);
    } else if (how === 'only') {
        watchOnly(handle);
    } else {
        watchStream(handle);
    }
}

// ---- The "me" event, for Twitch (4.10) ----

// One of the reader's own connections may have changed (the "me" event:
// me.ts), and "me" is read again: `read` is that read, which resolves when it
// has come back. The Connect sheet listens here, to look then whether its
// window did its work.
type LinkListener = (read: Promise<unknown>) => void;

const linkListeners = new Set<LinkListener>();

export function twitchLinkHeard(read: Promise<unknown>): void {
    Array.from(linkListeners).forEach((listener) => listener(read));
}

export function onTwitchLink(listener: LinkListener): () => void {
    linkListeners.add(listener);

    return () => {
        linkListeners.delete(listener);
    };
}

// ---- Installing ----

// From initialize(), as the session pool is: the handles followed on every
// snapshot, and the theatre closed when its stream leaves the watch list
// without a tile there to close it. Returns what undoes it.
export function installTwitch(): () => void {
    noteHandles(getSnapshot());
    const unsubscribe = subscribe(() => noteHandles(getSnapshot()));
    const unwatch = onWatchingChange(() => {
        if (theatre !== null && !getWatching().includes(theatre)) {
            closeTheatre();
        }
    });

    return () => {
        unsubscribe();
        unwatch();
        twitchHandles.clear();
        inlineHandles.clear();
        linkListeners.clear();
        theatre = null;
        theatreChanged();
        measuredRow = null;
    };
}
