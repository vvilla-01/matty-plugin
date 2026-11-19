import type React from 'react';

import {contrastingSimpleColor} from './color';
import type {Me} from './types';

// The classification banner at the top of the panel, and at the bottom too
// when the placement is "Top and bottom": the same on the lock screen as on
// the panel (design/lock-screen.md 4.1). The server's Banner text, colour
// and placement: no text, no banner.
//
// Nothing is assumed before the server answers. So that a server that has a
// banner does not see it blink in on every reload, the last banner "me"
// brought is remembered in this browser (identity.ts rememberBanner) and
// drawn until "me" answers again; a browser that has never had an answer
// draws none.

type Banner = {text: string; color: string; placement: 'top' | 'both'};

const BANNER_KEY = 'matty.banner';

const NO_BANNER: Banner = {text: '', color: '', placement: 'both'};

// A colour the server would send: a hex colour, or '' for the theme's accent.
const HEX = /^#[0-9a-fA-F]{3}(?:[0-9a-fA-F]{3})?$/;

function bannerFrom(value: unknown): Banner | null {
    if (!value || typeof value !== 'object') {
        return null;
    }

    const {text, color, placement} = value as {text?: unknown; color?: unknown; placement?: unknown};
    // No bound on the text: the server sets none, and a remembered banner
    // that is not read back would blink in on every reload.
    if (typeof text !== 'string' || typeof color !== 'string' || (color !== '' && !HEX.test(color))) {
        return null;
    }

    return {text, color, placement: placement === 'top' ? 'top' : 'both'};
}

function bannerOfMe(me: Me): Banner {
    return {
        text: typeof me.bannerText === 'string' ? me.bannerText : '',
        color: typeof me.bannerColor === 'string' ? me.bannerColor : '',
        placement: me.bannerPlacement === 'top' ? 'top' : 'both',
    };
}

// Read once, as the page starts: what the last answer said.
let remembered: Banner | null = (() => {
    try {
        const text = window.localStorage.getItem(BANNER_KEY);

        return text ? bannerFrom(JSON.parse(text)) : null;
    } catch {
        return null;
    }
})();

// Kept for the next reload, whenever "me" comes.
export function rememberBanner(me: Me): void {
    const next = bannerOfMe(me);
    if (remembered && remembered.text === next.text && remembered.color === next.color && remembered.placement === next.placement) {
        return;
    }

    remembered = next;
    try {
        window.localStorage.setItem(BANNER_KEY, JSON.stringify(next));
    } catch {
        // Not remembered: the next reload draws none until "me" answers.
    }
}

export function bannerOf(me: Me | null): {text: string; atBottom: boolean; style: React.CSSProperties} {
    const banner = me ? bannerOfMe(me) : (remembered || NO_BANNER);

    // With no colour set, the banner takes the theme's accent and the words
    // on it (the stylesheet's fallbacks).
    const color = banner.color;

    return {
        text: banner.text,
        atBottom: banner.text !== '' && banner.placement !== 'top',
        style: (color ? {
            ['--vntv-banner-bg' as string]: color,
            ['--vntv-banner-fg' as string]: contrastingSimpleColor(color),
        } : {}) as React.CSSProperties,
    };
}
