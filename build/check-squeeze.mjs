// Prove that build/squeeze-css-loader.js changes nothing the browser reads.
//
// The loader strips comments and indentation from webapp/src/styles.ts's
// stylesheet literals at build time. This runs the loader's own function on
// the file exactly as webpack hands it over, then tokenizes the CSS before and
// after with a tokenizer of its own (not the loader's scanner, so a mistake in
// one is not repeated in the other) and fails unless:
//
//   * the tokens are the same, in the same order, once comments and whitespace
//     are set aside;
//   * whitespace sits between the same tokens as before, so no two tokens were
//     joined and no selector gained or lost a descendant combinator;
//   * every ${...} interpolation is still there, character for character;
//   * no comment is left, and the output is smaller;
//   * the loader found the same literals build/check-styles.py reads;
//   * both webpack configs, the plugin's and the lab's, apply it, so the lab
//     runs the CSS that ships;
//   * and the loader still refuses each kind of input it is meant to refuse.

import {readFileSync} from 'node:fs';
import {createRequire} from 'node:module';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const require = createRequire(import.meta.url);
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const loaderPath = path.join(root, 'build', 'squeeze-css-loader.js');
const {squeezeCss, squeezeSource, SqueezeError} = require(loaderPath);

const STYLES = 'webapp/src/styles.ts';
let failed = false;
const fail = (message) => {
    console.error(`check-squeeze: ${message}`);
    failed = true;
};

// --- A small CSS tokenizer: whitespace, comments, strings, ${...}, numbers,
// identifiers and single punctuation characters. Finer than CSS needs in
// places and coarser in others, but the same on both sides, which is what a
// comparison needs. An interpolation is one opaque token, or part of the
// string it sits in, and is also collected on its own.
const WS = /[ \t\n\r\f]+/y;
const NUMBER = /[+-]?(?:\d*\.\d+|\d+)(?:[eE][+-]?\d+)?/y;
const IDENT = /-*[A-Za-z_\u0080-￿][-\w\u0080-￿]*|--[-\w\u0080-￿]*/y;

function tokenize(css) {
    const tokens = [];
    const interpolations = [];
    const sticky = (pattern, at) => {
        pattern.lastIndex = at;
        const match = pattern.exec(css);

        return match ? match[0] : '';
    };
    const interpolationEnd = (at) => {
        let quote = '';
        for (let j = at + 2; j < css.length; j++) {
            const char = css[j];
            if (quote) {
                quote = char === quote ? '' : quote;
            } else if (char === '"' || char === '\'') {
                quote = char;
            } else if (char === '}') {
                interpolations.push(css.slice(at, j + 1));

                return j + 1;
            }
        }
        throw new Error(`unterminated interpolation at ${at}`);
    };

    let i = 0;
    while (i < css.length) {
        const char = css[i];
        let text = sticky(WS, i);
        if (text) {
            tokens.push({type: 'ws', text: ' '});
            i += text.length;
            continue;
        }
        if (char === '/' && css[i + 1] === '*') {
            const end = css.indexOf('*/', i + 2);
            if (end < 0) {
                throw new Error(`unterminated comment at ${i}`);
            }
            tokens.push({type: 'comment', text: css.slice(i, end + 2)});
            i = end + 2;
            continue;
        }
        if (char === '$' && css[i + 1] === '{') {
            const end = interpolationEnd(i);
            tokens.push({type: 'interpolation', text: css.slice(i, end)});
            i = end;
            continue;
        }
        if (char === '"' || char === '\'') {
            let j = i + 1;
            while (j < css.length && css[j] !== char) {
                if (css[j] === '\n') {
                    throw new Error(`unterminated string at ${i}`);
                }
                j = css[j] === '$' && css[j + 1] === '{' ? interpolationEnd(j) : j + 1;
            }
            tokens.push({type: 'string', text: css.slice(i, j + 1)});
            i = j + 1;
            continue;
        }
        text = sticky(NUMBER, i);
        if (text) {
            tokens.push({type: 'number', text});
            i += text.length;
            continue;
        }
        text = sticky(IDENT, i);
        if (text) {
            tokens.push({type: 'ident', text});
            i += text.length;
            continue;
        }
        tokens.push({type: 'punct', text: char});
        i++;
    }

    return {tokens, interpolations};
}

// Comments and whitespace set aside.
const significant = (tokens) => tokens.filter((t) => t.type !== 'ws' && t.type !== 'comment');

// Comments set aside, each run of whitespace (a comment between two runs
// included) counted once, and none at either end.
function shape(tokens) {
    const out = [];
    for (const token of tokens) {
        if (token.type === 'comment' || (token.type === 'ws' && (out.length === 0 || out[out.length - 1].type === 'ws'))) {
            continue;
        }
        out.push(token);
    }
    while (out.length && out[out.length - 1].type === 'ws') {
        out.pop();
    }

    return out;
}

function firstDifference(a, b) {
    const length = Math.max(a.length, b.length);
    for (let i = 0; i < length; i++) {
        if (!a[i] || !b[i] || a[i].type !== b[i].type || a[i].text !== b[i].text) {
            return i;
        }
    }

    return -1;
}

function context(tokens, at) {
    return tokens.slice(Math.max(0, at - 6), at + 6).map((t) => t.text).join('').replace(/\s+/g, ' ');
}

function compare(name, label, before, after) {
    const at = firstDifference(before, after);
    if (at >= 0) {
        fail(`${name}: the ${label} differ at token ${at} of ${before.length} (now ${after.length}):\n` +
            `  before: ${context(before, at)}\n  after:  ${context(after, at)}`);

        return false;
    }

    return true;
}

// --- The real file, as webpack hands it to the loader.
const source = readFileSync(path.join(root, STYLES), 'utf8');
const {code, literals} = squeezeSource(source, STYLES);

// The same literals check-styles.py reads, so neither check is looking at
// something the other cannot see.
const expected = [...source.matchAll(/^const [A-Z_]+ = `([\s\S]*?)^`;/gm)].map((m) => m[1]);
if (expected.length !== literals.length || expected.some((raw, i) => raw !== literals[i].raw)) {
    fail(`${STYLES}: the loader found ${literals.length} stylesheet literals and check-styles.py's ` +
        `pattern finds ${expected.length}, or they differ`);
}

const bytes = (text) => Buffer.byteLength(text, 'utf8');
let tokenCount = 0;
let commentCount = 0;
let interpolationCount = 0;
let rawBytes = 0;
let squeezedBytes = 0;
for (const {name, raw, squeezed} of literals) {
    const before = tokenize(raw);
    const after = tokenize(squeezed);

    const kept = significant(before.tokens);
    if (compare(name, 'tokens', kept, significant(after.tokens))) {
        compare(name, 'places whitespace separates tokens', shape(before.tokens), shape(after.tokens));
    }
    if (before.interpolations.join('\n') !== after.interpolations.join('\n')) {
        fail(`${name}: the \${...} interpolations are not the same before and after`);
    }
    if (after.tokens.some((t) => t.type === 'comment')) {
        fail(`${name}: a comment survived`);
    }
    if (bytes(squeezed) > bytes(raw)) {
        fail(`${name}: the output is larger than the input`);
    }

    tokenCount += kept.length;
    commentCount += before.tokens.filter((t) => t.type === 'comment').length;
    interpolationCount += before.interpolations.length;
    rawBytes += bytes(raw);
    squeezedBytes += bytes(squeezed);
}
if (!(squeezedBytes < rawBytes) || !(bytes(code) < bytes(source))) {
    fail(`${STYLES}: the squeeze did not make it any smaller`);
}

// --- Both builds apply it, before ts-loader, to styles.ts and nothing else.
for (const config of ['webapp/webpack.config.js', 'webapp/lab/webpack.config.js']) {
    const {module: {rules}} = require(path.join(root, config));
    const rule = rules.find((r) => {
        const use = Array.isArray(r.use) ? r.use : [r.use];

        return use.some((u) => (typeof u === 'string' ? u : u && u.loader) === loaderPath);
    });
    if (!rule) {
        fail(`${config}: does not apply build/squeeze-css-loader.js`);
        continue;
    }
    if (rule.enforce !== 'pre' || !(rule.test instanceof RegExp) ||
        !rule.test.test(path.join(root, STYLES)) || rule.test.test(path.join(root, 'webapp/src/index.tsx'))) {
        fail(`${config}: the squeeze rule should be enforce: 'pre' and match styles.ts only`);
    }
}

// --- What it must refuse, so a surprise stops the build instead of shipping.
const refusals = [
    ['an unterminated comment', '.a { color: red; }\n/* never closed\n'],
    ['an unterminated string', '.a::after { content: \'x; }\n'],
    ['"/*" inside a string', '.a::after { content: "x /* y */"; }\n'],
    ['a stray "*/"', '.a { color: red; } */\n'],
    ['a comment glued between tokens', '.b/* glued */.c { color: red; }\n'],
    ['${ inside a comment', '.a { width: ${W}px; }\n/* was ${W} */\n'],
    ['a brace inside an interpolation', '.a { width: ${f({})}px; }\n'],
    ['a newline inside an interpolation', '.a { width: ${W\n}px; }\n'],
    ['a backslash', '.a::before { content: "\\201C"; }\n'],
    ['a carriage return', '.a {\r\n    color: red;\r\n}\r\n'],
];
for (const [what, css] of refusals) {
    try {
        squeezeCss(css);
        fail(`the loader accepted ${what}`);
    } catch (error) {
        if (!(error instanceof SqueezeError)) {
            throw error;
        }
    }
}
const sourceRefusals = [
    ['a second kind of template literal', 'const CSS = `\n.a {}\n`;\nconst x = `y`;\n'],
    ['a literal that ends mid-line', 'const CSS = `\n.a {}`;\n'],
    ['a file with no stylesheet literal', 'export const X = 1;\n'],
];
for (const [what, text] of sourceRefusals) {
    try {
        squeezeSource(text);
        fail(`the loader accepted ${what}`);
    } catch (error) {
        if (!(error instanceof SqueezeError)) {
            throw error;
        }
    }
}

// And what it must do, exactly.
const sample = 'const CSS = `\n.a,\n.a .b {\n    /* a note\n       on two lines */\n' +
    '    background: url(\'${staticURL(\'x\')}\');\n\n    content: "a  b";\t\n' +
    '    animation: x  ${MS}ms ease;\n}\n`;\nexport default CSS;\n';
const want = 'const CSS = `.a,\n.a .b {\nbackground: url(\'${staticURL(\'x\')}\');\ncontent: "a  b";\n' +
    'animation: x  ${MS}ms ease;\n}`;\nexport default CSS;\n';
if (squeezeSource(sample).code !== want) {
    fail(`the loader squeezed the sample into ${JSON.stringify(squeezeSource(sample).code)}`);
}

if (failed) {
    process.exit(1);
}

const kb = (n) => `${(n / 1024).toFixed(1)} KiB`;
console.log(`${STYLES}: ${literals.length} stylesheets squeezed from ${kb(rawBytes)} to ${kb(squeezedBytes)} ` +
    `(${commentCount} comments out), ${tokenCount} tokens and ${interpolationCount} interpolations unchanged, ` +
    `${refusals.length + sourceRefusals.length} refusals hold`);
