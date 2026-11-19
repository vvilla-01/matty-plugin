// Links, hashtags and @names in a stream title, found (design/3.2.0.md 8.2).
// linkify.tsx draws them; this finds them, and imports nothing, so
// build/check-mentions.mjs can load it on its own, as check-twitch.mjs loads
// twitchLogic.ts.
//
// A streamer's title is whatever they typed, so this is careful about what it
// turns into a link: only http and https addresses, written out with their
// scheme or starting "www.". Nothing else becomes an href — not javascript:,
// not data:, not a bare word that happens to contain a dot — because the text
// comes from somebody else and is shown to everybody.
//
// It holds no module-level Map or Set, so nothing it finds outlives the call:
// @names in particular are looked up in Mattermost's own store, never in one
// of the panel's (2.6), and check-away.mjs holds this file to that.

// Up to the next space; trailing punctuation is given back to the sentence.
const URL_PATTERN = /\b(?:https?:\/\/|www\.)[^\s<>"']+/gi;
const TRAILING = /[.,;:!?)\]}'"]+$/;

// Hashtags, by the rule the SERVER indexes them by, since a hashtag only
// finds what the server filed under it (model/utils.go, ParseHashtags): the
// message split into words at whitespace; punctuation trimmed off both ends
// of each word, except that a # is never trimmed off the front; a run of #s
// counted as one; and what is left a hashtag if it is a # and then a letter,
// then letters, digits, - _ or ., ending in a letter or a digit. So "(#arma3)"
// and "#arma3," are #arma3, "#arma3's" and "x#arma3" are nothing, and "#3d"
// is nothing either.
//
// The pieces match Go's exactly. Whitespace is what Go's strings.Fields splits
// on (unicode.IsSpace), which is not quite JavaScript's \s. \p{L} is Go's \pL,
// and \d is ASCII 0-9 in both, so a hashtag cannot start with a mark or end
// in an Arabic-Indic digit here any more than there.
//
// Mattermost's own hashtag links follow the same rule, except that it leaves
// out any shorter than MinimumHashtagLength, 3 unless an admin changes it, and
// so does this; the length is counted the way Mattermost counts it, in
// UTF-16 units of what follows the #.
const SPACE = '\\t\\n\\v\\f\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000';
const HASHTAG = new RegExp(
    `(^|[${SPACE}])` + // the start of a word
    `([^\\p{L}\\d${SPACE}#]*)` + // punctuation in front of it
    '(#+)' +
    '(\\p{L}[\\p{L}\\d_.-]*[\\p{L}\\d])' + // the hashtag itself
    `(?=[^\\p{L}\\d${SPACE}]*(?:[${SPACE}]|$))`, // only punctuation after it
    'gu',
);

// @names, by Mattermost's own mention rule, so a title reads the way a post
// does: MENTIONS_REGEX in webapp/channels/src/utils/constants.tsx (v11.11.1).
// The @ has to start a word, or follow underscores, so "user@host" and
// "mailto:a@b.c" are not mentions. Then, as AtMention and the server have it:
//   - a name with a ":remote" part stays text (a shared channel's name means
//     nothing here);
//   - trailing ".", "-" and "_" go back to the sentence, so "@dave." is
//     @dave;
//   - @all, @channel and @here, in any case, stay text: never a person, and
//     never a broadcast;
//   - what is left must be a username Mattermost could have, 1 to 64 of
//     a-z, 0-9, ".", "_" and "-", or it stays text.
// Nothing found here notifies anybody: a title is not a post (8.5).
const MENTION = /(?:\B|\b_+)@([a-z0-9.\-_]+(?::[a-z0-9.\-_]+)?)/gi;
const MENTION_TRAILING = /[.\-_]+$/;

// A regex rather than a Set, so this file keeps no module-level Set.
const SPECIAL = /^(?:all|channel|here)$/;
const USERNAME = /^[a-z0-9._-]{1,64}$/;

export type Hashtag = {
    // Where the button goes in the text: the last # of a run, to the end of
    // the hashtag.
    start: number;
    end: number;

    // Without the #, as it was written.
    tag: string;
};

export function findHashtags(text: string, minimum = 1): Hashtag[] {
    const found: Hashtag[] = [];

    for (const match of text.matchAll(HASHTAG)) {
        const tag = match[4];
        if (tag.length < minimum) {
            continue;
        }

        const start = (match.index ?? 0) + match[1].length + match[2].length + match[3].length - 1;
        found.push({start, end: start + 1 + tag.length, tag});
    }

    return found;
}

// Whether these words are one hashtag and nothing else, as a search for it is
// written: "#arma3".
export function isHashtagTerm(term: string): boolean {
    const found = findHashtags(term);

    return found.length === 1 && found[0].start === 0 && found[0].end === term.length;
}

export type Mention = {
    // Where it is in the text: the @, to the end of the name as kept.
    start: number;
    end: number;

    // Lower-cased, for looking the person up.
    name: string;

    // As written, case kept, without the @ and what was given back.
    typed: string;
};

export function findMentions(text: string): Mention[] {
    const found: Mention[] = [];

    for (const match of text.matchAll(MENTION)) {
        const raw = match[1];
        if (raw.includes(':')) {
            continue;
        }

        const typed = raw.replace(MENTION_TRAILING, '');
        const name = typed.toLowerCase();
        if (typed === '' || SPECIAL.test(name) || !USERNAME.test(name)) {
            continue;
        }

        const start = (match.index ?? 0) + match[0].length - raw.length - 1;
        found.push({start, end: start + 1 + typed.length, name, typed});
    }

    return found;
}

// The only HTML ever handed to Mattermost's messageHtmlToComponent: one
// mention, and nothing of the title around it. name and typed are made only of
// [A-Za-z0-9._-] by findMentions, so nothing in them needs escaping. A title is
// never given to Mattermost whole: titles come from Broadcast Box, Twitch and
// admin notes, not only the panel.
export function mentionHTML(mention: {name: string; typed: string}): string {
    return `<span data-mention="${mention.name}">@${mention.typed}</span>`;
}

// One piece of a title: plain text, an address, a hashtag (without its #), or
// an @name (lower-cased, without its @; the text keeps it as written).
export type LinkPart = {text: string; href?: string; hashtag?: string; mention?: string};

type Span = {start: number; end: number; href?: string; hashtag?: string; mention?: string};

// An address as an href, or nothing if it is not one this will link to.
function safeHref(found: string): string {
    const candidate = /^www\./i.test(found) ? `https://${found}` : found;

    try {
        const url = new URL(candidate);
        if ((url.protocol !== 'http:' && url.protocol !== 'https:') || url.hostname === '') {
            return '';
        }

        return url.href;
    } catch {
        return '';
    }
}

function linkSpans(text: string): Span[] {
    const spans: Span[] = [];

    for (const match of text.matchAll(URL_PATTERN)) {
        const start = match.index ?? 0;
        let found = match[0];

        // "(see https://example.com)." — the bracket and the full stop belong
        // to the sentence, not the address. A closing bracket is kept when
        // the address opened one of its own, as Wikipedia's do.
        const trailing = found.match(TRAILING)?.[0] ?? '';
        if (trailing) {
            found = found.slice(0, found.length - trailing.length);

            const opens = (found.match(/\(/g) || []).length;
            const closes = (found.match(/\)/g) || []).length;
            if (trailing.startsWith(')') && opens > closes) {
                found += ')';
            }
        }

        const href = safeHref(found);
        if (href) {
            spans.push({start, end: start + found.length, href});
        }
    }

    return spans;
}

// The title cut into plain text, links, hashtags and @names, in order.
// Hashtags and names are found in the whole title and then any inside an
// address dropped: the address wins, as it does in a post, so
// "example.com/#top" and "twitch.tv/@dave" are one link each. A # and an @
// each start a span of their own, so a hashtag and a name never overlap.
//
// minimum is Mattermost's MinimumHashtagLength (linkify.tsx passes it).
// mentions is false for a Twitch card, whose titles use @ for Twitch handles:
// a Mattermost account with the same name would be the wrong person.
export function linkParts(text: string, minimum: number, mentions = true): LinkPart[] {
    const links = linkSpans(text);
    const outsideLinks = (span: {start: number; end: number}) => !links.some((link) => span.start < link.end && link.start < span.end);
    const tags = findHashtags(text, minimum).
        filter(outsideLinks).
        map((tag): Span => ({start: tag.start, end: tag.end, hashtag: tag.tag}));
    const names = mentions ? findMentions(text).
        filter(outsideLinks).
        filter((name) => !tags.some((tag) => name.start < tag.end && tag.start < name.end)).
        map((name): Span => ({start: name.start, end: name.end, mention: name.name})) : [];
    const spans = links.concat(tags, names).sort((a, b) => a.start - b.start);

    const parts: LinkPart[] = [];
    let at = 0;
    for (const span of spans) {
        if (span.start > at) {
            parts.push({text: text.slice(at, span.start)});
        }

        const part: LinkPart = {text: text.slice(span.start, span.end)};
        if (span.href) {
            part.href = span.href;
        }
        if (span.hashtag) {
            part.hashtag = span.hashtag;
        }
        if (span.mention) {
            part.mention = span.mention;
        }
        parts.push(part);
        at = span.end;
    }

    if (at < text.length) {
        parts.push({text: text.slice(at)});
    }

    return parts;
}

// Whether anything in the title can be pressed: a link, a hashtag or an
// @name. A title with one stops being a toggle button (Hud.tsx's ticker),
// because a control inside a button is two controls in one.
export function hasLinks(text: string, minimum: number, mentions = true): boolean {
    return linkParts(text, minimum, mentions).some((part) => part.href || part.hashtag || part.mention);
}
