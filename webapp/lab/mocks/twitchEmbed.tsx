import React from 'react';

import {meHeard} from '../../src/me';
import {EMBED_PORT_443, frameURL, TWITCH_PLAYER_PREFIX} from '../../src/twitchLogic';
import type {EmbedHost} from '../../src/twitchLogic';
import type {TwitchFrameProps} from '../../src/twitchEmbed';
import {labSay} from '../mattermost';
import {change, getWorld, ME_ID, victorHytaleLink, victorSteamLink, victorTwitchLink} from '../world';

// src/twitchEmbed.tsx, for the lab (design/twitch-live.md 11.3): the one
// module through which the panel touches anything outside Mattermost, swapped
// so the lab — a file with no server, which Twitch would refuse anyway —
// never frames Twitch, never opens a window and never leaves the page.

export const TWITCH_REFERRER_POLICY = 'strict-origin';

// A page Twitch's player can run on, unless the lab's "Player blocked here"
// says this is a Mattermost on a custom port.
export function embedHost(): EmbedHost {
    return getWorld().twitchPlayerBlocked ? {ok: false, reason: EMBED_PORT_443} : {ok: true, host: 'lab.localhost'};
}

// How many times each stream's player has loaded, so a reload — the sound
// moving, Reload, the theatre — can be counted on screen.
const loads = new Map<string, number>();

// The stand-in for Twitch's player: its own element, never a srcdoc frame,
// with the channel, whether it loaded muted, which load this is and its size,
// and a slow bar, so reloads and sizes can be seen. Built only when the real
// one would be: from an address frameURL made, with the player's prefix.
export const TwitchFrame: React.FC<TwitchFrameProps> = ({login, host, muted, width, height, reloadKey, title}) => {
    const url = frameURL(login, host, muted);
    if (url === null || !url.startsWith(TWITCH_PLAYER_PREFIX)) {
        return null;
    }

    return (
        <StandIn
            key={reloadKey}
            stream={reloadKey.replace(/\|[ms]\|\d+$/, '')}
            login={login}
            muted={muted}
            width={width}
            height={height}
            url={url}
            title={title}
        />
    );
};

type StandInProps = {stream: string; login: string; muted: boolean; width: number; height: number; url: string; title: string};

const StandIn: React.FC<StandInProps> = ({stream, login, muted, width, height, url, title}) => {
    const [load] = React.useState(() => {
        const next = (loads.get(stream) || 0) + 1;
        loads.set(stream, next);

        return next;
    });

    return (
        <div
            className='vntv-lab-twitch-standin'
            style={{width, height}}
            role='img'
            aria-label={title}
            title={url}
        >
            <span className='vntv-lab-twitch-standin-head'>{'Twitch player (stand-in)'}</span>
            <span className='vntv-lab-twitch-standin-line'>{`twitch.tv/${login}`}</span>
            <span
                className='vntv-lab-twitch-standin-sound'
                data-sound={muted ? undefined : 'true'}
            >{muted ? 'muted' : 'SOUND'}</span>
            <span className='vntv-lab-twitch-standin-line'>{`load ${load} · ${width} × ${height}`}</span>
            <span className='vntv-lab-twitch-standin-bar'/>
        </div>
    );
};

// Continue on Twitch, simulated (11.3): a pop-up blocker stops it and nothing
// happens at all; otherwise a window "opens" — a browser's, or the desktop
// app's own — takes focus from the page, and a moment later Twitch either
// connects Victor (and the server sends "me"), refuses, or never
// answers, by the lab's Twitch row. Nothing is ever really opened.
export function openConnectWindow(url: string): void {
    const world = getWorld();
    const account = (/\/api\/v1\/(steam|hytale)\/connect\?/).exec(url);
    if (account) {
        openAccountWindow(url, account[1] === 'steam' ? 'steam' : 'hytale');

        return;
    }
    if (world.twitchPopup === 'blocked') {
        labSay('A pop-up blocker stopped the Connect window: nothing opened, and window.open said nothing about it.');

        return;
    }

    labSay(world.twitchPopup === 'desktop' ?
        `The desktop app opened its own window for ${url}, and window.open returned null.` :
        `A window opened for ${url} (with noopener, so window.open returned null).`);
    window.setTimeout(() => window.dispatchEvent(new Event('blur')), 250);

    if (world.twitchWindow === 'never') {
        return;
    }

    window.setTimeout(() => {
        if (getWorld().twitchWindow === 'connects') {
            change((current) => {
                current.twitchLinks = {...current.twitchLinks, [ME_ID]: victorTwitchLink()};
                current.twitchLost = null;
            });
            meHeard();
            labSay('Twitch connected Victor as VictorPlays, and the server sent "me".');
        } else {
            labSay('Twitch did not confirm the account: its window says so, and nothing was connected.');
        }
        window.dispatchEvent(new Event('focus'));
    }, 1500);
}

// Continue on Steam or Continue on Hytale, simulated as Twitch's is
// (design/3.4.0.md 8.3), by the lab's Connect accounts row: stopped by a
// pop-up blocker, or a window that "opens" (a browser's, or the desktop
// app's own) and takes focus from the page, and a moment later the service
// connects Victor (the server sends "me") or never answers.
function openAccountWindow(url: string, service: 'steam' | 'hytale'): void {
    const world = getWorld();
    const word = service === 'steam' ? 'Steam' : 'Hytale';
    if (world.accountPopup === 'blocked') {
        labSay(`A pop-up blocker stopped the Connect ${word} window: nothing opened, and window.open said nothing about it.`);

        return;
    }

    labSay(world.accountPopup === 'desktop' ?
        `The desktop app opened its own window for ${url}, and window.open returned null.` :
        `A window opened for ${url} (with noopener, so window.open returned null), on its way to ${word}’s own sign-in page.`);
    window.setTimeout(() => window.dispatchEvent(new Event('blur')), 250);
    if (world.accountWindow === 'never') {
        return;
    }

    window.setTimeout(() => {
        change((current) => {
            if (service === 'steam') {
                current.steamLinks = {...current.steamLinks, [ME_ID]: victorSteamLink()};
                current.steamStatus = {...current.steamStatus, [ME_ID]: current.steamStatus[ME_ID] || {state: 'online', public: true}};
                current.steamLost = null;
            } else {
                current.hytaleLinks = {...current.hytaleLinks, [ME_ID]: victorHytaleLink()};
                current.hytaleLost = null;
            }
        });
        meHeard();
        labSay(`${word} signed Victor in, and the server sent "me".`);
        window.dispatchEvent(new Event('focus'));
    }, 1500);
}

// A link the viewer chose: a toast saying where it would go, instead of
// going.
export function openOutside(url: string): void {
    if (url === '') {
        return;
    }

    labSay(`Would open ${url}`);
    const toast = document.createElement('div');
    toast.className = 'lab-toast';
    toast.setAttribute('role', 'status');
    toast.textContent = `Would open ${url}`;
    document.body.appendChild(toast);
    window.setTimeout(() => toast.remove(), 3500);
}
