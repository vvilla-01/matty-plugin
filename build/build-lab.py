#!/usr/bin/env python3
"""Fold the lab bundle into one HTML file that opens from the filesystem.

A single file rather than a directory and a web server: the point of the lab is
that somebody can look at the panel and click around in it without being told to
run anything first. Nothing in it reaches the network — the fonts are data URIs
in the bundle and the "server" is an object in the page.
"""
import os
import sys

BUNDLE = 'webapp/lab/dist/lab.js'
SHELL = 'webapp/lab/shell.html'
OUT = 'dist/matty-lab.html'
MARKER = '<!--LAB_SCRIPT-->'

if not os.path.exists(BUNDLE):
    sys.exit(f'{BUNDLE} is missing; run the lab webpack build first')

script = open(BUNDLE, encoding='utf-8').read()

# An inline <script> ends at the first "</script" the parser sees, wherever it
# appears. Bail rather than guess: escaping it blindly would corrupt the bundle
# anywhere the sequence is not inside a string literal.
if '</script' in script.lower():
    sys.exit(f'{BUNDLE} contains "</script"; it cannot be inlined safely')

shell = open(SHELL, encoding='utf-8').read()
if MARKER not in shell:
    sys.exit(f'{SHELL} has no {MARKER} marker')

page = shell.replace(MARKER, '<script>\n' + script + '\n</script>')

os.makedirs('dist', exist_ok=True)
with open(OUT, 'w', encoding='utf-8') as handle:
    handle.write(page)

print(f'{OUT}: {len(page) / 1024:.0f} KiB, opens with no server')
