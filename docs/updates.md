[The Matty Plugin manual](README.md)

# Plugin updates

## What it does

Matty Plugin can look on GitHub for a newer release of itself and tell the system admins when there
is one, and a system admin can then install it from the System Console or from Matty, which asks
first. It is one setting, **System Console > Plugins > Matty > Updates > Look for updates**:

- **Off**, the default. Nothing is asked of GitHub, nobody is told anything, no daily job runs, and
  no bundle is handed out. A server that has never had this set is off.
- **On.** The server looks once a day, once when the setting is switched on, and whenever an admin
  presses **Check now** on the card under it. When the newest release is newer than the one running,
  system admins see "Matty Plugin 4.5.0 is out." among the panel's alerts, with a link to its page;
  Matty 0.1.32 and later say "Matty Plugin 4.5.0 is available" on the Plugins screen of a system
  admin; the card says so; and the plugin's bot sends each system admin one direct message about it.

Installing is always a system admin's own press of **Install**, on the card or in Matty. The plugin
never installs anything: Mattermost cannot replace a plugin at that plugin's own request, so the
plugin fetches and checks the bundle, and the admin's own session hands it to Mattermost (below).

A release counts as newer when its version is, number by number: 4.10.0 is newer than 4.9.0. Drafts
and pre-releases never count. Only the version and whether the release has a bundle for this
server's platform are read from GitHub's answer; nothing else in it is shown to anybody or written
to the log. The page that is linked is one the plugin builds itself,
`https://github.com/vvilla-01/matty-plugin/releases/tag/v<version>`, never an address from the
answer.

## What is asked of GitHub

While the setting is on, and only then:

- **Looking** is one request from the Mattermost server to
  `https://api.github.com/repos/vvilla-01/matty-plugin/releases/latest`: HTTPS only, at most 15
  seconds, at most 256 KB of answer, with the plugin's usual User-Agent,
  `MattyPlugin/<version> (Mattermost plugin; +https://github.com/vvilla-01/matty-plugin)`, no
  cookie, and nothing else about this server. In a cluster the daily look runs on one node, and
  switching the setting on looks once, on one node. **Check now** looks at most once a minute for the
  whole server; pressed again sooner, it says "Somebody looked less than a minute ago. Try again in
  a minute.", and the answer gives the time left of that minute. A 404 means there is no release
  yet, which is no trouble. A request that fails is logged by its kind alone (a timeout, the name
  lookup, the connection, TLS, or another). When the store does not answer as the daily look or
  the look of switching on starts, that is logged too, and the node looks anyway. A status GitHub
  answers with is said on the card, not in the log.
- **Fetching a bundle** happens only when a system admin presses **Install**. The server downloads
  the bundle for its platform, `ceo.villa.matty-<version>-<platform>.tar.gz`, from
  `https://github.com/vvilla-01/matty-plugin/releases/download/v<version>/`, an address the plugin
  builds itself. GitHub sends the download on to where it keeps release files; every redirect must
  be HTTPS, to `github.com` or a host under `githubusercontent.com`, and there may be at most five.
  The download is at most 64 MB, takes at most two minutes, and goes through the proxy the
  server's environment names, as the title lookups do.

A firewall that limits what the Mattermost server may reach must let it reach `api.github.com` on
port 443 for the look, and `github.com` and `*.githubusercontent.com` on port 443 for **Install**.
[LOCKDOWN.md](../LOCKDOWN.md) lists every address the plugin may reach.

## Installing a newer version

**Install 4.5.0** is on the card while a newer version is known and it can be installed from here.
A press asks first: "Install Matty Plugin 4.5.0? The plugin restarts, which takes a few seconds.",
and, when phones are in voice through Matty, how many, since a restart drops them for a few seconds
(each joins again by itself). Then:

1. **"Getting and checking the bundle…"** The server downloads the bundle into a new private folder
   and unpacks it there. It refuses anything but plain files and folders (a link of any kind), a
   name that is not made of letters, digits, `.`, `_` and `-` in parts joined by `/` (which leaves
   out an absolute path and a `..`), a part longer than 255 bytes or a name longer than 512, names
   longer than 1 MiB in all, a path twice, anything outside its one folder `ceo.villa.matty`, more
   than 4,000 entries and more than 256 MB. Then it checks the folder as the plugin checks itself,
   against Matty's two certificates built into it: both signatures over the manifest, every file's
   SHA-256, for exactly the version that was looked for and this platform, and no file the manifest
   does not list. Its `plugin.json` must say `ceo.villa.matty` and that version.
2. **What is handed over is what was checked.** The server packs a fresh archive from the checked
   folder, each file hashed again as it is packed, never the bytes that were downloaded. Its modes
   are its own: the server program `plugin.json` names is executable and every other file is not,
   whatever the download said, and only folders that hold a file are packed. The private folder is
   removed before the archive is sent, and nothing of it is kept.
3. **"Handing it to Mattermost…"** The browser uploads that archive to Mattermost's own plugin
   upload, as **System Console > Plugins > Plugin Management** does, with the admin's own session.
   Mattermost puts the new version in place of the running one and starts it.
4. **"Matty Plugin 4.5.0 is installed. Reload this page to see its panel."**

In Matty the same happens from the Plugins screen: the app gets the checked bundle from this server
and uploads it to this server, and nothing goes anywhere else.

A bundle is asked for at most once a minute for the whole server, and prepared one at a time on
each node: asked again within the minute, the server says "A bundle was asked for less than a
minute ago. Try again in a minute." with the time left of that minute in the answer, and while one
is being prepared on that node, "A bundle is being prepared just now. Try again in a minute." A
bundle that failed still counts as the minute's.
Anything that fails leaves the running plugin as it was: a download that does not finish, or a
bundle that is not as signed, is said on the card in red, in one of a fixed set of sentences that
hold no word of the download, and the buttons come back. If the plugin stops in the middle, the
answer is "The plugin stopped while it got the bundle. Try again once it is back." The card uploads
nothing but a whole bundle of the version it asked about: an answer that is not one is "The bundle
did not arrive whole. Try again in a moment.", and when another look found a newer version
meanwhile, it says "A newer version, 4.6.0, was found meanwhile. Press Install again in a minute."
and **Install** names that version. The bundle it refused counts as the minute's, so the next press
works a minute later.

A browser that leaves in the middle does not stop the server's work: it ends by itself, the
download giving up after two minutes at most, and leaves no folder behind; meanwhile another
**Install** on that node is told one is being prepared. A private folder that a
server stopping in the middle left is removed within about ten minutes of the plugin's next start.

## What Install needs

The card offers **Install** only when all of this holds, and otherwise says which is missing:

1. **This plugin is a signed release** (see [Signed releases](security.md#signed-releases)). A
   development build, or anybody's own build, does not install releases: "This copy of Matty Plugin
   is not a signed release, so it does not install releases. Install 4.5.0 by hand from its page."
2. **The release has the bundle for this server's platform**, such as
   `ceo.villa.matty-4.5.0-linux-amd64.tar.gz`: "Matty Plugin 4.5.0 has no bundle for this server's
   platform (linux-amd64)." The card names the platform.
3. **Mattermost takes plugin uploads:** **Enable Plugins** and **Enable Plugin Uploads** on, and
   **Require Plugin Signature** off, all under **System Console > Plugins > Plugin Management**.
   That is what Mattermost's own upload asks of a plugin it did not sign. The plugin reads these when
   it starts and whenever Mattermost's configuration changes.

Only a system admin sees the card, and only a system admin is handed a bundle.

## The messages

The plugin's bot sends them, in each system admin's direct messages with it: at most twenty system
admins, people who are active, and never a bot. Each admin gets one message for a version, however
many nodes the server has: "Matty Plugin 4.5.0 is out. To install it, a system admin opens System
Console > Plugins > Matty > Updates. What is new:" and the page. When not one admin could be
reached, the next look tries again.

With no bot (it could not be made), nobody is messaged, and the alert in the panel and the card
still say it.

## The card

**System Console > Plugins > Matty > Updates > Status** says which version this server runs and
on which platform (such as linux-amd64), the newest release the last look found and when, whether
it is newer and its page, **Install** or why it cannot be installed from here, and the last thing
that went wrong, such as "GitHub could not be reached." or why the last bundle could not be had.
That line goes at the next look that works, or when the next bundle is handed out. **Check now**
looks at once; it is off while the setting is off, and saving the setting is needed first.
**Refresh** reads the card again.

## When a new version does not start

If Mattermost lists Matty with an error after an update, or the plugin does not come back, install
the previous release by hand: download its bundle from its page on GitHub (the release before the
new one), and upload it in **System Console > Plugins > Plugin Management**, which replaces the
version that did not start. Every setting and everything the plugin keeps stays as it was. Leave the
new version's **Install** alone until a release that starts is out.

## Trying it on a development server

The stand-in build (`make standin-linux`) never asks GitHub. In its place it asks the stand-in
beside it (`build/fakebb`, on `127.0.0.1:7788`), which answers as GitHub does from a folder named on
its command line, `fakebb -releases <folder>`, and answers 404, as GitHub does with no release,
without one. `build/make-standin-update.sh <version> <folder>` makes such a folder outside this
tree: the stand-in build of a version you choose, in the release layout, signed with throwaway keys
it makes for the purpose, checked as the plugin checks a bundle before it hands it out, and the two
certificates beside it, which are the ones the stand-in build checks a bundle against. It refuses
a folder inside this tree, the folder of the real signing keys, and a folder that is not empty and
that it did not make. The stand-in build hands out a bundle without being a signed release itself,
and says the version it was made to be on the card, in `/access` and in `/me`. A release build has
none of this.
