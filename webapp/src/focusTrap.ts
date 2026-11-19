// Tab going round a dialog rather than out of it, into a page that cannot be
// used while the dialog is over it. For the Clips dialog, the clip editor and
// the Twitch theatre.
//
// HashtagSearch has the first version of this, which knew only links,
// buttons and tabindex 0. A dialog with a title field, a trimmer and a
// player needs more: inputs and text areas, a <video> with its own controls
// (focusable, with no tabindex of its own), Twitch's player in its frame
// (focusable too, and the only way a keyboard reaches its play, sound and
// fullscreen), and nothing inside an [inert] part — React 18 has no inert
// prop, so the editor sets the attribute, and what is under it cannot take
// focus however the selector reads. Nor anything hidden: a view kept in the
// page but not shown.

const STOPS = [
    'a[href]',
    'button:not([disabled])',
    'input:not([disabled]):not([type="hidden"])',
    'select:not([disabled])',
    'textarea:not([disabled])',
    'video[controls]',
    'iframe',
    '[tabindex]',
].join(', ');

export function focusStops(box: HTMLElement): HTMLElement[] {
    return Array.from(box.querySelectorAll<HTMLElement>(STOPS)).filter((element) => (
        (element.tabIndex >= 0 || element.matches('video[controls]')) &&
        element.closest('[inert]') === null &&
        element.closest('[hidden]') === null &&
        element.getClientRects().length > 0
    ));
}

// For a capture-phase keydown listener on the document: Tab and Shift+Tab
// wrap from the last stop to the first and back, and a Tab from outside the
// box — or from the box itself, which a dialog focuses when what had focus
// went away — comes into it. Nothing else is touched, so the browser moves
// focus between the stops itself.
export function trapTab(event: KeyboardEvent, box: HTMLElement | null): void {
    if (event.key !== 'Tab' || !box || event.ctrlKey || event.altKey || event.metaKey) {
        return;
    }

    const stops = focusStops(box);
    if (stops.length === 0) {
        event.preventDefault();

        return;
    }

    const first = stops[0];
    const last = stops[stops.length - 1];
    const active = document.activeElement;
    if (!box.contains(active) || active === box) {
        event.preventDefault();
        (event.shiftKey ? last : first).focus();
    } else if (event.shiftKey && active === first) {
        event.preventDefault();
        last.focus();
    } else if (!event.shiftKey && active === last) {
        event.preventDefault();
        first.focus();
    }
}

// The rule in spec 3.5: Mattermost sends almost any printable key to its
// message box, from a keydown listener on the document. A dialog's card
// stops those keys from going further than itself, in the bubble phase so
// its own handlers have them first, and without preventDefault so they still
// type into its fields and drive its native controls.
export function keepKeysInside(event: {key: string; ctrlKey: boolean; metaKey: boolean; altKey: boolean; stopPropagation: () => void}): void {
    if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) {
        event.stopPropagation();
    }
}

// For the same capture-phase listener: a printable key pressed while focus is
// on nothing at all — the page's body, after the button that had it was
// taken out of the dialog — would go on to Mattermost's message box, since
// the dialog's own bubble-phase stop (keepKeysInside) never sees it. A modal
// dialog's keys are its own, so the key is stopped here and focus brought
// back into the box. Returns whether it did.
export function catchStrayKey(event: KeyboardEvent, box: HTMLElement | null): boolean {
    const active = document.activeElement;
    if (!box || (active && active !== document.body)) {
        return false;
    }
    if (event.key.length !== 1 || event.ctrlKey || event.metaKey || event.altKey) {
        return false;
    }

    event.stopPropagation();
    box.focus({preventScroll: true});

    return true;
}

// Whether a key press is a paste, by the rule Mattermost's message box goes
// by (utils/keyboard.ts isKeyPressed, which post_utils.ts
// shouldFocusMainTextbox asks): Cmd, or Ctrl without Alt, with V. V by the
// letter the key types or, where the layout types another letter there —
// Russian, Ukrainian, Greek, Hebrew, Arabic — by the key's code, 86, which is
// how Mattermost still sees that Ctrl+V as a paste and takes it to its box.
// Going by the letter alone left those keyboards' pastes to Mattermost. Ctrl
// with Alt is AltGr on Windows, which types a character; and an input
// method's keys (229) are never a paste.
export function isPasteKey(event: {key: string; keyCode?: number; ctrlKey: boolean; metaKey: boolean; altKey: boolean}): boolean {
    if (!event.metaKey && !(event.ctrlKey && !event.altKey)) {
        return false;
    }
    if (event.keyCode === 229) {
        return false;
    }
    if (event.key === 'v' || event.key === 'V') {
        return true;
    }

    return event.keyCode === 86;
}

// Every bearer token bar's key rule (design/lock-screen.md 4.5), for the
// onKeyDown of whatever holds the bar: the lock, the let-in footer's sheet,
// the relink form. Mattermost sends a printable key, or a paste, pressed
// anywhere but in a text field — a button included — to its message box,
// where synced drafts save it on the server at once and the next Enter posts
// it. Here such a key goes into the bar instead: focus is moved there during
// the keydown, as Mattermost moves it to its own box, so the character or the
// paste lands in the bar; and the key goes no further. While the bar takes no
// typing (checking, unlocking), the key is only stopped. Space on a button or
// a link is left alone, because it presses it, as Mattermost leaves it; so
// are the bar's own keys, Tab, F6, Enter, Escape and other shortcuts.
export function keepTokenKeys(
    event: {key: string; keyCode?: number; ctrlKey: boolean; metaKey: boolean; altKey: boolean; target: EventTarget; stopPropagation: () => void},
    bar: HTMLInputElement | null,
): void {
    const target = event.target as HTMLElement | null;
    if (bar && target === bar) {
        return;
    }

    const command = event.ctrlKey || event.metaKey;
    const paste = isPasteKey(event);
    const printable = event.key.length === 1 && !command && !event.altKey;
    if (!paste && !printable) {
        return;
    }
    if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) {
        return;
    }
    if (event.key === ' ' && target && target.closest('button, a, [role="button"], [role="switch"]')) {
        return;
    }

    if (bar && bar.isConnected && !bar.readOnly && !bar.disabled) {
        bar.focus({preventScroll: true});
    }
    event.stopPropagation();
}
