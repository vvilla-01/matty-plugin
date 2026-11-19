import {mattermostStore} from './mattermost';
import {isPopoutWindow} from './popout';

// The side panel as wide as it can usefully go, while the panel is in it — and
// wider again when the window is.
//
// Mattermost sizes its right-hand panel from one CSS variable,
// --overrideRhsWidth: its own default of 400px, or whatever width the person
// last dragged the divider to, set on :root. The panel AND the spacer that
// makes room for it in the page read it, and both are capped per screen size
// by Mattermost's own max-width: 776px from 1681px wide, 464px from 1201px,
// 400px below. (webapp/channels/src/sass/layout/_sidebar-right.scss and
// components/resizable_sidebar, 9.1 to master.)
//
// 776px is where Mattermost stops however big the screen, and on a widescreen
// monitor that leaves the television a narrow strip down the side of a lot of
// empty channel. So past Mattermost's own caps this lets the panel go on
// growing with the window: half of it, while leaving at least WINDOW_RESERVE
// for Mattermost's own sidebar and the channel beside it — and no further than
// PANEL_MAX, where the panel's own content stops, so an ultrawide monitor gives
// its spare room to the channel rather than to blank space either side of the
// television. Worked out here, set on <body> as the variable and as a matching
// max-width, and worked out again whenever the window changes size —
// maximising a window widens it.
//
// The divider still works, and while this panel is open it is THIS panel's
// width it changes. Dragging it narrower keeps that width — in this browser,
// for this panel — and dragging it back out to the widest undoes it; a click
// on it that goes nowhere changes nothing, and a double-click, Mattermost's
// own "back to the default", goes back to the widest. Mattermost records every
// press of its divider as the width for everything else that uses the side
// panel — threads, search, pinned messages — and while this panel is widening
// that record would be this panel's width, so it is put back as it was. The
// width somebody had for those is the width they still have.
//
// In a window too narrow for any of this, below 1201px, the panel is
// Mattermost's alone: nothing is set, and a drag there is Mattermost's
// business, recorded as Mattermost records it.

const WIDE_CLASS = 'vntv-rhs-wide';

// Set as well while expanding would add nothing — the panel already as wide as
// Mattermost's expanded view would make it — which is when the stylesheet
// hides Mattermost's Expand button.
const FULL_CLASS = 'vntv-rhs-full';

// The width somebody dragged this panel to, in pixels; absent while it is as
// wide as it goes.
const CHOICE_KEY = 'villanet.rhsChosenWidth';

// 2.6.2's yes-or-no in place of a width, whose rule was "any drag at all means
// stand aside for good". Nothing reads it; it is only cleared away.
const OLD_CHOICE_KEY = 'villanet.rhsWidthChosen';

// Room left for Mattermost's own sidebar (240px) and a usable channel: 960 in
// 2.7.0, which was a little wide in a window, 1045 in 2.7.1 (a 547px panel in
// a 1592px Mattermost window), and 1053 since, which makes that 539px, the
// size it was asked to be there. A maximised ultrawide still reaches
// PANEL_MAX.
const WINDOW_RESERVE = 1053;

// The widest the panel is worth being: its content stops at 1100px (the
// ".vntv-body > *" rule in styles.ts), inside 12px of padding either side,
// with room for a scrollbar. Any wider is blank space either side of it — which
// is what a 3440px ultrawide showed, at 1720px.
const PANEL_MAX = 1140;

// Mattermost's expanded view is 60% of the window; here it goes no wider than
// the content either, and never narrower than the panel already is.
const EXPANDED_SHARE = 0.6;

// How far short of the widest a drag can end and still mean "the widest".
const NEAR_ENOUGH = 24;

// How soon after a press of the divider ends its double-click can follow. The
// browser sends it straight after the second press ends; this is only slack.
const DOUBLE_CLICK_MS = 500;

// Where Mattermost keeps the width its divider was last left at: one entry in
// its own stored state per user, named for the divider and the user's id.
// (components/resizable_sidebar/resizable_divider.tsx, through
// stores/hooks.ts useGlobalState, and reducers/storage.ts; 9.1 to master.)
const MATTERMOST_WIDTH_KEY = 'resizable_rhsResizeHandle:';

// Mattermost's own cap for a window this wide, as its stylesheet sets it.
function mattermostCap(viewport: number): number {
    if (viewport >= 1681) {
        return 776;
    }

    return viewport >= 1201 ? 464 : 400;
}

// How wide this makes the panel in a window this wide, or null where it
// leaves Mattermost alone: below 1201px the panel is already as wide as the
// layout has room for.
export function widestFor(viewport: number): number | null {
    if (viewport < 1201) {
        return null;
    }

    const grown = Math.min(viewport * 0.5, viewport - WINDOW_RESERVE, PANEL_MAX);

    return Math.round(Math.max(mattermostCap(viewport), grown));
}

// How wide Mattermost's expanded view is while this is widening: its own 60%
// of the window, up to where the content stops, and never narrower than the
// panel — the spacer beside it keeps the panel's width, so anything narrower
// would leave a strip of nothing between the channel and the panel.
export function expandedFor(viewport: number, widest: number): number {
    return Math.round(Math.max(widest, Math.min(viewport * EXPANDED_SHARE, PANEL_MAX)));
}

// A choice the browser would not keep — storage that is full or switched off
// — held for this visit instead, rather than thrown away the moment it is
// made. Undefined whenever the browser's own copy is the one to read, which
// is also what another tab changes.
let unsaved: number | null | undefined;

function chosenWidth(): number | null {
    if (unsaved !== undefined) {
        return unsaved;
    }

    try {
        const px = Number(window.localStorage.getItem(CHOICE_KEY));

        return Number.isFinite(px) && px > 0 ? px : null;
    } catch {
        return null;
    }
}

function rememberChosenWidth(px: number | null): void {
    try {
        if (px === null) {
            window.localStorage.removeItem(CHOICE_KEY);
        } else {
            window.localStorage.setItem(CHOICE_KEY, String(px));
        }
        unsaved = undefined;
    } catch {
        unsaved = px;
    }
}

// Mattermost's record of its divider's width, as it stands: the entry's name
// and the entry itself, which is undefined when there is none. Null when this
// cannot tell — no store, no user, a shape it does not recognise — and then
// nothing is put back.
type StoredWidth = {key: string; entry: unknown};

type MattermostState = {
    entities?: {users?: {currentUserId?: unknown}};
    storage?: {storage?: unknown};
};

function mattermostRecord(): StoredWidth | null {
    try {
        const state = mattermostStore()?.getState() as MattermostState | undefined;
        const userId = state?.entities?.users?.currentUserId;
        const entries = state?.storage?.storage;
        if (typeof userId !== 'string' || userId === '' || !entries || typeof entries !== 'object') {
            return null;
        }

        const key = MATTERMOST_WIDTH_KEY + userId;

        return {key, entry: (entries as {[key: string]: unknown})[key]};
    } catch {
        return null;
    }
}

// Put Mattermost's record back exactly as it was, width and timestamp both.
// Removed first and then written: Mattermost only takes a write stamped later
// than the entry it holds, and the one being put back is older by definition.
// Nothing is dispatched when the record is already what it was.
function restoreMattermostRecord(was: StoredWidth | null): void {
    const store = mattermostStore();
    const now = mattermostRecord();
    if (!store || !was || !now || now.key !== was.key || now.entry === was.entry) {
        return;
    }

    try {
        store.dispatch({type: 'REMOVE_GLOBAL_ITEM', data: {name: was.key}});

        const entry = was.entry as {value?: unknown; timestamp?: unknown} | undefined;
        if (entry && typeof entry === 'object') {
            store.dispatch({type: 'SET_GLOBAL_ITEM', data: {name: was.key, value: entry.value, timestamp: entry.timestamp}});
        }
    } catch {
        // Left as Mattermost wrote it.
    }
}

// For a React effect: widen now and whenever the window changes, and put
// things back when the panel closes.
export function widenWhileOpen(): () => void {
    const body = document.body;

    // A popped-out side panel has no side panel to widen: the panel is the
    // window, as wide as it is. (A popped-out channel has one, and widens it.)
    if (isPopoutWindow()) {
        return () => undefined;
    }

    try {
        window.localStorage.removeItem(OLD_CHOICE_KEY);
    } catch {
        // Nothing to clear away, or no way to.
    }

    const standAside = () => {
        body.classList.remove(WIDE_CLASS, FULL_CLASS);
        body.style.removeProperty('--overrideRhsWidth');
        body.style.removeProperty('--vntv-rhs-max');
        body.style.removeProperty('--vntv-rhs-expanded');
    };

    // The widest, as the cap, whatever the width: a narrower width somebody
    // chose above Mattermost's own cap would otherwise be clamped down to it.
    const apply = () => {
        const widest = widestFor(window.innerWidth);
        if (widest === null) {
            standAside();

            return;
        }

        const chosen = chosenWidth();
        const width = chosen === null ? widest : Math.min(Math.round(chosen), widest);

        body.classList.add(WIDE_CLASS);
        body.style.setProperty('--overrideRhsWidth', `${width}px`);
        body.style.setProperty('--vntv-rhs-max', `${widest}px`);
        const expanded = expandedFor(window.innerWidth, widest);
        body.style.setProperty('--vntv-rhs-expanded', `${expanded}px`);

        // From 1280px, where the expanded width above applies; below that it
        // is Mattermost's own, which is always wider than the panel here.
        body.classList.toggle(FULL_CLASS, window.innerWidth >= 1280 && expanded - width < NEAR_ENOUGH);
    };

    // Straight from the resize event, not deferred to an animation frame:
    // browsers already send resize at most once a frame, and a window
    // resized while it is hidden — restored from the taskbar, or the desktop
    // app maximised in the background — runs no animation frames at all
    // until it is shown, so a deferred width would arrive late. apply() only
    // writes three properties and a class, and writing the same value again
    // changes nothing.
    apply();
    window.addEventListener('resize', apply);

    // One press of the divider, read from the variable Mattermost sets inline
    // on the panel for exactly as long as the press lasts: the width it began
    // at, the width it has reached, whether this was widening when it began,
    // and Mattermost's record as it stood before the press — which Mattermost
    // overwrites as the press ends, just before it takes the variable off.
    type Press = {start: number; end: number; widest: number | null; before: StoredWidth | null};
    let press: Press | null = null;

    // The last press, and Mattermost's record from before it, for the
    // double-click that may follow it. Any press, not only one that went
    // nowhere: a hand drifts a pixel between the two clicks, and the browser
    // still calls it a double-click — so does Mattermost.
    let lastPress: {at: number; before: StoredWidth | null} | null = null;

    let observer: MutationObserver | null = null;
    const panel = document.getElementById('sidebar-right');
    if (panel && typeof MutationObserver !== 'undefined') {
        observer = new MutationObserver(() => {
            const inline = panel.style.getPropertyValue('--overrideRhsWidth');
            if (inline !== '') {
                const px = parseFloat(inline);
                if (!Number.isFinite(px)) {
                    return;
                }

                if (press === null) {
                    press = {start: px, end: px, widest: widestFor(window.innerWidth), before: mattermostRecord()};
                } else {
                    press.end = px;
                }

                return;
            }

            if (press === null) {
                return;
            }

            const {start, end, widest, before} = press;
            press = null;

            // Mattermost's own panel, in a window too narrow to widen it.
            if (widest === null) {
                return;
            }

            restoreMattermostRecord(before);
            lastPress = {at: Date.now(), before};

            if (Math.abs(end - start) < 1) {
                return;
            }

            // Where it ended, as far as the panel could follow: Mattermost's
            // divider keeps counting past the narrowest the panel is drawn.
            const narrowest = parseFloat(window.getComputedStyle(panel).minWidth) || 0;
            const px = Math.round(Math.max(end, narrowest));

            rememberChosenWidth(px >= widest - NEAR_ENOUGH ? null : px);
            apply();
        });
        observer.observe(panel, {attributes: true, attributeFilter: ['style']});
    }

    // A double-click on the divider is Mattermost's "back to the default
    // width", which for this panel is the widest. The divider is the one thing
    // inside #sidebar-right besides the panel's own content, which is how a
    // double-click on it is told from one on the words beside it; and it
    // reaches the window after Mattermost's own handler has cleared its
    // record — which is put back like any other press.
    const onDoubleClick = (event: MouseEvent) => {
        const target = event.target;
        if (!panel || !(target instanceof Element) || target.parentElement !== panel || target.classList.contains('sidebar-right-container')) {
            return;
        }

        const last = lastPress;
        lastPress = null;
        if (last === null || Date.now() - last.at > DOUBLE_CLICK_MS) {
            return;
        }

        restoreMattermostRecord(last.before);
        rememberChosenWidth(null);
        apply();
    };
    window.addEventListener('dblclick', onDoubleClick);

    return () => {
        window.removeEventListener('resize', apply);
        window.removeEventListener('dblclick', onDoubleClick);
        observer?.disconnect();
        standAside();
    };
}
