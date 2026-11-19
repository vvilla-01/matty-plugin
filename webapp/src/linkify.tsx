import React from 'react';

import {openHashtag} from './hashtagOpen';
import {hashtagMinimum} from './mattermost';
import * as titleParts from './titleParts';
import type {LinkPart} from './titleParts';

// Links, hashtags and @names in a stream title, made pressable. What counts as
// each is titleParts.ts's to say; this draws them.
//
// A hashtag is not a link to anywhere. It is a button that searches
// Mattermost's messages for the same hashtag, the way a hashtag in a post
// does, with the results over the panel; see HashtagSearch.tsx. An @name is
// Mattermost's own mention (design/3.2.0.md 8.3), which opens Mattermost's
// profile popover for that person, the panel's live row and all.
//
// Links open in a new tab, with no referrer and no opener, and a press on any
// of them is stopped from reaching whatever the title sits in: a card that
// opens a stream, a ticker that pauses when pressed. So is Enter or Space on
// one, and nothing else: Mattermost's own shortcuts, listened for on the whole
// page, must still work while one has focus.
//
// It keeps nothing at module level: no cache of people, and no state (the
// open hashtag is hashtagOpen.ts's), which check-away.mjs holds it to.

export type {LinkPart} from './titleParts';
export {findHashtags, isHashtagTerm} from './titleParts';

// The title in parts, with Mattermost's own MinimumHashtagLength. mentions is
// false for a Twitch card: its titles use @ for Twitch handles.
export function linkParts(text: string, mentions = true): LinkPart[] {
    return titleParts.linkParts(text, hashtagMinimum(), mentions);
}

// Whether anything in the title can be pressed: a link, a hashtag or an @name.
export function hasLinks(text: string, mentions = true): boolean {
    return titleParts.hasLinks(text, hashtagMinimum(), mentions);
}

// Enter or Space on a link, a hashtag or a name is its own; anything with a
// modifier is somebody's shortcut and goes on up.
const keepKey = (event: React.KeyboardEvent) => {
    if ((event.key === 'Enter' || event.key === ' ') && !event.ctrlKey && !event.metaKey && !event.altKey) {
        event.stopPropagation();
    }
};

// What the live copy of a ticker's @names say once Mattermost has drawn them,
// in order, and whether Mattermost found each person (and so drew it as a
// chip), for its inert second copy to say the same at the same width
// (Marquee, Hud.tsx).
export type MentionMirror = Array<{text: string; resolved: boolean}>;

// One part of a title as React nodes: plain text as text, an address as a
// link, a hashtag as a button that searches for it, an @name as Mattermost's
// own mention. inert is for a copy that is only there to be looked at — the
// second pass of a scrolling ticker — whose links and buttons must not be tab
// stops; shown is what the live copy's mention says, for that copy.
export function renderPart(part: LinkPart, key: React.Key, inert = false, shown?: MentionMirror[number]): React.ReactNode {
    if (part.mention) {
        return (
            <TitleMention
                key={key}
                name={part.mention}
                typed={part.text.slice(1)}
                inert={inert}
                shown={shown}
            />
        );
    }

    if (part.href) {
        return (
            <a
                key={key}
                className='vntv-link'
                href={part.href}
                target='_blank'
                rel='noopener noreferrer nofollow'
                tabIndex={inert ? -1 : undefined}
                title={part.href}
                onClick={(event) => event.stopPropagation()}
                onKeyDown={keepKey}
            >{part.text}</a>
        );
    }

    if (part.hashtag) {
        const tag = part.hashtag;
        const label = `Search messages for #${tag}`;

        return (
            <button
                key={key}
                type='button'
                className='vntv-link vntv-hashtag'
                tabIndex={inert ? -1 : undefined}
                aria-haspopup='dialog'
                aria-label={label}
                title={label}
                onClick={(event) => {
                    event.stopPropagation();
                    openHashtag(tag);
                }}
                onKeyDown={keepKey}
            >{part.text}</button>
        );
    }

    return <React.Fragment key={key}>{part.text}</React.Fragment>;
}

// A whole title. mirror is the live copy's @names as drawn, for an inert copy.
export function renderLinked(text: string, inert = false, mentions = true, mirror?: MentionMirror): React.ReactNode[] {
    let mention = 0;

    return linkParts(text, mentions).map((part, index) => {
        if (part.mention) {
            mention += 1;

            return renderPart(part, index, inert, mirror?.[mention - 1]);
        }

        return renderPart(part, index, inert);
    });
}

// ---- @names (design/3.2.0.md 8.3) ----

type PostUtils = {
    messageHtmlToComponent?: (html: string, options: Record<string, boolean>) => React.ReactNode;
};

// Exactly the options the contract names: Mattermost's AtMention for the one
// mention, looked up (and fetched if missing) in Mattermost's own store, never
// highlighted as "you", never a group, and nothing else of Markdown.
const MENTION_OPTIONS = {
    mentions: true,
    mentionHighlight: false,
    disableGroupHighlight: true,
    fetchMissingUsers: true,
    emoji: false,
    images: false,
    latex: false,
    inlinelatex: false,
    markdown: false,
};

// Mattermost's window.PostUtils, exported "for plugins to use" (M3), and
// feature-detected: it is public but untyped.
function postUtils(): PostUtils | null {
    const utils = (window as unknown as {PostUtils?: PostUtils}).PostUtils;

    return utils && typeof utils.messageHtmlToComponent === 'function' ? utils : null;
}

// One @name. Mattermost's own AtMention draws it: "@" and the person's name as
// the server's Teammate Name Display says, a button opening Mattermost's
// profile popover, or the text as typed for a name nobody has (and, for a
// moment, for one being looked up). The panel keeps no cache of people for it.
// A name Mattermost knows is drawn as a quiet chip (Victor's P7 pick, the
// stylesheet's .vntv-mention); one it does not stays plain words.
//
// The wrapper stops a press, and Enter or Space, from reaching the card or the
// ticker it sits in: the popover is portalled, but its events still bubble
// through React's tree.
//
// Inert, or with no PostUtils (a future Mattermost, the lab without its
// stand-in): plain text, not pressable, saying what the live copy says when
// there is one, and "@" and the typed name until then.
const TitleMention: React.FC<{name: string; typed: string; inert: boolean; shown?: MentionMirror[number]}> = ({name, typed, inert, shown}) => {
    const utils = inert ? null : postUtils();
    const drawn = React.useMemo(() => (utils ? utils.messageHtmlToComponent?.(titleParts.mentionHTML({name, typed}), MENTION_OPTIONS) : null), [utils, name, typed]);

    if (!utils) {
        return (
            <span
                className='vntv-mention'
                data-inert={inert ? 'true' : undefined}
                data-resolved={shown?.resolved ? 'true' : undefined}
            >{shown?.text || `@${typed}`}</span>
        );
    }

    return (
        <span
            className='vntv-mention'
            onClick={(event) => event.stopPropagation()}
            onKeyDown={keepKey}
        >{drawn}</span>
    );
};
