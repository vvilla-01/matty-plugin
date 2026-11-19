'use strict';

// A webpack loader for webapp/src/styles.ts, run before ts-loader in both the
// plugin's build and the lab's (enforce: 'pre'), so the lab keeps running the
// code that ships.
//
// The stylesheets are template literals full of comments that explain design
// decisions, and every byte of them used to ship. This takes the comments out
// and nothing else of substance. It is deliberately not a CSS minifier, because
// edits to that file have broken the stylesheet before without a single check
// noticing (a browser drops rules after a syntax slip and says nothing), and a
// clever transform would be one more way for that to happen. What it does, to
// each `const NAME = \`...\`;` literal and to nothing else in the file:
//
//   1. removes every /* ... */ comment, keeping the newlines inside it, and
//      never touching text inside a quoted CSS string;
//   2. trims spaces and tabs from the start and end of each line;
//   3. drops the lines that are left empty.
//
// It does not collapse whitespace within a line, does not join lines, and
// copies every ${...} interpolation exactly as written.
//
// Anything it does not expect stops the build instead of being guessed at: an
// unterminated comment or string, a backslash anywhere in a literal (in a
// template literal it is a JS escape, so the text would not be what the
// browser gets), a ${...} that is not a simple expression, a ${ inside a
// comment (the JS would still evaluate it), a "/*" inside a string, a stray
// "*/", a comment glued to code on both sides (taking it out could join two
// tokens or split a selector), a backtick anywhere else in the file, or a
// literal that does not end with a backtick at the start of a line.
//
// build/check-squeeze.mjs imports squeezeSource and squeezeCss from here and
// proves, token by token, that the CSS which comes out is the CSS that went in.

class SqueezeError extends Error {}

const OPENING = /^const ([A-Z][A-Z0-9_]*) = `/gm;

function lineOf(text, offset) {
    let line = 1;
    for (let i = 0; i < offset && i < text.length; i++) {
        if (text[i] === '\n') {
            line++;
        }
    }

    return line;
}

const isSpace = (char) => char === ' ' || char === '\t' || char === '\n';

// One ${...} interpolation starting at `start` (the "$"). Only a simple
// expression is accepted: identifiers, calls, and single- or double-quoted JS
// strings, all on one line. No braces, no backticks, no backslashes. Returns
// the index just past its closing "}".
function interpolationEnd(css, start, fail) {
    let quote = '';
    for (let i = start + 2; i < css.length; i++) {
        const char = css[i];
        if (char === '\n' || char === '\r' || char === '`' || char === '\\') {
            fail(start, `an interpolation holds ${JSON.stringify(char)}, which this loader does not handle`);
        }
        if (quote) {
            if (char === quote) {
                quote = '';
            }
        } else if (char === '\'' || char === '"') {
            quote = char;
        } else if (char === '{') {
            fail(start, 'an interpolation holds a "{", which this loader does not handle');
        } else if (char === '}') {
            if (!css.slice(start + 2, i).trim()) {
                fail(start, 'an empty interpolation');
            }

            return i + 1;
        }
    }

    return fail(start, 'an unterminated interpolation');
}

// The transform, on the raw text of one literal (what sits between its
// backticks). `where(offset)` turns an offset in that text into a place for an
// error message.
function squeezeCss(css, where = (offset) => `offset ${offset}`) {
    const fail = (offset, message) => {
        throw new SqueezeError(`squeeze-css-loader: ${where(offset)}: ${message}`);
    };

    for (const [char, name] of [['`', 'a backtick'], ['\\', 'a backslash'], ['\r', 'a carriage return']]) {
        const at = css.indexOf(char);
        if (at >= 0) {
            fail(at, `${name} inside a stylesheet literal; this loader works on the raw text and will not guess`);
        }
    }

    let out = '';
    let i = 0;
    while (i < css.length) {
        const char = css[i];
        const next = css[i + 1];

        if (char === '/' && next === '*') {
            const close = css.indexOf('*/', i + 2);
            if (close < 0) {
                fail(i, 'an unterminated /* comment');
            }
            const end = close + 2;
            const comment = css.slice(i, end);
            if (comment.includes('${')) {
                fail(i, 'a ${ inside a comment: the JS evaluates it even though CSS ignores it');
            }
            const before = i === 0 ? '\n' : css[i - 1];
            const after = end >= css.length ? '\n' : css[end];
            if (!isSpace(before) && !isSpace(after)) {
                fail(i, 'a comment with code right against it on both sides; taking it out could join or split tokens');
            }
            out += comment.replace(/[^\n]/g, '');
            i = end;
        } else if (char === '*' && next === '/') {
            fail(i, 'a "*/" outside any comment, so a comment has ended early');
        } else if (char === '$' && next === '{') {
            const end = interpolationEnd(css, i, fail);
            out += css.slice(i, end);
            i = end;
        } else if (char === '"' || char === '\'') {
            let j = i + 1;
            for (;;) {
                if (j >= css.length || css[j] === '\n') {
                    fail(i, 'an unterminated string');
                }
                if (css[j] === char) {
                    j++;
                    break;
                }
                if (css[j] === '/' && css[j + 1] === '*') {
                    fail(j, 'a "/*" inside a quoted string');
                }
                if (css[j] === '$' && css[j + 1] === '{') {
                    j = interpolationEnd(css, j, fail);
                } else {
                    j++;
                }
            }
            out += css.slice(i, j);
            i = j;
        } else {
            out += char;
            i++;
        }
    }

    return out.
        split('\n').
        map((line) => line.replace(/^[ \t]+|[ \t]+$/g, '')).
        filter((line) => line !== '').
        join('\n');
}

// The whole module source in, the whole module source out, with every
// `const NAME = \`` stylesheet literal squeezed. Returns the new source and,
// for the checker, each literal's name, raw text and squeezed text.
function squeezeSource(source, file = 'styles.ts') {
    const literals = [];
    let code = '';
    let copied = 0;

    OPENING.lastIndex = 0;
    let match;
    while ((match = OPENING.exec(source)) !== null) {
        const start = match.index + match[0].length;
        const end = source.indexOf('`', start);
        const at = (offset) => `${file}:${lineOf(source, offset)}`;
        if (end < 0) {
            throw new SqueezeError(`squeeze-css-loader: ${at(match.index)}: ${match[1]} never ends`);
        }
        if (source[end - 1] !== '\n' || source.slice(end, end + 2) !== '`;') {
            throw new SqueezeError(`squeeze-css-loader: ${at(end)}: ${match[1]} should end with a backtick and a ` +
                'semicolon at the start of a line; a backtick anywhere else ends the literal early');
        }

        const raw = source.slice(start, end);
        const squeezed = squeezeCss(raw, (offset) => at(start + offset));
        literals.push({name: match[1], raw, squeezed});

        code += source.slice(copied, start) + squeezed;
        copied = end;
        OPENING.lastIndex = end + 1;
    }
    code += source.slice(copied);

    if (literals.length === 0) {
        throw new SqueezeError(`squeeze-css-loader: ${file}: found no "const NAME = \`" stylesheet literal`);
    }

    // Every backtick in the file must be one of the literals' own two. A
    // template literal this loader did not recognise would ship unsqueezed at
    // best, and at worst means the file is not shaped the way it thinks.
    const backticks = source.split('`').length - 1;
    if (backticks !== literals.length * 2) {
        throw new SqueezeError(`squeeze-css-loader: ${file}: ${backticks} backticks for ${literals.length} ` +
            'stylesheet literals; every template literal in this file must be a "const NAME = `" stylesheet');
    }

    return {code, literals};
}

module.exports = function squeezeCssLoader(source) {
    return squeezeSource(source, this.resourcePath).code;
};
module.exports.squeezeCss = squeezeCss;
module.exports.squeezeSource = squeezeSource;
module.exports.SqueezeError = SqueezeError;
