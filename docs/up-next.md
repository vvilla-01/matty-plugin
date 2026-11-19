[The Matty Plugin manual](README.md)

# Up next, Twitch, Steam and Hytale

## Up next

A streamer says what they are streaming and when, and it appears in a row of cards under the set —
**soonest on the left** — showing that moment **in each reader's own timezone**. (Above the set
when two or more streams are up; see [Where the rows sit](panel.md#where-the-rows-sit).) The row is there
**only while something is planned**, for everybody: with nothing planned it takes no room at all.
Everybody who can open the panel can read it; planning is for somebody with their stream linked —
see [Who can plan](#who-can-plan).

**The way in is the calendar in the [bar at the bottom of the panel](panel.md#the-footer-bar)**, there
whether or not anything is planned. Press it and **Plan a stream** rises out of the bar in a sheet ("It shows in Up next for
everybody"); press it again, or its ×, or Escape, and it closes. The plan it saves brings the row in.
The row used to keep itself on screen with nothing planned, as dashed placeholder cards behind a
half-card **+** at its left end: a whole row of the panel spent on nothing, so it went, and with it
the **+ Plan** that stood in for the row when two streams were up.

Each card is three lines, in the order people actually ask: **when, what, who** — and *who* is a
face rather than a name.

- The big line counts down while a countdown is worth having — "In 25 min", "In 3 hours" — and
  becomes a day name after four hours, because past that nobody plans by counting hours. The clock
  time is on the third line either way, so the big line never has to carry two ideas.
- The card for somebody who is **on air right now** says so, with the live dot. Only a card whose
  time has actually come: somebody streaming tonight has not started tomorrow's session, and
  labelling both of their cards "Live now" would make the row say the one thing it must never say.
- The **leading card is accented**, so the left-hand end of the row answers "what is on next"
  without anybody comparing times.
- **A card that says Live now is clickable**, and opens that stream on its own: anything else you
  were watching closes, which is the same gesture as clicking a stream card. Only the live ones —
  there is nothing to open for a stream that has not started.
- **A movie or a game, if you say so.** Two icons joined to the left of the text box; pressing the
  pressed one clears it. Saying nothing is the ordinary case and gets no icon — a third state, not a missing one, and
  every session planned before 2.0 is in it. Unknown values are dropped rather than drawn, so a
  category this build has never heard of shows no icon instead of a broken one.
- **Who is the streamer's profile picture**, beside the clock time. A face is recognised at a
  glance and a name has to be read. The name is still there for anybody who cannot see the picture:
  it is the picture's alt text and its tooltip. Somebody with no picture to be had gets their
  initials, never a broken-image icon. The picture comes from Mattermost's own
  `/api/v4/users/{id}/image` on the same server, so it costs no external request.
- Your own cards have an × on hover, whether you are linked or let in. A system admin can tidy up
  anyone's.
- A streamer's planned sessions also appear **on their profile**, in the popover under their name,
  read from the same snapshot in the reader's own zone. No extra request and no per-user endpoint.

### Moving along the row

There is **no scrollbar**. Chromium on Linux drew a full desktop one under the cards, steppers and
all — a heavier thing than the row it belonged to. Instead:

- **Arrows, one plan at a time.** A › at the right end moves the row on by exactly one card, and a ‹
  appears to go back once there is somewhere to go back to. Each card has a resting place just clear
  of the controls, so a press never leaves one cut in half. An arrow is only there when it has
  somewhere to go. Swiping, a trackpad and the keyboard still scroll the row as they always did.
- **Cards go under the arrows.** The row fades out at its left edge — a faint trace under the back
  arrow, solid from where a card lands — so a card moving left dissolves beneath the arrow rather
  than being sliced off by it. The right edge fades the same way under the forward arrow. Neither
  edge fades when there is nothing more that way.

The fade is a mask over the row itself, and the arrows sit *over* the row rather than in it:
anything inside the row would have faded with it.

### Two rows, and nothing to type a time into

The form is two joined boxes. **What**: the movie and game toggles on the left of the text box,
part of it rather than a row of their own. **When**: the time button, with a ✓ in the theme's
accent joined to its right end — the save button, greyed until there is a title, and the last thing in the form, where
the eye already is once the time is chosen. Enter in the text box saves as well (not mid-word in an
input method, where Enter commits the word).

There is **no Cancel**. The sheet the form sits in has its own × in its heading, the calendar that
opened it closes it again, and Escape closes it too; a fourth way to do the same thing would be a whole
row of button saying so.

A box for what you are streaming, and **one button showing when**. Press it and a **calendar** opens
under it; choose a day and the calendar becomes a **clock** — outer ring for the morning, inner ring
for the afternoon and evening — and then the same dial marked in **five-minute steps**. Choosing the
minutes is the last decision, so that closes it. Three taps, exactly the thing everybody already has
in their pocket, and the dial can be dragged as well as tapped.

It took four goes. A `datetime-local` field first — the technically correct control and a miserable
one, because the text is parsed as you type and the keyboard is the fastest known way to enter a
date that is wrong. Then three rows of buttons, which removed the typing and replaced it with three
decisions and a lot of furniture. Then two dropdowns, tidy but still asking somebody to scroll a
list of forty-eight times.

A moment outside the window is not rejected, it is **not offered**. The form hands the picker the
two ends — never the past, never more than the ninety days the server accepts — and the picker greys
out the days, hours and minutes that fall outside them, then slides forward to the first mark that
is inside when a choice would otherwise land outside. There is no error to see.

The week starts on the day the reader's locale starts it on, asked of `Intl.Locale` rather than
assumed; Monday where the browser will not say.

The server stores **one instant** — epoch milliseconds, UTC — and formats nothing, ever. Not a
wall-clock string, not a zone name: "Friday 8pm" is wrong for every viewer but its author, and wrong
for its author too when their offset changes between planning and streaming. All the conversion
happens in the browser with `Intl`, which is built in — the plugin is not allowed to fetch a
timezone database and does not need one.

Details that are easy to get wrong and are handled:

- Everything is read in **your browser's** zone — the form and the display both. That is not a
  shortcut: a `datetime-local` input is read and written by the browser in the browser's own zone
  and there is no way to hand the native picker a different one, so preferring a Mattermost timezone
  preference for *display* while the form still worked in the browser's meant somebody typed 20:00
  and saw "Saved as 01:45" with no idea which the stream would start at. One zone for both, and it
  has to be the picker's. It is also the more truthful of the two: Mattermost's *automatic* zone is
  a copy taken whenever the webapp last got round to it, so somebody who flew on Friday can still be
  carrying last week's zone on Monday, while the browser is right the moment they land.
- The button shows **what will actually be saved** before you save it. A time in a daylight-saving
  gap does not exist — 02:30 on the morning the clocks go forward is resolved to 03:30 — and you
  see that on the button rather than discovering it on the night. The zone is not named anywhere:
  everything is already in the reader's own, so printing "GMT+1" beside it answered a question
  nobody had.
- "Today" and "Tomorrow" are a **calendar comparison in the reading zone**, not arithmetic on the
  instant. Dividing by 86,400 seconds is wrong in every zone whose offset is not a whole number of
  hours — Adelaide, Kathmandu — and asking whether something falls on "now plus 24 hours" is an hour
  out for part of every clock-change day: on the Saturday evening before the clocks go forward it
  would announce Monday's stream as "Tomorrow".
- Nothing time-like that is *derived* goes in the snapshot — no formatted date, no countdown, no
  "starting soon" flag. It would change on every poll and push a WebSocket message to every client
  for ever.
- **The row does not snap.** Scroll snapping was tried and taken out: a snap container re-snaps to
  whatever it was showing when its contents change, so a stream planned sooner than all the others
  — inserted at the left, which is the one arrival this row exists to announce — scrolled the row
  one card to the right to hold the old view still.

The schedule is the only document ordinary users write, so it is a compare-and-set against the
exact bytes read, retried when it loses. A plain write would hand the last person to click the
document and drop everybody else's plan with a 200 OK.

Three per person, forty in all, ninety days ahead at most. An entry stays listed for three hours
after its start — that is when it is probably on air, which is exactly when people look — and then
disappears.

There was a **"what are we watching" vote** here until 1.17. It came out because a plain Mattermost
poll in the channel does the same job without a second place to look. Its two documents are deleted
from the store on activation rather than left to sit there unread.

### Changing a plan

Your own plans carry a **pencil** beside the ×, shown when the card is pointed at or focused and
always on a touch screen. It opens the same form in the same sheet, filled in — the words, the time,
movie or game, and the title it is linked to — headed **Change a plan** with the plan's name under
it, and the ✓ saves the changes. Only the person who made a plan can change it, and only while their
stream is linked — without it the pencil is gone and the server refuses the change, though the ×
still cancels it; an admin can still cancel anybody's. A plan that has already started keeps its
start time unless a new one is picked, so fixing a typo does not move it off **Live now**.

### Joining a plan

Somebody else's plan has a **Join** pill at the end of its last line for anybody whose own stream is
linked — joining means streaming in it, so it is for streamers. Pressed, it turns into a filled
**In ✓**, and pressing that again leaves — which works even for somebody who has since unlinked
their stream and is only let in to watch, so nobody's name is stuck on a plan. Everybody who is in
shows on the card as a row of faces tucked in behind the host's, the first three and then a count;
the profile of a linked plan lists them all by name, the host first.

### Sharing a plan

A plan can be posted into the channel you have open, **as you**, as a card, the way a clip is
shared. Two places offer it, both only to somebody linked: a **share** tool on every Up next card,
beside the pencil and the ×, shown when the card is pointed at or focused and always on a touch
screen; and, straight after you save a new plan, the plan sheet's **Planned.** line with **Share in
Town Square** (or wherever you are) beside it. Either says where the post will go, and the second
click posts it, as Share does for a clip.

**The card** is drawn for each reader from the list of plans their panel already has, in their own
time zone: the plan's art across the top when it is linked to a movie, show or game (a plan with no
art of its own has no banner, so cards differ in height), then its words, the host's face and name
and up to three of the people joining ("with Bob and 3 others"), the day and time, and how long until
it starts ("in 3 days", "tomorrow", "in 40 min", then **Due now**).

- **Join**, for a linked reader who is not the host, while there is room: the same Join as on Up
  next, which means streaming in it too. It turns into **In ✓**, which leaves. The host sees **Your
  plan**, and a plan with twelve joined says **Full**.
- **Watch**, once it is live (its time has come and the host, or somebody who joined, is on the
  air), with a red **LIVE** on the art and Join still beside it: it opens the stream in the panel, as
  a Live now card does.
  For a reader who has not unlocked the panel it opens the lock, with the stream waiting behind it.
- **Once it is over or cancelled** the card is dimmed and says only "This stream has been and gone."
  or "This plan was cancelled.", with no title and no names.
- **Somebody locked sees the card in full**: plans are on everybody's profile popovers already. They
  get no Join, and Watch opens the lock.
- In a window with no panel of its own, a popped-out thread, Watch says "Open Matty in the main
  window to watch." In compact message display the art is left out.

**The post's own message is "📅 Matty plan"** (with the server's own name for the plugin in place of
Matty, when it has one) and, on the next line, "See it:" and a link to
`/plug/ceo.villa.matty/plan/<id>` on Mattermost's **Site URL**, which opens the panel on that plan
in Up next. A plan shared before 4.0.0 says what it said then and links to
`/plug/ceo.villa.villanet/plan/<id>`; its card draws as before, and the Matty app opens that link,
but a browser opening it is no longer answered. Its props are the plan's id and its start time, and nothing else: never the title, the
host, the people joining or the art. So an edit to the plan shows on every card at once without the
post being touched, and once a plan is cancelled nothing of it is left in any post. The price is the
phone: the Mattermost app runs no plugins, so it shows the message and the link, and so do
notifications, email and the Threads list. A card read before the panel's first refresh says only "A
plan in Matty" and the time.

Sharing checks what Mattermost would have checked had you posted it yourself, exactly as a clip's
share does (see [Sharing a clip](panel.md#sharing-a-clip)); a plan cancelled a moment ago is refused with
"That plan isn't there any more."; the same share again within a minute is answered with the post
already made; and each person can share ten plans in ten minutes. Any plan on the list can be shared,
anybody's, since they are public. The post cannot be edited, and a post of the card's type that
the plugin did not make is shown as an ordinary message; see [Security posture](security.md#security-posture).

### Who can plan

**Planning is for streamers**: somebody with their stream linked by its bearer token (see
[How a person gets a red dot](unlocking.md#how-a-person-gets-a-red-dot)), the same rule that has always applied
to joining a plan. The server holds to it. Planning or changing a plan is refused at the
[gate](unlocking.md#what-the-server-holds-to-and-where-it-stops) to anybody not linked, and joining is refused
in its handler with *"Link your stream first — joining is for streamers."*, before any title is looked up. The panel does not
offer what would be refused:

- **Let in to watch**, there is no calendar in the bar (its line says Watch only), no pencil, no
  share and no **Join**. The row's cards start at its left edge, and the arrows land them clear of the back arrow.
- **Your own plans stay yours to cancel.** Somebody who planned a stream and has since unlinked
  loses the pencil but keeps the ×, so a plan they can no longer change is never stuck on every
  screen. Leaving a plan you joined works the same way. Both stay open on the server to anybody
  signed in, because they only ever take back the caller's own. A system admin who can open the panel
  can still cancel anybody's.
- **Reading is for everybody who can open the panel**: the row, every card, the title profiles. The
  plans on a streamer's profile popover are for everybody, locked or not.
- **The title search behind the form is for streamers only, too.** Asked directly by somebody without
  a link, it is refused before it is metered or reaches TMDB, Steam or IGDB, so nobody can have the server
  look titles up on their behalf by going round the panel.
- **Unlinking with the form open closes it**, since it could no longer save.
- **The panel opens already knowing.** Until its own check lands a moment after it opens, it goes by
  what the plugin learned about you when the page loaded. So a linked streamer's panel, bar and
  all, is there from the first frame, with no lock flashing up first. In a popped-out window that has
  only just loaded, before anything is known, a starting frame shows (blank, then the server's emblem
  faintly, when it has one) until the answer comes.

### An empty queue

With nothing planned there is **no row at all**, for anybody: the tiles and Live now get the room, and
the calendar in the bar is the way to plan one. Until 5 October 2026 an empty row showed the dashed
outlines of three placeholder cards behind a **+**, for somebody who could plan. It was a whole row of
the panel spent on nothing.

## Movies and games (optional)

**Off unless an admin switches it on.** With **Look up movies, shows and games** on, the title box
in the planning form becomes a search: choose the movie icon and it offers matching movies and TV
shows from **TMDB** (The Movie Database — IMDb has no public API, and TMDB links every title back
to its IMDb page); choose the controller and it offers games from the **Steam** store and, when an
admin has set up a Twitch application, from **IGDB**, for games Steam does not sell (Hytale,
Vintage Story).

- **As you type**, a list opens under the box in the panel's own dress — cover thumbnails in the
  shape the art really is (portrait posters and box art, Steam's wide capsules), the part of each title that
  matches what you typed underlined, and the kind and year underneath. The arrow keys move through
  it, Enter links the highlighted one, Escape closes it and keeps what you typed. The last line
  says so in words: **Keep “…” as my own title**. Picking is never required.
- **Linked**, the title shows under the box with its thumbnail and a **Linked** tag, and an × to
  unlink. The words stay yours: "Movie night: Heat" linked to *Heat* (1995) is fine.
- **On the card**, a linked plan wears its art faded in behind the words, darkest on the left where
  the words are.
- **Pointed at, focused or tapped**, a linked card opens a little **profile**: the poster or box
  art over the blurred backdrop, what it is and when it came out, a line of facts (genres and
  running time, seasons, or the developer), a few lines about it, who is in, and a link to its
  **IMDb** page, **Steam** store page or **IGDB** page. It follows the card as the row moves. A tap on a live
  linked card opens the profile first, and its **Watch now** opens the stream. From the keyboard,
  focusing a card opens its profile, Enter moves into it, Escape comes back to the card; on a live
  card, whose Enter watches, the Down arrow is the way in.

- **What's on?**, beside the stream title in the bar, uses the same toggles and the same search
  for what you are streaming right now, and the same profile opens from the title on your Live now
  card and under your picture. See [What's on?](unlocking.md#whats-on).

Movies and shows need a **TMDB read access token** — free: make an account at themoviedb.org,
then Settings → API, and paste the *API Read Access Token*. Without one, games are still looked up
and movies are not. Where TMDB data is shown it says so, as TMDB asks.

### The Twitch application

**IGDB**, Twitch's game database, knows the games Steam does not: ones that were never on it, like
Hytale and Vintage Story, and ones Steam no longer sells. It needs a **Twitch developer
application**, which is free and lives in the plugin's own **Twitch** section, where
[Twitch live](#twitch-optional) finds the same one. Sign in at dev.twitch.tv/console with a Twitch
account that has two-factor authentication on, open Applications → Register Your Application, give it
a name, and set OAuth Redirect URLs to the address **Twitch connections** shows at the end of the
Twitch section (Twitch live needs it; for IGDB alone `http://localhost` will do, since IGDB does not
use it). Pick any category and set Client Type to Confidential. Press Manage, copy the **Client ID**, press
**New Secret** and copy the **Client Secret**, and paste both into System Console > Plugins >
Matty > Twitch. With both filled in and lookups on:

- **Steam stays first**, and IGDB fills in. A game Steam lists for the same search is left to Steam,
  so nothing appears twice and Steam's page is the link; a game Steam no longer sells, which its
  search never lists, comes from IGDB. A game typed in full comes first wherever it came from.
- **IGDB's results say so**: "IGDB" in the list, and "Data from IGDB" in the profile, which links to
  the game's page on igdb.com. Add-ons, editions, bundles and mods are left out, and so are games
  IGDB files as erotic.
- **The access token** Twitch issues for the application is kept in memory only, renewed before it
  runs out, and never logged or written anywhere. The server keeps to IGDB's limits (4 requests a
  second, 8 at once) on its own, and leaves IGDB alone for a moment when it is slow or failing, so
  a game search then costs exactly what Steam costs. The list then says its games are from Steam
  alone, and asks again a moment later, rather than claim a game is on neither.

Leave both Twitch boxes empty and games come from Steam alone, exactly as before, and nobody can
connect Twitch.

### Remembered titles

What the server looks up is remembered in the plugin's store in the
Mattermost database, so the same search or title is not looked up again: a search for seven days
(three for IGDB's, one when it found nothing), a title's details for thirty days (seven for an IGDB
game, seven for a show still running, three for anything not out yet or in early access), and "no
such title" for a day. When one of those is due again and TMDB or Steam cannot be reached just then,
the old answer stands in for up to ninety days; IGDB's never does — an IGDB search is not served
past its three days, nor an IGDB game past its seven, even while IGDB is down. A title the provider
says it no longer has is believed the second time it says so, an hour or more after the first. What
the database keeps is words and where the pictures are — never the pictures themselves, and never a
token or a password — and it is bounded at about 2 MB, the least recently used going first,
searches before titles.

The **pictures** are kept on the server's own disk, so that none is fetched from TMDB, Steam or IGDB
twice, even after a restart: one file per title and size in `ceo.villa.matty/media/` under
Mattermost's file storage directory (beside where Clips keep theirs), named by a hash rather than
the title, readable only by the Mattermost server's user. A picture is kept as long as its title is
(thirty days, seven for an IGDB game, less for a title still changing). Past that it is fetched
again; if TMDB or Steam cannot be reached just then, the old picture stands in for up to ninety days,
while an IGDB picture is never served past its seven days, and is removed when it is next asked
for or by the tidy that runs, at most hourly, as pictures are written. They take **at most 256 MB**
(4,096 pictures), the least recently used going first, and none is written that would leave the
disk with less free space than Clips keep (2 GB, or 5% of a large disk). This needs Mattermost's
**File Storage System** set to **Local file system**; on any other, or when that directory cannot
be written, pictures are held in memory only, as before, and the server log says so once. In a
cluster whose nodes share that directory, each picture is written whole or not at all.

While lookups are off, nothing remembered is read or added, on the disk or in the database, and
the pictures already kept are left where they are; switched back on, the ones still fresh are
served again. The **Remembered titles** card in the settings shows how much is kept, pictures
included, and has a **Forget remembered titles** button, which clears the pictures as well. Like all
of the plugin's data, the remembered titles stay in the database and the pictures stay on the disk
if the plugin is removed, so press it first when removing the plugin for good.

## Twitch (optional)

**Off unless an admin switches it on.** With **Show people live on Twitch** on, people who stream on
Twitch can connect their Twitch account on the **Twitch** row of **Connected accounts**, in the sheet
the link key in the panel's [bar](panel.md#the-footer-bar) opens. While they are live there they wear the live badge, have a card in
Live now with their Twitch title, category, viewer count and picture, and their time live on Twitch
counts toward the airtime boards and medals. A second switch, **Play Twitch streams inside
Matty**, also off by default, plays their stream in the panel in Twitch's own player; without it,
a Twitch card opens the stream on twitch.tv.

### Setting it up

1. Register a Twitch application, as [The Twitch application](#the-twitch-application) describes.
   If IGDB already has one, it is the same one: Twitch live uses the same Client ID and Client
   Secret.
2. Paste its Client ID and Client Secret into the **Twitch** section of the settings.
3. Add the **OAuth Redirect URL** that **Twitch connections**, at the end of that section, shows to
   the application's OAuth Redirect URLs, exactly as shown, and press Save on Twitch's page. It is
   `<Site URL>/plugins/ceo.villa.matty/api/v1/twitch/callback`, so Mattermost's **Site URL** must be
   set, and Twitch needs it to be https (only `http://localhost` is allowed otherwise).
4. Switch on **Show people live on Twitch**, and, for the player in the panel, **Play Twitch streams
   inside Matty**.

### Connecting

Press the link key in your bar, then **Connect** on the **Twitch** row, the one with Twitch's
Glitch. The sheet turns into Connect Twitch, which says what connecting means: who sees what, and
what this server keeps. Press **Continue on Twitch**. A small window opens at
Twitch, where you sign in and approve this server's Twitch application. It asks for no permissions; it only proves the
channel is yours. The window says when it is done, and closes itself where the browser lets it; the
sheet then shows your Twitch name. If no window appeared, the sheet says after four seconds to allow
pop-ups for this site. If Twitch shows the wrong account, sign out of Twitch in that window first.

One Twitch account per person. If somebody else proves the same account, it moves to them, as a
stream key does, and you see a notice over your bar saying so. Only people who have unlocked
the panel with their own bearer token see it, since only they have that bar: somebody let in or
locked is not told in the panel, and the account has already left them, so there is nothing to
disconnect. The notice is kept for up to 30 days, so it is usually there if they unlock in that
time. It goes sooner if the account is disconnected from its new owner (by them or by an admin) or
its Twitch data is deleted, and it is not shown while an admin has blocked them from connecting.
If a Twitch connection fails, do not post the window's address anywhere: it can carry a one-time
sign-in from Twitch.

**Only people who have unlocked the panel can connect.** Connecting makes somebody a streamer whom
everybody sees live, which is more than watching, so it needs the panel unlocked with your own bearer
token: somebody let in to watch, or locked, has no Connect. And **a Twitch connection is not a link**.
It never unlocks the panel or lets anybody in, and it opens none of planning, What's on?, Top
Snoopers or anything in Clips. A streamer who is only on Twitch needs a Broadcast Box bearer token
like everybody else.

**Disconnecting is open to everybody who has connected.** **Disconnect** on the Twitch row (two
presses) removes the connection at once, even while an admin has Twitch switched off. Somebody who
is locked has a small **Disconnect Twitch** on the lock screen, under the bar, and somebody
let in to watch has the same over their Watch only line, for an account they connected while they
were linked. With Steam or Hytale connected too, it is the word Twitch on one Disconnect line, which
keeps Twitch's own words and its warning about your Twitch hours.

### What everybody sees, and what the plugin keeps

This is the privacy notice Twitch asks for.

While you are live on Twitch, **everyone on this server** sees your Twitch title, category, viewer
count and preview picture in Live now, your live badge, and your Twitch login (in the Watch on Twitch
link). Your Twitch hours count on the airtime boards and medals beside your Broadcast Box hours.

The plugin keeps your Twitch user id, login and display name, when you connected, and how long you
have been live on Twitch since. The token Twitch issues is used once, to ask Twitch who you are, and
revoked at once. The plugin never keeps a Twitch password, token or e-mail address, and sets no cookie
for this. Your stream's title, category, viewer count and picture are held in memory for a few
minutes at most. Your name is checked with Twitch once a day while the switch is on, and an account
Twitch no longer knows two days running is disconnected.

**Disconnect** deletes all of it, including your Twitch hours, so the boards and past months' medals
are worked out again without them. A minute you were live on Broadcast Box and Twitch at once is a
Broadcast Box minute, so you keep it. **Removing this server's Twitch application in Twitch's
Settings > Connections does not disconnect you here** (the plugin keeps no token, so it cannot see
that); press Disconnect as well.

One exception to "deletes all of it": if the plugin ever found its Twitch data damaged, it set aside a
copy for the admin, which may still hold your Twitch name and hours until the admin uses **Delete all
Twitch data** (the admin's Twitch connections card says when such a copy exists). Nothing is shared
with anyone outside this Mattermost server.

### What an admin controls

Anybody who has linked their stream can connect. In System Console > Plugins > Matty > Twitch, on
the **Twitch connections** card, an admin can disconnect a person, **Disconnect and block** them from connecting again,
or mark a channel **24/7** (tagged 24/7, no live badge, no airtime, like an always-on stream key).
The card also shows the OAuth Redirect URL with **Copy**, whether the Client ID and Secret are set,
whether Twitch's player can run at this server's address, when Twitch was last asked and how many
connected people were live, the blocked list with **Unblock**, the Twitch hours held, and **Delete
all Twitch data**, which disconnects everybody and deletes every Twitch hour, name and id the plugin
holds, set-aside copies included, and keeps the block list. All of it works whether or not the
switch is on, so connections can always be taken away. At most 500 people can be connected at once.

Twitch no longer marks mature streams, so the plugin cannot hide them; blocking is the remedy.

While the switch is off, nothing is asked of Twitch, nobody can connect, and no Twitch card shows.
Connections and Twitch hours are kept, the hours left off every board, and they come back when it is
switched on again. Delete all Twitch data is the way to remove them for good.

### How live is noticed

The Mattermost server asks Twitch once a minute which connected people are live, and only while the
switch is on and somebody is connected. A card appears within a couple of minutes of going live,
since Twitch's own list can lag that much. When Twitch cannot be reached, the cards stay up for five
minutes, then go, and system admins see a line in the panel saying so; Broadcast Box streams never
wait on Twitch.

**On the card.** A Twitch card is a Live now card like any other, with three differences. It says
**Twitch**, the plain word, as Twitch's agreement asks of whatever shows its streams, with no Twitch
logo and no Twitch purple. (The one place the panel shows Twitch's mark is the Twitch row under
Connected accounts, the Glitch, drawn unaltered and in the sheet's grey.) Where a Broadcast Box card shows who
is watching, it shows Twitch's own count, its whole audience ("1,234 watching on Twitch"), because
the plugin cannot see who is watching inside Twitch. And it carries the category Twitch lists the
stream under, unless What's on? or a plan says what is on, and **Watch on Twitch ↗**, the way back
to the source Twitch asks for. Its picture is fetched from Twitch by the server, held for two
minutes and relayed, so no browser fetches it, and it follows **Show live preview pictures**.

**Live on both?** You get one card, the Broadcast Box one, with **Also on Twitch ↗**, one badge, and
each minute counted once. The change from two cards to one, or back, waits for the same missed checks
as a badge does, so a reconnect does not make it flicker, and a Twitch stream somebody is already
watching keeps playing.

### Watching inside the panel

Only with **Play Twitch streams inside Matty** on. The stream plays in Twitch's own player. Your
browser then talks to Twitch and the services Twitch uses, including advertising and audience
measurement, and Twitch may show ads. Twitch sees this server's address and your IP address and, if
you are signed in to Twitch in that browser, who you are. In the desktop app, connecting Twitch signs
Twitch in inside the app, so its players then know you too. Nothing loads until somebody presses play
on a Twitch stream.

Twitch's player needs a site on https at the standard port, under a name rather than an address, and
a space at least 400 by 300. In the side panel that means a panel about 440 px wide, which Mattermost
allows only in windows at least 1201 px wide. In a narrower window, watching a Twitch stream opens it
in a larger player over the page, in the same click: **Escape**, the ×, or a click outside it closes
it. On a page Twitch's player cannot run on at all, such as a Mattermost reached at `:8065` or by
address, a Twitch card opens the stream on twitch.tv instead, as it does with the switch off.

A Twitch tile is a bar and the player under it. The bar says whose stream it is and how many are
watching on Twitch, and holds the sound, **Reload**, **Play bigger**, **Watch on Twitch ↗** and the
×. Nothing the panel draws ever sits over Twitch's player, as Twitch asks. In a grid, a Twitch tile
takes a whole row of its own, after the Broadcast Box tiles. When the panel is dragged too narrow
for the player, the tile says so, with **Play bigger**, and it goes back to the player only once
there is a little more than enough room again, so dragging the edge back and forth does not keep
reloading it. While the larger player has the stream, the tile offers **Bring it back**.

Moving the sound to or from a Twitch tile reloads Twitch's player, which can replay an ad: Twitch's
plain player can only choose its sound as it loads, and its script, which could do better, cannot
run on Mattermost's page. The panel cannot see Twitch's own mute button, so a Twitch player you
unmuted yourself stays audible. Twitch streams are never kept playing in the small player while a
thread has the panel. They cannot be clipped, give no preview stills, show no faces, and do not count
on Top Snoopers.

## Steam (optional)

**Off unless an admin switches it on.** With **Show people's Steam status** on, people who have
unlocked the panel can connect their Steam account with **Connect** on the **Steam** row of
**Connected accounts**, in the sheet the link key in the panel's [bar](panel.md#the-footer-bar)
opens. From then on, everyone who can open the panel sees their Steam name, whether Steam says they
are online, busy or away, and the game they are playing, in their profile popover and, while they
are in a game, on the **Playing** line under In voice: "Dave · ELDEN RING", with the person's
Mattermost name. The line shows nothing at all while nobody connected is in a game. The popover's
Steam row has a small arrow button to the person's Steam profile, an ordinary link that opens
steamcommunity.com only when pressed. Statuses follow Steam within a minute or two.

**Setting it up.** (1) Make a Steam Web API key at steamcommunity.com/dev/apikey, signed in to the
Steam account that will own it, with the domain name of this server's Site URL (for example
`mattermost.example.com`); Steam gives keys only to accounts that have spent at least 5 US dollars and use
the Steam Guard Mobile Authenticator. (2) Paste it into **Steam Web API key**, in the **Steam**
section of the settings. (3) Switch on **Show people's Steam status**. There is no redirect address
to register: Steam's sign-in sends people back to
`<Site URL>/plugins/ceo.villa.matty/api/v1/steam/callback` on its own, so Mattermost's **Site
URL** must be set, and on https (only `http://localhost` is allowed otherwise). **Steam
connections**, at the end of that section, shows that address and says whether the key works.

**Connecting.** Press **Connect** on the Steam row, read what it means, then press **Continue on
Steam**. A small window opens at Steam's own sign-in page, where you sign in (with your password and
Steam Guard, or the Steam app's QR code) and press **Sign in**. The plugin never sees your password.
The window says when it is done, and closes itself where the browser lets it; the row then shows
your Steam name. If no window appeared, the sheet says after four seconds to allow pop-ups for this
site. If Steam shows the wrong account, sign out of Steam in that window first. One Steam account
per person; if somebody else proves the same account, it moves to them, and you see a notice over
your bar saying so, kept for up to 30 days. If a connection fails, do not post the window's
address anywhere: it carries Steam's one-time sign-in and your Steam ID.

**Your profile must be public.** Steam tells other apps your status and game only while your Steam
profile and its game details are public (in Steam's privacy settings). With a private profile,
people here see you as offline, and your own row says so: "Private: people here see you as
offline".

**What everybody sees, and what the plugin keeps.** This is the privacy notice Steam's terms ask for.
Everyone who can open the panel on this server (people who unlocked it, and people an admin let in to
watch; never anybody locked) sees your Steam name, a link to your Steam profile, whether you are
online, busy or away, and the game you are playing. The plugin keeps your Steam ID, your Steam name
and when you connected, in this Mattermost server's database. While
you are connected the server asks Steam about you once a minute; your status and game
are held in memory only, for a few minutes at most, and never written down. The plugin never sees or
keeps your Steam password, and keeps no avatar, real name, location, game history or playtime.
**Disconnect** deletes all of it at once, even while an admin has Steam switched off, including any
notice that names your Steam account. Nothing is shared with anyone outside this Mattermost server.
Two exceptions to "deletes all of it": if the plugin ever found its list of Steam connections damaged,
it set aside a copy for the admin, which may hold your Steam name until the admin uses **Delete all
Steam data** (the admin's card says when such a copy exists); and when you connect in a browser,
Steam sends you back to an address that carries your Steam ID, which this server's web proxy may
record in its access log, where Disconnect cannot remove it (the admin can keep that path's query out
of the log; `LOCKDOWN.md` says how). Connecting in Matty does not send that address to the server.

**If the plugin is removed.** Like all of the plugin's data, Steam connections stay in the database
if the plugin is removed. An admin removing the plugin for good, or stopping using Steam's Web API,
presses **Delete all Steam data** first.

**As Steam gives it.** Steam's information is shown as Steam reports it, "as is": neither Valve nor
this server promises that it is right, complete or current, and neither is liable for it. Matty
Plugin is not made, endorsed or supported by Valve or Steam. Steam's Web API Terms of Use:
steamcommunity.com/dev/apiterms.

**Disconnecting is open to everybody who has connected.** **Disconnect** on the Steam row (two
presses) removes the connection at once, whatever the switch says. Somebody locked, or let in to
watch, has it on the Disconnect line under the lock's bar or over their Watch only line.

**What an admin controls.** Anybody who has unlocked the panel can connect. In System Console >
Plugins > Matty > Steam, on the **Steam connections** card, an admin can disconnect a person, **Disconnect and
block** them from connecting again, unblock them or clear the block list, and **Delete all Steam
data**, which disconnects everybody and deletes every Steam ID and name the plugin holds, set-aside
copy included, and keeps the block list. A connection somebody was still in the middle of making when
it was pressed is refused when Steam answers, so nothing is written after it. All of it works whether
or not the switch is on. The card
also shows the return address and the realm Steam's sign-in page names, whether the key is set and
whether Steam refused it, when Steam was last asked and for how many people it answered, and the
calls made today. It never shows anybody's status: the card works for an admin who is locked, and
locked people see no Steam status. A person whose Mattermost account is deactivated is marked so
(or "account deleted", shown by user id, when Mattermost no longer has it), and Steam is not asked
about them. At most 100 people can connect Steam, which is what one of
Steam's calls can ask about, so the server makes one call a minute whatever the number: about 1,440
a day, of the 100,000 Steam's terms allow. When something is wrong (the return address, the key, Steam
refusing it, asking the plugin to slow down, or out of reach for three checks running), a system admin
also sees a line saying so at the top of the panel once it is unlocked. Steam's "slow down" is waited
out, two minutes and then longer, up to ten; a refused key stops the minute's checks until the Steam settings
change.

While the switch is off, nothing is asked of Steam, nobody can connect, and no Steam status shows.
Connections are kept, and come back when it is switched on again; people already connected can still
disconnect. Delete all Steam data is the way to remove them for good.

**What a Steam connection is not.** It is not a link: it unlocks nothing and lets nobody in.

## Hytale (optional)

**Off unless an admin switches it on.** With **Show people's Hytale names** on, people who have
unlocked the panel can connect their Hytale account with **Connect** on the **Hytale** row of
**Connected accounts**, in the link key's sheet. From then on, everyone who can open the panel sees
the Hytale username they picked, in their profile popover, with a **Copy** button, so friends can
find each other in-game.

**What it cannot show.** Hytale tells other apps who somebody is and nothing else: not whether they
are playing, nor on which server. The plugin does not know where anybody is in Hytale, and does not
pretend to.

**Setting it up.** At accounts.hytale.com, under Third-party developer apps, apply for an
application: a **confidential** client, the scopes **openid** and **hytale:profile**, and as its
Redirect URI exactly `<Site URL>/plugins/ceo.villa.matty/api/v1/hytale/callback` (the **Hytale
connections** card, in System Console > Plugins > Matty > Hytale, shows it with **Copy**), with no
trailing slash and no query. Hytale's staff review it. Then paste the **Client ID** and **Client Secret** into the **Hytale** section, and switch
on **Show people's Hytale names**. Hytale needs the Site URL on https (only `http://localhost` is
allowed otherwise).

**Connecting.** Press **Connect** on the Hytale row, read what it means, then **Continue on Hytale**.
A small window opens at Hytale's own sign-in page; sign in, pick the game profile to show, and
approve. The plugin never sees your password. One profile per person; connecting again lets you pick
another. If somebody else proves the same profile, it moves to them, and you see a notice. A profile
you rename in Hytale shows its old name here until you connect again. Some of Hytale's refusals (an
application it does not know, or a Redirect URI that does not match) are shown on Hytale's own page
and never come back; after a minute the sheet says that an admin needs to check the application.

**What everybody sees, and what the plugin keeps.** Everyone who can open the panel on this server
(never anybody locked) sees the username of the profile you picked. The plugin keeps that username,
the profile's id and when you connected, in this server's database. The token Hytale issues is used
once, to read the profile, and revoked at once; the plugin never keeps a Hytale password or token. Hytale's
sign-in page is Hytale's own: it loads Cloudflare's Turnstile check and, if you accept Hytale's
cookies, Cloudflare's analytics, and reports to Hytale's error tracker, which records a share of
visits; it may also offer signing in with Google, Microsoft, Discord, Twitch or Epic Games, and
picking one takes you to that service. **Disconnect** deletes what the plugin keeps, even while an
admin has Hytale switched off, including any notice that names your profile, with the same
set-aside-copy exception as Steam. The address Hytale sends your browser back to carries only a
one-time code, which is useless without a secret that never leaves the server, and nothing about
who you are.

**If the plugin is removed.** Like all of the plugin's data, Hytale connections stay in the database
if the plugin is removed. An admin removing the plugin for good presses **Delete all Hytale data**
first.

**What an admin controls.** As for Steam, in System Console > Plugins > Matty > Hytale, on the
**Hytale connections** card: the Redirect URI with **Copy**, whether the Client ID and Client Secret are set and
whether Hytale refused them or has not granted the scope hytale:profile yet, who has connected,
**Disconnect**, **Disconnect and block**, the blocked list, and **Delete all Hytale data**. At most
100 people can connect Hytale. Delete all Hytale data refuses a connection still being made when it
is pressed, as Steam's does. When Hytale refuses the application or the scope, a system admin also
sees a line saying so at the top of the panel once it is unlocked, until somebody connects or the
Hytale settings are changed. While the switch is off, nothing
is asked of Hytale, nobody can connect and no Hytale names show; people already connected can still
disconnect. A Hytale connection unlocks nothing either.
