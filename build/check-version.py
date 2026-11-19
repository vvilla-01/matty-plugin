#!/usr/bin/env python3
"""Fail if the version or the id in plugin.json and the ones compiled into the server drift.

The server records manifestVersion in the airtime backup, so a stale constant
would label a backup with the wrong build — exactly when someone is trying to
work out which upgrade lost their hours.
"""
import json
import re
import sys

plugin = json.load(open('plugin.json'))
manifest = plugin['version']

source = open('server/plugin.go').read()
match = re.search(r'manifestVersion\s*=\s*"([^"]+)"', source)
if not match:
    print('server/plugin.go: no manifestVersion constant', file=sys.stderr)
    sys.exit(1)

if match.group(1) != manifest:
    print(
        f'version drift: plugin.json says {manifest}, '
        f'server/plugin.go says {match.group(1)}',
        file=sys.stderr,
    )
    sys.exit(1)

match = re.search(r'manifestID\s*=\s*"([^"]+)"', source)
if not match:
    print('server/plugin.go: no manifestID constant', file=sys.stderr)
    sys.exit(1)

if match.group(1) != plugin['id']:
    print(
        f'id drift: plugin.json says {plugin["id"]}, '
        f'server/plugin.go says {match.group(1)}',
        file=sys.stderr,
    )
    sys.exit(1)

print(f'plugin.json and server/plugin.go agree on version {manifest} and id {plugin["id"]}')
