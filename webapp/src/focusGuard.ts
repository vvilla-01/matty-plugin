// Putting focus where it belongs after the plugin's side panel opens, without
// fighting Mattermost over it. Used by away.ts after a return, and by the lock
// screen for its bearer token bar when somebody opened the panel themselves.
//
// Mattermost moves focus into new side-panel content two timer hops after it
// renders, and restores it on a close (sidebar_right.tsx, handleRHSFocus;
// a11y_utils.ts), so no timer of the plugin's can be relied on to run last.
// Instead this waits for Mattermost's own move into the side panel and
// corrects it, or applies itself at the deadline if Mattermost never moves
// it. The person pressing or typing anything first ends it:
// they have put focus where they want it. By default it only ever moves focus
// that is on the page's body or inside the side panel; a caller can let it
// take focus from more (the lock: from anything but a text field), and it
// never takes it out of a text field somewhere else.

// How long it waits for Mattermost's own move before applying itself.
export const FOCUS_GUARD_MS = 600;

function sidebarRight(): HTMLElement | null {
    return document.getElementById('sidebar-right');
}

// Whether focus is somewhere a person types: never taken from there.
export function inTextField(element: Element | null): boolean {
    if (!element) {
        return false;
    }

    return element.tagName === 'INPUT' || element.tagName === 'TEXTAREA' || element.tagName === 'SELECT' ||
        (element instanceof HTMLElement && element.isContentEditable) ||
        element.closest('[contenteditable="true"]') !== null;
}

// The lock's rule for its bar: focus may be taken from anything that is not
// a text field. Opening the panel by hand leaves focus on what was pressed —
// the App Bar's icon, a header button — whenever Mattermost does not move it
// into the side panel itself (it does not when the panel was already open on
// a thread, a search or anything else), and a Ctrl+V on that button would
// go to the message box.
export const fromAnythingButText = (active: Element): boolean => !inTextField(active);

type GuardOptions = {
    deadlineMs?: number;

    // Where else focus may be taken from, beyond the page's body and the
    // side panel. Never a text field, whatever this says.
    from?: (active: Element) => boolean;

    // Put focus there at once as well, rather than first waiting for
    // Mattermost's move: a key pressed on the opener in the meantime would
    // otherwise be Mattermost's. A later move of Mattermost's is still put
    // right.
    atOnce?: boolean;
};

// Starts a guard that puts focus on whatever target() names, and returns what
// stops it. Stopping twice is harmless.
export function startFocusGuard(target: () => HTMLElement | null, options: GuardOptions = {}): () => void {
    const deadlineMs = options.deadlineMs ?? FOCUS_GUARD_MS;
    const timers: number[] = [];
    let stopped = false;

    const settle = () => {
        const element = target();
        if (!element || !element.isConnected || document.activeElement === element) {
            return;
        }

        const active = document.activeElement;
        const rhs = sidebarRight();
        const allowed = !active || active === document.body || (rhs !== null && rhs.contains(active)) ||
            (options.from !== undefined && !inTextField(active) && options.from(active));
        if (allowed) {
            try {
                element.focus({preventScroll: true});
            } catch {
                // Not focusable after all; Mattermost's choice stands.
            }
        }
    };

    const stop = () => {
        if (stopped) {
            return;
        }
        stopped = true;
        document.removeEventListener('pointerdown', stop, true);
        document.removeEventListener('keydown', stop, true);
        document.removeEventListener('focusin', moved, true);
        timers.forEach((timer) => window.clearTimeout(timer));
    };

    // Mattermost moving focus into the side panel: put right a moment later,
    // once its own move has landed.
    function moved(event: FocusEvent): void {
        const rhs = sidebarRight();
        if (rhs && event.target instanceof Node && rhs.contains(event.target)) {
            timers.push(window.setTimeout(settle, 0));
        }
    }

    document.addEventListener('pointerdown', stop, true);
    document.addEventListener('keydown', stop, true);
    document.addEventListener('focusin', moved, true);
    if (options.atOnce) {
        settle();
    }
    timers.push(window.setTimeout(() => {
        settle();
        stop();
    }, deadlineMs));

    return stop;
}
