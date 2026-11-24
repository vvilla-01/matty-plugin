[The Matty Plugin manual](README.md)

# Building and how it works

## Build from source

Requires Go 1.27.1 or later, and Node 18+. `go.mod` also names a toolchain, Go 1.27.4, whose
standard library has security fixes the server needs: with the default `GOTOOLCHAIN=auto` an older
Go 1.27 fetches 1.27.4 through the module proxy and builds with it, and an older Go either refuses
or fetches it, depending on `GOTOOLCHAIN`. The module is `github.com/vvilla-01/matty-plugin`.

```bash
make dist
```

This produces `dist/ceo.villa.matty-<version>.tar.gz` with server binaries for
linux/darwin (amd64 + arm64) and windows-amd64, plus the webapp bundle.

For a Linux amd64 server — the usual case — build the much smaller single-platform bundle:

```bash
make dist-linux    # dist/ceo.villa.matty-<version>-linux-amd64.tar.gz, about 12 MB
```

The file name is the plugin's id and version; the Makefile reads both from `plugin.json`. 3.8.0's
single-platform bundle was 11.7 MB, 3.4.0's 11.1 and 3.0.0's 10.5, where 2.11.0's was 8.3: the WebRTC
and MP4 libraries the Clips recorder is built on add about 6 MB to the server binary. Neither target
signs anything: a bundle is signed afterwards, by hand (see [Signing a release](#signing-a-release)),
and an unsigned bundle installs and runs exactly as a signed one does.

```bash
make check                # both halves below
make check-server         # go vet, go test -race (the server, the stand-ins and the release checker), gofmt, and that plugin.json and the server agree on the version
make check-webapp         # the React rule, tsc, the stylesheet checks (check-styles.py, check-squeeze.mjs), build/check-away.mjs, build/check-twitch.mjs, build/check-accounts.mjs, build/check-awards.mjs, build/check-sounds.mjs, build/check-mentions.mjs, build/check-player.mjs and build/check-reads.mjs
make e2e                  # the real-time WebRTC tests that make check leaves out
make e2e E2EFLAGS=-race   # the same under the race detector
```

`make check` is in two halves so that whoever is working on one can check it while the other is in
the middle of a change. It passes in any time zone: the birthday tests pin their own zone to UTC.

`build/check-away.mjs` runs the webapp's modules that import nothing — the side panel's return, the
small player's layout, the rules behind Clips and the lock, motion, focus, two-press buttons, and
the date handling in `time.ts` — without a browser, compiled with the project's own TypeScript.
From 3.1.0 `tagSearch.ts` runs the same way, with stand-ins playing Mattermost's part for the
modules it imports. For `time.ts` it checks that each kind of date makes its `Intl` formatter once
(one formatter for each language, zone and set of options is kept, since making one is the slow
part) and never uses it in another zone, and that the machine's zone is read once per render and a
change to it shows at the next. A formatter with no zone is made afresh each time, so it follows
the machine. Keeping them, and reading the zone once, took a snapshot push with four tiles and five
Up next cards from about 6 ms to 3, and a push or tick with the Clips list open on 101 clips from
about 35 ms to 4.5. For the hashtag search it checks that signing out forgets what Mattermost told
it in the last reader's name.

**The stylesheet's comments stay in the source.** `webapp/src/styles.ts` explains itself at length,
and none of that reaches a browser any more: both builds (the plugin's and the lab's) run
`build/squeeze-css-loader.js` over it before TypeScript sees it. The loader removes `/* … */`
comments, trims each line and drops the lines left empty, and does nothing else: it never joins two
lines or touches a quoted string or a `${…}`. That took `main.js` from 669 KB to 560 KB (176 KB to
140 KB gzipped) in 3.1.0. `build/check-squeeze.mjs`, part of `make check-webapp`, proves that it
changes no CSS token, no whitespace between tokens and no interpolation, and that both webpack configs
apply it. Before it went in, the squeezed stylesheet was also parsed in Chrome beside the original:
the same 1,349 rules, and the same computed style on every element of the panel. The loader stops the
build, naming the line, on anything it was not written for, which makes these rules of the file:
no `/*` inside a quoted string, no `${…}` inside a comment, no comment with code against it on both
sides, no backslash, simple `${…}` interpolations only (no braces inside), and each stylesheet a
`` const NAME = ` `` whose closing `` `; `` starts a line.

**React is Mattermost's.** The plugin bundle carries no React: webpack takes Mattermost's own off the
page (`window.React` and `window.ReactDOM`), and on Mattermost 11.11 that is **React 18.2.0**
(`webapp/channels/package.json` and the lockfile at v11.11.0 and v11.11.1). `webapp/package.json`
pins `react` and `react-dom` to exactly that, and `@types/react` 18.2.64 and `@types/react-dom`
18.2.25 as Mattermost's own web app does, so `tsc` and the lab see what the server runs: an API
React 18.2 lacks fails here rather than on the server. `build/check-react.py`, part of
`make check-webapp`, holds the pin to what Mattermost ships at `min_server_version`, checks that
`webapp/node_modules` matches the pin (run `npm install` in `webapp` after the pin changes), and
fails if `min_server_version` moves before its row in the script has been filled in from
Mattermost's source. Mattermost 11.11 mounts its page on a legacy root, `ReactDOM.render`, unless its
`EnableConcurrentReact` feature flag is on; the lab mounts the same way, and its **React root ›
concurrent (feature flag)** switch reloads it on `createRoot` instead.

`make e2e` runs the Clips recorder for real, in real time, over loopback: a publisher made with pion
sends the test clip, paced as it was recorded, to a small stand-in for Broadcast Box that answers WHEP
the way v2.0.2 does, and the real recorder records it, reconnects, and has a draft cut from it.
Real-time WebRTC under the race detector is flaky rather than wrong, so these tests skip themselves
unless `VILLANET_E2E=1`, which the target sets, and `make check` leaves them out.

Two dev-only scripts make the test media. Both need ffmpeg with libx264 and libopus; what they make is
committed, so no build needs them:

- `build/make-test-clip.sh` makes `server/testdata/clip-h264-opus.mp4`, the ten-second H.264 and Opus
  fixture the Clips tests feed through the recording, the cutting and the trimming.
- `build/make-lab-clip.sh` makes `webapp/lab/media/sample-clip.mp4`, the two-minute clip with a
  running clock burnt in that the lab hands out as every draft and clip (it needs ffmpeg's
  `drawtext` too). Only the lab imports it; the plugin bundle never does.

### Signing a release

A release is a `make dist-linux` bundle, signed afterwards by hand. Nothing in the Makefile signs.

```bash
make dist-linux
build/sign-release.sh <key folder> dist/ceo.villa.matty-<version>-linux-amd64.tar.gz
```

The key folder is a folder of your own outside the tree, laid out as Matty's signing kit makes it:
`matty-rsa.p12` and `matty-mldsa87.p12`, the RSA 4096 and ML-DSA-87 keys the app is signed with, and
their passwords in `pass/matty-rsa.pass` and `pass/matty-mldsa87.pass`. OpenSSL reads each key
straight from its file with `-passin file:`, so no password or key is ever on a command line, in the
environment or in a file left behind. The script needs OpenSSL 3.5 or newer (for ML-DSA), tar,
python3, sha256sum, stat and Go (`GO=/path/to/go` names one that is not on the `PATH`), and stops
at once if one is missing.

It unpacks the bundle and writes `release/manifest.txt`: a first line naming the format
(`matty-release-manifest 1`), then the product (`ceo.villa.matty`), the kind (`plugin`), the
version, the platforms and the number of files (`files <n>`), then one line per file, in byte order,
with its SHA-256, its size and its path. It signs the manifest's
exact bytes twice, `release/manifest.rsa.sig` (RSASSA-PSS with SHA-512 and a 64-byte salt, 512
bytes) and `release/manifest.mldsa87.sig` (pure ML-DSA-87 with the empty context, 4627 bytes),
repacks the bundle, and checks the new bundle with `build/checkrelease`, against the keys' own
certificates and against the two built into the plugin. Only a bundle that passes replaces the old
one, under the same name; otherwise the old one is left as it was. It refuses a bundle that is
already signed, the stand-in build (by its version, or by the sentence only that build carries), a
version that is not a plain release number, a bundle holding a `plugin.yml` or `plugin.yaml` (which
Mattermost reads in place of `plugin.json`) or anything that is not a plain file or folder, and keys
whose certificates are not the two built into the plugin, since every server would then report the
release as altered; `--unpinned` allows that last one, for a test with throwaway keys. It ends by
printing what to publish beside the release: the manifest's SHA-256, which is the release's
identity, and the SHA-256 of the two certificates.

**Checking a release.** `build/checkrelease` is the plugin's own checking code (`server/release`)
as a command, so a bundle, or an unpacked plugin folder, can be checked before it is installed:

```bash
go run ./build/checkrelease dist/ceo.villa.matty-4.5.0-linux-amd64.tar.gz
```

It checks both signatures before it reads anything in the manifest, then the product, the kind, the
version and the platforms, then every listed file's size and SHA-256, and that what Mattermost loads
(`plugin.json`, the executables and the web panel's bundle) is listed. It prints
`signed: ceo.villa.matty 4.5.0 linux-amd64, <n> files`, `unsigned: there is no release folder`, or
`altered:` and why; then the manifest's SHA-256, the two certificates it checked against, and any
files in the folder that nobody signed. It exits 0 for a signed release, 1 for anything else and 2
when it was not asked properly. `-rsa-cert` and `-mldsa-cert` check against other certificates,
`-version` holds the release to a version, `-platform` to one platform, and `-exact` to what
**Install** takes: no file the manifest does not list, a bundle of at most 64 MB, and the limits the
plugin unpacks a downloaded bundle within (4,000 entries, 256 MB). The signing script checks with
`-exact`.

The plugin runs the same check on its own folder each time it starts, and goes on running whatever
it finds. It logs the result once ("Matty is a signed release", with the manifest's SHA-256; "Matty
is not a signed release: there is no release folder, as in any development build"; or a warning
that its files do not match a signed release, naming the file), tells system admins in the panel
when its files are not as signed (the System Console's App check card says why) or when a signed
release's folder also holds files nobody signed (naming them, ten at most), and answers `GET /api/v1/release` with the manifest and both
signatures, which Matty verifies for itself. Every bundle `make dist` and `make dist-linux` make is
unsigned until it is signed. What a signature shows, and what it cannot, is under
[Signed releases](security.md#signed-releases): which release a server presents, not what code it
runs.

### The lab

```bash
make lab           # dist/matty-lab.html
```

One HTML file, titled "Matty Plugin lab". Open it in a browser — no server, no Mattermost, nothing
fetched — and the panel is there, working, next to a column of switches for the situations that are a nuisance to arrange on a
live server: four people live at once, a stream starting in two minutes, one that started an hour
ago, a save that fails, a slow server, an origin that is down.

The panel in it is **the real one**: the same `PanelGate` that Mattermost is given, showing the lock
screen or the `Sidebar`, the same stylesheet, the same date handling. Exactly six modules are
swapped by webpack — the HTTP client, the WebRTC player, Twitch's player and windows (below), the
manifest, which is only swapped so the font can be inlined as a data URI and the panel is drawn in
the typeface it ships with, and from 3.2.0 the soundboard's emoji pictures and its sound maker (below).
The WHEP
stand-in paints a canvas and hands over `canvas.captureStream()`, which is a real `MediaStream`, so
muting, the volume control and the audio-exclusivity rule behave exactly as they do against a real
broadcast.

It also renders the two places the plugin appears inside Mattermost's own furniture — the award
badges on a post header, and the profile popover — on the same fake data, and every System
Console control the plugin draws itself. The popover can be anybody's, on a light theme or a dark
one, and each lab streamer has a different history of past months, so every state of that row is
there to look at: twice champion, once champion, never first, and more months than fit. The trophy
in the lab's copy of the sidebar header opens the real Trophies card, monthly medals and all; **past
months only** empties both boards and keeps the medals, and **long name** gives Erin a display name
wider than any column, on every board, to see where it gives way. **Snoopers board** fills the
Snoopers tab (**five snoopers**), empties it with the setting still on (**empty**, which reads
"Nobody has snooped yet."), or switches it off (**off**, which leaves no tabs at all, and nobody
wearing the binoculars); **Victor snoops most** puts Victor, the lab's own account, first all time
instead of Erin, so the binoculars move to Victor, and the post header with them, beside Victor's
crown. The post header can be
anybody's (**Post by**), on a light theme or a dark one, so each person's badges can be seen on
white as well as black. **What’s on?** puts Dave's Arma 3, linked to the real game and so with art,
and Bob's Elden Ring, said with lookups off and so a kind and words alone, back on their cards while
they are live; **Erin’s plan is on** starts a film night linked to *Heat* half an hour ago, so her
card shows a running plan's title; **clear** takes everybody's down; and **lookups off** switches
title lookups off, so the footer's picker saves without a cover and art already saved stops being
relayed. The footer picker itself works against the lab's fake server, by the server's rules, and
going off the air in the lab takes that streamer's choice down at once, where the server waits for
the grace.

The hashtags in the lab streamers' titles open the real results card, on sample messages: `#milsim`
has three pages of them, the first one short by a deleted message, `#arma3` one of every kind — a reply, a direct message, a group message, a
webhook's post, a message in an archived channel, and a `#arma3` inside an address that must not be
marked — and `#soulslike` a few. **Hashtag search** has Mattermost answer with nothing, fail, or say
search is switched off. Stand-ins for Mattermost's store, its history and its search bar say what
Mattermost would have been asked to do when a message is opened or **Open in Mattermost search** is
pressed, from the panel or from the popout window, and **popout’s window gone** is a window that
never answers. **phone-sized** is Mattermost in its phone layout, where opening a message closes the
panel first.

Between the switches and the panel is a stand-in for the channel — its header, its message list
(`#post-list`) and its message box — with a Reply arrow on each message, so the side panel can be
taken away from the panel and given back the ways it is in Mattermost. The panel itself sits in a
`#sidebar-right` that follows a copy of Mattermost's side-panel reducers, and the small player is
mounted once at the root of the page, as Mattermost mounts a global component. **Side panel** has
**Reply** (from the keyboard; the arrows in the channel are the mouse, which collapses an expanded
panel first, as Mattermost does), **Open thread**, **Type in reply**, **Send reply** (a placeholder,
then the real post 400 ms later), **Reply fails**, **Reply from another device**, **Close side
panel**, **Search** and **Back to results**, **Mentions**, **Another plugin**, **Threads page**,
**Switch team**, **Channel switch** (Arma night has a bookmarks bar, so the list starts lower),
**Expanded**, **Delay mini mount by one frame**, which leaves the picture out of the page for a frame
on the way to the small player, and **Freeze pushes**, which leaves only the poll keeping the list
current. The thread has its own Attach, Edit, emoji picker, "…" menu and Delete root, and
**Settings** sets the two per-person settings. The **Media** row counts every WHEP exchange the mock
was asked for (**plays**), every one hung up (**stops**), how many are open (**live**), preview
stills uploaded (**stills**), sessions, and the most video elements ever unmuted at once, sampled
every 50 ms — more than one would be two streams audible together — with the reason the last session
ended. The mock's picture carries a seconds counter that starts again from 0.0 on any reconnect.
`window.__villanetLab` has the counters and `window.__villanetLabStore` the side panel's store, for
the panels the chips do not offer.

**Clips** has rows of its own, and every draft and clip in them plays the two-minute sample clip:
**Clips** (six clips, none, switched off, two drafts, and the list or one clip opened), **Admin**,
**Best Clipper**, **Recording** (every state the ✂ can
show, 47 seconds recorded, a recording cut short by its bitrate, and a dropped stream's end),
**Capture** and **Publish** (every answer the server can give, including **server forgot**, where
the server no longer knows the reader is watching, so the panel reconnects that tile once and asks
again), **Stream tag**, **Sound** (Safari's silence, and Mattermost snatching stray keys for its
message box), **Clip fetch**, **Ended tail**, **Streamer**, **Unseen clips**, **Retention pending**
and **Layout**. **Shared clips**, under the profile popover, draws a shared clip's post in every
state its card has — with and without a picture, expired, deleted, gone, a forged one, one whose
props are of the wrong types, and one that throws, to show the card's fallback — in Mattermost's
Denim or Onyx theme, and a clip shared from the panel lands in the lab's channel too.

The mock client is pinned to the real one's shape by `lab/mocks/surface.ts`: webpack swaps the
module wholesale, so a missing export is an `undefined` at runtime rather than a build error, and
that had already cost two debugging sessions. It is now a compile error.

**The lock** has rows of its own too, and every state of it can be reached:

- **Access** sets what the lab's server says about Victor as a fresh page would hear it: **linked**,
  **let in**, **locked**, **unknown** (the NO SIGNAL face), **starting** (the first answer held for
  two seconds), **signed in after load**, and **signed out, back locked**, where somebody locked signs
  in on the same page after Victor signs out with tiles playing.
- **Access changes** presses the very handlers the real event, timer and reconnect use:
  - **Let out now**, and **Let in now** while the lock is showing;
  - **linked + let in, editing**, then **Unlink while let in**: the editor closes, the panel comes
    back watch only, and the streams keep playing;
  - **Unlocked elsewhere**, which shows on the next **Window focused**;
  - **Socket down** and **Reconnect**;
  - **Store blip**: one "me" that does not answer;
  - **send a vote** as somebody let in, which the server refuses with `watch_only`.
- **Lab clock** moves time on by 15 seconds (a revealed token hides itself), a minute (the re-read
  while something plays) or ten minutes (anything waiting for the lock lapses).
- **Lock screen** picks what the next Unlock is answered with: success, each of the server's
  refusals, an empty stream key, or no answer.
- **Lock words** sets the plugin not set up, every admin alert (in the panel once unlocked; the lock is
  the same for an admin as for anybody), a public watch URL
  (Pop out for linked people), the unlock slowed ten times, and **Mattermost focuses the side panel**,
  a copy of Mattermost's own two-hop move of focus into the side panel. Without that copy, "the bar
  takes focus" would pass without being tested. It also sets which kind of window the page pretends
  to be: the main window, a channel popout or a thread popout.
- **Sound** has **Mattermost takes stray keys**. With it on, the lab's message box takes keys as
  Mattermost's does: printable keys, and Ctrl/Cmd+V from anything but a text box. So a paste that
  would miss the bar would be seen to miss it. **Ctrl+V, Russian keyboard** sends the paste a
  Russian layout sends. Enter in the lab's message box runs the post hook, so a token-shaped message
  can be seen being refused.
- **Show › System Console** includes the **Access** card, with lists of every kind and an add refused
  with each of the server's codes.

Where the panel would ask Mattermost something only once, because in Mattermost it never changes
under you — who you are, whether you are an admin, your stream note — the lab remounts the panel
instead, so those switches are not quietly inert. The **Me** row's **linked** switch remounts it too;
linking or unlinking from the panel's own footer does not, because the panel keeps track of that
itself, so the calendar and the pencils can be watched coming and going as they would in Mattermost.
Those switches also update what the plugin would have learned about you when the page loaded,
before the panel mounts again, as a reload would; the panel goes by that until its own check lands,
and a stale one would show the panel arriving late in a way no real opening does.

**Twitch** has rows of its own, and its own swapped module: `twitchEmbed`, the one module through
which the plugin's page reaches anything outside Mattermost. In the lab, Twitch's player is a
stand-in in its own element, never a frame, that says whose channel it is, whether it has the sound,
and how many times it has loaded, so a reload can be seen; the Connect window opens nothing and
simulates Twitch instead; and a link to twitch.tv shows a "Would open …" note rather than opening.
**Twitch** has the two switches, **player blocked here**, **no application**, a **callback problem**,
the **Site URL port** problem and **Twitch API down**; **Live on Twitch** puts Erin, Victor or Caro
live there, **Victor on both** simulcasts, **Erin on Broadcast Box** and **Erin pauses a poll** walk
the handoff from her Twitch card to her Broadcast Box one, and **watch Erin’s Twitch** watches it as
a card does; **Victor’s Twitch** is not connected, connected, taken over or blocked; **Connect** picks
how the ticket, the window and Twitch answer, a pop-up blocker and the desktop app included; and
**Twitch hours** is the ledger as the Twitch connections card sees it: fine, a removal waiting, a
set-aside copy, or recording halted. The widths 304, 400, 464 and 776 are the ones to look at:
464 is the narrowest panel where Twitch's player plays inside it. `window.__villanetLab` drives the
same scenarios by script. `build/check-twitch.mjs`, in `make check`, tests the pure rules behind
them: which pages can run Twitch's player, the sizes and the hysteresis, and that every player
address starts with Twitch's player prefix.

**3.2.0** has a row for each of its features, and each row sets the fake server up for a state and,
where the state is something a person does, drives the real panel there by pressing its own buttons:
**Choices** (nobody chose, the choices of the contract's examples, Victor's own, an unreadable
`award_prefs`, a NEW chip, and the Trophies card opened on each tab), **Fans** (on and off, nobody
past an hour, and the two ledger resets; 3.2.1 took out the opt-out chips), **Achievements** (the toast for a
new step, everybody's figures, a 24/7 channel line the plugin cannot read), **Soundboard** (every state
of the sheet: a sound playing, the wait, somebody else's sound, muted and the other reasons it is off,
blocked, empty, full, an unreadable board, and the plugin's Mumble account unable to speak), **Add a
sound** (a file, the microphone and a clip, then the trim, the emoji and the name, through the real
file input), **Plans** (every state of the plan card as posts in the channel, in Denim and Onyx, in
compact display and in a 304 px thread, the Up next share tool and the plan sheet's offer) and
**@names** (Teammate Name Display, a name that resolves late, names at the end of a title, `@all`,
addresses and a Twitch title). The microphone, the decoder and the WebCodecs encoder are a stand-in
fed from `webapp/lab/fixtures/sound-packets.json`, real Opus packets Chrome made; the emoji pictures
are a few dozen PNGs copied into the lab; Mattermost's own @mention is a stand-in that resolves after
500 ms when asked to. The lab still fetches nothing, except the avatar pictures it has always asked
the page's own origin for, which fail quietly and show initials. `window.__villanetLab.v320` drives
the same states by script.

**3.2.1** adds two chips to the **Clips** row: **downloads on**, the admin's switch (off, as it
ships), and **Victor blocks downloads**, a streamer's own switch for the clips of their stream. With
the first on, the clip viewer has **Download** for Victor while linked (and not on a clip of
Victor's stream while the second is on), and **Clips of your stream** has the second switch. The lab saves nothing: a Download
press is counted in `window.__villanetLab.downloads`.

**3.3.0** plays clips in the stream tile's look, so every state of the player can be looked at: a
**Clip file** row (**ok**, **never comes** for the loading screen, **fails** for CAN'T PLAY with Try
again; choose it, then open a clip, in the dialog or the System Console), and
`window.__villanetLab.clipPlayer` to open a clip afresh, set the file and the download switches, slow
the server so "Preparing…" can be seen, and give the clips pictures made from frames of their file.

**3.4.0** adds Steam and Hytale, both switched off at the start, as they ship. **Steam** and
**Hytale** have their switches, a missing key or application, the key refused, Steam out of reach,
the scope not granted, a callback problem, Dave deactivated and a set-aside copy; **Victor’s Steam**
and **Victor’s Hytale** are not connected, connected (online, playing, a long game's name, offline,
private), taken over or blocked; **Connect accounts** picks how the ticket, the window and the
service answer, a pop-up blocker, the desktop app and a Disconnect that fails included; **Others on
Steam** sets what Dave is playing (ELDEN RING, a 30- or 60-character name, a game with no name,
online, offline) and how many people are in a game, from nobody to eight, and refuses Copy; **Victor’s
connections** gives Victor any mix of Twitch, Steam and Hytale for the Disconnect line on the lock
screen and over Watch only, or three takeover notices; and **Accounts events** sends the two WebSocket events.
`window.__villanetLab.v340` does the same by script. The Go callback pages cannot be served by the
lab, which has no server: `VILLANET_PAGES_OUT=<folder> go test -run TestAccountPagesRender ./server/`
writes every one of them, for both services, as HTML files to look at.

**4.0.0** makes the lab any server, starting as a fresh one: named Matty, with no emblem, no ghost
and no banner. **Server name** gives the plugin another name, as the `me` event brings it, up to
**32 characters**, the longest a server may set; **reload, “me” late** reloads with the server's
answer three seconds late, to show that until then the panel draws what this browser last heard.
**Artwork** adds an **emblem** and a **ghost** (the lab's own stand-ins), and **Banner** a banner's
**text**, its **colour** and **top only**. **Release** is the plugin's own release (**unsigned**, **signed**, **files nobody
signed**, **altered**, **cannot read**), in the panel's admin alerts and on the System Console's
App check card; and **App check list** is that card's list of verdicts (**some**, **many**,
**empty**, **check off**, **bad digest**, **cannot read**, **refused**). The System Console page has
a **dark** chip of its own, which draws it on Onyx's colours.

**4.1.0** adds the System Console's [push notifications](push.md) card and the alert that goes with
it. **Push status** is the card in each of its states (**some**: eleven phones at four push servers,
working, waiting, failing and refused, with pushes not sent; **empty**; **switch off**; **key not
ready**; **full**; **bad host line**; **cannot read**; **refused**, the 403 for anybody not a system
admin), and **Mattermost’s push** is what Mattermost's own push notifications say (**on**, **off**,
**needs a licence**, **cannot read**), on the card and, while the plugin's switch is on, in the
panel's admin alerts.

The point is that a layout can be **looked at** rather than reasoned about. Three shipped visual
bugs came from reasoning about them; the scroll-snapping problem described under [Up next](up-next.md#up-next)
was found by clicking a button in the lab.

## How it works

```
Broadcast Box  ←── /api/status (poll) ──────────┐
   :8080       ←── /api/whep (viewers' SDP) ────┤  Mattermost server (plugin)
               ←── /api/whep (the recorder) ────┤    ├── polls, attributes streams to users
   UDP 3478    ──→ media, to the recorder ──────┤    ├── sets/clears 🔴 custom statuses
                                                │    ├── keeps each live stream's last minutes (Clips)
                                                │    ├── serves clips, posts shared ones as the sharer
                                                │    └── pushes updates over the WebSocket
                                                └──→ browser: panel + WebRTC player + clips
```

- The server polls `/api/status`, treats a stream as live only when it has at least one video
  track, and pushes a snapshot to clients over the Mattermost WebSocket when something in it that a
  panel shows has changed. The poll's own time does not count, and nor does each stream's bitrate:
  Broadcast Box measures it afresh on every read, so it moves on every poll of a live stream, and no
  panel shows it. Until 3.1.0 it counted, and a 24/7 channel sent the whole snapshot to every
  connected browser on every poll, around the clock. The bitrate is still kept in the snapshot, and a
  push for a real change carries the current one. A change to who is in voice, and a clip written, go
  out between polls without one. The boards' hours, which tick for as long as anybody is on the air,
  are not in the snapshot at all from 3.6.0: a browser reads the boards (`GET /api/v1/boards`) as
  its page starts, when the snapshot says something other than the seconds on them has changed, and
  each minute while somebody looks at them; the seconds ticking push nothing.
  Clients also poll every 30 seconds as a fallback.
- Playback negotiation is relayed through the plugin (`POST /api/v1/whep`), so the browser needs
  no credentials and there is no CORS surface. Only keys that are currently live are relayed, so
  the endpoint cannot be used as an open proxy. **Live media flows directly** from Broadcast Box
  to the viewer over WebRTC — it does not pass through Mattermost.
- **Clips do pass through Mattermost.** While Clips is on, the server opens a playback session of
  its own for each live stream that is not a 24/7 channel, through the same `/api/whep`, and the
  media comes to the Mattermost server over UDP; it keeps the last few minutes on its own disk,
  cuts clips from them, and serves clips to browsers from its own routes
  (`/api/v1/clips/video`). See [Clips](panel.md#clips) and `LOCKDOWN.md`.
- Every route the panel uses sits behind an access gate, which knows who has unlocked the panel and
  who an admin has let in; see [Unlocking Matty](unlocking.md#unlocking-matty).
- The font (Oswald — SIL Open Font License) is embedded in the plugin binary and served from the
  plugin's own endpoint. Out of the box the plugin makes **no external network requests** at
  runtime beyond Broadcast Box and, when voice is on, Mumble; the optional exceptions are
  [Movies and games][movies-and-games-optional], [Twitch][twitch-optional],
  [Steam][steam-optional], [Hytale][hytale-optional], GitHub for [Plugin
  updates][updates-optional] and for **Minimum Matty version** set to the latest release, and
  [UnifiedPush push notifications](push.md), all off by default. **Browsers
  contact somebody other than this server in exactly four cases, all off by default: two for Twitch,
  one for Steam and one for Hytale; the UnifiedPush push servers are reached by the server and by
  phones, and GitHub, for the plugin's updates and for **Minimum Matty version** at the latest
  release, by the server, never by a browser.** (1) The person who presses Connect Twitch and then Continue on Twitch,
  in the window that opens, reaches `id.twitch.tv` and Twitch's sign-in pages; (2) a viewer who
  presses play on a Twitch stream while **Play Twitch streams inside Matty** is on loads Twitch's
  player, which talks to Twitch and its advertising and measurement partners; (3) the person who
  presses Connect Steam and then Continue on Steam reaches Steam's sign-in page at
  `steamcommunity.com`, which loads from other servers, for example Steam's own and Google's
  reCAPTCHA; (4) the person who presses Connect Hytale and then Continue on Hytale reaches Hytale's
  sign-in pages (for example `accounts.hytale.com`, `connect.accounts.hytale.com` and
  `backend.accounts.hytale.com`), which load Cloudflare's Turnstile and, if the person accepts
  Hytale's cookies, Cloudflare's analytics, and report to Hytale's Sentry; Hytale's page may also
  offer signing in with Google, Microsoft, Discord, Twitch or Epic Games, and a person who picks one
  reaches that service too. For (3) and (4), these are examples from each page's own policy, not a
  complete list. In Matty, (3) and (4) happen in Matty's own sign-in window. The Steam profile button
  in a profile popover is an ordinary link, opened only when pressed, like Watch on Twitch.
- With **Show people live on Twitch** on, the server also asks `api.twitch.tv` once a minute who of
  the connected people is live (only while somebody is connected), once a day for their names, and
  once per connection who connected; it gets the application's token, and exchanges and revokes each
  person's one-time sign-in, at `id.twitch.tv`; and it fetches Twitch's preview pictures from
  `static-cdn.jtvnw.net` and relays them. Nothing about Broadcast Box is ever sent to Twitch.
- With **Show people's Steam status** on, the server asks `api.steampowered.com` once a minute about
  the connected people, in one call (only while somebody is connected, and never about a deactivated
  account), with the key in a request header, and once per connection for the new Steam name; and
  it asks `steamcommunity.com` once per connection to confirm the sign-in Steam sent back. With
  **Show people's Hytale names** on, it talks to `connect.accounts.hytale.com` once per connection,
  to exchange the one-time code, read the profile and revoke the token. Statuses live in the server's
  memory, and the panel reads them from `GET /api/v1/accounts`, never from the stream list, which
  reaches locked people too.
- A [hashtag search](unlocking.md#hashtags-in-stream-titles) is the reader's browser asking Mattermost's own
  `/api/v4` — the request its search box makes — on the same server, with the reader's own session.
  So is an [@name](unlocking.md#names-in-stream-titles) being looked up, by Mattermost's own code.
- With the [soundboard](voice.md#soundboard) on, a sound is made in the browser that adds it, checked and kept
  by the server, and sent by the server into Mumble over the connection the roster already uses. Its
  emoji pictures come from Mattermost itself (`/static/emoji/`, `/api/v4/emoji/`).
- With [push notifications](push.md) on, Mattermost's push hook hands the plugin each push it is
  about to send. The plugin answers at once, changing nothing, and its own senders encrypt the
  push for each of that person's registered phones and post it to the push server the phone's
  UnifiedPush app named, under the outbound rule in `LOCKDOWN.md`. No connection from the phone to
  this server is needed to show it.
- A [signed release](#signing-a-release) is checked against two certificates the plugin carries,
  and the [app check](security.md#the-app-check) against Google's attestation roots, a copy of
  Google's list of revoked attestation certificates and a copy of GrapheneOS's boot keys, all
  shipped inside the release: neither check fetches anything from anywhere.
- All CSS is scoped under a single `.vntv` class in one `<style>` element, removed when the
  plugin unloads. Nothing restyles message rendering. Its colours are the Mattermost theme's, read
  from Mattermost's own theme variables, so the panel changes with the theme.

## Native apps (Matty)

Matty, the Android app for Mattermost, and any other native client talk to the plugin through the
same API the panel uses. Since 3.5.0 that API is a written contract: `contract/README.md` says what a
client reads and what it must do, `contract/fixtures/` holds what the server really answers (its own
tests write those files, and Matty's tests read them), and `GET /api/v1/access` says which contract
the server speaks, so that a plugin and an app out of step say so instead of failing strangely.
Read that file first.

The contract is **revision 4**, first spoken by 4.0.0 (`contract/contract.json`), when the plugin's
id became `ceo.villa.matty`. Every route, event and share link moved with the id, so an app written
for revision 3 reaches nothing of 4.0.0; `GET /access`, `GET /me` and the `me` event carry `name`,
what this server calls the plugin, and `artwork`, whether it has an emblem and a No Signal ghost;
and the refusals' sentences say the server's name (`contract/README.md`, "What changed in revision
4"). Matty 0.1.27 is the app for revision 4. Two additions sit on top of it. `release` and `GET /release`,
the plugin's signed release, which the app verifies for itself, cost a client nothing if it does not
read them. `app`, `POST /app/challenge` and `POST /app/attest` are the app check, with which Matty
shows the server, on Android's word, that it is the official build; a client that does not read
them loses nothing while the server's **Check the Matty app** setting (under **App check**) is
`off` or `report`, but under `require` a Matty that has not proved itself is refused with `403` and `app_unverified` on every route except
`GET /access`, `GET /me`, `GET /release`, the two app routes, from 4.1.0 `DELETE /me/push` and,
from 4.3.0, `POST /voice/leave` (see
[Signed releases](security.md#signed-releases) and [The app check](security.md#the-app-check)).
From 4.1.0 a third: `push` in `GET /access` and the `/me/push` routes, with which Matty registers
for [push notifications](push.md) and the server sends them; a client that does not read them loses
nothing (`contract/README.md`, "Push notifications"). Those routes are not exempt from the app
check, but for `DELETE /me/push`: under `require`, a Matty that has not proved itself cannot
register, and can always take its registration back.
From 4.3.0 a fourth: `voice` in `GET /access`, the `/voice` routes and the data channel `voice`,
with which Matty shows the Mumble server's channels and people talk in them from their phones
([Talking from Matty](voice.md#talking-from-matty)); a client that does not read them loses nothing
(`contract/README.md`, "Voice"). Those routes are not exempt from the app check either, but for
`POST /voice/leave`: under `require`, a Matty that has not proved itself cannot join, and can always
leave.
From 4.4.0 two more. `update` in a system admin's `GET /access` and `updateAlert` in their `GET /me`
name a newer release of the plugin ([Plugin updates](updates.md); `contract/README.md`, "Plugin
updates"). `minimum` in the `app` of `GET /access` and `GET /me` is the oldest Matty this server
answers, while an admin has set one ([A minimum Matty version](security.md#a-minimum-matty-version);
`contract/README.md`, "A minimum Matty"). A client that does not read them loses nothing, but a
Matty older than that minimum, or whose User-Agent says no version, is refused with `403` and
`app_outdated` on the same routes as `app_unverified`, before the app check is asked, and its voice
session ends when the minimum comes into force.
From 4.5.0 the minimum may be the latest release of Matty, which the server asks GitHub for, and is
in `minimum` once the server knows it. With the check at `require`, a session whose verdict carries
an older version than the minimum, or none, is refused with `app_unverified` too, and `GET /access`
and `GET /me` say `none` to its Matty while that Matty says a version that meets the minimum, so
that it proves itself again ([A minimum Matty version](security.md#a-minimum-matty-version)).

The rest of this section is the part about people's Steam and Hytale accounts, new in 3.4.0.

**Ground rules.**

- **Base**: `<Site URL>/plugins/ceo.villa.matty/api/v1`.
- **Every request the app itself decides to make** carries `Authorization: Bearer <session token>`
  and `X-Requested-With: XMLHttpRequest`. A Bearer request needs no CSRF token. Never put the token
  in a query string. Nothing the app loads as a picture, as video or as a download sends
  `X-Requested-With`: that header is how the server tells a request a client meant from one a post
  made it make (`contract/README.md`, "What a client must do").
- **Sessions.** Anything that **adds** a connection needs an interactive session, a password or single
  sign-on sign-in: a personal access token is refused with `interactive_only`. Reading and
  disconnecting work with any session.
- **Answers** are JSON, never a redirect, with `Cache-Control: no-store`. A refusal is any status
  other than 2xx with `{"error": "<a sentence a person can read>", "code": "<a stable word>"}`, and
  `"retryAfterMs"` with every "too often" (from 3.6.0 on every route: `contract/README.md`, "A
  refusal"). Show `error` as it is sent: the sentence is the server's to word.
- **Access.** `GET /access` says who the reader is (`access`: `linked`, `letIn` or `locked`), and
  so does `GET /me`, among everything else about them. Ask `GET /accounts` only when it is `linked`
  or `letIn`. A locked reader gets 403 `locked`, and must be shown nothing about anybody's Steam or
  Hytale, in a profile included.

**Routes.**

| Method and path | Who | Body | Answer | Refusal codes |
| --- | --- | --- | --- | --- |
| `GET /access` | anybody signed in | — | `{"access", "configured", "contract", "version", "name", "artwork", "release", "app"}`, from 4.1.0 `"push"`, from 4.3.0 `"voice"`, and from 4.4.0 `"minimum"` in `"app"` and, for system admins, `"update"` | — |
| `GET /me` | anybody signed in | — | 3.3.0's fields, plus `steam` and `hytale` (below), and from 4.0.0 `name`, `artwork`, `release` and `app`; from 4.1.0, for system admins, `pushAlert`; from 4.3.0, `voiceTalkAlert`; from 4.4.0, `updateAlert` | `store` |
| `GET /banners` | anybody signed in | — | from 3.7.0, `{"version", "people": {"<user id>": {"v", "type", "crop"?}}}`, everybody's profile banner, and from 3.8.0 the part of it that shows (`contract/README.md`, "Profile banners"); `304` to a client that sends back the `ETag` of the answer it holds | `store` |
| `GET /banner?user=&v=` | anybody signed in, loaded as a picture | — | the picture's bytes, as they were sent | `not_found` |
| `POST /me/banner` | regular members (not guests, not bots), 6 in 10 minutes | the picture, `Content-Type: image/jpeg`, `image/png` or `image/gif`; from 3.8.0 `?crop=x,y,w,h` may say the part to show | `201 {"v", "type", "crop"?}` | `bad_image`, `too_large`, `bad_crop`, `forbidden`, `rate_limited`, `store` |
| `PUT /me/banner/crop` | regular members (not guests, not bots), 30 in 10 minutes, from 3.8.0 | `{"crop": [x, y, w, h]}` or `{"crop": null}`, with an optional `"v"`: the picture the part was chosen for (`contract/README.md`, "Profile banners") | `{"v", "type", "crop"?}`, the banner one has now with its new crop | `bad_crop`, `not_found`, `conflict` (the `v` is not the banner now; nothing changed), `forbidden`, `rate_limited`, `store` |
| `DELETE /me/banner` | anybody signed in | — | `{"ok": true}`, also when there was none | `store` |
| `DELETE /admin/banner?user=` | system admins | — | `{"ok": true}`, also when there was none | `forbidden`, `bad_request`, `store` |
| `GET /accounts` | linked or let in | — | everybody's Steam status and Hytale names, and from 3.6.0 what each person is doing (`activity`), in one request; `304` to a client that sends back the `ETag` of the answer it holds | `locked` |
| `POST /steam/app-connect`, `POST /hytale/app-connect` | linked | `{}` | `{"url", "finishAt", "expiresIn": 600}` | `locked`, `watch_only`, `interactive_only`, `off`, `setup`, `blocked`, `full`, `rate_limited`, `store` |
| `POST /steam/app-finish`, `POST /hytale/app-finish` | linked | `{"query": "<the raw query of the intercepted address>"}` | `{"ok": true, "account": <as /me's>}` | `locked`, `watch_only`, `interactive_only`, `bad_request`, `expired`, `other_session`, `cancelled`, `provider_error`, `busy`, `scope`, `no_profile`, `off`, `blocked`, `full`, `store` |
| `DELETE /steam/link`, `DELETE /hytale/link` | anybody signed in | — | `{"ok": true}`, also when nothing was connected | `store` |
| `POST /steam/notice`, `POST /hytale/notice` | anybody signed in | — | `{"ok": true}`: the takeover notice is read | `store` |

`POST …/connect-ticket` and the `connect` and `callback` pages are the panel's, for its browser
window, and of no use to an app: the connect page needs the browser's own Mattermost session.

**Codes** (Steam's words; Hytale's are the same with its name). Where a sentence says Matty, a
server that has given the plugin a name of its own says that name instead:

| Code | Status | `error` |
| --- | --- | --- |
| `locked` | 403 | Matty is locked. Unlock it with your bearer token. |
| `watch_only` | 403 | You are let in to watch only. Link your stream to do that. |
| `interactive_only` | 403 | Connect Steam while signed in with your password or single sign-on, not a personal access token. |
| `off` | 404 | Connecting Steam accounts is switched off on this server. |
| `setup` | 503 | An admin needs to finish setting up Steam. (A system admin is told what is missing.) |
| `blocked` | 403 | An admin has turned off Connect Steam for your account. |
| `full` | 409 | This server already has 100 Steam accounts connected, which is as many as it keeps. |
| `rate_limited` | 429 | Too many tries. Wait a few minutes and press Connect Steam again. |
| `bad_request` | 400 | Could not read the request. |
| `expired` | 400 | This Steam sign-in was already used, or took longer than 10 minutes. Start again. |
| `other_session` | 403 | This sign-in was started somewhere else. Start again from here. |
| `cancelled` | 400 | You cancelled on Steam, so nothing was connected. |
| `provider_error` | 502 | Steam did not confirm the account just now. Try again in a minute. |
| `busy` | 503 | Steam asked Matty to wait. Try again in a couple of minutes. |
| `scope` | 502 | Hytale has not approved this server's application to read game profiles yet. (Hytale only) |
| `no_profile` | 409 | That Hytale account has no game profile to show. (Hytale only) |
| `store` | 503 | Matty could not save that just now. Try again in a moment. |

Two refusals can come from any of these routes but `GET /access` and `GET /me`: from 4.4.0
`app_outdated` (403), to a request from a Matty older than the minimum an admin has set, and
`app_unverified` (403), to a request from Matty whose session has not passed the app check while an
admin has it set to refuse. Both are in `contract/README.md`, "A refusal".

**`/me`'s two new fields**, the same shape for both. Each is absent when that service is switched
off and the reader has nothing there:

```jsonc
"steam":  {"available": true, "connected": true, "name": "Dave",
           "url": "https://steamcommunity.com/profiles/76561197960287930", "hidden": false},
"hytale": {"available": true, "connected": true, "name": "Steve_Hytale"}
// not connected:           {"available": true}
// switched off, connected: {"available": false, "connected": true, "name": "Dave", …}  -> offer Disconnect only
// blocked:                 {"available": true, "blocked": true}                         -> show nothing for it
// taken over:              {"available": true, "lost": {"name": "Dave", "at": 1794430000000}}
```

`hidden` is Steam's last word that the profile is not public, so everybody else sees its owner as
offline. A system admin also gets `problem`, a sentence saying what is wrong with that service.

**`GET /accounts`**, for a profile, a member list and avatars, one request whatever the number of
people, served from the server's memory:

```jsonc
{"v": "3f2a9c01b7de",
 "steam":  {"on": true, "fresh": true},
 "hytale": {"on": true},
 "people": {
   "<Mattermost user id>": {"steam":  {"name": "Dave", "url": "https://steamcommunity.com/profiles/76561197960287930",
                                       "state": "online", "inGame": true, "game": "ELDEN RING"},
                            "hytale": {"name": "Steve_Hytale"},
                            "activity": {"kind": "streaming", "title": "ELDEN RING", "source": "steam"}},
   "<Mattermost user id>": {"hytale": {"name": "Bob_the_Builder"}}
 }}
// v: changes only when people does
// state: "online" | "busy" | "away" | "offline" | "" (Steam's last answer is not fresh, or left them out:
//        show names, not statuses)
// inGame and game: only while Steam reports a game for a public profile; game may be "" with inGame true
// fresh: Steam was read in the last five minutes; always sent, false while Steam is off
// activity (from 3.6.0): the line under a name, "playing" or "streaming" (their own stream is of that
//        very game, or, from 3.6.1, of the movie or show they tagged in What's on?), decided by the
//        server: print it, and compare no game names yourself (contract/README.md, "What somebody
//        is doing")
// people is {} when nobody is connected, never null; a service switched off says "on": false
```

**Connecting in the app's own sign-in window.** A browser opened from the app carries no Mattermost
session, so the panel's way of connecting cannot be used from it; instead the app hosts the sign-in
itself:

1. `POST /{steam,hytale}/app-connect` → `url` and `finishAt`.
2. Open `url` in a sign-in window inside the app (a WebView): https only, certificate errors refused,
   no file or content access, no mixed content, no JavaScript interface, no extra windows, the
   current host shown in its top bar, and plain `loadUrl(url)` with **no headers**, so no Mattermost
   token or cookie is ever in that window.
3. Watch every main-frame navigation (in `shouldOverrideUrlLoading`, and in `shouldInterceptRequest`
   too, for one a POST starts): an address whose scheme, host, port and path equal `finishAt`
   exactly is **never loaded**. Take its raw query, close the window, and wipe its cookies and
   storage.
4. `POST /{steam,hytale}/app-finish {"query": "<raw query>"}` → the account, or a code to word.
5. If the person closes the window instead, do nothing: the sign-in lapses in ten minutes.

A sign-in started this way can be finished only by the same session through `app-finish`. A browser
that reaches the callback with it is refused and the sign-in is spent, so somebody who starts one
and sends its address to someone else gets nothing, and nobody can finish anybody else's. Nothing
more is registered with Steam or Hytale for this: the app catches the same return address the web
uses, and Hytale's client secret and the sign-in's PKCE verifier stay on the server. A browser-based
alternative, a link the phone's browser opens with no Mattermost session behind it, was looked at
and not built: anybody could post such a link and attach the account of whoever opened it to their
own.

**Events** (the names after `custom_ceo.villa.matty_`):

| Event | To | Do |
| --- | --- | --- |
| `accounts` | everybody, with an empty payload | If the reader may watch and a screen of the plugin's or a profile is showing, `GET /accounts` (at most once a second); otherwise read it the next time one shows. It says only that something changed. The first change of a quiet ten seconds is told at once; there is never more than one in any ten. |
| `me` | one person, or everybody after an admin's Delete all, a change to the server's name or artwork, to the app check's setting or what it accepts, or to the minimum Matty version in force | What the server holds about the reader may have changed: their access (a link, an unlink, a let-in), or their own Twitch, Steam or Hytale connection (connected, disconnected, blocked or taken over); or, sent to everybody, the server's name for the plugin or its artwork, or the app check's rules (a Matty then proves itself again if its verdict no longer counts), or the oldest Matty this server answers. It carries `name` and `artwork` as `GET /access` gives them, and nothing about anybody. Ask again: `GET /access`, and for a client that shows connections, `GET /me`. It never says what changed. |

From 3.6.0 `me` is the one event about a reader's own state: until then there were three, `access`,
`twitch_link` and `account_link`, which carried nothing either and all meant this. `streams`
carries, beside the snapshot, its `tag`: the same tag `GET /streams` sends as its `ETag`, for the
client's next conditional read. The boards are not in the snapshot from 3.6.0: `GET /boards`, read
when somebody looks at a board or a profile (`contract/README.md`, "Awards and the boards").

[movies-and-games-optional]: up-next.md#movies-and-games-optional
[twitch-optional]: up-next.md#twitch-optional
[steam-optional]: up-next.md#steam-optional
[hytale-optional]: up-next.md#hytale-optional
[updates-optional]: updates.md
