import React from 'react';

import {COPIED, COPY, steamProfileName} from '../accountsLogic';
import type {PopoverRow} from '../accountsLogic';

import {CubeIcon, GamepadIcon, OutArrowIcon} from './icons';

// Somebody's Steam status and Hytale username in their profile popover
// (design/3.4.0.md 5.4, with Victor's Q8 pick): two more of the popover's
// rows, built as its other rows are, one fact about a person each: the mark
// on the left, what it says beside it, and at most one thing to press on the
// right. PopoverLive draws them after On Mumble and before the awards, from
// popoverRowsFor, which gives none to a reader who may not watch.
//
// Drawn in Mattermost's own popover on whatever theme the reader has, so
// the words take the theme's colour. Only the two pills pick theirs, for
// contrast: see the stylesheet's 3.4.0 popover section.

// Steam: in a game, the game is the title and the meta "On Steam · {name}";
// online, busy or away, that word; offline, private or not read lately, the
// name, faded. The pill beside it is an icon, not the words PROFILE ↗: the
// words took 65 px of a 157 px column and cut ELDEN RING short.
export const SteamRow: React.FC<{row: PopoverRow}> = ({row}) => {
    const url = row.url;

    return (
        <div
            className='vntv-factrow'
            data-dim={row.dim ? 'true' : undefined}
            title={row.full}
        >
            <span
                className='vntv-factrow-mark'
                aria-hidden={true}
            ><GamepadIcon size={20}/></span>
            <span className='vntv-factrow-text'>
                <span className='vntv-factrow-title'>{row.title}</span>
                <span className='vntv-factrow-meta'>{row.meta}</span>
            </span>

            {/* An ordinary link out, as Watch on Twitch ↗ is: their profile
                is how friends add each other on Steam. Never nofollow
                (Steam's terms, section 3). */}
            {url === '' ? null : (
                <a
                    className='vntv-pill vntv-pill-icon vntv-factrow-action vntv-account-pill'
                    href={url}
                    target='_blank'
                    rel='noopener noreferrer'
                    aria-label={steamProfileName(row.name)}
                    title={steamProfileName(row.name)}
                ><OutArrowIcon size={11}/></a>
            )}
        </div>
    );
};

// Hytale: the username, said to be one, and Copy, since it is typed into a
// game letter for letter. Hytale tells the plugin nothing else.
export const HytaleRow: React.FC<{row: PopoverRow}> = ({row}) => {
    const nameRef = React.useRef<HTMLSpanElement | null>(null);

    return (
        <div
            className='vntv-factrow'
            title={row.full}
        >
            <span
                className='vntv-factrow-mark'
                aria-hidden={true}
            ><CubeIcon size={19}/></span>
            <span className='vntv-factrow-text'>
                <span
                    ref={nameRef}
                    className='vntv-factrow-title vntv-factrow-copyable'
                >{row.title}</span>
                <span className='vntv-factrow-meta'>{row.meta}</span>
            </span>
            <CopyPill
                text={row.name}
                target={nameRef}
            />
        </div>
    );
};

const COPIED_MS = 2000;

// Copy, then Copied for two seconds in its place. Where the clipboard is
// refused, the name is selected instead, for the person to copy themselves.
// Nothing leaves the browser.
//
// Both words are always in the pill, one of them hidden, in one grid cell:
// the pill is as wide as Copied from the start, so it never moves and the
// name beside it never narrows while Copied shows.
const CopyPill: React.FC<{text: string; target: React.RefObject<HTMLElement>}> = ({text, target}) => {
    const [copied, setCopied] = React.useState(false);
    const timer = React.useRef(0);

    React.useEffect(() => () => window.clearTimeout(timer.current), []);

    const selectIt = () => {
        const element = target.current;
        const selection = window.getSelection();
        if (element && selection) {
            selection.selectAllChildren(element);
        }
    };

    const copy = () => {
        if (!navigator.clipboard?.writeText) {
            selectIt();

            return;
        }

        navigator.clipboard.writeText(text).then(() => {
            setCopied(true);
            window.clearTimeout(timer.current);
            timer.current = window.setTimeout(() => setCopied(false), COPIED_MS);
        }, selectIt);
    };

    return (
        <button
            type='button'
            className='vntv-pill vntv-factrow-action vntv-account-pill vntv-copy-pill'
            data-copied={copied ? 'true' : undefined}
            aria-label={copied ? `${COPIED}: ${text}` : `${COPY} ${text}`}
            title={`${COPY} ${text}`}
            onClick={copy}
        >
            <span
                className='vntv-copy-faces'
                aria-hidden={true}
            >
                <span
                    className='vntv-copy-face'
                    data-shown={copied ? undefined : 'true'}
                >{COPY}</span>
                <span
                    className='vntv-copy-face'
                    data-shown={copied ? 'true' : undefined}
                >{COPIED}</span>
            </span>
        </button>
    );
};
