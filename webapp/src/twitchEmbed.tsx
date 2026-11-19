import React from 'react';

import {embedHostFor, frameURL, TWITCH_PLAYER_PREFIX} from './twitchLogic';
import type {EmbedHost} from './twitchLogic';

// The one module through which the panel touches anything outside Mattermost
// (design/twitch-live.md 6.2, design/3.4.0.md 5.3 and 7). The four ways it
// sends a browser there, which README and LOCKDOWN count as the four cases,
// each off until an admin switches it on: Twitch's player in a frame; and the
// Connect window of Connect Twitch, Connect Steam and Connect Hytale, each
// opened at a plugin address on this server, which spends the ticket and
// sends the window on to that service's own sign-in page. Beside them, a link
// the viewer chose (Watch on Twitch, a Steam profile) opens only when it is
// pressed. Everything else the panel shows comes from this server. The lab
// swaps this module wholesale (lab/mocks/twitchEmbed.tsx), so a lab that runs
// from a file never frames Twitch or opens a window.

// What the player's frame sends Twitch as its referrer. Mattermost's page
// sends none at all; strict-origin sends only https://<host>/, which the
// parent parameter already tells Twitch, and never the team or the channel.
// Whether Twitch's player works with no referrer is checked on a real server
// (13.2 step 4); if it does, this one constant becomes 'no-referrer'.
export const TWITCH_REFERRER_POLICY = 'strict-origin';

// Twitch's own list for its frame, as its script builds it, without top
// navigation: the player may play sound, go fullscreen, show dialogs and open
// windows of its own, and can neither navigate nor read the Mattermost page.
const SANDBOX = 'allow-modals allow-scripts allow-same-origin allow-popups allow-popups-to-escape-sandbox';

// Whether this page may run Twitch's player, judged from the page actually
// loaded, which is the ancestor Twitch checks.
export function embedHost(): EmbedHost {
    return embedHostFor(window.location);
}

export type TwitchFrameProps = {
    login: string;
    host: string;
    muted: boolean;
    width: number;
    height: number;

    // `${handle}|${muted ? 'm' : 's'}|${reloads}`: a new key is a new frame,
    // which is how the sound moves and how Reload reloads.
    reloadKey: string;

    // `Twitch player: ${stream.title}`.
    title: string;
};

// Twitch's player: a frame of player.twitch.tv, and only ever one made by
// frameURL, whose address starts with the player's own prefix. There is no
// srcdoc here and never may be: a srcdoc frame is the page's own origin,
// and with allow-scripts and allow-same-origin it could reach the page and
// take its own sandbox off. Nothing is ever drawn over it (Twitch's rule that
// the player is not obscured); its controls live in the bar above it.
export const TwitchFrame: React.FC<TwitchFrameProps> = ({login, host, muted, width, height, reloadKey, title}) => {
    const url = frameURL(login, host, muted);
    if (url === null || !url.startsWith(TWITCH_PLAYER_PREFIX)) {
        return null;
    }

    return (
        <iframe
            key={reloadKey}
            className='vntv-twitch-frame'
            src={url}
            width={width}
            height={height}
            allow='autoplay; fullscreen'
            allowFullScreen={true}
            sandbox={SANDBOX}
            referrerPolicy={TWITCH_REFERRER_POLICY}
            scrolling='no'
            frameBorder={0}
            title={title}
        />
    );
};

// The Continue on Twitch, Continue on Steam and Continue on Hytale window
// (twitch-live.md 4.9, 3.4.0.md 5.3). With noopener, so the service's
// sign-in pages hold no reference to this tab. Its value is not used: with noopener
// it is null in every browser, and in the desktop app it is null anyway,
// where the app opens its own window for a plugin address and refuses this
// one's. So the panel cannot hold or close the window; the callback page
// closes itself where the browser lets it.
export function openConnectWindow(url: string): void {
    window.open(url, '_blank', 'popup=yes,width=520,height=760,noopener');
}

// A link the viewer chose, such as a channel on twitch.tv: a new tab, with
// nothing of this page handed to it. In the desktop app, the system browser.
export function openOutside(url: string): void {
    if (url === '') {
        return;
    }

    window.open(url, '_blank', 'noopener,noreferrer');
}
