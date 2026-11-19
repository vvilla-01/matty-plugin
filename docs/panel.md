[The Matty Plugin manual](README.md)

# The panel

The panel is called whatever the server calls it: the setting **Name**, under **System Console >
Plugins > Matty > Name and artwork**, which is Matty unless an admin chose another (see
[Name and artwork](install.md#name-and-artwork)). Its title, its App Bar button, its entry under
Mattermost's **Settings › Plugin preferences**, and every sentence it says that names it follow that
setting. This chapter quotes those sentences as they read with the name left as Matty; on a server
with a name of its own, that name stands where they say Matty. The System Console always lists the
plugin as Matty.

A system admin also finds at the top of the panel what only admins are told, among them: Clips that
need attention, with **System Console > Plugins > Matty > Clips** to put it right (see
[Moderation](#moderation)); plugin files that are not as their release signed them, with
**System Console > Plugins > Matty > App check**; files in the plugin's folder that nobody signed,
named (ten at most, then how many more) with where they are in the plugin's folder on the server (see
[Signed releases](security.md#signed-releases)).

## How it looks

The panel takes the Mattermost theme of whoever is looking at it: its ground, its words, its lines
and its accent are the theme's, so it looks at home on a light theme and on a dark one, and changes
when the theme does. Buttons, tabs, links, progress and anything chosen are in the theme's accent;
error words and anything that deletes are in the theme's error colour. **LIVE**, on air and recording keep a red of their own, because they are states, not
decoration. Nothing is red because the plugin is.

The shapes are Mattermost's too: cards and controls have Mattermost's small rounded corners and its
thin grey lines, dialogs and menus are drawn as Mattermost draws its own, and the keyboard's focus
ring is the theme's. What sits on a picture or on video (the tags on a Live now card, the player's
controls, the No Signal screen) keeps a dark shade and white words on every theme, so it reads over
any picture.

The player has no frame: the picture runs to the edge of its tile. The Live now cards are plain
cards with a thin grey line round them. The bar at the bottom is the message box's twin (below).

## The footer bar

The bottom of the panel is **a bar made as the twin of Mattermost's own message box**: the same
height, the same corners, the same line, darker when pointed at and darker again with focus inside,
the same ground, and the same distance from the edges of its column. With a thread open, or with the
panel beside a channel, the bar and the message box sit level, top and bottom (both are 98px tall at
the usual size). Under it is the same strip of room Mattermost leaves under its own box; when the
server has a banner at the bottom of the panel, the banner is that strip, so the bar stays level with
the banner on or off.

Everything the bar can open is a **sheet** that rises out of it, one at a time. Opening a sheet grows
the footer upward and the panel above gives way, so the bar never moves from under the pointer that
pressed it.

**For somebody linked**, the bar's top row is their **stream title**: a text field, like the
message box's, holding the title everybody sees on their stream, or "What are you streaming?" when
there is none. Click it and type; see [Stream notes](unlocking.md#stream-notes). **Enter** saves, and
so does clicking or tabbing away; **Escape** puts the saved title back. At the right of the bottom
row, where the message box has Send, is **Save**, a check mark, quiet as Send is until the title in
the field differs from the saved one, and saying "Saving" while it saves. A title that could not be
saved says why over the bar. Along the left of the bottom row are a few small tools, the size and
shape of the message box's own buttons, each of which says what it is when pointed at; a tool whose
sheet is open is tinted in the theme's accent:

- **The speaker**, first, while an admin has the soundboard on and you are in Mumble. It opens the
  **Soundboard**; see [Soundboard](voice.md#soundboard). It carries a small dot until it is first opened
  in this browser, and a slash across it while you are muted in Mumble, when it says so when pointed
  at. It comes and goes with you in voice.
- **What’s on?**: a television, or once something is said, its picture and title. It opens **What’s on
  right now**; see [What's on?](unlocking.md#whats-on).
- **The scissors**, while Clips is on. Their dot says whether clips of your stream are on: in the
  LIVE red and breathing while your stream is being kept for clipping this minute, in the theme's
  accent while clips of it are allowed and you are off the air, and grey when you have switched them
  off. They open **Clips of your stream**; see [For streamers](#for-streamers).
- **The link key**, always there, whatever else is switched on. It opens **Relink your stream**, with
  Unlink; see [Relinking, and unlinking yourself](unlocking.md#relinking-and-unlinking-yourself). While an admin
  has Twitch, Steam or Hytale switched on, or you have one connected, the same sheet is **Your stream
  and accounts**, with **Connected accounts** under your stream (below). It is lit in the theme's
  error colour while the stream title cannot be set, because relinking is what fixes that.
- **The calendar**, which opens **Plan a stream**; see [Up next](up-next.md#up-next).

**Connected accounts**, in the link key's sheet, has a row for each of Twitch, Steam and Hytale that
you can connect or have connected: the service's name, after its mark (for Twitch its own Glitch, the
speech bubble with two eyes, drawn unaltered in the sheet's grey; for Steam and Hytale a plain line
glyph), then your name there and how it stands. Twitch says **Live now** while you are live
there; Steam says Online, Busy, Away, Playing and the game, or Offline, or that your profile is
private. Not connected, a row says what connecting is for, with **Connect**, which turns the sheet
into that service's own steps, with an arrow back to the list: see
[Twitch (optional)](up-next.md#twitch-optional), [Steam (optional)](up-next.md#steam-optional) and
[Hytale (optional)](up-next.md#hytale-optional). **Disconnect** takes two presses, and what it did stays said
for six seconds. A row is one line unless that would cut its name or its status short; then the
status goes on a second, quieter line. Below about 300 px of row the services' marks are left out, so
the names keep their room. A row shows only while its service is switched on or you are connected to
it, never while an admin has blocked you from it; a system admin also sees why a service cannot be
connected, when something is wrong with it. The Twitch tool that was in the bar until 3.4.0 is this
row now.

Each of these sheets is drawn as Mattermost draws its own menus, in the theme's colours with a thin
line and a soft shadow, and has a heading, a line under it when there is something worth saying,
anything it can do to itself, and an **×**. Pressing its tool again closes it, and so does **Escape** from anywhere in it.
In a sheet with a bearer token bar, the first Escape clears an error and the next one closes. Inside
a service's steps under Connected accounts, Escape goes back to the list, as the arrow does. Focus
goes back to the tool that opened it. Only the clips sheet also closes when you click elsewhere. The
others may hold something half typed, which a stray click should not throw away.

The stream title has the whole top row, so a narrow panel narrows the field and the tools keep their
size. Below about 360 px of bar, What's on? keeps its picture and drops its words, which are still
its name and in its tooltip. The bar holds five tools at most.

When Broadcast Box has your stream hidden from its list, a warning sits over the bar, with **Show
me**; see [Stream notes](unlocking.md#stream-notes). So does a notice that your Twitch, Steam or
Hytale account was connected to somebody else, with **OK**: one at a time, the newest first, and OK
shows the next.

**For somebody let in to watch**, the bar is the same box: **Watch only** in its top row, in quieter
words, and **Link your stream** at the right of its bottom row, where Save would be. It opens the
bearer token bar in a sheet, with **Link** and **Cancel**; Escape closes it too. Nothing else in the
footer prompts or explains, with one exception: somebody who connected a Twitch, Steam or Hytale
account while they were linked has a small Disconnect line over it, the same as the lock screen's
(**Disconnect Twitch** for Twitch alone), because a person can always take that back.

## Live now

Every live stream is a card, at least the size of an Up next card, in a grid: one to a row in the
narrowest panel, **two** from a 344px-wide list, and **three** only from 680px, where a third card is
still a good size — three at the widest, about 360px each. The cards grow with the panel and the
words on them grow with the cards, so a wide panel on a big monitor shows a few big pictures rather
than a row of small ones. (2.7.1 fitted in as many 168px cards as would go, so a panel a few pixels
either side of about 545px flipped between three small cards and two big ones.) The card is a
16:9 picture of what is on the stream, with the streamer's face, name, medals and headphones along
the bottom, over their stream title (links and hashtags in it still work), and **LIVE**, how long,
and how many are watching along the top. Each card is a plain card with a thin grey line round it,
which darkens when the card is pointed at. Click a card to watch it. Once something is playing, a
round **+** appears in each card's corner that watches that stream *as well*, up to four at once, and
turns into a **−** on the ones you are watching, which carry a quiet **WATCHING** tag beside their
**LIVE**. With nothing playing there is no **+**:
"as well as" would be as well as nothing, and a click on the card is how a stream starts. And with
only one stream playing there is no **−** on its card: the power switch in the middle of its picture
is how a single stream is turned off, and the card does not need a second way.

### Who is watching

In place of a plain count, a card shows **the faces of the people watching** it — up to three,
overlapping, and "+N" for the rest — the way an Up next card shows who has joined a plan. Pointing
at them lists the names ("Watching: Bob, Erin, Carol and 2 more").

The plugin knows because every viewer's connection to Broadcast Box is set up through it, by a
signed-in Mattermost user, and Broadcast Box reports which of those connections are still open. So:

- somebody watching in two tiles or two tabs shows once;
- the hidden look-ins that take preview pictures never show at all;
- somebody watching some other way, not through the panel, counts in the "+N" but has no face;
- when somebody stops watching, their face goes at the first refresh after Broadcast Box lets their
  connection go: within one **Refresh interval (seconds)** (15 by default) when they close the tile,
  and about half a minute plus one Refresh interval when their browser simply disappears;
- after the plugin restarts, or **Show who is watching** is switched back on, people already
  watching count in the "+N" but have no face until they next start the stream — nothing is kept
  for the faces while the setting is off. Two other things do know whose connection is whose, and
  neither ever puts a face on a card:
  - the [Top Snoopers](awards.md#top-snoopers) board: while it is on, the plugin still remembers which
    connection is whose for people who have **linked their stream**, because those are the hours
    it counts. It remembers nothing for it about anybody else, and the board's figures then move
    only on the hour, so they cannot be compared from one refresh to the next to tell who is
    watching now;
  - [Clips](#clips), which lets only somebody watching a stream clip it, and so keeps a record of
    its own of which signed-in person has a playback session on which stream — everybody's, linked
    or let in, whatever either setting says. It is in memory only, never shown, logged or sent
    anywhere, used for that one check and nothing else, and each session is forgotten fifteen
    minutes after it was last known to be open, and when the plugin stops. See
    [Making a clip](#making-a-clip).

Everybody who can open the panel sees who is watching what, and only people who can open it can
watch, so only theirs are the faces. The list of streams that carries the faces still reaches every
signed-in browser, locked ones included (see
[What the server holds to, and where it stops](unlocking.md#what-the-server-holds-to-and-where-it-stops)).
**Show who is watching**, under **Panel**, turns the faces off, and the cards go back to showing
only how many.

### Where the pictures come from

Broadcast Box has no thumbnails, and the plugin's server does not decode video, so a still can only
come from a browser that is playing the stream. The browsers share them through the plugin, so one
picture serves every open panel:

- A browser that is **already watching** a stream takes a frame from the tile it is playing, which
  costs nothing at all.
- When **nobody is watching** a live stream, one open panel at a time tunes in to it for a moment,
  hidden and silent, takes the first picture, and hangs up.
- Only **linked** people's panels take pictures. A picture is shown to everybody on the card, which
  makes taking one publishing rather than watching. Panels of people let in to watch show everybody
  else's pictures but take none.

The plugin decides who takes each one: a panel asks, and is given the job only when that stream's
picture is out of date (a minute and a half, for a browser that is watching, or two and a half, for
one that would have to tune in) and nobody else is already on it. So a stream nobody watches is
visited about once every two and a half minutes, by one panel, not by every panel. A panel only
does this while it is open and on screen, one stream at a time, never for a paused stream or a 24/7
channel, never past the four streams a panel will play at once, and never when the browser has been
asked to save data.

A tune-in is marked as one, so the plugin leaves it out of the stream's viewer count: Broadcast Box
reports each viewer's session by an id, and the plugin knows which of those were its own look-ins.

The pictures are small (480 × 270 JPEGs), kept in the plugin's memory only and never written
anywhere, and dropped when the stream ends. The server only accepts a JPEG of a sensible size and
shape, only from the browser it gave that stream's job to, and only for a stream that is live.
Until the first one arrives, a card shows the streamer's own picture blurred into a wash of its
colours, so no card is ever an empty black box.

### Keeping them clean

A preview is whatever the browser that took it sent. The plugin checks that it is a JPEG of the
right size and shape, for a live stream whose picture is due, from the browser it gave that job to,
and it logs who shared each one. What it cannot do is tell a frame of the stream from any other
picture, so somebody who has linked a stream (nobody else may send one) could, with some effort,
put a picture of their own on a card until the next real frame replaced it.

**Preview pictures in use**, under **Panel** in System Console, is the answer for a small
server: every preview showing right now, as a thumbnail, with the stream and who took it and when.

- **Remove** takes one down. The card goes back to the streamer's picture, and the next real frame
  replaces it.
- **Block**, followed by the person's @username (a second click to confirm), takes down everything that person has shared and stops
  them sharing any more. Everything else they do is untouched: they watch exactly as before. Blocks
  are kept in the plugin's store, so they last through restarts, until you **Unblock**.

Both act at once, without the page's Save button, and are logged with the admin's and the shared
picture's owner's user IDs.

**Show live preview pictures**, under **Panel**, switches all of this off. Cards then show the
streamer's picture instead, and no browser tunes in to anything.

### 24/7 channels

An always-on channel never gets a still: it is a channel, not a person, and what it shows is
already known. It shows its **card picture** instead, which an admin sets under **24/7 pictures**,
under **Streams** (see [24/7 stream pictures](#247-stream-pictures)). Without one, the card shows the channel's square picture like an album cover,
on a blur of itself.

### Twitch cards

While an admin has Twitch live switched on, somebody live on Twitch has a card here too. It is tagged
**Twitch**, shows Twitch's own count of its audience where a Broadcast Box card shows faces, says
what category Twitch lists it under, and carries **Watch on Twitch ↗**. Its picture comes from
Twitch, fetched and relayed by the server. Somebody live on both has only their Broadcast Box card,
with **Also on Twitch ↗**. See [How live is noticed](up-next.md#how-live-is-noticed).

## Clips

A **✂** on a playing stream keeps its last two minutes. The clip opens at once in an editor, where
it can be trimmed shorter, given a title and published to **Clips**, for everybody who can open the
panel to watch and everybody linked to upvote, and shared into a channel as a card. Whoever's clips
have the most upvotes is the **Best Clipper**, and wears a gold clapperboard beside their name.

It is **on unless an admin switches it off** (**Let people clip streams**, under **Clips**), and
having it on means this server records every live stream that is not a 24/7 channel. Read
[What the server records](#what-the-server-records) before upgrading a server that has streams on
it.

### What the server records

Broadcast Box keeps nothing that has been streamed, and a browser can only clip what it happened to
play, so the server keeps the recording itself. While Clips is on, the Mattermost server opens **one
playback session of its own to Broadcast Box for every live stream**, receives the stream as a viewer
does, and keeps its last **Clip length** — two minutes unless an admin chose another — on its own
disk, overwriting as it goes. It re-encodes nothing; a clip is a copy of what arrived.

- **Never a 24/7 channel**, and never a stream whose streamer has switched clipping of it off.
- **Streamers are told** while their stream is kept, and can switch it off or wipe what is held; see
  [For streamers](#for-streamers).
- **Where:** under Mattermost's file storage directory (`FileSettings.Directory`), in
  `ceo.villa.matty/` — `ring/` for the recordings, `drafts/` for clips not yet published, `clips/`
  for published ones (and `media/` for the pictures of [remembered titles](up-next.md#movies-and-games-optional)).
  Directories are made `0750` and files `0600`, so the files can be read only by the user the
  Mattermost server runs as.
- **How:** the server asks Broadcast Box's `/api/whep` for each stream exactly as it relays a
  viewer's request, and the picture and sound then arrive over UDP from Broadcast Box's media port.
  Broadcast Box's own viewer count includes it; the panel's does not. The connection is described in
  `LOCKDOWN.md`, and what can stop it under
  [When clipping does not work](#when-clipping-does-not-work).
- **Four streams at a time** at most. A stream keeps its place while it is on, and a free place goes
  to the most-watched stream waiting.
- **Switching Clips off** stops every recording at once, without waiting for Broadcast Box to answer,
  and wipes what was held.

It needs Mattermost's **File Storage System** set to **Local file system**. On any other, or when
that directory cannot be written, Clips switches itself off and the System Console says why.

### Who can watch, and who can clip

Clips follow the [lock](unlocking.md#unlocking-matty), and the server holds to it on every route under
`/api/v1/clips`, before it reads anything the request carries:

- **Linked** people have all of it: making a clip, listing and watching them, upvoting, reporting,
  sharing and deleting, drafts, the controls over clips of their own stream, and downloading clips
  while an admin allows it.
- **People let in** to watch can list clips and watch them, and see shared cards with their pictures.
  Nothing more: there is no ✂ on their tiles and C does nothing, the dialog has no Just ended, no
  drafts and no **Yours**, a clip's upvotes are a count rather than a button, and Share, Download,
  Report and Delete are absent rather than refused. Anything else they ask the server for directly is
  answered 403 `watch_only`.
- **Locked** people have no panel, so no clips, and every clip route answers them 403 `locked` — the
  same answer whichever clip they ask about (one that exists, one that has gone, one that never did),
  and it costs them nothing. A shared card shows them only what its post carries; see
  [Sharing a clip](#sharing-a-clip).

Admins are no exception in the panel.

- **Unlinking** keeps your drafts for the rest of their two hours, out of reach: link again, to any
  stream, within that time and they are back. Your published clips stay in Clips for everybody who
  can watch, but you cannot delete them until you link again; an admin can delete them meanwhile.
  Your upvotes stop counting until you link again (see [Best Clipper](#best-clipper)).
- **Moderation is the one exception.** Every system admin, whatever their own panel shows, can list,
  watch and moderate clips in the System Console; see [Moderation](#moderation).

### Making a clip

Press **✂** on a playing stream — on the picture, with the sound and full screen — or click the
picture and press **C**. The editor opens at once and cuts the last two minutes, with the cursor
already in the title box, so the moment can be named while it is cut. Ctrl+C and ⌘+C are still copy,
and C never ends up in the message box.

**Who may clip a stream**, decided by the server:

- somebody linked who is **watching it in the panel right now**: a tile of theirs playing it, which
  Broadcast Box still listed at the last refresh, or which they opened in the last minute, before it
  could;
- **its streamer**: whoever's link is to that stream's key now, watching or not.

Somebody who tuned in ten seconds ago can still clip the whole two minutes: what is cut is the
server's recording, not whatever their browser happened to show. Somebody who is not watching is
told "Tune in to this stream to clip it." Without the rule, one clip after another would let
somebody follow a stream minute by minute without ever being counted as watching it.

One press keeps the admin's **Clip length**, from thirty seconds to five minutes, ending at the
moment the press reached the server — even when the server is busy cutting other clips and this
one has to wait its turn. A stream that has been recorded for less than that gives what there is,
and the ✂ says so first: "Clip the last 47 seconds — all that has been recorded so far". A recording
starts at the first keyframe after the server tunes in, so for a stream's first few seconds the ✂
reads "Clipping starts in a few seconds".

**The end of a stream can be clipped for ten minutes.** When a stream goes off, or drops and comes
back, the last stretch that was recorded is held: the waiting tile's ✂ offers "Clip the last 2:00
before Dave went off", a tile whose stream has just come back offers "…before the stream dropped"
until the new recording has five seconds in it, and Clips lists it under **Just ended**. Only people
who were watching when it ended may clip it, and its streamer.

A clip is a **draft** until it is published: private to whoever made it, its video included, and kept
for **two hours**. **Later** (or Escape, or the ×) closes the editor and keeps it, under **Your
drafts** in Clips and as a count on the clapper. Three drafts each at most, and twenty on the
server. **Discard** takes two clicks.

### Trimming

A clip can be made **shorter, never longer**. The trimmer under the picture has a start, an end and
a playhead. Drag them, or use the keys: the arrows move a second, ten with Shift or with Page Up and
Page Down, and Home and End go to the ends; **I** and **O** set the start and the end at the
playhead, and **Space** plays and pauses. The start and the end move in tenths of a second and stay
at least five seconds apart, the shortest a clip can be. **Loop** is on, so the stretch you have
chosen plays round while you trim it.

**The start is exact in the panel.** A video file can only begin on a keyframe, so a clip's file begins
on the keyframe at or before the start you chose, and the clip remembers how far in your start is;
the panel's players begin there. The end is exact to the frame. Anything else that plays the file
shows the lead-in, up to one keyframe interval of it — and OBS's default **Keyframe Interval** of 0
puts keyframes about eight seconds apart. **Set it to 2 s**: a clip's file then starts at most two
seconds before its chosen start, recording starts sooner after a stream does, and the files are
smaller.

The title is up to 80 characters and may be left empty; an untitled clip goes by its streamer and
when it was made, "Dave, 3 Oct 21:04". A bearer token or stream key pasted into it never gets in:
yours becomes "my stream", the stream's own "the stream", as everywhere else in the panel.

**Publish** cuts the chosen stretch, without re-encoding, takes its picture in your browser from a
frame near its start, and puts it in Clips for everybody who can watch. Clicking it twice, or publishing
the same draft from two tabs, makes one clip, and a publish whose answer was lost on the way back is
looked for before it is offered again.

### Clips and voting

The **clapper** beside the trophy in the panel's header opens Clips, with a count of your drafts on
it and a dot when there are clips you have not seen. It is the one way in, whether or not anybody is
live. The end of a stream you were watching that can still be clipped is under **Just ended** in the
dialog, and on the stream's tile while it waits. Which clips you have seen is remembered in this
browser only.

Clips lists **Just ended** first, then **Your drafts** ("only you can see these, kept for 2 hours"),
then every clip as a card — its picture, length, title, the stream's tag, who clipped it of whose
stream and when, and its upvotes, as a thumbs up and the number, which can be pressed right there on
the card — newest first, or **Top** for the most upvoted, and **Yours** for the clips you made or
that are of your stream. For somebody let in it is only the list, with no Just ended, drafts or
Yours, and an empty one says "No clips yet." with no invitation to make one. A card says when its
clip goes once that is less than three days away.

A clip plays in the same dialog, in the **same player as a live stream** (from 3.3.0): the stream
tile's layout, with whose stream it was and the time ("0:12 / 0:45") on the line under the picture.
The controls are the tile's own and come up over the picture while you point at it or tab into it:
play and pause, the sound and its volume, **Download** when you may, and full screen. A scrubber in
the theme's accent runs along the picture's bottom edge. While the clip plays and nobody is pointing
there is only the picture, the name, the time and a slim line of how far it has got; while it is
paused the
controls stay up. A click on the picture plays or pauses. The timeline is the clip's own, from its
exact start to its end; it stops at the end with **Replay**. The keys, with the player focused:
**Space** or **K** plays and pauses, **←** and **→** go back and on five seconds (**J** and **L**
ten), **M** mutes and **F** is full screen. A file that is slow to come shows the tile's tuning burst
saying LOADING, and one that will not load says why on the tile's line, with **Try again**; when you
may download the clip, **Download** stays on the picture, the one control left there, so a clip your
browser cannot play can still be saved and played elsewhere.

Under the player:

- **The thumbs up**, the upvote, for anybody linked but its clipper (and never a bot); filled, in the
  theme's accent, once you have voted. It sends the server the state you want rather than a toggle, so a request sent
  twice on a poor connection cannot undo itself. For anybody else it is just the count.
- **Share**, below, for anybody linked.
- **Report** (two clicks), which puts the clip at the top of the admins' list and tells them in the
  panel.
- **Delete** (two clicks), for its clipper, the stream's streamer, or a linked admin. Who deletes it
  decides what happens to its upvotes, and its second click says which: "Click again — its 14
  upvotes stop counting" for the clipper or an admin, "Click again to delete — Bob keeps its
  upvotes" for the streamer.

**Download** sits in the player, while an admin allows downloads and the clip's streamer has not
turned them off; see [Downloading a clip](#downloading-a-clip). It is the one way the panel offers:
the picture has no menu of its own, so no **Save video as**. That deters downloading where it is off;
it cannot prevent it — whatever a browser can play, it can save.

Playing a clip with sound takes the sound from the tile that had it, and gives it back when the clip
is paused or closed, or stops because its file failed.

### Downloading a clip

From 3.2.1 a clip can be downloaded, **only if an admin switches it on**: **Let people download
clips**, under **Clips**, is off as it ships (from 3.0.0 to 3.2.0 there was no download button at
all; 3.2.1 made it a switch). With it on:

- **Who:** people who have linked their stream. People let in to watch can watch clips but not
  download them; locked people have no panel. Admins are no exception in the panel, and the System
  Console's **Clips on the server** has no download button.
- **Where:** **Download** in the clip viewer's player, the download arrow among its controls beside
  full screen (from 3.3.0; in 3.2.1 it was a pill after Share). Never on the cards of clips shared in
  channels, and never in the System Console, whose **Watch** plays clips in the same player without
  it.
- **Whose say:** a streamer can turn downloads off for every clip of their own stream, those already
  made and every one to come, whatever the admin allows; see [For streamers](#for-streamers).
- **The server decides**, per reader and per clip, and the clip says so (`canDownload`): the admin's
  switch is on, the reader is linked, the clip is still kept, and its streamer has not said no. The
  panel shows **Download** only when all four hold, and the server checks them again at every step.

Pressing **Download** says "Preparing…" on the line under the picture, while the arrow pulses, then
"Your download has started.", and the browser (or the desktop app, which may ask where to save it)
saves the file. Refusals are said plainly, in the theme's error colour on that same line: "Downloads are off.", "The
streamer has turned off downloads of their clips.", "This clip has gone — it expired or somebody
deleted it.", "Slow down a little.".

**How it is fetched.** A clip can be five minutes, a few hundred megabytes, so it never passes through
the page: the browser saves it straight from the plugin's own address, as it would any download.

1. The panel asks for a **ticket**, `POST /api/v1/clips/download-ticket`. A POST carries
   Mattermost's CSRF check, so no other page, link or picture in a post can ask for one in somebody's
   name. Every rule is checked; the ticket is for that person and that clip, good for **60 seconds**,
   one at a time per person, and kept only as its hash. Ten tickets per person per ten minutes.
2. The browser opens `GET /api/v1/clips/download?t=<ticket>`. Every rule is checked again; then the
   ticket is **spent**, so it works once, the download is written to the server log ("A clip was
   downloaded", with the person's and the clip's ids and nothing else), and the browser is sent on to
   a **download address** for that one download.
3. The download address serves the file: `Content-Disposition: attachment`, `video/mp4`, never
   cached, with ranges, so an interrupted download resumes. It is signed for that person, lasts ten
   minutes and is checked against every rule on every request. It exists because the Mattermost
   desktop app asks for a download a second time after its own save dialog, and a browser resumes
   from the same address; a spent ticket would refuse both.

Anything stale, spent or somebody else's gets a short page, "This download link has run out or was
already used. Press Download in Matty again.", and a refused request writes nothing.

**The file's name** is the streamer, the title (or "clip") and when the clip starts, in your own
Mattermost time zone: `Dave - that jump - 2026-10-13 2105.mp4`. A clip whose streamer has no name
to give is named after the server's name for the panel (`Matty - clip - …`). Characters a file name cannot carry,
and anything that could make a name read as something else, become spaces; the streamer's name is held
to 40 characters and the title to 80.

### Best Clipper

One person holds **Best Clipper**: whoever's clips have the most upvotes **in total, all time**. There
is no runner-up anywhere and no monthly one. The clapperboard is drawn the way the airtime crown is,
in the same gold, and goes beside their name in posts (after the airtime medals and the Top
Snooper's binoculars) and on their clips, and in their profile popover as a row of its own after the
Top Snooper's: **Best Clipper**, "All time · 142 upvotes on 9 clips". The Trophies card has a
**Clippers** tab with the holder, their score and their best clip to watch. It is not on Live now
cards. Like the medals, it is everybody's to see, locked or not: it names a person and a score, never
a clip.

In plain words:

- **A tie keeps the holder.** Somebody has to overtake, not draw level; among others level at the top,
  whoever got there first. One upvote is enough to hold it.
- **Upvotes outlive a clip that runs its course.** A clip that expires keeps counting for its clipper,
  and so does one its streamer deletes: a streamer taking a clip of themselves down should not cost
  the person who made it.
- **A clipper's or an admin's deletion takes the clip's upvotes with it.** Otherwise a clipper could
  delete a clip and keep its score, and an admin removing one for cause would leave its reward
  standing.
- **Only votes from people who are linked count**, from accounts that are active and not bots.
  Somebody who unlinks stops counting until they link again — and on a clip that expires, or that its
  streamer deletes, meanwhile, only the votes counting at that moment are kept, so theirs is gone
  for good.
- **The holder must be linked, active and not blocked.** A holder who unlinks loses it until they
  link again; the upvotes on their clips still standing come back with them.
- **An admin can reset somebody's score**: the upvotes they had banked are cleared, and the votes on
  their clips so far stop counting toward Best Clipper, though each clip still shows its upvotes.
- **The same moment twice is refused**: a clip that shares more than half of the shorter of the two
  with another of yours from the same stream, so one moment cannot be farmed for votes.
- **It is hidden while Clips is switched off**, as the airtime boards are while airtime is off, and the
  holder is kept for when it comes back.

### Sharing a clip

**Share** posts a clip into the channel you have open, **as you**, as a card. It is in the clip's
player, and in the editor once a clip is published, and it says where the clip will go — "Share in
Town Square", "Share with Bob" in a direct message, "Share in this group message"; the second click
posts it. Sharing is for linked people. Clips can be shared while Clips is switched off, as they can
be watched and upvoted.

The card shows the clip's title, its stream's tag, who clipped it of whose stream and how long it is,
with its picture and **▶ Watch** for anybody who can watch (linked or let in). Watch opens the panel on
that clip: the panel and the clip in the main window, and the clip in a popped-out window. Somebody
locked sees the same words, which come from the post itself, and no picture, and nothing is fetched
for them. Their Watch, or the picture, opens the lock with "Unlock Matty to watch this clip.", and
the clip opens once it is unlocked. In a window with no panel of its own, a popped-out thread, the
card says "Open Matty in the main window to unlock it." instead. Once the clip has
expired the card says "This clip has expired."; once it has been deleted, "This clip was deleted."
and nothing else, and its title, names and tag are taken out of the post as well.

**The card carries the clip's thumbs up**, as a pill after **▶ Watch**: the thumb and how many count
now, which is the same number the Clips dialog shows. For somebody linked who may vote, it is a
button that votes exactly as the dialog's does, filled once they have, and showing at once; if the
vote does not go through, it goes back and says "Couldn’t count that. Try again." A vote from the
card shows in the dialog, and one from the dialog on the card. For somebody let in, and for a
clip's own clipper, it is the count alone, drawn dashed, not a button ("Thumbs up on your clip").
Somebody locked sees no count, and nothing is fetched for them. The rules are the dialog's: only
linked people vote, a vote counts only while its voter is linked, nobody votes on their own clip.
Somebody else's vote reaches a card on screen within about 30 seconds: when the clips change, the
cards then on screen are asked again together, at most once every 30 seconds a page and never while
the page is hidden, so a busy evening of votes costs each open page two requests a minute at most.
Mattermost's own reactions under the post are a different thing and stay one: anybody in the channel
can add one, and a plugin cannot refuse them, so they could never be "only while linked". **No count
ever goes into the post itself**, so the phone, notifications and email never show one.

**The post's own message carries no title and no names** — "🎬 Matty clip · 0:45" and, on the next
line, "Watch it:" and a link. That message is what the Mattermost mobile app, the Threads list,
notifications, email and Mattermost's search show, so nothing anybody typed can mention people, link
anywhere or outlive a deleted clip there. The name in it is the server's name at the moment the clip
was shared: a post keeps the name it was made with. The link is Mattermost's **Site URL** followed by
`/plug/ceo.villa.matty/clip/<id>`; with no Site URL set there is no link line, and the server log
says so once.

- **On a phone**, the Matty app opens the link in its own panel. The Mattermost app cannot run
  plugins, so there the link opens Mattermost in the phone's browser, where you sign in once and the
  panel opens on the clip. On the first visit from a phone's browser, Mattermost shows its own page
  offering its app first: choose **View in Browser**. **View in App** hands the link to the
  Mattermost app, which answers "This link you are trying to open is invalid." The page comes once
  per browser, and admins can turn it off with **Enable Desktop App Landing Page**.
- **Links made before 4.0.0** name the plugin's earlier id, `/plug/ceo.villa.villanet/clip/<id>`.
  The cards of those posts work as before, and the Matty app opens the links, but in a browser such
  a link no longer opens the clip.
- **Only the web card shows that a clip has gone.** The mobile app, the Threads list, notifications
  and email go on showing the message, which never changes; its link then opens the panel's "This
  clip has gone — it expired or somebody deleted it."
- **The checks are the ones Mattermost would make if you posted it yourself**, because it makes none
  of them for a post a plugin makes: you must be in the channel and allowed to post in it, it must
  not be archived, a direct message's other person must still be active, and where direct messages
  are kept within teams, the people in one must still share a team. A channel shared with another
  workspace is refused, and Share is greyed out there: the post, its link and its card would go to a
  server whose people can never be linked here.
- **One clip can be in ten posts at most**, so that every one of them can be told when the clip goes;
  a post deleted since frees its place. A second click, a second tab, or a retry within a minute is
  answered with the post already made. Anybody linked can use a clip's ten places, so one person
  could fill them.
- **The post is yours**, with Mattermost's ordinary unreads and notifications. Its message cannot be
  edited, and nothing about its card can; see [Security posture](security.md#security-posture).
- **A post in a channel archived later** keeps what it had: Mattermost refuses every change there. An
  expired clip's card still says so, worked out from the clip's date, but a deleted clip's title and
  names stay in that post. The same goes for a direct or group message whose people no longer share a
  team while messages are kept within teams. The server log names any post it
  could not mark.

### Clip tags

A clip carries **what its stream was tagged with** when it was cut: the streamer's
[What's on?](unlocking.md#whats-on), or else the movie, show or game of their plan that was on — whatever the
stream's Live now card said at the press, or at the end for the end of a stream. It is the stream's,
not the clipper's: the clipper's own words are the title, and the tag is never edited afterwards. It
shows in the editor ("From Dave’s stream · Hytale · 21:02:17 – 21:04:17"), as a tag on the clip's
card, under the title in the player, where a looked-up title opens its little profile, beside the
title on a shared card, and in the admins' list. It is never in a shared post's message, and a
deleted clip's posts lose it with the title. A stream with no tag gives a clip with none.

### For streamers

Somebody whose stream is linked has, while Clips is on, **the scissors** in the
[footer bar](#the-footer-bar) (and, while an admin allows downloads, with Clips off too: see the
download switch below), with a dot that says whether clips of their stream are on: in the LIVE red
and breathing while it is being kept this minute, in the theme's accent while clips are allowed and
they are off the air, grey when they have switched them off. The scissors open **Clips of your stream** in a sheet, which
says what is kept — "Clips are on: the last 2:00 of your stream is kept on this server so viewers
can clip it." while they are live — and holds:

- **Allow clips of my stream**, a switch, on until you switch it off. Off stops the recording of your
  stream at once, wipes what was held and throws away every draft cut from it, whoever made it;
  every ✂ on your stream then says you have switched clipping off. The switch goes with the
  **stream**, not with you: unlinking leaves it as it is, another stream you link starts with clips
  allowed, and whoever links your stream next finds it as you left it.
- **Wipe the last 2:00**, a button, while you are live and recorded, or while the end of your stream
  is held. It takes two presses: armed, it reads **Click again to wipe**, in the theme's error
  colour. What is held goes, and so does every draft cut from it, and the recording starts again at
  your next keyframe. Published clips are not touched.
- **3 clips of your stream · View**, which opens Clips on them. You may delete any clip of your
  stream, and its clipper keeps its upvotes.
- **Let people download clips of my stream**, a second switch under the first one's notice, there
  only while an admin allows downloads at all, and on until you switch it off. Off, nobody can
  download any clip of your stream, those already made or any to come: "Nobody can download clips
  of your stream, those already made or any to come." It deletes nothing, and goes with the stream
  as the first switch does. Clips already made can still be watched and downloaded while
  **Let people clip streams** is off, or while Clips has nowhere to keep anything on the server, so
  this switch stays within reach then: the scissors and the sheet are still there, with this switch
  alone in it (the dot says off: nothing of your stream is being kept).

Relinking is not in this sheet: it has its own home, the link key, whether or not Clips is on.

All of it follows the link as it is now: somebody who unlinks, or whose stream somebody else has since
linked with its bearer token, loses it at once. A stream the panel has only matched to its streamer
by username (*Match stream keys to usernames*) is recorded like any other. If that streamer is
let in to watch, their footer says so above its Watch only bar — "Your stream is being kept for
clipping. Link it to switch that off." — but nothing can be switched until they link, and somebody
locked has no panel to be told in. An admin can wipe any stream's recording from the System Console.

### How long things are kept

| What | Kept | Then |
| --- | --- | --- |
| Published clips | **Keep clips for (days)**: 30 days unless an admin chose otherwise, 1 to 365 | Gone from every list the moment they expire; their files leave the disk within ten minutes. Their upvotes stay with their clipper. |
| Drafts | 2 hours | Then gone, and the editor says so |
| The end of a stream | 10 minutes after it went off | Then gone |
| A live stream's recording | Its last **Clip length** and no more | Overwritten as the stream goes on |

- **Lowering Keep clips for waits a day** before removing the clips it would remove. A 3 typed for 30
  removes nothing for 24 hours, the System Console says how many will go and when, and setting it
  back meanwhile keeps every one. Raising it applies at once.
- **A restart keeps only published clips**: the recordings, the ends of streams and every draft are
  lost when the plugin restarts, is upgraded or disabled, or the server restarts.
- **Switching Clips off** wipes the recordings and the ends of streams and throws away every draft.
  Published clips stay — listed and watchable by everybody who can watch, upvotable and shareable by
  people linked — until they expire.
- **Saving a new Broadcast Box URL** wipes what was recorded, which belongs to the old one, and keeps
  the drafts.
- **Removing the plugin** leaves the clips' files in `ceo.villa.matty/` under Mattermost's file
  storage directory, and the clips themselves in the plugin's store, like the rest of its data.
  Delete that directory by hand when removing the plugin for good.

### What it costs

- **Disk for the recordings**: about 45 MB a minute of each recorded stream at 6 Mbit/s, so about
  110 MB for two minutes with the margin a recording needs. Each is sized from its stream's measured
  bitrate, never for less than 12 Mbit/s and never past 1 GiB, and all of them, with the ends of
  streams, take at most 4 GiB.
- **Disk for clips**: published clips and drafts together at most **Space for clips (GB)** (20 GB), one
  person's clips at most a fifth of that (and never less than 1 GB), and 30 published clips each, 500
  in all. A clip is never more than the 1 GiB a recording can hold. Publishing is refused past any of
  these, with words that say which; nothing is deleted to make room.
- **Free space**: nothing new is recorded, cut or published below 2 GiB free, or 5% of the disk when
  that is more, and every recording stops below 1 GiB.
- **Processor and memory**: about 4% of one core and 25 MB for each 6 Mbit/s stream, as measured
  against Broadcast Box v2.0.2 while this was designed. Cutting a clip copies; it re-encodes
  nothing. Two clips are cut at a time; a third waits up to fifteen seconds and is still the moment
  that was asked for.
- **The network**: each recorded stream once more, at its full bitrate, from Broadcast Box to the
  Mattermost server, on the LAN.
- **Broadcast Box**: one more viewer per recorded stream, and a keyframe request to the publisher
  each time a recording starts, which Broadcast Box sends for every new viewer. The recorder itself
  never asks for one.

### When clipping does not work

The ✂ says why in a few words. Admins see the reason in the System Console, and in the panel once
every recording has been failing for five minutes.

- **The Mattermost server must be able to exchange UDP with Broadcast Box.** The recorder is a WebRTC
  peer like a viewer's browser: the stream comes over UDP from Broadcast Box's media port (its UDP
  mux port, if one is set) to a port the Mattermost server picks. It uses no STUN or TURN server and
  takes media from no address but those of the **Broadcast Box URL**'s host, so a firewall on the
  Mattermost host that stops that UDP, or Broadcast Box's `INTERFACE_FILTER` hiding that address,
  means nothing can be clipped. The ✂ then says "Clipping isn’t working on this stream just now", and
  the recorder tries again — after 2 s, 5 s, 15 s and a minute, then every five minutes — until it
  can. Watching is unaffected.
- **The Broadcast Box URL must be its LAN address**, as it should be anyway. A URL through the reverse
  proxy does not work, since the proxy is not where the media comes from, and neither does
  `localhost`, `127.0.0.1` or a name that resolves to a loopback address, unless Broadcast Box offers
  a loopback candidate, which it ordinarily does not. A public `NAT_1_TO_1_IP` that Broadcast Box
  advertises for viewers outside the LAN is ignored; the recording still finds the LAN address.
- **H.264 without B-frames only.** AV1, VP9 and H.265 streams, and H.264 with B-frames, read "This
  stream’s video format can’t be clipped yet"; for B-frames the admin is told "set B-frames to 0; do
  not use the NVENC UHQ tune". OBS streams H.264 over WHIP unless told otherwise.
- **A keyframe at least once a minute.** A recording starts at a keyframe and so does every clip's
  file, so a stream whose keyframes are more than 60 seconds apart is not recorded ("keyframes too
  rare"). **Keyframe Interval** 2 s, as above.
- **Local file storage only**, as [above](#what-the-server-records).
- **Safari is not expected to play the sound.** A clip carries the stream's Opus sound in an MP4,
  which Safari does not play; the player says "This browser will play this clip without sound."
  whenever the browser admits as much.
- **An OBS reconnect starts a new recording**, and so does a change of resolution or codec, Broadcast
  Box rewriting the stream's timestamps, or the disk falling behind. A clip never spans two; what came
  before stays clippable for ten minutes as the end of a stream. A stall of a few seconds without a
  reconnect does not: the clip shows the picture frozen, with silence.
- **Simulcast**: a publisher sending several layers is recorded at whichever layer Broadcast Box hands
  the recorder.
- **Very high bitrates give shorter clips.** A recording holds at most 1 GiB, about 26 Mbit/s for five
  minutes; above that a press keeps what fitted, and the editor says "Only 1:24 fitted — this stream’s
  bitrate is higher than clips are sized for."
- **A disk too slow to keep up** breaks the recording rather than holding up the stream's packets, and
  it starts again at the next keyframe; the admin's reason says "disk too slow".
- **More than four streams**: the fifth reads "Clipping is busy with other streams right now" until a
  place frees.
- **Broadcast Box counts the recorder as a viewer**: its own viewer count, on its player and broadcast
  pages and anything else that reads it, has one more for each recorded stream. The panel's counts and
  faces leave it out, and it is never anybody's [Top Snoopers](awards.md#top-snoopers) time.

### Moderation

Clips are looked after in **System Console > Plugins > Matty > Clips**, under **Clips on the server**, by
**every system admin, linked, let in or locked** — the one place somebody locked sees clips at all.
It acts at once, with no Save; anything that cannot be undone takes a second click; and it refreshes
itself every twenty seconds while it is open.

- **A summary**: how many clips, the space they take against the limit and each person's share, the
  free space, what the recordings take, when the next clip goes, who holds Best Clipper, and where the
  files are.
- **Recording now**, each stream being recorded — how much is held, at what bitrate, or why not — with
  **Wipe**; and **Held after the end**, each stream's end that can still be clipped.
- **Every clip**, reported ones first with **Clear reports**: **Watch**, which plays it in the row,
  in the panel's own clip player (never with Download),
  **Remove picture**, **Delete** (its upvotes stop counting), and **Block** followed by the person's
  @username, which stops that person making clips. A block throws away their drafts, refuses new ones, and keeps them from holding Best
  Clipper; their published clips stay until they are deleted one by one, and they can still watch,
  upvote and share.
- **People**, most space first, with their upvotes and **Reset score**; and **Blocked from clipping**,
  with **Unblock**.

The panel tells admins, and only admins, when Clips needs attention: storage it cannot use, a clips
document it cannot read (below), every recording failing for five minutes, clips that have been
reported, or a lowered **Keep clips for** about to remove clips.

**If the clips document ever cannot be read**, Clips stops writing it rather than replace it: nothing
is published, upvoted, deleted or swept until it is put right, and a copy of it is kept under
`quarantine_clips` in the plugin's store. Fix or delete the `clips` row for `ceo.villa.matty` in
`PluginKeyValueStore`, then disable and enable the plugin.

## As wide as the side panel goes

While the panel is open, Mattermost's side panel is as wide as it can usefully go — and it follows
the window: maximise Mattermost onto a widescreen monitor and the panel grows with it.

| Window | Side panel |
| --- | --- |
| under 1201px | Mattermost's own, already as wide as it goes there |
| 1400px | 464px |
| 1600px | 547px |
| 1920px | 867px |
| 2560px | 1140px |
| 3440px | 1140px |

It is **half the window, while leaving at least 1053px** for Mattermost's own sidebar and the channel,
never narrower than Mattermost's own widest (464px from 1201px, 776px from 1681px), and **never wider
than 1140px**. Mattermost itself stops at 776px however big the screen — which on a widescreen
monitor left the television a narrow strip down the side of a lot of empty channel — so above that,
the plugin lifts Mattermost's cap to match, for as long as the panel is open. It is worked out again
whenever the window changes size.

The 1053px is 2.7.2's. 2.7.0 left 960, which made the panel a little wide in a window; 2.7.1 left
1045, a 547px panel in a 1592px Mattermost window; 2.7.2 makes it 539px there,
where the Live now cards sit two to a row at a good size.

The 1140px is where the panel's own content stops: the player, Up next and Live now are at most
1100px wide, inside a little padding. 2.6.3 went on to half of any window, and on a 3440px ultrawide
that was a 1720px panel with the television in the middle of it and 300px of nothing on either side;
now the channel gets that room instead.

It is done entirely through Mattermost's own setting: the side panel's width is one CSS variable,
`--overrideRhsWidth`, which the panel and the spacer that makes room for it both read. So the channel
makes room the ordinary way, with Mattermost's own transition.

**The edge still works, and while the panel is open it is the panel's width it changes:**

- **Drag it narrower** and that width holds, in that browser, from then on. It is a width, not a
  switch: 1000px on a 2560px window stays 1000px, and in a window too small for it the panel is
  simply as wide as that window allows, and goes back to 1000px when the window does.
- **Drag it back out as far as it goes**, or **double-click it** (Mattermost's own "back to the
  default"), and it follows the window again.
- **A click that does not move it** changes nothing.
- **Threads, search and everything else in the side panel keep their own width.** Mattermost records
  every press of the edge as the width for all of them, so each press made while the panel is widening
  is put back the way it was. Close the panel and the side panel is exactly as wide as it was before.
- **Mattermost's own ⤢ Expand** makes the panel 60% of the window, as Mattermost does, up to the
  same 1140px and never narrower than it already is. (Mattermost's own expanded view stops at 960px,
  which, next to a panel wider than that, used to make it narrower and leave a strip of nothing
  beside the channel.) On a window of about 2230px or more the panel is already that wide, so there
  the Expand button is **hidden**, having nothing to add; on smaller windows, where it still widens
  the panel, it stays. The keyboard shortcut is Ctrl+Shift+. (Cmd+Shift+. on a Mac), and an expanded
  panel always keeps its button to collapse it again.
- **Mattermost's ✕ close button is hidden** whenever the panel is open, at every window size. The
  panel is opened and closed with its own button, the server's icon — in the App Bar, or in the channel header
  where there is no App Bar — or with Ctrl+. (Cmd+. on a Mac). The trade-off, chosen deliberately:
  on a phone-sized window, where Mattermost hides the App Bar and the panel covers the channel,
  there is then no button on screen to close it.
- **Popped out into a window of its own** (Mattermost's ⧉), the panel is exactly as tall as that
  window, less the side panel's own header, and its middle scrolls — and follows the window as it is
  made taller or shorter. Mattermost lays that
  window out at *least* as tall as the window rather than exactly, so before 2.7.2 a wide window —
  and so a tall television — ran the page past the bottom of the window, and the footer with the
  stream title and the link went with it. Nothing here widens a popped-out window: it is as wide
  as it is.

Below 1201px wide the side panel is left entirely to Mattermost, and a drag there is Mattermost's
own. 2.6.2 stood aside for good after any drag at all; that setting is cleared away, and everybody
starts afresh.

## Opening the panel by itself

**Open the panel automatically**, under **Panel**, is a dropdown: never, when somebody is live, or
every time.

Decided **once**, when Mattermost loads, rather than watched for. That distinction is the whole
design: somebody who closes the panel is then left alone for the rest of their session, instead of
having it reopened underneath them the next time a poll finds somebody live. It reopens on their
next page load, and not before.

*When somebody is live* is the one worth having. The panel appears when there is something to watch
and stays out of the way the rest of the time, which is most evenings.

**It opens for locked people too**, straight onto the [lock screen](unlocking.md#unlocking-matty): that is how
somebody with no token learns there is something to unlock. Opened by itself, the
lock never puts its bearer token bar in focus, so somebody typing a message when the page loads goes
on typing it in the message box, not into the token field.

The mechanism is the one the **Watch** button in a profile popover has always used — the `showRHSPlugin`
action that `registerRightHandSidebarComponent` hands back — so it is the same well-worn path, just
taken at load.

## A theme for everybody

**Give people your theme**, under **Theme** in the System Console, puts the people on a server on a
Mattermost theme an admin chooses. A fresh server has no theme to give: **Theme colours** is empty,
and nothing is applied to anybody until an admin pastes one in. Any Mattermost theme will do: set it
up as your own, copy it from **Settings › Display › Theme › Copy to clipboard**, and paste it there. The panel follows each person's theme in any case (see [How it looks](#how-it-looks)), so a
theme given here colours the panel as well as the rest of Mattermost.

Mattermost can already restrict which of *its* themes people use — `ThemeSettings` has
`DefaultTheme` and `AllowedThemes` — but those name built-in themes, so a custom one cannot be set
that way at all. What a custom theme actually is, is a **per-user preference** in the category
`theme`, and setting a preference is something a plugin can do. That is why this lives here rather
than in Site Configuration.

Applying it is three separate questions, kept separate because the answers differ:

- **New accounts** get it the moment they are created, before they ever see the default one. Nobody
  has chosen anything yet, so nothing is being overridden.
- **Everybody else** gets it when an admin presses **Apply to everyone now** — an explicit act that
  reaches into every account and replaces a choice each of them may have made, so it says afterwards
  how many people it touched.
- **Keep people on it** is a dropdown, set by default to *leave people alone — they can change it*.
  Set to *put them back on it every time they log in* it stops being a default and starts being a
  rule: somebody who picks another theme keeps it until their next login, and then loses it.

Two things that would otherwise go wrong:

- **A per-team theme wins over the global one.** The webapp reads the preference named for the
  current team first and only falls back to the unnamed one, so setting the unnamed one alone would
  leave anybody with a per-team override seeing no change whatsoever. Applying clears those.
- **Mattermost does not reject a bad colour, it turns it white.** `Preference.PreUpdate` silently
  replaces any value that is not `#rgb` or `#rrggbb` with `#ffffff`, except `image`, `type` and
  `codeTheme`. So **Apply to everyone**, in the System Console, names the values it would do that
  to *before* anything is applied — discovering it as a white sidebar across the whole server is a
  poor way to find out.

### Code blocks

A Mattermost theme cannot set the code block background. The `codeTheme` key accepts only four
highlight.js themes, and none of them is black:

| codeTheme | background |
| --- | --- |
| `monokai` | `#272822` — warm dark grey, the darkest option |
| `solarized-dark` | `#002b36` — dark teal |
| `github` | `#ffffff` |
| `solarized-light` | light |

On a black theme, even monokai's grey sits as a visible block on the page. Only CSS can change that,
so the plugin offers **Black code blocks**, under **Theme**: turn it on and code blocks render on
`#0a0a0c` with a thin border in the theme's own line colour, keeping the code theme's syntax colours
(monokai's read well on black).

That setting is the one place the plugin styles anything outside its own panel, which is why it
is off by default. It lives in its own `<style>` element and is removed when the plugin is
disabled, so turning it off reverts the client exactly.

## No signal, and changing channel

The panel always has a television in it. With nothing tuned in, the tile shows **analogue snow**
behind scanlines and a darkened vignette, with a **No Signal** plate in the middle, and **No
Signal** on the line under it beside an empty ring where a face would be. The plate is set in caps
by the stylesheet; the line below it is not, which is why the string itself is capitalised.

It has **no controls at all** — no switch, no sound, no full screen, not even greyed out. It used to
carry dead copies of them, so the panel would not change shape when a stream arrived; now that the
controls live on the picture and only appear when it is pointed at, there is no shape to keep, and
dead buttons over static were clutter. A stream that has dropped off the air gets the same screen,
because it is the same idea — with the power switch, so it can still be turned off while it waits.

**Switching on.** When the panel opens it **boots**: a bright scan line with a faint glow in the
theme's accent sweeps
down the whole panel, with one flicker as the power comes on, uncovering everything as it goes. The
set then comes on the way a CRT does — a line across the middle, opening out to the full picture
with a white flash — and only then is there snow. Neither plays again while the panel stays open;
they are what opening it looks like, not what every re-render looks like.

**Faint reception.** On a server that has a **No Signal ghost** picture, the No Signal screen every
so often seems to catch a channel for about half a second: the picture swims up through the static,
doubled and unsteady, and is gone. No picture is built in, so a fresh server's No Signal screen is
static alone until an admin uploads one, as the third piece of **Emblem, icon and ghost** under
**Name and artwork** in the System Console (see [Changing the artwork](#changing-the-artwork)); it can
be anything. **No Signal ghost**, beside it, sets how often — never, rarely (about once a minute),
occasionally (every twenty seconds or so, the default) or often (every few seconds). The timing is
jittered rather than on a beat, because a ghost that turns up every twenty seconds exactly is a
screensaver, not reception.

**Changing channel** plays the effect everybody over thirty remembers: the static comes up, a bright
band rolls down the screen, and it holds there until the picture arrives. It is **not on a timer** —
it ends when the stream is actually playing, so it never clears onto a black rectangle and never
lingers over a stream that is already up.

Three things about how it is built:

- A stream that is actually playing has **neither** the snow nor the scanlines. Both layers are
absolutely positioned and therefore paint on top of the video, so leaving them on meant scanlines
over somebody's broadcast — a television effect applied to the one thing that is not one. There is
nothing to dress up once there is a picture.

The snow is a **turbulence tile moved in whole jumps** rather than scrolled, because static does
  not drift, it re-randomises. It is moved with `transform` rather than `background-position`, since
  transform is composited and background-position repaints — this sits behind a video player and
  must not compete with it for the main thread.
- `prefers-reduced-motion` **stills it rather than removing it**: the texture stays, the movement
  stops. A no-signal screen with no static is just a black rectangle. The boot, the switch-on, the
  switch-off and the ghost are skipped outright — they are motion and nothing else.
- The lab could not show any of this, because its fake player connects instantly and the effect only
  exists *while* a stream is being found — under a millisecond, which is indistinguishable from not
  rendering at all. There is now a **slow to tune** control in the lab's Server row that takes four
  seconds, which is how the effect was actually looked at.

## Where the rows sit

The **Up next** rail moves, because the right place for it depends on what is on screen:

| Watching | Up next sits |
| --- | --- |
| nothing, or one stream | **under** the set, where it reads as "and after this" |
| two or more | **above** the set, under the voice row |
| anything, with **nothing planned** | **nowhere**, for everybody |

Two or more tiles is where the set stops being one thing to look at and becomes a wall; anything
under it is then below the fold. A tile on its way out still counts, so the row does not jump from the
top to the bottom while that tile is still sliding away. When the row changes sides, the set and the
Live now cards glide into their new places rather than jumping there.

If the row had keyboard focus when it moved, the row itself takes focus where it lands. If it went
because its last plan was just cancelled with its own ×, focus goes to the calendar in the footer bar,
which is the way to plan another, or to the panel itself for somebody who cannot plan. The × is
switched off while the cancel is on its way, which drops focus out of it, and that still counts as
focus in the row rather than focus that has moved on. These are two placements in the markup rather than one moved with
CSS `order`, because they are different positions in the reading order and a screen reader follows
the DOM.

### Rows that open and close

The voice strip comes and goes on its own — somebody joins Mumble — and so do the sheets that rise
out of the [footer bar](#the-footer-bar). Both used to be the kind of thing that appeared between
one frame and the next, shoving everything below it down the panel with no warning. They now slide.
That is not decoration: it is what tells a reader the panel gained a row instead of that they lost
their place in it.

Three things it has to get right, and each is a way the obvious implementation is wrong:

- **Not on first paint.** Opening the panel should not play an animation for every row that happens
  to have something in it. The first render is drawn at its final size and the transition is armed
  two frames later.
- **The content stays while it closes.** Rendering nothing the moment the row empties collapses an
  empty box — it would disappear and *then* animate. The last content shown is kept until it has
  finished closing.
- **It is not there when it is closed.** A closed row is `inert` and hidden from screen readers, or
  the panel keeps a tab stop and a heading nobody can see.

The height comes from `grid-template-rows` going `0fr` to `1fr`, which is the one way to transition
to a height nobody has measured. `max-height` would need a number picked in advance, and a rail of
cards has no such number.

## The player

### The tile that said it had the sound and did not

A tile could come up showing the speaker as unmuted while being silent, and only pressing mute and
then unmute would fix it. It happened whenever a tile that already held the sound renegotiated its
stream — reopening the panel, a stream dropping and coming back, pressing Retry.

Two effects write `video.muted`. The negotiation effect sets it to `true`, because browsers only
allow autoplay while muted. A layout effect sets it from whether the panel says this tile has the
sound. The code said the second one was "declared after the negotiation effect so it runs second and
wins" — and that is **not how React orders effects**. Every layout effect runs before every passive
effect, whatever order they are declared in. So on mount the layout effect unmuted, the negotiation
effect then muted for autoplay, and nothing put it back.

Pressing the speaker button worked because `takeAudio` writes `video.muted` directly, inside the
click.

The sound is now restored where the negotiation finishes — once pictures actually arrive — reading
the panel's current opinion from a ref, so the negotiation does not have to depend on it and
restart the stream every time the sound moves between tiles. The layout effect stays, for the case
it is really for: moving the sound from one tile to another, which has to happen synchronously
inside the click or both tiles are briefly audible.


The player deliberately has **no native video controls**. On a live stream a full-frame
click-to-pause surface is worse than useless: a viewer pauses by accident and sits looking at a
frozen frame of something that has moved on. A pause from any other source is resumed
automatically.

### The controls are on the picture

Under the picture there is one line: the streamer's **face, name, and what they are streaming**.
A stream title too long for the line **scrolls like a ticker** — a moment's rest to read the start,
then along at an even pace and round again, seamlessly, because the words are there twice with a
gap. A title that fits sits still; pointing at it holds it; the whole of it is in its tooltip; and
anybody who has asked their system for less motion gets an ellipsis instead.

Everything else appears only while somebody is **pointing at the picture**:

- a **power switch** in the middle — the way to stop watching. It plays the CRT switch-off, and the
  sound cuts the instant it is pressed. It replaced the × that used to sit under the picture.
- **mute, volume and full screen** along the bottom on a dark shade (and the Pop out link, **Open in a
  new tab**, for somebody linked, when an admin has set a public watch page; see
  [Why the pop-out link defaults to off](unlocking.md#why-the-pop-out-link-defaults-to-off)).

When they show is decided in script rather than by CSS `:hover`, because `:hover` lies on a touch
screen:

- **Mouse:** moving over the picture brings them up; leaving it, or keeping still for a couple of
  seconds, puts them away — but never while the pointer is on a control, so the volume cannot vanish
  in the middle of a drag. In full screen the cursor hides with them.
- **Touch:** a tap brings them up, a tap on the picture while they are up puts them away, and they
  go by themselves after a few seconds. **A tap that brought them up is never also a press on one of
  them** — without that, tapping the middle of a picture to see the controls would switch the
  stream off. Asleep, the controls take no clicks at all.
- **Keyboard:** tabbing to one brings them all up for as long as one has focus. That is
  `:focus-visible`, so a mouse click that leaves focus on a button does not pin them over the
  picture.

While a switch or the controls are up, the words on the screen (NO SIGNAL, TUNING) step back so the
switch has the middle to itself.

The sidebar can be popped out into its own window, so the video is capped at 55% of the window
height (and centred, at most 1100px wide) rather than simply filling the width. Without that cap a
16:9 video in a maximised ultrawide window computes to roughly 1900px tall and pushes its own
controls, the stream list and the footer below the bottom of the screen.

### Watching more than one stream

Clicking a stream card still means *watch only this one*, exactly as it always has. Once something
is playing, the round **+** in each card's corner means *watch this as well*, up to four tiles; it
turns into a **−** to drop that tile again — shown while two or more are playing. The last one left
is turned off with the power switch on its picture.

- **Exactly one tile has sound.** The speaker button is a radio, not a per-tile mute: taking the
  sound for one tile mutes every other in the same update. Two POVs of the same game are two
  microphones in the same room half a second apart, and hearing both is worse than hearing neither.
  Nothing is ever unmuted except by a click, which also keeps the browser's autoplay policy happy.
- **The grid sizes itself against the panel, not the window.** The sidebar is around 350px wide
  inside a 2560px monitor, so a viewport media query is the wrong instrument — it would hand the
  narrow panel the wide layout and produce four postage stamps. A container query on the panel
  gives one column docked and two once there is room for two tiles worth looking at. Tile height is
  capped by how many tiles there are (55%, 42%, 34% of the window) so two rows still fit on screen.
- **A tile does not vanish the moment its stream blinks.** It holds its place for 45 seconds
  on its No Signal screen, "Waiting for Dave", with a dashed line round it and its own power switch, because dropping it immediately
  reflows the grid and resizes every other POV over an OBS reconnect that will be over in ten
  seconds. Its sound is released straight away, so a tile that comes back never tries to unmute
  without a click behind it.
- **A brief origin hiccup no longer empties the panel.** A failed status read used to publish an
  empty list to every connected client, which tore down whatever everyone was watching. The last
  known list is now kept, with the error shown alongside, until three scheduled reads in a row have
  failed. A failed read made between them (after a save) shows the error but does not bring the
  emptying forward.
- **TUNING means tuning.** The tile's TUNING screen clears when pictures arrive, not when the SDP
  answer is accepted, and a connection that fails afterwards says so. A tile that negotiates happily
  and then receives nothing — which is what a paused publisher does, since it stays in the list and
  still accepts a WHEP exchange — gives up after twelve seconds, says "No video arrived from this
  stream." and offers **Retry**, rather than tuning for ever. With four tiles, one quietly showing a black rectangle is much
  harder to spot than it was with one.

**Closing a stream switches its set off.** The picture collapses the way a CRT's does — squashed to
a bright line across the middle, the line to a dot, the dot gone — and the sound is cut the instant
the close is pressed rather than when the effect finishes. With more than one tile up, the dark set
then **slides up behind** the ones still playing and is gone, and the rest glide into their new
places rather than jumping there. The glide is the same trick as before any browser had view
transitions: measure where each tile was, let the layout change, and animate each one from the old
spot to the new with a transform, so nothing re-lays-out while it moves. The Up next row glides the
same way when it changes sides.

Two things worth knowing. Each tile is a separate WebRTC connection pulling the publisher's full
encode — Broadcast Box has no route for requesting a smaller layer through this plugin — which is
why the cap is four. And because the origin counts viewers by peer connection, one person watching
four streams reads as a viewer on all four.

## When a thread takes the panel

Replying to a message opens Mattermost's thread in the side panel, in the panel's place, and
Mattermost gives a thread no way back to a plugin — it forgets which plugin was there. Turning
Collapsed Reply Threads off does not change that: replies open in the side panel either way.

So the panel remembers for it. **Once your reply is posted, about a second and a half later, the
thread closes and the panel is back**, still playing. **Closing the thread brings it back straight
away**, and so does closing search, mentions, saved or pinned messages, files or channel info if one
of those took its place. If you are still busy in the thread — typing, clicking, scrolling, a menu
or the emoji picker open, or a draft or an attachment left in the box — it waits for your next reply
or for you to close the thread. After it comes back by itself, a **Back to thread** button sits at
the top of the panel until you click elsewhere in the panel or move to another of its controls, or
for half a minute, and never goes while you are pointing at it or on it. A screen reader is told
"Reply posted in Town Square. Matty is back. Back to thread is at the top of the panel."; the keyboard lands on the panel itself, one Tab away from it, and never in the message box, where
a stray keystroke would be posted. A thread reopened with Back to thread brings the panel back when
you close it, not when you reply in it.

**While the thread is open, the stream keeps going in a small player in a corner of the channel** —
the stream that was in front, with its speaker, a button that hides the player, a power switch, and
the server's icon, which brings the panel back ("Back to Matty"). A **+2** in its corner, in the
theme's accent, says how many other streams are still in the panel. The player is a plain card in
the theme's colours with a soft shadow, and no frame round the picture. Drag it, or use its corner button (Enter cycles the corners, the arrow keys
jump to one), to move it; it remembers the corner. Where there is no room for the picture it shrinks
to a bar, and where there is not even room for that, or on a phone-sized window, it steps aside. It
never covers the channel header, the message box or the side panel, never takes focus when it
appears, and lets a dragged file through to the channel underneath. Ctrl+F6 (F6 in the desktop app)
reaches it as the region "Matty player". If the browser will not let its sound play, it says
**Turn sound on**, and one click does.

A [Twitch stream](up-next.md#watching-inside-the-panel) is never kept in the small player: Twitch's player can
only be reloaded, not moved, and it is Twitch's to show. When a Twitch tile has the sound, the small
player takes a Broadcast Box stream you are watching, if there is one, and otherwise does not appear.

Going back into the panel does not reconnect anything: no tuning, no burst of keyframes asked of the
streamer, no hit against the playback limit. Each stream's connection and its one video element
belong to the panel rather than to whatever is showing them, and are simply moved between the panel
and the small player.

**Not for somebody locked.** A person who has not unlocked the panel is never brought back after a
reply or a closed panel, and never gets the small player: there is nothing of theirs to bring back,
and the lock would only cover the thread they were using. See [Unlocking Matty](unlocking.md#unlocking-matty).

Closing the panel yourself, with its icon or Ctrl+., still switches it off, as it always has, and so
does changing team. The small player only appears while something else is in the side panel, never
after you closed the panel. Another plugin's panel is left alone: the panel does not come back over it,
and the small player goes when that panel is closed. A reply only brings the panel back in the tab
that sent it; replying from your phone leaves your desktop alone.

Streams you are not looking at are let go after a minute; the one in the small player is kept for up
to four minutes while the player is out of sight (under the Threads page, say), and a minute after
**Hide player**. Both behaviours can be switched off per person in **Settings › Plugin preferences ›
Matty** (the server's name for the panel):

| Setting | Choices |
|---|---|
| After replying to a message | As soon as your reply is posted (the default) · When you close the thread · Never, I’ll open Matty myself |
| While something else is in the side panel | Keep watching in a small player over the channel (the default) · Only watch in the side panel |

In a popped-out channel window the panel comes back the same way, without
the small player. A popped-out panel is a window of its own, and nothing takes its place
there.

**How it knows a reply landed.** Sending a reply puts a placeholder in the thread at once, carrying a
pending id that only this tab ever sees; the server's post replaces it carrying the same id. The panel
waits for that, so a reply that fails, a reaction, following a thread, a scheduled reply or a slash
command never brings it back, and a retry that succeeds does. All of this reads Mattermost's own store
and never changes how a message is drawn; the only things it asks of Mattermost are the three actions
its own buttons use — show a plugin's panel, expand the side panel, open a thread.

## 24/7 stream pictures

A person's stream wears their Mattermost profile picture. An always-on channel belongs to nobody, so
without a picture it wears its initials — "LF" in a circle, for a channel named Lo-fi radio (a
`radio = Lo-fi radio` line in **Stream names**). **24/7 pictures**, under **Streams** in the System
Console, gives each one pictures of its own: one row per saved 24/7 key, each with two pictures, each
with a preview, **Choose a file**, and **Remove**. No picture is built in for any channel. Every panel
picks a new picture up on its next poll.

- The **picture** is square, and shown wherever a face would be: on the tile, on the waiting
  screen, and at the bottom of the channel's card.
- The **card picture** is wide, 16:9, and fills the channel's card under **Live now**, where a
  person's stream shows a still of what is on. Without it the card shows the square picture like
  an album cover.

### Choosing the part of the photo to use

Choosing a file does not upload it. It opens the photo in a square with a **circle** drawn over it —
the shape it will be shown in — and the part outside the circle darkened. (For a card picture, the
frame is a 16:9 rectangle instead, and what is saved is 960 × 540.) **Drag** the photo to move
it, **zoom** with the slider or the scroll wheel to bring a face up to size, or use the arrow keys
and + / − from the keyboard; the photo always covers the square, so no edge can show. **Use this**
saves what is inside the circle; **Cancel** leaves the old picture alone.

The crop is made in the browser, on a canvas, and only the result is sent: a **512 × 512** square,
a JPEG for a photo and a PNG for anything that might be see-through. That is also what lets a big
photo straight off a phone be used at all — the size limit applies to the square, not to the
original. The **bot's picture** uses the same cropper, for the same reason: it is shown in a circle
too.

The stream key still never reaches the browser. The panel asks for a channel's picture by the
stream's **opaque handle**, exactly as it asks for its video, and only a system admin's settings page
names keys — to people who can already read them in the console. The picture is stored under a hash
of the key, not the key itself, and the version is part of the address, so a new picture is a new
address and the old one can be cached.

## Changing the artwork

**Emblem, icon and ghost**, under **Name and artwork** in the System Console, takes three images, and
they are three different jobs rather than one image used three times:

| Piece | Where | Seen at | Built in |
| --- | --- | --- | --- |
| **Panel emblem** | faded into the bottom corner of the panel, and behind the lock screen's padlock | about 400px, a fifth of it off the edge | none |
| **App Bar icon** | the button that opens the panel, the panel's header in front of its name, and the small player | 24px, the App Bar's own size | Matty's icon |
| **No Signal ghost** | the picture the No Signal screen half-catches now and then | the middle of the set, through static | none |

A detailed picture that works at 400px is unreadable at the App Bar's size, so there is no single
upload that would serve both jobs. The built-in icon is Matty's own, a black chat bubble on a white
disc, drawn as vector shapes so it stays sharp when small and reads on light themes as well as dark
ones. A fresh server has no emblem and no ghost: the panel has nothing behind it, the lock is the
padlock alone, and the No Signal screen is static alone, until an admin uploads them. The ghost wants
a transparent background: an opaque one shows as a pale square, while round a transparent one the
static shows through.

Each is chosen with **Choose a file** and saves **immediately**, without the page's Save button; the
others on the server see a new picture on their next page load, though open panels are told at
once when an emblem or a ghost comes or goes. The icon has **Use Matty's icon**, which puts
Matty's icon back; the emblem and the ghost show "None" until one is uploaded, and have **Remove**,
which leaves the server without one again. Both take a second click, since they delete the
upload. SVG, PNG, JPEG, GIF or WebP, up to 512 KB.

They are kept in the plugin's KV store rather than in the configuration, for the same reason as the
bot's picture: a few hundred kilobytes of base64 in a config value is written to disk, held in
memory and shipped to every node in the cluster on every configuration change, for a picture.

The App Bar icon is also the **channel-header** button, which Mattermost shows wherever there is no
App Bar: on the desktop when the App Bar is turned off, and always in the narrow header of a phone or
a small window, where the App Bar is hidden. So the two always match.

The **panel's header** is the icon and the server's name: the **Name** setting beside it (Matty
unless an admin chose another), in plain words set as Mattermost sets the titles of its own side
panels, with the panel's tools at the other end as Mattermost's own icon buttons. An icon uploaded
here changes the header with the App Bar, and a new name changes the header, the App Bar button's
tooltip and every sentence the panel says.

**One thing this does not change**, and the setting's own help says so rather than leaving you to find out: the
icon beside the plugin in the **App Marketplace** (under *Installed*), which Mattermost reads from the
installed bundle's `icon_path`, so only installing a new bundle changes it. It is Matty's icon, and is
not shown anywhere when the Marketplace is turned off.

### What is served, and how

All three are served from the plugin's own public path, like the font — a stylesheet's `url()` and the
App Bar's `<img>` should not depend on how a browser decides to attach cookies. The URL is
**stable** and only its contents change, which is what lets `registerAppBarComponent` — which is
handed a URL once, at load — pick up new artwork at all. The icon is at `/static/brand/icon`, the
emblem at `/static/brand/logo` and the ghost at `/static/brand/ghost`; the emblem's and the ghost's
answer 404 on a server that has none, and the panel, which is told which pieces the server has, does
not ask for them then.

That is also why they are sent as `no-cache` with an `ETag` rather than cached for a week: a long
cache would mean uploading new artwork and seeing the old artwork for seven days with no way to tell
why. The ETag makes the revalidation a 304 in the ordinary case.

An upload is identified **from its bytes**, never from the `Content-Type` the browser sent.
Serving an HTML file back as `text/html` from this origin because somebody labelled it `image/png`
is the whole reason. An SVG is a document and a document can carry a script, so SVGs go out with
`Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; sandbox` and `nosniff`.
Only a system admin can upload one and a system admin can already install plugins, so this is not a
privilege boundary — but the URL is reachable by anybody, and a link to it should not be able to run
anything against whoever follows it. The `sandbox` directive is there because it was *measured* not
to break the image: with those exact headers an SVG renders identically in an `<img>` and in a CSS
`background-image`, with and without it.

## The emblem behind the panel

On a server that has uploaded an emblem, it sits in the bottom-right corner of the panel at 6%
opacity and 400px across, a fifth of it hanging past the panel's edges. It is a background, not
content: it does not scroll with the list, it takes no clicks, and it is beneath every piece of text
in the panel. A server without one has nothing there.

It is screened onto the panel's ground: only its light parts show, so an emblem of light lines on
black surfaces faintly on a dark theme and all but vanishes on a light one, rather than greying it.

Four things that were not obvious:

- **It is anchored to `.vntv-panel`, a class only the sidebar carries.** The first attempt hung it
  on `.vntv` and excluded the two places it must not appear — the award badges in a post header and
  the block in the profile popover. Measuring the page found a third: the sidebar *header* is a
  `.vntv` root too, twenty-eight pixels tall, and it had a three-hundred-pixel emblem behind it.
  Naming the one place it belongs cannot go wrong when a fourth appears.
- **The panel is made a stacking context on purpose.** An absolutely positioned pseudo-element
  paints *above* the text by default, which is a logo thrown over the panel rather than under it.
  With `position: relative; z-index: 0` on the panel, `z-index: -1` on the mark puts it above the
  panel's ground and beneath everything else. Nothing that should escape is trapped: the
  leaderboard is a portal onto `document.body`, and the player's fullscreen uses the real Fullscreen
  API, whose top layer ignores stacking contexts.
- **The opacity was chosen by looking.** 0.03, 0.045, 0.06 and 0.09 rendered side by side on a
  black panel: 0.03 is barely there, 0.09 competes with the footer text over it. 0.06 is the one
  that was picked.
- **The panel is clipped, not hidden.** The emblem hangs 82px past the right edge, and
  `overflow: hidden` still lets a box be *scrolled* over what it hides — by `scrollIntoView`, by
  find-in-page, by dragging a text selection to the edge. Any of those slid the whole panel 82px
  sideways under its header with no scrollbar to put it back. `overflow: clip` cannot be scrolled at
  all.

The artwork is served from the plugin's own `/static`, like the font, so it costs no external
request, and it is whatever **Emblem, icon and ghost** has been given. The lab starts as a server
with no emblem and no ghost; the **emblem** and **ghost** switches in its **Artwork** row give it
placeholder pictures for each.

## Banners

A fresh server has no banner. With **Banner text** set, the panel has a banner at its top, and at its
bottom too unless **Banner placement** says top only: the server's words, exactly as typed, on one
line, centred, sized to Mattermost's own channel banner (24px tall, its 13px semibold words, no
capitals added). The bottom one is the strip under the [footer bar](#the-footer-bar), so the bar
stays level with the message box with the banner on or off.

**Banner placement** offers the same two choices as Mattermost's Classification Markings, so set it
the way your classification banner is set. A blank or unrecognised value counts as top and bottom.

**Banner colour** is a hex colour; left blank, the banner takes the button colour of each person's
Mattermost theme, with the theme's button words on it. The text colour is not a setting: for a
colour that is set, it is computed from the background with the same rule Mattermost uses for its
classification banner (`getContrastingSimpleColor` — relative luminance, black above `0.179`, white
below). Set the plugin's banner colour to the same hex as your classification banner and the two will
render identically, including the text colour.

So that a server's banner does not blink in on every page load, a browser draws the last banner the
server sent it until the server answers again; a browser that has never been told draws none.
