import React from 'react';

// Somewhere for focus to go when the element holding it is taken out of the
// page. A button that unmounts with focus on it leaves focus on the page's
// body, and from there Mattermost sends the next printable key to its
// message box (spec 3.5): a tile swapped for the one that waits for its
// stream to come back, the scissors going when the end of a stream is let
// go, Wipe once there is nothing left to wipe.
//
// The handlers go on something that outlives what they watch, and remember
// what inside it has focus. After each render of the component that owns
// them, if that element has left the page — or been disabled, which drops
// focus as surely, as a tile's switch is while it turns off — and focus is
// on nothing at all, focus goes to home(). Never when focus is somewhere
// else, and never when the element is still there to be used: that is
// somebody who clicked away, on purpose, onto something that takes no focus.
//
// A blur with nowhere to go is one of those: a click away, or the element
// taken out of the page or disabled. Which one is looked at once the event
// is over: an element still there and usable was clicked away from, and is
// forgotten, so a later render never pulls focus back from where the person
// left it.
//
// watch, when given, narrows what is looked after to part of what the
// handlers are on: focus anywhere else there is forgotten, as focus that
// left altogether is.
export type FocusHome = {
    onFocus: (event: React.FocusEvent<HTMLElement>) => void;
    onBlur: (event: React.FocusEvent<HTMLElement>) => void;
};

// Whether focus on it has gone with it: out of the page, or disabled.
const dropped = (element: Element): boolean => !element.isConnected || element.matches(':disabled');

export function useFocusHome(home: () => HTMLElement | null, watch?: (target: Element) => boolean): FocusHome {
    const held = React.useRef<Element | null>(null);
    const box = React.useRef<Element | null>(null);
    const homeOf = React.useRef(home);
    homeOf.current = home;
    const watchOf = React.useRef(watch);
    watchOf.current = watch;

    React.useLayoutEffect(() => {
        const element = held.current;
        if (!element) {
            return;
        }

        // Somewhere else altogether, by a way no blur here saw: forgotten.
        // Not while it is still on the element held, wherever that is in the
        // page: a portal's content (a Twitch tile's theatre, on the page's
        // body) sends its focus here through React without being inside the
        // box, and forgetting it at the next render would leave nowhere to
        // go when the portal is taken out with its owner.
        const active = document.activeElement;
        if (active && active !== document.body) {
            if (active !== element && box.current && !box.current.contains(active)) {
                held.current = null;
            }

            return;
        }

        held.current = null;
        if (dropped(element)) {
            homeOf.current()?.focus({preventScroll: true});
        }
    });

    const onFocus = React.useCallback((event: React.FocusEvent<HTMLElement>) => {
        const watched = watchOf.current;
        held.current = !watched || watched(event.target) ? event.target : null;
        box.current = event.currentTarget;
    }, []);

    const onBlur = React.useCallback((event: React.FocusEvent<HTMLElement>) => {
        const next = event.relatedTarget as Node | null;
        if (next) {
            if (!event.currentTarget.contains(next)) {
                held.current = null;
            }

            return;
        }

        const left = event.target;
        void Promise.resolve().then(() => {
            if (held.current === left && !dropped(left)) {
                held.current = null;
            }
        });
    }, []);

    return {onFocus, onBlur};
}
