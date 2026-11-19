import React from 'react';

// ---- The hashtag being searched for ----
// Which hashtag's results are showing over the panel, if any. Kept here, apart
// from the buttons that set it (linkify.tsx), rather than in whichever tile or
// card was pressed: the dialog has to outlive a title that changes or a ticker
// that stops while it is open, and only one is ever open. Its own module so
// that linkify.tsx keeps nothing at module level (design/3.2.0.md 2.6).

type Listener = () => void;

const listeners = new Set<Listener>();

let openTag: string | null = null;

export function openHashtag(tag: string): void {
    openTag = tag;
    listeners.forEach((listener) => listener());
}

export function closeHashtag(): void {
    if (openTag !== null) {
        openTag = null;
        listeners.forEach((listener) => listener());
    }
}

export function useOpenHashtag(): string | null {
    const [, forceRender] = React.useState(0);

    React.useEffect(() => {
        const listener = () => forceRender((value) => value + 1);
        listeners.add(listener);

        return () => {
            listeners.delete(listener);
        };
    }, []);

    return openTag;
}
