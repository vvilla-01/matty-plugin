#!/usr/bin/env python3
"""Fail if the webapp is built and checked against a React Mattermost won't run.

The plugin does not bundle React: webpack.config.js takes Mattermost's, off the
page (window.React and window.ReactDOM, set in Mattermost's
webapp/channels/src/plugins/export.ts). So on a real server the panel runs on
whatever React that Mattermost ships, whatever webapp/package.json says.

The lab, though, bundles whatever React is in node_modules, and tsc checks
against whatever @types/react is. If those are newer than Mattermost's, an API
Mattermost's React lacks passes every local check, then throws on a real
server, and Mattermost replaces the whole panel with its plugin error box.
That is how React 18's useId nearly shipped when servers ran React 17.

So the pin follows the oldest server plugin.json allows, min_server_version,
and this check holds three things together:

  - MATTERMOST below says what that server ships, read from its source;
  - webapp/package.json pins react and react-dom to exactly that, and
    @types/react and @types/react-dom to the same major;
  - webapp/node_modules holds what package.json pins.

Moving min_server_version without adding its row below fails here on purpose:
somebody has to look at what the new floor ships before the pin can follow.
"""
import json
import os
import re
import sys

# What Mattermost's own web app ships, by the min_server_version it applies to.
# Read from github.com/mattermost/mattermost at the tags named in each row:
# webapp/channels/package.json pins react and react-dom in dependencies and
# @types/react and @types/react-dom in devDependencies, and
# webapp/package-lock.json holds exactly one copy of each, at those versions.
#
# Mattermost 11.11 mounts its page with ReactDOM.render, a legacy root, unless
# the EnableConcurrentReact feature flag is on (webapp/channels/src/entry.tsx);
# the lab can mount either way.
MATTERMOST = {
    '11.11.0': {
        'source': 'mattermost/mattermost tags v11.11.0 and v11.11.1, '
                  'webapp/channels/package.json and webapp/package-lock.json',
        'react': '18.2.0',
        'react-dom': '18.2.0',
        '@types/react': '18.2.64',
        '@types/react-dom': '18.2.25',
    },
}

# Pinned to exactly Mattermost's: these are what runs.
RUNTIME = ('react', 'react-dom')

# Pinned to the same major: these only describe what runs, and a patch of
# difference in a type file changes nothing on the page.
TYPES = ('@types/react', '@types/react-dom')

# Mattermost puts these on the page; bundling a copy would give the plugin a
# second React, and every hook in the panel would throw.
EXTERNALS = (('react', 'React'), ('react-dom', 'ReactDOM'))

EXACT = re.compile(r'^\d+\.\d+\.\d+$')


def major(version):
    return version.split('.')[0]


def installed(name):
    try:
        path = os.path.join('webapp/node_modules', name, 'package.json')
        return json.load(open(path))['version']
    except FileNotFoundError:
        return 'nothing'


def fail(problems, footer):
    for problem in problems:
        print(problem, file=sys.stderr)
    print(footer, file=sys.stderr)
    sys.exit(1)


minimum = json.load(open('plugin.json'))['min_server_version']
ships = MATTERMOST.get(minimum)
if ships is None:
    known = ', '.join(sorted(MATTERMOST))
    fail(
        [f'plugin.json: min_server_version is {minimum}, and build/check-react.py '
         f'only knows what Mattermost ships at {known}.'],
        f'Read webapp/channels/package.json (and webapp/package-lock.json) at tag '
        f'v{minimum} in mattermost/mattermost, add a row for {minimum} to MATTERMOST in '
        'build/check-react.py with its source, then pin webapp/package.json to it '
        'and run npm install in webapp.',
    )

declared = json.load(open('webapp/package.json'))['devDependencies']

problems = []
for name in RUNTIME + TYPES:
    wanted = ships[name]
    pinned = declared.get(name, 'nothing')
    if not EXACT.match(pinned):
        problems.append(f'webapp/package.json pins {name} to {pinned}, not one exact version')
        continue

    if name in RUNTIME and pinned != wanted:
        problems.append(f'webapp/package.json pins {name} to {pinned}; Mattermost {minimum} ships {wanted}')
    elif name in TYPES and major(pinned) != major(wanted):
        problems.append(f'webapp/package.json pins {name} to {pinned}; Mattermost {minimum} checks with {wanted}')
    elif installed(name) != pinned:
        problems.append(f'webapp/node_modules has {name} {installed(name)}, not {pinned}: run npm install in webapp')

webpack = open('webapp/webpack.config.js').read()
for module, name in EXTERNALS:
    if not re.search(r"""['"]?%s['"]?\s*:\s*['"]%s['"]""" % (re.escape(module), name), webpack):
        problems.append(f"webapp/webpack.config.js no longer takes {module} from Mattermost's window.{name}")

if problems:
    fail(
        problems,
        f'Mattermost {minimum} runs plugins on React {ships["react"]} ({ships["source"]}); '
        'the lab and tsc must use it too, or an API it lacks passes here and crashes there.',
    )

print(f'React {ships["react"]} throughout, as on Mattermost {minimum}')
