#!/usr/bin/env python3
"""Fail if styles.ts declares the same rule twice.

The stylesheets are template literals, so an edit that appends instead of
replacing leaves two rules for the same selector and the later one silently
wins. That has produced two shipped visual bugs, both of which looked like the
edit simply had no effect.

Three things this used to miss, and no longer does:

  * The head pattern required a rule to start with . # : or [, so every rule
    headed by an element name was invisible — including all four
    `button.vntv-pill` rules, exactly the kind a later duplicate would break.
  * It sliced from the first literal to the LAST backtick-semicolon in the
    file, swallowing the code between the two stylesheets as if it were CSS.
    Each literal is now found and scanned on its own.
  * A comma-separated head only registered its final line.

What counts as a duplicate is deliberately narrow, because `.vntv` legitimately
appears both in the box-sizing reset list and as its own rule, and those do not
fight: two rules declaring DIFFERENT properties for overlapping selectors is
ordinary CSS. The accident worth catching is a rule repeated in the same shape —
the same lone selector twice, or the same list twice — which is what an
append-instead-of-replace produces.
"""
import collections
import glob
import re
import sys

path = 'webapp/src/styles.ts'
source = open(path).read()

literals = re.findall(r'^const [A-Z_]+ = `(.*?)^`;', source, re.S | re.M)
if not literals:
    print(f'{path}: found no stylesheet literals to check', file=sys.stderr)
    sys.exit(1)

# Braces first, because nothing below is meaningful on a stylesheet the browser
# will refuse to finish reading. A splice that leaves ".vntv-x {.vntv-x {" is a
# syntax error, and a browser does not recover from one: it discards every rule
# after it and reports nothing. That shipped once, silently, and took a third of
# the stylesheet with it.
# A ${...} interpolation is TypeScript, not CSS: its braces are not a rule
# opening. They used to be counted as one, which only worked because no line had
# held two interpolations — the motion timings are the first that do.
def uninterpolated(css):
    return re.sub(r'\$\{[^}]*\}', 'X', css)


for index, css in enumerate(literals):
    plain = uninterpolated(re.sub(r'/\*.*?\*/', '', css, flags=re.S))
    opens, closes = plain.count('{'), plain.count('}')
    if opens != closes:
        print(f'{path}: stylesheet {index + 1} has {opens} opening braces and {closes} closing ones',
              file=sys.stderr)
        sys.exit(1)

    for number, line in enumerate(plain.split('\n'), 1):
        if line.count('{') > 1:
            print(f'{path}: line {number} opens two rules at once: {line.strip()[:70]}', file=sys.stderr)
            sys.exit(1)

heads = []
for css in literals:
    # Unindented heads only, so rules nested in @media/@container/@supports are
    # left alone. Comments are stripped first so a brace inside one cannot end
    # a head early.
    plain = uninterpolated(re.sub(r'/\*.*?\*/', '', css, flags=re.S))
    for head in re.findall(r'(?m)^([A-Za-z.#:\[][^{]*?)\{', plain):
        parts = [' '.join(p.split()) for p in head.split(',')]
        parts = [p for p in parts if p]
        if parts:
            heads.append(tuple(parts))

counts = collections.Counter(heads)
duplicates = {h: n for h, n in counts.items() if n > 1}

if duplicates:
    print(f'{path}: duplicate rules, the later one silently wins:', file=sys.stderr)
    for head, count in sorted(duplicates.items()):
        print(f'  {", ".join(head)}  x{count}', file=sys.stderr)
    sys.exit(1)

# And the other direction: a class the components ask for that no rule defines.
#
# Editing this file by slicing between two landmarks is how it is maintained,
# and one such slice silently took the whole "Up next" block with the section it
# was aiming at. Everything still compiled, every check still passed, and the
# panel rendered as an unstyled bulleted list.
components = set()
for source_file in glob.glob('webapp/src/**/*.tsx', recursive=True):
    for name in re.findall(r'vntv-[a-z0-9-]+', open(source_file).read()):
        components.add(name)

styled = set()
for css in literals:
    for name in re.findall(r'vntv-[a-z0-9-]+', css):
        styled.add(name)

undressed = sorted(components - styled)
if undressed:
    print(f'{path}: classes used by a component with no rule anywhere:', file=sys.stderr)
    for name in undressed:
        print(f'  .{name}', file=sys.stderr)
    sys.exit(1)

selectors = sum(len(h) for h in heads)
print(f'{path}: {len(literals)} stylesheets, {len(heads)} rules, {selectors} selectors, '
      f'{len(components)} classes used, none undressed')
