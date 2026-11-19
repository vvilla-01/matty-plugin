// @names in stream titles (design/3.2.0.md 8.2 and 8.7), checked without a
// browser: which @ in a title is a person by Mattermost's own mention rule,
// what is given back to the sentence, what never becomes anything, that an
// address wins over a name inside it, that a Twitch card makes none, and that
// the only HTML ever handed to Mattermost's messageHtmlToComponent is one
// mention in a fixed shape.
//
// webapp/src/titleParts.ts imports nothing, so it is transpiled with the
// project's own TypeScript, imported from a data URL and run here, as
// check-twitch.mjs does with twitchLogic.ts. Run from the project root (make
// check-webapp).

import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {createRequire} from 'node:module';

const require = createRequire(import.meta.url);
const ts = require('../webapp/node_modules/typescript');

async function load(file) {
    const source = readFileSync(new URL(`../webapp/src/${file}`, import.meta.url), 'utf8');
    const out = ts.transpileModule(source, {
        compilerOptions: {module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2020},
        fileName: file,
    });

    return import(`data:text/javascript;base64,${Buffer.from(out.outputText).toString('base64')}`);
}

const parts = await load('titleParts.ts');

let checks = 0;
function check(name, run) {
    try {
        run();
        checks++;
    } catch (error) {
        console.error(`check-mentions: ${name}`);
        throw error;
    }
}

// The names found in a title, as [typed, name] pairs.
const named = (text) => parts.findMentions(text).map((mention) => [mention.typed, mention.name]);

// What linkParts makes of a title: each piece as a short tag, so a case reads
// as the title does. MinimumHashtagLength is Mattermost's default, 3.
const pieces = (text, mentions = true) => parts.linkParts(text, 3, mentions).map((part) => {
    if (part.href) {
        return `link:${part.text}`;
    }
    if (part.hashtag) {
        return `tag:${part.hashtag}`;
    }
    if (part.mention) {
        return `name:${part.mention}:${part.text}`;
    }

    return part.text;
});

const SHAPE = /^<span data-mention="[a-z0-9._-]+">@[A-Za-z0-9._-]+<\/span>$/;

// Every title any case below uses, for the HTML-shape check at the end.
const TITLES = [];
const title = (text) => {
    TITLES.push(text);

    return text;
};

// ---- Mattermost's rule (MENTIONS_REGEX) ----

check('a name at the start of a title, after a space and after punctuation is a mention', () => {
    assert.deepEqual(named(title('@dave is live')), [['dave', 'dave']]);
    assert.deepEqual(named(title('Arma night with @dave and @bob')), [['dave', 'dave'], ['bob', 'bob']]);
    assert.deepEqual(named(title('Racing (@Dave)')), [['Dave', 'dave']], 'case kept as typed, looked up in lower case');
    assert.deepEqual(named(title('co-op,@erin')), [['erin', 'erin']]);
});

check('a name after underscores is a mention, as Mattermost has it', () => {
    assert.deepEqual(named(title('hey _@dave')), [['dave', 'dave']]);
    assert.deepEqual(named(title('__@bob__')), [['bob', 'bob']], 'and the trailing underscores go back to the sentence');
});

check('an @ that follows a letter or a digit is not a mention: an address, mailto:', () => {
    assert.deepEqual(named(title('mail me@example.com')), []);
    assert.deepEqual(named(title('mailto:a@b.c')), []);
    assert.deepEqual(named(title('room4@ten')), []);
});

check('trailing dots, dashes and underscores are given back to the sentence', () => {
    assert.deepEqual(named(title('Thanks @dave.')), [['dave', 'dave']]);
    assert.deepEqual(named(title('with @bob- tonight')), [['bob', 'bob']]);
    assert.deepEqual(named(title('with @erin_ tonight')), [['erin', 'erin']]);
    assert.deepEqual(named(title('with @victor.demo.')), [['victor.demo', 'victor.demo']], 'a dot inside stays');
    assert.deepEqual(pieces('Thanks @dave.'), ['Thanks ', 'name:dave:@dave', '.']);
});

check('@all, @channel and @here, in any case and with punctuation, never become anything', () => {
    for (const text of ['@all welcome', '@ALL welcome', 'ping @channel.', 'who is @here!', 'Hey @All']) {
        title(text);
        assert.deepEqual(named(text), [], text);
    }
});

check('a name with a remote part stays plain text', () => {
    assert.deepEqual(named(title('with @dave:other.server tonight')), []);
});

check('a username of 64 characters is a mention, and one of 65 is not', () => {
    const sixtyFour = 'a'.repeat(64);
    const sixtyFive = 'a'.repeat(65);
    assert.deepEqual(named(title(`hi @${sixtyFour}`)), [[sixtyFour, sixtyFour]]);
    assert.deepEqual(named(title(`hi @${sixtyFive}`)), []);
});

check('only the plain-letter part of a name is read: a letter outside a-z ends it', () => {
    assert.deepEqual(named(title('@dave’s stream')), [['dave', 'dave']]);
    assert.deepEqual(named(title('@bob, then')), [['bob', 'bob']]);
});

// ---- Addresses, hashtags, Twitch ----

check('an address wins over a name inside it', () => {
    assert.deepEqual(pieces(title('https://twitch.tv/@dave')), ['link:https://twitch.tv/@dave']);
    assert.deepEqual(pieces(title('see www.example.com/@x now')), ['see ', 'link:www.example.com/@x', ' now']);
});

check('a hashtag and a name side by side are one of each, in order', () => {
    assert.deepEqual(pieces(title('Arma night with @dave and @bob #arma3')),
        ['Arma night with ', 'name:dave:@dave', ' and ', 'name:bob:@bob', ' ', 'tag:arma3']);
    assert.deepEqual(pieces(title('#arma3 @dave')), ['tag:arma3', ' ', 'name:dave:@dave']);
    assert.deepEqual(pieces(title('@dave #arma3')), ['name:dave:@dave', ' ', 'tag:arma3']);
});

check('a Twitch card makes no names at all, and its hashtags and links still', () => {
    assert.deepEqual(pieces('GG @shroud #arma3', false), ['GG @shroud ', 'tag:arma3']);
    assert.equal(parts.hasLinks('GG @shroud', 3, false), false);
});

check('hasLinks counts a name, so a title with one stops being a toggle button', () => {
    assert.equal(parts.hasLinks('with @dave tonight', 3), true);
    assert.equal(parts.hasLinks('just words', 3), false);
    assert.equal(parts.hasLinks('mail me@example.com', 3), false);
});

check('the pieces of a title put back together are the title, every time', () => {
    for (const text of TITLES) {
        assert.equal(parts.linkParts(text, 3).map((part) => part.text).join(''), text, text);
    }
});

// ---- The HTML handed to Mattermost ----

check('every name found makes exactly one mention in the one fixed shape', () => {
    let seen = 0;
    for (const text of TITLES) {
        for (const mention of parts.findMentions(text)) {
            const html = parts.mentionHTML(mention);
            assert.match(html, SHAPE, html);
            assert.equal(html, `<span data-mention="${mention.name}">@${mention.typed}</span>`);
            seen++;
        }
    }
    assert.ok(seen >= 15, `only ${seen} names were checked`);
});

check('a title is never handed to Mattermost whole: the drawing builds the HTML from one name only', () => {
    const linkify = readFileSync(new URL('../webapp/src/linkify.tsx', import.meta.url), 'utf8');
    const calls = linkify.match(/messageHtmlToComponent\?\.\((.*?), MENTION_OPTIONS\)/g) || [];
    assert.equal(calls.length, 1, 'one call');
    assert.match(calls[0], /\(titleParts\.mentionHTML\(\{name, typed\}\), MENTION_OPTIONS\)$/);
});

console.log(`check-mentions: ${checks} checks passed`);
