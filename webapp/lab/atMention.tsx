import React from 'react';
import {createPortal} from 'react-dom';

import {Avatar} from '../src/components/Avatar';
import {PopoverLive} from '../src/components/PopoverLive';
import {PopoverTakebacks} from '../src/components/PopoverTakebacks';

import {THEMES} from './sharedClips';
import type {CentreTheme} from './sharedClips';
import {LAB_USERS} from './world';

// @names in titles (design/3.2.0.md 8.7): a stand-in for what the lab does
// not have, Mattermost's window.PostUtils.messageHtmlToComponent and the
// AtMention it draws for a <span data-mention>, with a stand-in for the
// profile popover it opens.
//
// Drawn the way AtMention draws (components/at_mention/at_mention.tsx,
// v11.11.1): a person Mattermost knows is a <button class="style--none">
// holding <span class="mention-link">@Name</span>, the name as the Teammate
// Name Display setting has it; anybody else is the text as typed, plain.
// While somebody is being fetched they are "anybody else" too, which is what
// the @names row's "held" and "after 500 ms" show: the ticker is seen to
// measure again when the name resolves.
//
// The popover is portalled onto the page, as Mattermost's is, so a press in
// it still bubbles through React's tree to whatever the mention sits in,
// which is what the panel's wrapper has to stop.

export type MentionMode = 'resolved' | 'slow' | 'held';
export type NameDisplay = 'full' | 'username';

const settings = {mode: 'resolved' as MentionMode, names: 'full' as NameDisplay, theme: 'onyx' as CentreTheme};
const loaded = new Set<string>();
const listeners = new Set<() => void>();

function notify(): void {
    listeners.forEach((listener) => listener());
}

export function mentionSettings(): typeof settings {
    return settings;
}

export function setMentionSettings(next: Partial<typeof settings>): void {
    Object.assign(settings, next);

    // A new mode starts Mattermost's store over: nobody loaded yet.
    if (next.mode) {
        loaded.clear();
    }
    notify();
}

function useMentionSettings(): typeof settings {
    const [, bump] = React.useState(0);
    React.useEffect(() => {
        const listener = () => bump((value) => value + 1);
        listeners.add(listener);

        return () => {
            listeners.delete(listener);
        };
    }, []);

    return settings;
}

// Mattermost's displayUsername: the full name when the setting asks for it
// and there is one, else the username.
export function displayNameOf(user: {username?: string; first_name?: string; last_name?: string}, names: NameDisplay): string {
    const full = [user.first_name, user.last_name].filter(Boolean).join(' ');

    return names === 'full' && full ? full : user.username || '';
}

const LabAtMention: React.FC<{mentionName: string; children: React.ReactNode}> = ({mentionName, children}) => {
    const now = useMentionSettings();
    const user = LAB_USERS.find((candidate) => candidate.username === mentionName);
    const buttonRef = React.useRef<HTMLButtonElement | null>(null);
    const [open, setOpen] = React.useState(false);

    // Mattermost's batched loader, slowly: known 500 ms after it was first
    // asked for.
    React.useEffect(() => {
        if (!user || now.mode !== 'slow' || loaded.has(mentionName)) {
            return undefined;
        }

        const timer = window.setTimeout(() => {
            loaded.add(mentionName);
            notify();
        }, 500);

        return () => window.clearTimeout(timer);
    }, [mentionName, now.mode]);

    const known = Boolean(user) && (now.mode === 'resolved' || (now.mode === 'slow' && loaded.has(mentionName)));
    if (!user || !known) {
        return <>{children}</>;
    }

    return (
        <>
            <button
                ref={buttonRef}
                type='button'
                className='style--none'
                aria-haspopup='dialog'
                aria-expanded={open}
                onClick={() => setOpen(!open)}
            ><span className='mention-link'>{`@${displayNameOf(user, now.names)}`}</span></button>
            {open && buttonRef.current ? (
                <LabFloatingProfile
                    userId={user.id}
                    anchor={buttonRef.current}
                    onClose={() => {
                        setOpen(false);
                        buttonRef.current?.focus();
                    }}
                />
            ) : null}
        </>
    );
};

// The profile popover, floating by the mention that opened it: above
// everything (Mattermost's is z-index 1200), closed by a press outside it or
// Escape, which hands focus back to the mention.
const LabFloatingProfile: React.FC<{userId: string; anchor: HTMLElement; onClose: () => void}> = ({userId, anchor, onClose}) => {
    const cardRef = React.useRef<HTMLDivElement | null>(null);
    const rect = anchor.getBoundingClientRect();
    const width = 240;
    const left = Math.max(8, Math.min(rect.left - 24, window.innerWidth - width - 8));
    const below = rect.bottom + 380 < window.innerHeight;
    const style: React.CSSProperties = below ?
        {position: 'fixed', top: rect.bottom + 6, left, width, zIndex: 1200} :
        {position: 'fixed', bottom: window.innerHeight - rect.top + 6, left, width, zIndex: 1200};

    React.useEffect(() => {
        const onDown = (event: MouseEvent) => {
            if (cardRef.current && !cardRef.current.contains(event.target as Node) && !anchor.contains(event.target as Node)) {
                onClose();
            }
        };
        const onKey = (event: KeyboardEvent) => {
            if (event.key === 'Escape') {
                onClose();
            }
        };
        document.addEventListener('mousedown', onDown, true);
        document.addEventListener('keydown', onKey, true);

        return () => {
            document.removeEventListener('mousedown', onDown, true);
            document.removeEventListener('keydown', onKey, true);
        };
    }, [anchor, onClose]);

    return createPortal(
        <div
            ref={cardRef}
            className='lab-mm-float'
            style={style}
        >
            <LabProfileCard userId={userId}/>
        </div>,
        document.body,
    );
};

// Mattermost's profile popover, near enough: the face, the name and the
// username, the plugins' attributes slot (the plugin's PopoverLive), Message
// and Call, and the plugins' actions slot (the plugin's takebacks, which draw
// only in your own, design/3.2.0.md 4.6). In the theme the lab's channel is
// in.
export const LabProfileCard: React.FC<{userId: string; theme?: CentreTheme}> = ({userId, theme}) => {
    const now = useMentionSettings();
    const user = LAB_USERS.find((candidate) => candidate.id === userId);
    const shownTheme = theme || now.theme;
    const vars = THEMES[shownTheme];
    const name = user ? displayNameOf(user, 'full') : userId;

    return (
        <div
            className='lab-mm-popover'
            data-theme={shownTheme}
            style={{...vars, background: vars['--center-channel-bg'], color: vars['--center-channel-color']} as React.CSSProperties}
        >
            <div className='lab-mm-popover-head'>
                <span className='vntv vntv-inline'>
                    <Avatar
                        className='lab-mm-popover-face'
                        userId={userId}
                        name={name}
                        decorative={true}
                    />
                </span>
                <div className='lab-mm-popover-who'>
                    <div className='lab-mm-popover-name'>{name}</div>
                    <div className='lab-mm-popover-user'>{`@${user?.username || ''}`}</div>
                </div>
            </div>
            <div className='lab-mm-popover-attrs'>
                <PopoverLive user={{id: userId}}/>
            </div>
            <div className='lab-mm-popover-buttons'>
                <span className='lab-mm-popover-button'>{'Message'}</span>
                <span className='lab-mm-popover-button lab-mm-popover-button-quiet'>{'Call'}</span>
            </div>
            <PopoverTakebacks user={{id: userId}}/>
        </div>
    );
};

// window.PostUtils.messageHtmlToComponent, for exactly the HTML the panel
// hands it: one <span data-mention="name">@typed</span>. Anything else comes
// back as its text, unparsed, which is what the panel must never rely on.
const ONE_MENTION = /^<span data-mention="([a-z0-9._-]+)">@([A-Za-z0-9._-]+)<\/span>$/;

export function installLabPostUtils(): void {
    (window as unknown as {PostUtils?: unknown}).PostUtils = {
        messageHtmlToComponent: (html: string) => {
            const match = ONE_MENTION.exec(html);
            if (!match) {
                return html;
            }

            return (
                <LabAtMention mentionName={match[1]}>{`@${match[2]}`}</LabAtMention>
            );
        },
    };
}

export function removeLabPostUtils(): void {
    delete (window as unknown as {PostUtils?: unknown}).PostUtils;
}
