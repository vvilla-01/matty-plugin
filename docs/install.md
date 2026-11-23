[The Matty Plugin manual](README.md)

# Getting started

Matty Plugin is the companion to Matty, the Android app for Mattermost: your own Broadcast Box's live
streams in a panel beside chat, in Mattermost on the web and the desktop, and in Matty. This chapter
is for an admin setting it up on a server that has never had it.

Mattermost lists it as **Matty**, and that is also what people see it called until an admin gives it
a name of their own (see [Name and artwork](#name-and-artwork)). The sentences quoted in this manual
use the default; on a server with a name of its own, that name stands where they say Matty.

## What it does

- **One icon, one panel.** Its icon in the App Bar (and in the channel header wherever there is no
  App Bar) opens a right-hand sidebar listing every live stream. Out of the box the icon is Matty's,
  a round speech bubble.
- **Locked until you unlock it.** The panel opens on a lock screen for everybody, system admins
  included. A streamer unlocks it once with their own **bearer token**, the token their streaming
  software publishes with. A system admin can let anybody else in to watch. Outside the panel nothing
  is hidden, and the server holds to all of it. See [Unlocking Matty](unlocking.md#unlocking-matty).
- **Plays inside Mattermost.** Video plays in the panel over WebRTC/WHEP. No new tab, no
  Basic Auth prompt, no second password to share.
- **Switch streamers in one click.** Every live stream is a card under **Live now** showing a recent
  still of what is on it, with the streamer's face, name and title along the bottom; clicking one
  swaps the player. A 24/7 channel's card shows a picture an admin chose.
- **Or watch up to four at once.** Once something is playing, each card has a **+** that adds that
  stream *alongside* it, so two people's POVs sit side by side. Exactly one tile has sound at a time.
- **Back after a reply.** A thread that takes the panel's place hands it back once your reply is
  posted, or as soon as you close it, and the stream keeps playing in a small player in the corner
  of the channel meanwhile, with nothing reconnecting. See
  [When a thread takes the panel](panel.md#when-a-thread-takes-the-panel).
- **Up next.** Planned streams sit in a row of cards under the set, soonest on the left, each one
  in the reader's own timezone and carrying the streamer's face. The row is there only while
  something is planned. Everybody who can open the panel sees it. For anybody with their stream
  linked, the calendar in the bar at the bottom of the panel is the way to plan one. Arrows move
  the row one plan at a time. It shows on the streamer's profile too. Scheduling one is a calendar
  and a clock, with nothing to type.
- **Who is in voice.** One row at the top of the panel listing whoever is on the Mumble server,
  by channel, and no row at all when nobody is in there.
- **Only some Mumble channels, if you want.** An admin can name the channels Matty and the panel
  show; the rest, and whoever is in them, are not shown. See
  [Showing only some channels](voice.md#showing-only-some-channels).
- **Talking from Matty.** If an admin switches it on, the Matty app lists the Mumble channels, as
  Discord lists its voice channels, and people who may watch join them and talk from their phone,
  through this server, on one UDP port. Off by default; see [Talking from Matty](voice.md#talking-from-matty).
- **A soundboard for whoever is in Mumble.** If an admin switches it on, somebody linked who is in
  Mumble gets a speaker in the bar at the bottom of the panel that opens a board of short sounds,
  each an emoji over a name. Pressing one plays it into the Mumble channel they are in. Greyed out while they are muted,
  with a wait between sounds and one sound at a time. Off by default; see [Soundboard](voice.md#soundboard).
- **Mumble chat, bridged.** The same connection carries text both ways, so the plugin replaces a
  Matterbridge instance instead of sitting beside one.
- **Birthdays.** Posted once on the morning, from a list only admins can edit — and a 🎂 next to
  their name for the day, which beats the live dot if they are streaming on it.
- **Movie or game.** A planned stream can say which, and the card carries the icon. If an admin
  switches lookups on, the title box finds the real movie, show or game as you type, and the card
  wears its art with a little profile on hover.
- **Live on Twitch too.** If an admin switches it on, a streamer who has unlocked Matty can
  connect their Twitch account. While they are live on Twitch they get the red dot, a Live now card
  and airtime, and with a second switch their stream plays in the panel in Twitch's own player. Off
  by default. See [Twitch (optional)](up-next.md#twitch-optional).
- **Plans you can change and join.** A pencil on your own plans opens them to change, and streamers
  can **Join** somebody else's, with everybody's faces on the card. Planning, changing and joining
  all need your stream linked; cancelling your own plan, or leaving one, never does.
- **Links, hashtags and @names in stream titles work.** An address in a streamer's title is a link,
  under the player and on their Live now card, a hashtag such as `#arma3` finds the messages tagged
  with it in Mattermost, shown over the panel so the stream keeps playing, and an `@dave` opens
  Dave's profile card. See [@names in stream titles](unlocking.md#names-in-stream-titles).
- **Share a plan into a channel.** A planned stream can be posted into the channel you are in, as
  you, as a card that everybody there reads in their own time zone, with **Join** before it starts
  and **Watch** once it is live. See [Sharing a plan](up-next.md#sharing-a-plan).
- **A television that is always on.** The panel boots when it opens and the set switches on like a
  CRT. With nothing tuned in it shows analogue snow and a NO SIGNAL plate, which now and then
  catches a ghost picture on a server that has uploaded one; changing channel plays the
  static-and-roll until the new picture arrives, and closing a stream switches its set off.
- **Controls on the picture, not under it.** Point at a stream and a power switch fades up in the
  middle, with sound and full screen along the bottom. Under the picture there is only the
  streamer's face, their name and what they are streaming — scrolling if it is too long for the line.
- **Looks like Mattermost.** The panel takes each person's own Mattermost theme: its ground, its
  words, its lines and its accent, with stock shapes. Only LIVE keeps a red of its own. The bar at
  the bottom of the panel is the twin of Mattermost's message box beside it: the same height, edges
  and colours.
- **Made your own.** A server can give the panel its own name, App Bar icon, a faded emblem in the
  panel's corner, a No Signal ghost, a banner and a Mattermost theme for everybody. Out of the box it
  has none of them but Matty's icon. See [Name and artwork](#name-and-artwork).
- **The settings, in a file.** One button backs up every setting on the page; another puts them
  back.
- **One theme for everybody, if you want it.** An admin can paste in a Mattermost theme that new
  accounts get automatically, and existing ones when the admin asks.
- **Medals spelled out on a profile.** Somebody's awards appear under their name in the profile
  popover — the artwork, what the place is called, and how long it took. Months already won stay
  there too: **Monthly champion ×2**, and which two.
- **A trophy in the sidebar header.** It opens the **Trophies** card over the whole window. Its
  **Streamers** tab has the airtime boards, every streamer listed rather than just the podium (the
  medals stay at three), and under them everybody's **monthly medals**: how many months each person
  finished first, second and third.
- **Top Snoopers.** The Trophies card's **Snoopers** tab ranks people by the hours they spend
  watching other people's streams in the panel, this month and all time. Only people who have
  linked their stream are counted. Whoever is first all time is the **Top Snooper**, and wears gold
  binoculars beside their name wherever the airtime medals go; see [Top Snoopers](awards.md#top-snoopers).
- **Choose what shows by your name.** The Trophies card's **You** tab lets anybody linked pick up
  to four of their awards, in their order, to show beside their name, or leave it on **Automatic**,
  which picks for them. See [Awards by your name](awards.md#awards-by-your-name).
- **Biggest fans.** Each linked streamer's biggest fan is whoever has watched them most in the
  panel, all time, once it is an hour. Counted only while the Live now cards show who is watching;
  there is no opting out (since 3.2.1), only the admin's switch. See [Biggest fans](awards.md#biggest-fans).
- **Achievements.** Coins for milestones: hours streamed, hours spent watching other people's
  streams, months on the podium, thumbs up on clips, and hours on the 24/7 channels an admin names
  (none out of the box). Worked out from the hours the plugin already keeps, so they
  are there from the first day. See [Achievements](awards.md#achievements).
- **Clips.** A ✂ on a playing stream keeps its last two minutes (the **Clip length** an admin
  sets). You can trim it, name it, publish
  it, give it a thumbs up, and share it into a channel as a card, which carries its thumbs up too.
  Whoever's clips have the most upvotes is the **Best Clipper**. Making, upvoting and sharing clips
  is for people who have linked their stream; people an admin has let in can watch them. It is on
  unless an admin switches it off, and while it is on the server records every live stream that is
  not a 24/7 channel. An admin can also let linked people **download** clips (off unless switched
  on), and a streamer can say no for clips of their own stream. See [Clips](panel.md#clips).
- **Set your own stream title.** Once you have linked your stream, click the title in the panel's
  bottom bar and type. It writes straight to Broadcast Box, so there is no need to visit its web
  page at all.
- **Say what is on.** Beside the Stream title, **What's on?** lets a linked streamer say which
  movie, show or game they are streaming right now — the stream nobody planned included. It shows
  on their Live now card, under their picture and in their profile, and clears itself once their
  stream has ended. See [What's on?](unlocking.md#whats-on).
- **Everyone sees every stream.** The panel deliberately offers no way to hide yourself, and
  saving a note re-lists you if Broadcast Box had you hidden.
- **A red dot next to live streamers.** When someone who has linked their stream key goes
  live, the plugin sets a 🔴 custom status on their account, so the dot shows up next to their
  name in posts, the direct-message list and member lists. Their previous custom status is
  restored when they stop.
- **Always-on channels are handled.** Unattended 24/7 channels are listed and tagged `24/7`,
  sorted below people, and never touch anyone's status.
- **Made for Matty.** Matty, the Android app, has the panel's streams, clips and plans too, and
  shows this server's name and icon. It checks that the plugin is a signed release, and the server
  can check that a phone's Matty is the official build. See
  [Signed releases](security.md#signed-releases) and [The app check](security.md#the-app-check).
- **The oldest Matty the server answers.** An admin can name a minimum Matty version; an older
  Matty is refused what it asks the plugin for, and Matty 0.1.32 and later close this server with
  a page asking to update. Blank by default. See [A minimum Matty version](security.md#a-minimum-matty-version).
- **Notifications without staying connected.** If an admin switches push notifications on, Matty
  0.1.28 and later can notify through a UnifiedPush app on the phone, such as ntfy, whenever
  Mattermost would notify that person, without keeping a connection to this server. Each push is
  end-to-end encrypted. Off by default. See [Push notifications](push.md).
- **Word of a newer release of the plugin.** If an admin switches Look for updates on, the server
  looks on GitHub for a newer Matty Plugin and tells the system admins, who install it from the
  System Console or from Matty. Off by default. See [Plugin updates](updates.md).

## Install

1. In Mattermost, go to **System Console > Plugins > Plugin Management** and set
   **Enable Plugin Uploads** to `true`. (On a hardened install, turn this back off afterwards.)
2. Download the bundle from the repository's
   [Releases](https://github.com/vvilla-01/matty-plugin/releases),
   `ceo.villa.matty-<version>-linux-amd64.tar.gz`, upload it there and click **Enable**.
3. Go to **System Console > Plugins > Matty**, set the **Broadcast Box URL** and save.
4. Under **Access** on the same page, let in anybody who should watch without a bearer token of their
   own. Streamers unlock the panel themselves with their token; see
   [Unlocking Matty](unlocking.md#unlocking-matty).
5. If you like, give it your server's own name and pictures under **Name and artwork**; see
   [Name and artwork](#name-and-artwork).

That is the whole setup. Everything else has a working default.

A new server starts quiet and neutral. Until the Broadcast Box URL is set, the plugin makes no
requests at all, the panel says Broadcast Box is not set up yet, and nobody can unlock it with a
token. There is no banner, no emblem, no No Signal ghost and no theme until an admin adds them, no
24/7 channel, and every optional service (Mumble, the soundboard, the chat bridge, birthdays, movie
and game lookups, Twitch, Steam, Hytale, push notifications, plugin updates) stays off until it is
switched on. The plugin makes its bot, `@matty-bot` (display name Matty), when it starts.

It needs **Mattermost 11.11.0 or later**: Matty Plugin supports only the latest Mattermost. Its
panel runs on the React that Mattermost 11.11 puts on the page (see
[Build from source](building.md#build-from-source)). An older server refuses to start it: Mattermost
reads `min_server_version` from `plugin.json` and stops with "plugin requires Mattermost 11.11.0".

A bundle from the Releases page is a signed release: it carries two signatures over a list of its
files, made with the same two keys Matty, the app, is signed with (RSA and the post-quantum
ML-DSA-87), and the plugin checks its own files against them each time it starts.
The server log then says "Matty is a signed release"; a bundle somebody built themselves says it is
not one, which is no fault, and files that do not match their signatures are told to system admins.
**System Console > Plugins > Matty > App check** says the same at the top of its card, with the
manifest's SHA-256 to compare with the one on the release's page. It shows which release the
server presents, not what code it runs; see
[Signed releases](security.md#signed-releases).

### Broadcast Box URL

Point this at Broadcast Box **directly**, not at the public site:

```
http://192.168.1.20:8080
```

The Mattermost server talks to Broadcast Box server-to-server. Going direct skips the reverse
proxy, its HTTP Basic Auth, its rate limiting and hairpin NAT. Browsers never use this address or
Broadcast Box's HTTP API: the plugin relays each playback request through its own endpoints. The
picture and sound themselves then come to each viewer's browser over UDP, straight from Broadcast
Box's media port, so Broadcast Box sees viewers' addresses; see `LOCKDOWN.md`.

Over plain `http://`, people's stream keys travel unencrypted between the two servers (on linking,
on opening the panel and on saving a title), so keep that path on a network you trust, or give
Broadcast Box an `https://` address. The plugin says so in the server log when the URL is
`http://` to another machine.

If you must go through the public hostname instead, fill in the **HTTP Basic Auth** username and
password as well. In that case `/api/whep` must *not* be behind Basic Auth, because WHEP already
uses the `Authorization` header to carry the stream key and the two cannot coexist. Clipping will
not work that way either: the Clips recorder takes media only from the addresses this URL's host
has, and those are the proxy's, which are not Broadcast Box's unless both run on one host. See
[When clipping does not work](panel.md#when-clipping-does-not-work).

## Name and artwork

Out of the box the plugin wears no community's name or look: it is called Matty, its icon is
Matty's, and the panel takes each person's Mattermost theme. Everything here is how a server makes it
its own. None of it is needed, and all of it is on **System Console > Plugins > Matty**.

**The name.** **Name and artwork > Name** is what people on this server see it called: the panel's
title, the App Bar button, every sentence the panel and the server say to people ("Matty is locked.
Unlock it with your bearer token." becomes "Movie Night Club is locked. Unlock it with your bearer
token."), the text of clips and plans shared into a channel ("🎬 Matty clip", "📅 Matty plan"), and
what Matty, the app, calls it on this server. Up to 32 characters, spaces and emoji included; blank
means Matty. Some things say Matty whatever the name is, because they name the product rather than
your server: the System Console's own listing and page (and so the paths the panel gives, such as
**System Console > Plugins > Matty > Broadcast Box**), the server log, and the User-Agent the server sends
to TMDB, IGDB, Twitch, Steam, Hytale and GitHub
(`MattyPlugin/4.4.0 (Mattermost plugin; +https://github.com/vvilla-01/matty-plugin)`, and to push
servers `MattyPlugin` alone). The bot and the plugin's Mumble account have
names of their own, under **Bot account** and **Mumble > Connect as**; both are Matty until you
change them.

**The pictures.** **Name and artwork > Emblem, icon and ghost** is a card of three rows, each with
**Choose a file**: an SVG, PNG, JPEG, GIF or WebP of up to 512 KB, saved at once with no Save
needed (a picture added or removed shows for everybody at once; one that replaces another, on
their next page load). Out of the box its line reads "Nothing
uploaded: Matty’s own icon, and no emblem or ghost."

- **Panel emblem**, faded into the bottom corner of the panel behind everything, and behind the
  lock screen's padlock. Square artwork works best. None is built in: the row shows **None**, and
  **Remove** takes an uploaded one away again, leaving none.
- **App Bar icon**, the button that opens the panel, drawn at 24 pixels, and also the button in
  the channel header wherever there is no App Bar. Matty's own icon is built in, and **Use Matty’s
  icon** goes back to it. The icon in the App Marketplace comes from the installed bundle, and
  only a new bundle changes that one.
- **No Signal ghost**, the picture that flickers through the static now and then when nothing is
  tuned in. Give it a transparent background: an opaque one shows as a pale square. None is built
  in, so out of the box No Signal is static only; the setting **No Signal ghost** says how often it
  appears once there is one.

**Use Matty’s icon** and **Remove** delete the uploaded file, so each takes a second click.

See [Changing the artwork](panel.md#changing-the-artwork). Each 24/7 channel can have a picture of
its own too, under **Streams > 24/7 pictures**, and the bot one under **Bot account > Picture**.

**A banner.** **Panel > Banner text** puts a banner at the top of the panel, or at the top and
bottom (**Banner placement**), to match a Mattermost classification banner. Blank, as it is out of
the box, means no banner. **Banner colour** takes a hex colour; blank uses each person's Mattermost
theme's button colour, and the text turns black or white by the contrast rule Mattermost uses for
its own banner. See [Banners](panel.md#banners).

**A theme.** The **Theme** section puts people on a Mattermost theme of your choosing. Make the theme
in Mattermost, copy it from **Settings > Display > Theme > Copy to clipboard**, paste it into
**Theme colours**, switch on **Give people your theme**, and save: new accounts get it as they are
created, and **Apply to everyone now** puts everybody who already has an account on it. **Keep people
on it** decides whether somebody who picks another theme keeps it or is put back at their next login,
and **Black code blocks** puts code blocks on black to match a dark theme. Nothing is applied while
**Theme colours** is blank. The panel follows whatever theme each person is on, so it matches yours
without anything else to set. See [A theme for everybody](panel.md#a-theme-for-everybody).

## Settings

The System Console page is grouped into **sections**, in the order an admin sets things up:
Broadcast Box, Access, App check, Updates, Push notifications, Panel, Streams, Name and artwork, Live badge,
Airtime and awards, Clips, Birthdays, Theme, Movies and games, Twitch, Steam, Hytale, Mumble,
Soundboard, Mumble chat bridge, Bot account, Backup, Troubleshooting. Sections are
what Mattermost's plugin schema offers; there is no tab mechanism in it, and faking one would mean
taking over the rendering of every control on the page and slicing an array of pre-built elements by
position. Each setting's help text is short and says what it promises; the longer story is here, and
the help text says where.

Each setting is also stored in Mattermost's configuration, under `PluginSettings.Plugins` →
`ceo.villa.matty`, each key in lower case: `broadcastboxurl`, `pollintervalseconds` and so on.
The plugin's own controls, next, are the exception: apart from the birthday list, which is saved
with the page, they have no value there.

Nineteen of the controls are the plugin's own rather than Mattermost's: the let-in list, the app
check's results, the updates' status, the push notifications' status, the 24/7 pictures, the
preview pictures in use, the artwork, the recorded airtime, the clips on the server, the birthday
list, the Apply to everyone button, the remembered titles, the Twitch connections, the Steam
connections, the Hytale connections, talking from Matty's status, the sounds on the server, the
bot's picture and the settings file. They share one look: a line saying what each holds, **No Save
needed** on the fifteen that act at once rather than with the page's Save button (the birthday list
is saved with the page, and the app check's results, the push notifications' status and talking
from Matty's status only show), rows with a picture, pills,
and a line under them for what was just done or what went wrong. Anything that cannot be
undone takes a second click: blocking somebody from clips or preview pictures, clearing a block list,
deleting, wiping, clearing reports, removing a picture,
resetting a score, forgetting, unlinking somebody's stream, disconnecting somebody's Twitch, Steam or
Hytale account, **Delete all Twitch data**, **Delete all Steam data**, **Delete all Hytale data**,
**Apply to everyone now**, going back to a default picture, moving the bot onto an old account,
deleting a sound, and **Start the board again**. The first click turns the pill red and adds a
question mark to its words, and the line under the card says what the second click will do. Taking somebody off the let-in list
can be undone, so it acts at once and offers **Undo** for ten seconds; blocking somebody from the
soundboard acts at once too, and **Unblock** beside their name undoes it. These cards are
the only part of the page that can be got wrong, so the lab (`make lab`, which writes
`dist/matty-lab.html`) draws the whole page from
`plugin.json` the way Mattermost does, under **Show › System Console**, with every one of them in its
place.

**A setting nobody has ever saved runs at the default the page shows.** Mattermost 11.8 and later,
so every server Matty Plugin supports, do that themselves, and the plugin applies the same defaults
itself as well, reading which settings have been saved from the stored configuration. Anything that
has been saved, even saved off or blank, is left exactly as it is.

A test reads `plugin.json` and the configuration struct and fails if they disagree: a setting on
the page with nowhere to store it, a stored field nobody can edit, or a password the manifest does
not mark `secret`.

| Setting | Default | What it does |
| --- | --- | --- |
| **Broadcast Box** | | |
| Broadcast Box URL | *(blank)* | Where the server fetches `/api/status` and relays WHEP, such as `http://192.168.1.20:8080`. Use the direct LAN address; see [Broadcast Box URL](#broadcast-box-url). Blank, nothing is asked of Broadcast Box and the panel says it is not set up. |
| Basic Auth username | *(blank)* | Only when the URL sits behind a proxy with HTTP Basic Auth. |
| Basic Auth password | *(blank)* | That proxy's password. A secret; see under the table. |
| Refresh interval (seconds) | `15` | How often to ask who is live. |
| Missed checks before a badge comes off | `2` | Checks in a row a stream must be missing before its badge comes off, its [What's on?](unlocking.md#whats-on) is taken down and its Clips recording stops. A check is one **Refresh interval**: the extra polls a save or a link makes do not count, and a poll that sees the stream again starts the count over. Stops flicker when OBS reconnects. |
| Public watch URL | *(blank)* | Only for the **Pop out** link, which goes through the plugin and is offered only to people who have linked a stream. A key it hands out keeps working after an unlink, so leave this blank unless you need it; see [Why the pop-out link defaults to off](unlocking.md#why-the-pop-out-link-defaults-to-off). |
| **Access** | | |
| Let in without a token | *(nobody)* | The people who may watch streams and clips without a bearer token, and nothing more, and the **Unlink** for anybody's stream. Acts at once, with no Save. See [Let in to watch](unlocking.md#let-in-to-watch). |
| **App check** | | |
| Check the Matty app | Check, and show the result | Whether a phone's Matty is the official signed build, on Android's own word: **Off**, **Check, and show the result**, or **Check, and refuse a Matty that fails**. Refusing turns away a Matty that has not passed everywhere but the lock screen; the web panel is never refused. It proves the app's RSA signing certificate, not its post-quantum one, and cannot tell the web panel from a program that hides what it is. See [The app check](security.md#the-app-check). |
| More signing certificates | *(blank)* | SHA-256 digests of more signing certificates to accept besides Matty's own, one per line, colons and case ignored, for people here who run a build of their own: the certificate Android attests, the first in the app's signing history. |
| More verified-boot keys | *(blank)* | Verified-boot key digests to accept for phones whose system is signed by a key their owner put in (a locked bootloader with a yellow boot screen), one per line. GrapheneOS's keys are built in. |
| Minimum Matty version | *(blank)* | The oldest Matty this server answers, such as `0.1.32`; blank for none. An older Matty, or one whose version cannot be read, is refused by the plugin's API everywhere but the few places it needs to show why, and taken out of voice: Matty 0.1.32 and newer then close this server until they are updated. Matty 0.1.31 and older are refused what they ask this plugin for. They still get the list of who is live, which goes to every open client, but cannot play from it. Push notifications go on. Matty says its own version, so a changed copy can get past it. Anything that is not three numbers counts as none, and **Results** says so. A number above the newest Matty turns every Matty away. See [A minimum Matty version](security.md#a-minimum-matty-version). |
| Results | — | Whether this plugin is a signed release, with its manifest's SHA-256, then the minimum Matty version in force, if any, and every Matty with a verdict that has not expired, newest first, with **Refresh**. See [The app check](security.md#the-app-check). |
| **Updates** | | |
| Look for updates | off | Whether this server looks on GitHub for a newer release of Matty Plugin: once a day, once when switched on, and on **Check now**. When one is out, system admins are shown it in the panel and in Matty, and the bot sends each of them one message per version. A system admin installs it with **Install** on the card below or in Matty: the server downloads and checks the bundle, and the admin's own session hands it to Mattermost. That needs this plugin to be a signed release, the release to have this platform's bundle, and Mattermost to take plugin uploads (**Enable Plugins** and **Enable Plugin Uploads** on, **Require Plugin Signature** off). The server then contacts GitHub. See [Plugin updates](updates.md). |
| Status | — | The version this server runs and its platform, the newest release the last look found and when, **Install** or why a newer one cannot be installed from here, and the last trouble, with **Check now** (at most once a minute for the whole server) and **Refresh**. See [The card](updates.md#the-card). |
| **Push notifications** | | |
| Send push notifications to Matty | **off** | Lets anybody signed in register their Matty for push notifications, and sends each an end-to-end encrypted push, through the push server their UnifiedPush app names, whenever Mattermost decides to push to them. Needs Mattermost's own push notifications on (**System Console > Environment > Push Notification Server**). The server then contacts those push servers. See [Push notifications](push.md). |
| Push servers on your own network | *(blank)* | Push servers on your LAN or this machine, one `host` or `host:port` a line, such as `ntfy.home.lan`; only these may be at a private address or use plain `http://`. See [Your own push server, and the list](push.md#your-own-push-server-and-the-list). |
| Only the push servers listed above | off | Refuses a push address at any other push server, public ones included. |
| Contact for push servers | *(blank)* | A `mailto:` or `https:` address every push server is given, so that whoever runs one can reach you. Anything else is left blank. |
| Status | — | Whether pushes can go out, and how many phones of how many people get them, by push server: working, waiting, failing and refused; never who uses which. With **Refresh**. See [The card](push.md#the-card). |
| **Panel** | | |
| Open the panel automatically | Never | Never, when somebody is live, or every time, decided once per page load. For somebody who has not unlocked Matty it opens onto the lock screen. See [Opening the panel by itself](panel.md#opening-the-panel-by-itself). |
| Banner text | *(blank)* | Text in the panel's banner, such as `Members only`. Blank hides the banner. See [Banners](panel.md#banners). |
| Banner colour | *(blank)* | A hex colour such as `#1c58d9`. Blank uses each person's Mattermost theme's button colour. The text colour is derived from it. |
| Banner placement | Top and bottom | **Top only** or **Top and bottom**, the same two choices as Mattermost's Classification Markings. |
| Show live preview pictures | on | Live now cards show a recent still of each stream, shared between viewers. Twitch streams' pictures come from Twitch, relayed by the server, and follow this too. See [Live now](panel.md#live-now). |
| Preview pictures in use | — | Every preview showing and who took it: remove one, or block somebody from sharing them. See [Keeping them clean](panel.md#keeping-them-clean). |
| Show who is watching | on | Faces of the people watching each live stream, on its card. Off, the Snoopers board and the watching achievements move once an hour, and nobody is counted toward biggest fans. See [Who is watching](panel.md#who-is-watching). |
| **Streams** | | |
| 24/7 stream keys | *(blank)* | Comma separated, such as `radio`. Listed and tagged `24/7`; never badges anyone, earns airtime or is recorded for Clips. See [24/7 channels](panel.md#247-channels). |
| 24/7 pictures | *(none)* | A square picture for each 24/7 stream, in place of a profile picture, and a wide card picture for its Live now card. See [24/7 stream pictures](panel.md#247-stream-pictures). |
| Stream names | *(blank)* | `streamKey = Display Name`, one per line, for streams no Mattermost account owns, such as `radio = Lo-fi radio`. A stream with neither an account nor a name here reads "Stream" and the start of its handle. |
| Stream notes | *(blank)* | `streamKey = note`, one per line, such as `radio = Music around the clock.` Only fills a stream that has no note of its own. See [Stream notes](unlocking.md#stream-notes). |
| Match stream keys to usernames | off | A key equal to a username is credited automatically. Usernames are self-service, so linking a key is the stronger option; see [How a person gets a red dot](unlocking.md#how-a-person-gets-a-red-dot). A match never unlocks the panel. |
| **Name and artwork** | | |
| Name | `Matty` | What people on this server see it called, up to 32 characters; blank is Matty. See [Name and artwork](#name-and-artwork). |
| Emblem, icon and ghost | Matty's icon; no emblem, no ghost | Upload any of the three, or take it away again; applied at once. See [Name and artwork](#name-and-artwork). |
| No Signal ghost | Occasionally | How often the No Signal screen catches the ghost picture, once there is one: never, rarely, occasionally or often. See [No signal, and changing channel](panel.md#no-signal-and-changing-channel). |
| **Live badge** | | |
| Show a live badge | on | The custom-status badge for linked streamers while they are live, and, while **Show people live on Twitch** is on, for connected Twitch streamers while they are live there. See [How a person gets a red dot](unlocking.md#how-a-person-gets-a-red-dot). |
| Badge emoji | `red_circle` | Any emoji name, with or without colons. Custom server emoji work too. |
| Badge text | `Live now` | Custom status text while live. |
| **Airtime and awards** | | |
| Track streamed hours and award medals | on | Streamed hours, the monthly and all-time boards, and the medals, by person. While **Show people live on Twitch** is on, time live on Twitch counts too, and a minute live on both counts once. See [Airtime and medals](awards.md#airtime-and-medals). |
| Rank the Top Snoopers | on | Ranks people with a linked stream by the hours they spend watching other people's streams, on the Trophies card's Snoopers tab. Watching a Twitch stream never counts. Off records nothing and hides the tab. See [Top Snoopers](awards.md#top-snoopers). |
| Give achievements | on | Coins for hours streamed, hours spent watching others, months on the podium, thumbs up on clips and hours on the 24/7 channels below, worked out from what the plugin already keeps. Beside a name only when its owner chooses. See [Achievements](awards.md#achievements). |
| 24/7 channel achievements | *(blank)* | `key = name = hours, hours, …`, one per line, such as `radio = Lo-fi radio = 1, 10, 50, 100, 250`, up to 8 lines and 8 steps; the key must be one of the **24/7 stream keys**. Counted for linked people watching in the panel, while **Rank the Top Snoopers** is on. The keys stay on the server. |
| Name each streamer's biggest fan | on | Whoever has watched each linked streamer most in the panel, all time, once it is an hour. Counted only while **Rank the Top Snoopers** and **Show who is watching** are both on. Keeps how long each person has watched each streamer. Nobody can opt out on their own (since 3.2.1); off stops it for everybody and keeps what was counted. See [Biggest fans](awards.md#biggest-fans). |
| Recorded airtime | — | What is recorded, and **Export**, **Restore backup** and **Import…**. See [Airtime is the one thing here that cannot be rebuilt](awards.md#airtime-is-the-one-thing-here-that-cannot-be-rebuilt). |
| **Clips** | | |
| Let people clip streams | **on** | The ✂, the Clips list, Best Clipper and sharing, for people who have linked their stream; people let in can watch clips. While it is on, this server records the last few minutes of every live stream that is not a 24/7 channel. Off stops every recording at once; published clips stay until they expire. See [Clips](panel.md#clips). |
| Clip length | 2 minutes | What one press keeps, from 30 seconds to 5 minutes, and how much of each live stream is held on the disk. People can trim a clip shorter, never longer. |
| Keep clips for (days) | `30` | 1 to 365. Clips that lowering it makes too old are removed only after 24 hours. See [How long things are kept](panel.md#how-long-things-are-kept). |
| Space for clips (GB) | `20` | Published clips and drafts together; one person's clips at most a fifth of it, and never less than 1 GB. Publishing is refused past either; nothing is deleted to make room. |
| Let people download clips | **off** | A **Download** in the clip viewer, for people who have linked their stream; people let in can watch but not download, and a streamer can turn downloads of their own stream's clips off. Every download is logged. See [Downloading a clip](panel.md#downloading-a-clip). |
| Clips on the server | — | Every clip, the recordings, the people and the blocks, for any system admin. See [Moderation](panel.md#moderation). |
| **Birthdays** | | |
| Post birthday greetings | off | A greeting on the morning of each birthday on the list, exactly once. See [Birthdays](voice.md#birthdays). |
| Birthday list | *(empty)* | The list: a name, a date, and a username or the parents. Saved with the page. |
| Channel | *(blank)* | `team/channel`, using the names in the URL, such as `myteam/town-square`. |
| Time zone | *(blank)* | An IANA name such as `Europe/London`; blank is the server's own time zone. |
| Hour to post (0 to 23) | `9` | 0 to 23, in that time zone. |
| Put a cake by their name | on | A 🎂 custom status for the whole of somebody's birthday. Beats the live dot. See [A cake next to their name](voice.md#a-cake-next-to-their-name). |
| Greeting | `# 🎂 Happy birthday, {full_name}! 🎉` and, on the next line, `@{username} turns **{age}** today.` | With `{full_name}`, `{first_name}`, `{username}` and `{age}`. |
| Greeting for somebody with no account | `# 🎂 Happy birthday, {full_name}! 🎉` and, on the next line, `{parents}'s kid turns **{age}** today.` | With `{full_name}`, `{first_name}`, `{parents}` (the parents' @mentions) and `{age}`. |
| **Theme** | | |
| Give people your theme | off | New accounts get the theme in **Theme colours**. See [A theme for everybody](panel.md#a-theme-for-everybody). |
| Keep people on it | Leave people alone | Or put everybody back on it each time they log in. |
| Theme colours | *(blank)* | The theme's JSON, as Mattermost's **Copy to clipboard** gives it. Nothing is applied while it is blank. |
| Apply to everyone | — | The saved theme's colours, and **Apply to everyone now**. |
| Black code blocks | off | Puts code blocks on black, with a thin border. See [Code blocks](panel.md#code-blocks). |
| **Movies and games** | | |
| Look up movies, shows and games | **off** | Lets the planning form search TMDB, Steam and (with a Twitch application) IGDB, through the server. See [Movies and games](up-next.md#movies-and-games-optional). |
| TMDB read access token | *(blank)* | Needed for movies and shows; games work without it. A secret. |
| Remembered titles | — | How much is remembered, pictures included, and **Forget remembered titles**. See [Remembered titles](up-next.md#remembered-titles). |
| **Twitch** | | |
| Client ID | *(blank)* | With the secret, lets game lookups also search IGDB for games that are not on Steam, and lets people connect Twitch. See [The Twitch application](up-next.md#the-twitch-application). |
| Client Secret | *(blank)* | The same application's secret. A secret here too. |
| Show people live on Twitch | **off** | People who have unlocked Matty can connect their Twitch account; while they are live there they get the badge, a Live now card and airtime. The server then contacts Twitch, and the person connecting is sent to Twitch in a window of their own. See [Twitch (optional)](up-next.md#twitch-optional). |
| Play Twitch streams inside Matty | **off** | Twitch streams play in the panel in Twitch's own player, which a viewer's browser loads from Twitch. Off, a Twitch card opens twitch.tv. See [Watching inside the panel](up-next.md#watching-inside-the-panel). |
| Twitch connections | — | The OAuth Redirect URL to register, whether Twitch's player can run at this server's address, who has connected, Disconnect, block, 24/7 and **Delete all Twitch data**. Works with the switch off too. See [What an admin controls](up-next.md#what-an-admin-controls). |
| **Steam** | | |
| Show people's Steam status | **off** | People who have unlocked Matty can connect their Steam account, and everybody who can open the panel sees their Steam name, whether they are online, busy or away, and what they are playing. The server then contacts Steam, and the person connecting is sent to Steam in a window of their own. See [Steam (optional)](up-next.md#steam-optional). |
| Steam Web API key | *(blank)* | Made at steamcommunity.com/dev/apikey for this server's domain. Needed for the switch above. A secret. |
| Steam connections | — | The address Steam sends people back to, whether the key works, the last check, who has connected, Disconnect, block and **Delete all Steam data**. Works with the switch off too. |
| **Hytale** | | |
| Show people's Hytale names | **off** | People who have unlocked Matty can connect their Hytale account, and everybody who can open the panel sees the Hytale username they picked, with Copy. The server then contacts Hytale, and the person connecting is sent to Hytale in a window of their own. See [Hytale (optional)](up-next.md#hytale-optional). |
| Client ID | *(blank)* | From a Hytale third-party application. Needed for the switch above. |
| Client Secret | *(blank)* | The same application's secret. A secret. |
| Hytale connections | — | The Redirect URI to register with Hytale, with Copy, whether the application is set up, who has connected, Disconnect, block and **Delete all Hytale data**. Works with the switch off too. |
| **Mumble** | | |
| Show who is in voice | off | Connects to Mumble and lists who is in there. See [Who is in voice](voice.md#who-is-in-voice). |
| Mumble server | *(blank)* | `host` or `host:port`, such as `192.168.1.20:64738`; the port defaults to 64738. |
| Connect as | `Matty` | The plugin's name in Mumble. Hidden from its own roster under this name. |
| Server password | *(blank)* | Only if your server has one. A secret. |
| Names to leave out | `Mattermost` | Bots rather than people. Comma separated. |
| Match names to usernames | on | Marks your own row and puts headphones on the matching account; the soundboard uses the same match. Only plain-letter Mumble names match. See [Headphones for whoever is on Mumble](awards.md#headphones-for-whoever-is-on-mumble). |
| Names that differ | *(blank)* | `mumbleName = mattermostUsername`, one per line, such as `alice_ = alice`. |
| Channels to show | *(blank)* | The Mumble channels Matty and the panel show, one a line, each with the channels under it: a name shows every channel so named, a path such as `Gaming/Duo` or `/Lobby` that channel alone. People elsewhere are not shown in voice. Blank: every channel. Only the first 200 lines are used, and a line longer than 256 characters is not. See [Showing only some channels](voice.md#showing-only-some-channels). |
| Let people talk from Matty | **off** | People who may watch join the Mumble channels from the Matty app and talk there, through this server, each on a Mumble connection of their own named `<username> (Matty)`. Needs **Show who is in voice**, the port below open and the address phones reach. One Mattermost server only. See [Talking from Matty](voice.md#talking-from-matty). |
| Voice port (UDP) | `8445` | 1024 to 65535. The one UDP port every phone's voice reaches this server on: open it in the firewall, UDP. See [The port and the firewall](voice.md#the-port-and-the-firewall). |
| Address for phones | *(blank)* | Where phones reach this server: IPv4 addresses or host names, one a line. Blank: the Site URL's host. See [The address](voice.md#the-address). |
| The voice server's autoban is off | off | Lifts the plugin's own limit of 8 connections to the Mumble server in two minutes. Only once the Mumble server's autoban no longer counts them. See [The autoban, and the two ways round it](voice.md#the-autoban-and-the-two-ways-round-it). |
| Talking from Matty | — | Whether the voice port is open, the addresses phones are given, whether Mumble is connected, how many people talk from Matty now, Mumble's last refusal, and the lines of **Channels to show** that match no channel, match more than one, or are not used. See [The card and the alert](voice.md#the-card-and-the-alert). |
| **Soundboard** | | |
| Let people play sounds into Mumble | **off** | A speaker in the bar at the bottom of the panel, for people who have linked their stream and are in Mumble: a board of short sounds they play into their own Mumble channel. The plugin's Mumble account then sends audio; check [Setting the soundboard up](voice.md#setting-the-soundboard-up) first. Needs **Show who is in voice**. |
| Wait between sounds (seconds) | `10` | 3 to 120, after somebody's own sound ends. One sound at a time and 8 a minute in all, whatever this says. |
| Only registered Mumble users | off | Offers the soundboard only to Mumble names that are registered. See [What a Mumble name proves](voice.md#what-a-mumble-name-proves). |
| Sounds on the server | — | Every sound with **Listen** and **Delete**, who is blocked, and whether the plugin's Mumble account can play. See [Setting the soundboard up](voice.md#setting-the-soundboard-up). |
| **Mumble chat bridge** | | |
| Bridge Mumble chat | off | Mumble text chat in a channel, both ways. See [Mumble chat, in the same channel](voice.md#mumble-chat-in-the-same-channel). |
| Channel | *(blank)* | `team/channel`, using the names in the URL. |
| Accounts to ignore | *(blank)* | Any other bridge's account. See [Running it alongside your old bridge](voice.md#running-it-alongside-your-old-bridge). |
| **Bot account** | | |
| Username | `matty-bot` | The @name the bot goes by. Renames the existing account. |
| Display name | `Matty` | What shows above its posts. |
| Description | `Relays chat between Mumble and Mattermost, and posts birthdays.` | Shown on its profile. May be blank. |
| Picture | — | The bot's picture, cropped and applied at once. See [The bot](#the-bot). |
| **Backup** | | |
| Settings file | — | **Back up** and **Restore…**. See [Backing the settings up](#backing-the-settings-up). |
| **Troubleshooting** | | |
| Log WHEP negotiation | off | Logs each playback negotiation to the server log. SDP holds session ICE credentials, so it is off unless somebody is looking into a problem. |

**The six secrets** — the Basic Auth password, the TMDB token, the Twitch Client Secret, the Steam
Web API key, Hytale's Client Secret and the Mumble server password — are kept as plain text in
Mattermost's configuration, like every plugin
setting, so protect configuration backups accordingly. Mattermost masks them on this page. The
plugin never sends them to a browser, and a [settings backup](#backing-the-settings-up) leaves them out
unless you tick **Include the passwords and keys**.

## The bot

One account, created and owned by the plugin when it starts, that posts relayed Mumble chat and
birthday greetings: `@matty-bot`, shown as Matty, until you change it. The **Bot account** section
sets its **username**, **display name** and **description**, and its **picture**.

Renaming it renames the account it already has rather than leaving a second one behind:
`EnsureBotUser` remembers the bot's id in the plugin's own store and patches the existing account
with whatever name it is handed. Nothing looks the old name up, so there is no window in which two
bots exist.

The description is the one of the three that may be blank — leave it empty and the bot simply has
none. It is cut to Mattermost's 1024-rune limit rather than sent long: the limit applies to the
whole patch, so an over-long description would take the username and display name down with it. It
is cut with nothing appended, which is not the same clamp the chat bridge uses — that one adds an
ellipsis, which would land one rune *over* the limit that made it clamp.

Mattermost will not take a username with a capital or a space in it, and it will not take one
somebody else already has. The first is caught here: capitals are lowered and a leading `@` is
dropped, and anything else Mattermost would refuse falls back to `matty-bot` rather than silently
stopping. The second can only be found out by asking, so it is asked, and the reason
is shown under the bot's picture — an admin who typed a name and saw nothing happen otherwise has
no way to learn which of those two things went wrong.

When a rename is refused, the plugin keeps posting under the name the account **already has**:
asked for again, it cannot collide with anything. Falling back to the default name would not be
safe, because another account may be sitting on it: after taking over a bot left behind (below),
the retired bot keeps its name, and a refused rename would then find both names taken and leave the
plugin with no bot at all. The default is tried only when there is no account to ask about.

### Taking over a bot left behind

If the name you type belongs to **another bot whose plugin has been removed**, the settings page
says so and offers **Use @name**. This is for a bot an earlier plugin made and left behind,
carrying every relayed message and birthday greeting it ever posted.

Pressing it points the plugin at that account and **disables** — does not delete — the one it was
using. That distinction is the point. Mattermost's `PermanentDeleteUser` removes the account's
posts, reactions and files from every channel; there is no deleting a bot that keeps its history.
Moving onto the original keeps all of it under one name.

It will only take over a bot when all of this is true, and says which rule stopped it otherwise:

- it is a **bot**, not a person's account;
- it is **not owned by a person** — a bot made from Integrations belongs to whoever made it;
- the plugin that made it is **not installed at all**. Merely disabled is not enough: a disabled
  plugin that is switched back on goes straight back to its own bot, and two plugins patching one
  account in turn is a fight nobody wins. `GetPluginStatus` answers 404 for a plugin that is gone.

The candidate is looked up again on the server when the button is pressed, rather than trusted from
the page, so a stale page cannot point the plugin at some other account. If Mattermost will not hand
the account over, everything is put back as it was.

Mechanically: `EnsureBotUser` remembers its bot under `mmi_botid` in the plugin's own KV store
(`server/channels/app/bot.go`). Writing that key is how the plugin is told "this account", rather
than hoping a lookup by name lands on the right one. The account keeps saying *Managed by plugin*
and the old plugin's id in Integrations afterwards: that label is the bot's `OwnerId`, which no
Mattermost API will reassign to a plugin. It has no effect on anything the plugin does.

## Backing the settings up

**Backup** saves every setting on the page as one JSON file, and puts them back.

**Back up** downloads it, as `matty-settings.json`. Passwords and keys (the six secrets above: Basic
Auth, the TMDB token, the Twitch Client Secret, the Steam Web API key, Hytale's Client Secret and
Mumble) are left out unless you tick **Include the passwords and keys**, which names the file
`matty-settings-with-passwords.json`, and a file without them
says so in its own `omitted` list — which is not the same as a file that carries an empty password.
**Anything a backup does not mention is left exactly as it is on import**, so restoring a
passwordless backup onto a working server does not take it off the air.

It is written indented, because a backup nobody can read is a backup nobody checks.

Even without the passwords it names stream keys: the 24/7 channels, the stream names and the
stream notes are all written by key. Keep it as privately as the settings page itself.

**Restore…** takes it back. The file is checked before anything is written: it must say it is a
Matty Plugin settings backup, or one VillaNET made (an airtime export politely refuses and points at
the right section), it must
not come from a newer format, and every value must fit the setting it claims to be. That last
check has to happen in the plugin rather than being left to the server:
`LoadPluginConfiguration` *logs* an unmarshal failure and carries on, so a hand-edited file with
`"ten"` where a number belongs would not fail the import — every setting after it would silently
become its zero value. It is refused instead, and the offending setting is named.

The page reloads itself afterwards. It has to: the console is still showing the old values, and
pressing **Save** on that stale form would put every one of them straight back.

**What it does not carry**, and the file says so in its own `notIncluded` list:

- **Recorded airtime.** It has its own **Export** under *Airtime*, because it is the irreplaceable
  half and does not belong in the same file as a banner colour.
- **Clips**: the published clips, drafts, upvotes, Best Clipper, who is blocked from clipping, and
  which streams are switched off for clipping or for downloads. They live in the plugin's store and
  on the server's disk; the file carries the Clips settings, **Let people download clips** among
  them, and never a clip.
- **The artwork, the 24/7 pictures, and who is blocked from sharing preview pictures.** They live in
  the plugin's store.
- **The bot's picture**, which lives on the bot's Mattermost account rather than in the settings.
- **Who has linked which stream key.** Those are per-person publish credentials, and a file in
  somebody's Downloads folder is the wrong home for them. Re-linking is one paste of the bearer
  token each. (*Match stream keys to usernames* brings back the badges, but never unlocks the
  panel.)
- **Who is let in to watch without a token.** It is per-person access, like the links, not a setting,
  and restoring an older file would quietly let back in people an admin has since let out — exactly
  the wrong failure for a lock. Add them again under **Access**; the list is short.
- **The remembered movie, show and game titles.** They are a cache of what the providers said,
  rebuilt by looking them up again.
- **Who chose which awards to show by their name.** They are people's own choices, and an older file
  put back would undo a change of mind.
- **Biggest fan and 24/7 channel hours**: who watched whom. They live in the plugin's store.
- **Soundboard sounds, and who is blocked from the soundboard.** They live in the plugin's store too.
- **Who has connected which Twitch, Steam or Hytale account**, who is blocked from connecting them,
  and their Twitch hours. They are people's own connections, which they can delete with one press.
- **Which phones get push notifications, and this server's push key.** Each phone registers again by
  itself, with the push key of whichever server it finds: a server set up afresh from a backup makes
  a key of its own.

A setting the backup carries but this build has no field for is reported and ignored (**Who to ask**,
in a backup made before 3.1.0, is one); one the
stored configuration has and this build does not know about is carried across untouched, because
`SavePluginConfig` replaces the whole map and leaving it out would delete it.
