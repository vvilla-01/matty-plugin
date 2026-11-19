import React from 'react';

import {getBoards, lookAtBoards, onBoards, watchBoards} from './boards';
import type {Boards} from './types';

// The boards store (boards.ts) for a component: it re-renders whenever the
// store changes, including between its first render and its subscription
// (the read made as the page starts can land there). The away.ts pattern:
// no useSyncExternalStore, which Mattermost's React 17 does not have. Kept
// apart from the store so that the checks can load the store without React.
export function useBoards(): Boards {
    const [, bump] = React.useState(0);
    const current = getBoards();
    const rendered = React.useRef(current);
    rendered.current = current;

    React.useEffect(() => {
        const listener = () => {
            if (getBoards() !== rendered.current) {
                bump((value) => value + 1);
            }
        };
        const unsubscribe = onBoards(listener);

        // Anything that changed between the render and now.
        listener();

        return unsubscribe;
    }, []);

    return current;
}

// For something opened to be looked at for a moment, a profile popover: the
// boards as held, asked about again as it opens if the copy is a minute old.
export function useBoardsLookedAt(): Boards {
    React.useEffect(lookAtBoards, []);

    return useBoards();
}

// For something that shows a board for as long as it is open, the Trophies
// card: asked about again as it opens, and once a minute until it closes.
export function useBoardsWatched(): Boards {
    React.useEffect(watchBoards, []);

    return useBoards();
}
