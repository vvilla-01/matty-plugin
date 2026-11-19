import React from 'react';

// What this server calls the plugin, and which of its pictures it has.
//
// Every server names the plugin for itself (the Name setting, "Matty" when
// it is blank) and may upload an emblem for behind the panel and a ghost for
// the No Signal screen; the icon always exists. The server says both in
// GET /api/v1/access, GET /api/v1/me and the "me" event (contract/README.md),
// and whichever of them arrives is taken (takeBrand). Each is remembered in
// this browser, so a reload draws what the server said last rather than
// flashing the default and then the server's own: the name "Matty" and no
// pictures until the server has said anything at all.
//
// Module-level, as identity.ts is, because the side panel's title, the App
// Bar's tooltip, the channel header's button and the panel are separate React
// trees. nameNow() is for code that makes a sentence when it is asked;
// useName() and useArtwork() are for components, which draw again when the
// server's answer changes; onNameChange() is for what has to be done again
// outside React when the name changes (Mattermost's Settings dialog, whose
// entry is a plain string).

// The product's own name: what a server is called that has not named the
// plugin, and the default before any answer.
export const PRODUCT_NAME = 'Matty';

// The longest name the server's setting allows, in characters.
export const NAME_MAX = 32;

export type Artwork = {emblem: boolean; ghost: boolean};

const NO_ARTWORK: Artwork = {emblem: false, ghost: false};

// localStorage, each read and written inside try: a private window, blocked
// site data or a full store must cost nothing but the memory.
const NAME_KEY = 'matty.name';
const ARTWORK_KEY = 'matty.artwork';

function remembered(key: string): string | null {
    try {
        return window.localStorage.getItem(key);
    } catch {
        return null;
    }
}

function remember(key: string, value: string): void {
    try {
        window.localStorage.setItem(key, value);
    } catch {
        // Not remembered; the next answer says it again.
    }
}

// A name worth showing: text, trimmed, from one to NAME_MAX characters, with
// no control characters. Anything else is not taken.
export function saneName(value: unknown): string | null {
    if (typeof value !== 'string') {
        return null;
    }

    const name = value.trim();
    const length = Array.from(name).length;
    if (length === 0 || length > NAME_MAX) {
        return null;
    }

    for (const char of name) {
        const code = char.codePointAt(0) || 0;
        if (code < 0x20 || (code >= 0x7f && code < 0xa0) || code === 0x2028 || code === 0x2029) {
            return null;
        }
    }

    return name;
}

// Artwork as the server writes it, {emblem, ghost}, both true or false.
export function saneArtwork(value: unknown): Artwork | null {
    if (!value || typeof value !== 'object') {
        return null;
    }

    const {emblem, ghost} = value as {emblem?: unknown; ghost?: unknown};
    if (typeof emblem !== 'boolean' || typeof ghost !== 'boolean') {
        return null;
    }

    return {emblem, ghost};
}

function rememberedArtwork(): Artwork | null {
    const text = remembered(ARTWORK_KEY);
    if (!text) {
        return null;
    }

    try {
        return saneArtwork(JSON.parse(text));
    } catch {
        return null;
    }
}

let name: string = saneName(remembered(NAME_KEY)) || PRODUCT_NAME;

// null until the server, or this browser's memory of it, has said: nothing
// is drawn until then.
let artwork: Artwork | null = rememberedArtwork();

const listeners = new Set<() => void>();
const nameListeners = new Set<() => void>();

// The name, now. Never empty.
export const nameNow = (): string => name;

// What the server has said it has, or null before it has said.
export const artworkNow = (): Artwork | null => artwork;

// Told whenever the name changes; returns what stops it.
export function onNameChange(listener: () => void): () => void {
    nameListeners.add(listener);

    return () => {
        nameListeners.delete(listener);
    };
}

function subscribe(listener: () => void): () => void {
    listeners.add(listener);

    return () => {
        listeners.delete(listener);
    };
}

// A value of this module for a component, drawn again whenever it changes,
// including between the render and the subscription. The useBoards pattern:
// no useSyncExternalStore, which Mattermost's React 17 does not have.
function useBrandValue<T>(read: () => T): T {
    const [, bump] = React.useState(0);
    const current = read();
    const rendered = React.useRef(current);
    rendered.current = current;

    React.useEffect(() => {
        const listener = () => {
            if (read() !== rendered.current) {
                bump((value) => value + 1);
            }
        };
        const unsubscribe = subscribe(listener);
        listener();

        return unsubscribe;
    }, []);

    return current;
}

// For components: the name, drawn again when it changes.
export const useName = (): string => useBrandValue(nameNow);

const artworkOrNone = (): Artwork => artwork || NO_ARTWORK;

// For components: which pictures to draw. Neither, until the server has said.
export const useArtwork = (): Artwork => useBrandValue(artworkOrNone);

// The name and the artwork from any answer that carries them: GET /access,
// GET /me, or the "me" event's data. Whatever is missing or not sane is left
// as it was.
export function takeBrand(answer: unknown): void {
    if (!answer || typeof answer !== 'object') {
        return;
    }

    const said = answer as {name?: unknown; artwork?: unknown};
    let changed = false;
    let renamed = false;

    const nextName = saneName(said.name);
    if (nextName !== null && nextName !== name) {
        name = nextName;
        changed = true;
        renamed = true;
    }
    if (nextName !== null) {
        remember(NAME_KEY, nextName);
    }

    const nextArtwork = saneArtwork(said.artwork);
    if (nextArtwork !== null) {
        if (artwork === null || artwork.emblem !== nextArtwork.emblem || artwork.ghost !== nextArtwork.ghost) {
            artwork = nextArtwork;
            changed = true;
        }
        remember(ARTWORK_KEY, JSON.stringify(nextArtwork));
    }

    if (changed) {
        Array.from(listeners).forEach((listener) => listener());
    }
    if (renamed) {
        Array.from(nameListeners).forEach((listener) => listener());
    }
}
